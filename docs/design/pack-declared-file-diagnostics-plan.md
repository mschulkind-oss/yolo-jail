---
title: "Pack-declared file diagnostics — implementation sketch"
date: 2026-09-18
status: draft
tags: [sketch, packs, diagnostics, check, plan]
summary: "Implementation sketch for pack-declared traps and bypassed file diagnostics in yolo check."
vantage:
  status-chip: true
---

# Pack-declared file diagnostics — implementation sketch

**Status:** SKETCH, 2026-09-18 — incomplete, and unstable while questions are open. Nothing is built.

> **Precedence:** [`pack-declared-file-diagnostics.md`](pack-declared-file-diagnostics.md) leads on all behavioral
> and architectural decisions.

## 1. File and Component Map

| File | Proposed Change |
| :--- | :--- |
| `internal/packdecl/manifest.go` | Add `Traps` contribution definition and JSON unmarshaling. |
| `internal/packload/packload.go` | Collect `Traps` contributions across selected packs. |
| `internal/cli/check/section_traps.go` | New `yolo check` section: iterates declared traps, evaluates conditions, and emits warnings. |
| `packs/pi/pack.json` | Declare initial trap for `~/.pi/agent/APPEND_SYSTEM.md`. |

## 2. Blockers and Decisions

- Blocked on rulings for [`OQ-1`](pack-declared-file-diagnostics.md#oq-1), [`OQ-2`](pack-declared-file-diagnostics.md#oq-2), and [`OQ-3`](pack-declared-file-diagnostics.md#oq-3).
