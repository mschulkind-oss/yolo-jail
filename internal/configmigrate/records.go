package configmigrate

// records.go carries a renamed pack's records to its new name (config-migration.md, the pack
// records). Converting a legacy `packs` entry to its readable handle renames the pack (OQ-RC15:
// `~/code/yolo-packs/pi-fork` is the pack `pi-fork`, and `yolo-packs/pi-fork` is the pack
// `yolo-packs/pi-fork`), and the records below are keyed by that name or by a fork key
// "<pack>/<bin>" made from it. Left under the old name, the next launch would re-pin each fork,
// re-check and rebuild each patched build, and re-resolve each pack's commit. Its build logs and
// pending background-advance state move too (keyedstate.go). Every respell is idempotent and moves
// a record, so it runs inside the journaled commit and Resume runs it again.

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// PackRename is one pack the conversion renames: Old is the name the legacy entry gave it, New the
// readable handle's. OldSource and NewSource are its address before and after, equal unless the
// handle normalizes it (a direct git table drops a trailing `.git`).
type PackRename struct {
	Old       string `json:"old"`
	New       string `json:"new"`
	OldSource string `json:"old_source,omitempty"`
	NewSource string `json:"new_source,omitempty"`
	// SkillsRenamed reports that the converted entry names the pack's skills differently from the
	// old one (its namespace or landing differs), and Namespace is the namespace they take. The
	// conversion writes `bare = true` on everything it converts (pack-naming.md PN-D7), so this is
	// false unless a launch would read the converted entry otherwise.
	SkillsRenamed bool   `json:"skills_renamed,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
}

// RenamesHeading opens the list of renamed packs, in the launch's offer and in the plan.
const RenamesHeading = "In TOML a pack is named by where it comes from, so these are renamed:"

// RenameRows are the renamed packs' rows, old name padded so every arrow lines up: each is the old
// name, the padding after it, and the new name.
func RenameRows(renames []PackRename) [][3]string {
	width := 0
	for _, r := range renames {
		width = max(width, utf8.RuneCountInString(r.Old))
	}
	rows := make([][3]string, len(renames))
	for i, r := range renames {
		rows[i] = [3]string{r.Old, strings.Repeat(" ", width-utf8.RuneCountInString(r.Old)), r.New}
	}
	return rows
}

// RenamesClosing is the sentence after the list: what the renames do not change. It says the
// skills keep their names only when every renamed pack's do, and names each pack whose do not.
//
// Nothing is fetched or rebuilt: every record keyed by the old name moves (moveRecords), and what
// a fetch or a build is keyed by otherwise — a git mirror and its fetch stamps, by the repository
// as packsrc.Parse spells it, which a dropped trailing `.git` does not change; a pack tree, by its
// commit — is the same before and after, since packRenames proved each renamed pack the same
// directory or repository.
func RenamesClosing(renames []PackRename) string {
	var changed []string
	for _, r := range renames {
		if r.SkillsRenamed {
			if r.Namespace != "" {
				changed = append(changed, "the skills of "+r.New+" take the namespace "+r.Namespace)
			} else {
				changed = append(changed, "the skills of "+r.New+" are renamed")
			}
		}
	}
	if len(changed) == 0 {
		return "Your skills keep their names, and nothing is fetched or rebuilt."
	}
	return capitalize(strings.Join(changed, "; ")) + "; every other skill keeps its name, and nothing is fetched or rebuilt."
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// renameMap is old name → new name.
func renameMap(renames []PackRename) map[string]string {
	out := map[string]string{}
	for _, r := range renames {
		out[r.Old] = r.New
	}
	return out
}

// forkKeyRename respells a fork key or owner key "<pack>/<bin>[#upstream]" whose pack is renamed.
// The bin never holds a "/", so a key already respelled to a qualified handle is never matched
// again by a shorter old name.
func forkKeyRename(renames map[string]string) func(string) (string, bool) {
	olds := make([]string, 0, len(renames))
	for o := range renames {
		olds = append(olds, o)
	}
	sort.Slice(olds, func(i, j int) bool { return len(olds[i]) > len(olds[j]) })
	return func(key string) (string, bool) {
		for _, old := range olds {
			rest, ok := strings.CutPrefix(key, old+"/")
			if ok && rest != "" && !strings.Contains(rest, "/") {
				return renames[old] + "/" + rest, true
			}
		}
		return "", false
	}
}

// packTreeSourceRename respells a pack-tree program's build source, "pack:<pack>/<from>" or
// "checkout:<pack>:<dir>" (packdecl.PackTreeBuildSource), whose pack is renamed.
func packTreeSourceRename(renames map[string]string, source string) (string, bool) {
	for old, to := range renames {
		if from, ok := strings.CutPrefix(source, packdecl.PackTreeBuildSource(old, "")); ok {
			return packdecl.PackTreeBuildSource(to, from), true
		}
		if dir, ok := strings.CutPrefix(source, packdecl.PackTreeCheckoutBuildSource(old, "")); ok {
			return packdecl.PackTreeCheckoutBuildSource(to, dir), true
		}
	}
	return "", false
}

// respellPackRecords moves every record a renamed pack is keyed by, and returns one line per
// record moved. workspace is the migration's, whose .yolo build logs move with the host's; ""
// moves only the host's. In a jail it does nothing: the pack store, the capture store and the host
// floor are the host's, and a jail's user config renames nothing there.
func respellPackRecords(userConfigDir, workspace string, renames []PackRename) ([]string, error) {
	out, conflicts, err := moveRecords(userConfigDir, workspace, renames, true)
	for _, c := range conflicts {
		out = append(out, c.String())
	}
	return out, err
}

// moveRecords is respellPackRecords, with the host-skills manifest's owners moved only when
// manifest is set: a pack's records set aside under a parked handle leave the paths its handle
// still writes owned by that handle. It returns the moves it made and, apart, the records it left
// because their new key already held one (packsrc.MoveConflict).
func moveRecords(userConfigDir, workspace string, renames []PackRename, manifest bool) ([]string, []packsrc.MoveConflict, error) {
	if len(renames) == 0 || userConfigDir == "" || config.InJail() {
		return nil, nil, nil
	}
	names := renameMap(renames)
	sources := map[string]string{}
	for _, r := range renames {
		if r.NewSource != "" && r.NewSource != r.OldSource {
			sources[r.Old] = r.NewSource
		}
	}
	userConfig := filepath.Join(userConfigDir, paths.UserConfigTOMLName)
	storeDir := paths.PacksDir()
	keys := forkKeyRename(names)
	var out []string
	var conflicts []packsrc.MoveConflict
	moved, c, err := packsrc.RenamePackLock(storeDir, packsrc.LockPath(userConfig), names, sources)
	out, conflicts = append(out, moved...), append(conflicts, c...)
	if err != nil {
		return out, conflicts, err
	}
	moved, c, err = packsrc.RenameForkLock(storeDir, packsrc.ForkLockPath(userConfig), keys)
	out, conflicts = append(out, moved...), append(conflicts, c...)
	if err != nil {
		return out, conflicts, err
	}
	moved, c, err = (&packsrc.Store{Dir: storeDir}).RenameCheckRecords(keys)
	out, conflicts = append(out, moved...), append(conflicts, c...)
	if err != nil {
		return out, conflicts, err
	}
	out = append(out, respellReceipts(names, keys)...)
	moved, err = (&hostfloor.Floor{Dir: paths.HostFloorDir()}).RenamePack(names)
	out = append(out, moved...)
	if err != nil {
		return out, conflicts, err
	}
	out = append(out, moveKeyedState(workspace, names)...)
	if !manifest {
		return out, conflicts, nil
	}
	moved, err = respellSkillsManifest(filepath.Join(paths.GlobalStorage(), "host-skills-manifest.json"), names)
	out = append(out, moved...)
	return out, conflicts, err
}

// respellReceipts gives every build a renamed pack's fork or pack-tree program admitted a record
// line under the new name, beside the old one: selection takes a build by its fork key and its
// source (resolvePatchedBuild), and a receipt log is only ever appended to. A line already there
// is not appended again.
func respellReceipts(names map[string]string, keys func(string) (string, bool)) []string {
	store := &capture.Store{Dir: paths.CapturesDir()}
	entryKeys, err := store.EntryKeys()
	if err != nil {
		return nil
	}
	var out []string
	for _, k := range entryKeys {
		entry, err := store.Resolve(k)
		if err != nil {
			continue
		}
		path := capture.ReceiptsPath(entry.Root)
		builds, err := entrypoint.ReadBuildReceipts(path)
		if err != nil {
			continue
		}
		have := map[string]bool{}
		for _, b := range builds {
			have[b.Line()] = true
		}
		for _, b := range builds {
			if b.Act != entrypoint.ReceiptActRecord {
				continue
			}
			r, changed := b, false
			if to, ok := keys(b.Fork); ok {
				r.Fork, changed = to, true
			}
			if to, ok := packTreeSourceRename(names, b.Source); ok {
				r.Source, changed = to, true
			}
			if !changed || have[r.Line()] {
				continue
			}
			if entrypoint.AppendReceiptLine(path, r.Line()) == nil {
				have[r.Line()] = true
				what := r.Fork
				if what == "" {
					what = r.Source
				}
				out = append(out, "build receipt "+k+": "+what)
			}
		}
	}
	return out
}

// respellSkillsManifest respells the pack each path `yolo host apply` wrote is recorded as
// belonging to, so the next apply neither archives it as a dropped pack's nor refuses it as
// another pack's.
func respellSkillsManifest(path string, names map[string]string) ([]string, error) {
	m, err := hostskills.LoadManifest(path)
	if err != nil || len(m.Entries) == 0 {
		return nil, nil
	}
	n := 0
	for dest, pack := range m.Entries {
		if to, ok := names[pack]; ok {
			m.Entries[dest] = to
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	if err := m.Save(path); err != nil {
		return nil, err
	}
	return []string{filepath.Base(path) + ": " + strconv.Itoa(n) + " " + plural(n, "path", "paths") + " respelled"}, nil
}
