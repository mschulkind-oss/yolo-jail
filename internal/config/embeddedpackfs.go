package config

import (
	"io/fs"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// embeddedPackDeclarationsFromFS reads embedded manifests for config validation without
// materializing the process cache. A bad shipped declaration is omitted here; selected-entry
// resolution reports its own detailed failure.
func embeddedPackDeclarationsFromFS(fsys fs.FS) []*packload.Pack {
	names, err := packload.EmbeddedNamesFromFS(fsys)
	if err != nil {
		return nil
	}
	packs := make([]*packload.Pack, 0, len(names))
	for _, name := range names {
		if pack, ok := embeddedPackDeclarationFromFS(fsys, name); ok {
			packs = append(packs, pack)
		}
	}
	return packs
}

func embeddedPackDeclarationFromFS(fsys fs.FS, name string) (*packload.Pack, bool) {
	if fsys == nil {
		return nil, false
	}
	p, _, err := packload.ReadSelectedPackFS(fsys, name,
		filepath.Join(string(filepath.Separator), "embedded", name), name, nil, nil)
	if err != nil || p == nil || p.Decl == nil {
		return nil, false
	}
	p.Official = true
	return p, true
}

func renamedEmbeddedPackFromFS(old string, fsys fs.FS) (string, bool) {
	for _, pack := range embeddedPackDeclarationsFromFS(fsys) {
		if pack.Decl.FormerNames() == nil {
			continue
		}
		for _, former := range pack.Decl.FormerNames().Packs {
			if former == old {
				return pack.Name, true
			}
		}
	}
	return "", false
}
