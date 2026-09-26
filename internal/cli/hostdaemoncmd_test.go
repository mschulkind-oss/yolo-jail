package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// The management surface OQ-HD2 ruled into existence: `yolo host-daemon` over the
// host-scoped set, with `broker` retained as an alias.
//
// Every test here drives dispatchNative, so it fails if the registry entry, the
// argv parse or the resolution is removed — a test that reached the command
// bodies directly would stay green with the whole verb unregistered, which is the
// callee-pinned/call-site-unpinned shape AGENTS.md documents this repo shipping.
//
// NOTHING HERE STARTS A DAEMON. `status` reads a PID file, stats a socket and
// (only if that socket exists) connects and closes; the acting verbs are driven
// only down their refusal paths.

// TestTheBrokerAliasStillResolvesToTheClaudeSingleton is the compatibility half of
// the ruling: `yolo broker <verb>` must keep meaning exactly
// `yolo host-daemon <verb> claude-oauth-broker`, because that spelling is in
// muscle memory, in the release notes, in `yolo check`'s remedies and in this
// tree's own docs.
//
// It compares the two DOCUMENTS rather than asserting a constant, so it fails
// both ways: if the alias stops resolving to the Claude singleton, and if the
// general verb resolves that name to something else.
func TestTheBrokerAliasStillResolvesToTheClaudeSingleton(t *testing.T) {
	withNoPackDiscovery(t)
	_, aliasOut, _ := captureDispatchRC(t, []string{"broker", "status", "--format", "json"})
	_, verbOut, _ := captureDispatchRC(t,
		[]string{"host-daemon", "status", broker.BrokerLoopholeName, "--format", "json"})

	var alias, verb map[string]any
	if err := json.Unmarshal([]byte(aliasOut), &alias); err != nil {
		t.Fatalf("`yolo broker status --format json` is not one JSON object: %v\n%s", err, aliasOut)
	}
	if err := json.Unmarshal([]byte(verbOut), &verb); err != nil {
		t.Fatalf("`yolo host-daemon status <name> --format json` is not one JSON object: %v\n%s",
			err, verbOut)
	}
	if alias["socket"] != broker.BrokerSingletonSocket() {
		t.Errorf("the alias reports socket %v, want the Claude singleton's %s — "+
			"`yolo broker` must resolve to the broker whatever else is running",
			alias["socket"], broker.BrokerSingletonSocket())
	}
	if alias["pid_file"] != broker.BrokerSingletonPIDFile() {
		t.Errorf("the alias reports pid_file %v, want %s", alias["pid_file"], broker.BrokerSingletonPIDFile())
	}
	if !reflect.DeepEqual(alias, verb) {
		t.Errorf("`yolo broker status` and `yolo host-daemon status %s` describe different daemons:\n"+
			"alias: %v\nverb:  %v", broker.BrokerLoopholeName, alias, verb)
	}

	// THE WHOLE TEST RUNS WITH DISCOVERY EMPTY (withNoPackDiscovery), which is the
	// state the alias exists for: in a jail, in any process that resolved no
	// packs, on a host whose `packs` list does not name claude. The daemon is
	// still at a path that has not moved, so an alias that resolved only through
	// the derived set would go dark exactly where a user still needs it.
	_, darkOut, darkErr := captureDispatchRC(t, []string{"broker", "status", "--format", "json"})
	var dark map[string]any
	if err := json.Unmarshal([]byte(darkOut), &dark); err != nil {
		t.Fatalf("with discovery empty, `yolo broker status` no longer reports the broker: "+
			"%v\nstdout: %s\nstderr: %s", err, darkOut, darkErr)
	}
	if dark["socket"] != broker.BrokerSingletonSocket() {
		t.Errorf("with discovery empty the alias reports socket %v, want %s",
			dark["socket"], broker.BrokerSingletonSocket())
	}
	// AND IT RESOLVES TO A RESTARTABLE RECORD. Reporting is the easy half: every
	// path is a function of the name, so even a record derived from a stray PID
	// file reports the right socket. The argv is the half that can go missing, and
	// `yolo broker restart` is the command the incompatible-daemon warning names.
	// MEASURED in this repo's own jail: /tmp/yolo-claude-oauth-broker.pid exists
	// with no claude pack resolved in the test process, so the set holds a
	// rendezvous-only member and an alias that took it verbatim would refuse to
	// restart the broker on the machine the broker is running on.
	s, ok := resolveHostDaemon("broker", broker.BrokerLoopholeName)
	if !ok {
		t.Fatal("`yolo broker` resolves to nothing with discovery empty")
	}
	if len(s.Argv) == 0 {
		t.Errorf("the alias resolves to %+v — no spawn argv, so `yolo broker restart` "+
			"refuses instead of cycling the daemon", s)
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
		if !strings.Contains(errOut, "name the host-wide daemon") {
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
