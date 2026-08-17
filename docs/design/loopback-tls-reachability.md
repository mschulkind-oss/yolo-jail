---
title: "Loopback-TLS reachability — how a jail reaches a host daemon, and why it currently cannot"
date: 2026-08-17
status: in-review
tags: [transport, networking, loopholes, regression]
summary: "The transport assumes one rootless networking stack's behaviour is universal. It is not. A walk through every networking mode, why 'just bind the right address' has nowhere to go, and the three decisions left."
---

# Loopback-TLS reachability — how a jail reaches a host daemon, and why it currently cannot

**Status:** DESIGN, 2026-08-17. Nothing built. Absorbs and replaces the operational handoff that
originally reported this (`docs/plans/handoff-loopback-tls-pasta.md`, deleted in the same commit —
its evidence is folded into §2 and §3).

**The short version.** yolo's host daemons bind `127.0.0.1` and tell the jail to dial
`host.containers.internal`, on the assumption that a container runtime forwards that name to the
host's **loopback**. Whether it does is a property of *which rootless networking stack is in use* —
true for slirp4netns, **false for pasta**, which podman has defaulted to since 5.0. So on a pasta
host every loopback-TLS service has been unreachable from every jail since `58ce9ee` (2026-08-13).
The fix is to make the runtime forward loopback rather than to change what yolo binds.

