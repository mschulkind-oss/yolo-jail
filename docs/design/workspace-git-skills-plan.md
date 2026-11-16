---
title: Workspace Git skills implementation plan
status: accepted
stage: DECIDED
next: Implement Task 1 serially from this plan; stop on any non-cosmetic tree disagreement
depends-on: []
---

# Workspace Git skills implementation plan

**Written against** `b5769ecb393f234c2c36d46750d50b6d8cf4dae2`, 2026-11-16. The tree facts below were checked at that HEAD; this plan is the first implementation hand-off, not evidence that code or tests exist.

**Design:** [`workspace-git-skills.md`](workspace-git-skills.md), accepted / DECIDED. It owns behavior; this plan owns the build map. **Reads with:** [`config-approval-presentation-plan.md`](config-approval-presentation-plan.md), whose approval behavior and task sequence remain independent. Its config-key label coverage and any shared paths must be landed/rebased serially as specified in [the fence](#cross-feature-fence-config-approval-presentation).

> **In short.** Add workspace-only `skill_imports`, resolve through the same filtered Git broker fetch policy, persist host-owned per-workspace objects/records, project only confined skill directories, and stage them as the lowest layer. `add`, `update` and `pin` are host-only; jailed invocations print the existing human-mediated host-run line and do nothing. No jail-to-host RPC, credential, grant, scope entry or ambient host Git is part of the implementation.

## Precedence and settled behavior

1. The design wins on behavior. Do not reopen WGS-P15 or the owner rulings recorded in the [design's decision ledger](workspace-git-skills.md#12-decision-ledger).
2. The tree wins on fact; cosmetic path/symbol drift is corrected in the report. Stop rather than work around a missing authority seam, changed consumer, dirty file outside this task, or a test that fails differently than specified.
3. This plan is advice and the first thing to correct if the tree overtakes it. Never twist production code to match a stale plan.

The command contract is intentionally repeated here for a cold implementer: host `add`/`update`/`pin` only; jailed form prints `yolo host-run -C <workspace> -- yolo skills <arguments as typed>` and returns before effects. `remove` edits workspace config on either side. `list` reads declarations in a jail and gives the full resolution/store view only on the host. A host fetch is bounded by the workspace's last approved Git scope and grants, narrowed by current remotes and the current user read grant. Authenticated reads require the existing broad Git read grant and the exact approved remote URL. Public HTTPS keeps the existing anonymous-only path; SSH/scp without approved scope is refused. No repository-scoped read grant exists. The host fetch must be the same filtered fetch path as a launch, never `packsrc` or ambient `git fetch`.

## Current-tree map

| Path | Change |
|---|---|
| `internal/config/skillimports.go` (new), `readableschema.go`, `readablevalidate.go`, `readableconsumers.go` | Add typed workspace-only `skill_imports` entries and user-only `workspace_skill_imports` veto to the canonical readable config consumers; reject the key at user scope and the veto at workspace scope. |
| `internal/config/validate.go`, config source-named tests | Validate handles, transports, URL credentials, pin/ref forms, paths, filters and the scalar veto using the ordinary config gate; no fetch in validation. |
| `internal/gitbroker/broker.go`, `https.go`, `upstream.go` and new broker caller tests | Factor/reuse the exact fetch authorization and upstream leg for an explicit host import caller; do not add a host-service operation or change the jail protocol. |
| `internal/paths/paths.go`, `internal/runtime/naming.go` | Add a host-only import-state path rooted under `paths.GlobalStorage()` and keyed by `runtime.FromWorkspace`; keep it out of `approvals/`, checkout, user config, jail mounts and `cache/`. |
| New `internal/skillimports/` package and tests | Implement import-store/record read-write, lock, resolution policy, explicit-command authority narrowing, and projection input from committed Git objects. Keep filesystem effects behind explicit calls. |
| `internal/jailcontent/workspaceskills.go`, `workspaceskills_test.go` | Accept imported skill input through the existing confined reader/composition, with the design's stricter per-import-skill symlink and all-or-nothing refusal rules and the shared workspace skills caps. |
| `internal/cli/run/run.go`, `prepare.go`, `workspaceskills_test.go` | Resolve only after the fresh-launch config gate; pass the current approved scope/grants to resolution, and stage current records on fresh launch and attach. Attach must never fetch or refresh. |
| `internal/cli/dispatch.go`, new `internal/cli/skills.go`, new CLI tests | Register public `yolo skills`; implement host effects and human-mediated jail refusal; config-only remove/list semantics are as designed. |
| `internal/cli/config_ref.txt`, `configrefcoverage_test.go`, config-ref tests | Add only the new config-key reference entries; ensure every readable key has an intentional label and example. |
| `docs/design/workspace-git-skills.md`, `docs/design/workspace-git-skills-plan.md`, `docs/plans/roadmap.md` | Keep design/plan/roadmap aligned; the code slices must update relevant usage/reference docs in their own commits. |
| `docs/reference/config/editing.md`, `docs/reference/agent-briefings.md`, `docs/reference/pack-system.md` | Recheck claims affected by the new config key, command group, and imported lowest-precedence skill layer; edit only claims that become stale. |
| `integration/skillimports_test.go` (new) | Exercise a real fresh launch with a synthetic bare Git source and local fake-SSH fixture; gate with `requireJail(t)`, assert staged skill bytes via the same workspace path as the unit caller, and never run an agent or contact a remote. |

### U5–U7 current-tree verification

- **U5, broker caller:** `internal/gitbroker.Broker.Handle` is the jail request entry; `serveRequest` performs URL/scope/read-grant checks and dispatches SSH or HTTPS upstream through private functions. `https.go` rechecks authority before credential use; `upstream.go` starts the host transport. There is no host-side import caller. Existing consumer fixtures are `internal/gitbroker/broker_test.go`'s `newFixture`/`TestFetchThroughTheBroker` and `https_test.go`'s local TLS `newHTTPSFixture`. Reuse these fixtures, but make the new test drive the import resolver/CLI caller, not just a new authorization helper.
- **U6, host state:** `paths.GlobalStorage()` and `paths.ApprovalsDir()` are in `internal/paths/paths.go`; approvals are host-only and have a closed record-file list in `internal/config/scopeapproval.go`. `runtime.FromWorkspace` is the existing workspace identity. `config.ApprovedScope(workspace, "git")` and `config.ApprovedGrants(workspace, "git")` report the last approval; `config.BrokeredUserGrants("git")` reports the user's current grant. `brokerscope.ReadAllRemotes` reads current remotes as config text. Keep import files separate from approvals and all path-keyed state tests scoped under a temporary HOME.
- **U7, caller context:** `internal/cli/hostrun.go`'s jail branch calls `hostRunReadyLine`; it asks a human to paste a line and does not execute on the host. `internal/cli/packadd.go` and jailed user-scope edits in `configeditverbs.go` are the precedent; workspace-scope config edits remain legal in-jail. `internal/cli/dispatch.go` has no `skills` command; `internal/config` has no `skill_imports` key today. The existing resolver/stager callsite is the launch's `Options.stagePacks` followed by `Options.refreshJailBriefings` (`internal/cli/run/run.go`, `prepare.go`); the latter also runs at attach, so only its staging/read path may handle imports.
- **Grant boundary:** repository-binding ceilings are write-only (`internal/config/reposcope_ceiling_test.go:TestAScopedCeilingRefusesTheReadCapability`, `reposcope_gateceilings_test.go:TestAScopedCeilingWithoutAReadFloorWritesNothing`). Credentialed fetch still requires the broad Git read grant plus the exact approved URL. Do not infer scoped read, change grant semantics, access user credentials directly, or add a broker RPC.

## Reuse before writing

| Existing seam | Reuse |
|---|---|
| `config.LoadConfig`, `ValidateConfig`, readable schema/consumers | One config interpretation shared by config check, CLI and launch; use the ordinary merge/validation path. |
| `config.ApprovedScope`, `config.ApprovedGrants`, `config.BrokeredUserGrants`, `brokerscope.ReadAllRemotes`, `brokerscope.ReadCovers` | Explicit-command authority inputs and fail-closed current-state narrowing. The approved URL list is exact URL scope, not repository identity. |
| `internal/gitbroker` fetch authorization and `https.go` anonymous/credential policy | One filtered host fetch policy, reused by launch and explicit commands. Preserve anonymous HTTPS and credential rechecks as-is. |
| `paths.GlobalStorage`, `runtime.FromWorkspace`, `config.ApprovalRecordFiles` | Host state root, workspace partitioning and the explicit exclusion from approval-record files. |
| `internal/cli/hostrun.go`, `packAdd`, `configeditverbs.go` | Jailed host-command refusal and allowed workspace-scope config edits. Test through the CLI dispatcher so registration/callsite deletion fails. |
| `internal/jailcontent.WorkspaceSkills`, `PrepareSkillsWith`, `openConfinedTree` | Confined workspace-skill copy/composition, refusal reporting and shared caps. Imported trees must enforce the stricter link boundary in the design; do not use the pack stager. |
| `internal/cli/run/workspaceskills_test.go` (`wsSkillsLaunch`, `stageWorkspaceSkills`) | Actual staging caller fixture: it runs `stagePacks` then `refreshJailBriefings` and reads staged bytes. Extend this, not a disconnected helper-only test. |
| `internal/gitbroker`'s local fake-SSH repository and `httptest` HTTPS fixtures | Network-policy tests without external fetches, API calls, real credentials or agent CLIs. |

## Traps

- The broker's existing server is jail-request-driven and holds launch scope; do not treat `Broker.Handle` as a host `Fetch` API or copy a weaker policy beside it.
- Approved scope is a list of exact remote URLs for Git, not repository identities. Identity equality alone never admits a URL.
- Last-approved scope alone is not enough: intersect it with current remotes and the current user read grant. Current state narrows; it never supplies a newly added remote/grant before the gate.
- `workspace_skill_imports` is a user-only scalar veto; it is not workspace-settable. `skill_imports` is workspace-only. Do not add a `config` CLI editing verb for imports; the public command group is `yolo skills`.
- An attach is a host-side reader of the running jail's staged pack tree; it never runs the import fetch/refresh step. Staging must use the record/store as they exist at attach.
- A fetched Git tree is not a trusted checkout. Do not run hooks, filters, submodules, pack manifests, `git checkout`, workspace Git config, `url.*.insteadOf`, user-scope `packsrc`, or the operator's ambient Git command.
- A test that pins a resolver/helper but not `Run`, CLI dispatch or `refreshJailBriefings` is not caller evidence. Each behavior slice needs a test which fails when its production callsite is deleted.
- Temp fixtures use `testtmp`/package helpers and `EvalSymlinks` where paths are compared. Do not set global `TMPDIR`, HOME, GOCACHE, GOPATH or GOENV; tests own and clean their effects.

## Build order and tasks

Every task is one commit in one worktree. Tasks are serial because their public config/command and launch seams join. Never land the approval-presentation plan's shared config-display key coverage concurrently; see the fence below.

### Task 1 — Config contract and validation

**After:** none. **Commit:** `feat(config): validate workspace skill imports`.

**Settled — do not reopen:** `[skill_imports.<handle>]` is workspace-only; `workspace_skill_imports = false` is a user-only scalar veto defaulting to enabled. Parse and validate pins/moving refs, URL/transport, path and name filters offline. Existing configs without either key remain valid. No config value causes a fetch during validation.

**Tests first — caller example:** Add `internal/config/skillimports_test.go` and a user-scope coverage case in `internal/config/workspace_skill_imports_test.go`. The test writes this exact `yolo-jail.toml` under `t.TempDir()`, loads it with `LoadConfig`, then calls the public `ValidateConfig` path; it must accept it before implementation. Also run the same case with `rev` removed and `ref = "main"`; accept. The red case is current validation treating the key as unknown.

```toml
[skill_imports.acme]
git = "https://github.com/acme/agent-skills"
rev = "3f9c2b7e1d4a5c6b8e9f0a1b2c3d4e5f60718293"
path = "skills"
only = ["review", "triage"]
```

Add table-driven negative cases through `ValidateConfig`: user-scope `skill_imports`, workspace-scope `workspace_skill_imports`, URL user-info, unsupported transport, abbreviated `rev`, invalid `ref`, `..`/absolute/`.git` path, slash/duplicate `only` names, and wrong veto type. Use `config.LoadConfig` plus canonical schema coverage consumer where applicable; do not test a validator helper alone.

**Red:** `go test ./internal/config -run 'TestSkillImportsWorkspaceConfig|TestWorkspaceSkillImportsVetoScope' -count=1` (new tests first; expected unknown-key/scope errors). **Change:** readable schema, consumer and validation callsites; retain paths `internal/config/readableschema.go`, `readablevalidate.go`, `readableconsumers.go`, `validate.go`, schema/config reference tests. Do not alter the broker or launch yet.

**Green:** `go test ./internal/config -run 'TestSkillImports|TestWorkspaceSkillImports' -count=1`; `go test ./internal/config -count=1`; `yolo config check` fixture path through its CLI test, not a real workspace. **Report:** red and green output, plus exact keys/cases covered.

**Ships with:** all schema/config-reference coverage for both scope spellings; existing unknown-key and config-check behavior unchanged. **Stop:** if canonical schema does not model readable workspace/user scope as expected; do not add a legacy key alias.

### Task 2 — Broker caller and durable host state

**After:** Task 1. **Commit:** `feat(skill-imports): add brokered per-workspace resolver state`.

**Settled — do not reopen:** one host caller uses the same Git fetch decision/upstream implementation as the broker; host state is one separate per-workspace sibling beneath `GlobalStorage`, keyed by `runtime.FromWorkspace`, not approvals/cache/checkout/user config. Only fetch (`git-upload-pack`) is needed. SSH/scp requires exact approved URL plus current broad read grant. HTTPS keeps the existing anonymous-first behavior; credentials are available only when the same approved URL/read checks succeed. No RPC is added.

**Tests first — caller examples:** In the `gitbroker` package, add `importfetch_test.go` around the host import-resolver caller. Make a synthetic bare remote, exact approved URL, and broad read grant; request one commit and assert the object is present in the caller-owned import store. Repeat with:

- missing grant and SSH URL: refusal, no upstream invocation;
- same repository identity but a different URL: no credentialed contact;
- public HTTPS without grant: anonymous-only behavior using `newHTTPSFixture`;
- unsupported/credential-bearing URL: no state files.

These tests enter through the resolver that launch and CLI call, not `scopeContains` or a new low-level helper alone. Use no external remote or real credential helper.

In `internal/skillimports`, tests call the public store/record reader/writer with a temporary HOME and two `t.TempDir()` workspaces: records and Git objects remain workspace-separated; record validity compares configured `git` plus `ref`/`rev`; incomplete/invalid records fail closed; concurrent writer lock is event-driven; objects are add-only; record replacement is atomic. Assert paths resolve under temporary `paths.GlobalStorage()` and do not appear in `config.ApprovalRecordFiles`.

**Red:** `go test ./internal/gitbroker -run 'TestImportResolverUsesBrokerPolicy' -count=1`; `go test ./internal/skillimports -run 'TestImportState' -count=1` (the new consumer tests are red before the resolver/store exist). **Change:** share the existing broker policy/upstream path rather than duplicating it; implement only the host-only store/resolution state. The store path's leaf name and serialization are implementation choices; preserve the design contract and do not create cleanup/GC policy.

**Green:** `go test ./internal/gitbroker -run 'TestImportResolverUsesBrokerPolicy|TestHTTPSPublic' -count=1`; `go test ./internal/skillimports -count=1`; `go test ./internal/config ./internal/gitbroker ./internal/skillimports -count=1`.

**Ships with:** synthetic SSH and TLS-HTTPS broker caller tests; current-scope/current-user-grant narrowing cases; state-path, identity-separation, malformed-record and lock tests. **Stop:** if broker code cannot expose a caller that preserves its policy without widening host authority; file a design gap in [`workspace-git-skills.md`](workspace-git-skills.md) rather than using ambient Git or a new daemon operation.

### Task 3 — Resolve after gate; project and stage through real launch/attach callers

**After:** Task 2. **Commit:** `feat(skill-imports): stage confined imports in jail skills`.

**Settled — do not reopen:** fresh launch resolves only after the config gate has passed and gets that launch's approved scope/grants. Explicit commands use last approval narrowed by current state. Attach never resolves or refreshes; it stages only from the current record/store under current access. Imported directories are projected from Git objects, copied through the confined workspace-skill path, strictly confined per skill, all-or-nothing per skill, subject to the existing shared caps, and lowest precedence. Nothing reaches `yolo host`.

**Tests first — real caller examples:** Extend `internal/cli/run/workspaceskills_test.go`'s `wsSkillsLaunch`/`stageWorkspaceSkills` path. With a synthetic approved local Git source containing `skills/review/SKILL.md` and `skills/other/SKILL.md`, configure `only = ["review"]`; drive the launch's actual post-gate resolver and `refreshJailBriefings`, then read staged `review` bytes and assert `other` and every non-skill path are absent. Remove the resolver call at the launch site and this test must fail.

For attach, prepare a valid record/store, make the fake upstream fail-on-contact, run the actual attach refresh caller, and assert it stages the recorded commit without invoking the fake upstream. Add reader caller tests for an escaping symlink and a special file inside an import: the entire imported skill is withheld, its sibling skill still stages, and no partial files appear. Pin lowest-precedence collisions with workspace-owned, built-in and pack skills via real `PrepareSkillsWith` composition.

**Red:** `go test ./internal/cli/run -run 'TestImportedSkillsReachJailThroughLaunch|TestImportAttachNeverFetches' -count=1`; `go test ./internal/jailcontent -run 'TestImportedSkill' -count=1`; `go test -run 'TestImportedSkillsReachJail' ./integration`. These are caller tests; the integration test uses a synthetic bare repository and local fake-SSH executable, not a real network, credential helper, API or agent.

**Change:** call resolver in the fresh-launch pipeline strictly after gate success; pass read-only resolved import input into the common staging path, separating resolver effects from attach projection. Reuse `WorkspaceSkills` confinement and reporting; extend its source representation only for imported immutable Git trees. Update run callsite tests for launch, attach and macos-user staging path; stage nothing in `pack-trees/.live`.

**Green:** `go test ./internal/jailcontent -run 'TestImportedSkill|TestWorkspaceLayer' -count=1`; `go test ./internal/cli/run -run 'TestImportedSkills|TestWorkspaceSkills' -count=1`; `go test -run 'TestImportedSkillsReachJail' ./integration`; then `go test ./internal/config ./internal/gitbroker ./internal/skillimports ./internal/jailcontent ./internal/cli/run -count=1`.

**Ships with:** projection boundary/refusal reporting, cap sharing, precedence/collision, fresh launch resolution after the gate, attach no-fetch and stale-record behavior. `integration/skillimports_test.go` must run a fresh launch against a synthetic bare Git repository served by a local fake-SSH executable, use the normal approved-scope test setup, and verify the imported bytes at the existing workspace-skills destination; do not invoke an agent, make an API/remote fetch, or use real credentials. Update `docs/reference/agent-briefings.md` and related workspace-skill references only where the current prose becomes incomplete; preserve WS-D24 host exclusion. **Stop:** if full launch placement cannot establish gate-success ordering or a running-jail tree fence without changing unrelated shared staging, report before editing.

### Task 4 — Public `yolo skills` commands and accurate operator reference

**After:** Task 3. **Commit:** `feat(cli): add workspace skill import commands`.

**Settled — do not reopen:** host-only `add`, `update`, `pin`; jailed versions print one existing host-run line and make no config/state/network changes. `remove` is workspace-config-only and works on either side. Jail `list` is declarations only; host `list` has resolution/store data. Add/update failures write nothing as the design specifies. `update` contacts only entries with valid resolution records; no approved scope permits anonymous HTTPS only and refuses SSH/scp.

**Tests first — command caller examples:** Add `internal/cli/skills_test.go` through the registry/dispatch caller. A `YOLO_VERSION`-set jail runs `yolo skills add https://example.invalid/acme/skills --as acme`; assert nonzero status, stdout empty, stderr contains exactly the host-run command and “nothing ran”, and the workspace config plus temporary `GlobalStorage` are byte-for-byte unchanged. The line must preserve the typed argv as a single human-mediated command. A jailed `remove acme` edits only workspace config; jailed `list` names declarations, says resolved state is host-side and prints the host-run line for full view. Host add/update/pin/list/remove run through the public command parser with synthetic local broker fixtures, not directly into handlers. Test add refusal/failed fetch no writes, pinned update with no valid record no contact, valid update moving ref with one record move, pin no network, and remove leaves store/record alone.

**Red:** `go test ./internal/cli -run 'TestSkillsCommandDispatch|TestSkillsJailedCommands' -count=1` (currently `skills` is unregistered); then `go test ./internal/cli -run 'TestSkillsHostCommands' -count=1`. Use the existing testconfig and dispatch capture patterns (`packadd_test.go`, `setupcmd_test.go`), temporary HOME/workspace and fake broker transport.

**Change:** `internal/cli/dispatch.go` registration and new command parser/handler, exact operator output/next steps and proper config edit via existing targeted config-edit API. Verify `config-ref`/help source and update its tests; update `docs/reference/config/editing.md`, config reference and workspace skill docs. No `internal/cli/config.go` verb is needed: command is `yolo skills`.

**Green:** `go test ./internal/cli -run 'TestSkills' -count=1`; `go test ./internal/config ./internal/gitbroker ./internal/skillimports ./internal/jailcontent ./internal/cli/run ./internal/cli -count=1`; `UV_OFFLINE=1 scripts/vantage-check.sh docs/design/workspace-git-skills.md docs/design/workspace-git-skills-plan.md docs/plans/roadmap.md docs/reference/config/editing.md docs/reference/agent-briefings.md`; `git diff --check`.

**Ships with:** command help, registry coverage, jailed no-effect disclosure, config editing examples, exact errors/next steps, update/pin/list semantics. No source tests start an agent or contact an external API.

## Cross-feature fence: config approval presentation

The accepted approval-presentation plan remains intact: its approval identity, JSON bytes, prompt predicate, slices, test gates and completion report are not changed by this plan. Its cross-feature note now links this accepted design and build hand-off. The features are independent, but integration is **serial, not concurrent**, wherever paths/tests meet:

- Approval presentation adds `yolo config approval-diff` in `internal/cli/config.go`; this feature is the top-level `yolo skills` registry entry in `internal/cli/dispatch.go`, not a `yolo config` verb. Keep each command in its own entry point; if help/usage/registry generated coverage overlaps after rebasing, update it with both spellings present.
- The approval plan's workspace-readable-key label coverage may fail when Task 1 adds `skill_imports`. If approval presentation lands first, Task 1 adds the accurate noun-phrase label and updates its coverage assertion in the same Task 1 commit; if Task 1 lands first, the approval slice must rebase and keep that test green. Neither feature may omit the key or weaken coverage.
- Do not edit `internal/cli/run/preflight.go` or `internal/config/snapshot.go` for skill imports unless the tree demonstrates a necessary caller change: imports resolve in the run pipeline after its existing gate, not by changing gate identity or presentation. If a source change becomes necessary, stop, record the exact overlap, and serially rebase/retest the two work plans.
- The approval plan's `internal/cli/config.go`, preflight, snapshot, approval-display and integration test fences otherwise remain unchanged. No shared edits run in parallel; land one feature's commit, rebase the next, then rerun both affected caller suites.

## Final gates, fences, and ships-with

**Final gates for the implementation landing:** all targeted package commands above; source-named integration caller test; `just format`; `just check-ci`; `just build-dev-bundle` and the project-required nested-jail smoke of the emitted absolute-path launcher from a throwaway workspace. This is not a reachability/rootless-only change. No full slow integration suite should be repeated by every slice; the landing owner runs the project-required suite once on the combined tree. `just done` runs only after all relevant commits and a clean worktree.

**Never do:** implementation via `packsrc`, arbitrary `git fetch`, jail-to-host helper/RPC, a scoped-read grant, new credential or broker grant, checkout/hooks/submodules/filters/manifests, user-scope config write from jail, access to host import state from jail, host skill delivery, cleanup/GC policy, or changes to approval-record schema. No production implementation is part of authoring this plan.

**Ships with:** workspace config and config-ref support; an explicit host command route and jail refusal; filtered host fetch sharing the broker policy; host-owned isolated store and resolution record; post-gate fresh-launch resolve; attach no-fetch projection; confined and lower-precedence staging; tests at config, broker, CLI and staging callers; updated operator references and readable-key label coverage. Existing approval display and Git read-authority behavior stay unchanged.
