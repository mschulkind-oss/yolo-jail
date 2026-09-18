---
status: current
verified: 2026-09-18
verified_commit: f659d878
covers:
  - internal/cli/configtarget.go
  - internal/cli/config.go
  - internal/cli/configdiff.go
  - internal/cli/configls.go
  - internal/cli/configprovenance.go
  - internal/cli/configrunningjail.go
  - internal/cli/run/packhostgrants.go
  - internal/entrypoint/hostlayerlabel.go
tags: [config, cli, notch, workspace, capture, disclosure]
summary: "One resolved config target per `yolo config` invocation — notch, workspace, capture store, home root, may-write and chosen-by — disclosed on stderr by every verb and selectable with `--at`; the workspace marker that decides it; the four store states and three surface reaches that keep an unknown from reading as an empty; the fifth `YOLO_HOST_LAYERS` disposition that delivers yolo's own render as a baseline rather than a layer, keyed on the provenance mark and not on the posture; and the host-side jail-notch `reset` with its tri-state liveness refusal."
---

# The config target — which home a `yolo config` verb is about

**Status:** CURRENT as of 2026-09-18, verified against `f659d878`.

**MEASURED:** the resolution and its disclosure, the marker and the unknown states, the
presence answer, `--at`, the preview's host layer in every disposition, and the host-side
jail-notch `reset` in all three liveness answers — by unit tests in `internal/cli`, plus
`TestHostFilesConfigLsAndReset` and `TestConfigTargetResolvesFromTheCwd` in `integration`
against a real jail.

