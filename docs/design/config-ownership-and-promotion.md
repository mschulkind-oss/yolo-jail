---
title: "Who owns the config file — declared host management, and the way out of capture"
date: 2026-09-09
status: in-review
tags: [design, config, host, capture, packs, ownership]
summary: "yolo decides who owns an agent's config file by inferring it from the confinement notch, and the inference is wrong for anyone who adopted `yolo host apply`. Declare ownership in the user config instead, make the host render like a jail when it is owned, and build the promotion path that turns a captured in-jail edit into a declared one — the verb three shipped messages already advise and nothing implements."
vantage:
  status-chip: true
---

# Who owns the config file — declared host management, and the way out of capture

**Status:** in-review, 2026-09-11 — **review round 0 folded in 2026-09-10**
([OQ-CO2](#OQ-CO2) and [OQ-CO3](#OQ-CO3) ruled, [OQ-CO9](#OQ-CO9) opened), and an
**adversarial audit folded in 2026-09-11** (postscript below). Nothing built.
Every claim about current behavior was verified against the tree or measured in
this development jail on 2026-09-09, the round-0 claims about `ComposeStateful`
on 2026-09-10, and every `file:line` re-opened on 2026-09-11; each carries its
evidence inline.

> [!WARNING]
> **Audit postscript, 2026-09-11 — what the tree overturned.** Every item below
> was already in the tree or in this document; none needed new research. The body
> is corrected in place, each correction marked ⚠ with its citation and date.
>
> - **This design reverses THREE recorded rulings and had named none of them** —
>   env-manager plan [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (no host `--revert`), [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (pure `rmw` on the host;
>   whole-file compose + capture *rejected*) and [`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (host capture overlay,
>   ruled MOOT on the strength of [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)), all 2026-08-01.
>   [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications) now says so.
> - **[OQ-CO11](#OQ-CO11) re-asked a question already ruled**: env-manager plan
>   [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (2026-08-01) *retired* the read-in `host` layer — "RESOLVED: YES", never
>   implemented — and named the migration this document's own verb provides.
> - **[§6.3.1](#631-why-the-adoption-diff-is-empty)'s "empty for a home already on
>   `assert`" is false for a leaf under a managed object**: `rmw` deep-merges,
>   adoption drops the whole subtree, and the gate that should notice is off once
>   provenance exists. A known, silent loss path at the exact transition `own`
>   asks users to make.
> - **[§5.4](#54-promotion-moves-a-key-down-the-stack)'s "real defect class" is
>   empty for the default destination** since 2026-09-10: the overlay cannot hold a
>   `computed`/`managed` key, and the local pack folds last.
> - **The host provenance record has four consumers**, one of which deletes keys
>   on its authority — "recorded and unused" was false.
> - Smaller: one shipped "promote" message, not three; `refuseHostSideWrite` guards
>   data loss, not privacy; the migration-guide fix is `27d21a4f`, not a commit this
>   repo has; the archive [OQ-CO7](#OQ-CO7) prices is a bucket in a subsystem that
>   already ships; the `host` layer silently drops on `macos-user`.

> [!NOTE]
> **Review round 0 made this design smaller in two places, both by finding the
> mechanism already shipped.** The migration prompt is gone — the `assert`
> default is the whole migration — and the adoption confirmation is gone,
> because `stateful` adoption already seeds the overlay from the file it finds
> ([§6.3.1](#631-why-the-adoption-diff-is-empty)). What that surfaced instead is
> a real gap the confirmation had been hiding: keyless surfaces are never
> adopted, which is safe in a jail and not on a host ([OQ-CO9](#OQ-CO9)).

**The short version.** yolo never asks who owns `~/.claude/settings.json` — it
infers the answer from the confinement notch, giving the jail whole-file
composition plus a capture overlay and the host a keys-only read-modify-write.
That inference is sound only while the host file is the human's and yolo is a
guest, and adopting `yolo host apply` is precisely the act of leaving that world.
The fix is three moves: **declare** ownership with one user-scope key
(`host_management`) instead of deriving it; **render the owned host like a
jail**, same modes and same sidecars, with one deliberate carve-out for deletion;
and **build `yolo config promote`**, so a captured in-jail edit has a path into a
pack — which is the same path that carries it to every other jail and to the
host, because the conventional local pack renders at every notch.

**The most important section** is [§4](#4-declaring-ownership--the-host_management-key),
which is the part that makes every other question answerable; [§5](#5-promotion--the-way-out-of-capture)
is the part with the most unbuilt surface.

**Needs your ruling:** [OQ-CO11](#OQ-CO11) first — it is upstream of
[OQ-CO10](#OQ-CO10) and carries a 2026-08-01 ruling of yours to confirm or
reverse — then [OQ-CO5](#OQ-CO5), [OQ-CO6](#OQ-CO6), [OQ-CO7](#OQ-CO7),
[OQ-CO9](#OQ-CO9).

**Reads with:** [`host-render-target.md`](host-render-target.md) (the doc whose
[§6.3](host-render-target.md#63-the-structural-problem-on-a-host-target-the-host-layer-is-the-output) ruling this proposes to re-scope, and whose `Target`/notch vocabulary this
builds on), [`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md)
(the layer stack and the capture overlay, as designed),
[`environment-manager-user-stories.md`](environment-manager-user-stories.md)
(story 1 is this problem told from the user's side; its `Q1` is the question this
doc tries to close), [`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md)
(Phase 5.3 is the unbuilt half),
[`../guides/migrating-to-packs-and-host-management.md`](../guides/migrating-to-packs-and-host-management.md)
(the user-facing guide, corrected 2026-09-09 in `27d21a4f` after it claimed the
host layer no longer existed — ⚠ this document cited `a23b1fea`, a commit this
repository does not have, until 2026-09-11).

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
  ([`apply.go:818`](../../internal/cli/apply.go#L818)) — ⚠ this document said
  *three* until the 2026-09-11 audit counted: the only other `promote` in a
  user-facing string is [`briefing.go:470`](../../internal/jailcontent/briefing.go#L470),
  and it is about skills. The other five hits are Go comments.
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

Every row here was checked on 2026-09-09.

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

⚠ **It is not unconsumed** — corrected 2026-09-11; this section and two others
said *"nothing consumes it"*. Four readers exist: `PruneHostOverlayKeys` deletes
keys from the user's real file on its authority — *"THE PROVENANCE RECORD IS THE
AUTHORITY, and it has to be"* ([`hostoverlayprune.go:12`](../../internal/entrypoint/hostoverlayprune.go#L12));
`hostProvenanceExists` decides `FirstApply` ([`hostrender.go:253`](../../internal/entrypoint/hostrender.go#L253));
`retireUnclaimed` carries attributions forward past a pack drop
([`prism.go:946`](../../internal/entrypoint/prism.go#L946)); and `yolo config diff`
annotates from it ([`configdiff.go:349`](../../internal/cli/configdiff.go#L349)).
What no reader does is the two things this design wants — a revert, and a
jail-side filter — so the claim survives only in that narrower form.

### 2.4 The verbs that exist, and the ones that do not

`yolo config` dispatches `ls, render, diff, reset, capture, drift, dump`
([`config.go`](../../internal/cli/config.go)). There is no `promote`.
`yolo host apply` takes `--assert`, `--dry-run`, `--shell-init`; it is the
ergonomic spelling of `yolo apply --at host`, both ship, and only the first carries
`--shell-init` ([`hostapply.go:17`](../../internal/cli/hostapply.go#L17)). There is
no `--revert`, though [`host-render-target.md`](host-render-target.md) shows one as
an example and names the missing memory as the reason. ⚠ **Its absence is a
ruling, not a gap** (found 2026-09-11): env-manager plan
[`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
resolved *no `--revert` on the host target* (2026-08-01), and
[`apply.go:125`](../../internal/cli/apply.go#L125) records it — *"no --revert —
the resolved [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)–[`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) model"*. [§10](#10-what-i-would-build-in-order) step 3
reverses that ruling and now says so.

Host-side `capture` and `reset` **refuse** unless `--force`
(`refuseHostSideWrite`, [`configdiff.go:84-90`](../../internal/cli/configdiff.go#L84-L90)).
⚠ **On data-loss grounds, not privacy** — corrected 2026-09-11. The guard's own
docstring calls itself *"the Phase-0 data-loss guard"*: reset truncates a real
dotfile to its often-empty pure render, capture copies real host config into the
workspace sidecar tree, and *"both are destructive on a file yolo does not own in
that context"* ([`configdiff.go:77`](../../internal/cli/configdiff.go#L77)); the
user-facing text says *"could clobber your own config"*. The privacy ruling — a
credential copied into a workspace sidecar — is
[`host-render-target.md`](host-render-target.md)'s ledger row 9.3, which records
that the same refusal *also* closes the leak path. Two reasons, one guard; this
document cited only the second.

### 2.5 The three existing host user-scope keys

`host_files`, `host_wrappers` and `host_apply_on_launch` share one **scope**
construction ([`hostapplyonlaunch.go`](../../internal/config/hostapplyonlaunch.go),
[`inherit.go:205-220`](../../internal/config/inherit.go#L205-L220)): read from the
**user** config rather than the merged config, so workspace scope is
*inexpressible* rather than merely refused, and refused for inheritance into a
nested jail. That construction is the security boundary for any key that licenses
writing the real `$HOME`, and [§4](#4-declaring-ownership--the-host_management-key)'s
new key joins it rather than inventing a fourth shape.

⚠ **Two corrections from the 2026-09-11 audit.** The construction is a scope
boundary, *not* a fail direction: the shared helper `UserScopeConfigOrEmpty`
([`hostwrappers.go:72`](../../internal/config/hostwrappers.go#L72)) has **five**
callers, and `agent_updates` deliberately fails **open** through it because it is
an opt-*out* — *"absent, empty or unreadable means TRUE"*
([`agentupdates.go:43`](../../internal/config/agentupdates.go#L43)); `host_wrappers`,
`host_apply_on_launch` and `perf_logging` fail closed; and `host_files` does not use
the helper at all — it loads user scope strictly and returns an *error*
([`hostfiles.go:264`](../../internal/config/hostfiles.go#L264)). So "fail closed" is
a per-key choice each key states, which is how [§4.2](#42-scope-defaults-and-failure-direction)
states it for the new key, not a property it inherits.

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
> **This design reverses three recorded rulings, and said so for none of them
> until 2026-09-11.** The env-manager plan's ledger
> ([`environment-manager-plan.md`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase),
> all dated 2026-08-01) holds: **[`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — *"On the host notch, `rmw` or
> whole-file compose? → RESOLVED: pure `rmw`"*, with whole-file `stateful`+capture
> **rejected** as *"capture buys nothing — it is solving a problem `rmw` does not
> have"*; **[`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — where a host capture overlay lives — *"MOOT. It only
> existed if [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) chose capture"*; and **[`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** — *"Is there a `--revert` verb on
> the host target? → RESOLVED: NO."* `own` is [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)'s rejected option,
> [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)'s store is
> [`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)'s answer, and [§10](#10-what-i-would-build-in-order) step 3 is [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)'s verb.
>
> The reversal is defensible on grounds [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) never weighed — its argument was that
> `rmw` *protects the agent's keys*, which it does;
> [§3.1](#31-first-what-actually-differs-between-rmw-and-capture)'s is that `rmw`
> cannot be *regenerated*, which no amount of key-protection buys — and [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)'s own
> text leaned on [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) (*"esp. with [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) retiring the read-in layer"*), which was
> never implemented. But a reversal has to be seen, and a ruling that lands here
> must be written back into that ledger in the same commit.

### 3.1 First: what actually differs between `rmw` and capture

The two look interchangeable, and in **steady state on the happy path they produce
the same bytes** — which is why the quote above can describe capture as merely
making composition "non-destructive". Being precise matters, because the rest of
this section leans on the difference and [§5](#5-promotion--the-way-out-of-capture) is unbuildable without it.

**Capture is not a fixed set of keys.** It is `mergeDiff(previous render, current
on-disk)` ([`engine.go`](../../internal/agentcfg/engine.go)) — the minimal RFC-7386 patch that turns the
render yolo last produced into the file as it now stands, accumulated into the
overlay sidecar. Open-ended by construction: a key the jail adds is recorded as
its new subtree, a leaf that changed as its new value, and **a key the jail
deleted as an explicit null tombstone** so the next render does not resurrect it.

**So "a key no layer declares" needs saying precisely**, because the captured
overlay *is* one of the layers. A surface's render is composed from `defaults`,
each pack's contributions, `managed`, the computed layer, the host layer where
there is one, and the captured overlay. A key is deleted on the next render only
when it is in the real file and in **none of those** — which is a narrower and
more interesting set than "anything the user typed":

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
   [§5](#5-promotion--the-way-out-of-capture) is *"the way out of capture"* rather than "the way out of editing".

> [!NOTE]
> **`rmw`'s inability to express removal is a fixable limitation, not a
> distinction** — corrected in review, and worth recording because the first draft
> of this section had it backwards. `rmw` cannot say "this key should stop
> existing", which is defect **(1)** below; but nothing about the mode forbids
> giving it a tombstone list, and `retired:<layer>` is already the beginning of
> one. So it belongs in the evidence that the *current* asymmetry is incoherent —
> not in the argument that the two mechanisms are different in kind.

> [!IMPORTANT]
> **The convergence is real but one-directional, and that is the trap.** Any file
> compose+capture can produce, `rmw` can also hold — so two machines in steady
> state look identical and the mechanisms look redundant. What differs is the
> operations they *permit*, and that only shows when something changes: a pack is
> dropped, a file is deleted, an edit is promoted. Judging these two by the file
> they leave behind judges them by the one case that cannot tell them apart.

Four pieces of evidence that the resulting ambiguity is structural, not felt:

**(1) Provenance laundering already required a fix.** From
[`compose.go`](../../internal/agentcfg/compose.go)'s `RetiredLayer` note: the rmw
notch derives `host` for every key already in the file, then upgrades the ones a
live layer claims — so drop the pack that contributed a key and the key *yolo
wrote* reads back as `host`, "the user set this," self-reinforcing forever. A new
provenance token (`retired:<layer>`) had to be invented because **`rmw` cannot
distinguish yolo's leftovers from the user's keys**. That is the ownership
question failing to have an answer, in shipped code.

**(2) The data for the better design is already recorded and unused** —
[§2.3](#23-the-notches-and-what-each-keeps-on-disk).

**(3) There is no `--revert`,** though the design doc treats it as wanted and
names the missing memory as the blocker. That memory now exists.

**(4) Host capture refuses for two *further* reasons, neither of which is "it
would be meaningless here"** — the guard's docstring says *data loss*
([`configdiff.go:77`](../../internal/cli/configdiff.go#L77), *"clobber your own
config"*) and the design ledger says *privacy* (a credential into a workspace
sidecar, [`host-render-target.md`](host-render-target.md) row 9.3). ⚠ Corrected
2026-09-11 — this item named only privacy. Four justifications for one asymmetry,
and they do not compose into a story.

> [!IMPORTANT]
> **What survives the critique — and it is NOT "the jail home is disposable".**
> ⚠ **Reframed in review 2026-09-11; the earlier version of this callout had the
> axis wrong**, and the correction matters because the wrong axis makes the host
> look permanently special when it is only *temporarily* so.
>
> **The axis is OWNERSHIP, not the notch.** An **owned** file is derived output:
> delete it and the next render reproduces it exactly — [§3.1](#31-first-what-actually-differs-between-rmw-and-capture)'s
> own table already says so (*"Delete the file | it is gone; nothing can rebuild
> it | the next boot reproduces it exactly"*). So an owned file is disposable
> **wherever it lives, host included**. What is not disposable is a file holding
> bytes that exist nowhere else — which is every file yolo does not yet own.
>
> **So the host's specialness is a TRANSITION, not a property.** Before adoption a
> host file is the user's and irreplaceable; after adoption it is derived and
> reproducible. The dangerous moment is the crossing, not the address.
>
> ⚠ **And jails have that crossing too — this is measured, not theoretical.** A
> jail starts empty, so the common case has nothing to lose. But enable a pack that
> starts owning a file the home already has and you are in the same transition:
> that is `ComposeStateful`'s `firstMigration` branch, and **the B1 data-loss fix
> exists because a jail lost real state to it** — `copilot/config` collapsed to
> `{"yolo": true}` and logged the user out, destroying `copilot_tokens` that lived
> only in that jail home ([§6.3.1](#631-why-the-adoption-diff-is-empty)). A home
> that holds an agent's auth tokens is not disposable, whatever notch it is on.
>
> **What this changes.** The guard belongs on the **transition** — first render into
> a home that already has content — which is what `FirstApply` already keys on
> host-side and `firstMigration` keys on jail-side. ⚠ **Not "two names for one
> concept"** (sharpened 2026-09-11): they are two signals of one *shape* — each is
> "no sidecar for this surface yet" — that differ in sidecar, trigger and
> consequence. `FirstApply` is *no provenance record* and gates a **consent
> prompt** ([`hostrender.go:98`](../../internal/entrypoint/hostrender.go#L98));
> `firstMigration` is *no trusted `last_render`* and triggers **adoption**
> ([`staterender.go:160`](../../internal/agentcfg/staterender.go#L160)). The
> difference bites below: on a home already on `assert`, provenance exists, so
> `FirstApply` is **false** at the very transition to `own` — the prompt cannot
> fire there ([§6.3.1](#631-why-the-adoption-diff-is-empty)). The residual
> host-only fact is narrow and real: a jail's transition is survivable by
> relaunching from a clean home, and the host has no clean home to fall back to.
> That justifies a **stronger net** there ([§6.3.3](#633-what-survives-as-a-guard)'s
> archive), never a different rule.
>
> None of this is an argument against capture: capture is about surviving
> regeneration, and nothing stops yolo recording "the human changed
> `permissions.defaultMode` since the last apply" with no whole-file compose at all.
> That recording is what the current `⚠ would overwrite your existing value` warning
> gropes toward, one apply at a time, with no memory between them.

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
  mount it. Ownership governs writing; the read is a separate grant. ⚠ **"Nothing
  here changes it" was withdrawn 2026-09-11**: whether that read survives at all is
  [OQ-CO11](#OQ-CO11), and env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) already ruled it retired.
- Under `none`, `host_apply_on_launch` has nothing to check: with no rendered host
  surface there is no staleness, and the four dispositions' *nothing would change
  ⇒ silent* rule ([§4.4](#44-what-the-key-does-not-do)) makes the launch check a
  no-op rather than a nag.
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
  nobody could read has granted no write claim. Stated here per key, as
  `host_apply_on_launch` states it, because the shared scope helper does not
  choose a direction ([§2.5](#25-the-three-existing-host-user-scope-keys)).

### 4.3 The unset state, and what happens to everyone already running

An **absent** key is not a fourth value; it is the *unset* state, and it
needs no ceremony of its own — **the default carries the whole migration**
([OQ-CO2](#OQ-CO2), ruled in review):

> [!NOTE]
> **"Unset", deliberately, and not "undeclared"** (renamed in review 2026-09-10).
> *Undeclared* already has a formal meaning three sections of this corpus rely
> on: the [Undeclared tier of the input closure](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails)
> — an input that shapes an environment while nothing names it, which is a
> statement about **a value inside an agent's config file**. This section is
> about **a yolo config key that has no value at all**, which is the ordinary
> word *unset*. The two appear four paragraphs apart and have opposite remedies:
> an undeclared key is promoted into a pack or discarded; an unset key is
> written. [§11](#11-success-criteria)'s "undeclared key" is the closure sense
> and is left as it is.

1. Behavior is `assert` — today's behavior, so nothing breaks on upgrade day,
   and nobody is interrupted in order to be told that.
2. **No migration prompt and no notice.** Each value explains itself at the
   point of the act instead. `yolo host apply` under `none` refuses — writes
   nothing and names the key that decided it, the row [§4.1](#41-the-key) states; `assert` and `own` do what
   they are configured to do. A prompt at upgrade asks the question at the one
   moment the user has least to go on — before they have seen any of the three
   values behave — and buys nothing, because every path it guards is either
   today's behavior or a value the user typed on purpose.
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
> **"Does not grant approval" is not "prompts because it is owned", and this
> paragraph used to read as the second** (corrected in review). The shipped rule
> is driven by **what the write would do**, never by which key is set, and it
> already has two teeth that answer the question directly:
>
> - **Nothing would change ⇒ silent.** The four dispositions
>   ([`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-four-dispositions))
>   put it as an invariant, not a default: *"A freshly-applied home must prompt
>   **not at all, ever**, until something actually changes."*
> - **A change that changes nothing the user has ⇒ still silent.** The host
>   apply gate is `confirmHostLosses`
>   ([`apply.go:577`](../../internal/cli/apply.go#L577), wired on the writing
>   path at [`apply.go:369`](../../internal/cli/apply.go#L369)), and its first
>   stated property is *ONLY WHEN SOMETHING IS ACTUALLY LOST* — gated on
>   `FirstApply && EntryLosses` (the code, [`apply.go:595`](../../internal/cli/apply.go#L595);
>   ⚠ its docstring still says `Overwrites`, [`apply.go:564`](../../internal/cli/apply.go#L564)
>   — a drift to know before quoting it), so a home yolo has asserted before
>   "prompts not at all". A scalar whose value merely changes is reported as an ordinary `⚠`
>   and does not prompt.
>
> So an owned host in steady state is silent, and the prompt that remains is the
> one that was already there for the case that was already dangerous. `own`
> adds no interruption of its own — which is the point, because a confirmation
> that fires on every apply is the mechanism `confirmHostLosses`' own docstring
> refuses to build.

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
| `workspace` | *(nothing to write to)* | **blocked, and more deeply than "unwired"** — see [OQ-CO8](#OQ-CO8) |

> [!IMPORTANT]
> **`local` and `host` both "reach every jail", and they are not remotely
> equivalent** (clarified in review 2026-09-10, and the table used to imply they
> were). Three facts decide it, all measured against the tree on that date.
>
> **1. Which file.** `--to host` writes the surface's *own* real-home path — for
> `claude/settings` that is `~/.claude/settings.json`. It edits that file
> directly, with no pack anywhere in the loop. That is not a workaround; it is
> what `assert` *means* — shared ownership of a hand-editable file, which
> [§7](#7-what-this-does-not-propose) keeps as fully supported and the right answer for a single machine.
>
> **2. It comes back into a jail only through `reads-host`, which is per-pack
> and rare.** The host file is not read into a jail because it is the host file;
> it is read because a pack asked for it. Exactly **two** shipped grants exist:
> `packs/claude` (`.claude/settings.json` → `host-claude/settings.json`) and
> `packs/pi` (`.pi/agent/settings.json` → `host-pi/settings.json`). So for
> `codex/config`, `opencode/config`, `agy/*`, `claude/config`, `pi/models` and
> `mise/config`, a `--to host` promotion is a **host-only edit that no jail will
> ever read**. The row must say so, because a user reading it beside `local`'s
> *"reaches every jail and the host"* will assume symmetry that does not exist.
>
> **3. Even where it is read, it lands at the second-weakest precedence.** The
> `host` layer is second in the stack
> ([§2.1](#21-the-layer-stack)) — under `workspace`, every
> `config-overlay`, capture, `computed` and `managed`. A `local` promotion lands
> as a `config-overlay`, three slots higher, at *every* notch. So the two
> destinations differ in reach, in precedence, and in whether any pack has to
> opt in.
>
> **What follows for the design:** `--to host` is not a general destination. It
> is a narrow convenience for the two surfaces that have a host layer, under the
> one ownership value that keeps the file the user's. It is never the answer to
> "get this key to all my jails" — that is `local`, and the promote UI should
> not offer `host` as though it were the same kind of thing.

#### 5.1.1 Why only two — and why that is a question, not a fact to design around

**Asked in review 2026-09-10.** *"Why do only those two have it? This is pack
declared? Not generic? Is that smart?"* Three separate answers, and they do not
all point the same way.

**Yes, pack-declared — and it should stay declared, though not for the reason
this section first gave.** `reads-host` carries a real file out of the user's home
into a container, and it is `ReviewWorthy` in the footprint
(`internal/packload/footprint_test.go:74-98`), which is what puts it on the
startup disclosure banner. A generic rule — *"every surface imports its own host
file"* — would make every jail read host state with nothing declaring it and
nothing to disclose, which is the shape
[`gate-placement-principle.md`](../reference/gate-placement-principle.md) exists
to refuse.

> [!WARNING]
> **It is NOT a gate, and an earlier draft of this section called it one**
> (corrected 2026-09-10). A declared `reads-host` is **unconditionally honored** —
> no approval, no origin check, no per-pack decision:
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
> file, and [OQ-TP9](trust-paths.md#decision-ledger) retired that gate for both."* The `refused` return slot is
> vestigial — its only consumer (`run/packrefusal.go`) was deleted with the gate and
> the seven non-test call sites across the `Honored*` family spell it
> `granted, _ :=` (⚠ counted 2026-09-11: the docstring's own *"Twelve"* at
> [`packload.go:435`](../../internal/packload/packload.go#L435) and this document's
> earlier *"five"* were both wrong). Two guard tests go red if a refusal source
> reappears: `TestNoFetchedPackHostAccessGateExists`
> ([`hostaccessgates_test.go:82`](../../internal/packload/hostaccessgates_test.go#L82))
> and `TestFetchedPackHostClaimsAreHonoredWithNoApproval`
> ([`packnohostgate_test.go:101`](../../internal/cli/run/packnohostgate_test.go#L101))
> — ⚠ the `TestNoPackHostAccessGate` the docstring names does not exist.
>
> **This strengthens the packaging argument rather than weakening it.** A
> declaration that can never be refused does exactly one job — *disclosure* — and a
> field on the surface discloses precisely as well as a separate contribution kind
> does. The only thing the separate kind still buys is the `host_files` case
> below.

**But the declaration carries no information.** Measured across all six shipped
agent packs on 2026-09-10:

| Pack | Surface path | `reads-host` source |
| :--- | :--- | :--- |
| `claude` | `~/.claude/settings.json` | `.claude/settings.json` |
| `pi` | `~/.pi/agent/settings.json` | `.pi/agent/settings.json` |

Both grants name **exactly the surface's own path** with `~/` stripped. The field
is shaped like "which host file?" and is used as "does this surface have a host
layer at all?" — a boolean wearing a path. That is worth noticing before anyone
builds on the path being freely chosen, because nothing shipped chooses it.

**And the coverage looks like incompleteness rather than a decision.** Eleven
pack surfaces ship; two have the grant. The nine without it all have a real host
counterpart a user plausibly has: `claude/config` (`~/.claude.json`),
`codex/config`, `opencode/config`, `agy/settings`, `agy/mcp`,
`copilot/{config,lsp,mcp}`, `pi/models`. Both grants that do exist are *"the
settings surface of an agent that has one"* — a pattern, not a chosen subset, and
**no doc records a reason any of the nine declined**.

##### The coupling is a basename match, and the read that depends on it is fail-open

**Asked in review: *"why would we want to allow a surface to exist without
`reads-host`? Package this so it structurally can't be forgotten."* Chasing that
found the mechanism is weaker than "separable" — it is `path.Base`.**

The surface already carries the field. `manifest.Surface.HostSource` is *"the
in-jail path the `host` layer is read from"*, and `Surface.HasHostLayer()` is
*exactly* `HostSource != ""` — the one predicate the boot render and the host-side
`config` verbs both consult. So the host layer is already a property **of the
surface**. The `reads-host` contribution does not express it; it *populates* it,
across a seam:

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

A surface is bound to its grant **if the two paths share a final component**. Three
consequences, none of them chosen:

1. **The directory is ignored.** A grant for `.claude/settings.json` binds to a
   surface at any path ending `settings.json`. Two surfaces in one pack sharing a
   basename would both take the first matching grant. Nothing shipped collides —
   `pi/settings` and `pi/models` differ — so the hazard is latent, not live.
2. **A typo does not fail; it un-binds.** A grant whose basename stops matching
   silently yields `HostSource == ""`, and `HasHostLayer()` then reports **false**
   for a surface whose author declared a host layer.
3. **And the read is fail-open**: `data, _ := os.ReadFile(remapCtx(surface.HostSource))`
   (`internal/entrypoint/packsurfaces.go:467`) discards the error and composes the
   surface **without** its host layer. **This has already shipped a real bug** —
   `StagedSlug`'s docstring records it, measured 2026-09-05: a pack named `my_pack`
   had its file mounted at `/ctx/host-my_pack/` while the entrypoint read
   `/ctx/host-my_5fpack/`, *"and the host-layer read is fail-open, so the surface
   composed"*.
4. ⚠ **And on one backend it fails on every launch, by construction** (verified
   2026-09-11). `macos-user` has no bind mounts and no `/ctx`; `YOLO_CTX_ROOT`, the
   seam that relocates the read, is set for Apple Container only
   ([`assemble.go:700`](../../internal/cli/run/assemble.go#L700));
   [`runplan.go:262`](../../internal/macosuser/runplan.go#L262) filters the *user's*
   source-bearing `host_files` out as an *"accepted deficiency"*, but a **pack's**
   `reads-host` grant is neither mounted nor filtered — so `hostSurfaceBytes` finds
   nothing and `claude/settings` composes **without the user's settings**, silently,
   on that backend alone. The fail-open docstring names the case and folds it into
   "no such host file": *"or this is macos-user, with no /ctx at all"*
   ([`packsurfaces.go:455`](../../internal/entrypoint/packsurfaces.go#L455)). That is
   a user feature-detecting the backend to learn whether their settings arrived —
   the failure [P5](#1-the-verdict-and-the-principles-it-rests-on)'s parity source
   exists to forbid — and it counts for both [OQ-CO10](#OQ-CO10) (fail closed) and
   [OQ-CO11](#OQ-CO11) (retire).

So the honest answer to *"why allow a surface without it"* is: **no reason was
ever given**, and the separation is not a design with a rationale — it is two
declarations naming the same file, joined by a string match, guarding a read that
cannot tell "no host layer" from "I could not find it".

##### There is no second location to name — the homes differ by `~` and nothing else

**Pushed further in review: *"How could a host surface ever be at a different
location? How could one NOT exist? Everything is mirrored."* Both halves hold,
and the manifest already says so about itself.**

`HostSource`'s own docstring calls it **"Derived, not declared"** — it is the
`/ctx` mount path the host CLI chose, not a path anyone wrote down
(`internal/agentcfg/manifest/manifest.go:131-142`). So the only *declared* path in
the whole arrangement is the grant's `host:` field, and that one cannot vary:

- **Every shipped surface path is `~/`-relative** — all eleven pack surfaces plus
  core's `mise/config` (`~/.config/mise/config.toml`); zero exceptions, measured
  2026-09-10.
- `~` is the **only** thing that differs between the two homes. So "the host's
  version of this surface" is fully determined by the surface's own path, and both
  shipped grants are exactly `<surface path minus ~/>`.

A grant *could* today name some other file — bind `~/.claude/settings.json` into a
surface that writes `~/.claude/settings.local.json` — but nothing does, nothing
ever has, and no doc describes a case for it. That is speculative generality with
a `path.Base` join holding it up.

**And "the host surface does not exist" is a policy claim wearing a factual one.**
The same docstring says *"Empty means the surface has no host layer, which is the
common case: most surfaces are yolo-owned outright."* But the file can exist for
every surface — the path mirrors — so what "no host layer" actually means is **yolo
declines to read it**. Those are different statements, and only one of them is
about the world. The field should therefore be a **policy bit on the surface**
("does this surface import the user's version?"), with the path derived, because
there is no second location for a path to name.

> [!IMPORTANT]
> **One thing this must not take with it: `reads-host` also serves the user's
> `host_files` key**, which maps to review-worthy `reads-host` claims in the
> footprint (`internal/packload/footprint_test.go:74-76`). Those carry *arbitrary*
> host files into a jail — not a config surface's mirrored twin — so they are
> genuinely not derivable and genuinely need a declared path. **The kind stays;
> what moves onto the surface is the config-surface binding.** A naive "delete the
> kind, put a boolean on the surface" would break `host_files`.

⚠ **`CombineShared` is not the obstacle it looks like.** `reads-host` is
`Combine: CombineShared` (*"Many packs may read one file; no combine"*,
`internal/packdecl/kinds.go:87`), which sounds like a reason it must stand apart
from a `CombineExclusive` surface. It is not: sharing describes how the **mount**
de-duplicates when two packs want one file, and says nothing about where the
**declaration** lives. Two surfaces could each declare the same host file and
still share one mount.

This is [OQ-CO10](#OQ-CO10). It is really a
[`pack-system`](../reference/pack-system.md) question rather than an ownership
one, and it is routed here because `--to host`'s usefulness is a direct function
of its answer: today the destination works for whichever surfaces happen to carry
a grant, and a user cannot tell which without reading pack manifests.

### 5.2 What it does, in order

1. Read the capture overlay for the surface from `<workspace>/.yolo/prism/`.
2. Drop keys `yolo config diff` already reports as **redundant** — identical to
   what the layers produce anyway — and keys that are **dead**: overridden where
   they already sit, so the move cannot help them. Since 2026-09-10 the overlay
   cannot hold a `computed` or `managed` key at all ([§5.4](#54-promotion-moves-a-key-down-the-stack)),
   which leaves one dead class: a key the Lua `transform` rewrites, which
   `narrowOverlay` does not see — its signature takes only the two owner layers
   ([`staterender.go:387`](../../internal/agentcfg/staterender.go#L387)). Redundant
   is free and it is most of the noise: of the six captured keys in this
   development jail on 2026-09-09, four were redundant — ⚠ and under the engine
   that ships today four of the seven the sidecar holds on 2026-09-11
   (`permissions`, `enabledPlugins`, `env`, `skipDangerousModePermissionPrompt`)
   could not be captured at all; this jail booted at 10:26 on 2026-09-10 and the
   narrowing landed at 18:15 (`7b0cc818`, `4e87181c`).
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

  ⚠ **Measured, not hypothetical — and it is a JAIL finding** (2026-09-11). This
  development jail's `opencode-config.overlay.json` holds a live-looking
  `TAVILY_API_KEY` **value**: absent from `last_render` (yolo wrote the
  `${TAVILY_API_KEY}` placeholder, which the codex sidecar still shows) and present
  in the overlay — opencode expanded it into its own file and capture recorded the
  result. The first `promote opencode` in this workspace meets a secret on its
  first key, so the deny-list is load-bearing from day one. ⚠ **And no deny-list
  exists yet**: [§7](#7-what-this-does-not-propose) used to say sensitive keys are
  refused *"using the deny-list and pack declarations that exist"*; nothing in
  `internal/agentcfg`, `internal/packdecl` or the config verbs matches
  `sensitive`, `deny-list` or `redact` (grepped 2026-09-11). Both are new work,
  owned by [§10](#10-what-i-would-build-in-order) step 4.

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
`config-overlay:<pack>`, which is **below** it
([§2.1](#21-the-layer-stack)). So promotion could in principle defeat itself: a
key that won as a capture might lose once declared, and the user would see their
value silently revert on the next boot having just been told it was promoted.

⚠ **The class is far smaller than this section first claimed, and for the default
destination it is empty** — corrected 2026-09-11 against the 2026-09-10 engine.
The first draft said a promoted key *"may lose to `computed`, `transform` or
`managed`"* and offered `permissions` — *"captured in this development jail today
and a `managed` key"* — as proof it was *"a real defect class, not a corner case"*.
Three facts dissolve that:

1. **The overlay cannot hold a `computed` or `managed` leaf any more.**
   `narrowOverlay` strips both from the *accumulated* overlay on every boot, on
   both branches — *"ONE RULE, BOTH BRANCHES"*,
   [`staterender.go:252-264`](../../internal/agentcfg/staterender.go#L252-L264) —
   so a key promote reads from the overlay is, by construction, not one those
   layers claim. The `permissions` measurement was real and is stale: this jail
   booted at 10:26 on 2026-09-10 and the fix landed at 18:15 (`7b0cc818`,
   `4e87181c`); the sidecar still shows it because the outer jail's binaries are
   frozen for the session.
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
cheap, and it is what protects `pack:<name>` — but it is a **two-sided** check
(*wins now* and *wins after*), a key that fails it is **refused by name with the
losing layer**, never promoted with a warning, and this section no longer claims
the case is common. [OQ-CO6](#OQ-CO6) asked whether a third option — offer the
pack's `managed` block, which does win — belongs on the table; the audit found it
is not expressible for any shipped surface, which is recorded there.

### 5.5 Where promote may run

The capture sidecar is at `<workspace>/.yolo/prism/`, which is a host directory —
so the **host** can read a jail's captures without any channel. The destinations,
however, are host-side files a jail cannot **write**. ⚠ Stated precisely
(2026-09-11): the jail *reads* the local pack as a staged `:ro` copy at
`/ctx/packs/local/` like any other pack
([`assemble.go:645`](../../internal/cli/run/assemble.go#L645)), and the host's
`config.lua` plus a filtered `config.jsonc` snapshot also cross
([`inheritscope.go:51`](../../internal/cli/run/inheritscope.go#L51),
[`:149`](../../internal/cli/run/inheritscope.go#L149)); what no jail can do is
write anything under the host's `~/.config/yolo-jail/`, which is the boundary
promote needs — a manifest is an input to composition, and an agent that could
rewrite one in-jail could grant its own pack a host file on the next boot.

**Promote is therefore a host-side verb**, and in-jail it refuses and prints the
host command — the mirror image of `refuseHostSideWrite`, which refuses host-side
`capture`/`reset` and points into the jail. [OQ-CO5](#OQ-CO5) asks whether a
jail should additionally be able to *file a request* the next host-side launch
surfaces, in the shape the `.yolo/handover.md` host→jail handoff already
established.

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

This reverses a shipped ruling and so it was opened rather than assumed:
[OQ-CO3](#OQ-CO3), **ruled yes-under-`own` in review**.

> [!WARNING]
> **The credential hazard is a property of CAPTURE, not of the host notch — and the
> jail already has it** (measured 2026-09-11,
> [§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)). The
> jail's overlay sits in `<workspace>/.yolo/prism/` at mode `0644`, in a directory
> yolo *assumes* is gitignored ([`target.go:226`](../../internal/render/target.go#L226),
> [`prism.go:41`](../../internal/entrypoint/prism.go#L41)) but never adds to any
> `.gitignore` — this repository's own `.gitignore` line 6 does it by hand — and it
> holds a captured API key today. So the shipped asymmetry is inverted on its own
> axis: the notch whose sidecar *is* in a workspace and *can* reach git is the one
> that captures freely. By [P5](#1-the-verdict-and-the-principles-it-rests-on) the
> guard belongs to capture at every notch — a capture-time deny-list, a sidecar
> mode — and the `0600` proposed for the host store is a parity gap against the
> jail's `0644` until both move. Neither is this document's to rule; both are
> listed for the roadmap.

> [!IMPORTANT]
> **Host capture under `own` is not a convenience — it is what makes adoption
> non-destructive**, so this ruling is a precondition of
> [§6.3](#63-the-one-asymmetry-that-survives-deletion) rather than an addition to
> it. Refusing host capture under `own` would leave the first owned render
> composing from declared layers alone, which is the empty-overlay seed that
> [§6.3.1](#631-why-the-adoption-diff-is-empty) records as a shipped data-loss
> bug. The two must land together.

### 6.3 The one asymmetry that survives: deletion

Whole-file composition means yolo may **remove** a key from a real host file.
That is correct under `own` — a derived file contains what the definition says
and nothing else — and it is the single place where "the host is a real home"
genuinely changes the answer.

**Adoption, however, is not that case.** The first owned render reproduces every
key the file holds that yolo does not itself declare, and it does so *by
construction* rather than by a guard this design adds: `capture-then-regenerate`
is what `stateful` adoption already does. ⚠ Not *"byte-identical"*
unconditionally, as this paragraph said until 2026-09-11: adoption also **adds**
yolo's declared keys the file lacked (`dropNullLeaves` strips the tombstones
`mergeDiff` records for them, [`staterender.go:198`](../../internal/agentcfg/staterender.go#L198))
— which a first `assert` apply adds too — and it drops the classes
[§6.3.2](#632-the-three-classes-adoption-does-not-cover) names, one of which is
**not** empty on a home already on `assert`.

### 6.3.1 Why the adoption diff is empty

`ComposeStateful` treats "no trusted `last_render` for this surface" as a
**first migration** and, on that branch, seeds the overlay from the file it
finds: `residue = mergeDiff(pureRender, current)`
([`staterender.go:174`](../../internal/agentcfg/staterender.go#L174) is the prose,
[`:198`](../../internal/agentcfg/staterender.go#L198) the code). The next line of
the render is then `declared layers + that residue`, which reproduces the file's
undeclared keys exactly. Switching a host surface to `own` is precisely that branch —
there is no host `last_render` yet — so **capture happens before the first owned
write, and the write puts back what capture just took**.

This is not a hopeful reading of the mechanism. That branch exists *because*
seeding an empty overlay was a shipped data-loss bug (`copilot/config` collapsed
to `{"yolo": true}` and logged the user out); the comment above it is labelled
`B1 (⚠ DATA LOSS FIX)` ([`staterender.go:164`](../../internal/agentcfg/staterender.go#L164)).
The engine's answer to "adopt a file I did not write" is already the one this
section wanted a confirmation prompt to protect.

⚠ **Do not check this against the file header.** `staterender.go`'s package
comment, `StatefulInputs.LastRenderPresent`, `StatefulOutput.OverlayJSON` and the
`ComposeStateful` docstring all still describe the pre-B1 branch — *"seeds a
truthful baseline with an empty overlay and skips capture"*, *"{} on a first
migration"*, *"render = Compose(overlay=∅)"* — so a reader who checks the docstring
finds the opposite of this section. The body at
[`staterender.go:164-215`](../../internal/agentcfg/staterender.go#L164-L215) is the
authority (noted 2026-09-11; the docstrings are listed for the roadmap).

**For a home already on `assert`, the diff is empty for MOST keys for a second
reason — ⚠ and NOT for all, which this paragraph claimed until 2026-09-11.** `rmw`
re-asserts the declared keys on every apply, so a key that collides with yolo's
declarations does not survive under `assert` either. But the two modes disagree
about **granularity**, and the disagreement is a loss path:

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

That is the same file, one apply apart, losing a leaf `assert` had preserved for
months. Three things make it worse than a corner. The engine's own steady-state
rule, three functions down, says a blanket top-level drop *"would be simpler and
wrong — it would discard the agent's permission list on every boot"*
([`staterender.go:410-414`](../../internal/agentcfg/staterender.go#L410-L414)), so
the two rules in one file disagree and adoption took the wrong one. The gate that
should notice cannot: `FirstApply` is *false* once a provenance record exists
([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)'s callout),
and `EntryLosses` is defined over table entries. And [§11](#11-success-criteria)'s
*"zero bytes"* criterion fails for every user with such a leaf.

**The design therefore requires adoption to drop at the granularity `rmw` writes
at**: leaf-level against deep-merged owners (`managed`, `defaults`,
`config-overlay`), wholesale only against `computed` tables — which is exactly
`dropOverriddenKeys`' existing rule applied to the pure render. Until that lands,
`own` is not zero-bytes on an `assert` home, and [OQ-CO7](#OQ-CO7)'s archive
covers a *known* path rather than a speculative one. The same coarse rule fires in
a jail whenever `last_render` is lost — B1's own trigger — so this is a shipped
jail defect too, not only a design hole.

### 6.3.2 The three classes adoption does not cover

Stated because "empty by construction" is a claim with edges, and each edge is a
deliberate line in the engine rather than an oversight:

| Class | What happens | Why |
| :--- | :--- | :--- |
| A key nested inside **any** top-level object the pure render holds — a `computed` table like `mcpServers`, *and* a deep-merged `managed`/`defaults` object like `permissions` | **not adopted** | `dropYoloOwnedSubtrees` ([`staterender.go:338`](../../internal/agentcfg/staterender.go#L338)) — right for the table (adopting would resurrect a dropped entry and break *regenerate, don't reconcile*); ⚠ **wrong for the deep-merged object**, where `rmw` would have kept the leaf ([§6.3.1](#631-why-the-adoption-diff-is-empty), corrected 2026-09-11) |
| A key a higher layer re-asserts — `managed`, or the per-boot `computed` layer | **never captured, in either branch** | `narrowOverlay` — both fold above the capture overlay and win unconditionally, so a captured copy could only sit in the sidecar as noise `yolo config diff` would report as a phantom edit |
| A **keyless** surface (`raw`, `lines`) | **not adopted at all** | one "key" is the whole file, so adoption would mean "the file wins outright", freezing a host-mirrored file at stale content forever ([`staterender.go`](../../internal/agentcfg/staterender.go)) |

> [!NOTE]
> **Row 2 stopped being adoption-scoped on 2026-09-10.** It described
> `dropKeys` against `Surface.Managed`, a narrowing only the first-migration
> branch ran. Steady-state capture ran no such narrowing, so it recorded both
> managed AND computed keys into the sidecar every boot — where they could never
> affect output. The rule now runs once, on the ACCUMULATED overlay, for both
> branches and both layers, which also makes the store **self-healing**: a
> sidecar an older yolo dirtied is canonicalized on the next boot rather than
> staying dirty forever. It is LEAF-level per layer, because both layers
> deep-merge and share their objects with the user (mise's computed `[tools]` vs.
> a user-added global tool; managed `permissions.defaultMode` vs. Claude's own
> `permissions.ask`), and it keeps a null tombstone under an object-valued owner
> because what that erases is a lower layer this rule cannot see. The competing
> semantic — retain the capture so it activates if the layer later stops
> supplying the key — was rejected: the pending edit is invisible and can sit for
> months, so it would silently restore a stale `permissions` grant, or resurrect
> an MCP server the user had deleted. See
> [§5.1 of the composition plan](../plans/agent-settings-composition.md#51-the-store-holds-only-edits-that-can-win).

The first two are the loss set `confirmHostLosses` gates on **only on a first
apply** (`FirstApply && EntryLosses`, [§4.4](#44-what-the-key-does-not-do)) — ⚠ so
on a home already on `assert` the prompt does **not** fire, and row 1's
deep-merged half is exactly the case that loses there
([§6.3.1](#631-why-the-adoption-diff-is-empty)). This paragraph said *"no new
guard is needed"* until 2026-09-11; the honest form is that the drop must be
narrowed so there is nothing for a guard to catch, and until it is, the archive is
the only net.

The third has no answer yet and is the honest gap in this section: no shipped
host surface is keyless, so nothing regresses on the day `own` lands, but a
keyless host surface would adopt nothing and render over the file. It is
[OQ-CO9](#OQ-CO9).

### 6.3.3 What survives as a guard

**One archive at adoption.** The pre-existing file is copied once to the state
dir before the first owned render. Not per-apply snapshots: those are a
different feature with a retention policy. What changed in review is the *risk
it covers* — no longer "adopting `own` ate settings I had", which
[§6.3.1](#631-why-the-adoption-diff-is-empty) makes structurally hard, but
"adoption's classification was wrong about one of the three rows above". ⚠ On
2026-09-11 the audit found row 1 *is* wrong for deep-merged objects
([§6.3.1](#631-why-the-adoption-diff-is-empty)), so the archive covers a known
loss, not a speculative one — and it is cheaper than this section priced it: the
archive root, the one-generation-per-apply layout and `yolo prune`'s reclamation
already ship for skills, files and briefings
(`~/.local/share/yolo-jail/archive/<bucket>/<stamp>/`,
[`applyhostskills.go:76`](../../internal/cli/applyhostskills.go#L76)); a config
bucket is a new *bucket*, not a new *surface*. See [OQ-CO7](#OQ-CO7).

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
  no taint propagation. ⚠ The deny-list itself is new
  ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot); nothing
  shipped matches on sensitivity, checked 2026-09-11) — a list of name patterns,
  owned by step 4.
- **No `pack import` verb.** Promotion moves *captured keys* into a pack. ⚠ But
  `own` + `promote` **compose into one** for every key nothing declares (noted
  2026-09-11): adoption captures a hand-written file's undeclared keys into the
  host overlay ([§6.3.1](#631-why-the-adoption-diff-is-empty)), and promote lifts a
  captured key into the local pack — which is the migration env-manager plan
  [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) assumed when it retired the host layer (*"author (or `yolo config
  promote` into) a one-file local pack"*). What stays manual is re-authoring a file
  *as* a pack, with structure and intent; what becomes mechanical is not losing its
  keys.
- **The `guest` notch is untouched.** It has no provenance dir and no sidecars
  today; giving it an ownership contract is a separate question.
- **No automatic promotion, ever** — see [§5.7](#57-forbidden-behavior).

---

## 8. Alternatives considered

**A. Leave it alone; document the asymmetry better.** The migration guide's
correction (`27d21a4f`) is most of this. **Rejected** — it addresses the
confusion and none of the missing capability: there is still no way out of
capture except discard, and still no revert.

**B. Make the host `stateful` unconditionally.** Simplest possible parity.
**Rejected** — it takes deletion authority over every user's real home without
asking, including users who never ran `yolo host apply` and never will.

**C. A boolean `manage_host: true|false`.** What the maintainer initially
suggested, and closest to the question actually being asked. **Rejected for the
three-value enum**, narrowly: the boolean cannot express the difference between
"assert my declared keys into a file that is mine" and "this file is yours
now" — which is precisely the ambiguity that produced this document. See
[OQ-CO1](#OQ-CO1); if `assert` proves not to be a real population, this becomes
right.

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
| A user picks `own`, and yolo deletes settings they cared about | Adoption is capture-then-regenerate, so the first owned render is byte-identical ([§6.3.1](#631-why-the-adoption-diff-is-empty)); the classes it does not cover are the ones `confirmHostLosses` already prompts for ([§6.3.2](#632-the-three-classes-adoption-does-not-cover)), plus the one-time archive |
| Promotion silently demotes a key that then reverts | Precedence check is a **refusal**, not a warning ([§5.4](#54-promotion-moves-a-key-down-the-stack)) — and the class is empty for `--to local` since 2026-09-10; the check protects `--to pack:<name>` |
| A user on `assert` switches to `own` and silently loses a leaf under a managed object (`permissions.ask`) | Adoption must drop at `rmw`'s granularity ([§6.3.1](#631-why-the-adoption-diff-is-empty)); until it does, the archive ([OQ-CO7](#OQ-CO7)) is the only net, because `confirmHostLosses` is off once provenance exists |
| A credential is promoted into a pack, and the pack is pushed — or already sits in a sidecar | Promote's deny-list ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot), new work); `--force` is per-key and named in output; the sidecar half is a capture-time question at every notch ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)), listed for the roadmap |
| ~~The unset-state prompt trains people to hit enter~~ | **Risk retired** — there is no prompt ([OQ-CO2](#OQ-CO2)). The `--sealed` refusal is the whole backstop, and it does not depend on attention |
| The local pack becomes an unreviewable pile of promoted keys | Every promotion is an ordinary edit to a readable `pack.json`; `yolo pack lint` and `footprint` already report its claims |
| Three-value enum confuses users who wanted a switch | ⚠ **Mitigation weakened by [OQ-CO2](#OQ-CO2)** — with no prompt, the three values are explained in `yolo config-ref` and at the point of the act (`none` names the key that made it write nothing). Accepted: the value a confused user lands on is `assert`, which is what they already have |
| A jail-side agent cannot promote, so the workflow stalls where the work happens | [OQ-CO5](#OQ-CO5)'s request channel; the refusal names the host command regardless |

---

## 10. What I would build, in order

1. **`host_management`, parsing and validation only** — the key, its user-scope
   read, its fail-closed direction, its `inherit.go` entry, and the `--sealed`
   refusal while the key is unset. Nothing changes behavior yet; the contract
   becomes expressible. **No prompt to build** ([OQ-CO2](#OQ-CO2)), which is most of
   what this step used to be.
2. **Wire `none` and `assert`.** `none` makes `yolo host apply` refuse; `assert`
   is today's path. This is the whole key for everyone who does not want `own`,
   and it lands the ownership answer without touching the render engine.
3. **`--revert` under `assert`,** consuming the provenance record that already
   exists. Small, independently useful, and it proves the record is trustworthy
   before anything depends on it more heavily. ⚠ Reverses env-manager plan [`OQ-1`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
   ([§2.4](#24-the-verbs-that-exist-and-the-ones-that-do-not)); write the reversal
   into that ledger in the same commit.
4. **`yolo config promote --plan --json`** — classification and precedence
   checks, emitting the plan, writing nothing — including the sensitivity
   deny-list, which does not exist yet ([§5.3](#53-classification--what-a-machine-can-decide-and-what-it-cannot)). The dangerous half is the write;
   the valuable half is the analysis, and it can ship first.
5. **Promote's write path** to `local` and `pack:<name>`, with the atomic
   write-and-reset.
6. **`own`:** the host notch renders `stateful`, **with the host capture store
   ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)) and
   host-side `reset` in the same commit** — adoption is unsafe without either
   ([§6.3.3](#633-what-survives-as-a-guard)) — plus the one-time archive, the
   keyless carve-out [OQ-CO9](#OQ-CO9) rules, and **adoption narrowed to `rmw`'s
   granularity** ([§6.3.1](#631-why-the-adoption-diff-is-empty)), without which
   `own` is not zero-bytes on an `assert` home. Last because it is the only step
   that can lose data, and by then promotion exists, which is what makes `own`
   attractive rather than merely strict. ⚠ Reverses env-manager plan [`OQ-4`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) and
   un-moots [`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) ([§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications));
   record both there.

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
  naming that pack. ⚠ Not by promoting a `managed` key (as this line said until
  2026-09-11): one cannot be in the overlay since 2026-09-10, so `--keys
  permissions` fails [§5.6](#56-degenerate-inputs-and-failure-paths)'s *not in the
  capture* check instead.
- Switching to `own` on a home already applying under `assert` changes **zero
  bytes** — the measurable form of [§6.3.1](#631-why-the-adoption-diff-is-empty),
  and the criterion to write the test for first. ⚠ The shipped adoption rule does
  **not** satisfy it for a leaf under a managed object; the first test to write is
  the one with `permissions.ask` in the file. Switching on a home that never
  applied loses only what a first `assert` apply would have lost, prompts for it
  through the gate that already exists, and leaves the pre-existing file
  recoverable from the archive.
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

1. ✅ **OQ-CO1: Two values or three?** — **RULED 2026-09-10: three.** Does `assert` earn its place, or is the
   real choice binary — yolo writes the host file or it does not? Three values
   is one more thing to explain at the one moment a user is deciding; two values
   collapses "asserts my declared keys into a file that is mine" into either
   "off" (losing today's shipped, working behavior) or "own" (taking deletion
   authority nobody asked for). This decides the shape of
   [§4.1](#41-the-key), the migration in [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running),
   and how alternative C in [§8](#8-alternatives-considered) resolves.

   <!-- vantage: oq id=OQ-CO1 leaning="Three values. `assert` is the shipped behavior and has real users — including this maintainer today — so collapsing it means either a regression or a forced escalation to `own`." -->

   _Leaning:_ Three. `assert` is not hypothetical — it is what is running on this
   maintainer's machine right now, and collapsing it means either regressing that
   or force-escalating it to `own` without asking.

   **Answer (2026-09-10, review round 0 — THREE, with the leaning):**
   > **Three values.** Ruled in the leaning's own words: *"`assert` is the shipped
   > behavior and has real users — including this maintainer today — so collapsing
   > it means either a regression or a forced escalation to `own`."* The cost the
   > question worried about — one more thing to explain at the deciding moment —
   > is paid down by [OQ-CO2](#OQ-CO2)'s ruling, which removed the prompt that
   > would have done the explaining: nobody is asked to choose at upgrade, so the
   > third value costs a line in `yolo config-ref` rather than a decision under
   > time pressure. Alternative C in [§8](#8-alternatives-considered) resolves as
   > rejected.

2. ✅ **OQ-CO2: Should the unset state prompt, or just warn?** — **RULED 2026-09-10: neither.** [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running)
   proposes that the first `host apply --assert` under an absent key stops and
   asks. The alternative is to print a notice and carry on as `assert`, leaving
   `--sealed` as the only place the unset state bites. This decides whether
   "no question anymore" is achieved for people who never read a config
   reference, at the cost of one interruption in an established workflow.

   <!-- vantage: oq id=OQ-CO2 leaning="Prompt once. The goal is to eliminate the ambiguity, and a notice that carries on is exactly the mechanism that let the current ambiguity persist." -->

   _Leaning:_ Prompt, once, with no default. A notice that proceeds anyway is the
   same mechanism that let this ambiguity survive this long — and the prompt fires
   at most once per machine.

   **Answer (2026-09-10, review round 0 — NEITHER, against the leaning):**
   > **Keep the default as `assert` and say nothing.** No prompt, no notice. The
   > question was posed as prompt-or-warn and the ruling is that both are
   > ceremony: *"can't we just keep the default as assert? then there's nothing
   > needed here?"* Each value already explains itself at the point of the act —
   > `none` reports that it wrote nothing and why, `assert` and `own` do what
   > they are configured to do — and that is feedback where the user is looking,
   > rather than at upgrade time when they have seen none of the three behave.
   > `apply --sealed` remains the one place an unset key bites, which is the
   > command whose whole job is auditing declaredness. Settled in
   > [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running).
   >
   > The leaning's argument — *"a notice that proceeds anyway is the mechanism
   > that let this ambiguity survive"* — does not carry, because the ambiguity it
   > names was **inference** (the notch silently deciding ownership), not
   > silence. A default that equals today's behavior and is documented in
   > `yolo config-ref` is declared in the only sense that matters here; nothing
   > is being inferred from where a file lives.

3. ✅ **<a id="OQ-CO3"></a>[`OQ-CO3`](#OQ-CO3) — RULED 2026-09-10: yes, under `own` only.** Does `own` re-permit host-side capture, reversing the
   [§9.3](host-render-target.md#9-open-questions--the-discussion-part) refusal?** [`host-render-target.md`](host-render-target.md) [§9.3](host-render-target.md#9-open-questions--the-discussion-part)
   ruled that host-side `capture`/`reset` refuse rather than redact, because capture would
   copy a credential into a workspace sidecar. [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)
   argues the hazard does not arise under `own` — the store is in the host state
   dir at `0600` and never crosses a boundary — but this reverses a decision
   taken deliberately, and reversals deserve to be seen.

   <!-- vantage: oq id=OQ-CO3 leaning="Yes under `own` only, keeping the refusal under `none` and `assert`. The original hazard is the workspace sidecar crossing into a jail, which an owned host's own state dir does not do." -->

   _Leaning:_ Yes, and only under `own`. The refusal stays for `none` and
   `assert`. The hazard [§9.3](host-render-target.md#9-open-questions--the-discussion-part) names is the *workspace* sidecar, and an owned
   host's capture never goes there.

   **Answer (2026-09-10, review round 0 — YES, under `own` only, and it is a
   precondition rather than a permission):**
   > Ruled with the leaning, and promoted in force: *"we should capture, then
   > regen the exact same thing with the host capture layer."* Host capture under
   > `own` is the mechanism that makes adoption byte-identical, so it is not an
   > additional capability `own` may have — it is what `own` is built on. The
   > `0600` store beside the host provenance record never crosses a boundary, and
   > promote refuses sensitive keys independently, so the
   > [§9.3](host-render-target.md#9-open-questions--the-discussion-part) hazard
   > (a credential reaching a *workspace* sidecar) does not arise in this shape.
   > The refusal stays for `none` and `assert`. Settled in
   > [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to) and
   > [§6.3.1](#631-why-the-adoption-diff-is-empty).

4. ✅ **OQ-CO4: Is the conventional local pack too blunt a default destination?** — **RULED 2026-09-10: no — keep it, and confirm.**
   It is implicitly selected in every jail and renders at every notch, so
   `--to local` is genuinely "everywhere, forever" — which is the feature, and
   also means a casually promoted key has the widest possible blast radius with
   the least ceremony. The alternative default is to require `--to` explicitly so
   the scope is always a stated choice.

   <!-- vantage: oq id=OQ-CO4 leaning="Keep `local` as the default but print the blast radius in the confirmation — 'this reaches every jail and your host'. Requiring --to punishes the common case to guard a reversible edit." -->

   _Leaning:_ Keep the default, and make the confirmation state the blast radius
   in words. The edit is a readable line in a `pack.json` and trivially undone;
   requiring `--to` taxes the common case to guard a reversible one.

   **Answer (2026-09-10, review round 0 — KEEP the default; the guard is the
   confirmation, not the flag):**
   > **`local` stays the default, and promote shows exactly what it is about to
   > write and asks — unless the approval flag is passed.** *"If we show what's
   > being promoted and require a confirm (unless a `--yes` or whatever is passed) that
   > is enough."* So the blast radius is disclosed at the moment of the act rather
   > than defended by making every invocation type `--to`, which is the same
   > shape [OQ-CO2](#OQ-CO2) took for the unset state: **feedback at the point of
   > the act beats ceremony in front of it.**
   >
   > Two requirements this pins, both already in [§5.1](#51-surface) and now
   > load-bearing rather than incidental:
   >
   > - **Selection is explicit and reviewable.** `--keys a,b` selects specific
   >   keys; omitting it means all of them. Either way the confirmation lists what
   >   was selected — *"we'll want a confirm of what we've selected before running
   >   it"* — so "all" is never silently wider than the user pictured.
   > - **One approval flag is the only way past it**, and it is the scripting
   >   path, not a default. `--plan` remains the read-only form for looking
   >   without deciding.
   >
   > ⚠ **That flag is NOT `--yes`** — raised in review (*"I don't love `--yes`,
   > isn't there a more standard option name for this?"*) and **settled
   > 2026-09-10: keep the repo's form.** `--yes`/`-y` is the most
   > standard name across tools generally — and **this repo has already declined
   > the generic form, on the record.** `config.AcceptConfigChangesFlag`
   > (`internal/config/snapshot.go:81`) is spelled **`--accept-config-changes`**,
   > and its docstring rules why consent here has the shape it does:
   >
   > > A FLAG AND NOT AN ENVIRONMENT VARIABLE, deliberately […] Those suppress a
   > > DIAGNOSIS; this one grants an APPROVAL. An env var is inherited by every
   > > child process and survives in a shell for the rest of a session — precisely
   > > the property a per-launch approval must not have.
   >
   > Both halves apply verbatim to promote: it grants an approval, and an
   > inheritable env var would be the wrong vehicle. The house pattern is
   > **`--accept-<what is being approved>`**, so promote's is
   > **`--accept-promotion`**. It also keeps `--force` free for its existing job —
   > overriding a *refusal* (`refuseHostSideWrite`) — which is a different act from
   > confirming an intended one, and collapsing the two is how `--force` becomes
   > the flag people paste without reading.

5. 💬 **OQ-CO5: In-jail promote — refuse with instructions, or file a request?**
   [§5.5](#55-where-promote-may-run) establishes promote as host-side. But the
   work happens in the jail, and an agent that has just made a good config change
   is exactly who should promote it. A request file at `<workspace>/.yolo/` that
   the next host-side launch surfaces would close that loop, in the shape
   `.yolo/handover.md` already established for the host→jail direction. It is
   also a new consent surface pointed at the user's home.

   <!-- vantage: oq id=OQ-CO5 leaning="Refuse-with-instructions in v1; design the request channel but do not build it until promote itself has been used enough to know which keys people actually promote." -->

   _Leaning:_ Refuse with instructions for v1. The request channel is the right
   shape and the wrong time — build it once there is evidence about what people
   actually promote, so the consent prompt can say something specific.

   ⚠ **Sharpened 2026-09-11 — the channel is not the question.** The host already
   reads `<workspace>/.yolo/` on every launch (drift, the prism sidecars), so a
   request would be one more file there and needs no new plumbing; and there is no
   jail→host handoff today of any kind — [`prepare.go:509-537`](../../internal/cli/run/prepare.go#L509-L537)
   is host→jail only, verified. What CO5 decides is narrower than it reads:
   whether the next host-side launch *surfaces* such a file, and to whom — since a
   request is written by an agent and read by a human, the consent shape matters
   more than the transport.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-CO6: When a promoted key would lose precedence, may promote offer the
   `managed` block?** [§5.4](#54-promotion-moves-a-key-down-the-stack) refuses
   such a key. A pack's own `managed` layer *does* win — so promote could offer
   to write there instead, which would make the promotion work. It would also
   hand users a routine way to assert keys above `computed` and `transform`,
   which those layers exist to prevent.

   ⚠ **Re-grounded 2026-09-11: for every shipped surface the option does not
   exist, so the question has shrunk to a niche.** `Surface.Managed` is populated
   only from the owning pack's own `config` contribution plus its autonomy posture
   ([`packload.go:144-164`](../../internal/packload/packload.go#L144-L164) — `SurfaceContributions` → `DecodeSurfaces` → `foldPostureManaged`); a
   `config-overlay` body has a field *named* `managed` that is *"NOT a claim about
   the managed LAYER"* and folds at the single `config-overlay` slot
   ([`overlay.go:33`](../../internal/agentcfg/manifest/overlay.go#L33)). So neither
   the local pack nor any `pack:<name>` that is not the surface's owner can write a
   managed block — and the owner of every shipped surface is an embedded pack
   promote refuses to edit. Two more things narrow it: the input set that would
   lose to `managed`/`computed` is empty by construction
   ([§5.4](#54-promotion-moves-a-key-down-the-stack)), and the residual loser —
   another overlay ordered later — is beaten by `managed`, which is not on offer.
   **For shipped surfaces, refuse is what the mechanism permits, not a preference.**
   What remains askable: a surface a *user's own* pack declares, whose captures
   promote reads and whose `managed` block that pack may write — should promote
   offer it there?

   <!-- vantage: oq id=OQ-CO6 leaning="Refuse only, everywhere. For shipped surfaces nothing else is expressible (managed is owner-only and the owners are embedded). For a user's own surface the friction argument still holds: a one-keystroke path to outranking computed/transform would be used for exactly the reasons that rule exists; the managed block stays a hand edit." -->

   _Leaning:_ Refuse only, everywhere. For shipped surfaces nothing else is
   expressible; for a user's own surface writing the `managed` block by hand stays
   possible, and the friction is the point — a one-keystroke path to outranking
   regenerate-don't-reconcile would be used for exactly the reasons that rule
   exists.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-CO7: One archive at adoption, per-apply snapshots, or nothing?**
   [§6.3.3](#633-what-survives-as-a-guard) proposes a single copy
   of the pre-existing file taken once, when a home first becomes `own`. Per-apply
   snapshots would cover more (a bad pack update deleting a key months later) at
   the cost of a retention policy and a new disk surface — in a project actively
   reducing both.

   ⚠ **Re-grounded in review 2026-09-10, and *nothing* got stronger.** This was
   written when adoption was a confirmed diff; adoption is now byte-identical by
   construction ([§6.3.1](#631-why-the-adoption-diff-is-empty)), so the risk the
   archive covers is no longer "adopting `own` ate my settings" but the narrower
   "adoption misclassified one of [§6.3.2](#632-the-three-classes-adoption-does-not-cover)'s
   three rows". That is a smaller and more speculative risk, which is an argument
   for `nothing` that did not exist before — weighed against an archive being one
   `copyFile` with no retention policy at all.

   ⚠ **Re-grounded again 2026-09-11, and BOTH sides of the previous paragraph
   moved.** The risk is no longer speculative: row 1 *is* misclassified for
   deep-merged objects, `assert→own` loses `permissions.ask`-shaped leaves, and the
   gate cannot see it ([§6.3.1](#631-why-the-adoption-diff-is-empty)). And the cost
   was overstated: *"a new disk surface in a project actively reducing both"* is
   wrong — an archive subsystem already ships for skills, files and briefings, with
   one generation per apply and `yolo prune` reclamation
   (`~/.local/share/yolo-jail/archive/<bucket>/<stamp>/`,
   [`applyhostskills.go:76`](../../internal/cli/applyhostskills.go#L76)); a config
   bucket is one more call into it, and per-apply snapshots would inherit its
   retention rather than needing one. The config-surface path has *no* archive
   today, and adoption of skills is a *move*, so the work is real but small. Two
   further stakes: a keyless whole-file replacement at an unattended jail boot has
   no prompt available ([OQ-CO9](#OQ-CO9)), so an archive is the only net that can
   exist there; and by [P5](#1-the-verdict-and-the-principles-it-rests-on) whatever
   is taken on the host's first owned render should be taken at a jail's
   `firstMigration` too, into the workspace's own `.yolo/` tree.

   <!-- vantage: oq id=OQ-CO7 leaning="One archive at adoption, as a `config` bucket in the archive subsystem that already ships — and, by P5, at a jail's firstMigration too. It now covers a KNOWN loss path (the deep-merged-leaf drop) that the prompt cannot see; the later-regression case is what git on the pack is for." -->

   _Leaning:_ One archive, taken at the transition, as a `config` bucket in the
   existing archive subsystem — at both notches. It covers a known loss path the
   prompt is structurally blind to. The later-regression case is covered by the
   pack being in git, which is the whole arrangement `own` is recommending.

   **Answer:**
   > _(empty — fill in when decided)_

8. 🔒 **OQ-CO8: Is `--to workspace` in scope?** Promoting a key to the workspace
   config would be the natural home for a genuinely project-specific value.
   Blocked: the `workspace` layer is recorded as "DECIDED BUT UNWIRED" in
   [`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md) —
   no `agent_config.<agent>` key exists and `Inputs.Workspace` is a real engine
   slot every caller passes nil for. This cannot be answered until that layer is
   either wired or retired.

   > [!NOTE]
   > **"Workspaces have no packs, so there is nowhere to put it" — correct about
   > packs, and the layer was never going to be one** (asked in review
   > 2026-09-10; all four checks below run against the tree that day).
   >
   > `packs` is **user-scope-only by construction**, so a workspace genuinely
   > cannot select a pack — that is [OQ-TP9](trust-paths.md#decision-ledger)'s
   > ruling and it is load-bearing: `/workspace` is bind-mounted read-write, so
   > an agent can edit `yolo-jail.jsonc`, and a workspace that could add a pack
   > could promote itself into the user's home.
   >
   > The `workspace` layer is a different thing: a **slot in the compose stack**
   > (`Inputs.Workspace`, third in [§2.1](#21-the-layer-stack)), fed by a config
   > key in `yolo-jail.jsonc` rather than by any pack. Three measurements say how
   > unwired it is:
   >
   > - **No key.** `agent_config` appears nowhere in `internal/config`.
   > - **No producer.** No call site sets `Inputs.Workspace` at all — not
   >   `prism.go`, not `packsurfaces.go`, not `config.go`. The slot is nil
   >   everywhere, so it is not merely unreachable by users; nothing fills it.
   > - **It could never reach the host anyway.** `render.Host` leaves `Workspace`
   >   empty *by definition* (stated in `prism.go`'s `targetTransformScript`), so
   >   a `--to workspace` promotion would be jail-and-guest-only by construction —
   >   which is a real limit on how useful the destination could ever be.
   >
   > So the answer to "where would it even go" is: a config key that does not
   > exist, feeding an engine slot nothing fills, in a layer that cannot cross to
   > the host. That is three separate pieces of work before the question is even
   > askable — which is why this stays 🔒 rather than becoming a leaning.
   >
   > ⚠ **And there is a reason to be slow about wiring it**, which belongs here
   > rather than in whatever doc wires it: the workspace config is
   > **jail-writable**. A layer an agent can edit sitting *above* the host layer
   > is fine while it stays inside the jail, and is exactly the shape that must
   > never be given a path to a real home. Whoever wires it owns that argument.

   <!-- vantage: oq id=OQ-CO8 leaning="Blocked, not leaning: the workspace layer is decided-but-unwired, so a promote destination pointing at it would write a key nothing reads. Wire or retire that layer first, then ask this." -->

   **Answer:**
   > _(blocked — wiring the `workspace` layer decides it; and per the note above,
   > that is three pieces of work — a config key, a producer, and an argument
   > about a jail-writable layer's reach — not one)_

9. 💬 **<a id="OQ-CO9"></a>OQ-CO9: What does `own` mean for a KEYLESS surface?**
   Opened in review 2026-09-10 and re-grounded twice since. **Three of this entry's
   claims have been wrong, including both leanings it has carried** — recorded in
   full because the pattern is the finding: every wrong version assumed a fallback
   that does not exist.

   ⚠ **Wrong claim 1 — "refuse `own`, they stay `rmw`". There is no `rmw` to stay
   in.** `rmwCodecRefusal` (`internal/entrypoint/surfacecodec.go:78-95`) refuses
   keyless codecs **by kind**: *"RMW asserts and fills individual keys, so it needs
   an object […] Those surfaces belong in `stateful`/`computed`."* So `assert` is
   the mode a keyless surface **cannot** have, and `own` is the only coherent one.

   ⚠ **Wrong claim 2 — "no shipped host surface is keyless", offered as "the class
   is empty so the carve-out is free".** The conclusion was right and the reason was
   not, which is worse than being wrong: `raw` **is** reachable — it is the default
   codec for any `host_files` entry whose destination is not `.json`/`.toml`
   (`hostFileCodecFor`, `internal/config/hostfiles.go:205-211`). See the scope
   correction below for why that still does not populate *this* class.

   ⚠ **Wrong claim 3 — "adopt them on the host notch, because there the file IS the
   output". That reasoning inverts into the same failure it was avoiding.** Adopting
   a keyless surface makes **the whole file** the overlay, and the overlay outranks
   every declared layer beneath it — so the file would freeze at its adopted content
   and yolo's declarations would never take effect again. `staterender.go:213` says
   exactly this about the jail (*"adoption would mean 'the existing file wins
   outright'"*), and relocating it to the host does not repair it. **Adopting is
   strictly worse than overwriting**, not safer.

   **The scope correction that makes this rulable.** `host_management` governs the
   **host notch**, and `RenderHostPack` walks `p.SurfacesForReport(…)` —
   **a pack's own surfaces** (`internal/entrypoint/hostrender.go:175`). `host_files`
   entries are not pack surfaces; they are a user config key rendered **in the jail**
   (`internal/entrypoint/hostfiles.go`), which is where carrying a host file *into* a
   container belongs. So:

   - **`raw`'s real job is jail-side delivery, not host composition** — which answers
     *"how would we even use raw?"*. In `host_files` it is not a merge strategy at
     all; it is "the bytes are the value", which is what lets capture give a carried-in
     file **edit-survives-regeneration for free** rather than needing a parallel
     mechanism (`staterender.go`'s keyless note).
     > [!NOTE]
     > **"`host_files` means just copying in files" is nearly right, and the exceptions
     > are the interesting part.** **Four** modes, and only one is a copy
     > (`yolo config-ref`, `host_files` → *Modes*):
     >
     > | Mode | What it does | Default for |
     > | :--- | :--- | :--- |
     > | `readonly` | Re-rendered every boot at `0444`; a **host-side edit propagates** on the next launch | a source-bearing entry |
     > | `once` | Seeded when absent, then never touched; in-jail edits persist with no sidecar, later host edits do **not** propagate. Re-seed by DELETING the file | a source-less entry |
     > | `copy` | Overwritten every boot; in-jail edits discarded | implied for a directory |
     > | `capture` | Re-rendered every boot **and** in-jail edits captured into a sidecar that outranks the host layer | **never** — always explicit |
     >
     > `capture` is never a default *"because a captured edit wins over the host file
     > FOREVER (the sidecar never ages out), so implicit capture would silently fork a
     > host-mirrored file the first time a tool rewrote its own config (`npm config
     > set`, any CLI's first run)"*. So a `host_files` entry becomes a real surface
     > composed through the engine (as the `user` pseudo-agent), not a `cp`. Worked
     > examples — `~/.npmrc`, a `starship.toml` with a `managed` key, an inline-`content`
     > ripgrep config, a `capture` surface with `defaults` — are in `yolo config-ref`
     > under `host_files`.
     > **What review is right about is ownership**: yolo never writes the host's copy,
     > so there is no host-notch ownership question for these at all — which is why
     > they are absent from the host apply report.
   - **At the host notch the class is empty**, and only a pack deliberately declaring
     a `raw`/`lines` surface could populate it. None does.

   **So all four options are now visible, and three are bad:** `assert` is impossible;
   `own`-with-adoption freezes the file; `own`-without-adoption overwrites a real
   file in a real home; refusing costs nothing today.

   ##### Why this was scoped to the host notch — and why that was wrong

   **Review pushed back: *"why do you single out the host notch? All should be
   equal, no?"* That is [P5](#1-the-verdict-and-the-principles-it-rests-on), and
   applying it correctly changes the answer.**

   **The RULE is already uniform and should stay so.** A keyless surface renders
   `stateful` or `computed` at every notch — `rmwCodecRefusal`'s own words, ⚠ not
   "`stateful`" alone as this sentence said until 2026-09-11 — and adoption never
   applies to it at any notch. Today the host notch **refuses it per surface and
   carries on** ([`hostrender.go:244`](../../internal/entrypoint/hostrender.go#L244))
   with advice that is circular there — *"declare this surface `stateful` or
   `computed` instead"* — because the host render never reads the mode except to
   skip `unrendered`. Nothing about that is host-specific, and
   "refuse `own`" would have *introduced* an asymmetry rather than removing one:
   `own` is simply the host notch's name for `stateful`, so refusing it would make
   one surface kind behave differently on one notch for no reason the surface knows
   about.

   **What genuinely differs is not the rule but the COST of the TRANSITION** — the
   first render into a home that already has content. ⚠ **Reframed again 2026-09-11**:
   this paragraph used to say the difference was that *"the jail home is disposable
   and the host home is not"*, and [§3](#3-the-diagnosis--one-asymmetry-three-unrelated-justifications)
   now records why that axis is wrong. An **owned** file is disposable wherever it
   lives, because it is reproducible; an **unowned** one is not, wherever it lives.
   The host is not permanently special — it is special *until it is owned*, and a
   jail hits the same crossing whenever a newly-enabled pack starts owning a file
   the home already had. So the correct shape is **same rule everywhere, stronger
   net at the transition**, with the residual host-only fact being narrow: a jail's
   bad transition is survivable by relaunching from a clean home and the host has no
   clean home to fall back to.

   ⚠ **And the existing safety net structurally cannot see this case.** The
   one-way-door gate reads `EntryLosses`, which is defined as *"the NAMED-ENTRY
   casualties of this render: an entry in a table like `mcpServers`"*
   (`internal/entrypoint/hostrender.go:84-97`). **A keyless surface has no tables and
   no named entries**, so `EntryLosses` is always empty for one, `confirmHostLosses`
   returns *"nothing would be lost — no prompt"*, and the render replaces the whole
   file **silently**. The most destructive case available is the one case the gate is
   shaped wrong to notice. It is a gap in the guard, not in the rule — **and by the
   transition framing above it is not host-only**: a keyless surface at a jail's own
   `firstMigration` cannot adopt either (adoption would freeze it), so the same
   whole-file overwrite is available there, against a home that may hold an agent's
   own state. Latent at both notches today, since it needs a pack to declare a
   keyless surface.

   <!-- vantage: oq id=OQ-CO9 leaning="Keep the rule uniform across notches — a keyless surface renders stateful everywhere and is never adopted — and fix the guard instead. A whole-file replacement of a keyless surface is the maximal loss, so it should confirm like an EntryLoss does, and EntryLosses cannot express it because it is defined in terms of named table entries. Refusing `own` for keyless is the cheap fallback if the guard is not worth building, since the class is empty at the host notch, but it introduces a per-notch asymmetry that P5 argues against. The archive (OQ-CO7) is the second half either way." -->

   _Leaning (**fourth version, following review's equality argument rather than my
   earlier scoping**):_ **Keep the rule uniform and fix the guard.** A keyless
   surface renders `stateful` at every notch and is never adopted — unchanged. What
   changes is that a **whole-file replacement is recognised as a loss**: it is the
   maximal one, and `EntryLosses` cannot express it because that field is defined in
   terms of named table entries. Confirm it the way an entry loss is confirmed, and
   let [OQ-CO7](#OQ-CO7)'s archive be the second half. **Refusing `own` for keyless
   remains the cheap fallback** if the guard is not worth building — the class is
   empty at the host notch, so it costs nobody anything — but it buys that cheapness
   with exactly the per-notch asymmetry P5 exists to prevent.

   ⚠ **The guard cannot be the same primitive at both notches, and P5 permits
   that** (sharpened 2026-09-11). "Confirm like an `EntryLoss`" needs a human: the
   host apply is interactive and its stdin exists for exactly that prompt
   ([`apply.go:38`](../../internal/cli/apply.go#L38)), but a jail's `firstMigration`
   runs at an unattended boot — in-jail `yolo apply` is a report, not a provision
   ([`apply.go:111`](../../internal/cli/apply.go#L111)). So the RULE is uniform, and
   the NET splits by the primitive available: a prompt where there is a TTY,
   archive-or-refuse where there is not. That is the one asymmetry P5 allows, and
   it is why [OQ-CO7](#OQ-CO7)'s archive is load-bearing here rather than optional.

   ⚠ **This says nothing about `raw` or `lines` as codecs.** Both survive on their
   jail-side merits: `raw` is the default that stops a hand-written `.jsonc` or
   `.yaml` being reformatted, and `lines` is the line-oriented encoding for
   allowlist-style files (`internal/agentcfg/codec/lines.go:9`) — *"a decent
   motivation, and I could see that being used"*. ⚠ **But the append-merge that
   motivated it does not exist** (checked 2026-09-11): the codec's docstring says the
   engine can *"deep-merge / append over it like any array"*, and the engine
   replaces arrays wholesale — *"Arrays and scalars replace wholesale"*,
   [`engine.go:66`](../../internal/agentcfg/engine.go#L66). So `lines` today is
   `raw` with a per-line encoding; whether the append rule should be built is a
   codec question, not this document's.

   **Answer:**
   > _(empty — fill in when decided; two live shapes — uniform-rule-plus-guard, or
   > refuse-as-cheapest — and the choice is whether the guard is worth building
   > before anyone has a keyless host surface)_

10. 💬 **<a id="OQ-CO10"></a>OQ-CO10: Is `reads-host` coverage a decision, and
    should the grant name a path at all?** Opened in review 2026-09-10 from
    [§5.1.1](#511-why-only-two--and-why-that-is-a-question-not-a-fact-to-design-around).
    Two shipped grants, eleven pack surfaces, no recorded reason for the nine
    without one — and both grants name their own surface's path, so the field is
    a boolean in a path's clothing. **What it decides:** whether `--to host` is a
    destination users can reason about, and whether the other nine surfaces are
    deliberately host-blind or merely unfinished.

    Three sub-questions, and they separate cleanly:

    - **Coverage.** Do the nine get grants, or is host-blindness correct for
      them? `mise/config` is the interesting case *against* a blanket yes —
      importing the host's mise config into a jail would fight the pinned
      toolchain rather than help it.
    - **Shape.** If a grant's source is always its own surface path, should the
      field become a flag (`reads_host: true`) so the two cannot drift apart? A
      path that must equal another path is a bug waiting for its first typo.
    - **Discoverability.** Whatever the answer, `--to host` should say *"this
      surface has no host layer, so nothing will read this"* rather than writing
      a file no jail consults.

    <!-- vantage: oq id=OQ-CO10 leaning="Move the declaration ONTO the surface so the binding is structural instead of a path.Base match, and make the read fail CLOSED. Keep the privilege claim and its disclosure — those come from the declaration existing and being enumerable, not from its being a separate contribution kind. Coverage then becomes a visible per-surface yes/no in review rather than something that can be forgotten, and mise/config is a deliberate no. Regardless, promote must refuse --to host on a surface with no host layer." -->

    _Leaning (**revised in review 2026-09-10**, and the revision is the
    maintainer's, not mine):_ **Package it onto the surface so the binding is
    structural, and make the read fail closed.** My first leaning was "keep the
    separate kind, narrow its field to a boolean" — which treats a redundant path
    as a cosmetic problem. The review asked the better question: *why is a surface
    allowed to exist without it at all?* Chasing that found the binding is a
    `path.Base` match feeding a fail-open read
    ([the basename-match finding](#the-coupling-is-a-basename-match-and-the-read-that-depends-on-it-is-fail-open)
    under [§5.1.1](#511-why-only-two--and-why-that-is-a-question-not-a-fact-to-design-around)),
    which has already shipped one silent-wrong-composition bug. A boolean would
    not have touched that.

    What moves and what does not:

    - **Moves:** the config-surface binding becomes a **policy bit on the surface**
      — *does this surface import the user's own version?* — with the path derived,
      because [there is no second location to name](#there-is-no-second-location-to-name--the-homes-differ-by--and-nothing-else):
      every surface is `~/`-relative and `~` is the only difference between the two
      homes. *A surface with a host layer* and *a surface without one* become the
      only representable states, and the third — **declared but not bound** — stops
      existing.
    - **Does NOT move:** the `reads-host` kind itself, which also carries the user's
      `host_files` entries. Those are arbitrary host files with no mirrored twin, so
      they genuinely need a declared path. Deleting the kind would break them.
    - **Stays:** the disclosure. It comes from the declaration being present and
      enumerable, not from its being a separate kind — the footprint can walk
      surfaces as easily as contributions. Note there is no *gate* to preserve:
      [OQ-TP9](trust-paths.md#decision-ledger) deleted `MayAccessHost` on
      2026-09-04, so a declared grant is unconditionally honored and the claim
      discloses rather than decides.
    - **Also changes:** the read fails **closed**. A surface that declares a host
      layer and cannot read it must refuse rather than compose without it — today
      those two outcomes are the same bytes.
    - **Coverage falls out.** Every surface then visibly says yes or no in one
      place, so `mise/config` becomes a deliberate **no** — importing the host's
      mise config would fight the pinned toolchain — rather than an omission
      indistinguishable from the other eight.

    Unchanged either way: promote must refuse `--to host` on a surface with no
    host layer rather than writing a file nothing reads.

    ⚠ **Two things the 2026-09-11 audit adds.** First, "fail closed" is not only
    about a typo: on `macos-user` the read fails on **every** launch by
    construction — no `/ctx`, no `YOLO_CTX_ROOT`, pack grants neither mounted nor
    filtered — so the fail-open composes `claude/settings` without the user's
    settings on one backend and with them on the others
    ([§5.1.1](#511-why-only-two--and-why-that-is-a-question-not-a-fact-to-design-around)
    item 4). That is a shipped parity defect, and fail-closed would turn it from
    silent into a refusal that names the backend. Second, the relationship to
    [OQ-CO11](#OQ-CO11) is exact rather than loose: **this question dissolves for
    config surfaces iff CO11 honours env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)** (retire the read-in
    layer). Under a per-ownership-value interim it stays live for `none`, and the
    `host_files` half of the kind stays regardless.

    **Answer:**
    > _(empty — fill in when decided)_

11. 💬 **<a id="OQ-CO11"></a>OQ-CO11: Env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) retired the
    read-in `host` layer on 2026-08-01 — does that ruling stand, and is
    adopt-then-promote its migration?** Opened in review 2026-09-10 as *"does the
    `host` layer survive `host_management` at all?"* and **re-scoped 2026-09-11**,
    because the tree already holds an answer. It is **upstream of
    [OQ-CO10](#OQ-CO10)**: if the ruling stands, CO10's packaging question
    dissolves for config surfaces rather than being answered.

    ⚠ **This question was already ruled, and this document re-asked it without
    saying so.** [`environment-manager-plan.md`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase):
    *"**[`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) — Retire the `reads-host` read-in layer? → RESOLVED: YES
    (2026-08-01).** Drop settings-inheritance […]; express personal settings as a
    local pack instead — declared, locked, portable to every notch. […] the `host`
    compose layer and the `reads-host` kind's compose role go away."* Its 2026-08-23
    consumption check records the ruling as **not implemented** — *"`HostSource`
    still exists and the `host` layer still composes"*. The two shipped grants
    predate the ruling (`484a3cfc`, 2026-07-29), so they are residue, not a
    reversal. The maintainer's 2026-09-10 review line — *"why auto-leak from
    host→jail if host is managed? We don't leak from jail to jail automatically"* —
    is the ruling's own reasoning, restated. The previous version of this entry
    called the ruling *"recorded as owed"* and said retirement *"needs the
    migration story env-manager [§3.3](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails) never wrote"*; both were wrong, and the second
    is the interesting one.

    **The migration story WAS written, and it is this document's verb.** The same
    section of [`yolo-as-environment-manager.md`](yolo-as-environment-manager.md#the-host-layer-is-the-input-we-should-retire)
    names the cost and the path: *"today a user's `~/.claude/settings.json` 'just
    works' in the jail with zero setup, and under this direction they would author
    (or `yolo config promote` into) a one-file local pack."* And `own` makes it
    mechanical rather than manual: adoption captures the host file's undeclared
    keys into the host overlay ([§6.3.1](#631-why-the-adoption-diff-is-empty)),
    promote lifts them into the local pack ([§5](#5-promotion--the-way-out-of-capture)),
    and the local pack renders at every notch — so a user on `own` loses nothing
    when the layer goes. Under `assert` and `none` there is no host capture, so the
    path there is *adopt `own` first, or author by hand* — which is exactly the
    ruling's *"author (or promote into)"*.

    **Three arguments the earlier entry made, re-weighed:**

    - **"A loop with no source" was wrong once [OQ-CO3](#OQ-CO3) was ruled.** Under
      `own` the host file is `declared layers + host capture overlay`, and the
      overlay holds the user's own host-side edits — a real source, existing
      nowhere else until promoted. Reading the file back into a jail therefore
      imports *host captures* at the second-weakest precedence. The right argument
      is [P3](#1-the-verdict-and-the-principles-it-rests-on) and
      [P5](#1-the-verdict-and-the-principles-it-rests-on) together: **no notch's
      captures flow to another notch automatically; promotion is the channel** —
      which is what the review said about jails, applied to the host.
    - **The per-value table treated one user act two ways.** The 2026-09-10 shape
      (`none`: on; `assert`: import the user-owned keys; `own`: refused) makes a
      hand edit to `~/.claude/settings.json` reach every jail under `assert` and no
      jail under `own` — so switching to `own` silently cuts a channel the user was
      relying on. The `assert` row's mechanism was sound (a filter over the
      provenance record, importing keys labelled `host` and skipping `retired:` and
      `defaults`), but ⚠ its premise that the record is *"recorded and unused"* was
      false ([§2.3](#23-the-notches-and-what-each-keeps-on-disk) — four consumers),
      and its staleness citation was wrong on location: the host render does write
      `Defaults` into the file, via `applyRMWLayers`' fill-if-absent pass
      ([`prism.go:1067`](../../internal/entrypoint/prism.go#L1067)), not at the two
      `hostrender.go` lines the entry cited, which are prune checks. The staleness
      inference — a pack default returning from the host file above the pack's own
      *current* default — still follows from that and is still unreproduced.
    - **The layer is not at parity today.** On `macos-user` it silently drops
      ([§5.1.1](#511-why-only-two--and-why-that-is-a-question-not-a-fact-to-design-around)
      item 4), which the env-manager section had already said — *"on `macos-user`
      there is no `/ctx`, so the layer silently drops today"* — and which is the
      one input whose *meaning* depends on the backend. Retiring it is the parity
      fix; per-value gating would carry the defect forward.

    **What it decides:** whether `reads-host` keeps any config-surface role;
    whether [§2.1](#21-the-layer-stack)'s stack loses a layer; what `--to host`
    means ([§5.1](#51-surface)); and whether [OQ-CO10](#OQ-CO10) is asked at all.

    <!-- vantage: oq id=OQ-CO11 leaning="Honour OQ-3: retire the read-in host layer for config surfaces at every ownership value, with per-value TIMING rather than per-value semantics — `own` refuses it immediately (adoption captured the keys, promote carries them, nothing is lost); `none`/`assert` keep today's read only until promote ships, then one launch notice naming `yolo config promote` and the local pack. The 2026-09-10 three-row table is at most that interim, not the end state. CO10 dissolves for config surfaces; `host_files` keeps the kind." -->

    _Leaning (**fifth version, and this one is a confirmation of the maintainer's
    own 2026-08-01 ruling rather than a new proposal**):_ **Honour [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase).** Retire
    the read-in layer for config surfaces at every ownership value, with per-value
    *timing* rather than per-value *semantics*: under `own` refuse it immediately —
    adoption captured the keys and promote carries them, so nothing is lost; under
    `none` and `assert` keep today's read only until promote ships, then retire it
    with one launch notice naming `yolo config promote` and the local pack. The
    2026-09-10 table is at most that interim. What this asks the maintainer is one
    thing: does the 2026-08-01 ruling stand now that its migration path exists? If
    yes, [OQ-CO10](#OQ-CO10) dissolves for config surfaces and `host_files` keeps
    the kind; if no, the reversal belongs in that ledger, dated, and the per-value
    table becomes the design.

    **Answer:**
    > _(empty — fill in when decided; the question is now "confirm or reverse
    > env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)", and the migration it lacked is [§6.3.1](#631-why-the-adoption-diff-is-empty) + [§5](#5-promotion--the-way-out-of-capture))_

---

## 13. Decision Ledger

Rulings on [§12](#12-open-questions)'s questions get folded into the normative
body text and compacted into this table, keeping the exact `OQ-CO` ids so
citations from sibling docs and code comments continue to resolve.

**Four settled in review round 0; six still open, three of them opened by the
same rounds — and [OQ-CO11](#OQ-CO11) is upstream of [OQ-CO10](#OQ-CO10).** The
2026-09-11 audit ruled nothing — it is not the maintainer — but it re-scoped CO11
to a confirmation of env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase), narrowed CO6 to a user's-own-surface
niche, and moved CO7's stakes from speculative to measured; the postscript at the
top lists what it overturned.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| [OQ-CO1](#OQ-CO1) | **Three values** (`none`/`assert`/`own`). `assert` is shipped behavior with real users, so collapsing it is either a regression or a forced escalation to `own`. [OQ-CO2](#OQ-CO2) pays down its cost by removing the prompt that would have explained it. | 2026-09-10 | [§4.1](#41-the-key) |
| [OQ-CO4](#OQ-CO4) | **Keep `local` as the default; the guard is the confirmation, not the flag.** Promote lists the selected keys and asks; `--accept-promotion` is the only way past it (NOT `--yes` — the repo's consent pattern names what is approved). Same shape as CO2 — feedback at the point of the act beats ceremony in front of it. | 2026-09-10 | [§5.1](#51-surface) |
| [OQ-CO2](#OQ-CO2) | **Neither prompt nor notice** — the unset state is `assert`, silently. Each value explains itself at the point of the act; `apply --sealed` is the one place an unset key bites. *Against the leaning.* | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) |
| — | **Terminology: the absent key is the *unset* state, never the "undeclared" one** — *undeclared* is reserved for the input-closure tier ([§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running)'s note). Not an OQ; recorded because renaming it later costs four anchors. | 2026-09-10 | [§4.3](#43-the-unset-state-and-what-happens-to-everyone-already-running) |
| [OQ-CO3](#OQ-CO3) | **Yes, under `own` only** — and it is a precondition of adoption, not an added capability: capture-then-regenerate is what makes the first owned render byte-identical. The refusal stays for `none` and `assert`. | 2026-09-10 | [§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to), [§6.3.1](#631-why-the-adoption-diff-is-empty) |

**One consequence worth recording where a reader will hit it, because it moved
work out of this design rather than into it.** Both rulings removed a mechanism
this doc had proposed — a migration prompt and an adoption confirmation — and in
each case the replacement was already shipped: the `assert` default, and
`ComposeStateful`'s first-migration adoption. [§10](#10-what-i-would-build-in-order)'s
step 1 shrinks accordingly (the key, its read, its fail-closed direction, and
the `--sealed` refusal — no prompt to build), and step 6 gains the host capture
store it now depends on.
