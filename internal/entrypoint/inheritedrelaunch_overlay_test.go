package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

const inheritedCoreOverlayTestDest = ".config/yolo-jail/config.jsonc"

var inheritedCoreOverlayTestBody = []byte("sealed inherited config\n")

type inheritedCoreOverlayFixture struct {
	home, sidecar, overlay, source, dest string
	env                                  *Env
}

func newInheritedCoreOverlayFixture(t *testing.T) inheritedCoreOverlayFixture {
	t.Helper()
	base := t.TempDir()
	f := inheritedCoreOverlayFixture{
		home:    filepath.Join(base, "home"),
		sidecar: filepath.Join(base, "workspace", ".yolo", "home"),
		overlay: filepath.Join(base, "overlay"),
	}
	for _, dir := range []string{f.home, f.sidecar, f.overlay} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := DarwinEnvFrom(map[string]string{
		"HOME": f.home, "JAIL_HOME": f.home, DarwinHomeSidecarEnv: f.sidecar,
	}, f.home)
	if err := InstallDarwinHomeLayout(e, nil); err != nil {
		t.Fatalf("lay inherited config's ~/.config route: %v", err)
	}
	f.env = e
	f.source = filepath.Join(f.overlay, filepath.FromSlash(inheritedCoreOverlayTestDest))
	f.dest = filepath.Join(f.sidecar, "config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(f.source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.source, inheritedCoreOverlayTestBody, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.source, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteHomeOverlayManifest(f.overlay, []string{inheritedCoreOverlayTestDest}); err != nil {
		t.Fatal(err)
	}
	e.Vars["YOLO_DARWIN_HOME_OVERLAY"] = f.overlay
	return f
}

func (f inheritedCoreOverlayFixture) install() error {
	return InstallHomeOverlay(f.env, nil)
}

func inheritedCoreOverlayEntryInfo(t *testing.T, name string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// AN UNCHANGED RELAUNCH KEEPS THE DELIVERED COPY (MMP-D35): the first install copies the sealed
// staged file, and the second, finding the bytes the host's record lists, builds nothing and leaves
// the same inode in place.
func TestInheritedCoreOverlayReusesTheUnchangedCopyWithoutWriting(t *testing.T) {
	f := newInheritedCoreOverlayFixture(t)
	writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody)
	staged := 0
	withOverlayInstallHook(t, func(dest, window string) {
		if dest == inheritedCoreOverlayTestDest && window == "staged" {
			staged++
		}
	})
	if err := f.install(); err != nil {
		t.Fatal(err)
	}
	before := inheritedCoreOverlayEntryInfo(t, f.dest)
	source, err := os.Lstat(f.source)
	if err != nil || os.SameFile(before, source) || before.Mode().Perm() != 0o444 {
		t.Fatalf("first install is not a sealed copy of the source: source %v, destination %v, err %v", source, before, err)
	}
	if staged != 1 {
		t.Fatalf("first install staged %d times, want 1", staged)
	}
	if err := f.install(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(f.dest)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || staged != 1 {
		t.Fatalf("unchanged install wrote the inherited destination: before %v, after %v, staged %d, err %v", before, after, staged, err)
	}
}

func TestInheritedCoreOverlayRefusesExistingOwnerEntries(t *testing.T) {
	cases := map[string]func(t *testing.T, f inheritedCoreOverlayFixture) (os.FileInfo, string){
		"independent matching file": func(t *testing.T, f inheritedCoreOverlayFixture) (os.FileInfo, string) {
			if err := os.MkdirAll(filepath.Dir(f.dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.dest, inheritedCoreOverlayTestBody, 0o444); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, f.dest), f.dest
		},
		"different mode": func(t *testing.T, f inheritedCoreOverlayFixture) (os.FileInfo, string) {
			if err := os.MkdirAll(filepath.Dir(f.dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.dest, inheritedCoreOverlayTestBody, 0o600); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, f.dest), f.dest
		},
		"symbolic link": func(t *testing.T, f inheritedCoreOverlayFixture) (os.FileInfo, string) {
			if err := os.MkdirAll(filepath.Dir(f.dest), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(filepath.Dir(f.dest), "owner-config")
			if err := os.WriteFile(target, inheritedCoreOverlayTestBody, 0o444); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, f.dest); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, f.dest), f.dest
		},
		"directory": func(t *testing.T, f inheritedCoreOverlayFixture) (os.FileInfo, string) {
			if err := os.MkdirAll(filepath.Dir(f.dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(f.dest, 0o700); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, f.dest), f.dest
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInheritedCoreOverlayFixture(t)
			before, preservePath := create(t, f)
			var beforeBody []byte
			if before.Mode().IsRegular() {
				body, err := os.ReadFile(preservePath)
				if err != nil {
					t.Fatal(err)
				}
				beforeBody = body
			}
			if err := f.install(); err == nil {
				t.Fatal("install replaced an existing owner entry")
			}
			after := inheritedCoreOverlayEntryInfo(t, preservePath)
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("refusal changed owner entry: before %v, after %v", before, after)
			}
			if before.Mode().IsRegular() {
				afterBody, err := os.ReadFile(preservePath)
				if err != nil || string(beforeBody) != string(afterBody) {
					t.Fatalf("refusal changed owner bytes: before %q, after %q, err %v", beforeBody, afterBody, err)
				}
			}
		})
	}
}

func TestInheritedCoreOverlayNoReplaceAtStagedBoundary(t *testing.T) {
	cases := map[string]func(t *testing.T, dest string) (os.FileInfo, string){
		"independent matching file": func(t *testing.T, dest string) (os.FileInfo, string) {
			if err := os.WriteFile(dest, inheritedCoreOverlayTestBody, 0o444); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, dest), dest
		},
		"owner mode": func(t *testing.T, dest string) (os.FileInfo, string) {
			if err := os.WriteFile(dest, inheritedCoreOverlayTestBody, 0o600); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, dest), dest
		},
		"symbolic link": func(t *testing.T, dest string) (os.FileInfo, string) {
			target := dest + ".owner"
			if err := os.WriteFile(target, inheritedCoreOverlayTestBody, 0o444); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, dest); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, dest), dest
		},
		"directory": func(t *testing.T, dest string) (os.FileInfo, string) {
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			return inheritedCoreOverlayEntryInfo(t, dest), dest
		},
	}
	for name, createOwner := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInheritedCoreOverlayFixture(t)
			var before os.FileInfo
			var preservePath string
			withOverlayInstallHook(t, func(dest, window string) {
				if dest == inheritedCoreOverlayTestDest && window == "staged" {
					before, preservePath = createOwner(t, f.dest)
				}
			})
			if err := f.install(); err == nil {
				t.Fatal("atomic no-replace install accepted a raced-in owner entry")
			}
			if before == nil {
				t.Fatal("staged hook did not create the owner entry")
			}
			after := inheritedCoreOverlayEntryInfo(t, preservePath)
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("no-replace refusal changed raced-in entry: before %v, after %v", before, after)
			}
			if before.Mode().IsRegular() {
				got, err := os.ReadFile(preservePath)
				if err != nil || string(got) != string(inheritedCoreOverlayTestBody) {
					t.Fatalf("no-replace refusal changed owner bytes: %q, %v", got, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(f.dest), ".config.jsonc.yolo-overlay-new")); !os.IsNotExist(err) {
				t.Fatalf("failed staged entry was left behind: %v", err)
			}
		})
	}
}
