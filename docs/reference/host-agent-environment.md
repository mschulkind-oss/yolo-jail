---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/cli/host.go
  - internal/cli/hostapply.go
  - internal/cli/hostapplygate.go
  - internal/hostwrap/
  - internal/cli/check/section_hostwrappers.go
tags: [host, env, packs, profiles, wrappers]
summary: "The two channels that deliver a pack's environment to an agent running on the HOST — configuration into the agent's native config surface, environment into the process via `yolo host` — plus the opt-in wrapper directory that makes both reachable from a bare command or an absolute path."
---

# Delivering environment to host agents — two channels and a wrapper directory

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

Inside a jail, injecting environment is trivial: yolo controls the process spawn, so it passes
`-e KEY=VAL` and PID 1 has the exact environment. On the host it controls nothing — the user
types `claude` in a shell yolo exited from hours ago, or an IDE spawns a binary by absolute
path. And it cannot be avoided: yolo's provider architecture deliberately puts only a variable
*name* in config (`api_key_env_name`), so **something must populate the process environment or
BYOK does not work on the host at all**.

So there are **two channels, always both present, split by what they carry**:

- **Configuration** — endpoints, model aliases, `wire_api`, permissions, MCP wiring — goes into
  the agent's **native config surface**, written by `yolo host apply`. Universal on
  *invocation*: it works for an IDE, a cron job, another process, an absolute path.
- **Environment** — secrets, feature flags, and **unsets** — goes into the **process
  environment**, composed by `yolo host -- <cmd>`. Universal on *payload*: it can carry a
  credential and can delete a variable, which no config file can.

Neither is universal, and they are partial along different axes. That is why both always apply,
and why "just use wrappers for everything" does not collapse the problem.

| Component | Lives in |
| :--- | :--- |
| The `yolo host` verb tree and the exec half | `internal/cli` (`hostMain`, `hostExec`, `hostEnv`, `hostWrappers`) |
| Environment composition, and the resolution order | `internal/cli` (`host.go`: the pack env fold, `hostScopedEnvSources`) |
| Wrapper generation, contents, and the plan | `internal/hostwrap` (`Body`, `Bins`, `Plan`, `OnPath`) |
| The apply stage that writes them, and `--shell-init` | `internal/cli` (`applyHostWrappers`, `runShellInit`, `setHostWrappers`) |
| The every-run `PATH` observation | `internal/cli/check` (`section_hostwrappers.go`) |
| Where the directory lives | `internal/paths` (`WrapDir`, `WrapDirUnder`, `GeneratedBinDir`) |

**Reads with:** [`providers.md`](providers.md) (what a provider declares, and which agent reads
which endpoint), [`envsource-relative-paths.md`](envsource-relative-paths.md) (what a relative
`env_sources` path points at — the secret channel this composition hydrates),
[`../design/host-render-target.md`](../design/host-render-target.md) (the host as one notch of
the confinement dial), [`pack-system.md`](pack-system.md) (the contribution model). For the
`host_wrappers` key and every flag, run `yolo config-ref` and `yolo host --help`.

---

## Principles

1. **P1 — Split by payload type, never by agent.** Two channels, both always present for every
   pack, carrying different things. There is no per-agent method selection: selecting per agent
   *was* the fragility, not the fix for it. A pack that needs neither channel declares neither.
2. **P2 — The env channel is mandatory, not a fallback.** `api_key_env_name` and `env_sources`
   put only a variable *name* in config; nothing else populates it on the host. **Any BYOK
   provider is unusable on the host without this channel** — for every agent, not just the one
   with no config file. The deciding fact is not a property of the agent at all: whether the env
   channel is needed is a property of the **provider**. Bedrock needs AWS credentials in the
   environment whether the agent is `claude` or `codex`; a first-party subscription needs
   nothing.
3. **P3 — No silent shell profile pollution.** `yolo host apply` never mutates a shell rc to
   export agent variables. Session-wide exports break tool isolation, leak secrets across
   unrelated commands, and make per-command profile switching impossible. A single `PATH` entry
   is a smaller and different claim — but it is still the user's file, so yolo prints the line
   and writes it only when asked.
4. **P4 — One env-composition implementation, two front doors.** `yolo host -p <profile> --
   <agent>` is the mechanism. A generated wrapper is a three-line `exec` into it, never a second
   implementation to drift. That is what makes keeping wrappers affordable.
5. **P5 — The PATH claim is opt-in; the wrapper set is not conditional on anything.** One
   user-level decision (`host_wrappers`) enables the directory. After that a wrapper exists for
   **every host program a selected pack installs**, unconditionally — never per-agent opt-in,
   and never gated on the resolved environment.
