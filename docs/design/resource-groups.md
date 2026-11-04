---
status: in-review
stage: DESIGN
date: 2026-11-04
next: "Obtain the owner ruling on OQ-RG1 (initial backend coverage), then validate Linux placement and delegation before accepting this design."
---

# Owner-managed resource groups can bound combined workspace work only when placement and enforcement are proven

**Status:** 2026-11-04. Initial design; no implementation approved or built. Existing-source references were checked at `5a361ba5826acbf120280180c9f918dbc1748c89`; the reported incident was not independently measured, and Linux placement and device behavior have **not** been demonstrated.

> **In short.** The host owner persists named resource groups and their limits; Yolo selects a group for a workspace and places its work there *before startup*. Where the owner delegates a finite allowance, Yolo may maintain two shared, hard-capped children beneath that group. A configured mapping is never reported as an enforced budget without evidence of actual process placement, available controllers, and host policy.

**Why now:** The request reports substantial disk traffic from a Forms import and a nearly continuously busy SSD during desktop stalls. Disk contention is a plausible contributor, not a proven cause of any particular Brave freeze. Per-container limits and process I/O priority do not establish a shared host I/O ceiling across launches.

**Cost:** Owners must establish persistent groups, policy, and the authority needed for placement. Strict placement can prevent a launch when that setup is absent. Fixed child ceilings may leave capacity unused; this design adds neither a monitoring daemon nor adaptive borrowing.

