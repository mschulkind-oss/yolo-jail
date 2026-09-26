---
title: "Agent program runtimes — the three questions left open"
date: 2026-09-21
status: in-review
tags: [packs, programs, mise, node, provisioning, open-questions]
summary: "A stub. The Node floor, its resolution, the launcher splice and the refusal graduated to docs/reference/agent-program-runtimes.md; what stays here is the three open questions about where the refusal does not reach (OQ-AR5, OQ-AR6) and the launch whose launcher does not yet honor a floor the stage just met (OQ-AR7)."
---

# Agent program runtimes — the three questions left open

**Status:** DESIGN, 2026-09-26 — three open questions, and none of their fixes is built. The
settled body of this doc GRADUATED on 2026-09-25 to
[`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md): the principle,
the `node_floor` declaration, the resolution order and why resolving is split from installing, the
launcher's exec prefix, the refusal, what is measured, and the rulings [`OQ-AR1`](../reference/agent-program-runtimes.md#oq-ar1)–[`OQ-AR4`](../reference/agent-program-runtimes.md#oq-ar4) and
[`AR-L2`](../reference/agent-program-runtimes.md#ar-l2) in its [Why it's this way](../reference/agent-program-runtimes.md#why-its-this-way)
table. The companion plan, `agent-program-runtimes-plan.md`, was deleted with the graduation.

**Needs your ruling:** [OQ-AR5](#OQ-AR5), [OQ-AR6](#OQ-AR6), [OQ-AR7](#OQ-AR7).

**This file is what remains: three open questions.** The first two are cases where a jail
starts although [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) says it should not; the
third is a launch that passes the floor check while its launcher does not yet exec the interpreter
that met it. They were found while making the refusal real and while reviewing its graduation,
and were recorded rather than fixed, because each fix changes when the provisioning stage or the
launcher generation runs. Read the reference first; each question below assumes it. The ids are
kept because Go comments and
[`jail-notch-readiness.md`](jail-notch-readiness.md) cite them.

---

## Open Questions

1. 💬 <a id="OQ-AR5"></a>**[OQ-AR5](#OQ-AR5): macos-user runs no provisioning stage without
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

   <!-- vantage: oq id=OQ-AR5 leaning="(b): count in ProvisionNeeded only a floor the resolution cannot meet. (a) charges every launch a privileged stage for a node that is probably already there; (c) leaves OQ-AR3 unkept on one backend. ProvisionNeeded runs on the host before the sandbox exists, so it cannot ask the in-sandbox resolution directly." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-AR6"></a>**[OQ-AR6](#OQ-AR6): a failed `mise install` skips the floor check.**
   The stage's steps are joined with `&&`, so when `mise install` fails (offline, or a broken
   workspace `mise.toml`) the bootstrap never runs, and the launch degrades as any failed stage
   does, with no floor checked at all. Options: **(a)** run the bootstrap whether or not
   `mise install` succeeded, the stage's status being `RefusedStatus` if the bootstrap refused and
   `mise install`'s failure otherwise; **(b)** leave it: a failed `mise install` is already
   recorded as `PROVISIONING FAILED`, and the agent's briefing says so.

   _Leaning:_ **(a)**. The refusal is a claim about the jail, and a workspace's broken `mise.toml`
   should not be the thing that silences it.

   <!-- vantage: oq id=OQ-AR6 leaning="(a): run the bootstrap whether or not mise install succeeded, the stage's status being RefusedStatus if the bootstrap refused and mise install's failure otherwise. A workspace's broken mise.toml should not silence a claim about the jail." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-AR7"></a>**[OQ-AR7](#OQ-AR7): a stage-installed interpreter reaches the launcher
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

   <!-- vantage: oq id=OQ-AR7 leaning="(a): after a floor install, the bootstrap regenerates the launchers of the programs declaring that floor. It keeps resolution at generation, costs nothing when nothing is installed, and makes the first launch honor the floor; (c) refuses a working install and (d) runs the program under the wrong node on the launch that noticed." -->

   **Answer:**
   > _(empty — fill in when decided)_
