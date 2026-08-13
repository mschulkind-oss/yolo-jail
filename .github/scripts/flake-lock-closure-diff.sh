#!/usr/bin/env bash
#
# Make a weekly flake.lock bump reviewable.
#
# The bump's entire diff is three lines — a nixpkgs `rev`, its `narHash`, a
# `lastModified`. Between two nixos-unstable tips ~100k packages may have
# moved; this image consumes a few dozen, so those three lines cannot tell an
# openssl CVE fix from a README typo in a package nobody here installs. This
# script computes what moved IN THE IMAGE and appends it to the PR body:
#
#     chromium: 151.0.7922.71 → 151.0.7922.108
#     aardvark-dns: 2.0.0 → 2.1.0, -22.5 KiB
#
# It runs AFTER the PR exists, on purpose. Nothing here can block the bump:
# the worst case is a PR that keeps the body the action already wrote, plus a
# note saying why the diff is missing. Hence `set -uo pipefail` WITHOUT -e —
# every step that can fail is checked and degrades to that note.
#
# What is compared: `.#imageClosureRoot`, whose closure is the nixpkgs half of
# the image's contents (~570 store paths, ~3 GiB) — see the long comment on
# that attr in flake.nix for why it is not the image itself.
#
# Environment:
#   BEFORE_LOCK  path to the pre-update flake.lock                  (required)
#   AFTER_LOCK   path to the post-update flake.lock   (default: read from the
#                PR's head branch, which is the only race-free source)
#   PR           pull request number to edit; UNSET → print to stdout instead,
#                which is how this is rehearsed outside CI:
#                  BEFORE_LOCK=old.lock AFTER_LOCK=flake.lock \
#                    .github/scripts/flake-lock-closure-diff.sh
#   NIX_TIMEOUT  per-side budget for `nix build` (default 20m). Everything in
#                the closure is substitutable from cache.nixos.org, so a side
#                that runs long means nixpkgs handed us something Hydra had
#                not built yet — degrade rather than compile chromium in CI.
#
set -uo pipefail

HEADING='### What this moves in the jail image'
ATTR="${ATTR:-.#imageClosureRoot}"
NIX_TIMEOUT="${NIX_TIMEOUT:-20m}"
TMP="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
NIX=(nix --extra-experimental-features "nix-command flakes")

log() { printf '%s\n' "$*" >&2; }

run_link() {
  if [ -n "${GITHUB_RUN_ID:-}" ]; then
    printf '[the workflow run](%s/%s/actions/runs/%s)' \
      "${GITHUB_SERVER_URL:-https://github.com}" \
      "${GITHUB_REPOSITORY:-}" "$GITHUB_RUN_ID"
  else
    printf 'the workflow run'
  fi
}

# emit <markdown>: append the section to the PR body, or print it when there
# is no PR (local rehearsal).  A failure to READ the current body means we
# cannot append without destroying it — so we log and leave the PR alone.
emit() {
  if [ -z "${PR:-}" ]; then
    printf '%s\n' "$1"
    return 0
  fi
  local body="$TMP/pr-body.md"
  if ! gh pr view "$PR" --json body -q .body >"$body"; then
    log "could not read PR #$PR body; leaving it as the action wrote it"
    return 0
  fi
  # The bump runs weekly against the SAME PR when one is already open, so a
  # section from a previous run may already be there.  Cut at the heading and
  # re-append rather than stacking a second copy.
  awk -v h="$HEADING" '$0 == h {exit} {print}' "$body" >"$body.base" &&
    mv "$body.base" "$body"
  printf '\n%s\n' "$1" >>"$body"
  gh pr edit "$PR" --body-file "$body" ||
    log "could not edit PR #$PR body"
}

degrade() {
  emit "$(
    printf '%s\n\n' "$HEADING"
    printf 'Not computed: %s. See %s.\n' "$1" "$(run_link)"
  )"
  exit 0
}

[ -n "${BEFORE_LOCK:-}" ] || degrade "no pre-update flake.lock was captured"
[ -r "${BEFORE_LOCK}" ] || degrade "the pre-update flake.lock is unreadable"

