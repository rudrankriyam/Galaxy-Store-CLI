# Buyer comments and replies

Listing comments is read-only. Add `--paginate` only when you want every page:

```sh
gsc reviews list \
  --content-id 000000000000 \
  --profile production \
  --page 1 \
  --output table
```

A reply needs the comment's country code as well as its seven-digit ID. This
dry run checks the text and prints the proposed write:

```sh
gsc reviews reply \
  --content-id 000000000000 \
  --comment-id 0000000 \
  --country-code USA \
  --body "Thanks for the report. We are checking it." \
  --profile production \
  --dry-run \
  --output json
```

Samsung allows one seller reply per comment. Replacing one requires a separate,
confirmed deletion followed by a confirmed reply.

