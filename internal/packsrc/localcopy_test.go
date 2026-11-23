package packsrc

// localcopy_test.go pins the LAUNCH'S COPY DECISION for a pack with a `local` clone
// (docs/design/local-pack-clones.md §6.2-§6.3, LPC-D8..LPC-D11) against REAL repositories: a bare origin, the
// store's own mirror of it, the user's clone, and a second clone that pushes. Every row of the §6.3 table, and
// each way a clone is not usable, is one case. The decision is read from the Outcome a real Store.Refresh
// returns, so a refresh that stops consulting decideLocalCopy leaves Outcome.Local nil and fails every case.
// Nothing sleeps and nothing touches the network.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type localCopyFixture struct {
	store               *Store
	root, bare, pusher  string
	clone, lock, source string
	now                 time.Time
}

func newLocalCopyFixture(t *testing.T) *localCopyFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &localCopyFixture{
		root: root, bare: filepath.Join(root, "origin.git"), pusher: filepath.Join(root, "pusher"),
		clone: filepath.Join(root, "clone"), lock: filepath.Join(root, "packs.lock.json"),
		store: &Store{Dir: filepath.Join(root, "store"), Getenv: noStagedTree},
		now:   time.Unix(1_800_000_000, 0),
	}
	gitIn(t, root, "init", "--bare", "-q", "-b", "main", f.bare)
	if err := os.MkdirAll(f.pusher, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.pusher, "init", "-q", "-b", "main")
	gitIn(t, f.pusher, "remote", "add", "origin", f.bare)
	commitFile(t, f.pusher, "pack.json", `{"name":"p"}`)
	gitIn(t, f.pusher, "push", "-q", "origin", "main")
	gitIn(t, root, "clone", "-q", f.bare, f.clone)
	f.source = "git+file://" + f.bare + "?ref=main"
	return f
}

