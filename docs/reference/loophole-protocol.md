---
status: current
verified: 2026-09-09
verified_commit: a3922298
covers:
  - internal/frameproto/
  - internal/hostservice/
  - internal/crossaudit/
  - internal/svcendpoint/crossing.go
  - internal/svcendpoint/token.go
  - internal/svcendpoint/endpointfile.go
  - cmd/yolo-ps/
tags: [loopholes, protocol, wire-format, audit, security]
summary: "The frame protocol v1 spoken between a jail-side client and a host-side loophole daemon: a length-prefixed JSON request, stream-tagged response frames, an exit frame, and the four transport-owned steps that precede the first request byte. Also the two-tier boundary audit — one line per connection from the transport, one line per request from the daemon — and the from-scratch client and server specs."
---

# The loophole frame protocol — v1

**Status:** CURRENT as of 2026-09-09, verified against `a3922298`.

A **loophole daemon** runs on the host and answers requests from inside a jail. The
protocol it speaks is one length-prefixed JSON request in, then stream-tagged frames
out, then an optional exit frame. `ProtocolVersion` is **1** and the frame format has
never moved: the codec is `internal/frameproto`, which takes only `io.Reader`/`io.Writer`
and so cannot tell which transport carried its bytes.

This document is the **wire contract**. External loophole authors may rely on it;
breaking changes bump the version and ship a transition window.

| Component | Lives in |
| :--- | :--- |
| The frame codec — the frozen contract | `internal/frameproto` (`ProtocolVersion`, `StreamStdout`, `StreamStderr`, `StreamExit`, `ErrClosedBeforeRequest`) |
| The server harness: accept loop, request parsing, tier-2 log, guarded exec | `internal/hostservice` (`ServeUnix`, `ServeFrontedUnix`, `ServeEndpoint`, `Session`, `Handler`) |
| The transport beneath it: endpoint file, cert pin, token frame | `internal/svcendpoint` (`Listen`, `Dial`, `ServeFront`, `Probe`, `IsToken`) |
| Tier-1 connection records | `internal/svcendpoint` (`Crossing`, `CrossingViaFront`, `CrossingViaEndpoint`), written by `internal/crossaudit` |
| The reference client | `cmd/yolo-ps` |

**Reads with:** [`loophole-transport.md`](loophole-transport.md) (the layer beneath this
one — how a jail reaches the daemon at all), [`loophole-system.md`](loophole-system.md)
(how a loophole is declared, activated and disclosed),
[`pack-system.md`](pack-system.md) (the pack framework a loophole is a contribution to),
[`../guides/loopholes.md`](../guides/loopholes.md) (the authoring guide, including the
manifest keys that select a server shape).

---

## Principles

