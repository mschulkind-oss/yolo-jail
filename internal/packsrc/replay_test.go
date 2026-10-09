package packsrc

// replay_test.go pins the REPLAY and the newest-fit walk (replay.go; docs/design/patched-forks.md
// §5, §6.4) against a real blobless mirror: the cases the design measured — a member whose
// context moved upstream applies, an edit between the members' lines conflicts at the first, a
// member already upstream is clean — the walk stopping at the newest fit, the base that does not
// take its own series, a blob the mirror cannot get, and the replay's own config regime.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// checked runs a forced check of the fixture's main and returns the series and the list.
func (u *patchedUpstream) checked(t *testing.T) (*Series, []ListEntry) {
	t.Helper()
	res := u.check(t, u.want(t, "main", ""), true)
	if res.Record.Check.Problem != "" {
		t.Fatalf("check problem: %s", res.Record.Check.Problem)
	}
	return u.series(t), res.Record.Check.List
}

// handRebaseTree is the tree a hand rebase of the series onto commit gives, made in a full clone
// of the upstream with git's own rebase — what "applies cleanly" is measured against (§5.2).
func (u *patchedUpstream) handRebaseTree(t *testing.T, onto string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, filepath.Dir(clone), "clone", "-q", u.repo, clone)
	gitIn(t, clone, "checkout", "-q", "--detach", u.base)
	var files []string
	for _, m := range u.series(t).Members {
		files = append(files, filepath.Join(u.pack, "patches", m.Name))
	}
	gitIn(t, clone, append([]string{"am", "-q"}, files...)...)
	gitIn(t, clone, "rebase", "-q", "--onto", onto, u.base)
	return gitIn(t, clone, "rev-parse", "HEAD^{tree}")
}

// THE NEWEST FIT: the walk replays down the list, newest first, records the conflict at the version
// that does not take the series, and stops at the first that does — whose patched tree is a hand
// rebase's. v1.1.0 edits line 14, inside the second member's context only, which `git am --3way`
// could not replay and a rebase does.
func TestTheWalkStopsAtTheNewestFit(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	u.release(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	series, list := u.checked(t)
	if got := listLabels(list); got != "v1.2.0 v1.1.0 v1.0.0" {
		t.Fatalf("list = %q", got)
	}
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Err != nil || w.Base != nil {
		t.Fatalf("walk = %+v", w)
	}
	if len(w.Results) != 2 || w.Fit != 1 {
		t.Fatalf("walk replayed %d entries with fit %d, want the conflict then the fit:\n%+v", len(w.Results), w.Fit, w.Results)
	}
	c := w.Results[0]
	if c.Conflict == nil || c.Conflict.Member != series.Members[0].Name || strings.Join(c.Conflict.Paths, ",") != "f.txt" {
		t.Errorf("v1.2.0 = %+v, want a conflict at the first member in f.txt", c)
	}
	if failure := w.PatchFailure(); failure == nil || failure.Kind != "conflict" ||
		failure.Target.Commit != list[0].Commit || failure.Member != series.Members[0].Name {
		t.Errorf("classified first walk failure = %#v, want the concrete newest conflict", failure)
	}
	fit := w.Results[1]
	if !fit.Clean || fit.Entry.Commit != v11 || fit.Err != nil {
		t.Fatalf("v1.1.0 = %+v, want it clean", fit)
	}
	if want := u.handRebaseTree(t, v11); fit.Tree != want {
		t.Errorf("the patched tree is %s, and a hand rebase onto v1.1.0 gives %s", fit.Tree, want)
	}
	if w.Git == "" {
		t.Error("the walk does not report the git version it ran")
	}
}

// A MEMBER ALREADY UPSTREAM picks nothing and is clean, named so it can be dropped.
func TestAMemberAlreadyUpstreamIsClean(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{10: "ten", 25: "x"})
	series, list := u.checked(t)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list[:1], WalkOptions{})
	if w.Fit != 0 || len(w.Results) != 1 {
		t.Fatalf("walk = %+v", w)
	}
	if got := w.Results[0].Upstream; len(got) != 1 || got[0] != series.Members[0].Name {
		t.Errorf("already upstream = %v, want the first member", got)
	}
}

