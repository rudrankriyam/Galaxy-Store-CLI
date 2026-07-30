# Distribution boundaries

This repository separates packaging validation from release creation and
package-manager publication.

## Continuous snapshot validation

`Distribution snapshot` runs with read-only repository permissions and no
signing secrets. It asks GoReleaser for a snapshot, verifies the six supported
OS/architecture archives, archive contents, SBOM inventory, and checksums, then
uploads a short-lived GitHub Actions artifact. It cannot create a tag, GitHub
release, Homebrew tap commit, or WinGet submission.

The Homebrew job installs the HEAD formula through an isolated local tap. It
does not modify or push to the public tap.

## Manual draft release

`Draft release from existing tag` is dormant until an operator dispatches it.
It has four independent guardrails:

1. The supplied tag must already exist and use a SemVer-shaped name.
2. The checked-out commit must exactly match that tag.
3. The operator must type `CREATE_DRAFT_RELEASE`.
4. Both `APPLE_DEVELOPER_ID_CERT` and
   `APPLE_DEVELOPER_ID_PASSWORD` repository secrets must be configured.

`APPLE_DEVELOPER_ID_CERT` is the base64 content of a Developer ID Application
P12 certificate. The workflow maps those two secrets to GoReleaser's
cross-platform macOS signing support, which signs the macOS executables before
they enter their archives. Snapshot runs do not receive those secrets, so their
signing stage remains disabled.

The workflow creates a **draft** GitHub release only. It never creates or moves
a tag, publishes the draft, updates Homebrew, or submits to WinGet. macOS
notarization is not configured; adding it later requires separate App Store
Connect notary credentials and validation.

Publishing a draft release, updating a package manager, or running the WinGet
installed-package smoke workflow remain separate, explicit operations.
