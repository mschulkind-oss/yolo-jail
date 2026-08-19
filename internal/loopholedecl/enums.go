package loopholedecl

// enums.go is the CLOSED vocabulary the schema is built on: a manifest does not
// DEFINE a transport or a publication mode, it SELECTS one. The set is closed
// because the run pipeline, the checks and the manifests all have to agree on
// each value, and a typo in any one of those places silently selects the other
// mechanism.
//
// The values live here rather than in internal/loopholes because they are what
// the manifest may SAY — every reader of a manifest needs them, including the
// footprint and `pack lint`, neither of which may import the runtime.

// DefaultBrokerIP is the container runtime's host-gateway sentinel, and the
// default for `broker_ip`. The runtime translates it into the right
// host-reachable address.
const DefaultBrokerIP = "host-gateway"

// Transport values.
//
// THERE ARE TWO, and that is the whole point of the unification
// (docs/design/loophole-transport.md §7.4). `transport` now answers exactly one
// question — "does this loophole have a host daemon a jail dials, and if so how"
// — where it used to conflate that with "does this loophole intercept TLS".
const (
	// TransportLoopbackTLS is THE transport (internal/svcendpoint): the framework
	// publishes an endpoint file and the daemon never learns what carried its
	// bytes.
	TransportLoopbackTLS = "loopback-tls"
	// TransportNone means NO DAEMON, not a different transport. It stays.
	TransportNone = "none"
)

// Retired transport values, kept ONLY to recognize them and say what to write
// instead. Neither is in validTransports, so a manifest naming one is rejected —
// deliberately, because a value that still validates is a value someone will use.
//
// A manifest that declared either one loses nothing by declaring loopback-tls:
//
//   - "tls-intercept" never selected a transport. Intercept-ness is carried
//     entirely by `intercepts` + `broker_ip` + `ca_cert`, which is why the one
//     behavioural reader (RuntimeArgsFor's Apple Container skip) now keys on
//     len(Intercepts) instead. It named hop A (the in-jail TLS terminator) while
//     saying nothing about hop B, the hop that actually crosses to the host.
//   - "unix-socket" is the transport that does not work on macOS + podman at all
//     (virtiofs shares a socket's inode, not its connection endpoint), which is
//     the defect the unification exists to fix.
const (
	RetiredTransportTLSIntercept = "tls-intercept"
	RetiredTransportUnixSocket   = "unix-socket"
)

// host_daemon.publishes values: WHAT THE DAEMON ITSELF PUBLISHES. The retired
// `unix-socket` transport conflated two facts — what the jail dials and what
// the daemon binds — and this field is the honest split's second half
// (loophole-packaging.md §2.1). `transport` stays loopback-tls either way,
// because the transport is what the jail dials and that does not change.
const (
	// PublishesEndpoint (the default): the daemon publishes the loopback-TLS
	// endpoint file itself at {endpoint} — what all bundled loopholes do.
	PublishesEndpoint = "endpoint"
	// PublishesSocket: the daemon binds a plain AF_UNIX socket at {socket};
	// yolo waits for that socket, then runs the TLS front (svcendpoint) and
	// publishes the endpoint file in front of it. The daemon never learns what
	// carried its bytes — this is what makes a non-Go daemon expressible.
	PublishesSocket = "socket"
)

// host_daemon.request_end values: HOW A REQUEST ENDS on the daemon's socket,
// meaningful under publishes:"socket" (loophole-packaging.md §2.1b hazard 2).
const (
	// RequestEndFramed (the default): the protocol is length-prefixed (or
	// otherwise self-delimiting), so the front never propagates the client's
	// EOF upstream — today's relay-parity behaviour, bit-identical.
	RequestEndFramed = "framed"
	// RequestEndEOF: the daemon reads its request TO EOF, so the front
	// half-closes the upstream socket when the client's request direction
	// ends. Without this such a daemon works on a bare socket and hangs
	// forever behind the front.
	RequestEndEOF = "eof"
)

