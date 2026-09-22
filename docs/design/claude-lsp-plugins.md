---
title: "Claude's LSP plugins: three hardcoded languages for a table yolo already owns"
date: 2026-09-19
status: accepted
tags: [lsp, claude, plugins, packs, config, derive]
summary: "Where this landed: GENERATE (option D), BUILT — yolo authors one plugin whose lspServers is rendered from the user's own lsp_servers table, so any language works with no marketplace, no hardcoded ids and no hook. D was ruled without waiting on the load-path measurement, which therefore became a PRECONDITION of the build; that measurement landed 2026-09-22 and the answer is YES — Claude 2.1.278 auto-loads a plugin from `~/.claude/skills/*` with no marketplace, no enabledPlugins entry and no flag, which is a tree yolo already stages, so D stands and is cheap. jailcontent.writeLSPPlugin is now the only route an LSP server has to Claude: the three hardcoded marketplace ids are deleted, and so is the vendor install path that was supposed to fetch them."
vantage:
  status-chip: true
---

# Claude's LSP plugins: three hardcoded languages for a table yolo already owns

**Status:** BUILT 2026-09-22. **Every ruling is in, the precondition was met, and option D ships.**
MEASURED: the renderer, its translation, the absent-and-stale cases, the duplicated ownership marker
and the `PrepareSkills` call site each carry a test, and the call-site and marker tests were verified
to fail when their half is removed. UNMEASURED: no `claude` session has been started against a
rendered plugin — by `AGENTS.md`'s no-agent-tests rule, that is a human check. [OQ-LSP1](#OQ-LSP1) ruled **generate (D)** *without* waiting on [OQ-LSP3](#OQ-LSP3), which
was ruled **go measure** — so that measurement was a **precondition of the build**. It landed
2026-09-22 against Claude Code **2.1.278**: a local plugin loads with **no marketplace**, so D
stands and does not reopen. ⚠ **No vendor install verb runs in a jail, and nothing replaced the
`claude_plugins` hook in kind** — an LSP server reaches Claude only because yolo AUTHORS the plugin
and stages it ([§1.1](#11-the-three-and-where-they-went)).

> **In short.** `packs/claude/derive.lua` and `internal/entrypoint/claude.go` each hardcoded
> the same three-language map — python → `pyright-lsp`, typescript → `typescript-lsp`, go →
> `gopls-lsp`, all from the `claude-plugins-official` marketplace. That marketplace publishes
> **thirteen** such plugins, and each one is nothing but an `lspServers` declaration: a command
> and an extension→language map. yolo already owns that table (`lsp_servers`) and already
> installs the binaries itself, so the three were an arbitrary, duplicated subset of a table
> yolo could generate — and now does, from one plugin of its own.

**Why it matters.** Three reasons, in order of weight. It **fills the hole the hook's retirement
left**: `claude_plugins` was the only thing that put an LSP plugin into a jail's plugin cache and it
is deleted ([`pi-pack-extensions.md`](./pi-pack-extensions.md)
[`OQ-2`](./pi-pack-extensions.md#10-decision-ledger)), which left a derive naming three plugins
nothing installed ([§1.1](#11-the-three-and-where-they-went)). It **narrows a live data-loss
defect** on the roadmap: `dropComputedTables` over-drops any table a derive fills only partly, and
the derive no longer asserts `enabledPlugins` at all, so that table is out of its reach —
`env.ENABLE_LSP_TOOL` is the one assertion left, so [row 0b](../plans/roadmap.md) bites there and
not here. And it removes agent-specific residue of exactly the shape this project keeps
deleting: a language list hardcoded in Go, duplicated in Lua, that the user's
own config could have supplied.

**Shipped shape.** `jailcontent.writeLSPPlugin` renders one manifest at
`<skills staging>/yolo-lsp/.claude-plugin/plugin.json`, translating the user's `lsp_servers` into
`lspServers` — `command` and `args` pass through, `fileExtensions` becomes `extensionToLanguage`, and
an entry with no `command` is skipped rather than rendered broken. It carries the
`x-yolo-managed-by` marker, so the host-side adoption walk already recognises it as yolo's and leaves
it alone. `claudeLSPPluginOrder` and the derive's three-language table are **deleted**, and the derive
writes **no `enabledPlugins` at all** — which also removes that table's whole interaction with
`dropComputedTables`.

**The shape.** Five options were live — **keep** (status quo), **generalize** the map to the full
official set, **delete** it where unused, **generate** a single yolo-authored plugin from the
configured `lsp_servers`, or **inline** the table into Claude's settings. **Generate is the
ruling** ([OQ-LSP1](#OQ-LSP1)), and the load path it rested on is measured: Claude reads a plugin
yolo authored, off the skills tree yolo already stages, with no marketplace ([OQ-LSP3](#OQ-LSP3)).
Inlining is refuted by the same measurement — Claude's `settings.json` takes no `lspServers` table.

**Needs your ruling:** **None** — all three were ruled 2026-09-20, the measurement they were
conditional on landed 2026-09-22, and the build shipped the same day; see the
[decision ledger](#6-decision-ledger). The
[`slots-and-contributions.md`](./slots-and-contributions.md) role model governs how the generated
tree is delivered.

**Reads with:** [`mcp-configuration.md`](../reference/mcp-configuration.md) (the canonical
MCP/LSP tables and per-agent projection), [`pi-pack-extensions.md`](./pi-pack-extensions.md)
(plugin delivery; its `claude_plugins` retirement has SHIPPED — note its `files` **encoding** is
superseded by [`slots-and-contributions.md`](./slots-and-contributions.md), which governs how a
plugin tree reaches Claude), [`claude-plugins-official`](https://github.com/anthropics/claude-plugins-official)
(the marketplace — its `.claude-plugin/marketplace.json` is the evidence that these plugins
are declarations only),
[`roadmap.md`](../plans/roadmap.md) (the `dropComputedTables` over-drop this touches).

---

## 1. What exists, precisely — and what was deleted

### 1.1 The three, and where they went

| Place | What it held | Today |
| :--- | :--- | :--- |
| [`packs/claude/derive.lua`](../../packs/claude/derive.lua) | the `plugin` table (three ids) → `enabledPlugins`, enabled when `ctx.lsp_servers[lang]` was set and tombstoned otherwise | the table is gone and the derive writes **no `enabledPlugins` at all**; `env.ENABLE_LSP_TOOL` when any LSP is configured is all that remains |
| [`internal/entrypoint/claude.go`](../../internal/entrypoint/claude.go) | `claudeLSPPluginOrder`, the same three pairs | **deleted**; the file is the tombstone comment naming its replacement |

Two copies of one fact, one in Lua and one in Go, with nothing pinning them together. Both are
deleted, and [`jailcontent.writeLSPPlugin`](../../internal/jailcontent/lspplugin.go) renders one
manifest from the whole `lsp_servers` table in their place.

**What installed those three plugins, from 2026-09-20 until they were deleted: nothing.** The Go
table was read by `installClaudePlugins`, which diffed Claude's own `installed_plugins.json`
against the configured servers and shelled out to `claude plugins install|uninstall`. That function
— and the `claude_plugins` hook that was its only entry point — were **deleted 2026-09-20**
(`01263ab2`), ruled by [`pi-pack-extensions.md`](./pi-pack-extensions.md)
[`OQ-2`](./pi-pack-extensions.md#10-decision-ledger): yolo PLACES a plugin tree, it does not run a
vendor install verb inside a jail. `packdecl` refuses the hook name with that migration, and
`packs/claude/pack.json` declares it no longer.

**So the derive spent two days writing a cheque the tree could not cash**, and that is the sharper
motivation than the one this doc opened with. A jail with `lsp_servers.go` configured told Claude to
enable `gopls-lsp@claude-plugins-official` while no code put that plugin in the cache: the three
languages were not merely arbitrary and duplicated, they were **inert**. The question was never
"why only three" but *"how does a configured LSP server reach
Claude at all"*, and option D is the answer the retirement assumed and did not build.

⚠ **Those marketplace ids still appear in a test, and there they are the USER's, not yolo's.**
`internal/entrypoint/prism_claude_test.go` puts `pyright-lsp` and `gopls-lsp` in a HOST
`settings.json` layer and asserts a jail render leaves them enabled — the data-loss case that ended
the derive's tombstones. An id yolo asserts nothing about has to survive composition, so that test
is about ownership and not about a yolo table.

### 1.2 What a plugin actually is — a declaration, not code

Every `*-lsp` plugin in the `claude-plugins-official` marketplace carries a single
`lspServers` entry and nothing else. `gopls-lsp`, in full:

```json
{
  "name": "gopls-lsp",
  "source": "./plugins/gopls-lsp",
  "strict": false,
  "lspServers": {
    "gopls": { "command": "gopls", "extensionToLanguage": { ".go": "go" } }
  }
}
```

No hooks, no commands, no scripts. The marketplace publishes **thirteen**: `clangd`,
`csharp`, `gopls`, `jdtls`, `kotlin`, `liquid`, `lua`, `php`, `pyright`, `ruby`,
`rust-analyzer`, `swift`, `typescript`. yolo shipped three of them and now ships none: the
plugin it renders declares whatever `lsp_servers` names.

### 1.3 yolo already owns the table the plugins restate

`lsp_servers` is a yolo config key, and it is the canonical table. Two small **hardcoded name
maps** sat beside it. Neither should exist; one is gone and one is not:

- **YOLO's install registry.** [`internal/config/lsp.go`](../../internal/config/lsp.go)'s
  `lspInstallRecipes` maps three *server names* (`python`, `typescript`, `go`) to a package that
  provides them — it **picks a server**. That is an opinion, and it is to be **deleted**, not
  bounded: there is more than one Python server and more than one Rust server, and naming one
  "the" server is recommending a tool, which is not YOLO's call. The `command` a `lsp_servers`
  entry already declares *is* the user's choice; YOLO's job is to make that command exist, not to
  choose it.
- **Claude's plugin map.** The three `name → plugin id` entries — the same opinion one layer out,
  each id naming a plugin that names a specific server. ✅ **Deleted 2026-09-22** with option D.

**The rule this rests on.** YOLO holds no opinion about *which* tool serves a capability — only
about how to make a tool the **user named** work in this environment. **Shaping is fine; picking
is not.** A preset that runs Chrome DevTools inside the jail, or a bridge that reaches a provider,
shapes an environment the tool has to fit. "This is the Python server" picks a tool, and the
choice belongs to the user.

**The plan to remove the opinion.** Step 3 shipped 2026-09-22; steps 1 and 2 are open.

1. **Delete `lspInstallRecipes`** and the install plumbing that consumes it
   (`[`internal/config/lsp.go`](../../internal/config/lsp.go)`'s `LSPInstalls`, and the
   `YOLO_LSP_NPM_INSTALL` / `YOLO_LSP_GO_INSTALL` env it feeds in
   [`macosuser/runplan.go`](../../internal/macosuser/runplan.go) and
   [`entrypoint/serverrefresh.go`](../../internal/entrypoint/serverrefresh.go)). A configured
   server's `command` must then resolve on `PATH`, full stop.
2. **The user brings the server** — via `mise_tools`, a pack `program`, or an absolute `command`
   — the way any other host tool arrives. That moves the choice to where it belongs and removes
   the only place YOLO installs a tool it chose for the user.
3. ✅ **Claude's map went with the generated plugin** (option D, built 2026-09-22): one plugin whose
   `lspServers` is rendered from the user's own `lsp_servers`, so there are no per-language ids to
   pick.

**Opinions found elsewhere.** `mcp_presets` ships two MCP servers YOLO chose, and they fall on
opposite sides of the line:

- **`chrome-devtools` is shaping and the capability stays** — as a builtin **pack** rather than a
  preset ([`OQ-MP2`](./mcp-presets-removal.md#decision-ledger)). Its preset carries
  jail-specific argv (`chromeDevtoolsArgs`) and a wrapper around a resolved chromium — a browser
  that runs *in this jail*, which a user could not write down without knowing the environment.
- **`sequential-thinking` is picking, and it goes.** Its preset is a command and one argument
  (the vendored server binary) with **no** jail-specific config — a user who wants it names it in
  `mcp_servers` in one line, so the preset adds nothing but YOLO's recommendation. Removing it
  means dropping the name from `validMCPPresets` ([`internal/config/config.go`](../../internal/config/config.go)),
  the preset map in [`internal/entrypoint/mcp.go`](../../internal/entrypoint/mcp.go), the npm
  install in `mcpPresetNpmPackages` ([`internal/entrypoint/shell.go`](../../internal/entrypoint/shell.go)),
  and the two doc copies; a config that still names it then fails `mcp_presets` validation with
  the valid set, which is the honest outcome. **RULED and not yet built** —
  [`OQ-MP1`](./mcp-presets-removal.md#decision-ledger) drops it with no replacement, and
  the whole `mcp_presets` key retires with it; the removal also stops installing the npm package.

**Drift, fixed.** The `yolo init` template claimed a `mise_tools` default of `neovim`, and the
`lsp_servers` key claimed "default servers (always present)". Neither exists —
[`defaultMiseToolsVals`](../../internal/config/config.go) is empty and no LSP server is default —
so both are corrected in the template and in `yolo config-ref`.

### 1.4 Copilot is generic through a config key; Claude through a plugin

Copilot's [`derive`](../../packs/copilot/derive.lua) is generic, and there is **no map**: it is
a short function that copies `command`, `args` and `fileExtensions` for *every* entry in
`ctx.lsp_servers` into Copilot's native `lspServers` config. It is generic for one reason —
**Copilot's config format is the same shape as YOLO's canonical table**, so nothing had to be
invented and nothing exceeds the derive's reach.

Claude took the long way for the mirror reason: **Claude has no settings-level "here is an LSP
server" key** ([OQ-LSP3](#OQ-LSP3) measured that, and it is why [option E](#3-options) is dead). A
server can only reach Claude through a plugin that names the command, which is why three plugin ids
were once hand-written. The asymmetry was never that Copilot was done well and Claude poorly — it is
that the two agents consume LSP through different mechanisms, and only one matches YOLO's own table.
**Both are generic now:** a user who configures `rust` gets it for Copilot from the derive and for
Claude from the rendered plugin. The route still differs — Claude's is a plugin — but
`enabledPlugins` is no longer part of it, because a plugin on the skills-tree arm is enabled by
default.

### 1.5 They are not on by default — and the docs used to say they were

No code merges default LSP servers: [`internal/config/lsp.go`](../../internal/config/lsp.go)
returns two empty strings for an absent `lsp_servers`, and
[`internal/entrypoint/mcp.go`](../../internal/entrypoint/mcp.go) starts `LoadLSPServers` from
an empty map. Both [`internal/cli/template_tail.txt`](../../internal/cli/template_tail.txt) and
[`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) claimed *"Default servers
(always present): python (pyright), typescript, go (gopls)."* **Fixed 2026-09-20** (commit
`3fb79e8f`): both now say no server is default. This resolves [OQ-LSP2](#OQ-LSP2).

## 2. How every agent consumes an LSP server

The decision is not "what do we do about Claude's three" but "what is YOLO's LSP delivery
model, for every agent." Measured against the shipped CLIs, the agents surveyed fall into **four
different situations**, and only some are about the agent's capability:

| Agent | LSP? | How a server is named | YOLO today |
| :--- | :--- | :--- | :--- |
| **Copilot** | native | `~/.copilot/lsp-config.json` → `lspServers` | **fully generic** — the derive projects `lsp_servers` near-verbatim |
| **opencode** | native, built-in (~35 servers) | `opencode.json` → `lsp` | **mechanism present, producer absent** — the surface exists; no `lsp` derive |
| **Claude** | native (LSP tool) | plugin only: `.lsp.json` or `plugin.json.lspServers`; enabled by `enabledPlugins`, or by default on the skills-tree arm | **fully generic** — `jailcontent.writeLSPPlugin` renders one yolo-authored plugin from `lsp_servers` |
| **Pi** | **extension only** | `pi-lens` → `~/.pi-lens/config.json` → `lsp.servers` | **not written** — the MCP analogue is, the LSP one is not |
| **Codex** | **none** | — | no key exists; its config reference names `mcp_servers` 171× and LSP 0× |
| **agy** | **none user-configurable** | its language server is internal; the only route is an MCP bridge | only via the `mcp-language-server` bridge deleted with gemini |

⚠ **The table is a SURVEY, not the agent list.** `rg -l '"kind": "program"' packs/*/pack.json` is
the list ([`AGENTS.md`](../../AGENTS.md)), and it has grown since this survey: `omp` (bin `oh-omp`)
has no row here and was never measured for an LSP surface.

**Three of the "nulls" were wrong.** Saying "Codex, Pi, opencode, agy — no `lsp` surface" was a
statement about *YOLO's config*, not about the agent. Two of those four can take an LSP server
today, one of them through exactly the mechanism Pi already uses for MCP:

- **Pi has no built-in MCP either.** Core Pi is deliberately minimal; YOLO renders
  `~/.pi/agent/mcp.json` for the `pi-mcp-adapter` **package**. LSP is the same shape with the
  same answer: the `pi-lens` extension reads `~/.pi-lens/config.json` under `lsp.servers`, so
  YOLO can project `lsp_servers` there. Two things differ from the MCP case, and both are already
  named in the docs: `pi-lens` must be enabled in `settings.json` `packages`, and `~/.pi-lens`
  must be writable — the read-only-home case `yolo config-ref` documents.
- **opencode** ships LSP first-class and its config file is *already* a YOLO config surface
  ([`packs/opencode/pack.json`](../../packs/opencode/pack.json)); only an `lsp` producer is
  missing from its derive.

**Codex and agy genuinely cannot.** Codex's binary contains no LSP strings and its config
reference never mentions a language server — its only route is a third-party MCP bridge. agy's
internal language server is not user-configurable and its docs never mention LSP; the only route
is the `mcp-language-server` bridge deleted when gemini retired. Those two are facts about the
agents, not gaps in YOLO.

**The four archetypes, and what each costs:** a **config table** (Copilot, opencode — a rename
plus shape moves; a new agent of this kind costs a derive, not a mechanism); an **extension**
(Pi — render the package's config, as YOLO already does for its MCP); a **plugin** (Claude — the
only one needing a manifest, and what option D built); and **none** (Codex, agy — a bridge is a
workaround, not a projection).

**The feature earns its keep**, on the argument MCP already won: most of the surveyed agents can
consume one canonical `lsp_servers` table today or with a small producer. The problem was never
that it is Claude-shaped — it is that the projections were never written, and Claude's is the one
now built. Pi's and opencode's are still owed.

## 3. Options

- **A — keep.** Three hardcoded, two copies. *Cost:* arbitrary, incomplete, duplicated, and the
  reason the hook existed.
- **B — generalize.** Cover the full official set and generate both copies from one table.
  *Cost:* it would encode a per-language opinion — *"this is the Rust server"* — for thirteen
  languages instead of three, which is the thing [§1.3](#13-yolo-already-owns-the-table-the-plugins-restate)
  says to avoid; and it keeps the marketplace dependency and leaves Claude the only agent whose
  LSP set is fixed rather than user-driven.
- **C — delete.** Remove the plugins, the derive table, the Go table and the hook; Claude runs
  without LSP. *Cost:* loses the Claude LSP tool. *Condition:* an actual user.
- **D — generate. ✅ RULED 2026-09-20, BUILT 2026-09-22** ([OQ-LSP1](#OQ-LSP1)). yolo authors
  **one** plugin whose `lspServers` is rendered from the user's `lsp_servers` at boot, delivers it
  like any other content tree, and needs no enable entry. Any language works, no marketplace, no
  hook, no hardcoded list. *Its precondition — Claude must load a locally-authored plugin — was
  MEASURED and met* ([OQ-LSP3](#OQ-LSP3)), and it is the Agent Plugins decision in
  [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger).
- **E — inline. ❌ REFUTED 2026-09-22.** Write the table into Claude's settings directly
  (Copilot's model), with no plugin at all. Claude has no such key: the only producer of an LSP
  config takes a **plugin**, so there is no non-plugin route ([OQ-LSP3](#OQ-LSP3)). Had it existed
  it would have retired D as well as B and C.

## 4. Failure modes

| Failure mode | Before option D | As built |
| :--- | :--- | :--- |
| A configured language outside the three | silent no-op for Claude — and once the install path was deleted, **the three were no-ops too** ([§1.1](#11-the-three-and-where-they-went)) | delivered: every configured server is rendered, and an entry with no `command` is skipped rather than rendered broken |
| The two hardcoded tables drift | silent mismatch | one source — both tables deleted |
| `enabledPlugins` over-dropped on an adopting render | user's other enabled plugins lost ([`roadmap`](../plans/roadmap.md) row 0b) | the derive asserts **no** `enabledPlugins`, so the table is out of `dropComputedTables`' reach; `env` is the assertion that remains, and row 0b is still open for it |
| `lsp_servers` configured, binaries missing | install recipe is the only path | **unchanged** — `lspInstallRecipes` still resolves the same three names. Deleting it is [§1.3](#13-yolo-already-owns-the-table-the-plugins-restate)'s open plan, after which a configured `command` must resolve on `PATH` and a missing binary is reported |

## 5. Open questions

1. ✅ **OQ-LSP1: Keep, generalize, delete, or generate?** The core ruling, and it is shared
   with [`pi-pack-extensions.md`](./pi-pack-extensions.md).

   <!-- vantage: oq id=OQ-LSP1 leaning="Generate (D) if Claude loads a locally-authored plugin, else delete (C) after checking for a user. Generalizing (B) is NOT a fallback: it would hardcode a per-language opinion for thirteen languages, which §1.3 forbids." -->

   _Leaning:_ **D if [`OQ-LSP3`](#OQ-LSP3) says yes, C if nobody uses them — and B is not a
   fallback.** B would name "the" server for thirteen languages, the per-language opinion
   [§1.3](#13-yolo-already-owns-the-table-the-plugins-restate) rules out and the
   `lspInstallRecipes` deletion removes. D makes archetype 2 as generic as archetype 1, so the
   hardcoded list, the marketplace dependency and the hook all go in one move.

   **Answer (2026-09-20): D — generate.**
   > yolo authors **one** plugin whose `lspServers` is rendered from the user's own `lsp_servers`,
   > delivers it as content, and enables it. Any configured language works; no marketplace, no
   > hardcoded ids, no per-language opinion. It makes Claude's archetype — plugin — as generic as
   > Copilot's config table, and it is the only option that *removes* the picking
   > [§1.3](#13-yolo-already-owns-the-table-the-plugins-restate) forbids rather than multiplying
   > it.

   ⚠ **D was taken outright, WITHOUT waiting on [OQ-LSP3](#OQ-LSP3), which made that measurement a
   PRECONDITION of the build rather than a tiebreak.** The leaning had made D conditional — *D if
   [OQ-LSP3](#OQ-LSP3) says yes, C if nobody uses them* — and the ruling dropped the condition. The
   measurement then came back **YES** on 2026-09-22 and the build shipped, so the ruling holds as
   taken. Had it come back negative — a plugin loadable only from a registered marketplace — D would
   have been **unbuildable on that path**, and this ruling would have REOPENED rather than quietly
   downgrading; **B would still not have been the fallback**, because B was rejected on the
   no-picking rule and no measurement can move that.

2. ✅ **OQ-LSP2: Are the "default servers (always present)" claims true?** The code says no
   ([§1.5](#15-they-are-not-on-by-default--and-the-docs-used-to-say-they-were)).

   <!-- vantage: oq id=OQ-LSP2 leaning="Fix the docs (the template and config_ref), or implement the merge — but do not leave the two contradicting." -->

   _Leaning:_ The code is the authority and no default merge exists, so fix the two docs. If a
   default set is wanted, it belongs as an explicit merge in `internal/config/lsp.go` with a
   test, not a sentence.

   **Answer:**
   > **Resolved 2026-09-20** — the docs were fixed (commit `3fb79e8f`); the code was the authority.
   > No default merge exists or is wanted.

3. ✅ **OQ-LSP3: Can Claude load one yolo-authored plugin that declares any language?** D
   depends on a documented local-load path (`--plugin-dir`, a `./`-prefixed marketplace source)
   and on whether a plugin's `lspServers` may list many servers.

   <!-- vantage: oq id=OQ-LSP3 leaning="Measure it alongside the Agent Plugins decision in pi-pack-extensions; if Claude reads a YOLO-authored plugin directly, D is cheap, and if not the marketplace path forces B or C." -->

   _Leaning:_ Measure it, alongside the Agent Plugins decision in
   [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger): whether Claude reads a
   YOLO-authored plugin directly decides whether one artifact can serve every configured
   language.

   **Answer (2026-09-20): measure it — alongside the Agent Plugins decision in
   [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger).**
   > If Claude reads a yolo-authored plugin directly, D is cheap. If it does not — if a plugin
   > must come from a registered marketplace — the marketplace path forces B or C, which is why
   > this is a precondition of [OQ-LSP1](#OQ-LSP1)'s build and not a detail of it.

   **MEASURED 2026-09-22 — the answer is YES, and the precondition is MET.** Claude Code
   **2.1.278** (the binary this jail runs, `CLAUDE_CODE_EXECPATH`), read statically from its Bun
   bundle rather than by starting it, so [`AGENTS.md`](../../AGENTS.md)'s no-agent-tests rule is
   intact.

   **A marketplace registration is not required, and the vendor documents that deliberately.** The
   plugin-set assembler merges **five independent arms** — `session` (the `--plugin-dir` /
   `--plugin-url` flags), `marketplace`, `skill`, `synced` and `builtin` — so a marketplace is one
   source among five rather than the gate. A plugin loaded off a path is stamped with the sentinel
   marketplace name `inline` and **no registry lookup happens on that path**. The only thing that
   closes it is the managed-settings key `disableSideloadFlags`, which Anthropic's own schema text
   describes as *"rejects the `--plugin-dir`, `--plugin-url` … flags at startup. **Closes the
   CLI-flag bypass of `strictKnownMarketplaces`**. … Only honored from managed settings."* A vendor
   calling it a bypass is a vendor supporting it.

   **The route that needs nothing at all is the `skill` arm.** Plugins are auto-loaded from
   `~/.claude/skills/*`, under the `skills-dir` sentinel — **no marketplace, no `enabledPlugins`
   entry, no launch flag, no env var.** That matters more than the flag route because
   **yolo already stages that directory**: the skills staging tree is bind-mounted at
   `~/.claude/skills`, so the delivery mechanism for D already exists and D reduces to rendering one
   more directory into it.

   ```text
   ~/.claude/skills/yolo-lsp/
   └── .claude-plugin/
       └── plugin.json      # lspServers rendered from the user's lsp_servers table
   ```

   > [!IMPORTANT]
   > **The `.claude-plugin/` segment is MANDATORY on the local path.** The loader resolves
   > `join(dir, ".claude-plugin", "plugin.json")` and passes an EMPTY extra-candidates list on that
   > arm, so the root-level `plugin.json` fallback that marketplace and archive installs enjoy does
   > **not** apply. A manifest at the directory root is simply not found.

   **Three further facts, each re-read rather than inherited:**

   - **One plugin may declare many servers.** `lspServers` is *"LSP server configurations keyed by
     server name"* and every key is registered as `plugin:<pluginName>:<serverName>`. The only
     conflict rule is two servers claiming the same file **extension**, which emits an
     `lsp-extension-conflict` **warning**, not a load failure.
   - **An inline/skills-dir plugin is ENABLED BY DEFAULT.** With no settings entry the value falls
     back to `manifest.defaultEnabled !== false`, and for those two sentinels the settings test is
     `!== false` (**opt-out**) where a real marketplace's is `=== true` (opt-in). So the derive
     writes no `enabledPlugins` id for this plugin at all, which is why its three old toggles could
     go entirely.
   - **A plugin may also ship a root `.lsp.json`**, read unconditionally and merged with
     `manifest.lspServers` — a second expression of the same thing, and not needed if the manifest
     carries the table.

   > [!WARNING]
   > **[Option E](#3-options) is settled and it is a NO.** Nothing in Claude's `settings.json`
   > accepts an `lspServers` table: the sole producer of LSP configs takes a **plugin**, and the only
   > settings-side mention of `lspServers` is a hook-scanner exclusion list, not a schema entry. The
   > plugin question does not go away.

   **What it implied, and what was built:** the best of the four possible outcomes — local load
   works *and* `lspServers` takes many servers — so **D as ruled**: one rendered tree, with the
   hardcoded pairs and `claudeLSPPluginOrder` deleted in the same move. [OQ-LSP1](#OQ-LSP1) does not
   reopen, and [option E](#3-options) is dead.

   **What would falsify it:** a Claude version that removes the `skill`/`session` arms or ships
   `disableSideloadFlags` on by default, plus a managed-settings policy on the user's machine that
   sets it. Anyone revisiting this re-reads the installed version's assembler, because every
   citation above is version-pinned to 2.1.278.

## 6. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-LSP1** | ✅ **BUILT 2026-09-22. Generate (D).** yolo authors ONE plugin whose `lspServers` is rendered from the user's `lsp_servers` and delivers it as content — any language, no marketplace, no hardcoded ids, no per-language opinion. The ruling said "and enables it"; no enable step was needed, the skills-tree arm being opt-out rather than opt-in. ✅ Its precondition is MET: [OQ-LSP3](#OQ-LSP3) measured YES on 2026-09-22, so this row does NOT reopen. B remains rejected on the no-picking rule, which no measurement can move | 2026-09-20 · unblocked 2026-09-22 | [§5](#5-open-questions), [§3](#3-options) | ✅ `jailcontent.writeLSPPlugin`; `claudeLSPPluginOrder` and the derive's three-language table deleted, and the derive now asserts no `enabledPlugins` |
| **OQ-LSP2** | No default LSP servers exist; the two docs claiming "always present" were drift and are corrected | 2026-09-20 | [§1.5](#15-they-are-not-on-by-default--and-the-docs-used-to-say-they-were) | `3fb79e8f` |
| **OQ-LSP3** | **Go and measure** — DONE 2026-09-22, and the answer is **YES**. Claude Code 2.1.278 assembles its plugin set from five independent arms, of which marketplace is one; a plugin auto-loads from `~/.claude/skills/*` under the `skills-dir` sentinel with **no marketplace, no `enabledPlugins` entry and no flag**, and is enabled by default there (the settings test is opt-OUT, not opt-in). One plugin may declare MANY servers. ⚠ `.claude-plugin/plugin.json` is mandatory on that path — the root-level fallback does not apply. ⚠ [Option E](#3-options) is REFUTED: settings.json accepts no `lspServers` table. Measured statically from the bundle, so the no-agent-tests rule holds | 2026-09-20 · measured 2026-09-22 | [§5](#5-open-questions) | ✅ the measurement, and the build it unblocked shipped the same day ([OQ-LSP1](#OQ-LSP1)) |

---

## Appendix: the thirteen official LSP plugins

`clangd` · `csharp` · `gopls` · `jdtls` · `kotlin` · `liquid` · `lua` · `php` · `pyright` ·
`ruby` · `rust-analyzer` · `swift` · `typescript` — each a bare `lspServers` declaration.
yolo used to ship `gopls`, `pyright` and `typescript`; it now depends on none of them, which is
why the list is an appendix rather than a table to keep in step.
