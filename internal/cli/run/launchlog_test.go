package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// launchlog_test.go covers docs/design/report-tiers.md §4.7's *Persist the launcher's
// half*: the launcher's own output, which used to exist only on a terminal, lands in
// <workspace>/.yolo/launch.log beside the entrypoint's boot.log.

// readLaunchLog returns the log's content, or fails.
func readLaunchLog(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".yolo", LaunchLogName))
	if err != nil {
		t.Fatalf("reading the launch log: %v", err)
	}
	return string(b)
}

// TestLaunchIsRecordedInTheWorkspaceLog is THE CALL-SITE PIN, and it is the only test
// here that fails if `attachLaunchLog(o)` is deleted from Run.
//
// Every other test in this file drives the attach directly and would stay green against
// a Run that never calls it — the callee-pinned/call-site-unpinned shape AGENTS.md
// records this repo as having shipped five times. What it asserts is the property the
// feature exists for: a line the LAUNCHER printed, in the file, after a real Run.
func TestLaunchIsRecordedInTheWorkspaceLog(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	got := readLaunchLog(t, ws)
	if !strings.Contains(got, launchRunPrefix) {
		t.Fatalf("the launch log has no run header:\n%s", got)
	}
	// A line the launcher actually printed this run, not merely the header: a log
	// holding only its own preamble is the "complete, correct-looking, permanently
	// empty" failure attachLaunchLog's docstring is shaped to prevent.
	printed := stdout.String() + stderr.String()
	line := firstPrintedLine(printed)
	if line == "" {
		t.Fatalf("this launch printed nothing, so the test cannot tell a wired tee "+
			"from an unwired one\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(got, line) {
		t.Errorf("the launcher printed %q and the launch log does not carry it:\n%s", line, got)
	}
	if !strings.Contains(got, "rc=0") {
		t.Errorf("the run block does not record how the launch ended:\n%s", got)
	}
}

// firstPrintedLine returns the first non-empty line of a launch's output, stripped of
// ANSI, or "".
func firstPrintedLine(out string) string {
	for _, l := range strings.Split(string(stripANSI([]byte(out))), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			return s
		}
	}
	return ""
}

// TestLaunchLogRecordsBothStreamsAndLeavesTheTerminalBytesAlone is the tee's contract,
// both halves at once.
//
// The stripping half matters because color is resolved against the real terminal rather
// than against the writer (Options.pr consults IsTTYStdout), so a styled line reaches
// the tee already rendered to escape sequences — and the terminal must still receive
// them, byte for byte, because the config-change prompt writes a bare `[y/N] ` through
// this same stdout.
func TestLaunchLogRecordsBothStreamsAndLeavesTheTerminalBytesAlone(t *testing.T) {
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := &Options{Workspace: ws, Stdout: &stdout, Stderr: &stderr, Getenv: func(string) string { return "" }}

	l := attachLaunchLog(o)
	if l == nil {
		t.Fatal("attachLaunchLog returned nil for a writable workspace")
	}
	fmt.Fprint(o.Stdout, "\x1b[1mbuilding the image\x1b[0m\n")
	fmt.Fprint(o.Stderr, "\x1b[2mFlake source: /workspace\x1b[0m\n")
	fmt.Fprint(o.Stdout, "Accept these workspace config changes? [y/N] ")
	l.finish(0)

	if got := stdout.String(); got != "\x1b[1mbuilding the image\x1b[0m\n"+
		"Accept these workspace config changes? [y/N] " {
		t.Errorf("the terminal copy was altered: %q", got)
	}
	if got := stderr.String(); got != "\x1b[2mFlake source: /workspace\x1b[0m\n" {
		t.Errorf("the terminal copy was altered: %q", got)
	}

	got := readLaunchLog(t, ws)
	for _, want := range []string{
		"building the image",                            // stdout
		"Flake source: /workspace",                      // stderr
		"Accept these workspace config changes? [y/N] ", // a write with no newline
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch log is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("the log copy carries ANSI escapes:\n%q", got)
	}
}

// TestLaunchLogHeaderNamesTheVersionAndTheEnvironmentHatches: a run block read a week
// later has to be self-describing. The version is here because it is the one launch line
// the tee cannot see — the banner is printed before Run — and the hatches because each
// one changes what a launch DOES without appearing in any line it prints.
func TestLaunchLogHeaderNamesTheVersionAndTheEnvironmentHatches(t *testing.T) {
	ws := t.TempDir()
	env := map[string]string{"YOLO_RUNTIME": "podman", "YOLO_ALLOW_STALE_IMAGE": "1"}
	o := &Options{Workspace: ws, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Getenv: func(k string) string { return env[k] }}
	attachLaunchLog(o).finish(1)

	got := readLaunchLog(t, ws)
	for _, want := range []string{
		"yolo=",                    // the version the banner printed and the tee missed
		"workspace=" + ws,          // which workspace this block is about
		"YOLO_RUNTIME=podman",      // a fact that was set
		"YOLO_ALLOW_STALE_IMAGE=1", // a hatch that was taken
		"(unset:",                  // and the ones that were not, named rather than omitted
		"YOLO_NO_HOST_LOOPBACK",
		"rc=1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the header is missing %q:\n%s", want, got)
		}
	}
}

// TestLaunchLogTrimsToTheSameBoundThePerfLogHas: the file is per workspace and appended
// once per launch, so without a bound it is a disk-exhaustion bug on a directory the
// user cannot see into from the jail. §4.7 rules the retention by precedent — the perf
// log's — which is why this asserts against perf.MaxRuns rather than a local number.
func TestLaunchLogTrimsToTheSameBoundThePerfLogHas(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < perf.MaxRuns+5; i++ {
		var out bytes.Buffer
		o := &Options{Workspace: ws, Stdout: &out, Stderr: &out,
			Getenv: func(string) string { return "" }}
		l := attachLaunchLog(o)
		fmt.Fprintf(o.Stdout, "launch %d\n", i)
		l.finish(0)
	}

	got := readLaunchLog(t, ws)
	if runs := strings.Count(got, launchRunPrefix); runs != perf.MaxRuns {
		t.Errorf("the log holds %d run blocks, want %d", runs, perf.MaxRuns)
	}
	if !strings.Contains(got, fmt.Sprintf("launch %d", perf.MaxRuns+4)) {
		t.Error("the trim kept the wrong end: the newest launch is gone")
	}
	if strings.Contains(got, "launch 0\n") {
		t.Error("the trim kept the oldest launch")
	}
}

// TestLaunchLogFailureLeavesTheLaunchAlone: a logger that can stop a launch is a worse
// bug than the blindness it fixes, and one that announces its own failure adds a line to
// the stream §4.7 is compressing. An unwritable state dir degrades to the writers the
// launch already had, silently.
func TestLaunchLogFailureLeavesTheLaunchAlone(t *testing.T) {
	ws := t.TempDir()
	// A FILE where the state dir goes: MkdirAll fails, which is the shape a read-only
	// mount or a full disk produces.
	if err := os.WriteFile(filepath.Join(ws, ".yolo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := &Options{Workspace: ws, Stdout: &stdout, Stderr: &stderr,
		Getenv: func(string) string { return "" }}

	l := attachLaunchLog(o)
	if l != nil {
		t.Fatal("attachLaunchLog claimed a log it cannot have opened")
	}
	fmt.Fprintln(o.Stdout, "the launch continues")
	l.finish(0) // nil receiver: every method accepts one

	if got := stdout.String(); got != "the launch continues\n" {
		t.Errorf("the launch's own output changed: %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("the log failure was announced to the launch stream: %q", stderr.String())
	}
}
