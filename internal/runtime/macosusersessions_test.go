package runtime

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// plantSession makes a session dir under base the way the launch's openServicesSession does, and
// says how its liveness question should come out: "live" holds the lock on a descriptor of the
// test's own (a separate open, so as far as flock is concerned another holder), "gone" leaves the
// lock file unheld, and "nolock" creates no lock file at all. rec, when non-nil, is written as the
// session's record.
func plantSession(t *testing.T, base, cname, state string, rec *SessionRecord) string {
	t.Helper()
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix(cname))
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		if err := WriteSessionRecord(dir, *rec); err != nil {
			t.Fatal(err)
		}
	}
	if state == "nolock" {
		return dir
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.HostServicesSessionLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if state == "gone" {
		_ = f.Close()
		return dir
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return dir
}

func sessionByDir(sessions []Session) map[string]Session {
	out := map[string]Session{}
	for _, s := range sessions {
		out[s.Dir] = s
	}
	return out
}

// TestListSessionsAnswersTriStateAndRemovesNothing: a held lock is live, a free one is gone, a
// dir with no lock file is unknown, and the listing leaves every one of them where it was. The
// gone dir is the one a sweep would remove, and this lister is not a sweep.
func TestListSessionsAnswersTriStateAndRemovesNothing(t *testing.T) {
	base := t.TempDir()
	const cname = "yolo-ws-0001"
	rec := &SessionRecord{Notch: NotchMacosUser, Workspace: "/ws", Name: cname}
	live := plantSession(t, base, cname, "live", rec)
	gone := plantSession(t, base, cname, "gone", rec)
	noLock := plantSession(t, base, cname, "nolock", rec)
	// A container jail's dir of the same name is no session's (no dash after the hash).
	container := filepath.Join(base, paths.HostServicesDirName(paths.JailShortHash(cname)))
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}

	sessions, ok := ListSessions(base)
	if !ok {
		t.Fatal("ListSessions reported that it could not list")
	}
	got := sessionByDir(sessions)
	for dir, want := range map[string]SessionLiveness{live: SessionLive, gone: SessionGone, noLock: SessionUnknown} {
		s, found := got[dir]
		if !found {
			t.Errorf("%s is not listed", dir)
			continue
		}
		if s.Liveness != want {
			t.Errorf("%s lists as %s, want %s", dir, s.Liveness, want)
		}
		if s.Key != paths.JailShortHash(cname) {
			t.Errorf("%s has key %q, want the workspace key %q", dir, s.Key, paths.JailShortHash(cname))
		}
	}
	if _, found := got[container]; found {
		t.Errorf("the container jail's dir %s is listed as a session", container)
	}
	if len(sessions) != 3 {
		t.Errorf("listed %d sessions, want 3: %+v", len(sessions), sessions)
	}
	for _, d := range []string{live, gone, noLock, container} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("the listing removed %s: %v", d, err)
		}
	}
	// And the live session's lock is still the owner's alone: a listing that took it, even
	// shared, and kept it would read as a second holder.
	if f, state := LockSession(live, true); state != SessionLive {
		if f != nil {
			_ = f.Close()
		}
		t.Errorf("after the listing, the live session's lock reads %s to an exclusive probe", state)
	}
}

