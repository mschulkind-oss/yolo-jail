// Package nofollow walks a directory tree on the host WITHOUT FOLLOWING A SYMLINK AN AGENT COULD HAVE
// PLACED, for trees an agent may write: a dev override's checkout (docs/design/pack-buildable-programs.md
// §5, BG-D22), and every source tree yolo stages into a build.
//
// filepath.WalkDir and os.ReadFile classify an entry by its path and then open it by its path again,
// so an entry swapped for a symlink between the two (`src/` → `~/.ssh`) is followed, and the host
// reads bytes outside the tree. And O_NOFOLLOW on a whole path covers only its LAST component: a
// directory above the root swapped for a link (`proj/vendor` → a host directory that also holds a
// `tool`) is followed silently.
//
// So nothing here opens a path. The ROOT is reached one component at a time from `/`, each opened
// relative to its parent's descriptor with O_NOFOLLOW (resolveParent). A symlink met on the way is
// followed only when it is TRUSTED — owned by root, in a directory owned by root that no one else can
// write (or a sticky one, where no one else can replace root's entry) — because no agent can have
// placed or re-pointed it: `/var` → `/private/var` and `/tmp` on macOS, `/home` → `/var/home` on
// Fedora Atomic, an autofs or corporate home link. Any other link refuses the walk
// (UntrustedLinkError). That reach is for a tree whose every ancestor may be an agent's, a dev
// override's checkout; WalkBelow instead takes a base that is the caller's own (yolo's state
// directory, the user's home), follows every link up to it, and no link from it down. BELOW the root, no link is ever followed: each entry is opened from its
// parent's descriptor with O_NOFOLLOW (and O_DIRECTORY for a directory, O_NONBLOCK for a file, so a
// planted FIFO cannot hang it), classified by fstat rather than by the path, and a symlink is only
// reported by its target (Readlinkat). A component swapped after it was listed fails the open
// (ELOOP) or the fstat check (ErrChanged). A race can make a walk fail; it cannot make one read
// anything outside the tree.
//
// A KNOWN LIMIT: a hard link to a file outside the tree is a regular file inside it, and is read. An
// agent can only hard-link a file it can already read, on the same filesystem; yolo does not refuse
// files with more than one link.
package nofollow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Kind is what a walked entry is.
type Kind int

const (
	Dir     Kind = iota // a directory, visited before its children
	File                // a regular file, open for reading in Entry.File
	Symlink             // a symlink, its target in Entry.Target; never followed
	Other               // a device, socket or pipe: never opened
)

// Entry is one walked entry. Rel is its path relative to the root, separated by the platform's
// separator ("." for the root). Perm is its permission bits, from the fstat of what was opened (a
// directory or file) or the lstat of a symlink or special file. File holds the
// directory/file descriptor only for the visit's duration and is closed by the walk.
// Directory visitors may Chmod that descriptor without reopening a raced path;
// only Kind File is a byte stream, and visitors must not close either descriptor.
type Entry struct {
	Rel    string
	Kind   Kind
	Perm   os.FileMode
	Target string
	File   *os.File
}

// ErrChanged is returned when an entry changed kind between its listing and its open: a file or
// directory swapped for a symlink, the race this package exists to refuse.
var ErrChanged = errors.New("changed while it was read (an entry was swapped for a symlink or another kind)")

// UntrustedLinkError is a symlink on the way to a root that is not trusted (see the package comment):
// Path is the link, Why says why it is not trusted.
type UntrustedLinkError struct {
	Path string
	Why  string
}

func (e *UntrustedLinkError) Error() string {
	return e.Path + " is a symlink " + e.Why + ", and yolo follows no symlink anyone but root could have placed"
}

const (
	dirFlags  = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fileFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	maxLinks  = 40
)

// testHookAfterLstat, when set by a test, runs between an entry's lstat and its open: the window a
// swap to a symlink races.
var testHookAfterLstat func(rel string)

// Walk visits root and everything under it, depth first, each directory's children sorted by name,
// skipping every Rel in skip (and its subtree). root is reached component by component, following
// only trusted links (an untrusted one is an *UntrustedLinkError); a root whose LAST component is an
// untrusted link is visited as one Symlink entry and nothing under it is read. visit's error stops
// the walk and is returned.
func Walk(root string, skip map[string]bool, visit func(Entry) error) error {
	parent, name, _, err := resolveParent(root, true)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	return walkAt(parent, name, ".", skip, visit)
}

