---
title: "A host launch that does not depend on who launched it — composing `yolo host`'s PATH"
date: 2026-09-25
status: in-review
tags: [design, host, env, path, mise, depcheck, predictability]
summary: "`yolo host` resolves its target, probes pack dependencies and runs installers against whatever PATH its caller happened to hold, so the same command gives different verdicts from a terminal and from Waybar. Compose the host PATH from a fixed per-OS baseline plus a user-scope `host_path` declaration, resolve every host-notch decision against that one value, and stage the change report-first."
---

# A host launch that does not depend on who launched it — composing `yolo host`'s PATH

**Status:** DESIGN, 2026-09-25 — a proposal; nothing built. Evidence read against the working tree
on that date.

> **In short.** `yolo host` should make every decision against the environment it composes, not
> the one it inherited. PATH is the input where that fails today. Composing it from a fixed
> baseline plus a declared list, and routing every host-notch lookup through that one value,
> removes the "works from my terminal, not from Waybar" class of failure.

**Why it matters.** A Waybar-started job ran `yolo host -- opencode`. The provider keys arrived,
but `opencode` (installed through mise) was not found, because mise reaches PATH only through
`mise activate` in an interactive shell's rc file. The exec, the launch gate's dependency probe,
and `check-deps` each give a verdict that depends on the caller. `yolo check` does not ask the
question at all.

**The shape.** One resolver builds the *composed host PATH* from a baseline, `host_path` and
`YOLO_HOST_PATH`. Every host-notch consumer (exec, dependency probe, installer re-probe,
`check-deps`, `yolo check`) asks that resolver instead of reading `PATH`.

