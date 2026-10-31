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

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// PackTreeRecordName is the record's file name, at the tree's top level, where the boot's
// fallback walk skips every non-directory. It is a name no pack slug can spell: a slug spells
// every byte outside [A-Za-z0-9.-] as "_" plus two hex digits, so no slug starts "_pa". A
// dot-name could collide, because a pack name may be any string without "/", "\" or ":".
const PackTreeRecordName = "_pack-tree.json"

type PatchBranchMapping struct {
	// Repository is the host-established identity of the relative locator's repository.
	Repository string `json:"repository"`
	// Directory is the declaring pack directory relative to the host workspace.
	Directory string `json:"directory,omitempty"`
	// Mapped says Directory is available under the jail workspace. It is separate from
	// Directory because "." is a valid mapping for a pack at the workspace root.
	Mapped bool `json:"mapped,omitempty"`
}

// PackTreeEntry is one pack in a tree's record.
type PackTreeEntry struct {
	// Name is the pack's name, as the launch loaded it.
	Name string `json:"name"`
	// Dir is the pack's directory, relative to the tree, slash-separated.
	Dir string `json:"dir"`
	// Skipped are the version-skew notes the launch already printed for this pack (Pack.SkewNotes
	// without their "pack <name>: " prefix, since the boot names a pack by its directory): what
	// the use read skipped, kept without a field, or ignored (docs/reference/patched-forks.md
	// PF-D68). The boot reads the same skips from the same manifest, and logs a note found here
	// to boot.log only rather than printing the launch's line a second time; a note the launch
	// did not print, as from an entrypoint of another build, still warns. Absent from a tree an
	// older launch staged, and ignored by an older boot.
	Skipped []string `json:"skipped,omitempty"`
	// PatchBranches carries host-side repository identity and the workspace-relative declaring
	// directory for selected relative patch branches, keyed by staged owner key.
	PatchBranches map[string]PatchBranchMapping `json:"patch_branches,omitempty"`
	// Namespace, NamespaceOwner and Landing are the pack's agent-facing naming as the launch
	// computed it from config (Pack's fields of these names, docs/design/pack-naming.md §3.4),
	// so the jail lands `files` and mounts /ctx where the launch did. Absent from an older tree.
	Namespace      string `json:"namespace,omitempty"`
	NamespaceOwner string `json:"namespace_owner,omitempty"`
	Landing        string `json:"landing,omitempty"`
}

// ApplyNaming gives p the agent-facing naming its launch recorded, so a reader of the tree delivers
// skills and lands files under the names the launch did (docs/design/pack-naming.md §3.4).
func (e PackTreeEntry) ApplyNaming(p *Pack) {
	if p == nil {
		return
	}
	p.Namespace, p.NamespaceOwner, p.Landing = e.Namespace, e.NamespaceOwner, e.Landing
}

type packTreeRecord struct {
	Packs []PackTreeEntry `json:"packs"`
}

// WritePackTreeRecord writes root's record from the packs a launch loaded out of it, in the order
// given, which is the precedence order (config.PackSelection.Packs).
func WritePackTreeRecord(root string, packs []*Pack) error {
	return WritePackTreeRecordForWorkspace(root, packs, "")
}

// WritePackTreeRecordForWorkspace writes root's record and carries the selected packs' relative branch
// identity across the host-to-jail path mapping. A branch directory outside workspace is not mapped;
// its identity still distinguishes its record, without making that host path accessible in the jail.
func WritePackTreeRecordForWorkspace(root string, packs []*Pack, workspace string) error {
	rec := packTreeRecord{Packs: []PackTreeEntry{}}
	for _, p := range packs {
		rel, err := filepath.Rel(root, p.Root)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("packs: pack %s was loaded from %s, outside its tree %s", p.Name, p.Root, root)
		}
		entry := PackTreeEntry{Name: p.Name, Dir: filepath.ToSlash(rel),
			Namespace: p.Namespace, NamespaceOwner: p.NamespaceOwner, Landing: p.Landing}
		for _, note := range p.SkewNotes {
			entry.Skipped = append(entry.Skipped, strings.TrimPrefix(note, "pack "+p.Name+": "))
		}
		mappings, err := patchBranchMappingsForTree(p, workspace)
		if err != nil {
			return fmt.Errorf("packs: recording relative patch branches for %s: %w", p.Name, err)
		}
		entry.PatchBranches = mappings
		rec.Packs = append(rec.Packs, entry)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, PackTreeRecordName), append(data, '\n'), 0o644)
}

func patchBranchMappingsForTree(p *Pack, workspace string) (map[string]PatchBranchMapping, error) {
	if p == nil || p.Decl == nil {
		return nil, nil
	}
	dir := p.SourceRoot
	if dir == "" {
		dir = p.Root
	}
	var mappedDir string
	mapped := false
	if workspace != "" {
		ws, err := filepath.EvalSymlinks(workspace)
		if err == nil {
			source, sourceErr := filepath.EvalSymlinks(dir)
			if sourceErr == nil {
				if rel, relErr := filepath.Rel(ws, source); relErr == nil && filepath.IsLocal(rel) {
					mappedDir, mapped = filepath.ToSlash(rel), true
				}
			}
		}
	}
	var out map[string]PatchBranchMapping
	for _, c := range p.Decl.Contributions() {
		if c.PatchBranch == "" || !packsrc.IsRelativeLocator(c.PatchBranch) {
			continue
		}
		identity, err := packsrc.RelativeRepositoryIdentity(c.PatchBranch, dir)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = make(map[string]PatchBranchMapping)
		}
		owner := p.StagedSlug() + "/" + c.ExtensionName()
		out[owner] = PatchBranchMapping{Repository: identity, Directory: mappedDir, Mapped: mapped}
	}
	return out, nil
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
		for owner, mapping := range e.PatchBranches {
			if owner == "" || mapping.Repository == "" {
				return nil, true, fmt.Errorf("the record %s has an incomplete relative patch branch mapping for %q", path, owner)
			}
			if !mapping.Mapped && mapping.Directory != "" || mapping.Mapped && !filepath.IsLocal(filepath.FromSlash(mapping.Directory)) {
				return nil, true, fmt.Errorf("the record %s maps a relative patch branch directory outside the workspace: %q",
					path, mapping.Directory)
			}
		}
	}
	return rec.Packs, true, nil
}
