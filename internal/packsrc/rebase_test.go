package packsrc

// rebase_test.go pins the REBASE CLONE (rebase.go; docs/design/patched-forks.md §8.4, PF-D13,
// PF-D47) against a real upstream served blobless over file://: a conflicting target leaves the
// clone mid-rebase at the member the walk stops at, with the markers in its work tree and the
// upstream as its promisor; a target the series fits leaves nothing; the clone reads no template
// and its replay no git config of the user's; a clone that cannot be made is removed; and the
// marker tells a rebase clone from a directory the verb must not touch.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// rebaseFixture is the patched upstream with a v1.1.0 that edits line 11, between the series'
// two members' lines (§5.2's conflict row): the walk stops at the first member there.
func rebaseFixture(t *testing.T) (u *patchedUpstream, v11 string, series *Series) {
	t.Helper()
	u = newPatchedUpstream(t)
	v11 = u.release(t, "v1.1.0", map[int]string{11: "eleven"})
	series, _ = u.checked(t)
	return u, v11, series
}

func (u *patchedUpstream) rebase(t *testing.T, series *Series, target ListEntry, dir string) RebaseResult {
	t.Helper()
	return u.store.RebaseClone(RebaseOptions{Owner: "forkpack/tool", Repo: mustAddr(t, u.source("main")).Repo,
		Series: series, Target: target, Dir: dir, Now: u.now})
}

// A CONFLICT LEAVES THE CLONE MID-REBASE: stopped at the member the walk stops at, its paths
// conflicting in the work tree with git's markers, the upstream its promisor, and its marker saying
// whose it is and what it was rebased onto. Nothing is written into the fork pack or the pack
// store's trees.
func TestARebaseCloneStopsAtTheConflictWithItsMarkers(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	packBefore := patchNames(t, u.pack)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11, Tag: "v1.1.0"}, dir)
	if r.Err != nil || r.Base != nil || r.Clean || r.Conflict == nil || !r.Kept {
		t.Fatalf("rebase = %+v, want a conflict kept in the clone", r)
	}
	if r.Conflict.Member != series.Members[0].Name || strings.Join(r.Conflict.Paths, ",") != "f.txt" {
		t.Errorf("the rebase stopped at %+v, want the first member in f.txt", r.Conflict)
	}
	if !RebaseInProgress(dir) {
		t.Error("the clone is not mid-rebase, so `git rebase --continue` has nothing to resume")
	}
	body, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err != nil || !strings.Contains(string(body), "<<<<<<<") || !strings.Contains(string(body), ">>>>>>>") {
		t.Errorf("f.txt holds no conflict markers (%v):\n%s", err, body)
	}
	if got := gitIn(t, dir, "config", "remote.origin.url"); got != "file://"+u.repo {
		t.Errorf("the clone's origin is %q, want the upstream's URL", got)
	}
	if got := gitIn(t, dir, "config", "remote.origin.promisor"); got != "true" {
		t.Errorf("the clone is not a blobless clone of the upstream (promisor %q), so the user's git cannot fetch what it lacks", got)
	}
	state, m, err := InspectRebaseDir(dir)
	if err != nil || state != RebaseDirClone || m.Owner != "forkpack/tool" || m.Target.Commit != v11 ||
		m.Base != series.Base || m.Repo != "file://"+u.repo {
		t.Errorf("the clone reads as %v with marker %+v (%v)", state, m, err)
	}
	if strings.Contains(gitIn(t, dir, "status", "--porcelain", "--ignored"), RebaseMarkerName) {
		t.Error("the marker is in the clone's work tree, where an export or a `git add -A` would take it")
	}
	if got := patchNames(t, u.pack); strings.Join(got, ",") != strings.Join(packBefore, ",") {
		t.Errorf("the fork pack's series changed: %v → %v", packBefore, got)
	}
	mustNotExist(t, filepath.Join(u.store.Dir, "trees"), "the rebase checked a tree out in the pack store")
}

// A TARGET THE SERIES FITS leaves nothing behind, and names a member already upstream; a directory
// the user made empty for the clone is emptied again, not removed.
func TestARebaseCloneOfATargetThatFitsIsRemoved(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{10: "ten", 25: "x"})
	series, _ := u.checked(t)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11}, dir)
	if !r.Clean || r.Kept || r.Err != nil || len(r.Upstream) != 1 || r.Upstream[0] != series.Members[0].Name {
		t.Fatalf("rebase = %+v, want clean with the first member already upstream", r)
	}
	mustNotExist(t, dir, "the clean rebase left its clone")

	made := t.TempDir()
	if r := u.rebase(t, series, ListEntry{Commit: v11}, made); !r.Clean {
		t.Fatalf("rebase into an empty directory = %+v", r)
	}
	if ents, err := os.ReadDir(made); err != nil || len(ents) != 0 {
		t.Errorf("the directory the user made is %v (%v), want it there and empty", ents, err)
	}
}

