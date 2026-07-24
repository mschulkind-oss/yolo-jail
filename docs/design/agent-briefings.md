# Agent briefings — how AGENTS.md and CLAUDE.md are handled

**Audience:** anyone wondering where the text an in-jail agent reads at
session start comes from, why editing it in-jail fails, or how to inject
project-specific instructions.
**Source of truth:** `src/cli/agents_md.py` (generation),
`src/cli/run_cmd.py` `_refresh_jail_briefings` (refresh + mounts).

## The two layers

Agents read instruction files at two levels, and yolo treats them
completely differently:

| Layer | In-jail path | Who owns it | Yolo's role |
|---|---|---|---|
| **User-level briefing** | one per selected agent — `~/.claude/CLAUDE.md`, `~/.copilot/AGENTS.md`, `~/.gemini/AGENTS.md`, `~/.config/opencode/AGENTS.md`, `~/.pi/agent/AGENTS.md` | yolo (generated) | Generated per jail, mounted read-only |
| **Project-level file** | `/workspace/AGENTS.md`, `/workspace/CLAUDE.md` | the repository | None — it's just a file in the workspace bind, exactly what the repo checked in |

Yolo never writes, rewrites, or merges the project-level files. Everything
below is about the user-level layer.

Which agents get a briefing is driven by the `agents` config (default
`["claude"]`) — only the selected agents' files are generated and mounted.
Each agent's staging filename, in-jail mount path, and host-file source
come from the agent registry (`src/entrypoint/agent_registry.py`,
`BriefingSpec`).

## What the generated briefing contains

`generate_agents_md()` (src/cli/agents_md.py) composes each file from
three parts, in order:

1. **The host user's own briefing, prepended.** If the agent's host
   source file (each spec's `briefing.host_source` — e.g.
   `~/.claude/CLAUDE.md`, `~/.copilot/AGENTS.md`, `~/.gemini/AGENTS.md`,
   `~/.config/opencode/AGENTS.md`, `~/.pi/agent/AGENTS.md`) exists on the
   host, its content comes first, separated from the jail part by a `---`
   rule. This is how the user's global instructions (commit rules, skill
   architecture, tool preferences) reach every jail. Note the mapping is
   filename-exact: Claude reads `CLAUDE.md`, the others read `AGENTS.md`;
   variants like `CLAUDE.local.md` are not picked up.
2. **The jail-managed briefing** — one `# YOLO Jail Environment` document
   describing this specific jail, deliberately limited to what an agent
   *cannot* discover through its own native mechanisms, with inline
   manuals replaced by pointers (`yolo --help`, `yolo config-ref`,
   `yolo-cglimit --help`) and conditional sections that appear only when
   their data exists. Emission order: ⚠ Provisioning failed (conditional:
   only when `/workspace/.yolo/startup.log` contains `PROVISIONING
   FAILED` — refreshed every invocation, so it appears on the next attach
   after a failed boot) → Environment (workspace, home, network,
   forwarded ports, and the *configured* resource limits with a
   `yolo-cglimit` pointer — nothing when none are set) → the rg
   `--replace` trap warning → Loopholes (conditional: the actual enabled
   set by name, not an instruction to enumerate) → Blocked Tools
   (conditional, from `security.blocked_tools`) → Additional Context
   Mounts (conditional, from `mounts`) → Limitations (two lines) →
   Packages & Resource Limits (edit config → `yolo check` → restart;
   reference `yolo config-ref`) → Skills (the read-only-user-level /
   writable-workspace constraint) → Testing Changes to yolo-jail
   (conditional: yolo-jail source workspaces only). There is no tool
   inventory, no MCP listing (agents read their own generated config),
   and no handover section (the staged `jail-startup` skill's description
   drives invocation).
3. **`agents_md_extra`**, appended verbatim — the config key
   (`yolo-jail.jsonc`, user- or workspace-level; string) for injecting
   arbitrary extra instructions into all three files.

The same jail content goes to every selected agent; only the prepended
host file differs per agent.

## Where the files live and how they get into the jail

Generated files land host-side in `AGENTS_DIR/<container-name>/`
(`~/.local/share/yolo-jail/agents/<cname>/`), one staging file per
selected agent (`CLAUDE.md`, `AGENTS-copilot.md`, `AGENTS-gemini.md`,
`AGENTS-opencode.md`, `AGENTS-pi.md`), then bind-mount **read-only** into
the jail at each agent's registry mount path:

```
AGENTS_DIR/<cname>/CLAUDE.md          →  /home/agent/.claude/CLAUDE.md:ro
AGENTS_DIR/<cname>/AGENTS-copilot.md  →  /home/agent/.copilot/AGENTS.md:ro
AGENTS_DIR/<cname>/AGENTS-gemini.md   →  /home/agent/.gemini/AGENTS.md:ro
AGENTS_DIR/<cname>/AGENTS-opencode.md →  /home/agent/.config/opencode/AGENTS.md:ro
AGENTS_DIR/<cname>/AGENTS-pi.md       →  /home/agent/.pi/agent/AGENTS.md:ro
```

The read-only mount is why an in-jail agent gets `Read-only file system`
if it tries to edit its own briefing — that's kernel-enforced and
intentional. On Apple Container, single-file mounts under `/home/agent`
trip apple/container#1089, so the files are materialized under `ws_state`
instead (`_ac_materialize_under_ws_state`); same content, different
plumbing.

