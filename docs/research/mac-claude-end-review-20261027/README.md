---
title: "Independent Mac Claude Repair End Review — 2026-10-27"
date: 2026-10-27
---

# Independent Mac Claude repair end review

**Record status:** historical independent review; advisory evidence only. This is not an owner ruling, an accepted design, an executable plan, or a native macOS certificate.

[Complete review opinion](./review.txt) preserves the reviewer’s full supplied final text byte-for-byte.

## Provenance and scope

The independent Sol HIGH review was run `85c9c8a8-e38f-43bf-a24c-8fb6d9b4b7b2`, resolved to `amazon-bedrock / global.openai.gpt-6.1-sol`, with fresh context. Its verified native `reviewer_output` matches the complete opinion preserved in [review.txt](./review.txt). The reviewer covered the complete 53-path `9d040116b531b937d595148a42dcb27cee0c1153..c8de7db109edca31b891cb661bfe6fb7f4928fc5` range, not only the later 41-path security increment. The reviewed source was c8 `c8de7db109edca31b891cb661bfe6fb7f4928fc5`, tree `90caa63fb05e89cbbdd3d356c6b10c57df0e86f9`. The original earlier `173b67c` BLOCK/four-P1 verdict remains historical; this review does not retroactively accept it.

The reviewer reported no issues and an “OK with notes” merge verdict for c8 only. It reports actual candidate checks: CLI controls (1.174 s), actual-Run carrier spoof (0.566 s), and compatibility race controls (1.022 s), all exit 0. Review details and qualifications are in the complete opinion, not replaced by this summary.

## Combined-tree and completion context

The subsequent source bridge was c8 to clean merge `5a1c6a5158941cc0944c9002cf51187e9d47cd53` onto `0aa`, then `1faabc0d43e7e8d7056cd48d8cb224e435513ac9`, adding four test/census repairs. That bridge is not a second independent review of changed behavior. At `1fa`, the primary worktree was clean at tree `da9a93e313acce96b112cc8e9140e814d407bdf9`; the retained verified completion record reports the full ordinary `Done`/CI completion. The local combined-tree measurements were full CI 397.260750354 s and full integration 1008.114860029 s, with matched nested fresh/attach/stop controls zero. These are distinct from the c8 reviewer’s three focused checks.

A separate preserved actual 47B formal final output was REJECTED for missing ALL10 and must not be represented as a green formal handshake. The full ordinary completion and that rejected output are separate evidence; neither upgrades the reviewer’s c8-only scope. Original malformed `just -d` pre-recipe2 and earlier gate failures remain retained, not rewritten as successes.

## Findings and limits

The review found no credential, Keychain, Seatbelt, or bare-host/API-key widening. Relevant authority and follow-up context are the [implemented Claude security source](../../../internal/claudesecurity/claudesecurity.go), [Mac Claude lab follow-ups](../../design/mac-claude-lab-followups.md), and [2026-10-27 Mac verification](../../design/mac-claude-lab-followups.md) (recorded there). These sources, not this historical review, carry implementation and lab details.

Native macOS account/self/Keychain/logout/shell, rootless delivery, reachability, and actual Kitty remain unrun. Nested Linux is not native certification. No publication is claimed. The complete opinion retains the precise run and custody limitations.
