package check

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// localCloneFixture is a clone under HOME at ~/src/tillr whose origin is the pack's repository as the config
// spells it (a github.com URL: a direct git pack must use one). Nothing fetches it: the clone's own refs say
// what it has, as they would after a real clone.
type localCloneFixture struct {
	home, clone string
}

const localCloneURL = "https://github.com/owner/tillr"

func newLocalCloneFixture(t *testing.T) *localCloneFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	clone := filepath.Join(home, "src", "tillr")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, clone, "init", "-q", "-b", "main")
	gitAt(t, clone, "remote", "add", "origin", localCloneURL)
	gitAt(t, clone, "commit", "-q", "--allow-empty", "-m", "initial")
	gitAt(t, clone, "update-ref", "refs/remotes/origin/main", "HEAD")
	f := &localCloneFixture{home: home, clone: clone}
	f.config(t, "~/src/tillr")
	return f
}

func (f *localCloneFixture) config(t *testing.T, local string) {
	t.Helper()
	dir := filepath.Join(f.home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "packs = [{ git = \"" + localCloneURL + "\", ref = \"main\", local = \"" + local + "\" }]\n"
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *localCloneFixture) report(t *testing.T) (string, *reporter) {
	t.Helper()
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: hostEnv}).sectionPacks(r, jsonx.NewOrderedMap())
	return buf.String(), r
}

// §8: `yolo check` reports what the clone alone says about a `local` pack, each with its next step, and reads
// no network and writes no record.
func TestCheckReportsACloneFromTheCloneAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *localCloneFixture)
		want  []string
		warn  int
	}{
		{name: "usable", want: []string{"local clone ~/src/tillr is usable (branch main"}},
		{name: "absent",
			setup: func(t *testing.T, f *localCloneFixture) { os.RemoveAll(f.clone) },
			want:  []string{"no local clone at ~/src/tillr", "rigin's copy runs", "git clone " + localCloneURL + " ~/src/tillr"}},
		{name: "another branch", warn: 1,
			setup: func(t *testing.T, f *localCloneFixture) { gitAt(t, f.clone, "switch", "-q", "-c", "other") },
			want:  []string{"local clone ~/src/tillr not used: it is on branch other, not main", "git -C ~/src/tillr switch main", "rigin's copy runs"}},
		{name: "remote names another repository", warn: 1,
			setup: func(t *testing.T, f *localCloneFixture) {
				gitAt(t, f.clone, "remote", "set-url", "origin", "https://github.com/o/other")
			},
			want: []string{"not used: no remote of the clone names", "fix the remote, or the pack's git url"}},
		{name: "no tracking ref", warn: 1,
			setup: func(t *testing.T, f *localCloneFixture) {
				gitAt(t, f.clone, "update-ref", "-d", "refs/remotes/origin/main")
			},
			want: []string{"refs/remotes/origin/main", "fetch origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalCloneFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			out, r := f.report(t)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("report lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, f.home) {
				t.Errorf("report spells the home directory out:\n%s", out)
			}
			if r.warned != tc.warn {
				t.Errorf("warned = %d, want %d:\n%s", r.warned, tc.warn, out)
			}
			assertNoMirrors(t)
			if _, err := os.Stat(filepath.Join(paths.PacksDir(), "checks")); err == nil {
				t.Error("`yolo check` wrote a launch record")
			}
		})
	}
}

// A pack with no `local` says nothing about a clone.
func TestCheckSaysNothingAboutAPackWithoutLocal(t *testing.T) {
	f := newLocalCloneFixture(t)
	body := "packs = [{ git = \"" + localCloneURL + "\", ref = \"main\" }]\n"
	if err := os.WriteFile(filepath.Join(f.home, ".config", "yolo-jail", paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := f.report(t)
	if strings.Contains(out, "local clone") {
		t.Errorf("report names a clone for a pack that has none:\n%s", out)
	}
}

// `yolo config check` reports the same clone findings, since they need only the config and the clone.
func TestConfigCheckReportsACloneOnAnotherBranch(t *testing.T) {
	f := newLocalCloneFixture(t)
	gitAt(t, f.clone, "switch", "-q", "-c", "other")
	var out, errw bytes.Buffer
	ConfigCheck(Options{Getenv: hostEnv, Stdout: &out, Stderr: &errw, Workspace: t.TempDir()})
	for _, w := range []string{"local clone ~/src/tillr not used: it is on branch other, not main", "git -C ~/src/tillr switch main"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("config check lacks %q:\n%s\n%s", w, out.String(), errw.String())
		}
	}
}
