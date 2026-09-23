---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/entrypoint/mcp.go
  - internal/jailcontent/lspplugin.go
  - internal/config/lsp.go
  - internal/agentcfg/luahook/derive.go
  - internal/entrypoint/mcp_wrappers.go
  - internal/entrypoint/packsurfaces.go
  - internal/agentcfg/manifest/
  - packs/claude/derive.lua
  - packs/codex/derive.lua
  - packs/copilot/derive.lua
  - packs/opencode/derive.lua
  - packs/agy/derive.lua
  - packs/pi/derive.lua
tags: [mcp, lsp, packs, prism, config, wrappers]
summary: "How MCP and LSP server config reaches an agent: one canonical server table built in-jail from config (presets expanded, null removes, requires_env gates, no ${VAR} interpolation), filtered by the active source's capabilities, and published as a source that each pack's derive.lua projects into its own tool's dialect — except Claude's LSP, which core renders host-side as one generated yolo-lsp plugin in every skills destination. Plus the node/npx wrappers, what is left of their job now that nix-ld covers the loader, and the gap where a custom server bypasses them."
---

# MCP and LSP configuration — one table, projected per tool

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`. UNMEASURED: no `claude`
session has been started against the generated LSP plugin — the facts about Claude's plugin
loader were read statically from its bundle, and by the no-agent-tests rule a live check is a
human's ([Claude's LSP route](#lsp-claudes-route-is-a-generated-plugin)).

MCP config is **pack-declarative**. Core builds **one** canonical server table in-jail from
the user's config — presets expanded, custom entries merged, `requires_env` gates applied —
and publishes it as a named source. Each pack's `derive.lua` then *projects* that one table
into its own tool's dialect. Core knows the domain (`mcp_servers`); it never knows the tool.

LSP config follows the same shape for every agent but one. Claude takes a language server only
from a **plugin**, so core renders one plugin of its own, `yolo-lsp`, from the user's
`lsp_servers` table and stages it into every skills destination — the one place in this
pipeline where core writes a vendor's format rather than a pack
([below](#lsp-claudes-route-is-a-generated-plugin)).

Alongside that, three tiny wrapper scripts sit in front of `node` and `npx` for the servers
yolo itself declares.

| Component | Lives in |
| :--- | :--- |
| Building the canonical table, and the LSP table beside it | `internal/entrypoint` (`Env.LoadMCPServers`, `LoadLSPServers`, `Env.LoadMCPPresetNames`) |
| Publishing it as a composition source | `internal/entrypoint` (`packsurfaces.go`), `internal/agentcfg/manifest` (`SourceMCPServers`, `SourceLSPServers`) |
| Capability-driven MCP filtering at the derive boundary | `internal/agentcfg/luahook` (`eligibleMCPServers`, `withoutProvidesKey`, `sourceCapabilities`) |
| The per-tool projection | each pack's `derive.lua`, run in `internal/agentcfg/luahook` |
| Claude's generated LSP plugin | `internal/jailcontent` (`writeLSPPlugin`, `SetLSPServers`, `LSPPluginDir`), called from `PrepareSkills` |
| The LSP install recipes (still present, slated for removal) | `internal/config` (`LSPInstalls`, `lspInstallRecipes`) |
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
  ├─ HOST, every launch: lsp_servers → jailcontent.writeLSPPlugin
  │     → <every skills destination>/yolo-lsp/.claude-plugin/plugin.json
  │     (Claude's LSP route; mounted at ~/.claude/skills for the claude pack)
  → shipped into the jail as JSON env vars
  → in-jail LoadMCPServers(): expand presets → merge custom entries
      (override / add / null-remove) → apply the requires_env gate
      [NO ${VAR} interpolation, at any notch]
  → published once as the canonical source tables (SourceMCPServers, SourceLSPServers)
  → at the derive boundary: drop MCP servers whose `provides` the active
      source already performs, then strip `provides` from the survivors
  → each pack's derive.lua PROJECTS those tables into its tool's format
```

