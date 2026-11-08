package stores

import (
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// SectionFreshHome is every known workspace's fresh homes: the new, empty homes a
// `yolo --fresh-home` launch ran on, kept afterwards (docs/design/workspace-state-safety.md §3.6,
// SD-D9, SD-D14).
const SectionFreshHome = "fresh agent homes"

// freshHomeStores lists each fresh home with its size and the command that deletes it. Nothing
// reclaims one: the session in it may have found what the user was looking for, so yolo never
// deletes it, and the row says how the user may. The workspaces are the ones rescueStores reads:
// the one this was run in, then those a runtime still lists a jail for.
func freshHomeStores(o Options, rt string) []Store {
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
		for _, f := range paths.WorkspaceFreshHomes(ws) {
			s := Store{
				Key:     "freshhome." + shortHash(f.Dir),
				Section: SectionFreshHome,
				Name:    filepath.Base(ws) + "/.yolo/setups/" + f.Setup + "/" + filepath.Base(f.Dir),
				Path:    f.Dir,
				Verdict: VerdictHuman,
				Reclaimer: Reclaimer{Detail: "a home a `yolo --fresh-home` launch ran on; yolo never " +
					"deletes it"},
			}
			rm := []string{"rm", "-r", f.Dir}
			bytes, err := freshHomeBytes(ws, f.Dir)
			if err == nil && f.Prism != "" {
				var more int64
				more, err = freshHomeBytes(ws, f.Prism)
				bytes += more
				rm = append(rm, f.Prism)
			}
			if err != nil {
				s.Sizing, s.Reason = SizingUnknown, err.Error()
			} else {
				s.Sizing, s.Bytes = SizingMeasured, bytes
			}
			// The names are the jail's choice (the home sits in its workspace), and the note is
			// rich markup, so every name is escaped.
			s.Note = "workspace " + richtext.Escape(ws) + ", setup " + richtext.Escape(f.Setup) +
				". The workspace's usual home is separate and untouched. Once you no longer need " +
				"what the session there left, and no jail runs on it, delete it with: " +
				richtext.Escape(shquote.Join(rm))
			out = append(out, s)
		}
	}
	return out
}

// freshHomeBytes sums the apparent size of every regular file under dir, one of ws's fresh homes or
// its capture store, read beneath a root that refuses a link at `.yolo` and at every component
// below it, so it sizes nothing a jail pointed elsewhere. No link inside is followed.
func freshHomeBytes(ws, dir string) (int64, error) {
	rel, err := filepath.Rel(paths.WorkspaceStateDir(ws), dir)
	if err != nil {
		return 0, err
	}
	root, err := paths.OpenWorkspaceStateRel(ws, rel)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}
