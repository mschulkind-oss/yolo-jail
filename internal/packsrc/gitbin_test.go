package packsrc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// fakeDarwinGit makes storeGit's host darwin, with PATH resolving git to lookPath and xcrun naming
// found, and counts the xcrun questions. Not parallel: it swaps package seams.
func fakeDarwinGit(t *testing.T, lookPath string, found string, findErr error) *int {
	t.Helper()
	goos, look, find := gitGOOS, gitLookPath, xcrunFindGit
	asked := 0
	gitGOOS = "darwin"
	gitLookPath = func(string) (string, error) {
		if lookPath == "" {
			return "", errors.New("not found")
		}
		return lookPath, nil
	}
	xcrunFindGit = func() (string, error) { asked++; return found, findErr }
	shimTargets.Clear()
	t.Cleanup(func() {
		gitGOOS, gitLookPath, xcrunFindGit = goos, look, find
		shimTargets.Clear()
	})
	return &asked
}

func executable(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// ON DARWIN A STORE WITH NO GIT SET RUNS THE GIT APPLE'S SHIM RUNS, asking xcrun once a process:
// PATH resolving git to /usr/bin/git, Store.git() is xcrun's answer, the developer tools' git,
// and a second store asks nothing again.
func TestADarwinStoreRunsTheGitTheShimRuns(t *testing.T) {
	clt := executable(t)
	asked := fakeDarwinGit(t, appleGitShim, clt, nil)
	t.Setenv("DEVELOPER_DIR", "")
	if got := (&Store{}).git(); got != clt {
		t.Errorf("Store.git() = %q, want xcrun's %q", got, clt)
	}
	if got := (&Store{}).git(); got != clt || *asked != 1 {
		t.Errorf("second Store.git() = %q after %d xcrun questions, want %q after one", got, *asked, clt)
	}
	if got := (&Store{Git: "/opt/git"}).git(); got != "/opt/git" {
		t.Errorf("a store's own Git %q became %q", "/opt/git", got)
	}
}

// EVERY OTHER ANSWER LEAVES "git": another host, a git on PATH that is not the shim (a Homebrew or
// nix git, or a test's wrapper), no git on PATH, and an xcrun that fails, names the shim itself,
// or names something that is not an executable file.
func TestStoreGitLeavesGitUnlessXcrunNamesTheShimsGit(t *testing.T) {
	clt := executable(t)
	notExec := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVELOPER_DIR", "")
	for name, c := range map[string]struct {
		goos, lookPath, found string
		err                   error
	}{
		"linux":             {"linux", appleGitShim, clt, nil},
		"homebrew git":      {"darwin", "/opt/homebrew/bin/git", clt, nil},
		"no git":            {"darwin", "", clt, nil},
		"xcrun fails":       {"darwin", appleGitShim, "", errors.New("no developer tools")},
		"xcrun names shim":  {"darwin", appleGitShim, appleGitShim, nil},
		"relative answer":   {"darwin", appleGitShim, "git", nil},
		"not an executable": {"darwin", appleGitShim, notExec, nil},
		"missing file":      {"darwin", appleGitShim, filepath.Join(t.TempDir(), "gone"), nil},
	} {
		fakeDarwinGit(t, c.lookPath, c.found, c.err)
		gitGOOS = c.goos
		if got := (&Store{}).git(); got != "git" {
			t.Errorf("%s: Store.git() = %q, want git", name, got)
		}
	}
}

// XCRUN IS ASKED AGAIN FOR ANOTHER DEVELOPER DIRECTORY, which selects another git as xcode-select
// does: the answer is kept per DEVELOPER_DIR.
func TestStoreGitAsksXcrunPerDeveloperDir(t *testing.T) {
	clt := executable(t)
	asked := fakeDarwinGit(t, appleGitShim, clt, nil)
	for _, dir := range []string{"", "/Applications/Xcode.app/Contents/Developer", ""} {
		t.Setenv("DEVELOPER_DIR", dir)
		(&Store{}).git()
	}
	if *asked != 2 {
		t.Errorf("xcrun asked %d times for two developer directories, want 2", *asked)
	}
}

// AN XCRUN THAT HANGS DOES NOT HOLD THE STORE: past xcrunBound the question is killed, the store
// runs plain "git", and nothing is kept, so the next store asks again.
func TestAHungXcrunLeavesGitAndIsAskedAgain(t *testing.T) {
	fakeDarwinGit(t, appleGitShim, "", nil)
	xcrunFindGit = askXcrunForGit // the real question, of the fake xcrun below
	bound := xcrunBound
	xcrunBound = 200 * time.Millisecond
	t.Cleanup(func() { xcrunBound = bound })
	dir := t.TempDir()
	asked := filepath.Join(dir, "asked")
	script := "#!/bin/sh\necho x >> " + shquote.Quote(asked) + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEVELOPER_DIR", "")
	for i := 1; i <= 2; i++ {
		start := time.Now()
		if got := (&Store{}).git(); got != "git" {
			t.Errorf("store %d: Store.git() = %q under a hung xcrun, want git", i, got)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("store %d: a hung xcrun held Store.git() for %s, bound %s", i, took, xcrunBound)
		}
		if _, kept := shimTargets.Load(""); kept {
			t.Errorf("store %d: a hung xcrun's non-answer was kept", i)
		}
	}
	if data, _ := os.ReadFile(asked); strings.Count(string(data), "x") != 2 {
		t.Errorf("xcrun asked %q times, want twice (once a store)", data)
	}
}
