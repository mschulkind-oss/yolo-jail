// Package svcendpoint is the loophole framework's transport: a TCP connection to
// 127.0.0.1 that behaves like a 0600 Unix socket. It is the ONLY real transport a
// loophole daemon uses ("loopback-tls"); `none` means "no daemon", not a second
// transport. See docs/design/loophole-transport.md §3.0.
//
// Five steps, each replacing something the filesystem used to give away free:
//
//  1. the listener binds 127.0.0.1 on a KERNEL-ASSIGNED port (loopback, so not
//     on the LAN; kernel-assigned, so there is no probe-then-rebind window in
//     which another local process could squat the port);
//  2. it mints a throwaway TLS certificate whose PRIVATE KEY NEVER LEAVES THIS
//     PROCESS'S MEMORY — not written to disk, not mounted into a jail;
//  3. it publishes host:port, the public certificate, and a freshly minted
//     per-(jail, service) bearer token to one file in the jail's own per-jail
//     mounted directory;
//  4. the client re-reads that file FRESH ON EVERY DIAL and demands the server
//     present EXACTLY that certificate — via a dedicated root pool, never a CA
//     (docs/design/loophole-transport.md §5: the broker CA's private key was
//     readable in-jail, and pinning must not depend on a CA at all);
//  5. the client sends the token as the first bytes on the connection; the
//     server compares it in constant time, acks one byte, and hangs up on a
//     mismatch. Reachability is not authorization: possession of the token is
//     what "only my user" means on a port.
//
// # Threat model, in six lines
//
// The adversary is a SIBLING JAIL, not the jail's own agent. The jail is trusted
// with its own credential by design (it runs as UID 0 and can read its own
// endpoint file; rewriting it only breaks its own connection). A same-user host
// process is INSIDE the intended boundary too — that is the product's spec, matching
// the host, where anything running as you can already act as you. What a shared
// loopback/bridge port loses relative to a per-jail-mounted socket is sibling
// isolation: the port is scannable, so reachability stops proving identity. TLS
// stops a sibling sniffing, the pinned host-only key stops a sibling impersonating
// the listener, and the token stops a sibling impersonating the jail.
//
// # The published endpoint file IS A CREDENTIAL
//
// It carries the per-jail bearer token alongside the address and the public cert,
// so its 0600 mode and its per-jail directory are load-bearing, not cosmetic.
// Never log its contents, never copy it between jails, never place it in a shared
// directory. There is deliberately NO env-var delivery of the token: an env var is
// inherited by every child process a daemon spawns, and a fallback would keep that
// inheritance alive for whatever reads it first (§3.2, OQ-T7).
//
// # Why both halves live in one package
//
// The file format, the token frame, and the pin are ONE contract. Splitting the
// server from the client is exactly how the two ends drift (§3.3 mitigation 1),
// which is also why the transport belongs to the framework rather than to any one
// daemon. The package is deliberately STDLIB-ONLY with zero internal imports, so
// the leanest baked clients (cmd/yolo-ps is "a pure frameproto client — no config,
// no json5") can import it without dragging the CLI in.
//
// # Two length-prefixed frames on one connection, and they are OPPOSITES
//
// Both are 4-byte big-endian length then a body, so they look alike and are not:
//
//   - the TOKEN frame (token.go:97-127) is CLIENT→SERVER, pre-auth, and is
//     consumed and DISCARDED — its bytes never become part of the payload;
//   - the connection PREAMBLE (preamble.go, docs/design/broker-as-a-pack.md §5.5)
//     is HOST→DAEMON, post-auth, written once at connection open, and is the only
//     thing yolo ever ADDS to a stream.
//
// Neither is part of the daemon's own protocol. yolo never decodes a daemon's
// bytes: the preamble replaces the relay's parse-and-restamp with a prefix, which
// is why the framework has no opinion about what follows it and works the same
// for audio, HTTP or a database socket as for frameproto.
//
// This layer sits BENEATH the wire protocol. internal/frameproto is unchanged and
// unaware; a daemon behind Listen never learns which transport carried its bytes —
// one implementation of the preamble covers both server shapes, so a fronted
// daemon reads it through the front's io.Copy and an endpoint-publishing one reads
// it directly off its own accepted connection.
package svcendpoint
