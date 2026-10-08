#!/usr/bin/env bash
# Trusted-main mutation stage for an already validated/prepared exact commit.
set -euo pipefail

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required (OWNER/REPO)}"
: "${RELEASE_VERSION:?RELEASE_VERSION is required (X.Y.Z)}"
: "${RELEASE_SHA:?RELEASE_SHA is required (full lowercase commit SHA)}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
if [ "${GITHUB_REF_TYPE:-}" != branch ] || [ "${GITHUB_REF_NAME:-}" != main ]; then
  echo "✗ Release mutation must run from the protected main workflow source. No tag was created." >&2
  exit 1
fi
if [ "${GITHUB_RUN_ATTEMPT:-1}" != 1 ]; then
  echo "✗ This release request is a rerun. Preserve state and inspect the original request/publisher runs read-only with the owner; no write is safe." >&2
  exit 1
fi
if ! GITHUB_REPOSITORY="$GITHUB_REPOSITORY" RELEASE_VERSION="$RELEASE_VERSION" \
  RELEASE_SHA="$RELEASE_SHA" GITHUB_REF_TYPE="$GITHUB_REF_TYPE" GITHUB_REF_NAME="$GITHUB_REF_NAME" \
  WORKFLOW_SHA="${WORKFLOW_SHA:-}" RELEASE_PRETAG=1 RELEASE_ORDER_CHECK=1 tools/release-wiring/preflight.sh; then
  echo "✗ Final exact-SHA eligibility proof refused ${RELEASE_VERSION}; correct the issue and request this still-unreserved version again. No tag was created." >&2
  exit 1
fi

tag="v${RELEASE_VERSION}"
if ! tag_object=$(gh api --method POST "repos/${GITHUB_REPOSITORY}/git/tags" \
  -f "tag=${tag}" -f "message=yolo-jail ${RELEASE_VERSION}" \
  -f "object=${RELEASE_SHA}" -f type=commit --jq .sha); then
  echo "✗ Could not create the annotated tag object for ${tag}. Inspect API permissions/connectivity and retry only while the version remains unreserved; no tag ref was requested." >&2
  exit 1
fi
if ! printf '%s\n' "$tag_object" | grep -Eq '^[0-9a-f]{40}$'; then
  echo "✗ GitHub returned an invalid tag object for ${tag}. Inspect ${RELEASE_SHA}; no tag ref was requested." >&2
  exit 1
fi
if ! gh api --method POST "repos/${GITHUB_REPOSITORY}/git/refs" \
  -f "ref=refs/tags/${tag}" -f "sha=${tag_object}"; then
  echo "✗ Could not create ${tag}; it may already exist. Do not rerun this request. Inspect the tag and original request read-only with the owner." >&2
  exit 1
fi

echo "Created immutable ${tag} for ${RELEASE_SHA}; dispatching the trusted-main Release workflow."
if ! gh workflow run release.yml --repo "$GITHUB_REPOSITORY" --ref main \
  -f mode=publish -f "version=${RELEASE_VERSION}" -f "sha=${RELEASE_SHA}" \
  -f "request_run_id=${GITHUB_RUN_ID}"; then
  echo "✗ ${tag} exists for ${RELEASE_SHA}, but Release dispatch failed. Do not rerun release-request. Inspect the tag and run read-only with the owner." >&2
  exit 1
fi

release_workflow_id=$(gh api "repos/${GITHUB_REPOSITORY}/actions/workflows/release.yml" --jq .id) || {
  echo "✗ ${tag} exists, but the trusted Release workflow id could not be read. Do not dispatch a second run; inspect the original request and tag read-only." >&2
  exit 1
}
if ! printf '%s\n' "$release_workflow_id" | grep -Eq '^[1-9][0-9]*$'; then
  echo "✗ GitHub returned an invalid Release workflow id. Preserve ${tag} and ask the owner to inspect the original request." >&2
  exit 1
fi
release_title="Release ${tag} @ ${RELEASE_SHA} / request ${GITHUB_RUN_ID}"
wait_seconds=${RELEASE_WAIT_SECONDS:-5400}
if ! printf '%s\n' "$wait_seconds" | grep -Eq '^[1-9][0-9]{0,4}$' || [ "$wait_seconds" -gt 21600 ]; then
  echo "✗ Invalid bounded Release wait duration. Preserve ${tag} and inspect the original request." >&2
  exit 1
fi
start=$(date +%s)
release_run_id=
while :; do
  matches=$(gh api --paginate "repos/${GITHUB_REPOSITORY}/actions/workflows/${release_workflow_id}/runs?per_page=100&branch=main&event=workflow_dispatch" \
    --jq ".workflow_runs[] | select(.display_title == \"${release_title}\" and .event == \"workflow_dispatch\" and .head_branch == \"main\") | [.id,.status,(.conclusion // \"\"),.run_attempt] | @tsv") || {
      echo "✗ ${tag} exists, but the original Release run state is unreadable. Do not redispatch; inspect its run read-only with the owner." >&2
      exit 1
    }
  line_count=$(printf '%s\n' "$matches" | awk 'NF { n++ } END { print n+0 }')
  if [ "$line_count" -gt 1 ]; then
    echo "✗ Multiple Release runs match ${release_title}; refuse ambiguous publication. Preserve the tag and inspect all matching runs read-only with the owner." >&2
    exit 1
  fi
  if [ "$line_count" -eq 1 ]; then
    # Tab is IFS whitespace, so splitting on it would merge the empty
    # conclusion of a run that has not completed (GitHub reports null) into
    # its neighbor and misread the attempt. Split on a non-whitespace
    # separator, which keeps every empty field in place.
    IFS='|' read -r candidate_id status conclusion attempt <<< "${matches//$'\t'/|}"
    if ! printf '%s\n' "$candidate_id" | grep -Eq '^[1-9][0-9]*$' || [ "$attempt" != 1 ]; then
      echo "✗ The matching Release run has invalid identity or is a rerun. Preserve the tag and inspect the original run read-only." >&2
      exit 1
    fi
    if [ "$status" = completed ]; then
      if [ "$conclusion" != success ]; then
        echo "✗ ${tag} exists, but its exact original GoReleaser/Release run concluded ${conclusion:-unknown}. Do not redispatch or reuse artifacts; inspect the original run read-only with the owner." >&2
        exit 1
      fi
      release_run_id=$candidate_id
      break
    fi
  fi
  now=$(date +%s)
  if [ $((now - start)) -ge "$wait_seconds" ]; then
    echo "✗ ${tag} exists, but the exact original Release run did not complete within ${wait_seconds}s. Do not rerun or redispatch; inspect it read-only with the owner." >&2
    exit 1
  fi
  sleep 15
done

echo "Exact original Release run ${release_run_id} succeeded for ${tag}; dispatching the trusted-main direct PyPI/cache publisher."
if ! gh workflow run publish.yml --repo "$GITHUB_REPOSITORY" --ref main \
  -f "version=${RELEASE_VERSION}" -f "sha=${RELEASE_SHA}" \
  -f "request_run_id=${GITHUB_RUN_ID}" -f "release_run_id=${release_run_id}"; then
  echo "✗ ${tag} and its successful Release run are retained, but Publish dispatch failed. Do not rerun release-request or create another release; inspect the exact original runs and tag read-only with the owner." >&2
  exit 1
fi
echo "Publish dispatch accepted for ${tag} (${RELEASE_SHA}); its workflow must atomically claim the release asset before any wheel/image build. Dispatch acceptance is not registry publication success."
