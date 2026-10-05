package entrypoint

// hostfilesdir_test.go pins the macos-user half of a DIRECTORY host_files entry: the host CLI
// copies the tree into the staged context tree (run's copyCtxTreeConfined), and this boot step,
// which runs OUTSIDE Seatbelt as the sandbox account, merges it into the home through the
// layout. Every test drives the real boot entry, RunDarwinBootstrap, against a real filesystem,
// as hostfileredirect_test.go does and for its reason.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// launchDir runs the real macos-user bootstrap for workspace `name` with one directory entry,
// after staging `files` (relative path → bytes) as the launcher's copy of its source.
func (f *homeRootFixture) launchDir(name string, entry config.HostFileEntry, files map[string]string) (string, error) {
	f.t.Helper()
	staged := hostUserPath(entry.Slug())
	if err := os.RemoveAll(staged); err != nil {
		f.t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(staged, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	ws := f.ws(name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		f.t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"HOME":                  f.home,
		"JAIL_HOME":             f.home,
		"YOLO_HOST_DIR":         ws,
		"YOLO_BLOCK_CONFIG":     `[]`,
		"YOLO_MISE_TOOLS":       `{}`,
		"YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv:    f.sidecar(name),
		"MISE_DATA_DIR":         filepath.Join(f.home, ".yolo", "mise"),
	}, f.home)
	setHostFiles(f.t, e, entry)
	var out strings.Builder
	e.Stderr = &out
	err := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	return out.String(), err
}

var themesEntry = config.HostFileEntry{Path: ".config/themes", Source: "/Users/someone/themes",
	IsDir: true, Mode: config.HostFileModeCopy}

// THE TREE LANDS IN THE HOME, through the layout's own links: ~/.config is the workspace's
// sidecar, so each workspace gets its own copy.
func TestMacosUserBootstrapMergesADirectoryHostFileIntoTheHome(t *testing.T) {
	f := newHomeRootFixture(t)
	said, err := f.launchDir("a", themesEntry, map[string]string{"inner.txt": "INNER\n", "sub/deep.txt": "DEEP\n"})
	requireHostFilesStepsOK(t, said, err)

	for rel, want := range map[string]string{"inner.txt": "INNER\n", "sub/deep.txt": "DEEP\n"} {
		if got := readOrAbsent(t, filepath.Join(f.home, ".config", "themes", rel)); got != want {
			t.Errorf("~/.config/themes/%s = %q, want %q", rel, got, want)
		}
	}
}

// A LINK INSIDE THE DESTINATION IS REFUSED, NEVER WRITTEN THROUGH. The bootstrap is unconfined
// and its account can write every workspace under the shared root, so a link the agent left at
// ~/.config/themes/inner.txt, pointing into another workspace, would carry this copy there. The
// launch's host_files step fails naming the link and the command that clears it, and the file
// it pointed at is untouched. Delete the macos-user branch of stageHostFile and copyTree
// overwrites the other workspace's file.
func TestMacosUserBootstrapRefusesALinkInsideAHostFilesDirectory(t *testing.T) {
	f := newHomeRootFixture(t)
	said, err := f.launchDir("a", themesEntry, map[string]string{"inner.txt": "FIRST\n"})
	requireHostFilesStepsOK(t, said, err)

	victim := filepath.Join(f.ws("other"), ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(f.home, ".config", "themes", "inner.txt")
	if err := os.Remove(planted); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, planted); err != nil {
		t.Fatal(err)
	}

	said, err = f.launchDir("a", themesEntry, map[string]string{"inner.txt": "#!/bin/sh\necho pwned\n"})
	if err == nil || !strings.Contains(err.Error(), "configure_host_files") {
		t.Fatalf("the boot wrote a host_files directory through a link it did not lay (err %v)\n%s", err, said)
	}
	for _, want := range []string{"is a link", "rm "} {
		if !strings.Contains(err.Error()+said, want) {
			t.Errorf("the refusal does not say %q: %v\n%s", want, err, said)
		}
	}
	if got := readOrAbsent(t, victim); got != "#!/bin/sh\nexit 0\n" {
		t.Errorf("the other workspace's hook now holds %q: the copy followed the agent's link", got)
	}
}

// The copy WRITES BENEATH the destination: a link that stays inside it is still refused by name
// (yolo lays none there), and nothing outside the destination is touched on any path.
func TestCopyTreeBeneathNeverWritesOutsideTheDestination(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, dest, outside := filepath.Join(base, "src"), filepath.Join(base, "dest"), filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(src, "sub"), filepath.Join(dest, "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "f.txt"), []byte("NEW\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("KEEP\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A whole directory of the destination re-pointed outside it.
	if err := os.RemoveAll(filepath.Join(dest, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "sub")); err != nil {
		t.Fatal(err)
	}

	err = copyTreeBeneath(src, dest, ".config/x")
	if err == nil || !strings.Contains(err.Error(), "~/.config/x/sub was not written") {
		t.Errorf("a linked directory in the destination was not refused by name: %v", err)
	}
	if got := readOrAbsent(t, filepath.Join(outside, "f.txt")); got != "KEEP\n" {
		t.Errorf("a file outside the destination now holds %q", got)
	}
}
