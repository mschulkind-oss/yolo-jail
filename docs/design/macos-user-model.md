---
title: "macos-user's model: a second account, or a sandbox that starts from nothing"
date: 2026-10-22
status: in-review
stage: DESIGN
next: "Rule OQ-MM10 (what an absent macos_user_mode means on a Mac set up for the account); slice 12a, two workspaces at once in account mode, is built and waits on its Mac witness (W1)"
tags: [macos-user, seatbelt, design, isolation, parity, safehouse, sandvault]
summary: "Today macos-user runs the agent as a second Mac account under a Seatbelt profile that allows everything and denies a short list (SandVault's model). Ruled 2026-10-22: macos-user moves to Safehouse's model, the agent running as you under a profile that denies everything and allows a list, the closest macOS gets to podman's start-from-nothing container; the second account is kept from day one as an optional mode, off by default, on the same profile. This doc lays the models side by side, maps podman's pieces onto the Safehouse model, and names what the second account buys and costs."
vantage:
  status-chip: true
---

# macos-user's model: a second account, or a sandbox that starts from nothing

**Status:** 2026-11-03 — the model is ruled ([§7](#7-the-ruled-model-two-modes-one-profile)).
Ten of [the plan](macos-user-model-plan.md)'s thirteen slices are built, opt-in behind
`macos_user_mode = "self"`. [Slice 12a](macos-user-model-plan.md#MMP-D25), built 2026-11-03 and
awaiting its Mac witness, moves the production default, the second account, onto the same
deny-by-default profile with a home per workspace, so a second workspace launches beside a running
session instead of being refused. The
[§6](#6-what-a-prototype-must-show) prototype passed in both modes on 2026-10-23. This document
also carries the account mode's concurrency rulings, made in the retired concurrent-workspaces
design ([OQ-MCW2](#decision-ledger) to [OQ-MCW4](#decision-ledger)).

**Needs your ruling:** [OQ-MM10](#OQ-MM10). **Ruled 2026-10-23:** [OQ-MM7](#OQ-MM7), [OQ-MM8](#OQ-MM8) (a trial), [OQ-MM9](#OQ-MM9), raised by the [first Mac run](#first-mac-run-2026-10-22).

**Reads with:** [Agent Safehouse research](../research/agent-safehouse.md) (the evidence for every
Safehouse claim here), [SandVault and Safehouse compatibility](sandvault-safehouse-compat.md), and
[the configurable workspace root](configurable-workspace-root.md), whose questions this doc puts on
hold.

## 1. The problem in one example

You keep client code in `/opt/src/clientX` and your project in `/Users/Shared/yolo/alpha`. You run
`yolo -- claude` in alpha on macos-user. The agent can read `/opt/src/clientX`, and anything else
outside `/Users` that macOS lets every user read. It can't write there. Wherever the agent launches
makes no difference.

The reason is the profile's shape, not a missing rule: it begins with `(allow default)` and denies
reads only under `/Users`, on external disks, of raw disks and of keychains
([`seatbelt.go`](../../internal/macosuser/seatbelt.go)). Every new place a secret can live needs
another deny. Podman has no such list: a container starts empty and sees only what yolo puts in it.

## 2. The three models

| | **SandVault** (yolo today) | **Hybrid** | **Safehouse** |
| :--- | :--- | :--- | :--- |
| Who the agent runs as | a second account, `_yolojail` | a second account | **you** |
| Profile base | allow everything, deny a list | deny everything, allow a list | deny everything, allow a list |
| What people picture | "a second user who can't see other homes" | "podman, plus a second user" | "podman: it sees only what yolo lists" |
| Workspace inside your home | refused | refused | **works** |
| One-time admin setup | yes ([privilege lifecycle](macos-user-privilege-lifecycle.md)) | yes | **none** |
| Access grants on project folders | yes (`macos-fix-permissions`) | yes | **none**: the files are yours |
| A projects folder | yes ([configurable root](configurable-workspace-root.md)) | yes | **none** |
| Two workspaces at once | refused while a session runs: every workspace shares one account home | **each launch gets its own home** ([slice 12a](macos-user-model-plan.md#MMP-D25), which moves the account mode here) | **each launch gets its own profile and home** |
| Agent logins | bridged across accounts ([Claude refresh](macos-user-claude-refresh.md), [keychain from a jail](keychain-from-a-jail.md)) | same | rendered into the launch's own home, as on podman |
| What stops a missed rule | Unix permissions on your 0600/0700 files | Unix permissions | nothing: the profile is the whole wall |

SandVault and yolo share the same file rules: yolo's profile calls itself *"SandVault-parity"*, so
both expose `/opt/src/clientX` in the same way. SandVault additionally denies mounting and three
system services; yolo plans to adopt those
([A2](sandvault-safehouse-compat.md)).

## 3. Podman, mapped onto the Safehouse model

The parity model: every piece of the podman jail gets the nearest macOS equivalent, and where there
is none the doc says so.

| Podman jail | Safehouse-model equivalent | Same? |
| :--- | :--- | :--- |
| Empty root filesystem from an image | `(deny default)` | yes |
| The image's `/usr`, `/bin`, `/nix/store`, read-only | read allowed on the system and nix paths tools need | yes |
| Workspace bound read-write at `/workspace` | read-write allowed on the workspace **at its real path** | **no path relocation** |
| `/home/agent`, backed by `.yolo/home` | `HOME` set to `<workspace>/.yolo/home`, read-write allowed | yes, at a real path |
| `mounts` (read-only or read-write), bound at `/ctx/<name>` | a link at `$YOLO_CONTEXT_DIR/<name>`, with read, or read-write, allowed on the original path because the link needs it (what macos-user already does, [context mounts](../reference/context-mounts.md)) | yes: `$YOLO_CONTEXT_DIR/<name>` is the one supported path on every backend ([OQ-MM5](#OQ-MM5)) |
| `host_files` copied into the home | rendered into that `HOME` the same way | yes |
| Shared caches (mise, tool caches) | read-write allowed on the machine cache directories | yes |
| PID namespace: no host processes visible | cross-process inspection denied (built today); signals to processes outside the sandbox denied | yes |
| Network bridge with its own loopback | network open; **the host's loopback denied** except yolo's own service ports and ports the workspace config allows ([OQ-MM3](#OQ-MM3)) | yes |
| An environment yolo builds from scratch | the same: the launcher builds the environment | yes |
| A user that is not you | **you** | **the trade, see [§4](#4-what-the-second-account-buys-and-what-it-costs)** |
| No host services | system services denied by default, allowing a short list (Safehouse allows 16) | yes |

**The two real differences.**
- **No path relocation.** Seatbelt only allows or denies real paths. There is no unprivileged bind
  mount on macOS, so the agent sees `/Users/you/code/alpha`, not `/workspace`. Anything that assumes
  `/workspace` or `/home/agent` must read `$PWD` and `$HOME` instead. (A root-level `/workspace`
  through `/etc/synthetic.conf` is per machine and needs a reboot, so it can't serve two
  workspaces at once.)
- **Same identity.** Covered next.

## 4. What the second account buys, and what it costs

**What it buys.** Three things. Only the first is lost by moving to the Safehouse model; a
deny-by-default profile replaces the other two, so they are no reason to keep the account:

1. **A backstop under the profile — lost, and small.** Under deny-by-default a *missing* rule fails
   closed: a tool that needs a path breaks, and nothing is exposed. Exposure needs an allow rule
   that is too broad (a parent folder instead of the one subfolder a tool needs, such as all of
   `/opt` instead of `/opt/homebrew`) or a Seatbelt bug. Only then would Unix permissions on your
   owner-only files still have stopped the read under a second account. Both are reviewable: the
   allow list is short and each allow is tested ([§6](#6-what-a-prototype-must-show)). This is the
   one benefit nothing replaces, and it is the maintainer's *"except for user readable files"*.
2. **Separate per-user services — replaced.** As a different user, the agent can't reach your
   keychain, clipboard or Apple Events (which can drive your other apps). Running as you, a
   deny-by-default service list does the same; Safehouse denies the keychain unless asked.
3. **Separate privacy grants — replaced.** Running as you, the agent would inherit macOS privacy
   grants you gave your terminal, such as Full Disk Access, which a second account does not have.
   That should not matter: macOS checks a Seatbelt deny separately from those grants, so a file the
   profile denies stays denied even under Full Disk Access. This is expected, not measured;
   [§6](#6-what-a-prototype-must-show) checks it.

**What it costs.** Every row of [§2](#2-the-three-models) marked "yes" in the SandVault column: the
admin setup, access grants, the projects folder and its open questions, the shared account home that
the two-workspace plan exists to work around, the credential bridges across accounts, and the
root-owned pack trees ([JD-10](../reference/macos-user-provisioning.md#JD-10)). It also refuses the
most natural place to put a project: your home folder.

It costs every user a second mental model on top of the sandbox: two accounts, access grants
(ACLs) that let the agent's account reach a folder, and which folders are shared between the two.
Podman has none of that, and neither does the Safehouse model, where the files are simply yours
and the profile is the only thing to understand.

**The stray-grant risk is not a blocker.** The maintainer ruled on 2026-10-22 that the risk of an
access grant on a home folder exposing `~/.ssh` is *"manageable, acceptable, not a blocker"*. Under
the Safehouse model no such grant exists anyway.

## 5. What the Safehouse model must deny explicitly

Running as you, everything the second account used to keep out must be named in the profile:

- your home folder, apart from the workspace if it sits there (deny by default covers it);
- the keychain: the files and the security service lookup;
- the ssh agent socket, by its file path and its network spelling (Safehouse has a module for exactly
  this);
- Apple Events, the clipboard, accessibility and notification services;
- signals to your processes, and inspecting them;
- your loopback services, except yolo's own and the ports the workspace allows ([OQ-MM3](#OQ-MM3)).

## 6. What a prototype must show

On a Mac, with a hand-written profile and no yolo changes:

1. An agent session (Claude) logs in from a home under the workspace, edits files and runs a build.
2. As the agent: reading `~/.ssh`, the login keychain (`security find-generic-password`), the ssh
   agent, `osascript` driving another app, the clipboard, and `kill` against one of your processes
   all fail.
3. `/opt/src/clientX` is unreadable; `/opt/homebrew` and the nix store are readable.
4. With Full Disk Access granted to the terminal, a file the profile denies is still unreadable.
5. Nothing escapes through a service running as you: `open -a Terminal`, `osascript -e 'do shell
   script …'` and `launchctl submit` all fail from inside.
6. Claude logs in and stays logged in with the keychain denied to the sandbox.
7. Steps 1 to 3 pass again with the same profile run as the second account.

The [prototype runbook](../plans/runbooks/macos-user-model-prototype/README.md) is that profile
and a script that runs these checks in both modes. That run is the gate before building. The existing macos-user policy suite
([§8.1](../research/agent-safehouse.md#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost))
is where its checks become permanent tests.

### First Mac run (2026-10-22)

The maintainer ran the prototype on macOS 26.5 (arm64), as you and as `_yolojail`. Each mode had 30
passes (29 as the account, which skips the Full Disk Access check) and the same two failures.

**What passed, in both modes unless noted:**

- **The agent's own work:** the profile loads; the workspace, home and temp dirs are writable;
  signals reach processes inside; git init and commit, a C build, a Homebrew binary and a nix binary
  all run; HTTPS works with certificate trust (`curl`, `gh`, `git ls-remote`); the system fonts read.
- **Your files:** the decoy secret, `~/.ssh`, writing in your home and the login keychain file are
  all refused. As the account, Unix permissions refused these even without the profile, so there
  they test the backstop rather than the profile.
- **Your session:** `do shell script` through System Events, `open -a TextEdit` and
  `launchctl submit` all fail.
- **Outside `/Users`:** the decoy project and its parent are unreadable; `/opt/homebrew` and the nix
  store are readable.
- **Loopback:** a listener of yours is unreachable until its port is listed, then reachable.
- **Full Disk Access** (self mode, granted): profile-denied files, `TCC.db` included, stay unreadable.

The ssh agent and self-mode clipboard checks were skipped (no `SSH_AUTH_SOCK`, empty clipboard), and
the optional Claude check was not run.

**The two failures, and what changed:**

- **Apple Events reached Finder — a false alarm, found 2026-10-23.** `osascript -e 'tell application
  "Finder" to get name'` printed `Finder` inside the sandbox. AppleScript answers an application's
  name from the specifier without sending an event, so that check passes under any profile. A real
  event (`count windows`) fails inside with or without the `(deny appleevent-send)` the profile
  gained; the rule stays as defense in depth, and b8 now counts windows.
- **The keychain check was inconclusive.** Searching for an item that does not exist returns the
  same exit code whether or not the keychain service was reached. It now adds a throwaway item to
  your login keychain outside the sandbox and passes only if its secret does not come out inside.
  The profile also denies the keychain service's real names, `com.apple.SecurityServer` and
  `com.apple.securityd`; the rerun is what proves them.

**Fixed in the script, not the profile:** the process the signal and command-line checks target had
already exited, because macOS `sleep` rejected its argument, so those two passes proved nothing. They
now target a long-lived process. `/bin/ps` would not run inside the sandbox at all, being setuid root,
so the command-line check also reads `kern.procargs2` directly.

**Awaiting rulings, rules unchanged:**

- **Loopback (d3).** Denying the host's loopback also blocks the agent's own servers, including
  test servers on random ports, because Seatbelt cannot tell who is listening.
- **The shared `/tmp` (b14).** In self mode a 0600 file of yours in `/tmp` is readable from inside;
  as the account, Unix permissions refuse it.

### Self-mode rerun (2026-10-23)

Run on the same Mac (macOS 26.5, arm64) at `fc7b1c56e`, self mode only, against both the
hand-written [`profile.sb`](../plans/runbooks/macos-user-model-prototype/profile.sb) and the
profile yolo generates (`--profile-from-yolo`). **Both: 33 PASS, 0 FAIL.** b4's throwaway keychain
item stayed hidden, b12 and b13 hit a live process and were refused, b6 covered the ssh agent's
launchd socket, and e1–e2 counted, the terminal holding Full Disk Access. Skipped: b7 (the clipboard
held no text) and f1 (opt-in). b14 and d3 read as before under both profiles. Account mode is not
yet rerun.

- **b8 had been a false alarm.** AppleScript answers `get name` of an application from the
  specifier without sending an event, so the first run's check passed under any profile. A real
  event (`count windows`) is refused inside, with or without `(deny appleevent-send)`, which stays
  as defense in depth. b8 now counts windows, in the script and in
  `TestMacosUserDenyDefaultProfileEnforcesItsRules`.
- **The integration suite's first native run** found one real gap: the generated profile's
  `/dev` metadata allow out-ranked the raw-device `file-read*` deny, so `ls -l /dev/disk0`
  succeeded. The deny now names `file-read-metadata` too. The same gap leaves the
  metadata (not the contents) of `/System/Library/Keychains` visible, which holds Apple's public
  root certificates; left as is.

**Both modes after the rulings (2026-10-23, later the same day).** With loopback open and the shared
`/tmp` closed as built, `--profile-from-yolo` passed with no FAIL in self mode (43 PASS) and in account
mode (42 PASS), the account crossing through yolo's own run-as rule with no password. Details and the
skips are in [the lab report](../lab-reports/2026-10-23-native-mac-verification.md#1-the-prototype-both-modes-against-the-generated-profile).

## 7. The ruled model: two modes, one profile

Ruled 2026-10-22 ([OQ-MM1](#OQ-MM1), [MM-R2](#decision-ledger)):

- **One profile, deny by default.** Every macos-user launch runs under a deny-by-default Seatbelt
  profile covering files, system services, signals and process inspection, built as
  [§3](#3-podman-mapped-onto-the-safehouse-model) maps podman's pieces. Which user runs it is a
  parameter of the profile, not a second profile.
- **Default mode: the agent runs as you.** No account, admin setup, access grants or projects
  folder. A workspace may be anywhere, including your home folder. Everything [§5](#5-what-the-safehouse-model-must-deny-explicitly)
  lists is denied.
- **Optional mode: the second account, off by default, built from day one.** The same profile run
  as `_yolojail`. The mode alone brings the account's setup and switching, and the access grants
  that let the account read and write the workspace, because Unix permissions still decide file
  access under a different user. The maintainer's reason for building it now: *"we already have a
  lot of this machinery built, and I think it's going to help us smooth out the rest of it."*
- **Shared by both modes:** a per-launch `HOME` under the workspace's `.yolo/home`, so two
  workspaces run at once in either mode without a shared account home; mounts at their original
  path with a context-dir link; the host keychain reached only through the loophole the
  [keychain design](keychain-from-a-jail.md#41-macos-user-no-seam-and-probably-no-keychain) rules
  ([OQ-KC4](keychain-from-a-jail.md#8-decision-ledger): *"always the host's keychain"*), never by the sandbox.
- **The profile starts from Safehouse's** layered profiles (Apache-2.0, credited), adapted to nix
  and narrowed wherever they read wider than yolo wants: install trees such as `/opt/homebrew`,
  never a parent such as `/opt` ([OQ-MM2](#OQ-MM2)).
- **Loopback is denied by default**, as podman's own loopback keeps your local services out of
  reach. yolo's own service ports are allowed. A workspace may allow more ports in its config; a
  change to that list goes through the same config-change approval as any other workspace config
  edit ([config safety](../reference/config-safety.md)). The key's name is the plan's to choose
  ([OQ-MM3](#OQ-MM3)).
- **Mounts are reached at `$YOLO_CONTEXT_DIR/<name>`** on every backend. Containers do not also bind
  them at the original host path. On macOS the profile allows the original path only because the
  link resolves there; a script that uses the original path directly works on macOS by accident,
  and nothing promises it ([OQ-MM5](#OQ-MM5)). Every backend also puts a `ctx` link in the agent's
  home pointing at `$YOLO_CONTEXT_DIR`, so mounts are browsable from where the agent starts;
  `$YOLO_CONTEXT_DIR` stays the documented path ([OQ-MM6](#OQ-MM6)).
- **No transition for Macs already set up for the second account.** Launches move to the default
  mode, and nothing migrates, cleans up or announces the old account. Reusing an existing account
  for the optional mode is welcome if it falls out of the build, but is not required
  ([OQ-MM4](#OQ-MM4)).
- **No per-language home grants.** Unlike Safehouse's toolchain files, the profile does not open
  your home's toolchain folders: tools run with the agent's own home. Package caches are shared
  with your real caches by the general rule for every backend
  ([OQ-BF11](../reference/disk-footprint.md#OQ-BF11)), which needs the default mode's
  ownership; their config files, which hold registry tokens, never are.
- **Selecting the mode** is a user-scope setting; a workspace config cannot choose it. Its name and
  spelling are the plan's to choose.
- **What done looks like:** with no setting, `yolo -- claude` in `~/code/alpha` launches with no
  admin step, and the agent cannot read `/opt/src/clientX`, `~/.ssh` or the keychain; with the
  optional mode set on a Mac where setup ran, the same launch runs as `_yolojail` with the same
  denies; two workspaces launch at once in either mode.

## 8. Open questions

1. <a id="OQ-MM7"></a>✅ **[`OQ-MM7`](#OQ-MM7) — On macOS, does loopback stay denied, now that it blocks the agent's own servers?**


   [OQ-MM3](#OQ-MM3) ruled loopback denied except listed ports, matching podman. The first Mac
   run (check d3) showed the cost: Seatbelt filters by port, not by process, so a server the agent
   starts on a random port and connects to itself is blocked too. Podman avoids this only because a
   jail has its own network namespace, which macOS cannot give without a VM. Allowing loopback
   leaves your local services (a dev database, say) reachable from the jail, as macos-user is today.

   _Leaning:_ allow loopback on macOS; podman keeps its private loopback.

   **Answer:**
   > **Ruled in review 2026-10-23, as leaned:** allow all loopback on macOS. *"Let's just allow it
   > access to the loopback. I don't see any other solution. ... if you have loopback services that
   > are sensitive and relies on this I think you also get what you deserve here because that's poor
   > security practice. The random port thing is like a weird thing that's going to break mental
   > models ... We're basically just in network mode host. So we'll note that wherever it's needed
   > to be noted, ... and note that there is no other choice."* macos-user shares the Mac's loopback
   > as podman's host network mode does; a sandbox without a VM has no private one. This replaces
   > [OQ-MM3](#OQ-MM3)'s port list on macOS. Podman jails keep their private loopback.

2. <a id="OQ-MM8"></a>✅ **[`OQ-MM8`](#OQ-MM8) — In the run-as-you mode, may the agent read your files in the shared `/tmp`?**


   Check b14: running as you, the agent reads your owner-only files in `/tmp`; the account mode is
   protected by Unix permissions. The shared `/tmp` also holds yolo's own per-session endpoint
   directories for its host services, so an agent of one session can read another session's. Denying `/tmp` reads would need tools pointed at the sandbox's own
   temp dir, and breaks the ones that hardcode `/tmp`.

   _Leaning:_ leave it readable; document it.

   **Answer:**
   > **Ruled in review 2026-10-23, against the leaning, as a trial:** deny the shared `/tmp` by
   > default, with a user-scope setting that allows it again. *"Let's try B. Let's see how it
   > works, and if it's terrible we'll change it, and yes we'll have the hatch."* The sandbox's own
   > `TMPDIR` and this session's service files stay reachable; the per-user temp folder under
   > `/var/folders` stays allowed, because Apple's `mktemp`, Java and Chrome use it and ignore
   > `TMPDIR`; zsh and tmux are pointed at `TMPDIR`; and the briefing tells the agent to use
   > `$TMPDIR`, since Seatbelt's refusal cannot name a next step.

3. <a id="OQ-MM9"></a>✅ **[`OQ-MM9`](#OQ-MM9) — Is host software (Homebrew) visible to the agent?**

   The prototype already allows `/opt/homebrew`, `/usr/local` and `/Applications` read-only. The
   alternative is nix-only tools, matching the container exactly. Either way your home's toolchain
   folders (and the tokens in their configs) stay hidden.

   _Leaning:_ visible read-only, declared packages first on `PATH`.

   **Answer:**
   > **Ruled in review 2026-10-23, as leaned:** *"Host software visible? A. Yes. That's what we
   > decided."* Install trees (`/opt/homebrew`, `/usr/local`, `/Applications`) are readable and
   > runnable; your home's toolchain folders stay hidden; declared packages come first on `PATH`.

4. 💬 **<a id="OQ-MM10"></a>[OQ-MM10](#OQ-MM10): When the default becomes "run as you", what happens on a Mac that was set up for the second account and has no `macos_user_mode` line?**

   Dana set up `_yolojail` with `yolo macos-setup` in October, keeps her projects under
   `/Users/Shared/yolo`, and has never written `macos_user_mode`; her user config is otherwise
   ordinary. She upgrades yolo and runs `yolo -- claude` in a project as always. On 2026-10-22
   you ruled ([OQ-MM4](#OQ-MM4)) *"No transition for Macs already set up for the second account.
   Launches move to the default mode, and nothing migrates, cleans up or announces the old
   account."* On 2026-11-03 you ruled *"Every config that worked in the previous release keeps
   working, UNCHANGED, with the same meaning."* Her unchanged config meant "the agent is
   `_yolojail`"; under the first ruling as written, the same config would now mean "the agent is
   Dana", with a different Seatbelt profile and her Claude state read from
   `<ws>/.yolo/home/.claude` instead of the account's links. The two rulings cannot both be
   applied to her launch, so the default flip needs to know which wins.

   - **A — The 2026-10-22 ruling as made.** Dana's next launch prints the run-as-you disclosure
     and runs as her. Nothing to edit. Cost: a config that launched one way launches another, and
     `_yolojail`'s home keeps her old agent state with nothing pointing at it.
   - **B — The absent key follows the machine.** With no line, a Mac where the account exists
     (the sudo rule and `_yolojail` that `yolo macos-setup` installs) keeps running as the
     account; a Mac never set up runs as you. Dana's launch is unchanged; a first-time user gets
     run-as-you with no admin step, as the done-state says. One line at Dana's launch offers the
     switch: `yolo config set macos_user_mode self --user`. Cost: "the default" is a machine fact,
     and `yolo check` has to print which one this Mac is on.
   - **C — The absent key stays "account" for good; run-as-you stays opt-in.** Dana is unchanged,
     and so is every Mac: a first-time user still meets `yolo macos-setup`. Cost: *"Default mode:
     the agent runs as you"* is not delivered.

   <!-- vantage: question id=OQ-MM10 leaning="B — it is the only option under which both rulings hold: an unchanged config means what it meant, and a fresh Mac runs as you with no admin step." -->

   _Leaning:_ B — it is the only option under which both rulings hold: an unchanged config means
   what it meant, and a fresh Mac runs as you with no admin step.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| MM-R1 | The risk that an access grant on a home folder exposes `~/.ssh` is acceptable, not a blocker. | 2026-10-22 | [§4](#4-what-the-second-account-buys-and-what-it-costs) | — |
| <a id="OQ-MM1"></a>[OQ-MM1](#OQ-MM1) | macos-user moves to Safehouse's model: a deny-by-default profile, the agent running as you by default. The [§6](#6-what-a-prototype-must-show) prototype is the gate before building. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in the run-as-you mode, opt-in; the production default is unchanged |
| MM-R2 | The second account is kept from day one as an optional mode, off by default, running the same profile; it alone carries account setup and workspace access grants. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in the run-as-you mode, opt-in; the production default is unchanged |
| <a id="OQ-MM2"></a>[OQ-MM2](#OQ-MM2) | Start from Safehouse's layered profiles (Apache-2.0, credited), adapted for nix and narrowed where they read wider than yolo wants, such as all of `/opt`. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in the run-as-you mode, opt-in; the production default is unchanged |
| <a id="OQ-MM3"></a>[OQ-MM3](#OQ-MM3) | Deny loopback by default and allow yolo's own service ports, matching podman; a workspace config may allow more ports, through the config-change approval. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in the run-as-you mode, opt-in; the production default is unchanged |
| <a id="OQ-MM4"></a>[OQ-MM4](#OQ-MM4) | No transition for Macs already set up for the second account; reusing the account for the optional mode is welcome, not required. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | pending; whether "launches move to the default mode" survives the 2026-11-03 rule that an unchanged config keeps its meaning is [OQ-MM10](#OQ-MM10) |
| OQ-MCW4 | **The account mode's concurrency, carried from the retired concurrent-workspaces design.** Sharing remains limited to selected pack machine scope and existing configured shared paths. The exact backing and relative-link geometry are engineering design, not a new credential grant. | 2026-10-20 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in account mode by [slice 12a](macos-user-model-plan.md#MMP-D25) (2026-11-03): unit-tested on Linux, not yet run on a Mac |
| OQ-MCW2 | **Carried from the retired concurrent-workspaces design.** Protect Seatbelt-confined processes and their descendants against sibling HOME/state access; confinement is irreversible from inside. Host/admin control and trusted unconfined same-UID processes are outside the claim. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in account mode by [slice 12a](macos-user-model-plan.md#MMP-D25) (2026-11-03): unit-tested on Linux, not yet run on a Mac |
| OQ-MCW3 | **Carried from the retired concurrent-workspaces design.** Retain persistent workspace HOME/state by default, like container workspace state. No automatic deletion, expiry or cap; any future separately authorized explicit cleanup must refuse live or unknown state. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built in account mode by [slice 12a](macos-user-model-plan.md#MMP-D25) (2026-11-03): unit-tested on Linux, not yet run on a Mac |
| <a id="OQ-MM6"></a>[OQ-MM6](#OQ-MM6) | A `~/ctx` link to `$YOLO_CONTEXT_DIR` in the agent's home on every backend; `$YOLO_CONTEXT_DIR` stays the documented path. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | built: podman via the home skeleton (skipped when a config entry claims `ctx`); Apple Container and macos-user via the `link_context_dir` boot step (macos-user retargets its own link per workspace); macos-user is unit-tested only, not yet run on a Mac; Apple Container is untested |
| <a id="OQ-MM5"></a>[OQ-MM5](#OQ-MM5) | Containers do not also bind mounts at their original host path; `$YOLO_CONTEXT_DIR/<name>` is the one supported path, and macOS allows the original path only for the link. | 2026-10-22 | [§7](#7-the-ruled-model-two-modes-one-profile) | — |
| [OQ-MM7](#OQ-MM7) | macos-user allows all loopback, like podman's host network mode: Seatbelt filters by port, not by process, so a jail's own random-port servers cannot be told from host services, and there is no private loopback without a VM. Replaces [OQ-MM3](#OQ-MM3) on macOS. | 2026-10-23 | [§8](#8-open-questions) | pending |
| [OQ-MM8](#OQ-MM8) | Trial: the shared `/tmp` is denied by default in the deny-by-default profile, with a user-scope setting to allow it; the sandbox `TMPDIR`, the session's service files and the per-user temp folder stay allowed. Revisit if it proves painful. | 2026-10-23 | [§8](#8-open-questions) | pending |
| [OQ-MM9](#OQ-MM9) | Host software is visible to the agent, read-only and runnable: `/opt/homebrew`, `/usr/local` and `/Applications`, never a parent such as `/opt`; declared packages come first on `PATH`. | 2026-10-23 | [§8](#8-open-questions) | built in the run-as-you mode, opt-in; not yet run on a Mac |