// A SERIES THAT DOES NOT APPLY AT ITS OWN BASE is malformed, the fork's reason, never a conflict.
func TestASeriesThatDoesNotApplyAtItsBase(t *testing.T) {
	u := newPatchedUpstream(t)
	series, list := u.checked(t)
	// The second member, rewritten to change a line its preimage does not hold.
	m := &series.Members[1]
	m.Data = []byte(strings.Replace(string(m.Data), "-12\n+twelve", "-no such line\n+twelve", 1))
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Base == nil || w.Base.Member != m.Name || len(w.Results) != 0 {
		t.Fatalf("walk = %+v, want the second member refused at the base", w)
	}
	if failure := w.PatchFailure(); failure == nil || failure.Kind != "base" || failure.Target.Commit != series.Base ||
		failure.Member != m.Name || failure.Detail == "" {
		t.Errorf("classified base failure = %#v", failure)
	}
	if !strings.Contains(w.Base.Error(), "git format-patch --base") {
		t.Errorf("the reason does not name the re-export: %s", w.Base)
	}
}

func TestAExecutedMemberApplicationCommandFailureIsClassifiedWithFullDiagnosis(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	u.store.Git = wrappedGit(t, `case " $* " in *" merge-tree "*) printf 'fatal: first diagnostic\nsecond diagnostic\n' >&2; exit 2;; esac`)
	addr := mustAddr(t, u.source("main"))
	walk := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{StopOnPatchFailure: true})
	failure := walk.PatchFailure()
	if failure == nil || failure.Kind != "application-command" || failure.Target.Commit != list[0].Commit ||
		failure.Member != series.Members[0].Name || !strings.Contains(failure.Detail, "fatal: first diagnostic") ||
		!strings.Contains(failure.Detail, "second diagnostic") {
		t.Fatalf("application command failure = walk %+v; classified %#v", walk, failure)
	}
}

func TestASignalledBaseApplicationCommandIsNotClassified(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	u.store.Git = wrappedGit(t, `case " $* " in *" am "*) kill -TERM $$;; esac`)
	addr := mustAddr(t, u.source("main"))
	walk := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{})
	if walk.PatchFailure() != nil || walk.Base != nil {
		t.Fatalf("signalled application command became fatal evidence: walk=%+v failure=%#v", walk, walk.PatchFailure())
	}
	if walk.Err == nil {
		t.Fatalf("signalled git am produced no operational error: %+v", walk)
	}
}

func TestACancelledApplicationCommandIsNotClassified(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	marker := filepath.Join(t.TempDir(), "merge-started")
	u.store.Git = wrappedGit(t, `case " $* " in *" merge-tree "*) echo started > `+shquote.Quote(marker)+`; exec sleep 30;; esac`)
	addr := mustAddr(t, u.source("main"))
	walk := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{Timeout: time.Second})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("merge-tree did not start before cancellation: %v; walk=%+v", err, walk)
	}
	if walk.PatchFailure() != nil {
		t.Fatalf("cancelled command became patch failure: %+v", walk.PatchFailure())
	}
}

// A BLOB THE MIRROR CANNOT GET is an apply error, never a conflict: the remote gone, a version the
// blobless mirror holds no file of yet stops the walk with the entry unsettled.
func TestABlobThatCannotBeFetchedIsAnApplyError(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	if err := os.Rename(u.repo, u.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(u.repo+".gone", u.repo) })
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Fit >= 0 {
		t.Fatalf("walk = %+v, want no fit", w)
	}
	var stopped error
	if w.Err != nil {
		stopped = w.Err
	} else if n := len(w.Results); n > 0 {
		stopped = w.Results[n-1].Err
		if w.Results[n-1].Conflict != nil {
			t.Errorf("a missing blob read as a conflict: %+v", w.Results[n-1])
		}
	}
	if stopped == nil || !strings.Contains(stopped.Error(), "could not fetch the upstream's files") {
		t.Errorf("walk = %+v, want an apply error naming the missing files", w)
	}
}

