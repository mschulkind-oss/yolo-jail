package run

// macosuserportrelay.go delivers the REMAPS of the two port keys on macos-user
// (docs/design/declaration-parity.md §5.1.1, corrected 2026-10-05): one TCP relay per entry whose
// two port numbers differ, opened OUTSIDE the sandbox as this launch's own listener and closed
// when the command exits, with its live connections.
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
//     reaches only a service bound to the container's own interface, so a service bound to its
//     127.0.0.1 stays private; here the relay dials the Mac's loopback, so a service the agent
//     bound to 127.0.0.1 on purpose answers at HOST on every interface the relay listens on.
//     The disclosure line says so for every relay that does not listen on loopback.
//   - ⚠ Nothing CONFINES a port. The sandbox still binds any port it likes on the real
//     interfaces; SBPL `network-bind` could narrow that (docs/plans/setup-support-gaps.md G13),
//     and that half is not built.
//   - A relay whose listen port is taken warns, names the command that finds the holder, and the
//     launch goes on without it, as the container path's socat-absent warning does.
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
	line := fmt.Sprintf("%s %s -> %s for `%s` entry %s, outside the sandbox, until the command "+
		"exits.", verb, r.listen, r.dial, r.key, r.entry)
	if r.key == keyNetworkPorts && !loopbackListen(r.listen) {
		line += " Unlike podman's -p, it also publishes a service the sandbox bound to " +
			"127.0.0.1 alone."
	}
	return richtext.Escape(line)
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

// startMacosUserPortRelays opens every planned relay and returns what closes them all. One that
// cannot listen is warned about by entry, with the command that names what holds its port, and
// the launch goes on without it: a remap is a convenience the rest of the session does not
// depend on, which is the container path's socat-absent shape.
func (o *Options) startMacosUserPortRelays(relays []macosUserPortRelay) func() {
	var running []*portRelay
	out := o.pr(o.Stderr)
	for _, r := range relays {
		pr, err := startPortRelay(r.listen, r.dial)
		if err != nil {
			_, port, _ := net.SplitHostPort(r.listen)
			out.print(fmt.Sprintf("[yellow]Warning: not relaying `%s` entry %s[/yellow] — listening "+
				"on %s failed: %s. Something on this Mac may hold the port already: `lsof -iTCP:%s "+
				"-sTCP:LISTEN` names it. The launch goes on without this remap.", r.key, r.entry,
				r.listen, richtext.Escape(err.Error()), port))
			continue
		}
		running = append(running, pr)
		out.print(relayDisclosure("Relaying", r))
	}
	return func() {
		for _, pr := range running {
			pr.stop()
		}
	}
}

// portRelay is one running TCP relay: a listener, and every connection it has open on either
// side, so stop can close them all.
type portRelay struct {
	ln     net.Listener
	dial   string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	conns map[net.Conn]struct{} // nil once stopped
	wg    sync.WaitGroup
}

// startPortRelay listens on listen and splices each accepted connection to dial, until stop.
func startPortRelay(listen, dial string) (*portRelay, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &portRelay{ln: ln, dial: dial, ctx: ctx, cancel: cancel, conns: map[net.Conn]struct{}{}}
	r.wg.Add(1)
	go r.serve()
	return r, nil
}

// addr is the address the relay listens on, its port resolved when the plan asked for port 0.
func (r *portRelay) addr() string { return r.ln.Addr().String() }

func (r *portRelay) serve() {
	defer r.wg.Done()
	for {
		c, err := r.ln.Accept()
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

// stop closes the listener and every open connection, cancels any dial in flight, and returns
// once every goroutine of the relay has.
func (r *portRelay) stop() {
	r.cancel()
	_ = r.ln.Close()
	r.mu.Lock()
	conns := r.conns
	r.conns = nil
	r.mu.Unlock()
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
		"for this session only: " + strings.Join(parts, "; ") + ". A relay whose host port was " +
		"already taken at launch is not running; the human was told which."
}
