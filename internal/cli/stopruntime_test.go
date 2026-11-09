package cli

// stopruntime_test.go pins that `yolo stop` asks the runtime its jail runs on, and asks it in that
// runtime's own terms (docs/design/jail-lifetime-last-session-wins.md JL-D79). Apple Container's
// `container inspect` takes no --format, so the podman template every runtime used to get read
// nothing there and the stop said "No jail running" while the jail ran (G11,
// docs/plans/setup-support-gaps.md). And a jail launched with YOLO_RUNTIME=container in a
// workspace whose default is podman runs where podman cannot see it.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// acStop is a fake host with Apple Container's CLI, and podman's when podman is set, holding one
// running jail, cname, on Apple Container. It answers as each runtime does: AC's inspect refuses a
// Go template, its `ls` lists the running containers as a table, and podman knows no such container.
type acStop struct {
	t       *testing.T
	cname   string
	podman  bool
	stopped bool
	lsRC    int
	// stopRC is `container stop`'s exit status, 0 unless a test makes the stop fail.
	stopRC int
	// lsAfterStop, when set, answers `container ls` once the stop has run: ask is 1 on the first
	// listing ask since the stop. It stands in for Apple Container, whose `container ls` keeps
	// listing a jail for a moment after its stop returns (JL-D83) and whose API server can fail an
	// ask while it ends one.
	lsAfterStop func(ask int) (out string, rc int)
	lsAsks      int
	calls       [][]string
}

func (f *acStop) run(argv []string, _ time.Duration) (string, bool, int) {
	f.calls = append(f.calls, argv)
	switch {
	case argv[0] == "podman" && f.podman && len(argv) > 1 && argv[1] == "inspect":
		return "", true, 125 // Error: no such object
	case argv[0] != "container":
		return "", false, 1
	case len(argv) > 1 && argv[1] == "inspect":
		if slices.Contains(argv, "--format") {
			return "", true, 64 // Error: Unknown option '--format'
		}
		state := "running"
		if f.stopped {
			state = "stopped"
		}
		return `[{"id":"` + f.cname + `","status":{"state":"` + state + `"}}]`, true, 0
	case len(argv) == 2 && argv[1] == "ls":
		if f.stopped && f.lsAfterStop != nil {
			f.lsAsks++
			out, rc := f.lsAfterStop(f.lsAsks)
			return out, true, rc
		}
		if f.lsRC != 0 {
			return "", true, f.lsRC
		}
		out := "ID IMAGE OS ARCH STATE ADDR\n"
		if !f.stopped {
			out += f.cname + " localhost/yolo-jail:abc linux arm64 running 192.168.64.3/24\n"
		}
		return out, true, 0
	case len(argv) == 3 && argv[1] == "ls" && argv[2] == "--all":
		// The existence question `container ls` (running only) cannot answer: a stopped jail is
		// here, and it is what would block the next launch by name.
		out := "ID IMAGE OS ARCH STATE ADDR\n"
		if !f.stopped {
			out += f.cname + " localhost/yolo-jail:abc linux arm64 running 192.168.64.3/24\n"
		}
		return out, true, 0
	case len(argv) == 3 && argv[1] == "stop" && argv[2] == f.cname:
		f.stopped = true
		return f.cname + "\n", true, f.stopRC
	}
	f.t.Errorf("the stop ran %v, which this fake does not answer", argv)
	return "", true, 1
}

func (f *acStop) stoppedOn() []string {
	var rts []string
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == "stop" {
			rts = append(rts, c[0])
		}
	}
	return rts
}

// stopWorkspace makes a workspace whose launch selects setupName (YOLO_SETUP), enters it, and stubs the keeper's teardown;
// it returns the jail's name and the runtimes the teardown was handed.
func stopWorkspace(t *testing.T, setupName string) (cname string, finished *[]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "")
	t.Setenv("YOLO_SETUP", setupName)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ws)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	finishStop = func(_, _ io.Writer, _, _, rt string, _ int64, _ func(string, string, string), _ bool) int {
		got = append(got, rt)
		return 0
	}
	return runtime.JailName(cwd, setupName), &got
}

func useStopExec(t *testing.T, f stopCommandRunner) {
	saved := stopExec
	t.Cleanup(func() { stopExec = saved })
	stopExec = f
}

