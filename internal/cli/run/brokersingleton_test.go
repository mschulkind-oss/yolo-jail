package run

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// TestBrokerEndpointEnvVarDoesNotDrift ties the run pipeline's PRODUCER to the
// in-jail terminator's CONSUMER, across two packages that never reference each
// other at runtime.
//
// Nothing else catches a divergence: the producer writes an env var into a
// container's argv and the consumer reads it inside that container, so a mismatch
// compiles, links, launches, and then exits 2 with "no host broker endpoint
// available" — which reads as a missing loophole, not a typo. That is precisely
// how a drifted name once silently disabled the cgroup delegate in every jail.
func TestBrokerEndpointEnvVarDoesNotDrift(t *testing.T) {
	if got, want := hostServiceEnvVar(broker.BrokerLoopholeName), oauthterminator.BrokerEndpointEnv; got != want {
		t.Errorf("run emits %q; the terminator reads %q", got, want)
	}
	if want := "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT"; oauthterminator.BrokerEndpointEnv != want {
		t.Errorf("BrokerEndpointEnv = %q, want %q", oauthterminator.BrokerEndpointEnv, want)
	}
	// The retired spelling must not be what either side settled on.
	if strings.HasSuffix(oauthterminator.BrokerEndpointEnv, "_SOCKET") {
		t.Error("the broker env var still says _SOCKET; its value is an endpoint file")
	}
}

// brokerFixtureDirs makes a lone broker manifest THIS process's whole pack-module
// record, so the env-emission gate is exercised without depending on this machine's
// real packs.
//
// A PACK MODULE, not a bundled dir. The broker's manifest is a contribution of
// `packs/claude` as of 2026-08-19 (docs/reference/loophole-system.md, "The Claude OAuth broker, a contribution of the claude pack") and the
// bundled channel it used to live in is retired, so a fixture in the old shape would be
// testing a source that no longer exists. Two consequences the fixture has to honor:
// `publishes: "socket"` is REQUIRED of a pack-shipped daemon (the default, "endpoint", is
// refused at load and the loophole silently vanishes), and the module needs an approving
// ORIGIN GATE or brokerLoopholeActive answers false — which is a real gate now, not an
// artifact of the fixture (see TestBrokerEnvSuppressedForAnUnapprovedPack).
func brokerFixtureDirs(t *testing.T, enabled bool) {
	t.Helper()
	brokerFixtureModule(t, enabled, true)
}

