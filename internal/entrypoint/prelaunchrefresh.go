package entrypoint

import (
	"path"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// prelaunchrefresh.go is the launcher half of a program's PRE-LAUNCH REFRESH (packdecl.Refresh,
// a term coined for that field): the execution and concurrency tiers of
// docs/design/pi-extension-lifecycle.md, §3.2 and §3.3.
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
const refreshDeclShell = `# The pack's declared PRE-LAUNCH REFRESH (packdecl.Refresh; pi-extension-lifecycle.md §3.2):
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
# The declared WORTH-RUNNING test (packdecl.Refresh.OnlyIf, pi-extension-store-builds.md XB-D23):
# the refresh runs only when a listed file — home-relative, or relative to where the program
# starts — holds one of the listed strings. HAS_REFRESH_ONLY_IF gates every expansion.
HAS_REFRESH_ONLY_IF=__YOLO_HAS_REFRESH_ONLY_IF__
REFRESH_ONLY_IF_FILES=(__YOLO_REFRESH_ONLY_IF_FILES__)
REFRESH_ONLY_IF_PROJECT=(__YOLO_REFRESH_ONLY_IF_PROJECT__)
REFRESH_ONLY_IF_CONTAINS=(__YOLO_REFRESH_ONLY_IF_CONTAINS__)
`

// prelaunchRefreshShellFn runs the declared refresh, and is spliced into both templates after
// the MCP/LSP refresh and immediately before the exec — §3.2's "strictly before exec", and
// after every step that can change what $REAL_BIN is.
//
// It reads the templates' own UPDATE_INTERVAL, UPDATE_TIMEOUT, STALE_LOCK, UPDATES_ENABLED,
// _stamp_mtime and _bounded rather than defining its own, so a refresh is throttled, bounded
// and policy-gated by exactly the numbers the program's own update is. It does not read
// STAMP_DIR: that is the machine-global ~/.cache, and the refresh's throttle is its lock's
// (RefreshStateDirName).
// __YOLO_EXEC_PREFIX__ is the npm template's resolved interpreter (a declared node_floor), so a
// `#!/usr/bin/env node` program is refreshed under the node it is launched under; the native
// template renders it empty.
const prelaunchRefreshShellFn = `
# --- pre-launch refresh (pi-extension-lifecycle.md §3.2, §3.3) -------------------------
# The stamp has the LOCK'S SCOPE (pi-extension-store-builds.md XB-D14): it lives beside the
# lock, in the store the lock's parent names, so it throttles exactly the launches the lock
# excludes. For pi that store is the workspace's own .pi, so one refresh an hour per workspace.
# Its directory is yolo's bookkeeping inside the store (named, like the lock, with the prefix
# that tells it from the tool's content), and the bin in each name keeps two programs locking
# one store from colliding.
REFRESH_LOCK="$HOME/$REFRESH_LOCK_REL"
REFRESH_STORE="${REFRESH_LOCK%/*}"
REFRESH_STATE="$REFRESH_STORE/$REFRESH_STATE_NAME"
REFRESH_STAMP="$REFRESH_STATE/$BIN.stamp"
REFRESH_TOKEN=""
REFRESH_HEARTBEAT=60 # seconds between touches of a HELD lock; STALE_LOCK is ten of them
REFRESH_BEAT_PID=""
# One marker per CONTENT KEY a refresh has SUCCEEDED for in this store (DueOnChange). Beside
# the stamp, so with the lock's scope like it; keyed by content, so a store two workspaces with
# different settings share is refreshed once for each and then stops, instead of taking turns.
REFRESH_SEEN_DIR="$REFRESH_STATE/$BIN.seen"
REFRESH_KEY=""

# _refresh_state_dir makes the directory the stamp and the seen markers live in, and never the
# STORE above it: a missing store is a mount that did not happen, and inventing it in a home
# would hide that (_take_refresh_lock). So with no store there is nowhere to throttle, and the
# report of it is said at every launch.
_refresh_state_dir() {
    [ -d "$REFRESH_STORE" ] || return 1
    mkdir -p "$REFRESH_STATE" 2>/dev/null
}

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

# _refresh_content_unseen: 0 when the watched content has never been refreshed with here, and no
# refresh of it failed within UPDATE_INTERVAL (XB-D26): content a failed refresh met waits out the
# hourly stamp like any other, so an offline jail does not refresh on every launch.
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
    local failed="$REFRESH_SEEN_DIR/$REFRESH_KEY.failed"
    [ -f "$failed" ] || return 0
    [ "$(( $(date +%s) - $(_stamp_mtime "$failed") ))" -gt "$UPDATE_INTERVAL" ]
}

_refresh_record_seen() {
    [ "$HAS_REFRESH_DUE" = "1" ] && [ -n "$REFRESH_KEY" ] || return 0
    _refresh_state_dir && mkdir -p "$REFRESH_SEEN_DIR" 2>/dev/null &&
        : > "$REFRESH_SEEN_DIR/$REFRESH_KEY" 2>/dev/null
    rm -f "$REFRESH_SEEN_DIR/$REFRESH_KEY.failed" 2>/dev/null || true
}

# _refresh_record_failed records that a refresh of the watched content FAILED (XB-D26), beside the
# seen marker it did not earn, so that content is due again only past UPDATE_INTERVAL.
_refresh_record_failed() {
    [ "$HAS_REFRESH_DUE" = "1" ] && [ -n "$REFRESH_KEY" ] || return 0
    _refresh_state_dir && mkdir -p "$REFRESH_SEEN_DIR" 2>/dev/null &&
        : > "$REFRESH_SEEN_DIR/$REFRESH_KEY.failed" 2>/dev/null
}

# _refresh_worth is the declared worth-running test (XB-D23): 0 when there is none, or when a
# listed file holds a listed string; 1 when no listed file holds any, so the refresh — and the
# second program process it costs — is skipped. Read with the shell alone, so no shim and no
# missing tool can change its answer.
_refresh_worth() {
    [ "$HAS_REFRESH_ONLY_IF" = "1" ] || return 0
    local f
    for f in ${REFRESH_ONLY_IF_FILES[@]+"${REFRESH_ONLY_IF_FILES[@]}"}; do
        _refresh_file_holds "$HOME/$f" && return 0
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

_refresh_due() {
    [ "$UPDATES_ENABLED" = "1" ] || return 1
    _refresh_content_unseen && return 0
    [ -f "$REFRESH_STAMP" ] || return 0
    [ "$(( $(date +%s) - $(_stamp_mtime "$REFRESH_STAMP") ))" -gt "$UPDATE_INTERVAL" ]
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

_refresh_touch() { _refresh_state_dir && touch "$REFRESH_STAMP" 2>/dev/null; }

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

# _refresh_lock_owner prints the lock's owner token, or nothing, read by the shell alone for
# _refresh_file_holds' reason: through a blocked cat no launcher would ever see its own token, so
# none would release its lock, and every launch for STALE_LOCK after would find it held.
_refresh_lock_owner() {
    local owner=""
    { owner=$(<"$REFRESH_LOCK/.yolo-lock-owner"); } 2>/dev/null || owner=""
    printf '%s' "$owner"
}

# _drop_refresh_lock releases the lock only while it is still THIS launcher's: a lock another
# launcher broke as stale and re-took must survive the first holder finishing.
_drop_refresh_lock() {
    [ "$(_refresh_lock_owner)" = "$REFRESH_TOKEN" ] || return 0
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
    local rc=0
    _bounded __YOLO_EXEC_PREFIX__"$REAL_BIN" "${REFRESH_ARGV[@]}" </dev/null >&2 || rc=$?
    _stop_refresh_heartbeat
    # Stamped on EVERY outcome (§4.1 invariant 3): an offline hour must not retry per launch.
    _refresh_touch || true
    # The content key only on SUCCESS: a failed refresh leaves the change due, so a later launch
    # retries the install under the lock instead of leaving it to the program — once the failure
    # it records is past UPDATE_INTERVAL (XB-D26), not at every launch an offline hour makes.
    if [ "$rc" = 0 ]; then
        _refresh_record_seen || true
    else
        _refresh_record_failed || true
    fi
    return "$rc"
}

# _prelaunch_refresh ALWAYS RETURNS 0: launching the program outranks refreshing it (§4.1
# invariant 2), so no outcome here may stop the exec below. What varies is what it says.
_prelaunch_refresh() {
    [ "$HAS_REFRESH" = "1" ] || return 0
    # A VERSION PROBE runs no refresh (XB-D24): it asks the program about itself.
    [ "$_YOLO_PROBE" != 1 ] || return 0
    [ -x "$REAL_BIN" ] || return 0
    _refresh_worth || return 0
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
        # Every stop names the next step, and the two causes have different ones. A missing store
        # is a mount that did not happen, which a restart of the jail makes again. A store that is
        # there refused the write or holds something other than a directory at the lock's path,
        # and the one command that tells those apart is named with both paths.
        if [ ! -d "$REFRESH_STORE" ]; then
            echo "  ⚠ $BIN: cannot take the refresh lock: $REFRESH_STORE is missing, so this jail did not mount it — skipping the pre-launch refresh. To mount it, restart the jail: yolo stop on the host, then launch again." >&2
        else
            echo "  ⚠ $BIN: cannot take the refresh lock $REFRESH_LOCK ($REFRESH_STORE refuses writes, or something that is not a directory is at that path) — skipping the pre-launch refresh. See which: ls -ld $(printf '%q %q' "$REFRESH_STORE" "$REFRESH_LOCK")" >&2
        fi
        # Stamped where the store can hold a stamp, so a lock path something else occupies says
        # so once an hour rather than every launch, and the content key is recorded for the same
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
        echo "  ⚠ $BIN: the pre-launch refresh was interrupted (Ctrl-C) — running what is installed." >&2
    elif [ "$rc" = 124 ]; then
        echo "  ⚠ $BIN: the pre-launch refresh timed out after ${UPDATE_TIMEOUT}s — running what is installed." >&2
    else
        echo "  ⚠ $BIN: the pre-launch refresh failed (status $rc) — running what is installed." >&2
    fi
    return 0
}

_prelaunch_refresh || true
`

// RefreshStateDirName is the directory, beside a pre-launch refresh's lock in the store the
// lock's parent names, that holds the refresh's stamp (<bin>.stamp) and its seen-content markers
// (<bin>.seen/): XB-D14 of docs/design/pi-extension-store-builds.md, "a throttle has its lock's
// scope". It carries packdecl.StoreBookkeepingPrefix for the lock's own reason: a store's
// emptiness is judged by its entries (the shared_directory hook), and yolo's bookkeeping must
// not read as the tool's content. The launcher templates are handed it as a splice, so this
// constant is the one spelling.
const RefreshStateDirName = packdecl.StoreBookkeepingPrefix + "refresh"

// RefreshStampRel is the home-relative path of bin's refresh stamp, for the home-relative lock
// a pack declares.
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
func refreshSplices(r *packdecl.Refresh) []string {
	if r == nil {
		return append([]string{
			"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_ARGV__", "",
			"__YOLO_REFRESH_LOCK__", shquote.Quote(""),
			"__YOLO_REFRESH_STATE_NAME__", shquote.Quote(RefreshStateDirName),
			"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(false)),
			"__YOLO_REFRESH_DUE_ON_CHANGE__", "",
		}, onlyIfSplices(nil)...)
	}
	return append([]string{
		"__YOLO_HAS_REFRESH__", shquote.Quote(boolFlag(len(r.Argv) > 0 && r.Lock != "")),
		"__YOLO_REFRESH_ARGV__", shquote.Join(r.Argv),
		"__YOLO_REFRESH_LOCK__", shquote.Quote(r.Lock),
		"__YOLO_REFRESH_STATE_NAME__", shquote.Quote(RefreshStateDirName),
		// Join, like the argv: a LIST of home-relative files, each one word.
		"__YOLO_HAS_REFRESH_DUE__", shquote.Quote(boolFlag(len(r.DueOnChange) > 0)),
		"__YOLO_REFRESH_DUE_ON_CHANGE__", shquote.Join(r.DueOnChange),
	}, onlyIfSplices(r.OnlyIf)...)
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

// probeDeclShell is a program's declared PROBE ARGUMENTS (packdecl.Install.Probe, XB-D24), baked
// into every launcher template's header, and the one test of them: _YOLO_PROBE is 1 when the
// launcher's first argument is one, which every update step below reads and skips on. At the
// header, where "$@" is still the launcher's own. HAS_PROBE gates the array for bash 3.2.
const probeDeclShell = `# The program's PROBE ARGUMENTS (pi-extension-store-builds.md XB-D24): an invocation whose first
# argument is one only asks the program about itself, so it runs no update step at all.
HAS_PROBE=__YOLO_HAS_PROBE__
PROBE_ARGS=(__YOLO_PROBE_ARGS__)
_YOLO_PROBE=0
if [ "$HAS_PROBE" = "1" ] && [ "$#" -gt 0 ]; then
    for _yolo_p in "${PROBE_ARGS[@]}"; do
        if [ "$1" = "$_yolo_p" ]; then _YOLO_PROBE=1; fi
    done
fi
`

// launcherStepSplices are the splices of every launcher template's update steps for inst: its
// pre-launch refresh (refreshSplices) and its probe arguments (probeDeclShell).
func launcherStepSplices(inst *packdecl.Install) []string {
	return append(refreshSplices(inst.Refresh),
		"__YOLO_HAS_PROBE__", shquote.Quote(boolFlag(len(inst.Probe) > 0)),
		"__YOLO_PROBE_ARGS__", shquote.Join(inst.Probe))
}
