package entrypoint

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
    local rc=0 detach=0
    if command -v yolo >/dev/null 2>&1 &&
        YOLO_BYPASS_SHIMS=1 yolo internal ` + NoTerminalVerb + ` --timeout=1 --kill-after=1 -- true </dev/null >/dev/null 2>&1; then
        detach=1
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
    trap '_YOLO_INTERRUPTED=1' INT
    trap "trap '' INT; $cleanup; trap - TERM; kill -TERM \$\$" TERM
    trap "trap '' INT; $cleanup; trap - HUP; kill -HUP \$\$" HUP
    "$@" || rc=$?
    trap '' INT
    eval "$cleanup" || true
    trap - INT TERM HUP
    return "$rc"
}

# _say_not_updated WHAT RC is the one line an update act that did not succeed gets: interrupted,
# timed out, or failed with RC, then what happens next. A launch goes on with what is installed.
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
`