> [!NOTE]
> **"Why can't we know how routing works ahead of time?"** — we can, and that is the real
> indictment. `podman info` reports `rootlessNetworkCmd` on every host. Nothing here was
> unknowable; the design hardcoded one stack's behaviour as if it were the only one, and never
> asked. See [§4](#4-what-yolo-does-today-and-exactly-where-the-premise-fails).

**Start with [§2](#2-the-mental-model-two-namespaces-two-loopbacks)** if the networking is the
unclear part — everything else depends on it.

**Reads with:** [`loophole-transport.md`](./loophole-transport.md) — §3.0 is the loopback-bind
security model this preserves, §7.4 the decision that retired `unix-socket`, which OQ-R3 would
reopen.

---

## 1. The symptom

| | |
| :--- | :--- |
| **What fails** | `yolo-ps`, `yolo-journalctl`, and the Claude OAuth broker — everything on loopback-TLS |
| **How** | `dial tcp 169.254.1.2:<port>: connect: connection refused` |
| **Since** | `58ce9ee`, 2026-08-13. First real failure logged `2026/08/13 21:10:02`; 24 broker refresh failures on record |
| **Where** | any host whose podman reports `rootlessNetworkCmd: pasta` — the default since podman 5.0 |
| **Not the cause** | the preamble/pack sprint. `git log -L` on the bind line returns `58ce9ee` plus two commits that cancel out, and `ECONNREFUSED` at `connect(2)` means no byte above TCP is ever written |

> [!IMPORTANT]
> **It fails closed.** `/etc/hosts` pins `platform.claude.com` to `127.0.0.1`, and the in-jail
> terminator's only route out is the relay — so a jail **cannot** silently mint its own OAuth
> refresh instead. The single-use-refresh-token race stays prevented. What is lost is availability,
> not safety.

---

## 2. The mental model: two namespaces, two loopbacks

This is what makes the rest obvious, and it is why *"just bind the right place and connect to the
right place"* is harder than it sounds.

A jail runs in its own **network namespace**: its own interfaces, its own routing table, and —
critically — **its own loopback**. When a daemon on the host binds `127.0.0.1` and a process in the
jail dials `127.0.0.1`, those are two different addresses on two different stacks. The jail is not
failing to reach the host; it is successfully reaching *itself*.

So the jail needs some *other* address that means "the host". Every rootless stack invents one, and
**they disagree about what it forwards to**:

```mermaid
flowchart LR
    subgraph jail["jail netns"]
        c["client (yolo-ps)"]
        jlo["127.0.0.1 — the JAIL's loopback"]
    end
    subgraph host["host netns"]
        hlo["127.0.0.1 — the HOST's loopback<br/>(the daemon binds HERE)"]
        glob["192.168.1.131 — host's LAN address"]
    end
    c -->|"dials host.containers.internal"| gw["the stack's 'host' address"]
    gw -.->|"slirp4netns, with allow_host_loopback"| hlo
    gw -->|"pasta, by default"| glob
    c --> jlo
```

**The trap: the two ends of that arrow are chosen by different parties.** yolo picks the bind
address; the *container runtime* picks what the forwarding address maps to. yolo has been choosing
one end and assuming the other.

**Pasta makes this especially confusing, and this jail is a live example.** Pasta copies the host's
interfaces, addresses and routes into the namespace, so the jail looks almost exactly like the host.
Measured here, right now:

```console
$ ip -brief addr
lo         UNKNOWN  127.0.0.1/8 ::1/128
enp191s0   UNKNOWN  192.168.1.131/24 …      # the HOST's interface name and LAN address

$ ip route
default via 192.168.1.1 dev enp191s0 …      # the HOST's real gateway

$ cat /etc/hosts
169.254.1.2  host.containers.internal host.docker.internal
```

The jail believes it is `192.168.1.131` on `enp191s0`. It is not — it holds a **copy** of that
identity in a separate namespace. Dialling `192.168.1.131` from inside reaches the jail's own copy,
which is why that probe comes back *refused* rather than answered.

---

## 3. The networking modes, spelled out

Every row answers one question: **when a process inside the jail dials the "host" address, where does
the packet actually arrive?**

| Mode | Jail resolves `host.containers.internal` to | That forwards to | Can yolo *bind* what the jail dials? | Loopback-TLS today |
| :--- | :--- | :--- | :--- | :--- |
| **pasta** (rootless; podman ≥ 5.0 default) | `169.254.1.2`, a synthetic tunnel address | the host's **global** address ⚠️ | **No** — not an interface on the host; `bind()` returns `EADDRNOTAVAIL` | ❌ **broken** |
| **pasta** + `--map-host-loopback` | `169.254.1.2` | the host's **loopback** | No — and it does not need to | ✅ the proposed fix |
| **slirp4netns** (rootless; older default) | `10.0.2.2`, a userspace gateway | host loopback **only if** `allow_host_loopback=true` | No | ⚠️ works *if* that option is set |
| **netavark bridge** (rootful) | the bridge gateway, e.g. `10.88.0.1` | the host, via a real bridge interface | **Yes** — a genuine host interface | ✅ works |
| **`--net=host`** (no namespace) | n/a — the jail *shares* the host's stack | itself | Yes, trivially | ✅ works |
| **nested jail** (podman-in-podman) | forced to `--net=host` | itself | Yes | ✅ works — **and this is why nobody caught it** (§7) |
| **Apple Container / `macos-user`** | a VM hop, not pasta | out of scope | — | not affected |

> [!WARNING]
> Read the fourth column downward. **In every rootless mode, yolo cannot bind the address the jail
> dials.** That is not an inconvenience to route around — it is the shape of the whole problem, and
> §5 is what falls out of it.

**Verification status, stated honestly.** The pasta rows are measured on this host (§2 and §3.1).
The slirp4netns and netavark rows come from documented behaviour and from `46d5417`'s findings,
**not** re-measured — there is no slirp host here to test on. The `--map-host-loopback` row is the
proposal; whether the flag exists on a given passt build is OQ-R3.

### 3.1 Where pasta actually forwards — measured

The original report left this open. A differential probe from inside a jail settled it:

| probe | result | what it establishes |
| :--- | :--- | :--- |
| `169.254.1.2:22` | connects, `SSH-2.0-OpenSSH_10.4` | the mapping is real and reaches the host |
| `169.254.1.3:22` | times out | control — the mapping is specific, not a catch-all |
| the two yolo ports | **refused**, not timed out | the packet reached a host stack and got an RST |
| `192.168.1.131:22` | refused | the jail's own copy of the host's address, per §2 |

**Pasta forwards `169.254.1.2` to the host's global address, not its loopback.**

---

## 4. What yolo does today, and exactly where the premise fails

Two lines, both from `58ce9ee`:

```go
// internal/svcendpoint/listen.go
raw, err := net.Listen("tcp", "127.0.0.1:0")             // bind: the HOST's loopback
const DefaultAdvertiseHost = "host.containers.internal"  // advertise: "wherever the host is"
```

The daemon writes the advertised host and port into an endpoint file the jail reads, and the jail
dials it. Correct **if and only if** the runtime forwards `host.containers.internal` to the host's
loopback — which [`loophole-transport.md`](./loophole-transport.md) §3.0 states as an assumption
rather than something checked.

**The premise was never universally true, and the counter-evidence predated the design by four
months.** `46d5417` (2026-04-17) fixed this same class of bug on this same host, concluding that
`169.254.1.2` *"is a container-side pasta tunnel, not a real interface on the host, so bind(2)
returns EADDRNOTAVAIL."* The knowledge was in the tree; the design contradicted it.

**And none of it was unknowable at launch.** `podman info` reports `rootlessNetworkCmd`, so the mode
is a fact yolo can read before starting anything. It simply never asks: today `network.mode:
"bridge"` — the default — emits **no** `--network` flag at all and defers entirely to podman
([`assemble.go`](../../internal/cli/run/assemble.go#L252-L259)).

---

## 5. Why "bind somewhere else" has nowhere to go

The instinct — *pick a better bind address* — is right, and it is exhausted. The launcher's network
namespace contains **only** loopback and the host's real interfaces, so there are exactly two
candidates:

- **loopback** — what we do now, unreachable from a rootless jail;
- **`0.0.0.0` or the host's LAN address** — reachable, and **on the LAN**, which is precisely what
  §3.0's loopback bind exists to prevent. TLS and the per-jail token still gate *access*, but the
  port becomes visible to the network. That is a change of security posture, not a bug fix.

There is no third address. The option is not unattractive; it does not exist.

> [!CAUTION]
> `a1003b9` (reverted by `c49051c`) hit **both** failure modes at once by binding `10.88.0.1`, the
> **rootful** netavark gateway: unbindable from a rootless launcher (`EADDRNOTAVAIL`, so the daemon
> died at startup) *and* an address a pasta jail never dials. Do not retry a variant of it.

That leaves three real options, and only one keeps both the security model and the single transport:

| Option | Verdict |
| :--- | :--- |
| **Make the runtime forward loopback** (`--map-host-loopback`) | ✅ **Take this.** Bind, cert pinning, per-jail token and the one-transport decision all survive untouched |
| **Bind `0.0.0.0` / the LAN address** | ❌ Rejected — trades a reachability bug for permanent LAN exposure that §3.0 exists to prevent |
| **Bind-mounted AF_UNIX socket on Linux** | ⚠️ Works and is LAN-free, but reopens a decision `loophole-transport.md` §7.4 retired *on purpose*. Fallback only — OQ-R3 |

---

## 6. The fix

Ask the runtime what it is, then tell it to forward loopback:

```bash
# rootlessNetworkCmd = pasta
--network=pasta:--map-host-loopback,<addr>
# rootlessNetworkCmd = slirp4netns
--network=slirp4netns:allow_host_loopback=true
# anything else (rootful bridge, --net=host, Apple Container)
#   → emit nothing, exactly as today
```

`internal/svcendpoint` is **not touched**: the `127.0.0.1` bind, cert pinning, the per-jail bearer
token and the single-transport decision all survive verbatim, and
`TestAdvertiseHostDiffersFromBindHost` keeps passing unmodified — real signal that nothing
security-shaped moved.

**One implementation note:** podman already passes `--map-guest-addr 169.254.1.2`. If passt rejects
the same address for both options, use a distinct one and advertise that literal — `svcendpoint`
supports it, because the cert's ServerName is a fixed label rather than a hostname.

---

## 7. Companion work — non-optional, whichever way the fix goes

**`yolo check` reports PASS during a total outage.** Its probe uses `DialLocal`, which substitutes
`127.0.0.1` for the advertised host ([`dial.go`](../../internal/svcendpoint/dial.go#L64), called at
[`sections_loopholes.go`](../../internal/cli/check/sections_loopholes.go#L143)) — so it dials the one
address a jail cannot use, and stays green while everything is down. A probe that cannot fail when
its subject is down is worse than no probe. The honest probe is **in-jail**, because the advertised
address is only meaningful from inside; that makes its severity a real choice (OQ-R2).

**Nested-jail verification is structurally blind to this.** Row 6 of §3 explains the whole incident:
a nested podman is forced onto `--net=host`, the one mode where the bug **cannot** reproduce. So
`AGENTS.md`'s "verify in a nested jail" instruction is not merely insufficient here, it is
*misleading*, and needs an explicit carve-out. There is currently no integration coverage of in-jail
reachability at all.

---

## 8. Non-goals

- **Not** a change to `internal/svcendpoint`. A patch touching the bind or the advertise host is the
  wrong patch.
- **Not** a change to the §3.0 security model. Loopback-bind stands; this makes it *reachable*.
- **Not** a revival of `unix-socket` as a second transport — unless OQ-R3 forces it, in which case it
  is an amendment to a retired decision and must be written as one.
- **Not** macOS work. Apple Container and `macos-user` do not use pasta.

---

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| yolo starts dictating a network option and a future podman default shifts under it | Detect from `podman info` rather than assuming; emit nothing for unrecognised backends, which is exactly today's behaviour |
| A user with an explicit `network.mode` keeps the bug and we cannot fix it for them | Warn on that path naming the reachability risk, rather than overriding a setting they chose deliberately |
| The fix is unverifiable in a nested jail (§7), so it could land untested | Verification is a real launch on an affected host plus new integration coverage — do not accept a nested-jail green as evidence |
| `podman info` per launch adds startup latency | One host-side subprocess; measure before caching, and cache into the boot baseline only if it matters |
| The slirp4netns and netavark rows in §3 are unverified | They do not gate the fix — the pasta path is measured, and unrecognised backends keep today's behaviour |

---

## 10. Sequencing

**First, the in-jail probe (§7).** Independent of the fix, it makes the outage visible, and it is
what will prove the fix worked. Build the witness before the change.

**Second, the network option (§6)**, gated on `podman info`, on the default path only.

**Third, integration coverage** asserting a jail can actually reach a published endpoint — the gap
that let this ship.

**Fourth, the `AGENTS.md` carve-out**, so the next agent does not trust a nested jail here.

---

## Open Questions

**Three open, one resolved. This is the complete list — nothing about this bug is open elsewhere.**

### ✅ OQ-R0 — what does pasta forward `169.254.1.2` to? — RESOLVED (2026-08-17)

The original report's blocking question.

**Answer:**
> **The host's GLOBAL address, not its loopback**, established by the differential probe in §3.1.
> This is what kills the "bind somewhere else" family in §5 and rules out three of the four options
> the original report listed.

### 💬 OQ-R1 — may yolo emit a network option on the default path?

Today `network.mode: "bridge"` means *"emit nothing, let podman decide"*
([`assemble.go`](../../internal/cli/run/assemble.go#L252-L259)). The fix makes it mean *"emit
`--network=pasta:--map-host-loopback,…` when podman reports pasta"* — yolo taking ownership of
something it deliberately left alone.

**What it decides:** whether the fix can live in the launcher at all. If not, the only remaining
lever is the transport, and §5 says that lever has nowhere to go.

Knock-on worth naming: a user who sets `network.mode` explicitly keeps full control and therefore
**keeps the bug**, and we cannot fix it for them.

_Leaning:_ **Yes, and warn on the custom path.** A transport that works only by luck of the host's
network stack is not a transport. Emitting nothing for unrecognised backends preserves today's
behaviour exactly, so the blast radius is confined to hosts we can positively identify.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-R2 — when a jail-facing service is unreachable at boot: warn, or fail the launch?

§7's in-jail probe needs a severity, and it cannot be deferred to `yolo check`, because a host-side
check structurally cannot test jail reachability.

**What it decides:** whether a pasta host with an old passt can launch a jail at all.

_Leaning:_ **Loud warning naming the service, not a fatal.** A jail with no journal bridge is still a
working jail. But the broker's case is *"your Claude auth is down"*, which is closer to fatal than
the others — so if any service earns its own severity it is that one, which may argue for a
per-service level rather than one global rule.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-R3 — if the host's passt predates `--map-host-loopback`, what then?

Two options, not equivalent: **AF_UNIX on Linux**, which reopens
[`loophole-transport.md`](./loophole-transport.md) §7.4 — retired *on purpose*, so it needs an
amendment rather than a quiet workaround — or **refuse to launch** with a message naming the passt
version required.

**What it decides:** whether this fix stays contained to the launcher, or reopens the transport
question.

> [!TIP]
> **This collapses to a fact, and it cannot be obtained from inside a jail** (no pasta here, and
> `yolo-ps` is down because of this very bug). One command on the host settles it:
> ```bash
> pasta --version
> ```

_Leaning:_ **Refuse to launch, and say what is needed.** Reviving a retired transport to serve one
old passt build is a large permanent cost for a shrinking population; a clear refusal names an
upgrade the user can actually perform. If the affected population turns out to be large, the
amendment is the honest path — but that is evidence we do not have yet.

**Answer:**
> _(empty — fill in when decided)_
