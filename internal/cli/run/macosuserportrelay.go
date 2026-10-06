package run

// macosuserportrelay.go delivers the REMAPS of the two port keys on macos-user
// (docs/design/declaration-parity.md §5.1.1, corrected 2026-10-05): one TCP relay per entry whose
// two port numbers differ, opened OUTSIDE the sandbox as this launch's own listener when the
// session starts, and closed when the command exits, with its live connections.
//
// WHEN. The backend opens them, through macosuser.JailDaemons.OnLaunch (macosUserRelaysAt),
// immediately before the session's command: after every step that can refuse the launch, the
// backend's own preconditions and its nix build (up to half an hour on a first launch) included.
// So a refused launch, or one still building, publishes nothing — before the sandbox exists, the
// only thing listening on a `ports` entry's 127.0.0.1:JAIL is one of the human's own processes.
//
// WHY A RELAY, AND WHY HERE. A macos-user sandbox runs on the launcher's own network stack, so a
// same-port entry is already true without anything doing it: binding is publishing, and the
// sandbox's `localhost:<port>` is this machine's. A remap is not, because there is no second
// stack for a rewrite to land on — the container path has podman's `-p` for `network.ports` and a
// socat hop for `network.forward_host_ports`, both below the macos-user return in run.Run. What
// stands in for them is a splice the launcher performs itself, in Go, since this backend bakes
// nothing (no socat). The two directions:
//
//   - `network.ports` "[IP:]HOST:JAIL": listen on IP:HOST, or on 0.0.0.0:HOST when the entry
//     names no address (the default of podman's `-p`), and dial 127.0.0.1:JAIL, where the
//     sandbox's service listens.
//   - `network.forward_host_ports` "JAIL:HOST": listen on 127.0.0.1:JAIL, where the sandbox's
//     client dials, and dial 127.0.0.1:HOST, as the container path's host socat does.
//
// WHAT IT DOES NOT DO, each said where it applies:
//
//   - TCP only. A "/udp" remap is named in the port-key notice as not relayed.
//   - No relay for a same-port entry: it is already satisfied, and a listener there would take
//     the very port the sandbox's service or the host's own service holds.
//   - None when the launch resolves a mode other than bridge (resolveNetModeSource):
//     `network.mode: "host"` or a typed `--network host` drops both keys, as it does on podman.
//     And none under the seal, which hands a build no forward and no published port (seal.go).
//   - ⚠ A `ports` relay publishes MORE than podman's `-p` does. A container's published port
//     reaches only the container's own service, and a service bound to its 127.0.0.1 stays
//     private; here the relay dials the Mac's loopback, which the sandbox shares with every
//     process of the human's, so whatever listens on 127.0.0.1:JAIL — the agent's service bound
//     there on purpose, or one of the human's own — answers at HOST on every interface the relay
//     listens on, for the session's whole life. The disclosure line says so, with the entry that
//     keeps it on this Mac, for every relay that does not listen on loopback (relayExposure).
//   - ⚠ Nothing CONFINES a port. The sandbox still binds any port it likes on the real
//     interfaces; SBPL `network-bind` could narrow that (docs/plans/setup-support-gaps.md G13),
//     and that half is not built.
//   - A relay whose listen port is taken warns, names the command that finds the holder, and the
//     launch goes on, as the container path's socat-absent warning does; the relay keeps trying the
//     port and takes it once it frees, which is how a second session of one workspace takes
//     a remap over when the first ends (startPortRelay). One that cannot listen for any other
//     reason warns and is skipped, and so is one on a port this launch picked for a served address.
//
// Its decisions are recorded as an implementation decision in docs/design/declaration-parity.md's
// ledger, under the maintainer's 2026-10-04 delegation, and are reversible.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// The two config keys a relay can come from, spelled as the user's config spells them.
const (
	keyNetworkPorts     = "network.ports"
	keyForwardHostPorts = "network.forward_host_ports"
)

// relayDialTimeout bounds one relayed connection's dial to the far side. A port nothing listens
// on refuses at once on a loopback, so this bounds only an address that does not answer at all.
const relayDialTimeout = 10 * time.Second

// relayRetryInterval is how often a relay whose port was in use at launch tries it again.
const relayRetryInterval = time.Second

