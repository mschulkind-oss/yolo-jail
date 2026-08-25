package run

// nestedports_test.go pins the PORT half of the applied-vs-configured network mode,
// the twin of hostloopbackargv_test.go's selector half.
//
// The defect it exists for was fatal, not cosmetic. The publish gate read the
// CONFIGURED mode (`network.mode`, default "bridge") while the selector read the
// APPLIED one, and podman-in-podman is where those two disagree: the launch emitted
// --net=host AND every -p, and a non-empty publish list then appends the DNAT sysctl,
// which the host namespace refuses at `podman create` time —
//
//	Error: ... sysctl net.ipv4.conf.all.route_localnet=1 can't be set since Network
//	Namespace set to host
//
// So a nested jail whose config declared ANY port could not start at all, and a
// nested jail is this repo's own mandated verification loop (AGENTS.md).
//
// Both directions are held here. Dropping the ports on a nested launch is only right
// if a NON-nested bridge launch still gets them, sysctl included — a fix that gated
// too widely would silently take published ports away from every ordinary jail, and
// the goldens would not see it (they declare no ports).

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const routeLocalnetSysctl = "net.ipv4.conf.all.route_localnet=1"

// publishedPortArgs returns the value after each `-p`, i.e. what podman would
// publish. Empty means the launch publishes nothing.
func publishedPortArgs(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-p" {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// hasSysctl reports whether argv carries `--sysctl <spec>`. The flag is checked as a
// PAIR: the bare string appearing anywhere else in the argv would not be the thing
// podman rejects.
func hasSysctl(argv []string, spec string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--sysctl" && argv[i+1] == spec {
			return true
		}
	}
	return false
}

// portsConfig is a config declaring both port keys under the default bridge mode
// (mode left unset, so resolveNetMode answers with o.Network — "bridge" in
// goldenOptions). The nested drop must be driven by the LAUNCH, not by anything the
// user wrote here.
func portsConfig() *jsonx.OrderedMap {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	net := jsonx.NewOrderedMap()
	net.Set("ports", []any{"8000:3000"})
	net.Set("forward_host_ports", []any{"5432:5432"})
	return newConfig("security", sec, "network", net)
}

// portArgv assembles a launch with portsConfig on the given runtime, with the
// nested-container probe answering `nested`, and returns the argv plus everything
// printed. PathExists answers ONLY that probe so the rest of the argv stays the
// deterministic golden fixture.
func portArgv(t *testing.T, rt string, nested bool, mode string) ([]string, string) {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	o := goldenOptions(ws, home)
	o.PathExists = func(p string) bool { return nested && p == "/run/.containerenv" }
	var stdout, stderr bytes.Buffer
	o.Stdout, o.Stderr = &stdout, &stderr

	cfg := portsConfig()
	if mode != "" {
		cfgMap(cfg, "network").Set("mode", mode)
	}

	in := relocationInput(t, rt, ws, nil)
	in.cfg = cfg
	in.agentsPath = ws
	argv := o.assembleRunCmd(in)
	return argv, stdout.String() + stderr.String()
}

// TestNestedPodmanDropsPublishedPorts is the regression for the fatal pairing. All
// four assertions are one launch: the selector says host, no -p survives, the sysctl
// that host networking refuses is absent, and the user is told why their ports went
// away — a silent drop would leave a jail that starts but does not do what the config
// says, which is the failure mode this warning exists to keep out.
func TestNestedPodmanDropsPublishedPorts(t *testing.T) {
	argv, printed := portArgv(t, "podman", true, "")

	if !slices.Contains(argv, "--net=host") {
		t.Fatalf("nested podman must still be forced onto host networking: %v", networkSelectors(argv))
	}
	if got := publishedPortArgs(argv); len(got) != 0 {
		t.Errorf("a nested launch cannot publish ports (it shares this container's netns), got -p %v", got)
	}
	if hasSysctl(argv, routeLocalnetSysctl) {
		t.Errorf("--sysctl %s under --net=host is refused by podman at create time — "+
			"emitting it makes a nested jail with any declared port unlaunchable", routeLocalnetSysctl)
	}
	if _, ok := envValue(argv, "YOLO_PUBLISHED_PORTS"); ok {
		t.Error("YOLO_PUBLISHED_PORTS names ports that were never published")
	}
	if _, ok := envValue(argv, "YOLO_FORWARD_HOST_PORTS"); ok {
		t.Error("YOLO_FORWARD_HOST_PORTS names forwards the launch did not wire")
	}
	if !strings.Contains(printed, "network.ports") ||
		!strings.Contains(printed, "network.forward_host_ports") {
		t.Errorf("the forced drop must name the keys it dropped, printed:\n%s", printed)
	}
}