// TestAStopEndsAnAppleContainerJailItsSetupNames is the call-site pin: `yolo stop` under
// YOLO_SETUP=apple-container-self finds the running jail and stops it on Apple Container.
func TestAStopEndsAnAppleContainerJailItsSetupNames(t *testing.T) {
	cname, finished := stopWorkspace(t, "apple-container-self")
	f := &acStop{t: t, cname: cname}
	useStopExec(t, f.run)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if got := f.stoppedOn(); !slices.Equal(got, []string{"container"}) || !f.stopped {
		t.Errorf("the stop ran a stop on %v, want one `container stop %s`; calls %v\nstdout:\n%s",
			got, cname, f.calls, stdout)
	}
	if !strings.Contains(stdout, "Stopped "+cname) || strings.Contains(stdout, "No jail running") {
		t.Errorf("the stop must say it stopped the running jail:\n%s", stdout)
	}
	if !slices.Equal(*finished, []string{"container"}) {
		t.Errorf("the keeper's teardown was handed %v, want Apple Container", *finished)
	}
}

// TestAStopAsksTheRuntimeItsJailWasLaunchedOn: a workspace whose default is podman, its jail
// launched with YOLO_RUNTIME=container. The keeper's start record names the runtime it launched
// on, and the stop asks that one, saying so, rather than reporting nothing running on podman.
func TestAStopAsksTheRuntimeItsJailWasLaunchedOn(t *testing.T) {
	cname, finished := stopWorkspace(t, "podman-self")
	record := filepath.Join(paths.GlobalStorage(), "owners", cname+".keeper.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"pid":4242,"runtime":"container"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &acStop{t: t, cname: cname, podman: true}
	useStopExec(t, f.run)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if got := f.stoppedOn(); !slices.Equal(got, []string{"container"}) || !f.stopped {
		t.Errorf("the stop ran a stop on %v, want one `container stop %s`; calls %v\nstdout:\n%s",
			got, cname, f.calls, stdout)
	}
	if !strings.Contains(stdout, "launched on container") {
		t.Errorf("the stop must say it asks the runtime the jail was launched on:\n%s", stdout)
	}
	if !slices.Equal(*finished, []string{"container"}) {
		t.Errorf("the keeper's teardown was handed %v, want Apple Container", *finished)
	}
}

// TestAStopThatCannotListAppleContainerFails: `container ls` failing is "could not ask", never
// "nothing running", so the stop fails and names the commands that see and end the jail.
func TestAStopThatCannotListAppleContainerFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	f := &acStop{t: t, cname: cname, lsRC: 1}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "container", f.run, nil, false); rc != 1 {
		t.Fatalf("rc=%d, want 1\nstdout:\n%s", rc, out.String())
	}
	if strings.Contains(out.String(), "No jail running") {
		t.Errorf("a runtime that could not answer was read as nothing running:\n%s", out.String())
	}
	for _, want := range []string{"container ls", "container stop " + cname} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the failure must name %q:\n%s", want, errb.String())
		}
	}
}

// TestANoOpStopOnAppleContainerIsSuccess: `container ls` answering without the jail is nothing
// running, and the stop is idempotent there as on podman: it says so, succeeds, and stops nothing.
// A probe that read any answer from `container ls` as running would run `container stop` on a jail
// that is not there, and fail the first half of `yolo stop && yolo`.
func TestANoOpStopOnAppleContainerIsSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	f := &acStop{t: t, cname: cname, stopped: true}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "container", f.run, nil, false); rc != 0 {
		t.Fatalf("rc=%d, want 0\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if got := f.stoppedOn(); len(got) != 0 {
		t.Errorf("nothing to stop must issue no stop command, ran one on %v; calls %v", got, f.calls)
	}
	if !strings.Contains(out.String(), "No jail running") {
		t.Errorf("the no-op must say so:\n%s", out.String())
	}
}

// TestAStopThatFailsNamesItsNextStep: each way the stop fails ends on the command that moves it on
// (the happy path principle), now that Apple Container and a start record's runtime reach both.
func TestAStopThatFailsNamesItsNextStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name  string
		stats []string
		want  []string
	}{
		{"the runtime could not be run", []string{"-"}, []string{"`podman` is not on PATH", "run `yolo stop` again"}},
		{"the runtime's stop failed and the probe could not answer", []string{"true\n", "!stopped with an error"},
			[]string{"`podman stop " + runtime.FromWorkspace("/ws") + "` failed", "`yolo stop` again retries it"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			s := &stopRun{stats: tc.stats}
			if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", s.run, nil, false); rc != 1 {
				t.Fatalf("rc=%d, want 1", rc)
			}
			for _, want := range tc.want {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("the failure must say %q:\n%s", want, errb.String())
				}
			}
		})
	}
}

