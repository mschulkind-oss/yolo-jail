---
title: "Extension delivery — implementation sketch"
date: 2026-09-19
status: draft
tags: [pi, extensions, plugins, packs, implementation, plan]
summary: "Implementation sketch for addressed file-tree delivery into agent-declared aliases, and for retiring the claude_plugins hook by seeding Claude's plugin state."
---

# Extension delivery — implementation sketch

**Status:** SKETCH, 2026-09-19 — the design in [`pi-pack-extensions.md`](./pi-pack-extensions.md)
is accepted (all five questions ruled 2026-09-19). This sketch is the build hand-off and gets
a pass against those rulings before it is built from.

> **Notice:** This sketch is a companion to [`pi-pack-extensions.md`](./pi-pack-extensions.md).
> The design doc wins on all behavioral decisions and architecture. Do not build from this
> document while it is stamped `SKETCH`.

**What changed from the first sketch:** the selected architecture moved from **B (addressed
`files` with a `target` slot)** to **D (addressed `files` into an agent-declared alias,
activated by the owning pack's derive)**. The `target` axis is dropped, flat per-file prefixing
is replaced by a per-pack subdirectory, and the `claude_plugins` decision is retired.

---

## 1. Architecture D touchpoints

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
   (decompose into YOLO's canonical tables, or author one YOLO-owned Agent Plugins 1.0 plugin),
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
