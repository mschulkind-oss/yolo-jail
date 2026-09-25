---
title: "Who owns the config file — declared host management, and the way out of capture"
date: 2026-09-09
status: in-review
tags: [design, config, host, capture, packs, ownership]
summary: "yolo decides who owns an agent's config file by inferring it from the confinement notch, and the inference is wrong for anyone who adopted `yolo host apply`. Declare ownership in the user config instead, make the host render like a jail when it is owned, and build the promotion path that turns a captured in-jail edit into a declared one — the verb a shipped message already advises and nothing implements."
vantage:
  status-chip: true
---

# Who owns the config file — declared host management, and the way out of capture

**Status:** DESIGN, 2026-09-25 — the bulk was BUILT 2026-09-12 and amended twice on 2026-09-20, and the first amendment's follow-on [`CO13`](#13-decision-ledger) is BUILT 2026-09-25; but [`OQ-CO14`](#oq-co14) now owes a ruling, so the word names that rather than the code.

**Needs your ruling:** [`OQ-CO14`](#oq-co14) (what retiring `assert` does to a config, and a home, already on it). Two smaller ones, both opened 2026-09-25 by the `CO13` build and neither blocking anything: [`OQ-CO15`](#oq-co15) (what the jail's `rmw` arm does with a table not declared in full) and [`OQ-CO16`](#oq-co16) (whether the provider catalogs stay declared in full).

**The first amendment narrowed the adoption drop.** It no longer takes a computed table that
asserts nothing, *wholesale against `computed`* is withdrawn as the general rule, and the half
of the signal needed to narrow the rest did not exist — decided as
[`CO13`](#13-decision-ledger), an **implementation shape rather than an escalation**, because
every candidate produced identical user-visible behaviour and differed only in what the code
carries ([the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule)
carries the ruling and the measurements). **Built 2026-09-25:** a derive declares a table it
regenerates in full by wrapping it in `ctx.in_full`, and a table not declared in full claims
only the leaves it names — at the jail's adoption and at the host's table probe alike
([what shipped](#built-2026-09-25--what-shipped)). Everything below it stands.

**The second reverses [`OQ-CO1`](#13-decision-ledger)**: `assert` is retired, `none` and `own`
are the two values left, and `none` becomes the default
([§4.5](#45-retiring-assert--the-two-value-key)). ⚠ It is **recorded and unbuilt**, so every
present-tense claim below still describes a three-value key, deliberately. The reversal takes
the ground out from under a second ledger row and leaves one declaration unspellable — that is
[`OQ-CO14`](#oq-co14), and it is why the status moved rather than the ledger row simply gaining
a date.

MEASURED: verification after it landed measured four gaps, and a
second pass over [`OQ-CO12`](#13-decision-ledger) found six more things.
[§10](#10-what-i-would-build-in-order)'s eight steps all
landed: the `host_management` key, `own`'s composing render and its capture store, host-side
`reset`, `yolo config promote`, `yolo host apply --revert`, and — last, on 2026-09-12 —
step 8's **one-time adoption archive** ([§6.3.3](#633-what-survives-as-a-guard),
[`OQ-CO7`](#13-decision-ledger)), written at BOTH notches by one call in the shared stateful
writer ([`entrypoint.archiveAdoption`](../../internal/entrypoint/adoptionarchive.go)).
Verification after it landed measured **four gaps**; **three are closed** (the gate could not
tell yolo's own output from the user's file, nor an absent file from an unreadable one, and the
verdict closed an adopting run as a run with nothing to do) and **one remains**, recorded as live
residue in [§6.3.3](#633-what-survives-as-a-guard) rather than closed over — not a regression, but
a loss a reader of this doc would expect the copy to net.
[§13](#13-decision-ledger)'s **Built** column carries the same answer ruling by ruling, so
"settled" and "shipped" can be read apart rather than inferred from each other.
[`OQ-CO12`](#13-decision-ledger) — the one the build itself opened — was ruled and built the same
day, and **verified on a second pass that found six more things**, all recorded in
[§11](#11-success-criteria) and none of them fixed here.
Claims about *current behavior*
in [§2](#2-what-exists-today-stated-precisely) were verified or measured between
2026-09-09 and 2026-09-11 and describe the tree the build acted on; the figures
re-measured after it say so at the point they are stated.

> **In short.** yolo infers who owns `~/.claude/settings.json` from the
> confinement notch, and adopting `yolo host apply` is precisely the act of
> leaving the world that inference is sound in — so ownership should be
> **declared** in one user-scope key, and a captured edit should have a verb
> that **promotes** it into a pack.

**Why it matters.** A key typed inside a jail today has exactly one exit:
`yolo config reset`, which discards it. There is no way to carry it to another
jail, to the host, or into anything declared.

**The shape.** One user-scope key (`host_management`) selects the host notch's surface mode;
`own` makes the host render like a jail; `yolo config promote` lifts captured keys into the
conventional local pack, which renders at every notch. The key shipped with **three** values and
is ruled down to **two** — `none` (the new default) and `own`
([§4.5](#45-retiring-assert--the-two-value-key)); `assert` is what ships today and what this
document still describes in the present tense below.

**Cost.** It reverses **four** rulings recorded in
[`environment-manager-plan.md`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
on 2026-08-01 — three settled when this was written and the fourth ([`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase))
settled by [`OQ-CO11`](#13-decision-ledger) on 2026-09-11. See
[§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications).

**Start at [§4](#4-declaring-ownership--the-host_management-key)** — the key is what makes every other question answerable.

**One thing here needs a ruling, and a ruling opened it.** Every question this design
or its build opened was settled ([§13](#13-decision-ledger)), the last of those
([`OQ-CO12`](#13-decision-ledger)) on 2026-09-12. Then two follow-ons were filed on 2026-09-20.
[`CO13`](#13-decision-ledger) came from a ruling that narrowed the adoption drop and could only
narrow it halfway
([the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule)).
It was **decided the same day** as an implementation shape, and **built 2026-09-25**.
[`OQ-CO14`](#oq-co14) came the same day from the ruling that retired `assert`
([§4.5](#45-retiring-assert--the-two-value-key)), and it is **open**. **A settled design reopens
exactly this way**, and twice in one day is a fact about the sprint rather than about the design.
⚠ **Settled is not the same as clean, and the difference is recorded rather than rounded off.**
Two sections carry **live residue** — measured behavior that no ruling covers and no commit closed:
[§6.3.3](#633-what-survives-as-a-guard)'s one unnetted adoption loss, and
[§11](#11-success-criteria)'s list, found by verifying the ruling above rather than by trusting
it — of which the two smallest, a latent `reinstateAt` panic and an invalid TOML float, were fixed
on 2026-09-25 and are marked so in place. The rest sit in the body as present-tense behavior,
because that is what they are.
**The graduation rewrite is no longer the only thing left, and it should now WAIT.** Every
ruling is folded into [§13](#13-decision-ledger) and the sections it governs; what still
reads as sequencing is [§10](#10-what-i-would-build-in-order)'s build order, which is now history,
and [§6.3](#63-the-one-asymmetry-that-survives-deletion)'s argument, which a reference would have
to re-state as a contract. ⚠ But [§4.5](#45-retiring-assert--the-two-value-key) retires a value
the reference would have to state as current, so graduating this file before
[`OQ-CO14`](#oq-co14) is ruled
would mint a `CURRENT` doc with a known expiry. That rewrite is scoped in
[what the graduation owes](#what-the-graduation-owes--the-scope-of-the-rewrite), below.
[`OQ-DT1`](../plans/README.md#oq-dt1)
was ruled on 2026-09-13: graduate one doc at a time. So what holds this file back is
[`OQ-CO14`](#oq-co14) alone.
⚠ **One thing to carry out of this doc rather than into it:** four rulings in
[`environment-manager-plan.md`](../plans/environment-manager-plan.md)'s 2026-08-01 ledger are
reversed here; that ledger carries the dated reversal rows as of 2026-09-12.

**Reads with:** [`host-render-target.md`](host-render-target.md) (whose
[§6.3](host-render-target.md#63-the-structural-problem-on-a-host-target-the-host-layer-is-the-output)
ruling this re-scopes, and whose `Target`/notch vocabulary this builds on),
[`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md)
(the layer stack and the capture overlay, as designed),
[`environment-manager-user-stories.md`](environment-manager-user-stories.md)
(story 1 is this problem from the user's side),
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md)
(Phase 5.3 is the unbuilt half, and its ledger is the one this reverses).

---

## 1. The verdict, and the principles it rests on

**Ownership of a config file is a decision the user makes once, not a property
yolo derives from where the file happens to live.** Everything below follows from
that sentence.

Five load-bearing principles, numbered so later sections and sibling docs can
cite them:

- **P1 — Ownership is declared, never inferred.** A file yolo writes is owned by
  yolo, by the user, or shared, and which one it is comes from the user's config.
  Today it comes from `render.Kind` ([`target.go`](../../internal/render/target.go)),
  which is a confinement fact wearing an ownership hat.
- **P2 — The notch selects confinement; it must not silently also select the
  ownership model.** These are independent axes. Collapsing them is what makes
  "the host is different" feel arbitrary: the host is differently *confined*,
  which says nothing about who owns its files.
- **P3 — Capture is a staging area, not a terminal layer.** This adopts the
  leaning already recorded as `Q1` in
  [`environment-manager-user-stories.md`](environment-manager-user-stories.md#open-questions).
  A value that wins the merge while nothing declares it is a hole in the
  definition; the fix is to make declaring it easy, not to stop capturing.
- **P4 — Promotion is the only non-destructive exit from capture.** Today the
  only shipped exit is `yolo config reset`, which discards. One shipped message
  advises "promote" as English prose for a verb that does not exist
  ([`apply.go`](../../internal/cli/apply.go)).
- **P5 — The host is a notch like any other, except where a real home forbids
  it.** Same modes, same verbs, same sidecars. The single legitimate asymmetry
  is *deletion* — see [§6.3](#63-the-one-asymmetry-that-survives-deletion). This
  is the parity constraint [`OQ-HT4`](../reference/macos-user-home-tiers.md#oq-ht4)
  states for backends, applied to notches: one mechanism everywhere, and only the
  *primitive enforcing the boundary* may differ, because that is the one thing a
  pack and a user never have to feature-detect.

> [!NOTE]
> **Three terms are coined in this document** *(coined here)*, and each is used
> in the narrow sense given: a **portable** key means the same thing on every
> machine; an **environment-bound** key is one whose value is an artifact of the
> workspace, host or jail it was set in; a **sensitive** key is one that may
> carry a credential. They classify *keys inside a captured overlay*, and they
> are not a security model, a taint system, or a claim about the file as a whole.
> [§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot) is
> their only definition.

---

## 2. What exists today, stated precisely

Every row here was checked between 2026-09-09 and 2026-09-11.

### 2.1 The layer stack

Ascending precedence, from [`compose.go`](../../internal/agentcfg/compose.go):

```text
defaults → host → workspace → config-overlay:<pack>… → overlay (capture) → computed → managed
```

(A Lua `transform` layer sat between `computed` and `managed` until 2026-09-11,
when it was removed — [`OQ-LT1`](../reference/pack-system.md#oq-lt1).
Every layer above is still in [`compose.go`](../../internal/agentcfg/compose.go).)

**Since 2026-09-24 one fold step sits between `config-overlay` and capture.** It is not a layer
of its own. A `config-list` contribution appends entries to an array another pack owns
(`applyListContributions`, [`listcontrib.go`](../../internal/agentcfg/listcontrib.go)). The
additions apply after every ordinary overlay, and only capture, `computed` and `managed` can
replace the final array ([`OQ-AL2`](../reference/pack-system.md#oq-al2)).

`managed` is a floor: a pack's own asserted keys win everything. `overlay` is the
capture-diff sidecar — in-jail edits, carried across regeneration. Note that
`config-overlay` sits **below** capture, which matters in
[§5.4](#54-promotion-moves-a-key-down-the-stack).

### 2.2 The four surface modes

From [`manifest.go`](../../internal/agentcfg/manifest/manifest.go):

| Mode | What it does | Capture? |
|---|---|---|
| `stateful` | composes the whole file from layers; captures in-jail edits (the default) | yes |
| `computed` | composes the whole file and overwrites every boot, discarding edits | no |
| `rmw` | read-modify-writes an agent-owned file: only declared keys are rewritten | no |
| `unrendered` | yolo does not write the file at all | no |

### 2.3 The notches, and what each keeps on disk

From [`target.go`](../../internal/render/target.go):

| Notch | Sidecars | Provenance record |
|---|---|---|
| `jail` / `preview` | `<workspace>/.yolo/prism/` | same dir |
| `host` | none | `<home>/.local/share/yolo-jail/host-provenance/` |
| `guest` | none | none |

**The host provenance record already exists and already answers "who owns this
key".** Measured 2026-09-09 by running a real `yolo host apply --assert` into a
scratch home containing one hand-written key:

```text
$ cat ~/.local/share/yolo-jail/host-provenance/claude-settings.provenance
enabledPlugins	computed
env	computed
permissions	managed
skipDangerousModePermissionPrompt	managed
verbose	host
```

`verbose` is the user's; the rest is yolo's. The information needed for a revert
and for whole-file composition on the host is on disk today.

> [!IMPORTANT]
> **The record is consumed, and one consumer deletes keys on its authority.** It
> is tempting to read "nothing uses it" off the fact that no revert exists; four
> readers already do. `PruneHostOverlayKeys` removes keys from the user's real
> file — *"THE PROVENANCE RECORD IS THE AUTHORITY, and it has to be"*
> ([`hostoverlayprune.go`](../../internal/entrypoint/hostoverlayprune.go));
> `hostProvenanceExists` decides `FirstApply`
> ([`hostrender.go`](../../internal/entrypoint/hostrender.go));
> `retireUnclaimed` carries attributions forward past a pack drop
> ([`prism.go`](../../internal/entrypoint/prism.go)); and
> `yolo config diff` annotates from it
> ([`configdiff.go`](../../internal/cli/configdiff.go)). What no reader
> does is the two things this design wants — a revert, and a jail-side filter.

### 2.4 The verbs that exist, and the ones that do not

`yolo config` dispatches `ls, render, diff, reset, capture, drift, dump`
([`config.go`](../../internal/cli/config.go)). There is no `promote`.
`yolo host apply` takes `--assert`, `--dry-run`, `--shell-init`; it is the
ergonomic spelling of `yolo apply --at host`, both ship, and only the first carries
`--shell-init` ([`hostapply.go`](../../internal/cli/hostapply.go)).

**There is no `--revert`, and its absence is a ruling rather than a gap.**
[`host-render-target.md`](host-render-target.md) shows one as an example and names
the missing memory as the reason, but env-manager plan
[`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
resolved *no `--revert` on the host target* on 2026-08-01, and
[`apply.go`](../../internal/cli/apply.go) records it — *"no --revert —
the resolved [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)–[`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
model"*. [§10](#10-what-i-would-build-in-order) step 3 reverses it deliberately.

**Host-side `capture` and `reset` refuse unless `--force`** (`refuseHostSideWrite`,
[`configdiff.go`](../../internal/cli/configdiff.go)). **Two reasons,
one guard**, and quoting only the second is easy to do: the docstring calls itself
*"the Phase-0 data-loss guard"* — reset truncates a real dotfile to its often-empty
pure render, capture copies real host config into the workspace sidecar tree, and
*"both are destructive on a file yolo does not own in that context"*
([`configdiff.go`](../../internal/cli/configdiff.go)); the user-facing text
says *"could clobber your own config"*. The **privacy** reason — a credential
copied into a workspace sidecar — is
[`host-render-target.md`](host-render-target.md)'s ledger row 9.3, which records
that the same refusal *also* closes the leak path.

### 2.5 The three existing host user-scope keys

`host_files`, `host_wrappers` and `host_apply_on_launch` share one **scope**
construction ([`hostapplyonlaunch.go`](../../internal/config/hostapplyonlaunch.go),
[`inherit.go`](../../internal/config/inherit.go)): read from the
**user** config rather than the merged config, so workspace scope is
*inexpressible* rather than merely refused, and refused for inheritance into a
nested jail. That construction is the security boundary for any key that licenses
writing the real `$HOME`, and [§4](#4-declaring-ownership--the-host_management-key)'s
new key joins it rather than inventing a fourth shape.

> [!WARNING]
> **The construction is a scope boundary, not a fail direction.** Reading "user
> scope" as "therefore fails closed" is the available mistake, and one shipped key
> does the opposite. The shared helper `UserScopeConfigOrEmpty`
> ([`hostwrappers.go`](../../internal/config/hostwrappers.go)) has five
> callers: `agent_updates` deliberately fails **open** through it because it is an
> opt-*out* — *"absent, empty or unreadable means TRUE"*
> ([`agentupdates.go`](../../internal/config/agentupdates.go));
> `host_wrappers`, `host_apply_on_launch` and `perf_logging` fail closed; and
> `host_files` does not use the helper at all — it loads user scope strictly and
> returns an *error* ([`hostfiles.go`](../../internal/config/hostfiles.go)).
> So the direction is a per-key choice each key must state, which is how
> [§4.2](#42-scope-defaults-and-failure-direction) states it for the new key.

---

## 3. The diagnosis — one asymmetry, three unrelated justifications

[`host-render-target.md`](host-render-target.md) [§6.3](host-render-target.md#63-the-structural-problem-on-a-host-target-the-host-layer-is-the-output)
rules that **on a host target every surface is `rmw`**, and grounds it like this:

> `rmw` only ever rewrites the keys yolo declares […] so an agent's own keys are
> preserved *for free*, with no whole-file compose and therefore no capture
> overlay to protect them. Capture exists only to make *whole-file* composition
> non-destructive, and a host target does no whole-file composition.

The mechanism claim is true. The conclusion is scoped narrower than it reads, and
the scope is exactly what the user leaves when they adopt host apply.

> [!WARNING]
> **This design reverses four rulings recorded in another ledger.** Three are below;
> the fourth was a live question when this was written and is now
> [`OQ-CO11`](#13-decision-ledger)'s ruling. **The collision is no longer unrecorded** — that
> ledger carries the dated reversal rows as of 2026-09-12. The env-manager plan's ledger
> ([`environment-manager-plan.md`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase),
> all dated 2026-08-01) holds:
>
> - **[`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — *"On the host notch, `rmw` or whole-file compose? → RESOLVED:
>   pure `rmw`"*, with whole-file `stateful`+capture **rejected** as *"capture buys
>   nothing — it is solving a problem `rmw` does not have"*. `own` is that rejected
>   option, and [OQ-CO3](#13-decision-ledger) — already settled here — reverses it.
> - **[`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — where a host capture overlay lives — *"MOOT. It only existed
>   if [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) chose capture"*.
>   [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)'s store is
>   its answer, so it is un-mooted.
> - **[`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — *"Is there a `--revert` verb on the host target? → RESOLVED:
>   NO."* [§10](#10-what-i-would-build-in-order) step 3 is that verb.
>
> The fourth was the subject of a live question when this section was written and is now
> settled: **[`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)**
> *retired* the read-in `host` layer — "RESOLVED: YES", never implemented — and
> named the migration this document's own verb provides.
> [`OQ-CO11`](#13-decision-ledger) ruled on 2026-09-11 that **the layer stays**, because
> deciding its binding, failure direction and coverage decides that it exists.
>
> The reversal is defensible on grounds [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
> never weighed: its argument was that `rmw` *protects the agent's keys*, which it
> does; [§3.1](#31-what-actually-differs-between-rmw-and-capture)'s is that `rmw`
> cannot be *regenerated*, which no amount of key-protection buys. But a reversal
> has to be seen, and **a ruling that lands here must be written back into that
> ledger in the same commit.**

### 3.1 What actually differs between `rmw` and capture

The two look interchangeable, and in **steady state on the happy path they produce
the same bytes** — which is why the quote above can describe capture as merely
making composition "non-destructive". Being precise matters, because
[§5](#5-promotion--the-way-out-of-capture) is unbuildable without the difference.

**Capture is not a fixed set of keys.** It is `mergeDiff(previous render, current
on-disk)` ([`engine.go`](../../internal/agentcfg/engine.go)) — the minimal RFC-7386
patch that turns the render yolo last produced into the file as it now stands,
accumulated into the overlay sidecar. ⚠ **One exception, since 2026-09-24.** At an array
path that a `config-list` contribution targets, capture records per-entry additions and
removals in a separate `<agent>-<name>.list-capture.json` sidecar, never the whole array
([`OQ-AL1`](../reference/pack-system.md#oq-al1),
[the reference](../reference/config-migration-to-prism.md#list-paths-capture-per-entry)).
Everything below is about the overlay, and the list sidecar is outside it. Open-ended by construction: a key the jail
adds is recorded as its new subtree, a leaf that changed as its new value, and **a
key the jail deleted as an explicit null tombstone** so the next render does not
resurrect it.

**So "a key no layer declares" needs saying precisely**, because the captured
overlay *is* one of the layers. A key is deleted on the next render only when it is
in the real file and in **none** of `defaults`, each pack's contributions, `managed`,
`computed`, the `host` layer where there is one, and the captured overlay — a
narrower and more interesting set than "anything the user typed":

- the write happened **after** the last capture. Capture is deferred to the next
  entrypoint run, so there is a real window;
- the surface's mode does not capture at all;
- the overlay was reset (`yolo config reset` truncates the surface to its pure
  render, deliberately);
- the key was written directly into a composed file **on the host**, where capture
  reads the jail's copy.

| | `rmw` | compose + capture |
| :--- | :--- | :--- |
| A key in the real file that no layer supplies | survives, untouched, **forever** | **deleted** on the next render |
| The file is | a *history* — every write anyone ever made | a *function* of (declarations + captured overlay) |
| Delete the file | it is gone; nothing can rebuild it | the next boot reproduces it exactly |
| An edit made in the jail | bytes in one file on one machine | a recorded patch in a sidecar yolo owns |

**There is one structural difference, and it is regenerability.** Under
compose+capture the file is a pure function of its inputs (and the *delete the
file* row is measured, not hoped: an absent current file **skips capture** rather
than tombstoning every key — *"we bias toward under-capture rather than freezing a
spurious delta"*, [`staterender.go`](../../internal/agentcfg/staterender.go)
— so the next boot renders `layers + overlay` and reproduces it); under `rmw` the
file *is* the state, because `rmw`'s defining property — never touch what you do
not own — means the file holds bytes that exist nowhere else. That is not a missing
feature: making `rmw` regenerable would require recording everything it preserves,
which is compose.

Two consequences follow, and both are things this document wants:

1. **The tooling needs a definition to compare against.** `yolo config render`,
   `config diff`, drift detection and `config reset` all measure a real file
   against what it *should* be. Under `rmw` there is no such thing, so drift has
   nothing to be drift *from* and reset has nothing to reset *to*.
2. **Promotion needs the edit to exist as an object.** Because compose must record
   an edit to reproduce it, the edit is already a patch yolo owns — liftable into a
   pack and carryable to every other jail and to the host. `rmw` has no reason to
   record anything, so there is nothing to lift. This is exactly why
   [§5](#5-promotion--the-way-out-of-capture) is *"the way out of capture"* rather
   than "the way out of editing".

> [!IMPORTANT]
> **The convergence is real but one-directional, and that is the trap.** Any file
> compose+capture can produce, `rmw` can also hold — so two machines in steady
> state look identical and the mechanisms look redundant. What differs is the
> operations they *permit*, and that only shows when something changes: a pack is
> dropped, a file is deleted, an edit is promoted. Judging these two by the file
> they leave behind judges them by the one case that cannot tell them apart.

Three pieces of evidence that the resulting ambiguity is structural, not felt:

**(1) Provenance laundering already required a fix.** From
[`compose.go`](../../internal/agentcfg/compose.go)'s `RetiredLayer` note: the rmw
notch derives `host` for every key already in the file, then upgrades the ones a
live layer claims — so drop the pack that contributed a key and the key *yolo
wrote* reads back as `host`, "the user set this," self-reinforcing forever. A new
provenance token (`retired:<layer>`) had to be invented because **`rmw` cannot
distinguish yolo's leftovers from the user's keys**. That is the ownership
question failing to have an answer, in shipped code.

> [!NOTE]
> **`rmw`'s inability to express removal is a fixable limitation, not a
> distinction in kind.** `rmw` cannot say "this key should stop existing", which is
> defect (1) above; but nothing about the mode forbids giving it a tombstone list,
> and `retired:<layer>` is already the beginning of one. So it belongs in the
> evidence that the *current* asymmetry is incoherent — not in the argument that
> the two mechanisms differ structurally. That argument is regenerability, and it
> is the only one.

**(2) There is no `--revert`,** though the design doc treats it as wanted and names
the missing memory as the blocker. That memory now exists
([§2.3](#23-the-notches-and-what-each-keeps-on-disk)).

**(3) Host capture refuses for two *further* reasons, neither of which is "it
would be meaningless here"** — data loss and privacy, both in
[§2.4](#24-the-verbs-that-exist-and-the-ones-that-do-not). Four justifications for
one asymmetry, and they do not compose into a story.

> [!IMPORTANT]
> **What survives the critique is an OWNERSHIP axis, not a notch axis — and "the
> jail home is disposable" is the intuitive reading that gets this wrong.**
>
> An **owned** file is derived output: delete it and the next render reproduces it
> exactly, as [§3.1](#31-what-actually-differs-between-rmw-and-capture)'s own table
> says. So an owned file is disposable **wherever it lives, host included**. What
> is not disposable is a file holding bytes that exist nowhere else — which is
> every file yolo does not yet own.
>
> **So the host's specialness is a TRANSITION, not a property.** Before adoption a
> host file is the user's and irreplaceable; after adoption it is derived and
> reproducible. The dangerous moment is the crossing, not the address.
>
> **And jails have that crossing too — measured, not theoretical.** A jail starts
> empty, so the common case has nothing to lose. But enable a pack that starts
> owning a file the home already has and you are in the same transition: that is
> `ComposeStateful`'s `firstMigration` branch, and **the B1 data-loss fix exists
> because a jail lost real state to it** — `copilot/config` collapsed to
> `{"yolo": true}` and logged the user out, destroying `copilot_tokens` that lived
> only in that jail home ([§6.3.1](#631-adoption-is-capture-then-regenerate)). A
> home that holds an agent's auth tokens is not disposable, whatever notch it is on.
>
> **What this changes.** The guard belongs on the **transition** — first render into
> a home that already has content — which is what `FirstApply` keys on host-side and
> `firstMigration` keys on jail-side. Those are two signals of one *shape* (each is
> "no sidecar for this surface yet") and **not two names for one concept**: they
> differ in sidecar, trigger and consequence. `FirstApply` is *no provenance record*
> and gates a **consent prompt** ([`hostrender.go`](../../internal/entrypoint/hostrender.go));
> `firstMigration` is *no trusted `last_render`* and triggers **adoption**
> ([`staterender.go`](../../internal/agentcfg/staterender.go)). The
> difference bites: on a home already on `assert`, provenance exists, so `FirstApply`
> is **false** at the very transition to `own`, and the prompt cannot fire there
> ([§6.3.1](#631-adoption-is-capture-then-regenerate)). The residual host-only fact
> is narrow and real — a jail's bad transition is survivable by relaunching from a
> clean home, and the host has no clean home to fall back to — which justifies a
> **stronger net** there ([§6.3.3](#633-what-survives-as-a-guard)), never a different
> rule.

---

## 4. Declaring ownership — the `host_management` key

**A new user-scope config key states the ownership contract for the host, and
that statement selects the surface mode the host notch renders in.** This is P1
and P2 made concrete, and it makes "treat the host like a jail" literally true
rather than aspirational — `own` *is* the jail's mode.

### 4.1 The key

```jsonc
// ~/.config/yolo-jail/config.jsonc  — USER SCOPE ONLY
{
  "host_management": "own"   // "none" | "assert" | "own"
}
```

| Value | Host surfaces render as | Who owns the file | `yolo host apply` |
|---|---|---|---|
| `none` | `unrendered` | the user, entirely | refuses, naming the key |
| `assert` | `rmw` | shared: yolo owns declared keys, user owns the rest | today's behavior |
| `own` | `stateful` | yolo — the file is derived output | composes the whole file, captures edits |

> [!IMPORTANT]
> **That is the SHIPPED key, and the middle row is ruled out**
> ([§4.5](#45-retiring-assert--the-two-value-key), 2026-09-20). Two values remain and the
> **default moves to `none`**, which changes the first row's meaning as much as it deletes the
> second: `none` stops being the value a user opts into and becomes the one they start at. The
> table stays as written because it describes the tree, and everything in this section below it
> is present-tense about `assert` for the same reason — the ruling is recorded, the build is
> not done, and a design doc that describes an unbuilt tree cannot be checked against one.

Consequences worth stating because users act on them:

- Under `none`, the host file is today still **read** into a jail as the `host`
  layer — for the two surfaces with a `reads-host` grant, on the backends that can
  mount it. Ownership governs writing; the read is a separate grant, and whether it
  survives at all is [OQ-CO11](#13-decision-ledger).
- Under `none`, `host_apply_on_launch` has nothing to check: with no rendered host
  surface there is no staleness, and the *nothing would change ⇒ silent* rule
  ([§4.4](#44-what-the-key-does-not-do)) makes the launch check a no-op rather than
  a nag.
- Under `assert`, keeping `~/.claude/settings.json` in a dotfiles repo is
  coherent but mixes two authorships; `yolo host apply` is idempotent, so the
  diff is stable after the first apply.
- Under `own`, the file is **derived** and belongs in `.gitignore`. What goes in
  the dotfiles repo is the pack — which is the arrangement the mixed-authorship
  problem actually wants.

### 4.2 Scope, defaults, and failure direction

- **User scope only, read directly from the user config**, exactly as
  `host_apply_on_launch` is ([§2.5](#25-the-three-existing-host-user-scope-keys)).
  A workspace `yolo-jail.jsonc` is jail-writable, so a cloned repository must not
  be able to promote itself to owning the user's home. Workspace scope is
  *inexpressible*, and a workspace-scope occurrence is a validation error as
  defense in depth.
- **Not inherited into a nested jail**, joining the three entries in
  [`inherit.go`](../../internal/config/inherit.go) for the same reason
  they are there: in a jail the key's referent rebinds to a disposable home.
- **Unreadable or unparseable user config yields `none`.** Fail closed: a config
  nobody could read has granted no write claim. Stated here per key, because the
  shared scope helper does not choose a direction
  ([§2.5](#25-the-three-existing-host-user-scope-keys)).

### 4.3 The unset state, and what happens to everyone already running

An **absent** key is not a fourth value; it is the *unset* state, and it
needs no ceremony of its own — **the default carries the whole migration.**

> [!NOTE]
> **"Unset", deliberately, and not "undeclared".** *Undeclared* already has a
> formal meaning three sections of this corpus rely on: the
> [Undeclared tier of the input closure](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails)
> — an input that shapes an environment while nothing names it, which is a
> statement about **a value inside an agent's config file**. This section is about
> **a yolo config key that has no value at all**, which is the ordinary word
> *unset*. The two appear four paragraphs apart and have opposite remedies: an
> undeclared key is promoted into a pack or discarded; an unset key is written.
> [§11](#11-success-criteria)'s "undeclared key" is the closure sense.

1. Behavior is `assert` — today's behavior, so nothing breaks on upgrade day,
   and nobody is interrupted in order to be told that.
2. **No migration prompt and no notice.** Each value explains itself at the
   point of the act instead: `yolo host apply` under `none` writes nothing and
   names the key that decided it; `assert` and `own` do what they are configured
   to do. A prompt at upgrade asks the question at the one moment the user has
   least to go on — before they have seen any of the three values behave — and
   buys nothing, because every path it guards is either today's behavior or a
   value the user typed on purpose.
3. `yolo apply --sealed` **refuses** while the key is unset, listing it beside
   the two *undeclared inputs* (closure sense) it already refuses for
   ([`apply.go`](../../internal/cli/apply.go)). An environment
   whose host-ownership contract is unstated is not sealed. **This is the one
   place an unset key bites**, and it bites where the user asked a question
   about declaredness rather than where they asked for an apply.

That is the whole migration: a default, and one refusal in the command whose
whole job is to audit what is declared. There is no rewrite of anyone's files at
upgrade, and no host apply behaves differently until its user writes the key.

> [!WARNING]
> **The last sentence is exactly what the 2026-09-20 ruling costs, and it is why the ruling
> reopened this document.** The whole migration above works because the default **equals
> today's behaviour**, so the upgrade is unobservable and no notice is owed —
> [`OQ-CO2`](#13-decision-ledger) rules "neither prompt nor notice" on that ground and on no
> other. Moving the default to `none` ([§4.5](#45-retiring-assert--the-two-value-key)) removes
> the ground: for a user who has run `yolo host apply` and never wrote the key, the flip means
> yolo silently stops maintaining a file it has been maintaining, leaving the keys it last
> wrote in place with nothing left to retire them. [`OQ-CO2`](#13-decision-ledger)'s conclusion may well survive —
> but it has to be re-argued rather than inherited, which is
> [`OQ-CO14`](#oq-co14).

**Adopting `own` later needs no ceremony either, and that is a property of the
render engine rather than a promise made here** —
[§6.3](#63-the-one-asymmetry-that-survives-deletion) has the mechanism and the
three classes it does not cover.

### 4.4 What the key does not do

It does **not** grant approval for any individual write. That distinction is
`host_apply_on_launch`'s hard-won ruling — "THE KEY ENABLES THE MECHANISM. IT
DOES NOT GRANT THE APPROVAL"
([`hostapplyonlaunch.go`](../../internal/config/hostapplyonlaunch.go)) —
and it holds here identically: `host_management: own` selects the mode, it does
not pre-authorize the writes that mode performs. Both keys are read: `host_management` says
*how* the host renders, and `host_apply_on_launch` says *when* a re-render is checked. ⚠ **They
are no longer independent in their defaults.** Since 2026-09-22 an unset `host_wrappers` is
derived from `own`, and an unset `host_apply_on_launch` follows `host_wrappers`. So declaring
`own` alone also turns on the launch-time re-render. An explicit `false` at either key still
wins ([`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-opt-in-key)). The
derivation turns on the mechanism and grants no approval, so the sentence above stands.

> [!IMPORTANT]
> **"Does not grant approval" is not "prompts because it is owned".** That is the
> natural second reading and it is wrong: the shipped rule is driven by **what the
> write would do**, never by which key is set, and it has two teeth.
>
> - **Nothing would change ⇒ silent.** The four dispositions
>   ([`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-dispositions))
>   put it as an invariant, not a default: *"A freshly-applied home must prompt
>   **not at all, ever**, until something actually changes."*
> - **A change that changes nothing the user has ⇒ still silent.** The host apply
>   gate is `confirmHostLosses` ([`apply.go`](../../internal/cli/apply.go),
>   wired on the writing path at [`apply.go`](../../internal/cli/apply.go)),
>   and its first stated property is *ONLY WHEN SOMETHING IS ACTUALLY LOST* —
>   gated on `FirstApply && EntryLosses`
>   ([`apply.go`](../../internal/cli/apply.go)), so a home yolo has
>   asserted before "prompts not at all". A scalar whose value merely changes is
>   reported as an ordinary `⚠` and does not prompt.
>
> ⚠ Its docstring still says `Overwrites` where the code reads `EntryLosses`
> ([`apply.go`](../../internal/cli/apply.go)) — a drift to know before
> quoting it.
>
> So an owned host in steady state is silent, and the prompt that remains is the
> one that was already there for the case that was already dangerous. `own` adds
> no interruption of its own — which is the point, because a confirmation that
> fires on every apply is the mechanism `confirmHostLosses`' own docstring refuses
> to build.

### 4.5 Retiring `assert` — the two-value key

**RULED 2026-09-20, not built.** `host_management` keeps **`none` and `own`**, and **`none`
becomes the unset answer**. The maintainer's reason, verbatim: *"shouldn't they just be a single
promote away from having their configs managed correctly? assert is just too fragile in
general."* This **reverses [`OQ-CO1`](#13-decision-ledger)** — the only ruling in
[§13](#13-decision-ledger) this design has taken back — and the reference doc that owns the
notch carries the same ruling from its own side
([`config-target-resolution.md`](../reference/config-target-resolution.md#ruled-2026-09-20-not-built-retiring-assert)).

#### Why this is a reversal on new information, and not a flip-flop

**[`OQ-CO1`](#13-decision-ledger) was not wrong when it was ruled.** Its recorded reasoning is
one sentence — *"`assert` is shipped behavior with real users, so collapsing it is either a
regression or a forced escalation to `own`"* — which weighs **migration** cost and the cost of
explaining a third value, and names no engine mechanism at all. Two things have changed under it.

**1. The escalation is no longer forced** (MEASURED, `git log -S` 2026-09-20). `yolo config
promote` did not exist on 2026-09-10: it shipped two days after the ruling (`4223ca95`), in the
same sprint as `host_management` itself (`6012ff9e`). It declares a captured key into the
conventional local pack and clears **only that key**
(`TestPromoteDeclaresTheKeyLocallyAndClearsOnlyThatKey`), so the choice is no longer "regress or
have your whole file composed" — it is one verb, per key, into something declared. That is the
maintainer's sentence, and it is the half of [`OQ-CO1`](#13-decision-ledger)'s premise that has expired rather than
been overruled.

**2. Mechanism cost was never on the balance.** What keeping `assert` obliges the engine to
carry is set out from the notch's side in
[what keeping `assert` costs](../reference/config-target-resolution.md#what-keeping-assert-costs-measured-here),
and [§4.5.2](#452-what-the-ruling-does-not-delete--the-mechanism-tally-corrected) corrects the
tally against the tree.

> [!WARNING]
> **One overstatement to refuse, because a reader can check it in a minute and flip the ruling
> back on it.** It is tempting to say the machinery postdates [`OQ-CO1`](#13-decision-ledger) and so could not have
> been weighed. That is true of **one** of the three mechanisms and false of the others, dated
> with `git log -S` on 2026-09-20: the render/baseline mark (`HostLayerRender`,
> `hostLayerIsRender`) arrived in `369c6f63` on 2026-09-18, **eight days after** the ruling,
> while `layerRetired`/`RetiredLayer` and `LayerAsserted`'s refusal of `host` both arrived in
> `ecf35644` on 2026-08-03, **five weeks before** it. So *"ruled on incomplete information"* is
> exact for the mark and for promote, and for the rest the honest word is **uncounted** — they
> were there to be weighed and [`OQ-CO1`](#13-decision-ledger)'s sentence does not weigh them.

#### 4.5.1 What actually carries it: `assert` is the one contract where P6 and P7 collide

[`config-target-resolution.md`](../reference/config-target-resolution.md#principles)'s **P6** —
*"yolo never reads back a file it writes"* — and its **P7** — *"adopting yolo requires no
migration"*, the fresh user's `~/.claude/settings.json` composing into a jail transparently —
are both satisfiable at `none` and at `own`, and **only at `assert` does satisfying one break
the other.** The chain, READ in the tree on 2026-09-20:

1. **P6 is defended by a mark that is per FILE.** The launcher labels a delivered host copy a
   *render* from `entrypoint.HostSurfaceRendered`, which is `hostProvenanceExists` — *"has yolo
   EVER asserted this surface in this home"* (`run.hostLayerIsRender`). A labelled copy is a
   **baseline**, never a layer.
2. **Under `assert` one file carries both authorships and no sidecar separates them.** The
   file holds the user's own keys *and* the keys yolo rmw-asserts into it, so a per-file answer
   has to pick one for both.
3. **It picks baseline, and the justification does not hold at this contract.**
   `HostLayerRender`'s own docstring says a render-composed surface drops none of the user's keys
   because *"they are the CAPTURE, which is already its own layer"* — true at `own`, and there
   **is no host capture at `assert`**: [`OQ-CO3`](#13-decision-ledger) scoped the capture store
   to `own`, and `render.HostAssertModes` does not run `stateful`.
4. **So on an `assert` home, one `yolo host apply` is enough to stop the user's own keys
   reaching any jail.** The host layer is gone by the mark, and nothing else carries them.

That is P7 inverted by the mechanism built to serve P6 — and it is confined to `assert` by
construction: at `none` yolo writes nothing, so the file stays purely the user's and composes as
a layer; at `own` what the user typed lives in the capture overlay, which is a layer of its own,
so the baseline answer costs nothing. **The mark is not wrong. It is being asked a per-file
question about a file with two owners**, which is the shape of the fragility the ruling names.

> [!NOTE]
> **Step 4 is a READ of two shipped mechanisms' interaction, not a run.** Each link is measured
> — the census records `rmw` under `assert`, every recording render writes the provenance
> record, and `hostLayerIsRender` reads exactly that record — but the end-to-end loss has not
> been reproduced on a live `assert` home, and whoever builds the retirement should reproduce it
> before quoting it as a user-visible defect. The sibling doc reaches the same hazard from the
> migration side and leaves it live work
> ([there](../reference/config-target-resolution.md#does-the-renderbaseline-disposition-become-dead)).

**Three things the ruling deletes outright**, each measured rather than inferred:

- **The coercion.** `render.HostAssertModes` renders *every* composing surface through `rmw` —
  a surface declaring `stateful` or `computed` included. That coercion is what puts the two
  `readsHost` surfaces on the write path at all, and it is the whole of step 2 above.
  `HostOwnedModes` coerces nothing.
- **`--to host`.** `refuseHostPromoteContract`
  ([`configpromotewrite.go`](../../internal/cli/configpromotewrite.go)) refuses the destination
  under `none` *and* under `own`, for opposite and both-principled reasons, so `assert` is the
  only contract it is legal at. Retiring it leaves the destination with no posture to exist in —
  a **dissolution**, not a deprecation, and [§5.1](#51-surface)'s table and its `IMPORTANT` note
  go with it.
- **The transition no guard can see.** `confirmHostLosses` fires only on
  `FirstApply && EntryLosses` ([`apply.go`](../../internal/cli/apply.go)), and `FirstApply` is
  false on a home yolo has already asserted — so the `assert` → `own` switch, the exact one that
  drops a deep-merged leaf, is **unprompted today** ([§6.3.2](#632-the-three-classes-adoption-does-not-cover)).
  With `none` the only starting contract, an adoption is a first apply by construction and the
  prompt can fire at the moment it was written for. ⚠ Not retroactively: a home asserted into
  *before* the retirement keeps its record, so this is a property of new homes and one more face
  of [`OQ-CO14`](#oq-co14).

#### 4.5.2 What the ruling does NOT delete — the mechanism tally, corrected

**MEASURED 2026-09-20, and it cuts against the argument as first written.** The claim that the
three mechanisms are *"precisely the price of `assert` existing"* is true of the coercion and
**not** of two of the three, because **`own` runs and records `rmw` too**:
`render.HostOwnedModes` holds `rmw` in both its `runs` and its `records` set, deliberately —
*"`own` is the USER's statement about who owns the file; it is not a licence to overrule the
PACK's statement about what kind of file it is"* — and the rmw writer's provenance gate is
`Modes().Records(manifest.ModeRMW)` ([`prism.go`](../../internal/entrypoint/prism.go)), which is
true there. The surfaces that declare `rmw` today are `claude/config` and `copilot/config`
(`rg -n '"mode": "rmw"' packs/*/pack.json`), both files holding live agent state. So:

| Mechanism | At an `own` host after the retirement |
| :--- | :--- |
| `layerRetired` / `RetiredLayer`, and `retireUnclaimed`'s use of the previous record | **Live.** `rmwProvenance` still derives `host` for every key the file already had and still needs the previous record to tell yolo's leftovers from the user's keys |
| `LayerAsserted`'s refusal of `host` — the laundering asymmetry | **Live**, and for the same surfaces, since it is the filter that record passes through |
| The render/baseline mark | **Live.** `own` writes the host file, so a jail must still be told those bytes are a render rather than a layer |

**What the ruling removes is not the mechanisms — it is the one contract under which they cannot
be made to agree.** Two corollaries follow, and both are worth having before anyone builds this:

- **The P6 exposure narrows to nothing even though rmw survives.** Neither `claude/config` nor
  `copilot/config` declares `readsHost`, and `readsHost` is the only channel by which a host file
  re-enters a jail as a layer ([§5.1.1](#511-why-only-two-surfaces-have-a-host-layer)). So an
  `own` host's rmw surfaces are written and never read back — P6 is satisfied by the *pairing*
  of the two declarations, not by the absence of rmw.
- **The mark's objection dies, but the mark does not.** The
  [MARK warning](../reference/config-target-resolution.md#a-staged-copy-is-not-always-a-layer)
  rejects keying the render/layer decision on POSTURE because *"an absent `host_management`
  resolves to `assert`"*, so a posture test would mislabel every default install. Moving the
  default to `none` removes that premise. ⚠ It does **not** follow that posture can then replace
  the mark: under `own` a surface whose render is refused (`hostMechanismRefusal` — a keyless
  surface, a file yolo cannot parse) leaves the user's bytes in place and writes no record, so
  the mark still answers per surface where a posture can only answer per home. Whether to
  substitute is a question for whoever builds this, and the fail-safe answer is to keep reading
  the mark.

#### What this ruling obliges

Recorded here rather than in [§10](#10-what-i-would-build-in-order), whose build order is
history:

1. **Rule [`OQ-CO14`](#oq-co14)
   first.** It decides what the other steps do to an existing installation, and one of its faces
   re-opens [`OQ-CO2`](#13-decision-ledger)'s "neither prompt nor notice".
2. **Drop the value and move the default**, together — `config.KnownHostManagements`, the
   validator's enumeration, and `config.HostManagementDeclared`'s absent-key answer. Doing
   either alone reproduces the failure the MARK warning describes, from the other end.
3. **Delete `render.HostAssertModes` and its census entry**, leaving the host notch two
   contracts. The `excluded` reasons that name `assert` as the remedy (`HostOwnedModes` tells a
   `computed` surface to *"set `host_management: assert`"*) need new text, not deletion.
4. **Dissolve `--to host`**: `refuseHostPromoteContract`, the destination table in
   [§5.1](#51-surface), `promoteDestHost`, and the per-surface `promotionNoHostLayer`
   classification that only that destination reaches.
5. **Re-verify, do not assume, that the three mechanisms in
   [§4.5.2](#452-what-the-ruling-does-not-delete--the-mechanism-tally-corrected) still have
   their `own`-side callers** after the census loses an entry. The table above says they do; a
   build that deletes one on the strength of the ruling's original wording would take
   `claude/config`'s anti-laundering pass with it.

---

## 5. Promotion — the way out of capture

**`yolo config promote` turns captured keys into declared ones.** It is the verb
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md)
Phase 5.3 specified, that
[`environment-manager-user-stories.md`](environment-manager-user-stories.md)'s
`Q1` leans on, and that the one shipped message already names in prose (P4).

The punchline for the cross-jail question: **the conventional local pack is the
channel.** `~/.config/yolo-jail/local/` needs no `packs` entry, is selected
implicitly when it exists, is re-read live every launch, and its
`config-overlay` contributions render at *every* notch. Measured 2026-09-09: a
local pack carrying a `config-overlay` on `claude/settings` rendered into a
scratch host home under `yolo host apply --assert`, reported as
`config-overlay keys from: local`. So promoting a key into the local pack
delivers it to every jail *and* the host in one move. Promotion does not need a
distribution mechanism; it needs a destination.

**And that destination folds last.** The conventional local pack is appended after
every configured entry — *"ORDER IS LOAD-BEARING, AND IT IS LAST"*
([`config/packs.go`](../../internal/config/packs.go)) — and
`config-overlay` layers fold in pack order, later wins
([`compose.go`](../../internal/agentcfg/compose.go)), so a `local`
promotion outranks every other pack's overlay on the same key.
[§5.4](#54-promotion-moves-a-key-down-the-stack) rests on this (verified
2026-09-11).

### 5.1 Surface

```console
$ yolo config promote <agent[/surface]> [--keys a,b] [--to <dest>]
                              [--plan] [--json] [--answers <file>]
                              [--accept-promotion]
```

| Destination | Written as | Notes |
|---|---|---|
| `local` (default) | a `config-overlay` on the surface, in `~/.config/yolo-jail/local/pack.json` | reaches every jail and the host |
| `pack:<name>` | the same, in that pack's `pack.json` | **refused for a fetched pack** — not the user's file to edit |
| `host` | the keys themselves, into the surface's own real-home file | only under `host_management: assert`; refused under `own` (the file is derived — promote to a pack) and under `none`. **Reaches a jail for only two shipped surfaces** — see below |
| `workspace` | *(nothing to write to)* | **out of scope for this design** ([§13](#13-decision-ledger)) |

> [!IMPORTANT]
> **The `host` row is dissolved by the 2026-09-20 ruling, not narrowed**
> ([§4.5](#45-retiring-assert--the-two-value-key)). Its own "only under `host_management:
> assert`" is the whole of it: `refuseHostPromoteContract` refuses the destination under `none`
> and under `own` on principled and opposite grounds, so retiring `assert` leaves it legal
> nowhere. The `IMPORTANT` note below — why `local` and `host` are not equivalent — becomes
> history rather than guidance, and it is kept because the reasoning is what makes the
> dissolution obviously right rather than a casualty.

Promotion **shows what it is about to write and asks**; `--accept-promotion` is
the only way past the confirmation, and it is the scripting path rather than a
default. `--keys a,b` selects specific keys and omitting it means all of them —
either way the confirmation lists what was selected, so "all" is never silently
wider than the user pictured. `--plan` is the read-only form for looking without
deciding.

`--to workspace` is out of scope **as a decision, not a wait** — but it could not
have been built here in any case, because three separate pieces are missing: the
`workspace` layer has no config key (`agent_config` appears nowhere in
`internal/config`), no producer sets `Inputs.Workspace` anywhere in the tree
([`compose.go`](../../internal/agentcfg/compose.go) is its only reader), and
`render.Host` leaves it empty *by definition* — the docstring that stated so sat
on `prism.go`'s `targetTransformScript`, which went with the Lua transform on
2026-09-11 ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)) — so the
destination could never reach the host at all.
Whoever wires that layer owns one argument this design will not pre-empt: the
workspace config is **jail-writable**, and a layer an agent can edit sitting above
the `host` layer must never be given a path to a real home.

> [!IMPORTANT]
> **`local` and `host` both "reach every jail", and they are not remotely
> equivalent.** Three facts decide it, all measured against the tree on 2026-09-10.
>
> **1. Which file.** `--to host` writes the surface's *own* real-home path — for
> `claude/settings` that is `~/.claude/settings.json`. It edits that file
> directly, with no pack anywhere in the loop. That is not a workaround; it is
> what `assert` *means* — shared ownership of a hand-editable file, which
> [§7](#7-what-this-does-not-propose) keeps as fully supported and the right
> answer for a single machine.
>
> **2. It comes back into a jail only through `reads-host`, which is per-pack
> and rare.** The host file is not read into a jail because it is the host file;
> it is read because a pack asked for it. Exactly **two** shipped grants exist:
> `packs/claude` (`.claude/settings.json` → `host-claude/settings.json`) and
> `packs/pi` (`.pi/agent/settings.json` → `host-pi/settings.json`). So for
> `codex/config`, `opencode/config`, `agy/*`, `claude/config`, `pi/models` and
> `mise/config`, a `--to host` promotion is a **host-only edit that no jail will
> ever read**.
>
> **3. Even where it is read, it lands at the second-weakest precedence.** The
> `host` layer is second in the stack ([§2.1](#21-the-layer-stack)) — under
> `workspace`, every `config-overlay`, capture, `computed` and `managed`. A
> `local` promotion lands as a `config-overlay`, three slots higher, at *every*
> notch. So the two destinations differ in reach, in precedence, and in whether
> any pack has to opt in.
>
> **What follows for the design:** `--to host` is not a general destination. It
> is a narrow convenience for the two surfaces that have a host layer, under the
> one ownership value that keeps the file the user's. It is never the answer to
> "get this key to all my jails" — that is `local`, and the promote UI should
> not offer `host` as though it were the same kind of thing.

#### 5.1.1 Why only two surfaces have a host layer

Eleven pack surfaces ship; two carry a `reads-host` grant, and no doc records a
reason any of the nine declined. Both grants that exist are *"the settings surface
of an agent that has one"* — a pattern, not a chosen subset. This is
[OQ-CO10](#13-decision-ledger); the findings below are what make it answerable.

**The grant is pack-declared, and it should stay declared.** `reads-host` carries
a real file out of the user's home into a container, and it is `ReviewWorthy` in
the footprint (`internal/packload/footprint_test.go`), which is what puts it
on the startup disclosure banner. A generic rule — *"every surface imports its own
host file"* — would make every jail read host state with nothing declaring it and
nothing to disclose, which is the shape
[`gate-placement-principle.md`](../reference/gate-placement-principle.md) exists to
refuse.

> [!WARNING]
> **`reads-host` is a disclosure, not a gate.** Calling it one is the intuitive
> reading, and it is wrong: a declared grant is **unconditionally honored** — no
> approval, no origin check, no per-pack decision.
>
> ```go
> // internal/packload/packload.go — since OQ-CO10 it also returns the surfaces' own grants
> func (p *Pack) HonoredHostFiles() (granted []packdecl.HostFile, refused []string) {
>     return append(p.Decl.HostFileContributions(), p.SurfaceHostFiles()...), nil
> }
> ```
>
> [OQ-TP9](trust-paths.md#decision-ledger) **deleted `MayAccessHost` and the whole
> fetched-pack origin gate on 2026-09-04**, and `HonoredMounts` says the same for
> mounts: *"Nothing is refused — a mount reads the host home exactly like a host
> file, and [OQ-TP9](trust-paths.md#decision-ledger) retired that gate for both."*
> The `refused` return slot is **vestigial** — its only consumer
> (`run/packrefusal.go`) was deleted with the gate, and every non-test call site of
> `HonoredHostFiles` and `HonoredMounts` discards it as `granted, _ :=` (verified
> 2026-09-11; the sibling `HonoredInstalls` still has one real consumer, so the
> claim is about the host-file half of the family, not all of it). Two guard tests go red if
> a refusal source reappears: `TestNoFetchedPackHostAccessGateExists`
> ([`hostaccessgates_test.go`](../../internal/packload/hostaccessgates_test.go))
> and `TestFetchedPackHostClaimsAreHonoredWithNoApproval`
> ([`packnohostgate_test.go`](../../internal/cli/run/packnohostgate_test.go)).
> ⚠ The docstring at [`packload.go`](../../internal/packload/packload.go)
> miscounts the call sites and names a `TestNoPackHostAccessGate` that does not
> exist — check the tree, not the comment.
>
> **This strengthens the packaging argument rather than weakening it.** A
> declaration that can never be refused does exactly one job — *disclosure* — and a
> field on the surface discloses precisely as well as a separate contribution kind
> does.

**The declaration carries no information.** Measured across all six shipped agent
packs on 2026-09-10, both grants name **exactly the surface's own path** with `~/`
stripped:

| Pack | Surface path | `reads-host` source |
| :--- | :--- | :--- |
| `claude` | `~/.claude/settings.json` | `.claude/settings.json` |
| `pi` | `~/.pi/agent/settings.json` | `.pi/agent/settings.json` |

The field is shaped like "which host file?" and used as "does this surface have a
host layer at all?" — a boolean in a path's clothing. **And there is no second
location a path could name:** every shipped surface path is `~/`-relative (all
eleven pack surfaces plus core's `mise/config`, zero exceptions, measured
2026-09-10), and `~` is the only thing that differs between the two homes.
`HostSource`'s own docstring already calls itself *"Derived, not declared"* — it is
the `/ctx` mount path the host CLI chose, not a path anyone wrote down
(`manifest.Surface.HostSource`, [`manifest.go`](../../internal/agentcfg/manifest/manifest.go)).

##### The binding is a basename match, and the read that depends on it is fail-open

> [!NOTE]
> **SHIPPED 2026-09-12, and this section describes the state it replaced.** The declaration
> is a `readsHost` field on the surface (`manifest.Surface.ReadsHost`), `HasHostLayer()` reads
> it, the `/ctx` path is derived from the surface's own path by one expression both halves run
> (`packload.SurfaceHostFile` → `CtxPath`), and the read fails CLOSED against a report of what
> the launcher delivered (`packload.HostLayerReport`). A launch that delivered NOTHING reports
> `unsupported` and is **not** refused for a delivery nobody attempted — the carve-out and its
> reasoning are in `internal/macosuser/hostlayers_test.go`. Everything below is the diagnosis
> that produced [OQ-CO10](#13-decision-ledger), kept because the ruling rests on it.
>
> **⚠ Amended 2026-09-13.** This note said `macos-user` reports `unsupported`, because that
> backend had no mechanism for carrying host bytes at all. DP-L1 gave it one — a host-side
> COPY into a root-owned tree — so it reports `supported` whenever a tree was staged, and a
> delivered file it cannot read now REFUSES the launch exactly as every other backend's does.
> `unsupported` survives as a fact about a LAUNCH that staged nothing (the install capture is
> the shipped one), which is what leaves [`OQ-R3`](../reference/loopback-tls-reachability.md#oq-r3)'s no-refusal rule intact.

The surface already carries the field: `Surface.HasHostLayer()` is *exactly*
`HostSource != ""`, the one predicate the boot render and the host-side `config`
verbs both consult. The `reads-host` contribution does not express that; it
*populates* it, across a seam:

```go
// internal/packload/packload.go, as of 2026-09-10 — replaced by surfaceHostSource
func (p *Pack) hostSourceFor(surfacePath string, granted []packdecl.HostFile) string {
    want := path.Base(surfacePath)
    for _, hf := range granted {
        if path.Base(hf.From) != want { continue }
        return CtxPath(p.StagedSlug(), hf)
    }
    return ""
}
```

A surface is bound to its grant **if the two paths share a final component**. Four
consequences, none of them chosen:

1. **The directory is ignored.** A grant for `.claude/settings.json` binds to a
   surface at any path ending `settings.json`. Two surfaces in one pack sharing a
   basename would both take the first matching grant. Nothing shipped collides —
   `pi/settings` and `pi/models` differ — so the hazard is latent, not live.
2. **A typo does not fail; it un-binds.** A grant whose basename stops matching
   silently yields `HostSource == ""`, and `HasHostLayer()` then reports **false**
   for a surface whose author declared a host layer.
3. **The read is fail-open**: `data, _ := os.ReadFile(remapCtx(surface.HostSource))`
   (`internal/entrypoint/packsurfaces.go`, as of 2026-09-10) discards the error and composes the
   surface **without** its host layer. **This has already shipped a real bug** —
   `StagedSlug`'s docstring records it, measured 2026-09-05: a pack named `my_pack`
   had its file mounted at `/ctx/host-my_pack/` while the entrypoint read
   `/ctx/host-my_5fpack/`, *"and the host-layer read is fail-open, so the surface
   composed"*.
4. **On `macos-user` it fails on every launch, by construction** (verified
   2026-09-11). That backend has no bind mounts and no `/ctx`; `YOLO_CTX_ROOT`, the
   seam that relocates the read, is set for Apple Container only
   ([`assemble.go`](../../internal/cli/run/assemble.go));
   [`runplan.go`](../../internal/macosuser/runplan.go) filters the *user's*
   source-bearing `host_files` out as an *"accepted deficiency"*, but a **pack's**
   `reads-host` grant is neither mounted nor filtered — so `hostSurfaceBytes` finds
   nothing and `claude/settings` composes **without the user's settings**, silently,
   on that backend alone. The fail-open docstring names the case and folds it into
   "no such host file": *"or this is macos-user, with no /ctx at all"*
   ([`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)).
   **That is a user feature-detecting the backend to learn whether their settings
   arrived** — the failure [P5](#1-the-verdict-and-the-principles-it-rests-on)
   exists to forbid — and it counts for both [OQ-CO10](#13-decision-ledger) (fail closed) and
   [OQ-CO11](#13-decision-ledger) (retire).

So *"why allow a surface without it"* has no recorded answer: the separation is two
declarations naming the same file, joined by a string match, guarding a read that
cannot tell "no host layer" from "I could not find it". Note also that **"the host
surface does not exist" is a policy claim wearing a factual one** — the same
docstring says *"Empty means the surface has no host layer […] most surfaces are
yolo-owned outright"*, but the file can exist for every surface, so what it means
is that yolo declines to read it.

> [!IMPORTANT]
> **One thing a fix must not take with it: `reads-host` also serves the user's
> `host_files` key**, which maps to review-worthy `reads-host` claims in the
> footprint (`internal/packload/footprint_test.go`). Those carry *arbitrary*
> host files into a jail — not a config surface's mirrored twin — so they are
> genuinely not derivable and genuinely need a declared path. A naive "delete the
> kind, put a boolean on the surface" would break them. **The kind stays; what
> could move onto the surface is the config-surface binding.**
>
> And `CombineShared` is not the obstacle it looks like. `reads-host` is
> `Combine: CombineShared` (*"Many packs may read one file; no combine"*,
> [`kinds.go`](../../internal/packdecl/kinds.go)), which sounds like a reason it must stand apart
> from a `CombineExclusive` surface. It is not: sharing describes how the **mount**
> de-duplicates when two packs want one file, and says nothing about where the
> **declaration** lives.

### 5.2 What it does, in order

1. Read the capture overlay for the surface from `<workspace>/.yolo/prism/`. Per-entry list
   captures ([§3.1](#31-what-actually-differs-between-rmw-and-capture)) are not read as keys.
   Promote reports how many it found, and says they cannot be promoted yet and that
   `yolo config diff` lists them (`promoteListNote`,
   [`configpromote.go`](../../internal/cli/configpromote.go)). Lifting them is a separate
   roadmap item
   ([`pack-system.md`'s warning](../reference/pack-system.md#list-records-stay-outside-the-overlay)).
2. Drop keys `yolo config diff` already reports as **redundant** — identical to
   what the layers produce anyway — and keys that are **dead**: overridden where
   they already sit, so the move cannot help them. The overlay cannot hold a
   `computed` or `managed` key at all ([§5.4](#54-promotion-moves-a-key-down-the-stack)),
   and those two are what the shipped drop reads (`agentcfg.DeadOverlayKeys` over
   `narrowOverlay`, [`promote.go`](../../internal/agentcfg/promote.go)). The one
   dead class this step used to add — a key the Lua `transform` rewrote, which
   `narrowOverlay` never saw — went with the transform on 2026-09-11
   ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)). Redundant
   is free and it is most of the noise: **re-measured 2026-09-12**, this
   development jail carries three captured top-level keys across three surfaces —
   `codex/config`'s `mcp_servers`, `mise/config`'s `tools`, `opencode/config`'s
   `mcp` — and **two of the three are redundant**, both TOML surfaces reporting
   *"same as yolo's last render"*. The third is redundant in every leaf but one
   (see the gaps below).
3. Classify the remainder ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
4. Check each surviving key would **still win** after the move
   ([§5.4](#54-promotion-moves-a-key-down-the-stack)).
5. Write the selected keys into the destination, deep-merging with what is there.
6. **Reset those keys from the capture overlay**, so the value is declared in
   exactly one place.
7. Record the move in the destination's provenance.

Steps 5–7 are one logical write. If any part fails the whole promotion is
abandoned with nothing changed: a half-promoted key is declared *and* captured,
which is the double-declaration the verb exists to end.

> [!NOTE]
> **The figure above replaces a withdrawn one, and the withdrawal is the point.**
> This step used to read *"of the six keys captured in this development jail on
> 2026-09-09, four were redundant"*. That count came out of the same
> `yolo config diff` comparison step 2 consumes — which decoded **every**
> `last_render` sidecar as JSON, so a TOML sidecar failed to decode, the baseline
> came back empty, and a byte-for-byte-identical capture read as `(added in-jail)`
> on every TOML surface (fixed in `027bd9bd`, 2026-09-12). The old number is not
> recoverable and has not been adjusted: it was read through the bug *and* against
> a different day's sidecars, both of which have since changed. The re-measurement
> was taken with the fixed binary against the live `<workspace>/.yolo/prism/`.
>
> It is the only measured claim in this document that came through that
> comparison. The other 2026-09-09 measurements — the host provenance record
> ([§2.3](#23-the-notches-and-what-each-keeps-on-disk)), the local pack's
> `config-overlay` reaching a host apply ([§5](#5-promotion--the-way-out-of-capture)),
> and the undeclared key left byte-identical under `assert`
> ([§11](#11-success-criteria)) — were all read off
> `yolo host apply --assert`, which does not use it.

> [!WARNING]
> **Two known gaps in the redundancy verdict step 2 rests on.** Both verified
> 2026-09-12, both recorded here rather than fixed; neither is a misreport today.
>
> **1. The verdict is all-or-nothing per top-level key, so a redundant sibling
> leaf rides along into the deep-merge.** `overlayKeyStates` compares whole
> top-level values (`Redundant: baseline[k] == oneLineJSON(v)`,
> [`configdiff.go`](../../internal/cli/configdiff.go)), so one changed leaf makes
> the entire key non-redundant and step 5 deep-merges all of it into the
> destination. **This is live in this jail**, and it is the measured case twice
> over: `opencode/config`'s `mcp` is non-redundant solely because
> `mcp.tavily.environment.TAVILY_API_KEY` expanded — `mcp.chrome-devtools` and
> `mcp.sequential-thinking` are identical to the last render, and `mcp.tavily`'s
> own `command`, `enabled` and `type` are too. Promoting `mcp` would declare four
> values the layers already produce. Refusing a key for a leaf's sake and
> declaring a key for a leaf's sake are the same defect seen from two sides: the
> credential walk already descends to the leaves
> ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)),
> and redundancy does not. Cost it as **narrowing the promoted value to its
> non-redundant leaves**, not as a display fix.
>
> **2. A keyless surface (`lines`/`raw`) gets no redundancy verdict at all.**
> `overlayKeyStates` returns `ok=false` for anything that is not an object, and
> `overlayDiffLines` then prints a bare `<file>` line with the whole captured value
> and no annotation — so `config diff` cannot say whether such a capture is
> identical to the last render, and `readLastRenderKeys` returns an empty baseline
> for it regardless. **Promote already has an answer and it is not "unknown"**: it
> refuses the surface outright, noting *"the whole file is one captured value, so
> there is no key to declare — `yolo config reset` is the only exit"*
> ([`configpromote.go`](../../internal/cli/configpromote.go)). So the gap is
> `config diff`'s output, not promote's decision. **It is latent, not live:** no
> shipped surface declares the `lines` or `raw` codec, and every capture-mode
> surface today is `json` or `toml` (`yolo config ls --all`, 2026-09-12). It
> becomes real the first time a pack declares a capture-mode keyless surface, and
> the answer it needs then is a whole-file redundant/changed verdict — cheap,
> since the sidecar holds the exact bytes.

### 5.3 Classification — what a machine can decide, and what it cannot

This is the "how automatic can this really be" question, and the honest answer is
*mostly, but not entirely* — so the design puts the mechanical part in the tool
and makes the judgement part answerable three ways.

**Fully mechanical, no judgement:**

- **Redundancy** — already computed by `yolo config diff`, and shared as data
  rather than re-derived (`overlayKeyStates`). Whole top-level keys only, and
  nothing at all for a keyless surface — the two gaps in
  [§5.2](#52-what-it-does-in-order).
- **Precedence** — whether the key still wins after moving down the stack. Pure
  function of the layer set.
- **Environment-bound by construction** — a value containing the workspace root,
  the jail home path, or a literal `${workspace}`. Detectable by inspection and
  refused rather than rewritten.
- **Sensitive by declaration** — a key on a deny-list of name patterns, or on a
  surface a pack marks sensitive. **Refused, never redacted**, which is the same
  ruling host-side capture already took rather than inventing a notion of
  redactable secret.

> [!WARNING]
> **The deny-list is load-bearing from day one, and it does not exist yet.**
>
> *Load-bearing:* this development jail's `opencode-config.overlay.json` holds a
> live-looking `TAVILY_API_KEY` **value** (measured 2026-09-11) — absent from
> `last_render`, where yolo wrote the `${TAVILY_API_KEY}` placeholder, and present
> in the overlay, because opencode expanded it into its own file and capture
> recorded the result. The first `promote opencode` in this workspace meets a
> secret on its first key. Note this is a **jail** finding, not a host one.
>
> *Does not exist:* nothing in `internal/agentcfg`, `internal/packdecl` or the
> config verbs matches `sensitive`, `deny-list` or `redact` (grepped 2026-09-11).
> Both the list and the per-surface marking are new work, owned by
> [§10](#10-what-i-would-build-in-order) step 4.
>
> ⚠ ***"a key on a deny-list"* reads as reuse, and there is nothing to reuse.**
> The bullet above is phrased as a lookup against a list yolo has, and that
> sentence is false in both halves: no sensitivity deny-list exists anywhere in
> the tree — not in capture, not in the pack vocabulary, not in the config verbs
> — and no surface can be marked sensitive. Anyone sequencing this step should
> cost it as **new classification**, not as wiring. Half of it shipped with
> promote on 2026-09-12 (`internal/cli/configpromotesensitive.go`): a name-token
> list, matched as whole tokens rather than substrings, walked to the leaves
> because the measured case is `mcp.tavily.environment.TAVILY_API_KEY` four
> levels below a top-level `mcp` that carries no signal. The per-surface
> `sensitive` marking is still unbuilt, and the capture-time guard
> [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to) wants is a
> third thing again — this one refuses at export, after capture has already
> written the bytes to disk.

**Needs judgement, and no heuristic should pretend otherwise:**

- Is `model: "opus[1m]"` a durable personal preference or this project's choice?
- Does an `enabledPlugins` entry reflect a preference, or the fact that *this*
  jail happened to have a language server installed?
- Local pack (just me, everywhere) or a shared pack (my team)?

So: **promote is conservative by default and asks about the rest.** The
questions it cannot answer are emitted as data, and answering them is not an
agent-shaped hole:

```console
$ yolo config promote claude --plan --json
```

emits the classified key list with one open question per undecidable key.
`--answers <file>` consumes rulings in the same shape. A human answers
interactively, a script answers with a file, and an agent is simply a third thing
that writes that file. **There is no agent mode**, and that is deliberate: an
"agent-assisted" code path is a second implementation of the same decision that
drifts from the first.

Absent `--plan`, an interactive run asks the same questions at the prompt and a
non-interactive one **promotes only the keys it can classify mechanically** and
reports the rest as skipped, naming them. Silence about a skipped key is the
failure mode this whole document is about.

### 5.4 Promotion moves a key down the stack

A captured key sits at `overlay`; a promoted key lands at
`config-overlay:<pack>`, which is **below** it ([§2.1](#21-the-layer-stack)). So
promotion could in principle defeat itself: a key that won as a capture might lose
once declared, and the user would see their value silently revert on the next boot
having just been told it was promoted.

**The class is much smaller than that framing suggests, and for the default
destination it is empty.** Three facts, of which the Lua removal has since made
the second moot:

1. **The overlay cannot hold a `computed` or `managed` leaf.** `narrowOverlay`
   strips both from the *accumulated* overlay on every boot, on both branches —
   *"ONE RULE, BOTH BRANCHES"*,
   [`staterender.go`](../../internal/agentcfg/staterender.go) —
   so a key promote reads from the overlay is, by construction, not one those
   layers claim.
2. ~~**A `transform`-overridden key never won at `overlay` either.**~~ **Moot
   since 2026-09-11**, when the Lua transform was removed
   ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)): there is no such key
   any more, and the argument rests on the fact either side of it.
3. **What is left is another pack's `config-overlay` on the same key, ordered
   later** — and the conventional local pack folds **last**
   ([`config/packs.go`](../../internal/config/packs.go)), so for
   `--to local` nothing outranks the promoted key except the overlay it just left.
   The residue is `--to pack:<name>` for a pack ordered before another overlay on
   the same key — which is BACKLOG's bare
   [`OQ-CO`](../plans/BACKLOG.md#-oq-co--two-packs-writing-one-config-overlay-key-is-silent-last-one-wins),
   silent last-one-wins between overlays, seen from the promote side. One latent
   exception: a pack pulled in through `needs` is appended *after* local
   ([`cli/run/packs.go`](../../internal/cli/run/packs.go)); no such pack
   declares a `config-overlay` today.

So the precedence check stays — it is a pure function of the layer set, it is
cheap, and it is what protects `pack:<name>`. It is a **two-sided** check (*wins
now* and *wins after*), and a key that fails it is **refused by name with the
losing layer**, never promoted with a warning.

**Promote never offers the pack's `managed` block as an escape**, at any
destination. For every shipped surface it is not expressible: `Surface.Managed` is
populated only from the owning pack's own `config` contribution plus its autonomy
posture ([`packload.go`](../../internal/packload/packload.go)),
and a `config-overlay` body's field *named* `managed` is *"NOT a claim about the
managed LAYER"* — it folds at the single `config-overlay` slot
([`overlay.go`](../../internal/agentcfg/manifest/overlay.go)). So neither
the local pack nor any `pack:<name>` that is not the surface's owner can write a
managed block, and every shipped surface's owner is an embedded pack promote
refuses to edit. For a surface a *user's own* pack declares, the block is writable
by hand and stays that way: a one-keystroke path to outranking `computed` would
be used for exactly the reason that layer exists.

### 5.5 Where promote may run

The capture sidecar is at `<workspace>/.yolo/prism/`, which is a host directory —
so the **host** can read a jail's captures without any channel. The destinations,
however, are host-side files a jail cannot **write**. Stated precisely: the jail
*reads* the local pack as a staged `:ro` copy at `/ctx/packs/local/` like any other
pack ([`assemble.go`](../../internal/cli/run/assemble.go)), and a filtered
`config.jsonc` snapshot also crosses
([`inheritscope.go`](../../internal/cli/run/inheritscope.go)) — the host's
`config.lua` crossed here too until the Lua transform was removed on 2026-09-11
([`OQ-LT1`](../reference/pack-system.md#oq-lt1)); what no jail can do is write
anything under the host's `~/.config/yolo-jail/`, which is the boundary promote
needs — a manifest is an input to composition, and an agent that could rewrite one
in-jail could grant its own pack a host file on the next boot.

**Promote is therefore a host-side verb**, and in-jail it refuses and prints the
host command — the mirror image of `refuseHostSideWrite`, which refuses host-side
`capture`/`reset` and points into the jail.

**A jail-side request channel is designed and deliberately unbuilt.** An agent
that has just made a good config change is exactly who should promote it, and the
transport is free: the host already reads `<workspace>/.yolo/` on every launch, so
a request would be one more file there, in the shape the `.yolo/handover.md`
host→jail handoff established (there is no jail→host handoff today of any kind —
[`prepare.go`](../../internal/cli/run/prepare.go) is host→jail
only, verified 2026-09-11). What is *not* free is the consent surface: a request is
written by an agent and read by a human, pointed at the user's home, so the prompt
has to say something specific. It waits until promote has been used enough to know
which keys people actually promote.

### 5.6 Degenerate inputs and failure paths

| Situation | Behavior |
|---|---|
| no captures for the surface | no-op, exit 0, says so |
| every captured key is redundant | no-op, exit 0, naming them |
| `--keys` names a key not in the capture | error, exit 2, naming it |
| destination pack is fetched (not `file://`) | refused, naming the pack and why |
| destination pack has no `pack.json` | one is created with the pack's name |
| destination already declares the key | deep-merged; the promoted value wins; the overwrite is reported |
| two workspaces captured the same key with different values | promote is per-workspace and one-at-a-time; the second promotion reports the overwrite of the first and requires confirmation |
| write succeeds, reset fails | whole promotion abandoned, destination restored, non-zero exit |
| surface is `rmw`, `computed` or `unrendered` | refused: those modes have no capture overlay |

**Concurrency.** Two jails can hold captures of one key simultaneously, but
promotion runs on the host and one at a time; the destination write is
last-writer-wins with the collision *reported*, never silent. Nothing in a jail
writes a promotion destination, so the cross-boundary race cannot arise.

**One writer, named.** `~/.config/yolo-jail/local/pack.json` is written by the
human and by `yolo config promote`. Never by a jail, never by a launch, never by
`yolo host apply` (which only reads packs).

### 5.7 Forbidden behavior

Promotion must never: write a fetched pack; promote a key classified sensitive
without an explicit per-key `--force`; promote a key that would lose precedence
at the destination; promote automatically as a side effect of a launch, an apply,
or a boot; or leave a key both declared and captured.

`--force` and `--accept-promotion` are different acts and stay different flags:
`--force` overrides a *refusal*, `--accept-promotion` confirms an *intended*
write. Collapsing the two is how `--force` becomes the flag people paste without
reading.

---

## 6. The host as a notch like any other

### 6.1 Mode parity

Under `host_management: own`, the host notch renders `stateful` — the jail's own
mode — and everything that follows from it follows here: whole-file composition,
a capture overlay for edits made between applies, `yolo config diff` and `reset`
working host-side without `--force`, and `--revert` becoming trivial because the
file is derived.

Under `assert` the host stays `rmw` and today's behavior is unchanged. Under
`none` the host renders nothing.

> [!IMPORTANT]
> **The census loses an entry, and `own` does not inherit what it held**
> ([§4.5](#45-retiring-assert--the-two-value-key)). Retiring `assert` deletes
> `render.HostAssertModes` — with it the **coercion**, which is the only thing that made a
> surface declaring `stateful` or `computed` render through `rmw` at a real home. What it does
> **not** delete is `rmw` at the host: `HostOwnedModes` runs *and records* it for a surface
> whose pack declared it, on the reasoning its own comment gives, so the notch keeps two
> mechanisms rather than becoming `stateful`-only
> ([§4.5.2](#452-what-the-ruling-does-not-delete--the-mechanism-tally-corrected)). The census is
> where that distinction is visible at all, which is the paragraph below making its own case.

**Which mechanisms a notch runs is written down in the mode census, and `own` is
an edit to that statement.** The **mode census** — `render.ModeSet`, one entry
per notch in [`internal/render/modes.go`](../../internal/render/modes.go), asked
through `Target.Modes()` — is the per-notch table of which engine mechanisms
(`stateful`, `computed`, `rmw`, `unrendered`) a target actually runs and which of
those keep a provenance record. It is not the *declaration* a pack writes on a
surface: a surface declares one mode, and the census says what the notch does
with that declaration. `HostModes()` today says the host runs `rmw` alone, runs
it for surfaces declaring `stateful` or `computed` too, and records it — so
`own`'s first edit is to that function, and the capture store
[§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to) specifies is
what the added `stateful` then needs.

> [!WARNING]
> **Until this paragraph the document named neither the census nor its type**
> — zero occurrences of `census`, `ModeSet` or `Modes()` (grepped 2026-09-12) —
> while [§10](#10-what-i-would-build-in-order)'s `own` step depends on both. And
> the census was only **half wired** when that step was written: `Records()` had
> exactly one production consumer, the rmw writer's provenance gate
> ([`prism.go`](../../internal/entrypoint/prism.go)), while `Runs()`,
> `Excludes()` and `Undecided()` had none — and the host's *"every surface is
> read-modify-written"* was not enforced by the census at all: `hostrender.go`
> called the rmw writer unconditionally.
>
> **The two agreed, and that is the hazard rather than the reassurance.** An
> unconditional call goes on agreeing with itself whatever `HostModes()` is
> changed to say, so **`own` could have shipped with `Modes()` untouched, leaving
> `HostModes()` a false statement** about the notch it is the authority for —
> `stateful` in the census, `rmw` in the only code that renders. Wired on
> 2026-09-12 (`67b649ac`): the host entry asks `ModeSet.Mechanism` per surface
> and refuses in `Excludes()`'s own words, and `Mechanism` reads `Runs()` and
> `Undecided()`. The `own` step inherits that seam, and must keep it — its
> `stateful` arm is reached by the census answering `stateful`, never by a second
> `if` beside it.

### 6.2 Host capture, and the privacy ruling it has to answer to

Host capture is currently refused on the grounds that it would copy a credential
out of the real file into a **workspace** sidecar — which crosses into a jail and
plausibly into git. Under `own` that hazard does not arise in the same shape: the
host capture store sits beside the existing host provenance record, at
`<home>/.local/share/yolo-jail/host-capture/`, and never crosses a boundary. The
key that *exports* anything — promote — refuses sensitive keys independently
([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
The refusal stays under `none` and `assert`.

**What the store holds: the three capture files, per surface, and nothing else.**
They are the same three a jail keeps under `<workspace>/.yolo/prism/`, with the
same names — `<agent>-<name>.last_render`, `<agent>-<name>.overlay.json`,
`<agent>-<name>.selection.json` — because they are what `stateful` composition
needs and `own` is that composition at the host notch: the baseline a capture
diffs against, the captured edits themselves, and the recorded selection.

**The provenance record does not move, and it is not one of them.** It stays at
`<home>/.local/share/yolo-jail/host-provenance/<agent>-<name>.provenance`, where
`Target.ProvenanceDir()` already puts it
([`target.go`](../../internal/render/target.go)) and where `--revert`
already reads it ([§10](#10-what-i-would-build-in-order) step 3). Two directories
because they have two lifetimes: provenance is per-key attribution, written at
**every** host apply including under `assert`, and it is what `--revert` consumes;
capture is `own`-only state that a host-side `yolo config reset` is entitled to
delete. Folding the record into the capture store would make reverting an
`assert` home depend on a directory only `own` creates.

**The directory is RESOLVED, never hand-built.** `render.Target.SidecarDir()`
answers it for `KindHost` under `own`, and `""` under the other two contracts —
which keeps "an empty answer means this target keeps no capture state" true at
every notch. Both the writer and every reader ask it there rather than joining
the path themselves, and the three capture-file names moved onto the `Target`
beside `ProvenancePath` for the same reason. The precedent is `hostProvenancePath`,
which resolves through `render.Host(…).ProvenancePath` precisely
so one definition serves the entrypoint that writes and the CLI that reads. Its
docstring calls two hand-copied path builders — *"how the CLI's own prism\* twins
already work"* — *"a standing hazard"*
([`configls.go`](../../internal/cli/configls.go)), and a
hand-built capture-store path would be the next pair.

> [!WARNING]
> **`0600` is a file mode, and the store is a directory.** An earlier draft of
> this section put `0600` on the directory itself, which would make it unusable:
> the execute bit on a directory is the right to *resolve a name inside it*, so
> without it even the owner gets `EACCES` opening any file in the store, while
> `ls` still lists the names. Measured 2026-09-12 as an unprivileged uid — `open`
> failed `[Errno 13] Permission denied` at `0600` and succeeded at `0700`, with
> `listdir` working in both. **The store is `0700`, holding `0600` files.**

> [!IMPORTANT]
> **Host capture under `own` is not a convenience — it is what makes adoption
> non-destructive**, so it is a precondition of
> [§6.3](#63-the-one-asymmetry-that-survives-deletion) rather than an addition to
> it. Refusing host capture under `own` would leave the first owned render
> composing from declared layers alone, which is the empty-overlay seed that
> [§6.3.1](#631-adoption-is-capture-then-regenerate) records as a shipped
> data-loss bug. The two must land together.

> [!WARNING]
> **The credential hazard belongs to CAPTURE, not to the host notch — and the jail
> already has it.** The jail's overlay sits in `<workspace>/.yolo/prism/` at mode
> `0644`, in a directory yolo *assumes* is gitignored
> ([`target.go`](../../internal/render/target.go),
> [`prism.go`](../../internal/entrypoint/prism.go)) but never adds to any
> `.gitignore` — this repository's own `.gitignore` line 6 does it by hand — and it
> holds a captured API key today ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
> So the shipped asymmetry is inverted on its own axis: the notch whose sidecar
> *is* in a workspace and *can* reach git is the one that captures freely. By
> [P5](#1-the-verdict-and-the-principles-it-rests-on) the guard belongs to capture
> at every notch — a capture-time deny-list, a sidecar mode — and the `0600`
> proposed for the host store's **files** is a parity gap against the jail's
> `0644` until both move. Promote's own name-token list is not that guard: it
> refuses at *export*, and these bytes are already on disk before it runs
> ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
> Neither is this document's to rule; both are listed for the roadmap.

### 6.3 The one asymmetry that survives: deletion

Whole-file composition means yolo may **remove** a key from a real host file.
That is correct under `own` — a derived file contains what the definition says
and nothing else — and it is the single place where "the host is a real home"
genuinely changes the answer.

**Adoption, however, is not that case.** The first owned render reproduces every
key the file holds that yolo does not itself declare, and it does so *by
construction* rather than by a guard this design adds. Two edges to state, because
"byte-identical" is a claim with them: adoption also **adds** yolo's declared keys
the file lacked (`dropNullLeaves` strips the tombstones `mergeDiff` records for
them, [`staterender.go`](../../internal/agentcfg/staterender.go)) — which
a first `assert` apply adds too — and it drops the classes
[§6.3.2](#632-the-three-classes-adoption-does-not-cover) names, one of which is
**not** empty on a home already on `assert`.

### 6.3.1 Adoption is capture-then-regenerate

`ComposeStateful` treats "no trusted `last_render` for this surface" as a
**first migration** and, on that branch, seeds the overlay from the file it
finds: `residue = mergeDiff(pureRender, current)` (`ComposeStateful`'s docstring
in [`staterender.go`](../../internal/agentcfg/staterender.go) is the prose, its
first-migration branch the code). The next line of
the render is then `declared layers + that residue`, which reproduces the file's
undeclared keys exactly. Switching a host surface to `own` is precisely that
branch — there is no host `last_render` yet — so **capture happens before the
first owned write, and the write puts back what capture just took**.

This is not a hopeful reading of the mechanism. That branch exists *because*
seeding an empty overlay was a shipped data-loss bug (`copilot/config` collapsed
to `{"yolo": true}` and logged the user out); the comment above it is labelled
`B1 (⚠ DATA LOSS FIX)` ([`staterender.go`](../../internal/agentcfg/staterender.go)).
The engine's answer to "adopt a file I did not write" is already the one a
confirmation prompt would have been built to provide.

> [!WARNING]
> **Do not check this against the file header.** `staterender.go`'s package
> comment, `StatefulInputs.LastRenderPresent`, `StatefulOutput.OverlayJSON` and the
> `ComposeStateful` docstring all still describe the pre-B1 branch — *"seeds a
> truthful baseline with an empty overlay and skips capture"*, *"{} on a first
> migration"*, *"render = Compose(overlay=∅)"* — so a reader who checks the
> docstring finds the opposite of this section. The body at
> [`staterender.go`](../../internal/agentcfg/staterender.go) is
> the authority (noted 2026-09-11; the docstrings are listed for the roadmap).

**For a home already on `assert` the diff is empty for MOST keys for a second
reason — and not for all.** `rmw` re-asserts the declared keys on every apply, so
a key that collides with yolo's declarations does not survive under `assert`
either. But the two modes disagree about **granularity**, and the disagreement is
a loss path:

- `rmw` **deep-merges** a declared object — `applyRMWLayer` recurses *"so a
  sibling key the agent owns under the same parent survives"*
  ([`prism.go`](../../internal/entrypoint/prism.go)). A user's
  `permissions.ask` beside yolo's managed `permissions.defaultMode` lives on under
  `assert`. Only object-valued **`computed`** tables are replaced wholesale
  (`regenerateManagedTables`, [`prism.go`](../../internal/entrypoint/prism.go)).
- Adoption **used to** drop every top-level key the pure render holds as an
  object, whole. So at the first owned render `permissions.ask` — and every other
  leaf under `permissions`, `env` or any declared object that yolo does not itself
  pin — was **not adopted**, and the render omitted it.

> [!WARNING]
> **The coarse rule was a shipped defect, not only a design hole**, and it is
> recorded because the shape recurs. The blanket drop was coarser than
> `dropOverriddenKeys`, the steady-state rule three functions further down in the
> same file, which says a blanket top-level drop *"would be simpler and wrong — it
> would discard the agent's permission list on every boot"*. Two rules in one file
> disagreed, and adoption took the wrong one. It lost a leaf silently at
> `assert → own` and in **any jail that lost its `last_render`** — B1's own trigger
> — because that is the same branch, and nothing caught it: `FirstApply` is *false*
> once a provenance record exists
> ([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)), and
> `EntryLosses` is defined over table entries.

**The design therefore requires adoption to drop at the granularity `rmw` writes
at**: leaf-level against deep-merged owners (`managed`, `defaults`,
`config-overlay`), wholesale only against `computed` tables — which is exactly
`dropOverriddenKeys`' existing rule applied to the pure render.

**That landed.** Adoption now narrows its residue in two passes at two
granularities: `dropComputedTables` wholesale against the computed layer, then the
shared leaf-level `narrowOverlay` both branches run. A leaf under a deep-merged
owner survives the switch, measured as a byte golden in
`internal/entrypoint/hostassertbaseline_test.go` and its `own` twin. (Neither golden
carries a `computed` layer, so neither moved for what follows.)

#### The drop narrows again — wholesale-against-`computed` is withdrawn as the general rule

**RULED 2026-09-20.** The sentence above stops at "wholesale against the computed
layer", and that is one word too broad. **The drop is justified only by the leaves yolo
actually REGENERATED**, so a computed table that regenerated none may claim none.
`dropComputedTables` now skips a key whose computed value is an **empty** object, and
takes the key whole only for a **non-empty** one.

The live shape is `mise` on a jail with no `YOLO_MISE_TOOLS` pin: the render emits a
`[tools]` table so the `last_render` sidecar stays non-empty and trusted, which made the
table **present while asserting nothing** — and `mise use -g neovim` went with it on the
first prism boot. That was recorded as an accepted cost of the
wholesale drop (`TestMisePrismUserGlobalToolDroppedThenPreserved`) and is withdrawn as one; the same
fixture now pins the opposite verdict under a name that says so.

> [!WARNING]
> **The [§4.1](#41-the-key) scrub loses its no-pin half, deliberately.** `mise`'s migration guarantee —
> an old yolo-written `node`/`python`/`go` line no longer shadows the baked `/bin/<tool>`
> — was delivered by exactly this drop. It still runs for a jail that pins *something*
> (the unpinned sibling is dropped, `TestMisePrismInjectedPinLands`), and no longer runs
> for a jail that pins nothing, because there the two readings — yolo's stale output, and
> `mise use -g node@22` typed by hand — are the same bytes with nothing to tell them
> apart. Getting it back needs a record of what yolo **wrote**, not the shape of what it
> computes, and the first migration is defined by that record being absent.

**The general rule does not survive the narrowing, and one case is why.** [§6.3.2](#632-the-three-classes-adoption-does-not-cover)'s row 1
justified the wholesale drop as *"right for a table yolo regenerates in full"*. A derive
does not always regenerate such a table in full, and **claude's `env` is the case that
cannot be argued back**: yolo asserts `ENABLE_LSP_TOOL` and can never own the rest of a
user's environment, so "fills it in full" is false there **by construction**, not by
accident. `enabledPlugins` is the same shape and the cheaper example; `mcpServers` is the
genuine fill-in-full table the rule was written for. One rule cannot serve both, and the
premise that it could has expired.

**What narrowing the rest needs, and why it is not here** (MEASURED 2026-09-20,
[`CO13`](#13-decision-ledger)). The obvious fix — drop the leaves the computed table
names, keep the rest — was tried and **reddens six tests across two packages**
(`TestComposeStatefulFirstMigrationDropsComputedTableWholesale`, three `mise` tests,
codex's and opencode's first-migration tests): a stale MCP server is precisely a leaf the
derive does not name, so leaf-narrowing alone resurrects it and breaks
[§2](#2-what-exists-today-stated-precisely) principle 1. The signal splits in two, and
only one half exists:

- **Which leaves the derive asserted** is already in the computed layer, exactly. A
  `ctx.tombstone` decodes to a PRESENT key with a nil value, so the shipped
  `claude/settings` derive hands the engine `enabledPlugins: {<3 ids>: nil}` — three
  asserted leaves, not the empty table it reads like. The table's key set **is** the
  asserted set.
- **Whether yolo fills the table in full** is the half the bet needs, and nothing carries
  it. Every candidate discriminator was tried and each flips on CONFIGURATION rather than
  on intent: the key set (above), the presence of a tombstone (`env` carries none once an
  LSP is configured), value shape (`mise`'s `[tools]` holds scalars and codex's
  `mcp_servers` holds objects, and both are fill-in-full), and whether a lower layer also
  contributes (true only when the user happens to have a host file).

So the remaining half has to be **declared by the derive that knows it**. Until it was,
the drop stayed wholesale for a non-empty table, and a test pinned that cost in the suite
rather than leaving it in prose.

**Built 2026-09-25 ([`CO13`](#built-2026-09-25--what-shipped)).** The derive declares it:
`ctx.in_full(t)` wraps a table it regenerates in full, and `dropComputedTables` takes a
non-empty table whole only when it is declared. One not declared in full is left to the leaf
pass, which removes exactly the leaves the computed layer names. The pinned test was inverted
rather than deleted — it is now
`TestComposeStatefulFirstMigrationKeepsTheUnassertedLeavesOfAPartlyAssertedTable` — and the
six tests the leaf-narrowing attempt reddened stay green: the engine fixture among them now
declares its table, and the `mise`, codex and opencode tables the other five cover are declared
at their source.

> [!NOTE]
> **One defect this ruling did NOT fix, because it was never this mechanism's**, and it is
> the one that surfaced the ruling. Measured on a live jail 2026-09-20: the host
> `~/.claude/settings.json` enabled `pyright-lsp` and `gopls-lsp`, and the jail's composed
> copy held `enabledPlugins: {}`. That was [`packs/claude/derive.lua`](../../packs/claude/derive.lua)
> emitting a **tombstone** for every plugin id whose LSP is unconfigured — and a tombstone
> is an RFC-7386 delete aimed at the layers BELOW, the lowest of which is the user's own
> host file. "Remove a stale enable" removed the user's deliberate enable, on every boot,
> through the fold rather than through adoption. Nothing was owed in its place: the surface
> is RECOMPOSED from layers every boot, so a toggle the derive stops asserting stops being
> written without any delete. The tombstones are gone, and
> `TestConfigureClaudePrismKeepsHostEnabledPluginsWithNoLSP` pins it with a host layer —
> the only fixture shape that can tell the fix from the defect, since a render with no host
> file is byte-identical either way.

**It is not the whole of [§11](#11-success-criteria)'s criterion, and the rest is
recorded there** rather than here: the leaf case passes, the two reformatting axes
are conformant under the criterion [`OQ-CO12`](#13-decision-ledger) ruled, and the
two key DELETIONS beside them were fixed as bugs on 2026-09-12 — one by teaching
`dropNullLeaves` to keep an object the user wrote empty, one by carrying a literal
`null` outside the layer stack, which is the only place a merge patch leaves for
it.

### 6.3.2 The three classes adoption does not cover

Stated because "empty by construction" is a claim with edges, and each edge is a
deliberate line in the engine rather than an oversight:

| Class | What happens | Why |
| :--- | :--- | :--- |
| A key nested inside a top-level object the **`computed`** layer holds as a **non-empty** table its derive **declares it regenerates in full** (`ctx.in_full`) — `mcpServers`, `mise`'s `[tools]` with a pin | **not adopted** | `dropComputedTables`. Right for a table yolo regenerates in full, where adopting would resurrect a dropped entry and break *regenerate, don't reconcile*. A table **NOT DECLARED IN FULL** — claude's `env` with an LSP configured — is no longer in this row (built 2026-09-25, [`CO13`](#built-2026-09-25--what-shipped)): it claims only the leaves it names, and the rest of the file's table is adopted. An **EMPTY** computed table takes nothing, declared or not: it regenerated no leaf, so it can claim none |
| A key a higher layer re-asserts — `managed`, or the per-boot `computed` layer | **never captured, in either branch** | `narrowOverlay` — both fold above the capture overlay and win unconditionally, so a captured copy could only sit in the sidecar as noise `yolo config diff` would report as a phantom edit |
| A **keyless** surface (`raw`, `lines`) | **not adopted at all** | one "key" is the whole file, so adoption would mean "the file wins outright", freezing a host-mirrored file at stale content forever ([`staterender.go`](../../internal/agentcfg/staterender.go)) |

Three things about that table are worth having in front of you before designing
against it.

> [!WARNING]
> **Row 1's justification had a false premise, and it is kept in the table with the
> correction beside it rather than rewritten away.** *"A table yolo regenerates in full"*
> was read as a property of the `computed` layer's SHAPE, and it is a property of the
> DERIVE's intent — which the shape does not carry. claude's `env` is the case that
> settles it: yolo asserts `ENABLE_LSP_TOOL` and cannot own a user's environment, so no
> future engine change makes "regenerates it in full" true there. Reaching the right
> answer for both `env` and `mcpServers` needs a declaration
> ([`CO13`](#13-decision-ledger)); until then this row was a cost, not a rule. **The
> declaration is built (2026-09-25)**, so the row now names only declared tables and is the
> rule it was written as.
>
> ⚠ **The narrowing does not license reconciliation.** *Regenerate, don't reconcile*
> ([§2](#2-what-exists-today-stated-precisely) principle 1) is unchanged: what the ruling
> withdrew is the claim that an object-valued computed key PROVES the file's contents
> under it are yolo's own previous output. Where yolo did regenerate the table, it is
> still taken whole.

**Row 1's deep-merged half is the shipped defect**
[§6.3.1](#631-adoption-is-capture-then-regenerate) names, and `confirmHostLosses`
gates rows 1 and 2 **only on a first apply** (`FirstApply && EntryLosses`,
[§4.4](#44-what-the-key-does-not-do)) — so on a home already on `assert` the prompt
does not fire at the exact transition that loses the leaf. The fix is to narrow the
drop so there is nothing for a guard to catch; behind it the archive
([§6.3.3](#633-what-survives-as-a-guard)) is the only net, and it is what the transition
gets instead of a prompt. **The narrowing is built (2026-09-25,
[`CO13`](#built-2026-09-25--what-shipped))** for every table whose derive does not declare it
regenerated in full; a declared table still takes the deep-merged leaves under it, by design,
and the archive is still what nets that.

> [!NOTE]
> **Row 2's rule is not adoption-scoped, and the competing semantic was rejected
> for a reason worth keeping.** `narrowOverlay` runs once, on the ACCUMULATED
> overlay, for both branches and both layers, which makes the store
> **self-healing**: a sidecar an older yolo dirtied is canonicalized on the next
> boot rather than staying dirty forever. It is LEAF-level per layer, because both
> layers deep-merge and share their objects with the user (mise's computed
> `[tools]` vs. a user-added global tool; managed `permissions.defaultMode` vs.
> Claude's own `permissions.ask`), and it keeps a null tombstone under an
> object-valued owner because what that erases is a lower layer this rule cannot
> see. The rejected alternative — retain the capture so it activates if the layer
> later stops supplying the key — loses because the pending edit is invisible and
> can sit for months: it would silently restore a stale `permissions` grant, or
> resurrect an MCP server the user had deleted. See
> [§5.1 of the composition plan](../plans/agent-settings-composition.md#51-the-store-holds-only-edits-that-can-win).

**Row 3 — the keyless case — is the honest gap, and three facts bound it.**

1. **There is no `rmw` for a keyless surface to stay in.** `rmwCodecRefusal`
   ([`surfacecodec.go`](../../internal/entrypoint/surfacecodec.go)) refuses keyless codecs **by
   kind**: *"RMW asserts and fills individual keys, so it needs an object […] Those
   surfaces belong in `stateful`/`computed`."* So `assert` is the mode a keyless
   surface cannot have, and the rule is already uniform across notches — a keyless
   surface renders `stateful` or `computed` everywhere, and adoption never applies
   to it anywhere.
2. **Adopting one is strictly worse than overwriting it.** Adoption makes **the
   whole file** the overlay, and the overlay outranks every declared layer beneath
   it — so the file would freeze at its adopted content and yolo's declarations
   would never take effect again ([`staterender.go`](../../internal/agentcfg/staterender.go) says exactly this about the
   jail, and relocating it to the host does not repair it).
3. **The class is empty today and only a pack can populate it.** `host_management`
   governs the **host notch**, and `RenderHostPack` walks a pack's *own* surfaces
   (`internal/entrypoint/hostrender.go`). A user's `host_files` entries are not
   pack surfaces — they are a user config key rendered **in the jail**
   (`internal/entrypoint/hostfiles.go`), yolo never writes the host's copy, and so
   there is no host-notch ownership question for them at all. `raw` is reachable
   (it is the default codec for any `host_files` entry whose destination is not
   `.json`/`.toml`, `hostFileCodecFor`, `internal/config/hostfiles.go`),
   but jail-side delivery is its job. No shipped pack declares a `raw`/`lines`
   surface.

What to do about row 3 is [OQ-CO9](#13-decision-ledger).

### 6.3.3 What survives as a guard

> [!NOTE]
> **BUILT 2026-09-12.** One call in the shared stateful writer
> ([`entrypoint.archiveAdoption`](../../internal/entrypoint/adoptionarchive.go), from
> `persistStatefulSurface`) serves both notches, so the host's `own` adoption and a jail's
> `firstMigration` cannot end up with different nets — or with one of them silently missing,
> which is how the gap survived the sprint that ruled it. The paragraphs below are the design;
> the four sub-headings after them are what the build had to decide and did.

**One archive at adoption.** The pre-existing file is copied once to the state dir
before the first owned render — and, by [P5](#1-the-verdict-and-the-principles-it-rests-on),
at a jail's `firstMigration` too, into the workspace's own `.yolo/` tree. Not
per-apply snapshots: those are a different feature with a retention policy. The
later-regression case — a bad pack update deleting a key months afterwards — is
what having the pack in git is for, which is the whole arrangement `own` is
recommending.

**It is cheaper than it looks, and it covers a known loss rather than a
speculative one.** The archive root and `yolo prune`'s sweep of it already ship for skills,
files and briefings (`~/.local/share/yolo-jail/archive/<bucket>/`,
[`applyhostskills.go`](../../internal/cli/applyhostskills.go)); a config
bucket is a new *bucket*, not a new *surface*. And the loss it covers is
[§6.3.1](#631-adoption-is-capture-then-regenerate)'s deep-merged leaf, which the
prompt is structurally blind to.

#### Where each copy lands

`render.Target.ArchivePath` resolves it, beside `SidecarDir` and `ProvenanceDir`, so the two
notches have one definition rather than two hand-copied joins:

| Notch | Path |
| :--- | :--- |
| host (`own`) | `<home>/.local/share/yolo-jail/archive/config/<agent>-<name>/<basename>` |
| jail / preview | `<workspace>/.yolo/archive/config/<agent>-<name>/<basename>` |

The jail's anchor is the **workspace**, which is the same anchor the capture sidecars already
use — and that is why this is not the problem the deleted `lspSentinelExpr` (formerly in
`internal/entrypoint/shell.go`, removed with the LSP install recipes on 2026-09-25) solved. That
function needed two spellings because the per-workspace file it named, the
`~/.yolo-installed-lsps` sentinel, lived inside the **home**, which podman reaches by a bind mount
and `macos-user` cannot reach at all (one account home, every workspace). A path in the workspace is one both backends name directly — `Env.WorkspaceDir`
honors `YOLO_WORKSPACE` and `macos-user` passes the real path. One anchor, no backend switch.

> [!WARNING]
> The jail's copy inherits the capture overlay's exposure, because it sits in the same tree:
> `<workspace>/.yolo/` crosses into a container and plausibly into git. That is the exposure
> [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to) already accepts for
> the overlay at this notch and refuses at the host, where the archive goes under the state
> dir instead. The shipped PACK surface that would have carried a credential into it —
> `copilot/config` — is `rmw`, which adopts nothing and is therefore archived never.
> ⚠ **The surface that does reach it is a `host_files` entry in `mode: capture`**, which
> renders stateful under `Agent: user`, `Name: <slug>`
> ([`hostfiles.go`](../../internal/entrypoint/hostfiles.go)) — so a credential file the USER
> declared is copied whole into `<workspace>/.yolo/archive/config/user-<slug>/`. Measured
> rather than argued: the same surface's own `last_render` sidecar already holds the same
> content in full, in the same tree, on every boot. The archive adds a copy inside an exposure
> this notch already accepts; it adds no new one.

#### Keyed by surface, not by stamp — and why prune must not reclaim it

The other buckets are `archive/<bucket>/<stamp>/`, and `yolo prune` keeps the newest three
generations in each. **This bucket deliberately has no stamp layer.** Those buckets hold
content yolo REPLACED and can regenerate, so keep-newest-N is right for them: what a user
wants back is "the last few applies". This holds the user's own pre-yolo file, which nothing
regenerates, and there is exactly one per surface ever. Under the stamped layout a home that
adopted a dozen surfaces on a dozen days would have nine originals swept — by yolo's own
reaper, for a retention policy nobody chose, out of the one directory that exists to prevent
exactly that loss. Prune needs no exemption for this: its own rule is that a directory whose
name it cannot parse as a stamp is left alone, so the layout is already covered
([`PruneHostArchive`](../../internal/prune/hostarchive.go)).

#### One archive — one per what

**One per surface, per home, for the life of that home**, and what makes it idempotent is the
archive's own existence: the writer copies only when the destination path does not exist. No
sentinel, no record to keep in sync. A second adoption of the same surface is reachable —
`yolo config reset`, a deleted or corrupt `last_render`, a restored workspace — and the
decisive reason not to re-archive is not tidiness: by then the file on disk is **yolo's own
output**, so a second copy would overwrite the user's original with the very thing it exists
to be compared against. The net would perform the deletion it exists to prevent. A
steady-state render archives nothing at all, which is what keeps this true across every boot
of a jail rather than only across host applies.

#### When the copy cannot be made, and the degenerate inputs

**A failed archive REFUSES the adoption; the file is left exactly as the agent wrote it.** The
alternative — warn and adopt anyway — is a net that can silently not exist, which is worth
less than one that says so: it takes the one-way door without the thing that makes it
survivable and reports a successful render. Refusing is also what the surrounding code already
does with comparable failures (every other write in the adopting path returns its error) and
what the host dispatch already knows how to present: a per-surface `refused:` line, the file
untouched, the remaining surfaces still rendered. Nothing is persisted, so the next render
still sees a first migration and retries the whole adoption from the same file.

The degenerate inputs, each with the reason rather than just the answer:

| The pre-existing file is… | Archived? | Why |
| :--- | :--- | :--- |
| absent | no | Nothing to lose. This is every surface of every fresh jail home, and it is what keeps the archive from existing at all for most surfaces |
| empty | no | Restoring "absent" and restoring "zero bytes" leave the user in the same place; a zero-byte archive is not a net, it is a directory entry saying yolo ran |
| unparseable | **yes** | The sharpest case, and the reason the gate keys on BYTES rather than on the file parsing. At the host `own` refuses such a surface outright ([`OQ-CO9`](#13-decision-ledger)); at a jail it does not — adoption skips capture and the render replaces the file wholesale, with an empty residue, so the archive is the only record the file ever existed |
| present but **unreadable** | n/a — **the render is refused** | EACCES, EIO, EISDIR and a symlink loop yield no bytes, so this used to fall into the `absent` row and take its answer: no archive, and a file that EXISTS replaced wholesale. `composeStatefulSurface` now fails closed on any read error but absence, in `decodeSurfaceObject`'s own words (*"cannot read … the file is left untouched"*), so nothing is archived because nothing is adopted |
| yolo's own output, byte for byte | no | There is nothing of the user's in it to lose. The comparison is against the LAYERS-ALONE render (`agentcfg.StatefulOutput.PureBytes`), which never consulted the file — not against the composed result, which would make the net conditional on adoption being right |

> [!NOTE]
> **The last two rows are what the gate could not see until 2026-09-12, and each cost the net a
> different way.** "Present but unreadable" was not a row at all: it fell into `absent`, because
> `current, _ := os.ReadFile(surfacePath)` in `composeStatefulSurface`
> ([`prism.go`](../../internal/entrypoint/prism.go)) discarded the error, so the `unparseable`
> row's total loss was one errno away and unnetted. "Yolo's own output" was not a row either: it
> fell into the bytes-present case and SPENT the one-per-surface slot, announcing a copy of the
> render as *"the file as yolo found it"*. The first is now a refusal at both notches (the host
> already had `decodeSurfaceObject`'s); the second is a gate condition plus the other half it
> needs — `yolo config reset` re-seeds the baseline it has just made true rather than deleting
> it, so the render after a reset is a steady state and never reaches the gate
> ([`cli.reseedResetBaseline`](../../internal/cli/configdiff.go)). Ruling 1's two halves are
> both still there; what changed is the spelling of the third.

#### What the net does not reach — live residue, found after it shipped

> [!CAUTION]
> **A ✅ over a known hole is the exact failure this ruling is the case study for**, so what the
> post-build verification found is recorded here rather than in a commit message. **Four in
> total, three of them now closed:** the two the GATE could not see are fixed and stated with the
> degenerate inputs above (an unreadable file, and yolo's own output), and the third was the
> REPORT rather than the net — a run that adopted a file still ended on *"nothing to apply"*.
> The one below is not. It is not a regression — it is a loss that predates the archive and that
> the archive does not reach — and it is not fixed, because every candidate fix reverses
> something ruled above. It is the maintainer's to rule, not the next builder's to assume.

> [!NOTE]
> **Closed 2026-09-12, and recorded rather than deleted because the shape is the lesson.** The
> gate could not tell *"there are bytes here"* from *"these bytes are mine"*, so any adoption
> over a file yolo itself had written spent the one-per-surface slot on a copy of the render and
> announced it as *"the file as yolo found it"* — leaving the next GENUINE adoption with
> `Archived` empty and no net at all. `yolo config reset` was the reachable route in: it removes
> both sidecars AND truncates the surface to its pure render, so the next render saw no
> `last_render` (a `FirstMigration`) over a non-empty file (bytes to archive). Measured
> 2026-09-12 through `RenderHostPack` under `own`, at both notches, since `refuseHostSideWrite`
> permits `reset` host-side under `own`.
>
> The fix is two halves, because neither reaches the other's case. The gate gained the
> layers-alone comparison in the table above — which is **not** the *"the render changes no
> bytes"* predicate [`archiveAdoption`](../../internal/entrypoint/adoptionarchive.go) refuses by
> name, since that one compares against the COMPOSED result and so trusts adoption to be right.
> And `reset` now re-seeds the baseline for the bytes it has just written instead of deleting it
> ([`cli.reseedResetBaseline`](../../internal/cli/configdiff.go)), which is what the deletion was
> already reaching for — its own output already calls the outcome *"(baseline re-seeded)"* —
> performed by the half that knows the bytes. Reset is the half the gate cannot cover: its truncation composes WITHOUT the
> computed layer, so its output is a subset of the comparison rather than equal to it.
>
> What it costs: the render after a reset is a steady state, not a first migration, so the
> one-time orphan retirement gated on that signal
> (`RetireOnFirstRender`, [`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)) does
> not fire on it. That sweep is keyed to a surface's FIRST render into a home, which a reset is
> not.

**A lost overlay sidecar is a silent, unnetted, unannounced loss — and the gate's first
condition used to say the opposite.** `archiveAdoption` argued that a steady-state render
*"has a trusted baseline … and loses nothing this could net"*; that sentence is GONE (the
docstring states the boundary instead), but the loss it denied is still here. Measured
2026-09-12 at the host notch: on an adopted home, deleting ONLY `<agent>-<name>.overlay.json` and
keeping `last_render` leaves the next render in steady state — the delta against `last_render` is
empty because nothing moved on disk, and the absent overlay reads as empty — so the render
collapses to the pure layers and both `apiKeyHelper` and `permissions.ask` disappear.
`FirstMigration` is false, so no archive; `Overwrites` and `EntryLosses` are both empty, so
no loss line and no prompt. A surface whose keys arrived AFTER a fresh-home first render has
no earlier archive to fall back on, and the loss is total.

*Why it is not just fixed:* widening the trigger past `FirstMigration` — or treating a
missing or corrupt overlay as an adoption event of its own — changes which renders adopt at
all, which is scope [`OQ-CO7`](#13-decision-ledger) did not grant; and a one-per-surface slot
is the wrong shape for a sidecar that can be lost more than once, which is what makes this a
different feature rather than a wider gate. One fact for whoever rules it: the state is
DETECTABLE without widening anything — no render leaves a `last_render` without an overlay
beside it (`persistStatefulSurface` writes both, `{}` included), so "baseline present,
overlay absent" is an anomaly yolo could report even if it cannot net it.

> [!NOTE]
> **Closed 2026-09-12 — and by leaving alone the thing this entry said could not be moved.** A run
> that adopted a file printed the surface's own line, printed the unsuppressible archive
> disclosure under it, and then closed on *"Nothing to apply — this home is up to date"*: two
> surfaces disagreeing about one run, which is the defect
> [`../reference/report-tiers.md`](../reference/report-tiers.md) exists to prevent, over the one line
> [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) forbids hiding. Measured on the BUILT binary —
> the baked `yolo` on PATH is many commits behind and shows the old report.
>
> **The obstacle recorded here was real and is untouched.** `hostApplySurvey.InSync` still counts
> an adopting destination, `Changes()` still means exactly what it meant, and the
> `host_apply_on_launch` gate therefore still stops for the homes it stopped for before
> ([`hostapplygate.go`](../../internal/cli/hostapplygate.go) reads `Changes()` and nothing else) —
> which is also [`../reference/report-tiers.md`](../reference/report-tiers.md#what-this-does-not-license)'s rule that
> the survey grows fields rather than changing what `Changes()` means. What closed it is a count
> the gate does not read: `hostApplySurvey.Adoptions()`, marked from `HostRenderResult.Archived`
> in `noteConfig`, which makes an adoption a class of its own in
> [the verdict block](../reference/report-tiers.md#the-verdict-block). The run now ends on
> *"Applied: 1 surface adopted (archived first)"*, and a home with nothing to adopt still ends on
> *"Nothing to apply"*.
>
> The lesson is the same shape as the one above it: a TIER decides how a destination's own line
> renders, and only a COUNT reaches the sentence the run ends on. `configResultTier` gave the
> archive disclosure its heading back that morning and left the verdict under it saying the
> opposite, because the two are different mechanisms and the fix for one is not the fix for the
> other.

**And one latent defect, one refusal away from real.** `RenderHostPack`'s refusal branch builds
its `HostRenderResult` without `Archived`, so a surface refused AFTER a successful archive would
never announce the copy. Unreachable today — the only refusal inside `persistStatefulSurface` is
`archiveRefusal` itself, raised BEFORE `r.archived` is set, and every later failure is a plain
error that becomes a pack-level failure — but `renderSurfaceStatefulDetail` promises a caller can
read a render back *"even on a refusal"*, and this caller discards it. A refusal added below the
archive ships a silent copy.

> [!WARNING]
> **The one-way-door gate cannot see the most destructive case available.**
> `confirmHostLosses` reads `EntryLosses`, defined as *"the NAMED-ENTRY casualties
> of this render: an entry in a table like `mcpServers`"*
> (`HostRenderResult.EntryLosses`, `internal/entrypoint/hostrender.go`). A **keyless surface has no tables
> and no named entries**, so `EntryLosses` is always empty for one,
> `confirmHostLosses` returns *"nothing would be lost — no prompt"*, and the render
> replaces the whole file **silently**. The gate is entry-shaped and a whole-file
> replacement is not an entry. It is a gap in the guard, not in the rule — and it
> is not host-only: a keyless surface at a jail's own `firstMigration` cannot adopt
> either, so the same whole-file overwrite is available there, against a home that
> may hold an agent's own state.

**And the net cannot be the same primitive at both notches, which
[P5](#1-the-verdict-and-the-principles-it-rests-on) permits.** "Confirm like an
`EntryLoss`" needs a human: the host apply is interactive and its stdin exists for
exactly that prompt ([`apply.go`](../../internal/cli/apply.go)), but a
jail's `firstMigration` runs at an unattended boot — in-jail `yolo apply` is a
report, not a provision ([`apply.go`](../../internal/cli/apply.go)). So
the RULE is uniform and the NET splits by the primitive available: a prompt where
there is a TTY, the archive where there is not. That is what makes the archive
load-bearing rather than optional.

**One load-bearing dependency, named because it is easy to drop.** Adoption is
safe against `yolo config reset` *only* because reset also truncates the surface
to its pure render — without that, reset → no baseline → adopt would resurrect
the very edits the user asked to discard, making reset a no-op. The engine
comment says the two halves are one change. Host-side `reset` refused when this was written
([§2.4](#24-the-verbs-that-exist-and-the-ones-that-do-not)), so **`own` had to
make host-side `reset` work, not merely permit it** — [§6.1](#61-mode-parity)
lists it as parity, and it is a correctness requirement. Built 2026-09-12:
`refuseHostSideWrite` exempts `reset` when the target is host-owned
([`configdiff.go`](../../internal/cli/configdiff.go)).

After adoption, a removal is ordinary and reported, exactly as it is in a jail.

---

## 7. What this does not propose

- **No change to the jail's precedence stack.** `defaults → host → workspace →
  config-overlay → capture → computed → managed` stays as it is (the Lua
  `transform` layer this line also named was removed on 2026-09-11, by
  [`OQ-LT1`](../reference/pack-system.md#oq-lt1) rather than by anything
  here).
  [§5.4](#54-promotion-moves-a-key-down-the-stack) works *around* it deliberately
  rather than reordering it.
- **No sealing-by-default.** Whether `apply --sealed` is a flag or the default is
  `Q1a` in [`environment-manager-user-stories.md`](environment-manager-user-stories.md#open-questions)
  and stays there. This doc only adds one more thing `--sealed` refuses.
- **Packs are not made mandatory.** `host_management: assert` — hand-edit the
  host file, no pack — remains fully supported and is the right answer for a
  single machine. ⚠ **The 2026-09-20 ruling withdraws this bullet**
  ([§4.5](#45-retiring-assert--the-two-value-key)): with `assert` retired, the no-pack answer is
  `none` — yolo writes nothing and the file stays entirely the user's to hand-edit — and the
  single machine that wants yolo's keys in it uses `own` plus `promote`. What is given up is
  the *shared* file, which is the thing the ruling calls fragile, not the hand edit.
- **No new secret model.** Sensitive keys are *refused* — no redaction, no vault,
  no taint propagation. The deny-list itself is new work
  ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
- **No `pack import` verb.** Promotion moves *captured keys* into a pack. But
  `own` + `promote` **compose into one** for every key nothing declares: adoption
  captures a hand-written file's undeclared keys into the host overlay
  ([§6.3.1](#631-adoption-is-capture-then-regenerate)), and promote lifts a
  captured key into the local pack — which is the migration env-manager plan
  [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
  assumed when it retired the host layer (*"author (or `yolo config promote` into)
  a one-file local pack"*). What stays manual is re-authoring a file *as* a pack,
  with structure and intent; what becomes mechanical is not losing its keys.
- **The `guest` notch is untouched.** It has no provenance dir and no sidecars
  today; giving it an ownership contract is a separate question.
- **No automatic promotion, ever** — see [§5.7](#57-forbidden-behavior).
- **`--to workspace` is out of scope** ([§5.1](#51-surface)).

---

## 8. Alternatives considered

**A. Leave it alone; document the asymmetry better.**
[`migrating-to-packs-and-host-management.md`](../../userguide/guides/migrating-to-packs.md)'s
2026-09-09 correction is most of this. **Rejected** — it addresses the confusion
and none of the missing capability: there is still no way out of capture except
discard, and still no revert.

**B. Make the host `stateful` unconditionally.** Simplest possible parity.
**Rejected** — it takes deletion authority over every user's real home without
asking, including users who never ran `yolo host apply` and never will.

**C. A boolean `manage_host: true|false`.** What the maintainer initially
suggested, and closest to the question actually being asked. **Rejected for the
three-value enum**, narrowly: the boolean cannot express the difference between
"assert my declared keys into a file that is mine" and "this file is yours
now" — which is precisely the ambiguity that produced this document. If `assert`
proves not to be a real population, this becomes right.

> [!IMPORTANT]
> **C's condition was met on 2026-09-20, by a different route than the one it named**
> ([§4.5](#45-retiring-assert--the-two-value-key)). It is not that `assert` proved to be an
> empty population — nobody measured that — but that the shared file it expresses proved to be
> the one contract where [P6 and P7 collide](#451-what-actually-carries-it-assert-is-the-one-contract-where-p6-and-p7-collide).
> The outcome is C's shape with C's spelling refused: **two values, keeping the
> `none`/`own` vocabulary** rather than a boolean, because the words say which way the
> ownership runs and `manage_host: false` does not distinguish "mine" from "unmanaged by
> anyone". Recording this is the point of the row — an alternative rejected *conditionally* is
> the cheapest thing in a design to re-check, and this one named its own condition.

**D. Infer ownership from whether a provenance record exists.** No new key: if
yolo has ever applied to this home, it owns it. **Rejected** — it is inference
again (P1), it silently escalates on first apply, and it makes an irreversible
decision out of a `--dry-run` slip.

**E. Promote by hand; document the local-pack recipe and stop.** Cheapest. **Not
rejected so much as insufficient** — it is what the maintainer would do today,
and the reason to build the verb is the classification and precedence checks in
[§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot) and
[§5.4](#54-promotion-moves-a-key-down-the-stack), which are exactly the parts a
human doing it by hand gets wrong silently.

**F. Make promotion the agent's job entirely — a skill, no verb.** **Rejected**
— the mechanical half (redundancy, precedence, path detection, the atomic
write-and-reset) is the half that must not be reimplemented per-agent, and an
agent with no `--plan --json` to read would be guessing at the same data the tool
already has.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| A user picks `own`, and yolo deletes settings they cared about | Adoption is capture-then-regenerate, so the first owned render reproduces the file's undeclared keys ([§6.3.1](#631-adoption-is-capture-then-regenerate)); the classes it does not cover are in [§6.3.2](#632-the-three-classes-adoption-does-not-cover), plus the one-time archive |
| A user on `assert` switches to `own` and silently loses a leaf under a managed object (`permissions.ask`) | Adoption must drop at `rmw`'s granularity ([§6.3.1](#631-adoption-is-capture-then-regenerate)) — **shipped 2026-09-12**, so the leaf survives. The fallback this row named is there too, **shipped the same day**: the one-time archive ([§6.3.3](#633-what-survives-as-a-guard)) holds the file as yolo found it, for any class adoption still drops — which matters precisely because `confirmHostLosses` is off once provenance exists. ⚠ It does not cover everything a reader of this row would assume: one gap remains recorded as live residue in the same section |
| Promotion silently demotes a key that then reverts | Precedence check is a **refusal**, not a warning ([§5.4](#54-promotion-moves-a-key-down-the-stack)) — and the class is empty for `--to local`; the check protects `--to pack:<name>` |
| A credential is promoted into a pack, and the pack is pushed — or already sits in a sidecar | Promote's deny-list ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot), new work); `--force` is per-key and named in output; the sidecar half is a capture-time question at every notch ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)), listed for the roadmap |
| The local pack becomes an unreviewable pile of promoted keys | Every promotion is an ordinary edit to a readable `pack.json`; `yolo pack lint` and `footprint` already report its claims |
| Three-value enum confuses users who wanted a switch | Explained in `yolo config-ref` and at the point of the act (`none` names the key that made it write nothing). Accepted: the value a confused user lands on is `assert`, which is what they already have. ⚠ **The mitigation inverts under [§4.5](#45-retiring-assert--the-two-value-key)**: with two values the confusion largely goes, and what a confused user lands on becomes `none` — yolo writing nothing, which is the safe landing but no longer "what they already have". That swap is [`OQ-CO14`](#oq-co14)'s face 2, stated here as a risk because this row is where a reader looks for it |
| A pack's derive returns a table it does not declare in full, and the jail and the host treat it differently | The host's table probe and the jail's `stateful` adoption both claim only declared tables (`CO13`, built 2026-09-25). The jail's `rmw` arm does not read the declaration yet, and no shipped pack exposes the gap; what it should do is [`OQ-CO15`](#oq-co15) |
| A user's own provider entry, added by hand to an agent's catalog, is dropped | The provider catalogs are declared in full, so a first-migration adoption and a host apply take them whole, as they did before `CO13`. The supported place for a personal provider is a user `providers` entry. Whether the catalogs should stay declared is [`OQ-CO16`](#oq-co16) |
| A jail-side agent cannot promote, so the workflow stalls where the work happens | The refusal names the host command; the request channel ([§5.5](#55-where-promote-may-run)) is designed for when there is evidence to shape its prompt |

---

## 10. What I would build, in order

1. **`host_management`, parsing and validation only** — the key, its user-scope
   read, its fail-closed direction, its `inherit.go` entry, and the `--sealed`
   refusal while the key is unset. Nothing changes behavior yet; the contract
   becomes expressible.
2. **Wire `none` and `assert`.** `none` makes `yolo host apply` refuse; `assert`
   is today's path. This is the whole key for everyone who does not want `own`,
   and it lands the ownership answer without touching the render engine.
3. **`--revert` under `assert`,** consuming the provenance record that already
   exists. Small, independently useful, and it proves the record is trustworthy
   before anything depends on it more heavily. ⚠ Reverses env-manager plan
   [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   ([§2.4](#24-the-verbs-that-exist-and-the-ones-that-do-not)); write the reversal
   into that ledger in the same commit.
4. **`yolo config promote --plan --json`** — classification and precedence
   checks, emitting the plan, writing nothing — including the sensitivity
   deny-list, which does not exist yet
   ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)). The
   dangerous half is the write; the valuable half is the analysis, and it can ship
   first.
5. **Promote's write path** to `local` and `pack:<name>`, with the atomic
   write-and-reset and the `--accept-promotion` confirmation.
   ⚠ **Written against today's binding, deliberately.** `--to host` is refused for
   a surface with no host layer ([`OQ-CO10`](#13-decision-ledger)), and the
   predicate promote asks is `Surface.HasHostLayer()` — which, until step 6, is
   `HostSource != ""`: a `/ctx` path populated across a seam by a basename match
   ([§5.1.1](#511-why-only-two-surfaces-have-a-host-layer)). Promote binds to the
   **predicate**, never to what currently populates it, so step 6 is invisible
   here. The two alternatives are both worse: waiting for the restructure blocks
   a self-contained verb behind a refactor, and reading the `reads-host`
   contribution directly would give the basename match a second caller to be
   removed from.
6. **The `reads-host` restructure, part 1 — the declaration moves onto the
   surface.** A config surface declares its own host layer as a boolean field;
   `HasHostLayer()` becomes that field, and the `/ctx` destination is derived
   from the surface's own path by ONE expression that the launcher (which mounts
   it) and the jail (which opens it) both evaluate, so there is no state where
   the declaration is present and the binding is not. The contribution kind
   **stays** — the user's `host_files` entries carry arbitrary host files with no
   mirrored twin and genuinely need a declared path — and a pack's `reads-host`
   naming its own surface is refused with the migration on the authoring and host
   paths only, because that is a version-skew fact and a jail must boot across
   the boundary ([§5.1.1](#511-why-only-two-surfaces-have-a-host-layer)).
7. **The `reads-host` restructure, part 2 — the read fails closed.** The launcher
   reports what it delivered, in `YOLO_HOST_LOOPBACK`'s shape and for its reasons:
   emitted on every launch, so an absent report means *"launcher older than the
   variable"* and never *"nothing was delivered"*; severity is the disposition's
   alone. The jail is then a **witness** rather than a second decider — a
   destination the launcher says it delivered and the jail cannot read is a
   refusal, a file the user simply does not have composes without the host layer,
   and a launch that carried no host bytes at all reports `unsupported` and is
   **not** refused for a delivery nobody attempted. This is what ends the fail-open
   read that already shipped a wrong composition and the `macos-user` silent drop
   ([§5.1.1](#511-why-only-two-surfaces-have-a-host-layer)); it is worth doing
   whether or not `own` ever is.
8. **`own`:** the host notch renders `stateful`, **with the host capture store
   ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)) and
   host-side `reset` in the same commit** — adoption is unsafe without either
   ([§6.3.3](#633-what-survives-as-a-guard)) — plus the one-time archive
   ([§6.3.3](#633-what-survives-as-a-guard)), the
   keyless carve-out [OQ-CO9](#13-decision-ledger) rules, and **adoption narrowed to `rmw`'s
   granularity** ([§6.3.1](#631-adoption-is-capture-then-regenerate)), without
   which `own` does not keep an `assert` home's keys and values. Last because it is the only
   step that can lose data, and by then promotion exists, which is what makes
   `own` attractive rather than merely strict. ⚠ Reverses env-manager plan
   [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   and un-moots [`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   ([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)); record
   both there.

Steps 1–3 are worth doing even if [§5](#5-promotion--the-way-out-of-capture) is
never built; step 4 is worth doing even if step 5 is not; and steps 6–7 fix two
live defects on their own, so they survive `own` being dropped entirely.

**Why the restructure is scheduled after the verb that needs it, rather than
before.** Step 5 is the step that asks "does this surface have a host layer?",
which is the question the basename match answers badly — so the intuitive order
puts steps 6–7 first. It is the wrong order twice over: promote is a new verb
that can be written against the predicate and re-verified for free once the
binding changes, while the restructure touches the boot render on every backend
and carries a skew refusal, so pulling it forward puts the riskier change ahead
of the one whose output tells you whether the design works at all.

---

## 11. Success criteria

Observable outcomes that mean this was built as designed:

- A user can answer "does yolo own my `~/.claude/settings.json`?" by reading one
  key, and every command that writes that file agrees with the answer.
- `yolo apply --sealed` refuses in an environment whose host-ownership contract
  is unstated.
- A key set interactively inside one jail can reach every other jail *and* the
  host, without the user hand-editing a `pack.json` and without discarding it.
- Promoting a key that would lose precedence at its destination fails, with the
  losing layer named — verified by promoting, `--to pack:<earlier>`, a key that a
  later-ordered pack's `config-overlay` also sets, and observing the refusal
  naming that pack. Not by promoting a `managed` key: one cannot be in the overlay
  at all, so `--keys permissions` fails
  [§5.6](#56-degenerate-inputs-and-failure-paths)'s *not in the capture* check
  instead.
- Switching to `own` on a home already applying under `assert` keeps **every key
  and every value** — the measurable form of
  [§6.3.1](#631-adoption-is-capture-then-regenerate). Switching on a home that
  never applied loses only what a first `assert` apply would have lost, prompts
  for it through the gate that already exists, and leaves the pre-existing file
  recoverable from the archive.

  **Keys-and-values invariance, stated once so the comparator is not re-invented
  per test.** Decode both files with the SURFACE'S OWN codec and compare the
  decoded values structurally: two objects are equal when they hold the same key
  set and equal values under each key, two arrays when they hold equal elements
  in the same order, scalars by value — and **a key present with a `null` value is
  a key, not an absence**. A number cannot differ by type across the comparison,
  because one codec decodes both sides. Everything the codec does not decode is
  outside the criterion *by construction*, and that is the whole content of the
  relaxation: byte layout, indentation, insertion order, and — a comment being
  neither a key nor a value — **TOML comments and yolo's generated header**. A
  keyless (`raw`/`lines`) surface has one value, the whole file, so the comparator
  degenerates to byte equality there; `own` refuses keyless surfaces
  ([`OQ-CO9`](#13-decision-ledger)), so the case cannot arise at the switch.

  > [!IMPORTANT]
  > **This criterion was BYTE invariance until [`OQ-CO12`](#13-decision-ledger)
  > relaxed it (2026-09-12).** Two of the four axes that relaxation was measured
  > against were NOT conformant — they were key deletions — and they were **fixed
  > as bugs on the same day** rather than absorbed. Keeping the table is the point:
  > a criterion that moves must not launder into conformance the defects that
  > prompted it.
  >
  > | Axis, measured 2026-09-12 | Under `assert` | Under `own`, then | Verdict | Now |
  > | :--- | :--- | :--- | :--- | :--- |
  > | A key valued `null`, at any depth | kept | **deleted** | **bug** — a key, and it was gone | kept |
  > | A key valued `{}`, at any depth | kept | **deleted** | **bug** — a key, and it was gone | kept |
  > | JSON key order, at every depth | the file's own order | sorted | conformant | sorted |
  > | A TOML surface's user comments | reattached in place | destroyed, and a three-line generated header prepended | conformant | unchanged |
  >
  > ⚠ **The two *"kept"* cells answer for these two axes, not for every key on the switch.** A
  > surface declaring a **non-empty** object-valued `computed` table still loses the user's own
  > leaves under that table at an adopting render: `dropComputedTables` drops such a table
  > WHOLESALE. Measured at HEAD on 2026-09-12 and named here because a reader who stops at this
  > table takes the switch for lossless, which it is only where no such table is declared.
  >
  > ⚠ **Narrowed 2026-09-20, but only at one end.** A computed table that is present and EMPTY
  > asserts no leaf and now takes none, so the loss above needs yolo to be regenerating something
  > under that key on that render. The non-empty case stands, and *"wholesale against `computed`"*
  > is no longer the granularity RULE it is stated as here — it is the residue of one
  > ([the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule),
  > [`CO13`](#13-decision-ledger)).
  >
  > ⚠ **Narrowed at the other end 2026-09-25 ([`CO13`](#built-2026-09-25--what-shipped)).** Only
  > a non-empty table its derive DECLARES regenerated in full (`ctx.in_full`) is still taken
  > wholesale; one not declared in full keeps the user's leaves and loses only the ones yolo
  > asserts.
  > So the loss above now needs a declared table — at the host notch, one of the MCP and LSP
  > tables `hostTableKeys` reports — where it is the rule rather than a residue.
  >
  > **Why the last two are conformant rather than tolerated.** A composing
  > renderer that sorts keys is the contract `own` *states* — the file is derived,
  > and a derivation has no insertion order to preserve. The alternative was to
  > teach `own`'s encoder rmw's byte layout, which would push formatting knowledge
  > into the capture path, a path that has no reason to know it. The two contracts
  > compose through **different encoders** and that is what they are for: `assert`
  > writes read-modify-write, preserving insertion order and reattaching comments
  > because *that* is its contract; `own` composes the whole file through the
  > surface's codec.
  >
  > ⚠ **The two deletions looked like one defect and were two**, which is worth
  > recording because the first reading — *"a `null` leaf is dropped by name on the
  > adoption path, and an empty object diffs to nothing, so neither reaches the
  > overlay"* — is right about `{}` and incomplete about `null`.
  >
  > - **`{}` really is one drop, in one function.** `dropNullLeaves` strips the
  >   RFC-7386 tombstones out of an adopted residue and drops any object its own
  >   recursion empties, so it could not tell an object the user WROTE empty from
  >   its own leavings. The two are distinguishable there without a heuristic: a
  >   patch never carries an empty object for any other reason, because `diffValue`
  >   copies an added key's subtree verbatim and records nothing at all for a key
  >   whose recursion found no difference. An already-empty object on the way IN is
  >   always the user's. Fixed by keeping it.
  > - **`null` has a SECOND deleter downstream, and it is structural.** Even
  >   carried into the overlay, a null is deleted by the fold: every layer merges
  >   through RFC 7386, where a null under a key DELETES that key — the semantics
  >   the capture overlay *needs*, so a deletion made in-jail survives a boot
  >   ([`config-migration-to-prism.md`](../reference/config-migration-to-prism.md#the-accumulation-step-preserves-tombstones)).
  >   Inside a merge patch the two meanings are one token. So the value that is not
  >   a patch is carried **outside** it: `Inputs.LiteralNulls`
  >   ([`literalnull.go`](../../internal/agentcfg/literalnull.go)) is a keypath
  >   skeleton read off the DECODED FILE — where a null is unambiguous, a decoded
  >   file holding no tombstones — and re-asserted after the fold, wherever no layer
  >   that OUTRANKS THE CAPTURE OVERLAY spoke for the path.
  >
  >   **Outside the stack is a statement about the CHANNEL, not about precedence**,
  >   and conflating the two shipped a second value-loss for one day (2026-09-12). A
  >   literal null folds at the capture overlay's OWN precedence, because that is
  >   what it is — a captured value, carried beside the stack only because no merge
  >   patch can spell one. `computed` and `managed` sit above the overlay and beat
  >   it: a `computed` tombstone removes a key on purpose, and putting it back as
  >   null would undo a decision yolo made that boot. `defaults`, `host`,
  >   `workspace` and every `config-overlay:<pack>` sit below it and LOSE to it,
  >   exactly as they lose to a non-null captured value. The rule read *"a layer
  >   always wins"* — every layer, at every precedence — which made the null the one
  >   captured value in this engine a LOWER layer could overwrite: a file holding
  >   `"theme": null` against a pack whose `defaults` says `"system"` keeps the null
  >   under `assert` (rmw fills a default only where the key is ABSENT, and a
  >   null-valued key is present) and took the default under `own`. A value changing
  >   across the switch is this section's criterion broken, in the form [`OQ-CO12`](#13-decision-ledger)
  >   ruled it — the relaxation freed FORMATTING, not values. The control settles
  >   the direction rather than leaving it to taste: the same file holding
  >   `"theme": "dark"` keeps `"dark"`. Measured by
  >   `TestSwitchingToOwnKeepsANullThatALowerLayerDeclares`
  >   ([`hostownednullprecedence_test.go`](../../internal/entrypoint/hostownednullprecedence_test.go)),
  >   which asserts BOTH halves of the disagreement — that `assert` fills an absent
  >   default and leaves a null one — so it cannot be satisfied by changing `assert`,
  >   and by `TestLiteralNullsLoseOnlyToLayersAboveTheFile`
  >   ([`literalnullprecedence_test.go`](../../internal/agentcfg/literalnullprecedence_test.go))
  >   one layer at a time.
  >
  > ⚠ **With ONE layer excluded, and only a real jail boot found it: the capture
  > overlay is not evidence.** It is a record OF the file, so letting it outrank the
  > file is circular — and circular in the direction that loses the key. `mergeDiff`
  > cannot tell a key the user ADDED with a null value from one they DELETED (both
  > are `k: null` in a patch), so a file gaining `"k": null` in STEADY STATE records
  > a tombstone, which then deletes the very key it was recording. Every test here
  > missed it because they all exercise the ADOPTION branch, where the overlay is
  > built from the file in the same breath; it took the nested-jail run the build
  > rules require, against `claude/settings` on a real boot. The tombstone is left in
  > the sidecar rather than swept, because sweeping durable state deserves its own argument —
  > **but the reason first given for leaving it, that it is *inert* once it is not evidence, is
  > false and was measured false the same day.** It is inert for the RENDER and not for the two
  > commands that read the sidecar directly; [the residue below](#what-the-criterion-does-not-see--live-residue-measured-after-the-ruling)
  > has the measurement and the shape of the fix.
  >
  > **The switch is never silent on any axis.** `WouldChange` compares BYTES —
  > deliberately stricter than this criterion
  > ([`hostStatefulWouldChange`](../../internal/entrypoint/hostrender.go)) — so a
  > conformant reformat is still announced as a pending change, and the adoption
  > archive still holds the file as yolo found it
  > ([§6.3.3](#633-what-survives-as-a-guard)).
  >
  > ⚠ **"Not silent" is thinner than it looks for COMMENTS, and it is the one axis
  > worth checking rather than assuming.** `WouldChange` reports that a byte moved
  > in the same undifferentiated word it uses for a re-sorted key, so it cannot say
  > that prose was destroyed; and `Archived` names the copy only on the WRITE — a
  > dry run makes none, deliberately — so it is not a warning that precedes the
  > one-way door. What is left is `Formatting`, whose whole job is to say the
  > comments will not survive, in observe, before the write. It was computed from
  > an RMW SIMULATION for every mechanism, and rmw *reattaches* comments — so on an
  > `own` render it answered with the handful rmw would have dropped, which is
  > approximately none, on the one render that drops all of them. Measured and
  > fixed 2026-09-12 (`hostFormattingLosses` now branches on mechanism, as
  > `hostMechanismWouldChange` beside it already did);
  > `TestSwitchingToOwnDisclosesThatCommentsWillNotSurvive`
  > ([`hostownedformattingloss_test.go`](../../internal/entrypoint/hostownedformattingloss_test.go))
  > is the standing measurement, with `TestASteadyStateOwnedTOMLFileReportsNoCommentLoss`
  > beside it because yolo's own generated header is itself comments — a probe that
  > cannot tell the header from the user's prose reports a loss on every apply
  > forever, and `configResultTier` reads this field.
  >
  > **The general form, worth stating once:** relaxing a criterion moves the burden
  > of an axis it frees onto whatever ELSE reports that axis. Freeing comments was
  > ruled, and is right; leaving the only field that names them answering for a
  > different mechanism is what would have turned the ruling into a silent loss. While the two deletions stood, what
  > they defeated was the LOSS GATE rather than the announcement: neither
  > `EntryLosses` nor `Formatting` names a dropped `null`- or `{}`-valued key, so
  > `yolo host apply` never prompted for one. Nothing was added to those fields to
  > close it — the keys survive, so there is no loss left for a gate to name.
  >
  > Measured by `TestSwitchingToOwnPreservesKeysAndValues`
  > ([`hostownedkeysandvalues_test.go`](../../internal/entrypoint/hostownedkeysandvalues_test.go)),
  > whose fixtures are deliberately non-canonical — unsorted JSON, a commented TOML
  > file — so each case asserts both that the bytes DIFFER and that the values do
  > not, and renders twice so the FIXED POINT is measured too. That last part is
  > load-bearing for the nulls: a literal null is the one value no sidecar can
  > hold, so it is re-read from the file on every render rather than replayed from
  > the overlay, and a fix applied to the adoption branch alone would put the key
  > back once and drop it on the next apply.
  > `TestSwitchingToOwnKeepsACanonicalFileByteIdentical`
  > ([`hostownedadoption_test.go`](../../internal/entrypoint/hostownedadoption_test.go))
  > keeps the stricter byte comparison for the one fixture that is canonical on
  > every freed axis, where it is the sharper instrument rather than a stale one.
- A `yolo host apply --assert` under `assert` still leaves an undeclared key
  byte-identical — the property measured on 2026-09-09 and the one thing this
  design must not regress.

#### What the criterion does not see — live residue, measured after the ruling

> [!CAUTION]
> **A relaxed criterion is a smaller instrument, and a ✅ over what it can no longer see is the
> failure this section is already the case study for.** So a second verification pass ran on
> 2026-09-12, after the two deletions were fixed, and measured the six below through
> `RenderHostPack` and `Compose` against real fixtures rather than reasoning about them. **None
> was fixed here.** Two are value-model calls whose fix reaches well past this design, one is the
> durable-state sweep the note above declined to make a side effect, and three are smaller. Two of
> the smaller three — items 4 and 6 — were fixed on 2026-09-25, each in its own commit with its own
> test, and are marked **FIXED** where they stand; the rest sit here as present-tense behavior
> because that is what they are — a reference would say the same sentences.

**1. A JSON integer past $$2^{53}$$ is silently rounded, and keys-and-values is structurally
incapable of seeing it.** Measured: a file holding `"bigId": 9007199254740993` keeps that value
under `assert`, because `rmw` touches only the keys yolo declares, and becomes `9007199254740992`
under `own`. **The criterion says the two are equal, and it is right to** — `codec.JSON` decodes
through `encoding/json` into `any`, so both sides are `float64` and compare equal. The byte
criterion caught this; keys-and-values cannot, ever, and the reassurance stated above — *a number
cannot differ by type across the comparison, because one codec decodes both sides* — is precisely
the mechanism that hides it. Nothing else names it either: `Formatting` is empty and correctly so
(JSON has no comments), `EntryLosses` and `Overwrites` are empty, and `WouldChange` gives a
re-sorted key and a rounded integer the same undifferentiated word. The adoption archive does hold
the original.

> [!WARNING]
> **The `rmw` half solved this deliberately and the composing half never inherited the fix.**
> `tomlValue`'s docstring ([`surfacecodec.go`](../../internal/entrypoint/surfacecodec.go)) says so
> in as many words — *"INTEGERS STAY INTEGERS. `jsonx.Plain` turns an integer literal into
> float64 … the TOML emitter would then render it `4096.0`, silently retyping a user's
> `model_max_output_tokens` on every apply"* — and lowers integers to `int64` for exactly this
> reason. `Compose` has no such step. TOML surfaces are unaffected, because their decode yields
> `int64` already. A fix means `json.Number` through `codec.Decode`, which reaches merge, diff,
> enforce and promote and every `float64` fixture behind them: **a value-model decision, not a bug
> fix**, which is why it is recorded here rather than closed in a commit.

**2. The stale tombstone is not inert, and two commands now state the opposite of the truth.**
The render half of the *inert* argument holds — the key survives, because the overlay is no longer
evidence against the file. The REPORTING half does not. Measured over three renders: boot 1 renders
a file; the user types `"autoMemoryEnabled": null`; boot 2 keeps the key in the file and writes
`{"autoMemoryEnabled": null}` into the overlay sidecar; boot 3 leaves both exactly there, so the
record is durable and the file and the sidecar now disagree about what that `null` means.
[`configdiff.go`](../../internal/cli/configdiff.go)'s `Deleted: v == nil` is the one definition
both readers share, so:

- **`yolo config diff` prints *"deleted in-jail"*** for a key the file holds.
- **`yolo config promote` offers it** as *"a captured DELETION — declaring it in `local` deletes
  the key wherever that pack renders"* and, accepted, writes the `null` into the local pack's
  `config-overlay`. That block is a merge patch, `local` folds after every other pack and needs no
  `packs` entry, so the promoted null becomes a **real** tombstone at every notch — and promote
  clears the key from the capture overlay, leaving the user's null existing only as a deletion.
  The user asked to ADD a null-valued key.

⚠ It also falsified a claim shipped in [`literalnull.go`](../../internal/agentcfg/literalnull.go) —
*"`yolo config promote` refuses it as not in the capture, and that refusal is correct rather than a
gap"*. The comment was corrected the same day and now points here. The claim is true on the ADOPTION branch, where `dropNullLeaves` strips the residue's nulls before
anything can see them; false on the steady-state branch, which is the branch that escaped to a real
boot once already. **The fix is provable and its two sets are disjoint by construction:** a keypath
marked in `Inputs.LiteralNulls` means the DECODED FILE holds that key, so it can never be a genuine
deletion record, which requires the key to be absent. What is unruled is WHERE — sweep the
accumulated sidecar, narrow the delta before it accumulates, or fix the readers — and that is the
durable-state argument, not a patch to make on the way past.

**3. A literal `null` in the HOST file of a `readsHost` surface never reaches the jail, and
tombstones yolo's own default on the way.** The same collision on a second, unfixed channel:
`Inputs.LiteralNulls` is read off the CURRENT FILE only, and `Inputs.HostBytes` has no equivalent.
Measured through `Compose`: a host file of `{"apiKeyHelper":null,"theme":null,"other":"reaches"}`,
against a surface declaring `readsHost` and a `defaults` of `theme: "system"`, composes to
`{"other":"reaches"}` — `apiKeyHelper` silently absent, and `theme` DELETED, so the jail receives
neither the host's null nor yolo's own default. `apiKeyHelper: null` is
[`literalnull.go`](../../internal/agentcfg/literalnull.go)'s own motivating example and
`claude/settings` is the shipped `readsHost` surface, so the class is not hypothetical. Left
unfixed because it needs the same argument made in a different place: *a decoded file holds no
tombstones* is what licensed the jail-file channel, and whether it licenses the host file — and at
what precedence a host-file null should fold — is a ruling rather than a patch.

**4. ✅ FIXED 2026-09-25 — `reinstateAt` panicked on a typed-nil map, on the boot render path.**
Its subtree branch now allocates on a NIL map rather than on an absent key, and writes the map
back only when a null landed in it; `TestLiteralNullsReinstateUnderATypedNilMap`
([`literalnull_test.go`](../../internal/agentcfg/literalnull_test.go)) drives the measured case
below through `Compose`. What was measured:
[`literalnull.go`](../../internal/agentcfg/literalnull.go) is the only writer into the composed
config in its package, and every other writer allocates by construction, so nothing before it
needed the guard. Its subtree branch allocates when the key is absent — but a `map[string]any(nil)`
is PRESENT, so the leaf recursion assigns into a nil map. Measured: a surface whose `managed` holds
`permissions` as a typed-nil map, against a file of `{"permissions":{"ask":null}}`, panics as
`assignment to entry in nil map`. The one producer is `enforceValue` → `deepCopyMap(nil)`
([`enforce.go`](../../internal/agentcfg/enforce.go)), and **no shipped surface reaches it** —
`encoding/json` never yields a typed-nil map, so no manifest and no sidecar can carry one. Latent,
one line, recorded so the next rewriter of a `managed` block does not find it on a user's boot.

**5. The same key is labelled `host` at one notch and `overlay` at the other, and the corpus built
to catch that has no fixture for it.** At `assert`, `rmwProvenance` records `LayerHost` for every
key the file had — `present := obj.Keys()`, null-valued keys included
([`prism.go`](../../internal/entrypoint/prism.go)). At `own`, `Compose`'s literal-null pass records
`overlay`. Before the fix there was no shared key to disagree about, because `own` deleted it.
[`provenanceparity_test.go`](../../internal/entrypoint/provenanceparity_test.go) exists for exactly
this class — *the two derivations naming different winners for the same layer set* — and refuses an
UNDOCUMENTED divergence, and no literal-null fixture was added to it. Reporting-only, since
`LayerAsserted` is false for both labels and no mechanism turns on the difference; but whether
`overlay` is the honest label for a value that came from the file is a judgement, and a declared
divergence is what that corpus is for.

**6. ✅ FIXED 2026-09-25 — adjacent, and not this ruling's subject: `assert` wrote invalid TOML for
a non-finite float, and `own` then refused the file.** The scalar encoder now spells the three
TOML's own way — `inf`, `-inf`, `nan` — before the `.0` rule runs, and
`TestTOMLNonFiniteFloats` ([`codec_test.go`](../../internal/agentcfg/codec/codec_test.go))
round-trips each through decode. A NaN's sign is not preserved (`-nan` writes back as `nan`):
which bit pattern TOML's `-nan` names is implementation-defined, and both decode to a NaN. What
was measured: `codec.TOML`'s scalar encoder formats a `float64` with
`strconv.FormatFloat` and appends `.0` whenever the result contains none of `.eE`, so a TOML file
holding `infinite = inf` survives decode as `+Inf` and is written back as `infinite = +Inf.0`.
Measured: the `assert` apply reports `rendered`, and the next `own` render refuses with *"is not
valid TOML … refusing to rewrite it, because a read that cannot see your keys cannot preserve
them"* — loudly, which is the right failure, over a file `assert` had already corrupted. NaN is the
same class. Pre-existing, and unreachable from the switch except through that corruption, so it is
recorded rather than folded into the criterion.

---

## 12. Follow-ons, and the one this design settled itself

> [!NOTE]
> **`OQ-CO<n>` here is not BACKLOG's bare `OQ-CO`**
> ([two packs writing one `config-overlay` key](../plans/BACKLOG.md#-oq-co--two-packs-writing-one-config-overlay-key-is-silent-last-one-wins)).
> The prefixes collided by accident and both stay, because ids are an API; a grep
> for `OQ-CO` returns both, so read the digit. The two are not unrelated —
> BACKLOG's is [§5.4](#54-promotion-moves-a-key-down-the-stack)'s residual
> precedence case seen from the pack side.

**One open, and the design opened neither.** Every question the design itself opened is settled
— the last three on 2026-09-11 — and so is the one its BUILD opened
([`OQ-CO12`](#13-decision-ledger), ruled 2026-09-12, which closed this section for eight days).
Two follow-ons were opened by RULINGS on 2026-09-20. The first was **decided the same day as an
implementation shape**, because its four candidates differed in what the code carries and not in
what a user gets ([`CO13`](#13-decision-ledger)), and **built 2026-09-25**. The second is
[`OQ-CO14`](#oq-co14) and is
**open**, because its candidates differ in what the user gets by the width of their whole
installation. All of them are in [§13](#13-decision-ledger), with the rulings themselves living
in the sections they govern.

### `CO13` — how a derive says it fills a computed table in full — **DECIDED**

> [!IMPORTANT]
> **This was filed as an open question and should not have been, and the correction is worth
> keeping.** Every one of the four shapes below was rejected on **blast radius, granularity or a
> purity contract** — engineering trade-offs with a defensible answer, not policy with a
> stakeholder. Filing it as `OQ-` converted design work into a question for the maintainer, and
> nothing downstream re-checks that conversion. **The test is whether the alternatives differ in
> what the USER gets.** Here they do not: every shape produces identical behaviour and differs
> only in what the code has to carry. That is a decision the design makes.
>
> **DECIDED 2026-09-20: a third derive sentinel**, beside `ctx.tombstone` and `ctx.empty_array`,
> wrapping the table whose leaves the derive regenerates in full.

**Why that one, and it turns on a fact rather than a preference.** The objection recorded against
the sentinel below — *"it needs a Go representation that survives to `Inputs.Computed` and is
removed before encode"* — **is not a new cost. It is the cost already paid twice, in the same
file, for the same reason.** Verified in
[`luahook/derive.go`](../../internal/agentcfg/luahook/derive.go): `tombstoneName` and
`emptyArrayName` are both declared there, both exposed on `ctx`, and both decoded on the way
back. A third is a third instance of a solved pattern.

The reserved-key shape is the one that looked closest and is genuinely worse, for the reason its
own row gives: **every reader of a computed layer would have to learn to strip it**, and a reader
that forgets writes a literal marker key into a user's file. A sentinel is stripped by machinery
that already exists and already has that job. That asymmetry — a new obligation on every reader
versus a third use of one decoder — is the whole argument.

The other two stay rejected on **correctness**, not taste: a field on `manifest.Surface` is the
wrong granularity, because the claim is per-KEY and one surface can hold both kinds; and the
double probe needs the derive run twice and its answer carried into a function whose contract is
purity, which the row rightly calls a declaration with extra steps.

**Scope of the decision:** it is not done until the HOST notch uses it too. `hostTableKeys`
([`hostrender.go`](../../internal/entrypoint/hostrender.go)) answers the same question with the
same coarse rule and feeds SENTINEL live tables, so `claude/settings` reports `env` and
`enabledPlugins` as wholesale-owned on every host apply regardless of configuration. One
declaration, both notches — or they disagree about which keys are tables, which is the exact
failure that function's docstring says it exists to prevent.

#### Built 2026-09-25 — what shipped

- **The sentinel.** `ctx.in_full(t)` ([`luahook/derive.go`](../../internal/agentcfg/luahook/derive.go))
  returns a userdata wrapping `t`; the decoder that already strips the other two sentinels
  unwraps it to the plain object and reports its key in `DeriveOutput.InFull`. It is refused
  below the top level, around an array, and around anything but a table — each loudly, because
  a declaration honored at the wrong depth is the silent reading. Nothing downstream sees the
  wrapper, so no reader of a computed layer learned to strip anything, which is the asymmetry
  this section chose it for.
- **The jail notch.** `deriveComputedLayer` returns the declaration beside the layer; the
  stateful arm carries it through `render.Layers.ComputedInFull` to
  `agentcfg.Inputs.ComputedInFull`, and `dropComputedTables` takes a non-empty table whole only
  when it is named there. Everything else is left to the leaf pass, which removes exactly the
  leaves the computed layer names. Core's own `mise/config` has no derive, so its Go caller
  states it: `ConfigureMisePrism` ([`prism_mise.go`](../../internal/entrypoint/prism_mise.go))
  declares `tools`.
- **The host notch.** `hostTableKeys` keeps only declared keys, so `claude/settings` no longer
  reports `env` and a host apply no longer clears the user's `env` block and rewrites it from
  layers that declare none. The host's `own` arm hands adoption its table layer's own key set
  (`hostTableInFull`), since every key of that layer is a table `hostTableKeys` kept.
- **The shipped declarations**, classified by the one criterion the rejected double probe
  below calls sound: a table whose key set TRACKS a live table is regenerated in full, and one
  with a FIXED key set is not. Declared: every MCP table (`agy/mcp`, `claude/config`,
  `codex/config`, `copilot/mcp`, `opencode/config`, `pi/mcp`), `copilot/lsp`'s `lspServers`,
  the provider catalogs (`codex/config`'s `model_providers`, `opencode/config`'s `provider`,
  and `providers` on `pi/models` and `oh-omp/models`), and `mise`'s `[tools]`. Not declared in
  full: `claude/settings`' `env` and `modelPicker`, and `pi/settings`' `subagents`. ⚠ The
  catalogs are the one classification the criterion made rather than this section: they were
  taken wholesale before the build, and declaring them keeps that. Whether that is right is
  filed as [`OQ-CO16`](#oq-co16).
  `TestShippedDerivesDeclareTheirInFullTables` pins the whole list, both halves, by making each
  table exist: it runs every producer in two probe worlds, one with no provider selected and one
  with `openai-codex` selected, because `modelPicker` and `subagents` are produced only under a
  selection. A classified table that neither world produces fails the test, and so does a new
  producer until someone classifies it.
- **Version skew, one direction only.** Each `derive.lua` reaches the sentinel through a local
  guard (`in_full(ctx, t)`), so an entrypoint older than the sentinel — whose `ctx` has no
  `in_full` — runs the script and gets the same layer with no declaration, which it reads as it
  always did. An unguarded call would repeat the `yolo.env` incident
  (`luahook.DeriveCtx.UnknownAPI`): the tolerance for an unknown API covers `yolo.*`, not a field
  of `ctx`. ⚠ **The reverse pairing is NOT covered, and the guard cannot cover it.** `derive.lua`
  is staged from the HOST binary's embedded packs (`packload.MaterializeEmbedded(packs.FS, …)` in
  [`internal/cli/run/packs.go`](../../internal/cli/run/packs.go)), while the entrypoint reading
  the declaration is built from the flake. A host `yolo` older than this build therefore hands a
  newer entrypoint derives that declare nothing, and that entrypoint adopts every MCP table leaf
  by leaf: on a first migration a server removed from config survives in the rendered file and is
  captured into the overlay as the user's own, so it keeps coming back after the skew is gone,
  until `yolo config reset`. Measured: the pre-build `packs/codex/derive.lua` and
  `packs/opencode/derive.lua` under the new Go code fail `TestConfigureCodexPrismFirstMigration`
  and `TestConfigureOpencodePrismFirstMigration` with the stale server kept. The pairing arises
  only when `YOLO_REPO_ROOT` names a source tree newer than the host `yolo`, since both shipped
  flake bundles travel with the binary ([`srcskew.go`](../../internal/version/srcskew.go)), and
  the only thing that refuses it is the `version.SourceSkew` launch gate. So a launch under
  `YOLO_ALLOW_SOURCE_SKEW=1`, or one where the change is still uncommitted (the gate compares
  commits, so it is silent there), is exposed: taking the hatch accepts this resurrected server
  along with every other host↔jail contract move it waves through. Closing the gap would need a
  signal that tells an old derive apart from one that declares nothing, such as a per-script
  marker that the script knows `ctx.in_full`; that is not built.
- **Tests.** The data-loss case at the engine (the pinned test, inverted:
  `TestComposeStatefulFirstMigrationKeepsTheUnassertedLeavesOfAPartlyAssertedTable`), through
  the shipped claude pack at the jail (`TestConfigureClaudePrismFirstMigrationKeepsTheAgentsOwnEnv`)
  and at the host (`TestHostApplyKeepsTheUsersClaudeEnv`, `TestHostTableKeysDoNotClaimClaudeEnv`),
  the per-key granularity (`TestComposeStatefulFirstMigrationInFullIsPerKey`), the host
  `own` arm (`TestHostOwnAdoptionStillRegeneratesADeclaredTable`), and that arm's dry run
  (`TestHostOwnObserveAgreesWithAssertUnderADeclaredTable`: on a first owned apply over a file
  already holding the canonical render of an earlier config, `WouldChange` must be true exactly
  when the `--assert` drops the stale server). The four data-loss tests were written first and
  failed against the unfixed tree. Each production call site was then deleted in a private copy
  and a named test failed, with ONE exception: the declaration `hostListConflict` passes
  ([`hostrender.go`](../../internal/entrypoint/hostrender.go)). It is passed so that probe runs
  the writer's exact composition, but it cannot change the probe's answer. The declaration
  shapes only the capture overlay adoption seeds, and `Compose` folds `config-list` entries
  BELOW that overlay ([`compose.go`](../../internal/agentcfg/compose.go)), so no list conflict
  can depend on it and there is nothing for a test to assert.

⚠ **One residual, recorded rather than built.** The jail's `rmw` arm
(`regenerateManagedTables`, [`prism.go`](../../internal/entrypoint/prism.go)) still regenerates
EVERY object-valued computed key wholesale, declared in full or not. No shipped `rmw` surface's
derive returns a table it does not declare in full (`claude/config` returns only `mcpServers`,
which is declared, and `copilot/config` has no producer), so nothing observable changes today.
But a pack's `rmw` surface returning an assert-leaves table would have it cleared in a jail and
merged at the host — the notches disagreeing, which the scope note above exists to prevent.
Closing it means choosing what `rmw` does with a table not declared in full (assert its leaves,
or skip it with a note), which changes what such a pack gets, so it is filed rather than guessed,
as [`OQ-CO15`](#oq-co15). Until then, every statement that a table not declared in full "claims only the leaves it names"
holds for the `stateful` adoption and for the host's table probe, not for the jail's `rmw` arm.

<details>
<summary>The four shapes as they were weighed</summary>

**Opened 2026-09-20 by the ruling in
[the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule),**
which narrowed the adoption drop and could only narrow it halfway. The engine has to tell
a table yolo REGENERATES (`mcpServers`, `mise`'s `[tools]`) from one it only ASSERTS
LEAVES of (claude's `env`, `enabledPlugins`), and:

- **the leaf half already exists** — the computed table's key set IS the asserted set,
  tombstones included (MEASURED against the shipped pack);
- **the fill-in-full half exists nowhere**, and four candidate discriminators were
  measured to flip on configuration rather than intent ([the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule));
- **guessing it wrong costs data in one direction and correctness in the other** — a
  wrong "leaves" reading resurrects a deleted MCP server (six tests measure this), a wrong
  "in full" reading deletes the agent's own `env`.

So it must be DECLARED by the derive that knows it, and the question is where the
declaration lives, not whether. The shapes considered and not chosen, each rejected on the
same axis — what else would have to learn to strip or carry it:

| Where | Why not, yet |
| :--- | :--- |
| A reserved namespace in the derive's return, like `agentcfg.SelectionKey` | The precedent is right and the blast radius is not: every reader of a computed layer would have to strip it — `TakeSelection`'s call site, `hostTableKeys`' probe, `yolo config render` — or it reaches a user's file as a literal key |
| A third Lua sentinel beside `ctx.tombstone` / `ctx.empty_array` | The narrowest spelling at the derive, but it needs a Go representation that survives to `Inputs.Computed` and is removed before encode |
| A field on `manifest.Surface` | Wrong granularity: the claim is per-KEY, and a surface can hold both kinds |
| Inferred from a double probe (run the derive with empty vs. sentinel live tables; a key set that TRACKS the live table is fill-in-full, a FIXED one is assert-leaves) | The only sound inference found, and it needs the derive run twice and the answer carried into a function whose contract is purity — so it is a declaration with extra steps |

</details>

⚠ **The same over-claim exists at the HOST notch and is part of this decision's scope.**
`hostTableKeys` ([`hostrender.go`](../../internal/entrypoint/hostrender.go)) answers the
same question with the same coarse rule — "an object-valued key the derive produces is a
table yolo owns wholesale" — and its probe feeds SENTINEL live tables, so `claude/settings`
reports `env` and `enabledPlugins` as owned tables on every host apply regardless of what
is configured. Building [`CO13`](#13-decision-ledger) owns that call site too, or the two notches will
disagree about which keys are tables — which is the exact failure `hostTableKeys`' own
docstring says it exists to prevent. **Closed by the build (2026-09-25):** `hostTableKeys` now
keeps only the keys the derive declares in full, as reported by the same `deriveComputedLayer`
the jail's adoption reads them from ([what shipped](#built-2026-09-25--what-shipped)).

#### Does retiring `assert` change `CO13`'s scope?

**No — it neither dissolves nor narrows it, and the premise that it might rests on a misreading
of what `CO13` is for.** Worth recording, because "the RMW notch is going away, so the thing
that exists for the RMW notch goes with it" is the available inference and it is wrong twice
over. MEASURED 2026-09-20:

- **`CO13`'s mechanism is `stateful`, not `rmw`.** The drop it governs is `dropComputedTables`,
  inside `ComposeStateful`'s first-migration branch — a whole-file composition adopting a file
  that already exists. `rmw` never reaches it. The case that produced the ruling is a **jail**
  first migration (`mise` on a jail with no pin,
  [the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule)),
  and a jail runs `stateful` at every posture: `render.Target.censusNotch` drops ownership for
  every kind but the host, because `host_management` has no referent inside a container.
- **Its host half survives too, in the arm that remains.** `hostTableKeys` is computed **before**
  the mechanism switch in `RenderHostPack` and its layer is passed into **both** arms — the
  stateful arm's own comment says so: *"it holds for both arms: under `own` the same layer is
  what makes `mcpServers` a table yolo regenerates rather than one adoption freezes at whatever
  the file held."* So the over-claim `CO13`'s scope note records is an `own`-side fact as much
  as an `assert`-side one.
- **What does change is the population, not the scope.** `hostTableKeys` runs on every host
  apply today because the unset key resolves to `assert`; after the flip it runs only where a
  user declared `own`. That lowers the blast radius of the over-claim and lowers nothing else.
  The one simplification is small and real: the host stops having more than one RENDERING
  contract for *"one declaration, both notches"* to hold across.

**So `CO13` is unchanged by the retirement and is not blocked by it.** The two can be built in
either order — and if the retirement lands first, whoever builds `CO13` has one fewer host
contract to make the declaration agree across.

### <a id="oq-co14"></a>💬 [`OQ-CO14`](#oq-co14) — what the retirement does to a config already on `assert` — **OPEN**

**Opened 2026-09-20 by the ruling in [§4.5](#45-retiring-assert--the-two-value-key).** It is a
question and not an implementation shape by this section's own test — *do the alternatives
differ in what the USER gets?* — and here they differ by the width of the installation: refusing
the launch, silently ceasing to manage a file yolo has been maintaining, and silently beginning
to compose that file whole are three different mornings.

**Two faces, one question, and they may resolve differently.** It is filed as one because a
single migration answers both, and both must be answered before the value is dropped:

1. **A user config that says `"assert"`.** That is a declaration the user made on purpose, and
   after the retirement it is unspellable. Refuse the launch naming `own` and `promote`? Resolve
   it to `none` with a notice? Resolve it to `own`, which is the one answer that takes deletion
   authority over a real home from a value the user chose for the opposite reason?
2. **An absent key on a home yolo has already asserted into.** The user never declared anything;
   today the unset state resolves to `assert` and yolo keeps the file current. After the flip it
   resolves to `none`: yolo stops, and the keys it last wrote stay in the file with nothing left
   to retire them — plus the mark those writes left, which is what makes the file arrive in a
   jail as a baseline rather than a layer
   ([§4.5.1](#451-what-actually-carries-it-assert-is-the-one-contract-where-p6-and-p7-collide)).
   Whether the retirement clears the mark, or names `yolo host apply --revert` as the remedy, is
   part of this.

**It re-opens [`OQ-CO2`](#13-decision-ledger), which is why it cannot be left to the build.**
That ruling's "neither prompt nor notice" rests on one stated ground — the unset state equals
today's behaviour, so nobody is interrupted to be told nothing changed
([§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running)). Moving the default
to `none` removes the ground without touching the conclusion, and a conclusion whose only
premise has expired has to be re-argued rather than inherited. ⚠ **Face 2 is the one this
document is likeliest to get wrong**, because it is invisible on the maintainer's own machine:
a developer who has written the key is unaffected by either face.

⚠ **Two open questions are not two outstanding items, and the questions are kept apart from the
residue on purpose.**
The measured behaviors still unclosed live in the body as present-tense **residue** rather
than here: [§6.3.3](#633-what-survives-as-a-guard)'s unnetted overlay-sidecar loss, and what is
left of [§11](#11-success-criteria)'s
[list](#what-the-criterion-does-not-see--live-residue-measured-after-the-ruling), whose two
smallest items were fixed on 2026-09-25. Three of them
will need a ruling eventually — a JSON value model, a host-file null's precedence, and where a
stale tombstone gets swept — and none is opened as an `OQ-CO` here, because a question in this
section is one whose answer this design owes before it can be believed, and all three outlive it.
Whoever picks one up opens it where it lands.

**One consequence is large enough to state here rather than leave in a ledger row.**
[`OQ-CO10`](#13-decision-ledger) decided the *shape* of the read-in `host` layer — the
declaration moves onto the surface, the read fails closed, coverage becomes a visible
per-surface yes/no. **Deciding a mechanism's shape decides that it exists**, so the
2026-08-01 ruling to retire that layer
([`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase))
is **superseded**, and it is one of four from that ledger this design reverses —
[§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications) names them all with
their dated reversals.

### <a id="oq-co15"></a>💬 [`OQ-CO15`](#oq-co15) — what the jail's `rmw` arm does with a table not declared in full — **OPEN**

**Opened 2026-09-25 by the `CO13` build**, as the one residual
[what shipped](#built-2026-09-25--what-shipped) records. The jail's `rmw` arm
(`regenerateManagedTables`, [`prism.go`](../../internal/entrypoint/prism.go)) does not read
`ctx.in_full`: it clears and rewrites every object-valued key the computed layer returns. The
host's table probe (`hostTableKeys`) claims only the declared keys and merges the rest. So a
pack whose `rmw` surface returns a table it does not declare in full would have that table
cleared in a jail and merged at the host, which is the two notches disagreeing. **No shipped
pack does this today**, so nothing a user sees changes until a pack does. It is a question and
not an implementation shape, because the options differ in what that pack's user gets:

- **(a) Assert its leaves.** Merge the table leaf by leaf into what the file holds, as the host
  does. The jail and the host then agree, and a user's own keys in that table survive a boot.
  A leaf yolo stopped asserting stays in the file unless the derive tombstones it.
- **(b) Skip it with a boot note.** Leave the table as the file holds it and print that the
  derive returned a table `rmw` will not regenerate. Nothing is lost, and nothing is written
  either, so the pack's contribution silently does not arrive.
- **(c) Refuse the pack.** Reject, at pack load, an `rmw` surface whose derive returns an
  object-valued key it does not declare in full. Loud and early, and it forbids a shape the
  host arm already handles.

<!-- vantage: oq id=OQ-CO15 leaning="(a) assert its leaves: it is what the host notch already does, so it makes one declaration mean one thing at both notches, which is the scope note's own requirement." -->

_Leaning:_ **(a).** It is what the host notch already does, so it makes one declaration mean
one thing at both notches, which is what the scope note above requires. (b) hides a pack's
contribution and (c) forbids a shape the host already handles. Not urgent: it changes nothing
until a pack ships the shape.

**Answer:**
> _(empty — fill in when decided)_

### <a id="oq-co16"></a>💬 [`OQ-CO16`](#oq-co16) — do the provider catalogs stay declared in full? — **OPEN**

**Opened 2026-09-25 by the `CO13` build.** Every other classification in
[what shipped](#built-2026-09-25--what-shipped) follows a plain reading of what the table is
for. The provider catalogs (`codex/config`'s `model_providers`, `opencode/config`'s `provider`,
and `providers` on `pi/models` and `oh-omp/models`) were declared in full because the
criterion said so: their key set tracks a live table, the selected providers. That keeps what
they did before the build, which was to be taken wholesale. The cost is the same one an MCP
table has: a provider entry the user added by hand beside yolo's is dropped at a first-migration
adoption and at a host apply. Unlike an MCP server, a hand-added provider is a thing users
plausibly have.

- **(a) Keep them declared in full** (today's behavior). A provider removed from config leaves
  no stale entry. A user's own provider must be declared in yolo config to survive.
- **(b) Stop declaring them.** A user's hand-added provider survives. A provider yolo stopped
  selecting stays in the file unless the derive tombstones it, and a stale provider entry
  points at an endpoint or credential the jail no longer serves.
- **(c) Decide per catalog.** For example, keep the ones whose agent has no way to add a
  provider in its own UI, and stop declaring the rest.

<!-- vantage: oq id=OQ-CO16 leaning="(a) keep them declared in full: a stale provider entry names an endpoint the jail no longer serves, which fails at use rather than at boot, and yolo config already has a place for a user's own provider." -->

_Leaning:_ **(a).** A stale provider entry names an endpoint or credential the jail no longer
serves, so it fails when the agent uses it rather than at boot, which is worse than a dropped
entry the boot can report. And yolo config already has a place for a user's own provider.
Weak: it rests on a guess about how many users hand-edit a catalog.

**Answer:**
> _(empty — fill in when decided)_

---

## What the graduation owes — the scope of the rewrite

This scope was written on 2026-09-12 in a triage record, `doc-triage.md`, and moved here on
2026-09-25 when that record was retired (`git log --follow -- docs/plans/doc-triage.md` recovers
it). A **graduation** is the move of a built design's settled body into
[`../reference/`](../reference); the local rules it follows are in
[`../plans/README.md`](../plans/README.md#oq-dt1).

**Not a candidate yet.** [`OQ-CO14`](#oq-co14) is open and
[§4.5](#45-retiring-assert--the-two-value-key)'s retirement of `assert` is unbuilt, so a reference
written today would state a three-value key as current and expire on the next build.

**Where it would live:** `docs/reference/config-ownership.md`, which does not exist yet. It would
hold the ownership axis (the notch does not decide who owns a file; a declared key does), the modes
and their surface postures, promotion as the way out of capture including the precedence refusal,
the deletion asymmetry, and what the one-time adoption archive does and does not catch.
[§10](#10-what-i-would-build-in-order)'s build order is history and is cut;
[§6.3](#63-the-one-asymmetry-that-survives-deletion)'s argument is re-stated as a contract, and so
is [`OQ-CO12`](#13-decision-ledger)'s keys-and-values criterion: *`own` composes through the
surface's codec*.

**The ids that must survive.** Each ledger id cited from outside this file keeps its id in the
reference's `## Why it's this way` appendix, because after this file goes that appendix is the only
place it resolves. That means every [ledger](#13-decision-ledger) id, whichever way it is spelled:
most rows are `OQ-CO<n>`, but [`CO13`](#13-decision-ledger) has no `OQ-` prefix, and Go and
`packs/omp/derive.lua` cite it that way. The 2026-09-12 assessment counted eight cited from Go
([`OQ-CO1`](#13-decision-ledger), CO2, CO4, CO5, CO7, CO8, CO9 and CO10). On 2026-09-25 the same
sweep, widened to the unprefixed spelling, also finds [`OQ-CO12`](#13-decision-ledger) and
[`CO13`](#13-decision-ledger). Re-run it at the move rather than trust either list. It prints each
id with the `OQ-` stripped, so both spellings of one id are counted together:

```console
$ rg -o '\b(OQ-)?CO[0-9]+\b' internal cmd packs integration | sed 's/.*://; s/^OQ-//' | sort -V | uniq -c
```

**The residue, re-stated in present tense.** Seven live items were counted on 2026-09-12: the
unnetted overlay-sidecar loss in
[§6.3.3](#633-what-survives-as-a-guard) ([the residue it records](#what-the-net-does-not-reach--live-residue-found-after-it-shipped)),
and the six [§11](#11-success-criteria) measured after the [`OQ-CO12`](#13-decision-ledger) ruling
([the residue list](#what-the-criterion-does-not-see--live-residue-measured-after-the-ruling)).
An item marked **FIXED** where it stands is closed and carries nothing into the reference. Every
other item is behavior a reference would state as behavior — *here is what the render does and
does not preserve* — which is the same re-statement the criterion needs, once per item.

**Fix the tombstone readers before the move, not after.** Item 2 of
[that list](#what-the-criterion-does-not-see--live-residue-measured-after-the-ruling) is the one residue item with
a data-loss path: `yolo config diff` and `yolo config promote` read a stale tombstone as a deletion,
and an accepted `promote` writes that `null` into the local pack's `config-overlay`, where it is a
real tombstone at every notch. A reference that documents that as behavior documents a defect as
the contract. It is unfixed as of 2026-09-25; the fix is provable, and only where it goes — sweep
the sidecar, narrow the delta, or fix the readers — is unruled.

**Go's `§N` citations are repaired by hand.** Go comments cite this file by numbered section
([§11](#11-success-criteria) most of all), and a reference drops numbered sections, so each citation is re-pointed line by
line at whatever section now holds its text. The reason it is never a `sed` is in
[the rules](../plans/README.md#oq-dt1).

---


## 13. Decision Ledger

Rulings on [§12](#12-follow-ons-and-the-one-this-design-settled-itself)'s questions are folded into the normative
body text and compacted here, keeping the exact `OQ-CO` ids so citations from
sibling docs and code comments continue to resolve. **Every row but
[`OQ-CO14`](#13-decision-ledger), [`OQ-CO15`](#13-decision-ledger) and [`OQ-CO16`](#13-decision-ledger) is settled** — the ones the design opened, the last three of
those on 2026-09-11, [`OQ-CO12`](#13-decision-ledger), which its build opened, on 2026-09-12, and
[`CO13`](#13-decision-ledger), decided 2026-09-20 and built 2026-09-25. A RULING opened both of the
last two, which is the one way a settled design reopens.

> [!WARNING]
> **One row here is REVERSED, and it is the first one.** [`OQ-CO1`](#13-decision-ledger) is the
> only ruling this design has taken back, on 2026-09-20
> ([§4.5](#45-retiring-assert--the-two-value-key)). The row is kept with its original reasoning
> intact and the reversal beside it, for the reason the note below keeps the four foreign
> reversals visible: **a ruling overwritten in place reads as though it was never made**, and
> the next reader re-derives the original from the same premises. Reading [`OQ-CO1`](#13-decision-ledger) and stopping
> at the ✅ is the mistake this warning exists to prevent — the `Built` cell is still true,
> which is exactly why it is not evidence that the ruling still holds.

**Settled and built are two axes, and the second does not follow from the first.** Each `Built`
cell names the symbol that carries the ruling, read off the tree on 2026-09-12 rather than off
the sprint's commit messages; `n/a` marks a row that rules no mechanism of its own. **Every
ruling here now has code behind it**; [`OQ-CO7`](#13-decision-ledger) was the last without, and
its one-time adoption archive shipped on 2026-09-12 — **with four measured gaps, three closed
the same day and one left as open residue rather than an open question**
([§6.3.3](#633-what-survives-as-a-guard)): a `Built` cell says a mechanism exists, never that it
is complete. [`OQ-CO12`](#13-decision-ledger)'s row carries the same caveat for the same reason,
and its residue is the larger of the two.

> [!IMPORTANT]
> **This design reverses four rulings from [`environment-manager-plan.md`](../plans/environment-manager-plan.md)'s
> 2026-08-01 ledger, and all four reversals are deliberate** — recorded here because
> that ledger's existence is why this document spent four review rounds re-arguing
> settled ground. [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (no `--revert`) is reversed by
> [§10](#10-what-i-would-build-in-order) step 3; [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)/[`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (pure `rmw`; whole-file
> `stateful`+capture rejected) by [`OQ-CO3`](#13-decision-ledger); and [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (retire the read-in `host`
> layer) by [`OQ-CO10`](#13-decision-ledger), which decided that layer's shape and thereby its existence.
> **The reversal rows belong in that ledger too** — a ruling reversed in one document
> and unmarked in the other is the exact failure this note exists to stop repeating.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| [`OQ-CO1`](#13-decision-ledger) | ⛔ **REVERSED 2026-09-20 — two values (`none`/`own`), and `none` is the default** ([§4.5](#45-retiring-assert--the-two-value-key)). *The 2026-09-10 ruling, kept verbatim:* **Three values** (`none`/`assert`/`own`). `assert` is shipped behavior with real users, so collapsing it is either a regression or a forced escalation to `own`. The cost — one more thing to explain — is paid down by [`OQ-CO2`](#13-decision-ledger) removing the prompt that would have explained it. Alternative C in [§8](#8-alternatives-considered) resolves as rejected. *Why it could be reversed without having been wrong:* the escalation it feared was forced only because `yolo config promote` did not exist yet (it shipped two days later), and its balance weighs migration cost while naming no engine mechanism — [§4.5](#45-retiring-assert--the-two-value-key) has the dating and [§4.5.2](#452-what-the-ruling-does-not-delete--the-mechanism-tally-corrected) corrects the mechanism tally the reversal is tempted to overstate. Alternative C's conditional rejection resolves as **substantially adopted** | 2026-09-10, reversed 2026-09-20 | [§4.1](#41-the-key), [§4.5](#45-retiring-assert--the-two-value-key) | ✅ **for the reversed ruling** — `config.KnownHostManagements` holds the three and the validator's message enumerates that same list. ⬜ **the reversal is unbuilt**, and its steps are in [§4.5](#45-retiring-assert--the-two-value-key) |
| [`OQ-CO2`](#13-decision-ledger) | **Neither prompt nor notice** — the unset state is `assert`, silently. Each value explains itself at the point of the act; `apply --sealed` is the one place an unset key bites. *Against the leaning:* the ambiguity this design fixes was **inference**, not silence, so a documented default that equals today's behavior is declared in the only sense that matters. ⚠ **Its stated ground expires with [`OQ-CO1`](#13-decision-ledger)'s reversal** — `none` is not today's behaviour, so the silence has to be re-argued rather than inherited. The conclusion is not withdrawn here; it is [`OQ-CO14`](#13-decision-ledger)'s to re-rule | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) | ✅ `config.HostManagementDeclared` gives the two absences different answers — absent ⇒ `assert`, silently; unreadable ⇒ `none` — and `TestApplySealedRefusesAnUnsetHostManagement` pins the one place an unset key bites |
| [`OQ-CO3`](#13-decision-ledger) | **Yes, host-side capture under `own` only** — and it is a precondition of adoption, not an added capability: capture-then-regenerate is what makes the first owned render reproduce the file. The refusal stays for `none` and `assert`. Reverses env-manager plan [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)/[`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) — see [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications). | 2026-09-10 | [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to), [§6.3.1](#631-adoption-is-capture-then-regenerate) | ✅ the owned render's capture store (the one `render.Target.SidecarDir` resolves for the host notch) and host-side `reset`, both keyed off `configTarget.hostOwned` (named `hostOwnsSurfaces` until 2026-09-18) — ⚠ **with one deliberate narrowing**: the `config capture` VERB stays refused host-side even under `own`, because `refuseHostSideWrite` rests that half on privacy rather than on ownership |
| [`OQ-CO4`](#13-decision-ledger) | **Keep `local` as the default; the guard is the confirmation, not the flag.** Promote lists the selected keys and asks; `--accept-promotion` is the only way past it. **Not `--yes`** — this repo has already declined the generic form: `config.AcceptConfigChangesFlag` ([`snapshot.go`](../../internal/config/snapshot.go)) is `--accept-config-changes`, and its docstring rules that an approval must be a flag naming what is approved, never an env var a child process inherits. `--force` stays free for overriding a *refusal*. | 2026-09-10 | [§5.1](#51-surface), [§5.7](#57-forbidden-behavior) | ✅ `--accept-promotion` in `parsePromoteArgs`; `local` is `resolvePromoteDest`'s default |
| [`OQ-CO5`](#13-decision-ledger) | **Refuse-with-instructions in v1.** Design the jail→host request channel but do not build it until promote has been used enough to know which keys people actually promote — the transport is free, the consent prompt is what needs the evidence. | 2026-09-11 | [§5.5](#55-where-promote-may-run) | ✅ `refuseInJailPromote` names the host command. The jail→host request channel is designed and unbuilt, which is the ruling |
| [`OQ-CO6`](#13-decision-ledger) | **Refuse only, everywhere** — promote never offers the pack's `managed` block to a key that would lose precedence. For shipped surfaces nothing else is expressible (`managed` is owner-only and every owner is an embedded pack). For a user's own surface the friction is the point: a one-keystroke path to outranking `computed` would be used for exactly the reason that layer exists, so the managed block stays a hand edit. | 2026-09-11 | [§5.4](#54-promotion-moves-a-key-down-the-stack) | ✅ `classifyPromoteKey`'s `promotionOutranked` disposition; no path writes a `managed` block |
| [`OQ-CO7`](#13-decision-ledger) | **One archive at adoption**, as a `config` bucket in the archive subsystem that already ships — and, by [P5](#1-the-verdict-and-the-principles-it-rests-on), at a jail's `firstMigration` too. It covers a KNOWN loss path (the deep-merged-leaf drop) the prompt cannot see; the later-regression case is what git on the pack is for. Not per-apply snapshots. | 2026-09-11 | [§6.3.3](#633-what-survives-as-a-guard) | ✅ `entrypoint.archiveAdoption`, called from `persistStatefulSurface` — the ONE writer both notches share, so the host's `own` adoption and a jail's `firstMigration` get the copy from the same line rather than from two implementations. `render.Target.ArchivePath` resolves where it lands at each notch; `HostRenderResult.Archived` reports it. ⚠ Built is not complete: [§6.3.3](#633-what-survives-as-a-guard) records four gaps measured after it shipped — three are closed (an unreadable file, yolo's own output, and a verdict that closed an adopting run as a run with nothing to do); the one the net does not reach is left for a ruling |
| [`OQ-CO8`](#13-decision-ledger) | **`--to workspace` is out of scope for this design** — a decision, not a wait. It could not have been built here regardless: the `workspace` layer has no config key, no producer sets `Inputs.Workspace`, and `render.Host` leaves it empty by definition. Whoever wires that layer also owns the argument that a jail-writable layer must not reach a real home. | 2026-09-11 | [§5.1](#51-surface), [§7](#7-what-this-does-not-propose) | ✅ `resolvePromoteDest` refuses `--to workspace` by naming this ruling, rather than folding it into "unknown destination" |
| [`OQ-CO9`](#13-decision-ledger) | **Refuse `own` for a keyless surface, until a real example exists.** The guard-growing alternative was the author's leaning, not something evidence forced, and the class is empty today — so the cheap answer is the honest one. Revisit when a pack has a reason to want a keyless surface host-rendered. | 2026-09-11 | [§6.3.2](#632-the-three-classes-adoption-does-not-cover) | ✅ `render.HostOwnedModes` refuses the coercion and `entrypoint.hostStatefulRefusal` refuses the surface, leaving the user's file untouched |
| [`OQ-CO10`](#13-decision-ledger) | **The declaration moves ONTO the surface** so the binding is structural instead of a `path.Base` match, and **the read fails CLOSED** — which turns the `macos-user` silent drop into a refusal that names the backend. The disclosure survives (it comes from the declaration being present and enumerable, not from a separate kind), and the `reads-host` kind stays for `host_files`, whose entries have no mirrored twin. Coverage becomes a visible per-surface yes/no, making `mise/config` a deliberate **no**. Promote refuses `--to host` on a surface with no host layer. | 2026-09-11 | [§5.1.1](#511-why-only-two-surfaces-have-a-host-layer) | ✅ `manifest.Surface.ReadsHost` is the predicate, `packload.SurfaceHostFile` derives the `/ctx` path both halves evaluate, and `packload.HostLayerReport` makes the read fail closed; a launch that delivered nothing reports `unsupported` and is not refused. ⚠ The status cell read *"`macos-user` reports `unsupported` and is not refused"* until 2026-09-13: DP-L1 gave that backend a delivery mechanism, so it now reports `supported` and REFUSES an unreadable delivered file like every other backend, and `unsupported` became a fact about a launch rather than about a backend |
| [`OQ-CO12`](#13-decision-ledger) | **Keys-and-values, not bytes** — and the two silent deletions are BUGS, fixed on their own rather than absorbed. A composing renderer that sorts keys is the contract `own` states, and matching `rmw`'s byte layout would make the capture path carry formatting it has no reason to know about. So JSON key order and a TOML surface's comments and generated header are CONFORMANT; a key valued `null` or `{}` disappearing is not, at any depth. The comparator is the surface codec's own decode, defined in [§11](#11-success-criteria). | 2026-09-12 | [§11](#11-success-criteria), [§6.3.1](#631-adoption-is-capture-then-regenerate) | ✅ `TestSwitchingToOwnPreservesKeysAndValues` states the criterion over deliberately non-canonical fixtures — asserting both that the bytes differ and that the values do not — and `TestSwitchingToOwnKeepsACanonicalFileByteIdentical` keeps the stricter byte comparison where it is still the sharper instrument. Both deletions are FIXED (2026-09-12): `dropNullLeaves` keeps an object the user wrote empty, and `agentcfg.Inputs.LiteralNulls` carries a literal `null` beside the layer stack because no merge patch can hold one. ⚠ `hostStatefulWouldChange` stays a BYTE comparison deliberately, so a conformant reformat is still disclosed as a pending change. ⚠ **Built is not complete**: a second verification pass the same day measured [six live items](#what-the-criterion-does-not-see--live-residue-measured-after-the-ruling) the relaxed criterion cannot see or the fixes did not reach — a JSON integer past 2^53 rounded, the stale tombstone making `config diff` and `promote` misreport a null the user ADDED, a host-file null on a `readsHost` surface, a latent `reinstateAt` panic, an undeclared provenance divergence, and an `assert`-side non-finite float. The panic and the float are FIXED (2026-09-25: `TestLiteralNullsReinstateUnderATypedNilMap`, `TestTOMLNonFiniteFloats`); the rest are not |
| [`OQ-CO11`](#13-decision-ledger) | **The read-in `host` layer stays — decided by [`OQ-CO10`](#13-decision-ledger), not separately.** Ruling a mechanism's binding, failure direction and coverage decides that it exists; asking in the same breath whether to delete it is incoherent. Supersedes env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase). | 2026-09-11 | [§5.1.1](#511-why-only-two-surfaces-have-a-host-layer), [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications) | n/a — a ruling to KEEP. The layer stands, restructured by [`OQ-CO10`](#13-decision-ledger) rather than removed |
| [`CO13`](#13-decision-ledger) | **DECIDED — how does a derive say it fills a computed table IN FULL?** ⚠ This cell said OPEN until 2026-09-21, contradicting [its own section](#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided), which records that filing it as a question was the mistake: the four shapes differ in what the CODE carries and not in what the USER gets, so it is a decision the design makes. The last column is the BUILD, which is a different axis (⬜ until 2026-09-25). The 2026-09-20 ruling narrowed the adoption drop to the leaves yolo regenerated, which settles the EMPTY table and leaves the non-empty one guessing. The leaf half of the signal already exists (a computed table's key set IS the asserted set, tombstones included); the fill-in-full half exists nowhere, and four candidate discriminators were measured to flip on configuration rather than intent. It has to be declared by the derive, and the decision is WHERE: a third derive sentinel beside `ctx.tombstone` and `ctx.empty_array`. | 2026-09-20 | [the decision](#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided), [the drop-narrowing ruling](#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule) | ✅ built 2026-09-25 ([what shipped](#built-2026-09-25--what-shipped)): `ctx.in_full` is the third sentinel in `luahook/derive.go`, decoded to `DeriveOutput.InFull`; `dropComputedTables` takes a non-empty table whole only when it is declared, so one not declared in full (claude's `env`) keeps the agent's own leaves (`TestComposeStatefulFirstMigrationKeepsTheUnassertedLeavesOfAPartlyAssertedTable`, the old cost pin inverted; `TestConfigureClaudePrismFirstMigrationKeepsTheAgentsOwnEnv`); `hostTableKeys` keeps only declared keys, so a host apply no longer clears the user's `env` (`TestHostApplyKeepsTheUsersClaudeEnv`). Every shipped table whose key set tracks a live table is declared, and every fixed-key-set one is not (`TestShippedDerivesDeclareTheirInFullTables`, over two probe worlds). ⚠ Version skew is covered in ONE direction: a derive staged by a host `yolo` older than the build declares nothing, so a newer entrypoint adopts MCP tables leaf by leaf and a first migration keeps a removed server; only the `SourceSkew` gate refuses that pairing ([what shipped](#built-2026-09-25--what-shipped)). The EMPTY-table half that landed 2026-09-20 stands (`TestComposeStatefulFirstMigrationKeepsResidueUnderAnEmptyComputedTable`, now declaring both its tables). ⚠ One residual filed rather than built: the jail's `rmw` arm still regenerates every object-valued computed key, declared or not — unobservable for the shipped packs ([the residual](#built-2026-09-25--what-shipped)). ⚠ **Unaffected by [`OQ-CO1`](#13-decision-ledger)'s reversal**, checked rather than assumed: the drop is a `stateful` mechanism and the host probe feeds both `own` arms, so retiring `assert` moves the population and not the scope ([the check](#does-retiring-assert-change-co13s-scope)) |
| [`OQ-CO14`](#13-decision-ledger) | **OPEN — what does retiring `assert` do to a config, and a home, already on it?** Two faces, one migration: a user config that *says* `"assert"` becomes unspellable, and an absent key on a home yolo has already asserted into flips from "keep maintaining this file" to "stop, and leave what was written". The second **re-opens [`OQ-CO2`](#13-decision-ledger)**, whose "neither prompt nor notice" rests entirely on the unset state equalling today's behaviour. It is a question rather than an implementation shape by [§12](#12-follow-ons-and-the-one-this-design-settled-itself)'s own test: refusing, silently ceasing to manage, and silently composing the whole file are three different outcomes for the same user. | — | [the question](#oq-co14), [§4.5](#45-retiring-assert--the-two-value-key) | ⬜ open, and it blocks the value being dropped rather than following it |
| [`OQ-CO15`](#13-decision-ledger) | **OPEN — what does the jail's `rmw` arm do with a table its derive does not declare in full?** `regenerateManagedTables` rewrites every object-valued computed key wholesale, while the host's table probe claims only declared keys, so such a table would be cleared in a jail and merged at the host. No shipped pack returns one. Options: assert its leaves, skip it with a note, or refuse the pack; leaning assert its leaves. | — | [the question](#oq-co15), [what shipped](#built-2026-09-25--what-shipped) | ⬜ open; changes nothing until a pack ships the shape |
| [`OQ-CO16`](#13-decision-ledger) | **OPEN — do the provider catalogs stay declared in full?** Declaring them keeps the wholesale take they had before `CO13`, so a user's hand-added provider is dropped at adoption and at a host apply. Options: keep, stop declaring, or decide per catalog; leaning keep, weakly. | — | [the question](#oq-co16), [what shipped](#built-2026-09-25--what-shipped) | ⬜ open; today's behavior is (a) |
| — | **The adoption archive's layout and failure policy, decided at build time** because [`OQ-CO7`](#13-decision-ledger) left them open and one of them contradicts what that ruling assumed. Keyed by SURFACE, not by the `<stamp>/` generation the other buckets use — under the stamped layout `yolo prune`'s keep-newest-3 would sweep the originals of every surface but the newest few, which is the loss this bucket exists to prevent performed by yolo's own reaper. Idempotent on the archive's own existence, so a second adoption cannot overwrite the user's original with yolo's output. A copy that cannot be written REFUSES the adoption rather than warning past it. Not an OQ; recorded because the first of them departs from [§6.3.3](#633-what-survives-as-a-guard)'s original text. | 2026-09-12 | [§6.3.3](#633-what-survives-as-a-guard) | ✅ `render.Target.ArchivePath` (layout), `entrypoint.archiveAdoption` (idempotency, refusal); `TestPruneLeavesTheAdoptionArchiveAlone` pins the reaper half across the two packages that each know only their own half |
| — | **Terminology: the absent key is the *unset* state, never the "undeclared" one** — *undeclared* is reserved for the input-closure tier ([§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running)'s note). Not an OQ; recorded because renaming it later costs four anchors. | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) | n/a — terminology |

**One consequence worth recording where a reader will hit it, because it moved
work out of this design rather than into it.** Two of the rulings above removed a
mechanism this doc had proposed — a migration prompt ([`OQ-CO2`](#13-decision-ledger)) and an adoption
confirmation ([`OQ-CO3`](#13-decision-ledger)) — and in each case the replacement was already shipped: the
`assert` default, and `ComposeStateful`'s first-migration adoption.
[§10](#10-what-i-would-build-in-order)'s step 1 is smaller for it, and its `own`
step gains the host capture store it now depends on.
