package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// THE TWO GATES, side by side — the whole of D12 as a table, so neither the
// recording set nor the (much smaller) reporting set can gain a spelling
// without this test noticing.
//
// The rule the rows encode: EVERY opt-in records; only an EXPLICIT
// per-invocation flag (--timing, --verbose/-v) prints. The env spellings are
// what a shell profile exports and the config key is always-on, so both record
// in silence — the pair of rows that were a single "enabled" answer until D12.
func TestTimingGatesSplitRecordingFromReporting(t *testing.T) {
	cases := []struct {
		name          string
		flag          bool
		verbose       bool
		perfLoggingOn bool
		env           map[string]string
		record        bool
		report        bool
	}{
		{name: "off by default"},
		{name: "--timing flag", flag: true, record: true, report: true},
		{name: "--verbose flag", verbose: true, record: true, report: true},
		{name: "YOLO_TIMING records quietly",
			env: map[string]string{paths.TimingEnv: "1"}, record: true},
		{name: "YOLO_VERBOSE records quietly",
			env: map[string]string{paths.VerboseEnv: "1"}, record: true},
		{name: "perf_logging records quietly", perfLoggingOn: true, record: true},
		{name: "empty env is off", env: map[string]string{paths.TimingEnv: ""}},
		{name: "flag wins over a quiet env", flag: true,
			env: map[string]string{paths.TimingEnv: "1"}, record: true, report: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{
				Timing:        tc.flag,
				Verbose:       tc.verbose,
				perfLoggingOn: tc.perfLoggingOn,
				Getenv:        func(k string) string { return tc.env[k] },
			}
			if got := o.timingRecording(); got != tc.record {
				t.Errorf("timingRecording() = %v, want %v", got, tc.record)
			}
			if got := o.timingReporting(); got != tc.report {
				t.Errorf("timingReporting() = %v, want %v", got, tc.report)
			}
		})
	}
}

// THE CALL-SITE PIN for the split, at the one place a user sees it: the exit
// report. A launch that recorded WITHOUT being asked to report prints one dim
// line naming the file and NOTHING else — no header, no span table, no Window A
// query — while an explicit --timing launch prints the whole report. Delete the
// quiet branch in emitTimingReport and the first half fails; delete the report
// call and the second half does.
func TestQuietRecordingPrintsOnlyTheFileLine(t *testing.T) {
	newLaunch := func(t *testing.T, ws string, explicit bool) (*Options, *bytes.Buffer) {
		t.Helper()
		o := goldenOptions(ws, t.TempDir())
		// The persistent opt-in, exactly as fillDefaults would have folded it.
		o.perfLoggingOn = true
		o.Timing = explicit
		var errb bytes.Buffer
		o.Stderr = &errb
		o.initPerf("yolo-ws-test0000")
		if o.Perf == nil {
			t.Fatal("a recording launch constructed no collector")
		}
		sp := o.Perf.Span("launch.thing")
		sp.End()
		return o, &errb
	}

	t.Run("quiet", func(t *testing.T) {
		ws := t.TempDir()
		o, errb := newLaunch(t, ws, false)
		o.emitTimingReport(0, "yolo-ws-test0000", "podman")
		got := errb.String()
		for _, unwanted := range []string{"Host-side timing", "launch.thing", "Total:"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("quiet launch printed %q; stderr:\n%s", unwanted, got)
			}
		}
		if !strings.Contains(got, filepath.Join(ws, ".yolo", HostPerfLogName)) {
			t.Errorf("quiet launch never named the file it wrote; stderr:\n%s", got)
		}
		if strings.Count(got, "\n") != 1 {
			t.Errorf("quiet launch printed %d lines, want exactly 1:\n%s",
				strings.Count(got, "\n"), got)
		}
		// The recording half is untouched by the silence.
		body, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
		if err != nil {
			t.Fatalf("quiet launch wrote no host-perf.log: %v", err)
		}
		if !strings.Contains(string(body), "end    launch.thing") {
			t.Errorf("quiet launch recorded no spans; file:\n%s", body)
		}
	})

	t.Run("explicit --timing", func(t *testing.T) {
		o, errb := newLaunch(t, t.TempDir(), true)
		o.emitTimingReport(0, "yolo-ws-test0000", "podman")
		got := errb.String()
		for _, want := range []string{"Host-side timing", "launch.thing", "host file:"} {
			if !strings.Contains(got, want) {
				t.Errorf("explicit launch missing %q; stderr:\n%s", want, got)
			}
		}
	})
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
			line, ok, _ := o.attributeWindowA("yolo-ws-test0000", tc.rt, podmanExit.Add(-time.Minute), podmanExit)
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

// THE 3-SECOND BUG, pinned. `podman events --until <future>` does not return
// what it has and exit — it WAITS until that wall-clock moment. The first
// shipped version passed podmanExited+5s, so every timed shutdown stalled for
// the full exec timeout (measured 3.002s per launch, vs 0.02s with an --until
// of now). Any future offset reintroduced here fails this test.
func TestWindowAUntilIsNeverInTheFuture(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = true
	o.initPerf("yolo-ws-test0000")

	var until string
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		for i, a := range argv {
			if a == "--until" && i+1 < len(argv) {
				until = argv[i+1]
			}
		}
		return ExecResult{Ran: true}
	}
	// A child that exited a minute ago — the ordinary case.
	o.attributeWindowA("yolo-ws-test0000", "podman", time.Now().Add(-2*time.Minute), time.Now().Add(-time.Minute))

	if until == "" {
		t.Fatal("no --until on the events argv; the query would stream unbounded")
	}
	got, err := time.Parse(time.RFC3339, until)
	if err != nil {
		t.Fatalf("--until %q is not RFC3339: %v", until, err)
	}
	// Second-granularity RFC3339 rounds down, so "now" can format up to a
	// second behind; anything beyond that is a real future wait.
	if slack := time.Until(got); slack > time.Second {
		t.Errorf("--until is %v in the future (%s) — podman will BLOCK until then, "+
			"adding that wait to every timed shutdown", slack.Round(time.Millisecond), until)
	}
}

