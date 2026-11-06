package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// orphanFixture is one cname's full set of per-name state under a temp global storage, every
// file aged past the floor. ws is the workspace path its pointer records (not created: gone).
type orphanFixture struct {
	gs, build, containers string
	dirs                  OrphanCnameDirs
	cname, ws             string
	files                 map[string][]string // class -> paths
	now                   time.Time
}

func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()
	gs := t.TempDir()
	if r, err := filepath.EvalSymlinks(gs); err == nil {
		gs = r
	}
	f := &orphanFixture{gs: gs, build: filepath.Join(gs, "build"), containers: filepath.Join(gs, "containers"),
		now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), files: map[string][]string{}}
	f.dirs = OrphanCnameDirsUnder(gs, f.build, f.containers)
	f.ws = filepath.Join(gs, "workspaces", "gone-ws")
	f.cname = runtime.FromResolved(f.ws)
	f.add(t, f.cname)
	return f
}

// add writes every per-name file for cname, aged past the floor; only the fixture's own cname
// gets a current-image pointer (at f.ws).
func (f *orphanFixture) add(t *testing.T, cname string) {
	t.Helper()
	old := f.now.Add(-OrphanCnameAgeFloor - 24*time.Hour)
	write := func(class, p, body string) {
		mustMkdir(t, filepath.Dir(p))
		mustWrite(t, p, []byte(body))
		backdate(t, p, old)
		f.files[class] = append(f.files[class], p)
	}
	for _, s := range []string{".lock", ".keeper", ".sessions", ".macos-user.arrival", ".host-daemons.lock"} {
		write(OrphanClassLocks, filepath.Join(f.dirs.Locks, cname+s), "")
	}
	rec := filepath.Join(f.dirs.Locks, cname+".session-records")
	mustMkdir(t, rec)
	mustWrite(t, filepath.Join(rec, "1234.lock"), []byte("{}"))
	backdate(t, filepath.Join(rec, "1234.lock"), old)
	backdate(t, rec, old)
	f.files[OrphanClassLocks] = append(f.files[OrphanClassLocks], rec)
	for _, n := range []string{"jail-keeper-" + cname + ".log", cname + "-socat.log", cname + "-socat.log.1",
		cname + "-socat.log.lock", cname + "-cgd.log"} {
		write(OrphanClassLogs, filepath.Join(f.dirs.Logs, n), "log")
	}
	write(OrphanClassOwners, filepath.Join(f.dirs.Owners, cname), "4242\n")
	for _, s := range []string{".json", ".scope.json", ".scope-sources.json", ".loophole-grants.json"} {
		write(OrphanClassApprovals, filepath.Join(f.dirs.Approvals, cname+s), "{}")
	}
	if cname != f.cname {
		return // no pointer: the workspace cannot be told
	}
	write(OrphanClassCurrentImages, filepath.Join(f.dirs.CurrentImages, cname), "/nix/store/x-image\n"+f.ws+"\n")
}

func (f *orphanFixture) sweep(known map[string]struct{}, apply bool, guard Guard) OrphanCnameReport {
	if known == nil {
		known = map[string]struct{}{}
	}
	return PruneOrphanCnameStateGuarded(f.dirs, known, map[string]bool{}, true, apply, f.now, guard)
}

func (f *orphanFixture) assertClass(t *testing.T, class string, wantGone bool) {
	t.Helper()
	for _, p := range f.files[class] {
		_, err := os.Lstat(p)
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s %s: gone=%v, want %v (%v)", class, filepath.Base(p), gone, wantGone, err)
		}
	}
}

func (f *orphanFixture) assertAll(t *testing.T, wantGone bool) {
	t.Helper()
	for _, c := range []string{OrphanClassLocks, OrphanClassLogs, OrphanClassOwners, OrphanClassApprovals,
		OrphanClassCurrentImages} {
		f.assertClass(t, c, wantGone)
	}
}

func TestOrphanCnameOfParsesOnlyPerNameFiles(t *testing.T) {
	// The legacy jail name, and a setup's (named-setups-plan.md SUP-D2).
	for _, c := range []string{"yolo-fork-1535ffccf8c9e593-9764d892", "yolo-fork-1535ffccf8c9e593-9764d892-seatbelt-self"} {
		orphanCnameOfParses(t, c)
	}
}

