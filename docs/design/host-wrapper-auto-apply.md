---
title: "Why host wrappers don't auto-apply — and whether they should"
date: 2026-09-18
status: in-review
tags: [design, host, apply, wrappers, ergonomics, consent]
summary: "Enabling `host_wrappers: true` installs shims that exec through `yolo host -- <bin>`, but leaves host apply checks off unless `host_apply_on_launch: true` is also configured. Even when enabled, the gate pauses for an interactive confirmation prompt whenever anything would change. This doc examines why that friction exists, evaluates coupling the keys, and designs an auto-apply policy for host launches."
vantage:
  status-chip: true
---

# Why host wrappers don't auto-apply — and whether they should

**Status:** DESIGN, 2026-09-18. Nothing built. Evidence verified against `bf474602`.

> **In short.** `host_wrappers: true` creates shims so running an agent on the host
> automatically routes through `yolo host -- <bin>`, but yolo leaves staleness checking off
> by default (`host_apply_on_launch: false`). Even when opted in, it blocks on an interactive
> `[y/N]` prompt on every drift. Coupling auto-apply directly to `host_wrappers` and distinguishing
> non-destructive updates from lossy collisions eliminates manual ceremony while preserving host safety.

**Why it matters.** A user who configures `host_wrappers: true` expects yolo to manage the host agent
environment seamlessly. Instead, updating a pack or editing configuration leaves the real `$HOME`
silently stale until they discover `yolo host apply --assert` or the obscure `host_apply_on_launch` key.
And once enabled, routine, non-destructive pack updates interrupt agent launches with confirmation prompts.

**The shape.** Make `host_wrappers: true` imply apply-on-launch by default (with an explicit opt-out
`host_apply_on_launch: false`), and replace the blanket `[y/N]` prompt with a tiered policy: **auto-apply
non-destructive RMW additions/updates silently**, while reserving confirmation prompts (or refusals)
strictly for destructive conflicts and key deletions.

**Cost.** A launch through a host wrapper may now update `$HOME` configuration without an interactive
keystroke if the change is non-destructive. Users who want static host configuration must explicitly set
`host_apply_on_launch: false` or `host_management: "none"`.

