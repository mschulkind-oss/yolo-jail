---
title: "A host agent floor: yolo keeps its own agents installed on the host, so a launch needs no particular PATH"
date: 2026-09-25
status: accepted
tags: [host, provisioning, floor, program, npm, capture, mise, path, evergreen]
summary: "yolo host should run every agent a selected pack declares from any launcher, a Waybar widget included, without the user having arranged a PATH. The design is a host agent floor: the program binaries of the user-scope selected packs, installed with no prompt into a yolo-owned host prefix (selecting the pack is the consent), kept current the way the jail's launchers keep them, and exec'd from there by path, so the floor's copy runs even where the user installed their own. The prefix is on no PATH of the user's, and a launch appends its bin/ last to the agent's PATH. npm agents run on the prefix's own Node; the installer-recipe agents come from a yolo capture, which on a Mac is the macos-user capture act and on a Linux host with no container runtime a capture on the host confined by Landlock; a fork's program comes from the capture store's build of its pinned commit, which on a Mac the macos-user sandbox account builds for darwin under a sealed Seatbelt profile. yolo never runs mise install at the host. Every question here is ruled; what runs for a selected pack's program the floor cannot hold is OQ-HE11 in host-launch-environment.md. Built 2026-09-29 on Linux, and on 2026-10-05 for a Mac (HP-D2, and a plain fork's build by forked-programs-as-packs.md FP-D24) and for a Linux host with no container runtime (HP-D18); the Built column of the ledger says what each ruling's code is and what is not yet measured."
stage: DECIDED
next: "Dispatch macos-user.yml for TestMacosUserHostFloorMaterializesAFixtureInstallerCapture and TestMacosUserHostFloorIsTheHostUsersAlone, and packs.yml for TestHostFloorInstallsTheVendorsRelease, then on a Mac run `yolo host -- claude` and `yolo host -- agy` once from a terminal; on a Linux host with no container runtime run `yolo host -- claude`; on Linux and on a Mac, with a real pi and an npm extension, run `yolo host -- pi` twice inside an hour and see one refresh line"
---

# A host agent floor: yolo keeps its own agents installed on the host, so a launch needs no particular PATH

