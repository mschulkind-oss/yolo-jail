# The loophole transport — how a jail reaches a host service, and why the unix socket is not enough

**Status:** DESIGN, 2026-08-12. Written because [PR #32](https://github.com/mschulkind-oss/yolo-jail/pull/32)
solves a problem that is **not specific to the OAuth broker**, and standardizing its answer needs a
document rather than a merge. Leans heavily on that PR, whose design is adopted almost wholesale —
though **#32 itself is being replaced by this work rather than merged** (§7.3), so treat it as the
spec and its test suite as the acceptance bar, not as code to relocate.

**The question this exists to answer:** the loophole protocol says a client connects to a Unix
socket. On macOS + podman that does not work. Is the fix a broker patch, or is it the loophole
framework's transport?

**Reads with:** [`loophole-protocol.md`](loophole-protocol.md) (the wire format — unchanged by
anything here), [`../guides/loopholes.md`](../guides/loopholes.md) (**five** shipped loopholes as of
2026-08-19 — this doc was written when there were three),
[`boundary-broker.md`](boundary-broker.md) §10.3 (the client-auth convergence),
[`../plans/roadmap.md`](../plans/roadmap.md) — the 🔒 rows on the fatal reachability witness
and the slirp4netns fallback. *(This used to say "Thread C"; the roadmap's lettered threads were
retired on 2026-08-17 and the name no longer resolves.)*

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

### 2.1 All three shipped loopholes are affected — checked, not assumed *(three was the count on 2026-08-12; it is five now — `audio`, `host-processes`, `journal`, `cgroup-delegate`, `claude-oauth-broker`, all pack-shipped)*

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

> **Correction, as built (§8):** the last clause overstates it. `runtime.go` does not "switch per
> platform" on the transport — it consults `Transport` in exactly ONE place, to skip
> `tls-intercept` loopholes on Apple Container, and that was the field's only behavioural reader in
> the whole repo. There was no existing per-platform transport switch to hang `loopback-tls` on;
> the *vocabulary* and the *validation* existed and the *mechanism* did not. That is why §4 turned
> out bigger than this section predicted, and why the one reader is now keyed on `intercepts`
> instead — see §8.

This is why the answer belongs in the framework. Every host service the plans call for —
**B1** (the crossing audit log), **B1b** (the git credential proxy), **B2** (approval-gated
credentials) — is a socket-reached host daemon, and each would rediscover #31 on a Mac.

---

## 3. What PR #32 does, and why the shape is right

### 3.0 What `loopback-tls` actually is, in plain terms

The name is doing too much work in this doc, so: **it is a TCP connection to `127.0.0.1` that
behaves like a `0600` Unix socket.** Five steps, and each one exists to replace something the
filesystem was giving us for free:

1. The **relay** (a host process, one per jail) opens a TCP listener on `127.0.0.1` on a
   kernel-assigned port. *Loopback, so it is not on the LAN.*
2. It mints a **throwaway TLS certificate whose private key never leaves that process's memory** —
   not written to disk, never mounted into a jail.
3. It writes **`host:port` plus the public certificate** into a file in the jail's own mounted
   directory. *This is the address book; a socket did not need one because the path was the name.*
4. The **jail** reads that file, dials the port, and demands the server present **exactly that
   certificate** — not "anything signed by a CA we trust", that specific one. *So a sibling cannot
   pretend to be the relay.*
5. The jail then sends a **pre-shared secret as the first bytes on the connection**. The relay
   compares it in constant time and hangs up on a mismatch. *This is the `0600`: reachability is
   not authorization, so possession of the secret is what "only my user" means on a port.*

After that the relay splices the connection into the plumbing that already existed, so the daemon
behind it never learns which transport carried the bytes.

**Why any of it:** on macOS + podman a Unix socket does not work at all (§2), and per
[`agent-credentials.md`](agent-credentials.md) §2.7 the same is true on Apple Container. TLS and
the pinned cert are there because the hop crosses a **shared** podman-machine bridge on which every
jail holds `NET_RAW` — so a sibling could otherwise read or impersonate it. On Linux loopback none
of that is load-bearing, which is why §3.3 had to argue about whether to use it there at all.


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

**DECIDED 2026-08-13: unify on `loopback-tls`.** `unix-socket` is retired as a transport. The
argument I was defending was withdrawn above, and what remained — "do not add machinery where a
path already works" — does not outweigh carrying two security models that drift apart, which was
the review's point from the start.

**The "forfeit" the previous draft named does not survive either**, and checking the presets is what
settled it. The claim was that a separate-user notch could use a socket to assert "only the sandbox
uid may connect", tighter than the host baseline. But:

- **Linux `guest` has no separate user by design.** `GuestProfileLinux()` is
  `PrimNamespaces + PrimLandlock`, and its own comment says *"NO separate user (bwrap uses the same
  namespace primitive podman does)"*. Landlock and bwrap confine **a process**, not a uid — so the
  review is exactly right that guest "can still do same user just as well". There is nothing here
  for peer credentials to check.