**UNMEASURED:** the fifth host-layer disposition end to end through a real launch of a
*managed* home. The launcher's label and the boot render's reading of it are each pinned by
unit tests, and nothing drives one home through both. See
[Where this does not reach](#where-this-does-not-reach).

A **config target** *(coined by the design this document graduated from)* is the single answer
a `yolo config` invocation resolves before any verb runs: **which home the verb is about**,
which capture store describes that home, whether this invocation may write it, and how that
was decided. It is the read-side analogue of `render.Target`, which resolves where a *write*
lands — and, unlike a `render.Target`, it is never constructed by a verb: one call site
resolves it, discloses it, and hands it down.

The defect it replaced was a **pair**. Two predicates were resolved independently — the
working directory picked the capture store, and `YOLO_VERSION` picked the home and therefore
the provenance record and the write guard — so one report could describe two homes, `diff`
and `reset` could disagree about which store they meant, and nothing on screen named either.

| Component | Lives in |
| :--- | :--- |
| The target, the resolution point, the marker walk, the store states, the disclosure | `internal/cli` (`configtarget.go`: `configTarget`, `resolveConfigTarget`, `workspaceMarked`, `storeState`, `disclosure`) |
| The verb dispatch that resolves once, and `--at`'s token extraction | `internal/cli` (`config.go`: `configRunW`, `extractAtFlag`, `notchlessVerbs`) |
| The preview's host layer | `internal/cli` (`config.go`: `hostLayerFor`, `userHostLayerFor`) |
| `diff`, `reset`, the write guard, the truncation to a pure render | `internal/cli` (`configdiff.go`: `configDiff`, `configReset`, `refuseHostSideWrite`, `truncateSurfaceToPureRender`, `userSurfaceFor`) |
| `ls`, and the per-key provenance block it inherited from `diff` | `internal/cli` (`configls.go`, `configprovenance.go`) |
| The running-jail condition on a host-side write | `internal/cli` (`configrunningjail.go`: `jailLiveness`, `workspaceJailLiveness`, `refuseWhileJailRuns`, the `jailLivenessProbe` seam) |
| The fifth host-layer disposition: the wire, the reader's entry points, the mark | `internal/entrypoint` (`hostlayerlabel.go`: `HostLayerRender`, `HostLayerWire`, `DispositionFor`, `StagedHostLayer`, `HostSurfaceRendered`) |
| The launcher half that computes the label | `internal/cli/run` (`packhostgrants.go`: `hostLayerIsRender`, `hostLayerEnv`) |
| The in-jail reader that consults it before the file read | `internal/entrypoint` (`packsurfaces.go`: `hostSurfaceBytes`) |

**Terms.** A **notch** is the confinement level an operation acts at — `jail`, `guest` or
`host` — which decides whose home its files are; the vocabulary is `internal/render`'s `Kind`
and it is coined in
[`../design/yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md#4-confinement-a-dial-with-three-notches).
A **capture store** is the per-surface sidecar directory holding a jail's captured edits and
the baseline they diverge from; the state machine that writes it is
[`config-migration-to-prism.md`](config-migration-to-prism.md). A **surface** is one composed
file a pack declares; the schema is [`pack-system.md`](pack-system.md).

**Reads with:**
[`../design/config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md)
(the write side: ownership is declared in one user-scope key, and `host_management` decides
whether a host home keeps a capture store at all);
[`../design/host-render-target.md`](../design/host-render-target.md)
(*"the `cwd` selects nothing"* — the write-side ruling this document is the read half of);
[`report-tiers.md`](report-tiers.md) (the disclosure rule the line here obeys).

---

## Principles

Kept with their original IDs, which are cited from code comments. **`P4` here is this
document's**, about the selector; `internal/cli` also cites
[`report-tiers.md`'s own `P4`](report-tiers.md#principles), about suppression, sometimes in the
same file. They are different rules from different documents.

- **P1. A config verb names the home it is about, every time.** Not on a flag, not only when
  it is surprising. The reader cannot infer it: the things that decide it are an environment
  variable and a directory walk, and neither is visible in the output.
- **P2. One resolution per invocation.** A verb resolves its target once, and every path it
  takes — the store, the surface file, the provenance record, the presence check — reads that
  one answer. Two predicates are how one report came to describe two homes.
- **P3. Unknown is not empty.** Where a verb cannot know, it says so rather than answering.
  The rule is this repo's standing one for reapers — *unreferenced* and *I could not ask the
  runtime* are the same empty answer, so a reaper that cannot ask declines — applied to a
  report instead of a sweep.
- **P4. The selector is explicit, and it is `--at`.** `yolo apply --at jail|guest|host` is the
  shipped answer to *"which notch does this verb act on"*. A second spelling for the read verbs
  would be a second vocabulary for one fact.
- **P5. No jail-scope input gains authority over a real home.** Whatever the working directory
  decides, it decides which *jail* is being described — never what yolo asserts into `$HOME`.
- **P6. yolo never reads back a file it writes.** A host layer exists to carry the *user's*
  bytes into a jail. The moment yolo writes that file — `assert` rewrites its declared keys
  into it, `own` composes it whole — those bytes stop being purely the user's, and reading them
  back makes a key yolo wrote indistinguishable from one the user wrote. **The rule is stated
  on the write, not on the notch's name**, because the mechanism is identical in both managed
  modes. This is the rule `SkillTarget.HostSource` was deleted for.
- **P7. Adopting yolo requires no migration. Adopting host management does.** Someone who
  installs yolo and launches a jail keeps the `~/.claude/settings.json` they already have,
  transparently. That is what a host layer is *for*, and it is the onboarding path rather than
  a convenience. The pack migration is the price of `host_management: assert|own`, charged when
  the user asks for that and never before.

## Invariants

- **One resolution point.** `configRunW` resolves the target after argv is parsed (the
  selector is an input) and before the verb's first read, then hands it down. No verb resolves
  a second time, and no code outside `configtarget.go` joins a store path, a home root or a
  provenance path by hand.
- **The disclosure goes to stderr, unconditionally.** Every verb, every invocation. Routing is
  not suppression — see [The disclosure](#the-disclosure).
- **A read verb writes nothing**, including creating a store directory it found absent.
- **The workspace store has three writers and no more:** the boot render, an in-jail
  `capture`/`reset`, and the host-side jail-notch `reset`. The host capture store keeps its
  two: the owned render, and the reset that discards.
- **`local` is false at the host notch by construction.** A `--at host` invocation *inside* a
  jail is about the jail's disposable home; letting that count as local would let `reset`
  truncate it.
- **A workspace target never falls back to the process home.** Host-side that home is the
  invoking human's own dotfiles; a surface a workspace does not back is reported unreachable
  instead.
- **A labelled host layer never refuses a launch.** The fail-closed read exists to stop a
  composition that would silently drop the user's own keys, and a render composed from packs
  alone drops none.

## The config target

Seven facts, resolved together:

| Field | What it answers |
| :--- | :--- |
| notch | `jail` or `host`. `guest` is refused by name at the boundary |
| workspace | which jail is being described; empty at a host target |
| store | the capture sidecar directory, as a `render.Target` — never a joined path |
| home root | the home a surface path's `~` resolves against |
| may write | whether this invocation may write, and which refusal it earns otherwise |
| runtime | which backend lays out a non-local workspace's jail home |
| chosen by | `--at`, or the working directory — the clause the disclosure prints |

Two payoffs follow from having a home root on the target. Presence becomes *knowable* for a
workspace target host-side, because that jail's files sit under the workspace's own state
directory — so `ls` stopped declining the column and its existence filter applies again. And a
host-side `reset` of a jail's surfaces became a resolver away rather than a design away.

The store is always a `render.Target` and never a joined path, which is what makes `diff` and
`reset` agree: at a jail notch it is the workspace's own sidecar directory, and at a host
target it exists only where the user declared `own` — the contract's decision, taken once when
the target is built rather than re-read per query.

> [!WARNING]
> **One config target, but TWO `render.Target`s, and the split is load-bearing.** A Target's
> workspace field feeds both `${workspace}` substitution *and* the sidecar directory, and these
> verbs need different values for the two: the **store** wants the resolved workspace, while a
> **preview** wants the container literal, because a preview of a jail file must show the keys
> that jail will get rather than keys under a host checkout path it never sees. Collapsing them
> either moves the store under the container workspace or previews host paths into a jail file.
> The target carries both — one for reading sidecars, one for composing.

Tests construct the target they mean rather than stubbing a predicate. That is deliberate: a
stubbable predicate is exactly how the store and the home came to be resolvable independently,
so the seam is on the whole answer now. The one package variable left is the liveness probe
[below](#discarding-a-jails-captures-from-the-host), which a bare test runner cannot supply any
other way.

### Resolution order

`--at` wins where given. Otherwise **the working directory selects the target**: a directory
that resolves a workspace gets that workspace at the `jail` notch, and a directory that
resolves none gets the `host` notch. Either way the disclosure names which, and what chose it.

> [!WARNING]
> **Do not read *"the `cwd` selects nothing"* as governing this.** That ruling
> ([`host-render-target.md`](../design/host-render-target.md)) is about the **write** side,
> where standing somewhere would silently change what yolo does to a real home. Choosing what
> a *report* is about is not that hazard, and the working directory is the one input that
> already names the thing a reader means by "this jail". What this design removed is not the
> inference — it is the **pair**.

`--at jail` from a directory that resolves no workspace **refuses, naming what is missing**. An
explicit request for a thing that does not exist is a different case from an unstated default,
and silently degrading to the other notch is the original defect. **Every remedy is a `cd` or
an `--at`:** there is no `--workspace` flag, and the launcher's own refusal says there will not
be one.

Three verbs take no notch and refuse `--at` rather than accepting it inertly: `promote`
(host-only, and its destination is user scope, which is not a notch), `drift` and `dump`. A
flag a verb accepts and does nothing with is the declaration-parity defect.

### What a workspace is — the marker is an artifact, never a directory

A directory is a workspace when it carries `.yolo/config-boot.json` (a launch artifact) **or**
any file name `config.LoadWorkspaceConfig` would read. Regular files only: a *directory* named
like a config is not one, and a directory's mere existence is what went wrong the first time.

Both halves are load-bearing.

- **A bare `.yolo/` cannot be the test.** `/home/agent/.yolo` exists in every jail — it is the
  anchor for the generated blocker and launcher script directories — so `cd ~` used to describe
  a workspace at the jail's home that has never existed.
- **The launch artifact cannot be the test alone.** A freshly cloned repository carrying a
  committed workspace config is a workspace before its first launch — the directory the user is
  about to launch in, and the one they will run `yolo config ls` in to see what they are about
  to get. The artifact is a weaker signal than it looks besides: it is written by a fresh launch
  and never by an attach, and it is best-effort, so its absence does not even mean "never
  launched".

The walk stops at a directory that may not be a workspace at all —
`paths.WorkspaceScopeBreach`: the home itself, or either of yolo's own two host directories —
and **the marker does not make that stop redundant.** The marker excludes a stray anchor; the
stop is the only thing that rejects a *pre-guard* boot artifact inside a home, which nothing
can create any more but which machines already carry. It stops rather than skipping upward,
because every ancestor of a boundary root contains that root and so is one too.

**A directory that resolves neither marker is answered about, not refused.** Outside a
workspace the only home there is to describe is the host's, and standing there is how the user
says so. It is a *target, not a permission*: the write guard is untouched, so `capture` stays
refused host-side even under `own`.

## The disclosure

One line, before the report, on every `yolo config` verb, in the shape a launch already uses
for `Flake source: <path> (<what selected it>)` — what this is about, then what chose it, then
the store. Help text and an unknown subcommand print none, because neither has a report whose
subject there is to disclose.

**It is unconditional.** Not *"only when surprising"*: surprising is a judgement the reader
cannot check, so the line's **absence** would carry information they have no way to decode, and
a disclosure you have to know the rules to notice the lack of is not a disclosure. Compression
is allowed, suppression is not — [`report-tiers.md`](report-tiers.md#principles).

> [!IMPORTANT]
> **It goes to stderr, and that is not a softening of the rule.** `yolo config dump` emits
> canonical JSON on stdout and `yolo config promote --format json` emits a document, so a prose
> line on stdout would break every machine consumer of those two verbs. The line is emitted on
> every invocation either way; only the stream is chosen.

## What each verb reads

Every verb below takes the resolved target. The columns that used to differ per verb — which
store, which home, which provenance record — no longer exist as separate answers.

- **`ls`** lists every surface with its mode, its layers, whether this target holds the file,
  and, since the split below, the per-key provenance block.
- **`diff`** reports **the captured divergence and nothing else**. Its subject is *"if I
  deleted these surfaces, discarded every capture and regenerated them, how would what I have
  now look different?"* A pure regeneration is what the render produces, so the only thing that
  can survive that wipe is what was captured — which makes the capture store `diff`'s subject
  by definition, and makes `diff` the right word for it.
- **Per-key provenance belongs to `ls`, not to `diff`.** Provenance answers *"which layer did
  this key come from"*, a property of the **render**: it would read identically before and
  after the wipe `diff` measures against, so it cannot justify a place there. It was also the
  block that made a two-home report possible — two independently resolved readers in one
  report, only one of which named its home.
- **`render`** previews the file a boot would write; its host layer is
  [below](#the-host-layer--a-staged-copy-a-baseline-or-nothing).
- **`capture`** and **`reset`** write, and answer to the guard in
  [Discarding a jail's captures from the host](#discarding-a-jails-captures-from-the-host).
- **`promote`** keeps reading the **workspace** store at every notch, deliberately: lifting a
  jail's captured keys into a pack is the whole verb, and its destination is user scope, which
  is not a notch. It gets its own accessor on the target rather than the target's store.
- **`drift`** and **`dump`** are workspace verbs throughout and take no notch. Neither uses the
  marker walk: both default to the bare working directory with no upward walk at all.

## Unknown is not empty

Two three-and-four-way answers exist where one boolean used to, and collapsing either is the
failure [P3](#principles) names.

**A capture store has four states**, not one. *Readable* (a negative from here is a
measurement); *none*, where this target keeps no store **by contract** — a host home the user
has not declared `own`; *absent*, meaning nothing has ever rendered here, which `diff` reports
as **"never rendered here"** rather than as "no edits"; and *unreadable*, which says so and
exits non-zero instead of reading as empty. The state is measured by opening the directory and
reading one name rather than by stat-ing it, because a directory can be stat-able and still
refuse to be listed.

**A surface has three reaches.** *Reachable* and *missing* are both measurements. **Not
resolvable at this notch** is the third: the surface's home is one this target does not back —
a machine-scoped shared directory, a home-root redirect, an absolute path — or this workspace
has no jail home at all. Saying "absent" for that sends the reader looking for a render that
was never going to land here. For a workspace target the distinction is read off the *directory*
rather than derived a second time, because that directory **is** the mount source: a missing
file inside a directory that exists is a genuine absence, and a directory that does not exist
means this workspace never backed that home.

## The host layer — a staged copy, a baseline, or nothing

One input to a composed surface is the **host layer**: the user's own pre-existing copy of the
file, which yolo layers its keys over instead of discarding. Three different files answer to
that description and they do not hold the same bytes — the copy the **launch staged** under
`/ctx`, the destination **inside** the jail, and the destination **on the host**. The boot
render reads the staged copy. A preview read the destination, which after one boot is yolo's
own composed output.

**The preview composes the staged copy, never the destination.** Where no staged copy is
reachable — host-side, where there is no `/ctx` at all, and inside a jail asked about a
*different* workspace, where the `/ctx` present belongs to this jail rather than that one — the
layer is reported **unavailable** and the preview composes without it. That is the judgement
`render` already makes about the computed layer: decline to invent one and say so, rather than
read a different file and call it the user's input.

### A staged copy is not always a layer

Where yolo manages the host home, the host's file **is yolo's own render**. Folding it back in
would make a key yolo wrote indistinguishable from the user's, so a pack overlay that later
changes or is removed leaves its old value in the file forever — the circularity
`SkillTarget.HostSource` was deleted for, one file over.

The jail cannot tell the two cases apart: `host_management` is deliberately not inherited into
a container, because in there the referent rebinds to the container's disposable home. So the
**launcher** says which, on the report the boot render already switches on.

| What the bytes are | What the launch says | What the jail does with them |
| :--- | :--- | :--- |
| the user's own, which yolo has never written | delivered, unlabelled | composes them as the **host layer** — the path [P7](#principles) keeps frictionless |
| yolo's own render, over what was the user's | delivered, labelled a **render** | **never a layer.** They are the **baseline** the jail reports divergence against; the surface composes from its packs and its capture alone |

A **baseline** is not a layer: nothing composes from it, so no key yolo wrote can re-enter as
the user's, and [P6](#principles) holds. The user's live edits are not lost in the second case —
they are the capture, which is already its own layer. Their pre-yolo original is in the
adoption archive, which is not a layer and must not become one.

> [!WARNING]
> **It is keyed on the MARK, not on the posture, and getting this backwards breaks every
> default install.** An absent `host_management` resolves to **`assert`**, so a posture test
> would label every default install's `~/.claude/settings.json` a render and stop every jail
> composing the settings file its user already has — [P7](#principles) inverted by the mechanism
> meant to serve it. [P6](#principles) states the rule on the **write**, and a write leaves a
> mark: the host provenance record, which `hostProvenanceExists` already reads as *"has yolo
> ever asserted this surface in this home"* and which every host render writes and no dry run
> does. So the launcher labels a delivery off that mark (`HostSurfaceRendered`,
> `hostLayerIsRender`), and the posture decides only whether there can be a *next* write.

### How it travels

The label is a fifth disposition on the **same** `YOLO_HOST_LAYERS` report, not a second
variable: `HostLayerWire` embeds the four-disposition report and adds one list of labelled
destinations. The embedding is load-bearing — an anonymous struct field is inlined by
`encoding/json`, so the wire stays the flat object the existing reader already parses, and a
half that knows only the four fields sees exactly what it saw before.

Two ordering rules the mechanism rests on:

- **The label is checked before the file is read.** A labelled path is delivered *and*
  readable, so a reader that consulted the report only on a failed read would compose exactly
  the bytes the label exists to keep out.
- **A grant that feeds no host-layer surface is never labelled.** The delivered list also
  carries `host_files` and `mount` contributions, which are not config layers at all.

An absent or unparseable report is **unknown**, which composes the layer and refuses nothing:
absence means only that the launcher is older than the variable. Exactly one disposition
refuses a launch — *the launcher delivered this path and the jail cannot read it there*.

### `host_files` surfaces

A `user` surface — the pseudo-agent every `host_files` entry lowers to — takes its **codec,
defaults and managed layers from the matching entry**, through the one lowering the boot render
uses (`entrypoint.HostFileSurface`), so a preview and a boot cannot disagree about which layers
an entry contributes. Its host layer is the declared inline content, or the staged copy, under
the same rule: the staged copy, never the destination, which for these is the very file a reset
is truncating.

The consequence is that the codec-less arm now covers **exactly one case**: an entry the user
has since removed. Its sidecars are still on disk — discovering surfaces from file names rather
than from config is precisely why `reset user` can clean up after one — and there is no
declaration left to re-render from, so discarding the sidecars is the whole of reset for it.

## Discarding a jail's captures from the host

Host-side there used to be no way to discard a jail's captured edits: the only exit was
`yolo config reset` **inside** the owning jail, so discarding a stopped jail's captures required
launching it — and a launch renders and captures first. The undo was reachable only by
performing the act being undone.

A host-side `reset` at a **jail** notch now needs **no `--force`**. It acts on the workspace's
own home overlay, which is the same directory the jail sees as its home. The hazard the write
guard names — *these surfaces resolve against a real home* — is true of a real home and false
of a workspace's own overlay, which is why the guard lets this one case through.

**It refuses while a jail for that workspace is running.** A running jail's copy of the file is
live: the agent inside may read it at any moment, and the jail captures on terminate, which
would fold the host-side truncation back in as an edit — the discard undoing itself. The
refusal costs nothing, because the remedy it names, the in-jail command, is available exactly
then.

> [!WARNING]
> **The liveness probe is tri-state and the third answer refuses too.** *"The runtime says no
> jail is running"* and *"the runtime could not be asked"* are the same empty answer to a naive
> reader, and a write is the case [P3](#principles) is strongest for. **`--force` reaches both
> refusals**, deliberately: the flag's shipped meaning is *"I really mean to write these
> files"*, it already reaches every other arm of the guard, and narrowing it here would retract
> a shipped hatch on the way to widening the default. The unqueryable case needs it most —
> there the remedy the running refusal offers may be unreachable precisely because the runtime
> is broken.

The probe asks about the container name a launch would use, through the frozen naming contract
rather than a name derived twice, and takes its runtime off the target, which resolved it the
way `yolo ps` does. `jailLivenessProbe` is a package variable because it is the one seam a test
cannot supply otherwise: a bare test runner has no container runtime, so without it every test
of this refusal would exercise the unqueryable arm and the other two would be unreachable.

The truncation writes the file back as a **pure render**, and it inherits the preview's host
layer for that: it read the surface's own destination until this design, and for a host-layer
surface that is the file being reset — the captured edit came straight back in as a host key
and the reset was a no-op on the surface class most likely to have one.

## Where this does not reach

Three gaps, all live at the stamped commit. None is hypothetical and none is a plan.

**1. `macos-user` never labels a host layer, so a managed home's host file still composes as a
layer there.** That backend builds its own report in its own plan builder
(`internal/macosuser`, `hostLayerWire`), constructing the four-field
`packload.HostLayerReport` directly; nothing in that package references the wire type, the
label constant or the mark. The container launcher is the only producer that computes the
label. The gap is reachable rather than theoretical: the darwin bootstrap calls the same
surface configuration the container boot does, so the identical reader runs — finds the path
delivered and unlabelled, reads it, and composes it as a layer. **The one thing pinned today is
compatibility, not coverage:** the tests and comments that mention that plan builder establish
only that adding the fifth disposition cannot break a reader that knows four.

**2. The fifth disposition is unmeasured end to end.** The launcher's label and the boot
render's reading of it are each pinned by unit tests; no test drives one managed home through
both halves in a real launch.

**3. `yolo apply --sealed` keeps the bare-working-directory walk, and `--at` is parsed twice.**
`applySealed` calls the pre-target spellings — `workspaceRoot`, which falls back to the bare
working directory, and `overlayKeyCount`, which reads the store that walk resolves — so from a
directory with no workspace it resolves an empty store, finds nothing undeclared, and reports
**"sealed"**: the confident empty answer, in the one verb whose entire job is refusing on
undeclared input. Whether that verb should take the config target is unruled, so the behaviour
is deliberately byte-identical rather than changed silently. `workspaceRoot` survives for it
alone, fenced by an allowlist test that names the two files permitted to call it; the store
wrapper it reaches through is itself unpinned, so a *new* caller of that wrapper would spread
the bare-working-directory answer without tripping anything.

`--at`'s token shape is genuinely written twice — once in `apply`'s own argv loop, once in
`extractAtFlag` — and the two differ in three small ways: `apply` hardcodes the notch list in
its messages while the config verbs derive it, `apply` validates against the confinement
vocabulary directly while the config verbs go through the render kind resolver, and `apply`
refuses unrecognised arguments where `extractAtFlag` passes them through for each verb's own
parser. What is **not** duplicated is the thing [P4](#principles) is about: two tests pin that
the read verbs and `apply` accept the same set and refuse `guest` with the same sentence, by
running both commands rather than by comparing two lists.

## What this does not license

- **No change to `host_management`, its values, or the unset answer.** Absent still resolves to
  `assert`, silently, and that fact is load-bearing for the label rule above.
- **`config capture` stays refused host-side, including under `own`.** Its premise is privacy —
  a credential copied out of a real file — not ownership.
- **No new store, no new sidecar, no new file format, and no migration.** Every path here is
  derived at read time.
- **No `guest` notch.** `--at guest` is refused by name, with the same sentence `apply` prints.
- **No second notch vocabulary.** No `yolo host config`, no `--jail`/`--host` pair.
- **No `--workspace` flag.** The launcher's refusal rules one out in as many words, so every
  remedy this document names is a `cd` or an `--at`.
- **Not a repo-scoped host input.** [P5](#principles) is a restatement, not an extension.
- **Not a licence to hard-code the container workspace in-jail.** That is wrong the moment the
  CLI runs for a different workspace than the one it is jailed for — a nested jail, and every
  integration test — where it read the wrong sidecars and `reset` would have deleted them.

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo. Every id keeps its original spelling
and its original anchor, because they are cited from code comments across `internal/cli`,
`internal/cli/run` and `internal/entrypoint`, and this table is where they resolve.

> [!NOTE]
> [`OQ-CR1`](../plans/cache-relocation.md#-oq-cr1--is-cache_relocations-the-right-level-held)
> and
> [`OQ-CR2`](../plans/cache-relocation.md#-oq-cr2--whether-the-relocation-should-also-be-reflected-host-side)
> are **also** the ids of two unrelated `cache_relocations` questions in
> [`../plans/cache-relocation.md`](../plans/cache-relocation.md). Link every citation with its
> file.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="oq-cr1"></a>[`OQ-CR1`](#oq-cr1) | **The working directory selects the TARGET** — workspace *and* notch — and `--at` overrides | The obvious "fix" is to import the write side's *"the `cwd` selects nothing"*. That governs what a directory may change about a real home's configuration; choosing what a report is **about** is not that hazard, and the working directory is the one input that already names the jail a reader means. What had to go was the **pair**, not the inference. |
| <a id="oq-cr2"></a>[`OQ-CR2`](#oq-cr2) | **Marker: the launch artifact OR a workspace config file. No workspace resolves the HOST target, under disclosure — not a refusal** | A launch artifact alone answers *"has a jail run here"*, which is not what a workspace is: a fresh clone with a committed config is one before its first launch. And outside a workspace the only home to describe is the host's, so naming it removes the confident-empty-answer failure exactly as well as an error does, with an answer instead. It is a target, not a permission — the write guard is untouched. |
| <a id="oq-cr3"></a>[`OQ-CR3`](#oq-cr3) | **One target, one store.** `diff` and `ls` resolve the capture store through `render.Target`, as `reset` already did | Closes the owned-host disagreement by **removing the second resolution**, not by teaching the readers about ownership. The tempting alternative — show both stores, labelled — is a two-home report wearing a feature's clothes. |
| <a id="oq-cr4"></a>[`OQ-CR4`](#oq-cr4) | **A host-side `reset` at a jail notch exists, with the running-jail refusal** | The store was always workspace-keyed and the surface file was always a resolver away. Without it, discarding a stopped jail's captures requires launching that jail — performing the act being undone. The refusal costs nothing because the in-jail command is available exactly when it fires. |
| <a id="oq-cr5"></a>[`OQ-CR5`](#oq-cr5) | **The disclosure is unconditional**, on every invocation | *"Only when surprising"* would make the line's **absence** carry information the reader cannot decode. Compression is allowed; suppression is not. |
| <a id="oq-cr6"></a>[`OQ-CR6`](#oq-cr6) | **The staged copy, never the destination; no staged copy means the layer is reported UNAVAILABLE. And where the bytes are yolo's own render they are a BASELINE, never a layer** | Reading the destination feeds yolo's previous output back in as the user's input. Delivering a managed home's file as a baseline rather than withholding it keeps what the jail actually wants — *how does what I have differ from what the host has* — while giving up only host-side provenance, which a jail has no business reporting. ⚠ **As built the discriminator is the MARK, not the posture**, because an absent `host_management` is `assert`; a posture test would stop every default install's jail composing its user's own settings. |
| <a id="oq-cr7"></a>[`OQ-CR7`](#oq-cr7) | **`diff` reports the captured divergence and nothing else, and keeps its name.** Per-key provenance moves to `ls` | Decided on the verb's **subject**, not on the defect: provenance is a fact about the render and would read identically before and after the wipe `diff` measures against. The name was never the problem — the second block was. |
| <a id="oq-cr8"></a>[`OQ-CR8`](#oq-cr8) | **A jail does read the host's own config file, for surfaces that declare it** — settled twice elsewhere, and kept here on [P7](#principles) | It was ruled *retired* in favour of a local pack once and **reversed** later, never implemented in between, and the reversal is what is built. The substantive reason the reversal never gave it is [P7](#principles): this layer is the onboarding path, so adopting yolo costs no migration. Reopening means arguing against that. The narrow coverage is deliberate: two surfaces declare a host layer and a third has to argue for itself. |

## Current values

Verified at `f659d878`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Notch selector | `--at jail\|guest\|host`, on every `yolo config` verb but `promote`, `drift`, `dump` | `internal/cli` (`extractAtFlag`, `notchlessVerbs`) |
| Disclosure stream | stderr, every verb, every invocation | `internal/cli` (`configRunW`) |
| Workspace markers | `.yolo/config-boot.json`, or any name `config.LoadWorkspaceConfig` reads | `internal/cli` (`workspaceMarked`, `workspaceConfigNames`) over `internal/config` |
| Workspace capture store | `<workspace>/.yolo/prism` | `render.Target.SidecarDir` at the jail kind |
| Host capture store | under the user's global storage, and **only** under `host_management: own` | `render.Target.SidecarDir` at the host kind |
| Jail home overlay backing a workspace's surfaces | `<workspace>/.yolo/home/…`, backend-dependent within it | `internal/cli` (`jailHomeHostLocation`) |
| Host-layer report variable | `YOLO_HOST_LAYERS` | `packload.HostLayerEnvVar` |
| Dispositions | four in `internal/packload`, plus `render` | `packload.HostLayerDisposition`, `entrypoint.HostLayerRender` |
| The label's wire field | one list of `/ctx` destinations, embedded in the four-field report | `entrypoint.HostLayerWire` |
| Unset `host_management` | resolves to `assert` | `config.HostManagementMode` |
| Liveness states | not-running, running, **could-not-ask** — the last refuses a write | `internal/cli` (`jailLiveness`) |