// THE CLONE READS NO TEMPLATE, AND ITS REPLAY NO GIT CONFIG, OF THE USER'S (PF-D47, PF-D29): a
// template whose config turns rename detection off, whose attributes merge every file as binary and
// whose hook would run at a checkout, and a global config that turns renames off, signs through a
// signer that does not exist and judges whitespace as an error — and the upstream renamed the
// series' file while a member adds a trailing space. The rebase is clean onto the new name, as a
// hand rebase is, and no hook of the template's is in the clone.
func TestARebaseCloneTakesNoTemplateOrConfigOfTheUsers(t *testing.T) {
	u := renamedUpstream(t)
	series, list := u.checked(t)
	tpl := t.TempDir()
	writeTestFile(t, filepath.Join(tpl, "config"), "[merge]\n\trenames = false\n")
	writeTestFile(t, filepath.Join(tpl, "info", "attributes"), "* -text merge=binary\n")
	writeTestFile(t, filepath.Join(tpl, "hooks", "post-checkout"), "#!/bin/sh\ntouch \"$GIT_DIR/../HOOK-RAN\"\n")
	if err := os.Chmod(filepath.Join(tpl, "hooks", "post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	writeTestFile(t, global, "[init]\n\ttemplateDir = "+tpl+"\n[merge]\n\trenames = false\n[commit]\n\tgpgsign = true\n"+
		"[gpg]\n\tprogram = /nonexistent/signer\n[apply]\n\twhitespace = error\n[core]\n\tautocrlf = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_TEMPLATE_DIR", tpl)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, list[0], dir)
	if !r.Clean || r.Err != nil || r.Base != nil {
		t.Fatalf("under the user's template and config the rebase = %+v (base %v), want v1.1.0 clean", r, r.Base)
	}

	// The same, kept: a conflicting target leaves a clone whose git directory has no template's hook
	// or config in it.
	c := u.release(t, "v1.2.0", map[int]string{10: "TEN"})
	dir = filepath.Join(t.TempDir(), "clone")
	if r := u.rebase(t, series, ListEntry{Commit: c}, dir); r.Conflict == nil {
		t.Fatalf("rebase onto a conflicting commit = %+v", r)
	}
	mustNotExist(t, filepath.Join(dir, ".git", "hooks", "post-checkout"), "the user's template hook is in the clone")
	mustNotExist(t, filepath.Join(dir, "HOOK-RAN"), "a template hook ran in the clone")
	if cfg, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); strings.Contains(string(cfg), "renames") {
		t.Errorf("the template's config is the clone's own:\n%s", cfg)
	}
}

// A CLONE THAT CANNOT BE MADE (the upstream gone) is an apply error, and leaves nothing behind.
func TestARebaseCloneThatCannotBeMadeIsRemoved(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	if err := os.Rename(u.repo, u.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(u.repo+".gone", u.repo) })
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11}, dir)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "cloning") || r.Kept {
		t.Fatalf("rebase = %+v, want the clone's error", r)
	}
	mustNotExist(t, dir, "a failed clone left its directory")
}

// A SERIES THAT DOES NOT APPLY AT ITS OWN BASE is the series' fault, said as such, and the clone is
// removed.
func TestARebaseCloneOfASeriesBrokenAtItsBase(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	m := &series.Members[1]
	m.Data = []byte(strings.Replace(string(m.Data), "-12\n+twelve", "-no such line\n+twelve", 1))
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11}, dir)
	if r.Base == nil || r.Base.Member != m.Name || r.Kept {
		t.Fatalf("rebase = %+v, want the second member refused at the base", r)
	}
	mustNotExist(t, dir, "a series broken at its base left its clone")
}

