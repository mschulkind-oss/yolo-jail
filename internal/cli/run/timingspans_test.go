package run

import (
	"bytes"
	"fmt"
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

// THE OVERLAY, pinned. Once the container is spawned the pty is attached and
// BOTH host streams are the container's, so a slow-span notice from the
// launcher's own goroutine lands on top of the agent's TUI — which is exactly
// what `housekeeping.slot took 62.891s` did, a minute into a live session, as
// the last line of the maintainer's launch.log on 2026-09-19. housekeepingNote
// already refuses to write to the terminal for this reason; the slot is ALSO a
// span, and the notice sink was the second door.
//
// Both directions matter, so both are asserted here: silence inside the window,
// and the notice back on stderr the moment the child returns the terminal —
// naming a slow quit is the whole reason the sink exists, and deleting the sink
// altogether must fail this test rather than look like the fix.
func TestSlowSpanNoticeIsSilentWhileTheContainerHoldsTheTerminal(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = true
	var out, errb bytes.Buffer
	o.Stdout, o.Stderr = &out, &errb
	o.initPerf("yolo-ws-test0000")

	slow := func(name string) {
		sp := o.Perf.Span(name)
		time.Sleep(perf.SlowSpanThreshold + 50*time.Millisecond)
		sp.End()
	}

	// The window: spawned → exited is when the agent owns the screen.
	o.Perf.Mark("child.spawned")
	slow("housekeeping.slot")
	if strings.Contains(errb.String(), "housekeeping.slot took") {
		t.Errorf("a slow-span notice reached the terminal while the container held it — "+
			"it lands on top of the agent's TUI (housekeeping.go property 2):\n%s", errb.String())
	}

	// The signal arm restores termios BEFORE onTerminate runs, so the
	// terminate.* spans it times must still be able to name themselves.
	o.Perf.Mark("child.termios_restored")
	slow("terminate.stop_jail")
	if !strings.Contains(errb.String(), "terminate.stop_jail took") {
		t.Errorf("no slow-span notice after the child returned the terminal — a slow quit "+
			"is the one thing this sink exists to name:\n%s", errb.String())
	}

	// Silence is never loss: the file has both, as it always did.
	got, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"end    housekeeping.slot  dur=", "end    terminate.stop_jail  dur="} {
		if !strings.Contains(string(got), want) {
			t.Errorf("host-perf.log missing %q — the suppressed notice must still be recorded:\n%s", want, got)
		}
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
		"shutdown.clear_tracking",
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

// parseDieAndCleanup against real `podman events --format '{{.TimeNano}}
// {{.Status}}'` shapes: the first death wins, the last teardown event wins,
// garbage and empty logs are skips/not-found, never errors.
//
// ⚠ THE FIXTURES SPELL THE STATUS `died` AND THE TERMINAL EVENT `remove`, and
// both spellings are the bug this test now exists to keep fixed. This parser
// matched Docker's `die` from its first commit — podman emits `died` — and it
// looked for a `cleanup` event that a `--rm` container never produces, which is
// how every jail runs. Measured against podman 5.8.6's own events.log,
// 2026-09-19. A fixture rewritten to `die`/`cleanup` would pass while the
// production query attributed nothing, which is the state that shipped.
func TestParseDieAndCleanup(t *testing.T) {
	die, cleanup, ok := parseDieAndCleanup("1757152800500000000 create\n" +
		"1757152810250000000 died\n" +
		"1757152813750000000 remove\n")
	if !ok {
		t.Fatal("death not found")
	}
	if die.UnixMilli() != 1757152810250 {
		t.Errorf("die = %v, want unix-milli 1757152810250", die.UnixMilli())
	}
	if cleanup == nil || cleanup.Sub(die) != 3500*time.Millisecond {
		t.Errorf("last teardown event = %v, want die+3.5s", cleanup)
	}

	// `cleanup` is still honoured, for a configuration that emits one.
	if _, c, ok := parseDieAndCleanup("1757152810250000000 died\n" +
		"1757152812000000000 cleanup\n"); !ok || c == nil {
		t.Errorf("a cleanup event must still count as the terminal one; got %v ok=%v", c, ok)
	}

	// Several deaths (a stop-timeout escalation): the FIRST is the one that
	// bounds Window A.
	die, _, ok = parseDieAndCleanup("1757152810250000000 died\n1757152815000000000 died\n")
	if !ok || die.Unix() != 1757152810 {
		t.Errorf("first death must win; got %v ok=%v", die, ok)
	}

	// Docker's spelling must NOT match: if it did, this parser's contract would
	// be "whatever the fixture says" rather than "what podman emits".
	if _, _, ok := parseDieAndCleanup("1757152810250000000 die\n"); ok {
		t.Error("`die` is Docker's status; podman emits `died` and only that must match")
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

	podmanExit := time.Unix(1757152830, 0).UTC() // 20s after the death below
	fixture := "1757152800500000000 start\n" +
		"1757152810250000000 died\n" +
		"1757152825000000000 remove\n"

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
				return tc.res
			}
			res := o.attributeWindowA("yolo-ws-test0000", tc.rt, podmanExit.Add(-time.Minute), podmanExit)
			line, ok := res.line, res.ok
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v (line %q)", ok, tc.want, line)
			}
			if ok {
				if !strings.Contains(line, "19.750s") { // 1757152830 - 1757152810.25
					t.Errorf("gap wrong in %q", line)
				}
				if !strings.Contains(line, "teardown event") {
					t.Errorf("terminal-event delta missing in %q", line)
				}
			}
		})
	}
}

