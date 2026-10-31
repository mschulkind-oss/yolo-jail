package configmigrate

// records.go carries a renamed pack's records to its new name (config-migration.md, the pack
// records). Converting a legacy `packs` entry to its readable handle renames the pack (OQ-RC15:
// `~/code/yolo-packs/pi-fork` is the pack `pi-fork`, and `yolo-packs/pi-fork` is the pack
// `yolo-packs/pi-fork`), and the records below are keyed by that name or by a fork key
// "<pack>/<bin>" made from it. Left under the old name, the next launch would re-pin each fork,
// re-check and rebuild each patched build, and re-resolve each pack's commit. Every respell is
// idempotent and moves a record, so it runs inside the journaled commit and Resume runs it again.

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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
}

// NotCarried names the pack state a rename does not carry, and what the next launch does
// instead. Said before the question, so the user answers knowing it.
const NotCarried = "a pending background update check for a renamed pack starts over, and its " +
	"build logs and cached copies are made again under the new name"

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
// record moved. In a jail it does nothing: the pack store, the capture store and the host floor
// are the host's, and a jail's user config renames nothing there.
func respellPackRecords(userConfigDir string, renames []PackRename) ([]string, error) {
	if len(renames) == 0 || userConfigDir == "" || config.InJail() {
		return nil, nil
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
	moved, err := packsrc.RenamePackLock(storeDir, packsrc.LockPath(userConfig), names, sources)
	out = append(out, moved...)
	if err != nil {
		return out, err
	}
	moved, err = packsrc.RenameForkLock(storeDir, packsrc.ForkLockPath(userConfig), keys)
	out = append(out, moved...)
	if err != nil {
		return out, err
	}
	moved, err = (&packsrc.Store{Dir: storeDir}).RenameCheckRecords(keys)
	out = append(out, moved...)
	if err != nil {
		return out, err
	}
	out = append(out, respellReceipts(names, keys)...)
	moved, err = (&hostfloor.Floor{Dir: paths.HostFloorDir()}).RenamePack(names)
	out = append(out, moved...)
	if err != nil {
		return out, err
	}
	moved, err = respellSkillsManifest(filepath.Join(paths.GlobalStorage(), "host-skills-manifest.json"), names)
	out = append(out, moved...)
	return out, err
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