// TestAStopWhoseNonZeroExitLeavesNoContainerStillEndsTheJail is JL-D82 at `yolo stop`'s door:
// podman's stop of a jail whose --rm removal races its own SIGKILL exits non-zero (measured: 125)
// with the container already gone. Judged on the exit status alone the stop reported a failure and
// skipped the keeper's teardown; judged by the existence probe it is a jail that ended.
func TestAStopWhoseNonZeroExitLeavesNoContainerStillEndsTheJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	finished := false
	finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), _ bool) int {
		finished = true
		return 0
	}
	probes := 0
	run := func(argv []string, _ time.Duration) (string, bool, int) {
		switch {
		case argv[1] == "inspect":
			probes++
			if probes == 1 {
				return "true\n", true, 0 // running, so the stop proceeds
			}
			return "", true, 125 // no such container: the stop left it gone
		case argv[1] == "container":
			return "", true, 1 // container exists: absent
		case argv[1] == "stop":
			return "", true, 125 // podman's SIGKILL/--rm race
		}
		return "", false, 1
	}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", run, nil, false); rc != 0 {
		t.Fatalf("rc=%d, want 0\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if strings.Contains(errb.String(), "failed (rc") {
		t.Errorf("a jail the probe found gone was reported as a failed stop:\n%s", errb.String())
	}
	if !finished {
		t.Errorf("the stop did not reach the keeper's teardown once the container was gone:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Stopped "+cname) {
		t.Errorf("the stop did not say it stopped the jail:\n%s", out.String())
	}
}

// TestAStopWhoseContainerIsStillRunningFails: a non-zero stop the existence probe then finds the
// container still running is a real failure, reported with the command that retries it — the same
// next step every other stop failure names. The keeper's teardown is not run.
func TestAStopWhoseContainerIsStillRunningFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), _ bool) int {
		t.Error("the stop ran the keeper's teardown with the container still running")
		return 0
	}
	run := func(argv []string, _ time.Duration) (string, bool, int) {
		switch {
		case argv[1] == "inspect":
			return "true\n", true, 0 // it still runs
		case argv[1] == "stop":
			return "", true, 1
		}
		return "", false, 1
	}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", run, nil, false); rc != 1 {
		t.Fatalf("rc=%d, want 1", rc)
	}
	for _, want := range []string{"`podman stop " + cname + "` failed", "`yolo stop` again retries it"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the failure must say %q:\n%s", want, errb.String())
		}
	}
}

// TestAnAppleContainerStopThatCannotListAfterwardsFails is the Apple Container half of JL-D82's
// probe. `container stop` returns non-zero and the re-probe's `container ls` then fails (rc != 0):
// read as `yolo stop`'s probe read it — rc discarded, `ran` taken for the answer — the failed stop
// would look like a jail that ended, skipping the keeper's teardown to report success (JL-P3: an
// unanswerable listing is never proof the container is gone). It must report the possible failure.
func TestAnAppleContainerStopThatCannotListAfterwardsFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), _ bool) int {
		t.Error("the stop ran the keeper's teardown after a re-probe that could not answer")
		return 0
	}
	f := &acStop{t: t, cname: cname, stopRC: 1, lsAfterStop: func(int) (string, int) { return "", 1 }}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "container", f.run, nil, false); rc != 1 {
		t.Fatalf("rc=%d, want 1\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	for _, want := range []string{"`container stop " + cname + "` failed", "`yolo stop` again retries it"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the failure must say %q:\n%s", want, errb.String())
		}
	}
	if strings.Contains(out.String(), "Stopped "+cname) {
		t.Errorf("a re-probe that could not answer was read as the jail gone:\n%s", out.String())
	}
}

// TestAnAppleContainerStopWaitsOutTheListingBeforeCallingItGone: `container stop` returns
// non-zero with the jail actually ending, and `container ls` keeps listing it for a few beats after
// (JL-D83). A single immediate probe reads "still running" and reports a failed stop; the bounded
// wait asks again while the jail is listed, and the stop ends as success, streaming the keeper's
// teardown.
func TestAnAppleContainerStopWaitsOutTheListingBeforeCallingItGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	finished := false
	finishStop = func(_, _ io.Writer, _, _, rt string, _ int64, _ func(string, string, string), _ bool) int {
		finished = rt == "container"
		return 0
	}
	listing := "ID IMAGE OS ARCH STATE ADDR\n" + cname +
		" localhost/yolo-jail:abc linux arm64 running 192.168.64.3/24\n"
	asks := 0
	f := &acStop{t: t, cname: cname, stopRC: 1, lsAfterStop: func(ask int) (string, int) {
		asks = ask
		if ask <= 2 { // the jail Apple Container's listing still trails
			return listing, 0
		}
		return "ID IMAGE OS ARCH STATE ADDR\n", 0
	}}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "container", f.run, nil, false); rc != 0 {
		t.Fatalf("rc=%d, want 0\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if asks < 2 {
		t.Errorf("the re-probe asked `container ls` %d time(s) after the stop; want it asked again while the jail was listed", asks)
	}
	if !finished {
		t.Errorf("the stop did not reach the keeper's teardown once the listing caught up:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Stopped "+cname) {
		t.Errorf("the stop did not say it stopped the jail:\n%s", out.String())
	}
}
