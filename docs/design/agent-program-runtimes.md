---
title: "Agent program runtimes — the three gaps left after graduation, decided and built"
date: 2026-09-21
status: accepted
tags: [packs, programs, mise, node, provisioning, open-questions]
summary: "A stub. The Node floor, its resolution, the launcher splice and the refusal graduated to docs/reference/agent-program-runtimes.md; what stays here is three questions about where the refusal did not reach (OQ-AR5, OQ-AR6) and the launch whose launcher did not yet honor a floor the stage just met (OQ-AR7). All three were decided as implementation choices on 2026-09-30 (AR-L3 to AR-L5) and built the same day, with two choices the build made (AR-L6, AR-L7); the stub is ready to graduate."
---

# Agent program runtimes — the three gaps left after graduation, decided and built

**Status:** BUILT, 2026-09-30 — all three questions were decided as implementation choices
([AR-L3](#AR-L3)–[AR-L5](#AR-L5)) and their fixes were built the same day; [AR-L6](#AR-L6) and
[AR-L7](#AR-L7) record the two choices the build made. The stub is now ready to graduate into
the reference, which already describes the built behavior; its ids stay here until the links
into it move. The
settled body of this doc GRADUATED on 2026-09-25 to
[`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md): the principle,
the `node_floor` declaration, the resolution order and why resolving is split from installing, the
launcher's exec prefix, the refusal, what is measured, and the rulings [`OQ-AR1`](../reference/agent-program-runtimes.md#oq-ar1)–[`OQ-AR4`](../reference/agent-program-runtimes.md#oq-ar4) and
[`AR-L2`](../reference/agent-program-runtimes.md#ar-l2) in its [Why it's this way](../reference/agent-program-runtimes.md#why-its-this-way)
table. The companion plan, `agent-program-runtimes-plan.md`, was deleted with the graduation.

**Needs your ruling:** nothing. [OQ-AR5](#OQ-AR5), [OQ-AR6](#OQ-AR6) and [OQ-AR7](#OQ-AR7) were
decided as implementation choices, each inside [`OQ-AR2`](../reference/agent-program-runtimes.md#oq-ar2)
and [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3)'s rulings, and the
[Decision Ledger](#decision-ledger) says why.

**This file is what remains: three questions, now decided and built.** The first two were cases
where a jail started although [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) says it
should not; the third was a launch that passed the floor check while its launcher did not yet
exec the interpreter that met it. They were found while making the refusal real and while
reviewing its graduation, and were recorded rather than fixed, because each fix changes when the
provisioning stage or the launcher generation runs. Read the reference first; each question
below assumes it. The ids are kept because Go comments and
[`jail-notch-readiness.md`](jail-notch-readiness.md) cite them. The file graduates into the
reference now that the three fixes are built.

---

## Open Questions

1. ✅ <a id="OQ-AR5"></a>**[OQ-AR5](#OQ-AR5): macos-user runs no provisioning stage without
   `mise_tools`.** That backend starts its stage only when `mise_tools` is non-empty
   (`ProvisionNeeded`, [`provision.go`](../../internal/macosuser/provision.go)), so a workspace
   selecting a floor-declaring pack with no `mise_tools` gets neither the eager install nor the
   refusal. Options: **(a)** count a declared floor in `ProvisionNeeded`, so every such launch
   pays a privileged stage even when the package floor's own node already satisfies it; **(b)**
   count only a floor the resolution cannot meet; **(c)** leave it, documented.

   _Leaning:_ **(b)**. (a) charges every launch for a node that is probably already there, and (c)
   leaves the ruling unkept on one backend. ⚠ (b) is harder than it reads: `ProvisionNeeded` runs
   on the HOST, before the sandbox exists, so it cannot ask the in-sandbox resolution directly.
   The resolution half is built: since 2026-09-25 the resolution reads the package floor's node
   as macos-user's first candidate, which fixed the reverse case, where a stage that did run
   refused with "Available: none" while the floor's `nodejs_24` sat on `PATH`. What stays open is
   only whether a floor starts a stage.

   **Answer:**
   > Decided as an implementation choice ([AR-L3](#AR-L3)), reversible: **(b)**. A declared floor
   > starts the stage unless the host can show a candidate it can read already meets it. macos-user
   > has no mount namespace, so the host reads the same package-floor paths the sandbox would, and
   > a host that cannot answer starts the stage, which checks again. **Built 2026-09-30**
   > ([AR-L7](#AR-L7)).

2. ✅ <a id="OQ-AR6"></a>**[OQ-AR6](#OQ-AR6): a failed `mise install` skips the floor check.**
   The stage's steps are joined with `&&`, so when `mise install` fails (offline, or a broken
   workspace `mise.toml`) the bootstrap never runs, and the launch degrades as any failed stage
   does, with no floor checked at all. Options: **(a)** run the bootstrap whether or not
   `mise install` succeeded, the stage's status being `RefusedStatus` if the bootstrap refused and
   `mise install`'s failure otherwise; **(b)** leave it: a failed `mise install` is already
   recorded as `PROVISIONING FAILED`, and the agent's briefing says so.

   _Leaning:_ **(a)**. The refusal is a claim about the jail, and a workspace's broken `mise.toml`
   should not be the thing that silences it.

   **Answer:**
   > Decided as an implementation choice ([AR-L4](#AR-L4)), reversible: **(a)**. The bootstrap runs
   > whether or not `mise install` succeeded, on both backends. The stage exits `RefusedStatus` when
   > the bootstrap refused, and otherwise with the first failure, so a failed `mise install` still
   > degrades exactly as it does today. **Built 2026-09-30** (`provision.Stage`).

3. ✅ <a id="OQ-AR7"></a>**[OQ-AR7](#OQ-AR7): a stage-installed interpreter reaches the launcher
   one boot late.** The launcher's interpreter is resolved once, at launcher generation, which is
   a boot step that runs before the provisioning stage on both backends. So when the stage
   installs the node a floor needs, the floor check (which asks the resolver again) passes, while
   the program's launcher was already written with a plain `exec "$REAL_BIN"` and runs the
   program under whatever node `PATH` gives it. The next boot regenerates the launcher and bakes
   the interpreter. No shipped floor reaches this today, because candidate 1 satisfies pi's
   floor on both backends; a floor above the image's node would. Pinned by
   `TestAStageInstalledNodeReachesTheLauncherOnlyAtTheNextGeneration` (`internal/entrypoint`);
   the reference states it as [a warning](../reference/agent-program-runtimes.md#a-stage-installed-interpreter-reaches-the-launcher-one-boot-late).
   Options: **(a)** after a floor install, the bootstrap regenerates the launchers of the
   programs declaring that floor (a new `yolo internal` verb, run where the bootstrap already
   runs, so it needs no new privilege); **(b)** resolve at run time instead, in the launcher,
   which costs a `--version` exec per invocation and gives up the baked, readable path;
   **(c)** refuse the launch when the check passes only through a node the launcher does not
   name, so the user relaunches; **(d)** leave it, documented, since the next boot heals it.

   _Leaning:_ **(a)**. It keeps resolution at generation, costs nothing on a launch that installs
   nothing, and makes the first launch honor the floor. (c) turns a working install into a
   refusal, and (d) runs the program under the wrong node on exactly the launch that noticed.

   **Answer:**
   > Decided as an implementation choice ([AR-L5](#AR-L5)), reversible: **(a)**. After a floor
   > install, the bootstrap regenerates the launchers of the programs that declare that floor,
   > through a new `yolo internal` verb run where the bootstrap already runs. **Built 2026-09-30**
   > as `yolo internal node-floor-launchers`, run whenever the floor is met in the stage while a
   > launcher waits ([AR-L6](#AR-L6)).

## Decision Ledger

Implementation decisions, numbered after the original design's `AR-L1` and
[`AR-L2`](../reference/agent-program-runtimes.md#ar-l2). The rulings they sit inside are the
reference's [Why it's this way](../reference/agent-program-runtimes.md#why-its-this-way) rows.

| ID | Ruling / Decision | Date | Settled in | Built? |
| :--- | :--- | :--- | :--- | :--- |
| <a id="AR-L3"></a>AR-L3 | *Implementation decision, [OQ-AR5](#OQ-AR5).* **(b): on macos-user, a declared floor starts the provisioning stage unless the host can show it met.** [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3)'s refusal must reach this backend, which rules out (c). The choice between (a) and (b) is cost, which the maintainer would not rank, and (b) costs a stage only where one can change the answer. The leaning's worry, that `ProvisionNeeded` runs on the host before the sandbox exists, turns out not to block it: macos-user is a native process with no mount namespace, and the launch already composes the sandbox's `PATH` host-side (`SandboxPath`). So the host reads the same package-floor nodes the resolution's candidate 1 reads (`packageFloorNodes`: the `PATH` entries outside the sandbox home). **The rule fails toward the stage.** A floor the host finds no readable candidate for, a candidate whose version it cannot read, or any error starts the stage, and the stage checks again and installs or refuses. So a wrong host answer costs one stage and never skips a refusal. [`jail-notch-readiness.md`](jail-notch-readiness.md)'s [JR-D2](jail-notch-readiness.md#JR-D2) gives a missing declared program the same start rule. Reversible: counting every floor, (a), is a one-line change to `ProvisionNeeded` | 2026-09-30 | [OQ-AR5](#OQ-AR5) | ✅ 2026-09-30: `macosuser.floorStageFor` in the orchestrator's `buildPlan`, over `entrypoint.PackageFloorMeets`; `ProvisionNeeded` takes its answer ([AR-L7](#AR-L7)) |
| <a id="AR-L4"></a>AR-L4 | *Implementation decision, [OQ-AR6](#OQ-AR6).* **(a): the bootstrap runs whether or not `mise install` succeeded, on both backends.** The stage exits `provision.RefusedStatus` when the bootstrap refused, and otherwise with the first step's failure. Only the bootstrap stops depending on the steps before it. They stay joined as they are (`provision.Setup` uses `&&`), so on the container a failed `mise install` still skips the venv step, as today. [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) ruled the refusal with no escape hatch, and a workspace's broken `mise.toml` silencing it is an unintended one. The accepted cost is AR3's own: an offline boot whose floor nothing meets refuses, even when `mise install` also failed. Nothing else changes, because a failed `mise install` still writes `PROVISIONING FAILED` and degrades as before. Reversible: rejoin the step with `&&` | 2026-09-30 | [OQ-AR6](#OQ-AR6) | ✅ 2026-09-30: `provision.Stage`, which both backends' stage bodies now compose |
| <a id="AR-L5"></a>AR-L5 | *Implementation decision, [OQ-AR7](#OQ-AR7).* **(a): after a floor install, the bootstrap regenerates the launchers of the programs declaring that floor**, through a new `yolo internal` verb run where the bootstrap already runs, so it needs no new privilege. Resolution stays at generation, where it is a baked, readable path, and a launch that installs nothing does no extra work. The first launch honors the floor. (b) costs a `--version` exec on every invocation and gives up the baked path. (c) refuses a working install. (d) runs the program under the wrong node on the one launch that knew better. On macos-user the stage runs under `env -i`, so the verb's inputs are baked into the script, as the floor checks already are. `TestAStageInstalledNodeReachesTheLauncherOnlyAtTheNextGeneration` pinned the one-boot lag and was replaced by `TestAStageInstalledNodeReachesTheLauncherOnTheLaunchThatInstalledIt`. Reversible: drop the verb call, and the next boot heals as it did | 2026-09-30 | [OQ-AR7](#OQ-AR7) | ✅ 2026-09-30: `yolo internal node-floor-launchers` (`entrypoint.RegenerateFloorLaunchers`), called by the bootstrap's `_yolo_node_floor` ([AR-L6](#AR-L6)) |
| <a id="AR-L6"></a>AR-L6 | *Implementation decision, inside [AR-L5](#AR-L5).* **The regeneration finishes a launcher from the render the boot left, and runs whenever the floor is met in the stage while such a launcher waits.** A launcher whose declared floor resolved to nothing at generation leaves a **floor-pending record**, a term this build coins: the launcher's render split at every place the interpreter goes, plus the floor, at `~/.yolo/bin/floor-pending/<bin>`, cleared with the launch dir each boot. Once a floor is met, the bootstrap hands its npm programs' bins to the verb only if one of them has a record; the verb resolves the floor again and joins the record's segments with the interpreter, so every other byte is the boot's render and the result is the next boot's launcher, byte for byte. The pending and launch directories are baked into the bootstrap, as the checks are. A failed regeneration is a warning, because the floor is met and what remains is the one-boot lag. **Why:** a floor is met in the stage by more than its own install: the workspace's `mise install`, which runs first, can install a node that meets it, and the launcher lags the same way. Keeping the boot's render rather than regenerating from the packs means the verb needs no copy of the generator's inputs, which macos-user's `env -i` stage would otherwise have to be handed one by one. A record's split point is a random token, so a pack value spelling the template's sentinel cannot become a place the interpreter lands | 2026-09-30 | [OQ-AR7](#OQ-AR7) | ✅ 2026-09-30 |
| <a id="AR-L7"></a>AR-L7 | *Implementation decision, inside [AR-L3](#AR-L3).* **The host reads the declared floors from the staged pack tree the bootstrap renders from, strictly, and asks only the resolution's first candidate.** `entrypoint.DeclaredNodeFloorsAt` loads that tree without the jail's tolerant decoding, which is process-wide and must not switch on in the host process, and `entrypoint.PackageFloorMeets` reads each `node` on the sandbox's `PATH` outside its home, the same files the sandbox's candidate 1 reads. The mise store, candidate 2, lives in the sandbox home and is left to the stage. An unreadable tree starts the stage. The dry run asks too, against a `PATH` with no floor materialized, so its plan shows the stage for any declared floor and names the floor it runs for. **Why:** this binary staged the tree, so it reads it as the staging did; the rule fails toward the stage everywhere, so a candidate the host cannot see costs one stage, never a skipped refusal | 2026-09-30 | [OQ-AR5](#OQ-AR5) | ✅ 2026-09-30 |
