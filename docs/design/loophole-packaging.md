---
title: "Loophole packaging — the two questions still open"
date: 2026-08-13
status: open-questions-only
tags: [loopholes, packs, config, guest]
summary: "A stub. The loophole packaging design is built and its as-built account is docs/reference/loophole-system.md; what stays here are the two questions that never got a ruling — whether a pack-shipped loophole may declare conditional jail environment, and whether the `guest` notch gets a field census of its own."
---

# Loophole packaging — the two questions still open

> [!IMPORTANT]
> **The design is BUILT, and this file is no longer where it is described.**
> [`../reference/loophole-system.md`](../reference/loophole-system.md) is the as-built
> account: the `loophole` contribution kind, the activation model, the pack-shipped subset,
> the crossing enumeration, the install/enable scope split, the placement rule, and the
> Decision Ledger where every settled `OQ-LP` and `OQ-A` id resolves. The transport is
> [`../reference/loophole-transport.md`](../reference/loophole-transport.md); the wire is
> [`../reference/loophole-protocol.md`](../reference/loophole-protocol.md).
>
> **This stub exists for one reason:** a reference doc may not carry a live question, and
> two of this design's questions have never been ruled. They keep their ids and this
> filename so that [`../plans/roadmap.md`](../plans/roadmap.md)'s
> [💬 27](../plans/roadmap.md#-27--conditional-env-and-whether-guest-gets-its-own-field-census)
> links keep resolving.

Both are cheap to rule and expensive to discover later. Neither blocks anything shipped.

## Open questions

#### <a id="oq-lp5"></a>💬 **[OQ-LP5](#oq-lp5)** — does `jail_env` stay refused for pack-shipped loopholes?

The pack-shipped subset refuses `jail_env` because it emits container environment variables
into the same target namespace the `env` contribution kind claims, and cross-kind collisions
are not detected — the footprint's collision key is `{kind, target}`, so two *different*
kinds claiming one target can never collide. The refusal buys yolo out of a fourth bespoke
collision pass.

**The cost is no longer hypothetical: the shipped `audio` pack pays it.** A loophole's
`jail_env` is *conditional on the loophole being active*; the `env` kind is unconditional. So
`PULSE_SERVER` and `PIPEWIRE_REMOTE` are declared through `env` and set on **every** launch
that selects the pack, including on a machine where no socket ever crossed.

One tempting justification does **not** hold and should not be re-offered: the kinds'
namespaces are not disjoint by luck. `program` and `launch` already share the bin-name target
namespace by design, so nothing is being *preserved* — what is being avoided is the collision
pass itself, which is purely additive.

**What the answer decides:** whether a pack can ever set an environment variable
*conditionally on a loophole actually activating*, or whether "the pack is selected" stays the
only granularity yolo offers. Every future pack whose loophole is predicate-gated inherits
this.

**Verified 2026-09-09.** The refusal is `packJailEnvProblems` in `internal/loopholedecl`
(`packshipped.go`), whose own doc comment names this question and this resolution path;
`packs/audio/pack.json` does declare both variables through the `env` kind, so the cost is
paid in the shipped tree rather than in prospect.

_Leaning:_ **keep the refusal.** `audio` wants conditional env and tolerates the
unconditional form, which is the whole evidence base — a cost one consumer absorbs is not yet
a reason for a cross-kind collision pass. Revisit at the first pack that **cannot** absorb it.

**Answer:**
> _(empty — fill in when decided)_

#### <a id="oq-lp7"></a>💬 **[OQ-LP7](#oq-lp7)** — does `guest` get its own field census, or keep borrowing `HostFields()`?

A loophole is **incoherent at the `host` target** — it is a host daemon whose only client is a
container, so with no jail there is no client and nothing for the endpoint file to be mounted
into — and it is **coherent at `guest`**, which is a real process on the real machine under an
LSM or Seatbelt profile. `Target.Fields()` nonetheless funnels both into the host field set.

**Verified 2026-09-09, and the shape has improved without the question closing.**
`Target.Fields()` in `internal/render` (`fieldset.go`) is no longer an if-jail-else-host: it
is a switch that **names** `KindGuest` in its `default` branch, deliberately, so the
over-permission is on the record rather than a fallthrough. Its own comment states the half
that is settled and the half that is not — `guest` must not fall into the *jail* set, which
would honor `mount` / `reads-host` / `state` at a notch with no mount namespace to honor them
with, and its real census is the guest notch's own work to state. So the fail-closed direction
is chosen and built; what is still open is only whether `guest` ever gets a census of its own.

**What the answer decides:** the shape of the first loophole on a no-VM, separate-user
backend — decided deliberately rather than discovered by whoever writes it. It blocks nothing
shipped, because that backend starts no host services at all.

_Leaning:_ **split the census when the guest notch lands, and not before.** The funnel is
wrong for a reason, but inventing a third field set with zero consumers is how a vocabulary
grows faster than the system it describes.

**Interaction to respect:** this is the same `guest` notch as the environment manager's
unbuilt phase ([💬 7](../plans/roadmap.md#-7--macos-and-the-environment-manager-stories)), so
the two are meant to be ruled in one sitting.

**Answer:**
> _(empty — fill in when decided)_
