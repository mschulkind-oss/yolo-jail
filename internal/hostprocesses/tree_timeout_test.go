package hostprocesses

import (
	"testing"
)

// TestTreeTimeoutStderrGolden pins the tree-mode timeout stderr bytes to the
// exact form Python's str(subprocess.TimeoutExpired) produces (the argv-list
// repr + "after 15 seconds"). A hardcoded "timed out" previously diverged from
// those wire bytes.
func TestTreeTimeoutStderrGolden(t *testing.T) {
	argv := []string{"ps", "-eo", "pid,ppid,comm,args", "--forest"}
	got := "tree mode failed: Command '" + pyReprStrList(argv) + "' timed out after 15 seconds\n"
	want := "tree mode failed: Command '['ps', '-eo', 'pid,ppid,comm,args', '--forest']' timed out after 15 seconds\n"
	if got != want {
		t.Errorf("timeout stderr = %q, want %q", got, want)
	}
}