6. **P6 — Blocker, launcher, wrapper: three mechanisms, three words, and "shim" is retired.**
   They sit at different `PATH` positions for different reasons, and the directory names say
   which is which: `bin/block`, `bin/launch` in a jail, `bin/wrap` on the host.

## The vocabulary

Three generated script directories exist, and conflating them is the mistake P6 exists to
prevent. Each is defined by what its scripts *do*, not by where they sit:

- **Blocker** — a script that refuses a command and prints an alternative (`exit 127`). In the
  jail, first on `PATH`, because interception is its whole job.
- **Launcher** — a script that installs or updates a tool on use, then `exec`s the real binary.
  In the jail, **second** on `PATH` — ahead of every install prefix, or it becomes unreachable
  the moment its own install succeeds.
  [program-delivery.md §3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)
  is the authority for that position and for the collision check that replaced the old one.
- **Wrapper** — a script that composes a host environment and `exec`s into `yolo host`. On the
  host, in its own directory, **prepended** to `PATH`.

> [!WARNING]
> **Blockers and launchers are gathered in the filesystem, never on `PATH`.** They share one
> parent (`~/.yolo/bin` in the jail, a single bind-mount anchor), and nothing may put that
> parent on `PATH` — a launcher reachable from the blockers' position defeats both mechanisms.
> Both are cleared **contents-only**: the directory itself may be a mount or a captured `PATH`
> entry, so `RemoveAll` is wrong. The host wrap directory follows the same rule for the same
> reason.

## The wrapper directory

A wrapper only works if it is found *before* the real binary, and the real `claude` is installed
by its own installer into `~/.local/bin/claude`.

> [!CAUTION]
> **`~/.local/bin` is not a placement option, and the reason is stronger than `PATH` ordering —
> it is a FILE collision.** A wrapper named `claude` in the directory that holds the real
> `claude` is the same path: one overwrites the other, and `claude update` re-running the
> installer either clobbers the wrapper or fails against it.

So wrappers get their own directory, prepended ahead of `~/.local/bin`. Four consequences of
that placement:

1. **Prepend, not append.** Appending puts the wrapper behind the real binary, where it never
   runs. Prepending means everything in that directory shadows the user's tools — a standing
   claim, which is why the directory holds only generated wrappers and is reset contents-only.
2. **It is an rc edit, and P3 says yolo does not make it.** `apply` prints the line;
   `yolo host apply --shell-init` appends it on explicit request. (It refuses to guess fish
   syntax and prints the `fish_add_path` line instead.)
3. **It is one decision, not one per agent** — the `host_wrappers` config key, top level beside
   `host_files`. Not opted in means no directory, no wrappers, and no messages at all;
   `yolo host --` still works.
4. **The wrapper is three lines and holds no logic** — a header comment and
   `exec yolo host -- <program> "$@"`. One env-composition implementation (P4).

### What a wrapper does not cover

| Bypass | Consequence |
| :--- | :--- |
| Invocation by absolute path to the real binary | wrapper skipped |
| An IDE extension with a configured binary path | wrapper skipped — **and the inversion is the fix**: point the IDE at `<wrap dir>/claude` and the composed environment arrives by absolute path, with no `PATH` consulted |
| A shell function of the same name | **beats `PATH` outright** — it has to be deleted either way |
| A process that sanitizes `PATH` before spawning | wrapper skipped |

A generated wrapper is therefore a governance win over a hand-written `.bashrc` function
(versioned, uniform, reviewable, removable) and — addressed by absolute path — a coverage win
beyond it. When `PATH` is not consulted, `yolo host -- claude` is a documented answer rather
than a mystery, and `<wrap dir>/claude` is the same answer in file form.

## Why every program gets a wrapper

The wrap directory is an **addressable launch surface**, the way a mise or asdf shim directory
is: the shim for every installed tool exists unconditionally, so a script or an IDE can point at
`<wrap dir>/<program>` and rely on getting a correct environment regardless of shell config.

There is **no trigger and nothing computes one**. The only gate is structural: a pack that
installs no program gets no wrapper. What varies by config is not *whether* the wrapper exists
but what its launch composes, and that is resolved at launch time from live state.

> [!WARNING]
> **Do not gate generation on the resolved environment.** "Generate only where the composed env
> is non-empty" fails twice. A gate computed at *apply* time gates a file whose payload is
> composed at *launch* time, so the wrapper set becomes a function of user config — unstable
> across machines and config edits. And it destroys the addressable-surface property: a
> conditionally-existing `<wrap dir>/agy` is a path nothing can rely on, which is to say, not a
> surface. The related objection — "a wrapper that injects nothing is a lie" — argues about a
> wrapper this design does not have: **no wrapper injects anything**, for any pack.

