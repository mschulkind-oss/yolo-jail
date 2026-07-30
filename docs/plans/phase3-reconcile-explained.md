# Phase 3, explained: why `reconcile` doesn't fit "make it a `derive` function"

**Status:** explainer, 2026-07-29. Companion to
[pack-declaration-reform-plan.md](pack-declaration-reform-plan.md) (Phase 3) and open question 12
in [../design/pack-declaration-reform.md](../design/pack-declaration-reform.md). This doc exists
because the blocker is easy to state wrong and hard to feel without a concrete example. No new
decisions here — it just makes OQ12 understandable.

## The one-sentence version

Phase 3 says "every config reshape becomes a small **pure** Lua function (`derive`), and we delete
the old reshaping code." That works for 8 of the 9 reshapes we ship. The 9th — how Claude's MCP
servers get written into `.claude.json` — **isn't a reshape, it's a bookkeeping operation that
remembers what it did last time.** A pure function can't remember. So it doesn't fit, and we have
to decide where it goes instead.

## Background: what a "reshape" is, and why `derive` is pure

Several agents want the *same* MCP server, each written in *its own* file format. There is one
canonical form in yolo's config:

```
mcp_servers:
  filesystem: { command: "mcp-fs", args: ["/work"], env: {ROOT: "/work"} }
```

Each agent needs that turned into its own shape. codex wants it almost verbatim; opencode wants
`env` renamed to `environment`, `command`+`args` folded into one array, and two extra keys added.
That per-agent turning-into-its-shape is a **reshape** (the doc calls it a *projection*).

Phase 3's decision (open question 1, already decided) is: **a reshape is a `derive` function** — a
few lines of sandboxed Lua the agent's pack ships, taking one canonical entry and returning that
agent's shape. The whole point of `derive` is that it is a **pure function**: same input → same
output, every time, with no memory and no side effects. That purity is not incidental — the
sandbox forbids a clock, randomness, and disk access precisely so the result is reproducible
(`internal/agentcfg/luahook/sandbox.go`). Purity is what makes the config predictable and
hashable.

**8 of our 9 reshapes are naturally pure:**

| Reshape | What it does | Pure? |
|---|---|---|
| passthrough / rename / fold / inject | codex, opencode, copilot×2, agy — turn one entry into one shaped entry | ✅ a function of the entry |
| tombstone | claude/settings — delete a key (`out.mcpServers = nil`) | ✅ |
| flags (conditional key) | claude/settings — "enable the pyright plugin *if* a Python LSP is configured" | ✅ a function of the LSP table |

All eight are "look at the input, produce the output." Perfect fit for a pure function. If Phase 3
only had these, it would already be done.

## The problem: `reconcile` isn't a reshape, it's *reconciliation*

The 9th case is `claude/config` — how MCP servers land in `~/.claude.json`. That file is special:
it is **agent-owned** (an "RMW" surface — read-modify-write). Claude writes to it too. yolo does
*not* own the whole file; it only manages *its own slice* of the `mcpServers` block, and must leave
everything Claude put there alone.

Now the hard part. Imagine this sequence:

1. **Boot 1.** Your config lists MCP servers `A` and `B`. yolo writes `A` and `B` into
   `.claude.json`.
2. **You, in the jail.** You tell Claude to add server `C` interactively. Claude writes `C` into
   the *same* `mcpServers` block. Now the file has `A`, `B`, `C`.
3. **You edit your config.** You remove `B` from your MCP list. Now your config has just `A`.
4. **Boot 2.** yolo re-renders. The file on disk has `A`, `B`, `C`. Your config has `A`. **What
   should yolo do?**

The right answer: keep `A`, **remove `B`** (yolo added it, you dropped it), **keep `C`** (you added
it, not yolo). Final: `A`, `C`.

Here is why a pure function *cannot* produce that. A pure function sees only two things: your
current config (`A`) and the file on disk (`A`, `B`, `C`). From those two facts alone, **`B` and
`C` are indistinguishable** — both are "in the file but not in your config." To know that `B` is
yolo's to remove and `C` is yours to keep, you need a *third* fact: **what yolo wrote last time**
(`A`, `B`). That fact isn't in the inputs — it's a memory of the previous boot.

That is what `reconcile` does, and how (`internal/entrypoint/prism.go:430`, `reconcileRMWTables`):
it keeps a tiny **sidecar file** listing the server names yolo asserted last boot. On each boot it
**removes exactly those, then adds the current set, and leaves everything else untouched.** The
sidecar is the memory. Remove the sidecar and the safe fallback is "add, never remove" — because
deleting a server the agent added itself is data loss the user can't recover, while a stale entry
is a visible, fixable annoyance.

