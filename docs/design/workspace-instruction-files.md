---
title: "Which instruction files an agent reads from the repository: keep, ignore, or replace"
date: 2026-11-24
status: accepted
stage: DECIDED
tags: [instructions, packs, briefing, config, parity]
summary: "A repository's own AGENTS.md, CLAUDE.md and their kin can be kept, ignored or replaced by a file the user names. Ignoring uses each agent's own setting, rendered by its pack at every setup; an agent with no such setting is unsupported, the launch continues, and says so. yolo never masks a workspace file."
next: "Build from the plan seed (workspace-instruction-files-plan.md); the owner's rulings are in the ledger"
---

# Which instruction files an agent reads from the repository: keep, ignore, or replace

**Status:** 2026-11-24. Both open questions ruled the same day; the build follows this document. Repository
evidence read at `0b7092a6b`; agent evidence read from the installed programs in this jail on the same date
([Appendix A](#appendix-a-evidence)). Every macOS and Apple Container cell is UNMEASURED: no Mac was available.

> **In short.** A repository's instruction files are the agent's to read only because nobody told the agent
> not to. Each agent that has a setting for it gets one rendered by its pack, at every setup. An agent
> with no setting is unsupported: yolo never masks a workspace file, so the launch continues and says that
> agent still reads them.

**Why it matters.** Today a jail's agent reads whatever the checked-out repository says, and the only way out is to
edit the repository or hand-edit one agent's own settings.

**The shape.** One config table, `repo_instructions`; one new pack contribution, `instructions`, that says which
files an agent reads and which of three mechanisms stops it; the existing briefing path for a replacement.

**Cost.** One new pack kind, one config table and a launch disclosure. Four of the eight agents have no way to
ignore the files, so for them the mode only discloses.

**Start at [§4](#4-the-per-agent-mechanisms).**

**Needs your ruling:** None.

**Reads with:** [`workspace-instruction-files-plan.md`](workspace-instruction-files-plan.md) (the build hand-off's
seed), [`agent-briefings.md`](../reference/agent-briefings.md) (the
briefing a replacement joins), [`report-tiers.md`](../reference/report-tiers.md) (why the disclosure cannot be
silenced).

---

## 1. What the owner asked for, and what stands in the way

The request: block or ignore the upstream instruction files that come with a repository, per user or per
workspace, or overlay the user's own. The owner first asked for an empty file mounted over them and then ruled
that mount out ([IF-D14](#10-decision-ledger)).

Two facts shape everything below.

- **P1. The agent's own user-level instruction file is where yolo puts its briefing.** `~/.claude/CLAUDE.md`,
  `~/.pi/agent/AGENTS.md`, `~/.codex/AGENTS.md` and `~/.copilot/copilot-instructions.md` are the packs'
  briefing destinations. A switch that turns off *all* instruction loading also turns off the briefing, which
  is the one text the jail's agent must read ([Appendix A](#appendix-a-evidence), claude `managed-only`, pi
  `--no-context-files`).
- **P2. yolo never masks a workspace file.** A file bound over `/workspace/AGENTS.md` reads as empty to git in
  the jail, so `git commit -a` records an emptied upstream file, and the bind blocks the unlink-and-rename that
  `git checkout` and `git pull` use. yolo cannot hide that from git without writing the user's git state, which
  it never does. So no mode ever binds, replaces or hides a workspace file, for any agent, tracked or not.

## 2. Vocabulary

- **Instruction file** — a file an agent finds by name in the workspace and puts in its prompt: `AGENTS.md`,
  `CLAUDE.md`, `CLAUDE.local.md`, `GEMINI.md`, `.github/copilot-instructions.md`, path-scoped rule files. Not a
  skill, not an MCP file, and not the user-level file of P1.
- **Mode** *(coined here)* — what a launch does about those files for the selected agents: `keep`, `ignore`,
  `replace`.
- **Knob** — the agent's own supported way to stop reading them: a setting, a command-line flag or an
  environment variable. Not a mount.
- **Unsupported** *(coined here)* — an agent with no knob. The modes `ignore` and `replace` cannot change what it
  reads from the repository; its launch continues and says so ([§8](#8-an-agent-that-cannot-honor-the-mode)).

## 3. The decisions this design rests on

The owner's rulings are in the [ledger](#10-decision-ledger) as `IF-D1` to `IF-D6`. In short:

- The knob is the only mechanism, rendered by the agent's pack through the config composition (or handed as a
  launch argument), so every setup gets it (the parity rule). An agent with no knob is unsupported.
- Scope is every instruction file a selected agent reads from the repository, root and nested, per agent.
- Modes are `keep` (the default, today's behavior), `ignore`, and `replace` with a user-named file composed
  through the briefing.
- The user config, the per-workspace layer and the committed project file may each set the mode.
- Every launch discloses what it ignored or replaced, and which selected agents still read the files.

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
  It drives the disclosure.
- **At most one mechanism**, which the pack's author fills from the agent's own documentation:
  - `ignore.list` — entries added to an array in a config surface (`claudeMdExcludes`), through the existing
    `config-list` path, so the user's own entries survive. `${workspace}` is the workspace path.
  - `ignore.set` — a value set in a config surface, or handed as a launch argument.
  - `ignore.launch` — flags handed to the program at launch, the way the `autonomy` kind hands
    `--dangerously-skip-permissions`. Only this kind may carry them, and only while the mode is not `keep`.
  - nothing — the agent is unsupported.
  - `ignore.host_launch` — flags handed only by `yolo host -- <agent>`, in place of `list` and `set`, which
    write a per-workspace file the unconfined path does not have ([§6](#6-per-setup-behavior)).
- **`redeliver`** (optional) — for a mechanism that also drops the user-level file (P1), the briefing file the
  pack already delivers, handed back to the agent as a launch argument (`${home}` is the home directory), so no
  second destination is needed.

A census test requires every pack that installs an agent and declares a briefing to declare `instructions`
(possibly with no mechanism), so a new agent pack cannot ship unclassified.

## 4. The per-agent mechanisms

Every name here has a cited source in [Appendix A](#appendix-a-evidence). Versions are the ones installed in
this jail.

| Agent | Reads from the repository | Knob | Collateral | Verdict |
| :--- | :--- | :--- | :--- | :--- |
| `claude` 2.1.331 | `AGENTS.md`, `.claude/AGENTS.md`, `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md`, `.claude/rules/**`; cwd and ancestors at start, a subdirectory's own on demand | Setting `claudeMdExcludes`, an array of absolute-path globs, at any settings layer, merged across layers; it applies to every file above | None if each glob is anchored at the workspace; `managed-only` and `CLAUDE_CODE_DISABLE_CLAUDE_MDS` would also drop the briefing | **Knob, list.** |
| `codex` 0.166.0 | `AGENTS.override.md`, `AGENTS.md`, then `project_doc_fallback_filenames`; one file per directory, project root down to cwd | Config `project_doc_max_bytes = 0` (`-c` flag at launch) stops project loading | None: the global `~/.codex/AGENTS.md` loads separately. Source read at `main`; the key is present in the 0.166.0 binary | **Knob, set.** |
| `pi` 1.6.0 | `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD` per directory, cwd and ancestors; also `.pi/SYSTEM.md` and `.pi/APPEND_SYSTEM.md` | Flag `--no-context-files` / `-nc`; no settings key or variable | Drops the agent-dir `AGENTS.md`, the briefing; `--append-system-prompt` takes a file's contents, which re-delivers it. The `.pi/` files are **not** covered | **Knob, launch flag, with `redeliver`: the briefing file handed back through `--append-system-prompt`.** |
| `copilot` 1.0.114 | `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, `.github/instructions/**`, `.claude/rules/**`, plus `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` | Flag `--no-custom-instructions`; no setting or variable found | Drops every custom-instruction source, `$HOME/.copilot/copilot-instructions.md` (the briefing) included, and no channel re-delivers it ([Appendix A](#appendix-a-evidence)) | **Unsupported.** |
| `opencode` 1.18.34 | `AGENTS.md`, `CLAUDE.md`, `CONTEXT.md`, found upward from the directory to the worktree | Only `OPENCODE_DISABLE_PROJECT_CONFIG=1`, which also drops the project's `opencode.json`, `.opencode` and TUI config | Too broad to be a knob for instruction files | **Unsupported.** |
| `agy` | `GEMINI.md`, `AGENTS.md`, walking up from cwd to the repository root | None in `--help`, the binary's settings text or its bundled docs | n/a | **Unsupported.** |
| `muse` | First of `AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md` per directory, up to the nearest `.git` | None documented; only workspace trust gates them | n/a | **Unsupported.** |
| `oh-omp` | Native formats from many tools, including `AGENTS.md` | None documented; binary not installed here | n/a | **Unsupported**, UNMEASURED. |

An agent counts as having a knob only when the knob leaves the briefing in place or the pack can redeliver it
([IF-D8](#10-decision-ledger)); copilot's cannot. The `.pi/` files are listed in pi's `reads` and named in its disclosure as
still read, so the line is truthful about what the mode did not reach.

## 5. What each mode does

| Mode | Knob agent | Unsupported agent |
| :--- | :--- | :--- |
| `keep` | Nothing is rendered or handed; the launch prints nothing about it. | The same. |
| `ignore` | The pack's mechanism is rendered or handed; the launch names it. | Nothing changes; the launch says the agent still reads the files ([§8](#8-an-agent-that-cannot-honor-the-mode)). |
| `replace` | `ignore`, then the file's text is added to the briefing as one titled section. | The text still joins the briefing; the files are still read, and the launch says so. |

The replacement joins the briefing through the user block ([the same path as `agents_md_extra`](../reference/agent-briefings.md#customizing-in-practice)),
so it lands in every destination a selected pack declares, refreshes the way the briefing does, and is
composed by `yolo host apply` for the user-scope value only. A workspace-layer replacement is never written into
the user's real home, for the reason `agents_md_extra` is not.

### 5.1 The disclosure (IF-D5)

Every launch under `ignore` or `replace` prints, per selected agent, one line in the launch stream:

```text
repo instructions: claude: ignoring 3 files (AGENTS.md, CLAUDE.md, +1 nested) through claudeMdExcludes
repo instructions: pi: ignoring AGENTS.md; still read: .pi/APPEND_SYSTEM.md
repo instructions: agy: NOT ignored, it still reads GEMINI.md, AGENTS.md; agents that can ignore them: claude, codex, pi; to stop this notice: yolo config set repo_instructions.mode keep
```

The last form is the unsupported agent's line. It names the next step: the agents that do honor the mode, and the
command that sets it back to `keep`.

- The files are counted by a bounded scan: depth at most 6, skipping `.git`, `node_modules` and the workspace's own `.yolo` state, at most 5,000
  directories. A scan that cannot finish says so and names the patterns instead.
- It is in the launch stream and `launch.log`, and `YOLO_NO_BANNER` does not hide it ([report-tiers.md](../reference/report-tiers.md)).
- `yolo config why repo_instructions.mode` names the layer that set the mode.

## 6. Per-setup behavior

The four columns are the setups the parity rule covers. `yolo host` is the unconfined path; its two verbs differ
because `yolo host apply` writes the user's one real home, shared by every workspace, so it can carry only
user-scope values.

| Mechanism | podman jail | Apple Container | macos-user | host |
| :--- | :--- | :--- | :--- | :--- |
| `ignore.list` (claude) | Rendered into the per-workspace `~/.claude/settings.json` | Same | Same, into the account's home | `yolo host -- claude` hands `--settings` JSON carrying the entries (claude merges `--settings` into the settings its exclusion reads, [Appendix A](#appendix-a-evidence)); `yolo host apply` writes none |
| `ignore.set` (codex) | Rendered into the per-workspace `~/.codex/config.toml` | Same | Same | `yolo host -- codex` hands `-c project_doc_max_bytes=0` |
| `ignore.launch` (pi) | Flag from the launcher, briefing redelivered | Same | Same | `yolo host -- pi` hands the flag and the same re-delivery argument naming the real home's briefing file |
| Unsupported agent | Reads the files; the line says so | Same | Same | Same |
| `replace` text | Briefing destinations | Same | Same | User-scope value only, by `yolo host apply`; a workspace-layer value is not delivered, and the launch says so ([§8](#8-an-agent-that-cannot-honor-the-mode)) |

The per-workspace home is why the jail cells can anchor entries at the workspace: the settings file belongs to
one workspace, so a pattern like `${workspace}/**/AGENTS.md` never matches the briefing under `/home/agent` and
never leaks to another workspace.

## 7. Why yolo never masks

An empty read-only file bound over an instruction file is the obvious way to make any agent ignore it, and yolo
does not do it, for any agent, tracked file or not ([P2](#1-what-the-owner-asked-for-and-what-stands-in-the-way),
[IF-D14](#10-decision-ledger)). Git in the jail would see an emptied file, `git checkout` and `git pull` would
fail with "Device or resource busy", and a file created later would escape the bind. The mode is therefore only
as strong as each agent's own setting.

## 8. An agent that cannot honor the mode

A selected agent cannot honor `ignore` when it is unsupported ([§4](#4-the-per-agent-mechanisms)), and a
workspace-layer `replace` cannot reach `yolo host apply`.

1. The launch **continues**. It never refuses and has no hatch to override.
2. The disclosure line for that agent says it still reads the files, names the agents that can ignore them, and
   names `yolo config set repo_instructions.mode keep` ([IF-D5](#10-decision-ledger), [IF-D15](#10-decision-ledger)).
3. The line is printed every launch and `YOLO_NO_BANNER` does not hide it.

## 9. Non-goals, and alternatives rejected

- **Not a trust or safety boundary.** `ignore` stops the agent's *loading*; it does not stop an agent that opens a
  file by hand or a repository whose hooks or skills carry instructions of their own.
- **Not skills, MCP files or `.claude/settings.json`.** Those have their own layers.
- **No per-agent overrides in v1.** One mode for every selected agent.
- **Rejected: a global switch** (`managed-only`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS`, `OPENCODE_DISABLE_PROJECT_CONFIG`).
  Each drops the briefing or unrelated project config.
- **Rejected: a mask** — an empty file bound over the path, as a primary or as a fallback, tracked or untracked
  ([P2](#1-what-the-owner-asked-for-and-what-stands-in-the-way), [§7](#7-why-yolo-never-masks)).
- **Rejected: refusing the launch for an agent that cannot honor the mode** ([IF-D15](#10-decision-ledger)).
- **Rejected: hide a mask from git** with `skip-worktree`, `assume-unchanged` or an exclude line. It writes the
  user's git state, which they ruled against for settings in general ([RC-D71](../reference/config/rulings.md#RC-D71)'s owner words: "VERY surprising when
  your own settings get changed for you").
- **Rejected: a stricter merge so a project file cannot lower the user's mode.** The config-change prompt
  already shows a project edit, and a repository writing `keep` is asking for today's behavior.

## 10. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| IF-D1 | Mechanism: each agent's own knob rendered by its pack through the config composition, at every setup; no other mechanism exists | 2026-11-24 | [§3](#3-the-decisions-this-design-rests-on), [§7](#7-why-yolo-never-masks) | — |
| IF-D2 | Scope: every instruction file a selected agent reads from the repository, root and nested, per agent, never a global filename list | 2026-11-24 | [§4](#4-the-per-agent-mechanisms) | — |
| IF-D3 | Modes `keep` (default), `ignore`, `replace` with a user-named file; replace composes through the briefing | 2026-11-24 | [§5](#5-what-each-mode-does) | — |
| IF-D4 | The user config, the per-workspace layer and the committed project file may each set it | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D5 | Every launch discloses what it hid or replaced; not suppressible | 2026-11-24 | [§5.1](#51-the-disclosure-if-d5) | — |
| IF-D6 | One config table `repo_instructions` with `mode` and `replace_with`, both scopes | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D7 | The mechanism lives in a new pack contribution `instructions` with `reads` and at most one of `ignore.list`, `ignore.set`, `ignore.launch`; core names no agent. A census test requires every agent pack to declare it. Tier 1 (structural) | 2026-11-24 | [§3.2](#32-the-new-pack-contribution) | — |
| IF-D8 | A flag or setting that also drops the user-level briefing is a knob only with a declared `redeliver`; pi redelivers the briefing file through `--append-system-prompt`; copilot's flag drops it with no way back, so copilot is unsupported; opencode's only switch is too broad | 2026-11-24 | [§4](#4-the-per-agent-mechanisms) | — |
| IF-D9 | Claude entries are anchored at `${workspace}` and added as a list, never replacing the user's own `claudeMdExcludes` | 2026-11-24 | [§3.2](#32-the-new-pack-contribution), [§6](#6-per-setup-behavior) | — |
| IF-D10 | Ordinary merge order, later layer wins; no stricter rule for the project file | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D11 | A missing or unreadable `replace_with` refuses the launch; an empty one behaves as `ignore` | 2026-11-24 | [§3.1](#31-the-config-key) | — |
| IF-D12 | The disclosure counts files with a bounded scan (depth 6, 5,000 directories, skipping `.git` and `node_modules`) and says when it could not finish | 2026-11-24 | [§5.1](#51-the-disclosure-if-d5) | — |
| IF-D13 | At the host, workspace-scoped mechanisms go through the launch (`yolo host -- <agent>`), never into the shared real home by `yolo host apply`; `replace` at the host is the user-scope value only | 2026-11-24 | [§6](#6-per-setup-behavior) | — |
| IF-D14 | Owner, on whether a mask may cover a tracked file: yolo never masks a workspace file with a mount: no empty-file bind, no replacement bind, not as a fallback, not for an untracked file. "my empty mount was a bad idea. so yes, B. let's never do that." An agent with no knob is unsupported. Tier 1 (structural: no mask code exists) | 2026-11-24 | [§7](#7-why-yolo-never-masks) | — |
| IF-D15 | Owner, on whether to refuse when an agent cannot honor the mode: when a selected agent cannot honor the mode the launch continues ("it should continue") and discloses, per agent, that it still reads the files, naming the agents that can and the way back to `keep`; never suppressible, no refusal, no hatch | 2026-11-24 | [§8](#8-an-agent-that-cannot-honor-the-mode) | — |
| IF-D16 | `ignore.host_launch`: at `yolo host --` the claude entries and the codex setting travel as launch arguments, since the unconfined path has no per-workspace file | 2026-11-24 | [§3.2](#32-the-new-pack-contribution), [§6](#6-per-setup-behavior) | — |

## 11. Open questions

None. The two questions this design opened are ruled and compacted into IF-D14 and IF-D15 of the
[ledger](#10-decision-ledger).

## 12. Test plan

Event-driven, no agent sessions; probes are `--version` and `copilot instruction list`-style listings only.

- **Config.** Validation table: modes, `replace_with` required and refused, project-layer path escape and link,
  missing file, empty file; `yolo config set|unset|why` round trip; merge order across the four layers.
- **Pack kind.** Decode and validate `instructions`; the census over every shipped agent pack
  (`TestEveryAgentPackDeclaresItsInstructionFiles`); a deleted declaration must fail it.
- **Render per mechanism.** claude `ignore` yields workspace-anchored entries and a user's own array survives;
  a glob match of every entry against the briefing's path is false and against a nested workspace file is true;
  codex argv carries `-c project_doc_max_bytes=0`; pi argv carries `-nc` and `--append-system-prompt` naming the briefing file; `keep` renders and hands nothing.
- **Replace.** The section is in every destination; `yolo host apply` composes only the user-scope value.
- **Disclosure.** The line per agent and mode, present with `YOLO_NO_BANNER` set, and in `launch.log`.
- **Unsupported agents.** The launch continues and the line names the agents that can ignore and the way back to `keep`; no assembled argv holds a bind over a workspace path.
- **Parity.** One table test names every cell of [§6](#6-per-setup-behavior), in the style of the briefing parity
  tests, and fails when a new mechanism has no cell.
- **Integration (nested jail).** A launch with the mode set to `ignore` renders claude's `claudeMdExcludes` and
  prints the disclosure. Nothing here proves reachability or rootless-only behavior.

Each test must fail if its call site is deleted.

## 13. Files a build would touch

The seed lists them with notes ([`workspace-instruction-files-plan.md`](workspace-instruction-files-plan.md)):
`internal/packdecl/packdecl.go` (the kind), `internal/packload` (validation, census),
`internal/config/schema.go`, `validate.go` and the editing verbs, `internal/render/configkeys.go` and
`internal/setupcensus/configkeys.go` (per-setup honored status), `internal/entrypoint/surfaceloop.go` (rendering),
a new disclosure file in `internal/cli/run`, `internal/jailcontent` (the replacement
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
| copilot's flag drops the personal file too | Static, from the installed 1.0.114 package (`~/.cache/copilot/pkg/linux-x64/1.0.114`); no session was started. `cli-main.js` lists `$HOME/.copilot/copilot-instructions.md` and `$HOME/.copilot/instructions/**/*.instructions.md` beside the repository files as one set of custom-instruction locations. Its `/instructions` picker groups sources as `user`, `repository`, `working-directory` and `plugin` and, under the flag, marks every source disabled and read-only ("Disabled for this session by --no-custom-instructions"). The flag becomes the session option `instructions.skipCustomInstructions`, which `copilot-sdk/types.d.ts` documents as skipping "custom-instruction sources". The loader is compiled into `prebuilds/linux-x64/runtime.node`, so the personal file's exclusion is read from the picker and the option's contract, not from loader source. No flag, setting or variable re-delivers a file under it |
| claude reads `claudeMdExcludes` from `--settings` | The 2.1.331 binary: the exclusion reads `Et().claudeMdExcludes`, the merged settings; the merge ranges over `Yo()`, which is `userSettings`, `projectSettings`, `localSettings`, `flagSettings`, `policySettings` filtered by `--setting-sources`, with `flagSettings` and `policySettings` always added. `--settings` is the `flagSettings` source ("command line arguments"), so entries handed there are read, and arrays merge across sources |
| opencode names and the broad switch | The JS in `opencode-ai` 1.18.34: names `["AGENTS.md","CLAUDE.md","CONTEXT.md"]` with `disableClaudeCodePrompt` removing `CLAUDE.md`; `OPENCODE_DISABLE_PROJECT_CONFIG` also gates `opencode.json`, `.opencode` and TUI config |
| agy rule locations and no disable setting | `agy --help`; the binary's embedded "Workspace Rules" text and env-name strings |
| muse's file order, upward walk and trust gate | [Muse Code configuration](https://dev.meta.ai/docs/muse-code/configuration); `muse` not installed here |
| oh-omp discovery and no documented disable | The project README; `oh-omp` not installed here |
| Packs deliver the briefing to the user-level files | `[[briefing]]` in `packs/claude`, `packs/pi`, `packs/codex`, `packs/copilot`, `packs/agy`, `packs/muse-code`, `packs/opencode` |
| Launch flags are a posture-only channel today | [pack-system.md](../reference/pack-system.md) `autonomy`; `packs/claude/pack.toml` |
| Apple Container honors `:ro` from 1.1.0 | `acROBindsFloor` in `internal/cli/run/backendcaps.go` |
