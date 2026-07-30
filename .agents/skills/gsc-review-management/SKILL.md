---
name: gsc-review-management
description: Read, paginate, triage, reply to, and delete seller replies for Galaxy Store buyer reviews with the unofficial `gsc` CLI. Use when an agent needs to inspect customer feedback, find an exact review, prepare or post a response, or remove an existing seller response safely.
---

# Galaxy Store review management

## Read reviews before replying

Use a profile with the `publishing` scope. List the first page as JSON:

```bash
gsc reviews list \
  --content-id 000007654321 \
  --page 1 \
  --profile production \
  --output json
```

Use `--paginate` to retrieve every page beginning at `--page`:

```bash
gsc reviews list \
  --content-id 000007654321 \
  --page 1 \
  --paginate \
  --profile production \
  --output json
```

Use an exact seven-digit comment ID to retrieve one review:

```bash
gsc reviews list \
  --content-id 000007654321 \
  --comment-id 1234567 \
  --profile production \
  --output json
```

Do not parse table output for automation. Preserve the exact `commentId`,
country, rating, body, and current seller-reply state when preparing a response.
Samsung ratings use a 1–10 scale; two rating points correspond to one star.

## Prepare a seller reply

Draft a reply that directly addresses the buyer's report and does not disclose
private account data. Confirm these command constraints:

- `--comment-id` is exactly seven digits.
- `--country-code` is exactly three uppercase letters.
- `--body` is non-empty and no more than 1,400 UTF-8 bytes.
- Samsung permits one seller reply per comment.

Quote shell metacharacters safely. Prefer a shell variable for multiline or
punctuation-heavy text:

```bash
REPLY_BODY="Thanks for reporting this. We fixed the sign-in issue in the latest update."
```

Preview the reply without opening a remote mutation:

```bash
gsc reviews reply \
  --content-id 000007654321 \
  --comment-id 1234567 \
  --country-code USA \
  --body "$REPLY_BODY" \
  --profile production \
  --output json \
  --dry-run
```

Show the draft to the user when the request is draft-only. Do not interpret a
request to write or refine a response as authorization to post it.

## Post an authorized reply

After the user approves the exact text, repeat the command with `--confirm`:

```bash
gsc reviews reply \
  --content-id 000007654321 \
  --comment-id 1234567 \
  --country-code USA \
  --body "$REPLY_BODY" \
  --profile production \
  --output json \
  --confirm
```

Read the exact review again afterward and verify the returned seller reply. If
the request times out ambiguously, re-read before retrying; Samsung permits only
one reply and a blind retry can become a conflict.

## Delete an existing seller reply

First retrieve the exact comment and confirm that its seller reply is the
intended target. Preview deletion:

```bash
gsc reviews reply delete \
  --content-id 000007654321 \
  --comment-id 1234567 \
  --profile production \
  --output json \
  --dry-run
```

Delete only with explicit authorization:

```bash
gsc reviews reply delete \
  --content-id 000007654321 \
  --comment-id 1234567 \
  --profile production \
  --output json \
  --confirm
```

Re-read the comment afterward. Treat reply creation and reply deletion as
separate authorizations.