// relayAcceptBackoff is the pause after an accept error that did not come from closing the
// listener (a process out of file descriptors, say), so the relay keeps serving rather than
// spinning or giving up.
const relayAcceptBackoff = 50 * time.Millisecond

// macosUserPortRelay is one remap this launch delivers: a TCP listener on listen, outside the
// sandbox, splicing each connection it accepts to dial.
type macosUserPortRelay struct {
	key    string // keyNetworkPorts or keyForwardHostPorts
	entry  string // the entry as the user wrote it
	listen string // host:port the relay listens on
	dial   string // host:port each accepted connection is spliced to
}

// macosUserUnrelayed is a remap entry this launch does not relay, and why, in words that end with
// the step that would have it relayed where there is one.
type macosUserUnrelayed struct {
	key, entry, why string
}

// macosUserPortPlan is what a macos-user launch does with the two port keys' remaps.
type macosUserPortPlan struct {
	relays    []macosUserPortRelay
	unrelayed []macosUserUnrelayed
}

// Why a remap is not relayed. The mode reasons are composed in macosUserRelaysWithheld, because
// they name where the mode came from.
const (
	relayWhyUDP = "the relay carries TCP only, so the sandboxed process is reachable on the port " +
		"it binds; list a TCP port, or use a container runtime (`runtime: \"podman\"`), whose " +
		"published ports carry UDP"
	relayWhySealed = "a sealed build gets no published port and no forward"
)

// macosUserPortPlan is the plan for this launch: nothing on any backend but macos-user, whose
// container siblings deliver both keys through their own argv.
func (o *Options) macosUserPortPlan(rt string, cfg *jsonx.OrderedMap) macosUserPortPlan {
	if rt != "macos-user" { // parity: HonoredBy — a container backend's argv carries both keys itself: podman's -p and the socat forward hostForwardPorts feeds (Apple Container's own cells are its assembler's)
		return macosUserPortPlan{}
	}
	return planMacosUserPortRelays(cfg, o.macosUserRelaysWithheld(cfg))
}

// macosUserRelaysWithheld says why this launch relays no remap at all, or "" when it relays them.
// The mode is the one the launch resolves (a typed `--network` over `network.mode`), and the
// reason names whichever of the two set it, since that is the thing to change.
func (o *Options) macosUserRelaysWithheld(cfg *jsonx.OrderedMap) string {
	if o.Sealed {
		return relayWhySealed
	}
	mode, source := o.resolveNetModeSource(cfg)
	if mode == "bridge" {
		return ""
	}
	if source == netModeFromFlag {
		return "`--network " + mode + "` drops both port keys, as it does on podman; launch " +
			"without it to have the remap relayed"
	}
	return "`network.mode: \"" + mode + "\"` drops both port keys, as it does on podman; " +
		"remove it to have the remap relayed"
}

// planMacosUserPortRelays reads both keys' entries into relays and unrelayed remaps. withheld,
// when not empty, is why no remap is relayed. An entry that is not a remap (one number, or two
// that match) is neither: it is already satisfied on a shared stack. An entry the grammar does not
// read is neither too, because config validation refuses it before a launch gets here.
//
// THE ONE CLASSIFIER of a remap for this backend: the notice (noteMacosUserPortKeys), the launch's
// relays and the agent's briefing line (backendLimits) all read this plan, so what the human is
// told, what runs and what the agent is told cannot disagree.
func planMacosUserPortRelays(cfg *jsonx.OrderedMap, withheld string) macosUserPortPlan {
	var plan macosUserPortPlan
	netSec := cfgMap(cfg, "network")
	if netSec == nil {
		return plan
	}
	skip := func(key, entry, why string) {
		plan.unrelayed = append(plan.unrelayed, macosUserUnrelayed{key: key, entry: entry, why: why})
	}
	for _, p := range parsePublishPorts(asAnyList(mapGet(netSec, "ports"))) {
		if p.host == p.jail {
			continue
		}
		switch {
		case withheld != "":
			skip(keyNetworkPorts, p.entry, withheld)
		case p.proto != "tcp":
			skip(keyNetworkPorts, p.entry, relayWhyUDP)
		default:
			ip := p.ip
			if ip == "" {
				ip = "0.0.0.0"
			}
			plan.relays = append(plan.relays, macosUserPortRelay{key: keyNetworkPorts, entry: p.entry,
				listen: net.JoinHostPort(ip, strconv.Itoa(p.host)),
				dial:   net.JoinHostPort("127.0.0.1", strconv.Itoa(p.jail))})
		}
	}
	for _, e := range asAnyList(mapGet(netSec, "forward_host_ports")) {
		// One entry at a time, so a label stays paired with its parse; ParsePortForwards is the
		// container path's own reader of this key, so the two backends read one grammar.
		parsed, err := ParsePortForwards([]any{e}, nil)
		if err != nil || len(parsed) != 1 || parsed[0].LocalPort == parsed[0].HostPort {
			continue
		}
		f, entry := parsed[0], pyStrCoerce(e)
		if withheld != "" {
			skip(keyForwardHostPorts, entry, withheld)
			continue
		}
		plan.relays = append(plan.relays, macosUserPortRelay{key: keyForwardHostPorts, entry: entry,
			listen: net.JoinHostPort("127.0.0.1", strconv.Itoa(f.LocalPort)),
			dial:   net.JoinHostPort("127.0.0.1", strconv.Itoa(f.HostPort))})
	}
	return plan
}

