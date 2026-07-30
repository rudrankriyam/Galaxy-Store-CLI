# Architecture

Galaxy Store CLI borrows the command and automation contracts of
[App Store Connect CLI](https://github.com/rorkai/App-Store-Connect-CLI), then
maps them to Samsung's resource names, API hosts, and state rules.

The rule is simple: ASC-like at the command boundary, Samsung-native at the
protocol boundary.

## Current command contract

- Commands use long flags and never prompt.
- Read-only commands don't require confirmation.
- Writes validate local input before the first side effect.
- Store, commerce, upload, review, rollout, and raw API writes need
  `--dry-run` or `--confirm`; dry-run wins if both appear.
- Dry-run can read state needed to validate a plan, but it cannot write remote
  or local state.
- Command data goes to stdout. Errors and diagnostics go to stderr.
- `--output auto` selects a stable table only for typed row data on an
  interactive terminal. Other results, pipes, and CI receive minified JSON.
- `--output json|table|markdown` requests a fixed representation.
- Ctrl-C cancels the active request and returns exit code 130.

`auth login` is the bootstrap exception. It offers `--dry-run`, but exchanging
and storing the first token doesn't require a separate `--confirm`.

Canonical resource verbs are `list`, `view`, `create`, `update`, and `delete`.
Samsung lifecycle terms such as `submit`, `complete`, `acknowledge`, `refund`,
and `revoke` remain distinct because they carry different effects.

## Implemented command tree

This tree matches `cmd/root.go` and the constructors under `internal/cli`.

```text
gsc
├── auth
│   ├── login
│   ├── status
│   └── revoke
├── apps
│   ├── list
│   ├── view
│   ├── update
│   ├── submit
│   └── status
│       └── update
├── binaries
│   ├── add
│   ├── update
│   └── delete
├── uploads
│   ├── sessions
│   │   └── create
│   └── file
├── beta
│   └── testers
│       ├── list
│       └── update
├── rollouts
│   ├── rate
│   │   ├── view
│   │   ├── update
│   │   └── complete
│   └── binaries
│       ├── list
│       └── update
├── reviews
│   ├── list
│   └── reply
│       └── delete
├── iap
│   ├── items
│   │   ├── list
│   │   ├── view
│   │   ├── create
│   │   ├── replace
│   │   ├── update
│   │   └── delete
│   ├── purchases
│   │   ├── consume
│   │   └── acknowledge
│   ├── subscriptions
│   │   ├── status
│   │   ├── cancel
│   │   ├── refund
│   │   └── revoke
│   ├── orders
│   │   └── list
│   └── receipts
│       └── verify
├── stats
│   ├── seller
│   └── content
├── api
│   └── request
├── doctor
├── capabilities
├── search
├── schema
└── version
```

`reviews reply` creates a reply and also owns the nested `reply delete`
operation. Receipt verification sits under `iap`, but it calls Samsung's public
receipt endpoint without a Developer API token.

## Primitives now, orchestration later

The implemented commands are direct, independently usable API operations.
Shipping an app currently means calling the primitives in order: create an
upload session, upload a file, register the binary, update content, then submit.
Each mutation produces its own dry-run plan and requires its own confirmation.

The repository does not register commands for these higher-level jobs yet:

- metadata pull, diff, validation, and apply;
- a single app plan or ship command;
- submission and status waiting with recovery;
- resumable multi-step workflows;
- shell completion.

Future orchestration should compose service interfaces in-process. It must not
shell out to `gsc`, bypass the existing confirmation boundary, or turn a timed
out write into an automatic retry.

## Package boundaries

```text
cmd/                         process boundary, root parsing, exit codes
internal/cli/                flag parsing, validation, command composition
internal/cli/shared/         mutation mode, dry-run plan, usage errors
internal/catalog/            checked-in operation and limitation catalog
internal/config/             non-secret named-profile metadata
internal/credentials/        environment, config, and keychain resolution
internal/output/             JSON, table, and Markdown rendering
internal/samsung/            HTTP client, auth, errors, and API services
internal/session/            resolved authenticated client sessions
```

The current API service packages cover apps, auth, beta testers, content and
uploads, IAP families, public receipts, reviews, rollouts, and statistics.
Command packages validate flags, open the required service only after local
checks, call one service operation, and render the result.

No `internal/workflow`, `internal/plan`, or metadata-sync package exists yet.
Those names describe planned work, not hidden functionality.

## Authentication and profiles

The Developer API uses OAuth 2.0 service-account credentials:

1. `gsc auth login` signs an RS256 JWT containing `iss`, `scopes`, `iat`, and
   `exp`.
2. The JWT lasts 10 minutes by default and never exceeds Samsung's 20-minute
   maximum.
3. The CLI exchanges it at `POST /auth/accessToken`.
4. It stores the returned access token in the operating-system credential
   manager.

Authenticated API requests send:

```http
Authorization: Bearer <access-token>
service-account-id: <service-account-id>
```

The config file contains profile name, service-account ID, private-key path,
and scopes. It never contains a private key or access token.

Credential resolution uses this fixed order:

1. complete credentials passed by the caller;
2. an explicit `--profile`;
3. the complete `GSC_ACCESS_TOKEN` and `GSC_SERVICE_ACCOUNT_ID` pair;
4. `GSC_PROFILE`;
5. the configured default profile, or the sole configured profile.

A selected source that is incomplete returns an error. The resolver doesn't
mix part of one source with values from another.

`gsc doctor` checks local runtime, config, and credential state. It makes a
network request only with `--remote`, which validates the existing token rather
than minting a new one.

## HTTP operations

Every known operation in the checked-in catalog records its API family, scope,
method, host, path, authentication policy, read or mutation classification,
retry policy, command mapping or documented limitation, and official Samsung
source.

The authenticated host is `https://devapi.samsungapps.com`. Multipart uploads
use `https://seller.samsungapps.com/galaxyapi/`; public receipt verification
uses `https://iap.samsungapps.com`. `gsc api request` accepts only relative,
catalog-backed paths on the allowed Samsung hosts and owns authentication
headers itself.

GET and HEAD requests may retry bounded transport failures, HTTP 408, HTTP 429,
and selected 5xx responses while honoring `Retry-After`. Mutations and
multipart uploads aren't blindly replayed. A future workflow that sees an
ambiguous write timeout must read current state before deciding whether a new
write is safe.

## Samsung-specific safety

### App state

A 12-digit `contentId` can have `SALE` and `REGISTRATION` records at the same
time. Reads can return both. Rollout and beta commands require
`--app-status` so the target variant stays explicit.

### Metadata presence

Samsung metadata isn't ordinary PATCH data:

- an omitted field means don't change it;
- `null` preserves documented collections;
- `[]` intentionally removes every item;
- a populated collection replaces or updates its contents.

Metadata updates preserve these states. They reject `binaryList`; binary
changes belong to the dedicated v2 commands.

### Binary and upload lifecycle

Samsung rejects `contentUpdate.binaryList` beginning in July 2026. `gsc`
registers, updates, and deletes binaries through `/seller/v2/content/binary`.

Uploads stream from disk rather than loading an APK or AAB into memory. The file
must be regular, non-symlinked, and non-empty. Upload sessions and file transfer
remain separate commands, so scripts can retain Samsung's returned session ID
between steps.

### Rollouts, beta, and commerce

- A deployed staged-rollout percentage cannot decrease.
- Samsung's "disable rollout" operation means complete distribution globally,
  so the CLI names it `rollouts rate complete`.
- Closed-beta responses may succeed at the request level while individual
  tester updates fail; the command inspects each result.
- IAP cancel, refund, and revoke stay separate, confirmed operations.
- Orders and statistics use POST for reads. The catalog classification, not the
  HTTP verb alone, decides whether confirmation is required.

## Output and exit codes

The renderer writes one JSON value followed by a newline when JSON is selected.
Table columns keep a stable order, and Markdown output escapes cell separators
and line breaks. Unsupported table projections fail rather than printing a
lossy representation.

| Code | Meaning |
| ---: | --- |
| `0` | success |
| `1` | other error or unhealthy diagnostics |
| `2` | invalid flags, arguments, or local input |
| `3` | authentication or authorization |
| `4` | not found |
| `5` | conflict |
| `6` | confirmation required, or Samsung HTTP 400/422 |
| `7` | another HTTP failure |
| `130` | interrupted |

Samsung HTTP 400 and 422 map to validation; 401 and 403 map to authentication;
404 maps to not found; 409 maps to conflict. Other HTTP failures use code 7.

## Discovery and API drift

Samsung doesn't publish one dependable OpenAPI document for the full surface.
The checked-in operation catalog supplies data for:

- `gsc capabilities`, including Seller Portal-only boundaries;
- `gsc search`, which finds operations and mapped commands;
- `gsc schema`, which describes catalog provenance and fields;
- coverage tests and later API-drift checks.

The catalog documents support; it doesn't turn an unimplemented operation into
a working command.

## Seller Portal-only boundaries

Samsung exposes no public API for initial app creation, commercial seller
enrollment, service-account creation, subscription product registration,
category changes, or settlement reports. Scripts must stop at those edges and
send the operator to Seller Portal. Browser automation isn't part of `gsc`.

The dated official-source and competitor audit remains in
[RESEARCH.md](RESEARCH.md).

## Testing

API service tests assert exact method, path, query, headers, body, response
decoding, and Samsung error envelopes with local HTTP servers. Command tests
cover validation order, dry-run behavior, confirmation, output routing, and the
registered root tree.

Cross-package suites exercise cancellation, retry rules, JWT claims, secret
redaction, pagination, metadata presence, and streaming uploads. Live tests
remain opt-in and read-only unless a user authorizes a named mutation.

## Distribution without a release

The repository contains packaging validation, not a published distribution:

- GoReleaser snapshot archives for macOS, Linux, and Windows on amd64 and arm64;
- SHA-256 checksums, per-archive SBOMs, and source snapshots;
- a Homebrew HEAD formula tested through an isolated local tap;
- generated WinGet candidate manifests and local schema checks;
- a guarded manual workflow that can create a draft from an existing tag.

Snapshot workflows use read-only repository permissions and upload short-lived
CI artifacts. They cannot create a tag, GitHub Release, Homebrew tap commit, or
WinGet submission. The manual workflow requires an existing tag, typed
confirmation, and signing secrets; even then it creates a draft only.

No tag, package-manager submission, or public release belongs to the current
goal. [DISTRIBUTION.md](DISTRIBUTION.md) records the exact guardrails.
