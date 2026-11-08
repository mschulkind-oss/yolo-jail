package stateguard

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Setup says what holds the jail's homes, which decides what a mismatch means for them.
type Setup int

const (
	// Mounted: a container setup, whose homes are bind-mounted from `.yolo`. The jail still holds
	// them after a move, until it stops, and its stop copies them out.
	Mounted Setup = iota
	// Moved: macos-user, or the host, where nothing is mounted. The homes moved with `.yolo`, and
	// are wherever it went.
	Moved
)

// Warning is what a mismatch says to a person who can act on it, next step first, in plain words
// (docs/design/workspace-state-safety.md §3.4). It is the attach's warning and `yolo check`'s
// finding. It is empty when nothing moved.
func Warning(f Finding, workspace string, setup Setup) []string {
	if !f.Moved() {
		return nil
	}
	state := filepath.Join(workspace, ".yolo")
	back := "`mv <where you moved it> " + shquote.Quote(state) + "`"
	if !f.HomesLost() {
		durable := filepath.Join(state, "durable")
		return []string{
			durable + " is not the directory this jail started with: it was moved or deleted while the jail runs.",
			"Files the agents kept there are wherever it was moved. Put it back with `mv <where you moved it> " +
				shquote.Quote(durable) + "`.",
		}
	}
	lead := state + " is not the directory this jail started with: it was moved or deleted while the jail runs."
	what := state
	if !f.StateMoved() {
		lead = "A directory holding the agents' homes under " + state + " was moved or deleted while the jail runs."
		what = "it"
	}
	if setup == Moved {
		return []string{
			lead,
			"The agents' homes, with every session and login, moved with it. Put " + what + " back with " + back + ".",
		}
	}
	return []string{
		lead,
		"The agents' homes are still intact inside the running jail. Put " + what + " back with " + back +
			" before the last session ends.",
		"If you cannot, end the sessions as usual: as the jail stops, yolo copies the homes to " +
			filepath.Join(state, RescueDirName) + " and prints the command that restores them.",
	}
}

// StopLine is the one line a stop prints after a rescue: where the copy is, and the command that
// restores it.
func StopLine(workspace, rescueDir, home string) string {
	return filepath.Join(workspace, ".yolo") + " was moved or deleted while the jail ran, so yolo copied the agents' homes to " +
		rescueDir + " before stopping it. With no jail running in this workspace, restore them with: " +
		RestoreCommand(rescueDir, home)
}