- **macOS `guest` does have one** — `GuestProfileMacOS()` is `PrimSeparateUser + PrimSeatbelt`, and
  `PrimSeparateUser`'s description calls it *"a separate OS user — its own home and keychain reach
  (a credential boundary)"*. So there the sandbox genuinely is a different uid.
- **But that does not want a socket.** Whatever the transport, the credential has to be reachable by
  that other uid — a `chgrp`/`chmod` grant on the token file, exactly as `git 84d0365` sketched
  `chgrp`/`chmod` on the socket. **Same problem, same cost, either way.** The distinction is that
  file permissions **restrict** while peer credentials only **verify** — and restriction is the part
  you actually need. Verification is attribution, which is nice for logs and is not a boundary.

So the separate-user case needs one extra grant step on macOS, not a second transport.

## 4. The generalization, and the trust-model upgrade hiding inside it

**Decided (§7.4): `loopback-tls` becomes the framework's ONLY transport**, owned by the framework
rather than by `brokerrelay`. `unix-socket` retires; `none` stays (it means "no daemon", not "a
different transport"). An earlier draft framed this as adding a fourth value to the field described
in §2.2 — the field and its per-platform switch still do the work, there is just one fewer value to
switch on when the migration finishes.

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

> **Superseded by §7.4's own decision, and no selection key shipped.** This paragraph predates
> "unify"; with one transport there is nothing to select and nothing to override, so an override
> could only mean "no daemon", which `enabled: false` already says. What OQ-T2's reasoning actually
> rested on — that the active choice is *visible* rather than silent — is preserved: `yolo loopholes
> list` still prints `transport=` per loophole. See §8.

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
work items.** `sequencing-2026-07.md` §4d recorded four verified defects on ~2026-08-02 and none of them was
carried into the queue in [`../plans/roadmap.md`](../plans/roadmap.md), so
subsequent planning simply did not see them. The pack batch that followed was scoped from the
queue.

**Re-checked 2026-08-12 — all four are still unfixed:**

| §4d defect | State today | Evidence |
|---|---|---|
| **`ca.key` readable in-jail** | ✅ fixed 2026-08-12 | §5.1's `state_files` narrowing; verified in a nested jail — `ca.key` absent, the three needed files present |
| **Claude creds symlink dangles on macos-user** | 🔴 open | Thread B; blocks the Teams auth mode on macOS |
| **Config-approval snapshot is agent-writable** | ✅ fixed 2026-08-18 | [`config-safety.md`](config-safety.md) OQ-D1: the approval record moved to `~/.local/share/yolo-jail/approvals/<container-name>.json`, host-side and never mounted, so there is no in-jail path to it at any mode. The workspace keeps only `config-assembled.json`, the host→jail delivery copy, whose integrity is not load-bearing |
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
2. **Build the unified `loopback-tls` transport in the framework, superseding #32** (§7.3). Not a
   merge: #32 is closed and its design is the spec (§7.3 lists what must be carried over, and its
   test suite is what to re-derive against). Until this ships, macOS + podman cannot run a jail —
   a deliberate cost, recorded in §7.3.
