---
status: current
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f344
covers:
  - internal/config/hostmanagement.go
  - internal/config/promotiontarget.go
  - internal/render/modes.go
  - internal/render/target.go
  - internal/cli/configpromote.go
  - internal/cli/configpromotewrite.go
  - internal/cli/configpromotesensitive.go
  - internal/cli/configdiff.go
  - internal/cli/hostmanagementgate.go
  - internal/cli/hostrevert.go
  - internal/agentcfg/staterender.go
  - internal/agentcfg/promote.go
  - internal/agentcfg/literalnull.go
  - internal/agentcfg/luahook/derive.go
  - internal/entrypoint/adoptionarchive.go
  - internal/entrypoint/hostrender.go
  - internal/entrypoint/hostrevert.go
  - internal/entrypoint/prism.go
  - internal/packload/hostlayer.go
tags: [config, host, capture, packs, ownership, promotion]
summary: "Who owns an agent's config file is declared by the user-scope `host_management` key (`none`, the unset answer, or `own`), never inferred from the confinement notch. Under `own` the host renders like a jail — whole-file composition, a host capture store, host-side reset and capture — and adoption of an existing file is capture-then-regenerate behind a one-time archive. `yolo config promote` is the way out of capture: it declares captured keys in the conventional local pack (or a user's own pack) and clears them from the overlay, refusing redundant, dead, credential-named, environment-bound and outranked keys."
---

# Config ownership — `host_management`, adoption, and promotion out of capture

