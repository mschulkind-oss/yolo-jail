---
title: "Grounding for Git-sourced pack skills"
status: draft
stage: SKETCH
next: "Fable revises supporting-content deployment before this plan can become an implementation hand-off."
tags: [packs, skills, git, implementation]
---

# Grounding: Git-sourced pack skills

**Status:** 2026-11-17. Read against `b5a5fe32a326109371656162623b690a3c790427`.
The planner reproduced the declaration gap through the real offline lint caller with a
synthetic local Git repository. No production code changed. The owner then reopened
the deployment boundary; implementation stopped before any materializer or schema was enabled.

**Design:** [Fable's preserved design](git-skill-subtrees.md).
The design wins on behavior; the tree wins on fact; this plan is advice and the first
thing to be wrong. This is **not a ready hand-off**: it records completed grounding and
one tests-first checkpoint, not an approved replacement for Fable's pending revision.

## Verified map

These are read production seams, not a commitment to the paused directory-only shape.

| Path | Existing role and prospective work |
| :--- | :--- |
| [`contributes.go`](../../internal/packdecl/contributes.go) | `Contribution.Source`, `SkillsSource`, `SourceKey`, duplicate source validation; new static field rules must preserve content governance |
| [`fork.go`](../../internal/packdecl/fork.go) | `forkFieldPlacementProblems` currently refuses `source` on skills; do not exempt unrelated fork fields |
| [`toml.go`](../../internal/packdecl/toml.go) | Grouped TOML decoding; use `[[skills]]`, not legacy JSON's `contributes` shape |
| [`skew.go`](../../internal/packdecl/skew.go) | Authoring versus runtime-use reads; unchanged, no minimum-version mechanism |
| [`store.go`](../../internal/packsrc/store.go) | Mirror lock, transport environment, blob-prefetch, commit resolution, checkout; sourced bytes must not use checkout |
| [`refresh.go`](../../internal/packsrc/refresh.go) | `fetchStep`, hourly branch stamps, tag preservation, launch timeout and detached store |
| [`lock.go`](../../internal/packsrc/lock.go), [`forklock.go`](../../internal/packsrc/forklock.go) | Existing lockfile lock/atomic-save patterns; a new resolution record must not be keyed by selecting pack name |
| [`packresolve.go`](../../internal/config/packresolve.go) | `ResolvePack`, declaration versus stage modes, read-only-store posture, filters and `ResolvePackForProcess` |
| [`skillssource.go`](../../internal/packload/skillssource.go), [`governance.go`](../../internal/packload/governance.go) | Source resolution and original-governor audience; do not let a sourced declaration accidentally govern conventional local skills |
| [`compose.go`](../../internal/hostskills/compose.go) | `ComposeHostSkillsFor`, `PackLayer`, declaration claims, same-pack dedup, strict collision preflight and shared writer |
| [`skillname.go`](../../internal/hostskills/skillname.go) | Existing byte-preserving frontmatter `name:` rewrite; bare sourced delivery needs the same rewrite |
| [`skills.go`](../../internal/jailcontent/skills.go) | `SkillPlan`, collision preflight, built-in and workspace composition; current ordinary pack/built-in override behavior must survive |
| [`jailskills.go`](../../internal/cli/run/jailskills.go), [`packs.go`](../../internal/cli/run/packs.go) | `jailSkillSources`, `stagePackEntry`, audience conversion and launch collision refusal |
| [`packtreerecord.go`](../../internal/packload/packtreerecord.go), [`packtree.go`](../../internal/cli/run/packtree.go) | Tree-relative record, recorded load/attach; sourced bytes must survive removal of host cache |
| [`packrefresh.go`](../../internal/cli/run/packrefresh.go) | Shared launch/host fetch route, in-jail no-op and dry-run exemption |
| [`pack.go`](../../internal/cli/pack.go) | Offline lint, explain, install, status, footprint and online lint entry points |
| [`packs.go`](../../internal/cli/check/packs.go) | Read-only `ResolvePack` and `pack skill name collision` finding |
| [`applyhostskills.go`](../../internal/cli/applyhostskills.go) | Real apply composer and collision refusal before writes |

## Reuse and traps

- **Constraint:** pack mirrors are partial clones. `fetchMirror` explicitly uses
  `clone --bare --filter=blob:none`. Writer-phase blob acquisition must cover every byte
  the revised design permits; read-only inspection cannot trigger lazy fetching.
- **Advice:** reuse `fetchStep` under the existing mirror lock for transport and ref refresh.
  It is already separated from `refreshMirror`'s checkout for patched-fork callers.
  Reusing `Refresh` wholesale would also check out a tree, which the original design forbids.
- **Constraint:** `gitCmdWith` strips repository-state environment and sets
  `GIT_NO_LAZY_FETCH=1` except on receiving commands. It intentionally preserves host Git
  configuration for transport; object reads need the original design's separate config isolation.
- **Constraint:** workspace imports have a distinct authority/cache domain.
  [`project.go`](../../internal/skillimports/project.go)'s `materializeSkill` and
  `confinedObjectPath` provide a confinement rule reference, **not** a pack-store API.
  `Inspect`/`Project` first enforce workspace broker authority and records. Do not copy that state
  into pack resolution or route pack content through workspace grants.
- **Constraint:** the existing workspace materializer creates validated symlinks in its temporary
  projection. The original pack design requires link-free immutable materialization instead.
  Calling it unchanged would not satisfy that requirement.
- **Constraint:** existing `SkillsSources` deduplicates source directories, and the composer
  collects later-source-wins local skills before claim calculation. Sourced same-pack claims
  cannot be inferred only from that collapsed collection.
- **Constraint:** `ComposeHostSkillsFor` sees destination-resolved contributions; declaration
  metadata must survive resolution/cloning rather than introducing a host-only naming route.
- **Constraint:** jail built-ins are composed after packs; strict sourced collisions must be
  checked before that ordinary non-sourced override path, including rendered mechanics.
- **Constraint:** attach's `loadPackTree` reads the recorded running tree. Neither a new fetch nor
  a newly produced materialization belongs there. Both host attach and in-jail pack readers need
  the same tree-relative mapping, validated as confined to that tree.
- **Constraint:** TOML is `[[skills]]` without `kind`; legacy JSON uses
  `{"contributes":[{"kind":"skills", ...}]}`. Do not widen TOML to match the original example.

## Compatibility correction verified

`unknownFieldSkip` uses `fieldNextStep`, whose fallback text is
"a newer yolo may read the field (update yolo)". It does **not** say `update-yolo`.
`restrictingKind` includes only blocked-tool, intercept and autonomy kinds, not skills.
The source-only placement refusal is present in `forkFieldPlacementProblems`.
No new compatibility test or version-skew mechanism is proposed. Existing tests remain intact.

Strict authoring refuses unknown `as`/`license`; older runtime use skips the affected
contribution with a note and can still deliver conventional local content. It does not guarantee
whole-pack refusal, sourced delivery or the new collision checks. A graduated reference must
preserve this distinction; no reference graduation is appropriate before implementation.

## Tests-first checkpoint

[`TestPackLintSourcedSkillDirectory`](../../internal/cli/gitskillsubtrees_test.go) uses
`packMain` via the existing `lintReport` helper. It creates real Git objects for a nested skill,
asset, sibling skill, root license, hooks and plugin manifest. No fixture script executes.
The offline command does not fetch that repository or execute programs.

```sh
go test -short ./internal/cli -run '^TestPackLintSourcedSkillDirectory$' -count=1
```

Observed red with valid TOML: `json: unknown field "as"` at `skills[0]`.
An earlier run used the original design's invalid `[[contributes]]` TOML spelling and was
rejected before feature validation. That receipt is a schema-discrepancy finding, **not**
feature red evidence. A first fixture commit inherited signing policy and failed before
Git objects existed; the helper now disables signing for that one synthetic commit.

The regression is intentionally red on this candidate-only checkpoint. It must not be landed
as feature completion. It asserts declaration acceptance and an honest offline unverified-content
note, not completed delivery. This assertion must be reconciled with Fable's revised declaration
before implementation; do not weaken it just to make the checkpoint green.

## Remaining implementation units — pending author revision

1. Reconcile the deployment/exposure boundary and paths supporting scripts actually read.
   The original one-directory limit, external license exception, link confinement, materialization
   identity, byte bounds and exact-byte acceptance fixture may change. Fable decides; do not guess.
2. Complete static declaration validation plus object-only store resolution/materialization,
   validated record publication, read-only states and refresh fallback. Use real local Git objects.
3. Wire the selected-pack resolver, host composer, jail staging, staged record/load and strict
   claims together; drive real host apply and running-tree attach callers, not only store helpers.
4. Wire install/update, check, offline/online lint, explain, status and footprint observations.
   Online lint must use its scratch store; read-only verbs must never write/fetch.
5. Add production-caller regressions for every remaining acceptance behavior: audience,
   namespace/rename/frontmatter, same-pack/other-pack/built-in/reserved collisions, workspace shadow,
   inert upstream effects, malformed paths/transports/links/submodules/bounds/licenses,
   missing/offline cache/refresh and immutable attach. Preserve old pack and workspace-import tests.
6. Demonstrate callsite-removal failure, event-driven mirror/record-lock tests, focused integration
   and selected-pack host/jail fixture probes using a matched absolute dev launcher. Set both
   no-program-readiness and no-auto-capture gates and disable update checks. Do not install anything.

## Gates and fences

- Production, workspace authority, live configs/grants/credentials, upstream content and other
  worktrees are untouched in this checkpoint. No source-enabled partial feature is approved.
- The candidate-only commit is `test(packs): checkpoint sourced-skill grounding before design revision`.
- Final combined primary landing still owns full `just check-ci`, full launch integration,
  completion census and final `just done`. No green feature claim follows from baseline tests.
- Flag new out-of-package test readers for final completion census if later tests read source files.
- No native macOS, real rootless reachability or actual upstream behavior has been established.
