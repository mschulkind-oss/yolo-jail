package packsrc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hostgitconfig_test.go pins the store against the host user's own git configuration. The
// store runs on the host with the user's environment, on purpose: their ~/.gitconfig carries
// the credential helpers and insteadOf rewrites a private pack's fetch needs. So every other
// setting there reaches the store's git too, and a setting that is harmless in the user's
// own repositories can break a run inside the store's bare mirror. Each test here gives the
// store a global config holding one such setting and drives a refresh through every kind of
// run the store makes.
//
// Only the store's git reads the config (through Store.Env). The fixture's own git (gitRepo,
// gitIn) does not, so what these tests measure is the store's behavior alone.

// storeGlobalConfig writes a global git config holding body and returns an environment for
// Store.Env that makes the store's git read it, the way it reads a user's ~/.gitconfig.
func storeGlobalConfig(t *testing.T, body string) []string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(), "GIT_CONFIG_GLOBAL="+cfg)
}

// exerciseStore refreshes, under env, a pack at the repository root, one in a subdirectory,
// one pinned to a tag and one pinned to a commit on no branch or tag, then refreshes them again
// after the remote moved and the branch's interval passed, and resolves each offline. Between
// them that is every kind of git run the store makes: the clone, the fetch, the fetch of a
// pinned commit, the prefetch and the checkout, and the lookups (rev-parse, ls-tree, rev-list,
// for-each-ref, update-ref). Any failure is fatal.
func exerciseStore(t *testing.T, env []string) *refreshFixture {
	t.Helper()
	f := newRefreshFixture(t)
	gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
	commitFile(t, f.repo, "sub/pack.json", `{"name":"s"}`)
	gitIn(t, f.repo, "tag", "v1")
	c1 := f.head(t)
	gitIn(t, f.repo, "checkout", "-q", "-b", "feature")
	pinned := commitFile(t, f.repo, "three", "3")
	gitIn(t, f.repo, "checkout", "-q", "main")
	gitIn(t, f.repo, "branch", "-q", "-D", "feature")
	f.store.Env = env
	packs := []RefreshPack{
		{Name: "p", Source: f.source("main")},
		{Name: "s", Source: "git+file://" + f.repo + "//sub?ref=main"},
		{Name: "t", Source: f.source("v1")},
		{Name: "c", Source: f.source(pinned)},
	}
	for _, o := range f.refresh(t, false, packs...) {
		if o.Err != nil || o.FetchErr != nil || o.Commit == "" {
			t.Fatalf("first refresh: %+v", o)
		}
	}
	c2 := commitFile(t, f.repo, "two", "2")
	f.now = f.now.Add(2 * BranchRefreshInterval)
	outs := f.refresh(t, false, packs...)
	for _, o := range outs {
		if o.Err != nil || o.FetchErr != nil {
			t.Fatalf("second refresh: %+v", o)
		}
	}
	if outs[0].Commit != c2 || outs[1].Commit != c2 || outs[2].Commit != c1 || outs[3].Commit != pinned {
		t.Fatalf("second refresh resolved %s %s %s %s, want %s %s %s %s", outs[0].Commit, outs[1].Commit,
			outs[2].Commit, outs[3].Commit, c2, c2, c1, pinned)
	}
	for _, p := range packs {
		if _, err := f.store.Resolve(mustParse(t, p.Source), p.Name); err != nil {
			t.Fatalf("Resolve %s: %v", p.Name, err)
		}
	}
	return f
}

// THE STORE NAMES ITS MIRROR, NEVER DISCOVERS IT. safe.bareRepository=explicit (git 2.38) is
// a hardening setting a user may have globally: git then refuses to use a bare repository it
// found from the working directory, and allows only one named by --git-dir or GIT_DIR. The
// store's mirror is bare and every run inside it used to find it from the working directory,
// so every refresh and every resolution failed with "cannot use bare repository".
func TestStoreWorksUnderSafeBareRepositoryExplicit(t *testing.T) {
	env := storeGlobalConfig(t, "[safe]\n\tbareRepository = explicit\n")
	// A git that does not know the setting runs the bare repository anyway, and this test
	// would pass without testing anything.
	probe := t.TempDir()
	gitIn(t, probe, "init", "-q", "--bare")
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir, cmd.Env = probe, env
	if err := cmd.Run(); err == nil {
		t.Skip("this git does not enforce safe.bareRepository")
	}
	exerciseStore(t, env)
}

// Every run inside a repository names it with --git-dir, as an absolute path because git
// resolves the option against the working directory, which is that repository itself. The
// clone, which runs in no repository, names none.
func TestGitCmdNamesTheRepositoryItRunsIn(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Env: []string{"PATH=/bin"}}
	t.Chdir(t.TempDir())
	abs, err := filepath.Abs("mirrors/m")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir, want string
	}{
		{"mirrors/m", "--git-dir=" + abs},
		{abs, "--git-dir=" + abs},
		{"", ""},
	} {
		args := s.gitCmd(t.Context(), tc.dir, "rev-parse", "HEAD").Args
		got := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--git-dir=") {
				got = a
			}
		}
		if got != tc.want {
			t.Errorf("gitCmd(%q) argv %q: git dir %q, want %q", tc.dir, args, got, tc.want)
		}
	}
}
