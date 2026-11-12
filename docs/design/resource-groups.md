---
status: accepted
stage: DECIDED
next: "Follow the implementation plan (resource-groups-plan.md): keep mapped execution refused until native delegation, actual placement and device evidence are proven; keep authority and device proof as limits on claims, not blockers to safe implementation work."
---

# Owner-managed resource groups can bound combined workspace work only when placement and enforcement are proven

**Status:** 2026-11-04. Owner direction and request-authorized v1 choices are recorded; implementation is partly built: the default-off selection, finite-policy planning and a fake-only admission boundary are integrated (`93139b1f6`), and no production caller places or enforces anything ([the plan](resource-groups-plan.md)). Source seams were checked at `c62ca3ce089e7062c4932186e9c59a6c09441ea7`; Linux authority, actual placement, and device behavior remain unproven.

> **In short.** Resource-group behavior is off unless the owner opts in and maps work; the unconfigured launch path does not probe or change host groups. For opted-in Linux work, Yolo must verify actual placement before startup and may split a finite delegated allowance between shared agent and batch children using a 5/95 default that the owner can override.

**Why now:** The request reports substantial disk traffic from a Forms import and a nearly continuously busy SSD during desktop stalls. Disk contention is a plausible contributor, not a proven cause of any particular Brave freeze. Per-container limits and process I/O priority do not establish a shared host I/O ceiling across launches.

**Cost:** Owners must establish persistent groups, policy, opt-in mappings, and the authority needed for placement. Strict placement can prevent a mapped launch when that setup is absent. Fixed child ceilings may leave capacity unused; this design adds neither a monitoring daemon nor adaptive borrowing.