**Status:** 2026-09-25; every question ruled 2026-09-29, the last by [HP-DIR4](#HP-DIR4).
**Built 2026-09-29** (`internal/hostfloor`, wired in `internal/cli/hostfloor.go`), and on 2026-10-05
the Mac's installer agents ([HP-D2](#HP-D2)), a Linux host with no container runtime
([HP-D18](#HP-D18)) and a program's [pre-launch refresh](#defined-terms) at `yolo host --`
([HP-D19](#HP-D19)), whose hardware runs are still owed: the [Decision Ledger](#decision-ledger)'s Built column says what
each ruling's code is, [HP-D4](#HP-D4) to [HP-D9](#HP-D9) record the implementation's own
decisions, and [§8](#8-done-looks-like) says which rows are pinned by a test and which only a real
host can confirm. Evidence read against `7e529260`, and it cites symbols, never line numbers. Where
[§4](#4-when-provisioning-runs)'s prompt rows and consent paragraphs, or [§5](#5-mise-the-stale-shim-verdict),
disagree with the [Decision Ledger](#decision-ledger), the ledger wins: [HP-D3](#HP-D3) withdrew the
consent prompt, and [OQ-HP6](#OQ-HP6) ruled out the `mise install` that section proposes. The
stale-shim verdict [§5](#5-mise-the-stale-shim-verdict) starts from is withdrawn too, by
[OQ-HE3](../reference/host-agent-environment.md#oq-he3)'s answer under
[HE-DIR1](../reference/host-agent-environment.md#he-dir1).

> **In short.** What yolo can guarantee at the host is the set of agents it already knows about:
> the `program` binaries of the selected packs. Installing those into a directory yolo owns, and
> running them from there by path, makes the floor a fact about the machine rather than about
> whoever launched yolo. Everything else, the user's own tools included, stays the user's.

**Why it matters.** A Waybar-started `yolo host -- opencode` exited 127 because the only `opencode`
was a mise shim that an interactive rc file activates
([`host-launch-environment.md` §1.3](host-launch-environment.md#13-how-the-incident-happened)).
yolo's checks read the PATH the launcher handed it, by the maintainer's ruling
[HE-DIR1](../reference/host-agent-environment.md#he-dir1), so that failure follows the launcher. Something
still has to put the agent where a launch finds it whatever PATH the launch was handed, and today at
the host nothing yolo drives does
([`provisioner-sets.md` §1](provisioner-sets.md#1-the-verdict-and-five-principles)).

**The shape.** The [host agent floor](#defined-terms) (what must resolve) is installed into the
[host prefix](#defined-terms) (where it lives) by the jail's own launcher shape (how it stays
current). No install prompts: selecting the pack is the consent ([OQ-HP5](#OQ-HP5)).
[`yolo check`](#7-what-yolo-check-reports) reports the result.

**Cost.** A second copy of any agent a user already installed by hand, for good: the floor's copy is
the one `yolo host` runs ([HP-DIR4](#HP-DIR4)), and the user's copy stays where it is, untouched and
not run by `yolo host`. A new yolo-owned tree of executables on the host, which has to stay outside
every jail's reach. An installer-recipe agent has no floor entry on a machine that can capture it
neither in a jail nor on the host: for example a Mac before `yolo macos-setup`, or a Linux host with no
container runtime and no Landlock. [HP-D2](#HP-D2) names every Mac case and [HP-D18](#HP-D18) every
Linux one, a selected runtime that is not installed among them; what `yolo host` runs for it there is
[OQ-HE11](../reference/host-agent-environment.md#oq-he11).

**Start at [§3](#3-the-host-prefix).** The prefix's two rules, *only floor names in `bin/`* and
*never jail-reachable*, are what make running from it, and appending its `bin/` to the agent's PATH,
safe.

**Needs your ruling:** none here. [OQ-HE11](../reference/host-agent-environment.md#oq-he11), in
[`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs), asked what runs for a selected pack's
program the floor cannot hold, and was ruled (a) on 2026-09-29: it runs from the agent's PATH, and
the launch says why the floor has no copy. Ruled 2026-09-29: [OQ-HP1](#OQ-HP1) (on by default), [OQ-HP2](#OQ-HP2) (moot: on no user PATH), [OQ-HP3](#OQ-HP3) (captures, and a host capture on macOS), [OQ-HP4](#OQ-HP4) (the official Node tarball), [OQ-HP5](#OQ-HP5) (no consent prompt: the pack selection is the consent), [OQ-HP6](#OQ-HP6) (never provision the workspace runtime at the host), [OQ-HP7](#OQ-HP7) (the agent's children see the user's own environment), and [HP-DIR4](#HP-DIR4) (a selected pack's agent runs from the floor, from any launcher).

**Reads with:**
- [`host-tool-provisioning-plan.md`](host-tool-provisioning-plan.md): the implementation sketch.
  It is incomplete, and nobody builds from it.
- [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs): the launch PATH yolo's host checks
  read, which is the PATH yolo was started with plus `host_path`'s folders
  ([HE-DIR1](../reference/host-agent-environment.md#he-dir1)). The prefix is not part of it. This doc
  supplies the floor that doc execs a delivered agent from, and whose `bin/` it appends last to the
  agent's PATH ([HE-D1](host-launch-environment.md#he-d1)).
- [`provisioner-sets.md`](provisioner-sets.md): which provisioner wins, per environment. This doc
  adds one member to the host's set and doesn't rank it.
- [`program-delivery.md` §3.5](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03):
  agent versus project dependencies (P6), the split this design follows.
- [`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md#the-principle--an-agents-interpreter-belongs-to-the-pack):
  an agent's interpreter belongs to the pack, the rule that stops the prefix shadowing a project's
  `node`.

---

## Defined terms

- **Host agent floor** *(coined here)*: the set of binaries yolo guarantees resolve at the host
  notch whatever PATH its launcher held. It has one entry per `program` contribution of each pack
  the **user-scope** config selects. It is distinct from two existing uses of "floor": the macos-user
  **package floor**, which is everything the image bakes
  ([`../reference/macos-user-provisioning.md`](../reference/macos-user-provisioning.md)), and a program's **Node
  floor** (`node_floor`), which is a minimum interpreter version. The maintainer's phrase for it was
  *"a minimum floor that we're ensuring."*
- **Host prefix** *(coined here)*: the yolo-owned host directory the floor is installed into
  ([§3](#3-the-host-prefix)).
- **Delivers**: a selected pack *delivers* a program when the floor holds, or can provision, an
  entry for it on this machine. The word is the rulings'; its definition is in
  [the launch-PATH ruling](../reference/host-agent-environment.md#he-dir1), with the
  cases that leave a selected pack's program undelivered.
- **Floor entry disposition** *(coined here)*: what [`yolo check`](#7-what-yolo-check-reports) and a
  launch say about one entry. It is one of **provisioned** (in the prefix: the copy `yolo host`
  runs), **missing** (the floor can hold it and does not yet; the next `yolo host apply` or launch
  installs it, [HP-D3](#HP-D3)), or **no floor entry** (the floor cannot hold it here: it is
  configured out of the floor, handed to another provisioner by
  [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override, unpublished for this OS and architecture, or, by
  [HP-D7](#HP-D7), an installer agent this machine can neither materialize nor capture: no capture
  in the store, or only one recorded for a jail's home, and no way to capture one — on a Mac, the
  macos-user capture act cannot run here: yolo run as root, no `sandbox-exec`, no sandbox account
  (`yolo macos-setup`), or no terminal for its sudo ([HP-D2](#HP-D2)); on Linux, a runtime the user
  selected is not installed, or none is selected or on PATH and the kernel cannot confine a capture
  on the host ([HP-D18](#HP-D18)); or a capture that holds no runnable program and that a new
  capture would not change; or, by [FP-D16](forked-programs-as-packs.md#FP-D16), a fork's program
  with no usable pin or whose build cannot move out of its build home, and on a Mac a patched fork's,
  or a plain fork's the macos-user act cannot build here for one of the capture act's reasons
  ([FP-D24](forked-programs-as-packs.md#FP-D24)); or, by
  [HP-D15](#HP-D15), a program asking for a dynamic loader this machine lacks, as on NixOS without
  nix-ld or a musl system). A copy of the program the user installed
  elsewhere is not a disposition: under [HP-DIR4](#HP-DIR4) it never stands in for the floor's, and
  `yolo check` names it beside the row ([§7](#7-what-yolo-check-reports)). The earlier
  **present** and **stale shim** dispositions described such a copy and are withdrawn with it.
- **Deselected entry** *(coined here, 2026-09-29, in the build's review)*: a floor entry still in
  the prefix whose program the floor no longer keeps, because its pack is no longer selected or
  `host_floor` now leaves it out. It stays until the next `yolo host apply --assert` removes it
  ([§4](#4-when-provisioning-runs), "Deselection"), and `yolo host` never runs it
  ([HP-D11](#HP-D11)). It is not the "leftover" of [HP-D8](#HP-D8), which is an interrupted install's
  directory.
- **Launch PATH**, **notch**, **ambient environment**: as defined in
  [the launch-PATH ruling](../reference/host-agent-environment.md#he-dir1). That
  section also records the **composed host PATH**, which some ruled text here still names, as
  withdrawn by [HE-DIR1](../reference/host-agent-environment.md#he-dir1).
- **Agent dependency**, **project dependency**: as defined in
  [`program-delivery.md` §3.5](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03).
- **Pre-launch refresh** *(coined 2026-09-25 for the jail's launchers)*: a command a pack declares
  for its program, which the launcher runs with the installed program just before it starts it, to
  bring up to date what the program keeps beside itself rather than the program. Pi's
  `update --extensions`, which updates pi's extension packages, is the one shipped today; the
  design is [`pi-extension-lifecycle.md` §3.2](pi-extension-lifecycle.md#32-execution-tier-pre-launch-auto-refresh),
  and the host's run of it is [HP-D19](#HP-D19).

## 1. What exists today

All of this was read from the tree at `7e529260`.

- **The jail provisions its agents through lazy launchers.** `GenerateAgentLaunchers`
  (`internal/entrypoint/shims.go`) writes one launcher per `program` contribution into
  `~/.yolo/bin/launch`. On first use a launcher installs the program (npm into
  `$NPM_CONFIG_PREFIX`, a vendor installer into `~/.local/bin`). After that it refreshes an unpinned
  package at most once per `UPDATE_INTERVAL` (3600 s), which is the evergreen rule for agent
  dependencies. A program with a `node_floor` is exec'd under the resolved interpreter's absolute
  path, so the workspace's `node` never runs the agent
  ([the launcher](../reference/agent-program-runtimes.md#the-launcher)).
- **The same launchers already run natively on macOS.** macos-user calls `GenerateAgentLaunchers`
  from `internal/entrypoint/darwin.go` and runs the bodies in the guest account's home, not in a
  container. The launcher shape is therefore not tied to Linux. Whether its npm path works there
  end to end is recorded as UNVERIFIED
  ([agent-program-runtimes](../reference/agent-program-runtimes.md#macos-user-unverified)).
- **The host has no install location of its own.** `yolo host apply --assert`'s dependency gate
  (`internal/cli/applyhostdepgate.go`) offers the pack's remedy behind one prompt and runs it with
  `sh -c` in the inherited environment (`runDepInstallCommand`). So the program lands wherever that
  environment's npm prefix or the vendor's default puts it, which is a place a later launch's PATH
  may not name. [OQ-PS13](provisioner-sets.md#OQ-PS13) recorded that this run skipped the jail
  launcher's installer-body check; since 2026-09-30 an installer's remedy carries the same check
  ([PS-D4](provisioner-sets.md#PS-D4)) and runs with no terminal
  ([PS-D1](provisioner-sets.md#PS-D1)).
- **Seven shipped packs declare a program.** Four use npm recipes (`copilot`, `oh-omp`,
  `opencode`, `pi`). Three use vendor installer recipes (`agy`, `claude`, `codex`). `oh-omp` is
  version-pinned (`@0.15.3`), and `pi` declares `node_floor: "22.19"`.
- **Two stores already sit near the host, with opposite trust.** The jail's mise store
  (`paths.GlobalMise`) is mounted **read-write** at `/mise` in every jail
  (`internal/cli/run/assemble_parts.go`). The capture store (`paths.CapturesDir`) is mounted
  **`:ro`** at `/ctx/captures` (`capturesmount_test.go`).

## 2. The floor: what it contains, and what it doesn't

**In it:** every `program` contribution's `bin`, across the packs the user-scope config selects.
It reads user scope only, the same construction `composeHostVars` uses: a workspace config is
agent-editable and must never add a binary to the host.

**Not in it:**
- `requires` entries. A need with an empty recipe list
  ([`provisioner-sets.md` §8.4](provisioner-sets.md#84-the-ruling-a-pack-declares-a-need-and-its-recipes-the-environment-resolves))
  has nothing for yolo to install. It is reported as today.
- Project dependencies (`mise_tools`, `packages`). The user's own tools on their bashrc PATH are
  found on the launch PATH in
  [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs), not supplied by the floor.
- MCP servers the agents drive. They are agent dependencies too, but v1 covers the agent binaries
  only ([§9](#9-non-goals)).

**Degenerate cases:**
- No selected pack declares a program: the floor is empty, and nothing is installed or reported.
- Two packs declare the same `bin`: the pack system's existing combine rule for a `program`
  target decides which one counts. This doc adds no rule, and the floor holds one entry per name.
- A program whose vendor publishes no build for this OS and architecture: its entry is reported
  as **no floor entry, unpublished here**, using the same predicate the jail uses
  (`packdecl.Install.UnpublishedReason`, formerly the jail-only `launcherUnpublished`), and no
  install is ever attempted. What `yolo host` runs for it is
  [OQ-HE11](../reference/host-agent-environment.md#oq-he11).

## 3. The host prefix

**Where.** A directory under yolo's state dir (`~/.local/share/yolo-jail/`) that **no jail mounts,
in any mode**. The name is the implementer's; this doc says `host-tools/`. It is created mode
`0700`, so the macos-user guest account, which is another uid, can neither read nor replace
anything in it.

**Forbidden, by construction:**
- **Never under a segment a jail mounts.** That rules out `cache/`, `mise/` and every
  workspace's `.yolo/`. The host executes these files with the user's full authority, so a
  jail-writable copy would be the injection channel
  [`paths.EmbeddedPacksDir`](../../internal/paths/paths.go) already names for the embedded-pack
  tree. A test pins that no mount source the launcher emits is at or under the prefix.
- **Never inside the user's own install locations.** That means not `~/.local/bin`, not their npm
  prefix, not their mise data dir, and no dotfile edits. One writer: yolo.
- **Never on the user's interactive PATH, and not on the launch PATH either.** `yolo host` execs
  a floor entry by path, and appends the prefix's `bin/` last to the PATH it hands the agent, after
  the launch PATH: the caller's PATH, then `host_path`'s folders
  ([HE-D1](host-launch-environment.md#he-d1)). `yolo host env` still emits no PATH line.

**What `bin/` holds.** Exactly one executable per **provisioned** floor entry, and nothing else:
no `node`, no `npm`, no package's secondary binaries. This is the rule that makes appending the
prefix's `bin/` to the agent's PATH safe: it adds only agent names, and being last it supplies one
only where nothing earlier on that PATH has it. It can never put a `node` or `npm` ahead of the
project's. Each entry is a generated launcher of
the jail's shape: first-use install, throttled evergreen refresh, and the exec prefix for a
declared `node_floor`. An npm agent therefore runs under the prefix's interpreter by absolute path,
while `node` in its shell children is still the project's
([agent-program-runtimes](../reference/agent-program-runtimes.md#what-this-does-not-do)).

**Which recipes it carries:**

| Recipe | In the prefix? |
| :--- | :--- |
| `via: npm` | **Yes**, on Linux and macOS. It installs into a prefix-private npm prefix with a prefix-private interpreter ([OQ-HP4](#OQ-HP4)) |
| `via: installer` | **Yes**, by [OQ-HP3](#OQ-HP3)'s ruling: materialized from the jail's `yolo capture` where the host matches the capture jail, from the macos-user capture act on a Mac ([HP-D2](#HP-D2)), and on a Linux host with no container runtime from a capture on the host, its installer confined by Landlock ([HP-D18](#HP-D18)). A machine that can run none of them has **no floor entry** for it |
| `via: source` (a fork's build) | **Yes**, by [FP-D4](forked-programs-as-packs.md#FP-D4): the capture store's build of the fork's pinned commit, relocated into the prefix by the same confined materialize as an installer's capture. On Linux it is the entry a jail launch materializes, and a miss runs the fork's sealed build act, as a jail launch does. On a Mac a miss runs that act as the macos-user sandbox account, sealed, for darwin ([FP-D24](forked-programs-as-packs.md#FP-D24)). A Node script runs on the prefix's interpreter. A fork with no usable pin, a build that cannot move out of its build home, and a patched fork on a Mac are **no floor entry** ([FP-D16](forked-programs-as-packs.md#FP-D16)) |

**Install atomicity.** A program installs into a fresh versioned directory, and its `bin/` entry is
switched only once the install exits 0 and the entry binary exists. A failed or killed install
leaves the previous version serving, or no entry at all, never a half-written one. The next attempt
removes leftover staging directories whose lock holder is gone.

**What [OQ-HP3](#OQ-HP3)'s options left open, as filed.** For (a): the capture store is `:ro` in every jail
([§1](#1-what-exists-today)). Whether a captured binary runs on the host outside the image's
libraries is NOT MEASURED. For (b): it would re-open the
*"it's just not going to be a bash script"* ruling
([`provisioner-sets.md` §8.4](provisioner-sets.md#84-the-ruling-a-pack-declares-a-need-and-its-recipes-the-environment-resolves)),
and the vendor's own self-updater may still write under the real `$HOME` (NOT MEASURED).

## 4. When provisioning runs

| Trigger | Entry state | What happens |
| :--- | :--- | :--- |
| `yolo host apply`, at a TTY | any entry **missing** | One prompt lists every missing entry with its exact install command. Yes installs them in sequence. No leaves them missing and exits under `--assert`'s existing rule |
| `yolo host apply`, no TTY | **missing** | Prints the same list and installs nothing (the [§8.5](provisioner-sets.md#85-the-ruling-yolo-drives-the-winner-behind-the-confirm-and-last) degrade) |
| `yolo host -- <bin>`, at a TTY | target **missing** | The same prompt for that one entry, synchronously, before the exec |
| `yolo host -- <bin>`, no TTY | target **missing** | Exits 127 immediately with a line naming `yolo host apply`. **It never downloads on a non-interactive first install**: a Waybar widget can't answer a prompt, and a silent multi-minute install is worse than a named failure |
| any invocation of a **provisioned** entry | stale | The launcher's own throttled refresh, as in the jail: at most once per 3600 s, and a failure keeps the installed version and retries after the launcher's retry interval. No prompt; see [OQ-HP5](#OQ-HP5). An installer agent's poll is a capture: once the machine's newest capture of it is a day old, the refresh runs `yolo capture <bin>` and installs what it stored only when that release is newer ([HP-D16](#HP-D16)) |
| any `yolo host -- <bin>` of a program whose pack declares a [pre-launch refresh](#defined-terms) | any copy: the floor's, the launch PATH's, or a path given | The jail launcher's refresh, run against the copy about to start, before its model menu: at most once per 3600 s, sooner when a file it watches holds content no refresh has succeeded with, only where `agent_updates` lets the pack move, bounded at 60 s. A failure is a line and the program starts anyway ([HP-D19](#HP-D19)) |

**The prompt says the install runs on this machine.** A pack's footprint line for an installer
program says it runs *"INSIDE THE JAIL (not on your machine)"* (`internal/packload/footprint.go`).
That is true of the jail and false here, so the footprint is never the host consent.

**Consent is recorded per program.** The first yes records the program and its recipe source.
The throttled refresh of that same recipe needs no second prompt. A changed recipe (a different
package name or installer URL) prompts again. Whether one consent covers updates at all is
[OQ-HP5](#OQ-HP5).

**Concurrency.** One install per program at a time, serialized by a per-program lock in the
prefix. A second launch that finds the lock held waits for it and prints one line naming the
holder's pid. After the wait it re-checks the entry rather than installing again. Installs of
*different* programs don't wait on each other.

**Timeouts and offline.** A first install is bounded at 600 s. On timeout it is killed, its staging
directory is removed, and the launch exits non-zero with the last lines of the installer's stderr.
Offline, a first install fails with the network error it got. A refresh fails quietly into the
retry interval and never blocks the exec.

**State that already exists.** Users already have agents in `~/.local/bin`, their npm prefix, mise
or Homebrew.
- **Nothing is migrated or removed, and no existing copy stops the floor installing its own.** The
  floor provisions every program it can hold into the prefix, whatever copy is already on the
  machine, on the launch PATH or not, because the floor's copy is the one `yolo host` runs
  ([HP-DIR4](#HP-DIR4)). Naming the other copy's directory in `host_path`
  changes nothing for a floor entry.
- **The other copy is named, never hidden.** [`yolo check`](#7-what-yolo-check-reports) lists any
  copy of a floor program it finds on the launch PATH or at a
  [hint location](../reference/host-agent-environment.md#the-miss-line) as *not run by
  `yolo host`*, so the second copy is never a surprise.

**Deselection.** A prefix entry whose program no selected pack declares any more is removed by the
next `yolo host apply`, never at launch. The entry is removed contents-only. A running agent keeps
its open inode.

## 5. mise: the stale-shim verdict

[tool managers at the host](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs)
once gave a shim whose `mise which` fails its own verdict, with the remedy `mise install`; that
verdict is withdrawn ([OQ-HE3](../reference/host-agent-environment.md#oq-he3)), and yolo runs no mise command
at the host. The
maintainer's comment on that paragraph asks whether yolo should just run it, by default. The
behavior this doc proposes is [OQ-HP6](#OQ-HP6)'s leaning:

- **Scope:** `mise install <tool>` for the tool behind the one shim that got the verdict. Never a
  bare `mise install` of everything the directory selects.
- **Where:** in the invocation's cwd, for `yolo host`, with the composed environment. That is the
  same cwd and environment `mise which` was asked in, so the install answers the question that
  failed.
- **Gate: mise's own.** yolo never passes anything that bypasses mise's config trust (for example
  `MISE_TRUSTED_CONFIG_PATHS`, or a `--yes` to a trust prompt). If mise refuses an untrusted config,
  yolo prints mise's refusal and does not retry. A cloned repository's `mise.toml` must not become a
  way to run a plugin on the host without the user having trusted it once in mise itself.
- **Never** `mise env`, `mise activate` or `[env]` evaluation by yolo. Option E stays rejected.
- **No TTY:** print the remedy and install nothing. This is the same reason as [§4](#4-when-provisioning-runs).

This is a **project-dependency** act ([P6](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03):
*"on an explicit act only"*). A human typing `yolo host -- <bin>` at a terminal is that act, and a
widget is not.

## 6. The seams

- **[`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs).** That doc owns the launch PATH,
  and the prefix is not in it. A program a selected pack
  [delivers](../reference/host-agent-environment.md#he-dir1) is exec'd from its floor entry by
  path and checked by that entry, and the prefix's `bin/` is appended last to the PATH the agent is
  handed ([HE-D1](host-launch-environment.md#he-d1)). The floor disposition is computed from the
  prefix itself, never from a PATH lookup. [OQ-HE2](../reference/host-agent-environment.md#oq-he2) (a
  pack-declared install directory) is answered no by
  [HE-DIR1](../reference/host-agent-environment.md#he-dir1): no pack adds a folder to a launch.
- **[`provisioner-sets.md`](provisioner-sets.md).** That doc owns *which provisioner wins*. The
  prefix is one new member of the host's provisioner set, and its rank in the default order is
  [OQ-PS6](provisioner-sets.md#OQ-PS6)'s question, where this doc proposes first for agent
  dependencies. A user who prefers `claude` from Homebrew says so through that doc's override
  ([OQ-PS7](provisioner-sets.md#OQ-PS7)), and the program then has **no floor entry**. What
  `yolo host -- claude` runs in that case is [OQ-HE11](../reference/host-agent-environment.md#oq-he11), whose
  options include the override choosing which provisioner fills the floor's entry instead of
  removing it. The floor needs no consent point of its own ([OQ-HP5](#OQ-HP5)). An installer run
  through the dependency gate inherits [OQ-PS13](provisioner-sets.md#OQ-PS13)'s body check.
- **The jail.** Nothing changes. The prefix is host-only, and P2's *"a notch's resolution does not
  propagate"* holds both ways.

## 7. What `yolo check` reports

The host-launch section
([the one resolver](../reference/host-agent-environment.md#one-resolver)) gains one
row per floor entry. The row gives its disposition, the entry's path in the prefix and the version
and date of the last install for a provisioned entry, and the remedy or reason for anything else:
`yolo host apply` for missing (a launch installs it too, [HP-D3](#HP-D3)), and the reason for no
floor entry, with what `yolo host` runs instead ([OQ-HE11](../reference/host-agent-environment.md#oq-he11)).
Beside any disposition, the row names each other copy of the program it finds on the launch PATH
or at a [hint location](../reference/host-agent-environment.md#the-miss-line), as *not run
by `yolo host`* ([§4](#4-when-provisioning-runs)). It also reports a lock held by a pid that no
longer exists, and staging leftovers. `yolo check` installs nothing.

## 8. Done looks like

Each row says what pins it. The unit pins run on a fake Node distribution and a fake npm registry
served by the test itself (`internal/hostfloor/floortest`), and a fake capture store. Since
2026-10-05 four integration tests run the floor on the machine the suite runs on, with the real
bytes; none starts an agent beyond `--version`:

- `TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode`, on every push (`ci.yml` on Linux x64 and
  arm64, and the macOS nightly on darwin-x64). A first `yolo host -- cowsay`, from
  `PATH=/usr/bin:/bin`, fetches the shipped Node tarball from nodejs.org, checks it against the
  digest compiled into yolo, and installs `cowsay@1.6.0` with that release's npm: the pinned
  package the jail's own npm install test uses (`TestPinnedNpmProgramInstallsTheDeclaredVersion`). The test reads the installed version from npm's own `package.json`, checks the
  prefix's `0700`, runs the floor's launcher with an empty environment, and checks that the
  launch's stdout is the program's output alone. A second launch then installs and polls nothing,
  with the entry's last check first moved past the hourly update interval, so the pinned version,
  not the interval, is what keeps it off the registry. `yolo host apply --assert` keeps the entry.
- `TestHostFloorRunsARealCaptureOfAnInstallerFixture`, on every push. With no capture on the
  machine, a first `yolo host -- <bin>` runs a real `yolo capture` of a hermetic installer fixture
  in a capture jail. It materializes the entry into the floor, relocates the installer's absolute
  `/home/agent` link into it, and runs the floor's copy. The launch's stdout carries the floor
  copy's output alone: the capture jail's own output goes to this process's stderr, never into the
  host launch log, which takes yolo's own lines. A second launch, from `PATH=/usr/bin:/bin`,
  captures nothing. On a Mac with no sandbox account (the macOS nightly's runners), it checks that
  the launch names `yolo macos-setup` instead.
- `TestHostFloorInstallsTheVendorsRelease`, on Pack Installs' triggers (`packs.yml`, Linux x64
  and arm64), with one subtest per shipped program. It installs the vendor's current release into
  the floor and runs it with `--version`: npm packs on the floor's Node, installer packs from a
  capture.
- `TestMacosUserHostFloorIsTheHostUsersAlone`, Mac-only (`macos-user.yml`, darwin-arm64). It makes
  the first test's install on a Mac, then checks that the sandbox account cannot reach the prefix
  (the `yolo check` row below).

What only a real host can still confirm is said per row.

- After one `yolo host apply` at a terminal, `yolo host -- opencode` started from Waybar, with
  mise unactivated and a minimal PATH, execs the prefix's `opencode`.
  **Built.** `TestHostLaunchRunsTheFloorsCopyOfASelectedPacksAgent` (the first launch installs, a
  second from `PATH=/usr/bin:/bin` execs the same floor copy with no second install) and
  `TestAnNpmProgramInstallsOnTheFloorsOwnNodeAndStartsWithNoPATH` (the floor's launcher runs with
  an empty environment). **On a real host:** the official Node tarball and its npm are pinned by
  `TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode` (Linux, and darwin-x64) and
  `TestMacosUserHostFloorIsTheHostUsersAlone` (darwin-arm64). The real opencode-ai package is
  pinned by `TestHostFloorInstallsTheVendorsRelease/opencode` (Linux). Both were MEASURED by hand
  on 2026-10-05 in this development jail with a scratch `HOME`: Node v24.21.0's linux-x64 tarball
  verified against the compiled-in digest, `cowsay@1.6.0` installed and run from
  `PATH=/usr/bin:/bin` and with an empty environment, and opencode-ai 1.18.34 installed and run
  with `--version`. The floor's npm warned that opencode-ai's postinstall script is not yet covered
  by npm's `allowScripts` setting, so the Pack Installs cell is where a future npm that enforces the
  setting shows up first. **Still needs a real host:** a vendor's npm agent on a Mac's floor, which
  waits on [OQ-CI7](../reference/agent-install-in-ci.md#oq-ci7), and a real launcher such as a
  Waybar widget, in place of the tests' minimal PATH.
- With a hand-installed `claude` in `~/.local/bin` on the caller's PATH, `yolo host -- claude`
  still execs the prefix's copy, and `yolo check` names the hand-installed one as not run by
  `yolo host`.
  **Built** for the rule, pinned with an npm fixture program and a hand-installed stub
  (`TestHostLaunchRunsTheFloorsCopyOfASelectedPacksAgent`,
  `TestCheckReportsAProvisionedEntryItsOtherCopiesAndALeftover`), and for claude's own recipe with
  a fake capture (`TestAnInstallerProgramIsTheMachinesCaptureMaterialized`). A host without the
  loader the captured binary asks for (the 2.1.267 binary in this machine's store asks for
  `/lib64/ld-linux-x86-64.so.2`, which a NixOS host without nix-ld lacks) has no floor entry for
  it, and `yolo host -- claude` runs the PATH copy, naming the nix-ld step ([HP-D15](#HP-D15),
  `elfinterp_test.go`, `TestHostLaunchOnAMachineWithoutTheFloorsLoaderRunsThePATHCopyAndNamesTheStep`).
  A captured claude running from the floor outside the jail's `/lib` farm, on a glibc host, is
  pinned by `TestHostFloorInstallsTheVendorsRelease/claude` (Pack Installs, Ubuntu).
  **Needs a real Linux host:** that NixOS's stub-ld and then nix-ld give the two answers, and
  where claude's own self-updater writes when started from the floor with the real `$HOME` (NOT
  MEASURED). On a Mac
  claude is the macos-user capture act's entry ([HP-D2](#HP-D2):
  `TestAnInstallerProgramOnAMacIsItsCaptureMaterialized`, `TestAMacsFloorCapturesThroughTheMacosUserAct`),
  and before `yolo macos-setup` `yolo host -- claude` runs the PATH copy with one line naming that
  step. **Needs a Mac:** `TestMacosUserHostFloorMaterializesAFixtureInstallerCapture`, then a real
  claude.
- On Linux, `yolo host -- codex` runs the floor's codex, materialized from the machine's capture,
  and a store that holds only a capture recorded before codex's payload was a capture surface is
  captured again first, once ([HP-D17](#HP-D17)).
  **Built** 2026-10-05: `TestACodexCaptureIsTheFloorsCodex` runs the real capture driver over
  codex's standalone layout, `TestAStaleCodexCaptureIsRecapturedBeforeItsProgramIsJudged` and
  `TestACaptureRecordedBeforeTheSurfaceItsProgramIsInIsRecapturedOnce` the two stale shapes.
  A real capture of codex, materialized into the floor and run with `--version`, is pinned by
  `TestHostFloorInstallsTheVendorsRelease/codex` (Pack Installs, Linux). **Needs a real Linux
  host:** `yolo check` listing codex provisioned.
- An installer agent stays current: once its newest capture is a day old, the refresh captures it
  again (a failed capture is retried after the hourly interval) and installs the newer release it
  stores, never an older one ([HP-D16](#HP-D16)).
  **Built** 2026-10-05: `evergreen_test.go`, and at the production wiring
  `TestTheProductionFloorsRefreshRecapturesAnInstallerAgentThroughTheCaptureAct`. **Needs a real
  Linux host:** a backdated claude capture recaptured and updated by a real `yolo host -- claude`.
- A program's add-ons stay current at the host as in a jail: `yolo host -- pi` runs pi's
  [pre-launch refresh](#defined-terms), `update --extensions`, before it starts pi, at most once an
  hour and sooner when `~/.pi/agent/settings.json` changes, for whichever copy it runs, and
  `agent_updates` turns it off ([HP-D19](#HP-D19)).
  **Built** 2026-10-05: `prelaunch_test.go` (the stamp, the watched content, the bound, the lock and
  its one bounded wait, Ctrl-C and SIGTERM, a refresh that handles either), and at the call site
  `hostprelaunchrefresh_test.go` (once and before the exec, on stderr and never stdout, the
  interval, a settings change, `agent_updates`, a failure, a hang, a held lock, no credential
  composed for pi, what every process receives, the child's PATH before the blocked tools, the
  floor's own copy, a jail, SIGTERM handled or not, and Ctrl-C). **Needs a real host:** a real pi with an npm extension, on
  Linux and on a Mac: the refresh line at most hourly, pi's "Package Updates Available" box gone
  after it, and `agent_updates` stopping it.
- On a Linux host with no container runtime, the first `yolo host -- agy` captures agy on the host,
  its installer confined by Landlock, and runs the floor's copy; no jail ever selects that capture.
  **Built** ([HP-D18](#HP-D18)): `TestAHostCaptureConfinesItsInstallerAndTheFloorRunsWhatItLeft` runs a
  fixture installer through the real confinement on a Landlock kernel and checks that its stray write
  and read into the real home are refused, that it reaches neither the user's runtime directory, the
  account's home, a token nor the caller's terminal session, that the CA bundle it was granted is
  readable, and that nothing it left running outlives the capture;
  `TestTheHostCapturesConfinementHoldsOnThisKernel` measures each right, `/tmp` and `/dev/pts`
  included, and `TestAJailNeverSelectsAHostCapture` the origin. **MEASURED** 2026-10-05 with the real
  agy and claude installers in this development jail. **Needs a real host:** the same for claude on a
  Linux host without podman.
- ~~The same launch with the entry never provisioned exits 127 within a second, naming
  `yolo host apply`, and downloads nothing.~~ Withdrawn by [HP-D3](#HP-D3): the launch installs it.
  What is built instead: a first install that fails exits 127 with the installer's last lines and
  execs nothing (`TestHostLaunchThatCannotInstallItsAgentRefusesAndExecsNothing`), and a killed or
  timed-out install leaves no entry (`TestAKilledInstallTimesOutAndLeavesNothing`).
- Inside a host-launched npm agent, `node --version` in its shell prints the project's pinned
  version, while the agent itself ran under its `node_floor` interpreter.
  **Built** as its two halves: the agent is started by the floor's Node's absolute path
  (`TestAnNpmProgramInstallsOnTheFloorsOwnNodeAndStartsWithNoPATH`), and the child's PATH is the
  caller's first with only agent names from the floor last
  (`TestHostLaunchRunsTheFloorsCopyOfASelectedPacksAgent`). **Needs a real host:** the sentence
  itself, run in a real agent's shell, which no automated test may start.
- Two first launches of one program started together produce one install and one receipt.
  **Built.** `TestTwoFirstLaunchesTogetherProduceOneInstallAndOneReceipt`.
- A selected fork's program runs from the prefix as the build of its pinned commit: pinned by the
  first `yolo host -- <bin>` when nothing has pinned it yet, with no `yolo pack install`
  ([FP-D18](forked-programs-as-packs.md#FP-D18), 2026-10-02:
  `TestHostLaunchOfAnUnpinnedForkPinsItBuildsItAndRunsTheFloorsCopy`), built once in the sealed
  jail on the first `yolo host -- <bin>` unless a jail launch already built that commit, relocated
  out of the jail's home, and built again only for a moved pin or an edited recipe.
  **Built** 2026-10-01 ([FP-D16](forked-programs-as-packs.md#FP-D16)):
  `TestHostLaunchOfAPinnedForkBuildsItAndRunsTheFloorsCopy` drives a real local git repository
  through the pin, the build act, a launch that builds, one that does not, and a moved pin, with
  the build jail stood in for;
  `TestHostLaunchRunsTheBuildAJailLaunchMadeOnAMachineThatCannotBuild` runs the build a jail
  launch's own build call made, with no second build;
  `TestHostApplyProvisionsAPinnedForkAndRemovesItWhenThePinGoes` and the floor's own
  `built_test.go` cover the rest. On a Mac ([FP-D24](forked-programs-as-packs.md#FP-D24), 2026-10-05)
  the build is the macos-user act's, for darwin: `TestAMacHostLaunchOfAForkBuildsItAsTheSandboxAccount`
  drives the same chain with that act stood in for, and `TestMacosUserHostFloorBuildsAForkFixtureForTheMac`
  runs it on a Mac (`macos-user.yml`, no green run recorded). **Needs a real host:** a real fork's
  build, made in a real sealed jail or as the sandbox account, running from the prefix. The 2026-10-01 stand-in
  ([the plan's run](forked-programs-as-packs-plan.md#steps-1-to-3-on-a-stand-in-fork-2026-10-01))
  measured its build relocatable; nothing has run it outside the jail.
- `yolo check` lists every floor entry with a disposition. Every file yolo wrote is under the
  prefix, and no jail's mount list reaches it.
  **Built.** The section's tests (`section_hostfloor_test.go`), `TestEverySectionIsWired`, and
  `TestAssembleNeverMountsTheHostFloor` for the podman and Apple Container argvs. The macos-user
  guest, another uid, is kept out by the prefix's `0700` mode.
  `TestMacosUserHostFloorIsTheHostUsersAlone` (Mac-only, `macos-user.yml`) checks the mode alone:
  it makes a floor under a path the sandbox account can traverse down to the prefix's parent. It then checks that the account can
  list that parent, but can neither list the prefix nor read its launcher, and that a probe inside
  a macos-user launch cannot list the prefix either. A real floor, under `/Users/<you>`, has two
  more layers in front of that mode. The launch's Seatbelt profile denies reads under `/Users`
  outside the workspace and the account's own home (`users-read-deny` in
  `internal/macosuser/seatbelt.go`). And `yolo macos-setup` takes the account out of `staff`
  (`macosuser.CreateUserCommands`), the group a macOS home belongs to. The test logs the mode of
  the machine's own home and relies on neither layer.

**Pinned since the build's review** (2026-09-29). The review found each of these call sites could
be removed with the suite green; each test below fails with its line removed.

- The launch-owned-services path (`yolo host -p codex -- claude`) runs the floor's copy, with the
  caller's PATH and then the floor's `bin/`, and its hand-over line names the floor's copy:
  `TestHostServicesLaunchRunsTheFloorsCopyWithTheFloorsPath`.
- The launch gate's two applies, unattended and on a terminal, install nothing into the floor and
  remove nothing: `TestTheLaunchGatesApplyNeverTouchesTheFloor`, now driven through
  `hostApplyGateApply` and `hostApplyGateApplyInteractive`.
- The production floor's own inputs: the capture act (`hostFloorCaptureAct`), the highest
  `node_floor`, `agent_updates`, and the four in-jail guards (`hostfloorwiring_test.go`).
- A deselected entry is never run ([HP-D11](#HP-D11)), a caller with no PATH gets the baseline
  ([HP-D12](#HP-D12)), the refresh says it is polling before it polls ([HP-D13](#HP-D13)), the
  no-copy line's Mac wording (an installer agent before `yolo macos-setup`, naming that step) is
  checked on every OS from the floor's own platform (`TestTheNoCopyLineNamesTheFloorsPlatform`), and
  the JSON document carries the floor
  stage ([HP-D14](#HP-D14)).
- A manifest cannot place anything outside the floor ([HP-D10](#HP-D10)): `confine_test.go` in
  `internal/capture` and in `internal/hostfloor`, and the launcher runs only its exec line
  ([HP-D4](#HP-D4)). The case-insensitive aliasing test runs only where the temp directory's
  filesystem folds case, which is the macOS CI runner and not Linux.

A real capture of claude passing the confined materialize's checks on a Linux host and then
running from the floor is pinned by `TestHostFloorInstallsTheVendorsRelease/claude`. The checks
refuse nothing a capture's own walk writes; the unit tests show that for the fixture trees they
build, and `TestHostFloorRunsARealCaptureOfAnInstallerFixture` for a tree a real capture jail
recorded. **Needs a real host**, beyond the rows above: a caller with no PATH at all is a real
launcher's case (`env -i yolo host -- <bin>`), which the unit test reproduces only by unsetting PATH
in-process. `TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode` runs the floor's launcher with an
empty environment, not `yolo host`.

## 9. Non-goals

- **Installing project tools.** `mise_tools` and `packages` at the host are the user's, except
  for [§5](#5-mise-the-stale-shim-verdict)'s one-tool remedy.
- **MCP and LSP servers.** They are agent dependencies the floor could cover later. v1 covers the
  program binaries.
- **Putting the prefix on the user's own shell PATH.** A bare `claude` typed in a terminal
  reaches the prefix only through the host wrappers (`host_wrappers`), which exec `yolo host`.
- **Choosing between provisioners.** That is [`provisioner-sets.md`](provisioner-sets.md)'s.
- **Installing nix, mise or a system package manager** for a user who lacks one.

## 10. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| Keep driving the user's own npm prefix and `~/.local/bin` (today's gate) | **No.** Where the install lands depends on the ambient environment, and it writes into user-owned locations |
| Execute from the jail's mise store (`paths.GlobalMise`) | **No.** It is mounted read-write in every jail, so the host would run binaries any jail can rewrite |
| Put the whole prefix (node, npm, secondary bins) on PATH | **No.** It shadows the project's `node` for every child process, the defect [agent-program-runtimes](../reference/agent-program-runtimes.md) exists to prevent |
| Install silently on a non-TTY first launch | **No.** A background widget would block for minutes on a download nobody consented to |
| Require the user to arrange PATH (`host_path` only) | **No.** That is today's burden, which the maintainer's comment asks yolo to carry for what it knows |

## Open Questions

1. ✅ <a id="OQ-HP1"></a>**[OQ-HP1](#OQ-HP1): Is the floor on by default?**
   **Stakes:** whether a bare `"packs": ["claude"]` host user gets offered installs they never
   asked for.

   - **(a)** On by default: every launch checks, and installs happen only on consent
     ([§4](#4-when-provisioning-runs)).
   - **(b)** Opt-in through a user-scope key.
   - **(c)** Off, with
     the check reported by `yolo check` only.

   _Leaning:_ **(a).** The check is cheap, the install is always consented to, and a floor
   nobody turns on guarantees nothing. [`OQ-HE0`](../reference/host-agent-environment.md#oq-he0) isn't
   engaged, because the prefix is machine state yolo owns rather than an ambient input.

   <!-- vantage: question id=OQ-HP1 -->

      **Answer:**
      > **Ruled 2026-09-29: (a), on by default.** The maintainer: *"this floor should be on by
      > default for the host agent stuff because you're going to already have to have opted into
      > host management … Obviously, let it be configured. You could even configure a floor of
      > nothing … By default, if you include the claude pack, it should also have the floor include
      > claude on the host."* The floor is the programs of the user-scope selected packs (selecting
      > the claude pack puts claude in it); it is configurable, down to an empty floor; installs
      > still need consent. Host wrappers stay optional: they are sugar for `yolo host`.

2. ✅ <a id="OQ-HP2"></a>**[OQ-HP2](#OQ-HP2): Where does the prefix sit on the composed host PATH?**
   **Stakes:** whether yolo's copy or the user's declared copy wins when both exist.

   - **(a)** First, ahead of `host_path`.
   - **(b)** After `host_path` and before the baseline, as a fallback.
   - **(c)** Last.

   _Leaning:_ **(a).** `bin/` holds only floor names ([§3](#3-the-host-prefix)), and only ones
   nothing on the composed PATH provided at install time. A user who wants their own copy says
   so through [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override, and then the prefix never holds
   that name. It also matches the jail, where the launch dir precedes every install prefix.

   <!-- vantage: question id=OQ-HP2 -->

      **Answer:**
      > **Ruled 2026-09-29: moot — the prefix is on no PATH of the user's.** The maintainer: *"It
      > sits nowhere, right? … This is only going to work with the wrappers or with yolo host. So I
      > don't think this should change the user's environment by default other than through those
      > wrappers … if you launch the agent not through the wrapper, we don't care if it's broken."*
      > yolo host (and the wrappers, which are `yolo host`) runs the floor's program directly; the
      > user's shell PATH is never changed. Inside a yolo host launch the composed PATH handed to
      > the agent carries the prefix first, so an agent that starts another agent gets the floor's
      > copy ([HP-D1](#HP-D1)); that is within the launch yolo owns, not the user's environment.
      >
      > **Superseded in part (recorder's note, 2026-09-29):** the last sentence's "prefix first" is
      > the recorder's gloss, not the maintainer's words, and [HP-DIR2](#HP-DIR2) replaced it. By
      > [HE-D1](host-launch-environment.md#he-d1) the prefix's `bin/` is last in the PATH the agent
      > is handed, after the launch PATH (the caller's PATH, then `host_path`'s folders), so an
      > agent that starts another agent by bare name gets the floor's copy only where nothing
      > earlier on that PATH has one.

3. ✅ <a id="OQ-HP3"></a>**[OQ-HP3](#OQ-HP3): How do the installer-recipe agents (`claude`, `codex`, `agy`) reach the prefix?**
   **Stakes:** the three most-used agents. What (a) and (b) each left unmeasured, or would
   re-open, is in [§3](#3-the-host-prefix).

   - **(a)** Materialize a `yolo capture` of the
     installer into the prefix, where the host's OS and architecture match the capture jail's
     (Linux), and leave them hint-only on macOS.
   - **(b)** Run the vendor script under a prefix-private `$HOME`, the
     way macos-user runs it in the guest's home. That covers macOS.
   - **(c)** Keep installer programs out of the floor, reported as **missing** with the pack's
     hint.

   _Leaning:_ **(a), with (c) on macOS**, pending a measurement that a captured `claude` runs on
   a Linux host. It is the only option that keeps the standing ruling.

   <!-- vantage: question id=OQ-HP3 -->

      **Answer:**
      > **Ruled 2026-09-29: (a), and on macOS a host capture instead of hint-only.** The maintainer:
      > *"I want to reuse the yolo capture. Like, that would be great if we just have one package
      > inside and outside the jail that is captured in this way. Otherwise, can we do like a host
      > capture … if it's not possible, like on a Mac, can we just do it again in a way that is
      > possible? I want it to be part of the floor, for sure."* Installer agents (claude, codex,
      > agy) are in the floor. Where the host matches the capture jail (Linux, same arch) the floor
      > materializes the same `yolo capture` the jails use, one package inside and outside. Where it
      > cannot (macOS), yolo runs an equivalent **host capture**: the vendor installer in a
      > throwaway, confined environment with a private `$HOME`, its result stored in the same
      > read-only capture-store shape and then materialized into the prefix ([HP-D2](#HP-D2)). Both
      > need their measurement before they ship: a captured binary running on the host.

4. ✅ <a id="OQ-HP4"></a>**[OQ-HP4](#OQ-HP4): Where does the prefix's Node come from?**
   **Stakes:** the four npm agents can't install or run without an interpreter, and the floor
   may not assume one on the ambient PATH.

   - **(a)** The official Node tarball for the platform,
     verified against the release's published checksums, at a version yolo ships. It is raised to
     the highest `node_floor` a selected pack declares.
   - **(b)** The user's nix when present
     ([OQ-PS1](provisioner-sets.md#OQ-PS1)).
   - **(c)** The user's mise when present.

   _Leaning:_ **(a)**, with (b) and (c) left to [OQ-PS6](provisioner-sets.md#OQ-PS6)'s ranking
   later. It is the only source present on every host, and it lives entirely inside the prefix.

   <!-- vantage: question id=OQ-HP4 -->

      **Answer:**
      > **Ruled 2026-09-29, as leaned: (a).** The official Node tarball, checksum-verified, at a
      > version yolo ships raised to the highest selected `node_floor`. The maintainer: *"Using the
      > official node tarball sounds good."*

5. ✅ <a id="OQ-HP5"></a>**[OQ-HP5](#OQ-HP5): Does one consent cover the evergreen refresh?**
   **Stakes:** [`provisioner-sets.md` §8.5](provisioner-sets.md#85-the-ruling-yolo-drives-the-winner-behind-the-confirm-and-last)
   says driving is *"a per-launch offer, never a background action."* It was ruled about
   system-manager commands. **(a)** Yes: the first consent covers the throttled refresh of the
   same recipe, as the jail's launchers refresh with no prompt. **(b)** No: every version change
   prompts, and a non-TTY launch never updates.

   _Leaning:_ **(a).** An agent dependency *"wants to be current"*
   ([P6](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)).
   The refresh runs at the agent's own invocation rather than in the background, and a changed
   recipe prompts again ([§4](#4-when-provisioning-runs)).

   <!-- vantage: question id=OQ-HP5 -->

      **Answer:**
      > **Ruled 2026-09-29, past both options: no consent prompt at all.** The maintainer: *"why is
      > yolo even asking if you want to install this? If pi is going to be available, it's because
      > you've already configured that pack in your config file. You've acknowledged this already.
      > We don't have to ask again … certainly not the second time."* Selecting the pack in user
      > config IS the consent. The floor installs and keeps current what the selected packs declare,
      > the same way a jail's launchers do (throttled, at the agent's own invocation); no install or
      > update prompt. This also retires the consent clause of [OQ-HP1](#OQ-HP1)'s answer and [§4](#4-when-provisioning-runs)'s
      > consent design ([HP-D3](#HP-D3)).

6. ✅ <a id="OQ-HP6"></a>**[OQ-HP6](#OQ-HP6): Does yolo run `mise install` on the stale-shim verdict?**
   **Stakes:** the maintainer's *"by default when you run on the host we should probably run
   the mise install."*

   - **(a)** Yes, at a TTY, as [§5](#5-mise-the-stale-shim-verdict) scopes it:
     one tool, in the invocation's cwd, printed before it runs, with mise's own trust as the gate.
   - **(b)** Yes, including non-TTY launches.
   - **(c)** No: print the remedy, as
     [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs)
     has it.

   _Leaning:_ **(a).** It is what the user's own `mise.toml` asks for. mise's trust prompt
   already guards a cloned repository's config. A widget can't consent to a download.

   <!-- vantage: question id=OQ-HP6 -->

      **Answer:**
      > **Ruled 2026-09-29: never** ([HP-DIR3](#HP-DIR3)). The maintainer: *"when you're inside of
      > the jail, you can't really truly manage all the things in the environment … it's the safer
      > contained thing. So, that's okay to do there. But on the host, the host agent could run the
      > mise whatever it needs to once it starts up … we don't maintain the host development
      > environment, the workspace's runtime … and extend that ruling to whatever other questions it
      > applies to."* At the host yolo does not provision the workspace's runtime: no `mise
      > install`, no `mise env` or activation, no direnv. A missing project tool behaves as when the
      > user types the command, and the host agent can run `mise install` itself if the work needs
      > it.

7. ✅ <a id="OQ-HP7"></a>**[OQ-HP7](#OQ-HP7): When a delivered agent runs project commands, which environment do they see?**
   Raised in review 2026-09-29 by [HP-DIR2](#HP-DIR2): a delivered agent starts in a set
   environment (the floor's node by absolute path, mise stripped). **(a)** Only the agent's own
   startup is fixed; what it runs sees the user's own shell environment. **(b)** The set
   environment all the way down, so `npm test` runs on the floor's node.

   <!-- vantage: question id=OQ-HP7 -->

   **Answer:**
   > **Ruled 2026-09-29: (a)** ([HP-DIR3](#HP-DIR3)). Only a delivered agent's own startup runs in
   > the set environment; the commands the agent runs see the user's own shell environment, mise
   > included, exactly as the user would run them.

## Decision Ledger

| ID | Ruling | Date | Built |
| :--- | :--- | :--- | :--- |
| [OQ-HP1](#OQ-HP1) | **Maintainer ruling:** the floor is on by default: the programs of the user-scope selected packs, configurable down to empty, installed with consent | 2026-09-29 | **Built.** On by default; configurable down to empty by the user-scope `host_floor` key ([HP-D5](#HP-D5)). The consent clause is withdrawn by HP-D3 |
| [OQ-HP2](#OQ-HP2) | **Maintainer ruling:** moot; the prefix is on no user PATH, and only `yolo host` and the wrappers use it | 2026-09-29 | **Built.** The prefix is `~/.local/share/yolo-jail/host-floor` ([HP-D8](#HP-D8)), on no PATH of the user's; `yolo host env` emits no PATH line |
| <a id="HP-D1"></a>HP-D1 | *Implementation decision under HP2:* a yolo host launch execs the floor's program by path and puts the prefix first on the composed PATH it hands the agent, so a child agent resolves the floor's copy; the user's shell is untouched. Its "prefix first" is superseded by [HP-DIR2](#HP-DIR2) and [HE-D1](host-launch-environment.md#he-d1) (the prefix last); the exec by path stands | 2026-09-29 | **Built** for the exec by path: `yolo host` execs `bin/<bin>`, which starts the program by absolute path ([HP-D4](#HP-D4)) |
| [OQ-HP3](#OQ-HP3) | **Maintainer ruling:** installer agents are in the floor, from the jail's `yolo capture` where the host matches, and from a host capture where it does not (macOS) | 2026-09-29 | **Built on Linux** ([HP-D7](#HP-D7)): the capture store's entry, relocated into the prefix, and on a host with no container runtime a capture on the host ([HP-D18](#HP-D18)). **Built for a Mac** 2026-10-05 ([HP-D2](#HP-D2)) |
| <a id="HP-D2"></a>HP-D2 | *Implementation decision under HP3:* the host capture runs the vendor installer confined (on macOS, Seatbelt with a throwaway `$HOME`, the capture jail's recipe, no network beyond the vendor's), and writes the capture store's existing shape, so the floor has one materialization path for both; it is measured on a Mac before it ships. **Amended 2026-10-05**, *an implementation decision taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible:* the Mac's host capture **is the existing macos-user capture act** (`macosuser.RunCaptureAct`: Seatbelt's capture profile, a throwaway `$HOME` under `/Users/Shared/yolo-captures`, run as the sandbox account), whose recording half was measured on hardware on 2026-09-11, so no separate measurement round precedes it. The floor runs it as `yolo capture <bin>` on the macos-user runtime, handed to that one act and never set in the environment the agent is exec'd with: a container capture on a Mac records a Linux entry, which this floor cannot run. Its reasons for no floor entry are the act's own refusals, asked before it starts, each with its step: yolo run as root; `sandbox-exec` not on PATH; no sandbox account (`yolo macos-setup`); and no terminal for sudo with `sudo -n true` failing (`YOLO_RUNTIME=macos-user yolo capture <bin>` once in a terminal). A Mac's floor given no capture act holds no installer agent | 2026-09-29 | **Built** 2026-10-05: `noEntryReason` (`internal/hostfloor/floor.go`), `wireFloorCapture` and `macCaptureBlocked` (`internal/cli/hostfloor.go`), `captureAct` (`internal/cli/capturehost.go`); `TestAnInstallerProgramOnAMacIsItsCaptureMaterialized`, `TestAMacsFloorCapturesThroughTheMacosUserAct`, `TestTheMacosUserCaptureActHandsItsRuntimeToTheRunPipelineAlone`, `TestAMacsFloorNamesWhyTheMacosUserActCannotRun`. **NOT MEASURED on a Mac:** the floor's materialize of a real macos-user capture, relocated out of the staging home (`TestMacosUserHostFloorMaterializesAFixtureInstallerCapture`, Mac-only), a real claude or agy capture relocatable, sudo asking once at a terminal, and a launch with no terminal falling back to the PATH copy |
| [OQ-HP4](#OQ-HP4) | **Maintainer ruling:** the prefix's Node is the official tarball, checksum-verified, at yolo's version raised to the highest selected `node_floor` | 2026-09-29 | **Built** ([HP-D6](#HP-D6)): Node v24.21.0 with its published sha256 compiled in, raised to a higher `node_floor` |
| [OQ-HP5](#OQ-HP5) | **Maintainer ruling:** no consent prompt; selecting the pack is the consent, and the floor installs and updates like a jail's launchers | 2026-09-29 | **Built.** Nothing prompts |
| <a id="HP-D3"></a>HP-D3 | *Consequence of HP5:* [§4](#4-when-provisioning-runs)'s consent step and HP1's "installs still need consent" are withdrawn; a launch with no terminal installs too, disclosed on its launch line | 2026-09-29 | **Built.** A launch installs the one program it starts, with progress lines on stderr; `yolo host apply --assert` installs every missing entry ([HP-D8](#HP-D8)) |
| <a id="HP-DIR2"></a>HP-DIR2 | **Maintainer direction (2026-09-29), the two environment layers:** *"we always want our own node … like a nix shell thing … make the predictable environment predictable … we construct an environment. We do not sniff the command line … yolo host will let it use the node from the project. It will have a fallback of the floor … And then when you run the agent's wrapper … that one will now strip out the mise because now it wants not just a floor, but a predictable environment."* (1) `yolo host -- <cmd>` composes an environment: the user's PATH, project tools and mise shims included, with the floor as a FALLBACK after it; yolo never inspects the command. (2) An agent yolo delivers from the floor runs in a set, predictable environment: the floor's own node by absolute path, mise stripped. Replaces [HP-D1](#HP-D1)'s "prefix first" | 2026-09-29 | **Built**, `host_path` included since 2026-09-30 ([the launch PATH](../reference/host-agent-environment.md#the-launch-path), [HE-D3](host-launch-environment.md#he-d3); the per-OS baseline was withdrawn by [HE-DIR1](../reference/host-agent-environment.md#he-dir1)): the child's PATH is the caller's, then `host_path`'s folders, then the floor's `bin/`. Item (2) as [HP-D4](#HP-D4) reads it |
| <a id="HP-DIR3"></a>HP-DIR3 | **Maintainer principle (2026-09-29): at the host, yolo manages the AGENT's environment, never the WORKSPACE's runtime.** In a jail yolo provisions the workspace's runtime (it is the contained room, and safe to provision); at the host the workspace is where the user works, and its toolchain is the user's, or the host agent's to install once running. Extended by the maintainer to every question it applies to | 2026-09-29 | **Built** as an absence: nothing at the host provisions or activates the workspace's runtime |
| [OQ-HP6](#OQ-HP6) | **Maintainer ruling:** never; no `mise install`, `mise env` or direnv at the host | 2026-09-29 | **Built** as an absence: no `mise` call anywhere at the host |
| [OQ-HP7](#OQ-HP7) | **Maintainer ruling:** (a); the agent's own startup is fixed, its children see the user's environment | 2026-09-29 | **Built.** The child's environment is the one it was handed, the caller's PATH first |
| <a id="HP-DIR4"></a>HP-DIR4 | **Maintainer ruling (2026-09-29), which copy of a pack's agent `yolo host` runs, with a correction to how [HP-DIR2](#HP-DIR2) item (1) was read:** *"I thought the whole point of running yolo host was to get the actual agent."* `yolo host -- <agent>` runs the floor's copy whenever a selected pack delivers that agent, from any launcher (a terminal, a Waybar widget, cron). A copy the user installed, such as `~/.local/bin/claude`, is not the one that runs. The agent's own commands (`npm test`) see the user's PATH, mise included, first and the floor after it ([OQ-HP7](#OQ-HP7)). A program no selected pack delivers resolves on the user's PATH as usual, with the floor after. **The correction:** HP-DIR2 item (1)'s "the user's PATH … with the floor as a FALLBACK after it" describes the environment the agent's commands run in (the project's `node`, for example), never which copy of a pack's agent runs. HP-DIR2's text stands, read that way. [HP-D1](#HP-D1)'s exec of the floor's program by path stands too; HP-DIR2 still replaces its "prefix first" in the PATH the agent is handed. Recorded in [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs) as [OQ-HE10](../reference/host-agent-environment.md#oq-he10), answered (c). *Recorder's reading, not the maintainer's words:* HP-DIR2's "yolo never inspects the command" stands as a bar on inspecting what a command will do (its arguments, an `npm test` it will run). yolo already keys a host launch on the command's base name: `composeHostLaunchWith` takes `filepath.Base` of it, and the profile, provider env, credential gate, launch flags and launch services all follow the pack that installs that name (`selectedPackInstalls` is the existing predicate). HP-DIR4 adds only which copy runs to that same key, and only for a bare name: a target given as a path is exec'd as given ([the one resolver](../reference/host-agent-environment.md#one-resolver)). "Delivers" means the floor holds, or can provision, an entry for the program ([Defined terms](#defined-terms)); what runs for a selected pack's program with no possible floor entry is [OQ-HE11](../reference/host-agent-environment.md#oq-he11), ruled (a) | 2026-09-29 | **Built** (`resolveHostLaunchTarget`): a bare name a selected pack delivers runs the floor's copy; a path is exec'd as given; anything else, and a program with no floor entry, is looked up on the child's PATH, the latter with one line ([OQ-HE11](../reference/host-agent-environment.md#oq-he11), ruled (a)) |
| <a id="HP-D4"></a>HP-D4 | *Implementation decision:* the deciding (first install, a moved declaration, the throttled refresh, the lock) runs in yolo, on the launch that asks, before the exec; `bin/<bin>` is an exec-only launcher, `exec <node> <entry> "$@"` for a Node script and `exec <entry> "$@"` for anything else. The jail's bash launcher leans on what its image bakes (`jq`, `timeout`, a `yolo` on PATH to materialize with), which a host does not, so the same template here would behave per machine. HP-DIR2 item (2)'s "mise stripped" is read as: nothing on PATH chooses what starts or what interpreter it runs on, because both are absolute paths; the environment is otherwise the one handed over, since [OQ-HP7](#OQ-HP7) gives the agent's commands the user's own, mise included | 2026-09-29 | **Built.** `internal/hostfloor/ensure.go` (`launcherScript`). Its comment line names the record's bin, version and pack only through `commentField`, which turns every character outside a small safe set into `?`: the version is text a vendor chose (npm's `package.json`, a directory a capture's installer made), and a newline in it would otherwise make the rest a line the launcher runs (`launcher_test.go`) |
| <a id="HP-D5"></a>HP-D5 | *Implementation decision under [OQ-HP1](#OQ-HP1):* the configuration is a user-scope `host_floor` key taking `agent_updates`' two shapes, read by the same reader: `false` is a floor of nothing, and `{"*": true, "<pack>": false}` leaves a pack out. A workspace value is a `yolo check` error | 2026-09-29 | **Built.** `internal/config/hostfloor.go`, `entrypoint.PackPolicyAllows` |
| <a id="HP-D6"></a>HP-D6 | *Implementation decision under [OQ-HP4](#OQ-HP4):* the shipped release is Node v24.21.0, the LTS line the jail image runs, as `.tar.gz`, with its four platforms' published sha256 compiled in, so the check does not trust a value fetched beside the bytes. A higher `node_floor` raises the floor to that floor padded to three parts (`22.19` is v22.19.0, the lowest release meeting it), checked against that release's `SHASUMS256.txt`. An install runs npm with the floor's Node first on a fixed system PATH, and drops the ambient variables that would redirect an npm install or change what Node loads (`NPM_CONFIG_*`, `npm_config_*`, `NODE_OPTIONS`, `NODE_PATH`). A Node script is started by the floor's Node; a native build shipped through npm (opencode-ai's) is started as itself. An entry keeps the Node it was installed on until a refresh or a moved declaration reinstalls it | 2026-09-29 | **Built.** `internal/hostfloor/node.go`, `run.go`. The real tarball and its npm are exercised by `TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode` (every push: Linux x64 and arm64, and darwin-x64 on the macOS nightly), by `TestMacosUserHostFloorIsTheHostUsersAlone` (darwin-arm64, `macos-user.yml`), and by `TestHostFloorInstallsTheVendorsRelease`'s npm subtests (Pack Installs). **MEASURED 2026-10-05:** all four compiled-in digests match nodejs.org's `SHASUMS256.txt` for v24.21.0, and in this development jail the linux-x64 tarball verified and `cowsay@1.6.0` installed with its npm ran with an empty environment |
| <a id="HP-D7"></a>HP-D7 | *Implementation decision under [OQ-HP3](#OQ-HP3):* container captures now record the full reference scan (`--scan-content-refs`), without which `capture.Materialize` refuses to move an entry out of `/home/agent`; the floor materializes the store's selected entry into its own install directory and relocates it. An entry recorded before the scan is recaptured once. With no capture in the store, the floor runs `yolo capture <bin>` when a container runtime is on PATH; with none, the program has no floor entry here, and neither has one whose only capture was recorded for a jail's home (**revised 2026-10-05 by [HP-D18](#HP-D18)**: with none, a Linux host whose kernel offers Landlock captures it on the host). A capture whose `~/.local/bin/<bin>` is not in the capture has no floor entry either: codex's installer links it into `~/.codex`, which no capture records (measured 2026-09-29 against this machine's store; its claude 2.1.267 and agy entries hold their program, and no file in either names `/home/agent`, so a full scan finds them relocatable). An installer program is refreshed when the store's selected entry changes; the vendor's own update verb is never run against the real home. **Revised 2026-10-05 by [HP-D17](#HP-D17):** codex is in the floor; its exclusion rested on a capture recorded before its payload was a capture surface, and the recapture now comes before the program check. **Revised 2026-10-05 by [HP-D16](#HP-D16):** the refresh installs a newer release only, and captures again once the newest capture is a day old | 2026-09-29 | **Built** (`internal/hostfloor/ensure.go`, `captured.go`; `captureJailArgv`). A capture jail's entry run from the floor on a real host is pinned by `TestHostFloorRunsARealCaptureOfAnInstallerFixture` (every push, Linux: a real capture jail, the confined materialize, the relocated link) for a hermetic fixture, and by `TestHostFloorInstallsTheVendorsRelease` (Pack Installs, Ubuntu) for claude, agy and codex. **Where the capture jail writes** (2026-10-05; implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible): its own stdout, like its stderr, goes to this process's stderr, the raw stream rather than the launch's teed one, so the host launch log keeps only yolo's own lines; the floor's fork build and every advance at the host do the same (`hostJailStdout`). Until then it reached the launch's stdout, ahead of the agent's output. Pinned by `TestTheFloorsCaptureJailWritesItsOutputToTheLaunchsStderr` and its siblings in `hostfloorjailstdout_test.go`, and on a real host by the capture cell's stdout assertion |
| <a id="HP-D8"></a>HP-D8 | *Implementation decision:* the prefix is `host-floor/` under the state dir, `0700`: `bin/`, `node/v<version>/`, `programs/<bin>/<id>/` (a completion marker written last), `records/<bin>.json`, `receipts.jsonl`, `locks/`. Each install keeps its program's current and previous version. The per-program lock is a `flock` the kernel drops with its holder, so no lock can be held by a dead pid; `yolo check` reports an install running now and an interrupted one's leftover, which `yolo prune --apply` alone reclaims. `yolo host apply --assert` installs every missing entry and removes one no selected pack delivers, but only when the whole selection resolved; the launch gate's apply takes neither step. A `records/<bin>.json` whose schema is newer than this yolo's is refused and never installed over, as a pack lockfile of a newer schema is: `yolo host -- <bin>` and `yolo host apply --assert` refuse it and name two steps: `yolo update`, and, for a machine where the update finds nothing newer (the prefix is every yolo's on the machine, so the record can be another install's), removing the record, after which this yolo installs its own copy. While such a record is in the prefix, an `--assert` drops no Node release, since this yolo cannot read which one that record runs on (decided 2026-10-01) | 2026-09-29 | **Built.** `internal/hostfloor`, `internal/prune/hostfloor.go`, `yolo stores` |
| <a id="HP-D9"></a>HP-D9 | *Implementation decision under [the one resolver](../reference/host-agent-environment.md#one-resolver):* the dependency probe of `yolo host apply` (and so the launch gate's survey) and `yolo check-deps` answers a program the floor delivers by its floor entry, as satisfied (provisioned, or the floor's to install), never by a PATH lookup, so a not-yet-installed agent is not a missing-dependency blocker | 2026-09-29 | **Built.** `resolveHostDeps`, `configuredDepRequirements` |
| <a id="HP-D10"></a>HP-D10 | *Implementation decision under [OQ-HP3](#OQ-HP3), from the build's review:* the floor's materialize runs on the HOST, following a manifest the capture driver wrote inside the capture jail, so the manifest is treated as that jail's claim. Every materialize, in a jail too, refuses before its first write a manifest whose entries are not a tree under the home (an empty, absolute, unclean or climbing path, one listed twice, an unknown kind, one beneath a non-directory entry) and a file entry whose store source is not a regular file; none of these can come from a capture's own walk. The floor's is a **confined** materialize, which also requires an empty home, keeps every entry inside the capture surfaces, and checks each directory on the way to an entry with `Lstat`, so it writes through no link (the one case the entry checks cannot see is two names one directory on a case-insensitive filesystem). A jail's home is never confined: macos-user's reaches `.local` through a link it must follow | 2026-09-29 | **Built.** `internal/capture/confine.go` (`MaterializeOptions.Confined`); the floor sets it in `installFromCapture` |
| <a id="HP-D11"></a>HP-D11 | *Implementation decision under [HP-DIR4](#HP-DIR4), from the build's review:* a [deselected entry](#defined-terms) is never run. The launch's PATH lookup skips the floor's `bin/` for every name, one lookup for a program with no floor entry and for any other name: every name in `bin/` is a floor entry, and one a selected pack delivers runs by path before the lookup, so a hit there could only be a deselected entry. A launch that finds no other copy says the floor still holds one it does not run and that `yolo host apply --assert` removes it; `yolo check` says the same beside the no-floor-entry row. The child's PATH still ends with the floor's `bin/` ([HE-D1](host-launch-environment.md#he-d1)), deselected entry included, until the apply removes it | 2026-09-29 | **Built.** `resolveHostLaunchTarget`; `hostfloordeselected_test.go` |
| <a id="HP-D12"></a>HP-D12 | *Implementation decision under [HE-D1](host-launch-environment.md#he-d1), from the build's review:* a caller that passes no PATH at all (`env -i`) gets the floor installer's system baseline (`hostfloor.BaselinePath`, the per-OS system directories that exist) in place of its PATH, ahead of the floor's `bin/`. Otherwise the child would hold the floor's `bin/` alone, and every command an agent runs by name would be unfound, where before the floor a child with no PATH at least had libc's default search path | 2026-09-29 | **Built.** `hostChildPath`, through `hostpath.Launch.Child`. It stands in for the PATH the caller did not pass, and `host_path`'s folders follow it ([HE-D3](host-launch-environment.md#he-d3)); yolo's checks keep `host_path` alone ([HE-D4](host-launch-environment.md#he-d4)) |
| <a id="HP-D13"></a>HP-D13 | *Implementation decision under [OQ-RO3](../reference/report-tiers.md#why-its-this-way) (a launch has no quiet mode), from the build's review:* the evergreen refresh prints one line, naming the program, its installed version and the poll's bound, before its `npm view`. The poll is bounded at 60 s and runs before the hand-over line, so a slow or unreachable registry was otherwise a launch that waited up to a minute, once an hour, saying nothing | 2026-09-29 | **Built.** `newerThan`; `refreshnotice_test.go`. The installer recipe's refresh reads the capture store offline, and prints one line, naming the program and its installed version, before the capture it runs once its newest capture is a day old ([HP-D16](#HP-D16)) |
| <a id="HP-D14"></a>HP-D14 | *Implementation decision, from the build's review:* `yolo host apply --format json` carries the floor stage as `host_floor`, one row per program the selected packs declare (bin, pack, disposition, action, reason, version, launcher) and one per entry an `--assert` would remove (disposition `deselected`). The document is the dry run's text report as data, and the floor's "would remove" is a loss the text prints | 2026-09-29 | **Built.** `hostApplyDoc.HostFloor`, filled by `applyHostFloor` |
| <a id="HP-D15"></a>HP-D15 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **A program asking for a dynamic loader this machine lacks has no floor entry, so `yolo host` runs the PATH copy and names the step.** A Linux program built against glibc names its loader by absolute path (its `PT_INTERP`): Node's official 24.21.0 build asks for `/lib64/ld-linux-x86-64.so.2` on x86-64 and `/lib/ld-linux-aarch64.so.1` on arm64, and the captured claude and agy ask for the x86-64 one (MEASURED). A host with no file there cannot start it, and the floor's launcher exits 127: NixOS without nix-ld, and a musl system. NixOS's default `environment.stub-ld` puts a **stub** at that path: a static program, the store path `<hash>-stub-ld`, that prints a pointer to nix.dev and exits 127 (READ in nixpkgs' `nixos/modules/config/stub-ld.nix`). So a check that the file exists misses NixOS's own default, and a loader that resolves to a file named `*-stub-ld` counts as missing too. The floor reads `PT_INTERP` itself (the header, the program headers and the string) and resolves it as the kernel would, links followed, on Linux only. It checks Node's loader, compiled in per platform, before any download: at an npm program's status, and for a fork's Node script, which the floor runs on that build, both at its status from the store's build and at its install before Node is fetched. It checks the extracted `node` against the compiled-in loader, refusing a release that asks for another. It checks a capture's or a fork build's own program from the store before a materialize; whatever an install leaves before its launcher is switched; and an installed copy on every status, so a copy whose loader went away has no floor entry and `yolo host apply --assert` removes it. A loader path that leads to no file counts as missing: nothing there, a file where a directory of the path should be, a chain of links that never ends, or a directory. A patched fork's status reads its good build's program the same way, so no launch copies a good build this machine cannot start. The reason names the loader and its step: on NixOS, `programs.nix-ld.enable = true;`. A file that is not ELF, or one that cannot be read, needs no loader | 2026-10-05 | **Built.** `internal/hostfloor/elfinterp.go` (`Floor.Root` for tests); `elfinterp_test.go`, `TestHostLaunchOnAMachineWithoutTheFloorsLoaderRunsThePATHCopyAndNamesTheStep`, `TestCheckNamesTheLoaderAProgramLacksAndTheNixLDStep`. NixOS stub-ld and nix-ld hosts are NOT MEASURED |
| <a id="HP-D16"></a>HP-D16 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **An installer agent stays current by being captured again, once its newest capture is a day old,** which is [OQ-HP5](#OQ-HP5)'s "keeps current … the same way a jail's launchers do" for the recipe whose vendor updater the floor may not run ([OQ-HP3](#OQ-HP3) rejected running the vendor script under a prefix `$HOME`). Under the refresh's existing hourly stamp and `agent_updates` gate: when the store holds nothing newer and the newest capture is at least 24 hours old (`DefaultCaptureRefreshAge`), the floor prints one line naming the program, its installed version and the `yolo capture <bin>` it is running ([HP-D13](#HP-D13)), runs the capture act once, and installs what it stored only when that release is newer. The age runs from the modification time of the receipt log beside the store's selected entry, which every `record` receipt moves, a capture of identical bytes included, and from the floor's own install time when there is none: read as a file time, because the receipt schema has one reader (`internal/entrypoint`). **Never a downgrade:** the store selects its newest capture, which can be an older release (a vendor's stable channel behind the latest), so where both sides carry a versions-directory name the refresh compares the dotted release number each name starts with and installs only a newer one. The number, not the whole name: codex names a release directory `<version>-<target>` (`0.159.1-x86_64-unknown-linux-musl`), and `packdecl.CompareVersions` reads a part that is not a plain integer as 0, so compared whole, every codex patch release is equal to the one before it. Two names with one number and different text (a pre-release suffix) are ordered by nothing the floor reads, and follow the store's newest capture; a program whose captures carry no versions directory (agy's lone binary) has nothing to compare and follows the store's newest capture, as every jail's materialize does. A failed or contended capture keeps the installed copy and waits out the hourly interval, after which the next poll captures again, since only a recorded capture moves the age; a machine that cannot capture (no runtime, and neither the macos-user act nor Landlock, [HP-D18](#HP-D18)) asks nothing and keeps what it runs. The line before the capture says how it runs, as the first capture's does | 2026-10-05 | **Built.** `recaptureWhenOld`, `newerCapture` in `internal/hostfloor/ensure.go`; `evergreen_test.go`, `TestTheProductionFloorsRefreshRecapturesAnInstallerAgentThroughTheCaptureAct`. A real backdated capture is NOT MEASURED |
| <a id="HP-D17"></a>HP-D17 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **codex is in the floor on Linux** ([OQ-HP3](#OQ-HP3) names it), retracting [HP-D7](#HP-D7)'s exclusion. That exclusion was measured against a capture this machine recorded on 2026-09-09, before captures scanned their contents and before `~/.codex/packages/standalone` was a capture surface (`paths.InstalledProgramSurfaces`, added 2026-09-14): the entry held the link and nothing it named. Two things kept even a new capture out. The floor judged a capture's program before the recapture an entry recorded for a jail's home gets, so a stale entry was no floor entry for good, with the recapture never run (MEASURED: no floor entry, no capture); the recapture now comes first, in the status and in the install. And the manifest walk followed only a link that is the whole path, while codex's `~/.local/bin/codex` reaches its program through `current`, a link to a release directory; the walk now resolves a link in every component, as the kernel does, and refuses a target outside the capture's home, a climb out of it, a file mid-path and a component it did not record. A capture recorded with the full scan but before the surface its program's links lead into is captured again once, too | 2026-10-05 | **Built.** `internal/hostfloor/captured.go` (`recaptureReason`, `programInManifest`), `floor.go` (`provisionable`), `ensure.go` (`installFromCapture`); `realcapture_test.go`, `captured_test.go`, `TestAStaleCodexCaptureIsRecapturedBeforeItsProgramIsJudged` |
| <a id="HP-D18"></a>HP-D18 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible. Revises [HP-D7](#HP-D7)'s "with none, no floor entry".* **On a Linux host with no container runtime, `yolo capture <installer bin>` runs the vendor installer on the host, confined by Landlock**, and the floor uses it. Only when nothing selected a runtime (YOLO_RUNTIME, the user config's `runtime`) and none is on PATH: a runtime on PATH keeps the capture jail, and a selected runtime that is missing is the reason, as a launch's is. It needs Landlock ABI 2 (Linux 5.19), the first that lets a confined process rename across directories. The installer runs the jail's own generated launcher (`entrypoint.NativeCaptureLauncher`) and the same capture driver, `capture-run --scan-content-refs`, under a re-exec, `yolo internal landlock-exec`, that restricts itself and execs the driver: it may write only beneath the staging tree (its `TMPDIR` among it), `/dev/shm` and `/dev`'s stand-in devices (`null`, `zero`, `full`, `random`, `urandom`, `tty`); read and run everything else except the branches holding the user's home and runtime directory, the real `/tmp` and the rest of `/dev` (the yolo executable and the CA bundles its environment names excepted); from ABI 5, use device ioctls only on those stand-ins; create no AF_UNIX socket and set up no io_uring (a seccomp filter: Landlock does not mediate connecting to an existing socket, and the user's tmux, ssh-agent or docker socket would otherwise be one connect away); and, from ABI 6, signal nothing outside itself. **Not `/tmp`** (narrowed 2026-10-05, after the build's review): yolo keeps files there that only owner-only permissions protect from a process of the same user — a `yolo host` session's endpoint files, each a broker's address, certificate pin and bearer token, and a launch service's input — and Landlock can only allow, so `/tmp` cannot be granted with them carved out. **Not `/dev/pts`:** the user's terminals, where another session's typing could be read. **It lives no longer than the capture:** the confinement runs beneath a supervisor, `landlock-exec --supervise`, a child subreaper outside the confinement that kills and reaps whatever the installer leaves running once the driver exits, a process double-forked into a session of its own included, as stopping a capture jail's container would. It has no controlling terminal, its output reaches the user through pipes, and its environment is env -i's plus locale, terminal type, proxies and CA bundles: no token, agent socket or session bus. **The recorded origin is the receipt's platform**, `linux/<arch>+host`: selection and its complement, the reap, key on the platform, so no jail selects a build its installer chose for this host (a musl system's, say), and the reap keeps each origin's newest; the floor selects the newer of either. With no Landlock either (none, one older than ABI 2, or an architecture other than amd64 and arm64, which the seccomp filter is built for), the program has no floor entry, naming both ways on: a container runtime, or a kernel with Landlock enabled. A fork's build still runs in a sealed jail or not at all: a pack may not build on the host ([forked-programs §12](forked-programs-as-packs.md#12-what-this-does-not-license)). The residual, stated: below ABI 3 the installer may truncate a file it can name outside its write set; below ABI 5 it may use the ioctls of a device node it finds outside `/dev`; below ABI 6 it may signal any process of the user's, its supervisor included, so one that kills the supervisor before it exits can leave a process running after the capture. That last is accepted rather than refused, so the host capture keeps every kernel from Linux 5.19 (ABI 2) rather than only those from 6.12 (ABI 6): only an installer written to defeat yolo does it, and the floor runs what any installer installs unconfined in any case, so the confinement keeps an ordinary installer's side effects off the machine and cannot stop a hostile one. It may read what is outside the home, the runtime directory, the real `/tmp` and `/dev`; read and write beneath `/dev/shm`, where other programs of the user's may keep shared memory; and reach the network, as any program the user runs may | 2026-10-05 | **Built.** `internal/capture/landlock_linux.go` (`CapturePolicy`, `ExecConfined`), `internal/cli/capturelandlock_linux.go`, `chooseCaptureArm` (`capturehost.go`), `resolveFloorCapture` (`capturematerialize.go`); `capture.Supervise` (`landlock_linux.go`); `TestTheHostCapturesConfinementHoldsOnThisKernel` (a Landlock kernel's own answers, `/tmp`, `/dev/pts` and the signal scope among them; `YOLO_TEST_REQUIRE_LANDLOCK=1` makes its skip a failure), `TestADevicesIoctlsNeedTheirOwnGrant`, `TestASupervisedConfinementLeavesNothingRunning`, `TestAHostCaptureConfinesItsInstallerAndTheFloorRunsWhatItLeft` (the production chain: the home, runtime directory, account home, tokens and terminal session withheld, the CA bundle granted, nothing left running), `TestTheCaptureActChoosesItsArmAtTheCallSite`, `TestTheCaptureActTakesItsJailArmUnderTheMacosUserRuntime`, `TestAJailNeverSelectsAHostCapture`. **MEASURED 2026-10-05** in this development jail (Landlock ABI 10, no podman on PATH, a scratch HOME): `yolo capture agy` ran the real installer under Landlock and stored a 209.9 MB entry under `linux/amd64+host`; `yolo host apply --assert` materialized it, and the floor's `agy --version` printed 1.2.17 with an empty environment. On a second scratch HOME, a first `yolo host -- agy --version` did the same unprompted: captured, materialized, and ran the floor's copy. **Measured again after the review's narrowing** (no `/tmp`, no `/dev` beyond the stand-ins, the supervisor), each on a fresh scratch HOME: agy the same 209.9 MB entry, its floor copy printing 1.2.17; and claude, a 246.1 MB relocatable entry under `linux/amd64+host`, materialized by `yolo host apply --assert`, its floor copy printing `2.1.289 (Claude Code)` with an empty environment, and no process of either installer left running. **NOT MEASURED:** a real Linux host without podman (`yolo host -- claude`), and an ABI 2 kernel |
| <a id="HP-D19"></a>HP-D19 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible. Under [OQ-HP5](#OQ-HP5)'s "keeps current … the same way a jail's launchers do (throttled, at the agent's own invocation)" and [HP-DIR3](#HP-DIR3): the add-ons are the agent's environment, not the workspace's.* **`yolo host -- <bin>` runs the program's [pre-launch refresh](#defined-terms) as the jail's launcher does,** so `yolo host -- pi` runs `pi update --extensions` and pi stops showing its "Package Updates Available" box at the host. Until now only the jail's launchers read a pack's `refresh`. **A step of the launch, not of the floor's install:** `yolo host apply --assert`, which installs the floor, must not run a program; a copy found on the launch PATH or given as a path is refreshed too, since the refresh moves the program's add-ons rather than the program; and only the launch has the environment to run it in. It runs once the target has resolved, the floor's first install included, and before the model menu and the OpenAI prelaunch, the jail launcher's order, so a launch refused later, at that prelaunch or at a launch-owned service's start, has already refreshed, as it has already installed: the refresh starts no session and holds no credential. **What it keeps of the jail's:** due when its stamp is missing or at least 3600 s old, or when a file the pack watches (`due_on_change`, read under the real home) holds content no refresh has succeeded with; only where `agent_updates` lets the pack move; one at a time per program; a held lock is one line and the launch runs what is installed, except that new watched content waits up to 60 s for the holder and then asks again, the launcher's one wait; bounded at 60 s, with the launcher's 5 s before the kill, with no terminal and a `/dev/null` stdin, and its output on the launch's stderr, nested under the line that says it is running (so in the host launch log too); in the launch's working directory; stamped on every outcome and the watched content recorded only on a zero exit; a failure, a timeout or a Ctrl-C printed in the launcher's words, and the program started anyway; a SIGTERM or SIGHUP releasing the lock and ending the launch with 128 plus the signal, naming the re-run. Both signals are read off what the launch RECEIVED while the refresh ran, not off how the refresh exited: one that handles the forwarded signal and exits with a status of its own (a shell trap, a node handler's `process.exit(143)`) still ends the launch, as the launcher's `_shielded` trap ends the launcher whatever the program exits with, and a refresh that exits 0 after a Ctrl-C is interrupted, not successful, as the launcher's `_bounded` reads it. **What differs, and why:** the lock, the stamp and the watched-content records are the floor's own, under the prefix's `refresh/` (`<bin>.lock`, `<bin>.stamp`, `<bin>.seen/`), never the pack's `lock` or a jail's stamp. The pack's lock sits inside the store every jail shares (`~/.pi-shared-npm` in a jail's home, a directory under yolo's state dir on the host), and a host pi writes its own store under the real home (`~/.pi/agent/npm`, which a jail's home links to the shared one: [`pi-extension-lifecycle.md` §3.1](pi-extension-lifecycle.md#31-storage-tier-decoupling-packages-from-session-state)), so a lock there would order the host against nothing; a flock is dropped by the kernel with its holder, so it needs neither the launcher's stale-lock break nor its heartbeat. The lock is beside the stamp rather than among the install locks in `locks/`, so no program's name can collide with it, as one named `refresh-pi` would with `locks/refresh-pi.lock`. The environment is the one the launch inherited with the part of the program's composition that every process receives applied over it: each entry whose winner is also the credential gate's shared composition's, which is the ungated pack env (pi's `PI_TELEMETRY=0`), the `env_sources` values no provider claims (a proxy, a CA bundle, a registry) and the `env_sources` removals. That is what a jail's refresh holds, since a jail's shared env file reaches every process there, the launcher's refresh included. What the gate scopes to the program alone (its provider's claimed credentials, its profile-gated pack env, its env derive's output) is in the agent's own env file in a jail, which the launcher sources only after the refresh, because the refresh needs no credential and should run holding none; here it reaches the program alone. The first build applied the removals alone, which left a refresh behind a proxy set only through `env_sources` unable to reach its registry; corrected 2026-10-05 after the build's review. Its PATH is the child's before the blocked tools join it, as the jail runs its refresh with the blockers bypassed. Its time is its own span, `host.prelaunch_refresh`, recorded only for a program that declares a refresh. **Not at all** in a jail, whose own launcher runs it, nor on macos-user, which runs the jail's launchers, nor by `yolo host apply` | 2026-10-05 | **Built.** `Floor.PrelaunchRefresh` (`internal/hostfloor/prelaunch.go`), called from `hostLaunch` through `hostRefreshProgram` and `hostPrelaunchRefresh` (`internal/cli/hostfloor.go`); `prelaunch_test.go`, and `hostprelaunchrefresh_test.go` at the call site, the floor's own copy included. A real pi with an npm extension is NOT MEASURED |
