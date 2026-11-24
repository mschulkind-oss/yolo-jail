---
title: "Mac Claude lab follow-ups within the existing credential boundary"
status: accepted
stage: DECIDED
next: "Implement the three isolated components, then qualify the private candidate"
tags: [macos-user, claude, credentials]
---

# Mac Claude lab follow-ups

**Status:** 2026-10-27. Explicit maintainer authorization chooses the demonstrated software repairs; native confirmation remains owed. The existing [credential-view design](claude-login-without-interception.md) and [private-temp ruling](macos-user-model.md#OQ-MM8) remain binding.

## Evidence and accepted behavior

The [login-session record](../plans/runbooks/mac-claude-login-session.md#what-the-table-abbreviates-the-2026-10-25-lab-report-retired-into-this-section), including its four follow-up measurements, demonstrates the mechanisms. The 2026-10-27 Mac verification (at `12acfa691`) confirmed the self-mode view path was already repaired (`829717e46`) and the temp adapter absent; the full macos-user suite passed (162 passed, 0 failed, 4 skipped). Both are historical evidence and are not edited by this work.

- A selected Claude pack declares its native sandbox temp-variable adaptation. The final macos-user launch points `CLAUDE_CODE_TMPDIR` at its existing sandbox `TMPDIR`, without adding a shared `/tmp` grant or putting Claude-specific environment into unselected launches. An explicit override survives only when safe within existing temp validation.
- Account-mode Claude gets a write-only, nontransient `security` compatibility adapter: `add-generic-password` fails promptly with exit 50, allowing its measured file fallback. Account reads remain untouched. Self mode gets no adapter.
- Darwin host Claude with the credential view on gets an adapter scoped to the view store's exact `Claude Code-credentials-<hash>` service. Find/delete report not found (44); add fails nontransiently (50). The user's bare `Claude Code-credentials`, other services and commands pass through unchanged. View-off, other agents, other backends and non-Darwin launches are unchanged.
- Both argv and interactive `security -i` stdin forms are covered, including quoting, multiple commands and unknown commands. Forwarding preserves input bytes and process/control behavior; no shell evaluation, credential inspection or secret logging is introduced. The store-specific service is derived from the actual secure-storage environment string: Unicode NFC, SHA-256, first eight hexadecimal digits. This was checked by reading, not executing, the already-installed Claude Code 2.1.310 binary; the maintainer's ASCII store path produces the report's `5d0efea8` suffix. Use a pinned, locally cached normalization dependency and commit its vendor tree rather than approximating Unicode or widening the service scope.
- For a Darwin registration whose broker-written login is later missing, maintenance honors workspace logout. Never-written registrations remain fillable. Machine logout, view retirement, explicit re-registration and other workspaces retain their existing behavior; no canonical machine logout or token-projection widening occurs.

## Boundaries

These are compatibility repairs, not a permission channel. No extra Keychain is read, created, shared, enumerated or deleted. Seatbelt remains the authority for access. The existing self-mode credential-view path is not redone. Subscription metadata, onboarding and Apple Container host networking are separate backlog items.

## Qualification

Each component must first watch a permanent production-caller regression fail, then fix it and record the green run. Fake command spies and broker fixtures replace real auth operations. Scoped unit/race/static checks and Darwin compilation are local evidence, not native Mac execution. The parent owns combined landing gates and publication. The [implementation plan](mac-claude-lab-followups-plan.md) carries ownership and focused native handoff.