Nothing in a manifest can declare the trigger either, which is why there isn't one. Whether an
agent needs the env channel depends on whether *the user* configured a provider carrying an
`api_key_env` — a fact in the user's config, not in the pack's. A manifest declaration would
systematically under-generate. Three sources feed the composition, and only the first is
anything a manifest states: a pack's static `env` (or its active profile's `env` block), the
`api_key_env_name` of every provider the pack projects, and **removals** (a `null` value, i.e.
an `unset`, which has no config-surface equivalent at all).

> [!NOTE]
> **The honest cost of unconditional generation:** with wrappers enabled, yolo becomes a hard
> runtime dependency of every wrapped launch — a broken `yolo` or an unparseable config takes
> down an agent that needed nothing from yolo. It is bounded by the bypass table above: the real
> binary still sits behind the wrap dir on `PATH`, and invoking it by absolute path still works.

## The command surface

`yolo host` is the host-side counterpart of `yolo run`, and after P4 it is the **only** place a
host process environment is composed. Two spellings of the apply operation remain and the third
was removed outright:

| Spelling | Role |
| :--- | :--- |
| `yolo apply --at host` | The systematic form. The notch stays one value of the `confinement` dial, so every notch is spelled the same way |
| `yolo host apply` | The ergonomic form, and where the host-**only** verbs live: `yolo host env`, `yolo host wrappers enable`, and the exec half `yolo host -- <cmd>` |
| `yolo apply --host` | **Removed.** Not deprecated with a message |

Removal does not re-special-case the host. The dial governs *what the notch is*; `yolo host`
governs *where its ergonomics live*, and it earns a namespace for a reason no other notch can
match: only the host has a user shell and a `PATH` to claim, so `yolo host env` and
`yolo host wrappers enable` have no `jail` or `guest` counterpart and nowhere else to go.

**`--` is parsed before any verb**, and that ordering is the whole grammar: the exec half takes
flags before the separator (`yolo host -p bedrock -- claude`), so the first argument is routinely
a flag rather than a verb, and a verb switch running first would have to re-implement flag
parsing to discover whether a verb was present at all.

### Execution flow

1. **Locate the target binary** on the host `PATH`, **skipping yolo-managed directories**.
2. **Resolve the pack configuration** — the active profile for the target pack, and its
   effective `env` for the active workspace.
3. **Compose the process environment** — start from the current environment, hydrate
   `env_sources` (the secret channel), overlay the resolved `env`, then **apply removals**: a
   `null` is an `unset`, not an empty string.
4. **Exec** the target with that environment.

> [!WARNING]
> **Step 1's skip is load-bearing.** It is what lets `<wrap dir>/claude` be
> `exec yolo host -- claude "$@"` without calling itself. Narrow that skip and the wrapper front
> door breaks first, and loudly.

`eval "$(yolo host env)"` is a third front door onto the same composition, for direnv and mise
users. It emits POSIX `export` lines by default, with a JSON format for tooling.

## apply reports actions, check reports state

