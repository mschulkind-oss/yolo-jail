# Loophole wire protocol — v1

This is the framed protocol spoken between a jail-side client and a
host-side loophole daemon that uses the `internal/hostservice` helper
package (transport: `loopback-tls`, lifecycle: `spawned`).
The frame codec itself lives in `internal/frameproto`.

**The frame format is UNCHANGED.** What changed is only what carries
it: the bind-mounted Unix socket became a cert-pinned, token-
authenticated loopback TCP connection, because a Unix socket cannot
cross virtiofs on macOS + podman — see
[`loophole-transport.md`](loophole-transport.md). Everything from
"## Request" through "## Framing rules" below is byte-identical to v1
and stays that way; `PROTOCOL_VERSION` is still 1 because the wire
format did not move.

External loophole authors can rely on this spec: breaking changes
will bump `PROTOCOL_VERSION` and ship a transition window.

**This document is the WIRE contract only.** How a third party
*distributes* a loophole — and how they implement the server side
without a TLS stack — is
[`loophole-packaging.md`](loophole-packaging.md): a designed 15th
pack contribution kind, plus a yolo-run TLS **front** that lets a
daemon bind a plain AF_UNIX socket and have yolo publish the
endpoint file in front of it. Read it before writing a server; the
"Writing a server from scratch" section below is that doc's §2.3
deliverable, and it is explicit that the spec-only path is the
**unsupervised** one.

## Handshake

**There is no handshake at the frame layer.** A client sends one
length-prefixed JSON request and reads framed response data until the
server closes the connection or emits an exit frame. That much is
unchanged, and everything below "## Request" describes it.

**But four steps now precede the first request byte**, owned by the
transport rather than by this protocol. A client that skips them is
hung up on before the daemon ever sees it:

1. **Read the endpoint file** whose path is in
   `$YOLO_SERVICE_<NAME>_ENDPOINT`. One line, three whitespace-
   separated fields: `<host:port> <base64 cert DER> <token>`. Read it
   **fresh on every dial** and cache nothing — that is what lets a
   restarted daemon, on a new port with a new certificate and a new
   token, be picked up without relaunching the jail.
2. **TLS-dial** `host:port`, trusting **exactly** the certificate in
   field 2 through a dedicated root pool — not a CA, not the system
   roots — and verifying the server name `yolo-host-service`.
3. **Send the token frame**: a 4-byte big-endian length followed by
   the token bytes, as the first bytes on the connection, before any
   request.
4. **Read one byte.** `0x01` means authenticated. EOF means the token
   was rejected: the server writes nothing on failure, so a port
   scanner learns only that it was hung up on.

## Request

A single JSON object, length-prefixed with a 4-byte big-endian unsigned
int giving the UTF-8-encoded JSON body length:

```
+------------------+---------------------------------------+
| 4-byte big-endian| UTF-8 JSON body, exactly <length>     |
| unsigned length  | bytes                                 |
+------------------+---------------------------------------+
```

Canonical fields (convention, not enforced):

| Field      | Type    | Meaning                                                  |
|------------|---------|----------------------------------------------------------|
| `jail_id`  | string  | Jail identifier for logging (daemons must not trust it). |
| `mode`     | string  | Request kind, per-daemon vocabulary.                     |
| others     | any     | Daemon-specific; see its module.                         |

Example (host-processes list):

```json
{"jail_id": "yolo-a1b2c3", "mode": "list"}
```

## Response

After the request, the daemon writes zero or more **frames** then
optionally a final **exit frame**. Each frame:

```
+---------+-------------------+------------------+
| 1 byte  | 4 bytes big-endian| <length> bytes    |
| stream  | unsigned length   | payload           |
| id      |                   |                   |
+---------+-------------------+------------------+
```

Stream IDs:

| ID | Name   | Payload                                                                  |
|----|--------|--------------------------------------------------------------------------|
| 0  | stdout | Bytes the client should forward to its own stdout.                       |
| 1  | stderr | Bytes the client should forward to its own stderr.                       |
| 2  | exit   | Exactly 4 bytes: big-endian signed int32 exit code. Terminates response. |

