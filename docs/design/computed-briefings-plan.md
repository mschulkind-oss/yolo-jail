---
title: "Computed briefings: current-tree implementation handoff"
date: 2026-10-28
status: in-review
stage: DESIGN
next: "Resolve OQ-CBP1 and OQ-CBP2; materialize and verify the specified operation controls in an authorized implementation lane"
depends-on:
  - computed-briefings.md
  - pack-manifests.md
  - ../reference/config/rulings.md
tags: [briefing, packs, derive, plan]
summary: "Current callers, exclusive component fences, proposed interfaces, and remaining API contracts for computed briefings; no implementation authorized from this document yet."
vantage:
  status-chip: true
---

# Computed briefings: current-tree implementation handoff

**Status:** 2026-10-28. Written against `c7b58bc5a2484793049d555b5a3e46b08f827241`
(tree `36fd267f1fbcef31fd617bca973d403cc7aefde9`); runtime source and build inputs are
unchanged from `e02bb526bc5b4d59fa5ba7293163cdee44f3ff78`. The accepted design and prior
handoff were read in full. The three printed baseline red controls below were observed by the
prior planner using temporary tests, then removed; this continuation ran no software tests or
mutation probes. Exact operation fixtures, assertions and deletion controls are now specified
as **unexecuted test recipes**, not runtime evidence. Two observable access-projection choices
remain pending. This is not a ready-to-build claim or authorization to implement.

**Design:** [computed briefings](computed-briefings.md). **Precedence:** the design wins on
behavior; the tree wins on cosmetic facts; the plan is advice and is the first thing to be wrong.
Never twist code to match this map. Stop and report a missing symbol, another writer's dirty
file, a noncosmetic contradiction, an unexpected red result, an out-of-fence change, or an
out-of-scope gate failure. Do not silently expand a unit's ownership.

**Binding versus advisory:** the quoted settled behavior is binding by the linked design.
Current-source constraints are marked **must**. Proposed interfaces are labeled **new,
proposed**; they are not claims that APIs exist. Factoring advice includes its reason. Cheap
choices are explicitly marked; unresolved contracts are not implementer choices.