**Cost.** A host launch no longer sees a tool that lives only in the caller's PATH. That covers
`claude` in `~/.local/bin`, an npm global prefix and mise shims, until the user names those
directories. The staging in [§4](#4-migration--report-first-then-enforce) exists to make that
visible before it breaks anything.

**Start at [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables).** It holds the
criterion that decides what is composed and what passes through; the rest follows from it.

**Needs your ruling:** [OQ-HE1](#oq-he1), [OQ-HE2](#oq-he2), [OQ-HE3](#oq-he3),
[OQ-HE4](#oq-he4), [OQ-HE5](#oq-he5), [OQ-HE6](#oq-he6), [OQ-HE7](#oq-he7), [OQ-HE8](#oq-he8),
[OQ-HE9](#oq-he9).

**Reads with:**
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

> **Maintainer, 2026-09-25:** *"`yolo host` should be as predictable an environment as possible,
> so we shouldn't depend on the env it was launched in unless there's a clear `YOLO_…` something
> config in there."*

Everything below is subordinate to that ruling. It **supersedes** an earlier suggestion to append
mise's shims directory to PATH "opportunistically," meaning whenever it exists. Any dependence on
ambient state has to be named, either by an explicit config key or by a `YOLO_*` variable.

It also **overturns, for PATH**, a ruling that is built and documented:

- [`host-agent-environment.md`'s execution flow](../reference/host-agent-environment.md#execution-flow),
  step 3, says "start from the current environment."
- `composeHostLaunch`'s comment in `internal/cli/host.go` describes `os.Environ()` as "the user's
  own shell, which the agent should otherwise inherit whole."

Both remain true for the variables [§2.1](#21-the-criterion--decision-inputs-versus-carried-variables)
classifies as carried. Neither remains true for PATH.

Terms used throughout:

- **Ambient environment:** the environ the `yolo` process received from whoever started it
  (terminal, Waybar, systemd, cron, an IDE).
- **Notch:** a position on yolo's confinement dial. The jail notch is a podman or Apple Container
  container, the macos-user notch is macOS Seatbelt under a dedicated account, and the host notch is
  `yolo host` ([`host-render-target.md`](host-render-target.md)).
- ***Composed host PATH*** *(coined here):* the PATH yolo builds for a host launch from the pieces
  in [§2.2](#22-how-the-composed-host-path-is-built), independent of the ambient PATH.

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
| `YOLO_*` | e.g. `paths.AllowMissingProvidersEnv`, `YOLO_ACCEPT_CONFIG_CHANGES`, `config.InJail` via `YOLO_VERSION` | Named dials. The ruling allows these |

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
  through does not breach the ruling. This matches the jail notch, which forwards `TERM` and
  `COLORTERM` explicitly (`internal/cli/run/assemble.go`).

**PATH falls in both classes.** yolo reads it to resolve the target and probe dependencies, and the
child reads it for every subprocess it spawns. An npm-installed CLI whose entry script is
`#!/usr/bin/env node` needs `node` on the child's PATH. So PATH is composed, and **the composed
value is also the child's PATH.** Resolving against one PATH and exec'ing into another would move
the incident one process down instead of fixing it.

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
**Declared-impure**: named by `host_path`, with content that is machine state.

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

Other managers need no typed entry. Homebrew, npm's global prefix, pipx and `~/.local/bin` all put
real binaries in a fixed directory, and a plain `host_path` string names it. A pack-declared install
directory would cover the shipped installers without user config; that is [OQ-HE2](#oq-he2).

## 3. One authority — the seam

**Rule.** At the host notch, every lookup that decides something resolves against the **same
composed host PATH value**, computed **once per process** by one resolver. The resolver's package
and name are the implementer's choice; this doc calls it `hostpath.Compose`. It returns the ordered
entries, the joined value, where each entry came from (baseline, `host_path`, `YOLO_HOST_PATH`,
inherit), and a `LookPath(bin)` that knows the shim entries.

The consumers:

| Consumer | Today | After |
| :--- | :--- | :--- |
| `hostExec` target | `resolveHostTarget(os.Getenv("PATH"), …)` | `resolveHostTarget(composed.Value(), …)`. The skip list is unchanged |
| Child PATH | inherited | `environ()` overlays `PATH=<composed>` last, after removals, so no pack env or profile can replace it. A pack wanting a PATH entry declares it, if ever, through [OQ-HE2](#oq-he2) |
| Dependency probe (gate survey, `yolo host apply`, `check-deps`, re-probe) | `depcheck.LookPath` = `exec.LookPath` | `depcheck` takes the lookup from its caller. The package-level `var` remains a test seam only |
| `detectManager` | bare `exec.LookPath` | the same lookup |
| `runDepInstallCommand` | the inherited environ | the composed environ, so an install lands where the re-probe looks |
| `yolo check` | never probes pack dependencies | gains a **host launch** section: the composed entries with their sources, each declared entry's existence, and each selected pack program's resolution through the same `LookPath`, including the shim verdict |

**In-jail**, `config.InJail()` makes the resolver return the process PATH unchanged. The jail's PATH
is already composed (`entrypoint.BootPath`), and an in-jail `check-deps` asks about the jail.

**Exempt from composition, by name.** The wrapper-precedence checks (`hostWrappersStatus` and `yolo
check`'s wrapper section) keep reading the ambient PATH. Their question is *about the caller's
shell*: "will a bare `claude` typed there reach the wrapper?" That is a report on the ambient
environment, not a decision made from it. So is the wrapper body's own `exec yolo`, which runs in
the caller's shell.

**Forbidden.** No host-notch consumer may call `os.Getenv("PATH")` or bare `exec.LookPath` to make
a decision. The exemptions above are the only readers of the ambient PATH.

## 4. Migration — report first, then enforce

### 4.1 Stages

What breaks when PATH stops being inherited: anything found only through an ambient entry that the
baseline does not contain. The known cases are:

- `claude` in `~/.local/bin` (the claude pack's installer);
- an npm global prefix such as `~/.npm-global/bin` (every `via: "npm"` program);
- mise shims or activated install dirs;
- `~/go/bin`, `~/.cargo/bin`, `~/.nix-profile/bin`;
- `/opt/homebrew/bin` on macOS, if not in the baseline ([OQ-HE8](#oq-he8));
- the tools the **child** spawns, which yolo cannot enumerate.

| Stage | Trigger | Resolution | What it prints |
| :--- | :--- | :--- | :--- |
| **1 — report** | `host_path` unset | Ambient PATH, as today, for the exec, the probe **and** the child. Behavior is unchanged | For each binary yolo itself resolves (the exec target and every probed dependency) that the composed PATH would resolve **differently** (missing, or at another path), one stderr line: *"`opencode` resolved from `/home/u/.local/share/mise/shims`, an inherited PATH entry not named in `host_path`; add `\"host_path\": [{\"mise\": \"shims\"}]` to `~/.config/yolo-jail/config.jsonc`."* `yolo check`'s host launch section shows the same findings |
| **2 — enforce, opt-in** | `host_path` set, or `YOLO_HOST_PATH` set | The composed host PATH everywhere ([§3](#3-one-authority--the-seam)) | Only the miss diagnostic ([§4.2](#42-the-diagnostic-for-a-miss)) |
| **3 — enforce by default** | a later named release ([OQ-HE9](#oq-he9)) | The composed host PATH, from the baseline alone when `host_path` is unset | The miss diagnostic |

Stage 1's notice is emitted on every launch it applies to, with no rate limit. A background job's
stderr is where its operator looks. Stage 1 **cannot see what the child will spawn**; that is the
residual risk stage 3 carries, and the stage-3 release note must say so. The notice is a disclosure
under the launch stream's rules
([`report-tiers.md`](../reference/report-tiers.md#the-launch-stream)), so no flag hides it.

There is **no hatch** beyond `host_path` itself. An `{"inherit": "PATH"}` entry ([OQ-HE5](#oq-he5))
is config, not a hatch: it names the dependence.

### 4.2 The diagnostic for a miss

When a binary is missing from the composed host PATH, yolo still exits 127 for the exec, and the
dependency rule still applies to the probe. Before it does, yolo looks in two places to write the
message:

- the ambient PATH;
- a compiled list of ***hint locations*** *(coined here)*: the shims and installs under mise's
  default data dir, `~/.local/bin`, `~/.npm-global/bin`, `~/go/bin`, `~/.cargo/bin`,
  `~/.nix-profile/bin`, `/opt/homebrew/bin` and `/home/linuxbrew/.linuxbrew/bin`.

Then it prints one of:

- **Found on the ambient PATH:** *"`opencode` is not on the composed host PATH. The invoking PATH
  has it at `<path>`; add `<dir>` to `host_path`."*
- **Found at a hint location:** the same message, naming the location. For a mise install it
  suggests `{"mise": "shims"}`.
- **Found nowhere:** today's message, plus the composed entries so the reader can see what was
  searched.

**Hint locations never resolve anything.** They change only the text of a failure, never its
outcome. That keeps them outside the ruling. The gate survey's decision text uses the same
diagnostic, so the render-suppressing "missing dependency" and the exec's 127 name the same remedy.

## 5. Tests that would pin it

Each test is chosen by the repo's question: **does it fail if I delete the call site?**

1. **Exec call site.** Drive `hostMain` with `hostSyscallExec` stubbed.
   - Setup: an ambient `PATH` whose only hit for `tool` is in a directory not in `host_path`, and a
     `host_path` naming a second directory holding a different `tool`.
   - Assert: the exec'd path is the declared one, **and** the `PATH` in the passed environ equals
     the composed value.
   - It fails if `hostExec` goes back to `os.Getenv("PATH")`, or if `environ()` stops overlaying
     PATH.
2. **Parity across launchers.** Run the same config through the gate survey, `check-deps` and
   `yolo check`'s host launch section twice: once with a minimal ambient environ (`HOME` and a
   one-entry `PATH`), once with a rich one containing an extra tool directory. Assert identical
   verdicts. This is the incident as a test. It fails if any of those callers stops passing the
   composed lookup.
3. **Re-probe and installer agreement.** Stub `depInstallRun` to drop a binary into a declared
   directory that is absent from the ambient PATH. Assert that the `--assert` run succeeds. Today it
   reads as a decline.
4. **`detectManager` through the seam.** A manager present only on the ambient PATH is not
   detected.
5. **Shim verdict.** A fake shims dir whose link target is a stub `mise` that fails `which`. Assert
   the "shim present; version not installed" finding, and that the dependency rule treats it as
   missing.
6. **Scope.** A workspace-scope `host_path` is a validation error, and has no effect on composition
   even when validation is bypassed. The inheritance-table classification test covers the new key.
7. **Grammar.** Relative, `~user/`, `$`- and `:`-containing entries are refused. `YOLO_HOST_PATH`
   replaces rather than merges. `host_path: []` gives the baseline alone.
8. **Stage 1 notice.** With `host_path` unset and the target found only through an ambient entry,
   the launch still execs the ambient hit **and** prints the notice naming the entry. Deleting the
   notice call fails it.
9. **In-jail passthrough.** Under `config.InJail()` the resolver returns the process PATH unchanged.

## 6. Relation to the other notches

- **The jail and macos-user notches already obey the ruling.**
  - The jail's PATH is `entrypoint.BootPath`, set by `execBash`. Its environ is the image `Env`
    plus explicit `-e` pairs.
  - macos-user launches through `/usr/bin/env -i` with the closed list `sandboxEnvPairs`, whose
    PATH is `macosuser.SandboxPath`. Everything else crosses in the session env file.
  - The host notch is the one notch that inherits, and this design closes that gap.
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

- **Stage 3 breaks tools the child spawns.** Stage 1 cannot report them. Mitigation: the release
  note, `{"inherit": "PATH"}` if OQ-HE5 is accepted, and `yolo check` listing the composed entries.
- **Shim confirmation costs time on the gate's one-second budget.** On a budget overrun the survey
  reports "cannot determine" and launches (the existing disposition), so the worst case is
  today's behavior.
- **A macOS baseline read from `/etc/paths.d`** includes whatever installers dropped there. That is
  machine state, which is allowed, but it is not audited.

## 9. Open questions

### <a id="oq-he1"></a>💬 [`OQ-HE1`](#oq-he1) — what does an unset `host_path` resolve to once enforced? — **OPEN**

**Strict:** the baseline alone.

**Generous:** the baseline plus the hint locations, existence-filtered. This is as deterministic as
strict, because it depends on no launch environment, but it has the shape of the "opportunistic"
append the ruling superseded.

**Leaning: strict**, with stage 1's notices as the migration path. The ruling's objection to
opportunism reads as an objection to yolo guessing, and a fixed guess is still a guess.

> **Answer:**

### <a id="oq-he2"></a>💬 [`OQ-HE2`](#oq-he2) — may a pack declare the directory its program installs into? — **OPEN**

The claude pack's installer lands in `~/.local/bin`. A pack-declared install directory would put it
on the composed host PATH with no user config, and the declaration would be named in the manifest.
But a fetched pack could then add PATH entries to host launches, which is the capability the
user-scope rule withholds from a workspace.

**Leaning:** not in this design. If adopted later, shipped packs only.

> **Answer:**

### <a id="oq-he3"></a>💬 [`OQ-HE3`](#oq-he3) — ship the `{"mise": "shims"}` typed entry, or plain directories only? — **OPEN**

Option B in [§2.3](#23-tool-managers--mise-and-the-shim-is-not-installed-problem) against option A.

**Leaning: B.** It is the only option that keeps mise's per-directory versioning and makes the
probe's *present* true.

> **Answer:**

### <a id="oq-he4"></a>💬 [`OQ-HE4`](#oq-he4) — `YOLO_HOST_PATH`: exist at all; replace or prepend? — **OPEN**

It serves a systemd unit or CI job that wants a different path without editing the user config.

**Leaning:** it exists, and it **replaces** `host_path` whole. That way the composed value never
mixes two authorities, and `yolo check` names which one won.

> **Answer:**

### <a id="oq-he5"></a>💬 [`OQ-HE5`](#oq-he5) — offer `{"inherit": "PATH"}`? — **OPEN**

The ruling permits a *named* dependence on the launch environment, and this entry is exactly that.
It is also the off-ramp for anyone who wants today's behavior.

**Leaning: offer it.** `yolo check` labels it as the one non-deterministic entry.

> **Answer:**

### <a id="oq-he6"></a>💬 [`OQ-HE6`](#oq-he6) — does the ambient credential fallback survive? — **OPEN**

The provider `lookup` falls back to `os.LookupEnv`, and `credentialGaps` reads `os.Getenv`. So a
key exported in a shell rc resolves from a terminal and refuses under Waybar: the same class as the
incident. Removing the fallback makes the terminal refuse too, which is consistent but breaks
every `export ANTHROPIC_API_KEY` user.

**Leaning:** stage it the same way as PATH. Stage 1 reports "resolved from the invoking
environment; name it in `env_sources`," and enforcement comes with PATH's stage 3. The key itself
still reaches the child as a carried variable either way.

> **Answer:**

### <a id="oq-he7"></a>💬 [`OQ-HE7`](#oq-he7) — does the ruling extend to jail launches' host-side lookups? — **OPEN**

`yolo`, `yolo check`, `describe` and `commands` resolve podman, nix and the macOS tools from the
ambient PATH.

**Leaning:** yes, in its own doc. It reuses this resolver's baseline, but it is a different
command and a different migration.

> **Answer:**

### <a id="oq-he8"></a>💬 [`OQ-HE8`](#oq-he8) — the macOS baseline, and whether Homebrew is in it — **OPEN**

Options: `path_helper`'s file inputs, or a compiled list. `/opt/homebrew/bin` is added by `brew
shellenv` in an rc file, not by `/etc/paths`. Yet `detectManager` prefers `brew` on darwin, and
without it in the baseline, remedies fall to `nix`.

**Leaning:** read `/etc/paths` and `/etc/paths.d`, and include Homebrew's fixed prefixes when
present. They are fixed locations, not launch environment.

> **Answer:**

### <a id="oq-he9"></a>💬 [`OQ-HE9`](#oq-he9) — when does stage 3 flip the default? — **OPEN**

**Leaning:** setting `host_path` is the per-user opt-in (stage 2) from the first release. The
default flips in a later release named at the time. It does not flip on a timer, and not before
`yolo check`'s host launch section has shipped.

> **Answer:**

## 10. Decision Ledger

| Id | Date | Decision | Why it holds |
| :--- | :--- | :--- | :--- |
| <a id="oq-he0"></a>[**OQ-HE0**](#oq-he0) | 2026-09-25 | `yolo host` does not depend on the environment it was launched in unless a `YOLO_*` variable or explicit config names the dependence. This supersedes the opportunistic mise-shims append, and for PATH it overturns "start from the current environment" | Maintainer ruling ([§0](#0-the-governing-ruling)). The same command must give the same verdict from a terminal, Waybar, systemd, cron and an IDE |

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
- **`internal/hostwrap/hostwrap.go`:**
  - `Body` is `exec yolo host -- <bin> "$@"` under `#!/usr/bin/env bash`.
  - `LookPathSkipping` skips empty entries and requires an executable the caller may run.
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
  - `internal/cli/run/assemble.go`: `TERM`/`COLORTERM` forwarding.
- **Scope pattern:** `internal/config/hostwrappers.go` reads user scope directly, `validate.go` has a
  "user-scope only" error, and `inherit.go` classifies the `host_*` keys as *Neither*.
- **`packs/opencode/pack.json`:** `opencode` is a `program` with `via: "npm"` and brew and pacman
  install hints. On the incident machine it was installed through mise, which no declaration names.
- **mise behavior** (shim location, link-to-binary shims, cwd-based version selection, `mise
  which`, `mise bin-paths`): from mise's documentation. **Not measured in this jail.** Measure it
  before building option B's confirmation step.
