package entrypoint

import (
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// updatebound.go is the launchers' UPDATE BOUND: how every UPDATE ACT — a pack's declared update
// verb and its pre-launch refresh — is run, in all three launcher templates (npm, native and the
// fork's source launcher), so that one cannot hang the command the user typed nor take the
// user's terminal (docs/design/program-delivery.md §3.5, OQ-PD22). "Update act" and "update
// bound" are terms coined here for those two things; §3.5 states the bound's properties.
//
// # The hang it replaced
//
// Measured 2026-10-01: `claude` sat on "Updating claude..." for over five minutes, and a Ctrl-C
// did nothing. The update ran under GNU timeout(1), which runs its command in a process group of
// its own on the user's terminal. `claude install` switches that terminal to raw mode at once,
// which the kernel answers, for a process outside the terminal's foreground group, by stopping it
// with SIGTTOU: about 20 ms in, before it did any work. At 60 s timeout sent SIGTERM; claude's
// handler restores the terminal, so it was usually stopped again inside the handler, and timeout,
// given no -k, then had nothing left to send and waited on a process nothing would continue. A
// Ctrl-C went to the terminal's foreground group, the launcher's, and so reached nothing that
// could act on it.
//
// # What replaced it
//
// The act runs through `yolo internal no-terminal` with its bound (internal/notty): no controlling
// terminal and a /dev/null stdin, so no terminal call can stop it (PS-D1's detach, which the
// vendor installers already had); SIGTERM at UPDATE_TIMEOUT and SIGKILL UPDATE_GRACE later; and
// the terminal's Ctrl-C forwarded to it, with the same grace. GNU timeout -k is only the fallback
// for a launcher no yolo with that verb can serve, and it then runs in the terminal's own process
// group (--foreground), which keeps both the Ctrl-C and the terminal working.
//
// A Ctrl-C ends the ACT, not the launcher (_shielded): the user typed the program's name, and the
// installed version is there to run. That is the opposite of a Ctrl-C at a COLD install, which
// still ends the launcher (PS-D7), because there is nothing to run.

// updateBoundShellFn is spliced into all three templates once, ahead of every caller. It reads the
// template's own BIN and UPDATE_TIMEOUT. UPDATE_GRACE is spelled here, once, rather than in each
// template's header beside UPDATE_TIMEOUT, because nothing but this fragment reads it.
const updateBoundShellFn = `
# --- the update bound (program-delivery.md §3.5) ------------------------------------------
# How long an update act may outlive its SIGTERM (sent at UPDATE_TIMEOUT) or a Ctrl-C before it is
# killed. A program stopped, or stuck in its own SIGTERM handler, never exits by itself.
UPDATE_GRACE=5
# 1 once a Ctrl-C reached this launcher during the current update act; see _shielded.
_YOLO_INTERRUPTED=0

# _bounded runs one UPDATE ACT so that it can neither hang the command the user typed nor take
# their terminal, through yolo internal ` + NoTerminalVerb + ` (internal/notty):
#   DETACHED       no controlling terminal and a /dev/null stdin, so a terminal call cannot stop it
#                  with SIGTTOU (timeout(1) running it in a background group of the terminal is
#                  how "claude install" hung) and a prompt cannot wait on the user. Its output
#                  still reaches the terminal.
#   BOUNDED        SIGTERM at UPDATE_TIMEOUT, SIGKILL UPDATE_GRACE seconds after to what is left
#                  of its process group, even once the program itself has exited; status 124.
#   INTERRUPTIBLE  the terminal's Ctrl-C is forwarded to it, and it is killed UPDATE_GRACE seconds
#                  later if it ignores it. What the launcher does next is _shielded's.
# With no yolo that has the bounded verb (none on PATH, or one older than this launcher), GNU
# timeout -k bounds it IN the terminal's foreground group (--foreground), where a Ctrl-C reaches
# it and no SIGTTOU can; with no timeout(1) either it runs unbounded. Each fallback says so.
_bounded() {
    local rc=0 detach=0 probe=0
    if command -v yolo >/dev/null 2>&1; then
        YOLO_BYPASS_SHIMS=1 yolo internal ` + NoTerminalVerb + ` --timeout=1 --kill-after=1 -- true </dev/null >/dev/null 2>&1 || probe=$?
        if [ "$probe" = 0 ]; then detach=1; fi
        _probe_interrupted "$probe"
    fi
    # A Ctrl-C during that probe kills the probe, which then reads as "no yolo with the verb": the
    # act would start in a fallback, on the terminal, AFTER the user asked for it to stop. It ends
    # the act before it starts instead.
    if [ "$_YOLO_INTERRUPTED" = 1 ]; then return 130; fi
    if [ "$detach" = 1 ]; then
        YOLO_BYPASS_SHIMS=1 yolo internal ` + NoTerminalVerb + ` --timeout="$UPDATE_TIMEOUT" --kill-after="$UPDATE_GRACE" -- "$@" </dev/null || rc=$?
    elif command -v timeout >/dev/null 2>&1; then
        echo "  (yolo cannot detach $BIN's update from this terminal here; it runs with no stdin, under timeout)" >&2
        YOLO_BYPASS_SHIMS=1 timeout --foreground -k "$UPDATE_GRACE" "$UPDATE_TIMEOUT" "$@" </dev/null || rc=$?
        # --foreground kills only the command, so a command that outlived its SIGTERM leaves
        # timeout(1) the KILL's status, 137, rather than 124; the launcher's word for both is one.
        if [ "$rc" = 137 ]; then rc=124; fi
    else
        echo "  (yolo cannot detach or bound $BIN's update here; it runs with no stdin and no time limit)" >&2
        YOLO_BYPASS_SHIMS=1 "$@" </dev/null || rc=$?
    fi
    # A Ctrl-C ends the act whatever status the program then chose: claude install exits 0 on
    # one (measured 2026-10-01), and an interrupted update is not a successful one. Under
    # _shielded the trap has run by now, since bash runs it once the command it waited on returns.
    if [ "$rc" = 0 ] && [ "$_YOLO_INTERRUPTED" = 1 ]; then rc=130; fi
    return "$rc"
}

# _probe_interrupted RC acts on a Ctrl-C that ended a probe for yolo's ` + NoTerminalVerb + ` verb (here
# and in the native launcher's _run_without_terminal). RC is the probe's status, and 130 (128 +
# SIGINT) means the interrupt ended it: the probe runs "true", and a yolo without the verb exits 2.
# The launcher cannot wait for its own SIGINT instead. macOS signals a process group's members one
# at a time, newest first (XNU's pgrp_iterate), so the launcher, the group's oldest member, is
# signaled last, and on a busy machine the probe has died of its SIGINT and been reaped by then.
# bash then runs the trap after the check that follows the probe, or never: bash 3.2, macOS's
# /bin/bash, drops a SIGINT landing as it finishes waiting for a child it has reaped. Seen on
# check-macos on 2026-10-06: the update then ran unbounded, after the Ctrl-C. So the probe's
# status counts as the Ctrl-C, and the launcher sends itself the SIGINT, doing what the Ctrl-C
# would have made it do there: under _shielded the trap sets _YOLO_INTERRUPTED before the next
# command, the check; at a cold install, with no trap, it ends the launcher (PS-D7). A late SIGINT
# of the launcher's own is then one more of the same.
_probe_interrupted() {
    if [ "$1" = 130 ]; then kill -INT $$; fi
}

# _shielded CLEANUP CMD... runs CMD as one update act the user may interrupt, and then CLEANUP, which
# releases what the act held (its lock). A Ctrl-C (SIGINT) ends the act and NOT this launcher: it
# sets _YOLO_INTERRUPTED, and the launcher goes on to run what is installed, because the user typed
# the program's name, not "update". A SIGTERM or SIGHUP (a closed terminal, a stopped jail) still
# ends the launcher, by that same signal, but runs CLEANUP first, so the lock the act holds does
# not outlive it and refuse the next ten minutes of updates. bash runs a trap only once the command
# it waits on has returned, which _bounded keeps short. CMD's status is the return value.
#
# THE RELEASE IS INSIDE THE SHIELD, with Ctrl-C IGNORED by this shell and so by what it runs (the
# rmdir, the cat of a token): measured 2026-10-01, a Ctrl-C typed twice at claude's update had the
# second land after the shield came down and before the rmdir, and it killed the launcher with the
# lock held. So the caller releases nothing itself, and nothing can end the launcher between the
# act and the release.
_shielded() {
    local cleanup="$1" rc=0
    shift
    _YOLO_INTERRUPTED=0
    # _YOLO_SIGNALLED is 1 only inside the TERM and HUP traps: a cleanup that must tell a signal
    # from a normal end (the npm launcher's check claim, released on a signal alone) reads it.
    _YOLO_SIGNALLED=0
    trap '_YOLO_INTERRUPTED=1' INT
    trap "trap '' INT; _YOLO_SIGNALLED=1; $cleanup; trap - TERM; kill -TERM \$\$" TERM
    trap "trap '' INT; _YOLO_SIGNALLED=1; $cleanup; trap - HUP; kill -HUP \$\$" HUP
    "$@" || rc=$?
    trap '' INT
    eval "$cleanup" || true
    trap - INT TERM HUP
    return "$rc"
}

# _say_not_updated WHAT RC is the one line an update act that did not succeed gets: interrupted,
# timed out, or failed with RC, then what happens next. A launch reaches it only for a Ctrl-C
# (PD-NF4), and goes on with what is installed; its other failures are _yolo_failed_act's.
# Update mode ("yolo pack update", YOLO_PACK_UPDATE=1) runs nothing, so it says the installed
# version stays and how to retry, where it said it was running it.
_say_not_updated() {
    local next="running the installed version."
    if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then
        next="the installed version stays. Run 'yolo pack update' again to retry."
    fi
    if [ "$_YOLO_INTERRUPTED" = 1 ]; then
        echo "  ⚠ $BIN: $1 interrupted (Ctrl-C) — $next" >&2
    elif [ "$2" = 124 ]; then
        echo "  ⚠ $BIN: $1 timed out after ${UPDATE_TIMEOUT}s — $next" >&2
    else
        echo "  ⚠ $BIN: $1 failed (status $2) — $next" >&2
    fi
}

# --- no quiet fallback to an older version (program-delivery.md PD-NF1, PD-NF2) --------------
# When yolo found a newer version of this program and installing it failed, the command STOPS
# and lists the hatches; it never quietly runs the older installed version. Not a failure: an
# upstream that could not be reached at all (nothing says a newer version exists), a Ctrl-C at
# the act (PD-NF4), a busy update lock (PD-NF5), and an agent_updates hold.
#
# UPDATE_REFUSED is the status of that stop: 75, sysexits' EX_TEMPFAIL ("try again later"),
# distinct from 1 (nothing to run), 124 (a timeout) and 128+N (a signal), so a caller can tell "an
# update failed and nothing ran" from the program's own status (UpdateRefusedStatus).
UPDATE_REFUSED=__YOLO_UPDATE_REFUSED__
# How long a probe of the program's upstream may take, in seconds (PD-NF3).
PROBE_TIMEOUT=10
# The declaring pack's name, which the hatch's value may name as well as the bin.
_YOLO_HATCH_PACK=__YOLO_HATCH_PACK__
# The refusal's "Start anyway:" block, rendered in Go (patchrecovery.Section) at generation, so the
# refusal needs no yolo on PATH. Its label names "the installed version"; a refusal that knows the
# version puts it there.
_YOLO_HATCH_SECTION=__YOLO_HATCH_SECTION__
# What an update act that failed left for its call site to refuse with, outside the act's lock and
# shield: the ERROR line's text, the fact under it, and the installed version when it is known.
# Empty when the act succeeded, could not check, or was interrupted.
_YOLO_FAIL_HEAD=""
_YOLO_FAIL_FACT=""
_YOLO_FAIL_VER=""

# _yolo_use_installed: 0 when the user's ` + patchrecovery.UseInstalledEnv + ` covers this program —
# "1", or a comma-separated name equal to its bin or its pack (patchrecovery.Parse's grammar). Read
# from the RUNTIME environment, never baked: the hatch is the command's, typed in front of it here or
# in front of "yolo" on the host, which forwards it (container -e, the macos-user session env file).
_yolo_use_installed() {
    local v="${` + patchrecovery.UseInstalledEnv + `:-}"
    v="${v//[[:space:]]/}"
    [ -n "$v" ] || return 1
    [ "$v" = 1 ] && return 0
    case ",$v," in *",$BIN,"*) return 0 ;; esac
    if [ -n "$_YOLO_HATCH_PACK" ]; then
        case ",$v," in *",$_YOLO_HATCH_PACK,"*) return 0 ;; esac
    fi
    return 1
}

# _yolo_registry_reachable probes the npm registry this jail's npm is configured for, bounded by
# PROBE_TIMEOUT: 1 when it could not be reached, 0 when it answered or there is no npm here to ask
# with (no probe target known, which PD-NF3 treats as reachable). "npm ping", not "npm view" of a
# package: a package the registry does not have is a registry that answered.
_yolo_registry_reachable() {
    command -v npm >/dev/null 2>&1 || return 0
    YOLO_BYPASS_SHIMS=1 npm ping --fetch-retries=0 --fetch-timeout="$((PROBE_TIMEOUT * 1000))" </dev/null >/dev/null 2>&1
}

# _yolo_url_reachable URL probes an installer's host, bounded by PROBE_TIMEOUT, with one HEAD whose
# status it reads. The host floor's probe classifies the same way (hostfloor.probeUpstreamHEAD): no
# answer (curl's 000: a connection error or a timeout) or a server error (5xx) is unreachable, 1; any
# other HTTP status, a 404 or a 405 for HEAD included, is an upstream that answered, 0. 0 with no URL
# or no curl: no probe target known.
_yolo_url_reachable() {
    [ -n "${1:-}" ] || return 0
    command -v curl >/dev/null 2>&1 || return 0
    local code
    code=$(YOLO_BYPASS_SHIMS=1 curl -sS -o /dev/null -I -w '%{http_code}' --max-time "$PROBE_TIMEOUT" "$1" </dev/null 2>/dev/null) || true
    case "$code" in
    "" | 000 | 5??) return 1 ;;
    esac
    return 0
}

# _yolo_failed_act RC WHAT PROBE... classifies an update act that has NO version query and failed
# with RC (PD-NF3): a vendor's update verb, an installer re-run, a pre-launch refresh. Such an act
# cannot say whether upstream had a newer version, so its upstream is probed (PROBE..., a command)
# AFTER the failure: unreachable is "could not check" and the installed version runs, said in one
# line; reachable, or no probe known, leaves _YOLO_FAIL_HEAD for the call site to refuse with. After
# the failure rather than before every act: an act that succeeds costs no probe, and a failed act
# whose upstream answers is the case the ruling stops on either way.
_yolo_failed_act() {
    local rc="$1" what="$2" reached=0 how
    shift 2
    "$@" || reached=$?
    if [ "$_YOLO_INTERRUPTED" = 1 ]; then
        echo "  ⚠ $BIN: $what interrupted (Ctrl-C) — running what is installed." >&2
        return 0
    fi
    how="failed (status $rc)"
    if [ "$rc" = 124 ]; then how="timed out after ${UPDATE_TIMEOUT}s"; fi
    if [ "$reached" != 0 ]; then
        echo "  $BIN: $what $how, and its upstream could not be reached to check for a newer version — running what is installed. It asks again the next time this jail starts (yolo stop on the host, then launch), or now with: yolo pack update." >&2
        return 0
    fi
    _YOLO_FAIL_HEAD="$BIN: $what $how"
    _YOLO_FAIL_FACT="Its upstream answered, so a newer version may be waiting; the update's output is above."
}

# _yolo_update_refused STAMP HEAD FACT [VERSION] is the refusal: the ERROR line, the fact, Repair:,
# then Start anyway: with the hatch, and exit UPDATE_REFUSED without running the program. STAMP (may
# be empty) is the act's throttle stamp, removed so the next invocation retries the act rather than
# quietly running what is installed for an hour. When the hatch covers this program it says one line
# instead and returns, the stamp kept: the user chose the installed version for this throttle period.
_yolo_update_refused() {
    local stamp="$1" head="$2" fact="$3" ver="${4:-}" s="$_YOLO_HATCH_SECTION"
    if _yolo_use_installed; then
        echo "  ⚠ $head — running the installed version${ver:+ $ver}, as ` + patchrecovery.UseInstalledEnv + ` asks." >&2
        return 0
    fi
    if [ -n "$stamp" ]; then rm -f "$stamp" 2>/dev/null || true; fi
    if [ -n "$ver" ]; then s="${s//the installed version/the installed $ver}"; fi
    {
        echo "yolo: ERROR: $head"
        echo "  $fact"
        echo ""
        echo "Repair:"
        echo "  fix what stopped it (its output is above), then run $BIN again, which retries the update:"
        echo "    $BIN"
        printf '%s' "$s"
    } >&2
    exit "$UPDATE_REFUSED"
}

# _yolo_refuse_failed STAMP refuses with what a failed act left, if it left anything. What it left is
# consumed here: when the hatch runs past the refusal, a later act in the same run must not find this
# act's failure and say it again as its own.
_yolo_refuse_failed() {
    [ -n "$_YOLO_FAIL_HEAD" ] || return 0
    local head="$_YOLO_FAIL_HEAD" fact="$_YOLO_FAIL_FACT" ver="$_YOLO_FAIL_VER"
    _YOLO_FAIL_HEAD="" _YOLO_FAIL_FACT="" _YOLO_FAIL_VER=""
    _yolo_update_refused "$1" "$head" "$fact" "$ver"
}
`

// UpdateRefusedStatus is the status a generated launcher exits with when it refuses to run the
// installed version of a program whose newer version failed to install (docs/design/program-delivery.md
// PD-NF1): EX_TEMPFAIL, distinct from the launchers' 1, 124 and 128+N. `yolo internal refresh-servers`
// exits with it for an MCP server's refusal, and the launchers pass it through.
const UpdateRefusedStatus = 75

// useInstalledVersion is the words of a hatch label that name what runs; the shell and
// WithInstalledVersion put the installed version in place of "version" when they know it.
const useInstalledVersion = "the installed version"

// UseInstalledHatches is the refusal's hatch list: value (the program's bin, or the refused
// packages) in front of bin, typed in this jail, and in front of `yolo -- bin`, typed on the host,
// where a bare bin would run the host's own binary (PD-NF2). With no bin there is only one form.
func UseInstalledHatches(value, bin string) []patchrecovery.Hatch {
	if bin == "" {
		return []patchrecovery.Hatch{{Env: patchrecovery.UseInstalledEnv, Value: value, Command: "yolo",
			Label: "run " + useInstalledVersion}}
	}
	q := shquote.Quote(bin)
	return []patchrecovery.Hatch{
		{Env: patchrecovery.UseInstalledEnv, Value: value, Command: q, Label: "in this jail: run " + useInstalledVersion},
		{Env: patchrecovery.UseInstalledEnv, Value: value, Command: "yolo -- " + q, Label: "from the host: run " + useInstalledVersion},
	}
}

// WithInstalledVersion names version in every hatch label of hs, in place of "version".
func WithInstalledVersion(hs []patchrecovery.Hatch, version string) []patchrecovery.Hatch {
	for i := range hs {
		hs[i].Label = strings.Replace(hs[i].Label, useInstalledVersion, "the installed "+version, 1)
	}
	return hs
}

// useInstalledSplices renders the refusal's baked values for one launcher: the pack its hatch may
// name, the "Start anyway:" block (no color: a launcher cannot ask the terminal), and the status.
func useInstalledSplices(bin, pack string) []string {
	return []string{
		"__YOLO_HATCH_PACK__", shquote.Quote(pack),
		"__YOLO_HATCH_SECTION__", shquote.Quote(patchrecovery.Section(patchrecovery.StartAnyway, UseInstalledHatches(bin, bin), false)),
		"__YOLO_UPDATE_REFUSED__", strconv.Itoa(UpdateRefusedStatus),
	}
}
