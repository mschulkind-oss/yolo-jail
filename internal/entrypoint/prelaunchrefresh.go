package entrypoint

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// prelaunchrefresh.go is the launcher half of a program's PRE-LAUNCH REFRESH (packdecl.Refresh,
// a term coined for that field): the execution and concurrency tiers of
// docs/design/pi-extension-lifecycle.md, §3.2 and §3.3.
//
// WHAT IT IS FOR. Pi keeps its extension packages in a machine-scoped store every jail reads
// (§3.1, shipped 2026-09-21). Nothing refreshed that store, so every workspace went on showing
// Pi's "Package Updates Available" box until a human ran `pi update --extensions` by hand. The
// refresh is that command, run by the launcher the user just invoked, before the exec — so the
// in-app check that follows finds the store current.
//
// WHY IT IS DECLARED AND NOT KEYED ON "pi". The plan sketch put a `_refresh_pi_extensions`
// branch into npmLauncherTemplate for binary pi. Both templates are shared by every program,
// and a bin-named branch in them is exactly how core learns what an agent is — the thing
// AGENTS.md's first rule forbids. So the argv and the lock are the pack's declaration, and this
// file renders whatever a pack declares, for both launcher templates.
//
// THE LOCK IS A NON-BLOCKING mkdir, not a flock, for three reasons. §3.3 ruled it. It is the
// same primitive both templates' install-prefix `_take_lock` already use, because there is no
// flock(1) in the image and none on a stock macOS. And it is the one primitive that is atomic
// on EVERY path this store can be shared over: a flock is per kernel, while Apple Container
// runs each jail in its own VM over a shared host directory, where only the host filesystem's
// own mkdir(2) arbitrates.
//
// WHAT THE flock CONVENTIONS STILL TEACH IT. internal/cli/run/flock.go's error path fails OPEN
// — a lock it could not take lets the launch proceed unguarded — and the base-home plan's
// lesson from it is that anything DESTRUCTIVE must be gated on the lock having really been
// taken. Here the refresh is the write, so the rule is: the LAUNCH fails open (it always
// proceeds) and the WRITE fails closed (no lock, no refresh). "Cannot take it at all" (the store
// is missing or read-only) is reported as that, never as "another jail is refreshing", because
// the two ask the user to do different things.
//
// A HELD LOCK IS KEPT YOUNG BY A HEARTBEAT, because the stale break reads nothing but the
// lock's age. On the container backends `timeout 60` already keeps every refresh far shorter
// than STALE_LOCK, but _bounded has no timeout(1) on a stock macOS, so on macos-user a live
// refresh stalled on a slow registry can outlast STALE_LOCK — and without the heartbeat a
// second launcher would break its lock and start a second writer in the same store. The
// heartbeat touches the lock every REFRESH_HEARTBEAT seconds while the launcher that took it
// is alive and the lock still carries that launcher's token, so "older than STALE_LOCK"
// means "the launcher that took it is gone" on every backend. It does NOT bound the refresh:
// on macos-user a hung refresh still hangs the launch that ran it, as the program's own
// update already does there, and while it hangs other launches skip their refresh.
//
// THE RESIDUAL RACE, stated because the lock cannot close it: breaking a STALE lock is a
// check-then-act. Two launchers that both judge one lock stale in the same instant can each
// remove it and one can take the other's fresh lock. It needs a holder whose launcher died
// (only then does the lock age past STALE_LOCK) AND two launches inside a window of a few
// syscalls; the owner token below at least stops the loser from deleting the winner's lock
// when it finishes.

// refreshDeclShell is the refresh's BAKED declaration, spliced into both templates' header
// beside the other per-program values. Same splice contract as npmLauncherTemplate: every
// sentinel is a shquote'd literal in a bare position. HAS_REFRESH gates every expansion of
// REFRESH_ARGV for HAS_UPDATE_VERB's reason — bash before 4.4 treats "${arr[@]}" on an EMPTY
// array as unbound under "set -u", and macos-user runs these launchers on a stock bash 3.2.
const refreshDeclShell = `# The pack's declared PRE-LAUNCH REFRESH (packdecl.Refresh; pi-extension-lifecycle.md §3.2):
# the program's own argv, bin omitted, and the home-relative LOCK DIRECTORY inside the store
# the refresh writes (§3.3). BAKED, like everything above: a jail cannot talk it into one.
HAS_REFRESH=__YOLO_HAS_REFRESH__
REFRESH_ARGV=(__YOLO_REFRESH_ARGV__)
REFRESH_LOCK_REL=__YOLO_REFRESH_LOCK__
# The declared DUE-ON-CHANGE files (packdecl.Refresh.DueOnChange), home-relative. HAS_REFRESH_DUE
# gates every expansion of the list, for HAS_REFRESH's bash-3.2 reason.
HAS_REFRESH_DUE=__YOLO_HAS_REFRESH_DUE__
REFRESH_DUE_ON_CHANGE=(__YOLO_REFRESH_DUE_ON_CHANGE__)
`

