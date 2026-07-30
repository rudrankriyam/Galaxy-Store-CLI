# Galaxy Store CLI

An unofficial CLI for Galaxy Store developers.

`gsc` exposes the documented
[Galaxy Store Developer API](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api.html)
as non-interactive commands for local development and CI. It covers
service-account authentication, app and binary operations, uploads, closed
beta, staged rollouts, buyer reviews, Samsung IAP, statistics, and guarded raw
API requests.

> [!IMPORTANT]
> This project is not affiliated with, endorsed by, or sponsored by Samsung.
> Galaxy Store and Samsung are trademarks of Samsung Electronics Co., Ltd.

## Status

No public release has been published. The repository builds and tests source
installs, Homebrew HEAD installs, and snapshot archives, but it does not publish
a GitHub Release, Homebrew package, or WinGet package.

The commands listed below are implemented. Multi-step shipping commands such as
metadata sync, `ship`, status waiting, resumable workflows, and shell completion
remain future work; see [Architecture](docs/ARCHITECTURE.md).

## Install from source

The module currently requires the Go version declared in `go.mod`.

```bash
git clone https://github.com/rudrankriyam/Galaxy-Store-CLI.git
cd Galaxy-Store-CLI
go build -trimpath -o ./bin/gsc .
./bin/gsc version
./bin/gsc --help
```

On macOS or Linux, the checked-in formula can install the current `main`
branch through a local tap:

```bash
brew tap-new --no-git local/gsc
cp Formula/gsc.rb "$(brew --repository local/gsc)/Formula/gsc.rb"
brew install --HEAD local/gsc/gsc
gsc version
```

This is the same isolated HEAD install exercised by CI, not a released Homebrew
package.

> [!NOTE]
> Gambit Scheme also installs a command named `gsc`. Homebrew's
> `gambit-scheme`, `ghostscript`, and `gerbil-scheme` formulae conflict over
> that executable, so this formula declares the same conflicts. Run
> `type -a gsc` before installing if you use Scheme tooling. On Windows,
> inspect `Get-Command gsc -All`; the candidate package ID
> `RudrankRiyam.GalaxyStoreCLI` avoids package-search ambiguity but cannot
> prevent a PATH collision.

## Seller Portal setup

Before using authenticated commands, create these in Seller Portal:

- a commercial seller account;
- an app record, which supplies the 12-digit `contentId`;
- a service account with the `publishing` scope, the `gss` scope, or both;
- an RSA private key for that service account.

Samsung's API cannot create the initial app record or service account. Follow
[Samsung's getting-started guide](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/get-started-with-the-gsd-api.html)
for the portal steps.

## Authenticate

Start with a dry run. It reads and validates the private key, signs a local JWT,
and prints the planned token exchange and storage operations without contacting
Samsung or changing local state.

```bash
gsc auth login \
  --profile production \
  --service-account-id "$SAMSUNG_SERVICE_ACCOUNT_ID" \
  --private-key /absolute/path/to/service-account.pem \
  --scope publishing \
  --scope gss \
  --set-default \
  --dry-run
```

Remove `--dry-run` to exchange the JWT. `gsc` stores the access token in the
operating-system credential manager and writes only non-secret profile metadata
to its config file.

```bash
gsc auth login \
  --profile production \
  --service-account-id "$SAMSUNG_SERVICE_ACCOUNT_ID" \
  --private-key /absolute/path/to/service-account.pem \
  --scope publishing \
  --scope gss \
  --set-default

gsc auth status --profile production
```

CI can supply an existing token without a local profile:

```bash
export GSC_SERVICE_ACCOUNT_ID="..."
export GSC_ACCESS_TOKEN="..."
gsc apps list --output json
```

Set both variables together. `GSC_PROFILE` selects a named profile, while
`GSC_CONFIG_PATH` selects an alternate absolute config path.

## First safe commands

These commands don't mutate Galaxy Store:

```bash
gsc capabilities
gsc search rollout
gsc schema --output json
gsc doctor
gsc apps list --profile production --output table
```

`capabilities`, `search`, and `schema` use the checked-in catalog and make no
network request. `doctor` is local unless you pass `--remote`; it returns a
nonzero exit code when required credentials are missing or invalid.

## Command reference

| Command | Implemented subcommands |
| --- | --- |
| `gsc auth` | `login`, `status`, `revoke` |
| `gsc apps` | `list`, `view`, `update`, `submit`, `status update` |
| `gsc binaries` | `add`, `update`, `delete` |
| `gsc uploads` | `sessions create`, `file` |
| `gsc beta` | `testers list`, `testers update` |
| `gsc rollouts` | `rate view`, `rate update`, `rate complete`, `binaries list`, `binaries update` |
| `gsc reviews` | `list`, `reply`, `reply delete` |
| `gsc iap items` | `list`, `view`, `create`, `replace`, `update`, `delete` |
| `gsc iap purchases` | `consume`, `acknowledge` |
| `gsc iap subscriptions` | `status`, `cancel`, `refund`, `revoke` |
| `gsc iap orders` | `list` |
| `gsc iap receipts` | `verify` |
| `gsc stats` | `seller`, `content` |
| `gsc api` | `request` |
| `gsc doctor` | local checks; add `--remote` to validate the current token |
| `gsc capabilities`, `gsc search`, `gsc schema` | local API discovery |
| `gsc version` | build version |

Run `gsc <command> --help` for flags and examples.

## Safety and automation

Read commands run without confirmation. Store, commerce, upload, review,
rollout, and raw API writes validate local input first and require either
`--dry-run` or `--confirm`; if both flags appear, dry-run wins. A dry run emits
a machine-readable plan and performs no mutation. Authentication bootstrap is
the deliberate exception: `auth login` supports `--dry-run`, but the actual
login doesn't take `--confirm`.

Command data goes to stdout. Errors and diagnostics go to stderr, which keeps
JSON pipelines clean. `--output auto` selects a table for typed row data on an
interactive terminal and minified JSON otherwise. Use
`--output json|table|markdown` when a script needs a fixed representation.

Exit codes form part of the command contract:

| Code | Meaning |
| ---: | --- |
| `0` | success |
| `1` | other error or an unhealthy diagnostic report |
| `2` | invalid flags, arguments, or local input |
| `3` | authentication or authorization failure |
| `4` | resource not found |
| `5` | conflict |
| `6` | missing confirmation, or Samsung HTTP 400/422 |
| `7` | other HTTP failure |
| `130` | interrupted with Ctrl-C |

## Seller Portal-only limits

`gsc` cannot automate features for which Samsung exposes no public API. The
known portal-only set includes initial app creation, commercial seller
enrollment, service-account creation, subscription product registration,
category changes, and settlement reports. `gsc capabilities` reports these
boundaries alongside implemented operations.

The independent research and competitor links remain in
[docs/RESEARCH.md](docs/RESEARCH.md). Packaging guardrails live in
[docs/DISTRIBUTION.md](docs/DISTRIBUTION.md).

## Agent skills

Six repo-local skills under [`.agents/skills`](.agents/skills) teach coding
agents how to operate `gsc` safely:

- general CLI discovery, authentication, output, and failure handling;
- app update, binary upload, submission, and distribution;
- closed beta and staged rollout management;
- buyer review triage and seller replies;
- IAP catalog, transaction, subscription, order, and receipt workflows;
- seller and app statistics.

Each skill keeps mutation authorization explicit and sends portal-only work
back to Seller Portal.

## License

MIT
