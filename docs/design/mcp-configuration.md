# MCP configuration: the node/npx wrapper, per-agent formats, and the pi gap

**Status:** REFERENCE for §1 and §2's *rules*; **§2's mechanism and §3 are
STALE** — see the retraction below. **Spot-verified 2026-08-23:** the wrapper
generator (`GenerateMCPWrappers`, `internal/entrypoint/mcp_wrappers.go:7`, called
from `boot.go:472` / `darwin.go:59`); the shared loader (`LoadMCPServers`,
`internal/entrypoint/mcp.go:84`); the `${VAR}` non-interpolation ruling
(`mcp.go:33-61`, and no `os.Expand`/`ExpandEnv` anywhere in `internal/entrypoint`
or `internal/render`); and the two dead helpers in §2
(`internal/config/derived.go:33,65` — still zero call sites, now not even in
tests). **Not verified:** the per-agent config-file paths and schema shapes in
§2's table, the pi-adapter research in §3 (2026-07-18, external and unrechecked),
and the `LD_LIBRARY_PATH` root-cause narrative in §1.

> ### ⚠ Retracted 2026-08-23: "every agent's `configure_*()` writes MCP"
>
> **The per-agent configure functions do not exist.** There is no
> `configure_claude()`, `ConfigureClaude`, `configure_pi()` or any sibling —
> `internal/entrypoint/agent_configs.go` is now three small helpers
> (`dumpJSONIndent2`, `setDefaultMap`, `setDefault`) and nothing else. Nor is
> there a Python reference implementation: **the repo has zero tracked `.py`
> files** (`git ls-files '*.py'` → 0, verified 2026-08-23), so every
> `src/entrypoint/*.py` pointer below names a file that is gone.
>
> **What replaced it: MCP is pack-declarative.** Core publishes the canonical
> server table once — `manifest.SourceMCPServers: prismMap(e.LoadMCPServers())`
> at `internal/entrypoint/packsurfaces.go:241` — and each pack's `derive.lua`
> *projects* it into that tool's dialect. The packs that write MCP today:
> `claude` (`packs/claude/derive.lua:7` → `mcpServers` in `~/.claude.json`),
> `codex` (`:23` → `mcp_servers`), `copilot` (`:4-5` →
> `~/.copilot/mcp-config.json`), `opencode` (`:20` → `mcp`), and `agy` (`:4-5` →
> `~/.gemini/antigravity-cli/mcp_config.json`).
>
> **`gemini` is not an agent any more** (`internal/entrypoint/env.go:280-283`),
> so §2's gemini row and §3's "six agents in the registry" are both wrong. There
> is no registry, and the six agent packs are `claude`, `copilot`, `opencode`,
> `pi`, `codex`, `agy`.
>
> The *rules* in §2 — presets expand in-jail, `null` removes, `requires_env`
> gates, no `${VAR}` interpolation — all still hold, because they live in the one
> shared loader that every projection reads. Only the "who writes the file" half
> changed. §1's wrapper and its gap are untouched and current.

This doc explains three things that keep coming up:

1. **The node/npx wrapper** — what it is, why MCP servers need it, and the gap
   where *custom* servers bypass it.
2. **How MCP config flows** end-to-end and how it differs across agents.
3. **pi and MCP** — why pi has no MCP today, and what a "detect the adapter and
   fill it in" approach would look like. *(Read §3 as a 2026-07 proposal, not as
   shipped behaviour — and note it predates the pack/`derive.lua` model that
   would now carry it.)*

Source of truth for the code: `internal/entrypoint/mcp.go` (`LoadMCPServers:84`,
`LoadLSPServers:12`, `LoadMCPPresetNames:196`) and
`internal/entrypoint/mcp_wrappers.go` (`GenerateMCPWrappers:7`), plus each pack's
`derive.lua`.

---

## 1. The node/npx wrapper

### What breaks without it

> **Root cause — read this first:** the "node needs `LD_LIBRARY_PATH`" story is
> subtler than it looks, and an earlier version of this section got it wrong. The
> nix-built `/bin/node` runs fine with **no** `LD_LIBRARY_PATH` (it has a correct
> `RPATH`). The problem is specific to the **mise/upstream** node, whose ELF
> interpreter this image points at a nix `ld.so` that ignores `/etc/ld.so.cache`
> and can't see `libstdc++` — leaving `LD_LIBRARY_PATH` as the only lookup path.
> The full proven mechanism, why the clean structural fixes are blocked, and the
> open direction to actually remove this manipulation live in
> **[mise-node-dynamic-linking.md](mise-node-dynamic-linking.md)**. This section
> covers only how the MCP wrapper mitigates it today.

The mise/upstream node finds `libstdc++.so.6` **only** through
`LD_LIBRARY_PATH=/lib:/usr/lib`, a path the image bakes into its process
environment (`flake.nix:718`, re-exported on the container `-e`). Most processes
inherit it and are fine.

