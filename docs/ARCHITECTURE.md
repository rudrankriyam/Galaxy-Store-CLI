# Architecture

Galaxy Store CLI applies the mature interaction contracts of
[App Store Connect CLI](https://github.com/rorkai/App-Store-Connect-CLI) to
Samsung's APIs without carrying over Apple-specific resource models.

The governing rule is: ASC-like at the command boundary, Samsung-native at the
protocol boundary.

## Command contract

- Commands use long flags and do not prompt.
- Read-only commands are safe by default.
- Mutations validate every local input before the first side effect.
- `--dry-run` may read remote state but cannot create an upload session, upload
  a file, change content, submit, publish, or alter rollout state.
- Destructive or externally visible actions require `--confirm`.
- When both flags are supplied, dry-run wins.
- Data goes to stdout. Diagnostics, progress, warnings, and retries go to
  stderr.
- Interactive stdout defaults to a stable table when a typed table projection
  exists. Pipes and CI default to minified JSON.
- Explicit `--output json|table|markdown` overrides detection.
- Exit codes are deterministic: success, generic error, usage, authentication,
  not found, conflict, validation/precondition, then mapped HTTP failures.

Canonical leaf verbs are `list`, `view`, `create`, `update`, `delete`, and
domain-specific lifecycle actions. `get` is not a canonical leaf.

## Package boundaries

```text
cmd/                         process boundary, root parsing, exit codes
internal/cli/                thin command packages
internal/catalog/            embedded operation and capability catalog
internal/config/             non-secret profile metadata
internal/credentials/        keychain and environment resolution
internal/output/             JSON, table, and Markdown rendering
internal/patch/              presence-aware metadata fields
internal/plan/               durable mutation plans
internal/samsung/            HTTP, auth, errors, and API families
internal/upload/             bounded streaming multipart upload
internal/workflow/           validation, execution, state, and resume
```

Command packages parse flags, validate inputs, call a service, and render a
result. They do not build ad-hoc HTTP requests. High-level shipping workflows
compose service interfaces rather than invoking other CLI commands.

## Command taxonomy

```text
gsc auth
gsc doctor
gsc apps
gsc binaries
gsc uploads
gsc metadata
gsc submit
gsc publish
gsc status
gsc beta
gsc rollouts
gsc reviews
gsc iap
gsc stats
gsc workflow
gsc api
gsc capabilities
gsc schema
gsc search
gsc completion
gsc version
```

Primitive one-to-one API commands remain independently usable. Higher-level
commands add safe orchestration:

- metadata pull, diff, validate, and apply
- upload session, streaming upload, v2 binary registration, and readback
- app plan, ship, submit, and status wait
- rollout start, advance, and complete

## Authentication

The Galaxy Store Developer API uses OAuth 2.0 server-to-server credentials:

1. Sign an RS256 JWT containing `iss`, `scopes`, `iat`, and `exp`.
2. Keep JWT lifetime at 10 minutes by default and never over Samsung's
   20-minute limit.
3. Exchange it at `POST /auth/accessToken`.
4. Use the returned access token until it is revoked.

Normal authenticated requests send:

```http
Authorization: Bearer <access-token>
service-account-id: <service-account-id>
```

Credential resolution is deterministic:

1. explicitly selected named profile
2. complete CI environment credential pair
3. active keychain-backed profile
4. explicitly configured private-key exchange

A selected source that fails is an error. The CLI does not silently fall
through to another identity. Access tokens live in the operating-system
keychain where available; private keys and tokens must never appear in config,
logs, errors, telemetry, or command output.

## HTTP operations

Each API operation is cataloged with:

- stable ID
- API family and scope
- method, host, and path
- authentication policy
- read versus mutation semantics
- retry policy
- command or documented limitation
- official Samsung source URL and verification date

The normal allowlisted host is `https://devapi.samsungapps.com`. Multipart
uploads use the separate documented host
`https://seller.samsungapps.com/galaxyapi/`. Raw API access cannot escape
allowlisted Samsung hosts.

GET and HEAD requests may retry bounded transport failures, 408, 429, and
selected 5xx responses with exponential backoff, jitter, and `Retry-After`.
Mutations and multipart uploads are never blindly replayed. If a mutation
times out after it may have reached Samsung, a high-level workflow reads current
state and reconciles before deciding whether another write is safe.

## Samsung-specific safety

### App state

A 12-digit `contentId` can have `SALE` and `REGISTRATION` records at the same
time. Read commands may show both. A mutation must require `--app-status` when
the target cannot be proven unambiguous.

### Metadata presence

Samsung metadata is not ordinary PATCH data:

- omitted: do not change
- `null`: preserve for documented collections
- `[]`: intentionally remove all
- populated collection: replace or update

Presence-aware fields model these states explicitly. `omitempty` is not used to
guess intent. Destructive empty-list changes require an explicit clear action
and appear in a reviewed plan.

### Binary lifecycle

`contentUpdate.binaryList` is forbidden. Samsung rejects it beginning July
2026. All binary mutations use `/seller/v2/content/binary`.

Uploads stream from disk without buffering an APK or AAB in memory. Inputs must
be regular, non-symlink, non-empty files. The implementation records file size
before and after upload and uses a separate upload timeout.

### Rollouts and commerce

- A deployed staged rollout percentage cannot decrease.
- "Disable rollout" means complete globally, not pause.
- Closed-beta success can contain per-tester failures; every item is inspected.
- IAP cancel, refund, and revoke are separate operations with separate
  confirmations because their entitlement effects differ.
- Orders and statistics use POST for read operations; the catalog, not the HTTP
  verb alone, determines mutation safety.

## Discovery and API drift

Samsung does not publish a dependable OpenAPI document for this complete
surface. The checked-in operation catalog is the schema source for:

- `gsc capabilities`
- `gsc schema`
- `gsc search`
- generated command reference
- operation-to-command coverage tests
- Samsung API drift audits

Portal-only work is explicit rather than hidden: app creation, commercial
seller enrollment, service-account creation, subscription product registration,
category changes, and settlement reports.

## Testing

Every vertical slice includes:

- pure validation and model tests
- exact `httptest` method/path/query/header/body assertions
- response and error-envelope decoding
- command tests for exit code, stdout, and stderr
- table/JSON/Markdown golden behavior where applicable
- proof that validation and dry-run prevent side effects

Cross-cutting suites cover retry, cancellation, token claims, secret redaction,
pagination, tri-state encoding, streaming multipart behavior, and workflow
resume.

Live tests remain opt-in and read-only unless a user explicitly authorizes a
specific mutation.

## Distribution without publishing

The repository includes the complete distribution machinery before any
software release:

- Linux, macOS, and Windows cross-platform builds
- checksums and snapshot archives
- installer tests
- GitHub Actions setup and smoke tests
- Homebrew HEAD formula validation
- WinGet portable manifest generation and local validation
- SBOM and provenance configuration

Current workflows may upload ephemeral CI artifacts for inspection but cannot
create a tag, GitHub Release, tap commit, or WinGet submission. Publishing
remains a later manual decision.
