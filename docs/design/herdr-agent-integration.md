---
title: "yolo delivers herdr's per-agent integration itself: one pack, vendored at a pinned herdr version, rendered the same on every notch"
date: 2026-11-26
status: in-review
stage: DESIGN
next: "Rule OQ-HR5 (may a jail have a report-only herdr channel) and OQ-HR9 (may yolo replace a provably pristine older herdr file); slice 1 (the pi extension on the host) needs neither"
tags: [design, herdr, packs, extensions, hooks, host-management, notches, pi]
summary: "herdr wants an extension installed in each agent it supervises. yolo can deliver all of it with no help from herdr: a shipped `herdr` pack vendors herdr's integration files at a pinned release, byte for byte (measured identical to what herdr's own installer writes), and renders them through the `files` and `config-list` kinds that already exist, on the host under `host_management = own` and in jails alike. Because the bytes and the config entries equal herdr's, herdr's installer re-run is a no-op and there is one copy, never two writers fighting. `yolo check` asks herdr itself whether our files are current. A jail's copy is inert until a ruling gives it a channel."
vantage:
  status-chip: true
---

# yolo delivers herdr's per-agent integration itself: one pack, vendored at a pinned herdr version, rendered the same on every notch

**Status:** 2026-11-26. Nothing built by this doc. Evidence: herdr `v0.9.5` (the latest release: the
public manifest [`herdr.dev/latest.json`](https://herdr.dev/latest.json) names 0.9.5 on 2026-11-26, and
the `master` branch's `Cargo.toml` also says 0.9.5), read from source at that tag and **run**: the
`herdr 0.9.5` binary from nixpkgs, headless, in scratch home directories. Those runs are marked
MEASURED. yolo evidence was read at `3f958b85c`. No agent CLI was started. This doc takes over
[OQ-HR5](#OQ-HR5) to [OQ-HR7](#OQ-HR7) from
[`herdr-integration.md` §8.6](../research/herdr-integration.md#86-questions-for-the-owner), asked here
under the owner's rulings of 2026-11-26.

> **In short.** herdr's integration is a handful of small files plus one config entry per agent,
> and every one of them can be shipped by a yolo pack that holds the exact bytes herdr's installer
> writes. Delivering the *same* bytes and the *same* entries, rather than an imitation, is what
> makes yolo and herdr's installer agree: the installer finds nothing to change.

**Why it matters.** herdr asks each user to install per-agent extensions, and under
`host_management = "own"` yolo already owns the files those installs edit, so herdr's installer is
a second writer ([research §3.6](../research/herdr-integration.md#36-two-writers-on-one-agent-config-file)).
The owner wants this "as seamless as possible", working with host management on, with no
dependency on herdr changing anything.

**The shape.** A pack named `herdr`. It holds herdr's files at a pinned tag, declares them as
ordinary `files` and `config-list` contributions to the agent packs' own surfaces, and ships a
`yolo check` row that lets herdr's own status command judge whether yolo's copy is current.
Notches differ only in what the channel can reach, never in what is rendered ([§6](#6-the-channel-notch-by-notch)).

**Cost.** One new pack, about 58 KB of vendored files, a refresh script, and four small core
capabilities that do not exist today ([§4.2](#42-what-the-kinds-cannot-say-today)). A pinned copy
lags herdr by the time between a herdr release and a yolo release, and [§3.3](#33-how-yolo-notices)
is how that is seen and what it costs.

**Start at [§3](#3-where-the-bytes-come-from)**, the ranked byte sources, then [§5](#5-one-copy-and-herdrs-installer) for the part the owner cares about most.

**Needs your ruling:** [OQ-HR5](#OQ-HR5), [OQ-HR6](#OQ-HR6), [OQ-HR7](#OQ-HR7), [OQ-HR9](#OQ-HR9).

**Reads with:**

- [`herdr-integration.md` §8](../research/herdr-integration.md#8-herdrs-agent-extensions-and-how-a-jail-could-carry-them)
  (what each integration reports; this doc supersedes its option 3 and its leanings on [OQ-HR5](../research/herdr-integration.md#OQ-HR5) to [OQ-HR8](../research/herdr-integration.md#OQ-HR8)).
- [`pack-system.md`](../reference/pack-system.md) (`files`, `config-list`, the host copy).
- [`config-ownership.md`](../reference/config-ownership.md) (`host_management`, capture and adoption).
- [`host-import-exclusions.md`](../reference/host-import-exclusions.md) (why its filter cannot serve here).
- [`agent-event-watchers.md`](agent-event-watchers.md) (its Claude `SessionStart` hook shares the list this doc appends to).

---

## 1. What the rulings settle

The owner's words, 2026-11-26:

1. *"herdr wants to install extensions to each agent and seems like we could give some help here. I want as seamless as possible."*
2. *"this herdr stuff also needs to work with host management on, so can't just import from the host locations that herdr writes. we need to sidestep that somehow."* ([HR-D10](../research/herdr-integration.md#HR-D10) in the research ledger)
3. *"I want to explore everything we can do ourselves without involving upstream."*

What follows, as principles this doc's decisions cite:

- **P1. yolo renders; yolo never imports.** The bytes come from a source yolo controls ([§3](#3-where-the-bytes-come-from)), never from what herdr's installer left in a host directory.
- **P2. One declaration, every notch.** The pack declares each file once. The host copy, the jail mount and the macos-user overlay are produced by the renderers that already exist, so no notch has code of its own for herdr.
- **P3. One copy.** What yolo writes is what herdr's installer would write, byte for byte, so the installer re-run is a no-op ([§5](#5-one-copy-and-herdrs-installer)).
- **P4. No upstream dependency.** Nothing here needs a herdr release, issue or change. The upstream asks of the research doc are [not pursued](#9-not-pursued-by-ruling).
- **P5. Every stop names its next step** ([`happy-path-principle.md`](../reference/happy-path-principle.md)).

**Non-goals.** Giving a jail a herdr handle (that is [OQ-HR5](#OQ-HR5), and nothing else here
opens one). Running herdr inside a jail. Integrations for agents yolo ships no program for (kimi,
droid, cursor and the rest). Changing the launcher's pane registration, which already works
([`herdragent.go`](../../internal/cli/run/herdragent.go)).

Terms. A **notch** is yolo's word for a confinement setting: a container jail, macos-user, or none
(`yolo host`). A **pin** is the herdr release whose files the pack holds. **Vendored** means the
pack's repository holds the files. A **harvest** *(coined here)* is running herdr's own installer
into a throwaway home and keeping what it wrote. A **channel** is the path by which an installed
extension or hook reaches herdr's socket. **Drift** is the pin falling behind the herdr the user runs.

## 2. What herdr integrates, measured

### 2.1 How the installer behaves

All MEASURED on 2026-11-26 with the 0.9.5 binary under `env -i HOME=<scratch>`, no server, no
network, exit 0 for every install below, unless the line says otherwise.

| Finding | Evidence |
| :--- | :--- |
| `herdr integration install <agent>` is a pure file edit. It needs no running server and no pane. | Ran it with a closed environment. Source: `integration` is dispatched in `cli::maybe_run` ([`cli.rs:131`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/cli.rs)), which `main` calls at [`main.rs:558`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/main.rs), well before it loads config. |
| It honors `HOME`, and for five agents a config-dir variable. | `HOME` through `home_dir` ([`env.rs:209`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/env.rs)); `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`, `PI_CODING_AGENT_DIR` and `ANTIGRAVITY_CLI_CONFIG_DIR` through `config_dir_from_env_or_home` (`env.rs:91`). opencode has `HOME` only (`env.rs:126`). `CLAUDE_CONFIG_DIR=<dir>` wrote under `<dir>`. |
| The files it writes are the files in herdr's source at that tag, and the same bytes sit inside the binary. | Nine files (pi, omp, claude, codex, copilot, agy, and opencode's three) from the installer compared equal to the tag's `src/integration/assets/` by `cmp`. They are `include_str!`ed ([`mod.rs:37-40, 180-186`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/mod.rs)). In the binary the claude asset occurs once, and the next asset's first byte follows its last with no delimiter. |
| Each file carries `HERDR_INTEGRATION_ID=` and `HERDR_INTEGRATION_VERSION=` in its header. `herdr integration status` is a `>=` compare of that marker with the version it expects. | [`registry.rs:444-462`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/registry.rs). With the marker edited from 11 to 10 it printed `claude: outdated (v10 < v11)`, and `status --outdated-only` printed `installed herdr integrations need updating; run herdr integration install claude.` |
| `herdr update` prints that notice after it replaces the binary. | [`update.rs:2336-2343`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/update.rs) runs `integration status --outdated-only` on the new binary. |
| The installer is idempotent by comparing the **command string** of the hook entry, which embeds the absolute hook path as `bash '<abs path>' session`. | [`config_edit.rs:73-86`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/config_edit.rs), [`command.rs:10-33`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/command.rs). MEASURED both ways: a `settings.json` already holding the canonical entry (the installer's own matcher, `timeout` 10 and quoting, with the real path, pretty-printed differently, plus a user key) came back **byte-identical** after the install. The same entry written as `bash ~/.claude/hooks/herdr-agent-state.sh session` came back with **two** `SessionStart` groups. |
| The Claude entry is `{"matcher":"^(startup|resume|clear|compact|fork)$","hooks":[{"type":"command","command":"bash '<abs>' session","timeout":10}]}`. | [`claude_settings.rs:19, 350-358`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/claude_settings.rs); the measured `settings.json` agrees. |
| The extensions are inert without herdr's pane variables. | pi: `if (!enabled()) return;` ([`herdr-agent-state.ts:18-23, 180-182`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/assets/pi/herdr-agent-state.ts)), `enabled()` needing `HERDR_ENV=1`, `HERDR_SOCKET_PATH` and `HERDR_PANE_ID`. claude: three `exit 0` guards ([`herdr-agent-state.sh:20-22`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/assets/claude/herdr-agent-state.sh)); it also needs `python3`. |

### 2.2 What each integration is, for the agents yolo ships

"Marker" is the integration version at v0.9.5. "Authority" means herdr takes the agent's reported
state over its screen reading
([`detect/mod.rs:330-339`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/detect/mod.rs)); only pi, omp
and opencode have it among these, and agy is listed as session-identity-only (`:341-349`). For every hook agent the report is a session id, and **herdr, not
the hook, builds the resume command from it** (`claude --resume <id>`, [`agent_resume.rs:199-209`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/agent_resume.rs))
when the report's source is the official string `herdr:<agent>` ([`agent_resume.rs:328-352`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/agent_resume.rs)).

| yolo agent | herdr name | Files written (relative to the agent's config dir) | Config edit | Marker | Reports |
| :--- | :--- | :--- | :--- | :--- | :--- |
| pi | `pi` | `extensions/herdr-agent-state.ts` (6,694 B) | none | 9 | Live state, authority; session path |
| omp (`oh-omp`) | `omp` | `extensions/herdr-omp-agent-state.ts` (12,746 B) | none | 10 | Live state, authority |
| claude | `claude` | `hooks/herdr-agent-state.sh` (3,070 B) | `settings.json` `hooks.SessionStart`, the entry above | 11 | Session id only |
| codex | `codex` | `herdr-agent-state.sh` (2,966 B) | `hooks.json` `SessionStart` group; `[features] hooks = true` in `config.toml` | 10 | Session id only |
| copilot | `copilot` | `hooks/herdr-agent-state.sh` (2,697 B) | `settings.json` `hooks.SessionStart`: `{"bash":"bash '<abs>'","timeoutSec":10,"type":"command"}` | 4 | Session id only |
| opencode | `opencode` | `plugins/herdr-agent-state.js` (6,520 B), `herdr-tui-session.js` (21,240 B), `herdr-opencode/tui.js` (219 B) | `tui.jsonc` `plugin: ["./herdr-tui-session.js"]`; `cli.json` `plugins: ["./herdr-opencode"]` | 13 | Live state, authority |
| agy | `agy` (`antigravity-cli` on the CLI) | `hooks/herdr-agent-state.sh` (1,971 B) | `hooks.json` key `herdr`: a `PreInvocation` handler | 4 | Session id only |

The config directories, from `env.rs`: `~/.pi/agent` (`extensions/` under it), `~/.omp/agent`
unless `PI_CODING_AGENT_DIR` or `PI_CONFIG_DIR` says otherwise, `~/.claude`, `~/.codex`,
`~/.copilot`, `~/.config/opencode`, `~/.gemini/config`. The nine files total 58,123 bytes.

Two differences from the research doc's table matter here. For codex, v0.9.5 no longer installs
turn hooks (its changelog: *"Herdr no longer installs Codex turn hooks … Run `herdr integration
install codex` to update"*), and its installer removes the turn-hook entries an older one wrote
([`targets.rs:160-190`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/targets.rs)), so an older pin would deliver entries herdr has since dropped. For copilot the entry is flat, a `bash` key with no `hooks` wrapper.

## 3. Where the bytes come from

### 3.1 The candidates, ranked

| Rank | Source | What it is | Verdict |
| :--- | :--- | :--- | :--- |
| 1 | **Vendored at a pinned tag** | The pack's repository holds the nine files at `v0.9.5`, a provenance file with the tag and each file's SHA-256, and herdr's Apache-2.0 license text. A script, not a hand, refreshes them: it runs herdr's installer into a scratch home and copies the result. | **Chosen.** Hermetic: renders offline, identical on every notch and in CI, reviewable in a diff. Bytes proven equal to the installer's ([§2.1](#21-how-the-installer-behaves)). Costs a yolo release per herdr integration bump. |
| 2 | **Harvest at render time** | When a pack tree is staged on the host, run the user's own `herdr integration install <agent>` into a scratch `HOME` and render what it wrote, rewriting the scratch prefix in the entry to the destination home. | **The fallback**, not built in slice 1. Never lags: it is whatever the installed herdr expects, so a user who upgraded herdr gets the new bytes with no yolo release. Costs a new core idea (a pack tree partly generated by running a host binary, which must be disclosed like the pack exec banners), a cache keyed by the herdr binary, and a path-rewrite of one JSON string. Its failure modes are in [§3.2](#32-what-breaks-on-a-new-herdr-release). |
| 3 | **Our own equivalents** that speak herdr's socket protocol | A yolo-written script or extension sending `pane.report_agent_session` and `pane.report_agent`. | **Only where herdr ships nothing**, which is the jail channel ([§6.2](#62-every-self-built-way-to-carry-it)) and a yolo-built resume report. For the host it loses: herdr grants state authority and builds resume plans only for the exact source strings `herdr:<agent>` ([`detect/mod.rs:330-339`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/detect/mod.rs), `agent_resume.rs:328-352`), so an imitation either claims herdr's name or gets screen reading and no resume; and a user who also runs herdr's installer then has two hooks. |
| 4 | **Cut the literals out of the binary** | Scan `herdr` for `HERDR_INTEGRATION_ID=` headers and split. | **Rejected.** It works (MEASURED: 21 `HERDR_INTEGRATION_ID=` headers in the 0.9.5 binary) but the literals are adjacent with no delimiter, so an end is only the next header; and it reads a binary's layout where the installer is a documented command. |
| 5 | **A git-sourced `files` tree** through the existing built-tree route | `files` with `source = git:…herdr@v0.9.5`. | **Rejected.** That route is for pi extension packages and builds in a capture jail ([`pi-extension-store-builds.md` §4.1](pi-extension-store-builds.md#41-the-declaration)). Its declaration names no sub-path, so it would fetch a whole Rust repository at launch to use 58 KB. |
| 6 | **A release artifact** | herdr's release assets. | **Not a source.** The manifest lists one file per OS and architecture, the binary itself (`herdr-linux-x86_64` and its siblings, MEASURED from `latest.json`). The `herdr` crate on crates.io has one version, `0.1.0`, published 2026-03-27 and pointing at a different repository path (MEASURED, `crates.io/api/v1/crates/herdr`), so there is no current source package to read either. |

Two further facts shape the choice. Four of the seven integrations above changed across the two patch releases from 0.9.3 to 0.9.5 (claude 10 to 11, codex 8 to
10, copilot 3 to 4, agy 3 to 4; read from `mod.rs` at both tags). A pin will lag. And herdr's
`status` check is `>=`, so a file *newer* than the pin is "current" to herdr and harmless to leave
([§5](#5-one-copy-and-herdrs-installer)).

### 3.2 What breaks on a new herdr release

| Event | Vendored pin (rank 1) | Harvest (rank 2) |
| :--- | :--- | :--- |
| herdr bumps an integration's version, bytes only | herdr marks yolo's files `outdated`, and prints the notice after `herdr update`. If the user obeys and runs the installer, the file becomes newer than the pin; yolo leaves it ([HAI-D3](#13-decision-ledger)). Until the pin is refreshed, the delivered bytes are older than herdr expects but still speak the protocol. | None: the new bytes are what is rendered. |
| herdr changes an entry's shape (matcher, timeout, quoting, key) | The entry no longer equals herdr's, so the installer adds a second one (MEASURED for a differing command). The `yolo check` probe sees this ([§3.3](#33-how-yolo-notices)). | The entry is read from the installer's own output, so it follows. |
| herdr moves or renames an asset or a directory | The pin's destination is wrong and the probe's install writes elsewhere, so the probe reports a difference. | The destination is read from where the installer wrote. |
| herdr changes the installer's CLI or its `HOME` handling | No effect on a launch; the **probe** becomes inconclusive and says so. | The harvest fails; it falls back to the pin and says so. |
| herdr changes the socket protocol (`protocol` is 22 in `latest.json`) | Old extensions may stop working, and they fail silently by design (every guard is `exit 0`). Neither source detects this. Only a real herdr pane does, so it is on the [unmeasured list](#11-what-is-unmeasured). | Same. |

### 3.3 How yolo notices

Three instruments, each cheap and none on the launch path.

1. **The install-over-ours probe, in `yolo check`.** Render the pack's herdr files and entries
   into a scratch home, run the user's `herdr integration install <agent>` over it, and compare
   the tree before and after. herdr itself is the judge: any difference means yolo's copy is not
   what herdr would write, whether the cause is bytes, a marker, an entry shape or a path. It also
   runs `herdr integration status` over the scratch tree and reports `current`, `outdated` or
   `needs repair` per agent in herdr's own words. The probe runs a host binary the user already
   has, from a verb the user ran, in a directory it removes; the row says so
   (*"runs `herdr integration install` in a throwaway home"*). Inside a jail `herdr` is not on
   the path, and the row says that, naming the host as the place to run it.
2. **The finding text** names the next step ([P5](#1-what-the-rulings-settle)):
   - *behind:* `herdr 0.9.8 expects the claude hook at v12; yolo's pin is v11. yolo left the files herdr wrote. The pin refreshes with yolo's next release: run yolo update.`
   - *different entry:* `herdr's installer would add a second SessionStart entry for claude, so yolo's entry differs from herdr's. Nothing was changed. This is a yolo bug: the pin and herdr 0.9.8 disagree on the entry's shape; run yolo update.`
   - *inconclusive:* `could not run herdr's installer probe (<reason>); your files were not touched.`
3. **A scheduled, informational CI job** compares the pack's tag with `latest.json`'s version and,
   where a herdr binary is available (nixpkgs ships one; which version the flake pins is the plan's to check), runs the probe over the vendored
   files. It reports and never gates a landing or a release.

The offline test that does gate is a digest tripwire: the pack's files hash to the values in its
provenance file, and the refresh script is the only writer of both.

## 4. The herdr pack, as surfaces

### 4.1 One declaration per agent

Each row is a pack contribution that exists today, except where marked. Paths are the ones in
[§2.2](#22-what-each-integration-is-for-the-agents-yolo-ships), under whatever home the notch has.

| Agent | Bytes | Config | Surface exists? |
| :--- | :--- | :--- | :--- |
| pi | `files` at `.pi/agent/extensions/herdr-agent-state.ts` | none | `files` yes; gating on pi is [G3](#42-what-the-kinds-cannot-say-today) |
| omp | `files` at `.oh-omp/agent/extensions/herdr-omp-agent-state.ts` | none | as pi; yolo's omp home is `~/.oh-omp/agent` ([`packs/omp/pack.toml:9`](../../packs/omp/pack.toml)), so herdr's installer, which defaults to `~/.omp/agent`, writes a different path than yolo's omp reads. That `oh-omp` loads `extensions/` there is UNMEASURED |
| claude | `files` at `.claude/hooks/herdr-agent-state.sh` (exec bit) | `config-list` on `claude/settings`, `/hooks/SessionStart`, one entry | `claude/settings` yes ([`packs/claude/pack.toml:146`](../../packs/claude/pack.toml)); the entry needs an absolute path ([G1](#42-what-the-kinds-cannot-say-today)) |
| codex | `files` at `.codex/herdr-agent-state.sh` | a `hooks.json` entry; `[features] hooks = true` as a `config-overlay` on `codex/config` | `codex/config` yes; **no `hooks.json` surface** ([G5](#42-what-the-kinds-cannot-say-today)) |
| copilot | `files` at `.copilot/hooks/herdr-agent-state.sh` | `config-list` on `copilot/settings`, `/hooks/SessionStart` | surface yes ([`packs/copilot/pack.toml:77-80`](../../packs/copilot/pack.toml)); absolute path (G1) |
| opencode | three `files` | `config-list` on `opencode/tui`, `/plugin`; a `cli.json` entry | `opencode/tui` yes ([`packs/opencode/pack.toml:290`](../../packs/opencode/pack.toml)); **no `cli.json` surface** (G5) |
| agy | `files` at `.gemini/config/hooks/herdr-agent-state.sh` | a `hooks.json` block `herdr` | **no `hooks.json` surface**; yolo's agy surfaces sit under `~/.gemini/antigravity-cli/` (G5) |

No shipped pack declares a hooks key today: `rg -i hooks packs/claude packs/codex packs/copilot
packs/agy packs/opencode packs/omp packs/pi` finds nothing. The pi route has a ready alternative
that needs no gating, an addressed tree landing in `.pi/agent/yolo-packs/herdr/` and listed in pi's
`packages` ([`pack-system.md#pi-packages`](../reference/pack-system.md#pi-packages)). It is
**rejected for the common case**: a user who ran herdr's installer already has the file in
`extensions/`, which pi loads by itself, so pi would load both. That is [P3](#1-what-the-rulings-settle) broken, and unmeasured as harmless.

### 4.2 What the kinds cannot say today

Each is a capability, checked against the tree, with the smallest shape that closes it. Spelling
is the plan's.

| ID | The gap | Evidence it is missing | Needed by |
| :--- | :--- | :--- | :--- |
| **G1** | A per-notch **absolute home** in a `config-list` or `config-overlay` value, spelled so it equals herdr's `bash '<abs>' session` quoting exactly. herdr's idempotence is string equality ([§2.1](#21-how-the-installer-behaves)), so `~` does not do. | `${home}` exists only for the `instructions` kind ([`packdecl/instructions.go:135`](../../internal/packdecl/instructions.go)); config surfaces know `${workspace}` only ([`agentcfg/builtin.go:32`](../../internal/agentcfg/builtin.go)); the derive context has no home field. The token must give `/home/agent` in a jail, the sandbox home under macos-user and the real home on the host, and a test pins its quoting against the measured installer output on a home containing an apostrophe. | claude, copilot, codex, agy, opencode |
| **G2** | **Remove list entries by pattern**, not by whole-value equality, so the pack can drop herdr's host-path entry that a `readsHost` surface imported into a jail and put its own in place. | `config-list remove` deletes entries equal to a given value ([`pack-system.md`, config-list](../reference/pack-system.md#adding-entries-to-an-array-config-list)); the host path is the user's, so no literal serves. `host_import_exclusions` cannot, since it is refused under `own` ([`host-import-exclusions.md:66`](../reference/host-import-exclusions.md)) and drops every host hook, not herdr's. | claude in jails and under `own` |
| **G3** | A single-file `files` contribution **at an exact home path, live only while a named agent's pack is selected.** | A `files` contribution with `into` is ungated, so selecting `herdr` without pi would create `~/.pi/agent/extensions/` on the host, which herdr's own installer refuses to do (*"install pi first"*, `targets.rs:67-77`). The only owner-gated form is an addressed tree, which lands in the owner's slot subdirectory. | every `files` row |
| **G4** | **Adoption of an existing file** in the host copy. Today an occupied path yolo did not write is *"refused: exists and yolo has no record of writing it"* ([`hostfilestree.go:137`](../../internal/entrypoint/hostfilestree.go)), a stop with no next step. | Read at that line. Slice 1 needs only the identical-bytes half ([§5](#5-one-copy-and-herdrs-installer)); the rest is [OQ-HR9](#OQ-HR9). | host notch, every `files` row |
| **G5** | Three **agent-owned surfaces** that do not exist: codex `hooks.json`, agy `hooks.json`, opencode `cli.json`. `config-list` cannot create a surface. | `packs/codex/pack.toml:90-93` declares `config` and `model-list` only; agy's two surfaces are under `antigravity-cli/` (`packs/agy/pack.toml:32-35`); opencode declares `config` and `tui`. These are a few lines of manifest in the owning agent pack, not core. | codex, agy, opencode |
| **G6** | **A notch gate** on `files` and `config-list`, if [OQ-HR7](#OQ-HR7) withholds hooks from jails. | Only `env` has `jail_only` ([`packs/pi/pack.toml:225`](../../packs/pi/pack.toml)); an `autonomy` posture's `lists` is host-only or jail-only for `config-list`. | [OQ-HR7](#OQ-HR7) only |
| **G7** | **A herdr-presence gate**, if [OQ-HR6](#OQ-HR6) wants one. | None exists: `needs.when_bins` conditions a pack dependency ([`packdecl/needs.go:63-76`](../../internal/packdecl/needs.go)), not a contribution. | [OQ-HR6](#OQ-HR6) only |

Slice 1 needs G3 and the identical half of G4. The hook agents need G1, G2, G5.

## 5. One copy, and herdr's installer

The invariant: **after yolo renders, `herdr integration install <agent>` changes nothing.** It
holds because the pack's files and entries equal what the installer writes
([§2.1](#21-how-the-installer-behaves), MEASURED), and `yolo check` re-proves it on the user's
herdr ([§3.3](#33-how-yolo-notices)).

### 5.1 What yolo does with a file or entry that is already there

At the host notch, under `host_management = "own"`. Under `none` (also the unset state) yolo
writes nothing into the real home and `yolo host apply` says so naming the key
([`config-ownership.md`, the unset state](../reference/config-ownership.md#the-unset-state)); the
user's own `herdr integration install` stays the way in, and `yolo check` says that.

| What is on disk | What yolo does | The line it prints |
| :--- | :--- | :--- |
| Nothing | Writes the pin's file and appends the entry. | `herdr/files  wrote ~/.pi/agent/extensions/herdr-agent-state.ts` |
| A file with the pin's exact bytes, yolo has no record | **Adopts**: records ownership and its digest, writes nothing. *(Adopt is coined here: take over a file without changing its bytes.)* | `adopted ~/.pi/…  already herdr's v9 file; left as is` |
| A file herdr wrote at an **older** marker, provably pristine: its SHA-256 is in the pack's table of every herdr release's asset | Per [OQ-HR9](#OQ-HR9): archive the old copy, write the pin's. | `replaced … (archived to <path>)` |
| A file at a **newer** marker than the pin | Leaves it, never downgrades ([HAI-D3](#13-decision-ledger)), and does not record it as yolo's. | `left ~/.claude/hooks/…  herdr's v12 is newer than yolo's v11 pin; run yolo update` |
| A file whose bytes are in no release (a user edit) | Refuses, as today. | `refused … differs from every herdr release; to let yolo manage it: mv it aside, then run yolo host apply` |
| An entry equal to the pin's | The entry list already holds it ([config-list equality](../reference/pack-system.md#adding-entries-to-an-array-config-list), first occurrence wins); no second copy. | none |
| A herdr entry that differs (older path, other quoting) | G2 removes entries whose command names `herdr-agent-state`, then appends the pin's. | `replaced herdr's SessionStart entry` |
| The user ran `herdr integration uninstall` while the pack is selected | The next apply writes it back. Selecting the pack is the standing intent; the line says how to stop. | `restored … ; to stop managing herdr's files run yolo pack remove herdr` |

The row for a pristine older file is the one place yolo would replace a file it did not write.
It is provable (the digest table) and recoverable (archive first), and it is the owner's call:
[OQ-HR9](#OQ-HR9).

### 5.2 In a jail

The jail's home is yolo's. Its copy comes from the same declaration, so there is nothing to
dedupe except one import: **Claude's `settings.json` is read from the host into a jail** (the
`readsHost` layer, [`packs/claude/pack.toml:146`](../../packs/claude/pack.toml)), and a host with
herdr installed holds herdr's entry at the host path, which does not exist in the jail. That is
[HR-D9](../research/herdr-integration.md#HR-D9) and the only herdr artifact that crosses
today. The pack drops it with G2 and appends its own, at the jail's path from G1. The other six
agents have no host layer in a jail, so they need no dedupe: codex's and agy's configs have none,
copilot's surface is `rmw` without one, pi's and opencode's integrations are files, not keys.

### 5.3 Two writers, honestly

herdr's installer and yolo's apply can still run at once. Both write identical bytes, so the
worst result is a last-writer-wins on a file that does not change. The settings entry is the
risk: both edit `settings.json`. yolo's apply records what it inserted, and herdr's write goes through
`write_config` ([`config_file.rs:72-80`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/integration/config_file.rs));
what either does to a file the other replaced between its read and its write was not measured.

## 6. The channel, notch by notch

The extension and hook files are inert without `HERDR_ENV`, `HERDR_SOCKET_PATH` and
`HERDR_PANE_ID`. So rendering is identical everywhere and **the environment decides what is
live**. That is why this doc needs no per-notch rendering code.

### 6.1 The table

| Notch | What yolo renders | `HERDR_*` reaches the agent? | Extension state | Hook session ids |
| :--- | :--- | :--- | :--- | :--- |
| **Host, `host_management = own`** | Written into the real home by `yolo host apply`, with the record and archive of the `files` kind | **Yes**, carried through `yolo host` ([research §3.3](../research/herdr-integration.md#33-yolo-host), INFERRED there) | **Live.** Under `yolo host -- pi` the launch's per-launch session home links `extensions/` back to the real directory ([`hostsessionhome.go`](../../internal/cli/hostsessionhome.go)), so the file is reached (UNMEASURED) | **Live.** After a herdr restart herdr types `claude --resume <id>`, which `host_wrappers` routes back through yolo ([research §3.3](../research/herdr-integration.md#33-yolo-host)) |
| **Host, `none` or unset** | Nothing. `herdr integration install` is the user's | Yes | Whatever herdr's installer wrote | Same |
| **Container jail** | Mounted `:ro` in the jail home from the same declaration | **No**: `TERM_PROGRAM` only, by [HR-D2](../research/herdr-integration.md#HR-D2) | **Inert.** The guard returns at once | **Inert**, and withheld if [OQ-HR7](#OQ-HR7) says so |
| **macos-user** | Overlaid into the sandbox home | **No**: `env -i` with a closed list ([research §3.2](../research/herdr-integration.md#32-macos-user)); the sandbox account could not open a `0600` socket the user owns anyway | Inert | Inert |
| **seatbelt-self** (run-as-you) | Host-like renderer, UNMEASURED | UNMEASURED whether the closed list applies | Unknown until a Mac says | Unknown |

### 6.2 Every self-built way to carry it

All are options for a jail or macos-user and none opens by itself; [OQ-HR5](#OQ-HR5) decides. The
rows below are what a yes would pick from.

| ID | Way | How | Crosses a handle? | Fits the byte rule |
| :--- | :--- | :--- | :--- | :--- |
| **C1** | **A pack loophole relay over loopback-TLS** | A pack contributes a loophole. On the host, `yolo internal daemon herdr-relay` holds herdr's real socket and speaks `loopback-tls` ([`loophole-transport.md`](../reference/loophole-transport.md)); in the jail `yolo-jaild herdr-relay` listens on a Unix socket that herdr's own, unmodified extension connects to through `HERDR_SOCKET_PATH`. The host side filters: it allows `pane.report_agent`, `pane.report_agent_session`, a title-only `pane.report_metadata` and `ping`, overwrites `pane_id` with the launching pane's, drops `resume_argv` ([HR-D7](../research/herdr-integration.md#HR-D7)), pins the agent, and refuses the rest. | A jail-local socket path and `HERDR_ENV=1`. No herdr path, no `HERDR_PANE_ID` that matters | **Yes**: herdr's own bytes, unchanged. Env values delivered only where the relay is served, since a pack `env` is literal and ungated |
| **C2** | **A launcher-side file tail** (a report-only filter with no socket crossing) | A yolo-written jail extension appends JSON lines to a file under the workspace's `.yolo/`, which is bind-mounted live to the host. The launcher already runs `herdr pane report-agent` as the pane ([`herdragent.go`](../../internal/cli/run/herdragent.go)); it adds a reader that validates each line (a state from a closed set, a sequence number) and makes the same call. The pane is pinned by construction: the launcher *is* the pane. | **None.** No socket, no port, no `HERDR_*` in the jail | **No**: a yolo extension, not herdr's. It is also the only variant where the *launcher* chooses the report's source string, so it can send `herdr:pi` (authority, honest because it relays pi's own state) or `yolo-jail` (labels only, UNMEASURED how herdr ranks it) |
| **C3** | **In-band, over the pane's own terminal stream** | The launcher's TTY proxy sits in the pty between the pane and the runtime ([`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go), Linux only, TTY runs only). A yolo extension writes a private escape sequence; the proxy strips it and makes the same host call as C2. | **None** | No. The proxy package is Linux-only (`//go:build linux`) and bypassed for non-TTY runs, so it is the weakest on parity |
| **C4** | **Host-built resume only** | No jail-to-host state at all. The launcher, which knows the pane and the workspace, reports a yolo-built resume command (`yolo -- <agent> --continue` shaped, never an id from the jail) with `report-agent-session`. | **None** | Not applicable: no extension involved. Covers [OQ-HR7](#OQ-HR7)'s resume need, not live state |
| **C5** | **A report-only socket in the workspace** | A Unix socket under `.yolo/` the launcher serves, a C2 with a socket in place of a file. | None, but a socket inode does not carry a connection on macOS podman or Apple Container ([`loophole-transport.md:56`](../reference/loophole-transport.md)) | No; strictly worse than C2 |

Ranking for a yes: **C2**, because it opens no handle, needs no new daemon and no change to
`HERDR_*` handling, and fails closed (a jail that writes garbage gets a rejected line). **C1** is
the way to keep herdr's own bytes live in a jail, and costs a loophole, an in-jail daemon and a
filter over an API herdr changes. C3 and C5 are recorded and not recommended. Not one of them is
in slice 1. A nested jail cannot prove any of C1 to C3, which are reachability-shaped
([`AGENTS.md`](../../AGENTS.md), Testing); only a real rootless host with a herdr pane settles
them.

## 7. Activation and resume

### 7.1 Activation

"Nothing is active by default" ([`AGENTS.md`](../../AGENTS.md)). With the channel closed
([OQ-HR5](#OQ-HR5) A) the pack only renders inert files in jails and, under `own`, real files on the
host; auto-selecting it widens nothing a jail can reach, so the research doc's reason for refusing
auto-select ([§8.4 item 4](../research/herdr-integration.md#84-options-ranked)) holds only if a channel
opens. The question is therefore how the user learns the pack exists, which is [OQ-HR6](#OQ-HR6).

### 7.2 Resume for the hook agents

herdr builds `claude --resume <id>` itself from the hook's session id and types it into a **host**
shell at restore. On the host that is right. For a jail pane the id names a conversation inside the
jail and the command would run the host's `claude`, which is [HR-D7](../research/herdr-integration.md#HR-D7)'s
hazard. A yolo-built hook cannot repair it, because herdr builds the plan only for source
`herdr:<agent>`, and a yolo source gets no plan and may report its own `resume_argv` instead
([`agent_resume.rs:199-202`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/agent_resume.rs);
the research doc measured a `yolo` source's resume accepted). The decision is whether a jail's
hooks are delivered at all ([OQ-HR7](#OQ-HR7)).

## 8. The first slice

**Slice 1: the pi extension, on the host and in jails, from a `herdr` pack.** pi is the best first
agent: one file, no config edit, live state with authority, inert without `HERDR_ENV`, so a jail
copy cannot misbehave.

What it needs built, as outcomes (the plan assigns files):

1. The pack: pi's extension byte-identical at `v0.9.5`, a provenance file (tag, SHA-256, marker), herdr's license text, a pack `description` saying what the pack does and that it is inert outside a herdr pane (no briefing: it would cost every agent's context for a file no agent reads).
2. **G3**: the file lands only while the pi pack is selected, at herdr's exact path.
3. **G4, identical half**: a host apply adopts a byte-identical herdr file, and a refused file prints a next step instead of a bare refusal.
4. The refresh script (installer into scratch, copy, digest, provenance) and the digest tripwire test; the scheduled informational job.
5. The `yolo check` row: the probe, the `status` verdicts, the duplicate-copy finding, the three finding texts of [§3.3](#33-how-yolo-notices).
6. Tests that **fail if the call site is deleted**: a real `yolo host apply` into a throwaway home over each row of [§5.1](#51-what-yolo-does-with-a-file-or-entry-that-is-already-there) that slice 1 covers, and a `yolo check` run with a stand-in `herdr` on `PATH`. One check on a real herdr, run by a human: `yolo host -- pi` in a herdr pane shows pi's state from the extension, not from the screen.

**Not in slice 1:** hook agents, relay, harvest, omp, opencode.

**Later, in order.** Slice 2: claude, codex, copilot and agy on the host (G1, G2, G5; the
`hooks` collision with [`agent-event-watchers.md` EW-D5](agent-event-watchers.md#EW-D5) is a
shared list, so two appenders already coexist by design). Slice 3: omp and opencode. Slice 4: the
channel, if [OQ-HR5](#OQ-HR5) says yes. The harvest fallback is built only when the check row has
fired for a real herdr release and a yolo release was too slow to fix it.

**Notes for the planner (not decisions).** Check whether `pack lint` accepts a `files` claim into
another agent's directory. Check that the pi pack's `session_homes` shared list leaves
`extensions/` linked, not copied. Confirm herdr's Apache-2.0 text and yolo's own license compose
for redistribution. Decide the refresh script's name and the digest table's file format.

## 9. Not pursued by ruling

The research doc's upstream asks ([§8.4 option 2](../research/herdr-integration.md#84-options-ranked)):
a documented report-only transport, an `omp` alias, an `integration export` command, a
"containers" paragraph. None is needed, none is filed or waited on. The `omp` alias is moot, since
the pack-declared `herdr_agent = "omp"` already shipped
([`packs/omp/pack.toml:20`](../../packs/omp/pack.toml)). A herdr `integration export` would only
shorten the refresh script. The research doc's [OQ-HR8](../research/herdr-integration.md#OQ-HR8) (may the investigation open
issues on herdr's repository) is answered by ruling 3: no.

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| The pin lags herdr and a codex-style change makes old entries wrong | The install-over-ours probe names it; the refresh is one script and one commit; the harvest fallback is specified ([§3.1](#31-the-candidates-ranked)) |
| A user's herdr installer and yolo's apply both run | Identical bytes make it a no-op ([§5](#5-one-copy-and-herdrs-installer)); the entry equality is MEASURED |
| yolo replaces a user's file | Only a provably pristine one, archived first, and only if [OQ-HR9](#OQ-HR9) says A |
| Vendored code runs in a user's agent | The extension is herdr's own, and the user's herdr already asks them to install it; the pack's provenance and digest are listed by `yolo pack footprint` |
| herdr's protocol changes and old extensions fail silently | Unmeasurable offline; the human check names it. The extensions' own guards never break the agent |
| A jail copy drifts from the host copy | One declaration, the same renderers; `filesslotparity_test.go` pins the host and jail routes together for a slot, and slice 1's tests do the same for this pack |

## 11. What is unmeasured

- Whether `yolo host -- pi` reaches the file through the session home (INFERRED from `hostsessionhome.go`).
- Whether herdr shows live state from the yolo-delivered extension in a real pane.
- How herdr ranks a second reporting source on one pane, and whether a `yolo-jail` source's state is ignored.
- Whether two pi extension copies (herdr's and a package) are harmless. Not needed if [§4.1](#41-one-declaration-per-agent) holds.
- Every Mac claim: whether seatbelt-self carries `HERDR_*`; macos-user's inertness is by the closed `env -i` list, read from source, not run. **Mac witnesses** for the owner's Mac agent: `env | rg HERDR` from inside an agent under each of the two setups, started from a herdr pane.
- Whether the protocol, `22` today, is stable across the next herdr release.

## 12. Open questions

1. 💬 **<a id="OQ-HR5"></a>[OQ-HR5](#OQ-HR5): May a jail's agent report its state to herdr?**

   Matt runs `yolo -- pi` in a herdr pane. herdr shows the pane as a plain terminal with a
   `🔒 JAIL` label, because the jail's pi cannot reach herdr's socket: no `HERDR_*` crosses
   ([research §8.2](../research/herdr-integration.md#82-how-the-channel-crosses-the-jail-boundary-today)).
   With the extension delivered, a channel would make that pane show pi's exact `working`,
   `blocked` and `idle`. It would also be the first thing a jail sends to herdr itself, and
   herdr's socket can run host commands, so the channel must be report-only.

   - **A.** No. Jails stay channel-less. The jail's extension is delivered and inert, the pane keeps the launcher's label and herdr's screen reading, and slice 1 is the whole feature.
   - **B.** Yes, by relay (C1): herdr's own bytes, live in the jail through a filtering loophole.
   - **C.** Yes, by the launcher tailing a file (C2): a yolo-written extension, no handle crosses.
   - **D.** Yes, in a later slice, after a real-herdr measurement shows exact state beats the screen reading enough to justify a build.

   <!-- vantage: question id=OQ-HR5 leaning="A for now, with D as the path: slice 1 first; if a measurement says the screen reading is not enough, build C2, which opens no handle." -->

   _Leaning:_ **A**, with **D** as the way forward. If it becomes yes, **C** first: it opens no
   handle and fails closed, where B keeps herdr's bytes live at the price of a loophole, an in-jail
   daemon and a filter over an API herdr changes.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **<a id="OQ-HR6"></a>[OQ-HR6](#OQ-HR6): How does a user turn the `herdr` pack on?**

   Matt installs herdr, starts a pane and runs `yolo host -- pi`. The pack is not in his `packs`,
   so nothing is delivered and pi reports nothing. Auto-adding a pack is something only
   `cgroup-delegate` is allowed, because it can only limit a jail.

   - **A.** `yolo pack add herdr`, once, and nothing detects anything.
   - **B.** Auto-select the pack when the launcher sees `HERDR_ENV=1` on the host. One fewer step, and a pack that enters a jail on a detected signal.
   - **C.** A, plus a one-time line from the launcher's existing herdr arm: `herdr detected: add the integration with yolo pack add herdr`.

   <!-- vantage: question id=OQ-HR6 leaning="C: the existing launcher arm already detects the pane, one printed verb is the next step, and nothing is selected on a signal." -->

   _Leaning:_ **C.** The launcher already detects the pane, so it can print the one verb; nothing
   is selected on a signal, and the banner discloses the pack as it does every pack. B is only
   defensible if jails stay channel-less ([OQ-HR5](#OQ-HR5) A), and even then the rule has one exception
   already.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **<a id="OQ-HR7"></a>[OQ-HR7](#OQ-HR7): Do claude, codex, copilot and agy get herdr's hook inside a jail?**

   Matt jails `yolo -- claude` in a herdr pane, then restarts herdr. herdr types a stored command
   into the pane's shell to bring the agent back. If the jail's hook reported a session id, herdr
   would store `claude --resume <id>` and run the **host's** `claude` on a conversation the host
   never saw. The hooks add no live state (they report a session id only).

   - **A.** No. The hook agents' files are withheld from jails (needs [G6](#42-what-the-kinds-cannot-say-today)), delivered on the host, and a jail pane's resume is the launcher's own report (C4), never an id from the jail.
   - **B.** Yes, delivered everywhere and inert; a channel, if one opens, drops the session id.
   - **C.** Yes, delivered and live through the channel with the id passed; herdr's resume then runs on the host.

   <!-- vantage: question id=OQ-HR7 leaning="A: the hooks add no state and their id feeds a host-typed command; withhold them from jails and report resume from the launcher." -->

   _Leaning:_ **A.** The hooks add no state, and their id feeds a restore that types a host
   command. B delivers a file that can only matter if a channel opens, and C is the hazard itself.
   A costs one capability (G6) and gives the jail pane a resume the host builds.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **<a id="OQ-HR9"></a>[OQ-HR9](#OQ-HR9): May yolo replace a herdr file it did not write, when it can prove the file is herdr's own unmodified older copy?**

   Matt ran `herdr integration install pi` last month, then adds the `herdr` pack. His
   `~/.pi/agent/extensions/herdr-agent-state.ts` is herdr's v8; yolo's pin is v9. Today an
   occupied path yolo did not write is refused with no next step. The proof is a SHA-256 table of
   every herdr release's asset, so a user's edit never matches.

   - **A.** Yes: archive the old file (as every `files` replacement already is), write the pin's, and say so.
   - **B.** No: refuse, print `herdr integration uninstall pi` then `yolo host apply`, as the next step.
   - **C.** Ask each time on a terminal, A if yes.

   <!-- vantage: question id=OQ-HR9 leaning="A: the file is herdr's own unmodified copy, provable by digest, recoverable from the archive, and herdr's own header says the installer overwrites it." -->

   _Leaning:_ **A.** The file is provably herdr's own, herdr's header says its installer overwrites
   it, and the archive makes the replacement recoverable. B is the safe floor if you would rather
   yolo never touch a file it did not write.

   **Answer:**
   > _(empty — fill in when decided)_

## 13. Decision ledger

Decided here as implementation choices inside the rulings of [§1](#1-what-the-rulings-settle).
Overturn any by saying so.

| ID | Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| <a id="HAI-D1"></a>HAI-D1 | **The byte source is a vendored pin of herdr's files at a tag, refreshed by a script that runs herdr's own installer into a scratch home. The fallback is a render-time harvest through that same installer, built only if the check row fires.** Own protocol-speaking equivalents are for what herdr ships nothing for. Cutting literals from the binary, a git-sourced tree and release artifacts are rejected | 2026-11-26 | [§3](#3-where-the-bytes-come-from) |
| <a id="HAI-D2"></a>HAI-D2 | **yolo never edits, renames or re-quotes herdr's bytes or entries.** The same filenames, paths, matcher, timeout and `bash '<abs>' <action>` command, so herdr's installer is a no-op | 2026-11-26 | [§5](#5-one-copy-and-herdrs-installer) |
| <a id="HAI-D3"></a>HAI-D3 | **yolo never downgrades.** A herdr-managed file at a marker newer than the pin is left alone and reported; yolo's pin fills only an absent file or, per [OQ-HR9](#OQ-HR9), a provably pristine older one | 2026-11-26 | [§5.1](#51-what-yolo-does-with-a-file-or-entry-that-is-already-there) |
| <a id="HAI-D4"></a>HAI-D4 | **herdr judges the pin.** `yolo check` renders the pack into a scratch home and runs herdr's own `integration install` and `status` over it; no launch runs herdr for this | 2026-11-26 | [§3.3](#33-how-yolo-notices) |
| <a id="HAI-D5"></a>HAI-D5 | **Rendering is identical on every notch; herdr's own environment guards decide what is live.** No per-notch rendering code | 2026-11-26 | [§6](#6-the-channel-notch-by-notch) |
| <a id="HAI-D6"></a>HAI-D6 | **A jail's host-imported herdr entry is dropped by pattern removal in the pack (G2), not by `host_import_exclusions`**, which is refused under `own` and removes every host hook | 2026-11-26 | [§5.2](#52-in-a-jail) |
| <a id="HAI-D7"></a>HAI-D7 | **The pack is named `herdr`, ships no default selection, and pi goes first** | 2026-11-26 | [§8](#8-the-first-slice) |
| <a id="HAI-D8"></a>HAI-D8 | **The exact home path is used, not pi's package slot**, so a user with herdr's own copy does not load two | 2026-11-26 | [§4.1](#41-one-declaration-per-agent) |