# The post-update lock comes from the PR branch, not the working tree:
# create-pull-request may restore the tree, and the tip could have moved on
# between our read and the action's `nix flake update`.  The branch is what
# will actually be merged.
if [ -z "${AFTER_LOCK:-}" ]; then
  [ -n "${PR:-}" ] || degrade "neither AFTER_LOCK nor PR was set"
  branch="$(gh pr view "$PR" --json headRefName -q .headRefName)" ||
    degrade "the PR's head branch could not be resolved"
  git fetch --no-tags origin "$branch" >/dev/null 2>&1 ||
    degrade "the PR branch could not be fetched"
  AFTER_LOCK="$TMP/flake.lock.after"
  git show "FETCH_HEAD:flake.lock" >"$AFTER_LOCK" ||
    degrade "flake.lock could not be read from the PR branch"
fi

build_side() {
  timeout "$NIX_TIMEOUT" "${NIX[@]}" build --no-link --print-out-paths \
    --no-write-lock-file --reference-lock-file "$1" "$ATTR"
}

log "building $ATTR against $BEFORE_LOCK"
before_root="$(build_side "$BEFORE_LOCK")" ||
  degrade "the image closure would not build on the OLD lock"
log "building $ATTR against $AFTER_LOCK"
after_root="$(build_side "$AFTER_LOCK")" ||
  degrade "the image closure would not build on the NEW lock"

if [ "$before_root" = "$after_root" ]; then
  emit "$(
    printf '%s\n\n' "$HEADING"
    printf 'Nothing. The image closure is byte-identical on both locks, so this\n'
    printf 'bump cannot change the jail image at all.\n'
  )"
  exit 0
fi

# `nix store diff-closures` reports VERSION changes (and size changes over
# 8 KiB).  A staging-next merge that rebuilds the world without moving a
# single version is invisible to it — which is why the path count below is
# reported too: it is the difference between a quiet week and a mass rebuild.
diff_out="$(
  "${NIX[@]}" store diff-closures "$before_root" "$after_root" |
    sed -e $'s/\x1b\\[[0-9;]*m//g'
)" || degrade "\`nix store diff-closures\` failed"
log "--- diff-closures ---"; log "$diff_out"

# A PR body caps at 65536 characters and the closure holds ~570 packages, so
# an untruncated worst case is ugly rather than fatal — but a diff nobody
# scrolls through is not review either.  The full list stays in the job log.
diff_lines="$(printf '%s' "$diff_out" | grep -c '' )"
if [ "${diff_lines:-0}" -gt 100 ]; then
  diff_out="$(printf '%s' "$diff_out" | head -n 100)
… and $((diff_lines - 100)) more (full list in the job log)"
fi

paths_of() { "${NIX[@]}" path-info -r "$1" | grep -vFx "$1" | sort; }
paths_of "$before_root" >"$TMP/closure.before" || :
paths_of "$after_root" >"$TMP/closure.after" || :
total="$(wc -l <"$TMP/closure.after" | tr -d ' ')"
moved="$(comm -13 "$TMP/closure.before" "$TMP/closure.after" | wc -l | tr -d ' ')"

size_of() { "${NIX[@]}" path-info --closure-size "$1" | awk '{print $2}'; }
before_size="$(size_of "$before_root")"
after_size="$(size_of "$after_root")"
size_line="$(
  awk -v a="${before_size:-0}" -v b="${after_size:-0}" 'BEGIN {
    d = b - a; m = (d < 0 ? -d : d)
    if (d == 0)             delta = "unchanged"
    else if (m < 1048576)   delta = sprintf("%s%.1f KiB", (d > 0 ? "+" : "-"), m / 1024)
    else if (m < 1073741824) delta = sprintf("%s%.1f MiB", (d > 0 ? "+" : "-"), m / 1048576)
    else                    delta = sprintf("%s%.2f GiB", (d > 0 ? "+" : "-"), m / 1073741824)
    printf "closure size %.2f GiB (%s)", b / 1073741824, delta
  }'
)"

section="$(
  printf '%s\n\n' "$HEADING"
  if [ -n "$diff_out" ]; then
    printf '```\n%s\n```\n\n' "$diff_out"
  else
    printf 'No package in the image'\''s closure changed version.\n\n'
  fi
  printf '%s of %s store paths changed; %s.\n\n' "$moved" "$total" "$size_line"
  printf '<sub>`nix store diff-closures` over `.#imageClosureRoot` — the nixpkgs half of\n'
  printf 'the image'\''s contents (our own Go binaries are excluded: a lock bump cannot\n'
  printf 'move them). Versions come from store path names, so a rebuild that keeps a\n'
  printf 'version shows only in the path count. Computed by %s.</sub>\n' "$(run_link)"
)"

emit "$section"
