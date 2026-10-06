---
title: "Loophole packaging — the two questions still open"
date: 2026-08-13
status: in-review
stage: DESIGN
next: "Rule OQ-LP5, whether a pack-shipped loophole may declare jail_env; OQ-LP7 is meant for the same sitting as the guest-notch questions in environment-manager-user-stories.md"
tags: [loopholes, packs, config, guest]
summary: "A stub. The loophole packaging design is built and its as-built account is docs/reference/loophole-system.md; what stays here are the two questions that never got a ruling — whether a pack-shipped loophole may declare conditional jail environment, and whether the `guest` notch gets a field census of its own."
---

# Loophole packaging — the two questions still open

**Status:** 2026-10-04 — a stub that owes two rulings, plus one implementation decision taken
beside the first ([LP-D1](#LP-D1)). The design itself graduated on 2026-08-13 and its as-built
account is [`../reference/loophole-system.md`](../reference/loophole-system.md); what stays here is
the two questions that never got a ruling.

**Needs your ruling:** [OQ-LP5](#OQ-LP5), [OQ-LP7](#OQ-LP7).

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
> filename so that every inbound link to them keeps resolving.

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

## Background to the two questions

### `jail_env` and the pack-shipped subset

Background to [OQ-LP5](#OQ-LP5).

The pack-shipped subset refuses `jail_env` because it emits container environment variables
into the same target namespace the `env` contribution kind claims, and cross-kind collisions
are not detected — the footprint's collision key is `{kind, target}`, so two *different*
kinds claiming one target can never collide. The refusal buys yolo out of a fourth bespoke
collision pass.

**The shipped `audio` pack paid this cost until 2026-10-04, and no longer does.** A loophole's
`jail_env` is *conditional on the loophole being active*; the `env` kind is unconditional. So
`PULSE_SERVER` and `PIPEWIRE_REMOTE`, declared through `env`, were set on **every** launch that
selected the pack, including on a machine where no socket ever crossed. That was worse than a dead
pointer at the host: `yolo host -- claude` handed the agent `PULSE_SERVER=unix:/run/pulse/native`,
and libpulse given that value never tries the host's own `$XDG_RUNTIME_DIR/pulse/native`, so
selecting the pack broke the host's audio (MEASURED 2026-10-04 with `sox`). The macos-user
sandbox got both variables too.

[LP-D1](#LP-D1) closed that without `jail_env`: the pack's `env` names its loophole as
`served_by`, and a loophole that binds sockets or devices into a jail counts as served exactly
where a launch's container argv carries its binds. So the variables now follow the binds, which
is the condition `jail_env` would have given them. What is left of the cost is narrower: a pack
whose variables should follow a loophole that neither runs a jail daemon nor binds anything into
the jail has no name to give `served_by`. No shipped pack has one.

One tempting justification does **not** hold and should not be re-offered: the kinds'
namespaces are not disjoint by luck. `program` and `blocked-tool` already share the bin-name
target namespace by design (a tool can be both blocked and pack-declared, and gets a blocker and
a launcher), so nothing is being *preserved* — what is being avoided is the collision pass
itself, which is purely additive. (This sentence used to name `launch` as `program`'s partner;
that kind was retired on 2026-09-12, its flags moving into the `autonomy` kind's postures, and
the argument is unchanged by the substitution.)

**Verified 2026-09-09, re-checked 2026-09-24 and 2026-10-04.** The refusal is
`packJailEnvProblems` in `internal/loopholedecl` (`packshipped.go`), whose own doc comment names
this question and this resolution path. `packs/audio/pack.json` declares both variables through
the `env` kind, with `served_by: "audio"` since [LP-D1](#LP-D1).

### `guest` and `HostFields()`

Background to [OQ-LP7](#OQ-LP7).

A loophole is **incoherent at the `host` target** — it is a host daemon whose only client is a
container, so with no jail there is no client and nothing for the endpoint file to be mounted
into — and it is **coherent at `guest`**, which is a real process on the real machine under an
LSM or Seatbelt profile. `Target.Fields()` nonetheless funnels both into the host field set.

**Verified 2026-09-09, re-checked 2026-09-24, and the shape has improved without the question closing.**
`Target.Fields()` in `internal/render` (`fieldset.go`) is no longer an if-jail-else-host: it
is a switch that **names** `KindGuest` in its `default` branch, deliberately, so the
over-permission is on the record rather than a fallthrough. Its own comment states the half
that is settled and the half that is not — `guest` must not fall into the *jail* set, which
would honor `mount` / `reads-host` / `state` at a notch with no mount namespace to honor them
with, and its real census is the guest notch's own work to state. So the fail-closed direction
is chosen and built; what is still open is only whether `guest` ever gets a census of its own.

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

## Open questions

#### <a id="OQ-LP5"></a>💬 **[OQ-LP5](#OQ-LP5)** — does `jail_env` stay refused for pack-shipped loopholes?

<!-- vantage: question id=OQ-LP5 leaning="Keep the refusal: audio wants conditional env and tolerates the unconditional form, and a cost one consumer absorbs is not yet a reason for a cross-kind collision pass. Revisit at the first pack that cannot absorb it." -->

**What the answer decides:** whether a pack can ever set an environment variable
*conditionally on a loophole actually activating*, or whether "the pack is selected" stays the
only granularity yolo offers. Every future pack whose loophole is predicate-gated inherits
this.

Why the refusal exists, what the shipped `audio` pack pays for it, and the justification not to
re-offer are in [`jail_env` and the pack-shipped subset](#jail_env-and-the-pack-shipped-subset).

_Leaning:_ **keep the refusal.** `audio` wants conditional env and tolerates the
unconditional form, which is the whole evidence base — a cost one consumer absorbs is not yet
a reason for a cross-kind collision pass. Revisit at the first pack that **cannot** absorb it.

**Since 2026-10-04 that consumer no longer pays the cost at all** ([LP-D1](#LP-D1)): `audio`'s
variables follow its binds through `served_by`, so the evidence base for the leaning is now no
shipped consumer rather than one that tolerates it. The leaning is unchanged; what is left of the
cost is in [the background](#jail_env-and-the-pack-shipped-subset).

**Answer:**
> _(empty — fill in when decided)_

#### <a id="OQ-LP7"></a>💬 **[OQ-LP7](#OQ-LP7)** — does `guest` get its own field census, or keep borrowing `HostFields()`?

<!-- vantage: question id=OQ-LP7 leaning="Split the census when the guest notch lands, and not before: the funnel is wrong for a reason, but a third field set with zero consumers grows the vocabulary faster than the system it describes." -->

**What the answer decides:** the shape of the first loophole on a no-VM, separate-user
backend — decided deliberately rather than discovered by whoever writes it.

Where the funnel stands in the code, and why it blocks nothing while loopholes run on
`macos-user` today: [`guest` and `HostFields()`](#guest-and-hostfields).

_Leaning:_ **split the census when the guest notch lands, and not before.** The funnel is
wrong for a reason, but inventing a third field set with zero consumers is how a vocabulary
grows faster than the system it describes.

**Interaction to respect:** this is the same `guest` notch as the environment manager's
unbuilt phase, whose open questions on what the environment manager promises at each notch are
in [`environment-manager-user-stories.md`](environment-manager-user-stories.md), so the two are
meant to be ruled in one sitting.

**Answer:**
> _(empty — fill in when decided)_

## Decision Ledger

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="LP-D1"></a>LP-D1 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* It extends [NC-D16](../plans/notch-convergence.md#NC-D16)'s **served at this notch** to a **bound loophole** (a term this ledger coins): a loophole that runs no `jail_daemon` but declares `host_bind_mounts` or `host_devices`. One is served at a notch when that notch's container command line carries its binds, decided by the predicate the bind loop already asks (`loopholes.Set.JailBoundNames`, over `admitsJailSideEffects`: active, the origin gate, the runtime's own skip). So it is never served at the host, which starts no jail, nor on macos-user, whose sandbox binds nothing. There is no new manifest field: a pack `env` contribution names the loophole with the existing `served_by`, which packdecl does not check against declared daemons. `packs/audio` declares it, so `PULSE_SERVER` and `PIPEWIRE_REMOTE` reach only a container jail that binds the loophole. Every other launch withholds them and names them, saying what they point at and why it has none (`packload.CredentialScope.UnservedEnvLines`), and `yolo check` predicts the same served set. That why is the notch's own, never a reason the launch gives for a daemon or a doorway: `yolo host --` gives a doorway's reason to every pointer a profile or platform gates, so a gated pointer at a bound loophole would otherwise tell the user that switching the loophole on opens a doorway, which no switch does for a bind. [OQ-LP5](#OQ-LP5) stays open and `jail_env` stays refused. Known leftovers: a launch with the loophole on still sets a socket's variable on a host missing that socket (`PULSE_SERVER` with no pulse socket, `PIPEWIRE_REMOTE` with no `pipewire-0`), since that bind is skipped with a warning and the pointer is not; an attach composes from the current `loopholes` switch while the running jail's binds are the ones it started with; the macos-user launch still discloses the loophole's mounts, which that backend does not make; and a launch's pack-environment banner still lists both variables as set beside the line naming them withheld, as it does for every `served_by` pointer a launch withholds | 2026-10-04 | [Background](#jail_env-and-the-pack-shipped-subset) | ✅ `TestAudioPackPointersAreDeliveredOnlyWhereTheLoopholeBinds`, `TestABoundLoopholesPointerReachesOnlyTheJailThatBindsIt`, `TestMacosUserHandsTheSandboxNoAudioPointer`, `TestHostEnvWithholdsTheAudioPointers`, `TestCheckPredictsABoundLoopholeServedWhereItsBindsGo`, `TestOnlyABoundLoopholesWithheldPointerIsWordedAsBound`, `TestABoundPointersNotchClauseOutranksTheLaunchsReason`, `TestTheHostWordsAGatedBoundPointerAsTheHostsOwn` |
