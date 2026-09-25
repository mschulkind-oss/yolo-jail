---
title: "A host agent floor: yolo keeps its own agents installed on the host, so a launch needs no particular PATH"
date: 2026-09-25
status: draft
tags: [host, provisioning, floor, program, npm, capture, mise, path, evergreen]
summary: "yolo host should run every agent a selected pack declares from any launcher, a Waybar widget included, without the user having arranged a PATH. The design is a host agent floor: the program binaries of the user-scope selected packs, installed on consent into a yolo-owned host prefix, kept current the way the jail's launchers keep them, and put first on the composed host PATH. npm programs are covered on both OSes; the three installer-recipe agents, and whether yolo runs mise install on a stale shim, are open. Six questions are open. Nothing is built."
---

# A host agent floor: yolo keeps its own agents installed on the host, so a launch needs no particular PATH

**Status:** DESIGN, 2026-09-25. Nothing built. Evidence read against `7e529260`, and it cites symbols,
never line numbers.

> **In short.** What yolo can guarantee at the host is the set of agents it already knows about:
> the `program` binaries of the selected packs. Installing those into a directory yolo owns, and
> putting only those names first on the composed host PATH, makes the floor a fact about the
> machine rather than about whoever launched yolo. Everything else, the user's own tools included,
> stays the user's.

