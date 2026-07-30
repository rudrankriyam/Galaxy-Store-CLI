param(
    [Parameter(Mandatory = $true)]
    [string]$ManifestDirectory,

    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,

    [switch]$RequireWinget,
    [switch]$CheckWingetCreate
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$validator = Join-Path $PSScriptRoot "validate_winget_manifests.py"

python $validator --manifest-dir $ManifestDirectory --installer $InstallerPath
if ($LASTEXITCODE -ne 0) {
    throw "Repository WinGet validation failed."
}

$winget = Get-Command winget -ErrorAction SilentlyContinue
if ($null -eq $winget) {
    if ($RequireWinget) {
        throw "winget is required but was not found."
    }
    Write-Warning "winget was not found; Microsoft schema validation was skipped."
} else {
    & $winget.Source validate --manifest $ManifestDirectory --disable-interactivity
    if ($LASTEXITCODE -ne 0) {
        throw "winget manifest validation failed."
    }
}

if ($CheckWingetCreate) {
    $wingetCreate = Get-Command wingetcreate -ErrorAction SilentlyContinue
    if ($null -eq $wingetCreate) {
        Write-Warning "wingetcreate was not found; its availability check was skipped."
    } else {
        # WinGetCreate authors and updates manifests but currently has no local
        # validate subcommand. Keep this as a compatibility/availability check;
        # `winget validate` above remains the authoritative local schema check.
        & $wingetCreate.Source --help | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "wingetcreate could not be executed."
        }
    }
}

Write-Host "WinGet candidate manifests are valid. Nothing was submitted."
