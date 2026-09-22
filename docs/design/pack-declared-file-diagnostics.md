---
title: "Pack-declared file diagnostics: detecting bypassed dotfiles without core agent knowledge"
date: 2026-09-18
status: in-review
tags: [packs, diagnostics, check, dotfiles, isolation]
summary: "When users or coding agents configure tool-specific files (e.g. APPEND_SYSTEM.md) that do not project into isolated jail environments, the configuration is silently inert. This doc designs a generic, declarative mechanism for agent packs to declare bypassed or trapped file patterns, enabling yolo check to detect and warn about inert dotfiles without core knowing about individual agents."
vantage:
  status-chip: true
---

# Pack-declared file diagnostics: detecting bypassed dotfiles without core agent knowledge

**Status:** IN-REVIEW, 2026-09-18. Three open questions ([`OQ-1`](#oq-1), [`OQ-2`](#oq-2), [`OQ-3`](#oq-3)) need ruling.

**Needs your ruling:**
- [`OQ-1`](#oq-1) — Declarative JSON trap patterns vs. executable pack self-checks
- [`OQ-2`](#oq-2) — Diagnostic execution scope: `yolo check` only vs. launch/apply notice
- [`OQ-3`](#oq-3) — Scan root boundaries: host `$HOME` vs. active workspace

The companion sketch is [`pack-declared-file-diagnostics-plan.md`](pack-declared-file-diagnostics-plan.md).

**Reads with:** [`../reference/pack-system.md`](../reference/pack-system.md) (what a pack declares),
[`../reference/agent-briefings.md`](../reference/agent-briefings.md) (the three supported paths for agent instructions),
[`../reference/what-yolo-is.md`](../reference/what-yolo-is.md) (the core separation of concerns).

---

## 1. Context and Diagnosis

A recurring failure mode among users and LLM coding agents is assuming that host dotfiles automatically propagate into sandboxed jail environments. 

In a recent incident in `mschulkind/dotfiles`, an agent attempted to customize Pi's conversational behavior by writing `pi/agent/APPEND_SYSTEM.md` and deploying it to host `~/.pi/agent/` via `rcup`. The agent falsely assumed that:
1. Running `rcup` on the host would project the file into the jail.
2. The jail mounts `/home/agent/.pi` directly from host storage.
3. `APPEND_SYSTEM.md` is the canonical mechanism to configure Pi under YOLO.

In reality:
- YOLO isolates `/home/agent/.pi` as a per-workspace state overlay (`<workspace>/.yolo/state/pi`). Host `~/.pi` is completely unmapped into the jail.
- YOLO's host briefing prepend strictly looks for `~/.pi/agent/AGENTS.md` (the declared `after: "host:.pi/agent/AGENTS.md"` path), ignoring `APPEND_SYSTEM.md`.
- Deploying `APPEND_SYSTEM.md` on the host left the instructions completely invisible and inert in both environments.

While documentation updates (in `configuring-the-jail` and `agent-briefings.md`) state this clearly, static diagnostics in `yolo check` could actively intercept users and agents before they waste time on inert files.

### The Architectural Trap

The naive implementation is to add a check in `internal/cli/check` that looks for `~/.pi/agent/APPEND_SYSTEM.md`.

**This is strictly prohibited by YOLO's core design invariant:**
> **"AGENTS ARE PACKS. Core does not know what an agent is."**

Core has no agent registry and no knowledge of Pi, Claude, Codex, or their proprietary unmanaged configuration files. Hardcoding a check for Pi's `APPEND_SYSTEM.md` in core would violate this separation. Tomorrow, someone using Claude will invent `~/.claude/custom_prompts.md`, or Codex will introduce `~/.codex/instructions.txt`. Core cannot become an unmaintainable lint registry of every agent's proprietary dotfiles.

Therefore, the mechanism **must be generic and declared by the packs themselves**.

---

## 2. Architectural Principles

**P1. Packs own tool semantics; core owns execution.** A pack knows which files its tool reads, which files are deprecated, and which out-of-band files are bypassed by YOLO's isolated overlays. Core only evaluates declarative rules or executes pack-provided checks.

**P2. Zero agent-specific code in core.** Core's diagnostic engine evaluates collections of pack contributions uniformly, with no branching on `agent == "pi"` or string matching on tool names.

**P3. Actionable diagnosis at the point of failure.** Any warning must name both the bypassed file and the canonical replacement (e.g., "Use a pack briefing audience instead of `APPEND_SYSTEM.md`").

---

## 3. Proposed Mechanism

We consider two distinct approaches for packs to declare defensive diagnostics:

### Approach A: Declarative `traps` in `pack.json`

Packs declare known unmanaged, deprecated, or bypassed file patterns as pure data in `pack.json`:

```json
{
  "name": "pi",
  "contributes": [
    {
      "kind": "traps",
      "rules": [
        {
          "path": "~/.pi/agent/APPEND_SYSTEM.md",
          "condition": "exists",
          "level": "warn",
          "summary": "Detected ~/.pi/agent/APPEND_SYSTEM.md — bypassed by YOLO",
          "remedy": "YOLO isolates /home/agent/.pi per workspace. To deliver Pi rules to jails and host alike, use a pack briefing with \"agent\": \"pi\"."
        }
      ]
    }
  ]
}
```

#### Core Responsibility:
When `yolo check` runs on the host (or in a jail), core collects all `traps` contributions across selected packs, resolves paths (e.g. expanding `~` to host home), evaluates the condition, and emits standard check rows.

### Approach B: Pack Self-Checks via Executable Scripts

A pack contributes an executable check that `yolo check` runs:

```json
{
  "name": "pi",
  "contributes": [
    {
      "kind": "self_check",
      "cmd": "scripts/check-traps.sh"
    }
  ]
}
```

The script runs during `yolo check` and emits structured diagnostic output:
```text
WARN: ~/.pi/agent/APPEND_SYSTEM.md exists but is bypassed by YOLO home isolation.
```

**`self_check` is a NEW contribution kind, exactly as `traps` is — B does not ride an existing
facility.** `packdecl`'s contribution set is closed (`packdecl.KnownKinds`, backed by the
`footprints` table) and carries no `self_check` and no `traps`; the one retired kind is `launch`.
The executable self-check that *does* ship is a **loophole manifest's `doctor_cmd`** — run by
`yolo check` through `loopholes.Set.RunDoctorChecks` and graded line by line into FAIL / NOTE / OK
by `check.reportSelfCheckLines` — and a pack reaches it only by shipping a **loophole**. Two
properties of that seam make it the wrong host for a trap, whichever way [`OQ-1`](#oq-1) goes:

- **It is activation-gated.** `yolo check` reaches a `doctor_cmd` only for a loophole that is
  enabled *and* whose `requires` are met; otherwise the row renders `disabled` or `inactive` and
  the command never runs. A bypassed dotfile is inert whether or not any loophole is switched on,
  so the diagnostic has to fire on pack SELECTION.
- **A pack with no loophole has nowhere to hang one** — and `pi`, the motivating case, ships none.

So the real comparison in [§4](#4-evaluation-of-alternatives) is new-kind versus new-kind. What B
additionally costs is the execution seam and an origin gate for a fetched pack's script; `doctor_cmd`
has the precedent (an unapproved pack's comes back `RC=nil`, reported as *withheld* rather than
silently skipped), but a precedent is something to re-implement, not a facility to reuse.

---

## 4. Evaluation of Alternatives

| Dimension | Approach A: Declarative `traps` | Approach B: Executable `self_check` |
| :--- | :--- | :--- |
| **Purity** | Pure JSON data; hermetic, inspectable, fast | Executable shell/binary; requires interpreter |
| **Performance** | Instant (single `os.Stat` in Go) | Subprocess spawn per pack |
| **Extensibility** | Limited to path conditions (`exists`, `not_exists`) | Arbitrary logic (grep file contents, inspect git branches) |
| **Security** | Safe to evaluate without executing untrusted code | Executes script from pack; needs an origin gate |
| **New vocabulary** | one new contribution kind | one new contribution kind, plus an execution seam and that gate |

Neither approach is free — the closed kind set has no member for either
([§3](#3-proposed-mechanism)) — so the choice is not "reuse versus invent". Approach A provides the
tightest fit for file-existence diagnostics without introducing execution overhead or security
concerns during `yolo check`.

---

## 5. Open Questions

1. <a id="oq-1"></a>💬 **[`OQ-1`](#oq-1) — Should file diagnostics be declarative JSON facts in `pack.json` (Approach A) or executable scripts (Approach B)?**
   - **Option (a) [Recommended]:** Declarative JSON under `"kind": "traps"`. Fast, safe, and easily inspectable by `yolo check`.
   - **Option (b):** Executable pack self-checks. More flexible for complex inspections, but slower, executes pack code, and needs an origin gate of its own. Both options cost a new contribution kind — the existing `doctor_cmd` seam belongs to a loophole and is activation-gated ([§3](#3-proposed-mechanism)), so it cannot carry this.

2. <a id="oq-2"></a>💬 **[`OQ-2`](#oq-2) — Where should trap diagnostics run?**
   - **Option (a) [Recommended]:** In `yolo check` only. `yolo check` is the designated diagnostic tool for environment health; launches and applies should stay fast and focused on execution.
   - **Option (b):** In `yolo check` and also as an informational warning during `yolo host apply`.

3. <a id="oq-3"></a>💬 **[`OQ-3`](#oq-3) — Should the path search be restricted to host `$HOME`, or also inspect the workspace?**
   - **Option (a) [Recommended]:** Host `$HOME` only (`~`). Workspace files (`<workspace>/pi/agent/APPEND_SYSTEM.md`) in dotfile repositories may just be source files awaiting installation; checking host `$HOME` catches the actual deployed files.
   - **Option (b):** Both host `$HOME` and workspace paths.

---

## 6. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="decision-ledger"></a>[`OQ-1`](#decision-ledger) | *Pending ruling* | — | — | — |
| [`OQ-2`](#decision-ledger) | *Pending ruling* | — | — | — |
| [`OQ-3`](#decision-ledger) | *Pending ruling* | — | — | — |