**Start at [§3](#3-the-proposal-implied-checking-and-tiered-auto-apply)** — the consolidation and consent tiers.

**Needs your ruling:** [OQ-1](#oq-1--should-host_wrappers-true-imply-host_apply_on_launch-by-default), [OQ-2](#oq-2--what-should-the-default-auto-apply-posture-be), [OQ-3](#oq-3--should-yolo-pack-update-also-trigger-host-apply).

**Reads with:** [`../reference/host-apply-staleness.md`](../reference/host-apply-staleness.md) (the current
staleness gate implementation), [`host-render-target.md`](host-render-target.md) (the host render model),
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) (`host_management` modes),
[`host-wrapper-auto-apply-plan.md`](host-wrapper-auto-apply-plan.md) (the companion sketch).

---

## 1. The status quo and why it got this way

### What happens today
To have yolo manage an agent's host-side environment today, a user must navigate three distinct concepts:

1. **`host_wrappers: true`**: Tells yolo to write launcher shims (e.g. `~/.local/share/yolo-jail/bin/claude`)
   that prepend to `$PATH`. The shim execs `yolo host -- claude "$@"`.
2. **`host_management`** (`"assert"` | `"own"` | `"none"`): Determines how yolo renders into `$HOME`.
   - Defaults to `"assert"`: pure RMW (read-modify-write). Yolo only writes the keys declared in packs,
     leaving user-defined keys intact.
   - `"own"`: Yolo owns the file completely and captures user edits into a sidecar store.
   - `"none"`: Yolo refuses to write any host files.
3. **`host_apply_on_launch`** (`true` | `false`): Defaults to `false`.
   - When `false`: The wrapper shim blindly execs the real binary. If the pack was updated or changed,
     `$HOME` remains stale.
   - When `true`: The wrapper runs a fast (~11ms) observe survey. If drift is detected, it **prompts the user**:
     ```text
     yolo host: 1 managed file would change (~/.claude.json).
       Apply these and launch claude? [y/N]
     ```
   - If accepted (`y`), it runs `host apply` and execs. If declined (`N`), it aborts.
   - If off a TTY with no `YOLO_ACCEPT_CONFIG_CHANGES=1` env var, it refuses outright.

### Why it was built this way
The rationale recorded in [`docs/reference/host-apply-staleness.md`](../reference/host-apply-staleness.md) was rooted in
two strict security and safety positions:

- **P2 (Consent over Disposability):** A jail filesystem is ephemeral; a user's host `$HOME` is permanent.
  The authors took the stance that modifying `$HOME` as a side effect of running an agent required explicit consent.
- **P4 (No Standing Consent in Config):** The designers believed that writing `"auto_apply": true` in a config
  file would constitute "standing consent" — a permanent grant to mutate `$HOME` across every launch forever.
  They therefore restricted the approval to an interactive prompt or an ephemeral environment variable
  (`YOLO_ACCEPT_CONFIG_CHANGES=1`).
- **Orthogonality of Mechanisms:** `host_wrappers` was viewed purely as "generate PATH wrappers", while
  `host_apply_on_launch` was "re-check the render before launching".

---

## 2. Diagnosis: The ergonomics gap

The current separation creates friction that runs counter to the user's mental model:

### 1. Two flags for one intention
When a user sets `"host_wrappers": true`, their explicit intention is: *"I want yolo to manage my host agent environment."*
Leaving `host_apply_on_launch` disabled by default means the wrappers are half-wired:
- You update a pack (or pull a git pack).
- You run `claude` (expecting the new skill or MCP server).
- Nothing happens because `$HOME` is stale.
- You have to know to either run `yolo host apply --assert` by hand, or discover and enable `host_apply_on_launch: true`.

### 2. Routine updates are treated as hazardous collisions
In the common case:
- A pack adds a new skill in `~/.claude/skills/`.
- A pack adds or updates an MCP server in `~/.claude.json`.
- A pack asserts a default setting in a file configured with `host_management: "assert"` (pure RMW) or `"own"`.

Under `host_management: "assert"`, yolo only touches its own declared keys and explicitly preserves user keys.
Under `host_management: "own"`, user edits are captured.
Stopping the user with `Apply these and launch claude? [y/N]` every time a pack or setting advances treats safe,
additive configuration synchronization as if it were an accidental data-clobbering hazard.

### 3. Disconnect from `yolo pack update`
When a user runs `yolo pack update`, they are explicitly asking to refresh their packs. Yet `yolo pack update`
updates the store and lockfile and stops dead without touching the host render. The user is left wondering why
their update didn't take effect on the host.

---

## 3. The proposal: Implied checking and tiered auto-apply

We propose resolving this complexity with two core changes:

### A. Implied Auto-Check when `host_wrappers: true`
`host_wrappers: true` becomes the single master switch for host-wrapper integration.

- If `host_wrappers: true` is set, `host_apply_on_launch` **defaults to `true`**.
- Users can still explicitly disable it with `"host_apply_on_launch": false` if they want static wrappers.
- If `host_management: "none"` is configured, the check continues to be a silent no-op.

### B. Tiered Auto-Apply Policy (Distinguish Additions from Destructive Losses)
Instead of prompting `[y/N]` on *any* detected change, [`hostApplyGate`](file:///workspace/internal/cli/hostapplygate.go#L120)
inspects the nature of the change:

| Tier | Change Type | Examples | Default Action |
| :--- | :--- | :--- | :--- |
| **Tier 1: Safe / Additive** | Purely additive or non-destructive managed key updates | New skill staged; new MCP server added; declared managed key written via RMW where no user key existed | **Auto-apply silently** (with a single stderr notice: `yolo host: applied updated pack surfaces`) |
| **Tier 2: Modification of Managed State** | A managed setting's value changes from a previous render | A pack changes a model setting from `claude-3-5-sonnet` to `claude-3-7-sonnet` | **Auto-apply** under `assert`/`own` (idempotent policy update) |
| **Tier 3: Destructive / Conflicting** | Data loss or conflicting user edits | Overwriting an undeclared key the user wrote by hand; dropping an existing MCP server; removing a user-modified skill | **Prompt `[y/N]` on TTY**, or refuse off TTY unless `YOLO_ACCEPT_CONFIG_CHANGES=1` |

This directly eliminates prompting during ordinary development and pack upgrades, while preserving the safeguard
where it actually matters: preventing the loss of user-authored keys or external customizations.

### C. Coupling with `yolo pack update`
When running `yolo pack update` on the host:
- If `host_management` is `"assert"` or `"own"`:
  - If any updated pack contributes host surfaces, `yolo pack update` runs a host apply survey.
  - If safe (Tier 1/2), it applies the updates immediately and reports:
    ```text
    claude: updated host configuration (~/.claude.json)
    ```
  - If conflicting (Tier 3), it reports the conflict and advises running `yolo host apply --assert`.

---

## 4. Non-Goals

- **No unmanaged host mutations:** This proposal does not touch `host_management: "none"`. If a user declares
  `none`, yolo will never write to their `$HOME`.
- **No changes to jail launches:** In-jail launches remain entirely unaffected. This proposal is purely about
  the host notch (`yolo host -- <bin>` and `host_wrappers`).
- **No elimination of safety gates:** Conflicts that overwrite user data or drop servers continue to prompt
  or refuse.

---

## 5. Alternatives considered

### Alternative 1: Keep them separate, but add a `yolo check` warning
- *Idea:* Keep `host_apply_on_launch` defaulting to `false`, but have `yolo check` warn when `host_wrappers: true`
  is set without it.
- *Verdict:* **Rejected.** A warning forces the user to learn two configuration knobs when only one is conceptually
  relevant.

### Alternative 2: Auto-apply everything silently with no prompts ever
- *Idea:* If `host_wrappers` is on, always apply all changes silently, even destructive key overwrites or server deletions.
- *Verdict:* **Rejected.** Host `$HOME` is not disposable. Silent deletion of a user's custom MCP server or overwriting
  a manually edited API key without confirmation is unacceptable on a user's real workstation.

### Alternative 3: The Tiered Policy (Proposed)
- *Idea:* Safe/additive changes auto-apply silently; true conflict/loss triggers confirmation.
- *Verdict:* **Recommended.** Delivers zero-friction updates for 99% of normal workflows while retaining the safety
  net for genuine conflicts.

---

## 6. Risks and mitigations

| Risk | Mitigation |
| :--- | :--- |
| **R1: Unintended `$HOME` modification** | Tier 1/2 auto-apply is restricted to RMW managed keys and pack-owned skills. Undeclared keys and conflicting edits escalate to Tier 3 (prompt/refusal). |
| **R2: Launch latency** | The observe survey takes ~11ms warm. Tier 1 silent apply adds <15ms. The total launch overhead remains imperceptible. |
| **R3: Scripted / CI breaks** | Non-TTY launches with Tier 1/2 safe changes proceed automatically. Only Tier 3 conflicts refuse without `YOLO_ACCEPT_CONFIG_CHANGES=1`. |

---

## 7. Open Questions

### `OQ-1`: Should `host_wrappers: true` imply `host_apply_on_launch` by default?
- **Option (a) [Recommended]:** Yes. If `host_wrappers: true`, default `host_apply_on_launch` to `true`. Setting
  `"host_apply_on_launch": false` explicitly remains the escape hatch.
- **Option (b):** Deprecate `host_apply_on_launch` as an independent boolean, replacing it with an enum
  `host_apply_on_launch: "auto" | "prompt" | "off"`, defaulting to `"auto"` when wrappers are enabled.

### `OQ-2`: What should the default auto-apply posture be?
- **Option (a) [Recommended]:** Tiered auto-apply: silently apply safe/additive changes (new skills, RMW managed keys,
  non-colliding MCP servers); prompt only when replacing an existing user-defined key or deleting a resource.
- **Option (b):** Always prompt on TTY for any change (status quo when enabled).
- **Option (c):** Full auto-apply for all managed surfaces without prompting (relying on `host apply --revert` or
  git/capture for rollback).

### `OQ-3`: Should `yolo pack update` automatically run `host apply --assert`?
- **Option (a) [Recommended]:** Yes, on the host, whenever `host_management` is `"assert"` or `"own"` and changes
  are Tier 1/2 safe.
- **Option (b):** No, leave `yolo pack update` focused solely on fetching and lockfile updates, letting the wrapper
  launch hook handle the apply.

---

## 8. Decision Ledger

| ID | Status | Decision | Date |
| :--- | :--- | :--- | :--- |
| `OQ-1` | 💬 Open | Pending user ruling | — |
| `OQ-2` | 💬 Open | Pending user ruling | — |
| `OQ-3` | 💬 Open | Pending user ruling | — |
