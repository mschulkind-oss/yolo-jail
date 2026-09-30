package cli

// stop.go is `yolo stop`: end this workspace's running jail deliberately.
//
// The ordinary lifecycle needs this rarely — a jail lives while any session in it
// does, and the last one to leave tears it down (its keeper does, and --rm sweeps
// the container). `yolo stop` ends every session at once: a wedged session, a
// headless straggler, a jail whose keeper is gone. It is also the first half of the recommended replacement
// series — `yolo stop`, then an ordinary `yolo` launch — which is what every
// message that used to recommend the old `--new` flag now names instead
// (--new was removed in 0.9.0, recorded in CHANGELOG.md: its one-command
// replacement hid the kill).

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// stopUsage is what `yolo stop --help` prints.
const stopUsage = `Usage: yolo stop

Stop this workspace's running jail.

Rarely needed: a jail lives while any session in it does, and quitting the
last one tears it down. This ends every session at once — a wedged session, a
headless straggler, a jail whose keeper is gone — and it is the first half of
the recommended replacement series whenever a change cannot reach a running
jail:

    yolo stop && yolo -- <cmd>

Stopping IS the end of that jail's sessions; the next launch starts fresh. It
returns once the jail's teardown is done, and prints it.
Idempotent: with nothing running it says so and succeeds.

Flags:
  --help, -h    Show this help.

Examples:
  yolo stop                           # release this workspace's jail
  yolo stop && yolo -- claude         # the replacement series, in full`

// runStop runs `yolo stop`.
func runStop(args []string) int {
	if answerHelp("stop", args, os.Stdout) {
		return 0
	}
	ws, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo stop: resolving the workspace: %v\n", err)
		return 1
	}
	// The timing gate rides the env opt-ins only — stop has no --timing of its
	// own to grow, and the person running it is often already asking "why is
	// everything slow".
	p := run.TimingLogFor(ws, os.Getenv, os.Stderr)
	rc := stopJail(os.Stdout, os.Stderr, ws, detectListingRuntime(ws), realStopExec, p)
	// D12, stop's small case of it: recording and REPORTING are different
	// questions. The spans are already in <ws>/.yolo/host-perf.log; the table
	// prints only when the user asked for it on THIS invocation, which for stop
	// means the global --verbose / -v. An inherited YOLO_VERBOSE=1 (a shell
	// profile's "always on") records in silence — same rule the run pipeline's
	// timingReporting() states, same reason.
	if p != nil && explicitVerbose() {
		// Not dashed: TestUsageListsEveryParsedFlag reads `---`-prefixed
		// literals in handlers as flags, and a report header is not one.
		fmt.Fprintln(os.Stderr, "yolo stop timing:")
		p.Report(os.Stderr, time.Now())
	}
	return rc
}

// realStopExec runs one runtime command, capturing stdout for the probes that
// read it. Returns (stdout, ran, exit code).
func realStopExec(argv []string) (string, bool, int) {
	var out strings.Builder
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return out.String(), true, ee.ExitCode()
		}
		return "", false, 1
	}
	return out.String(), true, 0
}

// stopJail is stop's testable core. A RUNNING container is stopped gracefully
// (the runtime's own TERM-then-KILL discipline; the launcher's --rm removes
// the container as it exits, so the next launch is fresh by construction). A
// stopped or absent container is SUCCESS: stop is idempotent, and any leftover
// is the next launch's stale-removal job, not this command's.
//
// p is the optional timing collector; a nil p makes every span a no-op, which
// is the everyday off state.
func stopJail(stdout, stderr io.Writer, ws, rt string,
	run func(argv []string) (string, bool, int), p *perf.Log) int {
	if rt == "" {
		fmt.Fprintln(stderr, "yolo stop: no container runtime found (podman / container).")
		return 1
	}
	if rt == "macos-user" {
		fmt.Fprintln(stdout, "The macos-user backend has no persistent jail to stop — "+
			"every invocation is a fresh sandbox.")
		return 0
	}
	cname := runtime.FromWorkspace(ws)

	// Is anything actually running? `stop` on a non-existent container errors,
	// and an erroring stop would make the stop-then-launch series fail on its
	// first, idempotent half.
	sp := p.Span("stop.inspect")
	state, ran, rc := run([]string{rt, "inspect", "--format", "{{.State.Running}}", cname})
	sp.End()
	if !ran {
		fmt.Fprintf(stderr, "yolo stop: the %s runtime could not be run.\n", rt)
		return 1
	}
	// The keeper's log as it stands before the stop, so the teardown streamed below is the one this
	// stop causes (run.FinishStop).
	logFrom := keeperLogOffset(ws)
	if rc != 0 || strings.TrimSpace(state) != "true" {
		// A jail whose container is gone and whose keeper is still ending it: the stop waits for that
		// teardown, as it waits for the one it causes, so the next launch finds it done.
		if keeperAlive(ws) {
			fmt.Fprintf(stdout, "This workspace's jail (%s) is already ending.\n", cname)
			return finishStop(stdout, stderr, ws, rt, logFrom, stopCapture(stderr))
		}
		fmt.Fprintf(stdout, "No jail running for this workspace (%s).\n", cname)
		return 0
	}

	// Why, for every session the stop ends, recorded before it (recordYoloStop): each prints it
	// once its own session is cut short.
	recordYoloStop(cname)
	sp = p.Span("stop.stop_container")
	_, ran, rc = run([]string{rt, "stop", cname})
	sp.End()
	if !ran || rc != 0 {
		fmt.Fprintf(stderr, "yolo stop: stopping %s failed (rc %d).\n", cname, rc)
		return 1
	}
	// THE TEARDOWN, before the stop returns (docs/design/jail-lifetime-last-session-wins.md JL-D25):
	// the jail's keeper runs it and this streams it, and a jail whose keeper died gets it from here
	// (JL-D30). Returning at the runtime's stop would let the next launch, or a `yolo config diff`,
	// meet a teardown still running.
	sp = p.Span("stop.keeper_teardown")
	frc := finishStop(stdout, stderr, ws, rt, logFrom, stopCapture(stderr))
	sp.End()
	if frc != 0 {
		return frc
	}
	fmt.Fprintf(stdout, "Stopped %s. The next yolo launch starts fresh.\n", cname)
	return 0
}

// finishStop is run.FinishStop behind a var, so a test can pin that the stop reaches it.
var finishStop = run.FinishStop

// stopCapture is the E3 config capture `yolo stop` hands the reap of an unkept jail, as a launch is
// handed it (commands.go).
func stopCapture(stderr io.Writer) func(workspace, rt string) {
	return func(workspace, rt string) {
		captureOnTerminate(workspace, rt, func(msg string) { fmt.Fprintln(stderr, "Warning: "+msg) })
	}
}

// recordYoloStop records, for the sessions a stop of cname's jail ends, that `yolo stop` ended it
// (run.RecordJailStop). Outside stopJail because that function's runner parameter is named run.
func recordYoloStop(cname string) {
	run.RecordJailStop(cname, run.YoloStopReason(os.Getpid()))
}

// keeperLogOffset and keeperAlive are run.KeeperLogOffset and run.KeeperAlive, outside stopJail for
// recordYoloStop's reason.
func keeperLogOffset(ws string) int64 { return run.KeeperLogOffset(ws) }

func keeperAlive(ws string) bool { return run.KeeperAlive(ws) }