// publishPort is one `network.ports` entry read in the grammar config.validatePublishPort accepts
// and podman's `-p` takes: [IP:]HOST:JAIL, then an optional "/tcp" or "/udp".
type publishPort struct {
	entry      string // as written
	ip         string // "" when the entry names no address
	host, jail int
	proto      string // "tcp" (also when unsuffixed) or "udp"
}

// parsePublishPorts reads every entry the grammar accepts and drops the rest, which validation
// has already refused. ParsePortForwards cannot stand in: it splits at the first colon and would
// read "127.0.0.1:8000:3000" and "8000:3000/tcp" as errors.
func parsePublishPorts(entries []any) []publishPort {
	var out []publishPort
	for _, e := range entries {
		s, ok := e.(string)
		if !ok {
			continue
		}
		p := publishPort{entry: s, proto: "tcp"}
		base := s
		if i := strings.LastIndex(base, "/"); i >= 0 {
			p.proto, base = base[i+1:], base[:i]
			if p.proto != "tcp" && p.proto != "udp" {
				continue
			}
		}
		parts := strings.Split(base, ":")
		var host, jail string
		switch len(parts) {
		case 2:
			host, jail = parts[0], parts[1]
		case 3:
			p.ip, host, jail = parts[0], parts[1], parts[2]
		default:
			continue
		}
		var hok, jok bool
		p.host, hok = portNumber(host)
		p.jail, jok = portNumber(jail)
		if !hok || !jok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// portNumber reads a TCP/UDP port number, 1-65535.
func portNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// relayDisclosure is the line naming one relay, for a live launch ("Relaying") and a plan render
// ("Would relay"). A disclosure rather than a warning: it reports something the launch does.
func relayDisclosure(verb string, r macosUserPortRelay) string {
	return richtext.Escape(fmt.Sprintf("%s %s -> %s for `%s` entry %s, outside the sandbox, until "+
		"the command exits.", verb, r.listen, r.dial, r.key, r.entry) + relayExposure(r))
}

// relayExposure is the sentence a `ports` relay that does not listen on loopback adds to every
// line naming it, "" for any other relay: what it really publishes, which is the Mac's shared
// 127.0.0.1:JAIL — whoever listens there — on a real interface, and the entry that keeps it on
// this Mac. podman's `-p` can only ever reach the container's own port.
func relayExposure(r macosUserPortRelay) string {
	if r.key != keyNetworkPorts || loopbackListen(r.listen) {
		return ""
	}
	_, hostPort, _ := net.SplitHostPort(r.listen)
	_, jailPort, _ := net.SplitHostPort(r.dial)
	return fmt.Sprintf(" It publishes whatever listens on this Mac's %s, the sandbox's service or "+
		"one of yours, on %s, which podman's -p never does; write the entry as `127.0.0.1:%s:%s` "+
		"to keep it on this Mac.", r.dial, r.listen, hostPort, jailPort)
}

// relaysExpose reports whether any of relays publishes a port on a real interface: a
// `network.ports` relay not listening on loopback, which exposes whatever listens on the port it
// dials however that binds (relayExposure).
func relaysExpose(relays []macosUserPortRelay) bool {
	for _, r := range relays {
		if relayExposure(r) != "" {
			return true
		}
	}
	return false
}

// loopbackListen reports whether a relay's listen address is on this machine's loopback.
func loopbackListen(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// macosUserRelaysAt is the session-start hook that opens relays (macosuser.JailDaemons.OnLaunch),
// or nil when there are none. The backend calls it once nothing is left to refuse, immediately
// before the session's command, and runs what it returns once the command has exited.
func (o *Options) macosUserRelaysAt(relays []macosUserPortRelay) func() (stop func()) {
	if len(relays) == 0 {
		return nil
	}
	return func() func() { return o.startMacosUserPortRelays(relays) }
}

// startMacosUserPortRelays opens every planned relay and returns what closes them all. None
// refuses the launch: a remap is a convenience the rest of the session does not depend on, which
// is the container path's socat-absent shape. One that cannot listen is warned about by entry:
//
//   - its port is in use: the warning names the command that finds the holder, and the relay
//     keeps trying, taking the port once it frees (startPortRelay). Said on the terminal
//     now, because by the time it takes over the agent owns the terminal.
//   - its port is one this launch picked for a served address (servedaddresses.go): skipped.
//     Those picks went free before the sandbox started, and the guest daemon handed one may not
//     have bound it yet, so a relay opened there would take it from the daemon.
//   - any other failure (an address this Mac does not have, say): skipped.
func (o *Options) startMacosUserPortRelays(relays []macosUserPortRelay) func() {
	var running []*portRelay
	out := o.pr(o.Stderr)
	served := o.servedPorts()
	for _, r := range relays {
		_, port, _ := net.SplitHostPort(r.listen)
		if addr, ok := served[port]; ok {
			out.print(fmt.Sprintf("[yellow]Warning: not relaying `%s` entry %s[/yellow] — this launch "+
				"picked port %s for its own served address %s (a jail daemon's or a service's, chosen "+
				"among the ports free at launch). The launch goes on without this remap; the next "+
				"launch chooses again, so relaunching relays it.", r.key, r.entry, port, addr))
			continue
		}
		pr, err := startPortRelay(r.listen, r.dial)
		switch {
		case err == nil:
			running = append(running, pr)
			out.print(relayDisclosure("Relaying", r))
		case pr != nil:
			running = append(running, pr)
			out.print(fmt.Sprintf("[yellow]Warning: `%s` entry %s is not relayed yet[/yellow] — "+
				"listening on %s failed: %s. Something on this Mac holds the port: `lsof -iTCP:%s "+
				"-sTCP:LISTEN` names it. The launch goes on, and takes the port over once it "+
				"frees, relaying it to %s until the command exits; if another yolo session of this "+
				"workspace holds it, that is when that session ends.", r.key, r.entry,
				r.listen, richtext.Escape(err.Error()), port, r.dial) + richtext.Escape(relayExposure(r)))
		default:
			out.print(fmt.Sprintf("[yellow]Warning: not relaying `%s` entry %s[/yellow] — listening "+
				"on %s failed: %s. The launch goes on without this remap; give the entry an address "+
				"this Mac has and a port it lets you listen on.", r.key, r.entry, r.listen,
				richtext.Escape(err.Error())))
		}
	}
	return func() {
		for _, pr := range running {
			pr.stop()
		}
	}
}

// servedPorts is the port of every served address this launch picked (servedaddresses.go), each
// with its address.
func (o *Options) servedPorts() map[string]string {
	ports := map[string]string{}
	for _, addr := range o.served.moved {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			ports[port] = addr
		}
	}
	return ports
}

// portRelay is one TCP relay: its listener once it has one, and every connection it has open on
// either side, so stop can close them all.
type portRelay struct {
	dial   string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	ln    net.Listener          // nil while a port in use is awaited
	conns map[net.Conn]struct{} // nil once stopped
	wg    sync.WaitGroup
}

// startPortRelay listens on listen and splices each accepted connection to dial, until stop.
//
// A PORT IN USE IS NOT GIVEN UP ON: the error comes back WITH a relay, which tries the port again
// every relayRetryInterval and serves once it frees, until stop. Two terminals in one
// workspace are two macos-user sessions (servicessession.go), each opening its own relays on the
// same ports, so the later one's cannot listen while the earlier one's runs; without the retry its
// remap went with the earlier session, under an agent still using it. Any other error returns no
// relay.
func startPortRelay(listen, dial string) (*portRelay, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &portRelay{dial: dial, ctx: ctx, cancel: cancel, conns: map[net.Conn]struct{}{}}
	ln, err := net.Listen("tcp", listen)
	switch {
	case err == nil:
		r.ln = ln
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.serve(ln)
		}()
		return r, nil
	case errors.Is(err, syscall.EADDRINUSE):
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.awaitPort(listen)
		}()
		return r, err
	default:
		cancel()
		return nil, err
	}
}

