package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// normalizeCases pins the byte contract for version normalization.
var normalizeCases = []struct {
	raw  string
	want string
}{
	{"0.1.0", "0.1.0"},                                   // exactly on tag
	{"v0.1.0", "0.1.0"},                                  // leading v stripped
	{"0.1.0-dirty", "0.1.0+dirty"},                       // dirty on tag — WITH the +
	{"v0.1.0-dirty", "0.1.0+dirty"},                      // ...and with a leading v
	{"0.1.0-3-gabcdef1", "0.1.0+3.gabcdef1"},             // commits past tag
	{"0.1.0-3-gabcdef1-dirty", "0.1.0+3.gabcdef1.dirty"}, // commits + dirty
	{"v0.6.0-19-g661ac98", "0.6.0+19.g661ac98"},          // a real describe from this repo
	{"1.2.3-rc1", "1.2.3-rc1"},                           // hyphenated base, no g-hash: base rejoined
	{"deadbeef", "deadbeef"},                             // --always fallback (no tag)
	{"deadbeef-dirty", "deadbeef+dirty"},                 // ...dirty
}

func TestNormalize(t *testing.T) {
	for _, tc := range normalizeCases {
		if got := Normalize(tc.raw); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestBuildVersionPrecedence pins the release-stamp contract (§2d of the
// distribution work): a binary stamped via -ldflags -X must report ITS OWN
// version even when invoked from inside a git repository — `git describe
// --always` succeeds in ANY repo, so describe-first would report the user's
// cwd repo, not the binary. These tests run inside the yolo-jail checkout,
// so live git describe IS available: buildVersion winning proves the order.
func TestBuildVersionPrecedence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	orig := buildVersion
	defer func() { buildVersion = orig }()

	buildVersion = "v9.9.9-2-gfeedbee"
	if got, want := gitDescribe(""), "9.9.9+2.gfeedbee"; got != want {
		t.Errorf("stamped binary: gitDescribe() = %q, want normalized stamp %q", got, want)
	}

	// YOLO_VERSION still beats the stamp (in-jail banner parity contract) and
	// is returned VERBATIM, never normalized.
	t.Setenv("YOLO_VERSION", "0.1.0-dirty")
	if got, want := gitDescribe(""), "0.1.0-dirty"; got != want {
		t.Errorf("YOLO_VERSION over stamp: gitDescribe() = %q, want %q", got, want)
	}
	t.Setenv("YOLO_VERSION", "")

	// A literal "unknown" stamp (pre-fix scripts/build-go.sh stamped this on
	// describe failure) must NOT shadow live git describe in a known repo
	// root (this checkout).
	buildVersion = "unknown"
	repo := checkoutRoot(t)
	// The describe reads this checkout under the developer's own git configuration, and
	// `describe --dirty` refreshes the index: with core.fsmonitor=true there, git started a
	// `git fsmonitor--daemon` for the checkout that outlived the suite. git's command-line
	// scope turns it off for this test's runs alone
	// (TestBuildVersionPrecedenceStartsNoFsmonitorDaemon).
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.fsmonitor'='false'")
	if got := gitDescribe(repo); got == "unknown" || got == "" {
		t.Errorf("legacy 'unknown' stamp shadowed live describe: gitDescribe(repo) = %q", got)
	}

	// Unstamped + no repo root: NEVER describe the process cwd (it could be
	// any repo the user is standing in — the tests themselves run inside the
	// yolo-jail checkout, so a cwd describe would return a version here).
	buildVersion = ""
	if got := gitDescribe(""); got != "" {
		t.Errorf("unstamped no-root binary described the cwd: gitDescribe(\"\") = %q, want \"\"", got)
	}
	if got := Get(""); got != "unknown" {
		t.Errorf("Get(\"\") = %q, want \"unknown\"", got)
	}
}

// checkoutRoot is the yolo-jail checkout these tests run in.
func checkoutRoot(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// fsmonitorWatching reports whether a git fsmonitor daemon is watching the worktree dir, asking
// git itself (`fsmonitor--daemon status` exits 0 only then), and has git stop one it finds when
// stop is set.
func fsmonitorWatching(t *testing.T, dir string, stop bool) bool {
	t.Helper()
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = packsrc.CleanGitEnv(os.Environ())
		return cmd.Run()
	}
	if git("fsmonitor--daemon", "status") != nil {
		return false
	}
	if stop {
		_ = git("fsmonitor--daemon", "stop")
	}
	return true
}

// THE LIVE DESCRIBE LEAVES NO FSMONITOR DAEMON BEHIND. TestBuildVersionPrecedence describes this
// checkout with the git configuration of the machine running the suite, and on one with
// core.fsmonitor=true (a setting GitHub recommends for large repositories) its
// `git describe --dirty` started a `git fsmonitor--daemon` for the checkout that kept running
// after the suite. It runs here on such a machine, and must leave none.
func TestBuildVersionPrecedenceStartsNoFsmonitorDaemon(t *testing.T) {
	repo := checkoutRoot(t)
	readable := exec.Command("git", "rev-parse", "--git-dir")
	readable.Dir, readable.Env = repo, packsrc.CleanGitEnv(os.Environ())
	if err := readable.Run(); err != nil {
		t.Skipf("git cannot read this checkout here (%v), so TestBuildVersionPrecedence cannot describe it", err)
	}
	if fsmonitorWatching(t, repo, false) {
		t.Skip("a git fsmonitor daemon already watches this checkout, so one this test started could not be told apart")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\tfsmonitor = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	_ = os.Unsetenv("GIT_CONFIG_GLOBAL")
	// A git that starts no daemon for the setting (a platform without the builtin daemon) would
	// pass this test without testing anything, so first see one start the way the describe's did.
	probe := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"status", "--porcelain"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = probe
		cmd.Env = packsrc.CleanGitEnv(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in the probe: %v\n%s", args, err, out)
		}
	}
	if !fsmonitorWatching(t, probe, true) {
		t.Skip("this git starts no fsmonitor daemon for core.fsmonitor=true")
	}
	t.Run("TestBuildVersionPrecedence", TestBuildVersionPrecedence)
	if fsmonitorWatching(t, repo, true) {
		t.Errorf("TestBuildVersionPrecedence left a git fsmonitor daemon watching %s", repo)
	}
}

func TestIsDigits(t *testing.T) {
	cases := map[string]bool{
		"":     false,
		"0":    true,
		"19":   true,
		"g123": false,
		"1a":   false,
		"-1":   false,
	}
	for in, want := range cases {
		if got := isDigits(in); got != want {
			t.Errorf("isDigits(%q) = %v, want %v", in, got, want)
		}
	}
}
