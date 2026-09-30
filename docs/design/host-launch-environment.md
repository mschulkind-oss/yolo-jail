---
title: "A host launch that does not depend on who launched it — composing `yolo host`'s PATH"
date: 2026-09-25
status: in-review
tags: [design, host, env, path, mise, depcheck, predictability]
summary: "`yolo host` resolves its target, probes pack dependencies and runs installers against whatever PATH its caller happened to hold, so the same command gives different verdicts from a terminal and from Waybar. Compose a host PATH from a fixed per-OS baseline plus a user-scope `host_path` declaration and resolve yolo's PATH checks against that one value; run, and check, a program a selected pack delivers by its host agent floor entry; hand the agent the caller's PATH with the composed value and the floor after it; stage the checks' change report-first."
---

# A host launch that does not depend on who launched it — composing `yolo host`'s PATH

> ⚠ **Read [§0](#0-the-governing-ruling) first.** On 2026-09-29 the maintainer ruled that yolo's checks at the host **read the
> PATH yolo was started with** ([HE-DIR1](#he-dir1)). This doc's title, summary and [OQ-HE1](#oq-he1) to
> [OQ-HE9](#oq-he9) were written for the opposite reading and are being restated.

**Status:** DESIGN, 2026-09-25 — a proposal; nothing built. Evidence read against the working tree
on that date. Two rulings of 2026-09-29 reshaped it. Principle
[HP-DIR3](host-tool-provisioning.md#HP-DIR3) says that at the host yolo manages the agent's
environment and never provisions or activates the workspace's runtime. The maintainer's ruling
[HP-DIR4](host-tool-provisioning.md#HP-DIR4) says `yolo host` runs the
[floor](#0-the-governing-ruling)'s copy of any agent a selected pack delivers, from any launcher.
Under them [OQ-HE10](#oq-he10) is answered (c): the caller's PATH comes first in the PATH the agent
is handed.
[OQ-HE1](#oq-he1) is narrowed to programs no selected pack delivers, and still needs a ruling.
[OQ-HE11](#oq-he11), raised in review the same day, asks what runs for a selected pack's program
the floor cannot hold.

> **In short.** `yolo host` should make every check against the environment it composes, not
> the one it inherited. PATH is the input where that fails today. Composing it from a fixed
> baseline plus a declared list, routing yolo's other PATH checks through that one value, and
> running a selected pack's agent from the [host agent floor](#0-the-governing-ruling) removes the
> "works from my terminal, not from Waybar" class of failure.

**Why it matters.** A Waybar-started job ran `yolo host -- opencode`. The provider keys arrived,
but `opencode` (installed through mise) was not found, because mise reaches PATH only through
`mise activate` in an interactive shell's rc file. The exec, the launch gate's dependency probe,
and `check-deps` each give a verdict that depends on the caller. `yolo check` does not ask the
question at all.

**The shape.** One resolver builds the *composed host PATH* from a baseline, `host_path` and
`YOLO_HOST_PATH`. A program a selected pack [delivers](#0-the-governing-ruling) is exec'd from the
floor by path and checked by its floor entry. Every other PATH check yolo makes at the host
(dependency probe, installer re-probe, `check-deps`, `yolo check`) asks that resolver instead of
reading `PATH`. The agent is handed the caller's PATH with the composed value and the floor after it
([OQ-HE10](#oq-he10)), and any other target is looked up on that same PATH.

**Cost.** yolo's own checks no longer see a tool that lives only in the caller's PATH, such as a
`requires` tool in `~/.cargo/bin` or behind mise's shims, until the user names its directory. The
selected packs' agents do not pay this: the floor supplies them, and a copy the user installed
(`~/.local/bin/claude`) is not the one that runs. Where the floor cannot hold a selected pack's
program, what runs is [OQ-HE11](#oq-he11). The agent's own commands keep the caller's PATH
first. The staging in [§4](#4-migration--report-first-then-enforce) exists to make the checks'
change visible before it breaks anything.

**Start at [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables).** It holds the
criterion that decides what is composed and what passes through; the rest follows from it.

**Needs your ruling:** [OQ-HE1](#oq-he1), [OQ-HE2](#oq-he2), [OQ-HE3](#oq-he3), [OQ-HE4](#oq-he4), [OQ-HE5](#oq-he5), [OQ-HE6](#oq-he6), [OQ-HE7](#oq-he7), [OQ-HE8](#oq-he8), [OQ-HE9](#oq-he9). Ruled: [OQ-HE0](#oq-he0), [OQ-HE10](#oq-he10) (c), [OQ-HE11](#oq-he11) (a).

**Reads with:**
- [`host-tool-provisioning.md`](host-tool-provisioning.md): the floor, which supplies every agent a
  selected pack delivers to a host launch ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)).
- [`host-agent-environment.md`](../reference/host-agent-environment.md): the host env channel and
  wrappers. This design overturns its "start from the current environment" for PATH.
- [`report-tiers.md`](../reference/report-tiers.md#the-dependency-rule): the dependency rule, whose
  probe this design re-points.
- [`host-apply-staleness.md`](../reference/host-apply-staleness.md#the-launch-gate): the launch
  gate, where the incident's probe ran.
- [`yolo-as-environment-manager.md`](yolo-as-environment-manager.md#the-full-closure): the input
  tiers this design uses.

---

## 0. The governing ruling

> ⚠ **Maintainer ruling, 2026-09-29 (evening), which REVISES the one below for PATH:** "I think that you need to go back and review that decision again. I thought we decided that we can pick up the path if it's there because it's just not feasible to otherwise know these things. … I think this is the second time you're bringing this back up, so it needs to be presented in a more prominent location also. I just don't see any way around it. And then I think we just get things from [PATH]." (The last word
> was dictated; speech-to-text wrote "pet" for PATH.)
>
> **So: at `yolo host`, yolo's checks read the PATH yolo was started with, when it has one.** Whether a
> program a pack needs is present (`rg` for the guardrails pack, a pack's `requires`), and which copy of a
> program no floor entry covers, are answered from that PATH, as they are today. `host_path`, when set, adds
> folders to it. Nothing else is guessed. A launcher that passes a bare PATH (a Waybar button, cron, a
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

Everything below is subordinate to that ruling. It **supersedes** an earlier suggestion to append
mise's shims directory to PATH "opportunistically," meaning whenever it exists. Any dependence on
ambient state has to be named, either by an explicit config key or by a `YOLO_*` variable.

**How far it reaches, after 2026-09-29.** The ruling stays whole for yolo's own PATH checks:
whether a pack's agent and its dependencies are present reads nothing the launcher chose. Whether a
launch refuses for a missing credential still reads the ambient environment
(`credentialGaps(os.Getenv)`) until [OQ-HE6](#oq-he6) rules. For the programs the selected packs
deliver, the floor is now what satisfies the presence check. `yolo host -- <agent>` runs the floor's
copy from any launcher ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)), so the answer is the same
from a terminal and from Waybar. Two later rulings bound what this ruling covers:

- The commands the agent runs see the user's own environment
  ([OQ-HP7](host-tool-provisioning.md#OQ-HP7)), so the caller's PATH reaches the child first
  ([OQ-HE10](#oq-he10)).
- A program no selected pack delivers resolves on the user's PATH as usual (HP-DIR4). Which copy of
  it runs is the user's business, as when they type it, and not a check of yolo's.

It also **overturns, for PATH**, a ruling that is built and documented:

- [`host-agent-environment.md`'s execution flow](../reference/host-agent-environment.md#execution-flow),
  step 3, says "start from the current environment."
- `composeHostLaunch`'s comment in `internal/cli/host.go` describes `os.Environ()` as "the user's
  own shell, which the agent should otherwise inherit whole."

Both remain true for the variables [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables)
classifies as carried, and for the start of the child's PATH, which is the caller's
([OQ-HE10](#oq-he10)). Neither remains true for the PATH yolo's own checks read.

Terms used throughout:

- **Ambient environment:** the environ the `yolo` process received from whoever started it
  (terminal, Waybar, systemd, cron, an IDE).
- **Notch:** a position on yolo's confinement dial. The jail notch is a podman or Apple Container
  container, the macos-user notch is macOS Seatbelt under a dedicated account, and the host notch is
  `yolo host` ([`host-render-target.md`](host-render-target.md)).
- ***Composed host PATH*** *(coined here):* the PATH value yolo builds for a host launch from the
  pieces in [§2.2](#22-how-the-composed-host-path-is-built), independent of the ambient PATH. Every
  PATH check yolo makes reads it and nothing else, except that a program a selected pack delivers is
  checked by its floor entry. The child's PATH carries it after the caller's ([OQ-HE10](#oq-he10)).
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
  [OQ-HE11](#oq-he11).

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

| Input | Reader | Decision it steers |
| :--- | :--- | :--- |
| `PATH` | `resolveHostTarget(os.Getenv("PATH"), cmd[0])` → `hostwrap.LookPathSkipping`, skipping `yoloManagedDirs()` | Which binary is exec'd; exit 127 on a miss |
| `PATH` | `depcheck.LookPath` (= `exec.LookPath`) via `depcheck.Check`/`Present` | Whether a pack's `program`/`requires` dependency is **missing**. Reached from the launch gate's survey (`hostApplyGate` → `applyHostSurveyed` → `probeHostDeps` → `resolveHostDeps`), from `yolo host apply`'s pre-flight, from `check-deps` (`configuredDepRequirements`) and from the post-install re-probe in `gateHostDeps` |
| `PATH` | `depcheck.detectManager`, which calls bare `exec.LookPath` for `brew`/`apt`/`dnf`/`pacman`, **not** the `LookPath` seam | Which package manager's hint a remedy names |
| `PATH` (and the whole environ) | `runDepInstallCommand`: `exec.Command("sh", "-c", cmd)` with the inherited environ | Where an accepted install lands, and so whether the re-probe finds it |
| `PATH` | `hostWrappersStatus`; `yolo check`'s wrapper section (`o.Getenv("PATH")` with `hostwrap.OnPath`/`Precedence`) | Whether the wrap dir is on PATH and ahead of the real binary |
| `PATH` | `yolo check`'s `Options.LookPath` (default `exec.LookPath`): runtime, nix, loophole, GPU and macOS tool probes; `describe.go`'s `resolvedMechanism`; `commands.go`'s `detectListingRuntime` | Jail-launch readiness, not the host launch. Out of scope here; see [OQ-HE7](#oq-he7) |
| Provider credentials | the provider `lookup` in `composeHostVars` falls back to `os.LookupEnv`; `launch.credentialGaps(os.Getenv)` | Whether a provider resolves, and whether the launch refuses for a credential gap |
| `HOME` | `paths.Home()` (`$HOME`, then the passwd entry, then `/`), and `os.UserHomeDir` in `hostApplyGate` | Which config, state and home are used |
| cwd | `os.Getwd()` in `composeHostLaunch` and `hostEnv` | The workspace `env_sources` resolve against. `hostScopedEnvSources` drops relative entries |
| stdin TTY | `hostGateCanPrompt` | Whether the gate may prompt |
| `YOLO_*` | e.g. `paths.AllowMissingProvidersEnv`, `config.InJail` via `YOLO_VERSION` | Named dials. The ruling allows these |

### 1.3 How the incident happened

Under Waybar the ambient PATH lacks mise's activation. The sequence:

1. The gate's survey marks `opencode` missing.
2. `PendingDecisions()` is therefore non-empty, so the gate prints the decisions, renders nothing,
   **and still launches**.
3. `resolveHostTarget` misses and the launch exits 127.

From a terminal, all three steps pass. `yolo check` stays silent in both cases, because it never
runs the pack dependency probe: `internal/cli/check` has no `depcheck` caller. So "doctor says
fine" was not even an answer to the same question.

## 2. Target model

### 2.1 The criterion — decision inputs versus carried variables

Every ambient variable falls into one of two classes:

- A ***decision input*** *(coined here)* is a variable yolo **reads** to decide something: what to
  exec, whether a dependency is present, whether to refuse, what to render.
- A ***carried variable*** *(coined here)* is one yolo only **hands to the child**.

The ruling is about yolo's predictability, so:

- **A decision input must be composed or named.** It comes from config or from a `YOLO_*`
  variable, never from whoever launched yolo.
- **A carried variable passes through unchanged.** `TERM`, `COLORTERM`, `DISPLAY`,
  `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`, `SSH_AUTH_SOCK`, `LANG`/`LC_*`
  and `TZ` describe the session the user asked the program to run in. A Waybar-started widget has
  to talk to that Waybar's display. Dropping these variables would make the child uniformly
  broken, not more predictable. yolo's own decisions do not vary with them, so passing them
  through does not breach the ruling. This matches the jail notch, which forwards `TERM`,
  `COLORTERM` and — when set — `NO_COLOR` explicitly (`internal/cli/run/assemble.go`).

**PATH falls in both classes.** yolo reads it to resolve the target and probe dependencies, and the
child reads it for every subprocess it spawns. The two halves are ruled separately:

- **yolo's checks never read the ambient PATH.** A program a selected pack delivers is checked by
  its floor entry, the copy that runs. Every other PATH check (a `requires` tool, `detectManager`,
  the installer re-probe) resolves against the composed value. That is the decision-input half, and
  the governing ruling covers it.
- **The child's PATH is the ambient PATH, then the composed value, then the floor's `bin/`**, with
  duplicates removed and the first occurrence kept ([OQ-HE10](#oq-he10), ruled (c); the floor's
  place last is [HE-D1](#he-d1)). That is the carried half: the commands a host agent runs see the
  user's own environment ([OQ-HP7](host-tool-provisioning.md#OQ-HP7)). The composed value fills in
  the system directories a bare launcher's PATH lacks.

This doc first wrote one rule for both halves: the composed value as the child's whole PATH, so
that yolo never resolves against one PATH and execs into another. An npm-installed CLI whose entry
script is `#!/usr/bin/env node` needs `node` on the child's PATH, and resolving it anywhere else
would move the incident one process down. The rulings close that gap differently:

- **A program a selected pack delivers, named bare, is looked up on no PATH.** It execs from the
  floor, and its own startup runs in the floor's set environment: for an npm agent, the floor's own
  `node` by absolute path, with mise stripped ([HP-DIR2](host-tool-provisioning.md#HP-DIR2) item
  2, [HP-DIR4](host-tool-provisioning.md#HP-DIR4)). Only a bare name (no `/`) picks the floor's
  copy.
- **A target given as a path is exec'd as given**, as `hostwrap.LookPathSkipping` honors one today
  (*"the user named a file"*). Its composition is still keyed on its base name, as today. So
  `yolo host -- ~/src/claude/dist/claude` runs that build, with the claude pack's environment and
  launch flags.
- **Any other target is looked up on the child's PATH itself**, so what yolo execs is what that
  PATH names first. Whether that also covers a selected pack's program the floor cannot hold is
  [OQ-HE11](#oq-he11)'s question.

What the rulings give up: a dependency yolo checked against the composed value can resolve to a
different copy in the child, because the caller's PATH comes first there. What that ambient part
supplies varies with the launcher, and no design can guarantee it.

How the criterion classifies the rest of [§1.2](#12-ambient-values-yolo-reads-to-decide-something):

- **Credentials are a decision input.** They steer `credentialGaps`. They stay an open question,
  [OQ-HE6](#oq-he6), because the migration cost is separate.
- **cwd is an argument of the invocation**, not ambient state. It stays.
- **HOME stays.** A `HOME` that disagrees with the passwd entry is a deliberate act, and yolo
  honors `$HOME` everywhere through `paths.Home()`. Changing that is out of scope.
- **stdin TTY stays.** It is the interaction mode, and it already fails closed.
- **Output styling** (`NO_COLOR`, color detection) is cosmetic and exempt.

In the environment-manager closure tiers
([`yolo-as-environment-manager.md`](yolo-as-environment-manager.md#the-full-closure)), the ambient
PATH is an **Undeclared** input: it participates and nothing names it. This design moves it to
**Declared-impure** for yolo's checks: named by `host_path`, with content that is machine state.
What reaches the child, and the lookup of a target no selected pack delivers, is carried like
`TERM`, by ruling ([OQ-HE10](#oq-he10)).

### 2.2 How the composed host PATH is built

The composed host PATH is the concatenation, in this order, of:

1. **The declared part**, which is the first of these that is set:
   - `YOLO_HOST_PATH`, when set and non-empty. It **replaces** `host_path` whole
     ([OQ-HE4](#oq-he4));
   - the user-scope `host_path` list, in written order.
2. **The baseline:** a fixed per-OS list of system directories. It is compiled into yolo and
   filtered by existence at compose time ([OQ-HE1](#oq-he1), [OQ-HE8](#oq-he8)).
   - Linux leaning: `/usr/local/sbin`, `/usr/local/bin`, `/usr/sbin`, `/usr/bin`, `/sbin`, `/bin`,
     plus NixOS's fixed profile directories when present (`/run/wrappers/bin`,
     `/run/current-system/sw/bin`, `/etc/profiles/per-user/<user>/bin`).
   - macOS leaning: the inputs `path_helper` reads (`/etc/paths` and `/etc/paths.d/*`), read as
     files.

The composed host PATH depends only on the machine, the user config and one named variable. Two
launches with the same user, machine and config get the same composed host PATH, whoever started
them. Machine state is allowed: the ruling is about the *launcher's* environment, not the
filesystem.

**`host_path` grammar.** `host_path` is a list. Each entry is one of:

- **A directory string.** Absolute, or starting with `~/`, which is expanded against `paths.Home()`.
  Validation refuses:
  - relative paths;
  - `~user/`;
  - any `$`, because variable expansion would reintroduce the ambient dependence;
  - any `:`.
- **`{"mise": "shims"}`.** Expands to `<mise data dir>/shims`, where the data dir is mise's
  default (`~/.local/share/mise`) unless the entry names one (`{"mise": "shims", "data_dir": "…"}`).
  It never takes the data dir from an ambient `MISE_DATA_DIR`. It also marks hits in that directory
  as shims for the probe ([§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem)).
  Whether to ship this typed entry at all is [OQ-HE3](#oq-he3).
- **`{"inherit": "PATH"}`.** Splices the ambient PATH in at that position. It is the explicit,
  greppable way to declare the dependence the ruling otherwise forbids. Whether to offer it is
  [OQ-HE5](#oq-he5).

Degenerate cases:

- **An empty list** means the baseline only, stated on purpose.
- **Unset** means the staged default ([§4](#4-migration--report-first-then-enforce)).
- **Duplicates** keep their first occurrence.
- **A declared directory that does not exist** is kept, and `yolo check` reports it as absent.
  Lookup skips it.

**Scope.** `host_path` is user-scope only, by the same construction as `host_wrappers`:

- The reader reads the user config directly.
- `validate.go` errors on a workspace-scope entry.
- The key is classified *Neither* in the config inheritance table (`internal/config/inherit.go`). A
  host PATH has no referent in a jail.

A workspace config is agent-editable, and a PATH entry there would let a cloned repository choose
the binary a host agent runs.

**What is excluded.** The wrap dir (`paths.WrapDir()`) is not added implicitly. Resolution keeps
skipping `yoloManagedDirs()` whether or not the user lists it, so the recursion guard is unchanged.

**`YOLO_HOST_PATH` grammar.** A colon-separated list of absolute directories. The typed entries are
config-only.

**`yolo host env`** emits no PATH line. Its output is `eval`'d into the caller's own shell, where the
caller owns PATH, and exporting a composed value there would clobber the interactive shell's
activation hooks. `yolo check` is where the composed host PATH is shown
([§3](#3-one-authority--the-seam)).

### 2.3 Tool managers — mise, and the "shim is not installed" problem

These mise facts come from mise's documentation. They were not measured here; see
[Appendix A](#appendix-a--evidence).

- mise's shims live in `<data dir>/shims`.
- On Unix, a shim is by default a link to the `mise` binary. At run time it selects a version from
  the config found by walking up from **the process's cwd**.
- A shim exists for a binary once *some* installed version provides that binary. So finding a shim
  does not mean the version the cwd selects is installed. Depending on mise settings, that version
  errors or auto-installs at run time.
- `mise which <bin>` resolves the real path for the version active in the cwd.
- `mise bin-paths` lists the active tools' real bin directories.

The options:

| Option | What it costs | Verdict |
| :--- | :--- | :--- |
| **A. Plain `host_path` entry naming the shims dir** | Nothing new. But the probe says *present* for a shim whose selected version is absent | Works with no code beyond the key. The probe lies in the one case mise makes easy |
| **B. `{"mise": "shims"}` typed entry: the static shims dir, plus a shim-aware probe** | When a lookup hits the shims dir, the probe confirms it with `mise which <bin>`. That command runs in the invocation's cwd for `yolo host`, in `paths.Home()` for `apply`/`check-deps`, and always with the composed environment. mise is found through the shim's own link target, so mise itself need not be on PATH. One exec per shim hit, inside the gate's existing one-second budget | **Recommended.** It keeps mise's per-directory versioning, which is the job mise earns on the host ([`host-agent-environment.md`](../reference/host-agent-environment.md#what-this-does-not-license)), and makes *present* mean installed |
| **C. `{"mise": "global"}`: run `mise bin-paths` from a fixed dir on every launch, and put real install dirs on PATH** | One mise exec per launch, on the widget's hot path. It needs mise on the composed PATH, loses per-project versions, and must scrub ambient `MISE_*` variables to stay deterministic | Rejected as the default: slower and less faithful. Stays available as a later typed entry |
| **D. `mise which` per target binary** | Answers only for the named binary. The child's own subprocesses (for example `node`) still miss | Useful only as the **diagnostic** in [§4.2](#42-the-diagnostic-for-a-miss), never as a PATH source |
| **E. Run `mise env` / `mise activate` output** | Executes the user's mise config, including `[env]`. That is the directory-scoped credential channel `host-agent-environment.md` refuses | Rejected |

Under option B, a shim whose `mise which` fails gets a distinct verdict, not *present*: **"shim
present; the version selected here is not installed,"** with the remedy `mise install`. The
dependency rule treats that verdict as missing.

Whether yolo runs that `mise install` itself, and whether it keeps agent installs of its own on the
host so that the floor a selected pack needs never depends on the user's mise, were
[`host-tool-provisioning.md`](host-tool-provisioning.md)'s questions. It ruled both: yolo never runs
it at the host ([OQ-HP6](host-tool-provisioning.md#OQ-HP6)), and the floor supplies the selected
packs' agents ([OQ-HP1](host-tool-provisioning.md#OQ-HP1),
[HP-DIR4](host-tool-provisioning.md#HP-DIR4)). This doc only resolves and reports.

Other managers need no typed entry. Homebrew, npm's global prefix, pipx and `~/.local/bin` all put
real binaries in a fixed directory, and a plain `host_path` string names it. A pack-declared install
directory would cover the shipped installers without user config; that is [OQ-HE2](#oq-he2).

## 3. One authority — the seam

**Rule.** At the host notch, a program a selected pack [delivers](#0-the-governing-ruling) is
checked by its floor entry, the copy that runs. Every other PATH check yolo makes (a `requires`
tool, `detectManager`, the installer re-probe) resolves against the **same composed host PATH
value**, computed **once per process** by one resolver. The resolver's package and name are the
implementer's choice; this doc calls it `hostpath.Compose`. It returns the ordered entries, the
joined value, where each entry came from (baseline, `host_path`, `YOLO_HOST_PATH`, inherit), and a
`LookPath(bin)` that knows the shim entries.

Three exec rules sit beside it, from [HP-DIR4](host-tool-provisioning.md#HP-DIR4) and
`hostwrap.LookPathSkipping`: a bare name (no `/`) of a program a selected pack delivers execs from
the floor by path; a target given as a path is exec'd as given; and any other target is looked up
on the child's PATH ([OQ-HE10](#oq-he10)), as the user's shell would find it.

The consumers:

| Consumer | Today | After |
| :--- | :--- | :--- |
| `hostExec` target | `resolveHostTarget(os.Getenv("PATH"), …)` | A bare name (no `/`) of a program a selected pack delivers: its floor entry, by path, whatever the caller's PATH holds. A target given as a path: exec'd as given, as `LookPathSkipping` honors one today; it still gets the composition its base name keys. Any other program, a selected pack's program the floor cannot hold included until [OQ-HE11](#oq-he11) rules: `resolveHostTarget(<child PATH>, …)`, the value the next row builds. The skip list is unchanged |
| Child PATH | inherited | `environ()` overlays `PATH=<ambient>:<composed>:<floor bin/>`, duplicates removed with the first kept ([OQ-HE10](#oq-he10), ruled (c)). It is overlaid last, after removals, so no pack env or profile can replace it. A pack wanting a PATH entry declares it, if ever, through [OQ-HE2](#oq-he2) |
| Dependency probe (gate survey, `yolo host apply`, `check-deps`, re-probe) | `depcheck.LookPath` = `exec.LookPath` | `depcheck` takes the lookup from its caller. A program a selected pack delivers is answered by its floor entry, the copy that runs ([`host-tool-provisioning.md` §7](host-tool-provisioning.md#7-what-yolo-check-reports)); every other dependency by the composed value. The package-level `var` remains a test seam only |
| `detectManager` | bare `exec.LookPath` | the same lookup |
| `runDepInstallCommand` | the inherited environ | the inherited environ with `PATH` set to the composed value alone: not the child's PATH, which starts with the caller's, so an accepted `npm install -g` runs the `npm` found on the same value the re-probe reads, not the caller's |
| `yolo check` | never probes pack dependencies | gains a **host launch** section: the composed entries with their sources, each declared entry's existence, each selected pack program's floor entry (naming any other copy it finds as not run by `yolo host`), and every other dependency's resolution through the same `LookPath`, including the shim verdict |

**In-jail**, `config.InJail()` makes the resolver return the process PATH unchanged. The jail's PATH
is already composed (`entrypoint.BootPath`), and an in-jail `check-deps` asks about the jail.

**Exempt from composition, by name.** The wrapper-precedence checks (`hostWrappersStatus` and `yolo
check`'s wrapper section) keep reading the ambient PATH. Their question is *about the caller's
shell*: "will a bare `claude` typed there reach the wrapper?" That is a report on the ambient
environment, not a decision made from it. So is the wrapper body's own `exec yolo`, which runs in
the caller's shell. So are the two places the ambient PATH reaches by ruling: the start of the
child's PATH, and the lookup of a target no selected pack delivers, which reads that child PATH
([OQ-HE10](#oq-he10), [HP-DIR4](host-tool-provisioning.md#HP-DIR4)). Neither decides whether one of
yolo's checks passes.

**Forbidden.** No host-notch consumer may call `os.Getenv("PATH")` or bare `exec.LookPath` to make
a decision. The exemptions above are the only readers of the ambient PATH.

## 4. Migration — report first, then enforce

### 4.1 Stages

What breaks when yolo's checks stop reading the inherited PATH: a dependency found only through an
ambient entry that the composed value does not contain. Two cases this list used to lead with no
longer break. A program a selected pack delivers runs from the floor, whatever copy the user has
in `~/.local/bin` ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)); one the floor cannot hold is
[OQ-HE11](#oq-he11)'s. The tools the child spawns still find the caller's PATH first
([OQ-HE10](#oq-he10)). The known cases are dependencies yolo probes that live in:

- an npm global prefix such as `~/.npm-global/bin`;
- mise shims or activated install dirs;
- `~/go/bin`, `~/.cargo/bin`, `~/.nix-profile/bin`;
- `/opt/homebrew/bin` on macOS, if not in the baseline ([OQ-HE8](#oq-he8)).

| Stage | Trigger | Resolution | What it prints |
| :--- | :--- | :--- | :--- |
| **1 — report** | `host_path` unset | Ambient PATH, as today, for every check the composed value will answer. Those checks' verdicts are unchanged | For each dependency yolo probes that the composed PATH would resolve **differently** (missing, or at another path), one stderr line: *"`rg` resolved from `/home/u/.cargo/bin`, an inherited PATH entry not named in `host_path`; add `"host_path": ["~/.cargo/bin"]` to `~/.config/yolo-jail/config.jsonc`."* `yolo check`'s host launch section shows the same findings |
| **2 — enforce, opt-in** | `host_path` set, or `YOLO_HOST_PATH` set | The composed host PATH for every check ([§3](#3-one-authority--the-seam)) | Only the miss diagnostic ([§4.2](#42-the-diagnostic-for-a-miss)) |
| **3 — enforce by default** | a later named release ([OQ-HE9](#oq-he9)) | The composed host PATH for every check, from the baseline alone when `host_path` is unset ([OQ-HE1](#oq-he1)) | The miss diagnostic |

Stage 1's notice is emitted on every launch it applies to, with no rate limit. A background job's
stderr is where its operator looks. The notice is a disclosure under the launch stream's rules
([`report-tiers.md`](../reference/report-tiers.md#the-launch-stream)), so no flag hides it.

**The stages stage the checks, and nothing else.** The exec and the child's PATH follow
[§3](#3-one-authority--the-seam) from the first release, independent of these stages. Two changes a
user can see therefore arrive at once, with stage 1: a program a selected pack delivers execs from
the floor rather than the user's copy ([HP-DIR4](host-tool-provisioning.md#HP-DIR4)), and every
child's PATH gains the composed value and the floor's `bin/` after the caller's. The check of a
delivered program by its floor entry goes with its exec, so it too applies from the first release:
checking the ambient PATH for a program the launch runs from the floor would report a copy that
does not run. What the stages hold back is the composed-value checks alone. With `host_path` unset
(stage 1), the composed value in the child's PATH is the one stage 3 will enforce: the baseline
alone under [OQ-HE1](#oq-he1)'s leaning. Stage 1's notice compares against that same value. Both
additions to the child's PATH follow the caller's PATH, so no stage changes what the caller's PATH
already finds.

There is **no hatch** beyond `host_path` itself. An `{"inherit": "PATH"}` entry ([OQ-HE5](#oq-he5))
is config, not a hatch: it names the dependence.

### 4.2 The diagnostic for a miss

A miss has two shapes, and each gets its own message. Both draw on a compiled list of
***hint locations*** *(coined here)*: the shims and installs under mise's default data dir,
`~/.local/bin`, `~/.npm-global/bin`, `~/go/bin`, `~/.cargo/bin`, `~/.nix-profile/bin`,
`/opt/homebrew/bin` and `/home/linuxbrew/.linuxbrew/bin`.

**A probe miss.** When a dependency is missing from the composed host PATH, the dependency rule
still applies to the probe. Before it does, yolo looks on the ambient PATH and at the hint
locations, then prints one of:

- **Found on the ambient PATH:** *"`rg` is not on the composed host PATH. The invoking PATH has it
  at `<path>`; add `<dir>` to `host_path`."*
- **Found at a hint location:** the same message, naming the location. For a mise install it
  suggests `{"mise": "shims"}`.
- **Found nowhere:** today's message, plus the composed entries so the reader can see what was
  searched.

**An exec miss.** When a target no selected pack delivers is missing from the child's PATH, yolo
still exits 127. That PATH starts with the ambient PATH, so a target missing from it is never on the
ambient PATH, and yolo consults the hint locations alone. It prints one of:

- **Found at a hint location:** *"`notes-sync` is not on this launch's PATH (the invoking PATH,
  then the composed entries). `~/.local/bin` has it; add `~/.local/bin` to `host_path`."* For a
  mise install it suggests `{"mise": "shims"}`.
- **Found nowhere:** today's message, plus the entries of this launch's PATH, so the reader can see
  what was searched.

**Hint locations never resolve anything.** They change only the text of a failure, never its
outcome. That keeps them outside the ruling. The gate survey's decision text uses the probe-miss
message, so the render-suppressing "missing dependency" and the exec's 127 both name `host_path`
as the remedy.

## 5. Tests that would pin it

Each test is chosen by the repo's question: **does it fail if I delete the call site?**

1. **Exec call site.** Drive `hostMain` with `hostSyscallExec` stubbed.
   - Setup: a selected pack declaring the program `tool`, with a provisioned floor entry for it,
     and an ambient `PATH` whose first hit for `tool` is a user's copy elsewhere. A second program,
     `other`, that no selected pack declares, has one copy on the ambient `PATH` and a different one
     in a directory `host_path` names.
   - Assert: `yolo host -- tool` execs the floor entry, not the user's copy, and
     `yolo host -- other` execs the ambient hit. `yolo host -- <dir>/tool`, a path naming the
     user's copy, execs that file, not the floor entry, and still gets the `tool` pack's launch
     flags. In all three, the `PATH` in the passed environ is the ambient PATH, then the composed
     value, then the floor's `bin/`, with duplicates removed.
   - It fails if `hostExec` looks a pack's agent up on a PATH, if it replaces a path-shaped target
     with the floor entry, if it resolves any other target against a value other than the child's
     PATH, or if `environ()` stops overlaying PATH or puts the composed value ahead of the
     caller's.
2. **Parity across launchers.** Run the same config through the gate survey, `check-deps` and
   `yolo check`'s host launch section twice: once with a minimal ambient environ (`HOME` and a
   one-entry `PATH`), once with a rich one containing an extra tool directory. Assert identical
   verdicts. This is the incident as a test. It fails if any of those callers stops passing the
   composed lookup.
3. **Re-probe and installer agreement.** Stub `depInstallRun` to drop a binary into a declared
   directory that is absent from the ambient PATH. Assert that the `--assert` run succeeds. Today it
   reads as a decline. Also assert the installer was handed `PATH` equal to the composed value
   exactly: no ambient entry, no floor `bin/`. It fails if `runDepInstallCommand` is given the
   child's PATH or the inherited one. Today's seam, `depInstallRun(cmd, out)`, carries no environ,
   so pinning this needs the seam to take the environ the install runs with.
4. **`detectManager` through the seam.** A manager present only on the ambient PATH is not
   detected.
5. **Shim verdict.** A fake shims dir whose link target is a stub `mise` that fails `which`. Assert
   the "shim present; version not installed" finding, and that the dependency rule treats it as
   missing.
6. **Scope.** A workspace-scope `host_path` is a validation error, and has no effect on composition
   even when validation is bypassed. The inheritance-table classification test covers the new key.
7. **Grammar.** Relative, `~user/`, `$`- and `:`-containing entries are refused. `YOLO_HOST_PATH`
   replaces rather than merges. `host_path: []` gives the baseline alone.
8. **Stage 1 notice.** With `host_path` unset and a probed dependency found only through an
   ambient entry, the probe still finds it **and** the launch prints the notice naming the entry.
   Deleting the notice call fails it.
9. **In-jail passthrough.** Under `config.InJail()` the resolver returns the process PATH unchanged.
10. **A selected pack's program the floor cannot hold** ([OQ-HE11](#oq-he11), pending its ruling).
    Setup: a selected pack declaring `tool`, the floor configured to exclude it, and a user's copy
    on the ambient `PATH`. Under the leaning, assert that `yolo host -- tool` execs the ambient
    copy, prints the disclosure line naming why the floor holds none, and still injects the `tool`
    pack's launch flags. It fails if a floor-less program exits 127 as a floor miss, or if the
    disclosure call is deleted. Rewrite the assertion to the ruled option when
    [OQ-HE11](#oq-he11) is answered.

## 6. Relation to the other notches

- **The jail and macos-user notches already obey the ruling.**
  - The jail's PATH is `entrypoint.BootPath`, set by `execBash`. Its environ is the image `Env`
    plus explicit `-e` pairs.
  - macos-user launches through `/usr/bin/env -i` with the closed list `sandboxEnvPairs`, whose
    PATH is `macosuser.SandboxPath`. Everything else crosses in the session env file.
  - The host notch is the one notch that inherits. This design closes that gap for yolo's own
    checks, and leaves the child's PATH starting with the caller's, by ruling
    ([OQ-HE10](#oq-he10)).
- **The contents should not be unified.** The host's real directories are not the jail's
  (`~/.yolo/bin/block` and `~/.yolo/bin/launch` do not exist on the host), and AGENTS.md already
  records that `SandboxPath` and `BootPath` disagree with nothing comparing them. What *is* shared is
  the discipline: each notch has **one** function that produces its PATH, and a test that pins its
  consumers to it.
- **macos-user's baseline and the host's are different questions.** `SandboxPath` is inside a
  sandbox account; the host baseline is the user's own machine. [OQ-HE8](#oq-he8) decides the latter
  alone.

## 7. Non-goals

- **An allowlist for carried variables.** The criterion in
  [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables) leaves them unchanged, and
  this design does not revisit that.
- **Jail-launch lookups** (podman, nix, `container`, `sandbox-exec` found through `Options.LookPath`
  and friends). They are the same class of problem, but a different command; see [OQ-HE7](#oq-he7).
- **Finding the `yolo` binary itself.** Waybar's own PATH has to find `yolo`, and a wrapper's `exec
  yolo` runs in the caller's shell.
- **Writing `host_path` for the user.** The diagnostic prints the entry; the user's config is the
  user's file (P3 in `host-agent-environment.md`).

## 8. Risks

- **A check and the child can disagree.** A dependency the composed value lacks reads as missing
  even when the agent's commands would find it on the caller's PATH, which comes first in the
  child's ([OQ-HE10](#oq-he10)). Mitigation: the miss diagnostic names the directory to add to
  `host_path`, stage 1 reports it before anything is enforced, `{"inherit": "PATH"}` if
  [OQ-HE5](#oq-he5) is accepted, and `yolo check` lists the composed entries.
- **Shim confirmation costs time on the gate's one-second budget.** On a budget overrun the survey
  reports "cannot determine" and launches (the existing disposition), so the worst case is
  today's behavior.
- **A macOS baseline read from `/etc/paths.d`** includes whatever installers dropped there. That is
  machine state, which is allowed, but it is not audited.

## 9. Open questions

### <a id="oq-he1"></a>💬 [`OQ-HE1`](#oq-he1) — what does an unset `host_path` resolve to once enforced? — **OPEN**

**Setup.** A user selects the claude pack and a pack that lists `rg` under `requires`. Their `rg`
is a hand-installed binary in `~/.cargo/bin`, which their shell rc puts on PATH. They also keep a
script of their own, `notes-sync`, in `~/.local/bin`; no pack delivers it, and a Waybar widget
whose PATH is `/usr/bin:/bin` runs `yolo host -- notes-sync`. They have never set `host_path`.
Today yolo checks and resolves both against the PATH it was handed: from a terminal both are
found, and from the widget `rg` reads as missing and `notes-sync` exits 127. After
[stage 3](#41-stages), yolo's checks read the composed host PATH, so an unset `host_path` has to
stand for some fixed list. This question is which list.

**What no longer turns on it.** `claude` runs from the [floor](#0-the-governing-ruling) under
every option, from any launcher, wherever the floor can hold it
([HP-DIR4](host-tool-provisioning.md#HP-DIR4); elsewhere, [OQ-HE11](#oq-he11)). Whatever the
caller's PATH holds, the agent's commands find it there first ([OQ-HE10](#oq-he10)). So the list
decides only programs no selected pack delivers, in three places:

- the dependency check of a `requires` tool such as `rg`, from every launcher, because the check
  reads the composed value alone;
- the target of `yolo host -- <cmd>` for a program no pack delivers, when the caller's PATH lacks
  it, as the widget's does;
- what the agent's own commands find when the caller's PATH lacks a directory, because the
  composed value follows the caller's PATH in the child's. From the widget, a launched claude's
  `rg` calls find only what the composed value names.

**The options.**

- **(a) Strict: the baseline alone**, the per-OS list of system directories in
  [§2.2](#22-how-the-composed-host-path-is-built). The check reports `rg` missing from every
  launcher, the terminal included, with a diagnostic naming `~/.cargo/bin` for `host_path`. From a
  terminal the agent's own `rg` calls still work, because the caller's PATH comes first in the
  child's. From the widget, the agent's own `rg` calls fail too, as they do today, until
  `host_path` names `~/.cargo/bin`. From the widget, `notes-sync` still exits 127, now with the same
  kind of diagnostic. One line, `"host_path": ["~/.cargo/bin", "~/.local/bin"]`, fixes all three.
- **(b) Generous without mise: the baseline plus the non-mise hint locations that exist.** Those
  are `~/.local/bin`, `~/.npm-global/bin`, `~/go/bin`, `~/.cargo/bin`, `~/.nix-profile/bin`,
  `/opt/homebrew/bin` and `/home/linuxbrew/.linuxbrew/bin`. `rg` reads present from every
  launcher, the widget's `notes-sync` runs, and from the widget the agent's own `rg` calls find
  `~/.cargo/bin/rg`. It is as deterministic as (a), because it depends on no launch environment.
  But it is a guess compiled into yolo and filtered by existence, the shape of the opportunistic
  append [OQ-HE0](#oq-he0) superseded.

**Leaning: (a) strict**, with [stage 1](#41-stages)'s notices as the migration path. The ruling's
objection to opportunism reads as an objection to yolo guessing, and a fixed guess is still a
guess. What strict costs has shrunk. The floor supplies the selected packs' agents, and the
caller's PATH still reaches the agent's commands. So strict costs a one-line `host_path` entry for
a `requires` tool outside the baseline, for a program no pack delivers that a bare-PATH launcher
starts, and for an agent a bare-PATH launcher starts whose own commands use such a tool.

**What narrowed it, 2026-09-29.**

- **No default may include mise's shims or install directories**, by principle
  [HP-DIR3](host-tool-provisioning.md#HP-DIR3). At the host, yolo manages the agent's environment
  and never the workspace's runtime; in the maintainer's words, *"we don't maintain the host
  development environment, the workspace's runtime."* The generous form as first written included
  the shims and installs under mise's default data dir.
  - A mise shim picks a version from the cwd's mise config, which is the workspace's runtime.
    Depending on mise's settings, it can also install that version at run time
    ([§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem)).
  - mise documents putting the shims directory on PATH as a way of activating mise: `mise activate
    --shims` is its shorthand for exactly that entry ([Appendix A](#appendix-a--evidence)). HP-DIR3
    and [OQ-HP6](host-tool-provisioning.md#OQ-HP6) forbid yolo activating mise, or installing
    through it, at the host.
  - The composed host PATH also reaches the agent's children, after the caller's PATH
    ([OQ-HE10](#oq-he10)). A default mise entry would therefore activate mise for the workspace's
    commands whenever the caller's PATH lacks it.
  - It is also the opportunistic mise-shims append that [OQ-HE0](#oq-he0) superseded.

  [HP-DIR2](host-tool-provisioning.md#HP-DIR2) item 1's "mise shims included" does not cut the
  other way. It describes the environment the agent's commands run in, where the user's own
  activation passes through (HP-DIR4's correction), and does not have yolo add mise. A user may
  still name mise's shims in `host_path` themselves, which is the user's act rather than yolo's; how
  they name it is [OQ-HE3](#oq-he3), still open. The mise locations stay in the miss diagnostic
  ([§4.2](#42-the-diagnostic-for-a-miss)), because a hint location only changes a message's text
  and never resolves anything.
- **Which copy of a pack's agent runs is settled.** This question used to flag that HP-DIR2 item 1
  (the user's PATH first, the floor as a fallback) put the user's own copy ahead of the floor's.
  HP-DIR4 answered the flag: the floor's copy runs, and item 1 was never about which copy.

<!-- vantage: oq id=OQ-HE1 leaning="(a) strict: an unset host_path resolves to the baseline alone once enforced, with stage 1's notices as the migration path. It now decides only programs no selected pack delivers: a requires tool yolo checks, from every launcher; a target a bare-PATH launcher starts; and what an agent a bare-PATH launcher starts finds when its own commands use such a tool, which under (a) fails until host_path names the directory. The floor supplies the selected packs' agents from any launcher (HP-DIR4), and the caller's PATH still comes first for the agent's commands (OQ-HE10). HP-DIR3 already ruled out any default with mise's shims or install dirs; the generous list without mise is still a guess compiled into yolo." -->

> **Answer:**

### <a id="oq-he2"></a>💬 [`OQ-HE2`](#oq-he2) — may a pack declare the directory its program installs into? — **OPEN**

The claude pack's installer lands in `~/.local/bin`. A pack-declared install directory would put it
on the composed host PATH with no user config, and the declaration would be named in the manifest.
But a fetched pack could then add PATH entries to host launches, which is the capability the
user-scope rule withholds from a workspace.

**Leaning:** not in this design. If adopted later, shipped packs only.

<!-- vantage: oq id=OQ-HE2 leaning="Not in this design: no pack declares the directory its program installs into, because a fetched pack could then add PATH entries to host launches, the capability the user-scope rule withholds from a workspace. If adopted later, shipped packs only." -->

> **Answer:**

### <a id="oq-he3"></a>💬 [`OQ-HE3`](#oq-he3) — ship the `{"mise": "shims"}` typed entry, or plain directories only? — **OPEN**

Option B in [§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem) against option A.

**Leaning: B.** It is the only option that keeps mise's per-directory versioning and makes the
probe's *present* true.

<!-- vantage: oq id=OQ-HE3 leaning="B: ship the typed mise-shims entry, not plain directories only. It is the only option that keeps mise's per-directory versioning and makes the probe's present true." -->

> **Answer:**

### <a id="oq-he4"></a>💬 [`OQ-HE4`](#oq-he4) — `YOLO_HOST_PATH`: exist at all; replace or prepend? — **OPEN**

It serves a systemd unit or CI job that wants a different path without editing the user config.

**Leaning:** it exists, and it **replaces** `host_path` whole. That way the composed value never
mixes two authorities, and `yolo check` names which one won.

<!-- vantage: oq id=OQ-HE4 leaning="YOLO_HOST_PATH exists, and it replaces host_path whole, so the composed value never mixes two authorities and yolo check names which one won." -->

> **Answer:**

### <a id="oq-he5"></a>💬 [`OQ-HE5`](#oq-he5) — offer `{"inherit": "PATH"}`? — **OPEN**

**Setup.** After [stage 3](#41-stages), the user from [OQ-HE1](#oq-he1)'s setup keeps `rg` in
`~/.cargo/bin`, where a pack's `requires` check looks for it. They would rather yolo's checks see
what their shell sees than name that directory, so they write `"host_path": [{"inherit": "PATH"}]`.
From a terminal the check then finds `rg`. From a Waybar widget whose PATH is `/usr/bin:/bin` it
reads missing.

The ruling permits a *named* dependence on the launch environment, and this entry is exactly that.
**Narrowed 2026-09-29 by [OQ-HE10](#oq-he10)'s ruling (c).** The child's PATH already starts with
the caller's, so the entry no longer changes what the agent or its commands find. Its only effect is
on yolo's own checks, which then follow the launcher. It is no longer an off-ramp to today's
behavior as a whole, only to today's checks.

- **Offer it.** The user's checks agree with what their terminal's agent finds, and Waybar and the
  terminal can disagree again. `yolo check` labels it as the one non-deterministic entry.
- **Don't.** The user names `~/.cargo/bin` in `host_path`, and every launcher gets the same verdict.

**Leaning: offer it.** It is config that names the dependence, which the ruling allows, and it is
the one way to keep a check in agreement with the terminal's agent without listing directories.

<!-- vantage: oq id=OQ-HE5 leaning="Offer the inherit-PATH entry. Since OQ-HE10 ruled (c) the child already starts with the caller's PATH, so the entry affects only yolo's own checks. It is the named dependence on the launch environment the ruling permits, and the one way to keep a check in agreement with what the terminal's agent finds without listing directories. yolo check labels it as the one non-deterministic entry." -->

> **Answer:**

### <a id="oq-he6"></a>💬 [`OQ-HE6`](#oq-he6) — does the ambient credential fallback survive? — **OPEN**

The provider `lookup` falls back to `os.LookupEnv`, and `credentialGaps` reads `os.Getenv`. So a
key exported in a shell rc resolves from a terminal and refuses under Waybar: the same class as the
incident. Removing the fallback makes the terminal refuse too, which is consistent but breaks
every `export ANTHROPIC_API_KEY` user.

**Leaning:** stage it the same way as PATH. Stage 1 reports "resolved from the invoking
environment; name it in `env_sources`," and enforcement comes with PATH's stage 3. The key itself
still reaches the child as a carried variable either way.

<!-- vantage: oq id=OQ-HE6 leaning="Stage the ambient credential fallback the same way as PATH: stage 1 reports that a key resolved from the invoking environment and should be named in env_sources, and enforcement comes with PATH's stage 3. The key itself still reaches the child as a carried variable either way." -->

> **Answer:**

### <a id="oq-he7"></a>💬 [`OQ-HE7`](#oq-he7) — does the ruling extend to jail launches' host-side lookups? — **OPEN**

`yolo`, `yolo check`, `describe` and `commands` resolve podman, nix and the macOS tools from the
ambient PATH.

**Leaning:** yes, in its own doc. It reuses this resolver's baseline, but it is a different
command and a different migration.

<!-- vantage: oq id=OQ-HE7 leaning="Yes, in its own doc: the ruling extends to the host-side lookups of jail launches. That doc reuses this resolver's baseline, but it is a different command and a different migration." -->

> **Answer:**

### <a id="oq-he8"></a>💬 [`OQ-HE8`](#oq-he8) — the macOS baseline, and whether Homebrew is in it — **OPEN**

Options: `path_helper`'s file inputs, or a compiled list. `/opt/homebrew/bin` is added by `brew
shellenv` in an rc file, not by `/etc/paths`. Yet `detectManager` prefers `brew` on darwin, and
without it in the baseline, remedies fall to `nix`.

**Leaning:** read `/etc/paths` and `/etc/paths.d`, and include Homebrew's fixed prefixes when
present. They are fixed locations, not launch environment.

<!-- vantage: oq id=OQ-HE8 leaning="Read /etc/paths and /etc/paths.d, and include Homebrew's fixed prefixes when present. They are fixed locations, not launch environment." -->

> **Answer:**

### <a id="oq-he9"></a>💬 [`OQ-HE9`](#oq-he9) — when does stage 3 flip the default? — **OPEN**

**Leaning:** setting `host_path` is the per-user opt-in (stage 2) from the first release. The
default flips in a later release named at the time. It does not flip on a timer, and not before
`yolo check`'s host launch section has shipped.

<!-- vantage: oq id=OQ-HE9 leaning="Setting host_path is the per-user opt-in (stage 2) from the first release. The default flips in a later release named at the time, not on a timer, and not before yolo check's host launch section has shipped." -->

> **Answer:**

### <a id="oq-he10"></a>✅ [`OQ-HE10`](#oq-he10) — is the composed value the child's whole PATH, or its prefix? — **RULED (c) 2026-09-29**

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
[OQ-HE0](#oq-he0) is untouched. Which binary is exec'd is in the answer below.

**The remaining question.** Once the composed host PATH is enforced, which comes first in the
child's PATH: yolo's composed entries, or the caller's own PATH? Terms:

- The *composed value* is the [composed host PATH](#0-the-governing-ruling): the per-OS baseline
  of system directories plus `host_path` ([§2.2](#22-how-the-composed-host-path-is-built)).
- The *ambient PATH* is the PATH yolo received from whoever started it, part of the
  [ambient environment](#0-the-governing-ruling).
- The *floor* is the [host agent floor](host-tool-provisioning.md#defined-terms): the selected
  packs' programs, installed into yolo's own prefix.

**Setup.** After [stage 3](#41-stages), with `host_path` unset, a user's terminal PATH is
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
baseline fills in for a launcher whose PATH lacks it (Waybar). This doc first rejected (c) because
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
> [OQ-HE0](#oq-he0) holds.
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
  [§4.2](#42-the-diagnostic-for-a-miss)'s exec-miss message, until `host_path` names
  `/home/linuxbrew/.linuxbrew/bin`; the composed value is in the child's PATH, so that one entry
  fixes the widget. But a terminal can still pick a different copy, because the caller's PATH comes
  first. It departs from HP-DIR4's "a copy the user installed is not the one that runs", in the one
  case where the floor has nothing to run instead.
- **(b) Look it up on the composed value alone, and say so.** The same copy from every launcher,
  as [OQ-HE0](#oq-he0) asks of a check. But the Mac user's working `yolo host -- claude` stops
  working from their terminal until `host_path` names `~/.local/bin`, and the brew user's until it
  names `/home/linuxbrew/.linuxbrew/bin`.
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
and (b) the dependency probe checks it against the composed value, as it checks a `requires` tool.
[§5](#5-tests-that-would-pin-it) test 10 pins the leaning, and is rewritten to the ruled option.

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

| Id | Date | Decision | Why it holds |
| :--- | :--- | :--- | :--- |
| <a id="he-dir1"></a>**HE-DIR1** | 2026-09-29 | **Maintainer ruling, revising [OQ-HE0](#oq-he0) for PATH:** yolo's checks at the host read the PATH yolo was started with, when it has one, plus `host_path` when set; nothing is guessed, and a bare-PATH launcher may get a different answer. *"It's just not feasible to otherwise know these things … I just don't see any way around it."* | The "checks never read the ambient PATH" extension of [OQ-HE0](#oq-he0) was the doc's, never the maintainer's; [OQ-HE1](#oq-he1) to [OQ-HE9](#oq-he9) are restated or retired under it |
| <a id="oq-he0"></a>[**OQ-HE0**](#oq-he0) | 2026-09-25 | `yolo host` does not depend on the environment it was launched in unless a `YOLO_*` variable or explicit config names the dependence. This supersedes the opportunistic mise-shims append, and for PATH it overturns "start from the current environment" | Maintainer ruling ([§0](#0-the-governing-ruling)). The same command must give the same verdict from a terminal, Waybar, systemd, cron and an IDE. Since 2026-09-29 it governs yolo's own checks; the child's PATH and a target no selected pack delivers follow [OQ-HE10](#oq-he10) |
| [**OQ-HE10**](#oq-he10) | 2026-09-29 | (c): the child's PATH is the ambient PATH, then the composed value, then the floor's `bin/`, duplicates removed. A bare name of a program a selected pack delivers execs from the floor by path; a path is exec'd as given; any other bare name is looked up on that child PATH. yolo's own checks never read the ambient PATH | Answered by [OQ-HP7](host-tool-provisioning.md#OQ-HP7) (the agent's commands see the user's own environment) and the maintainer's ruling [HP-DIR4](host-tool-provisioning.md#HP-DIR4) (the floor's copy of a pack's agent runs, from any launcher) |
| [**OQ-HE11**](#oq-he11) | 2026-09-29 | (a): a program the floor cannot hold runs from the child's PATH, and the launch says the floor holds no copy and why. The macOS host capture is prioritized | Keeps a Mac user's working `yolo host -- claude` working until [HP-D2](host-tool-provisioning.md#HP-D2) ships; departs from [HP-DIR4](host-tool-provisioning.md#HP-DIR4) only where the floor has nothing to run instead |
| <a id="he-d1"></a>**HE-D1** | 2026-09-29 | *Implementation decision under [OQ-HE10](#oq-he10):* the floor's `bin/` goes last in the child's PATH, after the composed value | HP-DIR4 puts the floor after the user's PATH, and the composed value stands in for the system directories and `host_path` entries the user's PATH would normally hold. The floor holds only agent names, so last means a child's lookup of an agent reaches the floor only where nothing of the user's has one |

## Appendix A — evidence

Every claim in [§1](#1-inventory--what-yolo-host-takes-from-its-caller-today) was read in the
working tree on 2026-09-25. They are cited by function, not by line.

- **`internal/cli/host.go`:**
  - `hostExec`'s order: `parseHostExecFlags`, `hostApplyGate`, `composeHostLaunch`, the
    `credentialGaps` check, `resolveHostTarget(os.Getenv("PATH"), cmd[0])`,
    `prepareOpenAIAuthHost`, `environ()`, `packload.ReleaseEmbedded`, `hostSyscallExec` (a `var`
    seam over `syscall.Exec`).
  - `resolveHostTarget` wraps `hostwrap.LookPathSkipping` with `yoloManagedDirs()`
    (`paths.GeneratedBinDir()`).
  - `environ()` is `agentenv.Apply(os.Environ(), c.vars)`.
  - `composeHostLaunch` takes the workspace from `os.Getwd()`. Its provider `lookup` tries the
    user env first, then `os.LookupEnv`.
  - `hostWrappersStatus` uses `hostwrap.OnPath(os.Getenv("PATH"), …)`.
  - Read 2026-09-29, for [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables)'s
    exec rules and [OQ-HE11](#oq-he11): a host launch keys on the command's base name.
    `composeHostLaunchWith` sets `agent := filepath.Base(bin)`, and `composeHostVarsWith` resolves
    the profile (`effectiveHostProfiles`), the pack selection (`loadedHostPacks`), the provider env,
    the credential gate and any launch service for that agent. `injectHostLaunchFlags` calls
    `packload.InjectLaunchFlags`, which looks the flags up by `filepath.Base` of the command.
    `selectedPackInstalls` is the predicate for "a selected pack installs this name".
- **`internal/hostwrap/hostwrap.go`:**
  - `Body` is `exec yolo host -- <bin> "$@"` under `#!/usr/bin/env bash`.
  - `LookPathSkipping` skips empty entries and requires an executable the caller may run. A target
    containing a separator is honored as-is (read 2026-09-29): *"the user named a file, not a PATH
    lookup, and second-guessing that would be surprising."*
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
- **Other notches:**
  - `internal/entrypoint/boot.go`: `BootPath` and `execBash`.
  - `internal/macosuser/macosuser.go`: `LaunchArgv` (`env -i`), `sandboxEnvPairs`, `SandboxPath`.
  - `internal/cli/run/assemble.go`: `TERM`/`COLORTERM`/`NO_COLOR` forwarding.
- **Scope pattern:** `internal/config/hostwrappers.go` reads user scope directly, `validate.go` has a
  "user-scope only" error, and `inherit.go` classifies the `host_*` keys as *Neither*.
- **`packs/opencode/pack.json`:** `opencode` is a `program` with `via: "npm"` and brew and pacman
  install hints. On the incident machine it was installed through mise, which no declaration names.
- **mise behavior** (shim location, link-to-binary shims, cwd-based version selection, `mise
  which`, `mise bin-paths`): from mise's documentation. **Not measured in this jail.** Measure it
  before building option B's confirmation step.
  - Checked against mise's documentation on 2026-09-29, for [OQ-HE1](#oq-he1): its Shims page
    lists `mise activate --shims` among the ways to load mise's context and calls it "a shorthand
    for adding the shims directory to PATH", and its settings name a shim's missing-version
    auto-install (`not_found_auto_install`).
