package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupPathsAreArithmetic(t *testing.T) {
	ws := t.TempDir()
	if got, want := SetupsDir(ws), ws+"/.yolo/setups"; got != want {
		t.Errorf("SetupsDir = %q, want %q", got, want)
	}
	if got, want := SetupStateDir(ws, "podman-self"), ws+"/.yolo/setups/podman-self"; got != want {
		t.Errorf("SetupStateDir = %q, want %q", got, want)
	}
	if got, want := SetupHomeState(ws, "podman-self"), ws+"/.yolo/setups/podman-self/home"; got != want {
		t.Errorf("SetupHomeState = %q, want %q", got, want)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("path helpers created state: %v", err)
	}
}

func TestEnsureSetupStateDirCreatesTheChain(t *testing.T) {
	ws := t.TempDir()
	d, err := EnsureSetupStateDir(ws, "podman-self")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want, err := os.Lstat(SetupStateDir(ws, "podman-self"))
	if err != nil || !want.IsDir() {
		t.Fatalf("setup dir not created: %v", err)
	}
	got, err := d.Stat()
	if err != nil || !os.SameFile(got, want) {
		t.Fatalf("returned directory is not the setup dir: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo", WorkspaceStateIgnoreName)); err != nil {
		t.Errorf(".yolo was not made un-committable: %v", err)
	}
	d2, err := EnsureSetupStateDir(ws, "podman-self")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	d2.Close()
}

func TestEnsureSetupStateDirRefusesBadNames(t *testing.T) {
	ws := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", "Podman", "-x"} {
		if d, err := EnsureSetupStateDir(ws, name); err == nil {
			d.Close()
			t.Errorf("EnsureSetupStateDir(%q) accepted a name that is not a setup name", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused name still created state: %v", err)
	}
}

// TestEnsureSetupStateDirRefusesLinks: `.yolo` is jail-writable, so a link the last jail left at
// `.yolo`, `setups` or the setup's own directory is refused before any write beneath it.
func TestEnsureSetupStateDirRefusesLinks(t *testing.T) {
	for _, at := range []string{".yolo", ".yolo/setups", ".yolo/setups/podman-self"} {
		t.Run(at, func(t *testing.T) {
			ws := t.TempDir()
			target := t.TempDir()
			link := filepath.Join(ws, at)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			d, err := EnsureSetupStateDir(ws, "podman-self")
			if err == nil {
				d.Close()
				t.Fatalf("a link at %s was followed", at)
			}
			var linked *LinkedStateDirError
			if !errors.As(err, &linked) || linked.Path != link {
				t.Errorf("err = %v, want a LinkedStateDirError naming %s", err, link)
			}
			if entries, _ := os.ReadDir(target); len(entries) != 0 {
				t.Errorf("wrote %d entries into the link's target", len(entries))
			}
		})
	}
}
