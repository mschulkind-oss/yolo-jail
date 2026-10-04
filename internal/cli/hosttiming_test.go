package cli

// hosttiming_test.go pins the timing surface at the HOST NOTCH (perf-logging.md D18;
// internal/cli/run's hosttiming.go): `yolo host -- <cmd>` and `yolo host apply` span their stages
// by the jail launch's two gates, into the machine-wide host-notch-perf.log, and print the table
// only when this invocation typed --timing or --verbose. Every cell runs the real front door
// (hostMain, Main) with the exec replaced, so deleting a span's call site fails the cell naming it.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// hostLaunchStages are the spans every `yolo host -- <cmd>` that reaches its exec records, in
// the order it runs them.
var hostLaunchStages = []string{"host.pack_refresh", "host.capability_gate", "host.apply_gate",
	"host.compose", "host.preflight", "host.resolve_target", "host.model_menu",
	"host.openai_prelaunch"}

// timingHome is a scratch host with no timing opt-in of its own: a temp HOME holding cfg as the
// user config, the timing variables blank, the global --verbose untyped, and a `mytool` on PATH.
// It returns the directory the launch runs in, which is also a scratch directory.
func timingHome(t *testing.T, cfg string) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(paths.TimingEnv, "")
	t.Setenv(paths.VerboseEnv, "")
	orig := verboseFlagTyped
	verboseFlagTyped = false
	t.Cleanup(func() { verboseFlagTyped = orig })
	if cfg != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "mytool"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "mytool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd = t.TempDir()
	t.Chdir(cwd)
	return home, cwd
}

// hostTimingLaunch runs `yolo host <flags> -- mytool` through hostMain with the exec replaced, and
// returns the exit code, stderr, and the perf file as it stood when the exec was reached.
func hostTimingLaunch(t *testing.T, flags ...string) (rc int, errs, atExec string) {
	t.Helper()
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error {
		data, _ := os.ReadFile(run.HostNotchPerfLogPath())
		atExec = string(data)
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = orig })
	var errw bytes.Buffer
	rc = hostMain(append(append([]string{}, flags...), "--", "mytool"), io.Discard, &errw, false, nil)
	return rc, errw.String(), atExec
}

// assertNoWorkspaceState fails if dir holds a .yolo: a host command must never mint one.
func assertNoWorkspaceState(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, ".yolo")); err == nil {
		t.Errorf("a host-notch command created %s", filepath.Join(dir, ".yolo"))
	}
}

// --timing RECORDS EVERY STAGE AND PRINTS THE TABLE BEFORE THE HAND-OVER: each span is in the file
// by the time the exec runs, the table is on stderr ahead of the starting line, and the file's
// header names the directory by its short code, never by its path.
func TestHostTimingRecordsEveryStageAndPrintsBeforeTheHandOver(t *testing.T) {
	_, cwd := timingHome(t, "")
	rc, errs, atExec := hostTimingLaunch(t, "--timing")
	if rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errs)
	}
	for _, name := range hostLaunchStages {
		if !strings.Contains(atExec, "end    "+name+"  dur=") {
			t.Errorf("the perf file lacks the end of %s when the exec runs:\n%s", name, atExec)
		}
		if !strings.Contains(errs, "  "+name+"\n") {
			t.Errorf("the printed table lacks %s:\n%s", name, errs)
		}
	}
	if !strings.Contains(atExec, "mark   host.handover") {
		t.Errorf("the perf file lacks the hand-over mark:\n%s", atExec)
	}
	label := "jail=host:" + paths.JailShortHash(runtime.FromWorkspace(cwd)) + " ==="
	if !strings.Contains(atExec, label) {
		t.Errorf("the run header lacks %q:\n%s", label, atExec)
	}
	if strings.Contains(atExec, cwd) {
		t.Errorf("the perf file names the directory the launch ran in (%s):\n%s", cwd, atExec)
	}
	table := strings.Index(errs, "yolo host timing (to the hand-over):")
	starting := strings.Index(errs, "yolo host: starting mytool")
	if table < 0 || starting < 0 || table > starting {
		t.Errorf("the table must print before the starting line, the last thing yolo says:\n%s", errs)
	}
	assertNoWorkspaceState(t, cwd)
}

