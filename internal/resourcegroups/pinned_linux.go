//go:build linux

package resourcegroups

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const cgroup2Magic = 0x63677270

type pinnedGroup struct {
	fd     int
	device uint64
	inode  uint64
}

func (g *pinnedGroup) close()           { syscall.Close(g.fd) }
func (g *pinnedGroup) identity() string { return fmt.Sprintf("%x-%x", g.device, g.inode) }
func (g *pinnedGroup) same(other *pinnedGroup) bool {
	return g.device == other.device && g.inode == other.inode
}
func pinFD(fd int, requireCgroup bool) (*pinnedGroup, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		syscall.Close(fd)
		return nil, errors.New("selected group is not a directory")
	}
	if requireCgroup {
		var fs syscall.Statfs_t
		if err := syscall.Fstatfs(fd, &fs); err != nil || fs.Type != cgroup2Magic {
			syscall.Close(fd)
			return nil, errors.New("selected group is not on cgroup v2; run yolo check --no-build")
		}
	}
	return &pinnedGroup{fd: fd, device: uint64(stat.Dev), inode: stat.Ino}, nil
}

// openPinnedGroup walks every directory without following symlinks; an absolute
// pathname check followed by a leaf O_NOFOLLOW is not sufficient for writes.
func openPinnedGroup(path string, requireCgroup bool) (*pinnedGroup, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path && !strings.HasSuffix(path, "/.") {
		return nil, errors.New("non-absolute or unclean group path")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			syscall.Close(fd)
			return nil, errors.New("parent traversal")
		}
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return pinFD(fd, requireCgroup)
}
func (g *pinnedGroup) verifyPath(path string) error {
	current, err := openPinnedGroup(path, false)
	if err != nil {
		return err
	}
	defer current.close()
	if !g.same(current) {
		return errors.New("selected group changed on disk; run yolo check --no-build")
	}
	return nil
}
func (g *pinnedGroup) openChild(name string, create bool) (*pinnedGroup, error) {
	if name != "yolo-agent" && name != "yolo-batch" {
		return nil, errors.New("unsupported delegated child")
	}
	if create {
		if err := syscall.Mkdirat(g.fd, name, 0700); err != nil && err != syscall.EEXIST {
			return nil, err
		}
	}
	fd, err := syscall.Openat(g.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return pinFD(fd, create)
}
func (g *pinnedGroup) verifyChild(child *pinnedGroup, name string) error {
	current, err := g.openChild(name, false)
	if err != nil {
		return err
	}
	defer current.close()
	if !child.same(current) {
		return errors.New("delegated child changed on disk; run yolo check --no-build")
	}
	return nil
}
func (g *pinnedGroup) writePolicy(data []byte, _ bool) error {
	fd, err := syscall.Openat(g.fd, "io.max", syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var stat syscall.Stat_t
	var fs syscall.Statfs_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return err
	}
	if err := syscall.Fstatfs(fd, &fs); err != nil {
		return err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || fs.Type != cgroup2Magic || stat.Ino == 0 {
		return errors.New("delegated io.max is not a cgroup v2 policy file; run yolo check --no-build")
	}
	n, err := syscall.Write(fd, data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errors.New("short delegated policy write")
	}
	return nil
}

// Lock roots are caller-owned persistent private storage, not cgroup mount paths.
func pinLockRoot(path string) (*pinnedGroup, error) {
	g, err := openPinnedGroup(path, false)
	if err != nil {
		return nil, err
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(g.fd, &stat); err != nil {
		g.close()
		return nil, err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
		g.close()
		return nil, errors.New("lock root must be private and owned by the current user")
	}
	return g, nil
}

func (g *pinnedGroup) readPolicy(name string) ([]byte, error) {
	if name != "io.max" && name != "cgroup.controllers" && name != "cgroup.subtree_control" {
		return nil, errors.New("unsupported policy file")
	}
	fd, err := syscall.Openat(g.fd, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var stat syscall.Stat_t
	var fs syscall.Statfs_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if err := syscall.Fstatfs(fd, &fs); err != nil {
		return nil, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || fs.Type != cgroup2Magic || stat.Ino == 0 {
		return nil, errors.New("observed policy is not a cgroup v2 file; run yolo check --no-build")
	}
	return io.ReadAll(file)
}
