---
title: "Extension delivery — implementation sketch"
date: 2026-09-19
status: draft
tags: [pi, extensions, plugins, packs, implementation, plan]
summary: "Implementation sketch for addressed file-tree delivery into agent-declared aliases, and for retiring the claude_plugins hook by placing plugin trees locally. Superseded by `slots-and-contributions.md` — the `files` + `agent`/`agents` field shape this plan builds is pending rework to the `exposes` model."
---

# Extension delivery — implementation sketch

**Status:** SUPERSEDED, 2026-09-20 — do not build from this. — the design in [`pi-pack-extensions.md`](./pi-pack-extensions.md)
is accepted; **this sketch is not**. The `files` + `agent`/`agents` field shape its touchpoints
([§1](#1-architecture-d-touchpoints)) edit is retired by
[`slots-and-contributions.md`](./slots-and-contributions.md), which makes a destination a second
axis (`exposes`) addressed by the agent `bin` name, never the pack slug. Slices 1–3 landed the
superseded shape; this sketch is the record of what landed and what remains.

> **Authority chain:** [`pi-pack-extensions.md`](./pi-pack-extensions.md) (architecture, accepted)
> → [`slots-and-contributions.md`](./slots-and-contributions.md) (the role model, in-review) →
> this sketch (pending rework). Where this sketch and either design disagree, the design wins.

**What changed from the first sketch:** the selected architecture moved from **B (addressed
`files` with a `target` slot)** to **D (addressed `files` into an agent-declared alias,
activated by the owning pack's derive)**. The `target` axis is dropped, flat per-file prefixing
is replaced by a per-pack subdirectory, and the `claude_plugins` decision is retired.

---

## 1. Architecture D touchpoints

> ⚠ **Superseded field names.** Every `agent`/`agents` below is pending rework to the role model
> in [`slots-and-contributions.md`](./slots-and-contributions.md): a destination becomes an
> `exposes` slot, and an addressed contribution targets the **agent `bin` name**. Read that doc
> before touching these sites; the mechanics (borrowing, the `<into>/<pack>` join, the derive
> source) are the part that survives.

1. **`internal/packdecl/contributes.go`** — allow `agent`/`agents` on `kind: "files"` by
   narrowing the blanket refusal (currently `c.Kind != KindBriefing && c.Kind != KindSkills`)
   to include `KindFiles`. The existing `into`-xor-`agents` rule then applies unchanged: a
   destination (`agent` present) requires `into`; an addressed contribution (`agents` present)
   refuses it.
2. **`internal/packload/mergedest.go`** — extend destination borrowing to `files`, matching a
   contribution's `agents` against a destination's `agent`. Enforce **one files destination
   per agent**: a second is a load error naming both contributions.
3. **`internal/cli/run/packfiles.go`** — an addressed contribution mounts at
   `<destination into>/<contributing pack>/`. The directory branch already exists
   (`case isDir`); this is a join, not a new stager. No per-file rewriting.
4. **`internal/agentcfg/luahook/derive.go`** — add one declared source, the addressed-content
   table, exposed read-only as `ctx.<alias>`. Ordered deterministically by pack name. Requires
   the rule-sharpening in [design §6](./pi-pack-extensions.md#6-what-a-derive-may-read--the-rule-being-sharpened)
   to land first.
5. **`packs/pi/pack.json`** — declare the files alias for `pi`; migrate
   `extensions/yolo-openai-auth.js` into it. Pi's derive needs no activation step (extensions
   auto-discover).
6. **`packs/claude/*` + remove `HookClaudePlugins`** — deliver the LSP plugin trees by file
   (author one YOLO-owned Agent Plugins 1.0 plugin — **not** the "YOLO's canonical LSP tables" path,
   which [`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin) retires: YOLO must not pick a server),
   keep the `enabledPlugins` derive, and delete the hook, the `packdecl.KnownHooks` entry, and
   the `packhook` case.

## 2. Verification & tests

- `internal/packdecl/contributes_test.go` — `agent`/`agents` accepted on `files`; `into` +
  `agents` still refused together; destination requires `into`.
- `internal/packload/mergedest_test.go` — `files` borrowing; the one-destination-per-agent
  refusal.
- `internal/cli/run/packfiles_test.go` — addressed tree lands at `<into>/<pack>/`.
- `internal/agentcfg/luahook/derive_test.go` — the addressed-content source is deterministic
  and ordered.
- **Call-site pins** (this repo's repeated failure): a test that deletes the addressed
  contribution and asserts the mount disappears, and a test that removes the hook and asserts
  no `claude plugins` invocation occurs. A test that only pins the callee is not a test.
- Nested-jail verification for the `cmd/`/`internal/` changes, per `AGENTS.md`.

## 3. Disqualified / deferred

- **Architecture A (`pi_extensions` Go hook).** Rejected: compiles agent-specific code into
  core. See [design §3](./pi-pack-extensions.md#3-the-candidate-architectures).
- **`target` axis.** Dropped.
- **Flat prefixed symlinks.** Replaced by the per-pack subdirectory; no per-file stager.
