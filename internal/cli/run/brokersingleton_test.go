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

// brokerFixtureDirs points loophole discovery at a bundled dir holding ONLY a
// broker manifest, so the env-emission gate is exercised without depending on the
// real bundled set or on `claude` being installed.
func brokerFixtureDirs(t *testing.T, enabled bool) {
	t.Helper()
	dir := t.TempDir()
	lp := filepath.Join(dir, broker.BrokerLoopholeName)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + broker.BrokerLoopholeName + `", "description": "fixture",
	  "version": 1, "default_enabled": ` + map[bool]string{true: "true", false: "false"}[enabled] + `,
	  "transport": "` + loopholes.TransportLoopbackTLS + `", "lifecycle": "spawned",
	  "intercepts": [{"host": "platform.claude.com"}], "broker_ip": "127.0.0.1",
	  "host_daemon": {"cmd": ["yolo", "internal", "daemon", "` + broker.BrokerLoopholeName + `", "--socket", "{socket}"]},
	  "jail_daemon": {"cmd": ["yolo-jaild", "oauth-terminator"], "restart": "on-failure"}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	origB := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = origB })
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

// TestBrokerEnvSuppressedInANestedJailThatHasNoSingleton is the regression for the
// measurement that the fatal witness broke this repo's own development loop.
//
// MEASURED 2026-08-18, from inside this repo's jail, with a freshly built launcher:
// `yolo -- bash` REFUSED TO START. The chain has four links and every one of them is
// working as designed on its own — the broker endpoint variable is wired on loophole
// activity alone with no publish gate (hostServicesMountArgs, deliberately); a
// nested launcher's broker singleton never binds, because the jail image bakes no
// openssl and the daemon exits with `cannot locate openssl` the instant brokerEnsure
// spawns it; nothing therefore ever writes the endpoint file the variable names; and
// the witness reads that as faultUnpublished under disposition `shared`, both of
// which escalate (OQ-R4, OQ-R5). The result is a jail that cannot start, on the one
// launch shape AGENTS.md makes mandatory for verifying a Go change.
//
// THE HOST CASE IS NOT WHAT THIS TESTS AND MUST NOT MOVE. "Broker configured,
// singleton down" refusing a host's jails is an accepted, documented consequence
// (loopback-tls-reachability.md §7.3) — the control below holds it in place.
func TestBrokerEnvSuppressedInANestedJailThatHasNoSingleton(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	// The launcher is itself inside a jail (inJail reads YOLO_VERSION), and
	// brokerEnsure has already run and left no singleton socket behind.
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.8.0+255.gdeadbee"
		}
		return ""
	}
	o.PathExists = func(string) bool { return false }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			t.Errorf("a nested launch promised an endpoint nothing on this side can publish, "+
				"which the fatal witness turns into a refused launch: %q", a)
		}
	}
	// The mount is unconditional; only the promise is withheld.
	if len(args) != 2 || args[0] != "-v" {
		t.Errorf("want exactly the host-services mount, got %v", args)
	}
}

// TestBrokerEnvStillEmittedInANestedJailWithALiveSingleton is half the control: the
// suppression above keys on "nothing published it", not on nesting. A jail whose
// image does carry openssl runs its own singleton, so a nested launch there is an
// ordinary launch and the variable is owed.
func TestBrokerEnvStillEmittedInANestedJailWithALiveSingleton(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.8.0+255.gdeadbee"
		}
		return ""
	}
	o.PathExists = func(p string) bool { return p == broker.BrokerSingletonSocket }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		paths.JailHostServicesDir + "/" + broker.BrokerLoopholeName + paths.ServiceEndpointExt
	if !containsStr(args, want) {
		t.Errorf("a nested launch with a live singleton must still be wired: %v", args)
	}
}

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
	d, err := os.MkdirTemp("/tmp", "yj-run-")
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
	long := filepath.Join("/tmp", "yj-tmpdir-"+strings.Repeat("x", 60))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(long) })
	t.Setenv("TMPDIR", long)

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

// TestBrokerLifecycleIsGatedOnTheLoopholeRecord is OQ-A11 at the CALL SITE that
// decides whether the host singleton runs at all.
//
// # Why this is a source assertion and not a behavioural one
//
// `brokerEnsure` spawns a detached host process under a flock; it is not injectable,
// and driving `runNormal` needs a container. What CAN be driven is the predicate —
// the two tests below do that — and what a predicate's own tests cannot see is the
// call going missing, which is exactly the state this ruling found:
// `brokerLoopholeActive` already existed and was already correct, and the run
// pipeline called `brokerEnsure()` beside it with no lookup at all. So the host
// singleton ran for EVERYBODY, including a user with `packs: []` who has never heard
// of claude, while the jail was wired to it only when the loophole was Active — both
// halves failing, in opposite directions (loophole-activation.md §1.1, §1.3's first
// table row).
//
// # The property, stated so a reader knows what may be edited
//
// ONE predicate governs the spawn AND the wiring. `hostServicesMountArgs` already
// consults `brokerLoopholeActive`; the lifecycle site must consult the same function
// rather than a second spelling of it. A jail that is not given the broker's address
// must not leave a broker running on the host either — which is a stronger
// requirement than "it is gated", because two gates that agree today are two gates
// that can disagree tomorrow.
//
// THE ATTACH HALF OF THIS TEST IS GONE with the per-jail relay it guarded. An attach
// used to re-ensure a relay behind the same gate; there is no per-jail broker process
// left to heal, and the front that replaced it belongs to the yolo process that
// launched the jail. What survives is the launch gate, which is the one that governs
// a host daemon.
func TestBrokerLifecycleIsGatedOnTheLoopholeRecord(t *testing.T) {
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	// The LAUNCH path: the ensure sits behind the gate.
	if !strings.Contains(src, `rt != "container" && brokerLoopholeActive(cfg)`) {
		t.Error("the launch path no longer gates the broker singleton on " +
			"brokerLoopholeActive(cfg). Ungated, yolo spawns a host daemon on every launch " +
			"for every user — including one with `packs: []` — while the jail is wired to it " +
			"only when the loophole is Active (OQ-A11)")
	}
	if !strings.Contains(src, "o.brokerEnsure()") {
		t.Error("run.go no longer ensures the broker singleton before assembling the argv. " +
			"brokerEndpointIsUnpublishable reads the singleton socket to decide whether the " +
			"argv may promise the jail an endpoint at all, so the ensure has to happen while " +
			"the argv is still being written — startHostSingleton is too late for that " +
			"decision even though it ensures the same daemon")
	}
	// And the services DIR is created either way: it holds every loophole's endpoint
	// file and the assembler mounts it unconditionally, so folding it into the gate
	// would make the mount name a directory that does not exist whenever the broker is
	// off. Asserted because it is the exact mistake the narrowing invites.
	if strings.Count(src, "mkdirHostServicesDir(socketsDir)") < 2 {
		t.Error("mkdirHostServicesDir is inside the broker gate. That directory is not the " +
			"broker's — every loophole publishes its endpoint file there and the assembler " +
			"mounts it on every launch — so a jail with the broker off would mount a path " +
			"that does not exist")
	}
	// AND NOTHING SPAWNS A RELAY. The deletion is asserted rather than assumed: a
	// resurrected per-jail relay would splice the same endpoint file the front now
	// publishes, and the loser of that race is the jail's credential path.
	if strings.Contains(src, "ensureBrokerRelay") || strings.Contains(src, "relayReapOrphans") {
		t.Error("run.go references the per-jail broker relay, which is deleted " +
			"(docs/design/broker-as-a-pack.md §7)")
	}
}

// The predicate itself, in the direction that used to be unreachable: a DISABLED
// broker loophole answers false, so the gate above suppresses the spawn.
//
// Driven through the same fixture the env-emission tests use, so the record this reads
// is built the way discovery builds one rather than by hand.
func TestBrokerLoopholeActiveIsFalseWhenDisabled(t *testing.T) {
	brokerFixtureDirs(t, false)
	if brokerLoopholeActive(jsonx.NewOrderedMap()) {
		t.Error("brokerLoopholeActive = true for a manifest declaring default_enabled: false")
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