3. **Unify on `loopback-tls`** (§4, decided in §7.4) — no longer "add a fourth value" but
   **replace three with one**. `unix-socket` retires. `host-processes` is the first port and the
   proof, because it is broken on macOS today (row **D4**) and its failure is harmless; the broker
   relay follows. Then drop `unix-socket` from `validTransports`.
4. **Per-jail client secrets on `loopback-tls`** (§4.1) — scoped down from "both transports" per
   OQ-T3, since on `unix-socket` the per-jail mount already provides the isolation and a token
   there buys only attribution.

Deliberately *not* first: the generalization. #32 is a working fix for a total outage on one
platform, and the framework question should not hold it hostage.

**Also worth filing, and not in any queue before this doc:** `host-processes` is silently broken on
macOS + podman. Nobody has reported it because `yolo-ps` failing is quiet, but it means the
`host-processes` loophole is Linux-only in practice while being advertised as available. Tracked as
**D4** in [`../plans/roadmap.md`](../plans/roadmap.md).

---

## 7. Decisions — settled, and the two that are not

Audited 2026-08-13 to leave **only** questions that genuinely need the maintainer. Everything else
is recorded as decided, with the reasoning, so nothing looks quietly dropped.

### 7.1 Settled

**All of §7 is now settled.** OQ-T1 (§7.2) and OQ-T8 (§7.3) closed 2026-08-13; OQ-T9 (§7.4)
the same day. Nothing in this design is waiting on a decision — the remaining work is execution,
tracked as row **T1** in [`../plans/roadmap.md`](../plans/roadmap.md).

| Was | Answer | Why it did not need a ruling |
|---|---|---|
| **OQ-T2** transport selection: automatic, configured, or both? | **Automatic by platform, with an explicit config override.** | The "silent fallback nobody notices" objection is already answered by shipped code: `yolo loopholes list` prints `transport=` per loophole, so the active choice is visible without asking. A Mac user should not have to know what virtiofs is to run a loophole, and an override costs one key. |
| **OQ-T3** per-jail secrets on `unix-socket` too? | **No — `loopback-tls` only.** | §3.1 established they do not close the same-user gap, and §3.3 that the socket's per-jail mount already gives sibling isolation. On that path a token buys only attribution, at the cost of a new failure mode on a path that works. |
| **OQ-T4** does `macos-user` make this moot? | **No, not today.** | Factual, not a preference: it has no VM, but the broker is unwired there (the cross-uid grant, now `EndpointGrantCommands`, still has zero call sites) and skills/briefings never reach that home at all (see Thread B). Re-ask if P7 lands. |
| **OQ-T5** is the endpoint file jail-writable, and does it matter? | **A jail can rewrite its own, and it gains nothing.** | It already holds its own token, and redirecting its own endpoint only breaks its own connection. A sibling cannot reach it — separate per-jail mounts. Now stated in §3.2 rather than left to inference. **This changes with §3.2's decision:** the file is secret-bearing, so it must be `0600` and per-jail — but the tamper analysis is unchanged. |
| **OQ-T6** per-file mounts: general or one-off? | **Narrow, shipped** as the `state_files` manifest key. | Done 2026-08-12. The general `mounts_into_jail` (default-nothing, whole surface, breaking) is folded into the §4 work, which it subsumes cleanly. |
| **OQ-T7** token delivery: env, endpoint file, or a separate file? | **The endpoint file** — decided by the maintainer 2026-08-13. | See §3.2 for the four consequences that must land with it, including deleting the env var rather than deprecating it. |
| **§3.3 / OQ-T9** drop the socket, unify on TCP? | **DECIDED 2026-08-13: unify.** `unix-socket` retired. | I had called `SO_PEERCRED` decisive; it cannot distinguish the jail from a same-user host process (both arrive as the same uid under rootless podman), and the same-user set is the *intended* boundary rather than a gap. What is left is a complexity-vs-uniformity engineering call, which is a decision, not a finding. See §7.4. |

### 7.2 OQ-T1 — token or mTLS? — **SETTLED: token, and mTLS changes nothing**

Originally framed as a question for the maintainer, because #32's author asked it. **The review
closed it with one question — *"we mint a unique token per jail, how does mTLS change this?"* — and
the answer is that it does not.**