**There are no per-agent `configure_*` functions and no agent registry.** Every MCP
projection, and every LSP projection except Claude's, is the pack's, not core's. Claude's LSP
plugin is the exception, and it names no agent: core writes it into every skills
destination rather than asking which one is Claude's.

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

### What the derive boundary removes

One more cut happens after the loader and before any pack sees the table, where the derive
context is built. **Capability-driven MCP delivery** drops a server whose `provides` names a
capability the launch's **authentication source** already performs — a search MCP is not
delivered to an agent whose login searches natively. The source is either the provider a
profile selects (its row's `capabilities`) or, when none is selected, the agent's own built-in
login (the capabilities its pack's program contribution declares). A server with no `provides`
makes no claim and always passes; only an exact-name match is dropped. Because it sits at the
context boundary, no pack can opt out of it and none has to opt in.

Then `provides` itself is stripped from every surviving entry, unconditionally: it is yolo's
own vocabulary, and a pack that copies an entry verbatim into its agent's file would otherwise
leak it there.

> [!WARNING]
> **Do not move the `provides` strip beside the `requires_env` strip in the loader.** The
> loader runs upstream of the derive boundary, so a `provides` removed there is gone before the
> filter can read it, and capability-driven delivery silently becomes a no-op with every test
> green. The two keys are stripped at two layers on purpose: `requires_env` gates delivery and
> must go first, `provides` feeds the filter and must go after it.

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
- Pi projects the canonical table into `~/.pi/agent/mcp.json` (`mcpServers`), where adapter
  extensions like `pi-mcp-adapter` or `pi-mcp-extension` read it.

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

## LSP: Claude's route is a generated plugin

`lsp_servers` is one table, and agents consume it through different mechanisms:

- **Copilot's** native config is the same shape as yolo's table, so its pack's derive projects
  every entry near-verbatim, the in-jail way every MCP projection works.
- **Claude** has no settings-level key for a language server. A server reaches it only through
  a **plugin** — a directory whose manifest declares `lspServers`, each entry a command plus an
  extension-to-language map. So yolo authors one: the **LSP plugin**, a directory named
  `yolo-lsp` whose manifest is rendered from the user's whole `lsp_servers` table. Any language
  the user configures reaches Claude; there is no per-language list and no marketplace.
- **opencode and Pi** can take a server (a native `lsp` key; the `pi-lens` extension's config)
  but no pack writes one yet — see [Unbuilt](#unbuilt). **Codex and agy** have no
  user-configurable LSP surface at all.

**No server is a default.** An absent `lsp_servers` renders no plugin, projects nothing for
Copilot, and installs nothing.

### How the plugin is rendered

The launcher renders it **on the host, on every launch**, while it stages each pack's skills
tree — `writeLSPPlugin`, called from `PrepareSkills`, on both the container backends and
macos-user. It is not a derive and does not run in the jail. The claude pack delivers its
skills staging at `~/.claude/skills`, which Claude reads plugins from, so delivery needs
nothing beyond the mount that already carries skills.

- **Translation.** Each server's `command` passes through and `args` passes through;
  `fileExtensions` is renamed `extensionToLanguage`. Either list is omitted when empty rather
  than written empty, so a hand-read manifest shows only what was declared. **An entry with no
  `command` is skipped**, never rendered: a server yolo cannot spawn would only make Claude
  report a failure yolo could have declined to cause. (Copilot's derive keeps such an entry,
  minus the command.) The manifest also carries a `name` and a `description`.
- **Ownership marker.** The manifest carries yolo's `x-yolo-managed-by` field, which is how the
  host-side adoption walk (`hostskills.IsYoloPluginDir`) knows the directory is yolo's output
  and never offers to migrate it into the user's local pack. The value is duplicated between
  `jailcontent` and `hostskills` rather than imported — `jailcontent` must not depend on the
  host-skills composer — and a drift test pins the two together.
- **Written last.** It goes in after the built-in skills and every pack's skills, so a pack
  that ships a directory of the same name cannot replace it with something Claude would load
  as an LSP declaration.
- **Written into every skills destination**, not only Claude's. Core knows no agents, and
  teaching the staging loop which destination is Claude's would be the agent registry again; a
  destination whose tool does not read plugins simply holds a directory it ignores (INFERRED
  from the other agents' loaders, not measured for each).
- **Removed when empty.** With nothing configured, a stale `yolo-lsp` directory is deleted, so
  dropping the last server stops it reaching Claude. The staging is cleared per launch anyway;
  this covers a caller that stages without clearing.
- **Per-launch state, scoped.** The run pipeline injects the table (`SetLSPServers`) because
  `jailcontent` reads no config itself. That makes it process-wide state, and auto-capture
  runs the same pipeline in-process, so `packRecordScope` snapshots and restores it; its
  source-scan tripwire fails a new record that joins without a declared lifetime.

**Nothing enables the plugin, and nothing needs to.** A plugin auto-loaded from the skills
tree is enabled by default there — the vendor's settings test for that load path is opt-out
rather than opt-in — so the claude derive writes **no `enabledPlugins` at all**. Its one LSP
assertion is `env.ENABLE_LSP_TOOL`, set when any server is configured and absent otherwise.

> [!WARNING]
> **The `.claude-plugin/` segment is mandatory on this load path.** Claude resolves the
> manifest only at `<dir>/.claude-plugin/plugin.json` for a plugin loaded off the skills tree;
> the root-level `plugin.json` fallback that marketplace installs get does not apply. A
> manifest at the directory root is silently not found.

> [!WARNING]
> **Do not inline an `lspServers` table into Claude's `settings.json`.** No such key exists:
> Claude's only producer of LSP configuration takes a plugin, and the one settings-side
> mention of `lspServers` in its bundle is a hook-scanner exclusion list, not a schema entry.
> The plugin is the only route, which is why it exists.

> [!WARNING]
> **Do not bring back per-language `enabledPlugins` toggles, and never tombstone one.** A
> tombstone is a delete aimed at the lower layers, and the lowest layer under the derive is
> the user's own host `settings.json` — so "remove yolo's stale enable" removes the user's
> deliberate enable of the same plugin id. A marketplace id in a host layer is the user's,
> and composition must leave it alone.

> [!WARNING]
> **The injection call site is unpinned.** The plugin tests call `SetLSPServers` themselves,
> and the scope tripwire still finds the setter in `packRecordScope`, so deleting the run
> pipeline's one injection line switches the feature off with the unit gate green (MEASURED
> at `7ad8358c`, in a scratch copy of the tree). A test that runs the launch's
> briefing-and-skills refresh with `lsp_servers` set and asserts the manifest exists is what
> closes it.

**The plugin is jail-only.** The host render (`yolo apply --at host`) composes skills
through its own path and does not render `yolo-lsp`.

**Binaries are a separate question.** The plugin and Copilot's projection name a `command`;
whether it exists is decided elsewhere. Today three server names — the recipe table in
`internal/config` — still map to packages the bootstrap installs, and every other server's
`command` must already resolve on `PATH`. That table is slated for deletion ([Unbuilt](#unbuilt)).

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

> [!NOTE]
> **The chrome wrapper is generated on every container boot and nothing yolo generates spawns
> it.** No preset's `command` names it; its only other references in the tree are the boot
> catalog's declared-orphan entry — which is what stops `catalogLocalBinOrphans` reporting it
> as an unowned file in `~/.local/bin` — and tests. The `chrome-devtools` preset spawns `mcp-wrappers/node`
> directly and lets the MCP server start chromium itself.

### Where the wired preset finds chromium

Because the preset does not go through the fat wrapper, the resolution the wrapper does has to
happen again in the argv the preset emits: `chrome-devtools-mcp` is passed
`--executablePath`, and that value is **resolved when the entry is generated, not written into
the source**. A baked image answers with its `/usr/bin` symlink; a store-delivered launch
answers with the `/run/yolo/packages` farm, which is where `.#yoloImageExtras` puts chromium
once `.#ociImageLean` has stopped baking it. With nothing to resolve the value falls back to
the baked path, so a jail with no chromium fails the way it always did.

The search is the **launcher-collision check's probe path** (`imageProbePath`) rather than a
second search order of its own — one answer to *what does this launch provide*, which already
counts the store farm and excludes the per-home install prefixes. That exclusion is also what
makes the answer available this early: the farm is built by the **first** generator in the
boot, while the install prefixes are still being filled in when config is rendered.

> [!WARNING]
> **Pinning `/usr/bin/chromium` here is a real outage, not a tidiness point.** That symlink is
> created only inside `mkBinPathLinks`' `withChromium` block, and `.#ociImageLean` — the image
> a `YOLO_STORE_PACKAGES=1` launch builds — turns that block off. A pinned argv therefore names
> a path that does not exist on exactly the launches that do have a chromium. See
> [`image-staging-vs-baking.md`](image-staging-vs-baking.md#store-delivered-packages).

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

- **Not** a filter on a workspace's own MCP config. yolo composes the canonical table as a
  *source*; a repo's `.vscode/mcp.json` or `.mcp.json` reaching the agent is intended, and the
  `/dev/null` shadow that used to blank one of them was removed on 2026-09-22 — see
  [`../design/workspace-mcp-sources.md`](../design/workspace-mcp-sources.md) for the position, the
  measured per-agent file sets, and the one open precedence question.
- **Not** a per-tool branch in core. Core publishes the domain table; the pack projects it.
  The one exception is Claude's LSP plugin, which core renders in a Claude format — and even
  that names no agent, going to every skills destination.
- **Not** a per-language LSP map, a marketplace plugin id, or an `lspServers` key in Claude's
  settings. The plugin is generated from the user's whole table ([`OQ-LSP1`](#oq-lsp1)).
- **Not** an opinion about which server serves a language. The user's `command` is the choice;
  yolo's job is to deliver it.
- **Not** `${VAR}` interpolation, in either notch, in any field.
- **Not** a new sidecar file for convergence. The fold and the provenance record answer it.
- **Not** an `LD_LIBRARY_PATH` export in a wrapper, or anywhere else per call site.
- **Not** a wrapper that shells out to discover a path. Self-contained or it is not robust to
  the case it exists for.
- **Not** an absolute chromium path written into the `chrome-devtools` argv. Where chromium is
  depends on how the launch got its packages, so the entry resolves it.

## Unbuilt

**Deleting the LSP install recipes.** `internal/config` still maps three server names
(`python`, `typescript`, `go`) to packages, resolved by `LSPInstalls` into the two install-list
variables the bootstrap reads on every backend. The recipe table picks a server for a language,
which is the opinion the plugin removed from Claude's side; the ruled plan deletes it and its
install plumbing, after which a configured `command` must resolve on `PATH` and the user brings
the server through `mise_tools`, a pack program or an absolute path.

**LSP producers for opencode and Pi.** opencode's config file is already a pack surface with no
`lsp` producer in its derive; Pi's `pi-lens` extension reads `lsp.servers` from
`~/.pi-lens/config.json`, which no pack writes.

**Removing the `sequential-thinking` preset** is ruled and not built —
[`OQ-MP1`](../design/mcp-presets-removal.md#decision-ledger).

Auto-installing or bundling an MCP adapter extension for Pi: `packs/pi` projects the canonical
server table into `~/.pi/agent/mcp.json`, which `pi-mcp-adapter` and `pi-mcp-extension` consume
natively, but yolo does not auto-install either extension at boot. Deciding whether to bundle
a standalone extension in `kind: "files"`, auto-install via a hook, or leave it to user
configuration is a choice about Pi's minimal posture against boot-time network dependencies.

## Current values

Verified at `7ad8358c`. The prose above explains what each of these is for; this table is the
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
| Where the wired `chrome-devtools` argv gets chromium | resolved against what the launch provides, falling back to the baked `/usr/bin` path | `chromiumExecutablePath` |
| Retired sidecar name | `yolo-managed-mcp-servers.json`, deleted on first composed render | each pack's `retireOnFirstRender` |
| Bootstrap-installed MCP packages | gated on the same preset declaration that builds the table | `Env.LoadMCPPresetNames` |
| MCP entry key the capability filter reads, then strips | `provides` | `luahook.eligibleMCPServers`, `withoutProvidesKey` |
| LSP plugin directory name | `yolo-lsp` | `jailcontent.LSPPluginDir` |
| LSP plugin manifest path | `<skills destination>/yolo-lsp/.claude-plugin/plugin.json` | `jailcontent.writeLSPPlugin` |
| Ownership marker | `x-yolo-managed-by: "yolo-jail"` | `jailcontent.yoloPluginManagedBy`, `hostskills.yoloManagedMarker` |
| Claude's LSP switch | `env.ENABLE_LSP_TOOL = "1"` in `settings.json`, only when a server is configured | `packs/claude/derive.lua` |
| Claude Code version the plugin-loader facts were read from | 2.1.278, statically from its bundle | [`OQ-LSP3`](#oq-lsp3) |
| LSP install recipes (slated for deletion) | `python`, `typescript`, `go` → `YOLO_LSP_NPM_INSTALL` / `YOLO_LSP_GO_INSTALL` | `config.lspInstallRecipes`, `config.LSPInstalls` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `D6` | The bootstrap's npm install for a preset is gated on the **same declaration** that builds the server table | Hardcoding a package list beside the preset table lets the two drift, and the failure is a preset that is configured and whose package was never installed. |
| Principle 2 of [`pack-system.md`](pack-system.md) | Core publishes the **domain** (`mcp_servers`); the pack owns the **tool's dialect** | A per-tool branch in core is the agent registry coming back through a different door. The projection changes when the tool changes, which is the pack's business. |
| <a id="oq-lsp1"></a>[`OQ-LSP1`](#oq-lsp1) | **Option D — generate.** yolo authors ONE plugin whose `lspServers` is rendered from the user's own `lsp_servers` table, and delivers it as content. Never a per-language map, never marketplace plugin ids — neither the three it replaced nor the full official set | A per-language map is yolo picking "the" server for a language, which is the user's choice; marketplace ids also need an install path, and a jail runs no vendor install verb. The ruling said "and enables it", but no enable step exists: the skills-tree load path is opt-out. |
| <a id="oq-lsp3"></a>[`OQ-LSP3`](#oq-lsp3) | Claude **auto-loads a plugin from `~/.claude/skills/*`** with no marketplace, no `enabledPlugins` entry and no flag; it is **enabled by default** there; ONE plugin may declare MANY servers, keyed by server name. Measured statically against Claude Code 2.1.278 | This is the precondition option D rests on, and it is version-pinned. **Falsifier:** a Claude release that removes the skills-tree or session load arm, or a managed-settings `disableSideloadFlags` in force — shipped on by default, or set by a policy on the user's machine. Anyone revisiting it re-reads the installed version's plugin assembler. Two servers claiming one extension is a warning, not a load failure. |
| Orphan-file cleanup ([`config-migration-to-prism.md`](config-migration-to-prism.md#the-two-sidecars-are-different-kinds-of-thing)) | A retired sidecar is deleted by the surface's owner on first composed render, as **data** rather than Go | It was the last thing left in the per-agent render functions besides the computed layer, and keeping it in Go would keep those functions alive for a one-shot cleanup. |
