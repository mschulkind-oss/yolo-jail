---
title: "Mac capture runner, first pass: M-0 only, and the owner's macOS approval hook posts nothing"
date: 2026-11-11
tags: [lab-report, macos, approvals, notifications, git-broker, podman]
summary: "At main 77df9ca0d, M-0's inventory is done and M-1 to M-13 cannot run yet: the screen is locked and the session has no Screen Recording access. The owner's on_request hook calls alerter 26.5 with single-dash flags, which it rejects with exit 64, so on this Mac every approval request shows nothing and yolo records no failure. W-GB1 passes 5 of 5 under podman-self; the witness's YOLO_RUNTIME=podman skips the test."
---

# Mac capture runner, first pass

**Run 2026-11-11 on the owner's Mac** (macOS 26.5, build 25F71, arm64; backing scale factor 2;
appearance Light), at main `77df9ca0d`. The checks are the
[Mac capture runner's](../design/approval-experience/mac-capture-runner.md#4-mac-checks) M-0 to
M-13 and [W-GB1 in the git-broker plan](../design/git-broker-plan.md#mac-checks). Nothing was
fixed, and no system setting was changed.

## Observed

| Check | Result |
| :--- | :--- |
| **The owner's `on_request` hook on macOS** | **Shows nothing and answers nothing**, see [§2](#2-the-owners-hook-posts-nothing-on-this-mac) |
| M-0, environment and inventory | **Done**, see [§1](#1-m-0-environment-and-inventory). terminal-notifier 3.1.0 is installed, so the setup passes |
| M-1 to M-13 | **Cannot run yet**: the screen is locked and this session has no Screen Recording access, see [§3](#3-what-blocks-m-1-to-m-13) |
| W-GB1, as written (`YOLO_RUNTIME=podman`) | **Skipped**, all five runs: *"the git-broker chain needs podman, and this runtime is "container""* |
| W-GB1, under `YOLO_SETUP=podman-self` | **Passes 5 of 5**, see [§4](#4-w-gb1) |

### 1. M-0, environment and inventory

- `brew list --versions`: `terminal-notifier 3.1.0` and `alerter 26.5`. `cliclick` is not
  installed, so presses would fall back to AXPress. `jq` is at `/opt/homebrew/bin/jq`.
- terminal-notifier's bundle identifier is `fr.julienxx.oss.terminal-notifier`. alerter is a bare
  binary with no `.app`; its `--sender` defaults to `com.apple.Terminal`.
- `terminal-notifier -diagnose` reported `authorization authorized`, `alerts enabled`,
  `alert style banners`, `lock screen enabled`, `previews when unlocked`, and *"No problems
  found."* So the checks' Alerts style is not set yet.
- **Baseline path:** `command -v alerter` finds it, so the baseline is alerter, not the dialog.
  But see [§2](#2-the-owners-hook-posts-nothing-on-this-mac).
- `who` lists two console sessions, `matt` and `yolo-witness`.

**terminal-notifier 3.1.0's help, against the doc's expectations** (saved as
`$RUN/out/tn-help.txt`):

- `-action` repeats or takes a comma-separated list, and `-group` and `-remove` exist, as
  [§3](../design/approval-experience/mac-capture-runner.md#3-shared-tools) assumes.
- With `-action`, the help names the outcomes as *"the button title, … or one of @ACTIONCLICKED,
  @CLOSED or @TIMEOUT"*. That is `@ACTIONCLICKED`, where M-4 expects `@CONTENTCLICKED` for a body
  click. M-4 still has to measure it.
- `-timeout` *"Prints @TIMEOUT and exits 6 if it expires"*, and without it the tool waits
  indefinitely.
- `-sender` and `-appIcon` are listed as removed.
- `-diagnose` exists, and is a non-GUI way to read authorization and style before M-8 and M-9.

**alerter 26.5 takes only double-dash options**: `--title`, `--message`, `--actions`,
`--close-label`, `--version`. `alerter -closeLabel Ignore …` fails with *"Error: Unknown option
'-closeLabel'. Did you mean '--close-label'?"*. M-11's `al` and M-13's `baseline-hook.sh` use the
single-dash spelling, so as written they would measure a parse error, not a notification.

### 2. The owner's hook posts nothing on this Mac

The user config sets `on_request = "~/.local/bin/yolo-approve-toast"`, a link into the owner's
dotfiles. On macOS there is no `notify-send`, so the hook takes its alerter branch:

```sh
a=$(alerter -title "$title" -message "$YOLO_REQUEST_DESCRIPTION" \
  -actions "Allow once,Allow 1h,Deny" -closeLabel Ignore)
```

That exact shape exits 64 with *"Error: At least one of --message, --remove, or --list is
required."* and posts nothing. The hook then matches no `case` branch and exits 0.

**End to end:** with the `git-push-new-branch` fixture's `hook_env` exported, and a stub `yolo` on
`PATH` that only logs its arguments, `~/.local/bin/yolo-approve-toast` printed that error, exited 0,
and made no `yolo` call.

**What yolo does with it:** `internal/brokerhook/brokerhook.go` records a hook failure on the
request (`noteFailure`) only when the hook exits non-zero, or times out. So this run leaves the
request pending with nothing on screen and no failure line in `yolo approve`.

So until the hook changes, every approval request on this Mac waits unseen.

### 3. What blocks M-1 to M-13

- **The screen is locked.** `CGSessionCopyCurrentDictionary` gives `CGSSessionScreenIsLocked = 1`
  for `matt` on the console.
- **No Screen Recording access.** `screencapture -x` fails with *"could not create image from
  display"*, and `CGWindowListCopyWindowInfo` returns names for only 2 of 27 on-screen windows.
  This session runs under a `tmux` server whose parent is `launchd`, not directly under a terminal
  app.
- **Accessibility works.** System Events answered for `Finder` and `NotificationCenter` processes.
- The checks also change system settings: Alerts style for terminal-notifier, its *Allow
  notifications* off and on (M-8a), Do Not Disturb (M-9), dark mode (M-1). None was changed in this
  pass.

### 4. W-GB1

- `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'` printed
  `true pasta`.
- The witness names main `520b739e5db1`, which is not a commit on main. The diagnostic it describes
  is in `3b1be0bc1`, which `77df9ca0d` contains.
- `YOLO_RUNTIME=podman go test -count=5 …` skipped all five, because this Mac's default setup is
  Apple Container and the integration suite reads `YOLO_SETUP`.
- `YOLO_SETUP=podman-self go test -count=5 -timeout 0 -v -run '^TestGitBrokerConfigBranch$'
  ./integration` passed all five (46.1–46.3 s each, 254 s total). No run printed `bad object` or
  `did not send all necessary objects`. So the nightly's failure did not reproduce here.

## Fixed and committed

Nothing.

## Open

1. [§2](#2-the-owners-hook-posts-nothing-on-this-mac): the owner's hook needs alerter's
   double-dash spelling. Separately, yolo cannot tell a hook that printed an error and exited 0
   from one that showed a prompt. (Owner.)
2. M-1 to M-13 need the screen unlocked, Screen Recording for this session, and the owner's
   go-ahead on the settings they change.
3. M-11 and M-13 use alerter's old single-dash flags. M-4 expects `@CONTENTCLICKED`, where the
   help says `@ACTIONCLICKED`.
4. W-GB1 should say `YOLO_SETUP=podman-self` and commit `3b1be0bc1`.

## Evidence

In `/Users/Shared/yolo/mac-capture/out/`: `tn-help.txt`, `tn-diagnose.txt`, `alerter-help.txt`,
`hook-real.out`. In `/Users/Shared/yolo/evidence/`:
`2026-11-11-gb1-77df9ca0d.log` and `2026-11-11-gb1-77df9ca0d-yolo-runtime-skipped.log`.