**Start at [§7](#7-decisions-research-gates-and-routing)** — implement only the opt-in Linux path, with authority and device evidence bounding enforcement and performance claims.

**Needs your ruling:** None.

**Read with:** [I/O-priority design](io-priority.md) (existing, in review), [cgroup-delegate reference](../reference/security-shim.md) (existing), [roadmap](../plans/roadmap.md) (routes this work), [Linux cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [systemd slices](https://www.freedesktop.org/software/systemd/man/latest/systemd.slice.html), and [systemd resource control](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html).

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

The owner persists named groups, hierarchy, and policy for I/O bandwidth, CPU, memory, and other host resource controls through the host's normal resource-control machinery. Yolo stores only explicit selection intent—not a duplicate copy of those limits—with an optional default workspace mapping, more-specific workspace/project mappings, and the owner-designated group for machine-wide maintenance. An explicitly configured default mapping opts its covered workspaces in; absent owner opt-in and mapping, Yolo does not resolve groups or inspect manager, controller, or device policy at startup, create or move processes, set caps, or add startup work. The exact opt-in/configuration spelling remains an implementation detail.

For opted-in work, the proposed resolution order is an exact workspace mapping, then a project mapping, then the explicit default; conflicting mappings at the same specificity must be rejected rather than resolved by incidental ordering. The selected identity and source of selection must be displayed before work starts. The implementation may choose configuration spelling and the authorization mechanism within the least-authority constraints below; neither may weaken explicit opt-in or owner-imposed hierarchy.

Yolo must not rewrite an owner-managed parent limit, create an unlimited replacement, move work around a required ancestor, or remove an owner-managed slice during cleanup. A selected mapping requires verified placement; there is no silent unplaced or unlimited fallback. Host policy changes ordinarily act on existing members through the kernel without relaunching Yolo. Mapping changes apply only to future launches; existing work remains attached to its actual group until it exits, and status distinguishes that attachment from the new mapping.

The placement transaction is: resolve selection; validate group identity, hierarchy, permissions, and required controllers; establish or reuse any authorized children; place the execution boundary; verify observable membership and policy; only then permit expensive workspace startup. This applies to image import, extraction, provisioning, and other Yolo-owned startup work, not just the eventual agent. Machine-wide maintenance uses its separately owner-configured group rather than arbitrarily charging one workspace. If a runtime performs work in an external daemon, client placement alone is insufficient and that work is disclosed as excluded or unverified.

### Authority is not implied by a slice name

A system-manager slice, a user-manager slice, a delegated subtree, and a rootless container runtime can have different authority despite similar names. The Linux path is conditional on verifying every relevant ancestor, controller availability, systemd delegation, runtime cgroup-parent behavior, and permission to create or join the intended units. The request's reported lack of user-manager I/O delegation on one host is a setup obstacle to investigate, not a universal Linux property. Yolo must not quietly gain authority over unrelated sibling groups to make placement work.

The existing jail-scoped cgroup delegate is **not** presumed to provide that authority. Any privileged placement or subdivision route must have a bounded authorization model and be reviewed as a security-boundary change before adoption. This design does not authorize a host helper, broader delegate protocol, or host policy change.

## 3. Shared child ceilings

For opted-in assigned groups that permit child management, Yolo maintains one shared agent child and one shared batch child, reused by all its launches. They are **not** created once per session. Work classification occurs at the actual agent, tool, managed-background-job, and container execution boundaries; terminal attachment, foreground focus, and guessed PIDs do not classify work. A heavy operation performed *within* the agent process cannot be split into a separate child by classification alone. A heavy job deliberately placed in the agent child still faces that child's aggregate ceiling and its parent's ceiling.

Child allocation is off until the owner opts in to resource-group use. For each opted-in assigned group with a finite allowance, the engineering v1 default split is 5% agent-facing and 95% batch for each controlled device and I/O dimension; the owner can override that split. These are proportions of the group's finite allowance, not absolute throughput values or hardware-specific caps. No hardware-tuned rate is invented. Allocation must satisfy `agent-child ceiling + batch-child ceiling ≤ finite assigned allowance`; both child ceilings must be positive and finite. Never encode an unlimited sentinel or substitute relative weights for a cap. If the default or owner override cannot produce representable positive finite ceilings for a controlled dimension, fail with an actionable explanation rather than writing zero or unlimited.

If the assigned group has no usable finite allowance, report it and do not create a split or start mapped work outside the required group; the owner must supply or authorize a finite allowance. An ancestor ceiling is an upper bound, not an entitlement: siblings may consume it, so an ancestor-only cap cannot be converted into an assigned allowance. Unlimited or unreadable policy is not a finite budget. An unconstrained dimension does not acquire an invented cap. The request's 900 MiB/s parent and 20/880 MiB/s child split illustrates arithmetic, **not** a default.

Concurrent launchers must serialize child creation and refresh through a bounded one-writer operation, using suitable locking or systemd unit operations if validated. On retry, they inspect and reuse the same children rather than multiplying allocations. No resident Yolo policy coordinator is introduced.

When the owner lowers a parent cap, the kernel enforces it immediately on existing descendants if that controller and placement are actually effective, even before Yolo refreshes the old child settings. During that interval, programmed child ceilings might sum above the new parent allowance; status must say **stale child allocation, parent still caps**, not assert the subdivision invariant currently holds. A successful subsequent launch or explicit refresh recomputes and reconciles children before starting new work. A parent increase may leave bandwidth unused until then. If the parent becomes unlimited or unreadable, no new finite allowance is inferred; existing work remains under whatever host policy can be observed, and required new starts cannot rely on a fresh split. Refresh failure must not raise the parent, fabricate an allowance, or start new work under an unverified required allocation. Idle agent capacity is not automatically loaned to batch work.

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

Initial enforcement is Linux-first. Only a proved Linux placement path may claim aggregate control; advisory priority or per-container limits are not equivalent. A mapping applied to a backend without verified aggregate placement refuses before expensive startup with the missing support/setup action, rather than silently running unrestricted.

Children are Yolo-managed subordinate state, while parent slices remain owner-managed. On restart or crash, a launcher reconciles existing children and live membership instead of assuming a clean slate. Reconnect does not create another allowance. Cleanup may remove only Yolo-owned children after confirming no active jobs depend on them; uncertain membership favors retention and an explicit report over stranding work. No custom poller retrofits escaped processes after launch.

## 5. Observable state and failure contract

A launch preview and status/check surface are part of this feature. For each workspace they distinguish **requested mapping**, **resolved owner group**, **actual observed placement**, **controller/device policy**, **effective known ancestor bounds**, **child allocation and freshness**, and **known exclusions**. Participating workloads are shown with the evidence used to classify them; external-service and runtime-daemon traffic is not counted as contained merely because its initiating client is contained.

| State | Operator-visible behavior |
|---|---|
| Resource groups disabled or no explicit mapping | Report disabled from local selection state without manager/controller/device probes; leave ordinary startup unchanged. |
| Group exists but permission, ancestor, controller, delegation, or device-policy proof is missing | Report the precise unavailable observation as **unknown/unsupported** rather than zero or enforced. Required placement blocks before expensive startup. |
| Placement request accepted but membership not demonstrated | Report **placement unverified**, not success. Required work does not proceed on that assertion alone. |
| Concurrent creation, owner update, or group removal races with startup | Re-read identity, policy, and membership at the commitment boundary; reconcile under the one-writer operation or fail closed for required work. |
| Mapping changes while jobs run | Show each running job's actual attachment separately from the new mapping; the new mapping applies only to future launches, not live migration. |
| Parent changes while child allocation is stale | Show the live observed parent policy and programmed child ceilings separately; reconcile on the next permitted launch or explicit refresh. |
| Reconnect or crash leaves jobs alive | Discover and report actual surviving membership; do not delete their groups or claim they moved. |
| Coverage passes into a shared daemon | Report that portion as excluded or unverified, with the attribution gap; do not apply a global daemon cap. |

Host policy is read from the host, not copied into Yolo as another authoritative limit store. A successful check can establish placement, configured controller values, and observed throttling counters where available. It cannot by itself guarantee desktop responsiveness, a precise physical-device rate, or complete buffered-write attribution.

## 6. Acceptance outcomes

The design is accepted for implementation, but no implementation or host/device verification is complete. Implementation work may proceed on the disabled-by-default, opt-in path. A mapped workload may claim enforcement only after host authority, controller availability, and actual membership are demonstrated; claims about device-level throughput or desktop responsiveness require representative accounting and latency evidence. The operator outcomes below remain required:

1. Verify an unconfigured launch performs no manager/controller/device probes, group creation, process moves, cap writes, or added startup work. Then configure an explicit opt-in default and workspace mapping; verify selection persists across launcher restarts without a monitoring daemon and show requested, resolved, and actual groups separately.
2. Run two simultaneous workspaces in one owner group and verify their **combined** sustained per-device traffic against its budget within a documented accounting tolerance. Adding sessions must not multiply it.
3. Exercise distinct project children beneath a limited parent, plus non-Yolo batch work there; observe both project limits and their common aggregate parent bound.
4. Reuse one pair of agent/batch children across sessions. For each finite controlled device/dimension, verify the unoverridden 5/95 split and any owner override, and confirm the ceilings sum within the allowance. Verify a deliberately heavy agent-child job remains bounded, without substituting weights.
5. Exercise finite, unlimited, unreadable, and ancestor-with-siblings cases. Decrease and increase a parent cap with work running; distinguish immediate parent enforcement from stale or refreshed children without a watcher.
6. Change systemd policy while jobs run and verify effective behavior changes without restarting them or editing a second Yolo policy store.
7. Exercise buffered writes, reads, provisioning, host-mode descendants, and nested containers; list actual external-service coverage gaps.
8. On a representative encrypted Btrfs/NVMe host, observe kernel-reported throttling and compare desktop responsiveness against unrestricted and several higher-cap baselines. Choose any throughput value from **machine-specific latency and batch-throughput evidence**, not the earlier suggested 200–300 MiB/s figure.
9. Confirm unrelated desktop work remains outside the restricted hierarchy and can use available device capacity.
10. Exercise unknown names, missing controllers or delegation, simultaneous launches, mapping changes, reconnects, crashes, and cleanup; neither silently escape a required budget nor delete an owner slice or strand active jobs.

These are outcomes, not claims that the current host passes them. `io.max` is a rate control, not a latency service-level objective. Encryption, Btrfs writeback and metadata, shared inodes, device mapping, and competing ancestor siblings complicate attribution; IOPS controls and prioritization should be evaluated experimentally without changing the host scheduler implicitly.

## 7. Decisions, research gates, and routing

### Decision ledger

| ID | Ruling / decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-RG1 | Linux-first enforcement; a mapped backend without proved aggregate placement refuses rather than claiming parity or silently escaping. This is the v1 scope permitted by the request, not a general backend capability claim. | 2026-11-04 | [§2 Ownership and placement](#2-ownership-and-placement-architecture) | — |
| OQ-RG2 | For opted-in groups with a finite assigned allowance, default to a 5% agent-facing / 95% batch split per controlled device and I/O dimension; the owner may override it. These proportions do not set an absolute hardware rate or cap. | 2026-11-04 | [§3 Shared child ceilings](#3-shared-child-ceilings) | — |
| OQ-RG3 | Keep resource-group behavior off by default. No opt-in/mapping means no group resolution, manager/device probes, group or process changes, caps, or startup impact. A selected mapping is required placement: no silent unplaced or unlimited fallback. | 2026-11-04 | [§2 Ownership and placement](#2-ownership-and-placement-architecture) | — |
| OQ-RG4 | Mapping changes apply to future launches only; existing work remains in its observed group. Live host-policy changes continue to govern existing members when the host controller and placement are effective. This v1 behavior is permitted by the request, not a separate owner ruling. | 2026-11-04 | [§2 Ownership and placement](#2-ownership-and-placement-architecture) | — |

These rows compact the four answered design questions; the request's other requirements remain normative in the body. Linux authority and device evidence are engineering gates on truthful enforcement/performance claims, not unanswered owner questions or a reason to block all implementation work.

### Engineering gates for enforcement and performance claims

| Gate | Evidence needed; do not assume success |
|---|---|
| Linux authority | Establish a least-authority route for rootless Yolo to select owner groups and create/reuse delegated children; inspect system versus user manager, `Delegate=`, every ancestor controller and subtree state, and whether privileged setup is necessary. Establish that unrelated sibling authority is not granted. This investigation can proceed alongside safe implementation; no mapped launch may claim enforcement without the proof. |
| Actual placement | Demonstrate systemd scope and supported container-runtime cgroup-parent behavior for startup helpers, host `exec`, jails, nested containers, attachment, and reconnect. Determine a race-safe, authenticated one-writer operation without a resident coordinator. Required mapped work must verify membership before expensive startup. |
| Accounting and responsiveness | On encrypted Btrfs/NVMe, establish how device identities, buffered writeback, shared files/inodes, metadata, and I/O throttling counters correspond to physical traffic; document an empirical accounting tolerance rather than inventing one. Measure responsiveness separately before claiming a user-visible latency benefit or recommending absolute rates. |
| External services and unsupported backends | Test whether individual Nix or other service work can be attributed without constraining unrelated users. Other backends remain unsupported unless aggregate enforcement and status are demonstrated; advisory priority or `nice` weight is not an equivalent cap. |

The Linux-first implementation may proceed while these gates are investigated. If a gate fails, that path stays unsupported or the design is revised; no mapping may silently run unplaced and no document may claim enforcement, a throughput ceiling, or responsiveness beyond observed evidence.
