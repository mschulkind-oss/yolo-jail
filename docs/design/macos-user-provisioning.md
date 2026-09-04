---
title: "macos-user has no floor and no provisioning stage"
status: draft
date: 2026-09-04
tags: [macos-user, provisioning, packages, mise, backend-parity]
summary: "Every imperative provisioning step the container path runs — mise install, the LSP/MCP npm installs, the agent CLI installers — is missing on macos-user, and so is the package floor those steps need to run at all. Two separable halves, in that order: give the noncontainer profile a core set, then run the same stage. The open questions are how much floor, whether it is GNU or BSD, and where the stage's state lives given one shared home."
---

# macos-user has no floor and no provisioning stage

**Status:** DESIGN SKETCH, 2026-09-04. Nothing built. Four open questions, all
needing a ruling before this is buildable.

**The short version.** A container jail gets its tools two ways: an **image floor**
of ~19 packages baked by the flake (git, node, mise, ripgrep, fd, python…), and an
**imperative stage** the launch runs inside the jail (`mise install`, the generated
`~/.yolo-bootstrap.sh` that npm-installs LSP servers and MCP presets). macos-user
has *neither*. It has only `packages:`, realized natively, containing exactly what
the user declared and nothing else. So `mise_tools`, `lsp_servers`, `mcp_presets`
and the lazy agent-CLI installers all render config and install nothing — silently
until 2026-09-04, when the last of them gained a warning. The fix is two halves in
strict order, because the stage cannot run without the floor: **give the
noncontainer profile a core set, then run the same stage inside the sandbox.**

**The most important section is §6** — the alternatives — because "the same as
everywhere else" has a cost on this backend that it does not have in an image, and
the ruling turns on whether that cost is worth paying.

