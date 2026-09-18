---
title: "Handoff: what the first macos-user hardware run left open"
status: in-review
date: 2026-09-12
tags: [macos-user, handoff, lsp, integration, provisioning]
summary: "The macos-user manual-checks runbook was run end to end on hardware for the first time on 2026-09-12. All ten items now have a measurement, three defects were found and fixed that day, and seven threads were opened. Five are left: one test-suite design flaw that only bites a persistent Mac, one logging gap, and three automation gaps that are work nobody has done rather than problems. Two are closed: provider credentials on every argv this backend builds, and the one product defect (lsp_servers installed nothing here) — ruled and wired on 2026-09-13, leaving one Mac run of its integration subtest."
---

# Handoff: what the first macos-user hardware run left open

**Audience:** the next agent working on `macos-user`, on a Mac or off it. Each thread below
says which it needs.

**Status:** DESIGN, 2026-09-12 — a handoff; one ruling is owed and the rest is work nobody has done.
[`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md) end to end on
the maintainer's Apple Silicon Mac (macOS 26.5, arm64) — the first time items 5-10 or any of
their automated twins had executed anywhere. **Nothing here is speculative about whether the
backend works:** it does, and the run says so. What is left is one test-design flaw, one
logging gap, and three pieces of automation nobody has written —
[§6](#6-provider-secrets-rode-the-launch-argv--fixed-2026-09-13-and-the-framing-below-it-was-wrong)
was a seventh thread and is now closed, and the product defect
([§1](#1-lsp_servers-installs-nothing-on-this-backend--ruled-and-wired-2026-09-13)) was ruled and wired on 2026-09-13, leaving one Mac run of its subtest.

**What the session settled, so you do not re-do it:** every item of that runbook passes on
hardware, the six automated twins ran `executed=6 skipped=0` with **one** subtest red
([§1](#1-lsp_servers-installs-nothing-on-this-backend--ruled-and-wired-2026-09-13)),
and [`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger)'s floor claim — the
runbook's own "single largest unmeasured claim of the whole pair" — is measured. Three defects
were found and fixed the same day (`2a4ac34e`, `28caa116`, `92244306`); each is recorded at the
runbook item that found it, and none is open work.