// THE QUERY'S BOUND IS `--stream=false`, AND `--until` MUST NOT BE ON THE ARGV
// AT ALL. This pins the third attempt at one constraint, and the two it
// replaces are worth the paragraph because each looked like the answer:
//
//  1. `--until podmanExited+5s` — a bound in the FUTURE makes podman wait for
//     that wall-clock moment, stalling every timed shutdown for the full exec
//     timeout (measured 3.002s per launch, 2026-09-06).
//  2. `--until <now>` at RFC3339 SECOND granularity — truncation put the bound
//     just before the death it was hunting, so every fast Window A reported
//     no_die (2026-09-13).
//  3. `--until <now>` at any precision — MEASURED on podman 5.8.6, 2026-09-19:
//     an `--until` at or before now makes podman stop at EOF immediately and
//     return a racy PREFIX of the log, usually empty. The shipped argv returned
//     nothing 3 times out of 3 against a container whose death was in the log;
//     the same argv with `--stream=false` and no `--until` returned all six of
//     its events in 0.012s, 3 for 3. That is what the maintainer's 8-of-8
//     `no_die` marks were: the feature built to price this window had never
//     priced it once.
//
// So the bound is the flag that MEANS "return what you have and exit", and
// nothing about it is a timestamp the reader has to interpret. The format must
// stay `{{.TimeNano}}`: `{{.Time}}` renders integer seconds, which truncates the
// death downward and inflates the measured window by up to a second.
func TestWindowAQueryIsBoundedByStreamFalseNotUntil(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = true
	o.initPerf("yolo-ws-test0000")

	var argv []string
	o.Exec = func(a []string, _ string, _ []string, _ time.Duration) ExecResult {
		argv = a
		return ExecResult{Ran: true}
	}
	// A child that exited a minute ago — the ordinary case.
	o.attributeWindowA("yolo-ws-test0000", "podman", time.Now().Add(-2*time.Minute), time.Now().Add(-time.Minute))

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--stream=false") {
		t.Errorf("no --stream=false on %q: `podman events --since` alone STREAMS, and a "+
			"diagnostics query that never returns is the failure this feature exists to end", joined)
	}
	for _, a := range argv {
		if a == "--until" {
			t.Errorf("--until is back on %q. An --until at or before now returns a racy, "+
				"usually EMPTY prefix of the event log (measured, podman 5.8.6); one in the "+
				"future BLOCKS until that moment. --stream=false is the bound.", joined)
		}
	}
	if !strings.Contains(joined, "--since") {
		t.Errorf("no --since on %q: the query would read the whole event log", joined)
	}
	if !strings.Contains(joined, "{{.TimeNano}}") {
		t.Errorf("format is not {{.TimeNano}} in %q: {{.Time}} is integer SECONDS, which "+
			"truncates the death downward and inflates every measured window", joined)
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
		{"no died event", "podman", ExecResult{Ran: true, Stdout: "1757152800500000000 start\n"}, "no container `died` event"},
		{"timeout", "podman", ExecResult{Ran: true, Timeout: true}, "did not answer within"},
		{"could not run", "podman", ExecResult{Ran: false}, "could not be run"},
		{"nonzero rc", "podman", ExecResult{Ran: true, RC: 125}, "exited 125"},
		{"not podman: silent", "container", ExecResult{Ran: true}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return tc.res }
			res := o.attributeWindowA("yolo-ws-test0000", tc.rt, exited.Add(-time.Minute), exited)
			line, ok, why := res.line, res.ok, res.reason
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

// TestInitPerfPublishesTheCollectorToTheCaller closes a measurement hole that
// LOOKED instrumented. `yolo run`'s deferred title restore spans itself as
// process.title_restore on the front door's own copy of Options — and Options
// crosses the launchRunPipeline seam BY VALUE, so a collector built inside the
// pipeline was invisible there. Span on a nil *perf.Log is a deliberate silent
// no-op, so the span never fired and design H6 stayed unmeasured while every
// reader of the code saw a span.
//
// The assertion is that a CALLER's holder ends up non-nil, because that is the
// only thing standing between "spanned" and "spanned on nil".
func TestInitPerfPublishesTheCollectorToTheCaller(t *testing.T) {
	ws := t.TempDir()
	ref := &PerfRef{}
	o := &Options{Workspace: ws, PerfRef: ref}
	fillDefaults(o)
	o.Getenv = func(k string) string {
		if k == paths.TimingEnv {
			return "1"
		}
		return ""
	}

	o.initPerf("yolo-ws-test0000")

	if o.Perf == nil {
		t.Fatal("the gate was on and no collector was built — this test cannot say anything")
	}
	if ref.Log == nil {
		t.Fatal("initPerf did not publish the collector to the caller's ref. Anything the front " +
			"door spans after Run returns — the title restore, design H6 — then spans a nil " +
			"collector and records nothing, silently.")
	}
	if ref.Log != o.Perf {
		t.Error("the caller got a DIFFERENT collector; its spans would land in another sink")
	}
	// And the span it exists for actually records.
	if sp := ref.Log.Span("process.title_restore"); sp == nil {
		t.Error("the published collector produced a nil span while timing is on")
	}
}

// TestInitPerfWithNoRefIsFine: a nil holder is the normal case for every caller
// that does not span past Run, and must not be a special case at the call site.
func TestInitPerfWithNoRefIsFine(t *testing.T) {
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.initPerf("yolo-ws-test0000") // must not panic
}

// windowAFixture renders a `podman events --format '{{.TimeNano}} {{.Status}}'`
// log for a container that died `ago` before now and was removed 300ms later.
// Relative to now, not a frozen unix stamp, because the gap Window A measures is
// (child.exited mark) - (died event) and the mark is real-clock. The statuses are
// the ones podman actually emits for a `--rm` container: `died`, then `remove`.
func windowAFixture(ago time.Duration) string {
	die := time.Now().Add(-ago)
	return fmt.Sprintf("%d start\n%d died\n%d remove\n",
		die.Add(-time.Second).UnixNano(), die.UnixNano(),
		die.Add(300*time.Millisecond).UnixNano())
}

// quietRecordingOptions is a launch with the PERSISTENT opt-in on and neither
// explicit flag: it records everything and prints nothing. Every test below
// that pins the gate move needs exactly this shape, because a --timing launch
// cannot tell the old wiring from the new one.
func quietRecordingOptions(t *testing.T, ws, home string) *Options {
	t.Helper()
	o := goldenOptions(ws, home)
	// The field fillDefaults would set from `perf_logging: true`. Set directly
	// so the test does not depend on the developer's own config file — the same
	// reason PerfLoggingConfig is a seam at all.
	o.perfLoggingOn = true
	o.initPerf("yolo-ws-test0000")
	if !o.timingRecording() {
		t.Fatal("fixture wrong: this launch must RECORD")
	}
	if o.timingReporting() {
		t.Fatal("fixture wrong: this launch must NOT print")
	}
	return o
}

// THE CALL-SITE PIN for Window A, and it is deliberately a QUIET launch.
//
// Window A attribution used to run inside emitTimingReportLocked, so it fired
// only for a launch that typed --timing — and Window A is the one span a user
// cannot predict wanting, because the way you find out it was slow is by
// waiting through it. Every --timing test in this file passes with
// recordWindowA deleted from teardownAfterExit, because the report would run
// the query itself. This test does not: a quietly-recording launch never
// reaches the report at all, so it fails the moment that call site goes.
func TestQuietTeardownRecordsWindowA(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	o.Perf.Mark("child.exited") // the arm's precondition, as the proxy leaves it
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0, Stdout: windowAFixture(1500 * time.Millisecond)}
	}

	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", 0)

	ev, ok := o.Perf.LastEvent("shutdown.window_a")
	if !ok {
		t.Fatal("shutdown.window_a was not recorded — a quiet launch still cannot price Window A")
	}
	if ev.Dur < 1300*time.Millisecond || ev.Dur > 1900*time.Millisecond {
		t.Errorf("Window A recorded as %v, want ~1.5s from the fixture", ev.Dur)
	}
	fileBytes, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes), "end    shutdown.window_a  dur=1.") {
		t.Errorf("the number did not reach the file; got:\n%s", fileBytes)
	}
}

