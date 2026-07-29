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

The leaning is (c) because it's the only option that lets the reform reach its actual goal (the
mini-languages gone) *and* keeps the one genuinely-stateful mechanism truthful about being
stateful, instead of disguised as a pure reshape it can never be.

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