// WalkBelow is Walk with the TRUST ROOT named by the caller: base, a directory that is the caller's
// own, such as yolo's state directory or the user's home. base is opened as the OS resolves it,
// following every link on the way, a link the user placed included (a state directory moved to a
// bigger disk, a dotfile manager's home). Every component from base down to root is opened from its
// parent's descriptor with O_NOFOLLOW and follows NO link at all, not even root's: that span is where
// an agent may write (a capture jail's workspace is staging/<id> under the store). A root whose last
// component is a link is visited as one Symlink entry, as in Walk. root must be base or under it.
//
// Walk's reach from `/` is for a tree whose every ancestor may be an agent's, a dev override's
// checkout (BG-D22); WalkBelow is for one whose ancestors above base are the user's (BG-D23).
func WalkBelow(base, root string, skip map[string]bool, visit func(Entry) error) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absBase, absRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is not under %s", root, base)
	}
	fd, err := openat(unix.AT_FDCWD, absBase, dirFlags&^unix.O_NOFOLLOW)
	if err != nil {
		return &os.PathError{Op: "open", Path: absBase, Err: err}
	}
	defer func() { unix.Close(fd) }()
	if rel == "." {
		return walkAt(fd, ".", ".", skip, visit)
	}
	comps := strings.Split(rel, string(filepath.Separator))
	cur := absBase
	for _, c := range comps[:len(comps)-1] {
		here := filepath.Join(cur, c)
		var st unix.Stat_t
		if err := unix.Fstatat(fd, c, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return &os.PathError{Op: "lstat", Path: here, Err: err}
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return &BelowBaseLinkError{Path: here, Base: absBase}
		}
		nfd, _, err := openChecked(fd, c, here, dirFlags, unix.S_IFDIR, &st)
		if err != nil {
			return err
		}
		unix.Close(fd)
		fd, cur = nfd, here
	}
	return walkAt(fd, comps[len(comps)-1], ".", skip, visit)
}

// BelowBaseLinkError is a symlink between WalkBelow's base and its root: Path is the link.
type BelowBaseLinkError struct {
	Path string
	Base string
}

func (e *BelowBaseLinkError) Error() string {
	return e.Path + " is a symlink below " + e.Base + ", where yolo follows no symlink, since an agent may have placed it — " +
		"replace the link with the directory it names, or, when it is in a capture's output, run the capture again: " +
		"an installer that leaves a symlink in its own output path is refused"
}

// Resolve is path with every trusted link on it resolved, read component by component as Walk reads
// a root, or why it cannot be: an *UntrustedLinkError for any other link (its last component
// included), or the error of a component that does not exist. What it returns has no symlink in it,
// so Walk reads exactly the directory Resolve named.
func Resolve(path string) (string, error) {
	parent, name, resolved, err := resolveParent(path, false)
	if err != nil {
		return "", err
	}
	defer unix.Close(parent)
	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", &os.PathError{Op: "lstat", Path: resolved, Err: err}
	}
	return resolved, nil
}

// resolveParent walks path from `/` and returns a descriptor of the directory holding its final
// component, that component's name ("." when the path ends at a directory with no name, `/`), and
// the resolved path. Every directory is opened from its parent's descriptor with O_NOFOLLOW; a
// trusted link is spliced into the components still to read; an untrusted one refuses, unless it is
// the final component and keepFinalLink asks for it as it is.
func resolveParent(path string, keepFinalLink bool) (fd int, name, resolved string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1, "", "", err
	}
	var dirSt unix.Stat_t
	openRoot := func() (int, error) {
		fd, err := openat(unix.AT_FDCWD, "/", dirFlags)
		if err != nil {
			return -1, &os.PathError{Op: "open", Path: "/", Err: err}
		}
		if err := unix.Fstat(fd, &dirSt); err != nil {
			unix.Close(fd)
			return -1, &os.PathError{Op: "fstat", Path: "/", Err: err}
		}
		return fd, nil
	}
	if fd, err = openRoot(); err != nil {
		return -1, "", "", err
	}
	fail := func(e error) (int, string, string, error) {
		unix.Close(fd)
		return -1, "", "", e
	}
	cur := "/"
	pending := strings.Split(abs, "/")
	links := 0
	for {
		for len(pending) > 0 && (pending[0] == "" || pending[0] == ".") {
			pending = pending[1:]
		}
		if len(pending) == 0 {
			return fd, ".", cur, nil
		}
		c, rest := pending[0], pending[1:]
		if c == ".." {
			// Up from the directory actually reached, never lexically: a descriptor's ".." is no link.
			nfd, err := openat(fd, "..", dirFlags)
			if err != nil {
				return fail(&os.PathError{Op: "open", Path: cur + "/..", Err: err})
			}
			unix.Close(fd)
			fd, cur, pending = nfd, filepath.Dir(cur), rest
			if err := unix.Fstat(fd, &dirSt); err != nil {
				return fail(&os.PathError{Op: "fstat", Path: cur, Err: err})
			}
			continue
		}
		here := filepath.Join(cur, c)
		var st unix.Stat_t
		if err := unix.Fstatat(fd, c, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if len(rest) == 0 {
				// The final component's own lstat is the caller's (walkAt, Resolve) to report.
				return fd, c, here, nil
			}
			return fail(&os.PathError{Op: "lstat", Path: here, Err: err})
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			if why := untrusted(&st, &dirSt); why != "" {
				if len(rest) == 0 && keepFinalLink {
					return fd, c, here, nil
				}
				return fail(&UntrustedLinkError{Path: here, Why: why})
			}
			if links++; links > maxLinks {
				return fail(&os.PathError{Op: "resolve", Path: here, Err: unix.ELOOP})
			}
			target, err := readlinkat(fd, c)
			if err != nil {
				return fail(&os.PathError{Op: "readlink", Path: here, Err: err})
			}
			if strings.HasPrefix(target, "/") {
				unix.Close(fd)
				if fd, err = openRoot(); err != nil {
					return -1, "", "", err
				}
				cur = "/"
			}
			pending = append(strings.Split(target, "/"), rest...)
			continue
		}
		if len(rest) == 0 {
			return fd, c, here, nil
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			return fail(&os.PathError{Op: "open", Path: here, Err: unix.ENOTDIR})
		}
		nfd, err := openat(fd, c, dirFlags)
		if err != nil {
			return fail(openFailure(here, err))
		}
		var got unix.Stat_t
		if err := unix.Fstat(nfd, &got); err != nil {
			unix.Close(nfd)
			return fail(&os.PathError{Op: "fstat", Path: here, Err: err})
		}
		if got.Ino != st.Ino || got.Dev != st.Dev {
			unix.Close(nfd)
			return fail(fmt.Errorf("%s: %w", here, ErrChanged))
		}
		unix.Close(fd)
		fd, dirSt, cur, pending = nfd, got, here, rest
	}
}

