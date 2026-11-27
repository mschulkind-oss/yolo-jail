package run

// invocationtrace_test.go pins the launcher's half of startup tracing (invocationtrace.go; plan
// slice U6b3 A) through the real Run: the trace is acquired at the collector's boundary, the
// branch is committed where the launch actually takes it, and the invocation closes once.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// tracedTrace is the one trace directory a launch left, and what is in it.
type tracedTrace struct {
	dir  string
	meta map[string]any // nil when meta.json is not published
	lane []map[string]any
}

// readLaunchTrace reads the single trace under paths.TracesDir. It fails when there is none.
func readLaunchTrace(t *testing.T) tracedTrace {
	t.Helper()
	tt, err := tryReadLaunchTrace()
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

// tryReadLaunchTrace is readLaunchTrace for a goroutine that is not the test's own.
func tryReadLaunchTrace() (tracedTrace, error) {
	var out tracedTrace
	entries, err := os.ReadDir(paths.TracesDir())
	if err != nil {
		return out, fmt.Errorf("no trace store at %s: %v", paths.TracesDir(), err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return out, fmt.Errorf("want exactly one trace directory, found %v", dirs)
	}
	out.dir = filepath.Join(paths.TracesDir(), dirs[0])
	if b, err := os.ReadFile(filepath.Join(out.dir, "meta.json")); err == nil {
		if err := json.Unmarshal(b, &out.meta); err != nil {
			return out, fmt.Errorf("meta.json is not JSON: %v", err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(out.dir, "launcher-*.jsonl"))
	if len(files) != 1 {
		return out, fmt.Errorf("want one launcher lane file in %s, found %v", out.dir, files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		return out, err
	}
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			return out, fmt.Errorf("lane line %q: %v", ln, err)
		}
		out.lane = append(out.lane, m)
	}
	return out, nil
}

// rootEvents are the lane's events naming the invocation root: its designated start and its end.
func (tt tracedTrace) rootEvents() (starts, ends int, designated bool) {
	for _, m := range tt.lane {
		if m["name"] != "invocation" {
			continue
		}
		switch m["k"] {
		case "start":
			starts++
			designated = m["launcher_root"] == true
		case "end":
			ends++
		}
	}
	return
}

// recordingOn turns the persistent recording gate on through the environment seam (fillDefaults
// re-reads the config-driven one inside Run): recording without the printed report.
func recordingOn(o *Options) {
	prev := o.Getenv
	o.Getenv = func(k string) string {
		if k == paths.TimingEnv {
			return "1"
		}
		return prev(k)
	}
}

// TestRunInvocationTraceOFFAndEarlyGuards: with recording off, Run does no trace-root lookup,
// admission or adoption whatever the environment says, and an early guard's refusal happens before
// the collector exists, so a refused launch leaves no trace even with recording on.
func TestRunInvocationTraceOFFAndEarlyGuards(t *testing.T) {
	_, ws, _, _ := attachStagingFixture(t)
	t.Setenv("YOLO_TRACEPARENT", "00-"+strings.Repeat("a", 32)+"-"+strings.Repeat("b", 16)+"-01")
	attachThroughRun(t, ws, nil)
	if _, err := os.Lstat(paths.TracesDir()); err == nil {
		t.Fatalf("a launch with recording off made %s", paths.TracesDir())
	}

	home := os.Getenv("HOME")
	var out, errOut bytes.Buffer
	o := dispatchOptions(t, home, "podman", &out, &errOut, nil)
	recordingOn(o)
	if rc := Run(*o); rc != 1 || !strings.Contains(errOut.String(), "Refusing to launch") {
		t.Fatalf("the home as workspace was not refused (rc %d):\n%s", rc, errOut.String())
	}
	if _, err := os.Lstat(paths.TracesDir()); err == nil {
		t.Fatalf("a launch refused by an early guard made %s", paths.TracesDir())
	}
}

// TestRunInvocationTraceCommitsActualBranches drives a real attach with recording on and a scope
// the test borrows: the metadata is published at the attach's commit, while the invocation is
// still open, with the actual jail and the original collector's start.
func TestRunInvocationTraceCommitsActualBranches(t *testing.T) {
	_, ws, _, _ := attachStagingFixture(t)
	cname := runtime.JailName(ws, "podman-self")
	scope := NewInvocationScope()
	ref := &PerfRef{}
	attachThroughRun(t, ws, func(o *Options) {
		recordingOn(o)
		o.InvocationScope = scope
		o.PerfRef = ref
	})
	if ref.Log == nil {
		t.Fatal("the legacy collector was not published")
	}
	tt := readLaunchTrace(t)
	want := map[string]any{
		"kind":      "attach",
		"jail":      paths.JailShortHash(cname),
		"workspace": paths.JailShortHash(runtime.FromWorkspace(ws)),
		"version":   version.Baked(),
		"commit":    version.GitCommit,
		"trace":     filepath.Base(tt.dir),
	}
	if len(tt.meta) != 7 {
		t.Fatalf("metadata has %d keys, want exactly seven: %v", len(tt.meta), tt.meta)
	}
	for k, v := range want {
		if tt.meta[k] != v {
			t.Errorf("meta[%s] = %v, want %v", k, tt.meta[k], v)
		}
	}
	if got := tt.meta["start"].(float64); got != float64(ref.Log.StartTime().UnixNano()) {
		t.Errorf("meta start %v is not the original collector's start %d", got, ref.Log.StartTime().UnixNano())
	}
	starts, ends, designated := tt.rootEvents()
	if starts != 1 || !designated {
		t.Errorf("want one designated invocation start, got %d (designated %v)", starts, designated)
	}
	if ends != 0 {
		t.Error("a borrowed scope was finalized by Run: the front door still has its title restore to run")
	}

	scope.Finalize()
	scope.Finalize()
	tt = readLaunchTrace(t)
	if _, ends, _ := tt.rootEvents(); ends != 1 {
		t.Errorf("want exactly one invocation end after Finalize, got %d", ends)
	}
	if tt.meta["kind"] != "attach" {
		t.Errorf("finalizing reclassified the committed branch: %v", tt.meta["kind"])
	}
}

// TestRunInvocationTraceFreezesMissingOrConflictingProvenance: a stamp the metadata validator
// refuses publishes nothing, says so once, and changes no outcome; later finalization does not
// publish a guess.
func TestRunInvocationTraceFreezesMissingOrConflictingProvenance(t *testing.T) {
	_, ws, _, _ := attachStagingFixture(t)
	saved := version.GitCommit
	version.GitCommit = "not a valid stamp!"
	t.Cleanup(func() { version.GitCommit = saved })
	scope := NewInvocationScope()
	r := attachThroughRun(t, ws, func(o *Options) {
		recordingOn(o)
		o.InvocationScope = scope
	})
	tt := readLaunchTrace(t)
	if tt.meta != nil {
		t.Fatalf("invalid provenance was published: %v", tt.meta)
	}
	if n := strings.Count(r.stderr, "trace"); n == 0 {
		t.Errorf("publication refusal was not reported:\n%s", r.stderr)
	}
	scope.Finalize()
	if tt = readLaunchTrace(t); tt.meta != nil {
		t.Errorf("finalization published after a frozen refusal: %v", tt.meta)
	}
}

// TestRunInvocationTraceIncompleteWhenNoBranchCommits: a direct Run owns its own scope, and a
// launch that stops before any branch publishes `incomplete` and closes its invocation.
func TestRunInvocationTraceIncompleteWhenNoBranchCommits(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	var out, errOut bytes.Buffer
	o := runFatalOptions(t, ws, "podman", &out, &errOut)
	recordingOn(o)
	if rc := Run(*o); rc == 0 {
		t.Fatalf("the repo-less launch succeeded:\n%s", errOut.String())
	}
	tt := readLaunchTrace(t)
	if tt.meta == nil || tt.meta["kind"] != "incomplete" {
		t.Fatalf("want published kind incomplete, got %v", tt.meta)
	}
	if starts, ends, _ := tt.rootEvents(); starts != 1 || ends != 1 {
		t.Errorf("want the invocation started and ended once, got %d starts %d ends", starts, ends)
	}
}

// TestRunInvocationTracePreservesLegacyCollectorAndReports: the trace is beside the legacy log,
// never in it: host-perf.log keeps its ordinary spans and never names the root.
func TestRunInvocationTracePreservesLegacyCollectorAndReports(t *testing.T) {
	_, ws, _, _ := attachStagingFixture(t)
	scope := NewInvocationScope()
	t.Cleanup(scope.Finalize)
	ref := &PerfRef{}
	attachThroughRun(t, ws, func(o *Options) {
		recordingOn(o)
		o.InvocationScope = scope
		o.PerfRef = ref
	})
	legacy, err := os.ReadFile(filepath.Join(ws, ".yolo", "logs", HostPerfLogName))
	if err != nil {
		t.Fatalf("host-perf.log: %v", err)
	}
	if !strings.Contains(string(legacy), "probes.done") {
		t.Errorf("the legacy log lost its ordinary events:\n%s", legacy)
	}
	if strings.Contains(string(legacy), "invocation") {
		t.Errorf("the trace-only root leaked into host-perf.log:\n%s", legacy)
	}
	tt := readLaunchTrace(t)
	var sawOrdinary bool
	for _, m := range tt.lane {
		if m["name"] == "probes.done" {
			sawOrdinary = true
		}
	}
	if !sawOrdinary {
		t.Error("the trace lane holds no ordinary event beside the root")
	}
}

// TestRunInvocationTraceFinalizesBeforeExplicitExit: the arms that end the process with os.Exit
// close the trace first, at the captured exit, before any defer could.
func TestRunInvocationTraceFinalizesBeforeExplicitExit(t *testing.T) {
	cases := map[string]func(o *Options, cname string) func(){
		"post-keeper arm": func(o *Options, cname string) func() {
			arm := armLaunchSignalsWith(func() {}, o.interruptedArmExit())
			return func() { popLaunchArm(arm) }
		},
		"attach arm": func(o *Options, cname string) func() {
			arm := o.attachSignalArm("podman", cname, "")
			return func() { popLaunchArm(arm) }
		},
	}
	for name, install := range cases {
		t.Run(name, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			cname := runtime.JailName(ws, "podman-self")
			var out, errOut bytes.Buffer
			o := dispatchOptions(t, ws, "podman", &out, &errOut, nil)
			recordingOn(o)
			o.RestoreTerminal = func() {}
			o.InvocationScope = NewInvocationScope()
			o.initPerf(runtime.FromWorkspace(ws))
			o.initLauncherTrace()
			if o.InvocationScope.claim == nil {
				t.Fatalf("the trace was not acquired:\n%s", errOut.String())
			}
			var endsAtExit int
			var tt tracedTrace
			var readErr error
			exits := seeArmExitsWith(t, cname, func() {
				tt, readErr = tryReadLaunchTrace()
				_, endsAtExit, _ = tt.rootEvents()
			})
			catchSignal(t, syscall.SIGTERM)
			cleanup := install(o, cname)
			t.Cleanup(cleanup)
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			e := <-exits
			if e.code != 128+int(syscall.SIGTERM) {
				t.Errorf("exit %d, want %d", e.code, 128+int(syscall.SIGTERM))
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if endsAtExit != 1 {
				t.Errorf("at the explicit exit the invocation had %d ends, want 1", endsAtExit)
			}
			if tt.meta == nil || tt.meta["kind"] != "incomplete" {
				t.Errorf("an interrupted uncommitted launch must publish incomplete, got %v", tt.meta)
			}
		})
	}
}

// TestRunInvocationTraceCommitsNativeAndFreshBranches: the native arm commits `launch` after its
// approval and before its payload, a dry run stays precommit, and the container arm's fresh path
// commits `launch` before its image work; each is read at the effect seam, with the invocation
// still open.
func TestRunInvocationTraceCommitsNativeAndFreshBranches(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		home := packHome(t)
		writeUserPacks(t, home, `[]`)
		ws := t.TempDir()
		var out, errOut bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &out, &errOut, nil)
		recordingOn(o)
		var seen tracedTrace
		var readErr error
		o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int {
			seen, readErr = tryReadLaunchTrace()
			return 0
		})
		if rc := Run(*o); rc != 0 {
			t.Fatalf("native launch rc %d:\n%s", rc, errOut.String())
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if seen.meta == nil || seen.meta["kind"] != "launch" ||
			seen.meta["jail"] != paths.JailShortHash(runtime.JailName(ws, "seatbelt-yolo-account")) {
			t.Errorf("at the native backend the metadata is %v, want kind launch and the actual jail", seen.meta)
		}
		if _, ends, _ := seen.rootEvents(); ends != 0 {
			t.Error("the invocation had ended before the backend returned")
		}
	})
	t.Run("dry run stays precommit", func(t *testing.T) {
		home := packHome(t)
		writeUserPacks(t, home, `[]`)
		ws := t.TempDir()
		var out, errOut bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &out, &errOut, nil)
		recordingOn(o)
		o.DryRun = true
		var seen tracedTrace
		var readErr error
		o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int {
			seen, readErr = tryReadLaunchTrace()
			return 0
		})
		_ = Run(*o)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if seen.meta != nil {
			t.Errorf("a dry run committed a branch: %v", seen.meta)
		}
	})
	t.Run("fresh container", func(t *testing.T) {
		home := packHome(t)
		writeUserPacks(t, home, `[]`)
		ws := t.TempDir()
		t.Setenv("PATH", t.TempDir())
		var out, errOut bytes.Buffer
		o := dispatchOptions(t, ws, "podman", &out, &errOut, nil)
		recordingOn(o)
		repo, _ := o.RepoRoot()
		o.PathExists = func(p string) bool {
			return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
		}
		o.Exec = func([]string, string, []string, time.Duration) ExecResult {
			return ExecResult{Ran: true, RC: 0}
		}
		var seen tracedTrace
		var readErr error
		o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
			seen, readErr = tryReadLaunchTrace()
			return image.LoadResult{OK: false}
		}
		_ = Run(*o)
		if readErr != nil {
			t.Fatalf("the launch never reached its image build: %v\n%s", readErr, errOut.String())
		}
		if seen.meta == nil || seen.meta["kind"] != "launch" {
			t.Errorf("at the image build the metadata is %v, want kind launch", seen.meta)
		}
	})
}

// TestRunInvocationTraceAbandonLaunchFinalizes: a launch run inside this one, in this process,
// ends the process on a signal; this launch's trace closes with it (abandonLaunch).
func TestRunInvocationTraceAbandonLaunchFinalizes(t *testing.T) {
	f := newGuardFixture(t, "yolo-guard-trace")
	f.o.Getenv = func(k string) string {
		if k == paths.TimingEnv {
			return "1"
		}
		return ""
	}
	f.o.InvocationScope = NewInvocationScope()
	f.o.initPerf(runtime.FromWorkspace(f.o.Workspace))
	f.o.initLauncherTrace()
	if f.o.InvocationScope.claim == nil {
		t.Fatal("the trace was not acquired")
	}
	f.o.abandonLaunch(f.o.launchGuard)
	tt := readLaunchTrace(t)
	if _, ends, _ := tt.rootEvents(); ends != 1 {
		t.Errorf("abandonLaunch left the invocation with %d ends, want 1", ends)
	}
}
