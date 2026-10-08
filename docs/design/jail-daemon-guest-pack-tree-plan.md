---
title: "Plan: stop macos-user launches replacing live guest pack bytes"
date: 2026-10-08
status: accepted
stage: DECIDED
tags: [macos-user, packs, jail-daemon, lifetime, plan]
summary: "A tests-first handoff for JD-10's unique guest pack destination, shared by the launch payload and run/capture plans. Keep existing keeper, session-file and privilege contracts; retain bytes whenever consumer liveness is unknown."
next: "Add the two-launch destination and capture regressions; stop after the focused source tests, before a separately authorized native proof"
vantage:
  status-chip: true
---

# Plan: stop macos-user launches replacing live guest pack bytes

**Status:** 2026-10-08 — written against `22afb2d38483`; source defect confirmed by callers,
not a native reproduction. No source implementation or new native proof in this handoff.
**Design:** [the guest pack-tree correction](jail-daemon-on-macos-user-plan.md#the-correction-one-destination-one-writer-no-reuse),
with [the no-holder cleanup contract](jail-daemon-on-macos-user-plan.md#ownership-and-cleanup-unknown-keeps-the-bytes).

Precedence: the design wins on behavior; the tree wins on cosmetic fact; this plan is advice.
Stop and report a non-cosmetic disagreement, a regression not red as predicted, an owner-dirty
file, a missing seam, a required file outside the fence, or an unrelated gate failure. Parent
owns acceptance, delivery and any later source/native authorization; no commit/staging/dispatch
is authorized by this document.

## Settled — do not reopen

- One guest tree per launch at `/var/yolo-jail/packs/<cname>.<tree-id>`; tree id is the directory
  leaf of the pipeline's existing immutable host pack tree, **not** its pack selection/hash,
  a newly minted session id or workspace identity alone.
- Staging, bootstrap/session env, capture/fork plan and module-dir payload must agree on that
  exact destination. Create exclusively, fill before consumers start, never merge/replace.
- No holder proof means retention. A failed stage can remove only its own exclusively created
  tree when its writers have ended and **no consumer was dispatched**. Once any consumer was
  dispatched the current production lifecycle supplies no complete no-holder proof, even on
  ordinary return: retain and disclose. No record/age/account sweep is added for pack trees.
- Keeper/root ownership, session-file sweep, containment/ELF declines, supervisor policies,
  containers, privileges and credential/keychain behavior remain unchanged.

## Map and fence

| Path | Narrow work / proof |
| :--- | :--- |
| [`internal/macosuser/macosuser.go`](../../internal/macosuser/macosuser.go) | Shared unique-destination helper and create-once pack commands; retain `StagedPackRoot` as the legacy workspace path, never a live launch destination. |
| [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go), [`packroot_test.go`](../../internal/macosuser/packroot_test.go) | Connect `PackRoot`, env and commands to the unique helper; invariant catches reusable destination/unguarded removal. |
| [`internal/macosuser/capture.go`](../../internal/macosuser/capture.go), [`capture_test.go`](../../internal/macosuser/capture_test.go) | Install/fork captures use their supplied host tree identity; no unconditional guest-tree removal in generic capture cleanup. |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go), [`orchestrator_test.go`](../../internal/macosuser/orchestrator_test.go) | Exact pre-consumer unwind ownership and retained-tree disclosure at return. This actual lifecycle caller is necessary; pure helper tests cannot pin it. |
| [`internal/macosuser/staging_darwin_test.go`](../../internal/macosuser/staging_darwin_test.go) | Rewrite only packs' reusable-tree test; execute two distinct stages and an old restart target in private temp directories without sudo. Overlay and Mach-O staging tests stay unchanged. |
| [`internal/cli/run/macosuserguestdaemons.go`](../../internal/cli/run/macosuserguestdaemons.go), [`macosuserjaildaemon_test.go`](../../internal/cli/run/macosuserjaildaemon_test.go), [`macosuserdoorways_test.go`](../../internal/cli/run/macosuserdoorways_test.go) | Use the same helper over `o.packTree`, not workspace-only `StagedPackRoot`; real `Run` payload regression, adjust related expected guest argv only. |
| [`internal/cli/stores/inventory.go`](../../internal/cli/stores/inventory.go), [`macosuserrows_test.go`](../../internal/cli/stores/macosuserrows_test.go) | Narrow packs row: unique launch trees plus retained legacy trees, no false automatic reclaimer or blanket workspace deletion recommendation. No storage framework change. |
| [`integration/macosuserjaildaemon_test.go`](../../integration/macosuserjaildaemon_test.go) | Later source-named native fixture for overlapping sessions and restart/retention; no native run in the source-only slice. |

Audit-only callers already checked: [`jailDaemonsFor`](../../internal/cli/run/packservices.go),
[`Run`](../../internal/cli/run/run.go), [`macosUserRun`](../../internal/cli/commands.go),
[`runCaptureJail`](../../internal/cli/capturehost.go), [`startBackgroundReal`](../../internal/macosuser/real.go),
[`sessionfiles.go`](../../internal/macosuser/sessionfiles.go), [`keeper.go`](../../internal/cli/run/keeper.go),
and [`keeperspawn.go`](../../internal/cli/run/keeperspawn.go). Do not change their keeper/token,
stop/count, account or session-record protocols to make the tree collectable.

Cheap implementation choices: helper's name and factoring, exact diagnostics, storage-row
wording. Default advice: add `StagedPackTreeRoot(cname, hostPackRoot, sd)` beside the legacy
helper, because it avoids breaking every legacy/manual-path reader and makes both identities
visible. A create-once directory filled before consumers start needs no temp rename: exclusive
mkdir of the final destination, copy the source's **contents**, preserve execute bits/readability
and deny guest writes. Never run removal against an existing destination to make mkdir succeed.
Track successful exclusive creation in the executor, not just a plan boolean; a failed reservation
confers no ownership. A command interrupted before its creation outcome is known is retained.

## Reuse and traps

- `newPackTree` already mints a unique host directory with `os.MkdirTemp` before payload
  composition. `Run` hands this exact `staged.root` to `MacosUserRun`; capture's callback
  passes it as `HostPackRoot`. No new front-door argument, session-id mint or keeper record.
- `relUnder` handles containment and darwin symlink spellings; `JailDaemonSpec.InGuest` places
  every module-dir token. Keep both; replacing only argv[0] breaks config-file arguments.
- `PlanInvariants`/`CapturePlanInvariants`, `planWithPacks`, `testCaptureOptions`,
  `macosUserLaunch` and `payloadOf` are existing fixtures/call-site gates.
- `stagesTreeAt` currently recognizes the final `mv` of tree staging. Adapt it to recognize
  **pack** create-once staging without relaxing home-overlay/context staging checks.
  Its callers and `stageCommandsUseFreshInode` also cover binaries; preserve those rules.
- `RunMacosUser` currently defers supervisor stop after the agent and before session-file
  cleanup. `Background.Stop` has no descendant verdict; `Exited` reports the sudo launcher.
  Neither is permission to remove a used guest tree. `sessionTeardown` remains env/CA/profile
  cleanup, not a new pack reaper.
- `CaptureCleanupCommands` does not currently remove packs. Preserve that for dispatched
  consumers, and report retention in both `RunCaptureAct` and `RunForkBuildAct`; a returned
  driver is not proof of no detached descendant. The per-program capture lock is not a tree id.
- Keep `/var/yolo-jail/packs/<cname>` and its old `.new` intact. Flat new siblings avoid
  traversing/adopting a legacy tree an older session may still use.
- No automatic account-wide/root-tree enumeration, age collection, guest-held lease,
  passwordless sudo or host-home grant. No weakening of Seatbelt to make the fixture pass.

## Task 1 — unique destination and placement (first)

**Preconditions:** clean assigned files and the named seams still present. No native execution.
**Tests first:** add these to the existing package fixtures; they compile against today's APIs.
The expected red is **one shared guest root for two different host trees**. These tests alone
are not enough: the real `Run` payload test described below is a required companion.

```go
func TestTwoLaunchPackTreesHaveDifferentGuestRoots(t *testing.T) {
    a := planWithPacks(t, "/host/pack-trees/20261008T120000Z-1111111111")
    b := planWithPacks(t, "/host/pack-trees/20261008T120000Z-2222222222")
    if a.Cname != b.Cname {
        t.Fatal("fixture must represent the same workspace")
    }
    if a.PackRoot == b.PackRoot {
        t.Fatalf("second launch replaces first launch's pack root: %s", a.PackRoot)
    }
    for _, p := range []RunPlan{a, b} {
        if !containsArg(p.BootstrapArgv, "YOLO_PACK_ROOT="+p.PackRoot) {
            t.Fatal("bootstrap lost its own tree")
        }
        if !SandboxEnvFileSets(p.EnvFileContent, "YOLO_PACK_ROOT", p.PackRoot) {
            t.Fatal("session lost its own tree")
        }
        if problems := PlanInvariants(p); len(problems) != 0 {
            t.Fatalf("invalid plan: %v", problems)
        }
    }
}

func TestRepeatedCapturePackTreesHaveDifferentGuestRoots(t *testing.T) {
    ao, bo := testCaptureOptions(), testCaptureOptions()
    ao.HostPackRoot = "/host/pack-trees/20261008T120000Z-1111111111"
    bo.HostPackRoot = "/host/pack-trees/20261008T120000Z-2222222222"
    a, b := BuildCapturePlan(ao), BuildCapturePlan(bo)
    if a.Cname != b.Cname || a.StagingRoot != b.StagingRoot {
        t.Fatal("fixture must represent repeated capture of one program")
    }
    if a.PackRoot == b.PackRoot {
        t.Fatalf("repeated capture reuses guest root: %s", a.PackRoot)
    }
    for _, p := range []CapturePlan{a, b} {
        if !containsArg(p.BootstrapArgv, "YOLO_PACK_ROOT="+p.PackRoot) {
            t.Fatal("capture bootstrap lost its own tree")
        }
        if problems := CapturePlanInvariants(p); len(problems) != 0 {
            t.Fatalf("invalid capture plan: %v", problems)
        }
    }
}
```

```bash
go test -short ./internal/macosuser -run 'Test(TwoLaunchPackTrees|RepeatedCapturePackTrees)'
```

**Change:** wire one shared mapping through the map's plan/payload/staging seams. Keep empty
source behavior and module-relative token semantics. Add the `Run` regression beside
`TestMacosUserRunsAModuleDirJailDaemonFromTheStagedCopy`: intercept each `MacosUserRun` call's
host root and payload for two launches of one workspace and compare payload paths against the
run plan built from **that same intercepted root**. Change the local module body between them,
then drop it for C; A's stored argv must still name A's unique root. Deleting placement or
reverting only one of the run/capture plan call sites must fail. Preserve the outside-tree,
symlink-root, ELF-decline and container non-placement cases.

**Execute private staging test on macOS later:** create source A (`hello` script printing `A`),
B (printing `B`), C (module absent); stage each under the same cname and distinct host tree
leaves into one temporary state dir. Assert A's script's bytes/inode remain unchanged, execute
A's stored target again and get `A`, and prove B/C cannot remove it. Reusing A's exact destination
must fail reservation without changing A. A source file with its exec bit keeps it; a data file
does not gain exec. Rewrite the packs arm of `TestStagedTreesReplaceRatherThanMerge` to this
contract, not until the old replacement behavior passes. Do not alter the home-overlay arm.

## Task 2 — cleanup and honest retention (after task 1)

**Tests first:** through `RunMacosUser` and its recorded `Deps`, fail after exclusive pack
creation but before the bootstrap is dispatched: exact-owned cleanup occurs, sibling A and
legacy root stay. Fail reservation: no removal of that destination. Then dispatch bootstrap,
supervisor/agent or capture driver, and assert guest-tree retention on success, refusal,
signal teardown, closed/nil `Background.Exited`, expired sudo and an unlocked launcher record.
The existing session env/profile cleanup must still occur; record failure must not refuse a
launch. Delete the retention call-site guard and see the unknown-case regression fail.

**Change:** smallest executor ownership marker for pre-consumer unwind, conservative
post-consumer retention and path-specific disclosure. The same behavior applies to install
and fork-build capture acts. In `TestTheMacosUserSectionListsTheStateDirAndTheHomesStores`,
rewrite the packs row's `workspaces` count label and blanket removal expectation only: entries
now include retained launch trees, not one reusable copy per workspace. Keep the no-reclaimer
verdict and the rest of the inventory test unchanged. Keep every post-consumer case unknown
in production until an
actual complete holder proof exists; no synthetic success from `Background.Stop` or `Exited`.
A fake proof cannot become a production env bypass. Update the packs inventory row's human
inspection/recovery text, not its verdict into an automatic reclaimer.

**Mutation evidence required:** restore workspace-only placement; restore reusable staging;
unconditionally remove on return; add pack removal to `sweepGoneSessions`; remove capture's
mapping. Each affected regression must go red at its actual caller, then green after restoration.
No production files are left mutated.

## Focused source gates and ships with

Commands below are for the later authorized source worker, **not run for this doc-only task**.
Parent owns whole-tree/landing gates and the separately scheduled native pass.

```bash
go test -short ./internal/macosuser ./internal/cli/run ./internal/cli/stores
go test -short ./internal/cli -run 'Test.*(Capture|ForkBuild|MacosUser)'
go test -short ./integration -run '^TestMacosUser.*(JailDaemon|ModuleDir|Capture)'
uvx vantage-check docs/design/jail-daemon-on-macos-user-plan.md docs/design/jail-daemon-guest-pack-tree-plan.md docs/plans/roadmap.md --strict
uvx vantage-check index --roadmap docs/plans/roadmap.md --format json
```

- Adjust only path expectations in [`ctxtree_test.go`](../../internal/macosuser/ctxtree_test.go)
  if its pack-root row now describes the legacy helper; no context behavior change.
- Update the old reusable-copy warnings in [the jail-daemon reference](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox)
  and [the module-dir token reference](../reference/loophole-system.md#two-module-dir-tokens-and-value-sanitation)
  **after** implementation: unique paths, conservative retention, no unproved native claim.
- The source comment in [`macosuserguestdaemons.go`](../../internal/cli/run/macosuserguestdaemons.go)
  must stop describing replacement as current once corrected. Keep unresolved privilege,
  keychain and held host-daemon work outside this diff.
- No new config flag, lease, registry, daemon or prerequisite gate. Source test names are
  cheap to refine; the behaviors/mutations above are mandatory.

## Real native proof still owed

Only a separately authorized Mac run can settle root ownership, permissions, copying, Seatbelt
exec and actual restart behavior. Extend the fixture in the named integration file; no agent
turn/API, credential read, account-wide scan/kill, helper/sudoers installation or dispatch here.

Run overlapping sessions A and B of one workspace. A's local script emits its own marker,
`$0` and PID to a fixture-specific file, exits nonzero once, then restarts under the unchanged
supervisor policy. Hold A at a marker barrier; change its host module for B, then drop it for C.
Require overlap markers from the sessions, distinct guest roots, A's before/after-restart bytes
and path unchanged, B's own new bytes and C's absence. Use per-tree markers rather than a
per-workspace daemon log alone (logs are shared). Exercise capture staging with private script
fixtures only, and assert it cannot rewrite A/B's roots. Record retained paths on orderly and
unknown exits, not a clean-directory claim. A real post-consumer cleanup claim remains owed to
a future lifecycle proof, **not** to this fix's acceptance.

**Report back:** actual red outputs, focused green gates, mutation results, changed files,
retained-path messages, and native evidence explicitly marked not-run or recorded. Ready for
parent-owned review and a later bounded source implementation, not native acceptance.
