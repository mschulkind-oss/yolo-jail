---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/entrypoint/mcp.go
  - internal/entrypoint/mcp_wrappers.go
  - internal/entrypoint/packsurfaces.go
  - internal/agentcfg/manifest/
  - packs/claude/derive.lua
  - packs/codex/derive.lua
  - packs/copilot/derive.lua
  - packs/opencode/derive.lua
  - packs/agy/derive.lua
tags: [mcp, lsp, packs, prism, config, wrappers]
summary: "How MCP and LSP server config reaches an agent: one canonical server table built in-jail from config (presets expanded, null removes, requires_env gates, no ${VAR} interpolation), published as a source that each pack's derive.lua projects into its own tool's dialect. Plus the node/npx wrappers, what is left of their job now that nix-ld covers the loader, and the gap where a custom server bypasses them."
---

# MCP and LSP configuration — one table, projected per tool

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

MCP config is **pack-declarative**. Core builds **one** canonical server table in-jail from
the user's config — presets expanded, custom entries merged, `requires_env` gates applied —
and publishes it as a named source. Each pack's `derive.lua` then *projects* that one table
into its own tool's dialect. Core knows the domain (`mcp_servers`); it never knows the tool.

Alongside that, three tiny wrapper scripts sit in front of `node` and `npx` for the servers
yolo itself declares.

| Component | Lives in |
| :--- | :--- |
| Building the canonical table, and the LSP table beside it | `internal/entrypoint` (`Env.LoadMCPServers`, `LoadLSPServers`, `Env.LoadMCPPresetNames`) |
| Publishing it as a composition source | `internal/entrypoint` (`packsurfaces.go`), `internal/agentcfg/manifest` (`SourceMCPServers`, `SourceLSPServers`) |
| The per-tool projection | each pack's `derive.lua`, run in `internal/agentcfg/luahook` |
| The wrapper scripts | `internal/entrypoint` (`GenerateMCPWrappers`, `nodeWrapper`, `npxWrapper`, `chromeWrapper`) |
| Host-side validation of the two config keys | `internal/config` (`validate.go`) |

