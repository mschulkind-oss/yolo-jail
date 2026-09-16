---
title: "Backend gap tracker: the 705-cell audit, its 32 overturned hard claims, and the ranked backlog they produce"
date: 2026-09-16
status: current
tags: [gap-tracker, backend-parity, macos-user, apple-container, podman, silent-drops, refutation, measured]
summary: "Four setups crossed with every closed vocabulary yolo has — config keys, pack contribution kinds, shipped loopholes — plus the user-facing capabilities that are not keys, audited cell by cell (705 cells, 14 agents), then every 'impossible' and 'ruled-wontfix' claim handed to an adversarial refuter. 33 hard claims were attacked and 32 fell; exactly one survived (NVIDIA passthrough on Apple Container). What is left is a backlog of 34 ranked gaps — most of them small, several of them silent losses of a shipped default, one of them launch-breaking — and a silent-drop table naming the file that should print each missing notice. A measurement pass on 2026-09-16 settled 15 of the 22 cells no reading could settle, including the whole podman/macOS column that had zero measured cells out of 141: the Mac's loopback IS forwarded, the DNAT fixup is NOT, and on Apple Container TCP crosses in neither direction while a published unix socket crosses in the wrong one."
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

> [!NOTE]
> **This table is as the grid was audited, and 15 of the 22 `unknown` cells have since been settled** by the
> 2026-09-16 measurement pass in [§5.1](#51-what-is-now-measured) — which also found one defect the grid had
> classified as working (G34). It is left un-recomputed on purpose: re-deriving 705 cells from 15 answers would
> mean re-reading the other 690, and a table that mixes audited and estimated counts is worse than one with a
> date on it. [§5](#5-measured-cells-and-what-is-still-unmeasured) is the current answer wherever the two disagree.

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

**Ids are stable; the ORDER is not re-derived.** G34 was added by the 2026-09-16 measurement pass
([§5.1](#51-what-is-now-measured) row 8) and ranks second by impact — it is a launch-breaking defect, not a
missing feature — but it sits at the end of the table so that every id already cited downstream keeps its
meaning. Rows whose disposition that pass CHANGED carry a **Measured 2026-09-16** note.

| # | Gap | Setups affected | User impact | Size | Design doc? |
|---|---|---|---|---|---|
| G1 | **A re-entry silently ignores every edited config key, and the agent's briefing is refreshed to describe the edit anyway** | podman/Linux, podman/macOS, container/macOS | Highest. "You add an MCP server / an LSP / a mise tool / raise memory, re-run `yolo`, watch the boot regenerate everything, and get the old value — with no message anywhere." The briefing half is worse than silent: it tells the agent the new limit is in force. | medium (small for the notice, medium for the env-carried half) | **yes** — the re-entry contract |
| G2 | **On Apple Container a re-entry delivers *nothing*: no channel, no briefing, no pack tree — and it burns the one-time handoff on the way** | container/macOS | `yolo -p zai -- claude` prints where the selection landed and runs the previous launch's provider. A rotated API key does not arrive. `handover.md` is renamed `.consumed` by the one entry that could not carry it. | small (~50 lines, one shared materialize step) | no |
| G3 | **`macos-user` runs no pack `service`, so `wire-bridge` is absent — on a default composition** | macos-user/macOS | cerebras's `needs` joins wire-bridge whenever claude or copilot is selected, and cerebras declares its anthropic endpoint as `http://127.0.0.1:8214`. Nothing listens, nothing warns, no witness runs. Claude Code fails to connect while every launch-time surface reports success. | medium (delivery only — `internal/wirebridged` is portable Go) | no |
| G4 | **Two known macOS-15 Apple Container vmnet faults are detected only by `yolo check`, never by the launch** | container/macOS | The jail boots perfectly and cannot reach any API: `npm install` hangs, the agent's first request times out. You learn it only if you happen to run `yolo check`. | small (one call site) | no |
| G5 | **Terminate hooks are unwired everywhere except a Linux TTY, and one of the things they clean up is a credentials file** | macos-user, podman/macOS, container/macOS + podman/Linux non-TTY | Ctrl-C out of a `macos-user` session leaves `<stateDir>/env/<cname>.env` — root-owned 0600, holding every resolved `env_sources` value and provider credential — on disk. Elsewhere: orphaned socats, stale endpoint files, uncaptured config edits, no timing report. On AC, a jail no yolo command can then stop. | small (~30 lines) | no |
| G6 | **On Apple Container the openai-auth broker starts and the jail cannot reach it** | container/macOS | `codex`/`pi` print "OpenAI login is required", the interactive login *also* fails through the same dead hop, and nothing names Apple Container. The launch says nothing at all — this is the one loophole allow-listed on AC, so the inert report never covers it. **Measured 2026-09-16:** the dead hop is confirmed with bytes and generalises (TCP establishes both ways, carries data neither — [§5.1](#51-what-is-now-measured) rows 6-7), and a **working transport now exists**: a published unix socket crosses, host→container (row 8). So the second size below is no longer the only route — see G34. | small (disclosure) / medium (file-shaped delivery, or the host-initiated socket relay G34 names) | **yes** — credential delivery without a network hop |
| G7 | **`macos-user` never delivers the pack `files` kind, which breaks a shipped pack** | macos-user/macOS | `packs: ["pi"]` silently omits `~/.pi/agent/extensions/yolo-openai-auth.js`. The broker starts and is disclosed as running; the extension that dials it never arrives. | small (~15 lines in `buildMacosHomeOverlayFor`) | no |
| G8 | **podman/macOS reports its host-loopback disposition wrong in *both* directions** — **Measured 2026-09-16** | podman/macOS | On bridge, `YOLO_HOST_LOOPBACK` is always `unknown`, so the fatal reachability witness never escalates and a total loophole outage is silent — the exact shape of the four-day outage the subsystem exists to end. On `mode: host`, it is `shared`, which is false (the shared namespace is the VM's), so a launch can be refused for a boundary that was never crossed. **The measurement removes the excuse for the bridge half:** the Mac's loopback IS forwarded, by gvproxy, with no flag ([§5.1](#51-what-is-now-measured) row 1) — so `unknown` is not merely uninformative, it is PESSIMISTIC about a hop that works, and it switches off escalation on the one column that has it available. The fix is a disposition mapping, not plumbing: podman/macOS + bridge is a *forwarded* host, arrived at by a mechanism yolo did not have to ask for. The agent briefing tells the same lie twice — see the code note below. | small code, **no measurement left** | no |
| G9 | **`required_capabilities` is validated, exported, and read by nothing** | all four | A config declaring `["web_search"]` launches happily on a jail with nothing that provides it; the agent discovers the gap at its first API call. `config_ref.txt:1401-1404` is honest about this, so it is a trap only for someone who reads the key name. | medium (~150 lines + a census) | **yes** — nobody has settled what *satisfies* a capability ([`OQ-CAP2`](../design/agent-auth-modes.md#12-decision-ledger)) |
| G10 | **The `macos-user` declaration-silence set: keys accepted, dropped, and never mentioned** | macos-user/macOS | `mounts`, `network.mode`, `perf_logging`, `programs.autoprune`, `loopholes.<name>.jail_env`, `required_capabilities`, `YOLO_STORE_PACKAGES`, the inherited user scope, `workspace_readonly`'s implicit `yolo-jail.jsonc` lock, and `MISE_ENV`'s whole `mise.jail.toml` mechanism all vanish without a line. DP-D15 already **ruled** the answer (fatal refusal keyed on the declaration being present) and it is not built. | small each; medium as one sweep | no — the ruling exists |
| G11 | **Apple Container is invisible to yolo's own lifecycle commands** — **Measured 2026-09-16** | container/macOS | `yolo stop` says "No jail running" while it runs, and exits 0 — so every message prescribing `yolo stop` is unactionable. `yolo prune` reports "none" affirmatively with stopped containers present. Orphans are never reaped. The attach-skew warning and the broken-prefix post-mortem can never fire. **The gate is lifted:** `container inspect`'s payload is measured ([§5.1](#51-what-is-now-measured) row 5) and carries everything the reader needs — `status.state`, `status.startedDate`, `configuration.mounts` as a real array, and a `labels` dict yolo can key on. The strict `mountsArrayFrom` guard is now a *checkable* default rather than a hedge against an unknown shape. | small–medium (one AC inspect/ls JSON reader; several early returns deleted) | no |
| G12 | **`macos-user` enforces no `resources` limit at all** | macos-user/macOS | An agent build can take the whole machine; a fork bomb is unbounded. All three sub-keys were `ruled-wontfix` and all three were overturned. | medium (300-450 lines for the memory watchdog; ~50 for the cooperative env) | **yes** — advisory-vs-enforced and the kill policy |
| G13 | **`macos-user` reads neither port key** | macos-user/macOS | A declared remap (`"9090:8080"`) does nothing, and a host service does not answer at the sandbox's `localhost:<port>`. Both were `impossible`; both fell to a launcher-side loopback proxy. **Measured 2026-09-16:** the optional confinement half is expressible — SBPL `network-bind` has PORT granularity ([§5.1](#51-what-is-now-measured) row 13), which was the one thing no reading could settle. | small (~60-250 lines) | no |
| G14 | **The agent can rewrite its own skills and its own briefing** | macos-user/macOS, container/macOS | On `macos-user` both are writable copies; on AC the briefing is a 0644 file in the writable home, and below `acROBindsFloor` the *skills* bind lands writable onto the launcher's own staging dir. Nothing prints. An agent that edits its own instructions between launches is the failure the `:ro` bind exists to prevent. | small (~150 lines incl. tests) | no |
| G15 | **The platform-inert loophole report reads the manifest default instead of the merged config, and `host-processes` declares no platform at all** | podman/macOS (+ macos-user for the `env` half) | A user who *enables* a Linux-only loophole on a Mac gets a clean launch and no line. Worse, `host-processes` has no `platforms` key, so on macOS the daemon **starts**, the front publishes, the witness passes, and every `yolo-ps` call fails on GNU-procps argv. And `audio`'s pack `env` half still crosses, so `PULSE_SERVER` names a socket that does not exist. | small (one line in the manifest; one resolver fix) | no |
| G16 | **`copilot` and `omp` logins never persist across workspaces, because their manifests never asked** | all four | Every new workspace demands a fresh `copilot` login. The mechanism (`scope: machine` state + a `shared_credentials` hook) is fully built and simply not declared. | small (manifest lines, no Go) | no — but a **fact-finding** blocker: the hook symlinks a single *file* |
| G17 | **A fresh `/login` in one jail can be discarded, and on two backends nothing serializes the refresh at all** | all four | The loser's credential file is `os.Remove`d rather than renamed aside, so a just-completed login is unrecoverable. On `macos-user` and AC no broker runs, so two concurrent sessions can race and burn the single-use refresh token; the launch says the loophole is inert and never names the cost. | small (~10 lines for preserve-the-loser) / medium (start the singleton on the `macos-user` arm) | no |
| G18 | **`providers` has no workspace-scope containment** | all four | A repo-committed, agent-editable `yolo-jail.jsonc` can rewrite the `base_url` of a provider a user-scope profile selects — i.e. point the agent's API traffic at an endpoint of the repo's choosing. Nothing warns; the disclosure line reads identically. This is exactly what [`OQ-CS5`](../reference/providers.md#why-its-this-way)'s refusal text says a committed file must not do, applied to the sibling key that carries the address. | small (~15 lines, mirroring `validateProfiles`) | no |
| G19 | **The `macos-user` bootstrap env is a hand-maintained two-name wire, and four things fall off it** | macos-user/macOS | `YOLO_PROFILES` is missing, so every pack *config surface* renders as if no profile were selected while the agent's own env is correct. `env_sources` do not reach the bootstrap, so `mcp_servers.requires_env` deletes servers whose variable the agent **will** have. `YOLO_PACK_ROOT` is bootstrap-only, so in-sandbox `yolo programs ls` says "run it there" to someone who is there. `YOLO_VERSION` is unset, so `config.InJail()` is false and `yolo host apply` inside the sandbox is not refused — it renders into the shared sandbox home while every message says "your real home". | small (each is 1-15 lines) | no |
| G20 | **`macos-user` has no host-side observability at all** | macos-user/macOS | No `boot.log` (so a scrolled-away provisioning failure is undiagnosable — the exact case the log exists for), no `--timing` table (so the up-to-30-minute nix build, this backend's entire cost, is unmeasured), no housekeeping slot (nothing is ever reclaimed or offered), no `config-boot.json` (so `yolo config drift` answers "cannot determine" forever), no E3 config capture, and `yolo ps` prints a red "Could not query the macos-user runtime" on a healthy machine. | small each (~5-30 lines) | no |
| G21 | **Nix inside the jail: four independent breaks, one of them three lines** | podman/Linux, podman/macOS, container/macOS, macos-user | `nix build` in a jail is refused for `experimental-features` (measured), because the image bakes no `nix.conf` — three lines, the precedent is already in `flake.nix:1717-1721` for the builder image. On AC there is no store mount and no notice. An in-jail build's output gets **no GC root**, so a host `nix-collect-garbage` can delete the store path a running jail is executing from, silently. On `macos-user`, the one backend that *requires* a host nix for every launch is the one whose sandbox cannot see it (`nix: command not found`). A refuter measured a fully working in-jail nix with two podman volumes + that same 3-line `nix.conf`. **Measured 2026-09-16, and the macos-user arm is smaller than it looked:** inside the shipped profile shape, `connect(2)` to the daemon socket survives the write-deny, `nix build nixpkgs#hello` returns RC=0, and an indirect gcroot IS created because the daemon writes it as root ([§5.1](#51-what-is-now-measured) rows 10-12). Nothing about confinement blocks it — the whole macos-user break is that `nix` is not on the sandbox's PATH. | small (each break) / medium (store lifecycle) | **yes** — where the in-jail store lives, and who reaps it |
| G22 | **Store-delivered packages and the lean image's extras are refused on both Mac container backends** | podman/macOS, container/macOS | Every distinct `packages:` list costs a full Linux-builder-offloaded image build — the cost C4/C5 exists to remove, absent exactly where it is largest. All four cells were hard claims; all four fell. | small (podman/macOS: ~4 lines deleted, 2 added) / medium (AC: 200-400 lines) | **yes** — the whole-store-bind hazard and the additive fallback |
| G23 | **A `packages:` entry that cannot be built fails as a raw nix trace, three layers from the config line** | podman/Linux, podman/macOS, container/macOS | A typo names its attribute (workable); an unfree or platform-unsupported package surfaces as a check-meta trace from inside `buildEnv`. On a Mac it is worse: a bad package and a missing Linux builder produce failures in the same place, so each reads as the other. | medium-small (~2-3 days) | no |
| G24 | **`prune` is accepted with no validator, is undocumented, and the coverage test that should have caught it passes vacuously** | all four | `prune: {"warn_threshold": 40}` is accepted, does nothing, and cannot be looked up — `config-ref` has no `prune` section. `TestConfigRefDocumentsEveryLiveKey` does `strings.Contains(ref, key)` and "prune" is a substring of "autoprune", so the check is satisfied by a mention of a *different key*. | small (~20 lines + a doc section + the test tightening) | no |
| G25 | **`--network <mode>` is not an override** | all four | `yolo --network bridge` against a workspace whose config says `"mode": "host"` silently launches host-networked, contradicting `yolo run --help`. `resolveNetMode` lets the config win because the flag's default and an explicit flag are indistinguishable. | small (~10 lines, but every `NewDefaultOptions` caller must be checked) | no |
| G26 | **`macos-user` `host_files`: directory sources are dropped, and a home-root destination is shared across every workspace on the machine** | macos-user/macOS | A directory-shaped `host_files` entry (a host nvim config) is warned and never delivered. And a destination at the home *root* (`~/.npmrc`, `~/.netrc`) lands in the single `_yolojail` account home, so workspace A's launch overwrites workspace B's file, silently. | small (~30-50 lines each) | no — the real fix is the already-open per-workspace home design |
| G27 | **Apple Container's dropped keys: `pids_limit`, `ephemeral_storage`, the DNAT fixup, captures materialize, pack `mount` — and `network.ports` itself, which was believed to work** | container/macOS | A fork bomb is unbounded with nothing printed. `"ephemeral_storage": "volume"` silently gets RAM-backed scratch and can OOM under exactly the workload the key exists to move off RAM. `yolo capture claude` succeeds and every launch still downloads. A pack `mount` is **disclosed** in the banner and never arrives. **Measured 2026-09-16, and this row grew:** `network.ports` does not work here **at all**, not merely for a loopback-bound service — AC records the mapping and the published address carries no data, while the container's own IP does ([§5.1](#51-what-is-now-measured) row 6). So the DNAT sub-row is moot on this backend (there is nothing to fix up) and the KEY is the drop. Every doc that says AC publishes ports needs correcting, not just annotating. | small each — captures materialize is a **five-line** predicate swap | no for the drops; **yes** for `network.ports`, which now needs a ruling like G34's |
| G28 | **podman/Linux's own small defects** | podman/Linux | `gpu.mode: cdi` for AMD passes a probe that never checks for a CDI spec, so the launch dies on a raw runtime error instead of yolo's warn-and-skip (measured: `unresolvable CDI devices amd.com/gpu=all`). `yolo check` prints "Stop N orphaned jail(s)? [y/N]", reads a `Stdin` nothing assigns, always proceeds as N, then tells you to run the command that just declined. A nested launch drops both port keys and says "NOT applied" where the truth is "already reachable at localhost:H". | tiny each (10 lines, 1 line, 40-70 lines) | no |
| G29 | **`confinement: "guest"` is refused on all four setups** | all four | rc 1, including on a re-entry into a running jail. The refusal is right (the briefing's guest prose would be false of what launched), but the notch is the one a `macos-user` user is most likely to think they already have. | large (Linux: bwrap+Landlock, a whole backend arm) / medium (macOS: mostly a home-tier decision on a backend that ships) | **yes** — exists, [`handoff-guest-notch-macos.md`](./handoff-guest-notch-macos.md) |
| G30 | **`macos-user`'s posture inversions: the confinement keys are advisory and the userland surprise is unannounced** | macos-user/macOS | `host-processes` exists to show *nothing* by default plus an opt-in allowlist; here the sandbox runs the host's own `ps` under `(allow default)` and sees every process on the machine including other users' command lines, gated by no config key. `macos_log: "off"` is likewise advisory — the agent can exec `/usr/bin/log` directly. And [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger)'s no-GNU-userland ruling is invisible: `sed -i`, `find -printf`, `grep -P` and `tar --wildcards` all fail here and work on every container backend, with nothing at launch or in the briefing to predict it. | small (a `(deny process-info*)` + wrapper; one briefing sentence) | **yes** — is the allowlist a boundary or a convenience? |
| G31 | **Device and GPU keys on macOS: three cells that were `impossible` and are not** | macos-user, podman/macOS | `devices` on `macos-user` can at minimum carve the declared node out of yolo's own SBPL deny. `gpu` on podman/macOS: libkrun/krunkit exposes virtio-gpu with Venus, so `--device /dev/dri` gets Vulkan **compute** via MoltenVK — but `deviceArgs` refuses every entry by host OS before any path check, and `validate.go` accepts only `nvidia`/`amd`, so the one GPU an Apple silicon Mac has cannot be named. `kvm` on podman/macOS probes the **Mac** for `/dev/kvm` when the device would live in the VM. | small each (+ a real-Mac verification) | no |
| G32 | **The host user-level skills tree does not exist, and AGENTS.md still documents it** | all four | A skill in `~/.claude/skills` on the host does not reach the jail. `SkillTarget.HostSource` was removed by S3 because it was set to the *destination*; the middle term of "built-in < host user-level < workspace" is gone. Two routes already reach the outcome (the conventional local pack; a filtered `packs` entry pointing at `~/.claude`), and **no OQ, ledger row or comment rules the literal path out on purpose**. | zero (document route 1) / small (~tens of lines for route 2's robustness fix) | no — but fix the AGENTS.md sentence either way |
| G33 | **An inline `loopholes.<name>` record gets no container-side plumbing** | all four | No bind, device, intercept, CA or `jail_env`. Currently harmless because the inline key census cannot *express* any of it — an attempt is an unknown-key error, so the filter and the census agree. A user reaches the full outcome today with zero code by declaring the loophole in the conventional local pack at `~/.config/yolo-jail/local`. | zero (workaround) / small-medium (~200-350 lines for the inline spelling) | no |
| G34 | **`forward_host_ports` on Apple Container emits an inverted `--publish-socket` for a socket AC then refuses, so the container never starts** — **Measured 2026-09-16** | container/macOS | Ranks with G2 by impact: this is a **launch-breaking** row, the only one in the table. Two independent faults, both measured ([§5.1](#51-what-is-now-measured) row 8). (1) `run.go:1137` starts host-side socat **before** the container and waits for its socket file; AC then refuses the flag naming that path — `Error: host socket <path> already exists and may be in use` — so a config with `forward_host_ports` cannot create a container at all, and the error names a socket the user never wrote. (2) Even with no socat (not installed → no socket), the DIRECTION is wrong: AC's `--publish-socket host_path:container_path` creates `host_path` and forwards a HOST connection inward to a container-side listener, while the key needs jail→host, so the jail's connect to `/tmp/yolo-fwd/port-<n>.sock` reaches nothing. Note what this buys: a unix socket is the ONE transport measured to cross this boundary (TCP crosses in neither direction, rows 6-7), so the mechanism for G6 is *here*, inverted — a host-initiated relay into a jail-side listener. | small to make it honest (drop the flag, warn, ~20 lines) / medium for the host-initiated relay | **yes** — the relay inverts the loophole model, which is a ruling not a patch |

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
granularity is **measured and present** ([§5.1](#51-what-is-now-measured) row 13).

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

## 5. Measured cells, and what is still unmeasured

The audit shipped with **40 `measured`, 11 `inferred` and 654 `read-the-code` cells, and 22 classified `unknown`** —
meaning no amount of further reading settles them. **On 2026-09-16 a measurement pass on a real Mac settled 15 of
the 22**, so the classification table in [§1](#1-what-this-is) is PRE-MEASUREMENT and this section is the current
answer where the two disagree.

The instrument was one machine holding all three macOS setups at once: macOS 25.5 (Darwin 25.5.0, arm64), podman
6.0.2 on an `applehv` machine (inside the VM: `rootless: true | netavark | pasta | cgroups v2`), `container`
1.1.0, a running nix daemon, and `sandbox-exec`. Two probe rules earned their keep and belong in any re-run:
**read bytes, never just connect** ([§5.1](#51-what-is-now-measured) row 1 was a false `CONNECT` on the first attempt — the exact class
[loopholeinert.go](../../internal/cli/run/loopholeinert.go)'s reason describes), and **name the direction**, because
the one AC mechanism that carries data carries it the other way ([§5.1](#51-what-is-now-measured) row 8).

### 5.1. What is now measured

| # | Question | Answer, measured 2026-09-16 |
|---|---|---|
| 1 | Can a podman/macOS jail reach a host service on the Mac's own `127.0.0.1`? | **YES, and data crosses both ways.** `/etc/hosts` carries `192.168.127.254 host.containers.internal`; the Mac-side listener accepted from `('127.0.0.1', 64269)` and both banners arrived. Default bridge, **no `--net` flag and no `--map-host-loopback`** — gvproxy forwards it. Settles `claude-oauth-broker` (`default_enabled: true`), `openai-auth-broker`, `serial` and every inline config-declared loophole: the hop works. |
| 2 | Is a `127.0.0.1`-bound jail service publishable on podman/macOS — does the DNAT fixup work across the VM hop? | **NO for the fixup; the doc claims are RIGHT.** With `route_localnet=1`, `--cap-add NET_ADMIN` and the exact `PREROUTING … -j DNAT --to-destination 127.0.0.1:9000` rule installed and a listener confirmed on `127.0.0.1:9000`, the Mac's dial connects and receives **nothing**; the `0.0.0.0` control returns its banner. So `config-ref` and the agent briefing are correct and G23's "bind `0.0.0.0`" advice stands. **Hypothesis worth one CI run:** rootless port forwarding is a userspace splice *inside* the netns, so it never traverses `PREROUTING` — which would make the fixup inert on every rootless podman, including podman/Linux (the reference setup's own remaining `unknown`). |
| 3 | Who owns files the agent writes in a podman/macOS workspace bind? | **Your own uid.** In-jail `stat` says `0 0`; on the Mac the same file is `501 20`. No VM `core` uid, no unfamiliar numeric owner. |
| 4 | Does `network.ports` publish on podman/macOS? | **YES for a `0.0.0.0`-bound service** (banner returned through `-p 18096:9000`). Only the loopback-bound half fails, which is row 2. |
| 5 | Apple Container: the shape of `container inspect`'s payload, which gates G11 wholesale | **Measured.** A top-level ARRAY of objects, each with `id`, `status.state`, `status.startedDate`, `status.networks[].ipv4Address`/`ipv4Gateway`, and under `configuration`: `mounts` (a real array — so the strict `mountsArrayFrom` guard is the right default *and* now a checkable one), `labels` (a dict, so a yolo label is available for discovery), `publishedPorts`, `publishedSockets`, `resources`, `initProcess.environment`. `container list --all --format json` is the companion surface. |
| 6 | Apple Container: does `network.ports` publish at the address yolo believes? | **NO.** `container inspect` records `{"hostAddress": "0.0.0.0", "hostPort": 18097, "containerPort": 9000}` and dialling the Mac's `127.0.0.1:18097` **connects and carries nothing** (the container-side `socat` logged `Connection reset by peer`, so the connection reaches the jail and the data does not cross). Dialling the container's own `192.168.64.23:9000` returns the banner. The reachable address is the container IP; the published one is inert. |
| 7 | Apple Container: is the container→host outage still real on 1.1.0? | **YES — re-verified, and now with bytes.** The host *accepts* (peer is the container's IP `192.168.64.3`), then `sendall` raises `BrokenPipeError` and the client reads nothing. `backendInertReason`'s expiry check has been run: the reason still holds on 1.1.0 / macOS 25.5. Together with row 6 the finding generalises — **on AC, host↔container TCP establishes in both directions and carries data in neither.** |
| 8 | Apple Container: does `--publish-socket` work? | **YES, it carries data both ways — and its direction is host→container**, which is the opposite of the use yolo puts it to. AC *creates* the host socket (`srwxr-xr-x`) and forwards a Mac process's connection to a **container-side listener**: `HOST_READ b'JAIL_SOCK_HELLO\n'`, `JAIL_GOT b'HOST_HELLO\n'`. So a unix socket is the one transport that crosses this boundary, and `forward_host_ports` is wired backwards through it — see G34. |
| 9 | Apple Container: does headless chromium launch and serve CDP? | **YES.** In the `yolo-jail` image under AC, `chromium --headless --no-sandbox --disable-gpu --remote-debugging-port=9222` answered `/json/version` with `Chrome/152.0.7977.82` **after 1s**. The predicted shared-memory death did not occur. |
| 10 | macos-user: does `connect(2)` to `/nix/var/nix/daemon-socket/socket` survive `(deny file-write* (subpath "/"))`? | **YES.** `CONNECT ok` under the shipped profile shape, while `touch /nix/var/nix/gcroots/auto/…` under the same profile is `Operation not permitted` — so the deny is live and unix-socket connect is simply not governed by it. |
| 11 | macos-user: does an in-sandbox `nix build` therefore work? | **YES, RC=0.** `nix build nixpkgs#hello` inside `sandbox-exec`, substituted from cache.nixos.org. G21's macos-user arm is a **delivery** problem (`nix` is not on the sandbox's PATH), not a confinement one. |
| 12 | macos-user: is an indirect gcroot into `/nix/var/nix/gcroots/auto` denied? | **NO — it is created.** `gcroots/auto/jfp33… -> /tmp/…/ws/result` appeared for a sandboxed `--out-link`, because the DAEMON does that write as root. The sandbox's own write-deny is irrelevant to it. |
| 13 | Does SBPL `network-bind` have port granularity (G13's confinement half)? | **YES.** `(deny network-bind (local ip "*:8000"))` blocks 8000 (`PermissionError`) and permits 8001; a bare `(deny network-bind)` blocks both. The confinement upgrade G13 proposes is expressible. |
| 14 | Does `claude` start with an unwritable `~/.claude/skills` on darwin (G14)? | **YES.** With the deny confirmed live (`touch` → `Operation not permitted`), `claude --version` prints `2.1.269` and `claude mcp list` answers normally. Bounded honestly: neither exercises a session that *writes* skills, but the "it refuses to start" worry is refuted. |

### 5.2. Still unmeasured

Seven of the 22 `unknown` cells stand, and all seven need something this pass could not reach — a **real yolo
launch** rather than a bare container, an interactive TTY, or a rootless Linux host.

**A real Mac running podman machine** (4 cells):

- Whether Window A's number is trustworthy: the die/cleanup timestamps come from inside the VM while
  `podmanExited` is a host-side mark, so VM/host clock skew lands directly in the reported gap.
- Whether a vendor-installer capture share is present (an absent share and an empty store are the same observation).
- `cache_relocations` onto an unshared volume — expected to fail the *launch* rather than no-op silently, which
  is the safe direction, unconfirmed.
- Whether the in-jail `iptables` DNAT fixup can be written at all under Apple Container (the rule was installed
  fine under podman/macOS in row 2, so this is now AC-specific).

**A real Mac running Apple Container ≥ 1.1.0** (2 cells):

- What `^Z` does — nobody has pressed it in an AC jail and written down the answer; the doc explicitly refuses to
  let podman's behaviour be inherited. Needs an interactive TTY.
- Whether AC exec succeeds against a container wedged mid-provision — the reason to prefer the host-side
  log-freshness detector, which cannot hang. Exec against a *healthy* container works (measured), which is the
  easy half and not the one that matters.

**A real rootless Linux host, or CI** (1 cell, plus row 2's hypothesis):

- Whether the DNAT fixup works in a **non-nested** podman jail. Row 2's mechanism predicts it does not on a
  ROOTLESS host and does on a rootful one, which would make this a two-row answer rather than one. Report
  `podman info --format '{{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}'` with whatever comes back.

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
2. **DONE for the reachable half, on 2026-09-16 — read [§5.1](#51-what-is-now-measured) before re-reading any
   macOS cell.** The instruction was to close the measurement gap in the podman/macOS column before auditing it
   again, because a second reading pass over 141 cells with zero measured facts only produces a second set of
   `read-the-code` cells. One Mac holding all three macOS setups settled 15 of the 22 unknowns in under an hour of
   probes, and the cheapest of them (`/dev/tcp` against a Mac loopback listener) overturned the column's whole
   premise. Two lessons for the next pass: the first attempt at that very probe returned a FALSE `CONNECT`
   because it never read a byte, and the AC finding that matters most was found by asking which DIRECTION a
   mechanism runs, not whether it works. What remains needs a real `yolo` launch, a TTY, or rootless
   Linux — [§5.2](#52-still-unmeasured) names which for each.
3. **Re-verify every `file:line` in this document against the tree before citing it downstream.** The audit found
   more than fifty false doc claims as a by-product — including one this document repeats as a finding
   (`yolo-cglimit`'s non-existent nice/ulimit fallback, asserted in both `config_ref.txt:1608` and
   `boot.go:242`) and one about AGENTS.md's own PATH-order claim. Drift clusters at status lines and at exactly
   the numbers a reader stops checking. The re-runnable sweeps and their allowlists are in
   [docs/plans/README.md](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).
---

## 7. Found while fixing the docs (2026-09-16)

Fourteen agents were sent to fix the false claims this audit produced, one owner per file, each told
to verify before editing. What they hit on the way is listed here, because a false doc claim is
sometimes the visible end of a live defect. Nothing below is fixed; each is small and each has a
named site.

| # | Defect | Where | Why it matters |
|---|---|---|---|
| F1 | **`macos_log` is unreachable, and the code tells you to enable it.** The feature is wired end to end, but the key is not in `knownTopLevelConfigKeys`, and an unknown key is fatal — measured `[FAIL] config.macos_log: unknown key`. `internal/macosuser/macosuser.go` prints *"Enable it by setting `macos_log`: `user` … then restart"*: following yolo's own instruction refuses the next launch. | `internal/config/config.go`, `internal/macosuser/macosuser.go` | The one defect here that actively instructs a user into a broken state. Fix is the key, a `config-ref` entry and a test that a `macos_log` config launches. |
| F2 | **The openai-auth broker spawns on `macos-user` with no host-exec disclosure.** `notePackHostExec` has one call site, inside `startLoopholesDisclosed`, which runs below the macos-user arm's return; that arm reaches the daemon through `startLoopholesMatching` instead. So pack code runs on the user's real machine and *"This launch runs pack code on your machine"* never prints. The guard test greps for the literal `o.startLoopholes(` only, so the sibling bypasses it. | `internal/cli/run/run.go`, `internal/cli/run/packloopholes.go`, `internal/cli/run/packhostdisclosure_test.go` | AGENTS.md: the read/exec banners **are** the trust boundary. Also the exact callee-pinned/call-site-unpinned shape the repo names. The fix needs an openai-auth-scoped variant, mirroring `withoutOpenAIAuthPack`. |
| F3 | **Apple Container promises a broker endpoint nothing can write.** The `container` branch emits `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT` whenever the broker loophole is active, but the broker singleton is only ensured off that runtime and AC's allow-list admits `openai-auth-broker` alone. | `internal/cli/run/assemble_parts.go`, `internal/cli/run/run.go` | On an AC jail with `packs: ["claude", "codex"]` the in-jail terminator dials a file that never appears — and with the fatal reachability witness, possibly a refused launch. Same unbackable-promise shape `brokerEndpointIsUnpublishable` exists for, one axis over. |
| F4 | **A nested GPU launch is neither supported nor prevented.** `podmanNestingArgs` tests `inContainer` first and returns, so the GPU branch's `--runtime runc` and identity uid/gid maps are never emitted — while `gpuArgs` still puts the CDI device flags on the argv. | `internal/cli/run/assemble_parts.go`, `internal/cli/run/helpers.go` | The user guide says it is not available; nothing enforces that, so the failure is a runtime error rather than yolo's warn-and-skip. |
| F5 | **`yolo check`'s orphan-cleanup prompt asks a question nothing can answer.** It reads a `Stdin` that no caller assigns, so it always proceeds as if the answer were N — then prints the command to run, which is the one it just declined to run. | `internal/cli/check/helpers.go`, `internal/cli/commands.go` | A prompt that cannot be answered is the class the config-change gate already rules on: never prompt where a prompt cannot land. |
| F6 | **`yolo check` preflights the wrong image on every store-packages host.** `BuildOCIImage` builds the default attr unconditionally, so on a launch that would realize `.#ociImageLean` + `.#yoloImageExtras`, check proves neither. | `internal/cli/check/builder.go`, `internal/image/build.go` | Structurally green on the wrong artifact — the Image section cannot fail for the hosts it matters most to. |
| F7 | **Loopback-TLS is silently broken on rootful podman + netavark.** Measured from this jail: a host listener on `0.0.0.0` is reachable through the bridge gateway and one on `127.0.0.1` is not, and `svcendpoint` binds `127.0.0.1` unconditionally. The probe ladder has no rung for rootful, so the disposition is `unknown` and the fatal witness never fires. | `internal/cli/run/hostloopback.go`, `internal/svcendpoint/listen.go` | Every jail-facing service is down on such a host with nothing printed — the four-day-outage shape the subsystem exists to end. **Wants a ruling, not a patch:** an `unsupported`-style disposition plus a warning is the cheap honest option. |
| F8 | **A pack `program` cannot declare which platforms its vendor publishes for.** `packdecl.Install` has no `Platforms` field — only the `service` kind carries one. So an arm64 Linux jail selecting `packs: ["omp"]` runs the install and gets the vendor's own refusal (`oh-omp: unsupported platform linux-arm64. Supported: darwin-arm64, linux-x64`) instead of yolo declining with a reason, at the moment the agent is first used rather than at launch. | `internal/packdecl/packdecl.go` (`Install`), `packs/omp/pack.json` | Found by CI: `install (ubuntu-24.04-arm, omp)` red while `ubuntu-latest` was green. The test now carries the vendor fact per row and skips that arch; the product still cannot express it. The `service` kind's `platforms` is the precedent to copy, and the launch already has a place to say so. |

Two stale comments worth a sweep rather than a row: `internal/svcendpoint/preamble.go` still opens
*"NOTHING CALLS ANY OF THIS YET"* while `listenWith` and `ServeFrontWithOptions` consume it today, and
`internal/cli/run/backendcaps.go`'s parity marker for config `mounts` on macos-user reads `Warned`
where nothing warns — it should read `Dropped`, or the warning should be built.

**One defect found this way is already fixed** (`85517b97`, and it is why this section exists): the
agent auth prelaunch began an interactive OpenAI browser login with no terminal to answer it, so
`codex --version` printed an auth URL and blocked until the job deadline killed it — five CI jobs on
both arches, ~15 minutes each. It now says what is missing and lets the command run, since a
`--version` needs no credential. The regression test runs the real generated shell and fails when the
guard is deleted.
