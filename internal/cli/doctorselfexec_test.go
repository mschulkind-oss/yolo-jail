package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// TestLoopholesStatusSelfChecksLeaveNothingRunning pins TestMain's daemon dispatch on the path
// that measured its absence: `yolo loopholes status` runs each pack loophole's doctor_cmd, the
// claude pack's is `["yolo", "internal", "daemon", "claude-oauth-broker", "--self-check"]`, and
// execx.SelfExecArgv turns the leading "yolo" into os.Executable(), which under `go test` is
// THIS TEST BINARY.
//
// Without the dispatch the child did not run the self-check: it ran this package's whole suite.
// The doctor's 10-second deadline then killed the child's process group, but the child had
// already reached this same status path and started self-checks of its own, each in a session
// of its own (runOne's Setsid), which that kill cannot reach. Those survived the package run and
// went on writing under the finished tests' temp trees, which is how `just check-ci` failed with
// "go: unlinkat …/embedded-packs: directory not empty".
//
// Two assertions, because they fail differently: the broker's check must reach a verdict (a
// suite run instead of a self-check is "timeout after 10s"), and no `internal daemon` child of
// this binary may be running once the command has returned.
func TestLoopholesStatusSelfChecksLeaveNothingRunning(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// Host side: `status` reports "host-side only" in a jail, and the loopholes package reads
	// the variable's PRESENCE, so it is unset rather than emptied.
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	t.Chdir(cwd)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The lazy pack-module resolver answers once per process; this test's answer must come
	// from this home, and the next test's must not.
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)

	stdout, stderr := captureDispatch(t, []string{"loopholes", "status", "--format", "json"})
	var doc struct {
		Entries []loopholes.StatusEntry `json:"loopholes"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("status did not print a JSON document: %v\n%s\n%s", err, stdout, stderr)
	}
	var broker *loopholes.StatusEntry
	for i := range doc.Entries {
		if doc.Entries[i].Name == "claude-oauth-broker" {
			broker = &doc.Entries[i]
		}
	}
	if broker == nil {
		t.Fatalf("anti-vacuity: `packs: [\"claude\"]` gave no claude-oauth-broker to check, so no "+
			"self-check ran:\n%s", stdout)
	}
	if broker.RC == nil || strings.Contains(broker.Output, "timeout") {
		t.Errorf("the claude-oauth-broker self-check reached no verdict (rc %v, output %q): the "+
			"self-exec'd test binary ran something other than the daemon — route "+
			"`internal daemon` argv to internaldaemon.Run in TestMain", broker.RC, broker.Output)
	}
	if left := selfExecDaemonsRunning(t); len(left) > 0 {
		t.Errorf("self-checks of this test binary are still running after the command returned:\n%s",
			strings.Join(left, "\n"))
	}
}

// selfExecDaemonsRunning lists the processes running THIS test binary as `internal daemon …`,
// read from /proc. The executable's path is unique to this package's build, so another package's
// binary running beside it is not counted. Nil where there is no /proc to read (darwin): the
// verdict assertion above still holds there.
func selfExecDaemonsRunning(t *testing.T) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(argv) >= 3 && argv[0] == exe && argv[1] == "internal" && argv[2] == "daemon" {
			out = append(out, e.Name()+": "+strings.Join(argv, " "))
		}
	}
	return out
}
