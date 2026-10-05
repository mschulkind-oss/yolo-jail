package macosuser

// sessionfiles_test.go pins the per-session names and the liveness record (sessionfiles.go):
// two sessions of one workspace write, read and remove only their own files; a launch removes
// its Seatbelt profile and its record when it ends; and a launch sweeps exactly the files of the
// sessions whose records say they ended, keeping every record it cannot judge.
//
// Every RunMacosUser test here fails if its call site is deleted: the mint (two launches would
// share a key), the record (none would be held during the launch), the sweep (the free record's
// files would survive the launch) and the profile's removal (no rm of the profile is recorded).

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// launchedSessionKey is the session key a recorded RunMacosUser minted, read off the profile it
// installed: <stateDir>/profile-<key>.sb.
func launchedSessionKey(t *testing.T, rec []string, ws string) string {
	t.Helper()
	prefix := "install:" + stateDir + "/profile-" + cnameFor(resolvePathAbs(ws)) + "."
	for _, r := range rec {
		if rest, ok := strings.CutPrefix(r, prefix); ok {
			if sid, ok := strings.CutSuffix(rest, ".sb"); ok {
				return SessionKey(cnameFor(resolvePathAbs(ws)), sid)
			}
		}
	}
	t.Fatalf("the launch installed no session profile under %s:\n%s", prefix, strings.Join(rec, "\n"))
	return ""
}

// sessionPlan is one session's plan of a workspace that runs a provisioning stage and a jail
// daemon, so all three sandboxed argvs exist.
func sessionPlan(t *testing.T, sid string) RunPlan {
	t.Helper()
	return BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", provisionCfg(), []string{"codex"},
		[]string{"codex"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(),
		mockDarwin(), nil, openAIAdapterDaemons("/src"), FloorStage{}, PlanSession{ID: sid})
}

// TWO SESSIONS OF ONE WORKSPACE NAME DIFFERENT FILES, and each argv names only its own. The
// defect: every one of these was keyed by the workspace's cname, so session B rewrote A's env
// file between A's write and its read, and the first session to end removed the file under the
// other (jail-lifetime §9.9.10, row 6).
func TestTwoSessionsOfOneWorkspaceNameDifferentFiles(t *testing.T) {
	a, b := sessionPlan(t, "0123456789abcdef"), sessionPlan(t, "fedcba9876543210")
	for _, p := range []RunPlan{a, b} {
		if problems := PlanInvariants(p); len(problems) > 0 {
			t.Fatalf("a well-formed session plan fails its invariants:\n%s", strings.Join(problems, "\n"))
		}
		if len(p.ProvisionArgv) == 0 || len(p.JailDaemonArgv) == 0 || p.EnvFile == "" || p.DaemonEnvFile == "" {
			t.Fatalf("the fixture must produce every sandboxed argv and both env files: %+v", p)
		}
	}
	if a.Cname != b.Cname {
		t.Fatalf("one workspace, two cnames: %s, %s", a.Cname, b.Cname)
	}
	for _, f := range []struct{ what, a, b string }{
		{"profile", a.ProfilePath, b.ProfilePath},
		{"env file", a.EnvFile, b.EnvFile},
		{"daemons env file", a.DaemonEnvFile, b.DaemonEnvFile},
	} {
		if f.a == f.b {
			t.Errorf("both sessions name one %s: %s", f.what, f.a)
		}
	}
	for _, own := range []struct {
		name        string
		self, other RunPlan
	}{{"A", a, b}, {"B", b, a}} {
		for label, argv := range map[string][]string{
			"launch": own.self.LaunchArgv, "provisioning": own.self.ProvisionArgv,
			"jail-daemon": own.self.JailDaemonArgv,
		} {
			joined := strings.Join(argv, "\x00")
			if !strings.Contains(joined, own.self.ProfilePath) {
				t.Errorf("session %s's %s argv does not name its own profile", own.name, label)
			}
			for _, theirs := range []string{own.other.ProfilePath, own.other.EnvFile, own.other.DaemonEnvFile} {
				if strings.Contains(joined, theirs) {
					t.Errorf("session %s's %s argv names the other session's %s", own.name, label, theirs)
				}
			}
		}
	}
	// The workspace's own trees stay shared: the launch lock and the staging are the workspace's.
	if a.PackRoot != b.PackRoot || a.ContextDir != b.ContextDir {
		t.Errorf("the workspace's staged trees moved per session: %s/%s vs %s/%s",
			a.PackRoot, a.ContextDir, b.PackRoot, b.ContextDir)
	}
}