// brokerFixtureModule is brokerFixtureDirs with the origin gate as a parameter. A loophole runs
// whenever its pack is selected (default_enabled is refused), so "off" is the pack's absence:
// enabled false records no module at all.
func brokerFixtureModule(t *testing.T, enabled, approved bool) {
	t.Helper()
	dir := t.TempDir()
	lp := filepath.Join(dir, broker.BrokerLoopholeName)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + broker.BrokerLoopholeName + `", "description": "fixture",
	  "version": 1,
	  "transport": "` + loopholes.TransportLoopbackTLS + `", "lifecycle": "spawned",
	  "intercepts": [{"host": "platform.claude.com"}], "broker_ip": "127.0.0.1",
	  "host_daemon": {"cmd": ["yolo", "internal", "daemon", "` + broker.BrokerLoopholeName + `", "--socket", "{socket}"],
	    "publishes": "socket", "detach": true},
	  "jail_daemon": {"cmd": ["yolo-jaild", "oauth-terminator"], "restart": "on-failure"}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModuleResolver(nil)
	modules := []loopholes.PackModule{{Dir: lp, HostExecApproved: approved}}
	if !enabled {
		modules = nil
	}
	loopholes.SetPackModules(modules)
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

// TestBrokerEnvSuppressedForAnUnapprovedPack is the ORIGIN GATE at the broker's own
// predicate, and it is the half the pack move made necessary.
//
// brokerLoopholeActive stopped at Active() while the broker's record was BUNDLED — yolo's
// own manifest, under a name no pack could claim, with no origin to judge. Both halves of
// that reason went in one commit: the manifest is a pack's contribution now and the
// reserved namespace is retired, so the name is claimable and the record has a provenance.
//
// What this predicate switches is not cosmetic — the in-jail TLS terminator, the CA mount,
// the endpoint variable below, and (through run.go) the host singleton spawn. Starting all
// of that on the strength of a pack record whose origin nobody evaluated is exactly the
// crossing the gate exists to govern, and nothing else in the launch path would catch it:
// the loophole is enabled, its requirements are met, and the argv would simply carry a
// promise the front never keeps.
func TestBrokerEnvSuppressedForAnUnapprovedPack(t *testing.T) {
	brokerFixtureModule(t, true, false)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true } // the singleton IS up

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			t.Errorf("the broker endpoint variable was emitted for a pack whose host access "+
				"nobody approved: %v — Active() is not enough now that the record comes from "+
				"a pack, and MayRunHostCode is what says the pack may touch the host", args)
		}
	}
}

// TestBrokerEnvEmittedWhenLoopholeActive: the broker's endpoint env is emitted
// because the LOOPHOLE is active, not because a socket happened to exist.
//
// The old gate stat'd the singleton's socket at assembly time. The container's
// environment is frozen at `podman run`, so a jail launched during the second the
// singleton was restarting got NO broker address for its entire life — the
// terminator exits 2 and Claude Code never starts, and nothing later repairs it.
// PathExists is forced FALSE here: under the old gate that alone suppressed the
// variable.
func TestBrokerEnvEmittedWhenLoopholeActive(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false } // the singleton's socket is absent

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		paths.JailHostServicesDir + "/" + broker.BrokerLoopholeName + paths.ServiceEndpointExt
	if !containsStr(args, want) {
		t.Errorf("broker env not emitted while the loophole is active: %v", args)
	}
	// The value is a PATH, never an address: the port is kernel-assigned and can
	// change under a running container.
	if strings.Contains(want, ":") && !strings.Contains(want, "=/") {
		t.Errorf("emitted value is not an absolute path: %q", want)
	}
}

// TestBrokerEnvSuppressedWhenLoopholeDisabled is the control: without it the test
// above would also pass if the variable were emitted unconditionally.
func TestBrokerEnvSuppressedWhenLoopholeDisabled(t *testing.T) {
	brokerFixtureDirs(t, false)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true } // the singleton IS up

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			t.Errorf("broker env emitted for a disabled loophole: %q", a)
		}
	}
	// The directory mount is unconditional and still there — the loophole gate must
	// govern only the env var.
	if len(args) != 2 || args[0] != "-v" || !strings.HasSuffix(args[1], paths.JailHostServicesDir+":rw") {
		t.Errorf("want exactly the host-services mount, got %v", args)
	}
}

// TestBrokerEnvEmittedInANestedJailWithNoSingleton is the RULING that replaced a
// suppression, and it is deliberately the inverse of the test that stood here.
//
// That test (TestBrokerEnvSuppressedInANestedJailThatHasNoSingleton) pinned
// brokerEndpointIsUnpublishable's nested arm: a launcher inside a jail, with no singleton
// socket after brokerEnsure had already tried, withheld the broker's endpoint variable.
// Both halves of what justified that arm are spent — `openssl` was baked (`431625bc`) and
// then `EnsureCAAndLeaf` stopped needing it at all (`d5bb1e5d`, in-process crypto/x509),
// and MEASURED 2026-09-18 a nested launch minted its OWN P-256 CA in its OWN state
// directory and published /run/yolo-services/claude-oauth-broker.endpoint
// (docs/reference/claude-oauth-interposition.md#a-nested-jail-runs-its-own-broker). `OQ-2` ruled the general form: a
// nested jail runs its own broker singleton like any other host, because nesting earns
// affordances, not exemptions, and a jail that behaves differently cannot test the thing
// it is nested inside.
//
// So a nested launcher is an ORDINARY launcher here, and the optimistic emission the host
// has always had (9b77742, TestBrokerEnvSurvivesAHostWithNoSingleton) is now what a nested
// launch gets too. The conditions are the old test's exactly — inside a jail, no socket —
// with the assertion inverted, so re-adding the arm fails this by name rather than
// silently restoring a behaviour three documents now contradict.
func TestBrokerEnvEmittedInANestedJailWithNoSingleton(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	// The launcher is itself inside a jail (inJail reads YOLO_VERSION), and no
	// singleton socket is present.
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.8.0+255.gdeadbee"
		}
		return ""
	}
	o.PathExists = func(string) bool { return false }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		hostServiceEndpointPath(broker.BrokerLoopholeName)
	if !containsStr(args, want) {
		t.Errorf("a nested launch was not wired to the broker it now runs for itself: %v\n"+
			"OQ-2 ruled that a nested jail runs its own broker singleton, and the arm that "+
			"withheld this was deleted 2026-09-20 with both halves of its justification "+
			"(431625bc baked openssl; d5bb1e5d removed the need; the CA is minted in the "+
			"nested launcher's OWN state dir, measured 2026-09-18)", args)
	}
}

// TestBrokerEnvStillEmittedInANestedJailWithALiveSingleton IS DELETED, with the arm it
// was half the control for. It asserted that a nested launch WITH a live singleton was
// still wired — the other side of a suppression that keyed on "nothing published it".
// Nothing keys on the socket any more, so it and the test above it were asserting one
// code path under two names, and the surviving one (TestBrokerEnvEmittedInANestedJailWith
// NoSingleton) is the harder case: no socket at all.

// TestBrokerEnvSurvivesAHostWithNoSingleton is the other half, and it is the one that
// keeps a documented ruling from being reverted by accident.
//
// On the HOST the variable is emitted whether or not the singleton is up
// (TestBrokerEnvEmittedWhenLoopholeActive is the same fact from the other side, and
// records the restart window it was removed for in 9b77742). §7.3 accepts what that
// now costs under the fatal witness. Widening the nested-launch suppression to every
// launcher would silently undo both.
func TestBrokerEnvSurvivesAHostWithNoSingleton(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	// A host launcher: no YOLO_VERSION, and no singleton socket either.
	o.PathExists = func(string) bool { return false }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		paths.JailHostServicesDir + "/" + broker.BrokerLoopholeName + paths.ServiceEndpointExt
	if !containsStr(args, want) {
		t.Errorf("the host's optimistic emission is load-bearing (9b77742) and must not "+
			"have been narrowed along with the nested one: %v", args)
	}
}

// TestNoBrokerTokenEnvEmitted: no token environment variable exists, at all.
//
// The deletion is pinned rather than assumed. A fallback would keep the
// child-process inheritance problem alive for whatever read it first, which is the
// entire reason the token moved into the endpoint file.
func TestNoBrokerTokenEnvEmitted(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true }
	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	sawBroker := false
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			sawBroker = true
		}
		if strings.Contains(a, "_TOKEN=") {
			t.Errorf("a token env var is emitted: %q", a)
		}
		if regexp.MustCompile(`\b[0-9a-f]{64}\b`).MatchString(a) {
			t.Errorf("a 64-hex run (a token) is emitted: %q", a)
		}
	}
	if !sawBroker {
		t.Fatal("no broker env at all; the assertion above proved nothing")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// sunPathMax is the longest AF_UNIX path that binds on the tighter of the two
// platforms: darwin's sun_path is 104 bytes INCLUDING the NUL, Linux's is 108.
const sunPathMax = 103

// assertSockPathFits fails with the actionable message instead of letting the
// bind return a bare "invalid argument", which names neither the limit nor the
// fix.
func assertSockPathFits(t *testing.T, path string) {
	t.Helper()
	if len(path) > sunPathMax {
		t.Fatalf("socket path is %d bytes, over the %d-byte darwin sun_path limit:\n  %s\n"+
			"use shortSocketDir(t) — t.TempDir() is rooted at TMPDIR, which on macOS is "+
			"/var/folders/<2>/<26>/T/ (~49 bytes) before the test name is even appended",
			len(path), sunPathMax, path)
	}
}

// shortSocketDir returns a per-test directory short enough to hold a socket.
//
// t.TempDir() is NOT, and that is not a theoretical limit: two tests in the
// loopback-TLS batch used it, went green on Linux, and failed only on
// check-macos with "bind: invalid argument". Reproduce on Linux by pointing
// TMPDIR at a long path — the error comes back byte-for-byte identical, which is
// what TestSocketDirIgnoresALongTMPDIR pins.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(testtmp.ShortDir(), "yj-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// TestSocketDirIgnoresALongTMPDIR is the permanent regression test for the
// check-macos break: it reproduces darwin's long TMPDIR on any platform, proves
// the reproduction is real, and then proves the helper is immune to it.
func TestSocketDirIgnoresALongTMPDIR(t *testing.T) {
	long, err := os.MkdirTemp(testtmp.ShortDir(), "yj-tmpdir-"+strings.Repeat("x", 60)+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(long) })
	t.Setenv("TMPDIR", long)
	// Since Go 1.26 t.TempDir() roots at GOTMPDIR when that is set, so the control moves it too.
	t.Setenv("GOTMPDIR", long)

	// The control. Without it this test would still pass if sunPathMax were
	// raised to something no path can exceed, proving nothing.
	if p := filepath.Join(t.TempDir(), "relay.sock"); len(p) <= sunPathMax {
		t.Fatalf("the control did not reproduce the failure — %d bytes is under the "+
			"limit, so the assertion below is vacuous: %s", len(p), p)
	}
	// The fix: unaffected by TMPDIR, and actually bindable.
	p := filepath.Join(shortSocketDir(t), "relay.sock")
	assertSockPathFits(t, p)
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatalf("the short socket dir did not yield a bindable path: %v", err)
	}
	_ = ln.Close()
}

// The predicate itself, in the direction that used to be unreachable: a DISABLED
// broker loophole answers false, so nothing wires the jail to it (the credential view's gate,
// and the assembler's endpoint emission, read the same list).
//
// Driven through the same fixture the env-emission tests use, so the record this reads
// is built the way discovery builds one rather than by hand.
func TestBrokerLoopholeActiveIsFalseWhenDisabled(t *testing.T) {
	brokerFixtureDirs(t, false)
	if brokerLoopholeActive(jsonx.NewOrderedMap()) {
		t.Error("brokerLoopholeActive = true with the broker's pack unselected")
	}
}

// The control, without which the test above would pass on a predicate wired to
// `return false`.
func TestBrokerLoopholeActiveIsTrueWhenEnabled(t *testing.T) {
	brokerFixtureDirs(t, true)
	if !brokerLoopholeActive(jsonx.NewOrderedMap()) {
		t.Error("brokerLoopholeActive = false for an enabled, requirement-free broker record " +
			"— the gate would suppress the singleton on every launch and no jail would ever " +
			"get a serialized refresh")
	}
}

// TestTheTwoBrokerArgvSourcesAgree IS DELETED, with the second source it compared. It tied
// brokerEnsure's hardcoded `broker.RealDeps().Argv` to the shipped manifest's
// `host_daemon.cmd`, because both could spawn the one host-wide broker. HD-R1 deleted
// brokerEnsure and the singleton: the manifest's `cmd`, through startExternalService, is the
// only argv any launch spawns the broker with, and loopholedecl/shipped_test.go pins it.