// host_daemon.scope values: WHO THE DAEMON BELONGS TO — one process per jail, or
// one process per HOST serving every jail on it.
//
// This is the third question in the same grammar as `publishes` (what the daemon
// brings up) and `request_end` (how a request ends on it), and it is deliberately
// a scope rather than a `singleton: true` bool. The dimension is "what is this
// daemon shared across", and naming it for today's only interesting answer would
// make a third answer look like a violation instead of an addition — the same
// argument §5.5 makes for calling the connection preamble a preamble rather than
// an identity frame.
//
// It exists because ONE loophole in the tree cannot be spawned per jail at all:
// the Claude OAuth broker holds the flock that stops two jails burning the same
// single-use refresh token, so a second copy of it is not a second daemon, it is
// the race the loophole exists to prevent (docs/design/agent-credentials.md §2.5,
// broker-as-a-pack.md §5.2). Before this key, the run pipeline expressed that by
// testing the loophole's NAME.
const (
	// ScopeJail (the default): one daemon per jail. yolo spawns it at launch,
	// waits for it, and kills its process group when the jail ends.
	ScopeJail = "jail"
	// ScopeHost: ONE daemon per host, serving every jail on the machine. yolo
	// ENSURES it (idempotently, under a host-wide flock) instead of spawning it,
	// binds it at a fixed host-wide socket derived from the loophole's name
	// (paths.HostSingletonSocket), and gives each jail its own front over that one
	// socket. A jail ending closes ITS FRONT and never touches the daemon, which
	// other jails are still using.
	//
	// It requires PublishesSocket, and that is refused at load rather than
	// documented: under PublishesEndpoint the daemon publishes the endpoint file
	// itself, and an endpoint file carries ONE jail's bearer token — so a
	// host-wide daemon publishing one would hand every jail the same credential,
	// or hand N-1 of them a credential minted for someone else.
	ScopeHost = "host"
)

// Valid enum values. Kept as ordered slices in sorted order so the
// "not in [...]" error strings render deterministically. Unexported because a
// package-level slice is mutable; the accessors below hand out copies.
var (
	validTransports  = []string{TransportLoopbackTLS, TransportNone}
	validLifecycles  = []string{"external", "spawned"}
	validRestarts    = []string{"always", "on-failure", "no"}
	validPublishes   = []string{PublishesEndpoint, PublishesSocket}
	validRequestEnds = []string{RequestEndEOF, RequestEndFramed}
	validScopes      = []string{ScopeHost, ScopeJail}
)

// ValidTransports returns the accepted `transport` values.
func ValidTransports() []string { return copyOf(validTransports) }

// ValidLifecycles returns the accepted `lifecycle` values.
func ValidLifecycles() []string { return copyOf(validLifecycles) }

// ValidRestarts returns the accepted `jail_daemon.restart` values.
func ValidRestarts() []string { return copyOf(validRestarts) }

// ValidPublishes returns the accepted `host_daemon.publishes` values.
func ValidPublishes() []string { return copyOf(validPublishes) }

// ValidRequestEnds returns the accepted `host_daemon.request_end` values.
func ValidRequestEnds() []string { return copyOf(validRequestEnds) }

// ValidScopes returns the accepted `host_daemon.scope` values.
func ValidScopes() []string { return copyOf(validScopes) }

func copyOf(list []string) []string { return append([]string(nil), list...) }

// retiredTransportHint appends the migration instruction to the "not in [...]"
// error when the rejected value is one this repo used to ship.
//
// The bare enum error is technically complete and practically useless here: the
// reader wrote a value the docs told them to write, and the consequence of the
// rejection is that their loophole VANISHES (loadFromDir warns and moves on). The
// hint turns a breaking change into a self-documenting one, which is the price of
// removing a value rather than deprecating it.
func retiredTransportHint(transport string) string {
	switch transport {
	case RetiredTransportTLSIntercept:
		return " — 'tls-intercept' was retired: it named the in-jail TLS terminator," +
			" not a transport. Write 'loopback-tls' and keep 'intercepts'/'broker_ip'/" +
			"'ca_cert' exactly as they are; those are what wire the interception."
	case RetiredTransportUnixSocket:
		return " — 'unix-socket' was retired (docs/design/loophole-transport.md §7.4):" +
			" it cannot cross virtiofs on macOS + podman. Write 'loopback-tls' and add" +
			" 'publishes': 'socket' to 'host_daemon': the daemon keeps binding its" +
			" AF_UNIX socket at the path yolo substitutes into '{socket}', and yolo" +
			" runs the TLS front over it and publishes the endpoint file for you."
	}
	return ""
}
