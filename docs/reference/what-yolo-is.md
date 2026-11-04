---
status: current
verified: 2026-10-16
verified_commit: 8e65f3444
covers:
  - internal/agentcfg/
  - internal/agentcfg/luahook/
  - internal/macosuser/seatbelt.go
  - internal/config/confinement.go
  - internal/render/confinement.go
  - internal/render/fieldset.go
  - internal/cli/apply.go
  - internal/cli/describe.go
  - internal/cli/checkdeps.go
  - internal/depcheck/
  - internal/jailcontent/briefing.go
  - flake.nix
tags: [charter, boundaries, architecture, separability, packs, confinement, notches]
---

# What yolo is — the charter, named setups, and the boundaries

**Status:** The charter graduated here from the environment-manager design on 2026-10-16. The
named-setup section and the current setup-selection/refusal examples were checked against the
implementation on 2026-11-03; the older notch vocabulary below is retained only where it explains
internal render concepts or design history. The separability sections were verified 2026-09-09
against `4385c81a` and re-read at the earlier verification commit. The direct setup override for
`yolo check` and Linux Landlock setups are not built ([Not built](#not-built)).

yolo describes the environment an agent works in — its tools, its config, its skills, its
credentials — reproducibly, declaratively, per workspace. **Confinement is one attribute of that
description**, and a jail is the strongest setting of that attribute and the default.

| Concern | Lives in |
| :--- | :--- |
| The composition engine — the separable core | `internal/agentcfg` (+ `codec`, `manifest`, `luahook`) |
| Codec helpers, its only first-party dependencies | `internal/jsonx`, `internal/tomlx` |
| Jail-side render of composed surfaces | `internal/entrypoint` (`ConfigurePackSurfaces`) |
| Host-side render, and `yolo config render` | `internal/cli/config.go`, `internal/cli/hostapply.go` |
| The named `setup` and its fields | `internal/setup` (`Setup`, `Confinement`, `RunAs`) |
| The primitive model under the notches, and the autonomy policy | `internal/render` (`Profile`, `ProfileFor`, `AgentAutonomy`) |
| Which pack kinds and config keys apply at each notch | `internal/render` (`HostFields`, `FieldSet`) |
| `apply`, `apply --sealed` | `internal/cli` (`apply.go`: `applySealed`) |
| `describe` | `internal/cli` (`describe.go`) |
| The dependency probe and its manifest | `internal/depcheck`, `internal/cli` (`checkdeps.go`, `applyhostdepgate.go`) |
| The briefing's notch header | `internal/jailcontent` (`confinementHeader`) |
| The wall itself | `internal/cli/run` (container flags), `internal/macosuser` (Seatbelt) |
| Reproducibility | `flake.nix` |

**Reads with:** [`pack-system.md`](pack-system.md) (what a pack contributes and how it is rendered),
[`third-party-pack-logic.md`](third-party-pack-logic.md) (how a pack ships *logic*, and where the
unbuilt tier lives), [`host-agent-environment.md`](host-agent-environment.md) (the host notch's
two channels and its agent floor), [`fill-the-matrix-principle.md`](fill-the-matrix-principle.md)
(which bounds how many knobs the dial may expose),
[`../design/environment-manager-user-stories.md`](../design/environment-manager-user-stories.md)
(this charter walked through five users, and the open questions they raised), and
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md) (the phases that
built it, and the unbuilt Linux guest).

---

<a id="the-one-sentence-answer"></a>

## The one-sentence answer: how is this different from SandVault?

It isn't, at the wall. The confinement is a small, bounded set of container flags on the
container backends, and on macOS a Seatbelt profile that says *"SandVault-parity"* about itself
(`macosuser.SeatbeltProfile`). The credential boundary is **code that isn't there** — `~/.ssh` is
absent because nothing mounts it — and a boundary defined by omission is necessarily small and
necessarily commoditizable.

The difference is everything *inside*: a locked package set, composed agent config, skills and
house rules, credential brokers, host-capability bridges — described declaratively, per
workspace, reproducibly. So:

> **yolo describes the environment an agent works in — reproducibly, declaratively, per
> workspace — and confinement is one attribute of the description. A jail is the strongest
> setting of that attribute, and the default.**

That ordering is the charter, and it is the opposite of the intuitive one ("a sandbox product with
an unusually good config-composition engine inside it"). Every tagline, summary and guide
describes yolo environment-first and confinement second. The name stays: `jail` is the name of
the strongest confinement level, so "yolo jail" reads as "yolo, at the jail level."

<a id="description-and-confinement"></a>

## Description and setup: the two inputs

What a user does with yolo is **describe the environment, then choose a named setup for it**.

```text
       DESCRIPTION                                    SETUP
  ┌──────────────────────────┐              ┌────────────────────────────┐
  │ tools      packages, mise│              │ podman-self                │
  │ agents     packs         │      ×       │ apple-container-self       │
  │ knowledge  skills, briefs│              │ seatbelt-self              │
  │ config     composed files│              │ seatbelt-yolo-account      │
  │ services   loopholes     │              │ host setups                 │
  │ layout     workspace, env│              │ fields: confinement + run_as│
  └──────────────────────────┘              └────────────────────────────┘
```

The description is the product. A setup names the technology and account that apply it; it is
not a free-form policy vector. `yolo -- claude` still runs the selected/default setup, with
credentials omitted by default.

<a id="the-confinement-dial"></a>

## The setup

Where the environment runs is selected by naming a **setup**: a named set of values with two
independent fields. `confinement` names the technology that sets the boundary; `run_as` names the
account. Built-ins include `podman-self`, `apple-container-self`, `seatbelt-self` and
`seatbelt-yolo-account`. `landlock-*` names are reserved but not built. `host-self` has no walls and
is run only through `yolo host`.

The ordinary launch's one named default is `podman-self` on Linux and `apple-container-self` on
macOS. It does not probe for a runtime or fall back to `host`. A person can choose another setup
for a launch with `--setup <name>`, set `YOLO_SETUP=<name>` for a shell, or select a default in
user config with `setup = "<name>"`. `yolo setup list` reports built-ins, user-defined setups and
the current selection. The user config alone may hold `setup` or `[setups.<name>]`; a project file
cannot choose where its agent runs or define a setup. A user may copy a setup into that config with
`yolo setup new <name> --from <setup>`.

The old `confinement` and `runtime` dials, the `macos_user_mode` selector and the launch flag
`--at` are removed, not aliases. Their refusals name the setup command to use. The former `guest`
value is replaced by the explicit Seatbelt setup names; a host choice remains explicit through
`yolo host`.

- **`host` is never inferred**, never auto-detected and never a fallback when a technology is
  missing. The ordinary launch's default is a container setup, and missing technology is a
  refusal naming an installed alternative where one is available.
- **Setup names never change meaning by OS.** `podman-self` means Podman on both platforms, while
  `seatbelt-self` is macOS-only; each OS has one named default, chosen without probing.
- **The setup is a pair, not a free-form policy vector.** `confinement` and `run_as` are
  independent; neither field derives its value from the other. `render.Profile` holds the
  enforcement primitives underneath them, but users choose a named setup rather than assembling
  those primitives directly ([`fill-the-matrix-principle.md`](fill-the-matrix-principle.md)).
- **The grants stay where they are.** `mounts`, `host_files`, `network.ports`, `devices`, `gpu`
  and loopholes remain project or user choices. A setup changes the walls, not the grants.

<a id="why-guest-not-sandbox"></a>

### Why the former `guest` name was retired

The earlier notch design called its middle option `guest`, but that name hid two different
choices on macOS: a separate account and Seatbelt confinement. Named setups make those independent
choices explicit as `seatbelt-self` and `seatbelt-yolo-account`; they do not use `guest` as a
current config value or command-line selector.

<a id="autonomy-is-a-confinement-policy"></a>

### Agent autonomy follows the setup's enforcement kind

Every agent pack's "run without permission prompts" settings are safe only where the selected
setup confines the agent. They are therefore **selected by the setup's enforcement kind, not
asserted by the pack everywhere**:

- **The enforcement kind carries an autonomy policy bit** (`render.Profile.AgentAutonomy`), beside
  its enforcement primitives. Container and Seatbelt setups enable it; `host` leaves it off so
  prompts stay on.
- **Each pack declares both postures as data**, in the dedicated `autonomy` kind: the config
  patches and launch flags that mean *autonomous*, and those that mean *guarded*. The policy
  selects which renders. It runs in both directions: claude, codex, agy, opencode and copilot are
  guarded by default and a container or Seatbelt setup loosens them, while pi is permissive by
  default and `host` tightens it.
- **Keys safe anywhere** (an auto-updater off, a trust dialog accepted) stay in ordinary
  `managed` config and are unconditional.
- **A container boot is byte-identical** with the policy on: its autonomous posture reproduces
  exactly what the packs rendered before the kind existed (the render fingerprint gate).

> [!WARNING]
> **Do not put a permission-bypass key in a pack's unconditional `config` or `launch`.** A
> no-walls setup can render that value into the agent's host configuration without a jail to
> contain its effect. The `autonomy` kind keeps the guarded and autonomous values separate so the
> selected enforcement kind chooses the posture ([`OQ-11`](#why-its-this-way)).

<a id="the-verbs"></a>

## The verbs

```text
yolo -- <cmd>                    run <cmd> in the selected setup
yolo --setup seatbelt-self -- c  choose a setup for one launch
yolo apply                       make this environment match its description, run nothing
yolo apply --setup host-self     … in your real home (also `yolo host apply`)
yolo apply --sealed    … and refuse if anything undeclared participates
yolo describe          print the resolved description
yolo check-deps        probe this machine for what the selected packs need
yolo check             is this environment healthy here?
```

There is deliberately **no top-level `diff`**: a definition that binds retires "compare my two
machines." `yolo config diff` keeps its per-surface job (captured against rendered), `yolo config
drift` tells an in-jail agent a restart is owed, and a cross-machine comparison is `describe
--json` on each side and `diff(1)`.

<a id="apply"></a>

### `apply`: make it so, without the run

`yolo -- <cmd>` is "apply, then exec." `apply` names the make-it-so half, so there is an answer to
"set up my environment but don't run anything," and an answer at all at the host notch, where
there is nothing to enter.

- **At `jail`**, `apply` is the launch `yolo -- true` performs — build the image, stage packs,
  render config, install every program the selected packs declare — and exits.
- **At `host`**, `apply` is the whole host-render feature: it writes the applicable config into
  the real home ([`host-agent-environment.md`](host-agent-environment.md#what-yolo-host-apply-renders-into-a-derived-surface)).
  Its run half is the separate verb `yolo host -- <cmd>`.
- **At a Seatbelt setup** it follows the launch path; a Linux `landlock` setup is not built.
- `--format json` and `--timing` belong to the host notch's apply alone, and every other route
  refuses them by name.

<a id="describe"></a>

### `describe`: the reproducibility claim, made checkable

`yolo describe` prints the resolved description: the environment, the notch and its mechanism and
primitive vector, the tools, the agents, the knowledge, the composed config, the services, the
grants, and a description hash. `--json` carries the canonical computed config (it absorbed
`config dump`), and `--hash` prints the pin for CI.

> [!WARNING]
> **An unsealed hash is worse than no hash**, because it looks authoritative while moving for
> reasons the user cannot enumerate. `describe` therefore prints the hash **marked** as unsealed,
> and `describe_test.go` pins the marker. Do not "clean up" the marker.

<a id="the-input-closure-and-sealing"></a>

### The input closure and sealing

Every input that shapes an environment falls in one of four tiers, by whether the definition
names it:

| Tier | Inputs | nix analogue |
| :--- | :--- | :--- |
| **Locked** (pinned) | nixpkgs and the image (`flake.lock`); the pack set with per-pack commit pins (`packs.lock.json`) | `flake.lock` revs |
| **Declared** (named by the definition) | `yolo-jail.jsonc`; pack `contributes[]` (surfaces, `defaults`/`managed`, `derive`); inline `env_sources` entries | `flake.nix` |
| **Declared-impure** (named, but the content is machine state) | the user config; `include_if_found` targets; `env_sources` dotenv files; `mise_tools` (versions declared, toolchains fetched); a surface's declared `host` read-in layer | a fixed-output derivation |
| **Undeclared** (participates, nothing names it) | `yolo-jail.local.jsonc` (auto-merged); the capture overlay; at the host notch, the PATH and the provider, region and override variables of the process that started yolo ([HE-DIR1](host-agent-environment.md#he-dir1), [HE-D10](host-agent-environment.md#he-d10)) | `--impure`, silently |

**The sealing rule, against that table: `--sealed` refuses the Undeclared tier and reports the
Declared-impure tier; the Locked and Declared tiers are the definition.** `applySealed` refuses a
workspace local config, any capture surface with outstanding overlay keys (naming `yolo config
promote` and `yolo config reset` as the two exits), a capture store it could not read, and an
unset or retired `host_management`.

- **`--sealed` is opt-in, not the default.** The escapes it forbids are good features: capture
  keeps an agent's mid-session edit from being discarded, and a local config lets one machine
  drop two packages without touching the shared file. Sealing is for the moments that need "the
  declaration, and only the declaration": a CI pin, a hand-off, a release.
- **Sealing is a host-side check.** The whole closure is assembled on the host before any
  container exists, so `--sealed` needs no container.
- **Sealing and the dependency probe are two guarantees.** Sealing proves the config was
  generated only from declared inputs; it does not prove the resulting environment has `psql`.
  At `jail` the two nearly coincide, because the toolchain is built from the locked image. Below
  `jail` there is no image, the toolchain is machine state outside any closure, and the
  [dependency probe](#dependency-provisioning) is what checks it.
- **The `host` read-in layer stays**, declared per surface and read fail-closed
  ([`OQ-EM2`](#why-its-this-way)). A declared, enumerable, fail-closed input satisfies the rule.

> [!IMPORTANT]
> **The criterion is nameability, never version control.** An input is declared when something
> in the definition names it. Whether a file is committed is not yolo's question: `--sealed` runs
> no VCS command and reads no ignore file, and must not start to. A sealing check that consulted
> an ignore file would make yolo's reproducibility story depend on a tool that is not one of its
> dependencies.

> [!WARNING]
> **Sealing does not mean "no host reads."** A named-but-impure input is nix's fixed-output
> derivation; banning it would break what packs exist to provide. The rule is *no undeclared
> input*.

<a id="check-at-a-notch"></a>

### `check` and setup selection — no one-off override built

`yolo check` validates the machine, user and workspace config, the selected/default setup and
its prerequisites. It has no one-off `--setup` override. The earlier proposal for
`check --at <notch>` was not built; `--at` is now refused and names `--setup` for commands that
support setup selection. `check` never writes. `yolo check-deps` and host apply report which
selected contributions do not apply.

<a id="dependency-provisioning"></a>

### Dependency provisioning: declare once, check once, hand off

Outside the container setups, a `packages` or `requires` declaration becomes a question about the host.

- **One declaration, owned by whoever introduced the need.** A pack declares the binaries it
  introduces, with per-package-manager `install_hints`; it does not re-declare what another
  tool's own doctor already owns.
- **One checker, shared by every caller.** The probe is `internal/depcheck`, run by the host
  apply, by the launch gate's survey and standalone by `yolo check-deps`. Its boundary is the
  declared schema a third-party doctor can read, not an importable Go package
  ([`OQ-8`](#why-its-this-way)). An unprobeable or unhinted entry is reported as such, never as
  present.
- **A runnable manifest.** `yolo check-deps` prints each missing dependency's install command for
  the detected package manager and writes a bundle manifest (a Brewfile and its kin under
  `~/.config/yolo/`) that installs them in one step. A tool with its own installer is left out of
  the manifest and its command printed beside it.
- **The offer to run it.** `yolo host apply --assert` prints each missing dependency's exact
  install command above **one** prompt; a decline is fatal at the prompt with nothing written,
  and an install that leaves the binary missing counts as a decline. There is no terminal test: a
  nil or end-of-file stdin answers no, and what protects the user is that the commands print
  above the prompt whether it is answered or not. There is no batching by elevation class and no
  second prompt ([`OQ-EM1`](#why-its-this-way)).

<a id="program-below-jail"></a>

### `program` below `jail`

A pack's `program` (an agent CLI it installs) is honored in every setup that can run it. In a
Seatbelt setup, the agent uses the same Mac-side launchers. At `host` the **host agent floor** installs
every program a selected pack declares into a yolo-owned host prefix, with no prompt: selecting
the pack in user config is the consent ([`host-agent-environment.md`](host-agent-environment.md#the-host-agent-floor)).
A program the floor delivers is never a dependency-gate blocker; the gate's one prompt covers the
`requires` needs that remain.

<a id="packs-across-notches"></a>

## Packs across notches

**The batteries are declarative, locked, and portable across confinement levels.** A pack is
data, read through one loader and rendered by one loop with no switch on any tool name, and a
pack author writes one manifest and never thinks about notches.

`render.HostFields` is the authority for which kinds and config keys apply below `jail`; `yolo
config-ref` gives the per-kind reason. Composed config, skills, briefing prose, static env, launch
flags, `files` (written as a real copy), `config-list`, `requires`, `autonomy` (the guarded
posture at `host`), `provider`, `adapter` and `profile` are honored at the host; `mount`,
`reads-host` and `state` are not, and `loophole` is refused because its only client is a
container.

- **A kind that cannot apply is refused by name, never emulated.** The host apply names every
  inapplicable kind in its report.
- **A copy is never a substitute for a mount**: it goes silently stale, so a pack update appears
  to apply and doesn't. Skills port anyway, because their delivery is a merge that no mount
  expresses.

<a id="the-briefing-states-the-notch"></a>

## The briefing states the notch

The generated briefing tells the agent which notch it is in, because an agent that believes it
is disposable when it is not will take a disposable agent's risks. `confinementHeader` writes the
level, the grants and the absences on top of the same pack prose: at `host`, "this is the human's
REAL machine … Changes are NOT disposable"; at `guest`, a restricted account with a real,
persistent home. The jail's header keeps its historical bytes, so no existing jail's rendered
briefing moves; the other notches add the primitive vector `describe` prints.

<a id="what-does-not-change"></a>

## What does not change

- **`yolo -- claude` in a fresh repo**: one command, jail, credentials omitted by default.
- **Confinement defaults to the strongest notch.** `host` is never inferred or a fallback.
- **Credential omission stays the default posture** at every notch that can express it.
- **Packs stay user scope.** A workspace config travels with the repo and is agent-editable, so
  it must not decide what prose an agent follows; a `host` notch makes that more load-bearing.
- **Reproducibility stays the differentiator**: locked nixpkgs, locked packs, and a definition
  that binds under `--sealed`.

---

## The separability test: could you use the config engine without a jail?

**Yes, for the engine.** `internal/agentcfg` is a leaf. Its complete transitive first-party
dependency set is its own subpackages plus the two codec helpers, and its only third-party
dependencies are a TOML decoder and `gopher-lua`. There are **zero** references to `internal/cli`,
`internal/entrypoint`, `internal/paths`, `internal/config`, mounts, containers, or anything
jail-shaped. Re-measure with:

```console
$ go list -deps ./internal/agentcfg/... | grep yolo-jail
```

And it *runs* jail-free: `yolo config render <agent>` executes host-side, composes from the real
host config file, and prints the result — no `/ctx` mount, no container. The engine never touches
the filesystem itself; path policy belongs to the CLI, which expands `~/` and resolves the workspace
placeholder before handing a surface over.

The surface **data** is jail-independent too, and it was not always: a surface once carried the
literal path `/workspace` in a defaults layer, which was a latent correctness bug on any run whose
workspace is elsewhere. The seam that fixed it is `agentcfg.WorkspacePlaceholder` (`${workspace}`),
substituted by `SubstituteWorkspace` at render time in both the boot path and `config render`. That
is also what un-blocks surfaces-as-pack-data: a pack cannot ship a jail-specific absolute path.

### What the jail adds is not capability but *two sides*

| Concern | With a jail | Without |
|---|---|---|
| where the `host` layer comes from | a `:ro` mount under `/ctx` | just the real host file |
| where the output goes | the jail's writable overlay | your actual `~/.claude/` — you are composing over your own config |
| the credential boundary | meaningful: two sides, one of which must not see the other's secrets | **meaningless** — there is only one side |
| capture sidecars | per-workspace, agent edits vs yolo renders | still coherent, but it is now *your* edits vs the tool's |

That last row is the finding: **composition is jail-independent; the credential boundary is not.** A
standalone "compose my agent config from layers" tool is a coherent product — roughly
*chezmoi/home-manager for agent config*. It is just not a *sandbox* feature, and the
user-scope-only rule on `host_files` would have nothing to enforce.

## What is jail-shaped, and what only looks it

Almost nothing outside the confinement policy is *fundamentally* jail-shaped: not the packages, not
the surfaces, not the skills, not the briefings, not the loopholes. Half the config surface consists
of **grants** — ways to poke a hole through a wall — which presuppose a wall without creating one,
and go **inert rather than wrong** when the wall is absent. That inertness is the property that
makes the notches coherent at all.

Reproducibility is the one thing that is not commoditizable-adjacent. Another image format could
supply the same binaries, but nix is load-bearing for a property nothing else here provides — a
*reproducible* environment, which is what makes "the same jail on two machines" true rather than
aspirational.

So the pack system is **also** not fundamentally a jail feature. It is an agent-config distribution
mechanism that yolo happens to be a good host for. Worth knowing before deciding how much of yolo
becomes packs.

## The three code-execution seams

yolo ships logic three ways, and a new mechanism almost always belongs in one of them rather than
being invented. Each has a real precedent to copy.

| Seam | What executes | Isolation |
| :--- | :--- | :--- |
| **Lua** (`internal/agentcfg/luahook`) | a script, in-process | **Strong.** `openSandboxLibs` opens base, table, string and math and nothing else — `package`, `os`, `io`, `debug`, `coroutine` and `channel` are deliberately never opened — under an instruction budget. Pure computation: it cannot read a file or spawn a process. |
| **Loopholes** (`internal/loopholes`) | a host or jail daemon named by a manifest | None by design, but the command is an argv `[]string` rather than a shell string, so there is no shell-injection surface. A loophole's own directory is bind-mounted `:ro` into the jail, and a loophole shipping and running its own script is a *tested* contract. |
| **MCP servers** | arbitrary executables, installed on first boot | None. **This is already pack-shipped third-party logic in production** — and it is the ecosystem's own model: fetched at runtime, cached in a writable overlay, invoked over a JSON protocol. |

The third row answers "is there a story for shipping compiled third-party code?" — there already
is, and the same shape recurs in the lazy agent launchers and the LSP installs. A pack that needs
real logic should look like an MCP server, not like a plugin.

## Traps

> [!WARNING]
> **Do not propose Go plugins (`plugin.Open`). The finding is a measurement, not a preference.**
> `plugin` requires CGO and the shipped binaries are built `CGO_ENABLED=0`, so `plugin.Open` returns
> `plugin: not implemented`. Forcing CGO on does not rescue it: the hermetic build also passes
> `-trimpath`, and a non-trimpath plugin fails with *"plugin was built with a different version of
> package internal/goarch"*. Underneath both is a version lock — a plugin must match the host
> binary's Go version and every dependency version exactly, and yolo is built by nix while packs are
> not — so flipping CGO would buy a mechanism that still breaks on every toolchain bump.

> [!WARNING]
> **Nix hermeticity is "no *unpinned* network", not "no network".** Fixed-output derivations and
> builtin fetchers are allowed to reach out, because an output hash pins the result — and `flake.nix`
> already relies on this for pinned-nixpkgs specs and fetched source tarballs. So "we cannot fetch
> that at build time" is the wrong reason to reject something. The right reason is **cost and
> coupling**: pack *content* as an image input puts the pack in the image's store path, so every pack
> edit costs a full rebuild plus a host load — which destroys the point of packs. A pack needing a
> system **capability** is different, and feeds the derivation exactly as a `packages` entry does.

> [!WARNING]
> **The `goSrc` fileset is a constraint on *linking*, not on language.** The hermetic build sees only
> `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/` and `packs/`, so a Go package outside that set
> vanishes. That rules out exactly one thing: linking third-party Go code into the yolo binary. A
> separate binary, built separately and invoked over a protocol, is unaffected — which is why "pack
> logic can't be Go" is false. See [`third-party-pack-logic.md`](third-party-pack-logic.md).

<a id="what-it-costs"></a>

## What it costs

- **Every feature needs a per-notch ruling.** "Does it run in the container?" no longer answers
  scope questions for free. The mitigation is that the matrix is legible — the host apply names
  what does not apply, and `config-ref` says why — rather than implicit.
- **A wider identity has more neighbors.** As the agent environment manager, yolo is compared to
  home-manager, chezmoi, devcontainers and mise; the bet is that the batteries are the part that
  is ours.
- **`host` will be over-used.** It is faster and it has the user's real credentials. The counter
  is that `host` is a reduced environment that says what it cannot do, with the agent's prompts
  left on; this is a product-discipline risk with no technical fix.
- **A Linux Landlock setup must be built before it is a supported launch choice.** Its names are
  reserved, but Linux users currently use `podman-self` for a confined container.

<a id="not-built"></a>

## Not built

- **A one-off setup override for `yolo check`.** The command checks the resolved selection; there
  is no `check --setup` flag.
- **Linux Landlock setups.** `landlock-self` and `landlock-yolo-account` are reserved names, not
  working launch choices. A refusal names `podman-self` as the nearest built setup.
- **The questions the user stories raised** — capture against the closure, a shortfall as
  success, briefing stamping, a confinement floor for an organization, the exposure view — live
  in [`../design/environment-manager-user-stories.md`](../design/environment-manager-user-stories.md),
  by id, and are not duplicated here.

## What this does not license

- **Not a licence to extract `agentcfg` today.** The engine being a leaf is an argument for
  extracting the *engine* if reuse is the goal — not for extracting the *agent support*, which is a
  different project and the only one that needs the pack machinery.
- **Not a claim that the wall is unimportant.** It is small, well-understood and largely borrowed;
  that is a statement about where the novelty is, not about whether the wall matters. A jail is
  still the default, and still what nearly everyone wants nearly all the time.
- **Not a claim that a grant is meaningful without a wall.** Grants go inert at the host notch. A
  feature that reads as "poke a hole" and does something *else* when there is no hole is a bug in
  that feature, not an exception to the model.
- **Not a user-facing primitive vector.** The primitives compose underneath; the three presets
  are the interface.

<a id="why-its-this-way"></a>

## Why it's this way

The rulings below keep their ids because other documents and code comments cite them. The rows
without an `EM` prefix carry the environment-manager plan's ids, settled with this charter; the
[plan's own ledger](../plans/environment-manager-plan.md#decision-ledger) holds its other rulings,
including the two reversed on 2026-09-12 (retiring the read-in `host` layer, and pure `rmw` as the
host notch's only mode).

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| OQ-11 | A pack encodes its two autonomy postures as a dedicated `autonomy` kind, not a `when:` discriminator on `config` and `launch` (2026-08-01) | It keeps confinement-conditional keys physically out of the unconditional `config`, so a jail-bypass key cannot be left in the always-on half by accident, and it prints whole in `describe` and `footprint`. |
| OQ-8 | The dependency checker's boundary is a declared schema (`install_hints`) a third-party doctor can read, not an importable Go package (2026-08-01) | A schema is portable to a project whose doctor is not Go; a Go package is worth it only if the probe grows subtle enough that a spec under-specifies it. |
| OQ-EM1 | No elevation-class batching and no second prompt: the dependency gate prints each install's exact command above one prompt, so a `sudo` is visible before the answer (2026-09-30, answered by [`OQ-HP5`](host-agent-environment.md#OQ-HP5)) | The maintainer rejected asking again for what a selected pack declares: *"If pi is going to be available, it's because you've already configured that pack in your config file. You've acknowledged this already. We don't have to ask again … certainly not the second time."* |
| OQ-EM2 | The read-in `host` layer stays, declared per surface (`manifest.Surface.ReadsHost`) and read fail-closed (2026-09-11; supersedes the plan's [`OQ-3`](../plans/environment-manager-plan.md#decision-ledger), which would have retired it) | Deciding the layer's binding, failure direction and coverage decided that it exists ([`OQ-CO10`, `OQ-CO11`](config-ownership.md#why-its-this-way)). A declared, enumerable, fail-closed input satisfies sealing's rule, and the hand-maintained provenance map that once argued for retirement is deleted: both provenance columns are derived. |
