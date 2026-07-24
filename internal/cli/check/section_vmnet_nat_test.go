package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// vmnetStub configures the Exec seam for the macOS 15 vmnet network probes:
// sw_vers (version), container system logs (gateway allocation), ifconfig (host
// interface addresses), and the forwarding sysctl. An empty string for any
// field makes that probe report Ran=false (unreadable).
type vmnetStub struct {
	productVersion string
	containerLogs  string
	ifconfigOut    string
	forwarding     string
	routeOut       string // `route -n get default` output; "" => Ran=false
}

// runVmnetNetworkSection wires a minimal Options and runs the coordinator
// checkAppleContainerNetwork against the stub.
func runVmnetNetworkSection(t *testing.T, s vmnetStub) string {
	t.Helper()
	var out bytes.Buffer
	o := &Options{
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			key := strings.Join(argv, " ")
			ran := func(s string) ExecResult {
				if s == "" {
					return ExecResult{Ran: false}
				}
				return ExecResult{Ran: true, RC: 0, Stdout: s}
			}
			switch {
			case strings.HasPrefix(key, "sw_vers"):
				if s.productVersion == "" {
					return ExecResult{Ran: false}
				}
				return ran(s.productVersion + "\n")
			case strings.HasPrefix(key, "container system logs"):
				return ran(s.containerLogs)
			case key == "ifconfig":
				return ran(s.ifconfigOut)
			case strings.HasPrefix(key, "sysctl"):
				return ran(s.forwarding)
			case strings.HasPrefix(key, "route -n get default"):
				return ran(s.routeOut)
			}
			return ExecResult{Ran: false}
		},
	}
	fillDefaults(o)
	r := newReporter(&out, false)
	o.checkAppleContainerNetwork(r)
	return out.String()
}

// A realistic vmnet helper log line and a host ifconfig snippet where the
// gateway .1 IS present (addressing agrees).
const goodVmnetLog = `2026-07-24 15:20:12 container-network-vmnet: [NetworkVmnetHelper] allocated attachment [ipv4Address=192.168.64.3/24] [id=default] [ipv4Gateway=192.168.64.1] [hostname=abc]`
const goodIfconfig = `bridge100: flags=8a63<UP,BROADCAST> mtu 1500
	inet 192.168.64.1 netmask 0xffffff00 broadcast 192.168.64.255
	member: vmenet0`

// --- NAT / forwarding variant ---

const routeEn5 = "   route to: default\ndestination: default\n  interface: en5\n"

// TestVmnetNATWarnsOnMacOS15ForwardingOff: addressing agrees (gateway on host)
// but forwarding is off — the NAT variant WARN with its remediation. Both the
// interface (en5, from the route probe) and the subnet (derived from the
// gateway) are grounded, so the command is copy-paste exact and carries NO
// "verify" caveat.
func TestVmnetNATWarnsOnMacOS15ForwardingOff(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: goodVmnetLog, ifconfigOut: goodIfconfig,
		forwarding: "0", routeOut: routeEn5,
	})
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "no outbound internet") {
		t.Errorf("expected the vmnet forwarding WARN, got:\n%s", got)
	}
	if !strings.Contains(got, "net.inet.ip.forwarding=1") ||
		!strings.Contains(got, "pfctl -a 'com.apple/yolo-vmnet-nat'") {
		t.Errorf("expected the NAT remediation commands, got:\n%s", got)
	}
	// Derived, host-specific values — not the en0/literal fallback.
	if !strings.Contains(got, "nat on en5 from 192.168.64.0/24 to any -> (en5)") {
		t.Errorf("expected the derived pfctl command (en5 + gateway /24), got:\n%s", got)
	}
	if strings.Contains(got, "Verify") {
		t.Errorf("a fully-derived command must carry no 'Verify' caveat, got:\n%s", got)
	}
}

