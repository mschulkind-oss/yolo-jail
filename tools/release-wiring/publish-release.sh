#!/usr/bin/env bash
# Trusted-main mutation stage: upload only the inert, validated GoReleaser files.
set -euo pipefail
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${RELEASE_SHA:?RELEASE_SHA is required}"
: "${RELEASE_ASSETS_DIR:?RELEASE_ASSETS_DIR is required}"
: "${RELEASE_NOTES_FILE:?RELEASE_NOTES_FILE is required}"
tag="v${RELEASE_VERSION}"
if [ "${GITHUB_REF_TYPE:-}" != branch ] || [ "${GITHUB_REF_NAME:-}" != main ] || [ "${GITHUB_RUN_ATTEMPT:-1}" != 1 ]; then
  echo "✗ GoReleaser upload must be the first trusted-main workflow attempt; no release was written." >&2
  exit 1
fi
# Recheck regular CI and global version order after target preparation, directly
# before any release API mutation. This job runs trusted code only.
if ! GITHUB_REPOSITORY="$GITHUB_REPOSITORY" GITHUB_REF_TYPE="$GITHUB_REF_TYPE" GITHUB_REF_NAME="$GITHUB_REF_NAME" \
  RELEASE_VERSION="$RELEASE_VERSION" RELEASE_SHA="$RELEASE_SHA" RELEASE_PRETAG=0 RELEASE_ORDER_CHECK=1 \
  WORKFLOW_SHA="${WORKFLOW_SHA:-}" GITHUB_RUN_ATTEMPT="${GITHUB_RUN_ATTEMPT:-1}" tools/release-wiring/preflight.sh; then
  echo "✗ Final exact-CI/version-order proof refused ${tag}; no release API write is safe." >&2
  exit 1
fi

# This trusted validator reads target pack manifests as Git blobs and never
# executes target code, configuration, hooks, or artifact-selected commands.
assets_json=$(go run ./tools/release-wiring validate-assets \
  --repo-root "$GITHUB_WORKSPACE" --sha "$RELEASE_SHA" --version "$RELEASE_VERSION" \
  --trusted-config "$GITHUB_WORKSPACE/.goreleaser.yaml" --dir "$RELEASE_ASSETS_DIR") || {
  echo "✗ Release artifacts failed the trusted name/path/type/digest contract; preserve ${tag} and ask the owner to inspect the original run read-only." >&2
  exit 1
}
if ! GITHUB_REPOSITORY="$GITHUB_REPOSITORY" RELEASE_TAG="$tag" tools/release-wiring/ensure-release-absent.sh; then
  echo "✗ A release object already exists for ${tag}; no clobber, delete, or replay is permitted." >&2
  exit 1
fi

notes_json=$(jq -Rs . < "$RELEASE_NOTES_FILE")
prerelease=false
if [[ "$RELEASE_VERSION" == *-* ]]; then prerelease=true; fi
payload=$(jq -n --arg tag "$tag" --arg title "$tag" --argjson body "$notes_json" --argjson prerelease "$prerelease" \
  '{tag_name:$tag,name:$title,body:$body,draft:true,prerelease:$prerelease,make_latest:"legacy"}')
payload_file=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/yolo-release-payload.XXXXXX")
trap 'rm -f "$payload_file"' EXIT
printf '%s\n' "$payload" > "$payload_file"
release_id=$(gh api --method POST "repos/${GITHUB_REPOSITORY}/releases" --input "$payload_file" --jq .id) || {
  echo "✗ GitHub could not create the draft release for ${tag}; the outcome may be partial. Do not retry or remove state; inspect the original run read-only with the owner." >&2
  exit 1
}
if ! printf '%s\n' "$release_id" | grep -Eq '^[1-9][0-9]*$'; then
  echo "✗ GitHub returned an invalid draft release id for ${tag}; do not retry or remove state." >&2
  exit 1
fi

# The validator's sorted JSON contains only safe basenames. Upload one file at a
# time without --clobber; GitHub's asset-name conflict is a refusal, never an update.
while IFS= read -r name; do
  [ -n "$name" ] || continue
  case "$name" in
    checksums.txt|yolo-jail_"$RELEASE_VERSION"_darwin_amd64.tar.gz|yolo-jail_"$RELEASE_VERSION"_darwin_arm64.tar.gz|yolo-jail_"$RELEASE_VERSION"_linux_amd64.tar.gz|yolo-jail_"$RELEASE_VERSION"_linux_arm64.tar.gz|[A-Za-z0-9]*_"$RELEASE_VERSION"_linux_amd64|[A-Za-z0-9]*_"$RELEASE_VERSION"_linux_arm64|[A-Za-z0-9]*_"$RELEASE_VERSION"_darwin_amd64|[A-Za-z0-9]*_"$RELEASE_VERSION"_darwin_arm64)
      ;;
    *)
      echo "✗ Trusted release asset list contained an unexpected name ${name}; draft state is retained for owner inspection." >&2
      exit 1
      ;;
  esac
  if ! gh release upload "$tag" --repo "$GITHUB_REPOSITORY" "$RELEASE_ASSETS_DIR/$name"; then
    echo "✗ Asset upload for ${tag}/${name} failed or is ambiguous. Preserve the draft and all uploaded assets; no overwrite, delete, replay, or automatic retry is allowed." >&2
    exit 1
  fi
done < <(printf '%s\n' "$assets_json" | jq -r '.[]')

if ! gh api --method PATCH "repos/${GITHUB_REPOSITORY}/releases/${release_id}" -F draft=false; then
  echo "✗ Release assets were uploaded but ${tag} could not be marked published. Preserve the partial release; do not retry or delete state; inspect read-only with the owner." >&2
  exit 1
fi
echo "Published ${tag} from the exact validated target assets; all upload steps ran from trusted main and no target code had write permission."