func orphanCnameOfParses(t *testing.T, c string) {
	t.Helper()
	for _, tc := range []struct {
		class, base string
		ok          bool
	}{
		{OrphanClassLocks, c + ".lock", true},
		{OrphanClassLocks, c + ".keeper", true},
		{OrphanClassLocks, c + ".guest.keeper", true},
		{OrphanClassLocks, c + ".macos-user.session-records", true},
		{OrphanClassLocks, c + ".host-daemons.lock", true},
		{OrphanClassLogs, "jail-keeper-" + c + ".log", true},
		{OrphanClassLogs, c + "-cgd.log", true},
		{OrphanClassLogs, c + "-socat.log.1", true},
		{OrphanClassOwners, c, true},
		{OrphanClassCurrentImages, c, true},
		{OrphanClassApprovals, c + ".scope-sources.json", true},
		// The machine-wide files: never candidates.
		{OrphanClassLocks, "image-copy.lock", false},
		{OrphanClassLocks, "housekeeping.lock", false},
		{OrphanClassLocks, "housekeeping-pass.lock", false},
		{OrphanClassLocks, "host-apply.lock", false},
		{OrphanClassLocks, "capture-yolo-capture-fixture.lock", false},
		{OrphanClassLocks, "fork-build-ffe93fa1deb7391f.lock", false},
		{OrphanClassLocks, "macos-user-sessions", false},
		{OrphanClassLocks, "macos-user-pack-trees", false},
		{OrphanClassLocks, "macos-user-home", false},
		{OrphanClassLogs, "host-service-aws-auth.log", false},
		{OrphanClassLogs, "launches.log.lock", false},
		{OrphanClassLogs, "crossings.log", false},
		{OrphanClassOwners, c + ".tmp", false},
		{OrphanClassLocks, c, false},
	} {
		got, ok := OrphanCnameOf(tc.class, tc.base)
		if ok != tc.ok || (ok && got != c) {
			t.Errorf("OrphanCnameOf(%s, %q) = %q, %v; want ok=%v", tc.class, tc.base, got, ok, tc.ok)
		}
	}
}

// The approval suffixes this sweep parses are internal/config's: a part config adds and this
// sweep does not parse would be left behind (a stale part of a removed record).
func TestOrphanApprovalSuffixesMatchConfig(t *testing.T) {
	c := "yolo-ws-12345678"
	for _, p := range config.ApprovalRecordFiles(c) {
		if got, ok := OrphanCnameOf(OrphanClassApprovals, filepath.Base(p)); !ok || got != c {
			t.Errorf("approval record part %s does not parse as %s's (%q, %v)", filepath.Base(p), c, got, ok)
		}
		if filepath.Dir(p) != paths.ApprovalsDir() {
			t.Errorf("approval part %s is not under paths.ApprovalsDir", p)
		}
	}
}

func TestOrphanSweepRemovesAnAgedOrphanWhoseWorkspaceIsGone(t *testing.T) {
	f := newOrphanFixture(t)
	dry := f.sweep(nil, false, nil)
	if dry.Files == 0 || len(dry.Names) != 1 {
		t.Fatalf("dry run found %d files of %v", dry.Files, dry.Names)
	}
	f.assertAll(t, false) // a dry run removes nothing
	rep := f.sweep(nil, true, nil)
	if rep.Files != dry.Files || rep.Busy != 0 {
		t.Errorf("apply removed %d (busy %d), dry run predicted %d", rep.Files, rep.Busy, dry.Files)
	}
	f.assertAll(t, true)
}

func TestOrphanSweepDeclinesWhenTheRuntimeCannotBeAsked(t *testing.T) {
	f := newOrphanFixture(t)
	rep := PruneOrphanCnameStateGuarded(f.dirs, map[string]struct{}{}, nil, false, true, f.now, nil)
	if !rep.Declined || rep.Files != 0 {
		t.Errorf("unknown liveness: %+v, want declined with nothing removed", rep)
	}
	f.assertAll(t, false)
}

func TestOrphanSweepKeepsALiveTrackedOrSessionName(t *testing.T) {
	t.Run("container listed", func(t *testing.T) {
		f := newOrphanFixture(t)
		f.sweep(map[string]struct{}{f.cname: {}}, true, nil)
		f.assertAll(t, false)
	})
	t.Run("tracked", func(t *testing.T) {
		f := newOrphanFixture(t)
		mustMkdir(t, f.containers)
		mustWrite(t, filepath.Join(f.containers, f.cname), []byte("{}"))
		f.sweep(nil, true, nil)
		f.assertAll(t, false)
	})
	t.Run("session", func(t *testing.T) {
		f := newOrphanFixture(t)
		PruneOrphanCnameStateGuarded(f.dirs, map[string]struct{}{}, map[string]bool{paths.JailShortHash(f.cname): true},
			true, true, f.now, nil)
		f.assertAll(t, false)
	})
}

