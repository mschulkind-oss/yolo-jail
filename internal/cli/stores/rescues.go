package stores

import (
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/stateguard"
)

// SectionRescue is every known workspace's rescues: the agents' homes a jail's stop copied out
// because the workspace's `.yolo` was moved or deleted under the running jail
// (docs/design/workspace-state-safety.md §3.5, SD-D9).
const SectionRescue = "rescued agent homes"

// rescueStores lists each rescue with its size and the command that restores it. Nothing reclaims
// one: it is the user's only copy of a lost session, so yolo never deletes it, and the row says
// how the user may.
func rescueStores(o Options, rt string) []Store {
	// The workspace this was run in, then those a runtime still lists a jail for, as the durable
	// rows read them: a rescue's jail is gone by the time it is made.
	var workspaces []string
	if o.Workspace != "" {
		workspaces = append(workspaces, o.Workspace)
	}
	if slices.Contains(paths.SupportedRuntimes, rt) {
		for _, ws := range prune.FindYoloWorkspaces(rt, o.Exec) {
			if !slices.Contains(workspaces, ws) {
				workspaces = append(workspaces, ws)
			}
		}
	}
	var out []Store
	for _, ws := range workspaces {
		for _, r := range stateguard.List(ws) {
			s := Store{
				Key:     "rescue." + shortHash(r.Dir),
				Section: SectionRescue,
				Name:    filepath.Base(ws) + "/.yolo/rescue/" + filepath.Base(r.Dir),
				Path:    r.Dir,
				Verdict: VerdictHuman,
				Reclaimer: Reclaimer{Detail: "the only copy of agents' homes a stopped jail could not keep; " +
					"yolo never deletes it"},
			}
			if r.Err != nil {
				s.Sizing, s.Reason = SizingUnknown, r.Err.Error()
			} else {
				s.Sizing, s.Bytes = SizingMeasured, r.Bytes
			}
			// The names are the jail's choice (the rescue dir sits in its workspace), and the note
			// is rich markup, so every name is escaped.
			note := "workspace " + richtext.Escape(ws) + ". "
			if r.Restore != "" {
				note += "With no jail running there, restore it with: " + richtext.Escape(r.Restore) + "."
			} else {
				note += "Its record names none of the workspace's homes, so copy back what you need by hand."
			}
			s.Note = note + " Once restored, or not needed, delete it with: " + richtext.Escape(shquote.Join([]string{"rm", "-r", r.Dir}))
			out = append(out, s)
		}
	}
	return out
}
