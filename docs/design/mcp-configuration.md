# MCP configuration: the node/npx wrapper, per-agent formats, and the pi gap

This doc explains three things that keep coming up:

1. **The node/npx wrapper** — what it is, why MCP servers need it, and the gap
   where *custom* servers bypass it.
2. **How MCP config flows** end-to-end and how it differs across the six agents.
3. **pi and MCP** — why pi has no MCP today, and what a "detect the adapter and
   fill it in" approach would look like.

Source of truth for the code: `src/entrypoint/agent_configs.py` +
`src/entrypoint/mcp_wrappers.py` (Python reference) and their Go ports
`internal/entrypoint/mcp.go` / `agent_configs.go` / `claude.go` / `codex.go` /
`mcp_wrappers.go`.

---

## 1. The node/npx wrapper

### What breaks without it

The jail's Node is nix-built: `/bin/node` →
`/nix/store/…-nodejs-slim-…/bin/node`. That binary is **dynamically linked** and
finds `libstdc++.so.6` (and friends) **only** through
`LD_LIBRARY_PATH=/lib:/usr/lib` — a path the image bakes into its process
environment on purpose (`flake.nix` sets it in the image `Env`; see the
`LD_LIBRARY_PATH` comments around `flake.nix:718`). The nix loader ignores the
usual `RPATH` and leans entirely on that env var (`src/entrypoint/system.py:91`).

Here's the trap: **when a coding agent spawns an MCP server as a child process,
it often sanitizes the child's environment** — building a clean env dict rather
than inheriting the parent's. That strips `LD_LIBRARY_PATH`. The MCP server then
launches `node`, which can't find `libstdc++.so.6`, and dies with:

```
node: error while loading shared libraries: libstdc++.so.6: cannot open shared object file
```

This is the failure documented in `AGENTS.md` ("Common MCP Errors").

### What the wrapper does

`generate_mcp_wrappers()` (`src/entrypoint/mcp_wrappers.py`) writes three tiny
scripts at boot into `~/.local/bin/mcp-wrappers/`:

```bash
# ~/.local/bin/mcp-wrappers/node
#!/bin/bash
export LD_LIBRARY_PATH="/lib:/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/node "$@"
```

`npx` is the same shape (`exec /bin/npx "$@"`), and there's a fatter
`chrome-devtools-mcp-wrapper` that also launches headless Chromium.

The wrapper is **self-contained on purpose**: it re-asserts `LD_LIBRARY_PATH` +
fontconfig itself, so it works *even when the agent handed it a sanitized env*.
It also `exec`s the nix binary directly (`/bin/node`, not the mise shim) to skip
mise's per-directory env resolution on every MCP cold start. Rule of thumb
(from `AGENTS.md`): wrappers must never call `npm config get` or shell out —
they set their env from `$HOME`-relative constants and `exec`. That's what makes
them robust to sanitization.

Think of the wrapper as a **thin env-repair shim in front of node**: same
`node`, but with the two env vars the nix binary needs re-attached at the last
moment before exec.

### How the wrapper is wired in — and the gap

`_load_mcp_servers()` defines the two built-in **presets** with the wrapper path
baked into their `command`:

```python
presets = {
  "chrome-devtools":     {"command": MCP_WRAPPERS_BIN/"node", "args": _chrome_devtools_args()},
  "sequential-thinking": {"command": MCP_WRAPPERS_BIN/"node", "args": [NPM_BIN/"mcp-server-sequential-thinking"]},
}
```

So the presets are safe. **Custom `mcp_servers` entries are stored verbatim** —
whatever `command` you wrote is used as-is. There is **no rewrite step** that
routes a bare `node`/`npx` command through the wrapper.

Concrete example — the common tavily server people configure:

```jsonc
"mcp_servers": {
  "tavily": {
    "command": "npx",                       // ← bare npx
    "args": ["-y", "tavily-mcp@latest"],
    "env": { "TAVILY_API_KEY": "${TAVILY_API_KEY}" }
  }
}
```

`command: "npx"` resolves via `PATH` to `/mise/installs/node/…/bin/npx` — **not**
`~/.local/bin/mcp-wrappers/npx`. Whether it actually crashes depends on whether
the spawning agent preserved `LD_LIBRARY_PATH` (the image bakes it, so it *may*
survive), but it is exactly the fragile, unowned path the wrapper exists to fix.
This gap is **identical in Python and Go** — a shared design gap, not a port
regression.