- **`apply` prints the `PATH` line when it created or changed the wrapper directory** — a
  completion notice about its own action ("I just wrote these wrappers; here is what makes them
  take effect"), which needs to know nothing about anyone's rc file or shell. It is silent on
  every apply that changed no wrappers, which after P5 means every apply that neither enabled
  the feature nor changed the selected pack set.
- **`yolo check` carries the `PATH` observation, every run**, because its job is "what is the
  state of my environment" and it is typically run from a fresh shell. A generated wrapper
  directory that nothing on `PATH` reaches is an inert-configuration warning, in the
  summary-counted channel — and its remedy **prepends**.
- **`apply` does not refuse.** It also writes the Channel 1 surfaces, which work regardless;
  refusing the half that works because the other half is unwired would be the wrong gate.

> [!WARNING]
> **`apply` must never condition on observing `PATH`.** "Already set" has two meanings and they
> disagree exactly when it matters. `apply` can only see *this process's* `PATH` — a fact about
> the shell that invoked it, not about the user's rc. False positive: the line is in the rc but
> yolo ran from a shell started before the edit, so it nags about something already done. False
> negative, the worse one: someone typed `export PATH=…` in one shell, yolo sees it present and
> says nothing, and every *new* shell has no wrappers. Reading rc files to disambiguate is not
> the fix — the line can live in any of several files or be built dynamically, and P3 says they
> are not yolo's territory. Conditioning on its own action removes the unreliable input
> entirely.

**The residual, named:** a user who enables `host_wrappers` and never pastes the line has a
working `yolo host --` and inert wrappers — not silently (`apply` said the line once, `check`
repeats it every run), but not working either. That is the cost of P3, and `--shell-init` is its
exit.

## What this does not license

- **Not a shell rc that exports agent variables.** One `PATH` entry, on request, is the entire
  claim yolo makes on the user's shell (P3).
- **Not per-agent channel selection.** A pack does not choose config-vs-env; the payload does
  (P1).
- **Not `mise` or `direnv` as the env channel.** Their coverage is a strict *subset* of the
  wrapper's (shell activation only, so they lose to the IDE too) while adding an external
  dependency; their env is **directory**-scoped where profile env is **agent**-scoped, so every
  process started from that directory would inherit the credentials. `mise` keeps the job it
  earns here: tool versions.
- **Not wrappers instead of config surfaces.** Wrappers are universal on payload and partial on
  invocation — the same partiality as config files, rotated 90°. Dropping Channel 1 trades a gap
  declarable at apply time for one the user discovers at runtime inside an IDE.
- **Not a jail mechanism.** `kind: "env"` is refused at the host notch wholesale; what crosses
  to the host is a profile's `env` and the provider's key name.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| <a id="he-p1"></a>[**HE-P1**](#he-p1) — split by payload type, not by agent, and not as a preference order | The first design was a config-first ladder with process env as a fallback for the one agent that could do no better. A config file *routes* a credential and cannot *deliver* one, so the ladder had the load-bearing case backwards. |
| <a id="he-p2"></a>[**HE-P2**](#he-p2) — keep wrappers, as a three-line `exec` into `yolo host` | Consistency without a second env-composition implementation to drift. |
| <a id="oq-1"></a>[**OQ-1**](#oq-1) — copilot BYOK needs no per-agent advisory | Under P1 the need is a property of the provider, so copilot is the ordinary path rather than a special case; one statement covers every agent at once. |
| <a id="oq-2"></a>[**OQ-2**](#oq-2) — `yolo host -- <cmd>`, with `yolo --at host -- <cmd>` as the alias | The exec half needs a spelling that reads as a launch, and the dial keeps every notch equal. |
| <a id="oq-3"></a>[**OQ-3**](#oq-3) — `yolo host env` emits POSIX `export` by default, with JSON for tooling | Shell-specific emitters are not refused, just not built until asked for. |
| <a id="oq-4"></a>[**OQ-4**](#oq-4) — `apply` reports actions, `check` reports state, and `--shell-init` writes on request | An observation `apply` cannot make reliably must not gate what it prints; the actions-vs-state split is what the two commands are for. |
| <a id="oq-5"></a>[**OQ-5**](#oq-5) — every host program a selected pack installs gets a wrapper, unconditionally | The wrap dir is an addressable launch surface; a path that exists on some machines and not others is not one. Overrules an earlier "only when the resolved env is non-empty" leaning. |
| <a id="oq-6"></a>[**OQ-6**](#oq-6) — the wrap dir is hardcoded under the existing host state root, not `XDG_DATA_HOME` | This repo follows the XDG *layout* and honors no XDG *variable* anywhere, so honoring one for a single new directory would make it the only path in the tree that moves when the variable is set. A cache dir would be worse: an evicted `PATH` entry is a silently broken `claude`. |
| <a id="oq-7"></a>[**OQ-7**](#oq-7) — `yolo apply --host` is REMOVED, not deprecated | Three spellings for one operation was the problem, and a deprecation message keeps the third spelling alive. Sweep prose with an allowlist, never with a blind substitution: docs that record what shipped *at the time* must keep the old spelling. |

## Current values

Verified at `38873c0d`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Opt-in key | `host_wrappers` (boolean, user scope) | `config.HostWrappersEnabled`, documented by `yolo config-ref` |
| Host wrapper dir | `<host state>/bin/wrap` | `paths.WrapDir`, `paths.WrapDirUnder` |
| Generated-bin parent | `<host state>/bin` | `paths.GeneratedBinDir` |
| Jail blocker dir (first on PATH) | `~/.yolo/bin/block` | `entrypoint.BootPath` |
| Jail launcher dir (second on PATH, ahead of every install prefix) | `~/.yolo/bin/launch` | `entrypoint.BootPath` |
| Wrapper body | `exec yolo host -- <bin> "$@"`, with a generated-by header | `hostwrap.Body` |
| Wrapper set | every valid bin name a selected pack's `program` contributions install | `hostwrap.Bins` over `Pack.HonoredInstalls` |
| `yolo host` verbs | `apply`, `env`, `wrappers`, and the `--` exec half | `internal/cli` (`hostMain`) |
| `yolo host env` formats | `export` (default), `json` | `internal/cli` (`hostEnv`) |
| Profile flag | `--profile <name>` / `-p <name>` | `internal/cli` (`parseHostExecFlags`) |
| rc line writer | `yolo host apply --shell-init` | `internal/cli` (`runShellInit`) |
