package testsupport

import (
	"strconv"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// UntilInterrupted is a POSIX sh program standing in for a long-running nix that a test stops
// (internal/nixchildren's Stop, which interrupts the nix's PID alone and kills it once a grace has
// passed). Interrupted, it runs onInterrupt and exits 130. It creates the file started once its
// trap is set, so an interrupt a test sends after started exists is one the trap acts on. It gives
// up after 30 seconds with status 1, so a red run whose interrupt never comes does not leave it
// running after the test binary exits.
//
// onInterrupt is shell source run in the trap; quote any path in it with shquote.Quote. The trap
// body is quoted as a whole here.
//
// Its shape keeps the shell that owns the trap in the `wait` builtin, waiting for one background
// `sleep 0.05` at a time. A test interrupts that shell's PID within a few milliseconds of the start
// mark appearing. The stand-ins the nix-stop tests used to write for themselves set their trap, ran
// `touch` to mark their start, then slept in a loop of foreground `sleep 0.05`. Measured 2026-10-05
// on Linux, with a bash 3.2.57 build (macOS's /bin/sh is bash 3.2) and bash 5.3, except where a
// bullet gives another date:
//
//   - NOTHING RUNS IN THE FOREGROUND. While bash waits for a foreground child it holds a SIGINT
//     back and runs the trap once the child has exited, but it drops one that arrives after it
//     has reaped the child and before it has put the trap's handler back: in bash 3.2 until its
//     wait_for returns, in bash 5.3 for the rest of the reap. The stand-in then runs on until the
//     stop kills it. Of interrupts sent 0.2 to 0.5 ms after the old stand-in's mark, as its shell
//     reaped that `touch`, bash 3.2 dropped 1 to 4 percent; bash 5.3 dropped about 1 in 300 of
//     those sent within 2 ms of it; dash and busybox ash dropped none. Under bash 3.2 on a loaded
//     machine the old stand-in failed 3 of 900 internal/prune TestPrunesNixIsTracked subtests with
//     "the stop did not interrupt the nix" after the 10-second grace, as macOS CI did. The `wait`
//     builtin does not drop one: while bash 3.2 waits there, its SIGINT handler sees that builtin
//     and returns from it to run the trap (wait_sigint_handler in its jobs.c).
//   - NO WAIT OUTLASTS ONE SHORT SLEEP. bash 3.2 runs a pending trap only between commands, or
//     from inside `wait` once that builtin has turned its interrupt on, its first statement
//     (trap_handler in trap.c, wait_builtin in builtins/wait.def). A SIGINT that lands after the
//     shell's last check before a `wait` and before that statement, while it expands the wait's
//     words and looks up the builtin, is recorded and nothing more: the wait then blocks until its
//     child exits, and only then does the trap run. The helper this replaced marked its start and
//     then waited once, for a background loop that ran the whole 30 seconds, so an interrupt sent
//     as the shell moved from its mark into that wait went unanswered for 30 seconds. macOS CI
//     failed TestUntilInterruptedRunsItsTrapOnEveryInterrupt that way twice on 2026-10-30, each
//     time on an interrupt sent at the mark, and on 2026-10-30 a stock bash 3.2.57 build failed it
//     the same way in each of three runs on Linux. Here each wait's child is one 0.05-second
//     sleep, so such an interrupt runs the trap once that sleep ends. dash 0.5.12 has no such
//     window: its `wait` checks for a caught signal with every signal blocked and then suspends
//     with them unblocked (waitproc in its jobs.c), so a signal that came first ends the wait at
//     once.
//   - THE TRAP IS SET, AND THE START MARKED, ONCE THE FIRST WAIT HAS RETURNED. bash 3.2's `wait`
//     turns its interrupt on before it records where an interrupt returns to, so a trapped SIGINT
//     in between, in the shell's first wait, returns to a place never recorded: the shell dies of a
//     segmentation fault. A later wait returns to the place the one before it recorded, which is the
//     same, because every wait here is the one command in the loop: its first pass waits for a
//     `sleep 0` with no trap set. With that gap widened by 0.3 ms in the bash 3.2.57 build, 1393 of
//     12000 stand-ins that marked their start before their first wait crashed, and none of 12000
//     that marked it after (measured on the helper this replaced, whose first and later waits were
//     two commands at the same depth).
//
// Each sleep redirects its output to /dev/null, so one still running after the trap exits holds
// none of the stand-in's output pipes open; the trap also stops it.
func UntilInterrupted(onInterrupt, started string) string {
	return InterruptibleFor(30*time.Second, onInterrupt, started) + "; exit 1"
}

// InterruptibleFor is UntilInterrupted for a program that, unless interrupted, ends on its own after
// about d, with status 0.
func InterruptibleFor(d time.Duration, onInterrupt, started string) string {
	intervals := strconv.Itoa(int(d / (50 * time.Millisecond)))
	return "i=0; nap=0; child=; while [ $i -le " + intervals + " ]; do " +
		"sleep $nap >/dev/null 2>&1 & child=$!; wait $child; " +
		"if [ $i -eq 0 ]; then " +
		"trap " + shquote.Quote("if [ -n \"$child\" ]; then kill \"$child\" 2>/dev/null; fi; "+onInterrupt+"; exit 130") + " INT; " +
		"nap=0.05; : >" + shquote.Quote(started) + "; fi; " +
		"i=$((i+1)); done"
}