Terms such as briefing producer, access table, notch, grant, and working set retain the
[accepted design's definitions](computed-briefings.md#defined-terms). **Composition batch**
*(coined here)* means the complete, validated set of destination bytes for one invocation,
held in memory before any briefing write; it is not a broker transaction or a replacement
for inode-preserving publication.

## Readiness and exact dependencies

The three [owner rulings](computed-briefings.md#decision-ledger) are settled. Do not re-open:

- Core supplies approved access facts directly, not through a pack-published proxy.
- A producer receives only named inputs, not the effective config or surface-derive tables.
  Pack-owned typed settings are deferred until an actual need exists.
- Lua error, timeout, invalid return, or oversized return aborts the composition and invoking
  operation. No stale, partial, or fallback prose is delivered for the failed invocation.

The design's grant terminology is independent of a config rename. The dependencies above
name overlapping source/API work, not a universal wait for a green base:

| Prerequisite | Needed by | Exact handoff |
| :--- | :--- | :--- |
| Manifest reader/producer representation from [pack manifests](pack-manifests.md) | Unit A | Confirm the landed `Contribution` fields and grouped `[[briefing]]` lowering; reserve `derive` and `reads` without reusing a surface-producer DTO. Current base has neither field. |
| Approved carrier from [readable config plan](../reference/config/rulings.md#how-it-was-built) | Unit C | Transfer raw approved remote names, scope form, repositories, grants, and effective grant-kind eligibility without parsing display labels or rereading requested workspace config. Current predecessor is `ScopeCheck` → `Options.approvedScopes` / `approvedGrants`. |
| Existing scope/keeper version contracts | Units C–E | Specify backward reads and keeper transport before adding remote-name facts; no credential, host path, or new grant may enter Lua. |
| Verified red tests at actual callers | Every build unit | Three printed baseline probes were observed as described below; operation recipes below are specified but not materialized or verified. Materialize permanent tests only in an authorized implementation lane and record actual red output before changing production. |

Explicit historical lookup: `d8ed0c9dd` was inspected with `git show` only. Its
`readablescope.go` introduces `readableScopeCheck` and `scopeCheckFromReadable`; it still
projects into `ScopeSource`, and its contribution type does not establish a briefing-producer
API. It is not the current base and is not a landing prerequisite by SHA. No private candidate
symbol below is presented as current. Rebase this map on the actual landed producer/carrier
handoff rather than cherry-picking that lookup.

## Map and exclusive component ownership

Paths below are repo-relative. New files are explicitly marked. A build unit owns its tests
alongside its component. Shared files have an ordered owner, never two concurrent writers.

| Unit / owner | Paths | Change |
| :--- | :--- | :--- |
| A — manifest and governance | [`internal/packdecl/contributes.go`](../../internal/packdecl/contributes.go), [`packdecl.go`](../../internal/packdecl/packdecl.go), [`toml.go`](../../internal/packdecl/toml.go) | Declare and validate producer/read fields, source identity, duplicate rules, strict/tolerant decoding and TOML locations. |
| A | [`internal/packload/governance.go`](../../internal/packload/governance.go), [`mergedest.go`](../../internal/packload/mergedest.go) | Describe a virtual producer source without reading it as a file or assigning it remainder governance; make it count as content for destination resolution. |
| A | New `internal/packdecl/briefingderive_test.go`, `internal/packload/briefingderive_test.go` | JSON/TOML parity, backward reads, routing and real-file collision controls. |
| B — Lua registration and evaluation | [`internal/agentcfg/luahook/derive.go`](../../internal/agentcfg/luahook/derive.go), [`vm.go`](../../internal/agentcfg/luahook/vm.go), new `briefing.go`, `briefing_test.go` in that package | Register the separate producer space and expose a narrow context/evaluator using the existing sandbox. |
| B | New `internal/packload/derivebriefing.go`, `derivebriefing_test.go`; [`deriveenv.go`](../../internal/packload/deriveenv.go) only for shared script-loading/error handling | Strict producer pairing, one session per pack/batch, named-source filtering, contextual diagnostics. Do not hydrate provider credentials for this path. |
| B, after A | [`internal/cli/pack.go`](../../internal/cli/pack.go), new `internal/cli/packbriefingderive_test.go` | Lint both pairing directions and list deliveries/reads; footprint must disclose reads too. |
| C — applied access facts | [`internal/config/scopeapproval.go`](../../internal/config/scopeapproval.go), [`internal/cli/run/brokeredscope.go`](../../internal/cli/run/brokeredscope.go), [`keeperplan.go`](../../internal/cli/run/keeperplan.go), [`keeper.go`](../../internal/cli/run/keeper.go) | Preserve the gate-approved carrier into launch and keeper, not display-label parsing. Coordinate carrier hunks with the config owner before writing. |
| C | [`internal/brokerscope/file.go`](../../internal/brokerscope/file.go), [`live.go`](../../internal/brokerscope/live.go), [`internal/brokerstore/working.go`](../../internal/brokerstore/working.go); new tests in their packages | Applied remote-name persistence, explicit read diagnostics, bounded locked snapshots and backward reads. |
| C | New `internal/cli/run/briefingaccess.go`, `briefingaccess_test.go`; [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) | Build core-owned facts from approved/live scope and actual start disposition, using the contracts below. |
| D — composition and launch/attach | [`internal/cli/run/prepare.go`](../../internal/cli/run/prepare.go), [`packs.go`](../../internal/cli/run/packs.go), [`packtree.go`](../../internal/cli/run/packtree.go), [`run.go`](../../internal/cli/run/run.go) | Evaluate at composition, not static staging; batch before writes; preserve running pack tree and applied state on attach. |
| D | [`internal/entrypoint/hostbriefing.go`](../../internal/entrypoint/hostbriefing.go), [`internal/cli/applyhostbriefings.go`](../../internal/cli/applyhostbriefings.go), [`internal/jailcontent/briefing.go`](../../internal/jailcontent/briefing.go) | Shared per-destination producer projection, host error propagation, no duplicate host evaluation, section/order parity. |
| D | [`internal/cli/run/briefingparity_test.go`](../../internal/cli/run/briefingparity_test.go), [`jailbriefingaudience_test.go`](../../internal/cli/run/jailbriefingaudience_test.go), new `computedbriefing_test.go`; new host caller tests | Test real refresh/render and actual operation aborts, not only the evaluator. |
| E — live apply and delivery verification | [`internal/cli/run/liveapply.go`](../../internal/cli/run/liveapply.go), [`mechanicsrecord.go`](../../internal/cli/run/mechanicsrecord.go), new `liveapplybriefing_test.go` | Refresh after successful scope replacement under launch lock; preserve exact applied facts and fail the operation on producer failure. |
| E | New `integration/computedbriefing_test.go` | Pack fixture only: fresh launch, attach, scope apply, fatal operations; no interactive agent or API call. |
| E | [Pack reference](../reference/pack-system.md), [briefing reference](../reference/agent-briefings.md), [live-apply design](live-config-apply.md) | Correct overlapping current claims and document new delivery/refresh/failure behavior. This plan remains until graduation. |

The source containing a package is shown once in the map; new filenames are proposals, not
existing files. A/B can prepare in disjoint worktrees after their handoffs. C can prepare its
snapshot component independently of Lua. B's CLI changes follow A. D follows A/B/C; E follows D
and owns the later live-apply hunks. No build unit may edit shipped personal finishing prose.

## Reuse before writing

| Existing seam | Reuse and constraint |
| :--- | :--- |
| `newDeriveSession`, `guardUnknownAPI`, `openSandboxLibs`, `wrapLuaErr` | Use the same safe libraries and context cancellation. Registration-only listing runs the top-level script but must never invoke producers. |
| `DeriveRegistrations`, `EnvRegistrations` | Separate listing APIs already exist; the seed incorrectly implied one listing covers every registration space. Add a separate briefing listing, not a fake surface named `briefing`. |
| `packload.DeriveScript`, `AgentEnv` | Script discovery/error attribution patterns only. `DeriveScript` currently turns every read error into empty script; a declared producer needs an error-bearing read so an unreadable script cannot become a harmless missing registration. |
| `Contribution.SourceKey`, `Pack.declaration`, `GovernedSources`, `GovernedBriefingFor` | Preserve original declaration governance through `ResolveDestinations` clones. A producer needs its own key and virtual path, not the empty key for conventional remainder prose. |
| `JoinBriefingSources`, `ComposePackBriefings`, `ComposeBriefingSections` | Keep right-trimming/blank-line joining and one section per pack. Merge virtual and physical sources by byte-wise `Rel` before joining; pack config order stays unchanged. |
| `briefingDestinations`, `briefingStagingName` | Jail destination deduplication and mount staging spelling. Producer context uses the destination's declared `Agent`, not its owning pack name. |
| `HostDeliveredBriefing`, `HostWithholdsBorrowing`, `sortedUniqueSources` | Host delivery/`describes` gates and deduplication remain applicable. Do not turn a producer declaration into an implicit broadcast of unrelated physical files. |
| `recordApprovedScopes`, `ScopeCheck.ApprovedGrants`, `writeScopeFiles` | Gate output is authority input; approval records on disk are not a substitute for this invocation's result. Keep labels and source names distinct. |
| `LiveFilesContext`, `ReplaceScopeContext` | Source-locked live scope discovery and identity-checked atomic replacement. The composer must not call either under a recursively acquired source lock. |
| `Store.Working`, `WorkingEntry.Live`, `SameWorkspace` | Working-set filtering patterns. `Working` already takes the store transaction; wrapping it in that same lock deadlocks. Its current API has no context deadline. |
| `gitproto.Identity`, `brokerscope.ReadCovers`, `AnyRepo` | Normalize git URLs exactly as the broker does and expand wildcard grants over concrete approved identities, never expose `*` as an access-table repository. |
| `jailPack`, `jailDest`, `jailBriefingsWith`, `goldenOptions` | Existing jail test setup through real refresh; no synthetic standalone composer assertion can replace it. |
| `applyFixture`, `answer`, `swapSeam` | Existing apply fixtures drive the public operation and scoped seams; preserve fake-runtime and symlink-resolved paths. |

## Current ordering and traps

**Must:** no host login probe, git subprocess, filesystem API in Lua, whole config, token,
credential pointer, host path, new grant, or pack-published facts. The briefing advises; the
broker enforces. Reading already-approved core facts cannot widen authority.

| Event / current caller | Current facts and ordering | Required connection |
| :--- | :--- | :--- |
| Fresh container: `runContainer` → refresh at `run.go:2289` | Refresh happens before `keeperPlanFor` / keeper spawn. Keeper receives `ApprovedScopes` and `ApprovedGrants`, then starts services and writes scope files. | Planned candidates are not actual running brokers. Carry the final placement/start result back to composition before agent delivery; contract C2 below is required. |
| macos-user: `Run` arm → `startLoopholesDisclosed` at `run.go:820`, refresh at `:1109` | Services start before refresh; fresh gate result belongs to this invocation. No attach. | Use returned handles/refusal disposition, and unwind owned services on fatal composition before bootstrap/agent startup. Do not apply the container ordering here. |
| Attach: `attachExisting` at `run.go:2938` → refresh at `:3140`, or `refreshLegacyJailBriefings` | `runningJailPackView` supplies the running pack tree; authenticated `readMechanicsApplied` supplies applied body/extra/workspace-skills facts. Current requested cfg is still an argument. | Access facts come from live scopes/liveness, not that cfg. Both normal and legacy refresh paths must evaluate producers or abort; no compatibility fallback for producer failure. |
| Host: CLI `applyHostBriefings` → `ComposeHostBriefingsFor` then `RenderHostBriefings` | Composition currently returns no error and is repeated for reporting/rendering. Host has no broker; files are machine-wide. | Evaluate once into a batch; propagate error through report/render and the actual host command. Every declared source is false with the host reason, not a workspace grant query. |
| Live apply: `configApply` → `liveApply` → `runApplySteps` → `applyStep(changeScope)` | `holdLaunchLock` surrounds the prompt and apply. Each file is replaced independently via `replaceScopeReposSeam`; folded keys and mechanics are updated after steps. | One refresh after successful scope mutations, using applied files and running packs, not all desired changes. An error is fatal even when approval/some scopes already changed. |

- `startLoopholesMatching` refuses placement before `writeScopeFiles`; scope writes can fail
  and skip a start. `BrokeredToStart` alone overstates `running`.
- The container keeper means the design snapshot's apparent three local refresh callsites
  do not provide a common actual-start result. Do not fabricate a successful-start boolean.
- `LiveFilesContext` silently skips unreadable/malformed entries and filters by launcher and
  broker PID liveness. An empty result does not distinguish absent from unreadable scope.
- `ScopeRepo.Sources` are terminal-escaped display labels. Never recover `origin` by parsing
  `remote "origin"`. Raw names exist earlier in `ScopeSource.Read.Remotes` only.
- `ReadAllRemotes` keeps multiple `url`/`pushurl` values per name; the accepted ctx uses one
  identity per remote name. C1 must close that carrier projection before access rendering.
- The git broker's `coverFor` also reads current user `BrokeredGrantKinds`, while scope
  files carry approved grants. Listing a revoked/disallowed kind as effective would be false.
- The current anonymous HTTPS fetch route can succeed without a read grant or approved URL
  (`gitbroker.Broker.Run` and `serveHTTPS`). Do not infer `read=true` from public connectivity:
  these are approved access facts about lending host authority, not a promise that every
  ungranted public network operation fails.
- `Store.Working` and live scope discovery use the same source directory lock, each internally.
  Separate calls can observe different instants; choose the single snapshot contract below,
  not nested locks or an unbounded goroutine pretending to impose a deadline.
- `WriteBriefing` preserves an existing bind's inode (and breaks hardlinks when necessary).
  Renaming staged temporary files over live briefings makes the running jail keep old bytes.
- `refreshJailBriefingsMode` currently prepares skills and writes destination-by-destination;
  a later producer failure must be found before that write loop. Do not reuse its generated
  mechanics warning-and-keep-prior behavior for computed prose.
- `RenderHostBriefings` currently preserves per-destination broken-link refusals and may write
  other destinations. Keep that existing I/O posture distinct from an all-batch producer failure.
- Host composition does not currently retain one destination agent identity on its result.
  Resolve the identity from declared destinations before deduplication/evaluation; do not use
  each borrowing pack's identity or run a producer twice for aliases of the same path.

## Proposed contracts and unresolved seams

These are implementation-level prerequisites, not new owner product questions. Only their
handoff owner may settle/export the exact signatures. No API below exists on the stamped base; their absence is the feature gap, not an
admission gate requiring another implementation to happen first.

### C1 — approved remote and grant carrier

**Proposed engineering contract, owned by C:** retain the raw association as a list until the
singleton projection is ruled; do not make a display string the wire value. New types in the
core carrier seam (names proposed, no existing API claimed):

```go
// New brokerscope.ApprovedRemote owns the optional scope-file wire member.
// Core carrier imports/aliases it; brokerscope never imports run/config.
type ApprovedRemote = brokerscope.ApprovedRemote
type ApprovedAccessScope struct {
    Source, Label, Form, LaunchID string
    Repositories []string
    Remotes []ApprovedRemote
    Grants []brokerscope.Grant
    AllowedKinds []string
}
```

C extends `recordApprovedScopes` to carry this typed projection from `ScopeSource.Read.Remotes`;
the keeper plan carries the same value by source, alongside legacy approved maps during the
transition. Add optional `approved_remotes` to the version-1 scope file without changing broker
authority fields. New readers accept version 1 with or without it; older JSON readers ignore
this additive member. Validate its identities against that file's approved scope, not current
workspace config. Missing metadata reads as an empty remote mapping, never a new host read;
retain all concrete repositories/grants that can be normalized from the old file. The singleton
projection alone waits on [the recorded mismatch](#OQ-CBP1).

**New, proposed:** an error-bearing core carrier of source → approved scope form, concrete
repository entries, raw remote-name/identity associations, grants, effective allowed kinds,
and launch identity. Keep this secret-free carrier separate from the Lua context and the
mechanics fact sheet, which contains host paths. Source names are `brokered.source`; the
existing approval maps are keyed by loophole label.

The config/carrier owner must export the approved raw remotes from the gate's own read,
including how multiple URL/push-URL identities for one remote name project to the design's
single `remotes.<name>` field. Persist the approved association for attach/live apply; do not
reread mutable workspace remotes on attach. Supply effective kind eligibility from the same
core decision the broker applies, without exposing config to the producer. This is missing
from the current `ScopeRepo` and `brokerscope.File` contracts.

Backward-read handoff must state where this additive metadata lives and how an older applied
record without it reads. An absent mapping cannot be replaced by current requested config or
a host git probe. Scope `File.Version` currently rejects every different version; changing it
requires explicit supported reader versions, not an incidental bump. Preserve legacy `Widened`
read semantics in the broker; do not turn legacy widening into new producer-granted authority.

### C2 — actual broker start result across the keeper

**Proposed engineering contract, owned by D after C's carrier:** final container composition
runs inside `keeper.run`, after `startPlannedLoopholes` and its refusal check, before
`startJailMain`. Current `keeper.go:421–470` has this exact pre-container interval and already
holds the transferred launch lock; `unwindUnstarted(1)` unwinds owned handles without starting
a container. No new fact-return frame is necessary: the keeper publishes the final batch
itself and relays diagnostics through existing stderr frames.

Parent preparation remains responsible for skills/mount-source directory preparation and
secret-free applied body inputs, but must not evaluate/publish computed briefing text there.
Add a typed briefing request to `keeperPlan` (deduplicated destinations, applied briefing
input/extra/provenance, approved access carrier); the staged pack tree is already transferred.
Keeper loads producers only from that tree, constructs disposition from actual handles and
failed placement/scope/start results, evaluates once, publishes before container creation, and
consumes handoff only after publication. Keep `keeperPlan.Build` matching and existing plan
check; new optional request absent means the legacy static path, not invented producer facts.
New feature requests are sent only to the same-build keeper. Sealed builds still receive no
user prose or access facts. macos-user uses its existing per-session refresh interval, not the
shared keeper's machine-wide `holdKey` composition.

```go
type BriefingBrokerDisposition struct { Running bool; WhyNot string }
// New projection, not the configured candidate set.
type BriefingBrokerStartResult struct { Source, WhyNot string }
func briefingBrokerDispositions(scopes []ApprovedAccessScope, handles []loopholeDaemon, results []BriefingBrokerStartResult) map[string]BriefingBrokerDisposition
```

**New, proposed:** a per-source disposition containing `running` and `why_not`, produced from
placement, scope-file success, and successful service start, with no credential details.
The macos-user arm can project actual returned handles. The fresh container parent has no
such result at its current refresh point; the keeper starts services later.

The pre-agent-delivery handoff is the proposed keeper-owned publication interval above;
implement it together with the parent preparation split and teardown caller tests. Do not equate the
keeper plan, the enabled set, or written scope files with a running broker. This contract is
required even if no configured grant changed. Apple Container and host are false dispositions
with their design reasons; this feature must not change their service eligibility.

### C3 — bounded snapshot and diagnostic reads

**Proposed engineering contract, owned by C:** export one narrow snapshot in `brokerstore`,
with a specific `brokerscope` lock-held scope reader, rather than compose under a generic
callback. This dependency is `brokerstore` → `brokerscope`, not a cycle: the scope package
must not import the store. These are proposed new signatures:

```go
// brokerstore
type AccessSnapshot struct {
    Files []brokerscope.LiveFile
    Working []WorkingEntry
    ScopeReadError error
}
func ReadAccessSnapshot(ctx context.Context, source, container, workspace string) (AccessSnapshot, error)
// brokerscope: caller must hold the source transaction; no nested acquisition.
func ReadLiveScopesLocked(source, container, workspace string) ([]LiveFile, error)
```

The outer error is working-store/transaction/discovery failure; `ScopeReadError` describes a
matching live scope that could not be decoded/read. One bounded source transaction reads both
parts; filter working entries by `Live`, workspace, approved identity, `WorkingBranch` and
`refs/heads/`, then sort names with declared grants taking `why=declared` precedence for an
overlap. Existing `Store.Working` stays unchanged; no scope/store lock is recursively acquired.
Snapshot values are copied before releasing the lock. Attach's scope-read exception projects
false with the accepted reason; other read errors wait on [the explicit failure ruling](#OQ-CBP2).

**New, proposed:** `brokerstore.ReadAccessSnapshot` is the context-bounded access snapshot that
reads matching live scopes, read diagnostics, and live working entries under one source
transaction. Do not export generic arbitrary locked callbacks to Lua or compose Lua while a
source lock is held. Internally expose lock-held readers only to the core snapshot owner,
then release the source lock before producer evaluation.

Lock order: workspace launch lock → one source transaction at a time; sort source names;
release each source lock before any Lua or briefing write. Broker request paths must not
start taking workspace launch locks. Working-set mutations do not trigger composition.
The existing source locks already serialize broker decisions and scope replacement.

Attach read failure produces the design's `running=false`, `why_not="its scope could not be
read"`, and attach may continue **if the producer succeeds**. Unknown-source, producer, and
read-declaration failures remain fatal. The API must distinguish unreadable matching scope
from no live broker; `LiveFilesContext` alone cannot do that today. Snapshot/store error
projection outside this specified attach case is conditional on [OQ-CBP2](#OQ-CBP2);
do not silently treat unknown working-set data as complete.

### C4 — error-bearing batch composition

**Proposed engineering contract, owned by B/D:**

```go
// luahook: separate from DeriveCtx.
type BriefingCtx struct { Notch, Agent string; Access map[string]BriefingAccess }
type BriefingSession interface {
    Registrations() []string
    Eval(name string, ctx BriefingCtx) (string, error)
    Close()
}
// packload: bindings retain source key, governor, audience and virtual Rel.
func NewBriefingSession(p *Pack) (luahook.BriefingSession, error)
// entrypoint: the same prepared destinations feed reporting and publication.
func ComposeHostBriefingBatch(packs []*packload.Pack, home string, req HostBriefingRequest) ([]HostBriefingDestination, error)
```

`BriefingAccess` and its repository/branch records mirror exactly the accepted access-table
fields, not generic config maps. B owns the context builder and source guard; D owns the
batch/render caller migration. Keep the old static-only convenience wrapper only for callers
that provably cannot receive producers; every production producer-capable caller uses the
error-bearing batch. Migrate `RenderHostBriefings` and CLI reporting together so they share one
result. A declaration without a script warns only on `os.ErrNotExist`; other script read errors
are attributed composition errors, not disguised missing registrations.

**New, proposed:** `luahook.BriefingCtx`, a briefing registration listing, and an evaluator
session with `Eval(name, ctx) (string, error)` / `Close()`. Context has only `notch`, `agent`,
and declared `access`; no `DeriveCtx` embedding. Each call gets fresh input tables. One pack
session per composition retains the existing five-second budget, including registration;
a producer cannot reset that budget per destination. Reject all non-string values and
strings over 65,536 bytes; empty string is valid. Add an access-table missing-key guard that
raises for undeclared source reads, while additive absent fields within a declared source
remain Lua `nil`. Preserve the existing sandbox and version-boundary behavior.

**New, proposed:** an error-bearing composition batch API at `packload` / `entrypoint`, shared
by jail and host projection, plus a separate publication phase. `ComposeHostBriefingsFor`
and its compatibility wrapper currently return slices only; migrating their callers must
not swallow errors to preserve an old signature. Reporting and rendering consume the same
batch, not a second evaluation of potentially stateful Lua.

Evaluate every selected pack's registration pairing, even if no destination accepts its
prose: undeclared registrations are refused. Invoke a paired producer once per deduplicated,
audience-admitted destination; missing declared registration warns and contributes nothing.
Use virtual `briefing/<name>.md` in byte-wise order and refuse a real file collision even
when that real file's content is empty. Producer lines refuse `from`/`into`; `agents` is the
selector. A virtual source must preserve its own governor and declaration location.

### C5 — known-source universe distinct from selection

**Proposed engineering contract, owned by C:** a read-only declaration catalog distinct from
activation, with source name, owning pack reference/name and broker declaration. Populate it
from shipped manifest declarations and already-resolved offline configured/process pack
declarations, honoring local filters and the existing trust/resolver rules. Read no unapproved
arbitrary host path and perform no fetch. Loading a declaration does not reserve a pack name,
stage a runtime pack, grant host execution, or select it. Duplicate source owners are a catalog
error with both declaration locations; the selected universe is still computed separately.

```go
type BriefingAccessSource struct { Source, Pack string; Brokered loopholedecl.Brokered }
func BriefingAccessSources(packs []*packload.Pack) (map[string]BriefingAccessSource, error)
```

The implementation receives the approved offline declaration set explicitly; it must not use
`Resolver.Known` as an all-source catalog. Known but unselected entries become optional false
facts or the required-source refusal; unknown names get the supersession-style suggestion.
This catalog is new feature engineering, not a prior registry that must land before C starts.

The design's unknown-versus-unselected distinction cannot be answered by the current selected
set alone. `resolveConfiguredPacks` in the run pack reader computes the configured selection
closure; `loopholes.Discover` reads those recorded `PackModules`, and
[`Resolver.Known`](../../internal/loopholes/resolver.go) explicitly sees only selected packs.
`unmatchedSupersessions` compares declarations in that set, including disabled loopholes;
it does not export an all-unselected broker-source registry.

**New, proposed:** an error-bearing core source-declaration lookup, separate from the running
selection. The discovery owner must name its offline, already-approved declaration inputs
and how a known unselected source resolves to the pack named in `why_not`. Reuse supersession's
suggestion wording/algorithm, not an imagined broader current universe. Do not fetch packs,
probe logins, read arbitrary host trees, or activate an unselected pack to answer a read.
This missing source/API contract belongs in the integration handoff, not the owner-ruling list.

### Publication and live-apply transaction boundary

The design requires no **producer-failed** batch to publish any computed text. Therefore:

1. Capture approved/applied facts, resolve destination identities, validate pairing/reads,
   and evaluate the entire batch before the first briefing write or host ownership record.
2. On producer failure, return a file/line diagnostic and `yolo pack lint` repair step.
   Keep preexisting files untouched, but never launch/attach an agent using them as fallback.
   Do not consume a handoff or claim refreshed guidance. An already-running jail stays running.
3. Publish a successful batch using existing inode-preserving jail writes and host adoption/
   ownership rules. Do not invent multi-file atomic rename semantics incompatible with binds.
4. For live apply, read the scope **actually replaced**, compose after the scope mutations,
   and publish only after all producers pass, under the already-held workspace launch lock.
   Preserve completed broker mutations and their truthful result report if composition fails;
   do not silently roll back approved authority or report total success.

`runApplySteps` currently folds partial step outcomes, and replacing several scopes is not
one transaction. E needs an error-bearing refresh outcome separate from `outcomeNextRestart`:
a producer failure is fatal to the invoking operation, not a restart-pending success. The
**Proposed engineering contract, owned by E:** retain `[]applyChange` and folded state, but
return a separate `BriefingErr` from the sequence. CLI reports actual mutations, then the
producer diagnostic, and returns failure (1), never restart-pending (3). Change `applyAtAttach`
to return the briefing error; `attachExisting` aborts before session exec only on that error,
preserving existing decline/pending behavior. The newer accepted producer-failure ruling
supersedes the old void-path assumption that nothing here can fail attach. No approval or
completed mutation is rolled back. Proposed signatures:

```go
type applyExecution struct { Changes []applyChange; BriefingErr error }
func (o *Options) runApplySteps(run *applyRun, plan []applyChange) applyExecution
func (o *Options) applyAtAttach(cname, rt string, cfg *jsonx.OrderedMap, envLines []string) error
```

Use running-tree and authenticated mechanics readers for the refresh request. Applied scope
metadata/snapshot decides access even when another apply step failed; E implements these new
APIs rather than waiting for another lane to implement its own component.

## Pending observable access-projection decisions

These two gaps were found in current-source planning. They do not reopen the three settled
computed-briefing rulings; the accepted design is outside this writer's fence. Root must route
the chosen behavior into that authority before dispatching affected implementation.

1. 💬 **OQ-CBP1: Which repository identity does one remote name expose when it has several URLs?**

   <!-- vantage: question id=OQ-CBP1 -->

   `ReadAllRemotes` retains multiple `url` and `pushurl` values for one name, while the
   accepted `ctx.access.git.remotes[name]` is one identity. Raw approved associations will be
   retained; display labels will not be parsed. Safe alternatives are omitting an ambiguous
   name, or exposing one identity only when all approved URLs normalize to the same identity.
   Selecting a distinct fetch or push URL silently would prescribe a new finishing behavior.

   **Answer:** Pending; no singleton projection is claimed settled here.

2. 💬 **OQ-CBP2: What happens when access snapshot/store reads fail outside the attach scope-file exception?**

   <!-- vantage: question id=OQ-CBP2 leaning="Fail the invoking composition on snapshot/store-read errors outside the accepted attach scope-file exception; do not publish incomplete facts." -->

   Attach has an explicit unreadable-scope false-source exception. The design does not specify
   other scope-discovery or working-store failures; hiding them as empty branches could deliver
   incomplete finishing instructions.

   _Leaning:_ Fail the invoking composition on snapshot/store-read errors outside the accepted
   attach scope-file exception; do not publish incomplete facts.

   **Answer:** Pending; this proposal is not an owner ruling.

## Permanent caller-red controls

These are proposed permanent test sources for the implementation package. The **prior planner**
observed the three printed bodies as temporary baseline probes, then removed its temporary
files; the **current continuation ran no software tests**. Declaration probe failed on unknown
`derive`; jail caller probe failed on absent
`COMPUTED-jail-pi`; host render probe failed on missing producer error. No production mutation
control or green implementation is claimed. The first two controls use current caller/fixture APIs;
fixture JSON unmarshaling intentionally bypasses declaration validation so the caller test
cannot pass merely because the decoder refuses the new field. Unit A separately tests the
decoder. Imports are `encoding/json`, `os`, `path/filepath`, `strings`, `testing`, and the
existing fixture package imports shown by each body.

### Declaration control — Unit A

New test in package `packdecl`; full test body:

```go
func TestDecodeAcceptsDeclaredBriefingProducer(t *testing.T) {
    _, problems := Decode([]byte(`{"name":"house","contributes":[{"kind":"briefing","derive":"finishing","reads":[{"access":"git","optional":true}],"agents":["pi"]}]}`))
    if len(problems) != 0 {
        t.Fatalf("declared producer refused: %v", problems)
    }
}
```

```bash
go test -short ./internal/packdecl -run '^TestDecodeAcceptsDeclaredBriefingProducer$' -count=1
```

Expected baseline red: strict decode reports unknown `derive` (or `reads`), not a compile
failure. Extend with JSON and grouped TOML parity; invalid slug; `reads` on nonproducer;
`from`/`into` on producer; duplicate source/read/name; missing access name; optional type;
older tolerant reader skips the entire unknown producer contribution with a warning rather
than interpreting it as remainder governance. Permanent production callers are `Decode`,
`DecodeTOML`, and tolerant decode, not only `validateContribution`.

### Jail output control — Unit D

New test in package `run`; full test body using existing fixtures:

```go
func TestRefreshJailBriefingsInvokesDeclaredProducer(t *testing.T) {
    var c packdecl.Contribution
    if err := json.Unmarshal([]byte(`{"kind":"briefing","derive":"finishing","agents":["pi"]}`), &c); err != nil {
        t.Fatal(err)
    }
    p := jailPack(t, "house", map[string]string{
        "derive.lua": `yolo.briefing("finishing", function(ctx) return "COMPUTED-" .. ctx.notch .. "-" .. ctx.agent end)`,
    }, c)
    got := jailBriefings(t, []*packload.Pack{
        jailDest(t, "pi", ".pi/agent/AGENTS.md", "pi"),
        jailDest(t, "claude", ".claude/CLAUDE.md", "claude"), p,
    }, nil)
    if !strings.Contains(got[".pi/agent/AGENTS.md"], "COMPUTED-jail-pi") {
        t.Fatal("refresh caller omitted the computed pi prose")
    }
    if strings.Contains(got[".claude/CLAUDE.md"], "COMPUTED-") {
        t.Fatal("producer escaped its audience")
    }
}
```

```bash
go test -short ./internal/cli/run -run '^TestRefreshJailBriefingsInvokesDeclaredProducer$' -count=1
```

Expected baseline red: no `COMPUTED-jail-pi` text. After implementation, deleting the
production refresh→producer call must make this control red; the fixture must not precompute
`staged.briefings`. Preserve it when old static-staging helpers are adapted.

### Fatal host caller control — Unit D

New test in package `run` alongside the parity fixture; full test body:

```go
func TestRenderHostBriefingsRejectsProducerBeforeAnyWrite(t *testing.T) {
    var c packdecl.Contribution
    if err := json.Unmarshal([]byte(`{"kind":"briefing","derive":"finishing"}`), &c); err != nil {
        t.Fatal(err)
    }
    p := jailPack(t, "house", map[string]string{
        "derive.lua": `yolo.briefing("finishing", function(ctx) if ctx.agent == "pi" then error("producer-broken") end return "NEW" end)`,
        "briefing/a.md": "Static prose.",
    }, c)
    packs, _ := packload.ResolveDestinations([]*packload.Pack{
        jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
        jailDest(t, "pi", ".pi/agent/AGENTS.md", "pi"), p,
    })
    home := t.TempDir()
    for _, rel := range []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"} {
        path := filepath.Join(home, rel)
        if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }
        if err := os.WriteFile(path, []byte("BEFORE"), 0644); err != nil { t.Fatal(err) }
    }
    _, err := entrypoint.RenderHostBriefings(packs, home, entrypoint.HostBriefingRequest{}, false)
    if err == nil || !strings.Contains(err.Error(), "producer-broken") {
        t.Fatalf("render did not fail on producer: %v", err)
    }
    for _, rel := range []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"} {
        data, err := os.ReadFile(filepath.Join(home, rel))
        if err != nil || string(data) != "BEFORE" { t.Fatalf("published failed batch at %s: %q, %v", rel, data, err) }
    }
}
```

```bash
go test -short ./internal/cli/run -run '^TestRenderHostBriefingsRejectsProducerBeforeAnyWrite$' -count=1
```

Expected baseline red: render returns no producer error. Retain the later-destination error:
an implementation that publishes claude before evaluating pi must fail the unchanged-byte
assertion. Repeat at the jail caller with preexisting staging files, including an inode check.
The operation-level tests below must additionally pin CLI failure; an error at a callee alone
is insufficient.

## Operation/caller regression recipes

**Evidence boundary:** these are complete test *recipes*, in pseudocode, not compilable Go or
executed tests. Names under `Test...` are proposed permanent tests. Helper operations in the
fences describe fixture construction/assertions to implement, not landed APIs. On the current
base the strict decoder fails first; that is A's RED, not evidence for Run/keeper/apply abort.
After A/B/C make fixtures admissible, install D/E's tests before wiring their callers and record
the specific behavioral RED below. A missing symbol/compile error is not that RED. Later,
delete only the named **production** call in a disposable authorized implementation tree,
rerun the named test, capture the failure, and restore byte-exact source. Never remove the
callee, producer declaration, test assertion, or fixture injection to obtain a red result.

### Frozen operation inputs and per-file deliverables

D adds the following **proposed** request to the existing
[`keeperPlan`](../../internal/cli/run/keeperplan.go); no user prose goes to a sealed plan.
`FactSheet` is core-only and may contain host paths; it is never embedded in Lua `BriefingCtx`.
Destinations carry their declared identity rather than a contributing pack's name.

```go
// New in run/briefingaccess.go; serialized by keeperplan.go.
type JailBriefingDestination struct { Into, Agent string }
type JailBriefingRequest struct {
    Destinations []JailBriefingDestination
    Base, Extra string
    Provenance bool
    IOPriority ioprio.Priority
    Applied *jailcontent.FactSheet
    Approved []ApprovedAccessScope
}
// Proposed field on keeperPlan, nil only for static legacy requests or sealed builds:
// Briefing *JailBriefingRequest `json:"briefing,omitempty"`
// New D APIs in prepare.go (or a D-owned new composition file):
type JailBriefingOutput struct { Into, Agent, Text string }
func (o *Options) composeJailBriefingBatch(req JailBriefingRequest, staged stagedPacks,
    access map[string]luahook.BriefingAccess) ([]JailBriefingOutput, error)
func (o *Options) publishJailBriefingBatch(cname string, req JailBriefingRequest,
    batch []JailBriefingOutput) (string, error)
// New C wire/replacement API in brokerscope/file.go and live.go:
// type ApprovedRemote struct { Name, Identity string }
func ReplaceAccessScopeContext(ctx context.Context, path string, before File,
    repos []string, grants []Grant, remotes []ApprovedRemote) error
```

- **Parent D**, [fresh preparation](../../internal/cli/run/run.go#L2274-L2314) and
  [refresh](../../internal/cli/run/prepare.go#L409-L439): split skills/directory/mount-anchor
  preparation from composition/publication. Freeze applied base/extra/provenance/identity and
  gate-approved carrier into the request; no producer evaluation, destination overwrite or
  handoff consumption at the old parent refresh point. Preserve static-only requests and
  `WriteBriefing` inode behavior. `stagePacks` only stages source, never computed text.
- **Keeper D**, [service-start interval](../../internal/cli/run/keeper.go#L421-L477): after
  `startPlannedLoopholes` and refusal handling, and only in the container arm, load the
  transferred `PackTree`, project actual successful handles, compose all destinations, publish,
  then materialize Apple Container delivery before `startJailMain`. Propagate failure through
  existing stderr/exit frames and `unwindUnstarted(1)`; do not move the native `holdKey` arm into
  container composition. Retain `endedBeforeTheContainer` and service-watch semantics.
- **C disposition**, [start loop](../../internal/cli/run/loopholesruntime.go#L420-L598) and
  [scope write](../../internal/cli/run/brokeredscope.go#L84-L143): carry per-source refusal
  reasons from actual placement/scope/start results alongside handles. Extend C2's proposed
  projector with these results; `handles` alone gives absence, not its reason. Successful handle
  + matching approved source + successful scope write is running; candidate/declaration alone
  is not. A later live snapshot uses scope identity and broker liveness, not yesterday's handle.
- **Host D**, [CLI kind](../../internal/cli/applyhostbriefings.go#L260-L289) and
  [outer caller](../../internal/cli/apply.go#L941-L945): make `ComposeHostBriefingBatch` return
  an error; add proposed `RenderHostBriefingBatch(batch []HostBriefingDestination, home string,
  req HostBriefingRequest, observe bool) ([]HostRenderResult, error)`. Report/render share the
  batch. Composition error stops this briefing kind before render/prune/ownership-save and
  contributes rc 1 to `applyHostSurveyed`. Preserve prior adoption/migration and unrelated kind
  outcomes; this is not a whole-host-apply rollback. A migration that changes inputs requires a
  new batch of those inputs, not a second evaluation merely for reporting.
- **Attach D**, [actual caller](../../internal/cli/run/run.go#L3130-L3169): both
  `refreshJailBriefings` and `refreshLegacyJailBriefings` use running-tree producers and the same
  live access projection. Authenticated applied mechanics decides body/extra when available;
  its legacy absence must not suppress producers. Producer failure propagates before exec,
  discards only this entry's unused tree, releases locks and does not stop the live jail.
- **E per-file result**, [scope loop](../../internal/cli/run/liveapply.go#L502-L539): a single
  `changeScope` can replace file 1 and fail file 2. Track each successful replacement at the
  loop, not only a successful whole step. Proposed `applyStep` returns `(scopeFilesChanged bool,
  err error)`; other step kinds return false. C's additive `ReplaceAccessScopeContext` writes approved
  raw remotes atomically with repos/grants, preserving launch identity and existing replacement
  validation; E carries the gate's raw association through its replacement seam. The existing
  `ReplaceScopeContext` remains for old callers, never used to silently discard new metadata.
  `runApplySteps` ORs this bit, folds actual outcomes
  and updates mechanics, then refreshes once if any scope file changed, even when the scope
  step is pending. Read back applied files, never desired grants for failed files. Extend
  proposed `applyExecution` with `ScopeFilesChanged bool`; its `BriefingErr` stays separate.
- **E error propagation**, [liveApply](../../internal/cli/run/liveapply.go#L269-L295) and
  [applyAtAttach](../../internal/cli/run/liveapply.go#L588-L608): proposed internal
  `liveApplyExecution(cfg, rt, cname, envLines, mode) (rc int, briefingErr error)` shares the
  existing gate/result reporting. Keep `liveApply(...) int` as the command wrapper; `configApply`
  receives rc 1 on `BriefingErr`, overriding pending rc 3. `applyAtAttach(...) error` returns only
  that typed error; decline, moved-config, no-TTY notice and pending-only behavior still continue
  attach. `attachExisting` checks the error before its ordinary refresh/exec. No second failed
  composition or fallback refresh, no rollback of approvals or already-replaced files.

These are C/D/E's own new calls to build, not external global admission gates. Serialize
`run.go` ownership D → E; E owns attach-apply error hunks after D's refresh changes. The exact
internal helper factoring is cheap within those fences; the input provenance, result and
publication/error boundaries above are not optional. Native macos-user may join a keeper that
already holds services: its returned roster's declared service list is not proof of a live
broker. C/D project that session's approved scope and verified live broker scope/endpoint;
keeperless sessions project their own returned handles. Do not claim all native launches start
local handles at the current `startLoopholesDisclosed` site.

### Shared fixture protocol

**Proposed new test fixture, owned by D:** adapt `packHome`, `writeUserPacks`, `dispatchOptions`
in [dispatch tests](../../internal/cli/run/packstagedispatch_test.go#L40-L99),
`inProcessKeeper` / `adoptLaunchSeams` in [keeper tests](../../internal/cli/run/keeper_test.go#L42-L124),
and `fakeJail.mainArgv` / `exec` there. The fixture installs only a temporary selected `house`
pack and destination-only `pi`/`claude` packs, no agent program surface. Resolve fixture paths where
minted. Permit no real podman: put only a test shell runtime on PATH, recording `run` and
`exec`, and emit `BootReadyLine`/hold only for the former. Auto-load returns the fake image;
no nix build, agent invocation, credentials or API. Record calls from keeper and parent in a
synchronized slice; existing `Options.Exec` does not intercept `startJailMain` or final session
exec, so shell marker files are required. Adapt fixtures to preserve rc instead of discarding it.

For actual-start positives, add a brokered test module to the selected `house` staged tree,
with `source="gbsrc"`, approved read and `branch main` on `github.com/o/r`, and command using
this test binary's existing `-front-upstream-child line {socket}` helper, not `/bin/cp`.
Copy the no-credentials endpoint protocol from
[`perJailFixture`](../../internal/cli/run/hostdaemonperjail_test.go#L203-L233), but set broker
activation through approved selected-pack configuration, not `default_enabled`. Host-exec
approval must be explicit; module and command must be outside the workspace/agent-writable
placement boundary. The fixture's `.git/config` has exactly one approved URL:

```ini
[remote "origin"]
    url = git@github.com:o/r.git
```

The read is core's gate read, not a git subprocess. The broker helper serves `ping` → `pong`;
probe its endpoint once to establish actual successful startup, and ensure cleanup ends it.
Do not assert authority enforcement from this no-op protocol. The
[`/bin/cp` scope-transfer fixture](../../internal/cli/run/brokeredscope_test.go#L358-L402)
exits without serving and **must not** be used to assert `running=true`.

All tests below seed two destination files with `BEFORE-pi` and `BEFORE-claude`, store their
bytes and inode identities, and seed `.yolo/handover.md` with `HANDOFF`. Fatal tests assert
unchanged destinations, no `.consumed` pointer and no successful refresh report. An admitted
empty producer is not failure. The success pair must prove composition reached the caller:
`Run==0` with only old bytes is a failure, not a passing smoke test. No test calls the proposed
composer directly in place of its named operation.

### Fresh Run and keeper final-facts controls — D

Permanent file: new `internal/cli/run/computedbriefing_test.go`; use the shared fixture above.
This producer has no ambiguous URL projection or failing snapshot, so neither pending owner
choice changes its assertions. The fake service is enabled, selected and actually started.

```text
TestComputedBriefingRunPublishesAfterActualStart:
  setup shared fresh-Run fixture; approve read+branch-main only; accept gate via AcceptConfigChanges.
  declare finishing with reads [{access:"gbsrc"}], agents ["pi"].
  Lua: yolo.briefing("finishing", function(ctx)
         local s = ctx.access.gbsrc
         if not s.running then error("not-actually-started") end
         if s.remotes.origin ~= "github.com/o/r" then error("not-approved-origin") end
         if not s.repos["github.com/o/r"].read then error("read-not-applied") end
         return "FINAL-jail-pi-main"
       end)
  run Run(*o) with Args ["true"] through inProcessKeeper (never real runtime).
  at keeper spawn read plan: Briefing != nil, approved origin correct, transferred tree correct;
    BOTH files still BEFORE, HANDOFF not consumed (parent did not publish).
  fake runtime run entry reads pi staging: contains FINAL-jail-pi-main and HANDOFF;
    claude has no FINAL; endpoint ping/pong already succeeded; note event "main".
  fake runtime exec entry reads same pi bytes; note event "session"; exit 0.
  assert Run rc == 0, main marker once, session marker once, publication before main,
    no producer failure and HANDOFF consumed only after successful publication.
  failure literal: "Run delivered before keeper composed actual approved/start facts".
  deletion control: remove keeper.run -> final composition/publication call, leaving
    parent preparation and service start intact; expected failure at main marker byte check.
  second control: remove runContainer -> request population; same byte failure (not nil panic).
```

The shell runtime records marker/read failure explicitly and exits 91 on absent `FINAL`;
the test's outer assertion prints the literal above plus captured bytes and rc. Do not let a
shell read failure silently produce an empty record. Producer invocation count may be recorded
by a test observer around the real session evaluation (new test-only seam, D's fence); never
use that observer to synthesize output or grant running status.

```text
TestComputedBriefingRunFailureUnwindsBeforeMain:
  identical successful-service setup; change Lua finishing body to error("producer-broken").
  execute Run(*o), wait for keeper exit and helper cleanup (bounded 30s fixture wait).
  assert rc == 1; stderr includes house/derive.lua, producer-broken, line attribution,
    and "yolo pack lint"; no main marker, no session marker, endpoint no longer reachable;
    service helper PID gone, own scope file removed; unrelated scope/tree untouched.
  assert prior bytes/inodes, HANDOFF retained, lock reacquirable and own keeper record gone.
  failure literal: "fresh producer failure reached startJailMain or escaped unwindUnstarted".
  deletion control: remove keeper fatal-error -> unwindUnstarted return (keep evaluation);
    expected main/session marker or rc mismatch with that literal.
  remove keeper composition call separately: rc/no-diagnostic assertions must also fail.
```

Keeper-only `TestComputedBriefingKeeperFinalPublicationAndUnwind` in new
`internal/cli/run/keeperbriefing_test.go` isolates the exact interval:
adapt `startKeeperFixtureWith`, tune `Config`, `PackTree`, `Packs`, `Services`, `ApprovedScopes`,
`ApprovedGrants` and new `Briefing` before `runKeeper`, enable only the helper capability,
and set `RunCmd` to a marker-writing fake shell hold. `relay()==false`, `wait()==1`, no main
marker and no endpoint after failure pin the unwinder; positive `relay()==true` requires the
marker-time `FINAL` read before closing `lifeW` to terminate the held fake jail. This complements,
not replaces, the `Run` request-transfer test. Include absent-request static compatibility and
sealed-plan cases: no producer/user prose/access handed to the sealed keeper. Placement refusal,
blocked scope write (fixture directory obstruction), and helper startup failure each produce a
false source with a nonempty reason, never `FINAL`; preserve any existing startup refusal that
aborts before composition rather than pretending those cases deliver a successful launch.

### Attach: running staged context, fresh scoped facts — D

Permanent file: new `internal/cli/run/computedbriefing_test.go`. Adapt
[`attachStagingFixture`](../../internal/cli/run/concurrentstaging_test.go#L272-L328) **before**
it stages the tree, adding the producer declaration/script to `treepack`. Extend
`attachThroughRun` into a fixture returning `(rc, execMarker, runtimeCalls, staging)` without
requiring success; its current helper discards rc and requires exec, so cannot test abort.
Freeze current contract tags; seed authenticated mechanics for one row, omit it for the legacy
row. Keep pack-tree load/contract gates valid so an unrelated skew refusal cannot pass the test.

```text
TestComputedBriefingAttachUsesRunningTreeAndFreshScope:
  for mechanics in [authenticated, legacy]:
    stage treepack producer that finds branch main by name and returns "BOOTED-main" plus
      sorted names/reasons from ctx.access.gbsrc.repos["github.com/o/r"].push_branches.
    persist live tree via writeLivePackTree; save snapshotTree(tree) and prior pi inode.
    live scope matches workspace, cname, launch ID, launcher/broker liveness;
      approved origin o/r, read+branch main; current live kind eligibility includes branch.
    configured local treepack now returns "REQUESTED"; requested config adds branch forbidden;
      mutable workspace remote now o/other (do not approve it).
    add live WorkingBranch refs/heads/agent/fresh for same workspace/repo to store;
      also ended, wrong-workspace and wrong-repo entries, which must not appear.
    execute Run(*o) attach with matching contracts, no-TTY notice/no apply mutation.
    assert rc==0, one session exec, no runtime run/stop/rm, live tree byte-identical,
      only unused entry tree discarded, pi inode unchanged; prose starts BOOTED-main,
      contains agent/fresh created reason, not REQUESTED/forbidden/o/other/filtered branches.
    update working entry state then second attach: next invocation reflects the change.
    failure literal: "attach composed requested staging or stale/unscoped access facts".
    deletion controls: remove EACH normal/legacy refresh call in turn; corresponding row
      must fail the new-byte assertion; keep evaluator and runningJailPackView intact.
    replace running-tree argument with entry staged argument separately: REQUESTED assertion fails.
```

Use a deterministic Lua producer printing sorted branch names/reasons in addition to the
prefix (do not depend on arbitrary map order); exact positive branch text is
`agent/fresh:created,main:declared` with lexical name sorting;
assert those tokens and no forbidden token. A branch-main first assumption must not be made
on a lexically sorted list; select `main` by name in the prefix producer.

```text
TestComputedBriefingAttachFailureLeavesLiveJail:
  both mechanics rows; running-tree producer errors("producer-broken"); requested producer succeeds.
  execute Run attach; assert rc==1, diagnostics script/line/lint, no session marker,
    no stop/rm/run, runtime still answers abc123, running tree identical;
    prior bytes/inodes/HANDOFF retained, release invoked and session lock released.
  failure literal: "attach delivered fallback or stopped the already-running jail".
  deletion control: remove refreshErr fatal return at attachExisting, leaving refresh;
    expect session marker or rc mismatch, not a helper-only evaluator failure.
  independent row: unreadable matching live scope -> running=false,
    why_not exactly "its scope could not be read"; successful producer returns SCOPE-UNREADABLE,
    attach rc==0 and session reads that text. Erroring producer in same row still aborts.
```

### Host command/render propagation — D

Permanent file: new `internal/cli/packbriefingapply_test.go`, package `cli`. Reuse
[`applyWith`](../../internal/cli/applyhostprune_test.go#L64-L69), `selectPacks`, `writeFile`,
`treeHashes`, `countLines` in [host briefing tests](../../internal/cli/applyhostbriefings_test.go).
Select destination-only packs, not installed program surfaces; HOME/XDG point into test homes.
First run a static-only successful apply to create the real briefing ownership record and
prior bytes; then modify just the temporary house manifest/script to declare a producer.
This avoids an adoption decline masquerading as producer failure. Snapshot destinations,
manifest and archive listing immediately before the second command (not entire host home:
other apply kinds may legitimately write). Use known optional gbsrc offline declaration input;
host composer must not query workspace broker snapshots.

```text
TestComputedBriefingHostApplyPropagatesRenderFailure:
  house producer returns "HOST-NEW" for claude, errors("producer-broken") for pi;
    dest paths [.claude/CLAUDE.md, .pi/agent/AGENTS.md], resolves once per path.
  execute applyWith(t, true, nil) -> actual applyHost -> applyHostSurveyed -> applyHostBriefings.
  assert rc==1; report names house/derive.lua, producer-broken, line, yolo pack lint;
    destinations and ownership manifest unchanged, no briefing retirement/archive mutation;
    countLines(report,"briefing","refused") >= 1, no successful changed briefing line.
  failure literal: "host apply swallowed producer error or published a failed briefing batch".
  deletion controls: remove applyHostBriefings -> error-bearing compose/render call;
    rc==1/diagnostic assertion fails; remove outer applyHostSurveyed brc handling separately;
    rc==1 assertion fails even when kind report still says refused.
  positive twin: producer validates ctx.notch=="host", destination identity and each named
    source running==false, why_not=="on the host, the agent uses your own login";
    returns "HOST-" .. ctx.agent; command rc==0, both exact tokens delivered.
    Test-only evaluation observer counts one per admitted path; report/render do not double it.
```

The earlier `RenderHostBriefings` full Go control still pins all-batch failure and late pi
error. Adapt its entrypoint call to the new error-bearing render API without precomputing in
the test; it must exercise production composition. Broken-link I/O partial writes remain
separate existing behavior. Repeat command in observe mode: rc 1 for producer failure and no
briefing writes; do not claim observation successfully previewed broken computed prose.

### Config apply: partial file success then fatal briefing — E

Permanent file: new `internal/cli/run/liveapplybriefing_test.go`. Reuse
[`applyFixture`](../../internal/cli/run/liveapply_test.go#L45-L111), `answer`, `swapSeam`,
and gate/scope setup in [`TestConfigApplyReplacesGrants`](../../internal/cli/run/liveapply_test.go#L840-L884).
Install a temporary selected brokered pack via the normal config reader (the older raw
`SetPackModules` fixture alone is insufficient for `configApply` reload). Runtime answers a
known running jail and frozen tags/mechanics. Two real temporary version-1 scope files A/B
belong to the same source/cname/workspace with different valid launch identities; retain their
old read-only grants and C1 metadata. Discovery seam may return these files but snapshot must
read the resulting **actual bytes**, not desired grants. `replaceScopeReposSeam` delegates A
to real C-owned `ReplaceAccessScopeContext` after that API exists (same identity checks as
current `ReplaceScopeContext`), returns `errors.New("scope-B-blocked")` for B. Fake liveness
for fixture PIDs only; no real broker/API required for this replacement operation.

```text
TestLiveApplyBriefingPartialScopeFailureReturnsFatalOne:
  write desired workspace create-branch grant for github.com/o/r and user eligibility;
    gate answers y; frozen applied baseline is read-only; running-tree producer errors("producer-broken").
  seed BEFORE files/HANDOFF; execute o.configApply(false), not runApplySteps directly.
  expect A new read+create-branch, B old read-only, approval recorded desired grant;
    exactly two replacement attempts, exactly one refresh after A success/B failure,
    refresh sees A new and B old and uses running-tree script/applied mechanics.
  assert rc==1 (explicitly not 3); report includes Config apply result, scope-B-blocked,
    producer-broken script/line/lint, no claim all changes applied;
    folded baseline/mechanics must not claim the failed whole scope key fully applied.
  prior briefing bytes/inodes/HANDOFF unchanged; A not rolled back; no restart/stop/exec.
  failure literal: "partial scope mutation skipped refresh or BriefingErr became pending rc 3".
  deletion controls: remove per-file success-bit recording or post-step refresh call;
    expected rc==3/no producer diagnostic and the failure literal; remove BriefingErr->1
    priority in liveApplyExecution separately -> rc mismatch with same literal.
  positive twin: same A/B outcomes, successful producer returns "APPLY-jail-pi";
    core snapshot observer sees A new/B old (raw files are not Lua inputs);
    rc==3 only for B, A stays new/B old, exact APPLY token published once.
  all-success twin: both replacements real, producer succeeds, command rc==0 and next
    attach session reads refreshed text without runtime restart.
```

Before Lua, the source transaction must be released; assert it by an observer acquiring the
same source transaction with a bounded context at evaluation entry. The observer records
failure rather than hanging. At replacement and evaluation entry, a separate lock contender
must fail a nonblocking workspace launch-lock take; after `configApply` it succeeds. Use
existing lock primitives rather than sleeping to guess ordering. Never nest `Store.Working`
inside its source transaction. Omit no-mutation rows: all B-like failures, record-only,
nonrunning, unknown-runtime and unchanged scope perform zero refreshes and retain their
existing rc/report posture; a missing producer call cannot pass solely on those negative rows.

```text
TestLiveApplyBriefingFatalErrorStopsAttach:
  adapt attach fixture with real running tree/contracts/session marker, terminal y or
    AcceptConfigChanges; same A-success/B-failure replacement and erroring producer.
  execute attachExisting (or Run attach), assert (1,false), no session marker,
    exactly one failed apply composition and no ordinary fallback refresh;
    runtime remains live, A still new, B still old, prior briefings/HANDOFF unchanged.
  failure literal: "applyAtAttach discarded BriefingErr and started a session".
  deletion control: remove attachExisting -> applyAtAttach error check; session marker
    or missing error assertion fails; remove applyAtAttach error return independently.
  positive rows: y with successful producer reaches session reading new facts;
    n, pending-only and no-TTY notice still reach session with live applied facts,
    no replacement on n/no-TTY and no approval rollback.
```

### Native and conditional-policy rows

New D native test uses `dispatchOptions(...,"macos-user",...)` and a `MacosUserRun` counter,
never a real account/bootstrap. Cover keeperless returned handles and joined keeper/live
scope separately; producer error → rc 1, zero `MacosUserRun`, own service cleanup only,
no shared keeper teardown. Positive guest/jail notch rows deliver session-approved facts;
unselected/disabled/placement-refused/helper-not-started rows cannot become running by reading
manifest declarations. Delete native refresh fatal return to make the error row red, and
native projection-to-refresh connection to make actual-facts positives red. Real native and
Apple Container delivery remain platform/CI verification, not Linux fixture proof.

Owner choices remain **conditional**, not accepted failure policy:

- [OQ-CBP1](#OQ-CBP1): test raw carrier round-trip with one remote holding `url` A and
  `pushurl` B. If owner selects omission of ambiguous names, assert no `origin` for A≠B.
  If owner selects singleton only for normalized unanimity, assert `origin=github.com/o/r`
  for several equivalent URL spellings, absent for A≠B. Do not select first/fetch/push by
  ordering. Pin the chosen equal-identity and distinct-identity expectations only after ruling.
- [OQ-CBP2](#OQ-CBP2): inject working-store/transaction error `snapshot-broken` at actual
  Run/attach/apply snapshot acquisition, not Lua. **If fatal is ruled**, require operation
  rc 1, no batch publication/session delivery, appropriate fresh unwind or live preservation,
  and live-apply mutations retained. **If a nonfatal false-source projection is ruled**, require
  precisely the owner-specified source/reason and empty/withheld fields, successful producer
  prose and operation completion; never silently assert stale/partial facts are valid. No such
  nonfatal field/reason policy is selected here. Attach's accepted matching unreadable-scope
  exception above is unconditional and distinct from this outer snapshot error.

**Unexecuted RED/green commands for the authorized builders**, after fixture/API materialization:

```bash
go test -short ./internal/cli/run -run '^TestComputedBriefing(RunPublishesAfterActualStart|RunFailureUnwindsBeforeMain|AttachUsesRunningTreeAndFreshScope|AttachFailureLeavesLiveJail)$' -count=1
go test -short ./internal/cli/run -run '^TestComputedBriefingKeeper' -count=1
go test -short ./internal/cli -run '^TestComputedBriefingHostApply' -count=1
go test -short ./internal/cli/run -run '^TestLiveApplyBriefing' -count=1
```

Record actual argv/base, fixture path, direct exit and expected assertion text at RED, green
and each single-call deletion. Keep these permanent tests in the named implementation units;
this docs-only continuation installs none and makes no executed-operation claim.

## Ordered build units

Every unit reports baseline SHA, installed test path, actual red output, green gates, caller
mutation control result, and any cosmetic drift. Every unit stops on the shared precedence
conditions. Commit messages are proposals to use exactly once the respective unit is green.

### A — declarations and virtual-source governance (independent after manifest handoff)

- **Settled, do not re-open:** producer declaration and registration pair both ways; missing
  registration warns, undeclared registration refuses; producer output takes the virtual file's
  slot; `agents` narrows and absence broadcasts.
- **Preconditions:** reconcile the actual landed grouped-TOML contribution representation; A itself
  introduces `derive`/`reads` and needs no C1 carrier prerequisite. No concurrent
  manifest writer; `git status --short` clean; baseline declaration control is red as above.
- **Tests first:** declaration control, physical/virtual collision including empty physical
  file, interleaved physical/virtual ordering, original-governor preservation across clones,
  anonymous destination and duplicate-path identities, strict/tolerant JSON/TOML parity.
- **Change:** only A's map fence. Preserve `describes`, source locations, selected-pack audience
  validation and remainder sources. Update `SourceKey`/`carriesFor` so a producer counts as
  content without requiring a nonexistent file read.
- **Gates:** `go test -short ./internal/packdecl ./internal/packload`.
- **Fence:** no VM execution, config approval, scopes, host writes, or private pack edits.
- **Commit:** `feat(packs): declare computed briefing sources`.

### B — narrow Lua producer and inspection (independent core, CLI after A)

- **Settled, do not re-open:** only `notch`, destination `agent`, declared `access`; one session
  per pack/composition; five-second budget; strict string result ≤64 KiB; failure fatal.
- **Preconditions:** A's field/key handoff; C4 proposed signature reconciled with the landed tree; dedicated session tests
  and actual lint caller tests present and demonstrably red. Do not add provider/config inputs.
- **Tests first:** duplicate registration; independent derive/env/briefing namespaces; missing
  and surplus pairing; listing does not call a function that raises; timeout at registration
  and invocation; `nil`, table, boolean, number, 65,537-byte result rejected; 65,536 accepted;
  empty string accepted; sandbox stripped globals/randomness; undeclared access error;
  per-destination input mutation cannot leak; absent additive field remains `nil`.
- **Change:** B's fence; retain current tolerant surface readers' reported unknown API behavior,
  strict host env behavior, and no-op briefing registration on readers that do not compose it.
  Lint lists `derive:finishing → claude, pi (reads access: git, optional)` and footprint exposes
  the declared read before launch without adding a new credential/exec banner. Source-universe
  contract C5 below is required before treating unselected as known. Unknown-source checks
  are C's, not a hardcoded git registry.
- **Gates:** `go test -short ./internal/agentcfg/luahook ./internal/packload ./internal/cli`.
- **Fence:** no start/disposition inference, no scope reads in Lua, no grants or typed settings.
- **Commit:** `feat(briefing): evaluate isolated Lua producers`.

### C — approved facts and snapshots (independent core; integration follows carrier handoff)

- **Settled, do not re-open:** applied, never configured; sorted concrete identities;
  wildcard expansion; broker start truth; source-not-selected optional entry is present/false.
- **Preconditions:** C1–C3 and C5 proposed contracts and backward-read cases fixed. No change in the
  broker's authority or login/host-source boundary. Current APIs cannot supply these facts
  completely; do not implement this unit by guessing from the seed.
- **Tests first:** gate approval declined/request edited after approval absent; raw remotes
  survive keeper/scope serialization; approved `origin` unchanged after workspace remote edit;
  read-only, branch-main, create-branch, declared/created reason, ended/revoked/wrong-workspace
  entry, read prerequisite, disallowed live kind, wildcard across two approved repositories;
  equivalent URL identities; empty remotes; placement/scope-write/start failure false.
- **Permanent callers:** gate recording → keeper restoration → scope write; actual
  `startLoopholesMatching`; attach `LiveFilesContext` successor; snapshot working-store read.
  A helper-only synthetic map test does not cover those transfers.
- **Change:** C's fence. Source universe must come from the same installed/configured discovery
  universe used by `unmatchedSupersessions` in
  [`loopholes/supersede.go`](../../internal/loopholes/supersede.go), separate from selection.
  Its nearest-capability helper is private; reuse the algorithm through a narrow source-name
  adapter after C5's declaration-lookup handoff, not pretend an exported generic suggestion
  API or an all-unselected source registry exists. Reject `gti` with suggestion
  `git`; reject real unselected source unless optional; inspect brokered manifests, not pack slug.
- **Gates:** `go test -short ./internal/config ./internal/brokerscope ./internal/brokerstore ./internal/cli/run`.
- **Fence:** no credential reads, broker-policy widening, current-branch query, or universal
  config/schema redesign; carrier overlap is serialized with its owner.
- **Commit:** `feat(briefing): project approved broker access facts`.

### D — all composition callers and fatal publication (after A/B/C)

- **Settled, do not re-open:** one call per admitted deduplicated destination; host has no
  broker; fresh launch starts no agent on failure; attach failure leaves existing jail running.
- **Preconditions:** C2 keeper readiness/teardown handoff implemented; C4 batch API fixed;
  the two caller controls above recorded red; running-tree and legacy-attach fixtures available.
- **Tests first:** both full caller controls; late failure after another destination succeeded;
  unchanged prior bytes/inodes/ownership record/handoff; diagnostics name pack script and line
  plus `yolo pack lint`; host context/access false; guest/jail notch; static/computed order and
  provenance parity; missing declaration warning; registration surplus failure at operation.
- **Permanent operation controls:** materialize the [operation recipes](#operationcaller-regression-recipes)
  at `Run`, `keeper.run`, both attach refresh paths and `applyHost`; the request-transfer,
  marker-time publication and fatal-return controls are independent of evaluator-only tests.
  Record the specified production-call deletion failures in an authorized disposable tree.
- **Change:** D's fence. All Lua completes before publication. Do not precompute producer text
  in `stagePacks`; attach loads running pack sources through `runningJailPackView`/`loadPackTree`
  and evaluates using live applied access, regardless of current cfg requests. Normal and
  legacy attach paths both propagate fatal producer errors. Host report uses the same batch.
- **Gates:** `go test -short ./internal/cli/run ./internal/entrypoint ./internal/jailcontent ./internal/cli`.
- **Fence:** no E live-apply hunks, no changing skills failure policy, no host grant query.
- **Commit:** `feat(briefing): compose producers before publishing destinations`.

### E — live scope refresh and delivery (after D)

- **Settled, do not re-open:** scope-changing apply composes from what it wrote, under the launch
  lock; working-set push alone does not render; failed producer fails apply, without fallback.
- **Preconditions:** live-apply fatal outcome/attach propagation handoff fixed; D batch caller
  ready; use running pack tree/applied mechanics, never staging the requested pack selection.
- **Tests first:** through `configApply`/`applyAtAttach`, approve scope change and read new bytes
  without restart; unchanged/record-only/nonrunning/unknown runtime does not falsely refresh;
  partial multi-file replacement composes only actual state; postreplacement producer failure
  returns failure and publishes no text while reporting mutations already performed; source
  lock released before Lua; launch lock serializes competing refresh; macos-user sessions use
  their applied scopes rather than one requested config. The [operation recipes](#operationcaller-regression-recipes) specify exact fixtures and
  failure controls; their implementation and RED executions are still owed.
- **Change:** E's fence. Compose once after the applicable scope step group, before successful
  operation completion. Do not silently map producer failure to pending/restart or omit it
  from the void attach apply path. Preserve `foldApplied`'s truthful partial state.
- **Gates:** `go test -short ./internal/cli/run -run 'Test.*(ComputedBriefing|LiveApplyBriefing|ConfigApply)'`;
  targeted integration cases below, once the code/fixtures exist.
- **Fence:** no auto-restart, no rollback of completed approval/grants, no broker-triggered writes.
- **Commit:** `feat(briefing): refresh computed prose after live scope apply`.

## Ships with and proportional verification

- **Unit/caller tests:** A–E's named cases; actual producer-call deletion controls remain
  permanent. Update parity expectations to include computed virtual sources, not replace
  independent content assertions with jail-equals-host alone.
- **Integration:** proposed `TestComputedBriefingLaunchAndAttach`,
  `TestComputedBriefingLiveScopeApply`, `TestComputedBriefingFailureStopsOperation` in the new
  integration file. Use a temporary selected pack, fake producer prose, `requireJail(t)`,
  serial package execution, and `YOLO_TEST_JAIL_TIMEOUT`. Run shell commands to read briefing
  files, never start an interactive coding agent or make an API call.
- **Integration command once implemented:**

  ```bash
  go test -count=1 -timeout 0 -run '^TestComputedBriefing(LaunchAndAttach|LiveScopeApply|FailureStopsOperation)$' ./integration
  ```

- **Docs:** the pack reference's derive-slot registrations, briefing declarations/governance,
  lint and version-boundary claims; the briefing reference's refresh/applied-body/audience
  claims; live-apply's scope/report/error claims. The inspected pack reference currently says
  there are two registrations and all producer inputs are `DeriveCtx`; that becomes incomplete.
  The briefing reference says composition precedes container existence and that editing
  `agents_md_extra` propagates on attach; authenticated applied mechanics now prevents an
  unapproved requested edit from becoming applied merely by attach. Correct that existing
  overstatement while documenting the new scope-refresh trigger. Existing static prose
  delivery remains valid. Do not change shipped git prose or add speculative pack settings;
  no CHANGELOG at landing.
- **Other surfaces:** doc comments on new manifest fields are schema reference; TOML diagnostics
  keep source positions; refusal/error text names next step; lint/footprint delivery wording;
  keeper/scope backward-reader fixtures; host apply error propagation/help if exit semantics
  need correcting. No generated binary, config key, or daemon is introduced by this feature.
- **Targeted first:** package tests at each component commit, `just format` before code commit;
  `python3 scripts/completion-census.py` when a changed test reads outside its package, committing
  the generated census through the authorized implementation owner.
- **Combined landing gate once:** landing owner runs `just check-ci`, full integration once
  for this launch-path change, and the required nested jail using the absolute launcher from
  `just build-dev-bundle` in a throwaway workspace. No builder should duplicate the full slow
  suite per unit. Main publication/`just done` belongs to the landing owner.
- **Honest limitations:** nested podman cannot prove rootless networking/reachability or
  rootless-only behavior; use a real host/CI for those claims. macos-user and Apple Container
  delivery need their real platform or CI; Linux fixture green is not runtime parity proof.
  This plan's docs checks are not software validation or the later whole-document HIGH review.

## Don't

- Do not build affected access projections until the two pending observable decisions are
  recorded in the behavioral authority. Once separately authorized, the responsible units
  materialize their proposed interfaces/fixtures/permanent tests, obtain behavioral RED before
  implementing the caller, then run caller-deletion controls after green and restore source.
  C1–C5 and live-apply signatures above are their engineering work, not preexisting APIs or
  executed-control admission gates. Precise recipes are the planning deliverable.
- Do not read full config into producers, add typed settings preemptively, publish pack facts,
  probe logins, widen credentials/grants, or treat generated prose as broker authority.
- Do not activate the old seed, implement in a private user's pack, or reinterpret an unknown
  declaration as static broadcast content.
- Do not treat planned start, old approval records, or current requested config as applied facts.
- Cheap implementation choices: internal map/slice factoring and helper file splits within
  the exclusive fence. Advice: keep typed facts beside their owning carrier, because display
  labels and secret-bearing surface contexts otherwise become tempting shortcuts.

## Readiness closure

The current source confirms the feature gap: `Contribution` has no briefing `derive`/`reads`,
`newDeriveSession` registers derive/env/model helper but no briefing producer, and production
refresh/render never evaluate one. The old seed's static map did not cover the keeper boundary,
applied remote-name carrier, live kind eligibility, or incremental-publication failure.

Before affected implementation dispatch, route the two observable gaps into the behavioral
authority. This plan supplies [precise operation recipes](#operationcaller-regression-recipes),
not executed closure. In the first separately authorized responsible unit, materialize its
interface, fixtures and permanent tests; record expected behavioral RED before implementing
that caller. After green, run the specified production-call deletion controls and restore
source. Neither absent feature APIs nor unexecuted deletion controls are global prerequisites
to beginning that unit. The prior planner observed the three baseline red bodies on unchanged
production source; the current continuation ran no tests. Operation abort/lock/keeper/live-apply
recipes remain unexecuted. The proposed C1–C5/live-apply APIs are implementation deliverables,
not admission gates. All three original owner product rulings stay closed. Independent docs-only
review may be explicitly skipped for this planning change; neither that skip nor targeted
render/link checks is equivalent to a HIGH code review or the separately requested
whole-document review.

The [roadmap's computed-briefings entry](../plans/roadmap.md) routes this
plan's pending [remote projection](#OQ-CBP1) and [snapshot failure](#OQ-CBP2) questions at the
existing priority position. The accepted design's three original owner rulings remain settled;
this routing is not plan readiness or authorization to implement.
