# Agent briefings — how AGENTS.md and CLAUDE.md are handled

**Status:** REFERENCE — describes shipped behaviour, **substantially corrected
2026-08-23**. This doc had rotted further than any of its siblings: **every
source-of-truth pointer in it named a Python file, and the repo has zero tracked
`.py` files** (`git ls-files '*.py'` → 0, verified 2026-08-23). Spot-verified and
fixed below: the generation and refresh entry points (`jailcontent.BriefingContent`
at `internal/jailcontent/briefing.go:208`; `refreshJailBriefings` at
`internal/cli/run/prepare.go:29`, called from `run.go:343`); the skills mirror
(`jailcontent.PrepareSkills`, `internal/jailcontent/skills.go:87`); the
**retirement of the `agents` config key** (`internal/config/validate.go:189-201`);
and the **removal of `gemini` as an agent** (`internal/entrypoint/env.go:280-283`).
**Not verified:** the jail-managed briefing's exact section emission order (§2
below), the host-symlink behaviours empirically checked in 2026-07, and the
changelog at the foot.

> [!WARNING]
> **Do not trust any `src/…py` path, `_underscore_function`, or `gemini` mention
> that survives elsewhere in this doc's prose.** The Python reference
> implementation is gone entirely, and so is the gemini agent. Where this doc
> still shows an old name it is because the *behaviour* is what mattered and the
> Go replacement is named beside it.

**Audience:** anyone wondering where the text an in-jail agent reads at
session start comes from, why editing it in-jail fails, or how to inject
project-specific instructions.

**Source of truth:** `internal/jailcontent/briefing.go` (composition —
`BriefingContent:208`, `ComposeBriefing:425`, `PrependHostBriefing:435`,
`ComposePackBriefings:552`), `internal/jailcontent/write.go` (`WriteBriefing:71`),
and `internal/cli/run/prepare.go` (`refreshJailBriefings:29` — refresh + mounts).

## The two layers

Agents read instruction files at two levels, and yolo treats them
completely differently:

| Layer | In-jail path | Who owns it | Yolo's role |
|---|---|---|---|
| **User-level briefing** | one per `briefing` contribution — `~/.claude/CLAUDE.md`, `~/.copilot/copilot-instructions.md`, `~/.codex/AGENTS.md`, `~/.config/opencode/AGENTS.md`, `~/.pi/agent/AGENTS.md`, `~/.gemini/config/AGENTS.md` (agy) | yolo (generated) | Generated per jail, mounted read-only |
| **Project-level file** | `/workspace/AGENTS.md`, `/workspace/CLAUDE.md` | the repository | None — it's just a file in the workspace bind, exactly what the repo checked in |

Yolo never writes, rewrites, or merges the project-level files. Everything
below is about the user-level layer.

### ⚠ Retracted 2026-08-23: "the `agents` config drives this"

This doc used to say *"Which agents get a briefing is driven by the `agents`
config (default `["claude"]`) … from the agent registry
(`src/entrypoint/agent_registry.py`, `BriefingSpec`)."* **Both halves are dead.**

- **`agents` is a REMOVED config key that hard-errors on the host**
  (`internal/config/validate.go:189-201`, verified 2026-08-23). Its message:
  *"config.agents: REMOVED — which agents a jail gets is no longer a config key of
  its own. An agent arrives as a pack, so name the pack that installs it in
  `packs` instead."* In-jail it downgrades to a warning, because the in-jail
  config is the host-generated snapshot.
- **There is no agent registry.** `internal/agents` was deleted and its
  non-per-agent remainder renamed `internal/jailcontent`. **Agents are packs.**

**What drives it now:** each pack's `briefing` contribution names its own `into`
destination, and only *selected* packs stage. Nothing is active by default — an
empty `packs` yields a jail with no agent and no briefing. Two packs may name one
`into` (an agent pack plus a house-rules pack); first writer wins the mount, since
podman rejects a duplicate mount destination. See
[`pack-system.md`](pack-system.md) §3 (`briefing` kind) and
[`jail-home.md`](jail-home.md) §2.9.

