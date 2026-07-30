# Authentication

Create service accounts and download their private keys in Seller Portal. Keep
the key outside the repository. The dry run below checks the inputs and prints
the token-exchange plan without contacting Samsung:

```sh
gsc auth login \
  --profile production \
  --service-account-id "$GSC_SERVICE_ACCOUNT_ID" \
  --private-key "$GSC_PRIVATE_KEY_PATH" \
  --scope publishing \
  --scope gss \
  --dry-run
```

After a real login, these read-only commands inspect the selected profile and
ask Samsung to validate its stored token:

```sh
gsc auth status --profile production --output json
gsc doctor --profile production --remote --output table
```

Token revocation is permanent. Preview it first:

```sh
gsc auth revoke --profile production --dry-run --output json
```