Skills ride the same staging area, for the selected agents that have a
user-skills dir (claude/copilot/gemini; opencode and pi have none):
`_prepare_skills()` mirrors each host-side `~/.<agent>/skills/` into
`AGENTS_DIR/<cname>/skills-<agent>/` (plus the built-in `jail-startup`
skill) and mounts each at `/home/agent/.<agent>/skills:ro`. No cross-agent
merging.

## Refresh semantics — live jails see host edits

`_refresh_jail_briefings()` runs on **every** `yolo` invocation — fresh
launch *and* attach-to-running — so editing your host `~/.claude/CLAUDE.md`
(or skills, or `agents_md_extra`) propagates to an already-running jail
the next time you run any `yolo` command against it.

This works only because the refresh preserves inodes: `write_text`
truncates the existing file in place (a file→file bind mount is pinned to
the inode it captured at container start), and the skills refresh clears
*inside* the staged dirs rather than recreating them. If either write
path ever switches to unlink-and-recreate, running jails silently stop
seeing refreshes — there's a warning comment on `_refresh_jail_briefings`
to that effect; treat it as load-bearing.

Generation happens early in `run()`, **before** the container exists and
before provisioning runs. That's why the provisioning-failure signal is a
*pointer* (the Startup Log section directing agents to
`/workspace/.yolo/startup.log`) rather than an inline error flag: the
briefing is written before any failure can have happened, and the `:ro`
mount means nothing in-jail can append to it afterward.

## No cross-agent copying — sharing is the user's choice, via host symlinks

Nothing in yolo ever copies briefing or skill content *between* agents.
Each agent is sourced strictly in parallel from its own host dotdir:

```
~/.copilot/AGENTS.md   →  copilot briefing only
~/.gemini/AGENTS.md    →  gemini briefing only
~/.claude/CLAUDE.md    →  claude briefing only
~/.<agent>/skills/     →  that agent's skills only (no merging)
```

Cross-agent sharing is deliberately left to the user, with **host-side
symlinks** — the generation-time reads follow them (verified
empirically 2026-07-03):

- A file symlink works: `~/.gemini/AGENTS.md → ~/.claude/CLAUDE.md`
  gives Gemini the Claude briefing content.
- A broken symlink degrades cleanly: the agent gets the jail-managed
  content only, no error.
- Skill-dir entries may be symlinks too (`_copy_skill_subdirs` follows
  them).
- **Caveat — whole-dotdir symlinks share skills but not the briefing:**
  with `~/.gemini → ~/.claude`, Gemini's skills resolve fine, but its
  briefing lookup becomes `~/.claude/AGENTS.md`, which doesn't exist
  (Claude's file is named `CLAUDE.md`). To share everything via a dotdir
  symlink, also add `~/.claude/AGENTS.md → CLAUDE.md` inside the target
  dir.

Note the symlink is resolved on the **host at generation time** — the
in-jail file is a materialized merged copy. Retargeting a host symlink
propagates on the next `yolo` invocation like any other briefing edit.

## How to customize, in practice

- **All jails, one agent:** edit the host-level file
  (`~/.claude/CLAUDE.md` etc.) — prepended everywhere, live-refreshes.
- **All jails, all agents:** `agents_md_extra` in the user config
  (`~/.config/yolo-jail/config.jsonc`).
- **One workspace:** `agents_md_extra` in the workspace `yolo-jail.jsonc`,
  or the repo's own checked-in `/workspace/AGENTS.md` / `CLAUDE.md`
  (project layer, yolo-untouched).
- **One session / handover to the next agent:** write
  `.yolo/handover.md` in the workspace — the briefing's First Session
  section and the `jail-startup` skill route new agents to it.

## Gotchas

- The briefing describes the jail as configured *at generation time*;
  config edits mid-session refresh it on the next `yolo` invocation, but
  the running container's actual mounts/limits don't change until
  restart — the text can be ahead of reality.
- In-jail skill directories are read-only by the same mechanism; skill
  development happens in `/workspace/.claude/skills/` (or the agent's
  equivalent) and gets promoted host-side.
- `~/.claude/CLAUDE.md` prepending is unrelated to the host settings
  file — the yolo-declared `settings.json` (`agents.AgentSpec.HostFiles`)
  is composed into `~/.claude/settings.json`, not briefings.

<!-- changelog -->
- Agent library model: briefings/skills are now generated only for the agents selected in the `agents` config (default claude), driven by the agent registry (`src/entrypoint/agent_registry.py`); added opencode + pi
- [8e08ea37] Removed the MCP-server listing from the generated briefing (agents read their own generated config) and dropped the mcp_servers/mcp_presets plumbing from generate_agents_md
- [89dc5579] Slimmed the Skills section to the one non-discoverable fact: user-level skill dirs read-only in-jail, workspace-level writable, promote via the host
- [a6cc1e7c] Deleted the First Session — Handover section; the staged jail-startup skill's own description already drives invocation
- [5774c1d9] Made "Testing Changes to yolo-jail" conditional on the workspace being a yolo-jail source tree (predicate moved to agents_md.py), and updated its text for the live /opt/yolo-jail mount
