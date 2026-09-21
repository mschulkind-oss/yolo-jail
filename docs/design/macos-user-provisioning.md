---
title: "macos-user has no floor and no provisioning stage"
status: deprecated
date: 2026-09-04
superseded_by: ../reference/macos-user-provisioning.md
tags: [macos-user, provisioning, packages, mise, backend-parity, superseded]
summary: "SUPERSEDED — graduated to docs/reference/macos-user-provisioning.md. Both halves this design proposed are built and measured; the system as it stands, the rulings worth keeping (OQ-P1 through OQ-P5), and the one cost still unmeasured live in the reference."
---

# macos-user has no floor and no provisioning stage

**Status:** SUPERSEDED, 2026-09-21 — graduated to
[**`../reference/macos-user-provisioning.md`**](../reference/macos-user-provisioning.md).

This was a design doc, and it had no live questions left: both halves it proposed — a native
darwin **floor** for the noncontainer profile, and a Seatbelt-confined provisioning **stage**
between the bootstrap and the agent — shipped on 2026-09-12, and all five of its questions were
ruled. Once a design is built, its reader stops being someone deciding and becomes a
maintainer, which is a different genre. So the content moved.

**Go to the reference for anything you came here for.** It states the system as built, in the
present tense, and carries:

- the floor, its two-kinds-of-exclusion split, and why the fatal covers only one of them —
  [The floor](../reference/macos-user-provisioning.md#the-floor);
- the stage's argv, its four-of-six step set, its skip rule, the sidecar paths and the session
  env file — [The stage](../reference/macos-user-provisioning.md#the-stage);
- why no argv in this backend carries `sudo --login` —
  [No `sudo --login`](../reference/macos-user-provisioning.md#no-sudo---login-anywhere-in-this-backend);
- the failure policy, and why the `PROVISIONING FAILED` marker is the discriminator —
  [A failing stage does not abort the launch](../reference/macos-user-provisioning.md#a-failing-stage-does-not-abort-the-launch);
- where the stage's state lands —
  [Where the stage's state lands](../reference/macos-user-provisioning.md#where-the-stages-state-lands);
- [`OQ-P1`](../reference/macos-user-provisioning.md#oq-p1) through
  [`OQ-P5`](../reference/macos-user-provisioning.md#oq-p5) and the four principles, with the reasons
  a maintainer would otherwise re-derive —
  [Why it is this way](../reference/macos-user-provisioning.md#why-it-is-this-way);
- what a hardware session and the nightly CI job have measured, and the **one cost nobody has
  recorded** —
  [The one unmeasured claim](../reference/macos-user-provisioning.md#the-one-unmeasured-claim--what-a-first-stage-costs).

⚠ **The anchors a Go comment names, routed explicitly.** A citation in source cannot be found
and repointed by any markdown tool, and this page's section numbers are gone — so the forms that
reach it from code get a row each. Measured 2026-09-21 by grepping `internal/`, `cmd/`,
`integration/` and `flake.nix` for this basename; this is the whole set:

| Cited as | Cited from | Now |
| :--- | :--- | :--- |
| [`§1.1`](../reference/macos-user-provisioning.md#no-sudo---login-anywhere-in-this-backend) | `internal/macosuser` (2 sites) | [No `sudo --login`](../reference/macos-user-provisioning.md#no-sudo---login-anywhere-in-this-backend) — ⚠ and the design was **false** where it said the launch argv keeps the flag: `PlanInvariants` refuses it on BOTH argvs, so a comment repeating that is repeating a retracted claim |
| *half one* / *half two* | `internal/entrypoint`, `internal/macosuser` | half one is [The floor](../reference/macos-user-provisioning.md#the-floor), half two is [The stage](../reference/macos-user-provisioning.md#the-stage) |
| *the imperative keys*, *four config keys*, *the note under…* | `internal/cli/run`, `internal/config` | [What each imperative config key delivers](../reference/macos-user-provisioning.md#what-each-imperative-config-key-delivers-here) |
| [`OQ-P1`](../reference/macos-user-provisioning.md#oq-p1)–[`OQ-P5`](../reference/macos-user-provisioning.md#oq-p5) | several, by id | unchanged spelling, anchored in the reference — which is why the ids survived graduation at all |

**This file is kept only because inbound links exist**, including source comments that cite it
by path. It is not maintained; nothing here should be cited in preference to the reference.
