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
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	rc := stopJail(os.Stdout, os.Stderr, ws, detectListingRuntime(ws), stopExec, p)
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

// stopExec is realStopExec behind a var, so a test can drive runStop against a fake runtime.
var stopExec = realStopExec

// stopRuntimeInstalled reports whether a runtime's CLI is on PATH, behind a var for the tests.
var stopRuntimeInstalled = func(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
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
// rt is the runtime the config and YOLO_RUNTIME resolve. The one the jail's keeper recorded
// launching it on goes ahead of it (launchedRuntime, JL-D79): a jail launched with
// YOLO_RUNTIME=container in a workspace whose default is podman runs where podman cannot see it.
//
// p is the optional timing collector; a nil p makes every span a no-op, which
// is the everyday off state.
func stopJail(stdout, stderr io.Writer, ws, rt string,
	run func(argv []string) (string, bool, int), p *perf.Log) int {
	launched, known := launchedRuntime(ws)
	if known && launched != rt {
		if rt != "" {
			fmt.Fprintf(stdout, "This workspace's jail was launched on %s, so this stop asks %s, not %s.\n",
				launched, launched, rt)
		}
		rt = launched
	}
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
	running, ran, rc := probeJailRunning(rt, cname, run)
	sp.End()
	if !ran {
		fmt.Fprintf(stderr, "yolo stop: the %s runtime could not be run.\n", rt)
		return 1
	}
	if rt == "container" && rc != 0 { // parity: HonoredBy — `container ls` is AC's answer, and a failed one is "could not ask", where podman's failed inspect is "no such container"
		fmt.Fprintf(stderr, "yolo stop: `container ls` failed (rc %d), so whether this workspace's jail "+
			"(%s) is running is not known. `container ls` shows why; `container stop %s` stops the jail.\n",
			rc, cname, cname)
		return 1
	}
	// The keeper's log as it stands before the stop, so the teardown streamed below is the one this
	// stop causes (run.FinishStop).
	logFrom := keeperLogOffset(ws)
	if !running {
		// A jail whose container is gone and whose keeper is still ending it: the stop waits for that
		// teardown, as it waits for the one it causes, so the next launch finds it done.
		if keeperAlive(ws) {
			fmt.Fprintf(stdout, "This workspace's jail (%s) is already ending.\n", cname)
			return finishStop(stdout, stderr, ws, rt, logFrom, stopCapture(stderr))
		}
		fmt.Fprintf(stdout, "No jail running for this workspace (%s).\n", cname)
		if !known {
			noteOtherRuntimes(stdout, rt, cname)
		}
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

// probeJailRunning asks rt whether cname is running. ran is false when the runtime could not be run, and
// rc is its exit status. Podman answers a Go-template inspect, and a non-zero status there is "no
// such container". Apple Container's `container inspect` takes no --format, so the template read
// nothing there and every AC jail looked stopped (G11, docs/plans/setup-support-gaps.md); its
// `container ls` lists the running containers, the question the attach decision asks it too
// (probeRunningContainer), and a non-zero status there is "could not ask".
func probeJailRunning(rt, cname string, run func(argv []string) (string, bool, int)) (running, ran bool, rc int) {
	if rt == "container" { // parity: HonoredBy — `container ls` lists the running containers, the question podman's template inspect answers
		out, ran, rc := run([]string{"container", "ls"})
		_, live := runtime.ParseContainerLsLive(out)[cname]
		return ran && rc == 0 && live, ran, rc
	}
	state, ran, rc := run([]string{rt, "inspect", "--format", "{{.State.Running}}", cname})
	return ran && rc == 0 && strings.TrimSpace(state) == "true", ran, rc
}

// launchedRuntime is the container runtime the workspace's jail was launched on, from its keeper's
// start record (run.LaunchedRuntime); ok is false when no record names one of them.
func launchedRuntime(ws string) (string, bool) {
	rt, ok := run.LaunchedRuntime(ws)
	if !ok || !slices.Contains(paths.SupportedRuntimes, rt) {
		return "", false
	}
	return rt, true
}

// noteOtherRuntimes is a no-op stop that cannot know which runtime launched the jail it tracks: no
// keeper's start record names one, and a tracking file says a jail of this name was launched. Each
// other container runtime installed here is a place that jail can run, so it names the stop that
// asks there.
func noteOtherRuntimes(stdout io.Writer, rt, cname string) {
	if _, tracked := runtime.ReadContainerWorkspace(cname); !tracked {
		return
	}
	for _, other := range paths.SupportedRuntimes {
		if other != rt && stopRuntimeInstalled(other) {
			fmt.Fprintf(stdout, "If it was launched on %s, `YOLO_RUNTIME=%s yolo stop` stops it.\n", other, other)
		}
	}
}

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
