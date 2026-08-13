# The loophole transport — how a jail reaches a host service, and why the unix socket is not enough

**Status:** DESIGN, 2026-08-12. Written because [PR #32](https://github.com/mschulkind-oss/yolo-jail/pull/32)
solves a problem that is **not specific to the OAuth broker**, and standardizing its answer needs a
document rather than a merge. Leans heavily on that PR, which is the working implementation of
most of what is proposed here.

**The question this exists to answer:** the loophole protocol says a client connects to a Unix
socket. On macOS + podman that does not work. Is the fix a broker patch, or is it the loophole
framework's transport?

**Reads with:** [`loophole-protocol.md`](loophole-protocol.md) (the wire format — unchanged by
anything here), [`../guides/loopholes.md`](../guides/loopholes.md) (the three shipped loopholes),
[`boundary-broker.md`](boundary-broker.md) §10.3 (the client-auth convergence),
[`../plans/outstanding-work.md`](../plans/outstanding-work.md) Thread C.

---

## 1. What the transport is today

A loophole is a host daemon plus a Unix socket the jail can `connect()`:

- the host daemon listens on a socket inside a per-jail directory,
  `/tmp/yolo-host-services-<8hex>/` (`hostServiceSocketsDir`, keyed by container name),
- that directory is bind-mounted into the jail at `/run/yolo-services/`,
- the jail finds each socket by a `YOLO_SERVICE_<NAME>_SOCKET` env var,
- the wire format is `internal/frameproto`: a 4-byte big-endian length prefix and a JSON body.

**The trust model is one sentence, and it is load-bearing** — from
[`loophole-protocol.md`](loophole-protocol.md) §Security posture:

> *"The socket file is the authentication. A daemon trusts whoever can `connect()` — which is the
> jail (and anything else running as the same user on the host)."*

Everything below is about what happens when that sentence stops being true — either because
`connect()` cannot work (§2) or because "whoever can connect" stops being a small set (§4).

---

## 2. The macOS problem is a property of the boundary, not of the broker

On macOS + podman, containers run inside a podman-machine VM, and the per-jail directory crosses
that boundary over **virtiofs**. Virtiofs shares the socket's **inode** but not its connection
endpoint: in the jail the socket appears as an unstattable `s?????????` entry and `connect()`
fails.

For the OAuth broker the symptom is total — every `platform.claude.com` request 502s and Claude
Code will not start ([#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)).

**Nothing about that is broker-specific.** Any loophole reached by a bind-mounted Unix socket on
macOS + podman fails the same way, for the same reason. The repo already knows this in one place —
`docs/guides/macos.md:575` tells users to use `host.containers.internal` *"instead of Unix domain
sockets (virtiofs doesn't…)"* — but the loophole framework never absorbed it.

### 2.1 All three shipped loopholes are affected — checked, not assumed

An earlier draft said `audio` and `host-processes` were *"unaffected or differently wired"*. **That
was a guess and it was wrong in both cases**, in two different ways. Read from the manifests
2026-08-12:

| Loophole | `transport` | macOS + podman |
|---|---|---|
| `claude-oauth-broker` | `tls-intercept` | 🔴 **broken** — the relay hop is a Unix socket; this is #31 |
| `host-processes` | `unix-socket` | 🔴 **broken the same way** — same transport, same virtiofs failure |
| `audio` | `none` | ⚪ **inapplicable** — but for a reason, not by luck |

**`host-processes` has exactly the broker's problem.** Its manifest declares
`"transport": "unix-socket"` and a spawned host daemon reached at `{socket}`. Nothing about #31 is
broker-specific; `yolo-ps` fails identically on macOS + podman. It has simply never been the thing
that blocks a jail from starting, so nobody noticed.

**`audio` would break too — it is saved by a predicate, not by its design.** `transport: "none"`
means no loophole daemon, but its `host_bind_mounts` pass through
`${XDG_RUNTIME_DIR}/pulse/native` and `${XDG_RUNTIME_DIR}/pipewire-0`, **which are Unix sockets**.
Bind-mounted across virtiofs they would fail exactly as the relay socket does. What prevents the
failure is `requires: {file_exists: "${XDG_RUNTIME_DIR}/pulse/native"}` — macOS has no
PipeWire/PulseAudio, the predicate is false, and the loophole never activates. So the honest
reading is **inapplicable on macOS**, not unaffected; anyone running PulseAudio on a Mac would meet
the same wall.

**So today's honest statement:** the Unix-socket assumption is baked into the loophole framework,
and **every shipped loophole that actually uses a socket is broken on macOS + podman.** The broker
is not the special case — it is the one whose failure is loud, because it must work before the
agent starts.

### 2.2 The framework already has a transport field

Worth stating plainly because it makes §4 much smaller than it first looks:
`internal/loopholes/loopholes.go:27` already declares
`validTransports = []string{"tls-intercept", "unix-socket", "none"}`, and `runtime.go:37` already
switches on it per platform (Apple Container skips `tls-intercept`).

**So this is not "add a transport concept to manifests" — it is "add a fourth value."** The
vocabulary, the validation, and the per-platform switch all exist.

This is why the answer belongs in the framework. Every host service the plans call for —
**B1** (the crossing audit log), **B1b** (the git credential proxy), **B2** (approval-gated
credentials) — is a socket-reached host daemon, and each would rediscover #31 on a Mac.

---

## 3. What PR #32 does, and why the shape is right

For the `tcp-publish` case the relay:

1. **binds `127.0.0.1:0` itself** — kernel-assigned, so there is no probe-then-rebind race;
2. serves **TLS with an ephemeral self-signed cert whose private key is host-only** — in memory,
   never persisted, never mounted into a jail;
3. **publishes the public cert plus `host:port`** to a file in the jail's mounted host-services
   dir;
4. **verifies** a **pre-shared per-jail bearer token** carried in the connection's leading frame
   (4-byte BE length + bytes, `subtle.ConstantTimeCompare`, dropped on mismatch);
5. splices to the relay's own Unix socket, leaving the relay core (the `jail_id` stamp, the
   per-connection broker dial, the failure semantics) untouched.

The terminator, for a `tcpfile:` address, re-reads the endpoint file **fresh on every dial** (so a
restarted relay is picked up without relaunching the jail) and TLS-dials trusting **only** that
cert, via a dedicated root pool.

> **The token is never ISSUED over the connection — only verified.** An earlier draft said the
> token is "sent inside TLS", which reads as *obtained by connecting*. It is not: connecting gets
> you nothing. Verified in the diff:
>
> - the **run pipeline** generates the token and persists it host-side and user-private at
>   `~/.local/share/yolo-jail/broker-relay-tokens/<hash>.token`;
> - the **relay** receives it as `--token-file <path>` — a *path* on argv, deliberately, "so the
>   secret isn't visible in process listings";
> - the **terminator** (in-jail) reads it from the env var
>   `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_TOKEN` (`paths.BrokerTokenEnv`);
> - the **published endpoint file carries no secret** — only `"<host:port> <base64 cert>"`, with an
>   explicit comment in the PR that the cert is public and "the per-jail token guards access".
>
> So it is a **pre-shared secret injected into both ends out of band, before any connection
> exists.** Guessing or scanning the port yields a TLS handshake and then a dropped connection.

**Why each piece is load-bearing**, because a simpler version is tempting and wrong:

| Piece | What breaks without it |
|---|---|
| loopback bind | the port is on the podman-machine bridge, i.e. the LAN |
| TLS | a sibling jail on the shared bridge can sniff the hop |
| pinning a **host-only** key | a sibling can impersonate the relay — and see §5, the broker CA cannot be used for this |
| per-jail bearer token | loopback TCP has no `connect()`-implies-authorized property; the socket-file model does not survive the port |
| re-read on every dial | a relay restart otherwise strands the jail until relaunch |

### 3.1 The threat model, spelled out — who the token is actually against

The token is easy to misread, so here is the whole picture.

**The topology.** The relay is a **host** process, one per jail, binding loopback TCP. The jail
reaches it across the podman-machine boundary. Per #32's own security section, **that hop is a
shared bridge on which every jail holds `NET_RAW`/`NET_ADMIN`** — so jails are not isolated from
each other at the network layer.

**The adversary is a SIBLING JAIL. It is not the jail's own agent.**

That distinction answers the obvious objection: *yes*, anything inside jail A can read jail A's
token — it arrives in the terminator's environment, and the agent runs as UID 0, so
`/proc/<pid>/environ` is readable. **That is expected and harmless.** The loophole model has always
trusted the jail; [`loophole-protocol.md`](loophole-protocol.md) says so outright — *"a daemon
trusts whoever can `connect()`"*. Jail A using jail A's credential to talk to jail A's relay is the
system working.

What breaks without a token is **jail B talking to jail A's relay**. A Unix socket made that
impossible by construction: the socket lived in a per-jail mounted directory, so only that jail's
mount namespace could reach it. A **shared-bridge TCP port has no such property** — the port is
kernel-assigned but scannable, and reachability is no longer proof of identity. The token restores
what the filesystem used to provide.

| Control | Adversary it stops |
|---|---|
| loopback bind | anything on the LAN |
| TLS | a sibling **sniffing** the bridge (they hold `NET_RAW`) |
| pinned host-only-key cert | a sibling **impersonating the relay** — and see §5: the broker CA cannot do this job |
| **per-jail token** | a sibling **impersonating the jail** to its relay |
| re-read on every dial | not security — a relay restart otherwise strands the jail |

### 3.2 Where should the token be delivered — env, or the published file?

Raised in review: *"we need to issue a token in the file we write inside the jail and verify
that."* The premise it came from (that connecting yields a token) is not what #32 does — §3 —
**but the underlying proposal is a real improvement, for reasons other than the one that prompted
it.**

**Security-wise the two channels are a wash.** Everything that can read one can read the other:

| Reader | env var | file in the jail's mount |
|---|---|---|
| the jail's own agent (UID 0) | yes — `/proc/<pid>/environ` | yes |
| a sibling jail | no — per-jail | no — per-jail mount |
| a same-user host process | yes (host-side token file) | yes (host-side token file) |

So neither choice changes the threat model in §3.1. The differences are operational, and two of
them favour the file:

1. **Env is inherited; a file is not.** `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_TOKEN` propagates to
   every child process the terminator spawns, and onward. A file is read at the moment of use by
   the one process that needs it. That is a genuine reduction of exposure *inside* the jail, and it
   is the same reasoning that already put the token on `--token-file` instead of argv host-side —
   applied consistently to the other end.
2. **Rotation works for free.** The endpoint file is already re-read **fresh on every dial**,
   precisely so a restarted relay is picked up without relaunching the jail. A token delivered the
   same way inherits that property. Delivered by env, a rotated token needs a terminator restart —
   so today the address can rotate live and the credential cannot, which is an asymmetry with no
   design reason behind it.
3. **One connection, one lifetime, one place.** Address, cert, and credential are three parameters
   of the same hop. Splitting them across two channels means they can desynchronise.

**The argument against, which is the one #32 made deliberately:** the published file is currently
*designed* to hold no secret, so its mode and mount posture do not matter. Putting a token in it
makes it secret-bearing, and its permissions become load-bearing — a real cost, though a small and
well-understood one.

**A third option avoids both horns:** deliver the token in a *separate* host-only file
bind-mounted read-only into the jail. That buys the no-inheritance and rotation properties without
making the endpoint file secret-bearing — at the cost of one more mount.

**DECIDED 2026-08-13: the published endpoint file.** The token joins `host:port` and the cert in the
one file the terminator already re-reads on every dial. Consequences to implement deliberately:

- the file **becomes secret-bearing**, so its mode and its mount posture are now load-bearing where
  before they were not. It must be written `0600`, and it must land in the *per-jail* host-services
  dir it already uses — never a shared one;
- the endpoint file's own comment in `brokerrelay` currently says "no field is a secret — the token
  guards access and the cert is public." **That comment becomes false and must change with the
  code**, or the next reader will reasonably treat the file as publishable;
- **`YOLO_SERVICE_CLAUDE_OAUTH_BROKER_TOKEN` goes away.** Leaving it as a fallback would keep the
  inheritance problem alive for anything that reads it first, so the env path is removed rather
  than deprecated;
- rotation now works: the relay rewrites the file, the terminator picks it up on the next dial, no
  restart. That is the property env could not give.

This does not block landing #32 — it is a local change at both ends, and #32's own re-read-per-dial
design is what makes it cheap.

### 3.3 Drop the Unix socket and unify on loopback TCP? — the security argument WITHDRAWN

Asked in review: *"is there a good argument for keeping the socket at all? it's not like TCP is
expensive for loopback and it would unify designs."* An earlier draft of this section answered
"keep both" and called `SO_PEERCRED` the **decisive** argument. **That was wrong, and the review's
follow-up is what showed it:**

> *"On the host, anything as your user can read creds on disk or act as you. We want that in the
> jail to extend to anything as your user can use privs granted to this jail — so why do we need
> this extra UID checking? Make a file only readable by your user."*

Three reasons that lands, and the third is fatal to the old argument:

**1. The same-user set is the INTENDED boundary, not a gap.** The product deliberately matches the
host: anything running as you can act as you. So "a same-user host process can use this jail's
privileges" is the *specification*, not a weakness. Calling it "the widest weakness in the loophole
model" — as §4.1 did — imported a threat model the product does not claim.

**2. A `0600` file is the boundary, and the token is exactly that for a transport with no file.**
This is the cleanest framing and it comes from the review: file permissions are how you say
"only my user" on a path; a pre-shared token is how you say the same thing on a port. The token is
not extra security machinery layered on top — **it is the file-permission equivalent.** #32 already
stores it `0600` and host-only.

**3. `SO_PEERCRED` cannot distinguish the jail from a same-user host process anyway.** Rootless
podman maps the container's UID 0 to the invoking user's uid (and `--userns host` on the nested
path makes it literal), so both connections arrive at the host socket carrying **the same uid**.
There is nothing for the kernel to tell apart. The repo's own protocol doc has said so all along —
*"a daemon trusts whoever can `connect()` — which is the jail (and anything else running as the
same user on the host)"* — which is only true **because** peer credentials cannot separate them.
So the old §3.3 cited as decisive a mechanism that answers a question with one possible answer.

**What actually survives for keeping the socket, having dropped the security claim:**

| Surviving argument | Weight |
|---|---|
| **Fewer moving parts on Linux.** A path that exists or does not, versus port + TLS + ephemeral cert + publication + token issuance/distribution/verification + endpoint staleness + a dial-ordering constraint the terminator must handle. | Real. This is complexity and failure modes, not security. |
| **No listening port where none exists today**, on the platform that is 100% of current use. | Modest — the token makes reachability insufficient, so it is defense-in-depth, not a boundary. |
| **Peer credentials become meaningful on a SEPARATE-USER backend.** macos-user runs the sandbox as its own `SandboxUser`, and a future `guest` with `PrimSeparateUser` would too. There the jail genuinely *is* a different uid, so the socket can enforce a boundary **tighter than the host baseline**. | Narrow and forward-looking. Not available today: the broker is unwired on macos-user and `guest` is unbuilt. And a tighter-than-host boundary is a bigger product claim than the one above. |

**Revised read: I now lean toward unifying on `loopback-tls`,** which is the review's position. The
argument I was defending has been withdrawn, and what is left — "do not add machinery where a path
already works" — is a legitimate engineering preference but not a reason to carry two security
models that can drift apart, which was the review's original point and stands.

**The one thing unifying forfeits** is row 3 above: on a separate-user notch, a socket could assert
"only the sandbox uid may connect," which no token can. That is a capability for a backend that
does not work yet, and it can be reintroduced for that notch alone if `guest` ever wants it — so it
should not decide the default now.

**This is now an engineering/complexity call rather than a security one, so it is a decision rather
than a finding: OQ-T9.**

## 4. The generalization, and the trust-model upgrade hiding inside it

**Proposal: `loopback-tls` becomes a fourth value of the transport field the framework already
has** (§2.2), owned by the framework rather than by `brokerrelay`.

The framework owns endpoint publication, cert pinning, and token issuance. Daemons keep speaking
`frameproto` and never learn which transport carried the bytes. The wire protocol in
[`loophole-protocol.md`](loophole-protocol.md) is **unchanged**; this is the layer beneath it.

**`host-processes` is the second consumer and it already exists** (§2.1) — it is `unix-socket`
today and broken on macOS for the same reason. So the "wait for a second consumer" test in §6 is
already satisfied; it just has not been noticed, because `yolo-ps` failing is quiet where the
broker failing is not.

Selection should be **automatic by platform, overridable by config**: `loopback-tls` where a Unix
socket cannot cross (macOS + podman), `unix` elsewhere. A user should not have to know what
virtiofs is to run a loophole on a Mac.

### 4.1 Per-jail client secrets, everywhere

The upgrade worth taking beyond macOS: **give every jail a named client secret and require it on
every request, on both transports.**

Today's *"the socket file is the authentication"* means a daemon trusts anything running as the
same user on the host — a browser extension, an unrelated npm postinstall, another jail.

> **Reframed 2026-08-13 (see §3.3).** An earlier draft called this "the weakest claim in the
> loophole design". It is not a weakness — it is the **specification**, chosen to match the host,
> where anything running as you can already read your credentials or act as you. The jail extends
> that unchanged: anything as your user may use privileges granted to this jail. So there is no gap
> here to close, and a mechanism that "closes" it would be making a tighter claim than the product
> makes.

> **Correction to an earlier draft, which said per-jail secrets fix that weakness. They do not.**
> Per §3.1, a same-user host process can read the host-side token file exactly as the relay does,
> so the "anything running as the same user" gap **survives** this change untouched. Claiming
> otherwise would be precisely the overclaim [`boundary-broker.md`](boundary-broker.md) §6.2 warns
> against — manufacturing the appearance of a boundary.
>
> **What per-jail secrets actually buy, and it is still worth having:**
> 1. **Sibling-jail isolation on a shared transport** — the property a per-jail-mounted Unix socket
>    gave by construction and a shared TCP port destroys. This is the real win, and on macOS it is
>    not optional.
> 2. **Verifiable attribution** — *which* jail made this request, checked rather than self-reported.
>
> Fixing the same-user gap needs a different mechanism (peer credentials the host can attest, or
> not co-locating the daemon with untrusted same-user code) and is out of scope here.

**Three independent designs have now arrived at the same fix:**

1. **PR #32** — a per-jail bearer token, because TCP forced the question.
2. **unYOLO** ([`boundary-broker.md`](boundary-broker.md) §10.3) — *"the caller presents a named
   broker-client secret before the broker accepts a request"*, with operators on a **separate**
   listener holding **distinct** credentials.
3. **This repo's own attribution work** — the per-jail relay already injects `jail_id` host-side
   *precisely because* an in-jail self-report is not trustworthy. A per-jail secret is that same
   insight applied to authorization rather than logging.

Three arrivals is enough evidence. And the cost is low: the relay is already per-jail, so there is
already a place to put a per-jail secret.

**It must be issued host-side and never live in a jail-writable path.** #32 gets this right — the
token is generated into a host-only file and passed via `--token-file`, kept **off argv** so it
does not appear in `ps`. That detail is not incidental; `env_sources` secrets on macos-user are
visible in `ps` today, and this is the same mistake one layer down.

---

## 5. The `ca.key` defect belongs in this doc

> **FIXED 2026-08-12** — per §5.1, via a `state_files` key on the loophole manifest. The shipped
> broker declares `["ca.crt", "server.crt", "server.key"]`, so those three files each cross on
> their own `:ro` mount and the state **directory** no longer crosses at all. The section below is
> kept as the analysis; OQ-T6 records why the narrow form was chosen.

[Issue #33](https://github.com/mschulkind-oss/yolo-jail/issues/33), open, no PR — and it is
inseparable from §3, because it is *why* #32 pins its own cert instead of reusing the broker CA.

**Measured from inside a live jail, 2026-08-12:**

```
$ ls -l /var/lib/yolo-jail/loopholes/claude-oauth-broker/
-rw-r--r--  ca.crt   -rw-------  ca.key   -rw-r--r--  ca.srl
-rw-r--r--  leaf.cnf -rw-r--r--  refresh.lock
-rw-r--r--  server.crt  -rw-------  server.key
$ head -c 28 …/ca.key  →  -----BEGIN PRIVATE KEY-----     (3268 bytes, readable)
```

The **entire loophole state dir** crosses the boundary `:ro`, so the CA's private key is inside
every jail.

**`0600` is not a mitigation.** A yolo jail runs its agent as UID 0 by design (Claude YOLO is
`--dangerously-skip-permissions` plus `IS_SANDBOX=1`, which exists to bypass the UID-0 refusal), so
owner-only permissions are no barrier — the read above is the proof.

### 5.0 How bad is it, actually? — a correction

**Challenged in review: *"was it really even an issue? you can MITM and steal data, but it doesn't
escalate auth at all."* That is substantially correct, and an earlier draft of this section
oversold it** (it called this "the most serious item", "a live privilege-boundary failure", and
argued it should jump ahead of #37). Working it through properly:

**A MITM needs two things: a trusted certificate *and* the ability to redirect the victim's
traffic. `ca.key` supplies only the first.** The second is a separate capability the attacker must
obtain independently.

| Scenario | Does `ca.key` help? |
|---|---|
| **Attacker in jail A, victim = jail A** | **No.** A UID-0 process already owns that jail — it can read credential files directly, `LD_PRELOAD`, patch binaries, read `/proc/*/environ`. Minting a cert is more work for less. |
| **Attacker in jail A, victim = sibling jail B** | **Only with traffic redirection too** (ARP spoof on a shared bridge, DNS control). Given that, yes — forge any host, jail B trusts it. |
| **Escalate to the host** | **No.** The CA signs certs; it is not a credential for anything upstream. |
| **Escalate to the broker** | **No.** Broker authorization is the socket (Linux) or the pre-shared token (§3). #32 deliberately does not use this CA. |

**And for the headline case it yields nothing new:** the Claude credential is what flows over
`platform.claude.com`, and `.claude-shared-credentials` is **machine-global and already mounted into
every jail**. Jail A does not need to MITM jail B to read it.

**So the accurate framing:** not an auth-escalation bug. A **lateral-movement / defense-in-depth**
issue, gated behind a second capability, whose realistic yield is cross-jail theft of *per-workspace*
secrets (copilot's `gho_` token, a workspace `.env`) that the attacker does not otherwise hold.

### 5.0.1 One thing that is worse than assumed

The trust surface is **not** Node-only. `internal/entrypoint/system.go` merges every
`NODE_EXTRA_CA_CERTS` path into `$HOME/.yolo-ca-bundle.crt` and points `SSL_CERT_FILE`,
`CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, and `REQUESTS_CA_BUNDLE` at it. **Verified in a live jail
2026-08-12:** all four env vars point at that bundle, and the broker CA's PEM body is present among
its 122 certificates.

So a forged leaf is trusted by **curl, git, python-requests and Node alike** — essentially every
TLS client in the jail, not just Node ones. That widens *case 2*'s blast radius without changing
the precondition that gates it.

### 5.0.2 What this changes about priority

**#37 should go first after all.** It is a *certain, already-occurring* correctness bug in the
tool nested-jail verification depends on. This is a *conditional* lateral-movement issue requiring
a capability the attacker must separately obtain. On expected impact, #37 wins.

**Still fix this, for reasons that survive the downgrade:** it is free (three files instead of a
directory), it is least-privilege with no argument for the status quo, it removes a precondition so
that §3's pinning defends a narrower attacker, and it is publicly filed — which carries weight
independent of severity.

**A structural alternative worth naming:** the reason *case 2* exists at all is that **one CA is
shared by every jail** (the state dir is machine-global). Per-jail CAs would make `ca.key` exposure
self-only, and case 2 would vanish regardless of routing. That is a larger change than the mount
fix and probably not worth it alone — but if the CA is ever regenerated for another reason, it is
the better shape.

### 5.1 The fix

**Three files are needed in-jail; `ca.key` is not one of them.** Verified 2026-08-12:

| File | Needed in-jail? | Why |
|---|---|---|
| `ca.crt` | **yes** | `NODE_EXTRA_CA_CERTS` points at it — measured: `/var/lib/yolo-jail/loopholes/claude-oauth-broker/ca.crt` |
| `server.crt`, `server.key` | **yes** | the in-jail TLS terminator serves with them |
| **`ca.key`** | **NO** | read only by `caKey()` in `internal/oauthbroker/cert.go`, host-side signing |
| `ca.srl`, `leaf.cnf`, `refresh.lock` | no | host-side CA bookkeeping and the refresh flock |

So the mount narrows from the state **directory** to **three files**.

> **Correction to an earlier draft**, which said "only `server.crt` and `server.key`". `ca.crt` is
> required in-jail — dropping it would break `NODE_EXTRA_CA_CERTS` and therefore every in-jail TLS
> verification of the terminator. The distinction that matters is **public vs private**, not
> **ca vs server**.

**There is already a declaration to build on.** The loophole manifest names the CA cert explicitly
— `"ca_cert": "{state}/ca.crt"` — so the framework already knows which single file is the public
CA. What it did not have is a way to say *"mount these files, not the state dir"*. See **OQ-T6**.

**What shipped** (`internal/loopholes`, 2026-08-12): an OPTIONAL `state_files` list of paths
relative to the state dir. Present → each named file crosses on its own `:ro` mount and the state
directory does not; absent → the whole dir, exactly as before, so no external manifest changed
meaning. Two details worth keeping:

- **a missing entry is skipped, not mounted.** The container runtime materializes a missing bind
  source as an empty *directory*, which would shadow the very file the jail daemon waits for. The
  ordering makes this rare — `brokerEnsure` runs before the argv is assembled and the broker's
  `EnsureCAAndLeaf` completes before its socket binds — but "rare" is not "never".
- **entries are validated at load time** as relative, `..`-free paths, so the key can only narrow
  the mount it describes and never reach outside it.

This is a **mount-scope change, not a redesign**, and it should land before or with #32 — not
after, because #32's security argument explicitly assumes the CA is untrustworthy and a reader who
merges #32 alone may reasonably conclude the CA problem was handled.

**It also removes a rung from the ladder**: with the CA private key gone from the jail, pinning in
§3 defends against a narrower and more plausible attacker, and `NODE_EXTRA_CA_CERTS` stops being a
liability.

### 5.2 Why it was not acted on — and what else was not

Fair question, and the answer is not flattering: **the audit that found it produced findings, not
work items.** `ROADMAP.md` §4d recorded four verified defects on ~2026-08-02 and none of them was
carried into the queue in [`../plans/outstanding-work.md`](../plans/outstanding-work.md), so
subsequent planning simply did not see them. The pack batch that followed was scoped from the
queue.

**Re-checked 2026-08-12 — all four are still unfixed:**

| §4d defect | State today | Evidence |
|---|---|---|
| **`ca.key` readable in-jail** | ✅ fixed 2026-08-12 | §5.1's `state_files` narrowing; verified in a nested jail — `ca.key` absent, the three needed files present |
| **Claude creds symlink dangles on macos-user** | 🔴 open | Thread B; blocks the Teams auth mode on macOS |
| **Config-approval snapshot is agent-writable** | 🔴 open | `.yolo/config-snapshot.json` is mode `664` and writable in-jail *(re-measured)*. An agent that edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt disappear — the exact bypass [`config-safety.md`](config-safety.md) exists to prevent |
| **Two shipped docs contradict the code** | ✅ fixed 2026-08-12 | `USER_GUIDE.md` and `bundled_loopholes/claude-oauth-broker/README.md` both said *"no background timer / no proactive refresh"* while `oauthbrokercmd.go:88` starts `RunBackgroundRefresher` by default. Both now describe the real loop (tick 60 s, lead 300 s, 5 s fast retry ×12, `--no-background-refresh`). The separate `--host-creds-file` staleness was already fixed |

**The process lesson, which matters more than the four items:** an audit whose output lives only in
a narrative doc is invisible to planning. Findings need to become rows in the queue on the day they
are found, or they age quietly until an outside contributor files one of them as a public issue —
which is exactly what happened here.

All four are now rows in the queue.

---

## 6. What I would build, in order

1. **Fix `ca.key`** (§5.1). Narrow, known, publicly filed, and a prerequisite for reading #32's
   security argument correctly.
2. **Land #32 as scoped**, answering its author's open question (§7, OQ-T1). It is green, it is
   `MERGEABLE`, and it unblocks Claude on macOS today. Generalizing first would strand a working
   fix behind a refactor.
3. **Generalize the transport** (§4) — as a **fourth value** of the existing field (§2.2), not a
   new concept. The "wait for a second consumer" test is **already met**: `host-processes` is
   `unix-socket` and broken on macOS today (§2.1). Porting it is the natural forcing function and
   the natural proof, because it is small and its failure is harmless.
4. **Per-jail client secrets on `loopback-tls`** (§4.1) — scoped down from "both transports" per
   OQ-T3, since on `unix-socket` the per-jail mount already provides the isolation and a token
   there buys only attribution.

Deliberately *not* first: the generalization. #32 is a working fix for a total outage on one
platform, and the framework question should not hold it hostage.

**Also worth filing, and not in any queue before this doc:** `host-processes` is silently broken on
macOS + podman. Nobody has reported it because `yolo-ps` failing is quiet, but it means the
`host-processes` loophole is Linux-only in practice while being advertised as available. Tracked as
**D4** in [`../plans/outstanding-work.md`](../plans/outstanding-work.md).

---

## 7. Decisions — settled, and the two that are not

Audited 2026-08-13 to leave **only** questions that genuinely need the maintainer. Everything else
is recorded as decided, with the reasoning, so nothing looks quietly dropped.

### 7.1 Settled

| Was | Answer | Why it did not need a ruling |
|---|---|---|
| **OQ-T2** transport selection: automatic, configured, or both? | **Automatic by platform, with an explicit config override.** | The "silent fallback nobody notices" objection is already answered by shipped code: `yolo loopholes list` prints `transport=` per loophole, so the active choice is visible without asking. A Mac user should not have to know what virtiofs is to run a loophole, and an override costs one key. |
| **OQ-T3** per-jail secrets on `unix-socket` too? | **No — `loopback-tls` only.** | §3.1 established they do not close the same-user gap, and §3.3 that the socket's per-jail mount already gives sibling isolation. On that path a token buys only attribution, at the cost of a new failure mode on a path that works. |
| **OQ-T4** does `macos-user` make this moot? | **No, not today.** | Factual, not a preference: it has no VM, but the broker is unwired there (`BrokerSocketGrantCommands`, zero call sites) and skills/briefings never reach that home at all (see Thread B). Re-ask if P7 lands. |
| **OQ-T5** is the endpoint file jail-writable, and does it matter? | **A jail can rewrite its own, and it gains nothing.** | It already holds its own token, and redirecting its own endpoint only breaks its own connection. A sibling cannot reach it — separate per-jail mounts. Now stated in §3.2 rather than left to inference. **This changes with §3.2's decision:** the file is secret-bearing, so it must be `0600` and per-jail — but the tamper analysis is unchanged. |
| **OQ-T6** per-file mounts: general or one-off? | **Narrow, shipped** as the `state_files` manifest key. | Done 2026-08-12. The general `mounts_into_jail` (default-nothing, whole surface, breaking) is folded into the §4 work, which it subsumes cleanly. |
| **OQ-T7** token delivery: env, endpoint file, or a separate file? | **The endpoint file** — decided by the maintainer 2026-08-13. | See §3.2 for the four consequences that must land with it, including deleting the env var rather than deprecating it. |
| **§3.3** drop the socket, unify on TCP? | **REOPENED as OQ-T9** — the security argument was withdrawn 2026-08-13. | I had called `SO_PEERCRED` decisive; it cannot distinguish the jail from a same-user host process (both arrive as the same uid under rootless podman), and the same-user set is the *intended* boundary rather than a gap. What is left is a complexity-vs-uniformity engineering call, which is a decision, not a finding. See §7.4. |

### 7.2 OQ-T1 — answer #32's author: token + pinned cert as proposed, or full mTLS?

**Status: waiting on you. An outside contributor asked this when he opened the PR and nobody has
replied.** His words:

> *"I keep the terminator↔relay hop on a per-jail token + pinned host-only-key TLS (no client
> certs). Happy to switch to full mTLS if you'd prefer."*

**What is actually being chosen.** Both options authenticate the client; they differ in what the
credential *is* and who manages its lifecycle.

| | as proposed (bearer token) | full mTLS |
|---|---|---|
| client credential | a random per-jail string | a per-jail client certificate |
| lifecycle | generate, write, delete with the jail | generate, sign, distribute, **expire**, re-issue, and handle clock skew |
| who signs | nobody | a CA — and §5 is the whole story of what having one costs us |
| revocation | delete the file | CRL/OCSP, or short expiry plus re-issue |
| identity strength | equal — both prove possession of a per-jail secret, neither is kernel-attested |

**Why I recommend "as proposed."** The hop is already loopback-bound and pinned to a host-only-key
cert, so mTLS's marginal gain is *client*-side proof — which a token already provides equally well,
since both are "possession of a per-jail secret." What mTLS adds is a second certificate lifecycle
and, if done conventionally, **a second CA** — and issue #33 is a live lesson in what a CA we own
costs when its key ends up somewhere it should not be. Paying that for no identity gain is the
wrong direction.

**What would change my mind:** if we ever want a daemon to make an authorization decision *based on
which jail is calling* rather than merely *that a valid jail is calling*, a certificate carries a
verifiable subject where a bearer token carries nothing but itself. That is the boundary-broker's
per-verb-policy world ([`boundary-broker.md`](boundary-broker.md) §5). It is not this feature, and
a token can be swapped for a cert later without changing the transport shape.

**Why it needs you and not me:** it is a reply to a contributor in your repo, and §4 makes it every
future host service's answer rather than one hop's — so it is a posture decision, not a code review
note.

### 7.3 OQ-T8 — does the generalization ship with #32, or after it?

**Status: waiting on you, and it is the live disagreement between us.** §6 recommends landing #32
as scoped and generalizing when a second consumer appears. You have suggested replacing it with the
open work instead.

**The case for merge-then-generalize:** #32 fixes a **total outage** — on macOS + podman every
`platform.claude.com` request 502s and Claude Code will not start. It is green, `MERGEABLE`, and
re-verified 2026-08-13 as still conflict-free against everything we pushed (including B-0's
restructuring of the same `run.go`). The generalization is unstarted work; replacing means macOS
stays broken for its duration, and 1064 tested lines get rewritten rather than relocated.
Generalizing *after* makes the move a refactor with tests already in place.

**The case for replace:** the generalization moves his transport out of `brokerrelay` into the
framework, so merging first guarantees churn on code that just landed — and §3.3's mitigation (1)
says the bolt-on placement is exactly what will let the TCP path drift. If the rewrite is happening
anyway, doing it once is cheaper than doing it twice.

**My read: merge first.** The churn is real but bounded and mechanical; the outage window is not.
And **the second consumer already exists** — `host-processes` is `unix-socket` and silently broken
on macOS the same way (row **D4**), so §6's "wait for a second consumer" test is already satisfied
and the generalization can start immediately after the merge rather than someday.

**Either way, tell him which.** Leaving a tested PR open with an unanswered question is the one
option with no upside.

### 7.4 OQ-T9 — one transport, or two?

**Status: waiting on you. The security argument for two has been withdrawn (§3.3), so this is now
purely an engineering call — and it is close.**

| | Unify on `loopback-tls` | Keep both |
|---|---|---|
| code paths | one, everywhere | two, plus a per-platform selector |
| security models | one | two — and **the review's original point stands: the less-used one drifts** |
| Linux (100% of current use) | gains a listening port + a TLS handshake per connection where it has neither | unchanged |
| moving parts | port, TLS, ephemeral cert, publication, token issue/distribute/verify, endpoint staleness, dial ordering | a filesystem path |
| Apple Container | **works** (host services are unavailable there today for the same virtiofs reason) | still unsupported unless `loopback-tls` is built anyway |
| separate-user notches (macos-user, future `guest`) | forfeits a socket-only capability: asserting "only the sandbox uid may connect", which is **tighter than the host baseline** | retains it |

**Note the asymmetry in the AC row:** `loopback-tls` has to be built either way — that is #32 — so
"keep both" does not save the work, only the migration of the two existing `unix-socket` consumers
(the broker relay and `host-processes`).

**My read, revised: unify.** Two security models that can drift is a worse long-term cost than a
loopback TLS handshake, and the thing I was protecting turned out to be a mechanism that answers a
question with only one possible answer. The migration is bounded — two consumers.

**What would change my mind:** if `guest` with a separate user becomes a near-term goal. There the
socket can make a genuinely tighter claim than "anything as your user", and that is the one place
peer credentials stop being vacuous. Today `guest` is unbuilt and the broker is unwired on
macos-user, so it should not decide the default.

**If you unify, two things must land with it** so the old path does not linger half-alive: migrate
`host-processes` (row **D4**, which is broken on macOS today and is therefore the natural first
port), and retire `unix-socket` from `validTransports` rather than leaving it accepted-but-unused.
