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
// Its shape is what keeps /bin/sh from losing the interrupt. A test interrupts its stand-in within
// a few milliseconds of the start mark appearing. The stand-ins the nix-stop tests used to write
// for themselves set their trap, ran `touch` to mark their start, then slept in a loop of
// foreground `sleep 0.05`. Measured 2026-10-05 on Linux, with a bash 3.2.57 build (macOS's
// /bin/sh is bash 3.2) and bash 5.3:
//
//   - NOTHING RUNS IN THE FOREGROUND. While bash waits for a foreground child it holds a SIGINT
//     back and runs the trap once the child has exited, but it drops one that arrives after it
//     has reaped the child and before it has put the trap's handler back: in bash 3.2 until its
//     wait_for returns, in bash 5.3 for the rest of the reap. The stand-in then runs on until the
//     stop kills it. Of interrupts sent 0.2 to 0.5 ms after the old stand-in's mark, as its shell
//     reaped that `touch`, bash 3.2 dropped 1 to 4 percent; bash 5.3 dropped about 1 in 300 of
//     those sent within 2 ms of it; dash and busybox ash dropped none. Under bash 3.2 on a loaded
//     machine the old stand-in failed 3 of 900 internal/prune TestPrunesNixIsTracked subtests with
//     "the stop did not interrupt the nix" after the 10-second grace, as macOS CI did. Here each
//     wait is a background sleep that the `wait` builtin waits for, which POSIX has a trapped
//     signal interrupt at once.
//   - THE TRAP IS SET, AND THE START MARKED, ONCE THE FIRST WAIT HAS RETURNED. bash 3.2's `wait`
//     turns its interrupt on before it records where an interrupt returns to, so a trapped SIGINT
//     in between, in the shell's first wait, returns to a place never recorded: the shell dies of a
//     segmentation fault. A later wait in the loop returns to the place the one before it recorded,
//     which is the same. With that gap widened by 0.3 ms in the bash 3.2.57 build, 1393 of 12000
//     stand-ins that marked their start before their first wait crashed, and none of 12000 of
//     these.
//   - An interrupt that arrives between two waits is taken late, not lost: bash 3.2 runs the trap
//     once the next wait has ended, a tick later.
//
// Each sleep writes to /dev/null, so one that outlives the trap's exit holds none of the
// stand-in's output pipes open, and the test's Wait does not wait for it.
func UntilInterrupted(onInterrupt, started string) string {
	return InterruptibleFor(30*time.Second, onInterrupt, started) + "; exit 1"
}

// InterruptibleFor is UntilInterrupted for a program that, unless interrupted, ends on its own after
// about d, with status 0.
func InterruptibleFor(d time.Duration, onInterrupt, started string) string {
	return "i=0; t=0; while [ $i -le " + strconv.Itoa(int(d/(50*time.Millisecond))) + " ]; do " +
		"sleep $t >/dev/null 2>&1 & wait $!; " +
		"if [ $i -eq 0 ]; then trap " + shquote.Quote(onInterrupt+"; exit 130") + " INT; : >" + shquote.Quote(started) + "; t=0.05; fi; " +
		"i=$((i+1)); done"
}
