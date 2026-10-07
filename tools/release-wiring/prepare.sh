#!/usr/bin/env bash
# Build target-controlled release inputs only after trusted-main SHA/CI
# eligibility, in a job with contents:read and no publication credentials.
set -euo pipefail

: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${RELEASE_SHA:?RELEASE_SHA is required}"
: "${TARGET_ROOT:?TARGET_ROOT is required}"
target_head=$(git -C "$TARGET_ROOT" rev-parse HEAD) || {
  echo "✗ Could not resolve the isolated target source checkout; no tag was created." >&2
  exit 1
}
if [ "$target_head" != "$RELEASE_SHA" ]; then
  echo "✗ Target source checkout $target_head does not match frozen SHA $RELEASE_SHA; no tag was created." >&2
  exit 1
fi
prepare_root=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/yolo-release-prepare.XXXXXX") || {
  echo "✗ Could not allocate isolated release preparation space for ${RELEASE_VERSION}. Free runner temp space and request again; no tag was created." >&2
  exit 1
}
trap 'rm -rf "$prepare_root"' EXIT

if ! (
  cd "$TARGET_ROOT"
  env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN \
    sh scripts/changelog-section.sh "$RELEASE_VERSION" >/dev/null
); then
  echo "✗ Target CHANGELOG.md has no usable section for ${RELEASE_VERSION}; correct the target source and retry while the version remains unreserved." >&2
  exit 1
fi
if ! (
  cd "$TARGET_ROOT"
  env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN \
    go run ./tools/pack-binaries check "$RELEASE_VERSION"
); then
  echo "✗ Official pack binaries are not pinned for ${RELEASE_VERSION}; correct and commit the exact target before requesting the still-unreserved version again." >&2
  exit 1
fi
if ! (
  cd "$TARGET_ROOT"
  env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN \
    VERSION="$RELEASE_VERSION" scripts/stage-source-bundle.sh "$prepare_root/source-bundle"
); then
  echo "✗ release source-bundle preparation failed for ${RELEASE_VERSION}. No tag was created; fix the build and request again." >&2
  exit 1
fi
if ! (
  cd "$TARGET_ROOT"
  env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN \
    go run ./tools/build-wheels --version "$RELEASE_VERSION" --output-dir "$prepare_root/wheels"
); then
  echo "✗ release wheel preparation failed for ${RELEASE_VERSION}. No tag was created; fix the build and request again." >&2
  exit 1
fi
if [ ! -f "$prepare_root/source-bundle/flake.nix" ] || ! compgen -G "$prepare_root/wheels/*.whl" >/dev/null; then
  echo "✗ release preparation returned without its source bundle and wheels. No tag was created; inspect the build output and retry." >&2
  exit 1
fi
echo "Target source-bundle, official binary pin, changelog and wheel preparation passed for ${RELEASE_VERSION} at ${RELEASE_SHA}; no build output is reused as a publication artifact."
