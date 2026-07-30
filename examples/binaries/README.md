# Uploading and registering a binary

Galaxy Store handles the file upload and binary registration as separate
operations. Preview each step without opening a session:

```sh
gsc uploads sessions create \
  --profile production \
  --dry-run \
  --output json

gsc uploads file \
  --session-id "$GSC_UPLOAD_SESSION_ID" \
  --file "$GSC_BINARY_PATH" \
  --profile production \
  --dry-run \
  --output json

gsc binaries add \
  --content-id 000000000000 \
  --file-key "$GSC_UPLOADED_FILE_KEY" \
  --gms N \
  --profile production \
  --dry-run \
  --output json
```

A real upload-session response supplies `GSC_UPLOAD_SESSION_ID`; the real file
upload then returns the file key used by `binaries add`. `--gms` must be `Y` or
`N`.

Updating an existing binary changes its GMS declaration. It does not upload a
replacement file:

```sh
gsc binaries update \
  --content-id 000000000000 \
  --binary-seq 42 \
  --gms N \
  --profile production \
  --dry-run
```

Do not put `binaryList` in a `contentUpdate` payload. Samsung's current flow
uses `/seller/v2/content/binary`.

