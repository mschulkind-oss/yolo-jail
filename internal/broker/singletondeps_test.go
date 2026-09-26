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
// generalization safe.
//
// Four things reach the broker singleton by four different routes: the run
// pipeline's front dials paths.HostSingletonSocket(name) built from the loophole
// RECORD (and BrokerSpawn flocks paths.HostSingletonLock(name)), `yolo broker
// {status,stop,restart}` reads the BrokerSingleton* constants, `yolo check`
// hardcodes the same strings in its own package, and a not-yet-upgraded yolo on
// the same host still uses the constants too.
//
// WHAT A DISAGREEMENT WOULD ACTUALLY COST is operability, and stating it
// precisely matters because this comment used to claim something stronger and
// FALSE. Every route would keep working in isolation while addressing a DIFFERENT
// daemon: `yolo broker stop` reaps a PID file the jails' daemon never wrote and
// leaves the live one running, `status` and `yolo check` report on a socket no
// jail dials, and the SPAWN flock is itself per-spelling — it is
// HostSingletonLock(name) — so neither process excludes the other and both spawn.
//
// WHAT IT WOULD NOT COST is a concurrent single-use-refresh-token burn. The
// refresh flock is not keyed by the socket name at all: oauthbroker's
// RefreshLockPath is set to `BrokerDir()/refresh.lock`, and BrokerDir() takes no
// name and no socket path — it is a function of $HOME alone. So two brokers in
// one home contend on the SAME inode however they are spelled (as they do on the
// cert mint's flock beside it), and DoRefresh re-reads the creds file inside that
// lock and hands back the winner's token as a cache hit. Two brokers in one $HOME
// are a stray process and a confused operator, not a lost credential.
//
// The derivation was CHOSEN so this holds byte-for-byte rather than being adapted to
// it: `/tmp/yolo-<name>.sock` for name="claude-oauth-broker" IS
// /tmp/yolo-claude-oauth-broker.sock. That is why the generalization needed no
// migration and no compatibility shim.
func TestSingletonPathsMatchTheBrokerConstants(t *testing.T) {
	// The Broker* paths are derived from paths.HostSingleton* now, so the two cannot
	// disagree; what this pins is that the derivation still yields the retired
	// literals byte for byte in production, which is what a not-yet-upgraded yolo on
	// the same host reaches. Checked under the production directory, since this
	// package's TestMain gives its own launches a private one.
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = paths.DefaultHostSingletonDir
	t.Cleanup(func() { paths.HostSingletonDir = prev })
	for _, tc := range []struct{ what, got, want string }{
		{"socket", BrokerSingletonSocket(), "/tmp/yolo-claude-oauth-broker.sock"},
		{"pid file", BrokerSingletonPIDFile(), "/tmp/yolo-claude-oauth-broker.pid"},
		{"lock", BrokerSingletonLock(), "/tmp/yolo-claude-oauth-broker.lock"},
		{"lock glob", paths.HostSingletonGlob(), "/tmp/yolo-*.lock"},
	} {
		if tc.got != tc.want {
			t.Errorf("the broker %s is %q in production, want %q — the run pipeline, "+
				"`yolo broker status` and an older yolo on this host must reach one file",
				tc.what, tc.got, tc.want)
		}
	}
}

// TestSingletonDepsUsesTheDerivedPaths: SingletonDeps is name-driven for a loophole
// that is NOT the broker, which is what "vocabulary" rather than "second spelling of
// the broker" means. A hardcoded constant anywhere in it would pass the test above
// and fail here.
func TestSingletonDepsUsesTheDerivedPaths(t *testing.T) {
	deps := SingletonDeps("some-other-loophole", []string{"/bin/d"})
	// Under this package's private paths.HostSingletonDir (TestMain), not /tmp: the
	// derivation must follow the directory, which is what lets a test redirect reach
	// every singleton path at once.
	dir := paths.HostSingletonDir
	if deps.SocketPath != dir+"/yolo-some-other-loophole.sock" {
		t.Errorf("SocketPath = %q", deps.SocketPath)
	}
	if deps.PIDFilePath != dir+"/yolo-some-other-loophole.pid" {
		t.Errorf("PIDFilePath = %q", deps.PIDFilePath)
	}
	if deps.LockPath != dir+"/yolo-some-other-loophole.lock" {
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
	want := []string{exe, "internal", "daemon", BrokerLoopholeName, "--socket", BrokerSingletonSocket()}
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
