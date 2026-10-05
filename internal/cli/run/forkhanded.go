package run

// forkhanded.go is a fresh launch's DELIVERY RECORD for its forks (docs/design/patched-forks.md
// §6.3, §7, PF-D20): what the launch handed its jail per forked program — the store key the
// jail's source launcher materializes, or why there is none — written beside the jail's own pack
// tree, as `<tree>.forks.json`.
//
// TWO READERS, and both are why it exists:
//
//   - A PATCHED FORK'S MOVE reaps every other build of the fork that no running jail was handed
//     (HandedForkKeys). A jail materializes its build at the program's first run, not at boot
//     (entrypoint's forklauncher.go), so a build a running jail was handed must outlive the move;
//     and a record that cannot be read keeps every build.
//   - AN ATTACH says what the jail it enters runs (noteAttachForkBuilds), from what that jail was
//     handed, never from the good build as it stands, which may have moved since the jail booted.
//
// IT LIVES AND DIES WITH THE TREE. It sits beside the tree rather than in it, because the tree is
// bound :ro into the jail and nothing edits it once staged, and the attach compares trees by
// content (packtree.go); its name carries a dot, which a tree's name (a stamp and os.MkdirTemp's
// digits) and `.live` never do. discardPackTree removes it with its tree, which is only once the
// tree's container is known gone, and the reaper of a gone jail's AGENTS_DIR/<cname> takes both.
//
// Written under the fork's record lock for a patched fork (ForkBuildRequest.Hand), so a move,
// which reaps under the same lock, reads every launch that decided to hand a build before it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// handedForksSuffix names a tree's delivery record: `<tree>` + this.
const handedForksSuffix = ".forks.json"

// handedForksSchema is the record's schema. A record of another schema cannot be read, and a reaper
// keeps every build then.
const handedForksSchema = 1

// HandedFork is what one launch handed its jail for one forked program.
type HandedFork struct {
	// Key is the capture store entry handed, "" when none was.
	Key string `json:"key,omitempty"`
	// Reason is why none was, "" when Key is set.
	Reason string `json:"reason,omitempty"`
	// Fork is a PATCHED fork's key ("<pack>/<bin>"), "" for a plain fork's program. Commit, Tag,
	// Patches and Series are the handed build's inputs, as an attach's line names them.
	Fork    string `json:"fork,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Patches int    `json:"patches,omitempty"`
	Series  string `json:"series,omitempty"`
}

type handedForksFile struct {
	Schema int                   `json:"schema"`
	Forks  map[string]HandedFork `json:"forks"`
	// Trees are what the launch handed its jail per PATCHED EXTENSION, by extension key
	// (docs/design/patched-extensions.md §8.1): an attach's line reads it. Additive, so a record
	// an older yolo wrote reads with none, and an older yolo reading this one ignores it.
	Trees map[string]HandedTree `json:"trees,omitempty"`
}

// HandedTree is what one launch handed its jail for one patched extension: the store entry its
// per-launch copy was made from and that build's inputs, or why there is none.
type HandedTree struct {
	Entry   string `json:"entry,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Into    string `json:"into,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Patches int    `json:"patches,omitempty"`
	Series  string `json:"series,omitempty"`
}

// handedForksPath is tree's delivery record.
func handedForksPath(tree string) string { return tree + handedForksSuffix }

// readHandedForks reads tree's delivery record: nil and no error when there is none.
func readHandedForks(tree string) (map[string]HandedFork, error) {
	f, err := readHandedFile(tree)
	if err != nil || f == nil {
		return nil, err
	}
	return f.Forks, nil
}

// readHandedFile reads tree's whole delivery record: nil and no error when there is none.
func readHandedFile(tree string) (*handedForksFile, error) {
	data, err := os.ReadFile(handedForksPath(tree))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f handedForksFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", handedForksPath(tree), err)
	}
	if f.Schema != handedForksSchema {
		return nil, fmt.Errorf("%s is schema %d, which this yolo does not read", handedForksPath(tree), f.Schema)
	}
	return &f, nil
}

// recordHandedFork merges bin's delivery into tree's record, through a temp file and a rename.
// One launch writes its own tree's record, from its own goroutine, so there is no second writer.
func recordHandedFork(tree, bin string, h HandedFork) error {
	return updateHandedFile(tree, func(f *handedForksFile) { f.Forks[bin] = h })
}

// recordHandedTree merges one patched extension's delivery into tree's record, as recordHandedFork.
func recordHandedTree(tree, key string, h HandedTree) error {
	return updateHandedFile(tree, func(f *handedForksFile) {
		if f.Trees == nil {
			f.Trees = map[string]HandedTree{}
		}
		f.Trees[key] = h
	})
}

// updateHandedFile rewrites tree's delivery record with edit applied, through a temp file and a
// rename.
func updateHandedFile(tree string, edit func(*handedForksFile)) error {
	if tree == "" {
		return nil
	}
	cur, err := readHandedFile(tree)
	if err != nil || cur == nil {
		cur = &handedForksFile{} // an unreadable record of our own is replaced, never read around
	}
	cur.Schema = handedForksSchema
	if cur.Forks == nil {
		cur.Forks = map[string]HandedFork{}
	}
	edit(cur)
	data, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	p := handedForksPath(tree)
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// recordHandedForks records what this launch hands its jail for every forked program its trigger
// answered, leaving the bins a patched fork's advance recorded itself (with what its line names).
func (o *Options) recordHandedForks(out map[string]entrypoint.ForkDelivery) {
	if o.packTree == "" || len(out) == 0 {
		return
	}
	done := map[string]bool{}
	for _, bin := range o.handedForks {
		done[bin] = true
	}
	bins := make([]string, 0, len(out))
	for bin := range out {
		bins = append(bins, bin)
	}
	sort.Strings(bins)
	for _, bin := range bins {
		if done[bin] {
			continue
		}
		d := out[bin]
		if err := recordHandedFork(o.packTree, bin, HandedFork{Key: d.Key, Reason: d.Reason}); err != nil {
			// What it costs: an attach to this jail cannot say what it was handed for these bins. No
			// move reaps a plain fork's build, and a patched fork's advance records its own bin.
			o.pr(o.Stderr).printf("[yellow]Warning: could not record what this launch hands its jail for %s "+
				"(%v): an attach to this jail will not say which build it runs; the jail itself is "+
				"unaffected, and making %s writable lets the next launch record it[/yellow]", bin, err,
				filepath.Dir(handedForksPath(o.packTree)))
			return
		}
	}
}

// HandedForkKeys is every capture store key any launch on this machine has recorded handing a jail
// whose pack tree is still there — a running jail, or one whose end no launch has seen yet — for a
// patched fork's move, which reaps none of them (PF-D20). An error when any record cannot be read
// or the tree roots cannot be listed: the caller then keeps every build.
func HandedForkKeys() (map[string]bool, error) {
	keys := map[string]bool{}
	jails, err := os.ReadDir(paths.AgentsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	for _, j := range jails {
		if !j.IsDir() {
			continue
		}
		root := paths.PackTreeRoot(j.Name())
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".json" || len(name) <= len(handedForksSuffix) ||
				name[len(name)-len(handedForksSuffix):] != handedForksSuffix {
				continue
			}
			handed, err := readHandedForks(filepath.Join(root, name[:len(name)-len(handedForksSuffix)]))
			if err != nil {
				return nil, err
			}
			for _, h := range handed {
				if h.Key != "" {
					keys[h.Key] = true
				}
			}
		}
	}
	return keys, nil
}

// recordHandedTrees records what this launch hands its jail for every patched extension its tree
// arm answered, for an attach's line (noteAttachTreeBuilds).
func (o *Options) recordHandedTrees(out map[string]TreeDelivery) {
	if o.packTree == "" || len(out) == 0 {
		return
	}
	intos := map[string]string{}
	for _, f := range o.patchedTrees {
		intos[f.Key()] = f.Into
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := out[k]
		h := HandedTree{Reason: d.Reason, Into: intos[k]}
		if d.Dir != "" {
			h = HandedTree{Entry: d.Entry, Into: intos[k], Commit: d.Commit, Tag: d.Tag, Patches: d.Patches, Series: d.Series}
		}
		if err := recordHandedTree(o.packTree, k, h); err != nil {
			o.pr(o.Stderr).printf("[yellow]Warning: could not record what this launch hands its jail for %s (%v): "+
				"an attach to this jail will not say which build it runs; the jail itself is unaffected[/yellow]", k, err)
			return
		}
	}
}