A client consumes frames until it sees stream id 2 (exit) or the
connection closes. A daemon that finishes without sending an exit frame
is treated as exit code 0 by the library; closure without frames counts
as a protocol error (client's choice how to report).

## Framing rules

- Frames are independent; payload may be empty (length 0). Clients must
  handle zero-length frames.
- stdout and stderr frames may arrive interleaved; the client should
  forward each to the corresponding stream without reordering.
- After an exit frame, the daemon MUST NOT send additional frames. The
  library enforces this via `Session._exited`.
- Neither side should hold the connection open after the exit frame.
  Clients close after reading exit; daemons close after writing it.

## Versioning

`PROTOCOL_VERSION = 1` is exposed by the library. Future revisions that
break wire format bump this number and add a separate frame/field for
the version negotiation (so v1 clients continue to work against v1
daemons with no change).

A daemon SHOULD log its advertised version on startup. Clients do not
currently send a version with the request; if we need per-request
versioning later, it will be an optional `_v` field in the request
body.

## Security posture

**The boundary is "whatever runs as your user", and it did not change
when the transport did.** That is the specification rather than a gap:
on the host, anything running as you can already read your credentials
or act as you, and a jail extends that unchanged — anything running as
you may use the privileges granted to this jail. What changed is only
how that sentence is *enforced*.

The old wording — *"the socket file is the authentication; a daemon
trusts whoever can `connect()`"* — is quoted widely, including by
[`loophole-transport.md`](loophole-transport.md). It described a path.
On a port it is false twice over, so it is replaced rather than edited:

- **`0600` on a path is how you say "only my user"; a pre-shared token
  is how you say the same thing on a port.** The token is not extra
  security machinery bolted on top of the file-permission model — it
  *is* that model, expressed where there is no file to permit.
- **Reachability is not authorization.** The port is kernel-assigned
  but scannable, so "can connect" stopped being proof of anything.
  Connecting gets you a TLS handshake and then a dropped connection.

What each control actually stops:

| Control | Adversary it stops |
|---|---|
| bind `127.0.0.1` | anything on the LAN |
| TLS | a sibling jail **sniffing** a shared bridge (every jail holds `NET_RAW` there) |
| pinning the exact cert, whose key is host-only | a sibling jail **impersonating the daemon** |
| the per-jail, per-service token | a sibling jail **impersonating this jail** to its daemon |

**The token defends against a sibling jail, not against the jail's own
agent.** Anything inside jail A can read jail A's endpoint file — it is
mounted there and the agent runs as UID 0 by design. That is expected:
jail A using jail A's credential is the system working. A per-jail-
mounted Unix socket gave that isolation by construction and a shared
TCP port destroys it, which is the entire reason the token exists. A
jail may also rewrite its own endpoint file, and gains nothing by it —
it would only break its own connection.

Concretely:

- **The endpoint file is a credential.** It is written `0600` into the
  jail's own per-jail directory, which is created `0700`; a daemon
  refuses to publish into a directory that is group- or world-
  accessible. Never copy one between jails, and never paste one into a
  log or a bug report.
- **The TLS private key never leaves the daemon's memory** — never
  written to disk, never mounted into a jail. A fresh certificate per
  daemon process is correct precisely because clients re-read the file
  on every dial.
- **There is no token environment variable, deliberately.** An env var
  is inherited by every child process a client spawns; a file is read
  at the moment of use by the one process that needs it.
- Daemons must never trust request fields as argv material. The
  library's `Session.ExecAllowlisted(argvBuilder, allowlist, …)` helper
  enforces this by construction: argv positions are validated against a
  server-owned allowlist before the subprocess runs.

> **What the deleted first bullet claimed, and why it had to go.** It
> said the socket "is chmod 0600 and lives under the user's socket
> dir". Measured in a live jail 2026-08-13, of the three sockets then
> mounted `cgroup-delegate.sock` was `0777`, `claude-oauth-broker.sock`
> `0755`, and only `host-processes.sock` `0600` — inside a `0755`
> directory. The mode was never the mechanism it was described as; the
> per-jail **mount** was. Under `loopback-tls` the endpoint file's mode
> genuinely is load-bearing, so it is asserted in code rather than
> claimed here.

## Access logging

The helper library logs one structured line per request:

```
INFO host_service: jail=<id> keys=<sorted-req-keys> rc=<code> elapsed_ms=<n> bytes_out=<n>
```

Full request bodies are not logged (could be large or sensitive); just
the top-level key names, the exit code, and the total bytes written
across stdout+stderr frames. Enough to audit "what did jail X ask for"
without hoarding payload data.

## Writing a client from scratch

`cmd/yolo-ps` is the reference implementation. **This is no longer
implementable with `nc`** — steps 2–4 need a TLS library. That is the
honest cost of a transport that works on every platform, and it is
worth naming here rather than letting someone discover it: on the
retired `unix-socket` transport a shell one-liner really was enough.

1. Read the path in `$YOLO_SERVICE_<NAME>_ENDPOINT`, then read **that
   file**. Split on whitespace into **exactly three** fields:
   `host:port`, base64 cert DER, token. Fewer or more is a malformed
   endpoint — in particular, do not read a two-field file as "no
   token"; it is a truncated or stale file and authenticates nothing.
2. Base64-decode field 2, parse the DER certificate, and put that one
   certificate into a fresh, otherwise-empty trust root pool.
3. TLS-dial `host:port` with that pool as the **only** roots and
   `yolo-host-service` as the expected server name. Do not disable
   verification; the dial target and the certificate name differ on
   purpose, so the server name is overridden, not the checking.
4. Write the token frame: a 4-byte big-endian length, then the token
   bytes. Then read one byte — `0x01` is authenticated; EOF means the
   token was rejected, and reporting that as "the daemon is down" is
   the single most misleading thing a client can do here.
5. Write a 4-byte big-endian request length, then the JSON body.
6. Read response:
   - Read 5-byte header `(stream_id:u8, length:u32)`.
   - Read `length` bytes; forward or capture by `stream_id`.
   - If `stream_id == 2`, payload is a 4-byte signed exit code; done.
7. Close the connection.

Redo steps 1–4 on **every** connection. Caching the address, the
certificate, or the token is what re-reading exists to avoid.

## Writing a server from scratch

`internal/svcendpoint` is the reference implementation — `Listen` is
the whole server half, and yolo's own daemons layer the framed
protocol on top of it through `internal/hostservice`. **This is no
longer implementable with a shell script** any more than the client
is — steps 3–6 below need a TLS stack and a CSPRNG. Two boundaries
before the steps, both blunter than anything in the client section:

- **This is the UNSUPERVISED path.** Every security-critical
  property below — the endpoint file's `0600` mode, the publication
  directory's `0700`, the private key never touching disk, the
  constant-time token compare, the frame-length cap checked before
  allocation — is enforced by the daemon itself, and yolo cannot
  verify any of it from outside. A daemon on this path is trusted
  to the degree its author is.
- **It is reachable only by a loophole yolo itself ships.** Under
  [`loophole-packaging.md`](loophole-packaging.md) (§2.1, §2.3;
  designed, not yet built) a pack-shipped loophole may not implement
  this section: its manifest declares
  `host_daemon.publishes: "socket"`, the daemon binds a plain
  AF_UNIX socket at `{socket}`, and yolo runs the one audited
  implementation of everything below in front of it —
  `svcendpoint.ServeFront`, which already fronts the broker relay
  today. Behind the front, anything that can bind AF_UNIX and read a
  4-byte length prefix works: Python, Node, Rust, a shell script
  with `socat`. The `nc`-era simplicity this doc mourns above is
  restored on the *server* side. One behaviour change is invisible
  from inside the daemon and worth stating twice: the front **never
  propagates the client's EOF upstream**, so a daemon that reads its
  request *to EOF* works on a bare socket and hangs forever behind
  the front. Read to the length prefix. This section exists so the
  front can be understood and audited, not so a pack can opt out of
  it.

The steps, in an order that is load-bearing — publish last, so a
published file always names a live listener:

1. **Verify the publication directory before anything else.** The
   endpoint path arrives substituted into your argv as `{endpoint}`.
   Its directory must be a real directory (`lstat`, not `stat` — a
   symlink is a refusal), owned by your uid, with no group or world
   permission bits. Fail closed on any mismatch: `mkdir -p`
   semantics succeed on an already-existing attacker-owned directory
   without changing its owner or mode, and publishing there hands
   your credential to whoever owns it.
2. **Bind `127.0.0.1:0`** and let the kernel assign the port.
   Loopback keeps you off the LAN; kernel assignment means there is
   no probe-then-rebind window in which another local process could
   squat the port. Never take the port as configuration — the
   address is published, not passed in.
3. **Mint a throwaway TLS certificate** whose CommonName and sole
   SAN are `yolo-host-service`, and whose **private key never leaves
   your process's memory** — not marshaled, not PEM-encoded, never
   written to disk. A fresh certificate per process is correct, not
   a compromise, because clients re-read the endpoint file on every
   dial. Serve TLS 1.2 or newer. Interop note: mark the self-signed
   leaf `CA:TRUE` — OpenSSL-family verifiers require the trust
   anchor to carry it even for a one-certificate chain.
4. **Mint the token: 32 bytes from a CSPRNG, rendered as exactly 64
   lowercase hex characters.** No other format — see the couplings
   below for how anything else kills the daemon.
5. **Publish after a successful bind**: one line —
   `<advertised-host:port> <base64 cert DER> <token>\n`, three
   whitespace-separated fields — written to a temp file in the
   **same** directory, chmod `0600`, then renamed onto `{endpoint}`.
   Atomic-rename because clients re-read the file on every dial, and
   a torn read hands them a truncated token. The advertised host is
   `$YOLO_SVC_ADVERTISE_HOST` when set and non-empty, else
   `host.containers.internal`: bind loopback, advertise the gateway
   name the jail resolves — reverse the two and the jail dials its
   own loopback.
6. **Authenticate every connection before reading anything else.**
   Read a 4-byte big-endian length; **check it against a cap before
   allocating** (the reference cap is 4096 — without it a garbage
   prefix from an unauthenticated caller allocates gigabytes
   pre-auth); read that many bytes; compare against the token **in
   constant time** (`crypto/subtle` or your language's equivalent,
   never `==`). On success write the single byte `0x01` and clear
   the read deadline; on any failure write **nothing** and close, so
   a port scanner learns only that it was hung up on. Authenticate
   each connection concurrently — one stalled pre-auth connection
   must not block the rest for the handshake timeout.
7. **Then speak the framed protocol** ("## Request" onward).
8. **On shutdown, unlink the endpoint file** — retiring the listener
   retires its credential in the same step. Republishing (rewrite +
   rename) is how rotation works: the next dial picks it up, no
   restart on either side.

And three couplings that are enforced by yolo's **health** code
rather than by the wire — get one wrong and a conforming-looking
daemon dies with a misleading symptom:

- **The token must be exactly 64 lowercase hex**
  (`svcendpoint.IsToken`). Publish anything else — base64, uppercase
  hex, 16 bytes — and the endpoint file *parses* fine, but `Probe`
  returns false, the readiness wait times out, and the daemon is
  **SIGKILLed after five seconds** with a log that looks perfectly
  healthy: it bound, minted, published, and was then killed without
  a word.
- **`Probe` — a content check, never file existence — is THE health
  predicate**, at startup readiness and everywhere else. It
  re-parses the published file on every call: exactly three fields,
  a splittable `host:port`, a certificate that parses, a well-formed
  token. A file that merely *exists* proves nothing and is treated
  as proving nothing; never design a health story around existence.
- **`yolo check` dials `127.0.0.1` at the published port**
  (`svcendpoint.DialLocal` keeps the port and substitutes the
  loopback address, because the host generally cannot resolve the
  advertised gateway name). The listener must therefore accept on
  loopback regardless of what it advertised — which step 2 gives you
  for free, and any deviation from step 2 takes away.
