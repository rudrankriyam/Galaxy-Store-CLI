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

## Plan and run the typed pipeline

Prepare a canonical three-file metadata bundle with `$gsc-metadata-sync`.
Then build the deterministic plan entirely offline:

```bash
gsc ship plan \
  --content-id 000007654321 \
  --binary app-release.aab \
  --metadata-dir metadata \
  --gms Y \
  --output json
```

Preview the complete run without opening credentials, creating a checkpoint,
or changing local or remote state:

```bash
gsc ship run \
  --content-id 000007654321 \
  --binary app-release.aab \
  --metadata-dir metadata \
  --gms Y \
  --profile production \
  --output json \
  --dry-run
```

Review the exact target, hashes, ordered steps, metadata diff, and destructive
changes. Only after the user explicitly authorizes this exact plan, repeat the
same command with `--confirm` instead of `--dry-run`.

`ship run` is fixed to the `REGISTRATION` variant. It uploads and registers one
binary, applies metadata with drift and readback checks, then submits for
review. It stores private resumability state under
`.gsc/ship-000007654321.json` by default, reconciles that checkpoint before
continuing, and never changes distribution to `FOR_SALE`.

## Manual primitive fallback

Use the following lower-level commands only when diagnosing a step or when the
user has separately authorized an individual operation.

### Upload the binary

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

### Register the uploaded binary

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

### Update metadata

Prefer `$gsc-metadata-sync` and its canonical three-file bundle. The raw
`apps update` command below remains an escape hatch for an individually
reviewed `contentUpdate` payload.

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

### Verify and submit for review

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

Wait for the exact pending variant with a bounded timeout:

```bash
gsc apps status wait \
  --content-id 000007654321 \
  --app-status REGISTRATION \
  --until READY_FOR_SALE \
  --interval 15s \
  --timeout 30m \
  --profile production \
  --output json
```

Use `gsc apps view` afterward when the full record is needed. Inspect Samsung's
returned status instead of inferring it from command success. When the app is
approved and the user explicitly authorizes publication, move it to sale:

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
