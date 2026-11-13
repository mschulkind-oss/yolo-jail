---
status: draft
stage: SKETCH
next: "Complete against the tree once claude-keychain-at-host.md is accepted (OQ-KH1, OQ-KH2 ruled)"
depends-on: [claude-keychain-at-host.md#OQ-KH1, claude-keychain-at-host.md#OQ-KH2]
tags: [plan, macos, keychain, claude, mcp]
---

# Plan seed: Claude's Keychain item as an MCP sign-in source at a Mac's `yolo host`

**Status:** 2026-11-12 — seeded during design; not a hand-off, and incomplete until the design is
accepted. Nothing may be built from it while it is a sketch.

**Design:** [`claude-keychain-at-host.md`](claude-keychain-at-host.md). The design wins on behavior;
the tree wins on fact; this file is the first thing to be wrong.

Blocked on [OQ-KH1](claude-keychain-at-host.md#OQ-KH1) (whether this is built at all) and
[OQ-KH2](claude-keychain-at-host.md#OQ-KH2) (the too-large branch).

## Notes the design surfaced

- **After the Linux slice.** This adds a store to [ID-21](mcp-auth-sharing-plan.md#id-21)'s host
  session; it lands after ID-21, never as a second poll loop.
- **Reuse the exit-code names.** `securityExitError` and its errors in
  `internal/cli/run/hostkeychainenv.go` already map `security`'s exits 44, 36, 51 and 128; the
  existing 2-minute `hostKeychainReadTimeout` is the read bound the design quotes.
- **Service hash.** `internal/claudesecurity.ViewService` computes
  `Claude Code-credentials-<8 hex>` from a directory with NFC normalization. Check whether Claude
  normalizes `CLAUDE_CONFIG_DIR` (the bundle's `GOe` normalizes only
  `CLAUDE_SECURESTORAGE_CONFIG_DIR`; the other path goes through `je()`) before reusing it for the
  `CLAUDE_CONFIG_DIR` case.
- **Lock.** Reuse the source lock of [ID-12](mcp-auth-sharing-plan.md#id-12)
  (`internal/mcpauth/source.go`); only the lock's directory differs (the storage dir, not the
  item).
- **The darwin line to replace.** `hostOnceSignInNote` in `internal/cli/hostmcponce.go` prints the
  not-kept line today, and ID-21 rewords its darwin half. Under [OQ-KH1](claude-keychain-at-host.md#OQ-KH1) A it becomes the
  [§3.5](claude-keychain-at-host.md#35-disclosure) start line.
- **The write command's length** is measured on the exact bytes sent to `security -i`, newline
  included, against 4032; the hex of the JSON is twice its length.
- **Tests run on Linux with a fake `security`** on a fixture path (the pattern
  `claudesecurity_test.go` and the host Keychain env tests use); the real item is the Mac checks'.
- **`source_dir` encoding** for an item ([ID-18](mcp-auth-sharing-plan.md#id-18)) is the
  implementer's, as long as it can never equal a directory path.
