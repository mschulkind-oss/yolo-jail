---
title: "A host launch is a readiness act, and a declared program is never a PATH copy"
date: 2026-10-07
status: accepted
stage: BUILT
next: "Graduate into docs/reference/host-agent-environment.md once a release ships it"
tags: [host, provisioning, readiness, floor, path, program-delivery, notches]
summary: "`yolo host -- <cmd>` installs only the one program it starts and falls back to the caller's PATH for a program the floor cannot hold. A jail installs every program a selected pack declares before the command runs and stops when one cannot install. This makes the host launch the same readiness act: install every declared program first, refuse on a failure unless YOLO_ALLOW_MISSING_PROGRAMS=1, and never run a PATH copy for a program a selected pack declares. It reverses HP-D3 and OQ-HE11 (a), and the costs of doing so are recorded below."
vantage:
  status-chip: true
---

# A host launch is a readiness act, and a declared program is never a PATH copy

**Status:** ruled 2026-10-07 (*"host should act just like a jail and not sniff the path"*), **built
2026-10-08**: the act is `hostReadinessAct` ([`hostreadiness.go`](../../internal/cli/hostreadiness.go)),
called by `hostLaunch` before `resolveHostLaunchTarget`, which now refuses a declared program with no
floor entry. [§3](#3-the-host-readiness-act-hnr-d1) and
[§4](#4-a-declared-program-is-never-a-path-copy-hnr-d2) carry the decisions; the build added
[HNR-D4](#HNR-D4) to [HNR-D7](#HNR-D7), the questions the build had to answer. §2 describes the
behavior before the build, read at `afab7bea`.

> **In short.** The jail and the host should answer *"is this environment ready?"* the same way,
> and today they answer it oppositely: a jail installs every program a selected pack declares
> before your command and stops when one fails, while a host launch installs the single program it
> is about to start and quietly substitutes a PATH copy for anything its floor cannot hold.

**Why it matters.** A user launching an agent at the host gets a different, weaker promise than the
same launch in a jail — and the difference is invisible until the substitution bites. The reported
case: `yolo host -- codex` could not install codex, and `yolo host -- bash` then launched anyway,
having made no attempt at the programs the config declared. "All the tools that are marked" is a
jail guarantee the host does not give.

**The shape.** One new step on the host launch path — a **launch readiness act** over the floor's
existing program set — plus one rule change in target resolution: a program a selected pack
declares runs from the floor or not at all.

**Cost.** Every host launch touches the whole floor, so a first launch on a machine installs every
declared program even for `yolo host -- bash`. And a machine the floor cannot provision (a Mac
before `yolo macos-setup`, a Linux host with neither a container runtime nor Landlock) now
**refuses** `yolo host -- claude` instead of running a PATH copy. That refusal is deliberate and is
the reversal's main price; [§6](#6-what-this-costs) states it in full.

**Start at [§3](#3-the-host-readiness-act-hnr-d1)** — the act. [§4](#4-a-declared-program-is-never-a-path-copy-hnr-d2)
is its other half.

**Needs your ruling:** None — the direction is ruled (2026-10-07); both decisions are settled in
[§11](#11-decision-ledger).

**Reads with:** [`jail-notch-readiness.md`](jail-notch-readiness.md) (the act this mirrors, and the
[`OQ-JR1`](jail-notch-readiness.md#OQ-JR1) contract it copies), [`host-tool-provisioning.md`](host-tool-provisioning.md) (the floor,
and `HP-D3` which [HNR-D1](#HNR-D1) reverses), [`../reference/host-agent-environment.md`](../reference/host-agent-environment.md)
([`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) (a), which [HNR-D2](#HNR-D2) reverses), [`../plans/notch-convergence.md`](../plans/notch-convergence.md)
(`NC-D1`: *"host is supposed to act like everywhere else"*).

---

## 1. The ruling

The maintainer, 2026-10-07:

> *"host should act just like a jail and not sniff the path."*

Two consequences, and they are independent enough to be two decisions:

1. **The host launch is a readiness act.** Before the command, install every program a selected
   pack declares — not only the one about to run. A failure stops the launch by default.
2. **A program a selected pack declares is never a PATH copy.** The floor is the only source. A
   program the floor cannot hold is a refusal, not a silent substitution.

Neither is built. Both reverse a ruled decision, and each reversal is recorded in
[§11](#11-decision-ledger).

## 2. What existed before the build, and the asymmetry

**The jail** has a launch readiness act since 2026-10-05 ([`jail-notch-readiness.md`](jail-notch-readiness.md),
[`OQ-JR1`](jail-notch-readiness.md#OQ-JR1)): the boot's provisioning stage installs every program
a selected pack declares, before the command, and stops when one cannot install, offline included,
unless `YOLO_ALLOW_MISSING_PROGRAMS=1` is set. The act lives in
[`readiness.go`](../../internal/entrypoint/readiness.go).

**The host** does three weaker things, each ruled separately:

| Question | Jail | Host today | The host's authority |
| :--- | :--- | :--- | :--- |
| What installs before the command | **every declared program** | **only `cmd0`**, plus the MCP servers its config names | [`HP-D3`](host-tool-provisioning.md#HP-D3), `ensureMCPPrograms` |
| A failure during that install | **refuses the launch** | refuses only if it was `cmd0` (exit 127); otherwise nothing else was attempted | [`HP-D3`](host-tool-provisioning.md#HP-D3) |
| A declared program with no floor entry | (no equivalent — the jail always has a way) | **runs a PATH copy**, with one line saying the floor has none | [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) (a) |
| The verb that installs everything | `yolo -- true` (the launch) | `yolo host apply --assert` — **not on the launch path** | [`HP-D8`](host-tool-provisioning.md#HP-D8) |

The evidence, read at `afab7bea`:

- `resolveHostLaunchTarget` (`internal/cli/hostfloor.go:767`) calls `floor.Ensure` for **`cmd0` and
  nothing else**, then `ensureMCPPrograms` (`internal/cli/hostfloor.go:842`) for the MCP servers.
- A declared program with no floor entry falls to `onPath()` (`internal/cli/hostfloor.go:758`),
  the PATH lookup, which is [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) (a).
- `yolo host apply --assert` installs every missing entry, but a launch never runs it
  ([`HP-D8`](host-tool-provisioning.md#HP-D8); the two verbs are separate).

> [!WARNING]
> **`host_apply_on_launch` is not this act, and must not be wired as it.** It is the wrapper gate's
> **staleness** net — "the apply wrote once and nothing looked again" — and it says so itself
> ([`host-apply-staleness.md`](../reference/host-apply-staleness.md)). Readiness is the launch's;
> drift is the wrapper gate's. [`jail-notch-readiness.md` §2](jail-notch-readiness.md#2-the-host-notch-already-has-this-and-says-why)
> draws the same line at the jail.

## 3. The host readiness act (HNR-D1)

**HNR-D1: every `yolo host -- <cmd>` launch installs every program a user-scope selected pack
declares, before it resolves and execs the target.**

| Property | Value |
| :--- | :--- |
| **Trigger** | Every exec of the `yolo host -- …` half, including every wrapper that execs it. Not `yolo host env`, `apply`, or `wrappers`, which start no program. |
| **Set** | `floorPrograms(packs)` (`internal/cli/hostfloor.go:41`) — the same set `yolo host apply` uses, one authority. **Not** filtered by `cmd0`; the point is that the command does not choose the environment. |
| **Order** | `floorPrograms` order, which is the pack order. Independent programs may install concurrently as `Ensure` already allows; the per-program lock serializes one program. |
| **Prompt** | None. `HP-D3` already withdrew the host floor's consent step; a launch with no terminal installs. |
| **On success** | Continue to target resolution. Each install already prints its own line (`hostfloor.Ensure`), and a warm floor prints nothing but still probes. |
| **On failure** | **Refuse the launch** (nonzero, before the target runs), naming the program, its pack, the floor's reason or the installer's error, and the way back. This is [`OQ-JR1`](jail-notch-readiness.md#OQ-JR1)'s contract. |
| **Bypass** | `YOLO_ALLOW_MISSING_PROGRAMS=1` (`paths.AllowMissingProgramsEnv`, the jail's one spelling, [HNR-D3](#HNR-D3)): the launch proceeds and lists each program it could not install, in the jail's wording. |
| **`cmd0`** | **Exempt from the bypass.** If `cmd0`'s own install failed or has no entry, there is nothing to run, and the launch refuses in every case — today's exit 127 |
| **Refresh** | Unchanged. `Ensure` on an already-provisioned entry runs the throttled evergreen refresh and keeps the installed copy on a failed poll. Readiness is not currency ([`jail-notch-readiness.md` §1](jail-notch-readiness.md#1-readiness-and-currency-are-different-questions)). |
| **Zero declared programs** | No act, launch unchanged — the jail's `TestAJailWithNoProgramsHasNoReadinessAct` case. |

**There is one installer.** The act calls the floor's existing `Ensure`, per program, exactly as
`yolo host apply` and an `cmd0` install do; it adds no second install path and no new recipe.

## 4. A declared program is never a PATH copy (HNR-D2)

**HNR-D2: a bare name a selected pack declares is exec'd from the floor or not at all.** Where the
floor has no entry, the launch **refuses**, naming [`Status.Reason`](host-tool-provisioning.md)
(the floor's own reason: no capture, no runtime, no Landlock, outranked, no build here) and that
reason's fix. This reverses [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) (a), which
ran the child's PATH copy with one line.

Three cases stay as they are, and the design must not disturb them:

- **A program no selected pack declares** — `yolo host -- bash`, `yolo host -- rg` — is still a
  PATH lookup. The act installs declared programs and then gets out of the way; nothing here claims
  the host owns the user's whole PATH.
- **A target given as a path** (`yolo host -- ~/src/claude/dist/claude`) is still exec'd as given
  with its base name's composition ([`HP-DIR4`](host-tool-provisioning.md#HP-DIR4)).
- **A program the user's `provisioners` order hands to another manager** is still the manager's copy
  ([`PS-D12`](provisioner-sets.md), `hostOutranking` in `internal/cli/hostfloor.go:774`). That is an
  explicit user decision, not a PATH sniff, and it keeps its own miss line.

The child's PATH is untouched: the launch PATH then the floor's `bin/` last
([`OQ-HE10`](../reference/host-agent-environment.md#oq-he10), [`HE-D1`](host-launch-environment.md#he-d1)).
That rule is about what the *agent's own commands* find. This rule is about which copy yolo itself
execs for the agent, and the two were being conflated by the fallback.

## 5. Failure, ordering and existing state

The holes an implementer would otherwise fill silently:

- **A second launch while the first installs.** The floor's per-program `flock` serializes one
  program; the second launch waits on that program with the floor's existing "waiting for pid N"
  line and proceeds. No new lock. Two launches install the same set; `Ensure` is idempotent.
- **A half-installed floor from an interrupted launch.** `HP-D8`'s leftover is the floor's own;
  `Ensure` under the lock clears it on the next attempt. Do not add staging here.
- **A deselected entry** (a program no selected pack delivers any more) is never run
  ([`HP-D11`](host-tool-provisioning.md#HP-D11)); the act installs the *selected* set and does not
  resurrect a deselected entry. `yolo host apply --assert` remains what removes it.
- **A floor written by a newer yolo** refuses (`ErrNewerRecord`) and is never installed over; the
  launch's refusal is the floor's own, unchanged.
- **Offline with everything installed** — no download: `Ensure` sees a provisioned entry and takes
  the throttled refresh, whose failure keeps the copy. **Offline with something missing** — the
  refusal names `YOLO_ALLOW_MISSING_PROGRAMS=1`, and under it the launch proceeds and lists it.
- **A bypass value** is any non-empty string, as at the jail ([`JR-D3`](jail-notch-readiness.md#JR-D3));
  no other `YOLO_ALLOW_*` implies it.
- **`cmd0` is resolved after the act**, so a run that installs a program the target later resolves
  to sees the fresh launcher. Resolution order is the one observable ordering constraint.

## 6. What this costs

- **Every host launch touches the whole floor.** The first launch on a machine downloads and
  installs every declared program, `yolo host -- bash` included, where today it installed nothing.
  Later launches are probes over installed entries; the evergreen refresh stays throttled by
  `UpdateInterval`, so "every launch" is not "every download". The user asked for exactly this:
  the environment is ready whatever the command.
- **The no-entry refusal is a real break.** `yolo host -- claude` on a Mac before `yolo macos-setup`,
  or on a Linux host with neither a container runtime nor Landlock, ran a PATH copy and now refuses.
  The refusal carries the floor's reason and its fix ([`HP-D2`](host-tool-provisioning.md#HP-D2),
  [`HP-D18`](host-tool-provisioning.md#HP-D18)), and `yolo host apply` shows the whole floor at
  once. This is the single place a reviewer should push back if the parity is judged too absolute;
  [§9](#9-alternatives-considered) records the softer alternative and why it was rejected.
- **A user's own copy of a declared agent stops being a silent substitute.** That is `HP-DIR4`
  already, now enforced for the no-entry case too.
- **Deleted:** the branch of `resolveHostLaunchTarget` that runs a PATH copy for a declared
  program, and with it the `noCopyWhere` / miss-line dance for that case. The miss line stays for
  programs no pack declares and for `host_path` diagnostics.

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| First-launch latency surprises a user (several programs, gigabytes). | The install prints a line per program (existing); `yolo host apply` remains the way to pre-stage, and does not run a command. |
| An offline first launch refuses where it used to work. | The refusal names the hatch; `YOLO_ALLOW_MISSING_PROGRAMS=1` runs the target and lists what is missing. `cmd0`'s own absence still refuses. |
| A machine with no capture route becomes unusable for a declared agent. | Deliberate ([§6](#6-what-this-costs)); the refusal names the capture act's reason and fix. |
| The act installs a program the user did not want on that host. | Selecting the pack is the consent (`HP1`); the set is the selected packs', and `host_floor`/`provisioners` already scope it. |
| `yolo host -- bash` doing installs feels surprising. | It is the requested behavior; one line per install keeps it visible. |

## 8. Non-goals

- **Not `yolo host apply`.** `apply` still renders config, reports state, and prompts; the launch
  installs programs only. This does not move rendering onto the launch path.
- **Not the wrapper gate.** `host_apply_on_launch` stays the staleness net ([§2](#2-what-exists-today-and-the-asymmetry)).
- **Not the jail or macos-user readiness acts.** Their contract ([`OQ-JR1`](jail-notch-readiness.md#OQ-JR1),
  [`JR-D2`](jail-notch-readiness.md#JR-D2)) is unchanged; this mirrors it at the host.
- **Not currency.** Refresh cadence and `agent_updates` are untouched.
- **Not a new installer, floor layout, or record.** `Ensure` and the floor are reused as they are.
- **Not the child's PATH** ([`OQ-HE10`](../reference/host-agent-environment.md#oq-he10)); no entry is
  added, moved, or removed there.
- **Not `yolo host env`**, which starts no program.

## 9. Alternatives considered

- **A. Keep `HP-D3` (install only `cmd0`).** *Rejected.* It is the reported asymmetry: a launch
  cannot promise an environment the command did not name.
- **B. Install every declared program, but keep the PATH fallback for one with no entry (reverse
  only `HP-D3`).** *Rejected.* It leaves the "sniff the path" half: a declared agent would still
  sometimes be a copy yolo did not build, with no floor record of it.
- **C. Enforce the refusal but only in `yolo host apply --assert`, not at launch.** *Rejected.*
  `apply` is not on the launch path, which is exactly how the reported gap stayed invisible.
- **D. Install the whole floor lazily, on first launch touching the floor.** *Rejected.* The point
  is before the command; "whatever command you run" is not a first-touch heuristic.
- **E. Refuse on a failed install but allow the no-entry PATH fallback.** *Rejected* for the same
  reason as B. This is the one a reviewer may prefer; if so it is a smaller decision, recorded as
  a change to [HNR-D2](#HNR-D2) alone, and `HP-D3`'s reversal stands on its own.

## 10. What I would build, in order

1. **One function for the act** over `floorPrograms`, calling the existing `Floor.Ensure` per
   program, with the refusal and the `paths.AllowMissingProgramsEnv` bypass. No new installer.
2. **Call it in `hostLaunch`** before `resolveHostLaunchTarget`, and not from `hostMain`'s
   `env` / `apply` / `wrappers` arms.
3. **Change target resolution** ([HNR-D2](#HNR-D2)): a declared program with `NoEntry` refuses,
   keeping the `provisioners`-order branch and the path-target branch untouched.
4. **Reuse the jail's wording** for the refusal and the bypass line, so the two notches say the
   same thing (`packload.UnservedLines` is the precedent for one wording, two notches).
5. **Tests, at each call site:** every declared program is ensured on a launch whose command is
   neither of them (`yolo host -- true` with two selected programs); a failed install refuses and
   names the hatch; the bypass runs and lists; a declared program with no entry refuses instead of
   taking the PATH; a program no pack declares still resolves on PATH; `yolo host env` installs
   nothing; and zero declared programs changes nothing.
6. **Update the reversed rows in the same change:** [`HP-D3`](host-tool-provisioning.md#HP-D3) and
   [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) get a superseded-by note naming
   [HNR-D1](#HNR-D1) and [HNR-D2](#HNR-D2), and the launch-PATH body sentence in
   [`host-agent-environment.md`](../reference/host-agent-environment.md) that says a declared
   program falls to the PATH is rewritten, not annotated.

**Done looks like:** with two selected packs and a cold floor, `yolo host -- true` installs both
programs before exiting; with one install made to fail, the launch refuses and names
`YOLO_ALLOW_MISSING_PROGRAMS=1`; with that set it runs and lists the program; and on a host whose
floor cannot hold a declared agent, `yolo host -- <that agent>` refuses with the floor's reason
rather than running a PATH copy.

## 11. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="HNR-D1"></a>HNR-D1 | **Every host launch is a readiness act: install every program a user-scope selected pack declares, before resolving the target. A failure refuses the launch unless `YOLO_ALLOW_MISSING_PROGRAMS=1`; `cmd0` is exempt from the bypass.** Reverses [`HP-D3`](host-tool-provisioning.md#HP-D3). The maintainer's direction, 2026-10-07: *"host should act just like a jail."* | 2026-10-07 | [§3](#3-the-host-readiness-act-hnr-d1) | ✅ 2026-10-08, `hostReadinessAct`; `TestEveryHostLaunchInstallsEveryDeclaredProgramBeforeTheCommand`, `TestAHostLaunchRefusesWhenADeclaredProgramCannotBeInstalled`, `TestAHostLaunchWithNoDeclaredProgramsHasNoAct` |
| <a id="HNR-D2"></a>HNR-D2 | **A bare name a selected pack declares runs only from the floor. A declared program with no floor entry refuses, naming the floor's reason and its fix; a PATH copy is never a substitute.** The user's `provisioners` order is excepted, being explicit. Reverses [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) (a). Maintainer: *"not sniff the path."* | 2026-10-07 | [§4](#4-a-declared-program-is-never-a-path-copy-hnr-d2) | ✅ 2026-10-08, `resolveHostLaunchTarget`; `TestHostLaunchOnAMachineWithoutTheFloorsLoaderRefusesAndNamesTheStep`, `TestTheNoCopyLineNamesTheFloorsPlatform` and the fork and patched-fork cases |
| <a id="HNR-D3"></a>HNR-D3 | **The bypass is `paths.AllowMissingProgramsEnv` (`YOLO_ALLOW_MISSING_PROGRAMS`), the jail's one spelling and any-non-empty semantics ([`JR-D3`](jail-notch-readiness.md#JR-D3)); it never applies to `cmd0`.** | 2026-10-07 | [§3](#3-the-host-readiness-act-hnr-d1), [§5](#5-failure-ordering-and-existing-state) | ✅ 2026-10-08; `TestTheBypassStartsTheLaunchAndListsWhatIsMissing`, `TestTheBypassNeverRunsTheLaunchsOwnMissingProgram` |
| <a id="HNR-D4"></a>HNR-D4 | *Implementation decision, from the build:* **a program that is not the floor's to hold is not a readiness failure and keeps the PATH lookup: a pack the user-scope `host_floor` leaves out, a program the `provisioners` order gives to a manager (already excepted by [HNR-D2](#HNR-D2)), and a program whose vendor publishes no build for this platform.** The act skips them without a line, and a launch of one still runs the PATH copy with the no-copy line. Refusing on `host_floor` would make `host_floor: false`, documented as "a floor of nothing" ([HP-D5](host-tool-provisioning.md#HP-D5)), a way to make every declared agent unlaunchable at the host; the unpublished case is the jail's own rule, which writes no launcher for such a program and so finds it on the PATH. The act also skips a kind the jail's act does not install (only npm, installer and source programs count). `yolo check`'s host-floor and model-list rows now say a launch refuses for every other no-entry program | 2026-10-08 | [§4](#4-a-declared-program-is-never-a-path-copy-hnr-d2) | ✅ `hostfloor.Floor.OutsideTheFloor`; `TestTheActSkipsWhatTheUserLeavesOutOfTheFloor`, `TestHostLaunchOfAProgramTheUserLeavesOutOfTheFloorRunsThePATHCopyAndSaysSo`, `TestAnUnpublishedProgramRunsThePATHCopyAndIsNotAReadinessFailure`, `TestCheckNamesTheLoaderAProgramLacksAndTheNixLDStep` |
| <a id="HNR-D5"></a>HNR-D5 | *Implementation decision, from the build:* **the act never installs the program the launch itself runs.** A bare name's install stays target resolution's, with its own refusal (so it is installed once, and [HNR-D3](#HNR-D3) keeps the bypass off it). A target given as a path is the user's own copy of its base name's program ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)), so the floor's copy of that name is not installed for it; the other declared programs still are | 2026-10-08 | [§3](#3-the-host-readiness-act-hnr-d1) | ✅ `TestAnAgentLaunchInstallsTheOtherDeclaredProgramsAndItselfOnce`, `TestAPathTargetsOwnProgramIsNotInstalledByTheAct` |
| <a id="HNR-D6"></a>HNR-D6 | *Implementation decision, from the build:* **an MCP server's program a selected pack declares is a declared program like any other, so a failed install of it refuses the launch**, as the jail's act does for the same program. This narrows [HC-D28](host-computed-layer.md#HC-D28)'s "the agent starts either way" to the programs the act does not cover (one the user leaves out of the floor). `ensureMCPPrograms` skips a program the act already settled, so one launch never tries an install twice | 2026-10-08 | [§3](#3-the-host-readiness-act-hnr-d1) | ✅ `TestAHostAgentLaunchSaysWhenItsMCPServersProgramCannotBeInstalled` |
| <a id="HNR-D7"></a>HNR-D7 | *Implementation decision, from the build:* **the act installs one program at a time, in pack order, and its refusal exits 1** (the target's own refusal keeps exit 127, "not available"). Running installs in parallel would interleave their progress lines; the per-program lock already serializes concurrent launches. No act runs in a jail, which has no floor and whose boot already ran its own | 2026-10-08 | [§3](#3-the-host-readiness-act-hnr-d1), [§5](#5-failure-ordering-and-existing-state) | ✅ `hostReadinessAct` |
