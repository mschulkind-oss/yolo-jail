---
title: "Host-only config contributions — the gate is missing, and the jail leak mostly is not"
date: 2026-09-27
status: accepted
stage: BUILT
next: "Record §4.3's Mac check: the 2026-10-03 scheduled macos-user.yml run (GitHub Actions run 37121866798, at 0e34798c6) passed TestMacosUserComposesARenderedHostFileAsABaseline. Then step 2 of §5, the maintainer's by hand: add the guarded posture list to the matt pack, run yolo host apply --assert on the host, and confirm pi-automode loads there and in no jail"
tags: [packs, notch, autonomy, host, config-list, config-overlay, pi, permissions]
summary: "A pack could not declare a config-list entry for the host alone, because packoverlay.Collect placed every list at every notch. A host-applied entry coming back into a jail through a readsHost mount was already prevented on podman and Apple Container by the OQ-CR6 render mark, and happened only on macos-user. Recommended, and built on 2026-09-27 on OQ-5's leaning: posture-selected lists inside the autonomy kind, where standing rulings put confinement-conditional content, plus render-mark parity on macos-user (unit-tested only)."
vantage:
  status-chip: true
---

# Host-only config contributions — the gate is missing, and the jail leak mostly is not

**Status:** 2026-09-27 — **steps 1, 3 and 4 of [§5](#5-fastest-path-to-the-motivating-case)
are built** (`bbe5c878`, `499a332f`, `a6021d86`): posture lists, render-mark parity on
`macos-user`, and the end-to-end test. Step 2 is the maintainer's, by hand, on the maintainer's own host
and in the `matt` pack, which lives outside this repository, and is not done. The one ruling this line used to say was
owed, [OQ-5](#OQ-5), was ruled as leaned on 2026-09-28, so what step 1 built stands and
[§4.2](#42-the-first-drafts-modifier-corrected) is not built. Evidence verified at
`8da7840d`; the build was made against `33c0eb18`. A review of the build found four defects, fixed
in `1300fb12`, `2ab44527`, `77dc6afa` and `22d086bd` (NS-D11 to NS-D13, and the release note). A
fifth, found by the same review, was not the build's: `yolo host apply` applied any pack whose
manifest has problems, a second `autonomy` contribution included, which every launch refuses.
Fixed in `d4aa6a43` (NS-D14). A review of that fix found four defects in it, fixed in `68505100`,
`d20f21dd`, `55600c5c` and `203b3009` (NS-D15 to NS-D18).

**MEASURED** by unit tests only, each revert-checked against its production call site: the
decode and refusals, a second `autonomy` contribution on both decode paths, `Collect` at both
bits, the jail boot loop, `yolo check`'s dry-run probe, `RenderHostPack` with its insert record,
the real `yolo host apply --assert`, `yolo config render` and `yolo config ls` at both notches,
the footprint, the notch line with a placed and an ownerless list, and one managed home driven
from the assert through each backend's launcher label to the boot render. **MEASURED on
2026-10-01 in a launched jail** (podman, nested, at `d4e435a3`):
`TestAPostureListRendersOnlyItsOwnSideOfTheLine` (`integration/configlist_test.go`) boots a jail
selecting `pi` and a `file://` pack whose `autonomy` contribution lists `npm:posture-jail-only`
under `autonomous` and `npm:posture-host-only` under `guarded`. The jail's
`~/.pi/agent/settings.json` held the first once and not the second, and `yolo config render
pi/settings --at host` from the same isolated home held the second and not the first. With the
gate in `packoverlay.Collect` disabled, the jail held both and the test failed (revert-checked).
**UNMEASURED:** `macos-user` parity is unit-tested only — no Mac has run it. No real host has run a
posture list.

> **In short.** What was missing is collection: `packoverlay.Collect` placed every `config-list`
> at every notch, so no pack could declare an entry for the host alone. An `autonomy` posture's
> `lists` now can. The jail side needed almost nothing: once yolo has written a host file, the
> render mark keeps it out of container jails, and since `499a332f` out of `macos-user` jails
> too.

**Why it matters.** A permission gate for pi (`@czottmann/pi-automode`) belongs on the host and
only costs tokens and prompts in a jail; until posture lists, a pack could add it everywhere or
nowhere.

**The shape.** One gate in `Collect` on the autonomy bit its callers already pass, declared as a
posture's `lists` in the `autonomy` kind (recommended), plus render-mark parity on `macos-user`.

**Cost.** No `Collect` signature change; `Collect`'s "the bit is inert" contract changes meaning.

**Start at [§3.2](#32-the-host-layer-is-already-a-baseline-on-container-backends)**, then
[§5](#5-fastest-path-to-the-motivating-case) for the build order.

**Rulings:** [OQ-5](#OQ-5) held as leaned, and [OQ-3](#OQ-3) was ruled "do it now" (both
2026-09-28, in review). Nothing here awaits a ruling. [OQ-3](#OQ-3) is **BUILT** in
`12032eb2`: a posture's `config` entry on a surface another pack owns is a *posture overlay*,
placed by the `config-overlay` path at the posture that selects it (NS-D19 to NS-D26), which is
how a host-only scalar exists. It is measured by unit tests and the in-process chain only, like
the rest of this build.

**Reads with:** [`notch-scoped-config-contributions-plan.md`](notch-scoped-config-contributions-plan.md)
(the implementation sketch, superseded by the build),
[`config-target-resolution.md`](../reference/config-target-resolution.md#a-staged-copy-is-not-always-a-layer)
(the render mark), and [`pack-system.md`](../reference/pack-system.md#autonomy) (posture lists and
posture overlays as built).

---

## Terms, in plain words

- **Notch.** One of yolo's selectable confinement levels, `jail`, `guest` or `host`
  (`render.SelectableNotches`). Every jail boot renders at `jail` today, on every backend:
  podman, Apple Container and `macos-user` (a dedicated macOS account under Seatbelt) all get
  `render.Jail` from `(*Env).renderTarget`. `guest` has no constructor yet (`render.KindGuest`,
  env-manager Phase 7), so nothing renders there. `host` is `yolo host` and `yolo host apply`,
  the invoking user's real home. Two kinds are not notches: `KindPreview` and `KindUnset` (a
  target no constructor built).
- **Posture.** `autonomous` (permission prompts bypassed) or `guarded` (prompts on). The notch's
  profile picks one through `AgentAutonomy` (`render.ProfileFor`): autonomous at jail, guest and
  preview; guarded at host and at unset.
- **`config-list`.** A contribution that appends entries to an array at an RFC 6901 pointer on a
  surface some selected pack owns ([pack-system.md](../reference/pack-system.md#adding-entries-to-an-array-config-list)).
- **Surface owner vs contributor.** The pack declaring `kind: "config"` owns the surface identity
  (`agent/name`), its path, codec and mode. Any other pack contributes keys through
  `config-overlay` or entries through `config-list`.
- **Host layer.** For a surface declaring `readsHost`, the user's host copy, staged read-only
  under `/ctx/` and folded as `defaults < host < workspace < …` — **unless the launcher labelled
  it a render**, in which case it is a baseline and is not folded at all.
- **Render mark** (not coined here: [`OQ-CR6`](../reference/config-target-resolution.md#oq-cr6)).
  The host provenance record a writing `yolo host apply` leaves in the home. The launcher's
  `run.hostLayerIsRender` reads it and labels that delivery `rendered` in `YOLO_HOST_LAYERS`.
- **Posture list** *(coined here)*. A `config-list` body declared inside an `autonomy` posture,
  contributing only while that posture is the selected one. It is not a new kind and not a
  modifier on `config-list`.
- **Posture overlay** *(coined here, for [OQ-3](#OQ-3)'s build, NS-D19)*. An entry of an
  `autonomy` posture's `config` that names a surface its own pack does not declare. It
  contributes keys the way a `config-overlay` does, only while that posture is the selected
  one. An entry naming the pack's own surface is not one: it folds into that surface's
  `managed` layer, as before.

---

## 1. The case, and why no channel carries it today

On 2026-09-27 a trial added `@czottmann/pi-automode@1.17.0` to the maintainer's personal pack
(`yolo-packs/matt`, outside this repository) and was reverted because it rendered in every jail.
That commit is reported, not verifiable from here; the code is consistent with it, because
`ConfigurePackSurfaces` collects every `config-list` at boot with no gate
(`internal/entrypoint/packsurfaces.go`).

1. **The host needs it.** Direct and IDE launches of pi run with the user's real privileges. A
   gate that classifies tool calls and prompts before destructive ones is what `guarded` means.
2. **A jail does not.** The jail already contains pi, and the classifier costs model tokens per
   tool call and interrupts unattended runs. The per-call cost is a pi-side fact, reported rather
   than measured here.
3. **The entry needs `config-list`.** `pi/settings#/packages` is an array, and a merge patch
   replaces arrays whole; [pack-system.md](../reference/pack-system.md#adding-entries-to-an-array-config-list)
   uses this exact surface and path as the kind's motivating case.

No declared channel could make the entry host-only at `8da7840d` (a posture list can since
`bbe5c878`, [§4.1](#41-recommended-posture-lists-inside-autonomy)):

| Channel | Why it cannot carry a host-only entry | Evidence |
| :--- | :--- | :--- |
| `config-list` | No gate at all: Pass 3 places it at every notch, and `profile` is refused on the kind | `packoverlay.Collect`; `validateContribution` |
| `config-overlay` + `profile` | Gates on a profile name, not the notch; and the patch replaces `packages` whole | `Collect` Pass 2 |
| `autonomy`'s `config` half | At `8da7840d` it folded only into surfaces the declaring pack owns — a patch naming another pack's surface was inert plus a `FoldNote`. It lands in the MANAGED layer, which outranks capture and host, and replaces arrays, so even `pi` setting `packages` this way would clobber the user's own list on every render. Since `12032eb2` ([OQ-3](#OQ-3)) a patch on another pack's surface is a posture overlay at config-overlay's slot, but a key still replaces an array whole, so it cannot carry an entry either | `(*Pack).SurfacesForReport`, `foldPostureManaged`, `mergeManagedMap`; now `packoverlay.Collect` |
| `autonomy`'s `launch` half | Already cross-pack (`launchFlagClaims`), but `InjectLaunchFlags` asked for the autonomous posture only, so no guarded flag reached a host launch (since 2026-09-28 it takes the target's posture and `yolo host --` calls it: [notch-convergence item 20](../plans/notch-convergence.md#tier-6--render)); a flag would also miss IDE launches, which still holds | `packload.InjectLaunchFlags` |
| a derive | `luahook.DeriveCtx` carries no notch, and `computed` replaces arrays | `internal/agentcfg/luahook/derive.go` |
| a host-only `packs` list | `packs` is one user-scope list for both notches; no `host_*` key selects packs | `internal/config` |

`autonomy` is one declaration per pack (`CombineExclusive`), and **any** pack may make one; what
confined its `config` half to the pack's own surfaces was `foldPostureManaged`, not the combine
rule. Since [OQ-3](#OQ-3)'s build nothing confines it: a patch on another pack's surface is
`packoverlay.Collect`'s, as a posture overlay (NS-D19).

---

## 2. Load-bearing principles

- **P1. Content conditional on confinement lives in `autonomy`.** The notch decides the posture;
  a pack declares what each posture means. Env-manager [OQ-11](yolo-as-environment-manager.md#9-decision-ledger)
  chose a dedicated kind over a `when`-discriminator so a conditional key cannot sit in the
  always-on half by accident, and [PV-OQ-1](../reference/providers.md#pv-oq-1) rules that
  *"the confinement-conditional keys live in `autonomy` and nowhere else"*. Env-manager
  [§4.2](yolo-as-environment-manager.md#42-agent-autonomy-is-a-confinement-policy-not-baked-pack-config)
  names this direction for pi by name: pi is permissive by default, so *"the `host` notch must
  add a restriction"* — and a permission gate is that restriction. Conditioning on the notch for an
  environment dependency instead (host Docker, a native keychain) has no shipped or reported
  case; it is part of [OQ-5](#OQ-5), not a principle.
- **P2. Contributors never own surfaces, and owners never anticipate every contributor.** An
  agent pack owns its surface; a personal or team pack (`matt`, a company pack) contributes to
  it. The lever a contributor needs must not require editing the owning pack.
- **P3. Symmetry between `config-overlay` and `config-list` is not a given.** It is already
  broken by design: `profile` gates `config-overlay` and is refused on `config-list`. Whether a
  host-only scalar should exist was [OQ-3](#OQ-3), ruled yes and built as the posture overlay
  (NS-D19): the posture gates a key the way it gates a list, and neither kind grows a modifier.
- **P4. A host-only entry never reaches a jail through the host file.** On podman and Apple
  Container the render mark already holds this
  ([§3.2](#32-the-host-layer-is-already-a-baseline-on-container-backends)); on `macos-user` it does
  not until parity ([§4.3](#43-render-mark-parity-on-macos-user)).
- **P5. Backward compatible, fail-closed where the notch is unknown.** No gate means
  unconditional, as today. A misspelled field is a fatal authoring error on the host, where
  manifests decode strictly. At `KindUnset` a permission gate stays in. Under version skew a jail
  must drop, never widen, a conditional entry ([§4.5](#45-failure-paths)).

**Standing rulings this design is held to:**

| Ruling | Where it lives | What it requires here |
| :--- | :--- | :--- |
| Nothing leaks between jails; a launch's result never depends on other launches | [pi-git-extension-caching OQ-2](pi-git-extension-caching.md#OQ-2), [OQ-BR4](../reference/providers.md#oq-br4) | The gate is a pure function of the render target's notch; no cross-launch state |
| The credential gate | [providers.md](../reference/providers.md#the-credential-gate); [CN-D3, CN-D13](provider-credential-scope.md#7-decision-ledger) | No interaction: host pi runs the classifier on the user's inherited environment, and no provider or `env_sources` value changes |
| Escape hatches are for broken user config; an acknowledgment override only where yolo knows the environment differs | [attach-skew-and-contract-guardrails.md](attach-skew-and-contract-guardrails.md#decision-ledger) | None is needed, and none is proposed |
| No post-merge script slot; a declarative filter only against a real case | [OQ-LT2](../reference/pack-system.md#oq-lt2) | Rules out any Lua or finalize hook here; see the sanitizer, [§6](#6-alternatives-considered) F |
| Core knows notch NAMES only at render's two edges | [pack-system.md §6c](../reference/pack-system.md#batch-6c) | A manifest that spells `"host"` adds a third edge |

---

## 3. What exists today

### 3.1 Collection is notch-blind

`packoverlay.Collect(packs, autonomy, profiles)` runs three passes:

1. **Owners.** Each pack's `SurfacesFor(autonomy)` names the surfaces it owns.
2. **Overlays**, in pack order then declaration order. The `profile` gate sits after
   `manifest.DecodeOverlay` and before the owner check, so a malformed body is reported at every
   profile and a profile-inactive overlay is a clean skip rather than an orphan.
3. **Lists**, the same order, through `agentcfg.NewListContribution`, with **no gate**. An
   ownerless one becomes an `OrphanOverlay` (ruling R2, inert and reported).

Every production caller already holds its notch and passes that notch's bit:

| Caller | Notch it renders | Bit it passes |
| :--- | :--- | :--- |
| `entrypoint.ConfigurePackSurfaces` (every jail boot) | jail | `e.renderTarget().Profile().AgentAutonomy` |
| `entrypoint.ConfigurePackByName` (`yolo check`'s dry-run probe; production code despite its file name, `packrender_test_support.go`) | jail | the same |
| `applyHostSurveyed` (`yolo host apply`, `internal/cli/apply.go`) | host | `render.Host(…).Profile().AgentAutonomy` |
| `renderContributions` (`yolo config render`) | `t.notch` | `render.ProfileFor(t.notch).AgentAutonomy` |
| `overlayContributionRows` (`yolo config ls`) | `t.notch` | the same |
| `loadPromoteFold` (`yolo config promote`) | jail, fixed | `render.ProfileFor(render.KindJail).AgentAutonomy` |

`Collect`'s doc comment calls taking a `render.Profile` instead of one bit "a deliberate boundary
rather than a leftover", and states that the bit's effect on the output is zero, pinned by
`TestCollectAutonomyDoesNotChangeTheResolution`. A gate on the bit keeps the first contract and
ends the second. **As built (`bbe5c878`)** the comment says the bit never changes identities or
ownership and does select posture lists; the test pins the first half, and
`TestCollectAutonomySelectsPostureLists` beside it pins the second.

### 3.2 The host layer is already a baseline on container backends

A host-applied entry does not come back into a podman or Apple Container jail through the
`pi/settings` host layer. The render mark that prevents it shipped at `369c6f63` (2026-09-18):

1. **The write.** `yolo host apply` is a dry run and `--assert` writes; `yolo host -- <agent>`
   also re-applies a stale render when `host_apply_on_launch` is on. With `host_management` unset (it resolved to `assert`
   in `config.hostManagementValue` when this was written, and resolves to `none` since 2026-10-05), `RenderHostPack` took the rmw arm
   (`renderSurfaceRMWSurface`): entries go in through `agentcfg.ReconcileInsertedList`, then
   the insert record (`writeListRecord`, `<ProvenanceDir>/pi-settings.list-record.json`) and
   the provenance record (`writeProvenanceRecord`) are written. `own` writes provenance too.
   `none` refuses the apply and writes nothing (`hostManagementRefusal`).
2. **The label.** At the next launch `(*Options).hostFileArgs` stages `~/.pi/agent/settings.json`
   at `/ctx/host-pi/settings.json` (a `:ro` bind on podman, a copy on Apple Container), and
   `hostLayerIsRender` asks `entrypoint.HostSurfaceRendered` whether the home carries the mark.
   If it does, the destination goes out as `Rendered` on `YOLO_HOST_LAYERS`
   (`entrypoint.HostLayerWire`, from `(*Options).hostLayerEnv`).
3. **The read.** In the jail, `hostSurfaceBytes` checks the label **before** reading the file.
   On `HostLayerRender` it returns no bytes, and the surface "composes from its packs and its
   capture alone".

So once an assert has written `pi/settings`, **none** of the host file reaches a container jail:
not the automode entry, and not the user's hand-added packages either. Pinned by
`TestBootDoesNotComposeAHostLayerLabelledARender`, `TestHostSurfaceRenderedReadsTheProvenanceMark`
and `TestRenderDropsAHostLayerTheLaunchLabelledARender`, all green at `8da7840d`.

In a home yolo never asserted into, no yolo-inserted entry can be in the host file, because only
an assert inserts. What is there is the user's, and composing it is the onboarding path the host
layer exists for ([`OQ-CR8`](../reference/config-target-resolution.md#oq-cr8)).

> [!WARNING]
> **Filtering the host file inside the jail is the intuitive fix, and it is aimed at the wrong
> layer.** On the container backends those bytes are already discarded, and in a home yolo never
> wrote they are the user's own ([§6](#6-alternatives-considered), F).
>
> **`macos-user` was the one backend where the leak was real, until `499a332f`.**
> `macosuser.hostLayerWire` marshalled a bare `packload.HostLayerReport` with no `Rendered`, and
> `(*Options).buildMacosCtxTree` never called `hostLayerIsRender`, so a managed home's host file
> composed as a layer there
> ([gap 1](../reference/config-target-resolution.md#where-this-does-not-reach)), and a
> host-applied automode entry would have reached a `macos-user` jail. The fix was parity with the
> container path ([§4.3](#43-render-mark-parity-on-macos-user)), not a sanitizer. The same
> section's gap 2 — no test drove one managed home through the label and the boot read
> together — is closed below a real launch by `a6021d86`.

`ReconcileInsertedList` also withdraws: an entry yolo inserted and no longer contributes is removed
from the host file on the next assert, and an identical entry the user already had is left
unrecorded and never removed.

### 3.3 A pending change to the default

A 2026-09-20 ruling retires `assert` and makes an unset `host_management` mean `none`
([built 2026-10-05](../reference/config-target-resolution.md#ruled-2026-09-20-not-built-retiring-assert)).
Only `own` writes a host-only entry now. The mark survives it: `own` still marks, and a mark an
earlier assert left stays until `yolo host apply --revert`, which runs under `none`.

---

## 4. Proposed solution

Two shapes carry the same gate. [§4.1](#41-recommended-posture-lists-inside-autonomy) is the
recommendation; [§4.2](#42-the-first-drafts-modifier-corrected) is the draft's modifier, corrected
against the tree. [OQ-5](#OQ-5) chooses between them. [§4.3](#43-render-mark-parity-on-macos-user)
to [§4.6](#46-what-done-looks-like) hold for both.

### 4.1 Recommended: posture lists inside `autonomy`

> [!NOTE]
> **Recommendation (review, 2026-09-27), pending [OQ-5](#OQ-5). BUILT on that leaning in
> `bbe5c878`.** The first draft proposed
> [§4.2](#42-the-first-drafts-modifier-corrected) and rejected this shape on two facts that do not
> hold ([§6](#6-alternatives-considered), C). This is the shape the standing rulings already
> prescribe. The mechanism choices the build made are NS-D4 to NS-D8 in the
> [ledger](#10-decision-ledger), and the review's fixes NS-D11 to NS-D13.

```json
{
  "kind": "autonomy",
  "guarded": {
    "lists": [
      { "surface": "pi/settings", "path": "/packages",
        "add": ["npm:@czottmann/pi-automode@1.17.0"] }
    ]
  }
}
```

- **Selector.** The posture `Collect`'s `autonomy` argument already selects (`PostureFor`).
  `guarded.lists` contribute at host and at `KindUnset`; `autonomous.lists` at jail, guest and
  preview. No notch name appears in a manifest.
- **Validation.** Each entry takes `surface`, `path` and `add` under `config-list`'s own rules
  (`configListProblems`, `configListPathProblems`). A posture holding only `lists` is a valid
  posture. The host refuses an unknown field (strict `packdecl.Decode`).
- **Collection.** A posture list takes the existing list path: `NewListContribution`, the owner
  check, `listsByTarget`. An ownerless one is an orphan reported under the `autonomy` kind. Order
  is the existing rule, pack order then declaration order
  ([config-list-order](../reference/pack-system.md#config-list-order)).
- **Gate position.** Both postures' lists are decoded at every notch, so a malformed entry is
  reported wherever it would or would not render. The gate sits after the decode and before the
  owner check, so an unselected posture's list is a clean skip: no problem, no orphan, no applied
  row — the `profile` gate's behavior.
- **The inertness contract.** After this the bit still never changes surface identities or
  ownership, and it does select posture lists. `Collect`'s comment and
  `TestCollectAutonomyDoesNotChangeTheResolution` are rewritten to say so; the test keeps
  pinning identities.
- **Disclosure.** `yolo pack footprint` claims the list unconditionally and names the posture in
  the detail (the `profile` modifier's precedent in `footprint.go`) — as built, on the pack's
  `autonomy` claim, e.g. `guarded appends 1 entry: "npm:…" to pi/settings#/packages` (NS-D4).
  `yolo host apply`'s notch line counts a posture list as a fold (`surveyNotchFacts.AutonomyFolds`),
  so it never says "nothing folds" for a pack whose guarded posture is lists only. As built, it
  counts only a list `Collect` placed (NS-D12): an ownerless one is reported "no effect" one line
  above and folds nothing.
- **Inspection.** `yolo config ls` and `yolo config render` follow with no change: each already
  passes `render.ProfileFor(t.notch).AgentAutonomy`, so `--at host` and `--at jail` differ.
- **No collision.** `autonomy`'s "never collides across packs" survives, because a list only
  appends and dedups (`entryKey`); two packs' posture lists cannot contend the way two config
  writers would.

### 4.2 The first draft's modifier, corrected

The draft put `notches: ["host"]` (or a singular `notch`) on `config-list` and `config-overlay`.
[OQ-1](#10-decision-ledger)'s decision applies to it too, so its corrected form is one spelling of
one field: a `posture: "guarded"` modifier. What building it takes:

- **Validation** in `validateContribution`, one helper shared by both kinds, and refusal on every
  other kind with the `profile` modifier's pattern. There is no `configOverlayProblems`; overlay
  validation is inline in the kind switch.
- **Notch names, if kept, cost a third edge.** `packdecl` cannot import `render`
  (`render/fieldset.go` imports `packdecl`; `packdecl` is dependency-free), and
  `render.IsValidNotch` does not exist. `packdecl` would carry its own name list, pinned to
  `SelectableNotches` by a test in `internal/render` like `notchnames_test.go`, and the collector
  would map names through `render.KindForNotch` once and compare Kinds. And `guest` has no
  constructor, so `notches: ["guest"]` would validate and never match: refuse it until Phase 7.
- **Gate position** as in [§4.1](#41-recommended-posture-lists-inside-autonomy): after
  `DecodeOverlay` or `NewListContribution`, before the owner check.
- **A signature change.** A `render.Kind` parameter reaches every caller in
  [§3.1](#31-collection-is-notch-blind) and every test caller; the boot passes
  `e.renderTarget().KindOf()`, never a literal.
- **Two rulings amended.** [PV-OQ-1](../reference/providers.md#pv-oq-1) and env-manager
  [OQ-11](yolo-as-environment-manager.md#9-decision-ledger).
- **Skew fails open** ([§4.5](#45-failure-paths)).

### 4.3 Render-mark parity on `macos-user`

Needed whichever shape [OQ-5](#OQ-5) picks, and an implementation decision under the existing
[`OQ-CR6`](../reference/config-target-resolution.md#oq-cr6) ([ledger](#10-decision-ledger), NS-D3):
compute `Rendered` in `(*Options).buildMacosCtxTree` with the same `hostLayerIsRender`, carry it
on `macosuser.HostContext`, and have `macosuser.hostLayerWire` marshal `entrypoint.HostLayerWire`
(`internal/macosuser` already imports `internal/entrypoint`, so there is no cycle). This closes
gap 1 for every `readsHost` surface on that backend, not just this entry, and it keeps the
jail's reading identical to the container path's. **BUILT in `499a332f`, exactly so.** It is
unit-tested only: `TestMacosUserLaunchLabelsAHostFileYoloHasRendered` drives a real `Run()` of
the `macos-user` arm, and `TestHostLayerReportCarriesTheRenderLabel` reads the plan's wire back
through the boot's reader. No Mac has run it. The same commit hides the machine's host-render
mark in `TestMacosUserDeliversHostBytesByCopy` (NS-D9), which the label would otherwise turn red on
the self-hosted Mac. **The Mac check is written** (2026-10-01):
`TestMacosUserComposesARenderedHostFileAsABaseline`
([`macosuserrendermark_test.go`](../../integration/macosuserrendermark_test.go)), which the
scheduled `macos-user.yml` job runs on a GitHub-hosted Mac. It launches once with no mark, where
the user's own key must reach the sandbox's `settings.json`, then plants this home's mark in a
private state dir and launches again, where the key must not. It asserts bytes and no line.
The "baseline and not a layer" note was discarded on this backend when the test was written,
because the macos-user bootstrap kept no boot log. Since 2026-10-04 it keeps the container's
`<workspace>/.yolo/boot.log` and the note lands there, but the bytes are still the assertion.
UNMEASURED until that job runs it.

### 4.4 Behavior at each target, and the degenerate cases

| Target | `AgentAutonomy` | `guarded.lists` | `autonomous.lists` | Draft's `notches: ["host"]` |
| :--- | :--- | :--- | :--- | :--- |
| host (`yolo host apply`, `yolo host`) | off | contribute | skipped | contribute |
| jail (every backend's boot, `yolo check`'s probe) | on | skipped | contribute | skipped |
| preview (`KindPreview`; no `Collect` caller passes it today) | on (the jail's policy) | skipped | contribute | unstated in the draft |
| guest | on | nothing renders there yet (`render.NotchUnbuilt`) | the same | validates, never matches |
| unset | off (`HostProfile`) | contribute: the gate stays in | skipped | skipped: the gate is dropped |

- **Empty.** `lists: []` and an entry with `add: []` are declared no-ops, as `add: []` already is.
- **The same entry in both postures** contributes at every target, exactly as an ungated list.
- **The same entry from an ungated list and a posture list**, or from two packs, is written once
  and recorded once (`entryKey`; `ReconcileInsertedList` skips a present entry).
- **Owner not selected.** An orphan, reported only at a target that selects the posture. The
  host apply's notch line does not count it as a fold (NS-D12).
- **Two `autonomy` contributions in one pack**, one per posture. `yolo pack lint`, `yolo check`
  and the launch refuse the second; a jail's read skips it with a note (NS-D11). Before
  `1300fb12` it decoded clean and its lists rendered nowhere. `yolo host apply --assert` refuses
  the pack too and writes nothing (NS-D14); until `d4aa6a43` it dropped the problem and applied
  the rest of the pack.
- **Contributor dropped, owner kept.** The next assert removes the inserted entry
  (`ReconcileInsertedList`).
- **Owner and every contributor dropped.** The entry stays in the host file
  ([`OQ-AL3`](../reference/pack-system.md#oq-al3)); `yolo host apply --revert` removes it for a
  shipped owner. The mark still keeps the file out of container jails.
- **The user already had the entry** before the first assert: it is theirs, unrecorded, and never
  removed.
- **The user deletes the yolo-inserted entry** from the host file: it moves to the record's
  `Declined` list and is not re-added.

### 4.5 Failure paths

| Step | Failure | What happens | Who finds out |
| :--- | :--- | :--- | :--- |
| Host manifest read | Host yolo older than the new field (`bbe5c878`), and so older than NS-D14 (`d4aa6a43`) | `yolo host apply --assert` and `yolo host --` exit 0 with the declaring pack read as an empty manifest, so the posture list and everything else that pack declares are absent from the real home and nothing says why. `yolo check`, `yolo pack lint` and a jail launch refuse the manifest by name | The user, only at a jail launch or a check. Order: `just install` before the pack change |
| Jail manifest read | Entrypoint older than the new field (the host CLI and the flake bundle out of step) | `DecodeTolerant` ignores unknown fields. A posture's `lists` reads as absent (fail closed). A modifier reads as absent, so its list is **unconditional** (fail open, the exact leak) | Nobody, for the modifier |
| Either notch | A yolo older than the posture overlay (`12032eb2`) reads a posture `config` entry on another pack's surface | The schema did not change, so the manifest decodes, and the entry folds nowhere: the key renders at no notch (fail closed). That build prints a "folded nowhere" note only when the declaring pack declares a surface of its own | The note, when there is one; otherwise nobody. `just install` before the pack change |
| Host apply | `host_management: none` | Refused, nothing written | The refusal names the key |
| Host apply | The `assert` retirement lands ([§3.3](#33-a-pending-change-to-the-default)) | Unset becomes `none`; only `own` writes | The done conditions name `own` |
| Host apply | Only ever a dry run | Nothing written and no mark; the host file holds only the user's own entries, so the jail composing it as a layer adds nothing yolo put there | — |
| `macos-user` boot | Host yolo older than [§4.3](#43-render-mark-parity-on-macos-user) (`499a332f`); the label is computed host-side, by the launcher | The host file composes as a layer; an asserted entry reaches the jail | Nobody; `just install` ends it |
| First host pi start | The `npm:` package is not installed | pi installs a missing package at resolve time (read from pi 0.87.1's source by the review, not run) | — |
| First host pi start | npm or the registry is unavailable | That install has no catch on pi's startup path, so the start fails outright rather than degrading (read from pi 0.87.1's source, not run; [host-computed-layer §8.2](host-computed-layer.md#82-read-from-source-not-run)) | The user, at the first start |
| Host pi, direct or IDE launch | pi uses `openai-codex` | The delivered extension's login and refresh fail, because only `yolo host --` sets the host socket. Since the fix (`brokerFailure` in `yolo-openai-auth.js`, pinned by `TestPiOpenAIAuthOutsideAJailSaysToLaunchThroughYoloHost`) the error says to launch pi with `yolo host -- pi`, with the client's own words after it; it used to name only the jail's endpoint variable. Inside a jail the client's message stands (MEASURED through pi 0.87.1's own extension loader; [host-computed-layer §8.1](host-computed-layer.md#81-measured)) | The user, at the first refresh |
| Host pi, any tool call | No classifier model configured | pi-automode classifies with the current session model, and blocks the action only when there is no session model or its provider fails, as it does in the row above (read from pi-automode 1.17.0's source, not run) | The user, at the first blocked tool call |
| Host pi | The package's own skill (`automode-diagnostics`, as reported) | Loads with the package, so it is host-only too | — |

### 4.6 What done looks like

Each item's state as of `77dc6afa`: ✅ built and unit-tested, ⏳ not run anywhere real.

1. ✅ `yolo host apply` lists `pi/settings` with a list entry attributed to the contributing
   pack; `yolo host apply --assert` writes it into `~/.pi/agent/settings.json` and into the
   insert record — under `own`, since the `assert` retirement made an unset key `none`
   (`TestHostApplyAssertWritesAGuardedPostureList`,
   `TestHostApplyInsertsOnlyTheGuardedPostureList`). ⏳ No real host has run it.
2. ✅ A jail's `~/.pi/agent/settings.json` lacks the entry on every backend, `macos-user`
   included (`TestJailBootRendersOnlyTheAutonomousPostureList`, and through the host file
   `TestAnOwnedHomesHostFileIsABaselineFromTheApplyToTheBoot`, which was
   `TestAManagedHomesHostFileIsABaselineFromTheAssertToTheBoot` until the `assert` retirement).
   ⏳ No launched jail has
   shown it.
3. ✅ `yolo config ls --at host` shows the entry and `--at jail` does not; `yolo config render`
   agrees (`TestConfigRenderAndLsFollowAPostureListsNotch`). The first draft wrote
   `yolo config ls pi`; `ls` takes no agent.
4. ✅ `yolo pack footprint` names the posture in the autonomy claim, and `yolo host apply`'s
   notch line reports a fold for a placed list and none for an ownerless one
   (`TestFootprintNamesAPostureListUnderItsPosture`,
   `TestHostApplyNotchLineCountsAPostureListAsAFold`,
   `TestHostApplyNotchLineDoesNotCountAnOrphanedPostureList`). A second `autonomy` contribution
   never reaches the footprint (`TestASecondAutonomyContributionNeverReachesTheFootprint`).
5. ✅ One test drives a managed home through the launcher's label and the boot read (gap 2),
   below a real launch: the container runtime's bind is the one step it copies by hand.
6. ✅ Each test fails when its production call site is deleted: the gate in `Collect`, the
   boot's, `yolo check`'s probe's and the host apply's calls, `config render` and `config ls`,
   and the `macos-user` label — each revert-checked. `config promote` also passes the bit, and
   reads only overlays, where the bit selects nothing.

---

## 5. Fastest path to the motivating case

**Goal:** pi-automode loads in host pi (through `yolo host apply`, IDE and direct launches
included) and in no jail.

1. **Posture lists (code; needs no new ruling). ✅ BUILT, `bbe5c878`.** Build [§4.1](#41-recommended-posture-lists-inside-autonomy):
   the `lists` field, its validation, the gate in `Collect`, the rewritten inertness contract,
   footprint and the notch line, with tests for decode, `Collect` at both bits, the jail boot,
   `RenderHostPack` with its insert record, and `config ls`/`render` at both notches. Then the
   docs, `just check-ci` and the nested-jail check. It is what [PV-OQ-1](../reference/providers.md#pv-oq-1)
   and [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) already prescribe;
   [OQ-5](#OQ-5) only asks whether to depart from them. The review's estimate: 60–80 lines of
   production Go, 200–250 of tests, half a day to a day. **As built:** every listed test, plus
   the real `yolo host apply --assert`, the malformed and ownerless cases at both bits, and
   declaration order; `just check-ci` green at each commit. The nested-jail check was not run
   (the build ran under a no-nested-jail constraint). A launched jail first rendered a posture
   list on 2026-10-01, in `TestAPostureListRendersOnlyItsOwnSideOfTheLine` (the status line has
   what it read).
2. **The host, by hand (no ruling; about 15 minutes).**
   1. `just install`, so the host yolo and the flake bundle both know the field.
   2. Add the [§4.1](#41-recommended-posture-lists-inside-autonomy) contribution to the `matt`
      pack.
   3. Confirm `host_management` is `own` (unset is `none` since the `assert` retirement, and
      `yolo host apply` refuses under it).
   4. `yolo host apply`, then `yolo host apply --assert`.
   5. Start pi on the host once, then run `/automode model`.
   6. Relaunch a jail and confirm the entry is absent.

   On a podman or Apple Container host that finishes the job, because the mark keeps the host
   file out of jails from the first assert on.
3. **`macos-user` parity (code; no ruling; the review's estimate is 1–2 hours). ✅ BUILT,
   `499a332f`; unit-tested only.** [§4.3](#43-render-mark-parity-on-macos-user). Needed only on
   that backend, and without it a `macos-user` jail gets automode. A Mac with a host yolo from
   this commit or later is what makes it true there.
4. **The end-to-end test (code; no ruling; 2–3 hours). ✅ BUILT, `a6021d86`.** Gap 2, as an
   in-process chain of the production steps (NS-D10):
   `TestAManagedHomesHostFileIsABaselineFromTheAssertToTheBoot` (now
   `TestAnOwnedHomesHostFileIsABaselineFromTheApplyToTheBoot` and
   `TestAHomeAssertedIntoBeforeTheRetirementStaysABaselineToTheBoot`) and its unmanaged twin, for the
   container launcher and the `macos-user` one.

**What gates what.** Nothing gates steps 1–4 unless [OQ-5](#OQ-5) amends the rulings for the
modifier, in which case step 1 builds [§4.2](#42-the-first-drafts-modifier-corrected) instead:
about the same size, with worse skew behavior. [OQ-3](#OQ-3) gated only a host-only scalar,
which automode does not need; it is built (`12032eb2`) as the extension point the ruling asked
for. [`OQ-PR1`](pack-pi-resources.md#OQ-PR1) and
pi-git-extension-caching [OQ-6](pi-git-extension-caching.md#OQ-6) gate nothing here.

**An interim with no code, container backends only.** After one `yolo host apply --assert` has
left the mark, a `pi install` by hand on the host stays out of container jails, because the whole
host file is a baseline there, and the rmw arm keeps it as the user's entry. It is not declarative,
it leaks on `macos-user`, and it depends on the mark outliving the `assert` retirement.

---

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A. `notches` modifier on `config-list` and `config-overlay`** (the first draft's proposal) | **Not recommended.** It conflicts with [PV-OQ-1](../reference/providers.md#pv-oq-1) and env-manager [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) as written, adds the notch-name edge [§6c](../reference/pack-system.md#batch-6c) rules out, fails open under skew, and changes `Collect`'s signature at every call site. Buildable if [OQ-5](#OQ-5) amends both rulings, and then as B |
| **B. `posture: "guarded"` modifier on the same kinds** | **The fallback if [OQ-5](#OQ-5) amends the rulings** ([§4.2](#42-the-first-drafts-modifier-corrected)). The draft rejected it as the sole selector for host packages that need the host's environment rather than its permission policy (1Password, host notifications). No such case is shipped or reported; when one arrives, the predicate is a capability such as "a real home on the real filesystem", not a notch name |
| **C. Posture `lists` inside `autonomy`** | **Recommended** ([§4.1](#41-recommended-posture-lists-inside-autonomy)), pending [OQ-5](#OQ-5). The draft's rejection rested on two facts that do not hold: `autonomy` is one declaration per pack that any pack may make, not reserved to agent-owning packs; and [OQ-PT8](../reference/providers.md#oq-pt8) moved `profile`'s config patch into gated contribution kinds, while [PV-OQ-1](../reference/providers.md#pv-oq-1) keeps `autonomy` a separate bundling kind on purpose |
| **D. Mount the host's `list-record.json` and subtract** | **Moot.** The zero-mount signal it wanted already crosses as the launcher's `rendered` label ([`OQ-CR6`](../reference/config-target-resolution.md#oq-cr6)) |
| **E. A Lua transform** | **Rejected.** Transforms were removed on 2026-09-11 (`2c5c84a1`, `e958ed96`); `luahook` is only the derive sandbox now, and [OQ-LT2](../reference/pack-system.md#oq-lt2) forbids a post-merge script slot |
| **F. Sanitize the host bytes in the jail** (the first draft's second half) | **Rejected; moot on containers.** It filters bytes `hostSurfaceBytes` already discards under the label. In an unlabelled home it could only strip the user's own entry, against [`OQ-CR8`](../reference/config-target-resolution.md#oq-cr8). It is a second model of "which host bytes are yolo's" beside the mark, it knows only the selected packs' entries (so it misses a dropped pack's residue, which the mark covers), and it is a per-entry veto [the kind's limits](../reference/pack-system.md#config-list-limits) say does not exist |
| **G. A guarded launch flag (`pi -e npm:…`)** | **Rejected.** `InjectLaunchFlags` asked for the autonomous posture only, and a flag misses IDE and direct launches. The first half is gone since 2026-09-28 ([notch-convergence item 20](../plans/notch-convergence.md#tier-6--render)); the second still rejects it |
| **H. `host_management: own` plus `pi install` by hand** | **Not recommended.** Zero code, but the host capture stores `packages` as one whole array, which then shadows the contributing pack's own overlay from that day on |

---

## 7. Risks and mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| **Skew widens a conditional entry** | With the modifier, an older entrypoint renders the list in every jail | The posture shape fails closed; either way, `just install` before the pack change (the host refuses the reverse order) |
| **`macos-user` leaks until parity** | automode reaches those jails after an assert | [§4.3](#43-render-mark-parity-on-macos-user), step 3 of [§5](#5-fastest-path-to-the-motivating-case) — built in `499a332f`; a host yolo older than it still leaks |
| **The `assert` retirement moves the default to `none`** | An unset home stops receiving the entry | Done conditions name `own`; the mark is durable |
| **The inertness test goes red** | A reviewer reads the pinned contract as broken | Rewrite it to pin identities and ownership only, and add a test that fails when the gate is deleted — done in `bbe5c878` (`TestCollectAutonomySelectsPostureLists`) |
| **A user's own identical entry** | Removed by yolo? | Never: `ReconcileInsertedList` leaves an unrecorded entry alone (the sanitizer, F, would have stripped it) |
| **A custom confinement with prompts on** ([env-manager §4.2](yolo-as-environment-manager.md#42-agent-autonomy-is-a-confinement-policy-not-baked-pack-config)) | Should get the gate | The posture shape gives it one; a notch-name match would not |

---

## 8. What this does not propose

- **A host-only scalar only through a posture's `config`**, by [OQ-3](#OQ-3)'s ruling: no
  separate mechanism for it, no modifier on `config-overlay` and no notch name. Built in
  `12032eb2` as the posture overlay (NS-D19 to NS-D26): a posture's `config` may now patch a
  surface another pack owns, on the `config-overlay` path.
- **No per-entry removal or veto** on a host-supplied array ([OQ-LT2](../reference/pack-system.md#oq-lt2)).
- **No capability predicate** (host Docker, a keychain, notifications) until a real case exists.
- **No change to which packs a notch selects**: `packs` stays one list.
- **No escape hatch**, and no interaction with the credential gate.

---

## 9. Open questions

- ✅ <a id="OQ-3"></a>**OQ-3: Should posture-conditional content also reach `config-overlay`?**
   The draft asked for `notches` on both kinds. In the recommended shape the equivalent question
   is whether a posture's `config` may patch a surface another pack owns. That would activate
   patches that today only produce a "folded nowhere" `FoldNote`, and it decides whether a
   host-only scalar ever exists. The motivating case needs none: pi-automode keeps its own
   settings in `~/.pi/agent/extensions/pi-automode/config.json` (its README, as reported).

   <!-- vantage: question id=OQ-3 -->

   _Leaning:_ Defer until a real case arrives. Nothing in the motivating case needs it, and
   widening the posture's `config` reach is a ruling of its own.

   **Answer:**
   > **Ruled in review 2026-09-28, against the leaning:** *"do it now. extension point."* Build
   > it now as an extension point, not on a motivating case: a posture's `config` may patch a
   > surface another pack owns, the way posture lists already reach another pack's arrays, so
   > the patches that today only produce a "folded nowhere" note take effect at the posture that
   > selects them. That is also the way a host-only scalar exists.

   **Built** in `12032eb2`, as the *posture overlay*: the entry is placed by
   `packoverlay.Collect`'s `config-overlay` pass at the posture that selects it, with that
   kind's owner check, fold slot, order, refusals and provenance. The mechanism choices are
   NS-D19 to NS-D26 in the [ledger](#10-decision-ledger).

- ✅ <a id="OQ-5"></a>**OQ-5: Do the rulings that put confinement-conditional content in `autonomy` hold for a non-owning pack's host-only entry?**
   [PV-OQ-1](../reference/providers.md#pv-oq-1) (settled 2026-09-01) and env-manager
   [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) (2026-08-01) put confinement-conditional
   content in `autonomy`. Since then the case has changed in two ways: the contributor does not
   own the surface, and the content is an array append rather than a managed key. If the rulings
   hold, step 1 builds [§4.1](#41-recommended-posture-lists-inside-autonomy). If they are amended,
   it builds [§4.2](#42-the-first-drafts-modifier-corrected), a `posture` modifier on
   `config-list` and `config-overlay`.

   <!-- vantage: question id=OQ-5 -->

   _Leaning:_ They hold; build posture lists. A permission gate is exactly the content
   [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) put
   in `autonomy`. The shape needs no `Collect` signature change and no notch names in manifests,
   it keeps a permission gate in where the notch is unknown, and an older in-jail reader drops a
   nested posture field, where it would render a modifier-gated list unconditionally.

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** the rulings hold, and posture lists inside
   > `autonomy` are the shape, as built in `bbe5c878`. A permission gate is the content
   > [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) moved into `autonomy`; the shape
   > needs no `Collect` signature change and no notch names in manifests; and an older in-jail
   > reader drops a nested posture field, where it would render a modifier-gated list
   > unconditionally.

---

## 10. Decision Ledger

The `NS-D` rows are implementation decisions recorded rather than asked, each with one workable
answer; `NS` is this file's name, notch-scoped.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | *Implementation decision*, under [§6c](../reference/pack-system.md#batch-6c) and [PV-OQ-1](../reference/providers.md#pv-oq-1). **The selector is the posture, not a notch name**, in one spelling (no `notch`/`notches` pair). `Collect` already receives the bit at every caller; a name would add a notch-name edge and a `packdecl`→`render` import cycle; at `KindUnset` the posture keeps a permission gate in where a `"host"` match drops it; and a custom confinement with prompts on gets the gate. Today posture and notch give the same answer at every shipped notch | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| OQ-2 | *Answered — moot.* Filtering host-only entries out of a `readsHost` layer: the render mark already delivers a managed home's host file as a baseline, not a layer, on podman and Apple Container. The residual `macos-user` leak is NS-D3. The sanitizer and the host-record mount are rejected ([§6](#6-alternatives-considered), D and F) | 2026-09-27 | [§3.2](#32-the-host-layer-is-already-a-baseline-on-container-backends) | ✅ `369c6f63` (containers); `macos-user` by NS-D3 |
| OQ-4 | *Answered — moot; the rest is NS-D1 and NS-D2.* `yolo config ls` and `yolo config render` already pass `render.ProfileFor(t.notch).AgentAutonomy`, so `--at host` and `--at jail` follow the gate with no change | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878`, pinned by `TestConfigRenderAndLsFollowAPostureListsNotch` with no change to either verb |
| OQ-3 | **Maintainer ruling**, against the leaning: *"do it now. extension point."* A posture's `config` may patch a surface another pack owns, so a posture can carry a host-only scalar | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` (NS-D19 to NS-D26) |
| OQ-5 | **Maintainer ruling**, as leaned: [PV-OQ-1](../reference/providers.md#pv-oq-1) and env-manager [OQ-11](yolo-as-environment-manager.md#9-decision-ledger) hold for a non-owning pack's host-only entry; posture lists inside `autonomy` | 2026-09-28 | [§9](#9-open-questions) | ✅ `bbe5c878` |
| NS-D1 | *Implementation decision.* The gate sits after the list decode and before the owner check; an unselected contribution is a clean skip (no problem, no orphan, no applied row), as the `profile` gate is | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| NS-D2 | *Implementation decision.* `yolo pack footprint` claims a gated list unconditionally and names the posture in the detail; `surveyNotchFacts.AutonomyFolds` counts posture lists | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878`; the claim is the `autonomy` one (NS-D4); the fold count narrowed to placed lists by NS-D12 (`2ab44527`) |
| NS-D3 | *Implementation decision*, under [`OQ-CR6`](../reference/config-target-resolution.md#oq-cr6). `macos-user` computes `Rendered` with `hostLayerIsRender` in `buildMacosCtxTree` and emits `entrypoint.HostLayerWire` from `macosuser.hostLayerWire` | 2026-09-27 | [§4.3](#43-render-mark-parity-on-macos-user) | ✅ `499a332f`, unit-tested only |
| NS-D4 | *Implementation decision.* A posture list is disclosed on its pack's `autonomy` footprint claim, not as a `config-list` claim: `<posture> appends <entries> to <agent/name>#<pointer>`, the entries spelled as the config-list claim spells them (`configListShown`, shared). A claim leads with the kind the author wrote, as an orphan and a problem do (NS-D7); a `config-list` claim would name a declaration the pack does not make | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| NS-D5 | *Implementation decision.* One projection, `packdecl.(*Manifest).ListContributions`, walks `config-list` contributions and posture lists in declaration order — a posture's lists at the `autonomy` contribution's position, autonomous before guarded — each tagged with its `Posture`; `ConfigListContributions` stays the kind's own projection. Only the first `autonomy` contribution counts, the one `PostureFor` reads, so a posture's config, flags and lists come from one declaration | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878`; a second contribution refused or dropped at decode by NS-D11 (`1300fb12`) |
| NS-D6 | *Implementation decision.* Validation shares `configListBodyProblems` with the `config-list` kind; a posture list's messages name "a posture list (a config-list body)" and its position (`contributes[i].guarded.lists[j]`). The surface identity is the collector's to parse, as it is for a `config-list` | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| NS-D7 | *Implementation decision.* A malformed posture list's problem leads with `autonomy <posture>.lists`, which `collectProblemKind` reads as `autonomy`; an ownerless selected one is an orphan whose kind is `autonomy`, and its core-owned sentence says "a posture list" | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| NS-D8 | *Implementation decision.* Once placed, a posture list is an `agentcfg.ListContribution` from its pack, indistinguishable from a `config-list`: the same fold, per-entry capture, `rmw` insert record and `config-list:<pack>` source label in `config render --explain` and `config ls`. The kind is named where an author reads the declaration (NS-D4, NS-D7), not in the fold, which keys on the pack | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `bbe5c878` |
| NS-D9 | *Implementation decision.* `TestMacosUserDeliversHostBytesByCopy` hides the machine's host-render mark (`privateHostProvenance`), as `TestAppleContainerReadsHostGrantArrives` already does: with the label on `macos-user`, the self-hosted Mac's own mark would drop the test's hand-written settings file for a reason that is not the one it measures | 2026-09-27 | [§4.3](#43-render-mark-parity-on-macos-user) | ✅ `499a332f` |
| NS-D10 | *Implementation decision.* Gap 2's test is an in-process chain in `internal/cli/run` — `RenderHostPack` at `assert` over a host-posture `Collect`, each backend's launcher, then `ConfigurePackSurfaces` with that launcher's wire — rather than an integration test. The integration harness links the machine's yolo state dir into every isolated home (`packHomeSharedStores`), so a real `yolo host apply --assert` there writes host-render state into the machine's own store. The container runtime's bind is the one step the test copies by hand | 2026-09-27 | [§5](#5-fastest-path-to-the-motivating-case) | ✅ `a6021d86` |
| NS-D11 | *Implementation decision.* A second `autonomy` contribution in one pack is refused by the strict decoder (`validateSingleAutonomy`, beside `validateFilesDestinations`) and dropped with a skew note by the tolerant one (`secondAutonomySkip`), so every reader — `PostureFor`, `ListContributions`, the footprint — sees one declaration. Before, it decoded clean, the posture readers took the first, and the footprint claimed both; `packload.Collisions` cannot see it, since the claim's target is the pack's own name. A skip rather than a problem in the jail, because the boot treats a problem as fatal. `ListContributions` keeps its first-only guard for a hand-built manifest | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `1300fb12`. That commit's message lists `host apply` among the strict readers that refuse the second, which was false until NS-D14 (`d4aa6a43`) |
| NS-D12 | *Implementation decision.* The notch line counts a posture list as a fold only when `Collect` placed it: `OverlaySet.PlacesPostureListFrom(pack)`, read by `surveyNotchFacts` from the apply's own `OverlaySet`. Counting declarations printed "folded into the config surfaces below" under an orphan line. The config-patch half still counts a declared patch | 2026-09-27 | [§4.1](#41-recommended-posture-lists-inside-autonomy) | ✅ `2ab44527` |
| NS-D13 | *Implementation decision.* `ConfigurePackByName` is the embedded lookup plus `configureOnePack`, its body over a pack in hand, so a fixture pack reaches the probe's `Collect` call. No embedded pack declares a posture list, so the bit there was invertible with every test green while `Collect`'s comment said otherwise. `Collect`'s comment now names the test pinning each caller | 2026-09-27 | [§4.6](#46-what-done-looks-like) | ✅ `77dc6afa` |
| NS-D14 | *Implementation decision*, under the no-half-states ruling `yolo host apply --assert` already refused an unresolvable pack by ([`pack-system.md`](../reference/pack-system.md#fetch-refresh-lock)). `cli.resolveConfiguredPack` treats a pack whose manifest has problems (`packload.LoadDir`'s second result) as unresolvable (`manifestProblemsError`) instead of discarding them, so no host verb reads a manifest every launch refuses. Each caller keeps its disposition for an unresolvable pack, decided per verb: `host apply --assert` refuses the whole set and writes nothing, its dry run and `--format json` say it would (`manifest_problems` per pack), and the launch gate renders nothing and launches; `yolo host --` and `host env` compose without the pack and warn ([the dispositions](../reference/host-apply-staleness.md#the-dispositions)), which stops the launch only when the selected profile is one that pack alone declares (NS-D18); `--revert` leaves its keys recorded; `yolo capture` does not search it; `check-deps` names it and exits 1; the read-only `config` verbs (`render`, `ls`, `diff`) name it as not folded or not inspected, and exit as before; `config promote`, which writes a manifest, refuses to write into it (NS-D16). `footerHostPacks`, which resolves without that function, skips it too, to stay the host launch's set. The remedy group names `yolo pack lint`, not `yolo pack install`. As a consequence, a local pack naming `AGENTS.md` in a briefing `from` is refused by the resolver, with nothing written, before the local-pack move's own refusal is reached; that refusal used to fire after the config surfaces had been rendered. A host yolo older than this row applies such a pack at rc 0, since an unknown field used to leave `LoadDir` an empty manifest, which is why the [§4.5](#45-failure-paths) row for a host yolo older than the posture-list field names no host refusal. **Revised 2026-09-28 by [notch-convergence item 6](../plans/notch-convergence.md#tier-2--one-selection-p1-p2)** ([NC-D5](../plans/notch-convergence.md#7-decision-ledger), [NC-D32](../plans/notch-convergence.md#NC-D32)): `yolo host --`, `yolo host env` and the launch gate now refuse the launch over an unresolvable pack or one whose manifest has problems, naming each with its remedy, instead of composing without it (or, at the gate, launching on the last apply). The per-verb dispositions of the read-only and `apply` verbs above are unchanged | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `d4aa6a43`; the undecodable-manifest case pinned in `7059a2ba` |
| NS-D15 | *Implementation decision*, correcting NS-D14. The problems and the declaration `resolveConfiguredPack` and `footerHostPacks` read are those of the tree the entry's `only`/`exclude` leave, the tree the launch, `yolo check` and config validation load: `cli.loadAsStaged` stages the source through `packstage.Stage` with the entry's filters into a throwaway directory, loads that, and rebinds the pack's `Root` to the source, since the host notch reads files in place and the copy is deleted. A fetched pack is always staged (its no-escape check, now reused rather than thrown away); a local one only when filtered. Before, `host apply --assert` refused, as "every launch refuses it too", a pack whose only problem is a file its entry excludes or whose entry filters out an undecodable `pack.json`, and a `pack.json` the filters drop was still read, its `config-overlay` applied to the real home. The host notch's FILE reads still ignore the filters (a skill or `files` source the entry drops is delivered from the source tree); that gap predates NS-D14 and is not closed here | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `68505100` |
| NS-D16 | *Implementation decision*, correcting NS-D14's record of `config promote` as a verb that writes nothing. Promote reads its fold through `configuredPacksForInspection` and writes the destination manifest, so a destination (`--to local` or `--to pack:<name>`) whose manifest has problems is refused at the write (`promotePlan.destManifestProblems`): exit 1, the problems named, `yolo pack lint <dir>` as the remedy, nothing written. It used to write, clear the capture overlay, exit 0 and say the next launch renders the keys, which no launch does. The dry run says `--accept-promotion` is refused and `--plan` names the destination as unwritable; both exit 0, as a plan is information | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `d20f21dd` |
| NS-D17 | *Implementation decision.* The remedy group for a pack refused over its problems says "fix each problem named above in the pack", not "in the pack's manifest", because `packload.LoadDir` also reports a file such as `briefing/CLAUDE.md`, fixed by renaming it. The conventional local pack, which no `packs` list names (`PackEntry.Implicit`, now carried on `unresolvedPack`), gets its own group keyed on its directory, naming that directory and saying it has no `packs` entry to remove; it used to be told to remove it from `packs` | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `55600c5c` |
| NS-D18 | *Implementation decision*, correcting NS-D14's "a pack-set fault never stops a host launch". A profile only a refused pack declares is undeclared at `yolo host --` and `host env`, so [OQ-CS6](../reference/providers.md#oq-cs6) refuses it, as a jail launch refuses the same config over the pack. The refusal now names that pack as the declarer, with its problems and `yolo pack lint` (`undeclaredHostProfileError`), from the profile names the part of its manifest that decoded declares, read for the message only (`manifestProblemsError.profiles`). A pack that cannot say what it declares (a manifest that did not decode, a git pack not in the store) is named beside the plain undeclared message. A provider only such a pack declares still launches without it, with the existing warning. **Revised 2026-09-28 by [notch-convergence item 6](../plans/notch-convergence.md#tier-2--one-selection-p1-p2)** ([NC-D5](../plans/notch-convergence.md#7-decision-ledger), [NC-D32](../plans/notch-convergence.md#NC-D32)): a pack with problems now refuses `yolo host --` and `yolo host env` before the selected profile's declaration or its provider is checked, the refusal naming the pack and its problems, so neither the profile refusal this row describes nor the provider fallback is reached. `undeclaredHostProfileError` and `manifestProblemsError.profiles` are deleted | 2026-09-27 | [§4.4](#44-behavior-at-each-target-and-the-degenerate-cases) | ✅ `203b3009` |
| <a id="NS-D19"></a>NS-D19 | *Implementation decision*, building [OQ-3](#OQ-3). **What a posture overlay is, and the one path it takes.** An `autonomy` posture `config` entry whose identity the declaring pack does not declare is a *posture overlay* (the term coined in [Terms](#terms-in-plain-words)); one naming the pack's own surface still folds into its `managed` layer. The schema does not change: an author writes a patch on another pack's surface exactly as one on their own. One projection, `packdecl.(*Manifest).OverlayContributions`, walks `config-overlay` contributions and every posture `config` entry in declaration order, a posture's entries at the `autonomy` contribution's position, autonomous before guarded, first `autonomy` only (NS-D5's rule); `ConfigOverlayContributions` stays the kind's own projection. `packoverlay.Collect`'s overlay pass is the one planner: it skips the pack's own entries, decodes the rest, gates on the posture after the decode and before the owner check (NS-D1's position), and places each as an ordinary `agentcfg.Overlay`, so the fold slot below the owner's `managed`, "later wins", `config-overlay:<pack>` provenance, the boot's "config-overlay keys from" line, `config diff`, `config ls`, `config render`, `config promote`, the outranked report and the drop prune all read it with no change. No second planner, and no change at any `Collect` caller | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D20"></a>NS-D20 | *Implementation decision.* **Its body follows `config-overlay`'s rules.** `manifest.DecodePostureOverlay` decodes the entry as a strict surface DTO, then applies `overlayRefusals`, the table `DecodeOverlay` uses, except the four fields the posture schema itself requires (`agent`, `name`, `path`, `codec`): `defaults`, `mode`, `retireOnFirstRender` and `readsHost` are refused by name, and an empty `managed` contributes nothing and is refused. Problems lead with `autonomy <posture>.config <agent/name>`, which `collectProblemKind` reads as `autonomy`, and are reported at every notch, whichever posture is selected (a posture list's rule). A malformed entry is reported once: by the posture fold when it also decodes it (the selected posture of a pack declaring a surface of its own), by `Collect` otherwise. Before, a pack declaring no surface of its own had its posture `config` read by nothing | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D21"></a>NS-D21 | *Implementation decision.* **Its `path` and `codec` must be the owner's.** The schema requires both of every posture patch, and one on another pack's surface naming a different file or codec would read, to its author, like keys written somewhere they are not. Checked after the owner check, since only the owner knows the answer, so it is reported at a notch that would place the entry; the owner's declaration is the one `Collect`'s owner pass already holds | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D22"></a>NS-D22 | *Implementation decision.* **The fold note is retired, and the orphan replaces it.** Every patch the posture fold missed named another pack's surface by definition, so all of them are now `Collect`'s: placed, refused, skipped at the other posture, or an ownerless `autonomy` orphan (`OrphanOverlay.PostureConfig` makes the core-surface sentence say "a posture's config patch"). `(*Pack).SurfacesForReport` returns no notes, and keeps its signature, because the release probe compiles against it (`TestReleaseDecodeProbeAPIIsStable`); `FoldNote` stays as a type for the same reason. The boot's "folded nowhere" warning and `RenderHostPack`'s `ignored:` row are deleted, and `planPackSurfaces` returns no notes. The [`OQ-Z5`](../reference/zai-plumbing.md#oq-z5) typo still reaches its author, as an orphan that says to check the identity | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D23"></a>NS-D23 | *Implementation decision.* **Disclosure is on the `autonomy` claim**, NS-D4's precedent: `yolo pack footprint` appends `<posture> contributes keys to <agent/name> (owner still wins)` for each posture's overlays, with `config-overlay`'s precedence clause. A patch on the pack's own surface is not named there, since the pack's `config` claim covers its own file | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D24"></a>NS-D24 | *Implementation decision*, extending NS-D12. **The notch line counts a posture overlay only when placed** (`OverlaySet.PlacesPostureConfigFrom`), and a patch on the pack's own surface by declaration (`packload.(*Pack).PosturePatchesOwnSurface`), since that one always lands. The config-patch half used to count any declared patch, which, once a patch could be an orphan, printed "folded into the config surfaces below" under the orphan's "no effect" line | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D25"></a>NS-D25 | *Implementation decision.* **A host-only scalar leaves the host the way every overlay key does.** `yolo host apply --assert` writes it and records it `config-overlay:<pack>` in the provenance record. When the posture stops selecting it, `host_management: own` regenerates the file without it on the next apply; under `assert`, whose rmw arm cannot express removal, the next apply records it `retired:config-overlay:<pack>` (`retireUnclaimed`) and `yolo host apply --revert` removes it; dropping the pack removes it through the same prompt as its other overlay keys (`PruneHostOverlayKeys`). No new withdrawal was added for `assert`: an owner's managed key and a `config-overlay` key behave the same way there today, and a posture-only withdrawal would be a second rule for one record. A posture list differs because its insert record withdraws entries (`ReconcileInsertedList`) | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
| <a id="NS-D26"></a>NS-D26 | *Implementation decision.* **Every vehicle takes it with no vehicle-specific code.** The jail boot on every backend, `macos-user` included, runs `ConfigurePackSurfaces`, and `yolo host apply` and `yolo config render --at host` run `RenderHostPack`, all over `Collect`, so the render mark keeps a host-applied scalar out of a later jail on both launchers exactly as it does a posture list's entry (`TestAHostOnlyScalarReachesTheHostAndNoJailLaunchedAfterIt`, the in-process chain of NS-D10). `yolo check`'s probe collects over the one pack it renders, so a posture overlay, on another pack's surface by definition, is never placed there, as a cross-pack `config-overlay` is not | 2026-09-28 | [§9](#9-open-questions) | ✅ `12032eb2` |
