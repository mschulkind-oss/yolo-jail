---
title: "Plan: start a jail daemon on macos-user — graduated"
date: 2026-09-17
status: accepted
tags: [macos-user, loopholes, jail-daemon, parity, plan, graduated]
summary: "A pointer. Every step of this plan is built, and its settled content graduated on 2026-10-01 into docs/reference/macos-user-nix-and-features.md: how the sandbox runs the jail daemons, what it declines and why, the supervisor's start, stop and log, the token-file boundary, the traps, and the decisions JD-1 to JD-8 except JD-4. What stays here is JD-4, a follow-up question parked for the maintainer: whether the wire bridge's jail daemon should move into the guest."
stage: GRADUATED
next: "Nothing is owed by this file. JD-4 waits on the maintainer, outside the queue; delete this file once it is ruled or filed elsewhere and the roadmap's two links move"
vantage:
  status-chip: true
---

# Plan: start a jail daemon on macos-user — graduated

**Status:** 2026-10-01 — every step of this plan is in the tree, and its settled content is now
[`macos-user-nix-and-features.md`'s jail-daemon section](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox),
which is the authority: the darwin guest set and its prefix, the supervisor and its log, the
shapes the sandbox declines, the token files, the warnings this plan carried as traps, and the
implementation decisions in its [Why it's this way](../reference/macos-user-nix-and-features.md#why-its-this-way)
table. MEASURED: `TestMacosUserJailDaemonRunsConfinedInTheGuest` passed in the `macos-user.yml`
run 36719581090 (2026-09-30, at `8f7468dd`). UNMEASURED, a human at a Mac: two concurrent
launches of one workspace, the start time against the readiness bound, and what a real `sudo -n`
refusal or `sandbox-exec` denial prints. The plan's map, reuse list, build order and
verification split described the tree before the build; they are in git history
(`git log --follow -- docs/design/jail-daemon-on-macos-user-plan.md`).

**Needs your ruling:** nothing this file owes. [JD-4](#decision-ledger) is a follow-up the build
surfaced and left for the maintainer, outside the queue.

## Decision ledger

The rulings are [OQ-DP8](declaration-parity.md#OQ-DP8) and [OQ-DP9](declaration-parity.md#OQ-DP9).
JD is this plan's own prefix for the implementation decisions the build took under them.

| ID | Decision | Now resolves at |
| :--- | :--- | :--- |
| JD-1 | The guest set is `yolo-jaild` alone | [`jd-1`](../reference/macos-user-nix-and-features.md#jd-1) |
| JD-2 | The guest prefix is `/var/yolo-jail/bin`, and the staged `yolo` lives in it | [`jd-2`](../reference/macos-user-nix-and-features.md#jd-2) |
| JD-3 | What the guest declines is one split, `loopholes.JailDaemonsRunIn` | [`jd-3`](../reference/macos-user-nix-and-features.md#jd-3) |
| <a id="JD-4"></a>JD-4 | **The wire bridge keeps its host half on macos-user, and whether its jail daemon should move into the guest is a follow-up for the maintainer, not decided.** Its jail daemon publishes at `/run/yolo-services`, which the guest has no counterpart of, and NC-D65 already serves the bridge launch-owned. Moving it into the guest would retire that host half. As built, the guest declines a pack service's daemon by name ([the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox)) | stays here |
| JD-5 | The supervisor reads an env file of its own | [`jd-5`](../reference/macos-user-nix-and-features.md#jd-5) |
| JD-6 | Started after the provisioning stage and before the agent, under `sudo -n`, in its own process group | [`jd-6`](../reference/macos-user-nix-and-features.md#jd-6) |
| JD-7 | macos-user picks served addresses for the daemons it runs | [`jd-7`](../reference/macos-user-nix-and-features.md#jd-7) |
| JD-8 | The supervisor's own output goes to `supervisor.log`, and "Started" waits for its readiness line | [`jd-8`](../reference/macos-user-nix-and-features.md#jd-8) |
