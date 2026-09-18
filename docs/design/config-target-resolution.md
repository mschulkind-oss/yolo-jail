---
title: "Which home does `yolo config` mean? — two predicates, one report, no disclosure"
date: 2026-09-16
status: accepted
tags: [design, config, cli, notch, workspace, capture, disclosure]
summary: "A `yolo config` verb answers about a home and a sidecar store it never names, and it picks them with two independent predicates — the cwd for the store, YOLO_VERSION for the home. So one report describes two homes, a `cd` silently changes the answer, and the one write verb refuses for a hazard only half its work has. One resolved target per invocation, disclosed on every invocation, selected by the `--at` the write verbs already have."
vantage:
  status-chip: true
---

# Which home does `yolo config` mean? — two predicates, one report, no disclosure

**Status:** DESIGN, 2026-09-17 — **all eight questions are ruled**
([§10](#10-decision-ledger)); nothing built. The eighth was filed and closed the same day: the
review remembered a ruling I had not found, and there are two of them in opposite directions
([OQ-CR8](#oq-cr8)). Every current-behavior claim below was
measured against `46142522` on 2026-09-16 — by running the shipped `yolo` from two cwds, and
by one throwaway probe test for the `own` case, which is stated where it is used.

> **In short.** A `yolo config` verb is about a home and a capture store, and it names
> neither — it picks the store from the **cwd** and the home from **`YOLO_VERSION`**,
> independently. Those two can disagree, so a single report can describe two homes; and
> because nothing is disclosed, the reader cannot tell which one they are looking at.

**Why it matters.** Four failures, all measured, all silent. The one that started this: a
host-side `yolo config diff claude` in `~/code/swarf` printed that jail's captured edits and
the *invoking user's real home's* overlay provenance as two blocks of one report, and then
`yolo config reset claude` refused — for a hazard that applies to half of what reset does.
Nothing in either output named a home, a workspace, or a notch.

**The shape.** One resolved **config target** per invocation — notch, workspace, store, home
root, may-write — computed once from `--at` plus the cwd, replacing `surfacesAreLocal()` and
the unconditional `prism*` readers; one **disclosure line** every verb prints before its
report; and `--at`, which `yolo apply` already has, extended to the read verbs.

**Cost.** Every `yolo config` verb grows a line of output. Two answers that are silently
empty today become refusals. `--at` on a read verb is new surface that must mean exactly what
it means on `apply`, or it is worse than not having it.

**Start at [§3](#3-one-resolved-target)** — the resolution. Every other section falls out of it.

**Needs your ruling:** nothing. **All eight questions are ruled** ([§10](#10-decision-ledger)),
on 2026-09-17 across two review rounds — the eighth on the authority of two earlier rulings
this doc had not found: the cwd selects the **target**; a
workspace is marked by a launch artifact *or* a workspace config, and a directory that is
neither resolves the host; `diff`/`ls` read the target's store; `diff` reports the captured
divergence and nothing else; a preview reads the STAGED host bytes or none; a host-side `reset`
lands with the running-jail refusal; and the disclosure is unconditional.

**Reads with:** [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) (the
**write** side, BUILT: ownership is declared in one user-scope key, and host-side `reset` is
`own`-only — this doc changes none of that);
[`host-render-target.md`](host-render-target.md) ([§6.6](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)'s
ruling *"the `cwd` selects nothing"*, which is the host-render half of the sentence this doc
is the read half of); [`../reference/report-tiers.md`](../reference/report-tiers.md)
([P4](../reference/report-tiers.md#principles): a disclosure is never suppressible);
[`config-target-resolution-plan.md`](config-target-resolution-plan.md) (the implementation
sketch — incomplete, and unstable while these questions are open).

---

## 1. The verdict, and the principles it rests on

**Build the resolution, then the disclosure, then the verbs' behavior.** The disclosure is the
cheapest of the three and fixes the most, but it cannot be written honestly until there is one
answer to disclose — today a `config diff` would have to print two lines naming two homes,
which is the defect, not a report of it.

- **P1. A config verb names the home it is about, every time.** Not on a flag, not only when
  it is surprising. The reader cannot infer it: the two things that decide it are an
  environment variable and a directory walk, and neither is visible in the output.
- **P2. One resolution per invocation.** A verb resolves its target once, and every path it
  takes — the store, the surface file, the provenance record, the presence check — reads that
  one answer. Two predicates are how one report came to describe two homes.
- **P3. Unknown is not empty.** Where a verb cannot know, it says so rather than answering.
  This repo already states the rule for reapers — *"unreferenced and I could not ask the
  runtime are the same empty answer, so a reaper that cannot ask declines"*
  ([`AGENTS.md`](../../AGENTS.md)) — and every failure in [§2.3](#23-the-four-failures) is
  that rule broken in a report instead of a sweep.
- **P4. The selector is explicit, and it is `--at`.** `yolo apply --at jail|guest|host` is the
  shipped answer to "which notch does this verb act on". A second spelling for the read verbs
  would be a second vocabulary for one fact.
- **P5. No jail-scope input gains authority over a real home.** Whatever the cwd decides, it
  decides which *jail* is being described — never what yolo asserts into `$HOME`. That is
  [`host-render-target.md`](host-render-target.md)'s ruling and it is untouched here.
- **P6. yolo never reads back a file it writes.** A `readsHost` layer exists to carry the
  USER's bytes into a jail. The moment yolo writes that file — `assert` rewrites its declared
  keys into it, `own` composes it whole — those bytes stop being purely the user's, and
  reading them back makes a key yolo wrote indistinguishable from one the user wrote. **The
  rule is stated on the WRITE, not on the notch's name**, because the mechanism is identical
  in both managed modes; what differs is only how much residue it can leave. Under `own` it is
  every key; under `assert` it is the keys that have LEFT yolo's declaration set, since the
  rest are re-asserted at higher precedence anyway — which is the narrower case and the harder
  one to notice. This is the rule `SkillTarget.HostSource` was deleted for.
- **P7. Adopting yolo requires no migration. Adopting host management does.** Someone who
  installs yolo and launches a jail keeps the `~/.claude/settings.json` they already have,
  transparently — no pack to author, no key to promote, nothing to learn. That is what
  `readsHost` is FOR, and it is the onboarding path rather than a convenience. The pack
  migration is the price of `host_management: assert|own`, charged when the user asks for that
  and **never before**: the conventional local pack is a destination, not a prerequisite.

---

## 2. What exists today, stated precisely

### 2.1 Two predicates, resolved independently

| | What it decides | How | Where |
| :--- | :--- | :--- | :--- |
| **The workspace** | which sidecar store the capture half reads: `<root>/.yolo/prism/` | cwd, walked up to the nearest dir holding a `.yolo/` — **else the cwd itself** | `workspaceRoot()`, [`internal/cli/configls.go:338`](../../internal/cli/configls.go) |
| **The notch** | which home the surfaces live in, which provenance record is read, whether a write is allowed | `YOLO_VERSION` set **and** `workspaceRoot() == "/workspace"` | `surfacesAreLocal()`, [`internal/cli/configls.go:388`](../../internal/cli/configls.go) |

Neither is printed. There is no `Workspace:` line and no notch line in any `yolo config`
output; the only thing on screen is the version banner's `host` / `in-jail` word, which is a
fact about the process, not about the answer.

**The cwd never selects the host notch.** Host-side, `surfacesAreLocal()` is false whatever
directory you stand in — so the host-fallback shape this looks like from outside is not what
is happening. What the cwd silently selects is the *store*, and outside a workspace it
selects one that does not exist.

### 2.2 What each verb resolves, today

| Verb | Capture store | Surface file / home | Provenance | Write guard |
| :--- | :--- | :--- | :--- | :--- |
| `ls` | cwd's workspace (`prism*`) | presence is *claimed true* host-side | — | read-only |
| `diff` | cwd's workspace (`prism*`), **always** | — | the **notch's**: jail sidecar in-jail, host record host-side | read-only |
| `render` | not read (says so) | composes for the **jail**, reads its `host` layer from `expandHome(s.Path)` — the invoking process's home | — | read-only |
| `capture` | cwd's workspace | `expandHome(s.Path)` | — | refuses unless local, `--force` (privacy: `own` does **not** unlock it) |
| `reset` | **the notch's**: workspace tree, or the host capture store under `own` | `expandHome(s.Path)` | — | refuses unless local or (`own` and host-side), `--force` |
| `promote` | cwd's workspace, deliberately | writes `~/.config/yolo-jail/…` | — | refuses **in-jail** — host-side only |
| `drift`, `dump` | — | the cwd's workspace config vs its boot baseline | — | read-only |

**What `diff` is for** *(stated in review, 2026-09-17, and it settles both the naming question
and [OQ-CR7](#oq-cr7)).* `yolo config diff` answers *"if I deleted all of these surfaces,
discarded every capture, and regenerated them, how would what I have now look different?"*
A pure regeneration is exactly what the render produces, so the only thing that can survive
that wipe is what was **captured** — which makes the capture store `diff`'s subject **by
definition**, not by implementation accident, and makes `diff` the right word for it. Every
other fact the verb prints has to justify itself against that sentence.

Two rows are load-bearing and easy to miss. `promote` reading the workspace tree host-side is
**deliberate and correct** — its whole job is lifting a jail's captured keys into a pack
([`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)
[§5.2](config-ownership-and-promotion.md#52-what-it-does-in-order)) — so "host-side reads the workspace
tree" is not a bug per se; it is a choice nobody wrote down for the other verbs. And `reset`
is the only verb whose store is resolved through `render.Target`, which is why it is the only
one that can disagree with `diff`.

### 2.3 The four failures

**F1 — one report, two homes.** Host-side `yolo config diff claude` in a workspace prints the
capture block from `<workspace>/.yolo/prism/` (that jail's edits) and the `config-overlay`
provenance block from `<home>/.local/share/yolo-jail/host-provenance/` (the invoking user's
real home). The provenance lines say *"at the host notch"*; the capture lines above them say
nothing, so the report reads as one home's story. Both halves are individually deliberate —
`surfaceProvenance` switches on the notch *on purpose*, precisely so it does not report one
notch's outcome as another's ([`configdiff.go:440`](../../internal/cli/configdiff.go)) — which
is what makes this a resolution defect rather than a bug in either reader.

**F2 — a `cd` changes the answer, silently, at exit 0.** Same jail, same files, only the cwd
differs (measured 2026-09-16):

```console
$ cd /workspace && yolo config ls          # claude/settings … 2 keys ⚠   (11 surfaces)
$ cd /tmp/notaworkspace && yolo config ls  # claude/settings … –          (15 surfaces)
$ cd /tmp/notaworkspace && yolo config diff claude
No captured in-jail edits for claude.      # rc=0
```

Three separate wrongnesses in one move: the divergence disappears (the store resolved to
`/tmp/notaworkspace/.yolo/prism`, which does not exist), four extra surfaces appear (presence
became "unknowable", so the existence filter stopped applying —
[`configls.go:145`](../../internal/cli/configls.go)), and the negative is stated with the same
confidence as a real one. Per [P3](#1-the-verdict-and-the-principles-it-rests-on) the answer has to name what it
resolved, and under [OQ-CR2](#oq-cr2)'s ruling that directory resolves the **host** target and
says so — rather than reporting a workspace's silence as a workspace's answer.

> [!WARNING]
> **In a jail, `$HOME` is a false workspace.** `/home/agent/.yolo` exists — it is the anchor
> for the generated `bin/block` and `bin/launch` dirs — so `workspaceRoot()` stops there, and
> from `cd ~` the verbs describe a workspace at `/home/agent` that has never existed.
> Measured: `cd ~ && yolo config diff claude` → *"No captured in-jail edits for claude"*, with
> the real captures sitting in `/workspace/.yolo/prism`. Any marker-based fix
> ([OQ-CR2](#oq-cr2)) has to exclude this directory, and a bare `.yolo/` test cannot — which is
> why the ruled marker is an artifact rather than a directory. Since 2026-09-17 nothing can
> *create* a workspace there either: `paths.WorkspaceScopeBreach` refuses a launch whose
> workspace is the home and refuses to create the state dir, so no `config-boot.json` can ever
> appear in one. The anchor directory itself still exists in every jail, which is exactly why
> the marker, and not the directory, has to be the test.

**F3 — under `own`, `diff` and `reset` describe different stores.** `reset` resolves its two
sidecars through `render.Target` (`resetCapturePaths`, [`configdiff.go:174`](../../internal/cli/configdiff.go)),
so on a host with `host_management: own` it acts on
`<home>/.local/share/yolo-jail/host-capture/`. `diff` calls `readOverlayValue` →
`prismOverlayPath` unconditionally. `hostOwnsSurfaces()` is consulted by the write guard, by
`resetCapturePaths`, by the trailer that names the re-rendering command, by the baseline's mode
and by the host truncation — `rg -n hostOwnsSurfaces internal/cli` is the whole list, and **no
read path is in it**. MEASURED with a throwaway probe on 2026-09-16 — an owned home whose host
capture store holds one edit:

```
surfacesAreLocal=false hostOwnsSurfaces=true
reset would act on:  …/.local/share/yolo-jail/host-capture/claude-settings.overlay.json
diff reads:          …/002/.yolo/prism/claude-settings.overlay.json
configDiff rc=0 → "No captured in-jail edits for claude surface settings."
```

So on an owned host, the shipped inspect-then-undo pair is broken in the direction that
matters: `diff` and `ls` report no divergence, and `reset` then discards an edit the user was
never shown. This is also the case
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)
[§6.2](config-ownership-and-promotion.md#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)
warned about in as many words — *"the directory is RESOLVED, never hand-built"*, naming the
CLI's `prism*` twins as *"a standing hazard"* and the next hand-built path as the next pair.
The hazard landed in the readers rather than in a new writer.

**F4 — `render` previews the jail's file with the wrong home's `host` layer.** `renderSurface`
substitutes the container workspace so that *"what `render` prints is what the jail gets"*, and
then reads the `host` layer from `expandHome(s.Path)` — the **destination**, in the invoking
process's home ([`config.go:310`](../../internal/cli/config.go)). The boot render reads it from
`surface.HostSource`, a `/ctx` path derived by `packload`
([`entrypoint/packsurfaces.go:509`](../../internal/entrypoint/packsurfaces.go)). So the preview
composes the user's real dotfile host-side, and the jail's own previous output in-jail, as a
layer that at boot comes from neither. The predicate for *which* surfaces get a host layer was
fixed to match the boot path; *where the bytes come from* was not.

**F5 — `yolo apply --sealed` reports "sealed" from a directory that is not a workspace.**
Found 2026-09-17 while writing the plan, in a verb this design had not looked at. `applySealed`
calls BOTH retired predicates, so outside a workspace it resolves an empty store, finds nothing
undeclared, and reports success — the [F2](#23-the-four-failures) shape, in the one verb whose
entire job is refusing on undeclared input. It is listed here rather than left to the plan
because it is the same defect as F1–F4 and has the same fix: the two predicates become one
resolved target, and this verb reads it too.

### 2.4 What the user asked for, and why it does not exist

Host-side, there is no way to discard a jail's captured edits — the fourth disposition of a
matrix that has three:

| | in the owning jail | host-side, `own` | host-side, `none`/`assert` |
| :--- | :--- | :--- | :--- |
| `reset` acts on | that jail's store + its surface file | the host store + the real dotfile | **refused** |

The refusal's premise is *"these surfaces resolve against a real home"*
([`configdiff.go:130`](../../internal/cli/configdiff.go)), and it is true of the half of
`reset` that truncates the surface file — `expandHome` → `paths.Home()` → the human's real
`~/.claude/settings.json`. It is **not** true of the other half: deleting the two sidecars is
workspace-keyed and would have been correct. So one hazard refuses two operations.

And the jail's own copy is reachable from the host. A pack declares `{"at": ".claude", "kind":
"state"}` ([`packs/claude/pack.json`](../../packs/claude/pack.json)); the launch backs that
with `<workspace>/.yolo/home/claude` (`prepareWsState`, dropping the leading dot —
[`run/prepare.go:349`](../../internal/cli/run/prepare.go)). Verified 2026-09-16:
`/home/agent/.claude` and `/workspace/.yolo/home/claude` are **inode 5787789**, the same
directory. What is missing is a resolver, not a path.

**The cost of not having it is a launch.** The only shipped exit from a captured edit is
`yolo config reset` inside the owning jail — so discarding a stopped jail's captures requires
launching it, and a launch renders and captures first. The undo is reachable only by
performing the act being undone.

---

## 3. One resolved target

A **config target** *(coined here — the read-side analogue of `render.Target`, which resolves
where a WRITE lands)* is what a `yolo config` invocation resolves once, before any verb runs:

| Field | What it answers | Today's stand-in |
| :--- | :--- | :--- |
| **notch** | `jail` \| `host` (`guest` is not reachable — [§5](#5-what-this-does-not-propose)) | `surfacesAreLocal()`, inverted |
| **workspace** | which jail is being described; empty for a host target | `workspaceRoot()` |
| **store** | the capture sidecar dir, resolved through `render.Target.SidecarDir()` | `prismSidecarDir()` for four verbs, the Target for `reset` |
| **home root** | the root `~/…` in a surface path resolves against | `paths.Home()`, via `expandHome` |
| **provenance** | which per-key record is read | a switch inside `surfaceProvenance` |
| **may write** | whether this invocation may write, and what refusal it earns | `refuseHostSideWrite` + `hostOwnsSurfaces` |
| **how it was chosen** | `--at`, the cwd, or the process — the text the disclosure prints | nothing |

**Resolution, in order** ([OQ-CR1](#oq-cr1) and [OQ-CR2](#oq-cr2), ruled). `--at` wins where
given. Otherwise **the cwd selects the target**: a directory that resolves a workspace gets
that workspace at the `jail` notch, and a directory that resolves none gets the `host` notch.
Either way the disclosure names which, and what chose it. `promote` is unaffected — it stays
host-only and keeps reading the workspace store, because its write destination is user scope,
which is not a notch — and `drift`/`dump` are workspace verbs throughout.

**Why the cwd may select the notch here, when it may not on the write side.**
[`host-render-target.md` §6.6](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)'s
*"the `cwd` selects nothing"* governs what a directory may change about a **real home's
configuration** — a write-side hazard, where standing somewhere would silently alter what yolo
does to your dotfiles. Selecting *what a report is about* is not that hazard, and the cwd is
the one input that already names the thing a reader means by "this jail". What this design
removes is not the inference; it is the **pair** — two predicates resolved independently, so
one report could describe two homes.

**Every path reads that one answer.** The `prism*` twins stop being callable directly:
`readOverlayValue`, `readLastRenderKeys`, `overlayKeyCount`, `surfaceProvenance`,
`composedFileExists`, `expandHome` at a surface path, and both halves of `reset` take the
store and the home root off the target. That is what makes [F1](#23-the-four-failures) and
[F3](#23-the-four-failures) unrepresentable rather than fixed: there is no second predicate
left to disagree with.

**Two payoffs fall out for free.** With a home root on the target, presence is knowable for a
workspace target host-side (`<workspace>/.yolo/home/…`), so `ls`'s host-side row inflation in
[F2](#23-the-four-failures) goes away; and a host-side `reset` of a jail's surfaces becomes a
resolver away rather than a design away ([OQ-CR4](#oq-cr4)).

### 3.1 The disclosure

One line, before the report, on every `yolo config` verb — the shape a launch already uses for
`Flake source: <path> (<what selected it>)`:

```console
$ yolo config diff claude                                    # host-side, in ~/code/swarf
Surfaces: /home/matt/code/swarf — jail notch, from the cwd · store <workspace>/.yolo/prism
Provenance: the same jail's sidecar record

$ yolo config diff claude --at host                          # the invoking user's real home
Surfaces: /home/matt — host notch, from --at · host_management: own · store ~/.local/share/yolo-jail/host-capture

$ cd /tmp && yolo config diff claude          # /tmp resolves no workspace: the host target
Surfaces: /home/matt — host notch, from the cwd resolving no workspace · host_management: assert
Provenance: the host per-key record
```

It is not suppressible ([P4](../reference/report-tiers.md#principles) —
[`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) deleted the launch's quiet flag for
this reason), and it is one line by the same tier rules: compression is allowed, suppression is
not.

---

## 4. Behavior, stated exhaustively

### 4.1 Degenerate inputs

| Input | Today | Proposed |
| :--- | :--- | :--- |
| cwd resolves no workspace | store = `<cwd>/.yolo/prism`, absent → a confident empty answer at rc 0 | the **host** target, disclosed as chosen by the cwd resolving none ([OQ-CR2](#oq-cr2), ruled) |
| cwd is `$HOME` in a jail (`~/.yolo` exists) | describes a workspace at `/home/agent` | excluded — the marker is a launch artifact or a workspace config, and the generated-script anchor is neither ([OQ-CR2](#oq-cr2), ruled) |
| a workspace marked by its config file but never launched | *"No captured in-jail edits"* | *"never rendered here"* — the state `capture` already reports and `diff` does not. This is the case the `yolo-jail.jsonc` half of the marker exists for: a fresh clone is a workspace before its first launch |
| a workspace inside a workspace | innermost `.yolo/` wins, silently | unchanged, and disclosed by path |
| an unrelated `.yolo/` dir in a subtree | selects a workspace nobody launched | excluded by the marker test ([OQ-CR2](#oq-cr2), ruled) |
| the jail for the target workspace is **running** | `reset` host-side refuses for the wrong reason | see [§4.3](#43-concurrency-and-ordering) |
| `--at host` where `host_management` is unset | n/a (no such flag) | the shipped unset answer: `assert`, silently ([`OQ-CO2`](config-ownership-and-promotion.md#13-decision-ledger)) — so writes stay refused |
| a surface whose home path is under no per-workspace state dir (a machine-scoped `shared` dir, a home-root file) | n/a | a workspace target cannot resolve it: report the surface as **not resolvable at this notch**, never fall back to the process home |

### 4.2 Failure paths

- **The store dir is absent** → *"never rendered here"*, not "no edits". Distinguished by the
  presence of the store dir, not of the files in it.
- **The store dir is unreadable** (permissions) → say so and exit non-zero. It is the third
  state of [P3](#1-the-verdict-and-the-principles-it-rests-on) and it currently reads as empty
  (`os.ReadDir` error → `nil`, [`configdiff.go:211`](../../internal/cli/configdiff.go)).
- **A pack the invocation cannot read** (fetched, offline) → unchanged: `diff` already names
  them and says its answer is incomplete. That is the model for every other unknown here.
- **`--at` names a notch this invocation cannot describe** (e.g. `--at jail` with no workspace,
  `--at guest`) → refuse naming what is missing. Never silently degrade to the other notch:
  degrading is [F1](#23-the-four-failures).
- **A write path resolves a home root that is not the target's** → an assertion failure, not a
  fallback. This is the class that put a jail's autonomy posture into a real home once
  (`truncateHostSurfaceToPureRender`'s re-resolution,
  [`configdiff.go:1132`](../../internal/cli/configdiff.go)).

### 4.3 Concurrency and ordering

Nothing here serialises against a jail, and one case needs a ruling rather than a lock: a
host-side write to a *running* jail's surfaces ([OQ-CR4](#oq-cr4)). The file is live — the
agent inside may read it at any moment, and the jail captures on terminate, which would fold
the host-side truncation back in as an edit. My leaning is to refuse while a jail for that
workspace is running and name the in-jail command, which costs nothing: that command is
available exactly then.

Read verbs need no ordering guarantee. They already read a store a live jail is writing, and a
torn read reports a stale key, which is what "captured at some point" means anyway.

### 4.4 One writer, and what nothing may do

- **The workspace store has exactly two writers:** the boot render, and an in-jail `capture`/
  `reset`. [OQ-CR4](#oq-cr4) would add a third — host-side `reset` — which is why its
  concurrency answer is part of the ruling and not a detail.
- **The host capture store keeps its two** (the owned render, and the reset that discards);
  this design adds only readers.
- **Forbidden:** no read verb writes anything, including creating a store dir it found absent;
  no verb resolves a home root or a store path by hand — both come off the target; and a
  workspace config never influences a host target ([P5](#1-the-verdict-and-the-principles-it-rests-on)).

### 4.5 Defaults and triggers

Resolution runs once per invocation, before the verb, with no cache and no state — there is no
timer and nothing persisted, so there is no staleness to reason about. Existing state needs no
migration: the stores, their names and their locations are unchanged, and every path this
design touches is derived at read time.

### 4.6 What done looks like

- Every `yolo config` verb prints one line naming the workspace-or-home, the notch, and how it
  was chosen; and `diff` prints one block about one home, because per-key provenance has left
  that verb ([OQ-CR7](#oq-cr7)).
- From a directory that is not a workspace, no verb reports an empty answer as a fact.
- On a host with `host_management: own`, `yolo config diff` shows exactly the keys
  `yolo config reset` would discard, and `ls` counts them.
- `yolo config render` composes the same `host`-layer bytes the boot render does, or reports
  that layer as unavailable here.
- In a jail, `cd ~` and `cd /workspace` either agree or say why they differ.

---

## 5. What this does not propose

- **No change to `host_management`, its three values, or the unset answer.** That is settled
  and built ([`OQ-CO1`](config-ownership-and-promotion.md#13-decision-ledger),
  [`OQ-CO2`](config-ownership-and-promotion.md#13-decision-ledger)).
- **`config capture` stays refused host-side, including under `own`.** Its premise is privacy
  (a credential copied out of a real file), not ownership —
  [`OQ-CO3`](config-ownership-and-promotion.md#13-decision-ledger) narrowed it deliberately.
- **No new store, no new sidecar, no new file format.**
- **No `guest` notch.** Phase 7 is unbuilt; `--at guest` is refused by name, as
  `surfaceProvenance`'s default arm already does.
- **Not the `promote` destination rules, and not `--to workspace`**
  ([`OQ-CO8`](config-ownership-and-promotion.md#13-decision-ledger)).
- **Not a repo-scoped host input.** [P5](#1-the-verdict-and-the-principles-it-rests-on) is a
  restatement, not an extension.
- **Not the seven live residues** of the ownership design
  ([§11](config-ownership-and-promotion.md#11-success-criteria)); none of them is a resolution
  question, and none is fixed by this.

---

## 6. Alternatives considered

| | Alternative | Verdict |
| :--- | :--- | :--- |
| **A** | **Disclosure only** — one line, no resolution change | **Insufficient alone, and shipped first anyway.** With two predicates the honest line is two lines naming two homes, which reports the defect rather than fixing it. It is still the largest single improvement and [§8](#8-what-i-would-build-in-order) step 1. |
| **B** | **Hard-code `/workspace` in-jail** | **Rejected — restates a deleted bug.** It is wrong the moment the CLI runs for a different workspace than the one it is jailed for (a nested jail, every integration test), where it *"silently read the wrong sidecars and `reset` would have deleted them"* ([`configls.go:328`](../../internal/cli/configls.go)). |
| **C** | **The cwd selects nothing here either** — host-side always means the host notch; describing a jail needs `--at jail --workspace <path>` | **Rejected as the default, kept as the escape hatch.** It is the exact mirror of the host-render ruling, but it breaks the verb that ruling was written around: `promote` reads the cwd's workspace host-side *by design*, and a `diff` that stopped doing so would make its output stop matching promote's. `--at`/`--workspace` remain available for the explicit case. |
| **D** | **Two commands** — `yolo config` for the jail, `yolo host config` for the host | **Rejected.** `yolo host` exists and this would be a second vocabulary for the notch, against [P4](#1-the-verdict-and-the-principles-it-rests-on); it also does not answer F2, which is a workspace question, not a notch one. |
| **E** | **Host-side `reset` deletes sidecars only, no truncation** | **Rejected — measured to be a no-op.** reset → no baseline → the next render adopts the file, and the discarded edits come back ([`OQ-CO7`](config-ownership-and-promotion.md#13-decision-ledger); the two halves are one change). |

---

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A host-side jail-notch write lands on a live jail's file | [OQ-CR4](#oq-cr4)'s ruling includes the running-jail disposition; leaning is refuse-and-name-the-in-jail-command |
| The truncation's pure render differs at the two notches (no computed layer, `${workspace}`, the `host` layer) | The three differences are already stated and handled for the two existing notches ([`configdiff.go:1053`](../../internal/cli/configdiff.go)); a workspace target host-side uses the **jail** arm, and depends on [OQ-CR6](#oq-cr6) fixing where the `host` layer is read from |
| `--at` on a read verb drifts from `--at` on `apply` | One parser, one vocabulary, one help text; a test that the two accept the same set and refuse `guest` the same way |
| The marker test excludes a workspace someone legitimately has | Two remedies, and neither is a flag: the ruled marker also counts a workspace CONFIG file, so a directory yolo has never launched in still resolves; and a directory that is neither resolves the host target under disclosure rather than refusing ([OQ-CR2](#oq-cr2)). ⚠ **There is no `--workspace` flag and there is not going to be one** — `internal/cli/run/run.go`'s refusal says so in as many words (*"there is no --workspace flag: cd into the project you meant"*), so every remedy this design names is a `cd` or an `--at`. |
| One more line of output on every invocation | It is one line, and [report-tiers](../reference/report-tiers.md) allows compression; the alternative is the status quo, where the reader cannot tell which home they are reading about |

---

## 8. What I would build, in order

1. **The resolution object and its single resolution point**, with every `prism*`/`expandHome`
   surface-path reader taking the target — a refactor with no behavior change except that the
   two predicates become one. Its test is that deleting the resolution call site fails
   something, not that a helper returns the right string.
2. **The disclosure line**, everywhere, once there is one answer to print ([OQ-CR5](#oq-cr5)).
3. **The unknown states**: no workspace, never rendered here, unreadable store
   ([OQ-CR1](#oq-cr1) and [OQ-CR2](#oq-cr2), both ruled) — the three answers that are silently
   empty today.
4. **`diff`/`ls` read the target's store**, closing [F3](#23-the-four-failures)
   ([OQ-CR3](#oq-cr3), ruled) — and `diff` DROPS its per-key provenance block in the same
   step, which moves to `ls` ([OQ-CR7](#oq-cr7), ruled). Those belong together: they are the
   two halves of *"one verb, one subject, one home"*.
5. **`--at` on the read verbs**, sharing `apply`'s parser.
6. **`render`'s `host` layer** through `Surface.HostSource` ([OQ-CR6](#oq-cr6), ruled),
   including the managed-home case where the honest answer is that there is no user host layer
   to read — which is a behavior change to the composition, not only to the preview.
7. **Host-side jail-notch `reset`** — [OQ-CR4](#oq-cr4) ruled for it, with the running-jail
   refusal — last, because it is the only step that writes, and it depends on 1, 3 and 6.

---

## 9. Open Questions

1. <a id="oq-cr1"></a>✅ **OQ-CR1: Does the cwd select the notch, or only the workspace?**
   Three shapes: **(a)** keep today's inference and merely disclose it; **(b)** the cwd selects
   the **workspace only**, the notch comes from `--at` with a per-verb default; **(c)** the cwd
   selects nothing and a jail target is always explicit (alternative C). This is the closure
   question for the whole design — every other question below assumes an answer to it, and it
   decides whether `yolo config diff` in a workspace keeps meaning what it means today.

   <!-- vantage: oq id=OQ-CR1 leaning="(b) — the cwd selects the workspace, never the notch; it is the read-side mirror of the host-render ruling without breaking promote." -->

   _Leaning:_ **(b).** It is the read-side mirror of *"the `cwd` selects nothing"* applied to
   the thing the cwd is actually good for — naming a repo — while leaving the notch to an
   explicit selector. (a) preserves a predicate pair that can describe two homes; (c) breaks
   `promote`'s deliberate host-side workspace read. — **Overturned on review; see the answer.**

   **Answer:**
   > **(a) — the cwd selects the target, and the disclosure names it.** A directory that
   > resolves a workspace makes the invocation about that workspace at the `jail` notch; one
   > that resolves none makes it about the `host` notch; `--at` overrides either.
   >
   > **The leaning imported a ruling from a different problem.**
   > [`host-render-target.md` §6.6](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)'s
   > *"the `cwd` selects nothing"* governs what a directory may change about a real home's
   > **configuration** — a write-side hazard, where standing somewhere would silently alter
   > what yolo does to your dotfiles. Choosing what a report is **about** is not that hazard,
   > and the cwd is the one input that already names the jail a reader means by "this one". So
   > the inference stays, and reaching the host stays explicit in the way that matters: stand
   > outside a workspace, or pass `--at host`.
   >
   > **What does not stay is the PAIR**, and that distinction is the whole ruling. "Keep
   > today's inference" is not "keep today's resolution": today the notch comes from
   > `surfacesAreLocal()` and the store from `workspaceRoot()`, independently, which is what
   > lets one report describe two homes ([F1](#23-the-four-failures)) and `diff` disagree with
   > `reset` ([F3](#23-the-four-failures)). Under (a) there is one predicate — the cwd — and
   > notch, workspace, store, home root and provenance are all derived from the single target
   > it resolves. [§3](#3-one-resolved-target)'s collapse is unchanged by this ruling; only
   > the question of who supplies the notch is.
   >
   > `promote` is untouched: host-only, reading the workspace store, writing user scope —
   > which is not a notch, which is why (c) was the shape that broke it.

2. <a id="oq-cr2"></a>✅ **OQ-CR2: What is a workspace, and what happens when there is none?**
   Two halves, one ruling. **Which marker** identifies a workspace — today it is any dir
   holding a `.yolo/`, which matches `/home/agent` inside every jail; candidates are
   `.yolo/.gitignore` (written whenever the state dir is prepared) or `.yolo/config-boot.json`
   (written by a launch, so it means *a jail has run here*). And **what a verb does with no
   workspace**: refuse with the remedy, or answer host-only under disclosure. Decides whether
   `yolo config ls` from `/tmp` prints a table at all.

   <!-- vantage: oq id=OQ-CR2 leaning="config-boot.json as the marker; refuse-with-remedy for the capture verbs, since a confident empty answer is the failure being fixed." -->

   _Leaning:_ `.yolo/config-boot.json` as the marker, and **refuse with the remedy**
   (`--workspace <path>`, or `--at host`) for the verbs whose answer depends on a workspace. A
   launch is what creates anything for these verbs to report, so its own artifact is the
   honest marker; and an empty answer at rc 0 is precisely the failure this design exists to
   remove. — **Both halves amended on review; see the answer.**

   **Answer:**
   > **Marker: `.yolo/config-boot.json` OR a workspace config file** — `yolo-jail.jsonc`,
   > `yolo-jail.json`, `yolo-jail.local.jsonc`, `yolo-jail.local.json`, i.e. whatever
   > `config.LoadWorkspaceConfig` would read. Either artifact is enough. **With no workspace:
   > the host target, under disclosure** — not a refusal.
   >
   > **Why the config file has to count.** A launch artifact alone answers *"has a jail run
   > here"*, which is not what a workspace IS. A freshly cloned repo carrying a committed
   > `yolo-jail.jsonc` is a workspace before its first launch — that is the directory the user
   > is about to launch in, and the one they will run `yolo config ls` in to see what they are
   > about to get. `config-boot.json` is also a weaker signal than it looks: it is written by a
   > FRESH launch and never by an attach, and it is best-effort (a warning, not a failure), so
   > its absence does not even mean "never launched"
   > ([`config.WriteWorkspaceBootBaseline`](../../internal/config/drift.go)). Both markers
   > still exclude the two false positives that matter — the in-jail `$HOME`, whose `.yolo` is
   > the generated-script anchor, and a stray `.yolo` in a subtree.
   >
   > **Why no workspace is an answer rather than an error.** Outside a workspace the only home
   > there is to describe is the host's, and standing there is how the user says so. The
   > refusal the leaning proposed would have made `yolo config ls` in `/tmp` an error message
   > in a case where a correct answer exists. The failure this design removes is the
   > **confident empty answer** — a workspace's silence reported as a workspace's answer — and
   > naming the host target removes it exactly as well as refusing does, with an answer instead
   > of an error.
   >
   > **It is a target, not a permission.** The write guard is unchanged: at the host notch
   > `capture`/`reset` still obey `host_management` (`own` unlocks; `assert`/`none` refuse), so
   > this ruling adds no write path anywhere. `--at jail` with no workspace resolvable still
   > refuses, naming what is missing ([§4.2](#42-failure-paths)) — an explicit request for a
   > thing that does not exist is a different case from an unstated default.

3. <a id="oq-cr3"></a>✅ **OQ-CR3: On an owned host, what does `diff` show?**
   [F3](#23-the-four-failures) is a defect either way; the ruling is what replaces it.
   **(a)** `diff`/`ls` resolve their store through the target, so an owned host shows its host
   captures and a workspace target shows the jail's; **(b)** show **both**, labelled, since a
   user may care about each; **(c)** refuse to answer without `--at`. Decides whether the
   shipped inspect-then-undo pair agrees on an owned host.

   <!-- vantage: oq id=OQ-CR3 leaning="(a) — one target, one store, resolved through render.Target, which is the rule the ownership design already stated." -->

   _Leaning:_ **(a).** One target, one store, resolved through `render.Target` — which is the
   rule [§6.2](config-ownership-and-promotion.md#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to)
   already stated and this is the reader that broke it. (b) reintroduces a two-home report as a
   feature.

   **Answer:**
   > **(a) — one target, one store.** `diff` and `ls` resolve the capture store through
   > `render.Target`, exactly as `reset` already does, so an owned host reports its host
   > captures and a workspace target reports that jail's. That closes
   > [F3](#23-the-four-failures) by removing the second resolution rather than by teaching the
   > readers about ownership.
   >
   > **The review sharpened what was being asked, and it is worth recording:** `diff` always
   > reads the capture store — it has no other subject — so the only live content of this
   > question was ever *which* store, and (b) "show both, labelled" was a two-home report
   > wearing a feature's clothes. What `diff` prints **beside** the capture is the per-key
   > provenance block, which is neither a capture nor a diff, and stapling it on is where F1's
   > two homes came from. Whether that block belongs in this verb is now
   > [OQ-CR7](#oq-cr7).

4. <a id="oq-cr4"></a>✅ **OQ-CR4: Is there a host-side `reset` of a jail's captured edits?**
   The question that started this doc. **(a)** Yes — delete the workspace store's sidecars and
   truncate `<workspace>/.yolo/home/…`, refusing while a jail for that workspace is running;
   **(b)** no — keep refusing, and make the refusal name the in-jail command and say why the
   host cannot do it; **(c)** yes, unconditionally, running jail or not. Decides whether
   discarding a stopped jail's captures still requires launching that jail — the act being
   undone.
   ⚠ Depends on [OQ-CR6](#oq-cr6): the jail arm of the truncation re-reads the surface's own
   destination for its `host` layer, which host-side is the wrong home.

   <!-- vantage: oq id=OQ-CR4 leaning="(a) with the running-jail refusal — the store is already workspace-keyed and the home is reachable; refusing while a jail runs costs nothing because the in-jail verb is available exactly then." -->

   _Leaning:_ **(a), with the running-jail refusal.** The store is already workspace-keyed and
   the surface file is a resolver away (measured: same inode); the hazard the refusal names is
   real for a *real home*, not for a workspace's own home overlay. Refusing while that jail
   runs costs nothing, because the in-jail command is available exactly then.

   **Answer:**
   > **(a), with the running-jail refusal**, as leaned — the fourth disposition of
   > [§2.4](#24-what-the-user-asked-for-and-why-it-does-not-exist)'s matrix exists. The store is
   > workspace-keyed already and the surface file is a resolver away; the hazard the current
   > refusal names is real for a *real home* and not for a workspace's own home overlay, and
   > refusing while that workspace's jail is running costs nothing because the in-jail verb is
   > available exactly then.
   >
   > **The [OQ-CR6](#oq-cr6) dependency survives this ruling**, which is why it is still last
   > in [§8](#8-what-i-would-build-in-order): the truncation has to rewrite the surface to a
   > pure render, and *"pure render"* includes a `host` layer whose source is the very thing
   > [OQ-CR6](#oq-cr6) decides. Building this before that answer would hard-code the wrong home into a
   > write.

5. <a id="oq-cr5"></a>✅ **OQ-CR5: Is the disclosure line unconditional?**
   **(a)** Every `yolo config` invocation, always; **(b)** only when the resolution is
   surprising (no workspace, `--at` given, an owned host); **(c)** only under `--verbose`.
   Decides whether a reader has to know the rules to know what they are reading.

   <!-- vantage: oq id=OQ-CR5 leaning="(a) — a disclosure is never suppressible, and 'surprising' is a judgement the reader cannot verify." -->

   _Leaning:_ **(a).** [P4](../reference/report-tiers.md#principles) is a standing ruling for
   the launch stream and the argument transfers unchanged; worse, *"surprising"* is a judgement
   the reader cannot check, so its absence would carry information they have no way to decode.

   **Answer:**
   > **(a) — every `yolo config` invocation, always.** [P4](../reference/report-tiers.md#principles)
   > is a standing ruling and the argument transfers unchanged: compression is allowed,
   > suppression is not. *"Surprising"* would have been a judgement the reader cannot check, so
   > the line's ABSENCE would carry information they have no way to decode — and a disclosure
   > you have to know the rules to notice the lack of is not a disclosure.

6. <a id="oq-cr6"></a>✅ **OQ-CR6: When `render` previews a file, whose bytes are the `host` layer?**
   *(Rewritten 2026-09-17. The first phrasing named two code paths and asked which to call,
   which is unanswerable without knowing what each path MEANS.)*

   `yolo config render claude/settings` prints the file a jail's boot would write. One input to
   that composition is the **`host` layer**: the user's own pre-existing `settings.json`, which
   yolo layers its keys over instead of discarding. Three different files answer to that
   description, and they do not hold the same bytes:

   - `/ctx/host-<pack>/settings.json` — what the LAUNCH staged: a read-only copy of the host's
     file, taken before the jail could touch anything. **This is the one the boot render
     reads** (`Surface.HostSource`, derived by `packload`).
   - the destination **inside the jail**, `~/.claude/settings.json` — which after one boot
     holds yolo's own composed output.
   - the destination **on the host** — the same path, and on an `assert` or `own` home it too
     holds yolo's composed output.

   `render` reads the destination ([`config.go:310`](../../internal/cli/config.go)). So in a
   jail the preview feeds yolo's previous output back in as if it were the user's input, and
   host-side on a managed home it does the same — while the bytes the boot render actually uses
   come from neither. **This repo has already made and deleted this exact mistake once:**
   `SkillTarget.HostSource` also named the destination, *"so a jail read yolo's own generated
   output back in as 'the user's tree'"*, and the field was removed rather than repointed
   ([`internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go)).

   **(a)** read the staged copy (`Surface.HostSource`), and where this process cannot reach it
   — host-side, where there is no `/ctx` — report the `host` layer as **unavailable** rather
   than silently substituting a different file; **(b)** keep reading the destination, and
   document `render` as previewing the layer as the NEXT render will find it rather than as the
   last one saw it. *(The adoption archive is not a third answer: it holds the file as it was
   at adoption, not as it is now.)* Decides whether `render` is a faithful preview or a re-read
   of whichever home the process happens to be in — and [OQ-CR4](#oq-cr4)'s truncation writes
   the same layer, so it inherits the answer.

   <!-- vantage: oq id=OQ-CR6 leaning="(a) — render already declines to invent the computed layer for exactly this reason." -->

   _Leaning:_ **(a).** `render` already refuses to supply the computed layer rather than
   inventing one, and says so in its output; reading a host layer out of the wrong home is the
   same error made silently.

   **Answer:**
   > **(a) — the staged copy, never the destination; and where there is no staged copy, the
   > layer is reported UNAVAILABLE rather than substituted.** `render` composes from
   > `Surface.HostSource` in a jail, exactly as the boot render does, and host-side — where no
   > `/ctx` exists — it says the `host` layer is unavailable here instead of reading the
   > destination and calling it the user's input.
   >
   > **The review sharpened this with a case the question did not distinguish, and it is the
   > important half: "the user's own file" is not always the user's.** Where
   > `host_management` is `assert` or `own`, the host's file is yolo's OWN composed output, so
   > a `/ctx` copy of it is not a user layer at all — folding it back in makes a key yolo
   > wrote indistinguishable from a key the user wrote, and a pack overlay that later changes
   > or is removed leaves its old value in place forever, because it came back as "the user's".
   > That is the same circularity `SkillTarget.HostSource` was DELETED for, one file over. So
   > the ruling has two cases, and the composition must state which it is in:
   >
   > | `host_management` | what the host file is | what the launch delivers | what the jail does with it |
   > | :--- | :--- | :--- | :--- |
   > | `none` (the default) | the user's own bytes, which yolo has never written | the staged copy, labelled **user bytes** | composes it as the `host` LAYER — exactly as today, and [P7](#1-the-verdict-and-the-principles-it-rests-on) makes this the path that must stay frictionless |
   > | `assert` / `own` | yolo's own render, over what was the user's | the staged copy, labelled **a render** | **never a layer.** It is the BASELINE the jail reports divergence against; the jail composes from packs alone |
   >
   > The user's live edits are not lost in the second case: they are the **capture**, which is
   > already its own layer. Their pre-yolo original is in the adoption archive, which is not a
   > layer and must not become one — it is the file as it was at adoption, not as it is now.
   >
   > **THE REVIEW WAS REMEMBERING A REAL RULING, AND THIS IS THE MIDDLE POSITION BETWEEN IT
   > AND THE ONE THAT REVERSED IT.** The history, because both halves of it matter:
   >
   > | | Ruling | Date |
   > | :--- | :--- | :--- |
   > | [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger) | **"Retire the read-in `host` layer; express personal settings as a local pack instead"** — the review's memory, almost verbatim | 2026-08-01 |
   > | [`OQ-CO10`](config-ownership-and-promotion.md#13-decision-ledger)/[`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger) | **REVERSED it: the layer stays.** Never implemented in between. The declaration moved onto the surface and the read fails closed, on the grounds that ruling a mechanism's binding, failure direction and coverage decides that it exists | 2026-09-12 |
   >
   > Neither drew the distinction above: [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger) retired the layer in every case, and [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger)
   > kept it in every case. The two cases give each ruling its own — [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger)'s reason is the
   > `none` home, where a jail that ignored the settings file you already have would be the
   > defect, and [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger)'s reason is the managed home, where the bytes are yolo's own. **And
   > [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger)'s remedy survives as the managed case's answer:** personal settings expressed as a
   > local pack is what `yolo config promote --to pack` already does, shipped
   > ([`config-ownership-and-promotion.md` §5.2](config-ownership-and-promotion.md#52-what-it-does-in-order)).
   > So under `own`, a key you want in every jail lives in a pack — not in a host file the jail
   > reads back.
   >
   > ⚠ **What does not hold is the stronger reading** — that a jail reads no host surface at
   > all. It reads two: `packs/claude` and `packs/pi` each declare `readsHost: true` on their
   > `settings` surface, and the boot render **refuses the launch** rather than composing
   > without the layer, because a settings file that silently dropped the user's own keys
   > *"would look correct"* ([`entrypoint/packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)).
   >
   > **THE RENDER IS DELIVERED AS A BASELINE, NOT WITHHELD — and that is what closes the
   > discriminator gap below.** The first spelling of this ruling said the managed case reads
   > *nothing*, which throws away something the jail wants: with the host's rendered surface in
   > hand, the jail can still answer *"how does what I have differ from what the host has"*,
   > which is [§2.2](#22-what-each-verb-resolves-today)'s definition of what `diff` is for, and
   > `capture` keeps a baseline to compute against. What is lost is **provenance** — which
   > layer a host-side key came from — and that is ruled acceptable: a jail has no business
   > reporting host-side provenance, and per [OQ-CR7](#oq-cr7) provenance is leaving `diff`
   > anyway. A baseline is not a layer: nothing composes from it, so no key yolo wrote can
   > re-enter as the user's, and [P6](#1-the-verdict-and-the-principles-it-rests-on) holds.
   >
   > **`readsHost` also stays narrow.** Two surfaces declare it and a third should have to
   > argue for itself: the grant carries a real file out of the user's home and its only job is
   > disclosure, so coverage is a deliberate per-surface yes/no
   > ([`OQ-CO10`](config-ownership-and-promotion.md#13-decision-ledger)). Anything else that
   > needs host bytes uses the explicit mechanism — a `host_files` grant, which is disclosed at
   > launch and is not a config layer at all.
   >
   > ⚠ **THE JAIL CANNOT DERIVE WHICH CASE IT IS IN, and the ruling therefore owes a
   > mechanism.** `host_management` is deliberately NOT inherited into a jail
   > ([`internal/config/inherit.go`](../../internal/config/inherit.go) refuses it, because the
   > referent rebinds to the container's disposable home), so a boot render holding a `/ctx`
   > copy has no way to tell the user's file from yolo's own render — the two-case table above
   > has no discriminator at the jail notch. The fact is the HOST's to state, and the launch
   > already has a channel for exactly that shape: the `YOLO_HOST_LAYERS` report, whose
   > dispositions the boot render already switches on. A fifth disposition — *this file is
   > yolo's own render, treat it as a baseline and not as a layer* — is the shape, and the
   > table above is its whole payload. The alternative, inheriting `host_management`, is the
   > thing `inherit.go` refuses for a stated reason, and it would be the wrong fix regardless:
   > the jail does not need to know the user's POSTURE, only what the bytes it was handed ARE.
   > That is a fact about a delivery, which is exactly what that report carries.
   >
   > **This is one of three logged defects in the same previewer**, and the other two are worth
   > fixing in the same pass: `yolo config render` also has no `Computed`/`Overlay` layers, and
   > `yolo config render user` does not exist
   > ([`OQ-ACP3`](../plans/agent-config-packs.md#-oq-acp3--whether-the-prism-should-become-a-standalone-tool-that-also-manages-host-configs)'s
   > leaning (c), which names a faithful host-side previewer as the prerequisite for host-config
   > management at all).

7. <a id="oq-cr7"></a>✅ **OQ-CR7: Does `diff` report provenance, or is that `ls`'s job?**
   *(Opened by the 2026-09-17 review of [OQ-CR3](#oq-cr3) — "doesn't `diff` always show the
   capture?" It does, which is what makes the OTHER block it prints worth questioning.)*

   `yolo config diff <agent>` prints two things: the **captured divergence** (the overlay
   against the last render — a diff in the ordinary sense, and the verb's subject) and a
   per-key **provenance** block (which layer each key came from, read from the notch's own
   record). The second is neither a capture nor a diff; it is `ls`'s kind of fact. It is also
   the block that made [F1](#23-the-four-failures) possible — two independently-resolved
   readers in one report, only one of which ever named its home.

   **(a)** `diff` reports only the captured divergence, and per-key provenance moves to `ls`
   (or behind a flag on either); **(b)** `diff` keeps both, now that one resolved target means
   both halves describe the same home. Decides whether the fix for F1 is *"label both blocks"*
   or *"one verb, one subject"*.

   <!-- vantage: oq id=OQ-CR7 leaning="(b) — one resolved target already removes the two-home defect, and splitting a shipped verb's output is a separate, breaking change." -->

   _Leaning:_ **(b), and keep the name.** Once the target is single, both blocks describe one
   home and F1 is gone without moving anything — and `diff` is the right word for *"what have
   I changed against the baseline"*, which is exactly what the capture half is. The argument
   for (a) is that a verb with one subject cannot grow an F1 again; the argument against is
   that it is a breaking output change to a shipped verb, bought after
   [OQ-CR1](#oq-cr1)'s single target has already closed the defect it would prevent.
   — **Overturned on review; see the answer.**

   **Answer:**
   > **(a) — `diff` reports the captured divergence and nothing else. Provenance moves, and
   > the name stays.** The leaning for (b) argued from the defect (one target already closes
   > F1) and not from the verb's subject, and the subject is what decides it:
   > **provenance is a property of the RENDER, not of a capture.** It answers *"which layer
   > did this key come from"*, which is a fact about how the file was composed; at the moment
   > you are asking what you have changed, you are not asking whose value you outrank. Against
   > [§2.2](#22-what-each-verb-resolves-today)'s definition of what `diff` is for — the delta
   > between what you have and a pure regeneration — the provenance block cannot justify its
   > place, because it would be identical before and after the wipe.
   >
   > It lands where the render is described: `ls`, which already reports per-surface state, or
   > `render`, which is the composition itself. **The name is right and stays:** the reviewer's
   > sentence IS a diff — current state against a regenerated baseline — so `diff` names it
   > exactly, and it was only the second block that made the verb look mis-named.
   >
   > This closes [F1](#23-the-four-failures) twice over: one resolved target means the two
   > blocks could no longer describe two homes, and now there is only one block.

8. <a id="oq-cr8"></a>✅ **OQ-CR8: Should a jail read the host's own config file at all?**
   *(Filed and closed on 2026-09-17. It was filed because I reported finding no ruling, and the
   review was right that one exists. There are two, in opposite directions, and the second is
   current — so this question is answered elsewhere and is recorded here only to point at them.
   The search failure is worth naming, because it is a repeatable one: I found the REVERSAL and
   read it as the whole history. [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) states the current rule and
   does not restate what it overturned, so stopping at the doc that governs today hid the
   earlier ruling — which sat in a file my first grep had already listed and I did not open.)*

   Two shipped surfaces declare `readsHost: true` — `claude/settings` (`~/.claude/settings.json`)
   and `pi/settings` — and for those, a launch stages a read-only copy of the host's file at
   `/ctx` and the boot render layers yolo's keys over it. It is not best-effort: a surface that
   declares a host layer and cannot get one **refuses the launch**, on the stated grounds that a
   settings file which silently dropped the user's own keys *"would look correct"*
   ([`entrypoint/packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)).

   **(a)** keep it — a jail respecting the settings you already have is the feature, and
   [OQ-CR6](#oq-cr6)'s two cases already stop it reading yolo's own output back in;
   **(b)** drop it — a jail composes from packs and captures only, and a user who wants a
   personal key in a jail puts it in the conventional local pack, which is the mechanism that
   already replaced the same idea for skills. Decides whether `host_files`-backed config layers
   survive as a concept, and it is a product posture rather than a defect: nothing is broken
   today either way.

   <!-- vantage: oq id=OQ-CR8 leaning="(a) — the refusal exists because dropping the user's own keys looks correct, and OQ-CR6 already removes the circular case." -->

   _Leaning:_ **(a).** The hazard that motivates (b) is real but is the *managed-home* case,
   and [OQ-CR6](#oq-cr6) rules that case out on its own terms. What (b) additionally costs is
   the unmanaged case, which is most users: a jail that ignores the `~/.claude/settings.json`
   they already have, with the local pack as the migration. That is a bigger change than this
   doc's subject, and it wants its own doc if it is wanted at all.

   **Answer:**
   > **(a) — the layer stays, and this was already settled twice.**
   > [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger) ruled it retired on 2026-08-01
   > in favour of a local pack; [`OQ-CO10`](config-ownership-and-promotion.md#13-decision-ledger)/[`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger)
   > **reversed that on 2026-09-12** — never implemented in between — and the reversal is built.
   > This question adds nothing to them and is closed on their authority, not on mine.
   >
   > ⚠ **The reversal is worth reading before reopening it**, because the retirement left
   > residue that looked like unbuilt work for five days: a row in
   > [`yolo-as-environment-manager.md` §3.3](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails)
   > still said *"retire the read-in `host` layer"* after the ledger had struck it through, and
   > `HostSource` in `internal/agentcfg/manifest` reads as the retirement's leftovers when it is
   > in fact the implementation of the ruling that replaced it.
   >
   > What is NEW is not this question's answer but [OQ-CR6](#oq-cr6)'s distinction: the layer
   > stays where the host file is the user's, and is not read where it is yolo's own render.
   >
   > **And the keep now has a substantive reason, which the reversal did not give it.**
   > [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger)'s recorded argument is
   > procedural — ruling a mechanism's binding and failure direction presupposes that it exists
   > — so the layer survived without anyone re-arguing that it should. [P7](#1-the-verdict-and-the-principles-it-rests-on)
   > is that argument: this layer is the ONBOARDING path. Someone who installs yolo today keeps
   > the settings file they already have, with nothing to author and nothing to promote, and
   > requiring the local pack instead would charge every new user a migration to get what they
   > already had. The pack migration is the price of asking yolo to manage the host, and it is
   > charged then and not before. Reopening this means arguing against that, not against
   > [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger)'s procedure.

---

## 10. Decision Ledger

**All eight ruled on 2026-09-17**, across two review rounds. Nothing is live, and nothing is
built. [OQ-CR8](#oq-cr8) was filed and closed the same day on two earlier rulings' authority: the
search that missed them found the REVERSAL and read it as the whole history, because the doc
that governs today does not restate what it overturned.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| [OQ-CR1](#oq-cr1) | **The cwd selects the TARGET — (a), against the leaning.** A directory that resolves a workspace means that workspace at the `jail` notch; one that resolves none means the `host` notch; `--at` overrides either. The leaning for (b) had imported *"the `cwd` selects nothing"* from the WRITE side, where the hazard is a directory silently changing what yolo does to a real home; choosing what a report is *about* is not that hazard. What the design removes is not the inference but the **pair** — two predicates resolved independently, which is what let one report describe two homes | 2026-09-17 | [§3](#3-one-resolved-target) | — |
| [OQ-CR2](#oq-cr2) | **Marker: `.yolo/config-boot.json` OR a workspace config file; no workspace means the host target, disclosed.** Both halves amended from the leaning. A launch artifact alone answers *"has a jail run here"*, which is not what a workspace is — a fresh clone with a committed `yolo-jail.jsonc` is one before its first launch, and `config-boot.json` is fresh-launch-only and best-effort besides. And outside a workspace the only home to describe is the host's, so naming it removes the confident-empty-answer failure just as well as a refusal does, with an answer instead of an error. It is a target, not a permission: the write guard is untouched | 2026-09-17 | [§4.1](#41-degenerate-inputs) | — |
| [OQ-CR3](#oq-cr3) | **(a) — one target, one store.** `diff`/`ls` resolve the capture store through `render.Target`, as `reset` already does, closing [F3](#23-the-four-failures) by removing the second resolution rather than teaching the readers about ownership. The review also sharpened the question: `diff` has no subject but the capture store, so "which store" was all that was live, and (b) was a two-home report wearing a feature's clothes | 2026-09-17 | [§3](#3-one-resolved-target) | — |
| [OQ-CR4](#oq-cr4) | **(a), with the running-jail refusal** — the fourth disposition exists. The store is workspace-keyed and the surface file is a resolver away; the hazard the current refusal names is real for a real home, not for a workspace's own home overlay; refusing while that jail runs costs nothing because the in-jail verb is available exactly then. Still last to build: the truncation writes a `host` layer whose source [OQ-CR6](#oq-cr6) decides | 2026-09-17 | [§4.3](#43-concurrency-and-ordering) | — |
| [OQ-CR5](#oq-cr5) | **(a) — the disclosure is unconditional**, on every `yolo config` invocation. [P4](../reference/report-tiers.md#principles) transfers unchanged: compression is allowed, suppression is not. *"Only when surprising"* would have made the line's absence carry information the reader has no way to decode | 2026-09-17 | [§3.1](#31-the-disclosure) | — |
| [OQ-CR6](#oq-cr6) | **(a) — the staged copy, never the destination; no staged copy means the layer is reported UNAVAILABLE, never substituted.** And the case the question failed to distinguish is the load-bearing one: under `host_management: assert`/`own` the host's file is yolo's OWN render, so it is delivered LABELLED AS A RENDER and used as a BASELINE rather than composed as a layer. Folding it in would make a key yolo wrote indistinguishable from the user's, and would pin a removed pack overlay's value forever — the circularity `SkillTarget.HostSource` was deleted for. **Refined the same day into [P6](#1-the-verdict-and-the-principles-it-rests-on)**, which states the rule on the WRITE rather than on the notch's name and is also the discriminator — the label on the delivery is the only fact the jail needs. The first spelling said the managed case reads *nothing*, which threw away the baseline `diff` and `capture` want; what is given up instead is host-side provenance, ruled acceptable because a jail has no business reporting it. **This is the middle position between two earlier rulings the review remembered and this doc had not found:** [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger) retired the layer outright (2026-08-01, in favour of a local pack) and [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger) reversed that and kept it outright (2026-09-12). Each ruling's reason owns one of the two cases, and [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger)'s remedy — personal settings as a pack — is what `promote --to pack` already does for the managed one | 2026-09-17 | [§9](#9-open-questions) | — |
| [OQ-CR8](#oq-cr8) | **The layer stays — answered elsewhere, twice, and closed on their authority.** Filed the same day from the review's recollection and closed once the plans were searched rather than just the designs: [`OQ-3`](yolo-as-environment-manager.md#9-decision-ledger) retired it, [`OQ-CO10`](config-ownership-and-promotion.md#13-decision-ledger)/[`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger) reversed that, and the reversal is built. Recorded rather than deleted because the retirement left five days of residue that reads as unbuilt work, and because the stronger reading — no host layer in any case — is a reopening of [`OQ-CO11`](config-ownership-and-promotion.md#13-decision-ledger) in its own doc, not a question this design can answer. **And the keep now has the substantive reason the reversal never gave it**, as [P7](#1-the-verdict-and-the-principles-it-rests-on): this layer is the onboarding path, so adopting yolo costs no migration and adopting host management is what costs the pack one | 2026-09-17 | [§9](#9-open-questions) | n/a — a ruling to KEEP |
| [OQ-CR7](#oq-cr7) | **(a) — `diff` reports the captured divergence and nothing else, and keeps its name.** Against the leaning, on the verb's SUBJECT rather than on the defect: provenance answers *"which layer did this key come from"*, a fact about the render, and it would read identically before and after the wipe that [§2.2](#22-what-each-verb-resolves-today)'s definition of `diff` measures against. Per-key provenance moves to `ls`/`render`. The name was never the problem — the second block was | 2026-09-17 | [§2.2](#22-what-each-verb-resolves-today), [§8](#8-what-i-would-build-in-order) | — |
