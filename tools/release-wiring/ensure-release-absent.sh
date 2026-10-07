#!/usr/bin/env bash
# Refuse an automated GoReleaser replay if any GitHub release already exists.
set -euo pipefail
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${RELEASE_TAG:?RELEASE_TAG is required}"
response=$(mktemp)
trap 'rm -f "$response"' EXIT
if gh api --include "repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}" >"$response" 2>/dev/null; then
  echo "✗ GitHub release ${RELEASE_TAG} already exists; automated GoReleaser replay is refused. Inspect the original release run and assets read-only, then ask the owner to review partial-publication state. Nothing was republished." >&2
  exit 1
fi
status=$(awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i ~ /^[0-9][0-9][0-9]$/) { print $i; exit } }' "$response")
if [ "$status" != 404 ]; then
  echo "✗ Could not prove GitHub release ${RELEASE_TAG} is absent (HTTP ${status:-unknown}); inspect permissions/API availability and retry. No automated publication was attempted." >&2
  exit 1
fi
echo "No GitHub release exists for ${RELEASE_TAG}; initial GoReleaser publication may proceed."