**Possible fix (not yet implemented):** in the shared `_load_mcp_servers()` /
`LoadMCPServers()`, after merging custom servers, rewrite a server's `command`
when it is a bare `node` or `npx` (basename match, not an absolute path) to the
corresponding `~/.local/bin/mcp-wrappers/<name>` path. Narrowest safe policy:
only `node`/`npx`; leave absolute paths, `python`, and other interpreters
untouched. Applied once in the shared loader, it covers all MCP-enabled agents.
Anything relying on the current verbatim behavior (e.g. a user who deliberately
points at a non-wrapper node) would want an escape hatch, but there's no known
such case.

---

## 2. How MCP config flows, and how agents differ

### The pipeline

```
yolo-jail.jsonc (mcp_servers, mcp_presets)
  → validated host-side (config.py :: _validate_config)
  → shipped into the jail as env: YOLO_MCP_SERVERS / YOLO_MCP_PRESETS (json.dumps'd)
  → in-jail _load_mcp_servers(): expand presets → merge custom (override / add /
      null-remove) → requires_env gate → ${VAR} interpolation
  → per-agent configure_*(): translate the ONE shared server dict into each
      agent's native config format
```

Key facts:

- **Presets are opt-in** (nothing enabled by default) and are **expanded
  in-jail**, not on the host. Valid presets: `chrome-devtools`,
  `sequential-thinking`.
- **`null` removes** a server or preset (`"tavily": null` kills it). Same-file
  "preset enabled AND null-removed" is a validation error; cross-hierarchy
  (user enables, workspace nulls) is intentional and allowed.
- **`requires_env`** gates a server: if any listed var is unset/empty in the
  jail, the server is dropped with a `notice:` line; otherwise the
  `requires_env` key is stripped before it reaches the agent. This lives in the
  single shared loader, so it applies **identically** to every MCP-enabled
  agent.
- **`${VAR}`** in `env` values is interpolated against the jail's startup env
  (which already has `env_sources` merged), so a secret can live in one unsynced
  file and be scoped to one server.

### Per-agent translation

Every selected agent's `configure_*()` calls the same `_load_mcp_servers()` and
then writes the result in that agent's native shape:

| Agent | Config file | MCP key / format | MCP? |
|---|---|---|---|
| **claude** | `~/.claude.json` (servers) + `~/.claude/settings.json` (perms) | `mcpServers` object | Yes |
| **copilot** | `~/.copilot/mcp-config.json` | `mcpServers` object (whole file yolo-owned) | Yes |
| **gemini** | `~/.gemini/settings.json` | `mcpServers` object — **plus LSP servers wrapped as `<name>-lsp` MCP entries** via `mcp-language-server` | Yes |
| **codex** | `~/.codex/config.toml` | `[mcp_servers.<name>]` TOML tables | Yes |
| **opencode** | `~/.config/opencode/opencode.json` | `mcp` object — `type:"local"`, `command:[argv]`, `environment` | Yes |
| **pi** | `~/.pi/agent/settings.json` | — none — | **No** (see §3) |

Differences worth remembering:

- **Schema shape varies**: claude/copilot/gemini use `{command,args,env}`
  objects; codex uses TOML tables; opencode flattens `command`+`args` into one
  argv **array** and renames `env`→`environment`.
- **Gemini uniquely** folds LSP servers into its MCP config (it has no native
  LSP), wrapping each through `mcp-language-server`.
- **Stale-server hygiene**: claude, gemini, codex, opencode each keep a
  `yolo-managed-mcp-servers.json` sidecar so a server dropped from config is
  removed from the agent's file **without** clobbering servers the user added by
  hand. Copilot rewrites its whole file each boot, so it needs no sidecar.

### Two dead host-side helpers (latent)

`_effective_mcp_server_names` and `_filter_mcp_servers_by_env` (Python
`config.py`; Go `internal/config/derived.go`) are defined, tested, and
re-exported but have **no production call site** in either language. They were
built to make the **AGENTS.md briefing** enumerate the effective MCP servers
(honoring presets, null-removals, and `requires_env`), but `agents_md.py` never
lists MCP servers, so the briefing is silent on MCP. Harmless today (the actual
per-agent config is correct); it's unfinished wiring — either wire the briefing
to use them, or delete them.

---

## 3. pi and MCP

### Why pi has no MCP today

`configure_pi()` deliberately writes no MCP config. Its docstring:

> pi is deliberately minimal — no permission popups, and no native MCP (MCP
> would require installing a separate adapter extension, so we do not wire the
> shared MCP servers here).

That was accurate: unlike the others, **pi has no built-in MCP client.** So we
have a real product gap — pi is the one agent in the registry (claude, copilot,
gemini, opencode, codex, pi) that gets none of the user's `mcp_servers`.

