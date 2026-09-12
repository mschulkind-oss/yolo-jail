---
title: "Handoff: what the first macos-user hardware run left open"
status: handoff
date: 2026-09-12
tags: [macos-user, handoff, lsp, integration, provisioning]
summary: "The macos-user manual-checks runbook was run end to end on hardware for the first time on 2026-09-12. All ten items now have a measurement, three defects were found and fixed that day, and six threads are left. One is a confirmed product defect with three false published claims riding on it (lsp_servers installs nothing here); one is a test-suite design flaw that only bites a persistent Mac; four are automation gaps that are work nobody has done rather than problems."
---

# Handoff: what the first macos-user hardware run left open

**Audience:** the next agent working on `macos-user`, on a Mac or off it. Each thread below
says which it needs.

**Status:** **HANDOFF.** Written 2026-09-12, at the end of the session that ran
[`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md) end to end on
the maintainer's Apple Silicon Mac (macOS 26.5, arm64) — the first time items 5-10 or any of
their automated twins had executed anywhere. **Nothing here is speculative about whether the
backend works:** it does, and the run says so. What is left is one product defect, one
test-design flaw, and four pieces of automation nobody has written.

**What the session settled, so you do not re-do it:** every item of that runbook passes on
hardware, the six automated twins run `executed=6 skipped=0` with **one** subtest red
([§1](#1-lsp_servers-installs-nothing-on-this-backend--confirmed-and-three-published-claims-ride-on-it)),
and [`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger)'s floor claim — the
runbook's own "single largest unmeasured claim of the whole pair" — is measured. Three defects
were found and fixed the same day (`2a4ac34e`, `28caa116`, `92244306`); each is recorded at the
runbook item that found it, and none is open work.

