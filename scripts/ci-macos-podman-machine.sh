#!/usr/bin/env bash
# Install podman and bring up a Podman Machine on a GitHub-hosted macOS Intel runner, for
# nightly-macos.yml's `archive-delivery-macos` job.
#
# ⚠ THIS IS A SECOND COPY OF THE SHARDS' `Install Podman` STEP, AND IT IS PINNED, NOT TRUSTED.
#
# The `integration-macos` shards run the same sequence inline, and that inline copy stays put:
# integration/macosmachineshares_test.go reads it straight out of the workflow (the FIRST
# `podman machine init`, the `for share in …` probe, the bounded retry) and pins it on every
# push. That step's comments are the authority for WHY each line below exists — the share list
# that `--volume` REPLACES rather than extends, the 504s from the release CDN, the `Error: EOF`
# and the 14-minute hang that the per-attempt deadline answers. Read them there; they are not
# restated here, because two copies of an explanation drift as surely as two copies of a list.
#
# What this copy owes the inline one is AGREEMENT, and
# integration/archivedeliveryworkflow_test.go checks it: the same share set, a probe for every
# share, the same podman release, one `init`, and a bounded, retried `start`. So a share added
# to the shards and forgotten here fails a -short test on the push that forgot it, instead of a
# nightly an hour in. The retry itself — two starts, then a VM reset and a last start, with what
# ran named in the warning or the error — is RUN against both copies by
# integration/macosmachinereset_test.go, which also redoes each step's cap.
#
# The job's step carries the `timeout-minutes` cap; this script bounds each `start` attempt
# itself, because a step cap can only end the step, never an attempt.
set -euo pipefail

# The last line with an x86_64 macOS installer (podman v6 dropped Intel macOS builds).
curl -fsSL --retry 5 --retry-delay 5 --retry-all-errors \
  --retry-max-time 180 -o /tmp/podman.pkg \
  "https://github.com/containers/podman/releases/download/v5.8.4/podman-installer-macos-amd64.pkg"
sudo installer -pkg /tmp/podman.pkg -target /
if [ -n "${GITHUB_PATH:-}" ]; then
  echo "/opt/podman/bin" >> "$GITHUB_PATH"
fi
export PATH="/opt/podman/bin:$PATH"

# THE COMPLETE SHARE LIST — `--volume` replaces podman's darwin defaults, it does not add to
# them. Must equal the shards' list; archivedeliveryworkflow_test.go compares the two. A function,
# so the reset below re-inits from this list and no other.
machine_init() {
  podman machine init --cpus 2 --memory 4096 --disk-size 30 \
    -v /Users:/Users \
    -v /private:/private \
    -v /var/folders:/var/folders \
    -v /nix:/nix
}
machine_init

# macOS ships no timeout(1), so the deadline is a sleep+kill watchdog.
start_with_deadline() {
  podman machine start &
  start_pid=$!
  ( sleep "$1"; kill -TERM "$start_pid" 2>/dev/null ) &
  dog_pid=$!
  start_rc=0
  wait "$start_pid" || start_rc=$?
  kill "$dog_pid" 2>/dev/null || true
  wait "$dog_pid" 2>/dev/null || true
  return "$start_rc"
}

# The last attempt starts a FRESH VM, as the shards' does: a third start of a machine that hung
# twice has never come up. Bounded like a start; `pkill -P` because the process that hangs is the
# subshell's podman child.
reset_machine() {
  podman machine rm -f || return
  machine_init
}
reset_with_deadline() {
  reset_machine &
  reset_pid=$!
  ( sleep "$1"; pkill -TERM -P "$reset_pid" 2>/dev/null || true
    kill -TERM "$reset_pid" 2>/dev/null ) &
  dog_pid=$!
  reset_rc=0
  wait "$reset_pid" || reset_rc=$?
  kill "$dog_pid" 2>/dev/null || true
  wait "$dog_pid" 2>/dev/null || true
  return "$reset_rc"
}

started=
ran=
for attempt in 1 2 3; do
  if [ "$attempt" -eq 3 ]; then
    echo "resetting the VM before the last attempt: podman machine rm -f, then the same init"
    reset_started=$(date +%s)
    if reset_with_deadline 360; then
      ran="${ran:+$ran; }VM reset (podman machine rm -f + init)"
    else
      if [ "$(( $(date +%s) - reset_started ))" -ge 360 ]; then
        ran="${ran:+$ran; }VM reset HUNG past its 6m deadline (killed), so attempt 3 never ran"
      else
        ran="${ran:+$ran; }VM reset FAILED, so attempt 3 never ran"
      fi
      break
    fi
  fi
  attempt_started=$(date +%s)
  if start_with_deadline 360; then
    started=yes
    break
  fi
  if [ "$(( $(date +%s) - attempt_started ))" -ge 360 ]; then
    outcome="HUNG past its 6m deadline (killed)"
  else
    outcome="failed"
  fi
  ran="${ran:+$ran; }attempt ${attempt} ${outcome}"
  # No stop after the last attempt: nothing follows it.
  if [ "$attempt" -eq 3 ]; then
    echo "podman machine start ${outcome} (attempt 3/3, on the fresh VM) — no attempts left"
    break
  fi
  echo "podman machine start ${outcome} (attempt ${attempt}/3) — stopping and retrying"
  podman machine stop || true
  sleep 5
done
if [ -z "$started" ]; then
  echo "::error::the podman machine never came up — ${ran}. Each start was bounded at 6m. No yolo code has run yet, so the fault is the runner's VM, not the commit under test. Next: re-run the failed job with gh run rerun ${GITHUB_RUN_ID:-<run-id>} --failed."
  exit 1
fi
if [ -n "$ran" ]; then
  echo "::warning::the podman machine came up on attempt ${attempt}/3, after: ${ran}."
fi

# A missing share fails LATER, as `Error: statfs <path>` naming a path the host created, so
# each root is asserted here where the cause can still be named.
for share in /Users /private /private/tmp /var/folders /nix/store "${TMPDIR:-}"; do
  [ -n "$share" ] || continue
  share=${share%/}
  if ! podman machine ssh -- test -d "$share"; then
    echo "::error::the podman machine cannot see $share. \`--volume\` REPLACES podman's default share set, so the list passed to the init above is the complete one."
    exit 1
  fi
done
