package check

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostEnv is an injected environment for a HOST check run: no YOLO_VERSION (so not in a
// jail) and no YOLO_PACK_ROOT (so no staged-tree fallback can answer for a pack). This repo
// is developed from inside its own jail, where both are always set, so a test reading the
// ambient environment would be measuring the machine.
func hostEnv(string) string { return "" }

// jailEnv is hostEnv as seen from inside a jail that staged no packs.
func jailEnv(k string) string {
	if k == "YOLO_VERSION" {
		return "0.0.0-test"
	}
	return ""
}

// assertNoMirrors fails when the pack store grew a mirror: `yolo check` fetches nothing.
func assertNoMirrors(t *testing.T) {
	t.Helper()
	if entries, _ := os.ReadDir(filepath.Join(paths.PacksDir(), "mirrors")); len(entries) != 0 {
		t.Errorf("`yolo check` created a pack mirror, so it fetched: %v", entries)
	}
}

// checkGitRepo makes a real git repository with a pack at sub/ and returns its path. Real
// git, because the classification under test reads the REAL store's errors, and a mocked
// store would keep passing after internal/packsrc reworded them.
func checkGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	skill := filepath.Join(repo, "sub", "skills", "s")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"),
		[]byte("---\nname: s\ndescription: a test skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "initial")
	return repo
}

// syncInto fetches repo into this HOME's pack store the way `yolo pack install` does (a
// forced Refresh, which fetches and checks out), at the given ref, so a case can then name
// a ref the resulting mirror does not hold.
func syncInto(t *testing.T, repo, ref string) {
	t.Helper()
	src := "git+file://" + repo + "//sub?ref=" + ref
	if _, err := packsrc.Parse(src); err != nil {
		t.Skipf("the pack grammar does not accept a local git transport: %v", err)
	}
	outs, err := (&packsrc.Store{Dir: paths.PacksDir()}).Refresh(
		[]packsrc.RefreshPack{{Name: "fixture", Source: src}}, packsrc.RefreshOptions{Force: true})
	if err != nil {
		t.Fatalf("fetching the pack into the store: %v", err)
	}
	if o := outs[0]; o.Err != nil && ref == "main" {
		t.Fatalf("fetching the pack into the store: %v", o.Err)
	}
}

// A GIT PACK THE NEXT LAUNCH WOULD FETCH IS A [SKIP], NOT A [FAIL] — and only that class.
//
// A host launch runs the pack refresh step before it resolves anything, and that step
// fetches a git pack whose mirror is absent or whose ref the mirror does not hold
// (docs/reference/pack-system.md, "Fetch, refresh, lock"). `yolo check` still never fetches,
// so it cannot look inside such a pack, and says so without failing. Everything a fetch does
// NOT repair stays a [FAIL], because `yolo check` passing a config the launch refuses is the
// one outcome the Packs section exists to prevent.
//
// Driven through the REAL pack store, because the classification reads the store's typed
// errors (Options.launchWouldRepair) and a mocked store would keep passing after the store
// changed which failures carry them.
func TestSectionPacksSkipsAPackTheLaunchWouldFetch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sync     bool   // fetch the repository into the store first
		syncRef  string // the ref that fetch was for (default main)
		later    bool   // commit v2 upstream after the fetch, and fetch it without a checkout
		suffix   string // appended to git+file://<repo>
		env      func(string) string
		wantSkip bool
		wantFail bool
		wantPass bool
		skipText string // the [SKIP] line expected (default: the not-fetched one)
	}{
		{name: "never fetched", suffix: "//sub?ref=main", env: hostEnv, wantSkip: true},
		{name: "ref not in the mirror", sync: true, suffix: "//sub?ref=not-yet-pushed",
			env: hostEnv, wantSkip: true},
		// A ref a SUCCESSFUL fetch was run for and did not find — a typo — fails every
		// launch, so check must not pass it.
		{name: "ref absent after a fetch", sync: true, syncRef: "mian", suffix: "//sub?ref=mian",
			env: hostEnv, wantFail: true},
		// A commit the mirror holds but has not checked out: check must not check it out
		// (a partial mirror's checkout fetches blobs), and the launch will.
		{name: "commit not checked out", sync: true, later: true, suffix: "//sub?ref=v2",
			env: hostEnv, wantSkip: true, skipText: "gp: not checked out yet — the next launch checks it out"},
		// A fetch does not create a directory the commit lacks, so the launch refuses it.
		{name: "subpath absent at the commit", sync: true, suffix: "//nosuch?ref=main",
			env: hostEnv, wantFail: true},
		// In a jail the refresh step does nothing, so a nested launch refuses the pack.
		{name: "never fetched, in a jail", suffix: "//sub?ref=main", env: jailEnv,
			wantFail: true},
		// The control: a fetched pack resolves and passes, so the cases above are measuring
		// the classification and not a fixture that can never resolve.
		{name: "fetched", sync: true, suffix: "//sub?ref=main", env: hostEnv, wantPass: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := checkGitRepo(t)
			source := "git+file://" + repo + tc.suffix
			packsFixture(t, `{"packs": [{"source": "`+source+`", "name": "gp"}]}`)
			if tc.sync {
				ref := tc.syncRef
				if ref == "" {
					ref = "main"
				}
				syncInto(t, repo, ref)
			}
			if tc.later {
				laterTagWithoutCheckout(t, repo)
			}
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{Getenv: tc.env}).sectionPacks(r, jsonx.NewOrderedMap())
			out := buf.String()

			if got := r.skipped == 1; got != tc.wantSkip {
				t.Errorf("skipped=%d, want a [SKIP]=%v:\n%s", r.skipped, tc.wantSkip, out)
			}
			if got := r.failed > 0; got != tc.wantFail {
				t.Errorf("failed=%d, want a [FAIL]=%v:\n%s", r.failed, tc.wantFail, out)
			}
			want := tc.skipText
			if want == "" {
				want = "gp: not in the pack store yet — the next launch fetches it"
			}
			if tc.wantSkip && !strings.Contains(out, want) {
				t.Errorf("the [SKIP] must name the pack and what the launch does (%q):\n%s", want, out)
			}
			if tc.wantPass && !strings.Contains(out, "gp: 1 file(s) stage") {
				t.Errorf("the fetched control did not stage:\n%s", out)
			}
			if !tc.sync {
				assertNoMirrors(t)
			}
			if tc.later {
				assertNoTree(t, repo, "v2")
			}
		})
	}
}