**Reads with:** [`../reference/nix-across-backends.md`](../reference/nix-across-backends.md)
(what nix produces for each backend, and why the image is a floor),
[`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the single shared home,
which OQ-P3 depends on),
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend).

---

## 1. The two missing halves

They are separable, and they are ordered. Naming them apart is most of the design,
because a fix that addresses one is not a partial fix — it is no fix.

**The floor.** `flake.nix` builds the image as `corePackages ++ extra`
(`flake.nix:904`), where the core is ~19 packages including `git`, `nodejs_24`,
`mise`, `ripgrep`, `fd`, `python3` and the GNU userland (`flake.nix:849-870`). The
native profile is `noncontainerPackages` — the user's declared list and **nothing
else** (`flake.nix:481-487`). There is no core.

**The stage.** `setupScript` (`internal/cli/run/command.go:19-30`) runs, inside the
jail, on every container launch: a store prune, `mise install --quiet`, then
`~/.yolo-bootstrap.sh` (the generated script that npm-installs LSP servers and MCP
presets) and `~/.yolo-venv-precreate.sh`. It is part of the **container command
wrapper**. The macos-user launch argv goes straight to `sandbox-exec … zsh -l`; it
runs no stage, and `RunDarwinBootstrap` does not even *generate* the bootstrap
script — `GenerateBootstrapScript` has zero call sites in
`internal/entrypoint/darwin.go` (verified 2026-09-04).

**Verified on the machine, not inferred.** The real sandbox home
(`/Users/_yolojail`) has `.config/opencode` and `.local/share/claude` and nothing
else: no mise data dir, no mise config, no npm prefix. No `mise` binary exists on
any path the sandbox can read — the host's is at `/opt/homebrew/bin`, which is not
on `SandboxPath` and whose state lives under `/Users`, which the profile denies.

## 2. What this costs today

| Config key | Container | macos-user | Told? |
| :--- | :--- | :--- | :--- |
| `mise_tools` | installed by the stage | nothing; shims dir on PATH so it *looks* provisioned | warns (2026-09-04) |
| `lsp_servers` | npm-installed by the stage | config renders, binaries absent | warns |
| `mcp_presets` | npm-installed by the stage | wrappers skipped | warns |
| agent CLIs (lazy launchers) | launcher execs npm/native installer | launcher generated, but no node and no npm to run it | **silent** |
| `packages:` | baked into the image | realized natively | works |

The last row is the tell: the one mechanism that works on this backend is the
declarative one, and it works because it is the only one that never needed a
runtime to already be present.

> [!WARNING]
> The agent-CLI launchers are the sharp edge and are still unwarned. They are
> generated (`generate_agent_launchers` runs in the darwin bootstrap), they sit on
> PATH, and they fail at the moment an agent is invoked rather than at launch — so
> the failure lands on the user's first real command, not on the launch they could
> have read. Fixing the warning is cheap and should not wait for this design.

## 3. Principles

**P1. A backend either provides a mechanism or refuses it out loud.** Rendering the
config for a mechanism that does nothing is the failure this whole document is
about; it has produced five instances and cost a day each time.

**P2. The declarative path is the one that composes.** `packages:` works here
precisely because nix needs nothing pre-installed. Every imperative installer
assumes a runtime that something else put there.

**P3. Convergence beats a second dialect.** Two ways to say "install neovim"
depending on backend is a tax on every user and every doc. Where the backends can
run the same step, they should run the same step.

## 4. The proposed shape

**Half one: a core set for the noncontainer profile.** `yoloNoncontainerPackages`
gains a core list, the way `ociImage` has one — the same attr, evaluated for the
native system. Minimum viable core is whatever the stage needs to run: `mise` and
`nodejs`. Whether it extends to the rest of the image's floor is **OQ-P1**.

**Half two: a provisioning stage inside the sandbox.** The macos-user launch grows
a step that runs the same `setupScript` body, inside the Seatbelt sandbox, as the
sandbox user, before the agent command. `RunDarwinBootstrap` starts generating
`~/.yolo-bootstrap.sh` so there is something to run.

Ordering is not a preference: `mise install` needs `mise`, and the bootstrap script
needs `npm`. Half two without half one is a script that fails on its first line.

**Trigger:** every launch, like the container's, and idempotent for the same
reason — the config can change between launches and the jail must reflect it.
**Failure:** a failing stage must not abort the launch. The container path tees to
`<workspace>/.yolo/startup.log` and marks `PROVISIONING FAILED`, which the briefing
then reports (`ProvisioningFailed` is already a `BriefingInput` field); macos-user
should do the same rather than refusing to start, because a jail with a missing LSP
server is still a usable jail. **Degenerate input:** no `mise_tools`, no
`lsp_servers`, no `mcp_presets` and no agent packs → the stage is skipped entirely,
so a bare `yolo -- bash` pays nothing.

## 5. What this does NOT propose

- **Not an image for macos-user.** The absence of one is the backend's whole
  reason to exist. A floor is a nix profile, not a filesystem.
- **Not changing the container path.** Every claim here is about giving macos-user
  what the container already has.
- **Not a second config surface.** No `macos_packages`, no per-backend `mise_tools`.
  If a package is Linux-only, `platforms: ["linux"]` already says so.
- **Not fixing the shared home.** The stage will write into it, which makes
  OQ-P3 real, but the split itself is `macos-user-home-tiers.md`.

## 6. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **A. Floor + stage** (§4) | **Recommended.** The only one that satisfies P3. Costs a native core closure and the questions below. |
| **B. Declarative only** — delete the imperative surfaces on this backend, refuse `mise_tools`/`lsp_servers`/`mcp_presets` loudly, tell users to write `packages:` | **Rejected, but it is the honest runner-up.** It satisfies P1 and P2 fully and costs nothing to build — today's warnings are already 80% of it. It fails P3: a user with one config across a Mac and a Linux host would need two spellings of the same intent. Revisit if the core closure in A proves painful. |
| **C. Status quo + warnings** (what ships today) | **Rejected as an end state**, accepted as the interim. It is honest and it is not a backend anyone can use for real work. |
| **D. Floor only** — core packages, no stage | **Rejected.** Puts `mise` on PATH and never runs `mise install`, which is a worse lie than the current absence: the tool exists and reports nothing to do. |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A core package has no native darwin build | It is the same `yoloUnavailablePackages` mechanism `packages:` uses — but for a CORE package a skip must be **fatal**, not warned: a floor with a hole in it is not a floor. |
| First launch builds a large closure natively | One-off per machine; nix caches. Cachix already applies (`--accept-flake-config`). Measure before assuming it is a problem. |
| The stage's state lands in the shared home | Real, and it is OQ-P3. |
| GNU-vs-BSD userland surprise | OQ-P2. |

## 8. Sequencing

Ship the unwarned agent-launcher case first — it is independent of every question
below and it is the one failure that lands on a user's first real command. Then
half one, gated on OQ-P1 and OQ-P2. Then half two, gated on OQ-P3. Half two is
worth nothing before half one, so there is no partial-credit ordering to be clever
about.

## Open Questions

1. 💬 **OQ-P1: How much floor?** The minimum that makes the stage run is `mise` and
   `nodejs`. The maximum is the image's ~19-package core. Everything between is
   available.

   _Leaning:_ Start at the minimum plus `git` — `git` because a jail without it is
   not a development environment and the Mac's `/usr/bin/git` is an Xcode shim the
   user may not have. Add on demand. A large floor here costs a native build on a
   machine that is not building an image, which is the thing this backend exists to
   avoid.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-P2: GNU userland or the Mac's own?** The image's core deliberately bakes
   `coreutils-full`, `gnused`, `gnugrep`, `gawk` so a jail behaves the same
   everywhere. On macos-user those would sit ahead of the BSD tools the human's own
   shell uses. This decides whether a script that works in the jail works in the
   human's terminal on the same machine.

   _Leaning:_ **No GNU userland.** The consistency argument is real but this
   backend's whole proposition is "your Mac, confined" — an agent whose `sed -i`
   behaves differently from the human's is a surprise in the direction that costs
   more. Revisit if a pack turns out to depend on GNU behavior.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-P3: Where does the stage's state live, given one shared home?** `mise
   install` writes to `MISE_DATA_DIR` and npm to a prefix, both under the sandbox
   home — which is machine-wide here. Two workspaces with different `mise_tools`
   would fight, and the second launch would silently reshape the first's toolchain.
   This is the same collision `macos-user-home-tiers.md` describes for pack state,
   arriving through a different door.

   _Leaning:_ Block half two on the home split rather than shipping a known
   collision. The alternative — a per-workspace `MISE_DATA_DIR` under the shared
   home — repairs this one case and leaves the general problem, which is how the
   backend accumulated five of these.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-P4: Does the stage run before the agent, or on demand?** The container
   runs it unconditionally before the command. macos-user could instead let the
   lazy launchers trigger it, which pays nothing on a `yolo -- bash`.

   _Leaning:_ Unconditional, matching the container — the launch already prints what
   it is doing, and a first-command stall is worse than a launch stall. But this is
   a taste call about where the wait lands.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

_(empty — no questions settled yet)_