// A PERSISTENT OPT-IN RECORDS IN SILENCE (D12): YOLO_TIMING, an inherited YOLO_VERBOSE, or
// `perf_logging: true` writes the file and prints one line naming it, never the table. With no
// opt-in at all, nothing is written and nothing is said.
func TestHostTimingPersistentOptInsRecordQuietly(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		env       map[string]string
		records   bool
	}{
		{"YOLO_TIMING", "", map[string]string{paths.TimingEnv: "1"}, true},
		{"YOLO_VERBOSE inherited", "", map[string]string{paths.VerboseEnv: "1"}, true},
		{"perf_logging", `{"perf_logging": true}`, nil, true},
		{"no opt-in", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cwd := timingHome(t, tc.cfg)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			rc, errs, atExec := hostTimingLaunch(t)
			if rc != 0 {
				t.Fatalf("rc = %d\n%s", rc, errs)
			}
			if strings.Contains(errs, "yolo host timing") {
				t.Errorf("a persistent opt-in printed the table:\n%s", errs)
			}
			line := "yolo: timings recorded in " + run.HostNotchPerfLogPath()
			if tc.records {
				if !strings.Contains(atExec, "end    host.compose") {
					t.Errorf("nothing recorded:\n%s", atExec)
				}
				if !strings.Contains(errs, line) {
					t.Errorf("the quiet path did not name the file (%q):\n%s", line, errs)
				}
			} else {
				if _, err := os.Stat(run.HostNotchPerfLogPath()); err == nil {
					t.Errorf("a launch with no opt-in wrote %s", run.HostNotchPerfLogPath())
				}
				if strings.Contains(errs, "timings recorded") {
					t.Errorf("a launch with no opt-in mentioned timings:\n%s", errs)
				}
			}
			if !strings.HasPrefix(run.HostNotchPerfLogPath(), home) {
				t.Errorf("the perf file %s is not under this host's storage", run.HostNotchPerfLogPath())
			}
			assertNoWorkspaceState(t, cwd)
		})
	}
}

// A TYPED --verbose PRINTS THE TABLE like --timing (D12's second explicit flag).
func TestHostTimingTypedVerbosePrints(t *testing.T) {
	timingHome(t, "")
	verboseFlagTyped = true
	rc, errs, _ := hostTimingLaunch(t)
	if rc != 0 || !strings.Contains(errs, "yolo host timing (to the hand-over):") {
		t.Fatalf("rc=%d, want the table for a typed --verbose:\n%s", rc, errs)
	}
}

// THE LAUNCH RUN IN THE HOME MINTS NO ~/.yolo, with every recording opt-in on.
func TestHostTimingInTheHomeCreatesNoWorkspaceState(t *testing.T) {
	home, _ := timingHome(t, `{"perf_logging": true}`)
	t.Chdir(home)
	if rc, errs, _ := hostTimingLaunch(t, "--timing"); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errs)
	}
	assertNoWorkspaceState(t, home)
}

// THE FLOOR INSTALL IS INSIDE host.resolve_target: the floor is built while that span is open, so
// a slow first launch's install is attributed to it rather than to the time between two spans.
func TestHostTimingResolveTargetCoversTheFloorInstall(t *testing.T) {
	dist, _ := floorHostFixture(t, "")
	t.Setenv(paths.TimingEnv, "")
	t.Setenv(paths.VerboseEnv, "")
	got := captureHostExec(t)
	var during string
	inner := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		data, _ := os.ReadFile(run.HostNotchPerfLogPath())
		during = string(data)
		return inner(out, progs)
	}
	t.Cleanup(func() { newHostFloor = inner })
	var errw bytes.Buffer
	if rc := hostExec([]string{"--timing"}, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if n := len(dist.NpmCalls("install")); n != 1 {
		t.Fatalf("npm install ran %d times, want the first launch's one install", n)
	}
	if !strings.Contains(during, "start  host.resolve_target") || strings.Contains(during, "end    host.resolve_target") {
		t.Errorf("the floor was built outside an open host.resolve_target span:\n%s", during)
	}
	if !strings.Contains(errw.String(), "  host.resolve_target\n") {
		t.Errorf("the table lacks host.resolve_target:\n%s", errw.String())
	}
}

