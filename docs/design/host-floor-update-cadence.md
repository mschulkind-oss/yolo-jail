---
title: "The host floor checks for updates once per host start, not once an hour"
date: 2026-11-27
status: in-review
tags: [host-floor, updates, program-delivery, host, evergreen]
summary: "The host floor's hourly evergreen poll and its hourly pre-launch refresh go. A top-level `yolo host --` launch checks every program once, in parallel under one short deadline; a launch nested inside another host launch does not. Two questions are open: what counts as a start, and what an offline start pays."
stage: DESIGN
next: "Rule OQ-HF1 and OQ-HF2; the clock-free part (delete the hourly gate, check without the lock, one parallel phase) builds under either answer"
---

# The host floor checks for updates once per host start, not once an hour

**Status:** 2026-11-27. Nothing built. Code claims read at `fd75c97c4` ([Appendix A](#appendix-a-evidence)); no timing here is measured.

> **In short.** The owner's rule for launchers is "check once per start; relaunching is how you pick up a new
> version" ([LU-D1](launcher-update-per-jail-start.md#decision-ledger)). The host has no jail start, so its start is the
> top-level `yolo host --` launch: it asks every program once, all at once under one short deadline, and a launch
> nested inside it asks nothing.

**Why it matters.** `yolo host -- claude` right after a release still runs the old version for up to an hour, while a jail
launcher and the pack check already follow the ruling ([LU-D11](launcher-update-per-jail-start.md#decision-ledger)).

**The shape.** A **host start** ([Vocabulary](#vocabulary)) gates one **check phase**; what the phase finds is acted on
under the program's own lock. No clock decides anything.

**Cost.** An offline top-level launch pays the deadline each time instead of once an hour ([OQ-HF2](#OQ-HF2) is the open part).

**Start at [The rule](#the-rule).**

**Needs your ruling:** [OQ-HF1](#OQ-HF1), [OQ-HF2](#OQ-HF2).

**Reads with:** [`launcher-update-per-jail-start.md`](launcher-update-per-jail-start.md) (the rule this carries to the host),
[`local-pack-clones.md`](local-pack-clones.md) (the pack check, which owns the sibling branch refresh and the probe-and-deadline
shape reused here), [`host-agent-environment.md`](../reference/host-agent-environment.md#HP-D13) (the floor and the rows this
changes), [`program-delivery.md`](program-delivery.md#PD-NF3) (the failure rules).

---

## Goal and non-goals

**Goal.** A user who relaunches an agent at the host after a release runs the new version, and no launch waits long for a
check that finds nothing.

**Non-goals.**

- **The pack branch refresh and the patched-fork upstream check.** [`local-pack-clones.md`](local-pack-clones.md) owns them; the host launch
  already runs them at every invocation (`refreshHostPacks`, under its 5 s deadline).
- **Pinned programs, `agent_updates` holds, the no-silent-fallback refusals ([PD-NF1](program-delivery.md#PD-NF1)) and a fork's
  build.** None is a throttle; all are untouched.
- **The jail's launchers.** Their rule is built; where a question below would change them too, it says so.

## What exists today

| Piece | Today | Evidence |
| :--- | :--- | :--- |
| The poll's gate | `Floor.refresh` skips a program checked less than `DefaultUpdateInterval` (1 h) ago, read from the record's `checked` time | `ensure.go`, `floor.go` |
| Stamp on any outcome | `checked` is written **before** the poll, so a registry that is down is not asked again for an hour; a refused update and a busy capture put the old value back | `refresh`, `failedUpdate` |
| Who reads `checked` | Only that gate (and the install, which sets it to the install time); no verb reports it | `rg` over `internal/` |
| What one launch asks | The readiness act runs `Ensure` for every other program a selected pack declares, one after another; target resolution runs it for the command; the MCP step for the servers' programs | `hostReadinessAct`, `resolveHostLaunchTarget`, `ensureMCPPrograms` |
| The poll's cost | One `npm view`, announced by one line per program before it, bounded at 60 s (`DefaultPollTimeout`), run holding the program's lock | `newerThan`, `refresh` |
| The installer recipe | Nothing newer in the capture store: a capture runs once the newest capture is 24 h old (`DefaultCaptureRefreshAge`, [HP-D16](../reference/host-agent-environment.md#HP-D16)) | `recaptureWhenOld` |
| The pre-launch refresh | Due when its stamp is missing or an hour old, or when watched content is unseen; its stamp is `refresh/<bin>.stamp` | `refreshDue`, `PrelaunchRefresh` |
| Who runs the floor's copy | The launcher in `bin/` execs the program and decides nothing; the wrapper a user puts on PATH runs `yolo host -- <bin>` | `launcherScript`, `hostwrap.BodyFor` |

Two consequences drive the design. Deleting the gate alone would make **every launch poll every program one after another,
each up to 60 s, holding each program's lock**. And a **subagent fan-out reaches `yolo host --` only through a wrapper**
that sits ahead of the floor's `bin/` on the agent's PATH; the floor's own launcher never polls. Whether a real agent's PATH
puts a wrapper first is NOT MEASURED.

## Vocabulary

- **Host start** *(coined here)*: a `yolo host --` launch, or a `yolo host apply --assert`, that is not nested in another.
  It is the host's counterpart of a [jail start](launcher-update-per-jail-start.md#vocabulary).
- **Nested launch** *(coined here)*: a `yolo host --` run by a process that descends from a host start still running
  ([OQ-HF1](#OQ-HF1) decides whether the distinction exists).
- **Check phase** *(coined here)*: the one step of a host start in which every evergreen-eligible program is asked
  whether a newer version exists. **Eligible** means installed, not pinned, not a fork's build, allowed by `agent_updates`,
  and inside the floor.
- **Verdict**: a program's answer from the phase: current, a newer version, or could not check (with why).

## The rule

*Written under [OQ-HF1](#OQ-HF1)'s leaning, B. Under A there are no nested launches and every other line stands.*

### The start

A host start mints a **start token** and puts it in the child's environment, and only the child's: never the launching
process's own. A `yolo host --` that finds a token whose minting launch is **still running** is nested: it runs no check
phase and no pre-launch refresh beyond [what a changed settings file still asks](#the-pre-launch-refresh), and it passes the same token on.
A token whose minter has gone, an unreadable token and no token all make a start. Every failure to prove nesting
therefore checks, and a surviving token never silences a floor for longer than its launch lives: a terminal multiplexer
started inside an agent and left for weeks does not turn every later launch into a nested one.

| Question | Answer |
| :--- | :--- |
| Where does the marker live? | In the process tree: one environment variable of the child. Nothing on disk |
| Who owns its lifetime? | The minting launch: it exists exactly as long as that process, and nothing needs collecting |
| What can it do? | Suppress a check, which keeps the installed version. It grants nothing, and `agent_updates` and pins are read as before |
| What does `env -i yolo host -- <bin>` (a launcher that scrubs the environment) do? | Starts: it checks |

### The check phase

Before the readiness act, the launch asks, **concurrently and in one phase**, every eligible program among the readiness set,
the command's program and the MCP servers' programs. The phase has **one deadline, `FloorCheckDeadline`, 8 s** (new, a
constant, tests inject the context), not 8 s per program. A poll still running at the deadline is cancelled and reads
as could not check. The verdicts are kept for this launch, and the later `Ensure` of that program uses its verdict and never polls again.

- The 8 s is the existing upstream-probe bound ([`UpstreamProbeTimeout`](program-delivery.md#PD-NF3)): an `npm view` starts
  Node before it asks the registry, so it gets more than the pack check's 5 s.
- An installer program's check is its offline read of the capture store, so it adds no network wait to the phase.
- `yolo host --update` keeps the 60 s serial poll of the program it names ([`--update`](#--update-the-capture-age-and-what-stays)).

### The act, under the lock

The poll takes **no lock**: it only reads. A program with a newer verdict then takes its lock without waiting, **re-reads its
record under it** (another launch may have installed that version), and installs. A lock another launch holds is today's
`another update is in progress` line, and the installed version runs. Only an install can meet that line now; a poll cannot.

### The failure rule

Nothing is stamped on any outcome, so every row below says only whether the next start asks again.

| Outcome | What runs and what is said | Next start |
| :--- | :--- | :--- |
| Registry did not answer, or the deadline passed | The installed version runs. `yolo host: could not check claude for a newer version (…); running the installed 2.1.0. It asks again at the next start, or now with: yolo host --update -- claude` | Asks again |
| A newer version found, its install fails | PD-NF1's refusal block, exit unchanged | Asks again (there is no stamp to take back) |
| Another launch's install holds the lock | One line; the installed version runs | Asks again |
| `agent_updates` holds the pack, or the version is pinned | Nothing | Nothing |
| The capture is busy (another `yolo capture` of it) | One line; the installed version runs | Asks again |

Today's wording lacks the next step on the first row ([happy-path-principle.md](../reference/happy-path-principle.md)); it gains it.

### The pre-launch refresh

`pi update --extensions` follows the same start rule, so the host and the jail agree ([LU-D8](launcher-update-per-jail-start.md#decision-ledger)).
It is due at a start; it is also due when a watched file holds content no refresh has succeeded with, **at a start only**: a
nested launch never refreshes, so a refresh that fails on new content is retried at the next start and not at every launch, as
[XB-D26](pi-extension-store-builds.md#XB-D26) rules for the jail. Its `refresh/<bin>.stamp` file has no reader and goes; `<bin>.seen/`
and the lock stay, and a stamp an older yolo left is ignored and never deleted. Its 60 s bound is unchanged: it is an act, not a probe.

### `--update`, the capture age and what stays

- **`--update` (`ManualUpdate`)** keeps its meaning: it checks now whatever the start, the token, `agent_updates` or the capture
  age says, and still honors pins, provisioner ownership and platform rules. Nothing remains for it to bypass in a throttle.
- **The 24 h capture age stays ([HP-D16](../reference/host-agent-environment.md#HP-D16)).** A capture boots a jail or the sandbox
  account and runs the vendor's installer, which is the expense the owner's "as long as the check is not expensive" excludes, and
  it is not a poll of anything. With the hourly gate gone its age test runs at every start, and it costs one `stat`. The cost is an
  asymmetry with the jail's native launcher, which asks the vendor's update verb at each start; a newer cheap signal for a vendor
  release would close it.
- **`yolo host apply --assert`** is a host start and runs the phase, as it runs `Ensure` today.

### Disclosures

A launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)), but progress may be compressed. The per-program line
before each poll becomes **one line per phase** naming the programs that ask the registry and the deadline (`yolo host:
checking claude, pi for newer versions (at most 8 s)`), and it prints only when such a program is in the phase. A current program
says nothing. A found update prints today's `updating …` and `updated …` lines.

## Open questions

1. 💬 **OQ-HF1: What counts as one start at the host?**

   Maya runs `yolo host -- pi`. Her orchestrating pi starts twenty sub-pis in a minute, each through the wrapper directory on her PATH, so each one is its own `yolo host -- pi`. The hourly gate that kept this to one registry check an hour is going, and the owner's rule says "once per start", but a host has no jail whose start can be counted. Her script that loops over the wrapper from a bare shell is a different case.

   - **A — Every launch checks.** Maya's twenty sub-pis each ask the registry, and each runs `pi update --extensions`. Simplest, and what the pack check already does at every launch. A bare-shell loop and the fan-out cost the same.
   - **B — A launch checks unless it runs inside another host launch.** The twenty inherit the orchestrator's start and ask nothing; her bare-shell loop still checks each time. Relaunching pi from a terminal is how she picks up a release. A lost token only causes an extra check.
   - **C — Once per login or boot.** After a release, relaunching pi still runs the old one until she reboots, which is the opposite of "relaunch is how you pick up changes".

   A short clock window (a check skipped when one ran a minute ago) was rejected in the body: it is a clock decision, the thing the owner removed.

   <!-- vantage: question id=OQ-HF1 leaning="B (inside another host launch checks nothing) — the jail's rule at the host: one check per start, fan-out pays once, no file to own; A is the fallback and only the gate changes." -->

   _Leaning:_ B (inside another host launch checks nothing) — it is the jail's rule at the host: one check per start, a fan-out pays once, no file to own. A is the fallback, and only the gate differs.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-HF2: Should an offline start probe the upstream before it runs an expensive check, or after it fails?**

   Sam is on a flaky tether when he runs `yolo host -- pi`. pi's refresh runs `pi update --extensions`, bounded at 60 s, and only after it fails does the floor probe the registry for 8 s and say "could not check" ([PD-NF3](program-delivery.md#PD-NF3) chose this order so a success makes no extra request). With the hourly stamp gone that is up to about 68 s at every start, instead of once an hour; typical offline time is NOT MEASURED, and a DNS failure returns at once. The same applies to claude: with its newest capture 3 days old and no network, each start would boot a capture jail that then fails.

   - **A — Keep probing after the failure.** An online start makes no extra request; an offline one may wait about 68 s (pi) or a capture jail's run (claude), at every start.
   - **B — Probe first, then run the act.** An online start makes one extra 8 s-bounded request; an offline one waits 8 s and says "could not check", at every start. The jail's launchers have the same shape per jail start, so the same change would be owed there, in their doc.

   <!-- vantage: question id=OQ-HF2 leaning="B (probe first) — an offline start waits 8 s instead of about 68 s, and the extra request is one HEAD when online; the jail's owed change is a separate doc." -->

   _Leaning:_ B (probe first) — an offline start waits 8 s instead of about 68 s, and the extra request is one HEAD when online. The jail's matching change is another doc's.

   **Answer:**
   > _(empty — fill in when decided)_

## Test impact

Every row must fail if its production call site is deleted, and none waits on a clock or the network (fake `npm` that logs each call, as `newWorld` does).

| Existing test | Change |
| :--- | :--- |
| `internal/hostfloor/floor_test.go` `TestTheEvergreenRefreshIsThrottledAndFollowsAgentUpdates` | Replaced: two `Ensure`s make two `npm view`s with no clock moved; a pinned program, a frozen pack and a nested launch make none; `ManualUpdate` makes one |
| `internal/hostfloor/prelaunch_test.go` `TestPrelaunchRefreshRunsOnceAnIntervalOnItsOwnStamp` (reads `DefaultUpdateInterval`) | Replaced: a start refreshes, a nested launch does not, changed content refreshes at a start and fails closed until the next; no `pi.stamp` is written |
| `internal/hostfloor/evergreen_test.go` `TestNoRecaptureRunsInsideTheAgeOrTheIntervalOrUnderAHold`, `…WaitsOutTheInterval` | The stamp half is deleted; the age and hold halves stay; the offline test follows [OQ-HF2](#OQ-HF2)'s answer |
| `internal/cli/hostfloorwiring_test.go` (3 sites), `hostnofallback_test.go` | Drop `f.UpdateInterval = time.Nanosecond` (the field is deleted); `CaptureRefreshAge` lines stay |
| `integration/hostfloor_test.go` `ageFloorRecordCheck` and its call | Deleted: nothing gates on `checked`, so the pinned-program check no longer needs it to reach the pinned rule |
| New, at the call site in `internal/cli` | A host launch's child environment carries a token; a launch started with a live token runs no poll and no refresh; one started with a dead, unreadable or no token does. The token goes into the child's environment only, so the tests that launch repeatedly in one process each remain a start. A dead minter is a helper process the test waits on |

## Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| HF-D1 | **Owner ruling (standing).** *"Drop every hourly refresh throttle (packs, patched forks, launchers); launchers check once per jail start."* The host floor's evergreen poll and pre-launch refresh are the part not yet designed ([LU-D11](launcher-update-per-jail-start.md#decision-ledger)) | 2026-11-23 | [What exists today](#what-exists-today) | no |
| HF-D2 | *Implementation decision, reversible.* `DefaultUpdateInterval`, `Floor.UpdateInterval`, the gate on `checked` and `refreshDue`'s age test are deleted, and no constant replaces them. `checked` stays in record schema 1, written by the install only and read by nothing: no schema bump, and an older yolo that still gates on it only polls more | 2026-11-27 | [The failure rule](#the-failure-rule) | no |
| HF-D3 | *Implementation decision, reversible.* The poll takes no lock; the act takes it without waiting and re-reads the record under it. The stamp-first rule and its two take-backs are deleted | 2026-11-27 | [The act](#the-act-under-the-lock) | no |
| HF-D4 | *Implementation decision, reversible.* One concurrent check phase under one 8 s deadline (`FloorCheckDeadline`); a launch's later `Ensure` consumes the verdict. Without it, deleting the gate makes a launch run every poll serially at up to 60 s each | 2026-11-27 | [The check phase](#the-check-phase) | no |
| HF-D5 | *Implementation decision, reversible.* No outcome is stamped; every failed or deadline-hit check is asked again at the next start, with one line naming `yolo host --update -- <bin>` | 2026-11-27 | [The failure rule](#the-failure-rule) | no |
| HF-D6 | *Implementation decision, reversible.* The 24 h capture age stays; a capture is not a cheap check, and the owner's rule is conditioned on cheap | 2026-11-27 | [`--update`…](#--update-the-capture-age-and-what-stays) | no |
| HF-D7 | *Implementation decision, reversible.* `--update` bypasses the start rule, `agent_updates` and the capture age, keeps pins, and keeps the 60 s serial bound | 2026-11-27 | [`--update`…](#--update-the-capture-age-and-what-stays) | no |
| HF-D8 | *Implementation decision, reversible.* The pre-launch refresh follows the start rule; its stamp file is deleted; a refresh failing on changed content is retried at the next start only | 2026-11-27 | [The pre-launch refresh](#the-pre-launch-refresh) | no |
| HF-D9 | *Implementation decision, reversible.* The progress line is one per phase, printed only when a registry poll is in it | 2026-11-27 | [Disclosures](#disclosures) | no |
| HF-D10 | *Implementation decision, reversible.* `yolo host apply --assert` is a start; the pack branch refresh stays [`local-pack-clones.md`](local-pack-clones.md)'s | 2026-11-27 | [`--update`…](#--update-the-capture-age-and-what-stays) | no |

## Appendix A: Evidence

Read at `fd75c97c4`, 2026-11-27, in the source; nothing was run.

- The gate, the stamp-first write and its two take-backs: `Floor.refresh` and `failedUpdate` in `internal/hostfloor/ensure.go`.
  `checked` is read by `refresh` and set to the install time by the install, and by nothing else in `internal/` (`rg`).
- The constants: `DefaultPollTimeout` (60 s), `DefaultUpdateInterval` (1 h), `DefaultCaptureRefreshAge` (24 h) in
  `internal/hostfloor/floor.go`; `UpstreamProbeTimeout` (8 s) in `nofallback.go`; `PackCheckDeadline` (5 s) in `internal/packsrc/refresh.go`.
- The calls one launch makes: `hostReadinessAct` (serial loop, skips the command's own program), `resolveHostLaunchTarget`,
  `ensureMCPPrograms` (skips what the act settled), and the pack refresh `refreshHostPacks` at the top of `hostExec`.
- The refresh's gate and stamp: `refreshDue`, `touchRefreshStamp`, `refreshStampPath` in `internal/hostfloor/prelaunch.go`; the content
  rule `contentUnseen` bypasses the stamp, so a failing refresh on changed content is due at every launch today.
- The floor launcher execs the program and decides nothing (`launcherScript`); the wrapper execs `yolo host -- <bin>` (`hostwrap.BodyFor`).
- `refresh` also calls `selectPreparedNPM` with the install timeout **before** the hourly gate; it already runs at every launch and this design does not move it.
- NOT MEASURED: any launch's duration; `npm view` and `pi update --extensions` online and offline; whether an agent's PATH puts the
  wrapper ahead of the floor's `bin/`; a process-start-time reader on darwin (`internal/prune/procstart_linux.go` is the Linux one; check before relying on a token's liveness check there).
