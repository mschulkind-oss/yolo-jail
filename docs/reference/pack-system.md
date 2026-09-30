---
status: current
verified: 2026-09-24
verified_commit: f491d192
covers:
  - internal/packdecl/
  - internal/packload/
  - internal/packstage/
  - internal/packoverlay/
  - internal/packsrc/
  - internal/entrypoint/packsurfaces.go
  - internal/entrypoint/packhooks.go
  - internal/entrypoint/launchercollision.go
  - internal/cli/run/packs.go
  - internal/cli/run/packtree.go
  - internal/cli/run/flock.go
  - internal/treesync/
  - internal/cli/run/packhostgrants.go
  - internal/cli/run/packloopholes.go
  - internal/cli/pack.go
  - internal/cli/applyhostbriefings.go
  - internal/entrypoint/hostbriefing.go
  - internal/entrypoint/hostoverlayprune.go
  - internal/cli/applyhostprune.go
  - internal/cli/applyhostoverlaykeys.go
  - internal/entrypoint/hostrevert.go
  - internal/agentcfg/
  - internal/loopholedecl/settings.go
  - internal/loopholedecl/capabilities.go
  - packs/
tags: [packs, config, kinds, manifest, prism, trust, disclosure]
---

# The pack system — how a jail gets everything in it

