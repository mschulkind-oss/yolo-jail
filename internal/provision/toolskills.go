package provision

// JailMainDir is the jail's main-process state directory (entrypoint's jailMainDir, which is
// this value): where the session locks live. It is named here because the first session's stage
// is composed by the launcher, which must name the tool-skills lock without importing the
// entrypoint's private state.
const JailMainDir = "/run/yolo/main"

// ToolSkillsLockName is the file under JailMainDir that serializes sessions refreshing the
// tool-skills snapshot, as mise-install.lock serializes their installs.
const ToolSkillsLockName = "tool-skills.lock"

// StepToolSkills is the mise tool-skills refresh (docs/reference/mise-tool-skills.md §4 step 2): the
// snapshot of the skills the user's own mise tools declare, written after `mise install` so the
// bytes are there to read. It belongs after StepMiseInstall in a stage and nowhere else.
//
// IT NEVER FAILS: it ends in `|| true`, so the `&&` join the steps share never stops on it, and
// the subcommand itself bounds its own run (toolskills.Runner) and prints one line on failure.
//
// IT IS INERT WITHOUT AN ALLOWLIST: the test is two shell builtins, so an empty list
// (YOLO_MISE_SKILL_TOOLS unset, empty or `[]`) starts no process at all.
//
// workspace and lock are SHELL WORDS the caller has already quoted: the container's stage is
// wrapped in `sh -c '…'` and so cannot carry a single quote (SetupBypassingShims), which is why
// this does not quote them itself. macos-user passes `"$PWD"` (its stage starts in the
// workspace) and no lock, which makes the snapshot use its own lock file.
func StepToolSkills(workspace, lock string) string {
	cmd := "yolo internal tool-skills " + workspace
	if lock != "" {
		cmd += " " + lock
	}
	return `if [ -n "${YOLO_MISE_SKILL_TOOLS:-}" ] && [ "$YOLO_MISE_SKILL_TOOLS" != "[]" ]; then ` +
		cmd + " >&2 || true; fi"
}
