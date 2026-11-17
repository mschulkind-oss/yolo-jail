---
title: "Build Git-sourced skill registrations without activating their repository"
status: accepted
stage: DECIDED
next: "Complete candidate runtime witnesses and exact-candidate conformance review; retain RED-to-GREEN and caller-removal receipts."
tags: [packs, skills, git, implementation]
---

# Plan: whole-repository payloads, selected skill registrations

**Status:** 2026-11-17. Grounded against clean candidate
`566ef01fa6339697d58defddd0c859a0b12b6921`; production matches
`b5a5fe32a326109371656162623b690a3c790427`. Planning only; the permanent real-caller
lint test remains deliberately RED. No behavior is enabled.

**Design:** [the accepted design](git-skill-subtrees.md). Its behavior wins; the tree
wins on cosmetic fact drift; this plan is advice and the first thing to be wrong.
This canonical candidate incorporates Fable's accepted whole-repository revision,
**one shared record, per-caller validation, no per-selection history** addendum and
**repository/ref stamp and leaf-lock correction**, and **final reader clarification** of
2026-11-17. The original external revision predates those amendments and is not the
builder's standalone contract. The
[canonical transaction](git-skill-subtrees.md#62-resolution-sequence-and-transaction)
and [cached-reader/fallback contract](git-skill-subtrees.md#83-reader-states-and-commands)
include them. All author clarifications are incorporated and no judgment gap remains;
this is a build hand-off, not production completion. No production behavior shipped.

All **Constraints** below are binding implementation fences or settled behavior;
**Advice** and explicitly **Cheap** choices may vary without changing behavior.
Substantive disagreement, unexpected test failure, dirty foreign work or need for
another authority is **stop and report**, not permission to diagnose a replacement.

## 1. Settled contract — do not re-open

[The decision ledger](git-skill-subtrees.md#10-decision-ledger) owns the rulings.
This hand-off carries what a builder needs without recreating the argument:

| ID | Required implementation outcome | Units |
| :--- | :--- | :--- |
| GSS-D1, GSS-D6 | Real TOML `[[skills]]` (no `kind`), existing `source` grammar, required ref/subdirectory, optional normalized `as`; namespace then frontmatter rewrite, even bare | 1, 3 |
| GSS-D2, GSS-D3 | Pack-store transport only; object-only whole tree, isolated object reads, writer obtains all blobs, no checkout/smudge/hooks | 2 |
| GSS-D4, GSS-D10, GSS-D11 | Existing ref timing; aggregate validation before record advance; named fallback/refusal; READY/REFUSED/NOT FETCHED; read-only never fetches | 2, 4, 5 |
| GSS-D5, GSS-D9 | Superseded, not alternative contracts: only immutable rename publication and recorded-tree attach survive the old shape | 2, 4 |
| GSS-D7 | Declaration claims independent of fetched state; same-pack sourced duplicates and built-in collisions fatal; ordinary non-sourced rules preserved | 1, 3, 4, 5 |
| GSS-D8 | Explicit root-relative regular license ≤1 MiB; case-insensitive basename conflict refuses; four-name default or absence note | 2, 3 |
| GSS-D12 | Legacy `only` withholds every sourced row and fetches nothing; `exclude` alone does not | 1, 2, 5 |
| GSS-D13, GSS-D20 | Whole immutable payload outside all discovery/pack roots, no upstream metadata discovery, no general filters or other kinds | 2, 4, 5 |
| GSS-D14, GSS-D15 | Real registration directory; private regular `SKILL.md`, optional regular license, typed planned absolute links only; ambient guardrails unchanged | 3, 4 |
| GSS-D16, GSS-D17 | Strict selected-directory validation; whole-root omit-and-count elsewhere; bounded object reads/copy | 2, 4, 5 |
| GSS-D18 | Alias registrations each have private rewritten marker; one payload per repository/commit/validator version | 1, 3, 4 |
| GSS-D19 | Separate payload-deployment and selected-registration claims/counts; declarations never falsely listed under `delivers` | 5 |
| GSS-D21, GSS-D22 | Caller-local immutable selection, one result per pair; mirror→leaf-record transaction, latest shared record only; revalidate own rows; full-commit pin remedy | 2, 4, 5, 6 |
| GSS-D23, GSS-D24 | Derived NOT MATERIALIZED reader skip; shared pure object validator, exact diagnosis; not-due writer rematerializes locally without fetch/stamp/mirror change, force unchanged | 2, 4, 5 |
| GSS-D25, GSS-D26 | Directory, marker and every registration target must survive effective omissions; row refusal by path/cause, no case-group exception or copy-around | 2, 3, 4, 5 |

**Constraint:** relative paths are explicitly PARTIAL, as
[the design's limit](git-skill-subtrees.md#84-what-resolves-and-what-does-not).
A path walked by the OS through a `scripts` link can climb inside the repository;
textual normalization and climbs beginning at the real registration root cannot.
No workspace setup, dependency installation or upstream runtime success is promised.

## 2. Read source map

Paths below were read at the stamp above. New files are proposed names, not existing APIs.
Keep implementation small within each unit; do not build generic payload infrastructure.

| Path | Work and existing seam |
| :--- | :--- |
| [`contributes.go`](../../internal/packdecl/contributes.go), [`fork.go`](../../internal/packdecl/fork.go), [`toml.go`](../../internal/packdecl/toml.go) | Add skills-only fields/rules; source placement exception only for skills; duplicate source/governor identity must allow aliases without suppressing conventions |
| [`skew.go`](../../internal/packdecl/skew.go) | Preserve strict-authoring/runtime-use skip split, unchanged |
| [`store.go`](../../internal/packsrc/store.go), [`refresh.go`](../../internal/packsrc/refresh.go) | `fetchStep`, mirror lock, transport env, blob acquisition and ref timing; do not call checkout-based `Refresh` for sourced skills |
| [`lock.go`](../../internal/packsrc/lock.go), [`forklock.go`](../../internal/packsrc/forklock.go) | Copy deterministic atomic record/lock discipline, not their pack/fork keying |
| `internal/packsrc/skillpayload.go`, `internal/packsrc/skillslock.go` (new) | Whole-object tree plan, bounded validator/materializer and shared pair record; proposed record file `skills.lock.json` beside existing user lock files, dedicated lock under store `locks/` |
| [`packresolve.go`](../../internal/config/packresolve.go), [`selectedpacks.go`](../../internal/config/selectedpacks.go) | Complete selected closure before grouped sourced resolution; keep single-pack resolution declaration/store-read-only, not a first-writer side effect |
| [`packload.go`](../../internal/packload/packload.go), [`skillssource.go`](../../internal/packload/skillssource.go), [`governance.go`](../../internal/packload/governance.go) | Typed sourced row metadata/state beside ordinary directory sources; never treat a payload as local `skills/` |
| [`mergedest.go`](../../internal/packload/mergedest.go), [`plugins.go`](../../internal/packload/plugins.go), [`defaultnames.go`](../../internal/packload/defaultnames.go) | Preserve original governor, addressed borrowing and conventional/plugin readers; payload directories never enter plugin discovery or default-name rewriting |
| [`compose.go`](../../internal/hostskills/compose.go), [`composeinto.go`](../../internal/hostskills/composeinto.go), [`skillname.go`](../../internal/hostskills/skillname.go) | Shared host/jail layer plan, declaration-only strict claims and private marker rewriting |
| `internal/hostskills/sourcedregistration.go` (new), [`deliver.go`](../../internal/hostskills/deliver.go), [`archive.go`](../../internal/hostskills/archive.go), [`manifest.go`](../../internal/hostskills/manifest.go) | Typed registration writer, change/stale/archive ownership paths; ordinary copy still dereferences |
| [`treedigest.go`](../../internal/treedigest/treedigest.go), [`treesync.go`](../../internal/treesync/treesync.go) | Existing digest hashes READLINK text; new explicit owned-link synchronization entry point, default `Sync` still rejects links |
| [`skills.go`](../../internal/jailcontent/skills.go) | Carry typed registration plans through `SkillPlan`, built-in preflight, `PrepareSkillsWith`, scratch composition and final sync |
| [`packs.go`](../../internal/cli/run/packs.go), [`jailskills.go`](../../internal/cli/run/jailskills.go), [`packrefresh.go`](../../internal/cli/run/packrefresh.go), [`run.go`](../../internal/cli/run/run.go) | Complete selection, fresh-launch sourced writer boundary, backend mapping before composition, audience/claim conversions; no attach sourced fetch |
| [`packtreerecord.go`](../../internal/packload/packtreerecord.go), [`packtree.go`](../../internal/cli/run/packtree.go), [`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go) | Typed payload/registration records separate from Packs; load only recorded packs; recorded byte mapping on attach/boot; reserve payload area even in legacy root walkers |
| [`assemble.go`](../../internal/cli/run/assemble.go), [`helpers.go`](../../internal/cli/run/helpers.go), [`wsstatebeneath.go`](../../internal/cli/run/wsstatebeneath.go) | Podman bind/AC copy; preserve payload links and owned registration links under confined destination APIs instead of generic dereferencing copy |
| [`macosuser.go`](../../internal/macosuser/macosuser.go), [`runplan.go`](../../internal/macosuser/runplan.go), [`sessiontmp.go`](../../internal/macosuser/sessiontmp.go) | Actual guest pack-root calculation/copy/seal, bootstrap environment and legacy tree walker |
| [`apply.go`](../../internal/cli/apply.go), [`applyhostskills.go`](../../internal/cli/applyhostskills.go), [`host.go`](../../internal/cli/host.go) | Complete host selection before grouped writer, collision/failure before adoption or writes; observe never resolves/fetches |
| [`pack.go`](../../internal/cli/pack.go), [`packs.go`](../../internal/cli/check/packs.go), [`footprint.go`](../../internal/packload/footprint.go) | All inspection/explicit writer verbs; separate payload and registration claims, row-specific diagnostics |
| [`skillinventory.go`](../../internal/cli/run/skillinventory.go), [`unmatchedaudience.go`](../../internal/cli/run/unmatchedaudience.go) | Preserve typed sourced audience/name/state in derived inventories and warnings, not only composition |

## 3. Reuse before writing; traps that compile

### 3.1 Store and transaction

**Verified:** `fetchMirror` clones bare with `--filter=blob:none`. `gitCmdWith`
uses `CleanGitEnv`, disallows lazy fetch except commands `receivesObjects` recognizes,
and preserves user Git config for transport. `prefetchBlobs(commit, "")` batches
whole-tree missing objects but is **best effort** because checkout is its old fallback.
A sourced writer must make acquisition complete and fail if any object remains absent;
readers must never compensate by fetching. Keep fsck and detached timeout transport behavior.

**Constraint:** object listing/blob reads additionally disable system/global config and
injected Git config environment; do not apply that isolation to credential-bearing transport.
Use object IDs and literal paths, no checkout, worktree, index, filter, hook, LFS smudge,
submodule init or attribute evaluation. The yolo-owned bare mirror's local config is not
an upstream checkout config. Missing objects stay NOT FETCHED in read-only mode.

**Constraint:** make an inspectable pure plan (entry kinds, omitted paths/reasons,
charged counts/sizes, selected-row validation/license/name results), then one effectful
publication boundary. The **same pure validator is called by readers** to classify
complete/valid objects; no writer-only validity check or deferred validation fallback. Cap streamed listing at 16 MiB; charge all 32,768 entries and
256 MiB blob bytes before reading, including omitted entries, no refunds. Target strings
≤4096 bytes; resolved marker and registration license ≤1 MiB each. Acquire all tree blobs
in writer phase. A refuse over a bound names measured value, bound and next step;
there is no new knob.

**Advice:** use isolated `ls-tree -z`/`cat-file` batch forms with bounded reads and checked
object sizes, matching existing store subprocess lifetime/error discipline. Cheap: helper
factoring and map container, not identity, budgets or state transitions.

**Publication constraint:** payload identity is repository identity + full commit + validator
format version, shared independently of row/ref/alias/pack. Materialize into a private sibling,
seal files/directories without write bits (retain Git execute bits), then rename; never
rewrite a published payload. Rename loser verifies/reuses the completed winner and cleans
its scratch. A candidate failing any selected row publishes neither payload nor record;
remove its temporary bytes. An old unreferenced payload is never a fallback pointer.

**Transaction — do not re-open:** snapshot the caller's complete selected closure/rows
not withheld by `only` once, before any sourced resolution. Group by repository identity/ref
across all packs, regardless of audience; process one pair at a time. Use the parsed
`Addr.Repo` mirror identity and explicit `Addr.Ref` as the pair, excluding `Addr.Path`;
reuse current identity spelling, not a new URL canonicalizer. Hold its repository
mirror lock, reload current shared commit `R` under that lock, discard prior reads. Obtain
candidate `C` only if no sourced record, branch due, or force. Validate bounds and **all**
group rows at `C` before materializing/publishing either payload or record. Any failed later
row cancels the whole candidate. If no advance, revalidate all rows at `R`; only a passing
`R` is in-transaction fallback, and its missing payload may be materialized from present
objects without fetch. Absent objects remain NOT FETCHED. Release mirror lock and retain
one immutable result per pair for the whole invocation; later stages never reload it.

**Shared latest means no durable per-selection guarantee:** B may publish `c2` valid for B
and invalid for A. A's previous staged tree/attach stays on `c1`; A's next resolution fails
closed if no due/forced usable candidate, even if an old `c1` payload remains on disk. Give
row, recorded commit, reason and shared-record explanation; direct to a usable full-commit
pin or correct/remove the row. Update helps only if upstream moved again. Pin is a separate
pair and immune. Do not add history, index, global union, selection registry or retries.

**Reader constraint:** no mirror/record locks, fetch, materialization or record write; read
one record snapshot and validate own rows at its full commit with no lazy fetch. Never
consult pair stamps for a decision or create/write/touch them in any read-only surface. A record
can be READY for B and REFUSED for A simultaneously. Offline lint is static-only, never
represents its unknown content as validated. No shared payload alone proves row readiness.

**Exact reader classification:** no record or absent objects → NOT FETCHED; present invalid
objects/row/effective plan → REFUSED; record plus complete valid objects but absent published
payload → **NOT MATERIALIZED**; present validated payload → READY. NOT MATERIALIZED is a
derived, unpersisted SKIP (not failure), no delivers/registration or repair. Exact diagnostic:

```text
<row>: not materialized — objects present and valid; published payload absent; the next fresh launch or host writer rematerializes from local objects
```

Do not use missing-object/fetch/remote/network wording for this row; other rows unaffected.
Snapshot mirror/record/stamp/payload-root bytes before and after every reader. On a not-due
branch the fresh-launch or host writer revalidates/rematerializes from local objects under
the existing transaction, leaving stamp and mirror unchanged (no transport or auto-GC).
Due/no-record/force remain ordinary triggers; install/update are **not** promised network-free.

**New lock order:** mirror→skills-record leaf. Reload while holding the mirror; acquire
record-file lock only around short read-modify-write, reloading the full file at publication
to preserve other pairs. No fetch, validation, subprocess, callback acquiring another lock,
pack/fork transaction or second mirror lock beneath the record lock. One pair at a time;
release first mirror before starting next pair, including another ref on the same repository.
All writer entry points (fresh launch, non-observe host apply, install/update and online lint
scratch) call this grouped boundary only after declaring-pack resolution/selection; none
opens its own nested mirror/skills-record transaction. Atomic save reloads the whole file
under the leaf to merge unrelated pairs; read projection never calls the leaf writer.
Readers take neither. Proposed record remains `skills.lock.json`, not packs/forks locks.

| Acquisition site read | Existing held order | Sourced adapter consequence |
| :--- | :--- | :--- |
| `Store.Sync`, `Store.Materialize` in [store.go](../../internal/packsrc/store.go) | One `lockMirror(Repo)` through fetch/materialization | Never call checkout materializer for sourced rows |
| `refreshGrouped`→`refreshMirror` in [refresh.go](../../internal/packsrc/refresh.go) | One mirror at a time, released before later `WithLock` | Do not call whole `Refresh` inside sourced transaction |
| `WithLock` in [refresh.go](../../internal/packsrc/refresh.go); `RenamePackLock` in [rename.go](../../internal/packsrc/rename.go) | Pack-record only; load/modify/save | No sourced mirror acquisition inside that callback |
| `WithForkLockGuarded` in [forklock.go](../../internal/packsrc/forklock.go); `PinForks` in [forkpin.go](../../internal/packsrc/forkpin.go) | Fork-record→mirror during `refreshItems`; fork-record held across pin save | Separate lock identity; never invoke from sourced record leaf |
| `ProbeSeries` in [seriesprobe.go](../../internal/packsrc/seriesprobe.go); `ResolveOnto` in [onto.go](../../internal/packsrc/onto.go) | One mirror | No skills-record acquisition today |
| Patch checks in [patchcheck.go](../../internal/packsrc/patchcheck.go); replay in [replay.go](../../internal/packsrc/replay.go) | `lockMirrors` in [patchbranch.go](../../internal/packsrc/patchbranch.go), sorted unique repositories, possibly several held | Existing multi-repo paths unchanged; sourced writer never enters them |
| `RenameForkLock` in [rename.go](../../internal/packsrc/rename.go) | Fork-record only | Pair-keyed sourced records need no pack-handle rename |
| New grouped sourced writer | Exactly one existing mirror, then short skills-record leaf | Lock held across validation/publication, no reverse acquisition; record lock not held across network |

`lockMirror` uses `Store.Dir/locks/mirrorSlug(Repo).lock`, so every ref of a repository
shares one lock. Existing fork-record→mirror does not create a cycle: sourced code never
acquires fork/pack locks, and those paths never acquire skills-record. Do not claim the
whole existing store never holds two mirrors; that is true only of the new sourced path.

**Verified stamp adapter:** `stampPath` is `Store.Dir/stamps/mirrorSlug(Repo)/mirrorSlug(Ref)`,
not a field of any resolution record. Ordinary `fetchStep` stamps at its successful ref step.
The sourced caller alone defers this stamp until every required ref/pin/blob network step succeeds,
then writes once with the unchanged best-effort `writeStamp` helper **before** content validation.
Required pin-fetch failures already recorded by the shared helper are propagated only for the selected
sourced address; ordinary callers retain their best-effort behavior. Failed required network work,
including blob completion timeout/cancel, preserves stamp bytes/time for same-clock retry. Deterministic
local failures after ref success still stamp. Distinguish blob-fetch subprocess failure from local
resolution/enumeration/bound/OID errors. Thus a completely fetched invalid tip is paced hourly; failed
transport or failed best-effort stamp write is retried by current policy. Reuse this pair-keyed stamp;
do not introduce per-record due state. The author's accepted stamp correction replaces
the initial per-record assumption.
Ordinary Refresh of the same pair shares the stamp: either successful transport paces the
other; this coupling is accepted, not something to engineer around. `stampFresh` reads only
the existing stamp; eligible sourced fetch uses `fetchMirror`/`fetchPinnedCommits` and the
existing tag snapshot/restore behavior, then `writeStamp` on transport success, without
entering pack/fork/patch locks. No read-only projection calls those helpers.

**Constraint:** do not call unmodified `fetchStep` to decide whether a sourced record fetches:
its absent mirror-local ref arm fetches even with a sourced `R`. Compute sourced fetch
eligibility from no record/due branch/force, then reuse the existing transport/ref/tag and
stamp mechanics *when eligible*, with an internal sourced mode that defers stamping and propagates required selected-pin errors without changing ordinary `Refresh`/default `fetchStep` behavior. A usable
recorded full commit and not-due ref do not contact the remote merely because the mirror's
local ref is absent. Preserve ref classification/error behavior and tag restoration.
Add event-driven opposing-writer tests, not sleeps.

### 3.2 Payload copy and backend mapping

Proposed reserved launch-tree area: `_skill-payloads/<identity>/`, beside `_official`
and configured slugs, never inside a pack. `_skill-...` cannot be produced by current
slug escaping. Record it separately from `Packs`; an older-record fallback root walk
must explicitly skip it. Audit all three read paths: run `loadUnrecordedPackTree`,
entrypoint `loadPackRootAsStaged`, and macos-user `sandboxTmpdirEnvAt` fallback.
A typed record is not a new pack manifest; never call `LoadDir`, `HonoredPlugins`,
`packstage.Stage`, `ManifestWith` or `SkillsSources` on a payload.

| Setup | Physical payload target seen by jail | Source evidence / effect |
| :--- | :--- | :--- |
| Podman (Linux/macOS) | `/ctx/packs/_skill-payloads/<identity>/<entry>` | `packCtxDir = paths.ContextPacksDir`; assembler binds launch tree `:ro` and sets `YOLO_PACK_ROOT` |
| Apple Container | `/home/agent/.yolo-packs/_skill-payloads/<identity>/<entry>` | `acPackRootRel`, `acPackRootInJail`; assembler `acMaterializeTree` copies through workspace-state home bind; no read-only integrity guarantee for guest copy |
| macos-user, self/account | `/Users/Shared/yolo-staged/packs/<cname>.<launch-tree-leaf>/_skill-payloads/<identity>/<entry>` | `runplan.BuildRunPlanWithStages` calls `StagedPackTreeRoot`, uses result for bootstrap `YOLO_PACK_ROOT`; `StagePackCommands` reserves, `cp -R`s and seals `a-w,a+rX`; no mount namespace |

**Constraint:** factor a single explicit launch-tree-relative→jail-root mapping and pass its
result into sourced plans before host staging composition. For macos-user call the actual
`StagedPackTreeRoot` with the same cname/launch leaf that the run plan uses, not `/ctx/packs`
or a home guess. Do not rely on comments claiming `/var`: the current code uses
`/Users/Shared/yolo-staged`. Run's native dispatch passes the host tree to that plan.
Host apply uses the host store payload path instead. Tree records retain relative locations,
not a reusable host absolute path. Confine both lexical locations and physical ancestors
when reading a record; no location or registration target supplied by declaration data.

**Constraint:** copy each needed payload once per launch, preserving payload links as links
and rechecking whole-root confinement without following cycles. Default pack copiers still
refuse/dereference their own input links. AC's current `copyTreeBeneath` `ReadFile`s linked
entries and normalizes modes to 0644/0755; it is not a payload copier. Add typed payload copy
beneath `os.Root` with no-follow reads/writes; do not broadly permit source symlinks there.
Mac's external copy/seal must preserve safe relative links, including cyclic supporting links;
native verification is still owed, not claimed from reading `cp -R` argv.

### 3.3 Registration writer and stale/digest paths

**Constraint:** carry selected rows as typed metadata, not parent directories appended to
`Layer.Sources`. A registration plan lists real directory, private regular marker bytes,
optional license bytes and each owned top-level link with its computed absolute target.
Validate selected scope before producing this plan. Then validate its selected directory,
resolved marker and every target against the **effective** whole-payload plan after all
omissions. A case-colliding ancestor (`skills/Foo/chosen` versus `skills/foo/other`) can
invalidate a raw-valid selected subtree. Refuse the whole row by omitted path/cause, not
retain that case group, copy around omission or emit a READY dangling target. Unrelated
`docs/A` versus `docs/a` groups still only omit-and-count. `SKILL.md` may resolve through an
in-skill link upstream; registration still contains a private regular blob. Links for other
entries must not point to the host payload at the jail notch.

Reuse `renamedSkill` byte-preserving frontmatter semantics, including missing `name` handling;
payload marker stays upstream-exact. Reuse ownership/result reporting and the common
`writeLayer` boundary, not a second host-only composer. `treedigest.Of` already hashes
READLINK text without following it; a staged dangling link is not missing content.

**Verified sharp edges:** `jailcontent.prepareSkillTarget` composes privately, then
`treesync.Sync` refuses links. Introduce an explicit plan-aware sync that accepts only the
exact registration links the current plan owns; preserve ordinary `Sync` refusal. It must
use Lstat/Readlink, preserve destination directory inode, update changed links atomically,
and retire stale entries without following targets. Built-in/workspace bytes remain ordinary.

`Archive` and `moveTree` fall back to `copyTree`, which dereferences every link. A sourced
registration replacement/retirement must use a typed no-follow archive copy for its owned
links (copy first, remove only after success), leaving ordinary archive behavior unchanged.
Carry owned entry types/target texts into provenance when needed to retire a row absent from
the *new* plan. Do not infer permission from an arbitrary on-disk symlink. Hash/read private
files without following unexpected substitutions; do not adopt a dangling jail-target link
as user data or pass a sourced registration through the ordinary source copier.

### 3.4 Governance and collisions

**Verified:** `Contribution.SkillsSource` and `SourceKey` currently turn omitted `from` into
`skills`; `governors` folds equal keys; `governedSkills` calls `skillsDir` on that key;
`SkillsSources` deduplicates resolved directories. `borrowingSources` is derived from
readable governed sources, so an unfetched sourced row would currently vanish from routing.
`ResolveDestinations` clones keep `origDecl` and append synthesized destination rows.
`layerClaims` collapses local same-name sources; `dedupePackClaims` then collapses same
pack/author; `Collisions` skips a destination with any `Layer.Unresolved`.

**Constraint:** sourced row identity is distinct from local-directory governance and from
pair/payload identity. Preserve declaration index/provenance through clones and audience
routing; several aliases of one directory must survive directory dedup. Sourced rows never
become an omitted-`from` governor of the declaring pack's conventional `skills/`, never widen
root-plugin audience, and never suppress implicit broadcast of genuine local skills.
Do not encode sourced identity as a fake local path and feed it into `skillsDir`.
Static duplicate validation must allow distinct delivered aliases, yet refuse duplicate
claims; preserve old duplicate local-source rules. Cheap: internal identity struct spelling.

**Constraint:** compute sourced (destination, delivered name) claims from declarations,
including NOT FETCHED rows. Do not mark that state `Layer.Unresolved` merely to suppress
collision checks; unrelated truly unresolved local layers still suppress the destination
as today. Keep same-pack declaration row identity in sourced claims; do not erase it via
`collectSkills`, `layerClaims` or `dedupePackClaims`. Add built-in shipped names, their
prefix aliases, and rendered mechanics to sourced-only preflight at both notches before
ordinary pack/built-in override writing. Ordinary non-sourced source precedence and local
conventions remain unchanged; two different selected packs already cannot override alike.

Reserved claims are withheld/disclosed, not fatal. Workspace/import layers are lower and
shadowed with contributor disclosure. `agents` and `describes` use existing per-destination
routing/gates; resolve every selected non-withheld sourced row regardless of audience.
Do not change workspace `confinedTree`, import broker authorization or import cache.

## 4. Tests-first units

These are serial candidate commits, not parallel writers or individually landable feature
releases. Each starts clean at its predecessor,
adds permanent tests through existing consumer callers, records RED, implements, and runs
its named focused gate. Do not leave an enabled production half-feature. Parent owns final
landing review, census, full gates. If a unit needs unruled behavior, stop instead of enlarging it.

### Unit 1 — declaration, governance identities and static claims (first)

**Tests first:** preserve
[`TestPackLintSourcedSkillDirectory`](../../internal/cli/gitskillsubtrees_test.go) unchanged.
Its real `packMain`→`packLint` assertion is valid for the revised declaration. Reproduced:

```sh
go test -short ./internal/cli -run '^TestPackLintSourcedSkillDirectory$' -count=1
# RED: skills[0]: json: unknown field "as"; exit 1
```

Add table cases through `lintReport`: valid TOML/legacy JSON; invalid TOML
`[[contributes]]`; missing ref/subdirectory, source+from, URL password, plain file directory,
bad normalized name, license without source, root skill, path traversal. Assert actionable
row labels. Add `ResolveDestinations` caller cases with two aliases, conventional local `skills/`,
audience and describes; inspect inferred rows rather than a private identity helper.
Strict collision behavior is tested in Unit 3 and through real check selection in Unit 5.

**Change:** declaration/fork placement validators and source/governor branching from map;
typed metadata preserving unresolved declarations without reading them as directories.
No global filter, registry or compatibility mechanism. Keep old skew tests untouched.

**Gate:** focused `go test -short ./internal/packdecl ./internal/packload ./internal/cli ./internal/cli/check -run 'SourcedSkill|DecodeForUseSkipsAContributionWithAnUnknownField|SkewSkipNotesSayUpdateYolo|Governed|Borrow' -count=1`.
The existing regression must turn green by accepting fields at the *real lint caller*, not a
callee-only acceptance test. Commit: `feat(packs): declare sourced skill registrations`.

### Unit 2 — grouped store resolution and immutable whole-object payload (after 1)

**Tests first:** new `internal/packsrc/skillpayload_test.go` drives the public grouped store
writer/reader API that production selection will call; real synthetic Git objects, no remote
network. Branch with two selected rows in separate declaring packs: first tip valid; next tip
valid for row one, invalid for row two. Assert record stays old and **both** outcomes use old
bytes; reverse row order, then three rows across two packs with only row three invalid.
Assert **no candidate payload** and, with no old record, fatal/no publication. Concurrent
B publishes `c2` while A waits; A reloads under lock and refuses its invalid row, never
uses a stale `c1` pointer. B's reader is READY, A's reader REFUSED against the same record.
Pin A to `c1`, advance B's branch, and assert A pair immune. Use channels/readiness lines
at real mirror acquisition/publication: a test holds the actual flock through `lockMirror`,
waiting notice establishes A's wait, B releases only after publication. Wrapper Git logs
prove no remote event for not-due/pin/tag and no-lazy-fetch readers; generous hang guards
are not timing pass conditions.
A first-writer helper test alone does not prove production collection of the complete pair;
Unit 5 repeats rollback through install/launch callers.

**Fixture matrix:** pinned commit, tag, due/not-due branch with injected clock; unreachable
cached/never-fetched; all blobs missing in a promisor mirror; existing record with deleted
local branch ref, not-due pair stamp and removed remote must resolve by full commit with stamp byte-identical; successful invalid-tip
fetch paces another sourced invocation, failed transport/stamp write preserves old retry;
ordinary Refresh success on the same pair shares pacing. A controlled local promisor completion failure
must leave the due stamp/record/payload unchanged, warn and use only valid latest-record fallback;
same-clock retry completes and stamps, while a complete invalid candidate stays paced. A required
full-commit pin-fetch failure likewise leaves no fresh stamp/record and retries, with an ordinary
caller control retaining its ref-success stamp. These test authored local origins only, never upstream.
Config-defined smudge marker
must remain absent; ready payload shared by aliases/refs; writer cancellation before record
save; readonly no mkdir/fetch/write, missing object; rename races and format version identity.
Object tree includes safe in-root links/cycles, dangling/absolute/escaping links, gitlinks,
case-collision groups and executable regular blobs. Selected subtree must reject dangerous
modes/links/cycles/case ambiguity/missing marker; outside omit-and-count. Test listing/entry/
byte/target limits at boundary and over, with charged omissions; explicit/default license,
case-insensitive basename conflict. Add selected ancestor case group and unrelated supporting
case group to test effective-plan survival. Delete published payload only: reader is NOT
MATERIALIZED, no writes; not-due writer restores from valid objects with removed remote,
stamp/mirror unchanged. Invalid objects/row must report REFUSED before absent payload.
No arbitrary script runs in this unit.

**Change:** pure bounded tree and row plans, writer-only blob completion and atomic payload/
record publication, typed reader states and pair-keyed successful-fetch pacing.
Scratch belongs to test-owned temp directories or durable receipts, cleaned with writable-mode
restoration for sealed payloads. No checkout-based `Materialize`/`Refresh` for sourced content.

**Gate:** `go test -short ./internal/packsrc -run 'SkillPayload|SkillsLock|Refresh|ForkLock' -count=1`.
Commit: `feat(packs): resolve grouped skill payloads from Git objects`.

### Unit 3 — common registration plan/writer and guarded links (after 2)

**Tests first:** new `internal/hostskills/sourcedregistration_test.go` drives
`ComposeHostSkillsFor` + `RenderHostSkills`, and `ComposeInto` via real layer conversion.
Two aliases have different regular marker name lines but shared target payload, identical
supporting bytes and executable modes. Directory-entry inventory is exact: real directory,
regular marker/license, only planned links, no wrapper/plugin manifest. Marker-only skill,
upstream marker symlink, license basename conflicts and describes/audience withholding.

Permanent real caller tests cover all strict sourced collisions (same pack local/sourced,
sourced/sourced, other pack, generated and bare/prefixed built-ins, rendered mechanics),
reserved withholding, workspace shadow and old local override semantics. Tests through
`RenderHostSkills` must assert **no adoption/write** occurs on refusal. Same non-sourced
local sources still retain later-source-wins behavior.

Exercise second render with changed payload, alias removal, unplanned destination link,
unchanged digest and dangling in-jail targets. Drive actual archive fallback under injected
rename failure, verify link target text preserved without reading target; fallback failure
leaves original intact. Ordinary symlink source delivery still dereferences; generic sync
still refuses. Planned sync keeps directory inode and removes stale links without touching
external target bytes. Capture no-follow race errors as errors, not missing-success.

**Change:** typed owned entry plans on `Layer`/writer and narrow archive/sync APIs; no
ambient symlink allowance. Reuse `renamedSkill`, ownership records and digest, extending only
owned typed fields that the stale path requires. No agent switch.

**Gate:** `go test -short ./internal/hostskills ./internal/treesync ./internal/jailcontent -run 'Sourced|Registration|BothNotches|Dangling|Collision|Reserved|Sync' -count=1`.
Commit: `feat(skills): compose private registrations with planned payload links`.

### Unit 4 — selected host/launch wiring, tree records and attach (after 3)

**Tests first:** new real host-apply regression in `internal/cli/gitskillsubtrees_test.go`
uses isolated home/config/store and public apply path; staged-jail test in
`internal/cli/run/gitskillsubtrees_test.go` runs `stagePacks`/fresh-launch path and
`PrepareSkillsWith` with actual selected packs, then records/reloads through `loadPackTree`
and entrypoint `LoadJailPacks`. Assert separate payload area, exactly selected registrations,
no upstream skills/plugin/hook/program/needs/surface contributions in loaded pack set or
staged skills destinations. Do not hand-load the upstream repository as a pack to filter it.

Assert exact target text for each backend, using `StagedPackTreeRoot` for both macos-user
modes and real AC assembler copy under isolated state. No host follower may open dangling
jail link; digest succeeds. Test omitted payload links/gitlinks and copied safe links/modes.
Delete mirror and store payload; `runningJailPackView`/`adoptPackRecords` + skills refresh
must still read launch bytes, without writer, fetch, or rematerialization. A changed/unfetched
current declaration cannot prevent attach from using the running recorded tree. Validate
malformed traversal, link-ancestor and unknown-payload/row-reference records fail by name;
old records without new optional fields keep working. Payload area excluded from legacy
walks and from all pack/plugin discovery. Shared aliases stage one payload per identity.

**Change:** grouped writer only after complete selection and fresh-launch disposition;
early comparison staging remains declaration/read-only for sourced rows. Attach adopts
running record before sourced composition. Host apply fetch-capable selection is separate
from observe/read-only. Carry typed state through destination clones, `jailSkillSources`,
`SkillPlan`, `adoptPackRecords`, inventories and record load. Use actual [backend mappings](#32-payload-copy-and-backend-mapping), and typed copy/sync rather than weakening default copiers.

**Gate:** `go test -short ./internal/cli ./internal/cli/run ./internal/entrypoint ./internal/packload ./internal/macosuser -run 'Sourced|SkillPayload|PackTree|PackSkills|BothNotches|WorkspaceSkills' -count=1`.
Commit: `feat(packs): stage skill payloads and preserve recorded attach delivery`.

### Unit 5 — explicit writers and honest reader/disclosure surfaces (after 4)

**Tests first:** `packMain` drives offline/online lint, install/update, explain, status and
footprint; check drives actual `sectionPacks`; launch dry-run drives real no-write path.
Each test snapshots store/record/stamp bytes and a fake Git transport event log to prove
readers never fetch, touch stamps or create payload/record files. Toggle due/not-due stamps
with identical reader outcomes; code paths must not consult stamps for fetch decisions. Baseline check may privately
stage selected packs;
that existing transient behavior is not a durable sourced writer.

READY/REFUSED/NOT FETCHED/NOT MATERIALIZED listings are separate; only READY is under
`delivers`. Deleted-payload readers emit the exact NOT MATERIALIZED diagnostic and no
missing-object/fetch/remote text for that row; skip and store hashes unchanged. Drive not-due
repair through actual fresh launch and host apply with unreachable remote; restored links,
no Git transport, unchanged mirror/stamp. Drive raw-valid but effective-omitted selected
ancestor through actual writer: whole row REFUSED by path/cause, no payload/registration;
unselected supporting case group stays READY. Preserve install/update force semantics.
Offline exact note contains `contents` and `not verified`; online uses its own scratch store
and removes it. Missing source read-only check is SKIP + row + `yolo pack install`, while
explicit writers and first launch/apply fail by contributor with a next step. Cached refresh
failure warns, old commit/new candidate/failing row named; install/update return nonzero for
requested failed fetch despite fallback. Through install and fresh launch, advance branch
to a tip invalid for the second selected row: no record advance or mixed delivery. Check
strict collisions hold even with never-fetched sources, subject to existing unrelated
unresolved-layer policy. Legacy only must withhold and produce **zero Git events**; exclude
alone still registers sourced skills.

Footprint/explain emit one payload deployment claim (repo/ref/commit, total files/bytes/
executables, omissions count+paths, whole-repo readability and no activation), and separate
row registration claims (names/agents, selected executables, license added, rename). Alias
cost/count must not duplicate payload deployment. Unselected metadata contributes **no**
plugin/hook/skill/program claims. Status reports per pair and recorded state without fetch.
Update help/diagnostics together, not a later documentation-only patch.

**Change:** wire grouped writer after declaration selection in install/update/online scratch;
wire reader projection in check/explain/footprint/status/dry-run and launch disclosures.
Do not use `RefreshConfiguredPacks` as a reader; it returns early when no configured Git
packs, even if local selected packs contain Git-sourced rows. Do not route through workspace
imports or parse payload declarations. Preserve old skew message prose `(update yolo)`;
there is no invented `update-yolo` command.

**Gate:** `go test -short ./internal/cli ./internal/cli/check ./internal/cli/run ./internal/packload -run 'Sourced|SkillPayload|PackLint|PackExplain|PackFootprint|PackStatus|DryRun' -count=1`.
Commit: `feat(packs): report sourced deployment separately from registration`.

### Unit 6 — controlled end-to-end dependency witness and regression gates (after 5)

**Tests first:** add `integration/gitskillsubtrees_test.go`, package integration, serial,
`requireJail(t)`-gated. Reuse suite local-repository/config-isolation/launch and attach fixture
patterns in [attachpacktree_test.go](../../integration/attachpacktree_test.go); no agent or
upstream scripts. Use durable fixture/scratch, standard shared Go caches, full cleanup.

The synthetic repository contains `VERSION = payload-v1`, `shared/data = sibling-v1`,
selected `skills/chosen/scripts/probe.sh` (executable), sibling skill marker, plugin manifest,
hook bytes and outside executable. The **controlled test-authored** script is:

```sh
#!/bin/sh
set -eu
here=$(dirname "$0")
printf '%s\n' "$(cat "$here/../../../VERSION")" "$(cat "$here/../../../shared/data")"
```

Run it by actual exposed **host registration** `<dest>/<name>/scripts/probe.sh`, never its
payload path. Run by actual exposed **jail registration** path in the matched synthetic jail.
Both must print exactly `payload-v1\nsibling-v1\n`. This fails if registration linkage or
backend path mapping is removed; merely asserting bytes exist is insufficient. Unit 4 also
executes through a staged test notch whose root mapping is physically readable and asserts
production backend target strings; the integration jail settles the actual jail mapping.
Include a negative partial-fidelity witness: a top-level registration `../` climb is not
reported as supported, and no `_bmad` workspace setup is created.

Remove host mirror/payload after first launch, attach and execute the exposed path again;
assert identical output and zero sourced fetch events. Verify no hook/script sentinel ran
because of yolo, no unselected destination entries, no unselected claims, unchanged workspace
import store and no smudge sentinel. Payload remains readable outside discovery. Aliases
share one launch payload. No installed coding agent invoked, even for discovery checks.

**Mandatory suppression for later probes:** emitted absolute dev launcher from
`just build-dev-bundle`, isolated home/config and throwaway workspace under owned durable
scratch; `YOLO_NO_PROGRAM_READINESS=1`, `YOLO_NO_AUTO_CAPTURE=1`,
`YOLO_NO_UPDATE_CHECK=1` on host and effective launch environment; remove containers,
stores, sealed trees and scratch at exit. No bare installed `yolo`, deploy, installer,
credential/config/grant edits or network fetch. Build bundle is not authorization to capture.

**Gate:** `go test -run '^TestGitSourcedSkill' ./integration` and the matching source-named
unit tests. Record real caller deletion evidence in disposable authorized candidate hunks:
removing host apply grouped-resolution call fails apply test; removing fresh-launch grouped
call fails aggregate rollback/staging test; removing `writeLayer` registration call fails
exact inventory/script witness; removing record→pack metadata hydration fails deleted-store
attach test; removing install/online/reader projection calls fails their CLI tests. Restore
before committing; reviewer role must not be asked to mutate. Store helper tests alone are
not evidence for those production calls. Commit: `test(packs): witness sourced relative paths and inert upstream effects`.

## 5. Ships with; fences and final gates

**Builder docs:** update [pack-system.md](../reference/pack-system.md), selected
[config editing/reference index](../reference/config/README.md) links if required, and
[user guide packs and skills](../../userguide/guides/packs-and-skills.md) with actual declaration/commands,
whole-repo readability, separate discovery, relative-path limits and old-build skip distinction.
Graduate this design only after production and measurements exist; no public reference
claim or defaults edit is part of the planning commit. No changelog entry at landing.

**Tier-3 scope fence (human review plus diff):** no production edit in this planning task;
build tasks cannot change shipped pack membership/defaults, dotfiles request, workspace
imports/broker/grants, owner's live configs/credentials, dependency/vendor set, agent registry,
generic source filters, other contribution kinds or license opt-out. No upstream/model/agent/
API/install/capture/deploy calls. Isolation is structural in test temp/durable homes, not
merely a reminder to avoid the owner's home. No extra worktrees/ref switches/resets.

Parent owns one final combined `just check-ci`, completion census for any new out-of-package
source-reader tests, full launch integration suite and change-aware `just done` after clean
commits. Builder runs `just format`, scoped gates, `git diff --check`, records RED→GREEN and
commit at each coherent step; never skip/waive the existing red test to create green optics.
This planning checkpoint claims **no whole-suite green**, no native Mac run, no upstream
compatibility, no measured 256 MiB launch copy cost, and no native agent discovery proof.

## 6. Mac checks

These are builder follow-through, on **main after Linux landing**, not a pre-landing hold
or a branch delivery. First main commit containing Unit 6 (SHA filled by lander), or later.
The owner’s Mac agent uses a matched bundle from that main checkout, fresh owned durable
workspace and isolated test home, with the three suppression variables above.

1. **W-GSS1 — Apple Container mapping/copy:** run the source-named integration witness with
   the suite's Apple Container setup; report exact exposed script output, `Readlink` targets,
   payload relative inventory/modes and omitted link/gitlink counts. Confirm host tree bytes
   unchanged by guest mutations; attach still uses original recorded host tree.
2. **W-GSS2 — macos-user both modes:** run the same witness in `seatbelt-self` and
   `seatbelt-yolo-account`; report actual `YOLO_PACK_ROOT` matching `StagedPackTreeRoot`,
   exposed script output, preserved relative supporting links/cycle, sealed modes, cleanup
   and unchanged owner paths. Do not infer either mode from Podman.
3. **W-GSS3 — filesystem/copy refusal:** run targeted selected/outside case-collision,
   symlink-copy and forced archive-fallback tests on the Mac filesystem; record strict row
   refusals versus omission groups, no-follow behavior and no leaked scratch.

Exact new test commands/setup selectors and diagnostic literals must be filled from the
implemented Unit 6 before this section is handed to the Mac agent; no future test is claimed
runnable now. Native checks settle path/copy semantics, not agent discovery or API behavior.