## What the generated briefing contains

`jailcontent.BriefingContent` (`internal/jailcontent/briefing.go:208`) composes
each file from three parts, in order:

1. **The host user's own briefing, prepended.** Driven by the pack's
   `briefing` contribution `after: "host:<path>"` field (not a `BriefingSpec`
   any more) — e.g. `after: "host:.claude/CLAUDE.md"`. If that host file
   exists, its content comes first, separated from the jail part by a `---`
   rule (`PrependHostBriefing`, `internal/jailcontent/briefing.go:435`). This is
   how the user's global instructions (commit rules, skill architecture, tool
   preferences) reach every jail. Note the mapping is filename-exact: Claude
   reads `CLAUDE.md`, the others read `AGENTS.md`; variants like
   `CLAUDE.local.md` are not picked up.

   > [!WARNING]
   > `after: "host:…"` is **origin-gated and JAIL-ONLY**. A *fetched* pack may
   > declare it but the grant is honored only if the user approved it at `yolo
   > pack install`; and at the `yolo host apply` notch the path it names IS the
   > generated destination, so the host render ignores it outright. See
   > [`pack-system.md`](pack-system.md) §3 and §9.
2. **The jail-managed briefing** — one `# YOLO Jail Environment` document
   describing this specific jail, deliberately limited to what an agent
   *cannot* discover through its own native mechanisms, with inline
   manuals replaced by pointers (`yolo --help`, `yolo config-ref`,
   `yolo-cglimit --help`) and conditional sections that appear only when
   their data exists. Emission order: ⚠ Provisioning failed (conditional:
   only when `/workspace/.yolo/startup.log` contains `PROVISIONING
   FAILED` — refreshed every invocation, so it appears on the next attach
   after a failed boot) → Handoff (conditional: only when a fresh
   `.yolo/handover.md` is carried this launch; there is deliberately NO
   standing counterpart line for the no-handoff case, because an
   always-present line moves the bytes
   `TestBriefingJailHeaderIsUnchanged` pins —
   host-to-jail-handoff.md §9a) → Environment
   (workspace, home, network,
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
   inventory, no MCP listing (agents read their own generated config), and
   the handoff is a conditional section, not a skill — see
   host-to-jail-handoff.md.
3. **`agents_md_extra`**, appended verbatim — the config key
   (`yolo-jail.jsonc`, user- or workspace-level; string) for injecting
   arbitrary extra instructions into every generated briefing.

The same jail content goes to every briefing destination; only the prepended
host file (and each pack's own prose) differs per destination.

## Where the files live and how they get into the jail

Generated files land host-side in `AGENTS_DIR/<container-name>/`
(`~/.local/share/yolo-jail/agents/<cname>/`), one staging file **per `briefing`
contribution**, named `briefing-<pack>.md` (`briefingStagingName`), then
bind-mounted **read-only** into the jail at that contribution's `into`:

```
AGENTS_DIR/<cname>/briefing-claude.md    →  /home/agent/.claude/CLAUDE.md:ro
AGENTS_DIR/<cname>/briefing-copilot.md   →  /home/agent/.copilot/copilot-instructions.md:ro
AGENTS_DIR/<cname>/briefing-codex.md     →  /home/agent/.codex/AGENTS.md:ro
AGENTS_DIR/<cname>/briefing-opencode.md  →  /home/agent/.config/opencode/AGENTS.md:ro
AGENTS_DIR/<cname>/briefing-pi.md        →  /home/agent/.pi/agent/AGENTS.md:ro
AGENTS_DIR/<cname>/briefing-agy.md       →  /home/agent/.gemini/config/AGENTS.md:ro
```

> [!WARNING]
> **The old per-agent staging names (`CLAUDE.md`, `AGENTS-copilot.md`, …) and the
> `~/.gemini/AGENTS.md` destination are both gone** (corrected 2026-08-23). Names
> are keyed on the *pack*, not the agent, because two packs may brief one
> destination. And `~/.gemini/` is now **agy's** tree — the gemini agent was
> removed, and agy keeps its state one level down under `antigravity-cli/` so the
> two never collided (`internal/entrypoint/env.go:280-290`).

The read-only mount is why an in-jail agent gets `Read-only file system`
if it tries to edit its own briefing — that's kernel-enforced and
intentional. On Apple Container, single-file mounts under `/home/agent`
trip apple/container#1089, so the files are materialized under the workspace
state dir instead (`acMaterialize`); same content, different plumbing.

Skills ride the same staging area, per `skills` contribution:
`jailcontent.PrepareSkills` (`internal/jailcontent/skills.go:87`) mirrors each
host-side `~/.<agent>/skills/` into `AGENTS_DIR/<cname>/skills-<pack>/`
(`SkillStagingName`, `skills.go:77`) — plus the built-in skills
(`configuring-the-jail`, `diagnosing-the-jail`, and source-tree-only
`developing-yolo-jail`) — and mounts each at the contribution's `into`,
read-only. Staging is **rebuilt every invocation**, clearing contents in place.
No cross-agent merging: precedence is built-in < pack < the user's own tree.

## Refresh semantics — live jails see host edits

`refreshJailBriefings` (`internal/cli/run/prepare.go:29`, called from
`internal/cli/run/run.go:343`) runs on **every** `yolo` invocation — fresh
launch *and* attach-to-running — so editing your host `~/.claude/CLAUDE.md`
(or skills, or `agents_md_extra`) propagates to an already-running jail
the next time you run any `yolo` command against it.

This works only because the refresh preserves inodes: the write truncates the
existing file in place (a file→file bind mount is pinned to the inode it
captured at container start), and the skills refresh clears *inside* the staged
dirs rather than recreating them. If either write path ever switches to
unlink-and-recreate, running jails silently stop seeing refreshes — treat the
in-place rule as load-bearing. The general form of it is
[`jail-home.md`](jail-home.md) §7 gotcha 1 (`WriteInPlace`,
`internal/entrypoint/fsx.go:35-40`).

Generation happens early in the run pipeline, **before** the container exists
and before provisioning runs. That's why the provisioning-failure signal is a
*pointer* (the Startup Log section directing agents to
`/workspace/.yolo/startup.log`) rather than an inline error flag: the
briefing is written before any failure can have happened, and the `:ro`
mount means nothing in-jail can append to it afterward.

## No cross-agent copying — sharing is the user's choice, via host symlinks

Nothing in yolo ever copies briefing or skill content *between* agents.
Each agent is sourced strictly in parallel from its own host dotdir:

```
~/.copilot/copilot-instructions.md  →  copilot briefing only
~/.codex/AGENTS.md     →  codex briefing only
~/.claude/CLAUDE.md    →  claude briefing only
~/.<agent>/skills/     →  that agent's skills only (no merging)
```

Cross-agent sharing is deliberately left to the user, with **host-side
symlinks** — the generation-time reads follow them (verified
empirically 2026-07-03; **the mechanism was NOT re-verified 2026-08-23**, and the
examples below were written when `gemini` was still an agent — read them as
*shape*, substituting any live agent for gemini):

- A file symlink works: `~/.codex/AGENTS.md → ~/.claude/CLAUDE.md`
  gives codex the Claude briefing content.
- A broken symlink degrades cleanly: the agent gets the jail-managed
  content only, no error.
- Skill-dir entries may be symlinks too — `PrepareSkills` copies host
  `~/.<agent>/skills/*` **dereferencing symlinks**.
- **Caveat — whole-dotdir symlinks share skills but not the briefing:**
  with `~/.codex → ~/.claude`, codex's skills resolve fine, but its
  briefing lookup becomes `~/.claude/AGENTS.md`, which doesn't exist
  (Claude's file is named `CLAUDE.md`). To share everything via a dotdir
  symlink, also add `~/.claude/AGENTS.md → CLAUDE.md` inside the target
  dir.

> [!WARNING]
> **Superseded in part at the HOST notch.** `yolo host apply` composes skills and
> briefings **wholesale**, moving the user's own prose and skills into the local
> pack (`~/.config/yolo-jail/local/`) and regenerating every destination from
> there. Under that model the symlink recipe above is the *jail-notch* story;
> at the host the one-copy-in-the-local-pack is the sharing mechanism, and it
> exists precisely because per-agent copies drift. See
> [`pack-system.md`](pack-system.md) §14.

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
- **One session / handover to the next agent:** write `.yolo/handover.md`
  in the workspace — it's surfaced as a **Handoff** section in the next
  launch's briefing and consumed once that briefing is written
  (host-to-jail-handoff.md). Consumption is announced on stderr with the
  `mv` that undoes it, because core cannot tell `yolo -- claude` from
  `yolo -- bash` and either one carries the handoff away (§9c).

## Gotchas

- The briefing describes the jail as configured *at generation time*;
  config edits mid-session refresh it on the next `yolo` invocation, but
  the running container's actual mounts/limits don't change until
  restart — the text can be ahead of reality.
- In-jail skill directories are read-only by the same mechanism; skill
  development happens in `/workspace/.claude/skills/` (or the agent's
  equivalent) and gets promoted host-side.
- `~/.claude/CLAUDE.md` prepending is unrelated to the host settings
  file — the yolo-declared `settings.json` is composed into
  `~/.claude/settings.json`, not briefings.

  > [!WARNING]
  > **`agents.AgentSpec.HostFiles` no longer exists** (corrected 2026-08-23).
  > That fixed per-agent Go constant died with the registry. The grant is now a
  > pack's `reads-host` contribution, and **the gate is ORIGIN, not a baked
  > list**: `HonoredHostFiles` honors it for an embedded or local pack and
  > refuses a fetched one outright. `claude` and `pi` each declare just
  > `settings.json`. See [`jail-home.md`](jail-home.md) §2.9.

<!-- changelog -->
<!-- NOTE 2026-08-23: entries below are HISTORY, not current state. They name
     Python files and an `agents` config key that no longer exist. Kept because
     they record why each section says what it says. -->
- **2026-08-23 audit:** all Python source-of-truth pointers replaced with their Go
  equivalents; the `agents` config key retracted (it is now a hard error naming
  `packs`); `gemini` removed as an agent (`~/.gemini/` is agy's tree now); staging
  filenames corrected to `briefing-<pack>.md`
- The Handoff section has no standing counterpart line, and its pointer is consumed only once a briefing has actually been written — a jail with no briefing destination leaves it fresh (host-to-jail-handoff.md §9)
- Deleted the `jail-startup` skill; the one-time host→jail handoff is now a conditional **Handoff** section in the briefing, consumed by the run pipeline on the launch that reads it (host-to-jail-handoff.md)
- Agent library model: briefings/skills are now generated only for the agents selected in the `agents` config (default claude), driven by the agent registry (`src/entrypoint/agent_registry.py`); added opencode + pi
- [8e08ea37] Removed the MCP-server listing from the generated briefing (agents read their own generated config) and dropped the mcp_servers/mcp_presets plumbing from generate_agents_md
- [89dc5579] Slimmed the Skills section to the one non-discoverable fact: user-level skill dirs read-only in-jail, workspace-level writable, promote via the host
- [a6cc1e7c] Deleted the First Session — Handover section; the staged jail-startup skill's own description already drives invocation
- [5774c1d9] Made "Testing Changes to yolo-jail" conditional on the workspace being a yolo-jail source tree (predicate moved to agents_md.py), and updated its text for the live /opt/yolo-jail mount
