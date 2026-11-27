---
title: "Repository instruction files — keep, ignore or replace what an agent reads from the workspace"
status: accepted
stage: CURRENT
verified: 2026-11-27
verified_commit: 33b200771
covers:
  - internal/config/repoinstructions.go
  - internal/repoinstr/
  - internal/packdecl/instructions.go
  - internal/packload/instructions.go
  - internal/packoverlay/instructions.go
  - internal/cli/run/repoinstructions.go
  - internal/cli/hostrepoinstructions.go
  - internal/entrypoint/packsurfaces.go
  - packs/claude/pack.toml
  - packs/codex/pack.toml
  - packs/pi/pack.toml
tags: [instructions, packs, briefing, config, parity]
summary: "How the repo_instructions table lets a user keep, ignore or replace the AGENTS.md, CLAUDE.md and kin a repository carries: each agent's own setting, rendered by its pack at every setup; an agent with no setting is named in a launch line and keeps reading them."
---

# Repository instruction files — keep, ignore or replace what an agent reads from the workspace

**Status:** Verified 2026-11-27 against `33b200771`. Every macOS and Apple Container cell is
**unverified**: see [Unverified on macOS](#unverified-on-macos).

A repository's instruction files are the agent's to read only because nobody told the agent not to.
One config table, `repo_instructions`, lets the user keep them (the default), **ignore** them, or
**replace** them with a file the user names. Ignoring uses each agent's own supported setting,
rendered by the agent's pack at every setup. An agent with no such setting is **unsupported**: the
launch continues and says that agent still reads the files. **yolo never masks a workspace file.**

**Instruction file** — a file an agent finds by name in the workspace and puts in its prompt:
`AGENTS.md`, `CLAUDE.md`, `CLAUDE.local.md`, `GEMINI.md`, `.github/copilot-instructions.md`,
path-scoped rule files. Not a skill, not an MCP file, and not the agent's user-level file (below).
**Mode** *(coined here)* — what a launch does about those files for the selected agents: `keep`,
`ignore`, `replace`. **Knob** — the agent's own supported way to stop reading them: a setting, a
command-line flag or an environment variable; not a mount. **Unsupported** *(coined here)* — an
agent with no knob.

**Reads with:** [`agent-briefings.md`](agent-briefings.md) (the briefing a replacement joins),
[`report-tiers.md`](report-tiers.md) (why the disclosure cannot be silenced),
[`pack-system.md`](pack-system.md#instructions) (the `instructions` pack contribution),
[`host-apply-staleness.md`](host-apply-staleness.md) (the host gate that delivers a replacement).

| Part | Lives in |
| :--- | :--- |
| The config table, its validation | `internal/config` (`repoinstructions.go`) |
| Resolution, the wire form, the scan, the disclosure | `internal/repoinstr` |
| The `instructions` kind, its validation and accessors | `internal/packdecl` (`instructions.go`) |
| The selection handed to each loaded pack; the launch-flag fold | `internal/packload` (`instructions.go`, `LaunchFlagsFor`) |
| The list and set folds into config surfaces | `internal/packoverlay` (`instructions.go`) |
| The launch: resolve once, hand to packs, disclose | `internal/cli/run` (`repoinstructions.go`) |
| The session input and the replacement text in the briefing | `internal/cli/run` (`sessioninputs.go`, `prepare.go`) |
| The jail's and macos-user's boot | `internal/entrypoint` (`packsurfaces.go`, `LoadJailPacks`) |
| The unconfined path | `internal/cli` (`hostrepoinstructions.go`) |
| Each agent's declaration | `packs/*/pack.toml`, `[[instructions]]` |

---

## Principles

**P1. The agent's own user-level instruction file is where yolo puts its briefing.** The packs'
briefing destinations are the agents' user-level files (for example `~/.claude/CLAUDE.md`). A switch
that turns off *all* instruction loading also turns off the briefing, the one text a jail's agent
must read. So a knob counts only if it leaves the briefing in place or the pack can hand it back
(`redeliver`, [below](#the-instructions-contribution)).

**P2. yolo never masks a workspace file.** A file bound over `/workspace/AGENTS.md` reads as empty to
git in the jail, so `git commit -a` records an emptied upstream file, and the bind blocks the
unlink-and-rename that `git checkout` and `git pull` use ("Device or resource busy"); a file created
later would escape the bind. yolo cannot hide the mask from git without writing the user's git state,
which it never does. So no mode ever binds, replaces or hides a workspace file, for any agent, tracked
or not, as a primary mechanism or a fallback. The mode is only as strong as each agent's own setting.

> [!WARNING]
> **There is no mask, and no kind will ever carry one.** Do not add a bind, an empty-file overlay, or
> a `skip-worktree`, `assume-unchanged` or exclude line to make an unsupported agent ignore a file.

**P3. Core names no agent.** The mechanism is a pack contribution, rendered through the config
folds that already exist, so every setup gets it (the parity rule) and no engine code learns what an
instruction file is.

**P4. A launch never refuses for an agent that cannot honor the mode.** It continues, and says so.
There is no hatch to override it because there is nothing to override.

**P5. Not a trust or safety boundary.** `ignore` stops the agent's *loading*. It does not stop an
agent that opens a file by hand, or a repository whose hooks or skills carry instructions of their
own. Skills, MCP files and `.claude/settings.json` have their own layers.

## The config key

One table, settable in both scopes, spelled so `yolo config set repo_instructions.mode ignore` works
like any key ([editing.md](config/editing.md)):

```toml
[repo_instructions]
mode = "replace"                  # keep (default) | ignore | replace
replace_with = "~/rules/mine.md"  # required when mode is replace; refused otherwise
```

| Rule | Behavior |
| :--- | :--- |
| Merge | The ordinary [merge order](config/README.md#merge-order); later wins, `mode` and `replace_with` independently. A committed project file may set `ignore` or `replace` and reaches a launch only through the config-change prompt, like any project key. A project file writing `keep` is asking for today's behavior; there is no stricter merge. |
| Validation | A mode outside the three, or a non-string, is an error. `replace_with` beside `keep` or `ignore` in the same table is an error that names `yolo config unset repo_instructions.replace_with`. An unknown key is an error. |
| `replace_with`, user layers | An absolute path or `~/…`, read as the user spelled it. |
| `replace_with`, project layers | A path inside the workspace, read without following a link at any step; a link or an escape is refused naming the path. |
| Mode `replace` with no file named | The launch refuses naming `yolo config set repo_instructions.replace_with` and the `ignore` alternative. |
| Missing, unreadable or non-regular file | The launch refuses naming the path and `yolo config unset repo_instructions.replace_with`. |
| Empty or whitespace-only file | Behaves as `ignore`; no empty section is composed. |
| Size | The text counts toward the briefing's existing [size line](agent-briefings.md#budgets-and-the-size-line); yolo never trims it, and only a mistaken path is stopped by a multi-megabyte read cap. |

The table is read **on the host at launch** (its `replace_with` is a host path), so a jail neither
re-reads it nor passes it to an inner launch; the jail's boot receives only the mode, on the
per-entry session channel as `YOLO_REPO_INSTRUCTIONS` (a JSON `{"mode": …}`, empty for `keep`). A
sealed build or a capture jail applies nothing: it carries none of the user's own text.
`yolo config why repo_instructions.mode` names the layer that set the mode.

## The `instructions` contribution

`instructions` *(coined here)* is a pack contribution kind; its fields are documented where all pack
kinds are, in [`pack-system.md`](pack-system.md#instructions) and the doc comments of
`internal/packdecl/instructions.go`. It declares, for the agent the pack installs:

- **`reads`** — the workspace-relative name patterns the agent loads. It drives the disclosure.
- **At most one mechanism** under `ignore`, filled from the agent's own documentation:
  `list` (entries appended to an array in a config surface through the config-list fold, so the
  user's own entries survive; `${workspace}` is the workspace path), `set` (a value folded into a
  surface's managed layer), or `launch` (flags handed to the program at launch). No mechanism at all
  means the agent is unsupported.
- **`host_launch`** — flags handed only by `yolo host -- <agent>`, in place of `list` and `set`,
  which write a per-workspace file the unconfined path does not have. It takes `${entries_json}`.
- **`still_read`** — files the mechanism does not reach, named in the disclosure.
- **`redeliver`** — for a mechanism that also drops the user-level file (P1), the briefing file the
  pack already delivers, handed back to the program as `<flag> <home>/<file>`, so no second
  destination is needed. At the host the file is handed only when it exists, because a program that
  takes "text or a file" would read a missing path as the text itself.

Validation requires `reads` and, for launch flags, the program (`bin`); refuses a second declaration
for one agent; and requires `redeliver` to sit beside `ignore.launch`. A census test
(`TestEveryAgentPackDeclaresItsInstructionFiles`) requires every pack that delivers a briefing to an
agent to declare `instructions`, possibly with no mechanism, so a new agent pack cannot ship
unclassified; a second test pins the supported and unsupported sets as a ruling.

**How a launch uses it.** The launch resolves the config once, above the dispatch, and hands the
resulting selection (mode, the workspace and home *as the agent sees them*, and whether the path is
the host) to every loaded pack. The packs' own declarations answer from it. The jail's and
macos-user's boot rebuild the selection from `YOLO_REPO_INSTRUCTIONS` and apply it to the pack tree
they load, so the same declaration renders at every setup. A `keep` launch hands nothing, so it
composes exactly as it did before the key existed.

## The agents

Which agents have a knob, and what it is, comes from the `[[instructions]]` table of each pack, not
from this page. At the time of verification: **`claude`, `codex` and `pi` have a knob; `copilot`,
`opencode`, `agy`, `muse` and `oh-omp` are unsupported.** A test pins that split.

| Agent | Knob | Why it is that |
| :--- | :--- | :--- |
| `claude` | `list`: workspace-anchored globs added to the `claudeMdExcludes` setting of the per-workspace `~/.claude/settings.json`. | The setting takes absolute-path globs at any settings layer and merges across layers. Anchoring each glob at the workspace means it never matches the briefing under the home and never leaks to another workspace. `managed-only` and `CLAUDE_CODE_DISABLE_CLAUDE_MDS` would also drop the briefing. |
| `codex` | `set`: `project_doc_max_bytes = 0` in the managed layer of `~/.codex/config.toml`. | Zero stops project loading; the global `~/.codex/AGENTS.md`, the briefing, loads separately. |
| `pi` | `launch`: `--no-context-files`, with `redeliver` handing the briefing file back through `--append-system-prompt`. | The flag also drops the agent-dir `AGENTS.md` (the briefing); `--append-system-prompt` takes a file's contents. pi's `.pi/SYSTEM.md` and `.pi/APPEND_SYSTEM.md` are **not** covered and are named by `still_read`. |
| `copilot` | none (unsupported) | Its only flag, `--no-custom-instructions`, also drops the user-level briefing and no channel re-delivers it. |
| `opencode` | none (unsupported) | Its only switch, `OPENCODE_DISABLE_PROJECT_CONFIG`, also drops the project's `opencode.json`, `.opencode` and TUI config. |
| `agy`, `muse`, `oh-omp` | none (unsupported) | No setting documented that stops them. |

The agent-side facts above were read from each agent's documentation and installed package, never by
starting an agent session; they are the premise of the declarations, so a new release of an agent
that adds a knob changes its pack's declaration and the pinned split together. A measured session
showing copilot's personal file survives `--no-custom-instructions` would promote it to supported.

## What each mode does

| Mode | Knob agent | Unsupported agent |
| :--- | :--- | :--- |
| `keep` | Nothing is rendered or handed; the launch prints nothing about it. | The same. |
| `ignore` | The pack's mechanism is rendered or handed; the launch names it. | Nothing changes; the launch says the agent still reads the files. |
| `replace` | `ignore`, then the file's text is added to the briefing as one titled section. | The text still joins the briefing; the files are still read, and the launch says so. |

The replacement joins the briefing through the user block, the same path as `agents_md_extra`
([customizing in practice](agent-briefings.md#customizing-in-practice)), so it lands in every
destination a selected pack declares and refreshes the way the briefing does. At a jail the launch
composes it from the merged config; a workspace-layer replacement is never written into the user's
real home, for the reason `agents_md_extra` is not.

### The disclosure

Every launch under `ignore` or `replace` prints, per selected agent, one line in the launch stream
(yellow, teed to `launch.log`), from a launch and from an attach. It is **never suppressible**: a
launch has no quiet mode, and `YOLO_NO_BANNER` hides the version line and nothing else
([report-tiers.md](report-tiers.md)). Shapes:

```text
repo instructions: claude: ignoring 3 files (AGENTS.md, CLAUDE.md, +1 more) through claudeMdExcludes
repo instructions: pi: ignoring 1 file (AGENTS.md) through --no-context-files; still read: .pi/SYSTEM.md, .pi/APPEND_SYSTEM.md
repo instructions: agy: NOT ignored, it still reads 1 file (AGENTS.md); agents that can ignore them: claude, codex, pi; to stop this notice: yolo config set repo_instructions.mode keep
```

A `replace` adds `; replaced by <path>` to a knob agent's line and `; the replacement text still joins
its briefing` to an unsupported agent's. The unsupported line names the next step: the agents that do
honor the mode (read from the shipped packs, never used to decide what a launch selects) and the
command that sets it back to `keep`.

The files are counted by a **bounded scan**: shallow in depth, a fixed number of directories, skipping
`.git`, `node_modules` and the workspace's own top-level `.yolo` state, following no link. A scan that
finds nothing, or cannot finish, says so and names the patterns instead of a count. The scan limits are
in [Current values](#current-values).

## Per-setup behavior

`yolo host` is the unconfined path. Its two verbs differ: `yolo host apply` writes the user's one real
home, shared by every workspace, so it carries only user-scope values; `yolo host -- <agent>` is one
launch.

| Mechanism | podman jail | Apple Container | macos-user | host |
| :--- | :--- | :--- | :--- | :--- |
| `list` (claude) | Rendered into the per-workspace `~/.claude/settings.json` | Same (unverified) | Same, into the account's home (unverified) | `yolo host -- claude` hands `--settings` JSON carrying the entries; `yolo host apply` writes none |
| `set` (codex) | Rendered into the per-workspace `~/.codex/config.toml` | Same (unverified) | Same (unverified) | `yolo host -- codex` hands `-c project_doc_max_bytes=0` |
| `launch` (pi) | Flag from the launcher, briefing redelivered | Same (unverified) | Same (unverified) | `yolo host -- pi` hands the flag and the redelivery argument naming the real home's briefing file |
| Unsupported agent | Reads the files; the line says so | Same | Same | Same |
| `replace` text | Briefing destinations | Same (unverified) | Same (unverified) | User-scope value only; see below |

The per-workspace home is why the jail cells can anchor entries at the workspace: the settings file
belongs to one workspace.

**At the host**, `yolo host -- <agent>` reads the **user scope's** mode alone (the host composition
draws that boundary for every setting), hands the loaded packs a selection marked as the host, and
prints the line for the agent being launched only. Its replacement text reaches the briefing through
the launch's own auto-apply (`host_apply_on_launch`, [host-apply-staleness.md](host-apply-staleness.md)),
so the user never runs `yolo host apply` by hand. That gate renders everything except one question: a
dropped pack's leftovers waiting for a retire answer no longer stop the render (declining the retire
keeps every path), and the launch says the retire is still waiting. `yolo host apply` composes the
user scope's replacement into the briefing and writes no per-workspace setting into the shared home;
an unreadable replacement file is said and the apply goes on with the plain `agents_md_extra`.

A workspace-layer value never reaches the host path, and the host says so. `yolo host --` and
`yolo host apply` read only the user scope, so a `repo_instructions` set in the project or
per-workspace file would be inert there. When that value differs from the user scope's, `yolo host --
<agent>` (for an agent that declares what it reads) and `yolo host apply` print one line naming the
workspace's value and the user-scope command that would make the host follow it
(`yolo config set repo_instructions.mode <mode> --user`, plus `replace_with` when set). The line is
printed whatever the user scope's mode is, and a workspace value equal to the user scope's is silent
(`internal/cli/hostworkspacerepoinstructions.go`).

## Unverified on macOS

The Linux podman path is covered by unit tests and by a nested-jail integration check that the mode
renders and prints the disclosure. A nested jail proves nothing about reachability or rootless-only
behavior. These have **not been observed** and wait on the owner's Mac:

- **Every macOS cell** of the table above (macos-user and Apple Container): that the per-workspace
  settings render in the account's home and the guest's boot applies the mode it receives on the
  session channel.
- **`yolo host -- claude` on a Mac**: that claude reads `claudeMdExcludes` from `--settings` and
  drops the files. (It was measured on a Linux host.)
- **A real agent session honoring any setting inside a jail** under its own credentials: the
  nested-jail check showed the rendered settings and the printed lines, not an agent's behavior. On
  a Linux host, one-shot print-mode probes showed claude ignoring and replacing through
  `yolo host --`, and pi ignoring and replacing; codex was not probed.

The Mac-waiting group of [the roadmap](../plans/roadmap.md#waiting-on-a-mac-ci-a-rootless-host-or-an-outside-account)
lists them.

## Current values

Verified at `33b200771`.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Scan depth, directory count | 6 levels, 5,000 directories | `repoinstr` (`scanMaxDepth`, `scanMaxDirs`) |
| Scan skips | `.git`, `node_modules`, the workspace's top-level `.yolo` | `repoinstr.ScanWorkspace` |
| Replacement file read cap | 4 MiB | `repoinstr` (`maxReplaceBytes`) |
| Session input | `YOLO_REPO_INSTRUCTIONS` | `repoinstr.EnvName` |
| Modes | `keep` (default), `ignore`, `replace` | `internal/config` (`RepoInstructionsMode*`) |
| Claude's entries | `${workspace}/**/{AGENTS.md,CLAUDE.md,CLAUDE.local.md,.claude/rules/**}` | `packs/claude/pack.toml` |
| Codex's setting | `project_doc_max_bytes = 0` | `packs/codex/pack.toml` |
| Pi's flag and redelivery | `--no-context-files`; `--append-system-prompt <home>/.pi/agent/AGENTS.md` | `packs/pi/pack.toml` |

## Why it's this way

Rulings a maintainer reading only the text above might undo. The ids are cited from code comments.

| ID | Ruling |
| :--- | :--- |
| IF-D1 | The mechanism is each agent's own knob, rendered by its pack through the config composition at every setup; no other mechanism exists. |
| IF-D2 | Scope is every instruction file a selected agent reads from the repository, root and nested, per agent; never a global filename list. |
| IF-D4 | The user config, the per-workspace layer and the committed project file may each set the mode. |
| IF-D5 | Every launch discloses what it hid or replaced, and which selected agents still read the files; not suppressible. |
| IF-D7 | The mechanism lives in the pack contribution `instructions`, with `reads` and at most one of `ignore.list`, `ignore.set`, `ignore.launch`; core names no agent; a census test requires every agent pack to declare it. |
| IF-D8 | A flag or setting that also drops the user-level briefing is a knob only with a declared `redeliver`: pi redelivers; copilot's flag drops it with no way back, so copilot is unsupported; opencode's only switch is too broad. |
| IF-D9 | Claude's entries are anchored at `${workspace}` and added as a list, never replacing the user's own `claudeMdExcludes`. |
| IF-D10 | Ordinary merge order, later layer wins; no stricter rule so a project file may not lower the user's mode (the config-change prompt already shows a project edit). |
| IF-D11 | A missing or unreadable `replace_with` refuses the launch; an empty one behaves as `ignore`. |
| IF-D12 | The disclosure counts files with a bounded scan and says when it could not finish. |
| IF-D13 | At the host, workspace-scoped mechanisms go through the launch (`yolo host -- <agent>`), never into the shared real home by `yolo host apply`; the host reads the user scope's mode alone, and `replace` at the host is the user-scope value only. The replacement reaches the briefing through the launch's own auto-apply, so the user never runs `yolo host apply` by hand; that auto-apply renders everything but a waiting dropped-pack retire. No per-launch flag carries the text. |
| IF-D14 | Owner: yolo never masks a workspace file with a mount: no empty-file bind, no replacement bind, not as a fallback, not for an untracked file ("my empty mount was a bad idea. so yes, B. let's never do that."). An agent with no knob is unsupported. Hiding a mask from git is rejected too, since it writes the user's git state ([RC-D71](config/rulings.md#RC-D71)). |
| IF-D15 | Owner: when a selected agent cannot honor the mode the launch continues ("it should continue") and discloses, per agent, that it still reads the files, naming the agents that can and the way back to `keep`; never suppressible, no refusal, no hatch. |
| IF-D16 | `ignore.host_launch`: at `yolo host --` the claude entries and the codex setting travel as launch arguments, since the unconfined path has no per-workspace file. |
