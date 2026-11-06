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
// `.yolo`, `setups` or the setup's own directory is refused before any write beneath it, whether
// the link resolves or dangles.
func TestEnsureSetupStateDirRefusesLinks(t *testing.T) {
	for _, c := range []struct {
		at       string
		dangling bool
	}{
		{".yolo", false}, {".yolo/setups", false}, {".yolo/setups/podman-self", false},
		{".yolo", true}, {".yolo/setups", true}, {".yolo/setups/podman-self", true},
	} {
		at := c.at
		name := at
		if c.dangling {
			name += " dangling"
		}
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			target := t.TempDir()
			if c.dangling {
				target = filepath.Join(target, "absent")
			}
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

// TestHomeStateRelNamesOnlyTheWorkspacesHomes: the legacy home and a valid setup's home are
// the workspace's; anything else (another workspace's, a setup dir, a bad name) is not, and
// SplitHomeState recovers the workspace from either spelling.
func TestHomeStateRelNamesOnlyTheWorkspacesHomes(t *testing.T) {
	ws := "/w/proj"
	for _, c := range []struct {
		home, rel string
		ok        bool
	}{
		{"/w/proj/.yolo/home", "home", true},
		{"/w/proj/.yolo/setups/podman-self/home", "setups/podman-self/home", true},
		{"/w/proj/.yolo/setups/podman-self/home/", "setups/podman-self/home", true},
		{"/w/proj/.yolo/setups/podman-self", "", false},
		{"/w/proj/.yolo/setups/Bad Name/home", "", false},
		{"/w/proj/.yolo/setups/podman-self/home/x", "", false},
		{"/w/other/.yolo/home", "", false},
		{"/w/proj/.yolo/prism", "", false},
		{"", "", false},
	} {
		rel, ok := HomeStateRel(ws, c.home)
		if rel != c.rel || ok != c.ok {
			t.Errorf("HomeStateRel(%q) = %q, %v; want %q, %v", c.home, rel, ok, c.rel, c.ok)
		}
		if IsWorkspaceHomeState(ws, c.home) != c.ok {
			t.Errorf("IsWorkspaceHomeState(%q) != %v", c.home, c.ok)
		}
		if !c.ok {
			continue
		}
		gotWS, gotRel, ok := SplitHomeState(c.home)
		if !ok || gotWS != ws || gotRel != c.rel {
			t.Errorf("SplitHomeState(%q) = %q, %q, %v; want %q, %q", c.home, gotWS, gotRel, ok, ws, c.rel)
		}
	}
	if _, _, ok := SplitHomeState("/w/proj/.yolo/prism"); ok {
		t.Error("SplitHomeState accepted a path that is no home")
	}
}

// TestWorkspaceHomeStatesListsRealHomesOnly: the legacy home and each setup's home that is a real
// directory are listed; a link at a setup's dir or home is neither followed nor listed.
func TestWorkspaceHomeStatesListsRealHomesOnly(t *testing.T) {
	ws := t.TempDir()
	for _, d := range []string{".yolo/home", ".yolo/setups/podman-self/home", ".yolo/setups/seatbelt-self/home", ".yolo/setups/empty"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(ws, ".yolo/setups/linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".yolo/setups/homelink"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "home"), filepath.Join(ws, ".yolo/setups/homelink/home")); err != nil {
		t.Fatal(err)
	}
	got := WorkspaceHomeStates(ws)
	want := []string{LegacyHomeState(ws), SetupHomeState(ws, "podman-self"), SetupHomeState(ws, "seatbelt-self")}
	if len(got) != len(want) {
		t.Fatalf("WorkspaceHomeStates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("WorkspaceHomeStates = %v, want %v", got, want)
		}
	}
}

// TestOpenWorkspaceHomeRootRefusesALinkOnTheWay: a link at `.yolo`, `setups`, the setup's dir or
// the home is a LinkedStateDirError naming it; a path that is no home is refused unopened.
func TestOpenWorkspaceHomeRootRefusesALinkOnTheWay(t *testing.T) {
	for _, at := range []string{".yolo", ".yolo/setups", ".yolo/setups/podman-self", ".yolo/setups/podman-self/home"} {
		t.Run(at, func(t *testing.T) {
			ws := t.TempDir()
			target := t.TempDir()
			for _, d := range []string{"setups/podman-self/home", "podman-self/home", "home"} {
				_ = os.MkdirAll(filepath.Join(target, d), 0o755)
			}
			link := filepath.Join(ws, at)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			r, err := OpenWorkspaceHomeRoot(ws, SetupHomeState(ws, "podman-self"))
			if err == nil {
				r.Close()
				t.Fatalf("a link at %s was followed", at)
			}
			var linked *LinkedStateDirError
			if !errors.As(err, &linked) || linked.Path != link {
				t.Errorf("err = %v, want a LinkedStateDirError naming %s", err, link)
			}
		})
	}
	ws := t.TempDir()
	if err := os.MkdirAll(SetupHomeState(ws, "podman-self"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := OpenWorkspaceHomeRoot(ws, SetupHomeState(ws, "podman-self"))
	if err != nil {
		t.Fatalf("a real setup home did not open: %v", err)
	}
	r.Close()
	if _, err := OpenWorkspaceHomeRoot(ws, filepath.Join(ws, ".yolo", "prism")); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("a path that is no home: err = %v, want ErrInvalid", err)
	}
}
