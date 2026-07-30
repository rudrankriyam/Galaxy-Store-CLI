# Galaxy Store CLI contributor guide

This repository contains `gsc`, an unofficial, automation-first command-line
client for the Galaxy Store Developer API. It is not affiliated with Samsung.

## Development contract

- Use Go 1.25 or the newer version declared by `go.mod`.
- Keep commands scriptable and non-interactive.
- Use long, descriptive flags. Validate arguments before network or filesystem
  side effects.
- Write command data to stdout and diagnostics to stderr.
- Preserve stable JSON for pipes and CI; use tables only for interactive output.
- Require explicit confirmation for destructive or irreversible operations.
- Never print private keys, access tokens, authorization headers, or raw
  credential-bearing requests.
- Treat Samsung's current public API documentation as the protocol source of
  truth. In particular, use the current v2 binary endpoints rather than the
  retired `contentUpdate.binaryList` flow.
- Preserve the distinction between omitted, `null`, and empty collection values
  in update payloads. These can have different destructive semantics.
- Do not claim support for creating a new Galaxy Store app record: that remains
  a Seller Portal operation.

## Repository structure

- `cmd/` owns process startup, root flags, exit-code mapping, and command
  registration.
- `internal/` owns clients, authentication, output, and domain command packages.
- Keep API transport separate from command parsing so both can be tested with
  `httptest`.
- Add new domains as focused packages instead of growing a single command file.

## Testing

Use test-driven changes for observable behavior. Tests must cover:

- validation before side effects;
- exact HTTP method, route, query, headers, and request body;
- safe stdout/stderr separation and deterministic exit codes;
- authentication redaction and failure paths;
- pagination, retry, cancellation, and timeout boundaries where relevant;
- mutation planning, confirmation, and dry-run behavior.

Run focused tests during development, then:

```bash
make format-check
make vet
make lint
make test
make test-race
make build
```

The GitHub Actions matrix compiles and executes a smoke test on Linux, macOS,
and Windows. Govulncheck and CodeQL run independently.

## Commits and pull requests

- Keep commits small, coherent, and independently buildable.
- Do not mix generated or mechanical changes with behavioral changes.
- Do not rewrite unrelated user changes in a dirty worktree.
- Include tests with the behavior they protect.
- Document user-visible command changes in the same pull request.
- Never add credentials, local Seller Portal exports, or private app data.

## Publishing boundary

The repository may contain validation and packaging configuration before any
public distribution is enabled. Do not create tags, GitHub releases, upload
artifacts, publish packages, update Homebrew/WinGet, or otherwise distribute a
build unless the user explicitly authorizes that separate release operation.