**Status:** CURRENT as of 2026-09-24. Verified in full against `7ad8358c` (2026-09-23); every
commit since that touches a path this doc covers was re-checked against `f491d192`. The
[fetch section](#fetch-refresh-lock) was rewritten on 2026-09-25 for the launch-time fetch
([`OQ-PF1`](#oq-pf1)), in the same change that builds it, so no commit has verified it yet. The
[concurrent-launch section](#concurrent-launches-of-one-workspace) was written on 2026-09-26 in
the change that fixes the staging race it describes, and rewritten the same day with
[`OQ-PK2`](#oq-pk2), in the change that builds one pack tree per launch; neither is verified by a
later commit. MEASURED in CI at `7ad8358c`
(run 35820702335): a pack's `briefing/` prose reaches a real container and its root `AGENTS.md`
does not (`TestPackDeliversSkillAndBriefing`). UNMEASURED: no launched jail has been observed
routing one pack's `briefing/` files to *different* agents. The container audience test routes a
file outside `briefing/`, and `yolo pack lint`'s [delivery listing](#briefing-lint-listing) is
where that routing is visible today. The [`config-list` section](#adding-entries-to-an-array-config-list)
was re-checked against `5a44129d` (2026-09-25) when its design folded in. MEASURED in CI at
`85401c4e` (run 36093253387, on rootless podman — `rootless: True` — in both `integration` jobs,
`ubuntu-latest` and `ubuntu-24.04-arm`): `TestConfigListSurvivesInJailEditsAndPackDrop` passed. It
runs three launches of one workspace: pi with a `file://` pack's list contribution, an in-jail `jq`
append standing in for `pi install`, then the contributing pack dropped. UNMEASURED: no real
`pi install` has been observed against a list path. The
[dropped-pack retirement section](#retiring-a-dropped-packs-host-output) and its four
*pack drop* rulings were folded in on 2026-09-26 and verified against `ba80c719`. UNMEASURED: no
real `yolo host apply` over a dropped pack is recorded; the behavior is pinned by unit tests that
drive the apply into throwaway homes (`applyhostprune_test.go`, `applyhostoverlaykeys_test.go`,
`provenanceretire_test.go`). The *open rulings* and *pack batch* rows of the
[appendix](#why-its-this-way), the [lint questions](#pack-lint-questions), the
[S4 warning](#skills-fanout-s4) and the [host-side dependency rule](#host-side-staging-then-jail-side-render)
were folded in on 2026-09-26 and verified against `ca86d945`. MEASURED for the *pack batch*
rows: each defect they record was found by running the host lifecycle (apply, re-apply, drop)
with a real binary in a temporary home, on 2026-08-04. The [`autonomy` section](#autonomy) and
the posture-list lines of the [`config-list` section](#adding-entries-to-an-array-config-list)
were written on 2026-09-27 in the change that builds posture lists, and no later commit has
verified them. MEASURED by unit tests that run the real verbs in temporary homes (`yolo host
apply --assert`, `yolo config render` and `ls` at both notches, the jail boot loop).
UNMEASURED: no launched jail and no real host has run a posture list.

A **pack** is a directory of jail configuration — skills, briefing prose, composed config
files, environment variables, and optionally a tool to install — that yolo delivers into
every jail it launches. This is the whole of how a jail is populated: with no packs
configured a jail has nothing but the built-in shell and skills, and no coding agent.

**Agents are packs.** Core has no notion of "an agent": no agent registry, no `agents`
config key, no `YOLO_AGENTS`. The packs that install an agent CLI are packs like any
other, selected by bare name in the one `packs` list. Most of what yolo ships installs no
CLI at all — a loophole, a provider, a set of blocked-tool refusals, an in-jail service.

This is the reference for **authoring, debugging, or changing a pack**: what a pack
declares, how the declaration is validated, how packs are selected and fetched, and how
they are rendered into a jail.

| Component | Lives in |
| :--- | :--- |
| Manifest schema — the closed kind registry and every field | `internal/packdecl` (`Manifest`, `Contribution`, `Kind`, `footprints`) |
| Manifest loading, footprints, collisions, host grants | `internal/packload` (`Pack`, `FootprintOf`, `Collisions`, `Embedded`) |
| Staging a pack's tree into the mounted root | `internal/packstage` (`Spec`, the three content rules) |
| Fetch, the content-addressed store, the lockfile | `internal/packsrc` (`Store`, `LockEntry`) |
| Selection, pre-flights, disclosure at launch | `internal/cli/run` (`stagePacks`, `notePackHostAccess`, `startLoopholesDisclosed`) |
| Jail-side render — one loop, no switch on a tool name | `internal/entrypoint` (`ConfigurePackSurfaces`, `packhooks.go`) |
| Config surfaces, the layer fold, the four modes | `internal/agentcfg` (`manifest.Surface`, `Compose`, `ManifestWith`) |
| `config-overlay` fold and the `profile` gate | `internal/packoverlay` |
| `config-list` entries: the fold, per-entry capture, the `rmw` insert record | `internal/agentcfg` (`listcontrib.go`, `ListCaptureRefusal`), `internal/entrypoint` (`applyRMWLayers`) |
| `models` contributions: the add-then-only pass, the `models_only` mark, the check notes | `internal/packdecl` (`models.go`), `internal/packload` (`modellists.go`, called by `ComposeProviders`) |
| The managed floor — the compose pipeline's last step | `internal/agentcfg` (`enforceManaged`, `enforceManagedTOML`) |
| The Lua sandbox: `yolo.derive` / `yolo.env` | `internal/agentcfg/luahook` (`GopherLuaVM`, `DeriveCtx`) |
| Which contribution governs each prose and skills source | `internal/packload` (`GovernedSources`) |
| The local pack's legacy `AGENTS.md` move | `internal/entrypoint` (`MoveLegacyLocalPackBriefing`), run by `yolo host apply` |
| The packs yolo ships | `packs/` (each a directory; `packs/embed.go` is the embed) |
| CLI surface | `internal/cli/pack.go` (`packMain`) |

**Reads with:** [`providers.md`](providers.md) (the `provider` and `profile` kinds, the
`profile:` modifier, and everything a selection does — the authority for all of it),
[`../guides/loopholes.md`](../../userguide/guides/loopholes.md) (what a loophole is, and its settings
block), [`wire-bridge.md`](wire-bridge.md) (`needs`, and the `service`
kind), [`protocol-resolution.md`](protocol-resolution.md) (the `adapter` kind and `protocols`),
[`agent-briefings.md`](agent-briefings.md) (what a composed briefing contains around the pack
prose), [`../design/trust-paths.md`](../design/trust-paths.md) (the 25 trust paths, and why
there is no approval gate), [`../design/program-delivery.md`](../design/program-delivery.md)
(the launcher, its PATH position, and evergreen updates).

For the `packs` config key, its precedence and its entry schema, run `yolo config-ref`. For
the manifest field by field, read `internal/packdecl` — the doc comments are the reference.

---

## Principles

Everything below follows from three rules. Read these first; the rest is their mechanism.
Sibling docs and code comments cite them by number.

1. **Every file on disk has exactly one writer.** A pack either OWNS a file outright, or it
   does not write that file — it contributes typed INPUTS to a neutral core-owned assembler
   that writes it. Two packs claiming one owned path is an error, caught before anything
   runs. Two packs feeding one assembled file is the feature. There is no third case.

2. **Core knows the DOMAIN, not the TOOL.** Core names domain nouns from a closed,
   tool-independent set — `program`, `skills`, `briefing`, `config`, `state`, and so on. It
   never names `claude` or `copilot`. A pack maps its tool onto those nouns. The boot path
   renders every pack in one loop with no switch on any tool name; the *selection of which
   packs stage* is the only filter.

3. **The manifest is static data.** Every claim a pack makes is readable without executing
   anything — that is what keeps linting, disclosure, and content hashing honest. Lua has
   exactly one job in a pack (the [derive slot](#the-derive-slot)): it computes config
   *values*, and never declares *effects*.

Principle 2 has a boundary, and it is worth knowing where: the **assembly** layer achieved
it, and the **imperative** layer did not, because only one tool ever needed a side effect.
Those side effects are the [`hook`](#hook) kind, and its set is closed — a pack requests a
named behavior core implements, and cannot ship the behavior itself. New behavior means a
new named hook in core.

## Invariants

- **Reading a manifest is inert; SELECTING one is not.** Every claim is static data —
  readable, diffable, and printable without running any of it, which is what makes
  `yolo pack footprint`, `pack lint` and the startup disclosure banner possible at all. What
  that does **not** extend to is honoring the claim: `program` installs a tool in the jail,
  and `loophole` starts a process **on your machine**. The cost of *looking* is zero and the
  cost of *choosing* is exactly what the claim says it is — which is why a claim must name
  its target precisely (the raw argv, the exact path) rather than summarize, and why
  **selection, not a prompt, is where the decision lives**.

- **The credential boundary is drawn at SELECTION, and no pack reads the host
  UNDISCLOSED.** Origin does not decide host access: a fetched pack's claims are honored
  exactly like an embedded pack's. What holds the line is that naming a pack in `packs`
  requires writing the user config as the host user — user scope only, inexpressible at
  workspace scope by construction, so **an agent cannot add a pack** — plus the startup
  banner, which says what each loaded pack actually reaches this launch. See
  [the credential boundary](#the-credential-boundary-disclosure-not-consent).

- **The claim enumeration must be TOTAL.** A crossing with no claim is a crossing that
  appears in **no report at all**, because the footprint and the launch banner are the only
  reports there are. Every declaration that crosses the boundary emits its own
  separately-disclosed claim.

- **Packs stay user scope.** A workspace config naming one is a hard error.

- **The source set for `derive` stays closed and core-owned.** A pack *projects* from
  core's tables into its tool's dialect; it never invents a source.

- **`derive` is deterministic** — required of every script, and not fully enforced by the
  sandbox; see [the derive slot](#derive-determinism).

- **The MOUNT is the filter.** The entrypoint renders every pack it finds under the mounted
  pack root, so each launch stages only the selected packs, into a tree of its own, and a pack
  dropped from config is simply absent from the next launch's tree.

- **A pack tree is never written once it is staged** ([`OQ-PK2`](#oq-pk2), built). Each launch
  stages a NEW tree; a running jail keeps the one it booted with until it stops, and an attach
  reads that tree and writes nothing into it. A running jail's bind mount captured its tree's
  inode, and nothing replaces or edits it. The skills staging a podman jail binds is still
  refreshed on attach, by SYNC ([`internal/treesync`](../../internal/treesync/treesync.go)), so an
  unchanged file or directory keeps its inode. The generated-script dirs document the same rule
  independently.

- **Two launches of one workspace never write the shared staging at once.** Pack staging needs
  no lock, since the tree is per launch. The per-workspace launch lock covers what the launches
  still share: the attach-or-create decision, the skills and briefing staging, and on macos-user
  the content trees its sandbox copies
  ([concurrent launches](#concurrent-launches-of-one-workspace)).

> [!WARNING]
> **Name reservation covers only the SELECTED packs** — the maintainer's
> [`OQ-BH14`](../design/base-home-legacy-state.md#OQ-BH14) ruling, which reversed what this
> warning used to say: an unselected pack is treated as if it does not exist. A
> `writable_home_dirs` entry may not claim a selected pack's writable or shared dir, and the
> refusal names the pack; a `host_files` destination under a selected pack's writable or
> shared dir needs no staging, and under any other pack's dir it is an ordinary path. A *configured*
> pack's dirs are reserved too, which the old shipped-set lists never did. The accepted
> cost: one user-scope entry can pass in one workspace and be refused in another. Two lists
> still span every pack yolo SHIPS: the machine store's shared dirs, which
> `EnsureGlobalStorage` creates before the config is loaded, and `host_files`' surface-path
> reservation, which the ruling did not reach
> ([`jail-home.md`](jail-home.md#reserving-a-name-is-not-creating-a-directory); open as
> [`OQ-BH15`](../design/base-home-legacy-state.md#OQ-BH15)).

> [!WARNING]
> **`packload.Embedded()` leases one immutable tree per build, shared by every process of
> that build.** The returned `Pack.Root` values are handles into a tree on disk, and nothing
> can know when the last read happens, so no process may delete a tree another might be
> reading. The tree is named by a content hash of the embedded FS (never a version stamp:
> two unstamped dev builds with different packs must not share one) and lives at
> `~/.local/share/yolo-jail/embedded-packs/<hash>`. The first reader writes it under a
> `.tmp-` name and renames it into place, so a reader sees either no tree or a whole one;
> later readers verify it byte for byte and hold a shared `flock` on its `.lease` for as
> long as they hold the packs. Nothing is materialized until the first real caller: package
> init writes nothing. When that location is unusable, the process falls back to its own
> tree in `$TMPDIR/yolo-embedded-lease-*`, which `ReleaseEmbedded` deletes. Do not make a
> second process-lifetime copy: three call sites did, and each leaked a never-removed
> directory on every invocation of every command. A caller that wants its own lifetime
> calls `MaterializeEmbedded` and deletes the destination itself.
> [`embeddedcache.go`](../../internal/packload/embeddedcache.go) is the authority.

---

## What a pack is, on disk

A pack is a directory. Nothing in it is mandatory.

```
my-pack/
├── pack.json      # optional — the manifest
├── briefing/      # optional — every *.md directly inside is prose for every agent
│   └── house-rules.md
├── derive.lua     # optional — Lua producers for config dynamic layers
└── skills/        # optional — one dir per skill, each with a SKILL.md
    └── rust-review/
        └── SKILL.md
```

The zero-ceremony pack — a `briefing/` directory plus a `skills/` tree, no `pack.json` — is a
complete, useful pack: house rules and a skill corpus applied in every jail. `yolo pack
init` scaffolds exactly this.

<a id="briefing-directory"></a>**`briefing/` is the conventional prose source**
(`packdecl.DefaultBriefingDir`), and it is a directory of files rather than one file
([`OQ-PB1`](#oq-pb1)):

- Every regular `*.md` **directly** inside it is read. A subdirectory is not read, nor is a file
  with another extension; `yolo pack lint` names each one it skips. A symlinked prose file is
  followed.
- Names match case-sensitively, so `Briefing/` is not the convention — on a case-insensitive
  filesystem too (default APFS, which `macos-user` and a Mac's `yolo host apply` read packs
  from): the directory is read only when the pack root holds an entry spelled exactly `briefing`,
  never by opening the path.
- A pack's delivered prose is ordered **byte-wise by pack-relative path** and stays contiguous.
  That order covers every file the pack delivers, a declared `from` outside `briefing/` included,
  so `briefing/x.md` precedes `files/pi-rules.md`. Packs keep their config order, so one pack's
  filenames never reorder another pack's prose, and a rename inside one pack reorders only that
  pack's section.
- An empty or whitespace-only file contributes nothing, silently. So does an absent `briefing/`.

**A root `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` is never read as pack prose**, with or without a
manifest, at any notch ([P1 (briefing defaults)](#briefing-p1)). Agent tools read
those names as a *repository's own* instructions, and a pack is often a repository, so shipping
one gave that file two readers: agents working in the pack's repository, and every agent in every
jail selecting the pack. The repository keeps it. A root `AGENTS.md` used to be the conventional
source, and a pack whose `AGENTS.md` was meant to ship delivers nothing from it until the file
moves under `briefing/`. That is a hard cut, with no notice at launch or at `yolo host apply`
([`OQ-PB3`](#oq-pb3)). One text for both readers is
written once under `briefing/` and pointed at from the in-repository file (`CLAUDE.md` can say
`@briefing/house-rules.md`).

**Those three basenames are refused as a SOURCE at any depth**
([`OQ-PB2`](#oq-pb2)), both ways, with a message
spelling the `git mv`:

- a `briefing` `from` naming one (`packdecl.RepositoryInstructionFile`, in the validator, so the
  tolerant in-jail decode refuses it too);
- a file with one of those names inside `briefing/` — a fatal `packload.LoadDir` problem, so it
  refuses every launch, `yolo pack lint` and `yolo check`.

The two refusals share one message (`packdecl.ReservedBriefingSourceProblem`), so the manifest
spelling and the file spelling teach the same move. The explicit-`from` refusal is on `briefing`
alone; a `skills` or `files` `from` is not checked against the basenames.

> [!NOTE]
> **`yolo check` and the launch agree about a pack that does not load.** Every
> `packload.LoadDir` problem is fatal at launch, and `check`'s Packs section fails on each one
> with LoadDir's own message (`internal/cli/check/packs.go`, over the tree `config.ResolvePack`
> staged). It used to keep a pack only when LoadDir returned no problems and drop one that
> had problems without printing them, so a pack carrying `briefing/AGENTS.md` passed
> `yolo check` and was then refused at launch
> (`TestSectionPacksFailsOnALoadProblemTheLaunchRefuses`). The gap was closed in `check`,
> where it was; do not answer a check/launch disagreement with a second refusal site.

The rule is about sources only. A destination path ending in `AGENTS.md`, which several shipped
agent packs declare, and `after: "host:AGENTS.md"` are not sources, and neither is checked.

<a id="one-governance-reader"></a><a id="briefing-r5"></a>

> [!WARNING]
> **A pack's conventional sources have ONE reader, `packload.GovernedSources`**
> (R5 (briefing defaults), [ruled](#briefing-r5-row)). The jail briefing composer, the jail skills reader,
> the host notch's borrower, `yolo host apply` and `yolo pack lint` all derive from it
> ([`governance.go`](../../internal/packload/governance.go)). Each notch used to keep its own
> "does this pack declare anything of this kind?" gate. The three agreed, which is how one trap
> reached every notch: declaring one narrow delivery switched off the pack's whole implicit
> broadcast. A fourth reader that re-lists the convention will drift the same way.

> [!WARNING]
> **Governance reads the pack's ORIGINAL declaration, never a `ResolveDestinations` clone.** The
> clone appends a synthesized `{into, from}` copy of each borrowed destination, so reading it
> gives every explicit `from` two governors and turns the implicit borrower into an
> omitted-`from` content line — and the implicit broadcast vanishes at the host notch only.
> `Pack.origDecl` is the pointer the clone keeps for exactly this.

Content rules are enforced at staging by `internal/packstage`, and a violation is **fatal,
not skipped** — a pack that half-stages is worse than one that fails loudly:

| Rule | Enforcement |
| :--- | :--- |
| A symlink whose target escapes the pack root is refused | staging error |
| Staging clears a destination dir's *contents*, never the dir itself | avoids clobbering a mountpoint |

Skills staging deliberately *does* dereference symlinks, because its source is the user's
own home — a different source, so a different rule. The one skills source that is **not** yours
is the workspace, which a `git clone` populates and the agent can edit, so its layer has its own
reader: links are followed only while they stay inside the workspace, and one that leaves it is
skipped and named, never read ([the workspace layer](agent-briefings.md#the-workspace-layer)).

A pack **ships its tools**: a file carrying the exec bit stages executable, so a skill can
deliver the script it tells an agent to run. That holds for a pack configured by path; an
**embedded** pack's files read back `0444` whatever their mode in the repository, so a program
an embedded pack's loophole runs is declared as a download instead
([a program the loophole downloads](loophole-system.md#a-program-the-loophole-downloads)).

> [!WARNING]
> **Do not re-add an `allow_exec` gate on the exec bit.** It read as a trust boundary and
> was not one — `bash file.sh` never needed the bit — so it stopped nothing an adversary
> would do while failing on the honest case it kept meeting. Two narrower things replaced
> it, one enforcing and one informing: a destination on the **jail's PATH** is refused in
> the manifest (`packdecl.appendJailPathProblems` — the dir itself, a parent of it, or
> anything inside it), so a pack reaches PATH by declaring [`program`](#program) and nothing
> else; and the executables a pack ships are counted as a **claim** in its footprint, since
> a mode bit is a property of the tree with no manifest line a reader could find it on.

`yolo pack lint` runs the real staging executor against a directory and reports what it
would stage and what it would drop, so these rules are checkable before a pack is
configured.

<a id="pack-lint-questions"></a>Its two whole-pack checks ask two questions, and never as one
([§7 (pack batch)](#batch-7)). Each is a failure, not a warning: lint prints it as a ✗ line and
exits 1.

- **Does the pack do anything?** A pack that declares zero contributions *and* ships nothing read
  by convention fails lint. Both halves are required, because a pack with no manifest does
  real work through its `skills/` tree and its `briefing/` prose.
- **Does it stage content nothing reads?** This fires only when not one staged content file is
  claimed, by a contribution or by a conventionally-read location, so one stray file beside
  working content draws no line.

A declared `from` that stages nothing is a more specific complaint, and it replaces both. A pack
that merely leaves out a part it could ship never fails either check.

## The manifest

`pack.json` carries a handful of per-pack facts plus one list of typed contributions. Every
field is optional; a pack with no `pack.json` behaves as an empty manifest. Decoding is
strict (`DisallowUnknownFields`) and reports *every* problem, not the first, so a typo in
one contribution does not mask a second.

| Top-level key | What it is |
| :--- | :--- |
| `name` | informational — **not** the pack's effective name (below) |
| `description` | prose for `yolo pack ls` |
| `contributes` | the pack's effects: one list of typed contributions, each with a `kind` |
| `skills_tier` | the pack's opt-in to having its own skills namespaced |
| `supersedes` | the pack's claim that a [capability](#capabilities-and-supersession)'s job no longer needs doing |
| `needs` | [conditional pack dependency](#needs--conditional-pack-dependency): another pack joins the launch when a condition holds |

The last three are **top-level rather than contribution kinds**, and the argument is the
same each time: a kind is something the pack DELIVERS at a target, with a `Combine` rule
saying how two claims on that target resolve. None of these delivers anything or owns a
target — they are per-pack facts about how the pack relates to its environment. A
contribution that contributes nothing is a category error, and `supersedes` is pinned
rejected as a kind by `TestSupersedesIsNotAContributionKind`.

> [!IMPORTANT]
> **`name` is informational — it is not the pack's effective name.** That comes from the
> `packs` entry that selected the pack (its explicit `name`, else the last segment of its
> source address), or for a pack yolo ships, its directory under `packs/`. The effective
> name is both what the staging directory derives from and the handle `yolo pack ls` prints
> and `yolo pack explain` takes — fixed from the config line alone, before any `pack.json`
> is read and before a git source is fetched, so the manifest cannot supply it. The
> **staging directory** is `PackEntry.Slug`'s escaping of that name, and it — not the name —
> is what a `reads-host` grant with no `into` is mounted under, because it is the only
> string the jail can name a pack by (`Pack.StagedSlug`). A shipped pack's `name` is pinned
> equal to its directory so the two cannot drift
> (`packload.TestEmbeddedPackManifestNamesMatchTheirDirs`).

```jsonc
{
  "name": "claude",
  "contributes": [
    { "kind": "program", "bin": "claude", "via": "installer", "url": "https://claude.ai/install.sh" },
    { "kind": "skills",  "from": "skills", "into": ".claude/skills" }
    // ...
  ]
}
```

Each contribution has a required `kind` drawn from a closed set core owns, plus the fields
that kind uses. The struct is a flat superset — a contribution carries only the fields its
kind reads, and a field belonging to another kind is **refused by name** rather than
ignored, because a field that is silently never read is a declaration that does nothing
with nothing to say why.

**Every path is relative and points into `$HOME` or the pack.** Absolute paths, `..`
segments, and `:` are rejected as a security property, not a style rule: a pack —
especially a fetched one — naming `/etc/shadow` or `../../` must never validate. This check
runs on every path-bearing field of every kind.

### Unknown kinds across the version boundary

An unknown `kind` is a loud load error **at authoring** — every host-side read — and, across
the version boundary only, a skipped-and-reported contribution instead. The in-jail load
runs `packload.TolerateSkew()`, so a manifest using a kind a pre-`just load` entrypoint does
not know still boots the jail, warning by name.

> [!WARNING]
> **Tolerance is for a kind, not for a CONSTRAINT.** A settings declaration is refused on
> the tolerant decoder too, and that inversion is deliberate: an unknown key that a newer
> build would read as a *restriction* must not be dropped, or core validates a value against
> half a rule. `TestUnknownSettingDeclarationKeyIsRefusedByBOTHDecoders` pins it.

### Retired kinds

A kind can also **leave** the set, and it is then neither known nor unknown but **retired**:
refused at authoring with its *replacement named*, skipped across the version boundary like
any other kind this build cannot render. `packdecl`'s `retiredKinds` is the registry, and it
is the same pattern the repo uses for a removed top-level config key (`journal`,
`host_processes`) and a removed contribution field (`tier`, `requires_provider`).

The generic *"unknown kind (expected one of …)"* would be the wrong answer: it reads
identically whether the kind never existed or was deliberately taken away, and it tells an
author nothing about what to write instead. The skip note differs too — it must not repeat
the version-skew promise that *"a build that knows the kind will render it"*, because no
build will.

| Retired kind | Replacement |
| :--- | :--- |
| `launch` | the `autonomy` kind's `autonomous`/`guarded` postures, whose nested `launch` block is a **different thing** and is where launch flags are declared. A flag declared as a top-level `launch` contribution was one **no notch could withhold** — exactly what the confinement policy exists to prevent. The kind's other half, a flag-ALIAS map, is gone with no replacement: a table of other spellings of one switch restates the tool's own flag parser in a place that cannot notice it drift. |

## The kinds and how two claims combine

The kind set is closed and core-owned. **`packdecl`'s `footprints` map is the authority for
the whole set** — the map key is what `KnownKinds()` derives from, so there is deliberately
no second list to drift, and each entry carries that kind's combine rule, its one-line
claim description, and whether it can produce a review-worthy claim. `KnownKinds()` returns
the set sorted alphabetically; the map is in declaration order.

What a kind's **combine rule** says is how two claims on the *same target* resolve. This is
the semantic axis, and it is short:

| Combine | Meaning | Kinds that use it |
| :--- | :--- | :--- |
| **Exclusive** | one owner per target; a second claim is an error | `program` (by bin) · `files` (by path) · `config` (by surface identity) · `autonomy` · `loophole` (by loophole name) · `service` (by service name) · `provider` (by provider name) · `adapter` (by `from → to` pair) · `blocked-tool` (by bin) · `intercept` (by bin) · `profile` (by pack + name) |
| **Shared** | many independent claimants are the ordinary case | `requires` · `reads-host` · `mount` |
| **Merge** | many inputs into one target is the feature | `skills` · `env` (a key claimed twice collides) |
| **Concat** | ordered concatenation | `briefing` |
| **Overlay** | ordered after the target's owner, never a collision | `config-overlay` (later wins, per-key provenance) · [`config-list`](#adding-entries-to-an-array-config-list) (entries appended, first occurrence wins) · `models` (a provider's model list: every `add` appended, then every `only` intersected, the user's own `providers.<name>.models` last; [providers.md](providers.md#model-lists-shaped-by-packs)) |
| **Scoped** | the same subtree at two scopes is an error | `state` |
| **PerHook** | per hook name | `hook` |

Exclusivity is **per target, not per pack**: one pack shipping three loopholes, two
programs or two providers is ordinary; two packs claiming one name is the collision. The
reasoning is the same everywhere — the name is the whole identity. A shadowed loophole name
is a daemon nobody audited running under a name the user trusts, and everything downstream
keys on the name: the state dir, the endpoint, the `enabled` toggle, the disclosed claim.

The **footprint** is this table applied to a concrete set of packs: the union of every
claim, plus the collisions where an Exclusive or Scoped target is claimed twice. `yolo pack
footprint` prints it and `yolo check` folds it in. Some claims are flagged `⚠ review`
because they widen the trust surface — machine-scope state (it leaks across workspaces), a
`reads-host` grant, a `mount` of a host directory, an installer URL, a briefing that
prepends a host file. The review flag is an invitation to look, not a refusal. A second,
strictly narrower flag marks a claim whose crossing is **execution on the user's machine**,
so a reader scanning a footprint does not have to notice that one of a dozen
identically-flagged lines happens to say RUNS.

### Collisions the generic loop cannot see

The generic loop keys collisions by `(kind, target)`. Four things need their own pass
because they do not fit that shape, and all four live in `internal/packload/footprint.go`:

- **`AgentNameCollisions`** — the AGENT NAME is exclusive **across kinds**, and it is the
  only namespace that is. A pack claims one by installing its launcher (`program`) or by
  declaring where that agent reads (`briefing`/`skills` `agent`). Two packs claiming one
  name is fatal at launch, at `yolo host apply` and at `yolo check` — and two of the three
  claiming kinds merge by design, so the generic loop cannot express it. `requires` is deliberately **not** a claim on the
  name: it is Shared for a reason, and a content pack asserting `claude` beside the pack
  that provides it is an ordinary dependency.
- **`ConfigSurfaceCollisions`** — see [config surfaces](#config-surfaces-and-the-compose-engine);
  a pack can commit this against *itself*, which the generic loop skips.
- **`LoopholeNameCollisions`** — the claims come from a file outside `pack.json`.
- **`pluginNameCollisions`** — same, for a wrapped plugin.

### The per-kind rules worth knowing

`internal/packdecl` is the field-by-field reference. What follows is the subset where the
rule is not derivable from the field name — and the traps.

#### `program`

Installs a tool and puts it on PATH via a launcher that installs on first invocation — and
thereafter mediates **every** invocation, which is what keeps an agent dependency current.
The launcher goes in `~/.yolo/bin/launch/<bin>`, which is **second** on PATH: immediately
after the blocked-tool shims (which stay first, because interception must outrank
installation) and **ahead of every install prefix**, and so ahead of `/bin`.
`entrypoint.BootPath` is the authority for that order, and there are two more
independently-written copies of it — the `.bashrc` export, compared to `BootPath` entry by
entry by `TestBashrcPathMatchesBootPathOrder`, and `macosuser.SandboxPath`.

> [!WARNING]
> **This position is load-bearing and the obvious alternative is a defect.** Ordering the
> launch dir *after* the prefixes it installs *into* makes a launcher unreachable the moment
> it succeeds, so the lazy install runs exactly once per home and the update never runs
> again. An **agent dependency** wants to be current, so the launcher has to mediate every
> invocation, not just the first.
> [`../design/program-delivery.md`](../design/program-delivery.md)
> [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)
> (P6) is the standing account.

What the old position bought is now bought by a check at generation time:
`internal/entrypoint/launchercollision.go` writes **no launcher at all** for a name `/bin`,
`/usr/bin`, a declared `mise_tools` entry, or the store-delivered package farm already
provides. Same outcome, weaker guarantee — the failure is **handled** rather than
unrepresentable, which is the honest cost. The consequence to know is unchanged: a name the
**image** bakes still wins over the pack's declared version, now because the launcher
declines to exist.

> [!WARNING]
> **The collision check must never be spelled *"is this name already resolvable on
> PATH?"*** — spelled that way it is a silent kill switch that destroys the feature. After
> one successful install the installed binary exists in a prefix, so the next boot writes no
> launcher, so PATH resolves it directly, and evergreen works exactly once: green, silent,
> and identical to the freeze the reorder exists to end. The install prefixes are excluded
> from the probe by the property that *defines* them — every one lives under the jail home,
> and nothing the image ships does. **Declared, not installed, for mise too:**
> `GenerateAgentLaunchers` runs before mise is configured, so the shim directory is empty on
> a cold boot and a check that read it would shadow a project dependency on exactly the
> boots where the project is new.

Version selectors decide whether the launcher polls. **No selector means `@latest`**, which
the launcher re-checks against the registry once an hour — so an unversioned declaration
changes what runs with nobody present. **A selector turns that poll off**: the registry's
`latest` is not an answer to a declaration that named its own version, and for a tag or a
range it would never compare equal, so polling would reinstall hourly forever. A pinned
launcher compares the recorded spec against the declared one, offline, so moving a pack from
one version to another still takes effect. The recorded spec is what npm **installed**,
never merely what was asked for — an upgrade leaves the previous binary in place, so
recording a failed attempt would shut both exits at once and freeze the jail on the old
version with nothing left to retry.

`update` is the argv that makes the program update ITSELF, with the bin omitted. The
launcher runs it when the program is due an update; absent, it falls back per `via` —
re-running the declared installer, or a global npm install. It is a vendor's argv and
therefore deliberately NOT a closed enum: the vendors disagree, and core hardcoding one is
how `yolo pack update` came to skip the installer class entirely. `update` is read on
`program` alone and refused by name on every other kind.

`versions_dir` is the home-relative directory where an installer-delivered program's vendor keeps
**one entry per installed version**. After every successful install or update the native launcher
keeps the newest two entries there, plus the one `~/.local/bin/<bin>` resolves into, and removes
the rest ([the V-axis prune](agent-cli-copies.md#the-v-axis-prune)). Absent, it is
`.local/share/<bin>/versions`, claude's layout. Codex declares
`.codex/packages/standalone/releases`. It is read on a `program` with `via: "installer"` alone and
refused by name everywhere else. `packdecl` refuses a path that is absolute, escapes the home, is
unclean, or names the home itself.

`refresh` is the program's **pre-launch refresh**, a term coined for this field (2026-09-25).
It is an object: `argv`, the program's own argv with the bin omitted, and `lock`, a
home-relative lock directory whose parent is the store the refresh writes. Pi declares
`{"argv": ["update", "--extensions"], "lock": ".pi-shared-npm/.yolo-update.lock"}`. The launcher
runs it right before the exec, at most once an hour on a machine-global stamp, and only when
`agent_updates` lets the pack move. It is bounded by the same timeout as an update, reads
nothing from the terminal, writes its stdout to stderr, and runs only while it holds `lock`.
`lock` is a non-blocking `mkdir`: a lock another jail holds skips the refresh, and so does a
store that is missing or read-only. The holder touches the lock while it runs, so only a lock
whose launcher died goes stale. Every outcome still launches the program. The lock serializes
the refresh only, not writes the program makes to the store on its own. A refresh is not
`update`: it leaves the binary alone, ignores the binary's pin, and `yolo pack update` does not
run it. `refresh` is read on `program` alone. `packdecl` refuses an empty `argv` or word, and a
`lock` that is absolute, escapes the home, or has no directory above it. It also refuses a lock
whose name does not start with `.yolo-`: the lock sits inside the store, and that prefix is what
tells yolo's bookkeeping apart from the tool's content when the `shared_directory` hook asks
whether the store is empty.
[`pi-extension-lifecycle.md`](../design/pi-extension-lifecycle.md#32-execution-tier-pre-launch-auto-refresh)
is the design.

`due_on_change`, optional, lists home-relative **files** whose content makes the refresh due
regardless of the hourly stamp: pi declares `[".pi/agent/settings.json"]`. The launcher keys the
content of every listed file (an absent file counts as its own content) and keeps one marker per
key beside the refresh stamp. A key no refresh has succeeded for is due. It is keyed on content,
not mtime, because yolo rewrites a composed file on every boot; and markers are per key, not per
workspace, so two workspaces with different settings each refresh once and then stop. A refresh
that exits non-zero records nothing, so the change stays due. A lock another jail holds is still
skipped, with one exception: a launch whose content has never been refreshed with waits for the
holder, bounded by the update timeout, because running the program instead would let it install
what that content names outside the lock. It exists for the first-install race of a
machine-shared store
([`pi-git-extension-caching.md` §3.12](../design/pi-git-extension-caching.md#312-the-refresh-trigger-that-stays)).
`packdecl` refuses an empty list, an empty, absolute, escaping or unclean entry, and a
duplicate.

`platforms` is **where the vendor publishes a build**: a list of `<goos>` or
`<goos>/<goarch>` entries, spelled as Go spells them. Absent means every platform, which is
what almost every pack wants and what every manifest written before the key kept meaning. A
bare GOOS matches every architecture on it (`"linux"`), because a Node CLI is OS-shaped far
more often than machine-shaped; an empty list `[]` is refused, since it declares support for
nothing. The same field, the same grammar, is what a [`service`](#service) declares about
where its daemon can run — one question asked of two kinds.

When this machine is not in the list, `GenerateAgentLaunchers` writes **no launcher** and
says so, naming what *is* published beside what this jail is. That is the same disposition
the collision check above takes, and the same one a platform-excluded loophole gets
(one disclosed line, and the launch continues): a pack is more than its program, so refusing
the launch would turn a degraded pack into an unusable one, and the platform is the one
fault class no user can act on.

> [!NOTE]
> **`omp` is the case that bought the field.** Its vendor publishes `darwin-arm64` and
> `linux-x64` only. Without the declaration an arm64 Linux jail selecting `omp` installed
> nothing, said nothing, and handed the agent the vendor's own `oh-omp: unsupported platform
> linux-arm64` the first time it ran — a fact the manifest knew statically, discovered
> dynamically and misattributed. Found by CI, whose arm64 install job was red while the
> x86-64 one was green.

`protocols` is **which wire protocols the installed binary can be pointed at** — a list of
provider endpoint keys (`anthropic`, `openai`), in preference order. It is read on `program`
alone, because it is a fact about a binary; absent means unconstrained, and an empty list is
refused. [`protocol-resolution.md`](protocol-resolution.md) is what reads it.

`node_floor` is the **minimum Node version the program's own entrypoint needs**, again on
`program` alone. When it is set, the generated launcher execs an interpreter that meets it rather
than the workspace pin's, and a floor nothing satisfies refuses the launch.
[`agent-program-runtimes.md`](agent-program-runtimes.md) is what reads it.

`provider_sets` (`true`) declares that the agent this program installs **holds several providers
in one session**: its pack's derives read the whole active set (`ctx.active_set`), so a list in
the `profile` key or a `-p <bin>=a,b` list of more than one profile may select for its bin. On
`program` alone. A program that declares nothing is single-provider, and a list named at its bin
is refused before anything starts, because a derive written before sets would run the first
entry and drop the rest in silence ([`providers.md`](providers.md#an-active-set-several-profiles-for-one-agent),
[AP-D2](../design/active-provider-sets.md#AP-D2)). `packs/pi` declares it.

<a id="model_catalog"></a>`model_catalog` names the files, inside the package an npm `program`
installs, that hold its agent's own **model catalog**, meaning the model ids the agent knows with
no network. It is a list of slash-separated globs, one pattern per path segment, relative to the
installed package's directory (`<npm prefix>/lib/node_modules/<package name>`). Every file one
matches is read as JSON, and every string an object holds under an `id` key, at any depth, is an
id that catalog knows. `packs/pi` declares pi-ai's per-provider data files. `yolo check` is the one
reader: it warns about a model id a provider list names that no installed agent's catalog knows
([`providers.md`](providers.md#model-lists-shaped-by-packs),
[MM-D19](../design/model-lists-and-pickers.md#MM-D19)). It reads the files where the agent is
installed and runs nothing. On `program` with `via: "npm"` alone; `packdecl` refuses an empty list,
and an entry that is empty, absolute, unclean, escaping, backslashed, a duplicate, or not a valid
pattern.

`install_hints` maps a host package manager to the package that provides `bin` there. Used
below the `jail` notch, where yolo bakes no image, by `yolo check-deps` / `apply` to probe
for the binary and emit a runnable manifest. One key names an installer *flavor* rather than
a manager: **`brew-cask`**, because a Brewfile `brew` line naming a cask token fails and
bare `brew install <token>` silently prefers a same-named *formula* — brew's `copilot`
formula is AWS's deprecated ECS CLI, not the CLI this pack means. `brew-cask` wins over
`brew` when a pack declares both; the detected manager stays plain `brew`.

> [!WARNING]
> **Do not add `nix` hints to a pack whose tool ships its own installer and updater.** The
> agent packs dropped theirs: routing a user through nixpkgs hands them whatever that
> repo has, with nothing in the output to say so (measured once at 16 releases behind), and
> `detectManager` reaches `nix` only by *elimination*, so a user cannot select it
> deliberately anyway. `nix` hints belong on genuine third-party dependencies where the
> user's own package manager is the right answer. A `via: installer` program's remedy is a
> download-check-run command, never a pipe into `sh`: it fetches the script to a temporary file,
> `yolo internal installer-check` refuses a web page or a binary naming the URL, and only then
> does `sh` run it ([`PS-D4`](../design/provisioner-sets.md#PS-D4)). `yolo check-deps` only
> prints it; `yolo host apply --assert` runs it behind its one prompt, with no terminal
> ([`PS-D1`](../design/provisioner-sets.md#PS-D1)).

<a id="program-via-source"></a>**`via: "source"` is a fork**: a program built from a pinned source
address instead of a registry or a vendor installer
([`forked-programs-as-packs.md`](../design/forked-programs-as-packs.md)). A fork is declared as a
fork OF a base pack and claims no name of its own:

```json
{ "kind": "program", "bin": "pi", "via": "source", "fork_of": "pi",
  "source": "git+https://github.com/you/pi-fork?ref=main",
  "build": "npm ci && npm run build && npm install -g \"$(npm pack --silent)\"",
  "produces": [".npm-global/bin/pi", ".npm-global/lib/node_modules/pi-fork"] }
```

- **`fork_of`** names the base pack. The base keeps the bin, so its launch flags, autonomy
  posture, profiles, briefing and skills stay its own
  ([FP-D2](../design/forked-programs-as-packs.md#FP-D2)).
- **`source`** is a pack source address, git transports only. A `file://` directory is refused,
  because a directory has no revision to key a build on. `git+file://` names a local repository and
  is accepted.
- **`build`** is one command line, run by bash in the checked-out source.
- **`produces`** lists the home-relative paths the build must leave, each inside a program surface
  (`.npm-global`, `.local`, `go`, or codex's `.codex/packages/standalone`). One of them must be the
  program itself at `.local/bin/<bin>`, `.npm-global/bin/<bin>` or `go/bin/<bin>`. The jail's
  launcher deletes every listed path before it puts a different build in place, and materializing
  merges into a directory that is already there, so listing the directory the build installs into
  is what makes a new pin replace the old build whole.

Beside those four, a fork may declare `platforms` (where it builds) and `node_floor`. `packdecl`
refuses every other program field on a fork by name, because each belongs to the base
([FP-D6](../design/forked-programs-as-packs.md#FP-D6)). An inherited `update` verb or
`install_hints` would replace the pinned build with the vendor's release. The four fork fields are
refused on every other kind and every other `via`.

A fork's own contribution installs nothing: `InstallContributions` skips it, and it claims no agent
name. Its footprint claim is `<bin> (fork of <base>)`, review-worthy, and names the source and the
build.

**The selection rewrites the base.** Where the selected set is final, a copy of the base pack's
program is rewritten with the fork's delivery (`packload.ApplyForks`), so every reader of the base's
programs, the launcher generator and the host floor included, sees the fork's. It runs in the one
selection function (`config.SelectPacks`), which every host verb and the launch read, and in the
jail's pack loader over the staged tree, whose base `pack.json` is unchanged. The rewrite keeps the
base's `refresh`, `protocols`, `provider_sets`, `platform_switches`, `capabilities`,
`platform_regions`, `unlisted_background_models` and `node_floor` (a fork's own `node_floor`
replaces it). It drops every other delivery field of the base: `package`, `url`, `flags`, `update`,
`versions_dir`, `install_hints`, `model_catalog`, and `platforms` unless the fork declares its own.

A fork brings its base into the launch the way an unconditional [`needs`](wire-bridge.md#needs--a-conditional-pack-dependency)
entry does, with a cause line (`+ pi (forked by pi-matt)`), when the base is a pack yolo ships. A
base yolo does not ship must be in `packs` by name. The selection refuses a fork whose base is
absent, a base that declares no program by the fork's bin, a pack forking itself, and two forks of
one program.

**The pin is `forks.lock.json`**, beside `packs.lock.json` in `~/.config/yolo-jail`, keyed
`<fork pack>/<bin>` ([FP-D7](../design/forked-programs-as-packs.md#FP-D7)). `yolo pack install` pins
every selected fork the lock does not already pin for its declared `source`, resolving the ref to a
commit, and leaves a pinned fork alone when its branch moves. For every pin, new or standing, it
checks the commit out into the pack store, fetching the repository first when this machine has
never fetched it, so a fork lock that arrived with the config builds the same commit here as on the
machine that pinned it. `yolo pack update` re-resolves every
fork. `yolo pack status` lists each pin and fails on a pin made for a source the fork no longer
declares. A launch only reads the lock: each launch that carries a fork prints the commit it is
pinned to (`fork <pack>: <bin> (in place of pack <base>'s) is built from <source> at commit <sha>`),
or why it has none and the command that pins it. A fork's source never goes through the launch's
hourly pack refresh.

**The build runs in a sealed capture jail**
([FP-D9](../design/forked-programs-as-packs.md#FP-D9)). The pinned commit is checked out of the
pack store's mirror and copied into a workspace inside the capture store. The ordinary run pipeline
then runs the fork's `build` there under the seal (`run.Options.Sealed`), which withholds every
crossing of the host into the jail: `env_sources`, pack `env`, provider credentials, `host_files`,
`mounts`, pack `mount` and reads-host layers, loopholes and host services, machine-scope pack
directories, the host-cache alias and the nix daemon socket. It also withholds the host's network:
the build runs on the runtime's own bridge whatever `network.mode` says, with no host-loopback
forwarding, so no service the host binds to 127.0.0.1 is in its reach
([FP-D13](../design/forked-programs-as-packs.md#FP-D13)). `~/.cache` and `/mise` are private
directories of the build's workspace. The selection is narrowed to the fork and its configured
base. A build whose result misses a `produces` path stores nothing, and so does one that leaves a
link into its own workspace, which is deleted when the build ends: `npm install -g .` is the common
cause, npm installing a folder as a link to it, and the refusal names the copy-installing spelling
(`npm install -g "$(npm pack --silent)"`). An admitted build is
recorded under a `kind: "build"` receipt carrying the commit, the recipe hash and the jail's image
identity ([FP-D8](../design/forked-programs-as-packs.md#FP-D8)). Selection keys a fork's entry on
its bin, platform and source address, so an installer capture never answers for a fork or the
reverse. `yolo capture <bin>` of a forked program is the explicit rebuild, and refuses while another
build of the same commit holds its lock.

**A launch delivers the fork in place of its base's program**
([OQ-FP4](../design/forked-programs-as-packs.md#14-decision-ledger),
[FP-D3](../design/forked-programs-as-packs.md#FP-D3)). A container launch carrying a pinned fork
looks up the build of that commit in the capture store and, on a miss, builds it before the jail
starts, saying so. An attach to a running jail builds nothing, because that jail read its decisions
when it booted ([FP-D14](../design/forked-programs-as-packs.md#FP-D14)). A launch that finds another process building the same commit waits for it, at
most 20 minutes (`forkBuildWaitBound`), and then uses its entry. A failed build or an expired wait
never fails the launch: it is that fork's reason. The launch hands the jail each forked bin's store
key, or the reason it has none, in `YOLO_FORK_BUILDS`. Neither the lookup nor the build runs in a
capture or build jail, so a build cannot start a build.

In the jail the forked bin's launcher is a **source launcher**
([`forklauncher.go`](../../internal/entrypoint/forklauncher.go)). Its first run deletes the
`produces` paths, materializes the key from the mounted store
(`yolo internal capture-materialize --key`), and records the key under `~/.local/state/yolo` in
that home; every later run execs the program, and a launch handing it a new key replaces the build.
The record is the home's own, never the machine-wide `~/.cache` every jail shares, so one
workspace's materialize cannot vouch for what another workspace's home holds. With no key it prints the reason and
exits 1. It never installs the base's package in its place, never runs an older build still in the
home, and has no update step: `yolo pack update` in the jail says the pin moves it. A fork's
`node_floor`, or its base's, joins the launcher's exec prefix as an npm launcher's does.

Two backends deliver no fork yet. A `macos-user` launch carrying one says that its program is not
delivered there and names hand-off H4
([FP-D3](../design/forked-programs-as-packs.md#FP-D3)). Apple Container below its read-only floor
mounts no capture store, so the launch builds nothing and each fork's launcher says why.

#### `requires`

A binary that must **already exist**. Asserts presence and installs nothing — no launcher,
nothing on PATH — so unlike `program` it cannot shadow the very binary it asserts.

`program` and `requires` are *install* vs *presence*, and conflating them was a real defect:
a pack needing a tool the image already bakes, or the user already has, had only `program`,
so it either lied — declaring an npm install for a baked binary, which then shadowed it — or
declared nothing and lost `install_hints` entirely.

`via`/`package`/`url`/`update`/`refresh` are **refused by name**: those belong to `program`, and a
`requires` carrying one is the author reaching for the other kind. At the jail and guest
notches a missing bin is a **warning naming the bin**, never a boot failure — the pack's
other contributions are fine, and a fatal here would stop the jail you need in order to fix
the pack. At the host notch it feeds `check-deps` / `apply` exactly as `program`'s hints do,
which is what lets a content-only pack carry a remedy.

#### `skills`

A skills tree merged into an agent's skills dir. Layer order is the workspace < built-in < pack
< the local pack (`~/.config/yolo-jail/local`, appended last); the workspace layer exists in a
jail only and never shadows ([below](#project_dirs)). Two packs claiming one unnamespaced name at
one destination is fatal at every notch, the local pack included
([the collision warning](#skills-collision)). `from` defaults
to `skills/`, and is honored at both
notches and by wrapped-plugin discovery through one resolver (`packload.SkillsSourceDir`).

A content contribution routes by `into` (a path), by `agents` (an audience), or by **neither**,
which broadcasts to every skills destination the selected packs declare — the same rule as
[`briefing`](#briefing), including who governs what. The `skills/` tree is **one unit**: it
broadcasts implicitly unless a content contribution names it, by omitting `from` or by
`from: "skills"`. A contribution naming a *different* tree adds that tree and leaves `skills/`
broadcasting ([P3 (briefing defaults)](#briefing-p3)). Two content contributions naming one tree
are refused ([`OQ-PB5`](#oq-pb5)).

A **destination** (`agent` + `into`, the line every agent pack uses to name where its agent
reads skills) sources nothing, and `from` on one is refused, naming the addressed spelling
([P5 (briefing defaults)](#briefing-p5)). An agent pack that carries a `skills/`
tree of its own ships it as the implicit broadcast, which reaches its own destination like any
other pack's.

A `from` naming a directory the pack does not contain delivers nothing and is **reported by
name** — a warning at launch, a `refused` line and a non-zero exit at `yolo host apply` —
rather than silently falling back. An absent *conventional* `skills/` is silent, because most
packs carry none.

<a id="project_dirs"></a>A destination may also declare **`project_dirs`**: the
workspace-relative directories its agent reads skills from at project scope, in that agent's own
precedence order (`"project_dirs": [".github/skills", ".agents/skills", ".claude/skills"]` for
copilot). It is data about the agent, and it drives the jail's **workspace layer**
([`workspace-skills.md`](../design/workspace-skills.md)): every shipped pack's declaration,
selected or not, plus the selected packs' own, is the set of workspace directories mirrored into
every destination as the lowest layer; and a destination whose own list names a directory gets no
copy of it, because its agent reads it natively — nor a copy of any skill whose name that
directory carries, from any other. Each entry must be clean, relative, inside the
workspace and outside `.git` and `.yolo`; the field is refused on any other kind and on a content
entry. The host notch ignores it — `yolo host apply` never writes a workspace's skills into a
real home ([`OQ-WS5`](../design/workspace-skills.md#OQ-WS5)). What each shipped pack declares is
pinned in `internal/packload/projectskilldirs_test.go` and witnessed against the installed agent
by `integration/agents_test.go`'s probe, wherever the agent's bundle names the path at project
scope as text; the probe lists the rows it cannot witness and where each was measured instead.

`skills_tier` is a **per-pack** choice, not per contribution, and that is the whole of the
ruling behind it: a tier decides what a skill is CALLED, which is a global property.
Declared per contribution it could not express a consistent name — and a zero-ceremony pack,
which declares no destinations at all and borrows them from the other selected packs,
*inherited* a tier per destination, so one skill acquired two invocation names without the
pack ever choosing either. Values are unnamespaced (the default), `flat` (the same thing
said out loud), and `namespaced` (one subtree per destination, invoked `<pack>:<skill>`).

**A skills contribution may also RESERVE children of its destination** — `reserved`, a list of bare
child names yolo neither adopts nor composes over. It sits beside the tier rule because it is the
same kind of fact about a destination, and it is **pack-declared for the reason the tier is
per-pack**: the name belongs to whoever owns the tree. Core learning one vendor's directory name is
how core learns what an agent is, one string at a time
([`OQ-ST2`](../design/synced-skill-trees.md#OQ-ST2)).

`packs/claude` reserves `synced`. `~/.claude/skills/synced/` is a **sync root**: a bucket Claude Code
fills from a registration that lives *outside* it, so nothing written inside survives that tool's
next sync. It defeats every other guard — not dot-prefixed, a real directory, in no ownership record,
and carrying no manifest at its own level, because a sync root's manifests are two levels down, one
per identity bucket. So it was adopted, moved into the local pack and composed back byte-identically,
which looks like success and loses the user's edits later.

- The fence applies at **every posture** — a reserved child is never a pending change.
- A **non-empty** reserved child is reported on one line: the path, what the tree is, and that yolo
  leaves it alone. *What the tree is* comes from the pack's `reserved_notes` (a map from a reserved
  name to one short phrase — `packs/claude` says "skills Claude Code syncs from your claude.ai
  account"), so core still names no vendor; a child without a note is "another tool's sync root".
  The line is in the default view the first time `yolo host apply` sees the tree non-empty and
  whenever its content changes, and under `--verbose` otherwise
  ([`ST-N2`](../design/synced-skill-trees.md#ST-N2)). An empty one is silent, so a user with the
  feature on and nothing synced gets no line.
- An entry must be a **bare child name**; the schema refuses one carrying path structure, because it
  is compared against a single directory entry and could never match — a fence that cannot match is
  worse than none, since it reads as protection.
A pack still carrying a per-contribution `"tier"` is refused BY NAME with the migration in
the message, rather than failing on the strict decoder's bare unknown-field error.

<a id="skills-collision"></a>

> [!WARNING]
> **A skills name collision between two packs is FATAL at `yolo host apply` and at a jail
> launch**, naming both packs, both source paths, and both remedies (rename, or opt one pack
> into namespacing). At flat tier one pack's skill silently won and the loser produced no output
> line at all. The error costs the deliberate flat-tier override, and that is the trade: an
> intentional override and an accidental clash are the same declaration, so yolo cannot tell
> them apart and the user should. **Adoption preserves, declaration refuses** — migrating a
> user's pre-existing tree keeps both copies (`mine`, `mine-from-codex`), because those are two
> different situations.
>
> **The jail half is [`OQ-NC11`](../plans/notch-convergence.md#OQ-NC11)** (ruled 2026-09-28 by
> parity). It is a launch pre-flight beside the agent-name one, so it refuses host-side before
> any container exists, on an attach too, with the host's message; it is not a boot failure
> inside a running jail. The jail composes through the host's layer plan and writer, so the
> remedy holds there: a namespaced pack's skills invoke as `/<pack>:<skill>` in a jail as at the
> host, and a wrapped plugin is delivered as the host delivers it. The destinations each pack's
> skills reach still differ between the notches ([below](#skills-fanout-s4)).

<a id="skills-fanout-s4"></a>

> [!WARNING]
> **The jail's skills fan-out is not a hole in the selection gate. The S4 audit settled that the
> gate holds; do not re-audit it as one.** In a jail every selected pack's skills reach every
> skills destination the selected packs declare, where the host narrows a content `into` (the
> note under [`briefing`](#briefing)). The fan-out bypasses nothing. `into` is a path, and core
> has no concept of an agent to check it against. Every destination traces to a contribution on a
> pack in `packs`, and `packs` is user scope only, so a repo-committed config cannot add one.
> Selecting a pack is consent to its declared destinations, which `yolo pack footprint` shows
> beforehand. What the audit did find is a reporting gap: a manifest understates where its
> content goes, because the merge is global. Whether the jail should narrow to match the host is
> [`OQ-S4`](../plans/BACKLOG.md#OQ-S4),
> which is open, and `internal/cli/run/packskillsdelivery_test.go` pins today's behavior so that
> answering it moves a test on purpose.

#### `briefing`

Prose concatenated into a briefing file, attributed to its pack. **The destination is
generated wholesale at every notch**, so a hand edit does not survive.

Six rules govern what a pack's prose and skills deliver. Code comments cite them as
"P1 (briefing defaults)" through "P6", so a bare `P1` never resolves to
[this doc's own principles](#principles) or to
[`agent-briefings.md`](agent-briefings.md#why-its-this-way)'s audience rows:

- <a id="briefing-p1"></a>**P1 (briefing defaults). One file, one reader.** yolo never ships a file
  as pack prose if an agent tool reads it as a repository's own instructions — today `AGENTS.md`,
  `CLAUDE.md` and `GEMINI.md`. Those files stay where they are, as the repository's; shipped prose
  lives [under `briefing/`](#briefing-directory).
- <a id="briefing-p2"></a>**P2 (briefing defaults). Silence means broadcast, in a manifest too.** A
  `briefing` or `skills` content contribution naming neither `into` nor `agents` reaches every
  destination of its kind in the selected pack set. It is the audience rule [P2](agent-briefings.md#ba-p2) made reachable
  from a `pack.json`.
- <a id="briefing-p3"></a>**P3 (briefing defaults). Declarations add; they never subtract.** A
  contribution governs the files it names, and nothing it declares about one file changes whether
  a different file is delivered.
- <a id="briefing-p4"></a>**P4 (briefing defaults). A named source is the only source.** A declared
  `from` that cannot be read delivers nothing. It never quietly delivers a different file.
- <a id="briefing-p5"></a>**P5 (briefing defaults). A destination accepts content; it never
  supplies it.** A contribution that declares a destination (`agent` + `into`) sources nothing,
  for `briefing`, `skills` and `files` alike.
- <a id="briefing-p6"></a>**P6 (briefing defaults). Every delivery is visible before the first
  launch**, the implicit ones included, and so is every conventional-looking file that is *not*
  delivered — the [lint listing](#briefing-lint-listing).

A contribution is one of two things, decided by `agent` alone:

| Written | What it is | Sources |
| :--- | :--- | :--- |
| `{kind:"briefing", agent:"claude", into:".claude/CLAUDE.md"}` | a **destination** — the file an agent reads, declared by the pack that owns that agent | **nothing**; `from` on one is refused, naming the addressed spelling ([P5 (briefing defaults)](#briefing-p5)) |
| `{kind:"briefing"}`, `{kind:"briefing", from:"…", agents:["pi"]}`, `{kind:"briefing", into:"…"}` | **content** — prose this pack ships | the files it governs (below) |

`agent` is the IDENTITY a destination declares for itself (the launcher command whose agent
reads it), declared by the pack that OWNS that name; nothing is derived from the pack's
`program`/`requires` bins — the string is declared, carried, and compared literally. A
destination needs `into`. An `agent` beside `agents` with no `into` is refused with both readings
spelled — drop `agent` for content, or give it `into` for a destination — because P5 makes
`agent` mean "destination".

Content routes by **one of three answers**:

- **`agents`**, the AUDIENCE: only destinations whose owner declared a matching `agent`. The
  vocabulary is the SELECTED packs' agent names and nothing wider — naming an agent your `packs`
  do not provide is fatal at launch and at `yolo host apply`.
- **`into`**, a path. A content pack that hardcoded `.claude/CLAUDE.md` would be coupled to a fact
  only the claude pack can keep current, which is why `agents` exists. `into` and `agents` together
  are refused.
- **Neither**: a **broadcast** to every briefing destination the selected packs declare, the
  broadcasting pack's own included. That is what a pack with no manifest gets, and a manifest
  can say it too ([P2 (briefing defaults)](#briefing-p2)): `{"kind": "briefing"}` is valid. It
  names no agent, so it can never be an unmatched audience, and an agent pack selected later
  receives it with no edit. With no destination selected it is simply unused, never fatal.

<a id="briefing-governance"></a>**Each file has exactly ONE governing contribution**, and
declarations only ever ADD ([P3 (briefing defaults)](#briefing-p3)):

- <a id="briefing-r4"></a>A declared `from` names **exactly that one file**, anywhere in the pack.
  It is compared after `path.Clean`, so `./briefing/a.md` and `briefing/a.md` are one file
  (R4 (briefing defaults): governance keys on the *cleaned* pack-relative path, or two spellings
  of one file would read as two files with two governors). Every declared `from` also passes a
  lexical in-pack containment check before it is read.
- A content contribution that **omits `from`** governs every `briefing/*.md` that no other
  contribution names. That is how a pack narrows its whole convention in one line.
- A `briefing/` file that **nobody** names broadcasts implicitly.
- A destination governs nothing, so an agent pack's `{agent, into}` line never switches off its
  own pack's broadcast.

So `{"kind": "briefing", "from": "files/pi-rules.md", "agents": ["pi"]}` beside a `briefing/`
directory delivers **both**: `pi-rules.md` to pi, and every `briefing/` file to every agent.
Naming `briefing/pi.md` the same way narrows that one file and leaves the rest broadcasting. The
order of the `contributes` list never changes which files are delivered, or where.

**A shipped agent pack addresses its own prose to its own agent.** The claude and pi packs each
ship `briefing/worktrees.md` as `{"kind": "briefing", "from": "briefing/worktrees.md", "agents":
["claude"]}` (`["pi"]` for pi), because the file is about that agent's own tool: named by no
line it would broadcast to every agent, and a content `into` narrows only at the host notch
([below](#briefing-non-goals)). Their destination lines keep `into` and take no `agents`, and
content in an agent pack may name only that pack's own agent; `TestShippedAgentPacksKeepIntoForSkew`
holds both. The addressed line opens a version-skew window: an entrypoint older than the
audiences field (first released in v0.9.0) refuses it, and the boot fails. `just install` and a
release each publish the host `yolo` and the entrypoint together, so only a from-source launch
(`YOLO_REPO_ROOT`) can pair a newer host with such an entrypoint, and the source-skew gate
refuses that launch only when the checkout contains the binary's commit. Measured, the line
widens no window, since those older readers already refuse every launch that selects claude or pi
for other reasons: pi's own manifest, and the bedrock pack, which both agents `need`. Whether to
accept the window is not ruled ([DS-D33](../design/durable-scratch-space.md#DS-D33),
[OQ-D6](../design/slots-and-contributions.md#OQ-D6)).

**Two content contributions naming one source are refused**, naming both
([`OQ-PB5`](#oq-pb5)): the same cleaned `from`, or
both omitting it. Every legitimate shape already has a one-contribution spelling — one `agents`
list for several audiences, silence for all of them. The check is a sibling check
(`validateDuplicateContentSources`), so it runs on the strict decode, which is the launcher's and
`yolo pack lint`'s. The tolerant in-jail read folds a repeat instead, so each file is still
delivered once: the FIRST contribution governs, and when neither names an `into` their audiences
union, a broadcast absorbing any list.

**A declared source is the only source**
([P4 (briefing defaults)](#briefing-p4)). A `from` that is absent, a directory,
empty or whitespace-only delivers **nothing** from that contribution, and nothing else is read in
its place. The jail launch and `yolo host apply` both warn, naming the path, and the rest of the
pack set still composes. A `from` escaping the pack tree is refused outright. The old fallback chain,
`[from, AGENTS.md]`, is gone: it quietly delivered a different file than the one named.

One pack's files reach a destination as **one section**: its governed files, byte-wise by
pack-relative path, joined with one blank line, which is also the spacing between packs. A file
routed elsewhere is simply absent and does not split the section. `briefing_provenance: true`
heads each pack's section with one label, never one per file. The jail
(`jailcontent.ComposePackBriefings`) and the host (`entrypoint.ComposeHostBriefings`) produce the
same bytes, pinned against each other by
[`briefingparity_test.go`](../../internal/cli/run/briefingparity_test.go).

<a id="briefing-lint-listing"></a>**`yolo pack lint` lists every delivery before any launch**
([P6 (briefing defaults)](#briefing-p6)). Under a `delivers:` header it prints each `briefing`
file and `skills` tree the pack delivers and where it goes, from the same `GovernedSources` answer
the notches use (deliberately not from the footprint, which feeds the launch banners). An implicit
delivery reads `every agent (implicit broadcast)`, a `{"kind": "briefing"}` with no route reads
`every agent (declared broadcast)`, and a declared one names the `contributes[i]` line that routes
it. Lint takes one pack and no config, so it cannot resolve "every agent" into names; the resolved
list is `yolo host apply`'s and the launch banner's to print. It then names, one info line each,
every conventional-looking file it will **not** ship: a root `AGENTS.md`, `CLAUDE.md` or
`GEMINI.md` (the repository's own instructions — info, not a warning, because not shipping it is
correct), and a subdirectory or non-`*.md` file inside `briefing/`. Its advisory on a content
`into` that an agent pack already declares says that dropping the line **widens** delivery back to
the broadcast.

`yolo pack init` scaffolds `briefing/<pack>.md` — or `briefing/prose.md` when the directory's own
name is a reserved basename, so the scaffold never fails its own lint — and keeps its advice on
addressed contributions in the scaffold's `README.md`, which ships nowhere.

<a id="briefing-non-goals"></a>What the briefing rules deliberately do **not** do:

- **`files` does not broadcast.** Its destinations are typed slots, and "every agent" has no
  meaning for one, so a `files` contribution still names `into` or `agents`.
- **A filename never addresses.** `briefing/claude.md` reaches every agent like any other file;
  audience is a manifest field, never an implicit rule read from a name.
- **No per-file consumer toggle and no fetch-origin rule** ([`OQ-PB4`](#oq-pb4)). Selecting a pack
  is the consent for its prose, as for every other kind.
- **The severity of a mistyped `from`** stays a warning here; whether it should refuse belongs to
  [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md).

> [!NOTE]
> **A content `into` narrows at the host notch only**, for `briefing` and `skills` alike.
> `ResolveDestinations` sends a content `{"into": ".claude/CLAUDE.md"}` there and nowhere else, so
> a declaration is never widened into every other agent's directory. A jail carries no `into` for
> pack content and composes it into every destination, as a broadcast. `ResolveDestinations`
> records the asymmetry as deliberate. Name an audience with `agents` to narrow at both notches.

`after: "host:<path>"` prepends the user's own briefing at that host path ahead of the
composed content, so the user's own file still outranks the pack's. It is **jail-only**:
at the host notch the path it names IS the generated destination, so there is no
user-maintained file left to prepend.

> [!WARNING]
> **"The user's own" is CHECKED, not assumed.** Once a machine has run `yolo apply --at
> host`, that same path holds yolo's own composition — so the jail prepended every pack's
> prose and then composed the same packs again, and each pack arrived twice. The run pipeline
> asks `entrypoint.GeneratedHostBriefings`: ownership is proved from
> `host-briefing-manifest.json`, never inferred from content, and it fails OPEN so an absent
> record still prepends.

The first apply into a destination the user wrote is CONFIRMED, once, and fails closed on a
non-interactive stdin — taking wholesale ownership of a file the user wrote is a one-way
door. Their prose is MOVED into the conventional local pack, as
`~/.config/yolo-jail/local/briefing/local.md`, from which yolo composes it back into every
destination, so their instructions keep reaching their agents. To add personal prose, edit the
local pack, not the destination. Dropping the last contributing pack **archives** the
destination rather than leaving a generated file with no owner; nothing is ever deleted.

<a id="local-pack-briefing-move"></a>**The local pack's legacy `AGENTS.md` is moved by yolo,
because yolo chose that location.** An earlier yolo migrated adopted prose to the local pack's
root `AGENTS.md`, which is no longer read ([P1 (briefing defaults)](#briefing-p1)). The first step
of the `briefing` kind in `yolo host apply` (`entrypoint.MoveLegacyLocalPackBriefing`) moves it to
the same `briefing/local.md` the adoption writes — one name for both writers, so the two never
race into two files whose join order the user did not choose — and says so. Each case is chosen
so that nothing of the user's is lost or chosen between:

- **Nothing to move** — absent, a directory, or a dangling link: silent, since none of them ever
  delivered prose.
- **The target name is taken** — refused, naming both files. The one exception is the same file
  under both names, which is what an interrupted move leaves; that is finished.
- **A symlink** — refused, naming both files, since renaming it one directory deeper would break a
  relative link and rewriting the user's link is not yolo's to do.
- **The local `pack.json` still names `AGENTS.md` in a briefing `from`** — refused: governance
  never reads a reserved `from`, so the moved file would be named by no declaration and broadcast
  to every agent instead of the audience that `from` routed it to. Such a `from` is a manifest
  problem ([OQ-PB2](#oq-pb2)), so `yolo host apply` refuses the local pack before this step,
  with the launch's own words and nothing written
  ([NS-D14](../design/notch-scoped-config-contributions.md#10-decision-ledger)). The move's
  refusal, which names the edit to `briefing/local.md`, stays as the function's own guard.
- Otherwise it moves **without clobbering**: a hard link then an unlink, so a file created at the
  target between the check and the move is never replaced. Only a filesystem without hard links
  falls back to a rename, behind the earlier existence check. A dry run reports `would move` and
  writes nothing.

A refused move **stops the briefing kind before it composes**: composing without the local pack's
prose would regenerate every destination without the user's own instructions, and could archive a
destination the local pack was the only contributor to. A third-party pack's `AGENTS.md` is its
author's to move; nothing touches one.

#### `files`

An opaque tree the pack owns outright, bind-mounted `:ro` at `into` in the jail. `from` is
required and honored **on a contribution**: there is no conventional location for an opaque tree,
so the declaration is the only thing that can name it. The source bound is the pack's **staged**
tree, so `packstage`'s escaping-symlink refusal has already run on it — `files` is not a
channel around it.

`files` also carries the `agent`/`agents` pair, and it is the kind where the two spellings are
**two different declarations** rather than one field used two ways:

| Written | What it is | Fields |
| :--- | :--- | :--- |
| `{kind:"files", agent:"pi", into:".pi/agent/extensions"}` | a **slot** — where content addressed to `pi` lands | `agent` + `into`, and **no `from`** |
| `{kind:"files", agents:["pi"], from:"pi-extensions"}` | a **contribution** — this pack's tree, for whoever owns `pi` | `agents` + `from`, and no `into` |

A slot ships nothing, so it makes no mount and writes no file; a contribution addressed to it
lands in a **subdirectory of the slot named for the contributing pack** —
`.pi/agent/extensions/<pack>` — at **both notches**, through one resolver
([`packload.SlotLanding`](../../internal/packload/mergedest.go)). Two facts follow, and both are
the reason the layout is what it is: many packs can address one slot without a
sole-ownership collision, and nothing is ever delivered AT the slot root, where the owner's own
tree and every other contributor's live. A tree bound at the root with addressed trees nested
inside it is the nested-mount conflict `files` was reshaped to remove
([`OQ-4`](../design/pi-pack-extensions.md#10-decision-ledger)/[`OQ-6`](../design/pi-pack-extensions.md#10-decision-ledger)).

> [!WARNING]
> **One slot per agent, and one addressed tree per agent per pack; a second of either is a load
> error**, naming the first entry's index. Two SLOTS: the address a content pack writes is the agent
> NAME, so `{"agents":["pi"]}` names both and there is no answer to "which one". Two addressed
> TREES from one pack: the landing path carries the contributing pack, so both name one directory —
> which the jail cannot honor at all (podman refuses the duplicate mount) and the host would merge
> in silence. The remedy for the second is one `from` directory holding both trees. Two slots for
> two DIFFERENT agents, or two trees addressed to different agents, are fine.

The join lives in destination borrowing rather than in either notch's renderer, which is why the
host and the jail cannot drift apart again ([`filesslotparity_test.go`](../../internal/cli/run/filesslotparity_test.go)
pins the two against each other, and `pack lint`/`footprint` name the address and the
subdirectory in the claim's target).

Sole ownership is enforced before the container starts: two contributions claiming one
`into` are refused at launch, naming both packs, rather than reaching podman as a "duplicate
mount destination" error that names neither.

> [!WARNING]
> **`files` is deliberately NOT deduplicated the way `skills` and `briefing` are.** Those
> two emit one bind per contribution over content the assembler had *already* merged, so
> deduplicating per destination was the fix. A second `files` claimant on one path is a
> genuine sole-ownership violation, so it stays a pre-flight refusal naming both packs —
> deduping there would let one pack's content shadow another's. Same podman symptom,
> opposite correct response.

A `files` claim is a `:ro` mount over its whole destination, so a surface the entrypoint
must write beneath that path would hit a read-only filesystem. That is caught in pre-flight,
before the container exists, with the remedy (narrow the `into`) — it used to refuse the
boot with an error naming the *surface* rather than the claim that shadowed it, and usually
cross-pack, so neither author could see it.

On Apple Container a `files` contribution naming one FILE is copied into the jail home instead
of mounted. That is a choice, not a limitation: a single regular-file bind was measured to
arrive and honor `:ro` on `container` 1.1.0, and the copy is kept because it works on every
version with no version gate to get wrong
([`composed-file-permissions.md`](composed-file-permissions.md#why-0o444-is-not-a-posture),
`run.acMaterialize`). A directory is mounted on both backends. **At the HOST notch the tree is WRITTEN, not bound** — `yolo host apply` copies it
into the real `$HOME` against an ownership record shared with skill delivery: it writes only
paths that record says are yolo's, archives the previous copy into the kind's own archive
bucket before replacing one, and **fails closed** — a render that cannot prove ownership
refuses and touches nothing
([`applyhostfiles.go`](../../internal/cli/applyhostfiles.go),
[`render.HostFields`](../../internal/render/fieldset.go)). It used to be refused there, on the
grounds that a bind mount means nothing off-container — true of the mechanism and false of the
intent, since a pack that owns `~/.claude/file-suggestion.sh` means *this file is mine to
maintain*. That changed on 2026-08-02; **ownership does not carry over**, which is the one
asymmetry the host copy keeps.

#### `state`

A writable home subtree that persists. `scope` is `workspace` (the default, backed
per-workspace) or `machine` (backed by the global home, and **leaking across workspaces by
design**). `because` is **required** for `scope: machine`, so a cross-workspace leak is a
conscious, documented decision.

A machine-scope state claim is review-worthy in the footprint but is deliberately **not** on
the launch banner: it is a subtree of the JAIL's home that yolo owns, not a path on the host
the pack reads. `TestDisclosureCoversEveryReviewWorthyKind` names it as the deliberate
exclusion, so this reasoning has to be restated or refuted by anyone who changes it.

#### `reads-host` and `mount`

Two host-home reads. `reads-host` names one **file**, whose `/ctx` destination defaults to
the pack's staging slug plus the basename. `mount` names a directory (or a file) and picks an
arbitrary `/ctx` destination, for making a reference tree — a dataset, a shared prompt
library — visible in the jail. An absent source is skipped with a warning rather than failing
the jail: the user simply has not created it.

> [!IMPORTANT]
> **`reads-host` is no longer how a config surface gets its `host` layer.** It was, bound by
> a **basename match** between the grant and the surface path, until 2026-09-12; that half is
> a field on the surface now — `"readsHost": true` — and naming one of your own surfaces in a
> `reads-host` contribution is refused with the migration
> ([`OQ-CO10`](../design/config-ownership-and-promotion.md#13-decision-ledger)). What the kind still serves is the user's `host_files` key, whose entries carry
> arbitrary host files that have no mirrored twin in the jail and so genuinely need a
> declared path.

Both are Shared: many readers of one host file is fine. **Neither is origin-gated any
more** — see [the credential boundary](#the-credential-boundary-disclosure-not-consent).

A `mount` is the one host grant with no relocation available. `reads-host` and the
`host_files` config key materialize into a per-workspace directory on Apple Container
because their reader is the ENTRYPOINT, which can be redirected; a `mount`'s reader is the
AGENT, following the `/ctx` path its own briefing names, so there is nowhere else to put it.
Both forms are dropped with a reason on that backend.

#### `env`

Static environment variables set in the jail. Values are **literal strings** — no secrets,
no host references — so `env` never reads the host. The one interpolation is **`{listen}`**,
legal only in a contribution that declares **`served_by`**: it resolves to the address the
named loophole jail daemon serves at in this launch. That is the daemon's declared
`jail_daemon.listen` on a jail with its own network namespace, and a port the launch picked
on one that shares its launcher's (`network.mode: "host"`, or a nested jail). So a pointer
at a daemon writes the port once, in the daemon's manifest
([notch convergence §2.4](../plans/notch-convergence.md#24-the-addresses-those-secrets-protect-are-composed-not-literal)).
Decode refuses `{listen}` without `served_by`, and beside a `served_by` naming a service the
same pack declares, since a pack service has no listen address. A pointer served by another
pack's service is withheld at launch and named. A key two
packs both set collides. For values that must reference a secret or a host path, the user
config's `env_sources` is the channel, kept out of a distributable pack on purpose. `env` is
shown on the launch banner anyway, because it changes what the agent inside the jail sees,
which is the other thing a user checks a launch for.

An unconditional `env` reaches every process of the jail. A **gated** one reaches only the
agents whose selection satisfies its gate — the pack's own agent, or, for a pack that installs no
CLI, every agent whose selection does — through each agent's own env file, and no shell or other
agent sees it ([`providers.md`, the credential gate](providers.md#the-credential-gate)). The gate
is **`platform:`**, satisfied when the agent's selected provider declares that platform (what
`packs/aws-auth`'s pointer uses, for `aws-bedrock`), or **`profile:`**, satisfied by the profile's
name; one per contribution. A fact of the provider belongs on `platform:`, so a second profile
over the same provider gets it ([`providers.md`, the `profile` modifier](providers.md#the-profile-modifier)).

An `env` contribution may also declare **`overridden_by`**: what, delivered into the same jail,
makes a consumer ignore its variables — other variables (all of them delivered, unless one of an
`unless` list is too), or a `host_files` grant under a home path — each with a `because` the
refusal quotes. A launch delivering the contribution beside one of those is **refused**, fatally
and with no escape hatch, and `yolo check` predicts it as a FAIL. An entry marked
**`certain: false`** says the thing only MAY override the contribution. Beside one of those the
launch prints a warning and continues, and `yolo check` reports a WARN. No flag or variable
silences that warning. The pack owns the knowledge and core names no variable: `packs/aws-auth`
declares that a Bedrock bearer or a static AWS key pair beats its credentials pointer, and that
a `~/.aws` grant may
([`sso-backed-bedrock.md` OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8)). The schema and its
rules are `packdecl.EnvOverride`'s doc comment.

An `env` contribution whose variables point a client at a yolo jail daemon declares that daemon
as **`served_by`**: the loophole's name for a loophole's `jail_daemon`, or the service's name. The
variables are then delivered only where that daemon is served: in a container launch whose
payload includes it, in the macos-user guest or at the doorway that launch opens for it, and at
`yolo host` at the doorway it opens for its one agent, aws-auth's for an agent on a Bedrock
provider ([`host-notch-services.md` §4.8](../design/host-notch-services.md#48-yolo-host)). They
are left out, and the launch names them and why, wherever nothing serves the daemon, such as a
loophole left disabled or `yolo host env`, which runs no process. An address nothing serves is a
dead pointer, and on a shared loopback it
hands the client's request to whoever binds the port first
([notch convergence §2.4](../plans/notch-convergence.md#24-the-addresses-those-secrets-protect-are-composed-not-literal)).
`packs/codex` declares it on `CODEX_REFRESH_TOKEN_URL_OVERRIDE`, and `packs/aws-auth` on its
Bedrock pointer. `overridden_by` is not evaluated for a contribution the launch leaves out.

#### `hook`

A named request for a core-provided imperative behavior. The set is **closed**
(`packdecl.KnownHooks`, drift-pinned by `entrypoint.TestHookSetsAgree`): a pack requests a hook
by name and supplies its parameters; it cannot ship the hook's logic, which would put
arbitrary effect code in a fetched pack. New behavior means a new named hook in core, and the
bar is not *"a pack needs it"* but *"the thing it does is not one tool's"* — an agent-named hook
is ruled out ([`OQ-2`](../design/pi-pack-extensions.md#10-decision-ledger), 2026-09-19), which is
what retired `claude_plugins`. The parameters a hook REQUIRES are validated on the host
(`packdecl.hookRequiredFields`), so a declaration missing the path it acts on is a `yolo check`
refusal rather than a boot failure; a surplus field is ignored. ⚠ This paragraph claimed the
opposite of the first half until 2026-09-21 — that an unused parameter was an error — while the
`hook` case validated only the NAME.

The set can also SHRINK, and a removed name is not an unknown one: a retired hook is refused
with the migration that replaces it (`packdecl.RetiredHook`), because a name one of yolo's own
packs shipped is not a typo, and "unknown hook" would tell an author their declaration is wrong
and nothing about what to write instead.

The `shared_credentials` hook's contract is *"symlink this file into this machine-scoped
dir"*, and its rule is that **the shared side always wins**:

```
already the right symlink        → done
real file + EMPTY shared         → copy local into shared, then symlink
real file + POPULATED shared     → discard local, then symlink
real file + the copy FAILED      → local left in place, NOT symlinked
anything else                    → symlink
```

`shared_directory` is the same rule for a whole DIRECTORY — *"symlink this home subdirectory
at this machine-scoped dir"* — and the two share one implementation of that table
(`entrypoint.linkThroughShared`, parameterized by a payload shape) rather than a copy of it,
because the ORDER of those rows is the whole content of the rule and getting it wrong once
destroyed credentials. What differs is the payload: "empty" is *no entries* rather than *zero
bytes* (a directory's `Size()` is filesystem-defined and says nothing about what is in it), the
copy is a strict tree copy that reports every per-entry error, and a copy in progress is marked
inside the shared dir so an interrupted one is retried instead of read as the populated side
that wins. The discard in row three is priced differently too: a lost login needs a human, a
lost package store needs a re-install.

`unshare_directory` (`from`, `at`) is how a pack STOPS sharing a directory. A home that booted
under the old `shared_directory` hook holds `from` as a symlink to `at`; once the pack no longer
declares `at`, that directory is not mounted and the link dangles. The hook replaces exactly
that link, recognised by its target and never followed, with an empty real directory, so the
tool repopulates its own copy per workspace. A real directory, a link to anything else, or an
absent path are left alone, and the store the link pointed at is never touched. First used for
pi's git checkouts ([`pi-git-extension-caching.md`](../design/pi-git-extension-caching.md)).

The copy-if-empty branch is not a freshness rule — it is what makes a first login in a fresh
install survive. There is deliberately no freshness comparison in any schema: a
schema-specific merge is what made a generically-named hook claude-shaped, and the second
tool to consume the hook exposed the seam.

> [!WARNING]
> **A revoked or expired shared credential is sticky, by design.** If the shared file holds
> a dead credential and a jail logs in again, that fresh login is discarded at the next boot
> and the jail is back on the dead token. Re-login fixes it until the next boot, so the exit
> is to `rm` the shared file, not to change the rule. What must survive any change here is
> the metadata the credential file carries beside the token trio — a file carrying only the
> trio reads as *not logged in* to a current agent — which under "shared always wins"
> survives for free so long as the broker keeps preserving it on refresh.

#### `loophole`

A loophole MODULE the pack ships: `from` names a pack-relative directory holding a
`manifest.jsonc`. The contribution **points at** the module rather than inlining the
manifest, so the on-disk shape is the one a user loophole already has — one loader reads
every source, and an author can develop a loophole standalone and drop it into a pack
unchanged. `from` is required (the directory's basename *is* the loophole's name) and there
is no `into`: the host half runs on the host and the jail half is mounted at a path core
owns, so there is no destination for a pack to name, and one is refused by name rather than
accepted and ignored.

**The sharpest kind, and the only one whose claim is host code EXECUTION.** Four things
follow, and none is optional:

1. **Its claims come from outside `pack.json`**, so they are produced at the `packload`
   layer (`Pack.loopholeClaims`), not in `packdecl`, which has no pack root and no internal
   imports — a claim computed there could only be a bare `loophole <name>`, a line blind to
   the daemon it discloses. Same layer and same reason as a wrapped plugin's claims.
2. **The enumeration is TOTAL.** One separately-disclosed claim per crossing: the daemon
   argv (with `doctor_cmd` folded in — it is host execution too), one per intercept (which
   claims even with no daemon: it installs a CA every TLS client in the jail trusts), one per
   bind mount, one per **socket** bind as its own read-write host-IPC class (`:ro` is no
   boundary for an AF_UNIX socket), and one per device. `state_files` needs none — it stays
   inside yolo's own state tree.
3. **The claim string is the raw argv** — placeholders unexpanded, nothing elided, joined
   with `shquote.Join`. **The reason is INJECTIVITY:** two different argvs must never render
   to one claim. An ellipsis collapses two daemons onto one line; a bare space join collapses
   `["sh","-c","a b"]` and `["sh","-c","a","b"]` — the same failure arrived at by accident.
   Nothing ever execs the string (the spawn reads the argv list), and `{loophole_dir}` stays
   unexpanded so the line reads the same on every machine. The footprint's *Detail* and the
   banner's `Claim.DisclosureSentence` are **renderings** of that identity and may
   abbreviate; the record and the prose are deliberately different strings, and
   `TestDisclosureSentenceDistinguishesClaimsThatDiffer` pins the injectivity.
4. **Exclusive by NAME**, and every instance is review-worthy — which no other kind can say
   of all its instances.

The launch refuses a loophole-name collision outright: it is the fourth launch pre-flight,
and it is FATAL (`run.PackLoopholeNameConflicts` over `packload.LoopholeNameCollisions`).

> [!WARNING]
> **Do not re-add a reserved-loophole-name list.** It looks like the obvious hardening and it
> is the exact change that was backed out. Once `claude-oauth-broker` became a contribution
> of `packs/claude`, a reserved name and a pack-shipped name would be the same name — and
> because the collision pre-flight is fatal, the reservation would refuse every launch that
> selected the pack. The collision check is the mechanism that replaced it. One constant
> survives, `paths.BuiltinCgroupLoopholeName`.

`loophole` is refused at the **host** notch, and the reason is the inverse of the generic
one: a loophole is a host daemon whose only client is a container, so with no jail there is
no client, no `--add-host`, no `YOLO_JAIL_DAEMONS`, and nothing for its endpoint file to be
mounted into. Refused because its **counterparty** is missing, not its mechanism — which
also keeps "selecting this pack runs a daemon" a statement about launching a jail rather
than about applying a config.

#### `service`

A daemon with an endpoint file under `/run/yolo-services/`, and **no boundary crossing at
all**: no grant, no host read, no host execution. The machinery is loophole-shaped on
purpose — a supervised daemon, an endpoint file, a reachability witness — and the TRUST is
not. The grant-shaped fields are refused on the kind, so a service that wanted a host read
is unrepresentable, and the footprint marks it never review-worthy for that reason rather
than as an omission. A daemon that DOES cross is a `loophole` declaration with the
per-crossing review that kind carries. What a service runs is in-jail, and its claim Detail
says what, so a reader sees the argv without opening the manifest.

It carries a [`platforms`](#program) list too — the same field and the same grammar,
answering the same question for the daemon that `program` answers for the vendor's build.
The service half is **declared and carried**: nothing reads it yet, so a service is started
on every platform whatever it says.

#### `adapter`

A **protocol conversion served at an address**: `adapts: {from, to}` plus the `address` that
speaks `to`. It says nothing about who runs it, and that separation is the rule — a remote
gateway, a proxy the user already runs, and a daemon a pack ships all declare the same thing. A
pack that does run its adapter declares the daemon separately, as a `service` or a `loophole`,
which is where that crossing is reviewed; `packs/wire-bridge` declares both. Exclusive by the
pair, never by pack, so one pack declaring two pairs is ordinary and two packs both claiming one
conversion collide. Not review-worthy: an address is the same class of fact a provider endpoint
is. How core pairs an agent's `protocols` with a provider's endpoints through an adapter is
[`protocol-resolution.md`](protocol-resolution.md)'s, and nothing here restates it.

#### `blocked-tool`

Refuses a tool inside the jail, printing a message and an alternative instead of running it.
Exclusive, because the blocker is a FILE at `~/.yolo/bin/block/<bin>` — the same reason
`program` is exclusive. Never review-worthy: a blocker writes a refusing shim INSIDE the
jail and crosses nothing. A fetched pack blocking a tool can make a jail less useful; it
cannot make it less contained.

**A pack concern, not a core one.** Core used to block `grep -r` and `find` by default — a
default that silently assumed the image bakes `rg` and `fd`, true of the container backends
and false of `macos-user`, where nothing is baked and the suggestion named a binary that did
not exist. Moving the list into a pack makes the assumption explicit: the pack that blocks a
tool is the pack that can say what replaces it, and selecting it is the opt-in. A blocker is
generated **only when its declared replacement is on the agent's PATH** (`agentPath` is the
one authority for which PATH counts), so a block can never leave a jail with neither the
tool nor its alternative. A generated shim is unconditional at run time unless
`YOLO_BYPASS_SHIMS=1`. A user's own `security.blocked_tools` entry naming the same tool
REPLACES the pack's whole.

A tool that is both blocked and pack-declared gets one of each, and the blocker wins by
position — the two generated script dirs are adjacent at the head of PATH, and their order
relative to each other is what carries the meaning. They share one bind-mount anchor at
`~/.yolo/bin`, so both are cleared contents-only. **Nothing may put that shared parent on
PATH**, or a launcher would be reachable from the blockers' position.

#### `intercept`

Routes a command NAME in the jail to a forwarder the pack declares:
`{"kind": "intercept", "bin": "gh", "forward": ["yolo", "gh", "--"]}` writes
`~/.yolo/bin/block/gh` as `exec yolo gh -- "$@"`. It is how a pack layers permissions over an
existing CLI ([`boundary-broker.md` OQ-BB8](../design/boundary-broker.md#OQ-BB8)); `packs/github`
is the first user. It lives in the BLOCK dir because that is the directory whose job is to
intercept a name ahead of everything installed, including a program the image bakes at `/bin`,
for which `launchercollision.go` writes no launcher. `forward[0]` is a bare program name on the
jail's PATH and never `bin` itself. `YOLO_BYPASS_SHIMS=1` makes the shim exec the program the
name resolves to behind the block dir. Exclusive by bin, for `blocked-tool`'s reason, and a
blocked-tool entry for the same name wins, with a warning at boot, since a refusal is the more
specific statement. Not review-worthy: the shim is in the jail, and what the forwarder reaches is
reviewed where it is declared. It does not apply at the host notch.

#### `autonomy`

A pack's two permission **postures**: `autonomous` (permission prompts bypassed) and
`guarded` (prompts on). The notch's confinement profile picks one through its
`AgentAutonomy` bit (`render.ProfileFor`): autonomous at `jail`, `guest` and preview, guarded
at `host` and at an unset target. No manifest names a notch ([§6c](#batch-6c)). Either
posture may be absent. A posture has three halves, and each reaches a different distance:

| Half | What it does | Which surfaces |
| :--- | :--- | :--- |
| `config` | On the declaring pack's own surface: keys folded into its `managed` layer (`packload.foldPostureManaged`). On another pack's surface: a **posture overlay**, keys contributed as a [`config-overlay`](#overlay-rules) does | The declaring pack's own, and any selected pack's through the `config-overlay` path |
| `launch` | Flags for one binary. A posture is the only place a launch flag can be declared | — |
| `lists` | **Posture lists**: `config-list` bodies appended only while this posture is selected | Any selected pack's, through the [`config-list`](#adding-entries-to-an-array-config-list) path |

A **posture list** *(a term coined by
[the design](../design/notch-scoped-config-contributions.md#41-recommended-posture-lists-inside-autonomy))*
is how a pack declares an entry for one side of the confinement line. The motivating case is
a permission gate for pi that belongs on the host and would only cost tokens and prompts in a
jail:

```json
{
  "kind": "autonomy",
  "guarded": {
    "lists": [
      {"surface": "pi/settings", "path": "/packages",
       "add": ["npm:@czottmann/pi-automode@1.17.0"]}
    ]
  }
}
```

- **Validation.** Each entry takes `surface`, `path` and `add` under `config-list`'s rules
  (`postureListProblems`, which shares `configListBodyProblems` with the kind). A posture
  holding only `lists` is valid. The host's strict decoder refuses an unknown field.
- **Collection.** `packoverlay.Collect` decodes both postures' lists at every notch, so a
  malformed entry is reported wherever it would or would not render. It then skips the posture
  the notch does not select: no problem, no orphan, no applied line. A selected posture list
  whose surface has no owner is inert and reported, led by `autonomy`. Order is the
  [config-list order](#config-list-order), with a posture's lists at the `autonomy`
  contribution's position.
- **Rendering.** Once placed, a posture list is a list contribution from its pack: the same
  fold, the same per-entry capture, the same `rmw` insert record and the same `config-list:<pack>`
  source label.
- **Disclosure.** `yolo pack footprint` names each posture's lists in the pack's `autonomy`
  claim, with the posture and the `agent/name#<pointer>` target. `yolo host apply`'s notch line
  counts a posture list as a fold only when it lands in a surface; an ownerless one is reported
  as having no effect and folds nothing. `yolo config render` and `yolo config ls` show it at
  `--at host` and not at `--at jail`, because each passes its notch's bit.
- **It stays out of jails through the host file too.** Once `yolo host apply --assert` has
  written a `readsHost` surface, every backend's launcher labels the host copy a render, and
  the jail keeps it as a baseline rather than composing it
  ([`OQ-CR6`](config-target-resolution.md#oq-cr6)). So the host-only entry never re-enters a
  jail as "the user's".
- **Skew fails closed.** An entrypoint older than the field drops `lists` (`DecodeTolerant`
  ignores an unknown nested field), so the entry renders nowhere rather than everywhere. Install
  the host yolo before a pack uses the field, since the host refuses the manifest otherwise.

A **posture overlay** *(a term coined by
[the design's OQ-3 build](../design/notch-scoped-config-contributions.md#10-decision-ledger), NS-D19)*
is a posture `config` entry naming a surface its own pack does not declare. It is how a pack
sets another pack's setting for one side of the confinement line, such as a scalar only the
host gets:

```json
{
  "kind": "autonomy",
  "guarded": {
    "config": [
      {"agent": "pi", "name": "settings", "codec": "json",
       "path": "~/.pi/agent/settings.json", "managed": {"someSetting": "host-only"}}
    ]
  }
}
```

- **Shape.** The same surface entry as every posture patch: `agent` and `name` identify the
  target, `path` and `codec` are required and must be the owner's, and `managed` carries the
  keys. An entry on the pack's own surface keeps folding into its `managed` layer; which of
  the two an entry is depends only on whether the declaring pack declares its identity.
- **Validation.** It contributes keys, so [`config-overlay`'s refusals](#overlay-rules) apply:
  `defaults`, `mode`, `retireOnFirstRender` and `readsHost` are refused, and an empty
  `managed` is refused (`manifest.DecodePostureOverlay`). Both postures are decoded at every
  notch, so a malformed entry is reported wherever it would or would not render, led by
  `autonomy <posture>.config`. A `path` or `codec` different from the owner's is refused at a
  notch that would place it.
- **Collection.** `packoverlay.Collect` places it in the overlay pass, gated on the posture
  after the decode and before the owner check: an unselected posture is a clean skip, and a
  selected one whose surface has no owner is inert and reported, led by `autonomy`. Order is
  config-overlay's, pack order then declaration order, with a posture's entries at the
  `autonomy` contribution's position, and later wins.
- **Rendering.** Once placed it is a config-overlay layer from its pack: the one slot below
  the owner's `managed` (the owner still wins a conflict), `config-overlay:<pack>` provenance,
  the boot's "config-overlay keys from" line, and `yolo config diff`, `yolo config ls` and
  `yolo config render --at host|jail` as for any overlay.
- **At the host.** `yolo host apply --assert` writes it into the real file and records it in
  the provenance record. Once the posture stops selecting it, it leaves the way every
  overlay key does: `host_management: own` regenerates the file without it, and under
  `assert` it is recorded `retired:config-overlay:<pack>` and `yolo host apply --revert`
  removes it. [Dropping the pack](#retiring-a-dropped-packs-host-output) removes it with the
  pack's other overlay keys.
- **Disclosure.** `yolo pack footprint` names it in the pack's `autonomy` claim as
  `<posture> contributes keys to <agent/name> (owner still wins)`. `yolo host apply`'s notch
  line counts it as a fold only when it lands in a surface.
- **Skew fails closed.** A yolo older than the posture overlay folds such an entry nowhere, so
  the key renders at no notch rather than at every one.

Use a posture list for an entry in an array and a posture overlay for a key: a key in
`managed` replaces an array whole.

A pack declares at most one `autonomy` contribution, with both postures inside it.
`yolo pack lint`, `yolo check` and the launch refuse a second one, and a jail's read skips it
with a warning, so no reader there sees postures that do not render. `autonomy` never collides
across packs: a posture overlay folds at config-overlay's slot, where later wins by pack
order as it does for every overlay, and a list only appends.

#### `provider` and `profile`

A `provider` declares a service's facts — endpoints by protocol, wire protocol, model
aliases, the *name* of the environment variable holding the credential (or a list of names,
for a credential that arrives in several variables; the credential gate delivers each only to
an agent that selected the provider), and the options a profile may tune, and what service it is (`platform`, open vocabulary). A
`profile` is a selection and nothing else: `{name, provider}`. Everything a profile used to carry
as a body is now an ordinary contribution: in an agent's own derive keyed on the provider, on
`env` gated by `platform:`, or gated by the `profile:` modifier, which is accepted on `env` and
`config-overlay` and refused by name on every other kind. A `program` may declare
`platform_switches`, the settings in its own config that put it on a platform by themselves.

[`providers.md`](providers.md) is the authority for all of it: the canonical `wire_api`
vocabulary, the composition of the provider table, the credential pre-flight, selection, and
per-agent delivery. Nothing here restates it.

## `needs` — conditional pack dependency

A pack declares that ANOTHER pack belongs in the launch when a condition on the selected set
holds — `needs: [{pack, when_bins}]`. It delivers nothing and owns no target: its effect is
to EXTEND SELECTION, and its conflict rule is "already present = no-op", a rule about the
selected SET that no per-target combine rule can state. That is why it is a top-level key.

`internal/packdecl/needs.go` refuses only version-invariant structure (an empty pack name, a
name carrying `=`, an empty bin entry). The two facts about the pack UNIVERSE are checked
where the universe is known, at `packload.ResolveNeeds`. Four rules govern it, and
[`wire-bridge.md`](wire-bridge.md)
[`wire-bridge.md`](wire-bridge.md#kind-service--the-vocabulary-it-landed-as) is the authority:

- **The named pack must be embedded** (WB-D9). A fetched pack needs-ing another fetched pack
  would make selection itself a supply-chain channel; refusing keeps `packs:` the only place
  unreviewed code enters a launch.
- **Resolution is a transitive closure run at selection, BEFORE staging** (WB-D10), because
  the mount is the filter. Explicit user selection is joined, never overridden.
- **The auto-inclusion prints** — on the launch banner and in `yolo check`. A silent join is
  the one forbidden behavior (WB-D12).
- **User config carries no `needs` key** (WB-D11). Manifests only.

## The one-writer rule and the neutral assembler

Principle 1 restated concretely. There are two ways a file reaches the jail:

- **Sole ownership.** `files`, `program`, and a pack's own `skills`/`briefing` tree — the
  pack owns the target, and two packs claiming one path is an error before anything runs.

- **Shared production via a neutral owner.** When two packs affect one file, no pack writes
  it. Each emits typed contributions and a **core-owned assembler** consumes all of them and
  writes the file into a staging tree it wholly owns; the staging tree is mounted read-only
  into the jail. The compose engine is that owner for config; the stage-then-`:ro`-mount
  pattern is it for skills and briefings.

```
   packs' typed contributions                 core assembler                 the jail
  ┌────────────────────────────┐          (the ONLY writer)            ┌──────────────────┐
  │ claude:  config inputs ─────┼──┐                                   │                  │
  │ house-rules: config overlay─┼──┼──►  compose ──► staging/ ──:ro──► │ ~/.claude/       │
  │ claude:  skills tree ───────┼──┼──►  merge   ──► staging/ ──:ro──► │   settings.json  │
  │ house-rules: skills tree ───┼──┘                  │                │   skills/        │
  │ claude:  briefing prose ────┼─────►  concat  ─────┘  (collision-   │   CLAUDE.md      │
  │ house-rules: briefing ──────┼─────►             checked, tracked)  │                  │
  └────────────────────────────┘                                      └──────────────────┘
        declare inputs                  one module writes every file      never written in place
```

Three properties fall out: no file is ever written twice (a "collision" is detected among
inputs, never raced on disk); provenance is free (one module writing from declared inputs
can say which contribution produced which key); and the staging layer is a safety boundary
(a bad contribution corrupts a throwaway tree, never the live home).

## Config surfaces and the compose engine

A `config` contribution declares one or more **surfaces**. A **surface** is one generated
file plus its layer data, identified by `(agent, name)` — that identity is what yolo keys
on internally, and `path` is where it lands. Two packs using one identity are talking about
the same file.

`agentcfg.BuiltinManifest()` is **core's own surfaces and nothing else** (`mise/config`
today); every other surface, agent and non-agent alike, lives in a pack's `pack.json`, and
callers wanting the full set merge pack surfaces via `ManifestWith`. The surface schema is
validated by the config engine and its fields are documented in `internal/agentcfg/manifest`
— `codec` is the decode/encode round-trip, `defaults` is yolo's freely-overridable base
layer, `managed` is the keys yolo asserts and always wins, `retireOnFirstRender` names stale
sidecars to clean up, and `readsHost` declares that the user's own copy of this same file,
from their real home, is composed as the `host` layer. Three fields decide whether and beside
what the file is written: `retireIfMatchesRender` names a sibling to delete only while it holds
exactly this surface's render, the one way to retire a file name an agent or a user also
writes; `whenListed` renders the surface only while a list in an earlier surface of the same
pack holds a matching entry, which is how a file for one extension follows the agent's own
record of it; and `notAtHost` gives the reason `yolo host apply` skips the surface. The pi
pack's MCP files use all three ([Pi's MCP files](mcp-configuration.md#pis-mcp-files)).

The engine composes a surface by folding layers with RFC-7386 merge semantics, lowest to
highest precedence, and then applies [the managed floor](#the-managed-floor):

```
defaults < host < workspace < config-overlay < config-list < capture-overlay < list-capture < computed(derive) < managed
```

- **`host`** is declared by the surface (`"readsHost": true`) and its `/ctx` mount path is
  derived from the surface's own `path`, which is how an agent's own host-side settings
  compose into the jail with no second declaration and no second path to keep in step. The
  read **fails closed**: the launcher reports what it delivered, and a host file the launch
  says it delivered that the jail cannot read refuses the boot rather than composing the
  file without the user's settings. The states that are not a delivery fault compose without it
  and refuse nothing: the user has no such file, the launch carried no host layers, the launcher
  is older than the report, or — since 2026-09-18 — the bytes are LABELLED A RENDER, meaning yolo
  composed them itself and they are a baseline rather than a layer
  ([`config-target-resolution.md`](config-target-resolution.md#the-host-layer--a-staged-copy-a-baseline-or-nothing)).
  ⚠ **`macos-user` is no longer the example of the second one.** DP-L1 gave that backend a real
  mechanism, so it reports `unsupported` only when it actually composed none, and
  [`internal/packload/hostlayer.go`](../../internal/packload/hostlayer.go) carries its own warning
  that no backend emits `unsupported` unconditionally any more. What that backend still does not
  emit is the render LABEL, so a managed home's host file composes there as a layer.
- **`config-overlay`** carries the keys OTHER packs contribute to a surface this one owns, in
  the one pack order (later wins): `packs`-list order, then the packs a `needs` pulled in, then
  the local pack, at every notch ([OQ-NC4](../plans/notch-convergence.md#OQ-NC4)). Below
  `managed`, so the owner still wins a genuine conflict.
- **`config-list`** is not a merge-patch layer. It appends entries to one array each, after
  every `config-overlay` ([below](#adding-entries-to-an-array-config-list)).
- **`capture-overlay`** carries a user's in-jail edits across regeneration, for `stateful`
  surfaces.
- **`list-capture`** is the per-entry half of that capture, and exists only at a path a
  `config-list` targets. It removes the entries an in-jail edit removed, then appends the ones
  it added. The capture overlay never records such a path.
- **`computed`** is the per-boot dynamic layer produced by [`derive`](#the-derive-slot); a
  null value there is an RFC-7386 tombstone that deletes the key.
- **`managed`** is the floor yolo always wins. It is not one of the folded layers: it is
  re-asserted over the fold's result as the pipeline's last step.

`${workspace}` in a map key is substituted with the container workspace path.

### The managed floor

<a id="lt-p3"></a>The **managed floor** is the step that re-asserts a surface's `managed` layer over
everything the fold produced, so yolo's non-negotiable keys win the rendered file whatever any
layer below said. It belongs to the compose pipeline, in `internal/agentcfg` (`enforceManaged`, and
`enforceManagedTOML` for a `toml` surface), not to any VM — P3 (transform removal): nothing about
it is Lua, and it used to live as a method on the transform's Lua context, which is how a
disconnected part hides. It reads the surface's own declared `managed` layer directly, so nothing
the fold did can move it.

Its semantics differ from the fold's merge on purpose:

- <a id="lt-r1"></a>**For JSON, a managed `null` is ASSIGNED, not deleted.** The fold is RFC 7386,
  where a null under a key deletes the key. The floor is not: a managed null means "this key
  renders as null", not "drop whatever the host set". For **TOML**, which has no literal null, a
  managed null deletes that key, since assigning it would produce a file the encoder must refuse.
  Pinned by `TestEnforceManagedNilIsAssignedNotDeleted`, `TestComposeManagedNilValueIsAssignedNotDeleted`
  and `TestComposeTOMLManagedNullDeletesOnlyItsKey` (R1 (transform removal): the floor must not
  change meaning when it moves).
- **Deep for objects.** A managed object merges key by key into the composed object, so a host
  `permissions.ask` survives beside a managed `permissions.allow`. A managed scalar or array
  replaces, deep-copied, so the result never shares structure with the managed layer.
- <a id="lt-r3"></a>**Whole-value for a keyless surface.** On a `raw` or `lines` surface a non-nil
  managed layer replaces the entire rendered value — `managed` there means "this file is exactly
  these bytes". Pinned without any script by `TestEnforceManagedKeylessReplacesWholeValue` and
  `TestComposeRawManagedReplacesWholeFile` (R3 (transform removal): the keyless floor's only proof
  once ran through a transform script).
- **In place.** When both sides are objects, the composed map is mutated and the same map
  returned; a copying rewrite would change aliasing for a caller still holding the merged map.
  Pinned by `TestEnforceManagedMutatesConfigInPlace`.

> [!WARNING]
> **Do not reuse the fold's `mergeValue` for the floor, however tidy one merge looks.** The two are
> one `if` apart on a null value and read as interchangeable; unifying them silently turns a
> managed null from "render null" into "delete the host's key".

### The four modes

`mode` says how the file is maintained across boots, and the set is closed: `stateful` (the
default — compose from layers *and* capture in-jail edits back into the overlay), `computed`
(compose and overwrite every boot, discarding in-jail edits), `rmw` (read-modify-write an
agent-owned file: merge yolo's managed keys into whatever the agent wrote, no capture sidecars), and
`unrendered` (declared but never written).

`rmw` is the mode for a file the agent itself owns and mutates at runtime. yolo regenerates
only the keys it manages and leaves the rest alone; where it owns a top-level key it
regenerates that key wholesale each boot, and a server a UI adds at that scope is
overwritten with a boot-time drop notice. **yolo does not reconcile; it regenerates.** The one
exception is a [`config-list`](#adding-entries-to-an-array-config-list) path, where yolo keeps
a record of the entries it inserted so that it can remove them later without touching the user's.

An **`rmw` surface has no layer fold** — it merges keys into a file the agent owns — so it
expresses the same precedence by write order instead: overlays are asserted first, then the
derived tables, then `managed`, then `defaults` fill only where absent, then `config-list`
entries, skipping any path `managed` or a derived table holds. Same outcome, one mechanism
short. The lists go after the defaults fill for a reason. If they went first, a list would create
a key the file lacked, the fill would then find it present and skip it, and the default
array's own entries would be lost. `Compose` keeps them.

**Comments survive an `rmw` render, and the mode is why.** "Preserve everything yolo does not
declare" covers the prose as well as the keys, so on a `toml` surface a comment is put back
beside the key it explains — with one exception, which is a rule rather than a limitation: a
comment above a key the render CHANGES is dropped, because a stale `# pinned to …` sitting
above a new value misleads worse than no comment at all. Every such drop is named in `yolo
host apply`'s output. A `json` surface has nothing to preserve — strict JSON has no comment
syntax, so a commented file never decodes and `rmw` refuses it untouched. The other two
composing modes are unchanged for different reasons: `computed` is a file yolo solely
authors, so there is no user comment in it; `stateful` composes from many layers, which makes
preserving a comment a PROJECTION out of the `host` layer rather than an in-place edit.

### When two packs want one config file

A second pack contributing to a surface writes `config-overlay`, naming the target by
identity and carrying only keys. The owning pack stays the sole *owner* of the file; the
contributor is explicitly a *contributor*, with per-key provenance so an override is legible.

> [!WARNING]
> **Do not declare the same surface identity from a second pack, and do not "fix" the
> collision by deep-merging two declarations.** Declaring it twice is refused (below) because
> the merge used to resolve one identity last-writer-wins, WHOLE: the survivor brought its
> own `mode`, `path`, `codec` and `defaults`, so a pack could flip another pack's surface
> from `stateful` to `rmw` and silently disable in-jail edit capture for a file it did not
> own. Deep-merging instead answers "whose `managed` keys win" but not "whose `mode` wins" —
> and `mode` is not mergeable, so one declaration still has to lose. It also erases the
> ownership distinction that makes provenance possible: under a merge, "which pack set this
> key" becomes unanswerable. A pack CAN avoid the flip by matching the owner's `mode`, and
> that is politeness by the author, **not** a fix: the hazard belongs to the mechanism, so
> only the refusal closes it (R1).

`packload.ConfigSurfaceCollisions` is the single detector, consumed at three refusal sites:
`packload.Collisions` (so `yolo pack footprint` and `yolo check` report it), `stagePacks`
(so a launch is refused before the container exists), and `yolo host apply` (so nothing is
written into a real home). It is **its own pass**, not a row in the generic exclusive loop,
for two structural reasons: a pack can commit this against ITSELF (two declarations inside
one manifest are just as silent, and the generic loop correctly skips a single-pack group),
and the REMEDY is a different KIND rather than a different target — "give them different
paths" is right for `files` and useless here, because two packs wanting keys in one config
file is a legitimate intent with a correct expression, so the message has to teach the
conversion.

**The refusal names the DIVERGENCE, not just the rule.** Two packs agreeing on everything
but `managed` are still refused, but where the declarations disagree on `mode`/`path`/`codec`
the message says which field and which two values. That is the concrete damage, and a reader
who sees it does not have to take the rule on faith. In a SELF-collision both sides would
carry the same pack name, so there the labels are `declaration 1` / `declaration 2`.

`yolo pack lint` and `yolo pack footprint <dir>` hold ONE pack by construction, so they
cannot see a cross-pack collision — and the most likely one, a surface a pack yolo ships
already owns, is exactly the case an author hits. Both single-pack views therefore compare
against the (not selection-gated) embedded set and **warn** by name, with the
`config-overlay` shape to copy. A warning rather than a lint failure: whether two packs are
ever selected together is a config question these commands cannot answer, so the refusal
stays where the pack set is known.

Two things the exclusivity refusal is deliberately NOT. An `autonomy` posture patching a
surface the same pack owns is not a second declaration — it merges into the base surface's
managed layer, which is what keeps the agent packs launchable. And a `config-overlay`
alongside a `config` on one identity is the supported shape, not a clash.

### Overlay rules

- **An overlay body may set ONLY `managed`.** Every field that would redefine the *surface*
  (`agent`, `name`, `path`, `codec`, `mode`, `defaults`, `retireOnFirstRender`, `readsHost`,
  `retireIfMatchesRender`, `whenListed`, `notAtHost`)
  is refused BY NAME at decode, with the rule in the message rather than a generic
  unknown-field error — each of those keys is real, it is just not a contributor's to set.
  That refusal is what makes "the contributor cannot change the file's mode, path or codec"
  mechanical instead of conventional; without it the `mode` flip comes straight back through
  the correct syntax. `defaults` is refused for a subtler reason worth stating: an overlay
  folds at exactly ONE precedence slot, so a contributor has no second, lower position to
  occupy.
- **A malformed overlay is FATAL; an ownerless one is not.** An unselected owner is not the
  author's mistake — there is nothing to fix — whereas a body that redeclares the surface is
  the author asserting something the mechanism will never honor.
- **If the target surface has no owner** among the selected packs, the overlay is **inert and
  reported by name** (R2). It neither creates the file — that would let an overlay own a
  surface by accident, the exact distinction the kind exists to draw — nor fails the launch:

  ```
  config-overlay  no effect — claude/settings has no owner (the `claude` pack is not selected)
  ```

  It also fails in the useful direction: add the owning pack later and the overlay starts
  working with no further edit.
- **An overlay onto a CORE surface is inert too, with its own message.** The kind contributes
  to a surface *another pack* owns, and core's surfaces belong to no pack — so it is reported
  as core-owned rather than as an unknown identity, and the message does not send a user
  hunting a typo that is not there.
- **`rmw` asserts an overlay's keys rather than defaulting them.** An overlay body says
  `managed` — "keep this key at this value" — so fill-if-absent would make it work on the
  first boot and then never pick up a changed value.

### Adding entries to an array: `config-list`

A `config-overlay` is a merge patch, so an array in it replaces the owner's array whole. A pack
that wants to add one package to pi's `packages` would have to copy every package the owner
lists, and that copy drifts. A **list contribution** *(a term coined for this kind)* is a pack's
request to append JSON values to one array inside a config surface some selected pack owns. It is
a separate operation, not a merge patch, and it cannot create a surface: it names its target by
the surface's identity and a path inside it, never by a file path of its own choosing. Core
knows paths and arrays, not any agent's entry syntax: to it, a pi package spec is an opaque
JSON value. The motivating case, one package added to pi's list without copying it:

```json
{
  "kind": "config-list",
  "surface": "pi/settings",
  "path": "/packages",
  "add": ["git:github.com/mschulkind/kilo-pi-provider"]
}
```

- **`path` is an [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) JSON Pointer**, not a dotted
  path, because real keys contain dots (model ids, server names). Inside a key, `~1` stands for
  `/` and `~0` for `~`. The root (`""`) and an empty step (`/a//b`) are refused. Every step
  names an object key; nothing indexes into an array.
- **`add` is required and must be an array.** `[]` is a no-op. A `null` is refused anywhere
  inside an entry, because a TOML surface cannot encode one.
- **Refused fields.** `config` and `profile` are refused on this kind, and `path` and `add` are
  refused on every other kind (`internal/packdecl`, `configListProblems`). A malformed
  declaration fails the manifest's validation, so it never reaches a launch.
- **The same body can sit inside a posture.** An `autonomy` posture's `lists` take these three
  fields under these rules and contribute only while the notch selects that posture — the one
  way to make an entry host-only or jail-only ([posture lists](#autonomy)). A top-level
  `config-list` has no gate and contributes at every notch.

<a id="config-list-fold"></a>**How it folds** (`internal/agentcfg/listcontrib.go`):

1. <a id="config-list-order"></a>**Order.** After every ordinary layer and every
   `config-overlay`, contributions apply in pack order, then declaration order. Existing entries
   keep their order, including any duplicates the lower layers already hold, and the first
   occurrence of each contributed entry not already present is appended. So a later list
   contribution can re-add an entry an earlier overlay's replacement dropped
   ([OQ-AL2](#oq-al2)).
2. <a id="config-list-equality"></a>**Equality.** Entries are compared as whole JSON values after
   normalizing number types, so a pack.json `1` (a float) equals a TOML file's `1` (an integer).
   Nothing is parsed, normalized or sorted. The same entry from two packs is written once, and
   is not an error.
3. <a id="config-list-type-conflict"></a>**Type conflicts.** A missing array starts empty and
   missing parents are created. A non-array at the path, or a non-object parent, refuses the
   surface's render and names the surface, the path and the pack; the conflicting value is never
   overwritten and never skipped. For `stateful` and `computed` that is a failed boot. For `rmw`
   it is a refusal that warns and leaves the agent's file untouched.
4. <a id="config-list-precedence"></a>**Precedence.** The capture overlay, `computed` and
   `managed` can still replace or delete the assembled array. A list contribution is not a
   mandatory-entry policy and cannot defeat those layers. A pack that wants the array replaced
   uses `config-overlay`, whose arrays still replace.
5. <a id="config-list-label"></a>**Provenance label.** A top-level key that only list
   contributions created carries the provenance label `config-list`. That label is never treated
   as asserted, since the key may hold the user's entries too, so no retirement or revert removes
   the whole array on its strength.

**No owner, or no file.** A list contribution whose target has no owner is inert and reported the
way an ownerless overlay is, led by `config-list` instead of `config-overlay`. One aimed at an
`unrendered` surface is inert and warned. A keyless surface (`raw`, `lines`) has no keys to point
into, so a contribution there is refused (`agentcfg.ListCaptureRefusal`).

<a id="config-list-capture"></a>**Capture per entry.** A surface that reads the agent's own
edits back must not freeze the contributed entries into a whole-array capture. If it did, the
first `pi install` would capture every pack's entries too, mask later additions and keep a
dropped pack's entries forever. So at every **list path** — a path some live contribution
targets, or one the surface's list record already names — each mechanism records entries instead
([OQ-AL1](#oq-al1)):

| Mechanism | What it records | Where |
| :--- | :--- | :--- |
| `computed` | Nothing. The array is a function of its inputs, so a dropped pack's entries vanish on the next render. | — |
| `stateful` | Per path, the entries an in-jail edit **added** and **removed** relative to the last render. The capture overlay never records a list path. | `<agent>-<name>.list-capture.json`, beside the overlay sidecar |
| `rmw` | Per path, the entries yolo **inserted** and the inserted entries the user later removed from an array the file still holds (**declined**). A declined entry is never re-inserted; deleting the key or the whole file declines nothing, and neither does a render in which `managed` or a `computed` table held the path (the record is marked suspended, so the next render re-inserts instead of declining). An entry already in the file that yolo did not insert is the user's and is never removed. Revert removes only inserted entries. | `<agent>-<name>.list-record.json`, under the provenance directory, since the host under `assert` has no capture store |

`ListCaptureRefusal` is keyed on the **resolved** mechanism, not the declared mode: a `stateful`
surface at the host under `assert` renders through `rmw`, and `rmw`'s record decides. A new
mechanism starts refused, so whoever adds one has to say how it captures a list path. The
refusal fails a jail boot and is a `refused: config-list …` row in `yolo host apply`.

On a `stateful` surface, deleting the key or replacing the array with a non-array is still a
**whole-value** capture. It masks every contribution at that path, and the boot says so and names
`yolo config reset`. When an array comes back there, the capture ends and the array is measured
against the list the capture was hiding, so the next boot renders what the user wrote. An array
emptied in-jail is a per-entry capture instead: every entry then present stays removed, but an
entry first contributed later still appears. A capture by presence cannot keep a **reorder** or a
de-duplication of the entries: the rendered order is restored, and the boot says so. The state
machine is in
[`config-migration-to-prism.md`](config-migration-to-prism.md#list-paths-capture-per-entry).

> [!WARNING]
> **Every capture must read and write the list capture, including the ones that compose with no
> layers.** `yolo config capture` and the host-side capture-on-terminate (`captureSurfaceAt` in
> `internal/cli`) compose with no pack contributions at all, so the list-capture file is the
> only place they learn a surface's list paths. A capture that skipped it would record the whole
> array into the overlay, and the freeze [OQ-AL1](#oq-al1) rules out would be back.

<a id="list-records-stay-outside-the-overlay"></a>

> [!WARNING]
> **Never store list records inside the overlay JSON.** The list capture is a file of its own
> beside the overlay (`render.Target.ListCapturePath`), not a reserved key inside it, because
> every overlay reader — `config diff`, `config promote`, the overlay entry count, the host drop
> prune — would read the marker as a user key, and `promote` would copy it into a pack.
> `yolo config promote` does not lift list captures into a pack yet; whether it should is
> [`OQ-AL4`](#oq-al4), and it is not a reason to move the records into the overlay.

**At the host, the insert record is kept under both contracts.** `yolo host apply` under `own`
renders through `stateful`, but it also writes the `rmw` insert record (the entries a contribution
put in the file, and the user's recorded removals as declined). So switching `host_management`
between `assert` and `own` keeps a pack's entries yolo's: `own`'s adoption does not take an
inserted entry for the user's, `assert` knows which entries it may remove on a pack drop, and a
revert under `own` withdraws exactly the inserted entries. A key whose array a captured per-entry
edit changed is labelled `overlay`, as the whole-array capture labelled it, so a revert never
deletes it whole.

> [!NOTE]
> **The `host_management` value `"assert"` is retired by a 2026-09-20 ruling that is not built.**
> This section describes the shipped tree, where `assert` is still the default for an absent key.
> The ruling keeps `none` and `own`, with `none` as the new default
> ([`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)).
> When it lands, the `assert` cases above go with it. What it does to a config or a home already
> on `assert` is still open there, as
> [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14). The `--assert` flag on
> `yolo host apply` is a different thing, and the ruling keeps it.

<a id="config-list-visibility"></a>**Where you see it.** A key's one-word provenance label
cannot say that several packs' entries survive in one array — `packages  config-overlay:personal`
would read as that one pack's list — so the per-entry account is printed separately:

- `yolo config render --explain` prints, per list path, its entries in order with the source of
  each (the lower layers, a pack, or a captured edit), or the layer that replaced the assembled
  array. The preview folds `config-overlay` and `config-list` contributions. It still folds no
  `computed` layer and no capture.
- Each boot, and each `yolo host apply`, names the packs that appended entries to each surface.
- `yolo config ls <agent>` prints, per array, the contributing packs with their entry counts, or
  that `managed` or a captured edit replaces the array, and the in-jail adds and removes recorded
  there. Its OVERLAY column counts captured list entries separately (`N list entries`).
- `yolo pack footprint` and `yolo pack lint` show one claim per contribution, targeting
  `agent/name#<pointer>`. A posture list is named on its pack's `autonomy` claim instead,
  beside its posture, because a claim leads with the kind the author wrote.
- `yolo config diff` prints one `+` or `-` line per captured list entry. Which packs contributed
  is not a captured edit, so diff does not say: `diff` reports captured divergence and nothing
  else ([`OQ-CR7`](config-target-resolution.md#oq-cr7)), and the contributor account belongs to
  `ls` and `render --explain`.
- `yolo config reset` discards the list capture with the overlay.
- `yolo config promote` does not promote list captures yet. It names how many captured list
  entries a surface holds, and at which paths, instead of reporting nothing captured.

<a id="config-list-limits"></a>**What it does not do.**

- **It does not withdraw its entries once the owner is gone too.** When the owning pack and the
  contributing packs have all left `packs`, the entries yolo inserted stay in the host file. The
  drop prune in `yolo host apply` (`entrypoint.PruneHostOverlayKeys`) retires `config-overlay`
  keys only and has no list handling, and no render reaches a surface whose owner is not loaded.
  `yolo host apply --revert`, which withdraws everything yolo wrote to the home, does remove them
  for an owner yolo ships: it walks the shipped packs' surfaces as well as the configured ones,
  and removes exactly the entries the insert record names. A contributor dropped while its owner
  stays is handled by the owner's next render, as above.
- **It does not promote.** `yolo config promote` leaves list captures in the workspace (see
  [the list above](#config-list-visibility)).
- **It removes and rewrites nothing.** A list contribution only appends. There is no per-entry
  removal or veto for a pack, and [OQ-LT2](#oq-lt2) records that gap as accepted.
- **It is not a user-level channel.** A contribution is a pack's, so an entry only one person
  wants still needs a pack of theirs to declare it (the conventional local pack, for example).
  What the kind removes is the need for that pack to copy anyone else's list.

> [!WARNING]
> **Do not make arrays additive in the merge patch to get this.** A `"packages": null` in a patch
> deletes the whole key; it is not a request to remove one package. Deliberate empty arrays and
> every overlay that exists to replace a list depend on RFC 7386 staying as it is.

The first two limits above are open questions, not rulings: nothing records either as accepted,
the way [OQ-LT2](#oq-lt2) accepts the third. Their ids continue the additive config-lists
design's numbering ([`OQ-AL1`](#oq-al1), [`OQ-AL2`](#oq-al2)), because both are that design's
residue.

- 💬 <a id="oq-al3"></a>**[`OQ-AL3`](#oq-al3) — should dropping the owner and every contributor
  withdraw the entries yolo inserted?** Today they stay in the host file: the drop prune in
  `yolo host apply` retires `config-overlay` keys only, and no render reaches a surface whose owner
  is not loaded. `yolo host apply --revert` is the one path that removes them, and only for an
  owner yolo ships.
- 💬 <a id="oq-al4"></a>**[`OQ-AL4`](#oq-al4) — should `yolo config promote` lift list
  captures?** Today it leaves them in the workspace and names how many captured list entries a
  surface holds, and at which paths ([the list above](#config-list-visibility)).

### Provenance, and what `config diff` can say

R3: provenance must be **user-visible**, not merely recorded, because provenance nobody can
read does not make an override legible — which was the entire justification for the kind.
`yolo config diff <agent>` therefore reads pack declarations *and* the render's own record,
and reports one of five outcomes per contributed key: `set by <pack>` when it won,
`contributed by <pack> but <layer> won` (naming the layer, measured, not guessed),
`contributed by <pack> but the key is not in the rendered file` (a tombstone dropped it), `contributed by <pack> (winner not measured at the <notch> notch — <reason>)`,
and `written by a past apply for <pack>, which no longer asserts it`.

> [!WARNING]
> **Never infer the winner from the declarations.** That inference is what made this command
> print `contributed by X but managed won` for a key no `managed` layer even declared — at
> the host notch, where no provenance sidecar is written at all, so it told users their
> overlay had lost when it had won. A confident wrong answer is worse than an unknown, which
> is why the no-record case names *which* absence it is.

An `rmw` or `computed` surface writes no provenance sidecar by design, and that reads as
"winner unknown — this surface's mode keeps no provenance sidecar" rather than as a loss:
sending a user to investigate a by-design absence is its own kind of misreport.

## Composed-file posture: what "writable" means

Every composed file has a read/write posture, from a three-way taxonomy:

- **Derived** — yolo is the sole author; the file is a pure function of layers. Made
  effectively read-only in the jail (`computed` mode); an in-jail edit is discarded next
  boot.
- **Shared** — yolo and the agent both write, on disjoint keys (`rmw`); yolo asserts its
  managed keys and preserves the agent's.
- **State** — the agent owns it; yolo only seeds and persists it (`stateful` with capture, or
  a `state` dir).

A second axis is whether a surface is **host-linked** (declares `readsHost`, so it has a
`host` layer). The posture and the host-link together decide whether an in-jail edit survives, and
whether the file is safe to regenerate.

**Ownership does not carry over from the jail to the host.** Every jail path is disposable
and `:ro`; the host equivalents are the user's own files. So the host render refuses any path
it cannot prove yolo wrote, and retires its own output by ARCHIVING it under the state dir
rather than deleting (reclaimed by `yolo prune`).

Skills and briefings are **composed wholesale** at every notch, and the user's own content
MOVES into the local pack (`~/.config/yolo-jail/local/`), where yolo composes it back into
every destination. That is the point of the rule rather than a side effect: a personal skill
used to live in each agent's dir independently and drift per agent, with no command
reporting the divergence. One copy cannot diverge.

- **Precedence is the LAYER ORDER**, and the local pack is appended last. The per-entry rule it
  replaced asked "did THIS PACK write it?", which refused any pack overwriting another's
  recorded name whatever the order; composition asks only "is this yolo's?", so the refusal is
  unrepresentable rather than handled ([§6a-5 (pack batch)](#batch-6a-5)). Order is not a
  licence to shadow a name: at every notch two packs claiming one unnamespaced skill at one
  destination is [a fatal collision](#skills-collision), the local pack included.
- **Migration collisions are resolved by CONTENT, not by name.** Byte-identical copies of one
  name union silently. DIFFERING content is a real conflict — both survive as `<name>` and
  `<name>-from-<agent>`, warned about ONCE, at the migration, naming both sources. Losing one
  of two hand-written skills silently is the failure that rule exists to prevent.

## Retiring a dropped pack's host output

`yolo host apply`'s render loop visits the packs that are configured, so a pack removed from
`packs` is never asked what it left in the home. Each kind's own retire pass ("what this pack
wrote last time, minus what it ships now") is keyed on the pack being rendered: it handles a pack
that **changed** and cannot see a pack that **left**. A separate retire pass covers the second
case, for every kind a pack writes into a real home:

| Kind | When its pack is dropped from `packs` |
| :--- | :--- |
| `briefing` | a destination no remaining pack composes into is archived, with **no** prompt ([R4 (pack drop)](#pd-r4)) |
| `skills` | archived, behind the prompt ([R1](#pd-r1), [R2 (pack drop)](#pd-r2)) |
| `files` | archived, behind the same prompt |
| `config-overlay` | the key is removed from the file in place, in the same prompt, and its provenance entry goes with it ([R3 (pack drop)](#pd-r3)) |

Three rules decide what counts as dropped:

- **The set is the one the config NAMES, not the one that resolved.** A fetched pack whose remote
  is unreachable resolves to nothing, and to a retire pass keyed on the resolved set it looks
  dropped. For a briefing that mistake heals on its own, but an archived skills tree does not
  come back until the user goes digging in the state dir. An `--assert` over an unresolvable pack
  is refused outright ([the dispositions](host-apply-staleness.md#the-dispositions)), so the
  retire pass meets an incomplete set only in a dry run, where this rule keeps the preview
  honest. The briefing prune reaches the same outcome another way: it retires nothing while the
  pack set is incomplete.
- **Emptying `packs` still retires.** With nothing to render, the apply still runs the retire
  pass against an empty configured set. That is the most complete drop there is.
- **An unknown set is refused.** A caller that passes no configured set would otherwise read as
  "every pack is gone" and archive everything yolo ever delivered.

**The evidence of ownership is self-contained.** The two host ownership records (the per-entry
record that `files` and per-entry skill delivery share, and the composed-skills record) each map
an absolute path to the pack that wrote it. So they answer "whose was this?" without the dropped
pack's manifest or tree, which may be gone from the machine. A namespaced skills subtree is also
recognized by its own plugin marker (`hostskills.YoloPluginOwner`), consulted only for a
directory no record already owns: for a wrapped plugin the marker names the *plugin*, not the pack
that delivered it. The marker scan looks in the skills destinations the candidate packs declare,
so a dropped pack whose `into` no other pack names leaves no discoverable subtree. That gap is
narrow, and accepted. A record whose path is already gone is dropped without a prompt, since
nothing is lost.

**The prompt.** One confirmation covers every path and every key a drop left behind, grouped by
pack. It appears only when something would actually move. A dry run never prompts and prints
`would archive` and `would remove key` lines instead, and a nil or EOF stdin reads as **no**. A
decline leaves all of it, with no partial application, and says so. It also leaves the exit
status unchanged: nothing the user asked for failed, and a non-zero exit would make every
scripted `--assert` after a drop look broken, with no non-interactive way to answer. A record is
forgotten only after its path has moved, so a path still in the home is never read as the user's.

A silent archive would be recoverable, and it is still the wrong shape. The user's action was
*edit a config list*; the consequence is *files left my real home*. Those are far enough apart
that the action is named at the moment it happens.

**The `config-overlay` half reads the provenance record, because nothing else knows.** An
overlay folds in below the owning surface's managed layer and leaves no mark in the file's
bytes. The per-key host provenance record is the only place "a pack put this here" was written
down (`entrypoint.PruneHostOverlayKeys`). A key is eligible when the record attributes it to the
dropped pack as `config-overlay:<pack>` or as `retired:config-overlay:<pack>`. Which of those is
on disk depends only on whether a render has run since the drop, so accepting only one spelling
would make the prune work in only one posture. Four conservatisms each keep a key rather than
remove one:

- **`host` is never touched**, so a key the user set, even one whose name a pack also uses, is
  out of reach.
- **A key a live layer still claims is not an orphan**, whatever the record says (`liveClaims`,
  which ignores `defaults`, since that layer only fills an absent key). The record holds one
  winner per key, so when two packs contribute one key and the one it names is dropped, the
  record alone would call a live key an orphan. An `--assert` would self-correct through its own
  render. A dry run would not, and would print `would remove` for a key the next assert keeps.
- **An unparseable file yields nothing.** The render refuses that file loudly, once.
- **An unknown active set is refused**, as above.

Two cases are out of reach on purpose. A dynamic managed table (the `mcpServers` block) folds an
overlay's entries into a wholesale table attributed `computed`, and the next apply regenerates
that table without them (`regenerateManagedTables`); a second remover would race it.
`retired:managed` and `retired:computed` are the owning pack's own keys, which is a different
axis. A pack that contributes an overlay and nothing else still raises the prompt by itself. A
removed key is not archived, unlike a path: a delivered path may carry the user's edits, while a
key is the pack's own assertion, and putting the pack back in `packs` restores it exactly.

<a id="the-retired-provenance-label"></a>

### The `retired:` provenance label

An `rmw` render has no layer fold, so its provenance record is derived by replaying the write
order: every key the file already has starts as `host`, and each live layer then claims its
own. Drop the pack that contributed a key and nothing claims it, so yolo's own output reads
`host`, *the user set this*. That mislabel is **provenance laundering** *(the name the
drop-retirement work gave it)*, and it feeds itself: once a key reads `host`, every mechanism
that asks "did yolo write this?" answers no, forever, although the true answer was in the
record one apply earlier.

So the render rewrites a key to `retired:<the layer that last claimed it>`, for example
`retired:config-overlay:dropme`, when all three hold (`retireUnclaimed`):

1. this render derived `host` for it;
2. the previous record attributed it to a layer yolo **force-writes** (`agentcfg.LayerAsserted`:
   `managed`, `computed`, or `config-overlay:<pack>`);
3. the key is still in the file.

- **Sticky.** A retired key stays retired, and keeps naming the original layer rather than
  nesting (`agentcfg.RetiredOf`). Without that the fix would only delay the laundering by one
  apply.
- **A prefix on the previous label, not a bare token or a second column.** The record stays one
  `key<TAB>layer` line, so every reader parses it unchanged. The label is not `host`, so
  nothing mistakes the key for the user's, and it is not `config-overlay:<pack>`, so a reader
  asking "did my pack win this key?" correctly answers no.
- **`host` and `defaults` are never retired.** Retiring a user's key is the same laundering in
  reverse, and the direction that costs something: a prune reading the record would delete it.
  A default is written once, fill-if-absent, and its value is the user's from then on.
- **Fail-safe.** A missing or unreadable previous record proves nothing and changes nothing.
  Corruption inside a readable record is skipped line by line, and only a closed set of exact
  tokens can claim a key, so one bad byte cannot relaunder a surface.
- **`Compose` never emits it.** A fold renders only from the layers it has, so a key no layer
  claims is simply not in the file, and there is nothing to launder. The provenance parity table
  asserts that no first-render host record carries a retired label
  (`provenanceparity_test.go`), so a retirement that ever fired without a previous record would
  fail the table rather than invalidate its premise.

The reader is `yolo config diff`, which reaches a surface whose only finding is a retired key and
reports it as written by a past apply for a pack that no longer asserts it
([provenance](#provenance-and-what-config-diff-can-say)).

## The derive slot

A surface whose content depends on live configuration — which MCP servers are set, which LSP
servers are enabled, which provider a profile selected — cannot be static data. That dynamic
layer is the one place a pack runs Lua, and it is tightly bounded.

A pack ships `derive.lua` at its root and registers producers. There are two registrations,
each with a key space of its own: `yolo.derive(agent, surface, fn)` produces a surface's
computed layer, and `yolo.env(agent, fn)` emits process environment (see
[`providers.md`](providers.md)).

```lua
yolo.derive("opencode", "config", function(ctx)
  -- ctx's source tables are live and read-only.
  -- return the computed layer for opencode's `config` surface.
end)
```

The contract:

- A `derive` function is a **producer**: it returns the computed layer (a config value),
  which the engine folds in at the `computed` slot. It runs *before* the merge — it does not
  mutate the composed file.
- Its inputs are **live source tables**, read-only, and the set is closed and core-owned: a
  pack *projects* from these into its tool's dialect, and never invents a new source.
  `DeriveCtx` in `internal/agentcfg/luahook` is the enumeration.
- `ctx.tombstone` is a sentinel that round-trips to Go `nil` so a key can be deleted — bare
  Lua `nil` in a table just drops the entry, which cannot express "delete this key" — and
  `ctx.empty_array` is the sentinel for an intentional empty JSON array, which Lua cannot
  distinguish from an empty object.
- `yolo.model_for(alias)` is the one helper beside the two registrations: it resolves a model
  alias for the selected provider to `"<provider>/<id>", "<id>"`, or `nil`, and never reads
  another provider's aliases. Asked for a [tier alias](providers.md#tier-aliases) the provider
  lacks, it warns at boot and does not refuse. A pack that renders yolo's tiers into one
  extension's own config file (an *adapter pack*, a term
  [`extension-model-defaults.md`](../research/extension-model-defaults.md#defined-terms) coins)
  uses it to map them onto that extension's names
  ([OQ-XM1](../research/extension-model-defaults.md#OQ-XM1)).
- `ctx.in_full(t)` declares that `t` — the value of a TOP-LEVEL key — is a table the derive
  regenerates in full: its entries track a live table, so an entry on disk this run did not
  produce is yolo's own stale output. A first migration's adoption takes such a table whole,
  and `hostTableKeys` counts only such tables among the ones the host writes by replacement;
  at those two readers, a table returned without it claims only the leaves it names. Refused
  nested, around an array, and around a non-table.
  ⚠ **The jail's `rmw` arm does not read the declaration yet.** On an `rmw` surface
  `regenerateManagedTables` still clears and rewrites EVERY object-valued key of the computed
  layer, declared or not, so a table there that a derive means only to assert leaves of is
  regenerated whole in a jail while the host merges it. No shipped `rmw` surface returns such a
  table; what `rmw` should do with one awaits a ruling
  ([the residual](../design/config-ownership-and-promotion.md#built-2026-09-25--what-shipped)).
  Shipped derives reach the sentinel through a local guard so an older entrypoint, whose `ctx`
  lacks it, still runs them. The reverse pairing is not covered: a newer entrypoint handed an
  older derive (the packs are staged from the host binary) sees no declaration at all. That
  pairing arises only when `YOLO_REPO_ROOT` names a source tree newer than the host `yolo`, and
  the only refusal of it is the launch-time check comparing the binary's commit stamp with that
  tree's `HEAD` (`version.SourceSkew`, overruled by `YOLO_ALLOW_SOURCE_SKEW=1`, and silent for
  uncommitted changes)
  ([`CO13`](../design/config-ownership-and-promotion.md#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided)).
- It runs in the sandboxed Lua VM, and it must be deterministic.

The VM (`luahook.GopherLuaVM`, pure-Go gopher-lua, vendored so the hermetic image build works
offline) is **the derive path's alone**: the `luahook` package's whole identity is the pack derive
sandbox. It opens only the base, string, table and math libraries and then strips every forbidden
global by subtraction — no `os`, `io`, `require`, `package`, code loaders, `print`, environment
reassignment or the `math` library's random-number generator — so a script's only channel in or
out is the context the VM marshals. A run is bounded
by a wall-clock budget, and a Lua error surfaces as a loud Go error carrying file and line, never as
a partial computed layer. `internal/agentcfg`, the compose engine, does not link Lua at all; the
config-composition pipeline has no user-supplied script slot.

<a id="lt-p1"></a>

> [!WARNING]
> **The sandbox pieces in `luahook` look like transform leftovers and are shared core.** Until the
> config transform was removed ([`OQ-LT1`](#oq-lt1)) the package served two callers, and the
> removal was a SPLIT, not a deletion (P1 (transform removal): `derive.lua` runs exactly as it did,
> and every proof of the *shared* sandbox was re-expressed on the derive path before the
> transform's tests went). `openSandboxLibs`, `extraStrippedGlobals`, `ForbiddenGlobals`,
> `wrapLuaErr` — whose `lua transform error:` prefix is a wording leftover, not a sign the
> function is dead — the marshallers and `GopherLuaVM` itself are what every shipped `derive.lua`
> runs on. `TestEveryShippedPackDeriveStillRuns` (`luahook/shippedpacks_test.go`) is the tripwire:
> it runs every `packs/*/derive.lua` through the real VM, across all three registration spaces,
> because a fixture script exercises only the API subset its author wrote. The
> `TestDeriveSandbox_*` tests carry the sandbox proofs themselves.

<a id="derive-determinism"></a>

> [!WARNING]
> **"Must be deterministic" is still partly a requirement on the script.** The sandbox enforces
> it for randomness: the `math` library is opened for its deterministic functions, and
> `extraStrippedGlobals` clears `math.random` and `math.randomseed` out of it, so a
> `derive.lua` calling either fails loudly instead of producing a different computed layer
> every boot (`TestDeriveSandbox_RandomnessUnavailable`). It does not enforce reference
> identity: `tostring()` of a table or a function prints the Go pointer behind it
> (`table: 0xc000…`, gopher-lua's `LTable.String`), so a script that keys on or emits that
> string still varies between runs. Nothing yolo ships does. The package doc and
> `sandbox.go` record the remaining gap.

- 💬 <a id="oq-dr1"></a>**[`OQ-DR1`](#oq-dr1) — should the sandbox enforce reference identity too?** Opened 2026-09-25,
  when `5c1bfa5d` closed the randomness half and recorded this one as open. *DR* stands for "derive";
  the prefix is new with this question.

  - **(a) Leave it a requirement on the script.** Documented here and in the package doc; nothing
    yolo ships prints a reference. Cost: a third-party `derive.lua` that emits `tostring(t)`
    produces a different computed layer every boot, and nothing names the cause.
  - **(b) Close it in the sandbox, as the randomness half was closed.** Replace the base
    `tostring` with one that renders a table, function, userdata or thread as its type name
    alone (`"table"`), so no Go pointer reaches the script. Cost: it must reach every path that
    renders a reference, not only the global `tostring`, or it becomes a partial fix that reads
    as complete.
  - **(c) Detect it at the output instead.** Run each derive twice and refuse a layer that
    differs between the runs. This catches every source of variation, not only this one. Cost:
    it doubles derive time on every boot, or covers only the shipped packs if it runs only in
    `TestEveryShippedPackDeriveStillRuns`.

  <!-- vantage: oq id=OQ-DR1 leaning="(b), with a test that renders a table through every path gopher-lua offers and asserts no 0x appears. It matches how the randomness half was closed: take the nondeterministic source out of the sandbox, so the script cannot reach it at all." -->

  _Leaning:_ **(b)**, pinned by a test that renders a table and a function through every path
  gopher-lua offers and asserts that no pointer appears. That is how the randomness half was
  closed: the nondeterministic source is taken out of the sandbox, so no script can reach it.

  **Answer:**
  > _(empty — fill in when decided)_

The **canonical MCP-server type** lives in core: `name → {command, args, env}`, open and
additively versioned, so a new transport is a new optional field that never breaks an
existing projection. Each agent pack's derive projects that canonical table into its tool's
shape — folding `command`+`args` into one array and renaming keys, or tombstoning a table out
of one file because that tool keeps MCP in another.

> [!WARNING]
> **The `yolo.*` set is a version boundary, and it reads tolerantly on the rendering side.** A
> `derive.lua` is staged by the host and executed by the in-jail entrypoint, baked at the last
> `just load` — so a script may legitimately call an API newer than the build running it.
> Reading a member this build never registered yields a no-op and one warning naming it,
> rather than failing the boot; the surface's own producer still runs. This landed after a new
> registration arriving in a shipped pack bricked every jail on an older image with a
> non-function call. The trade is the same one the unknown-kind rule makes: a **typo** in a
> registration is a warning at boot rather than an error. The one strict reader left is the
> host-side env composition, `packload.AgentEnv`, which refuses an unknown member by name.

This is the whole of pack-supplied logic. There is no reshape op DSL and no pack-supplied
effect code; a projection a fixed combine rule cannot express is a `derive` function, and
nothing else in a pack executes.

## Capabilities and supersession

A **capability** *(coined in this repo, 2026-08-13)* is a **named job** — not a name for the
thing that does the job. `claude-oauth-refresh` is *"serializing OAuth token refreshes so
concurrent consumers do not burn a single-use refresh token"*; the broker loophole is one
implementation of it, and a different implementation would serve the same capability. Not a
loophole name, and not a feature flag: the capability is the **invariant**, the loophole is
the **implementation**, and a pack should only ever have to know the invariant.

Two verbs, and the asymmetry between them is a principle in its own right:

```jsonc
// a loophole's manifest.jsonc — a statement about ITSELF
"serves": ["claude-oauth-refresh"]

// a pack manifest — a claim about SOMETHING ELSE, so it costs more to make
"supersedes": [
  { "capability": "claude-oauth-refresh",
    "because": "Bedrock overrides the OAuth path entirely; no token is ever refreshed" }
]
```

**`serves` is a bare string list; `supersedes` requires an object with `because`.** A claim
about yourself is cheap; a claim about another component is not. Saying "this is my job"
needs no justification. Saying "your job does not need doing" is an assertion about code you
did not write, and the person who later finds their loophole silently absent deserves a
sentence explaining why. The mandatory `because` is **printed wherever the supersession takes
effect** — `yolo loopholes list`, `yolo doctor`, `yolo pack footprint` — so the justification
travels with the consequence.

`serves` lives on the **loophole** manifest and travels *inside* the pack, and it is refused
on a pack manifest with a message naming the distinction: a statement about an implementation
belongs with the implementation. The gate is one field read in `Loophole.Active()`:

```
Active()     = Enabled && !Superseded() && SupportedHere() && RequirementsMet()
Superseded() = serves is NON-EMPTY  AND  every served capability is superseded by some selected pack
```

Supersession is **second**, beside `Enabled`, because both are decisions a user's
configuration made where the two below are facts about the machine; `InactiveReason()`
branches in the same order, which is what stops the gate and the explanation from disagreeing.

- **`every`, not `any`.** A loophole serving two jobs, one of them superseded, still has a
  job. Superseding *all* of them retires the loophole — an arithmetic consequence of retiring
  each job, not a separate "retire this loophole" power.
- **Non-empty `serves` is required.** Silence means "not participating", never a default
  claim, so adding this mechanism cannot change the behavior of any manifest that does not
  opt in.
- **Granularity is always per job, never per component.** There is deliberately no way to say
  "turn that component off": `enabled: false` exists for that and is honest about being a
  blunt instrument where this is a statement about work.
- **Two declarations naming one capability string is the mechanism working, not a
  collision.** A capability name is an **interface** — a rendezvous point — where a skill name
  is an **identity**. Bare strings, no prefix. The footprint claim for `supersedes` is a
  display label deliberately absent from the closed kind registry, so two packs superseding
  one capability cannot be reported as a collision.

> [!WARNING]
> **Supersede is NOT provide.** The test is: *after this pack is selected, does the job still
> need doing?* `supersedes` claims the DEMAND vanished, never that SUPPLY moved — and
> superseding when you meant providing silently stops the job being done with nothing taking
> over. Nothing in the system can detect it, because "I will do it instead" is exactly the
> claim `supersedes` does not make. There is deliberately no `needs: [<capability>]`: it
> would invent conflict resolution before the conflict exists, and it is purely additive if
> it ever bites.

> [!WARNING]
> **A supersession matching no `serves` REFUSES THE LAUNCH, and the commands you diagnose it
> with only report it.** The structure is refused at load on both decoders (an empty
> `capability`, a missing `because`, a duplicate, a control character: all version-invariant).
> The MATCH needs the loophole set, which `pack lint` and the in-jail entrypoint do not have and
> cannot get (the loopholes → config → packload cycle), so it is decided where the set exists:
> the host side of a jail launch refuses it among the pack pre-flights, over the packs it just
> staged; `yolo host --` and `yolo host env` refuse it through the same gate, over their own
> selection; and `yolo check` fails the same row. `yolo loopholes list` and `status` only warn, so the
> commands a user runs to find out what happened keep working. When the host `yolo` is provably
> older than the source tree it builds from, the refusal says so and names `just install`: a
> capability only a newer shipped pack serves is skew, not a typo. The message is most of the
> value: *"pack 'claude-bedrock' supersedes capability 'claude-oauth-refersh', which NO loophole
> on this machine serves — did you mean 'claude-oauth-refresh'?"* is a fix. The design is
> [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md),
> [§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) steps 4
> and 5.

There is deliberately **no yolo-owned registry of capability names**: core does not know what
an agent is, and a central registry would rebuild the registry the pack system exists to
avoid. Capabilities are declared by whoever holds the fact.

## A pack's own config keys

A loophole a pack ships gets a `settings` block under its own name —
`loopholes.<name>.settings.<key>` — whose keys are **declared and typed in the loophole's
manifest**. Core validates them at the existing validation point, resolves them from the
merged config, and writes them to a file it owns. `yolo config-ref` is the authority for the
config keys; [`../guides/loopholes.md`](../../userguide/guides/loopholes.md) is the authority for the
manifest block. The four halves are `internal/loopholedecl/settings.go` (declare),
`internal/config/validate_loopholesettings.go` (validate), `internal/loopholes/settings.go`
plus `internal/cli/run/loopholesettings.go` (resolve and write).

> [!WARNING]
> **An opaque settings map is a trust regression regardless of where it is placed.** If core
> validates only "it is an object", it cannot tell one key from another — and the
> user-scope-only refusal on a loophole's `env` exists precisely to keep `LD_PRELOAD` out of a
> host daemon's spawn environment. Opacity does not dodge that rule; it launders it. So the
> keys are typed and declared, from a closed type set: a free-form JSON Schema would put
> validation outside core's hands and be a second config system.

Placement decides severity, and that is why the block lives under `loopholes.<name>`: an
unknown **top-level** key refuses the launch, while an unknown entry inside a loophole's block
is a warning. The inner key set beside `settings` stays closed, so a nested map keeps the
closure intact — an unknown key *inside* `settings` is checked against the declaration, an
unknown key *beside* it is still the existing error.

**Delivery is a file yolo owns, named by a manifest token** (`{settings}`, exactly as the
manifest already names the socket and the state dir). **A path is the one thing a spawn may
carry**, and the *contents* are written by core after validation rather than by whoever edited
the config: the workspace supplies **values**, never **environment**. It also closes a hole
rather than relocating one — a daemon reading the raw workspace file per request lets an agent
rewrite the allowlist mid-session with no relaunch and therefore no approval gate, so a
core-written file makes the value **launch-frozen**. Changing what such a daemon may reveal
requires a jail restart, which is where the config-approval gate lives.

> [!WARNING]
> **The settings file must never cross into the jail.** A loophole with a `jail_daemon` gets
> its state dir bind-mounted into the container, and an ABSENT `state_files` means the *whole
> directory* crosses — so writing resolved settings there published them to the agent,
> including a key declared user-scope whose entire purpose is to stay out of the agent's
> reach. The `0600` is no barrier: a jail's agent runs as UID 0. The combination is now
> **unrepresentable**: a manifest declaring `settings` AND a `jail_daemon` must declare
> `state_files`, and may not list the settings file in it — both refused at load
> (`loopholedecl.refuseSettingsFileCrossingIntoTheJail`, pinned by
> `TestSettingsFileMayNotCrossIntoTheJail`), so the manifest fails and the loophole vanishes
> rather than leaking. Excluding one file from a mount the author declared was rejected as a
> carve-out the author cannot see.

> [!WARNING]
> **A user-scope list is a ceiling a workspace cannot narrow — only widen.** The config merge
> union-merges every list at every depth, and the replace-wholesale exception was deleted
> precisely because a workspace value replacing the user's is what let an agent-editable file
> decide what entered the jail. So for an allowlist the weak, agent-writable scope can only
> **add** capability. The per-key `scope` field is the answer: a capability-widening key is
> declared `scope: "user"` and a workspace cannot contribute to it at all. **`user` is the
> default** — an author who says nothing gets the strict answer, and a widening key has to ask
> in writing.

A workspace **may** supply values that reach a host daemon, and the conditional is the whole
ruling: **as long as they go through the config-change gating**. A typed, declared setting core
validates and writes is a different object from an arbitrary key/value pair injected into a
process environment — but "different object" alone would not be enough. What closes the gap is
that the approval snapshot lives in host-side state the jail never mounts, and a
non-interactive launch does not auto-accept a changed config. **If either is ever reverted,
this answer goes with it:** a workspace-supplied value reaching a host daemon is only as strong
as the approval that admits it.

`enabled` is **not** pack-declared. It is universal and core declares it for every loophole;
what the manifest supplies is its *default*. One config key, one manifest field declaring its
default, and the arbitration dissolves rather than needing one.

Scope is a **host-notch** property only. Inside a jail the config directory is a read-write
bind of the workspace's own tree, so user-vs-workspace is not a boundary there.

## Selection and the load path

### The `packs` key

Packs are selected by the `packs` config key, **user scope only** — a workspace config naming
a pack is a hard error, because a workspace is agent-editable and travels with the repo.
Nothing is active by default: an empty `packs` yields a jail with no agent, and says so at
launch (`cli.warnIfNoPacks`). `internal/config/validate.go` hard-errors on the retired
`agents` key on the host.

Three source forms:

```jsonc
"packs": [
  "claude",                                              // a pack yolo ships, by bare name
  "file:///home/me/code/my-pack",                        // a local directory
  "git+ssh://git@github.com/org/repo//subdir?ref=main"   // a fetched pack (ref mandatory)
]
```

The object form adds `name` and `only`/`exclude` globs, for per-project narrowing of a shared
corpus. A config still carrying the retired `allow_exec` is refused as an unknown key, because
a key that does nothing must not be accepted quietly.

- 💬 <a id="oq-pk1"></a>**[`OQ-PK1`](#oq-pk1) — which packs may a workspace config declare?**
  The user-scope rule above stands in the code, but
  [`OQ-MP7`](../design/mcp-presets-removal.md#OQ-MP7) ruled on 2026-09-20 that it should not
  simply stand: define a **safe subset** of packs a workspace may declare, drawn on host reach
  rather than on install. The subset itself is not ruled. The hard case the ruling names is an
  `mcp` entry, which is code that runs unconditionally at jail start in a home holding real
  credentials, so the rule has to say why that is acceptable or admit content kinds first and
  leave executable kinds to a second ruling. The ruling asks for
  [`OQ-WS1`](../design/workspace-skills.md#OQ-WS1) (the same question for skills) to be ruled in
  the same sitting, and names R5 in [`loophole-system.md`](loophole-system.md#principles) as the
  wording it amends. It blocks building
  [`mcp-presets-removal.md`](../design/mcp-presets-removal.md): that doc's
  [§13](../design/mcp-presets-removal.md#13-what-i-would-build-in-order) steps 1–3 wait on it,
  because that ruling moved the workspace-scope boundary they build against. *PK* stands for the
  `packs` key; the prefix is new with this question.

### Fetch, refresh, lock

**A host launch fetches and refreshes git packs itself, and the ref decides what moves**
([`OQ-PF1`](#oq-pf1)). `yolo pack install` and `update` still exist, but neither is required.

- **Where it runs.** Once per launch, on the host, before any pack is resolved or staged. The
  jail launch runs it (except `--dry-run`, which materializes nothing), and so do the host
  commands that resolve packs for the real home: `yolo host -- <bin>`, before it composes or
  applies anything and whether or not host management is enabled, and `yolo host apply` and
  `yolo apply --at host`, before they render. It reaches every configured git pack and nothing
  else. An embedded pack ships in the binary, and a `file://` pack is a directory on disk, so
  neither ever touches the network. **In a jail it does nothing**: there is no pack store and no
  git credentials in there, and a nested launch resolves from the tree the outer launch staged.
- **What the ref decides.** The launch looks the ref up in the pack's local mirror, the bare
  repository the store keeps per remote, and acts on what it finds:

  | The ref is | The launch |
  | :--- | :--- |
  | not in the store (no mirror, or the mirror does not hold the ref) | fetches the remote and checks the commit out, the same code path `install` runs |
  | a full 40-hex commit SHA the mirror holds | never fetches. A commit is frozen |
  | a tag the mirror holds (`refs/tags/<ref>`) | never fetches. A tag is treated as immutable, so following a re-pointed tag takes an explicit `yolo pack install` or `yolo pack update` |
  | a branch (`refs/heads/<ref>`) | fetches when the last successful refresh of that mirror and ref is more than an hour old, and otherwise uses the mirror as it is |

  The hour is the same interval the in-jail agent launchers use for their evergreen update
  check. A refresh time is recorded in the pack store only when a fetch succeeds. **A
  launch's fetch never moves a tag**, even one no pack in this launch is pinned to: the fetch
  updates every ref, so the launch puts back every tag it moved or pruned, and a tag pack of
  another workspace sharing the mirror stays where it was. "Refresh"
  here means re-fetching a pack's mirror; it is unrelated to a `program` contribution's
  `refresh` field, which is a program's own pre-launch step ([`program`](#program)).
- **A failed fetch** (offline, refused credentials, timeout) **is not fatal when the ref
  already resolves locally**: the launch prints one warning naming the pack and the error, and
  uses the commit it has. When there is no usable local copy, resolution fails as it always
  has, fatally and by name, and the message carries the fetch error. A ref that a fetch
  **succeeded** without finding (a typo in `?ref=`, a deleted branch) is fatal too, and says
  so: no later launch repairs it. Each launch-time fetch of a repository runs under one
  timeout, shorter than `install`'s, covering its clone, its fetch and the checkouts after
  them, so a hung remote costs a bounded wait and then counts as a failed fetch.
- **Every move is disclosed.** A pack delivered for the first time prints
  `Fetched pack <name>: <ref> → <short sha>`: one fetched now, or one whose repository and
  subdirectory the lockfile has no entry for (a monorepo's second subpath, which arrives
  without a fetch). A pack that moved off the commit its lockfile entry recorded prints
  `Updated pack <name>: <ref> <old short> → <new short>`. Nothing prints when nothing moved.
  Like every launch disclosure, these have no flag to hide them
  ([`OQ-RO3`](report-tiers.md#why-its-this-way)).
- **Concurrency.** Fetch plus checkout runs under an exclusive lock per mirror, and the
  lockfile's read-modify-write under an exclusive lock of its own; `install` and `update` take
  the same two. A launch that waited on another's fetch re-reads the refresh time, so two
  launches started together fetch a branch once.
- **Git hygiene is the install path's, and then some.** Every git run that receives objects
  (the clone, the fetch, and a checkout of the partial mirror, which fetches blobs) runs with
  fsck-on-transfer, so malformed third-party content is rejected at the boundary, and with
  terminal prompts disabled, so a missing credential errors instead of hanging. A launch also
  runs git with no controlling terminal: ssh reads its host-key and passphrase prompts from
  the terminal rather than through git, so without that a first contact with an ssh host would
  stop the launch at a prompt. On a timeout it kills git's whole process group, transport
  helper included. `install` and `update` keep the terminal, so you can answer an ssh prompt
  there. A git pack is cloned into a content-addressed
  store: a bare mirror per repository and a checkout per commit. The mirror is a partial
  (`--filter=blob:none`) clone: it holds every commit and directory listing of the
  repository's branches and tags, and a file's contents only once a checkout needs them. A
  checkout fetches the contents it lacks in one request before it starts, since git's own
  checkout would fetch each file separately, one connection to the remote apiece. A remote
  that does not support partial clone ignores the filter and sends everything.
- **A pack in a subdirectory checks out that subdirectory and nothing else**, so a pack living
  in a large repository downloads the contents of its own files, not the repository's. The
  subdirectory is matched literally: a directory named `p*` checks out that directory and no
  sibling the name would match as a pattern. Each commit and subdirectory gets a checkout of
  its own, created empty, so moving a pack to a new commit leaves no file of the old one
  behind, and a moving ref never corrupts an existing checkout. A pack at the repository root
  checks out the whole commit. A subdirectory that is a symlink or a submodule is refused, and
  so is a path through one, and the refusal names it. That holds for a link to another
  directory of the same repository too: with `packs/current` a link to `v2`, the address
  `//packs/current/mypack` is refused, and `//packs/v2/mypack` is the one to write. A pack
  root is never a directory a link in someone else's repository chose, which could be one
  on your machine. A symlink inside the pack that
  points out of it, to a sibling directory or an absolute path, refuses the pack
  ([its content rules](#what-a-pack-is-on-disk)); the sibling is not checked out, so such a
  link points at nothing in the store.

**What never fetches.** Every read-only surface resolves from what the store already holds and
stays offline: `yolo check`, `yolo check-deps`, config validation, and the agent footer's profile
read. `yolo check` also checks nothing out, since a checkout of the partial mirror can fetch.
It reports a git pack the store does not hold yet as a `[SKIP]`, a check that did not look,
saying the next launch fetches it, and a commit the store holds but has not checked out as a
`[SKIP]` saying the next launch checks it out. A `[SKIP]` never fails `check`. A ref a fetch
already came back without stays a `[FAIL]`, because every launch fails on it.

**The lockfile.** It records the asked-for `source`, the resolved `commit`, and the `ref`, and
nothing else. **There is no approval record in it, and the absence is a ruling rather than an
omission**: a field asserting an approval nothing enforces is worse than no field, and
`packsrc.LockEntry`'s own doc comment refuses to have one back without a design ruling. The
launch writes an entry for every git pack it resolved, the way `install` does, and saves the
file. It never prunes: dropping the entries of packs that left the config is `install`'s job,
and entries of packs the launch did not process are left alone. A launch never records a
`file://` pack, so only `install` writes a local pack's entry. The launch reads the lockfile
only to tell whether a pack moved. Resolution then stages the commit the refresh decided,
not whatever the shared mirror's ref names by then, so a concurrent fetch by another launch
or an `install` cannot slip in content this launch did not disclose. `yolo pack status` flags
drift between the config address and the lock.

**What `install` and `update` are for now.** Both force a refresh of every configured git
pack, a tag or a branch still inside its hour included, so either one follows a re-pointed
tag; a launch never does. `install` also prunes the lockfile and records local packs.
`update` is `install` plus the refresh of npm-declared programs ([`program`](#program)).

**Choosing a ref IS the trust decision.** A pack can run host code: a loophole's daemon, a
`reads-host` read, a render into the real home by `yolo host apply`. So host-side content must
never move silently, and two things keep it from doing so. The ref rule decides whether it
moves at all: pin a tag or a commit and it does not move until you change the pin or run
`install` or `update`. The disclosure lines
say when it did. Following a branch is consent to its author's next push, picked up within the
hour and announced when it lands. **A tag or commit pin is the shape for a pack carrying host
execution.**

`yolo host apply` resolves through the launch's own resolver (`config.ResolvePack`, called by
`cli.resolveConfiguredPack`) after the refresh has run, and **stages every filtered pack** —
embedded, local or fetched, any entry with an `only` or `exclude` — into a directory of the
process's own leased tree, which it reads for as long as the verb runs
([notch convergence](../plans/notch-convergence.md), item 5). An unfiltered pack is read in
place, after the same symlink check the launch's staging applies, because a copy would hold the
same files. So an entry's `only`/`exclude` decides what reaches the real home exactly as it
decides what reaches a jail, an embedded entry's included. A fetched pack with a symlink pointing
out of the pack is refused, as the launch refuses it. A local pack's symlinks, filtered or not,
are followed wherever they point, as the launch follows them
([OQ-NC9](../plans/notch-convergence.md#OQ-NC9)). **An incomplete set is refused whole**: if any configured
pack cannot be resolved, `--assert` writes nothing and exits 1, naming each pack and its
reason. The dry run says it would refuse. A pack whose manifest has problems, the ones
`yolo check` and every launch refuse, counts as unresolvable here: it is named with each
problem, and the fix is in the pack, not a fetch
([NS-D14](../design/notch-scoped-config-contributions.md#10-decision-ledger)). For the
conventional local pack, which has no `packs` entry, the remedy names its directory. The
problems, and the declaration every host verb reads, come from the tree the entry's
`only`/`exclude` leave, as the launch loads it: a file the entry excludes is no problem, and a
`pack.json` it filters out is not read ([NS-D15](../design/notch-scoped-config-contributions.md#10-decision-ledger)).
The other host
verbs leave such a pack out and say so: `yolo host --` and `yolo host env` compose without it
(a profile only it declares refuses the launch, naming it), `--revert` keeps its keys recorded,
`check-deps` exits 1, the read-only `config` verbs report it, and `config promote` refuses to
write into it.

### Host-side staging, then jail-side render

- The host stages only the **selected** packs, into a **new pack tree per launch** under
  `AGENTS_DIR/<cname>/pack-trees` (delivered as `YOLO_PACK_ROOT`), because **the mount is the
  filter**. Embedded packs land under `_official/<name>`, configured ones at `<slug>`, and the
  [`needs` closure](#needs--conditional-pack-dependency)'s additions under `_official/` too. The
  tree carries its own record of which directory holds which pack, in load order.
- A dropped pack needs no unstaging: the next launch's tree never holds it. No launch writes a
  tree another launch staged, so a running jail keeps its own, and a staging that refuses takes
  its partial tree with it and leaves every other tree alone
  ([`OQ-PK2`](#oq-pk2), [concurrent launches](#concurrent-launches-of-one-workspace)).
- An **attach never re-stages.** It stages the config into a tree of its own, which nothing
  binds, to compare with the running jail's and to run the pack pre-flights, then discards it.
  Everything it composes on the host reads the running jail's tree.
- A declared pack that cannot be staged is a **fatal** error, and resolution failure is
  reported by name with the command that fixes it. A jail must not come up silently missing a
  pack it was told to load, and an empty pack view is **not** a deactivation signal.
- In the jail, the entrypoint renders every staged pack in **one loop with no switch on any
  tool name**: for each contribution it dispatches on `kind`. This loop is the concrete proof
  of principle 2.

**For a jail launch, what runs host-side is decided by dependency, not by preference**
([Rulings 3 and 4 (open rulings)](#open-rulings-3)). Only what needs the host runs there: the
image-build inputs, which feed a nix derivation built before any container exists, and anything
that reads a host file or a host credential, such as pack fetch and the lockfile, `host_files`
staging and the `host` layer. Everything that needs no host influence runs in the jail's
entrypoint: composing each surface, capturing in-jail edits into overlays, and writing the
sidecars under `<workspace>/.yolo/prism/`. **A running jail is never re-rendered.** An agent's
edit to a composed file is folded into its overlay by the next boot's render
([`config-migration-to-prism.md`](config-migration-to-prism.md)), so only observability waits for a
restart, and nothing is lost.

### Concurrent launches of one workspace

Two launches of one workspace share one per-workspace directory, `AGENTS_DIR/<cname>`: the
skills and briefing staging a podman jail binds, and on macos-user the home-overlay and `/ctx`
trees its sandbox copies. On macos-user the two launches are two sandboxes. On podman and Apple
Container the second attaches to the jail the first started. They do NOT share a pack tree: each
launch stages its own ([`OQ-PK2`](#oq-pk2)), which is what this section's first fix became. Nor
do two macos-user sessions share a host-services dir: each publishes its endpoints into one of
its own and removes only that one
([`HSD-4`](jail-home.md#why-its-this-way)), after the second Mac run measured the first
session to exit removing the other's
([`OQ-HD10`](../design/host-daemon-ownership.md#OQ-HD10)).

**What went wrong, MEASURED.** The first macOS run of
`TestMacosUserTwoConcurrentLaunchesOfOneWorkspace` (CI run 36240337031, commit `6eb92400`)
failed before either session was up. Launch B exited 1 with
`unlinkat …/agents/<cname>/packs/_official/claude: directory not empty`, and launch A warned
that its `claude-oauth-broker` module dir "is not a directory, so that loophole is NOT
active". Both launches staged into one shared tree, `AGENTS_DIR/<cname>/packs`, before any
lock, and staging cleared `_official/` wholesale, so each launch's clear raced the other's copy
into it and removed what the other was about to read. `TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed`
([`concurrentstaging_test.go`](../../internal/cli/run/concurrentstaging_test.go)) reproduced
the same `unlinkat` on Linux against the shared tree, because the staging code is
backend-agnostic, and passes now.

**A second defect, found in the same reading and needing no second launch.** Every attach
re-staged by clearing and copying: `_official/`, each configured pack's directory and each
skills directory. A bind mount captures an inode, and a directory that is removed and recreated
under a live bind leaves the jail looking at the removed one, which is empty. So an attach with
an **unchanged** config emptied the live jail's `files` trees and loophole module dirs, and its
agent saw its skills vanish until the copy caught up.

**Two fixes, the same day (2026-09-26).** The first opened the per-workspace launch lock at
staging and made every re-stage a sync (`internal/treesync`), so a second launch waited and an
unchanged file kept its inode. It kept the attach refresh exactly, and said why it stopped
there: per-launch trees would also close the race, but a running jail would then keep the tree it
booted with, which was a product decision. The maintainer ruled it ([`OQ-PK2`](#oq-pk2), option
(c)), and the second fix builds it: one immutable pack tree per launch. No launch writes a tree
another launch reads, so pack staging needs neither the lock nor the sync.

| Part of the first fix | What it is now |
| :--- | :--- |
| The launch lock opened at pack staging | It opens where each backend first touches what launches still share (below). Staging runs outside it |
| Every re-stage a sync | A pack tree is copied once, into an empty directory, and never re-staged. The skills staging is still synced on attach, so `internal/treesync` stays for that |
| An attach refreshed the pack tree a live podman jail binds | An attach writes into no pack tree; it reads the running jail's |

**What the lock is still for, and why each one is correctness rather than courtesy.** The lock
(`holdLaunchLock`, [`flock.go`](../../internal/cli/run/flock.go)) now covers:

1. **The attach-or-create decision** on podman and Apple Container. A workspace has one
   container name, and two launches deciding at once could both create it. Before the staging
   fix (`84e6d661`) the lock covered only the create side: a launch took it after its first
   attach look and its config-change prompt, and then looked again. Since that fix the whole
   decision is inside the window. It is taken at the top of `runContainer`, before the orphan
   sweep, so a reaped orphan of this workspace leaves its host-services dir to this relaunch.
2. **The skills and briefing staging** under `AGENTS_DIR/<cname>`. Every launch of the workspace
   writes it — a fresh launch, and an attach refreshing it from the running jail's tree — and a
   podman jail binds it. Two concurrent syncs of one destination race: one removes the other's
   temporary sibling as an entry its source lacks, and the other's rename then fails.
3. **On macos-user, the content trees built from that staging** (the home overlay and the
   `/ctx` tree), which the orchestrator's stage copies for the sandbox, and the orchestrator's
   own bootstrap-through-stage window. The lock opens in `Run`'s native arm just before
   `refreshJailBriefings` and is handed to the orchestrator (`AcquireWorkspaceLockFor`), which
   releases it before the agent.
4. **The attach's contract gate** ([`attach-skew-and-contract-guardrails.md`](../design/attach-skew-and-contract-guardrails.md)).
   A gate that restarts the jail continues into the fresh launch under the same hold, which is
   what makes the stopped jail's teardown leave its host-services dir to the relaunch.

| Arm | Opens | Released |
| :--- | :--- | :--- |
| podman, Apple Container: fresh launch | top of `runContainer` | handed to the jail's keeper at its spawn, which releases it once the container is running (`awaitRunning`, keeper.go) |
| podman, Apple Container: attach | top of `runContainer` | once the contract gate has passed and the skills and briefing staging is refreshed, before the exec |
| macos-user | before `refreshJailBriefings` | by the orchestrator, before the agent |
| any other return | | `Run`'s deferred release |

**What it no longer covers.** Pack staging, on every backend. And on macos-user the host-daemon
start, which runs from the launch's own tree: two launches' spawns can contend again, so
[`OQ-HD10`](../design/host-daemon-ownership.md#OQ-HD10)'s experiment measures the spawn flock on
its own, as it would have before the staging fix.

**What it costs.** A second launch of a podman workspace waits for the first launch's attach
decision and fresh window (config-change prompt, image load, host-service start), and an attach
waits for any other launch's window. That has been so since the staging fix put the attach
decision and the config-change prompt inside the lock; before it, only the create side was. It no
longer waits for the other launch's staging.

**What is left:**

- **Loophole state retirement is still config-driven.** A pack dropped from `packs` has its
  loophole state archived by the next launch of any workspace, an attach included
  ([retirement](loophole-system.md#retirement-what-happens-when-a-pack-goes-away)), while a jail
  that booted with the pack keeps its tree and, until it stops, the daemon its launcher started.
  That was true before per-launch trees (an attach then also removed the pack's tree), and the
  ruling does not reach it.
- **An attach still refreshes the skills and briefing staging**, from the running jail's own
  packs. So pack content in them does not change on attach, but yolo's built-in skills, the LSP
  plugin `lsp_servers` renders, and the config-driven parts of the briefing still do, as before.
  None of that is read by the jail's binaries, so it is not pack-contract skew.
- **macos-user copies its tree into one per-workspace destination**, `<stateDir>/packs/<cname>`,
  which the next launch's stage replaces. A running session read it at its bootstrap only:
  `YOLO_PACK_ROOT` is set in the bootstrap's environment and in no session environment
  (`macosuser.buildBootstrapEnv`). So a second session does not change what the first rendered.
  READ FROM CODE, not run on a Mac.
- **A lock file that cannot be opened** warns and leaves the launch unserialised, because the
  workspace lock is a courtesy (`acquireWorkspaceLock`).
- 💬 <a id="oq-pk3"></a>**[`OQ-PK3`](#oq-pk3) — a host-scoped singleton that names `{loophole_dir}` in its `host_daemon.cmd` resolves it
  inside the launch's own tree**, and that tree goes once the launch's container is known gone,
  while the singleton, by design, outlives the jail that started it (`internal/broker`). A
  singleton that reads its module dir after it starts (a lazy import, a data file) then loses it;
  under the shared tree the dir lasted until the pack left `packs`. No shipped pack is affected:
  the shipped singletons self-exec `yolo internal daemon …`, and the one shipped `{loophole_dir}`,
  the audio pack's, is a per-jail bind. **Needs a ruling**, since each remedy changes something a
  pack author sees or adds a store: keep a tree while a singleton spawned from it lives (which
  also needs the orphan-staging reaper to honour that veto); resolve the token, for host scope
  only, against a stable content-addressed copy with its own liveness collection; or refuse the
  token in a host-scoped `cmd` and say why.

  <!-- vantage: oq id=OQ-PK3 leaning="Refuse {loophole_dir} in a host-scoped host_daemon.cmd, naming why: no shipped pack uses it, and keeping a tree alive for a singleton (a reaper veto) or a content-addressed host-scope copy (a new store) each add machinery for a case nobody has." -->

  _Leaning:_ refuse the token in a host-scoped `cmd`, naming why: no shipped pack uses it, and the
  other two add a veto or a store for a case nobody has yet.

- **Not verified on the macOS backends.** The unit and integration tests run on Linux podman.
  Whether `TestMacosUserTwoConcurrentLaunchesOfOneWorkspace` reaches its sessions, and whether an
  Apple Container attach finds its tree through the host record, are for a Mac run to show.

#### <a id="oq-pk2"></a>✅ [`OQ-PK2`](#oq-pk2) — does a running jail keep the pack tree it booted with?

**Ruled (c), 2026-09-26, and built the same day:** one immutable pack tree per
launch, plus a notice on attach (`newPackTree` and `noteBootedPackSetDiffers`, pinned by
`TestASecondLaunchNeverTouchesTheFirstLaunchsTree` and
`TestAnAttachWritesNothingIntoTheRunningJailsPackTree`). Chosen with the attach-skew stopgap
([`attach-skew-and-contract-guardrails.md`](../design/attach-skew-and-contract-guardrails.md#findings-since-filing-2026-09-26)):
an attach that re-stages hands a running jail pack contracts its binaries may not read, and
per-launch trees close that class. The options as they stood: an attach re-staged the tree a
live podman jail had bound, so after a config change the jail's `/ctx/packs` showed the new pack
set. Nothing in the jail is re-rendered from it
([above](#host-side-staging-then-jail-side-render)).

- **(a) Keep the attach refresh.** What shipped until this was built, with a residual boot-window
  mix: the first launch's lock was released once its container was running, which could be
  before its entrypoint had read `/ctx/packs`.
- **(b) One immutable tree per launch.** The jail binds its own tree at `/ctx/packs` and keeps
  it until it stops. Its trees are collected when its container is known gone, the way the home
  skeleton is ([`OQ-BH10`](../design/base-home-legacy-state.md#OQ-BH10)). This closes the
  boot-window mix, and it drops the lock's reason to span the macos-user host-daemon start.
- **(c) (b), plus a notice on attach** when the config's pack set differs from the one the jail
  booted with.

**Recommendation: (c).** Under (a) a live jail is inconsistent after a changed-config attach:
the launchers, config and shims a dropped pack rendered at boot stay, while its tree goes.
Under (b) the jail stays whole until it restarts, and the notice says a restart is needed.

**What was built** ([`packtree.go`](../../internal/cli/run/packtree.go); tests in
[`packtree_test.go`](../../internal/cli/run/packtree_test.go),
[`concurrentstaging_test.go`](../../internal/cli/run/concurrentstaging_test.go),
[`packprune_test.go`](../../internal/cli/run/packprune_test.go) and
[`attachpacktree_test.go`](../../integration/attachpacktree_test.go)). A running jail sees the
packs it booted with at `/ctx/packs` for as long as it runs, however the config changes. An
attach after a change prints, on stderr, which packs were added, removed or changed and that
`yolo stop` then a launch picks them up. A selection only the configured packs can satisfy (a
profile only a newly added pack declares), and a jail tree this yolo cannot read, take the
attach-skew disposition ([OQ-SK1](../design/attach-skew-and-contract-guardrails.md#OQ-SK1)): the
restart prompt at a terminal, a refusal naming the restart elsewhere, and
`YOLO_ALLOW_ATTACH_SKEW=1` to attach delivering nothing. The mechanism choices, each made to build
the ruling and none changing it:

1. *Implementation decision.* **Where a tree lives.** `AGENTS_DIR/<cname>/pack-trees/<UTC
   stamp>-<random>` (`paths.PackTreeRoot`, `os.MkdirTemp`, mode 0755), with the old layout
   inside it: `_official/<name>` and `<slug>`. Beside the home skeleton's root, so the reaper that
   removes a gone jail's whole `AGENTS_DIR/<cname>` reaches it the same way.
2. *Implementation decision.* **A tree records its own packs**, in `_pack-tree.json` at its
   top level (a name no pack slug can spell, as `_official` is not): each pack's name and directory, in the order the launch loaded them. An attach
   rebuilds the jail's pack set from it, and the directories alone cannot say it: a configured
   pack's directory is its slug, not its name, and the order decides precedence (later wins for
   skills and briefing prose). The jail's loader skips every top-level file, so the record renders
   as nothing.
3. *Implementation decision.* **An attach finds the running jail's tree through a host-side
   record**, `pack-trees/.live`, naming the tree. The fresh container path writes it before its
   container starts, holding the launch lock an attach also takes, so no attach finds the
   container without it; it is removed with the tree, and only while it still names that tree, so
   a late teardown cannot remove a restart's record. Not the container's environment or mounts:
   Apple Container copies the tree into a home the jail can write, and host code must never read
   that copy (`run.acPackRootRel` says why), so only a host record identifies the host tree there,
   and one mechanism serves both backends. A missing record, or one naming a gone tree, falls back
   to the shared tree a jail launched before this change binds, except on Apple Container (item
   13). When neither is there, the attach composes from the configured packs, as every attach did
   before, with a warning that it could not find the jail's tree and cannot say whether they
   differ. A tree that is there and will not load is item 11's case, not this one. The jail's tree
   is untouched either way.
4. *Implementation decision.* **An attach still stages the config**, into a tree of its own that
   nothing binds. It needs it to compare with the jail's tree, and it keeps refusing a config
   whose packs fail the pre-flights, as every attach did. It is discarded once the attach commits,
   before the exec, so an all-day session does not keep it; on a refusal it goes when `Run`
   returns. A contract gate that restarts the jail continues with that tree as the fresh launch's.
5. *Implementation decision.* **Every host-side reader of an attach reads the jail's tree**: the
   channel, the injected launch flags, the skills and briefing refresh, and the loophole and
   supersession records the briefing's loophole list reads. When the two trees' content is equal
   the declarations are byte-identical, so what `Run` composed from the configured tree is reused.
   When it differs, the channel is composed again over the jail's packs with the environment
   `Run` already hydrated (a second `env_sources` pass could prompt twice), and the launch flags
   are injected again, disclosed only when the command differs from the one already disclosed. An
   attach starts no host daemon, so no daemon reads a staged loophole dir on that path.
6. *Implementation decision.* **The notice** goes to stderr after the `Attaching` line (the
   attach's stdout opens with that line and then belongs to the command), names each pack added,
   removed or changed, and is silent when nothing differs. "Changed" compares a digest of each
   pack's directory: every entry's path, type and execute bit, and every file's bytes. So an
   embedded pack a newer yolo ships differently counts, which is exactly the skew case.
7. *Implementation decision.* **When a tree goes.** The fresh container path hands its tree to the
   container just before it starts (`Options.packTreeHeld`), and `forgetGoneContainer` removes it
   on the runtime's answer that the container is gone, at each of the three ends where the launch
   sees it end; "could not ask" removes nothing. A tree no container holds goes when `Run` returns:
   a refusal, an attach, a `--dry-run`, and a macos-user launch, whose sandbox copied the tree at
   its bootstrap and whose host daemons, which run from it, have stopped by then. A launch killed
   without a teardown leaves its tree to the reaper, the residual the home skeleton accepted
   ([`OQ-BH16`](../design/base-home-legacy-state.md#OQ-BH16)).
8. *Implementation decision.* **The shared tree a pre-change jail binds** (`AGENTS_DIR/<cname>/packs`,
   `paths.LegacyPackStagingDir`) is never written. An attach to such a jail reads it, naming a
   configured pack by the config entry whose slug its directory carries (on podman; item 13 says
   why not on Apple Container), and takes item 11's disposition when it will not load. The first fresh container
   launch whose runtime answers that no container of the name exists removes it, since only such a
   container could bind it. A macos-user launch never removes it: it has no liveness question it
   can ask about an older native session, so the reaper does.
9. *Implementation decision.* **No contract tag.** An attach writes no pack tree, so it asks
   nothing of an older jail's binaries, and it finds an older jail's tree on the host side
   (`launchContractTags`' comment records this).
10. *Implementation decision.* **The lock's new placement** is the table above: the pack tree left
    the window, and what stayed in it is what the launches still share.
11. *Implementation decision.* **A jail tree that is there and will not load is a known
    difference**, and takes the attach-skew disposition rather than item 3's warning. It is the
    common legacy case, not a corner: this build's loader refuses v0.10.0's claude, which declares
    the `claude_plugins` hook this build removed, so every jail v0.10.0 launched with claude binds a
    tree this build cannot read. Composing from the configured packs there delivered the newer
    shape, claude's list-valued `api_key_env_name`, into a jail whose derive reads it as a string,
    which is the ride-along [OQ-SK1](../design/attach-skew-and-contract-guardrails.md#OQ-SK1) rules
    out. Not a tolerant read of the old tree: the host's loader is strict by design, `TolerateSkew`
    is process-wide and in-jail only, and a read that drops what it does not know composes from a
    guess at the jail's packs. Under the acknowledgment such an attach writes nothing: no channel,
    and no skills or briefing refresh, since the only packs to refresh from are the configured
    ones. Pinned on the last release's real packs
    (`TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong`), which the release-decode allowlist
    cites.
12. *Implementation decision.* **A selection the jail's packs cannot serve takes the same
    disposition**, where the first build refused it outright. [`OQ-SK1`](../design/attach-skew-and-contract-guardrails.md#OQ-SK1)'s terminal arm applies to
    every attach that cannot be made compatible, so at a terminal the attach asks
    `Restart jail now? [Y/n]` and a yes continues into the fresh launch, still holding the lock, as
    a missing contract tag does. The headline names the packs the jail lacks (`this jail was
    launched without zai`), since the composition's own error tells the user to declare a profile
    their configured pack already declares. Under the acknowledgment the command still carries the
    jail's own launch flags.
13. *Implementation decision.* **On Apple Container the shared tree is not the booted set.** That
    backend copied it into the jail's home at the fresh launch, and every attach before per-launch
    trees re-staged the shared tree afterwards, so it holds whatever the config said at the last
    entry. An Apple Container attach with no live-tree record therefore takes item 3's warning
    rather than reading the shared tree as the jail's packs. Every restart remedy an attach names
    there is `container stop <name>`, since `yolo stop` cannot see an Apple Container jail
    ([G11](../plans/setup-support-gaps.md)).
## The credential boundary: disclosure, not consent

**Host access is six crossings**: a host file read (a `reads-host` contribution, or a config
surface's own `readsHost` — one kind of crossing, disclosed identically, declared in two
places because only one of them can name a file that is not a surface's twin — or a
provider's `region_file`, which yolo reads on the host for one value and mounts nowhere, and
which is claimed as `reads-host` with a sentence saying so); a `mount`
directory or file read;
`program` via `installer`, a curl-to-shell install URL; `briefing` with `after: "host:…"`,
prepending the user's own briefing file; a wrapped **plugin's** code-running components; and a
shipped **loophole's** daemon, intercepts, host binds and devices. Static `env` is not host
access — its values are literal strings — and neither is `derive.lua`, which is sandboxed and
whose one escape (`yolo.env`) is the same field the static `env` channel already carries
literally.

A pack has an **origin**: embedded (ships with yolo), local (a `file://` directory the user
controls), or fetched (cloned from a git ref). **Origin does not decide host access.** It names
the delivery route — a fetched pack reaches the store by a fetch, at a host launch or at
`yolo pack install`, and gets a lockfile entry with a commit — and nothing more. `packload.HonoredHostFiles`,
`HonoredMounts`, `HonoredInstalls`, `HonoredLoopholes` and `HonoredPlugins` refuse nothing;
their `refused` return is retained and always nil, and a future refusal source must not quietly
refill them.

### Why there is no approval gate

Selecting a pack means writing `packs` in the user config, as the host user. `packs` is **user
scope only and inexpressible at workspace scope by construction** — that is the load-bearing
restriction, and it is the one that survives, because a workspace config travels with a repo
and is agent-editable. **An agent cannot add a pack.** So a prompt at `yolo pack install`
refuses an actor who has already passed a strictly stronger gate, which
[`../reference/gate-placement-principle.md`](gate-placement-principle.md) Test 1 calls
theatre — and `internal/config/userlayer.go` already applied the same test, the same way, to
`--user-layer`, the other route into `packs`.

> [!WARNING]
> **Do not re-add a prompt, at `pack install`, `check`, `footprint` or the entrypoint.** The
> reason is not that prompting is unpleasant. The gate's original containment rationale — *a
> fetched pack must not `curl | sh`* — was refuted in-house: `npm install -g` runs
> `postinstall` from the same fetched tree, ungated, so the set refused one path to arbitrary
> in-jail execution while permitting another. **The absence is pinned, not merely documented.**
> `internal/packload/hostaccessgates_test.go`'s `TestNoFetchedPackHostAccessGateExists` walks
> every non-test `.go` file under `internal/`, `cmd/` and `packs/` over the **AST** and fails if
> any of fourteen named retired gate identifiers reappears (comments are exempt by
> construction, because the prose recording the deletion has to be able to *name* what it
> deleted). Its sibling `TestTheDisclosureThatReplacedTheGateIsStillWired` fails if the
> disclosure that replaced it goes missing, and `internal/cli/run/packnohostgate_test.go` is
> the behavioural half. **A hit in any of them is a claim that the ruling was reversed, and
> that belongs in [`../design/trust-paths.md`](../design/trust-paths.md) before it belongs in a
> `.go` file.** Deleting a row from the list to get green is the specific failure the test was
> rewritten to prevent: its predecessor enumerated the two gates *by name*, so a third gate
> copied into `yolo check` would have satisfied it vacuously. A scan for zero gates has no such
> hole.

> [!WARNING]
> **`npm install -g` and `curl | sh` really are treated alike now, and the asymmetry that
> remains is deliberate with a stated reason.** A reader who re-derives *"npm runs postinstall,
> therefore gating only the installer is inconsistent"* has found a real fact — it is just no
> longer an open finding. The distinction that survives is **when the bytes change**, not whose
> they are: an unversioned npm declaration re-checks the registry hourly, and a selector turns
> that off. Containment is not the discriminator, and cannot be: an npm package, an installer
> script, and a pack-shipped jail-side binary all execute inside the same sandbox.

> [!WARNING]
> **A digest-pinned installer script is not a digest-pinned binary.** An installer pinned by
> digest gives you *"the recipe is pinned"*, not *"what runs is pinned"*: the script fetches
> more at run time and those fetches are unpinned. A binary artifact has no second hop — the
> digest covers exactly what executes. Any rule that treats the two as equivalent because both
> carry a digest is overselling the installer case. This is why a pack-shipped downloaded
> artifact carries a mandatory `sha256`: the lockfile's commit pins the pack's *tree*, and a
> downloaded artifact is not in that tree, so without a digest "pinned pack" silently means
> "pinned manifest, unpinned executable".

### Transparency at every launch

The startup banner lists what each loaded pack reads this launch — its mounts, host-file
reads, installer URLs, host-prepended briefings, and env. **Disclosure is not consent**, and
that is the point: it stays because it tells the user what actually crossed, which no prompt at
install time can do.

Reads and executions print in different places, and the split is not cosmetic.
`run.notePackHostAccess` prints reads on the banner; host **execution** prints at the spawn
boundary, *before* the daemon starts (`startLoopholesDisclosed`), because for an exec a banner
line would be a notification that something already happened.

A third disclosure answers a question the other two cannot: **what did this launch do to the
command line I typed?** A pack's launch flags are injected after the binary the user named, so
`yolo -- copilot chat` runs `copilot --yolo chat` — and `--yolo` is `--allow-all-tools
--allow-all-paths --allow-all-urls` in copilot's own help. `run.injectLaunchFlagsDisclosed`
prints both argvs, one above the other, plus the pack that asked, and it is a WRAPPER around the
injector rather than a line beside it so the two cannot be separated by an edit. It prints
**only when something was actually added** — a flag the user already typed is not injected, and
a launch that rewrote nothing says nothing, because a disclosure that appears on every launch
saying "nothing" is how a disclosure surface becomes wallpaper. It is unsuppressible like the
other two ([`OQ-RO3`](report-tiers.md#why-its-this-way): a launch has no quiet mode).

> [!NOTE]
> **The same declaration has a second mechanism, and it discloses itself from inside the jail.**
> `entrypoint.packAliases` writes a `.bashrc` alias so an interactive `copilot` matches
> `yolo -- copilot`, and it builds that alias by running THIS INJECTOR over the bare argv
> `copilot` — the same function, the same `packload.LaunchInjection` record. It then states
> what it wrote (`entrypoint.discloseShellAliases`), at the boot that wrote it, because the
> host cannot: on the attach path it does not regenerate the file, and on macos-user it writes
> into a shell rc that backend's login zsh never reads. So there is no launch-stream line for
> the alias, and there is a boot line. Until 2026-09-13 there was neither, on the argument
> that `type copilot` prints the definition — a way to check the fact, never a way to be told
> it. Why the two mechanisms cannot become one:
> [`declaration-parity.md` §5.6](../design/declaration-parity.md#56-one-declaration-two-mechanisms-the-argv-rewrite-and-the-shell-alias).

> [!IMPORTANT]
> **Which kinds the disclosure covers is DATA, not a switch at the print site.**
> `disclosureClasses` in `internal/cli/run/packloopholes.go` classifies every kind in
> `packdecl`'s closed set into read / exec / skip, and
> `TestDisclosureClassifiesEveryKnownKind` fails when a kind is added and not classified. The
> hardcoded set it replaced was wrong for a year and nothing noticed, because "which kinds does
> the disclosure cover" was a fact only the printer knew. An *unclassified* kind defaults to
> exec, so a new kind's claims are announced before anything spawns even before someone writes
> the row down — correct at runtime, loud in review.

> [!WARNING]
> **A wrapped plugin's `hooks` and `mcpServers` are reported under the `skills` kind, which the
> banner's filter classifies as skip.** They therefore show in `yolo pack footprint` and in **no
> launch banner**, while the agent runs the hook at every tool call. Tracked as
> [`OQ-TP10`](../design/trust-paths.md#oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner) in [`../design/trust-paths.md`](../design/trust-paths.md), and pinned where the behaviour
> actually is by `run.TestWrappedPluginHooksAreDeliveredAndDisclosed`, whose doc comment names
> the banner as the gap.

## Command surface

| Verb | What it does |
| :--- | :--- |
| `yolo pack init [dir]` | scaffold a valid skeleton (`briefing/<pack>.md`, an example skill, `README.md`); never a `pack.json` |
| `yolo pack lint [dir]` | run the real staging executor **and** validate the manifest — every problem, not the first — then print the pack's footprint and every delivery, implicit broadcasts included, plus an info line for each conventional-looking file it will not ship (a root `AGENTS.md`, a subdirectory or non-`.md` file in `briefing/`) |
| `yolo pack ls` | list configured packs and what each stages |
| `yolo pack explain <name>` | stage one pack and show what it stages and what it dropped (`file://` local only) |
| `yolo pack footprint [ref]` | claims + cross-pack collisions + review summary; `[ref]` may be an embedded pack name or a local path, so you can inspect a pack you are authoring |
| `yolo pack install` | force a refresh of every configured git pack, a tag or a branch still inside its hour included (so it follows a re-pointed tag, which a launch never does), materialize each commit into the store, write the lockfile, report whether each pin **moved**, prune the entries of packs that left the config. Optional: a host launch fetches a missing pack itself |
| `yolo pack update` | everything `install` does, plus the refresh of npm-declared programs |
| `yolo pack status` | show locked commits and flag config/lock drift |

**No `yolo pack` verb asks a question, and `packMain` takes no stdin at all.** `install` and
`update` fetch and report; every other verb inspects. That is a property of the whole surface
rather than an omission from one row: the only reader ever threaded through here was the
fetched-pack approval prompt. `install` and `update` share the fetch body, a forced refresh
of every git pack. `update` adds the refresh of npm-declared programs ([`program`](#program)). The distinction a user cares about is *did my pins move*, which the
output reports directly, as a launch's `Fetched pack` and `Updated pack` lines do.

## What this does not license

- **Not a top-level config key per pack.** A contribution kind that declared one dies four
  ways: the inherit census is static and total-by-test in *both* directions, the inherit filter
  silently drops an unclassified key at the jail boundary, and the kind registry demands a
  Combine rule and a footprint claim string.
- **Not a route to `env` for a host daemon.** Settings are values written by core into a file;
  they are not a key/value channel into a process environment.
- **Not a reordering of validation after staging.** Two of the three `ValidateConfig` callers
  never stage at all, so there is nothing to reorder against.
- **Not pack content as an image input.** Content and config values are read at compose time;
  nothing about them needs to be in the derivation, and baking them would make editing a prompt
  cost a rebuild.
- **Not a claim that scope is a boundary in-jail.** User-vs-workspace is a host-notch property.
- **Not a second implementation of anything the framework owns.** A pack points at a loophole
  module rather than inlining its manifest; a pack projects from core's derive sources rather
  than inventing one; a pack requests a named hook rather than shipping its body.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs — those IDs are
cited from code comments and sibling docs, and this appendix is where they resolve. The two
rows marked *(profiles)* are the retired profile-variant design's, and resolve in full in
[`providers.md`](providers.md#the-profile-variant-rulings), which is the index for that arc's
other rulings. Ids from the folded-in designs keep
their original spelling and are QUALIFIED where the bare id already means something here:
*briefing defaults* (the `briefing/` convention — its P1–P6, R4 and R5 are
[in the `briefing` section](#briefing)) and *transform removal* (its P1, P3, R1 and R3 are
[in the derive slot](#lt-p1) and [the managed floor](#the-managed-floor)). [`OQ-AL1`](#oq-al1)
and [`OQ-AL2`](#oq-al2) come from the additive config-lists design, folded into
[the `config-list` section](#adding-entries-to-an-array-config-list); their ids are unambiguous
here, so they carry no qualifier. *Pack drop* qualifies the four rulings of the dropped-pack
retirement work, folded into [its section](#retiring-a-dropped-packs-host-output). Go comments
mostly cite them BARE: in the dropped-pack code (`applyhostprune.go`, `applyhostoverlaykeys.go`,
`hostoverlayprune.go`, the retire call in `cli.applyHostSurveyed`, and the dropped-pack checks in
`cli.applyHostSkills`) a bare R1–R4 is one of these four, and only a few comments add
"(pack drop)". Outside that code a bare R-number is not a pack-drop ruling: in `apply.go`'s
surface-ownership and overlay-provenance checks it is one of the config-overlay rulings below.
<a id="open-rulings"></a>*Open rulings* are the
five rulings of 2026-07-26 on where composition runs and what a dropped pack leaves: Rulings 2–4
are below, Ruling 1 is in [`config-migration-to-prism.md`](config-migration-to-prism.md#ruling-1),
and Ruling 5 is [`A12`](jail-home.md#why-its-this-way). <a id="pack-batch"></a>*Pack batch* ids
([§6a (pack batch)](#batch-6a) through [§6c (pack batch)](#batch-6c), and
[§7 (pack batch)](#batch-7)) are the section numbers of the 2026-08 pack batch, which code comments
cite, often prefixed with `roadmap.md` or `plan` after the documents those sections lived in first.
A Go comment citing `pack-system.md` by section number 7 uses an older numbering of this
system's docs: in the derive and Lua comments it means [the derive slot](#the-derive-slot), and
in the config-overlay comments (R2, R3) it means the R1–R5 table below, never
[§7 (pack batch)](#batch-7). The unqualified **R1–R5** below are the config-overlay rulings.

| Ruling | Why it holds |
| :--- | :--- |
| **R1** — a same-identity `config` declaration is a loud collision, not a merge | The `mode` flip is a general defect in the mechanism, not one pack's impoliteness; matching the owner's mode fixes one pack and leaves the next one able to do the same damage. |
| **R2** — an ownerless `config-overlay` is inert and reported by name | Creating the file would let an overlay own a surface by accident; failing the launch would make an unselected pack an error. It also fails in the useful direction. |
| **R3** — provenance must be USER-VISIBLE, in `yolo config diff` | Provenance nobody can read does not make an override legible, which was the whole justification for the kind. |
| **R4** — the double `rendered` line is fixed by REFUSING, not deduping | Deduping hides the clash; refusing removes the state that produced the second line. |
| **R5** (corrected) — a user-scope list is a ceiling a workspace can only WIDEN | Lists union-merge at every depth and the replace-wholesale exception was deleted deliberately, so "the weak scope is bounded by the strong one" is false for any list-shaped setting. |
| <a id="pd-r1"></a>**R1** (pack drop) — removing a dropped pack's `skills` and `files` output from a real home is CONFIRMED, once, only when something would move, never in a dry run, fail-closed on a nil or EOF stdin; a decline leaves the exit status alone ([the prompt](#retiring-a-dropped-packs-host-output)) | The user edited a config list; the consequence is files leaving their home, and that gap needs naming at the moment it happens. A decline that failed the apply would make every scripted `--assert` after a drop fail forever, with no non-interactive way to answer. |
| <a id="pd-r2"></a>**R2** (pack drop) — retirement ARCHIVES, never deletes, reclaimed by `yolo prune`'s host-render archive sweep | The authority to remove comes from an ownership record that can go stale, so being wrong must cost the user one `mv` back. |
| <a id="pd-r3"></a>**R3** (pack drop) — a dropped pack's `config-overlay` keys ride the SAME prompt; and provenance never launders an unclaimed key yolo force-wrote into `host` ([the `retired:` label](#the-retired-provenance-label)) | The key is a value, not content, but it sits in a file the user owns, so it gets no silent path of its own; two prompts for one edit to `packs` would teach the user to stop reading them. The record exists so yolo can tell its output from the user's, and a record that relabels yolo's key as the user's defeats every mechanism that asks. |
| <a id="pd-r4"></a>**R4** (pack drop) — briefing retirement stays OUTSIDE the prompt | Every byte moved is one yolo composed: a briefing destination is generated wholesale. Pinned by `TestApplyHostRetireDoesNotGateBriefingRemoval`, so the two halves cannot be quietly unified. |
| <a id="open-rulings-2"></a>**Ruling 2** (open rulings) — pack selection is user scope only; state a pack keeps across jails is pack-declared (a `machine`-scope [`state`](#state) contribution, the `shared_credentials` and `shared_directory` hooks); dropping a pack deletes nothing it left in a workspace | A repository needs no distribution mechanism to reach files it already owns, so there is no workspace pack scope. Declaring shared state per pack is what let claude's credential directory become pack data instead of a path core hardcoded. A workspace clean-up on drop would have nothing to walk, since nothing enumerates workspaces, and the render already stops composing a dropped input: a re-selected pack finds its state intact, and `<workspace>/.yolo/` is the user's to delete. Nothing reports what a drop left in a workspace. The real home is handled differently on purpose ([retiring a dropped pack's host output](#retiring-a-dropped-packs-host-output)). |
| <a id="open-rulings-3"></a>**Rulings 3 and 4** (open rulings) — for a jail launch, composition runs in the jail and only what needs the host runs host-side; a running jail is never re-rendered ([staging](#host-side-staging-then-jail-side-render)) | Where composition runs was a free choice, since the engine composes with no container at all ([`what-yolo-is.md`](what-yolo-is.md#the-separability-test-could-you-use-the-config-engine-without-a-jail)). Moving it host-side would have bought earlier error reporting, and Ruling 5 recovered that directly by making a failed generator fatal at boot. Re-rendering a running jail was ruled unsupported, which removed the one reason left to move. |
| <a id="batch-6a"></a><a id="batch-6a-2"></a>[**§6a**](#batch-6a) **and** [**§6a-2**](#batch-6a-2) (pack batch) — `briefing` and `skills` destinations are composed wholesale at every notch, and a user's own prose and skills MOVE into the conventional local pack, archived only when they cannot move ([`briefing`](#briefing), [composed-file posture](#composed-file-posture-what-writable-means)) | A destination two parties own needs a negotiation (a marker block for prose, refuse-what-yolo-cannot-prove for skills) and a second mechanism beside the jail's, which already composed wholesale. One owner and one mechanism remove the question at every notch. Archiving alone is safe but is not a migration: moved content still reaches the user's agents, and one copy cannot drift per agent. Maintainer rulings, 2026-08-04. |
| <a id="batch-6a-3"></a>[**§6a-3**](#batch-6a-3) (pack batch) — conventions over configuration: a `skills` or `briefing` source defaults to its conventional location, and a destination's `into` is always declared by the pack that owns the agent, never inferred by core | A destination has one right answer per agent, so core inferring one would mean core knowing the agent set, which is what `packs` states. When one notch inferred a destination and the other did not, a pack rendered at one and silently rendered nothing at the other. |
| <a id="batch-6a-4"></a>[**§6a-4**](#batch-6a-4) (pack batch) — every notch reads a pack's sources through one resolver | A `from` that one notch honored and the other ignored was a silent divergence. The resolver is now `packload.GovernedSources` ([R5 (briefing defaults)](#briefing-r5-row)), and the fallback chain that once stood behind a missing `from` is gone ([P4](#briefing-p4)). |
| <a id="batch-6a-5"></a>[**§6a-5**](#batch-6a-5) (pack batch) — host skills delivery asks whether a path is yolo's, never whether it is this pack's, so precedence is layer order | Asking "is it this pack's?" left a later layer unable to replace another pack's recorded entry, so the local pack lost at flat tier. Winning that collision was later given up on purpose: two packs claiming one unnamespaced name at the host is fatal ([the collision warning](#skills-collision), maintainer ruling 2026-08-05), the local pack included. A test of this rule applies twice, because one apply is decided by that run's own claims and the defect lived in the saved record. |
| <a id="batch-6a-6"></a>[**§6a-6**](#batch-6a-6) (pack batch) — the briefing ownership record is a file of its own (`host-briefing-manifest.json`); the pack set is re-resolved once after a confirmed migration; an incomplete pack set retires no briefing (`PackSetComplete`, false at its zero value) | A composed briefing belongs to the whole pack set, so its record's owner is a pseudo-owner no config can name, and read by the skills record's dropped-pack pass it retired every composed briefing on the next apply. The migration creates the local pack, so a set resolved before it lacked the user's prose for one run. And a wholesale destination does not heal itself the way the old marker block did, so archiving an unreachable pack's briefing cost the user a trip to the state dir. Each defect was found only by running the lifecycle. |
| <a id="batch-6a-7"></a>[**§6a-7**](#batch-6a-7) (pack batch) — a silent retire is only yolo's own upkeep: a pack removed from `packs` is retired behind the confirmation on every path, the render's included (`ComposeRequest.Configured`, [R1 (pack drop)](#pd-r1)); an incomplete pack set retires nothing on either path; an entry already byte-identical is neither copied nor archived; a hand-authored plugin directory is never migrated as a skill; `skills_tier` still decides a skill's invocation shape | "The pack I still have stopped shipping this skill" and "I removed a pack" are different user actions, and the render reaches a dropped pack's entries before the prune does, so gating only the prune let the confirmation stop firing. An offline apply archived an unreachable pack's skills while reporting success. Archiving on every apply grew an unchanged home without bound. A plugin moved into `skills/` would be delivered again under a different namespace, breaking the paths its own manifest declares. The tier survived wholesale composition because dropping it would rename every namespaced invocation, which nobody asked for. |
| <a id="batch-6b"></a>[**§6b**](#batch-6b) (pack batch) — a kind means one thing at every notch. The notch is DECLARED by the constructor that builds a render target, never inferred from its shape (a bare target gets `KindUnset`), and a mechanism chosen for the jail is not the kind's definition. Its items: `skills` unified (D1, [§6a-2](#batch-6a-2)); which modes a notch runs, and which keep provenance, are census data a new notch must state (D2); `env` is unbuilt at the host because `yolo host apply` launches no process, a limit of the command and not of the notch (D3) | Inferred from shape, a `guest` target resolved silently to jail semantics; declared, adding a notch is a compile-time question. `stateful` exists because yolo regenerates a file an agent may edit, not because a jail home is disposable (it is not), so the mode is not jail-shaped. `files` keeps refuse-what-yolo-cannot-prove at the host, since it has no layer model to regenerate from. |
| <a id="batch-6c"></a>[**§6c**](#batch-6c) (pack batch) — confinement is not a pack. A notch's policy, the autonomy bit included, comes from its confinement profile (`render.Profile`), and core knows notch NAMES only at its two edges: `render.KindForNotch` on the way in, `Kind.String()` on the way out | A pack is content yolo renders; confinement is enforcement yolo executes, and it decides whether a pack may read the host at all, so a pack declaring it would let the confined thing choose its own confinement. The two also compose oppositely: pack kinds by merge, where the later pack wins, and confinement primitives by intersection, where the strictest wins. The profile replaced a hardcoded `true` or `false` at each call site, the boot loop's included. Letting a user assemble a primitive vector stays out of scope ([`happy-path-principle.md`](happy-path-principle.md)). |
| <a id="batch-7"></a>[**§7**](#batch-7) (pack batch) — `yolo pack lint` asks "does this pack do anything?" and "does it stage content nothing reads?" as separate questions ([its checks](#pack-lint-questions)) | The rule it replaced used "ships `skills/` or `AGENTS.md`" as a proxy for "does anything read this pack". That rejected every shipped pack and a working config-only pack, and gave a pack that did nothing the same message, so it was useless in the one case it existed for. Maintainer framing, 2026-08-04: *"a pack that does absolutely nothing should be warned about, but not one that leaves out a part it could ship."* What shipped is stricter than the word "warned": both checks fail lint. |
| <a id="oq-al1"></a>[**OQ-AL1**](#oq-al1) — a `config-list` path captures PER ENTRY — relative to the last render on `stateful`, to yolo's insert record on `rmw` — and a mechanism that cannot is refused at launch ([capture per entry](#config-list-capture)) | A whole-array capture of one `pi install` would hold every pack's entries, outrank every contribution and freeze the list: later additions masked, a dropped pack's entries never removed. The obvious shortcuts each fail: limiting the kind to `computed` surfaces does not reach pi's `settings`, making the path `computed` wipes every `pi install`, and an owner opt-in per path protects nothing, since any pack's `config-overlay` can already replace the key. |
| <a id="oq-al2"></a>[**OQ-AL2**](#oq-al2) — list contributions apply after every ordinary overlay; only capture, `computed` and `managed` replace the final array ([order](#config-list-order)) | An overlay has no per-entry veto to express, so folding lists below overlays would let any overlay silently erase them. |
| **[OQ-K1](#why-its-this-way)** — settings declarations are AUTHORITATIVE, never advisory | A launch fetches a git pack it does not have before it resolves anything ([`OQ-PF1`](#oq-pf1)), and a pack still unresolvable after that is fatal, so there is no launch where a configured pack's declaration is missing and the jail starts anyway. (Ruled when launches never fetched; the fetch changes how a pack arrives, not what happens when one cannot.) |
| **[OQ-K2](#why-its-this-way)** — a workspace may supply values reaching a host daemon, gated by the config-change flow | The conditional IS the ruling: it would have been unsafe before the approval snapshot moved out of the workspace and non-interactive auto-accept was removed. |
| **[OQ-K3](#why-its-this-way)** — freeze the host-processes visibility list | Live re-read of the workspace file is indistinguishable from the hole: the same property that lets you widen without restarting lets an agent widen its own, mid-session, with no approval gate. |
| **[OQ-K4](#why-its-this-way)** — a loophole with a top-level core config key becomes an ordinary pack | The scope rule arrives for free by making it ordinary; leaving the key in core is separation in appearance only. |
| **[OQ-CAP](#why-its-this-way)** — `supersedes` is a top-level manifest key, not a kind | A contribution that contributes nothing is a category error; the thing that IS a contribution is the loophole. Pinned rejected by `TestSupersedesIsNotAContributionKind`. |
| **[OQ-TP6](../design/trust-paths.md#decision-ledger)** — a refused pack contribution refuses the launch | Withheld-with-a-notice let a jail come up missing what it was told to load. |
| **[OQ-TP9](../design/trust-paths.md#decision-ledger)** — no fetched-pack host-access approval gate; disclosure replaces it | The gate refused an actor who had already passed a stronger one, and its containment rationale was refuted by `npm install -g` running `postinstall` ungated. |
| <a id="oq-pf1"></a>[**OQ-PF1**](#oq-pf1) — a launch fetches and refreshes git packs itself, and the ref decides what moves: a pack not in the store is fetched, a commit or tag is never re-fetched, a branch is re-fetched at most hourly, and every move is disclosed ([fetch, refresh, lock](#fetch-refresh-lock)). Maintainer ruling, 2026-09-25: *"yes, I want this"* | Both reasons for keeping the fetch in `yolo pack install` were gone. The approval prompt that had to run there was deleted by [`OQ-TP9`](../design/trust-paths.md#decision-ledger). The claim that a launch is offline was false: every launch already reaches the network for the nix build, the bootstrap npm installs and the agent launchers' evergreen updates. What the old rule really protected is that host-side content (loophole daemons, `reads-host` reads, `yolo host apply` renders into the real home) never moves silently. The ref rule keeps that: a tag or commit pin still does not move until an explicit `yolo pack install` or `yolo pack update`. The `Fetched pack` and `Updated pack` lines say when anything did. |
| **[OQ-1](providers.md#pv-oq-1)** (profiles) — `autonomy` and `profile` stay two kinds | The confinement-conditional keys live in `autonomy` "and nowhere else", and the selectors are asymmetric: autonomy keys off the constructor-only fail-closed notch, profile names arrive through a merge an agent can edit. |
| **[OQ-16](providers.md#pv-oq-16)/[OQ-17](providers.md#pv-oq-17)** (profiles) — the `profile` gate on `config-overlay` reads user-scope config at the HOST notch | A gated overlay rewrites real-home keys and a workspace config is agent-editable; inside a jail the blast radius is the disposable home, so the jail notch uses the full effective table. |
| **Q1.3** — `requires` is its own kind, CombineShared | Install and presence are different claims; conflating them made a pack either lie about a baked binary or lose its `install_hints` entirely. `requires` owns no path, so many packs requiring one binary is not a collision. |
| **Q2.1** — several `program` contributions per pack, each with its own launcher | Exclusivity is per `bin`; `shellcheck` + `shfmt` in one pack is ordinary, and there is no case for constricting packs. |
| **Q3.1** — prune only unconfigured slugs, contents-only; keep the unresolvable. **Superseded by [OQ-PK2](#oq-pk2)**: each launch stages a tree of its own, so there is no shared tree to prune | Clear-then-restage would discard a pack the user still wants because it could not be fetched; the staging root's inode is captured by a live jail's bind. Both still hold, and per-launch trees meet them by construction: no launch writes another's tree. |
| **[OQ-PK2](#oq-pk2)** — one immutable pack tree per launch, plus a notice on attach; built 2026-09-26 | An attach that re-staged handed a running jail pack contracts its binaries may not read (v0.10.0 cannot boot on this tree's claude and pi), and left a live jail inconsistent after a changed-config attach. |
| **WB-D9..D12** — `needs` names an embedded pack, resolves before staging, joins user selection, and always prints | A fetched pack needs-ing another would make selection a supply-chain channel; a silent join is the one forbidden behavior. |
| <a id="oq-pb1"></a>[**OQ-PB1**](#oq-pb1) (briefing defaults) — shipped prose lives in a `briefing/` directory of `*.md` files at the pack root, read one level deep, ordered per pack and never globally | A root `BRIEFING.md` would be shipped content spelled in the repository's grammar (uppercase root Markdown), which is how `AGENTS.md` got its second reader, and one file cannot be the unit per-file governance routes. A global sort across packs would let one pack's filenames reorder another pack's rules. |
| <a id="oq-pb2"></a>[**OQ-PB2**](#oq-pb2) (briefing defaults) — `AGENTS.md`, `CLAUDE.md`, `GEMINI.md` are refused as a SOURCE at any depth, naming the move | Agent tools read those names in subdirectories too, so depth does not make one safe, and an allowed opt-in (`from: "AGENTS.md"`) returns the dual-reader defect one pack at a time. One text for both readers is already spelled `CLAUDE.md` → `@briefing/x.md`. |
| <a id="oq-pb3"></a>[**OQ-PB3**](#oq-pb3) (briefing defaults) — the cut is hard: a root `AGENTS.md` stops being read, with no refusal, no notice at launch or host apply, and no hatch | A notice would fire forever on every correctly-unshipped repository guide; the pack may be someone else's repository, so it cannot be a refusal; and a hatch would keep the second reader alive behind a variable. `yolo pack lint`'s listing is where an author looks. |
| <a id="oq-pb4"></a>[**OQ-PB4**](#oq-pb4) (briefing defaults) — no separate rule for fetched packs | Selecting a pack is the consent, as for every other kind, and the lint listing is the disclosure. A rule keyed on how a pack was fetched makes one pack behave two ways; a per-file consumer toggle waits for a real third-party pack that abuses always-on prose. |
| <a id="oq-pb5"></a>[**OQ-PB5**](#oq-pb5) (briefing defaults) — two content contributions of one kind naming one source are refused, naming both | They contradict "one governing contribution per file", and every legitimate shape has a one-contribution spelling (`agents: [a, b]`, or silence). Strict decode only; the tolerant in-jail read folds a repeat so each file is still delivered once. |
| <a id="briefing-r5-row"></a>**R5** (briefing defaults) — one predicate, `packload.GovernedSources`, for every notch that reads a pack's sources | The gate used to live at three sites that agreed, which is how one trap reached every notch; a fourth site with its own gate drifts. See [the warning](#briefing-r5). |
| <a id="oq-lt1"></a>[**OQ-LT1**](#oq-lt1) (transform removal) — the config transform (`config.lua`, `host_files[].transform`, a surface's `transform`) is deleted outright: no named refusal, no deprecation window | It had no user, and permanent named refusals would be code written for nobody. A stale key is still loud for free: the `host_files` key set (`knownHostFileKeys`) is closed and surface and overlay decoding is `DisallowUnknownFields`, so each is refused as an unknown key — the basis the ruling rests on, pinned by `TestHostFilesRetiredTransformKey` and `TestDecodeOverlayRejectsUnknownField`. A leftover `config.lua` file is simply never read. Do not add a `validateJournalRetired`-style refusal for it, and do not widen `knownHostFileKeys` back. |
| <a id="oq-lt2"></a>[**OQ-LT2**](#oq-lt2) (transform removal) — principle 4 of the agent-settings composition design ("Lua, not a data-filter vocabulary") retires with the transform; the two gaps it leaves are accepted | Nothing declarative removes or rewrites an entry inside a host-supplied array, or edits one conditionally on its value; [`config-list`](#adding-entries-to-an-array-config-list) (2026-09-24) only APPENDS entries, and a merge patch still replaces the array whole. Nothing makes a declared, portable, *partial* edit of a raw file the host also owns. The answer is `capture` once (state) or `content:` (a jail-specific copy); a declarative `filter`/`replace` op is designed against a real case or not at all. Do not reintroduce a post-merge script slot to close either gap. |

## Current values

Verified at `7ad8358c`; the `config-list` rows at `5a44129d`; the two fetch rows were read from the working tree of the 2026-09-25 change that adds them, before it was committed. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Branch refresh interval | one hour: a branch-pinned git pack is re-fetched at launch when its last successful fetch is older ([`OQ-PF1`](#oq-pf1)) | `packsrc.BranchRefreshInterval` |
| Launch-time fetch timeout | 60 seconds per repository fetch, shared by its clone, fetch and checkouts, against the store's 2-minute default for `install` and `update` | `packsrc.LaunchFetchTimeout`, `packsrc.Store.Timeout` |
| Kind set | the `footprints` map key, from which `KnownKinds()` derives (sorted alphabetically) | `packdecl.footprints`, count-pinned by `packdecl.TestKnownKindsCoverEveryConstant` |
| Hook set | `shared_credentials`, `shared_directory`, `unshare_directory`, `per_jail_history`. `claude_plugins` was a member until it was retired ([`OQ-2`](../design/pi-pack-extensions.md#10-decision-ledger), 2026-09-19 — retire it and add nothing like it, no agent-named hook); the name is not unknown but REFUSED, with a migration message | `packdecl.KnownHooks`, drift-pinned by `entrypoint.TestHookSetsAgree`; the refusal is `packdecl.RetiredHook` |
| Manifest top-level keys | `name`, `description`, `contributes`, `skills_tier`, `supersedes`, `needs` | `packdecl.Manifest` |
| Conventional briefing source | every `*.md` directly inside `briefing/` | `packdecl.DefaultBriefingDir`, `packdecl.ConventionalBriefingFile` |
| Never a briefing source | `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, at any depth | `packdecl.RepositoryInstructionFile` |
| Which contribution governs each source | one per file, destinations excluded | `packload.GovernedSources` |
| Briefing source order within a pack | byte-wise by cleaned pack-relative path | `packload.GovernedSources` |
| Local pack's migrated prose | `~/.config/yolo-jail/local/briefing/local.md`; legacy `local/AGENTS.md` | `entrypoint.LocalPackBriefingRel`, `entrypoint.LegacyLocalPackBriefingRel` |
| Scaffolded briefing file | `briefing/<pack>.md`, or `briefing/prose.md` for a reserved name | `cli.scaffoldBriefingRel` |
| Conventional skills source | `skills/` | `packdecl.Contribution.SkillsSource` |
| Blocker dir (first on PATH) | `~/.yolo/bin/block` | `entrypoint.BootPath` |
| Launcher dir (second on PATH) | `~/.yolo/bin/launch` | `entrypoint.BootPath` |
| PATH order | `block : launch : $NPM_CONFIG_PREFIX/bin : <mise-shims> : $GOPATH/bin : $HOME/.local/bin : /run/yolo/packages/bin : /bin : /usr/bin` | `entrypoint.BootPath` (and two independently-written copies: the `.bashrc` export, `macosuser.SandboxPath`) |
| Shim bypass | `YOLO_BYPASS_SHIMS=1` | `internal/entrypoint` |
| Unversioned npm launcher poll | hourly | `entrypoint` launcher generation |
| `install_hints` managers | `brew`, `brew-cask`, `apt`, `dnf`, `pacman`, `nix` | `packdecl` install-hints validation |
| Mounted pack root | `YOLO_PACK_ROOT` | `internal/cli/run/packs.go`, `packsrc.Store` |
| Host staging root | `<global storage>/agents/<container>/packs/<slug>` | `paths.AgentsDir`, `PackEntry.Slug` |
| Lockfile | `~/.config/yolo-jail/packs.lock.json` (beside the user config) | `packsrc/lock.go` |
| Conventional local pack | `~/.config/yolo-jail/local` (`briefing/`, `skills/`) | `paths.LocalPackDir` |
| Config-surface layer order | `defaults < host < workspace < config-overlay < config-list < capture-overlay < list-capture < computed`, then the managed floor | `internal/agentcfg` (`Compose`, `listcontrib.go`) |
| `config-list` records | `stateful`: `<agent>-<name>.list-capture.json` beside the overlay; `rmw`: `<agent>-<name>.list-record.json` under the provenance directory. Each is written only for a surface with a list path | `render.Target` (`ListCapturePath`, `ListRecordPath`) |
| Managed null | JSON: assigned (renders `null`); TOML: deletes the key | `agentcfg.enforceManaged`, `agentcfg.enforceManagedTOML` |
| Derive VM run budget | 5s wall clock | `luahook.DefaultTimeout` |
| Derive sandbox libraries | base, string, table, math (minus `math.random` and `math.randomseed`) | `luahook.openSandboxLibs`, `ForbiddenGlobals`, `extraStrippedGlobals` |
| Packs shipping a `derive.lua` | `agy`, `claude`, `codex`, `copilot`, `omp`, `opencode`, `pi` | `packs/*/derive.lua`, all run by `TestEveryShippedPackDeriveStillRuns` |
| Surface modes | `stateful` (default), `computed`, `rmw`, `unrendered` | `internal/agentcfg/manifest` |
| `state` scopes | `workspace` (default), `machine` (requires `because`) | `packdecl` |
| `skills_tier` values | `""` / `flat` (default), `namespaced` | `packdecl.Manifest.SkillsTier` |
| Derive registrations | `yolo.derive(agent, surface, fn)`, `yolo.env(agent, fn)` | `internal/agentcfg/luahook` |
| Derive helpers | `yolo.model_for(alias)` → `"<provider>/<id>", "<id>"` or `nil`, for the selected provider only; warns for a missing `default`, `fast`, `balanced` or `frontier` | `luahook/modelfor.go` |
| Derive ctx sentinels | `ctx.tombstone`, `ctx.empty_array`, `ctx.in_full(t)` | `luahook/derive.go` |
| Derive ctx sources | live tables `ctx.mcp_servers`, `ctx.lsp_servers`, `ctx.providers`, `ctx.use_profiles`; scalars `ctx.agent`, `ctx.surface`, `ctx.selected_provider`, `ctx.profile_name`; `ctx.profile` | `luahook.DeriveCtx`, `knownDeriveSources` |
| Loophole settings token | `{settings}` in a manifest `cmd` | `internal/loopholes/settings.go` |
| Settings types | `string`, `bool`, `int`, `string_list` | `loopholedecl/settings.go` |
| Settings scope default | `user` | `loopholedecl/settings.go` |
| Cgroup loophole name constant | `paths.BuiltinCgroupLoopholeName` | `internal/paths` |
| Core-owned config surfaces | `mise/config` and nothing else | `agentcfg.BuiltinManifest` |
| Host ownership records | `~/.local/share/yolo-jail/host-skills-manifest.json` (per-entry `skills` and `files`); `host-composed-skills.json` beside it (composed skills) | `cli.hostSkillsManifestPath`, `cli.hostComposedSkillsManifestPath` |
| Host-render archive | `~/.local/share/yolo-jail/archive/<bucket>/<stamp>/`, one bucket per kind plus `retired` for a dropped pack's `skills` and `files` | `cli.hostArchiveRoot`, `cli.archiveBucketRetired`; swept by `prune.PruneHostArchiveBuckets` |
| Retired provenance label | `retired:<the layer that last claimed the key>` | `agentcfg.RetiredLayer`, `agentcfg.RetiredOf` |
