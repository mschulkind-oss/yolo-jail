---
title: "What `yolo host` reads for PATH — the PATH it was started with, plus `host_path`, and the floor for the agents yolo provides"
date: 2026-09-25
status: in-review
tags: [design, host, env, path, mise, depcheck, predictability]
summary: "At `yolo host`, yolo's checks (is a program a pack needs present, and which copy of a program no floor entry covers runs) read the PATH yolo was started with, plus the folders a user-scope `host_path` list adds, by the maintainer's ruling HE-DIR1. The checks look in no other folder, and a launcher with a bare PATH can get a different answer from a terminal. What stays fixed whoever starts yolo is what yolo provides: the host agent floor's copy of each selected pack's agent, and the floor's own Node. The agent is handed that same PATH with the floor's `bin/` after it, and a miss prints one line naming the PATH searched and the `host_path` fix. Nothing waits on a ruling: API keys, a region and the override check's variables keep counting from the shell that started yolo, because the agent is handed that shell (HE-D10, reversible)."
---

# What `yolo host` reads for PATH — the PATH it was started with, plus `host_path`, and the floor for the agents yolo provides

> ⚠ **Read [§0](#0-the-governing-ruling) first.** On 2026-09-29 the maintainer ruled that yolo's checks at
> the host **read the PATH yolo was started with** ([HE-DIR1](#he-dir1)). The doc had read the
> 2026-09-25 ruling the opposite way and asked [OQ-HE1](#oq-he1) to [OQ-HE9](#oq-he9) on that
> reading, twice. It is restated under HE-DIR1, which retires or answers each of them except
> [OQ-HE6](#oq-he6), about API keys. That one is answered by the implementation decision
> [HE-D10](#he-d10), which keeps today's behavior and is reversible. Nothing here waits on a
> ruling ([§9](#9-open-questions)).

**Status:** DESIGN, 2026-09-25; restated 2026-09-29 under [HE-DIR1](#he-dir1); built 2026-09-30,
as the [ledger](#10-decision-ledger)'s Built column records row by row. The code claims were
re-read against `303e0367` on 2026-09-29. Besides HE-DIR1 it rests on three rulings of that day:

- principle [HP-DIR3](host-tool-provisioning.md#HP-DIR3): at the host, yolo manages the agent's
  environment and never provisions or activates the workspace's runtime;
- [HP-DIR4](host-tool-provisioning.md#HP-DIR4): `yolo host` runs the
  [floor](#0-the-governing-ruling)'s copy of any agent a selected pack delivers, from any launcher;
- [OQ-HE10](#oq-he10), ruled (c): the PATH the agent is handed starts with the caller's.

[OQ-HE11](#oq-he11), ruled (a), decides what runs where the floor has no copy.

**Built 2026-09-29, in part** (with the floor,
[`host-tool-provisioning.md`](host-tool-provisioning.md#decision-ledger)): the three exec rules
(`resolveHostLaunchTarget` in `internal/cli/hostfloor.go`); the agent's PATH as the caller's PATH
then the floor's `bin/` ([OQ-HE10](#oq-he10) (c), [HE-D1](#he-d1)); a program the floor delivers
answered by its floor entry in the dependency probe and `check-deps`; and
[OQ-HE11](#oq-he11)'s ruled behavior (a): a selected pack's program with no floor entry is looked
up on the agent's PATH and the launch says so in one line. The launch's own lookup skips the
floor's `bin/` ([HP-D11](host-tool-provisioning.md#HP-D11)), and a caller with no PATH at all hands
the agent the floor installer's system baseline ([HP-D12](host-tool-provisioning.md#HP-D12)). yolo's
other checks already read the PATH yolo was started with, which is now the ruled behavior
([HE-DIR1](#he-dir1)).

**Built 2026-09-30, the rest:** the user-scope `host_path` key (`internal/config/hostpath.go`); the
one resolver, `hostpath.Resolve` (`internal/hostpath`), which every host PATH check asks
([HE-D5](#he-d5)); the agent's PATH with `host_path`'s folders between the caller's PATH and the
floor's `bin/` ([HE-D3](#he-d3)); the install the dependency gate runs, on the launch PATH
([HE-D6](#he-d6)); the miss line at every call site ([HE-D2](#he-d2)); and `yolo check`'s host
launch section ([HE-D7](#he-d7)). Where each is, and the test that pins its call site, is in the
[ledger](#10-decision-ledger).

> **In short.** yolo cannot know where a user keeps their tools except by reading the PATH it was
> handed. So at `yolo host` its checks read that PATH, `host_path` adds folders to it, and yolo
> guesses no others. What yolo guarantees, whoever starts it, is what yolo itself provides: the
> floor's agents and the floor's Node.

**Why it matters.** A Waybar-started job ran `yolo host -- opencode`. The provider keys arrived,
but `opencode` (installed through mise) was not found, because mise reaches PATH only through
`mise activate` in an interactive shell's rc file. Under [HP-DIR4](host-tool-provisioning.md#HP-DIR4)
that launch runs the floor's `opencode`, whatever the widget's PATH holds. What is left is a
program yolo does not provide, such as a tool a pack lists under `requires` that lives in
`~/.cargo/bin`. From the widget it reads missing wherever yolo checks it, and the miss now says
which PATH it searched and what to add ([HE-D2](#he-d2)). At launch that check is the launch
gate's, which runs only with `host_apply_on_launch` on (unset, it follows `host_wrappers`);
`yolo host apply` and `check-deps` always run it.

**The shape.**

- One resolver builds the *launch PATH*: the PATH yolo was started with, then each `host_path`
  folder not already on it ([§2.2](#22-the-launch-path-and-what-host_path-adds)).
- Every PATH check yolo makes at the host asks that resolver: the launch gate's dependency
  survey, `yolo host apply`, `check-deps`, the re-probe after an install, the package-manager
  guess behind a remedy, and a new host launch section in `yolo check`. The one exception is a
  program a selected pack delivers, which is checked by its floor entry.
- A delivered program, named bare, execs from the floor. A path execs as given. Anything else is
  looked up on the agent's PATH.
- The agent's PATH is the launch PATH with the floor's `bin/` last ([OQ-HE10](#oq-he10),
  [HE-D1](#he-d1)), so a check and the agent search the same folders.

**Cost.** A launcher with a bare PATH (a Waybar button, cron, a macOS hotkey launcher such as
Raycast) can get a different verdict from a terminal for a program yolo does not provide. HE-DIR1
accepts that: *"it's just not feasible to otherwise know these things."* The fix is one
`host_path` line, and the [miss line](#42-the-diagnostic-for-a-miss) prints it.

**Start at [§0](#0-the-governing-ruling)**, then [§2.2](#22-the-launch-path-and-what-host_path-adds).

**Needs your ruling:** none. Ruled: [HE-DIR1](#he-dir1), [OQ-HE0](#oq-he0) (revised for PATH by
HE-DIR1), [OQ-HE10](#oq-he10) (c), [OQ-HE11](#oq-he11) (a). Retired or answered by HE-DIR1:
[OQ-HE1](#oq-he1), [OQ-HE2](#oq-he2), [OQ-HE3](#oq-he3), [OQ-HE4](#oq-he4), [OQ-HE5](#oq-he5),
[OQ-HE7](#oq-he7), [OQ-HE8](#oq-he8), [OQ-HE9](#oq-he9). Answered by the implementation decision
[HE-D10](#he-d10), reversible: [OQ-HE6](#oq-he6) (API keys in the shell that started yolo).

**Reads with:**
- [`host-tool-provisioning.md`](host-tool-provisioning.md): the floor, which supplies every agent a
  selected pack delivers to a host launch ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)).
- [`host-agent-environment.md`](../reference/host-agent-environment.md): the host env channel and
  wrappers. Its "start from the current environment" stands for PATH too; this design appends
  `host_path`'s folders and the floor's `bin/` after the inherited PATH.
- [`report-tiers.md`](../reference/report-tiers.md#the-dependency-rule): the dependency rule, whose
  probe this design routes through one resolver.
- [`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-launch-gate): the launch
  gate, where the incident's probe ran.
- [`yolo-as-environment-manager.md`](yolo-as-environment-manager.md#the-full-closure): the input
  tiers this design uses.

---

## 0. The governing ruling

> ⚠ **Maintainer ruling, 2026-09-29 (evening), which REVISES the one below for PATH:** "I think that you need to go back and review that decision again. I thought we decided that we can pick up the path if it's there because it's just not feasible to otherwise know these things. … I think this is the second time you're bringing this back up, so it needs to be presented in a more prominent location also. I just don't see any way around it. And then I think we just get things from [PATH]." (The last word
> was dictated; speech-to-text wrote "pet" for PATH.)
>
> **What it means here** (the recorder's reading, not the maintainer's words): **at `yolo host`, yolo's
> checks read the PATH yolo was started with, when it has one.** Whether a
> program a pack needs is present (`rg` for the guardrails pack, a pack's `requires`), and which copy of a
> program no floor entry covers, are answered from that PATH, as they are today. `host_path`, when set, adds
> folders to it. The checks look in no other folder. A launcher that passes a bare PATH (a Waybar button, cron, a
> macOS hotkey launcher) can therefore get a different answer from a terminal, and that is accepted: *"it's
> just not feasible to otherwise know these things."* What stays fixed regardless of the launcher is what
> yolo itself provides: the floor's copy of each pack's agent ([HP-DIR4](host-tool-provisioning.md#HP-DIR4))
> and the floor's own Node.
>
> **Why this is here, first:** the doc had extended the 09-25 ruling to *"yolo's own checks never read the
> ambient PATH"*, which the maintainer never said, and asked a series of questions built on it twice
> ([OQ-HE1](#oq-he1) to [OQ-HE9](#oq-he9)). Each is restated or retired under this ruling
> ([HE-DIR1](#he-dir1)).

> **Maintainer, 2026-09-25:** *"`yolo host` should be as predictable an environment as possible,
> so we shouldn't depend on the env it was launched in unless there's a clear `YOLO_…` something
> config in there."*

**How the doc went wrong, from its own history.**

- **2026-09-25.** The doc recorded the ruling above ([OQ-HE0](#oq-he0)) and read it as covering
  yolo's own PATH checks. It proposed a fixed per-OS baseline plus `host_path` to replace the PATH
  yolo was started with, and filed [OQ-HE1](#oq-he1) to [OQ-HE9](#oq-he9) on top of that.
- **The same day's review already pointed the other way.** A tool the user put on PATH in their
  shell rc should reach a host agent *"just the same as it is outside"*. The doc filed it as
  [OQ-HE10](#oq-he10) and applied it to the agent's PATH only.
- **2026-09-29.** [HP-DIR2](host-tool-provisioning.md#HP-DIR2) (*"yolo host will let it use the
  node from the project. It will have a fallback of the floor"*) and [OQ-HE10](#oq-he10) (c) put
  the caller's PATH first for the agent. The same questions were then queued for a ruling a second
  time, and [HE-DIR1](#he-dir1) answered that the checks read that PATH too.

**What stands of the 2026-09-25 ruling.**

- **No folder of yolo's own guessing.** The 09-25 ruling superseded an earlier suggestion to
  append mise's shims directory to PATH "opportunistically," meaning whenever it exists. That
  still holds. HE-DIR1 names PATH as the source (*"we just get things from [PATH]"*), and a
  folder yolo adds whenever it exists would be a source of yolo's own.
- **A new input yolo reads to decide something** has to be named by config or by a `YOLO_*`
  variable. The shell reads already built stay. Three built checks read the shell that started
  yolo, and each can refuse a launch: the credential pre-flight, the region pre-flight and the
  [OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8) override check
  ([§1.2](#12-ambient-values-yolo-reads-to-decide-something)). The credential gate's delivery
  reads the shell too. They keep doing so ([OQ-HE6](#oq-he6), answered by [HE-D10](#he-d10),
  reversible): the agent is handed that shell, and the check reads what the agent is handed.

**What stays fixed whoever starts yolo**, because yolo provides it:

- the floor's copy of each agent a selected pack delivers ([HP-DIR4](host-tool-provisioning.md#HP-DIR4));
- the floor's own Node, which an npm agent starts on by absolute path, with mise stripped
  ([HP-DIR2](host-tool-provisioning.md#HP-DIR2) item 2).

**What follows the launcher**, by ruling:

- whether a program yolo checks for but does not provide is found ([HE-DIR1](#he-dir1));
- which copy of a program no floor entry covers runs, as when the user types it
  ([HP-DIR4](host-tool-provisioning.md#HP-DIR4), [OQ-HE11](#oq-he11));
- what the agent's own commands find: the caller's PATH, first ([OQ-HP7](host-tool-provisioning.md#OQ-HP7),
  [OQ-HE10](#oq-he10)).

**And by implementation decision, not by ruling:** whether a key, a region or an override
variable the launcher's shell exports counts ([HE-D10](#he-d10), reversible).

**Nothing here overturns a built ruling any more.** The doc once said it overturned two, for PATH:
[`host-agent-environment.md`'s execution flow](../reference/host-agent-environment.md#execution-flow)
step 3, *"start from the current environment,"* and `composeHostLaunch`'s comment in
`internal/cli/host.go`, which calls `os.Environ()` *"the user's own shell, which the agent should
otherwise inherit whole."* Both stand, PATH included. This design appends `host_path`'s folders
and the floor's `bin/` after the inherited PATH, and changes which copy of a delivered agent runs.
It removes nothing from the inherited environment.

Terms used throughout:

- **Ambient environment:** the environ the `yolo` process received from whoever started it
  (terminal, Waybar, systemd, cron, an IDE). Its PATH is the *ambient PATH*, the PATH yolo was
  started with.
- **Notch:** a position on yolo's confinement dial. The jail notch is a podman or Apple Container
  container, the macos-user notch is macOS Seatbelt under a dedicated account, and the host notch is
  `yolo host` ([`host-render-target.md`](host-render-target.md)).
- ***Launch PATH*** *(coined here, 2026-09-29)*: the PATH yolo's checks read at a host launch. It
  is the ambient PATH, then each `host_path` folder not already on it
  ([§2.2](#22-the-launch-path-and-what-host_path-adds)). It is not the agent's PATH, which is the
  launch PATH with the floor's `bin/` appended. Nor is it the PATH of the user's interactive shell,
  which yolo sees only when that shell started it.
- ***Composed host PATH*** *(coined here 2026-09-25, withdrawn 2026-09-29)*: the value the doc
  first proposed for yolo's checks in place of the ambient PATH, a fixed per-OS baseline plus
  `host_path`. HE-DIR1 withdrew it: there is no baseline, and `host_path` adds to the ambient PATH
  rather than replacing it. Ruled text that uses it, [OQ-HE10](#oq-he10)'s answer and
  [HP-D1](host-tool-provisioning.md#HP-D1) among it, now reads as "`host_path`'s folders".
- **Host agent floor**, or *the floor* (coined in
  [`host-tool-provisioning.md`](host-tool-provisioning.md#defined-terms)): the `program` binaries
  of the packs the user-scope config selects, which yolo installs into a host directory of its own,
  the *host prefix*, and runs from there. Its `bin/` holds one executable per provisioned agent
  and nothing else, no `node` or `npm`
  ([§3 there](host-tool-provisioning.md#3-the-host-prefix)).
- ***Delivers*** *(the rulings' word, defined here):* a selected pack *delivers* a program when the
  floor holds, or can provision, an entry for it on this machine. A program the floor can provision
  but has not yet is delivered: the launch installs it first
  ([HP-D3](host-tool-provisioning.md#HP-D3)). A selected pack's program is **not** delivered when
  the floor cannot hold it here: it is configured out of the floor
  ([OQ-HP1](host-tool-provisioning.md#OQ-HP1)'s "a floor of nothing"), handed to another
  provisioner by [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override, unpublished for this OS and
  architecture, or an installer agent on macOS before the host capture
  ([HP-D2](host-tool-provisioning.md#HP-D2)) ships. What runs for such a program is
  [OQ-HE11](#oq-he11)'s answer.

## 1. Inventory — what `yolo host` takes from its caller today

### 1.1 The whole environ, passed through

`(*hostComposition).environ()` returns `agentenv.Apply(os.Environ(), c.vars)`. The child gets the
complete ambient environment, with the composed variables overlaid on top.

- Composition order (`composeHostVars`): pack static env (`packload.EnvFold`), then `env_sources`
  (`config.ResolveEnvSourcesFull`), then provider variables (`packload.AgentEnv`), then removals.
- Composition reads the **user-scope config only** (`config.UserScopeConfigOrEmpty`). A workspace
  `yolo-jail.jsonc` is agent-editable and must never shape a host process.
- `yolo host env` prints only the delta (`hostEnvDelta`), never the ambient environ.

### 1.2 Ambient values yolo *reads to decide something*

This is the inventory as read on 2026-09-29, before the build. Its `PATH` rows for the target
lookup, the dependency probe, the package-manager guess and the install now go through the launch
PATH's resolver, as [§3](#3-one-authority--the-seam)'s table says under *After*. The wrapper checks
and the jail-launch lookups read the PATH yolo was started with, as before.

| Input | Reader | Decision it steers |
| :--- | :--- | :--- |
| `PATH` | `resolveHostTarget(os.Getenv("PATH"), cmd[0])` → `hostwrap.LookPathSkipping`, skipping `yoloManagedDirs()` | Which binary is exec'd; exit 127 on a miss |
| `PATH` | `depcheck.LookPath` (= `exec.LookPath`) via `depcheck.Check`/`Present` | Whether a pack's `program`/`requires` dependency is **missing**. Reached from the launch gate's survey (`hostApplyGate` → `applyHostSurveyed` → `probeHostDeps` → `resolveHostDeps`), from `yolo host apply`'s pre-flight, from `check-deps` (`configuredDepRequirements`) and from the post-install re-probe in `gateHostDeps` |
| `PATH` | `depcheck.detectManager`, which calls bare `exec.LookPath` for `brew`/`apt`/`dnf`/`pacman`, **not** the `LookPath` seam | Which package manager's hint a remedy names |
| `PATH` (and the whole environ) | `runDepInstallCommand`: `exec.Command("sh", "-c", cmd)` with the inherited environ | Where an accepted install lands, and so whether the re-probe finds it |
| `PATH` | `hostWrappersStatus`; `yolo check`'s wrapper section (`o.Getenv("PATH")` with `hostwrap.OnPath`/`Precedence`) | Whether the wrap dir is on PATH and ahead of the real binary |
| `PATH` | `yolo check`'s `Options.LookPath` (default `exec.LookPath`): runtime, nix, loophole, GPU and macOS tool probes; `describe.go`'s `resolvedMechanism`; `commands.go`'s `detectListingRuntime` | Jail-launch readiness, not the host launch. Out of scope: a jail launch keeps finding these on the PATH it was started with ([OQ-HE7](#oq-he7), retired) |
| Provider credentials | `launch.credentialGaps(os.Getenv)` indexes `environ()`, which is `os.Environ()` plus the composed variables, and only then asks that `getenv`, so the shell is read either way. The credential gate's `Fallback` in `composeHostVarsWith` is `os.LookupEnv`: it answers a key `env_sources` did not hydrate, and a pack's derive composes from that answer | Whether the launch refuses for a credential gap, and what the agent is handed: with `-p zai`, claude's derive sets `ANTHROPIC_AUTH_TOKEN` from `ZAI_API_KEY` wherever the gate found it ([OQ-HE6](#oq-he6)) |
| Provider region | `launch.regionGaps()`, over `environ()` | Whether the launch refuses for a missing region. A region the shell exports counts at `yolo host` by a built decision, [BR-D2](bedrock-plumbing.md#BR-D2) ([OQ-HE6](#oq-he6)) |
| A variable a selected pack declares it overrides | `launch.envOverrideLines(os.Getenv)`, the [OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8) check: a variable the shell exports counts as delivered, from `packload.FromLaunchEnv` | Whether the launch refuses because something beside a pack's pointer overrides it ([OQ-HE6](#oq-he6)) |
| `HOME` | `paths.Home()` (`$HOME`, then the passwd entry, then `/`), and `os.UserHomeDir` in `hostApplyGate` | Which config, state and home are used |
| cwd | `os.Getwd()` in `composeHostLaunch` and `hostEnv` | The workspace `env_sources` resolve against. `hostScopedEnvSources` drops relative entries |
| stdin TTY | `hostGateCanPrompt` | Whether the gate may prompt |
| `YOLO_*` | e.g. `paths.AllowMissingProvidersEnv`, `config.InJail` via `YOLO_VERSION` | Named dials. The 09-25 ruling allows these |

### 1.3 How the incident happened

Under Waybar the ambient PATH lacks mise's activation. The sequence:

1. The gate's survey marks `opencode` missing.
2. `PendingDecisions()` is therefore non-empty, so the gate prints the decisions, renders nothing,
   **and still launches**.
3. `resolveHostTarget` misses and the launch exits 127.

From a terminal, all three steps pass. `yolo check` stays silent in both cases, because it never
runs the pack dependency probe: `internal/cli/check` has no `depcheck` caller. So "doctor says
fine" was not even an answer to the same question.

**Under this design.** `opencode` is a program a selected pack delivers, so step 1 checks its floor
entry and step 3 runs the floor's copy, from Waybar as from a terminal
([HP-DIR4](host-tool-provisioning.md#HP-DIR4)). The same steps still happen to a `requires` tool
that only an rc file puts on PATH, and step 3 to a program no pack provides. Each miss now prints
the line in [§4.2](#42-the-diagnostic-for-a-miss), and `yolo check` gains the dependency probe
([§3](#3-one-authority--the-seam)).

## 2. Target model

### 2.1 The criterion — decision inputs versus carried variables

Every ambient variable falls into one of two classes:

- A ***decision input*** *(coined here)* is a variable yolo **reads** to decide something: what to
  exec, whether a dependency is present, whether to refuse, what to render.
- A ***carried variable*** *(coined here)* is one yolo only **hands to the child**.

The 09-25 ruling is about yolo's predictability, so:

- **A decision input must be composed or named.** It comes from config or from a `YOLO_*`
  variable, never from whoever launched yolo. **PATH is the exception, by ruling**
  ([HE-DIR1](#he-dir1)): yolo cannot otherwise know where a user's programs are. Credentials, a
  region and the override check's variables are the other exception: at the host the agent is
  handed the shell, and the check reads what the agent is handed ([HE-D10](#he-d10)).
- **A carried variable passes through unchanged.** `TERM`, `COLORTERM`, `DISPLAY`,
  `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`, `SSH_AUTH_SOCK`, `LANG`/`LC_*`
  and `TZ` describe the session the user asked the program to run in. A Waybar-started widget has
  to talk to that Waybar's display. Dropping these variables would make the child uniformly
  broken, not more predictable. yolo's own decisions do not vary with them, so passing them
  through does not breach the ruling. This matches the jail notch, which forwards `TERM`,
  `COLORTERM` and — when set — `NO_COLOR` explicitly (`internal/cli/run/assemble.go`).

**PATH falls in both classes, and both halves now read one value.**

- **yolo's checks read the launch PATH**, except that a program a selected pack delivers is
  checked by its floor entry, the copy that runs.
- **The child's PATH is the launch PATH, then the floor's `bin/`**, with duplicates removed and
  the first occurrence kept ([OQ-HE10](#oq-he10), ruled (c); the floor's place last is
  [HE-D1](#he-d1), and there is no baseline, [HE-D9](#he-d9)). The commands a host agent runs see
  the user's own environment
  ([OQ-HP7](host-tool-provisioning.md#OQ-HP7)).

So a check and the agent search the same folders. The only difference is the floor's `bin/`,
which holds only programs the checks answer from the floor anyway. The gap the doc first worried
about, yolo resolving against one PATH and exec'ing into another, is closed. Three exec rules sit
beside the checks:

- **A program a selected pack delivers, named bare, is looked up on no PATH.** It execs from the
  floor, and its own startup runs in the floor's set environment: for an npm agent, the floor's own
  `node` by absolute path, with mise stripped ([HP-DIR2](host-tool-provisioning.md#HP-DIR2) item
  2, [HP-DIR4](host-tool-provisioning.md#HP-DIR4)). Only a bare name (no `/`) picks the floor's
  copy.
- **A target given as a path is exec'd as given**, as `hostwrap.LookPathSkipping` honors one today
  (*"the user named a file"*). Its composition is still keyed on its base name, as today. So
  `yolo host -- ~/src/claude/dist/claude` runs that build, with the claude pack's environment and
  launch flags.
- **Any other target is looked up on the child's PATH**, so what yolo execs is what that PATH
  names first. That covers a selected pack's program the floor cannot hold, which also prints one
  line saying why the floor has no copy ([OQ-HE11](#oq-he11), ruled (a)).

How the criterion classifies the rest of [§1.2](#12-ambient-values-yolo-reads-to-decide-something):

- **Credentials are a decision input**, and so are a region and the variables the override check
  reads. At the host they keep being read from the shell that started yolo, beside what yolo
  composes ([OQ-HE6](#oq-he6), answered by [HE-D10](#he-d10)). A check that ignored the shell would
  refuse launches the agent would be served in.
- **cwd is an argument of the invocation**, not ambient state. It stays.
- **HOME stays.** A `HOME` that disagrees with the passwd entry is a deliberate act, and yolo
  honors `$HOME` everywhere through `paths.Home()`. Changing that is out of scope.
- **stdin TTY stays.** It is the interaction mode, and it already fails closed.
- **Output styling** (`NO_COLOR`, color detection) is cosmetic and exempt.

In the environment-manager closure tiers
([`yolo-as-environment-manager.md`](yolo-as-environment-manager.md#the-full-closure)), the ambient
PATH is an **Undeclared** input at the host notch: it participates and nothing names it. By
HE-DIR1 it stays there, and by [HE-D10](#he-d10) so do the keys, region and override variables
the shell exports. The folders `host_path` adds are **Declared-impure**: named in the user
config, with contents that are machine state.

### 2.2 The launch PATH, and what `host_path` adds

The launch PATH is, in this order:

1. **The ambient PATH**, its entries in their order, when it is set and not empty.
2. **Each `host_path` folder not already on it**, in written order ([HE-D3](#he-d3)).

Duplicates keep their first occurrence. When yolo was started with no PATH at all, the launch PATH
is `host_path`'s folders alone ([HE-D4](#he-d4)).

**Nothing else joins it.** There is no per-OS baseline ([OQ-HE1](#oq-he1), [OQ-HE8](#oq-he8)), no
folder a pack declares ([OQ-HE2](#oq-he2)), no typed entry for mise ([OQ-HE3](#oq-he3)), no
`YOLO_HOST_PATH` variable ([OQ-HE4](#oq-he4)) and no entry meaning "the ambient PATH here"
([OQ-HE5](#oq-he5)). Each was proposed for the withdrawn reading and is retired or answered by
HE-DIR1.

**With `host_path` unset, the launch PATH is the ambient PATH**, which is what every check reads
today. A user who never writes the key sees no change in a PATH check's verdict but one: a wrapper
in the wrap dir no longer counts as the program it wraps ([HE-D5](#he-d5)), a false *present* whose
launch would have exited 127 anyway, since the exec already skips that folder. A program a
selected pack delivers leaves the PATH checks altogether: its floor entry answers for it
([§3](#3-one-authority--the-seam)), and what that changes is in
[§4.1](#41-nothing-to-stage).

**`host_path` grammar.** `host_path` is a list of directory strings. Each is absolute, or starts
with `~/`, which is expanded against `paths.Home()`. Validation refuses:

- relative paths;
- `~user/`;
- any `$`: an entry that expanded a variable would read the launcher's environment a second time,
  behind a key that looks fixed;
- any `:`.

Degenerate cases:

- **An empty list** is the same as unset: the ambient PATH alone.
- **Duplicates** keep their first occurrence.
- **A declared folder that does not exist** is kept, and `yolo check` reports it as absent.
  Lookup skips it, as a shell does.
- **An entry validation refuses** reaches no PATH: the reader leaves it out. No host verb
  validates the config before it reads the key, so the miss line and `yolo check`'s host launch
  section each name every refused entry, with its fix where there is an obvious one (a `$HOME/`
  spelling becomes `~/`). Without that, a miss in the folder the user meant would ask them to add
  the folder they had already listed.

**Scope.** `host_path` is user-scope only, by the same construction as `host_wrappers`:

- The reader reads the user config directly.
- `validate.go` errors on a workspace-scope entry.
- The key is classified *Neither* in the config inheritance table (`internal/config/inherit.go`). A
  host PATH has no referent in a jail.

A workspace config is agent-editable, and a PATH entry there would let a cloned repository choose
the binary a host agent runs.

**What is excluded.** The wrap dir (`paths.WrapDir()`) is not added implicitly. Every lookup keeps
skipping `yoloManagedDirs()` whether or not the user lists it, so the recursion guard is unchanged
([HE-D5](#he-d5)).

**`yolo host env`** emits no PATH line. Its output is `eval`'d into the caller's own shell, where the
caller owns PATH, and exporting a value there would clobber the interactive shell's activation
hooks. `yolo check` is where the launch PATH is shown ([§3](#3-one-authority--the-seam)).

### 2.3 Tool managers — mise, and the "shim is not installed" problem

These mise facts come from mise's documentation. They were not measured here; see
[Appendix A](#appendix-a--evidence).

- mise's shims live in `<data dir>/shims`.
- On Unix, a shim is by default a link to the `mise` binary. At run time it selects a version from
  the config found by walking up from **the process's cwd**.
- A shim exists for a binary once *some* installed version provides that binary. So finding a shim
  does not mean the version the cwd selects is installed. Depending on mise settings, that version
  errors or auto-installs at run time.
- mise documents putting the shims directory on PATH as one way of activating mise:
  `mise activate --shims` is its shorthand for exactly that entry.

**What yolo does with mise at the host: nothing of mise's own** ([OQ-HE3](#oq-he3), answered by
HE-DIR1).

- A user whose shell activates mise has mise's folders on the PATH their terminal hands yolo. The
  checks see them from that terminal, and not from a launcher that never ran the activation.
- A user who wants them from every launcher names the folder in `host_path` as a plain string.
  That is the user's act, not yolo's ([HP-DIR3](host-tool-provisioning.md#HP-DIR3)).
- yolo runs no mise command at the host: not `mise which`, not `mise env` or `mise activate`, and
  not `mise install` ([HP-DIR3](host-tool-provisioning.md#HP-DIR3),
  [OQ-HP6](host-tool-provisioning.md#OQ-HP6)).

> [!WARNING]
> **A shim is found the way a shell finds it.** A check that finds `rg`'s shim reads *present*
> even when the version the cwd's mise config selects is not installed. The agent's first `rg`
> call then gets mise's own error, or mise installs the version, depending on the user's mise
> settings: the same as when the user types `rg` in that folder. The doc once proposed confirming
> each shim hit with `mise which`, through a typed `{"mise": "shims"}` entry. That is withdrawn
> with [OQ-HE3](#oq-he3)'s answer.

> [!WARNING]
> **Never run `mise env` or `mise activate` output.** It executes the user's mise config,
> including `[env]`, which is the directory-scoped credential channel
> [`host-agent-environment.md`](../reference/host-agent-environment.md#what-this-does-not-license)
> refuses.

Other managers need nothing either. Homebrew, npm's global prefix, pipx and `~/.local/bin` all put
real binaries in a fixed folder, which the user's shell usually puts on PATH, and a plain
`host_path` string names it for a launcher that lacks it.

## 3. One authority — the seam

**Rule.** At the host notch, a program a selected pack [delivers](#0-the-governing-ruling) is
checked by its floor entry, the copy that runs. Every other PATH check yolo makes (a `requires`
tool, the package-manager guess, the re-probe after an install) resolves against the **launch
PATH**, computed **once per process** by one resolver ([HE-D5](#he-d5)). The resolver's package and
name were the implementer's choice; it is built as `hostpath.Resolve`, which returns a
`hostpath.Launch`. It returns:

- the ordered entries, the joined value, and where each entry came from (the ambient PATH or
  `host_path`);
- a `LookPath(bin)` that skips `yoloManagedDirs()`, as `resolveHostTarget` does for the exec
  today, so a wrapper in the wrap dir never reads as the program it wraps.

Three exec rules sit beside it, from [HP-DIR4](host-tool-provisioning.md#HP-DIR4) and
`hostwrap.LookPathSkipping`: a bare name (no `/`) of a program a selected pack delivers execs from
the floor by path; a target given as a path is exec'd as given; and any other target is looked up
on the child's PATH ([OQ-HE10](#oq-he10)), as the user's shell would find it.

The consumers:

| Consumer | Today | After |
| :--- | :--- | :--- |
| `hostExec` target | `resolveHostTarget(os.Getenv("PATH"), …)` | A bare name (no `/`) of a program a selected pack delivers: its floor entry, by path, whatever the caller's PATH holds. A target given as a path: exec'd as given, as `LookPathSkipping` honors one today; it still gets the composition its base name keys. Any other program: `resolveHostTarget(<child PATH>, …)`, the value the next row builds; for a selected pack's program the floor cannot hold, with one line saying why ([OQ-HE11](#oq-he11)). The skip list is unchanged |
| Child PATH | inherited | `environ()` overlays `PATH=<launch PATH>:<floor bin/>`, duplicates removed with the first kept ([OQ-HE10](#oq-he10), ruled (c); [HE-D1](#he-d1)). It is overlaid last, after removals, so no pack env or profile can replace it |
| Dependency probe (gate survey, `yolo host apply`, `check-deps`, re-probe) | `depcheck.LookPath` = `exec.LookPath` | `depcheck` takes the lookup from its caller. A program a selected pack delivers is answered by its floor entry ([`host-tool-provisioning.md` §7](host-tool-provisioning.md#7-what-yolo-check-reports)); every other dependency by the resolver. The package-level `var` remains a test seam only |
| `detectManager` | bare `exec.LookPath` | the same lookup |
| `runDepInstallCommand` | the inherited environ | the inherited environ with `PATH` set to the launch PATH, so the re-probe reads the PATH the install ran with ([HE-D6](#he-d6)) |
| `yolo check` | never probes pack dependencies | gains a **host launch section**: the launch PATH's entries with their sources, each `host_path` folder's existence, each selected pack program's floor entry (naming any other copy it finds on the launch PATH as not run by `yolo host`), and every other dependency's resolution through the same lookup. It says which PATH it read ([HE-D7](#he-d7)) |

**In-jail**, `config.InJail()` makes the resolver return the process PATH unchanged. The jail's PATH
is already composed (`entrypoint.BootPath`), `host_path` is host-only, and an in-jail `check-deps`
asks about the jail.

**Exempt, by name.** The wrapper-precedence checks (`hostWrappersStatus` and `yolo check`'s wrapper
section) read the ambient PATH alone, without `host_path`. Their question is *about the caller's
shell*: "will a bare `claude` typed there reach the wrapper?" So is the wrapper body's own
`exec yolo`, which runs in the caller's shell.

**Forbidden.** No host-notch consumer may call `os.Getenv("PATH")` or bare `exec.LookPath` to make
a decision. Each goes through the resolver, so every check honors `host_path` the same way and no
two checks can disagree. The wrapper checks above are the only readers of the bare ambient PATH.

## 4. What changes, and when

### 4.1 Nothing to stage

**The PATH half changes no check that works today**, so it ships whole, with no report-first stage
and no migration notice ([HE-D8](#he-d8)). With `host_path` unset, every check of a program no
floor entry covers reads the PATH it reads today.

**The floor half does change what runs, and a launch that works today can fail.** A delivered
agent named bare runs from the floor, not from the user's copy
([HP-DIR4](host-tool-provisioning.md#HP-DIR4)). Where the floor has no copy yet, the first such
launch installs it before the exec, with a terminal or without one
([HP-D3](host-tool-provisioning.md#HP-D3)). That install is bounded at 600 s, and it fails offline
or behind a proxy that blocks it
([§4 of the floor design](host-tool-provisioning.md#4-when-provisioning-runs)). So a
`yolo host -- claude` that runs `~/.local/bin/claude` today can fail the first time on a machine
with no network. That change is HP-DIR4's, and HP-D3 discloses the install on the launch line.
This design adds no notice of its own.

What a user sees change, from the first release:

- a program a selected pack delivers runs from the floor, not from a copy the user installed
  ([HP-DIR4](host-tool-provisioning.md#HP-DIR4));
- a selected pack's program the floor cannot hold runs from the child's PATH, with one line saying
  why the floor has no copy ([OQ-HE11](#oq-he11));
- the agent's PATH gains `host_path`'s folders and the floor's `bin/`, both after the caller's
  PATH, so nothing the caller's PATH already finds changes;
- a check or a target lookup that misses prints the line in [§4.2](#42-the-diagnostic-for-a-miss);
- `yolo check` gains its host launch section ([§3](#3-one-authority--the-seam)).

The doc first staged a switch of the checks from the ambient PATH to the composed host PATH:
report, then opt in, then default. HE-DIR1 retired the switch, and the stages with it
([OQ-HE9](#oq-he9)).

### 4.2 The diagnostic for a miss

**The miss line** *(coined here)* is one line yolo prints beside its existing verdict whenever a
PATH check or a target lookup at the host finds nothing: a `requires` tool, a selected pack's
program the floor cannot hold, or the target of `yolo host -- <cmd>` ([HE-D2](#he-d2)). It names the
program, the whole launch PATH it searched, and the fix. From a Waybar widget whose PATH is
`/usr/bin:/bin`, with `rg` required by the guardrails pack and installed in `~/.cargo/bin`:

```text
yolo host: rg (required by the guardrails pack) is not on this launch's PATH, /usr/bin:/bin, the PATH yolo was started with. If rg is installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.
```

Its rules:

- **The PATH is printed whole**, never truncated, because it is the answer to "where did it look".
  With `host_path` set, its folders are named as such: *"…, /usr/bin:/bin, the PATH yolo was
  started with, then host_path's ~/.local/bin."* When yolo was started with no PATH, the line says
  so ([HE-D4](#he-d4)).
- **The last clause appears only when a hint location holds the program.** ***Hint locations***
  *(coined here)* are a compiled list of folders where tools commonly live outside a launcher's
  PATH: `~/.local/bin`, `~/.npm-global/bin`, `~/go/bin`, `~/.cargo/bin`, `~/.nix-profile/bin`,
  mise's default shims folder (`~/.local/share/mise/shims`), `/opt/homebrew/bin` and
  `/home/linuxbrew/.linuxbrew/bin`.
- **A hint location never resolves anything.** It changes only the line's text, never whether a
  check passes or what runs. That keeps it inside HE-DIR1: a check answers from PATH (*"we just
  get things from [PATH]"*) and from the folders the user names in `host_path`, and a hint
  location only helps the user write that line.
- **Where it prints:** the launch gate's report (beside its decision text, or on its own when the
  home's render is in sync), under `yolo host apply`'s dependency blocker (the dry run's, and the
  `--assert` prompt's), `check-deps`, `yolo check`'s host launch section, and the exec's exit 127,
  where it replaces *"not found in PATH (searched N directories, skipping yolo's own)"*. A program
  with no build for this host has no blocker to print it under, since nothing could install it, so
  `yolo host apply` prints its line just above the verdict. Once per missing program per run: the
  gate leaves the program the launch starts to the exec's own line.
  Where a launch prints it, the PATH is *this launch's*; where a verb only checks, it is *the PATH
  yolo searched* ([HE-D2](#he-d2)).
- **At launch, only the gate surveys dependencies.** The gate runs only with
  `host_apply_on_launch` on, and unset that key follows `host_wrappers`. With it off, a launch
  checks no `requires` tool, so a missing one prints nothing at launch, as today, and the agent's
  first call to it fails. The exec's miss for the target prints either way.
- **It is a disclosure on the launch stream**
  ([`report-tiers.md`](../reference/report-tiers.md#the-launch-stream)), so no flag hides it.
- **It prints on every miss.** yolo cannot tell a bare launcher from a terminal. From a terminal
  where the program is simply not installed, the dependency rule's install remedy still follows it,
  as today.

## 5. Tests that would pin it

Each test is chosen by the repo's question: **does it fail if I delete the call site?**

1. **Exec call site.** Drive `hostMain` with `hostSyscallExec` stubbed.
   - Setup: a selected pack declaring the program `tool`, with a provisioned floor entry for it,
     and an ambient `PATH` whose first hit for `tool` is a user's copy elsewhere. A second
     program, `other`, that no selected pack declares, has one copy on the ambient `PATH` and a
     different one in a `host_path` folder. A third, `extra`, is only in that folder.
   - Assert: `yolo host -- tool` execs the floor entry, not the user's copy. `yolo host -- other`
     execs the ambient hit, and `yolo host -- extra` the `host_path` copy. `yolo host -- <dir>/tool`,
     a path naming the user's copy, execs that file and still gets the `tool` pack's launch flags.
     In every case the `PATH` in the passed environ is the ambient PATH, then the `host_path`
     folders not already on it, then the floor's `bin/`.
   - It fails if `hostExec` looks a delivered agent up on a PATH, replaces a path-shaped target
     with the floor entry, or resolves any other target against a value other than the child's
     PATH. It fails if `environ()` stops overlaying PATH or puts `host_path` ahead of the
     caller's PATH.
2. **One PATH for every check.** Run one config through the gate survey, `yolo host apply`'s
   pre-flight, `check-deps` and `yolo check`'s host launch section. Use an ambient PATH lacking a
   required tool and a `host_path` folder holding it. Assert every caller reads it present. Drop
   `host_path`, and assert every caller reads it missing and prints the miss line. It fails if any
   caller stops passing the resolver's lookup, since bare `exec.LookPath` never sees `host_path`.
3. **Re-probe and installer agreement.** Stub `depInstallRun` to drop a binary into a `host_path`
   folder that is absent from the ambient PATH. Assert that the `--assert` run succeeds. Today it
   refuses with *"installing `<bin>` did not produce it"*, because the re-probe reads only the
   ambient PATH. Also assert the installer was handed `PATH` equal to the launch PATH
   exactly, with no floor `bin/`. Today's seam, `depInstallRun(cmd, out)`, carries no environ, so
   pinning this needs the seam to take the environ the install runs with.
4. **`detectManager` through the seam.** A manager present only in a `host_path` folder is
   detected. One present only outside the launch PATH is not.
5. **A wrapper never reads as its program.** A selected pack's program the floor cannot hold, the
   wrap dir first on the ambient PATH with that program's wrapper in it, and no other copy. Assert
   the check reads it missing and the exec exits 127 with the miss line, rather than finding the
   wrapper. It fails if the resolver's lookup drops `yoloManagedDirs()`.
6. **Scope.** A workspace-scope `host_path` is a validation error, and has no effect on the launch
   PATH even when validation is bypassed. The inheritance-table classification test covers the
   new key.
7. **Grammar.** Relative, `~user/`, `$`- and `:`-containing entries are refused.
   `host_path: []` gives the ambient PATH alone.
8. **The miss line.** From an ambient PATH of `/usr/bin:/bin`, with a required tool only in a hint
   location and `host_apply_on_launch` on: the gate survey, `yolo host apply`, `check-deps` and the exec's 127 each print the
   line naming the searched PATH, the `host_path` key and the hint folder. The hint copy never
   makes the check pass. With PATH unset, the line says yolo was started with no PATH. Deleting
   the call fails it.
9. **In-jail passthrough.** Under `config.InJail()` the resolver returns the process PATH unchanged.
10. **A selected pack's program the floor cannot hold** ([OQ-HE11](#oq-he11), ruled (a)). Setup: a
    selected pack declaring `tool`, the floor configured to exclude it, and a user's copy on the
    ambient `PATH`. Assert that `yolo host -- tool` execs the ambient copy, prints the disclosure
    line naming why the floor holds none, and still injects the `tool` pack's launch flags. It
    fails if a floor-less program exits 127 as a floor miss, or if the disclosure call is deleted.

## 6. Relation to the other notches

- **The jail and macos-user notches compose their PATH.**
  - The jail's PATH is `entrypoint.BootPath`, set by `execBash`. Its environ is the image `Env`
    plus explicit `-e` pairs.
  - macos-user launches through `/usr/bin/env -i` with the closed list `sandboxEnvPairs`, whose
    PATH is `macosuser.SandboxPath`. Everything else crosses in the session env file.
  - The host notch is the one notch that inherits, and by ruling it keeps inheriting: HE-DIR1 for
    yolo's checks, [OQ-HE10](#oq-he10) for the child. What it fixes, whoever launches it, is what
    yolo provides: the floor.
- **The contents should not be unified.** The host's real directories are not the jail's
  (`~/.yolo/bin/block` and `~/.yolo/bin/launch` do not exist on the host), and AGENTS.md already
  records that `SandboxPath` and `BootPath` disagree with nothing comparing them. What *is* shared is
  the discipline: each notch has **one** function that produces its PATH, and a test that pins its
  consumers to it.
- **There is no host baseline to compare with `SandboxPath`.** `SandboxPath` is the PATH of a
  sandbox account yolo builds; the host's launch PATH is whatever the user's launcher handed yolo.

## 7. Non-goals

- **An allowlist for carried variables.** The criterion in
  [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables) leaves them unchanged, and
  this design does not revisit that.
- **Jail-launch lookups** (podman, nix, `container`, `sandbox-exec` found through `Options.LookPath`
  and friends). A jail launch keeps finding them on the PATH it was started with
  ([OQ-HE7](#oq-he7), retired).
- **Folders yolo guesses.** Only the ambient PATH and `host_path` resolve anything. Hint locations
  change the text of a miss and nothing else.
- **Finding the `yolo` binary itself.** Waybar's own PATH has to find `yolo`, and a wrapper's `exec
  yolo` runs in the caller's shell.
- **Writing `host_path` for the user.** The miss line prints the entry; the user's config is the
  user's file (P3 in `host-agent-environment.md`).

## 8. Risks

- **A launcher's PATH decides a check.** A widget can read a tool missing that a terminal finds.
  HE-DIR1 accepts this. Mitigation: the miss line names the PATH it searched and the one-line fix.
- **`yolo check` can be green where a widget is red.** It reads the PATH of the shell it runs in,
  which is usually a terminal's. Mitigation: its host launch section says so, and the widget's own
  launch prints the miss line on its stderr.
- **A mise shim reads present when its selected version is missing.** See the warning in
  [§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem). The agent's call then
  behaves as the user's own would.
- **On a Mac, a launcher without `/opt/homebrew/bin` on its PATH gets remedies that name nix**,
  because `detectManager` does not find `brew` on that PATH either. `"host_path":
  ["/opt/homebrew/bin"]` fixes both, and the miss line names that folder, since Homebrew's prefix
  is a hint location.

## 9. Open questions

No question is open. Every question the 2026-09-25 reading raised is retired or answered by
[HE-DIR1](#he-dir1), except [OQ-HE6](#oq-he6), which the implementation decision [HE-D10](#he-d10)
answers. Each keeps its anchor and says why in a line.

### <a id="oq-he1"></a>✅ [`OQ-HE1`](#oq-he1) — what does an unset `host_path` resolve to once enforced? — **ANSWERED BY HE-DIR1, 2026-09-29**

It asked which fixed list an unset `host_path` would stand for once yolo's checks stopped reading
the PATH yolo was started with: (a) a per-OS baseline of system folders, or (b) that plus the common
per-user tool folders that exist.

> **Answer:** Neither. Answered by [HE-DIR1](#he-dir1): the checks never stop reading the PATH yolo
> was started with, so an unset `host_path` means that PATH alone, as today. Both lists would be
> folders yolo adds on its own, and HE-DIR1 names PATH as the source: *"we just get things from
> [PATH]."*

### <a id="oq-he2"></a>✅ [`OQ-HE2`](#oq-he2) — may a pack declare the directory its program installs into? — **ANSWERED BY HE-DIR1, 2026-09-29**

It asked whether a pack may name the folder its installer puts its program in, so a launch looks
there with no user config. After [HP-DIR4](host-tool-provisioning.md#HP-DIR4) the one case left
was a program the floor cannot hold, such as an installer agent on macOS before the host capture,
started from a launcher whose PATH lacks `~/.local/bin`.

> **Answer:** No. Answered by [HE-DIR1](#he-dir1): a launch's folders come from the PATH yolo was
> started with (*"we just get things from [PATH]"*) and from `host_path`, the user's own list. A
> pack's folder would be a third source, one the user did not name. The case shrinks as the prioritized macOS host capture ships ([OQ-HE11](#oq-he11)'s
> direction). Until then the user's one `host_path` line covers it, and the miss line prints it.

### <a id="oq-he3"></a>✅ [`OQ-HE3`](#oq-he3) — ship the `{"mise": "shims"}` typed entry, or plain directories only? — **ANSWERED BY HE-DIR1, 2026-09-29**

It asked whether `host_path` should take a typed mise entry that adds mise's shims folder and
confirms each shim hit with `mise which`, or plain folders only.

> **Answer:** Plain folders only. Answered by [HE-DIR1](#he-dir1): *"we just get things from
> PATH."* A check answers what the PATH resolves, as it does today, and the typed entry's default
> data dir would be a guess. With [HP-DIR3](host-tool-provisioning.md#HP-DIR3), which leaves the
> workspace's runtime (the version mise selects in the cwd) to the user, yolo runs no mise command
> to second-guess a shim. What a shim with a missing version does is in
> [§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem).

### <a id="oq-he4"></a>✅ [`OQ-HE4`](#oq-he4) — `YOLO_HOST_PATH`: exist at all; replace or prepend? — **RETIRED BY HE-DIR1, 2026-09-29**

It asked whether a `YOLO_HOST_PATH` variable should let a systemd unit or CI job use its own
folders without editing the user config, and whether it replaces `host_path` or goes in front of
it.

> **Answer:** Retired by [HE-DIR1](#he-dir1). A job that wants its own folders sets `PATH` in its
> unit, and yolo's checks now read that PATH as the agent does, so the variable has nothing left to
> do.

### <a id="oq-he5"></a>✅ [`OQ-HE5`](#oq-he5) — offer `{"inherit": "PATH"}`? — **RETIRED BY HE-DIR1, 2026-09-29**

It asked whether `host_path` should accept an entry that splices the PATH yolo was started with
into the list.

> **Answer:** Retired by [HE-DIR1](#he-dir1), which makes that PATH the start of every launch PATH.
> The entry would say what always happens.

### <a id="oq-he6"></a>✅ [`OQ-HE6`](#oq-he6) — does yolo keep looking for API keys in the shell that started it? — **ANSWERED BY IMPLEMENTATION DECISION HE-D10, 2026-09-29 (reversible)**

It asked whether `yolo host` should stop counting what the shell that started it exports. From a
terminal whose `.bashrc` exports `ZAI_API_KEY`, `yolo host -p zai -- claude` starts. A Waybar
button running the same command never read `.bashrc`, so the credential pre-flight refuses and
names where it looked. The choices were to keep counting the shell, to stop, or to stop after one
release of notice.

> **Answer:** Keep counting it, as today, for keys, for a region and for the override check's
> variables. It is decided as [HE-D10](#he-d10) rather than asked again, because the rulings and
> built decisions already in force leave one coherent answer:
>
> - **The check has to read what the delivery reads.** The jail's pre-flight states the rule
>   (`checkProviderCredentials`: *"the check and the delivery cannot disagree"*), and counts the
>   launch shell for credentials because a derive can relay a credential out of it.
> - **At `yolo host` the delivery reads the shell twice.** The agent inherits the shell whole
>   ([CN-D13](provider-credential-scope.md#7-decision-ledger), and the "start from the current
>   environment" that [§0](#0-the-governing-ruling) says stands). And the credential gate's
>   `Fallback` answers a key `env_sources` did not hydrate, so a pack's derive composes from it.
>   With `-p zai`, claude's derive sets `ANTHROPIC_AUTH_TOKEN` from the shell's `ZAI_API_KEY`
>   ([Appendix A](#appendix-a--evidence)).
> - **So stopping breaks something either way.** Stop only the check, and yolo refuses a launch
>   its delivery would serve. Stop the delivery too, and `yolo host -p zai -- claude` loses
>   `ANTHROPIC_AUTH_TOKEN`. The raw `ZAI_API_KEY` still passes through, but claude does not read
>   it. That reverses CN-D13 and the host half of [BR-D2](bedrock-plumbing.md#BR-D2). The region
>   and override checks ([§1.2](#12-ambient-values-yolo-reads-to-decide-something)) would still
>   read the shell unless they changed too.
> - **The 2026-09-25 ruling was said about PATH**, in reply to the suggestion to append mise's
>   shims folder ([§0](#0-the-governing-ruling)). Reading it as governing keys is the same
>   widening [HE-DIR1](#he-dir1) corrected for PATH. This bullet is the recorder's reading.
> - **It is HE-DIR1's shape.** yolo picks up the key the launcher's environment has. `env_sources`
>   is the named channel that works from any launcher, as `host_path` is for PATH. A refusal names
>   where yolo looked and says to put the key in one of those places.
>
> **Reversible.** If keys should stop following the launcher, that reopens this question with
> CN-D13, BR-D2's host half and the override check in scope. Nothing is built either way.

### <a id="oq-he7"></a>✅ [`OQ-HE7`](#oq-he7) — does the ruling extend to jail launches' host-side lookups? — **RETIRED BY HE-DIR1, 2026-09-29**

It asked whether the 2026-09-25 ruling should extend to the host side of a jail launch: `yolo`,
`yolo check`, `describe` and `commands` finding podman, nix and the macOS tools on the PATH they
were started with.

> **Answer:** Retired by [HE-DIR1](#he-dir1). `yolo host` itself reads the PATH it was started with
> now, so there is no rule about PATH to extend, and a jail launch keeps finding its tools as it
> does today. Nothing here extends the 2026-09-25 ruling to a jail launch's other reads either: that
> ruling names `yolo host`. So the jail indicator's reads of `TMUX` and kitty's variables, and the
> herdr label built the same way
> ([`herdr-integration.md`](../research/herdr-integration.md#43-option-2-the-launcher-tells-herdr-what-is-inside)),
> are governed by no ruling in this doc. This retirement settles PATH lookups only. For the herdr
> label, whether a jail launch may decide from its launcher's session variables is carried by
> [OQ-HR1](../research/herdr-integration.md#OQ-HR1): ruling it (A) or (B) accepts it. The tmux and
> kitty arms are built with no ruling. A question about them, if one is wanted, belongs to a
> jail-launch design.

### <a id="oq-he8"></a>✅ [`OQ-HE8`](#oq-he8) — the macOS baseline, and whether Homebrew is in it — **RETIRED BY HE-DIR1, 2026-09-29**

It asked which folders a macOS baseline holds (the files `path_helper` reads, or a compiled list),
and whether Homebrew's prefix is one of them.

> **Answer:** Retired by [HE-DIR1](#he-dir1): there is no baseline on any OS. A Mac launcher whose
> PATH lacks `/opt/homebrew/bin` misses brew's tools, and its remedies name nix because `brew` is not
> on that PATH either. `"host_path": ["/opt/homebrew/bin"]` fixes both, and the miss line names it
> ([§8](#8-risks)).

### <a id="oq-he9"></a>✅ [`OQ-HE9`](#oq-he9) — when does stage 3 flip the default? — **RETIRED BY HE-DIR1, 2026-09-29**

It asked when a later release would switch everyone from the PATH yolo was started with to the
composed host PATH.

> **Answer:** Retired by [HE-DIR1](#he-dir1): there is no switch. The checks read that PATH now and
> keep reading it, so there is nothing to stage ([HE-D8](#he-d8)).

### <a id="oq-he10"></a>✅ [`OQ-HE10`](#oq-he10) — is the composed value the child's whole PATH, or its prefix? — **RULED (c) 2026-09-29**

> ⚠ **Read with [HE-DIR1](#he-dir1).** This question was asked, and answered, under the reading
> HE-DIR1 withdrew. Below, the text says twice that yolo's checks never read the ambient PATH. They
> do read it, by HE-DIR1. It also describes a switch of the checks to the composed host PATH, and
> that switch is withdrawn too. The order (c) ruled stands: the caller's PATH first. What changes is
> what came after it. The "composed value" was a per-OS baseline plus `host_path`, and the baseline
> is gone ([OQ-HE1](#oq-he1), [OQ-HE8](#oq-he8)). So the child's PATH is the launch PATH, then the
> floor's `bin/` ([§2.2](#22-the-launch-path-and-what-host_path-adds)). The one thing (c)'s text
> promised that the child no longer gets is the baseline filling in for a bare launcher. That
> consequence is its own decision, [HE-D9](#he-d9).

Raised by the maintainer in review, 2026-09-25. yolo can't require a particular outside PATH to run
at all, and what it provides has to be there whoever launched it. Yet a tool the user put on PATH in
their shell rc should reach a host agent "just the same as it is outside".

**Narrowed 2026-09-29 by principle [HP-DIR3](host-tool-provisioning.md#HP-DIR3): option (a) is
ruled out, so the composed value cannot be the child's whole PATH.** (a) was the child getting the
composed value only, as [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables) was
first written. Under HP-DIR3, as [OQ-HP7](host-tool-provisioning.md#OQ-HP7) applied it, the
commands a host agent runs see the user's own shell environment, mise included, exactly as the user
would run them. [HP-DIR2](host-tool-provisioning.md#HP-DIR2) item 1 says the same for the
environment the agent's commands run in ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)'s reading):
the user's PATH, with the floor after it. A child PATH built only from the baseline plus
`host_path` would have yolo choosing the workspace's runtime. For example, it would drop the user's
mise-selected `node` from the `npm test` the agent runs. So the caller's PATH has to reach the
child. What yolo checks (whether a dependency is present) can still avoid the ambient PATH, so
[OQ-HE0](#oq-he0) is untouched. *(Withdrawn by [HE-DIR1](#he-dir1): the checks read the ambient
PATH.)* Which binary is exec'd is in the answer below.

**The remaining question.** Once the composed host PATH is enforced, which comes first in the
child's PATH: yolo's composed entries, or the caller's own PATH? Terms:

- The *composed value* is the [composed host PATH](#0-the-governing-ruling): the per-OS baseline
  of system directories plus `host_path`.
- The *ambient PATH* is the PATH yolo received from whoever started it, part of the
  [ambient environment](#0-the-governing-ruling).
- The *floor* is the [host agent floor](host-tool-provisioning.md#defined-terms): the selected
  packs' programs, installed into yolo's own prefix.

**Setup.** After the switch *(withdrawn by [HE-DIR1](#he-dir1))*, with `host_path` unset, a user's terminal PATH is
`~/.local/share/mise/shims:/usr/local/bin:/usr/bin:/bin`. The workspace's `mise.toml` pins node 22,
and `/usr/bin/node` is 18. From that terminal they run `yolo host -- opencode`, and opencode runs
`npm test`.

**(b) Composed first.** The child's PATH is the composed value followed by the ambient PATH, with
duplicates removed and the first occurrence kept. Here it starts with `/usr/local/bin:/usr/bin:/bin`,
so `npm test` runs on `/usr/bin/node` 18, which is not what the same command gets in the user's
terminal. This was the doc's leaning, and it matches how the 2026-09-25 review was summarized: the
composed PATH first, the caller's shell after it, for the agent.

**(c) Ambient first.** The child's PATH is the ambient PATH followed by the composed value, with
duplicates removed. `npm test` finds the mise shim and node 22, exactly as in the terminal, and the
baseline fills in for a launcher whose PATH lacks it (Waybar). *(There is no baseline now:
[HE-D9](#he-d9).)* This doc first rejected (c) because
an npm CLI's own `node` would resolve against the caller's PATH, which is the incident one process
down. HP-DIR2 item 2 now answers that for a delivered agent (one yolo runs from the floor): it
starts on the floor's own node, by absolute path, with mise stripped. A target no selected pack
delivers resolves its interpreter the way it does outside yolo.

**Leaning: (c).** [OQ-HP7](host-tool-provisioning.md#OQ-HP7)'s *"exactly as the user would run
them"* and HP-DIR2's order (the user's PATH first, the floor as a fallback) both describe it, and
HP-DIR2 item 2 removes the reason this doc rejected it.

This question left two things to decide: whether [OQ-HE5](#oq-he5)'s `{"inherit": "PATH"}` keeps
any use under (c) beyond decisions, and whether HP-DIR2 item 1 already meant the maintainer had
ruled (c). The answer settles both.

> **Answer:**
> **Ruled 2026-09-29, as leaned: (c)**, under [OQ-HP7](host-tool-provisioning.md#OQ-HP7) and the
> maintainer's ruling [HP-DIR4](host-tool-provisioning.md#HP-DIR4). The child's PATH is the
> ambient PATH, then the composed value, then the floor's `bin/`, with duplicates removed and the
> first occurrence kept. yolo's own checks still never read the ambient PATH (a program a
> selected pack delivers by its floor entry, everything else by the composed value), so
> [OQ-HE0](#oq-he0) holds. *(That sentence was the recorder's, and [HE-DIR1](#he-dir1) withdrew
> it: the checks read the ambient PATH. The warning under this heading says what stands.)*
>
> - **The child.** [OQ-HP7](host-tool-provisioning.md#OQ-HP7) ruled that the commands a host
>   agent runs see the user's own shell environment, *"exactly as the user would run them."* (c)
>   is that order.
> - **The target, which (c) alone left open.** HP-DIR4: `yolo host -- <agent>` runs the floor's
>   copy whenever a selected pack delivers that agent, from any launcher, and a copy the user
>   installed is not the one that runs. The maintainer: *"I thought the whole point of running
>   yolo host was to get the actual agent."* A program no selected pack delivers is looked up on
>   the child's PATH, as the user's shell would find it.
> - **HP-DIR2 item 1 did describe (c)**, for the environment the agent's commands run in. It was
>   never about which copy of a pack's agent runs; that is HP-DIR4's correction.
> - **`{"inherit": "PATH"}` now affects only yolo's checks**, since the child already starts with
>   the caller's PATH. Whether to offer it stays [OQ-HE5](#oq-he5)'s question.
>
> The floor's place last, after the composed value, is [HE-D1](#he-d1).
>
> **Superseded in part by [HE-DIR1](#he-dir1) (recorder's note, 2026-09-29):** see the warning
> under this heading. The order (c) stands; the checks read the ambient PATH; the baseline is gone
> from the child's PATH as from the checks ([HE-D9](#he-d9)); and [OQ-HE5](#oq-he5) is retired.

### <a id="oq-he11"></a>✅ [`OQ-HE11`](#oq-he11) — what runs for a selected pack's program the floor cannot hold? — **RULED (a) 2026-09-29**

Raised in review, 2026-09-29. [HP-DIR4](host-tool-provisioning.md#HP-DIR4) says
`yolo host -- <agent>` runs the floor's copy, and that a copy the user installed is not the one that
runs. Both assume the floor has a copy. It has none, and never will on this machine, for a selected
pack's program in four cases the rulings already allow:

- the floor is configured to exclude it ([OQ-HP1](host-tool-provisioning.md#OQ-HP1)'s answer: *"You
  could even configure a floor of nothing"*);
- [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override hands it to another provisioner, which keeps it
  out of the prefix ([`host-tool-provisioning.md` §6](host-tool-provisioning.md#6-the-seams));
- its vendor publishes no build for this OS and architecture, so no install is ever attempted
  ([§2 there](host-tool-provisioning.md#2-the-floor-what-it-contains-and-what-it-doesnt));
- it is an installer agent on macOS, whose host capture ([HP-D2](host-tool-provisioning.md#HP-D2))
  must be measured on a Mac before it ships.

This doc calls such a program *not delivered* ([§0](#0-the-governing-ruling)). The question is
which binary `yolo host -- <it>` execs. The launch's composition is not in question: it keys on the
command's base name, so the pack's profile, provider env and launch flags apply under every option.

**Setup.** Two users, neither with `host_path` set.

- A Mac user selects the claude pack. Their `claude` came from the vendor's installer, in
  `~/.local/bin`, which their shell rc puts on PATH. The day HP-DIR4 lands, the host capture has not
  shipped, so the floor holds no `claude`. Today `yolo host -- claude` works from their terminal.
- A Linux user selects the claude pack and prefers Homebrew's `claude`, which they say through
  [OQ-PS7](provisioner-sets.md#OQ-PS7)'s override (*"Claude from brew"*, the maintainer's own
  example there). Their terminal PATH has `/home/linuxbrew/.linuxbrew/bin`; a Waybar widget's PATH
  is `/usr/bin:/bin`.

**The options.**

- **(a) Look it up on the child's PATH, like a program no selected pack delivers, and say so.**
  The launch prints one line: *"the floor holds no `claude` here (<reason>); running `<path>`, found
  on this launch's PATH."* The Mac user's terminal launch works as today, and the brew user's
  terminal launch runs Homebrew's `claude`. From the widget the brew user's launch exits 127 with
  [§4.2](#42-the-diagnostic-for-a-miss)'s miss line, until `host_path` names
  `/home/linuxbrew/.linuxbrew/bin`; the `host_path` folders are in the child's PATH, so that one
  entry fixes the widget. But a terminal can still pick a different copy, because the caller's PATH
  comes first. It departs from HP-DIR4's "a copy the user installed is not the one that runs", in
  the one case where the floor has nothing to run instead.
- **(b) Look it up on the composed value alone, and say so.** The same copy from every launcher,
  as the withdrawn reading of [OQ-HE0](#oq-he0) asked of a check. But the Mac user's working
  `yolo host -- claude` stops working from their terminal until `host_path` names `~/.local/bin`,
  and the brew user's until it names `/home/linuxbrew/.linuxbrew/bin`.
- **(c) Refuse.** Exit 127, naming why the floor holds none and the remedy. HP-DIR4 read literally.
  The Mac user's working `yolo host -- claude` stops working the day HP-DIR4 lands, until the host
  capture ships, and a user who configured a floor of nothing can run no selected pack's agent
  through `yolo host`.

For the override case alone there is a fourth shape: the override chooses which provisioner fills
the floor's entry instead of removing it, so the floor records where Homebrew put `claude` and execs
that path. The brew user's program is then delivered, and falls outside this question. It needs
[OQ-PS7](provisioner-sets.md#OQ-PS7)'s override to name a location, which
[`provisioner-sets.md`](provisioner-sets.md) has not decided, so it is a refinement of (a), (b) or
(c) for that case, not a replacement for them.

Under every option, `yolo check` reports the program as **no floor entry**, with the reason
([`host-tool-provisioning.md` §7](host-tool-provisioning.md#7-what-yolo-check-reports)). Under (a)
and (b) the dependency probe checks it the way it checks a `requires` tool: under HE-DIR1, against
the launch PATH, the PATH it runs from. [§5](#5-tests-that-would-pin-it) test 10 pins the ruled
option.

**Leaning: (a), with the disclosure line.** In these four cases yolo has no copy of its own to run,
so HP-DIR4's "the floor's copy runs" has nothing to act on. (a) treats the program the way HP-DIR4
already treats one no selected pack delivers, and the disclosure line keeps the difference from
being silent. (b) and (c) break a launch that works today in exchange for a guarantee the floor
cannot give on that machine. This is a departure from HP-DIR4's words, which is why it is filed
here rather than settled in the body.


> **Answer:**
> **Ruled 2026-09-29, as leaned: (a)**, with a direction: *"74 A but let's also prioritize making
> this work on mac with a real capture. I can get my mac agent on the job."* A selected pack's program
> the floor cannot hold is looked up on the child's PATH like a program no selected pack delivers, and
> the launch prints one line saying the floor holds no copy and why. The macOS host capture
> ([HP-D2](host-tool-provisioning.md#HP-D2)) is prioritized, with its measurement done on the
> maintainer's Mac, so that on macOS this case shrinks to the configured-out and unpublished ones.

## 10. Decision Ledger

| Id | Date | Decision | Why it holds | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="he-dir1"></a>**HE-DIR1** | 2026-09-29 | **Maintainer ruling, revising [OQ-HE0](#oq-he0) for PATH:** *"we can pick up the path if it's there because it's just not feasible to otherwise know these things … I just don't see any way around it. And then I think we just get things from [PATH]."* The recorder's reading: yolo's checks at the host read the PATH yolo was started with, when it has one, plus `host_path` when set; they look in no other folder, and a bare-PATH launcher may get a different answer | The "checks never read the ambient PATH" extension of [OQ-HE0](#oq-he0) was the doc's, never the maintainer's. Under it, the rest of [OQ-HE1](#oq-he1) to [OQ-HE9](#oq-he9) are retired or answered, and [OQ-HE6](#oq-he6) is answered by [HE-D10](#he-d10) ([§9](#9-open-questions)) | **Built 2026-09-30.** `hostpath.Resolve` (`internal/hostpath`) is the launch PATH: the PATH yolo was started with, then `host_path` (`config.HostPathFolders`). Every host PATH check asks it ([HE-D5](#he-d5)) |
| <a id="oq-he0"></a>[**OQ-HE0**](#oq-he0) | 2026-09-25 | `yolo host` does not depend on the environment it was launched in unless a `YOLO_*` variable or explicit config names the dependence. This supersedes the opportunistic mise-shims append. **Revised for PATH by [HE-DIR1](#he-dir1)**: the PATH yolo was started with is read | Maintainer ruling ([§0](#0-the-governing-ruling)), said in reply to a suggestion about PATH. It still bars a folder or input of yolo's own guessing, and a new decision input needs config or a `YOLO_*` variable. The keys, region and override variables the shell exports keep counting at the host by [HE-D10](#he-d10) | **Built.** The launch PATH holds no folder of yolo's guessing (`hostpath.New`); a hint location changes only a miss line's text ([HE-D2](#he-d2)) |
| [**OQ-HE10**](#oq-he10) | 2026-09-29 | (c): the child's PATH is the ambient PATH, then `host_path`'s folders (the "composed value", now without a baseline), then the floor's `bin/`, duplicates removed. A bare name of a program a selected pack delivers execs from the floor by path; a path is exec'd as given; any other bare name is looked up on that child PATH | Answered by [OQ-HP7](host-tool-provisioning.md#OQ-HP7) (the agent's commands see the user's own environment) and the maintainer's ruling [HP-DIR4](host-tool-provisioning.md#HP-DIR4) (the floor's copy of a pack's agent runs, from any launcher). Its recorded "checks never read the ambient PATH" is superseded by [HE-DIR1](#he-dir1), and the baseline its option text put in the child's PATH is gone ([HE-D9](#he-d9)) | **Built.** `hostChildPath`: the launch PATH, then the floor's `bin/` (`TestHostChildPathIsTheCallersThenHostPathThenTheFloorsDeduplicated`; at the call site, `TestTheExecReadsHostPathAndMissesWithTheMissLine`). A caller with no PATH keeps [HP-D12](host-tool-provisioning.md#HP-D12)'s stand-in ([HE-D9](#he-d9)) |
| [**OQ-HE11**](#oq-he11) | 2026-09-29 | (a): a program the floor cannot hold runs from the child's PATH, and the launch says the floor holds no copy and why. The macOS host capture is prioritized | Keeps a Mac user's working `yolo host -- claude` working until [HP-D2](host-tool-provisioning.md#HP-D2) ships; departs from [HP-DIR4](host-tool-provisioning.md#HP-DIR4) only where the floor has nothing to run instead | **Built** with the floor (`resolveHostLaunchTarget`). The child's PATH it searches has `host_path`'s folders, and a miss prints the miss line naming the program's pack (`TestAProgramTheFloorCannotHoldIsFoundInHostPath`) |
| [**OQ-HE1**](#oq-he1) | 2026-09-29 | Answered by HE-DIR1: an unset `host_path` means the PATH yolo was started with, alone; no baseline and no per-user list | HE-DIR1: *"we just get things from [PATH]"*; either list would be folders yolo adds on its own | Nothing to build: an unset `host_path` is the ambient PATH alone (`hostpath.New`) |
| [**OQ-HE2**](#oq-he2) | 2026-09-29 | Answered by HE-DIR1: no pack declares a folder | A launch's folders come from the ambient PATH and `host_path` only | Nothing to build |
| [**OQ-HE3**](#oq-he3) | 2026-09-29 | Answered by HE-DIR1: `host_path` takes plain folders only; no typed mise entry, and no `mise which` confirmation of a shim | *"We just get things from PATH"*, with [HP-DIR3](host-tool-provisioning.md#HP-DIR3) | **Built.** `validateHostPath` takes plain folders only; yolo runs no mise command at the host |
| [**OQ-HE4**](#oq-he4) | 2026-09-29 | Retired by HE-DIR1: no `YOLO_HOST_PATH` | A job sets `PATH` itself, and the checks read it | Nothing to build: nothing reads a `YOLO_HOST_PATH` |
| [**OQ-HE5**](#oq-he5) | 2026-09-29 | Retired by HE-DIR1: no `{"inherit": "PATH"}` entry | The ambient PATH starts every launch PATH | Nothing to build |
| [**OQ-HE7**](#oq-he7) | 2026-09-29 | Retired by HE-DIR1: a jail launch keeps finding its tools on the PATH it was started with; nothing here governs a jail launch's other reads | The rule it asked to extend no longer holds for PATH at `yolo host` itself, and [OQ-HE0](#oq-he0) names `yolo host` | Nothing to build: a jail launch's lookups are unchanged |
| [**OQ-HE8**](#oq-he8) | 2026-09-29 | Retired by HE-DIR1: no macOS baseline, and no baseline on any OS | A baseline would be folders yolo adds on its own; `host_path` names Homebrew's prefix where a launcher lacks it | Nothing to build |
| [**OQ-HE9**](#oq-he9) | 2026-09-29 | Retired by HE-DIR1: no stage 3, and no stages | There is no switch of the checks to stage ([HE-D8](#he-d8)) | Nothing to build |
| <a id="he-d1"></a>**HE-D1** | 2026-09-29 | *Implementation decision under [OQ-HE10](#oq-he10):* the floor's `bin/` goes last in the child's PATH, after the launch PATH | HP-DIR4 puts the floor after the user's PATH. The floor holds only agent names, so last means a child's lookup of an agent reaches the floor only where nothing of the user's has one | **Built.** `hostChildPath`, the floor's `bin/` last |
| <a id="he-d2"></a>**HE-D2** | 2026-09-29 | *Implementation decision under HE-DIR1:* a PATH check or target lookup that misses prints the miss line: the program, the whole launch PATH searched (its `host_path` part marked), and the `host_path` fix, naming the folder when a hint location holds the program ([§4.2](#42-the-diagnostic-for-a-miss)). **The hint is found by a bounded look, and is a hint only, never a verdict:** one `stat` per folder of the compiled hint-location list, no recursion and no command run, for an executable of that name in a folder not already on the launch PATH. No check counts a program found there, and nothing runs it. Where a launch prints the line (the gate, the exec) it says *this launch's PATH*; where a verb only checks (`yolo host apply`, `check-deps`, `yolo check`) it says *the PATH yolo searched*. It names yolo's own folders the lookup skipped. It prints once per missing program per run: the gate leaves the program the launch starts to the exec's own line, and prints the others whether or not the home's render is in sync | HE-DIR1 accepts that a bare launcher can miss what a terminal finds. The line makes that miss self-explaining from the launcher's own output, and a hint location changes only its text. A miss in an in-sync home is still a miss ([§4.2](#42-the-diagnostic-for-a-miss): it prints on every miss), and a line printed twice for one program is noise | **Built.** `hostpath.Launch.MissLine`, with the hint from `hostpath.Launch.Hint` over `hostfloor.HintLocations`. Printed by the gate (`reportGateMisses`), under `yolo host apply`'s dependency blocker and as its JSON document's `miss` (`depBlockerGroups`), under `check-deps`' MISSING line, as the note of `yolo check`'s warning, and in place of the exec's lookup error (`resolveHostLaunchTarget`). `TestEveryHostCheckReadsHostPath` (the gate's in-sync branch among them), `TestHostExecLaunchesOverAnotherPacksMissingDependency` (the gate's decision branch), `TestTheExecReadsHostPathAndMissesWithTheMissLine`, `TestTheMissLineNamesTheProgramThePackThePathAndTheFix`, `TestHintIsAHintNeverAFolderOnThePath`. A selected pack's program whose vendor publishes no build for this host is no blocker, since nothing could install it, but it was looked up on the launch PATH and `yolo host -- <it>` runs what that PATH holds ([OQ-HE11](#oq-he11) (a)), so it gets the line too: beside `check-deps`' no-build line, above `yolo host apply`'s verdict in the default view (`printUnpublishedDepMisses`), from the gate, and as the note of `yolo check`'s no-build row (`TestAProgramWithNoBuildHereStillPrintsTheMissLine`, `TestCheckHostLaunchPathGivesAProgramWithNoBuildHereTheMissLine`). `yolo host apply --format json` carries a miss line only in a missing dependency's group. Each `host_path` entry the reader refused is named in the line with its fix (`hostpath.Launch.Refused`, from `config.HostPathRefusals`; `TestARefusedHostPathEntryIsNamedInTheMissLine`, `TestTheMissLineNamesEachRefusedHostPathEntry`), and in `yolo check`'s host launch section (`TestCheckHostLaunchPathNamesARefusedHostPathEntry`) |
| <a id="he-d3"></a>**HE-D3** | 2026-09-29 | *Implementation decision under HE-DIR1:* `host_path`'s folders go after the ambient PATH, in written order, duplicates keeping the first occurrence | HE-DIR1: `host_path` *adds* folders. After the ambient PATH, a folder fills in for a launcher that lacks it and never shadows what the caller's PATH already finds, the same order [OQ-HE10](#oq-he10) (c) ruled for the child | **Built.** `hostpath.New` (`TestTheLaunchPathIsTheAmbientPathThenHostPathsNewFolders`), and the child's PATH the same way (`hostChildPath`) |
| <a id="he-d4"></a>**HE-D4** | 2026-09-29 | *Implementation decision under HE-DIR1:* when yolo was started with no PATH, or an empty one, the launch PATH is `host_path`'s folders alone, and the miss line says yolo was started with no PATH | HE-DIR1's "when it has one", read literally; supplying a default PATH would be a guess | **Built** for the checks (`hostpath.New`; `TestWithNoPathTheChecksSearchHostPathAlone`). The child's PATH and the exec's lookup keep HP-D12's stand-in (`hostpath.Launch.Child`), and the exec's miss line names it ([HE-D9](#he-d9)) |
| <a id="he-d5"></a>**HE-D5** | 2026-09-29 | *Implementation decision:* one resolver computes the launch PATH once per process; every host PATH check goes through its lookup, which skips `yoloManagedDirs()` as the exec's does | Two readers of PATH can disagree, and a check that did not skip the wrap dir would read a wrapper as the program it wraps | **Built.** `hostpath.Resolve`, asked by `probeHostDeps` once per run (the gate's survey and `yolo host apply`, which record it for the install and the miss lines), by `hostExec` once per launch, by `checkDepsMain` and by `yolo check`. It is computed from its two inputs each time it is asked rather than cached; neither changes in a process's life, so every check reads one value. Its `LookPath` skips `hostpath.ManagedDirs`, which `yoloManagedDirs` returns. `depcheck.Check`, `depcheck.Present` and the package-manager guess take the lookup from the caller. `TestEveryHostCheckReadsHostPath`, `TestTheManagerGuessReadsHostPath`, `TestAWrapperNeverReadsAsItsProgram`, `TestEveryProbeReadsTheCallersLookup` |
| <a id="he-d6"></a>**HE-D6** | 2026-09-29 | *Implementation decision:* an install the dependency gate runs gets `PATH` set to the launch PATH, and the re-probe reads the same value | The re-probe then asks the PATH the installer ran with, so an install that lands in a `host_path` folder is found | **Built.** `depInstallEnviron`; the seam is `depInstallRun(cmd, env, out)`, and the re-probe is `depcheck.Present(bin, lp.LookPath)` (`TestTheInstallRunsWithTheLaunchPathAndTheReprobeReadsIt`) |
| <a id="he-d7"></a>**HE-D7** | 2026-09-29 | *Implementation decision:* `yolo check`'s host launch section reads the PATH of the shell it runs in, plus `host_path`, and says so in the section | A check run from a terminal cannot see a widget's PATH, and a green section must not read as a promise about every launcher | **Built.** `sectionHostLaunchPath` (`TestCheckHostLaunchPathSaysWhichPathItRead`); the floor section reads the same launch PATH, for the copy that runs where it has no floor entry (`TestCheckFloorSectionFindsANoFloorEntryProgramInHostPath`) and for the other copies it names as not run (`TestCheckFloorSectionNamesACopyInHostPathAsNotRun`) |
| <a id="he-d8"></a>**HE-D8** | 2026-09-29 | *Implementation decision under HE-DIR1:* no staging and no migration notice for the PATH half. The design ships whole | With `host_path` unset, every check of a program no floor entry covers reads the PATH it reads today, so no PATH verdict breaks that a notice would have warned about. The report-first stages existed only for the withdrawn switch. The floor half does change what runs: a delivered agent runs from the floor, and its first install can fail offline ([§4.1](#41-nothing-to-stage)). That change is [HP-DIR4](host-tool-provisioning.md#HP-DIR4)'s, and [HP-D3](host-tool-provisioning.md#HP-D3) discloses the install on the launch line | **Built** by staging nothing: no notice, and no stages |
| <a id="he-d9"></a>**HE-D9** | 2026-09-29 | *Implementation decision under HE-DIR1 and [OQ-HE10](#oq-he10) (c):* the child's PATH has no per-OS baseline either. It is the launch PATH, then the floor's `bin/`. **The cost, stated:** (c) as asked said the baseline "fills in for a launcher whose PATH lacks it (Waybar)". A bare launcher's child now gets only what its launcher handed yolo, plus `host_path`'s folders. For example, a Mac hotkey launcher's agent no longer finds `/opt/homebrew/bin` unless `host_path` names it | One PATH for the checks and the child ([§2.1](#21-the-criterion--decision-inputs-versus-carried-variables)), so a check never reads missing a tool the agent then finds, or the reverse. The baseline's contents were [OQ-HE1](#oq-he1)'s and [OQ-HE8](#oq-he8)'s open questions and never ruled, so (c) named a list nobody had decided. One `host_path` line fixes the checks and the child at once, and every delivered agent runs from the floor whatever the launcher's PATH holds ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)). Reversible: a baseline for the child alone would reopen [OQ-HE1](#oq-he1) and [OQ-HE8](#oq-he8) for the child | **Built** for a caller with a PATH (`hostChildPath`). A caller with no PATH at all keeps [HP-D12](host-tool-provisioning.md#HP-D12)'s stand-in, the floor installer's system folders ahead of `host_path`'s: a floor decision built before this doc was restated, which this build left as it was |
| <a id="he-d10"></a>**HE-D10** | 2026-09-29 | *Implementation decision, reversible, answering [OQ-HE6](#oq-he6):* at `yolo host`, the shell that started yolo keeps counting for the three checks that read it today (the credential pre-flight, the region pre-flight and the [OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8) override check) and for the credential gate's delivery. Nothing is built. A refusal names where yolo looked, the launch environment among them | The check reads what the delivery reads (`checkProviderCredentials`: *"the check and the delivery cannot disagree"*), and at the host the delivery is the inherited shell plus what yolo composes ([CN-D13](provider-credential-scope.md#7-decision-ledger), [BR-D2](bedrock-plumbing.md#BR-D2)). Stopping only the check refuses launches the delivery serves. Stopping the delivery too drops `ANTHROPIC_AUTH_TOKEN` for `yolo host -p zai -- claude` when `ZAI_API_KEY` is only in the shell. [OQ-HE0](#oq-he0) was said about PATH, so reading it as covering keys is the widening HE-DIR1 corrected (the recorder's reading) | Nothing to build: today's behavior |

## Appendix A — evidence

Every claim in [§1](#1-inventory--what-yolo-host-takes-from-its-caller-today) was read in the
working tree on 2026-09-25, and the ones this restatement leans on were re-read against
`303e0367` on 2026-09-29. They are cited by function, not by line.

- **`internal/cli/host.go`:**
  - `hostExec`'s order: `parseHostExecFlags`, `hostApplyGate`, `composeHostLaunch`, the
    `credentialGaps` check, `resolveHostTarget(os.Getenv("PATH"), cmd[0])`,
    `prepareOpenAIAuthHost`, `environ()`, `packload.ReleaseEmbedded`, `hostSyscallExec` (a `var`
    seam over `syscall.Exec`).
  - `resolveHostTarget` wraps `hostwrap.LookPathSkipping` with `yoloManagedDirs()`
    (`paths.GeneratedBinDir()`).
  - `environ()` is `agentenv.Apply(os.Environ(), c.vars)`.
  - `composeHostLaunch` takes the workspace from `os.Getwd()`.
  - Re-read 2026-09-29, for [OQ-HE6](#oq-he6): `hostExec` calls
    `launch.credentialGaps(os.Getenv)`; `credentialGaps` answers from `environ()`, then from that
    `getenv`; the credential gate's `ScopeInput` sets `Fallback: os.LookupEnv`; and the list the
    refusal quotes as consulted ends with `packload.FromLaunchEnv`, *"the environment yolo was
    launched from."*
  - Read 2026-09-29, for [OQ-HE6](#oq-he6)'s scope: before the credential check, `hostExec` runs
    `launch.envOverrideLines(os.Getenv)`, the [OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8) check, whose
    lookup answers `packload.FromLaunchEnv` for any variable that `getenv` finds. After it,
    `launch.regionGaps()` indexes `environ()`, and its comment says a region exported in the
    invoking shell *"counts here"*.
- **The credential gate's delivery** (read 2026-09-29, for [OQ-HE6](#oq-he6)):
  `CredentialScope.LookupFor` (`internal/packload/credentialscope.go`) answers from `env_sources`,
  then from `Fallback`, and `hydrateProviders` (`deriveenv.go`) hydrates each provider's `api_key`
  through it. `packs/claude/derive.lua` sets `ANTHROPIC_AUTH_TOKEN` from that `api_key`, and
  `packs/zai/pack.json` gives `zai` an `anthropic` endpoint and `api_key_env_name: ZAI_API_KEY`.
  - `hostWrappersStatus` uses `hostwrap.OnPath(os.Getenv("PATH"), …)`.
  - Read 2026-09-29, for [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables)'s
    exec rules and [OQ-HE11](#oq-he11): a host launch keys on the command's base name.
    `composeHostLaunchWith` sets `agent := filepath.Base(bin)`, and `composeHostVarsWith` resolves
    the profile (`effectiveHostProfiles`), the pack selection (`loadedHostPacks`), the provider env,
    the credential gate and any launch service for that agent. `injectHostLaunchFlags` calls
    `packload.InjectLaunchFlags`, which looks the flags up by `filepath.Base` of the command.
    `selectedPackInstalls` is the predicate for "a selected pack installs this name".
- **`internal/cli/run/providerpreflight.go`** (read 2026-09-29, for [OQ-HE6](#oq-he6)):
  `checkProviderCredentials`, the jail launch's refusal check, appends `packload.FromLaunchEnv` to
  its consulted list, and `profilechannel.go`'s credential gate falls back to `o.Getenv`.
- **`internal/hostwrap/hostwrap.go`:**
  - `Body` is `exec yolo host -- <bin> "$@"` under `#!/usr/bin/env bash`.
  - `LookPathSkipping` skips empty entries and requires an executable the caller may run. A target
    containing a separator is honored as-is (read 2026-09-29): *"the user named a file, not a PATH
    lookup, and second-guessing that would be surprising."* A miss reads *"not found in PATH
    (searched N directories, skipping yolo's own)"*, the text [§4.2](#42-the-diagnostic-for-a-miss)
    replaces.
- **`internal/cli/hostapplygate.go`:**
  - `hostApplyGate` is a no-op in-jail and opt-in through `HostApplyOnLaunchEnabled`.
  - It runs the survey under `hostApplyGateBudget` (one second).
  - When `PendingDecisions()` is non-empty, it reports and **returns true** (launches).
- **`internal/cli/hostapplysurvey.go`:** `PendingDecisions()` includes a missing declared
  dependency.
- **`internal/cli/applyhostdeps.go`, `applyhostdepgate.go`, `checkdeps.go`:**
  - `probeHostDeps` and `resolveHostDeps` call `depcheck.Check`.
  - `gateHostDeps` re-probes with `depcheck.Present` after `runDepInstallCommand`
    (`sh -c`, inherited environ).
  - `checkDepsMain` → `configuredDepRequirements` → `depcheck.Check`.
- **`internal/depcheck/depcheck.go`:**
  - `var LookPath = exec.LookPath`; `Present` goes through it.
  - `detectManager` calls `exec.LookPath` directly.
- **`internal/cli/check`:**
  - `Options.LookPath` defaults to `exec.LookPath` and `Options.Getenv` to `os.Getenv`.
  - No file references `depcheck` or pack dependency requirements.
- **Not built** (searched 2026-09-29): neither `host_path` nor `YOLO_HOST_PATH` appeared anywhere
  under `internal/`, `cmd/` or `packs/`. **Built 2026-09-30:** `host_path` is
  `internal/config/hostpath.go`, read by `internal/hostpath`; `YOLO_HOST_PATH` still appears nowhere,
  by [OQ-HE4](#oq-he4)'s retirement.
- **Other notches:**
  - `internal/entrypoint/boot.go`: `BootPath` and `execBash`.
  - `internal/macosuser/macosuser.go`: `LaunchArgv` (`env -i`), `sandboxEnvPairs`, `SandboxPath`.
  - `internal/cli/run/assemble.go`: `TERM`/`COLORTERM`/`NO_COLOR` forwarding.
- **Scope pattern:** `internal/config/hostwrappers.go` reads user scope directly, `validate.go` has a
  "user-scope only" error, and `inherit.go` classifies the `host_*` keys as *Neither*.
- **`packs/opencode/pack.json`:** `opencode` is a `program` with `via: "npm"` and brew and pacman
  install hints. On the incident machine it was installed through mise, which no declaration names.
- **mise behavior** (shim location, link-to-binary shims, cwd-based version selection): from mise's
  documentation. **Not measured in this jail.** It is the reason for
  [§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem)'s warning, and nothing is
  built on it.
  - Checked against mise's documentation on 2026-09-29: its Shims page lists
    `mise activate --shims` among the ways to load mise's context and calls it "a shorthand for
    adding the shims directory to PATH", and its settings name a shim's missing-version
    auto-install (`not_found_auto_install`).
