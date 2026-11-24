---
title: "Which instruction files an agent reads from the repository: keep, ignore, or replace"
date: 2026-11-24
status: in-review
stage: DESIGN
tags: [instructions, packs, briefing, config, mounts, parity]
summary: "A repository's own AGENTS.md, CLAUDE.md and their kin can be kept, ignored or replaced by a file the user names. Ignoring uses each agent's own setting, rendered by its pack at every setup; only an agent with no such setting falls back to an empty read-only file bound over the path."
next: "Rule OQ-IF1 and OQ-IF2; then a planner completes the build hand-off from the seed"
---

# Which instruction files an agent reads from the repository: keep, ignore, or replace

**Status:** 2026-11-24. Nothing built. Repository evidence read at `0b7092a6b`; agent evidence read from the
installed programs in this jail on the same date ([Appendix A](#appendix-a-evidence)). Every macOS and Apple
Container cell is UNMEASURED: no Mac was available.

> **In short.** A repository's instruction files are the agent's to read only because nobody told the agent
> not to. Each agent that has a setting for it gets one rendered by its pack, at every setup. An agent
> with no setting cannot be made to ignore the files without lying to git, so that path is narrow.

**Why it matters.** Today a jail's agent reads whatever the checked-out repository says, and the only way out is to
edit the repository or hand-edit one agent's own settings.

**The shape.** One config table, `repo_instructions`; one new pack contribution, `instructions`, that says which
files an agent reads and which of three mechanisms stops it; the existing briefing path for a replacement.

**Cost.** One new pack kind, one config table, a launch disclosure, and a fallback that is deliberately hard to use.

**Start at [§4](#4-the-per-agent-mechanisms).**

**Needs your ruling:** [OQ-IF1](#OQ-IF1), [OQ-IF2](#OQ-IF2).

**Reads with:** [`workspace-instruction-files-plan.md`](workspace-instruction-files-plan.md) (the build hand-off's
seed, incomplete while the two questions are open), [`agent-briefings.md`](../reference/agent-briefings.md) (the
briefing a replacement joins), [`report-tiers.md`](../reference/report-tiers.md) (why the disclosure cannot be
silenced).

---

## 1. What the owner asked for, and what stands in the way

The request: block or ignore the upstream instruction files that come with a repository, per user or per
workspace; on a container, mount an empty file over them; or overlay the user's own.

Two facts shape everything below.

- **P1. The agent's own user-level instruction file is where yolo puts its briefing.** `~/.claude/CLAUDE.md`,
  `~/.pi/agent/AGENTS.md`, `~/.codex/AGENTS.md` and `~/.copilot/copilot-instructions.md` are the packs'
  briefing destinations. A switch that turns off *all* instruction loading also turns off the briefing, which
  is the one text the jail's agent must read ([Appendix A](#appendix-a-evidence), claude `managed-only`, pi
  `--no-context-files`).
- **P2. Git sees what is mounted.** A file bound over `/workspace/AGENTS.md` reads as empty to git in the jail, so
  `git commit -a` records an emptied upstream file. yolo never writes the user's git state (no
  `skip-worktree`, no exclude lines), so it cannot hide the mask from git.

## 2. Vocabulary

- **Instruction file** — a file an agent finds by name in the workspace and puts in its prompt: `AGENTS.md`,
  `CLAUDE.md`, `CLAUDE.local.md`, `GEMINI.md`, `.github/copilot-instructions.md`, path-scoped rule files. Not a
  skill, not an MCP file, and not the user-level file of P1.
- **Mode** *(coined here)* — what a launch does about those files for the selected agents: `keep`, `ignore`,
  `replace`.
- **Knob** — the agent's own supported way to stop reading them: a setting, a command-line flag or an
  environment variable. Not a mount.
- **Mask** *(coined here)* — an empty read-only file bound over an existing instruction file inside a
  container. The fallback for an agent with no knob.

## 3. The decisions this design rests on

The owner's rulings are in the [ledger](#10-decision-ledger) as `IF-D1` to `IF-D6`. In short:

- The knob is primary, rendered by the agent's pack through the config composition, so every setup gets it
  (the parity rule). The mask is the fallback for an agent with no knob, on container setups only.
- Scope is every instruction file a selected agent reads from the repository, root and nested, per agent.
- Modes are `keep` (the default, today's behavior), `ignore`, and `replace` with a user-named file composed
  through the briefing.
- The user config, the per-workspace layer and the committed project file may each set the mode.
- Every launch discloses what it hid or replaced.

### 3.1 The config key

One table, in both scopes (`SchemaScopeBoth`), spelled so `yolo config set repo_instructions.mode ignore` works
like any other key ([editing.md](../reference/config/editing.md)):

```toml
[repo_instructions]
mode = "replace"                  # keep (default) | ignore | replace
replace_with = "~/rules/mine.md"  # required when mode is replace; refused otherwise
```

| Rule | Value |
| :--- | :--- |
| Merge | The ordinary order ([merge order](../reference/config/README.md#merge-order)): user, then `yolo-jail.toml`, then `yolo-jail.local.toml`, then the per-workspace file. Later wins, `mode` and `replace_with` independently. |
| `replace_with`, user layers | An absolute path or `~/…`, read as the user spelled it. |
| `replace_with`, project layers | A path inside the workspace, read without following links ([P5](../reference/agent-briefings.md#ws-p5)); a link or an escape is refused naming the path. |
| Missing, unreadable or non-regular file | The launch refuses naming the path and `yolo config unset repo_instructions.replace_with`. |
| Empty or whitespace-only file | Behaves as `ignore`; no empty section is composed. |
| Size | The text counts toward the briefing's existing size line ([budgets](../reference/agent-briefings.md#budgets-and-the-size-line)); yolo never trims it. |

The committed project file may set `ignore` or `replace`. A change to it reaches a launch only through the
config-change prompt, like any project key.

### 3.2 The new pack contribution

`instructions` *(coined here)* is a pack contribution, so core still knows no agent names. It declares, for the
agent the pack installs:

- **`reads`** — the workspace-relative name patterns the agent loads, with whether it walks up, down or both.
  It drives the disclosure and the mask.
- **At most one mechanism**, which the pack's author fills from the agent's own documentation:
  - `ignore.list` — entries added to an array in a config surface (`claudeMdExcludes`), through the existing
    `config-list` path, so the user's own entries survive. `${workspace}` is the workspace path.
  - `ignore.set` — a value set in a config surface, or handed as a launch argument.
  - `ignore.launch` — flags handed to the program at launch, the way the `autonomy` kind hands
    `--dangerously-skip-permissions`. Only this kind may carry them, and only while the mode is not `keep`.
  - nothing — the agent is fallback-only.
- **`redeliver`** (optional) — a second briefing destination used only while ignoring, for a mechanism that
  also drops the user-level file (P1).

A census test requires every pack that installs an agent and declares a briefing to declare `instructions`
(possibly with no mechanism), so a new agent pack cannot ship unclassified.

## 4. The per-agent mechanisms

Every name here has a cited source in [Appendix A](#appendix-a-evidence). Versions are the ones installed in
this jail.

| Agent | Reads from the repository | Knob | Collateral | Verdict |
| :--- | :--- | :--- | :--- | :--- |
| `claude` 2.1.331 | `AGENTS.md`, `.claude/AGENTS.md`, `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md`, `.claude/rules/**`; cwd and ancestors at start, a subdirectory's own on demand | Setting `claudeMdExcludes`, an array of absolute-path globs, at any settings layer, merged across layers; it applies to every file above | None if each glob is anchored at the workspace; `managed-only` and `CLAUDE_CODE_DISABLE_CLAUDE_MDS` would also drop the briefing | **Knob, list.** |
| `codex` 0.166.0 | `AGENTS.override.md`, `AGENTS.md`, then `project_doc_fallback_filenames`; one file per directory, project root down to cwd | Config `project_doc_max_bytes = 0` (`-c` flag at launch) stops project loading | None: the global `~/.codex/AGENTS.md` loads separately. Source read at `main`; the key is present in the 0.166.0 binary | **Knob, set.** |
| `pi` 1.6.0 | `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD` per directory, cwd and ancestors; also `.pi/SYSTEM.md` and `.pi/APPEND_SYSTEM.md` | Flag `--no-context-files` / `-nc`; no settings key or variable | Drops the agent-dir `AGENTS.md`, the briefing; `APPEND_SYSTEM.md` is read independently. The `.pi/` files are **not** covered | **Knob, launch flag, with `redeliver` to `APPEND_SYSTEM.md`.** |
| `copilot` 1.0.114 | `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, `.github/instructions/**`, `.claude/rules/**`, plus `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` | Flag `--no-custom-instructions`; no setting or variable found | UNMEASURED whether it also drops `~/.copilot/copilot-instructions.md`, the briefing | **Fallback-only** until measured. |
| `opencode` 1.18.34 | `AGENTS.md`, `CLAUDE.md`, `CONTEXT.md`, found upward from the directory to the worktree | Only `OPENCODE_DISABLE_PROJECT_CONFIG=1`, which also drops the project's `opencode.json`, `.opencode` and TUI config | Too broad to be a knob for instruction files | **Fallback-only.** |
| `agy` | `GEMINI.md`, `AGENTS.md`, walking up from cwd to the repository root | None in `--help`, the binary's settings text or its bundled docs | n/a | **Fallback-only.** |
| `muse` | First of `AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md` per directory, up to the nearest `.git` | None documented; only workspace trust gates them | n/a | **Fallback-only.** |
| `oh-omp` | Native formats from many tools, including `AGENTS.md` | None documented; binary not installed here | n/a | **Fallback-only**, UNMEASURED. |

An agent counts as having a knob only when the knob leaves the briefing in place or the pack can redeliver it
([IF-D8](#10-decision-ledger)). The `.pi/` files are listed in pi's `reads` and named in its disclosure as
still read, so the line is truthful about what the mode did not reach.

## 5. What each mode does

| Mode | Knob agent | Fallback-only agent |
| :--- | :--- | :--- |
| `keep` | Nothing is rendered or handed; the launch prints nothing about it. | The same. |
| `ignore` | The pack's mechanism is rendered or handed; the launch names it. | The mask, if [§7](#7-the-mask-and-the-git-hazard) allows it; otherwise [§8](#8-when-a-mode-cannot-be-honored). |
| `replace` | `ignore`, then the file's text is added to the briefing as one titled section. | The same. |

The replacement joins the briefing through the user block ([the same path as `agents_md_extra`](../reference/agent-briefings.md#customizing-in-practice)),
so it lands in every destination a selected pack declares, refreshes the way the briefing does, and is
composed by `yolo host apply` for the user-scope value only. A workspace-layer replacement is never written into
the user's real home, for the reason `agents_md_extra` is not.

### 5.1 The disclosure (IF-D5)

Every launch under `ignore` or `replace` prints, per selected agent, one line in the launch stream:

```text
repo instructions: claude: ignoring 3 files (AGENTS.md, CLAUDE.md, +1 nested) through claudeMdExcludes
repo instructions: pi: ignoring AGENTS.md; still read: .pi/APPEND_SYSTEM.md
repo instructions: agy: replaced by ~/rules/mine.md; 1 file masked (AGENTS.md)
```

- The files are counted by a bounded scan: depth at most 6, skipping `.git` and `node_modules`, at most 5,000
  directories. A scan that cannot finish says so and names the patterns instead.
- It is in the launch stream and `launch.log`, and `YOLO_NO_BANNER` does not hide it ([report-tiers.md](../reference/report-tiers.md)).
- `yolo check` and `yolo config why repo_instructions.mode` name the layer that set the mode.

## 6. Per-setup behavior

The four columns are the setups the parity rule covers. `yolo host` is the unconfined path; its two verbs differ
because `yolo host apply` writes the user's one real home, shared by every workspace, so it can carry only
user-scope values.

| Mechanism | podman jail | Apple Container | macos-user | host |
| :--- | :--- | :--- | :--- | :--- |
| `ignore.list` (claude) | Rendered into the per-workspace `~/.claude/settings.json` | Same | Same, into the account's home | `yolo host -- claude` hands `--settings` JSON carrying the entries (UNMEASURED that claude reads the array from that layer); `yolo host apply` writes none |
| `ignore.set` (codex) | Rendered into the per-workspace `~/.codex/config.toml` | Same | Same | `yolo host -- codex` hands `-c project_doc_max_bytes=0` |
| `ignore.launch` (pi) | Flag from the launcher, briefing redelivered | Same | Same | `yolo host -- pi` hands the flag; the real home's briefing is the user's, not redelivered, and the line says pi's global file is dropped |
| Mask (fallback-only agents) | `:ro` bind of an empty file; subject to [OQ-IF1](#OQ-IF1) | Same from Apple Container 1.1.0; below it the mask cannot be enforced, so [§8](#8-when-a-mode-cannot-be-honored) applies | No bind mounts exist; not built, so [§8](#8-when-a-mode-cannot-be-honored) applies | No mask exists; [§8](#8-when-a-mode-cannot-be-honored) applies |
| `replace` text | Briefing destinations | Same | Same | User-scope value only, by `yolo host apply`; a workspace-layer value is not honorable, [§8](#8-when-a-mode-cannot-be-honored) |

The per-workspace home is why the jail cells can anchor entries at the workspace: the settings file belongs to
one workspace, so a pattern like `${workspace}/**/AGENTS.md` never matches the briefing under `/home/agent` and
never leaks to another workspace.

## 7. The mask and the git hazard

The mask is the owner's "mount an empty file". For each existing instruction file the agent's `reads` names
(found by the same scan), the launch binds one yolo-owned empty file read-only over
`/workspace/<path>`, beside [`workspace_readonly`](../reference/jail-home.md)'s binds
(`internal/cli/run/mounts.go`). Three consequences:

- **Git sees an emptied file** (P2). `git commit -a` or `git add -A` in the jail records the empty content as a
  change to a tracked upstream file. The host's own copy is untouched until that commit is pushed.
- **The file cannot be replaced.** A bind mount blocks the unlink-and-rename that `git checkout` and `git pull`
  use, so they fail on a masked path with "Device or resource busy".
- **A file created later is not masked.** The scan runs at launch.

Containment turns on whether git tracks the path, which `git ls-files` answers without writing anything. That
choice is [OQ-IF1](#OQ-IF1). Whatever it rules, the mask never exists on a setup where `:ro` is not enforced.

## 8. When a mode cannot be honored

A mode cannot be honored for an agent when it is fallback-only and the setup has no mask (macos-user, host,
Apple Container below 1.1.0), when the mask is withheld by [OQ-IF1](#OQ-IF1), or when a workspace-layer
`replace` meets `yolo host`. Silently launching would leave the agent reading the very files the user asked it not
to read. What the launch does is [OQ-IF2](#OQ-IF2).

## 9. Non-goals, and alternatives rejected

- **Not a trust or safety boundary.** `ignore` stops the agent's *loading*; it does not stop an agent that opens a
  file by hand or a repository whose hooks or skills carry instructions of their own.
- **Not skills, MCP files or `.claude/settings.json`.** Those have their own layers.
- **No per-agent overrides in v1.** One mode for every selected agent.
- **Rejected: a global switch** (`managed-only`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS`, `OPENCODE_DISABLE_PROJECT_CONFIG`).
  Each drops the briefing or unrelated project config.
- **Rejected: mask as the primary** ([P2](#1-what-the-owner-asked-for-and-what-stands-in-the-way)).
- **Rejected: hide the mask from git** with `skip-worktree`, `assume-unchanged` or an exclude line. It writes the
  user's git state, which they ruled against for settings in general ([RC-D71](../reference/config/rulings.md#RC-D71)'s owner words: "VERY surprising when
  your own settings get changed for you").
- **Rejected: a stricter merge so a project file cannot lower the user's mode.** The config-change prompt
  already shows a project edit, and a repository writing `keep` is asking for today's behavior.

## 10. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| IF-D1 | Mechanism: each agent's own knob rendered by its pack through the config composition, at every setup; the mask is a fallback for an agent with no knob, container setups only, because git sees the mask | 2026-11-24 | [§3](#3-the-decisions-this-design-rests-on), [§7](#7-the-mask-and-the-git-hazard) | — |
| IF-D2 | Scope: every instruction file a selected agent reads from the repository, root and nested, per agent, never a global filename list | 2026-11-24 | [§4](#4-the-per-agent-mechanisms) | — |
| IF-D3 | Modes `keep` (default), `ignore`, `replace` with a user-named file; replace composes through the briefing | 2026-11-24 | [§5](#5-what-each-mode-does) | — |
| IF-D4 | The user config, the per-workspace layer and the committed project file may each set it | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D5 | Every launch discloses what it hid or replaced; not suppressible | 2026-11-24 | [§5.1](#51-the-disclosure-if-d5) | — |
| IF-D6 | One config table `repo_instructions` with `mode` and `replace_with`, both scopes | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D7 | The mechanism lives in a new pack contribution `instructions` with `reads` and at most one of `ignore.list`, `ignore.set`, `ignore.launch`; core names no agent. A census test requires every agent pack to declare it. Tier 1 (structural) | 2026-11-24 | [§3.2](#32-the-new-pack-contribution) | — |
| IF-D8 | A flag or setting that also drops the user-level briefing is a knob only with a declared `redeliver`; pi redelivers to `APPEND_SYSTEM.md`; copilot, unmeasured, is fallback-only; opencode's only switch is too broad | 2026-11-24 | [§4](#4-the-per-agent-mechanisms) | — |
| IF-D9 | Claude entries are anchored at `${workspace}` and added as a list, never replacing the user's own `claudeMdExcludes` | 2026-11-24 | [§3.2](#32-the-new-pack-contribution), [§6](#6-per-setup-behavior) | — |
| IF-D10 | Ordinary merge order, later layer wins; no stricter rule for the project file | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D11 | A missing or unreadable `replace_with` refuses the launch; an empty one behaves as `ignore` | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D12 | The disclosure counts files with a bounded scan (depth 6, 5,000 directories, skipping `.git` and `node_modules`) and says when it could not finish | 2026-11-24 | [§5.1](#51-the-disclosure-if-d5) | — |
| IF-D13 | At the host, workspace-scoped mechanisms go through the launch (`yolo host -- <agent>`), never into the shared real home by `yolo host apply`; `replace` at the host is the user-scope value only | 2026-11-24 | [§6](#6-per-setup-behavior) | — |

## 11. Open questions

1. 💬 **OQ-IF1: May the mask cover a file git tracks?**

   Priya's repository tracks `AGENTS.md`, and her jail runs `agy`, which has no setting to skip it. She sets
   `mode = "ignore"` and launches. The mask makes `AGENTS.md` empty inside the jail; an hour later she tells the
   agent to "commit everything" and `git commit -a` records the emptied file. Today nothing masks anything.
   Almost every upstream instruction file is tracked, so this choice decides whether the fallback is usable at all.

   - **A — Mask only files git does not track.** Untracked or ignored files (`CLAUDE.local.md`) are masked; for
     a tracked one the launch treats the agent as unable to ignore, and says why. Priya sees "agy: AGENTS.md is tracked by git;
     a mask would commit it empty".
   - **B — Mask tracked files and guard git.** The jail's `git` refuses to stage or commit a masked path, saying
     which one. Priya's agent can ignore the file, but a shim over `git` is a new thing to build and keep right.
   - **C — Mask tracked files, disclose only.** Priya sees the disclosure line and the risk is hers; the
     emptied commit can happen.

   <!-- vantage: question id=OQ-IF1 leaning="A (Mask only files git does not track) — a mode that can silently empty a tracked file upstream is worse than a mode that says it cannot be honored." -->

   _Leaning:_ A (Mask only files git does not track) — a mode that can silently empty a tracked file upstream is worse than a mode that says it cannot be honored.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-IF2: When a selected agent cannot honor the mode, does the launch refuse?**

   Priya sets `mode = "ignore"` in her user config because she distrusts upstream instructions, then launches a
   workspace that selects `claude` and `agy`. Claude honors it; `agy` cannot (no setting, and the mask is withheld
   for a tracked file). Today there is no mode, so the question never arises.

   - **A — Refuse the launch.** Priya sees "agy cannot ignore this repository's AGENTS.md here" with the next
     steps: `yolo pack remove agy` for this workspace, or `yolo config set repo_instructions.mode keep`.
     Nothing runs until she chooses; no hatch exists.
   - **B — Launch and disclose.** The launch runs and prints "agy: NOT ignoring AGENTS.md" in the disclosure
     line. Priya may not read it, and `agy` reads the files she meant to avoid.

   <!-- vantage: question id=OQ-IF2 leaning="A (Refuse the launch) — ignoring is a distrust stance, so a launch that quietly does not ignore defeats it." -->

   _Leaning:_ A (Refuse the launch) — ignoring is a distrust stance, so a launch that quietly does not ignore defeats it.

   **Answer:**
   > _(empty — fill in when decided)_

## 12. Test plan

Event-driven, no agent sessions; probes are `--version` and `copilot instruction list`-style listings only.

- **Config.** Validation table: modes, `replace_with` required and refused, project-layer path escape and link,
  missing file, empty file; `yolo config set|unset|why` round trip; merge order across the four layers.
- **Pack kind.** Decode and validate `instructions`; the census over every shipped agent pack
  (`TestEveryAgentPackDeclaresItsInstructionFiles`); a deleted declaration must fail it.
- **Render per mechanism.** claude `ignore` yields workspace-anchored entries and a user's own array survives;
  a glob match of every entry against the briefing's path is false and against a nested workspace file is true;
  codex argv carries `-c project_doc_max_bytes=0`; pi argv carries `-nc` and the briefing lands in
  `APPEND_SYSTEM.md`; `keep` renders and hands nothing.
- **Replace.** The section is in every destination; `yolo host apply` composes only the user-scope value.
- **Disclosure.** The line per agent and mode, present with `YOLO_NO_BANNER` set, and in `launch.log`.
- **Mask.** Argv holds `-v <empty>:/workspace/<path>:ro` for existing files only; a tracked path follows
  [OQ-IF1](#OQ-IF1) using a fixture repository; Apple Container below 1.1.0 and macos-user produce the outcome [OQ-IF2](#OQ-IF2) rules.
- **Parity.** One table test names every cell of [§6](#6-per-setup-behavior), in the style of the briefing parity
  tests, and fails when a new mechanism has no cell.
- **Integration (nested jail).** A masked file reads empty and `git status` shows it modified, which proves the
  hazard the disclosure names. Nothing here proves reachability or rootless-only behavior.

Each test must fail if its call site is deleted.

## 13. Files a build would touch

The seed lists them with notes ([`workspace-instruction-files-plan.md`](workspace-instruction-files-plan.md)):
`internal/packdecl/packdecl.go` (the kind), `internal/packload` (validation, census),
`internal/config/schema.go`, `validate.go` and the editing verbs, `internal/render/configkeys.go` and
`internal/setupcensus/configkeys.go` (per-setup honored status), `internal/entrypoint/surfaceloop.go` (rendering),
`internal/cli/run/mounts.go` and a new disclosure file (mask and lines), `internal/jailcontent` (the replacement
section), the shipped `packs/*/pack.toml` for the agents in [§4](#4-the-per-agent-mechanisms), and
[`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md).

## Appendix A: Evidence

Read 2026-11-24 in this jail. Programs were never run beyond `--version`, `--help` and listing verbs.

| Claim | Source |
| :--- | :--- |
| claude `claudeMdExcludes`: globs or absolute paths, picomatch on absolute paths, User, Project and Local only, managed cannot be excluded | The setting's schema text in `~/.local/share/claude/versions/2.1.331`; [How Claude remembers your project](https://code.claude.com/docs/en/memory), "Exclude specific CLAUDE.md files" |
| It applies at any settings layer, arrays merge, and its patterns apply inside each `AGENTS.md` | Same page; the binary's `AGENTS.md` module text |
| claude reads `AGENTS.md` when no `CLAUDE.md` or `CLAUDE.local.md` is above cwd; modes `claude-md-or-agents-md`, `claude-md-and-agents-md`, `claude-md`, `managed-only`; set under `pluginConfigs["cc-plugin-agents-md@builtin"].options.instructionFiles` in user, `--settings` or managed settings only | Same page, "AGENTS.md" and "Choose which instruction files load"; the binary holds the four mode names |
| `managed-only` also drops user `CLAUDE.md`; `CLAUDE_CODE_DISABLE_CLAUDE_MDS` returns no files | Same page; `iGn` and `vss` in the binary |
| claude `--settings <file-or-json>` and `--setting-sources` exist | `claude --help` |
| codex reads `AGENTS.override.md`, then `AGENTS.md`, then `project_doc_fallback_filenames`, root to cwd, one per directory; global `~/.codex/AGENTS.md` first | [Codex AGENTS.md guide](https://learn.chatgpt.com/docs/agent-configuration/agents-md) |
| codex returns no project instructions when `project_doc_max_bytes == 0` | `codex-rs/core/src/agents_md.rs` on `main` (not the 0.166.0 tag); the key name and `agents_md.rs` are in the 0.166.0 binary; `codex --help` documents `-c key=value` |
| pi's file names, ancestor walk, agent-dir file, `AGENTS.override.md` scope | `resource-loader.js` and `docs/configuration.md` in `@earendil-works/pi-coding-agent` 1.6.0 |
| pi `--no-context-files` empties `agentsFiles`, including the agent-dir file; `APPEND_SYSTEM.md` is read separately; no settings key | `resource-loader.js` lines 129, 179-209, 483, 973-977; `docs/cli.md`; `schemas/settings.schema.json` has no such key |
| copilot's file list and `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` | `copilot help config`, `copilot help environment`; [custom instructions](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions) |
| copilot `--no-custom-instructions`; no setting or variable | `copilot --help`; the two help topics |
| copilot's flag effect on the personal file | UNMEASURED: `copilot instruction list` takes no such flag |
| opencode names and the broad switch | The JS in `opencode-ai` 1.18.34: names `["AGENTS.md","CLAUDE.md","CONTEXT.md"]` with `disableClaudeCodePrompt` removing `CLAUDE.md`; `OPENCODE_DISABLE_PROJECT_CONFIG` also gates `opencode.json`, `.opencode` and TUI config |
| agy rule locations and no disable setting | `agy --help`; the binary's embedded "Workspace Rules" text and env-name strings |
| muse's file order, upward walk and trust gate | [Muse Code configuration](https://dev.meta.ai/docs/muse-code/configuration); `muse` not installed here |
| oh-omp discovery and no documented disable | The project README; `oh-omp` not installed here |
| Packs deliver the briefing to the user-level files | `[[briefing]]` in `packs/claude`, `packs/pi`, `packs/codex`, `packs/copilot`, `packs/agy`, `packs/muse-code`, `packs/opencode` |
| Launch flags are a posture-only channel today | [pack-system.md](../reference/pack-system.md) `autonomy`; `packs/claude/pack.toml` |
| Apple Container honors `:ro` from 1.1.0 | `acROBindsFloor` in `internal/cli/run/backendcaps.go` |