// launch is one fresh launch's refresh of the pack named "tillr", whose `local` is local.
func (f *localCopyFixture) launch(t *testing.T, source, local string) Outcome {
	t.Helper()
	f.now = f.now.Add(time.Minute)
	outs, err := f.store.Refresh([]RefreshPack{{Name: "tillr", Source: source, Local: local}},
		RefreshOptions{LockPath: f.lock, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if outs[0].Err != nil {
		t.Fatalf("a clone must never fail a launch: %v", outs[0].Err)
	}
	return outs[0]
}

// push makes origin one commit newer, through the second clone, and returns the new tip.
func (f *localCopyFixture) push(t *testing.T, rel string) string {
	t.Helper()
	tip := commitFile(t, f.pusher, rel, rel)
	gitIn(t, f.pusher, "push", "-q", "origin", "main")
	return tip
}

func TestTheCopyDecisionOverRealClones(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *localCopyFixture) (source, local string)
		copy  LocalCopy
		warn  bool
		rel   string
		// lines are substrings the one line must hold, in any order; %C is the clone, %O the origin tip's short id.
		lines []string
	}{
		{name: "equal and clean", copy: CopyWorkingTree, rel: "equal",
			lines: []string{"local %C (working tree, equal to origin; 1 file)"}},
		{name: "equal and dirty", copy: CopyWorkingTree, rel: "equal",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				write(t, filepath.Join(f.clone, "pack.json"), `{"name":"edited"}`)
				write(t, filepath.Join(f.clone, "new.md"), "new")
				return f.source, f.clone
			},
			lines: []string{"working tree", "equal to origin", "2 files uncommitted", "1 untracked"}},
		{name: "ahead", copy: CopyWorkingTree, rel: "ahead",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				commitFile(t, f.clone, "mine.md", "mine")
				return f.source, f.clone
			},
			lines: []string{"local %C (working tree", "1 unpushed commit"}},
		{name: "ahead by two", copy: CopyWorkingTree, rel: "ahead",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				commitFile(t, f.clone, "a.md", "a")
				commitFile(t, f.clone, "b.md", "b")
				return f.source, f.clone
			},
			lines: []string{"2 unpushed commits"}},
		{name: "behind and clean runs origin", copy: CopyOrigin, rel: "behind",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				f.push(t, "theirs.md")
				return f.source, f.clone
			},
			lines: []string{"origin main %O (clone is 1 behind origin and clean — git -C %C pull)"}},
		{name: "behind and dirty runs the working tree", copy: CopyWorkingTree, warn: true, rel: "behind",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				f.push(t, "theirs.md")
				write(t, filepath.Join(f.clone, "wip.md"), "wip")
				return f.source, f.clone
			},
			lines: []string{"local %C (working tree) is behind origin by 1 and has uncommitted changes — git -C %C pull"}},
		{name: "diverged", copy: CopyWorkingTree, warn: true, rel: "diverged",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				f.push(t, "theirs.md")
				commitFile(t, f.clone, "mine.md", "mine")
				return f.source, f.clone
			},
			lines: []string{"diverged from origin", "1 unpushed", "1 behind", "push --force-with-lease -- origin main", "(if you rebased)"}},
		{name: "clone absent", copy: CopyOrigin, rel: "absent",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				return f.source, filepath.Join(f.root, "nowhere", "tillr")
			},
			lines: []string{"origin main %O (no local clone at", "git clone " + "file://"}},
		{name: "not a git repository", copy: CopyOrigin, warn: true, rel: "unusable",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				plain := filepath.Join(f.root, "plain")
				write(t, filepath.Join(plain, "x"), "x")
				if out, err := f.store.run("", "-C", plain, "rev-parse", "--show-toplevel"); err == nil {
					t.Skipf("the temp directory sits inside a repository (%s)", strings.TrimSpace(out))
				}
				return f.source, plain
			},
			lines: []string{"origin main %O (clone ", "not used: ", "it is not a git clone", "remove local"}},
		{name: "clone on another branch", copy: CopyOrigin, warn: true, rel: "unusable",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.clone, "switch", "-q", "-c", "other")
				return f.source, f.clone
			},
			lines: []string{"not used: it is on branch other, not main — git -C %C switch main"}},
		{name: "clone remote names another repository", copy: CopyOrigin, warn: true, rel: "unusable",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.clone, "remote", "set-url", "origin", filepath.Join(f.root, "other.git"))
				return f.source, f.clone
			},
			lines: []string{"not used: no remote of the clone names", "fix the remote, or the pack's git url"}},
		{name: "clone lacks the tracking ref", copy: CopyOrigin, warn: true, rel: "unusable",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.clone, "update-ref", "-d", "refs/remotes/origin/main")
				return f.source, f.clone
			},
			lines: []string{"not used: ", "refs/remotes/origin/main", "fetch origin"}},
		{name: "ref is a tag upstream", copy: CopyOrigin, warn: true, rel: "unusable",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.pusher, "tag", "v1")
				gitIn(t, f.pusher, "push", "-q", "origin", "v1")
				return "git+file://" + f.bare + "?ref=v1", f.clone
			},
			lines: []string{"not used: v1 is a tag upstream", "name the branch the pack follows"}},
		{name: "origin cannot be reached", copy: CopyWorkingTree, warn: true, rel: "unverified",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				if err := os.Rename(f.bare, f.bare+".gone"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Rename(f.bare+".gone", f.bare) })
				return f.source, f.clone
			},
			lines: []string{"local %C (working tree, not verified:", "last fetched", "yolo pack update when online"}},
		{name: "origin deleted the branch", copy: CopyWorkingTree, warn: true, rel: "origin-gone",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.bare, "update-ref", "-d", "refs/heads/main")
				return f.source, f.clone
			},
			lines: []string{"local %C (working tree, not on origin", "push -- origin main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalCopyFixture(t)
			// The first launch holds the mirror; it is also the "equal" row.
			first := f.launch(t, f.source, f.clone)
			if first.Local == nil || first.Local.Copy != CopyWorkingTree || first.Local.Relation != "equal" {
				t.Fatalf("first launch decision = %+v, want the working tree, equal", first.Local)
			}
			source, local := f.source, f.clone
			if tc.setup != nil {
				source, local = tc.setup(t, f)
			}
			refs := cloneState(t, f.clone)
			o := f.launch(t, source, local)
			d := o.Local
			if d == nil {
				t.Fatal("the refresh never consulted decideLocalCopy: Outcome.Local is nil")
			}
			if d.Copy != tc.copy || d.Warn != tc.warn || d.Relation != tc.rel {
				t.Errorf("decision = copy %s warn %v relation %s, want %s %v %s\n%+v", d.Copy, d.Warn, d.Relation,
					tc.copy, tc.warn, tc.rel, d)
			}
			origin := ""
			if d.OriginCommit != "" {
				origin = shortCommit(d.OriginCommit)
			}
			line, warn := o.LocalLine()
			if warn != tc.warn || !strings.HasPrefix(line, "tillr  ") {
				t.Errorf("LocalLine() = %q, %v, want a line for tillr with warn %v", line, warn, tc.warn)
			}
			for _, want := range tc.lines {
				want = strings.NewReplacer("%C", local, "%O", origin).Replace(want)
				if !strings.Contains(line, want) {
					t.Errorf("line %q lacks %q", line, want)
				}
			}
			if after := cloneState(t, f.clone); after != refs {
				t.Errorf("the clone's refs or HEAD changed:\nbefore %s\nafter  %s", refs, after)
			}
			// The decision is the record a later reader (ResolvePack, `pack status`) reads.
			rec, err := f.store.LoadLocalCopyRecord("tillr")
			if err != nil || rec == nil || rec.Copy != d.Copy || rec.Line != d.Line || rec.Relation != d.Relation {
				t.Errorf("record = %+v, %v, want the decision %+v", rec, err, d)
			}
		})
	}
}

// A failed check that ran the working tree says so once: the held-copy warning would say the opposite.
func TestAWorkingTreeCopyReplacesTheHeldCopyWarning(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	if err := os.Rename(f.bare, f.bare+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(f.bare+".gone", f.bare) })
	o := f.launch(t, f.source, f.clone)
	if o.FetchErr == nil || o.Local == nil || o.Local.Copy != CopyWorkingTree {
		t.Fatalf("outcome = %+v, want a failed check that ran the working tree", o)
	}
	if w := o.Warning(); w != "" {
		t.Errorf("Warning() = %q, want none: the local line carries the failure", w)
	}
}

// A pack with no `local` is never decided, and its outcome is what it was.
func TestAPackWithoutLocalHasNoCopyDecision(t *testing.T) {
	f := newLocalCopyFixture(t)
	o := f.launch(t, f.source, "")
	if o.Local != nil {
		t.Errorf("Local = %+v, want nil", o.Local)
	}
	if line, _ := o.LocalLine(); line != "" {
		t.Errorf("LocalLine() = %q, want none", line)
	}
}

// The decision never writes to the clone, so it also reads nothing a hook or fsmonitor would run: this pins
// the one place a status could leave a mark.
func cloneState(t *testing.T, clone string) string {
	t.Helper()
	return gitIn(t, clone, "for-each-ref") + "\n" + gitIn(t, clone, "symbolic-ref", "HEAD")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
