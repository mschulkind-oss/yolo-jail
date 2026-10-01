package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestALaunchWhoseKeeperDiesBeforeItStartsSaysSo drives a whole fresh launch to a keeper that exits
// before it reports it started: nothing it would have said reaches the terminal (its own stderr is
// /dev/null), so the launch names the status and the keeper's log, returns the keeper's status, and
// takes back the skeleton and pack tree it would have handed over.
func TestALaunchWhoseKeeperDiesBeforeItStartsSaysSo(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
		removeKeeperPlan(planPath)
		return func() int { return 3 }, nil
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	cname := yoloruntime.FromWorkspace(ws)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	if rc := Run(*o); rc != 3 {
		t.Errorf("rc %d, want the keeper's 3\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	both := stdout.String() + stderr.String()
	if !strings.Contains(both, "This jail's keeper ended (status 3) before it started; its log: "+keeperLogPath(cname)) {
		t.Errorf("the launch did not say its keeper died:\n%s", both)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".local", "share", "yolo-jail", "agents", cname, "pack-trees")); len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the launch left pack trees no keeper held: %v", names)
	}
}
