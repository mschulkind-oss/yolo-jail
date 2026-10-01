package run

// servedaddresses.go is the launcher half of SERVED ADDRESSES (the term is coined in
// internal/packload's served.go; docs/plans/notch-convergence.md §2.4, NC-D41 to NC-D44): where
// each jail daemon and pack service this launch runs answers on the jail's loopback.
//
// WHY. Every such address used to be a fixed port: the OpenAI adapter's :1460, the AWS
// adapter's :1461, the wire bridge's :8214 to :8216. On a jail with a network namespace of its
// own that is harmless, since nothing else is on that loopback. On a jail that SHARES the
// launcher's (sharesLauncherNetns: `network.mode: host`, and a nested podman forced onto
// `--net=host`) two jails contend for one port, and since caller tokens the loser's clients
// reach the winner's daemon and are refused 401 (NC-D17). So on a shared namespace the launcher
// picks an ephemeral port for every declared address and composes it everywhere the address is
// named — the daemon's argv (JailDaemonSpec.Listen, resolved by loopholes.JailDaemonPayload),
// its clients' pack env pointer (packload.ServedDaemons.Listen) and the provider table and via
// base (packload.ServedDaemons.ServedURL). A private namespace keeps every declared address,
// so nothing a bridged jail's agents see changes (NC-D42).
//
// WHY THE LAUNCHER PICKS. A jail's clients are composed on the host before its daemons bind,
// so the port has to be known before the container starts. The launcher RESERVES a port on the
// declared loopback host (launchservice's reserve.go: a socket bound to port 0 and never listened
// on): on a shared namespace that loopback is normally the jail's, so the kernel's answer is a
// port free on the loopback the daemon will bind. ⚠ NOT on a macOS podman machine, where
// `network.mode: host` joins the VM's namespace rather than the Mac's: the port is picked on the
// Mac's loopback, which the daemon never binds. The pick still keeps two such jails off each
// other's declared ports, but whether the port is free in the VM is unproven there.
// sharesLauncherNetns classifies that setup as shared, which is the same blind spot (NC-D43).
//
// HELD UNTIL ITS SERVER HAS IT (NC-D69). This launch binds listeners of its own after the pick,
// each host service's front on port 0, and a pick that let its port go could be handed to one of
// them, so the daemon's own bind failed. So each reservation stays held: a doorway this launch
// opens itself is handed it (takeReservedPort, launchservice.PlanAt), and every other one is
// released only immediately before the process that starts the jail's daemons (releaseReservedPorts).
// From then until the daemon binds, only a process outside this launch can take the port, and that
// fails closed: the daemon cannot bind, and its clients are refused by whichever daemon holds the
// port, for the wrong caller token (NC-D43).
//
// LIFETIME. Settled once per process, like the caller tokens, so the two compositions one
// launch runs cannot hand one jail two ports. An ATTACH never picks: the running jail's
// daemons bound their ports at its boot, so the attach reads them back from the channel that
// launch wrote (runningServedAddresses), and a jail that recorded none serves the declared
// addresses (NC-D44).

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// servedAddressState is what this process has settled about served addresses.
type servedAddressState struct {
	// moved maps each declared loopback `host:port` this launch moved to its served address.
	moved map[string]string
	// held is the reservation of each served address's port this launch picked, keyed by the
	// served address, until the process serving it has it (HELD UNTIL ITS SERVER HAS IT, above).
	held map[string]*launchservice.Reserved
	// adopted is set once an attach took the running jail's map: from then on nothing is
	// picked, and a declared address the map does not name is served where it is declared.
	adopted bool
}

// sharesNetnsFor reports whether a jail launched on rt with cfg shares this process's network
// namespace — sharesLauncherNetns over the same inputs its other readers pass. A native runtime
// shares by construction (sharesLauncherNetns says so for every paths.NativeRuntimes entry),
// and since OQ-DP8/OQ-DP9 macos-user RUNS jail daemons in its guest, and since HS-D15 opens the
// credential doorways outside it (macosuserdoorways.go), both on the Mac's own loopback: so it
// picks a port for each, which is what keeps two concurrent launches off one declared port
// (macos-user-nix-and-features.md JD-7). Only the daemons the launch serves are
// settled (jailDaemonsFor, loopholes.ServedJailDaemons), so a declined one moves nothing.
func (o *Options) sharesNetnsFor(cfg *jsonx.OrderedMap, rt string) bool {
	// A hand-built Options (every one is a test) has no PathExists seam, and reads as not
	// nested, which is what fillDefaults' os.Stat answers outside a container too.
	inContainer := o.PathExists != nil && o.inContainer()
	return sharesLauncherNetns(rt, o.resolveNetMode(cfg), inContainer)
}

// declaredServedAddresses is every declared loopback `host:port` the payload specs serve at,
// sorted: each daemon's `jail_daemon.listen`, and each selected pack service's adapter
// addresses and via address, for the services the payload runs.
func declaredServedAddresses(specs []loopholes.JailDaemonSpec, packs []*packload.Pack) []string {
	set := map[string]bool{}
	running := map[string]bool{}
	for _, s := range specs {
		running[s.Name] = true
		if s.Listen != "" {
			set[s.Listen] = true
		}
	}
	addURL := func(raw string) {
		if hp := loopbackHostPort(raw); hp != "" {
			set[hp] = true
		}
	}
	for _, a := range packload.Adaptations(packs) {
		if a.Service != "" && running[a.Service] {
			addURL(a.Address)
		}
	}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, svc := range p.Decl.Services() {
			if running[svc.Name] && svc.ViaAddress != "" {
				addURL(svc.ViaAddress)
			}
		}
	}
	out := make([]string, 0, len(set))
	for hp := range set {
		out = append(out, hp)
	}
	sort.Strings(out)
	return out
}