// TestAListingIsNotReadAsAHolderByAnotherListing: two listings run at once — `yolo ps` beside a
// container launch's housekeeping slot, or a second `yolo ps` — so one listing's probe must never
// read as a holder to the other. The probe is SHARED for exactly that: a shared lock another
// descriptor holds leaves a gone session gone. Were the probe exclusive, the second listing would
// read the first's probe as the session's owner, list an ended session as running, and keep its
// staging from every sweep.
func TestAListingIsNotReadAsAHolderByAnotherListing(t *testing.T) {
	base := t.TempDir()
	gone := plantSession(t, base, "yolo-ws-0001", "gone",
		&SessionRecord{Notch: NotchMacosUser, Workspace: "/ws", Name: "yolo-ws-0001"})

	// Another listing's probe, mid-flight: a read-only descriptor of its own holding LOCK_SH.
	other, err := os.Open(filepath.Join(gone, paths.HostServicesSessionLockName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	sessions, ok := ListSessions(base)
	if !ok || len(sessions) != 1 {
		t.Fatalf("ListSessions = %+v, %v; want the one session", sessions, ok)
	}
	if got := sessions[0].Liveness; got != SessionGone {
		t.Errorf("with another listing's shared probe on its lock, the ended session reads %s; want %s",
			got, SessionGone)
	}
	if mu, _ := MacosUserSessions(base); len(mu) != 1 || mu[0].Liveness != SessionGone {
		t.Errorf("MacosUserSessions = %+v; want the one session, gone", mu)
	}
}

// TestListSessionsReadsTheRecord: the notch, workspace and name come from the session's record;
// a dir with no record, or one that does not parse, lists its notch as unknown with no workspace.
func TestListSessionsReadsTheRecord(t *testing.T) {
	base := t.TempDir()
	withRec := plantSession(t, base, "yolo-a-1", "live",
		&SessionRecord{Notch: NotchMacosUser, Workspace: "/Users/Shared/yolo/a", Name: "yolo-a-1"})
	none := plantSession(t, base, "yolo-b-2", "live", nil)
	garbled := plantSession(t, base, "yolo-c-3", "live", nil)
	if err := os.WriteFile(filepath.Join(garbled, SessionRecordName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, _ := ListSessions(base)
	got := sessionByDir(sessions)
	if s := got[withRec]; s.Notch != NotchMacosUser || s.Workspace != "/Users/Shared/yolo/a" || s.Name != "yolo-a-1" {
		t.Errorf("the recorded session lists as %+v", s)
	}
	for _, d := range []string{none, garbled} {
		if s := got[d]; s.Notch != NotchUnknown || s.Workspace != "" || s.Name != "" {
			t.Errorf("%s, which has no readable record, lists as %+v; want notch %q and no workspace",
				d, s, NotchUnknown)
		}
	}
}

// TestMacosUserSessionsLeavesOutHostNotchSessions: a `yolo host` launch's session says so in its
// record and is not a jail; a session with no record may be either, so it stays.
func TestMacosUserSessionsLeavesOutHostNotchSessions(t *testing.T) {
	base := t.TempDir()
	mu := plantSession(t, base, "yolo-a-1", "live", &SessionRecord{Notch: NotchMacosUser, Workspace: "/a", Name: "yolo-a-1"})
	host := plantSession(t, base, "yolo-b-2", "live", &SessionRecord{Notch: NotchHost, Workspace: "/b", Name: "yolo-b-2"})
	old := plantSession(t, base, "yolo-c-3", "live", nil)

	sessions, ok := MacosUserSessions(base)
	if !ok {
		t.Fatal("MacosUserSessions reported that it could not list")
	}
	got := sessionByDir(sessions)
	if _, found := got[host]; found {
		t.Errorf("the `yolo host` session %s is listed as a macos-user session", host)
	}
	for _, d := range []string{mu, old} {
		if _, found := got[d]; !found {
			t.Errorf("%s is missing from the macos-user sessions %+v", d, sessions)
		}
	}
	// ListSessions itself keeps all three: the staging sweeps protect every notch's.
	if all, _ := ListSessions(base); len(all) != 3 {
		t.Errorf("ListSessions listed %d, want all 3", len(all))
	}
}

// TestListSessionsSkipsASymlinkAtASessionsName: a link is not a session dir, so the listing
// does not follow it to another directory's lock and record.
func TestListSessionsSkipsASymlinkAtASessionsName(t *testing.T) {
	base := t.TempDir()
	target := plantSession(t, t.TempDir(), "yolo-a-1", "live", &SessionRecord{Notch: NotchMacosUser, Name: "yolo-a-1"})
	link := filepath.Join(base, filepath.Base(target))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if sessions, _ := ListSessions(base); len(sessions) != 0 {
		t.Errorf("listed %+v through a symlink", sessions)
	}
}

// TestListSessionsLeavesOutAnotherAccountsSessions: the session base is the machine-wide /tmp, so
// on a Mac with more than one user another account's session dirs sit beside this one's. Such a
// dir is 0700 and its lock cannot be opened, so listed it would read as unknown: a "starting or
// unknown" row in `yolo ps` and a name every staging sweep keeps. The listing reads only the dirs
// this user owns, so a live session of another account, record and all, is not listed, and
// neither is it a macos-user session. The owner seam stands in for the other account, since no
// unprivileged test can make a dir as one.
func TestListSessionsLeavesOutAnotherAccountsSessions(t *testing.T) {
	base := t.TempDir()
	plantSession(t, base, "yolo-theirs-1", "live", &SessionRecord{Notch: NotchMacosUser, Workspace: "/theirs", Name: "yolo-theirs-1"})

	if sessions, ok := ListSessions(base); !ok || len(sessions) != 1 {
		t.Fatalf("as the dir's owner ListSessions = %+v, %v; want the one session (the fixture is wrong otherwise)", sessions, ok)
	}

	prev := sessionOwner
	sessionOwner = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { sessionOwner = prev })
	sessions, ok := ListSessions(base)
	if !ok {
		t.Fatal("ListSessions reported that it could not list")
	}
	if len(sessions) != 0 {
		t.Errorf("listed another account's session dirs: %+v", sessions)
	}
	if mu, _ := MacosUserSessions(base); len(mu) != 0 {
		t.Errorf("MacosUserSessions listed another account's session dirs: %+v", mu)
	}
}

// TestTheSessionOwnerIsThisProcesssEffectiveUID: the seam's production answer, so a test that
// moves it cannot stand in for a filter that compares against anything else.
func TestTheSessionOwnerIsThisProcesssEffectiveUID(t *testing.T) {
	if got := sessionOwner(); got != os.Geteuid() {
		t.Errorf("sessionOwner() = %d, want the effective uid %d", got, os.Geteuid())
	}
}

// TestWriteSessionRecordIsPrivateAndWrittenOnce: the record is 0600 (the sandbox account's grant
// on the dir is search alone, and the record names a host path), and a second write refuses
// rather than replacing the first.
func TestWriteSessionRecordIsPrivateAndWrittenOnce(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSessionRecord(dir, SessionRecord{Notch: NotchHost, Workspace: "/w", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, SessionRecordName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("record mode %v, want 0600", st.Mode().Perm())
	}
	if err := WriteSessionRecord(dir, SessionRecord{Notch: NotchMacosUser}); err == nil {
		t.Error("a second WriteSessionRecord replaced the first")
	}
	if r, ok := readSessionRecord(dir); !ok || r.Notch != NotchHost {
		t.Errorf("the record reads back as %+v (%v)", r, ok)
	}
}

func TestSessionKey(t *testing.T) {
	for base, want := range map[string]string{
		"yolo-host-services-0a1b2c3d-123456": "0a1b2c3d",
		"yolo-host-services-0a1b2c3d":        "",
		"yolo-host-services-XYZ-1":           "",
		"yolo-host-services-0a1b2c3-1":       "",
		"something-else":                     "",
	} {
		if got := sessionKey(base); got != want {
			t.Errorf("sessionKey(%q) = %q, want %q", base, got, want)
		}
	}
}
