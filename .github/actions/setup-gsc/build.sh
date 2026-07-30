#!/usr/bin/env bash

set -euo pipefail

source_dir="${GSC_ACTION_PATH:?GSC_ACTION_PATH is required}"
install_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/gsc-action/bin"
binary_path="${install_dir}/gsc"

if [[ ! -f "${source_dir}/go.mod" || ! -f "${source_dir}/main.go" ]]; then
  echo "::error::The Galaxy Store CLI action source is incomplete." >&2
  exit 1
fi

commit="${GSC_ACTION_REF:-}"
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  commit="$(git -C "$source_dir" rev-parse --verify HEAD 2>/dev/null || true)"
fi
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  commit="unknown"
fi

commit_date="unknown"
if [[ "$commit" != "unknown" ]]; then
  candidate_date="$(git -C "$source_dir" show -s --format=%cI "$commit" 2>/dev/null || true)"
  if [[ "$candidate_date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[-+][0-9]{2}:[0-9]{2}$ ]]; then
    commit_date="$candidate_date"
  fi
fi

short_commit="${commit:0:12}"
version="source-${short_commit}"

mkdir -p "$install_dir"
(
  cd "$source_dir"
  CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off GOFLAGS=-mod=readonly go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${commit_date}" \
    -o "$binary_path" \
    .
)

version_output="$("$binary_path" version)"
if [[ -z "$version_output" || "$version_output" == *$'\n'* || "$version_output" == *$'\r'* ]]; then
  echo "::error::gsc returned an invalid version string." >&2
  exit 1
fi

printf '%s\n' "$install_dir" >>"$GITHUB_PATH"
{
  printf 'path=%s\n' "$binary_path"
  printf 'version=%s\n' "$version_output"
} >>"$GITHUB_OUTPUT"
