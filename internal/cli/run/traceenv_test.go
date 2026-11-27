package run

// traceenv_test.go pins the argv half of startup tracing (plan slice U6b3 C): the helper's table,
// and, through the production call sites, the carrier at the actual final argv of the keeper's
// container, the first session's exec and an attach's exec.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jailtrace"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestInsertTraceEnvBefore(t *testing.T) {
	const ctx = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	pair := []string{"-e", jailtrace.Env + "=" + ctx}
	cases := []struct {
		name     string
		argv     []string
		boundary string
		carrier  string
		want     []string
		ok       bool
	}{
		{"plain", []string{"podman", "run", "-d", "img", "yolo-entrypoint"}, "img", ctx,
			[]string{"podman", "run", "-d", "-e", pair[1], "img", "yolo-entrypoint"}, true},
		{"empty carrier is explicit", []string{"podman", "exec", "-i", "c", "ep"}, "c", "",
			[]string{"podman", "exec", "-i", "-e", jailtrace.Env + "=", "c", "ep"}, true},
		{"stale -e pair replaced", []string{"p", "run", "-e", jailtrace.Env + "=stale", "-e", "A=1", "img"}, "img", ctx,
			[]string{"p", "run", "-e", "A=1", "-e", pair[1], "img"}, true},
		{"stale --env pair and --env= replaced", []string{"p", "run", "--env", jailtrace.Env + "=x", "--env=" + jailtrace.Env + "=y", "img"}, "img", ctx,
			[]string{"p", "run", "-e", pair[1], "img"}, true},
		{"bare pass-through replaced", []string{"p", "run", "-e", jailtrace.Env, "img"}, "img", ctx,
			[]string{"p", "run", "-e", pair[1], "img"}, true},
		{"unrelated variables kept", []string{"p", "run", "-e", jailtrace.Env + "X=1", "img"}, "img", ctx,
			[]string{"p", "run", "-e", jailtrace.Env + "X=1", "-e", pair[1], "img"}, true},
		{"payload after the boundary untouched", []string{"p", "exec", "c", "ep", "-e", jailtrace.Env + "=payload"}, "c", ctx,
			[]string{"p", "exec", "-e", pair[1], "c", "ep", "-e", jailtrace.Env + "=payload"}, true},
		{"absent boundary", []string{"p", "run", "img"}, "other", ctx, []string{"p", "run", "img"}, false},
		{"empty boundary", []string{"p", "run", "img"}, "", ctx, []string{"p", "run", "img"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string{}, tc.argv...)
			got, ok := insertTraceEnvBefore(in, tc.boundary, tc.carrier)
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
			if !reflect.DeepEqual(in, tc.argv) {
				t.Errorf("the input argv was mutated: %q", in)
			}
		})
	}
}

// carrierOf returns every YOLO_TRACEPARENT value an `-e` pair in argv sets, and the argument right
// after the last such pair.
func carrierOf(argv []string) (values []string, before string) {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], jailtrace.Env+"=") {
			values = append(values, strings.TrimPrefix(argv[i+1], jailtrace.Env+"="))
			before = ""
			if i+2 < len(argv) {
				before = argv[i+2]
			}
		}
	}
	return values, before
}

// captureJailMainArgv swaps the keeper's jail-main starter for one that records the argv of its
// first start (the callback retry reuses the same seam) and then starts it for real.
func captureJailMainArgv(t *testing.T) chan []string {
	t.Helper()
	got := make(chan []string, 1)
	saved := keeperStartJailMain
	t.Cleanup(func() { keeperStartJailMain = saved })
	keeperStartJailMain = func(a []string, out, errs io.Writer, exited func()) (*jailMain, error) {
		select {
		case got <- append([]string{}, a...):
		default:
		}
		return saved(a, out, errs, exited)
	}
	return got
}

// keeperRuntimeArgv runs a real keeper whose plan's argv ends in the image, with a stale carrier
// ahead of it, and returns the argv the keeper's actual jail-main start received, plus the
// keeper's lifetime span id and the trace when it joined one.
func keeperRuntimeArgv(t *testing.T, recording, withContext bool) (argv []string, lifetimeSpan, trace string) {
	t.Helper()
	got := captureJailMainArgv(t)
	var scope *InvocationScope
	var ctx string
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		p.PerfRecording = recording
		if withContext {
			scope, ctx = launcherWaitingContext(t, p.Workspace, p.Cname)
			p.TraceContext = ctx
		}
		p.RunCmd = append(append([]string{}, p.RunCmd...), "arg0", "-e", jailtrace.Env+"=stale", p.ImageRef)
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Fatalf("the keeper ended %d", rc)
	}
	select {
	case argv = <-got:
	default:
		t.Fatal("the keeper never started its container")
	}
	if withContext {
		tr, _ := perf.ParseContext(ctx)
		trace = tr.Trace.String()
		for _, lane := range keeperLanes(filepath.Join(paths.TracesDir(), trace)) {
			for _, m := range laneLines(t, lane) {
				if m["k"] == "start" && m["name"] == "keeper" {
					lifetimeSpan, _ = m["span"].(string)
				}
			}
		}
		scope.Finalize()
	}
	return argv, lifetimeSpan, trace
}

// TestKeeperInvocationTraceReachesFinalRuntimeArgv: the container the keeper starts carries ONE
// carrier, the keeper's own joined lifetime context, immediately before the image, after the
// stale one the plan's argv held was removed. Red if the keeper stops inserting it.
func TestKeeperInvocationTraceReachesFinalRuntimeArgv(t *testing.T) {
	argv, span, trace := keeperRuntimeArgv(t, true, true)
	values, before := carrierOf(argv)
	if len(values) != 1 || before != "the-image" {
		t.Fatalf("want one carrier immediately before the image, got %v before %q in %q", values, before, argv)
	}
	if span == "" {
		t.Fatal("the keeper recorded no lifetime span to point at")
	}
	if want := "00-" + trace + "-" + span + "-01"; values[0] != want {
		t.Errorf("carrier %q, want the keeper lifetime context %q", values[0], want)
	}
}

