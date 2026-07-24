package check

import (
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// sectionMacOSPlatform runs the macOS Platform block (only runs on macOS).
// config is loaded lazily for the podman-machine-resources sub-check via
// load_config(strict=False) inside _check_podman_machine_resources.
func (o *Options) sectionMacOSPlatform(r *reporter, _ *jsonx.OrderedMap) {
	r.section("macOS Platform")
	r.ok("Architecture: " + o.Machine)

	if _, ok := o.LookPath("podman"); ok {
		res := o.Exec([]string{"podman", "machine", "info"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			r.ok("Podman Machine: available")
			o.checkPodmanMachineResources(r, o.loadWorkspaceConfigLoose())
		} else if !res.Ran || res.Timeout {
			r.warn("podman: probe failed", "")
		} else {
			r.warn("Podman Machine: not configured", "")
		}
	}

	_, hasContainer := o.LookPath("container")
	if hasContainer {
		res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			r.ok("Apple Container CLI: available")
			if strings.Contains(strings.ToLower(res.Stdout), "running") {
				r.ok("Apple Container system: running")
				o.checkAppleContainerNetwork(r)
			} else {
				r.warn("Apple Container system not running", "Start with: container system start")
			}
		} else if !res.Ran || res.Timeout {
			r.warn("Apple Container CLI: probe failed", "")
		} else {
			r.warn("Apple Container: installed but not started", "Start with: container system start")
		}
	}

	if hasContainer {
		if _, ok := o.LookPath("skopeo"); ok {
			r.ok("skopeo: available (OCI image conversion, no daemon needed)")
		} else if _, ok := o.LookPath("podman"); ok {
			r.ok("OCI conversion: via podman (skopeo recommended: brew install skopeo)")
		} else {
			r.warn("No OCI conversion tool for Apple Container",
				"Install skopeo (recommended): brew install skopeo")
		}
	}

	// Nix store volume check.
	if o.PathExists("/nix") {
		res := o.Exec([]string{"mount"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout {
			var nixLines []string
			for _, line := range strings.Split(res.Stdout, "\n") {
				if strings.Contains(line, " /nix ") || strings.Contains(line, " on /nix") {
					nixLines = append(nixLines, line)
				}
			}
			if len(nixLines) > 0 {
				if strings.Contains(strings.ToLower(nixLines[0]), "apfs") {
					r.ok("Nix store: mounted (APFS volume)")
				} else {
					r.ok("Nix store: mounted")
				}
			} else {
				r.warn("Nix store: /nix exists but mount not detected",
					"Check /etc/synthetic.conf and Disk Utility")
			}
		} else {
			r.ok("Nix store: /nix exists")
		}
	} else {
		r.fail("Nix store: /nix not found", "Reinstall Nix or check /etc/synthetic.conf")
	}
	r.blank()
}

// checkAppleContainerNetwork runs the macOS 15 vmnet health probes. The
// framework has two distinct outbound-internet failure modes on Darwin 24.x
// (macOS 15) — both gated to major version 15 here (macOS 26 fixes vmnet):
//
//  1. Subnet disagreement (checkAppleContainerVmnetSubnet): the network helper
//     and vmnet pick different subnets, so the gateway the helper hands to
//     containers isn't on any host bridge — the container is "completely cut
//     off" and can't even reach the gateway. This is an L2 break.
//  2. NAT/forwarding missing (checkAppleContainerVmnetNAT): addressing is fine
//     (the container reaches 192.168.64.1) but vmnet never installs the
//     outbound NAT, so there's no internet beyond the gateway.
//
// The two need DIFFERENT remedies (recreate the network vs. add host NAT), so
// they are separate checks. Subnet runs first: a disagreement makes the NAT tell
// meaningless (nothing is reachable regardless of forwarding), so when it fires
// the NAT check is skipped to avoid a misleading second WARN.
func (o *Options) checkAppleContainerNetwork(r *reporter) {
	major, ok := o.macOSMajorVersion()
	if !ok || major != 15 {
		return // Not macOS 15 (or version unreadable) — both limitations are 15-only.
	}
	// The vmnet gateway the helper hands to containers, read once and shared: the
	// subnet check compares it against host interfaces, and the NAT check derives
	// the container /24 from it. "" when no container has ever started.
	gw := o.observedVmnetGateway()
	if o.checkAppleContainerVmnetSubnet(r, gw) {
		return // Disagreement found — the NAT tell would only add noise.
	}
	o.checkAppleContainerVmnetNAT(r, gw)
}

// observedVmnetGateway returns the most recent gateway the vmnet helper handed
// to a container (from `container system logs`), or "" when unreadable or no
// container has ever started.
func (o *Options) observedVmnetGateway() string {
	logs := o.Exec([]string{"container", "system", "logs"}, "", nil, 5*time.Second)
	if !logs.Ran || logs.Timeout {
		return ""
	}
	return lastVmnetGateway(logs.Stdout)
}

// checkAppleContainerVmnetSubnet detects the macOS 15 subnet-disagreement
// variant: the vmnet network helper allocated a container gateway (gw, from the
// logs) that lives on NO host interface, meaning the helper and vmnet disagree
// on the subnet and containers are cut off entirely. Confirms gw against the
// host's interfaces via `ifconfig`. Returns true when it emitted a disagreement
// WARN (so the caller skips the NAT check). Fail-open: a blank gw or unreadable
// ifconfig returns false (stay silent).
func (o *Options) checkAppleContainerVmnetSubnet(r *reporter, gw string) bool {
	if gw == "" {
		return false // No allocation logged yet (no container has ever started).
	}
	ifc := o.Exec([]string{"ifconfig"}, "", nil, 5*time.Second)
	if !ifc.Ran || ifc.Timeout {
		return false
	}
	if hostHasInetAddr(ifc.Stdout, gw) {
		return false // Gateway is on a host interface — addressing agrees.
	}
	r.warn("Apple Container on macOS 15: vmnet subnet disagreement — containers are cut off",
		"The network helper hands containers gateway "+gw+", but no host "+
			"interface owns that address, so the helper and vmnet disagree on the "+
			"subnet (a jail can't even reach its gateway).  Recreate the network "+
			"coherently:\n"+
			"  container system stop && container system start\n"+
			"If it recurs, pin the CIDR in ~/.config/container/config.toml "+
			"([network] subnet = \"192.168.64.1/24\").  See docs/guides/macos.md.")
	return true
}

// checkAppleContainerVmnetNAT detects the macOS 15 vmnet outbound-internet
// limitation: on Darwin 24.x (macOS 15) the vmnet framework fails to install
// the outbound NAT for the container subnet, so containers can reach the bridge
// gateway (192.168.64.1) but have no internet. The tell we can read cheaply,
// without sudo or starting a container, is `net.inet.ip.forwarding` — observed
// 0 in the broken state and 1 once the host-side NAT workaround is applied.
// Caller (checkAppleContainerNetwork) already gated on macOS 15 and a sound
// subnet. A WARN (not FAIL): the heuristic is a strong correlate, not a proof,
// and a missing/unreadable sysctl must never block a run.
//
// The printed pfctl command is derived from THIS host: the egress interface
// from the default route and the container /24 from the observed vmnet gateway
// (gw, "" when no allocation was logged). Each derivation falls back to a
// documented literal (en0 / 192.168.64.0/24) with a "replace" note when its
// probe fails, so the command is copy-pasteable when we can ground it and still
// correct-with-a-caveat when we can't.
func (o *Options) checkAppleContainerVmnetNAT(r *reporter, gw string) {
	res := o.Exec([]string{"sysctl", "-n", "net.inet.ip.forwarding"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return // Can't read the tell; stay silent rather than guess.
	}
	if strings.TrimSpace(res.Stdout) == "1" {
		return // Forwarding on — the workaround (or something) is in place.
	}

	iface, ifaceDerived := o.defaultRouteInterface()
	subnet, subnetDerived := subnetFromGateway(gw)

	note := "This is the macOS 15 vmnet limitation: vmnet doesn't NAT the container " +
		"subnet, so a jail can ping " + gatewayForNote(gw) + " but not the internet.  " +
		"Apply the host-side workaround (reverts on reboot):\n" +
		"  sudo sysctl -w net.inet.ip.forwarding=1\n" +
		"  echo 'nat on " + iface + " from " + subnet + " to any -> (" + iface + ")' | " +
		"sudo pfctl -a 'com.apple/yolo-vmnet-nat' -f -\n"
	// Only nudge the user to substitute the value(s) we could NOT ground on this
	// host — a fully-derived command needs no caveat.
	switch {
	case !ifaceDerived && !subnetDerived:
		note += "Verify " + iface + " is your default-route interface (route -n get default) " +
			"and " + subnet + " is the container subnet.  "
	case !ifaceDerived:
		note += "Verify " + iface + " is your default-route interface (route -n get default).  "
	case !subnetDerived:
		note += "Verify " + subnet + " is the container subnet.  "
	}
	note += "See docs/guides/macos.md."

	r.warn("Apple Container on macOS 15: IP forwarding is off — containers likely have no outbound internet", note)
}

// defaultRouteInterface returns the host's default-route egress interface from
// `route -n get default` (e.g. "en0"), and whether it was derived. On any
// probe/parse failure it returns ("en0", false) — the documented literal, so
// the caller flags it for the user to verify.
func (o *Options) defaultRouteInterface() (string, bool) {
	res := o.Exec([]string{"route", "-n", "get", "default"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return "en0", false
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(line)
		// The line is "  interface: en0".
		if len(f) == 2 && f[0] == "interface:" {
			return f[1], true
		}
	}
	return "en0", false
}

// subnetFromGateway turns a gateway address ("192.168.64.1") into its /24 CIDR
// ("192.168.64.0/24"), and whether it was derived. A blank/malformed gateway
// falls back to ("192.168.64.0/24", false) — Apple Container's documented
// default, flagged for the user to verify.
func subnetFromGateway(gw string) (string, bool) {
	const fallback = "192.168.64.0/24"
	octets := strings.Split(strings.TrimSpace(gw), ".")
	if len(octets) != 4 {
		return fallback, false
	}
	for _, oct := range octets {
		if n, err := strconv.Atoi(oct); err != nil || n < 0 || n > 255 {
			return fallback, false
		}
	}
	return octets[0] + "." + octets[1] + "." + octets[2] + ".0/24", true
}

// gatewayForNote returns the gateway to name in prose, or the documented default
// when none was observed.
func gatewayForNote(gw string) string {
	if strings.TrimSpace(gw) == "" {
		return "192.168.64.1"
	}
	return gw
}

// lastVmnetGateway extracts the most recent `ipv4Gateway=<addr>` value from the
// vmnet helper log stream, or "" if none is present. The helper logs one line
// per attachment; the last is the current state.
func lastVmnetGateway(logs string) string {
	const key = "ipv4Gateway="
	gw := ""
	for _, line := range strings.Split(logs, "\n") {
		idx := strings.Index(line, key)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(key):]
		// The value ends at the next space or ']' (log fields are bracketed).
		end := strings.IndexAny(rest, " ]")
		if end < 0 {
			end = len(rest)
		}
		if v := strings.TrimSpace(rest[:end]); v != "" {
			gw = v
		}
	}
	return gw
}

// hostHasInetAddr reports whether ifconfig output assigns addr to any interface.
// Matches "inet <addr> " / "inet <addr>\t" (trailing boundary so 192.168.64.1
// doesn't match 192.168.64.10).
func hostHasInetAddr(ifconfigOut, addr string) bool {
	for _, line := range strings.Split(ifconfigOut, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "inet" && f[i+1] == addr {
				return true
			}
		}
	}
	return false
}

// macOSMajorVersion returns the integer major version from `sw_vers
// -productVersion` (e.g. 15 from "15.7.7"), or (0, false) when it can't be read
// or parsed. Uses the injectable Exec seam so the version gate is unit-testable.
func (o *Options) macOSMajorVersion() (int, bool) {
	res := o.Exec([]string{"sw_vers", "-productVersion"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return 0, false
	}
	ver := strings.TrimSpace(res.Stdout)
	major, _, _ := strings.Cut(ver, ".")
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil {
		return 0, false
	}
	return n, true
}

// call inside _check_podman_machine_resources. Any error → empty map.
func (o *Options) loadWorkspaceConfigLoose() *jsonx.OrderedMap {
	cfg := loadConfigLoose(o.Workspace)
	if cfg == nil {
		return jsonx.NewOrderedMap()
	}
	return cfg
}