// THE MARKER TELLS A REBASE CLONE FROM WHAT THE VERB MUST NOT TOUCH: nothing, an empty directory,
// a directory with anything in it, a git repository no rebase made, and a marker of another schema
// are told apart, and RemoveRebaseClone removes only its owner's clone.
func TestInspectRebaseDirAndRemove(t *testing.T) {
	root := t.TempDir()
	if s, _, _ := InspectRebaseDir(filepath.Join(root, "absent")); s != RebaseDirAbsent {
		t.Errorf("an absent directory reads as %v", s)
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := InspectRebaseDir(empty); s != RebaseDirEmpty {
		t.Errorf("an empty directory reads as %v", s)
	}
	other := gitRepo(t, map[string]string{"x": "x\n"})
	if s, _, _ := InspectRebaseDir(other); s != RebaseDirOther {
		t.Errorf("a repository no rebase made reads as %v", s)
	}
	if err := RemoveRebaseClone(other, "forkpack/tool"); err == nil {
		t.Error("RemoveRebaseClone removed a repository no rebase made")
	}
	writeTestFile(t, filepath.Join(other, ".git", RebaseMarkerName), `{"schema":99,"owner":"forkpack/tool"}`)
	if s, _, _ := InspectRebaseDir(other); s != RebaseDirOther {
		t.Errorf("a marker of another schema reads as %v", s)
	}
	data, _ := json.Marshal(RebaseMarker{Schema: RebaseMarkerSchema, Owner: "other/tool"})
	writeTestFile(t, filepath.Join(other, ".git", RebaseMarkerName), string(data))
	if s, m, _ := InspectRebaseDir(other); s != RebaseDirClone || m.Owner != "other/tool" {
		t.Errorf("another fork's clone reads as %v %+v", s, m)
	}
	if err := RemoveRebaseClone(other, "forkpack/tool"); err == nil {
		t.Error("RemoveRebaseClone removed another fork's clone")
	}
	if err := RemoveRebaseClone(other, "other/tool"); err != nil {
		t.Errorf("RemoveRebaseClone of its owner's clone: %v", err)
	}
	mustNotExist(t, other, "the owner's clone was not removed")
}

// --onto RESOLVES IN THE MIRROR, with no network: a tag (named as one), a branch, an abbreviated
// commit, and refusals for what names nothing and for a flag.
func TestResolveRebaseTarget(t *testing.T) {
	u, v11, _ := rebaseFixture(t)
	repo := mustAddr(t, u.source("main")).Repo
	if e, err := u.store.ResolveRebaseTarget(repo, "v1.1.0", nil); err != nil || e.Commit != v11 || e.Tag != "v1.1.0" {
		t.Errorf("v1.1.0 → %+v (%v)", e, err)
	}
	if e, err := u.store.ResolveRebaseTarget(repo, "main", nil); err != nil || e.Commit != v11 || e.Tag != "" {
		t.Errorf("main → %+v (%v)", e, err)
	}
	if e, err := u.store.ResolveRebaseTarget(repo, v11[:10], nil); err != nil || e.Commit != v11 {
		t.Errorf("an abbreviated commit → %+v (%v)", e, err)
	}
	if _, err := u.store.ResolveRebaseTarget(repo, "nope", nil); err == nil || !strings.Contains(err.Error(), "names no") {
		t.Errorf("a ref that names nothing: %v", err)
	}
	if _, err := u.store.ResolveRebaseTarget(repo, "--upload-pack=x", nil); err == nil {
		t.Error("a flag passed for a revision")
	}
	if got := u.store.RefKind(mustAddr(t, u.source("main"))); got != "branch" {
		t.Errorf("RefKind(main) = %q", got)
	}
	if got := u.store.RefKind(mustAddr(t, u.source("v1.1.0"))); got != "tag" {
		t.Errorf("RefKind(v1.1.0) = %q", got)
	}
}

// wrappedGit is a git that runs body (shell) before it execs the real git with the same arguments.
// In body, $last is the run's last argument.
func wrappedGit(t *testing.T, body string) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	bin := filepath.Join(t.TempDir(), "git")
	writeTestFile(t, bin, "#!/bin/sh\nfor last; do :; done\n"+body+"\nexec "+shquote.Quote(real)+" \"$@\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A CLONE REMOVES ONLY WHAT IT MADE: when another process fills the directory between the
// inspection and the clone, git refuses to clone there, and what that process wrote stays — in a
// directory the clone was to make, and in one the user made empty for it.
func TestARebaseCloneRemovesOnlyWhatItMade(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	u.store.Git = wrappedGit(t, `case " $* " in *" clone "*) mkdir -p "$last" && echo theirs > "$last/other-run.txt";; esac`)
	for _, dir := range []string{filepath.Join(t.TempDir(), "clone"), t.TempDir()} {
		r := u.rebase(t, series, ListEntry{Commit: v11, Tag: "v1.1.0"}, dir)
		if r.Err == nil || !strings.Contains(r.Err.Error(), "cloning") || r.Kept {
			t.Fatalf("rebase into %s = %+v, want the clone refused", dir, r)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "other-run.txt")); err != nil || string(got) != "theirs\n" {
			t.Errorf("the failed clone removed what another process wrote in %s (%v)", dir, err)
		}
	}
}

// A TARGET NO REF OF THE UPSTREAM REACHES (a commit a re-pointed tag named once, or one on a branch
// since deleted) is fetched into the clone by its id, as a checkout of it would need.
func TestARebaseCloneFetchesATargetNoRefReaches(t *testing.T) {
	u, _, series := rebaseFixture(t)
	gitIn(t, u.repo, "checkout", "-q", "-b", "gone")
	gone := commitFile(t, u.repo, "f.txt", thirtyLines(map[int]string{25: "gone"}))
	gitIn(t, u.repo, "checkout", "-q", "main")
	gitIn(t, u.repo, "branch", "-q", "-D", "gone")
	gitIn(t, u.repo, "reflog", "expire", "--expire=now", "--all")
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: gone}, dir)
	if r.Err != nil || r.Base != nil || !r.Clean {
		t.Errorf("rebase onto a commit no ref reaches = %+v (base %v), want it fetched and clean", r, r.Base)
	}
}