// laterTagWithoutCheckout commits and tags v2 upstream, then brings it into the store's
// mirror with a bare fetch and no checkout — the state a mirror is in when another pack's
// fetch brought a ref this one has not been resolved at yet.
func laterTagWithoutCheckout(t *testing.T, repo string) {
	t.Helper()
	gitAt(t, repo, "commit", "-q", "--allow-empty", "-m", "v2")
	gitAt(t, repo, "tag", "v2")
	mirrors, err := os.ReadDir(filepath.Join(paths.PacksDir(), "mirrors"))
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("fixture: want one mirror: %v %v", mirrors, err)
	}
	gitAt(t, filepath.Join(paths.PacksDir(), "mirrors", mirrors[0].Name()),
		"fetch", "-q", "origin", "+refs/tags/*:refs/tags/*")
}

// assertNoTree fails when the store checked out repo's ref: `yolo check` writes nothing.
func assertNoTree(t *testing.T, repo, ref string) {
	t.Helper()
	sha := strings.TrimSpace(gitAt(t, repo, "rev-parse", ref+"^{commit}"))
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "trees", sha)); err == nil {
		t.Errorf("`yolo check` checked %s out into the pack store", ref)
	}
}

// gitAt runs git in dir with a clean environment and a fixed identity.
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(packsrc.CleanGitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// DRIFT NAMES THE REMEDY THAT ARRIVES. A launch rewrites a drifted GIT pack's lock entry (its
// refresh records what it fetched); it never records a local pack, so a drifted local entry
// must be sent to install, not told the next launch rewrites it. The git half is the control.
func TestSectionPacksDriftRemedyFollowsThePackKind(t *testing.T) {
	for _, tc := range []struct {
		name, source, want, notWant string
	}{
		{"local", "file://" + t.TempDir(), "a local pack: run `yolo pack install`", "next host launch"},
		{"git", "git+file:///nonexistent/yolo-test/x.git?ref=main", "the next host launch fetches the config address", "a local pack:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packsFixture(t, `{"packs": [{"source": "`+tc.source+`", "name": "gp"}]}`)
			seed := &packsrc.Lock{Packs: map[string]packsrc.LockEntry{
				"gp": {Name: "gp", Source: "file:///somewhere/else"},
			}}
			if err := seed.Save(packsrc.LockPath(paths.UserConfigPath())); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			(&Options{Getenv: hostEnv}).sectionPacks(&reporter{w: &buf}, jsonx.NewOrderedMap())
			out := buf.String()
			if !strings.Contains(out, "config address changed since the lock was written") ||
				!strings.Contains(out, tc.want) || strings.Contains(out, tc.notWant) {
				t.Errorf("drift report for a %s pack, want %q and not %q:\n%s", tc.name, tc.want, tc.notWant, out)
			}
		})
	}
}