// The blank is recorded too. A quiet launch prints no reason line, so without
// this the file would say nothing at all about a query that ran and found
// nothing — the "an observability feature that cannot explain its own blank"
// rule, applied to the half of it that does not print.
func TestQuietTeardownRecordsWindowAFailureClass(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	o.Perf.Mark("child.exited")
	// Ran, exited 0, and holds no death — the ordinary rootless file-backend case.
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0, Stdout: "1757152800500000000 start\n"}
	}

	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", 0)

	fileBytes, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes), "mark   shutdown.window_a_unattributed.no_die") {
		t.Errorf("the failure class did not reach the file; got:\n%s", fileBytes)
	}
	if _, ok := o.Perf.LastEvent("shutdown.window_a"); ok {
		t.Error("recorded a duration for a query that found no die event")
	}
}

// One query per launch, even though the arm records and the report renders.
// The report reads what the arm recorded; if it ever asks podman again, a
// signal-path shutdown pays the exec twice and the two answers can disagree.
func TestWindowAQueriedOncePerLaunch(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.Timing = true // the printing launch: arm records, report renders
	o.initPerf("yolo-ws-test0000")
	o.Perf.Mark("child.exited")
	queries := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		// The chain also runs the liveness `podman ps`; count only the events query.
		if len(argv) > 1 && argv[1] == "events" {
			queries++
			return ExecResult{Ran: true, RC: 0, Stdout: windowAFixture(2 * time.Second)}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	var errbuf bytes.Buffer
	o.Stderr = &errbuf

	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", 0)
	o.emitTimingReport(0, "yolo-ws-test0000", "podman")
	o.emitTimingReport(0, "yolo-ws-test0000", "podman") // the interleaving arm

	if queries != 1 {
		t.Errorf("podman events ran %d times, want exactly 1", queries)
	}
	got := errbuf.String()
	if !strings.Contains(got, "Window A (container died → podman exit)") {
		t.Errorf("report lost the Window A line; got:\n%s", got)
	}
	// The recorded event is inside the TABLE too, which is the second half of
	// what the gate move buys: a row, not only a footnote.
	if !strings.Contains(got, "shutdown.window_a") {
		t.Errorf("report table missing the shutdown.window_a row; got:\n%s", got)
	}
}

// The attach arm has no container death to attribute — `podman exec` into a
// jail that keeps running — so it must not query at all. It used to, and got
// back "no die event", which is a blank explaining a question nobody asked.
func TestWindowASilentWithoutAChildExit(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := quietRecordingOptions(t, ws, home)
	queries := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			queries++
		}
		return ExecResult{Ran: true}
	}

	o.recordWindowA("yolo-ws-test0000", "podman") // no child.exited mark exists

	if queries != 0 {
		t.Errorf("queried podman events %d times with no child.exited mark", queries)
	}
	if _, ok := o.Perf.LastEvent("shutdown.window_a"); ok {
		t.Error("recorded a Window A that never happened")
	}
}
