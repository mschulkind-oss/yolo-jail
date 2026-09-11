---
title: "Who owns the config file — declared host management, and the way out of capture"
date: 2026-09-09
status: accepted
tags: [design, config, host, capture, packs, ownership]
summary: "yolo decides who owns an agent's config file by inferring it from the confinement notch, and the inference is wrong for anyone who adopted `yolo host apply`. Declare ownership in the user config instead, make the host render like a jail when it is owned, and build the promotion path that turns a captured in-jail edit into a declared one — the verb a shipped message already advises and nothing implements."
vantage:
  status-chip: true
---

# Who owns the config file — declared host management, and the way out of capture

**Status:** in-review, 2026-09-11. Nothing built. Every claim about current
behavior was verified or measured between 2026-09-09 and 2026-09-11, with its
evidence inline.

> **In short.** yolo infers who owns `~/.claude/settings.json` from the
> confinement notch, and adopting `yolo host apply` is precisely the act of
> leaving the world that inference is sound in — so ownership should be
> **declared** in one user-scope key, and a captured edit should have a verb
> that **promotes** it into a pack.

**Why it matters.** A key typed inside a jail today has exactly one exit:
`yolo config reset`, which discards it. There is no way to carry it to another
jail, to the host, or into anything declared.

**The shape.** One user-scope key (`host_management: none | assert | own`)
selects the host notch's surface mode; `own` makes the host render like a jail;
`yolo config promote` lifts captured keys into the conventional local pack,
which renders at every notch.