// THE INVARIANT FAILS A PLAN THAT SHARES A NAME, or that does not remove its profile: each half
// is mutated here, so deleting either check fails the test.
func TestPlanInvariantsRefuseAWorkspaceKeyedFileAndAnUnremovedProfile(t *testing.T) {
	plan := sessionPlan(t, "0123456789abcdef")
	shared := plan
	shared.EnvFile = SandboxEnvFile(plan.Cname, plan.StagedDir)
	if !slices.ContainsFunc(PlanInvariants(shared), func(p string) bool {
		return strings.Contains(p, "not named by this session's key")
	}) {
		t.Errorf("a workspace-keyed env file passes the invariants")
	}
	kept := plan
	kept.ProfileRemoveCommands = nil
	if !slices.ContainsFunc(PlanInvariants(kept), func(p string) bool {
		return strings.Contains(p, "nothing removes the session's Seatbelt profile")
	}) {
		t.Errorf("a plan that never removes its profile passes the invariants")
	}
	none := plan
	none.SessionID = ""
	if !slices.ContainsFunc(PlanInvariants(none), func(p string) bool { return strings.Contains(p, "no session id") }) {
		t.Errorf("a plan with no session id passes the invariants")
	}
}

// A LAUNCH MINTS ITS OWN SESSION, removes its profile and its env file when it ends, and holds
// its record for the whole launch, removing it LAST. Driven through RunMacosUser twice, so the
// mint, the record and the profile's removal are the orchestrator's.
func TestALaunchHoldsItsRecordAndRemovesItsOwnFilesLast(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	dir := t.TempDir()
	var keys []string
	for i := 0; i < 2; i++ {
		var rec []string
		d := mockDeps(&rec)
		d.SessionRecordDir = func() string { return dir }
		heldDuring := sessionUnknown
		d.RunWithProxy = func(argv []string) int {
			matches, _ := filepath.Glob(filepath.Join(dir, "*"+sessionRecordSuffix))
			if len(matches) == 1 {
				f, state := claimGoneSessionRecord(matches[0])
				if f != nil {
					_ = f.Close()
				}
				heldDuring = state
			}
			return 42
		}
		base := d.Run
		recordAtProfileRm := false
		d.Run = func(argv []string) int {
			if len(argv) == 4 && argv[1] == rmBin && strings.Contains(argv[3], "/profile-") {
				matches, _ := filepath.Glob(filepath.Join(dir, "*"+sessionRecordSuffix))
				recordAtProfileRm = len(matches) == 1
			}
			return base(argv)
		}
		var out bytes.Buffer
		d.Out = &out
		if rc := RunMacosUser(d, newOpts(ws)); rc != 42 {
			t.Fatalf("rc = %d\n%s", rc, out.String())
		}
		key := launchedSessionKey(t, rec, ws)
		keys = append(keys, key)
		joined := strings.Join(rec, "\n")
		for _, f := range []string{SessionProfilePath(key, ""), SandboxEnvFile(key, "")} {
			if !strings.Contains(joined, "run:sudo "+rmBin+" -f "+f) {
				t.Errorf("launch %d does not remove %s when it ends:\n%s", i, f, joined)
			}
		}
		if heldDuring != sessionLive {
			t.Errorf("launch %d's record was not held while its agent ran (%v)", i, heldDuring)
		}
		if !recordAtProfileRm {
			t.Errorf("launch %d removed its record before its profile", i)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("launch %d left its record behind: %v", i, left)
		}
	}
	if keys[0] == keys[1] {
		t.Errorf("two launches of one workspace minted one session key: %s", keys[0])
	}
	for _, k := range keys {
		if _, _, ok := parseSessionKey(k); !ok {
			t.Errorf("a launch minted a key the sweep would not parse: %s", k)
		}
	}
}