// THE REPLAY SEES NO USER GIT CONFIG (PF-D18). The user's global config here turns rename
// detection off for merges, signs every commit through a signer that does not exist, and judges
// whitespace as an error — and the upstream renamed the series' file while a member adds a
// trailing space. With no user config the replay applies to the new name, as the design measured
// (§5.2's rename row); under that config the pick would stop as a modify/delete conflict. (The
// package's tripwire, armed in TestMain, signs through the environment too.) The fetch keeps the
// user's config, which none of these settings disturbs.
func TestTheReplayIgnoresTheUsersGitConfig(t *testing.T) {
	u := renamedUpstream(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	writeTestFile(t, global, "[merge]\n\trenames = false\n[commit]\n\tgpgsign = true\n[gpg]\n\t"+
		"program = /nonexistent/signer\n[apply]\n\twhitespace = error\n[core]\n\tautocrlf = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	series, list := u.checked(t)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Err != nil || w.Base != nil || w.Fit != 0 {
		t.Fatalf("under the user's config the walk = %+v (base %v), want v1.1.0 to take the series", w, w.Base)
	}
	if want := u.handRebaseTree(t, list[0].Commit); w.Results[0].Tree != want {
		t.Errorf("the patched tree %s is not a hand rebase's %s", w.Results[0].Tree, want)
	}
}

// renamedUpstream is the fixture with a series whose second member adds a trailing space, and an
// upstream v1.1.0 that renamed the series' file f.txt to g.txt: the replay applies to the new name
// only with rename detection on (§5.2's rename row).
func renamedUpstream(t *testing.T) *patchedUpstream {
	t.Helper()
	u := newPatchedUpstream(t)
	gitIn(t, u.repo, "checkout", "-q", "-b", "ws")
	commitFile(t, u.repo, "f.txt", thirtyLines(map[int]string{10: "ten"}))
	commitFile(t, u.repo, "f.txt", thirtyLines(map[int]string{10: "ten", 5: "five "}))
	os.RemoveAll(filepath.Join(u.pack, "patches"))
	gitIn(t, u.repo, "format-patch", "-q", "--base="+u.base, "-o", filepath.Join(u.pack, "patches"), "main..ws")
	gitIn(t, u.repo, "checkout", "-q", "main")
	gitIn(t, u.repo, "mv", "f.txt", "g.txt")
	gitIn(t, u.repo, "commit", "-qm", "rename")
	gitIn(t, u.repo, "tag", "v1.1.0")
	return u
}

// THE REPLAY TAKES NO TEMPLATE AND NO GIT VARIABLE OF THE USER'S (PF-D29). A template directory
// whose config turns rename detection off and whose attributes merge every file as binary would
// land in the scratch repository as its own config, which no `-c` overrides, and a default hash of
// sha256 would make a scratch repository that cannot borrow the mirror's objects: under both, the
// renamed upstream still takes the series, as a hand rebase does.
func TestTheReplayIgnoresTheUsersGitTemplateAndVariables(t *testing.T) {
	u := renamedUpstream(t)
	series, list := u.checked(t)
	want := u.handRebaseTree(t, list[0].Commit)
	tpl := t.TempDir()
	writeTestFile(t, filepath.Join(tpl, "config"), "[merge]\n\trenames = false\n")
	writeTestFile(t, filepath.Join(tpl, "info", "attributes"), "* -text merge=binary\n")
	t.Setenv("GIT_TEMPLATE_DIR", tpl)
	t.Setenv("GIT_DEFAULT_HASH", "sha256")
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Err != nil || w.Base != nil || w.Fit != 0 {
		t.Fatalf("under the user's template the walk = %+v (base %v), want v1.1.0 to take the series", w, w.Base)
	}
	if w.Results[0].Tree != want {
		t.Errorf("the patched tree %s is not a hand rebase's %s", w.Results[0].Tree, want)
	}
}

// replayEnv drops every GIT_ variable but where git finds its own programs, whatever its name.
func TestTheReplayEnvKeepsNoGitVariableButItsExecPath(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_TEMPLATE_DIR", "/t"}, {"GIT_ATTR_SOURCE", "HEAD"},
		{"GIT_DEFAULT_HASH", "sha256"}, {"GIT_SOME_LATER_KNOB", "1"}, {"GIT_DIR", "/elsewhere"},
		{"GIT_EXEC_PATH", "/opt/git/libexec"}} {
		t.Setenv(kv[0], kv[1])
	}
	ours := map[string]bool{"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true, "GIT_ATTR_NOSYSTEM": true,
		"GIT_NO_LAZY_FETCH": true, "GIT_TERMINAL_PROMPT": true, "GIT_ASKPASS": true}
	kept := false
	for _, kv := range replayEnv() {
		key, val, _ := strings.Cut(kv, "=")
		switch {
		case key == "GIT_EXEC_PATH":
			kept = val == "/opt/git/libexec"
		case strings.HasPrefix(key, "GIT_AUTHOR_"), strings.HasPrefix(key, "GIT_COMMITTER_"), ours[key]:
		case strings.HasPrefix(key, "GIT_"):
			t.Errorf("the replay inherits %s", kv)
		}
	}
	if !kept {
		t.Error("the replay dropped GIT_EXEC_PATH, where git finds its own programs")
	}
}

