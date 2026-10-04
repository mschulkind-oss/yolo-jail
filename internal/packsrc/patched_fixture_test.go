package packsrc

// patched_fixture_test.go is the upstream every patched-fork test here replays against: a real git
// repository standing in for the upstream's remote, a two-member `git format-patch --base` series
// exported from a branch of it into a pack directory, and upstream versions the series fits and
// does not — the shapes the design measured (docs/design/patched-forks.md §5.2).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// patchedUpstream is the fixture.
type patchedUpstream struct {
	repo  string // the upstream repository (its work tree), served blobless
	pack  string // the fork pack's root; its series is at pack/patches
	base  string // the series' base, tagged v1.0.0
	store *Store
	now   time.Time
}

// thirtyLines is the fixture's one file, f.txt: the lines 1 to 30, with edits applied by line.
func thirtyLines(edits map[int]string) string {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		line := fmt.Sprint(i)
		if e, ok := edits[i]; ok {
			line = e
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// newPatchedUpstream makes the upstream at v1.0.0 (the series' base) and the series: member 0001
// changes line 10, member 0002 line 12 — a stacked series, so the second member's context holds
// the first's change and `git am --3way` could not replay it alone (§5.2). The fork branch is
// deleted once exported, so no ref of the upstream reaches a commit of the series.
func newPatchedUpstream(t *testing.T) *patchedUpstream {
	t.Helper()
	repo := gitRepo(t, map[string]string{"f.txt": thirtyLines(nil), "README": "upstream\n"})
	// A blobless clone of a file:// remote honours the filter only when the remote allows it, and
	// the store's mirror is blobless: without this the prefetch path is never taken.
	gitIn(t, repo, "config", "uploadpack.allowFilter", "true")
	base := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "tag", "v1.0.0")
	gitIn(t, repo, "checkout", "-q", "-b", "fork")
	commitFile(t, repo, "f.txt", thirtyLines(map[int]string{10: "ten"}))
	commitFile(t, repo, "f.txt", thirtyLines(map[int]string{10: "ten", 12: "twelve"}))
	pack := t.TempDir()
	gitIn(t, repo, "format-patch", "-q", "--base="+base, "-o", filepath.Join(pack, "patches"), "main..fork")
	gitIn(t, repo, "checkout", "-q", "main")
	gitIn(t, repo, "branch", "-q", "-D", "fork")
	return &patchedUpstream{
		repo: repo, pack: pack, base: base,
		store: &Store{Dir: t.TempDir(), Getenv: noStagedTree},
		now:   time.Unix(1_800_000_000, 0),
	}
}

// source is the upstream's address at ref.
func (u *patchedUpstream) source(ref string) string { return "git+file://" + u.repo + "?ref=" + ref }

// release commits f.txt with edits on main and tags it, returning the commit.
func (u *patchedUpstream) release(t *testing.T, tag string, edits map[int]string) string {
	t.Helper()
	c := commitFile(t, u.repo, "f.txt", thirtyLines(edits))
	if tag != "" {
		gitIn(t, u.repo, "tag", tag)
	}
	return c
}

// series reads the fixture's series.
func (u *patchedUpstream) series(t *testing.T) *Series {
	t.Helper()
	s, err := ReadSeries(u.pack, "patches")
	if err != nil {
		t.Fatalf("ReadSeries: %v", err)
	}
	return s
}

// want is the check request for the fixture's series at ref under follow.
func (u *patchedUpstream) want(t *testing.T, ref, follow string) PatchedWant {
	return PatchedWant{Owner: "forkpack/tool", Source: u.source(ref), Follow: follow, Base: u.series(t).Base}
}

// check runs one check at the fixture's clock.
func (u *patchedUpstream) check(t *testing.T, w PatchedWant, force bool) CheckResult {
	t.Helper()
	res := u.store.CheckPatched(w, CheckOptions{Force: force, Now: func() time.Time { return u.now }})
	if res.Err != nil {
		t.Fatalf("CheckPatched: %v", res.Err)
	}
	return res
}

// tags renders a list as its tags (or short commits), for a comparison.
func listLabels(list []ListEntry) string {
	var out []string
	for _, e := range list {
		l := e.Tag
		if l == "" {
			l = shortCommit(e.Commit)
		}
		if e.Tip {
			l += "*"
		}
		out = append(out, l)
	}
	return strings.Join(out, " ")
}

// mustNotExist fails when p exists.
func mustNotExist(t *testing.T, p, why string) {
	t.Helper()
	if _, err := os.Stat(p); err == nil {
		t.Errorf("%s exists: %s", p, why)
	}
}
