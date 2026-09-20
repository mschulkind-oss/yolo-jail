---
title: "Claude's LSP plugins: three hardcoded languages for a table yolo already owns"
date: 2026-09-19
status: in-review
tags: [lsp, claude, plugins, packs, config, derive]
summary: "Claude Code gets language servers through per-language marketplace plugins, and yolo hardcodes exactly three of them in two places while its own canonical lsp_servers table could express any of them. The plugins are pure lspServers declarations — no code — so the question is whether to keep, generalize, delete, or generate them, and whether Claude can load one yolo-authored plugin instead of the marketplace."
vantage:
  status-chip: true
---

# Claude's LSP plugins: three hardcoded languages for a table yolo already owns

**Status:** DESIGN, 2026-09-19. Nothing built.

> **In short.** `packs/claude/derive.lua` and `internal/entrypoint/claude.go` each hardcode
> the same three-language map — python → `pyright-lsp`, typescript → `typescript-lsp`, go →
> `gopls-lsp`, all from the `claude-plugins-official` marketplace. The marketplace actually
> publishes **thirteen** such plugins, and each one is nothing but an `lspServers`
> declaration: a command and an extension→language map. yolo already owns that table
> (`lsp_servers`) and already installs the binaries itself. So the three are an arbitrary,
> duplicated, five-language-short subset of a table yolo could generate.

**Why it matters.** Three reasons, in order of weight. It is the one remaining *reason* the
`claude_plugins` hook exists — a hook whose retirement [pi-pack-extensions.md](./pi-pack-extensions.md)
just reopened, so this doc and that one must be decided together. It is implicated in a live
data-loss defect already on the roadmap: the `enabledPlugins` table the derive writes is what
`dropComputedTables` over-drops. And it is agent-specific residue of exactly the shape this
project keeps deleting: a language list hardcoded in Go, duplicated in Lua, that the user's
own config could have supplied.