// THE LAUNCH-OWNED SERVICES START INSIDE host.services_start, and the table still prints before
// the starting line on that path, where yolo stays the agent's parent instead of exec'ing.
func TestHostTimingSpansTheServicesStart(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	var during string
	inner := startLaunchService
	startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
		data, _ := os.ReadFile(run.HostNotchPerfLogPath())
		during = string(data)
		return inner(p, env)
	}
	t.Cleanup(func() { startLaunchService = inner })
	t.Setenv(paths.TimingEnv, "")
	t.Setenv(paths.VerboseEnv, "")
	l := runServiceLaunch(t, codexConfig(upstream.URL), []string{"--timing", "-p", "codex"}, "", nil)
	if l.rc != 0 || l.execed {
		t.Fatalf("rc=%d execed=%v\n%s", l.rc, l.execed, l.errs)
	}
	if !strings.Contains(during, "start  host.services_start") || strings.Contains(during, "end    host.services_start") {
		t.Errorf("the service started outside an open host.services_start span:\n%s", during)
	}
	table := strings.Index(l.errs, "yolo host timing (to the hand-over):")
	starting := strings.Index(l.errs, "yolo host: starting claude")
	if table < 0 || starting < table || !strings.Contains(l.errs[table:starting], "  host.services_start\n") {
		t.Errorf("the table, with host.services_start, must print before the starting line:\n%s", l.errs)
	}
	assertServiceGone(t, l)
}

// `yolo host apply --timing --format json` TIMES EVERY STAGE AND KEEPS STDOUT ONE DOCUMENT: the
// table goes to stderr. And every spelling of the request times the same stages: the flag before
// the verb (`yolo --timing host apply`) and `yolo apply --at host --timing`.
func TestHostApplyTimingKeepsTheDocumentAndTimesEveryStage(t *testing.T) {
	stages := []string{"host_apply.pack_refresh", "host_apply.render", "host_apply.wrappers",
		"host_apply.floor"}
	for _, tc := range []struct {
		name string
		run  func(out, errw io.Writer) int
	}{
		{"host apply --timing --format json", func(out, errw io.Writer) int {
			return hostMain([]string{"apply", "--timing", "--format", "json"}, out, errw, false, nil)
		}},
		{"--timing host apply --format json", func(out, errw io.Writer) int {
			return hostMain([]string{"--timing", "apply", "--format", "json"}, out, errw, false, nil)
		}},
		{"apply --at host --timing --format json", func(out, errw io.Writer) int {
			return applyMain([]string{"--at", "host", "--timing", "--format", "json"}, out, errw, false, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cwd := timingHome(t, `{"packs": ["pi"]}`)
			stubDeclaredBins(t)
			var out, errw bytes.Buffer
			if rc := tc.run(&out, &errw); rc != 0 {
				t.Fatalf("rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
			}
			dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
			var doc map[string]any
			if err := dec.Decode(&doc); err != nil {
				t.Fatalf("stdout is not a JSON document: %v\n%s", err, out.String())
			}
			if dec.More() {
				t.Errorf("stdout holds more than one document:\n%s", out.String())
			}
			if !strings.Contains(errw.String(), "yolo host apply timing (rc 0):") {
				t.Errorf("no table on stderr:\n%s", errw.String())
			}
			data, _ := os.ReadFile(run.HostNotchPerfLogPath())
			for _, name := range stages {
				if !strings.Contains(errw.String(), "  "+name+"\n") || !strings.Contains(string(data), "end    "+name) {
					t.Errorf("%s is missing from the table or the file:\nstderr:\n%s\nfile:\n%s",
						name, errw.String(), data)
				}
			}
			assertNoWorkspaceState(t, cwd)
		})
	}
}

// --timing BELONGS TO THE HOST NOTCH'S APPLY: at the jail notch `yolo apply` runs no stage, so the
// flag is refused by name (exit 2), never ignored.
func TestApplyRefusesTimingAwayFromTheHost(t *testing.T) {
	timingHome(t, `{}`)
	for _, argv := range [][]string{{"--timing"}, {"--at", "jail", "--timing"}, {"--sealed", "--timing"}} {
		var out, errw bytes.Buffer
		rc := applyMain(argv, &out, &errw, false, nil)
		if rc != 2 || !strings.Contains(errw.String(), "--timing times the HOST notch's apply") {
			t.Errorf("`yolo apply %s`: rc=%d, want 2 and the refusal\n%s", strings.Join(argv, " "), rc, errw.String())
		}
	}
}
