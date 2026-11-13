---
name: yolo-pi
description: "pi under yolo: how to start `yolo wait` in the background, where pi-subagents and pi-dynamic-workflows put worktrees and which yolo removes, and where to put your own."
---

# pi under yolo

## Waiting on the host in Pi

Start `yolo wait r-<id>` (or `yolo wait <label>`) with `background_task` and
`completionPolicy: "wake"`, in the same turn as you relay the human's commands: its exit wakes you.

## Where pi's workflow tools put worktrees

pi-subagents makes a `worktree: true` run's worktrees in the system temp dir unless told
otherwise: `/tmp`, which a container jail deletes when it exits. So where `$YOLO_DURABLE_DIR` is
set, yolo's extension `~/.pi/agent/extensions/yolo-durable-worktrees.js` sets
`worktreeBaseDir` in `~/.pi/agent/extensions/subagent/config.json` to
`$YOLO_DURABLE_DIR/worktrees/pi-subagents`, and they go there. A `PI_SUBAGENTS_WORKTREE_DIR`
already set, or a user-configured `worktreeBaseDir`, wins.
pi-subagents removes a run's worktree only when nothing in it would be lost; one holding work it
could not capture as a patch, and one whose run was cut short, stays. Where their directory lies
beneath `$YOLO_DURABLE_DIR` (yolo's, or a `PI_SUBAGENTS_WORKTREE_DIR` you set there), the same
extension bounds them: at most once an hour it removes, with `git worktree remove`, each
`pi-worktree-*` worktree there that is clean (with no edit the index hides from `git status`), on
a branch, in no rebase, merge or other git operation, without submodules, unlocked, used by no
process in this jail and idle for a day. Its branch stays, so a commit made there is not lost,
unless it is a `pi-subagents/*` branch whose commit another branch or a tag holds. Files the
worktree ignores go with it. Every other worktree is kept and listed, with the reason, in that
directory's `.yolo-reap.json`: remove it yourself once its work is saved.

pi-dynamic-workflows puts its worktrees in the repository's `.pi/worktrees/`, which it has no
setting to change. They survive a restart. Unless the repository ignores `.pi/worktrees/`,
`git status` lists it: never `git add` it.

Neither place is a pattern for a worktree you make yourself. Put that where the user or the
project says; failing that, where your briefing's storage guidance says. In a yolo jail where
`$YOLO_DURABLE_DIR` is set, that is `$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance
says there is no durable directory, ask the user.
