package run

// launchrecord_test.go pins the machine-wide launch line (launchrecord.go, OQ-PR3 of
// docs/design/podman-reboot-readiness.md) THROUGH Run: one line per launch, refused or not,
// written where the launch's fate is decided, keyed by the workspace code and never its name.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// launchLines reads the machine-wide launch log under the test's HOME.
func launchLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(MachineLaunchLogPath())
	if err != nil {
		t.Fatalf("no machine-wide launch log: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// lineNamesWorkspace reports whether a launch line carries the workspace: its path anywhere, or
// its folder name as a whole field value. A substring test on the folder name alone flaked: a
// test's workspace is a temp dir named like "002", which a hex jail code such as 8002b8d0 can
// contain by chance.
func lineNamesWorkspace(line, ws string) bool {
	if strings.Contains(line, ws) {
		return true
	}
	base := filepath.Base(ws)
	for _, field := range strings.Fields(line) {
		value := field
		if i := strings.IndexByte(field, '='); i >= 0 {
			value = field[i+1:]
		}
		if value == base {
			return true
		}
	}
	return false
}

func TestLineNamesWorkspaceIsNotFooledByAJailCodeThatContainsTheFolderName(t *testing.T) {
	ws := "/tmp/TestSomething123/002"
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"2026-09-30T03:29:17Z launch jail=8002b8d0 runtime=podman outcome=started rc=-", false},
		{"2026-09-30T03:29:17Z launch jail=8002b8d0 workspace=002 outcome=started", true},
		{"2026-09-30T03:29:17Z launch jail=8002b8d0 path=/tmp/TestSomething123/002 outcome=started", true},
		{"2026-09-30T03:29:17Z launch 002 outcome=started", true},
	} {
		if got := lineNamesWorkspace(tc.line, ws); got != tc.want {
			t.Errorf("lineNamesWorkspace(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// assertOneLaunchLine checks the log holds exactly one line for ws, with every want field,
// and nothing that names the workspace.
func assertOneLaunchLine(t *testing.T, ws string, want ...string) string {
	t.Helper()
	lines := launchLines(t)
	if len(lines) != 1 {
		t.Fatalf("launch log has %d lines, want one per launch:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	line := lines[0]
	code := paths.JailShortHash(yoloruntime.FromWorkspace(ws))
	for _, w := range append([]string{" launch jail=" + code + " "}, want...) {
		if !strings.Contains(line, w) {
			t.Errorf("launch line lacks %q:\n%s", w, line)
		}
	}
	if lineNamesWorkspace(line, ws) {
		t.Errorf("the launch line names the workspace; it must carry only the code:\n%s", line)
	}
	if _, err := time.Parse(time.RFC3339, strings.Fields(line)[0]); err != nil ||
		!strings.HasSuffix(strings.Fields(line)[0], "Z") {
		t.Errorf("the line does not start with a UTC time: %q", line)
	}
	return line
}

// A launch the readiness gate refuses — before its container, before any loophole, the
// launch the 2026-09-29 investigation could not find — leaves its line.
func TestARefusedLaunchLeavesItsMachineWideLine(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	scriptedPodman(o, yoloruntime.Attempt{Exited: true, RC: 125, Stderr: "cannot clone: Operation not permitted"})
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run = %d", rc)
	}
	assertOneLaunchLine(t, ws, "runtime=podman", "podman_wait=0.0s", "tries=1", "outcome=not-started", "rc=1")
}

// An interrupted wait says so.
func TestAnInterruptedLaunchLeavesItsMachineWideLine(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	scriptedPodman(o, yoloruntime.Attempt{Pid: 9, Interrupted: true, Duration: 3 * time.Second})
	if rc := Run(*o); rc != 130 {
		t.Fatalf("Run = %d", rc)
	}
	assertOneLaunchLine(t, ws, "podman_wait=3.0s", "tries=1", "outcome=interrupted", "rc=130")
}

// A launch refused before runtime selection (an invalid config) is a launch too.
func TestALaunchRefusedBeforeTheRuntimeLeavesItsLine(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"packs": 7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	gate := answeringPodman(o, minimalPodmanInfo)
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run = %d\n%s", rc, stdout.String())
	}
	if gate.count() != 0 {
		t.Fatalf("the fixture reached runtime selection (%d gate attempts); it must refuse before", gate.count())
	}
	assertOneLaunchLine(t, ws, "runtime=- podman_wait=- tries=- outcome=not-started rc=1")
}

// An attach is recorded as one, when it begins — not when its session ends.
func TestAnAttachLeavesItsMachineWideLine(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	attachThroughRun(t, ws, nil)
	assertOneLaunchLine(t, ws, "runtime=podman", "tries=1", "outcome=attached", "rc=-")
}

// The line is written WHILE the jail runs, when its runtime is spawned — not when the session
// ends hours later, which is too late to read a storm by. The fake runtime here blocks until
// the test has seen the line; a record written only at Run's return would never let it go.
func TestAStartedLaunchWritesItsLineWhileTheJailRuns(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	bin, rec := t.TempDir(), t.TempDir()
	release := filepath.Join(rec, "release")
	script := "#!/bin/sh\ni=0\nwhile [ ! -e '" + release + "' ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })

	done := make(chan int, 1)
	go func() { done <- Run(*o) }()
	deadline := time.Now().Add(8 * time.Second)
	seen := false
	for time.Now().Before(deadline) && !seen {
		if data, err := os.ReadFile(MachineLaunchLogPath()); err == nil && strings.Contains(string(data), "outcome=started") {
			seen = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = os.WriteFile(release, nil, 0o644)
	<-done
	if !seen {
		t.Fatalf("no started line while the jail ran\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	assertOneLaunchLine(t, ws, "outcome=started")
}

// On macos-user the line is written when the backend is handed the launch, before it runs.
func TestAMacosUserLaunchWritesItsLineBeforeTheBackendRuns(t *testing.T) {
	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["claude"]}`, func(string) string { return "" })
	o.ProfileName = ""
	var atDispatch []string
	run := o.MacosUserRun
	o.MacosUserRun = func(cfg *jsonx.OrderedMap, ws string, a, b []string, c, d string, h macosuser.HomeOverlay,
		ctx macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
		atDispatch = launchLines(t)
		return run(cfg, ws, a, b, c, d, h, ctx, dry, env, bt, jd)
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if len(atDispatch) != 1 || !strings.Contains(atDispatch[0], "runtime=macos-user podman_wait=- tries=- outcome=started") {
		t.Errorf("at the backend's dispatch the launch log held %v", atDispatch)
	}
}

// A full log is rotated to one archived generation before the next line.
func TestTheMachineWideLaunchLogRotates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := MachineLaunchLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("x", launchLogMaxBytes) + "\n"
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	appendLaunchLine(path, "next\n")
	if got, _ := os.ReadFile(path); string(got) != "next\n" {
		t.Errorf("the fresh log holds %d bytes, want only the new line", len(got))
	}
	if old, err := os.ReadFile(path + ".1"); err != nil || string(old) != full {
		t.Errorf("the archived generation is not the full log (%v)", err)
	}
}

// ROTATION UNDER CONTENTION: launches that reach a full log at the same moment — a restore
// storm, the event this log exists to record — rotate it once, and lose neither the archived
// generation nor any launch's line. Each round fills the log to the brim, then releases a
// crowd of writers at once. A lock taken on the file being rotated let a writer that opened
// the old file rename the NEW one over the archive, deleting the whole previous generation.
func TestConcurrentLaunchesAtTheRotationBoundaryLoseNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := MachineLaunchLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("x", launchLogMaxBytes-10) + "\n"
	const writers = 24
	for round := 0; round < 8; round++ {
		_ = os.Remove(path + ".1")
		if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				appendLaunchLine(path, fmt.Sprintf("launch %d.%d\n", round, i))
			}(i)
		}
		close(start)
		wg.Wait()
		archived, err := os.ReadFile(path + ".1")
		if err != nil || string(archived) != full {
			t.Fatalf("round %d: the archived generation is %d bytes (%v), want the full log that was "+
				"rotated: concurrent rotation deleted it", round, len(archived), err)
		}
		active, _ := os.ReadFile(path)
		for i := 0; i < writers; i++ {
			if want := fmt.Sprintf("launch %d.%d\n", round, i); strings.Count(string(active), want) != 1 {
				t.Fatalf("round %d: launch %d's line is not in the active log exactly once:\n%s", round, i, active)
			}
		}
	}
}
