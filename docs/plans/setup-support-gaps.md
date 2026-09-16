---
title: "Backend gap tracker: the 705-cell audit, its 32 overturned hard claims, and the ranked backlog they produce"
date: 2026-09-16
status: current
tags: [gap-tracker, backend-parity, macos-user, apple-container, podman, silent-drops, refutation, unmeasured]
summary: "Four setups crossed with every closed vocabulary yolo has — config keys, pack contribution kinds, shipped loopholes — plus the user-facing capabilities that are not keys, audited cell by cell (705 cells, 14 agents), then every 'impossible' and 'ruled-wontfix' claim handed to an adversarial refuter. 33 hard claims were attacked and 32 fell; exactly one survived (NVIDIA passthrough on Apple Container). What is left is a backlog of 33 ranked gaps — most of them small, several of them silent losses of a shipped default — a silent-drop table naming the file that should print each missing notice, and 22 cells no reading can settle, 12 of them in podman/macOS, the column with zero measured cells out of 141."
---

# Backend gap tracker

**Audience:** the engineer who writes the design docs next. Every row here is meant to be openable — `file:line`
citations are deliberate and are the point of the document, not decoration. Where a line number is quoted from an
audit cell rather than re-checked here, it is quoted as the auditor wrote it; the ones this document leans on
hardest were re-verified against the tree on 2026-09-16 (`run.go:789` attach return above `checkConfigChanges`
at `:794` and `writeLaunchConfigArtifacts` at `:799`; `YOLO_REQUIRED_CAPABILITIES` emitted at
`internal/cli/run/assemble.go:964` with no reader anywhere in `internal/` or `cmd/`; `prune` accepted at
`internal/config/config.go:66` with no `validatePrune`; `internal/cli/check/helpers.go:103` returning false on a
nil `Stdin` that nothing in `internal/cli/commands.go` ever assigns; `internal/cli/run/captures.go:59`'s bare
`rt == "container"`; `internal/cli/run/proxy_other.go:21`'s `_ = onTerminate`).

## 1. What this is

The audit crossed **four setups** — `podman`/Linux, `podman`/macOS, `container` (Apple Container)/macOS, and
`macos-user` (Seatbelt, no VM)/macOS — with the **closed vocabularies** yolo actually ships:

- every **top-level config key** (`yolo config-ref`'s full surface, including the meta keys: `confinement`,
  `perf_logging`, `prune`, `programs`, `required_capabilities`, `loopholes.<name>` and its sub-keys, the
  inherited user scope);
- every **pack contribution kind** (`internal/packdecl`) — including the two that are nearly invisible from the
  Linux column, `mount` and `service`;
- every **shipped loophole** — `audio`, `host-processes`, `journal`, `cgroup-delegate`, `serial`,
  `openai-auth-broker`, `claude-oauth-broker`, `wire-bridge`;
- and the **named user-facing capabilities that are not keys**: who owns the files the agent writes, whether git
  identity exists, whether outbound internet works, whether a re-entry applies an edited config, whether an
  agent login persists across workspaces, what Ctrl-C does, what `yolo ps`/`stop`/`prune`/`check` report.

**705 cells. Fourteen agents.** Each cell was classified `works` / `works-differently` / `unbuilt-gap` /
`impossible` / `ruled-wontfix` / `unknown`, with the mechanism cited, the disposition the launch takes
(`Honored` / `HonoredBy` / `Warned` / `Dropped` / `Refused` / `NotApplicable`), when it takes effect
(`fresh-launch-only` / `any-entry`), and the symptom a user meets.

| Classification | Cells | | Disposition | Cells |
|---|---:|---|---|---:|
| `works` | 410 | | `Honored` | 414 |
| `unbuilt-gap` | 145 | | `Dropped` | 110 |
| `works-differently` | 82 | | `HonoredBy` | 82 |
| `impossible` | 26 | | `Warned` | 64 |
| `unknown` | 22 | | `NotApplicable` | 25 |
| `ruled-wontfix` | 20 | | `Refused` | 10 |

Then every `impossible` and `ruled-wontfix` cell — 46 of them, the ones that say *stop looking* — was handed to
an **adversarial refuter** whose brief was to defeat the claim by naming a mechanism, however worse, that reaches
the same user-visible outcome, with a size estimate.

**THE HEADLINE: 33 hard claims were tested and 32 were overturned.** Twenty became `unbuilt-gap`, three became
`works-differently` and one became `works` outright (`pack mount` on Apple Container — the fix is a version
gate the sweep missed, not a capability). **Exactly one survived: NVIDIA GPU passthrough on Apple Container.**

Read that as a statement about the corpus, not about the refuters: a wall of `impossible` cells was, in almost
every case, a *mechanism* that was impossible standing in for an *outcome* that was not. The audit's own vocabulary
is what made this legible — `impossible` was defined as "no mechanism, worse or otherwise, reaches the outcome",
and thirty-two cells failed that test on inspection.

**Cap to be honest about:** the refutation pass tested **at most seven hard claims per audit group**, so 13 of the
46 were never adversarially attacked at all and still stand only by default. They are not randomly distributed —
they cluster in two shapes on `macos-user`:

- **ELF/loader rows** (5): nix-ld as the FHS interpreter, the `/lib` + `/usr/lib` soname farm,
  `/etc/ld.so.cache`, `packages:` shared objects reaching `LD_LIBRARY_PATH`, and `/run/yolo/packages/bin`'s PATH
  position. These read as genuinely structural (Mach-O resolves by absolute `install_name`; SIP strips `DYLD_*`),
  but nobody argued the other side. The PATH-position cell did surface a real defect while being written:
  `macosuser.SandboxPath` puts `~/.local/bin` **third** where `BootPath` puts it **sixth**
  (`internal/macosuser/macosuser.go:604-621` vs `internal/entrypoint/boot.go`), so AGENTS.md's claim that the
  third copy "moves with the other two" is false of the ordering itself.
- **image-shaped and device rows** (8): `.#yoloImageExtras` on `macos-user`, `iptables`/DNAT on `macos-user`,
  `kvm` on `container` and on `macos-user`, `gpu: nvidia` on `macos-user`, the chrome-devtools/node/npx wrappers,
  the [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger) GNU-userland exclusion, and vendor-installer auto-capture on `macos-user`.

Given a 32-of-33 kill rate, the prior on the untested 13 should be low, and the ELF/loader five are the ones most
worth one adversarial pass before anyone writes "structural" in a design doc.

## 2. Ranked gap backlog

Ranked by **user impact**, not by size. Rows merge every cell that describes one underlying gap across setups.
Size is the *build* estimate the refuters and auditors gave, not the measurement cost. `Design doc?` is decisive:
"yes" means a ruling is missing, not that the change is large.

| # | Gap | Setups affected | User impact | Size | Design doc? |
|---|---|---|---|---|---|
| G1 | **A re-entry silently ignores every edited config key, and the agent's briefing is refreshed to describe the edit anyway** | podman/Linux, podman/macOS, container/macOS | Highest. "You add an MCP server / an LSP / a mise tool / raise memory, re-run `yolo`, watch the boot regenerate everything, and get the old value — with no message anywhere." The briefing half is worse than silent: it tells the agent the new limit is in force. | medium (small for the notice, medium for the env-carried half) | **yes** — the re-entry contract |
| G2 | **On Apple Container a re-entry delivers *nothing*: no channel, no briefing, no pack tree — and it burns the one-time handoff on the way** | container/macOS | `yolo -p zai -- claude` prints where the selection landed and runs the previous launch's provider. A rotated API key does not arrive. `handover.md` is renamed `.consumed` by the one entry that could not carry it. | small (~50 lines, one shared materialize step) | no |
| G3 | **`macos-user` runs no pack `service`, so `wire-bridge` is absent — on a default composition** | macos-user/macOS | cerebras's `needs` joins wire-bridge whenever claude or copilot is selected, and cerebras declares its anthropic endpoint as `http://127.0.0.1:8214`. Nothing listens, nothing warns, no witness runs. Claude Code fails to connect while every launch-time surface reports success. | medium (delivery only — `internal/wirebridged` is portable Go) | no |
| G4 | **Two known macOS-15 Apple Container vmnet faults are detected only by `yolo check`, never by the launch** | container/macOS | The jail boots perfectly and cannot reach any API: `npm install` hangs, the agent's first request times out. You learn it only if you happen to run `yolo check`. | small (one call site) | no |
| G5 | **Terminate hooks are unwired everywhere except a Linux TTY, and one of the things they clean up is a credentials file** | macos-user, podman/macOS, container/macOS + podman/Linux non-TTY | Ctrl-C out of a `macos-user` session leaves `<stateDir>/env/<cname>.env` — root-owned 0600, holding every resolved `env_sources` value and provider credential — on disk. Elsewhere: orphaned socats, stale endpoint files, uncaptured config edits, no timing report. On AC, a jail no yolo command can then stop. | small (~30 lines) | no |
| G6 | **On Apple Container the openai-auth broker starts and the jail cannot reach it** | container/macOS | `codex`/`pi` print "OpenAI login is required", the interactive login *also* fails through the same dead hop, and nothing names Apple Container. The launch says nothing at all — this is the one loophole allow-listed on AC, so the inert report never covers it. | small (disclosure) / medium (file-shaped delivery) | **yes** — credential delivery without a network hop |
| G7 | **`macos-user` never delivers the pack `files` kind, which breaks a shipped pack** | macos-user/macOS | `packs: ["pi"]` silently omits `~/.pi/agent/extensions/yolo-openai-auth.js`. The broker starts and is disclosed as running; the extension that dials it never arrives. | small (~15 lines in `buildMacosHomeOverlayFor`) | no |
| G8 | **podman/macOS reports its host-loopback disposition wrong in *both* directions** | podman/macOS | On bridge, `YOLO_HOST_LOOPBACK` is always `unknown`, so the fatal reachability witness never escalates and a total loophole outage is silent — the exact shape of the four-day outage the subsystem exists to end. On `mode: host`, it is `shared`, which is false (the shared namespace is the VM's), so a launch can be refused for a boundary that was never crossed. | small code + one measurement | no |
| G9 | **`required_capabilities` is validated, exported, and read by nothing** | all four | A config declaring `["web_search"]` launches happily on a jail with nothing that provides it; the agent discovers the gap at its first API call. `config_ref.txt:1401-1404` is honest about this, so it is a trap only for someone who reads the key name. | medium (~150 lines + a census) | **yes** — nobody has settled what *satisfies* a capability ([`OQ-CAP2`](../design/agent-auth-modes.md#12-decision-ledger)) |
| G10 | **The `macos-user` declaration-silence set: keys accepted, dropped, and never mentioned** | macos-user/macOS | `mounts`, `network.mode`, `perf_logging`, `programs.autoprune`, `loopholes.<name>.jail_env`, `required_capabilities`, `YOLO_STORE_PACKAGES`, the inherited user scope, `workspace_readonly`'s implicit `yolo-jail.jsonc` lock, and `MISE_ENV`'s whole `mise.jail.toml` mechanism all vanish without a line. DP-D15 already **ruled** the answer (fatal refusal keyed on the declaration being present) and it is not built. | small each; medium as one sweep | no — the ruling exists |
| G11 | **Apple Container is invisible to yolo's own lifecycle commands** | container/macOS | `yolo stop` says "No jail running" while it runs, and exits 0 — so every message prescribing `yolo stop` is unactionable. `yolo prune` reports "none" affirmatively with stopped containers present. Orphans are never reaped. The attach-skew warning and the broken-prefix post-mortem can never fire. | small–medium (one AC inspect/ls JSON reader; several early returns deleted) | no |
| G12 | **`macos-user` enforces no `resources` limit at all** | macos-user/macOS | An agent build can take the whole machine; a fork bomb is unbounded. All three sub-keys were `ruled-wontfix` and all three were overturned. | medium (300-450 lines for the memory watchdog; ~50 for the cooperative env) | **yes** — advisory-vs-enforced and the kill policy |
| G13 | **`macos-user` reads neither port key** | macos-user/macOS | A declared remap (`"9090:8080"`) does nothing, and a host service does not answer at the sandbox's `localhost:<port>`. Both were `impossible`; both fell to a launcher-side loopback proxy. | small (~60-250 lines) | no |
| G14 | **The agent can rewrite its own skills and its own briefing** | macos-user/macOS, container/macOS | On `macos-user` both are writable copies; on AC the briefing is a 0644 file in the writable home, and below `acROBindsFloor` the *skills* bind lands writable onto the launcher's own staging dir. Nothing prints. An agent that edits its own instructions between launches is the failure the `:ro` bind exists to prevent. | small (~150 lines incl. tests) | no |
| G15 | **The platform-inert loophole report reads the manifest default instead of the merged config, and `host-processes` declares no platform at all** | podman/macOS (+ macos-user for the `env` half) | A user who *enables* a Linux-only loophole on a Mac gets a clean launch and no line. Worse, `host-processes` has no `platforms` key, so on macOS the daemon **starts**, the front publishes, the witness passes, and every `yolo-ps` call fails on GNU-procps argv. And `audio`'s pack `env` half still crosses, so `PULSE_SERVER` names a socket that does not exist. | small (one line in the manifest; one resolver fix) | no |
| G16 | **`copilot` and `omp` logins never persist across workspaces, because their manifests never asked** | all four | Every new workspace demands a fresh `copilot` login. The mechanism (`scope: machine` state + a `shared_credentials` hook) is fully built and simply not declared. | small (manifest lines, no Go) | no — but a **fact-finding** blocker: the hook symlinks a single *file* |
| G17 | **A fresh `/login` in one jail can be discarded, and on two backends nothing serializes the refresh at all** | all four | The loser's credential file is `os.Remove`d rather than renamed aside, so a just-completed login is unrecoverable. On `macos-user` and AC no broker runs, so two concurrent sessions can race and burn the single-use refresh token; the launch says the loophole is inert and never names the cost. | small (~10 lines for preserve-the-loser) / medium (start the singleton on the `macos-user` arm) | no |
| G18 | **`providers` has no workspace-scope containment** | all four | A repo-committed, agent-editable `yolo-jail.jsonc` can rewrite the `base_url` of a provider a user-scope profile selects — i.e. point the agent's API traffic at an endpoint of the repo's choosing. Nothing warns; the disclosure line reads identically. This is exactly what [`OQ-CS5`](../reference/providers.md#why-its-this-way)'s refusal text says a committed file must not do, applied to the sibling key that carries the address. | small (~15 lines, mirroring `validateProfiles`) | no |
| G19 | **The `macos-user` bootstrap env is a hand-maintained two-name wire, and four things fall off it** | macos-user/macOS | `YOLO_PROFILES` is missing, so every pack *config surface* renders as if no profile were selected while the agent's own env is correct. `env_sources` do not reach the bootstrap, so `mcp_servers.requires_env` deletes servers whose variable the agent **will** have. `YOLO_PACK_ROOT` is bootstrap-only, so in-sandbox `yolo programs ls` says "run it there" to someone who is there. `YOLO_VERSION` is unset, so `config.InJail()` is false and `yolo host apply` inside the sandbox is not refused — it renders into the shared sandbox home while every message says "your real home". | small (each is 1-15 lines) | no |
| G20 | **`macos-user` has no host-side observability at all** | macos-user/macOS | No `boot.log` (so a scrolled-away provisioning failure is undiagnosable — the exact case the log exists for), no `--timing` table (so the up-to-30-minute nix build, this backend's entire cost, is unmeasured), no housekeeping slot (nothing is ever reclaimed or offered), no `config-boot.json` (so `yolo config drift` answers "cannot determine" forever), no E3 config capture, and `yolo ps` prints a red "Could not query the macos-user runtime" on a healthy machine. | small each (~5-30 lines) | no |
| G21 | **Nix inside the jail: four independent breaks, one of them three lines** | podman/Linux, podman/macOS, container/macOS, macos-user | `nix build` in a jail is refused for `experimental-features` (measured), because the image bakes no `nix.conf` — three lines, the precedent is already in `flake.nix:1717-1721` for the builder image. On AC there is no store mount and no notice. An in-jail build's output gets **no GC root**, so a host `nix-collect-garbage` can delete the store path a running jail is executing from, silently. On `macos-user`, the one backend that *requires* a host nix for every launch is the one whose sandbox cannot see it (`nix: command not found`). A refuter measured a fully working in-jail nix with two podman volumes + that same 3-line `nix.conf`. | small (each break) / medium (store lifecycle) | **yes** — where the in-jail store lives, and who reaps it |
| G22 | **Store-delivered packages and the lean image's extras are refused on both Mac container backends** | podman/macOS, container/macOS | Every distinct `packages:` list costs a full Linux-builder-offloaded image build — the cost C4/C5 exists to remove, absent exactly where it is largest. All four cells were hard claims; all four fell. | small (podman/macOS: ~4 lines deleted, 2 added) / medium (AC: 200-400 lines) | **yes** — the whole-store-bind hazard and the additive fallback |
| G23 | **A `packages:` entry that cannot be built fails as a raw nix trace, three layers from the config line** | podman/Linux, podman/macOS, container/macOS | A typo names its attribute (workable); an unfree or platform-unsupported package surfaces as a check-meta trace from inside `buildEnv`. On a Mac it is worse: a bad package and a missing Linux builder produce failures in the same place, so each reads as the other. | medium-small (~2-3 days) | no |
| G24 | **`prune` is accepted with no validator, is undocumented, and the coverage test that should have caught it passes vacuously** | all four | `prune: {"warn_threshold": 40}` is accepted, does nothing, and cannot be looked up — `config-ref` has no `prune` section. `TestConfigRefDocumentsEveryLiveKey` does `strings.Contains(ref, key)` and "prune" is a substring of "autoprune", so the check is satisfied by a mention of a *different key*. | small (~20 lines + a doc section + the test tightening) | no |
| G25 | **`--network <mode>` is not an override** | all four | `yolo --network bridge` against a workspace whose config says `"mode": "host"` silently launches host-networked, contradicting `yolo run --help`. `resolveNetMode` lets the config win because the flag's default and an explicit flag are indistinguishable. | small (~10 lines, but every `NewDefaultOptions` caller must be checked) | no |
| G26 | **`macos-user` `host_files`: directory sources are dropped, and a home-root destination is shared across every workspace on the machine** | macos-user/macOS | A directory-shaped `host_files` entry (a host nvim config) is warned and never delivered. And a destination at the home *root* (`~/.npmrc`, `~/.netrc`) lands in the single `_yolojail` account home, so workspace A's launch overwrites workspace B's file, silently. | small (~30-50 lines each) | no — the real fix is the already-open per-workspace home design |
| G27 | **Apple Container's dropped keys: `pids_limit`, `ephemeral_storage`, the DNAT fixup, captures materialize, pack `mount`** | container/macOS | A fork bomb is unbounded with nothing printed. `"ephemeral_storage": "volume"` silently gets RAM-backed scratch and can OOM under exactly the workload the key exists to move off RAM. A 127.0.0.1-bound jail service is unreachable on its published port with the same config that works on podman. `yolo capture claude` succeeds and every launch still downloads. A pack `mount` is **disclosed** in the banner and never arrives. | small each — captures materialize is a **five-line** predicate swap | no |
| G28 | **podman/Linux's own small defects** | podman/Linux | `gpu.mode: cdi` for AMD passes a probe that never checks for a CDI spec, so the launch dies on a raw runtime error instead of yolo's warn-and-skip (measured: `unresolvable CDI devices amd.com/gpu=all`). `yolo check` prints "Stop N orphaned jail(s)? [y/N]", reads a `Stdin` nothing assigns, always proceeds as N, then tells you to run the command that just declined. A nested launch drops both port keys and says "NOT applied" where the truth is "already reachable at localhost:H". | tiny each (10 lines, 1 line, 40-70 lines) | no |
| G29 | **`confinement: "guest"` is refused on all four setups** | all four | rc 1, including on a re-entry into a running jail. The refusal is right (the briefing's guest prose would be false of what launched), but the notch is the one a `macos-user` user is most likely to think they already have. | large (Linux: bwrap+Landlock, a whole backend arm) / medium (macOS: mostly a home-tier decision on a backend that ships) | **yes** — exists, [`handoff-guest-notch-macos.md`](./handoff-guest-notch-macos.md) |
| G30 | **`macos-user`'s posture inversions: the confinement keys are advisory and the userland surprise is unannounced** | macos-user/macOS | `host-processes` exists to show *nothing* by default plus an opt-in allowlist; here the sandbox runs the host's own `ps` under `(allow default)` and sees every process on the machine including other users' command lines, gated by no config key. `macos_log: "off"` is likewise advisory — the agent can exec `/usr/bin/log` directly. And [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger)'s no-GNU-userland ruling is invisible: `sed -i`, `find -printf`, `grep -P` and `tar --wildcards` all fail here and work on every container backend, with nothing at launch or in the briefing to predict it. | small (a `(deny process-info*)` + wrapper; one briefing sentence) | **yes** — is the allowlist a boundary or a convenience? |
| G31 | **Device and GPU keys on macOS: three cells that were `impossible` and are not** | macos-user, podman/macOS | `devices` on `macos-user` can at minimum carve the declared node out of yolo's own SBPL deny. `gpu` on podman/macOS: libkrun/krunkit exposes virtio-gpu with Venus, so `--device /dev/dri` gets Vulkan **compute** via MoltenVK — but `deviceArgs` refuses every entry by host OS before any path check, and `validate.go` accepts only `nvidia`/`amd`, so the one GPU an Apple silicon Mac has cannot be named. `kvm` on podman/macOS probes the **Mac** for `/dev/kvm` when the device would live in the VM. | small each (+ a real-Mac verification) | no |
| G32 | **The host user-level skills tree does not exist, and AGENTS.md still documents it** | all four | A skill in `~/.claude/skills` on the host does not reach the jail. `SkillTarget.HostSource` was removed by S3 because it was set to the *destination*; the middle term of "built-in < host user-level < workspace" is gone. Two routes already reach the outcome (the conventional local pack; a filtered `packs` entry pointing at `~/.claude`), and **no OQ, ledger row or comment rules the literal path out on purpose**. | zero (document route 1) / small (~tens of lines for route 2's robustness fix) | no — but fix the AGENTS.md sentence either way |
| G33 | **An inline `loopholes.<name>` record gets no container-side plumbing** | all four | No bind, device, intercept, CA or `jail_env`. Currently harmless because the inline key census cannot *express* any of it — an attempt is an unknown-key error, so the filter and the census agree. A user reaches the full outcome today with zero code by declaring the loophole in the conventional local pack at `~/.config/yolo-jail/local`. | zero (workaround) / small-medium (~200-350 lines for the inline spelling) | no |

### Build notes — the refuters' mechanisms, carried forward

These are the "what to build" details for the rows above that came out of an overturned hard claim. They are the
most actionable material in the audit because each names an existing seam.

**G1 (re-entry).** Three mechanisms, ascending. **(1)** Move the env block onto the channel that already
re-delivers per entry: add `exportPlain` lines for `YOLO_MISE_TOOLS`, `YOLO_LSP_SERVERS`, `YOLO_LSP_*_INSTALL`,
`YOLO_MCP_SERVERS`, `YOLO_MCP_PRESETS`, `YOLO_BLOCK_CONFIG`, `YOLO_REQUIRED_CAPABILITIES`,
`YOLO_FORWARD_HOST_PORTS` to `writeUserEnvFile`'s channel section (`internal/cli/run/userenv.go:83-127`) and
delete the matching `-e` pairs from `internal/cli/run/assemble.go:932,945-950,963`. Nothing else changes:
plain-form lines already beat the frozen container env (`boot.go:164-170`), hydrate is the boot's first step
(`boot.go:509`), and the attach arm already rewrites the file (`run.go:1630-1631`). This is the fix that kills the
headline symptom. Hazard the code already documents: a plain-form line must not also be a def-form default, or a
stale copy survives an entry that did not compose it. **(2)** Call
`config.WorkspaceConfigDrift(o.Workspace)` (`internal/config/drift.go:65`) from `attachExisting` and print the
diff restricted to argv-only keys, with the "restart to pick this up" remedy the provider branch already words
(`run.go:1620-1625`) — ~20 lines, turns `Dropped` into `Warned` on its own. **(3)** The runtime-mutable subset:
`podman update` accepts `--memory`/`--cpus`/`--pids-limit` on a running container, **but whether it takes effect
live is unsettled** — measured in this nested jail (podman 5.8.6, `rootless=false`, cgroupfs v2),
`podman update --memory 1g` errored `open memory.max for writing: Read-only file system` and yet still moved
`HostConfig.Memory` while `memory.max` stayed `max`. That is CARVE-OUT 2 territory; the instrument is a real
non-nested rootless Linux host. The delegate-daemon route (`internal/cgd/ops.go:83-111` pointed at the jail's own
cgroup) does not depend on that answer. Separately and independently: make the briefing honest on attach — render
the resources/ports sections from the container's **inspected** env, or suppress them, because
`refreshJailBriefings` at `run.go:781` runs *above* the attach return at `:789` and today re-renders live config
into a briefing the container does not implement.

**G12 (`macos-user` resources).** `resources.memory`: a darwin-only sampled watchdog on the session process tree,
hung on `ttyproxy.RunWithProxyHooked`'s existing `onStarted func(*os.Process)` seam
(`internal/ttyproxy/ttyproxy.go:123,:185`) — parse the `"8g"` string, walk descendants via
`ps -axo pid=,ppid=,rss=`, sum RSS, on breach print a red line naming the key and SIGTERM→SIGKILL the tree.
~300-450 lines; everything decision-shaped is unit-testable on Linux against a fixture `ps` table. Disclose it as
**"sampled, not kernel-enforced"** and replace the unconditional "resources are NOT enforced" sentence for the
`memory` sub-key only. `resources.cpus`: build **M2 first** — derive `GOMAXPROCS`, `MAKEFLAGS=-j N`,
`CARGO_BUILD_JOBS`, `RAYON_NUM_THREADS`, `OMP_NUM_THREADS` from the key and set them through the same `env.Set`
path that already carries `MISE_TRUSTED_CONFIG_PATHS` (`internal/macosuser/orchestrator.go:206-222`). ~50 lines;
`GOMAXPROCS` is runtime-enforced inside every Go process, and `-j N` is honored by make/ninja/cargo, which covers
an agent session's dominant CPU consumers. `resources.pids_limit`: a per-session process-tree supervisor
(`SysProcAttr{Setpgid: true}` + `kill(-pgid, …)` on breach), ~200-300 lines — **not** the `ulimit -u` one-liner,
which is the mechanism DP-D1 actually ruled out and which is a no-op at the 32768 default anyway. One
by-product finding worth its own fix: `internal/cli/config_ref.txt:1608` and `internal/entrypoint/boot.go:242`
both claim `yolo-cglimit` "falls back to nice/timeout/ulimit"; `cmd/yolo-cglimit/main.go:113-118` prints
"cgroup delegation not available" and returns 1. There is no fallback.

**G13 (`macos-user` ports).** `forward_host_ports`: one loopback proxy per **remap** entry (skip same-port
entries — vacuously satisfied, and a proxy there collides with the host service), started beside the existing
`startOpenAIAuth`/`defer o.stopLoopholes` pair at `run.go:343`/`:349`, parsed with the existing
`ParsePortForwards` (`internal/cli/run/hostports.go:39`). Pure Go `net.Listen` + `io.Copy` rather than socat,
because this backend bakes nothing. ~60-100 lines. `network.ports`: the mirror image, host-side, plus an optional
confinement upgrade — `(deny network-bind (local ip "*:*"))` + a loopback re-allow in `SeatbeltProfile`, which is
already last-match-wins and already takes per-launch config. Whether SBPL's `network-bind` filter has port
granularity is **unmeasured**; the one-command probe is in [§5](#5-unmeasured-cells).

**G14 (skills/briefing read-only).** Per-destination Seatbelt write denies: `buildMacosHomeOverlay` already holds
the home-relative destination list (`internal/cli/run/macoshomeoverlay.go:42-53,:64-76`); thread it into
`macosuser.BuildRunPlan` beside the `hostHomeOverlay` string it already takes and render it as a
`homeReadonlyDenies` **sibling** of `readonlyDenies` — a sibling because `readonlyDenies` deliberately drops
absolute entries as defence in depth for the workspace-relative key. Emit it in the same post-allow position
(`internal/macosuser/seatbelt.go:56`), which the existing tests already pin as the load-bearing property. This
moves the disposition `Warned → HonoredBy` with a *stronger* guarantee than the container `:ro`. Trap: the
workspace reaches `SeatbeltProfile` `EvalSymlinks`'d and `SandboxHome()` does not — an unresolved SBPL path
matches nothing (measured 2026-09-13). The briefing half is even cheaper and is reachable **today with zero code**
via `workspace_readonly: [".yolo/home/claude/CLAUDE.md"]`, because Seatbelt resolves the target.

**G21/G22 (nix and store delivery).** The measured one first: in this jail, against
`localhost/yolo-jail:latest`, with `--read-only`, `NIX_REMOTE` unset and no `/nix` host mount, two podman volumes
for `/nix/store` and `/nix/var` plus a 3-line `nix.conf` (`experimental-features`, `sandbox = false`,
`build-users-group =` — byte-for-byte `flake.nix:1717-1721`) gave a working `nix build nixpkgs#hello` (RC=0,
substituted from cache.nixos.org). podman seeds a fresh volume from the image's directory content, so
`/nix/store` comes up writable **already holding the image's closure** — which is why `/bin/bash` keeps
resolving, the failure that motivated `YOLO_NIX_HOST_STORE_LINUX`. Both volumes are needed: with only
`/nix/store`, the failure moves to `creating directory /nix/var/nix/profiles: Read-only file system`. Store
delivery on podman/macOS is nearly a config change — delete the `if isMacOS` clause at
`internal/cli/run/storepackages.go:115-118` and let the `storeMounted` clause carry macOS (on darwin
`shouldMountHostNix` already requires both operator claims), then pass the Linux double instead of `""` at
`:327`. One behaviour change needs care rather than a copy-paste: a materialize failure is currently **fatal**
(`:171-177`), which is right on Linux and wrong on macOS, where the honest disposition is to fall back to baking
with the same `ignored:` line. On AC, split `shouldMountHostNix` into "mount the socket" (never on AC) and "mount
the store `:ro`" (fine on AC ≥ `acROBindsFloor`) — and gate it, because a **writable** host `/nix/store` inside a
jail is a host-store injection channel, and mounting the host store *over* the image's own is what killed the
2026-09-13 podman nightly. Additive per-path binds are the worse-but-workable fallback. The GC-root half wants a
host-mediated build request over the writable workspace bind calling the existing `registerGCRoot`
(`internal/image/gcroot.go:73-95`) — file-shaped, so it survives AC's measured container→host outage.

**G27 (AC captures).** Replace `internal/cli/run/captures.go:59`'s `if rt == "container"` with the shared
predicate the other four sites already use: `if reason := o.roBindsUnsupported(rt); reason != "" { … }`. On
`container` ≥ 1.1.0 the existing `-v dir:/ctx/captures:ro` + `CapturesDirEnv` argv (`captures.go:97-100`) then
applies unchanged and the cell becomes `Honored`; below the floor it stays refused and now says why. ~5 lines
plus rows in `acrobinds_test.go`'s existing fake-probe table. Under an hour, and behaviour is unchanged on any
machine with no `container` CLI. Independently: auto-capture still runs on backends that cannot read the store,
which is its own two-line row.

**G31 (`macos-user` devices).** Thread `cfgList(cfg, "devices")` into `SeatbeltProfile`
(`internal/macosuser/runplan.go:385` already takes a config-derived list) and emit
`(allow file-read* file-write* (literal "<path>"))` per raw-path entry **after** the raw-disk/bpf deny
(`internal/macosuser/seatbelt.go:63-66`). ~20 lines, all testable on Linux. Budget for one trap:
`TestSeatbeltProfileHasNoWriteAllowAfterReadonlyDenies` forbids the literal substring `(allow file-write*`
anywhere after the readonly denies, so the invariant has to be re-spelled as path-scoped rather than textual —
the right change anyway, since `/dev` is disjoint from any workspace path. Note the framing correction the
refuter made: on podman, `devices` is not an *addition* to an open `/dev`; the container has no host devices and
the key **is** the allow-list, so "restriction" is what the key already does on the columns where it is honored.

**G33 (inline loopholes) and G32 (host skills).** Both have a zero-code route that should be **documented rather
than built**: the conventional local pack at `paths.LocalPackDir()` = `~/.config/yolo-jail/local` is appended to
the pack list implicitly whenever the directory exists, with no `packs` edit
(`internal/config/packs.go:246`, `localPackEntry` at `:295-307`), and a pack-shipped loophole gets the full
container-side plumbing with `HostExecApproved: true` unconditionally. Coverage: `binds`, `devices`, `intercepts`
and `ca_cert` are all honored; `jail_env` is the one refusal and its error text names its own substitute (the
pack `env` kind, worse in exactly one documented way — `env` is unconditional where `jail_env` applied only when
the loophole was active).

## 3. Silent drops

**The repo's own highest-priority defect class: a key is accepted, it does nothing, and no launch says so.** The
distinction that matters is between the reference setup and the rest. On podman/Linux there is no parity story to
tell — the key is simply broken, and every row below is a pure defect. On the other three, a silent drop is a
parity gap, which is a different argument but the same user experience.

### 3a. podman/Linux — pure defects

| Silently dropped | What actually happens | File that should print the notice |
|---|---|---|
| `required_capabilities` | Shape-validated (`internal/config/validate.go:1392-1398`), exported as `YOLO_REQUIRED_CAPABILITIES` (`internal/cli/run/assemble.go:964`), read by nothing — the writer, its golden test and a `config_ref` sentence are the only three hits in the tree | `internal/cli/run/preflight.go` — the pre-flight refusal [`OQ-CAP2`](../design/agent-auth-modes.md#12-decision-ledger) ruled, beside `checkProviderCredentials` (`run.go:404-409`), which is the same shape and already runs on all four backends |
| `prune`, every sub-key | Accepted at `internal/config/config.go:66` with **no validator at all**, so `prune: "hello"` and `prune: {"typo": true}` both pass; one sub-key has one reader, in `yolo check`, which skips in-jail | `internal/config/validate.go` (a `validatePrune` with a `knownPruneKeys` census) **and** `internal/cli/config_ref.txt`, which has no `prune` section |
| `providers` at workspace scope | Composed **over** pack-declared provider facts with no scope refusal — `providers` appears in none of the user-scope-only messages, unlike both profile keys (`internal/config/profiles.go:177-180`) | `internal/config/validate.go` — refuse the address-bearing fields (`base_url`, `endpoints`, `region`, `wire_api`) at workspace scope |
| `--network <mode>` | The flag is only a default for a config that never set the key: `if m := mapStr(netSec, "mode"); m != "" { netMode = m }` | `internal/cli/run/loopholesruntime.go:39` (`resolveNetMode`) — and make `Options.Network` empty by default so "given" and "defaulted" stop being the same state |
| Every config key, on a **re-entry** | `attachExisting` returns at `run.go:789`, above `checkConfigChanges` (`:794`) and `writeLaunchConfigArtifacts` (`:799`). No prompt, no notice, no refreshed drift baseline — and `refreshJailBriefings` at `:781` runs *above* the return, so the agent's briefing is rewritten to describe the edit | `internal/cli/run/run.go` — `attachExisting` (`:1470-1553`), which today prints a banner, a skew line, the no-packs notice and the channel delivery, and nothing about config |
| Git identity, when the host has neither `user.name` nor `user.email` | `if name == "" && email == "" && !haveIgnore { return nil }` (`internal/cli/run/assemble_parts.go:289-291`) — no mount, no env, no warning. The half-set case is worse: with a name and no email the file **is** composed and mounted, so the launch looks fully provisioned | `internal/cli/run/assemble_parts.go` (one emitter beside `gitIdentityMountArgs`; the information is in hand at `:278-279`) **plus** a `yolo check` section — nothing under `internal/cli/check/` reads `user.name`/`user.email` today |
| `gpu.mode: cdi` with `vendor: amd` | `rocmHostAvailable` checks amdgpu/kfd/render-node and never looks for an AMD CDI spec, so the probe **passes** and the launch dies on a raw runtime error (measured: `unresolvable CDI devices amd.com/gpu=all`). `yolo check` already checks for the spec | `internal/cli/run/hostprobes.go:147-173` — give the probe the mode, and require the exact predicate `internal/cli/check/sections_devices.go:159-166` already spells |
| An in-jail `nix build`'s durable GC root | Rooting is deliberately skipped in-jail (verified 2026-07-22, recorded at `internal/image/gcroot.go:43-48`), and a **user's** own build gets no root either, so a host `nix-collect-garbage` can delete the store path a running jail is executing from | `internal/entrypoint` boot path (or the `nix` wrapper) — today nothing in the jail warns |
| `nix-command` / `flakes` | The image bakes no `/etc/nix/nix.conf` and the boot writes none; yolo's own calls are unaffected because every one passes the flag, so the gap lands entirely on the human or agent typing `nix` (measured refusal, and the build succeeds with the flag added by hand) | `flake.nix` (one `cat >` in `mkBinPathLinks`) or the entrypoint writing `~/.config/nix/nix.conf` |
| `yolo check`'s orphaned-jail cleanup | The prompt prints, reads `o.Stdin`, and nothing in production assigns it (`internal/cli/check/checkcmd.go:264-266`; nil ⇒ false at `internal/cli/check/helpers.go:103-105`), so the branch is unreachable and it then tells you to run the command that just declined | `internal/cli/commands.go` — one line, `opts.Stdin = os.Stdin`, plus the non-TTY guard the reclaim offer already uses (`internal/cli/run/offer.go:238`) |
| Non-TTY stdin ⇒ the whole terminate chain | `RunWithProxyHooked` returns `runPlain` when stdin is not a tty, and `runPlain` takes `onStarted` but **not** `onTerminate` (`internal/ttyproxy/ttyproxy.go:155-158,:239-252`). The pty bypass is right; dropping the terminate hook is not — `signal.Notify` needs no pty | `internal/ttyproxy/ttyproxy.go` — give `runPlain` the parameter and the handler set, ~20 lines |

### 3b. The other three setups — parity gaps with the same symptom

| Silently dropped | Setup | What actually happens | File that should print the notice |
|---|---|---|---|
| `mounts`, `network.mode`, `perf_logging`, `programs.autoprune`, `required_capabilities`, `loopholes.<name>.jail_env`, `YOLO_STORE_PACKAGES`, `MISE_ENV`, the inherited user scope, `workspace_readonly`'s implicit `yolo-jail.jsonc` lock | macos-user | All are read only below the `macos-user` arm's return, or are absent from the closed `sandboxEnvPairs` / bootstrap wire lists. DP-D15 already ruled the answer (fatal refusal keyed on the declaration being present) and it is not built | `internal/cli/run/loopholeinert.go` — the four `noteMacosUser*Gaps` printers already on that arm cover only `devices`/`gpu`/`kvm`, the port keys, directory `host_files`, and skills/briefings |
| pack `service` (`wire-bridge`) and pack `files` | macos-user | `Decl.Services()` and `packFilesMountArgs` are reached only from `assembleRunCmd`; `noteMacosUserContentGaps`' own header enumerates briefings/skills/mise/lsp/mcp_presets and never mentions either | `internal/cli/run/loopholeinert.go` — a `noteMacosUserServiceGaps` beside the four existing notes (~15 lines) is the small half of G3 |
| pack `mount` | macos-user | Worse than silence: `notePackHostAccess` runs on this arm and classifies `mount` as `disclosureRead`, so the banner **prints a disclosure** for a mount that never arrives | `internal/cli/run/loopholeinert.go`, or drop the disclosure — a disclosed-but-undelivered grant is worse than a silent absence |
| The channel (`-p`, `env_sources`), briefings, and the pack tree, on a **re-entry** | container/macOS | The attach writes a file this backend does not read: the jail reads `<wsState>/.config/yolo-user-env.sh`, an `acMaterialize` **copy**, while `deliverChannelOnAttach` rewrites the source at `<wsState>/yolo-user-env.sh` (`run.go:1629-1630`). And `noteUseProfiles` at `:1640` still prints, asserting the delivery it did not make | `internal/cli/run/run.go` — the attach arm should call the AC materialize set, and `noteUseProfiles` should be conditional on a delivery that happened |
| The one-time handoff | container/macOS | The consume gate asks whether a briefing was **staged**, not whether it was **delivered**, so a re-entry burns `handover.md` and the notice claims it "surfaced in this jail's briefing" | `internal/cli/run/prepare.go:220-226` — pass a delivered count out of `refreshJailBriefings`, or move the consume below the backend dispatch |
| Two macOS-15 vmnet faults | container/macOS | Both fully **detected** — and only from `yolo check` (`internal/cli/check/sections_macos_platform.go:37` is the sole caller). The launch is silent; the jail boots and reaches nothing | `internal/cli/run/preflight.go` — call the two existing fail-open probes on `rt == "container" && macOSMajor == 15` |
| `ephemeral_storage`, `resources.pids_limit`, the DNAT fixup | container/macOS | `appleContainerBaseMounts` hardcodes `--tmpfs`, so `"volume"` silently gets RAM-backed scratch; no pids flag is ever passed; the DNAT gate is `rt == "podman"`, so a 127.0.0.1 listener is unpublishable with a config that works on podman | `internal/cli/run/assemble_parts.go` / `internal/cli/run/backendcaps.go` — note that [`backend-parity.md` §5.1](../design/backend-parity.md#51-confirmed-drops-i-deliberately-did-not-warn-about) and `:303` ruled out the *warning* specifically, so this row needs the ruling revisited, not just a print |
| An **enabled** Linux-only loophole | podman/macOS | `platformInertLines` resolves each loophole through `LoadLoophole` — the manifest only, no config — so `Enabled` is the `default_enabled: false` default and `PlatformInertNotes` skips it. A user who switched `audio` on gets a clean launch and no line, and the pack's `env` half still sets `PULSE_SERVER` | `internal/cli/run/loopholeinert.go:250-256` — resolve `loopholes.ConfigEnabledOverride` (`discover.go:135`) instead of the manifest default |
| `host-processes`, which does **not** drop | podman/macOS | The manifest declares no `platforms`, so the daemon starts, the front publishes, the reachability witness passes — and all three modes emit GNU-procps argv (`--forest`, `-C`, `/proc/<pid>/comm`). A healthy-looking service that fails every call | `packs/host-processes/loopholes/*/manifest.jsonc` — one line, `platforms: ["linux"]`, converts a silent failure into the `Warned` line `journal` already gets |
| Skills and briefing writability | container/macOS | The skills mount is the one `:ro` site that does not consult `roBindsUnsupported`, so below `acROBindsFloor` the agent gets a **writable** bind onto the launcher's own staging dir; the briefing is a 0644 copy in the writable home | `internal/cli/run/assemble.go` (route the skills loop through `o.roBindsUnsupported(rt)`) and `internal/cli/run/loopholeinert.go:304` (extend the copy-delivery warning to the AC materialize path) |
| The terminate chain | all three non-Linux | `internal/cli/run/proxy_other.go:21` is `_ = onTerminate` with no handlers, so `stopJail`, `cleanupPortForwarding`, `stopLoopholes`, `captureConfigOnTerminate`, `recordWindowA` and `emitTimingReport` are all skipped | `internal/cli/run/proxy_other.go` — a `signal.Notify` shim needs no pty and fixes three columns at once (~30 lines) |
| Window A attribution | container/macOS | `attributeWindowA` returns the zero result for any non-podman runtime — no duration, no reason, no token — which is precisely the "observability feature that cannot explain its own blank" failure `perfevents.go:53-58` says it was rewritten to remove | `internal/cli/run/perfevents.go:65-67` — give the not-applicable branch a reason token like every other failure mode |

## 4. Permanent limits

**One claim survived refutation.**

| Limit | Setup | The structural fact |
|---|---|---|
| **NVIDIA GPU passthrough** | container/macOS | Three independent facts stack, and the third is Apple Container's own: there is no NVIDIA driver stack on macOS, no CUDA for Apple silicon, and **Apple Container passes no devices at all** (`internal/cli/check/sections_devices.go:150-155`) — no `--device`, no CDI, no device-attach surface of any kind. With no attach surface there is no mechanism, worse or otherwise, to reach the outcome. The launch already says so with one yellow line, and `yolo check` adds its own. |

Note what this row does **not** cover, because the distinction is the whole reason the refutation pass was worth
running: the same key on **podman/macOS** was overturned. libkrun/krunkit on Apple silicon exposes virtio-gpu
with Venus, so `--device /dev/dri` gets Vulkan **compute** translated to Metal via MoltenVK — no CUDA and no
render, but "the jail gets GPU compute", which is the outcome the row claims. yolo blocks it in three places by
host OS, and cannot even *name* the GPU an Apple silicon Mac has (`validate.go:1466` accepts only `nvidia` and
`amd`). That is G31, a one-day change plus a real-Mac verification.

**Standing by default, never attacked** — the 13 in [§1](#1-what-this-is). Treat none of them as settled prose. The five ELF/loader
rows are the most likely to be genuinely structural (Mach-O resolves a dylib by absolute `install_name`; a
darwin binary has no `PT_INTERP`, no `/lib64`, and no ELF loader to give it; SIP strips `DYLD_*` from protected
processes) and the two `kvm` rows have a stated pre-answer worth keeping: accelerated virtualization on Darwin
runs through Hypervisor.framework, which requires a code-signing entitlement on the executing binary, and a
Seatbelt profile cannot grant an entitlement — so that route is a different feature request rather than a worse
mechanism for the same key.

## 5. Unmeasured cells

**Of 705 cells, 40 are `measured`, 11 are `inferred`, and 654 are `read-the-code`.** Twenty-two are classified
`unknown` — meaning no amount of further reading settles them.

**The least-measured column is podman/macOS: 0 measured cells out of 141, and 12 of the 22 unknowns.** That is
also the column where the launch's disposition is `unknown` by construction (`hostLoopbackFactsFor` returns bare
facts on `rt != "podman" || o.IsMacOS`), which means the **fatal reachability witness never escalates there** —
the one column where the escalation that exists to prevent a silent loophole outage is switched off. Every other
column is between 4.7% and 11.7% measured.

| Setup | Cells | measured | inferred | `unknown` |
|---|---:|---:|---:|---:|
| podman/Linux | 180 | 21 | 0 | 1 |
| container/macOS | 163 | 8 | 4 | 6 |
| macos-user/macOS | 169 | 8 | 3 | 3 |
| **podman/macOS** | **141** | **0** | 4 | **12** |
| all (backend-independent) | 52 | 3 | 0 | 0 |

### Grouped by the instrument that settles them

**A real Mac running podman machine** (the gvproxy/VM hop — 12 cells, the whole least-measured column):

- Can the jail reach a host service on the Mac's own `127.0.0.1`? One command:
  `(exec 3<>/dev/tcp/host.containers.internal/<port>)` from a podman/macOS jail against a listener on the Mac's
  loopback. This single answer settles `claude-oauth-broker` (**`default_enabled: true`**, so every macOS podman
  user selecting the claude pack is exercising an unmeasured hop by default), `openai-auth-broker`, `serial`,
  every inline config-declared loophole, and G8's disposition.
- Whether the DNAT fixup works at all across the VM hop, and whether the two doc claims that say a 127.0.0.1
  listener is *not* publishable are true (`config-ref` and the agent briefing both say so; if the DNAT works,
  both are false and every agent is told to widen its bind for no reason).
- Who owns the files the agent writes in `/workspace` — your uid, or the VM's `core` uid appearing as an
  unfamiliar numeric owner.
- Whether Window A's number is trustworthy: the die/cleanup timestamps come from inside the VM while
  `podmanExited` is a host-side mark, so VM/host clock skew lands directly in the reported gap.
- Whether a vendor-installer capture share is present (an absent share and an empty store are the same observation).
- `cache_relocations` onto an unshared volume — expected to fail the *launch* rather than no-op silently, which
  is the safe direction, unconfirmed.

**A real Mac running Apple Container ≥ 1.1.0** (6 unknowns plus most of [§2](#2-ranked-gap-backlog)'s AC rows):

- Whether `network.ports` publishes at the address yolo believes.
- Whether the in-jail `iptables` DNAT fixup can be written at all.
- Whether headless chromium launches and serves CDP (plausible failure: it starts and dies on shared memory,
  surfacing as an MCP tool that times out rather than a launch error).
- What `^Z` does — nobody has pressed it in an AC jail and written down the answer; the doc explicitly refuses to
  let podman's behaviour be inherited.
- The shape of `container inspect`'s payload, which gates G11 wholesale (the strict `mountsArrayFrom` guard is
  currently the right default precisely because the payload is unmeasured).
- Whether AC exec succeeds against a container wedged mid-provision — the reason to prefer the host-side
  log-freshness detector, which cannot hang.
- Whether `--publish-socket` works, which gates the best of the four host-mode substitutes.

**A real Mac, Seatbelt probes** (3 unknowns, plus the confinement claims in G13/G14):

- Whether `connect(2)` to `/nix/var/nix/daemon-socket/socket` survives `(deny file-write* (subpath "/"))` —
  Seatbelt governs unix-socket connect through a different operation, and nobody has run it.
- Whether an in-sandbox `nix build` therefore works or dies on permissions.
- Whether an indirect gcroot symlink into `/nix/var/nix/gcroots/auto` is denied.
- One-line probes that settle two backlog rows:
  `sandbox-exec -p '(version 1)(allow default)(deny network-bind (local ip "*:8000"))' python3 -m http.server 8000`
  next to the same on 8001 (G13's confinement half); and whether `claude`/`copilot` start cleanly with an
  unwritable `~/.claude/skills` on darwin (G14).

**A real rootless Linux host, or CI** (the reference setup's own blind spots — this is CARVE-OUT 2 territory and a
nested jail reports `rootless: false`):

- Whether `podman update --memory` takes effect **live** on cgroup v2 with delegation. Measured in this nested
  jail it errored on a read-only `memory.max` and still moved `HostConfig.Memory` — a false green in the worst
  direction. Report `podman info --format '{{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}'` with any answer.
- Whether the loopback-forwarding option a launch emits actually forwards on a given host's passt build. A nested
  jail is *structurally* blind here (`--net=host` makes the host's loopback and the jail's the same loopback);
  bare `podman run --network=pasta:…` from inside this jail is the reproduction that works.
- The reference setup's one `unknown`: whether the DNAT fixup works in a **non-nested** podman jail.

## 6. How this was produced, and how to re-run it

**Pass 1 — the grid.** Fourteen agents, each given one **group** (a slice of the vocabulary: `fs-mounts`,
`network`, `resources-devices`, `loopholes`, `pack-kinds-a`/`-b`, `identity-agents`, `packages-tools`,
`toolchain-inside`, `lifecycle`, `host-surface`, `meta-keys`, `user-questions-1`/`-2`) and **all four setups** as
columns. Each cell had to carry: the mechanism with `file:line`, the disposition the launch takes, when the key
takes effect, the symptom, what it would take to close, any host-fact caveat, and an explicit confidence
(`measured` / `read-the-code` / `inferred`). Groups were assigned by vocabulary rather than by setup on purpose:
one agent holding a row across all four columns is what makes a *parity* claim checkable, and a
`podman/Linux`-only agent cannot notice that a mechanism was wired into one branch and nothing checks the others
(the B-0 shape that recurs throughout [§3](#3-silent-drops)).

**Pass 2 — adversarial refutation.** Every `impossible` and `ruled-wontfix` cell was extracted (46) and handed
back with an inverted brief: *defeat this claim.* A claim survives only if **no mechanism, however worse, reaches
the same user-visible outcome** — a worse mechanism that reaches the outcome makes the cell `unbuilt-gap`, and a
mechanism that already ships makes it `works-differently` or `works`. Refuters were required to name existing
seams with citations, give a size estimate, and label anything they could not run as an open probe with the
instrument that settles it. Capped at seven claims per group, which is why 13 cells were never attacked ([§1](#1-what-this-is)).

**To re-run it.** The two artifacts are `/tmp/slices/all.json` (705 cells, one object per cell with the fields
above) and `/tmp/slices/overturned.json` (32 objects: `row`, `setup`, `was`, `verdict`, `to`, `alt`). **`all.json`
is pre-refutation** — an overturned cell still carries its old `gap_class` there, so any re-run must join the two
files on `(row, setup)` rather than trusting `all.json`'s classification alone. Filter with python, not by reading
(the `alt` fields alone are 80 KB).

Three things a re-run should do differently:

1. **Attack the remaining 13**, starting with the ELF/loader five. A 32-of-33 kill rate is a statement about how
   this corpus writes the word "impossible".
2. **Close the measurement gap in the podman/macOS column before auditing it again.** A second reading pass over
   141 cells with zero measured facts produces a second set of `read-the-code` cells, and the one question that
   unblocks a dozen of them is a single `/dev/tcp` probe from a real Mac.
3. **Re-verify every `file:line` in this document against the tree before citing it downstream.** The audit found
   more than fifty false doc claims as a by-product — including one this document repeats as a finding
   (`yolo-cglimit`'s non-existent nice/ulimit fallback, asserted in both `config_ref.txt:1608` and
   `boot.go:242`) and one about AGENTS.md's own PATH-order claim. Drift clusters at status lines and at exactly
   the numbers a reader stops checking. The re-runnable sweeps and their allowlists are in
   [docs/plans/README.md](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).