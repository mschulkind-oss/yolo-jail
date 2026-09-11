---
title: "macos-user has no floor and no provisioning stage"
status: in-review
date: 2026-09-04
tags: [macos-user, provisioning, packages, mise, backend-parity]
summary: "Every imperative provisioning step the container path runs — mise install, the LSP/MCP npm installs, the agent CLI installers — is missing on macos-user, and so is the package floor those steps need to run at all. Two separable halves, in that order: give the noncontainer profile a core set, then run the same stage, confined, inside the sandbox. Where the stage's state lives is settled by the container's own partition; how much floor and whether it is GNU or BSD are the two rulings left."
---

# macos-user has no floor and no provisioning stage

**Status:** DESIGN, 2026-09-11 (DESIGN SKETCH 2026-09-04). Nothing built. Audited
against the tree at `61c26c18` on 2026-09-11; two of its four questions are settled and
compacted into the [Decision Ledger](#decision-ledger). **[`OQ-P1`](#OQ-P1) and
[`OQ-P2`](#OQ-P2) need a ruling**, and both are the maintainer's.

> **In short.** A container jail gets its tools from an image **floor** and an
> imperative **stage**; macos-user has neither, so four config keys render and install
> nothing. The fix is the same floor (smaller) and the same stage (confined), and the
> stage's state goes exactly where the container already puts it — machine-wide
> for mise's data, per-workspace for everything else.

**Why it matters.** `mise_tools`, `lsp_servers`, `mcp_presets` and the lazy
agent-CLI installers all render config and install nothing — three of them warn,
the fourth still fails silently on the user's first real command
([§2](#2-what-this-costs-today)).

**The shape.** Half one adds a core set to the noncontainer nix profile. Half two
runs the container's `setupScript` body as a **new, Seatbelt-confined step** between
the bootstrap and the agent ([§4](#4-the-proposed-shape)).

**Cost.** A native darwin closure for the floor, built once per machine; and the
stage cannot ship before the home split lands the per-workspace surfaces it writes
into ([`OQ-P3`](#decision-ledger)).

**Start at [§6](#6-alternatives)** — "the same as everywhere else" has a cost on this
backend it does not have in an image, and the ruling turns on whether it is worth
paying.

**Needs your ruling:** [`OQ-P1`](#OQ-P1), [`OQ-P2`](#OQ-P2).

> [!NOTE]
> **Terms coined here.** The **floor** is the set of packages present in a jail
> before any config asks for anything — the image bakes one, the native profile does
> not. The **stage** is the imperative provisioning step a launch runs *inside* the
> jail, after the floor exists and before the agent starts. Neither is a yolo config
> key and neither appears in the code under these names; they are this document's
> words for two things that had none, which is why the gap between them was never
> stated as one problem. ⚠ The stage is *not* the darwin bootstrap: that runs yolo's
> own generators, installs nothing, and — measured 2026-09-11 — runs **outside** the
> Seatbelt profile ([§1](#1-the-two-missing-halves)).

**Reads with:** [`../reference/nix-across-backends.md`](../reference/nix-across-backends.md)
(what nix produces for each backend, and why the image is a floor),
[`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the home split, whose
[§5](macos-user-home-tiers.md#5-the-proposal) supplies [`OQ-P3`](#decision-ledger)'s answer and
whose [`OQ-HT2`](macos-user-home-tiers.md#OQ-HT2) is the one ruling still between
this doc's half two and buildable), and
[`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md) (the backend).

---

## 1. The two missing halves

They are separable, and they are ordered. Naming them apart is most of the design,
because a fix that addresses one is not a partial fix — it is no fix.

**The floor.** `flake.nix` assembles the image root as
`[ variantBinPathLinks ] ++ corePackages ++ variantFullPackages ++ extraPackages`,
where `corePackages` is `[ jailPrefixLinks imageIdentity ] ++ corePackagesFromNixpkgs`
and `corePackagesFromNixpkgs` is the list that matters here: **36 nixpkgs entries**
when counted 2026-09-11 — `bashInteractive`, `coreutils-full`, `git`, `ripgrep`, `fd`,
`curl`, `cacert`, `mise`, `findutils`, `which`, `nodejs_24`, `python3`, `go`, `neovim`,
`gh`, `gnused`, `gnugrep`, `gawk`, `gnupatch`, `diffutils`, `gzip`, `bzip2`, `xz`,
`gnutar`, `unzip`, `zip`, `zlib`, `procps`, `overmind`, `jq`, `uv`, `iptables`, `socat`,
`sox`, `openssl`, `tzdata`. The native profile is `noncontainerPackages` — the user's
declared list, minus what has no native build, and **nothing else** (its sole input is
the `YOLO_EXTRA_PACKAGES` config list). There is no core.

> ⚠ **Retracted (2026-09-11):** the first draft cited three `flake.nix` line ranges and
> "~19 packages". All three ranges were stale and the count was wrong by half. The
> attribute names above are the anchors now; a line number in a 1,600-line flake is the
> kind of claim that is wrong within weeks.

**The stage.** `setupScript` (`internal/cli/run/command.go:19-30`) runs, inside the
jail, on every container launch: a store prune, `mise install --quiet`, then
`~/.yolo-bootstrap.sh` (the generated script that npm-installs LSP servers and MCP
presets) and `~/.yolo-venv-precreate.sh`. It is part of the **container command
wrapper**. On macos-user, `RunMacosUser` runs exactly two things after staging
(`internal/macosuser/orchestrator.go:432-440`): the darwin bootstrap, then the agent.
Neither is a stage:

- **The bootstrap** is `sudo --user=_yolojail /usr/bin/env -i … <stagedYolo> internal
  darwin-bootstrap` (`internal/macosuser/runplan.go:103-110`) — yolo's own generators,
  run as the sandbox user **with no `sandbox-exec`**, i.e. outside the Seatbelt
  profile. It renders config and installs nothing; `GenerateBootstrapScript` has no
  call site on this path (its only production caller is the container boot loop,
  `internal/entrypoint/boot.go:536` — verified 2026-09-11).
- **The launch** is `sudo --login --set-home --user=_yolojail env -i … sandbox-exec -f
  <profile> -- /bin/zsh -c 'cd <ws> && exec <agent>'` (`macosuser.go:482-516`) —
  straight to the agent.

> ⚠ **Retracted (2026-09-11):** the first draft said the launch goes to `zsh -l`; it is
> `zsh -c` under `sudo --login`. Cosmetic — but the same paragraph implied the bootstrap
> ran under the sandbox, and that is the load-bearing error: half two is a **new
> confined step**, not a line added to an already-confined one
> ([§4](#4-the-proposed-shape)).

**Verified on the machine, not inferred — and one observation retracted.** No `mise`
binary exists on any path the sandbox can read — the host's is at `/opt/homebrew/bin`,
which is not on `SandboxPath` and whose state lives under `/Users`, which the profile
denies. No mise *data* dir exists (nothing creates one). ⚠ **But "no mise config" was
wrong**: `RunDarwinBootstrap` runs `ConfigureMisePrism` on every launch
(`internal/entrypoint/darwin.go:85`), and it always emits a `[tools]` table
(`internal/entrypoint/prism_mise.go:65-82`), so `~/.config/mise/config.toml` *is*
written — into the shared home, carrying this workspace's `mise_tools`. The step has been
on the darwin path since 2026-07-21 (`731dbe56`), so the 2026-09-04 listing missed it or
the home had been recreated since; re-measure before citing the
directory listing again.

## 2. What this costs today

| Config key | Container | macos-user | Told? |
| :--- | :--- | :--- | :--- |
| `mise_tools` | installed by the stage | nothing; shims dir on PATH so it *looks* provisioned; `~/.config/mise/config.toml` written to the shared home | warns, host-side (`internal/cli/run/loopholeinert.go:309-315`, since 2026-09-04) |
| `lsp_servers` | npm-installed by the stage | config renders, binaries absent | warns, host-side (`loopholeinert.go:316-321`) |
| `mcp_presets` | npm-installed by the stage | wrappers skipped | warns — **in the bootstrap only** (`darwin.go:100-105`), so `--dry-run` never shows it |
| agent CLIs (lazy launchers) | launcher execs npm/native installer | launcher generated, but no node and no npm to run it | **silent** — `GenerateAgentLaunchers` has no runtime precondition (`internal/entrypoint/shims.go`, verified 2026-09-11) |
| `packages:` | baked into the image | realized natively | works |

The last row is the tell: the one mechanism that works on this backend is the
declarative one, and it works because it is the only one that never needed a
runtime to already be present. **It is also the escape hatch the warnings already
point at**: `mise` and `nodejs` in `packages:` give a user the minimum floor today,
and the `mise_tools` warning says so verbatim.

> [!WARNING]
> The agent-CLI launchers are the sharp edge and are still unwarned (re-verified
> 2026-09-11). They are generated (`generate_agent_launchers` runs in the darwin
> bootstrap, `darwin.go:78`), they sit on PATH, and they fail at the moment an agent
> is invoked rather than at launch — so the failure lands on the user's first real
> command, not on the launch they could have read. Fixing the warning is cheap and
> should not wait for this design.

## 3. Principles

**P1. A backend either provides a mechanism or refuses it out loud.** Rendering the
config for a mechanism that does nothing is the failure this whole document is
about; it has produced five instances and cost a day each time.

**P2. The declarative path is the one that composes.** `packages:` works here
precisely because nix needs nothing pre-installed. Every imperative installer
assumes a runtime that something else put there.

**P3. Convergence beats a second dialect.** Two ways to say "install neovim"
depending on backend is a tax on every user and every doc. Where the backends can
run the same step, they should run the same step. This is the local form of the
parity constraint stated in review 2026-09-11
([`macos-user-home-tiers.md` §5.0](macos-user-home-tiers.md#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend)):
one mechanism on every backend, and only the primitive enforcing the boundary may
differ.

**P4. Third-party installers run confined; yolo's own generators may not need to.**
*(coined here, from the measurement in [§1](#1-the-two-missing-halves).)* The
bootstrap runs outside Seatbelt and that is tolerable because it executes only yolo's
code against a root-owned staged tree. The stage runs `npm install` postinstall hooks
and mise plugins — vendor code — and the container runs it inside the jail. Running it
unconfined here would be a regression the container never had.

## 4. The proposed shape

**Half one: a core set for the noncontainer profile.** `yoloNoncontainerPackages`
gains a core list, the way the image has one — the same attr, evaluated for the
native system. Minimum viable core is whatever the stage needs to run: `mise` and
`nodejs`. Whether it extends toward the image's 36 is **[`OQ-P1`](#OQ-P1)**.

**Half two: a provisioning stage inside the sandbox.** The macos-user launch grows a
**third** step between the bootstrap and the agent (`orchestrator.go:432-440`): the
same `setupScript` body, run as the sandbox user **under `sandbox-exec -f <profile>`**
— the profile is already installed before the bootstrap (`orchestrator.go:417`), so
nothing new has to exist for the stage to be confined, and a separately-launched
`sandbox-exec` process is not the nested-profile case `sandbox_apply` refuses
([`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md),
*Seatbelt does not nest a different profile*). `RunDarwinBootstrap` starts generating
`~/.yolo-bootstrap.sh` so there is something to run.

Ordering is not a preference: `mise install` needs `mise`, and the bootstrap script
needs `npm`. Half two without half one is a script that fails on its first line.

- **Trigger: every launch, unconditionally, before the agent** — like the container's,
  and idempotent for the same reason: the config can change between launches and the
  jail must reflect it ([`OQ-P4`](#decision-ledger)). **Not on demand**, and not as a
  matter of taste: the lazy launchers exist for agent CLIs and cover neither
  `mise_tools` nor LSP servers, so triggering off them would leave one config with
  tools absent on this backend and present on podman — the second dialect P3 in
  [§3](#3-principles) forbids. The container already splits the two the right way,
  staging eagerly and installing agent CLIs lazily; this backend takes the same split.
- **Failure: a failing stage must not abort the launch.** The container path tees to
  `<workspace>/.yolo/startup.log` and marks `PROVISIONING FAILED`, which the briefing
  then reports — and **the reader half is already wired on this arm**:
  `refreshJailBriefings` runs on the macos-user branch (`run.go:397`) and fills
  `ProvisioningFailed: jailcontent.ReadProvisioningFailed(o.Workspace)`
  (`internal/cli/run/prepare.go:147`). Only the emitter is missing, and it needs one
  seam: the `startupLog` constant is rooted at the container's fixed `/workspace` bind
  (`command.go:32`) and must be rebound to the real workspace's `.yolo/` sidecar here,
  the way `YOLO_DARWIN_WORKSPACE` already rebinds the workspace for the generators
  (`darwin.go:59-61`).
- **Degenerate input:** no `mise_tools`, no `lsp_servers`, no `mcp_presets` and no agent
  packs → the stage is skipped entirely, so a bare `yolo -- bash` pays nothing.
- **Concurrency:** two launches on one workspace run two stages against one sidecar; the
  container serializes that with a courtesy flock (`internal/cli/run/flock.go:41-57`)
  whose one production caller is inside `runContainer` — taking it on this arm is a
  moved call, sequenced with the stage.

**Where the stage's state lands** is settled by [`OQ-P3`](#decision-ledger) and is the
container's own partition, verified 2026-09-11 in `internal/cli/run/assemble_parts.go`:

| State | Container | macos-user, after the home split |
| :--- | :--- | :--- |
| mise data (`installs/`, `shims/`) | machine-wide: `MISE_DATA_DIR=/mise`, a store dir or named volume (`assemble.go:814`, `assemble_parts.go:172-176`) | machine-wide: `MISE_DATA_DIR` set **explicitly** to a path in the account home outside `~/.local` — the unset default `$HOME/.local/share/mise` (`internal/entrypoint/env.go:161-165`) would fall inside the per-workspace `~/.local` symlink |
| mise config (`~/.config/mise/config.toml`) | per-workspace: `config` bind (`assemble_parts.go:119`) | per-workspace: the `config` sidecar symlink |
| npm prefix (`~/.npm-global`) | per-workspace: `npm-global` bind (`:108`) | per-workspace: sidecar symlink |
| agent CLI installs (`~/.local`) | per-workspace: `local` bind (`:109`) | per-workspace: sidecar symlink |

> [!WARNING]
> **Sharing mise's DATA dir between workspaces is not a collision, and the fear that it
> was is refuted** (checked 2026-09-11). `installs/<tool>/<version>` is keyed by tool and
> version (`internal/cli/run/command.go:21`), so two workspaces asking for different
> tools *add* to the store rather than reshape it — which is why every container backend
> shares it machine-wide to begin with. The collision that is real is mise's **config**,
> and it is already live today with no stage at all ([§1](#1-the-two-missing-halves)).
> Do not "fix" the data dir by making it per-workspace: that is the inverse of every
> other backend ([§5](#5-what-this-does-not-propose)).

## 5. What this does NOT propose

- **Not an image for macos-user.** The absence of one is the backend's whole
  reason to exist. A floor is a nix profile, not a filesystem.
- **Not changing the container path.** Every claim here is about giving macos-user
  what the container already has.
- **Not a second config surface.** No `macos_packages`, no per-backend `mise_tools`.
  If a package is Linux-only, `platforms: ["linux"]` already says so
  (`internal/config/derived.go`, `EffectivePackages`; the refusal message at
  `orchestrator.go:379-396` names the spelling).
- **Not fixing the shared home.** The stage will write into it, which is what makes
  [`OQ-P3`](#decision-ledger) depend on the split, but the split itself is
  [`macos-user-home-tiers.md`](./macos-user-home-tiers.md).
- **Not a per-workspace `MISE_DATA_DIR`.** The first draft named it as the
  alternative to blocking on the split. It is doubly wrong: a mechanism no other
  backend has, *and* the inverse of the container's partition, which shares mise's
  data machine-wide by design.
- **Not running the stage unconfined**, even though the bootstrap is (P4).

## 6. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **A. Floor + stage** ([§4](#4-the-proposed-shape)) | **Recommended.** The only one that satisfies P3. Costs a native core closure and the two questions still open. |
| **B. Declarative only** — delete the imperative surfaces on this backend, refuse `mise_tools`/`lsp_servers`/`mcp_presets` loudly, tell users to write `packages:` | **Rejected, but it is the honest runner-up.** It satisfies P1 and P2 fully and costs nothing to build — today's warnings are already 80% of it, and the `mise_tools` warning already tells users to do exactly this. It fails P3: a user with one config across a Mac and a Linux host would need two spellings of the same intent. Revisit if the core closure in A proves painful. |
| **C. Status quo + warnings** (what ships today) | **Rejected as an end state**, accepted as the interim. It is honest and it is not a backend anyone can use for real work. |
| **D. Floor only** — core packages, no stage | **Rejected.** Puts `mise` on PATH and never runs `mise install`, which is a worse lie than the current absence: the tool exists and reports nothing to do. |
| **E. Stage on demand** — let the lazy launchers trigger it | **Rejected**, settled by [`OQ-P4`](#decision-ledger): its only benefit is already delivered by the skip rule in [§4](#4-the-proposed-shape), and it would make `yolo -- bash` behave differently per backend. |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A core package has no native darwin build | It is the same `yoloUnavailablePackages` mechanism `packages:` uses — but for a CORE package a skip must be **fatal**, not warned: a floor with a hole in it is not a floor. ⚠ At least two of the image's 36 are Linux-only by nature (`iptables`, `procps`), so "the whole image core" is not even an option here without a native eval per entry — see [`OQ-P1`](#OQ-P1). |
| First launch builds a large closure natively | One-off per machine; nix caches. Cachix already applies (`--accept-flake-config`). Measure before assuming it is a problem. |
| The stage's state lands in the shared home | Settled: the container's partition ([§4](#4-the-proposed-shape), [`OQ-P3`](#decision-ledger)). The residual risk is the **inverted default** — `MISE_DATA_DIR` unset once `~/.local` is a sidecar symlink — and it is closed by setting the variable explicitly. |
| The mise *config* collision ships today, without any stage | Real and already live ([§1](#1-the-two-missing-halves)); fixed by the same `config` sidecar symlink, which is why half two waits for the split rather than the other way round. |
| GNU-vs-BSD userland surprise | [`OQ-P2`](#OQ-P2). |
| The stage runs vendor postinstall scripts | Confined under the same profile as the agent (P4). |

## 8. Sequencing

Ship the unwarned agent-launcher case first — it is independent of every question
below and it is the one failure that lands on a user's first real command. Then
half one, gated on [`OQ-P1`](#OQ-P1) and [`OQ-P2`](#OQ-P2). Then half two, gated on
the home split's one open ruling ([`OQ-HT2`](macos-user-home-tiers.md#OQ-HT2)) — its
own [`OQ-P3`](#decision-ledger) is settled. Half two is worth nothing before half one, so there
is no partial-credit ordering to be clever about.

**Does the stated dependency hold?** Checked 2026-09-11: **yes, narrowed.** Half two
writes to three per-workspace surfaces (`config`, `npm-global`, `local`) that exist on
this backend only once the sidecar symlinks of the home split are laid; without them
the stage writes per-workspace content into the shared home and reproduces the race
the split exists to end. The machine-wide half (mise data) does not depend on the
split at all — it depends on setting `MISE_DATA_DIR`, which the split makes
*necessary* rather than optional.

## Open Questions

1. 💬 **OQ-P1: How much floor?** The minimum that makes the stage run is `mise` and
   `nodejs`. The maximum is the image's core — **36 packages, not the ~19 the first
   draft said** — minus whatever has no darwin build. Everything between is available.
   ⚠ *Sharpened 2026-09-11:* two facts move the stakes. The gap is roughly twice as
   wide as first stated, so "the whole core" is a real native build, not a rounding
   error; and the GNU userland question ([`OQ-P2`](#OQ-P2)) decides nine of the 36
   on its own (`coreutils-full`, `findutils`, `gnused`, `gnugrep`, `gawk`, `gnupatch`,
   `diffutils`, `gnutar`, `which`), so the two rulings are not independent — rule P2
   first.

   _Leaning:_ Start at the minimum plus `git` — `git` because a jail without it is
   not a development environment and the Mac's `/usr/bin/git` is an Xcode shim the
   user may not have. Add on demand. A large floor here costs a native build on a
   machine that is not building an image, which is the thing this backend exists to
   avoid. And the minimum is already reachable by hand today — `packages:` is realized
   natively and the `mise_tools` warning tells users to put the tools there — so the
   floor's job is to make that implicit, not to invent it.

   <!-- vantage: oq id=OQ-P1 leaning="Start at the minimum (mise and nodejs) plus git — git because a jail without it is not a development environment and the Mac's /usr/bin/git is an Xcode shim the user may not have. Add on demand. The maximum is 36 packages, not ~19, and nine of them are OQ-P2's GNU set, so rule P2 first." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-P2: GNU userland or the Mac's own?** The image's core deliberately bakes
   `coreutils-full`, `gnused`, `gnugrep`, `gawk` and the rest of the GNU set so a jail
   behaves the same everywhere. On macos-user those would sit ahead of the BSD tools
   the human's own shell uses. This decides whether a script that works in the jail
   works in the human's terminal on the same machine — and, per
   [`OQ-P1`](#OQ-P1), it decides a quarter of the floor.

   _Leaning:_ **No GNU userland.** The consistency argument is real but this
   backend's whole proposition is "your Mac, confined" — an agent whose `sed -i`
   behaves differently from the human's is a surprise in the direction that costs
   more. Revisit if a pack turns out to depend on GNU behavior. ⚠ *One fact for the
   leaning, found 2026-09-11:* the one place the tree has already chosen, it chose BSD —
   `DarwinEnvFrom` sets `GNUStat = false` and points the shim realbins at `/usr/bin`
   (`internal/entrypoint/darwin.go:62-63`), so yolo's own generated shims speak BSD on
   this backend. A GNU userland ahead on PATH would make the agent's shell disagree
   with the shims yolo wrote for it.

   <!-- vantage: oq id=OQ-P2 leaning="No GNU userland. This backend's proposition is 'your Mac, confined' — an agent whose sed -i behaves differently from the human's is a surprise in the direction that costs more; and yolo's own darwin shims already speak BSD (GNUStat=false). Revisit if a pack turns out to depend on GNU behavior." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-P1 | **Still open** — how much floor. Rule [`OQ-P2`](#OQ-P2) first; it decides nine of the 36. | — | [Open Questions](#open-questions) |
| OQ-P2 | **Still open** — GNU userland or the Mac's own. | — | [Open Questions](#open-questions) |
| OQ-P3 | The container's own partition: mise **data** machine-wide with `MISE_DATA_DIR` set **explicitly**, because the unset default would land inside the per-workspace `~/.local` symlink; mise config, the npm prefix and `~/.local` per-workspace through the home split's sidecar symlinks. A per-workspace `MISE_DATA_DIR` is rejected twice over. | 2026-09-11 | [§4](#4-the-proposed-shape), [§5](#5-what-this-does-not-propose) |
| OQ-P4 | Unconditional, before the agent, matching the container. [§4](#4-the-proposed-shape)'s own skip rule already delivers on-demand's only benefit, and on-demand would be a second dialect of "when are my tools there". | 2026-09-11 | [§4](#4-the-proposed-shape), [§6 row E](#6-alternatives) |
