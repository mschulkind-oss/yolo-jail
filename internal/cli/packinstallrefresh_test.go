package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// packinstallrefresh_test.go pins what `yolo pack install` still means now that a launch
// fetches: the EXPLICIT fetch, forced for every git pack — a tag pin included, which a launch
// never re-fetches — and a failed fetch is a failed install even with a cached copy.

// gitCmd runs git in dir with a clean environment and a fixed identity.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A TAG PIN: a launch keeps it where it is after the author re-points it; `yolo pack install`
// moves it, and reports the move.
func TestPackInstallForcesATagPinThatALaunchKeeps(t *testing.T) {
	repo := gitPackRepo(t)
	gitCmd(t, repo, "tag", "v1")
	c1 := gitCmd(t, repo, "rev-parse", "HEAD")
	neverInstalledGitPackHomeAt(t, "git+file://"+repo+"//tools/agent-pack?ref=v1")
	installGitPack(t)

	if err := os.WriteFile(filepath.Join(repo, "tools", "agent-pack", "x"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-qm", "two")
	gitCmd(t, repo, "tag", "-f", "v1")
	c2 := gitCmd(t, repo, "rev-parse", "HEAD")

	var errw bytes.Buffer
	refreshHostPacks(&errw) // a launch-shaped refresh: the tag is frozen
	if got := lockedCommit(t, "gp"); got != c1 {
		t.Fatalf("a launch moved the tag pin to %s (want %s)\n%s", got, c1, errw.String())
	}
	var out bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if got := lockedCommit(t, "gp"); got != c2 {
		t.Errorf("install did not move the re-pointed tag: locked %s, want %s", got, c2)
	}
	if !strings.Contains(out.String(), c1[:8]+" → "+c2[:8]) {
		t.Errorf("install did not report the move:\n%s", out.String())
	}
}

// A FAILED EXPLICIT FETCH IS A FAILED INSTALL, though the cached commit stays locked and usable.
func TestPackInstallFailsWhenItsFetchFails(t *testing.T) {
	repo := gitPackRepo(t)
	neverInstalledGitPackHomeAt(t, "git+file://"+repo+"//tools/agent-pack?ref=main")
	installGitPack(t)
	c1 := lockedCommit(t, "gp")
	if err := os.Rename(repo, repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(repo+".gone", repo) })

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc == 0 {
		t.Errorf("install succeeded although its fetch failed\n%s%s", out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "could not refresh") || !strings.Contains(errw.String(), "gp") {
		t.Errorf("the failed fetch was not named:\n%s", errw.String())
	}
	if got := lockedCommit(t, "gp"); got != c1 {
		t.Errorf("the lock entry changed on a failed fetch: %s -> %s", c1, got)
	}
}

// THE HELP SAYS WHAT A LAUNCH DOES NOW: it fetches, a branch refreshes hourly, and a tag or
// commit pin freezes a pack. The sentence it replaces claimed fetching never happens at launch.
func TestPackHelpSaysALaunchFetches(t *testing.T) {
	var out, errw bytes.Buffer
	packMain([]string{"--help"}, &out, &errw, false)
	help := out.String()
	for _, stale := range []string{"never at launch", "never touches the network", "fetching only ever happens there"} {
		if strings.Contains(help, stale) {
			t.Errorf("the pack help still says %q", stale)
		}
	}
	for _, want := range []string{"A LAUNCH FETCHES", "over an hour old", "how\nyou freeze a pack",
		"The next launch fetches a git pack it has never fetched"} {
		if !strings.Contains(help, want) {
			t.Errorf("the pack help does not say %q", want)
		}
	}
}

// neverInstalledGitPackHomeAt is neverInstalledGitPackHome for an explicit source.
func neverInstalledGitPackHomeAt(t *testing.T, src string) string {
	t.Helper()
	home := gitPackHome(t, src, "")
	t.Setenv("YOLO_VERSION", "")
	return home
}