// TestVmnetNATFallsBackToLiteralsWhenProbesFail: no route probe and no logged
// gateway → the command uses the documented literals (en0 / 192.168.64.0/24)
// AND both are flagged for the user to verify.
func TestVmnetNATFallsBackToLiteralsWhenProbesFail(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: "no attachments\n", ifconfigOut: goodIfconfig,
		forwarding: "0", routeOut: "", // route probe fails
	})
	if !strings.Contains(got, "nat on en0 from 192.168.64.0/24 to any -> (en0)") {
		t.Errorf("expected the literal fallback command, got:\n%s", got)
	}
	if !strings.Contains(got, "default-route interface") || !strings.Contains(got, "container subnet") {
		t.Errorf("expected verify-both caveat when neither is derived, got:\n%s", got)
	}
}

// TestVmnetNATSilentWhenForwardingOn: healthy — no warning.
func TestVmnetNATSilentWhenForwardingOn(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: goodVmnetLog, ifconfigOut: goodIfconfig, forwarding: "1",
	})
	if strings.Contains(got, "[WARN]") {
		t.Errorf("forwarding on + subnet agree must not warn, got:\n%s", got)
	}
}

// --- Subnet disagreement variant ---

// TestVmnetSubnetDisagreementWarns: the helper hands out gateway 192.168.64.1
// but the host bridge is on a DIFFERENT subnet (192.168.65.1) — the gateway is
// on no host interface, so containers are cut off. Distinct WARN + remedy.
func TestVmnetSubnetDisagreementWarns(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7",
		containerLogs:  goodVmnetLog, // gateway 192.168.64.1
		ifconfigOut: `bridge100: flags=8a63<UP> mtu 1500
	inet 192.168.65.1 netmask 0xffffff00 broadcast 192.168.65.255`,
		forwarding: "0",
	})
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "subnet disagreement") {
		t.Errorf("expected the subnet-disagreement WARN, got:\n%s", got)
	}
	if !strings.Contains(got, "container system stop && container system start") {
		t.Errorf("expected the recreate-network remedy, got:\n%s", got)
	}
	// The disagreement short-circuits the NAT check: the NAT note must NOT appear.
	if strings.Contains(got, "yolo-vmnet-nat") {
		t.Errorf("subnet disagreement must skip the NAT check, got:\n%s", got)
	}
}

// TestVmnetSubnetSilentWhenGatewayPresent: gateway is on a host interface, so no
// disagreement — falls through to the NAT check (forwarding on → silent).
func TestVmnetSubnetSilentWhenGatewayPresent(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: goodVmnetLog, ifconfigOut: goodIfconfig, forwarding: "1",
	})
	if strings.Contains(got, "subnet disagreement") {
		t.Errorf("gateway present must not warn on disagreement, got:\n%s", got)
	}
}

// TestVmnetSubnetSilentWhenNoAllocationLogged: no container ever started, so no
// gateway in the logs — can't judge disagreement, fall through to NAT.
func TestVmnetSubnetSilentWhenNoAllocationLogged(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: "no attachments here\n", ifconfigOut: goodIfconfig, forwarding: "1",
	})
	if strings.Contains(got, "subnet disagreement") {
		t.Errorf("no allocation logged must not warn on disagreement, got:\n%s", got)
	}
}

// --- Shared version gate & fail-open ---

// TestVmnetSkippedOnMacOS26: both limitations are macOS-15-only.
func TestVmnetSkippedOnMacOS26(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "26.0", containerLogs: goodVmnetLog,
		ifconfigOut: `bridge100:\n	inet 192.168.65.1`, forwarding: "0",
	})
	if strings.Contains(got, "[WARN]") {
		t.Errorf("macOS 26 must be skipped entirely, got:\n%s", got)
	}
}

