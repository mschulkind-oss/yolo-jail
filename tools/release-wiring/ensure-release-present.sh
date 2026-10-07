#!/usr/bin/env bash
# Verify the explicit Homebrew-only backfill names a published GitHub release.
set -euo pipefail
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${RELEASE_TAG:?RELEASE_TAG is required}"
if ! draft=$(gh api "repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}" --jq .draft 2>/dev/null); then
  echo "✗ Could not confirm GitHub release ${RELEASE_TAG} exists; inspect Actions/API permissions and retry. No formula update was attempted." >&2
  exit 1
fi
if [ "$draft" != false ]; then
  echo "✗ GitHub release ${RELEASE_TAG} is still a draft; publish/inspect the original release before Homebrew backfill. No formula update was attempted." >&2
  exit 1
fi
echo "Found published GitHub release ${RELEASE_TAG}; Homebrew-only backfill may proceed."
