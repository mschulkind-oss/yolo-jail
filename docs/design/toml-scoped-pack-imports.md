---
title: "Direct user-level scoped pack imports in TOML"
status: accepted
stage: DECIDED
next: "Build the modern TOML producer from the companion plan"
depends-on:
  - ../research/toml-scoped-pack-imports.md
tags: [packs, skills, toml, design]
---

# Direct user-level scoped pack imports in TOML

**Status:** 2026-11-17. Behavior authored against `a4dfc67d847d7d7c0b5fd06515cf93e86a06ad54`;
source seams and carrier facts rechecked at that commit. Nothing implemented. The behavior
and typed-directory carrier closure are accepted; no owner or engineering decision remains.

> **In short.** A user-level pack entry can deliver selected skills across projects without
> a wrapper pack, while every unselected contribution stays inert.

**Why it matters.** Environment management needs an affirmative selected-skill contract;
file filtering can retain executable declarations or accidentally withhold requested skills.

**Start at:** [the syntax](#syntax), then [projection semantics](#projection-semantics).

**Needs your ruling:** None. The remaining work is author contract closure, not owner policy.

**Reads with:** [the research](../research/toml-scoped-pack-imports.md) (verified baseline facts),
[the companion plan](toml-scoped-pack-imports-plan.md) (complete build hand-off).

## Goal and boundaries

Modern **user-level TOML** pack entries directly narrow imports. Selected upstream skills reach
all projects without per-project installation. No wrapper pack is required.

This adds no workspace pack authority, workspace `skill_imports` behavior, broker permission,
credential path, per-skill config alias, contribution-kind selector or legacy JSONC feature.
The conventional automatically appended user `local` content pack is not an entry and cannot
be scoped. Existing unfiltered entries retain their behavior.

**Projection** *(coined here)* means the effective selection record plus resolved skill rows,
not a filtered source checkout. **Typed mode** *(coined here)* enumerates a pack's own skill
contributions. **Raw mode** *(coined here)* treats an explicit directory as data, without reading
its manifest or interpreting pack conventions. These are two candidate modes of one projection.

## Syntax

```toml
packs = [
  "claude",
  { git = "https://example.invalid/acme/corpus", ref = "v1.2.0", skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } },
]
```

Other accepted forms:

```toml
packs = [
  { pack = "corp/tools", skills = { exclude = ["experimental-*"] } },
  { pack = "local/mine", skills = { only = ["*"] } },
  { git = "https://example.invalid/acme/mono", subdir = "packs/review" },
  { pack = "corp/skills", subdir = "core-tools", skills = { only = ["lint"] } },
  { git = "https://example.invalid/acme/one-skill", ref = "main", bare = true, skills = { dir = "." } },
]
```

| Key | Type | Meaning |
| :--- | :--- | :--- |
| `subdir` | String | Pack root relative to the entry's existing root: repository root for `git`, handle directory for `pack`. Alone, whole-pack semantics at that root. |
| `skills` | Table | Presence makes the import skills-only. At least one of `dir`, `only`, `exclude` is required. |
| `skills.dir` | String | Raw-mode directory relative to the pack root; `.` means that root. |
| `skills.only` | String array | Candidate-name selectors to retain; omission means all candidates. |
| `skills.exclude` | String array | Candidate-name selectors removed after inclusion. |

Existing `git`, `ref`, `pack`, `bare` and `namespace` meanings and exclusions remain intact.
`[[packs]]` is accepted as the same TOML table-list value model. String/table mixing follows
the existing reader. Strings cannot carry selection. Three-component handles remain refused,
with a suggestion to write `{ pack = "corp/skills", subdir = "core-tools" }`.

The schema and semantic parser both admit the keys. `pack_sources` gains nothing. Keys inherit
user-only scope, including user fragments. Per-entry format dispatch keeps legacy-origin
items on the unchanged reader. Shipped table entries such as `{ pack = "yolo/<name>" }`
allow typed selectors; `subdir` and `skills.dir` are refused for shipped identity.

## Projection semantics

### Pack root and typed candidates

Without `skills`, load the whole pack at the relocated root: manifests, conventions,
closure, plugins and disclosures retain ordinary semantics.

With `skills` but no `dir`, read the source declaration read-only and enumerate exactly
its whole-pack ordinary skill contributions:

1. Immediate child directories of broadcasting governed skills trees: conventional `skills/`
   plus declared extra trees. Ignore loose files; no marker is required for ordinary directories.
   A selected child becomes kind `directory`, carrying its governed source directory and child
   name as eligibility evidence.
2. Manifest sourced rows, named by their alias or source-directory basename. These remain
   kind `marker`, requiring a direct regular marker on their own existing path.

Destination declarations source nothing. Within a pack, later governed sources win duplicate
ordinary names before selection, using the common delivery enumerator. Audience metadata
travels with each selected candidate. A child classified as a plugin wrapper is not a skill
candidate. A literal selector naming it refuses with “is a plugin; import the pack whole to
use it”. Classification must not become plugin delivery. Zero candidates refuses with
“pack has no skill contributions; use `skills.dir` for a raw collection”.

### Raw candidates

No manifest, convention, home, overlay, briefing or plugin discovery runs anywhere in the raw
source. If the directory has a **regular-file** `SKILL.md`, it is one skill; children are
supporting data, not more candidates. Its name is the last directory component. For `dir = "."`,
use the last component of `subdir` when set, otherwise of the handle.

Without a root marker, enumerate immediate child directories with regular-file markers,
nonrecursively. Markerless children such as `shared/` never become candidates. Zero candidates
refuses with “no skill directories with SKILL.md directly under `<dir>`”, listing a bounded
set of deeper marker locations as suggested `dir` values. Raw mode broadcasts to all agents.

### Selectors and failure

A selector is a [Go path.Match glob](https://pkg.go.dev/path#Match) over one upstream candidate
name, never a physical file path. A selector without metacharacters is literal.

Always refuse offline: empty strings, `.` or `..`, `/`, backslash or NUL, invalid glob syntax,
duplicate selectors within one list, non-string members, an empty `skills` table, or explicitly
empty selector lists. Empty-table remediation is “add `dir`, `only` or `exclude`, or remove
`skills` for the whole pack”; empty-list remediation is “remove the key”.

Evaluation:

1. Start with all candidates if `only` is omitted; otherwise keep every candidate matching any
   `only` selector.
2. Remove every candidate matching any `exclude` selector.
3. Refuse unmatched selectors in **either** list, any inclusion selector whose every match is
   excluded, and an empty result independently. A stale exclusion must not silently deliver a rename.

Refusals identify the origin file, `packs[N]`, candidate names, and the repair; conflicts name
both selectors. No failure falls back to whole-pack or partial delivery.

### Effective declaration and supporting data

A scoped entry contributes selected skill rows and **nothing else**: no `needs`, programs,
hooks, surfaces, home files, overlays, briefing, description-driven behavior or root/nested
plugins. Build the effective declaration before closure. If another live closure edge targets
the same resolved pack root, refuse naming both entries; a dependency must not widen scope.
Current `needs` targets shipped packs only, so compare canonical shipped identity even when
an authored entry uses `yolo/<name>`.

A selected directory is the delivery unit. Git retains the existing repository payload shape,
including safe supporting data outside selected folders, **outside discovery roots**. Unsafe
selected data refuses; unsafe supporting data is omitted with recorded reasons. Staged local
and shipped carriers copy only selected directories. Nothing outside them is registered,
discovered as a skill or activated.

Sibling access has sourced-row parity, not a general guarantee. A payload-backed script can
resolve repository siblings through its location; a private marker's relative `../shared`
reference is not thereby guaranteed. Record the actual observation in acceptance evidence.

Selected manifest sourced rows retain repository, explicit ref, alias, license and audience.
Drop unselected rows before store work. Modern selection never sets or inherits legacy
`Only`/`Exclude` or `SourcedSkillsWithheld`; use separate modern wire members.

## Naming and identity

Registration names apply existing entry naming to upstream names. Bare keeps names;
otherwise a named-source key, repository last component or local pack word supplies the
namespace. Existing namespace override restrictions remain. `subdir` and selection never
change the default namespace. No individual alias is added to user config.

Apply reserved/core-owned name refusals and existing sourced-claim collision rules. Do not
rename conflicts. Allowed entries sharing a handle share namespace ownership; inert scoped
entries cannot acquire a duplicate file landing merely because they share that namespace.
Destinations remain the skills destinations of the same entry's whole-pack import.

Import identity is `(handle, normalized subdir, normalized skills.dir)`. Refuse duplicates of
that tuple, duplicate physical local roots with that tuple, whole/scoped entries at the same
`(handle, subdir)`, and two typed scoped entries at that root. Allow different raw `dir` values
or different `subdir` values at one handle. Entries sharing a handle must share the authored
ref, including omitted-versus-explicit distinctions; the lock retains one record per handle.

Source mirror, repository/ref stamp, default-ref pinning and physical source identity retain
existing meanings. Reuse commit/subdirectory checkout identity and repository/commit payload
publication. Selection never keys or mutates a shared store object.

The projection digest covers source content identity, normalized roots/selectors, naming and
resolved rows, including row kind and marker state. It contributes to staged slug and restart-required content/naming comparison.
Changing only selection needs no fetch; advancing content re-evaluates selection. Physical
identity is not a selection identity. Ordinary entries without new keys retain baseline slugs.

## Paths and refresh

| Boundary | Rule |
| :--- | :--- |
| Lexical paths | Relative clean forward-slash paths; no leading slash, tilde, `..`, empty/dot segments, backslash, NUL or `.git` component. Literal, never glob/pathspec. |
| Root spelling | Refuse empty or `.` `subdir`: omit it. `skills.dir = "."` is legal. |
| Git roots | Every component a real commit-tree directory; no symlink component. Selected links stay inside the selected directory and retain existing byte/entry bounds. |
| Git marker row | Direct regular marker required, including raw root imports; real-directory ancestry and existing bounds apply. |
| Git directory row | Same ancestry/link/bounds rules; marker may be absent, but a present non-regular marker refuses. Row must be the immediate child of its stated governed source inside the pack root. |
| Local roots | Resolve symlinks and require selected root to remain inside source root. Selected contents retain existing user-local link trust; this does not alter workspace link rules. |
| Registration | Independently enforce namespace and core-owned name constraints. |

Only existing host launch-shaped refresh surfaces perform transport. Pure readers—why,
editing validation, offline lint, check, footprint, explain and attach hydration—never fetch.
Validate syntax everywhere, candidate matching wherever pinned content is already local;
otherwise report “selection not yet verified; resolved at next launch” without failure.
At refresh, validate before publishing any selected row and join the one grouped payload call.

**Refresh is lock-first, with no rollback.** The current refresh saves an advanced pin before
pack resolution. A stale selector refuses the new projection, publishes no invalid or partial
selection, never widens or silently reuses the old subset, and leaves running staged trees
unchanged. Its error says the resolved pin advanced, names the new commit, failing selector and new
candidate names, and names the previous commit only when available without transport. It offers
editing the selection or explicitly setting a known-good ref. The reader accepts a full commit
SHA, so that is also a legal explicit ref remedy. Both recovery routes must be tested. Fresh
resolution repeats the refusal until config changes, never downgrading it to a warning.
This feature adds no store-transaction redesign.

## Carriers and recorded delivery

| Source | Carrier |
| :--- | :--- |
| Direct/named Git | Existing repository/commit payload and private registration; row names a repository-relative directory at the locked commit. Marker and directory kinds share one payload. Never load payload as pack/plugin. |
| Local path or local handle | Per-launch selected-directory copy outside discovery roots, using the common registration step and same row kinds; host process copies retain process lifetime. |
| Shipped | Same staged selected-directory carrier; typed mode only. |
| Selected manifest sourced row | Existing own-repository payload, unchanged marker and non-root requirements. |

### Row kinds and typed-directory eligibility

Every selected row has exactly one kind:

| Kind | Produced for | Marker rule |
| :--- | :--- | :--- |
| `marker` | Raw-mode and manifest sourced rows, in scoped or whole packs | A direct regular marker is required; no typed-directory exemption. |
| `directory` | Typed ordinary directories only | Absent marker accepted; present regular marker fully validated; every other marker entry refused. |

Kind `directory` is authority of the typed projection, not an option. It has no authored TOML,
manifest, legacy, CLI or environment spelling. YOLO_PACKS carries entry selection, never an
entry-level row-kind knob. Internal per-row requests and staged records carry kind explicitly.
Only the shared scoped resolver constructs directory-kind requests from broadcasting governed
sources, using the whole-pack enumerator. The request carries pack root, governed source and
child name; validation rechecks the immediate-child relationship, containment and real directory
against immutable source. Manifest decoding and raw enumeration can produce only marker rows.
Plugin-classified children are excluded before request construction; registrations never enter
plugin discovery, with or without a marker.

Inspect the exact direct entry named `SKILL.md`. Absent means marker state `absent`. A regular
file means `present`, with all existing marker validation/name-rewrite behavior. A symlink,
directory, submodule or other entry refuses; never treat it as absent. Differently cased files
are ordinary data. No marker is invented. A markerless registration has its supporting entries
and license but **no** `SKILL.md`, and its directory name follows whole-pack naming.

Everything else is shared: namespace, reserved names, claims/collisions, own/root-license
selection, bounds, ancestry and selected-link confinement. Both kinds share one repository/commit
payload or staged-copy location. Kind is not source/payload identity. If any per-row publication
record exists it carries kind/state and pure readers compare it with the request; the current
shared skills lock has repository/ref records only, so add no selection history there.

### Record validation and downgrade

The staged record schema changes for kind/state/governed-source fields. A reader that does not
know the version rejects, rather than skipping rows or defaulting marker kind. Directory-kind
hydration requires typed projection, immediate-child structural evidence, and staged registration
matching marker state. `present` requires a regular marker; `absent` requires no marker entry.
Reject unknown/missing/ineligible kind, altered kind/state, directory-kind raw rows and directory-kind
manifest sourced rows. Existing whole-pack sourced rows without a scoped record remain marker-kind
by definition and hydrate as before. Validation failure has no whole-pack/plain-copy/synthesized-marker
fallback.

A scoped entry stages **no original pack root**. Original manifests, skills trees, plugins,
programs and hooks never enter a discovery root. The effective record contains:

- mode, `subdir`, `dir`, inclusion/exclusion, digest and config origin;
- selected registration/upstream names, kind, marker state, carrier and source directory;
- governed source directory for each directory-kind row;
- commit or staged location, audience, license result and provenance per row;
- false modern withholding and omitted contribution counts by kind.

Reload and attach consume the running record and verify every row. Missing, duplicate and
unrecorded rows refuse; never re-enumerate or consult newer host cache/source state. Adoption
works after deleting original source, mirror, checkout and payload cache. This does not promise
that every host comparison/preflight is source-free. Host apply, lint, explain, footprint, check
and launch use one resolver, not independently reconstructed subsets. Rebuild embedded metadata
bindings for the effective declaration rather than retaining an unprojected binding.

Preserve selected marker/frontmatter content under existing name-rewrite rules. A directory-kind
row with absent marker creates none; the registration still uses the entry-named directory. Apply existing
own-license then root-license discovery, conflict/unsafe/oversized refusal, and “no license
found” recording. Direct config adds no explicit license path. Per-row provenance prints
normalized repository/local root, commit, source directory and config origin.

## Editing and inspect surfaces

`yolo pack add <handle>` and `yolo pack add --git <url> [--ref <ref>]` accept `--subdir`,
`--skills-dir`, repeatable `--only`/`--exclude`, `--bare`, `--namespace`, and `--dry-run`.
Positional handle and `--git` are exclusive. With no table-only option, preserve string writing;
otherwise write a table through the existing complete-graph, byte-verified transaction.
Dry-run prints the entry and local projection without fetching. Existing import identity
refuses with origin and an indexed edit path.

`yolo pack remove <handle>` matches authored strings and tables by derived handle. Multiple
matching entries refuse, listing `packs[N]`, roots and raw directories; `--subdir` and
`--skills-dir` disambiguate. One invocation removes at most one entry. Preserve fragment origins
and neighboring bytes; never route deletion by a table's formatted text.

Generic config edit accepts indexed new paths and elements, with user-only offline validation.
An edit leaving empty lists/tables refuses. `config why` reports fragment and value for entries
and selector paths. Writers omit default keys, empty lists and legacy spellings; round-trip is
entry/projection equivalent.

Extend the shared sourced-skill projection printer for explain, lint, footprint and check:
mode/roots, candidates/selected/excluded names and registration names; row carrier/license/
provenance; omitted contribution counts. Omission is information, not a warning. Typed missing
markers retain ordinary lint reporting. Label a markerless row “folder, no SKILL.md, delivered but
not a discoverable skill” and exclude it from all skill counts; no surface calls it a skill.
An unverified source says so instead of claiming delivery.

## Compatibility, alternatives and costs

The legacy reader, wire filters and withholding remain unchanged, including host withholding
regressions. Migration retains unconvertible files and refuses old `name`/`only`/`exclude`;
its explanation points at manual `skills.only`/`skills.exclude` rewriting without claiming
equivalence. No new JSONC feature is introduced.

| Alternative | Disposition |
| :--- | :--- |
| Reuse legacy file globs | Rejected: filters before load do not remove typed executable declarations and inclusion withholds sourced rows. |
| Wrapper manifest only | Rejected: fails direct user-level narrowing and collection/root cases. |
| Raw collection only | Rejected: omits real-pack typed skills, audience and sourced rows. |
| New filtered payload store | Rejected: selection must not mutate shared identities or content. |

Costs: new config/wire/record representation, record-driven effective loading, strict stale-selector
refusals, and table-aware authoring/removal. No automatic legacy equivalence is promised.

## Acceptance scenarios

Every fixture is synthetic local data/repositories. Real TOML producers must drive resolution,
closure, composition, staging and reload; tests of isolated filtering alone are insufficient.

| ID | Scenario | Observable acceptance |
| :--- | :--- | :--- |
| A-01 | Nested raw collection, root plugin, manifest with program/hook/need poison | Exactly two selected registrations, no poison activation or shared/framework candidate. |
| A-02 | One user fragment, two workspaces | Same selection without workspace config. |
| A-03 | Selected marker/script references repository sibling | Record reachability as observed; sibling never registered. |
| A-04 | Inclusion glob plus exclusion | Only surviving candidate delivered. |
| A-05 | Root single marker, bare and namespaced | One correctly named registration. |
| A-06 | Root collection without root marker | Marker-bearing immediate children only. |
| A-07 | Conventional typed directories | Existing ordinary-directory parity, no other conventions delivered. |
| A-08 | Declared extra tree, audience, poison contributions | Audience retained; omitted kinds counted; no closure from scoped entry. |
| A-09 | Selected manifest sourced alias plus ordinary directory | Both delivered, withholding false; unselected row does no store work. |
| A-10 | Legacy inclusion beside modern selection | Old withholding unchanged; modern wire fields separate. |
| A-11 | Whole-pack relocated root, direct Git and local named source | Nested pack loads; unrelated siblings absent. |
| A-12 | Empty/malformed/no-match/conflict/zero/plugin selector | Actionable origin/index/candidate refusal; no fallback. |
| A-13 | Lexical traversal, Git root link, local escape, selected escaping Git link | Refused; unsafe outside support omitted with reason. |
| A-14 | Identity/ref/scope/legacy/shipped conflicts; two raw directories | Conflicts refuse; legal same-handle entries coexist. |
| A-15 | Other live need targets scoped shipped root | Refusal names scope and needing pack. |
| A-16 | Add/dry-run/remove ambiguity/edit/why | Table round-trip, origin preservation, no transport. |
| A-17 | Shared commit, differing marker/directory selections, selection-only change | One shared payload unchanged, no second cache or fetch; selection change requires restart. |
| A-18 | Delete source/cache before reload/attach; tamper rows | Running view succeeds; missing/duplicate/extra rows refuse. |
| A-19 | Host apply parity | Exact same recorded names, rows and licenses as jail. |
| A-20 | Unfetched scoped source on pure readers | Not-yet-verified, no transport or failure. |
| A-21 | Own/root/absent/conflicting license | Correct recorded result or refusal. |
| A-22 | Legacy filtered migration | Retained file, refusal, modern manual spelling. |
| A-23 | Mixed typed marker/markerless folders on Git/local carriers | Both delivered, markerless folder has no synthesized marker, correct inspect counts/lint, parity with whole pack. |
| A-24 | Markerless raw directory/child and manifest sourced row | Refusal or no-match; none produces directory kind. |
| A-25 | Typed marker is symlink or directory | Refuse, never treat as absent. |
| A-26 | Forged/removed/unknown row kind, altered marker state, raw or sourced directory-kind row, marker changed after stage, previous-schema reader | Hydration rejects all, no discovery/default/conversion. |
| A-27 | Mixed typed rows after deleting every source/cache | Source-free attach/reload retains absent marker. |
| A-28 | Markerless inert support versus actual plugin wrapper | Inert data delivered without hooks/server activation; wrapper excluded. |
| A-29 | Legacy inclusion and legacy whole markerless directory beside modern rows | Old behavior/withholding unchanged; legacy never produces directory kind. |
| A-30 | Stale selector after ref advance with older running jail | Advanced pin, fresh refusal/no publication, new commit/selector/candidates in error, running tree unchanged and attach works. |
| A-31 | Edit selection or set known-good ref; unchanged repeat | Both remedies deliver exact rows without widening; unchanged launch repeats refusal. |

## Invariants for review

1. Normal TOML entries retain baseline behavior.
2. Scoped delivery never loads original sources as packs/plugins.
3. No refusal falls back to whole pack or a partial subset.
4. No pure reader transports; no shared object depends on selection.
5. Legacy reader, wire filters and withholding are unchanged.
6. Generic core has no source/framework/agent switch.
7. Directory kind is resolver-produced only, with no authored or entry-wire knob.
8. No synthesized marker and no markerless folder reported as a skill.
9. Failed advanced-pin projection never publishes, widens or reuses prior selection.

## Confirmed carrier facts

All finite author facts were checked from source at the baseline; none needs owner policy:

| ID | Finding and binding consequence |
| :--- | :--- |
| U-01 | Payload-backed scripts can read repository siblings; private marker-relative sibling access is not a general guarantee. Record observed acceptance behavior. |
| U-02 | Refresh saves pins before resolution; preserve lock-first behavior above. |
| U-03 | Plugin root/immediate governed-source child classification is available in the existing classifier. Never use classification as delivery. |
| U-04 | Needs addresses embedded official packs; compare canonical shipped root before already-selected no-op. |
| U-05 | Namespace-owner comparison already tolerates one owner; landing comparison separately refuses. Scoped rows have no landing claim. |
| U-06 | Unfiltered host local packs read in place; filtered packs have process lifetime. Scoped copies need the same lifetime rule. |
| U-07 | Shipped table form is accepted; generic set/add/unset/why and dry-run exist. |
| U-08 | Local frontmatter uses accepted/DECIDED for ruled unbuilt work; companion plan owns build sequencing. |
| U-09 | Readable explicitRef accepts full SHA strings; classifyRefAnd recognizes 40-digit hexadecimal commit identities. Known-good full commit remediation is legal. |
| U-10 | SkillsLock SkillRecord is per repository/ref, with ref classification Kind, not per-row kind. No per-row publication stamp exists in this machinery; add no selection history to the shared lock. |
| U-11 | Ordinary whole-pack writer computes names with packname.Delivered(namespace, upstream directory). Directory-kind registration must match it. |

Evidence links and inspected symbols are in [the research](../research/toml-scoped-pack-imports.md).

Synthetic emitted-launcher probes set both `YOLO_NO_PROGRAM_READINESS=1` and
`YOLO_NO_AUTO_CAPTURE=1`, disable updates/reapers, use controlled empty fixture config,
prove no capture/install markers and owned cleanup. No agent execution or provider API call.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-01 | User TOML entry keys `subdir` and `skills` only | 2026-11-17 | [Syntax](#syntax) | — |
| D-02 | `subdir` alone preserves whole-pack semantics | 2026-11-17 | [Pack root](#pack-root-and-typed-candidates) | — |
| D-03 | Presence of `skills` is skills-only | 2026-11-17 | [Effective declaration](#effective-declaration-and-supporting-data) | — |
| D-04 | Name selectors, not legacy file globs | 2026-11-17 | [Selectors](#selectors-and-failure) | — |
| D-05 | Typed and raw candidate modes | 2026-11-17 | [Projection](#projection-semantics) | — |
| D-06 | Include then exclude; strict empty/stale/conflict refusal | 2026-11-17 | [Selectors](#selectors-and-failure) | — |
| D-07 | No non-skill contribution or dependency widening | 2026-11-17 | [Effective declaration](#effective-declaration-and-supporting-data) | — |
| D-08 | Modern selected sourced rows never withheld | 2026-11-17 | [Effective declaration](#effective-declaration-and-supporting-data) | — |
| D-09 | Raw root marker allowed; manifest root remains refused | 2026-11-17 | [Raw candidates](#raw-candidates) | — |
| D-10 | Existing entry naming, no config per-skill alias | 2026-11-17 | [Naming](#naming-and-identity) | — |
| D-11 | Shared Git payload/local staged carrier, explicit marker/directory kinds | 2026-11-17 | [Carriers](#carriers-and-recorded-delivery) | — |
| D-12 | Persist projection/rows, no reload enumeration | 2026-11-17 | [Recorded delivery](#carriers-and-recorded-delivery) | — |
| D-13 | Unselected content never staged in discovery roots | 2026-11-17 | [Recorded delivery](#carriers-and-recorded-delivery) | — |
| D-14 | No new store/stamp; selection in staged/diff identity | 2026-11-17 | [Identity](#naming-and-identity) | — |
| D-15 | Tuple import identity; same handle shares ref | 2026-11-17 | [Identity](#naming-and-identity) | — |
| D-16 | Table-aware add/remove/edit/why; shared inspect printer | 2026-11-17 | [Editing](#editing-and-inspect-surfaces) | — |
| D-17 | Legacy reader/wire/withholding unchanged | 2026-11-17 | [Compatibility](#compatibility-alternatives-and-costs) | — |
| D-18 | No legacy filter migration; manual modern rewrite | 2026-11-17 | [Compatibility](#compatibility-alternatives-and-costs) | — |
| D-19 | Source validation never fetches on pure readers | 2026-11-17 | [Refresh](#paths-and-refresh) | — |
| D-20 | No broker authority or workspace-rule change | 2026-11-17 | [Boundaries](#goal-and-boundaries) | — |
| D-21 | Typed ordinary directory variant: absent marker accepted, present marker fully validated, none synthesized | 2026-11-17 | [Row kinds](#row-kinds-and-typed-directory-eligibility) | — |
| D-22 | Resolver-only kind authority; request/record/hydration eligibility and downgrade rejection | 2026-11-17 | [Record validation](#record-validation-and-downgrade) | — |
| D-23 | Preserve lock-first refresh; invalid new projection refuses/no publication, unchanged running tree, tested recovery | 2026-11-17 | [Refresh](#paths-and-refresh) | — |
