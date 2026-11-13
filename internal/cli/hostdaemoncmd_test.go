package cli

import (
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// The management surface OQ-HD2 ruled into existence: `yolo host-daemon` over what the jails
// are running (HD-R1), with `broker` retained as an alias.
//
// Every test here drives dispatchNative, so it fails if the registry entry, the
// argv parse or the resolution is removed — a test that reached the command
// bodies directly would stay green with the whole verb unregistered, which is the
// callee-pinned/call-site-unpinned shape AGENTS.md documents this repo shipping.
//
// NOTHING HERE STARTS A DAEMON. `status` globs sockets and connects and closes; the acting
// verbs are driven only down their refusal paths.

// TestTheBrokerAliasStillResolvesToTheClaudeBroker is the compatibility half of the ruling:
// `yolo broker <verb>` must keep meaning exactly `yolo host-daemon <verb> claude-oauth-broker`,
// because that spelling is in muscle memory, in the release notes and in this tree's own docs.
//
// Since HD-R1 the answer is every jail's running copy, so the fixture is two jails' sockets —
// one the broker's, one another daemon's — and the test compares the two DOCUMENTS: it fails if
// the alias stops naming the broker, and if the general verb names it differently.
//
// THE WHOLE TEST RUNS WITH DISCOVERY EMPTY (withNoPackDiscovery), which is the state the alias
// exists for: in a jail, in any process that resolved no packs, on a host whose `packs` list does
// not name claude. An alias that resolved only through declared loopholes would go dark exactly
// where a user still needs it.
func TestTheBrokerAliasStillResolvesToTheClaudeBroker(t *testing.T) {
	withNoPackDiscovery(t)
	dir, err := os.MkdirTemp(testtmp.ShortDir(), "yj-hd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	saved := paths.FrontSocketDir
	paths.FrontSocketDir = dir
	t.Cleanup(func() { paths.FrontSocketDir = saved })
	for _, n := range []string{broker.BrokerLoopholeName, "aws-auth"} {
		ln, err := net.Listen("unix", paths.FrontSocketFile("aaaaaaaa", n))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
	}

	_, aliasOut, aliasErr := captureDispatchRC(t, []string{"broker", "status", "--format", "json"})
	_, verbOut, _ := captureDispatchRC(t,
		[]string{"host-daemon", "status", broker.BrokerLoopholeName, "--format", "json"})
	var alias []map[string]any
	if err := json.Unmarshal([]byte(aliasOut), &alias); err != nil {
		t.Fatalf("`yolo broker status --format json` is not a JSON array: %v\nstdout: %s\nstderr: %s",
			err, aliasOut, aliasErr)
	}
	if len(alias) != 1 || alias[0]["loophole"] != broker.BrokerLoopholeName || alias[0]["jail"] != "aaaaaaaa" {
		t.Errorf("the alias reports %v, want the one jail's %s copy and nothing else", alias,
			broker.BrokerLoopholeName)
	}
	if aliasOut != verbOut {
		t.Errorf("`yolo broker status` and `yolo host-daemon status %s` describe different daemons:\n"+
			"alias: %s\nverb:  %s", broker.BrokerLoopholeName, aliasOut, verbOut)
	}
}

// RESTART RESTARTS NOTHING (HD-R1): a jail's copy restarts with its jail, so the verb refuses,
// exit 1, and names the relaunch — through the dispatch, so deleting the restart arm fails here.
func TestRestartRefusesAndNamesTheRelaunch(t *testing.T) {
	withNoPackDiscovery(t)
	rc, out, _ := captureDispatchRC(t, []string{"broker", "restart"})
	if rc != 1 {
		t.Errorf("`yolo broker restart` exited %d, want 1 (a refusal)", rc)
	}
	for _, want := range []string{"Nothing to restart", "leave that jail and launch it again"} {
		if !strings.Contains(out, want) {
			t.Errorf("`yolo broker restart` lacks %q:\n%s", want, out)
		}
	}
}

// THE UPGRADE STEP, THROUGH THE DISPATCH: `stop --host-wide <name>` acts on an older yolo's
// host-wide copy alone, so a jail's own copy — here a socket something accepts on — is never
// reached, and with no host-wide copy running the answer says that and nothing about the jail.
func TestStopHostWideIsParsedAndLeavesAJailsOwnCopy(t *testing.T) {
	withNoPackDiscovery(t)
	dir, err := os.MkdirTemp(testtmp.ShortDir(), "yj-hd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	saved := paths.FrontSocketDir
	paths.FrontSocketDir = dir
	t.Cleanup(func() { paths.FrontSocketDir = saved })
	ln, err := net.Listen("unix", paths.FrontSocketFile("aaaaaaaa", broker.BrokerLoopholeName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	for _, argv := range [][]string{
		{"host-daemon", "stop", "--host-wide", broker.BrokerLoopholeName},
		{"host-daemon", "stop", broker.BrokerLoopholeName, "--host-wide"},
		{"broker", "stop", "--host-wide"},
	} {
		rc, out, errOut := captureDispatchRC(t, argv)
		if rc != 0 {
			t.Errorf("%v exited %d:\n%s%s", argv, rc, out, errOut)
		}
		if !strings.Contains(out, "No host-wide "+broker.BrokerLoopholeName+" an older yolo left is running") {
			t.Errorf("%v does not answer for the host-wide copy alone:\n%s%s", argv, out, errOut)
		}
		if strings.Contains(out, "aaaaaaaa") {
			t.Errorf("%v spoke of a jail's own copy:\n%s", argv, out)
		}
	}
}

// TestBareStatusReportsTheSetAndNotOneDaemon pins the BARE-INVOCATION DECISION,
// which the ruling requires be explicit rather than accidental: with no name,
// `status` means the whole host-scoped set, never "the broker".
//
// The JSON shape is the assertion because it is machine-independent: the set may
// legitimately be empty here (discovery is fail-safe-empty in a process that
// staged no packs), and an empty ARRAY is still not an object. A bare status that
// silently meant the Claude singleton would decode as one.
func TestBareStatusReportsTheSetAndNotOneDaemon(t *testing.T) {
	withNoPackDiscovery(t)
	_, out, _ := captureDispatchRC(t, []string{"host-daemon", "status", "--format", "json"})
	var set []map[string]any
	if err := json.Unmarshal([]byte(out), &set); err != nil {
		t.Fatalf("a bare `yolo host-daemon status` must report the SET (a JSON array), got: %v\n%s",
			err, out)
	}
	for _, row := range set {
		if _, ok := row["loophole"]; !ok {
			t.Errorf("a set row does not name its loophole: %v — a report over three daemons "+
				"that loses the names is the defect this verb exists to close", row)
		}
	}
}

// TestActingVerbsRefuseWithoutANameAndSayWhichOnesExist is the other half of that
// decision: `status` defaults to the set because it REPORTS, while stop, restart
// and logs ACT and therefore refuse.
//
// An implicit target across three daemons is how a management surface comes to act
// on the wrong one — the same class as the warning that named `yolo broker
// restart` for a daemon it could not restart.
func TestActingVerbsRefuseWithoutANameAndSayWhichOnesExist(t *testing.T) {
	withNoPackDiscovery(t)
	for _, verb := range []string{"stop", "restart", "logs"} {
		rc, out, errOut := captureDispatchRC(t, []string{"host-daemon", verb})
		if rc == 0 {
			t.Errorf("`yolo host-daemon %s` with no name exited 0 — it acted on something "+
				"nobody named:\n%s", verb, out)
		}
		if !strings.Contains(errOut, "name the host daemon") {
			t.Errorf("`yolo host-daemon %s` with no name does not ask for one:\n%s", verb, errOut)
		}
	}
}

// TestAnUnknownDaemonIsRefusedByName covers the requirement that every failure
// path say which daemon it is talking about — including the path where the name
// is the thing that is wrong.
func TestAnUnknownDaemonIsRefusedByName(t *testing.T) {
	withNoPackDiscovery(t)
	const bogus = "yjtest-no-such-daemon"
	rc, _, errOut := captureDispatchRC(t, []string{"host-daemon", "status", bogus})
	if rc == 0 {
		t.Errorf("`yolo host-daemon status %s` exited 0 for a daemon that does not exist", bogus)
	}
	if !strings.Contains(errOut, bogus) {
		t.Errorf("the refusal does not name the daemon it refused:\n%s", errOut)
	}
}

// withNoPackDiscovery records an EMPTY pack-module set for one test, which is how
// a process says "nothing is discoverable here" — the record is the convergence
// point every host-side discovery surface reads, and PackModules short-circuits on
// its SET FLAG rather than falling through to the lazy store resolver.
//
// Two reasons, and the second is about the package rather than about this file.
// It makes these tests machine-independent: what `yolo host-daemon` finds must not
// depend on which packs the developer running the suite happens to have installed.
// And it keeps them from PRIMING the lazy resolver's process-wide memo — a
// dispatch that resolved the real pack store left `loopholes status` in a sibling
// test executing every pack loophole's doctor_cmd, turning a 0.03s test into a
// 10.7s failure. A test may not change what a later test measures.
func withNoPackDiscovery(t *testing.T) {
	t.Helper()
	// SnapshotPackModules, never ResetPackModules: the snapshot restores the record
	// AND its set flag ("nothing recorded yet" is not an empty record), and leaves
	// the memo alone.
	restore := loopholes.SnapshotPackModules()
	loopholes.SetPackModules(nil)
	t.Cleanup(restore)
}

// TestHostDaemonTargetSkipsFlagValues pins the one parse subtlety: the daemon name
// is the first bare token that is not a flag's VALUE.
//
// `yolo host-daemon logs -n 100 aws-auth` would otherwise resolve to a daemon
// called "100" and report it unknown, with the real name sitting later in the same
// argv — a refusal that names the wrong thing, which is this ruling's own defect in
// miniature.
func TestHostDaemonTargetSkipsFlagValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argv     []string
		wantName string
		wantRest []string
	}{
		{"bare name", []string{"aws-auth"}, "aws-auth", []string{}},
		{"after a value flag", []string{"-n", "100", "aws-auth"}, "aws-auth", []string{"-n", "100"}},
		{"before a value flag", []string{"aws-auth", "-n", "100"}, "aws-auth", []string{"-n", "100"}},
		{"glued value", []string{"-n100", "aws-auth"}, "aws-auth", []string{"-n100"}},
		{"boolean flag", []string{"-f", "aws-auth"}, "aws-auth", []string{"-f"}},
		{"format value", []string{"--format", "json", "aws-auth"}, "aws-auth", []string{"--format", "json"}},
		{"no name at all", []string{"-n", "100"}, "", []string{"-n", "100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rest := hostDaemonTarget(tc.argv)
			if got != tc.wantName {
				t.Errorf("name = %q, want %q", got, tc.wantName)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}
