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
	calls   [][]string
}

func (f *acStop) run(argv []string) (string, bool, int) {
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
		if f.lsRC != 0 {
			return "", true, f.lsRC
		}
		out := "ID IMAGE OS ARCH STATE ADDR\n"
		if !f.stopped {
			out += f.cname + " localhost/yolo-jail:abc linux arm64 running 192.168.64.3/24\n"
		}
		return out, true, 0
	case len(argv) == 3 && argv[1] == "stop" && argv[2] == f.cname:
		f.stopped = true
		return f.cname + "\n", true, 0
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

// stopWorkspace makes a workspace whose config is cfg, enters it, and stubs the keeper's teardown;
// it returns the jail's name and the runtimes the teardown was handed.
func stopWorkspace(t *testing.T, cfg string) (cname string, finished *[]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "")
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(cfg), 0o644); err != nil {
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
	finishStop = func(_, _ io.Writer, _, rt string, _ int64, _ func(string, string)) int {
		got = append(got, rt)
		return 0
	}
	return runtime.FromWorkspace(cwd), &got
}

func useStopExec(t *testing.T, f func([]string) (string, bool, int)) {
	saved := stopExec
	t.Cleanup(func() { stopExec = saved })
	stopExec = f
}

// TestAStopEndsAnAppleContainerJailItsConfigNames is the call-site pin: `yolo stop` in a workspace
// whose config names Apple Container, YOLO_RUNTIME unset, finds the running jail and stops it there.
func TestAStopEndsAnAppleContainerJailItsConfigNames(t *testing.T) {
	cname, finished := stopWorkspace(t, `{"runtime": "container"}`)
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
	cname, finished := stopWorkspace(t, `{"runtime": "podman"}`)
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
	if rc := stopJail(&out, &errb, "/ws", "container", f.run, nil); rc != 1 {
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

// TestANoOpStopNamesTheOtherRuntimeWhenItCannotKnow: with no start record, yolo cannot know which
// runtime launched a jail it tracks. When another container runtime is installed, the no-op names
// the stop that asks it; with none installed, with nothing tracked, or with a start record that
// names the runtime it asked, it says nothing more.
func TestANoOpStopNamesTheOtherRuntimeWhenItCannotKnow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	saved := stopRuntimeInstalled
	t.Cleanup(func() { stopRuntimeInstalled = saved })
	record := filepath.Join(paths.GlobalStorage(), "owners", cname+".keeper.json")
	cases := []struct {
		name      string
		installed bool
		tracked   bool
		recorded  bool
		want      bool
	}{
		{"tracked, Apple Container installed", true, true, false, true},
		{"tracked, nothing else installed", false, true, false, false},
		{"untracked", true, false, false, false},
		{"tracked, Apple Container installed, launched on podman", true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stopRuntimeInstalled = func(bin string) bool { return tc.installed && bin == "container" }
			runtime.CleanupContainerTracking(cname)
			_ = os.Remove(record)
			if tc.recorded {
				if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, []byte(`{"pid":4242,"runtime":"podman"}`+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tracked {
				if err := runtime.WriteContainerTracking(cname, "/ws"); err != nil {
					t.Fatal(err)
				}
			}
			var out, errb bytes.Buffer
			s := &stopRun{stats: []string{"!Error: no such container"}}
			if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil); rc != 0 {
				t.Fatalf("rc=%d", rc)
			}
			got := strings.Contains(out.String(), "YOLO_RUNTIME=container yolo stop")
			if got != tc.want {
				t.Errorf("named `YOLO_RUNTIME=container yolo stop` = %v, want %v:\n%s", got, tc.want, out.String())
			}
		})
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
	if rc := stopJail(&out, &errb, "/ws", "container", f.run, nil); rc != 0 {
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
		{"the runtime's stop failed", []string{"true\n", "!stopped with an error"},
			[]string{"`podman stop " + runtime.FromWorkspace("/ws") + "` failed", "`yolo stop` again retries it"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			s := &stopRun{stats: tc.stats}
			if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil); rc != 1 {
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
