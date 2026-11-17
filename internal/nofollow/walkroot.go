package nofollow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// WalkRoot uses an already-confined root's descriptor, not its pathname: an
// agent replacing that name cannot redirect a permission-restoration walk.
func WalkRoot(root *os.Root, rel string, skip map[string]bool, visit func(Entry) error) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("walk path %s is outside its owned root", rel)
	}
	base, err := root.Open(".")
	if err != nil {
		return err
	}
	defer base.Close()
	fd, err := unix.Dup(int(base.Fd()))
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	components := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, component, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return &BelowBaseLinkError{Path: component, Base: root.Name()}
		}
		next, _, err := openChecked(fd, component, component, dirFlags, unix.S_IFDIR, &st)
		if err != nil {
			return err
		}
		unix.Close(fd)
		fd = next
	}
	return walkAt(fd, components[len(components)-1], ".", skip, visit)
}