// prelaunchRefreshShellFn runs the declared refresh, and is spliced into both templates after
// the MCP/LSP refresh and immediately before the exec — §3.2's "strictly before exec", and
// after every step that can change what $REAL_BIN is.
//
// It reads the templates' own UPDATE_INTERVAL, UPDATE_TIMEOUT, STALE_LOCK, STAMP_DIR,
// UPDATES_ENABLED, _stamp_mtime and _bounded rather than defining its own, so a refresh is
// throttled, bounded and policy-gated by exactly the numbers the program's own update is.
// __YOLO_EXEC_PREFIX__ is the npm template's resolved interpreter (a declared node_floor), so a
// `#!/usr/bin/env node` program is refreshed under the node it is launched under; the native
// template renders it empty.
const prelaunchRefreshShellFn = `
# --- pre-launch refresh (pi-extension-lifecycle.md §3.2, §3.3) -------------------------
# The stamp is MACHINE-GLOBAL (~/.cache is one truth across workspaces), so one refresh an hour
# covers the machine-scoped store every jail shares. It throttles EVERYTHING the refresh does,
# though: what the program refreshes outside that store (for pi, a workspace's own git
# packages, and whichever package list that workspace's settings name) waits out the same
# hour. It lives in its own subdirectory so no bin name can collide with it.
REFRESH_STAMP="$STAMP_DIR/refresh/$BIN.stamp"
REFRESH_LOCK="$HOME/$REFRESH_LOCK_REL"
REFRESH_STORE="${REFRESH_LOCK%/*}"
REFRESH_TOKEN=""
REFRESH_HEARTBEAT=60 # seconds between touches of a HELD lock; STALE_LOCK is ten of them
REFRESH_BEAT_PID=""
# One marker per CONTENT KEY a refresh has SUCCEEDED for on this machine (DueOnChange). Beside
# the stamp, so machine-global like it; keyed by content rather than by workspace, so two
# workspaces with different settings each refresh once and then stop, instead of taking turns.
REFRESH_SEEN_DIR="$STAMP_DIR/refresh/$BIN.seen"
REFRESH_KEY=""

# _refresh_content_key prints one key for the current content of every declared file: an
# absent file contributes "absent", so absent and present differ. cksum is POSIX and ships on a
# stock macOS; the key is only compared, never trusted, so a CRC is enough.
_refresh_content_key() {
    local f all=""
    for f in "${REFRESH_DUE_ON_CHANGE[@]}"; do
        if [ -f "$HOME/$f" ]; then
            all="$all$f $(cksum < "$HOME/$f" 2>/dev/null || echo unreadable)
"
        else
            all="$all$f absent
"
        fi
    done
    printf '%s' "$all" | cksum | tr ' ' '-'
}

# _refresh_content_unseen: 0 when the watched content has never been refreshed with here.
_refresh_content_unseen() {
    [ "$HAS_REFRESH_DUE" = "1" ] || return 1
    [ -n "$REFRESH_KEY" ] || REFRESH_KEY=$(_refresh_content_key)
    [ ! -e "$REFRESH_SEEN_DIR/$REFRESH_KEY" ]
}

# _refresh_content_unseen_fresh re-asks after a wait: the key is unchanged (this launch's own
# content), but the holder may have recorded it meanwhile.
_refresh_content_unseen_fresh() {
    [ "$HAS_REFRESH_DUE" = "1" ] || return 1
    [ ! -e "$REFRESH_SEEN_DIR/$REFRESH_KEY" ]
}

_refresh_record_seen() {
    [ "$HAS_REFRESH_DUE" = "1" ] && [ -n "$REFRESH_KEY" ] || return 0
    mkdir -p "$REFRESH_SEEN_DIR" 2>/dev/null && : > "$REFRESH_SEEN_DIR/$REFRESH_KEY" 2>/dev/null
}

_refresh_due() {
    [ "$UPDATES_ENABLED" = "1" ] || return 1
    _refresh_content_unseen && return 0
    [ -f "$REFRESH_STAMP" ] || return 0
    [ "$(( $(date +%s) - $(_stamp_mtime "$REFRESH_STAMP") ))" -gt "$UPDATE_INTERVAL" ]
}

# _wait_for_refresh_lock is the ONE case a held lock is waited on: this launch's watched
# content has never been refreshed with, so the program is about to install what it names —
# and doing that outside the lock, while the holder installs the same thing into the same
# shared store, is the first-install race DueOnChange exists to close. Bounded by
# UPDATE_TIMEOUT; returns 0 once the lock is free (and taken by this launcher), 1 on timeout.
_wait_for_refresh_lock() {
    local waited=0 lrc
    echo "  $BIN: another refresh holds $REFRESH_LOCK and this workspace's add-ons are new — waiting for it (up to ${UPDATE_TIMEOUT}s)..." >&2
    while [ "$waited" -lt "$UPDATE_TIMEOUT" ]; do
        sleep 1
        waited=$((waited + 1))
        lrc=0
        _take_refresh_lock || lrc=$?
        [ "$lrc" = 1 ] || return "$lrc"
    done
    return 1
}

_refresh_touch() { mkdir -p "${REFRESH_STAMP%/*}" 2>/dev/null && touch "$REFRESH_STAMP" 2>/dev/null; }

# _take_refresh_lock returns 0 when this launcher now holds the lock, 1 when another holds it,
# and 2 when it cannot be taken at all. The STORE is never created here (a plain mkdir, never
# -p): a missing one is a mount that did not happen, and inventing it in a per-workspace home
# would hide that.
_take_refresh_lock() {
    if ! mkdir "$REFRESH_LOCK" 2>/dev/null; then
        # mkdir failed and there is no lock, so the STORE is the problem: it is missing, or it
        # refused the write (read-only, EACCES). Either way nobody holds anything.
        [ -d "$REFRESH_LOCK" ] || return 2
        local age
        age=$(( $(date +%s) - $(_stamp_mtime "$REFRESH_LOCK") ))
        [ "$age" -gt "$STALE_LOCK" ] || return 1
        # Remove ONLY the token and an EMPTY directory — never rm -r a path a manifest named.
        rm -f "$REFRESH_LOCK/.yolo-lock-owner" 2>/dev/null || true
        rmdir "$REFRESH_LOCK" 2>/dev/null || true
        mkdir "$REFRESH_LOCK" 2>/dev/null || return 1
    fi
    # The OWNER TOKEN: $$ alone repeats across jails (each has its own pid namespace).
    REFRESH_TOKEN="$$.${RANDOM:-0}.$(date +%s)"
    printf '%s\n' "$REFRESH_TOKEN" > "$REFRESH_LOCK/.yolo-lock-owner" 2>/dev/null || REFRESH_TOKEN=""
    return 0
}

# _drop_refresh_lock releases the lock only while it is still THIS launcher's: a lock another
# launcher broke as stale and re-took must survive the first holder finishing.
_drop_refresh_lock() {
    [ "$(cat "$REFRESH_LOCK/.yolo-lock-owner" 2>/dev/null || true)" = "$REFRESH_TOKEN" ] || return 0
    rm -f "$REFRESH_LOCK/.yolo-lock-owner" 2>/dev/null || true
    rmdir "$REFRESH_LOCK" 2>/dev/null || true
}

# _start_refresh_heartbeat keeps the HELD lock younger than STALE_LOCK for as long as this
# launcher lives (see prelaunchrefresh.go). It stops by itself when the launcher is gone, so a
# killed launcher's lock still ages, and when the lock stops carrying this launcher's token.
# "touch -c": the beat must never CREATE the lock path — a file there would read as "cannot
# take the lock" forever. Its stdio is /dev/null so neither it nor its sleep can hold open the
# pipe of a piped launch.
_start_refresh_heartbeat() {
    local launcher=$$
    (
        while sleep "$REFRESH_HEARTBEAT"; do
            kill -0 "$launcher" 2>/dev/null || exit 0
            [ "$(cat "$REFRESH_LOCK/.yolo-lock-owner" 2>/dev/null || true)" = "$REFRESH_TOKEN" ] || exit 0
            touch -c "$REFRESH_LOCK" 2>/dev/null || exit 0
        done
    ) </dev/null >/dev/null 2>&1 &
    REFRESH_BEAT_PID=$!
}

# _stop_refresh_heartbeat runs BEFORE the lock is released, and waits, so no beat can land
# after the release.
_stop_refresh_heartbeat() {
    [ -n "$REFRESH_BEAT_PID" ] || return 0
    kill "$REFRESH_BEAT_PID" 2>/dev/null || true
    wait "$REFRESH_BEAT_PID" 2>/dev/null || true
    REFRESH_BEAT_PID=""
}

# _prelaunch_refresh ALWAYS RETURNS 0: launching the program outranks refreshing it (§4.1
# invariant 2), so no outcome here may stop the exec below. What varies is what it says.
_prelaunch_refresh() {
    [ "$HAS_REFRESH" = "1" ] || return 0
    [ -x "$REAL_BIN" ] || return 0
    _refresh_due || return 0
    local lrc=0
    _take_refresh_lock || lrc=$?
    if [ "$lrc" = 1 ] && _refresh_content_unseen; then
        lrc=0
        _wait_for_refresh_lock || lrc=$?
        # The holder may have refreshed exactly this content while we waited: then there is
        # nothing left to do, and the lock just taken is released unused.
        if [ "$lrc" = 0 ] && ! _refresh_content_unseen_fresh; then
            _drop_refresh_lock
            return 0
        fi
    fi
    if [ "$lrc" = 1 ]; then
        # No stamp: the holder touches it when it finishes, and a launch after that sees it.
        echo "  $BIN: another refresh holds $REFRESH_LOCK — running what is installed." >&2
        return 0
    fi
    if [ "$lrc" != 0 ]; then
        echo "  ⚠ $BIN: cannot take the refresh lock $REFRESH_LOCK ($REFRESH_STORE is missing or not writable) — skipping the pre-launch refresh." >&2
        # Stamped, so a store that is simply absent says so once an hour rather than every launch.
        # The content key is recorded for the same reason: a refresh that can never run must not
        # turn the change trigger into a warning on every launch.
        _refresh_touch || true
        _refresh_record_seen || true
        return 0
    fi
    echo "  Refreshing $BIN (${REFRESH_ARGV[*]})..." >&2
    local rc=0
    _start_refresh_heartbeat
    # stdin from /dev/null: a refresh must never read the user's terminal. stdout to stderr: a
    # piped launch ("$BIN -p … | consumer") must receive the program's output and nothing else.
    _bounded __YOLO_EXEC_PREFIX__"$REAL_BIN" "${REFRESH_ARGV[@]}" </dev/null >&2 || rc=$?
    _stop_refresh_heartbeat
    # Stamped on EVERY outcome (§4.1 invariant 3): an offline hour must not retry per launch.
    _refresh_touch || true
    # The content key only on SUCCESS: a failed refresh leaves the change due, so the next
    # launch retries the install under the lock instead of leaving it to the program.
    [ "$rc" != 0 ] || _refresh_record_seen || true
    _drop_refresh_lock
    case "$rc" in
        0) ;;
        124) echo "  ⚠ $BIN: the pre-launch refresh timed out after ${UPDATE_TIMEOUT}s — running what is installed." >&2 ;;
        *) echo "  ⚠ $BIN: the pre-launch refresh failed (status $rc) — running what is installed." >&2 ;;
    esac
    return 0
}

_prelaunch_refresh || true
`

