package run

// hostports handles the forward_host_ports config parser and the socat
// UNIX-LISTEN→TCP argv the run path spawns per forwarded port.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Socket-wait tuning (host side polls for socat's socket files before the
// container starts). Production defaults must stay 2s / 5ms.
const (
	SocketWaitDeadline     = 2 * time.Second
	SocketWaitPollInterval = 5 * time.Millisecond
)

// PortForward is one parsed `network.forward_host_ports` entry (config.PortForward, where the one
// reader of the key lives so the macos-user profile's loopback allow list reads the same grammar).
type PortForward = config.PortForward

// ParsePortForwards is config.ParsePortForwards, the key's one reader.
func ParsePortForwards(entries []any, warn func(string)) ([]PortForward, error) {
	return config.ParsePortForwards(entries, warn)
}

// discloseImplicitProviderForwards prints ONE line per port a user provider caused to be
// forwarded, naming the port and the provider that asked for it (OQ-PC2,
// docs/reference/wire-bridge.md#oq-pc2).
//
// # Why this is not suppressible, and why that is right here
//
// A forward is a hole into the host: a port the user never wrote is bound inside the jail
// and a socket is opened on the launcher. The user's own config cannot be grepped for it,
// which is precisely what cost four hypotheses when an aliasing defect put the wire
// bridge's own address into this list. Under the report-tier rules a launch has no quiet
// mode, so this line is permanent — and that is the correct class for it: it reports
// something yolo DID, not the absence of something.
//
// Only ports that are actually implicit are named. A port the user ALSO declared in
// forward_host_ports is already visible in their config and in the briefing, so naming it
// here would report a hole they dug themselves.
func discloseImplicitProviderForwards(warn func(string), declared []any, sources []providerForward) {
	if len(sources) == 0 {
		return
	}
	explicit := map[int]bool{}
	if parsed, err := ParsePortForwards(declared, nil); err == nil {
		for _, p := range parsed {
			explicit[p.LocalPort] = true
		}
	}
	for _, src := range sources {
		if explicit[src.Port] {
			continue
		}
		warn(fmt.Sprintf(
			"yolo: forwarding host port %d into this jail — provider %q names it on localhost, "+
				"so the jail reaches the service on YOUR machine at the same port",
			src.Port, src.Provider))
	}
}

// appleContainerForwardRefusal is the launch's refusal of a declared `network.forward_host_ports`
// on Apple Container, or "" when there is nothing to refuse.
//
// THERE IS NO FORWARD TO DELIVER THERE, so the choice was never between a forward and a warning.
// The key asks for a path from the jail to a service on this Mac, and Apple Container 1.1.0
// carries no traffic from a container back to the Mac (backend-parity.md §5.5); the one flag it
// has near this, `--publish-socket host:container`, forwards a HOST connection inward, the
// opposite direction. Emitting it anyway failed the launch inside `container run`, after the
// host-side socat had created the socket the flag names, with an error naming that socket and
// not the key (measured 2026-09-16). Refusing here keeps the same outcome, a launch that does
// not start, and makes it yolo's: before any host process starts, naming the key, each entry
// and the two ways on. That is the setup census's `Refused` cell for this key
// (internal/setupcensus), which before this read as a refusal nobody made.
//
// Read through hostForwardPorts, the one predicate both halves of a forward read, so an unhonored
// `network.mode: "host"` (appliedNetMode answers bridge on this backend) still refuses. A sealed
// build forwards nothing (seal.go), so it has nothing to refuse. The implicit forwards a
// localhost provider asks for are not this key, and are not refused here.
func (o *Options) appleContainerForwardRefusal(rt string, cfg *jsonx.OrderedMap) string {
	if rt != "container" || o.Sealed { // parity: Refused — Apple Container carries no container-to-Mac traffic and its --publish-socket forwards the other way, so a declared forward refuses the launch naming the key
		return ""
	}
	fwd := o.hostForwardPorts(cfg, rt)
	if len(fwd) == 0 {
		return ""
	}
	return "[bold red]Refusing to launch: `network.forward_host_ports` cannot be delivered on " +
		"Apple Container[/bold red] — " + strings.Join(portLabels(fwd), ", ") + ". A forward lets " +
		"the jail reach a service on this Mac, and Apple Container carries no traffic from a " +
		"container back to the Mac; its own --publish-socket forwards the other way, so " +
		"`container run` would reject the launch. Remove `network.forward_host_ports` from your " +
		"config for this setup, or launch with `--setup podman-self`, which forwards it."
}

// mergeHostForwards appends implicit localhost-provider forwards to the user's
// explicit forwards. An explicit local port wins: it is a deliberate legacy
// remap (for example, jail 8080 -> host 9090), whereas the implicit spelling
// means same-port host loopback. Keeping the explicit mapping avoids changing a
// working configuration when automatic local-provider forwarding is introduced.
func mergeHostForwards(declared, implicit []any) []any {
	if len(implicit) == 0 {
		return declared
	}
	out := append([]any(nil), declared...)
	used := map[int]bool{}
	if parsed, err := ParsePortForwards(declared, nil); err == nil {
		for _, p := range parsed {
			used[p.LocalPort] = true
		}
	}
	for _, raw := range implicit {
		var port int
		switch v := raw.(type) {
		case int:
			port = v
		default:
			continue
		}
		if port < 1 || port > 65535 || used[port] {
			continue
		}
		used[port] = true
		out = append(out, port)
	}
	return out
}

// SocketPath returns the host-side socket file path for a local port under
// socketDir: "port-<localPort>.sock".
func SocketPath(socketDir string, localPort int) string {
	return filepath.Join(socketDir, fmt.Sprintf("port-%d.sock", localPort))
}

// SocatArgv returns the host-side socat argv bridging a Unix listen socket to a
// host TCP port. Frozen contract (argv must not drift — the container-side socat
// depends on the exact form):
//
//	socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:127.0.0.1:<hostPort>
func SocatArgv(sockPath string, hostPort int) []string {
	return []string{
		"socat",
		fmt.Sprintf("UNIX-LISTEN:%s,fork,mode=777", sockPath),
		fmt.Sprintf("TCP:127.0.0.1:%d", hostPort),
	}
}

// SocketNotReadyWarning is the stderr text emitted when socat's socket
// files don't appear before the deadline.
func SocketNotReadyWarning(missing []string) string {
	return fmt.Sprintf(
		"Warning: socat socket(s) not ready after %.1fs: %s",
		SocketWaitDeadline.Seconds(), joinComma(missing),
	)
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
