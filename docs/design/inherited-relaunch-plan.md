---
title: "Inherited account relaunch implementation plan"
status: accepted
stage: BUILT
next: "Run the inherited-scope macos-user integration witnesses natively on macOS before claiming runtime verification"
---

# Inherited account relaunch

Written against `8c0bc42f3a72e19b9f27a06f33b1fdf4a63c4679`, 2026-10-29. Main now contains the reviewed bounded repair.
Planner reproduced the reported JSONC refusal through two production Run calls and the real installer.
Behavior authority: the lane A owner-directed unchanged prior-delivery slice recorded below;
background contract: [inherited scope](../reference/loophole-system.md) and
[account home tiers](../reference/macos-user-home-tiers.md).
Precedence: settled behavior wins; the tree wins on cosmetic facts; this plan is advisory on structure.

## Settled behavior — do not re-open

**Constraint:** admit ONLY an unchanged prior-delivered hardlink snapshot. Preserve its inode,
exact bytes and sealed `0444` mode through the second Run and installer. No refresh or ownership claim.
Independent matching user inodes, copies, edited bodies/modes, links and ambiguous siblings remain
refused and preserved. No writable receipt, generated header or digest-only comparison proves ownership.
After Run admission, the installer must also preserve an owner replacement arriving at the effect
boundary: no-write reuse requires unchanged sealed-source identity; otherwise create atomically with
no-replace semantics and refuse if any destination appeared. Do not displace any late occupant.

**Amended for issue #70 (owner ruling, 2026-11-02):** a delivery the installer had to COPY (its
hard link refused, as the run-as-you Seatbelt profile is believed to do) has an inode of its own,
and the inode-only rule above refused every relaunch after one. A prior delivery is now also
admitted by a DELIVERY RECORD the host writes before staging: sealed `0444`, at
`<staging root>/inherited-delivered/<cname>.json` beside the staged overlay
(`entrypoint.InheritedDeliveryRecordPath`), never in the agent-writable sidecar. It lists each
destination's sha256 content digests. A regular, sealed `0444` destination owned by the expected
account whose digest the record lists is admitted, linked or copied, at both the Run caller and the
installer; a changed body replaces it only when the record lists the bytes being replaced, through
a move aside checked against the hashed file. A header match, matching bytes with no record, and
a hardlink whose changed bytes no record lists are still refused. The installer's copy fallback is
written to the boot log.

## Map and reuse

| Path | Change |
| :--- | :--- |
| [inheritscope.go](../../internal/cli/run/inheritscope.go) | Native selector admission, validated sealed-source lookup, preserve delivered paths in stale cleanup |
| [inheritscope_relaunch_test.go](../../internal/cli/run/inheritscope_relaunch_test.go) | New permanent production Run regression; extend with negative admission cases |
| [darwinoverlay.go](../../internal/entrypoint/darwinoverlay.go) | Inherited config destination no-replace creation/refusal at install effect boundary |
| [inheritedrelaunch_overlay_test.go](../../internal/entrypoint/inheritedrelaunch_overlay_test.go) | New permanent installer late-replacement and creation-race witnesses |

- Reuse `dispatchOptions`, `fakeMacosUserRun`, `overlayFileInfo` and `treeLaunchHome` from
  [dispatch fixtures](../../internal/cli/run/packstagedispatch_test.go),
  [backend fixture](../../internal/cli/run/housekeepingmacosuser_test.go),
  [overlay reuse tests](../../internal/cli/run/macoshomeoverlayreuse_test.go) and
  [patched-tree fixture](../../internal/cli/run/patchedtrees_test.go).
- Reuse real `DarwinEnvFrom` / `InstallHomeOverlay` as in
  [installer fixture](../../internal/cli/run/macoshomeoverlay_test.go), with selected synthetic packs.
- Reuse `StageHomeOverlayCommands`, `HomeOverlayStagedCurrent` and `StagedHomeOverlay` in
  [macosuser.go](../../internal/macosuser/macosuser.go); stable private sealed source across launches.
- Reuse `lstatHomeFile`, `selectInheritedJSONCRoute`, existing consumer-filtered rendering and
  exact family ambiguity checks in [inheritscope.go](../../internal/cli/run/inheritscope.go).

## Implementation constraints — retained from the approved plan

**Constraint:** limit production changes to the native inherited-scope path in
[inheritscope.go](../../internal/cli/run/inheritscope.go) and inherited-core installation in
[darwinoverlay.go](../../internal/entrypoint/darwinoverlay.go). The separate installer regression is
[inheritedrelaunch_overlay_test.go](../../internal/entrypoint/inheritedrelaunch_overlay_test.go).
No generic skill/file behavior change, generic selector signature change, container route change,
new manifest, environment/config authority or shared Run edit. Preserve generic skills/files behavior.

**Advice:** a tiny package-private staging-root lookup defaults to `macosuser.StagingRoot`;
 derive the sealed source using `StagedHomeOverlay(runtime.FromWorkspace(ws), root)`.
This lets the permanent test route only its private synthetic root without touching global Mac state.
The regression currently has no production hook; wire it to that lookup when introducing it.
Use the workspace-derived session instead of its current synthetic session then.