**Status:** Verified 2026-10-16 against `8e65f344`. Graduated from the design doc
`docs/design/config-ownership-and-promotion.md`, deleted the same day in `60168f077` (git history keeps it).
Two questions are open and block nothing ([Open questions](#open-questions)); the measured
behavior no ruling has closed is in [Known gaps](#known-gaps).

yolo writes agents' config files at several **notches** — the places a render lands: a jail, a
preview, the real host home, a guest (the vocabulary is
[`config-target-resolution.md`](config-target-resolution.md)'s). Whether yolo **owns** a file
— may regenerate it whole and delete keys from it — is not a property of the notch. Inside a
jail yolo always owns what it renders. At the host, ownership is a contract the user declares in
one user-scope key, `host_management`, and that contract selects which render mechanisms the
host notch runs. Under `own` the host renders like a jail. Any key a user types into a
composed file is **captured** — recorded as a patch in a sidecar yolo keeps — and
`yolo config promote` turns captured keys into declared ones, so an edit made in one jail can
reach every jail and the host.

| Component | Lives in |
| :--- | :--- |
| The key, its user-scope read and validation | `internal/config` (`HostManagementMode`, `HostManagementDeclared`, `KnownHostManagements`, `HostManagementRetired`) |
| Which mechanisms a notch runs (the mode census) | `internal/render` (`ModeSet`, `HostOwnedModes`, `Target.Modes`, `HostOwnershipFor`) |
| Where each notch keeps capture, provenance and archive state | `internal/render` (`Target.SidecarDir`, `ProvenanceDir`, `ArchivePath`, `SelectionPath`) |
| The host render and its loss reporting | `internal/entrypoint` (`RenderHostPack`, `RenderHostSurface`, `HostRenderResult`) |
| Adoption of an existing file | `internal/agentcfg` (`ComposeStateful`, `dropComputedTables`, `narrowOverlay`, `Inputs.LiteralNulls`) |
| The adoption archive | `internal/entrypoint` (`archiveAdoption`, called from `persistStatefulSurface`) |
| Host gate, revert and the `none` refusal | `internal/cli` (`hostManagementRefusal`, `hostRevertRefusal`, `confirmHostLosses`), `internal/entrypoint` (`hostrevert.go`) |
| `yolo config promote` | `internal/cli` (`configpromote.go` classifies, `configpromotewrite.go` writes, `configpromotesensitive.go` names credentials), `internal/agentcfg` (`DeadOverlayKeys`, `DeleteOverlayKeys`) |
| The derive's in-full declaration | `internal/agentcfg/luahook` (`ctx.in_full`, `DeriveOutput.InFull`) |

**Reads with:** [`pack-system.md`'s compose engine](pack-system.md#config-surfaces-and-the-compose-engine)
(the layer stack and the capture overlay),
[`config-target-resolution.md`](config-target-resolution.md) (which home a `yolo config` verb
is about, and the render/baseline mark),
[`config-migration-to-prism.md`](config-migration-to-prism.md) (capture and the first
migration in a jail), [`host-apply-staleness.md`](host-apply-staleness.md) (when a host
re-render is checked).

---

## Principles

Five principles, numbered because code comments and sibling docs cite them.

- <a id="p1"></a>**P1 — Ownership is declared, never inferred.** A file yolo writes is owned by
  yolo or by the user, and which one comes from the user's config. Inferring it from the
  confinement notch is a confinement fact wearing an ownership hat.
- <a id="p2"></a>**P2 — The notch selects confinement; it does not also select the ownership
  model.** The two are independent axes. The host is differently *confined*, which says nothing
  about who owns its files.
- <a id="p3"></a>**P3 — Capture is a staging area, not a terminal layer.** A value that wins the
  merge while nothing declares it is a hole in the definition. The fix is to make declaring it
  easy, not to stop capturing (the leaning recorded as
  [`Q1`](../design/environment-manager-user-stories.md#Q1)).
- <a id="p4"></a>**P4 — Promotion is the only non-destructive exit from capture.** The other
  exit, `yolo config reset`, discards.
- <a id="p5"></a>**P5 — The host is a notch like any other, except where a real home forbids
  it.** Same modes, same verbs, same sidecars. The one legitimate asymmetry is
  [deletion](#deletion-and-adoption). Only the *primitive enforcing a boundary* may differ
  between notches — the parity rule [`OQ-HT4`](macos-user-home-tiers.md#oq-ht4) states for
  backends, applied to notches.

### Terms

Three terms are coined by this system *(coined here)*, and they classify **keys inside a
captured overlay** — not files, and not a security model:

- **Portable** — a key that means the same thing on every machine.
- **Environment-bound** — a key whose value is an artifact of the workspace, host or jail it
  was set in (it names the workspace root or the jail home).
- **Sensitive** — a key that may carry a credential, judged by its name.

Two more words have narrow meanings here:

- **Unset** — `host_management` has no value at all. Not *undeclared*, which this corpus
  reserves for the input-closure tier of
  [`yolo apply --sealed`](what-yolo-is.md):
  a value inside an agent's config that shapes an environment while nothing names it. An
  undeclared key is promoted or discarded; an unset key is written.
- **Adoption** — the first owned render into a home that already holds the file: yolo captures
  what is there, then regenerates the file from layers plus that capture. Not a merge, and not a
  one-time import: from then on the file is derived output.

---

## Invariants

- **Ownership is the user's statement, read from user scope only.** A workspace
  `yolo-jail.jsonc` is jail-writable, so a cloned repository must not be able to make yolo own
  the user's home. A workspace-scope `host_management` is a validation error; the key is not
  inherited into a nested jail, where its referent would rebind to a disposable home.
- **An unreadable user config grants no write claim.** It reads as `none`. The shared
  user-scope helper (`config.UserScopeConfigOrEmpty`) picks no failure direction, so each key
  states its own — `agent_updates` fails *open* through the same helper, because it is an
  opt-out.
- **The key selects the mode; it grants no approval.** Exactly as `host_apply_on_launch`
  (*"THE KEY ENABLES THE MECHANISM. IT DOES NOT GRANT THE APPROVAL"*). Prompts are driven by
  what a write would do, never by which key is set: nothing would change ⇒ silent; a change that
  loses nothing the user has ⇒ still silent.
- **One writer for the promotion destination.** `~/.config/yolo-jail/local/pack.json` is
  written by the human and by `yolo config promote` — never by a jail, a launch, or
  `yolo host apply`, which only reads packs.
- **A key is declared in exactly one place.** Promotion's declare-and-reset is one logical
  write: if any part fails, every file is restored from its pre-image. A half-promoted key —
  declared *and* captured — is the state the verb exists to end.
- **Every notch resolves its state paths through `render.Target`, never by hand.**
  `SidecarDir`, `ProvenanceDir`, `ArchivePath` and the capture-file names live on the target, so
  the entrypoint that writes and the CLI that reads share one definition. An empty `SidecarDir`
  means "this target keeps no capture state", at every notch.
- **The census is the authority for what a notch runs.** The host render asks `ModeSet` per
  surface and refuses in `Excludes()`'s words; a mechanism is reached by the census answering
  it, never by a second `if` beside it.

---

## Surface modes and the layer stack

A pack declares one **mode** per config surface:

| Mode | What it does | Captures edits? |
| :--- | :--- | :--- |
| `stateful` | composes the whole file from layers and captures in-jail edits (the default) | yes |
| `computed` | composes the whole file and overwrites it every render | no |
| `rmw` | read-modify-writes an agent-owned file: only declared keys are rewritten | no |
| `unrendered` | yolo does not write the file | no |

The layers fold in ascending precedence:

```text
defaults → host → workspace → config-overlay:<pack>… → overlay (capture) → computed → managed
```

`config-list` additions apply after every `config-overlay` and below capture
([`OQ-AL2`](pack-system.md#oq-al2)). `managed` is a pack's floor and wins everything; `overlay`
is the capture sidecar. `config-overlay` sits **below** capture, which matters to
[promotion's precedence check](#precedence--promotion-moves-a-key-down-the-stack).

### Why capture and `rmw` are not interchangeable

In steady state they can leave the same bytes. They differ in what they *permit* when something
changes:

| | `rmw` | compose + capture |
| :--- | :--- | :--- |
| A key in the file that no layer supplies | survives forever | deleted on the next render |
| The file is | a history of every write | a function of declarations plus the captured overlay |
| Delete the file | gone; nothing rebuilds it | the next render reproduces it |
| An in-jail edit | bytes in one file | a recorded patch yolo owns |

The one structural difference is **regenerability**. Capture is `mergeDiff(previous render,
current file)` — the minimal RFC 7386 patch from yolo's last render to the file as it stands,
with a deleted key recorded as a `null` tombstone — accumulated into the overlay. Because the
edit already exists as a patch, tooling has something to compare against (`config diff`,
`reset`, drift) and promotion has an object to lift. `rmw` records nothing, so there is nothing
to lift. (One exception to the overlay: at an array path a `config-list` targets, capture
records per-entry additions and removals in a separate list sidecar —
[`OQ-AL1`](pack-system.md#oq-al1).)

> [!IMPORTANT]
> **Judging the two by the file they leave behind judges them by the one case that cannot tell
> them apart.** An owned file is derived output and disposable wherever it lives, host
> included; a file holding bytes that exist nowhere else is not disposable on any notch. The
> dangerous moment is the **transition** into ownership, not the address — which is why the
> guards sit on [adoption](#adoption-is-capture-then-regenerate).

---

## The `host_management` key

```jsonc
// ~/.config/yolo-jail/config.jsonc — USER SCOPE ONLY
{ "host_management": "own" }   // "none" (the unset answer) | "own"
```

| Value | Host surfaces render as | Who owns the file | `yolo host apply` |
| :--- | :--- | :--- | :--- |
| `none` (also unset) | nothing | the user, entirely | writes nothing, names the key and the way to `own` |
| `own` | the census's `HostOwnedModes` — `stateful` for a composing surface | yolo — the file is derived | composes the whole file and captures edits |

- **Under `none`** the host file can still be *read* into a jail as the `host` layer, for a
  surface that declares `readsHost` ([the read-in host layer](#the-read-in-host-layer)).
  Ownership governs writing; the read is a separate, declared grant. With nothing rendered,
  `host_apply_on_launch` has nothing to check.
- **Under `own`** the file belongs in `.gitignore`; what goes in a dotfiles repository is the
  pack. An unset `host_wrappers` derives from `own`, and an unset `host_apply_on_launch` follows
  `host_wrappers`, so `own` alone turns on the launch-time re-render; an explicit `false` at
  either key wins ([`host-apply-staleness.md`](host-apply-staleness.md#the-opt-in-key)).
- **`own` still runs `rmw`** for a surface whose pack declares `rmw` (`claude/config`,
  `copilot/config`): `own` is the user's statement about who owns the file, not a licence to
  overrule the pack's statement about what kind of file it is. Those surfaces declare no
  `readsHost`, so they are written and never read back into a jail.

### The unset state

An absent key is not a third value. It reads as `none` with **no prompt and no notice at
upgrade**, and `config.HostManagementDeclared` still tells it apart from a written `none`. Each
value explains itself at the point of the act instead:

- `yolo host apply` under `none` writes nothing and says whether the key is unset or written,
  then names `own` (with `yolo config promote` after it, for keys kept by hand) and
  `yolo host apply --revert` for keys an earlier yolo wrote. Under `--format json` the refused
  apply still emits its dry-run document, outcome `refused`.
- `yolo check`'s host-management row says whether the key is written or unset.
- `yolo apply --sealed` **refuses** while the key is unset, beside the undeclared inputs it
  already refuses for: an environment whose host-ownership contract is unstated is not sealed.
  This is the one place an unset key bites.

### The retired `assert`

`host_management` once had a third value, `assert` — the default — under which the host ran
pure `rmw` and yolo shared one file with the user. It was ruled out on 2026-09-20 and removed on
2026-10-05 ([`OQ-CO1`](#oq-co1), [`OQ-CO14`](#oq-co14)). A config still saying `"assert"` is
refused by name wherever yolo would act on it — validation (so a launch, `yolo check` and
`yolo internal validate`), `yolo host apply` and `yolo apply --at host`, `--revert`, a wrapped
`yolo host -- <bin>` launch, `yolo pack update`'s host half and `--sealed` — with one message
naming `none` and `own`. The read-only `--at host` verbs (`config ls`, `diff`, `render`) read it
as `none`.

**Why it went: `assert` is the one contract where P6 and P7 of
[`config-target-resolution.md`](config-target-resolution.md#principles) collide.** P6 (*yolo
never reads back a file it writes*) is defended by a per-file mark — the provenance record —
that labels a delivered host copy a **baseline** rather than a layer. Under `assert` one file
held both the user's keys and yolo's, with no capture to separate them, so one host apply was
enough to stop the user's own keys reaching any jail: P7 (*adopting yolo requires no
migration*) inverted. At `none` yolo writes nothing; at `own` the user's keys live in the
capture overlay, a layer of its own. Reproduced below a launch by
`TestAHomeAssertedIntoBeforeTheRetirementStaysABaselineToTheBoot` (`internal/cli/run`).

**A home `assert` wrote into is left exactly as `assert` last rendered it.** Its provenance
mark stays ([`CO-D7`](#CO-D7)), so a jail keeps treating the keys yolo wrote as yolo's; the
cost is that the user's own keys in that file reach no jail either, until `--revert`. Choosing
`own` later takes the file over the way any first owned apply does, archiving it once. The
first owned apply also reads the selection record `assert` kept beside the provenance record, so
a model pick the user made then is kept ([`CO-D14`](#CO-D14)).

> [!WARNING]
> **Retiring `assert` did not retire `rmw` at the host, nor the machinery around it.** The
> `retired:<layer>` provenance label, `LayerAsserted`'s refusal of `host`, and the
> render/baseline mark all still have `own`-side callers (`provenanceretire_test.go`, and the
> end-to-end tests of [`CO-D7`](#CO-D7)). Do not delete one on the strength of "the rmw notch is
> gone": `claude/config`'s anti-laundering pass goes with it.

---

## The host notch under `own`

Under `own` the host notch runs whole-file composition, so everything a jail has follows: a
capture overlay for edits made between applies, `yolo config diff`, `reset` and `capture`
working host-side without `--force`, and `--revert`.

### The host capture store

The host's capture state lives at `<home>/.local/share/yolo-jail/host-capture/`, beside the
host provenance record and outside any workspace, so it never crosses into a jail or a git
tree. It holds the same three files per surface a jail keeps under `<workspace>/.yolo/prism/`
— `<agent>-<name>.last_render`, `.overlay.json`, `.selection.json` — plus the list capture.
`render.Target.SidecarDir` answers it for the host under `own` and `""` under `none`. The
directory is `0700` holding `0600` files (a `0600` directory would refuse even its owner).

**The provenance record is not one of them.** It stays at
`<home>/.local/share/yolo-jail/host-provenance/<agent>-<name>.provenance`: per-key attribution
of who wrote each top-level key, written at every host apply and consumed by `--revert`,
`config diff`, overlay pruning and the render/baseline mark. Two directories because two
lifetimes — host-side `reset` is entitled to delete capture state, never the record.

Host capture under `own` is a precondition of adoption, not a convenience: without it the first
owned render would compose from declared layers alone, the empty-overlay seed that was a shipped
data-loss bug in a jail ([adoption](#adoption-is-capture-then-regenerate)). Under `none` there
is no store, and host-side `capture` refuses before `--force` is read, naming `own`.

### Host-side verbs under `own`

- **`yolo config capture`** persists the *capture half of the host apply's own render* of the
  surface, in observe (`HostRenderResult.Capture`) — the same composition, so it narrows
  against the same managed and computed layers the apply uses. It records nothing over a
  surface with no baseline or no file, and on a surface the apply refuses it captures nothing
  and exits 1.
- **`yolo config reset`** discards the sidecars, truncates the file to its pure render,
  re-seeds the baseline it has just made true (`cli.reseedResetBaseline`), and then runs the
  owned write for that one surface (`entrypoint.RenderHostSurface`), so the file comes back
  with the computed layer, other packs' overlays and the `profile` selection. If that
  composition cannot be built, the reset still discards and truncates, and says what is missing
  and that `yolo host apply --assert` lands it.
- Both take the host-apply lock and, finding it held, refuse with the command to re-run.

> [!WARNING]
> **Reset must truncate as well as discard.** Adoption is safe against `reset` only because
> reset leaves no foreign bytes to adopt: reset → no baseline → adopt would otherwise resurrect
> the very edits the user asked to discard.

### Revert

`yolo host apply --revert` withdraws what yolo wrote into a real home, on the provenance record.
It **runs under `none`**, set or unset — the one way back to a file that is purely the user's —
and says "never rendered" over a home with no record. It is **refused under `own`**, naming
`none`, because the next owned apply would compose the keys back ([`CO-D3`](#CO-D3)).

- **It withdraws values, never a whole key holding the user's own** ([`CO-D12`](#CO-D12)). The
  record is per top-level key, so each recorded key is checked against finer evidence: a key
  the selection record names goes only while it holds the selected value; a `defaults` key only
  while it holds the declared default; an object loses only the leaves the computed-leaf record
  or the managed layer name, still holding yolo's value. A key with no such evidence still goes
  whole. Every value kept is named in the report with its reason.
- **It forgets the surface's capture-store files** too, so the next owned apply is a first
  apply again rather than replaying the revert's removals as the user's deletions
  ([`CO-D13`](#CO-D13)).
- `own`'s stateful writer keeps the computed-leaf record as well as the `rmw` arm, so a value
  `own` wrote (a Bedrock switch, say) is attributed to yolo and withdrawable; a value the file
  held before, unclaimed, stays the user's ([`CO-D15`](#CO-D15)).

---

## Deletion and adoption

Whole-file composition means yolo may **remove** a key from a real host file. Under `own` that
is correct — a derived file holds what the definition says — and it is the one place where
"the host is a real home" changes the answer. After adoption, a removal is ordinary and
reported, exactly as in a jail.

### Adoption is capture-then-regenerate

`ComposeStateful` treats "no trusted `last_render` for this surface" as a **first migration**
and seeds the overlay from the file it finds: `residue = mergeDiff(pureRender, current)`. The
render is then declared layers plus that residue, which reproduces the file's undeclared keys.
Switching a host surface to `own` is exactly that branch, so capture happens before the first
owned write and the write puts back what capture took. The branch exists because seeding an
empty overlay was a shipped data-loss bug — `copilot/config` collapsed to `{"yolo": true}` and
logged the user out (the `B1` fix in `staterender.go`). Adoption also **adds** yolo's declared
keys the file lacked.

The residue is narrowed in two passes before it becomes the overlay:

1. **`dropComputedTables`, wholesale, only for a declared table.** A key the computed layer
   holds as a non-empty object is taken whole only when its derive declares it regenerated in
   full ([in-full tables](#in-full-tables)). An empty computed table claims nothing.
2. **`narrowOverlay`, leaf-level, against every layer above the overlay.** A leaf `managed` or
   `computed` re-asserts is never captured, on either branch. A sibling leaf under a deep-merged
   owner — the user's `permissions.ask` beside yolo's managed `permissions.defaultMode` —
   survives. Because the rule runs on the accumulated overlay every boot, the store is
   self-healing: a sidecar an older yolo dirtied is canonicalized on the next boot.

> [!WARNING]
> **Do not "retain" a capture that a higher layer overrides, waiting for the layer to stop.** The
> pending edit is invisible and can sit for months: it would silently restore a stale
> `permissions` grant, or resurrect an MCP server the user deleted
> ([what the first migration keeps](config-migration-to-prism.md#adoption-what-the-first-migration-keeps)).

### What adoption does not cover

| Class | What happens | Why |
| :--- | :--- | :--- |
| A key inside a non-empty `computed` table **declared in full** — `mcpServers`, `mise`'s `[tools]` with a pin | not adopted | the table is regenerated in full; adopting would resurrect a dropped entry (*regenerate, don't reconcile*) |
| A key `managed` or `computed` re-asserts | never captured, either branch | both fold above the overlay and win, so a captured copy would be noise `config diff` reports as a phantom edit |
| A **keyless** surface (`raw`, `lines`) | not adopted, and refused under `own` at the host ([`OQ-CO9`](#oq-co9)) | the whole file would become the overlay and outrank every declared layer forever |

A keyless surface has no `rmw` either (`rmwCodecRefusal` refuses it by kind). No shipped pack
declares one; a user's `host_files` entries are rendered in the jail, not at the host notch.

> [!WARNING]
> **The `mise` scrub has no no-pin half.** On a jail that pins no `mise` tool, an old
> yolo-written `node`/`python`/`go` line and a `mise use -g node@22` typed by hand are the same
> bytes, so the empty `[tools]` table claims nothing and both are kept. Getting the scrub back
> needs a record of what yolo **wrote**, which a first migration by definition lacks.

### Keys-and-values invariance

Switching to `own` keeps **every key and every value** the file holds outside the classes
above ([`OQ-CO12`](#oq-co12)). The comparator: decode both files with the surface's own codec
and compare structurally — objects by key set and values, arrays element by element in order,
scalars by value — and **a key valued `null` is a key, not an absence**. What the codec does not
decode is outside the criterion: byte layout, indentation, key order, and TOML comments and
yolo's generated header. `own` composes through the surface's codec; it does not imitate
`rmw`'s byte layout.

Two mechanisms keep values the merge model cannot spell:

- `dropNullLeaves` keeps an object the user wrote empty (a patch never carries `{}` for any
  other reason).
- `agentcfg.Inputs.LiteralNulls` carries a literal `null` read off the decoded file *beside*
  the layer stack, because inside an RFC 7386 patch a null means "delete". It folds at the
  capture overlay's own precedence: `computed` and `managed` beat it, every lower layer loses to
  it. The capture overlay itself is excluded as evidence — it is a record of the file, and
  `mergeDiff` cannot tell a key added as `null` from a key deleted.

**The switch is never silent.** `hostStatefulWouldChange` compares bytes, deliberately stricter
than the criterion, so a conformant reformat is still announced. On an owned render
`Formatting` reports that a TOML file's comments will not survive, in the dry run, before the
write (`hostFormattingLosses` branches on mechanism); yolo's own generated header is not
counted as a loss.

### The loss gate and the adoption archive

`confirmHostLosses` prompts only when something is actually lost on a **first** apply:
`FirstApply && EntryLosses`, where `EntryLosses` are the named entries of a wholesale table
(`mcpServers`) the render would drop. A home yolo has written before prompts not at all.

<a id="the-adoption-archive"></a>

Behind it, **one archive at adoption** ([`OQ-CO7`](#oq-co7)): before the first owned render —
and at a jail's first migration too, by P5 — the pre-existing file is copied once. One call in
the stateful writer both notches share (`entrypoint.archiveAdoption`, from
`persistStatefulSurface`) serves both, so the nets cannot diverge. The rule is uniform and the
net splits by primitive: a prompt where there is a TTY, the archive where there is not (a
jail's first migration runs at an unattended boot).

| Notch | Archive path (`render.Target.ArchivePath`) |
| :--- | :--- |
| host (`own`) | `<home>/.local/share/yolo-jail/archive/config/<agent>-<name>/<basename>` |
| jail / preview | `<workspace>/.yolo/archive/config/<agent>-<name>/<basename>` |

- **Keyed by surface, with no stamp layer, and once per surface for the life of the home.** The
  other archive buckets keep the newest few stamped generations; this one holds the user's own
  pre-yolo file, which nothing regenerates. `yolo prune` leaves a directory whose name is not a
  stamp alone (`PruneHostArchive`), so it never reaps this bucket. The writer copies only when
  the destination does not exist, because a second adoption's file is already yolo's output and
  would overwrite the original it exists to be compared against.
- **A failed copy refuses the adoption.** The file is left as found, the surface reports
  `refused:`, and nothing is persisted, so the next render retries the whole adoption.
- **The run's verdict counts it.** An adopting apply ends on *"Applied: 1 surface adopted
  (archived first)"*, through `hostApplySurvey.Adoptions()` — a count the
  `host_apply_on_launch` gate does not read.

| The pre-existing file is… | Archived? | Why |
| :--- | :--- | :--- |
| absent | no | nothing to lose |
| empty | no | restoring "absent" and "zero bytes" leave the user in the same place |
| unparseable | **yes** | the gate keys on bytes, not on parsing; at a jail the render replaces such a file wholesale, so the archive is the only record of it |
| present but unreadable | n/a — **the render is refused** | `composeStatefulSurface` fails closed on any read error but absence |
| yolo's own output, byte for byte | no | compared against the layers-alone render (`StatefulOutput.PureBytes`), which never consulted the file |

> [!NOTE]
> The jail's archive inherits the capture overlay's exposure: `<workspace>/.yolo/` crosses into
> a container and plausibly into git. A `host_files` entry in `mode: capture` renders stateful
> as `user/<slug>`, so a credential file the user declared is copied there — the same content
> its own `last_render` sidecar already holds in that tree.

---

## In-full tables

A derive tells the engine which computed tables it **regenerates in full** by wrapping them in
`ctx.in_full(t)`, a third derive sentinel beside `ctx.tombstone` and `ctx.empty_array`
([`CO13`](#co13)). The decoder that strips the other two unwraps it and reports the key in
`DeriveOutput.InFull`; it is refused below the top level, around an array and around anything
but a table. Nothing downstream sees the wrapper.

- **The jail notch.** `deriveComputedLayer` returns the declaration; it reaches
  `agentcfg.Inputs.ComputedInFull`, and `dropComputedTables` takes a non-empty table whole only
  when it is named there. Core's own `mise/config` has no derive, so `ConfigureMisePrism`
  declares `tools`.
- **The host notch.** `hostTableKeys` keeps only declared keys, so `claude/settings` does not
  report `env` as a wholesale table and a host apply does not clear the user's `env` block. The
  `own` arm hands adoption the same key set (`hostTableInFull`).
- **The classification criterion:** a table whose key set tracks a live table is regenerated
  in full; one with a fixed key set is not. Declared: every MCP table, `copilot/lsp`'s
  `lspServers`, the provider catalogs (`codex/config`'s `model_providers`, `opencode/config`'s
  `provider`, and `providers` on `pi/models` and `oh-omp/models`), and `mise`'s `[tools]`. Not declared: `claude/settings`'
  `env` and `modelPicker`, `pi/settings`' `subagents` — and, on purpose, `pi/subagents-mcp` and
  `pi/mcp`, whose files something besides yolo writes
  ([AM-D16](../design/agent-directory-map.md#AM-D16),
  [AM-D20](../design/agent-directory-map.md#AM-D20)). `TestShippedDerivesDeclareTheirInFullTables`
  pins both halves across two probe worlds; a new producer fails it until someone classifies it.
- **Version skew is covered in one direction.** Each `derive.lua` reaches the sentinel through a
  local guard, so an entrypoint older than the sentinel runs it unchanged. The reverse is not
  covered: a host `yolo` older than the sentinel stages derives that declare nothing, and a newer
  entrypoint then adopts MCP tables leaf by leaf — a first migration keeps a removed server.
  Only the `version.SourceSkew` launch gate refuses that pairing.

> [!WARNING]
> **The jail's `rmw` arm does not read the declaration.** `regenerateManagedTables` clears and
> rewrites every object-valued computed key, declared or not. No shipped `rmw` surface returns
> an undeclared table, so nothing observable differs today; what it should do is
> [`OQ-CO15`](#OQ-CO15).

---

## Promotion

`yolo config promote <agent[/surface]>` turns captured keys into declared ones. Its default
destination is the **conventional local pack**, `~/.config/yolo-jail/local/`: it needs no
`packs` entry, is re-read every launch, folds **last** among packs, and its `config-overlay`
contributions render at every notch. So one promotion reaches every jail and the host.
Promotion needs a destination, not a distribution mechanism.

| Destination | Written as |
| :--- | :--- |
| `local` (default, or the user-scope `promotion_target`) | a `config-overlay` on the surface in `~/.config/yolo-jail/local/pack.json` |
| `pack:<name>` | the same in that pack's `pack.json`; **refused for a fetched pack**, and for a `pack.jsonc` (its comments would not survive the encoder) |
| `host` | **retired with `assert`** and refused by name, naming `--to local` ([`CO-D6`](#CO-D6)) |
| `workspace` | refused as out of scope ([`OQ-CO8`](#oq-co8)) |

`promotion_target` is read from user scope only, so a workspace cannot redirect captured
personal settings into a pack of its choosing.

### What promote does

1. **Reads the surface's capture overlay** from `<workspace>/.yolo/prism/`. Per-entry list
   captures are not keys: promote counts them, says they cannot be promoted yet, and points at
   `yolo config diff` (`promoteListNote`).
2. **Classifies every captured key** into one disposition — the JSON wire vocabulary and the
   text report's column alike:

   | Disposition | Meaning |
   | :--- | :--- |
   | `promotable` | survives every check and would be written |
   | `redundant` | identical to yolo's last render, so declaring it adds nothing |
   | `dead` | an owning layer overrides it where it sits, and the destination is lower (`agentcfg.DeadOverlayKeys`) |
   | `sensitive` | the key, or a key nested under it, is named like a credential |
   | `environment-bound` | the value names the workspace root or the jail home |
   | `outranked` | a later-folding pack's `config-overlay` sets the same key |
   | `no-pack-owner` | the surface has no owner among the loaded packs (core's `mise/config`), so a `config-overlay` would be inert |

3. **Writes nothing without `--accept-promotion`.** The plan already lists the exact keys, so
   the dry run is the confirmation; `--accept-promotion` names what it approves
   ([`OQ-CO4`](#oq-co4)). `--plan` is the read-only form and refuses `--accept-promotion`;
   `--json` (or `--format json`) emits the document. `--keys a,b` selects keys; without it every
   promotable key is selected and listed.
4. **Declares the selected keys** at the destination, deep-merged, the promoted value winning
   and the overwrite reported, then **resets exactly those keys** from the capture overlay.
   Both files are restored from their pre-images if either write fails.

The plan **names keys and never prints values**: they are captured config, and a plan is the
output most likely to be pasted into a bug report. `yolo config diff` prints values; that
difference is deliberate.

**Classification is mechanical, and judgement stays with the user.** Whether
`model: "opus[1m]"` is a personal preference or this project's choice is not something a
heuristic decides; the user decides by what they select with `--keys`, and an agent reads the
same `--plan --json` document. There is no agent mode — a second code path for one decision
drifts from the first.

- **Sensitive keys are refused, never redacted** — the same ruling host capture took rather
  than inventing a redactable secret. The check is a name-token list matched as whole tokens,
  walked to the leaves, because the measured case is `mcp.tavily.environment.TAVILY_API_KEY`
  four levels below a top-level `mcp` with no signal of its own. `--force <keys>` overrides it
  per key, by name; there is no blanket form.
- **`--force` and `--accept-promotion` stay different flags.** `--force` overrides a refusal;
  `--accept-promotion` confirms an intended write. Collapsing them is how `--force` becomes the
  flag people paste without reading.

### Precedence — promotion moves a key down the stack

A captured key sits at `overlay`; a promoted key lands at `config-overlay:<pack>`, below it. The
risk class is small: the overlay cannot hold a `computed` or `managed` leaf (`narrowOverlay`
strips them), and the local pack folds last, so for `--to local` nothing outranks the promoted
key except the overlay it just left. The residue is `--to pack:<name>` for a pack ordered
before another overlay on the same key — BACKLOG's
[`OQ-CO`](../plans/BACKLOG.md#OQ-CO), silent last-one-wins between overlays, seen from the
promote side. (A pack pulled in through `needs` is appended after local; none declares a
`config-overlay` today.)

So the precedence check stays, two-sided (*wins now* and *wins after*), and a key that fails it
is **refused by name with the losing layer**, never promoted with a warning. Promote never
offers the pack's `managed` block as an escape ([`OQ-CO6`](#oq-co6)): `managed` is populated
only from the owning pack's own contribution, and for a user's own surface it stays a hand
edit.

### Where promote runs

Promote is **host-side only**. The capture sidecars live in the workspace, a host directory, so
the host reads a jail's captures with no channel; but the destinations are under the host's
`~/.config/yolo-jail/`, which no jail may write — a manifest is an input to composition, and an
agent that could rewrite one could grant its own pack a host file on the next boot. In a jail,
promote refuses and prints the host command (`refuseInJailPromote`, the mirror of
`refuseHostSideWrite`) ([`OQ-CO5`](#oq-co5)).

### Degenerate inputs

| Situation | Behavior |
| :--- | :--- |
| no captures for the surface, or every key redundant | no-op, exit 0, says so |
| `--keys` names a key not in the capture | error, exit 2, naming it |
| destination pack is fetched, or is a `pack.jsonc` | refused, naming what it found |
| destination pack has no `pack.json` | one is created with the pack's name |
| destination pack has problems every launch refuses it over | the write is refused, exit 1, naming each and `yolo pack lint`; `--plan` says so and exits 0 ([NS-D16](pack-system.md#why-its-this-way)) |
| the surface is keyless (`lines`/`raw`) | refused: the whole file is one captured value, so `yolo config reset` is the only exit |
| the surface is `rmw`, `computed` or `unrendered` | refused: no capture overlay |
| two workspaces captured one key differently | one promotion at a time; the second reports the overwrite |

> [!NOTE]
> **The move is recorded only by the destination file.** No per-pack provenance exists; a
> field on the contribution would be a hard refusal for every older yolo reading the pack, and a
> sidecar beside `pack.json` would be provenance nobody reads. The destination is the user's
> own, hand-readable, usually in git.
>
> **One window:** `last_render` is left alone by the reset (it is the first-migration selector),
> so a capture taken before the next boot can record the promoted key again — identical value,
> reported as redundant by the next `promote`.

---

## The read-in host layer

A surface declares `readsHost` (`manifest.Surface.ReadsHost`) to have its host file read into a
jail as the `host` layer. Two shipped surfaces do: `claude/settings` and `pi/settings` — the
settings surface of an agent that has one. The `/ctx` path is derived from the surface's own
path by one expression the launcher and the jail both evaluate (`packload.SurfaceHostFile`), and
the read **fails closed** against the launcher's report of what it delivered
(`packload.HostLayerReport`): a destination the launcher delivered and the jail cannot read
refuses the launch, a file the user does not have composes without the layer, and a launch that
delivered nothing reports `unsupported` and is not refused ([`OQ-CO10`](#oq-co10)). The layer
stays rather than being retired ([`OQ-CO11`](#oq-co11)).

- **It is a disclosure, not a gate.** A declared grant is honored unconditionally — the
  fetched-pack origin gate was deleted ([OQ-TP9](../design/trust-paths.md#decision-ledger)) — and
  it is review-worthy in the footprint, which puts it on the startup banner.
- **Coverage is a visible per-surface yes/no.** `mise/config` is a deliberate no.
- **The `reads-host` contribution kind stays** for the user's `host_files`, which carry
  arbitrary host files with no mirrored twin and need a declared path.

---

## What this does not license

- **No change to the jail's precedence stack.** Promotion works around it rather than
  reordering it.
- **No sealing by default.** Whether `apply --sealed` is the default is
  [`Q1a`](../design/environment-manager-user-stories.md#Q1a); this system adds one thing
  `--sealed` refuses.
- **No shared file.** The no-pack answer is `none` — yolo writes nothing and the file is the
  user's to hand-edit; a single machine that wants yolo's keys in it uses `own` plus `promote`.
- **No new secret model.** Sensitive keys are refused: no redaction, no vault, no taint
  propagation.
- **No `pack import` verb** — but `own` and `promote` compose into one for every undeclared key:
  adoption captures a hand-written file's keys into the host overlay, and promote lifts them
  into the local pack. What stays manual is re-authoring a file *as* a pack.
- **The `guest` notch has no ownership contract.** It keeps no sidecars or provenance.
- **No automatic promotion, ever** — not as a side effect of a launch, an apply or a boot.
- **No inference from a provenance record.** "yolo has applied here, so it owns this home" is
  inference (P1), escalates silently on a first apply, and makes an irreversible decision out of
  a `--dry-run` slip.

---

## Known gaps

Measured behavior no ruling has closed, stated as what the system does. Each needs a ruling or
a design of its own; none is filed as an `OQ-CO` question, because each outlives this system's
contract.

<a id="residue"></a>

- **A null tombstone an older yolo wrote lasts until the next capture.** Every capture — the
  boot render, `yolo config capture` and the teardown fold — now drops a tombstone at a keypath
  the file holds as `null` ([`CO-D20`](#CO-D20)). A sidecar an older yolo polluted is healed by
  the first of those, and `config diff` and `promote` run before it still misread the
  tombstone as a deletion.
- **A lost overlay sidecar is a silent, unnetted loss.** On an adopted home, deleting only
  `<agent>-<name>.overlay.json` and keeping `last_render` leaves the next render in steady
  state: the overlay reads as empty and the render collapses to the pure layers. No archive
  (not a first migration), no loss line, no prompt. Widening the archive trigger changes which
  renders adopt, which [`OQ-CO7`](#oq-co7) did not grant. The state is detectable — no render
  leaves a `last_render` without an overlay beside it — even where it cannot be netted.
- **A JSON integer past 2^53 is rounded under `own`.** `codec.JSON` decodes into `float64`, so
  `9007199254740993` becomes `9007199254740992` and the keys-and-values comparator sees them as
  equal. The `rmw` path lowers integers to `int64` (`tomlValue`); `Compose` has no such step.
  TOML surfaces are unaffected. A fix (`json.Number` through decode, merge, diff, enforce and
  promote) is a value-model decision. The adoption archive holds the original.
- **A literal `null` in a `readsHost` host file never reaches the jail**, and tombstones the
  matching `defaults` value on the way: `Inputs.LiteralNulls` is read off the current file only,
  and `Inputs.HostBytes` has no equivalent. Whether a host-file null is licensed, and at what
  precedence it folds, is a ruling.
- **The same null-valued key is labelled `host` by the `rmw` arm and `overlay` by the stateful
  arm**, and `provenanceparity_test.go` has no literal-null fixture declaring the divergence.
  Reporting only; no mechanism turns on the difference.
- **A keyless surface has no loss gate.** `EntryLosses` is entry-shaped, so a whole-file
  replacement of a keyless surface is never named, at a host apply or at a jail's first
  migration. Latent: no shipped surface is keyless.
- **Redundancy is judged per top-level key.** One changed leaf makes the whole key
  non-redundant, so a promotion deep-merges its unchanged sibling leaves too (live: opencode's
  `mcp`, non-redundant only because one `TAVILY_API_KEY` leaf expanded). The fix is narrowing
  the promoted value to its non-redundant leaves. A keyless surface gets no redundancy verdict in
  `config diff` at all.
- **`RenderHostPack`'s `rmw`-refusal branch drops `Archived`.** A surface refused after a
  successful archive would not announce the copy. Unreachable today — the only refusal inside
  `persistStatefulSurface` is the archive's own, raised before it is set.
- **The capture-time credential guard does not exist.** The jail's overlay is `0644` in
  `<workspace>/.yolo/prism/`, which yolo assumes is gitignored but never adds to a
  `.gitignore`, and it can hold an expanded credential (measured: an opencode `TAVILY_API_KEY`
  value). Promote's name-token list refuses at export, after the bytes are on disk. A per-surface
  `sensitive` marking is unbuilt too.
- **The jail→host promotion request channel is designed and unbuilt** ([`OQ-CO5`](#oq-co5)): a
  request file in `<workspace>/.yolo/`, in the shape the host→jail handoff established. The
  transport is free; the consent prompt waits on evidence of which keys people promote.
- **`--to workspace` is unbuildable as things stand**: the `workspace` layer has no config key
  and no producer. Whoever wires it owns the argument that a jail-writable layer must never
  reach a real home ([`OQ-CO8`](#oq-co8)).

---

## Open questions

- 💬 **[`OQ-CO15`](#OQ-CO15) — what does the jail's `rmw` arm do with a table its derive does
  not declare in full?** <a id="OQ-CO15"></a> `regenerateManagedTables` clears and rewrites every
  object-valued computed key, while the host's table probe claims only declared keys, so such a
  table would be cleared in a jail and merged at the host. No shipped pack does this.

  - **(a) Assert its leaves**, as the host does: the notches agree and the user's keys survive;
    a leaf yolo stopped asserting stays unless the derive tombstones it.
  - **(b) Skip it with a boot note**: nothing lost, but the contribution does not arrive.
  - **(c) Refuse the pack at load**: loud, and it forbids a shape the host handles.

  <!-- vantage: question id=OQ-CO15 leaning="(a) assert its leaves: it is what the host notch already does, so it makes one declaration mean one thing at both notches, which is the scope note's own requirement." -->

  _Leaning:_ **(a).** It is what the host notch already does, so it makes one declaration mean
  one thing at both notches, which is what the scope note above requires. Skipping it with a boot
  note hides a pack's contribution, and refusing the pack forbids a shape the host already handles. Not urgent: it changes nothing
  until a pack ships the shape.

  **Answer:**
  > _(empty — fill in when decided)_

- 💬 **[`OQ-CO16`](#OQ-CO16) — do the provider catalogs stay declared in full?**
  <a id="OQ-CO16"></a> The [provider catalogs](#in-full-tables) are declared in full because
  their key set tracks the selected providers, keeping the wholesale take they had before
  [`CO13`](#co13). The cost: a provider the user added by hand is dropped at a first-migration
  adoption and at a host apply, and unlike an MCP server a hand-added provider is plausible.

  - **(a) Keep them declared** (today): no stale entry; a user's own provider must be declared
    in yolo config.
  - **(b) Stop declaring them**: hand-added providers survive, but a provider yolo stopped
    selecting stays and points at an endpoint the jail no longer serves.
  - **(c) Decide per catalog**, e.g. by whether the agent has its own way to add one.

  <!-- vantage: question id=OQ-CO16 leaning="(a) keep them declared in full: a stale provider entry names an endpoint the jail no longer serves, which fails at use rather than at boot, and yolo config already has a place for a user's own provider." -->

  _Leaning:_ **(a).** A stale provider entry names an endpoint or credential the jail no longer
  serves, so it fails when the agent uses it rather than at boot, which is worse than a dropped
  entry the boot can report. And yolo config already has a place for a user's own provider.
  Weak: it rests on a guess about how many users hand-edit a catalog.

  **Answer:**
  > _(empty — fill in when decided)_

---

## Why it's this way

Rulings a future change would otherwise undo, with their original ids; code comments and
sibling docs cite them, and this table is where they resolve. `OQ-CO<n>` here is not BACKLOG's
bare [`OQ-CO`](../plans/BACKLOG.md#OQ-CO) (two packs writing one `config-overlay` key); the
prefixes collided and both stay. `CO13` has no `OQ-` prefix because it was a decision, not a
question.

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| <a id="oq-co1"></a>[`OQ-CO1`](#oq-co1) | **Two values, `none` and `own`; `none` is the default.** Reversed from the 2026-09-10 ruling of three values (with `assert`) on 2026-09-20 | The three-value ruling feared a forced escalation to `own`, and `yolo config promote` (one verb per key) removed that; `assert` is the one contract where P6 and P7 collide ([the retired `assert`](#the-retired-assert)). Kept as `none`/`own` words rather than a boolean, because they say which way ownership runs |
| <a id="oq-co2"></a>[`OQ-CO2`](#oq-co2) | **No migration prompt and no notice** for the unset state; each value explains itself at the act, and `--sealed` is the one place an unset key bites | First grounded on "the default equals today's behavior"; re-ruled on a new ground by [`OQ-CO14`](#oq-co14): every path that behaves differently says so where it acts. A prompt at upgrade asks before the user has seen any value behave |
| <a id="oq-co3"></a>[`OQ-CO3`](#oq-co3) | **Host-side capture under `own` only**, and it is a precondition of adoption. Reverses env-manager plan [`OQ-4`/`OQ-5`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) | Capture-then-regenerate is what makes the first owned render reproduce the file. The host store sits outside any workspace, so the privacy reason for refusing host capture does not arise |
| <a id="oq-co4"></a>[`OQ-CO4`](#oq-co4) | **`local` is the default destination; the guard is the plan, past which only `--accept-promotion` goes** — never `--yes` | An approval is a flag naming what is approved (as `--accept-config-changes`), never a generic yes or an env var a child inherits |
| <a id="oq-co5"></a>[`OQ-CO5`](#oq-co5) | **In a jail, promote refuses with the host command.** The jail→host request channel is designed and unbuilt | The transport is free; the consent prompt needs evidence of which keys people promote |
| <a id="oq-co6"></a>[`OQ-CO6`](#oq-co6) | **Promote never offers the `managed` block** to a key that would lose precedence | A one-keystroke path to outranking `computed` would be used for exactly the reason that layer exists |
| <a id="oq-co7"></a>[`OQ-CO7`](#oq-co7) | **One archive at adoption**, at both notches; not per-apply snapshots. Keyed by surface with no stamp, idempotent on its own existence, and a failed copy refuses the adoption (decided at build) | It covers a known loss the prompt cannot see; the later-regression case is what git on the pack is for. A stamped layout would let `yolo prune` reap the originals |
| <a id="oq-co8"></a>[`OQ-CO8`](#oq-co8) | **`--to workspace` is out of scope**, refused by name | The layer has no key and no producer; a jail-writable layer must never be given a path to a real home |
| <a id="oq-co9"></a>[`OQ-CO9`](#oq-co9) | **`own` refuses a keyless surface** (`hostStatefulRefusal`), leaving the file untouched; the refusal names leaving the surface to the jail or `none` ([`CO-D18`](#CO-D18)) | The class is empty today; growing a guard for it waits on a real example |
| <a id="oq-co10"></a>[`OQ-CO10`](#oq-co10) | **`readsHost` is declared on the surface and the read fails closed**; the `reads-host` kind stays for `host_files`. Supersedes env-manager plan [`OQ-3`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) | The old binding was a basename match guarding a fail-open read that shipped a wrong composition (a `my_pack` slug mismatch) and dropped the user's settings silently on `macos-user` |
| <a id="oq-co11"></a>[`OQ-CO11`](#oq-co11) | **The read-in `host` layer stays** — decided by [`OQ-CO10`](#oq-co10) | Ruling a mechanism's binding, failure direction and coverage decides that it exists |
| <a id="oq-co12"></a>[`OQ-CO12`](#oq-co12) | **Keys-and-values, not bytes**, for switching to `own`; a deleted `null` or `{}` key is a bug, not conformance | A derived file has no insertion order to preserve; teaching `own`'s encoder `rmw`'s byte layout would push formatting into the capture path. Relaxing a criterion moves the burden of each freed axis onto whatever else reports it — hence `Formatting` |
| <a id="co13"></a>[`CO13`](#co13) | **A derive declares an in-full table with the `ctx.in_full` sentinel**, read at both notches | Four discriminators (key set, tombstone presence, value shape, a lower layer) flip on configuration, not intent. A reserved key would oblige every reader of a computed layer to strip it; a third sentinel reuses a decoder that already has that job. A surface field is the wrong granularity: the claim is per key |
| <a id="oq-co14"></a>[`OQ-CO14`](#oq-co14) | **Ruled 2026-10-05:** a config saying `"assert"` is refused naming `none` and `own`; on a home `assert` wrote into with the key unset, the default is `none` with no prompt or notice and the file is left as rendered; `own` later takes it over as any first apply does; the provenance mark stays | In the maintainer's words, whichever value the user picks has to work from the state the home is in: at `none` *"our job is done"* |
| <a id="CO-D3"></a>[`CO-D3`](#CO-D3) | **`--revert` runs under `none`, set or unset, and is refused under `own`** | After the retirement no other value lets yolo's keys come out; under `own` the next apply would compose them back |
| <a id="CO-D7"></a>[`CO-D7`](#CO-D7) | **The provenance mark stays; nothing clears it at upgrade.** The launcher's render/baseline label keys on the mark, never on the posture | Clearing it would compose every key yolo wrote into a jail as the user's own `host` layer — the laundering the `retired:` label exists to stop |
| <a id="CO-D12"></a>[`CO-D12`](#CO-D12) | **`--revert` withdraws the values yolo wrote, never a whole key holding the user's** | The record is per top-level key; whole-key withdrawal took the user's `env` var with yolo's Bedrock switch |
| <a id="CO-D13"></a>[`CO-D13`](#CO-D13) | **A revert forgets the surface's capture-store files** | Left behind, the next owned apply replayed every removal as the user's deletion |
| <a id="CO-D14"></a>[`CO-D14`](#CO-D14) | **The selection record `assert` kept beside the provenance record is read, and removed by `--revert`** | Without it the first owned apply took the user's model pick for an unrecorded value and overwrote it every apply |
| <a id="CO-D15"></a>[`CO-D15`](#CO-D15) | **`own`'s stateful writer keeps the computed-leaf record**, except for a value the file held before that no record claimed | Otherwise a value `own` wrote reads as the user's and `--revert` cannot withdraw it |
| <a id="CO-D16"></a>[`CO-D16`](#CO-D16) | **The Bedrock-switch line names a fix that runs here**: remove the key by hand, or `--revert`, when no host apply renders | Telling a `none` user to apply names a command that refuses; "set `own` and apply" adopts the switch as the user's key ([`providers.md`](providers.md)) |
| <a id="CO-D18"></a>[`CO-D18`](#CO-D18) | **Remedies say set `"own"`, then promote** | Under `none` there is no host capture store, so promote has nothing until `own`'s adoption captures the keys |
| <a id="CO-D20"></a>[`CO-D20`](#CO-D20) | **The steady-state capture drops every null tombstone at a keypath the current surface file holds as `null`**, and any object that leaves empty, on the accumulated overlay (`dropHeldNullTombstones` in `ComposeStateful`). `config diff` and `promote` read the sidecar unchanged. An implementation decision, reversible | A decoded file holds no tombstones, so a null in it is one the user wrote, which the render already carries through the literal-null channel; a real deletion is absent from the file and keeps its tombstone. `last_render` cannot decide it, because the teardown capture and `yolo config capture` rewrite only the overlay and it lags by a session. The one engine call covers the boot, `config capture` and the teardown alike, and running on the accumulated overlay heals a sidecar an older yolo polluted |
| <a id="co-d-folded"></a>CO-D1, CO-D2, CO-D4–CO-D6, CO-D8–CO-D11, CO-D17, CO-D19 | <a id="CO-D1"></a><a id="CO-D2"></a><a id="CO-D4"></a><a id="CO-D5"></a><a id="CO-D6"></a><a id="CO-D8"></a><a id="CO-D9"></a><a id="CO-D10"></a><a id="CO-D11"></a><a id="CO-D17"></a><a id="CO-D19"></a>Implementation decisions building [`OQ-CO14`](#oq-co14), folded into the body: the one retirement message (D1), the unset default (D2), the rewritten remedies (D4), the census losing `HostAssertModes` and its coercion (D5), `--to host` dissolved (D6), `yolo pack update`'s host half under `own` only (D8), a home `assert` wrote into rebuilt in tests by the arm it ran rather than a frozen golden (D9), `yolo check`'s FAIL row for the retired value (D10), the selection record's writer making its directory (D11), the refused `--format json` document (D17), and four vacuous tests restored (D19) | Each is reversible; the body states the behavior it produced |
| <a id="unset-not-undeclared"></a>— | **The absent key is the *unset* state, never "undeclared"** | *Undeclared* is the input-closure tier's word, with the opposite remedy |

## Current values

Verified at `8e65f344`.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| `host_management` values | `none`, `own`; unset and unreadable read as `none` | `config.KnownHostManagements`, `hostManagementValue` |
| Promotion default destination | `local`, or user-scope `promotion_target` | `config.PromotionTarget` |
| Host capture store | `<home>/.local/share/yolo-jail/host-capture/`, directory `0700`, files `0600` | `render.Target.SidecarDir`, `SidecarDirMode`, `SidecarFileMode` |
| Jail capture store | `<workspace>/.yolo/prism/`, directory `0755`, files `0644` | the same |
| Host provenance record | `<home>/.local/share/yolo-jail/host-provenance/<agent>-<name>.provenance` | `render.Target.ProvenancePath` |
| Adoption archive | `archive/config/<agent>-<name>/<basename>` under the host state dir or `<workspace>/.yolo/` | `render.Target.ArchivePath` |
| Promote flags | `--keys`, `--to`, `--plan`, `--json`/`--format json`, `--force <keys>`, `--accept-promotion` | `parsePromoteArgs`; `yolo config --help` |
