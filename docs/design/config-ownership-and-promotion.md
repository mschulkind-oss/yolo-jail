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

**Status:** DESIGN SKETCH, 2026-09-09. Nothing built. Every claim about current
behavior was verified against the tree or measured in this development jail on
2026-09-09; each carries its evidence inline.

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

**The ruling is about a shared file.** "Preserving undeclared keys" is a benefit
only if you want undeclared keys to exist. A user who has adopted `yolo host
apply` as their source of truth wants the opposite: no undeclared keys, full
composition, reproducibility, and a revert. [§6.3](host-render-target.md#63-the-structural-problem-on-a-host-target-the-host-layer-is-the-output) also *explicitly rejects* the
ownership framing — "The reason is **not** 'the editor and yolo are the same
person' (a loose framing)" — so the ruling does not rest on ownership at all. Its
actual load-bearing sentence is the one before: "**(3) is the answer, and it is
already the shipped design for exactly this problem**", i.e. reuse `claude/config`'s
existing `rmw` mode. That is implementation economy. It is a good reason to ship
`rmw` first and not a reason the host cannot have capture.

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

### 4.3 The undeclared state, and what happens to everyone already running

An **absent** key is not a fourth value; it is the *undeclared* state, and it
exists only to make the transition honest:

1. Behavior is `assert` — today's behavior, so nothing breaks on upgrade day.
2. The first `yolo host apply --assert` under an undeclared key **prompts once**
   to write the key into the user config, showing the three values and their
   consequences. On a non-TTY it proceeds as `assert` and prints the
   one-line notice. It never writes the key without an answer.
3. `yolo apply --sealed` **refuses** while the key is undeclared, listing it
   beside the two undeclared inputs it already refuses for
   ([`apply.go:795-825`](../../internal/cli/apply.go#L795-L825)). An environment
   whose host-ownership contract is unstated is not sealed.

That is the whole migration. There is no rewrite of anyone's files at upgrade,
and no host apply behaves differently until its user answers the prompt.

### 4.4 What the key does not do

It does **not** grant approval for any individual write. That distinction is
`host_apply_on_launch`'s hard-won ruling — "THE KEY ENABLES THE MECHANISM. IT
DOES NOT GRANT THE APPROVAL" — and it holds here identically: `host_management:
own` selects the mode, and a launch or an apply that would change the file still
prompts on a TTY and still refuses off one. The two keys are orthogonal and both
are read: `host_management` says *how* the host renders, `host_apply_on_launch`
says *when* a re-render is checked.

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
| `host` | the keys themselves, into the host file | only under `host_management: assert`; refused under `own` (the file is derived — promote to a pack) and under `none` |
| `workspace` | the workspace config | **blocked**: the `workspace` layer is "DECIDED BUT UNWIRED" — see [OQ-CO8](#OQ-CO8) |

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

This reverses a shipped ruling and so it is opened rather than assumed:
[OQ-CO3](#OQ-CO3).

### 6.3 The one asymmetry that survives: deletion

Whole-file composition means yolo may **remove** a key from a real host file.
That is correct under `own` — a derived file contains what the definition says
and nothing else — and it is the single place where "the host is a real home"
genuinely changes the answer.

Two guards, and they are the price of `own`:

1. **Adoption is a diff, not a flag flip.** The first apply after switching to
   `own` prints the full key-level diff — every key that would be added, changed
   and *removed*, with its provenance — and requires confirmation. Off a TTY it
   refuses.
2. **One archive at adoption.** The pre-existing file is copied once to the state
   dir before the first owned render. Not per-apply snapshots: those are a
   different feature with a retention policy, and the risk being covered is
   "adopting `own` ate settings I had," which one archive covers exactly. See
   [OQ-CO7](#OQ-CO7).

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
| A user picks `own`, and yolo deletes settings they cared about | Adoption diff + confirmation + one-time archive ([§6.3](#63-the-one-asymmetry-that-survives-deletion)) |
| Promotion silently demotes a key that then reverts | Precedence check is a **refusal**, not a warning ([§5.4](#54-promotion-moves-a-key-down-the-stack)) |
| A credential is promoted into a pack, and the pack is pushed | Sensitive keys refused by default; `--force` is per-key and named in output |
| The undeclared-state prompt trains people to hit enter | The prompt has no default; `--sealed` refusal is the backstop that does not depend on attention |
| The local pack becomes an unreviewable pile of promoted keys | Every promotion is an ordinary edit to a readable `pack.json`; `yolo pack lint` and `footprint` already report its claims |
| Three-value enum confuses users who wanted a switch | The undeclared-state prompt explains all three at the one moment the choice is being made, rather than in a config reference |
| A jail-side agent cannot promote, so the workflow stalls where the work happens | [OQ-CO5](#OQ-CO5)'s request channel; the refusal names the host command regardless |

---

## 10. What I would build, in order

1. **`host_management`, parsing and validation only** — the key, its user-scope
   read, its fail-closed direction, its `inherit.go` entry, and the `--sealed`
   refusal while undeclared. Nothing changes behavior yet; the contract becomes
   expressible.
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
6. **`own`:** the host notch renders `stateful`, with the adoption diff and
   archive. Last because it is the only step that can lose data, and by then
   promotion exists — which is what makes `own` attractive rather than merely
   strict.

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
- Switching to `own` on a home with hand-written settings shows every removal
  before making one, and the pre-existing file is recoverable afterwards.
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
   [§4.1](#41-the-key), the migration in [§4.3](#43-the-undeclared-state-and-what-happens-to-everyone-already-running),
   and how alternative C in [§8](#8-alternatives-considered) resolves.

   <!-- vantage: oq id=OQ-CO1 leaning="Three values. `assert` is the shipped behavior and has real users — including this maintainer today — so collapsing it means either a regression or a forced escalation to `own`." -->

   _Leaning:_ Three. `assert` is not hypothetical — it is what is running on this
   maintainer's machine right now, and collapsing it means either regressing that
   or force-escalating it to `own` without asking.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CO2: Should the undeclared state prompt, or just warn?** [§4.3](#43-the-undeclared-state-and-what-happens-to-everyone-already-running)
   proposes that the first `host apply --assert` under an absent key stops and
   asks. The alternative is to print a notice and carry on as `assert`, leaving
   `--sealed` as the only place the undeclared state bites. This decides whether
   "no question anymore" is achieved for people who never read a config
   reference, at the cost of one interruption in an established workflow.

   <!-- vantage: oq id=OQ-CO2 leaning="Prompt once. The goal is to eliminate the ambiguity, and a notice that carries on is exactly the mechanism that let the current ambiguity persist." -->

   _Leaning:_ Prompt, once, with no default. A notice that proceeds anyway is the
   same mechanism that let this ambiguity survive this long — and the prompt fires
   at most once per machine.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CO3: Does `own` re-permit host-side capture, reversing the
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

   **Answer:**
   > _(empty — fill in when decided)_

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
   [§6.3](#63-the-one-asymmetry-that-survives-deletion) proposes a single copy
   of the pre-existing file taken once, when a home first becomes `own`. Per-apply
   snapshots would cover more (a bad pack update deleting a key months later) at
   the cost of a retention policy and a new disk surface — in a project actively
   reducing both.

   <!-- vantage: oq id=OQ-CO7 leaning="One archive at adoption. It covers the risk being taken (adopting `own` ate my settings); the later-regression case is what git on the pack is for." -->

   _Leaning:_ One archive. It covers the risk actually being taken. The
   later-regression case is covered by the pack being in git, which is the whole
   arrangement `own` is recommending.

   **Answer:**
   > _(empty — fill in when decided)_

8. 🔒 **OQ-CO8: Is `--to workspace` in scope?** Promoting a key to the workspace
   config would be the natural home for a genuinely project-specific value.
   Blocked: the `workspace` layer is recorded as "DECIDED BUT UNWIRED" in
   [`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md) —
   no `agent_config.<agent>` key exists and `Inputs.Workspace` is a real engine
   slot every caller passes nil for. This cannot be answered until that layer is
   either wired or retired.

   <!-- vantage: oq id=OQ-CO8 leaning="Blocked, not leaning: the workspace layer is decided-but-unwired, so a promote destination pointing at it would write a key nothing reads. Wire or retire that layer first, then ask this." -->

   **Answer:**
   > _(blocked — wiring the `workspace` layer decides it)_

---

## 13. Decision Ledger

Nothing settled yet — this doc is at Phase 1. Rulings on
[§12](#12-open-questions)'s questions get folded into the normative body text and
compacted into this table, keeping the exact `OQ-CO` ids so citations from
sibling docs and code comments continue to resolve.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| — | — | — | — |
