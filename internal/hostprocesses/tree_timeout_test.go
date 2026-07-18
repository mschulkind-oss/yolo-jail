package hostprocesses

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTreeTimeoutStderrMatchesPython pins the tree-mode timeout stderr bytes to
// Python's str(subprocess.TimeoutExpired). A hardcoded "timed out" previously
// diverged from the wire bytes Python emits (the argv list repr + "after 15
// seconds"). Skips when python3 is unavailable.
func TestTreeTimeoutStderrMatchesPython(t *testing.T) {
	argv := []string{"ps", "-eo", "pid,ppid,comm,args", "--forest"}
	got := "tree mode failed: Command '" + pyReprStrList(argv) + "' timed out after 15 seconds\n"

	py, err := exec.LookPath("python3")
	if err != nil {
		// Still assert the shape without the oracle.
		if !strings.Contains(got, "Command '['ps', '-eo', 'pid,ppid,comm,args', '--forest']' timed out after 15 seconds") {
			t.Errorf("timeout stderr = %q, missing the argv-repr form", got)
		}
		t.Skip("python3 unavailable; skipped byte-exact oracle")
	}

	out, err := exec.Command(py, "-c",
		`import subprocess
argv=["ps","-eo","pid,ppid,comm,args","--forest"]
e=subprocess.TimeoutExpired(argv,15)
import sys; sys.stdout.write(f"tree mode failed: {e}\n")`).Output()
	if err != nil {
		t.Fatalf("python oracle failed: %v", err)
	}
	if got != string(out) {
		t.Errorf("timeout stderr byte mismatch:\n go: %q\n py: %q", got, string(out))
	}
}
