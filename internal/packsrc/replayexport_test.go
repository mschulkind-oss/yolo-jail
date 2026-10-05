package packsrc

// replayexport_test.go pins the walk's two hooks for the build act (replay.go; docs/design/
// patched-forks.md §5.1, PF-D18, PF-D25): the newest fit's source subdirectory is handed to OnFit
// checked out of the replay's last commit, with its links as links and no .git, before the scratch
// repository goes; and a store's Ctx ends the walk's git when it is cancelled.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE FIT'S TREE, for the build's src/: the patched bytes, a link as a link, no .git, and only for
// the fit — an entry the series did not take is never exported.
func TestTheWalkHandsTheFitsPatchedTreeToOnFit(t *testing.T) {
	u := newPatchedUpstream(t)
	if err := os.Symlink("f.txt", filepath.Join(u.repo, "link")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, u.repo, "add", "link")
	gitIn(t, u.repo, "commit", "-qm", "a link")
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	u.release(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"}) // conflicts at the first member
	series, list := u.checked(t)
	calls := 0
	var got, link string
	var gitDir bool
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{
		OnFit: func(tree string) error {
			calls++
			data, err := os.ReadFile(filepath.Join(tree, "f.txt"))
			if err != nil {
				return err
			}
			got = string(data)
			link, _ = os.Readlink(filepath.Join(tree, "link"))
			_, err = os.Lstat(filepath.Join(tree, ".git"))
			gitDir = err == nil
			return nil
		}})
	if w.Fit != 1 || w.Results[w.Fit].Entry.Commit != v11 {
		t.Fatalf("walk = %+v, want the fit at v1.1.0", w)
	}
	if calls != 1 {
		t.Fatalf("OnFit ran %d times, want once, for the fit alone", calls)
	}
	if want := thirtyLines(map[int]string{10: "ten", 12: "twelve", 14: "fourteen"}); got != want {
		t.Errorf("the exported f.txt is not the patched v1.1.0:\n%s", got)
	}
	if link != "f.txt" {
		t.Errorf("the link was not exported as a link: %q", link)
	}
	if gitDir {
		t.Error("the exported tree carries a .git, which the build would see")
	}
}

// A SUBDIRECTORY SOURCE exports the subdirectory alone, at the root of the tree OnFit gets.
func TestTheWalkExportsTheSourcesSubdirectory(t *testing.T) {
	u := newPatchedUpstream(t)
	if err := os.MkdirAll(filepath.Join(u.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, u.repo, "pkg/x.txt", "x\n")
	gitIn(t, u.repo, "tag", "v1.1.0")
	series, list := u.checked(t)
	var names []string
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "pkg", series, list, WalkOptions{
		OnFit: func(tree string) error {
			entries, err := os.ReadDir(tree)
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return err
		}})
	if w.Fit < 0 {
		t.Fatalf("walk = %+v", w)
	}
	if strings.Join(names, ",") != "x.txt" {
		t.Errorf("the export holds %v, want the subdirectory's x.txt alone", names)
	}
}

// AN EXPORT THAT FAILS is the fit's apply error: no fit, and nothing a record would keep.
func TestAFailedExportIsAnApplyError(t *testing.T) {
	u := newPatchedUpstream(t)
	series, list := u.checked(t)
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{
		OnFit: func(string) error { return errors.New("disk full") }})
	if w.Fit >= 0 || len(w.Results) != 1 || w.Results[0].Err == nil || w.Results[0].Clean {
		t.Fatalf("walk = %+v, want the fit turned into an apply error", w)
	}
	if !strings.Contains(w.Results[0].Err.Error(), "disk full") {
		t.Errorf("the error does not say why: %v", w.Results[0].Err)
	}
}

// A CANCELLED Ctx ENDS THE WALK (PF-D25's Ctrl-C): an apply error, no conflict and no fit.
func TestACancelledStoreContextEndsTheWalk(t *testing.T) {
	u := newPatchedUpstream(t)
	series, list := u.checked(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u.store.Ctx = ctx
	w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", series, list, WalkOptions{})
	if w.Fit >= 0 || w.Err == nil {
		t.Fatalf("walk = %+v, want an apply error before any entry", w)
	}
	for _, r := range w.Results {
		if r.Conflict != nil {
			t.Errorf("a cancelled walk recorded a conflict: %+v", r)
		}
	}
}
