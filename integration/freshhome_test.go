package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestFreshHomeLaunchWritesIntoTheFreshHomeNotTheUsualOne is `yolo --fresh-home` end to end
// (docs/design/workspace-state-safety.md SD-D14): a file the jail writes in its home lands in a new
// `setups/<setup>/home.fresh-<stamp>` directory, the setup's usual home gets nothing of it, the
// launch says where the fresh home is, and the fresh home is kept after the jail stops.
func TestFreshHomeLaunchWritesIntoTheFreshHomeNotTheUsualOne(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	const marker = "fresh-home-marker"
	script := "mkdir -p ~/.config && echo fresh > ~/.config/" + marker
	r := runCommand(t, dir, append(jailRunArgs(), "--fresh-home", "--", "bash", "-lc", script))
	if r.rc != 0 {
		t.Fatalf("yolo --fresh-home: rc %d\n%s", r.rc, r.combined())
	}
	forceRemoveContainer(dir)

	fresh := paths.WorkspaceFreshHomes(dir)
	if len(fresh) != 1 {
		t.Fatalf("fresh homes after one --fresh-home launch = %+v, want exactly one\n%s", fresh, r.combined())
	}
	if !strings.HasPrefix(filepath.Base(fresh[0].Dir), paths.FreshHomePrefix) ||
		filepath.Dir(fresh[0].Dir) != filepath.Dir(jailHome(dir)) {
		t.Errorf("fresh home %s is not a home.fresh-* beside %s", fresh[0].Dir, jailHome(dir))
	}
	if b, err := os.ReadFile(filepath.Join(fresh[0].Dir, "config", marker)); err != nil || strings.TrimSpace(string(b)) != "fresh" {
		t.Errorf("the jail's write did not land in the fresh home %s: %q %v\n%s", fresh[0].Dir, b, err, r.combined())
	}
	if _, err := os.Lstat(filepath.Join(jailHome(dir), "config", marker)); !os.IsNotExist(err) {
		t.Errorf("the jail's write reached the usual home %s (err %v)", jailHome(dir), err)
	}
	if !strings.Contains(r.combined(), "Fresh home:") || !strings.Contains(r.combined(), fresh[0].Dir) {
		t.Errorf("the launch did not disclose the fresh home and its path %s:\n%s", fresh[0].Dir, r.combined())
	}
}
