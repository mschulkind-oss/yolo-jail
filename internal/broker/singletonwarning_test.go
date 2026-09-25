package broker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestFailedSpawnWarningNamesTheLoophole pins the half of the `scope: "host"`
// generalization that has no mechanical consequence and is therefore the half most
// easily left behind: the DIAGNOSTIC.
//
// This package's lifecycle stopped being the broker's alone when
// `host_daemon.scope: "host"` landed — SingletonDeps builds these Deps from any
// loophole's manifest, and its paths, argv and log are all derived from the name.
// reportFailedSpawn was not: it said "the Claude OAuth broker singleton" for every
// caller, and the sentence after it named Claude auth. The broker is the only
// loophole declaring the scope today, so nothing MISBEHAVES — which is exactly why
// a test is the only thing that will notice. The second host-scoped daemon to fail
// its spawn would print a warning naming a different loophole entirely, at the one
// moment its owner is reading the launch output, and send them to
// `yolo broker status` for a daemon that is not the broker.
//
// It asserts through BrokerSpawn rather than calling reportFailedSpawn directly:
// the reporter is unexported and the thing worth pinning is that the failure path
// reaches it with the record's name still attached.
func TestFailedSpawnWarningNamesTheLoophole(t *testing.T) {
	// A daemon that exits at startup without binding — the measured shape (the
	// missing-openssl case), and the branch that produces a warning at all.
	st := &fakeState{spawnPID: 11, spawnExited: true}
	deps := newFakeDeps(t, st)
	deps.Name = "yjtest-other-singleton"
	var buf bytes.Buffer
	deps.Out = &buf
	_ = BrokerSpawn(deps)

	out := buf.String()
	if !strings.Contains(out, "yjtest-other-singleton") {
		t.Errorf("the failed-spawn warning does not name the loophole from the record, so a "+
			"host-scoped daemon that is not the broker reports someone else's failure:\n%s", out)
	}
	if strings.Contains(out, "Claude") {
		t.Errorf("the failed-spawn warning still hardcodes the broker; SingletonDeps builds "+
			"these Deps for any `host_daemon.scope: \"host\"` loophole:\n%s", out)
	}
}

// TestSingletonDepsCarriesTheName is the CALL-SITE half. The warning above can only
// name the loophole if the constructor every production path goes through actually
// puts it on the record — and a Deps whose Name is empty degrades silently to the
// generic phrasing, which is a passing warning and a lost fact.
func TestSingletonDepsCarriesTheName(t *testing.T) {
	if got := SingletonDeps("yjtest-named", nil).Name; got != "yjtest-named" {
		t.Errorf("SingletonDeps(...).Name = %q, want %q — every host-scoped spawn is built "+
			"here, so a dropped name silences the loophole's identity in its own failure "+
			"warning", got, "yjtest-named")
	}
	if got := RealDeps().Name; got != BrokerLoopholeName {
		t.Errorf("RealDeps().Name = %q, want %q", got, BrokerLoopholeName)
	}
}

// TestSpawnSaysWhyItCouldNotTakeTheLock: BrokerSpawn used to RETURN SILENTLY when it could not
// open its lock file — no daemon, no line, and the first symptom a reachability refusal
// naming the socket rather than the lock. docs/design/host-daemon-ownership.md OQ-HD8 names
// the case that makes this more than hypothetical (the singleton's paths carry no user
// component, so a second user on one host cannot take the first one's 0644 lock) and its
// answer keeps the message fix: "the leaning's message fix is still worth doing while the
// singleton ships".
//
// The lock path's PARENT is a regular file here, which fails the open for every uid —
// root included, which is what this suite runs as in a jail and which no permission bit
// can refuse.
func TestSpawnSaysWhyItCouldNotTakeTheLock(t *testing.T) {
	st := &fakeState{spawnPID: 11}
	deps := newFakeDeps(t, st)
	deps.Name = "yjtest-locked-singleton"
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	deps.LockPath = filepath.Join(blocker, "broker.lock")
	var buf bytes.Buffer
	deps.Out = &buf

	if got := BrokerSpawn(deps); got != deps.SocketPath {
		t.Errorf("BrokerSpawn returned %q, want the socket path %q (the contract is unchanged)",
			got, deps.SocketPath)
	}
	if st.spawnArgv != nil {
		t.Errorf("BrokerSpawn spawned %v without holding its lock", st.spawnArgv)
	}
	out := buf.String()
	for _, want := range []string{
		deps.LockPath,             // which file
		"not a directory",         // why, in the OS's words
		"yjtest-locked-singleton", // whose daemon
		"nothing was started",     // what it means
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the lock-file failure does not say %q:\n%s", want, out)
		}
	}
}

// TestLockFailureNamesTheOtherUserOnPermission: the one cause worth naming beyond the OS's
// words is OQ-HD8's, and it is named only when the error IS a permission refusal — a
// "another user owns it" guess under ENOTDIR would send the reader looking for a user who
// does not exist. Driven with a synthesized error because this suite runs as root in a jail,
// where no permission bit refuses anything.
func TestLockFailureNamesTheOtherUserOnPermission(t *testing.T) {
	deps := Deps{Name: "yjtest-shared", LockPath: "/tmp/yolo-yjtest-shared.lock"}
	perm := lockFailureLine(deps, "open", &os.PathError{Op: "open", Path: deps.LockPath, Err: syscall.EACCES})
	if !strings.Contains(perm, "another user") || !strings.Contains(perm, "OQ-HD8") {
		t.Errorf("a permission refusal on the lock does not name the two-users collision:\n%s", perm)
	}
	other := lockFailureLine(deps, "open", &os.PathError{Op: "open", Path: deps.LockPath, Err: syscall.ENOTDIR})
	if strings.Contains(other, "another user") {
		t.Errorf("a non-permission failure guesses at another user:\n%s", other)
	}
}