Here's the trap: **when a coding agent spawns an MCP server as a child process,
it often sanitizes the child's environment** — building a clean env dict rather
than inheriting the parent's. That strips `LD_LIBRARY_PATH`. The MCP server then
launches `node`, which can't find `libstdc++.so.6`, and dies with:

```
node: error while loading shared libraries: libstdc++.so.6: cannot open shared object file
```

This is the failure documented in `AGENTS.md` ("Common MCP Errors").

### What the wrapper does

`GenerateMCPWrappers` (`internal/entrypoint/mcp_wrappers.go:7`) writes three tiny
scripts at boot. **Two of the three, not three** — `node` and `npx` go into
`~/.local/bin/mcp-wrappers/` (`mcp_wrappers.go:11,14`), while
`chrome-devtools-mcp-wrapper` is written one level up in `~/.local/bin/`
(`mcp_wrappers.go:8`). Corrected 2026-08-23; the old text put all three in the
subdirectory.

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

`LoadMCPServers` (`internal/entrypoint/mcp.go:84`) defines the two built-in
**presets** with the wrapper path baked into their `command`:

```
chrome-devtools:     command = McpWrappersBin()/"node", args = chromeDevtoolsArgs()   [presets: mcp.go:88-101]
sequential-thinking: command = McpWrappersBin()/"node", args = [NpmBin()/"mcp-server-sequential-thinking"]
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

**Fix direction (not implemented — see the investigation doc):** the clean
"remove this entirely" options are blocked by hard constraints (read-only nix
store, a host-shared mise binary, and a nix `ld.so` that ignores the FHS cache);
see [mise-node-dynamic-linking.md](mise-node-dynamic-linking.md) for the proof
and the blocked-options table. The feasible *mitigation* is to generalize the
preset pattern: in the shared `_load_mcp_servers()` / `LoadMCPServers()`, rewrite
a custom server's bare `node`/`npx` `command` to the corresponding
`~/.local/bin/mcp-wrappers/<name>` (which `exec`s the nix `/bin/node`, so the
server process is self-sufficient). That closes this gap without touching the
store, but it does not eliminate the wrapper indirection — the real elimination
is an image-build change tracked in the investigation doc.

---

## 2. How MCP config flows, and how agents differ

### The pipeline

```
yolo-jail.jsonc (mcp_servers, mcp_presets)
  → validated host-side (internal/config/validate.go)
  → shipped into the jail as env: YOLO_MCP_SERVERS / YOLO_MCP_PRESETS (JSON)
  → in-jail LoadMCPServers() [mcp.go:84]: expand presets → merge custom
      (override / add / null-remove) → requires_env gate
                                        [NO ${VAR} interpolation — see below]
  → published as the canonical source table SourceMCPServers
      [packsurfaces.go:241]
  → each pack's derive.lua PROJECTS that ONE table into its tool's native
      config format