func TestOrphanSweepAgeFloorIsPerName(t *testing.T) {
	f := newOrphanFixture(t)
	// One recent file — a launch lock a launch just truncated — keeps the whole name.
	recent := filepath.Join(f.dirs.Locks, f.cname+".lock")
	backdate(t, recent, f.now.Add(-time.Hour))
	rep := f.sweep(nil, true, nil)
	if rep.Files != 0 {
		t.Errorf("removed %d files of a name with a recent file", rep.Files)
	}
	f.assertAll(t, false)
}

func TestOrphanSweepKeepsApprovalsAndPointerWhileTheWorkspaceExists(t *testing.T) {
	f := newOrphanFixture(t)
	mustMkdir(t, f.ws)
	f.sweep(nil, true, nil)
	f.assertClass(t, OrphanClassApprovals, false)
	f.assertClass(t, OrphanClassCurrentImages, false)
	// The rest of the name's state carries no approval and goes.
	f.assertClass(t, OrphanClassLocks, true)
	f.assertClass(t, OrphanClassLogs, true)
	f.assertClass(t, OrphanClassOwners, true)
}

func TestOrphanSweepKeepsApprovalsWithNoPointerOrAMismatchedOne(t *testing.T) {
	t.Run("no pointer", func(t *testing.T) {
		f := newOrphanFixture(t)
		other := "yolo-other-0badc0de"
		f.add(t, other)
		f.sweep(nil, true, nil)
		for _, s := range []string{".json", ".scope.json", ".scope-sources.json", ".loophole-grants.json"} {
			if _, err := os.Lstat(filepath.Join(f.dirs.Approvals, other+s)); err != nil {
				t.Errorf("approval part %s of a name with no pointer was removed (%v)", s, err)
			}
		}
		if _, err := os.Lstat(filepath.Join(f.dirs.Owners, other)); !os.IsNotExist(err) {
			t.Errorf("the pointerless name's owner file was kept (%v)", err)
		}
	})
	t.Run("pointer names a workspace that does not hash to the name", func(t *testing.T) {
		f := newOrphanFixture(t)
		ptr := filepath.Join(f.dirs.CurrentImages, f.cname)
		mustWrite(t, ptr, []byte("/nix/store/x-image\n"+filepath.Join(f.gs, "elsewhere")+"\n"))
		backdate(t, ptr, f.now.Add(-OrphanCnameAgeFloor-time.Hour))
		f.sweep(nil, true, nil)
		f.assertClass(t, OrphanClassApprovals, false)
		f.assertClass(t, OrphanClassCurrentImages, false)
	})
}