// refreshSplices renders the refresh's sentinel pairs for a strings.Replacer, riding on the
// end of each generator's pair list as launchFlagSplices does. A nil refresh renders the
// switch off and an empty argv, so a program that declares none carries the function but
// never enters it.
//
// Join, not Quote, for the argv: it is a LIST that must reach the program as several words,
// landing in the bare `REFRESH_ARGV=(…)` — the same treatment UpdateVerb gets. The lock is
// ONE word, Quote'd, and joined to $HOME inside the script rather than baked absolute, which
// is how every other home path in both templates is spelled.
func refreshSplices(r *packdecl.Refresh) []string {
	if r == nil {
		return []string{
			"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_ARGV__", "",
			"__YOLO_REFRESH_LOCK__", shquote.Quote(""),
			"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_DUE_ON_CHANGE__", "",
		}
	}
	return []string{
		"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(len(r.Argv) > 0 && r.Lock != "")),
		"__YOLO_REFRESH_ARGV__", shquote.Join(r.Argv),
		"__YOLO_REFRESH_LOCK__", shquote.Quote(r.Lock),
		// Join, like the argv: a LIST of home-relative files, each one word.
		"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(len(r.DueOnChange) > 0)),
		"__YOLO_REFRESH_DUE_ON_CHANGE__", shquote.Join(r.DueOnChange),
	}
}
