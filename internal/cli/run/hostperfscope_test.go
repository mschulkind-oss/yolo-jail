package run

// hostperfscope_test.go pins the one stray-.yolo creator that lives OUTSIDE the launch
// pipeline and therefore behind no launch guard: the host perf log's file sink MkdirAll's
// <workspace>/.yolo, and `yolo stop` builds one (TimingLogFor) with the cwd as the
// workspace. On a machine with `perf_logging: true` a `yolo stop` typed in the home minted
// ~/.yolo/host-perf.log, which then hijacks workspaceRoot()'s upward walk for good.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// perfScopeHome mints a resolved HOME and points the process at it (see scopeGuardHome in
// workspacescopeguard_test.go for why it resolves at the mint).
func perfScopeHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestTheHostPerfLogIsNotWrittenIntoABoundaryDirectory(t *testing.T) {
	home := perfScopeHome(t)
	for _, ws := range []string{
		home,
		filepath.Join(home, ".config"),
		filepath.Join(home, paths.GlobalStorageRel()),
	} {
		var notices []string
		log := newTimingLog(true, ws, "yolo-ws-test0000", &bytes.Buffer{},
			func(msg string) { notices = append(notices, msg) })
		if log == nil {
			t.Fatalf("newTimingLog(%q) returned nil — recording was requested, and the "+
				"caller's own reporting hangs off a non-nil collector", ws)
		}
		// Drive one span end to end: a sink that only fails on WRITE would pass a test
		// that merely constructed the log.
		log.Record("probe", time.Millisecond)

		if _, err := os.Stat(paths.WorkspaceStateDir(ws)); !os.IsNotExist(err) {
			t.Errorf("%s exists — the timing log minted a stray state dir (stat err: %v)",
				paths.WorkspaceStateDir(ws), err)
		}
		if !strings.Contains(strings.Join(notices, "\n"), "not recording timings") {
			t.Errorf("the skip was silent; notices were %q", notices)
		}
	}
}

// TestTheHostPerfLogIsStillWrittenForAnOrdinaryWorkspace is the positive control, and the
// reason the test above cannot be satisfied by deleting the file sink altogether.
func TestTheHostPerfLogIsStillWrittenForAnOrdinaryWorkspace(t *testing.T) {
	home := perfScopeHome(t)
	ws := filepath.Join(home, "code", "project")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	log := newTimingLog(true, ws, "yolo-ws-test0000", &bytes.Buffer{}, func(string) {})
	if log == nil {
		t.Fatal("newTimingLog returned nil for an ordinary workspace")
	}
	log.Record("probe", time.Millisecond)

	path := filepath.Join(paths.WorkspaceStateDir(ws), HostPerfLogName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the host perf log was not written to %s: %v", path, err)
	}
	if !strings.Contains(string(data), "probe") {
		t.Errorf("%s does not carry the span:\n%s", path, data)
	}
}