// NOTHING THE REPLAY MAKES OUTLIVES IT: its scratch repository is in a private temporary directory,
// removed when the walk ends, and nothing is checked out in the pack store.
func TestTheReplayLeavesNothingBehind(t *testing.T) {
	u := newPatchedUpstream(t)
	series, list := u.checked(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Fit != 0 {
		t.Fatalf("walk = %+v", w)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("the replay left %d entries in its temporary directory", len(ents))
	}
	mustNotExist(t, filepath.Join(u.store.Dir, "trees"), "the replay checked a tree out in the pack store")
	if ents, _ := os.ReadDir(u.pack); len(ents) != 1 {
		t.Errorf("the replay wrote into the fork pack: %v", ents)
	}
}

// A SUBDIRECTORY SOURCE: the series is replayed over the whole commit, and the patched tree is the
// subdirectory's.
func TestThePatchedTreeIsTheSubdirectorys(t *testing.T) {
	u := newPatchedUpstream(t)
	commitFile(t, u.repo, "sub/x.txt", "x\n")
	series, list := u.checked(t)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "sub", series, list[:0], WalkOptions{})
	if w.Err != nil {
		t.Fatal(w.Err)
	}
	tip := gitIn(t, u.repo, "rev-parse", "HEAD")
	w = u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "sub", series,
		[]ListEntry{{Commit: tip, Tip: true}}, WalkOptions{})
	if w.Fit != 0 {
		t.Fatalf("walk = %+v", w)
	}
	if want := gitIn(t, u.repo, "rev-parse", "HEAD:sub"); w.Results[0].Tree != want {
		t.Errorf("patched tree = %s, want the subdirectory's %s (the series does not touch it)", w.Results[0].Tree, want)
	}
}

func mustAddr(t *testing.T, s string) Addr {
	t.Helper()
	a, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A GIT TOO OLD FOR THE REPLAY is a GitTooOldError at the walk and at the rebase clone alike, before
// either runs anything: it names the git it found and the one it needs, so a caller can name updating
// git as the next step rather than a retry that meets the same git.
func TestAGitTooOldForTheReplayIsAGitTooOldError(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	u.store.Git = wrappedGit(t, `if [ "$1" = version ]; then echo "git version 2.39.5"; exit 0; fi`)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, []ListEntry{{Commit: v11}}, WalkOptions{})
	r := u.rebase(t, series, ListEntry{Commit: v11}, filepath.Join(t.TempDir(), "clone"))
	for what, err := range map[string]error{"the walk": w.Err, "the rebase clone": r.Err} {
		var old *GitTooOldError
		if !errors.As(err, &old) || old.Have != "2.39.5" {
			t.Errorf("%s under git 2.39.5 returned %v, want a GitTooOldError naming 2.39.5", what, err)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "needs git 2.40 or newer") || !strings.Contains(msg, "this host's git is 2.39.5") {
			t.Errorf("%s's error says %q", what, msg)
		}
	}
	if len(w.Results) != 0 || r.Kept {
		t.Errorf("an old git replayed %d entries or kept a clone (%v)", len(w.Results), r.Kept)
	}
}

func TestGitAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"2.55.0": true, "2.40.0": true, "2.39.5": false, "1.9": false,
		"3.0.0": true, "2.45.windows.1": true, "weird": true} {
		if got := gitAtLeast(v, replayMinGit); got != want {
			t.Errorf("gitAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}
