package paths

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUnifiedHomeIsUnderTheWorkspaceStateDir(t *testing.T) {
	if got, want := UnifiedHome("/w"), "/w/.yolo/unified-home"; got != want {
		t.Errorf("UnifiedHome = %q, want %q", got, want)
	}
}

func TestEnsureUnifiedHomeMakesItThroughNoLinks(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := EnsureUnifiedHome(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want, err := os.Lstat(UnifiedHome(ws))
	if err != nil || !want.IsDir() {
		t.Fatalf("unified home not created: %v", err)
	}
	got, err := d.Stat()
	if err != nil || !os.SameFile(got, want) {
		t.Fatalf("returned directory is not the unified home: %v", err)
	}
	// A second call opens the same directory.
	d2, err := EnsureUnifiedHome(ws)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()

	// A link at the unified home is refused and nothing is written beneath its target.
	ws2, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws2, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := UnifiedHome(ws2)
	if err := os.Symlink(sentinel, link); err != nil {
		t.Fatal(err)
	}
	_, err = EnsureUnifiedHome(ws2)
	var linked *LinkedStateDirError
	if !errors.As(err, &linked) || linked.Path != link {
		t.Fatalf("err = %v, want a LinkedStateDirError naming %s", err, link)
	}
	if ents, _ := os.ReadDir(sentinel); len(ents) != 0 {
		t.Errorf("wrote into the link's target: %v", ents)
	}
}

func TestOpenUnifiedHomeRootRefusesALinkAtYolo(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sentinel, "unified-home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, WorkspaceStateDir(ws)); err != nil {
		t.Fatal(err)
	}
	_, err = OpenUnifiedHomeRoot(ws)
	var linked *LinkedStateDirError
	if !errors.As(err, &linked) {
		t.Fatalf("err = %v, want a LinkedStateDirError", err)
	}
}

func TestUnifiedHomeIsNotAWorkspaceHome(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := EnsureUnifiedHome(ws)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, ok := HomeStateRel(ws, UnifiedHome(ws)); ok {
		t.Error("HomeStateRel accepts the unified home as a home")
	}
	if slices.Contains(WorkspaceHomeStates(ws), UnifiedHome(ws)) {
		t.Error("WorkspaceHomeStates lists the unified home")
	}
	r, err := OpenUnifiedHomeRoot(ws)
	if err != nil {
		t.Fatalf("OpenUnifiedHomeRoot: %v", err)
	}
	r.Close()
}
