package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// copyPackTreeBeneath is only the recorded-pack-tree exception. Ordinary tree
// copies retain their old policy; payload links are copied as validated links.
func copyPackTreeBeneath(src string, r *os.Root, rel string) error {
	packs, err := loadPackTree(src)
	if err != nil {
		return err
	}
	if err := copyTreeBeneathExcept(src, r, rel, packload.SkillPayloadTreeDir); err != nil {
		return err
	}
	copied := map[string]bool{}
	for _, p := range packs {
		for _, row := range p.SourcedSkillRows() {
			// Trusted selected local/shipped copies contain only regular staged
			// content and were copied with their inert pack root above.
			if row.LocalCarrier {
				continue
			}
			loc := row.PayloadLocation
			if loc == "" || copied[loc] {
				continue
			}
			copied[loc] = true
			plan, err := packsrc.ReadSkillPayloadTreeBeneath(src, filepath.Join(src, filepath.FromSlash(loc)), nil)
			if err != nil {
				return err
			}
			if len(plan.Omissions) != 0 {
				return fmt.Errorf("recorded payload omits %s — restore its launch tree or restart", plan.Omissions[0].Path)
			}
			target := filepath.Join(rel, filepath.FromSlash(loc))
			if err := r.MkdirAll(target, 0755); err != nil {
				return err
			}
			var dirs []string
			for _, entry := range plan.Entries {
				dest := filepath.Join(target, filepath.FromSlash(entry.Path))
				switch entry.Mode {
				case "040000":
					if err := r.MkdirAll(dest, 0755); err != nil {
						return err
					}
					dirs = append(dirs, dest)
				case "120000":
					if err := r.Symlink(entry.Target, dest); err != nil {
						return err
					}
				default:
					mode := os.FileMode(0444)
					if entry.Mode == "100755" {
						mode = 0555
					}
					f, err := r.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
					if err != nil {
						return err
					}
					_, err = f.Write(entry.Data)
					closeErr := f.Close()
					if err != nil {
						return err
					}
					if closeErr != nil {
						return closeErr
					}
					if err := r.Chmod(dest, mode); err != nil {
						return err
					}
				}
			}
			sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
			for _, dir := range append(dirs, target) {
				if err := r.Chmod(dir, 0555); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