// TestKeeperInvocationTraceSendsAnEmptyCarrierWhenItJoinedNothing: no joined lifetime (gate off)
// means the explicit empty carrier, never the stale value the plan's argv held.
func TestKeeperInvocationTraceSendsAnEmptyCarrierWhenItJoinedNothing(t *testing.T) {
	argv, _, _ := keeperRuntimeArgv(t, false, false)
	values, before := carrierOf(argv)
	if len(values) != 1 || values[0] != "" || before != "the-image" {
		t.Fatalf("want one EMPTY carrier before the image, got %v before %q in %q", values, before, argv)
	}
}

// TestKeeperInvocationTraceMissingBoundaryDegradesOnly: an image ref the argv does not hold
// warns and starts the container with the argv unchanged; the launch outcome is not changed.
func TestKeeperInvocationTraceMissingBoundaryDegradesOnly(t *testing.T) {
	got := captureJailMainArgv(t)
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		p.ImageRef = "not-in-the-argv"
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Fatalf("a missing boundary changed the keeper's outcome: %d", rc)
	}
	argv := <-got
	if values, _ := carrierOf(argv); len(values) != 0 {
		t.Errorf("a carrier was placed with no boundary: %q", argv)
	}
	if !strings.Contains(f.errOut.String()+f.keeperLog(), "trace propagation declined") {
		t.Errorf("no warning for the missing boundary:\n%s", f.errOut.String())
	}
}

// launchCapturingExecArgv is a fresh podman launch through Run whose fake runtime writes the
// first session's exec argv, and whose keeper is played in-process (as launchCapturingKeeperPlan).
// With recording on it also returns the launch's actual waiting span id.
func launchCapturingExecArgv(t *testing.T, recording bool) (execArgv []string, trace, waiting string) {
	t.Helper()
	ws := t.TempDir()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	writeUserPacks(t, home, `[]`)
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nif [ \"$1\" = exec ]; then for a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + record + "; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, progress, lifeline, _ *os.File, _ []*os.File) (func() int, error) {
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
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the runtime never saw the first session's exec: %v\n%s", err, stderr.String())
	}
	execArgv = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if recording {
		tt := readLaunchTrace(t)
		trace = filepath.Base(tt.dir)
		for _, m := range tt.lane {
			if m["k"] == "start" && m["name"] == "launch.run_with_proxy" {
				waiting, _ = m["span"].(string)
			}
		}
	}
	return execArgv, trace, waiting
}

// TestRunInvocationTraceReachesFirstSessionArgv: the first session's exec carries the launch's
// actual launch.run_with_proxy waiting span right before the container name; with recording off
// it carries the explicit empty carrier. Red if the late insertion in runContainer is removed.
func TestRunInvocationTraceReachesFirstSessionArgv(t *testing.T) {
	argv, trace, waiting := launchCapturingExecArgv(t, true)
	values, _ := carrierOf(argv)
	if len(values) != 1 || waiting == "" {
		t.Fatalf("want one carrier and a waiting span, got %v / %q in %q", values, waiting, argv)
	}
	if want := "00-" + trace + "-" + waiting + "-01"; values[0] != want {
		t.Errorf("first session carrier %q, want the waiting span's %q", values[0], want)
	}
	_ = os.RemoveAll(paths.TracesDir())
	off, _, _ := launchCapturingExecArgv(t, false)
	if v, before := carrierOf(off); len(v) != 1 || v[0] != "" || !strings.HasPrefix(before, "yolo-") {
		t.Errorf("recording off: want one empty carrier before the container, got %v before %q in %q", v, before, off)
	}
}

// TestAttachInvocationTraceReachesExecArgv: an attach's exec carries its actual attach.exec span;
// without an acquired trace, the explicit empty carrier.
func TestAttachInvocationTraceReachesExecArgv(t *testing.T) {
	for _, traced := range []bool{true, false} {
		o, cfg, channel, stderr := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
		argv := recordExecArgv(t)
		if traced {
			recordingOn(o)
			o.InvocationScope = NewInvocationScope()
			o.initPerf("yolo-ws-abcd1234")
			o.initLauncherTrace()
			if o.InvocationScope.claim == nil {
				t.Fatalf("no launcher trace was acquired:\n%s", stderr)
			}
		}
		rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
			stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil)
		if rc != 0 {
			t.Fatalf("rc=%d\n%s", rc, stderr)
		}
		got := argv()
		values, before := carrierOf(got)
		if len(values) != 1 || before != "yolo-ws-abcd1234" {
			t.Fatalf("traced=%v: want one carrier right before the container, got %v before %q in %q", traced, values, before, got)
		}
		if !traced {
			if values[0] != "" {
				t.Errorf("an untraced attach carried %q, want the empty carrier", values[0])
			}
			continue
		}
		o.InvocationScope.Finalize()
		tt := readLaunchTrace(t)
		var execSpan string
		for _, m := range tt.lane {
			if m["k"] == "start" && m["name"] == "attach.exec" {
				execSpan, _ = m["span"].(string)
			}
		}
		if want := "00-" + filepath.Base(tt.dir) + "-" + execSpan + "-01"; execSpan == "" || values[0] != want {
			t.Errorf("attach carrier %q, want the attach.exec span's %q", values[0], want)
		}
		_ = os.RemoveAll(paths.TracesDir())
	}
}