// heldRecord makes a record another live session holds, in dir.
func heldRecord(t *testing.T, dir, key string) {
	t.Helper()
	r, err := openSessionRecord(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.close)
}

// freeRecord makes the record a session that ended without its teardown leaves: present, and
// locked by nobody.
func freeRecord(t *testing.T, dir, key string) string {
	t.Helper()
	p := filepath.Join(dir, key+sessionRecordSuffix)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// THE SWEEP REMOVES ONLY A FREE RECORD'S FILES, and the record after them. It keeps a record
// another open file holds (a live session), one it cannot open (a directory at the name: this
// jail runs as root, so a mode cannot make a file unopenable), one whose name does not parse, and
// a pending record, which no glob of finished records matches.
func TestTheSweepRemovesOnlyAnEndedSessionsFiles(t *testing.T) {
	dir := t.TempDir()
	cname := cnameFor("/Users/Shared/yolo/proj")
	gone := SessionKey(cname, "0123456789abcdef")
	live := SessionKey(cname, "1111111111111111")
	odd := SessionKey(cname, "2222222222222222")
	goneRecord := freeRecord(t, dir, gone)
	heldRecord(t, dir, live)
	if err := os.Mkdir(filepath.Join(dir, odd+sessionRecordSuffix), 0o700); err != nil {
		t.Fatal(err)
	}
	malformed := []string{"garbage" + sessionRecordSuffix, cname + ".NOTHEX0123456789" + sessionRecordSuffix,
		"Upper.0123456789abcdef" + sessionRecordSuffix,
		SessionKey(cname, "3333333333333333") + sessionRecordSuffix + sessionRecordPendingSuffix}
	for _, m := range malformed {
		if err := os.WriteFile(filepath.Join(dir, m), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var rec []string
	d := mockDeps(&rec)
	var out bytes.Buffer
	sweepGoneSessions(d, printer{w: &out}, dir)

	want := "run:sudo " + rmBin + " -f " + strings.Join(SessionFilePaths(gone, ""), " ")
	if len(rec) != 1 || rec[0] != want {
		t.Fatalf("the sweep ran %q, want exactly %q", rec, want)
	}
	for _, f := range []string{SandboxEnvFile(gone, ""), SandboxDaemonEnvFile(gone, ""),
		CABundleFile(gone, ""), CAExtrasFile(gone, ""), SessionProfilePath(gone, "")} {
		if !strings.Contains(rec[0], " "+f) {
			t.Errorf("the sweep does not remove %s", f)
		}
	}
	if _, err := os.Lstat(goneRecord); !os.IsNotExist(err) {
		t.Errorf("the ended session's record is still there after its files were removed")
	}
	for _, kept := range append([]string{live + sessionRecordSuffix, odd + sessionRecordSuffix}, malformed...) {
		if _, err := os.Lstat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("the sweep removed %s, which it cannot prove ended: %v", kept, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a sweep with nothing failing printed:\n%s", out.String())
	}
}

// A REMOVAL THAT FAILS KEEPS THE RECORD, so the next launch tries again, and warns naming the
// command; it never refuses.
func TestASweepWhoseRemovalFailsKeepsTheRecord(t *testing.T) {
	dir := t.TempDir()
	key := SessionKey(cnameFor("/Users/Shared/yolo/proj"), "0123456789abcdef")
	p := freeRecord(t, dir, key)
	d := mockDeps(nil)
	d.Run = func([]string) int { return 1 }
	var out bytes.Buffer
	sweepGoneSessions(d, printer{w: &out}, dir)
	if _, err := os.Lstat(p); err != nil {
		t.Errorf("a record whose files could not be removed was removed: %v", err)
	}
	if !strings.Contains(out.String(), "sudo "+rmBin+" -f "+SandboxEnvFile(key, "")) {
		t.Errorf("the warning does not name the command that removes the files:\n%s", out.String())
	}
}

// THE CALL SITE: a launch sweeps an ended session's files before it installs its own profile.
// Fails if RunMacosUser stops calling the sweep, or calls it after the first root-owned write.
func TestALaunchSweepsAnEndedSessionsFilesBeforeItWritesItsOwn(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	dir := t.TempDir()
	gone := SessionKey(cnameFor(ws), "0123456789abcdef")
	goneRecord := freeRecord(t, dir, gone)
	var rec []string
	d := mockDeps(&rec)
	d.SessionRecordDir = func() string { return dir }
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, newOpts(ws)); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	joined := strings.Join(rec, "\n")
	sweep := strings.Index(joined, "run:sudo "+rmBin+" -f "+strings.Join(SessionFilePaths(gone, ""), " "))
	install := strings.Index(joined, "install:"+stateDir+"/profile-")
	if sweep < 0 {
		t.Fatalf("the launch did not sweep the ended session's files:\n%s", joined)
	}
	if install < 0 || sweep > install {
		t.Errorf("the sweep ran after the launch's first root-owned write:\n%s", joined)
	}
	if _, err := os.Lstat(goneRecord); !os.IsNotExist(err) {
		t.Errorf("the ended session's record survived the launch's sweep")
	}
}

// A DRY RUN MINTS NOTHING AND WRITES NOTHING: no record, and the plan names the placeholder where
// each launch's id goes, with every invariant holding.
func TestADryRunCreatesNoRecordAndPrintsTheSessionPlaceholder(t *testing.T) {
	dir := t.TempDir()
	var rec []string
	d := mockDeps(&rec)
	d.SessionRecordDir = func() string { return dir }
	var out bytes.Buffer
	d.Out = &out
	o := newOpts("/Users/Shared/yolo/proj")
	o.DryRun = true
	if rc := RunMacosUser(d, o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("a dry run created %v", left)
	}
	key := SessionKey(cnameFor(resolvePathAbs(o.Workspace)), SessionPlaceholder)
	for _, want := range []string{"session:     " + key, "profile-" + key + ".sb",
		SandboxEnvFile(key, ""), "when the session ends", "all plan invariants hold"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the dry run does not print %q:\n%s", want, out.String())
		}
	}
	if len(rec) != 0 {
		t.Errorf("a dry run ran %v", rec)
	}
}

// THE RECORD APPEARS UNDER ITS NAME ALREADY HELD, and leaves no pending file; its close removes
// it before letting the lock go.
func TestASessionRecordIsHeldUnderItsNameAndRemovedOnClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks", sessionRecordLeaf)
	key := SessionKey("yolo-proj-0420db18", "0123456789abcdef")
	r, err := openSessionRecord(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != key+sessionRecordSuffix {
		t.Fatalf("the record dir holds %v, want only %s", entries, key+sessionRecordSuffix)
	}
	if f, state := claimGoneSessionRecord(filepath.Join(dir, key+sessionRecordSuffix)); state != sessionLive {
		if f != nil {
			_ = f.Close()
		}
		t.Errorf("a held record reads as %v, want live", state)
	}
	r.close()
	r.close() // idempotent
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("close left %v", entries)
	}
}

// AN UNLINKED RECORD IS NOT EVIDENCE: a sweeper that opened a record before its owner removed it
// and locked it after holds a file that is no longer the one at the path, and a record of the
// same name made since is not that one. Fails if the SameFile check is dropped.
func TestAClaimOnARecordReplacedSinceItOpenedIsUnknown(t *testing.T) {
	dir := t.TempDir()
	p := freeRecord(t, dir, SessionKey("yolo-proj-0420db18", "0123456789abcdef"))
	stale, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if f, state := claimOpenedSessionRecord(stale, p); state != sessionUnknown {
		if f != nil {
			_ = f.Close()
		}
		t.Errorf("a claim through an unlinked record reads as %v, want unknown", state)
	}
	heldRecord(t, dir, SessionKey("yolo-proj-0420db18", "0123456789abcdef"))
	stale2, err := os.OpenFile(filepath.Join(dir, "other"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if f, state := claimOpenedSessionRecord(stale2, p); state != sessionUnknown {
		if f != nil {
			_ = f.Close()
		}
		t.Errorf("a claim through a file that is not the record at the path reads as %v, want unknown", state)
	}
}

// THE KEY PARSES BACK STRICTLY: only a container name and a 16-hex id, which is all a sweep ever
// derives paths from.
func TestParseSessionKeyIsStrict(t *testing.T) {
	for key, want := range map[string]bool{
		"yolo-proj-0420db18.0123456789abcdef": true,
		"yolo-proj-0420db18.0123456789ABCDEF": false,
		"yolo-proj-0420db18.0123456789abcde":  false,
		"yolo-proj-0420db18":                  false,
		".0123456789abcdef":                   false,
		"Yolo.0123456789abcdef":               false,
		"yolo/x.0123456789abcdef":             false,
		"yolo..0123456789abcdef":              false,
		"yolo-proj.<session>":                 false,
	} {
		if _, _, ok := parseSessionKey(key); ok != want {
			t.Errorf("parseSessionKey(%q) ok = %v, want %v", key, ok, want)
		}
	}
	if got := newSessionID(); len(got) != 16 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Errorf("newSessionID() = %q, want 16 lowercase hex digits", got)
	}
}

// A TEARDOWN WHOSE REMOVAL FAILS KEEPS THE RECORD, free, for the next launch's sweep, and warns
// with the command. The record is the only pointer to a session's files once the session ends
// (env/ is a directory only root can list, and the names carry an id nothing else remembers), so
// unlinking it after a removal that failed, say the teardown's sudo not authenticating after a
// long session, left the env file and its credentials under a name no sweep would ever find.
// Driven through RunMacosUser for each of the session's removals, the supervisor's on both its
// exits; fails if any of them goes around the teardown, or if the teardown unlinks the record
// regardless.
func TestATeardownWhoseRemovalFailsKeepsTheRecordForTheNextSweep(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	for _, tc := range []struct {
		name   string
		fails  func(path string) bool
		exited bool
		wantRC int
	}{
		{"profile", func(p string) bool { return strings.Contains(p, "/profile-") }, false, 42},
		{"env file", func(p string) bool { return strings.HasSuffix(p, ".env") && !strings.HasSuffix(p, ".daemons.env") }, false, 42},
		{"daemons env file", func(p string) bool { return strings.HasSuffix(p, ".daemons.env") }, false, 42},
		{"daemons env file, supervisor exited", func(p string) bool { return strings.HasSuffix(p, ".daemons.env") }, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var rec []string
			d := mockDeps(&rec)
			d.SessionRecordDir = func() string { return dir }
			fakeSupervisor(&d, &rec, ws, "", readyLine, "", tc.exited)
			d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
			base := d.Run
			failed := ""
			d.Run = func(argv []string) int {
				rc := base(argv)
				if len(argv) == 4 && argv[0] == "sudo" && argv[1] == rmBin && argv[2] == "-f" && tc.fails(argv[3]) {
					failed = argv[3]
					return 1 // e.g. sudo could not authenticate at the end of a long session
				}
				return rc
			}
			var out bytes.Buffer
			d.Out = &out
			o := newOpts(ws)
			o.JailDaemons = openAIAdapterDaemons("")
			if rc := RunMacosUser(d, o); rc != tc.wantRC {
				t.Fatalf("rc = %d, want %d\n%s", rc, tc.wantRC, out.String())
			}
			if failed == "" {
				t.Fatalf("the launch never removed the %s:\n%s", tc.name, strings.Join(rec, "\n"))
			}
			key := launchedSessionKey(t, rec, ws)
			record := filepath.Join(dir, key+sessionRecordSuffix)
			f, state := claimGoneSessionRecord(record)
			if f != nil {
				_ = f.Close()
			}
			if state != sessionGone {
				t.Fatalf("removing %s failed, and the record is %v after the session ended, want "+
					"kept and free for the next sweep (records left: %v)", failed, state, dirNames(t, dir))
			}
			want := "sudo " + rmBin + " -f " + strings.Join(SessionFilePaths(key, ""), " ")
			if !strings.Contains(out.String(), want) {
				t.Errorf("the failed removal does not warn with %q:\n%s", want, out.String())
			}

			// The next launch's sweep takes the record and removes the files.
			var next []string
			sweepGoneSessions(mockDeps(&next), printer{w: &bytes.Buffer{}}, dir)
			if len(next) != 1 || next[0] != "run:"+want {
				t.Errorf("the next sweep ran %q, want %q", next, "run:"+want)
			}
			if _, err := os.Lstat(record); !os.IsNotExist(err) {
				t.Errorf("the next sweep left the record behind: %v", err)
			}
		})
	}
}

// dirNames lists dir's entries by name.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// RELEASE LETS THE LOCK GO AND KEEPS THE RECORD: what a session whose own removal failed does, so
// the next sweep reads it as ended and removes the files it names.
func TestASessionRecordReleasedIsKeptAndFree(t *testing.T) {
	dir := t.TempDir()
	key := SessionKey("yolo-proj-0420db18", "0123456789abcdef")
	r, err := openSessionRecord(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	r.release()
	r.release() // idempotent
	r.close()   // and a close after it removes nothing
	f, state := claimGoneSessionRecord(filepath.Join(dir, key+sessionRecordSuffix))
	if f != nil {
		_ = f.Close()
	}
	if state != sessionGone {
		t.Errorf("a released record reads as %v, want kept and free", state)
	}
}

// THE RECORD NEVER EXISTS UNLOCKED UNDER ITS FINAL NAME: until the rename it is a held pending
// file, which the sweep's glob does not match. Created under its final name, it would sit
// unlocked between the create and the flock, and a sweep in that gap would read the session as
// ended and remove the files it is about to write. Fails if the pending name is dropped.
func TestASessionRecordIsCreatedHeldUnderAPendingName(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, SessionKey("yolo-proj-0420db18", "0123456789abcdef")+sessionRecordSuffix)
	f, pending, err := createHeldPendingRecord(final)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		t.Errorf("the record exists under its final name before it is held there (%v)", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*"+sessionRecordSuffix)); len(matches) != 0 {
		t.Errorf("the sweep's glob matches a record that is not yet renamed: %v", matches)
	}
	g, state := claimGoneSessionRecord(pending)
	if g != nil {
		_ = g.Close()
	}
	if state != sessionLive {
		t.Errorf("the pending record reads as %v before the rename, want held", state)
	}
}

// openSessionRecord ITSELF GOES THROUGH THE PENDING NAME, which the test above pins only in its
// callee: with something already at <key>.lock.pending, the open must fail, and leave nothing
// under the final name. Created directly under its final name (and locked after), the record
// would open fine here, so this fails if openSessionRecord stops calling createHeldPendingRecord.
func TestOpeningASessionRecordGoesThroughItsPendingName(t *testing.T) {
	dir := t.TempDir()
	key := SessionKey("yolo-proj-0420db18", "0123456789abcdef")
	final := filepath.Join(dir, key+sessionRecordSuffix)
	if err := os.Mkdir(final+sessionRecordPendingSuffix, 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := openSessionRecord(dir, key)
	if err == nil {
		r.close()
		t.Fatalf("openSessionRecord succeeded with its pending name taken; it did not create the " +
			"record under that name first")
	}
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		t.Errorf("a failed open left a record under its final name (%v)", err)
	}
}

// THE PRODUCTION SEAMS ARE WIRED. Nothing on Linux runs RealDeps' launch, and each seam's nil is
// a launch that silently loses a feature rather than one that fails: no record dir, so no record
// is kept and a killed session's files are never swept; no keychain reader, so every launch warns
// it could not read the keychain; no verifier, so no CA is ever trusted.
func TestRealDepsWireTheSessionRecordAndTheKeychain(t *testing.T) {
	d := RealDeps(nil, nil, false)
	if d.SessionRecordDir == nil || d.SessionRecordDir() != SessionRecordsDir() {
		t.Errorf("RealDeps does not keep liveness records in %s", SessionRecordsDir())
	}
	for name, seam := range map[string]struct{ wired, real any }{
		"ReadSystemKeychain": {d.ReadSystemKeychain, readSystemKeychainReal},
		"VerifyCA":           {d.VerifyCA, verifyCAReal},
	} {
		if reflect.ValueOf(seam.wired).IsNil() ||
			reflect.ValueOf(seam.wired).Pointer() != reflect.ValueOf(seam.real).Pointer() {
			t.Errorf("RealDeps().%s is not the real keychain call", name)
		}
	}
	if !strings.HasSuffix(SessionRecordsDir(), filepath.Join("locks", sessionRecordLeaf)) {
		t.Errorf("the records live in %s, not beside the launch locks", SessionRecordsDir())
	}
}
