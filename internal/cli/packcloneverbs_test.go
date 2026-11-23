package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// A `local` pack's verbs (docs/design/local-pack-clones.md §8, slice 7), over real git repositories and no
// network: origin is a bare repository on disk, the pack's published address is a github.com URL as a direct git
// pack requires, and a git shim answers for that URL with the bare repository.
const cloneVerbURL = "https://github.com/owner/tillr"

type cloneVerbFixture struct {
	home, bare, pusher, clone, gitShim string
}

func cloneVerbGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCloneVerbFixture(t *testing.T) *cloneVerbFixture {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	forkPinHomeSeams(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
	f := &cloneVerbFixture{home: home, bare: filepath.Join(home, "origin.git"), pusher: filepath.Join(home, "pusher"),
		clone: filepath.Join(home, "src", "tillr"), gitShim: filepath.Join(home, "bin", "git")}
	cloneVerbGit(t, home, "init", "-q", "--bare", "-b", "main", f.bare)
	if err := os.MkdirAll(f.pusher, 0o755); err != nil {
		t.Fatal(err)
	}
	cloneVerbGit(t, f.pusher, "init", "-q", "-b", "main")
	cloneVerbGit(t, f.pusher, "remote", "add", "origin", f.bare)
	f.commit(t, f.pusher, "pack.json", `{"name":"tillr"}`)
	cloneVerbGit(t, f.pusher, "push", "-q", "origin", "main")
	// The clone's remote names the published URL; the shim below is the only thing that knows where it is.
	cloneVerbGit(t, home, "clone", "-q", f.bare, f.clone)
	cloneVerbGit(t, f.clone, "remote", "set-url", "origin", cloneVerbURL)
	cloneVerbGit(t, f.clone, "update-ref", "refs/remotes/origin/main", "HEAD")
	shim := "#!/bin/sh\nfor a; do shift; set -- \"$@\" \"$(printf %s \"$a\" | sed 's#" + cloneVerbURL + "#file://" + f.bare + "#g')\"; done\nexec " +
		realGit + " \"$@\"\n"
	if err := os.MkdirAll(filepath.Dir(f.gitShim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.gitShim, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := packInstallStore
	packInstallStore = func() *packsrc.Store { s := prev(); s.Git = f.gitShim; return s }
	t.Cleanup(func() { packInstallStore = prev })
	f.config(t, "~/src/tillr")
	return f
}

func (f *cloneVerbFixture) commit(t *testing.T, dir, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cloneVerbGit(t, dir, "add", rel)
	cloneVerbGit(t, dir, "commit", "-q", "-m", "change "+rel)
}

func (f *cloneVerbFixture) config(t *testing.T, local string) {
	t.Helper()
	dir := filepath.Join(f.home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "packs = [{ git = \"" + cloneVerbURL + "\", ref = \"main\", local = \"" + local + "\" }]\n"
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// origin moves one commit ahead of the clone.
func (f *cloneVerbFixture) originMoves(t *testing.T) {
	t.Helper()
	f.commit(t, f.pusher, "theirs.md", "theirs")
	cloneVerbGit(t, f.pusher, "push", "-q", "origin", "main")
}

func (f *cloneVerbFixture) update(t *testing.T) (rc int, out, errw string) {
	t.Helper()
	var o, e bytes.Buffer
	rc = packMain([]string{"update"}, &o, &e, false)
	return rc, o.String(), e.String()
}

func (f *cloneVerbFixture) status(t *testing.T) (int, string) {
	t.Helper()
	var o, e bytes.Buffer
	rc := packMain([]string{"status"}, &o, &e, false)
	return rc, o.String() + e.String()
}

func (f *cloneVerbFixture) cloneState(t *testing.T) string {
	t.Helper()
	return cloneVerbGit(t, f.clone, "for-each-ref") + "\n" + cloneVerbGit(t, f.clone, "status", "--porcelain") + "\n" +
		cloneVerbGit(t, f.clone, "rev-parse", "HEAD")
}

func cloneVerbPackName(t *testing.T) string {
	t.Helper()
	entries, err := config.LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Local != "" {
			return e.Name
		}
	}
	t.Fatal("no pack with a local key")
	return ""
}

// `yolo pack update` prints the same line a launch does for each copy case, in the same grade, never pulls
// into or writes to the clone, and does not claim a pin for a copy that is not origin's.
func TestPackUpdatePrintsWhichCopyRunsAndNeverTouchesTheClone(t *testing.T) {
	for _, tc := range []struct {
		name         string
		setup        func(t *testing.T, f *cloneVerbFixture)
		wantOut      []string
		wantErr      []string
		notInOut     []string
		wantRC       int
		wantBehindBy int // the clone still lacks this many origin commits afterwards
	}{
		{name: "equal", wantOut: []string{"local ~/src/tillr (working tree, equal to origin; 1 file)"},
			notInOut: []string{"→"}},
		{name: "ahead",
			setup:    func(t *testing.T, f *cloneVerbFixture) { f.commit(t, f.clone, "mine.md", "mine") },
			wantOut:  []string{"local ~/src/tillr (working tree, 1 unpushed commit; 2 files)"},
			notInOut: []string{"→"}},
		{name: "behind and clean runs origin",
			setup:   func(t *testing.T, f *cloneVerbFixture) { f.originMoves(t) },
			wantOut: []string{"origin main ", "(clone is 1 behind origin and clean — git -C ~/src/tillr pull)"}, wantBehindBy: 1},
		{name: "behind and dirty runs the working tree, warned",
			setup: func(t *testing.T, f *cloneVerbFixture) {
				f.originMoves(t)
				if err := os.WriteFile(filepath.Join(f.clone, "wip.md"), []byte("wip"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr:  []string{"Warning: ", "local ~/src/tillr (working tree; 2 files) is behind origin by 1 and has uncommitted changes — git -C ~/src/tillr pull"},
			notInOut: []string{"→"}, wantBehindBy: 1},
		{name: "clone absent",
			setup:   func(t *testing.T, f *cloneVerbFixture) { f.config(t, "~/src/nowhere") },
			wantOut: []string{"no local clone at ~/src/nowhere — git clone " + cloneVerbURL + " ~/src/nowhere"}},
		{name: "clone on another branch",
			setup:   func(t *testing.T, f *cloneVerbFixture) { cloneVerbGit(t, f.clone, "switch", "-q", "-c", "other") },
			wantErr: []string{"Warning: ", "clone ~/src/tillr not used: it is on branch other, not main — git -C ~/src/tillr switch main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloneVerbFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			name := cloneVerbPackName(t)
			before := f.cloneState(t)
			rc, out, errw := f.update(t)
			if rc != tc.wantRC {
				t.Fatalf("update rc=%d, want %d\n%s\n%s", rc, tc.wantRC, out, errw)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out, name+"  ") || !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q on %s's line:\n%s\n%s", w, name, out, errw)
				}
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(errw, w) {
					t.Errorf("stderr lacks %q:\n%s\n%s", w, out, errw)
				}
			}
			for _, w := range tc.notInOut {
				if strings.Contains(out, w) {
					t.Errorf("stdout holds %q, a pin move the working tree did not make:\n%s", w, out)
				}
			}
			if after := f.cloneState(t); after != before {
				t.Errorf("update changed the clone:\nbefore %s\nafter  %s", before, after)
			}
			if tc.wantBehindBy > 0 {
				if n := cloneVerbGit(t, f.clone, "rev-list", "--count", "HEAD..refs/remotes/origin/main"); n != "0" {
					t.Errorf("update moved the clone's tracking ref (%s)", n)
				}
				if tip := cloneVerbGit(t, f.pusher, "rev-parse", "HEAD"); cloneVerbGit(t, f.clone, "rev-parse", "HEAD") == tip {
					t.Error("update pulled into the clone")
				}
			}
		})
	}
}

// An update that cannot reach origin is a failed update, and the line says which copy runs and that.
func TestPackUpdateOfflineFailsButSaysWhichCopyRuns(t *testing.T) {
	f := newCloneVerbFixture(t)
	if rc, out, errw := f.update(t); rc != 0 {
		t.Fatalf("the first update rc=%d\n%s\n%s", rc, out, errw)
	}
	if err := os.Rename(f.bare, f.bare+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(f.bare+".gone", f.bare) })
	rc, out, errw := f.update(t)
	if rc == 0 {
		t.Errorf("an update that could not reach origin succeeded\n%s\n%s", out, errw)
	}
	if !strings.Contains(errw, "could not fetch main") || strings.Contains(errw, "pack install: \n") {
		t.Errorf("stderr does not name the failed fetch:\n%s", errw)
	}
	if !strings.Contains(errw, "local ~/src/tillr (working tree, not verified:") || !strings.Contains(errw, "yolo pack update when online") {
		t.Errorf("the line does not say the clone runs unverified:\n%s", errw)
	}
}

// `pack status` reads the record and no git: which copy the last launch ran, why, how long ago, and that the
// next launch decides again; and it says so for a pack no launch has decided yet, and for a record of another path.
func TestPackStatusShowsTheLocalCopyFromTheRecord(t *testing.T) {
	f := newCloneVerbFixture(t)
	if _, out := f.status(t); !strings.Contains(out, "no launch has decided which copy runs yet") ||
		!strings.Contains(out, "yolo pack update") {
		t.Errorf("status before any launch does not say so and name the step:\n%s", out)
	}
	if rc, out, errw := f.update(t); rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	// No git: a shim that fails every run proves the verb reads the record alone.
	if err := os.WriteFile(f.gitShim, []byte("#!/bin/sh\necho status ran git >&2\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rc, out := f.status(t)
	if rc != 0 {
		t.Fatalf("status rc=%d\n%s", rc, out)
	}
	for _, w := range []string{"local ~/src/tillr (working tree, equal to origin; 1 file)", "decided at the last launch", "the next launch decides again"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "status ran git") {
		t.Errorf("status ran git:\n%s", out)
	}
	f.config(t, "~/src/elsewhere")
	if _, out := f.status(t); !strings.Contains(out, "decided for ~/src/tillr, not ~/src/elsewhere") {
		t.Errorf("status does not say the record is for another clone:\n%s", out)
	}
}

// The pack name a verb prints is the pack's own: a pack with no `local` shows nothing extra.
func TestPackStatusAddsNothingForAPackWithoutLocal(t *testing.T) {
	f := newCloneVerbFixture(t)
	dir := filepath.Join(f.home, ".config", "yolo-jail")
	body := "packs = [{ git = \"" + cloneVerbURL + "\", ref = \"main\" }]\n"
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, out := f.status(t); strings.Contains(out, "launch") && strings.Contains(out, "decide") {
		t.Errorf("status speaks of a launch decision for a pack with no local:\n%s", out)
	}
}
