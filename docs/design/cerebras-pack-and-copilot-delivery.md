---
title: "The cerebras pack and copilot delivery — graduated to the reference tree"
date: 2026-09-18
status: accepted
tags: [design, packs, providers, profiles, cerebras, copilot, delivery, graduated]
summary: "A stub. The as-built account is docs/reference/cerebras-pack-and-copilot-delivery.md — provider reach per agent, copilot's env-only BYOK contract, the Cerebras pack's declarations, and every D-/OQ- ruling. This filename survives only while inbound references are repointed."
---

# The cerebras pack and copilot delivery — graduated to the reference tree

**Status:** GRADUATED, 2026-09-18 — the settled body of this doc moved to
[`../reference/cerebras-pack-and-copilot-delivery.md`](../reference/cerebras-pack-and-copilot-delivery.md).
Nothing is owed here.

> [!IMPORTANT]
> **The design is BUILT, and this file is no longer where it is described.**
> [`../reference/cerebras-pack-and-copilot-delivery.md`](../reference/cerebras-pack-and-copilot-delivery.md)
> is the as-built account: **provider reach** per agent across all seven shipped agent CLIs,
> including the two permanent negatives; copilot's BYOK contract and the three behaviors the
> implementation added beyond the design; the Cerebras pack's declarations as they stand *after*
> the wire bridge shipped; and the
> [`## Why it's this way`](../reference/cerebras-pack-and-copilot-delivery.md#why-its-this-way)
> appendix where every `D-` and `OQ-` id of this doc resolves.
>
> **Read the reference rather than this file for two claims this one got wrong**, both overtaken
> by [`wire-bridge.md`](../reference/wire-bridge.md) before the graduation: `packs/cerebras`
> declares an `anthropic` endpoint (this doc said declaring one "would be a lie about the
> service"), and it carries a `context_window` option (`D-4` said it should not). Claude and
> copilot can ride Cerebras through the bridge, which is what revived both.

**Why it still exists.** Inbound references that could not be repointed in the same commit, all in
files held by concurrent work:

- three links to this file's decision ledger in
  [`protocol-resolution.md`](protocol-resolution.md), which another workflow holds;
- one roadmap row in [`../plans/roadmap.md`](../plans/roadmap.md), which the maintainer owns;
- seven prose citations of this path in the Go and Lua trees —
  `packs/cerebras/README.md`, `packs/copilot/derive.lua`, `packs/claude/derive.lua`,
  `internal/wirebridged/boot.go`, `internal/cli/run/cerebraspack_test.go`,
  `internal/cli/run/copilotbyok_test.go` and `integration/providers_test.go`.

Point each of them at
[`../reference/cerebras-pack-and-copilot-delivery.md`](../reference/cerebras-pack-and-copilot-delivery.md)
(the ledger citations at its
[`#why-its-this-way`](../reference/cerebras-pack-and-copilot-delivery.md#why-its-this-way) anchor)
and **delete this file**, which is what the graduation would otherwise have done here.

## Decision ledger

**Moved.** Every `D-` and `OQ-` ruling now lives in
[`../reference/cerebras-pack-and-copilot-delivery.md`](../reference/cerebras-pack-and-copilot-delivery.md#why-its-this-way)'s
*Why it's this way* appendix, with its original id. This heading survives only so the ledger
citations named above keep resolving until they are repointed; it goes with the rest of this file.
