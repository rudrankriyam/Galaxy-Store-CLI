# WinGet candidate packaging

The repository can build and validate a WinGet portable-package candidate
without creating a tag, uploading an artifact, opening a package submission, or
publishing anything.

## Candidate identity

```yaml
PackageIdentifier: RudrankRiyam.GalaxyStoreCLI
PackageName: Galaxy Store CLI
Moniker: gsc
Commands:
  - gsc
InstallerType: portable
```

The package identifier is the stable, automation-safe identity. If this
candidate is eventually accepted into the community repository, prefer:

```powershell
winget install --id RudrankRiyam.GalaxyStoreCLI --exact
```

The shorter `winget install gsc` form depends on WinGet search remaining
unambiguous. A current WinGet source-index inspection on 2026-07-30 found no
package identifier, name, moniker, or declared command collision for this
candidate. That is a point-in-time check, not a reservation.

There is a separate PATH-level collision outside WinGet: Gambit Scheme has long
shipped its compiler as `gsc.exe`. The Galaxy Store CLI keeps the user-selected
`gsc` command, but developers who use Gambit must inspect command resolution
and choose their PATH order intentionally:

```powershell
Get-Command gsc -All -ErrorAction SilentlyContinue
```

The exact package identifier makes installation unambiguous; it cannot prevent
two installed programs from wanting the same executable name. Repeat both the
WinGet index audit and the local command-resolution check before any future
submission.

## Local snapshot validation

Build a Windows snapshot, even when running on another Go-supported platform:

```bash
mkdir -p build
GOOS=windows GOARCH=amd64 go build -trimpath -o build/gsc.exe .
```

Generate manifests from the exact local binary:

```bash
python3 scripts/generate_winget_manifests.py \
  --version 0.0.0-snapshot \
  --installer build/gsc.exe \
  --installer-url https://example.invalid/snapshots/gsc.exe \
  --output-dir build/winget
```

The example URL is deliberately non-publishing. It is sufficient for structural
validation but must be replaced by an immutable public HTTPS URL for the exact
hashed binary before any future package submission.

Candidate manifests deliberately use WinGet schema `1.10.0`. That is the schema
supported by the WinGet client currently present on GitHub's `windows-latest`
runner, and all fields used here are available in it. Upgrade this pin only
alongside an explicit, pinned WinGet client update in CI.

Run the repository validator:

```bash
python3 scripts/validate_winget_manifests.py \
  --manifest-dir build/winget/manifests/r/RudrankRiyam/GalaxyStoreCLI/0.0.0-snapshot \
  --installer build/gsc.exe
```

On Windows, run Microsoft WinGet validation as well:

```powershell
./scripts/validate-winget.ps1 `
  -ManifestDirectory build/winget/manifests/r/RudrankRiyam/GalaxyStoreCLI/0.0.0-snapshot `
  -InstallerPath build/gsc.exe `
  -RequireWinget
```

`winget validate` is the Microsoft schema validator. WinGetCreate can author or
update manifests but currently has no standalone local `validate` subcommand;
the PowerShell helper checks it can execute when present and uses
`winget validate` for schema validation.

The `WinGet snapshot validation` GitHub Actions workflow performs the complete
Windows build, executable smoke test, manifest tests, generation, checksum
verification, and WinGet schema validation. Its token has read-only repository
access, and it contains no submission or release operation.

The manual `WinGet installed-package smoke` workflow is intentionally dormant
until the package identifier is accepted into the WinGet community source. Once
that happens, an operator can use it to verify exact-ID discovery, installation,
command resolution, execution, and uninstall on a clean hosted runner. Running
that workflow does not submit or update a package.

## Future publishing boundary

No package ID or command is reserved until a manifest is accepted upstream.
Before any separately authorized submission:

1. Repeat the package-ID, moniker, and command collision checks.
2. Generate manifests using the final immutable HTTPS asset URL.
3. Confirm the generated SHA256 matches the bytes downloaded from that URL.
4. Run `winget validate` and a local-manifest install/uninstall smoke test on a
   clean Windows environment.
5. Review Microsoft community repository policies.

Do not run `wingetcreate submit`, open a `winget-pkgs` pull request, create a
tag, or publish an artifact as part of snapshot validation.
