package run

// keepertrace_test.go pins the keeper's half of startup tracing (plan slice U6b3 B): the launcher
// hands its actual waiting span through the private plan, and the keeper joins it with an
// independently acquired claim, never minting, publishing or borrowing the launcher's.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// laneLines reads one JSONL lane file.
func laneLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("lane line %q: %v", ln, err)
		}
		out = append(out, m)
	}
	return out
}

// keeperLanes are the keeper lane files in a trace directory.
func keeperLanes(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "keeper-*.jsonl"))
	return files
}

// traceDirCount is how many trace directories the store holds.
func traceDirCount() int {
	entries, _ := os.ReadDir(paths.TracesDir())
	var n int
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

// launchCapturingKeeperPlan drives a fresh podman launch through Run, whose spawner reads the
// keeper's plan as the keeper would, then plays a keeper that relays started and ready. It
// returns the plan the launch serialized.
func launchCapturingKeeperPlan(t *testing.T, recording bool) *keeperPlan {
	t.Helper()
	ws := t.TempDir()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	writeUserPacks(t, home, `[]`)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	var captured *keeperPlan
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, progress, lifeline, _ *os.File, _ []*os.File) (func() int, error) {
		plan, err := readKeeperPlan(planPath)
		if err != nil {
			return nil, err
		}
		captured = plan
		removeKeeperPlan(planPath)
		prog, err := dupCloseOnExec(progress)
		if err != nil {
			return nil, err
		}
		life, err := dupCloseOnExec(lifeline)
		if err != nil {
			_ = prog.Close()
			return nil, err
		}
		_ = writeFrame(prog, frameStarted, []byte("4242"))
		_ = writeFrame(prog, frameReady, nil)
		_ = prog.Close()
		return func() int {
			_, _ = io.Copy(io.Discard, life)
			_ = life.Close()
			return 0
		}, nil
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	if recording {
		recordingOn(o)
	}
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.RestoreTerminal = func() {}
	done := make(chan struct{})
	go func() { defer close(done); Run(*o) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the launch did not end\nstderr:\n%s", stderr.String())
	}
	if captured == nil {
		t.Fatalf("no keeper was spawned:\n%s", stderr.String())
	}
	return captured
}

// TestKeeperPlanCarriesTheActualWaitingSpan: a recording launch serializes, in the keeper's
// private plan, the context of its real launch.run_with_proxy span in its own trace; a launch
// that records nothing serializes none. Red if the caller stops setting TraceContext.
func TestKeeperPlanCarriesTheActualWaitingSpan(t *testing.T) {
	plan := launchCapturingKeeperPlan(t, true)
	if !plan.PerfRecording {
		t.Fatal("the plan did not carry the recording gate")
	}
	ctx, ok := perf.ParseContext(plan.TraceContext)
	if !ok {
		t.Fatalf("the plan's trace context %q does not parse", plan.TraceContext)
	}
	tt := readLaunchTrace(t)
	if filepath.Base(tt.dir) != ctx.Trace.String() {
		t.Errorf("context trace %s is not the launcher's trace %s", ctx.Trace, filepath.Base(tt.dir))
	}
	var waiting string
	for _, m := range tt.lane {
		if m["k"] == "start" && m["name"] == "launch.run_with_proxy" {
			waiting, _ = m["span"].(string)
		}
	}
	if waiting == "" || waiting != ctx.Span.String() {
		t.Errorf("context span %s is not the launch.run_with_proxy span %q", ctx.Span, waiting)
	}

	_ = os.RemoveAll(paths.TracesDir())
	off := launchCapturingKeeperPlan(t, false)
	if off.TraceContext != "" || off.PerfRecording {
		t.Errorf("a launch with recording off handed the keeper %q (recording %v)", off.TraceContext, off.PerfRecording)
	}
}

// launcherWaitingContext acquires a real launcher trace under the current HOME and returns its
// scope and the context of a started waiting span, as the launch caller serializes it.
func launcherWaitingContext(t *testing.T, ws, cname string) (*InvocationScope, string) {
	t.Helper()
	o := NewDefaultOptions()
	o.Workspace = ws
	prev := o.Getenv
	o.Getenv = func(k string) string {
		if k == paths.TimingEnv {
			return "1"
		}
		return prev(k)
	}
	fillDefaults(&o)
	o.InvocationScope = NewInvocationScope()
	o.initPerf(cname)
	o.initLauncherTrace()
	if o.InvocationScope.claim == nil {
		t.Fatal("the launcher trace was not acquired")
	}
	sp := o.Perf.Span("launch.run_with_proxy")
	ctx := o.keeperTraceContext(sp)
	if ctx == "" {
		t.Fatal("no context for an acquired scope and a started span")
	}
	return o.InvocationScope, ctx
}

// TestKeeperJoinsTheLaunchersWaitingSpan: a real keeper handed that context records a lane of the
// same trace whose lifetime is a child of the waiting span, ends it last, and publishes nothing.
// Red if newKeeper/run stop joining.
func TestKeeperJoinsTheLaunchersWaitingSpan(t *testing.T) {
	var scope *InvocationScope
	var ctx string
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		scope, ctx = launcherWaitingContext(t, p.Workspace, p.Cname)
		p.PerfRecording, p.TraceContext = true, ctx
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Fatalf("the keeper ended %d", rc)
	}
	tr, _ := perf.ParseContext(ctx)
	dir := filepath.Join(paths.TracesDir(), tr.Trace.String())
	lanes := keeperLanes(dir)
	if len(lanes) != 1 {
		t.Fatalf("want one keeper lane in %s, got %v", dir, lanes)
	}
	lines := laneLines(t, lanes[0])
	if lines[0]["k"] != "lane" || lines[0]["parent"] != tr.Span.String() || lines[0]["trace"] != tr.Trace.String() {
		t.Errorf("lane header %v does not join %s", lines[0], ctx)
	}
	var start map[string]any
	for _, m := range lines[1:] {
		if m["name"] == "keeper" && m["k"] == "start" {
			start = m
		}
	}
	if start == nil || start["parent"] != tr.Span.String() || start["launcher_root"] == true {
		t.Errorf("keeper lifetime start %v is not an undesignated child of the waiting span", start)
	}
	if last := lines[len(lines)-1]; last["k"] != "end" || last["name"] != "keeper" {
		t.Errorf("the keeper lifetime does not end last: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err == nil {
		t.Error("the keeper published metadata")
	}
	scope.Finalize()
}

// TestKeeperTraceDeclinesWithoutFallback: with the gate off under hostile ambient opt-ins, or with
// an absent or malformed context, the keeper records no lane and mints nothing.
func TestKeeperTraceDeclinesWithoutFallback(t *testing.T) {
	cases := map[string]struct {
		recording bool
		ctx       func(valid string) string
	}{
		"gate off, valid context, hostile env": {false, func(v string) string { return v }},
		"absent context":                       {true, func(string) string { return "" }},
		"malformed context":                    {true, func(string) string { return "00-nothex" }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(paths.TimingEnv, "1")
			t.Setenv(paths.VerboseEnv, "1")
			var scope *InvocationScope
			var dir string
			var session *sessionLock
			f := startKeeperFixture(t, true, func(p *keeperPlan) {
				lock, _, err := takeSessionLock(p.Cname)
				if err != nil {
					t.Fatal(err)
				}
				session = lock
				var valid string
				scope, valid = launcherWaitingContext(t, p.Workspace, p.Cname)
				tr, _ := perf.ParseContext(valid)
				dir = filepath.Join(paths.TracesDir(), tr.Trace.String())
				p.PerfRecording, p.TraceContext = c.recording, c.ctx(valid)
			})
			if !f.relay() {
				t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
			}
			session.release()
			if rc := f.wait(); rc != 0 {
				t.Fatalf("the keeper ended %d", rc)
			}
			if lanes := keeperLanes(dir); len(lanes) != 0 {
				t.Errorf("a declined keeper recorded %v", lanes)
			}
			if n := traceDirCount(); n != 1 {
				t.Errorf("the keeper minted a trace of its own: %d trace dirs", n)
			}
			scope.Finalize()
		})
	}
}

// TestPlanBuildersFreezeTheLaunchersGate: both plan builders carry the gate the launcher froze at
// its collector, not a fresh read of the ambient flags. Red if either builder re-reads
// timingRecording.
func TestPlanBuildersFreezeTheLaunchersGate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
	o.Getenv = func(k string) string {
		if k == paths.TimingEnv {
			return "1"
		}
		return ""
	}
	o.macosUserKey = &macosUserKeying{}
	build := func() (bool, bool) {
		podman, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-home", stagedPacks{}, nil, nil, nil, "", "",
			[]string{"podman", "run"}, &assembleInput{})
		if err != nil {
			t.Fatal(err)
		}
		mac, err := o.macosUserKeeperPlanFor(jsonx.NewOrderedMap(), "macos-user", "yolo-home", stagedPacks{}, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return podman.PerfRecording, mac.PerfRecording
	}
	o.Perf = nil
	if p, m := build(); p || m {
		t.Errorf("no collector (the launcher froze OFF) but the plans record: podman %v, macos-user %v", p, m)
	}
	o.Perf = perf.New(time.Now)
	if p, m := build(); !p || !m {
		t.Errorf("a collector (the launcher froze ON) but the plans do not record: podman %v, macos-user %v", p, m)
	}
}