// awaitPort tries listen every relayRetryInterval until it can, then serves it, or returns at
// stop.
func (r *portRelay) awaitPort(listen string) {
	tick := time.NewTicker(relayRetryInterval)
	defer tick.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-tick.C:
		}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			continue
		}
		r.mu.Lock()
		if r.conns == nil { // stopped while it listened
			r.mu.Unlock()
			_ = ln.Close()
			return
		}
		r.ln = ln
		r.mu.Unlock()
		r.serve(ln)
		return
	}
}

// addr is the address the relay listens on, its port resolved when the plan asked for port 0, or
// "" while it awaits its port.
func (r *portRelay) addr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln == nil {
		return ""
	}
	return r.ln.Addr().String()
}

// serve accepts on ln until it closes, splicing each connection on its own goroutine.
func (r *portRelay) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if r.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(relayAcceptBackoff):
			}
			continue
		}
		if !r.track(c) {
			_ = c.Close()
			return
		}
		r.wg.Add(1)
		go r.splice(c)
	}
}

// splice carries one accepted connection to the far side and back. Each direction half-closes
// its destination at a clean end of its source, so a client that finishes sending still reads
// the whole answer; a direction that ends in an error closes both, which ends the other.
func (r *portRelay) splice(in net.Conn) {
	defer r.wg.Done()
	defer r.untrack(in)
	d := net.Dialer{Timeout: relayDialTimeout}
	out, err := d.DialContext(r.ctx, "tcp", r.dial)
	if err != nil {
		return
	}
	if !r.track(out) {
		_ = out.Close()
		return
	}
	defer r.untrack(out)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pipeHalf(out, in)
	}()
	pipeHalf(in, out)
	<-done
}

