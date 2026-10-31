package configmigrate

// identities.go keeps every pack's records with its identity (docs/design/pack-naming.md §3.2,
// PN-D10): a renamed handle finds the records its pack had under the old one, and a pack whose
// handle now reads from somewhere else starts its pins over.
//
// HOW (PN-D15): the records stay keyed by handle, and an identity index beside the user config
// (packsrc.IdentityIndex) says which handle each identity's records are under. The first read of a
// selection — a launch's or `yolo host apply`'s pack refresh, `yolo pack install`, and
// `yolo pack source rename` — compares the two and moves records through the same idempotent
// moves `yolo config migrate` uses (respellPackRecords). A pack the index has never seen adopts
// the records under its handle, which is the migration of every record written before the index
// existed. A record whose pack is not selected keeps its place, and moves when the pack returns.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// IdentifiedPack is one selected pack: the handle the config gives it and its identity
// (config.PackEntry.Identity).
type IdentifiedPack struct {
	Handle   string
	Identity string
}

// IdentifiedPacks is entries' handles and identities. A shipped pack is left out: its handle is
// its own name, which the user cannot rename, and a `formerly` rename is the pack's own.
func IdentifiedPacks(entries []config.PackEntry) []IdentifiedPack {
	out := make([]IdentifiedPack, 0, len(entries))
	for _, e := range entries {
		if e.Embedded() {
			continue
		}
		out = append(out, IdentifiedPack{Handle: e.Name, Identity: e.Identity()})
	}
	return out
}

// ReconcilePackRecords moves each selected pack's records to the handle it is selected under, and
// returns one line per pack whose records moved or started over, each followed by the records it
// moved. Running it again over its own result moves nothing. In a jail it does nothing: the
// records are the host's.
func ReconcilePackRecords(userConfigDir string, selected []IdentifiedPack) ([]string, error) {
	if userConfigDir == "" || config.InJail() {
		return nil, nil
	}
	indexPath := filepath.Join(userConfigDir, packsrc.IdentityIndexName)
	// The common case, nothing renamed or moved, reads and takes no lock.
	if idx, err := packsrc.LoadIdentityIndex(indexPath); err == nil &&
		!planReconcile(idx.Packs, selected, readRecordState(userConfigDir)).changed {
		return nil, nil
	}
	var out []string
	err := packsrc.WithIdentityIndex(indexPath, func(idx *packsrc.IdentityIndex) (bool, error) {
		plan := planReconcile(idx.Packs, selected, readRecordState(userConfigDir))
		if len(plan.parks) > 0 {
			moved, err := moveRecords(userConfigDir, plan.parks, false)
			if err != nil {
				return false, err
			}
			out = append(out, plan.parkLines...)
			out = append(out, indent(moved)...)
		}
		if len(plan.renames) > 0 {
			moved, err := moveRecords(userConfigDir, plan.renames, true)
			if err != nil {
				return false, err
			}
			out = append(out, plan.renameLines...)
			out = append(out, indent(moved)...)
		}
		for id, handle := range plan.index {
			idx.Packs[id] = handle
		}
		return plan.changed, nil
	})
	return out, err
}

// recordState is what the records say before any index does: locked is {handle → identity} for
// every packs.lock.json entry, read from the address each was locked for, which finds the records
// of a pack renamed before the index first saw it; held is every handle with a lock entry or a fork
// pin. A pack with neither has nothing to keep, so the index does not record it yet.
type recordState struct {
	locked map[string]string
	held   map[string]bool
}

func readRecordState(userConfigDir string) recordState {
	st := recordState{locked: map[string]string{}, held: map[string]bool{}}
	userConfig := filepath.Join(userConfigDir, paths.UserConfigTOMLName)
	if lock, err := packsrc.LoadLock(packsrc.LockPath(userConfig)); err == nil {
		for handle, e := range lock.Packs {
			st.held[handle] = true
			if id := packsrc.PackIdentity("", "", e.Source); id != "" {
				st.locked[handle] = id
			}
		}
	}
	if forks, err := packsrc.LoadForkLock(packsrc.ForkLockPath(userConfig)); err == nil {
		for key := range forks.Forks {
			key, _, _ = strings.Cut(key, "#")
			if i := strings.LastIndex(key, "/"); i > 0 {
				st.held[key[:i]] = true
			}
		}
	}
	return st
}

type reconcilePlan struct {
	// parks set records aside under each identity's parked handle, before renames run, so a
	// handle another identity is about to take is free.
	parks, renames         []PackRename
	parkLines, renameLines []string
	// index is the identities whose entry changes, and where their records are after the moves.
	index   map[string]string
	changed bool
}

// planReconcile is ReconcilePackRecords' decision, over the index's {identity → handle}.
func planReconcile(index map[string]string, selected []IdentifiedPack, records recordState) reconcilePlan {
	plan := reconcilePlan{index: map[string]string{}}
	selectedHandles := map[string]bool{}
	for _, p := range selected {
		selectedHandles[p.Handle] = true
	}
	// An identity selected under two handles, or one that cannot be told, moves nothing.
	count := map[string]int{}
	for _, p := range selected {
		if p.Identity != "" {
			count[p.Identity]++
		}
	}
	want := map[string]string{} // identity → handle it is selected under
	for _, p := range selected {
		if p.Identity != "" && count[p.Identity] == 1 {
			want[p.Identity] = p.Handle
		}
	}
	holder := map[string]string{} // handle → identity whose records are under it
	for id, h := range index {
		holder[h] = id
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// An identity whose records are under a handle another selected identity takes is set aside
	// first, under its parked handle: it moved away (a pack now read from elsewhere took its
	// handle) or it is itself moving to a handle of its own in the second pass.
	parked := map[string]bool{}
	parkedFrom := map[string]bool{} // handles whose records were just set aside
	for _, id := range ids {
		h := want[id]
		other, held := holder[h]
		if !held || other == id || parked[other] {
			continue
		}
		parked[other] = true
		to := packsrc.ParkedHandle(other)
		plan.parks = append(plan.parks, PackRename{Old: h, New: to})
		parkedFrom[h] = true
		plan.index[other] = to
		if _, selectedElsewhere := want[other]; !selectedElsewhere {
			plan.parkLines = append(plan.parkLines, fmt.Sprintf("pack %s now reads from %s, not %s: its pins and "+
				"checks start over, and the earlier ones are kept in case %s is selected again",
				h, describeIdentity(id), describeIdentity(other), describeIdentity(other)))
		}
	}
	for _, id := range ids {
		h := want[id]
		from, known := index[id]
		if parked[id] {
			from, known = packsrc.ParkedHandle(id), true
		}
		switch {
		case !known:
			// First sight: the records under its handle are its own (the migration of the records
			// written before the index), unless another identity's were just set aside from it. A
			// pack renamed before the index first saw it is found by the address its pin was
			// locked for: one unselected, unindexed handle locked for this identity.
			// A handle with no records yet is not recorded: there is nothing to keep.
			if records.held[h] || parkedFrom[h] {
				plan.index[id] = h
			} else if old := formerHandle(id, h, records.locked, holder, selectedHandles); old != "" {
				plan.index[id] = h
				plan.renames = append(plan.renames, PackRename{Old: old, New: h})
				plan.renameLines = append(plan.renameLines, fmt.Sprintf("pack %s was %s: its pins and records follow it",
					h, old))
			}
		case from != h:
			plan.renames = append(plan.renames, PackRename{Old: from, New: h})
			plan.index[id] = h
			if index[id] == packsrc.ParkedHandle(id) {
				plan.renameLines = append(plan.renameLines, fmt.Sprintf("pack %s reads from %s again: "+
					"the pins and records kept for it are back", h, describeIdentity(id)))
			} else {
				plan.renameLines = append(plan.renameLines, fmt.Sprintf("pack %s was %s: its pins and records follow it",
					h, index[id]))
			}
		}
	}
	for id, h := range plan.index {
		if index[id] != h {
			plan.changed = true
		}
	}
	return plan
}

// formerHandle is the one handle other than h whose pin was locked for id, that no indexed
// identity holds and no selected pack is called: the handle a pack had before a rename the index
// never saw. "" when there is none, or more than one.
func formerHandle(id, h string, locked, holder map[string]string, selected map[string]bool) string {
	found := ""
	for handle, lockedID := range locked {
		if lockedID != id || handle == h || selected[handle] {
			continue
		}
		if _, indexed := holder[handle]; indexed {
			continue
		}
		if found != "" {
			return ""
		}
		found = handle
	}
	return found
}

// describeIdentity is an identity as a line names it: the place, without the kind prefix.
func describeIdentity(id string) string {
	for _, prefix := range []string{"dir:", "git:", "yolo:"} {
		if len(id) > len(prefix) && id[:len(prefix)] == prefix {
			return id[len(prefix):]
		}
	}
	return id
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}
