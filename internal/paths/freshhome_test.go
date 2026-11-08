package paths

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestNewFreshHomeMakesANewEmptyDirectoryBesideTheSetupsHome is SD-D14's layout: a fresh home is
// <workspace>/.yolo/setups/<setup>/home.fresh-<stamp>, made new and empty, and a second one made in
// the same second never reuses the first.
func TestNewFreshHomeMakesANewEmptyDirectoryBesideTheSetupsHome(t *testing.T) {
	ws := t.TempDir()
	now := time.Date(2026, 11, 8, 10, 15, 0, 0, time.UTC)
	regular := SetupHomeState(ws, "podman-self")
	if err := os.MkdirAll(filepath.Join(regular, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := NewFreshHome(ws, "podman-self", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(SetupStateDir(ws, "podman-self"), "home.fresh-20261108T101500Z"); first != want {
		t.Errorf("fresh home = %q, want %q", first, want)
	}
	entries, err := os.ReadDir(first)
	if err != nil || len(entries) != 0 {
		t.Errorf("the fresh home is not a new empty directory: %v %v", entries, err)
	}
	second, err := NewFreshHome(ws, "podman-self", now)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || filepath.Base(second) != "home.fresh-20261108T101500Z-1" {
		t.Errorf("a second fresh home in the same second = %q (first %q)", second, first)
	}
	for _, h := range []string{first, second} {
		if !IsFreshHomeState(ws, h) || !IsWorkspaceHomeState(ws, h) {
			t.Errorf("%s is not read as one of the workspace's fresh homes", h)
		}
	}
	if IsFreshHomeState(ws, regular) {
		t.Error("the setup's own home reads as a fresh home")
	}
	if _, err := os.Stat(filepath.Join(regular, "pi")); err != nil {
		t.Errorf("the regular home was touched: %v", err)
	}
}

// TestNewFreshHomeRefusesALinkedSetupDir: the setup's directory is jail-writable, so a link the
// jail left there is refused rather than followed.
func TestNewFreshHomeRefusesALinkedSetupDir(t *testing.T) {
	ws := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(SetupsDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, SetupStateDir(ws, "podman-self")); err != nil {
		t.Fatal(err)
	}
	_, err := NewFreshHome(ws, "podman-self", time.Now())
	var linked *LinkedStateDirError
	if !errors.As(err, &linked) {
		t.Fatalf("err = %v, want a LinkedStateDirError", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("a fresh home was made through the link: %v", entries)
	}
}

// TestAFreshHomeHasItsOwnCaptureStore: the captured edits of a fresh session stay with it, never in
// the setup's capture store, so the fresh launch leaves the user's own overlay untouched.
func TestAFreshHomeHasItsOwnCaptureStore(t *testing.T) {
	for rel, want := range map[string]string{
		"home":                    "prism",
		"setups/podman-self/home": "setups/podman-self/prism",
		"setups/podman-self/home.fresh-20261108T101500Z":   "setups/podman-self/prism.fresh-20261108T101500Z",
		"setups/podman-self/home.fresh-20261108T101500Z-3": "setups/podman-self/prism.fresh-20261108T101500Z-3",
		"setups/podman-self/home.fresh-bogus":              "prism",
	} {
		if got := PrismStateRel(rel); filepath.ToSlash(got) != want {
			t.Errorf("PrismStateRel(%q) = %q, want %q", rel, got, want)
		}
	}
}

// TestTheReadmeNamesFreshHomesOnlyWhenThereAreAny: the README says what a `home.fresh-*` directory
// is once one exists, and gives it no row of its own among the setups' homes.
func TestTheReadmeNamesFreshHomesOnlyWhenThereAreAny(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(SetupHomeState(ws, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(WorkspaceStateReadme(ws), "home.fresh-") {
		t.Errorf("the README names fresh homes with none on disk:\n%s", WorkspaceStateReadme(ws))
	}
	if _, err := NewFreshHome(ws, "work", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := WorkspaceStateReadme(ws)
	row := rowFor(t, got, "- `setups/<setup>/home.fresh-<stamp>/`")
	if !strings.Contains(row, "yolo --fresh-home") || !strings.Contains(row, "yolo stores") {
		t.Errorf("the fresh-home row does not say what made it and how to delete it:\n%s", row)
	}
	if strings.Contains(got, "- `setups/work/home.fresh-") {
		t.Errorf("a fresh home got a setup-home row:\n%s", got)
	}
}

// TestFreshHomesAreListedApartFromTheHomesReapersWalk is SD-D9's half for a fresh home: it is
// listed by WorkspaceFreshHomes (which `yolo stores` reads), and never by WorkspaceHomeStates, the
// list the agent-log reaper and prune walk.
func TestFreshHomesAreListedApartFromTheHomesReapersWalk(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(SetupHomeState(ws, "podman-self"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewFreshHome(ws, "podman-self", time.Date(2026, 11, 8, 10, 15, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	prism := filepath.Join(SetupStateDir(ws, "podman-self"), "prism.fresh-20261108T101500Z")
	if err := os.Mkdir(prism, 0o755); err != nil {
		t.Fatal(err)
	}
	// A linked look-alike is neither followed nor listed.
	if err := os.Symlink(t.TempDir(), filepath.Join(SetupStateDir(ws, "podman-self"), "home.fresh-20261108T101501Z")); err != nil {
		t.Fatal(err)
	}
	if homes := WorkspaceHomeStates(ws); slices.Contains(homes, fresh) || len(homes) != 1 {
		t.Errorf("WorkspaceHomeStates = %v: a fresh home must not be on the reapers' list", homes)
	}
	got := WorkspaceFreshHomes(ws)
	want := []FreshHome{{Setup: "podman-self", Dir: fresh, Prism: prism}}
	if !slices.Equal(got, want) {
		t.Errorf("WorkspaceFreshHomes = %+v, want %+v", got, want)
	}
}
