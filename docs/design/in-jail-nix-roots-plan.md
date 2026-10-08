---
title: "Implementation sketch: synchronized workspace Nix retention"
date: 2026-10-07
status: superseded
stage: SUPERSEDED
next: "None: the design was built as the root watcher (in-jail-nix-roots.md §8); read that, not this sketch"
depends-on: [in-jail-nix-roots.md]
tags: [nix, implementation-plan, storage]
---

# Implementation sketch: synchronized workspace Nix retention

**Status:** 2026-10-08. Superseded by the build. The design was built as the root watcher with
managed roots ([in-jail-nix-roots.md §8](in-jail-nix-roots.md#8-what-is-built)), not as the
producer hook this sketch proposed. The design doc's ledger ([NR-D3](in-jail-nix-roots.md#NR-D3)
to [NR-D7](in-jail-nix-roots.md#NR-D7)) records why. This page records only where each part of the
sketch went.

| The sketch proposed | What was built |
| :--- | :--- |
| A producer hook in a maintained nix client, admitting a root while the client still holds its temp root | Not built ([NR-D3](in-jail-nix-roots.md#NR-D3)). The in-jail root watcher reads every root request from a read-only bind of the host's `gcroots/auto`, and pins the store path itself as soon as it reads one ([NR-D7](in-jail-nix-roots.md#NR-D7)). The window left over is the design's [Known limit](in-jail-nix-roots.md#known-limit) |
| A pin/registration API with a post-registration fence | `nixroots.Conn` and `Registrar.Pin`: `AddTempRoot`, `AddIndirectRoot`, `AddTempRoot` on one connection; the order is pinned against the fake daemon |
| A workspace ledger with lease, cap, inspection and release | `nixroots.Registry` under `<workspace>/.yolo/nix-roots`: a 7-day lease, a 64-root count cap, LRU release, and `yolo nix-roots` list, keep, release and prune ([§4.1](in-jail-nix-roots.md#41-workspace-ownership-caps-and-release)) |
| Byte-union closure accounting and a user-wide threshold | Not built. The cap is a count ([NR-D4](in-jail-nix-roots.md#NR-D4)) |
| A host-owned workspace index and host-side housekeeping | Not built. The lifecycle runs in the jail and on demand from `yolo nix-roots prune` on either side ([§4.2](in-jail-nix-roots.md#42-the-numbers-and-when-they-apply)) |
| An isolated-store proof of the fence under a concurrent GC | Not done. The fence's GC behavior is argued from the nix source ([§2.4](in-jail-nix-roots.md#24-a-new-permanent-root-does-not-update-an-active-gc-snapshot)) |
