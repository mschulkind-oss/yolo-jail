---
title: "Internal rollout on macos-user: the day-one set"
date: 2026-09-16
status: in-review
tags: [macos-user, rollout, adoption, packs, claude, copilot, bedrock, scope]
summary: "The minimum set of user-visible promises that must hold before yolo is announced internally to Mac-using engineers on the macos-user backend — fifteen promises, each with its measured status, the file that decides it, and the cheapest fix. Two are hard blockers today (git refuses to operate in the workspace at all; a Claude subscription login stops sticking on day two), five are cheap text fixes on surfaces a newcomer reads first, and seven questions need the maintainer. Everything the 705-cell backend grid tracks that is NOT in this set is deferred here by name."
vantage:
  status-chip: true
---

# Internal rollout on macos-user — the day-one set

**Status:** DESIGN, 2026-09-16 — the set in [§3](#3-the-day-one-set) is a proposal built on five
rulings the maintainer has already given ([§2](#2-what-is-already-ruled)); the seven questions in
[§8](#8-open-questions) are what it still owes. Every status cell was measured or code-read on
2026-09-16 by a 66-agent survey plus an adversarial cross-check of each gating claim; cells that
rest on a Mac nobody has run are labelled as such rather than assumed.

## 1. What this is

An **internal rollout** here means announcing yolo inside the maintainer's own company to several
dozen engineers, nearly all on Apple Silicon Macs, and handing them a shared pack that carries the
team's agent setup. The backend is [`macos-user`](../reference/macos-user-nix-and-features.md) —
native macOS plus Seatbelt, no VM, no Linux image. Apple Container is a fallback nobody is being
asked to use, and Linux is not the audience.

> [!IMPORTANT]
> **day-one set** *(coined here; no prior term in this corpus means this)* — the set of
> user-visible promises that must hold before the announcement goes out. A promise is **in** the
> set when a colleague who meets its absence stops using yolo, or tells a teammate it is broken.
> A promise is **out** when its absence costs an inconvenience they can route around. That test is
> the whole editorial rule of [§3](#3-the-day-one-set), and everything it excludes is listed by
> name in [§6](#6-what-is-deliberately-out) rather than left unsaid.

**Three things this is not.**

- **Not the backend grid.** [`setup-support-gaps.md`](setup-support-gaps.md) crosses four setups
  with every closed vocabulary yolo has — 705 cells, 35 ranked gaps — and is the authority for
  *what is missing per setup*. This document is the **filter** over it: which of those gaps a
  colleague actually meets in their first week, and which they never will. Gap ids (`G7`, `G17`…)
  below are that document's.
- **Not the acceptance bar.** That term is taken and it means something else: the nix-layer bar
  in [`macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md#the-acceptance-bar) — *"a
  macOS backend that cannot carry the nix layer is not a yolo backend"* — which this backend
  **meets**, measured on hardware. Do not reuse it for adoption criteria.
- **Not a roadmap.** [`roadmap.md`](roadmap.md) routes every open decision in the project. This
  document routes one launch, and its rows leave when the launch happens.

## 2. What is already ruled

The maintainer settled these on 2026-09-16, and the set in [§3](#3-the-day-one-set) is derived
from them rather than from a general theory of adoption. They are recorded here because every one
of them cuts scope.

| | Ruling | What it decides |
|---|---|---|
| **R1** | **The sell is the ENVIRONMENT** — declared tools plus a shared pack. Isolation is table stakes, not the pitch | Seatbelt's strength needs to be *true and stated*, not *strong*. `packages:`, packs, skills and agent config carry the announcement, so their promises are the load-bearing ones |
| **R2** | **Day one is their REAL work repo**, not a scratch project | Puts private-repo git auth, internal registries and the workspace-location refusal INSIDE the set. This is the single biggest scope-widening ruling |
| **R3** | **`brew install` is the whole install of yolo.** Nix as a prerequisite is fine, a one-time `yolo macos-setup` is fine, and a password prompt per launch is fine | Removes the installer work ([`native-installer-migration.md`](native-installer-migration.md)) from the set. Keeps *nix must not have to compile anything* in it |
| **R4** | **Claude Code and GitHub Copilot CLI both work.** Claude defaults to the company's Teams subscription (ordinary OAuth); `-p bedrock` switches to Bedrock and dropping it switches back | Two agents, and one provider switch, in the set. Every other shipped agent pack is out |
| **R5** | **A first launch downloads; it does not build.** The maintainer can pre-cache company-private content | Makes cache coverage a promise ([`P2`](#p2)). Measurement says the floor already satisfies it — see that row |

## 3. The day-one set

Fifteen promises, in the order a colleague meets them. **Status** is the state of the promise, not
of the code behind it: a promise whose mechanism is implemented but has never run on a Mac is
`works-unverified`, because an announcement is a claim about a colleague's machine.

### A. Get it

| | Promise | Status | What stands between |
|---|---|---|---|
| <a id="p1"></a>**P1** | One command installs yolo; nix is the only other prerequisite | **works-unverified** | The tap is live and current — `brew tap mschulkind-oss/tap && brew install mschulkind-oss/tap/yolo-jail` fetches `v0.9.0`, 39 commits behind `main`. The formula is a **source build** (`depends_on "go" => :build`) that stages the flake bundle into `pkgshare`, which is where [`internal/reporoot`](../../internal/reporoot/reporoot.go) looks. **Nobody has ever run it** — no CI job installs from the tap, and all four `v0.9.0` release assets have zero downloads. Cost to close: one Mac, ten minutes |
| <a id="p2"></a>**P2** | The first launch downloads; it never compiles | **works** for the tool floor | MEASURED 2026-09-16: `nix build --dry-run .#packages.aarch64-darwin.yoloNoncontainerProfile` → **4 derivations built, 253 paths fetched, 540.6 MiB download / 2.2 GiB unpacked**, and the four builds are all symlink farms (`builder.pl`, the `nodejs_24` `lndir` farm, `unixtools.procps`, the `buildEnv` itself) at ~1 s total. 100% substitutable **from `cache.nixos.org` alone**, without yolo's own cachix. What is NOT measured is the org's own `packages:` entries on `aarch64-darwin`; an unfree or unsupported entry is warn-and-skipped rather than built ([`flake.nix`](../../flake.nix) `noncontainerSkipped`), so the residual risk is a free package Hydra has not built at the locked rev |
| <a id="p3"></a>**P3** | `yolo check` answers "is this Mac ready?" before the first launch | **partial** | MEASURED against `check.Check` with three fixtures: on a Mac whose user config already says `runtime: "macos-user"`, the readiness section **does** print (`rc=0` with nix present, including the `Run \`yolo macos-setup\`` hint). The earlier reading that the accumulated-fail gate hides it was wrong. Two real gaps remain: it probes **neither** condition that actually refuses a launch — the workspace's location and its `_yolojail` group ACL — and [`internal/cli/check/sections_macos.go`](../../internal/cli/check/sections_macos.go) is the only place in the whole product that calls this backend `experimental` and `NOT verified end-to-end`, in the first command an onboarding colleague runs |

### B. Configure it

| | Promise | Status | What stands between |
|---|---|---|---|
| <a id="p4"></a>**P4** | The company's settings arrive as a file, not a wiki page | **works** | Three channels, all real and all documented: `include_if_found` in the user config ([`config_ref`](../../internal/cli/config_ref.txt), `include_if_found`), the auto-merged `yolo-jail.local.jsonc`, and `--user-layer` / `YOLO_USER_LAYER` ([`internal/config/userlayer.go`](../../internal/config/userlayer.go)). All three carry `packs`. **This is the rollout's delivery mechanism and nothing frames it that way** — and it is not optional: `packs`, `providers`, `profiles` and `use_profiles` are all **user-scope only**, so a repo-committed `yolo-jail.jsonc` cannot carry the pack list, the Teams default or the Bedrock profile. The whole rollout is one file plus one `include_if_found` line |
| <a id="p5"></a>**P5** | A colleague can discover the backend without being told | **missing** | The [project README](../../README.md) contains **zero** occurrences of `macos-user`, `Seatbelt` or `macos-setup` (verified). The backend is never auto-detected — [`internal/cli/run/preflight.go`](../../internal/cli/run/preflight.go) tries `container` then `podman` on macOS — and the `No container runtime found` refusal does not name the backend that needs none. `yolo init-user-config` writes a template whose runtime comment offers only podman and Apple Container and whose only `packs` example is the `journal` loophole ([`internal/cli/userconfig.jsonc`](../../internal/cli/userconfig.jsonc) lines 11 and 23, verified). **[`P4`](#p4)'s file makes this survivable for the rollout** — the shipped config sets `runtime` — so this is in the set as *text*, not as auto-detection |

### C. Work in it

| | Promise | Status | What stands between |
|---|---|---|---|
| <a id="p6"></a>**P6** | Your project can live where you keep your projects — or you are told where it must live, once, before you install | **blocked by ruling, plus a defect** | Anything under `/Users/<you>` is a hard launch refusal with no config key and no env hatch (`HomeContaining` in [`internal/macosuser/macosuser.go`](../../internal/macosuser/macosuser.go), enforced as a run-plan invariant; the symlink bypass was deliberately closed). The ruling is [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md). **Two things soften it**, both measured: any non-home root passes — `/opt/hs/repos` or a `/Volumes` mount, not just `/Users/Shared/yolo` — and `yolo macos-fix-permissions <root>` will ACL an arbitrary one. **One thing makes it worse**: the workspace-ACL probe fires *first*, and its remedy prescribes `yolo macos-fix-permissions <ws>`, which then refuses that very path — so a colleague following the launch's own instructions loops, and the one message naming the real fix is unreachable. ~8 lines to reorder, plus an ordering test. The policy is [`OQ-IR1`](#OQ-IR1) |
| <a id="p7"></a>**P7** | git works in your checkout | **missing — the first blocker** | Nothing sets `safe.directory`: [`internal/entrypoint/identity.go`](../../internal/entrypoint/identity.go)'s `configureGit` sets `user.name`, `user.email` and `core.excludesFile` and no fourth key. The uid split is **structural and pinned by a test** — the workspace is granted by `chmod +a` ACEs, never `chown`, so ownership never converges. MEASURED (git 2.55): a repo owned by one uid and read by another inside a 2770 setgid dir whose group both belong to → `fatal: detected dubious ownership`, exit 128, **in both directions**, and full group access does not satisfy the check. So the agent cannot run `git status` at all. Two routes: three `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0` vars in the rollout config or the shared pack's `env` kind (measured working), or one line beside the other three keys. **No test or runbook item anywhere runs a git repository operation as the sandbox user** |
| <a id="p8"></a>**P8** | `git fetch` and `git push` to the company remote work after one documented step | **partial** | There is no ssh-agent forwarding on any backend, and that is a considered posture, not an oversight ([`agent-config-distribution.md`](../research/agent-config-distribution.md) ranks it 4th of 6 and argues against it: an agent socket is an unconditional signing oracle). Nothing seeds `known_hosts` either, so MEASURED the first failure is `Host key verification failed.` — not an auth error, and the message points nowhere near the fix. Three routes, all real: one in-jail `ssh-keygen` persists **machine-wide** on this backend (`~/.ssh` is not per-workspace here) so one pubkey upload covers every repo; `host_files` with `mode: "once"` for an existing key (the default `readonly` mode re-chmods 0444 every boot, which OpenSSH refuses); and `GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new` via `env_sources`, MEASURED to make the host-key error disappear and leave the real one. **The one recipe the user guide gives is the podman path and does not exist on this backend** |
| <a id="p9"></a>**P9** | Your internal npm/pip registry works | **partial**; the corporate root CA is **missing** | `env_sources` carries any variable into this backend's per-session env file, and `host_files` with a file source delivers `~/.npmrc` by copy (measured on hardware 2026-09-13 for one entry). Caveat: a home-root destination lands in the single `_yolojail` account home, so it is machine-global across every workspace (`G26`). **A TLS-inspecting corporate proxy has no mechanism at all** — no config key adds a root CA, and the `NODE_EXTRA_CA_CERTS` back door is already occupied by the broker CA. That is [`OQ-IR6`](#OQ-IR6): if the company runs one, this promise is not partial, it is missing |

### D. Run the agents

| | Promise | Status | What stands between |
|---|---|---|---|
| <a id="p10"></a>**P10** | `yolo -- claude` gives you the company Teams subscription, and the login sticks | **missing — the second blocker** | Day one is fine. From the first token refresh (~8 h access-token TTL) it is a **re-login on every launch, permanently, with no user-reachable remedy**. The chain is measured end to end: no broker runs on this backend, Claude's credential write is a temp-file + `rename` that **replaces** yolo's `shared_credentials` symlink with a regular file (read out of the shipped 2.1.271 binary; the in-place arm opens `O_NOFOLLOW` and refuses a symlink too), and [`internal/entrypoint/claude.go`](../../internal/entrypoint/claude.go) discards that regular file at the next boot whenever the shared file is non-empty — which by then holds the already-consumed token. The shared file is also frozen at the day-1 record forever, so cross-workspace sharing works for exactly one day. Apple Container has the identical loop; only macOS podman keeps the broker. **The cheapest escape is one line** and it is measured in the binary: `CLAUDE_SECURESTORAGE_CONFIG_DIR` overrides the credential-store *directory* only (not `CLAUDE_CONFIG_DIR`), so pointing it at the machine-scope shared dir makes Claude read **and write** the real file — the discard branch becomes unreachable rather than merely unreached. Scope it to this backend: on containers the broker also rename-writes that file. Ruling needed: [`OQ-IR3`](#OQ-IR3) |
| <a id="p11"></a>**P11** | `yolo -p bedrock -- claude` switches to Bedrock; dropping it returns to Teams | **works**, with two named residues | MEASURED by rendering the real pack three times into one home — default → `-p bedrock` → default gives `env={}` → `{"CLAUDE_CODE_USE_BEDROCK":"1"}` → `env={}`. The overlay gate reads `YOLO_USE_PROFILES`, which **is** relayed to this backend's bootstrap, so it holds here. Region and model ids are supposed to come from the user's own `providers.bedrock` config block, not from the shipped pack — that is the design and a shipped test says so ([`internal/cli/run/agentprofileenv_test.go`](../../internal/cli/run/agentprofileenv_test.go)); measured, `-p bedrock` plus that block delivers `AWS_REGION` and all four model vars. **Residue 1:** `~/.claude/settings.json` is a composed layer with `readsHost: true`, so a colleague whose own file already sets `env.CLAUDE_CODE_USE_BEDROCK` gets Bedrock on *every* launch and no `-p` turns it off — and the whole `env` block crosses, `ANTHROPIC_MODEL` and `ANTHROPIC_BASE_URL` included. [`OQ-IR2`](#OQ-IR2). **Residue 2:** [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — `-p bedrock -- copilot` sets *claude's* env var while claude's own settings stay clean, because the profile env gate matches a bin any selected pack installs |
| <a id="p12"></a>**P12** | `yolo -- copilot` works, and you log in once per machine | **partial** | Login works, but only at a TTY and only per repo. MEASURED at the version a colleague installs (`@github/copilot-darwin-arm64@1.0.85`, 88.5 MB compressed / 143 MB extracted): the device flow stores the token in the **macOS keychain first** via the Rust keyring crate, falls back to a plaintext config file only on an interactive `y/N`, and **saves nothing at all** non-interactively. `.copilot` is declared `scope: workspace` and nothing declares a machine-scope sibling, so every work repo needs its own login (`G16`, and the user guide already ships that row). The fix is ~10 declarative lines mirroring the claude pack's `shared_credentials` pair — **but the token is probably not in `config.json` at 1.0.85** (the `copilot_tokens`/`logged_in_users` keys the repo pins have zero occurrences there), and a hook aimed at the wrong file logs success while sharing nothing. So: one measurement, then the lines. Interim with no release at all: a fine-grained PAT in `COPILOT_GITHUB_TOKEN` via `env_sources`. Ruling: [`OQ-IR4`](#OQ-IR4) |
| <a id="p13"></a>**P13** | Selecting an agent does not gate your launch on an unrelated service | **partial — noise, not a stop** | `packs: ["claude"]` silently selects two more packs: MEASURED, `ResolveNeeds` reports `+ openai-auth (needed by claude)` and `+ wire-bridge (needed by claude)`, both unconditional. On this backend `openai-auth` is the **one** loophole that starts, and the launch **refuses** if it does not come up — so a Claude-subscription colleague with no OpenAI account has their Claude launch gated on an OpenAI daemon. Cross-checked: it does not actually stop them, because the broker has no credential precondition at startup and serves regardless; what they get is a spurious selection line and a red `yolo check` row. `wire-bridge` never starts here at all and, unlike a loophole, gets **no inert line** — free for a Teams user, fatal for a `cerebras`/`kilo` profile (`G3`). Two independent fixes: a 4-line `supersedes` block in the rollout's own pack (measured to skip the whole path, and it fixes the launch half only), or ~15 lines gating the block on a profile that actually resolves to `openai-codex` |

### E. Share it, and trust it

| | Promise | Status | What stands between |
|---|---|---|---|
| <a id="p14"></a>**P14** | One line and `yolo pack install`, and the company pack's skills, house rules and agent config are in your agent | **works-unverified** | Pack addressing and `yolo pack install` work (a `git+ssh://…?ref=v1` address with a **mandatory** `?ref=`); the fetch is host-side, so a private company repo uses the colleague's own git credentials with no jail boundary crossed, and a missing credential fails fast rather than hanging. Staging runs **above** the macos-user early return, and skills, briefing prose and config surfaces are delivered on this backend. **Three gaps.** The pack `files` kind is never delivered here (`G7`, ~15 lines) — a pack shipping a script or helper file loses it silently, which is exactly the shape of the shipped [`claude-fzf` example](../examples/claude-fzf-pack/pack.json). `yolo pack footprint <git address>` exits 1, so the pre-selection review the trust model points at cannot read the artifact being shared. And skills and briefings arrive as writable **copies** here, so the agent can edit its own instructions between launches (`G14`) |
| <a id="p15"></a>**P15** | When it breaks there is a file to send, and what we told you about the sandbox has been measured | **missing** | [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) hardcodes `Out: os.Stdout`, so **nothing this backend prints reaches `<workspace>/.yolo/launch.log`** — the one backend where the launch stream [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) declares non-suppressible exists only in scrollback. There is no `boot.log` either, so a scrolled-away provisioning failure is undiagnosable ([§7 of the open-threads handoff](handoff-macos-user-open-threads.md#7-nothing-the-macos-user-backend-prints-reaches-launchlog); `G20`). Separately: **the measured launch is not today's launch.** All ten runbook items passed on hardware on 2026-09-12, and three changes rewrote the critical path on or after 2026-09-13 — the credentials-off-argv session env file, the `/ctx`-by-copy tree, and the LSP wiring. Two independent paths now **refuse a launch** on `chmod +a` ACL semantics that have never executed on a Mac |

## 4. Where we stand

Counted from [§3](#3-the-day-one-set) rather than carried forward.

| Status | Promises | |
|---|---:|---|
| `works` | 3 | [`P2`](#p2) (floor only) · [`P4`](#p4) · [`P11`](#p11) |
| `works-unverified` | 2 | [`P1`](#p1) · [`P14`](#p14) |
| `partial` | 5 | [`P3`](#p3) · [`P8`](#p8) · [`P9`](#p9) · [`P12`](#p12) · [`P13`](#p13) |
| `missing` | 4 | [`P5`](#p5) · [`P7`](#p7) · [`P10`](#p10) · [`P15`](#p15) |
| `blocked by ruling` | 1 | [`P6`](#p6) |

**Two are hard blockers** — [`P7`](#p7) (git does not work in the workspace) and [`P10`](#p10) (the
Claude login stops sticking on day two). Both have a **zero-code route** that ships in
[`P4`](#p4)'s config file, which is why the rollout is closer than the count suggests: three env
variables and one env variable respectively.

**The good news the corpus does not reflect.** The `macos-user` CI job has been green on real
Apple Silicon hardware **eleven consecutive times** since 2026-09-13 (`executed=6 skipped=0` every
run), `lsp_servers` on this backend is measured green while three docs still say it has never run,
and the first-launch cost several docs call unmeasured is **41 s with a cold nix store**. The
backend is in much better shape than its own documentation says; the gap is concentrated in the
surfaces a *newcomer* reads and in the two credential paths above.

## 5. The order I would build it

Ranked by "a colleague stops", not by size. Steps 1–4 are the announcement's critical path.

1. **Write the rollout config file** ([`P4`](#p4)) — one `jsonc` a colleague drops in
   `~/.config/yolo-jail/` plus one `include_if_found` line. It carries `runtime: "macos-user"`,
   `packs`, `providers.bedrock`, and the four env variables that close [`P7`](#p7) and
   [`P10`](#p10) with no release. **This is the highest-leverage artifact in the whole rollout**
   and nothing in the tree needs to change for it. Verify: `yolo config dump` shows the merged
   result; `yolo -- bash` then `git status` in the workspace.
2. **Rule [`OQ-IR1`](#OQ-IR1) (where repos live) and fix the remedy loop** ([`P6`](#p6)) — ~8 lines
   moving the neutral-ground check above the ACL probe, plus an ordering test. Without the ruling
   the onboarding doc cannot be written at all.
3. **Land the two code fixes behind the config workarounds** — `safe.directory` beside the other
   three keys in `configureGit` ([`P7`](#p7)), and the credential-store decision from
   [`OQ-IR3`](#OQ-IR3) ([`P10`](#p10)). Both want the test the tree does not have: a git repository
   operation as the sandbox uid, and a three-boot render of the claude surfaces.
4. **Give this backend the pipeline's writers** ([`P15`](#p15)) — already
   [ready on the roadmap](roadmap.md), and it is what makes every later bug report a file.
5. **Fix the five newcomer surfaces** ([`P3`](#p3), [`P5`](#p5)) — the [project README](../../README.md) gains the backend and
   a three-command quickstart; `userconfig.jsonc` names `macos-user` and a real `packs` line;
   `sections_macos.go` drops `experimental` / `NOT verified end-to-end`; the no-runtime refusal
   names the backend that needs none; `yolo check` gains a workspace-location and ACL probe. Five
   small edits, all text or one predicate.
6. **Run the brew path once on a Mac** ([`P1`](#p1)) — install from the tap, `yolo macos-setup`,
   one launch. Ten minutes, and it is the only thing between `works-unverified` and `works`.
7. **Deliver the pack `files` kind on this backend** ([`P14`](#p14), `G7`, ~15 lines) — required
   only if the shared pack ships a script; check the pack first.
8. **Copilot's machine-scope credential** ([`P12`](#p12)) — one measurement of where 1.0.85 puts
   the token, then ~10 declarative lines.
9. **Re-measure the critical path on hardware** ([`P15`](#p15)) — the two `chmod +a` paths that can
   refuse a launch, plus a first launch end to end on today's code.

## 6. What is deliberately out

Every row is a real gap in [`setup-support-gaps.md`](setup-support-gaps.md). Each fails the
day-one test in [§1](#1-what-this-is): a colleague meets it and routes around it.

| Deferred | Why it is out |
|---|---|
| `resources` (cpu/memory/pids) not enforced (`G12`) | An agent can take the machine, and that is true of every agent they run outside yolo today. Documented absence, not a stop |
| Both port keys unread (`G13`) | Nothing in the announced workflow forwards a port. Wanted the day someone runs a dev server against a host service |
| Config `mounts`, pack `mount` grants, directory-shaped `host_files` | All three name an arbitrary host tree that a copy does not scale to. The file-shaped half is delivered, which covers the credential cases in [`P9`](#p9) |
| `mcp_presets` wrappers not delivered | `mcp_servers` is the supported spelling here and the launch says so. The rollout config uses it |
| `per_side_paths` / the default `node_modules` shadow | Both sides are darwin on this backend, so there is nothing to fork |
| `cache_relocations` | One large cold cache on other storage is a power-user need, and the documented symlink workaround is refused here |
| `confinement: "guest"` | Refused on all four setups, correctly. Nobody is being offered it |
| No attach, no `ps`, no `stop` | Every invocation is a fresh sandbox. `yolo ps` printing a red error on a healthy Mac is cosmetic and belongs with [`P15`](#p15)'s sweep |
| Install-capture materialize (`G7`'s sibling) | Every new workspace re-downloads the agent CLI. Costs bytes and minutes, stops nobody |
| No GNU userland | Ruled deliberately ([`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger)): *"your Mac, confined"*. It is the likeliest source of *"it worked on my colleague's Linux box"*, so it belongs in the onboarding doc's own text — but it is a documentation item, not a build item |
| Full host process visibility; advisory `macos_log` | Posture surprises with no boundary (`G30`). They belong in the honest description of Seatbelt that [R1](#2-what-is-already-ruled) requires, not in the build |
| Apple Container parity, podman/macOS parity | Not the announced backend |
| A private nix substituter as a yolo feature | [R5](#2-what-is-already-ruled) is already satisfied without one ([`P2`](#p2)). If the company later ships its own `packages:`, the route is nix-level — see [`OQ-IR5`](#OQ-IR5) |
| Ctrl-C leaving a 0600 credential file (`G5`) | Real, and it lands with [`P15`](#p15)'s terminate-chain work rather than as its own row |

## 7. The traps in this document's own material

Written down because each one cost an agent a wrong conclusion during the 2026-09-16 survey, and
the next reader will reach for the same thing.

- **A "NOT MEASURED" callout in a doc is not evidence.** Far more of this backend is measured than
  the corpus says — see [§4](#4-where-we-stand)'s good-news paragraph. Check the CI run before
  quoting a doc's warning.
- **The inverse also holds: a ✅ rots.** [`macos-support-matrix.md`](../research/macos-support-matrix.md)
  says so about itself, and its own `mounts` cells were stale within hours of a warning shipping on
  2026-09-16.
- **"Impossible" here is usually a mechanism, not an outcome.** 32 of 33 hard claims in the backend
  grid fell to a refuter. Three of the four `missing` rows above have a zero-code route to the same
  outcome, and the survey only found them because it was told to look.
- **A test that pins the callee while the call site is unpinned is not a test.** The two fixes in
  step 3 of [§5](#5-the-order-i-would-build-it) both need a test that fails when the call site is
  deleted — a git operation as the sandbox uid, and a render *without* the profile selected.
- **`YOLO_PROFILES` is not relayed to this backend's bootstrap** (only `YOLO_PROVIDERS` and
  `YOLO_USE_PROFILES` are, and the plan invariant pins the same incomplete pair). Any fix written
  in terms of `ctx.selected_provider` is correct on a container and silently wrong here; the
  `ctx.use_profiles.<agent>` spelling is the one that works on both. This is `G19`, and it is a
  trap for [`OQ-IR2`](#OQ-IR2)'s fix specifically.

## 8. Open questions

1. <a id="OQ-IR1"></a>💬 **[`OQ-IR1`](#OQ-IR1) — Where do the company's repos live on a macos-user Mac?**

   <!-- vantage: oq id=OQ-IR1 leaning="A documented company non-home root such as /opt/hs/repos — measured to pass HomeContaining, needs no code, and does not put everyone under /Users/Shared." -->

   Three options. **(a)** Mandate `/Users/Shared/yolo/<name>` — the shipped happy path, but every
   engineer re-clones and every IDE workspace, alias and absolute path moves. **(b)** Standardise a
   company non-home root such as `/opt/hs/repos` — MEASURED to pass `HomeContaining`, and
   `yolo macos-fix-permissions <root>` will ACL it, so this needs no code, only a documented root
   and one command per repo. **(c)** Relax `HomeContaining` or restore a `macos_shared_root`-shaped
   key — which reopens the `/Users` read boundary that
   [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) bought deliberately.
   Nothing else in the onboarding doc can be written until this is ruled.

   _Leaning:_ (b) — a documented non-home root, no code.

2. <a id="OQ-IR2"></a>💬 **[`OQ-IR2`](#OQ-IR2) — Does yolo OWN `env.CLAUDE_CODE_USE_BEDROCK` in `claude/settings`?**

   <!-- vantage: oq id=OQ-IR2 leaning="Tombstone the whole Bedrock env bundle when the profile is off, and say in the commit that it overrides the colleague's own settings.json for those keys." -->

   Tombstoning it when the `bedrock` profile is off makes *"Teams by default"* a guarantee, but it
   inverts the host layer's normal precedence and overrides a value the colleague wrote for
   themselves — the class [`OQ-CS2`](../reference/providers.md#why-its-this-way) ruled the other
   way. And it is not one key: the whole host `env` block crosses, so a one-key tombstone leaves
   `ANTHROPIC_MODEL` behind (a 404 on an unknown model rather than an auth error) and
   `ANTHROPIC_BASE_URL`, which would route a Teams OAuth bearer out of the jail. Only the
   derive layer can branch — a `config-overlay` gates positively on a profile and a `managed` null
   assigns a JSON null rather than deleting. Note the trap in [§7](#7-the-traps-in-this-documents-own-material):
   write it as `ctx.use_profiles.claude`, never `ctx.selected_provider`.

   _Leaning:_ tombstone the bundle, and say so in the commit.

3. <a id="OQ-IR3"></a>💬 **[`OQ-IR3`](#OQ-IR3) — What is the rollout's default Claude auth?**

   <!-- vantage: oq id=OQ-IR3 leaning="CLAUDE_SECURESTORAGE_CONFIG_DIR, scoped to macos-user, shipped in the rollout config — one line, keeps ordinary /login, and survives the hardened credential store." -->

   Three candidates. **(a)** `CLAUDE_SECURESTORAGE_CONFIG_DIR` pointed at the machine-scope shared
   dir — one line, ordinary `/login` keeps working, entitlement metadata is untouched, Claude's own
   refresh flock becomes machine-wide (which also blunts `G17`'s race half), and it must be scoped
   to this backend because the broker rename-writes that file on containers. **(b)**
   `claude setup-token` + `CLAUDE_CODE_OAUTH_TOKEN` per engineer — the token is `inferenceOnly`
   with a one-year expiry, Remote Control refuses it, and it needs
   `CLAUDE_CODE_SUBSCRIPTION_TYPE`/`CLAUDE_CODE_RATE_LIMIT_TIER` alongside it or Claude ≥2.1.200
   reads the credential as *not logged in*; `setup-token` on a Teams seat is also unverified.
   **(c)** Invert the discard in [`claude.go`](../../internal/entrypoint/claude.go) — correct, but
   it changes `agy` on **every** backend and so partially reverses the ruling stated in that file's
   own header; a ruling, not a patch. ⚠ A dated reason to prefer an env route either way: Claude
   2.1.271 already ships a hardened credential store that opens the file `O_NOFOLLOW` and has a
   first-class *refused-symlink* state, inert only because a gate is off. The day it flips, the
   `shared_credentials` symlink breaks on every backend at once.

   _Leaning:_ (a), scoped to `macos-user`, shipped in the rollout config.

4. <a id="OQ-IR4"></a>💬 **[`OQ-IR4`](#OQ-IR4) — How does a Copilot login persist across a colleague's repos?**

   <!-- vantage: oq id=OQ-IR4 leaning="Measure where 1.0.85 actually writes the token, then ship the machine-scope state + shared_credentials pair; a PAT via env_sources is the interim." -->

   The keychain-first flow means the fallback file is the only thing a hook can share, and its
   location at 1.0.85 is unestablished — a hook aimed at the wrong file **logs success while
   sharing nothing**, which is the worst available failure shape. Options: measure first, then ship
   the machine-scope `state` + `shared_credentials` pair (~10 declarative lines, plus a widening of
   the test that pins the machine tier to exactly two entries); or ship
   `settings.json`'s `storeTokenPlaintext: true` so keytar is never the store, which writes a live
   long-lived GitHub token to a 0600 file; or accept a per-repo login and say so. The interim that
   needs no release is a fine-grained PAT in `COPILOT_GITHUB_TOKEN` via `env_sources`.

   _Leaning:_ measure, then the state+hook pair; PAT as the interim.

5. <a id="OQ-IR5"></a>💬 **[`OQ-IR5`](#OQ-IR5) — Does the rollout touch each Mac's `nix.conf`?**

   <!-- vantage: oq id=OQ-IR5 leaning="Ship neither. Measurement says the floor is 100% substitutable from cache.nixos.org, so this only becomes necessary if the company distributes its own packages." -->

   Measurement says no: the floor is fully substitutable from `cache.nixos.org` without yolo's own
   cachix ([`P2`](#p2)). It becomes necessary only if the company later ships its **own** nix
   packages through `packages:`. If it does, the choice is **(a)** add the private cache to
   `/etc/nix/nix.conf`'s `substituters` + `trusted-public-keys`, which works for non-trusted users
   and grants nothing extra, or **(b)** `trusted-users = root @admin`, which nix's own
   documentation calls essentially equivalent to giving that user root. (a) is the narrow correct
   answer. Note that yolo has **no** config key or pack kind for a substituter, so either way this
   is an MDM step rather than something the rollout config can carry.

   _Leaning:_ ship neither now; (a) if it becomes necessary.

6. <a id="OQ-IR6"></a>💬 **[`OQ-IR6`](#OQ-IR6) — Is a corporate TLS-inspecting proxy in scope for day one?**

   <!-- vantage: oq id=OQ-IR6 leaning="Find out before announcing. If the company runs one, P9 is missing rather than partial and needs a real root-CA key." -->

   There is no mechanism to add a root CA to a jail's trust bundle, and the one back door
   (`NODE_EXTRA_CA_CERTS`) is occupied by the broker CA. If the company terminates TLS on the
   corporate network, [`P9`](#p9) is **missing**, not partial, and every `npm install` and every
   agent API call fails on certificate validation. This is a question about the company's network,
   answerable in one command on a managed Mac, and it should be answered before the announcement
   rather than after.

   _Leaning:_ find out first; it changes a status cell.

7. <a id="OQ-IR7"></a>💬 **[`OQ-IR7`](#OQ-IR7) — What does MDM do to this, on both of its two edges?**

   <!-- vantage: oq id=OQ-IR7 leaning="Check both on one managed Mac before announcing: whether engineers hold local admin, and whether IT already ships a Claude managed-settings.json." -->

   Two independent facts, both cheap to establish and both able to invalidate the announcement.
   **Admin rights:** `yolo macos-setup` needs `dscl`/`dseditgroup` under sudo, and every launch
   needs `sudo --user=_yolojail` plus root writes under `/var/yolo-jail` — so a fleet of standard
   users cannot run this backend at all, and the only mitigation is a scoped, source-pinned
   `NOPASSWD` drop-in, already ranked *"worth doing, worth pinning"* in
   [`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md).
   **Managed Claude settings:** `/Library/Application Support/ClaudeCode/managed-settings.json` is
   readable under this backend's Seatbelt profile, and `permissions.disableBypassPermissionsMode`
   is a managed key — so if IT already ships that file, yolo's autonomy is silently overridden and
   nothing in the launch says so. One `ls` on one managed Mac answers it.

   _Leaning:_ check both before announcing.

## 9. Decision ledger

Rulings already taken, so nobody re-opens them. [§2](#2-what-is-already-ruled) is the same list with
what each one *decides*; this is why each one stays.

| Ruling | Why it stays |
|---|---|
| **R1 — the sell is the environment, isolation is table stakes** | It is what the backend actually is. Seatbelt is not VM-grade and the docs say so; leading with isolation would either overclaim or invite the comparison the container cell exists to answer. Leading with declared tools plus a shared pack is also the only pitch a plain sandbox wrapper cannot make |
| **R2 — day one is their real work repo** | A rollout that only survives on scratch projects is one people try once. It is the expensive ruling — it is what puts [`P6`](#p6)–[`P9`](#p9) in the set — and taking it now is what stops those four being discovered by a colleague instead |
| **R3 — brew plus nix plus one privileged setup is an acceptable prerequisite chain** | The tap is live and the floor needs no build, so the chain is already short. Building an installer before establishing that anyone wants the tool is the wrong order |
| **R4 — Claude and Copilot, with a Bedrock switch** | Two agents is what the audience uses. Every other shipped agent pack is a *works or refuses* question, not a rollout question |
| **R5 — a first launch downloads, it does not build** | A 30-minute first launch is indistinguishable from a broken install, and this is the one prerequisite cost the rollout cannot apologise its way past. Measurement says it is already met, which is why the ruling is cheap to keep |
