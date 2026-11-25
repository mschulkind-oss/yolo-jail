package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// vanishedPackNote is the disclosure for a selected pack vanishedLocalPack left out: which pack,
// what is missing, the next step, and that the grant stays in effect.
func vanishedPackNote(entry PackEntry, source string) string {
	return fmt.Sprintf("selected pack %s was skipped: its local source %s does not exist, so it declares nothing and the broker's grants are unaffected. "+
		"Restore that directory or run `yolo pack remove %s`; the broker rereads the config on the next call", entry.Name, source, entry.Name)
}

// UserScopeSelectedPacksWithNotes is UserScopeSelectedPacks for a reader of broker declarations
// (the approval hook, the timed-offer cap): an entry whose local source directory is gone
// (vanishedLocalPack) is not an incomplete selection, it is named in notes; every other
// unresolved entry, and any closure error, still makes complete false.
func UserScopeSelectedPacksWithNotes() (packs []*packload.Pack, notes []string, complete bool) {
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		return nil, nil, false
	}
	resolved := SelectedPackDeclarationsDetailed(entries, UserScopeSelection(), nil, packload.EmbeddedFS())
	embeddedNames := packload.EmbeddedNames()
	complete = resolved.ClosureErr == nil
	for _, u := range resolved.Unresolved {
		if missing, source := vanishedLocalPack(u.Entry, embeddedNames); missing {
			notes = append(notes, vanishedPackNote(u.Entry, source))
			continue
		}
		complete = false
	}
	return resolved.Packs(), notes, complete
}

// vanishedLocalPack reports a selected entry whose source is a local directory that does not
// exist, and the path. It is the one failure that PROVES the entry cannot bear on a broker's
// policy: with no directory there is no manifest, so it declares no broker, capability, profile
// or need, and the read is the same as one without the entry (the launch itself would fail on it,
// so no jail runs with a different answer). Everything else is left to fail the read: an
// unreadable or invalid manifest, a remote source that is not cached (it may declare), a
// permission error (the directory may exist), a clone-backed entry, and an entry named like a
// shipped pack, which a present copy would shadow.
func vanishedLocalPack(entry PackEntry, embeddedNames []string) (bool, string) {
	if entry.Embedded() || entry.Local != "" || entry.EmbeddedName != "" {
		return false, ""
	}
	addr, err := packsrc.Parse(entry.Source)
	if entry.AllowDefaultRef {
		addr, err = packsrc.ParsePackAddress(entry.Source)
	}
	if err != nil || !addr.IsLocal() {
		return false, ""
	}
	// A present copy could carry a shipped pack's name (the entry's, its last segment, or its
	// directory's) and shadow it, so a vanished one named like a shipped pack stays a failure.
	for _, name := range []string{entry.Name, path.Base(entry.Name), filepath.Base(addr.Path)} {
		if slices.Contains(embeddedNames, name) {
			return false, ""
		}
	}
	if _, err := os.Stat(addr.Path); errors.Is(err, fs.ErrNotExist) {
		return true, addr.Path
	}
	return false, ""
}
