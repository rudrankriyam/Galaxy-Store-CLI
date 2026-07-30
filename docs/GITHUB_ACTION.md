# GitHub Actions setup

The repository includes a composite action that builds `gsc` from the action
source checked out by GitHub, installs it under the runner's temporary
directory, and adds it to `PATH`. It does not download a release, tag, or
package-manager artifact, and it does not accept Samsung credentials.

Pin both the checkout action and Galaxy Store CLI to reviewed, full commit
SHAs. The example below pins the first commit that contains the reviewed
`action.yml`:

```yaml
permissions:
  contents: read

steps:
  - name: Checkout
    uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6

  - name: Set up Galaxy Store CLI
    id: gsc
    uses: rudrankriyam/Galaxy-Store-CLI@acd2af64f4d43eff0ce7c27c2a841e0aa1bffd24

  - name: Inspect the source-built CLI
    shell: bash
    env:
      GSC_EXECUTABLE: ${{ steps.gsc.outputs.path }}
      GSC_VERSION: ${{ steps.gsc.outputs.version }}
    run: |
      "$GSC_EXECUTABLE" version
      gsc capabilities --output json
      gsc schema --output json
```

The action reads the required Go version from its own `go.mod`, uses a
commit-pinned `actions/setup-go`, disables the shared module cache, builds with
`CGO_ENABLED=0` against the checked-in module graph, and reports the installed
executable's absolute path and version text as outputs.

Authentication remains an explicit consumer concern. When a workflow later
needs Samsung API access, provide credentials only to the individual command
step that needs them; do not pass them to the setup action.