**So `reconcile` reads and writes disk state across boots. A `derive` function, by contract,
cannot touch disk and cannot remember anything.** They are opposite kinds of thing. This isn't a
Lua limitation to work around — reconciliation is genuinely *stateful*, and `derive`'s whole value
is that it is *stateless*. Forcing reconcile into `derive` would either break the sandbox guarantee
(let Lua read/write files — then every pack's `derive` can too, and the predictability argument
collapses) or silently lose the "keep the agent's own servers" behavior.

## Why this matters for Phase 3

Phase 3's plan was: turn all the reshapes into `derive`, then **delete the old code**
(`computed.go` and `project.go`, the two "reshape mini-languages" the reform exists to remove).
`reconcile` lives *inside* that old code today — it's declared as one flavor of a `computed[]`
block. So "delete the old code" and "reconcile keeps working" are in tension. We can't just delete
it, and we don't want to keep the whole old machinery alive for one feature. Hence the question.

## The options (open question 12)

**(a) Keep a sliver of the old code alive, just for `reconcile`.**
Delete most of `computed.go`/`project.go` but leave the reconcile path. *Cost:* the reform's headline
— "both reshape mini-languages are gone" — becomes "mostly gone," and the old code's validation
rules stay half-alive, which is exactly the kind of half-state this whole effort is trying to
eliminate. Simplest to do, worst to live with.

**(b) Push `reconcile` through the "subprocess projector" escape hatch.**
The reform already has an escape hatch for logic too complex for `derive`: run an external program
(design §3.4). *Cost:* that hatch is explicitly for **pure** arbitrary logic — it gets handed JSON
and returns JSON, with **no filesystem access on purpose**. But reconcile's entire job *is*
filesystem state (the sidecar). So this is the wrong home — it would need us to weaken the one
property that makes the escape hatch safe. Rejected.

**(c) (leaning) Make `reconcile` an explicit RMW-surface feature, declared on its own.**
Stop pretending reconcile is a "reshape." It never really was — even its own code comment says it is
*"only meaningful on an RMW surface."* Give the config kind, when it's an RMW (agent-owned) surface,
a small dedicated field — e.g. `reconcileKeys: ["mcpServers"]` — that says "this key is a managed
dynamic table; track what I asserted and reconcile it." Then:
- the two reshape mini-languages (`computed.go`, `project.go`) **can be fully deleted** as planned —
  the pure reshapes all moved to `derive`;
- `reconcile` **lives honestly as what it is**: a stateful capability of agent-owned files, not a
  reshape hiding in a reshape list;
- the sidecar mechanism (`reconcileRMWTables`) is **unchanged** — only *how it's declared* moves,
  from a `computed[]` entry to a named RMW field.

