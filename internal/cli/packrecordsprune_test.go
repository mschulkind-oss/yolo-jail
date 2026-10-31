package cli

// packrecordsprune_test.go pins that `yolo pack install` keeps the records an identity still
// claims (docs/design/pack-naming.md §7, PN-D15): a parked pack's, and a deselected pack's, so
// either is found again when it returns.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

const prunePinCommit = "1111111111111111111111111111111111111111"

// pruneFixture is a fake home whose user config selects acme/x from a local directory, with a
// fork pin for acme/x recorded and the identity index written by a launch's reconcile. It returns
// the user config path and the two pack directories, a and b, the source can point at.
func pruneFixture(t *testing.T) (userPath, a, b string) {
	t.Helper()
	root := t.TempDir()
	a, b = filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		writeFile(t, filepath.Join(dir, "x", "pack.json"), `{}`)
	}
	user := testconfig.User(t, pruneConfig("acme", "acme/x", a))
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(testconfig.Workspace(t, "").Root)
	if err := packsrc.WithForkLock(paths.PacksDir(), packsrc.ForkLockPath(user.Path), nil,
		func(l *packsrc.ForkLock) (bool, error) {
			l.Set(packsrc.ForkLockEntry{Key: "acme/x/pi", Source: "git+https://example.invalid/pi?ref=main",
				Ref: "main", Commit: prunePinCommit})
			return true, nil
		}); err != nil {
		t.Fatal(err)
	}
	pruneLaunch(t)
	return user.Path, a, b
}

func pruneConfig(source, handle, dir string) string {
	packs := "[]"
	if handle != "" {
		packs = `["` + handle + `"]`
	}
	return "packs = " + packs + "\n\n[pack_sources]\n" + source + " = { path = '" + filepath.ToSlash(dir) + "' }\n"
}

// pruneLaunch is what a launch does to the records: the reconcile and nothing else.
func pruneLaunch(t *testing.T) string {
	t.Helper()
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	run.ReconcilePackRecords(entries, func(l string) { fmt.Fprintln(&out, l) },
		func(l string) { fmt.Fprintln(&out, "Warning: "+l) })
	return out.String()
}

func prunePackInstall(t *testing.T) string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}

func prunePin(t *testing.T, userPath, key string) bool {
	t.Helper()
	l, err := packsrc.LoadForkLock(packsrc.ForkLockPath(userPath))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := l.Get(key)
	return ok && e.Commit == prunePinCommit
}

// A pack moved to another directory parks its pins; `pack install` keeps them; moving it back
// restores them. Fails with a prune that drops every handle not selected.
func TestPackInstallKeepsAParkedPacksPinsForItsReturn(t *testing.T) {
	userPath, a, b := pruneFixture(t)
	writeFile(t, userPath, pruneConfig("acme", "acme/x", b))
	installed := prunePackInstall(t)
	if prunePin(t, userPath, "acme/x/pi") {
		t.Fatalf("acme/x now reads from b but kept a's pin:\n%s", installed)
	}
	writeFile(t, userPath, pruneConfig("acme", "acme/x", a))
	back := pruneLaunch(t)
	if !prunePin(t, userPath, "acme/x/pi") {
		t.Errorf("acme/x reads from a again but its pin is gone\ninstall:\n%s\nlaunch:\n%s", installed, back)
	}
}

// A pack deselected, then `pack install`, then selected again under a handle renamed by hand,
// finds its pins under the new handle. Fails with a prune that drops every handle not selected.
func TestPackInstallKeepsADeselectedPacksPinsForARenamedReturn(t *testing.T) {
	userPath, a, _ := pruneFixture(t)
	writeFile(t, userPath, pruneConfig("acme", "", a))
	installed := prunePackInstall(t)
	writeFile(t, userPath, pruneConfig("tools", "tools/x", a))
	back := pruneLaunch(t)
	if !prunePin(t, userPath, "tools/x/pi") {
		t.Errorf("tools/x did not find acme/x's pin\ninstall:\n%s\nlaunch:\n%s", installed, back)
	}
}

// A selected pack's pin for a fork it no longer declares is still pruned: the identity guard
// keeps only the records of packs that are not selected.
func TestPackInstallStillPrunesASelectedPacksUndeclaredForkPin(t *testing.T) {
	userPath, _, _ := pruneFixture(t)
	out := prunePackInstall(t)
	if prunePin(t, userPath, "acme/x/pi") {
		t.Errorf("acme/x declares no fork pi, but its pin survived pack install:\n%s", out)
	}
}