### What's actually possible (researched 2026-07-18)

pi (`@earendil-works/pi-coding-agent`, pi.dev) supports MCP through an official
**adapter extension**, `pi-mcp-adapter`. Once installed, it reads a standard MCP
config and exposes the servers as pi tools. The important findings:

- **Install** (either form):
  ```bash
  pi install npm:pi-mcp-adapter          # global
  pi install npm:pi-mcp-adapter -l       # project-local
  ```
  Under the hood this is an npm install into `~/.pi/agent/extensions/` plus an
  `extensions/package.json` that lists the adapter:
  ```json
  {
    "name": "extensions",
    "pi": { "extensions": ["./node_modules/pi-mcp-adapter"] },
    "dependencies": { "pi-mcp-adapter": "^2.x" }
  }
  ```

- **Config file:** `~/.pi/agent/mcp.json` (pi-global) — also reads project
  `.mcp.json`. The schema is the **standard `mcpServers` shape** we already use,
  plus pi-specific extras:
  ```json
  {
    "settings": { "directTools": true },
    "mcpServers": {
      "tavily": {
        "command": "npx",
        "args": ["-y", "tavily-mcp@latest"],
        "env": { "TAVILY_API_KEY": "${TAVILY_API_KEY}" },
        "lifecycle": "lazy"
      }
    }
  }
  ```
  Notably, `${VAR}` interpolation and `env` maps are supported by pi the same way
  we already produce them — the tavily config we ship is **verbatim compatible**.

- **Auto-read:** once the adapter is enabled, it reads the standard config files
  automatically — no per-server wiring inside pi. An interactive `/mcp` panel
  shows connection status.

- pi-specific keys we'd want to set: `directTools: true` (expose the servers as
  direct tools rather than proxy-only) and `lifecycle: "lazy"` (don't spawn
  until first use).

### "Detect and fill in" — the proposed approach

This is very feasible because pi's format is our format. Sketch, for
`configure_pi()` (and its Go twin `ConfigurePi`), gated so we never fight the
user:

1. **Only if MCP servers exist.** Compute the shared `_load_mcp_servers()`; if
   it's empty, do nothing (keep pi minimal — its whole appeal).
2. **Detect the adapter.** Check whether `pi-mcp-adapter` is enabled:
   `~/.pi/agent/extensions/package.json` lists it under `pi.extensions`, OR the
   adapter dir exists under `~/.pi/agent/extensions/node_modules/`. This is a
   cheap filesystem check — no `pi` subprocess needed.
3. **If present:** write `~/.pi/agent/mcp.json` with our resolved servers
   translated to pi's shape (add `lifecycle:"lazy"` + `settings.directTools:true`
   in the wrapper, like opencode's `type:"local"` translation), reconciled via a
   `yolo-managed-mcp-servers.json` sidecar exactly like the other agents. Route
   bare `node`/`npx` through the wrapper (see §1's fix).
4. **If absent:** two options —
   - **(a) Auto-install** the adapter at boot (`pi install npm:pi-mcp-adapter`
     with `YOLO_BYPASS_SHIMS=1`, mirroring how we lazily npm-install the agents).
     Simplest for the user; costs a one-time npm install and assumes network at
     first boot. Would want to be idempotent + best-effort (never fail the boot).
   - **(b) Detect-only + hint:** don't install, but if the user has `mcp_servers`
     configured AND pi selected AND the adapter missing, print a one-line
     notice: *"pi: MCP servers configured but pi-mcp-adapter not installed — run
     `pi install npm:pi-mcp-adapter` to enable them."* Least magic, respects
     pi's minimalism, keeps the install an explicit user act.

Recommended: **(3) + (4b)** first — write `mcp.json` whenever the adapter is
present, and hint (not auto-install) when it's absent. Auto-install (4a) can
follow if the friction proves annoying; making it opt-in via a config key (e.g.
`pi.auto_mcp: true`) avoids silently installing an extension into a "minimal by
design" agent. Whatever we pick, keep Python and Go in lockstep per the port's
freeze rule.

### Open questions (need a human call before building)

- Do we **auto-install** the adapter, hint-only, or gate it behind a config key?
  (Trades user friction against pi's "minimal, install-only-what-you-need"
  philosophy and against a boot-time network dependency.)
- Global (`~/.pi/agent/mcp.json`) vs project (`.mcp.json`) placement — global
  matches how we configure the other agents; project would leak into the repo.
- Version-pinning the adapter (the setup guides pin e.g. `@2.6.1`) vs `@latest`.