**(d) (NEW leaning — delete reconcile entirely; yolo owns `mcpServers`, Claude doesn't).**
The question behind the question: *do we even want the behavior reconcile protects?* It exists to
preserve a server the user added through Claude across a jail restart. But **the other four agents
already don't do this** — codex/copilot/opencode/agy write their MCP block as a plain regenerated
layer, so a server added by hand is gone on the next render. That is the reform's own stated
principle, "regenerate, don't reconcile" (`compose.go:73`): your config is the source of truth, and
the rendered file is disposable output. Claude is the *lone* exception, carrying a whole
sidecar-state mechanism nothing else needs.

So (d) is: **declare that yolo owns the `mcpServers` block — Claude may not durably add to it.** No
sidecar, no memory, no reconcile. Each boot, yolo writes exactly the servers in your config; a
server you added via Claude's UI is simply overwritten next restart. This:
- deletes `reconcile`, `reconcileRMWTables`, the managed-name sidecar, AND the last remnant that
  keeps `computed.go`/`project.go` from dying — **more** deletion than (c), not less;
- makes Claude behave like every other agent (one rule for MCP, not "Claude is special");
- is the honest expression of "your config is the definition" — the same thing sealing (`apply
  --sealed`, the environment-manager doc) is built on.

**The cost, stated plainly so it's a real choice:** a server a user adds through Claude's own
`/mcp` UI vanishes on the next jail restart, with no warning at the moment they add it. Three things
make that acceptable, but they should be true:
1. **`.claude.json` stays RMW regardless** — it holds Claude's auth, session history, and project
   state, which yolo must never clobber. (d) narrows yolo's ownership to *just the `mcpServers`
   key* within that file, not the file. So "yolo owns mcpServers" is a per-key claim, not "yolo
   overwrites .claude.json."
2. **The right way to add an MCP server is your config** (`mcp_servers`), which reaches *every*
   agent at once — the whole point of the shared canonical type. Adding via Claude's UI only ever
   configured Claude, so losing it nudges the user to the place that works everywhere.
3. **It should be visible, not silent.** The one thing reconcile got right is not surprising the
   user. (d) is better if yolo *notices* a hand-added server it's about to drop and says so at boot
   ("removing mcpServers.foo — not in your config; add it under mcp_servers to keep it") rather than
   silently overwriting. That boot notice is cheap and turns "data loss" into "a told-you-why
   cleanup."

**(c) vs (d):** (c) *keeps* the preserve-hand-edits behavior but relocates its declaration; (d)
*drops* the behavior and deletes the mechanism. (d) is more deletion and a simpler mental model
("config is the truth, output is disposable, and it's the same for every agent"), at the cost of one
convenience Claude uniquely had. Given reconcile is a one-of-a-kind mechanism for a one-pack case,
and the behavior contradicts the reform's own "regenerate, don't reconcile" principle, **(d) is the
cleaner answer** — provided we add the boot-time "dropping X" notice so it isn't silent.

The earlier leaning was (c). It shifts to **(d) with a visible-drop notice**, unless preserving
hand-added Claude MCP servers turns out to be a workflow people actually rely on — which is the one
thing worth confirming before deleting it.

## Confirmed against Claude Code's live docs (2026-07-29)

Checked whether Claude has a dedicated standalone MCP file yolo could own outright — like it does
for copilot (`~/.copilot/mcp-config.json`, a `computed`-mode surface yolo writes whole). If it did,
this whole problem would vanish. It does not. Per `code.claude.com/docs/en/mcp`, Claude Code stores
MCP servers in exactly these places:

| Scope | Stored in | yolo's relationship |
|---|---|---|
| **User** | **top-level `mcpServers` key in `~/.claude.json`** | this is what yolo writes (`to: mcpServers`) |
| **Local** | `~/.claude.json`, nested **under each project's path** | a different location; yolo does not touch it |
| **Project** | `.mcp.json` in the repo root (team-committed) | a workspace file, not yolo's to write |

Two facts this nails down:

1. **There is no standalone user-level MCP-only file for Claude.** User/local servers live inside
   `~/.claude.json` alongside auth, sessions, and project state — which is *why* yolo can't own the
   file wholesale and why reconcile exists at all. The clean "own a dedicated file" escape (copilot's
   model) is not available for Claude. Confirmed, not assumed.
2. **yolo's `mcpServers` claim is the TOP-LEVEL (user-scope) key only.** A server a user adds at
   *local* scope lands under the project's path, a different key yolo never writes — so option (d)'s
   "yolo owns `mcpServers`, overwrite each boot" would clobber only the top-level user-scope block,
   **not** a locally-scoped server, and never the project's `.mcp.json`. That shrinks (d)'s
   data-loss surface to exactly one case: a server added at *user* scope through Claude's UI. And
   the doc's own guidance is that user scope is for "servers you frequently use across different
   projects" — which is precisely what belongs in yolo's `mcp_servers` config instead, reaching
   every agent, not just Claude.

So (d) is safer than it first looked: it overwrites the one block yolo is trying to own, leaves
local- and project-scoped servers alone, and the only thing lost is a *user-scope* server added via
Claude's UI rather than via yolo config — which the boot-time notice should name.

## What's already done, and what waits on this

Phases 0–2 shipped and don't depend on this at all:
- the kind vocabulary,
- the "what does each pack claim, and do any collide" check (`yolo pack footprint`, `yolo check`),
- persisted per-key provenance + the config-overlay merge mechanism.

**Only Phases 3 and 4 wait on the OQ12 answer** — because Phase 3 is where the old code gets
deleted, and we can't delete it until we know where `reconcile` goes. Answer (a), (b), or (c) and
the rest is mechanical.

## The decision, in one question

> When we delete the two reshape mini-languages, `reconcile` — the *only* reshape that needs to
> remember what it did last boot — has to go somewhere. Do we (a) keep a piece of the old code
> alive for it, (b) force it through the pure-logic escape hatch it doesn't fit, or (c) lift it out
> as its own small RMW-surface feature so the old code can fully die?

**Answer:**
> _(empty — fill in when decided)_