**Reads with:** [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md)
(the spec, and the results), [`../design/macos-user-provisioning.md`](../design/macos-user-provisioning.md)
(the floor and the stage; [§1.1](../design/macos-user-provisioning.md#11-the-forwarded-command-is-not-passed-through-faithfully)
is the forwarding defect, now fixed, and [§10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)
carries the ruling [§1](#1-lsp_servers-installs-nothing-on-this-backend--ruled-and-wired-2026-09-13)
below was waiting on), [`../design/macos-user-home-tiers.md` §10](../design/macos-user-home-tiers.md#10-what-shipped)
(the layout, and the sixth ordering rule the run added), and
[`../research/macos-support-matrix.md`](../research/macos-support-matrix.md) (the cells this
session moved).

---

## 1. `lsp_servers` installs nothing on this backend — RULED AND WIRED 2026-09-13

**This thread is closed as far as the source goes, and open only on hardware.**
`TestMacosUserDeclaredToolsArrive/lsp_servers` was the one red in an otherwise green suite on
2026-09-12; its three sibling subtests (`mise_tools`, `mise_store_is_machine_tier`,
`npm_prefix_is_workspace_tier`) passed. Predicted from a source reading by runbook item 9's ⚠,
measured red on hardware, and the gap it named is now wired shut. **What is left is one Mac
run of that subtest** — nothing below asserts it is green, because nothing off a Mac can.

**The defect, for the record.** The generated bootstrap script installs from two variables,
and the only producer of either in the tree was the container's podman `-e` lines.
`macos-user` set `YOLO_LSP_SERVERS` — the table that RENDERS agent config — and neither
install variable, so the confined stage execed the script, found an empty install list, and
**exited 0 having installed nothing**: precisely the "reports success having provisioned
nothing" mode the stage was warned about.

**The ruling was the maintainer's, and it was *wire*, not *restore***
([`OQ-P5`](../design/macos-user-provisioning.md#decision-ledger), recorded at
[§10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)).
The retired launch warning stays retired; the gap it described is closed instead.

**What shipped.** The recipe table that turns `lsp_servers` keys into install lists moved out
of the container launcher into `internal/config` (`config.LSPInstalls`) — `internal/macosuser`
could not import `internal/cli/run`, which is why only one backend could compute the set at
all — and `macosuser.BuildRunPlan` now carries both variables into **both** environments the
readers live in:

| Role | Site |
| :--- | :--- |
| producer — shared by both backends | `internal/config/lsp.go` — `config.LSPInstalls` |
| crossing — container | `internal/cli/run/assemble.go` — `-e YOLO_LSP_NPM_INSTALL=…`, `-e YOLO_LSP_GO_INSTALL=…` |
| crossing — macos-user, bootstrap env | `internal/macosuser/runplan.go` — `buildBootstrapEnv` |
| crossing — macos-user, stage env | `internal/macosuser/runplan.go` — the session env file, which is the stage's whole environment |
| reader — the generated script | `internal/entrypoint/shell.go` |
| reader — the boot catalog | `internal/entrypoint/catalog.go` |
| reader — the refresh path | `internal/entrypoint/serverrefresh.go` |

Setting one of the two crossings is a half-fix that still leaves a green-looking launch, so
`macosuser.PlanInvariants` refuses a plan that has only one of them, and
`internal/macosuser/lspinstall_test.go` deletes each in turn and asserts the refusal.
`internal/cli/run/lspinstallparity_test.go` is the cross-backend half: one config, and both
backends must resolve it to the same two lists.

**The published claims move with this, and they are the doc work this thread leaves.** Each
was corrected to *MEASURED FALSE* on 2026-09-12 and is corrected again here:

| Doc | Now says |
| :--- | :--- |
| [`../guides/macos.md`](../guides/macos.md) | wired 2026-09-13, install unmeasured on hardware |
| [`../design/provisioner-sets.md`](../design/provisioner-sets.md) row 4 | **drives**, with the same hardware caveat |
| [`../design/macos-user-provisioning.md` §10.6](../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them) | the retirement was premature, the gap is wired, and the rule that retired it stands |

⚠ **Both rows had hedged with "NOT MEASURED on hardware", and the hedge was honest while the
claim beside it was wrong** — a reader who trusted the row got a feature that did not exist.
The rows above carry the same hedge again, and it means the same thing: **a source fact, not an
install.** Only `TestMacosUserDeclaredToolsArrive/lsp_servers` going green on a Mac retires it.

The runbook's item 9 result records the same thing, and its *Known-absent* entry moved with it.

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

## 6. Provider secrets rode the launch argv — FIXED 2026-09-13, and the framing below it was wrong

**This section used to ask whether `--dry-run` should redact. It should not, and redaction was
never the question.** The `K=V` words it printed are POSITIONAL ARGUMENTS: `sudo` scrubs the
environment, so the pairs cannot cross as process state and have to be spelled on the command
line. The printer was faithful. The exposure was in the RUNNING PROCESSES — every argv this
backend starts carried every composed credential for the whole life of the session, whether or
not anybody ever typed `--dry-run` — and hiding it from the one view that showed it would have
removed the evidence and left the hole.

### What was measured, and what a Linux jail could not settle

| Question | Answer | Instrument |
| :--- | :--- | :--- |
| Do the launch and provisioning argvs carry credentials in cleartext? | **Yes, in full** | a `macos-user --dry-run` in a workspace whose `env_sources` held a fake AWS key and a fake Tavily key: both appeared on two argvs, three times in one printed plan |
| Is a process's argv readable by a DIFFERENT account on Linux? | **Yes** | in this repo's own jail: `/proc/<pid>/cmdline` is mode `0444` and uid 65534 read a root process's whole command line, while `/proc/<pid>/environ` is `0400` and the same read returned `EACCES`. **That contrast is the whole mechanism**: the argv is world-readable and the environment is not |
| …by a different account on **macOS**? | ⚠ **NOT MEASURED** | there is no `/proc`; the answer lives in the permission check inside `sysctl kern.procargs2`, and only a Mac can run it |
| …by the **invoking** user, whose `sudo` started it? | ⚠ **NOT MEASURED** | same instrument, same gap. Neither half of the macOS question was settled, and nothing below reasons past that |
| Does `sudo` itself record the argv it authorised? | ⚠ **NOT MEASURED** | sudo logs the command it allows; whether the `K=V` words reach a given Mac's unified log depends on that machine's sudoers, which yolo deliberately does not change |
| Does the container backend's podman argv carry the same values? | **No** | measured on the assembled argv with the same two fake keys: neither appears. The composed channel and the hydrated `env_sources` cross in `yolo-user-env.sh` at 0600 instead |
| Does the container path have a `--dry-run` to check? | **No** | it refuses: *"--dry-run is only supported for the macos-user runtime"* |
| Did `<workspace>/.yolo/launch.log` hold an on-disk copy? | **No — for a reason that is its own thread** | see [§7](#7-nothing-the-macos-user-backend-prints-reaches-launchlog) |

> [!IMPORTANT]
> **The macOS half being unmeasured does not change the sizing, and that is why the fix landed
> anyway.** Two things stand without it: the **guest notch would reuse this argv
> shape on Linux**, where the measurement above IS the answer; and the two other secrets yolo
> carries already travel in 0600 files, so this backend was the odd one out rather than the one
> making a trade-off. What the Mac measurement would change is only how urgent *today* is.

### Every secret-bearing value that reached an argv

A survey, not the two names that were first observed. The composed launch env is assembled in
`buildPlan` and rendered by one function that all three sandboxed argvs share, so what follows is
the whole of it:

| Value | Where it comes from | Rode which argv |
| :--- | :--- | :--- |
| hydrated `env_sources` | every dotenv file and inline dict the config names — the key whose entire purpose is carrying credentials | launch, provisioning |
| provider credentials | the profile channel's shape vars: the selected provider's `api_key_env_name` hydrated into the agent's own variable | launch, provisioning, **and the capture driver** |
| the pack env fold | each selected pack's `kind: "env"` values with the active variant's literals folded on top | the same three |
| `YOLO_PROVIDERS`, `YOLO_PROFILES`, `YOLO_USE_PROFILES` | composed tables — addresses and option names. No credential VALUES by design: a `base_url` carrying userinfo is refused at validation | the same three |
| git identity | the host's `git config` — personal data rather than a secret | the same three, **and the bootstrap** |
| `MISE_TRUSTED_CONFIG_PATHS`, `TERM`, `COLORTERM` | this backend and the invoking terminal | the same three |
| `YOLO_MCP_SERVERS` | the `mcp_servers` section verbatim, which MAY hold a literal key in a server's `env` block | **the bootstrap only** — and the container's podman argv carries the identical value |

⚠ **The capture driver is the row worth stopping on.** It carries the provider credentials
because it is handed the same composed channel, and it is the one argv whose audience includes a
**vendor installer yolo is running for the first time**.

### The fix: one root-owned 0600 file, which is the shape yolo's other two secrets already use

[`internal/macosuser/envfile.go`](../../internal/macosuser/envfile.go) is the whole mechanism, and
it invents nothing. A host service's bearer token already rides a 0600 endpoint file rather than
the podman argv, and the hydrated `env_sources` plus the entire profile channel already ride
`yolo-user-env.sh` at 0600. macos-user was the backend still putting them on a command line.

- **The file.** `/var/yolo-jail/env/<session>.env`, root-owned `0600`, inside a root-owned `0700`
  directory, with two `chmod +a` ACEs for `_yolojail` alone: `search` on the directory (traverse,
  not list, so one session cannot enumerate another's) and `read` on the file. Written through
  `sudo tee` with the content on **stdin**, never argv — the same rule the sandbox account's
  random password already follows, and for the reason stated there: argv would leak it to `ps`. The directory is locked down BEFORE the
  write, so `tee`'s brief 0644 window is unreachable.
- **The argvs.** They keep the identity quartet (`HOME`/`USER`/`SHELL`/`PATH`, plus the mise store
  and login PATH that travel with it) — none of it a secret, all of it needed before the file can
  be read — and gain one word naming the file.
- **The reader.** Each argv is wrapped in `/bin/sh -c '. "$1" || exit 1; shift; exec "$@"'`. It
  re-quotes nothing (`"$@"` is the wrapped command word for word, which matters on a backend whose
  argvs deliberately avoid `sudo --login` for exactly that reason), it `exec`s so no process
  survives it, and it **fails closed** — an unreadable file stops the launch instead of starting an
  agent that authenticates against nothing.
- **The structural half.** `LaunchArgv`, `ProvisionArgv` and `CaptureDriverArgv` no longer TAKE the
  composed environment; they take the file path. An argv builder that never receives a credential
  cannot print one, so this is a property of the call graph rather than of a loop somebody has to
  keep writing correctly.
- **The invariant.** `SandboxArgvEnvProblems` reads each argv the way `env` does — everything from
  `-i` to the first non-`K=V` word — and refuses any key outside a **closed allowlist**: the
  identity variables this backend owns, plus the two words that describe the delivery. A denylist
  of credential-shaped names would need updating for every provider a pack adds; an allowlist
  fails on a *new* composed variable by existing. Its twin, `SandboxArgvReadsEnvFile`,
  fails when a call site is DELETED — an argv that stopped reading the file is a sandbox with no
  credentials, which every leak check on earth would call clean.

**The dry run stayed honest, which was the constraint.** It prints the argv that would run, and
that argv now has no secrets in it, so nothing is suppressed and
[`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) never comes up. The plan gained a
disclosure rather than losing one: it names the file, its mode, who may read it, the six privileged
commands that lock it down, and **the variable names it sets** — names, never values, because a
name is a fact about the launch and a value is the credential.

### What it costs

- **Six privileged steps per launch** where there were none: three to prepare and lock the
  directory, the `tee`, the `chmod 0600`, and the read ACE. They run inside the same `sudo`
  credential window as the staging commands beside them.
- **A file can be left behind.** The launch sweeps it on every exit path and a capture's cleanup
  removes it, but a `kill -9` between the write and the sweep leaves one. Bounded rather than
  closed: it is 0600, root-owned, inside a 0700 directory, readable by one account, and the next
  launch of the same workspace rewrites the same path. Nothing reaps it on a schedule.
- **A longer printed argv.** A reader now meets a `/bin/sh -c … yolo-sandbox-env …` hop before the
  command they were looking for.
- **A variable name that is not a valid shell identifier now fails the launch** instead of being
  set. This is PARITY rather than a new rule — `yolo-user-env.sh` has the same constraint on the
  container backends, and the dotenv parser already filters names — but an inline `env_sources`
  dict or a pack's `kind: "env"` key is not filtered by that regex.

### What it does NOT cover

1. **`YOLO_MCP_SERVERS` on the bootstrap argv.** An `mcp_servers` entry may hold a literal key in
   its `env` block, and that JSON rides the bootstrap command line here and the podman `-e` line
   there — measured on both. It is therefore a cross-backend question about config literals, not a
   macos-user argv question, and the config's own convention (a `${VAR}` placeholder hydrated from
   `env_sources`) already points away from it. Left open deliberately; `SandboxArgvEnvProblems`
   states in as many words that it excludes this argv.
2. **The rest of the bootstrap argv**, which is the darwin bootstrap's command-line interface —
   wire tables and staged paths — and carries none of the composed values.
3. **Root.** A root process on that Mac reads the file exactly as it read the argv.
4. **The sandbox account reading its own other sessions.** Two workspaces launching at once both
   run as `_yolojail`, so one session's process can read the other's env file. Per-session files
   keep the two SEPARATE; they are not private from each other, which is the same sharing the
   single account home already has.
5. **Whether any of this was reachable on macOS in the first place** — the unmeasured rows above.
   If `kern.procargs2` already refuses a cross-account read, this change buys macOS nothing today
   and buys the guest notch everything.

⚠ **NOT MEASURED ON HARDWARE.** `chmod +a` is an Apple ACL extension: the commands are built and
unit-tested here and only a Mac can run them. The `sh` reader IS executed by the test suite, on
Linux and macOS alike, because it is POSIX. Add the file's mode, its ACE and a cross-account read
attempt to the next hardware pass.

---

## 7. Nothing the macos-user backend prints reaches `launch.log` — FIXED 2026-09-17

**Found while answering [§6](#6-provider-secrets-rode-the-launch-argv--fixed-2026-09-13-and-the-framing-below-it-was-wrong)'s
"is it on disk too?" question, and the answer is a different defect.** Everything the launcher
prints is supposed to be teed into `<workspace>/.yolo/launch.log`; that is what
[`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) offers in place of a quiet mode — *"too
much on the terminal" is answered by reading the file rather than by hiding the line*.

It is not true on this backend. The pipeline installs its tee on the Options' writers, and
`RealDeps` hands the backend `os.Stdout` instead — so the dry-run plan, the Seatbelt profile, the
three argvs, every `not enforced on macos-user` warning and every setup and teardown line are on
the terminal and nowhere else. The log for a macos-user launch holds the header and the trailer
with the whole launch missing between them.

**Two consequences, pointing opposite ways.** The good one is that the secrets in [§6](#6-provider-secrets-rode-the-launch-argv--fixed-2026-09-13-and-the-framing-below-it-was-wrong) never reached
a 0644 file on disk. The bad one is that this backend's disclosures — the pack read/exec banners
among them, which
[`OQ-TP9`](../design/trust-paths.md#decision-ledger) kept when it deleted the approval gate — are
unrecoverable the moment the scrollback is gone.

**Off-Mac work, and small.** The fix is to give the backend the pipeline's writers rather than the
process's; the trap is that `yolo macos-setup` and its three siblings build their Deps separately
and have no pipeline to take writers from, so whatever seam is added has to leave those alone.

**Fixed 2026-09-17 by a PUBLISHED writer** (`macosuser.SetLaunchWriter`, which `attachLaunchLog`
calls and `finish` undoes), not a threaded parameter: the writer is composed three packages away
and the `run.Options.MacosUserRun` seam carries none, so threading one would have moved three more
files — and publishing is also what handles the trap above, since nothing publishes on the
`macos-setup` path and not one of those call sites had to change.

**What did NOT change, and deliberately:** `real.go`'s two `cmd.Stdout = os.Stdout` sites. Those
hand a CHILD PROCESS the parent's streams — `sudo`'s password prompt, the bootstrap, the login
shell — so wrapping them would put an `os/exec` pipe between child and tty and would copy an agent
session's bytes into a 0644 `launch.log`, which is the one thing that file excludes by name. The
backend now draws the same launcher/session line the container backends draw. A Mac still owes
one observation: that a live launch's plan, profile and argv lines actually land in the file.

## What to do first, if you want an order

1. **[§2](#2-the-twin-suite-poisons-itself-in-test-order--a-persistent-mac-only)** — cheap, off-Mac
   thinking, and it is what makes a second local suite run trustworthy.
2. **[§3](#3-items-1-2-and-4-have-no-automated-twin)** — the highest coverage-per-hour left: three
   items, one launch, and item 2 is the one that establishes the backend is a sandbox at all.
3. **[§7](#7-nothing-the-macos-user-backend-prints-reaches-launchlog)** — off-Mac, small, and it
   is what makes every OTHER thread here reviewable after the fact.
   [§6](#6-provider-secrets-rode-the-launch-argv--fixed-2026-09-13-and-the-framing-below-it-was-wrong)
   is closed; what it leaves for a Mac is three lines on the next hardware pass (the file's mode,
   its ACE, and whether a second account can read an argv at all).

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
