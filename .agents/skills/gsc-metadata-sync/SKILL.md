---
name: gsc-metadata-sync
description: Pull, validate, diff, and safely apply metadata for an existing Galaxy Store app with the unofficial `gsc` CLI. Use when an agent needs to sync a Galaxy Store listing, edit a metadata bundle, compare local metadata with Samsung, diagnose metadata drift, or prepare a reviewed `contentUpdate` without confusing SALE and REGISTRATION variants.
---

# Galaxy Store metadata sync

Use the canonical three-file bundle and select one exact Samsung app variant.
Treat inspection, editing, applying, review submission, and distribution as
separate authorization states.

## Establish the target

Require the existing 12-digit content ID and an explicit app status:

```bash
CONTENT_ID=000007654321
APP_STATUS=REGISTRATION
PROFILE=PROFILE_NAME
```

Use only `SALE` or `REGISTRATION`. Samsung may return both for one content ID;
never infer, merge, or silently choose a variant. Prefer `REGISTRATION` only
when the user actually intends to edit the pending version. Selecting a variant
does not authorize a mutation.

Verify the named profile without exposing credentials:

```bash
gsc auth status --profile "$PROFILE" --output json
```

## Pull a lossless bundle

Pull the exact live record before editing:

```bash
gsc metadata pull \
  --content-id "$CONTENT_ID" \
  --app-status "$APP_STATUS" \
  --profile "$PROFILE" \
  --dir metadata \
  --output json
```

The directory contains:

- `manifest.json`: selected identity, source hash, schema, and pull time
- `metadata.json`: safe editable `contentUpdate` envelope
- `source.json`: lossless selected `contentInfo` response

Edit only `metadata.json`. Do not hand-edit the manifest or source snapshot.
Pull refuses to replace an existing bundle. Use `--force` only when the user
explicitly authorizes discarding that complete bundle; otherwise use a new
directory so local edits remain recoverable.

## Preserve Samsung's field semantics

Keep `contentId`, `defaultLanguageCode`, `paid`, and `publicationType` valid.
Never add `binaryList`; manage binaries with `gsc binaries` because Samsung's
current metadata endpoint does not accept that field.

Treat JSON states deliberately:

- Omitted field: do not send or change that field.
- `null`: preserve the current value for Samsung's tri-state collections.
- `[]`: explicitly clear the collection and treat the plan as destructive when
  live values exist.
- Populated array: replace the collection; review removals as destructive.

The tri-state collection guard applies to `addLanguage`, `screenshots`, and
`sellCountryList`. Do not convert omitted values, `null`, and empty arrays into
one another during formatting or scripting. Omitted and `null` may produce no
planned remote change for these collections, but they preserve different
request shapes.

## Validate and inspect

Validate offline before opening a session:

```bash
gsc metadata validate --dir metadata --output json
```

Confirm the validated `contentId` and `appStatus` match the intended target.
Local validation proves bundle integrity and request shape, not Samsung review
eligibility; an intentionally cleared collection may still need content before
submission.

Then fetch the exact live variant and calculate a deterministic semantic plan:

```bash
gsc metadata diff \
  --content-id "$CONTENT_ID" \
  --app-status "$APP_STATUS" \
  --profile "$PROFILE" \
  --dir metadata \
  --output json
```

Review every change path, `kind`, `before`, `after`, and `destructive` value.
An empty `changes` array is a no-op. `diff` reads remote state but does not
mutate it.

## Preview and apply safely

Preview through the apply path so the source hash, current live record, and
confirmation plan are checked together:

```bash
gsc metadata apply \
  --content-id "$CONTENT_ID" \
  --app-status "$APP_STATUS" \
  --profile "$PROFILE" \
  --dir metadata \
  --output json \
  --dry-run
```

Do not run a live apply merely because validation or dry-run succeeds. Require
explicit user authorization for the exact content ID, app status, and reviewed
plan. When authorized, repeat with `--confirm`:

```bash
gsc metadata apply \
  --content-id "$CONTENT_ID" \
  --app-status "$APP_STATUS" \
  --profile "$PROFILE" \
  --dir metadata \
  --output json \
  --confirm
```

Apply refuses source drift rather than overwriting unseen remote changes. If it
reports drift, do not retry or force the old bundle. Pull the latest exact
variant into a new directory, reapply intended edits, validate, and review a
fresh diff.

A confirmed apply performs one `contentUpdate`, fetches the same explicit
variant again, and succeeds only when readback matches the desired envelope.
Require `readbackVerified: true` and `mutationsPerformed: true` before reporting
the metadata update as verified. A successful metadata apply does not submit
for review or publish the app.

## Keep the run read-only when authorization is absent

For audits, examples, CI preparation, or local validation requests, stop after
`validate`, `diff`, or `apply --dry-run`. Do not use `--confirm`, submit for
review, change distribution status, upload binaries, or replace an existing
bundle without separate explicit authorization. Never print credentials or
include them in metadata files.
