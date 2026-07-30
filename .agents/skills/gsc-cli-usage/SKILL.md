---
name: gsc-cli-usage
description: Use the unofficial Galaxy Store CLI safely and predictably, including command discovery, service-account authentication, profiles, output formats, exit codes, dry runs, and confirmations. Use when an agent needs to inspect or operate Galaxy Store through `gsc`, design CI commands, diagnose a `gsc` invocation, or determine whether an operation is API-supported or Seller Portal-only.
---

# Galaxy Store CLI usage

## Discover before acting

Run help at each command level:

```bash
gsc --help
gsc apps --help
gsc apps list --help
```

When the workflow is known but the command is not, use the embedded catalog:

```bash
gsc search "staged rollout"
gsc capabilities --output table
gsc schema --output json
```

Treat `capabilities` as the support boundary. Do not claim that `gsc` can create
a new app record, create a Samsung service account, register subscription
products, change categories, or retrieve settlements when the catalog marks
those workflows Seller Portal-only.

## Authenticate

Create a named profile explicitly:

```bash
gsc auth login \
  --profile production \
  --service-account-id "$SAMSUNG_SERVICE_ACCOUNT_ID" \
  --private-key "$SAMSUNG_PRIVATE_KEY_PATH" \
  --scope publishing \
  --scope gss
gsc auth status --profile production --output json
```

`auth login` is the only normal command that signs a JWT and exchanges it for
an access token. Other authenticated commands require an existing token and
must not mint one implicitly.

For ephemeral CI, set both variables together:

```bash
export GSC_ACCESS_TOKEN="..."
export GSC_SERVICE_ACCOUNT_ID="..."
```

Select profiles with `--profile` or `GSC_PROFILE`. Never print access tokens,
private keys, authorization headers, keychain values, or credential-bearing
requests.

## Choose output for the consumer

- Omit `--output` for TTY-aware behavior.
- Use `--output json` for scripts and CI.
- Use `--output table` or `--output markdown` only for people.
- Do not parse tables.
- Rely on unknown-field preservation only where the command explicitly
  documents lossless raw JSON. Some commands intentionally return curated
  result envelopes.

Prefer canonical verbs: `list`, `view`, `create`, `update`, and `delete`.
Never silently select one app record when Samsung returns both `SALE` and
`REGISTRATION`.

## Apply mutation safety

Validate identifiers and local files before opening credentials or making a
network request. For a mutation:

1. Run the command with `--dry-run` where offered.
2. Review the plan and warnings.
3. Read current remote state again when ambiguity matters.
4. Repeat with `--confirm`.

Never include `binaryList` in app metadata updates. Use the v2 binary commands.
Treat rollout completion as global completion, not pause. Treat cancel, refund,
and revoke as different IAP effects requiring separate authorization.

## Handle failures

Keep stdout for data and stderr for diagnostics. Interpret stable process exit
codes:

- `0`: success
- `1`: generic failure
- `2`: invalid usage or local input
- `3`: authentication failure
- `4`: not found
- `5`: conflict
- `6`: validation or precondition failure
- `7`: another HTTP or API failure
- `130`: interrupted or canceled

Do not retry mutations blindly. Read current state and reconcile after an
ambiguous timeout.
