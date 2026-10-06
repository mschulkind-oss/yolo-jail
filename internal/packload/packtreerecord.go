package packload

// packtreerecord.go is a staged pack tree's RECORD: which directory holds which pack, in the one
// precedence order (docs/plans/notch-convergence.md OQ-NC4, ruled A: config order, then the
// selection closure's additions, then the conventional local pack last). The launch writes it
// from the packs it staged, in the order it loaded them; the jail's boot (entrypoint.LoadJailPacks)
// and an attach (the run pipeline's loadPackTree) read it back. It lives here because both halves
// read it, and the boot cannot import the run pipeline.
//
// The order cannot be recovered from the directories: the boot used to read the tree with
// os.ReadDir, embedded packs first and each level alphabetical, so a key two packs set had a
// winner in the jail that neither the launch nor the host picked (row B2).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PackTreeRecordName is the record's file name, at the tree's top level, where the boot's
// fallback walk skips every non-directory. It is a name no pack slug can spell: a slug spells
// every byte outside [A-Za-z0-9.-] as "_" plus two hex digits, so no slug starts "_pa". A
// dot-name could collide, because a pack name may be any string without "/", "\" or ":".
const PackTreeRecordName = "_pack-tree.json"

// PackTreeEntry is one pack in a tree's record.
type PackTreeEntry struct {
	// Name is the pack's name, as the launch loaded it.
	Name string `json:"name"`
	// Dir is the pack's directory, relative to the tree, slash-separated.
	Dir string `json:"dir"`
	// Skipped are the version-skew notes the launch already printed for this pack (Pack.SkewNotes
	// without their "pack <name>: " prefix, since the boot names a pack by its directory): what
	// the use read skipped, kept without a field, or ignored (docs/design/patched-forks.md
	// PF-D68). The boot reads the same skips from the same manifest, and logs a note found here
	// to boot.log only rather than printing the launch's line a second time; a note the launch
	// did not print, as from an entrypoint of another build, still warns. Absent from a tree an
	// older launch staged, and ignored by an older boot.
	Skipped []string `json:"skipped,omitempty"`
}

type packTreeRecord struct {
	Packs []PackTreeEntry `json:"packs"`
}

// WritePackTreeRecord writes root's record from the packs a launch loaded out of it, in the order
// given, which is the precedence order (config.PackSelection.Packs).
func WritePackTreeRecord(root string, packs []*Pack) error {
	rec := packTreeRecord{Packs: []PackTreeEntry{}}
	for _, p := range packs {
		rel, err := filepath.Rel(root, p.Root)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("packs: pack %s was loaded from %s, outside its tree %s", p.Name, p.Root, root)
		}
		entry := PackTreeEntry{Name: p.Name, Dir: filepath.ToSlash(rel)}
		for _, note := range p.SkewNotes {
			entry.Skipped = append(entry.Skipped, strings.TrimPrefix(note, "pack "+p.Name+": "))
		}
		rec.Packs = append(rec.Packs, entry)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, PackTreeRecordName), append(data, '\n'), 0o644)
}

// ReadPackTreeRecord reads root's record: its packs in precedence order, and recorded=false with
// no error when the tree carries none (one a launch before the record staged). A record that is
// unreadable, malformed, or names a directory outside the tree is an error: the tree disagrees
// with what its launch staged, which a reader must not guess around.
func ReadPackTreeRecord(root string) (entries []PackTreeEntry, recorded bool, err error) {
	path := filepath.Join(root, PackTreeRecordName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec packTreeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, true, fmt.Errorf("the record %s: %w", path, err)
	}
	for _, e := range rec.Packs {
		if e.Name == "" || !filepath.IsLocal(filepath.FromSlash(e.Dir)) {
			return nil, true, fmt.Errorf("the record %s names %q at %q, which is not a pack inside the tree",
				path, e.Name, e.Dir)
		}
	}
	return rec.Packs, true, nil
}
