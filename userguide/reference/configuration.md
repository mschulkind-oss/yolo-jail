# Configuration

## Configuration

You describe your agent's environment (its packs and agents, packages, MCP and LSP servers, network, and which runtime confines it) in JSONC (JSON with comments) files:

| File | Scope | Purpose |
|------|-------|---------|
| `yolo-jail.jsonc` | Workspace | Per-project settings |
| `yolo-jail.local.jsonc` | Workspace (untracked) | Per-machine overrides, auto-merged over `yolo-jail.jsonc` when present — gitignore it (a global gitignore entry works well) |
| `~/.config/yolo-jail/config.jsonc` | User | Global defaults for all projects |

**Merge rules:** Workspace config merges over user defaults, and `yolo-jail.local.jsonc` merges over the workspace config. Lists are merged and deduplicated; scalars and objects in later layers override earlier values.

### Minimal Example

```jsonc
{
  "runtime": "podman",
  "packages": ["postgresql", "redis"],
  "mcp_presets": ["chrome-devtools"]
}
```

### Full Example

```jsonc
{
  // Packs. Nothing is active by default, so without this key the jail
  // starts with no coding agent in it and the launch says so.
  "packs": ["claude"],

  // Runtime: "podman", "container" (Apple Container), or "macos-user"
  // (a sandboxed native macOS process — no container, and never auto-selected)
  "runtime": "podman",

  // Extra nix packages baked into the image
  "packages": ["postgresql", "htop", "strace"],

  // Network configuration
  "network": {
    "mode": "bridge",
    "ports": ["8000:8000", "3000:3000"],
    "forward_host_ports": [5432, 6379]
  },

  // Security settings
  "security": {
    "blocked_tools": [
      {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
      {"name": "find", "message": "Use fd"},
      "curl"
    ]
  },

  // Extra read-only mounts
  "mounts": ["~/code/shared-lib"],

  // MCP presets (opt-in)
  "mcp_presets": ["chrome-devtools", "sequential-thinking"],

  // Custom MCP servers
  "mcp_servers": {
    "my-custom": {
      "command": "/workspace/scripts/my-mcp-server.py",
      "args": []
    }
  },

  // Extra tools via mise
  "mise_tools": {"neovim": "stable", "typst": "latest"},

  // Additional LSP servers
  "lsp_servers": {
    "rust": {
      "command": "rust-analyzer",
      "args": [],
      "fileExtensions": {".rs": "rust"}
    }
  }
}
```

Run `yolo config-ref` for the complete field reference.

### Gateway providers and curated models

OpenRouter and Kilo are opt-in packs. They declare one endpoint and credential
variable each, but deliberately ship no model list: gateway catalogs change too
quickly for yolo to choose models for you. Put a finite alias-to-model-id map in
your **user** config, then have profiles select the aliases you want as defaults:

```jsonc
{
  "packs": ["openrouter", "kilo"],
  "providers": {
    "openrouter": {
      "models": {
        "coding": "~anthropic/claude-sonnet-latest",
        "reasoning": "~openai/gpt-latest"
      }
    },
    "kilo": {
      "models": { "economy": "kilo-auto/efficient" }
    }
  },
  "profiles": {
    "router-coding": { "provider": "openrouter", "model": "coding" },
    "kilo-economy": { "provider": "kilo", "model": "economy" }
  },
  "use_profiles": {
    "claude": "router-coding",
    "pi": "kilo-economy"
  }
}
```

Put `OPENROUTER_API_KEY` and `KILO_API_KEY` in an existing `env_sources` file; never
put a key value in the JSONC file. Each key reaches only the agents whose profile
selects its provider: here `OPENROUTER_API_KEY` reaches Claude and `KILO_API_KEY`
reaches Pi, and a plain shell in the jail sees neither. The launch lists which keys
went where, and which it kept from every agent because no profile selected their
provider. A key exported only in the shell you launch from is not listed, and reaches
only an agent whose pack builds its provider settings from it, such as Claude's
token. OpenRouter
works directly with Claude, Codex, Pi, OpenCode, and Copilot. Kilo works with
Claude and Copilot through yolo's local wire bridge, and directly with Pi and
OpenCode; it is not offered to Codex because Kilo documents Chat Completions,
not the Responses API Codex requires.

---

## Config Safety

When `yolo-jail.jsonc` changes between jail startups, the CLI shows a normalized diff and asks for confirmation:

```
Config has changed since last confirmed session.
Diff:
  + "packages": ["postgresql"]

Accept this config? [y/N]:
```

This prevents agents from silently adding packages, mounts, or devices. The human must approve every change.

**The record of what you approved lives on the host**, at
`~/.local/share/yolo-jail/approvals/<container-name>.json`. It is deliberately *not* in the
workspace: `/workspace` is bind-mounted read-write, so anything that can edit `yolo-jail.jsonc`
could also have rewritten a baseline kept in there, and the next launch would have had nothing to
show you. A workspace copied or moved to a new path loses its baseline and re-prompts once.

**A launch with no terminal (CI, `yolo … < /dev/null`, a script) is REFUSED when the config
changed** — it is not auto-accepted. The refusal prints the diff and tells you the flag:

```console
$ yolo --accept-config-changes -- ./ci-task.sh
```

That approves the change for **that launch only** and records it exactly as answering `y` does. It
is a flag rather than an environment variable on purpose: `YOLO_ALLOW_*` variables suppress a
*diagnosis*, this one grants an *approval*, and an approval must not be inherited by every child
process or linger in a shell for the rest of a session.

### Workflow for Config Changes

**From outside the jail (handoff to agent):**
1. Edit `yolo-jail.jsonc`
2. Run `yolo check` to validate
3. Fix any errors
4. Run `yolo` to start the jail (will see diff and prompt for approval)

**From inside the jail (agent edits mid-session):**
1. Agent edits `yolo-jail.jsonc`
2. Agent runs `yolo check --no-build` for fast validation
3. Agent fixes any reported problems
4. Agent asks human to restart: _"I've updated the config. Please restart the jail."_
5. Human exits and runs `yolo` again (sees diff, approves)

An agent can confirm at any point whether a restart is actually needed with
`yolo config drift`: it compares the workspace config on disk against the one the
running jail was started with, and exits `0` if they match, `3` if they differ
(printing the diff), or `4` if it cannot tell (no baseline). Because the config the
jail is *running under* is fixed until a restart, this is how an agent knows its
edit has not yet taken effect. To see the full effective config the jail is running
under — the merged, canonicalized form — use `yolo config dump`.

See [../reference/config-safety.md](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/config-safety.md) for the full workflow.

---

### After you edit your config

When your config has changed since the last launch — `yolo-jail.jsonc`, `yolo-jail.local.jsonc`, or a
file either one includes — the next launch shows the diff and asks y/N before using it. A workspace's
first launch with a non-empty config counts as a change.

**Scripts, CI, cron jobs and editor tasks cannot answer that question.** With no terminal to ask on,
the launch stops instead, printing the diff and the files involved. Pass `--accept-config-changes` to
approve it for that one launch; it is a flag rather than an environment variable, so an approval never
carries over to a later launch. Launching once in a terminal after each edit avoids the problem. yolo
asks only when its input is a terminal, so `yolo | tee log` still asks and `yolo < /dev/null` stops.

**A running jail does not pick up your edits.** Running `yolo` in a workspace whose jail is already
running joins that jail, and joining does not ask, and does not apply your edit. Resources, mounts and
network settings are fixed when a jail starts, so stop the jail and launch again. `macos-user` has no
running jail to join — every launch starts a fresh sandbox and reads the config again.