**Why it matters.** A Waybar-started `yolo host -- opencode` exited 127 because the only `opencode`
was a mise shim that an interactive rc file activates
([`host-launch-environment.md` §1.3](host-launch-environment.md#13-how-the-incident-happened)).
Composing the PATH makes that failure the same everywhere, but it doesn't make it go away. Something
still has to put the agent somewhere the composed PATH names, and today at the host nothing yolo
drives does ([`provisioner-sets.md` §1](provisioner-sets.md#1-the-verdict-and-five-principles)).

**The shape.** The [host agent floor](#defined-terms) (what must resolve) is installed into the
[host prefix](#defined-terms) (where it lives) by the jail's own launcher shape (how it stays
current). The first install is gated by one consent, and [`yolo check`](#7-what-yolo-check-reports)
reports the result.

**Cost.** A second copy of an agent a user already installed by hand, until they declare where
theirs lives. A new yolo-owned tree of executables on the host, which has to stay outside every
jail's reach. On macOS, the three installer-recipe agents stay uncovered until
[OQ-HP3](#OQ-HP3) is ruled.

**Start at [§3](#3-the-host-prefix).** The prefix's two rules, *only floor names on PATH* and *never
jail-reachable*, are what make putting it first safe.

**Needs your ruling:** [OQ-HP1](#OQ-HP1), [OQ-HP2](#OQ-HP2), [OQ-HP3](#OQ-HP3), [OQ-HP4](#OQ-HP4),
[OQ-HP5](#OQ-HP5), [OQ-HP6](#OQ-HP6).

**Reads with:**
- [`host-tool-provisioning-plan.md`](host-tool-provisioning-plan.md): the implementation sketch.
  It is incomplete, and nobody builds from it.
- [`host-launch-environment.md`](host-launch-environment.md): the composed host PATH. This doc
  supplies its first entry.
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
- **Floor entry disposition** *(coined here)*: what [`yolo check`](#7-what-yolo-check-reports) and a
  launch say about one entry. It is one of **provisioned** (in the prefix), **present** (found
  elsewhere on the composed host PATH), **stale shim** (a mise shim whose selected version isn't
  installed, per
  [`host-launch-environment.md` §2.3](host-launch-environment.md#23-tool-managers--mise-and-the-shim-is-not-installed-problem)),
  or **missing**.
- **Composed host PATH**, **notch**, **ambient environment**: as defined in
  [`host-launch-environment.md` §0](host-launch-environment.md#0-the-governing-ruling).
- **Agent dependency**, **project dependency**: as defined in
  [`program-delivery.md` §3.5](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03).

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
  environment's npm prefix or the vendor's default puts it, which is a place the next composed PATH
  may not name. [OQ-PS13](provisioner-sets.md#OQ-PS13) records that this run skips the jail
  launcher's installer-body check.
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
  covered by the carried PATH in
  [`host-launch-environment.md`](host-launch-environment.md), not by the floor.
- MCP servers the agents drive. They are agent dependencies too, but v1 covers the agent binaries
  only ([§9](#9-non-goals)).

**Degenerate cases:**
- No selected pack declares a program: the floor is empty, and nothing is installed or reported.
- Two packs declare the same `bin`: the pack system's existing combine rule for a `program`
  target decides which one counts. This doc adds no rule, and the floor holds one entry per name.
- A program whose vendor publishes no build for this OS and architecture: its entry is reported
  as **missing, unpublished here**, using the same `launcherUnpublished` predicate the jail uses,
  and no install is ever attempted.

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
- **Never on the user's interactive PATH.** The prefix enters only the composed host PATH of a
  `yolo host` launch. `yolo host env` still emits no PATH line.

**What `bin/` holds.** Exactly one executable per **provisioned** floor entry, and nothing else:
no `node`, no `npm`, no package's secondary binaries. This is the rule that makes putting the
prefix first ([OQ-HP2](#OQ-HP2)) safe. The prefix can shadow only an agent name, and only one that
nothing on the composed PATH provided when yolo installed it. Each entry is a generated launcher of
the jail's shape: first-use install, throttled evergreen refresh, and the exec prefix for a
declared `node_floor`. An npm agent therefore runs under the prefix's interpreter by absolute path,
while `node` in its shell children is still the project's
([agent-program-runtimes](../reference/agent-program-runtimes.md#what-this-does-not-do)).

**Which recipes it carries:**

| Recipe | In the prefix? |
| :--- | :--- |
| `via: npm` | **Yes**, on Linux and macOS. It installs into a prefix-private npm prefix with a prefix-private interpreter ([OQ-HP4](#OQ-HP4)) |
| `via: installer` | **Open**, [OQ-HP3](#OQ-HP3). The shipped vendor installers write under `$HOME`, which yolo can't redirect without running a script in place |

**Install atomicity.** A program installs into a fresh versioned directory, and its `bin/` entry is
switched only once the install exits 0 and the entry binary exists. A failed or killed install
leaves the previous version serving, or no entry at all, never a half-written one. The next attempt
removes leftover staging directories whose lock holder is gone.

## 4. When provisioning runs

| Trigger | Entry state | What happens |
| :--- | :--- | :--- |
| `yolo host apply`, at a TTY | any entry **missing** | One prompt lists every missing entry with its exact install command. Yes installs them in sequence. No leaves them missing and exits under `--assert`'s existing rule |
| `yolo host apply`, no TTY | **missing** | Prints the same list and installs nothing (the [§8.5](provisioner-sets.md#85-the-ruling-yolo-drives-the-winner-behind-the-confirm-and-last) degrade) |
| `yolo host -- <bin>`, at a TTY | target **missing** | The same prompt for that one entry, synchronously, before the exec |
| `yolo host -- <bin>`, no TTY | target **missing** | Exits 127 immediately with a line naming `yolo host apply`. **It never downloads on a non-interactive first install**: a Waybar widget can't answer a prompt, and a silent multi-minute install is worse than a named failure |
| any invocation of a **provisioned** entry | stale | The launcher's own throttled refresh, as in the jail: at most once per 3600 s, and a failure keeps the installed version and retries after the launcher's retry interval. No prompt; see [OQ-HP5](#OQ-HP5) |

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
- **Nothing is migrated or removed.** An entry already on the composed host PATH is **present**,
  and yolo installs nothing for it.
- **Present on the ambient PATH but not the composed one** doesn't count
  ([`OQ-HE0`](host-launch-environment.md#oq-he0)). The install prompt says where the other copy is
  and how to declare it (`host_path`) instead of installing a second one.

**Deselection.** A prefix entry whose program no selected pack declares any more is removed by the
next `yolo host apply`, never at launch. The entry is removed contents-only. A running agent keeps
its open inode.

## 5. mise: the stale-shim verdict

[`host-launch-environment.md` §2.3](host-launch-environment.md#23-tool-managers--mise-and-the-shim-is-not-installed-problem)
gives a shim whose `mise which` fails its own verdict, with the remedy `mise install`. The
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

- **[`host-launch-environment.md`](host-launch-environment.md).** That doc owns the composed host
  PATH. This one gives it one new first entry, the prefix's `bin/`, subject to
  [OQ-HP2](#OQ-HP2). Every decision that doc routes through the resolver (exec target, dependency
  probe, `yolo check`) sees the prefix like any other entry, and the floor disposition is computed
  from the same `LookPath`. If this design is accepted, [OQ-HE2](host-launch-environment.md#oq-he2)
  (a pack-declared install directory) narrows to programs outside the floor.
- **[`provisioner-sets.md`](provisioner-sets.md).** That doc owns *which provisioner wins*. The
  prefix is one new member of the host's provisioner set, and its rank in the default order is
  [OQ-PS6](provisioner-sets.md#OQ-PS6)'s question, where this doc proposes first for agent
  dependencies. A user who prefers `claude` from Homebrew says so through that doc's override
  ([OQ-PS7](provisioner-sets.md#OQ-PS7)). The floor entry is then **present** through Homebrew and
  never enters the prefix, so there is no shadowing by construction. The dependency gate becomes
  the consent point in [§4](#4-when-provisioning-runs), and an installer run through it inherits
  [OQ-PS13](provisioner-sets.md#OQ-PS13)'s body check.
- **The jail.** Nothing changes. The prefix is host-only, and P2's *"a notch's resolution does not
  propagate"* holds both ways.

## 7. What `yolo check` reports

The host-launch section
([`host-launch-environment.md` §3](host-launch-environment.md#3-one-authority--the-seam)) gains one
row per floor entry. The row gives its disposition, the path it resolved to, the version and date
of the last install for a provisioned entry, and the one remedy for anything else:
`yolo host apply` for missing, `mise install <tool>` for a stale shim, and nothing for present.
It also reports a lock held by a pid that no longer exists, and staging leftovers. `yolo check`
installs nothing.

## 8. Done looks like

- After one `yolo host apply` at a terminal, `yolo host -- opencode` started from Waybar, with
  mise unactivated and a minimal PATH, execs the prefix's `opencode`.
- The same launch with the entry never provisioned exits 127 within a second, naming
  `yolo host apply`, and downloads nothing.
- Inside a host-launched npm agent, `node --version` in its shell prints the project's pinned
  version, while the agent itself ran under its `node_floor` interpreter.
- Two first launches of one program started together produce one install and one receipt.
- `yolo check` lists every floor entry with a disposition. Every file yolo wrote is under the
  prefix, and no jail's mount list reaches it.

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

1. 💬 <a id="OQ-HP1"></a>**[OQ-HP1](#OQ-HP1): Is the floor on by default?**
   **Stakes:** whether a bare `"packs": ["claude"]` host user gets offered installs they never
   asked for. **(a)** On by default: every launch checks, and installs happen only on consent
   ([§4](#4-when-provisioning-runs)). **(b)** Opt-in through a user-scope key. **(c)** Off, with
   the check reported by `yolo check` only.

   _Leaning:_ **(a).** The check is cheap, the install is always consented to, and a floor
   nobody turns on guarantees nothing. [`OQ-HE0`](host-launch-environment.md#oq-he0) isn't
   engaged, because the prefix is machine state yolo owns rather than an ambient input.

   <!-- vantage: oq id=OQ-HP1 leaning="(a) on by default: every host launch checks the floor, and nothing installs without the consent in section 4; the prefix is yolo-owned machine state, not an ambient input, so OQ-HE0 is not engaged." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-HP2"></a>**[OQ-HP2](#OQ-HP2): Where does the prefix sit on the composed host PATH?**
   **Stakes:** whether yolo's copy or the user's declared copy wins when both exist. **(a)**
   First, ahead of `host_path`. **(b)** After `host_path` and before the baseline, as a fallback.
   **(c)** Last.

   _Leaning:_ **(a).** `bin/` holds only floor names ([§3](#3-the-host-prefix)), and only ones
   nothing on the composed PATH provided at install time. A user who wants their own copy says
   so through [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override, and then the prefix never holds
   that name. It also matches the jail, where the launch dir precedes every install prefix.

   <!-- vantage: oq id=OQ-HP2 leaning="(a) first: bin/ holds only floor names that nothing on the composed PATH provided at install time, a user preferring their own copy says so through OQ-PS7 and the prefix then never holds it; it mirrors the jail's launch dir." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-HP3"></a>**[OQ-HP3](#OQ-HP3): How do the installer-recipe agents (`claude`, `codex`, `agy`) reach the prefix?**
   **Stakes:** the three most-used agents. **(a)** Materialize a `yolo capture` of the
   installer into the prefix, where the host's OS and architecture match the capture jail's
   (Linux), and leave them hint-only on macOS. The capture store is `:ro` in every jail
   ([§1](#1-what-exists-today)). Whether a captured binary runs on the host outside the image's
   libraries is NOT MEASURED. **(b)** Run the vendor script under a prefix-private `$HOME`, the
   way macos-user runs it in the guest's home. That covers macOS. It would re-open the
   *"it's just not going to be a bash script"* ruling
   ([`provisioner-sets.md` §8.4](provisioner-sets.md#84-the-ruling-a-pack-declares-a-need-and-its-recipes-the-environment-resolves)),
   and the vendor's own self-updater may still write under the real `$HOME` (NOT MEASURED).
   **(c)** Keep installer programs out of the floor, reported as **missing** with the pack's
   hint.

   _Leaning:_ **(a), with (c) on macOS**, pending a measurement that a captured `claude` runs on
   a Linux host. It is the only option that keeps the standing ruling.

   <!-- vantage: oq id=OQ-HP3 leaning="(a) materialize a yolo capture into the prefix where host OS/arch match the capture jail, and (c) hint-only on macOS, pending a measurement that a captured claude runs on a Linux host; the only option that keeps the not-a-bash-script ruling." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-HP4"></a>**[OQ-HP4](#OQ-HP4): Where does the prefix's Node come from?**
   **Stakes:** the four npm agents can't install or run without an interpreter, and the floor
   may not assume one on the ambient PATH. **(a)** The official Node tarball for the platform,
   verified against the release's published checksums, at a version yolo ships. It is raised to
   the highest `node_floor` a selected pack declares. **(b)** The user's nix when present
   ([OQ-PS1](provisioner-sets.md#OQ-PS1)). **(c)** The user's mise when present.

   _Leaning:_ **(a)**, with (b) and (c) left to [OQ-PS6](provisioner-sets.md#OQ-PS6)'s ranking
   later. It is the only source present on every host, and it lives entirely inside the prefix.

   <!-- vantage: oq id=OQ-HP4 leaning="(a) the official Node tarball, checksum-verified, at a version yolo ships raised to the highest selected node_floor; nix and mise can rank above it later under OQ-PS6." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-HP5"></a>**[OQ-HP5](#OQ-HP5): Does one consent cover the evergreen refresh?**
   **Stakes:** [`provisioner-sets.md` §8.5](provisioner-sets.md#85-the-ruling-yolo-drives-the-winner-behind-the-confirm-and-last)
   says driving is *"a per-launch offer, never a background action."* It was ruled about
   system-manager commands. **(a)** Yes: the first consent covers the throttled refresh of the
   same recipe, as the jail's launchers refresh with no prompt. **(b)** No: every version change
   prompts, and a non-TTY launch never updates.

   _Leaning:_ **(a).** An agent dependency *"wants to be current"*
   ([P6](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)).
   The refresh runs at the agent's own invocation rather than in the background, and a changed
   recipe prompts again ([§4](#4-when-provisioning-runs)).

   <!-- vantage: oq id=OQ-HP5 leaning="(a) one consent per program covers the throttled refresh of the same recipe, as the jail's launchers do; a changed recipe prompts again, and the refresh runs at the agent's own invocation, not in the background." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-HP6"></a>**[OQ-HP6](#OQ-HP6): Does yolo run `mise install` on the stale-shim verdict?**
   **Stakes:** the maintainer's *"by default when you run on the host we should probably run
   the mise install."* **(a)** Yes, at a TTY, as [§5](#5-mise-the-stale-shim-verdict) scopes it:
   one tool, in the invocation's cwd, printed before it runs, with mise's own trust as the gate.
   **(b)** Yes, including non-TTY launches. **(c)** No: print the remedy, as
   [`host-launch-environment.md`](host-launch-environment.md#23-tool-managers--mise-and-the-shim-is-not-installed-problem)
   has it.

   _Leaning:_ **(a).** It is what the user's own `mise.toml` asks for. mise's trust prompt
   already guards a cloned repository's config. A widget can't consent to a download.

   <!-- vantage: oq id=OQ-HP6 leaning="(a) yes at a TTY: mise install for the one tool behind the stale shim, in the invocation's cwd, printed first, never bypassing mise's own config trust; no install on a non-TTY launch." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date |
| :--- | :--- | :--- |
| — | None yet | — |
