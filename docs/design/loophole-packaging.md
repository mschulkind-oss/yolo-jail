---
title: "Loophole packaging — the two questions still open"
date: 2026-08-13
status: in-review
tags: [loopholes, packs, config, guest]
summary: "A stub. The loophole packaging design is built and its as-built account is docs/reference/loophole-system.md; what stays here are the two questions that never got a ruling — whether a pack-shipped loophole may declare conditional jail environment, and whether the `guest` notch gets a field census of its own."
---

# Loophole packaging — the two questions still open

**Status:** GRADUATED, 2026-08-13 — the design is built and its as-built account is
[`../reference/loophole-system.md`](../reference/loophole-system.md). What stays here is the two
questions that never got a ruling.

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
> filename so that [`../plans/roadmap.md`](../plans/roadmap.md)'s links keep resolving — the
> compaction retired the row these two once had to themselves, and both ids are now cited by
> anchor from the *small calls that do not deserve a row* list under
> [💬 Needs you](../plans/roadmap.md#-needs-you).

Both are cheap to rule and expensive to discover later. Neither blocks anything shipped.

> [!NOTE]
> **Two pieces of the old body did NOT graduate, and are cited from Go.** They live in git
> history only — `git show 9190a4d1^:docs/design/loophole-packaging.md` is the last revision
> that carried them:
>
> - **"The finding"** (finding 2 of the old body) — the loophole commands load config with no schema
>   pass, so an entry `yolo check` rejects was honored by `yolo loopholes list` and its
>   `doctor_cmd` would have run on the host. Its live form is `config.LoopholeEntryErrors` and
>   `TestEvilDoctorWorkspaceEntryIsRefused`.
> - **risk R5** — the doc/code drift where `knownHostServiceKeys` contradicted the keys the
>   loader reads. Its live form is that census in `internal/config/config.go` and
>   `TestInlineLoopholeKeysLoaderReadsAreKnown`. ⚠ **Not to be confused with
>   [`loophole-system.md`](../reference/loophole-system.md#principles)'s own `R5`**
>   (*install is user-scope; enable is either scope*), which is an unrelated rule — a mechanical
>   repoint of the Go citations would have resolved and meant something else.
>
> Everything else the body held is in
> [`loophole-system.md`](../reference/loophole-system.md).

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
backend — decided deliberately rather than discovered by whoever writes it.

**The backend is not idle, so that is not why this blocks nothing.** `macos-user` — which
[`declaration-parity.md` §8](declaration-parity.md#8-the-guest-notch-is-not-a-backend) calls *the
guest notch by another name* — has started the WHOLE host-service set since 2026-09-17, through
the same spawn boundary a container launch uses: `run.Run`'s native arm calls
`startLoopholesDisclosed`, and `macosuser.EndpointGrantCommands` ACL-grants each published
endpoint across the uid split. So the first loophole on that backend is not a prospect — loopholes
run there today.

What blocks nothing is the FUNNEL, and it is unreachable rather than harmless: `render.KindGuest`
has no constructor, `yolo apply --at guest` returns `render.NotchUnbuilt`, `Target.Fields()` has no
production caller at all
([`DP-B34`](declaration-parity.md#53-both-axes-at-once-the-notch-the-briefing-and-the-render-target)),
and `macos-user` itself runs at `confinement: jail`, reasoning from `render.GuestProfileMacOS()`
rather than from any field census. So nothing consults the over-permission today, while a loophole
is live on the very backend this question is about.

_Leaning:_ **split the census when the guest notch lands, and not before.** The funnel is
wrong for a reason, but inventing a third field set with zero consumers is how a vocabulary
grows faster than the system it describes.

**Interaction to respect:** this is the same `guest` notch as the environment manager's
unbuilt phase — carried now by the [💬 Needs you](../plans/roadmap.md#-needs-you) row on
what the environment manager promises at each notch, whose doc is
[`environment-manager-user-stories.md`](./environment-manager-user-stories.md) — so the two are
meant to be ruled in one sitting.

**Answer:**
> _(empty — fill in when decided)_
