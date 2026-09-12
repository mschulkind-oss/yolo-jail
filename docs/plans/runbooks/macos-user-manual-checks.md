# What only a Mac can verify

**Audience:** whoever has a Mac and five minutes — and, since 2026-09-12, a nightly CI
job that stands in for them on six of the ten items below.

> [!IMPORTANT]
> **This file was called *What only a Mac with a password can verify*, and the title had
> to go.** Six items now run unattended on a Mac with **no** password — a host with
> passwordless `sudo`, which is what a GitHub-hosted macOS runner is. The password was
> never a property of the backend; it was a property of the maintainer's laptop. What is
> irreducible is the **Mac**, and for four items a **human at a keyboard**.

**This file is the SPEC, and it stays the spec.** An automated twin makes an item cheap
to re-check on a schedule; it does not retire the item, and it never observes everything
a human's run does. Where a twin exists, the item names it. Where only half an item is
automated, the other half stays written out in full rather than being quietly marked
done. [§0](#0-what-is-automated-what-is-not-and-what-a-red-job-means) is the whole map.

**What is covered by unprivileged tests, so you do not re-check it by hand:** the
generated home (shims, launcher dir, login rc files, mise config), a blocker actually
executing and refusing with its message and suggestion, the composed home overlay landing
at its destinations and replacing a removed pack's subtree, a missing overlay degrading to
a warning, the staging commands really running, the a+rX mode, the replace-not-merge
shape, and the J2 fresh-inode rule (re-stage, then exec) — all in
`internal/entrypoint/darwinbootstrap_darwin_test.go` and
`internal/macosuser/staging_darwin_test.go`, which run on any Mac with no privilege at all.

What remains needs either root, a kernel, or a human at a password prompt.

> [!IMPORTANT]
> **Items 5-10 are NEW (2026-09-12) and NONE HAS EVER BEEN RUN — including by their own
> automated twins.** Three things shipped that day — the per-workspace home layout, the
> non-container package FLOOR, and the confined provisioning STAGE — and every runtime claim
> about all three is unmeasured. Six integration tests were then written against them, and
> writing a test measures nothing: **no Mac has executed one**, so the first nightly run is
> the first measurement either way. Item 3 is worth re-running with them, for the reason that
> item states.
>
> **Read items 6-9 in order and stop at the first failure.** They are a dependency chain, not
> a list: no floor means no `mise` and no `npm`, which means the stage's first line fails,
> which means `mise_tools` and `lsp_servers` install nothing. A failure at item 6 explains
> every later one, and reporting them as four bugs would be reporting one.

> [!NOTE]
> **ITEMS 1-4 ALL PASSED on 2026-09-10** — one session on the maintainer's Apple Silicon
> Mac (macOS 26.5, arm64), host `yolo` `0.8.0+1293.g520e848d`, the first live
> `macos-user` session on that machine. Per-item results are recorded under each
> heading below, including the two places the measurement corrected this file.
>
> **This does not retire the runbook.** Every item is still the only instrument for
> its fact, and none of them is pinned by a test — so a change to the privilege
> transition, the Seatbelt profile, the native nix chain or content staging needs
> these four run again. Treat the results as a dated measurement, not a checkbox.
>
> ⚠ **An agent cannot run any of them ON THAT MAC** — and that is a fact about that Mac,
> not about the backend. `sudo -n true` reports `a password is required` there, and every
> macos-user argv leads with `sudo --user=_yolojail`, so an agent attempting a launch hangs
> on the prompt rather than failing. On a host with **passwordless** sudo the same launch is
> unattended, which is why the automated twins exist and why their gate REFUSES to run
> anywhere `sudo -n true` fails rather than risking that hang
> ([§0](#0-what-is-automated-what-is-not-and-what-a-red-job-means)). Items 1-4 stay a human's
> to run for a different reason: nobody has written their twins.

---

## 0. What is automated, what is not, and what a red job means

### 0.1 The map

Every twin lives in `integration/` and is named `TestMacosUser…` — the prefix is enforced by
the gate itself, so the job's `-run '^TestMacosUser'` selects the whole suite by construction
rather than by a list somebody maintains.

| # | Automated twin | Still a human's, and why |
|---|---|---|
| 1 | none | all of it. Automatable on a passwordless host — nobody has written it ([§0.5](#05-what-would-close-items-1-4)) |
| 2 | none | all of it — and ⚠ one of its two probes proves nothing; see the item |
| 3 | partly: `TestMacosUserFloorReachesTheSandboxPath` makes the same re-prepend assertion for the packages the FLOOR declares | naming a package **your own config** declares, which is what the acceptance bar is about |
| 4 | none | all of it. Needs a launch with a pack selected; nobody has written it |
| 5 | `TestMacosUserHomeTierIsPerWorkspace` | nothing of the item itself |
| 6 | `TestMacosUserFloorReachesTheSandboxPath` | nothing |
| 7 | `TestMacosUserProvisioningStageRunsAndRecordsItself` | nothing that can be spelled — its ⚠ cwd sub-item is **struck**, its ⚠ timing sub-item is now a logged number |
| 8 | `TestMacosUserAFailingProvisioningStageDoesNotAbortTheLaunch`, which covers a third half the item only implied | **both halves the item names**: the exec-layer fault injection and the interactive veto |
| 9 | `TestMacosUserDeclaredToolsArrive` (four subtests, one launch) | nothing — but ⚠ one subtest is **predicted to fail**; see the item |
| 10 | (a) `TestMacosUserLayoutRefusesAnOccupiedSidecarMirror` | (b) never, deliberately: it poisons an account home permanently |

Six of the ten run unattended. **Four do not, and two of those four are the ones that
establish the backend is a sandbox at all** (items 1 and 2).

### 0.2 The job that runs them

[`.github/workflows/macos-user.yml`](../../../.github/workflows/macos-user.yml) — nightly at
07:00 UTC and on manual dispatch, on `macos-latest` (Apple Silicon, floating label on purpose:
`sandbox-exec` is Apple-deprecated, and a pinned runner would hide the OS release that changes
what the profile does).

**It needs no jail image, and that is the point.** A macos-user launch returns before
`runContainer`, never calls `AutoLoadImage`, and builds its tools with native darwin nix from
this flake instead — so none of the chain that has kept `nightly-macos.yml` red since
2026-09-09 reaches it
([`../../design/darwin-image-provenance.md` §3](../../design/darwin-image-provenance.md#3-the-chain-and-the-one-link-worth-breaking)).
This job can report while that one is blocked.

Three steps stand between a fresh runner and a launch: install nix, install Go, and run
`yolo macos-setup` (idempotent; creates the hidden `_yolojail` account and `/Users/Shared/yolo`).
**The suite will not create that account for you** — a test run must not silently add a system
account to somebody's Mac — so a machine without it produces a countable skip naming the
command, not a mutation.

**You can run the twins on your own Mac**, which is the cheapest way to get an item's answer
without following its steps by hand:

```console
$ sudo -v                                    # prime the ticket; the gate skips if sudo would prompt
$ YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUser' ./integration
```

⚠ **`sudo -v` buys five minutes by default and a launch can outlast it**, in which case a
prompt appears mid-run — harmless at a terminal, fatal to an unattended run. That is the whole
reason the gate probes `sudo -n true` first rather than discovering it inside a launch. The
per-launch deadline is 30 minutes (`YOLO_TEST_MACOS_USER_TIMEOUT`, seconds), sized as a ceiling
on waste rather than an estimate: nobody has measured what a first macos-user launch costs.

### 0.3 A suite that skips must not look like a suite that passes

Every one of these tests skips on every machine that develops this repo — the jail is Linux
and has no `sudo` at all — and `go test` reports a skip as a pass. Left alone, the reward for
writing six of them is a green CI job that asserts nothing.

So the run **declares itself**: `YOLO_TEST_MACOS_USER=1` says "I was scheduled to exercise this
backend", and a declared run that executed **zero** of these tests exits non-zero, printing
every skip and its reason. Unset — a developer's `just test`, the Linux CI job — the same tests
skip quietly, which is correct there. The mechanism and its own tests are in
`integration/macosusergate_test.go`; the declaration lives in the workflow, because only the
step that scheduled the work knows what it was scheduled to do.

**Measured on Linux, 2026-09-12** — every row is the real `TestMain`, not a table of a pure
function:

| What was run | Result |
|---|---|
| the job's exact command, on a host that cannot run one | **exit 1**, `executed=0 skipped=6`, each reason printed |
| the same, declaration unset | exit 0 |
| declared, with a typo in the job's own selector (`^TestMacOSUser`) | **exit 1**, *"No test even reached the gate"* |
| declared, one gated test executing | exit 0, `executed=1 skipped=1` |
| declared, the only gated test clearing the gate and then skipping for its own reason | **exit 1** — a late skip is a skip |
| the guard's call site deleted from `TestMain` | exit 0 — **and `just test-fast` goes red naming it** (that pin costs 0.6s) |

The last two rows are the ones worth knowing. A test that clears the gate and then skips for a
reason of its own has not exercised the backend, and is not counted as if it had. And the guard
is deletable, like anything else — so the deletion is caught on Linux, by the ordinary
pre-commit gate, without waiting for a Mac.

### 0.4 Reading a red job

Three shapes, and they want different readers:

1. **`executed=0 skipped=N`, with reasons.** The RUNNER is wrong, not the backend — no
   `_yolojail` account, sudo would prompt, not a Mac. Each reason names its fix. Nothing was
   measured; do not read it as a failure of anything under test.
2. **A test FAILED.** A real finding. Items 6-9 are a dependency chain — the callout at the top
   of this file says why — so start at the lowest-numbered failure and read the rest as its
   consequences. The job deliberately does not stop at the first failure: `go test` orders by
   source position, not by that chain, so failing fast would stop at an arbitrary one and hide
   the items that do not depend on it.
3. **Only the `lsp_servers` subtest failed.** Known, and not a Mac problem — see item 9's ⚠.

### 0.5 What would close items 1-4

Not a gap to be lamented; a piece of work nobody has done. All four are automatable on the same
passwordless host the other six need, and items 1, 2 and 4 could share one launch's probes:

- **1** — `whoami` is `_yolojail` and `pwd` is the workspace. Two assertions on one probe.
- **2** — a file the host user creates for the purpose, mode 0644, plus the keychain path. Not
  the `~/.ssh` line, and the item says why.
- **3** — a workspace whose `packages:` declares one non-floor package, and one extra darwin
  derivation to build. The only one of the four with a cost beyond the launch.
- **4** — a launch with a pack selected, then `~/.claude/skills` and the briefing's first line.

### 0.6 The FIRST run is different, and the section above does not apply to it

> [!IMPORTANT]
> **Nothing in items 5-10 has ever executed.** They were written on Linux, in a jail that cannot
> run this backend, against a backend no CI job has ever exercised. [§0.4](#04-reading-a-red-job)
> shape 2 says a failing test is "a real finding" — **that is true from the second run onward.**
> On the first, a red is at least as likely to be a defect in the TEST as in the product.

Read the first run with that prior, or you will spend it "fixing" working code.

**The darwin classes to suspect first**, in order of how often this repo has actually been bitten:

| Class | Why it passes on Linux | The tell |
| :--- | :--- | :--- |
| **`/var/folders` symlink** | `t.TempDir()` returns `/var/folders/…`, which **is a symlink** to `/private/var/folders/…`. Any assertion comparing a fixture path against code that resolves symlinks (`filepath.EvalSymlinks`, and everything built on it) passes on Linux and fails here. | A path mismatch where the two sides differ only by a `/private` prefix. **Cost three tests across two packages on 2026-09-09**; `AGENTS.md` reproduces it on Linux in one line. |
| **Wording** | Every assertion on a message was written against the string in the tree, not against a run. A reworded remedy or a different error verb fails the test while the behavior is correct. | The test names a substring the output plainly does not contain, and the output looks *right*. |
| **Path shape** | The account home, the workspace sidecar and the store are three different trees here, and a test can assert the wrong one without Linux ever noticing. | An assertion about `/Users/_yolojail/...` failing where the real thing sits under `<ws>/.yolo/home`, or vice versa. |
| **Timing** | Nothing about the 30-minute per-launch ceiling was measured. A `mise install` or a darwin derivation may simply need longer. | A deadline, not an assertion, is what failed. |

**Telling a test bug from a product bug.** The question is not "is the assertion false" — it is
**"would a human running this item by hand call the observed behavior wrong?"** Every item below
states its manual procedure and its pass condition; run the item by hand once, and let the hand
result decide which side is broken. When the hand run agrees with the code, fix the test. When it
agrees with the test, you have found the thing this suite exists to find.

⚠ **Fix a wrong test by correcting the assertion, never by deleting it or loosening it to
tautology.** A test relaxed until it passes is worse than the manual item it replaced, because the
item at least told you it had never been run. If an assertion turns out to rest on something that
cannot be checked here, say so in the test and leave the item manual.

⚠ **Do not "fix" a product behavior to match a test written by someone who never ran it.** Items
6-9 are a dependency chain and items 5 and 10 assert layout rules that
[`macos-user-home-tiers.md`](../../design/macos-user-home-tiers.md) rules deliberately — a change
there needs the design consulted, not just a green job.

**What a clean first run would prove**, so the bar is explicit: `executed=N` with `N` matching the
number of `TestMacosUser…` tests, no failures, and the per-item observations in items 5-10 matching
what each says it expects. That would be the first time any runtime claim in either macos-user
design has been measured at all.

---

## 1. The privilege transition

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'whoami; pwd'
```

**Expect:** `_yolojail`, and the workspace path. One sudo prompt.

This is the whole irreducible remainder: `sudo` running at all, the `-u _yolojail`
switch landing, and the staged binary self-exec'ing as that user. Everything before
and after it is covered by the harnesses above.

**PASSED 2026-09-10.** It printed `_yolojail` and the workspace path after one
password prompt — `sudo` ran, the `-u` switch landed, and the staged binary
self-exec'd as that user (`internal/macosuser/runplan.go` builds both argvs).

> [!NOTE]
> **No automated twin, and nothing structural stands in the way of one** — two assertions on
> one probe, on the same passwordless host the six automated items already require
> ([§0.5](#05-what-would-close-items-1-4)). Until somebody writes it, the nightly launches
> sandboxes without ever asserting *whose* they are.

## 2. Seatbelt is actually applied

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls /Users/$(logname)/.ssh; ls /Library/Keychains'
```

**Expect:** both refused — but **not with the same message**, and this line claimed
they were until it was first run on 2026-09-10. Measured:

```
ls: /Users/<host user>/.ssh: Permission denied
ls: /Library/Keychains: Operation not permitted
```

The home path denies with `EACCES` and the keychain path with `EPERM`. Either message
is a pass; what fails is `No such file or directory` (see the warning below) or a
listing. Do not read the mismatch as a half-failure — the original wording invited
exactly that.

> [!WARNING]
> **Not `~/.ssh`.** Inside the jail `~` is the SANDBOX's home, which has no `.ssh`,
> so that spelling returns "No such file or directory" — which proves nothing and
> looks like a pass. The check has to name the HOST user's path explicitly, because
> the thing being tested is that the sandbox cannot read a home that is not its own.
> This runbook said `~/.ssh` on its first outing and got exactly that non-answer.

The profile is generated as a pure string and pinned by unit tests; what no test can
check is that the kernel loaded it. A jail that looks right and confines nothing is
the failure this catches, and it is silent otherwise.

**PASSED 2026-09-10**, same session as item 1 — both paths refused, so the kernel
does load the profile. That is the fact no unit test in this repo can reach.

> [!WARNING]
> **Only the SECOND probe proves anything, and the two errnos above are the evidence.** A
> Seatbelt denial surfaces as `EPERM` — *Operation not permitted* — which is what
> `/Library/Keychains` returned, and nothing in the filesystem would have produced that errno
> there. `~/.ssh` returned `EACCES`, *Permission denied*: the POSIX layer refusing before the
> profile was ever consulted, which is what a foreign uid gets at a 0700 directory (ssh-keygen
> creates `~/.ssh` that way) **on a machine with no sandbox at all**. So the first line is a
> refusal the backend would have earned by doing nothing.
>
> Keep both lines — the first is a cheap smoke test — but if only one can be believed it is the
> keychain one. **An automated twin should not use either**: it should have the host user create
> a mode-0644 file for the purpose, so that "readable without a sandbox" is certain by
> construction rather than by a claim about somebody's home directory.

> [!NOTE]
> **No automated twin.** This is the item that says the backend is a sandbox, and it is the one
> the nightly does not make ([§0.5](#05-what-would-close-items-1-4)).

## 3. The acceptance bar — `packages:` reaches the agent

**Name a package your own config actually declares.** Check first:

```console
$ yolo config-dump 2>/dev/null | grep -A5 '"packages"'
```

then ask for one of those:

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'which just'   # or any declared package
```

**Expect:** a `/nix/store/…` path.

> [!WARNING]
> **A tool macOS already ships proves nothing here.** `which jq` returning
> `/usr/bin/jq` is not a failure of the acceptance bar — it means `jq` was never in
> `packages:` and the system copy answered. This runbook suggested `jq` on its first
> outing and got that non-answer. Use a package the config declares, or the check
> cannot distinguish "nix delivered it" from "macOS already had it".

This is the backend's founding requirement — it honors `packages:` via native nix or
it does not ship. It exercises the whole native chain in one command: the build, the
GC root, the PATH prefix, and the login-rc re-prepend surviving macOS `path_helper`
(the [OQ-1](mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown) question). A Homebrew path here means the re-prepend lost.

**PASSED 2026-09-10**, same session as items 1 and 2, and it answers
[`OQ-1`](mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown):
the re-prepend HOLDS. Both declared packages resolved into the store profile —

```
/nix/store/…-yolo-noncontainer-packages/bin/just
/nix/store/…-yolo-noncontainer-packages/bin/fzf
```

— and the pair was chosen because **each had a competing host copy to beat**: `fzf`
at `/opt/homebrew/bin/fzf` and `just` at
`~/.local/share/mise/installs/just/1.58.0/just`. Either one answering would have
meant the re-prepend lost to `path_helper` or to mise. Neither did. **Pick the
package the same way when re-running this** — a declared package with no host rival
cannot distinguish a working re-prepend from a lucky PATH.

> [!NOTE]
> **Half of this is now asserted every night, for a different package set.**
> `integration/TestMacosUserFloorReachesTheSandboxPath` (item 6) requires each of nine FLOOR
> binaries to resolve into `/nix/store`, and two of them — `git` and `curl` — have a
> `/usr/bin` rival, which is the same "beat `path_helper`" claim this item makes. What it does
> **not** cover is the acceptance bar itself: a package **your config declares**, arriving
> through `packages:` rather than through the floor. Re-run this item by hand whenever the
> login-rc re-prepend changes shape — it did on 2026-09-12, when its value started arriving
> as `$YOLO_DARWIN_LOGIN_PATH` rather than baked.

## 4. Content actually reached the agent

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls ~/.claude/skills; head -3 ~/.claude/CLAUDE.md'
```

**Expect:** the built-in skills, and briefing prose.

The install is covered by test; what is not is that the launch composes and stages
it *for real*, through sudo, into the actual sandbox home.

**PASSED 2026-09-10**, same session. All fourteen built-in skills landed —
`brainstorming`, `configuring-the-jail`, `design-doc`, `developing-yolo-jail`,
`diagnosing-the-jail`, `headful-browser`, `implementation-plan`, `new-project`,
`open-source-project`, `research`, `roadmap`, `system-doc`, `user-stories`,
`vantage-docs` — and `~/.claude/CLAUDE.md` opened with the native-backend briefing
(*"You are confined by a Seatbelt sandbox on the human's REAL machine, not by a…"*).
Worth noting `developing-yolo-jail` is among them: it is the source-tree-only skill,
so its presence also confirms the source-tree probe fired correctly for this
workspace rather than the list being staged blind.

> [!NOTE]
> **No automated twin.** It needs a launch with a pack selected (the isolated user config the
> suite already writes, via `packHome`), then two reads: `~/.claude/skills` and the first line
> of `~/.claude/CLAUDE.md`. `TestMacosUserHomeTierIsPerWorkspace` selects a pack and launches,
> but asserts the SHAPE of the home rather than what was staged into it, so it would not notice
> content that never arrived.

## 5. The per-workspace home layout — NEW 2026-09-12, NEVER RUN

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls -ld ~/.claude ~/.config ~/.local; ls -l ~/.claude/.credentials.json; cat ~/.claude/.credentials.json | head -c 20'
```

**Expect:** the first three are **symlinks** into `<workspace>/.yolo/home`, the credential is a
**relative** symlink (`../.claude-shared-credentials/.credentials.json`) that nonetheless
**reads**, and a second workspace shows its own `~/.claude/projects` rather than this one's.

Everything about the layout is pinned on Linux against a real filesystem
([`../../design/macos-user-home-tiers.md` §10](../../design/macos-user-home-tiers.md#10-what-shipped)),
including the credential resolving through it. What no test here can reach is whether the
**sandbox uid can create `<workspace>/.yolo/home`** through the shared-group ACL. If it cannot,
the boot fails with the layout step named and the existing ACL hint attached —
`yolo macos-fix-permissions <workspace>` — which is the diagnosis to follow rather than a bug
to file.

> [!WARNING]
> **An account that predates this refuses to launch, by design.** There is no migration
> ([`OQ-HT2`](../../design/macos-user-home-tiers.md#decision-ledger)): a real directory where a
> layout symlink belongs is never removed, renamed or copied, so the launch names every
> offender and the reset. If that is what you get, it is the ruling working:
>
> ```console
> $ sudo rm -rf /Users/_yolojail && yolo macos-setup
> ```
>
> You lose that account's agent history, which was ruled affordable because the only session
> this backend ever had was the 2026-09-11 hardware run.

⚠ **Two more unmeasured facts ride along with this item.** `MISE_DATA_DIR` is now named
(`~/.yolo/mise`) rather than defaulted, so nothing lands in the per-workspace `~/.local`; and
the login rc files re-prepend `$YOLO_DARWIN_LOGIN_PATH` instead of a baked PATH, so **item 3's
`packages:` check is worth re-running** — it is the one that proves the re-prepend still beats
`path_helper`, and its value now arrives by variable. **Neither rides along with the automated
twin either**: the first is item 9's tier check, the second is item 3's, and the twin below
deliberately does not duplicate them.

> [!NOTE]
> **Automated twin: `integration/TestMacosUserHomeTierIsPerWorkspace`.** It runs TWO launches in
> two workspaces and asserts more than the command above can: six account-home paths are
> symlinks into *this* workspace's sidecar, `~/.claude-shared-credentials` is a real directory
> in the account home with the mirror pointing back at it, the credential link is still spelled
> RELATIVELY and still resolves, and the second workspace repoints the account home without the
> first's state being visible — **and without it having been erased**, which "B cannot see A" on
> its own would not distinguish.
>
> It proves the credential chain with a **probe file**, never through `.credentials.json`: on a
> real Mac that file is a live Claude login, and a test that wrote through it would destroy what
> it measured. Running it by hand is the same command as any other twin
> ([§0.2](#02-the-job-that-runs-them)).
>
> ⚠ **Running the item BY HAND needs a pack selected.** `.claude` and
> `.claude-shared-credentials` are pack declarations, so a user config that selects no packs
> produces no `~/.claude` at all — an empty result that looks like a failure and is not. The
> twin writes `packs: ["claude"]` into an isolated user config for exactly this reason.

---

## 6. The floor is on the sandbox's PATH — NEW 2026-09-12, NEVER RUN

**This is the root of the chain. If it fails, items 7-9 cannot pass and need no separate
report.**

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'for b in mise node npm git rg fd jq gh curl; do printf "%-6s %s\n" "$b" "$(command -v $b || echo MISSING)"; done; which --version 2>&1 | head -1'
```

**Expect:** every name resolves, and each of the first nine resolves into a `/nix/store/…`
profile rather than `/usr/bin`. `which --version` must print **nothing useful** — macOS's own
`/usr/bin/which` has no `--version` — because GNU `which` was removed from the floor on
2026-09-12 and its presence would mean the policy exclusion did not take effect.

**Settles:** that [`OQ-P1`](../../design/macos-user-provisioning.md#decision-ledger)'s floor
actually reaches the sandbox — the single largest unmeasured claim of the whole pair — and
that [`OQ-P2`](../../design/macos-user-provisioning.md#decision-ledger)'s no-GNU-userland
ruling is true of the shipped article and not just of the exclusion list.

**If `mise` or `npm` is MISSING**, stop: nothing below can pass, and this is the bug to file,
with the output of `yolo --dry-run` (which names the profile path the launch built).

**How big the floor is, if you need the number:** `len(darwinpkg.FloorNames())` — the image core
minus what darwin cannot build minus the GNU userland. Read it there rather than from a doc; the
count has already drifted once in this corpus, on the commit that removed `which`.

> [!NOTE]
> **Automated twin: `integration/TestMacosUserFloorReachesTheSandboxPath`.** It launches a real
> sandbox and makes both of this item's assertions, and the policy half MORE COMPLETELY than the
> command above: one probe per entry of `darwinpkg.FloorExcludedPolicy` — `stat`, `find`, `sed`,
> `grep`, `awk`, `patch`, `diff`, `tar`, `which` — where absent and "the system's copy answered"
> both pass and only a `/nix/store/…` path fails. That is the whole of
> [`OQ-P2`](../../design/macos-user-provisioning.md#decision-ledger) rather than its most famous
> entry. The package→binary table is checked against the exclusion list in both directions by a
> Linux test, so a package added to that list without a probe fails `just test-fast` before any
> Mac is involved.
>
> **The `which --version` line above stays the human spelling**; the twin asserts the RESOLVED
> PATH instead, because what `/usr/bin/which` prints for an unknown flag is Apple's business and
> not a claim this repo can pin. Two other spellings that do not survive automation, for anyone
> extending the probe: `ls` reports the generated bashrc's ALIAS rather than a path (the twin
> uses `stat` as the coreutils stand-in), and `$(logname)` — item 2's spelling — fails with no
> controlling terminal.
>
> Running it does not retire this entry: the runbook is still the spec, and a human re-running it
> is still the instrument for everything the gate cannot reach.

---

## 7. The provisioning stage runs, and is confined — NEW 2026-09-12, NEVER RUN

Use a workspace whose config declares one cheap tool, e.g. `{"mise_tools": {"jq": "latest"}}`.

```console
$ YOLO_RUNTIME=macos-user yolo -- true
$ cat <workspace>/.yolo/startup.log
```

**Expect:** the launch prints the provisioning banner and pauses before the agent; the log
exists, is **truncated to this launch** (its first line is `=== yolo provisioning <date> ===`),
and records `mise install` running. No `PROVISIONING FAILED` line.

**Settles:** [§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)
items 1, 2, 3 and 4 at once — that `sandbox-exec` accepts the stage process, that the confined
stage reaches the network, that it can write into the sidecar symlinks, and that
`sudo --user=… env -i … sandbox-exec …` forwards the script **verbatim** rather than mangling
it the way `sudo --login` does. Item 4 is the one to watch: a mangled script does not error,
it provisions nothing and exits 0, so the evidence is the LOG's content, never the exit code.

⚠ **Also time it** ([§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)
item 5): `time` the first launch of an LSP-configured workspace. Every `mise install` plus one
`npm install -g` per server runs in series before the agent starts, and nobody knows what that
costs. **The twin now carries this**, as two logged numbers — before the stage, and the stage
plus the agent — split at the banner, which is the stage's own first instruction. The first
green nightly is the measurement.

~~⚠ **And note the working directory** (item 9). Run `yolo` once from outside the workspace
tree.~~ **STRUCK 2026-09-12: that launch cannot be spelled.** The workspace *is* the working
directory — `internal/cli/run/runcmd.go` sets it from `os.Getwd()` with no walk-up, no flag, and
no host-side environment override (`YOLO_WORKSPACE` is a jail-side input, read by the config
loader inside the jail). Standing outside the tree does not launch this workspace from
elsewhere; it launches a **different** workspace. The stage then inherits that cwd — `runReal`
sets no `cmd.Dir`, and neither `sudo` without `--login` nor `env -i` changes directory — so it
always runs in the workspace it is provisioning, and a workspace-local `mise.toml` is read.
[§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle) item 9 goes with
it, for the same reason.

> [!NOTE]
> **Automated twin: `integration/TestMacosUserProvisioningStageRunsAndRecordsItself`.** One
> launch settles [§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)
> items 1-4 together: the log exists where `provision.StartupLog` says it
> should (so `sandbox-exec` accepted the process and the confined stage could write the
> sidecar), `↳ mise install` succeeded (so it reached the network), no `PROVISIONING FAILED`,
> and the banner's timestamp **parses and falls inside this launch's own window** — which is the
> item-4 check, because a `--login`-mangled script provisions nothing and exits 0. A sentinel
> seeded before the launch pins that no byte of a previous launch survived the truncation.
>
> ⚠ **"Is confined" is only half covered, and the twin covers the runtime half.** That the argv
> carries `/usr/bin/sandbox-exec` and does NOT carry `sudo --login` is pinned by
> `internal/macosuser/provision_test.go` and refused by `PlanInvariants`, so the twin
> deliberately does not re-assert it — and would **not** catch someone deleting `sandbox-exec`
> from the argv. Two tests, two halves; neither is redundant.

---

## 8. A stage that cannot START does not kill the launch — NEW 2026-09-12, NEVER RUN

```console
$ sudo chmod 000 /usr/bin/sandbox-exec     # or point the profile path at a missing file
$ YOLO_RUNTIME=macos-user yolo -- true ; echo "rc=$?"
$ sudo chmod 755 /usr/bin/sandbox-exec     # PUT IT BACK
```

**Expect:** the launch prints *"The provisioning stage could not be started … Launching
anyway"* and **the agent still runs**. It must NOT print *"Provisioning was aborted"*, and
`rc` must not be 1 on account of the stage.

**Settles:** [§4](../../design/macos-user-provisioning.md#4-the-proposed-shape)'s bolded rule
— *a failing stage must not abort the launch* — which the code inverted until 2026-09-12 for
exactly this class (see
[§10.7](../../design/macos-user-provisioning.md#107-the-failure-policy-the-code-did-not-implement)).
The complementary half is the veto: make a declared tool fail to install (a `mise_tools`
version that does not exist), answer **n** at the prompt, and confirm the launch DOES stop.

⚠ **Pick a reversible way to break it.** The `chmod` above is one; restore it in the same
session. Nothing in yolo needs modifying to run this check.

> [!NOTE]
> **A twin exists for a THIRD case this item only implied, and BOTH halves named above stay
> manual.** `integration/TestMacosUserAFailingProvisioningStageDoesNotAbortTheLaunch` gives the
> launch a `mise_tools` version that cannot resolve and, non-interactively, requires the stage
> to fail, say so on the console and in the log, and **let the agent run anyway**. That is
> [§4](../../design/macos-user-provisioning.md#4-the-proposed-shape)'s rule end-to-end, under a
> real Seatbelt profile, which nothing had ever run. It asserts the EVIDENCE before the verdict —
> the marker in the log and the red console line first — because a stage that silently succeeded
> would otherwise satisfy "the launch survived" while testing nothing.
>
> **Neither half above can be automated as written:**
>
> - **The exec-layer injection.** `chmod 000 /usr/bin/sandbox-exec` is a global, SIP-adjacent
>   mutation with a window in which the machine is broken for every process — no test should
>   make it. No process-local substitute exists either: the stage argv names
>   `/usr/bin/sandbox-exec` absolutely, so a PATH shim cannot reach it, and the profile is
>   installed root-owned by the same launch that consumes it. Closing this needs a SEAM in
>   `internal/macosuser`, not a cleverer test.
> - **The veto.** `provision.Script` gates its prompt on `[ -t 0 ]`, and no test in this suite
>   gives a child a terminal (`cmd.Stdin` is nil, so `/dev/null`) — deliberately, and shared by
>   the whole suite. The twin does assert that **no unanswerable prompt is emitted** without a
>   tty, which is the failure the gating exists to prevent.

---

## 9. `mise_tools` and `lsp_servers` actually arrive — NEW 2026-09-12, NEVER RUN

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'mise ls --installed; ls ~/.yolo/mise/installs; ls ~/.npm-global/bin'
```

**Expect:** the declared tools are installed; the mise store is under **`~/.yolo/mise`**, in
the ACCOUNT home; and `~/.npm-global` (a symlink into `<workspace>/.yolo/home`) holds the LSP
binaries.

**Settles:** the two launch warnings retired on 2026-09-12 — which were removed on the
strength of code that had never run. If either tool is absent, that retirement was premature
and both warnings should come back
([§10.6](../../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)).

⚠ **Check the TIER while you are here**, because it is the one thing a later launch cannot
undo: `~/.yolo/mise` must be a real directory in `/Users/_yolojail`, **not** a symlink into any
workspace. A per-workspace mise store is the inverse of every other backend, and a second
workspace's launch is what would reveal it.

> [!NOTE]
> **Automated twin: `integration/TestMacosUserDeclaredToolsArrive`** — four subtests over ONE
> launch, since a macos-user launch builds a native closure and then installs from the network,
> so four questions in four launches would cost four of those. Nothing of this item is left
> manual, the ⚠ tier check included: it asserts `~/.yolo/mise` is a real directory in the account
> home **after** asserting its sibling `~/.yolo/bin` is a symlink, because without that contrast
> "not a symlink" also passes on a launch where the layout never ran at all. Every failure
> attaches `<workspace>/.yolo/startup.log`, which on a CI run is all its reader gets.

> [!WARNING]
> **The `lsp_servers` half is expected to FAIL, and the failure is wiring rather than hardware.**
> A source reading made 2026-09-12, not a measurement: the generated bootstrap script installs
> from `$YOLO_LSP_NPM_INSTALL` / `$YOLO_LSP_GO_INSTALL`, and the **only** producer of either
> variable in the whole tree is the container's podman `-e` lines
> (`internal/cli/run/assemble.go`). macos-user sets `YOLO_LSP_SERVERS` — the table that renders
> agent config — and nothing else, so the stage execs the script, finds an empty install list,
> and **exits 0 having installed nothing**: precisely the "reports success having provisioned
> nothing" mode the stage was warned about.
>
> Three published claims are therefore false today —
> [`../../guides/macos.md`](../../guides/macos.md)'s `lsp_servers | installed, since 2026-09-12`,
> the same row in [`../../design/provisioner-sets.md`](../../design/provisioner-sets.md), and
> [§10.6](../../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)'s
> retirement of the `lsp_servers` warning. **The `mise_tools` half is unaffected** and is the one
> this item's verdict should turn on. A fix has to set both variables into the stage env AND into
> the bootstrap env, since the same three readers live in both processes; it was left undone
> deliberately, because [§10.6](../../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)
> frames the outcome as a maintainer's choice — bring the warning back, or wire the variable.

---

## 10. The two layout defects a mutation pass found — NEW 2026-09-12, NEVER RUN

Both are REFUSAL-message quality rather than data loss, and both were found by reasoning
rather than measurement. **The first has since been fixed and automated; the second is
deliberately neither.**

```console
$ mkdir -p <workspace>/.yolo/home/.claude-shared-credentials   # occupy a MIRROR path
$ YOLO_RUNTIME=macos-user yolo -- true
```

**Expect (FIXED 2026-09-12, and this item is now the verification of the fix):** the launch
refuses, names the path **under the workspace**, and prescribes `sudo rm -rf <that path>` — a
command that reaches it. It still names the account reset beside it, so nobody runs that one
from memory, while saying outright that the reset does not touch a sidecar path. The refusal
collects occupied paths in two groups — account home, workspace sidecar — and offers each the
remedy that reaches it.

~~**Expect (before the fix):** … prescribes `sudo rm -rf /Users/_yolojail`~~ — a command that
does not touch the offending path, so following it left the launch refusing forever. **A remedy
that cannot reach the path it names is worse than none, because it looks actionable.**

**Why it is reachable — ⚠ NOT for the reason this item gave until 2026-09-12.** It said
`run/prepare.go`'s `migrateOldOverlay` copies every `packload.SharedDirs` entry INTO
`<ws>/.yolo/home`. It copies the other way: `migrateOldOverlay(wsState/<dir>,
GlobalHome/<dir>)` — old source first — so it never creates a sidecar copy. The real route is
the era that call exists to repair: **Apple Container mounted no shared dirs until 2026-08-24**
and bound `/home/agent` straight at the workspace state dir, so an AC jail from before then left
a real `.claude-shared-credentials` sitting in that workspace's sidecar. Defect reachable;
mechanism was misstated.

> [!NOTE]
> **Automated twin for the first defect: `integration/TestMacosUserLayoutRefusesAnOccupiedSidecarMirror`**
> — this item's own recipe, plus what a unit test cannot see: the launch **stops**, the agent
> **never runs**, the refusal survives the boot's step wrapper and the sudo boundary intact, and
> nothing later in the boot deletes the directory it declined to migrate (a sentinel file inside
> it is read back afterwards). The message half is pinned separately on Linux, so a regression in
> the wording is caught by `just test-fast` rather than by a Mac.
>
> The twin re-runs `yolo macos-fix-permissions` after creating the occupied directory. That is
> not ceremony: the shared-group ACL inherits only at CREATION, so without it the launch fails on
> permissions several steps before the refusal under test.

**The second, which needs no command:** if `LoadJailPacks` ever fails transiently (a corrupt
or half-written pack root), the bootstrap continues anyway (A12) with an EMPTY link set, so
`install_home_overlay` writes a REAL `~/.claude` — and every later launch refuses forever,
with the same remedy, which here destroys the machine tier the shared-credentials hook exists
to preserve. If you ever see that refusal on an account you did not touch, this is the likely
route.

> [!CAUTION]
> **The second defect is NOT automated, and must never be.** Reaching it needs a fault injected
> into `LoadJailPacks` — a seam that does not exist — and its failure mode is a **permanently
> poisoned account home** whose only remedy destroys the machine tier the shared-credentials hook
> exists to preserve. No CI job should be able to reach that state by accident, on a Mac that
> belongs to somebody. If it is ever run, it is run on a machine nobody cares about.

---

## Known-absent, do not report as bugs

- **`mcp_presets`** are not delivered here — the wrappers hardcode Linux paths this
  backend never provisions. The launch says so.
- ~~**`mise_tools` are not installed.**~~ **FIXED 2026-09-12, and unverified on hardware** —
  see items 6, 7 and 9. All three grounds this entry gave are now false: the floor puts `mise`
  on the sandbox's PATH, `MISE_DATA_DIR` names a real machine-wide store, and the confined
  provisioning stage runs `mise install` and the generated bootstrap script before the agent
  starts. Until a Mac says otherwise, treat an absence here as a bug WORTH reporting, with the
  contents of `<workspace>/.yolo/startup.log`.
- **`lsp_servers` are STILL not installed** — the strike-through this entry carried from
  2026-09-12 was **restored on 2026-09-12** the same day, on a source reading rather than a
  measurement: nothing on this backend sets the two variables the install loop reads. Item 9's
  ⚠ has the whole chain and the two places a fix has to land. Report an absence here only if it
  is still absent **after** those variables are wired.
- **`per_side_paths`, `resources`, `cache_relocations`** are read and ignored, each
  for a structural reason (no mount namespace, no cgroups, no binds). Each warns.
- ~~**One home for every workspace.**~~ **FIXED 2026-09-12, and unverified on hardware** —
  see item 5, which is the check that settles it.
- **A jail here cannot launch a jail.** `sudo` cannot exec under Seatbelt and
  `sandbox_apply` refuses a second profile, so verify macos-user changes from an
  unsandboxed shell.