// THE CALL-SITE PIN for the persistent opt-in: fillDefaults must read
// `perf_logging` once, into its own field, so recording turns on WITHOUT the
// launch looking like the user typed --timing. Delete the read and the config
// key stops recording; fold it back into o.Timing (what it did until D12) and
// the reporting column goes wrong, which is the noise this split removed.
func TestPerfLoggingConfigRecordsWithoutReporting(t *testing.T) {
	cases := []struct {
		name       string
		flag       bool
		configSays bool
		record     bool
		report     bool
	}{
		{"neither", false, false, false, false},
		{"config only", false, true, true, false},
		{"flag only", true, false, true, true},
		{"both", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.TimingEnv, "")
			t.Setenv(paths.VerboseEnv, "")
			o := &Options{
				Timing:            tc.flag,
				PerfLoggingConfig: func() bool { return tc.configSays },
			}
			fillDefaults(o)
			if o.Timing != tc.flag {
				t.Errorf("fillDefaults mutated Timing to %v — the flag must stay the "+
					"record of what the USER typed", o.Timing)
			}
			if got := o.timingRecording(); got != tc.record {
				t.Errorf("timingRecording() = %v, want %v", got, tc.record)
			}
			if got := o.timingReporting(); got != tc.report {
				t.Errorf("timingReporting() = %v, want %v", got, tc.report)
			}
		})
	}
}

// fillDefaults must install the REAL reader when none is injected. Asserting
// only that the seam is non-nil is NOT enough — that passes with the default
// wired to a stub returning false, which is the config key silently inert in
// production while every test that injects a seam stays green (AGENTS.md's
// callee-pinned/call-site-unpinned shape). So this writes an actual user config
// and requires the default to have read it.
func TestPerfLoggingConfigDefaultsToTheRealReader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.UserLayerEnv, "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"),
		[]byte(`{"perf_logging": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(paths.TimingEnv, "")
	t.Setenv(paths.VerboseEnv, "")

	o := &Options{} // NOTHING injected: the production wiring is under test
	fillDefaults(o)
	if o.PerfLoggingConfig == nil {
		t.Fatal("fillDefaults left PerfLoggingConfig nil; the config key would never be read")
	}
	if !o.timingRecording() {
		t.Error(`fillDefaults did not honor "perf_logging": true from the user config — ` +
			"the seam is wired to something that is not config.PerfLoggingEnabled")
	}
	if o.timingReporting() {
		t.Error("the user config turned REPORTING on; the persistent opt-in records silently (D12)")
	}
}

// Attribution must EXPLAIN its blanks. Two real-host launches produced no
// Window A line and no way to tell whether the query failed, timed out, or
// found nothing — so every non-applicable outcome now carries a reason, and
// only the wrong-runtime case stays mute (there is nothing to say).
func TestWindowAExplainsWhyItHasNothing(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = true
	o.initPerf("yolo-ws-test0000")
	exited := time.Now()

	cases := []struct {
		name    string
		rt      string
		res     ExecResult
		wantWhy string
	}{
		{"no die event", "podman", ExecResult{Ran: true, Stdout: "1757152800.5 start\n"}, "no container `die` event"},
		{"timeout", "podman", ExecResult{Ran: true, Timeout: true}, "did not answer within"},
		{"could not run", "podman", ExecResult{Ran: false}, "could not be run"},
		{"nonzero rc", "podman", ExecResult{Ran: true, RC: 125}, "exited 125"},
		{"not podman: silent", "container", ExecResult{Ran: true}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return tc.res }
			line, ok, why := o.attributeWindowA("yolo-ws-test0000", tc.rt, exited.Add(-time.Minute), exited)
			if ok || line != "" {
				t.Fatalf("expected no attribution, got %q", line)
			}
			if tc.wantWhy == "" {
				if why != "" {
					t.Errorf("wrong-runtime case must stay silent, got %q", why)
				}
				return
			}
			if !strings.Contains(why, tc.wantWhy) {
				t.Errorf("why = %q, want it to mention %q", why, tc.wantWhy)
			}
		})
	}
}

// The call-site pin for the image-load split: run must hand its collector to
// AutoLoadImage, or the phases inside a 175-second image load stay invisible
// and `launch.auto_load_image` remains one unactionable number.
func TestImageLoadReceivesTheCollector(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(ws, home)
	o.Timing = true
	o.initPerf("yolo-ws-test0000")

	var got *perf.Log
	seen := false
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		got, seen = opts.Perf, true
		return image.LoadResult{OK: true, Ref: "localhost/yolo-jail:test"}
	}
	o.autoLoadImage(newConfig(), "podman", "/repo", storePackagesPlan{})
	if !seen {
		t.Fatal("autoLoadImage did not reach the loader")
	}
	if got != o.Perf {
		t.Error("AutoLoadOptions.Perf is not the launch's collector — image phases would be unspanned")
	}
}
