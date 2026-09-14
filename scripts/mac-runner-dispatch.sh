#!/bin/bash
# Dispatch the Apple Container CI workflow from the Mac that will run it.
#
# ─── WHY THE MACHINE ASKS, INSTEAD OF BEING ASKED ───
#
# apple-container.yml runs on a self-hosted Mac that is off much of the time. A
# scheduled job that simply targets an absent runner does not fail fast — GitHub
# QUEUES it for up to 24 hours and then gives up, so a cron every two hours leaves
# a trail of amber runs nobody reads.
#
# The workflow's first answer was a POLL: a hosted job asking
# `GET /repos/{owner}/{repo}/actions/runners` whether the Mac was online, with the
# real job `if:`-gated on the answer. That works, and it costs a fine-grained PAT
# with `administration: read` stored as a repository secret — because
# `administration` is not a scope `permissions:` can grant to GITHUB_TOKEN, so the
# built-in token cannot ask.
#
# It was also asking the question backwards. A runner is an OUTBOUND long-poll
# client: it dials GitHub and holds the connection open, which is why self-hosted
# runners need no inbound ports and work behind NAT. "Is the runner online" is
# therefore a fact about a connection THIS MACHINE opened — and this machine can
# see it without asking anyone, let alone with admin credentials.
#
# So the poll is inverted here: the Mac checks its own runner locally, and
# dispatches only when it is actually able to serve the job. No admin PAT, no
# repository secret, no hosted probe job, and no tri-state to reason about.
#
# ─── WHAT THIS DOES NOT REPLACE ───
#
# The trigger list is still the whole fork-safety boundary, and it is still
# enforced by integration/selfhostedtriggers_test.go rather than by this file.
# Dispatching needs write access; nothing here widens who can cause a run.

set -uo pipefail

# LAUNCHD GIVES A MINIMAL PATH — /usr/bin:/bin:/usr/sbin:/sbin and nothing else —
# so `gh` and the runner's own tools are NOT on it. Resolving them here rather than
# in the plist keeps one copy of the list, and makes a hand-run behave identically
# to the scheduled one.
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

REPO=${YOLO_RUNNER_REPO:-mschulkind-oss/yolo-jail}
WORKFLOW=${YOLO_RUNNER_WORKFLOW:-apple-container.yml}
REF=${YOLO_RUNNER_REF:-main}
RUNNER_DIR=${YOLO_RUNNER_DIR:-$HOME/actions-runner}

STATE_DIR=${XDG_STATE_HOME:-$HOME/.local/state}/yolo-jail
LOG=$STATE_DIR/mac-runner-dispatch.log
STATE=$STATE_DIR/last-dispatched-$WORKFLOW-$REF
mkdir -p "$STATE_DIR"

# EVERY EXIT IS LOGGED AND ZERO. launchd has no terminal, so a message that is not
# written to this file is lost; and a non-zero exit from a periodic agent buys
# nothing but noise in the system log. The states this script legitimately reaches
# ("runner not running", "nothing new") are ordinary, not failures.
say() { printf '%s  %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" >> "$LOG"; }
done_ok() { say "$*"; exit 0; }

command -v gh >/dev/null 2>&1 || done_ok "gh is not on PATH — cannot dispatch. \
Install it, or set PATH in the plist."

# ⚠ A SHADOWING TOKEN IS THE FAILURE THIS UNSETS, AND IT IS MEASURED (2026-09-14).
#
# `gh` prefers $GH_TOKEN / $GITHUB_TOKEN over its own stored login, silently. The
# maintainer's repo checkout carries a `.env` with a fine-grained GH_TOKEN that has
# no `actions: write`, and with it exported the dispatch fails as
#
#   HTTP 403: Resource not accessible by personal access token
#
# while the keychain login (classic, `repo` scope) does it fine. A launchd agent does
# not inherit a shell environment, so this is belt-and-braces here — but the same
# script hand-run from a shell that sourced .env would take the 403 branch and log a
# permissions error that has nothing to do with permissions.
#
# Unset rather than honored: this script's job is to dispatch as the human who owns
# the runner, which is exactly what `gh auth` already holds.
unset GH_TOKEN GITHUB_TOKEN

# ─── THE LOCAL HALF OF THE OLD PROBE ───
#
# Checked because a dispatch onto a machine whose runner is NOT listening is the
# exact failure the poll existed to prevent: the run queues instead of executing.
# The Mac being awake is necessary and not sufficient — `svc.sh install` may never
# have run, or the agent may have been stopped by hand.
#
# `launchctl list` rather than `svc.sh status`: it needs no cwd, no repo checkout
# and no subshell into the runner's own tooling, and the label the runner installs
# always starts with `actions.runner.`.
if ! launchctl list 2>/dev/null | grep -q 'actions\.runner\.'; then
  done_ok "no actions.runner launchd agent is loaded, so a dispatch would queue \
instead of running. Start it with: (cd $RUNNER_DIR && ./svc.sh start)"
fi

# ─── DISPATCH ONLY WHAT IS NEW ───
#
# Without this the agent re-runs the SAME commit every interval, which on a
# personal machine is a fan spinning up hourly to re-prove a green result. The SHA
# is read from the remote rather than from a local checkout on purpose: this script
# is about what is on `$REF` upstream, and the Mac may have no clone at all.
#
# A re-run of an unchanged commit is still one command away by hand
# (`gh workflow run …`), which is the right place for that — a flake is a decision,
# not a schedule.
if ! sha=$(gh api "repos/$REPO/commits/$REF" -q .sha 2>&1); then
  done_ok "could not read $REPO@$REF (offline, or gh is not authenticated): ${sha%%$'\n'*}"
fi

if [ -f "$STATE" ] && [ "$(cat "$STATE")" = "$sha" ]; then
  done_ok "nothing new: $REF is still ${sha:0:12}, already dispatched"
fi

if ! out=$(gh workflow run "$WORKFLOW" --repo "$REPO" --ref "$REF" 2>&1); then
  # NOT recorded as dispatched, so the next interval retries this same commit.
  done_ok "dispatch of $WORKFLOW failed for ${sha:0:12}: ${out%%$'\n'*}"
fi

# Written only AFTER a dispatch GitHub accepted, for the reason above.
printf '%s\n' "$sha" > "$STATE"
say "dispatched $WORKFLOW on $REF at ${sha:0:12}"
