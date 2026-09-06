package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// The timing gate is a pure OR of the flag and the two host-process env
// opt-ins — pinned as a table so a fourth spelling cannot appear without this
// test noticing (design D1/D5).
func TestTimingEnabledGate(t *testing.T) {
	cases := []struct {
		name string
		flag bool
		env  map[string]string
		want bool
	}{
		{"off by default", false, nil, false},
		{"flag", true, nil, true},
		{"YOLO_TIMING", false, map[string]string{paths.TimingEnv: "1"}, true},
		{"YOLO_VERBOSE", false, map[string]string{paths.VerboseEnv: "1"}, true},
		{"empty env is off", false, map[string]string{paths.TimingEnv: ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{Timing: tc.flag, Getenv: func(k string) string { return tc.env[k] }}
			if got := o.timingEnabled(); got != tc.want {
				t.Errorf("timingEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// initPerf's contract: nothing constructed and no file when the gate is off;
// collector + <ws>/.yolo/host-perf.log + live slow-span notice when it is on.
// The file-existence half is the call-site pin for the sink location (D2).
func TestInitPerfConstructsOnlyWhenEnabled(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = false
	o.initPerf("yolo-ws-test0000")
	if o.Perf != nil {
		t.Fatal("initPerf constructed a collector while timing is off")
	}
	if _, err := os.Stat(filepath.Join(ws, ".yolo", HostPerfLogName)); !os.IsNotExist(err) {
		t.Fatal("timing-off launch created host-perf.log")
	}

	o.Timing = true
	// Replace both streams post-fillDefaults (the assemble_notices_test
	// pattern): goldenOptions leaves the process's real streams installed.
	var out, errb bytes.Buffer
	o.Stdout, o.Stderr = &out, &errb
	o.initPerf("yolo-ws-test0000")
	if o.Perf == nil {
		t.Fatal("initPerf did not construct a collector with timing on")
	}

	// A fast span lands in the file; a slow one also names itself on stderr.
	fast := o.Perf.Span("fast")
	fast.End()
	slow := o.Perf.Span("slow")
	time.Sleep(perf.SlowSpanThreshold + 50*time.Millisecond)
	slow.End()

	got, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"jail=yolo-ws-test0000", "start  fast", "end    slow  dur="} {
		if !strings.Contains(string(got), want) {
			t.Errorf("host-perf.log missing %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(errb.String(), "slow took") {
		t.Errorf("slow-span notice missing from stderr: %q", errb.String())
	}
	if strings.Contains(errb.String(), "fast took") {
		t.Errorf("fast span crossed the notice threshold: %q", errb.String())
	}
}

// THE PIN the old single-Total block's comment said the next toucher owed: the
// normal-exit shutdown chain, extracted into teardownAfterExit exactly so this
// is unit-reachable. Deleting any span call site inside the chain fails this
// test — the shape AGENTS.md records this repo has shipped five times (a
// callee pinned while the call site went unpinned).
func TestTeardownChainEmitsShutdownSpans(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.Timing = true
	o.initPerf("yolo-ws-test0000")

	socketsDir := t.TempDir() // non-empty so stopLoopholes runs its full body
	o.teardownAfterExit(nil, "", nil, socketsDir, "yolo-ws-test0000", "podman", 0)

	var report bytes.Buffer
	o.Perf.Report(&report, time.Now())
	got := report.String()
	// In order, the exact names the chain owns.
	for i, want := range []string{
		"shutdown.cleanup_port_forwarding",
		"shutdown.stop_loopholes",
		"shutdown.container_check",
		"shutdown.capture_config",
		"shutdown.oom_check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing span %q; got:\n%s", want, got)
		}
		_ = i
	}
	fileBytes, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes), "end    shutdown.oom_check") {
		t.Errorf("file sink missing the chain's spans; got:\n%s", fileBytes)
	}
}

// A timing-off teardown writes no file and emits nothing — the off path must
// stay byte-silent, not merely unreported.
func TestTeardownChainSilentWhenOff(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.teardownAfterExit(nil, "", nil, "", "yolo-ws-test0000", "podman", 0)
	if _, err := os.Stat(filepath.Join(ws, ".yolo", HostPerfLogName)); !os.IsNotExist(err) {
		t.Fatal("timing-off teardown created host-perf.log")
	}
}

// parseDieAndCleanup against real `podman events --format '{{.Time}}
// {{.Status}}'` shapes: the first die wins, the last cleanup wins, garbage
// and empty logs are skips/not-found, never errors.
func TestParseDieAndCleanup(t *testing.T) {
	die, cleanup, ok := parseDieAndCleanup("1757152800.500 create\n" +
		"1757152810.250 die\n" +
		"1757152813.750 cleanup\n")
	if !ok {
		t.Fatal("die not found")
	}
	if die.UnixMilli() != 1757152810250 {
		t.Errorf("die = %v, want unix-milli 1757152810250", die.UnixMilli())
	}
	if cleanup == nil || cleanup.Sub(die) != 3500*time.Millisecond {
		t.Errorf("cleanup = %v, want die+3.5s", cleanup)
	}

	// Several dies (a stop-timeout escalation): the FIRST is the one that
	// bounds Window A.
	die, _, ok = parseDieAndCleanup("1757152810.250 die\n1757152815.000 die\n")
	if !ok || die.Unix() != 1757152810 {
		t.Errorf("first die must win; got %v ok=%v", die, ok)
	}

	if _, _, ok := parseDieAndCleanup(""); ok {
		t.Error("empty log must be not-found (the rootless file-backend case)")
	}
	if _, _, ok := parseDieAndCleanup("garbage line\n1.5 not-a-status\n"); ok {
		t.Error("garbage must be skipped, not fatal")
	}
}

// The attribution's contract against a fake Exec: a good fixture renders the
// die→exit gap, and every failure mode (not podman, exec failed, timeout,
// nonzero rc, no events) renders nothing — silence is the failure mode.
func TestAttributeWindowA(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = true
	o.initPerf("yolo-ws-test0000")

	podmanExit := time.Unix(1757152830, 0).UTC() // 20s after the die below
	fixture := "1757152800.5 start\n1757152810.25 die\n1757152825.0 cleanup\n"

	cases := []struct {
		name string
		rt   string
		res  ExecResult
		want bool
	}{
		{"good fixture", "podman", ExecResult{Stdout: fixture, RC: 0, Ran: true}, true},
		{"not podman", "container", ExecResult{Stdout: fixture, RC: 0, Ran: true}, false},
		{"exec failed", "podman", ExecResult{Ran: false}, false},
		{"timeout", "podman", ExecResult{Timeout: true, Ran: true}, false},
		{"nonzero rc", "podman", ExecResult{RC: 1, Ran: true}, false},
		{"no events", "podman", ExecResult{RC: 0, Ran: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if argv[0] != "podman" || argv[1] != "events" {
					t.Errorf("unexpected probe argv %v", argv)
				}
				for _, a := range argv {
					if a == "--until" {
						t.Logf("bounded: %v", argv)
					}
				}
				return tc.res
			}
			line, ok := o.attributeWindowA("yolo-ws-test0000", tc.rt, podmanExit.Add(-time.Minute), podmanExit)
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v (line %q)", ok, tc.want, line)
			}
			if ok {
				if !strings.Contains(line, "19.750s") { // 1757152830 - 1757152810.25
					t.Errorf("gap wrong in %q", line)
				}
				if !strings.Contains(line, "cleanup event") {
					t.Errorf("cleanup delta missing in %q", line)
				}
			}
		})
	}
}
