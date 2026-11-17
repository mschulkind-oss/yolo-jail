package cli

import (
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func selectedEntryDeclaresSourced(e config.PackEntry) (bool, error) {
	a, err := packsrc.ParsePackAddress(e.Source)
	if err != nil {
		return false, err
	}
	root, base := a.Path, filepath.Dir(a.Path)
	if a.Kind == packsrc.KindGit {
		store := &packsrc.Store{Dir: paths.PacksDir()}
		var result *packsrc.Resolved
		if e.AllowDefaultRef && a.Ref == "" {
			result, err = store.ResolvePackDefault(a, e.Name, e.Slug(), e.Source, packsrc.LockPath(paths.UserConfigPath()), true)
		} else {
			result, err = store.ResolveExisting(a, e.Slug())
		}
		if err != nil {
			return false, err
		}
		root = result.Root
		base = store.Dir
	} else if a.Kind != packsrc.KindFile {
		return false, fmt.Errorf("selected declaration unavailable")
	}
	return packload.DeclaresSourcedSkillsBeneath(base, root, e.Name)
}
