---
status: accepted
stage: DECIDED
next: "Keep mapped execution refused. The prototype review is done; collect the native witness it lists (delegation, child creation and readback, placement, lifetime) on a real rootless Linux host before building the real Brief 3 writer or connecting any writer to launch; complete combined and native evidence gates before claiming enforcement."
depends-on: [resource-groups.md]
---

# Plan: owner-selected resource groups

**Status:** 2026-11-05. The Brief 1 selection and Brief 2 read-only finite-policy foundation are integrated from the accepted source lineages. A bounded fake-only required-start admission test boundary is integrated and independently HIGH-reviewed. Its exact source passed the resource-group package, race, targeted Run/host/Check consumer, and Darwin arm64 compile gates. The committed primary-source development bundle reports the matching commit; its scrubbed `check --no-build` passed with resource groups disabled, and the required rootful `podman-self` default-off nested `true` launch passed. An unactivated descriptor-relative child-writer prototype has no production caller. **2026-11-13:** its independent review is recorded under [Brief 3](#prototype-review-2026-11-13); its two confirmed major findings are fixed, and its native evidence is listed there as owed. Native delegated-unit authority, actual placement, enforcement and device evidence remain unproven; whole-project combined and native gates are distinct and remain outstanding.
**Design:** [Resource groups](resource-groups.md) controls behavior; this plan is advice, and the tree is the source of implementation facts.

## Map

| Path | Change |
| :--- | :--- |
| `internal/config/config.go`, `schema.go`, `workspacescope.go`, `validate.go`, `inherit.go` | owner-only `resource_groups` schema, validation, and no nested inheritance |
| `internal/config/resourcegroups.go` | new — canonical selection and specificity resolution; no host authority calls |
| `internal/cli/run/resourcegroups.go` | new — mapped unsupported refusal for jail and host launches |
| `internal/cli/run/run.go`, `internal/cli/hostcapabilities.go` | real run and host gates; host reuses the capability gate's existing user-config read |
| `internal/cli/check/check.go`, `internal/cli/check/resourcegroups.go` | read-only selection status before repo-root work; disabled or selected/unsupported, never enforced |
| `internal/changetiming/changetiming.go`, `internal/render/configkeys.go` | classify new owner-only key in the existing config timing and host applicability censuses |
| `internal/cli/check/golden_test.go` | pin the new resource-groups check section |
| `internal/config/resourcegroups_test.go`, `internal/cli/run/resourcegroups_test.go`, `internal/cli/check/resourcegroups_test.go`, `internal/cli/hostcapabilities_test.go`, `internal/cli/configsetresourcegroups_test.go` | resolution/scope, caller refusal, check ordering and owner-scope advice tests |
| `internal/cli/config_ref.txt`, `userguide/reference/configuration.md` | exact fields, disabled behavior and unsupported status |
| `internal/resourcegroups/iomax.go`, `resolve.go` | finite `io.max` parser/planner and selected-group read-only path resolution |
| `internal/cli/check/resourcegroups.go`, `internal/cli/run/resourcegroups.go` | actual Check and Run consumers; Check exposes the read-only plan and mapped Run refuses before prewarm |
| `internal/resourcegroups/authority_linux.go`, `authority_other.go`, `pinned_linux.go` | guarded direct-writer refusal, unsupported-platform result, and fake-only required-start admission boundary; no activated writer |
| `internal/resourcegroups/authority_linux_test.go` | fake injected admission tests; no production caller or native authority proof |
| `internal/resourcegroups/pinnedio_linux.go`, `pinnedio_linux_test.go` | unactivated descriptor-relative Linux child-writer/guard prototype and ordinary-filesystem syscall fixtures; no mapped execution |
| `internal/cli/run/` launch-boundary files, `internal/cli/host.go` | Brief 4: actual placement-witnessed execution boundaries; unbuilt |

## Reuse

- `config.LoadConfigWithSources` and `loadAndValidateConfig` compose and validate normal user plus workspace config; do not reread owner config on the ordinary run-off path.
- `config.WorkspaceUserScopeOnlyKey`, `SchemaKeys`, `ReadableSchemaKeys`, and `firstReadableTOMLViolation` carry owner-only scope through both readable TOML and legacy JSONC.
- `config.UserScopeConfigOrEmpty` is already read by the host capability gate; resource selection reuses that map rather than adding an off-path owner-config read.
- `config.Check`'s `sectionConfigFiles` merges the same config the launch sees. Report selection before repo-root resolution so the check still shows it when another preflight fails.
- `config.InheritDisposition` / `FilterInherit` are the census and filter for nested launch state; resource-group selection has no in-jail authority and must not be emitted.

## Traps

- `yolo check` records other failures before the merged-config section; a selected resource-group finding must remain visible even when repo-root resolution fails.
- Host pack refresh precedes the existing capability gate and may fetch. It is an explicit exclusion from this slice, not work claimed to be placed.
- A selected unit string and a runtime argument do not prove membership. This slice refuses mapped work; it does not add a resource argument or claim enforcement.
- A project path is a mapping key, not authority in project config. The entire root is user-scope only.
- `resource_groups.maintenance` is independent of cwd/workspace selection; no machine-wide maintenance consumer exists in this slice.

## Build tasks

### Brief 1 — owner-only selection and real consumers (source integrated; acceptance gates pending)

**Settled — do not re-open:** no owner mapping means no resource-group manager/controller/device probes, group/process changes, caps, resource-group startup work or routine launch line. A matching explicit selection is required placement; until actual placement is supported and verified, run and host launch refuse before their remaining expensive launch work with `yolo check --no-build` as the next step. Check reports `disabled` or selected identity/source as `unsupported/unknown`, never `enforced`. Only user config may set these values. A default is an explicit opt-in; no mapping is off.

- **Tests first:** `internal/config/resourcegroups_test.go` — exact workspace > nearest component-bounded project > explicit default; empty config; user-only scope; percent 1/99 accepted and 0/100/bool/fraction refused; symlink aliases refused. `internal/cli/run/resourcegroups_test.go` drives `Run`; `internal/cli/hostcapabilities_test.go` drives `hostMain`; `internal/cli/check/resourcegroups_test.go` drives `Check`. Each consumer test must fail if its production callsite is deleted.
- **Change:** schema/validation and inherited-scope census; parser/selection; refusal in `Run` immediately after normal config validation; host refusal reuses `refuseHostUnmetCapabilities`'s loaded config; check emits status before repo-root preflight.
- **Gates:** `go test -run 'Test(ResourceGroups|HostLaunchRefusesMappedResourceGroup|CheckResourceGroups)' ./internal/config ./internal/cli/run ./internal/cli/check ./internal/cli`; whole affected package tests; `just format`; `just build-dev-bundle`; absolute emitted launcher from throwaway workspace on the default-off path only.
- **Fences:** no group manager/device probes, process moves, caps, activated Linux writer, cgroup delegate/keeper/runtime-argv changes, live migration, or performance claim. `ReconcileUserIO` is a read-only gate followed by an unsupported refusal; the injected fake-I/O algorithm is not a production writer. Host pack refresh and run migration/pack refresh happen before the owner config gate and remain named exclusions.
- **Commit:** `feat(resources): expose owner-only opt-in selection and check status`.

### Brief 2 — finite-policy plan and read-only check (source integrated; acceptance gates pending)

**Settled — do not re-open:** parse assigned `io.max` by device and independent dimension; `max`, absent records/dimensions, unreadable input, and finite ancestor-only limits remain distinct and never imply an assigned finite allowance. For each finite assigned allowance `A`, split `floor(A*s/100)` to agent and the remainder to batch (`s=5` default; owner override 1..99); both must be positive, finite and representable. Never invent a cap for an unconstrained dimension.

- **Tests first:** `TestCheckResourceGroupsFiniteAssignedPlan` calls `Check` with a fake read-only observer and asserts `101 rbps -> 5/96`, `20 wbps -> 1/19`, unconstrained `riops`, and refusal for `wiops=1`; include max, missing, unreadable, ancestor-only, duplicate/wrong device, stale readback and max integer cases. `TestMappedLaunchUsesResourceGroupPlanBeforePrewarm` drives `Run` and proves parse/authorization refusal precedes capture, prewarm, image and runtime hooks.
- **Change:** add pure parser/planner under `internal/resourcegroups`; inject one read-only authority/observer into the actual check and launch consumers. Keep the same plan object for later apply; no manager probes on the off path.
- **Gates:** red/green consumer tests plus `go test ./internal/resourcegroups ./internal/cli/check ./internal/cli/run`, `just format`, and `just build-dev-bundle`.
- **Fences:** no cgroup writes or `enforced` status; no host-wide arbitrary device scan. `ReconcileUserIO` performs only a read-only cgroup gate and then refuses because native delegated authority and placement remain unverified; it does not write. The injected `ReconcileDelegatedIO` algorithm and its fake-writer tests do not establish an activated writer or enforcement.
- **Commit:** `feat(resources): surface finite assigned IO plans without enforcement`.

### Brief 3 — least-authority Linux adapter (unactivated descriptor-relative prototype; writer/placement remain unbuilt)

**Integrated fake-only increment:** `ReconcileDelegatedIOAndStart` composes the injected reconciliation algorithm with a fresh observed-member check and an injected start callback under the assigned-group lock. Its tests cover unobserved/wrong-child/stale membership, missing delegation or finite budget, manager/policy drift, valid admission, concurrent shared-child reuse, cancellation, and no parent/sibling writes. An independent HIGH review accepted this bounded test/algorithm slice. The exact source passed the package, race, focused production-consumer, and Darwin compile gates. The committed primary-source bundle/check and rootful default-off nested smoke passed. These callbacks are fake only; the adapter has no production callers and establishes no native authority or placement.

**Current boundary:** the integrated `ReconcileUserIO` performs a read-only gate, then refuses native mutation. `ReconcileDelegatedIO` and `ReconcileDelegatedIOAndStart` are not wired to a production writer or launch caller. An unactivated [`pinnedIOChildren`](../../internal/resourcegroups/pinnedio_linux.go) prototype uses descriptor-relative child writes gated on observed user-manager delegation and cgroup-v2 checks; fixture tests do not establish native delegation, lifetime, safe allocation, or placement. The [native authority investigation](../research/resource-groups-native-authority.md) and independent review show upstream cgroup v2 disallows rename, whereas deletion/recreation and owner revocation still need identity checks. This is not implementation of the real Brief 3 writer and must not be wired to mapped starts before native evidence.

#### Prototype review (2026-11-13)

An independent review at high scrutiny of [`pinnedio_linux.go`](../../internal/resourcegroups/pinnedio_linux.go), [`pinned_linux.go`](../../internal/resourcegroups/pinned_linux.go) and their tests. It covered correctness, descriptor lifetime and ownership, symlink and race safety, error paths, and whether each check's tests fail when its call site is deleted. Mapped execution stays refused. The prototype still has no production caller.

**Confirmed and fixed** (failing tests first, in the commit `fix(resources): close the pinned I/O prototype's unit bypass and membership race`):

- **R1 (major): an empty unit was a mode that skipped the checks.** `writeChildPolicy` and `verifyPinnedStart` ran the selected-unit re-observation, the assigned-policy reread and the child readback only when the unit field was non-empty. They also called the native observer directly instead of the constructor's `pinnedIOUnitObserver` seam. So no fixture could reach those checks, and every fixture test ran with them off. Deleting any of those call sites left the suite green. Now an empty unit refuses. Every observation goes through the seam. The policy descriptor check is a fail-closed field: nil means the production cgroup-v2 check, and only regular-file fixtures relax it. New tests pin each call site: `TestPinnedIOChildrenRefuseWithoutSelectedUnit`, `TestPinnedIOStartReobservesUnitPolicyAndReadbackBeforeMembership`, `TestPinnedIOWriteChecksUnitAndAllocationBeforeWriting` and `TestPinnedIOWriteRefusesDifferingReadback`. The old fixture tests now run on the same selected-unit fixture.
- **R2 (major): child identity was checked before membership was observed.** `verifyPinnedStart` verified the pinned children and then observed the PID's cgroup by name. A child removed and recreated during that observation was admitted. Both pinned identities are now verified again after the observation. A cgroup that holds a live member cannot be removed, so this ties the observed PID to the pinned child. Test: `TestPinnedIOStartRechecksChildIdentityAfterObservedMembership`.

**Recorded, not fixed** (minor; for the real Brief 3 writer, not blockers for the prototype):

- **R3:** the assigned `io.max` and the parent's `cgroup.controllers` and `cgroup.subtree_control` are read by pathname (`ReadAndPlanIO`, `CheckDelegatedIO`), not through the pinned parent descriptor. The prototype brackets them only with `verifyPath` checks. In the real writer, read them with `readPolicyAt` on the parent descriptor. This race is in the same-UID owner-revocation class the design already excludes.
- **R4:** `pinnedGroup.close` and `pinnedIOChildren.close` are not idempotent. A second close could close a reused descriptor number. Today each has a single caller.
- **R5:** a refusal after `openPinnedIOChildren` creates a child leaves that child in place. This follows the rule to retain uncertain children, and it is not a defect.

**Checked and sound:** every directory step uses `openat` with `O_NOFOLLOW|O_DIRECTORY|O_CLOEXEC` from `/`, and `..` is refused. Child names are restricted to `yolo-agent` and `yolo-batch`, so `.control` and siblings are never opened. Policy writes and readbacks open `io.max` relative to the pinned child descriptor, never by pathname. Every error path closes the descriptors it opened. The lock file is created with `O_NOFOLLOW` in a private, owner-checked root, and it is verified to be a regular file the user owns.

**Owed: native evidence.** This needs a real rootless Linux host. A jail's cgroup namespace and a rootful nested jail cannot show any of it. Each item below is a witness to collect on the host, after the read-only witness in the [native authority investigation](../research/resource-groups-native-authority.md):

1. **Delegation and the no-internal-process rule.** `CheckDelegatedIO` requires `io` to be already enabled in the unit cgroup's `cgroup.subtree_control`, and nothing writes it. Cgroup v2 allows that only when the unit's own processes are in a subgroup (for example, systemd's `DelegateSubgroup=`). Witness: for the chosen unit, `systemctl --user show -p Delegate,DelegateControllers,DelegateSubgroup,ControlGroup,ActiveState`, plus the unit cgroup's `cgroup.controllers`, `cgroup.subtree_control` and `cgroup.procs`, and the names of its existing children.
2. **Child creation and readback.** `mkdirat` of both children succeeds under the delegated cgroup, and the children are owned by the user. Each child has an `io.max`. A write through the pinned descriptor reads back as written. This step needs owner authorization because it writes.
3. **Placement.** Nothing moves a process yet. A move into `yolo-agent` needs write access to the destination's `cgroup.procs` and to the common ancestor's `cgroup.procs`. Witness: `/proc/<pid>/cgroup` after the move, and the refusal when the common ancestor is not delegated.
4. **Lifetime.** Record what happens when the unit stops while a child holds a live member, when an empty child is removed and recreated, and when a write goes through a descriptor whose cgroup was removed. Also confirm on that host kernel that a rename of a child is refused.

**Settled — do not re-open:** only existing verified user-manager delegated rights or an authorized manager-owned child route may place work. Never write owner parents or unrelated siblings, elevate privilege, widen the jail delegate protocol, move jobs between owner groups, or delete active/uncertain Yolo children. Without verified authority/membership, mapped starts refuse as unsupported/unknown.

- **Tests first:** fake authority and membership tests cover missing I/O delegation, permission denial, no parent/sibling writes, finite-to-unknown transitions, concurrent reuse of one shared child pair, parent change races, reconnect/crash retention, and a requested argv with wrong observed PID membership refusing before startup work.
- **Change:** Linux-only adapter plus non-Linux unsupported implementation, narrowly wired into run/keeper assembly while preserving existing jail CPU/memory/PID lifecycle and limits. Lock/reconcile is group-wide; reread policy and verify placement before required start.
- **Gates:** focused fake-authority tests and package gates before any native fixture; real-host evidence later is scoped to observed host authority only.
- **Fences:** no host policy changes, parent rewrite, raw jail cgroup bind, privileged helper, daemon, or cleanup of uncertain live state.
- **Commit:** `feat(resources): reconcile shared Linux children under verified authority`.

### Brief 4 — verified execution-boundary coverage (after Brief 3)

**Settled — do not re-open:** charge work only when its actual process/runtime membership is witnessed before expensive work. A requested container parent or keeper scope return code is not proof. Attach reuses the running jail and does not create a second allowance. Nix builders/shared services and every unverified nested/external boundary are explicitly excluded.

- **Tests first:** each consumer gets a real-callsite fake test that deletes the placement callsite to fail and proves refusal before capture/prewarm/host preparation; verify fresh, attach, host exec, keeper, runtime/conmon and nested workload membership where supported.
- **Change:** place hooks before owned startup work, not after final argv assembly; keep unsupported boundaries visible and preserve jail lifetime semantics.
- **Gates:** focused package gates per boundary; combined launch-path landing runs the integration suite once.
- **Fences:** no guessed PID/name/TTY classifier, agent registry, post-start sweep, late `--cgroup-parent` claim, or moving tools outside existing jail quotas.
- **Commit:** separate conventional commits per disjoint consumer boundary; do not combine unverified boundaries under one success claim.

## Ships with

- Brief 1 config keys: `[resource_groups]`, `default`, `maintenance`, `agent_percent`, `[resource_groups.workspaces]`, `[resource_groups.projects]`; paths are quoted canonical absolute paths and unit names are owner references.
- Brief 1 selection status: no routine launch banner; `yolo check --no-build` says disabled or names selection/source and unsupported/unknown; mapped launch refusal says the mapping and check command.
- Documentation: `internal/cli/config_ref.txt` and `userguide/reference/configuration.md`; no changelog entry and no `BUILT` claim in the accepted design until evidence warrants it.
- The following briefs' status surfaces and proofs are listed only as future work; no authority, throughput, device accounting or responsiveness claim ships in Brief 1.

## Don't

- Do not add `enabled=true`; an explicit default mapping is the opt-in.
- Do not let a workspace/project TOML file choose a host group or override an owner mapping.
- Do not infer a workspace group for machine-wide maintenance from cwd.
- Do not stage, move, cap, or report `enforced` for any selected unit in Briefs 1 or 2.
- Stop and report on a missing named seam, unexpectedly green/red callsite test, scope-validation gap, out-of-fence file, or unrelated gate failure; do not resolve it by widening scope.