**The frame format is frozen.** Everything from [Request](#request) through
[Framing rules](#framing-rules) is byte-identical to v1 and stays that way. The
transport underneath was replaced wholesale — a bind-mounted Unix socket became a
cert-pinned, token-authenticated loopback TCP connection — and `ProtocolVersion` did not
move, because the wire format did not.

**A daemon never learns its transport.** `Session` and the connection handler are
`net.Conn`-based, so one accept loop serves all three server shapes. This is what lets a
daemon be tested against a bare socket and shipped behind a TLS front unchanged.

**A daemon must not trust request fields.** `jail_id` is for logging and nothing else;
argv material from a request is validated against a server-owned allowlist before any
subprocess runs (`Session.ExecAllowlisted`).

**The boundary is "whatever runs as your user", and it did not change when the transport
did.** That is the specification rather than a gap: on the host, anything running as you
can already read your credentials or act as you, and a jail extends that unchanged. What
changed is only how the sentence is enforced — `0600` on a path is how you say "only my
user"; a pre-shared token is how you say the same thing on a port.

## Vocabulary

- **Frame** — one `<stream id byte><4-byte big-endian length><payload>` unit in the
  response direction. Ours, and unrelated to the *token frame* below.
- **Token frame** — the length-prefixed credential the client sends as the very first
  bytes on a connection, pre-auth. It is consumed and discarded by the transport; its
  bytes never become part of the payload. Not a protocol frame.
- **Connection preamble** — the one thing yolo ever *adds* to a stream: a host-written
  prefix, post-auth, carrying the jail identity yolo derived host-side. Written once at
  connection open, read by the daemon before the request. Not part of the daemon's own
  protocol, and yolo never decodes what follows it.
- **The front** — a yolo-run loopback-TLS listener that authenticates a connection and
  then splices it to a plain AF_UNIX socket the daemon binds. The daemon sees an ordinary
  local socket. Defined in [`loophole-transport.md`](loophole-transport.md).
- **Tier 1 / tier 2** — the two halves of the boundary audit: one record per
  *connection*, emitted by the transport; one line per *request*, emitted by the daemon
  harness. See [Access logging](#access-logging).

## What precedes the first request byte

There is **no handshake at the frame layer**. A client sends one length-prefixed JSON
request and reads framed response data until the server closes or emits an exit frame.

**But four steps precede it**, owned by the transport rather than by this protocol. A
client that skips them is hung up on before the daemon ever sees it:

1. **Read the endpoint file** whose path is in `$YOLO_SERVICE_<NAME>_ENDPOINT`. One line,
   three whitespace-separated fields: `<host:port> <base64 cert DER> <token>`. Read it
   **fresh on every dial** and cache nothing — that is what lets a restarted daemon, on a
   new port with a new certificate and a new token, be picked up without relaunching the
   jail.
2. **TLS-dial** `host:port`, trusting **exactly** the certificate in field 2 through a
   dedicated root pool — not a CA, not the system roots — and verifying the server name
   `yolo-host-service`.
3. **Send the token frame**: a 4-byte big-endian length followed by the token bytes,
   before any request.
4. **Read one byte.** `0x01` means authenticated. EOF means the token was rejected: the
   server writes nothing on failure, so a port scanner learns only that it was hung up
   on.

> [!WARNING]
> **EOF at step 4 is an auth failure, and reporting it as "the daemon is down" is the
> single most misleading thing a client can do.** The ack byte exists for exactly this
> attribution: it arrives *before* the request is ever sent, so EOF at that point is
> unambiguous.

## Request

A single JSON object, length-prefixed with a 4-byte big-endian unsigned int giving the
UTF-8-encoded JSON body length:

```
+------------------+---------------------------------------+
| 4-byte big-endian| UTF-8 JSON body, exactly <length>     |
| unsigned length  | bytes                                 |
+------------------+---------------------------------------+
```

Canonical fields (convention, not enforced):

| Field | Type | Meaning |
| :--- | :--- | :--- |
| `jail_id` | string | Jail identifier for logging. **Daemons must not trust it.** |
| `mode` | string | Request kind, per-daemon vocabulary. |
| others | any | Daemon-specific; see its module. |

Example (host-processes list):

```json
{"jail_id": "yolo-a1b2c3", "mode": "list"}
```

Clients do not send a version with the request. If per-request versioning is ever needed
it is an optional `_v` field in the request body.

## Response

After the request the daemon writes zero or more **frames**, then optionally a final
**exit frame**. Each frame:

```
+---------+-------------------+------------------+
| 1 byte  | 4 bytes big-endian| <length> bytes    |
| stream  | unsigned length   | payload           |
| id      |                   |                   |
+---------+-------------------+------------------+
```

Stream IDs:

| ID | Name | Payload |
| :--- | :--- | :--- |
| 0 | stdout | Bytes the client forwards to its own stdout. |
| 1 | stderr | Bytes the client forwards to its own stderr. |
| 2 | exit | Exactly 4 bytes: big-endian **signed** int32 exit code. Terminates the response. |

The header is unsigned (`>BI`) and the exit payload is **signed** (`>i`), so a negative
return code — a signal death — round-trips.

> [!WARNING]
> **The journal bridge uses the same framing with DIFFERENT stream IDs (1/2/3). Do not
> conflate the two.** A client written against one and pointed at the other misreads
> every frame.

## Framing rules

- Frames are independent; a payload may be empty (length 0). Clients must handle
  zero-length frames.
- stdout and stderr frames may arrive interleaved; forward each to the corresponding
  stream without reordering.
- After an exit frame the daemon **must not** send additional frames. The library
  enforces this on the session.
- Neither side holds the connection open after the exit frame. Clients close after
  reading exit; daemons close after writing it.
- A daemon that finishes without sending an exit frame is treated as exit code 0 by the
  library. Closure with no frames at all is a protocol error, and how to report it is the
  client's choice.

## Versioning

`ProtocolVersion = 1` is exposed by the codec. A revision that breaks the wire format
bumps this number and adds a separate frame or field for version negotiation, so v1
clients keep working against v1 daemons with no change. A daemon logs its advertised
version at startup.

## The three server shapes

`internal/hostservice` owns the accept loop and the request harness but **not** a
transport — it owns three, and each caller names the one it wants. There is deliberately
**no `Serve`**: the unqualified name is what carried the host-wide broker singleton across
a transport boundary by accident, with its signature preserved and every test still green.

| Entry point | Binds / publishes | Who dials it | Tier 2? |
| :--- | :--- | :--- | :--- |
| `ServeUnix` | a plain AF_UNIX socket | host-to-host callers only | yes, with a caveat below |
| `ServeFrontedUnix` | an AF_UNIX socket **only yolo's own front dials** | the front, for a `publishes: "socket"` daemon | yes |
| `ServeEndpoint` | the loopback-TLS endpoint file itself | the jail directly | yes |

**"Fronted" and "no tier 2" are not the same statement**, though they coincided until
`ServeFrontedUnix` existed. A `publishes: "socket"` daemon built on this package sits
behind the front and still writes one request line per request, because the parsing
happens in the harness rather than in the splice. A fronted daemon **yolo did not write**
gets tier 1 and only tier 1.

## Access logging

**There are TWO tiers, and neither can cover the other.** Tier 1 is per-connection and
covers everything that rides the transport. Tier 2 is per-request and exists only where
there is a parsed request to describe.

### Tier 1 — one record per connection

`internal/svcendpoint` emits one `Crossing` per jail↔host connection — accepted *or*
rejected — and `internal/crossaudit` appends it to a single per-host log:

```
2026-08-15T12:00:00Z crossing jail=yolo-host-services-a1b2c3d4 service=host-processes \
  via=front outcome=accepted reason=- bytes_in=41 bytes_out=4096 elapsed_ms=1500
```

| Field | Meaning |
| :--- | :--- |
| `jail` | the per-jail publication directory's name — **derived host-side** from the path yolo published at, never the client's `jail_id` |
| `service` | the loophole / host-service name, from the same path |
| `via` | `front` (spliced) or `endpoint` (handed to a caller's own accept loop by `Listen`) |
| `outcome` | `accepted`, `rejected` (auth failed), or `unreachable` (authenticated, upstream daemon gone) |
| `reason` | `token-mismatch`, `bad-token-frame`, `handshake-incomplete`, `upstream-dial-failed`, or `-` |
| `bytes_in` / `bytes_out` | plaintext byte **counts** each way — a length, never a value |
| `elapsed_ms` | accept→close, or the length of a failed pre-auth exchange |

Three properties worth knowing:

- **Per HOST, not per jail.** The question is "what crossed today", which is cross-jail
  by nature; the narrower question is one `rg jail=` away, and a per-jail file would also
  sit where `yolo prune` sweeps.
- **Bounded** to the active log plus exactly one archived generation, rotated by rename,
  so the ceiling is a property rather than a policy. No reaper.
- **AUDIT ONLY.** It opens lazily, warns once on any failure and then goes silent, and a
  panicking sink is recovered and uninstalled. A crossing never fails, blocks or slows
  because of it — and when the log cannot be opened at all, crossings simply are not
  recorded.

### Tier 2 — one line per request

The daemon harness logs one structured line per request:

```
INFO host_service: jail=<id> keys=<sorted-req-keys> rc=<code> elapsed_ms=<n> bytes_out=<n>
```

Full request bodies are **not** logged — just the top-level key names, the exit code, and
the total bytes written across stdout+stderr frames. Enough to audit "what did jail X ask
for" without hoarding payload data. It goes to the package logger on stderr, which the
supervisor redirects into a per-service log file.

**Which `jail=` a tier-2 line carries depends on the transport that delivered the
request, and the split is the point:**

- On a **preamble-bearing** connection (`ServeEndpoint`, `ServeFrontedUnix`), `jail=` is
  the value yolo asserted in the connection preamble — the same host-side derivation tier
  1 uses. The two tiers then agree by construction, and a client sending a spoofed
  `jail_id` sees it overridden in both.
- On a bare `ServeUnix` connection there is no preamble, so `jail=` falls back to the
  client's own `jail_id`. Nothing yolo ships is on that path; the fallback stays for a
  host-to-host caller, where no jail boundary was crossed and there is nothing for yolo to
  assert.

> [!WARNING]
> **Do not design anything that expects the front to produce a tier-2 line.** It splices
> a byte stream it does not parse, and nothing constrains a loophole's protocol to be
> request-shaped — it may be framed, a raw stream, audio, video. Tier 2 comes from the
> **daemon**, or from nowhere. Connection-level is the honest ceiling for a fronted
> daemon yolo did not write, and anything richer is per-loophole rather than framework.

**When the two tiers disagree about `jail=`, tier 1 is the one that means something** —
tier 2 may be recording what the client said, which this protocol tells daemons not to
trust.

## Security posture

What each control actually stops:

| Control | Adversary it stops |
| :--- | :--- |
| bind `127.0.0.1` | anything on the LAN |
| TLS | a sibling jail **sniffing** a shared bridge (every jail holds `NET_RAW` there) |
| pinning the exact cert, whose key is host-only | a sibling jail **impersonating the daemon** |
| the per-jail, per-service token | a sibling jail **impersonating this jail** to its daemon |

**The token defends against a sibling jail, not against the jail's own agent.** Anything
inside jail A can read jail A's endpoint file — it is mounted there and the agent runs as
UID 0 by design. That is expected: jail A using jail A's credential is the system
working. A per-jail-mounted Unix socket gave that isolation by construction and a shared
TCP port destroys it, which is the entire reason the token exists. A jail may also
rewrite its own endpoint file, and gains nothing by it — it would only break its own
connection.

Concretely:

- **The endpoint file is a credential.** It is written `0600` into the jail's own per-jail
  directory, which is created `0700`; a daemon refuses to publish into a directory that
  is group- or world-accessible. Never copy one between jails, and never paste one into a
  log or a bug report.
- **The TLS private key never leaves the daemon's memory** — never written to disk, never
  mounted into a jail. A fresh certificate per daemon process is correct precisely because
  clients re-read the file on every dial.
- **There is no token environment variable, deliberately.** An env var is inherited by
  every child process a client spawns; a file is read at the moment of use by the one
  process that needs it.
- **Daemons must never trust request fields as argv material.**
  `Session.ExecAllowlisted` enforces this by construction: argv positions are validated
  against a server-owned allowlist before the subprocess runs.

> [!WARNING]
> **A socket's file mode is not the mechanism, and never was.** An older statement of this
> posture said the socket "is chmod 0600 and lives under the user's socket dir";
> measured in a live jail, of the three sockets then mounted one was `0777`, one `0755`
> and one `0600` inside a `0755` directory. The per-jail **mount** was doing the work.
> Under `loopback-tls` the endpoint file's mode genuinely is load-bearing, which is why it
> is asserted in code rather than claimed in prose.

## Writing a client from scratch

`cmd/yolo-ps` is the reference implementation.

> [!WARNING]
> **This is not implementable with `nc`.** Steps 2–4 need a TLS library. That is the honest
> cost of a transport that works on every platform: on the retired `unix-socket` transport
> a shell one-liner really was enough.

1. Read the path in `$YOLO_SERVICE_<NAME>_ENDPOINT`, then read **that file**. Split on
   whitespace into **exactly three** fields: `host:port`, base64 cert DER, token. Fewer or
   more is a malformed endpoint — in particular, do not read a two-field file as "no
   token"; it is a truncated or stale file and authenticates nothing.
2. Base64-decode field 2, parse the DER certificate, and put that one certificate into a
   fresh, otherwise-empty trust root pool.
3. TLS-dial `host:port` with that pool as the **only** roots and `yolo-host-service` as
   the expected server name. Do not disable verification; the dial target and the
   certificate name differ on purpose, so the server name is overridden, not the checking.
4. Write the token frame: a 4-byte big-endian length, then the token bytes. Then read one
   byte — `0x01` is authenticated, EOF is auth-rejected.
5. Write a 4-byte big-endian request length, then the JSON body.
6. Read the response: a 5-byte header `(stream_id:u8, length:u32)`, then `length` bytes,
   forwarded or captured by `stream_id`. `stream_id == 2` carries a 4-byte signed exit
   code and ends the response.
7. Close the connection.

Redo steps 1–4 on **every** connection. Caching the address, the certificate, or the
token is what re-reading exists to avoid.

## Writing a server from scratch

`internal/svcendpoint` is the reference implementation — `Listen` is the whole server
half, and yolo's own daemons layer this protocol on top of it through
`internal/hostservice`.

Two boundaries before the steps, both blunter than anything in the client section:

- **This is the UNSUPERVISED path.** Every security-critical property below — the endpoint
  file's mode, the publication directory's mode, the private key never touching disk, the
  constant-time token compare, the frame-length cap checked before allocation — is
  enforced by the daemon itself, and yolo cannot verify any of it from outside. A daemon
  on this path is trusted to the degree its author is.
- **A pack-shipped loophole may not implement this section.** Its manifest declares
  `host_daemon.publishes: "socket"`, the daemon binds a plain AF_UNIX socket at
  `{socket}`, and yolo runs the one audited implementation of everything below in front of
  it (`svcendpoint.ServeFront`). Behind the front, anything that can bind AF_UNIX and read
  a 4-byte length prefix works: Python, Node, Rust, a shell script with `socat`. The
  `nc`-era simplicity this document mourns above is restored on the *server* side. This
  section exists so the front can be understood and audited, not so a pack can opt out of
  it.

> [!WARNING]
> **By default the front does not propagate the client's EOF upstream.** A daemon that
> reads its request *to EOF* therefore works on a bare socket and hangs forever behind the
> front. Two ways out: read to the length prefix, or declare `request_end: "eof"` beside
> `publishes: "socket"` and the front half-closes the upstream socket when the request
> direction ends. The default stays `framed` because a length-prefixed protocol is
> self-delimiting and does not need the EOF.

The steps, in an order that is load-bearing — **publish last**, so a published file always
names a live listener:

1. **Verify the publication directory before anything else.** The endpoint path arrives
   substituted into your argv as `{endpoint}`. Its directory must be a real directory
   (`lstat`, not `stat` — a symlink is a refusal), owned by your uid, with no group or
   world permission bits. Fail closed on any mismatch: `mkdir -p` semantics succeed on an
   already-existing attacker-owned directory without changing its owner or mode, and
   publishing there hands your credential to whoever owns it.
2. **Bind `127.0.0.1:0`** and let the kernel assign the port. Loopback keeps you off the
   LAN; kernel assignment means there is no probe-then-rebind window in which another
   local process could squat the port. Never take the port as configuration — the address
   is published, not passed in.
3. **Mint a throwaway TLS certificate** whose CommonName and sole SAN are
   `yolo-host-service`, and whose **private key never leaves your process's memory** — not
   marshaled, not PEM-encoded, never written to disk. A fresh certificate per process is
   correct, not a compromise, because clients re-read the endpoint file on every dial.
   Serve TLS 1.2 or newer. Interop note: mark the self-signed leaf `CA:TRUE` —
   OpenSSL-family verifiers require the trust anchor to carry it even for a
   one-certificate chain.
4. **Mint the token: 32 bytes from a CSPRNG, rendered as exactly 64 lowercase hex
   characters.** No other format — see the couplings below for what anything else costs.
5. **Publish after a successful bind**: one line —
   `<advertised-host:port> <base64 cert DER> <token>\n`, three whitespace-separated fields
   — written to a temp file in the **same** directory, chmodded to owner-only, then renamed
   onto `{endpoint}`. Atomic-rename because clients re-read the file on every dial, and a
   torn read hands them a truncated token. The advertised host is
   `$YOLO_SVC_ADVERTISE_HOST` when set and non-empty, else the container runtime's gateway
   name: bind loopback, advertise the name the jail resolves — reverse the two and the
   jail dials its own loopback.
6. **Authenticate every connection before reading anything else.** Read a 4-byte
   big-endian length; **check it against a cap before allocating** — without the cap a
   garbage prefix from an unauthenticated caller allocates gigabytes pre-auth; read that
   many bytes; compare against the token **in constant time** (`crypto/subtle` or your
   language's equivalent, never `==`). On success write the ack byte and clear the read
   deadline; on any failure write **nothing** and close. Authenticate each connection
   concurrently — one stalled pre-auth connection must not block the rest for the
   handshake timeout.
7. **Then speak the framed protocol** ([Request](#request) onward).
8. **On shutdown, unlink the endpoint file** — retiring the listener retires its credential
   in the same step. Republishing (rewrite + rename) is how rotation works: the next dial
   picks it up, no restart on either side.

### Three couplings enforced by yolo's health code, not by the wire

Get one wrong and a conforming-looking daemon dies with a misleading symptom.

- **The token must be exactly 64 lowercase hex characters** (`svcendpoint.IsToken`).
  Publish anything else — base64, uppercase hex, 16 bytes — and the endpoint file *parses*
  fine, but `Probe` returns false, the readiness wait times out, and the daemon is
  **SIGKILLed** with a log that looks perfectly healthy: it bound, minted, published, and
  was then killed without a word.
- **`Probe` — a content check, never file existence — is THE health predicate**, at startup
  readiness and everywhere else. It re-parses the published file on every call: exactly
  three fields, a splittable `host:port`, a certificate that parses, a well-formed token. A
  file that merely *exists* proves nothing and is treated as proving nothing; never design
  a health story around existence.
- **`yolo check` dials `127.0.0.1` at the published port** (`svcendpoint.DialLocal` keeps
  the port and substitutes the loopback address, because the host generally cannot resolve
  the advertised gateway name). The listener must therefore accept on loopback regardless
  of what it advertised — which step 2 gives you for free, and any deviation from step 2
  takes away.

## Current values

Verified at `a3922298`. The prose above explains what each of these is for; this table is
the only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Protocol version | `1` | `frameproto.ProtocolVersion` |
| Stream ids | stdout `0`, stderr `1`, exit `2` | `frameproto.StreamStdout`/`StreamStderr`/`StreamExit` |
| Auth ack byte | `0x01` | `internal/svcendpoint/token.go` |
| Token entropy / rendering | 32 CSPRNG bytes → 64 lowercase hex | `internal/svcendpoint/token.go` |
| Token frame cap (checked before allocation) | 4096 bytes | `internal/svcendpoint/token.go` |
| Pre-request handshake deadline | 5s | `internal/svcendpoint/token.go` |
| Daemon readiness wait before SIGKILL | 5s | `internal/cli/run` (`serviceReadyTimeoutDefault`) |
| Endpoint file mode / directory mode | owner-only file in an owner-only directory | `internal/svcendpoint/endpointfile.go` |
| Endpoint file extension | `.endpoint` | `paths.ServiceEndpointExt` |
| Endpoint env var | `YOLO_SERVICE_<NAME>_ENDPOINT` | `paths.ServiceEnvVarPrefix` / `ServiceEnvVarSuffix` |
| Advertised-host override | `YOLO_SVC_ADVERTISE_HOST` | `svcendpoint.AdvertiseHostEnv` |
| Jail-side services directory | `/run/yolo-services` | `paths.JailHostServicesDir` |
| TLS server name the client verifies | `yolo-host-service` | `internal/svcendpoint/cert.go` |
| Tier-1 log cap / archived generations | 4 MiB active, exactly one archive | `crossaudit.MaxBytes`, `crossaudit.ArchiveSuffix` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo. Ids are the ones cited from
sibling docs and code comments.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="oq-t1"></a>[`OQ-T1`](#oq-t1) | A bearer token, not mTLS | There is one listener per (jail, service), so a matching token identifies the caller structurally — no map, no lookup, no subject field. mTLS would add a second certificate lifecycle and, conventionally, a second CA, in exchange for nothing. What would reopen it: a single **shared** listener serving many jails. |
| <a id="oq-t7"></a>[`OQ-T7`](#oq-t7) | The token is delivered in the published endpoint file, and there is no env-var fallback | An env var is inherited by every child a daemon spawns; a file is read at the moment of use. A fallback would keep that inheritance alive for whatever reads it first. It also gives rotation for free, since the file is already re-read on every dial. |
| <a id="oq-t5"></a>[`OQ-T5`](#oq-t5) | A jail may rewrite its own endpoint file, and this is not a defect | It already holds its own token, and redirecting its own endpoint only breaks its own connection. A sibling cannot reach it — separate per-jail mounts. |

Two further constraints have no id and are worth stating in the same register.
**The ack byte was added to the wire deliberately**: without it a token mismatch is a
post-accept drop that reaches a client as EOF-before-exit-frame and gets reported as a
failure of whatever sits behind the transport. And **the endpoint env var is
`_ENDPOINT`, with no `_SOCKET` dual emission**: a stale baked client reading an *absent*
variable hits its own clear "not wired up in this jail" path, where one reading a
same-named variable whose value is no longer a socket would dial a regular file and report
something obscure.
