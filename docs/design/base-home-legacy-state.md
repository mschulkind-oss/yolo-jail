---
title: "The base home is not a workspace — evicting legacy per-workspace state, and stopping its return"
date: 2026-09-20
status: in-review
tags: [base-home, jail-home, storage, migration, backend-parity, design]
summary: "The machine-wide base home is the union of every shipped pack's workspace-scope state dirs, pre-created as mountpoints and never cleaned, mounted read-only into every container jail — so it accumulates per-workspace runtime state (transcripts, session stores, command history) that every jail can read and that the launch seeds back into each workspace. The fix is a host-only, separate-marker, fail-closed migration that MOVES those runtime bytes to a host-only, non-prunable archive and never deletes them, plus a seed that keeps credential and config files only, and the invariant that a workspace-scope state dir in the base is a mountpoint whose steady state is empty."
---

# The base home is not a workspace — evicting legacy per-workspace state, and stopping its return

**Status:** DESIGN, 2026-09-20. Nothing built; every code claim verified against the tree
2026-09-20. [R1](#13-decision-ledger)–[R4](#13-decision-ledger) are pre-ruled; the rest is [§12](#12-open-questions).

> **In short.** `~/.local/share/yolo-jail/home` is the union of *every* shipped pack's
> workspace-scope state dir, created as mountpoints regardless of selection and mounted
> read-only into every podman jail at `/home/agent`. That union is the bug: one workspace's
> runtime state sits where every jail can read it, and the launch re-copies it into every
> workspace. Evict the runtime bytes to a host-only archive — move, never delete — and narrow
> the seed.

**Why it matters.** A jail running one agent can read an unselected agent's base session
store — a cross-workspace transcript leak — and `seedAgentDir` copies every top-level regular
file into the workspace overlay, re-infecting each new workspace ([§3](#3-the-two-harms)).

**The shape.** One host-only migration — separate marker, confirmation-gated, fail-closed,
archive-move — plus a seed allowlist, plus one invariant: a base state dir is a mountpoint
whose steady state is empty.

**Cost.** The base dirs stay (the OCI runtime cannot `mkdir` inside a `:ro` bind), so the fix
empties them. `seedAgentDir` stops being a blanket copy; a new, non-prunable archive bucket
holds the bytes.

**Start at [§5](#5-the-quarantine)** — the migration; [§6](#6-prevention-the-base-home-invariant)
keeps it from coming back.

**Needs your ruling:** [OQ-BH1](#OQ-BH1), [OQ-BH2](#OQ-BH2), [OQ-BH3](#OQ-BH3), [OQ-BH4](#OQ-BH4), [OQ-BH5](#OQ-BH5), [OQ-BH6](#OQ-BH6), [OQ-BH7](#OQ-BH7), [OQ-BH8](#OQ-BH8).

**Reads with:** [`base-home-legacy-state-plan.md`](base-home-legacy-state-plan.md) (the
implementation sketch — incomplete while the questions above are open), and
[`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md) (whose [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) must not be reused silently).

---

## 1. The base home is a union, and the union is the defect

The machine-wide base home is `paths.GlobalHome()` = `<state>/home`
(`internal/paths/paths.go:533`), mounted `:ro` into every podman jail at `/home/agent`
(`internal/cli/run/assemble_parts.go:107`). It is not a curated directory:
`EnsureGlobalStorage` builds it as the **union of every shipped pack's workspace-scope state
dirs**, whether or not the pack is selected (`internal/storage/ensure.go:52-77`, over
`packload.EmbeddedWritableDirs`, `internal/packload/embedded.go:142`). Six shipped packs
declare one:

| Pack | Base dir | Scope | Source |
| :--- | :--- | :--- | :--- |
| copilot | `.copilot` | workspace | `packs/copilot/pack.json:73` |
| claude | `.claude` | workspace | `packs/claude/pack.json:160` |
| codex | `.codex` | workspace | `packs/codex/pack.json:103` |
| pi | `.pi` | workspace | `packs/pi/pack.json:114` |
| agy | `.gemini` | workspace | `packs/agy/pack.json:102` |
| omp | `.oh-omp` | workspace | `packs/omp/pack.json:41` |

The base is **wider than those six**. The same function also creates the machine-scope shared
dirs (`packload.EmbeddedSharedDirs`, `internal/packload/embedded.go:145`), the single-file
mountpoints, the three `HomeFileRedirects` (`internal/paths/paths.go:600-608`), and a
hardcoded set — `.config/git`, `.pi/agent`, `.npm-global`, `.local`, `go`, `.yolo`, `.yolo/bin`,
`.config`, `.cache`, `.ssh` (`internal/storage/ensure.go:54-72`). [§8](#8-what-this-does-not-cover)
says which of those the migration deliberately leaves alone.

Two design decisions put the pack dirs there, and both are load-bearing:

1. **The union is deliberately not selection-gated.** A `host_files` entry must never be able
   to claim a path a pack added tomorrow needs, so the base reserves every shipped pack's
   directory (`docs/reference/jail-home.md`).
2. **The directories must exist as mountpoints.** The OCI runtime cannot `mkdir` inside a
   `:ro` bind, so the parent has to be pre-created or the launch fails with an opaque
   crun/conmon error. The pack dirs' base mountpoints are made by
   `EnsureGlobalStorage` (`internal/storage/ensure.go:71-75`, the rule in its own words at
   `:61-64`); `internal/cli/run/prepare.go:371-385` is the same rule applied to config
   `writable_home_dirs`, and `prepare.go:374-379` states it verbatim. ⚠ The two are easy to
   conflate — `prepare.go` creates pack-dir backing under `wsState`, never under `GlobalHome`.

The consequence is the defect. A workspace-scope dir is **shadowed by the per-workspace
overlay only when its pack is selected** (`internal/cli/run/assemble.go:376-379` binds
`wsState/<dir>` over `/home/agent/<dir>`). When the pack is not selected, the base copy is
plainly readable inside every jail. And when it *is* selected, the base copy is still read —
by `seedAgentDir`, at launch.

**The walk set is therefore not identical to the pack union.** The sweep in
[§5.1](#51-detection) covers (a) the six pack workspace-scope dirs, (b) the base mountpoints a
config `writable_home_dirs` entry creates (`internal/cli/run/prepare.go:382-386`), and (c)
state dirs left by a retired or third-party pack — the base never removes a directory, so a
dropped pack's fossil stays on every host that ever ran it (`EnsureGlobalStorage` only
`MkdirAll`s, `internal/storage/ensure.go:52-77`).

## 2. What is actually in there

The taxonomy is the design's central artifact. Four classes, applied per leaf:

| Class | What it is | Disposition |
| :--- | :--- | :--- |
| **CREDENTIAL** | A vendor-auth file, or anything under a machine-scope shared-credentials dir | Keep; seeding allowed |
| **CONFIG** | A file named by a pack `config` surface (the pack's own declared path) | Keep; yolo renders/owns it |
| **CONTENT** | A yolo-delivered read-only mount (briefing, skills, `files`) | Keep; never state |
| **RUNTIME** | Everything else the tool writes — session stores, transcripts, history, logs, caches, locks, package dirs | Archive-evict; never seed |

> [!WARNING]
> **THE SEVERITY BELOW IS N=1, AND THE MECHANISM ABOVE IS NOT.** Keep the two apart, because
> only one of them needs a host.
>
> [§1](#1-the-base-home-is-a-union-and-the-union-is-the-defect)'s mechanism — the base is a
> union, it is mounted `:ro` into every jail, and `seedAgentDir` copies every top-level regular
> file into each workspace — is read out of the code and holds on every host by construction.
> **How much is actually in there is a different claim, and it was one observation.**
>
> **MEASURED 2026-09-21 on a second host, and it disagrees completely:** five candidate entries,
> **0 bytes**, every one an empty directory, and `.copilot` contributing none. So the observed
> range across two hosts is 0 B to 34 MB, which is no distribution at all.
>
> ⚠ **RE-MEASURED 2026-09-22, and it is worse than "no distribution" — the 34 MB is no longer
> observable on the host it was measured on.** The base home here is **921 bytes**: 44 dirs, 20
> files, 3 symlinks, nineteen of the files empty, and the single non-zero one is a claude CONFIG
> surface the shipped classifier deliberately KEEPS. Zero runtime bytes. Run through the doc's own
> instrument rather than by hand — `noteLegacyBaseHome` printed nothing, meaning `Bytes()==0`,
> nothing unreadable, no refused root — and a before/after `find` showed the tree byte-identical.
>
> **Two hostile results worth keeping:**
>
> - **This is probably a RE-MEASURE, not a third host.** The "second host" reading is fingerprinted
>   in code by the fossils in this very base — `.foo`, `.filespack`, `yolo-it-newdir`, `.pi-lens`,
>   `.yolo-shims` are all present here. N stays 2.
> - **The outlier is gone, and the code says it cannot recur.** The launch log records the
>   maintainer's host launching after the refusal commit with **zero** base-home warnings, and the
>   disclosure states why: the base is bound `:ro` into every podman jail, so every write the host
>   CLI makes into `GlobalHome` is an `os.MkdirAll` of a DIRECTORY, never a file.
>
> **So the 34 MB is a HISTORICAL observation of a condition the code now prevents**, and every base
> home measurable today is at 0 actionable bytes. Anything below that was sized to VOLUME needs
> re-reading with that in mind: the migration is cheap because there is nothing to migrate, not
> because the mechanism was overstated.
>
> **What that does and does not undermine.** [R1](#13-decision-ledger)/[R2](#13-decision-ledger)
> — move, never delete — stand on N=1 without help: one host holding irreplaceable transcripts
> is a sufficient reason never to delete them, and no distribution would change it. What IS
> resting on the single observation is everything sized to VOLUME and FREQUENCY: the manifest,
> crash-consistent resume, the streaming `EXDEV` copy, the SQLite sibling rule. Those are
> justified by the worst case rather than the typical one, and a reader who takes 34 MB as
> typical will build for the wrong median.
>
> **[§11](#11-sequencing) step 1 is the instrument, and its stated purpose is too narrow.**
> "Verify the taxonomy against real hosts" is a correctness check; the more valuable thing it
> does is turn N=1 into N=many. It is now built and observe-only, so the sample costs nothing
> but running `yolo check` on each host.

**The immediate instance, measured on ONE host (the maintainer's, 2026-09-20) — see the
warning above before sizing anything to it:**
`GlobalHome/.copilot` held a 34 MB `session-store.db` (SQLite: sessions/turns/checkpoints/FTS),
`command-history-state.json`, and `session-state/*/events.jsonl` — transcripts pooled from many
workspaces while the home was shared and writable. All three are RUNTIME. The same base also
holds `.claude`, `.codex`, `.gemini`, `.pi`, `.oh-omp`; their contents were inventoried per
pack, and the mixed-directory case is real — `.gemini/antigravity-cli/` holds both
`mcp_config.json` (CONFIG) and `history.jsonl` (RUNTIME).

The machine-scope credential dirs — `.claude-shared-credentials`,
`.gemini-shared-credentials` (`packs/claude/pack.json:165-169`,
`packs/agy/pack.json:107-111`) — are **excluded** from any sweep. Losing one forces a
re-login yolo cannot perform.

> [!WARNING]
> **Readable in-jail is not the same as writable, and the two must not be conflated.** The
> host base **is** mounted `:ro` at `/home/agent` and is readable from inside a jail (that is
> H1). What is host-only is *mutation* and the *path resolution*: inside a jail,
> `paths.GlobalHome()` resolves to `<workspace>/.yolo/home/local/share/yolo-jail/home`, because
> the resolver's home is the overlay and `<ws>/.yolo/home/local` is bound at `$HOME/.local`
> (`internal/paths/paths.go:528-533`, `internal/cli/run/assemble_parts.go:109`). The 34 MB
> instance is the **host's** base home, which a host process mutates and an in-jail process
> cannot reach as the same tree — so the migration is host-only by construction
> ([§5.7](#57-the-trigger-and-where-it-runs)), and no in-jail test may assert on it.

## 3. The two harms

**H1 — exposure.** The base is mounted `:ro` but readable in every podman jail. A jail
running claude can read `GlobalHome/.copilot/session-store.db`; a jail running nothing still
mounts the whole base. The only thing that hides a dir is selecting its pack, and that is
per-jail, not per-machine. The leak is cross-workspace transcripts. On **Apple Container**
there is no whole-base bind at all (`internal/cli/run/assemble_parts.go:60`), so this specific
door is closed — but each machine-scope shared dir is mounted *read-write* from `GlobalHome`
there (`internal/cli/run/assemble_parts.go:90-92`), and `seedAgentDir` still reads `GlobalHome`
on every container backend (`internal/cli/run/prepare.go:409`). H1 is a podman-shaped leak, not
a backend-independent one.

**H2 — re-infection.** `seedAgentDir` copies **every top-level regular file** from
`GlobalHome/.<dir>` into `<workspace>/.yolo/home/<dir>` when missing
(`internal/cli/run/storagehelpers.go:42-68`, called from `internal/cli/run/prepare.go:409`).
Its comment says "auth-related files" and the docs repeat "auth tokens"
(`docs/reference/jail-home.md:399-403`); the body does no such filtering. So
`session-store.db` and `command-history-state.json` are copied into every workspace that
selects copilot. Eviction alone is not enough — without the seed fix, the next launch
re-creates the copy. H2 holds on Apple Container too, because the seed reads the same base.

## 4. Principles

**P1. Move, never delete.** The archive is the disposition, not a courtesy. A wrong
classification must cost the user one `mv` back, not their transcripts ([R1](#13-decision-ledger)).
The project already ships this shape: `internal/hostskills/archive.go:38-65` renames aside,
suffixes collisions, falls back to copy-then-remove across devices, and **returns the
destination so the caller can print it** — *"an archive the user cannot find is the same as a
deletion."* [§5.4](#54-atomicity) corrects the one part of that helper this migration cannot
reuse unchanged.

**P2. Fail closed.** No TTY, a lock not acquired, a transient move failure, an unclassifiable
leaf — do nothing to the bytes, leave the marker unstamped, retry later ([R3](#13-decision-ledger)).
Two precedents, one of which the doc used to state wrongly:

- **Precise:** `MigrateStorageLayout` returns *before* writing its marker **only when there
  are dangling mise symlinks and `canReclaim` is false** — the `return` is inside the
  `len(dangling) > 0` branch opened at `internal/storage/ensure.go:267`, whose guard is
  `canReclaim == nil || !canReclaim()` (`:268`). **Three** early returns precede the marker
  write at `:277` — `insideJail` (`:257-259`), marker already at version (`:261-265`), and that
  deferral (`:267-270`) — and only the third is fail-closed; the other two are "nothing to do".
  With no dangling links the marker is written unconditionally. That is why the base-home migration must not share that
  marker ([§5.7](#57-the-trigger-and-where-it-runs)).
- **General:** config approval refuses without a terminal and does not rewrite the snapshot
  (`docs/reference/config-safety.md:81-84`), and the cache-purge offer prints rather than
  implying yes on a non-TTY (`internal/cli/run/offer.go:238-240`).

**P3. Generic.** Every pack's workspace-scope state dir, every backend — not copilot
([R4](#13-decision-ledger)).

**P4. The base home holds no per-workspace runtime state.** This is the invariant the whole
design exists to restore; [§6](#6-prevention-the-base-home-invariant) enforces it.

**P5. Classification is conservative.** Unclassified means RUNTIME means archive. The cost of
archiving a file that was really a credential is a login; the cost of keeping a file that was
really a transcript is the leak. The archive makes the first recoverable and the second is not.

**P6. One writer — of everything this design touches.** The host CLI is the only *mutator* of
the swept set. It is **not** the only mutator of the base home, and the difference is worth
stating precisely because an earlier draft of this principle had it wrong.

> [!WARNING]
> **Both container backends bind the machine-scope shared dirs READ-WRITE out of `GlobalHome`,
> podman included.** Podman does it at `internal/cli/run/assemble.go:384-386` and Apple
> Container at `internal/cli/run/assemble_parts.go:90-92`, and the two emit the identical arg —
> `filepath.Join(paths.GlobalHome(), dir)+":/home/agent/"+dir`, **no `:ro`** — so on podman that
> read-write bind is nested inside the `:ro` base bind (`assemble_parts.go:107`). This principle
> previously said "podman excludes them by class", which is false: nothing excludes them at the
> mount layer on either backend.
>
> **The conclusion survives, for a different reason than the one it used to give.** What keeps
> the sweep safe is not that the base is unwritable — it is that the classifier is
> **structurally unable** to be handed a path under a declared machine-scope shared dir
> ([§5.2](#52-classification) step 1, over `packload.EmbeddedSharedDirs`,
> `internal/packload/embedded.go:145`). The exclusion is the classifier's, not the mount's.
> It also qualifies [§2](#2-what-is-actually-in-there)'s warning: *mutation* of the base is
> host-only **except** for these dirs, which a jail can write on every container backend.

## 5. The quarantine

### 5.1 Detection

On the host, walk each root under `paths.GlobalHome()`:

- every shipped pack's workspace-scope state dir — the union over packs
  (`packload.EmbeddedWritableDirs`, `internal/packload/embedded.go:142`), not the selected set;
- every base mountpoint a config `writable_home_dirs` entry creates
  (`internal/cli/run/prepare.go:382-386`) — these sit outside the pack union and would
  otherwise be invisible;
- every other top-level directory under `GlobalHome` that is not in the base's known non-pack
  set and not a machine-scope shared dir — the retired/unknown-pack case. That known set is a
  fixed exclusion list today; it must be derived from the same declarations
  [§5.2](#52-classification) uses, not hand-maintained ([OQ-BH4](#OQ-BH4)).

**The walk root itself is constrained.** A root that is a symlink or a regular file is
**refused**, not followed and not archived: following a `.claude` symlink would sweep the
user's real `~/.claude`, and archiving a file where a mountpoint belongs would break the next
podman launch. A root must be a real directory ([§10](#10-risks)). A missing root is a no-op.

An entry is a **candidate** when it classifies as RUNTIME ([§5.2](#52-classification)) or
cannot be classified at all. Symlinks are `Lstat`ed and never followed: a link into a
machine-scope credential dir is a CREDENTIAL; a stale link elsewhere is RUNTIME and the link
itself moves. **Detection never aborts and never moves anything.** An unreadable entry is
counted as a candidate and reported; detection is read-only, always-on, and bounded by the
roots above. Empty directories move whole. There is no size cap — the bytes are the point —
but there is no whole-file read ([§5.4](#54-atomicity)).

### 5.2 Classification

The rule, in order:

1. **Under a declared machine-scope shared dir** → CREDENTIAL, excluded outright. The set is
   `packload.EmbeddedSharedDirs` (`internal/packload/embedded.go:145`); the walk must never
   descend into one. These dirs are home-root *siblings* of the pack state dirs, so nothing
   under a state dir resolves here except a symlink or nested copy, and steps 2 and the
   `Lstat` rule decide those.
2. **A declared credential filename** → CREDENTIAL. The set is derived from the packs'
   `shared_credentials` hook declarations — the `from` paths such as `.claude/.credentials.json`
   and `.gemini/antigravity-cli/antigravity-oauth-token`
   (`packs/claude/pack.json:170-175`, `packs/agy/pack.json:112-117`) — plus a small core
   fallback (`.credentials.json`, `auth.json`, `oauth_creds.json`). `claude.json` is
   deliberately **not** here; it is CONFIG (step 3).
3. **A path named by a pack `config` surface** → CONFIG. Match home-relative: a surface path
   names either a leaf file or a directory surface (which owns its subtree), and the match is
   joined through `HomeFileRedirects` (`internal/paths/paths.go:600-608`) so the home-root
   surface `~/.claude.json` maps to `.claude/claude.json`. A home-root surface outside every
   walk root is reached only through its redirect target.
4. **A declared content destination** — the resolved destinations of the `skills`, `briefing`
   and `files` kinds, which is what `packload.ResolveDestinations` returns
   (`internal/packload/mergedest.go:240`) and what `prepareWsState` pre-creates mountpoints
   for (`internal/cli/run/prepare.go:389-396`) → CONTENT.
5. **Everything else** → RUNTIME.

Granularity is per leaf. A directory with no kept leaf beneath it (e.g. `.copilot/session-state/`)
moves **whole**, one rename. A mixed directory (`.gemini/antigravity-cli/`) is descended and
only its RUNTIME leaves move, leaving the CONFIG leaves in place. The SQLite sibling set
(`session-store.db`, `-wal`, `-shm`) is one unit ([§5.4](#54-atomicity)).

> [!WARNING]
> **The authority for steps 3–4 is a derive across two separate declaration systems, and
> step 1 is structural.** `packdecl` treats a state dir as one opaque subtree — `at` + `scope`,
> nothing inside it distinguished (`internal/packdecl/contributes.go:176-180`). Config
> surfaces are a *separate*, file-level kind (`internal/agentcfg/manifest/manifest.go:64,77`),
> and content destinations are a third (`internal/packload/mergedest.go:240`). No in-tree
> authority says "this file is a credential and that one is a transcript", so step 2 is
> **derived from the packs' own hook declarations** rather than a hand list — miss one and
> narrowing `seedAgentDir` forces a re-login on every workspace, the harm the machine tier
> exists to prevent. Two shipped cases made the derive concrete: `.claude/claude.json` is
> CONFIG reached only through the redirect, and `.claude/.credentials.json` is **not** a base
> leaf — `EnsureGlobalStorage` copies it into `.claude-shared-credentials` and removes the old
> file (`internal/storage/ensure.go:78-87`); the symlink exists in the *jail* home, created by
> the `shared_credentials` hook (`internal/entrypoint/packhooks.go:113-137`).
>
> **One CONFIG file still carries runtime content.** `.claude/claude.json` holds `projects` and
> `mcpServers` alongside the login keys, and step 3 keeps the whole file — so the H1 leak
> survives for those keys after the move. Closing it is not a file move; it is a key-level
> reduction through the allowlist `claudeJSONSeedKeys` already names (`oauthAccount`,
> `hasCompletedOnboarding`, `internal/storage/claudejson.go:10-13`, consumed by
> `SyncClaudeJSONSeed` at `:27`). The design names that as a companion cleanup and folds the
> authority question into [OQ-BH4](#OQ-BH4).

### 5.3 Disposition, archive layout, and manifest

The move lands in the **host-render archive subsystem**, as a new bucket — not a second
archive:

```text
<GlobalStorage>/archive/base-home/<state-dir>/<entry>
<GlobalStorage>/archive/base-home/manifest.json
```

`<GlobalStorage>` is `~/.local/share/yolo-jail` (`internal/paths/paths.go:414`). It is **not
"never mounted"** — `GlobalCache()` is bound at `/home/agent/.cache` and `GlobalMise()` at
`/mise` (`GlobalCache` at `internal/paths/paths.go:652`, `GlobalMise` at `:649`; `internal/cli/run/assemble_parts.go:120,173-175`)
— but `archive/` is under neither, so the archive root is outside every mounted subpath. That
narrower fact is what the layout rests on, and [§5.7](#57-the-trigger-and-where-it-runs)
asserts it.

**The generation directory must not be stamp-shaped.** `PruneHostArchiveBuckets` sweeps *every*
bucket under `archive/` (`internal/prune/prunecmd.go:548-549`), and `PruneHostArchive` deletes
every generation whose name `looksLikeArchiveStamp` parses, keeping the newest 3
(`internal/prune/hostarchive.go:83-113,122`; `hostArchiveKeep = 3`,
`internal/prune/prunecmd.go:268`).

> [!IMPORTANT]
> **It is the GENERATION directory that is at risk, not the bucket — this section's heading is
> right and an earlier draft's sentence under it was not.** `PruneHostArchiveBuckets` enumerates
> every top-level dir under `archive/` with **no name filter** and never deletes one
> (`internal/prune/hostarchive.go:50-54`); it recurses into each (`:57`), and the package's only
> `os.RemoveAll` is one level DOWN, on a bucket's stamp-named children (`:107`). So a
> stamp-named *bucket* would in fact survive — it would simply be scanned for stamp-shaped
> children — while `archive/base-home/<stamp>/` would go once three newer stamped siblings
> existed. **The design's conclusion is unchanged and the reason is now the right one:** this
> layout puts `<state-dir>` exactly at the generation level, so keying it by a stamp is what
> would arm the implicit delete that [R1](#13-decision-ledger)/[R2](#13-decision-ledger) forbid.

**This reasoning is already in the tree, which is corroboration rather than coincidence.**
`internal/cli/stores/inventory.go:257-262` carries the same argument for the `config` bucket —
*"the sweep below cannot parse it as a generation and leaves it — which is the point, not an
oversight"* — so the non-stamp key is an established mechanism here, not one this design
invents. **It also names a change site this design would otherwise miss:** that row's
user-facing `Detail` reads `"keep 3 generations (adoption archive exempt)"`
(`inventory.go:263`), so the moment a second exempt bucket exists, `yolo stores` tells the user
their transcripts are on a keep-3 rotation. The fix is **not** to add a second name to the
parenthesis — that is the enumerating form this corpus keeps having to correct — but to state
the property: *non-stamp buckets exempt*, which is true of `config`, true of this one, and true
of the next. The `config` bucket is the
precedent and the fix: it is keyed by a stable `<agent>-<name>` name, not a stamp, precisely so
prune's own rule *"does not delete what it cannot explain"* leaves it alone
(`internal/render/target.go:590,600`). The base-home bucket follows that shape: a stable
`<state-dir>` leaf, with a re-appearing identical file reconciled through the manifest rather
than a new stamp. Deletion is available only through an explicit opt-in verb that names the
bucket and says "transcripts" ([OQ-BH1](#OQ-BH1)).

**`hostskills.Archive` cannot be reused unmodified.** Its rename path is fine, but its
cross-device fallback calls `copyTree`, which **materializes every symlink** — `os.Stat`,
followed directory descent, `os.ReadFile` of the target's bytes
(`internal/hostskills/archive.go:66-77,96-137`), with the docstring saying so outright. That
directly violates [§5.1](#51-detection)'s "symlinks are `Lstat`ed and never followed" and could
dereference a link into `.claude-shared-credentials`, copying a secret into the archive, or
recurse into a machine-scope dir. Its collision-suffixing and cross-device fallback are also
**not tested directly** — **no test calls `Archive` at all** (there is no `archive_test.go`,
nothing injects `EXDEV`, and nothing asserts a `.2`-suffixed path); every test reaches it
indirectly, through `Deliver` (`deliver.go:311`) and through the compose/migrate path
(`compose.go:673` `skillsArchiveDetail`, `compose.go:884` `retireComposed`). This migration
therefore needs an `Lstat`-preserving, streaming, link-safe copy ([§5.4](#54-atomicity)), and
the archive's own location must be out of prune's stamp path ([OQ-BH1](#OQ-BH1)).

**The manifest is a versioned discovery artifact and the restore map.** `manifest.json` at the
bucket root records, per moved entry: `v` (schema version), `state_dir`, `entry`, `class`,
`source` (absolute), `archive` (absolute), `size_bytes`, `sha256` (files only; a whole-directory
move records `size_bytes` and a `sha256` over a deterministic listing of its sorted members),
`mtime_ns`, and `outcome` (`moved`, `skipped`). A single end-of-run write would lose a record
on a crash between moves, so the manifest is updated **per entry, atomically** (temp + fsync +
rename + directory fsync) **before the next entry starts** — one mechanism, not the two the doc
used to state. On resume it is reconciled against the archive: a moved entry with no row is
appended rather than re-moved.

### 5.4 Atomicity

Per entry, the order is **rename first**: `os.Rename(src, dest)` when the base and the archive
share a filesystem, then `fsync` the destination directory and, after `RemoveAll(src)`, the
source directory. The existing helper does neither (`internal/hostskills/archive.go:52-64`).

On `EXDEV`, copy to a temp name **in the destination directory** with a **streaming,
`Lstat`-preserving** copy — a symlink is recreated as a symlink, and a link that escapes the
source tree ([§5.2](#52-classification) step 1) is refused rather than materialized — then
`fsync` the file, rename into place, `fsync` the destination directory, and only then
`RemoveAll(src)`. The source is never unlinked before the copy is durable. There is **no
whole-file `ReadFile`**: the current fallback reads each file into memory
(`internal/hostskills/archive.go:133`), which OOMs on a large session store. There is no
pre-flight free-space promise (a cross-device copy cannot make one); a temp file is removed on
failure. `fsync` is a **blocking prerequisite** of this migration, not an open question.

**The SQLite sibling set moves as one unit.** `session-store.db`, `-wal` and `-shm` are
separate files, and moving them independently can leave a database without its WAL. There is no
live writer on the host (the base is `:ro` in every jail), so per-database consistency is not
required, but the three must stay together: move the containing directory whole, or order
`-wal`/`-shm` first and document the torn window.

### 5.5 Partial failure and rollback

A multi-entry move is not a filesystem transaction, and a cross-device copy cannot be
pre-flighted. The policy is therefore **idempotent resume**, not all-or-nothing:

- Each successful move is durable and recorded in the manifest **before the next entry
  starts** ([§5.3](#53-disposition-archive-layout-and-manifest)); the manifest write is atomic.
- A **transient** failure (a bad read, `ENOSPC`) ends the invocation, leaves the marker
  unstamped, and is retried next time.
- A **deterministic per-entry** failure — a dangling symlink, a root-owned `EACCES` leaf from
  prior container UID mapping (`internal/storage/ensure.go:91`), an unarchivable escape link —
  is recorded as `outcome: skipped`, disclosed loudly, and does **not** block the rest. A
  refusal to skip would mean one stale link stalls the whole eviction forever, which is the
  failure R3's *"retry later"* is supposed to recover from, not a state it can reach. The
  always-on detection of [§5.7](#57-the-trigger-and-where-it-runs) re-reports a skipped leaf
  on every subsequent launch, so skipping is not forgetting. This boundary is
  [OQ-BH8](#OQ-BH8).
- There is **no automatic rollback** ([R2](#13-decision-ledger)). Restore is a documented
  `mv` from the archive, which the manifest makes mechanical.
- **Crash consistency:** if a move completed but no manifest row exists, resume reconstructs
  the row from the on-disk archive layout and continues; if a copy succeeded but `RemoveAll`
  failed, the source is still present, and resume must **not** re-archive it as `.2` — the
  existing row maps source to destination, so resume treats it as done once the destination
  hashes equal or the row already names it.

### 5.6 Concurrency, liveness, and one writer

One writer: the host CLI, in its own apply step, under a dedicated non-blocking exclusive
`flock` (`GlobalStorage()/locks/base-home-migration.lock`, beside the machine-wide locks the
repo already uses). The lock is acquired **unconditionally at the top of the apply**,
independent of the dangling-mise branch — the seam the doc used to rely on is not what it
claimed: `canReclaim` is hardwired `func() bool { return false }` at both call sites
(`internal/cli/run/run.go:840`, `internal/cli/check/check.go:47`) and `MigrateStorageLayout`
consults it *only* inside `if len(dangling) > 0` (branch at `internal/storage/ensure.go:267`, the sole consult at `:268`). Both host
verbs that reach the apply must take the lock; `yolo check` does not go through
`ensureStorage()` and carries its own closure (`internal/cli/check/check.go:44-52`), which is
exactly the race.

The lock is **not held across the prompt** ([§5.7](#57-the-trigger-and-where-it-runs)):
detect without the lock, present the plan and confirm, then acquire, re-detect, apply, release.
Holding it across an unbounded human read would block a second `yolo` in `ensureStorage`.

**The re-detection can disagree with what was confirmed, and the apply is bounded by the
CONFIRMED set.** Dropping the lock across the prompt is what makes that possible, so the rule
belongs with the decision: an entry that appeared after the plan was shown is **not** moved,
because the user consented to a named list and a superset is a move they never saw. It is
reported and left for the next invocation, which will show it in its own plan. An entry that
has since vanished is dropped from the set, not an error. Only the intersection moves.
A live *jail* is not a veto, and the reason is stated rather than left implicit: the base is
`:ro`, so a jail cannot be a writer, and a jail that reads a file being renamed keeps an open
descriptor to the inode (POSIX), so a move cannot corrupt a reader.

> [!WARNING]
> **That argument covers jails and the other migration; it does not cover the SEED, which is a
> third reader of the base and is not under the lock.** `seedAgentDir` runs host-side in the
> launch pipeline (`internal/cli/run/prepare.go:409`), and `prepareWsState` takes no lock —
> VERIFIED: there is no lock call in `prepare.go`. So a second `yolo run`, already past
> `ensureStorage`, can be *copying out of* `GlobalHome/.copilot` while this apply moves it. It
> reads rather than writes, so the base cannot be corrupted; what tears is the **copy**, and a
> `session-store.db` seeded without the `-wal` that moved a moment earlier is exactly the
> outcome [§5.4](#54-atomicity)'s sibling rule exists to prevent — arriving by a route that
> rule does not cover.
>
> **What actually closes it is [§11](#11-sequencing) step 3**, and this is the second reason for
> it rather than a restatement of the first: once the seed is narrowed, its read set
> (CREDENTIAL/CONFIG) and the move set (RUNTIME) are **disjoint by construction**, so the race
> has no shared file to tear. ⚠ **The alternative [§5.6](#56-concurrency-liveness-and-one-writer)
> offers above — "the old blanket seed must be gated on migration not yet discharged" — does
> NOT close it**, because a gate keyed on *this* host's marker says nothing about a concurrent
> process mid-apply. If the seed fix cannot ship together, the seed must take the same lock.

**The seed runs *later*, not earlier.** `ensureStorage` is at `internal/cli/run/run.go:129`;
`prepareWsState`/`seedAgentDir` is at `run.go:1046` → `prepare.go:409`. So on any launch where
the apply defers (no TTY, transient failure), the **unfixed** `seedAgentDir` still copies base
RUNTIME into the workspace, re-infecting before the seed fix could take effect. The seed
allowlist ([§6](#6-prevention-the-base-home-invariant)) must therefore ship in the **same
change** as the move, or the old blanket seed must be gated on "migration not yet discharged".

### 5.7 The trigger and where it runs

The migration is host-only, matching `MigrateStorageLayout`'s `insideJail` short-circuit
(`internal/storage/ensure.go:257`). It is reached from the two host call sites that already
invoke the layout migration — `internal/cli/run/run.go:837-844` and
`internal/cli/check/check.go:44-52`. Both must share the lock above.

**Detection is always-on; the marker gates only the apply.** This resolves the contradiction
the doc used to state (a stamped host that never re-walks cannot also disclose recurrence on
every launch): detection is a read-only, bounded walk that runs wherever the host-only
storage path runs and
never depends on the marker, while the one-shot apply is gated by its own marker so a
completed move is not re-offered. Recurrence ([§6](#6-prevention-the-base-home-invariant)) is
therefore still seen and disclosed after the migration has run.

**The marker must not be `StorageLayoutVersion`.** That marker is written **unconditionally**
once past the dangling branch (`internal/storage/ensure.go:277`), and every existing host
already has it at `2` (`:22,260`), so:

- a base-home step carried on a bump to `3` would be **stamped before/without the apply** —
  `ensureStorage` and `yolo check` would both write `3` with nothing moved, and R3's retry
  guarantee would be dead;
- sharing the marker couples the base-home migration to the mise heal, which defers on
  `canReclaim=false` *before* the stamp (`:264-277`) — a host with a dangling symlink could
  never advance the marker for either;
- `yolo check` has no console and no TTY, so it can only ever be a detection/trigger site, not
  an apply site.

The base-home apply therefore carries a **separate marker**, written only after the apply
returns success. A **zero-candidate** apply discharges the debt and stamps (there is nothing to
move); later recurrence is caught by always-on detection, not by re-running the stamp. A
**downgrade** to a yolo without the migration is harmless: the old binary ignores the new
marker and never applies, and re-upgrade re-detects. [OQ-BH2](#OQ-BH2) holds the marker
mechanism and the apply location; the sound leaning is now the separate marker.

> [!NOTE]
> **What building step 1 measured (2026-09-21, `bc7685dd`).** Four facts the design could not
> have had before something walked a real base home.
>
> 1. **`writable_home_dirs` mountpoints cannot be a root where the trigger currently sits.**
>    [§5.1](#51-detection)'s second bullet needs the loaded config, and neither host call site
>    has one yet: `ensureStorage` is `internal/cli/run/run.go:129` and `loadAndValidateConfig`
>    is `:133`; check's `EnsureGlobalStorage` runs long before `config.LoadConfig`. A
>    **top-level** such dir is still found, by the third bullet — measured, this repo's base
>    yields `.pi-lens` that way — but a **nested** one stays invisible. Closing it means moving
>    the trigger after the config load at both sites, which is a decision, not an oversight.
> 2. **"Runs on every host command" was an overclaim, now corrected above.** There are exactly
>    two `EnsureGlobalStorage` callers; `prune`, `stores`, `config`, `pack` and `host apply`
>    reach none of this. Detection runs on a launch and on `yolo check`.
> 3. **The classifier is derived from SHIPPED packs while the walk sweeps unknown top-level
>    dirs**, so a config-declared dir or a non-shipped pack's state dir is classified with none
>    of its own declarations in hand — every leaf in it reads as RUNTIME, credentials included.
>    Harmless while step 1 only observes, and it is now reported by name when it bites (eight
>    such roots here). ⚠ **It is a hard precondition for the move**: the apply must not run over
>    a root whose declarations it never read.
> 4. **A core-provisioned directory can be NESTED inside a walk root**, which the top-level
>    exclusion list cannot see. `.pi/agent` is provisioned by `EnsureGlobalStorage` and created
>    EMPTY, so [§5.2](#52-classification)'s *"a directory with no kept leaf beneath it moves
>    whole"* proposed renaming a mountpoint the next launch cannot recreate inside the `:ro`
>    bind. Every fixture hid it, because each put a config surface under `.pi/agent`.

### 5.8 Disclosure

- **Before a move**, on every host launch, a one-line summary when candidates exist: the dirs,
  the measured size, and the command to quarantine. No transcript content is ever printed. The
  one-line summary names state dirs, not workspace names or absolute source paths; the manifest
  holds the absolute paths and is printed by the opt-in verb, not by the launch line.
- **After a move**, the destination bucket, the printed manifest path, and the exact `mv` that
  restores an entry.
- **The channel is non-suppressible.** Under [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) a
  disclosure is never gated by a report tier; this goes to stderr unconditionally and is not
  silenced by `YOLO_NO_BANNER`. If the apply runs in `ensureStorage` before the run console
  exists, the `warnf`→stderr closure is the whole channel and must say everything above.
- **A decline, a no-TTY deferral, and a transient failure are three different outcomes** and
  each prints its own line, with what was left in place and how to run it later. Silence after
  a decline is the one outcome the archive exists to prevent.
- **A failed apply is non-fatal.** A runtime byte is not a broken jail; the launch proceeds and
  the command's exit status is unchanged. Never turn this into a launch refusal.
- **The TTY probe is explicit:** `isatty(stdin) && isatty(stdout)` (or open `/dev/tty`), never
  a blocking read from a non-TTY stdin. The repo already has both seams (`IsTTYStdout`/`IsTTYStdin`,
  `internal/cli/run/runcmd.go:265-268`).
  `yolo run | tee` is stdout-false while stdin is still a TTY, and an agent launch with piped
  stdio must not hang. At the prompt, EOF/Ctrl-D, an empty answer, or anything that is not an
  explicit yes is a **decline** ([OQ-BH3](#OQ-BH3)).

## 6. Prevention: the base-home invariant

Eviction is a one-shot; recurrence is the design's other half.

**The invariant.** A workspace-scope state dir in the base home is a **mountpoint**. Its
correct steady-state content is the CREDENTIAL and CONFIG leaves a pack declares, and nothing
else. Any RUNTIME leaf there is a defect, not data. The top-level state-dir entries are walk
**roots** and are never themselves archived — the OCI runtime cannot `mkdir` inside the `:ro`
base, so removing one breaks the next podman launch ([§10](#10-risks)).

> [!NOTE]
> **A FOSSIL root is kept by that rule for a reason that does not apply to it, and the design
> leaves it with no end state.** The mountpoint argument protects a dir some pack still needs.
> A root from a retired or third-party pack ([§5.1](#51-detection) case (c)) is a mountpoint for
> nothing: no manifest declares it, no launch binds over it, and the base never removes a
> directory. So after a successful eviction it sits in the base permanently and empty — which
> is the one state this section's own invariant cannot describe, since it is neither a declared
> leaf nor a RUNTIME defect.
>
> **MEASURED 2026-09-21**, in a nested tree rather than a real host base, so treat the shape and
> not the count as the evidence: `.foo`, `.keeper`, `.dropped`, `.filespack` and `.pi-lens` were
> top-level dirs under a `GlobalHome` that **no shipped pack and no config declares** — nothing
> for any of the five in `packs/*/pack.json`, and no `writable_home_dirs` key in either config.
> All five were empty, so nothing would have moved.
>
> ⚠ **Their origin is not the one case (c) names, and that is the useful part.** Traced in-tree:
> `.filespack` is a fixture pack's dir (`integration/packs_test.go`), `.pi-lens` is
> `writable_home_dirs`' own worked example (`internal/cli/config_ref.txt`,
> `internal/config/writablehome_test.go`), `.dropped` belongs to
> `internal/cli/hostapplysurvey.go`, `.foo` is a generic test name, and `.keeper` has no in-tree
> reference at all. So the undeclared entries a sweep will actually meet on a DEVELOPER's host
> are **test and example residue**, which case (c) — *"a retired or third-party pack"* — does not
> describe. Same defect, different cause, and the same disposition: the base never removes a
> directory, so whatever once created one leaves it forever. Worth stating because a first run
> that reports five unrecognized roots on a maintainer's machine is the expected outcome, not a
> sign the classifier is broken.
>
> That is why it is a note rather than a ruling: emptying a fossil is the design's job and
> *removing* it is a disposition nobody has asked for, sitting between this invariant and
> [§8](#8-what-this-does-not-cover)'s "the base never removes a directory". Folded into
> [OQ-BH5](#OQ-BH5), which already owns how far prevention goes.

**The seed fix.** `seedAgentDir` (`internal/cli/run/storagehelpers.go:42-68`) narrows from
"every top-level regular file" to a **seed allowlist**: a file is seeded only if it is a
declared `config` surface path or a declared credential (the same predicate as
[§5.2](#52-classification) steps 1–3). RUNTIME files are never seeded. This makes the comment
and the docs true again, and it is what stops a future tool from re-infecting the workspaces
even if a runtime file appears in the base. The predicate must `Lstat`, not follow: the current
body uses `os.Stat` (`:58`; `:59` is the gate that consumes it) and would seed a symlink named like a credential whose target is
runtime. Two gaps remain and are named rather than hidden: nested config surfaces (`~/.pi/agent/*`,
`~/.gemini/antigravity-cli/*`, `~/.oh-omp/agent/models.yml`) are never seeded today because
`seedAgentDir` skips directories (`:52`), and the allowlist does not change that; and the seed
predicate's credential set is the same derive as [§5.2](#52-classification) step 2.

**What a launch does when the base still holds RUNTIME** — disclose, warn, or refuse — is
[OQ-BH5](#OQ-BH5). The design's leaning is disclose, never refuse: a runtime byte is not a
broken jail, and a refusal here would be a new way to fail a launch that used to work. The
classifier must be **structurally unable** to descend into a declared machine-scope shared dir,
not merely unlikely to ([§5.2](#52-classification) step 1).

## 7. Backend coverage

The migration runs host-side, before backend dispatch — `ensureStorage()` is at
`internal/cli/run/run.go:129` for every backend — so it walks the invoking host's
`paths.GlobalHome()` regardless of which backend is launching. The rows below differ in what
*else* is exposed, not in whether the walk runs.

| Backend | Where workspace state lives | What this migration does |
| :--- | :--- | :--- |
| **podman** | base `:ro` plus per-workspace overlay (selected packs only) | The target case. Detects and moves the host base copy; leaves the overlay alone |
| **Apple Container** | `wsState` bound whole at `/home/agent` (`internal/cli/run/assemble_parts.go:60`); **no whole-`GlobalHome` bind** | The host base still holds whatever earlier podman launches wrote; the walk runs and cleans **that**. It is not a no-op on a converted host. Shared dirs are bound read-write from `GlobalHome` (`:90-92`), and `seedAgentDir` still reads the base (`internal/cli/run/prepare.go:409`) |
| **macos-user** | `/Users/_yolojail` is the account home; after A′, workspace dirs are symlinks into the sidecar | The migration walks the **invoking admin's** `~/.local/share/yolo-jail/home`, which macos-user never mounts; it is a no-op when that base is empty and never touches `/Users/_yolojail`. Pre-A′ workspace state in the real account home is [OQ-HT2](../reference/macos-user-home-tiers.md#oq-ht2)/[OQ-BH7](#OQ-BH7), not this walk |

The macos-user case is where [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) must not be
silently reused. That ruling — *"No migration. Discard the old layout; wiping
`/Users/_yolojail` is a supported reset"* — was affordable **because its stated premise held:
nobody had used that backend for real work, so there were no transcripts to preserve**
([`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) states the premise it was ruled on).
The container base inverts every clause of
that premise: it is used, it holds real transcripts, and they are pooled across workspaces.
**The discard is scoped to the clean macos-user account and is not precedent for the container
base.** [OQ-BH7](#OQ-BH7) asks whether even that scope still holds after A′.

## 8. What this does not cover

- **Not a disk reclaimer.** It moves a bounded, one-time set of legacy bytes; it does not
  sweep caches, images, or the per-workspace overlays.
- **Not the machine-scope credential dirs.** `.claude-shared-credentials` and
  `.gemini-shared-credentials` are excluded by class, permanently.
- **Not the per-workspace overlays, including already-infected ones.** This is where the fix
  *stops* new copies; it does not clean up a `<workspace>/.yolo/home/<dir>` that already holds
  a seeded `session-store.db`. Those bytes are per-workspace and in the correct tier, but they
  are still a duplicate of base runtime, and the design names that as left in place rather
  than pretending the eviction reaches it.
- **Not the base's non-pack contents.** The hardcoded dirs, file mountpoints and
  `HomeFileRedirects` [§1](#1-the-base-home-is-a-union-and-the-union-is-the-defect) lists are
  out of scope; only pack workspace-scope state dirs (and the `writable_home_dirs` mountpoints)
  are swept.
- **Not `PruneShadowedHome`.** Its registry is `.cache/.npm/.npm-global/.local/go`; it
  **empties but preserves** the directories, because they anchor live jails' overlay mounts
  (`internal/prune/shadowed.go:14-28`). Wrong disposition and wrong set — but it does not
  delete the dirs, which the doc used to say it did.
- **Not a nested launch's `GlobalHome`.** `insideJail` short-circuits the apply, and its base
  is a different tree ([§2](#2-what-is-actually-in-there)).
- **Not the transcript-durability rule.** Claude transcripts are durable, non-regenerable user
  data and are never age-purged (`internal/prune/agentlogs.go:11-24`); this design moves them
  for the same reason, to an archive rather than a purger.

## 9. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| **A. Delete the legacy bytes outright** | **Rejected** — [R1](#13-decision-ledger)/[R2](#13-decision-ledger). Transcripts have no regeneration path |
| **B. Leave them, only warn** | **Rejected** — the exposure and the re-infection both survive. A warning is not a fix |
| **C. A new explicit command only (`yolo base-home archive`), no launch-path attempt** | **Runner-up** — cleanest TTY story, but nothing runs on a host that never invokes it, and R3's "retry later" implies an attempt. Feeds [OQ-BH2](#OQ-BH2) |
| **D. Per-workspace rescue copy, then delete the base** | **Rejected** — pooled transcripts have no single destination, and the copy would have to guess a workspace. This is the leaning [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) overrode for a backend where it was affordable |
| **E. Move the bytes into each workspace's own overlay** | **Rejected** — same no-single-destination problem, plus it would *seed* the exact runtime the design removes |
| **F. Shadow every unselected state dir, no eviction** | **Complement, not a substitute** — closes the read path structurally but leaves the bytes and the seed. Feeds [OQ-BH6](#OQ-BH6) |
| **G. Bump `StorageLayoutVersion` to 3 and share its marker** | **Rejected** — the marker (`internal/storage/ensure.go:277`) is written with no regard to whether any heal happened, and is therefore at 2 on every host except one permanently deferring on dangling mise symlinks, so it would stamp-without-apply every existing host and couple the base-home migration to the mise heal ([§5.7](#57-the-trigger-and-where-it-runs)). The **chosen skeleton is a separate marker**, with detection in the existing host-only path and apply where a TTY and the console exist |
| **H. A new per-artifact `packdecl` field** | **Open** — the durable classification authority, at the cost of a manifest schema change. Feeds [OQ-BH4](#OQ-BH4) |

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| A credential is misclassified as RUNTIME and moved | Machine-scope dirs excluded structurally; credential set derived from pack hook declarations; P5 fails closed; the archive retains the bytes and the manifest points at them |
| A reader holds the file open during the move | POSIX keeps the inode alive for an open descriptor; `os.Rename` does not corrupt a reader |
| Two host invocations race | The machine-wide `flock`, acquired unconditionally; a failed acquire defers. Both call sites take it ([§5.6](#56-concurrency-liveness-and-one-writer)) |
| The archive is pruned away | A non-stamp-shaped bucket, which prune's own rule leaves alone; deletion only through an explicit opt-in verb ([§5.3](#53-disposition-archive-layout-and-manifest), [OQ-BH1](#OQ-BH1)) |
| A symlink escaping the source tree leaks a secret into the archive | The copy is `Lstat`-preserving and refuses an escape link; `hostskills.Archive`'s materializing fallback is not reused ([§5.4](#54-atomicity)) |
| The migration never runs (headless host, no TTY) | Named, not hidden: the exposure persists and the launch discloses it. R3's ruling accepts this |
| A permanently unarchivable leaf stalls the eviction | Deterministic failures are skipped with disclosure and the rest proceeds; always-on detection re-reports them ([§5.5](#55-partial-failure-and-rollback), [OQ-BH8](#OQ-BH8)) |
| Crash between a move and its manifest row | The manifest is updated atomically per entry before the next move; resume reconstructs a missing row ([§5.4](#54-atomicity), [§5.5](#55-partial-failure-and-rollback)) |
| A top-level state dir is itself archived, breaking the next launch | Top-level entries are walk roots and never candidates; a bottoming test asserts they survive ([§6](#6-prevention-the-base-home-invariant)) |
| A new tool writes runtime into the base again | The invariant, the narrowed seed, and (if ruled) the shadow layer |

## 11. Sequencing

1. **Detection + classification + disclosure**, observe-only, no moves. **BUILT 2026-09-21**
   (`bc7685dd`). Two jobs, and the second is the one that was understated: verify the taxonomy
   against real hosts before anything moves, **and establish how much is actually out there**,
   because [§2](#2-what-is-actually-in-there)'s severity is a single observation and the second
   host measured 0 B. Run it on every host before sizing the move.
2. **The archive move + manifest + separate marker**, behind confirmation.
3. **Narrow `seedAgentDir`** to the seed allowlist — **in the same change as step 2**
   ([§5.6](#56-concurrency-liveness-and-one-writer)); shipping it later leaves the re-infection
   window open.
4. **(If ruled) the shadow layer** for unselected state dirs — [OQ-BH6](#OQ-BH6).
5. **macos-user coverage check** — confirm the walk is a no-op on a laid-out account —
   [OQ-BH7](#OQ-BH7).

**Done conditions.**

- After a successful apply, each base state dir holds only CREDENTIAL/CONFIG/CONTENT leaves;
  a second run is a no-op; the manifest lists every moved entry with an `outcome`; the separate
  marker is written.
- Detection is always-on and bounded, and runs without moving anything.
- The three failure outcomes are distinguishable in output: declined (left in place,
  unstamped), deferred/transient (unstamped), skipped-permanent (recorded and disclosed); a
  no-TTY run is declined-with-disclosure.
- A test fails if the apply call site is deleted — the repo rule that a test pinning the callee
  while the call site is unpinned is not a test.

## 12. Open Questions

1. 💬 **OQ-BH1: Where does the archive live, how long is it kept, and how is it found?**

   <!-- vantage: oq id=OQ-BH1 leaning="Host-only GlobalStorage/archive/base-home/ keyed by state dir, NOT by a render stamp, so prune's stamp rule leaves it alone (the config bucket's precedent); durable by default, with a separate explicit opt-in delete verb that names the bucket and says transcripts, and a manifest printed on request. Retention is the decision; the non-stamp location is not." -->

   The archive must sit outside the mounted tree and outside prune's stamp path.
   `GlobalStorage()/archive/base-home/` is the natural home, but `PruneHostArchiveBuckets`
   sweeps **every** bucket under `archive/` (`internal/prune/prunecmd.go:548-549`) and deletes
   stamp-shaped generations (`internal/prune/hostarchive.go:122`). The `config` bucket is
   deliberately unstamped — keyed `<agent>-<name>` — precisely so prune cannot touch it
   (`internal/render/target.go:590,600`), and these bytes are user transcripts, so the same
   treatment argues for non-stamp keying plus a separate opt-in delete. R2 permits an explicit
   delete path, and an unbounded archive is a disk leak.

   _Leaning:_ `GlobalStorage()/archive/base-home/<state-dir>/`, non-stamp so prune leaves it
   alone, durable by default, deleted only by an explicit opt-in command that names the bucket
   and says "transcripts"; a `manifest.json` at the bucket root; discovery through the printed
   path and a listing verb.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BH2: What is the trigger, and where does the apply step run?**

   <!-- vantage: oq id=OQ-BH2 leaning="A SEPARATE marker, written only after the apply returns success; detection always-on and read-only in the host-only path (insideJail short-circuit), apply where a TTY and the console exist. Sharing StorageLayoutVersion is the rejected alternative: it is written unconditionally and already at 2 on every host, so a bump stamps-without-apply." -->

   `MigrateStorageLayout` is the versioned, host-only skeleton
   (`internal/storage/ensure.go:251-277`), but it is `void`, takes three parameters
   (`insideJail`, `canReclaim`, `warnf`), and writes its marker unconditionally once past the
   dangling branch (`:277`). A shared bump to `StorageLayoutVersion` would stamp a host that
   moved nothing and couple this migration to the mise heal ([§5.7](#57-the-trigger-and-where-it-runs)).
   A separate marker keeps the two independent and keeps R3's retry guarantee real. Detection
   must be separable from apply either way.

   _Leaning:_ a separate marker written only by a successful apply; always-on read-only
   detection in the host-only path; apply in the run pipeline behind a plan/confirm seam, with
   both host call sites taking the lock.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BH3: Automatic move, or a y/N confirmation?**

   <!-- vantage: oq id=OQ-BH3 leaning="Prompt only when isatty(stdin) AND isatty(stdout) (or /dev/tty); fail closed otherwise. EOF, Ctrl-D, empty and anything but an explicit yes is a decline. A non-interactive opt-in flag exists; nothing moves on a host that never answers." -->

   R1/R2 make the move reversible, which argues for automatic; R3's "no TTY => do nothing" and
   the user's earlier "confirmation at upgrade" argue for a prompt. The two can be combined:
   automatic for leaves classified RUNTIME with certainty, a prompt for anything uncertain — at
   the cost of a second predicate to reason about. The prompt must never issue a blocking read
   from a non-TTY stdin; `yolo run | tee` is stdout-false while stdin is still a TTY
   ([§5.8](#58-disclosure)).

   _Leaning:_ prompt when both stdin and stdout are TTYs, fail closed otherwise, plus a
   non-interactive opt-in flag; nothing moves on a host that never answers.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-BH4: Who owns the classification — a core list, a new `packdecl` field, or a derive?**

   <!-- vantage: oq id=OQ-BH4 leaning="Derive from declared config-surface paths (joined through HomeFileRedirects), declared content destinations (packload.ResolveDestinations), and the credential set derived from packs' shared_credentials hook declarations for v1; unclassified is RUNTIME and archives. A per-artifact packdecl field is the durable fix if the derive proves brittle. The claude.json projects/mcpServers keys need a companion key-level reduction regardless." -->

   `packdecl` state is opaque (`internal/packdecl/contributes.go:176-180`), so today there is no
   in-tree authority that says "this file is a credential and that one is a transcript." The
   derive works from declarations that already exist — config surfaces, resolved content
   destinations, and hook-declared credential paths. A new field is a manifest-schema change
   that every pack must learn. The vendor's own XDG state list — copilot's, naming
   `session-store.db`, `session-state`, `command-history-state` — is the model for a
   vendor-declared split.

   _Leaning:_ derive for v1; unclassified archives; the credential set is derived, never hand-
   maintained; revisit with a packdecl field only if the derive misclassifies in practice.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-BH5: How far does prevention go, and what does a launch do while runtime remains?**

   <!-- vantage: oq id=OQ-BH5 leaning="Narrow seedAgentDir to the seed allowlist (Lstat, never follow) in the same change as the move, and have a launch disclose (one line) rather than warn or refuse when the base still holds runtime. Refusal is reserved for a missing machine-scope credential, which is a real breakage." -->

   Narrowing the seed is settled by [§6](#6-prevention-the-base-home-invariant); the open half is
   the launch's posture while runtime bytes remain — disclose, warn, or refuse — and whether
   prevention should also cover the non-seed writers (the claude.json reverse sync,
   `internal/storage/claudejson.go:48-55`, writes login keys only, so it is out of scope).

   _Leaning:_ seed allowlist shipped with the move; disclose on launch; never refuse for a
   runtime byte.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-BH6: Do unselected state dirs get a shadow layer, or is eviction enough?**

   <!-- vantage: oq id=OQ-BH6 leaning="Add the shadow layer as defense in depth: bind an empty per-workspace directory over every UNSELECTED pack's state dir, so the read path closes even before eviction and stays closed if the migration is declined. It is separable from eviction and must never shadow a selected pack's real overlay." -->

   Eviction is one-shot and can be declined; the structural leak is that an unselected dir is
   readable at all. A per-dir empty bind (or tmpfs) closes it without touching the base bytes.
   The cost is one extra mount per shipped-but-unselected pack, and the correctness burden is
   that the shadow must never land over a selected pack's overlay.

   _Leaning:_ yes, as a complement — eviction removes the bytes, shadowing removes the read.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-BH7: Does the macos-user home-tiers discard ruling still hold, and does macos-user need anything?**

   <!-- vantage: oq id=OQ-BH7 leaning="The macos-user home-tiers discard ruling stays scoped to the clean macos-user account and does not generalize to the container base; the migration walks the invoking admin's GlobalHome, which macos-user never mounts, so it is a no-op on a laid-out account. Pre-A' real dirs in the account home remain the occupied-layout refusal, never a move." -->

   The ruling in question is [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2).
   A′ made the account home's workspace dirs symlinks into the per-workspace sidecar, so the
   workspace tier is already where it belongs and the walk has nothing to move. The live
   question is whether [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2)'s discard should be
   re-opened for a machine that *has* used the backend for real work since A′ shipped, and
   whether the migration should ever move a real dir there.

   _Leaning:_ [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) holds where it was ruled (a
   clean account), does not extend to the
   container base, and macos-user needs detection only.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-BH8: Partial failure — resume, skip, or refuse to start until every move can succeed?**

   <!-- vantage: oq id=OQ-BH8 leaning="Idempotent resume: each durable move is recorded atomically before the next; a transient failure ends the invocation with the marker unstamped and is retried; a deterministic per-entry failure (dangling link, EACCES leaf) is recorded as skipped, disclosed loudly, does not block the rest, and is re-reported by always-on detection. All-or-nothing is impossible across devices and would let one stale link stall the eviction forever." -->

   No filesystem transaction spans the entries, and a cross-device copy cannot be pre-flighted.
   Resume is honest about what happened and keeps making progress; an all-or-nothing gate would
   mean a single unreadable file blocks the whole eviction forever — the exact state R3's
   "retry later" cannot recover from. The open half is where the skip/restart boundary sits and
   whether a skipped leaf counts as "discharged."

   _Leaning:_ resume, with deterministic per-entry failures skipped and disclosed, transient
   failures deferred, and no automatic rollback.

   **Answer:**
   > _(empty — fill in when decided)_

## 13. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R1 | Disposition is **MOVE** the legacy runtime artifacts to an **archive**, never delete. | 2026-09-20 | [§5.3](#53-disposition-archive-layout-and-manifest) | — |
| R2 | **Move over delete**; any delete path is explicit opt-in, never the default. | 2026-09-20 | [§5.3](#53-disposition-archive-layout-and-manifest), [§5.5](#55-partial-failure-and-rollback) | — |
| R3 | **Fail closed**: no TTY / cannot confirm safety ⇒ do nothing, leave the migration marker unstamped, retry later. | 2026-09-20 | [§5.6](#56-concurrency-liveness-and-one-writer), [§5.7](#57-the-trigger-and-where-it-runs), [§5.8](#58-disclosure) | — |
| R4 | **Generic**: cover all pack state dirs and all backends (podman, `container`, macos-user), not just copilot. | 2026-09-20 | [§4 P3](#4-principles), [§7](#7-backend-coverage) | — |

> [!NOTE]
> **[R1](#13-decision-ledger)–[R4](#13-decision-ledger) were pre-ruled by the user and are not
> re-opened here.** They are recorded with their own ids rather than `OQ-` ids because they
> were never open questions in this document. The eight live questions in
> [§12](#12-open-questions) are the decisions that remain.