**Reads with:** [`pack-system.md`](pack-system.md) (surfaces, layers, `derive.lua`, and the
principle that core knows the domain and never the tool),
[`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md) (the loader story the wrappers
used to carry), `yolo config-ref` (the authority for `mcp_servers`, `mcp_presets` and
`lsp_servers`).

---

## The pipeline

```
yolo-jail.jsonc: mcp_servers, mcp_presets, lsp_servers
  → validated host-side
  → shipped into the jail as JSON env vars
  → in-jail LoadMCPServers(): expand presets → merge custom entries
      (override / add / null-remove) → apply the requires_env gate
      [NO ${VAR} interpolation, at any notch]
  → published once as the canonical source table (SourceMCPServers)
  → each pack's derive.lua PROJECTS that one table into its tool's format
```

**There are no per-agent `configure_*` functions and no agent registry.** The projection is
the pack's, not core's.

### The rules the one loader enforces

Because every projection reads the same table, these apply **identically** to every
MCP-enabled tool.

- **Presets are opt-in and expanded in-jail**, not on the host. Their `command` is the wrapper
  path, baked in — so the servers yolo ships are wrapper-routed by construction.
- **`null` removes** a server or a preset. Same-file "enabled *and* null-removed" is a
  validation error; across scopes (a user config enables, a workspace nulls) it is intentional
  and allowed.
- **`requires_env` gates** a server: if any listed variable is unset or empty in the jail the
  server is dropped with a notice, and otherwise the `requires_env` key itself is **stripped**
  before the entry reaches the tool.
- **Key order is insertion order** — presets in the order the config listed them, then custom
  entries — so a projection's output is byte-stable across boots.

> [!WARNING]
> **`${VAR}` is not interpolated by yolo, at any notch. Do not add it back as a convenience.**
> yolo writes the reference **verbatim** into every field and whoever launches the server
> resolves it from their own environment. Two independent structural reasons:
>
> 1. **The value had no layer.** Every other value in a rendered surface has a provenance
>    answer, and the host-render story depends on being able to ask *who set this key*. An
>    interpolated secret entered the file without passing through any layer, so `config diff`
>    could not attribute it and the orphan-key prune could not tell yolo's output from the
>    user's. It was a value sneaking in the side door.
> 2. **It sourced config content from process env at render time.** `env_sources` is a jail
>    *provisioning* input — what the container's environment contains. Using it as a
>    *rendering* input made the bytes written to a config file depend on the ambient
>    environment of whoever ran the render, which is the one input the confinement model
>    deliberately does not treat as configuration.
>
> It was also unnecessary, which is what made the trade one-sided: the boot hydrates every
> `env_sources` variable into the process environment before any generator runs, so it is
> already in the environment of every process the entrypoint spawns — and the consuming agent
> can resolve `${VAR}` itself. yolo resolving first bought nothing and wrote a plaintext secret
> into a file it does not own.
>
> If a real need for declared secret references appears, the honest form is a **layer** with
> provenance, resolved at launch — not a string substitution during render.

Both notches now write the same literal and **nothing warns**. The old host-side `${VAR}`
warning went with the mechanism it papered over: its first remedy was *"put the value in the
file directly"* — advice to inline a live credential into a file a pack may carry — and it was
surface-wide, so it flagged the `env` case, where a literal `${VAR}` is exactly the desired
content, in the same words as the `url` case.

### The projection, and how tools differ

Each pack declares its own config surface and a `derive.lua` that reads the canonical table
and returns its tool's shape. **The per-tool table is pack data**: the file paths, the key
names and the schema live in each pack's `pack.json` and `derive.lua`, which are the
authority. What is worth knowing is *how the shapes differ*, because that is what a new
projection has to get right:

- Most tools take a `{command, args, env}` object map under some spelling of `mcpServers`.
- One takes TOML tables instead of JSON objects.
- One flattens `command` plus `args` into a single argv **array** and renames `env`.
- One tool's MCP goes in a *different file* from its permissions, so its projection writes two
  surfaces.
- One agent pack has **no MCP projection at all**, because that agent has no built-in MCP
  client — it needs a separately-installed adapter extension. That agent therefore receives
  none of the user's `mcp_servers`. See [Unbuilt](#unbuilt).

**Convergence — how a dropped server disappears** — is the composition engine's job, not a
per-tool one:

- For a composed surface, a fold renders the file from the layers it *has*, so a layer that
  stopped claiming a key simply does not contribute it and the key is not in the output. There
  is nothing to launder.
- For a read-modify-write surface — one holding live agent state, where "regenerate from
  layers" describes the wrong operation — the provenance record carries a `retired:<layer>`
  label for a key no live layer claims any more. Without it, a key yolo wrote would read back
  as the *user's* the moment its pack was dropped, and that is self-reinforcing: once a key
  reads as the user's, every mechanism asking "did yolo write this?" answers no, forever.

> [!WARNING]
> **The `yolo-managed-mcp-servers.json` sidecars are retired, not load-bearing.** They were how
> a pre-prism bespoke writer remembered which server names it owned. Two packs still name one —
> in `retireOnFirstRender`, so the first render through the composition engine **deletes** it.
> A missing file is not an error there; the common case is that there was nothing to clean. Do
> not write a new sidecar to solve a convergence problem: provenance and the fold already
> answer it.

## The node/npx wrapper

`GenerateMCPWrappers` writes three scripts at boot: `node` and `npx` into a `mcp-wrappers`
subdirectory of the local bin dir, and a fatter `chrome-devtools-mcp-wrapper` one level up
beside it.

The `node` and `npx` wrappers now do exactly two things: export the fontconfig variables, and
`exec` the **nix** binary directly.

- **Fontconfig** is chromium's font configuration, unrelated to the loader.
- **`exec`ing `/bin/node` rather than the mise shim** skips mise's per-directory environment
  resolution on every MCP cold start, and gets a binary whose `RPATH` makes it self-contained.

> [!WARNING]
> **The wrappers no longer set `LD_LIBRARY_PATH`, and re-adding it is the whack-a-mole this
> repo deliberately ended.** The loader problem they used to guard — an FHS node losing
> `libstdc++` when an agent scrubbed the child environment before spawning an MCP server — is
> covered *structurally* now, by nix-ld as the FHS ELF interpreter. See
> [`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md). The baked
> `LD_LIBRARY_PATH` in the image environment stays, for a different class entirely
> (`dlopen`-by-soname from nix-built processes); the **per-call-site re-assertions** were the
> problem, and they are gone.

A wrapper is **self-contained on purpose** — it sets its environment from `$HOME`-relative
constants and `exec`s. It must never call out to `npm config get` or shell out for a path:
that is what makes it robust to being handed a sanitized environment.

