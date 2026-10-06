package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// The fixtures below make version directories NOW, and a directory's change time cannot be set
// back, so each test judges at a clock two windows ahead: every fixture version is then "installed
// 60 days ago", and records are dated against that clock.

// miseFixture is a tool store under a temp dir, judged at now.
type miseFixture struct {
	t     *testing.T
	store string
	now   time.Time
}

func newMiseFixture(t *testing.T, versions ...string) *miseFixture {
	t.Helper()
	f := &miseFixture{t: t, store: filepath.Join(t.TempDir(), "mise"), now: time.Now().Add(2 * miseuse.Window)}
	for _, v := range versions {
		f.install(v)
	}
	// The record has covered a whole window: judging is allowed unless a test says otherwise.
	f.since(f.now.Add(-miseuse.Window - 24*time.Hour))
	return f
}

// install makes one version directory holding one 1000-byte file.
func (f *miseFixture) install(rel string) {
	f.t.Helper()
	dir := filepath.Join(f.store, "installs", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "tool"), make([]byte, 1000), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// since sets when the store's first record was written.
func (f *miseFixture) since(when time.Time) {
	f.t.Helper()
	dir := filepath.Join(f.store, miseuse.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, miseuse.SinceName), []byte(when.UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// record writes a use record for a jail of ws, recorded at when, naming installs.
func (f *miseFixture) record(ws string, when time.Time, installs ...string) {
	f.t.Helper()
	if err := miseuse.Write(f.store, miseuse.NewName(), miseuse.Record{Workspace: ws, Recorded: when, Installs: installs}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *miseFixture) find(live runtime.LiveSet) MiseSweep {
	return FindUnusedMiseVersions(f.store, live, f.now, time.Minute)
}

func (f *miseFixture) present(rel string) bool {
	_, err := os.Lstat(filepath.Join(f.store, "installs", filepath.FromSlash(rel)))
	return err == nil
}

func nothingRunning() runtime.LiveSet {
	return runtime.LiveSet{Known: true, Names: map[string]struct{}{}}
}

func running(workspaces ...string) runtime.LiveSet {
	l := nothingRunning()
	for _, ws := range workspaces {
		l.Names[runtime.FromWorkspace(ws)] = struct{}{}
	}
	return l
}

func candidateRels(s MiseSweep) []string {
	var out []string
	for _, v := range s.Candidates {
		out = append(out, v.Rel)
	}
	return out
}

// TestAVersionAnotherWorkspaceUsesIsKept is the trap the ruling names: `mise prune` in one jail
// judges from that workspace alone and removes versions other workspaces still use. Here
// workspace A uses node 22 and workspace B node 20; judged machine-wide, neither is unused, and
// only the version NO jail names is.
func TestAVersionAnotherWorkspaceUsesIsKept(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0", "node/22.5.0", "python/3.11.9")
	f.record("/home/u/code/a", f.now.Add(-24*time.Hour), "node/22.5.0")
	f.record("/home/u/code/b", f.now.Add(-20*24*time.Hour), "node/20.1.0")
	s := f.find(nothingRunning())
	if s.Declined != "" || s.Waiting != "" {
		t.Fatalf("declined %q / waiting %q", s.Declined, s.Waiting)
	}
	if got := candidateRels(s); len(got) != 1 || got[0] != "python/3.11.9" {
		t.Fatalf("candidates = %v, want only python/3.11.9: a version another workspace used within "+
			"the window is in use, whatever any one workspace's own record says", got)
	}
	if s.Bytes != 1000 || s.Candidates[0].Files != 1 {
		t.Errorf("the candidate is sized %d B in %d file(s), want 1000 B in 1", s.Bytes, s.Candidates[0].Files)
	}
}

// TestARecordStopsCountingAfterTheWindow: the window is the ruling's 30 days. A version whose
// last record is older is unused, and the sweep says when it was last used.
func TestARecordStopsCountingAfterTheWindow(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0")
	last := f.now.Add(-miseuse.Window - time.Hour)
	f.record("/home/u/code/a", last, "node/20.1.0")
	s := f.find(nothingRunning())
	if got := candidateRels(s); len(got) != 1 {
		t.Fatalf("candidates = %v, want node/20.1.0: its only record is older than the window", got)
	}
	if !s.Candidates[0].LastUsed.Equal(last) {
		t.Errorf("LastUsed = %v, want %v", s.Candidates[0].LastUsed, last)
	}
}

// TestARunningJailKeepsWhatItRecordedWhateverItsAge: a jail that runs for longer than the window
// still uses its tools. Its record counts for as long as it runs.
func TestARunningJailKeepsWhatItRecordedWhateverItsAge(t *testing.T) {
	ws := t.TempDir()
	f := newMiseFixture(t, "node/20.1.0")
	f.record(ws, f.now.Add(-miseuse.Window-24*time.Hour), "node/20.1.0")
	if got := candidateRels(f.find(running(ws))); len(got) != 0 {
		t.Fatalf("candidates = %v: a running jail's record must protect what it names", got)
	}
	if got := candidateRels(f.find(nothingRunning())); len(got) != 1 {
		t.Fatalf("candidates = %v once the jail is gone, want node/20.1.0", got)
	}
}

// TestTheSweepDeclinesWhenTheRecordsCannotAnswer is the tri-state rule, case by case: each of
// these is "cannot tell", and a reaper that cannot tell removes nothing.
func TestTheSweepDeclinesWhenTheRecordsCannotAnswer(t *testing.T) {
	t.Run("the runtime cannot be asked", func(t *testing.T) {
		f := newMiseFixture(t, "node/20.1.0")
		f.record("/home/u/code/a", f.now.Add(-time.Hour))
		if s := f.find(runtime.LiveSet{Known: false}); s.Declined == "" || len(s.Candidates) != 0 {
			t.Fatalf("an unknown live set judged: %+v", s)
		}
	})
	t.Run("a running jail has no record", func(t *testing.T) {
		f := newMiseFixture(t, "node/20.1.0")
		f.record("/home/u/code/a", f.now.Add(-time.Hour))
		s := f.find(running(t.TempDir()))
		if s.Declined == "" || !strings.Contains(s.Declined, "has not recorded") || s.Remedy == "" {
			t.Fatalf("a running jail that never recorded must decline the sweep, naming a remedy: %+v", s)
		}
	})
	t.Run("a record in force could not tell", func(t *testing.T) {
		f := newMiseFixture(t, "node/20.1.0")
		if err := miseuse.Write(f.store, miseuse.NewName(), miseuse.Record{
			Workspace: "/home/u/code/a", Recorded: f.now.Add(-time.Hour), Unknown: "`mise ls`: exit status 1",
		}); err != nil {
			t.Fatal(err)
		}
		s := f.find(nothingRunning())
		if s.Declined == "" || !strings.Contains(s.Declined, "/home/u/code/a") {
			t.Fatalf("an unknown record in force must decline, naming its workspace: %+v", s)
		}
	})
	t.Run("a recent record cannot be read", func(t *testing.T) {
		f := newMiseFixture(t, "node/20.1.0")
		bad := filepath.Join(f.store, miseuse.DirName, "00000000000000aa.json")
		if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(bad, f.now.Add(-time.Hour), f.now.Add(-time.Hour))
		if s := f.find(nothingRunning()); s.Declined == "" {
			t.Fatalf("an unreadable record in force must decline: %+v", s)
		}
	})
	t.Run("an expired unknown record does not", func(t *testing.T) {
		f := newMiseFixture(t, "node/20.1.0")
		if err := miseuse.Write(f.store, miseuse.NewName(), miseuse.Record{
			Workspace: "/home/u/code/a", Recorded: f.now.Add(-miseuse.Window - time.Hour), Unknown: "boom",
		}); err != nil {
			t.Fatal(err)
		}
		if s := f.find(nothingRunning()); s.Declined != "" || len(s.Candidates) != 1 {
			t.Fatalf("an unknown record past the window still blocked the sweep: %+v", s)
		}
	})
}

// TestNothingIsJudgedUntilTheRecordCoversAWindow: before the record is a window old, a version a
// workspace used under an older yolo — which never recorded — would read as unused. So the sweep
// waits, and says until when. A wait is not a decline.
func TestNothingIsJudgedUntilTheRecordCoversAWindow(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0")
	f.since(f.now.Add(-10 * 24 * time.Hour))
	s := f.find(nothingRunning())
	if s.Waiting == "" || s.Declined != "" || len(s.Candidates) != 0 {
		t.Fatalf("a record ten days old judged: %+v", s)
	}
	if !strings.Contains(s.Waiting, f.now.Add(-10*24*time.Hour).Add(miseuse.Window).Local().Format("2006-01-02")) {
		t.Errorf("the wait does not say when judging can start: %q", s.Waiting)
	}

	g := newMiseFixture(t, "node/20.1.0")
	_ = os.Remove(filepath.Join(g.store, miseuse.DirName, miseuse.SinceName))
	if s := g.find(nothingRunning()); s.Waiting == "" || len(s.Candidates) != 0 {
		t.Fatalf("a store no jail ever recorded judged: %+v", s)
	}
}

// TestAFreshInstallIsKeptByItsOwnAge: a version a jail installed since its last record is named
// by no record yet. Its directory's age is what keeps it.
func TestAFreshInstallIsKeptByItsOwnAge(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0")
	f.now = time.Now().Add(24 * time.Hour)
	f.since(f.now.Add(-miseuse.Window - 24*time.Hour))
	f.record("/home/u/code/a", f.now.Add(-time.Hour))
	if s := f.find(nothingRunning()); len(s.Candidates) != 0 || s.Declined != "" {
		t.Fatalf("a version installed a day ago is a candidate: %+v", s)
	}
}

// TestALinkIsNeverAVersion: mise keeps aliases ("22 -> ./22.5.0") and `mise link` entries that
// point outside the store. Neither is a version yolo installed, so neither is a candidate.
func TestALinkIsNeverAVersion(t *testing.T) {
	f := newMiseFixture(t, "node/22.5.0")
	outside := t.TempDir()
	tdir := filepath.Join(f.store, "installs", "node")
	if err := os.Symlink("./22.5.0", filepath.Join(tdir, "22")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tdir, "system")); err != nil {
		t.Fatal(err)
	}
	f.record("/home/u/code/a", f.now.Add(-time.Hour))
	s := f.find(nothingRunning())
	if got := candidateRels(s); len(got) != 1 || got[0] != "node/22.5.0" || s.Installed != 1 {
		t.Fatalf("candidates = %v (installed %d), want only the real directory", got, s.Installed)
	}
}

// TestARemovalTakesTheVersionAndItsAliasesAndNothingElse: the removal renames the version out of
// mise's sight, deletes it, and drops the aliases that named it — and it never reaches through a
// link to anything outside the store.
func TestARemovalTakesTheVersionAndItsAliasesAndNothingElse(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0", "node/22.5.0")
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(f.store, "installs", "node")
	for name, target := range map[string]string{"20": "./20.1.0", "20.1": "20.1.0", "22": "./22.5.0", "system": outside} {
		if err := os.Symlink(target, filepath.Join(tdir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A link INSIDE the doomed version to outside the store: removed as a link, never followed.
	if err := os.Symlink(outside, filepath.Join(tdir, "20.1.0", "escape")); err != nil {
		t.Fatal(err)
	}
	f.record("/home/u/code/a", f.now.Add(-time.Hour), "node/22.5.0")
	s := PruneUnusedMiseVersions(f.store, nothingRunning(), true, f.now, time.Minute)
	if len(s.Removed) != 1 || s.Removed[0].Rel != "node/20.1.0" || len(s.Failed) != 0 {
		t.Fatalf("removed %+v, failed %+v; want node/20.1.0 alone", s.Removed, s.Failed)
	}
	for rel, want := range map[string]bool{"node/20.1.0": false, "node/20": false, "node/20.1": false,
		"node/22.5.0": true, "node/22": true, "node/system": true} {
		if got := f.present(rel); got != want {
			t.Errorf("%s present = %v, want %v", rel, got, want)
		}
	}
	entries, _ := os.ReadDir(tdir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), miseReclaimPrefix) {
			t.Errorf("the removal left %s behind", e.Name())
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the removal reached outside the store: %v", err)
	}
}

// TestARemovalRechecksTheRecords: between the judgement and the removal a jail can start using a
// version. The removal reads the records again, and keeps a version a record now names.
func TestARemovalRechecksTheRecords(t *testing.T) {
	f := newMiseFixture(t, "node/20.1.0")
	f.record("/home/u/code/a", f.now.Add(-time.Hour))
	s := f.find(nothingRunning())
	if len(s.Candidates) != 1 {
		t.Fatalf("want one candidate, got %+v", s)
	}
	f.record("/home/u/code/b", f.now.Add(-time.Minute), "node/20.1.0")
	var rechecked bool
	s = PruneUnusedMiseVersionsGuarded(s, f.now, func(recheck func() bool, del func()) bool {
		rechecked = true
		if !recheck() {
			return false
		}
		del()
		return true
	})
	if !rechecked || len(s.Removed) != 0 || !f.present("node/20.1.0") {
		t.Fatalf("a version a jail recorded after the judgement was removed (rechecked %v): %+v", rechecked, s)
	}
}

// TestALeftoverRemovalIsFinished: a removal interrupted after its rename leaves a hidden
// directory mise no longer lists. The next pass finishes it.
func TestALeftoverRemovalIsFinished(t *testing.T) {
	f := newMiseFixture(t, "node/22.5.0")
	f.install("node/" + miseReclaimPrefix + "20.1.0.1")
	f.record("/home/u/code/a", f.now.Add(-time.Hour), "node/22.5.0")
	s := PruneUnusedMiseVersions(f.store, nothingRunning(), true, f.now, time.Minute)
	if f.present("node/" + miseReclaimPrefix + "20.1.0.1") {
		t.Fatalf("the leftover is still there: %+v", s)
	}
	if !f.present("node/22.5.0") {
		t.Fatal("a version in use went with the leftover")
	}
}
