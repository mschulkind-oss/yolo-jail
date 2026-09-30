package cli

// forkpin_test.go pins the PIN (docs/design/forked-programs-as-packs.md FP-D7) through the verbs that
// make and move it — `yolo pack install` pins once and leaves a pinned fork alone when its branch
// moves, `yolo pack update` moves it, `yolo pack status` reports it and its drift — against a real
// local git repository standing in for the fork's remote.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// forkRepo is a real git repository with one commit on main, and a func that adds a commit and
// returns the new HEAD.
func forkRepo(t *testing.T) (dir string, commit func(msg string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	commit = func(msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "build.sh"), []byte("# "+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	commit("first")
	return dir, commit
}

// forkPinHome is a temp HOME whose user config selects a base pack and a fork of it building from
// source; it returns the fork pack's manifest path, for a test that edits the declaration.
func forkPinHome(t *testing.T, source string) (forkManifestPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	packs := t.TempDir()
	writeFile(t, filepath.Join(packs, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	forkManifestPath = filepath.Join(packs, "forkpack", "pack.json")
	writeForkManifest(t, forkManifestPath, source)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]}`)
	// The update verb's in-jail program refresh and host apply are not what these tests are about.
	origRefresh, origApply := programRefresh, hostApplyFromPackUpdate
	programRefresh = func(richtext.Printer, io.Writer) int { return 0 }
	hostApplyFromPackUpdate = func([]string, io.Writer, io.Writer, bool, io.Reader) int { return 0 }
	t.Cleanup(func() { programRefresh, hostApplyFromPackUpdate = origRefresh, origApply })
	return forkManifestPath
}

func writeForkManifest(t *testing.T, path, source string) {
	t.Helper()
	writeFile(t, path, `{"name":"forkpack","contributes":[{"kind":"program","bin":"tool","via":"source",`+
		`"fork_of":"basepack","source":"`+source+`","build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
}

// pinnedCommit is what the fork lock pins forkpack/tool to, "" when nothing.
func pinnedCommit(t *testing.T) string {
	t.Helper()
	l, err := packsrc.LoadForkLock(packsrc.ForkLockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := l.Get("forkpack/tool")
	return e.Commit
}

func TestPackInstallPinsAForkOnceAndUpdateMovesIt(t *testing.T) {
	repo, commit := forkRepo(t)
	head1 := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	forkPinHome(t, "git+file://"+repo+"?ref=main")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if got := pinnedCommit(t); got != head1 {
		t.Fatalf("install pinned %q, want the branch's head %q\n%s", got, head1, out.String())
	}
	if !strings.Contains(out.String(), "forkpack/tool") {
		t.Errorf("install does not say it pinned the fork:\n%s", out.String())
	}

	// The branch moves. INSTALL LEAVES A PINNED FORK ALONE: moving a pin is update's act.
	head2 := commit("second")
	out.Reset()
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second install rc=%d\n%s", rc, errw.String())
	}
	if got := pinnedCommit(t); got != head1 {
		t.Errorf("install moved a pinned fork to %q (want it left at %q)", got, head1)
	}

	// UPDATE MOVES IT, and says the next launch builds the new revision.
	out.Reset()
	if rc := packMain([]string{"update"}, &out, &errw, false); rc != 0 {
		t.Fatalf("update rc=%d\n%s", rc, errw.String())
	}
	if got := pinnedCommit(t); got != head2 {
		t.Errorf("update pinned %q, want the moved head %q", got, head2)
	}
	if !strings.Contains(out.String(), "the next launch builds the new revision") {
		t.Errorf("update does not say the pin moved:\n%s", out.String())
	}
}

// `yolo pack status` reports the pin, and a fork whose source changed since it was pinned is
// drift: named with the command that repairs it, and a non-zero exit.
func TestPackStatusReportsAForkPinAndItsDrift(t *testing.T) {
	repo, _ := forkRepo(t)
	source := "git+file://" + repo + "?ref=main"
	manifest := forkPinHome(t, source)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc=%d\n%s", rc, errw.String())
	}
	out.Reset()
	if rc := packMain([]string{"status"}, &out, &errw, false); rc != 0 {
		t.Fatalf("status rc=%d with an up-to-date pin\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "forkpack/tool") || !strings.Contains(out.String(), shortSHA(pinnedCommit(t))) {
		t.Errorf("status does not show the fork's pin:\n%s", out.String())
	}
	// And what is built: nothing yet, and which act builds it.
	if !strings.Contains(out.String(), "not built yet for linux/") {
		t.Errorf("status does not say the pin is not built yet:\n%s", out.String())
	}

	writeForkManifest(t, manifest, "git+file://"+repo+"?ref=v2")
	out.Reset()
	if rc := packMain([]string{"status"}, &out, &errw, false); rc == 0 {
		t.Errorf("status exited 0 with a drifted fork pin:\n%s", out.String())
	}
	for _, want := range []string{"its source changed since it was pinned", "yolo pack install"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
}

// gitOut runs git in dir and returns its output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = packsrc.CleanGitEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// A SECOND MACHINE, GIVEN THE CONFIG AND ITS FORK LOCK: `yolo pack install` makes the pinned commit
// buildable here — its mirror fetched and the commit checked out into the pack store — and leaves
// the pin where the lock has it, though the branch has moved on. A launch never fetches (FP-D7), so
// an install that only read the lock would leave every build on this machine failing its checkout
// until `yolo pack update`, which builds a different commit than the first machine runs.
func TestPackInstallMakesAPinnedForkBuildableOnASecondMachine(t *testing.T) {
	repo, commit := forkRepo(t)
	head1 := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	forkPinHome(t, source)
	// The lock arrived with the config; this machine's pack store has never seen the repository.
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: source, Ref: "main", Commit: head1})
	if err := l.Save(forkLockPath()); err != nil {
		t.Fatal(err)
	}
	commit("moved on")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if got := pinnedCommit(t); got != head1 {
		t.Errorf("install moved the pin to %q, want the lock's %q", got, head1)
	}
	// Offline from here, as a launch is: the build's checkout reads only the pack store.
	if err := os.Rename(repo, repo+".gone"); err != nil {
		t.Fatal(err)
	}
	if err := checkOutForkSource(source, head1, filepath.Join(t.TempDir(), "src")); err != nil {
		t.Errorf("after install the pinned commit cannot be checked out on this machine: %v\n%s\n%s",
			err, out.String(), errw.String())
	}
}
