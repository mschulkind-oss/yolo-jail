---
title: "Principle: Stringly-Typed References Fail Closed by Default"
date: 2026-08-29
status: accepted
tags: [principles, validation, packs, config, architecture]
summary: "Establishes the design principle for stringly-typed identifiers and cross-component references in YOLO Jail: unmatched string references must fail closed with fatal load-time diagnostics by default, with permissive skipping restricted to explicit opt-in (e.g. optional: true)."
---

# Principle: Stringly-Typed References Fail Closed by Default

**Status:** PRINCIPLE — cited as a rule by sibling docs, so it is amended rather than rewritten.
Authored **2026-08-29**. Its live test cases are `pack-fragment` target resolution ([`pack-profiles.md`](pack-profiles.md)), loophole capability matching ([`pack-capabilities.md`](pack-capabilities.md)), and config key validation ([`validate.go`](../../internal/config/validate.go)).

**Audience:** anyone designing a manifest field, configuration key, or cross-pack reference where one component names another by string (pack slugs, profile tags, capability identifiers, service names, file slots).

**Sibling principles:** [`extension-point-principle.md`](extension-point-principle.md) (who designs an extension point), [`gate-placement-principle.md`](gate-placement-principle.md) (put the gate where the authority changes), and [`happy-path-principle.md`](happy-path-principle.md) (fill the matrix with one blessed path).

---

## The Principle

> **Every stringly-typed reference across component boundaries must fail closed by default. If a referenced target, capability, or entity does not exist in the active set, it is a fatal error at load time, unless the reference is explicitly marked optional.**

Silence must never mask a typo, a missing dependency, or an unfulfilled contract.

---

## Why

1. **Typos in Strings Are Undetectable Without Closed Verification:** When component A references component B by string (`target: "claude"` vs. `target: "cloude"`), a permissive system silently drops the unmatched payload. The user believes their configuration is active, while the system silently boots in an unconfigured or default state.
2. **The "Silent Skip" Debugging Nightmare:** When an adapter pack or profile fragment silently fails to apply because a target pack wasn't selected, debugging is excruciating. The error presents downstream as mysterious auth failures, missing environment variables, or silent token leaks.
3. **Explicit Intent Beats Implicit Guessing:** If an adapter pack is designed to target *multiple* possible agents (e.g. a general provider pack contributing fragments to `claude`, `pi`, and `codex`), the author knows those targets are opportunistic and can mark them `"optional": true`. If an adapter pack exists *specifically* to adapt Claude to Bedrock (`aws-bedrock`), omitting `claude` from `packs` is almost certainly a user configuration error.

---

## The Four Rules for Stringly-Typed References

### Rule 1: Fail Closed by Default (Required Unless Annotated)
Cross-component references (such as `pack-fragment.target`, `loopholes.requires`, or `profiles.<pack>`) are **required by default**. If the named target is not selected or does not exist in the pack universe, load/resolution aborts immediately.

### Rule 2: Explicit Opt-In for Permissiveness (`optional: true`)
Opportunistic references must declare `"optional": true` explicitly in their manifest or schema. Only when `optional: true` is present does the resolver gracefully skip an unselected target.

### Rule 3: Rich Diagnostics (Name the Unmatched String and Candidates)
A fatal error for a stringly-typed mismatch must never be a generic `"invalid target"`. It must provide:
* The exact offending string (`"cloude"`).
* The declaring component (`pack 'aws-bedrock'`).
* The active candidate set (`available packs: [claude, pi, codex]`).
* A "did-you-mean" suggestion if edit distance is low.
* The explicit remedy (e.g. *"add 'claude' to packs, or mark this fragment 'optional: true'"*).

### Rule 4: Closed Enums Where Fixed, Active Registry Validation Where Open
* **Fixed syntactic slots** (`kind`, `wire_api`, `notch`, `mode`) must be closed Go enums validated at schema parse time.
* **Open semantic slots** (pack slugs, profile names, loophole identifiers) must be validated against the live resolved registry at launch preflight.

---

## Live Census in YOLO Jail

| Mechanism | String Field | Default Semantics | Permissive Opt-In | Failure Mode on Mismatch |
| :--- | :--- | :--- | :--- | :--- |
| **`pack-fragment`** | `target` | **Required** | `"optional": true` | **Fatal resolution error** with pack candidate list |
| **`pack-capabilities`** | `supersedes` | **Required** | N/A (closed namespace) | **Fatal load error** with served capability list |
| **`loopholes`** | `requires` | **Required** | N/A | **Fatal preflight error** naming missing dependency |
| **`pack_profiles`** | `<pack_name>` | **Required** | N/A | **Validation error** if pack is not in configured universe |
| **`env_sources`** | file paths | Permissive on missing | N/A (Graceful skip for host portability) | Silent skip with trace log |