```

> [!WARNING]
> The last step used to read *"per-agent `configure_*()`"*. There are no such
> functions (see the retraction at the top). The projection is the pack's, not
> core's — that is principle 2 of [`pack-system.md`](pack-system.md): core knows
> the domain (`mcp_servers`), never the tool.

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
- **`${VAR}` IS NOT INTERPOLATED — by yolo, at any notch.** Removed 2026-08-03
  (maintainer ruling). yolo writes the reference **verbatim** into every field,
  and whoever launches the server resolves it from their own environment.

  It used to expand `env`, `headers`, and `url` against the jail's startup env.
  Two independent reasons that was wrong:

  1. **The value had no LAYER.** Every other value in a rendered surface has a
     provenance answer (`defaults` / `host` / `config-overlay:<pack>` / `managed` /
     `computed` / `retired:<layer>`), and the host-render story depends on being
     able to ask *who set this key*. An interpolated secret entered the file
     without passing through any layer, so `config diff` could not attribute it
     and the orphaned-key prune could not tell yolo's output from the user's.
  2. **It sourced config CONTENT from process env at render time.** `env_sources`
     is a jail-*provisioning* input — what the container's environment contains.
     Using it as a rendering input made the bytes written to a config file depend
     on the ambient environment of whoever ran the render, which is the one input
     the confinement model deliberately does not treat as configuration.

  It was also **unnecessary**, which is what made the tradeoff one-sided:
  `hydrateEnvFromUserEnvFile` does `os.Setenv` for every `env_sources` var before
  any generator runs, so those variables are already in the environment of every
  process the entrypoint spawns. Verified: a non-interactive `sh -c` and a bare
  `execve`'d python both see them, `env -i` clears them (so it is process-env
  inheritance, not a sourced rc file), and boot-time daemons carry them in
  `/proc/<pid>/environ`. The consuming agent can resolve `${VAR}` itself; yolo
  resolving first bought nothing and wrote a plaintext secret to disk.

  If a real need for declared secret references appears, the honest form is a
  **layer** with provenance, resolved at launch — not a string substitution during
  render.
- **Both notches now agree, and nothing warns.** The jail and `apply --host` write
  the same literal. The old host-side `${VAR}` warning is gone with the mechanism
  it existed to paper over: its first remedy was *"put the value in the file
  directly"* (advice to inline a live credential into a file a pack may carry), and
  it was surface-wide, so it flagged the `env` case — where a literal `${VAR}` is
  exactly the desired content, because the launching agent expands it — in the same
  words as the `url` case.

### Per-agent translation

Every selected agent's `configure_*()` calls the same `_load_mcp_servers()` and
then writes the result in that agent's native shape:

| Agent | Config file | MCP key / format | MCP? |
|---|---|---|---|
| **claude** | `~/.claude.json` (servers) + `~/.claude/settings.json` (perms) | `mcpServers` object | Yes |
| **copilot** | `~/.copilot/mcp-config.json` | `mcpServers` object (whole file yolo-owned) | Yes |
| ~~**gemini**~~ | — | — | **AGENT REMOVED** — see below |
| **codex** | `~/.codex/config.toml` | `[mcp_servers.<name>]` TOML tables | Yes |
| **opencode** | `~/.config/opencode/opencode.json` | `mcp` object — `type:"local"`, `command:[argv]`, `environment` | Yes |
| **agy** | `~/.gemini/antigravity-cli/mcp_config.json` | `mcpServers` object | Yes |
| **pi** | `~/.pi/agent/settings.json` | — none — | **No** (see §3) |

> [!WARNING]
> **The `gemini` row is retired (2026-08-23).** The gemini AGENT was removed
> (`internal/entrypoint/env.go:280-283`). The `~/.gemini/` tree survives — but it
> belongs to **agy** (Google Antigravity CLI) now, which keeps its state one
> level down under `antigravity-cli/` precisely so the two never collided. Its
> MCP goes to `~/.gemini/antigravity-cli/mcp_config.json`
> (`packs/agy/derive.lua:4-5`), **not** `~/.gemini/settings.json`.
>
> The LSP-folding claim below went with it: gemini uniquely wrapped LSP servers
> as `<name>-lsp` MCP entries through `mcp-language-server` because it had no
> native LSP. **Preserved here as rationale, not as live behaviour** — if you are
> looking for where LSP config goes today, start at `internal/cli/run/lsp.go` and
> the per-pack `derive.lua`, not at this row.

Differences worth remembering:

- **Schema shape varies**: claude/copilot/agy use `{command,args,env}`
  objects; codex uses TOML tables; opencode flattens `command`+`args` into one
  argv **array** and renames `env`→`environment`.
- **Stale-server hygiene**: claude, codex, opencode each keep a
  `yolo-managed-mcp-servers.json` sidecar so a server dropped from config is
  removed from the agent's file **without** clobbering servers the user added by
  hand. Copilot rewrites its whole file each boot, so it needs no sidecar.

### Two dead host-side helpers (latent)

`FilterMCPServersByEnv` (`internal/config/derived.go:33`) and
`EffectiveMCPServerNames` (`derived.go:65`) are defined and exported but have
**no call site anywhere** — verified 2026-08-23 by word-boundary grep across the
tree: only the two definitions, zero production callers **and zero tests**
(there is no `derived_test.go`). They were built to make the generated briefing
enumerate the effective MCP servers (honoring presets, null-removals, and
`requires_env`), but the briefing never listed MCP servers, so it is silent on
MCP. Harmless today (the per-pack projection is correct); it is unfinished wiring
— either wire the briefing to use them, or delete them.

> [!WARNING]
> This section used to say they were "defined, **tested**, and re-exported … in
> either language". Both qualifiers are now wrong: there is no Python half, and
> the test coverage is gone too. Whatever pinned them was deleted without
> deleting them, which is the worse of the two orders — the functions now look
> supported and are not.

---

## 3. pi and MCP

### Why pi has no MCP today

`configure_pi()` deliberately writes no MCP config. Its docstring:

> pi is deliberately minimal — no permission popups, and no native MCP (MCP
> would require installing a separate adapter extension, so we do not wire the
> shared MCP servers here).

That was accurate: unlike the others, **pi has no built-in MCP client.** So we
have a real product gap — pi is the one agent pack (of `claude`, `copilot`,
`opencode`, `codex`, `agy`, `pi`) that gets none of the user's `mcp_servers`.
Confirmed still true 2026-08-23: `packs/pi/` ships no `derive.lua` MCP
projection while the other five do. **"the registry" is retired language** —
there is no agent registry; the set is the shipped pack list.

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
  Notably `env` maps are supported by pi the same way we already produce them — the
  tavily config we ship is **verbatim compatible**. pi also expands `${VAR}` itself,
  which since 2026-08-03 is the ONLY expansion in the picture: yolo passes the
  reference through untouched and the consuming agent resolves it (see the `${VAR}`
  bullet above).

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