The chrome wrapper additionally starts headless chromium if it is not already answering, and
**resolves chromium rather than assuming a path**: a baked image has it at a known
`/usr/bin` symlink, while a launch that delivers the image's bulk extras from the mounted nix
store has no such symlink and a `chromium` on PATH instead. The baked path is tried first so a
jail that bakes behaves exactly as it always did.

### The gap: a custom server bypasses the wrapper

Custom `mcp_servers` entries are stored **verbatim**. Whatever `command` the user wrote is what
the tool gets, and there is **no rewrite step** routing a bare `node` or `npx` through the
wrapper. So a config like

```jsonc
"mcp_servers": {
  "example": {
    "command": "npx",
    "args": ["-y", "some-mcp@latest"],
    "env": { "SOME_API_KEY": "${SOME_API_KEY}" }
  }
}
```

resolves `npx` through PATH to the mise shim, not to the wrapper.

**What that costs is now small, and worth stating precisely** so nobody re-fixes the old
problem. The crash class is gone: nix-ld resolves the FHS node's libraries env-free, so a
scrubbed-environment spawn no longer dies. What a bypassing server misses is the fontconfig
export (which matters only if it drives chromium) and the mise-shim resolution cost on cold
start. The mitigation, if it is ever wanted, is to generalize the preset pattern — rewrite a
bare `node`/`npx` command to the wrapper path inside the one shared loader — which is one
change in one place rather than a call-site hunt.

## What this does not license

- **Not** a per-tool branch in core. Core publishes the domain table; the pack projects it.
- **Not** `${VAR}` interpolation, in either notch, in any field.
- **Not** a new sidecar file for convergence. The fold and the provenance record answer it.
- **Not** an `LD_LIBRARY_PATH` export in a wrapper, or anywhere else per call site.
- **Not** a wrapper that shells out to discover a path. Self-contained or it is not robust to
  the case it exists for.

## Unbuilt

One agent pack ships **no MCP projection**, because that agent has no built-in MCP client: MCP
arrives through a separately-installed adapter extension, which then reads a standard
`mcpServers`-shaped config from the agent's own config dir. The shape is compatible with the
canonical table, and that agent expands `${VAR}` itself, so the missing piece is a projection
plus a decision about the adapter: auto-install it at boot, detect-and-hint, or gate it behind
a config key. None of that is built, and the decision is a human call about that agent's
deliberately-minimal posture against a boot-time network dependency.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Config keys | `mcp_servers`, `mcp_presets`, `lsp_servers` | `yolo config-ref` |
| Wire form into the jail | one JSON env var per key | `internal/cli/run/assemble.go` |
| Canonical source names | `mcp_servers`, `lsp_servers` | `manifest.SourceMCPServers`, `SourceLSPServers` |
| Available presets | `chrome-devtools`, `sequential-thinking` | `Env.LoadMCPServers` |
| Wrapper locations | `~/.local/bin/mcp-wrappers/{node,npx}`; `~/.local/bin/chrome-devtools-mcp-wrapper` | `Env.McpWrappersBin`, `GenerateMCPWrappers` |
| What a wrapper exports | `FONTCONFIG_FILE`, `FONTCONFIG_PATH` — and nothing else | `internal/entrypoint/mcp_wrappers.go` |
| What a wrapper execs | the nix `/bin/node` / `/bin/npx` | `internal/entrypoint/mcp_wrappers.go` |
| Chrome debug endpoint defaults | overridable by `CHROME_DEBUG_PORT` / `CHROME_DEBUG_ADDR` | `chromeWrapper` |
| Retired sidecar name | `yolo-managed-mcp-servers.json`, deleted on first composed render | each pack's `retireOnFirstRender` |
| Bootstrap-installed MCP packages | gated on the same preset declaration that builds the table | `Env.LoadMCPPresetNames` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `D6` | The bootstrap's npm install for a preset is gated on the **same declaration** that builds the server table | Hardcoding a package list beside the preset table lets the two drift, and the failure is a preset that is configured and whose package was never installed. |
| Principle 2 of [`pack-system.md`](pack-system.md) | Core publishes the **domain** (`mcp_servers`); the pack owns the **tool's dialect** | A per-tool branch in core is the agent registry coming back through a different door. The projection changes when the tool changes, which is the pack's business. |
| Orphan-file cleanup ([`config-migration-to-prism.md`](config-migration-to-prism.md#the-two-sidecars-are-different-kinds-of-thing)) | A retired sidecar is deleted by the surface's owner on first composed render, as **data** rather than Go | It was the last thing left in the per-agent render functions besides the computed layer, and keeping it in Go would keep those functions alive for a one-shot cleanup. |
