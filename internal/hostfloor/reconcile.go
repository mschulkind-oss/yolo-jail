package hostfloor

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// reconcile.go takes entries back OUT of the prefix: what `yolo host apply` removes because no
// selected pack declares the program any more (§4, "Deselection"), and what `yolo prune` reclaims
// because an install was killed.
//
// Removal is never done at launch: a launch installs what it needs and leaves everything else
// alone, so a launch with a narrowed config can never delete the agent another terminal is
// running. `yolo host apply` is the act that says "this is my selection now".

// Removal is one entry Reconcile takes out, or would.
type Removal struct {
	Bin string
	// Why is the clause a report prints after the bin.
	Why string
	// Bytes is what removing it frees, as measured before.
	Bytes int64
}

// Reconcile removes every provisioned entry whose program is not in keep: no selected pack
// declares it, or the floor can no longer hold it (`host_floor` leaves it out, its vendor stopped
// publishing here). It also drops Node releases no remaining record runs on, except the one the
// floor installs on now. With apply false it only reports.
//
// Contents-only, and by unlinking: a running agent keeps the inode it has open, so an agent a
// terminal is using when its pack is deselected keeps running until it exits.
func (f *Floor) Reconcile(keep []Program, apply bool) []Removal {
	wanted := map[string]bool{}
	for _, p := range keep {
		if f.noEntryReason(p) == "" {
			wanted[p.Bin()] = true
		}
	}
	var out []Removal
	for _, bin := range f.floorBins() {
		if wanted[bin] {
			continue
		}
		why := "no selected pack declares it any more"
		for _, p := range keep {
			if p.Bin() == bin {
				why = f.noEntryReason(p)
			}
		}
		r := Removal{Bin: bin, Why: why, Bytes: treeBytes(f.programsDir(bin))}
		out = append(out, r)
		if !apply {
			continue
		}
		lk, err := acquire(f.lockPath(bin), false, nil)
		if err != nil {
			// An install of it is running right now; the next apply removes it.
			continue
		}
		_ = os.Remove(f.Launcher(bin))
		_ = os.Remove(f.recordPath(bin))
		_ = os.RemoveAll(f.programsDir(bin))
		f.appendReceipt(receipt{Act: "remove", Bin: bin})
		lk.release()
	}
	if apply {
		f.dropUnusedNode()
	}
	return out
}

// floorBins is every bin the prefix holds anything for: a launcher, a record or an install dir.
func (f *Floor) floorBins() []string {
	set := map[string]bool{}
	for _, sub := range []string{"bin", "records", "programs"} {
		entries, err := os.ReadDir(filepath.Join(f.Dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if sub == "records" {
				var ok bool
				if name, ok = strings.CutSuffix(name, ".json"); !ok {
					continue
				}
			}
			set[name] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dropUnusedNode removes every Node release no record runs on, keeping the one the floor installs
// on now: re-downloading it for the next npm program would cost what keeping it saves.
//
// NOTHING, while a record a newer yolo wrote is in the prefix: this yolo cannot read which Node
// that yolo's program runs on, and Records leaves the record out, so its Node read as unused and
// an --assert deleted it right after refusing to install over the program (HP-D8).
func (f *Floor) dropUnusedNode() {
	for _, bin := range f.floorBins() {
		if _, err := f.readRecord(bin); errors.Is(err, ErrNewerRecord) {
			return
		}
	}
	inUse := map[string]bool{f.NodeVersion(): true}
	for _, rec := range f.Records() {
		if rec.Node != "" {
			inUse[rec.Node] = true
		}
	}
	entries, err := os.ReadDir(f.nodeRoot())
	if err != nil {
		return
	}
	lk, err := acquire(f.lockPath("node"), false, nil)
	if err != nil {
		return
	}
	defer lk.release()
	for _, e := range entries {
		if v, ok := nodeVersionOf(e.Name()); ok && !inUse[v] {
			_ = os.RemoveAll(filepath.Join(f.nodeRoot(), e.Name()))
		}
	}
}

// Leftover is one interrupted install Sweep reclaims.
type Leftover struct {
	Path  string
	Bytes int64
}

// Leftovers lists the install directories and Node scratch trees a killed install left: no
// completion marker, and no process holding the lock that would be writing them. It never waits
// on a lock and never touches anything a running install owns.
func (f *Floor) Leftovers() []Leftover {
	var out []Leftover
	programs, _ := os.ReadDir(filepath.Join(f.Dir, "programs"))
	for _, pe := range programs {
		bin := pe.Name()
		if f.Lock(bin).Held {
			continue
		}
		dirs, _ := os.ReadDir(f.programsDir(bin))
		for _, d := range dirs {
			dir := filepath.Join(f.programsDir(bin), d.Name())
			if _, err := os.Stat(filepath.Join(dir, completeMarker)); err != nil {
				out = append(out, Leftover{Path: dir, Bytes: treeBytes(dir)})
			}
		}
	}
	if !f.Lock("node").Held {
		for _, sub := range []string{f.nodeRoot(), f.downloads()} {
			entries, _ := os.ReadDir(sub)
			for _, e := range entries {
				name := e.Name()
				if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".part") {
					p := filepath.Join(sub, name)
					out = append(out, Leftover{Path: p, Bytes: treeBytes(p)})
				}
			}
		}
	}
	return out
}

// Sweep removes what Leftovers lists, when apply is set, and reports it either way. It is
// `yolo prune`'s whole reach into the prefix: an interrupted install, never a provisioned entry
// (Reconcile's, at `yolo host apply`).
func (f *Floor) Sweep(apply bool) (bytes int64, n int) {
	for _, l := range f.Leftovers() {
		if apply {
			if err := os.RemoveAll(l.Path); err != nil {
				continue
			}
		}
		bytes += l.Bytes
		n++
	}
	return bytes, n
}

// treeBytes is the regular-file bytes under root; an unreadable entry counts as zero.
func treeBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