**Reads with:** [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md)
(the spec, and the results), [`../design/macos-user-provisioning.md`](../design/macos-user-provisioning.md)
(the floor and the stage; [§1.1](../design/macos-user-provisioning.md#11-the-forwarded-command-is-not-passed-through-faithfully)
is the forwarding defect, now fixed, and [§10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)
owns the ruling [§1](#1-lsp_servers-installs-nothing-on-this-backend--confirmed-and-three-published-claims-ride-on-it)
below needs from you), [`../design/macos-user-home-tiers.md` §10](../design/macos-user-home-tiers.md#10-what-shipped)
(the layout, and the sixth ordering rule the run added), and
[`../research/macos-support-matrix.md`](../research/macos-support-matrix.md) (the cells this
session moved).

---

## 1. `lsp_servers` installs nothing on this backend — CONFIRMED, and three published claims ride on it

**The one red in an otherwise green suite**, and the only open PRODUCT defect here.
`TestMacosUserDeclaredToolsArrive/lsp_servers` fails; its three sibling subtests
(`mise_tools`, `mise_store_is_machine_tier`, `npm_prefix_is_workspace_tier`) pass. Predicted
from a source reading on 2026-09-12 by runbook item 9's ⚠, and **now measured** on hardware.

**The chain, verified against the tree 2026-09-12.** The generated bootstrap script installs
from two variables, and the **only producer of either in the whole tree** is the container's
podman `-e` lines:

| Role | Site |
| :--- | :--- |
| producer (container only) | `internal/cli/run/assemble.go:866-867` — `-e YOLO_LSP_NPM_INSTALL=…`, `-e YOLO_LSP_GO_INSTALL=…` |
| reader — the generated script | `internal/entrypoint/shell.go:368,371` |
| reader — the boot catalog | `internal/entrypoint/catalog.go:243,396` |
| reader — the refresh path | `internal/entrypoint/serverrefresh.go:212,215` |

`macos-user` sets `YOLO_LSP_SERVERS` — the table that RENDERS agent config — and neither
install variable. So the confined stage execs the script, finds an empty install list, and
**exits 0 having installed nothing**: precisely the "reports success having provisioned
nothing" mode the stage was warned about.

**What a fix has to do.** Set both variables into **the stage env AND the bootstrap env** — the
readers above live in two different processes, so setting one is a half-fix that still leaves a
green-looking launch. `internal/macosuser/runplan.go` builds both env lists;
`macosuser.SandboxPath`'s `~/.npm-global/bin` is already on PATH and is already a
workspace-tier symlink (the passing `npm_prefix_is_workspace_tier` subtest proves the
destination is ready), so this is a wiring change, not a plumbing one.

> [!IMPORTANT]
> **THE RULING IS THE MAINTAINER'S, AND IT IS NOT "OBVIOUSLY FIX IT".**
> [§10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)
> frames the outcome as a choice: **wire the variables, or bring the retired launch warning
> back.** It retired two warnings on the strength of code that had never run; the `mise_tools`
> half of that retirement is now measured and correct, and the `lsp_servers` half is measured
> and wrong. Do not pick for the maintainer.

**The three published claims that said otherwise are ALREADY CORRECTED (2026-09-12) — you do not
have a doc sweep to do, you have a ruling to get.** Each now records the measurement and points
here:

| Doc | Was | Is |
| :--- | :--- | :--- |
| [`../guides/macos.md`](../guides/macos.md) | `lsp_servers \| installed, since 2026-09-12 … ⚠ NOT MEASURED` | **NOT installed — MEASURED FALSE**, with the chain |
| [`../design/provisioner-sets.md`](../design/provisioner-sets.md) row 4 | `lsp_servers drives, since 2026-09-12 … ⚠ NOT MEASURED` | **does NOT drive — MEASURED FALSE** |
| [`../design/macos-user-provisioning.md` §10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them) | two warnings retired | one retirement **was wrong**, and §10.6's own rule cuts the other way for that half |

⚠ **Both rows had hedged with "NOT MEASURED on hardware", which is the tell worth noticing:** the
hedge was honest and the claim beside it was still wrong, so a reader who trusted the row got a
feature that does not exist. When you close this, the rows move again — to *installed* with a
date, or to *absent and warned*.

The runbook's item 9 and its *Known-absent* entry already describe the absence and name this
chain; they move in the same pass.

---

## 2. The twin suite poisons itself in test order — a persistent Mac only

**Not a product defect, and not visible on CI.** Every twin launches into its own workspace, and
a launch repoints the **machine-scope** account home's layout symlinks at *that* workspace. The
test then deletes the workspace. The links are left dangling for whatever runs next.

`92244306` made that survivable — a launch no longer refuses over a stale link it does not
declare — so the suite is green today. What it did not do is make the suite **idempotent**: it
still leaves `/Users/_yolojail`'s links pointing into deleted directories when it finishes, and
the last state of the account home is whichever test ran last.

**Why nobody saw it before.** A GitHub-hosted macOS runner is fresh every night, so the first
run of the suite always meets a virgin account home. The maintainer's Mac is not fresh, which
is why the defect appeared on the very first local run — three tests failed on residue an
earlier test in the same run had left.

**What to weigh** (deliberately not decided here): a `t.Cleanup` that removes the layout links a
test's launch created is the obvious move, but it is a **shared, machine-scope** resource under
test-level concurrency the package forbids anyway (`integration/` is serial by design), and a
cleanup that deletes the wrong link would brick a developer's account home rather than a
tempdir. The safer shape may be one launch-shaped fixture the whole file shares. Either way it
wants the AGENTS.md rule applied: **does the test fail if the cleanup is deleted?**

---

## 3. Items 1, 2 and 4 have no automated twin

Work nobody has done, not a gap to lament — and
[§0.5](runbooks/macos-user-manual-checks.md#05-what-would-close-items-1-4) already specifies all
three. They share one launch's probes, on the same passwordless host the six existing twins
require:

| Item | What the twin asserts | Note |
| :--- | :--- | :--- |
| 1 | `whoami` is `_yolojail` and `pwd` is the workspace | two assertions on one probe. Until it exists, the nightly launches sandboxes without ever asserting *whose* they are |
| 2 | a Seatbelt denial | ⚠ **must not use either of the item's own probes** — it should have the host user create a **mode-0644 file** for the purpose, so "readable without a sandbox" is true by construction. `~/.ssh` returns `EACCES` from the POSIX layer on a machine with no sandbox at all, and `$(logname)` fails with no controlling terminal |
| 4 | `~/.claude/skills` and the briefing's first line | needs a pack selected (`packHome` already does this for other twins) |

⚠ **Item 4's twin must not assert a skill COUNT.** The 2026-09-10 run recorded fourteen and the
2026-09-12 run thirteen, and both were right: `developing-yolo-jail` is the source-tree-only
skill, and the second run used a workspace that is not this repo. Assert a named subset plus the
source-tree skill's presence *keyed on the workspace*, or the test encodes a bug.

---

## 4. Item 8's two named halves stay manual, and one needs a seam

[Item 8](runbooks/macos-user-manual-checks.md#8-a-stage-that-cannot-start-does-not-kill-the-launch--new-2026-09-12-never-run)'s
twin covers a third case the item only implied (a stage that fails *while running*). Both halves
the item actually names are still a human's, and the item explains why:

- **The exec-layer injection** (`chmod 000 /usr/bin/sandbox-exec`) is a global, SIP-adjacent
  mutation with a window in which the machine is broken for every process. No test should make
  it, and no process-local substitute exists — the stage argv names `/usr/bin/sandbox-exec`
  absolutely, so a PATH shim cannot reach it. **Closing this needs a SEAM in
  `internal/macosuser`, not a cleverer test.**
- **The interactive veto** needs a child with a terminal, which no test in the suite gives
  (`cmd.Stdin` is nil, deliberately, shared by the whole file). The twin does assert that no
  unanswerable prompt is emitted *without* a tty, which is the failure the gating prevents.

---

## 5. Item 10's second defect must never be automated

Recorded here so nobody "finishes the coverage." Reaching it needs a fault injected into
`LoadJailPacks`, and its failure mode is a **permanently poisoned account home** whose only
remedy destroys the machine tier the shared-credentials hook exists to preserve. The runbook
states the ruling as a `CAUTION`; it stands.

---

## 6. `yolo --dry-run` prints provider secrets in cleartext — unfiled, and not macos-user's alone

Observed 2026-09-12 while preparing the run: a `macos-user --dry-run` printed
`AWS_SECRET_ACCESS_KEY` and `TAVILY_API_KEY` in full, in the `env -i` list of two argvs, to
stdout. That is inherent to printing the argv — the env pairs ARE the argv — so it is not a
mistake in the printer so much as an unexamined consequence of it.

**Not investigated, and stated at exactly the confidence it was measured:** only the macos-user
printer was seen. Whether the container path's `--dry-run` does the same was **not** checked,
and neither was `launch.log`, which every launch is teed into
(`<workspace>/.yolo/launch.log`) — a file on disk is a different exposure from a terminal.

**Two things make this a real decision rather than an obvious redaction.** A launch has no quiet
mode **by ruling** ([`OQ-RO3`](../design/report-tiers.md#11-decision-ledger)), and a disclosure
is never suppressible — so "just hide it" collides with a standing principle and needs to be
argued, not assumed. And a `--dry-run` whose printed argv is not the argv that would run is a
worse tool for the job it exists to do. Redacting *values* while keeping *names* is probably the
shape, but that is a design call with a ledger entry to write.

---

## What to do first, if you want an order

1. **[§1](#1-lsp_servers-installs-nothing-on-this-backend--confirmed-and-three-published-claims-ride-on-it)** — ask the maintainer for the
   [§10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)
   ruling first, since it decides whether you write code or restore a warning. Either way the
   three false doc rows move in the same commit.
2. **[§2](#2-the-twin-suite-poisons-itself-in-test-order--a-persistent-mac-only)** — cheap, off-Mac
   thinking, and it is what makes a second local suite run trustworthy.
3. **[§3](#3-items-1-2-and-4-have-no-automated-twin)** — the highest coverage-per-hour left: three
   items, one launch, and item 2 is the one that establishes the backend is a sandbox at all.
4. **[§6](#6-yolo---dry-run-prints-provider-secrets-in-cleartext--unfiled-and-not-macos-users-alone)** —
   needs a ruling and a check of the container path before any code.

> [!WARNING]
> **Two rules from the run itself, for whoever does this work.**
> [§0.6](runbooks/macos-user-manual-checks.md#06-the-first-run-is-different-and-the-section-above-does-not-apply-to-it)'s
> arbiter is the one that got the triage right every time this session: *run the item by hand
> once, and let the hand result decide which side is broken.* It is how three failing twins were
> correctly read as one product defect plus test residue rather than as three bugs.
>
> And on this backend the **host binary is the implementation** — there is no image. The session
> began with a `yolo` **136 commits stale**, which would have made every measurement a statement
> about three-week-old code. `just install` first, and check `yolo --version` against `git log`.
