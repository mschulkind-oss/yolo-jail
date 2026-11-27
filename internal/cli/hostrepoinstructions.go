package cli

// hostrepoinstructions.go is `repo_instructions` at the unconfined notch
// (docs/reference/workspace-instruction-files.md IF-D13, IF-D16): `yolo host -- <agent>` reads the USER
// scope's mode (the launch composes from user scope alone), hands the loaded packs the selection with
// Host set, and so each agent's pack hands its own flag through the one launch-flag fold, where a
// jail renders a per-workspace file the unconfined path does not have. `yolo host apply` writes the
// user scope's replacement text into the briefing and no per-workspace setting into the shared real
// home.

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/repoinstr"
)

// applyHostRepoInstructions hands the host launch's packs their selection and prints the launch's
// repo-instructions line for the agent being launched. It never refuses a launch: an agent that
// cannot ignore the files is named, with the way back to keep (IF-D15). Silent when the user scope
// keeps the files, or the command is not an agent that declares what it reads.
func applyHostRepoInstructions(launch *hostComposition, bin string, errw io.Writer) {
	mode, with := config.RepoInstructions(launch.cfg)
	res := &repoinstr.Resolved{Mode: mode, ReplaceWith: with}
	// A workspace layer's own value is never read here (IF-D13); say so for an agent that declares
	// what it reads, whatever the user scope does.
	for _, a := range packload.InstructionsAgents(launch.packs) {
		if a.Bin != filepath.Base(bin) {
			continue
		}
		if line := workspaceRepoInstructionsIgnoredLine(launch.workspace, mode, with); line != "" {
			printHostLines(errw, []string{line})
		}
		break
	}
	if !res.Ignoring() {
		return
	}
	home, _ := os.UserHomeDir()
	packload.ApplyInstructions(launch.packs, res.Selection(launch.workspace, home, true))
	lines := repoinstr.Disclosure(launch.packs, packload.Embedded(), res, launch.workspace, true, filepath.Base(bin))
	printHostLines(errw, lines)
}

// hostBriefingExtra is the user-block prose `yolo host apply` writes: the user scope's
// agents_md_extra, then the repo_instructions replacement section when the user scope replaces.
// An unreadable replacement file returns the plain extra and the reason, so the apply goes on.
func hostBriefingExtra() (string, error) {
	extra := config.AgentsMDExtraUser()
	res, err := repoinstr.Resolve(config.UserScopeConfigOrEmpty(), "")
	if err != nil {
		return extra, err
	}
	return res.WithExtra(extra), nil
}