**Cost.** It reverses three rulings recorded in
[`environment-manager-plan.md`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
on 2026-08-01 — see [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications).

**Start at [§4](#4-declaring-ownership--the-host_management-key)** — the key is what makes every other question answerable.

**Needs your ruling:** **None** — all eleven questions are settled
([§13](#13-decision-ledger)). Ready to build, in [§10](#10-what-i-would-build-in-order)'s order.
⚠ **One thing to carry out of this doc rather than into it:** four rulings in
[`environment-manager-plan.md`](../plans/environment-manager-plan.md)'s 2026-08-01 ledger are
reversed here, and that ledger needs the dated reversal rows.

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
  Today it comes from `render.Kind` ([`target.go`](../../internal/render/target.go#L86-L104)),
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
  ([`apply.go:818`](../../internal/cli/apply.go#L818)).
- **P5 — The host is a notch like any other, except where a real home forbids
  it.** Same modes, same verbs, same sidecars. The single legitimate asymmetry
  is *deletion* — see [§6.3](#63-the-one-asymmetry-that-survives-deletion). This
  is the parity constraint [`macos-user-home-tiers.md` §5.0](macos-user-home-tiers.md#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend)
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

Ascending precedence, from [`compose.go:352-379`](../../internal/agentcfg/compose.go#L352-L379):

```text
defaults → host → workspace → config-overlay:<pack>… → overlay (capture) → computed → transform → managed
```

`managed` is a floor: a pack's own asserted keys win everything. `overlay` is the
capture-diff sidecar — in-jail edits, carried across regeneration. Note that
`config-overlay` sits **below** capture, which matters in
[§5.4](#54-promotion-moves-a-key-down-the-stack).

### 2.2 The four surface modes

From [`manifest.go:158-171`](../../internal/agentcfg/manifest/manifest.go#L158-L171):

| Mode | What it does | Capture? |
|---|---|---|
| `stateful` | composes the whole file from layers; captures in-jail edits (the default) | yes |
| `computed` | composes the whole file and overwrites every boot, discarding edits | no |
| `rmw` | read-modify-writes an agent-owned file: only declared keys are rewritten | no |
| `unrendered` | yolo does not write the file at all | no |

### 2.3 The notches, and what each keeps on disk

From [`target.go`](../../internal/render/target.go#L243-L296):

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
> ([`hostoverlayprune.go:12`](../../internal/entrypoint/hostoverlayprune.go#L12));
> `hostProvenanceExists` decides `FirstApply`
> ([`hostrender.go:253`](../../internal/entrypoint/hostrender.go#L253));
> `retireUnclaimed` carries attributions forward past a pack drop
> ([`prism.go:946`](../../internal/entrypoint/prism.go#L946)); and
> `yolo config diff` annotates from it
> ([`configdiff.go:349`](../../internal/cli/configdiff.go#L349)). What no reader
> does is the two things this design wants — a revert, and a jail-side filter.

### 2.4 The verbs that exist, and the ones that do not

`yolo config` dispatches `ls, render, diff, reset, capture, drift, dump`
([`config.go`](../../internal/cli/config.go)). There is no `promote`.
`yolo host apply` takes `--assert`, `--dry-run`, `--shell-init`; it is the
ergonomic spelling of `yolo apply --at host`, both ship, and only the first carries
`--shell-init` ([`hostapply.go:17`](../../internal/cli/hostapply.go#L17)).

**There is no `--revert`, and its absence is a ruling rather than a gap.**
[`host-render-target.md`](host-render-target.md) shows one as an example and names
the missing memory as the reason, but env-manager plan
[`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
resolved *no `--revert` on the host target* on 2026-08-01, and
[`apply.go:125`](../../internal/cli/apply.go#L125) records it — *"no --revert —
the resolved [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)–[`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
model"*. [§10](#10-what-i-would-build-in-order) step 3 reverses it deliberately.

**Host-side `capture` and `reset` refuse unless `--force`** (`refuseHostSideWrite`,
[`configdiff.go:84-90`](../../internal/cli/configdiff.go#L84-L90)). **Two reasons,
one guard**, and quoting only the second is easy to do: the docstring calls itself
*"the Phase-0 data-loss guard"* — reset truncates a real dotfile to its often-empty
pure render, capture copies real host config into the workspace sidecar tree, and
*"both are destructive on a file yolo does not own in that context"*
([`configdiff.go:77`](../../internal/cli/configdiff.go#L77)); the user-facing text
says *"could clobber your own config"*. The **privacy** reason — a credential
copied into a workspace sidecar — is
[`host-render-target.md`](host-render-target.md)'s ledger row 9.3, which records
that the same refusal *also* closes the leak path.

### 2.5 The three existing host user-scope keys

`host_files`, `host_wrappers` and `host_apply_on_launch` share one **scope**
construction ([`hostapplyonlaunch.go`](../../internal/config/hostapplyonlaunch.go),
[`inherit.go:205-220`](../../internal/config/inherit.go#L205-L220)): read from the
**user** config rather than the merged config, so workspace scope is
*inexpressible* rather than merely refused, and refused for inheritance into a
nested jail. That construction is the security boundary for any key that licenses
writing the real `$HOME`, and [§4](#4-declaring-ownership--the-host_management-key)'s
new key joins it rather than inventing a fourth shape.

> [!WARNING]
> **The construction is a scope boundary, not a fail direction.** Reading "user
> scope" as "therefore fails closed" is the available mistake, and one shipped key
> does the opposite. The shared helper `UserScopeConfigOrEmpty`
> ([`hostwrappers.go:72`](../../internal/config/hostwrappers.go#L72)) has five
> callers: `agent_updates` deliberately fails **open** through it because it is an
> opt-*out* — *"absent, empty or unreadable means TRUE"*
> ([`agentupdates.go:43`](../../internal/config/agentupdates.go#L43));
> `host_wrappers`, `host_apply_on_launch` and `perf_logging` fail closed; and
> `host_files` does not use the helper at all — it loads user scope strictly and
> returns an *error* ([`hostfiles.go:264`](../../internal/config/hostfiles.go#L264)).
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
> **This design reverses three rulings recorded in another ledger, and that
> collision is LIVE.** The env-manager plan's ledger
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
> A fourth is the subject of a live question rather than a settled reversal:
> **[`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)**
> *retired* the read-in `host` layer — "RESOLVED: YES", never implemented — and
> named the migration this document's own verb provides. That is
> [OQ-CO11](#13-decision-ledger), and it is the first thing to rule.
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
accumulated into the overlay sidecar. Open-ended by construction: a key the jail
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
spurious delta"*, [`staterender.go:247`](../../internal/agentcfg/staterender.go#L247)
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
> and gates a **consent prompt** ([`hostrender.go:98`](../../internal/entrypoint/hostrender.go#L98));
> `firstMigration` is *no trusted `last_render`* and triggers **adoption**
> ([`staterender.go:160`](../../internal/agentcfg/staterender.go#L160)). The
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
  [`inherit.go`](../../internal/config/inherit.go#L205-L220) for the same reason
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
   ([`apply.go:795-825`](../../internal/cli/apply.go#L795-L825)). An environment
   whose host-ownership contract is unstated is not sealed. **This is the one
   place an unset key bites**, and it bites where the user asked a question
   about declaredness rather than where they asked for an apply.

That is the whole migration: a default, and one refusal in the command whose
whole job is to audit what is declared. There is no rewrite of anyone's files at
upgrade, and no host apply behaves differently until its user writes the key.

**Adopting `own` later needs no ceremony either, and that is a property of the
render engine rather than a promise made here** —
[§6.3](#63-the-one-asymmetry-that-survives-deletion) has the mechanism and the
three classes it does not cover.

### 4.4 What the key does not do

It does **not** grant approval for any individual write. That distinction is
`host_apply_on_launch`'s hard-won ruling — "THE KEY ENABLES THE MECHANISM. IT
DOES NOT GRANT THE APPROVAL"
([`hostapplyonlaunch.go:24`](../../internal/config/hostapplyonlaunch.go#L24)) —
and it holds here identically: `host_management: own` selects the mode, it does
not pre-authorize the writes that mode performs. The two keys are orthogonal and
both are read: `host_management` says *how* the host renders,
`host_apply_on_launch` says *when* a re-render is checked.

> [!IMPORTANT]
> **"Does not grant approval" is not "prompts because it is owned".** That is the
> natural second reading and it is wrong: the shipped rule is driven by **what the
> write would do**, never by which key is set, and it has two teeth.
>
> - **Nothing would change ⇒ silent.** The four dispositions
>   ([`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-four-dispositions))
>   put it as an invariant, not a default: *"A freshly-applied home must prompt
>   **not at all, ever**, until something actually changes."*
> - **A change that changes nothing the user has ⇒ still silent.** The host apply
>   gate is `confirmHostLosses` ([`apply.go:577`](../../internal/cli/apply.go#L577),
>   wired on the writing path at [`apply.go:369`](../../internal/cli/apply.go#L369)),
>   and its first stated property is *ONLY WHEN SOMETHING IS ACTUALLY LOST* —
>   gated on `FirstApply && EntryLosses`
>   ([`apply.go:595`](../../internal/cli/apply.go#L595)), so a home yolo has
>   asserted before "prompts not at all". A scalar whose value merely changes is
>   reported as an ordinary `⚠` and does not prompt.
>
> ⚠ Its docstring still says `Overwrites` where the code reads `EntryLosses`
> ([`apply.go:564`](../../internal/cli/apply.go#L564)) — a drift to know before
> quoting it.
>
> So an owned host in steady state is silent, and the prompt that remains is the
> one that was already there for the case that was already dangerous. `own` adds
> no interruption of its own — which is the point, because a confirmation that
> fires on every apply is the mechanism `confirmHostLosses`' own docstring refuses
> to build.

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
([`config/packs.go:274`](../../internal/config/packs.go#L274)) — and
`config-overlay` layers fold in pack order, later wins
([`compose.go:362`](../../internal/agentcfg/compose.go#L362)), so a `local`
promotion outranks every other pack's overlay on the same key.
[§5.4](#54-promotion-moves-a-key-down-the-stack) rests on this (verified
2026-09-11).

### 5.1 Surface

```console
$ yolo config promote <agent> [--surface <name>] [--keys a,b] [--to <dest>]
                              [--plan] [--json] [--answers <file>]
                              [--accept-promotion]
```

| Destination | Written as | Notes |
|---|---|---|
| `local` (default) | a `config-overlay` on the surface, in `~/.config/yolo-jail/local/pack.json` | reaches every jail and the host |
| `pack:<name>` | the same, in that pack's `pack.json` | **refused for a fetched pack** — not the user's file to edit |
| `host` | the keys themselves, into the surface's own real-home file | only under `host_management: assert`; refused under `own` (the file is derived — promote to a pack) and under `none`. **Reaches a jail for only two shipped surfaces** — see below |
| `workspace` | *(nothing to write to)* | **out of scope for this design** ([§13](#13-decision-ledger)) |

Promotion **shows what it is about to write and asks**; `--accept-promotion` is
the only way past the confirmation, and it is the scripting path rather than a
default. `--keys a,b` selects specific keys and omitting it means all of them —
either way the confirmation lists what was selected, so "all" is never silently
wider than the user pictured. `--plan` is the read-only form for looking without
deciding.

`--to workspace` is out of scope **as a decision, not a wait** — but it could not
have been built here in any case, because three separate pieces are missing: the
`workspace` layer has no config key (`agent_config` appears nowhere in
`internal/config`), no producer sets `Inputs.Workspace` anywhere in the tree, and
`render.Host` leaves it empty *by definition* (stated in `prism.go`'s
`targetTransformScript`), so the destination could never reach the host at all.
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
the footprint (`internal/packload/footprint_test.go:74-98`), which is what puts it
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
> // internal/packload/packload.go:440
> func (p *Pack) HonoredHostFiles() (granted []packdecl.HostFile, refused []string) {
>     return p.Decl.HostFileContributions(), nil
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
> ([`hostaccessgates_test.go:82`](../../internal/packload/hostaccessgates_test.go#L82))
> and `TestFetchedPackHostClaimsAreHonoredWithNoApproval`
> ([`packnohostgate_test.go:101`](../../internal/cli/run/packnohostgate_test.go#L101)).
> ⚠ The docstring at [`packload.go:435`](../../internal/packload/packload.go#L435)
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
(`internal/agentcfg/manifest/manifest.go:131-142`).

##### The binding is a basename match, and the read that depends on it is fail-open

The surface already carries the field: `Surface.HasHostLayer()` is *exactly*
`HostSource != ""`, the one predicate the boot render and the host-side `config`
verbs both consult. The `reads-host` contribution does not express that; it
*populates* it, across a seam:

```go
// internal/packload/packload.go:316 — verified 2026-09-10
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
   (`internal/entrypoint/packsurfaces.go:467`) discards the error and composes the
   surface **without** its host layer. **This has already shipped a real bug** —
   `StagedSlug`'s docstring records it, measured 2026-09-05: a pack named `my_pack`
   had its file mounted at `/ctx/host-my_pack/` while the entrypoint read
   `/ctx/host-my_5fpack/`, *"and the host-layer read is fail-open, so the surface
   composed"*.
4. **On `macos-user` it fails on every launch, by construction** (verified
   2026-09-11). That backend has no bind mounts and no `/ctx`; `YOLO_CTX_ROOT`, the
   seam that relocates the read, is set for Apple Container only
   ([`assemble.go:700`](../../internal/cli/run/assemble.go#L700));
   [`runplan.go:262`](../../internal/macosuser/runplan.go#L262) filters the *user's*
   source-bearing `host_files` out as an *"accepted deficiency"*, but a **pack's**
   `reads-host` grant is neither mounted nor filtered — so `hostSurfaceBytes` finds
   nothing and `claude/settings` composes **without the user's settings**, silently,
   on that backend alone. The fail-open docstring names the case and folds it into
   "no such host file": *"or this is macos-user, with no /ctx at all"*
   ([`packsurfaces.go:455`](../../internal/entrypoint/packsurfaces.go#L455)).
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
> footprint (`internal/packload/footprint_test.go:74-76`). Those carry *arbitrary*
> host files into a jail — not a config surface's mirrored twin — so they are
> genuinely not derivable and genuinely need a declared path. A naive "delete the
> kind, put a boolean on the surface" would break them. **The kind stays; what
> could move onto the surface is the config-surface binding.**
>
> And `CombineShared` is not the obstacle it looks like. `reads-host` is
> `Combine: CombineShared` (*"Many packs may read one file; no combine"*,
> `internal/packdecl/kinds.go:87`), which sounds like a reason it must stand apart
> from a `CombineExclusive` surface. It is not: sharing describes how the **mount**
> de-duplicates when two packs want one file, and says nothing about where the
> **declaration** lives.

### 5.2 What it does, in order

1. Read the capture overlay for the surface from `<workspace>/.yolo/prism/`.
2. Drop keys `yolo config diff` already reports as **redundant** — identical to
   what the layers produce anyway — and keys that are **dead**: overridden where
   they already sit, so the move cannot help them. The overlay cannot hold a
   `computed` or `managed` key at all ([§5.4](#54-promotion-moves-a-key-down-the-stack)),
   which leaves one dead class: a key the Lua `transform` rewrites, which
   `narrowOverlay` does not see — its signature takes only the two owner layers
   ([`staterender.go:387`](../../internal/agentcfg/staterender.go#L387)). Redundant
   is free and it is most of the noise: of the six keys captured in this
   development jail on 2026-09-09, four were redundant.
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

### 5.3 Classification — what a machine can decide, and what it cannot

This is the "how automatic can this really be" question, and the honest answer is
*mostly, but not entirely* — so the design puts the mechanical part in the tool
and makes the judgement part answerable three ways.

**Fully mechanical, no judgement:**

- **Redundancy** — already computed by `yolo config diff`.
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
destination it is empty.** Three facts:

1. **The overlay cannot hold a `computed` or `managed` leaf.** `narrowOverlay`
   strips both from the *accumulated* overlay on every boot, on both branches —
   *"ONE RULE, BOTH BRANCHES"*,
   [`staterender.go:252-264`](../../internal/agentcfg/staterender.go#L252-L264) —
   so a key promote reads from the overlay is, by construction, not one those
   layers claim.
2. **A `transform`-overridden key never won at `overlay` either.** The Lua hook
   runs after the whole fold, at both positions, so such a key is *dead* where it
   sits, not *demoted* by the move — [§5.2](#52-what-it-does-in-order) step 2
   drops it as dead before precedence is ever asked.
3. **What is left is another pack's `config-overlay` on the same key, ordered
   later** — and the conventional local pack folds **last**
   ([`config/packs.go:274`](../../internal/config/packs.go#L274)), so for
   `--to local` nothing outranks the promoted key except the overlay it just left.
   The residue is `--to pack:<name>` for a pack ordered before another overlay on
   the same key — which is BACKLOG's bare
   [`OQ-CO`](../plans/BACKLOG.md#-oq-co--two-packs-writing-one-config-overlay-key-is-silent-last-one-wins),
   silent last-one-wins between overlays, seen from the promote side. One latent
   exception: a pack pulled in through `needs` is appended *after* local
   ([`cli/run/packs.go:293`](../../internal/cli/run/packs.go#L293)); no such pack
   declares a `config-overlay` today.

So the precedence check stays — it is a pure function of the layer set, it is
cheap, and it is what protects `pack:<name>`. It is a **two-sided** check (*wins
now* and *wins after*), and a key that fails it is **refused by name with the
losing layer**, never promoted with a warning.

**Promote never offers the pack's `managed` block as an escape**, at any
destination. For every shipped surface it is not expressible: `Surface.Managed` is
populated only from the owning pack's own `config` contribution plus its autonomy
posture ([`packload.go:144-164`](../../internal/packload/packload.go#L144-L164)),
and a `config-overlay` body's field *named* `managed` is *"NOT a claim about the
managed LAYER"* — it folds at the single `config-overlay` slot
([`overlay.go:33`](../../internal/agentcfg/manifest/overlay.go#L33)). So neither
the local pack nor any `pack:<name>` that is not the surface's owner can write a
managed block, and every shipped surface's owner is an embedded pack promote
refuses to edit. For a surface a *user's own* pack declares, the block is writable
by hand and stays that way: a one-keystroke path to outranking `computed` and
`transform` would be used for exactly the reasons those layers exist.

### 5.5 Where promote may run

The capture sidecar is at `<workspace>/.yolo/prism/`, which is a host directory —
so the **host** can read a jail's captures without any channel. The destinations,
however, are host-side files a jail cannot **write**. Stated precisely: the jail
*reads* the local pack as a staged `:ro` copy at `/ctx/packs/local/` like any other
pack ([`assemble.go:645`](../../internal/cli/run/assemble.go#L645)), and the host's
`config.lua` plus a filtered `config.jsonc` snapshot also cross
([`inheritscope.go:51`](../../internal/cli/run/inheritscope.go#L51),
[`:149`](../../internal/cli/run/inheritscope.go#L149)); what no jail can do is write
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
[`prepare.go:509-537`](../../internal/cli/run/prepare.go#L509-L537) is host→jail
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

### 6.2 Host capture, and the privacy ruling it has to answer to

Host capture is currently refused on the grounds that it would copy a credential
out of the real file into a **workspace** sidecar — which crosses into a jail and
plausibly into git. Under `own` that hazard does not arise in the same shape: the
host capture store sits beside the existing host provenance record, at
`<home>/.local/share/yolo-jail/host-capture/`, mode `0600`, and never crosses a
boundary. The key that *exports* anything — promote — refuses sensitive keys
independently ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
The refusal stays under `none` and `assert`.

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
> ([`target.go:226`](../../internal/render/target.go#L226),
> [`prism.go:41`](../../internal/entrypoint/prism.go#L41)) but never adds to any
> `.gitignore` — this repository's own `.gitignore` line 6 does it by hand — and it
> holds a captured API key today ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)).
> So the shipped asymmetry is inverted on its own axis: the notch whose sidecar
> *is* in a workspace and *can* reach git is the one that captures freely. By
> [P5](#1-the-verdict-and-the-principles-it-rests-on) the guard belongs to capture
> at every notch — a capture-time deny-list, a sidecar mode — and the `0600`
> proposed for the host store is a parity gap against the jail's `0644` until both
> move. Neither is this document's to rule; both are listed for the roadmap.

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
them, [`staterender.go:198`](../../internal/agentcfg/staterender.go#L198)) — which
a first `assert` apply adds too — and it drops the classes
[§6.3.2](#632-the-three-classes-adoption-does-not-cover) names, one of which is
**not** empty on a home already on `assert`.

### 6.3.1 Adoption is capture-then-regenerate

`ComposeStateful` treats "no trusted `last_render` for this surface" as a
**first migration** and, on that branch, seeds the overlay from the file it
finds: `residue = mergeDiff(pureRender, current)`
([`staterender.go:174`](../../internal/agentcfg/staterender.go#L174) is the prose,
[`:198`](../../internal/agentcfg/staterender.go#L198) the code). The next line of
the render is then `declared layers + that residue`, which reproduces the file's
undeclared keys exactly. Switching a host surface to `own` is precisely that
branch — there is no host `last_render` yet — so **capture happens before the
first owned write, and the write puts back what capture just took**.

This is not a hopeful reading of the mechanism. That branch exists *because*
seeding an empty overlay was a shipped data-loss bug (`copilot/config` collapsed
to `{"yolo": true}` and logged the user out); the comment above it is labelled
`B1 (⚠ DATA LOSS FIX)` ([`staterender.go:164`](../../internal/agentcfg/staterender.go#L164)).
The engine's answer to "adopt a file I did not write" is already the one a
confirmation prompt would have been built to provide.

> [!WARNING]
> **Do not check this against the file header.** `staterender.go`'s package
> comment, `StatefulInputs.LastRenderPresent`, `StatefulOutput.OverlayJSON` and the
> `ComposeStateful` docstring all still describe the pre-B1 branch — *"seeds a
> truthful baseline with an empty overlay and skips capture"*, *"{} on a first
> migration"*, *"render = Compose(overlay=∅)"* — so a reader who checks the
> docstring finds the opposite of this section. The body at
> [`staterender.go:164-215`](../../internal/agentcfg/staterender.go#L164-L215) is
> the authority (noted 2026-09-11; the docstrings are listed for the roadmap).

**For a home already on `assert` the diff is empty for MOST keys for a second
reason — and not for all.** `rmw` re-asserts the declared keys on every apply, so
a key that collides with yolo's declarations does not survive under `assert`
either. But the two modes disagree about **granularity**, and the disagreement is
a loss path:

- `rmw` **deep-merges** a declared object — `applyRMWLayer` recurses *"so a
  sibling key the agent owns under the same parent survives"*
  ([`prism.go:1093`](../../internal/entrypoint/prism.go#L1093)). A user's
  `permissions.ask` beside yolo's managed `permissions.defaultMode` lives on under
  `assert`. Only object-valued **`computed`** tables are replaced wholesale
  (`regenerateManagedTables`, [`prism.go:980`](../../internal/entrypoint/prism.go#L980)).
- Adoption drops **every top-level key the pure render holds as an object**,
  whole — `dropYoloOwnedSubtrees`
  ([`staterender.go:320-338`](../../internal/agentcfg/staterender.go#L320-L338)) —
  and the comment above the call says why that reaches managed: *"a managed
  object key is always one of those because Enforce puts it there"*
  ([`:204`](../../internal/agentcfg/staterender.go#L204)). So at the first owned
  render `permissions.ask` — and every other leaf under `permissions`, `env` or
  any declared object that yolo does not itself pin — is **not adopted**, and the
  render omits it.

> [!WARNING]
> **`dropYoloOwnedSubtrees` is coarser than `dropOverriddenKeys`, and the engine
> already knows the coarse rule is wrong.** Three functions further down, the
> steady-state rule says a blanket top-level drop *"would be simpler and wrong — it
> would discard the agent's permission list on every boot"*
> ([`staterender.go:410-414`](../../internal/agentcfg/staterender.go#L410-L414)).
> Two rules in one file disagree, and adoption took the wrong one.
>
> **This is a shipped defect, not only a design hole.** It loses a leaf silently at
> `assert → own`, and in **any jail that loses its `last_render`** — B1's own
> trigger — because that is the same branch. Nothing catches it: `FirstApply` is
> *false* once a provenance record exists
> ([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)), and
> `EntryLosses` is defined over table entries.

**The design therefore requires adoption to drop at the granularity `rmw` writes
at**: leaf-level against deep-merged owners (`managed`, `defaults`,
`config-overlay`), wholesale only against `computed` tables — which is exactly
`dropOverriddenKeys`' existing rule applied to the pure render. Until that lands,
`own` is not zero-bytes on an `assert` home, and
[§11](#11-success-criteria)'s *"zero bytes"* criterion fails for every user with
such a leaf.

### 6.3.2 The three classes adoption does not cover

Stated because "empty by construction" is a claim with edges, and each edge is a
deliberate line in the engine rather than an oversight:

| Class | What happens | Why |
| :--- | :--- | :--- |
| A key nested inside **any** top-level object the pure render holds — a `computed` table like `mcpServers`, *and* a deep-merged `managed`/`defaults` object like `permissions` | **not adopted** | `dropYoloOwnedSubtrees` ([`staterender.go:338`](../../internal/agentcfg/staterender.go#L338)) — right for the table (adopting would resurrect a dropped entry and break *regenerate, don't reconcile*); **wrong for the deep-merged object**, where `rmw` would have kept the leaf ([§6.3.1](#631-adoption-is-capture-then-regenerate)) |
| A key a higher layer re-asserts — `managed`, or the per-boot `computed` layer | **never captured, in either branch** | `narrowOverlay` — both fold above the capture overlay and win unconditionally, so a captured copy could only sit in the sidecar as noise `yolo config diff` would report as a phantom edit |
| A **keyless** surface (`raw`, `lines`) | **not adopted at all** | one "key" is the whole file, so adoption would mean "the file wins outright", freezing a host-mirrored file at stale content forever ([`staterender.go`](../../internal/agentcfg/staterender.go)) |

Three things about that table are worth having in front of you before designing
against it.

**Row 1's deep-merged half is the shipped defect**
[§6.3.1](#631-adoption-is-capture-then-regenerate) names, and `confirmHostLosses`
gates rows 1 and 2 **only on a first apply** (`FirstApply && EntryLosses`,
[§4.4](#44-what-the-key-does-not-do)) — so on a home already on `assert` the prompt
does not fire at the exact transition that loses the leaf. The fix is to narrow the
drop so there is nothing for a guard to catch; until then the archive
([§6.3.3](#633-what-survives-as-a-guard)) is the only net.

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
   (`internal/entrypoint/surfacecodec.go:78-95`) refuses keyless codecs **by
   kind**: *"RMW asserts and fills individual keys, so it needs an object […] Those
   surfaces belong in `stateful`/`computed`."* So `assert` is the mode a keyless
   surface cannot have, and the rule is already uniform across notches — a keyless
   surface renders `stateful` or `computed` everywhere, and adoption never applies
   to it anywhere.
2. **Adopting one is strictly worse than overwriting it.** Adoption makes **the
   whole file** the overlay, and the overlay outranks every declared layer beneath
   it — so the file would freeze at its adopted content and yolo's declarations
   would never take effect again (`staterender.go:213` says exactly this about the
   jail, and relocating it to the host does not repair it).
3. **The class is empty today and only a pack can populate it.** `host_management`
   governs the **host notch**, and `RenderHostPack` walks a pack's *own* surfaces
   (`internal/entrypoint/hostrender.go:175`). A user's `host_files` entries are not
   pack surfaces — they are a user config key rendered **in the jail**
   (`internal/entrypoint/hostfiles.go`), yolo never writes the host's copy, and so
   there is no host-notch ownership question for them at all. `raw` is reachable
   (it is the default codec for any `host_files` entry whose destination is not
   `.json`/`.toml`, `hostFileCodecFor`, `internal/config/hostfiles.go:205-211`),
   but jail-side delivery is its job. No shipped pack declares a `raw`/`lines`
   surface.

What to do about row 3 is [OQ-CO9](#13-decision-ledger).

### 6.3.3 What survives as a guard

**One archive at adoption.** The pre-existing file is copied once to the state dir
before the first owned render — and, by [P5](#1-the-verdict-and-the-principles-it-rests-on),
at a jail's `firstMigration` too, into the workspace's own `.yolo/` tree. Not
per-apply snapshots: those are a different feature with a retention policy. The
later-regression case — a bad pack update deleting a key months afterwards — is
what having the pack in git is for, which is the whole arrangement `own` is
recommending.

**It is cheaper than it looks, and it covers a known loss rather than a
speculative one.** The archive root, the one-generation-per-apply layout and
`yolo prune`'s reclamation already ship for skills, files and briefings
(`~/.local/share/yolo-jail/archive/<bucket>/<stamp>/`,
[`applyhostskills.go:76`](../../internal/cli/applyhostskills.go#L76)); a config
bucket is a new *bucket*, not a new *surface*. And the loss it covers is
[§6.3.1](#631-adoption-is-capture-then-regenerate)'s deep-merged leaf, which the
prompt is structurally blind to.

> [!WARNING]
> **The one-way-door gate cannot see the most destructive case available.**
> `confirmHostLosses` reads `EntryLosses`, defined as *"the NAMED-ENTRY casualties
> of this render: an entry in a table like `mcpServers`"*
> (`internal/entrypoint/hostrender.go:84-97`). A **keyless surface has no tables
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
exactly that prompt ([`apply.go:38`](../../internal/cli/apply.go#L38)), but a
jail's `firstMigration` runs at an unattended boot — in-jail `yolo apply` is a
report, not a provision ([`apply.go:111`](../../internal/cli/apply.go#L111)). So
the RULE is uniform and the NET splits by the primitive available: a prompt where
there is a TTY, the archive where there is not. That is what makes the archive
load-bearing rather than optional.

**One load-bearing dependency, named because it is easy to drop.** Adoption is
safe against `yolo config reset` *only* because reset also truncates the surface
to its pure render — without that, reset → no baseline → adopt would resurrect
the very edits the user asked to discard, making reset a no-op. The engine
comment says the two halves are one change. Host-side `reset` currently refuses
([§2.4](#24-the-verbs-that-exist-and-the-ones-that-do-not)), so **`own` must
make host-side `reset` work, not merely permit it** — [§6.1](#61-mode-parity)
lists it as parity, and it is a correctness requirement.

After adoption, a removal is ordinary and reported, exactly as it is in a jail.

---

## 7. What this does not propose

- **No change to the jail's precedence stack.** `defaults → host → workspace →
  config-overlay → capture → computed → transform → managed` stays as it is.
  [§5.4](#54-promotion-moves-a-key-down-the-stack) works *around* it deliberately
  rather than reordering it.
- **No sealing-by-default.** Whether `apply --sealed` is a flag or the default is
  `Q1a` in [`environment-manager-user-stories.md`](environment-manager-user-stories.md#open-questions)
  and stays there. This doc only adds one more thing `--sealed` refuses.
- **Packs are not made mandatory.** `host_management: assert` — hand-edit the
  host file, no pack — remains fully supported and is the right answer for a
  single machine.
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
[`migrating-to-packs-and-host-management.md`](../guides/migrating-to-packs-and-host-management.md)'s
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
| A user on `assert` switches to `own` and silently loses a leaf under a managed object (`permissions.ask`) | Adoption must drop at `rmw`'s granularity ([§6.3.1](#631-adoption-is-capture-then-regenerate)); until it does, the archive is the only net, because `confirmHostLosses` is off once provenance exists |
| Promotion silently demotes a key that then reverts | Precedence check is a **refusal**, not a warning ([§5.4](#54-promotion-moves-a-key-down-the-stack)) — and the class is empty for `--to local`; the check protects `--to pack:<name>` |
| A credential is promoted into a pack, and the pack is pushed — or already sits in a sidecar | Promote's deny-list ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot), new work); `--force` is per-key and named in output; the sidecar half is a capture-time question at every notch ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)), listed for the roadmap |
| The local pack becomes an unreviewable pile of promoted keys | Every promotion is an ordinary edit to a readable `pack.json`; `yolo pack lint` and `footprint` already report its claims |
| Three-value enum confuses users who wanted a switch | Explained in `yolo config-ref` and at the point of the act (`none` names the key that made it write nothing). Accepted: the value a confused user lands on is `assert`, which is what they already have |
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
6. **`own`:** the host notch renders `stateful`, **with the host capture store
   ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)) and
   host-side `reset` in the same commit** — adoption is unsafe without either
   ([§6.3.3](#633-what-survives-as-a-guard)) — plus the one-time archive, the
   keyless carve-out [OQ-CO9](#13-decision-ledger) rules, and **adoption narrowed to `rmw`'s
   granularity** ([§6.3.1](#631-adoption-is-capture-then-regenerate)), without
   which `own` is not zero-bytes on an `assert` home. Last because it is the only
   step that can lose data, and by then promotion exists, which is what makes
   `own` attractive rather than merely strict. ⚠ Reverses env-manager plan
   [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   and un-moots [`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   ([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)); record
   both there.

Steps 1–3 are worth doing even if [§5](#5-promotion--the-way-out-of-capture) is
never built; step 4 is worth doing even if step 5 is not.

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
- Switching to `own` on a home already applying under `assert` changes **zero
  bytes** — the measurable form of
  [§6.3.1](#631-adoption-is-capture-then-regenerate), and the criterion to write
  the test for first. The shipped adoption rule does **not** satisfy it for a leaf
  under a managed object, so the first test to write is the one with
  `permissions.ask` in the file. Switching on a home that never applied loses only
  what a first `assert` apply would have lost, prompts for it through the gate
  that already exists, and leaves the pre-existing file recoverable from the
  archive.
- A `yolo host apply --assert` under `assert` still leaves an undeclared key
  byte-identical — the property measured on 2026-09-09 and the one thing this
  design must not regress.

---

## 12. Open Questions

> [!NOTE]
> **`OQ-CO<n>` here is not BACKLOG's bare `OQ-CO`**
> ([two packs writing one `config-overlay` key](../plans/BACKLOG.md#-oq-co--two-packs-writing-one-config-overlay-key-is-silent-last-one-wins)).
> The prefixes collided by accident and both stay, because ids are an API; a grep
> for `OQ-CO` returns both, so read the digit. The two are not unrelated —
> BACKLOG's is [§5.4](#54-promotion-moves-a-key-down-the-stack)'s residual
> precedence case seen from the pack side.

Three live questions. [OQ-CO11](#13-decision-ledger) is upstream of
[OQ-CO10](#13-decision-ledger): if the 2026-08-01 ruling stands, CO10 dissolves for config
surfaces rather than being answered. Settled questions are in
[§13](#13-decision-ledger).
**None — every question this design opened is settled.** The last three were ruled on
2026-09-11 and are in [§13](#13-decision-ledger); the rulings themselves live in the
sections they govern.

**One consequence is large enough to state here rather than leave in a ledger row.**
[`OQ-CO10`](#13-decision-ledger) decided the *shape* of the read-in `host` layer — the
declaration moves onto the surface, the read fails closed, coverage becomes a visible
per-surface yes/no. **Deciding a mechanism's shape decides that it exists**, so the
2026-08-01 ruling to retire that layer
([`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase))
is **superseded**, and it is one of four from that ledger this design reverses —
[§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications) names them all with
their dated reversals.

---


## 13. Decision Ledger

Rulings on [§12](#12-open-questions)'s questions are folded into the normative
body text and compacted here, keeping the exact `OQ-CO` ids so citations from
sibling docs and code comments continue to resolve. **All eleven are settled**;
the last three on 2026-09-11.

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

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| [`OQ-CO1`](#13-decision-ledger) | **Three values** (`none`/`assert`/`own`). `assert` is shipped behavior with real users, so collapsing it is either a regression or a forced escalation to `own`. The cost — one more thing to explain — is paid down by [`OQ-CO2`](#13-decision-ledger) removing the prompt that would have explained it. Alternative C in [§8](#8-alternatives-considered) resolves as rejected. | 2026-09-10 | [§4.1](#41-the-key) |
| [`OQ-CO2`](#13-decision-ledger) | **Neither prompt nor notice** — the unset state is `assert`, silently. Each value explains itself at the point of the act; `apply --sealed` is the one place an unset key bites. *Against the leaning:* the ambiguity this design fixes was **inference**, not silence, so a documented default that equals today's behavior is declared in the only sense that matters. | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) |
| [`OQ-CO3`](#13-decision-ledger) | **Yes, host-side capture under `own` only** — and it is a precondition of adoption, not an added capability: capture-then-regenerate is what makes the first owned render reproduce the file. The refusal stays for `none` and `assert`. Reverses env-manager plan [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)/[`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) — see [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications). | 2026-09-10 | [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to), [§6.3.1](#631-adoption-is-capture-then-regenerate) |
| [`OQ-CO4`](#13-decision-ledger) | **Keep `local` as the default; the guard is the confirmation, not the flag.** Promote lists the selected keys and asks; `--accept-promotion` is the only way past it. **Not `--yes`** — this repo has already declined the generic form: `config.AcceptConfigChangesFlag` (`internal/config/snapshot.go:81`) is `--accept-config-changes`, and its docstring rules that an approval must be a flag naming what is approved, never an env var a child process inherits. `--force` stays free for overriding a *refusal*. | 2026-09-10 | [§5.1](#51-surface), [§5.7](#57-forbidden-behavior) |
| [`OQ-CO5`](#13-decision-ledger) | **Refuse-with-instructions in v1.** Design the jail→host request channel but do not build it until promote has been used enough to know which keys people actually promote — the transport is free, the consent prompt is what needs the evidence. | 2026-09-11 | [§5.5](#55-where-promote-may-run) |
| [`OQ-CO6`](#13-decision-ledger) | **Refuse only, everywhere** — promote never offers the pack's `managed` block to a key that would lose precedence. For shipped surfaces nothing else is expressible (`managed` is owner-only and every owner is an embedded pack). For a user's own surface the friction is the point: a one-keystroke path to outranking `computed`/`transform` would be used for exactly the reasons those layers exist, so the managed block stays a hand edit. | 2026-09-11 | [§5.4](#54-promotion-moves-a-key-down-the-stack) |
| [`OQ-CO7`](#13-decision-ledger) | **One archive at adoption**, as a `config` bucket in the archive subsystem that already ships — and, by [P5](#1-the-verdict-and-the-principles-it-rests-on), at a jail's `firstMigration` too. It covers a KNOWN loss path (the deep-merged-leaf drop) the prompt cannot see; the later-regression case is what git on the pack is for. Not per-apply snapshots. | 2026-09-11 | [§6.3.3](#633-what-survives-as-a-guard) |
| [`OQ-CO8`](#13-decision-ledger) | **`--to workspace` is out of scope for this design** — a decision, not a wait. It could not have been built here regardless: the `workspace` layer has no config key, no producer sets `Inputs.Workspace`, and `render.Host` leaves it empty by definition. Whoever wires that layer also owns the argument that a jail-writable layer must not reach a real home. | 2026-09-11 | [§5.1](#51-surface), [§7](#7-what-this-does-not-propose) |
| — | **Terminology: the absent key is the *unset* state, never the "undeclared" one** — *undeclared* is reserved for the input-closure tier ([§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running)'s note). Not an OQ; recorded because renaming it later costs four anchors. | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) |

**One consequence worth recording where a reader will hit it, because it moved
work out of this design rather than into it.** Two of the rulings above removed a
mechanism this doc had proposed — a migration prompt ([`OQ-CO2`](#13-decision-ledger)) and an adoption
confirmation ([`OQ-CO3`](#13-decision-ledger)) — and in each case the replacement was already shipped: the
`assert` default, and `ComposeStateful`'s first-migration adoption.
[§10](#10-what-i-would-build-in-order)'s step 1 is smaller for it, and step 6
gains the host capture store it now depends on.
| [OQ-CO9](#12-open-questions) | **Refuse `own` for a keyless surface, until a real example exists.** The guard-growing alternative was the author's leaning, not something evidence forced, and the class is empty today — so the cheap answer is the honest one. Revisit when a pack has a reason to want a keyless surface host-rendered. | 2026-09-11 | [§6.3.2](#632-the-three-classes-adoption-does-not-cover) |
| [OQ-CO10](#12-open-questions) | **The declaration moves ONTO the surface** so the binding is structural instead of a `path.Base` match, and **the read fails CLOSED** — which turns the `macos-user` silent drop into a refusal that names the backend. The disclosure survives (it comes from the declaration being present and enumerable, not from a separate kind), and the `reads-host` kind stays for `host_files`, whose entries have no mirrored twin. Coverage becomes a visible per-surface yes/no, making `mise/config` a deliberate **no**. Promote refuses `--to host` on a surface with no host layer. | 2026-09-11 | [§5.1.1](#511-why-only-two-surfaces-have-a-host-layer) |
| [OQ-CO11](#12-open-questions) | **The read-in `host` layer stays — decided by [`OQ-CO10`](#13-decision-ledger), not separately.** Ruling a mechanism's binding, failure direction and coverage decides that it exists; asking in the same breath whether to delete it is incoherent. Supersedes env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase). | 2026-09-11 | [§12](#12-open-questions) |
