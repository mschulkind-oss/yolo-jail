package check

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// brokerprobe_test.go pins where an OLDER yolo's host-wide broker lives, which `yolo check`
// still reports (reportBrokerDaemon, through broker.RunningInstances) so a user can stop it
// once the jails it served are relaunched.

// shortBrokerSocketDir returns a per-test dir short enough to hold a socket.
// t.TempDir() is not: it is rooted at TMPDIR, which on darwin is
// /var/folders/<2>/<26>/T/ before the test name is appended.
func shortBrokerSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(testtmp.ShortDir(), "yj-chk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestCheckSingletonPathsMatchTheDerivedOnes pins the paths an older yolo's host-wide broker
// used, which this package reports through broker.LegacyDeps.
//
// Four things reach the singleton: the run pipeline's front, via
// paths.HostSingletonSocket(name) built from the loophole RECORD; `yolo broker
// {status,stop,restart}`, via the broker.BrokerSingleton* constants; a
// not-yet-upgraded yolo on the same host, via those same constants; and THIS
// PACKAGE, via a hand-copied literal of its own. TestSingletonPathsMatchTheBrokerConstants
// pins the first pair against the second. Nothing pinned the third copy, so
// `/tmp/yolo-claude-oauth-broker.sock` could drift here alone — and the symptom
// would be `yolo check` reporting "daemon not running" about a broker serving every
// jail on the machine, with the remedy it prints (`yolo broker restart`) cycling a
// singleton that was fine.
//
// Measured 2026-08-19: changing both literals in broker.go passes `go test -short
// ./...` in full.
func TestCheckSingletonPathsMatchTheDerivedOnes(t *testing.T) {
	// This package no longer carries a literal of its own: it derives through
	// paths.HostSingleton*, the same function the run pipeline's front and
	// internal/broker use, so the three cannot drift apart by construction. What
	// still has to hold is the production BYTES, because a not-yet-upgraded yolo on
	// the same host hard-codes them. Checked under the production directory, since
	// this package's TestMain gives its own launches a private one.
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = paths.DefaultHostSingletonDir
	t.Cleanup(func() { paths.HostSingletonDir = prev })
	for _, tc := range []struct{ what, derived, want string }{
		{"socket", paths.HostSingletonSocket(brokerLoopholeName), "/tmp/yolo-claude-oauth-broker.sock"},
		{"pid file", paths.HostSingletonPIDFile(brokerLoopholeName), "/tmp/yolo-claude-oauth-broker.pid"},
		{"socket (broker)", broker.BrokerSingletonSocket(), "/tmp/yolo-claude-oauth-broker.sock"},
	} {
		if tc.derived != tc.want {
			t.Errorf("the broker %s is %q in production, want %q — an older yolo on the same "+
				"host would inspect a different file", tc.what, tc.derived, tc.want)
		}
	}
	// And the NAME this package keys on is the one the rest of the tree does, since
	// every derivation above is a function of it and nothing else.
	if brokerLoopholeName != broker.BrokerLoopholeName {
		t.Errorf("check's loophole name is %q, internal/broker's is %q",
			brokerLoopholeName, broker.BrokerLoopholeName)
	}
}

// TestBrokerSocketAcceptsWritesNothing and TestBrokerSocketAcceptsRejectsAStaleFile ARE
// DELETED, with this package's own probe: the broker rows read broker.RunningInstances, whose
// accept probe is broker.SingletonReachable, and broker.TestSingletonReachableWritesNothing and
// its stale-file twin pin that one.
