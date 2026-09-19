package listeners

import (
	"os"
	"path/filepath"
)

// ProcRoot is the kernel's process filesystem, the only production Source.
const ProcRoot = "/proc"

// Source is the tree a snapshot is read from: /proc in production, a fixture in
// tests. Names are slash-separated and relative to the root — "net/tcp",
// "412/fd", "412/fd/7", "412/comm" — and "." names the root itself.
//
// It is deliberately not an [io/fs.FS]. The operation attribution turns on is
// readlink, and /proc's fd entries point at "socket:[318411330]", which is not a
// path: a fixture built out of real files would have to create symlinks with that
// target text on disk, and any Source that resolved or validated targets would
// break the one thing this package needs. An in-memory fixture says what the
// kernel says, on every platform, with no filesystem at all.
//
// No method may block indefinitely and none may panic; a Source that cannot
// answer returns an error and the collector records a [Gap].
type Source interface {
	ReadFile(name string) ([]byte, error)
	// ReadDirNames returns a directory's entry names, in any order. The
	// collector sorts what it depends on.
	ReadDirNames(name string) ([]string, error)
	// ReadLink returns the symlink's target text VERBATIM, unresolved.
	ReadLink(name string) (string, error)
}

// DirSource reads a real directory tree. DirSource(ProcRoot) is what [Collect]
// uses on Linux.
//
// It is not restricted to the root the way os.Root is, because every name it is
// handed is built by this package from /proc's own contents — there is no
// caller-supplied path to escape with — and a containment check would have to
// resolve names, which ReadLink must not do.
type DirSource string

func (d DirSource) path(name string) string {
	return filepath.Join(string(d), filepath.FromSlash(name))
}

func (d DirSource) ReadFile(name string) ([]byte, error) {
	// os.ReadFile handles /proc's zero-stat files: it reads to EOF rather than
	// trusting the size.
	return os.ReadFile(d.path(name))
}

func (d DirSource) ReadDirNames(name string) ([]string, error) {
	f, err := os.Open(d.path(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Readdirnames, not os.ReadDir: ReadDir lstats every entry, which for
	// /proc/<pid>/fd is one extra syscall per file descriptor for information
	// the collector never reads.
	return f.Readdirnames(-1)
}

func (d DirSource) ReadLink(name string) (string, error) {
	return os.Readlink(d.path(name))
}
