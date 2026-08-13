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

**So today's honest statement:** loopholes are a Linux feature that happens to work on macOS only
where nothing needs a socket. `audio` and `host-processes` are unaffected or differently wired;
`claude-oauth-broker` is the one that exposed it, because it is the one that must work before the
agent starts.

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
4. authenticates a **per-jail bearer token** (length-framed, constant-time compare) *inside* TLS;
5. splices to the relay's own Unix socket, leaving the relay core (the `jail_id` stamp, the
   per-connection broker dial, the failure semantics) untouched.

The terminator, for a `tcpfile:` address, re-reads the endpoint file **fresh on every dial** (so a
restarted relay is picked up without relaunching the jail) and TLS-dials trusting **only** that
cert, via a dedicated root pool.

**Why each piece is load-bearing**, because a simpler version is tempting and wrong:

| Piece | What breaks without it |
|---|---|
| loopback bind | the port is on the podman-machine bridge, i.e. the LAN |
| TLS | a sibling jail on the shared bridge can sniff the hop |
| pinning a **host-only** key | a sibling can impersonate the relay — and see §5, the broker CA cannot be used for this |
| per-jail bearer token | loopback TCP has no `connect()`-implies-authorized property; the socket-file model does not survive the port |
| re-read on every dial | a relay restart otherwise strands the jail until relaunch |

**The token is the interesting part.** A Unix socket carried authorization implicitly — filesystem
permissions plus `getpeereid`. A TCP port carries none, so #32 had to invent explicit client
authentication. That is not a workaround; it is the missing half of the trust model, and §4 argues
we want it even where sockets work.

---

## 4. The generalization, and the trust-model upgrade hiding inside it

**Proposal: the transport becomes a property of the loophole framework, not of `brokerrelay`.**

A loophole manifest gains a transport selection — `unix` (today's default) or `loopback-tls` — and
the framework owns endpoint publication, cert pinning, and token issuance. Daemons keep speaking
`frameproto` and never learn which transport carried the bytes. The wire protocol in
[`loophole-protocol.md`](loophole-protocol.md) is **unchanged**; this is the layer beneath it.

Selection should be **automatic by platform, overridable by config**: `loopback-tls` where a Unix
socket cannot cross (macOS + podman), `unix` elsewhere. A user should not have to know what
virtiofs is to run a loophole on a Mac.

### 4.1 Per-jail client secrets, everywhere

The upgrade worth taking beyond macOS: **give every jail a named client secret and require it on
every request, on both transports.**

Today's *"the socket file is the authentication"* means a daemon trusts anything running as the
same user on the host — including a browser extension, an unrelated npm postinstall, or a second
jail. That is documented and deliberate, and it is also the weakest claim in the loophole design.

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
every jail. Combined with `NODE_EXTRA_CA_CERTS` trusting that CA in-jail, **a jail process can mint
a trusted leaf for any host** — including impersonating a sibling jail's relay.

**`0600` is not a mitigation.** A yolo jail runs its agent as UID 0 by design (Claude YOLO is
`--dangerously-skip-permissions` plus `IS_SANDBOX=1`, which exists to bypass the UID-0 refusal), so
owner-only permissions are no barrier — the read above is the proof.

### 5.1 The fix

**Only `server.crt` and `server.key` are needed in-jail.** `ca.key` is used solely host-side by
`cert.go`. Mount the two server files rather than the state directory.

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
| **`ca.key` readable in-jail** | 🔴 open | measured above; now also public as #33 |
| **Claude creds symlink dangles on macos-user** | 🔴 open | Thread B; blocks the Teams auth mode on macOS |
| **Config-approval snapshot is agent-writable** | 🔴 open | `.yolo/config-snapshot.json` is mode `664` and writable in-jail *(re-measured)*. An agent that edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt disappear — the exact bypass [`config-safety.md`](config-safety.md) exists to prevent |
| **Two shipped docs contradict the code** | 🟡 partly | `USER_GUIDE.md:182` and `bundled_loopholes/claude-oauth-broker/README.md:59` both still say *"no background timer / no proactive refresh"*, while `oauthbrokercmd.go:88` starts `RunBackgroundRefresher` by default. The separate `--host-creds-file` staleness **has** since been fixed |

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
3. **Generalize the transport** (§4) when the second consumer appears — B1 is the likely first,
   and it is small enough to be a good forcing function.
4. **Per-jail client secrets on both transports** (§4.1), as a deliberate trust-model change with
   its own note in the protocol doc.

Deliberately *not* first: the generalization. #32 is a working fix for a total outage on one
platform, and the framework question should not hold it hostage.

---

## 7. Open questions

- **OQ-T1. Per-jail token + pinned host-only cert, or full mTLS?** #32's author asked this
  explicitly and nobody has answered. A bearer token inside pinned TLS is already stronger than
  the socket-file model; mTLS adds client-cert lifecycle for a hop that is already
  loopback-bound and pinned. **Recommendation: as proposed** — but the answer should be given
  alongside §4, because generalizing makes it every service's answer.
- **OQ-T2. Is transport selection automatic, configured, or both?** §4 argues automatic-by-platform
  with a config override. The risk of automatic is a silent fallback nobody notices; the risk of
  configured is a Mac user who must know what virtiofs is.
- **OQ-T3. Do per-jail secrets apply to the `unix` transport too?** §4.1 argues yes. Against: it
  adds a failure mode to a path that works, on platforms where the socket already carries
  authorization.
- **OQ-T4. Does `macos-user` make this moot?** It has no VM, so no virtiofs boundary — but the
  broker is unwired there (`BrokerSocketGrantCommands`, zero call sites) and it renders zero pack
  surfaces. Not available today; worth re-asking once Thread B moves.
- **OQ-T5. Should the endpoint file be treated as jail-writable?** It lands in the jail's mounted
  host-services dir. #32 notes a sibling cannot tamper with *another* jail's file (separate
  per-jail mounts), but a jail can presumably rewrite **its own** — which redirects only itself,
  and it already knows its own token. Worth stating explicitly rather than leaving to inference.
