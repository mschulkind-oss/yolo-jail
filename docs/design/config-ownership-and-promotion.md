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

**Status:** DESIGN SKETCH, 2026-09-09; **review round 0 folded in 2026-09-10**
([OQ-CO2](#OQ-CO2) and [OQ-CO3](#OQ-CO3) ruled, [OQ-CO9](#OQ-CO9) opened).
Nothing built. Every claim about current behavior was verified against the tree
or measured in this development jail on 2026-09-09, and the round-0 claims about
`ComposeStateful` on 2026-09-10; each carries its evidence inline.

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

**Reads with:** [`host-render-target.md`](host-render-target.md) (the doc whose
[§6.3](host-render-target.md#63-the-structural-problem-on-a-host-target-the-host-layer-is-the-output) ruling this proposes to re-scope, and whose `Target`/notch vocabulary this
builds on), [`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md)
(the layer stack and the capture overlay, as designed),
[`environment-manager-user-stories.md`](environment-manager-user-stories.md)
(story 1 is this problem told from the user's side; its `Q1` is the question this
doc tries to close), [`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md)
(Phase 5.3 is the unbuilt half),
[`../guides/migrating-to-packs-and-host-management.md`](../guides/migrating-to-packs-and-host-management.md)
(the user-facing guide, corrected 2026-09-09 in `a23b1fea` after it claimed the
host layer no longer existed).

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
  only shipped exit is `yolo config reset`, which discards. Three shipped
  messages advise "promote" as English prose for a verb that does not exist
  ([`apply.go:818`](../../internal/cli/apply.go#L818)).
- **P5 — The host is a notch like any other, except where a real home forbids
  it.** Same modes, same verbs, same sidecars. The single legitimate asymmetry
  is *deletion* — see [§6.3](#63-the-one-asymmetry-that-survives-deletion).

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
and for whole-file composition on the host is on disk today and nothing consumes
it.

### 2.4 The verbs that exist, and the ones that do not

`yolo config` dispatches `ls, render, diff, reset, capture, drift, dump`
([`config.go`](../../internal/cli/config.go)). There is no `promote`.
`yolo host apply` takes `--assert`, `--dry-run`, `--shell-init` — there is no
`--revert`, though [`host-render-target.md`](host-render-target.md) shows one as
an example and names the missing memory as the reason.

Host-side `capture` and `reset` **refuse** unless `--force`
(`refuseHostSideWrite`, [`configdiff.go:84-90`](../../internal/cli/configdiff.go#L84-L90)),
on privacy grounds: capture would copy whatever is in the real file — an API key
included — into a workspace sidecar.

### 2.5 The three existing host user-scope keys

`host_files`, `host_wrappers` and `host_apply_on_launch` share one construction
([`hostapplyonlaunch.go`](../../internal/config/hostapplyonlaunch.go),
[`inherit.go:205-220`](../../internal/config/inherit.go#L205-L220)): read from the
**user** config directly rather than the merged config, so workspace scope is
*inexpressible* rather than merely refused; fail **closed** on an unreadable
config; and refused for inheritance into a nested jail. That construction is the
security boundary for any key that licenses writing the real `$HOME`, and
[§4](#4-declaring-ownership--the-host_management-key)'s new key joins it rather
than inventing a fourth shape.

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
compose+capture the file is a pure function of its inputs; under `rmw` the file
*is* the state, because `rmw`'s defining property — never touch what you do not
own — means the file holds bytes that exist nowhere else. That is not a missing
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

**(4) Host capture refuses for a *third*, unrelated reason** — a credential leak
into a workspace sidecar, not "it would be meaningless here". Three different
justifications for one asymmetry, and they do not compose into a story.

> [!IMPORTANT]
> **What survives the critique.** One asymmetry is real and does not go away by
> declaring ownership: the jail home is disposable and regenerated, the host home
> is not. Whole-file composition on the host authorizes yolo to **delete** keys
> from a real file in a real home — including keys a future agent version adds
> that no pack knows about yet, and keys another tool wrote. In a jail that costs
> a workspace's overlay; on the host it costs the machine's config, and nothing
> snapshots it. This is an argument for caution about *deletion*
> ([§6.3](#63-the-one-asymmetry-that-survives-deletion)) and **not** an argument
> against capture: capture is about surviving regeneration, and nothing stops
> yolo recording "the human changed `permissions.defaultMode` since the last
> apply" with no whole-file compose at all. That recording is what the current
> `⚠ would overwrite your existing value` warning gropes toward, one apply at a
> time, with no memory between them.

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

- Under `none`, the host file is still **read** into every jail as the `host`
  layer. Ownership governs writing; the read is a separate grant the `claude`
  pack makes with `reads-host`, and nothing here changes it.
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
  nobody could read has granted no write claim.

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
   point of the act instead. `yolo host apply` under `none` reports that it is
   writing nothing and names the key that decided it; `assert` and `own` do what
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
>   `FirstApply && EntryLosses`, so a home yolo has asserted before "prompts not
>   at all". A scalar whose value merely changes is reported as an ordinary `⚠`
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
`Q1` leans on, and that three shipped messages already name in prose.

The punchline for the cross-jail question: **the conventional local pack is the
channel.** `~/.config/yolo-jail/local/` needs no `packs` entry, is selected
implicitly when it exists, is re-read live every launch, and its
`config-overlay` contributions render at *every* notch. Measured 2026-09-09: a
local pack carrying a `config-overlay` on `claude/settings` rendered into a
scratch host home under `yolo host apply --assert`, reported as
`config-overlay keys from: local`. So promoting a key into the local pack
delivers it to every jail *and* the host in one move. Promotion does not need a
distribution mechanism; it needs a destination.

### 5.1 Surface

```console
$ yolo config promote <agent> [--surface <name>] [--keys a,b] [--to <dest>]
                              [--plan] [--json] [--answers <file>] [--yes]
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

**Yes, pack-declared, and that half is right.** `reads-host` is a **privilege
claim**, not a convenience: it carries a real file out of the user's home into a
container. It is `ReviewWorthy` in the footprint and is honored-and-disclosed for
every pack (`internal/packload/footprint_test.go:74-98`), which is what puts it
on the startup disclosure banner. A generic rule — *"every surface imports its
own host file"* — would make every jail read host state with nothing declaring
it and nothing to disclose, which is the shape
[`gate-placement-principle.md`](../reference/gate-placement-principle.md) exists to refuse.
Keep the declaration.

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

So the honest answer to *"why allow a surface without it"* is: **no reason was
ever given**, and the separation is not a design with a rationale — it is two
declarations naming the same file, joined by a string match, guarding a read that
cannot tell "no host layer" from "I could not find it".

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
   what the layers produce anyway. This is free and it is most of the noise: of
   the six captured keys in this development jail on 2026-09-09, four were
   redundant.
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
([§2.1](#21-the-layer-stack)). So promotion can defeat itself: a key that won as
a capture may lose to `computed`, `transform` or `managed` once declared, and the
user would see their value silently revert on the next boot having just been told
it was promoted.

This is a real defect class, not a corner case — `permissions` is captured in
this development jail today and is a `managed` key, so it is exactly the shape
that would break. Promote must therefore evaluate the destination's precedence
before writing, and a key that would lose is **refused by name with the reason**,
never promoted with a warning. See [OQ-CO6](#OQ-CO6) for whether a third option
(offer the pack's `managed` block, which does win) should be on the table.

### 5.5 Where promote may run

The capture sidecar is at `<workspace>/.yolo/prism/`, which is a host directory —
so the **host** can read a jail's captures without any channel. The destinations,
however, are host-side files a jail cannot reach: `~/.config/yolo-jail/local/` is
not mounted into a jail, and by design (the credential boundary).

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

**Adoption, however, is not that case.** The first owned render is
byte-identical to the file already on disk, and it is so *by construction*
rather than by a guard this design adds: `capture-then-regenerate` is what
`stateful` adoption already does.

### 6.3.1 Why the adoption diff is empty

`ComposeStateful` treats "no trusted `last_render` for this surface" as a
**first migration** and, on that branch, seeds the overlay from the file it
finds: `residue = mergeDiff(pureRender, current)`
([`staterender.go:174`](../../internal/agentcfg/staterender.go#L174)). The next
line of the render is then `declared layers + that residue`, which reproduces
the file exactly. Switching a host surface to `own` is precisely that branch —
there is no host `last_render` yet — so **capture happens before the first owned
write, and the write puts back what capture just took**.

This is not a hopeful reading of the mechanism. That branch exists *because*
seeding an empty overlay was a shipped data-loss bug (`copilot/config` collapsed
to `{"yolo": true}` and logged the user out); the comment above it is labelled
`B1 (⚠ DATA LOSS FIX)`. The engine's answer to "adopt a file I did not write"
is already the one this section wanted a confirmation prompt to protect.

**For a home already on `assert`, the diff is empty for a second and stronger
reason:** every key `own` would drop, `assert` has already dropped. `rmw`
re-asserts the declared keys on every apply, so a key that collides with yolo's
declarations does not survive under `assert` either — the two modes differ only
about keys *nothing* declares, and those are exactly what adoption captures.

### 6.3.2 The three classes adoption does not cover

Stated because "empty by construction" is a claim with edges, and each edge is a
deliberate line in the engine rather than an oversight:

| Class | What happens | Why |
| :--- | :--- | :--- |
| A key nested inside a container yolo computes wholesale (a hand-added `mcpServers` entry) | **not adopted** | `dropYoloOwnedSubtrees` ([`staterender.go`](../../internal/agentcfg/staterender.go)) — adopting it would resurrect a dropped entry and break *regenerate, don't reconcile* |
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

The first two are the loss set `confirmHostLosses` **already** gates on
(`FirstApply && EntryLosses`, [§4.4](#44-what-the-key-does-not-do)) — so the
case that can still lose something is the case that already prompts, and it
prompts for the same reason on `assert` today. No new guard is needed for it.

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
"adoption's classification was wrong about one of the three rows above". Cheap
insurance against a mechanism, rather than the mechanism's only safety net. See
[OQ-CO7](#OQ-CO7).

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
- **No new secret handling.** Sensitive keys are *refused*, using the deny-list
  and pack declarations that exist. No redaction, no vault, no taint propagation.
- **No `pack import`.** Promotion moves *captured keys*, not an existing
  `~/.claude/settings.json`, into a pack. Wholesale adoption of a hand-written
  file stays manual re-authoring.
- **The `guest` notch is untouched.** It has no provenance dir and no sidecars
  today; giving it an ownership contract is a separate question.
- **No automatic promotion, ever** — see [§5.7](#57-forbidden-behavior).

---

## 8. Alternatives considered

**A. Leave it alone; document the asymmetry better.** The migration guide's
correction (`a23b1fea`) is most of this. **Rejected** — it addresses the
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
| Promotion silently demotes a key that then reverts | Precedence check is a **refusal**, not a warning ([§5.4](#54-promotion-moves-a-key-down-the-stack)) |
| A credential is promoted into a pack, and the pack is pushed | Sensitive keys refused by default; `--force` is per-key and named in output |
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
   before anything depends on it more heavily.
4. **`yolo config promote --plan --json`** — classification and precedence
   checks, emitting the plan, writing nothing. The dangerous half is the write;
   the valuable half is the analysis, and it can ship first.
5. **Promote's write path** to `local` and `pack:<name>`, with the atomic
   write-and-reset.
6. **`own`:** the host notch renders `stateful`, **with the host capture store
   ([§6.2](#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)) and
   host-side `reset` in the same commit** — adoption is unsafe without either
   ([§6.3.3](#633-what-survives-as-a-guard)) — plus the one-time archive and the
   keyless carve-out [OQ-CO9](#OQ-CO9) rules. Last because it is the only step
   that can lose data, and by then promotion exists, which is what makes `own`
   attractive rather than merely strict.

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
  losing layer named — verified by promoting a `managed` key and observing the
  refusal.
- Switching to `own` on a home already applying under `assert` changes **zero
  bytes** — the measurable form of [§6.3.1](#631-why-the-adoption-diff-is-empty),
  and the criterion to write the test for first. Switching on a home that never
  applied loses only what a first `assert` apply would have lost, prompts for it
  through the gate that already exists, and leaves the pre-existing file
  recoverable from the archive.
- A `yolo host apply --assert` under `assert` still leaves an undeclared key
  byte-identical — the property measured on 2026-09-09 and the one thing this
  design must not regress.

---

## 12. Open Questions

1. 💬 **OQ-CO1: Two values or three?** Does `assert` earn its place, or is the
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

   **Answer:**
   > _(empty — fill in when decided)_

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

4. 💬 **OQ-CO4: Is the conventional local pack too blunt a default destination?**
   It is implicitly selected in every jail and renders at every notch, so
   `--to local` is genuinely "everywhere, forever" — which is the feature, and
   also means a casually promoted key has the widest possible blast radius with
   the least ceremony. The alternative default is to require `--to` explicitly so
   the scope is always a stated choice.

   <!-- vantage: oq id=OQ-CO4 leaning="Keep `local` as the default but print the blast radius in the confirmation — 'this reaches every jail and your host'. Requiring --to punishes the common case to guard a reversible edit." -->

   _Leaning:_ Keep the default, and make the confirmation state the blast radius
   in words. The edit is a readable line in a `pack.json` and trivially undone;
   requiring `--to` taxes the common case to guard a reversible one.

   **Answer:**
   > _(empty — fill in when decided)_

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

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-CO6: When a promoted key would lose precedence, may promote offer the
   `managed` block?** [§5.4](#54-promotion-moves-a-key-down-the-stack) refuses
   such a key. A pack's own `managed` layer *does* win — so promote could offer
   to write there instead, which would make the promotion work. It would also
   hand users a routine way to assert keys above `computed` and `transform`,
   which those layers exist to prevent.

   <!-- vantage: oq id=OQ-CO6 leaning="Refuse only; do not offer managed. A user who genuinely wants to override a computed key can write the managed block by hand, and having to do so by hand is the friction that keeps it rare." -->

   _Leaning:_ Refuse only. Writing a `managed` block by hand stays possible, and
   the friction is the point — a one-keystroke path to outranking
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

   <!-- vantage: oq id=OQ-CO7 leaning="One archive at adoption. It covers the risk being taken (adopting `own` ate my settings); the later-regression case is what git on the pack is for." -->

   _Leaning:_ One archive. It covers the risk actually being taken. The
   later-regression case is covered by the pack being in git, which is the whole
   arrangement `own` is recommending.

   **Answer:**
   > _(empty — fill in when decided)_

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

    - **Moves:** the declaration goes on the surface, so `HostSource` is populated
      from the surface's own manifest entry rather than matched to a sibling
      contribution. *A surface with a host layer* and *a surface without one*
      become the only representable states, and the third — **declared but not
      bound** — stops existing.
    - **Stays:** the privilege claim and its disclosure. Both come from the
      declaration being present and enumerable, not from its being a separate
      kind; the footprint can walk surfaces as easily as contributions.
    - **Also changes:** the read fails **closed**. A surface that declares a host
      layer and cannot read it must refuse rather than compose without it — today
      those two outcomes are the same bytes.
    - **Coverage falls out.** Every surface then visibly says yes or no in one
      place, so `mise/config` becomes a deliberate **no** — importing the host's
      mise config would fight the pinned toolchain — rather than an omission
      indistinguishable from the other eight.

    Unchanged either way: promote must refuse `--to host` on a surface with no
    host layer rather than writing a file nothing reads.

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

9. 💬 **<a id="OQ-CO9"></a>OQ-CO9: What does a KEYLESS host surface do under
   `own`?** Opened in review 2026-09-10 by the [§6.3.2](#632-the-three-classes-adoption-does-not-cover)
   table. `stateful` adoption is object-only by deliberate design — a `raw` or
   `lines` surface has one "key", the whole file, so adopting it would mean "the
   existing file wins outright" and a host-mirrored file would freeze at stale
   content forever ([`staterender.go:213`](../../internal/agentcfg/staterender.go#L213)).
   That reasoning is sound in a jail, where the file is disposable. On a real
   host it means the first owned render of a keyless surface **overwrites**
   without the capture step that makes every object surface non-destructive.
   Options: refuse `own` for keyless surfaces (they stay `rmw` regardless of the
   key); adopt them after all, accepting the frozen-content failure mode on the
   host only; or archive-and-overwrite, leaning on
   [OQ-CO7](#OQ-CO7)'s copy as the whole safety net for this one class.

   **Stakes are currently theoretical and that is the reason to rule it now:**
   no shipped host surface is keyless, so nothing regresses on the day `own`
   lands — which is exactly the condition under which an unruled case gets
   built wrong by the first person who adds one.

   <!-- vantage: oq id=OQ-CO9 leaning="Refuse `own` for keyless surfaces — they stay `rmw` whatever the key says, reported at apply. It is the only option that cannot lose a real host file, and the class is empty today so the carve-out costs nobody anything. Revisit if a keyless host surface ever has a reason to be derived." -->

   _Leaning:_ **Refuse `own` for keyless surfaces** — they stay `rmw` whatever
   the key says, and the apply reports that it did so. It is the only option
   that cannot lose a real host file, the class is empty today so the carve-out
   costs nobody anything, and a per-surface exception is honest in a way that a
   silently different adoption path is not.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 13. Decision Ledger

Rulings on [§12](#12-open-questions)'s questions get folded into the normative
body text and compacted into this table, keeping the exact `OQ-CO` ids so
citations from sibling docs and code comments continue to resolve.

**Two settled in review round 0; five still open, plus one opened by the same
round.**

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
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