I had written that a certificate "carries a verifiable subject where a bearer token carries nothing
but itself". That is true of bearer tokens in general and **false of this architecture**, because
**there is one relay per jail**. The relay's token *is* its jail's token, so a matching token
identifies the caller structurally — no map, no lookup, no subject field. There is nothing for a
cert's subject to add.

Working through where a cert could still earn its place, all three come up empty here:

| Possible advantage of mTLS | Does it apply? |
|---|---|
| Identity is verifiable by a party that does not hold the secret | **No.** The only verifier is the per-jail relay, which owns the secret by construction. |
| The broker behind the relay could verify the jail itself instead of trusting the relay's `jail_id` stamp | **No.** The relay is a host-side process we run; it is already trusted. The stamp exists to distrust the *jail's* self-report, not the relay's. |
| Revocation at scale | **Worse.** Jails are ephemeral; deleting a file is revocation. Certs would need expiry, re-issue, and clock-skew handling. |

**So: token, and this is settled rather than preferred.** mTLS would add a second certificate
lifecycle and, conventionally, a second CA — with issue #33 as a live lesson in what a CA we own
costs — in exchange for nothing. The earlier "what would change my mind" paragraph has been deleted
rather than softened, because it named an advantage that does not exist at one relay per jail.

The one thing that would genuinely reopen it: a **single shared** relay serving many jails. Then the
verifier no longer maps one-to-one to a caller and a signed subject starts doing real work. #32's
architecture is deliberately one relay per jail, and §7.3 keeps it that way.

### 7.3 OQ-T8 — ship the unification instead of #32? — **DECIDED: yes, replace it**

**Answered 2026-08-13 by the maintainer: we ship the unified transport instead of merging #32.**
My recommendation was the opposite (merge first, migrate after); recorded here because the decision
has consequences that must be planned for rather than discovered.

**What this costs, stated plainly so nobody later reads it as an oversight:**