// A BLOB THE CLONE CANNOT FETCH is an apply error naming the upstream's files, before any replay
// reads them: the blob prefetch fails here, and the clone is removed.
func TestARebaseCloneWithBlobsItCannotFetchIsAnApplyError(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	u.store.Git = wrappedGit(t, `case " $* " in *" fetch "*" --stdin "*) exit 1;; esac`)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11}, dir)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "could not fetch the upstream's files at") || r.Kept {
		t.Fatalf("rebase = %+v, want the missing blobs' apply error", r)
	}
	mustNotExist(t, dir, "a clone missing its blobs was left")
}

// THE APPLIED SERIES IS IN THE MARKER, and a finished rebase is told from an aborted one: the
// conflict's clone records the series as applied at its base; RebaseFinished is false while the
// rebase is stopped and once it is aborted (the branch back at the applied series), true once the
// rebase is resolved and continued, and false for a marker that names no applied series.
func TestRebaseFinishedTellsAFinishedRebase(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11, Tag: "v1.1.0"}, dir)
	if r.Conflict == nil || r.Applied == "" {
		t.Fatalf("rebase = %+v, want a conflict with the applied series", r)
	}
	_, m, _ := InspectRebaseDir(dir)
	if m.Applied != r.Applied || gitIn(t, dir, "rev-parse", RebaseBranchRef) != r.Applied {
		t.Fatalf("the marker's applied series %q, the result's %q, the branch at %q", m.Applied, r.Applied,
			gitIn(t, dir, "rev-parse", RebaseBranchRef))
	}
	if u.store.RebaseFinished(dir, m) {
		t.Error("a stopped rebase reads as finished")
	}
	gitIn(t, dir, "rebase", "--abort")
	if u.store.RebaseFinished(dir, m) {
		t.Error("an aborted rebase reads as finished")
	}
	r2dir := filepath.Join(t.TempDir(), "clone")
	u.rebase(t, series, ListEntry{Commit: v11, Tag: "v1.1.0"}, r2dir)
	_, m2, _ := InspectRebaseDir(r2dir)
	// Each member conflicts in turn, so a continue that stops at the next member exits non-zero
	// and the loop resolves that one too. The continue runs in the fixtures' environment, whose
	// fixed identity it needs to commit, and the loop is bounded: a continue that cannot commit
	// leaves the rebase where it was, and an unbounded loop then spins until the package times out.
	for i := 0; RebaseInProgress(r2dir); i++ {
		if i == 10 {
			t.Fatal("the rebase is still in progress after 10 continues")
		}
		writeTestFile(t, filepath.Join(r2dir, "f.txt"), thirtyLines(map[int]string{10: "ten", 11: "eleven", 12: "twelve"}))
		gitIn(t, r2dir, "add", "f.txt")
		cmd := exec.Command("git", "-C", r2dir, "-c", "core.editor=true", "rebase", "--continue")
		cmd.Env = append(gitTestEnv(), "GIT_EDITOR=true")
		_ = cmd.Run()
	}
	if !u.store.RebaseFinished(r2dir, m2) {
		t.Error("a resolved and continued rebase does not read as finished")
	}
	noApplied := *m2
	noApplied.Applied = ""
	if u.store.RebaseFinished(r2dir, &noApplied) {
		t.Error("a marker naming no applied series reads as finished")
	}
}

// A CLONE A CTRL-C OR A TIMEOUT KILLED cannot remove its own junk, as a git that fails does: the
// git directory it wrote is removed, and the directory this run made for it.
func TestAKilledRebaseCloneRemovesWhatItWrote(t *testing.T) {
	u, v11, series := rebaseFixture(t)
	// A clone that writes its git directory, then hangs until it is killed.
	u.store.Git = wrappedGit(t, `case " $* " in *" clone "*) mkdir -p "$last/.git/objects" && exec sleep 30;; esac`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u.store.Ctx = ctx
	time.AfterFunc(500*time.Millisecond, cancel)
	dir := filepath.Join(t.TempDir(), "clone")
	r := u.rebase(t, series, ListEntry{Commit: v11}, dir)
	if r.Err == nil || r.Kept {
		t.Fatalf("rebase = %+v, want the killed clone's error", r)
	}
	mustNotExist(t, dir, "a killed clone left its directory")
}
