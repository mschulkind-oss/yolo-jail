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
maps** sit beside it, and the reviewer's question — "are we creating our own registry?" — is
right to separate them:

- **YOLO's install registry.** [`internal/config/lsp.go`](../../internal/config/lsp.go)'s
  `lspInstallRecipes` maps three *server names* (`python`, `typescript`, `go`) to a package that
  provides them. It exists because a `lsp_servers` entry declares a `command` — a binary — not
  an installable *package*, and there is no standard binary→package mapping. But for those three
  names it is also **picking a server**, and picking is an opinion YOLO should not hold: there is
  more than one Rust server and more than one Python server, and naming one "the" server is
  exactly what to avoid. So it stays a **closed** convenience for three names and is deliberately
  **not** extended. A server outside them is the **user's** choice — brought via `mise_tools`, or
  a `command` already on `PATH` — and YOLO never selects it.
- **Claude's plugin map.** The three `name → plugin id` entries. This one is *pure ceremony*:
  the plugin restates a command + extension map that the `lsp_servers` entry already carries.

Neither map invents names — the keys are the user's own `lsp_servers` keys — but both are
incomplete by construction, and the ideal is zero: install follows the entry, and delivery is
generic (options D/E below).

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
model, for every agent." The shipped agents already fall into two archetypes and a null:

| Agent | How an LSP server reaches it | Generic today? |
| :--- | :--- | :--- |
| **Copilot** | native `~/.copilot/lsp-config.json`, projected by the derive | **yes** — no map, its format matches YOLO's table |
| **Claude** | a plugin per language, enabled via `enabledPlugins` | **no** — three hand-written plugin ids |
| **Codex, Pi, opencode, agy** | not configured by YOLO (no `lsp` surface) | n/a |

The archetypes a future agent falls into are therefore fixed, and they are what to decide
against — not Claude's three by themselves:

1. **Native table** — the agent's config accepts an arbitrary server table (Copilot). The
existing derive is generic, and a new agent of this kind costs nothing to support.
2. **Manifest per language** — the agent needs a plugin or extension that names the command
(Claude). This is the only archetype that needs a *map*, and the map today is hand-written,
agent-specific, and three entries long.
3. **No LSP surface** — nothing to do; the canonical table simply does not reach it.

The prism already answers archetype 1 — one canonical table, projected per agent — and
archetype 3 needs nothing. **Archetype 2 is the whole question**, and it generalizes cleanly:
if YOLO can author the manifest itself (option D), then *every* plugin-shaped agent becomes as
generic as Copilot; if it cannot, each such agent needs its own hand-written map (option B),
which is the residue to avoid. Deciding this for Claude is therefore deciding it for the
archetype, and the answer carries to the next plugin-per-language agent rather than being a
Claude special case.

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
