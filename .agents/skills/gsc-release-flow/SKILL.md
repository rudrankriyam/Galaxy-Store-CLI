---
name: gsc-release-flow
description: Prepare, upload, register, submit, monitor, and distribute an existing Galaxy Store app update with the unofficial `gsc` CLI. Use when an agent needs to ship an APK or AAB, update Galaxy Store metadata, submit an app for review, or move an approved app to sale without conflating those separate actions.
---

# Galaxy Store release flow

## Confirm the support boundary

Use this workflow for an existing Galaxy Store content record. Creating the
first app record, creating a Samsung service account, changing the category,
and managing subscription products remain Seller Portal-only.

Use a profile with the `publishing` scope:

```bash
gsc auth status --profile production --output json
gsc apps view \
  --content-id 000007654321 \
  --profile production \
  --output json
```

Read the returned app variants carefully. Samsung may expose both `SALE` and
`REGISTRATION`; never merge or silently choose between them.

## Upload the binary

Samsung upload sessions are valid for 24 hours. Preview and create a session:

```bash
gsc uploads sessions create \
  --profile production \
  --output json \
  --dry-run

gsc uploads sessions create \
  --profile production \
  --output json \
  --confirm
```

Capture the returned session ID from JSON. Validate the local APK or AAB path,
then upload it:

```bash
gsc uploads file \
  --session-id UPLOAD_SESSION_ID \
  --file app-release.aab \
  --profile production \
  --output json \
  --dry-run

gsc uploads file \
  --session-id UPLOAD_SESSION_ID \
  --file app-release.aab \
  --profile production \
  --output json \
  --confirm
```

Capture the returned file key. Do not parse human-readable table output in
automation.

## Register the uploaded binary

Register the file with Samsung's v2 binary API:

```bash
gsc binaries add \
  --content-id 000007654321 \
  --file-key FILE_KEY \
  --gms Y \
  --profile production \
  --output json \
  --dry-run

gsc binaries add \
  --content-id 000007654321 \
  --file-key FILE_KEY \
  --gms Y \
  --profile production \
  --output json \
  --confirm
```

Use `--gms Y` only when the binary uses Google Mobile Services. To retain an
existing binary's device targeting, add:

```text
--copy-device-config-from EXISTING_BINARY_SEQUENCE
```

Use `gsc binaries update --content-id ID --binary-seq SEQ --gms Y|N` only to
change the binary's GMS metadata. Use `gsc binaries delete` only with explicit
authorization because deletion is permanent.

Never put `binaryList` in an app metadata update. Binary operations belong to
the v2 `gsc binaries` commands.

## Update metadata

Build the JSON file from the current app state. Every update file must preserve
the current `contentId`, `defaultLanguageCode`, `paid`, and `publicationType`,
then add the intended changes. The file is the `contentUpdate` object itself:

```json
{
  "contentId": "000007654321",
  "defaultLanguageCode": "ENG",
  "paid": "N",
  "publicationType": "03",
  "contentName": "Example App",
  "contentDescription": "Updated store description"
}
```

Preserve the difference between an omitted field, `null`, and `[]`; Samsung may
interpret them differently. `gsc` rejects `binaryList` and validates the four
required envelope fields, but otherwise passes metadata fields through to
Samsung as raw JSON. Preview and confirm:

```bash
gsc apps update \
  --content-id 000007654321 \
  --file content-update.json \
  --profile production \
  --output json \
  --dry-run

gsc apps update \
  --content-id 000007654321 \
  --file content-update.json \
  --profile production \
  --output json \
  --confirm
```

Updating a live app prepares a parallel `REGISTRATION` record; it does not
replace the current `SALE` record immediately.

## Verify and submit for review

Read the app again and verify the registering record, metadata, and binary:

```bash
gsc apps view \
  --content-id 000007654321 \
  --profile production \
  --output json
```

Submission is a separate mutation:

```bash
gsc apps submit \
  --content-id 000007654321 \
  --profile production \
  --output json \
  --dry-run

gsc apps submit \
  --content-id 000007654321 \
  --profile production \
  --output json \
  --confirm
```

Submit only a `REGISTERING` app that is ready for Samsung review. Do not treat
successful submission as approval or live distribution.

## Monitor and distribute

Poll with bounded intervals:

```bash
gsc apps view \
  --content-id 000007654321 \
  --profile production \
  --output json
```

Inspect Samsung's returned status instead of inferring it from command success.
When the app is approved and the user explicitly authorizes publication, move
it to sale:

```bash
gsc apps status update \
  --content-id 000007654321 \
  --status FOR_SALE \
  --profile production \
  --output json \
  --dry-run

gsc apps status update \
  --content-id 000007654321 \
  --status FOR_SALE \
  --profile production \
  --output json \
  --confirm
```

`FOR_SALE`, `SUSPENDED`, and `TERMINATED` have materially different effects.
Never infer authorization for distribution, suspension, or termination from
authorization to upload or submit.

After any ambiguous timeout, re-read the app before retrying. A successful CLI
request is not proof that the app is publicly available; report the exact
remote state returned by Samsung.
