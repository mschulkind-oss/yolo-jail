---
title: "Mac at 1c0e0d50f: Apple Container now starts in a workspace macos-user used, and back again; the .claude-shared-credentials EEXIST no longer reproduces"
date: 2026-11-25
tags: [lab-report, macos, apple-container, macos-user, workspace-home]
summary: "apple-container-start-in-macos-user-workspace does not reproduce at 1c0e0d50f. In one throwaway workspace the sequence was macos-user, then Apple Container, then macos-user again, and every launch ran its command and exited 0. Each setup has its own home under .yolo/setups/<setup>/home. macos-user's .claude-shared-credentials is a link into /Users/_yolojail, and Apple Container's is a directory, so the two layouts never meet."
---

# Mac at `1c0e0d50f`: Apple Container after macos-user in one workspace

**Checked 2026-11-25 on the owner's Mac** (macOS 26.5, darwin/arm64), at main `1c0e0d50f`.

The launches used the dev bundle `just build-dev-bundle` emitted at that commit
(`0.20.1+9.g1c0e0d50f324`). The installed `yolo` is about 180 commits older. Every launch ran:
- in one empty throwaway workspace;
- with stdin closed;
- with every `YOLO_*` variable and `PI_CODING_AGENT_DIR` unset;
- with `YOLO_ALLOW_MISSING_PROGRAMS=1` set, because the owner's `board` program does not build
  on darwin (see the [earlier report](2026-11-25-mac-approvalnotify-sigint-and-host-recheck.md)).

No CI job was running, and the runner's `yolo-ac-builder` stayed up throughout. Nothing in
yolo was changed.

CI is green at `1c0e0d50f`. The x86_64-darwin floor job that the previous report saw failing
passes since `59ca448f2` (run `45664836511`).

## Observed

The backlog item:
[`apple-container-start-in-macos-user-workspace`](../plans/BACKLOG.md#apple-container-start-in-macos-user-workspace).
On 2026-10-29 at `d159a57f0`, Apple Container failed to start in a workspace macos-user had used,
with `mount failed with errno 17: failed to create directory '.claude-shared-credentials'`.

| # | Launch | Command's output | Exit |
| :--- | :--- | :--- | :--- |
| 1 | `yolo run --setup seatbelt-yolo-account -- /bin/sh -c 'echo IN-SANDBOX; id -un'` | `IN-SANDBOX`, `_yolojail` | 0 |
| 2 | `yolo run --setup apple-container-self -- /bin/sh -c 'echo IN-CONTAINER; uname -s'` | `IN-CONTAINER`, `Linux` | 0 |
| 3 | launch 1 again | `IN-SANDBOX`, `_yolojail` | 0 |

No launch printed `EEXIST`, `errno` or a refusal.

**Why it no longer collides.** The workspace now holds one home per setup,
`.yolo/setups/apple-container-self/` and `.yolo/setups/seatbelt-yolo-account/` (`2-setups.txt`).
The entry the old failure named has a different shape in each:

| Setup | `home/.claude-shared-credentials` |
| :--- | :--- |
| `seatbelt-yolo-account` | a symlink to `/Users/_yolojail/.claude-shared-credentials` (`1-layout.txt`) |
| `apple-container-self` | a directory (`drwxr-xr-x+`) |

`.pi-shared-credentials` is the same: a link in one, a directory in the other. So Apple
Container never mounts over macos-user's link.

## Fixed and committed

Nothing. This report only.

## Open

1. *Decision needed:* close `apple-container-start-in-macos-user-workspace`. The per-setup
   homes the workspace-home work introduced appear to have removed the cause. No commit was
   bisected.

## Evidence

All of it is under `/Users/Shared/yolo/evidence/2026-11-25c/`, captured at `1c0e0d50f`
(`HEAD.txt`):

| File | Contents |
| :--- | :--- |
| `build-dev-bundle.txt` | the dev bundle build, with the launcher path it emitted |
| `1-macos-user.txt`, `1-layout.txt` | launch 1's output, and macos-user's home listing after it |
| `2-apple-container.txt`, `2-setups.txt` | launch 2's output, and the workspace's setups directory |
| `3-macos-user-again.txt` | launch 3's output |
