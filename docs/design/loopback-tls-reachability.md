---
title: "Loopback-TLS reachability — fixing the forwarding, not the bind"
date: 2026-08-17
status: in-review
tags: [transport, networking, loopholes, regression]
summary: "Every loopback-TLS service is unreachable from every jail on a pasta host. The bind address is not the lever; the runtime's forwarding is. Three decisions needed before implementation."
---

# Loopback-TLS reachability — fixing the forwarding, not the bind

**The short version.** The transport binds `127.0.0.1` and advertises `host.containers.internal`, on
the premise that the container runtime forwards that name to the host's **loopback**. That premise
holds for slirp4netns and is **false for pasta**, which podman has defaulted to since 5.0 — so every
loopback-TLS service has been unreachable from every jail on this host since `58ce9ee`
(2026-08-13). The fix is to make the runtime forward loopback, **not** to change what yolo binds:
`--network=pasta:--map-host-loopback,<addr>`. `internal/svcendpoint` is not touched.

**The most important section is [§3](#3-why-bind-address-changes-are-structurally-dead)** — it rules
out the entire family of fixes that look obvious, including the one already tried and reverted.

**Reads with:** [the original bug report and my triage addendum](../plans/handoff-loopback-tls-pasta.md)
(symptom, environment, and the differential probe that settled where pasta forwards) ·
[`loophole-transport.md`](./loophole-transport.md) (§3.0 the loopback-bind security model, §7.4 the
decision that retired `unix-socket` — which the fallback in [OQ-R3](#open-questions) would reopen).

---

## 1. What is broken, and since when

| | |
| :--- | :--- |
| **Symptom** | `yolo-ps`, `yolo-journalctl` and the Claude OAuth broker all fail with `dial tcp 169.254.1.2:<port>: connect: connection refused` |
| **Since** | `58ce9ee`, 2026-08-13 — each service broke as it crossed onto the transport; the first real failure is logged at `2026/08/13 21:10:02` |
| **Scope** | every loopback-TLS service, on every jail, on any host where podman's `rootlessNetworkCmd` is pasta |
| **Not the cause** | the in-flight preamble/pack sprint — `git log -L` on the bind line returns `58ce9ee` plus two commits that cancel out, and `ECONNREFUSED` at `connect(2)` means no preamble byte is ever written |

> [!IMPORTANT]
> **It fails closed, and that is load-bearing.** `/etc/hosts` pins `platform.claude.com` to
> `127.0.0.1`, and the in-jail terminator's only route out is the relay — so a jail **cannot**
> silently mint its own refresh instead. The single-use-refresh-token race the broker exists to
> prevent stays prevented. What is lost is availability, not safety.

**The knowledge predated the design by four months**, which is the part worth sitting with:
`46d5417` (2026-04-17) fixed this same class of bug on this same host, concluding that `169.254.1.2`
*"is a container-side pasta tunnel, not a real interface on the host, so bind(2) returns
EADDRNOTAVAIL."*

---

## 2. Where pasta actually forwards

Settled by differential probe from inside a live jail, because the original report left it open:

| probe | result | what it establishes |
| :--- | :--- | :--- |
| `169.254.1.2:22` | connects, `SSH-2.0-OpenSSH_10.4` | the mapping is real and reaches the host |
| `169.254.1.3:22` | times out | control — the mapping is specific, not a catch-all |
| the two yolo ports | **refused**, not timed out | the packet reached a host stack and got an RST |
| `192.168.1.131:22` | refused | pasta copies the host's own address onto the guest |

**Pasta forwards `169.254.1.2` to the host's GLOBAL address, not its loopback.**

---

## 3. Why bind-address changes are structurally dead

This is the finding that determines the fix, and it kills three of the four options the original
report listed.

The launcher's network namespace holds **only** loopback and host-global addresses. So "pick a better
bind address" has exactly two candidates:

- **loopback** — unreachable from a pasta jail, which is the bug;
- **`0.0.0.0` or the host-global address** — reachable, and *on the LAN*, which is precisely what
  [`loophole-transport.md`](./loophole-transport.md) §3.0 exists to prevent.

There is no third address. The option is not unattractive; it does not exist.

> [!WARNING]
> `a1003b9` (reverted by `c49051c`) managed to hit **both** failure modes at once by choosing
> `10.88.0.1`, the **rootful** netavark bridge gateway: unbindable from the launcher's namespace
> (`EADDRNOTAVAIL`, so the daemon died at startup) *and* an address a pasta jail never dials. Do not
> retry a variant of it.

---

## 4. The fix

Emit an explicit per-backend network option at launch, and leave the transport alone:

```bash
# rootlessNetworkCmd = pasta
--network=pasta:--map-host-loopback,<addr>
# rootlessNetworkCmd = slirp4netns
--network=slirp4netns:allow_host_loopback=true
```

**What survives verbatim:** the `127.0.0.1` bind, cert pinning, the per-jail bearer token, and the
single-transport decision. `TestAdvertiseHostDiffersFromBindHost` keeps passing unmodified, which is
real signal that nothing security-shaped moved.

**Where it lands.** Today `network.mode: "bridge"` — the default — emits **no** `--network` flag at
all and lets podman decide ([`assemble.go`](../../internal/cli/run/assemble.go#L252-L259)). That
deference is exactly how pasta's behaviour leaks through, so the default path is the path that
changes. See [OQ-R1](#open-questions).

**One implementation note:** podman already passes `--map-guest-addr 169.254.1.2`. If passt rejects
the same address for both options, use a distinct one and advertise that literal — `svcendpoint`
supports it, because the cert's ServerName is a fixed label rather than a hostname.

---

## 5. Companion work — non-optional, whichever fix wins

**`yolo check` reports PASS during a total outage.** Its probe uses `DialLocal`, which substitutes
`127.0.0.1` for the advertised host ([`dial.go`](../../internal/svcendpoint/dial.go#L64), called at
[`sections_loopholes.go`](../../internal/cli/check/sections_loopholes.go#L143)). So the health check
dials the one address a jail cannot use, and is green while everything is down. A probe that cannot
fail when its subject is down is worse than no probe.

The honest probe is **in-jail**: the advertised address is only meaningful from inside. That makes
the severity a real choice — see [OQ-R2](#open-questions).

**Nested-jail verification is structurally incapable of catching this class.** A nested podman is
forced onto `--net=host`, which is exactly the configuration where the bug cannot reproduce. So
`AGENTS.md`'s "verify in a nested jail" instruction is not merely insufficient here, it is
*misleading*, and needs an explicit carve-out. There is currently **no** integration coverage of
in-jail reachability at all.

---

## 6. Non-goals

- **Not** a change to `internal/svcendpoint`. If a patch touches the bind or the advertise host, it
  is the wrong patch.
- **Not** a revival of `unix-socket` as a second transport — unless [OQ-R3](#open-questions) forces
  it, in which case it is an amendment to a retired decision and must be written as one.
- **Not** a change to the loopback-bind security model. §3.0 stands; this makes it *reachable*.
- **Not** macOS work. The Apple Container and `macos-user` backends do not use pasta.

---

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| yolo begins dictating a network option and a future podman default changes under it | Detect from `podman info` rather than assuming; emit nothing for backends we do not recognise, which is exactly today's behaviour |
| A user with an explicit `network.mode` keeps the bug and we cannot fix it for them | Warn on that path naming the reachability risk — the alternative is overriding a setting the user chose deliberately |
| `--map-host-loopback` is unavailable on an older passt | [OQ-R3](#open-questions). One host command settles whether this is hypothetical |
| The fix is unverifiable in a nested jail (§5), so it lands untested | Verification is a real launch on the affected host plus new integration coverage; do not accept a nested-jail green as evidence |
| `podman info` per launch adds startup latency | It is one subprocess on the host side; measure before caching, and cache in the boot baseline if it matters |

---

## 8. Sequencing

**First, the in-jail probe (§5).** It is independent of the fix, it is what makes the outage visible,
and it is what will prove the fix worked. Building it first means the fix has a witness.

**Second, the network option (§4)**, gated on `podman info`, on the default path only.

**Third, integration coverage** that asserts a jail can actually reach a published endpoint — the gap
that let this ship.

**Fourth, the `AGENTS.md` carve-out**, so the next agent does not trust a nested jail here.

---

## Open Questions

### 💬 OQ-R1 — may yolo emit a network option on the default path?

Today `network.mode: "bridge"` means *"emit nothing, let podman decide"*
([`assemble.go`](../../internal/cli/run/assemble.go#L252-L259)). The fix makes it mean *"emit
`--network=pasta:--map-host-loopback,…` when podman reports pasta"* — yolo taking ownership of
something it deliberately left alone.

**What it decides:** whether the fix can live in the launcher at all. If the answer is no, the only
remaining lever is the transport itself, and §3 says that lever has nowhere to go.

The knock-on worth naming: a user who sets `network.mode` explicitly keeps full control and therefore
**keeps the bug**, and we cannot fix it for them.

_Leaning:_ **Yes, and warn on the custom path.** A transport that works only by luck of the host's
network stack is not a transport. Emitting nothing for unrecognised backends preserves today's
behaviour exactly, so the blast radius is confined to hosts we can identify.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-R2 — when a jail-facing service is unreachable at boot: warn, or fail the launch?

§5's in-jail probe needs a severity, and it cannot be deferred to `yolo check`, because a host-side
check structurally cannot test jail reachability.

**What it decides:** whether a pasta host with an old passt can launch a jail at all.

_Leaning:_ **Loud warning naming the service, not a fatal.** A jail with no journal bridge is still a
working jail. But the broker's case is *"your Claude auth is down"*, which is closer to fatal than the
others — so if any service earns a different severity, it is that one, and that may argue for a
per-service level rather than one global rule.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-R3 — if the host's passt predates `--map-host-loopback`, what then?

Two options, and they are not equivalent: **AF_UNIX on Linux**, which reopens
[`loophole-transport.md`](./loophole-transport.md) §7.4 — a decision this repo retired *on purpose*,
so it needs an amendment rather than a quiet workaround — or **refuse to launch** with a message
naming the passt version needed.

**What it decides:** whether this fix stays contained to the launcher or reopens the transport
question. Worth pre-deciding rather than picking under pressure mid-implementation.

> [!TIP]
> **This collapses to a fact, and it cannot be obtained from inside a jail** (no pasta here, and
> `yolo-ps` is down because of this very bug). One command on the host settles it:
> ```bash
> pasta --version
> ```

_Leaning:_ **Refuse to launch, and say what is needed.** Reviving a retired transport to serve one
old passt build is a large, permanent cost for a shrinking population; a clear refusal names an
upgrade the user can actually perform. If the affected population turns out to be large, the
amendment is the honest path — but that is evidence we do not yet have.

**Answer:**
> _(empty — fill in when decided)_
