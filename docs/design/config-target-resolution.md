---
title: "Which home does `yolo config` mean? — two predicates, one report, no disclosure"
date: 2026-09-16
status: draft
tags: [design, config, cli, notch, workspace, capture, disclosure]
summary: "A `yolo config` verb answers about a home and a sidecar store it never names, and it picks them with two independent predicates — the cwd for the store, YOLO_VERSION for the home. So one report describes two homes, a `cd` silently changes the answer, and the one write verb refuses for a hazard only half its work has. One resolved target per invocation, disclosed on every invocation, selected by the `--at` the write verbs already have."
vantage:
  status-chip: true
---

# Which home does `yolo config` mean? — two predicates, one report, no disclosure

**Status:** DESIGN, 2026-09-16. Nothing built. Every current-behavior claim below was
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

**Needs your ruling:** [OQ-CR1](#oq-cr1), [OQ-CR2](#oq-cr2), [OQ-CR3](#oq-cr3),
[OQ-CR4](#oq-cr4), [OQ-CR5](#oq-cr5), [OQ-CR6](#oq-cr6).

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
confidence as a real one. Per [P3](#1-the-verdict-and-the-principles-it-rests-on) it should be
*"no workspace resolved from this directory"*.

> [!WARNING]
> **In a jail, `$HOME` is a false workspace.** `/home/agent/.yolo` exists — it is the anchor
> for the generated `bin/block` and `bin/launch` dirs — so `workspaceRoot()` stops there, and
> from `cd ~` the verbs describe a workspace at `/home/agent` that has never existed.
> Measured: `cd ~ && yolo config diff claude` → *"No captured in-jail edits for claude"*, with
> the real captures sitting in `/workspace/.yolo/prism`. Any marker-based fix
> ([OQ-CR2](#oq-cr2)) has to exclude this directory, and a bare `.yolo/` test cannot.

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

**Resolution, in order.** `--at` wins where given. Otherwise the notch defaults per verb (a
read verb about captured edits defaults to the workspace the cwd resolves; `promote` stays
host-only; `drift`/`dump` are workspace verbs and unaffected). The workspace comes from the
cwd — never from `--at` — and where the cwd resolves none, the verbs that need one say so
([OQ-CR2](#oq-cr2)).

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

$ cd /tmp && yolo config diff claude
yolo config diff: no workspace resolved from /tmp, and no --at given.   # rc per OQ-CR2
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
| cwd resolves no workspace | store = `<cwd>/.yolo/prism`, absent → a confident empty answer at rc 0 | [OQ-CR2](#oq-cr2): refuse-with-remedy, or host-only with disclosure |
| cwd is `$HOME` in a jail (`~/.yolo` exists) | describes a workspace at `/home/agent` | excluded by the marker test ([OQ-CR2](#oq-cr2)) |
| a workspace with `.yolo/` but never launched | *"No captured in-jail edits"* | *"never rendered here"* — the state `capture` already reports and `diff` does not |
| a workspace inside a workspace | innermost `.yolo/` wins, silently | unchanged, and disclosed by path |
| an unrelated `.yolo/` dir in a subtree | selects a workspace nobody launched | excluded by the marker test |
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
  was chosen; `diff` never prints two homes' facts without labelling both.
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
| The marker test excludes a workspace someone legitimately has | The remedy is explicit (`--workspace`), and the refusal names it; a marker written by the launch means "a jail has run here", which is the only case the capture verbs have anything to say about |
| One more line of output on every invocation | It is one line, and [report-tiers](../reference/report-tiers.md) allows compression; the alternative is the status quo, where the reader cannot tell which home they are reading about |

---

## 8. What I would build, in order

1. **The resolution object and its single resolution point**, with every `prism*`/`expandHome`
   surface-path reader taking the target — a refactor with no behavior change except that the
   two predicates become one. Its test is that deleting the resolution call site fails
   something, not that a helper returns the right string.
2. **The disclosure line**, everywhere, once there is one answer to print ([OQ-CR5](#oq-cr5)).
3. **The unknown states**: no workspace, never rendered here, unreadable store
   ([OQ-CR1](#oq-cr1), [OQ-CR2](#oq-cr2)) — the three answers that are silently empty today.
4. **`diff`/`ls` read the target's store**, closing [F3](#23-the-four-failures)
   ([OQ-CR3](#oq-cr3)).
5. **`--at` on the read verbs**, sharing `apply`'s parser.
6. **`render`'s `host` layer** through `Surface.HostSource` ([OQ-CR6](#oq-cr6)).
7. **Host-side jail-notch `reset`**, if [OQ-CR4](#oq-cr4) rules for it — last, because it is
   the only step that writes, and it depends on 1, 3 and 6.

---

## 9. Open Questions

1. <a id="oq-cr1"></a>💬 **OQ-CR1: Does the cwd select the notch, or only the workspace?**
   Three shapes: **(a)** keep today's inference and merely disclose it; **(b)** the cwd selects
   the **workspace only**, the notch comes from `--at` with a per-verb default; **(c)** the cwd
   selects nothing and a jail target is always explicit (alternative C). This is the closure
   question for the whole design — every other question below assumes an answer to it, and it
   decides whether `yolo config diff` in a workspace keeps meaning what it means today.

   <!-- vantage: oq id=OQ-CR1 leaning="(b) — the cwd selects the workspace, never the notch; it is the read-side mirror of the host-render ruling without breaking promote." -->

   _Leaning:_ **(b).** It is the read-side mirror of *"the `cwd` selects nothing"* applied to
   the thing the cwd is actually good for — naming a repo — while leaving the notch to an
   explicit selector. (a) preserves a predicate pair that can describe two homes; (c) breaks
   `promote`'s deliberate host-side workspace read.

   **Answer:**
   > _(empty — fill in when decided)_

2. <a id="oq-cr2"></a>💬 **OQ-CR2: What is a workspace, and what happens when there is none?**
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
   remove.

   **Answer:**
   > _(empty — fill in when decided)_

3. <a id="oq-cr3"></a>💬 **OQ-CR3: On an owned host, what does `diff` show?**
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
   > _(empty — fill in when decided)_

4. <a id="oq-cr4"></a>💬 **OQ-CR4: Is there a host-side `reset` of a jail's captured edits?**
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
   > _(empty — fill in when decided)_

5. <a id="oq-cr5"></a>💬 **OQ-CR5: Is the disclosure line unconditional?**
   **(a)** Every `yolo config` invocation, always; **(b)** only when the resolution is
   surprising (no workspace, `--at` given, an owned host); **(c)** only under `--verbose`.
   Decides whether a reader has to know the rules to know what they are reading.

   <!-- vantage: oq id=OQ-CR5 leaning="(a) — a disclosure is never suppressible, and 'surprising' is a judgement the reader cannot verify." -->

   _Leaning:_ **(a).** [P4](../reference/report-tiers.md#principles) is a standing ruling for
   the launch stream and the argument transfers unchanged; worse, *"surprising"* is a judgement
   the reader cannot check, so its absence would carry information they have no way to decode.

   **Answer:**
   > _(empty — fill in when decided)_

6. <a id="oq-cr6"></a>💬 **OQ-CR6: Where does a preview read its `host` layer from?**
   **(a)** `Surface.HostSource` — the `/ctx` path the boot render reads, reporting the layer as
   unavailable when this process cannot reach it; **(b)** keep reading the destination and
   document it. Decides whether `yolo config render` is a faithful preview or a re-read of
   whichever home the process happens to be in — and [OQ-CR4](#oq-cr4)'s truncation depends on
   the same answer.

   <!-- vantage: oq id=OQ-CR6 leaning="(a) — render already declines to invent the computed layer for exactly this reason." -->

   _Leaning:_ **(a).** `render` already refuses to supply the computed layer rather than
   inventing one, and says so in its output; reading a host layer out of the wrong home is the
   same error made silently.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 10. Decision Ledger

**Empty.** Nothing is settled yet; every question in [§9](#9-open-questions) is live. Rulings
land here — with their exact `OQ-CR` ids — as they are made, and the reasoning goes into the
section it governs.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | — | — | — | — |
