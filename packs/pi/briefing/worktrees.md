## Where pi's workflow tools put worktrees

pi-subagents makes a `worktree: true` run's worktrees in the system temp dir unless told
otherwise: `/tmp`, which a container jail deletes when it exits. So where `$YOLO_DURABLE_DIR` is
set, yolo's extension `~/.pi/agent/extensions/yolo-durable-worktrees.js` sets
`worktreeBaseDir` in `~/.pi/agent/extensions/subagent/config.json` to
`$YOLO_DURABLE_DIR/worktrees/pi-subagents`, and they go there. A `PI_SUBAGENTS_WORKTREE_DIR`
already set, or a user-configured `worktreeBaseDir`, wins.
pi-subagents removes a run's worktrees when the run ends; one a restart cut short stays until you
`git worktree remove` it.

pi-dynamic-workflows puts its worktrees in the repository's `.pi/worktrees/`, which it has no
setting to change. They survive a restart. Unless the repository ignores `.pi/worktrees/`,
`git status` lists it: never `git add` it.

Neither place is a pattern for a worktree you make yourself. Put that where the user or the
project says; failing that, where the storage guidance above says. In a yolo jail where
`$YOLO_DURABLE_DIR` is set, that is `$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance
says there is no durable directory, ask the user.
