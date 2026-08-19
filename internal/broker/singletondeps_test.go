package broker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestSingletonPathsMatchTheBrokerConstants is the CALL-SITE pin that makes the
// generalization safe, and it is the one assertion in this file that would cost a
// jail its credential path if it were missing.
//
// Four things reach the broker singleton by four different routes: the run
// pipeline's front dials paths.HostSingletonSocket(name) built from the loophole
// RECORD, `yolo broker {status,stop,restart}` reads the BrokerSingleton* constants,
// `yolo check` hardcodes the same strings in its own package, and a not-yet-upgraded
// yolo on the same host still uses the constants too. If the name-derived path and
// the constant ever disagree, every one of those keeps working in isolation while
// two brokers run — one per spelling — and the flock that stops a concurrent
// single-use-refresh-token burn is held by neither against the other
// (docs/design/agent-credentials.md §2.5).
//
// The derivation was CHOSEN so this holds byte-for-byte rather than being adapted to
// it: `/tmp/yolo-<name>.sock` for name="claude-oauth-broker" IS
// /tmp/yolo-claude-oauth-broker.sock. That is why the generalization needed no
// migration and no compatibility shim.
func TestSingletonPathsMatchTheBrokerConstants(t *testing.T) {
	for _, tc := range []struct {
		what, derived, constant string
	}{
		{"socket", paths.HostSingletonSocket(BrokerLoopholeName), BrokerSingletonSocket},
		{"pid file", paths.HostSingletonPIDFile(BrokerLoopholeName), BrokerSingletonPIDFile},
		{"lock", paths.HostSingletonLock(BrokerLoopholeName), BrokerSingletonLock},
	} {
		if tc.derived != tc.constant {
			t.Errorf("the %s derived from the loophole name is %q but the constant is %q — "+
				"the run pipeline would front one file while `yolo broker status` and "+
				"`yolo check` inspect another, and two singletons would run",
				tc.what, tc.derived, tc.constant)
		}
	}
}

// TestSingletonDepsUsesTheDerivedPaths: SingletonDeps is name-driven for a loophole
// that is NOT the broker, which is what "vocabulary" rather than "second spelling of
// the broker" means. A hardcoded constant anywhere in it would pass the test above
// and fail here.
func TestSingletonDepsUsesTheDerivedPaths(t *testing.T) {
	deps := SingletonDeps("some-other-loophole", []string{"/bin/d"})
	if deps.SocketPath != "/tmp/yolo-some-other-loophole.sock" {
		t.Errorf("SocketPath = %q", deps.SocketPath)
	}
	if deps.PIDFilePath != "/tmp/yolo-some-other-loophole.pid" {
		t.Errorf("PIDFilePath = %q", deps.PIDFilePath)
	}
	if deps.LockPath != "/tmp/yolo-some-other-loophole.lock" {
		t.Errorf("LockPath = %q", deps.LockPath)
	}
	if !strings.HasSuffix(deps.LogPath, filepath.Join("logs", "host-service-some-other-loophole.log")) {
		t.Errorf("LogPath = %q, want .../logs/host-service-<name>.log", deps.LogPath)
	}
	if !reflect.DeepEqual(deps.Argv, []string{"/bin/d"}) {
		t.Errorf("Argv = %v, want the caller's", deps.Argv)
	}
	// And the broker's own log path is the SAME derivation, not a survivor of the
	// old constant — otherwise a `yolo check` message naming BrokerLogPath() would
	// send the reader to a file the run pipeline never writes.
	if BrokerLogPath() != SingletonLogPath(BrokerLoopholeName) {
		t.Errorf("BrokerLogPath() = %q but SingletonLogPath(%q) = %q",
			BrokerLogPath(), BrokerLoopholeName, SingletonLogPath(BrokerLoopholeName))
	}
}

// TestRealDepsCarriesTheSelfExecBrokerArgv pins the argv at the CALL SITE that
// builds it, because the engine's own tests deliberately spawn a fixture argv.
//
// Two properties, and both have failed before in this repo. argv[0] is the RUNNING
// binary rather than the string "yolo": the jail agent's PATH need not contain
// `yolo`, and resolving it there is how a spawn silently stopped happening once. And
// the tail is the `internal daemon` subcommand form, not the retired standalone
// console-script name that RealPgrepStrays still only recognizes for reaping.
func TestRealDepsCarriesTheSelfExecBrokerArgv(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	want := []string{exe, "internal", "daemon", BrokerLoopholeName, "--socket", BrokerSingletonSocket}
	if got := RealDeps().Argv; !reflect.DeepEqual(got, want) {
		t.Errorf("RealDeps().Argv = %v, want %v", got, want)
	}
}

// TestBrokerSpawnRefusesAnEmptyArgv: a hand-built Deps with no argv reports rather
// than handing exec.Command an empty slice, which indexes argv[0] and panics inside
// a launch.
func TestBrokerSpawnRefusesAnEmptyArgv(t *testing.T) {
	st := &fakeState{spawnPID: 3}
	deps := newFakeDeps(t, st)
	deps.Argv = nil
	var out strings.Builder
	deps.Out = &out
	_ = BrokerSpawn(deps)
	if st.spawnArgv != nil {
		t.Errorf("spawned with no argv: %v", st.spawnArgv)
	}
	if !strings.Contains(out.String(), "no spawn argv") {
		t.Errorf("the refusal is silent: %q", out.String())
	}
}