// TestVmnetSkippedWhenVersionUnreadable: fail-open on missing sw_vers.
func TestVmnetSkippedWhenVersionUnreadable(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "", containerLogs: goodVmnetLog, ifconfigOut: goodIfconfig, forwarding: "0",
	})
	if strings.TrimSpace(got) != "" {
		t.Errorf("unreadable version must stay silent, got:\n%s", got)
	}
}

// TestVmnetNATSkippedWhenForwardingUnreadable: version 15, subnet fine, but the
// sysctl read fails — stay silent rather than guess.
func TestVmnetNATSkippedWhenForwardingUnreadable(t *testing.T) {
	got := runVmnetNetworkSection(t, vmnetStub{
		productVersion: "15.7.7", containerLogs: goodVmnetLog, ifconfigOut: goodIfconfig, forwarding: "",
	})
	if strings.TrimSpace(got) != "" {
		t.Errorf("unreadable forwarding must stay silent, got:\n%s", got)
	}
}

// --- unit tests for the parse helpers ---

func TestLastVmnetGateway(t *testing.T) {
	cases := []struct{ in, want string }{
		{goodVmnetLog, "192.168.64.1"},
		{"no gateway here", ""},
		{"[ipv4Gateway=192.168.64.1]\n[ipv4Gateway=192.168.65.1]", "192.168.65.1"}, // last wins
		{"[ipv4Gateway=10.0.0.1] trailing", "10.0.0.1"},
	}
	for _, c := range cases {
		if got := lastVmnetGateway(c.in); got != c.want {
			t.Errorf("lastVmnetGateway(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubnetFromGateway(t *testing.T) {
	cases := []struct {
		gw          string
		wantSubnet  string
		wantDerived bool
	}{
		{"192.168.64.1", "192.168.64.0/24", true},
		{"10.1.2.254", "10.1.2.0/24", true},
		{"", "192.168.64.0/24", false},
		{"not.an.ip.addr", "192.168.64.0/24", false},
		{"192.168.64", "192.168.64.0/24", false},     // too few octets
		{"192.168.64.999", "192.168.64.0/24", false}, // out of range
	}
	for _, c := range cases {
		gotSubnet, gotDerived := subnetFromGateway(c.gw)
		if gotSubnet != c.wantSubnet || gotDerived != c.wantDerived {
			t.Errorf("subnetFromGateway(%q) = (%q, %v), want (%q, %v)",
				c.gw, gotSubnet, gotDerived, c.wantSubnet, c.wantDerived)
		}
	}
}

func TestDefaultRouteInterface(t *testing.T) {
	mk := func(routeOut string, ran bool) *Options {
		return &Options{Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if !ran {
				return ExecResult{Ran: false}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: routeOut}
		}}
	}
	if iface, derived := mk(routeEn5, true).defaultRouteInterface(); iface != "en5" || !derived {
		t.Errorf("derived route = (%q, %v), want (en5, true)", iface, derived)
	}
	// Probe fails → literal fallback, not derived.
	if iface, derived := mk("", false).defaultRouteInterface(); iface != "en0" || derived {
		t.Errorf("failed probe = (%q, %v), want (en0, false)", iface, derived)
	}
	// Ran but no interface line → fallback.
	if iface, derived := mk("destination: default\n", true).defaultRouteInterface(); iface != "en0" || derived {
		t.Errorf("no interface line = (%q, %v), want (en0, false)", iface, derived)
	}
}

func TestHostHasInetAddr(t *testing.T) {
	if !hostHasInetAddr(goodIfconfig, "192.168.64.1") {
		t.Error("expected 192.168.64.1 to be found in goodIfconfig")
	}
	// Boundary: .1 must not match .10 via prefix.
	if hostHasInetAddr("\tinet 192.168.64.10 netmask", "192.168.64.1") {
		t.Error(".1 must not match .10 (field-exact, not prefix)")
	}
	if hostHasInetAddr(goodIfconfig, "192.168.65.1") {
		t.Error("192.168.65.1 is not in goodIfconfig")
	}
}
