// Points pi-subagents' worktrees at the durable dir (docs/design/durable-scratch-space.md §5.5,
// DS-D4). pi-subagents puts a `worktree: true` run's worktrees under os.tmpdir() — /tmp, which
// a jail deletes when it exits — unless `worktreeBaseDir` in
// ~/.pi/agent/extensions/subagent/config.json (or the PI_SUBAGENTS_WORKTREE_DIR environment
// variable) names somewhere else. Configuring subagent/config.json directly rather than setting
// PI_SUBAGENTS_WORKTREE_DIR into process.env ensures child processes (such as test runners
// executing inside the jail) do not inherit the variable and fail assertions expecting default
// worktree behavior.
//
// AN EXTENSION, NOT A PACK `env` VALUE: pack env values are literal, and the durable dir is
// /workspace/.yolo/durable on the container backends and the workspace's real path on
// macos-user. $YOLO_DURABLE_DIR is the launch's own answer, set only when the directory exists.
//
// It changes nothing when YOLO_DURABLE_DIR is unset (the host notch, or a launch with no
// durable dir) or when the user already set PI_SUBAGENTS_WORKTREE_DIR or their own
// worktreeBaseDir.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export function durableWorktreeDir(env = process.env) {
	const durable = env.YOLO_DURABLE_DIR;
	if (!durable || env.PI_SUBAGENTS_WORKTREE_DIR) return "";
	return durable.replace(/\/+$/, "") + "/worktrees/pi-subagents";
}

export function subagentConfigPath(env = process.env) {
	const configured = env.PI_CODING_AGENT_DIR;
	const home = env.HOME || env.USERPROFILE || os.homedir();
	let agentDir;
	if (configured === "~") {
		agentDir = home;
	} else if (configured?.startsWith("~/") || configured?.startsWith("~\\")) {
		agentDir = path.join(home, configured.slice(2));
	} else if (configured) {
		agentDir = configured;
	} else {
		agentDir = path.join(home, ".pi", "agent");
	}
	return path.join(agentDir, "extensions", "subagent", "config.json");
}

export default function registerYoloDurableWorktrees(_pi) {
	const configPath = subagentConfigPath(process.env);
	const dir = durableWorktreeDir(process.env);

	let config = {};
	let fileExists = false;
	try {
		if (fs.existsSync(configPath)) {
			fileExists = true;
			const raw = fs.readFileSync(configPath, "utf8");
			const parsed = JSON.parse(raw);
			if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
				config = parsed;
			}
		}
	} catch {
		return;
	}

	if (dir) {
		if (config.worktreeBaseDir && !config._yoloManagedWorktreeBaseDir) {
			return;
		}
		if (config.worktreeBaseDir === dir && config._yoloManagedWorktreeBaseDir === true) {
			return;
		}
		config.worktreeBaseDir = dir;
		config._yoloManagedWorktreeBaseDir = true;
	} else {
		if (!config._yoloManagedWorktreeBaseDir) {
			return;
		}
		delete config.worktreeBaseDir;
		delete config._yoloManagedWorktreeBaseDir;
		if (Object.keys(config).length === 0) {
			try {
				if (fileExists) fs.unlinkSync(configPath);
			} catch {}
			return;
		}
	}

	try {
		fs.mkdirSync(path.dirname(configPath), { recursive: true });
		fs.writeFileSync(configPath, `${JSON.stringify(config, null, "\t")}\n`, "utf8");
	} catch {}
}

