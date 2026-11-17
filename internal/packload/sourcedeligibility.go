package packload

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/nofollow"
	"golang.org/x/sys/unix"
)

// DeclaresSourcedSkillsBeneath reads only a selected OWN pack's manifest, never
// conventions or payload metadata. Whole-tree safety remains the delivery gate.
func DeclaresSourcedSkillsBeneath(base, root, name string) (bool, error) {
	done := errors.New("declaration read complete")
	var present bool
	var problem error
	err := nofollow.WalkBelow(base, root, nil, func(entry nofollow.Entry) error {
		if entry.Rel != "." || entry.Kind != nofollow.Dir {
			return fmt.Errorf("selected pack root is not a directory")
		}
		fd := int(entry.File.Fd())
		stat := func(p string) (os.FileInfo, error) {
			var st unix.Stat_t
			err := unix.Fstatat(fd, p, &st, unix.AT_SYMLINK_NOFOLLOW)
			return nil, err
		}
		read := func(p string) ([]byte, error) {
			n, err := unix.Openat(fd, p, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
			if err != nil {
				return nil, err
			}
			f := os.NewFile(uintptr(n), p)
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("selected manifest %s is not regular", p)
			}
			return io.ReadAll(f)
		}
		data, file, discovery := discoverManifestWith("", stat, read, func(_, p string) string { return p })
		if discovery != "" {
			problem = errors.New(discovery)
			return done
		}
		decl, problems, _ := decodeManifest(data, file, name, true)
		if len(problems) > 0 {
			problem = errors.New(problems[0])
			return done
		}
		for _, c := range decl.Contributions() {
			if c.IsSourcedSkill() {
				present = true
			}
		}
		return done
	})
	if err != nil && !errors.Is(err, done) {
		return false, err
	}
	return present, problem
}
