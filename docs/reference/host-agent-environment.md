---
status: current
verified: 2026-09-09
verified_commit: 40915b60
covers:
  - internal/cli/host.go
  - internal/cli/hostapply.go
  - internal/cli/hostapplygate.go
  - internal/hostwrap/
  - internal/hostpath/
  - internal/cli/check/section_hostwrappers.go
tags: [host, env, packs, profiles, wrappers]
summary: "The two channels that deliver a pack's environment to an agent running on the HOST — configuration into the agent's native config surface, environment into the process via `yolo host` — plus the opt-in wrapper directory that makes both reachable from a bare command or an absolute path."
---

# Delivering environment to host agents — two channels and a wrapper directory

**Status:** CURRENT as of 2026-09-09, verified against `40915b60`. The credential disclosure's
remedies for an ad-hoc command (execution flow step 2, and the paragraph after it) are newer, from
2026-09-27 ([ES-D2 to ES-D5](../design/credential-sources-separation.md#10-decision-ledger), with
the remedy's corrections in ES-D10 to ES-D12), and are pinned by unit tests through `hostMain`;
since 2026-09-28 a bare `-p` reaches agent CLIs only here, as in a jail
([OQ-NC5](../plans/notch-convergence.md#OQ-NC5), which retired ES-D1's `-p` grant). So is
the `--with-credentials` grant beside it, from 2026-09-27
([OQ-ES5](../design/credential-sources-separation.md#OQ-ES5)'s host half, ES-D13 to ES-D17), and
the refusal of a profile the in-jail bridge would serve (ES-D18 to ES-D20). The removal of
`yolo host apply --shell-init` is newer still, ruled 2026-09-27 ([HE-D1](#he-d1)), and is pinned
by unit tests through `hostMain`; so is the one-row-per-cause shape of the `yolo check` section
([HE-D2](#he-d2)), pinned by the section's own unit tests. Execution flow step 3's `host_path`
folders and the launch PATH every host check reads are from 2026-09-30
([host-launch-environment.md's HE-DIR1](../design/host-launch-environment.md#he-dir1)), pinned by
unit tests through `hostExec`, `checkDepsMain`, `applyHost` and the launch gate. Step 3's wire
tables are from 2026-09-30 ([FT-D2](../design/agent-footer.md#FT-D2)), pinned by unit tests
through `hostMain`.

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
| Wrapper generation, contents, and the plan | `internal/hostwrap` (`Body`, `Bins`, `Plan`, `OnPath`, `Precedence`) |
| The apply stage that writes them | `internal/cli` (`applyHostWrappers`) |
| The refusal left where `--shell-init` was | `internal/cli` (`refuseShellInit`) |
| The every-run `PATH`, precedence and completeness observations | `internal/cli/check` (`section_hostwrappers.go`) |
| Where the directory lives | `internal/paths` (`WrapDir`, `WrapDirUnder`, `GeneratedBinDir`) |

**Reads with:** [`providers.md`](providers.md) (what a provider declares, and which agent reads
which endpoint), [`envsource-relative-paths.md`](envsource-relative-paths.md) (what a relative
`env_sources` path points at — the secret channel this composition hydrates),
[`../design/host-render-target.md`](../design/host-render-target.md) (the host as one notch of
the confinement dial), [`pack-system.md`](pack-system.md) (the contribution model),
[`host-launch-environment.md`](../design/host-launch-environment.md) (which PATH `yolo host`'s checks and the agent read: the caller's, by ruling [HE-DIR1](../design/host-launch-environment.md#he-dir1), then the folders of the user-scope `host_path` list). For the
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
   and never writes it ([HE-D1](#he-d1)).
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
2. **It is an rc edit, and P3 says yolo does not make it.** `apply` prints the line, `yolo check`
   repeats it until it takes effect, and the user adds it. `yolo host apply --shell-init`, which
   used to append it, is removed and refuses, printing the line ([HE-D1](#he-d1)).
3. **It is one decision, not one per agent** — the `host_wrappers` config key, top level beside
   `host_files`. Not opted in means no directory, no wrappers, and no messages at all;
   `yolo host --` still works.

   ⚠ **And since 2026-09-22 that decision is usually DERIVED rather than made.** An unset
   `host_wrappers` is **ON** when `host_management` is `"own"`, because `own` means yolo composes the
   agent's config file whole — and a config file cannot carry a credential, so without a wrapper on
   PATH a bare `claude` gets the config and none of the environment it assumes. Declaring `own` and
   nothing else left exactly that half-configured host, and `yolo doctor` was silent about it because
   [the section that reports this state](#apply-reports-actions-check-reports-state) short-circuits on the opt-in.
   An explicit `false` still wins. ⚠ `"assert"` does **not** derive, because it is `host_management`'s
   own unset default — deriving from it would put a PATH claim on every machine that declared nothing.

   **`yolo host wrappers enable`/`disable` are DELETED** (they now refuse, naming the derivation). A
   verb whose whole effect was writing one boolean into the user's config made yolo a second writer
   of a file the user owns, for a line they can type themselves — and with the default derived, the
   common case needs no line at all. `yolo host wrappers status` survives, because it only reads.
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
| `yolo host apply` | The ergonomic form, and where the host-**only** verbs live: `yolo host env`, `yolo host wrappers status`, and the exec half `yolo host -- <cmd>` |
| `yolo apply --host` | **Removed.** Not deprecated with a message |

Removal does not re-special-case the host. The dial governs *what the notch is*; `yolo host`
governs *where its ergonomics live*, and it earns a namespace for a reason no other notch can
match: only the host has a user shell and a `PATH` to claim, so `yolo host env` and
`yolo host wrappers status` have no `jail` or `guest` counterpart and nowhere else to go.

**The exec half has the same two spellings**, and the front door decides between them once
(`cli.routeArgv`): every launch carrying `--at host`, wherever the flag sits, is `yolo host`.
`yolo --at host -- <cmd>`, `yolo run --at host -- <cmd>`, `yolo --at host run -- <cmd>` and a
bare `yolo --at host` all route there; the bare one runs nothing and prints `yolo host`'s usage.
The last `--at` typed wins in either order, and every `--at` is consumed on the way, so
`yolo --at jail --at host -- <cmd>` runs at the host as `yolo --at host --at jail -- <cmd>` runs
in the jail. `yolo host` takes `--at host` as a no-op and refuses any
other notch, and a jail-launch flag with no meaning at the host (`--timing`, `--dry-run`,
`--network`, `--accept-config-changes`) is refused by name rather than ignored. The config key
`confinement: host` is not an `--at` spelling and still refuses a launch
([OQ-DP3](../design/declaration-parity.md#decision-ledger)).

**`--` is parsed before any verb**, and that ordering is the whole grammar: the exec half takes
flags before the separator (`yolo host -p bedrock -- claude`), so the first argument is routinely
a flag rather than a verb, and a verb switch running first would have to re-implement flag
parsing to discover whether a verb was present at all. With no `--`, a first argument that is a flag
(other than `--help`) is still an exec flag, since no verb starts with a dash: `yolo host -p zai`
and `yolo --at host --profile=` get the exec half's refusals, exit 2, and flags that parse are
refused for naming no command, the host verb having no default one.

### Execution flow

1. **Locate the target binary.** A bare name of a program a selected pack delivers is **yolo's
   floor copy**, `~/.local/share/yolo-jail/host-floor/bin/<name>`, installed first when it is
   missing, whatever PATH the caller held
   ([`host-tool-provisioning.md`](../design/host-tool-provisioning.md), HP-DIR4). A target given
   as a path is exec'd as given. Anything else, and a selected pack's program the floor cannot hold
   on this machine (said on one line), is looked up on the child's PATH (step 3), **skipping
   yolo-managed directories** and the floor's own `bin/`: a name there that no selected pack
   delivers is an entry the floor no longer keeps, which is never run, and `yolo host apply
   --assert` removes it.
2. **Resolve the pack configuration** — the active profile for the launched command, and its
   effective `env` for the active workspace. The profile is a typed `-p`, else the command's
   entry in the `profile` key, else its `"*"` (or the key's string form) when a selected pack
   installs the command. A typed `-p` for a command no selected pack installs is refused, naming
   `--with-credentials` ([OQ-NC5](../plans/notch-convergence.md#OQ-NC5)), and a `profile`
   key no resolvable pack installs is refused with the validator's message
   ([ES-D5](../design/credential-sources-separation.md#10-decision-ledger)), so no profile
   selects for such a command.
3. **Compose the process environment** — start from the current environment, hydrate
   `env_sources` (the secret channel), overlay the resolved `env`, set the three **wire tables** a
   jail launch also carries (`YOLO_PROVIDERS`, `YOLO_PROFILES`, and `YOLO_USE_PROFILES` holding
   this one command's profile, `{}` when it has none), then **apply removals**: a `null` is an
   `unset`, not an empty string. The tables are set on every launch, so a launch started inside
   another replaces what it inherited, and an agent's footer names the profile this launch runs on
   ([FT-D2](../design/agent-footer.md#FT-D2)). PATH is overlaid last: the caller's PATH, then each
   folder of the user-scope `host_path` list not already on it, then the floor's `bin/`, which
   holds agent names only, so an agent's own commands see the caller's PATH first. A caller with no
   PATH at all (`env -i`) gets the system directories the floor's installers run with in its place,
   so an agent's commands still resolve. A dependency probe answers a program the floor delivers by
   its floor entry; every other PATH check reads the **launch PATH** (the caller's PATH, then
   `host_path`'s folders: [HE-DIR1](../design/host-launch-environment.md#he-dir1)) through one
   resolver, `internal/hostpath`, and a miss prints one line naming the PATH searched and the
   `host_path` fix ([`host-launch-environment.md` §4.2](../design/host-launch-environment.md#42-the-diagnostic-for-a-miss)).
4. **Say what starts** — one line naming the target and where it came from (yolo's floor copy,
   your PATH, or as given) — and **exec** it with that environment.

> [!WARNING]
> **Step 1's skip is load-bearing.** It is what lets `<wrap dir>/claude` be
> `exec yolo host -- claude "$@"` without calling itself. Narrow that skip and the wrapper front
> door breaks first, and loudly.

`eval "$(yolo host env)"` is a third front door onto the same composition, for direnv and mise
users. It emits POSIX `export` lines by default, with a JSON format for tooling. The script is
**one agent's slice** (`--agent`, default `claude`): a provider credential another agent's
profile claims is not in it ([the credential gate](providers.md#the-credential-gate)), and the
gate's disclosure goes to stderr, which an eval'ing shell does not read, naming what was
withheld.

**`-p` reaches agent CLIs only, as it does in a jail.** `yolo host -p zai -- pi` runs pi on the
zai profile, because a selected pack installs pi. `yolo host -p zai -- curl …` is refused before
anything runs, naming `yolo host --with-credentials zai -- curl …`, the grant for an ad-hoc
command ([OQ-NC5](../plans/notch-convergence.md#OQ-NC5)).
`eval "$(yolo host env --with-credentials zai)"` puts the same keys in the current shell. Each
withheld line in the disclosure names the fix: the grant for an ad-hoc command, and for an agent
a `-p` with a declared profile resolving to the claiming provider that the agent can run on, or a
note to declare one. When an agent cannot run on that profile, the line names the grant for an
ad-hoc `bash` instead; on an agent that can, it says the `-p` replaces the agent's own profile. A
withheld name the invoking shell already exports is disclosed as not added by yolo, and one a
pack's `env` sets as held from that source, since the command holds either anyway
([`providers.md`](providers.md#the-credential-gate)).

**`--with-credentials` hands a command keys by provider, several at once.**
`yolo host --with-credentials <provider[,provider...]|all> -- <cmd>` gives the one command the
named providers' claimed `env_sources` values, keys only. `all` means every composed provider
that claims a value, which is what a usage bar that pings every subscription needs from the one
store the user keeps. It selects no profile and re-points nothing, and it combines with `-p`: an
agent keeps its profile and receives the granted keys too.
`eval "$(yolo host env --with-credentials all)"` is the shell spelling. There, with neither
`--agent` nor `-p`, the script is an ad-hoc command's slice, so no agent's provider shape reaches
the shell. With `-p` it is the slice `-p` composes, the verb's default agent's, plus the keys
([ES-D21](../design/credential-sources-separation.md#10-decision-ledger)). Every run given the
flag prints a `Credential grant` block, names only, and a named provider with nothing to hand
over is reported. On such a run, a withheld line names the same run with the claimant added to
the grant, such as `yolo host --with-credentials zai,cerebras -- usage-bar`, rather than a `-p`,
which would replace the typed profile and drop the grant
([ES-D23](../design/credential-sources-separation.md#10-decision-ledger)). An unknown provider
refuses, naming the known ones. Only the typed flag grants, and a jail launch given it refuses as host-only
([OQ-ES5](../design/credential-sources-separation.md#OQ-ES5), ruled for the host;
[ES-D13 to ES-D17](../design/credential-sources-separation.md#10-decision-ledger)).

**A profile the wire bridge serves starts the bridge for that launch.** `yolo host -p cerebras --
claude` and `yolo host -p codex -- claude` start the bridge's host half as the launch's own
child, on a loopback port it picked, answering only that launch's caller token, which claude gets
as `ANTHROPIC_AUTH_TOKEN`; the launch stays resident and stops the bridge when claude exits,
by any route. `yolo host env` refuses such a profile, naming the `yolo host --` spelling, and
`yolo host apply` renders no address for a bridged selection in the `profile` key and says so. A
bridge the launch cannot start refuses, naming why and `yolo -p claude=<profile> -- claude` as
the container launch where the profile works. An agent that also speaks the provider's own wire
runs on it directly
([`wire-bridge.md`](wire-bridge.md#at-the-host-notch),
[`host-notch-services.md`](../design/host-notch-services.md)).

## apply reports actions, check reports state

- **`apply` prints the `PATH` line when it created or changed the wrapper directory** — a
  completion notice about its own action ("I just wrote these wrappers; here is what makes them
  take effect"), which needs to know nothing about anyone's rc file or shell. It is silent on
  every apply that changed no wrappers, which after P5 means every apply that neither enabled
  the feature nor changed the selected pack set.
- **`yolo check` carries the `PATH` observation, every run**, because its job is "what is the
  state of my environment" and it is typically run from a fresh shell. A generated wrapper
  directory that nothing on `PATH` reaches is an inert-configuration warning, in the
  summary-counted channel — and its remedy **prepends**. Three more observations share that
  section and that channel, each a state `apply` cannot see:
  - **Precedence — on `PATH` is not winning.** Each wrapper's name is resolved the way a shell
    resolves it (first match wins, compared by file identity so a symlinked spelling of the
    directory still counts), and a wrapper something earlier on `PATH` shadows is a warning
    naming the binary that wins and the prepend line that fixes it. It is the
    [Prepend, not append](#the-wrapper-directory) rule, observed.
  - **Completeness.** A program a selected pack installs with no wrapper — a pack added since
    the last apply — is a warning naming it. Such a program never passes the
    [launch gate](host-apply-staleness.md#the-launch-gate) (the re-check `yolo host --` runs
    before exec'ing), so it is the one launch that never brings the host up to date by itself.
  - **The launch gate's reachability.** `host_apply_on_launch` being on is reported as working
    only when at least one wrapper wins on `PATH` and `host_management` is not `"none"`. With no
    wrapper, the directory off `PATH`, or every wrapper shadowed, no launch can reach the
    re-check, so the row that names that cause also says the key is on and cannot fire, rather
    than anything promising an automatic sync that cannot happen. Under `"none"` the gate does
    nothing even when a launch reaches it, since there is no render to re-check, so fixing
    `PATH` would not make the key fire. The `host_management` row says that, and no `PATH` row
    does.

  **One cause, one row** *(coined here, for [HE-D2](#he-d2))*: each warning in that section is
  one cause. Its headline names the cause, its note leads with the fix, and it then says what the
  cause breaks: a bare command running unwrapped, and the launch sync when that is the reason it
  cannot fire. No row points at another row for its fix. Two different causes are still two rows;
  a `host_management` of `"none"` and a directory off `PATH`, for example, have separate fixes.
  Under `"none"` a missing wrapper is folded into the `host_management` row, because the apply
  that would generate it is what `"none"` refuses. For a directory off `PATH` the section prints:

  ```text
  [WARN] wrapper directory is not on PATH, so no wrapper runs
       -> export PATH="/home/you/.local/share/yolo-jail/bin/wrap:$PATH"
          Add that line to your shell rc, below any line that puts ~/.local/bin on PATH, then open a new shell.
          Until then a bare claude or pi runs unwrapped, with no composed environment, though each wrapper works by absolute path.
          host_apply_on_launch is on but cannot fire, so no launch re-checks this host's render.
  ```

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
repeats it every run), but not working either. That is the cost of P3, and the only exit is the
line, pasted by the user. yolo offers no writer for it ([HE-D1](#he-d1)).

## What this does not license

- **Not a shell rc that exports agent variables.** One `PATH` entry, printed for the user to add,
  is the entire claim yolo makes on the user's shell, and yolo never writes it (P3,
  [HE-D1](#he-d1)).
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
- **Not a second env fold.** Packs' `kind: "env"` contributions reach the host through the same
  fold the jail reduces (`packload.EnvFold`: each pack's static keys, then its profile-gated keys
  whose gate fires for the launched agent), and `hostfoldparity_test.go` pins the two notches to
  one winner per key.
- **Not a second credential gate.** Which `env_sources` values the host adds is the jail's own
  answer, `packload.ScopeCredentials` over this notch's one-agent table: a value a provider
  claims reaches the agent only when its profile selects that provider, and the launch says what
  it withheld ([`providers.md`](providers.md#the-credential-gate)). The shell the host notch
  inherits is the user's and passes through untouched. The one table is keyed by the launched
  command, so a typed `-p` makes an ad-hoc command a recipient; it is not an exemption from the
  gate, because the command receives only its profile's claimed values.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| <a id="he-p1"></a>[**HE-P1**](#he-p1) — split by payload type, not by agent, and not as a preference order | The first design was a config-first ladder with process env as a fallback for the one agent that could do no better. A config file *routes* a credential and cannot *deliver* one, so the ladder had the load-bearing case backwards. |
| <a id="he-p2"></a>[**HE-P2**](#he-p2) — keep wrappers, as a three-line `exec` into `yolo host` | Consistency without a second env-composition implementation to drift. |
| <a id="oq-1"></a>[**OQ-1**](#oq-1) — copilot BYOK needs no per-agent advisory | Under P1 the need is a property of the provider, so copilot is the ordinary path rather than a special case; one statement covers every agent at once. |
| <a id="oq-2"></a>[**OQ-2**](#oq-2) — `yolo host -- <cmd>`, with `yolo --at host -- <cmd>` as the alias | The exec half needs a spelling that reads as a launch, and the dial keeps every notch equal. |
| <a id="oq-3"></a>[**OQ-3**](#oq-3) — `yolo host env` emits POSIX `export` by default, with JSON for tooling | Shell-specific emitters are not refused, just not built until asked for. |
| <a id="oq-4"></a>[**OQ-4**](#oq-4) — `apply` reports actions, `check` reports state, and `--shell-init` writes on request | An observation `apply` cannot make reliably must not gate what it prints; the actions-vs-state split is what the two commands are for. The `--shell-init` half is withdrawn by [HE-D1](#he-d1). |
| <a id="oq-5"></a>[**OQ-5**](#oq-5) — every host program a selected pack installs gets a wrapper, unconditionally | The wrap dir is an addressable launch surface; a path that exists on some machines and not others is not one. Overrules an earlier "only when the resolved env is non-empty" leaning. |
| <a id="oq-6"></a>[**OQ-6**](#oq-6) — the wrap dir is hardcoded under the existing host state root, not `XDG_DATA_HOME` | This repo follows the XDG *layout* and honors no XDG *variable* anywhere, so honoring one for a single new directory would make it the only path in the tree that moves when the variable is set. A cache dir would be worse: an evicted `PATH` entry is a silently broken `claude`. |
| <a id="oq-7"></a>[**OQ-7**](#oq-7) — `yolo apply --host` is REMOVED, not deprecated | Three spellings for one operation was the problem, and a deprecation message keeps the third spelling alive. Sweep prose with an allowlist, never with a blind substitution: docs that record what shipped *at the time* must keep the old spelling. |
| <a id="he-d2"></a>[**HE-D2**](#he-d2) — one cause, one row in `yolo check`'s host wrappers section (2026-09-27, maintainer ruling) | The ruling, verbatim, on a section printing two warnings for one wrapper directory missing from `PATH`: *"also why are ther emultiple wranings? this is very hard to read."* The second warning was the `host_apply_on_launch` row saying the sync cannot fire and pointing at "the rows below" for the fix. The key's state now rides on the row whose cause stops it, and that row still names everything the pair named: the cause, the fix line, the unwrapped bare command, the absolute-path fallback, and that the key is on. "One cause, one row" is the implementer's phrasing of the ruling and applies [report tiers' P1](report-tiers.md#principles), one fact once, to `yolo check`. The same audit applied it to two other `yolo check` findings that counted one cause twice: no container runtime answering, and a Claude OAuth broker missing both of the certificates `--init-ca` mints together. A stopped runtime used to be three rows: a `[WARN]` and a `[FAIL]` with the same start hint in the Container Runtime section, then a Merged Configuration `[FAIL]` saying no runtime was on `PATH`. A missing one was two, "No container runtime installed" and that same Merged Configuration `[FAIL]`. The Container Runtime section's `[FAIL]` is now the one finding, and Merged Configuration prints a dim line pointing back at it. A native runtime named on a host that cannot run it is a different cause and keeps its own `[FAIL]`. A review then found a third: a running jail whose host-services directory is gone printed a `[FAIL]` per loophole, with remedies that disagreed. It is one `[FAIL]` per jail now, naming the directory and the loopholes it takes down. |
| <a id="he-d1"></a>[**HE-D1**](#he-d1) — `yolo host apply --shell-init` is REMOVED (2026-09-27, maintainer ruling) | The ruling, verbatim: *"this shell init command apperas to do nothing, and I don't th8ink it's ever safe so we shoud reove it."* Both halves were measured before the removal. It did nothing on the spelling every remedy printed: bare `--shell-init` is a dry run, so it printed a "would append" line below the report's closing sentence and wrote nothing. It was unsafe on the spelling that wrote: it chose the rc file by guessing from `$SHELL` (`~/.zshrc` for zsh, `~/.bashrc` for anything else, `/bin/sh` included), and under `--assert` it appended even after the apply had refused and printed "Nothing was written." The flag now refuses with exit 2, writes nothing and prints the line. It refuses by name rather than as an unknown flag, the way `yolo host wrappers enable` does, because the people who type it are the ones a shipped message told to. |

## Current values

Verified at `40915b60`, and the opt-in and verb rows re-verified 2026-09-22. The prose above
explains what each of these is for; this table is the only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Opt-in key | `host_wrappers` (boolean, user scope) — **unset DERIVES from `host_management`: on at `"own"`, off otherwise.** An explicit `false` wins; `"assert"` does not derive, being host_management's own unset default | `config.HostWrappersEnabled`, documented by `yolo config-ref` |
| Host wrapper dir | `<host state>/bin/wrap` | `paths.WrapDir`, `paths.WrapDirUnder` |
| Generated-bin parent | `<host state>/bin` | `paths.GeneratedBinDir` |
| Jail blocker dir (first on PATH) | `~/.yolo/bin/block` | `entrypoint.BootPath` |
| Jail launcher dir (second on PATH, ahead of every install prefix) | `~/.yolo/bin/launch` | `entrypoint.BootPath` |
| Wrapper body | `exec yolo host -- <bin> "$@"`, with a generated-by header | `hostwrap.Body` |
| Wrapper set | every valid bin name a selected pack's `program` contributions install | `hostwrap.Bins` over `Pack.HonoredInstalls` |
| `yolo host` verbs | `apply`, `env`, `wrappers` (**`status` only** — `enable`/`disable` were deleted 2026-09-22 and now refuse), and the `--` exec half | `internal/cli` (`hostMain`) |
| `yolo host env` formats | `export` (default), `json` | `internal/cli` (`hostEnv`) |
| Profile flag | `--profile <name>` / `-p <name>`, keying the launched command by its basename, agent or not | `internal/cli` (`parseHostExecFlags`, `effectiveHostProfiles`) |
| rc line writer | **none.** `yolo host apply --shell-init` was removed 2026-09-27 and refuses with exit 2, printing the line ([HE-D1](#he-d1)) | `internal/cli` (`refuseShellInit`) |
