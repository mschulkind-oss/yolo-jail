package treesync

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/nofollow"
)

type ownedEntry struct {
	rel    string
	kind   nofollow.Kind
	mode   os.FileMode
	data   []byte
	target string
}

// SyncOwnedLinks is the sourced-registration exception. Only exact relative
// entries/target texts in links may be symlinks. Source bytes are read through
// no-follow descriptors; destination writes stay confined under os.Root.
// Ordinary Sync still refuses every source link.
func SyncOwnedLinks(src, dst string, links map[string]string) (bool, error) {
	for p, target := range links {
		if p == "." || filepath.Clean(p) != p || filepath.IsAbs(p) || strings.HasPrefix(p, ".."+string(filepath.Separator)) || !filepath.IsAbs(target) {
			return false, fmt.Errorf("invalid owned link plan %q", p)
		}
	}
	var wanted []ownedEntry
	seenLinks := map[string]bool{}
	err := nofollow.WalkBelow(filepath.Dir(src), src, nil, func(e nofollow.Entry) error {
		entry := ownedEntry{rel: e.Rel, kind: e.Kind, mode: 0755, target: e.Target}
		switch e.Kind {
		case nofollow.Dir:
		case nofollow.File:
			entry.mode = 0644
			if e.Perm&0111 != 0 {
				entry.mode = 0755
			}
			var err error
			entry.data, err = io.ReadAll(e.File)
			if err != nil {
				return err
			}
		case nofollow.Symlink:
			entry.mode = 0777
			if target, ok := links[e.Rel]; !ok || target != e.Target {
				return fmt.Errorf("sync source %s: unplanned link — rebuild the sourced registration plan", e.Rel)
			}
			seenLinks[e.Rel] = true
		default:
			return fmt.Errorf("sync source %s is neither a planned link, regular file nor directory", e.Rel)
		}
		wanted = append(wanted, entry)
		return nil
	})
	if err != nil {
		return false, err
	}
	if len(seenLinks) != len(links) {
		return false, fmt.Errorf("owned link plan entries absent from source — rebuild the sourced registration plan")
	}
	if len(wanted) == 0 || wanted[0].kind != nofollow.Dir {
		return false, fmt.Errorf("sync source is not a real directory")
	}
	changed := false
	if fi, err := os.Lstat(dst); os.IsNotExist(err) {
		if err := os.MkdirAll(dst, 0755); err != nil {
			return false, err
		}
		changed = true
	} else if err != nil {
		return false, err
	} else if !fi.IsDir() {
		return false, fmt.Errorf("sync destination %s is not a real directory", dst)
	}
	current := map[string]ownedEntry{}
	err = nofollow.WalkBelow(filepath.Dir(dst), dst, nil, func(e nofollow.Entry) error {
		entry := ownedEntry{rel: e.Rel, kind: e.Kind, mode: e.Perm, target: e.Target}
		if e.Kind == nofollow.File {
			var err error
			entry.data, err = io.ReadAll(e.File)
			if err != nil {
				return err
			}
		}
		current[e.Rel] = entry
		return nil
	})
	if err != nil {
		return changed, err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return changed, err
	}
	defer root.Close()
	desired := map[string]bool{".": true}
	for _, e := range wanted {
		desired[e.rel] = true
		if e.rel == "." {
			continue
		}
		have, exists := current[e.rel]
		if exists && e.kind == have.kind && e.mode == have.mode && e.target == have.target && bytes.Equal(e.data, have.data) {
			continue
		}
		if e.kind == nofollow.Dir && exists && have.kind == nofollow.Dir {
			if err := root.Chmod(e.rel, e.mode); err != nil {
				return changed, err
			}
			changed = true
			continue
		}
		if exists && have.kind == nofollow.Dir {
			if err := root.RemoveAll(e.rel); err != nil {
				return changed, err
			}
		}
		if e.kind == nofollow.Dir {
			if exists && have.kind != nofollow.Dir {
				if err := root.Remove(e.rel); err != nil {
					return changed, err
				}
			}
			if err := root.Mkdir(e.rel, e.mode); err != nil {
				return changed, err
			}
		} else {
			// Unique temporary sibling, then a confined rename over a file or link.
			temp, err := os.CreateTemp(dst, ".owned-sync-*")
			if err != nil {
				return changed, err
			}
			tmp := filepath.Base(temp.Name())
			_ = temp.Close()
			_ = root.Remove(tmp)
			if e.kind == nofollow.Symlink {
				err = root.Symlink(e.target, tmp)
			} else {
				var file *os.File
				file, err = root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, e.mode)
				if err == nil {
					_, err = file.Write(e.data)
					closeErr := file.Close()
					if err == nil {
						err = closeErr
					}
					if err == nil {
						err = root.Chmod(tmp, e.mode)
					}
				}
			}
			if err == nil {
				err = root.Rename(tmp, e.rel)
			}
			if err != nil {
				_ = root.Remove(tmp)
				return changed, err
			}
		}
		changed = true
	}
	// Stale entries are removed by name without following their links.
	for rel := range current {
		if !desired[rel] {
			if err := root.RemoveAll(rel); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}
