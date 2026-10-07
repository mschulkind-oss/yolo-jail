#!/usr/bin/env bash
# Execute target verification only in a read-only job after trusted-main SHA/CI
# eligibility; no target checkout credentials are persisted.
set -euo pipefail
: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${RELEASE_SHA:?RELEASE_SHA is required}"
: "${TARGET_ROOT:?TARGET_ROOT is required}"
head=$(git -C "$TARGET_ROOT" rev-parse HEAD) || {
  echo "✗ Could not resolve the isolated target source checkout; no publication is allowed." >&2
  exit 1
}
if [ "$head" != "$RELEASE_SHA" ]; then
  echo "✗ Target checkout $head does not match immutable tag commit $RELEASE_SHA; no publication is allowed." >&2
  exit 1
fi
if ! (cd "$TARGET_ROOT" && env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN sh scripts/changelog-section.sh "$RELEASE_VERSION" >/dev/null); then
  echo "✗ Target changelog has no usable section for ${RELEASE_VERSION}; preserve the tag and ask the owner to review it." >&2
  exit 1
fi
if ! (cd "$TARGET_ROOT" && env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN go run ./tools/pack-binaries check "$RELEASE_VERSION"); then
  echo "✗ Target official pack binaries are not pinned for ${RELEASE_VERSION}; preserve the tag and ask the owner to review it." >&2
  exit 1
fi
echo "Target changelog and official pack binary pins verified read-only for v${RELEASE_VERSION} at ${RELEASE_SHA}."
