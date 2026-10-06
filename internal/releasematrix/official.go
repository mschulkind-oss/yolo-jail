package releasematrix

import (
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// Entry is one loophole manifest in a packs tree.
type Entry struct {
	Pack, Loophole string
	// Path is the manifest's path in the checkout, slash-separated:
	// packs/<pack>/loopholes/<loophole>/manifest.jsonc.
	Path     string
	Manifest *loopholedecl.Manifest
}

// Manifests reads every loophole manifest a packs tree carries — each
// `<pack>/loopholes/<loophole>/manifest.jsonc` — decoded STRICTLY, in path order.
//
// fsys is the packs tree itself: the embed (packs.FS) for the census, which checks what a
// release carries, and the checkout's packs/ for the pin tool, which has to write the files it
// read. A loophole directory with no manifest.jsonc is an error rather than a skip, because
// skipping it would leave a loophole the embed ships outside the matrix; strict, because every
// shipped manifest decodes strictly (internal/loopholedecl's TestShippedManifestsDecodeStrictly),
// so a failure here is a manifest `yolo pack lint` would also refuse.
func Manifests(fsys fs.FS) ([]Entry, error) {
	out, unread, err := manifests(fsys)
	if err != nil {
		return nil, err
	}
	if len(unread) > 0 {
		return nil, unread[0]
	}
	return out, nil
}

// ManifestsTolerant is Manifests that SKIPS, rather than refuses, a loophole directory whose
// manifest is missing or does not decode, and returns each one it skipped. It is the seed's read
// (tools/pack-binaries, `just install`): yolo loads a loophole only where a pack.json names it,
// and reads its manifest tolerantly, so a stray directory or a manifest mid-edit is nothing an
// install should stop for. The gates read strictly (Manifests), and the census in the short suite
// refuses either.
func ManifestsTolerant(fsys fs.FS) ([]Entry, []error, error) {
	return manifests(fsys)
}

// manifests is the walk both read: every entry it could decode, and each loophole directory it
// could not, in path order. err is a packs tree it could not list at all.
func manifests(fsys fs.FS) ([]Entry, []error, error) {
	packDirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("reading the packs tree: %w", err)
	}
	var out []Entry
	var unread []error
	for _, p := range packDirs {
		if !p.IsDir() {
			continue
		}
		mods, err := fs.ReadDir(fsys, path.Join(p.Name(), "loopholes"))
		if err != nil {
			// Most official packs ship no loophole, so an absent loopholes/ is ordinary.
			continue
		}
		for _, l := range mods {
			if !l.IsDir() {
				continue
			}
			dir := path.Join("packs", p.Name(), "loopholes", l.Name())
			rel := path.Join(p.Name(), "loopholes", l.Name(), loopholedecl.ManifestName)
			data, err := fs.ReadFile(fsys, rel)
			if err != nil {
				unread = append(unread, fmt.Errorf("%s: %w", path.Join(dir, loopholedecl.ManifestName), err))
				continue
			}
			m, err := loopholedecl.Decode(data, dir)
			if err != nil {
				unread = append(unread, err)
				continue
			}
			out = append(out, Entry{Pack: p.Name(), Loophole: l.Name(),
				Path: path.Join(dir, loopholedecl.ManifestName), Manifest: m})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, unread, nil
}

// WithBinaries is the entries that declare `binaries`: the manifests the matrix is about.
func WithBinaries(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if len(e.Manifest.Binaries) > 0 {
			out = append(out, e)
		}
	}
	return out
}