// loopbackHostPort is raw's `host:port` when raw is a URL whose host is a loopback IP literal
// with a port, "" otherwise: only such an address is one a jail daemon binds, so only such an
// address can move.
func loopbackHostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if loopholedecl.ListenAddressProblem(u.Host) != "" {
		return ""
	}
	return u.Host
}

// settleServedAddresses picks a served address for every declared address in specs and packs
// that this process has not settled yet, when the jail shares this process's network namespace.
// Nothing on a private namespace, nothing after an attach adopted the running jail's map. A
// pick that fails keeps the declared address, says so, and moves nothing else. Each pick is a
// reservation this launch holds (o.served.held) until the process serving the port has it.
func (o *Options) settleServedAddresses(cfg *jsonx.OrderedMap, rt string,
	specs []loopholes.JailDaemonSpec, packs []*packload.Pack) {
	if o.served.adopted || !o.sharesNetnsFor(cfg, rt) {
		return
	}
	var todo []string
	for _, hp := range declaredServedAddresses(specs, packs) {
		if _, ok := o.served.moved[hp]; !ok {
			todo = append(todo, hp)
		}
	}
	if len(todo) == 0 {
		return
	}
	picked, err := launchservice.ReservePorts(todo)
	if err != nil {
		o.pr(o.Stderr).print(fmt.Sprintf("[yellow]Could not pick free loopback ports for this "+
			"jail's daemons (%v); they keep their declared ports, which another jail on this "+
			"loopback may already hold.[/yellow]", err))
		return
	}
	if o.served.moved == nil {
		o.served.moved = map[string]string{}
	}
	if o.served.held == nil {
		o.served.held = map[string]*launchservice.Reserved{}
	}
	for hp, r := range picked {
		o.served.moved[hp] = r.Addr()
		o.served.held[r.Addr()] = r
	}
}

// takeReservedPort hands over the reservation of the served address addr's port, for a process
// this launch starts itself and hands it to (launchservice.PlanAt): nil when this launch holds
// none for it.
func (o *Options) takeReservedPort(addr string) *launchservice.Reserved {
	r := o.served.held[addr]
	delete(o.served.held, addr)
	return r
}

// releaseReservedPorts releases every reservation this launch still holds: its jail daemons'
// ports, immediately before the process that starts those daemons (the macos-user sandbox, the
// container's keeper), and whatever a launch that started nothing left, and every launch-owned
// service and doorway plan the launch never started.
func (o *Options) releaseReservedPorts() {
	launchservice.ReleaseAll(o.served.held)
	o.served.held = nil
	for _, p := range o.launchServices {
		p.Release()
	}
	for _, p := range o.launchDoorways {
		p.Release()
	}
}

// servedAddress is where the daemon declared at hp serves in this launch.
func (o *Options) servedAddress(hp string) string {
	if to, ok := o.served.moved[hp]; ok {
		return to
	}
	return hp
}

// withServedListen is specs with each Listen at its served address, the value
// loopholes.JailDaemonPayload resolves the argv's {listen} to.
func (o *Options) withServedListen(specs []loopholes.JailDaemonSpec) []loopholes.JailDaemonSpec {
	for i := range specs {
		if specs[i].Listen != "" {
			specs[i].Listen = o.servedAddress(specs[i].Listen)
		}
	}
	return specs
}

// movedServedAddresses is a copy of the map this process settled on, nil when it moved nothing.
func (o *Options) movedServedAddresses() map[string]string {
	if len(o.served.moved) == 0 {
		return nil
	}
	out := make(map[string]string, len(o.served.moved))
	for k, v := range o.served.moved {
		out[k] = v
	}
	return out
}

// servedAddressesValue is the channel line's value for moved, "" when nothing moved.
func servedAddressesValue(moved map[string]string) string {
	if len(moved) == 0 {
		return ""
	}
	data, err := json.Marshal(moved) // encoding/json sorts map keys, so the line is stable
	if err != nil {
		return ""
	}
	return string(data)
}

// parseServedAddresses reads a channel line's value back, keeping only entries whose both
// halves are loopback addresses: the file is jail-writable (callertokens.go's reason).
func parseServedAddresses(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	var out map[string]string
	for from, to := range m {
		if loopholedecl.ListenAddressProblem(from) != "" || loopholedecl.ListenAddressProblem(to) != "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[from] = to
	}
	return out
}

// runningServedAddresses reads the served addresses out of the live per-entry channel file the
// running jail's launch wrote, as runningCallerTokens reads its tokens. nil when the file, its
// channel section or the line is missing: a jail launched on a private namespace, or by a yolo
// older than served addresses, whose daemons serve at their declared addresses.
func runningServedAddresses(wsState string) map[string]string {
	data, err := readRegularFileIn(wsState, "yolo-user-env.sh")
	if err != nil {
		return nil
	}
	values, ok := entrypoint.ParseEntryChannel(data)
	if !ok {
		return nil
	}
	return parseServedAddresses(values[paths.ServedAddressesEnv])
}

// adoptRunningServedAddresses makes the running jail's served addresses this process's,
// REPLACING any it picked, whose reservations it releases: what an attach composes is where the
// jail's daemons already listen.
func (o *Options) adoptRunningServedAddresses(running map[string]string) {
	launchservice.ReleaseAll(o.served.held)
	o.served = servedAddressState{moved: running, adopted: true}
}

// servedAddressesAgree reports whether channel was composed with exactly the served addresses
// o has settled on.
func (c *packChannel) servedAddressesAgree(settled map[string]string) bool {
	if c == nil {
		return true
	}
	if len(c.servedAddresses) != len(settled) {
		return false
	}
	for k, v := range c.servedAddresses {
		if settled[k] != v {
			return false
		}
	}
	return true
}
