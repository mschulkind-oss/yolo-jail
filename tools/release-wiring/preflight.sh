#!/usr/bin/env bash
# Trusted-main eligibility gate. It reads the target commit/ref as Git data and
# proves exact regular CI before any target-controlled source is executed.
set -euo pipefail

fail() {
  echo "✗ $* No tag or publisher write was made by this preflight." >&2
  exit 1
}

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required (OWNER/REPO)}"
: "${RELEASE_VERSION:?RELEASE_VERSION is required (X.Y.Z)}"
: "${RELEASE_SHA:?RELEASE_SHA is required (full lowercase commit SHA)}"
: "${GITHUB_REF_TYPE:?GITHUB_REF_TYPE is required}"
: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"
if [ "$GITHUB_REF_TYPE" != branch ] || [ "$GITHUB_REF_NAME" != main ]; then
  fail "Release orchestration must use the trusted main branch workflow source (ref_type=$GITHUB_REF_TYPE ref=$GITHUB_REF_NAME)."
fi
if [ -n "${WORKFLOW_SHA:-}" ]; then
  checkout=$(git rev-parse HEAD) || fail "Could not resolve the trusted workflow checkout."
  if [ "$checkout" != "$WORKFLOW_SHA" ]; then
    fail "Trusted workflow checkout $checkout does not match workflow SHA $WORKFLOW_SHA."
  fi
fi
if ! printf '%s\n' "$GITHUB_REPOSITORY" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || [[ "$GITHUB_REPOSITORY" == *..* ]]; then
  fail "Invalid repository '$GITHUB_REPOSITORY'."
fi
if ! printf '%s\n' "$RELEASE_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  fail "Invalid release version '$RELEASE_VERSION' (want X.Y.Z or X.Y.Z-pre)."
fi
if ! printf '%s\n' "$RELEASE_SHA" | grep -Eq '^[0-9a-f]{40}$'; then
  fail "Invalid release SHA; pass one full lowercase 40-character commit SHA."
fi
if [ "${GITHUB_RUN_ATTEMPT:-1}" != 1 ]; then
  fail "This release-request run is a rerun; preserve all existing state and ask the owner to inspect the original run read-only."
fi
head=$(git rev-parse "$RELEASE_SHA^{commit}") || fail "Requested SHA $RELEASE_SHA is not a commit available to the trusted workflow."
if [ "$head" != "$RELEASE_SHA" ]; then
  fail "Requested SHA did not resolve to the exact commit object."
fi
git fetch --quiet --no-tags origin main || fail "Could not refresh origin/main; retry after connectivity is restored."
if ! git merge-base --is-ancestor "$RELEASE_SHA" FETCH_HEAD; then
  fail "Target $RELEASE_SHA is not contained in origin/main; push/merge that exact commit first."
fi

tag="v${RELEASE_VERSION}"
refs=$(git ls-remote origin "refs/tags/${tag}" "refs/tags/${tag}^{}") || fail "Could not read the remote tag ref for ${tag}; no write is safe."
if [ "${RELEASE_PRETAG:-0}" = 1 ]; then
  if [ -n "$refs" ]; then
    # A request may resume a tag-only release: an annotated tag already at
    # exactly this commit (the write job also checks its request-written message). The write job then proves nothing after
    # the tag left any state (verify-resume) before it dispatches anything.
    tag_object=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}" '$2 == ref { print $1 }')
    peeled=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}^{}" '$2 == ref { print $1 }')
    if [ "${RELEASE_ALLOW_RESUME:-0}" != 1 ] || [ -z "$tag_object" ] || [ "$peeled" != "$RELEASE_SHA" ]; then
      fail "${tag} is already reserved${peeled:+ at ${peeled}}, and only an annotated tag at exactly ${RELEASE_SHA} can resume. Never move the ref; inspect it read-only: git ls-remote origin refs/tags/${tag} 'refs/tags/${tag}^{}', and the release.yml and publish.yml runs, with the owner."
    fi
    echo "${tag} already exists at ${RELEASE_SHA}; this request may resume it if nothing after the tag left any state."
  fi
else
  tag_object=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}" '$2 == ref { print $1 }')
  peeled=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/${tag}^{}" '$2 == ref { print $1 }')
  resolved=${peeled:-$tag_object}
  if [ -z "$resolved" ]; then
    fail "Required immutable tag ${tag} was not found; inspect the exact original request read-only."
  fi
  if [ "$resolved" != "$RELEASE_SHA" ]; then
    fail "Tag ${tag} resolves to ${resolved}, not requested ${RELEASE_SHA}; preserve the ref and ask the owner to inspect it."
  fi
fi

if ! go run ./tools/release-gate --repo "$GITHUB_REPOSITORY" --sha "$RELEASE_SHA" --timeout 60m --poll-interval 15s; then
  if [ "${RELEASE_PRETAG:-0}" = 1 ]; then
    fail "Required regular CI did not prove success for ${RELEASE_SHA}; fix the commit and retry ${RELEASE_VERSION} while the version remains unreserved."
  fi
  fail "Required regular CI did not prove success for ${RELEASE_VERSION} at ${RELEASE_SHA}; preserve the tag and inspect the exact original runs read-only with the owner before any further write."
fi

if [ "${RELEASE_ORDER_CHECK:-0}" = 1 ] && ! GITHUB_REPOSITORY="$GITHUB_REPOSITORY" RELEASE_VERSION="$RELEASE_VERSION" \
  go run ./tools/release-wiring check-version-order; then
  fail "Release order is ambiguous or v${RELEASE_VERSION} is not newer than every already-published version; preserve all state and ask the owner to inspect releases read-only."
fi
