---
title: "Which workspace MCP files reach an in-jail agent"
date: 2026-09-22
status: in-review
tags: [mcp, workspace, packs, copilot, claude, config, shadow]
summary: "yolo does not try to keep a workspace's MCP config away from an in-jail agent — that config is the repo's or the user's own, and reaching the agent is the desired behavior. The /dev/null shadow over .vscode/mcp.json was removed on that basis on 2026-09-22. What is left is precedence: when a workspace file and yolo's canonical mcp_servers table name the same server, which one wins, and today the answer is whatever each vendor's merge order happens to be."
vantage:
  status-chip: true
---

# Which workspace MCP files reach an in-jail agent

**Status:** DESIGN, 2026-09-22 — one question open, [OQ-WM1](#OQ-WM1).
The removal in [§3](#3-the-removal-2026-09-22) is BUILT and owes no ruling; the position it rests on is
recorded in [`../plans/retired-decisions.md`](../plans/retired-decisions.md#a-jail-does-not-shadow-workspace-mcp-config).

**Needs your ruling:** [OQ-WM1](#OQ-WM1).

> **In short.** yolo composes one canonical MCP server table from the user's config and each pack
> projects it into that agent's own file. A workspace may *also* carry MCP config — a committed
> `.vscode/mcp.json`, a project `.mcp.json`, a devcontainer — and the agents that read those files
> pick them up. **That is fine and intended.** The one thing nobody has ruled is what happens when
> both name the same server, because right now the winner is decided by each vendor's merge order
> and is never disclosed.

**A sibling question with the same shape.** [§5](#5-what-this-does-not-propose) links
[`workspace-skills.md`](workspace-skills.md), which asks whether a repo's own *skills* may reach
whichever agent the reader chose. This doc is that question for MCP config, and the answer here is
simpler only because a workspace MCP file is already the mechanism the agent reads — there is no
composition to add.

## 1. The position

**yolo does not prevent a workspace's MCP config from reaching an in-jail agent, and does not want
to.** An agent finding the workspace's own MCP config is desired behavior: the file is the repo's or
the user's, and keeping it from the agent is not a goal this project has. The canonical `mcp_servers`
table is a *source*, not a *filter*.

This is stated because the code used to say the opposite. `internal/cli/run/assemble.go` bound
`/dev/null` over `<workspace>/.vscode/mcp.json` so an agent read an empty file, under a comment that
began *"Each makes the agent read an EMPTY file where the host has a real one."* That shadow was
removed on 2026-09-22 ([§3](#3-the-removal-2026-09-22)); the decision not to re-add it is in
[`retired-decisions.md`](../plans/retired-decisions.md#a-jail-does-not-shadow-workspace-mcp-config).

The shadow was also never the boundary it looked like. Its only in-jail reader is Copilot CLI, and
Copilot loads **three** repo-root MCP files, not one; the other two were never bound over. A single
blanked file is not a boundary, which is the second reason not to reach for that framing even if
someone later decides they do want one.

## 2. What each agent reads at project scope — measured

Checked against the agent bundles installed in a jail on 2026-09-22, version named per row. This is
evidence about those versions, not a claim about every version.

| Agent | Repo-root MCP file(s) it loads | Evidence |
|---|---|---|
| Copilot CLI `1.0.20` | `.mcp.json`, `.vscode/mcp.json`, `.devcontainer/devcontainer.json` | the loader `pW` → `jPs` in the installed bundle dispatches on those three basenames and logs `Loaded workspace MCP config from .vscode/mcp.json: N server(s)`; the interactive path calls it with `includeWorkspaceSources: true`, and folder-trust is bypassed under `--yolo`, which `packs/copilot` sets |
| Claude Code `2.1.278` | project-root `.mcp.json` | the installed binary carries `Project .mcp.json is not a regular file`, `project .mcp.json approval`, and `MCP server … already exists in .mcp.json` |
| opencode, pi, codex, agy | their own files, not these | `opencode.json`; `~/.pi/agent/mcp.json`; `~/.codex/config.toml`; `~/.gemini/…/mcp_config.json`. No project-scope reader of the three files above was found |

So the file set a jail would have to consider is **four sources across two agents**, and yolo ever
bound over exactly one of them.

## 3. The removal (2026-09-22)

`internal/cli/run/assemble.go` no longer emits `-v /dev/null:/workspace/.vscode/mcp.json:ro`. The
`.overmind.sock` shadow is kept and is unrelated — that one is about a **route** (the jail has no way
to the host's overmind daemon, and `OVERMIND_SOCKET` points at `/tmp/overmind.sock` in-jail), not
about keeping workspace content from an agent.

Two mechanical costs, neither of them about isolation, made the removal a clear call:

- **A device node breaks git.** The bind makes the destination a character device (`1:3`), which git
  can neither hash nor `git add`. On a *tracked* `.vscode/mcp.json` — as in a repo that commits one —
  that is a permanently dirty, uncommittable path. This is the observed symptom that started the
  thread.
- **It fired on file existence, not on a reader.** Every jail whose workspace happened to contain the
  file paid that cost, whether or not any selected agent read it.

⚠ **Do not "fix" a re-add by binding an empty regular file.** That is fail-open in the one way that
matters: git would stage the empty blob, so an in-jail `git commit -a` would record an *emptied*
config. The device node fails closed, which is the property to keep if this bind ever comes back.

## 4. Open question: precedence

<a id="OQ-WM1"></a>

### 💬 [OQ-WM1](#OQ-WM1) — When a workspace file and yolo's canonical table name the same server, who wins?

**Stakes.** yolo regenerates each agent's MCP file from the canonical `mcp_servers` table every boot,
so a user who declares `mcp_servers.foo` reasonably expects `foo` to be what the agent gets. A
workspace file naming `foo` can override it, and nothing says so.

**What actually happens today, per agent — and it is vendor-specific.** Copilot's merge is
`{...user, ...workspace}` (installed `1.0.20`: `TP(t,e) = {mcpServers: {...t.mcpServers,
...e.mcpServers}}`, applied user-then-workspace), so the **workspace wins** on a name collision. Any
other agent with a project-scope file decides by its own rule. So the precedence is not yolo's to
state today; it is each vendor's, and yolo neither knows nor reports it.

**Why it is not simply "workspace wins, done".** Under [§1](#1-the-position)'s position the workspace
winning is a defensible default — the repo is the user's choice for that workspace. But yolo is the
sole author of the file it regenerates, and it has an existing rule for exactly this shape: a managed
table yolo owns is regenerated wholesale, and an entry it does not declare is dropped *with a notice*
(the `claude/config` `mcpServers` case, [`handoff-host-mcp-servers.md`](../plans/handoff-host-mcp-servers.md)).
Silently losing to a workspace file is the same surprise the notice exists to prevent.

**Leaning.** Workspace wins, but yolo **discloses** when a workspace source overrides a server the
canonical table named — one line on the launch stream, naming the server and the file. This is
cheaper than a precedence rule and matches the project's standing *never silent* discipline; it also
avoids core having to know any agent's merge order, which the pack system forbids.

**Gate.** None yet — this is a ruling nobody has made, not blocked work.

**Answer.**



## 5. What this does not propose

- **Not a pack-declared shadow**, and not coverage of all four [§2](#2-what-each-agent-reads-at-project-scope--measured)
  sources. That was the earlier candidate ("fix B") and it dissolves with [§1](#1-the-position)'s
  position: there is no isolation property to complete.
- **Not core learning tool names.** A future mechanism that did want to act per-agent would have to
  arrive as a pack declaration, exactly as [`workspace-skills.md`](workspace-skills.md) concludes for
  skills.
- **Not touching the `.overmind.sock` shadow**, which is a route concern
  ([§3](#3-the-removal-2026-09-22)).

## 6. The other direction — writes, a different concern with a different home

Removing the bind has one side effect worth naming so it is not mistaken for this doc's subject: the
bind was `:ro`, so `<workspace>/.vscode/mcp.json` is now **writable by in-jail agents** through the
read-write workspace bind.

If the concern is the *inverse* direction — a jailed agent writing a file the **host** later reads and
executes — that is the blind cell, and its home is
[`../reference/host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md#the-two-axes),
which already names `.mcp.json` in that class and whose sanctioned instrument is
`workspace_readonly` and its protected-path list, not a `/dev/null` bind. Note this is not a new
class: `.git/hooks` and root `.mcp.json` were already writable in-jail, so the shadow was one entry on
a list that was never complete.

## 7. Verification status

- **MEASURED:** the removal and the surviving `.overmind.sock` shadow, at the argv level —
  `internal/cli/run/shadowbinds_test.go` asserts exactly one `/dev/null:` bind and that no
  `.vscode/mcp.json` bind is emitted, in both directions (re-adding it and deleting the survivor each
  fail the test). It reads the assembled argv and starts no container.
- **MEASURED:** the [§2](#2-what-each-agent-reads-at-project-scope--measured) project-scope file sets,
  read out of the installed agent bundles named there.
- **UNMEASURED:** the in-jail end-to-end outcome. `integration/isolation_test.go` was updated to assert
  `.vscode/mcp.json` reads *through*, but the container-backed suite could not be run where this
  landed (the image copy hit a full disk), so the mount-namespace half has not been observed running.
  The next `just test` on a machine with room settles it.