**Constraint:** the admission query validates root facts through existing
`Options.MacosUserLaunchProbes`, `StagingRootFacts` and `JudgeStagingRoot` in
[privilege.go](../../internal/macosuser/privilege.go), using the caller euid and real default probes.
A missing/foreign/unsafe/unknown root fails closed. Never infer trust from Run preflight:
its failure can skip build and still leave inheritance before the backend's eventual refusal.

**Constraint:** no-follow every source and destination parent using contained root reads.
Require real sealed source directories `0555`, regular source/destination files `0444`,
caller-owned source where existing metadata exposes ownership, and `os.SameFile` identity.
Compare exact freshly rendered unstamped bytes even for the same inode: the host can edit/chmod both links.
Keep JSON/JSONC/TOML ambiguity checks and the existing safe next-step refusal.

**Advice:** keep the selector native-only: establish candidate identity, select the existing family
using the existing route helper, then compare returned rendered files before cleanup.
This avoids a second renderer and changing container consumers. Factoring is cheap; safety is mandatory.

**Constraint:** the installer recognizes only the inherited core config destinations. If a destination
is already the exact staged source hardlink with the inherited `0444` type/mode invariants, perform no
write; otherwise a present destination is refused. For an absent destination, stage beside it and use
`renameNoReplaceBetweenAt` with directory handles (`root.Open(".")`); an entry created/replaced after
inspection must make the atomic create fail without touching it. Do not apply this policy to skills or
other overlay files. Reuse the existing Linux/Darwin no-replace helper; no new syscall layer.

**Tests:** preserve the end-to-end second-Run GREEN; make late replacement a permanent production caller
test. Add installer tests for late replacement and creation at the staged hook boundary, plus caller
refusal/preservation of independent matching owner inode. Generic overlay tests must remain unchanged.

### Tests and gates

Baseline RED command (exit 1):

```bash
go test -count=1 -run '^TestMacosUserAccountRelaunchKeepsDeliveredInheritedJSONC$' ./internal/cli/run
```

Observed: first real install sealed/hardlinked the snapshot; second Run returned 1 with only one
backend callback, reporting `refusing macos-user inherited preflight scope` and
`could overwrite existing user file` at the sidecar config JSONC destination.
Delivered bytes, mode and inode remained intact. This is Linux synthetic caller proof, not native execution.

The retained Run and installer regressions cover independent matching copies despite missing, corrupt or forged
real install records; changed-mode hardlinks; header-preserving edits at the production Run caller; destination and
parent links; unsealed/foreign staging roots; installer refusal of independent files, links, modes and types; and
atomic no-replace creation at the staged hook boundary. The unchanged sealed hardlink remains a no-write reuse.
JSONC/TOML ambiguity, missing/unsafe staging roots, dry-run behavior and cross-volume copied delivery are not
claimed as covered or fixed by this bounded slice.

```bash
go test -short ./internal/cli/run ./internal/entrypoint
go test -count=1 -run '^(TestMacosUserInheritedUserScopeArrivesReadOnly|TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills)$' ./integration
python3 scripts/completion-census.py
```

The [native integration witnesses](../../integration/macosusercontent_test.go) and
[overlay-state witness](../../integration/macosuseroverlaystate_test.go) were run on Linux and each skipped because
macos-user requires Darwin; this is not native success. The original Linux candidate short-package gate passed.
The historical baseline RED command and output above remain labeled as the baseline artifact. After the regression
went green, `python3 scripts/completion-census.py` updated `scripts/completion-readers.json`; Root regenerated it on
the final combined tree. Shared Go caches were unchanged.

**Implemented boundary:** Main contains the bounded production repair; tests establish synthetic Linux Run-caller
and installer effect-boundary behavior only. Native macOS execution and cross-volume copy fallback remain unmeasured.
No copied-delivery refresh, marker-only legacy cleanup, or wider ownership claim is made.

**Separate test-fixture follow-up:** One concurrent final-tree completion-census run failed in the existing
`internal/cli/run.TestAHealthyMintPrintsNoLaunchCheckLine/podman`: under 16-way load, its canned healthy cold mint
had not completed within the production two-second launch-check budget, while the fixture's `calls == 1` check
remained intact. After the isolated concurrent census workers stopped, the unchanged census command passed. That
rerun does not fix or eliminate this load-sensitive fixture assumption. Keep the actual `startLoopholesDisclosed`
caller, cold-mint and `calls == 1` assertions, and production budget unchanged; track a separate deterministic-fixture
repair. This is not a defect in the inherited-scope source fence.

**Stop:** unexpected RED mechanism, dirty owned file, missing named symbol, required production
change outside the fence, or unrelated failing gate: report rather than expand scope.

Planning commit: `test(run): reproduce inherited account scope relaunch refusal`.
Implementation evidence is recorded in the lane's external evidence receipt. No agents, login, API, keychain or
owner-session actions were performed.

## Historical RED provenance and current regressions

The original RED command and observed failure are recorded above. The full function-body excerpt once
kept here was a historical pre-fix specimen, not the current permanent test; it is removed to avoid
presenting stale code as current coverage. The permanent production caller regression is
[`TestMacosUserAccountRelaunchKeepsDeliveredInheritedJSONC`](../../internal/cli/run/inheritscope_relaunch_test.go),
and current installer refusal, reuse and staged-boundary regressions are in
[`inheritedrelaunch_overlay_test.go`](../../internal/entrypoint/inheritedrelaunch_overlay_test.go).