**The shape.** Four options: keep (status quo), **generalize** the map to the full official
set, **delete** it where unused, or **generate** a single yolo-authored plugin from the
configured `lsp_servers` so any language works with no marketplace and no hook. The last is
the attractive one and depends on whether Claude can load a locally-authored plugin, which is
the Agent Plugins decision in [pi-pack-extensions.md](./pi-pack-extensions.md#10-decision-ledger).

**Needs your ruling:** [OQ-LSP1](#OQ-LSP1), [OQ-LSP2](#OQ-LSP2), [OQ-LSP3](#OQ-LSP3).

**Reads with:** [`mcp-configuration.md`](../reference/mcp-configuration.md) (the canonical
MCP/LSP tables and per-agent projection), [`pi-pack-extensions.md`](./pi-pack-extensions.md)
(plugin delivery and the `claude_plugins` reopen), [`claude-plugins-official`](https://github.com/anthropics/claude-plugins-official)
(the marketplace — its `.claude-plugin/marketplace.json` is the evidence that these plugins
are declarations only),
[`roadmap.md`](../plans/roadmap.md) (the `dropComputedTables` over-drop this touches).

---

## 1. What exists today, precisely

### 1.1 The three, in two places

| Place | What it holds |
| :--- | :--- |
| [`packs/claude/derive.lua`](../../packs/claude/derive.lua) | the `plugin` table (three ids) → `enabledPlugins` (enable when `ctx.lsp_servers[lang]` is set, tombstone otherwise) and `env.ENABLE_LSP_TOOL` when any LSP is configured |
| [`internal/entrypoint/claude.go`](../../internal/entrypoint/claude.go) | `claudeLSPPluginOrder`, the same three pairs, used by `installClaudePlugins` ([`boot.go`](../../internal/entrypoint/boot.go)) to install/uninstall them via the vendor CLI |

Two copies of one fact, one in Lua and one in Go, with nothing pinning them together.

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
`rust-analyzer`, `swift`, `typescript`. yolo ships three of them.

### 1.3 yolo already owns the table the plugins restate

`lsp_servers` is a yolo config key, and it is the canonical table. Two small **hardcoded name
maps** sit beside it, and neither should exist:

- **YOLO's install registry.** [`internal/config/lsp.go`](../../internal/config/lsp.go)'s
  `lspInstallRecipes` maps three *server names* (`python`, `typescript`, `go`) to a package that
  provides them — it **picks a server**. That is an opinion, and it is to be **deleted**, not
  bounded: there is more than one Python server and more than one Rust server, and naming one
  "the" server is recommending a tool, which is not YOLO's call. The `command` a `lsp_servers`
  entry already declares *is* the user's choice; YOLO's job is to make that command exist, not to
  choose it.
- **Claude's plugin map.** The three `name → plugin id` entries. The same opinion one layer out:
  each id names a plugin that names a specific server.

**The rule this rests on.** YOLO holds no opinion about *which* tool serves a capability — only
about how to make a tool the **user named** work in this environment. **Shaping is fine; picking
is not.** A preset that runs Chrome DevTools inside the jail, or a bridge that reaches a provider,
shapes an environment the tool has to fit. "This is the Python server" picks a tool, and the
choice belongs to the user.

**The plan to remove the opinion.**

1. **Delete `lspInstallRecipes`** and the install plumbing that consumes it
   (`[`internal/config/lsp.go`](../../internal/config/lsp.go)`'s `LSPInstalls`, and the
   `YOLO_LSP_NPM_INSTALL` / `YOLO_LSP_GO_INSTALL` env it feeds in
   [`macosuser/runplan.go`](../../internal/macosuser/runplan.go) and
   [`entrypoint/serverrefresh.go`](../../internal/entrypoint/serverrefresh.go)). A configured
   server's `command` must then resolve on `PATH`, full stop.
2. **The user brings the server** — via `mise_tools`, a pack `program`, or an absolute `command`
   — the way any other host tool arrives. That moves the choice to where it belongs and removes
   the only place YOLO installs a tool it chose for the user.
3. **Claude's map goes with the generated plugin** (option D): one plugin whose `lspServers` is
   rendered from the user's own `lsp_servers`, so there are no per-language ids to pick.

**Opinions found elsewhere.** `mcp_presets` ships two MCP servers YOLO chose, and they fall on
opposite sides of the line:

- **`chrome-devtools` is shaping and stays.** Its preset carries jail-specific argv
  (`chromeDevtoolsArgs`) and a wrapper around a resolved chromium — a browser that runs *in this
  jail*, which a user could not write down without knowing the environment.
- **`sequential-thinking` is picking, and should go.** Its preset is a command and one argument
  (the vendored server binary) with **no** jail-specific config — a user who wants it names it in
  `mcp_servers` in one line, so the preset adds nothing but YOLO's recommendation. Removing it
  means dropping the name from `validMCPPresets` ([`internal/config/config.go`](../../internal/config/config.go)),
  the preset map in [`internal/entrypoint/mcp.go`](../../internal/entrypoint/mcp.go), the npm
  install in `mcpPresetNpmPackages` ([`internal/entrypoint/shell.go`](../../internal/entrypoint/shell.go)),
  and the two doc copies; a config that still names it then fails `mcp_presets` validation with
  the valid set, which is the honest outcome. **Kept as a recommendation, not yet done** — the
  removal also stops installing the npm package and touches five test files.

**Drift, fixed.** The `yolo init` template claimed a `mise_tools` default of `neovim`, and the
`lsp_servers` key claimed "default servers (always present)". Neither exists —
[`defaultMiseToolsVals`](../../internal/config/config.go) is empty and no LSP server is default —
so both are corrected in the template and in `yolo config-ref`.

### 1.4 Copilot is generic; Claude is three

Copilot's [`derive`](../../packs/copilot/derive.lua) is generic, and there is **no map**: it is
a short function that copies `command`, `args` and `fileExtensions` for *every* entry in
`ctx.lsp_servers` into Copilot's native `lspServers` config. It is generic for one reason —
**Copilot's config format is the same shape as YOLO's canonical table**, so nothing had to be
invented and nothing exceeds the derive's reach.

Claude is not generic for the mirror reason: **Claude has no settings-level "here is an LSP
server" key.** Its LSP support comes from plugins, activated by
`enabledPlugins["<plugin>@<marketplace>"]`, so a server can only reach Claude through a plugin
that names the command. That is why three plugin ids are hand-written. The asymmetry is not
that Copilot was done well and Claude poorly — it is that the two agents consume LSP through
different mechanisms, and only one matches YOLO's own table. A user who configures `rust`
therefore gets it for Copilot and silently nothing for Claude.

### 1.5 They are not on by default — and the docs say they are

No code merges default LSP servers: [`internal/config/lsp.go`](../../internal/config/lsp.go)
returns two empty strings for an absent `lsp_servers`, and
[`internal/entrypoint/mcp.go`](../../internal/entrypoint/mcp.go) starts `LoadLSPServers` from
an empty map. Yet
[`internal/cli/template_tail.txt`](../../internal/cli/template_tail.txt) and
[`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) both say *"Default servers
(always present): python (pyright), typescript, go (gopls)."* That is drift: a user who reads
it believes the three run by default, and in a jail with no `lsp_servers` they do not.

## 2. How every agent consumes an LSP server

The decision is not "what do we do about Claude's three" but "what is YOLO's LSP delivery
model, for every agent." Measured against the shipped CLIs, the six agents are **four different
situations**, and only some are about the agent's capability:

| Agent | LSP? | How a server is named | YOLO today |
| :--- | :--- | :--- | :--- |
| **Copilot** | native | `~/.copilot/lsp-config.json` → `lspServers` | **fully generic** — the derive projects `lsp_servers` near-verbatim |
| **opencode** | native, built-in (~35 servers) | `opencode.json` → `lsp` | **mechanism present, producer absent** — the surface exists; no `lsp` derive |
| **Claude** | native (LSP tool) | plugin only: `.lsp.json` or `plugin.json.lspServers`, enabled by `enabledPlugins` | **enable flag + vendor CLI** — no server config written; three plugin ids hand-mapped |
| **Pi** | **extension only** | `pi-lens` → `~/.pi-lens/config.json` → `lsp.servers` | **not written** — the MCP analogue is, the LSP one is not |
| **Codex** | **none** | — | no key exists; its config reference names `mcp_servers` 171× and LSP 0× |
| **agy** | **none user-configurable** | its language server is internal; the only route is an MCP bridge | only via the `mcp-language-server` bridge deleted with gemini |

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

**The archetypes, restated**

1. **Config table** (Copilot, opencode) — the agent reads a server table from a file YOLO owns.
   The projection is a rename plus shape moves (`fileExtensions` → `extensions`, and opencode
   fuses `command`+`args`). A new agent of this kind costs a derive, not a mechanism.
2. **Extension** (Pi) — no built-in LSP; a package supplies it. YOLO renders that package's
   config, exactly as it already does for Pi's MCP.
3. **Plugin** (Claude) — LSP arrives through a plugin, not a settings key. The only archetype
   that needs a manifest, and the one the `generate` option (D) targets.
4. **None** (Codex, agy) — no LSP surface at all; a bridge is a workaround, not a projection.

**So does the feature earn its keep?** Yes, on the argument MCP already won: one canonical
`lsp_servers` table, projected per agent, so a user configures a language server once. Four of
the six agents can consume it today or with a small producer; the other two cannot consume LSP
from anyone. The problem was never that the feature is Claude-shaped — it is that three
projections were never written, and Claude's is the only one that needed a *plugin* rather than a
config key.

## 3. Options

- **A — keep.** Three hardcoded, two copies. *Cost:* arbitrary, incomplete, duplicated, and
  the reason the hook lives.
- **B — generalize.** Cover the full official set and generate both copies from one table.
  *Cost:* it would encode a per-language opinion — *"this is the Rust server"* — for thirteen
  languages instead of three, which is the thing [§1.3](#13-yolo-already-owns-the-table-the-plugins-restate)
  says to avoid; and it keeps the marketplace dependency and leaves Claude the only agent whose
  LSP set is fixed rather than user-driven.
- **C — delete.** Remove the plugins, the derive table, the Go table and the hook; Claude runs
  without LSP. *Cost:* loses the Claude LSP tool. *Condition:* an actual user.
- **D — generate.** Have yolo author **one** plugin whose `lspServers` is rendered from the
  user's `lsp_servers` at boot, deliver it like any other content tree, and enable it. Any
  language works, no marketplace, no hook, no hardcoded list. *Condition:* Claude must load a
  locally-authored plugin, which is the Agent Plugins decision in
  [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger) and
  [OQ-LSP3](#OQ-LSP3).
- **E — inline.** If Claude's settings accept an `lspServers` table directly (Copilot's model),
  write it there with no plugin at all. *Condition:* unknown; the derive currently writes only
  `enabledPlugins`.

## 4. Failure modes

| Failure mode | Today | Desired |
| :--- | :--- | :--- |
| A configured language outside the three | silent no-op for Claude | delivered, or reported |
| The two hardcoded tables drift | silent mismatch | one source |
| `enabledPlugins` over-dropped on an adopting render | user's other enabled plugins lost ([`roadmap`](../plans/roadmap.md) row 0b) | the derive asserts leaf-level, not the table |
| `lsp_servers` configured, binaries missing | install recipe is the only path | unchanged; recipe stays |

## 5. Open questions

1. 💬 **OQ-LSP1: Keep, generalize, delete, or generate?** The core ruling, and it is shared
   with [`pi-pack-extensions.md`](./pi-pack-extensions.md).

   <!-- vantage: oq id=OQ-LSP1 leaning="Generate (D) if Claude loads a locally-authored plugin, else delete (C) after checking for a user; generalizing (B) is the fallback that keeps the marketplace." -->

   _Leaning:_ **D if [`OQ-LSP3`](#OQ-LSP3) says yes, C if nobody uses them, B only as a fallback.** Framed by [§2](#2-how-every-agent-consumes-an-lsp-server): D makes archetype 2 as generic as archetype 1, so the hardcoded list, the marketplace dependency, and the hook all go in one move. C is strictly simplest if the feature has no user. B merely makes the arbitrary set less arbitrary while leaving the archetype un-generic.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-LSP2: Are the "default servers (always present)" claims true?** The code says no
   ([§1.5](#15-they-are-not-on-by-default--and-the-docs-say-they-are)).

   <!-- vantage: oq id=OQ-LSP2 leaning="Fix the docs (the template and config_ref), or implement the merge — but do not leave the two contradicting." -->

   _Leaning:_ The code is the authority and no default merge exists, so fix the two docs. If a
   default set is wanted, it belongs as an explicit merge in `internal/config/lsp.go` with a
   test, not a sentence.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-LSP3: Can Claude load one yolo-authored plugin that declares any language?** D
   depends on a documented local-load path (`--plugin-dir`, a `./`-prefixed marketplace source)
   and on whether a plugin's `lspServers` may list many servers.

   <!-- vantage: oq id=OQ-LSP3 leaning="Measure it alongside the Agent Plugins decision in pi-pack-extensions; if Claude reads a YOLO-authored plugin directly, D is cheap, and if not the marketplace path forces B or C." -->

   _Leaning:_ Measure it, alongside the Agent Plugins decision in
   [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger): whether Claude reads a
   YOLO-authored plugin directly decides whether one artifact can serve every configured
   language.

   **Answer:**
   > _(empty — fill in when decided)_

## 6. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-LSP1** | — | — | — | — |
| **OQ-LSP2** | — | — | — | — |
| **OQ-LSP3** | — | — | — | — |

---

## Appendix: the thirteen official LSP plugins

`clangd` · `csharp` · `gopls` · `jdtls` · `kotlin` · `liquid` · `lua` · `php` · `pyright` ·
`ruby` · `rust-analyzer` · `swift` · `typescript` — each a bare `lspServers` declaration.
yolo ships `gopls`, `pyright` and `typescript`.
