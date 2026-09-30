## Claude Code's own worktrees

Claude Code puts the worktrees it makes in the repository's `.claude/worktrees/`: those of
`claude --worktree`, of `EnterWorktree`, and of subagents and workflows that run in a worktree.
Leave them there; they survive a restart. Never make `.claude` or `.claude/worktrees` a symbolic
link, because Claude Code then refuses to make a worktree.

A worktree you make yourself with `git worktree add` is not one of these, and Claude Code's
cleanup keeps it until you remove it. Put it where the user or the project says; failing that,
where the storage guidance above says, which in a yolo jail is
`$YOLO_DURABLE_DIR/worktrees/<task>`.

If `git status` lists `.claude/worktrees/`, this repository does not ignore it: never `git add`
it, and tell the user, since the fix is a line in their `.gitignore`.
