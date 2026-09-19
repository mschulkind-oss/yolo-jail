---
title: "Why host wrappers don't auto-apply — and whether they should"
date: 2026-09-18
status: accepted
tags: [design, host, apply, wrappers, ergonomics, consent]
summary: "Enabling `host_wrappers: true` installs shims that exec through `yolo host -- <bin>`, but leaves host apply checks off unless `host_apply_on_launch: true` is also configured. Even when enabled, the gate pauses for an interactive confirmation prompt whenever anything would change. This doc diagnoses that split and establishes zero-prompt auto-apply on host launches."
vantage:
  status-chip: true
---

# Why host wrappers don't auto-apply — and whether they should

**Status:** DECIDED, 2026-09-18 — all three questions are ruled ([§7](#7-decision-ledger)); nothing built. Evidence verified against `bf474602`.

> **In short.** `host_wrappers: true` creates shims so running an agent on the host
> automatically routes through `yolo host -- <bin>`, but yolo left staleness checking off
> by default (`host_apply_on_launch: false`). Coupling auto-apply directly to `host_wrappers`
> and eliminating prompts for declared pack updates removes ceremony while preserving user customizations.

**Why it matters.** A user who configures `host_wrappers: true` expects yolo to manage the host agent
environment seamlessly. Instead, updating a pack or editing configuration left the real `$HOME`
silently stale until they discovered `yolo host apply --assert` or the obscure `host_apply_on_launch` key.
And once enabled, routine pack updates interrupted agent launches with confirmation prompts even though
user edits are already protected by capture overlays and RMW isolation.

**The shape.** `host_wrappers: true` implies apply-on-launch by default (with an explicit opt-out
`host_apply_on_launch: false`), and replaces the interactive `[y/N]` launch prompt with **zero-prompt
auto-apply**: whenever an agent is launched through a host wrapper, yolo applies declared pack updates
and launches immediately.

**Cost.** A launch through a host wrapper synchronizes declared pack surfaces into `$HOME`
without an interactive keystroke. Users who want static host configuration must explicitly set
`host_apply_on_launch: false` or `host_management: "none"`.

**Start at [§3](#3-the-settled-design-implied-checking-and-zero-prompt-auto-apply)** — the implied check and auto-apply behavior.

**Needs your ruling:** None. All three questions are ruled ([§7](#7-decision-ledger)).

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
The rationale recorded in [`../reference/host-apply-staleness.md`](../reference/host-apply-staleness.md) was rooted in
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

## 2. Diagnosis: The ergonomics gap and false deletion fears

### Two flags for one intention
When a user sets `"host_wrappers": true`, their explicit intention is: *"I want yolo to manage my host agent environment."*
Leaving `host_apply_on_launch` disabled by default means the wrappers are half-wired:
- You update a pack (or pull a git pack).
- You run `claude` (expecting the new skill or MCP server).
- Nothing happens because `$HOME` is stale.
- You have to know to either run `yolo host apply --assert` by hand, or discover and enable `host_apply_on_launch: true`.

### Confirmation prompts on routine synchronization
The fear that pack updates represent "data loss" requiring a `[y/N]` prompt on launch does not hold under either active
management contract:

1. **Under `host_management: "own"`:** Yolo composes the file from packs and capture overlays. If a user customized a
   key or an MCP server by hand, that edit is captured into the overlay. A changing pack default does not overwrite a captured
   key. And if there is no captured edit, the pack is the declared source of truth: if the pack author added, modified,
   or dropped a server or setting, that is the declared configuration, not data loss.
2. **Under `host_management: "assert"`:** Yolo performs pure RMW (read-modify-write) on its declared keys. Undeclared keys
   authored by the user are left completely untouched. If a pack modifies or stops declaring a setting, yolo updates only
   that declared key.
3. **Under `host_management: "none"`:** Yolo writes nothing to `$HOME`. The staleness gate is already a silent no-op.

The only real "data loss" boundary was legacy unmanaged adoption (`FirstApply` on a home with pre-existing undeclared MCP
servers), which `confirmHostLosses` already handles on the very first apply. On routine wrapper launches, there is no
user data loss to defend against.

### Disconnect from `yolo pack update`
When a user runs `yolo pack update`, they are explicitly asking to refresh their packs. Yet `yolo pack update` updates
the store and lockfile and stops dead without touching the host render. The user is left wondering why their update didn't
take effect on the host.

---

## 3. The settled design: Implied checking and zero-prompt auto-apply

The design resolves this complexity through three unified rules:

### 3.1 Implied auto-check when host_wrappers: true
`host_wrappers: true` is the single master switch for host-wrapper integration ([`OQ-1`](#decision-ledger)).

- If `host_wrappers: true` is set, `host_apply_on_launch` **defaults to `true`**.
- Users can still explicitly disable it with `"host_apply_on_launch": false` if they want static wrappers.
- If `host_management: "none"` is configured, the check remains a silent no-op (yolo never touches `$HOME`).

### 3.2 Zero-prompt auto-apply on launch
Under both active management modes (`"assert"` and `"own"`), [`hostApplyGate`](../../internal/cli/hostapplygate.go)
does not pause for an interactive `[y/N]` prompt ([`OQ-2`](#decision-ledger)):

- When drift between declared packs and `$HOME` is detected at launch, yolo applies the updates automatically and execs
  the agent immediately.
- To maintain visibility without blocking, yolo prints a single concise stderr notice:
  ```text
  yolo host: synchronized host configuration (~/.claude.json)
  ```
- The existing one-way door (`confirmHostLosses`) remains strictly for `FirstApply && EntryLosses` (the initial adoption
  of an unmanaged home with pre-existing servers). Routine updates across already-managed homes apply seamlessly.

### 3.3 Coupling with yolo pack update
When running `yolo pack update` on the host ([`OQ-3`](#decision-ledger)):
- If `host_management` is `"assert"` or `"own"`, `yolo pack update` automatically triggers `host apply --assert`
  for any modified host surfaces.
- Output reports what was updated in `$HOME`:
  ```text
  claude: updated host configuration (~/.claude.json)
  ```
- If `host_management` is `"none"`, it skips host apply cleanly.

---

## 4. Non-Goals

- **No unmanaged host mutations:** This proposal does not touch `host_management: "none"`. If a user declares
  `none`, yolo will never write to their `$HOME`.
- **No changes to jail launches:** In-jail launches remain entirely unaffected. This proposal is purely about
  the host notch (`yolo host -- <bin>` and `host_wrappers`).
- **No bypass of legacy adoption safety:** The `FirstApply` confirmation for pre-existing unmanaged files continues
  to protect initial migrations.

---

## 5. Alternatives considered

### Alternative 1: Keep them separate, but add a `yolo check` warning
- *Idea:* Keep `host_apply_on_launch` defaulting to `false`, but have `yolo check` warn when `host_wrappers: true`
  is set without it.
- *Verdict:* **Rejected.** A warning forces the user to learn two configuration knobs when only one is conceptually
  relevant.

### Alternative 2: Tiered prompting on launch
- *Idea:* Auto-apply additions, but prompt interactively when a key value changes or an MCP server is removed.
- *Verdict:* **Rejected.** As diagnosed in [§2](#2-diagnosis-the-ergonomics-gap-and-false-deletion-fears), pack defaults
  do not clobber user edits (capture overlays preserve user customizations under `own`, and RMW leaves undeclared keys
  alone under `assert`). Prompting on declared pack updates creates needless prompt fatigue.

### Alternative 3: Zero-prompt auto-apply (Accepted)
- *Idea:* When host wrappers are on, apply declared pack updates automatically on launch without prompting.
- *Verdict:* **Accepted ([`OQ-2`](#decision-ledger)).** Matches the user's mental model: yolo seamlessly manages the host agent environment.

---

## 6. Risks and mitigations

| Risk | Mitigation |
| :--- | :--- |
| **R1: Unintended `$HOME` modification** | Restricted to declared pack surfaces under `assert` (pure RMW) or `own` (with capture overlay). `host_management: "none"` remains an absolute block. |
| **R2: Launch latency** | The observe survey takes ~11ms warm. Silent apply adds <15ms. The total launch overhead remains imperceptible. |
| **R3: Scripted / CI environments** | Non-TTY launches no longer refuse over benign pack updates; they auto-apply and exec seamlessly. |

---

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="decision-ledger"></a>[`OQ-1`](#decision-ledger) | `host_wrappers: true` implies `host_apply_on_launch: true` by default; `"host_apply_on_launch": false` is the escape hatch | 2026-09-18 | [§3.1](#31-implied-auto-check-when-host_wrappers-true) | — |
| [`OQ-2`](#decision-ledger) | Zero-prompt auto-apply on launch: yolo synchronizes declared pack updates silently without interactive confirmation | 2026-09-18 | [§3.2](#32-zero-prompt-auto-apply-on-launch) | — |
| [`OQ-3`](#decision-ledger) | `yolo pack update` on the host automatically triggers `host apply --assert` when `host_management` is `"assert"` or `"own"` | 2026-09-18 | [§3.3](#33-coupling-with-yolo-pack-update) | — |
