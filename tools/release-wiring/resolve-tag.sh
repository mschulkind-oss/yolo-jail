#!/usr/bin/env bash
# Resolve a published annotated or lightweight tag without checking out/running
# its source. Used by the version-only Homebrew backfill on trusted main.
set -euo pipefail
: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${EXPECTED_SHA:=}"
if ! printf '%s\n' "$RELEASE_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  echo "✗ Invalid immutable release version ${RELEASE_VERSION}." >&2
  exit 1
fi
tag="v${RELEASE_VERSION}"
refs=$(git ls-remote origin "refs/tags/${tag}" "refs/tags/${tag}^{}") || {
  echo "✗ Could not read ${tag} from origin; retry after connectivity is restored." >&2
  exit 1
}
tag_object=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}" '$2 == ref { print $1 }')
peeled=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}^{}" '$2 == ref { print $1 }')
resolved=${peeled:-$tag_object}
if ! printf '%s\n' "$resolved" | grep -Eq '^[0-9a-f]{40}$'; then
  echo "✗ Immutable tag ${tag} is missing or does not resolve to a commit; no Homebrew update occurred." >&2
  exit 1
fi
if [ -n "$EXPECTED_SHA" ] && [ "$resolved" != "$EXPECTED_SHA" ]; then
  echo "✗ ${tag} resolves to ${resolved}, not supplied SHA ${EXPECTED_SHA}; no Homebrew update occurred." >&2
  exit 1
fi
printf '%s\n' "$resolved"
