// Points pi-subagents' worktrees at the durable dir (docs/design/durable-scratch-space.md §5.5,
// DS-D4). pi-subagents puts a `worktree: true` run's worktrees under os.tmpdir() — /tmp, which
// a jail deletes when it exits — unless PI_SUBAGENTS_WORKTREE_DIR (or its own
// `worktreeBaseDir` setting, which outranks the variable) names somewhere else. It reads the
// variable when each run starts, so setting it here, when pi loads its extensions, is in time.
//
// AN EXTENSION, NOT A PACK `env` VALUE: pack env values are literal, and the durable dir is
// /workspace/.yolo/durable on the container backends and the workspace's real path on
// macos-user. $YOLO_DURABLE_DIR is the launch's own answer, set only when the directory exists.
//
// It changes nothing when YOLO_DURABLE_DIR is unset (the host notch, or a launch with no
// durable dir) or when the user already set PI_SUBAGENTS_WORKTREE_DIR.
export function durableWorktreeDir(env) {
	const durable = env.YOLO_DURABLE_DIR;
	if (!durable || env.PI_SUBAGENTS_WORKTREE_DIR) return "";
	return durable.replace(/\/+$/, "") + "/worktrees/pi-subagents";
}

export default function registerYoloDurableWorktrees(_pi) {
	const dir = durableWorktreeDir(process.env);
	if (dir) process.env.PI_SUBAGENTS_WORKTREE_DIR = dir;
}