// untrusted is why a link (link, its lstat) in a directory (dir, its fstat) could have been placed
// or re-pointed by someone other than root, "" when it could not.
func untrusted(link, dir *unix.Stat_t) string {
	mode := uint32(dir.Mode)
	switch {
	case link.Uid != 0:
		return fmt.Sprintf("owned by uid %d, not root", link.Uid)
	case dir.Uid != 0:
		return fmt.Sprintf("in a directory owned by uid %d, who could replace it", dir.Uid)
	case mode&0o022 != 0 && mode&unix.S_ISVTX == 0:
		return "in a directory others can write, who could replace it"
	}
	return ""
}

func walkAt(parent int, name, rel string, skip map[string]bool, visit func(Entry) error) error {
	if skip[filepath.ToSlash(rel)] {
		return nil
	}
	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "lstat", Path: rel, Err: err}
	}
	perm := os.FileMode(st.Mode & 0o777)
	if testHookAfterLstat != nil {
		testHookAfterLstat(rel)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		target, err := readlinkat(parent, name)
		if err != nil {
			return &os.PathError{Op: "readlink", Path: rel, Err: err}
		}
		return visit(Entry{Rel: rel, Kind: Symlink, Perm: perm, Target: target})
	case unix.S_IFDIR:
		fd, got, err := openChecked(parent, name, rel, dirFlags, unix.S_IFDIR, &st)
		if err != nil {
			return err
		}
		d := os.NewFile(uintptr(fd), rel)
		defer d.Close()
		if err := visit(Entry{Rel: rel, Kind: Dir, Perm: os.FileMode(got.Mode & 0o777), File: d}); err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		if err != nil {
			return &os.PathError{Op: "readdir", Path: rel, Err: err}
		}
		sort.Strings(names)
		for _, n := range names {
			if err := walkAt(fd, n, filepath.Join(rel, n), skip, visit); err != nil {
				return err
			}
		}
		return nil
	case unix.S_IFREG:
		fd, got, err := openChecked(parent, name, rel, fileFlags, unix.S_IFREG, &st)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), rel)
		defer f.Close()
		return visit(Entry{Rel: rel, Kind: File, Perm: os.FileMode(got.Mode & 0o777), File: f})
	}
	return visit(Entry{Rel: rel, Kind: Other, Perm: perm})
}

// openChecked opens name under parent with flags, checks by fstat that it is still the entry st
// lstat'ed (kind, inode, device), and clears O_NONBLOCK, which only guarded the open.
func openChecked(parent int, name, rel string, flags int, kind uint32, st *unix.Stat_t) (int, unix.Stat_t, error) {
	var got unix.Stat_t
	fd, err := openat(parent, name, flags)
	if err != nil {
		return -1, got, openFailure(rel, err)
	}
	if err := unix.Fstat(fd, &got); err != nil {
		unix.Close(fd)
		return -1, got, &os.PathError{Op: "fstat", Path: rel, Err: err}
	}
	if uint32(got.Mode)&unix.S_IFMT != kind || got.Ino != st.Ino || got.Dev != st.Dev {
		unix.Close(fd)
		return -1, got, fmt.Errorf("%s: %w", rel, ErrChanged)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return -1, got, &os.PathError{Op: "fcntl", Path: rel, Err: err}
	}
	return fd, got, nil
}

// openFailure names an open that failed; ELOOP (or EMLINK, FreeBSD's spelling) or ENOTDIR is a
// component swapped for a symlink or another kind since it was listed.
func openFailure(rel string, err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) || errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%s: %w", rel, ErrChanged)
	}
	return &os.PathError{Op: "open", Path: rel, Err: err}
}

func openat(dirfd int, name string, flags int) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flags, 0)
		if err == unix.EINTR {
			continue
		}
		return fd, err
	}
}

func readlinkat(dirfd int, name string) (string, error) {
	for size := 256; ; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dirfd, name, buf)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buf[:n]), nil
		}
	}
}
