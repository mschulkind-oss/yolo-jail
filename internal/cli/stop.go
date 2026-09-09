package cli

// stop.go is `yolo stop`: end this workspace's running jail deliberately.
//
// The ordinary lifecycle needs this rarely — a jail lives in the terminal that
// launched it, and exiting (or Ctrl-C-ing) that session tears the jail down
// with you (--rm sweeps the container). `yolo stop` is for the container with
// no live terminal left to own it: a launcher that died, a wedged session, a
// headless straggler. It is also the first half of the recommended replacement
// series — `yolo stop`, then an ordinary `yolo` launch — which is what every
// message that used to recommend the old `--new` flag now names instead
// (RELEASE-NOTES records why --new was removed: its one-command replacement
// hid the kill).

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

Rarely needed: a jail lives in the terminal that launched it, and exiting (or
Ctrl-C) that session tears the jail down with you. This is for the container
with no live terminal left to own it — a dead launcher, a wedged session, a
headless straggler — and it is the first half of the recommended replacement
series whenever a change cannot reach a running jail:

    yolo stop && yolo -- <cmd>

Stopping IS the end of that jail's sessions; the next launch starts fresh.
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
	if rc != 0 || strings.TrimSpace(state) != "true" {
		fmt.Fprintf(stdout, "No jail running for this workspace (%s).\n", cname)
		return 0
	}

	sp = p.Span("stop.stop_container")
	_, ran, rc = run([]string{rt, "stop", cname})
	sp.End()
	if !ran || rc != 0 {
		fmt.Fprintf(stderr, "yolo stop: stopping %s failed (rc %d).\n", cname, rc)
		return 1
	}
	fmt.Fprintf(stdout, "Stopped %s. The next yolo launch starts fresh.\n", cname)
	return 0
}
