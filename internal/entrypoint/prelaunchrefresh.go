package entrypoint

import (
	"path"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// prelaunchrefresh.go is the launcher half of a program's PRE-LAUNCH REFRESH (packdecl.Refresh,
// a term coined for that field): the execution and concurrency tiers of
// docs/reference/pi-extensions.md, §17.2 and §17.3.
//
// WHAT IT IS FOR. Pi showed its "Package Updates Available" box until a human ran
// `pi update --extensions` by hand. The refresh is that command, run by the launcher the user
// just invoked, before the exec — so the in-app check that follows finds the packages current.
// It was built for a machine-scoped store every jail read (§3.1, shipped 2026-09-21); since
// XB-D14 of docs/design/pi-extension-store-builds.md pi's npm prefix is per workspace again,
// and the refresh, its lock and its throttle with it.
//
// A THROTTLE HAS ITS LOCK'S SCOPE (that design's §6.2, rule 6). The refresh's stamp and its
// seen-content markers live in RefreshStateDirName, beside the lock, in the store the lock's
// parent names: for pi that is the workspace's own `.pi`, so a refresh in one workspace
// throttles that workspace alone. They lived in the machine-global ~/.cache until XB-D14, where
// a refresh another workspace ran, or a seen marker it recorded, let a new workspace skip the
// refresh it needed and left pi's own startup to install every extension into an empty prefix,
// unlocked.
//
// WHY IT IS DECLARED AND NOT KEYED ON "pi". The plan sketch put a `_refresh_pi_extensions`
// branch into npmLauncherTemplate for binary pi. Both templates are shared by every program,
// and a bin-named branch in them is exactly how core learns what an agent is — the thing
// AGENTS.md's first rule forbids. So the argv and the lock are the pack's declaration, and this
// file renders whatever a pack declares, for both launcher templates.
//
// THE LOCK IS A NON-BLOCKING O_EXCL FILE claim (lockShellFns), not a flock, for three reasons. §3.3 ruled it. It is the
// same primitive both templates' install-prefix `_take_lock` use, because there is no
// flock(1) in the image and none on a stock macOS. And it is the one primitive that is atomic
// on EVERY path this store can be shared over: a flock is per kernel, while Apple Container
// runs each jail in its own VM over a shared host directory, where only the host filesystem's
// own open(2) with O_EXCL arbitrates. (It is not mkdir: uutils coreutils' mkdir succeeds on a
// racing create.)
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
// lock's age. _bounded (updatebound.go) keeps every refresh to UPDATE_TIMEOUT plus UPDATE_GRACE,
// far shorter than STALE_LOCK, wherever the launcher's yolo has the bounded no-terminal verb or
// timeout(1) is on PATH, macos-user included since the verb took the bound over from timeout(1).
// A launcher with neither runs the refresh unbounded, and a live refresh stalled on a slow
// registry there can outlast STALE_LOCK — without the heartbeat a second launcher would then
// break its lock and start a second writer in the same store. The heartbeat touches the lock
// every REFRESH_HEARTBEAT seconds while the launcher that took it is alive and the lock still
// carries that launcher's token, so "older than STALE_LOCK" means "the launcher that took it is
// gone" in every case. It does NOT bound the refresh: where nothing bounds it, a hung refresh
// still hangs the launch that ran it, and while it hangs other launches skip their refresh.
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
const refreshDeclShell = `# The pack's declared PRE-LAUNCH REFRESH (packdecl.Refresh; pi-extensions.md §17.2):
# the program's own argv, bin omitted, and the home-relative LOCK DIRECTORY inside the store
# the refresh writes (§3.3). BAKED, like everything above: a jail cannot talk it into one.
HAS_REFRESH=__YOLO_HAS_REFRESH__
REFRESH_ARGV=(__YOLO_REFRESH_ARGV__)
REFRESH_LOCK_REL=__YOLO_REFRESH_LOCK__
# The directory beside the lock that holds the refresh's stamp and seen markers (XB-D14).
REFRESH_STATE_NAME=__YOLO_REFRESH_STATE_NAME__
# The declared DUE-ON-CHANGE files (packdecl.Refresh.DueOnChange), home-relative. HAS_REFRESH_DUE
# gates every expansion of the list, for HAS_REFRESH's bash-3.2 reason.
HAS_REFRESH_DUE=__YOLO_HAS_REFRESH_DUE__
REFRESH_DUE_ON_CHANGE=(__YOLO_REFRESH_DUE_ON_CHANGE__)
# WHEN the refresh runs: "launch" before the exec, "next-launch" in the background, so the
# launch does not wait and what it installs is the next launch's (program-delivery.md OQ-PD30).
# The user's agent_updates policy, BAKED for UPDATES_ENABLED's reason: a jail cannot move it.
REFRESH_TIMING=__YOLO_REFRESH_TIMING__
# The declared WORTH-RUNNING test (packdecl.Refresh.OnlyIf, pi-extension-store-builds.md XB-D23):
# the refresh runs only when a listed file — home-relative, or relative to where the program
# starts — holds one of the listed strings. HAS_REFRESH_ONLY_IF gates every expansion.
HAS_REFRESH_ONLY_IF=__YOLO_HAS_REFRESH_ONLY_IF__
REFRESH_ONLY_IF_FILES=(__YOLO_REFRESH_ONLY_IF_FILES__)
REFRESH_ONLY_IF_PROJECT=(__YOLO_REFRESH_ONLY_IF_PROJECT__)
REFRESH_ONLY_IF_CONTAINS=(__YOLO_REFRESH_ONLY_IF_CONTAINS__)
# The pack's SESSION HOMES (packdecl.Refresh.SessionHomes, docs/design/per-session-profiles.md §5):
# pairs of a home-relative declared directory and the variable that relocates it. A watched file
# under one resolves under the variable when the session set it. HAS_REFRESH_SESSION_HOMES gates
# every expansion.
HAS_REFRESH_SESSION_HOMES=__YOLO_HAS_REFRESH_SESSION_HOMES__
REFRESH_SESSION_HOMES=(__YOLO_REFRESH_SESSION_HOMES__)
`

// prelaunchRefreshShellFn defines the declared refresh: its variables, its functions, and the
// BACKGROUND JOB'S DISPATCH (a term coined here: the launcher re-entered as the job that runs a
// next-launch refresh, OQ-PD31). It is spliced into every template right after updateBoundShellFn,
// the last fragment it reads, and so ahead of the program's own install and update: the job must
// stop there, before any of them, and run the refresh alone. The CALL that runs the refresh on a
// launch is prelaunchRefreshCallShell, spliced after the MCP/LSP refresh and immediately before
// the exec — §3.2's "strictly before exec", and after every step that can change what $REAL_BIN
// is.
//
// It reads the templates' own UPDATE_TIMEOUT, STALE_LOCK, UPDATES_ENABLED, _stamp_mtime and
// _bounded rather than defining its own, so a refresh is bounded and policy-gated by exactly the
// numbers the program's own update is. Its due rule is the JAIL START's (LU-D8): no clock. It does not read
// STAMP_DIR: that is the machine-global ~/.cache, and the refresh's throttle is its lock's
// (RefreshStateDirName).
// __YOLO_EXEC_PREFIX__ is the npm template's resolved interpreter (a declared node_floor), so a
// `#!/usr/bin/env node` program is refreshed under the node it is launched under; the native
// template renders it empty.
//
// # The background arm (REFRESH_TIMING=next-launch)
//
// The launcher execs the program, so work that outlives the launch has to be started before the
// exec and must belong to nothing the program inherits. _refresh_in_background starts THIS
// LAUNCHER again, as the job, through `yolo internal no-terminal --detach`: a session of its own
// (no terminal, a process group no terminal signal reaches, which a stock macOS has no setsid(1)
// for), a /dev/null stdin, and its stdout and stderr appended to REFRESH_LOG. Re-entering the
// launcher rather than writing the job out twice keeps one implementation of the lock, the
// stamp, the bound and the heartbeat, and needs nothing bash 3.2 lacks: the job is a process of
// its own, so its $$ is its own and the heartbeat follows the job's life, not the agent's.
//
// What the background arm keeps from the launch path: the lock (taken by the job, never waited
// for: a held lock means another refresh is doing this work), the bound (the job's act runs under
// _bounded), the stamp (written on every outcome), and the content key (recorded on success).
// What it does not move: a launch whose watched content has never been refreshed with
// (DueOnChange) still refreshes BEFORE the exec, because that is the first install of what the
// settings name and the race DueOnChange closes is the program installing it unlocked at startup.
// The program's own update and its MCP servers' are not the refresh, and still complete first.
//
// It writes nothing to the terminal once the program runs. The launch says ONE line, before the
// exec, naming the log; a job that did not succeed leaves REFRESH_RESULT, which the next launch
// prints once and deletes — except a FAILED act whose upstream answered, which the next launch
// retries in front of the program and refuses on if it fails again (PD-NF1, _refresh_report_last).
//
// A job that never reaches its own ending is reported too. A podman or Apple Container jail that
// exits SIGKILLs every process in it, so no trap of the job's runs: its lock is left behind, to age
// past STALE_LOCK, and nothing it would have said is written. So the job writes a PROVISIONAL
// result before its act ("did not finish"), carrying the token its lock holds, and replaces it
// under the lock when the act ends. A launch says a provisional result only once the lock no
// longer carries that token or is older than STALE_LOCK, so a second terminal of the same jail
// says nothing while the job still runs. Until then every launcher sharing the store reads the
// dead job's lock as held: it skips its refresh, and one whose watched content is new waits for
// the lock first, up to UPDATE_TIMEOUT.
const prelaunchRefreshShellFn = programPhasePerfShellFn + lockShellFns + `
# --- pre-launch refresh (pi-extensions.md §17.2, §17.3) -------------------------
# The stamp has the LOCK'S SCOPE (pi-extension-store-builds.md XB-D14): it lives beside the
# lock, in the store the lock's parent names, so it throttles exactly the launches the lock
# excludes. For pi that store is the workspace's own .pi, so one refresh a jail start per
# workspace. It is a MARKER per jail start (LU-D8): <bin>.stamp.<id>, the id the boot's
# write_jail_start wrote, so its existence means "this start has had its refresh" and no
# decision reads a clock. An old <bin>.stamp is ignored, never deleted.
# Its directory is yolo's bookkeeping inside the store (named, like the lock, with the prefix
# that tells it from the tool's content), and the bin in each name keeps two programs locking
# one store from colliding.
REFRESH_LOCK="$HOME/$REFRESH_LOCK_REL"
REFRESH_STORE="${REFRESH_LOCK%/*}"
REFRESH_STATE="$REFRESH_STORE/$REFRESH_STATE_NAME"
REFRESH_STAMP="$REFRESH_STATE/$BIN.stamp"
# This start's marker is $REFRESH_STAMP_NOW, empty when the boot wrote no record (an older boot, a
# failed write): then nothing is due by the stamp, and only changed content refreshes. The boot is
# the record's only writer. Read with "$(<file)", which works under env -i and in bash 3.2.
REFRESH_START_FILE="$HOME/.local/state/yolo/jail-start"
REFRESH_START_ID=""
if [ -f "$REFRESH_START_FILE" ]; then REFRESH_START_ID=$(<"$REFRESH_START_FILE") || REFRESH_START_ID=""; fi
REFRESH_STAMP_NOW=""
if [ -n "$REFRESH_START_ID" ]; then REFRESH_STAMP_NOW="$REFRESH_STAMP.$REFRESH_START_ID"; fi
REFRESH_TOKEN=""
REFRESH_HEARTBEAT=60 # seconds between touches of a HELD lock; STALE_LOCK is ten of them
REFRESH_BEAT_PID=""
# One marker per CONTENT KEY a refresh has SUCCEEDED for in this store (DueOnChange). Beside
# the stamp, so with the lock's scope like it; keyed by content, so a store two workspaces with
# different settings share is refreshed once for each and then stops, instead of taking turns.
REFRESH_SEEN_DIR="$REFRESH_STATE/$BIN.seen"
REFRESH_KEY=""
# Where a BACKGROUND refresh (REFRESH_TIMING=next-launch) writes what it says, and what it leaves
# for the next launch to say. In THIS home's own storage (~/.local is per workspace), never the
# store the stamp is in, which may outlive this home: the report is about the job this home started.
REFRESH_LOG_DIR="$HOME/.local/state/yolo/refresh"
REFRESH_LOG="$REFRESH_LOG_DIR/$BIN.log"
REFRESH_RESULT="$REFRESH_LOG_DIR/$BIN.result"

# _refresh_state_dir makes the directory the stamp and the seen markers live in, and never the
# STORE above it: a missing store is a mount that did not happen, and inventing it in a home
# would hide that (_take_refresh_lock). So with no store there is nowhere to throttle, and the
# report of it is said at every launch.
_refresh_state_dir() {
    [ -d "$REFRESH_STORE" ] || return 1
    mkdir -p "$REFRESH_STATE" 2>/dev/null
}

# _refresh_home_file prints where the home-relative watched file $1 is for THIS session: under a
# declared session-home directory whose variable is set, the same place under the variable's
# directory, since that is the copy the program reads (per-session-profiles.md §5); else $HOME/$1.
# A while loop and ${!var}, both in bash 3.2.
_refresh_home_file() {
    local f="$1" i=0 dir var val
    if [ "$HAS_REFRESH_SESSION_HOMES" = "1" ]; then
        while [ "$i" -lt "${#REFRESH_SESSION_HOMES[@]}" ]; do
            dir="${REFRESH_SESSION_HOMES[$i]}"
            var="${REFRESH_SESSION_HOMES[$((i + 1))]}"
            i=$((i + 2))
            val="${!var:-}"
            [ -n "$val" ] || continue
            case "$f" in
                "$dir"/*) printf '%s\n' "$val/${f#"$dir"/}"; return 0 ;;
            esac
        done
    fi
    printf '%s\n' "$HOME/$f"
}

# _refresh_content_key prints one key for the current content of every declared file: an
# absent file contributes "absent", so absent and present differ. cksum is POSIX and ships on a
# stock macOS; the key is only compared, never trusted, so a CRC is enough.
_refresh_content_key() {
    local f p all=""
    for f in "${REFRESH_DUE_ON_CHANGE[@]}"; do
        p=$(_refresh_home_file "$f")
        if [ -f "$p" ]; then
            all="$all$f $(cksum < "$p" 2>/dev/null || echo unreadable)
"
        else
            all="$all$f absent
"
        fi
    done
    printf '%s' "$all" | cksum | tr ' ' '-'
}

# _refresh_content_unseen: 0 when the watched content has never been refreshed with here, and no
# refresh of it failed in THIS jail start (XB-D26): content a failed refresh met waits for the next
# start like any other, so an offline jail does not refresh on every launch.
_refresh_content_unseen() {
    [ "$HAS_REFRESH_DUE" = "1" ] || return 1
    [ -n "$REFRESH_KEY" ] || REFRESH_KEY=$(_refresh_content_key)
    _refresh_content_unseen_fresh
}

# _refresh_content_unseen_fresh re-asks after a wait: the key is unchanged (this launch's own
# content), but the holder may have recorded it meanwhile.
_refresh_content_unseen_fresh() {
    [ "$HAS_REFRESH_DUE" = "1" ] || return 1
    [ ! -e "$REFRESH_SEEN_DIR/$REFRESH_KEY" ] || return 1
    [ ! -f "$REFRESH_SEEN_DIR/$REFRESH_KEY.failed.${REFRESH_START_ID:-none}" ]
}

_refresh_record_seen() {
    [ "$HAS_REFRESH_DUE" = "1" ] && [ -n "$REFRESH_KEY" ] || return 0
    _refresh_state_dir && mkdir -p "$REFRESH_SEEN_DIR" 2>/dev/null &&
        : > "$REFRESH_SEEN_DIR/$REFRESH_KEY" 2>/dev/null
    rm -f "$REFRESH_SEEN_DIR/$REFRESH_KEY".failed* 2>/dev/null || true
}

# _refresh_record_failed records that a refresh of the watched content FAILED (XB-D26), beside the
# seen marker it did not earn, as <key>.failed.<id>, so that content is due again at the next jail
# start. A success removes every one of them.
_refresh_record_failed() {
    [ "$HAS_REFRESH_DUE" = "1" ] && [ -n "$REFRESH_KEY" ] || return 0
    _refresh_state_dir && mkdir -p "$REFRESH_SEEN_DIR" 2>/dev/null &&
        : > "$REFRESH_SEEN_DIR/$REFRESH_KEY.failed.${REFRESH_START_ID:-none}" 2>/dev/null
}

# _refresh_worth is the declared worth-running test (XB-D23): 0 when there is none, or when a
# listed file holds a listed string; 1 when no listed file holds any, so the refresh — and the
# second program process it costs — is skipped. Read with the shell alone, so no shim and no
# missing tool can change its answer.
_refresh_worth() {
    [ "$HAS_REFRESH_ONLY_IF" = "1" ] || return 0
    local f
    for f in ${REFRESH_ONLY_IF_FILES[@]+"${REFRESH_ONLY_IF_FILES[@]}"}; do
        _refresh_file_holds "$(_refresh_home_file "$f")" && return 0
    done
    for f in ${REFRESH_ONLY_IF_PROJECT[@]+"${REFRESH_ONLY_IF_PROJECT[@]}"}; do
        _refresh_file_holds "$PWD/$f" && return 0
    done
    return 1
}

# _refresh_file_holds reads the file with bash's own $(<file), never cat: a cat the user blocked
# (security.blocked_tools) is a shim first on PATH that exits 127, which would skip every refresh
# for good. The braces carry the 2>/dev/null to the substitution's own read error (on a bare
# assignment it does not), so an unreadable file is a silent "no"; bash 3.2 has the form too.
_refresh_file_holds() {
    [ -f "$1" ] || return 1
    local content s
    { content=$(<"$1"); } 2>/dev/null || return 1
    for s in "${REFRESH_ONLY_IF_CONTAINS[@]}"; do
        case "$content" in *"$s"*) return 0 ;; esac
    done
    return 1
}

# _refresh_due: changed content is unseen, or this jail start has no stamp yet (LU-D8). No record:
# only changed content, since the stamp cannot be named.
_refresh_due() {
    [ "$UPDATES_ENABLED" = "1" ] || return 1
    _refresh_content_unseen && return 0
    [ -n "$REFRESH_STAMP_NOW" ] || return 1
    [ ! -e "$REFRESH_STAMP_NOW" ]
}

# _wait_for_refresh_lock is the ONE case a held lock is waited on: this launch's watched
# content has never been refreshed with, so the program is about to install what it names —
# and doing that outside the lock, while the holder installs the same thing into the same
# store, is the first-install race DueOnChange exists to close. Bounded by
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

_refresh_touch() {
    _refresh_state_dir || return 1
    [ -n "$REFRESH_STAMP_NOW" ] || return 0
    : >> "$REFRESH_STAMP_NOW" 2>/dev/null || return 1
    # Garbage, which decides nothing: this bin's stamps of OTHER starts over a week old.
    local f
    for f in "$REFRESH_STAMP".*; do
        { [ "$f" != "$REFRESH_STAMP_NOW" ] && [ -f "$f" ]; } || continue
        if [ "$(( $(date +%s) - $(_stamp_mtime "$f") ))" -gt 604800 ]; then rm -f "$f" 2>/dev/null || true; fi
    done
    return 0
}

# _take_refresh_lock returns 0 when this launcher now holds the lock, 1 when another holds it,
# and 2 when it cannot be taken at all. The STORE is never created here (the claim creates only
# the lock file): a missing one is a mount that did not happen, and inventing it in a per-workspace home
# would hide that.
_take_refresh_lock() {
    local tok="$$.${RANDOM:-0}.$(date +%s)" lrc=0
    _lock_try "$REFRESH_LOCK" "$tok" "$STALE_LOCK" || lrc=$?
    [ "$lrc" = 0 ] || return "$lrc"
    # The OWNER TOKEN ($$ alone repeats across jails: each has its own pid namespace) was written
    # by the claim itself.
    REFRESH_TOKEN="$tok"
    return 0
}

# _refresh_lock_owner prints the lock's owner token, or nothing, read by the shell alone for
# _refresh_file_holds' reason: through a blocked cat no launcher would ever see its own token, so
# none would release its lock, and every launch for STALE_LOCK after would find it held.
_refresh_lock_owner() {
    _lock_owner "$REFRESH_LOCK"
}

# _drop_refresh_lock releases the lock only while it is still THIS launcher's: a lock another
# launcher broke as stale and re-took must survive the first holder finishing.
_drop_refresh_lock() {
    [ "$(_refresh_lock_owner)" = "$REFRESH_TOKEN" ] || return 0
    rm -f "$REFRESH_LOCK" 2>/dev/null || true
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
            [ "$(_refresh_lock_owner)" = "$REFRESH_TOKEN" ] || exit 0
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

# _refresh_act is the refresh itself and its bookkeeping, all under the refresh lock; it returns
# the refresh's status. stdin from /dev/null: a refresh must never read the user's terminal.
# stdout to stderr: a piped launch ("$BIN -p … | consumer") must receive the program's output and
# nothing else.
_refresh_act() {
    local rc=0 _phase_start=$SECONDS _phase_mode="${1:-foreground}"
    _yolo_phase_perf program_refresh "$BIN" start "$_phase_mode" 0 pending
    _bounded __YOLO_EXEC_PREFIX__"$REAL_BIN" "${REFRESH_ARGV[@]}" </dev/null >&2 || rc=$?
    _yolo_phase_perf program_refresh "$BIN" end "$_phase_mode" "$((SECONDS - _phase_start))" "$rc"
    _stop_refresh_heartbeat
    # Stamped on EVERY outcome (§4.1 invariant 3): an offline hour must not retry per launch.
    _refresh_touch || true
    # The content key only on SUCCESS: a failed refresh leaves the change due, so a later launch
    # retries the install under the lock instead of leaving it to the program — once the failure
    # it records is from an earlier jail start (XB-D26), not at every launch an offline start makes.
    if [ "$rc" = 0 ]; then
        _refresh_record_seen || true
        # A refresh that succeeded clears a failed background refresh's report (_refresh_report_last).
        rm -f "$REFRESH_RESULT" 2>/dev/null || true
    else
        _refresh_record_failed || true
    fi
    return "$rc"
}

# --- the background refresh (REFRESH_TIMING=next-launch; program-delivery.md OQ-PD31) ---------
# _refresh_note_result WHAT NEXT [TOKEN] leaves the one line the next launch says about a
# background refresh that did not succeed: what happened, where its log is, and what to do.
# Written whole and renamed, so a launch never prints half of one. With TOKEN (the lock's owner
# token, which may be empty) it is PROVISIONAL: a first line "pending TOKEN" that
# _refresh_report_last reads and never prints.
_refresh_note_result() {
    {
        [ "$#" -lt 3 ] || printf 'pending %s\n' "$3"
        printf '  ⚠ %s: the background refresh (%s) %s — its log is %s. %s\n' \
            "$BIN" "${REFRESH_ARGV[*]}" "$1" "$REFRESH_LOG" "$2"
    } > "$REFRESH_RESULT.tmp" 2>/dev/null &&
        mv -f "$REFRESH_RESULT.tmp" "$REFRESH_RESULT" 2>/dev/null || true
}

# _refresh_note_failed WHAT leaves a FAILED result (PD-NF1): the job's act failed and its upstream
# answered. Its first line is "failed", which _refresh_report_last acts on rather than only says.
_refresh_note_failed() {
    {
        printf 'failed\n'
        printf '  ⚠ %s: the background refresh (%s) %s — its log is %s.\n' \
            "$BIN" "${REFRESH_ARGV[*]}" "$1" "$REFRESH_LOG"
    } > "$REFRESH_RESULT.tmp" 2>/dev/null &&
        mv -f "$REFRESH_RESULT.tmp" "$REFRESH_RESULT" 2>/dev/null || true
}

# _REFRESH_RETRY is 1 once _refresh_report_last found a FAILED background refresh: this launch runs
# the refresh again IN FRONT of the program, whatever the stamp and the timing say, and refuses if it
# fails again with its upstream reachable (PD-NF1).
_REFRESH_RETRY=0
# _REFRESH_USE_INSTALLED is launch-scoped: the hatch runs the installed program without starting
# another refresh later in this same invocation.
_REFRESH_USE_INSTALLED=0

# _refresh_report_last says, once, how the last background refresh this home started ended, when
# it did not succeed. A success leaves nothing to say. A PROVISIONAL result is the job's own
# "did not finish", which the job replaces under its lock when its act ends: while the lock still
# carries the job's token and is no older than STALE_LOCK the job may still be running, so it waits
# for a later launch. Read once, so what is said is one whole result.
#
# A FAILED result (the job's act failed and its upstream answered) is not only said (PD-NF1): under
# the user's hatch it is said, deleted, and what is installed runs; otherwise it is said and KEPT, and
# this launch retries the refresh in front of the program (_REFRESH_RETRY), refusing if that fails as
# well. THE CLEAR RULE: a failed result is deleted by a refresh that succeeds (foreground or
# background, _refresh_act), by a launch the hatch covers, and by a retry that could not reach the
# upstream (nothing is then known to be newer); a refused retry keeps it, so the next launch retries in
# front again rather than starting a background refresh and running the older version meanwhile.
_refresh_report_last() {
    [ -s "$REFRESH_RESULT" ] || return 0
    local said first nl='
'
    { said=$(<"$REFRESH_RESULT"); } 2>/dev/null || return 0
    first=${said%%"$nl"*}
    if [ "$first" = "failed" ]; then
        case $said in
        *"$nl"*) said=${said#*"$nl"} ;;
        *) said="" ;;
        esac
        [ -z "$said" ] || printf '%s\n' "$said" >&2
        # An agent_updates hold (UPDATES_ENABLED, which _refresh_due is otherwise the only reader of)
        # runs what is installed, as it does for every update act: no retry, so no refusal.
        if [ "$UPDATES_ENABLED" != "1" ]; then
            echo "  $BIN: agent_updates holds its updates, so it is not retried — running what is installed." >&2
            rm -f "$REFRESH_RESULT" 2>/dev/null || true
            return 0
        fi
        if _yolo_use_installed; then
            echo "  $BIN: running what is installed, as ` + patchrecovery.UseInstalledEnv + ` asks." >&2
            _REFRESH_USE_INSTALLED=1
            rm -f "$REFRESH_RESULT" 2>/dev/null || true
            return 0
        fi
        echo "  $BIN: retrying it before $BIN starts." >&2
        _REFRESH_RETRY=1
        return 0
    fi
    if [ "${first%% *}" = "pending" ]; then
        if [ -e "$REFRESH_LOCK" ] &&
            [ "$(_refresh_lock_owner)" = "${first#pending }" ] &&
            [ "$(( $(date +%s) - $(_stamp_mtime "$REFRESH_LOCK") ))" -le "$STALE_LOCK" ]; then
            return 0
        fi
        case $said in
        *"$nl"*) said=${said#*"$nl"} ;;
        *) said="" ;;
        esac
    fi
    [ -z "$said" ] || printf '%s\n' "$said" >&2
    rm -f "$REFRESH_RESULT" 2>/dev/null || true
}

# _refresh_job is the background refresh itself: this launcher, re-entered by
# _refresh_in_background, in a session of its own with its output in REFRESH_LOG. It runs the act
# the launch path runs, under the same lock, bound and stamp, and it never waits: a lock another
# holds means another refresh is doing this work already, so this one stops.
_refresh_job() {
    local lrc=0 rc=0
    echo "--- $(date '+%Y-%m-%d %H:%M:%S') $BIN: background refresh (${REFRESH_ARGV[*]})"
    _take_refresh_lock || lrc=$?
    if [ "$lrc" = 1 ]; then
        echo "--- another refresh holds $REFRESH_LOCK and is doing this work; this one stops."
        return 0
    fi
    if [ "$lrc" != 0 ]; then
        # Stamped where the store can hold a stamp, as the launch path does. No content key is
        # recorded: a job is started only for content a refresh has already seen.
        _refresh_touch || true
        _refresh_note_result "could not take its lock ($REFRESH_STORE is missing or not writable)" \
            "Nothing was refreshed: check that $REFRESH_STORE is a directory this jail can write."
        # Last, like every job's closing line: its log says a job has ended only once it has.
        echo "--- cannot take the refresh lock $REFRESH_LOCK ($REFRESH_STORE is missing or not writable)."
        return 0
    fi
    # Asked again UNDER the lock: a refresh that finished after this job was started stamped it.
    if ! _refresh_due; then
        _drop_refresh_lock
        echo "--- already refreshed since this job was started; nothing to do."
        return 0
    fi
    # The PROVISIONAL result (prelaunchrefresh.go): what a later launch says if nothing replaces it,
    # because the jail exited under the job or the job was stopped. _refresh_job_act replaces it.
    _refresh_note_result "did not finish (the jail exited, or the job was stopped, while it ran)" \
        "A launch runs it again once its lock is free, and the lock of an exited jail frees itself after $((STALE_LOCK / 60)) minutes; to retry now, run: $BIN ${REFRESH_ARGV[*]}" \
        "$REFRESH_TOKEN"
    _start_refresh_heartbeat
    # _shielded for its SIGTERM and SIGHUP arms, which release the lock when the JOB is sent one (a
    # kill of its process group). A podman or Apple Container jail that exits sends none: it
    # SIGKILLs the job, no trap runs, and the lock stays until it is older than STALE_LOCK — what
    # the provisional result above reports. Nothing here has a terminal to type a Ctrl-C into.
    _shielded '_stop_refresh_heartbeat; _drop_refresh_lock' _refresh_job_act || rc=$?
    echo "--- $(date '+%Y-%m-%d %H:%M:%S') $BIN: background refresh ended (status $rc)"
    return 0
}

# _refresh_job_act is the job's act: _refresh_act, then the result that replaces the provisional
# one. Both run under the lock, inside the shield, so a launch that finds the lock released finds
# the result final.
_refresh_job_act() {
    local rc=0
    _refresh_act detached || rc=$?
    if [ "$rc" = 0 ]; then
        rm -f "$REFRESH_RESULT" 2>/dev/null || true
    elif ! _yolo_registry_reachable; then
        # COULD NOT CHECK (PD-NF3): its upstream did not answer, so nothing says a newer version
        # exists. Nothing for the next launch to act on; the log says what happened.
        echo "--- the refresh did not succeed, and the npm registry could not be reached to check for a newer version."
        rm -f "$REFRESH_RESULT" 2>/dev/null || true
    elif [ "$rc" = 124 ]; then
        _refresh_note_failed "timed out after ${UPDATE_TIMEOUT}s"
    else
        _refresh_note_failed "failed (status $rc)"
    fi
    return "$rc"
}

# _refresh_in_background starts the job and says so, or says why it need not. It returns 0 when
# the refresh is handled (the job started, or a live lock shows one already running), 1 when
# this launch cannot start a job (no yolo with the detach verb, or no log directory), and 2 when
# the store is unusable — the launch path then reports that as it always has.
_refresh_in_background() {
    # A job never starts a job: the dispatch below takes every re-entered launcher before it gets
    # here, and this line keeps a launcher that somehow did from starting them in a chain.
    [ -z "${_YOLO_REFRESH_JOB:-}" ] || return 1
    [ -d "$REFRESH_STORE" ] || return 2
    # A LIVE lock is a refresh already running, in this jail or another: a second job would only
    # find it held.
    if [ -e "$REFRESH_LOCK" ] &&
        [ "$(( $(date +%s) - $(_stamp_mtime "$REFRESH_LOCK") ))" -le "$STALE_LOCK" ]; then
        echo "  $BIN: another refresh holds $REFRESH_LOCK — running what is installed." >&2
        return 0
    fi
    command -v yolo >/dev/null 2>&1 || return 1
    mkdir -p "$REFRESH_LOG_DIR" 2>/dev/null || return 1
    # The job is this launcher, re-entered: _YOLO_REFRESH_JOB sends it to the dispatch, and
    # _YOLO_LAUNCHER_ACTIVE goes back to what it was before this launcher added $BIN, or the
    # re-entry guard would exec the program instead. "$0" is this script's own path.
    _YOLO_LAUNCHER_ACTIVE="${_YOLO_LAUNCHER_ACTIVE%":$BIN"}" _YOLO_REFRESH_JOB="$BIN" YOLO_BYPASS_SHIMS=1 \
        yolo internal ` + NoTerminalVerb + ` --detach --log="$REFRESH_LOG" -- "$0" </dev/null >/dev/null 2>&1 || return 1
    echo "  $BIN: refreshing in the background (${REFRESH_ARGV[*]}); the next launch runs what it installs. Its log: $REFRESH_LOG" >&2
    return 0
}

# The JOB'S DISPATCH: re-entered as the background job, this launcher runs the refresh and nothing
# else — no install, no update, no MCP server refresh, no exec — and ends.
if [ "${_YOLO_REFRESH_JOB:-}" = "$BIN" ]; then
    unset _YOLO_REFRESH_JOB
    if [ "$HAS_REFRESH" = "1" ] && [ -x "$REAL_BIN" ]; then
        _refresh_job || true
    fi
    exit 0
fi

# _prelaunch_refresh RETURNS 0 or STOPS THE COMMAND. Launching the program outranks refreshing it
# (§4.1 invariant 2) for every outcome but one: a refresh that failed while its upstream answered,
# which refuses (PD-NF1) unless the user's hatch covers the program. A held lock, a store it cannot
# write, an unreachable upstream and a Ctrl-C all still run what is installed.
_prelaunch_refresh() {
    [ "$HAS_REFRESH" = "1" ] || return 0
    [ -x "$REAL_BIN" ] || return 0
    _refresh_report_last
    # The one-invocation hatch is for this program run, not a reason to retry in the background.
    [ "$_REFRESH_USE_INSTALLED" = 1 ] && return 0
    if ! _refresh_worth; then
        # Nothing here is worth refreshing any more, so a failed result has nothing to retry.
        if [ "$_REFRESH_RETRY" = 1 ]; then rm -f "$REFRESH_RESULT" 2>/dev/null || true; fi
        return 0
    fi
    [ "$_REFRESH_RETRY" = 1 ] || _refresh_due || return 0
    # NEXT-LAUNCH (OQ-PD30): the refresh runs behind the program instead of in front of it —
    # unless this launch's watched content is new, which is the first install of what the
    # settings name, and stays in front (OQ-PD31), or a background refresh failed (_REFRESH_RETRY).
    if [ "$REFRESH_TIMING" = "next-launch" ] && [ "$_REFRESH_RETRY" != 1 ] && ! _refresh_content_unseen; then
        local brc=0
        _refresh_in_background || brc=$?
        if [ "$brc" = 0 ]; then
            return 0
        fi
        if [ "$brc" = 1 ]; then
            echo "  ($BIN: yolo cannot run this refresh in the background here, so it runs now)" >&2
        fi
    fi
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
        # Every stop names the next step, and the two causes have different ones. A missing store
        # is a mount that did not happen, which a restart of the jail makes again. A store that is
        # there refused the write or holds something other than a directory at the lock's path,
        # and the one command that tells those apart is named with both paths.
        if [ ! -d "$REFRESH_STORE" ]; then
            echo "  ⚠ $BIN: cannot take the refresh lock: $REFRESH_STORE is missing, so this jail did not mount it — skipping the pre-launch refresh. To mount it, restart the jail: yolo stop on the host, then launch again." >&2
        else
            echo "  ⚠ $BIN: cannot take the refresh lock $REFRESH_LOCK ($REFRESH_STORE refuses writes, or something unusable, such as a dangling link, is at that path) — skipping the pre-launch refresh. See which: ls -ld $(printf '%q %q' "$REFRESH_STORE" "$REFRESH_LOCK")" >&2
        fi
        # Stamped where the store can hold a stamp, so a lock path something else occupies says
        # so once a jail start rather than every launch, and the content key is recorded for the same
        # reason. A store that is missing or refuses writes has nowhere to keep either, so it is
        # said at every launch (XB-D14): a stamp kept anywhere else would be the machine-wide
        # throttle that let one jail's failed mount silence another's refresh.
        _refresh_touch || true
        _refresh_record_seen || true
        return 0
    fi
    echo "  Refreshing $BIN (${REFRESH_ARGV[*]})..." >&2
    local rc=0
    _start_refresh_heartbeat
    # _shielded, as the program's own update is: a Ctrl-C ends the refresh and the program still
    # runs, a SIGTERM or SIGHUP releases the lock before it ends the launcher, and _shielded
    # releases it on the way out, after _refresh_act has stamped and recorded under it.
    _shielded '_stop_refresh_heartbeat; _drop_refresh_lock' _refresh_act || rc=$?
    if [ "$rc" = 0 ]; then
        :
    elif [ "$_YOLO_INTERRUPTED" = 1 ]; then
        # The user's own Ctrl-C is the hatch at that moment (PD-NF4).
        echo "  ⚠ $BIN: the pre-launch refresh was interrupted (Ctrl-C) — running what is installed." >&2
    else
        # A FAILED REFRESH has no version query (PD-NF3): its upstream decides. Unreachable runs
        # what is installed, said in one line, and clears a failed background result (nothing is
        # known to be newer); reachable REFUSES (PD-NF1), with the refresh's stamp removed so the
        # next launch retries it rather than running the older content for the rest of the start. A launch the
        # hatch covers clears the background result too.
        _yolo_failed_act "$rc" "the pre-launch refresh (${REFRESH_ARGV[*]})" _yolo_registry_reachable
        if [ -z "$_YOLO_FAIL_HEAD" ] || _yolo_use_installed; then
            rm -f "$REFRESH_RESULT" 2>/dev/null || true
        fi
        _yolo_refuse_failed "$REFRESH_STAMP_NOW"
    fi
    return 0
}
`

// prelaunchRefreshCallShell runs the refresh on a launch, spliced after the MCP/LSP refresh and
// immediately before the exec (prelaunchRefreshShellFn says why the call and the definitions sit
// apart).
const prelaunchRefreshCallShell = `
[ "${_YOLO_PROBE:-}" = "1" ] || _prelaunch_refresh || true
`

// RefreshStateDirName is the directory, beside a pre-launch refresh's lock in the store the
// lock's parent names, that holds the refresh's stamp (<bin>.stamp) and its seen-content markers
// (<bin>.seen/): XB-D14 of docs/design/pi-extension-store-builds.md, "a throttle has its lock's
// scope". It carries packdecl.StoreBookkeepingPrefix for the lock's own reason: a store's
// emptiness is judged by its entries (the shared_directory hook), and yolo's bookkeeping must
// not read as the tool's content. The launcher templates are handed it as a splice, so this
// constant is the one spelling.
const RefreshStateDirName = packdecl.StoreBookkeepingPrefix + "refresh"

// RefreshStampRel is the home-relative path of bin's refresh stamp BASE, for the home-relative lock
// a pack declares. The marker a jail start leaves is that base plus "." and the start's id.
func RefreshStampRel(lockRel, bin string) string {
	return path.Join(path.Dir(lockRel), RefreshStateDirName, bin+".stamp")
}

// RefreshSeenRel is the home-relative directory of bin's seen-content markers, for the
// home-relative lock a pack declares.
func RefreshSeenRel(lockRel, bin string) string {
	return path.Join(path.Dir(lockRel), RefreshStateDirName, bin+".seen")
}

// refreshSplices renders the refresh's sentinel pairs for a strings.Replacer, riding on the
// end of each generator's pair list as launchFlagSplices does. A nil refresh renders the
// switch off and an empty argv, so a program that declares none carries the function but
// never enters it.
//
// Join, not Quote, for the argv: it is a LIST that must reach the program as several words,
// landing in the bare `REFRESH_ARGV=(…)` — the same treatment UpdateVerb gets. The lock is
// ONE word, Quote'd, and joined to $HOME inside the script rather than baked absolute, which
// is how every other home path in both templates is spelled.
//
// timing is Install.RefreshTiming, the user's: anything but "next-launch" bakes "launch", so a
// value the generator did not set runs the refresh where it always ran.
func refreshSplices(r *packdecl.Refresh, timing string) []string {
	if timing != config.AgentUpdatesNextLaunch {
		timing = config.AgentUpdatesAtLaunch
	}
	if r == nil {
		return append([]string{
			"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_ARGV__", "",
			"__YOLO_REFRESH_LOCK__", shquote.Quote(""),
			"__YOLO_REFRESH_STATE_NAME__", shquote.Quote(RefreshStateDirName),
			"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_DUE_ON_CHANGE__", "",
			"__YOLO_REFRESH_TIMING__", shquote.Quote(timing),
		}, append(onlyIfSplices(nil), sessionHomeSplices(nil)...)...)
	}
	return append([]string{
		"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(len(r.Argv) > 0 && r.Lock != "")),
		"__YOLO_REFRESH_ARGV__", shquote.Join(r.Argv),
		"__YOLO_REFRESH_LOCK__", shquote.Quote(r.Lock),
		"__YOLO_REFRESH_STATE_NAME__", shquote.Quote(RefreshStateDirName),
		// Join, like the argv: a LIST of home-relative files, each one word.
		"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(len(r.DueOnChange) > 0)),
		"__YOLO_REFRESH_DUE_ON_CHANGE__", shquote.Join(r.DueOnChange),
		"__YOLO_REFRESH_TIMING__", shquote.Quote(timing),
	}, append(onlyIfSplices(r.OnlyIf), sessionHomeSplices(r.SessionHomes)...)...)
}

// sessionHomeSplices renders the pack's session homes for _refresh_home_file: one word pair per
// declaration, the declared directory and its variable. Off, with an empty list, for none. Join,
// as for the argv: a LIST whose every entry is one word.
func sessionHomeSplices(homes []packdecl.SessionHome) []string {
	var pairs []string
	for _, h := range homes {
		pairs = append(pairs, strings.TrimSuffix(h.Dir, "/"), h.Env)
	}
	return []string{
		"__YOLO_HAS_REFRESH_SESSION_HOMES__", shquote.Quote(boolFlag(len(pairs) > 0)),
		"__YOLO_REFRESH_SESSION_HOMES__", shquote.Join(pairs),
	}
}

// onlyIfSplices renders a refresh's worth-running test (XB-D23): off, with empty lists, for none.
// Join, as for the argv: LISTS whose every entry is one word.
func onlyIfSplices(o *packdecl.RefreshOnlyIf) []string {
	if o == nil {
		return []string{
			"__YOLO_HAS_REFRESH_ONLY_IF__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_ONLY_IF_FILES__", "",
			"__YOLO_REFRESH_ONLY_IF_PROJECT__", "",
			"__YOLO_REFRESH_ONLY_IF_CONTAINS__", "",
		}
	}
	return []string{
		"__YOLO_HAS_REFRESH_ONLY_IF__", shquote.Quote(boolFlag(len(o.Contains) > 0)),
		"__YOLO_REFRESH_ONLY_IF_FILES__", shquote.Join(o.Files),
		"__YOLO_REFRESH_ONLY_IF_PROJECT__", shquote.Join(o.ProjectFiles),
		"__YOLO_REFRESH_ONLY_IF_CONTAINS__", shquote.Join(o.Contains),
	}
}
