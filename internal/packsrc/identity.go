package packsrc

// identity.go is the pack identity (docs/design/pack-naming.md §3.2, PN-D10): the canonical place
// a pack is read from, independent of the handle the user's config calls it. A rename of a source
// key changes every handle under it and no identity; moving a pack's directory or repository
// changes its identity and no handle.
//
// The records themselves stay keyed by handle, and an identity index (IdentityIndex) records which
// handle each identity's records are under; the first read after a rename moves them to the new
// handle, and a move sets the old identity's records aside (PN-D15).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// PackIdentity is a pack's identity: `yolo:<name>` for a shipped pack, `dir:<physical path>` for a
// local directory, `git:<repository>//<subdirectory>` for a pack inside a git repository and
// `git:<repository>` for a repository that is one pack, the repository normalized by RepoIdentity
// and never carrying a ref. "" when it cannot be told (a local directory that does not exist, an
// address that does not parse): such a pack is never matched to another, so nothing moves for it.
func PackIdentity(embeddedName, physicalPath, source string) string {
	if embeddedName != "" {
		return "yolo:" + embeddedName
	}
	if physicalPath != "" {
		return "dir:" + filepath.Clean(physicalPath)
	}
	if rest, ok := strings.CutPrefix(source, "file://"); ok {
		if u, err := url.Parse(source); err == nil && u.Path != "" {
			rest = u.Path
		}
		physical, err := LocalDirectoryIdentity(filepath.FromSlash(rest))
		if err != nil {
			return ""
		}
		return "dir:" + physical
	}
	addr, err := ParsePackAddress(source)
	if err != nil || addr.Kind != KindGit {
		return ""
	}
	repo := RepoIdentity(addr.Repo)
	if repo == "" {
		return ""
	}
	if addr.Path != "" {
		return "git:" + repo + "//" + addr.Path
	}
	return "git:" + repo
}

// ParkedHandle is the handle a moved pack's earlier records are set aside under: a pack whose
// identity changed under one handle starts its pins over, and the records of the place it used to
// be read from wait here in case a pack from there is selected again. `~` is no handle or source
// key a user can write, so nothing configured ever reads them.
func ParkedHandle(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "~parked/" + hex.EncodeToString(sum[:6])
}

// IdentityIndexName is the identity index's file name, beside the user config and its lockfiles.
const IdentityIndexName = "pack-identities.json"

// IdentityIndexSchema is the identity index's on-disk format version.
const IdentityIndexSchema = 1

// IdentityIndex is {pack identity → the handle its records are keyed under}.
type IdentityIndex struct {
	Schema int               `json:"schema"`
	Packs  map[string]string `json:"packs"`
}

// IdentityIndexPath is the identity index beside a user config path.
func IdentityIndexPath(userConfigPath string) string {
	return filepath.Join(filepath.Dir(userConfigPath), IdentityIndexName)
}

// LoadIdentityIndex reads the index. A missing file is the empty index: the state before the first
// launch of a build that keeps one, whose records are all still where their handles put them.
func LoadIdentityIndex(path string) (*IdentityIndex, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &IdentityIndex{Schema: IdentityIndexSchema, Packs: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var idx IdentityIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("%s: %w (delete it to start over: the next launch writes it again, "+
			"and a pack renamed since the last launch then re-pins once)", path, err)
	}
	if idx.Schema > IdentityIndexSchema {
		return nil, newerSchemaError(path, idx.Schema, IdentityIndexSchema)
	}
	if idx.Packs == nil {
		idx.Packs = map[string]string{}
	}
	return &idx, nil
}

// Save writes the index through a temporary file and a rename, so a reader never sees half of it.
func (idx *IdentityIndex) Save(path string) error {
	idx.Schema = IdentityIndexSchema
	if idx.Packs == nil {
		idx.Packs = map[string]string{}
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+IdentityIndexName+".*")
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
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// WithIdentityIndex runs fn over the index under its own lock, a file beside it (never in the pack
// store, which a configuration of only shipped and local packs never touches), and saves it when
// fn reports a change. fn may take the pack lockfile's and the fork lock's locks: nothing holding
// either takes this one.
func WithIdentityIndex(path string, fn func(*IdentityIndex) (bool, error)) error {
	unlock, err := flockPath(filepath.Join(filepath.Dir(path), "."+IdentityIndexName+".lock"),
		"the pack identity index "+path, nil)
	if err != nil {
		return err
	}
	defer unlock()
	idx, err := LoadIdentityIndex(path)
	if err != nil {
		return err
	}
	changed, err := fn(idx)
	if err != nil || !changed {
		return err
	}
	return idx.Save(path)
}

// BySource is the one entry locked for source, under whatever handle: the pin of a pack whose
// handle was renamed since it was locked (a renamed source key leaves its packs' addresses as they
// were). ok is false when no entry, or more than one, has that source.
func (l *Lock) BySource(source string) (LockEntry, bool) {
	var found LockEntry
	n := 0
	for _, e := range l.Packs {
		if e.Source == source {
			found = e
			n++
		}
	}
	return found, n == 1
}

// IsParkedHandle reports whether handle is one ParkedHandle names.
func IsParkedHandle(handle string) bool { return strings.HasPrefix(handle, "~parked/") }

// ForkKeyHandle is the handle of the pack a fork pin's key belongs to: the key without its
// `#<patch>` suffix and its last `/<fork>` segment. "" for a key with no pack part.
func ForkKeyHandle(key string) string {
	key, _, _ = strings.Cut(key, "#")
	if i := strings.LastIndex(key, "/"); i > 0 {
		return key[:i]
	}
	return ""
}

// ClaimedHandles is the prune guard for the identity index at indexPath (PN-D15): it reports
// whether a record under handle belongs to an identity that is not selected now, because the
// handle is parked or the index maps an identity to it and no selected pack is called it. A prune
// drops only records no such identity claims, so an unselected or moved pack's pins are found
// again when it returns, while a selected pack's records stay the prune's to judge. When the index
// cannot be read, every handle is claimed: a prune that cannot tell which records are kept removes
// none.
func ClaimedHandles(indexPath string, selected []string) func(handle string) bool {
	idx, err := LoadIdentityIndex(indexPath)
	if err != nil {
		return func(string) bool { return true }
	}
	claimed := map[string]bool{}
	for _, h := range idx.Packs {
		claimed[h] = true
	}
	for _, h := range selected {
		delete(claimed, h)
	}
	return func(handle string) bool { return IsParkedHandle(handle) || claimed[handle] }
}