// TestNestedPodmanWithoutDeclaredPortsIsSilent: the warning is conditional on there
// being something to drop. Every nested launch is forced to host networking, so an
// unconditional line would print on every single one of this repo's own dev launches
// and stop being read.
func TestNestedPodmanWithoutDeclaredPortsIsSilent(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	o := goldenOptions(ws, home)
	o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	var stdout, stderr bytes.Buffer
	o.Stdout, o.Stderr = &stdout, &stderr

	o.assembleRunCmd(relocationInput(t, "podman", ws, nil))
	if got := stdout.String() + stderr.String(); strings.Contains(got, "network.ports") {
		t.Errorf("a nested launch that declared no ports must not warn about them:\n%s", got)
	}
}

// TestNonNestedBridgeKeepsPublishedPorts is the other direction, and the one that
// makes the fix a fix rather than a removal: an ordinary bridge launch still gets its
// -p pairs, the DNAT sysctl that makes a published port reachable from inside, and
// the env the entrypoint reads — with nothing printed at it.
func TestNonNestedBridgeKeepsPublishedPorts(t *testing.T) {
	argv, printed := portArgv(t, "podman", false, "")

	if got := publishedPortArgs(argv); len(got) != 1 || got[0] != "8000:3000" {
		t.Errorf("publish args = %v, want [8000:3000]", got)
	}
	if !hasSysctl(argv, routeLocalnetSysctl) {
		t.Errorf("a bridged launch that publishes a port needs --sysctl %s, argv: %v",
			routeLocalnetSysctl, argv)
	}
	if v, ok := envValue(argv, "YOLO_PUBLISHED_PORTS"); !ok || !strings.Contains(v, "3000/tcp") {
		t.Errorf("YOLO_PUBLISHED_PORTS = %q (present=%v), want the container-side port", v, ok)
	}
	if v, ok := envValue(argv, "YOLO_FORWARD_HOST_PORTS"); !ok || !strings.Contains(v, "5432:5432") {
		t.Errorf("YOLO_FORWARD_HOST_PORTS = %q (present=%v), want the declared forward", v, ok)
	}
	if strings.Contains(printed, "Warning: nested launch") {
		t.Errorf("a non-nested launch drops nothing and must not warn:\n%s", printed)
	}
}

// TestAppleContainerHostModeStillPublishes is the consequence of gating on the
// APPLIED mode rather than the configured one, stated so it is a decision and not an
// accident: appliedNetMode answers "bridge" for Apple Container whatever `network.mode`
// says, because that backend emits no selector and runs its own bridged networking.
// So an unhonored `mode: "host"` no longer takes the ports with it — it is warned
// about (TestAppleContainerWarnsOnExplicitHostNetworking) and otherwise inert.
func TestAppleContainerHostModeStillPublishes(t *testing.T) {
	argv, _ := portArgv(t, "container", false, "host")
	if got := publishedPortArgs(argv); len(got) != 1 || got[0] != "8000:3000" {
		t.Errorf("Apple Container runs bridged whatever the key says, so -p must survive an "+
			"unhonored network.mode: got %v", got)
	}
}