1. **macOS + podman stays broken until the unification ships.** Every `platform.claude.com` request
   502s and Claude Code will not start there ([#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)).
   That outage window is now a deliberate choice, not a gap.
2. **The transport is no longer free.** The earlier plan treated `loopback-tls` as already built —
   #32 is the working implementation. Replacing it means row **T1** covers *building* the transport
   **and** migrating both consumers, not just migrating. Scope up accordingly.
3. **1064 tested lines are not reused directly.** Its test suite — token auth over pinned TLS,
   plaintext-dial rejection, wrong-cert MITM rejection, `tcpfile:` dial, ENOENT attribution — is the
   spec to re-derive against, and re-deriving is slower than relocating.

**What must be carried over from #32, because it was reverse-engineered the hard way** (§3, §3.1):
binding `127.0.0.1:0` rather than probing a port; the TLS key living **only** in process memory;
publishing the public cert plus `host:port` and re-reading it **fresh on every dial** so a relay
restart needs no jail relaunch; exact-cert pinning via a dedicated root pool rather than trusting a
CA; constant-time token comparison with a framed length cap; and **one relay per jail** — which
§7.2 now depends on.

**What to tell him.** Closing an outside contributor's green, tested, conflict-free PR needs a real
explanation, and there is one: his diagnosis of #31 was correct and is why this document exists, his
transport design is being adopted almost wholesale, and the reason we are not merging it is that it
lives in `brokerrelay` where §3.3's mitigation (1) says it would drift — the framework has to own
it, and `host-processes` (row **D4**) is broken on macOS for exactly the same reason his broker is.
He also filed [#33](https://github.com/mschulkind-oss/yolo-jail/issues/33), which is fixed. That is
a genuine contribution record even with the PR closed, and the close comment should say so.

### 7.4 OQ-T9 — one transport, or two? — **DECIDED: unify**

**Answered 2026-08-13 by the maintainer: unify on `loopback-tls`, retire `unix-socket`.** Kept here
because the reasoning matters for the migration.

The security argument for two was withdrawn (§3.3): `SO_PEERCRED` cannot distinguish the jail from a
same-user host process, and that set is the *intended* boundary rather than a gap. The remaining case
was complexity-vs-uniformity, and uniformity won — two models that drift is worse than a loopback TLS
handshake, and the migration is bounded at **two consumers**: the broker relay and `host-processes`.

**One asymmetry worth remembering:** `loopback-tls` had to be built either way — that is #32 — so
"keep both" would have saved no work, only the migration.

**What must land with the unification**, so the retired path does not linger half-alive:

1. **Migrate `host-processes` first.** It is `unix-socket` today and silently broken on macOS
   (row **D4**), so it is both the natural first port and the proof that the framework — not
   `brokerrelay` — owns the transport.
2. **Remove `unix-socket` from `validTransports`** rather than leaving it accepted-but-unused. A
   value that still validates is a value someone will use.
3. **Add the cross-uid grant for macOS `guest`.** `GuestProfileMacOS()` carries
   `PrimSeparateUser`, so the token file needs a `chgrp`/`chmod` grant to the sandbox uid. This is
   the one place the separate-user primitive costs something, and it is one step, not a transport.
4. **Update [`loophole-protocol.md`](loophole-protocol.md) §Security posture.** Its "the socket file
   is the authentication" sentence is the doc everyone quotes — including this one. It should say
   the boundary is *"whatever runs as your user"* and name the token as the mechanism that enforces
   it on a port, so the next reader does not rediscover this argument.


---

## 8. As built — 2026-08-13

The design above is what was decided; this section is what shipped, including the three places the
design was wrong about the code it was describing and the two places the implementation departed
from it. Written at the end of the build so the doc is not read as a plan whose outcome is unknown.

### 8.1 What exists now

`internal/svcendpoint` is the transport — one stdlib-only leaf package owning **both halves**,
because the file format, the token frame and the pin are one contract and splitting server from
client is how they drift (§3.3). `Listen` binds `127.0.0.1:0`, mints a P-256 certificate whose
private key is never marshalled, mints a 32-byte token, and publishes
`<host:port> <base64 cert DER> <token>` atomically at `0600` into the jail's per-jail directory
(created `0700`, and publication **fails closed** into one that is not). `Dial` re-reads that file
fresh every time, pins the exact certificate through a dedicated root pool, sends the token frame
under a length cap checked **before** allocation, and reads a one-byte accept ack.

Migrated: `host-processes` first (harmless failure, and row **D4**), then the broker relay's
jail-facing hop. `hostservice.Serve` keeps its exact signature and now delegates to
`svcendpoint.Listen`; its conformance tests pass **with their assertions unchanged**, which is the
mechanical proof that a daemon never learns its transport.

### 8.2 Three claims in this document that the code did not support

1. **§2.2 overstates what existed.** `Transport` was consulted in exactly one behavioural place —
   the Apple Container skip — not in a per-platform switch. That reader is now keyed on
   `len(Intercepts)`, which is the thing that actually emits the `--add-host` flags it skips, and
   that re-key is what allowed `tls-intercept` to retire alongside `unix-socket`.
2. **§7.4's "bounded at two consumers" undercounts by two.** There were four host services on the
   retired transport, not two: `cgroup-delegate` and `journal` also have clients baked into the
   image — generated **Python** speaking `AF_UNIX`, neither of which even speaks `frameproto` (one
   is newline-JSON, one uses stream IDs 1/2/3). Retiring the value did not migrate them.
3. **OQ-T2's config override has no domain.** See the inline note in §4.

### 8.3 Where the implementation departed, and why

- **The token is minted by the listener, in process.** §3.2 decides *where the token is delivered*
  and is silent on *who generates it*. #32 minted it host-side because env delivery forced two
  writers to agree; OQ-T7 removes that reason. One writer, one file, one rename — no persistence, no
  second artifact to leak, and rotation for free.
- **The token is per-(jail, service), not per-jail.** §7.2's answer holds at one relay per jail *per
  service*; a shared per-jail token would mean one leaked endpoint file granted the others. Free
  under in-process minting.
- **A one-byte accept ack was added to the wire.** Without it a token mismatch is a post-accept drop,
  which reaches the terminator as EOF-before-exit-frame and is reported as a **broker** failure —
  the most misleading message in the system for the most likely misconfiguration. The ack makes
  auth-rejected a first-class, testable error and leaks nothing (the server still writes nothing on
  failure).
- **`YOLO_SERVICE_<NAME>_SOCKET` became `..._ENDPOINT`, with no dual emission.** The decisive
  argument is image skew: a baked pre-migration client reading a same-named variable whose value is
  now an endpoint file would `net.Dial("unix", …)` a regular file and report something obscure,
  where one reading an *absent* variable hits its existing clean "not wired up in this jail" exit.
- **The relay's own Unix socket left the mounted directory** for `/tmp/yolo-broker-relay-<8hex>.sock`.
  Leaving it in the `:rw`-mounted dir would keep the retired transport reachable from inside the
  jail, and the jail could unlink the relay's own socket.

### 8.4 What did NOT change, and what is still owed

**Unchanged, deliberately:** the wire protocol (`internal/frameproto`, untouched); the broker's hops
A, C and D — the in-jail TLS terminator still binds `127.0.0.1:443`, and relay→singleton and
CLI→singleton are still host→host Unix; the host broker singleton daemon.

> **CORRECTION 2026-08-13, and it is a live defect: the singleton was NOT unchanged.** The sentence
> above states the intent and the code does not implement it. `internal/oauthbroker/oauthbrokercmd.go`
> serves via `hostservice.Serve`, and that function was migrated wholesale (`462729e`, 13:07) to
> delegate to `svcendpoint.Listen` — so the host-wide singleton was carried across the boundary by
> accident, with its signature preserved and every test still green. Measured with the real spawn
> argv (`BrokerSpawnArgv` → `--socket /tmp/yolo-claude-oauth-broker.sock`), `HOME`, `XDG_CONFIG_HOME`
> and `YOLO_BROKER_STATE_DIR` all temp:
>
> ```
> yolo-claude-oauth-broker-host: svcendpoint: refusing to publish a credential into /tmp:
>   mode 0755 is group/world-accessible, want 0700          exit=1, nothing published
> ```
>
> and in a 0700 directory it publishes a 673-byte **`-rw------- ASCII text`** token file at the path
> three sites dial with `net.Dial("unix", …)`. §3.2's first consequence — the file must never land in
> a shared directory — is the only reason this fails closed rather than writing a bearer token into a
> world-readable `/tmp`. Tracked as row **T3** in
> [`../plans/roadmap.md`](../plans/roadmap.md); the fix direction is this
> paragraph, not a new decision (the accept loop is already transport-neutral — `serveListener`).

**Still owed:**

- ~~**`yolo-cglimit` and `yolo-journalctl` are still `AF_UNIX` Python clients**~~ — **DONE
  2026-08-15, with one service left behind on purpose. See §8.6.**
- **A `loopholes:` config entry still gets a plain socket**, and this is the one place the retired
  value survives. `internal/hostservice` is `internal/`, so nothing yolo ships lets a third-party
  daemon publish an endpoint file; flipping that path would kill those daemons rather than migrate
  them. Retirement there means the value is unwritable in a manifest and rejected by name, which is
  §7.4 item 2's actual requirement.
- **The macOS `guest` cross-uid grant is built but uncalled.** `macosuser.EndpointGrantCommands`
  emits two `chmod +a` ACEs (read on the file, traverse on its directory) and replaces
  `BrokerSocketGrantCommands`, which had zero call sites, no test, and would have `chgrp`ed and
  `chmod 0750`ed the machine's `/tmp`. It stays uncalled because macos-user starts no host services
  at all (Thread B).
- **Apple Container remains explicitly deferred** (§6.5 of the implementation spec): `loopback-tls`
  removes the *transport* obstacle, but how the endpoint file crosses into an AC guest is an unmade
  mount decision. Recorded as an unclaimed win with a named blocker, not as delivered.

### 8.5 What was NOT verified

**macOS + podman, Apple Container and `macos-user` were never executed** — the whole build ran on
Linux. So the headline claim of this document, that this fixes
[#31](https://github.com/mschulkind-oss/yolo-jail/issues/31) and row **D4**, is *unverified on the
platform it is about*. What was verified, in nested jails against freshly built binaries: the
endpoint file's mode and shape through the `:rw` bind, `yolo-ps` over pinned TLS, the terminator
reaching the real broker through the relay's front with its host-side `jail_id` stamp intact, all
four failure layers distinguishable, and token rotation picked up with no restart.

> **One item in that list was verified against STALE STATE, 2026-08-13.** "The terminator reaching
> the real broker" could only have passed because this development jail still had a **pre-migration**
> singleton alive: pid 203814, started `2026-08-12 23:38:40`, holding a real `srw-------` AF_UNIX
> socket at `/tmp/yolo-claude-oauth-broker.sock` — i.e. a binary from *before* `462729e` (13:07 the
> next day) made `hostservice.Serve` publish an endpoint file. `BrokerIsAlive`'s four gates (pid
> file, pid live, socket exists, ping) therefore all pass, `brokerEnsure` no-ops, and the whole chain
> works end to end **on a daemon this tree can no longer start** (the correction in §8.4). The
> end-to-end hop through the relay's front is real and tested in-process; what is NOT established is
> that a host with no surviving pre-migration singleton reaches a broker at all.
>
> **Discipline this earns, for anyone verifying broker work from inside a jail:** a long-lived
> host-wide singleton is invisible stale state, and every liveness gate in the system is designed to
> be satisfied by it. Restart it first (`yolo broker restart`, or a temp `YOLO_BROKER_STATE_DIR` plus
> a spawn of the real argv) or the verification measures the previous binary.

### 8.6 The last two clients — as built, 2026-08-15

§8.4 owed a port of `yolo-cglimit` and `yolo-journalctl` off generated Python. Both are ported.
**Only one of the two services followed them onto the transport**, and the reason the other did not
is the most useful thing in this section.

#### 8.6.1 What shipped

- **`cmd/yolo-cglimit` and `cmd/yolo-journalctl` are Go binaries baked into the image**
  (`flake.nix` `shippedBinaries`). The generators in `internal/entrypoint/scripts.go` are gone.
- **The journal bridge is on `loopback-tls`.** `journald.ServeEndpoint` publishes its own endpoint
  file via `svcendpoint.Listen`; the client dials it with `svcendpoint.Dial`.
  `YOLO_SERVICE_JOURNAL_SOCKET` became `..._ENDPOINT`, with **no dual emission and no client-side
  fallback** — §8.3's argument, applied again.
- **`unix-socket` is now unreachable, not merely unwritable.** The run pipeline carried its own
  private `transportLegacySocket = "unix-socket"` for the two built-ins; it is deleted.
  `loopholedecl.RetiredTransportUnixSocket` survives with exactly one production reader,
  `retiredTransportHint` — i.e. **only as a migration hint for manifest authors**, which is what its
  doc comment already claims. Nothing to change there; the claim became unqualifiedly true only now.

#### 8.6.2 The journal bridge took `Listen`, not a front

`publishes: "socket"` exists for a daemon whose core is typed on `*net.UnixConn` and cannot swap its
listener. `journald`'s was — but only incidentally: `handleConn` and `readHeaderCapped` used nothing
a `net.Conn` lacks, so widening the two signatures was the entire server-side change. `front.go`'s
own guidance decides it: *"Every daemon that CAN take Listen directly should"* — a splice would mean
two listeners and a host-only socket for no benefit. `Serve` (AF_UNIX) stays for host-to-host use,
and its existing test suite still pins the protocol **with its assertions unchanged**, which is the
same mechanical proof §8.1 records for `hostservice.Serve`.

#### 8.6.3 The `cgroup-delegate` cannot move, and the blocker is not its client

This is the correction worth carrying forward, because §7.4 and §8.2 both frame the remaining work
as *"the clients are Python."* For the journal bridge that was the whole story. For the cgroup
delegate it is **not the story at all**: `cmd/yolo-cglimit` is a baked Go binary and the delegate
still cannot move.

**`SO_PEERCRED` is what does not survive the hop.** The delegate's security model is kernel-attested
identity ([`security-shim.md`](security-shim.md) §2, *"we never trust the container to identify
itself"*): `create_and_join` writes the peer's **host-namespace** pid — read off the connection by
the kernel, never sent by the caller — into the job cgroup's `cgroup.procs`, and that write *is* the
mechanism that moves the caller into the cgroup.

| Option | Why it fails |
|---|---|
| `svcendpoint.Listen` directly | a TCP connection carries no peer credential at all; `peerPID` would be 0 and every `create_and_join` would fail |
| `publishes: "socket"` (a TLS front) | **worse than failing** — `SO_PEERCRED` on the upstream Unix socket attests the FRONT's pid, i.e. yolo's own, so the delegate would move the `yolo run` process into the jail's job cgroup |
| the client sends its own PID | caller-**asserted** where the current value is kernel-**attested**, and it is a PID in the container's namespace where the host needs one in its own |

Closing the gap means giving the transport a way to carry a kernel-attested caller identity (host-side
`NSpid` translation, or an `SCM_CREDENTIALS` equivalent) — **a credential decision with its own
design, not a transport swap.** Deliberately left out of this row rather than improvised inside it.

**The consequence is honest and unpleasant:** `cgroup-delegate` is still an AF_UNIX service, so it is
still broken on macOS + podman for the virtiofs reason this whole document exists to fix. The
unification is complete for every service that *can* be unified; one cannot, and it is now the one
place `unix-socket` still describes reality. The argument lives above `startCgroupDelegate` so the
next reader meets it before reaching for `publishes: "socket"`.

#### 8.6.4 The scope was larger than "two consumers", again

§7.4 predicted two consumers; §8.2 corrected it to four. The port found a fifth thing, of a different
kind: **the ship set is spelled twice.** `flake.nix`'s `shippedBinaries` filters what a
source-checkout image installs, and `scripts/stage-source-bundle.sh`'s `SHIPPED_BINARIES` filters
what a *shipped bundle* carries as prebuilt artifacts — which `flake.nix`'s prebuilt short-circuit
then consumes with `[ -e "$src" ] || continue`. A binary missing from the second is silently absent
from a bundle-built image and present in a source-built one, and **no dev jail ever takes the bundle
path**, so the divergence is invisible from inside. `internal/entrypoint/shippedclients_test.go` now
pins `cmd/*`, both lists, and the single declared exemption (`goprobe`) together.

#### 8.6.5 Verified

Nested jail, freshly built `yolo` run **by path**, against a scratch `HOME` (this development jail's
own user config names three `file:///home/matt/.dotfiles/packs/*` packs that do not exist in-jail, so
a plain nested launch dies before any container starts):

- `nix eval .#installPrefix.outPath` and the loaded image's `readlink /bin/yolo-entrypoint`,
  `/bin/yolo-cglimit`, `/bin/yolo-journalctl` all resolve to the **same** store path — no stale-image
  fallback, and `installPrefix` picked up both new binaries;
- both clients are ELF at `/bin`, with **nothing** left in `~/.local/bin`;
- `/run/yolo-services/` holds `journal.endpoint` (`-rw-------`) and `cgroup-delegate.sock`
  (`srwxrwxrwx`) — and **no `journal.sock`**;
- the jail's environment carries `YOLO_SERVICE_JOURNAL_ENDPOINT` and
  `YOLO_SERVICE_CGROUP_DELEGATE_SOCKET`, and no `YOLO_SERVICE_JOURNAL_SOCKET`;
- `yolo-journalctl -n 3` returns the daemon's **own** `journalctl not found on host` stderr frame and
  exit code **127**, through dial → cert pin → token → accept ack → newline-JSON request → stream-2
  frame → stream-3 exit frame. The host is a container with no systemd, so 127 is the correct answer
  and it is a full-stack proof;
- `yolo-cglimit --cpu 50` returns the daemon's own `Failed to set up agent cgroup hierarchy` and exit
  1 — a nested jail's cgroup filesystem is read-only (the integration suite's `skipIfCgroupReadonly`
  exists for this), so the round trip is proven and the privileged write is the part the environment
  cannot supply.

**Not verified, same as §8.5:** macOS + podman, Apple Container and `macos-user`. Everything above ran
on Linux.
