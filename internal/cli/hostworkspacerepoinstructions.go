package cli

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// workspaceRepoInstructionsIgnoredLine is the line for a workspace whose own layer sets
// repo_instructions to something the user scope (userMode, userWith) does not: the host path reads
// the user scope alone (docs/reference/workspace-instruction-files.md IF-D13), so that value changes
// nothing there. It names what is ignored and the next step, the user-scope `yolo config set`.
// "" when the workspace sets nothing, sets what the user scope already says, or cannot be read (a
// launch never refuses for it).
func workspaceRepoInstructionsIgnoredLine(workspace, userMode, userWith string) string {
	wsCfg, err := config.LoadWorkspaceConfig(workspace, false, nil)
	if err != nil || wsCfg == nil {
		return ""
	}
	if _, set := wsCfg.Get(config.RepoInstructionsKey()); !set {
		return ""
	}
	wsMode, wsWith := config.RepoInstructions(wsCfg)
	if wsMode == userMode && wsWith == userWith {
		return ""
	}
	step := "yolo config set repo_instructions.mode " + wsMode + " --user"
	what := "mode " + wsMode
	if wsWith != "" {
		step += " && yolo config set repo_instructions.replace_with " + wsWith + " --user"
		what += ", replace_with " + wsWith
	}
	return "repo instructions: this workspace sets repo_instructions (" + what + "), but the host does not read it: " +
		"a host launch and `yolo host apply` follow your user scope (mode " + userMode + "). To have the host " +
		"follow it, run: " + step
}
