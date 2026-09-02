---
title: "Workspace Config Approvals: Approving Agent Edits, Trusting Host User Config"
date: 2026-08-29
status: accepted
tags: [config, security, approvals, ux, loopholes, packs]
summary: "Config approval previously diffed the merged (user + workspace) config against a single per-workspace host snapshot. Editing host user config (e.g. adding a loophole or pack) re-prompted across every single jail on the machine. This design eliminates the security theater of prompting for host user config, scoping the approval gate strictly to agent-editable workspace configs, and requiring explicit confirmation on fresh workspace launches."
---

# Workspace Config Approvals: Approving Agent Edits, Trusting Host User Config

**Status:** DECIDED 2026-08-29, **and built the same day** (`27b335ce` — this line said "Nothing
built" because it was written 77 minutes before the implementation landed, and was never updated).
Verified 2026-09-02: `CheckConfigChanges` takes workspace-only config at both call sites
(`internal/cli/run/run.go:210,557` via `LoadWorkspaceConfig`), the legacy-marker branch and
`retireLegacyWorkspaceSnapshot` are deleted, a fresh non-empty workspace prompts
(`internal/config/snapshot.go:211–222`), and `yolo check --accept-config-changes` is wired
(`internal/cli/check/checkcmd.go:56-57`). The `TestCheckConfigChanges*` suite pins every branch,
including `FreshWorkspaceNonTTYRefuses` and `FreshWorkspaceRefusedRecordsNoSnapshot`.

**The short version.** Config change confirmation previously diffed the fully merged config against a per-workspace snapshot (`~/.local/share/yolo-jail/approvals/<container-name>.json`). Adding a user-level setting—such as the `serial` loophole in `~/.config/yolo-jail/config.jsonc`—forced a repetitive approval prompt across every existing workspace jail on the machine. We eliminate approval gating for host user config (which the human already authored with host privileges) and scope the approval baseline strictly to workspace configuration (`yolo-jail.jsonc` + `.local.jsonc`). Workspace config edits require confirmation, and brand-new workspace launches confirm their initial configuration.

**Reads with:**
- [`config-safety.md`](config-safety.md) (the original snapshot-and-diff design and host-side approval move under OQ-D1/OQ-D2)
- [`gate-placement-principle.md`](gate-placement-principle.md) (why asking the human to confirm an edit they made in their own home is theatre)
- [`loophole-activation.md`](loophole-activation.md) (how loopholes are enabled via user vs workspace config)
- [`pack-config-keys.md`](pack-config-keys.md) (scope rules for config keys and settings)

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-S1** | **Drop approval gating for host user config.** Editing `~/.config/yolo-jail/config.jsonc` is trusted on disk immediately with zero prompts. The approval gate protects against agent edits and evaluates workspace config only. | 2026-08-29 | [§1 Principles](#1-principles--verdict), [§3 Proposed Architecture](#3-the-proposed-architecture) |
| **OQ-S2** | **Host-side pre-approval supported via `yolo check --accept-config-changes`.** Power users can validate and record workspace config approval directly from the host terminal (or in nested setups for nested launches), but not from within a standard jail targeting host state. | 2026-08-29 | [§6 Non-Interactive Refusal & Scripting](#6-non-interactive-refusal--scripting) |
| **OQ-S3** | **Fresh workspaces must confirm their config.** A brand-new workspace with no prior snapshot prompts to approve its initial `yolo-jail.jsonc` (diffed against empty), closing OQ-D3's silent first-run window. | 2026-08-29 | [§5 Fresh Workspaces & Migration](#5-fresh-workspaces--migration) |

---

## 1. Principles & Verdict

1. **P1. Put the gate where the authority changes.** Writing `~/.config/yolo-jail/config.jsonc` requires host user access. Prompting the human to confirm an edit they made in their own home directory is security theater ([`gate-placement-principle.md`](gate-placement-principle.md) Test 1). Host user config is trusted on disk immediately and never prompts.
2. **P2. The approval gate protects against the agent.** `/workspace` is bind-mounted `:rw`, so in-jail agents can freely edit `yolo-jail.jsonc`. The human must approve any workspace config modifications before a new or restarted jail launches with those settings.
3. **P3. Zero prompt fatigue from global edits.** Adding a pack, setting a default package, or enabling a loophole (like `serial`) in `config.jsonc` applies to all workspaces instantly without triggering confirmation prompts across any jail.
4. **P4. Fresh workspaces require explicit confirmation.** A repository cloned from the internet or opened for the first time must not silently execute untrusted `yolo-jail.jsonc` declarations. First launch displays the initial workspace config and prompts for confirmation.

---

## 2. Problem & Diagnosis

### 2.1 The N-Jail Reprompt Trap

When a user edits `~/.config/yolo-jail/config.jsonc`—for example, adding the `serial` loophole pack (`"packs": ["serial"]`) or declaring a global tool:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "serial"],
  "packages": ["ripgrep", "jq"]
}
```

The human opens `workspace-alpha` and runs `yolo`:
```console
$ yolo
⚠  Jail config changed since last run:

--- previous config
+++ current config
@@ -1,4 +1,5 @@
 {
   "packs": [
     "claude",
+    "serial"
   ]
 }

Accept these config changes? [y/N] y
```
The human types `y`. `workspace-alpha` launches.

Ten minutes later, the human switches to `workspace-beta` and runs `yolo`. **They are greeted by the exact same prompt with the exact same diff.**
Then `workspace-gamma`. Then every other project on their workstation.

### 2.2 Why This Happened: Conflated Baselines

The root cause was in `internal/config/snapshot.go:198-203` and `internal/cli/run/run.go:354`:

```
┌────────────────────────────────────────────────────────┐
│ Host User Config (~/.config/yolo-jail/config.jsonc)   │
└───────────────────────────┬────────────────────────────┘
                            │ MergeConfig (load.go:292)
┌───────────────────────────▼────────────────────────────┐
│ Workspace Config (yolo-jail.jsonc)                     │
└───────────────────────────┬────────────────────────────┘
                            │
                            ▼
               Combined Effective Config
                            │
                            ▼
              CheckConfigChanges(workspace, cfg)
                            │
               Diff against single snapshot:
   ~/.local/share/yolo-jail/approvals/<container-name>.json
```

1. `LoadConfig(workspace)` merges `userCfg` under `wsCfg`.
2. `CheckConfigChanges` compared this merged result against `ApprovalSnapshotPath(workspace)` (`approvals/<container-name>.json`).
3. Each workspace maintained its own snapshot containing the *entire merged tree*.
4. When `userCfg` changed on the host, **every workspace's snapshot became stale simultaneously**.
5. Approving the change in `workspace-alpha` updated only `approvals/yolo-ws-alpha-1234.json`. `approvals/yolo-ws-beta-5678.json` remained un-updated.

### 2.3 The Security Theater of User Config Prompts

Per [`gate-placement-principle.md`](gate-placement-principle.md):
> *"A gate earns its place only where the authority changes hands. If performing the guarded act already required at least as much authority as the gate protects, the gate is theatre."*

The threat model yolo-jail protects against is an **in-jail AI agent modifying configuration files** in the read-write workspace bind mount without human knowledge.

An agent inside a jail cannot modify `~/.config/yolo-jail/config.jsonc` on the host. Only the human (or a process with host user authority) can edit that file. Intercepting a launch to ask the human "Did you really want to apply the file you just edited?" protects against no adversarial actor. Worse, asking it $N$ times causes **prompt fatigue**, training the user to press `y` without inspecting diffs—the exact condition under which malicious or unintended workspace edits slip through.

---

## 3. The Proposed Architecture

The approval gate is scoped **exclusively to workspace configuration**. Host user config is excluded from snapshot diffing and approval checks entirely.

```
┌──────────────────────────────────────┐     ┌──────────────────────────────────────┐
│       Host User Config               │     │      Workspace Config                │
│ (~/.config/yolo-jail/config.jsonc)   │     │      (yolo-jail.jsonc)               │
└──────────────────┬───────────────────┘     └──────────────────┬───────────────────┘
                   │                                            │
                   │ (Trusted on disk:                          ▼ Diff
                   │  No approval snapshot)  ┌──────────────────────────────────────┐
                   │                         │    Workspace Approval Baseline       │
                   │                         │  (approvals/<container-name>.json)   │
                   │                         └──────────────────┬───────────────────┘
                   │                                            │
                   │                                            ▼
                   │                               Workspace Approval Gate
                   │                               • Matches baseline → Proceed
                   │                               • Differs / Fresh  → Prompt [y/N]
                   │                                            │
                   └─────────────────────┬──────────────────────┘
                                         │
                                         ▼
                               Container Launch
```

### 3.1 Data Model & Locations

| File | Purpose | Location | Content |
| :--- | :--- | :--- | :--- |
| **Workspace Approval Baseline** | Host-side record of last-approved workspace config. | `~/.local/share/yolo-jail/approvals/<container-name>.json` (`paths.ApprovalsDir()`) | `SnapshotJSON(wsCfg)`: Canonical JSON of `yolo-jail.jsonc` + `.local.jsonc` + includes. |
| **Workspace Boot Baseline** | In-jail baseline for `yolo config drift`. | `<workspace>/.yolo/config-boot.json` | Frozen copy of `wsCfg` written at fresh launch. |
| **Delivered Assembled Config** | Full merged config for in-jail tools. | `<workspace>/.yolo/config-assembled.json` | `SnapshotJSON(MergeConfig(userCfg, wsCfg))` written at launch. |

### 3.2 Evaluation Algorithm

During launcher preflight (in `internal/cli/run/preflight.go`):

```
Algorithm: CheckConfigChanges(workspace, isTTY, acceptNonInteractive)

1. Load wsCfg = config.LoadWorkspaceConfig(workspace)
2. currentJSON = SnapshotJSON(wsCfg)
3. snapshotPath = ApprovalSnapshotPath(workspace)

4. If snapshotPath does NOT exist:
     // Fresh workspace launch (OQ-S3)
     diffLines = unifiedDiff([], splitLines(currentJSON), "none (initial launch)", "workspace config")
     If NOT isTTY AND NOT acceptNonInteractive:
       Return (Approved: false, Error: NewChangedNonInteractiveError(diffLines))
     If isTTY:
       accept = prompter.Prompt(diffLines, "Accept initial workspace config? [y/N] ")
       If NOT accept: Return (Approved: false, nil)
     WriteSnapshot(snapshotPath, currentJSON)
     Return (Approved: true, nil)

5. oldJSON = ReadSnapshot(snapshotPath)
6. If oldJSON == currentJSON:
     Return (Approved: true, nil)

7. // Workspace config changed
   diffLines = unifiedDiff(splitLines(oldJSON), splitLines(currentJSON), "previous workspace config", "current workspace config")
   If NOT isTTY AND NOT acceptNonInteractive:
     Return (Approved: false, Error: NewChangedNonInteractiveError(diffLines))
   If isTTY:
     accept = prompter.Prompt(diffLines, "Accept these workspace config changes? [y/N] ")
     If NOT accept: Return (Approved: false, nil)

8. WriteSnapshot(snapshotPath, currentJSON)
   Return (Approved: true, nil)
```

---

## 4. User Experience & Lifecycle Flows

### 4.1 Flow A: User Config Changes (Global Loophole / Pack Install)

1. Human adds `"serial"` loophole to `~/.config/yolo-jail/config.jsonc`.
2. Human runs `yolo` in `workspace-alpha`.
3. Launcher loads `userCfg` and `wsCfg`.
4. `CheckConfigChanges` compares `wsCfg` against `approvals/yolo-ws-alpha.json` (matches!).
5. **`workspace-alpha` launches immediately with zero prompts.**
6. Human runs `yolo` in `workspace-beta`. **Zero prompts.**

### 4.2 Flow B: Agent Modifies Workspace Config

1. In-jail agent adds `"postgresql"` to `/workspace/yolo-jail.jsonc`.
2. Agent requests jail restart. Human restarts `yolo`.
3. `CheckConfigChanges` detects `wsCfg != oldJSON`.
4. Console displays:
   ```console
   ⚠  Workspace config changed since last run:

   --- previous workspace config
   +++ current workspace config (yolo-jail.jsonc)
   @@ -1,3 +1,6 @@
    {
   +  "packages": [
   +    "postgresql"
   +  ],
      "network": {

   Accept these workspace config changes? [y/N] y
   ```
5. Human answers `y`. Snapshot is updated, and jail starts with PostgreSQL.

### 4.3 Flow C: Fresh Workspace First Launch

1. Human clones a new project containing `yolo-jail.jsonc`.
2. Human runs `yolo` in the new workspace.
3. No host-side approval snapshot exists.
4. Console displays the complete initial workspace config:
   ```console
   ⚠  New workspace config detected for this repository:

   --- none (initial launch)
   +++ workspace config (yolo-jail.jsonc)
   @@ -0,0 +1,8 @@
   +{
   +  "packages": [
   +    "go",
   +    "gopls"
   +  ]
   +}

   Accept initial workspace config? [y/N] y
   ```
5. Human confirms with `y`. Baseline is recorded, and jail starts.

---

## 5. Fresh Workspaces & Migration

### 5.1 Closing the Migration Window (OQ-D3 / OQ-S3 Resolution)

[`config-safety.md`](config-safety.md) OQ-D3 documented a residual migration hole: because first runs accepted silently and the presence of `<workspace>/.yolo/config-snapshot.json` was the only signal separating migrations from first runs, an agent could `rm` that file to make its edits look like a first run.

By ruling in **OQ-S3** that fresh workspace launches must explicitly confirm their initial configuration:
- The silent first-run accept is **deleted**.
- An agent deleting its snapshot cannot bypass confirmation: the absence of a host-side snapshot triggers an initial-config confirmation prompt rather than a silent pass.
- Legacy migration markers (`LegacyWorkspaceSnapshotPath`) are fully retired.

### 5.2 Upgrading from Legacy Merged Snapshots

Existing host-side snapshots in `~/.local/share/yolo-jail/approvals/<container-name>.json` contain the merged config (user + workspace keys).

When the upgraded binary encounters a legacy snapshot:
- The snapshot format is upgraded by storing a canonical workspace-only JSON.
- If a workspace snapshot contains user-only keys (such as `packs`), the loader strips the non-workspace keys or prompts once to re-baseline the clean workspace config, preventing false deletion diffs on upgrade day.

---

## 6. Non-Interactive Refusal & Scripting

In CI pipelines and non-interactive scripts:
- If `wsCfg` differs from the approved baseline (or if running for the first time without a baseline) and `--accept-config-changes` is **not** passed, the command exits with code 1.
- `ChangedNonInteractiveError` displays the workspace diff and names the workspace config path:

```console
⚠ Workspace config changed since the last approved launch, and this launch has no terminal to approve it on.

--- previous workspace config
+++ current workspace config
@@ -1,3 +1,4 @@
 {
   "packages": [
+    "postgresql",
     "ripgrep"

A changed workspace config is never accepted without a human.
Revert the change, or approve it for THIS LAUNCH ONLY by re-running with:
  --accept-config-changes
```

### 6.1 Pre-Approving via `yolo check` (OQ-S2)

Power users and automated setup tools on the host can validate and pre-approve workspace config without starting a container:

```console
$ yolo check --accept-config-changes
```

This writes the approved baseline to `~/.local/share/yolo-jail/approvals/<container-name>.json`. Subsequent `yolo` runs in that workspace will launch without prompting.

> [!NOTE]
> `yolo check --accept-config-changes` operates strictly on the host (or in a nested jail for nested container launches). It is disabled inside standard jails targeting host state.

---

## 7. Costs & Invariants

### 7.1 What This Simplifies
- **Zero prompt storms:** User config edits never prompt across any workspace.
- **Deletes dual-snapshot complexity:** No need to synchronize or maintain an `approvals/user.json` baseline.
- **True gate alignment:** The confirmation prompt guards only untrusted, agent-editable files.
- **Clean drift alignment:** `yolo config drift` (in-jail) and `CheckConfigChanges` (host-side) both operate on the exact same workspace-only configuration layer (`LoadWorkspaceConfig`).

### 7.2 What This Costs
- **First-run prompt for new repos:** Brand-new workspaces prompt once at initial startup to confirm `yolo-jail.jsonc`. This is an intentional security trade to close OQ-D3.

### 7.3 Invariants
1. **Host-Side Storage:** Approval snapshots live strictly under `paths.ApprovalsDir()` on the host, never inside the workspace bind mount.
2. **Workspace Scope Only:** `CheckConfigChanges` reads `LoadWorkspaceConfig` and never `LoadConfig`. User config changes cannot trigger a diff.
3. **Fail-Closed Non-Interactive:** Any unapproved workspace config drift in a non-TTY environment without `--accept-config-changes` is fatal.

---

## 8. Alternatives Considered

| Alternative | Summary | Disposition |
| :--- | :--- | :--- |
| **A. Dual Baselines (`user.json` + `workspaces/*.json`)** | Maintain an independent approval baseline for user config and prompt once globally across all workspaces. | **Rejected (OQ-S1).** Prompting for host user config is security theater; writing `~/.config/yolo-jail/config.jsonc` already required host user privileges. |
| **B. Silent First-Run for Workspaces** | Allow brand-new workspaces to launch without a prompt. | **Rejected (OQ-S3).** Opens a migration hole (OQ-D3) and allows cloned repos to run arbitrary workspace package configurations without human review. |
| **C. Re-merging Snapshots on User Edits** | Iterate through all host snapshots and update them when user config changes. | **Rejected as brittle.** Couples independent workspace state and fails for unmounted/removable workspaces. |

---

## 9. Risks & Mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| **Spurious diff on legacy upgrade** | Medium (Old merged snapshot diffs against new workspace-only config) | Clean migration pass strips legacy user keys during baseline compare (§5.2). |
| **First-run prompt fatigue on repo checkout** | Low (One prompt per new cloned repository) | The prompt is clear, one-time per repo, and ensures the human reviews what packages/tools the repo requests. |

---

## 10. Sequencing

1. **Step 1: Scope Approval Engine to Workspace (`internal/config/snapshot.go`)**
   - Update `CheckConfigChanges` signature and implementation to take `wsCfg` (`LoadWorkspaceConfig`) rather than merged `cfg`.
   - Update diff labels to `"previous workspace config"` and `"current workspace config"`.
2. **Step 2: Enforce Initial Workspace Confirmation (`internal/config/snapshot.go`)**
   - Require prompt / `--accept-config-changes` on initial workspace launch (closing OQ-D3).
3. **Step 3: Update Call Sites (`internal/cli/run`)**
   - In `internal/cli/run/run.go` and `internal/cli/run/preflight.go`, pass `wsCfg` to `checkConfigChanges`.
4. **Step 4: Host Pre-Approval in `yolo check` (`internal/cli/check`)**
   - Wire `--accept-config-changes` flag into `yolo check` on the host.
5. **Step 5: Unit & Integration Tests (`internal/config`, `internal/cli/run`)**
   - Verify user config changes do not trigger prompts.
   - Verify workspace config changes trigger prompts.
   - Verify fresh workspace triggers initial prompt.