func TestOrphanSweepSkipsABusyLock(t *testing.T) {
	f := newOrphanFixture(t)
	held := filepath.Join(f.dirs.Locks, f.cname+".keeper")
	h, err := os.Open(held)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := syscall.Flock(int(h.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	rep := f.sweep(nil, true, nil)
	if rep.Busy != 1 {
		t.Errorf("busy=%d, want 1", rep.Busy)
	}
	if _, err := os.Lstat(held); err != nil {
		t.Errorf("a held lock file was removed (%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Locks, f.cname+".sessions")); !os.IsNotExist(err) {
		t.Errorf("a free lock file of the same name was kept (%v)", err)
	}
}

func TestOrphanSweepSkipsALockWhoseInodeChanged(t *testing.T) {
	f := newOrphanFixture(t)
	target := filepath.Join(f.dirs.Locks, f.cname+".lock")
	orphanAfterLock = func(p string) {
		if p != target {
			return
		}
		// Another process unlinked and recreated it between our open and our flock.
		_ = os.Remove(p)
		_ = os.WriteFile(p, []byte("new"), 0o644)
	}
	t.Cleanup(func() { orphanAfterLock = nil })
	rep := f.sweep(nil, true, nil)
	if rep.Busy != 1 {
		t.Errorf("busy=%d, want 1 (the replaced lock)", rep.Busy)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "new" {
		t.Errorf("the recreated lock file was removed or changed (%q, %v)", b, err)
	}
}

func TestOrphanSweepRechecksTrackingUnderTheGuard(t *testing.T) {
	f := newOrphanFixture(t)
	calls := 0
	guard := Guard(func(recheck func() bool, del func()) bool {
		calls++
		if calls == 1 { // a launch starts tracking the name while the first deletion waits
			mustMkdir(t, f.containers)
			mustWrite(t, filepath.Join(f.containers, f.cname), []byte("{}"))
		}
		if !recheck() {
			return false
		}
		del()
		return true
	})
	rep := f.sweep(nil, true, guard)
	if calls == 0 {
		t.Fatal("no deletion went through the guard")
	}
	if rep.Files != 0 {
		t.Errorf("removed %d files of a name tracked since the listing", rep.Files)
	}
	f.assertAll(t, false)
}

func TestAllYoloContainerNamesListsEveryState(t *testing.T) {
	run := stubExec(map[string]string{k("podman", "ps", "-a", "--format", "{{.Names}}"): "yolo-a-11111111\nother\nyolo-b-22222222\n"}, nil)
	got := AllYoloContainerNames("podman", run)
	if !got.Known || len(got.Names) != 2 {
		t.Errorf("got %+v, want both yolo-* names", got)
	}
	if AllYoloContainerNames("podman", func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }).Known {
		t.Error("a runtime that cannot run reported Known")
	}
	if AllYoloContainerNames("", run).Known {
		t.Error("no runtime reported Known")
	}
}

// The `yolo prune` call site: --apply removes an orphan's state, a STOPPED container keeps its
// name's (the any-state listing, not the live set), and an unknown runtime declines.
func TestPruneRunSweepsOrphanedPerJailState(t *testing.T) {
	setup := func(t *testing.T) (Options, *orphanFixture) {
		o, _ := baseOpts(t)
		f := newOrphanFixture(t)
		// Re-point the fixture at the options' storage root.
		o.GlobalStorage = func() string { return f.gs }
		o.BuildDir = func() string { return f.build }
		o.ContainerDir = func() string { return f.containers }
		o.Now = func() time.Time { return f.now }
		return o, f
	}
	t.Run("apply removes", func(t *testing.T) {
		o, f := setup(t)
		o.Apply = true
		var buf bytes.Buffer
		o.Out = &buf
		Run(o)
		want := "  removed: 17 file(s) (6 locks, 5 logs, 1 owners, 1 current-images, 4 approvals) of 1 container name(s)"
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
		if !bytes.Contains(buf.Bytes(), []byte("Orphaned per-jail state")) {
			t.Errorf("no section:\n%s", buf.String())
		}
		f.assertAll(t, true)
	})
	t.Run("stopped container keeps", func(t *testing.T) {
		o, f := setup(t)
		o.Apply = true
		o.Exec = stubExec(map[string]string{
			k("podman", "ps", "-a", "--format", "{{.Names}}"): f.cname + "\n",
		}, nil)
		var buf bytes.Buffer
		o.Out = &buf
		Run(o)
		f.assertAll(t, false)
	})
	t.Run("unknown runtime declines", func(t *testing.T) {
		o, f := setup(t)
		o.Apply = true
		o.Exec = func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }
		var buf bytes.Buffer
		o.Out = &buf
		if rc := Run(o); rc == 0 {
			t.Errorf("a declined sweep exited 0 (OQ-LS2)")
		}
		if !hasLine(&buf, "  skipped — could not list the runtime's containers (podman); declining to sweep") {
			t.Errorf("no decline line:\n%s", buf.String())
		}
		if !hasLine(&buf, "  `podman ps -a` must answer for this sweep to run; `yolo check` diagnoses the runtime. Re-run `yolo prune` once it does.") {
			t.Errorf("the decline names no next step:\n%s", buf.String())
		}
		f.assertAll(t, false)
	})
}

// `yolo prune` on macos-user reads the container runtimes installed beside it through
// InstalledContainerRuntimes, as the macos-user slot does: none installed sweeps on the tracking
// files and sessions alone, an installed runtime's stopped container keeps its name, and an
// installed runtime that cannot answer declines non-zero with a next step. The last two fail if
// prune stops consulting the installed runtimes.
func TestPruneOnMacosUserConsultsInstalledRuntimes(t *testing.T) {
	run := func(t *testing.T, has func(string) bool, exec func(f *orphanFixture) RunFunc) (*orphanFixture, int, string) {
		t.Helper()
		redirectSessions(t)
		o, _ := baseOpts(t)
		f := newOrphanFixture(t)
		o.GlobalStorage = func() string { return f.gs }
		o.BuildDir = func() string { return f.build }
		o.ContainerDir = func() string { return f.containers }
		o.Now = func() time.Time { return f.now }
		o.DetectRuntime = func() string { return "macos-user" }
		o.HasBinary = has
		o.Exec = exec(f)
		o.Apply = true
		var buf bytes.Buffer
		o.Out = &buf
		rc := Run(o)
		return f, rc, buf.String()
	}
	podmanOnly := func(bin string) bool { return bin == "podman" }
	const listing = "podman\x00ps\x00-a\x00--format\x00{{.Names}}"
	t.Run("none installed sweeps", func(t *testing.T) {
		f, _, out := run(t, func(string) bool { return false }, func(*orphanFixture) RunFunc { return noMacosUserExec(t) })
		if !strings.Contains(out, "Orphaned per-jail state") {
			t.Fatalf("no section:\n%s", out)
		}
		f.assertAll(t, true)
	})
	t.Run("an installed runtime's stopped container keeps its name", func(t *testing.T) {
		f, _, out := run(t, podmanOnly, func(f *orphanFixture) RunFunc {
			return stubExec(map[string]string{listing: f.cname + "\n"}, nil)
		})
		if !strings.Contains(out, "Orphaned per-jail state") {
			t.Fatalf("no section:\n%s", out)
		}
		f.assertAll(t, false)
	})
	t.Run("an installed runtime that cannot answer declines", func(t *testing.T) {
		f, rc, out := run(t, podmanOnly, func(*orphanFixture) RunFunc {
			return func(argv []string, _ time.Duration) ProbeResult {
				if strings.Join(argv, "\x00") == listing {
					return ProbeResult{Ran: false}
				}
				return ProbeResult{Ran: true}
			}
		})
		if rc == 0 {
			t.Errorf("a declined sweep exited 0 (OQ-LS2):\n%s", out)
		}
		if !strings.Contains(out, "  skipped — could not list the runtime's containers (podman); declining to sweep") ||
			!strings.Contains(out, "`podman ps -a` must answer for this sweep to run") {
			t.Errorf("no decline with a next step:\n%s", out)
		}
		f.assertAll(t, false)
	})
}

// An approval part that stayed (here its guarded deletion was refused) keeps the pointer, the
// evidence the next sweep needs to prove the workspace gone and finish the record.
func TestOrphanSweepKeepsThePointerWhileAnApprovalPartStays(t *testing.T) {
	f := newOrphanFixture(t)
	calls := 0
	guard := Guard(func(recheck func() bool, del func()) bool {
		calls++
		if calls == 1 { // approvals go first: refuse the first part
			return false
		}
		if !recheck() {
			return false
		}
		del()
		return true
	})
	f.sweep(nil, true, guard)
	kept := 0
	for _, p := range f.files[OrphanClassApprovals] {
		if _, err := os.Lstat(p); err == nil {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("%d approval parts kept, want the 1 whose deletion was refused", kept)
	}
	f.assertClass(t, OrphanClassCurrentImages, false)
	f.assertClass(t, OrphanClassLocks, true)
}

// A SETUP'S JAIL NAME IS SWEPT LIKE THE LEGACY ONE (named-setups-plan.md SUP-D2): every launch now
// keys its per-name state by its workspace and setup (runtime.JailName), so a sweep that parsed
// only the legacy name would leave every such file for ever; and its pointer, whose workspace
// hashes back to the name's project key, proves the workspace gone.
func TestOrphanSweepRemovesASetupsAgedOrphanWhoseWorkspaceIsGone(t *testing.T) {
	gs := t.TempDir()
	if r, err := filepath.EvalSymlinks(gs); err == nil {
		gs = r
	}
	f := &orphanFixture{gs: gs, build: filepath.Join(gs, "build"), containers: filepath.Join(gs, "containers"),
		now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), files: map[string][]string{}}
	f.dirs = OrphanCnameDirsUnder(gs, f.build, f.containers)
	f.ws = filepath.Join(gs, "workspaces", "gone-ws")
	f.cname = runtime.FromResolved(f.ws) + "-podman-self"
	f.add(t, f.cname)
	rep := f.sweep(nil, true, nil)
	if len(rep.Names) != 1 || rep.Names[0] != f.cname {
		t.Errorf("swept %v, want the setup's jail name %s alone", rep.Names, f.cname)
	}
	f.assertAll(t, true)
}
