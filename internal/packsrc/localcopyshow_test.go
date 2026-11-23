package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// homeAt makes root the store's HOME, so a clone under it is shown as ~/…
func (f *localCopyFixture) homeAt(root string) {
	f.store.Getenv = func(k string) string {
		if k == "HOME" {
			return root
		}
		return ""
	}
}

// §7: the clone is shown with ~ for the home directory, in the prose and in every command the line names.
func TestLocalLinesShowTheCloneWithTildeForHome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *localCopyFixture) (source, local string)
		want  []string
	}{
		{name: "equal", want: []string{"tillr  local ~/clone (working tree, equal to origin; 1 file)"}},
		{name: "behind and clean",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				f.push(t, "theirs.md")
				return f.source, f.clone
			},
			want: []string{"(clone is 1 behind origin and clean — git -C ~/clone pull)"}},
		{name: "clone absent",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				return f.source, filepath.Join(f.root, "src", "tillr")
			},
			want: []string{"no local clone at ~/src/tillr — git clone ", " ~/src/tillr)"}},
		{name: "another branch",
			setup: func(t *testing.T, f *localCopyFixture) (string, string) {
				gitIn(t, f.clone, "switch", "-q", "-c", "other")
				return f.source, f.clone
			},
			want: []string{"clone ~/clone not used: it is on branch other, not main — git -C ~/clone switch main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalCopyFixture(t)
			f.launch(t, f.source, f.clone)
			source, local := f.source, f.clone
			if tc.setup != nil {
				source, local = tc.setup(t, f)
			}
			f.homeAt(f.root)
			line, _ := f.launch(t, source, local).LocalLine()
			for _, w := range tc.want {
				if !strings.Contains(line, w) {
					t.Errorf("line %q lacks %q", line, w)
				}
			}
			if strings.Contains(strings.ReplaceAll(line, f.bare, ""), f.root) {
				t.Errorf("line %q spells the home directory out", line)
			}
		})
	}
}

// A clone outside the home directory keeps its absolute path, and so does one whose name merely starts like it.
func TestLocalLinesKeepAPathOutsideHomeAbsolute(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	f.homeAt(f.clone + "-other")
	line, _ := f.launch(t, f.source, f.clone).LocalLine()
	if !strings.Contains(line, "local "+f.clone+" (") {
		t.Errorf("line %q should name %s", line, f.clone)
	}
}

// §6.4's one rule: every working-tree line ends its parenthetical with the pack's file count.
func TestEveryWorkingTreeLineCarriesTheFileCount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *localCopyFixture)
		want  string
	}{
		{name: "equal", want: "; 1 file)"},
		{name: "equal dirty", want: "2 files uncommitted (1 untracked); 2 files)",
			setup: func(t *testing.T, f *localCopyFixture) {
				write(t, filepath.Join(f.clone, "new.md"), "n")
				write(t, filepath.Join(f.clone, "pack.json"), "{}")
			}},
		{name: "ahead", want: "1 unpushed commit; 2 files)",
			setup: func(t *testing.T, f *localCopyFixture) { commitFile(t, f.clone, "mine.md", "m") }},
		{name: "behind dirty", want: "(working tree; 2 files) is behind origin by 1 and has uncommitted changes",
			setup: func(t *testing.T, f *localCopyFixture) {
				f.push(t, "t.md")
				write(t, filepath.Join(f.clone, "w.md"), "w")
			}},
		{name: "diverged", want: "(working tree; 2 files) has diverged from origin",
			setup: func(t *testing.T, f *localCopyFixture) { f.push(t, "t.md"); commitFile(t, f.clone, "mine.md", "m") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalCopyFixture(t)
			f.launch(t, f.source, f.clone)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			line, _ := f.launch(t, f.source, f.clone).LocalLine()
			if !strings.Contains(line, tc.want) {
				t.Errorf("line %q lacks %q", line, tc.want)
			}
		})
	}
}

// §8: `yolo check` reads the clone alone. Each §6.2 condition is a finding with its next step, and nothing is
// written to the clone or to the store.
func TestInspectLocalCloneReportsEachConditionFromTheCloneAlone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, f *localCopyFixture) string // returns the path
		absent bool
		reason []string // substrings of NotUsed; none = usable
	}{
		{name: "usable", setup: func(t *testing.T, f *localCopyFixture) string { return f.clone }},
		{name: "absent", absent: true,
			setup: func(t *testing.T, f *localCopyFixture) string { return filepath.Join(f.root, "nowhere") }},
		{name: "not a clone", reason: []string{"it is not a git clone", "remove local"},
			setup: func(t *testing.T, f *localCopyFixture) string {
				plain := filepath.Join(f.root, "plain")
				write(t, filepath.Join(plain, "x"), "x")
				if _, err := f.store.run("", "-C", plain, "rev-parse", "--show-toplevel"); err == nil {
					t.Skip("the temp directory sits inside a repository")
				}
				return plain
			}},
		{name: "another branch", reason: []string{"it is on branch other, not main", "git -C ", "switch main"},
			setup: func(t *testing.T, f *localCopyFixture) string {
				gitIn(t, f.clone, "switch", "-q", "-c", "other")
				return f.clone
			}},
		{name: "remote names another repository", reason: []string{"no remote of the clone names", "fix the remote"},
			setup: func(t *testing.T, f *localCopyFixture) string {
				gitIn(t, f.clone, "remote", "set-url", "origin", filepath.Join(f.root, "other.git"))
				return f.clone
			}},
		{name: "no tracking ref", reason: []string{"refs/remotes/origin/main", "fetch origin"},
			setup: func(t *testing.T, f *localCopyFixture) string {
				gitIn(t, f.clone, "update-ref", "-d", "refs/remotes/origin/main")
				return f.clone
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalCopyFixture(t)
			path := tc.setup(t, f)
			// Network-free: the remote is gone, and the answer is the same.
			if err := os.Rename(f.bare, f.bare+".gone"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Rename(f.bare+".gone", f.bare) })
			before := ""
			if _, err := os.Stat(f.clone); err == nil {
				before = cloneState(t, f.clone)
			}
			a, err := Parse(f.source)
			if err != nil {
				t.Fatal(err)
			}
			st := f.store.InspectLocalClone(a, path)
			if st.Absent != tc.absent {
				t.Errorf("Absent = %v, want %v (%+v)", st.Absent, tc.absent, st)
			}
			if len(tc.reason) == 0 && st.NotUsed != "" {
				t.Errorf("NotUsed = %q, want a usable clone", st.NotUsed)
			}
			for _, w := range tc.reason {
				if !strings.Contains(st.NotUsed, w) {
					t.Errorf("NotUsed = %q lacks %q", st.NotUsed, w)
				}
			}
			if before != "" && cloneState(t, f.clone) != before {
				t.Error("the inspection changed the clone")
			}
			if rec, _ := f.store.LoadLocalCopyRecord("tillr"); rec != nil {
				t.Errorf("the inspection wrote a launch record: %+v", rec)
			}
		})
	}
}

// §8: `pack status` shows the last launch's decision and says it is the last launch's.
func TestStatusLineSaysWhichLaunchDecidedAndHowLongAgo(t *testing.T) {
	d := LocalCopyDecision{Line: "local ~/src/tillr (working tree, equal to origin; 41 files)", At: 1_800_000_000}
	got := d.StatusLine(time.Unix(1_800_000_000+3*3600, 0))
	want := "local ~/src/tillr (working tree, equal to origin; 41 files) — decided at the last launch, 3 hours ago; the next launch decides again"
	if got != want {
		t.Errorf("StatusLine = %q, want %q", got, want)
	}
}
