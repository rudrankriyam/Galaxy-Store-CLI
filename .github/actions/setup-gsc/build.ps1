$ErrorActionPreference = "Stop"

$sourceDirectory = $env:GSC_ACTION_PATH
if ([string]::IsNullOrWhiteSpace($sourceDirectory)) {
    throw "GSC_ACTION_PATH is required."
}
if (-not (Test-Path -LiteralPath (Join-Path $sourceDirectory "go.mod") -PathType Leaf) -or
    -not (Test-Path -LiteralPath (Join-Path $sourceDirectory "main.go") -PathType Leaf)) {
    throw "The Galaxy Store CLI action source is incomplete."
}

$installDirectory = Join-Path $env:RUNNER_TEMP "gsc-action\bin"
$binaryPath = Join-Path $installDirectory "gsc.exe"

$commit = $env:GSC_ACTION_REF
if ($commit -notmatch "^[0-9a-f]{40}$") {
    $commit = (& git -C $sourceDirectory rev-parse --verify HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or $commit -notmatch "^[0-9a-f]{40}$") {
        $commit = "unknown"
    }
}

$commitDate = "unknown"
if ($commit -ne "unknown") {
    $candidateDate = (& git -C $sourceDirectory show -s --format=%cI $commit 2>$null)
    if ($LASTEXITCODE -eq 0 -and
        $candidateDate -match "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[-+][0-9]{2}:[0-9]{2}$") {
        $commitDate = $candidateDate
    }
}

$shortCommit = $commit.Substring(0, [Math]::Min(12, $commit.Length))
$version = "source-$shortCommit"
$linkerFlags = "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$commitDate"

New-Item -ItemType Directory -Force -Path $installDirectory | Out-Null
Push-Location $sourceDirectory
try {
    $env:CGO_ENABLED = "0"
    $env:GOTOOLCHAIN = "local"
    $env:GOWORK = "off"
    $env:GOFLAGS = "-mod=readonly"
    & go build -buildvcs=false -trimpath -ldflags $linkerFlags -o $binaryPath .
    if ($LASTEXITCODE -ne 0) {
        throw "Building gsc failed with exit code $LASTEXITCODE."
    }
}
finally {
    Pop-Location
}

$versionOutput = (& $binaryPath version)
if ($LASTEXITCODE -ne 0 -or
    [string]::IsNullOrWhiteSpace($versionOutput) -or
    $versionOutput -match "[`r`n]") {
    throw "gsc returned an invalid version string."
}

$installDirectory | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append
"path=$binaryPath" | Out-File -FilePath $env:GITHUB_OUTPUT -Encoding utf8 -Append
"version=$versionOutput" | Out-File -FilePath $env:GITHUB_OUTPUT -Encoding utf8 -Append