**Start:** Research the Linux authority and placement paths in [§7](#7-decisions-research-gates-and-routing); settle [OQ-RG1](#OQ-RG1), [OQ-RG2](#OQ-RG2), [OQ-RG3](#OQ-RG3), and [OQ-RG4](#OQ-RG4). No implementation-plan handoff precedes design acceptance.

**Needs your ruling:** [OQ-RG1](#OQ-RG1) · [OQ-RG2](#OQ-RG2) · [OQ-RG3](#OQ-RG3) · [OQ-RG4](#OQ-RG4).

**Read with:** [I/O-priority design](io-priority.md) (existing, in review), [cgroup-delegate reference](../reference/security-shim.md) (existing), [roadmap](../plans/roadmap.md) (routes this design's live questions), [Linux cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [systemd slices](https://www.freedesktop.org/software/systemd/man/latest/systemd.slice.html), and [systemd resource control](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html).

## 1. Boundary and evidence

A **resource group** here means a host-managed collection of processes under hierarchical resource controls, not a Unix account group. An **assigned group** is the owner-selected group into which Yolo is authorized to place a workspace. A **finite allowance** is an explicit, readable, device-and-dimension-specific upper limit on that assigned group which the owner authorizes Yolo to subdivide. An **agent child** and **batch child** are two reusable subordinate groups for human-facing agent execution and expensive tool/background execution, respectively. A **required placement** prohibits starting the affected work if its selected group cannot actually be used.

On Linux, the intended substrate is [cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), with persistent owner-managed [systemd slices](https://www.freedesktop.org/software/systemd/man/latest/systemd.slice.html) where feasible. An aggregate ceiling constrains combined descendant traffic: it is neither a guaranteed minimum, a desktop-latency guarantee, nor a per-session grant. Owners may arrange independent project groups, several projects under one limited parent, and non-Yolo batch work under that parent. Parent limits apply to the aggregate, including non-Yolo descendants.

The request reports that a Forms import accumulated approximately **4.4 GiB read and 3.8 GiB written** in 30 seconds of kernel accounting; a later sample showed the main SSD **99.8–100% busy for three consecutive seconds**, with writes peaking near **1.7 GB/s**. It also reports encrypted Btrfs/NVMe with Kyber and a user manager lacking I/O-controller access. These are **request-reported observations**, not independently repeated measurements, a general hardware profile, or proof of end-user latency improvement.

### Existing controls and their insufficiency

| Existing mechanism | Checked scope | Why it does not settle this design |
|---|---|---|
| [`appliedResourceLimits`](../../internal/cli/run/backendcaps.go) | Assembles backend-specific container memory, CPU, and PID limits. | It does not assemble an aggregate host I/O cap shared across Yolo launches. |
| [`resources.io`](../../internal/ioprio/ioprio.go) and its [launch handling](../../internal/cli/run/iopriority.go) | Process I/O priority, disclosed or warned about by supported launch paths. | Priority is not a shared bandwidth ceiling; buffered writeback and Kyber limit its applicability. |
| [I/O-priority design](io-priority.md) | Excludes bandwidth caps in [§2](io-priority.md#2-non-goals) and leaves [OQ-IO7](io-priority.md#OQ-IO7) about a per-container cgroup control open. | That question does not decide the requested shared `io.max` budget. |
| [Cgroup delegate](../reference/security-shim.md) and [`internal/cgd/ops.go`](../../internal/cgd/ops.go) | Jail-scoped child operations for CPU, memory, and PIDs. | It neither grants host-group placement nor establishes delegated host I/O authority. Expanding its scope would change its security boundary. |
| [Host execution path](../../internal/cli/host.go) | Host-mode agent execution ultimately reaches `hostSyscallExec`. | It establishes a path requiring placement, not evidence that this path is already placed. |

These are bounded checks, **not** an exhaustive claim that no other component can affect I/O. The [I/O-priority design](io-priority.md) also identifies host Nix builders as processes of the host daemon, outside the jail process tree. Placing their client cannot be represented as placing the builders.

## 2. Ownership and placement architecture

### Host policy and Yolo selection

The owner persists named groups, hierarchy, and policy for I/O bandwidth, CPU, memory, and other host resource controls through the host's normal resource-control machinery. Yolo stores selection intent—not a duplicate copy of those limits: a default workspace mapping, more-specific workspace/project mappings, and the owner-designated group for machine-wide maintenance. The proposed resolution order is an exact workspace mapping, then a project mapping, then the default; conflicting mappings at the same specificity must be rejected rather than resolved by incidental ordering. The selected identity and source of selection must be displayed before work starts. Configuration spelling and the way the owner authorizes mappings remain open.

Yolo must not rewrite an owner-managed parent limit, create an unlimited replacement, move work around a required ancestor, or remove an owner-managed slice during cleanup. Host policy changes ordinarily act on existing members through the kernel without relaunching Yolo. A mapping change is a distinct selection event; whether it affects already-running work is [OQ-RG4](#OQ-RG4).

The placement transaction is: resolve selection; validate group identity, hierarchy, permissions, and required controllers; establish or reuse any authorized children; place the execution boundary; verify observable membership and policy; only then permit expensive workspace startup. This applies to image import, extraction, provisioning, and other Yolo-owned startup work, not just the eventual agent. Machine-wide maintenance uses its separately owner-configured group rather than arbitrarily charging one workspace. If a runtime performs work in an external daemon, client placement alone is insufficient and that work is disclosed as excluded or unverified.

### Authority is not implied by a slice name

A system-manager slice, a user-manager slice, a delegated subtree, and a rootless container runtime can have different authority despite similar names. The Linux path is conditional on verifying every relevant ancestor, controller availability, systemd delegation, runtime cgroup-parent behavior, and permission to create or join the intended units. The request's reported lack of user-manager I/O delegation on one host is a setup obstacle to investigate, not a universal Linux property. Yolo must not quietly gain authority over unrelated sibling groups to make placement work.

The existing jail-scoped cgroup delegate is **not** presumed to provide that authority. Any privileged placement or subdivision route must have a bounded authorization model and be reviewed as a security-boundary change before adoption. This design does not authorize a host helper, broader delegate protocol, or host policy change.

## 3. Shared child ceilings

Where the owner permits subdivision, each assigned group has one shared agent child and one shared batch child, reused by all its launches. They are **not** created once per session. Work classification occurs at the actual agent, tool, managed-background-job, and container execution boundaries; terminal attachment, foreground focus, and guessed PIDs do not classify work. A heavy operation performed *within* the agent process cannot be split into a separate child by classification alone. A heavy job deliberately placed in the agent child still faces that child's aggregate ceiling and its parent's ceiling.

For every controlled physical device and each configured dimension—read bandwidth, write bandwidth, read IOPS, and write IOPS considered independently—successful allocation must satisfy:

`agent-child ceiling + batch-child ceiling ≤ finite assigned allowance`.

Allocation does not use relative weights in place of hard ceilings. The request's 900 MiB/s parent and 20/880 MiB/s child split illustrates arithmetic, **not** a default. How an owner supplies or authorizes the division without turning Yolo into a duplicate top-level policy store is [OQ-RG2](#OQ-RG2).

A finite ancestor ceiling is an additional upper bound, **not** an entitlement to its full capacity: siblings, including non-Yolo jobs, may consume it. Subdivision requires an explicit finite allowance on the assigned Yolo group; an ancestor-only cap, an unlimited value, or unreadable policy cannot be silently converted into one. When an ancestor imposes a tighter bound, effective traffic remains subject to that bound, but Yolo must not present it as capacity reserved for its children. Device identity and effective bounds must be validated across the relevant hierarchy. An unconstrained dimension does not acquire an invented cap; if the required split cannot be established for a controlled dimension, setup fails with an actionable explanation.

Concurrent launchers must serialize child creation and refresh through a bounded one-writer operation, using suitable locking or systemd unit operations if validated. On retry, they inspect and reuse the same children rather than multiplying allocations. No resident Yolo policy coordinator is introduced.

A lower owner parent cap is enforced immediately on existing descendants **if that controller and placement are actually effective**, even before Yolo refreshes the old child settings. During that interval, programmed child ceilings might sum above the *new* parent allowance; status must say **stale child allocation, parent still caps**, not assert the subdivision invariant currently holds. A successful subsequent launch or explicit refresh recomputes and reconciles children before starting new work. A parent increase may leave bandwidth unused until then. If the parent becomes unlimited or unreadable, no new finite allowance is inferred; existing work remains under whatever host policy can be observed, and required new starts cannot rely on a fresh split. Refresh failure must not raise the parent, fabricate an allowance, or start new work under an unverified required allocation. Idle agent capacity is not automatically loaned to batch work.

### Workload coverage and lifecycle

Participation comes from launch/runtime placement; it must not depend on users prefixing individual build commands with wrappers.

| Workload | Required design treatment | Proof boundary |
|---|---|---|
| Simultaneous jails, attached sessions, agent subprocesses, subagents, and managed background jobs | Select once per workspace and place every actual execution boundary in the assigned hierarchy; agent versus batch follows the execution hook. | Inspect membership of the resulting process or runtime group, including after attachment and reconnect. An attachment alone is not a new budget. |
| Nested application containers | Integrate with the container runtime's supported cgroup-parent/placement route and verify the resulting hierarchy. | A requested parent argument does not prove that the runtime and its workload joined it. |
| Host-mode agents and host-side startup helpers | Establish placement before execution, including the path that replaces the launcher with `hostSyscallExec`. | Verify descendants inherit the intended scope; a post-start process sweep is not an acceptable substitute. |
| Image import, extraction, provisioning, and workspace maintenance | Charge workspace-specific work to the selected group before expensive execution; use the separately configured maintenance group for machine-wide work. | Expose work done inside runtime or service daemons as separate coverage claims. |
| Host Nix builders and other shared-service work | Attribute individual Yolo-triggered service work where a supported, authorized mechanism genuinely permits it. | Otherwise report it **outside group**; never throttle the shared Nix daemon globally for all users by implication. |
| Non-Yolo batch jobs and desktop apps | Owner may place other batch jobs beneath a common parent; unrelated desktop apps remain outside restricted groups. | Yolo observes rather than claims ownership of those processes. |

Initial backend coverage is [OQ-RG1](#OQ-RG1). A backend lacking aggregate control cannot label advisory priority or per-container limits as equivalent. Unsupported and unverified paths need a visible setup or limitation message, not a silent unrestricted fallback.

Children are Yolo-managed subordinate state, while parent slices remain owner-managed. On restart or crash, a launcher reconciles existing children and live membership instead of assuming a clean slate. Reconnect does not create another allowance. Cleanup may remove only Yolo-owned children after confirming no active jobs depend on them; uncertain membership favors retention and an explicit report over stranding work. No custom poller retrofits escaped processes after launch.

## 5. Observable state and failure contract

A launch preview and status/check surface are part of this feature. For each workspace they distinguish **requested mapping**, **resolved owner group**, **actual observed placement**, **controller/device policy**, **effective known ancestor bounds**, **child allocation and freshness**, and **known exclusions**. Participating workloads are shown with the evidence used to classify them; external-service and runtime-daemon traffic is not counted as contained merely because its initiating client is contained.

| State | Operator-visible behavior |
|---|---|
| Unknown or empty group; invalid, ambiguous, or forbidden mapping | Reject selection with the offending mapping and owner setup action; do not substitute an unlimited group. |
| Group exists but permission, ancestor, controller, delegation, or device-policy proof is missing | Report the precise unavailable observation as **unknown/unsupported** rather than zero or enforced. Required placement blocks before expensive startup. |
| Placement request accepted but membership not demonstrated | Report **placement unverified**, not success. Required work does not proceed on that assertion alone. |
| Concurrent creation, owner update, or group removal races with startup | Re-read identity, policy, and membership at the commitment boundary; reconcile under the one-writer operation or fail closed for required work. |
| Mapping changes while jobs run | Show each running job's actual attachment separately from the new mapping; do not equate new intent with migration. Resolution awaits [OQ-RG4](#OQ-RG4). |
| Parent changes while child allocation is stale | Show the live observed parent policy and programmed child ceilings separately; reconcile on the next permitted launch or explicit refresh. |
| Reconnect or crash leaves jobs alive | Discover and report actual surviving membership; do not delete their groups or claim they moved. |
| Coverage passes into a shared daemon | Report that portion as excluded or unverified, with the attribution gap; do not apply a global daemon cap. |

Host policy is read from the host, not copied into Yolo as another authoritative limit store. A successful check can establish placement, configured controller values, and observed throttling counters where available. It cannot by itself guarantee desktop responsiveness, a precise physical-device rate, or complete buffered-write attribution.

## 6. Acceptance outcomes

The design is acceptable for implementation planning only after the OQs close and the technical gates in [§7](#7-decisions-research-gates-and-routing) identify a feasible authority path. Subsequent operator verification must retain all of these outcomes:

1. Persist a default and workspace-specific mappings across launcher restarts without a Yolo monitoring daemon; show the resolved and actual groups separately.
2. Run two simultaneous workspaces in one owner group and verify their **combined** sustained per-device traffic against its budget within a documented accounting tolerance. Adding sessions must not multiply it.
3. Exercise distinct project children beneath a limited parent, plus non-Yolo batch work there; observe both project limits and their common aggregate parent bound.
4. Reuse one pair of agent/batch children across sessions. For each finite controlled device/dimension, verify explicit ceilings and their sum; verify a deliberately heavy agent-child job remains bounded, without substituting weights.
5. Exercise finite, unlimited, unreadable, and ancestor-with-siblings cases. Decrease and increase a parent cap with work running; distinguish immediate parent enforcement from stale or refreshed children without a watcher.
6. Change systemd policy while jobs run and verify effective behavior changes without restarting them or editing a second Yolo policy store.
7. Exercise buffered writes, reads, provisioning, host-mode descendants, and nested containers; list actual external-service coverage gaps.
8. On a representative encrypted Btrfs/NVMe host, observe kernel-reported throttling and compare desktop responsiveness against unrestricted and several higher-cap baselines. Choose any throughput value from **machine-specific latency and batch-throughput evidence**, not the earlier suggested 200–300 MiB/s figure.
9. Confirm unrelated desktop work remains outside the restricted hierarchy and can use available device capacity.
10. Exercise unknown names, missing controllers or delegation, simultaneous launches, mapping changes, reconnects, crashes, and cleanup; neither silently escape a required budget nor delete an owner slice or strand active jobs.

These are outcomes, not claims that the current host passes them. `io.max` is a rate control, not a latency service-level objective. Encryption, Btrfs writeback and metadata, shared inodes, device mapping, and competing ancestor siblings complicate attribution; IOPS controls and prioritization should be evaluated experimentally without changing the host scheduler implicitly.

## 7. Decisions, research gates, and routing

### Decision ledger

| ID | Current position | Authority / remaining step |
|---|---|---|
| D-RG1 | Owner-managed persistent hierarchy and policy; Yolo resolves and places work. No custom monitoring daemon or top-level policy takeover. | Requested architecture; validate an authorized Linux placement route. |
| D-RG2 | Delegated subdivision uses two **shared hard-capped** children and an explicit finite assigned allowance per controlled device/dimension. No weights-as-caps, invented default, or per-launch multiplication. | Requested contract; [OQ-RG2](#OQ-RG2) settles allocation authority. |
| D-RG3 | Selected mapping is not evidence of placement; unknown controller, membership, or external-service coverage remains visibly unknown. | Design invariant; prove each backend's observability. |
| D-RG4 | Parent host-policy changes affect existing properly attached work independently of mapping refresh; child refresh is launch-time or explicit. | Kernel hierarchy principle conditional on actual enforcement; validate the chosen host path. |
| D-RG5 | No automatic global limit on the shared host Nix daemon. | Requested exclusion; investigate per-build attribution separately. |
| D-RG6 | No implementation or platform promise is approved by this initial design. | [OQ-RG1](#OQ-RG1), [OQ-RG3](#OQ-RG3), and [OQ-RG4](#OQ-RG4) remain open. |

### Engineering research before acceptance

| Gate | Evidence needed; do not assume success |
|---|---|
| Linux authority | Determine a least-authority route for rootless Yolo to select owner groups and create/reuse delegated children; inspect system versus user manager, `Delegate=`, every ancestor controller and subtree state, and whether privileged setup is necessary. Establish that unrelated sibling authority is not granted. |
| Actual placement | Demonstrate systemd scope and supported container-runtime cgroup-parent behavior for startup helpers, host `exec`, jails, nested containers, attachment, and reconnect. Determine a race-safe, authenticated one-writer operation without a resident coordinator. |
| Accounting | On encrypted Btrfs/NVMe, establish how device identities, buffered writeback, shared files/inodes, metadata, and I/O throttling counters correspond to physical traffic; document an empirical accounting tolerance rather than inventing one. Measure responsiveness separately. |
| External services and other backends | Test whether individual Nix or other service work can be attributed without constraining unrelated users. Determine macOS/backend enforcement and status capability; an advisory priority or `nice` weight is not an aggregate-cap equivalent. |
| Roadmap routing | This design's live owner questions are routed from the project roadmap; its next action is to obtain the ruling on [OQ-RG1](#OQ-RG1), then investigate Linux placement and delegation. |

This is research, not a second author or an implementation sequence. Failure of a gate requires a revised design or explicit scope ruling, not a claim that the feature already works.

## 8. Open Questions

1. 💬 **OQ-RG1: Which backends must enforce placement in the initial release?**

   Rina maps a workspace to a limited group and later launches it on another backend. Comparable aggregate enforcement has not been proven there. Does the initial release support Linux-first or wait for broader backend parity?

   - **A — Linux-first enforcement.** Use a proved Linux path only; unsupported backends report their limits or refuse before startup, according to whether mappings are optional. This delivers a narrower feature without claiming false parity.
   - **B — Require backend parity.** Do not accept mappings until selected backends can enforce and report equivalent aggregate controls. This avoids platform divergence but can delay release or require different host integrations.

   <!-- vantage: question id=OQ-RG1 leaning="A: Linux-first with explicit unsupported states avoids presenting advisory controls as equivalent ceilings." -->

   _Leaning:_ A, contingent on a proved Linux authority path; truthful narrower support is preferable to false parity.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-RG2: How does the owner authorize the agent and batch split?**

   When Rina delegates child management for a finite assigned group, two shared children need ceilings for each constrained device and I/O dimension. Without an explicit split, Yolo cannot safely choose numbers; without an agent ceiling, the requested separation is absent. Which owner input should authorize those ceilings?

   - **A — Owner supplies concrete child ceilings.** Yolo validates each sum and reports unused capacity. The owner updates the values when the allowance or device policy changes.
   - **B — Owner supplies a split rule.** Yolo derives and refreshes concrete ceilings without a default. This reduces repeat configuration but entrusts Yolo with visible, bounded rounding and refresh behavior.

   <!-- vantage: question id=OQ-RG2 leaning="A: explicit ceilings make the delegated exception easiest to audit without silently choosing a throughput policy." -->

   _Leaning:_ A for the first release; owner-approved ceilings are clearer than a hidden ratio. Both alternatives require an explicit finite allowance and never modify its parent cap.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-RG3: Must every selected mapping be a required placement?**

   Kai maps a workspace to an owner group, but the host cannot confirm its I/O controller is delegated. Running outside the group may recreate the contention the mapping should prevent; refusing work blocks startup. Should every mapping require verified placement, or may the owner explicitly allow an unplaced launch?

   - **A — Every mapping requires proof.** Fail before expensive startup unless the group, membership, and controller are verified. This blocks work until host setup is fixed.
   - **B — Allow an explicit optional mapping.** Start unplaced only when the owner chose it, with visible status. This eases adoption but creates unbounded work.

   <!-- vantage: question id=OQ-RG3 leaning="A: a selected resource budget should not silently disappear; optional escape increases status and safety complexity." -->

   _Leaning:_ A; investigate setup failures rather than normalize an unplaced fallback.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-RG4: Do mapping changes move work already running?**

   Mira changes a workspace mapping while two sessions and their containers remain active. New launches can resolve the new group, but migration across host-mode processes, jails, containers, and external services is unproven. Should the new mapping affect only future launches, or should Yolo provide an explicit migration operation?

   - **A — Future launches only.** Status keeps each running job's actual group visible until it exits; the owner may need to stop and relaunch work to complete a transition.
   - **B — Provide explicit migration.** Report each workload's verified outcome, including partial migration and exclusions. This is more convenient for long-lived work but adds substantial runtime and failure-state complexity.

   <!-- vantage: question id=OQ-RG4 leaning="A: future-only mappings preserve truthful attachment without committing to an unproven live migration mechanism." -->

   _Leaning:_ A for the initial release. Live host-policy edits already change enforcement for existing members and should not be confused with moving their membership.

   **Answer:**
   > _(empty — fill in when decided)_