// pipeHalf copies src to dst, then half-closes dst for a clean end of src and closes both for
// anything else.
func pipeHalf(dst, src net.Conn) {
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = src.Close()
		return
	}
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = dst.Close()
}

// track records c as open, or reports false once the relay is stopped.
func (r *portRelay) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns == nil {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

// untrack forgets c and closes it.
func (r *portRelay) untrack(c net.Conn) {
	r.mu.Lock()
	if r.conns != nil {
		delete(r.conns, c)
	}
	r.mu.Unlock()
	_ = c.Close()
}

// stop closes the listener and every open connection, cancels any dial in flight and any wait
// for a port in use, and returns once every goroutine of the relay has.
func (r *portRelay) stop() {
	r.cancel()
	r.mu.Lock()
	ln, conns := r.ln, r.conns
	r.conns = nil
	r.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for c := range conns {
		_ = c.Close()
	}
	r.wg.Wait()
}

// relaySentenceForAgent is the briefing's line for the remaps this launch relays, or "" when it
// relays none: the agent cannot read the launch's stderr, and a remap it does not know about is a
// port it will not think to use.
func relaySentenceForAgent(relays []macosUserPortRelay) string {
	var parts []string
	for _, r := range relays {
		_, listenPort, _ := net.SplitHostPort(r.listen)
		_, dialPort, _ := net.SplitHostPort(r.dial)
		switch r.key {
		case keyForwardHostPorts:
			parts = append(parts, fmt.Sprintf("`localhost:%s` here reaches the host's port %s "+
				"(`%s` entry %s)", listenPort, dialPort, r.key, r.entry))
		case keyNetworkPorts:
			parts = append(parts, fmt.Sprintf("your `127.0.0.1:%s` is also published at the host's "+
				"`%s` (`%s` entry %s)", dialPort, r.listen, r.key, r.entry))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "This launch relays the config's port remaps from outside the sandbox, over TCP and " +
		"for this session only: " + strings.Join(parts, "; ") + ". A relay that could not listen " +
		"at launch is not running, except that one whose port was in use takes it once it frees; " +
		"the human was told which."
}
