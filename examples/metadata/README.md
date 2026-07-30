# Metadata bundle

The [bundle](bundle) directory shows the three files that keep editable
metadata tied to the exact Samsung record it came from:

- `metadata.json` contains only fields accepted by `contentUpdate`.
- `source.json` preserves the selected `contentInfo` record, including
  response-only fields.
- `manifest.json` records the `SALE` or `REGISTRATION` variant and the
  canonical SHA-256 of `source.json`.

`metadata.json` deliberately omits `appStatus`, `contentStatus`, and
`binaryList`. Array values need care: an omitted field, `null`, and `[]` can
produce different updates.

Validate this sample entirely offline:

```sh
gsc metadata validate \
  --dir examples/metadata/bundle \
  --output json
```

Treat `source.json` as an immutable snapshot. Edit `metadata.json`, then inspect
the live semantic diff:

```sh
gsc metadata diff \
  --content-id 000000000000 \
  --app-status REGISTRATION \
  --dir examples/metadata/bundle \
  --profile production \
  --output table
```

`metadata diff` reads Samsung but does not write. `metadata apply --dry-run`
adds drift checks and prints the mutation plan without sending
`contentUpdate`.
