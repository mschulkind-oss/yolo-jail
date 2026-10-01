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
// so the port has to be known before the container starts. The launcher binds port 0 on the
// declared loopback host and releases it: on a shared namespace that loopback is normally the
// jail's, so the kernel's answer is a port free on the loopback the daemon will bind. ⚠ NOT on a
// macOS podman machine, where `network.mode: host` joins the VM's namespace rather than the
// Mac's: the port is picked on the Mac's loopback, which the daemon never binds. The pick still
// keeps two such jails off each other's declared ports, but whether the port is free in the VM
// is unproven there. sharesLauncherNetns classifies that setup as shared, which is the same
// blind spot (NC-D43). The port is
// free when picked, not reserved, so a process binding it in the moments before the daemon
// does wins it. That fails closed: the daemon cannot bind, and its clients are refused by
// whichever daemon holds the port, for the wrong caller token (NC-D43).
//
// LIFETIME. Settled once per process, like the caller tokens, so the two compositions one
// launch runs cannot hand one jail two ports. An ATTACH never picks: the running jail's
// daemons bound their ports at its boot, so the attach reads them back from the channel that
// launch wrote (runningServedAddresses), and a jail that recorded none serves the declared
// addresses (NC-D44).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// servedAddressState is what this process has settled about served addresses.
type servedAddressState struct {
	// moved maps each declared loopback `host:port` this launch moved to its served address.
	moved map[string]string
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
// pick that fails keeps the declared address, says so, and moves nothing else.
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
	picked, err := pickLoopbackPorts(todo)
	if err != nil {
		o.pr(o.Stderr).print(fmt.Sprintf("[yellow]Could not pick free loopback ports for this "+
			"jail's daemons (%v); they keep their declared ports, which another jail on this "+
			"loopback may already hold.[/yellow]", err))
		return
	}
	if o.served.moved == nil {
		o.served.moved = map[string]string{}
	}
	for hp, to := range picked {
		o.served.moved[hp] = to
	}
}

// pickLoopbackPorts returns a free port on each declared address's host, keyed by the declared
// address. Every listener stays open until all are picked, so no two picks share a port.
func pickLoopbackPorts(declared []string) (map[string]string, error) {
	var held []net.Listener
	defer func() {
		for _, l := range held {
			_ = l.Close()
		}
	}()
	out := make(map[string]string, len(declared))
	for _, hp := range declared {
		host, _, err := net.SplitHostPort(hp)
		if err != nil {
			return nil, err
		}
		l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return nil, err
		}
		held = append(held, l)
		out[hp] = l.Addr().String()
	}
	return out, nil
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
// REPLACING any it picked: what an attach composes is where the jail's daemons already listen.
func (o *Options) adoptRunningServedAddresses(running map[string]string) {
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
