package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runPackageProfileSection drives checkPackageProfile over an isolated HOME so the probe
// reads a fixture GC root rather than the developer's real state dir.
func runPackageProfileSection(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	o := &Options{Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	r := newReporter(&out, false)
	o.checkPackageProfile(r)
	return out.String()
}

// rootLinkIn returns the GC-root path checkPackageProfile reads under home, with its parent
// dir created. Spelled from the same leaves darwinpkg.ProfileRootLink uses, so the fixture
// and the probe cannot disagree about where the root lives.
func rootLinkIn(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "yolo-jail", "build", "package-roots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "packages")
}

// The healthy state: a root that resolves is a PASS naming the store path, so a user can
// see which closure their agent's tools come from without running nix.
func TestPackageProfileSectionRooted(t *testing.T) {
	home := t.TempDir()
	store := t.TempDir() // stands in for the /nix/store profile
	if err := os.Symlink(store, rootLinkIn(t, home)); err != nil {
		t.Fatal(err)
	}
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[PASS]") || !strings.Contains(got, store) {
		t.Errorf("a resolved+rooted profile should PASS and name the store path:\n%s", got)
	}
}

// No root yet is the normal pre-first-run state, so it WARNs (a launch creates it) rather
// than failing — and the note must name the path a run will create.
func TestPackageProfileSectionAbsent(t *testing.T) {
	home := t.TempDir()
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[WARN]") {
		t.Errorf("an absent root is normal before the first run — expected WARN:\n%s", got)
	}
	if !strings.Contains(got, "package-roots") {
		t.Errorf("the note should name where the root will be created:\n%s", got)
	}
}

// The one genuinely bad state: the root exists but its target is GONE. That means a nix GC
// collected a ROOTED closure, which is the N1 defect recurring, so it is a FAIL — the whole
// reason to report the root at all is to make that observable rather than silent.
func TestPackageProfileSectionDanglingRootFails(t *testing.T) {
	home := t.TempDir()
	link := rootLinkIn(t, home)
	if err := os.Symlink(filepath.Join(t.TempDir(), "collected-away"), link); err != nil {
		t.Fatal(err)
	}
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("a dangling GC root means a rooted closure was collected — expected FAIL:\n%s", got)
	}
	if !strings.Contains(got, "no longer exists") {
		t.Errorf("the failure should say what is wrong:\n%s", got)
	}
}
