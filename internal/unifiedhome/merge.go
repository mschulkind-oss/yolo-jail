package unifiedhome

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// LinkForm is one spelling of the link composition lays: an absolute one (HomePath, the unified
// home as the jail sees it) or a relative one (FromSliceRoot, the unified home as seen from the
// slice root). Exactly one is set. It is the one place both spellings are built.
type LinkForm struct{ HomePath, FromSliceRoot string }

// Target is the link target at a position: depth is the number of directories between the slice
// root and the link, and unifiedPath is "<unified dir>/<rel>/<name>".
func (f LinkForm) Target(depth int, unifiedPath string) string {
	if f.FromSliceRoot != "" {
		return strings.Repeat("../", depth) + f.FromSliceRoot + "/" + unifiedPath
	}
	return path.Join(f.HomePath, unifiedPath)
}

// Source is one home to read from, labeled for .from- names.
type Source struct {
	Label string   // "podman-self", "legacy"
	Root  *os.Root // the home, opened beneath a no-link chain
	Dirs  []string // home-relative state directories to read: both spellings, ".claude" and "claude"
	// Stay are paths, relative to each of Dirs and slash separated, that are never moved out of
	// the source: a generated file, an install, a pack's carve-out. A name equal to one stays
	// whole; a directory that is a proper prefix of one is merged child by child. Nothing that
	// stays is counted as left: it was never the merge's to move.
	Stay []string
}

// Options tunes a merge.
type Options struct {
	// Rename moves old in oldDir to new in newDir and must fail with EEXIST rather than replace.
	// Nil means the platform's refuse-to-replace rename.
	Rename func(oldDir int, old string, newDir int, new string) error
	// OwnerUIDs, when non-empty, leaves a source state directory that none of these users owns.
	// Nil means unchecked.
	OwnerUIDs []uint32
	// Laid are the spellings of a link a launch lays into a slice (Compose). A source link whose
	// target is exactly what one of them lays at its position is yolo's own, not the user's: it is
	// neither moved nor counted, so a composed slice re-merges as a no-op.
	Laid []LinkForm
	// Symlink makes the link Compose lays: target, the directory descriptor, the name. Nil means
	// symlinkat(2). A test injects the failure of a slice that cannot hold links.
	Symlink func(target string, dirFd int, name string) error
}

// LeftEntry is something a merge did not move.
type LeftEntry struct {
	Path   string // relative to the source home
	Reason string
}

// Report counts what one Merge did.
type Report struct {
	Moved    int // files and links that arrived in the destination, kept-both ones included
	Skipped  int // regular files whose bytes the destination already held
	KeptBoth int // arrivals that sit beside a different file as <name>.from-<label>
	Left     []LeftEntry
}

type merger struct {
	rename func(int, string, int, string) error
	label  string
	rep    Report
	errs   []error
	// stay and base are Source.Stay and the source directory being walked, which a path's
	// Stay-relative spelling is cut from.
	stay []string
	base string
	// laid and dstDir recognize a link yolo laid: dstDir is the unified directory the walk merges into.
	laid   []LinkForm
	dstDir string
}

// Merge merges the state directories src.Dirs of one home into dstDir beneath dst. It deletes
// and overwrites nothing: every step is a rename that refuses to replace, or a skip. A source
// directory that is a link, owned by another user (Options.OwnerUIDs), or a child that cannot be
// moved is left and counted; a failed rename is also returned (joined) so the caller can say so.
// Every descriptor is opened with O_NOFOLLOW relative to its parent, so no link is ever
// followed; the source tree is its own worklist, so a rerun after a crash continues.
func Merge(dst *os.Root, dstDir string, src Source, o Options) (Report, error) {
	m := &merger{rename: o.Rename, label: src.Label, stay: src.Stay, laid: o.Laid, dstDir: dstDir}
	if m.rename == nil {
		m.rename = defaultRename
	}
	dstRoot, err := rootFd(dst)
	if err != nil {
		return m.rep, err
	}
	defer unix.Close(dstRoot)
	srcRoot, err := rootFd(src.Root)
	if err != nil {
		return m.rep, err
	}
	defer unix.Close(srcRoot)

	dstFd := -1
	defer func() {
		if dstFd >= 0 {
			unix.Close(dstFd)
		}
	}()
	for _, d := range src.Dirs {
		sfd, err := openDirAt(srcRoot, d)
		switch {
		case err == nil:
		case errors.Is(err, syscall.ENOENT):
			continue
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
			m.leave(d, "a link or not a directory")
			continue
		default:
			m.fail(d, err)
			continue
		}
		if len(o.OwnerUIDs) > 0 {
			var st unix.Stat_t
			if err := unix.Fstat(sfd, &st); err != nil || !slices.Contains(o.OwnerUIDs, st.Uid) {
				unix.Close(sfd)
				m.leave(d, "not owned by the user")
				continue
			}
		}
		if dstFd < 0 {
			if dstFd, err = openDestination(dstRoot, dstDir); err != nil {
				unix.Close(sfd)
				return m.rep, errors.Join(append(m.errs, fmt.Errorf("open the destination %s: %w", dstDir, err))...)
			}
		}
		m.base = d
		m.level(sfd, dstFd, d)
		unix.Close(sfd)
	}
	return m.rep, errors.Join(m.errs...)
}

func (m *merger) leave(p, why string) {
	m.rep.Left = append(m.rep.Left, LeftEntry{Path: p, Reason: why})
}

func (m *merger) fail(p string, err error) {
	m.leave(p, "could not be moved: "+err.Error())
	m.errs = append(m.errs, fmt.Errorf("%s: %w", p, err))
}

// level merges the directory sfd names into the one dfd names; rel is the source path for reports.
func (m *merger) level(sfd, dfd int, rel string) { m.levelOnly(sfd, dfd, rel, nil) }

// levelOnly is level restricted to the source children in only (all of them when nil). Only the
// top level is restricted: a directory taken is merged whole.
func (m *merger) levelOnly(sfd, dfd int, rel string, only map[string]bool) {
	srcE, err := list(sfd)
	if err != nil {
		m.fail(rel, err)
		return
	}
	if only != nil {
		kept := srcE[:0]
		for _, e := range srcE {
			if only[e.Name] {
				kept = append(kept, e)
			}
		}
		srcE = kept
	}
	srcE = m.dropLaid(sfd, rel, srcE)
	srcE = m.applyStay(sfd, dfd, rel, srcE)
	dstE, err := list(dfd)
	if err != nil {
		m.fail(rel, err)
		return
	}
	dstBy := make(map[string]int, len(dstE))
	for i, d := range dstE {
		dstBy[d.Name] = i
	}
	kinds := make(map[string]Kind, len(srcE))
	for i, s := range srcE {
		kinds[s.Name] = s.Kind
		if j, ok := dstBy[s.Name]; ok && s.Kind == KindFile && dstE[j].Kind == KindFile {
			srcE[i].ModTime = modTime(sfd, s.Name)
			dstE[j].ModTime = modTime(dfd, s.Name)
		}
	}
	steps := PlanLevel(srcE, dstE, func(name string) bool { return sameBytes(sfd, dfd, name) }, m.label)
	for _, st := range steps {
		p := path.Join(rel, st.Name)
		switch st.Op {
		case OpLeave:
			m.leave(p, st.Reason)
		case OpSkip:
			m.rep.Skipped++
		case OpRename:
			if kinds[st.Name] == KindDir && holdsLeftovers(sfd, st.Name) {
				// A rename would carry a lock, pid or socket file along: merge level by level.
				if err := unix.Mkdirat(dfd, st.Name, 0o755); err != nil && !isExist(err) {
					m.fail(p, err)
					continue
				}
				m.recurse(sfd, dfd, st.Name, p)
				continue
			}
			final, err := m.move(sfd, st.Name, dfd, st.Name, st.Name+".from-"+strings.ReplaceAll(m.label, "/", "_"))
			if err != nil {
				m.fail(p, err)
				continue
			}
			m.arrived(dfd, final, kinds[st.Name], final != st.Name)
		case OpKeepBoth:
			final, err := m.move(sfd, st.Name, dfd, st.Target, st.Target)
			if err != nil {
				m.fail(p, err)
				continue
			}
			m.arrived(dfd, final, kinds[st.Name], true)
		case OpDisplace:
			_, err := m.move(dfd, st.Name, dfd, st.Target, st.Target)
			if err != nil {
				m.fail(p, err)
				continue
			}
			final := st.Name
			if err := m.rename(sfd, st.Name, dfd, st.Name); err != nil {
				if !isExist(err) {
					m.fail(p, err)
					continue
				}
				if final, err = m.move(sfd, st.Name, dfd, st.Target+".src", st.Target+".src"); err != nil {
					m.fail(p, err)
					continue
				}
			}
			m.arrived(dfd, final, KindFile, true)
		case OpRecurse:
			m.recurse(sfd, dfd, st.Name, p)
		}
	}
}

// dropLaid drops from srcE every link whose target is exactly what a launch lays at that position.
// The link is read, never followed.
func (m *merger) dropLaid(sfd int, rel string, srcE []Entry) []Entry {
	if len(m.laid) == 0 {
		return srcE
	}
	depth := len(strings.Split(rel, "/"))
	within := strings.TrimPrefix(strings.TrimPrefix(rel, m.base), "/")
	kept := srcE[:0]
	for _, e := range srcE {
		if e.Kind == KindLink && m.isLaid(sfd, e.Name, depth, path.Join(m.dstDir, within, e.Name)) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

func (m *merger) isLaid(sfd int, name string, depth int, unifiedPath string) bool {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(sfd, name, buf)
	if err != nil || n >= len(buf) {
		return false
	}
	got := string(buf[:n])
	for _, f := range m.laid {
		if (f.HomePath != "") != (f.FromSliceRoot != "") && f.Target(depth, unifiedPath) == got {
			return true
		}
	}
	return false
}

// applyStay drops from srcE every child Source.Stay keeps in the source, and merges a directory that
// is only a prefix of a stay path child by child (it is made at the destination first, so the
// plan never renames it whole, carrying what stays along).
func (m *merger) applyStay(sfd, dfd int, rel string, srcE []Entry) []Entry {
	if len(m.stay) == 0 {
		return srcE
	}
	kept := make([]Entry, 0, len(srcE))
	for _, e := range srcE {
		p := path.Join(rel, e.Name)
		exact, prefix := matchStays(strings.TrimPrefix(p, m.base+"/"), m.stay)
		switch {
		case exact:
		case prefix && e.Kind == KindDir:
			if err := unix.Mkdirat(dfd, e.Name, 0o755); err != nil && !isExist(err) {
				m.fail(p, err)
				continue
			}
			m.recurse(sfd, dfd, e.Name, p)
		case prefix:
		default:
			kept = append(kept, e)
		}
	}
	return kept
}

// recurse merges the child directory name of sfd into the one of dfd.
func (m *merger) recurse(sfd, dfd int, name, p string) {
	ss, serr := openDirAt(sfd, name)
	ds, derr := openDirAt(dfd, name)
	if serr != nil || derr != nil {
		if serr == nil {
			unix.Close(ss)
		}
		if derr == nil {
			unix.Close(ds)
		}
		m.fail(p, errors.Join(serr, derr))
		return
	}
	m.level(ss, ds, p)
	unix.Close(ss)
	unix.Close(ds)
}

// holdsLeftovers reports whether the directory name in dir holds, at any depth, a socket, fifo,
// device, or lock or pid file: things a whole-directory rename must not carry along.
func holdsLeftovers(dir int, name string) bool {
	fd, err := openDirAt(dir, name)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	es, err := list(fd)
	if err != nil {
		return false
	}
	for _, e := range es {
		switch {
		case e.Kind == KindDir:
			if holdsLeftovers(fd, e.Name) {
				return true
			}
		case e.Kind == KindFile:
			if isProcessFile(e.Name) {
				return true
			}
		case e.Kind == KindOther:
			return true
		}
	}
	return false
}

// arrived counts one moved entry; a directory counts the files and links beneath it.
func (m *merger) arrived(dfd int, name string, k Kind, both bool) {
	m.rep.Moved += countLeaves(dfd, name, k)
	if both {
		m.rep.KeptBoth++
	}
}

// move renames from to to, refusing to replace. When to exists (a race, or a rename that lost
// it), the entry takes fallback, then fallback.2, fallback.3 ... instead. It returns the name used.
func (m *merger) move(fromFd int, from string, toFd int, to, fallback string) (string, error) {
	err := m.rename(fromFd, from, toFd, to)
	if err == nil {
		return to, nil
	}
	if !isExist(err) {
		return "", err
	}
	for n := 1; n < 10000; n++ {
		cand := fallback
		if n > 1 {
			cand = fallback + "." + strconv.Itoa(n)
		}
		if cand == to {
			continue
		}
		err = m.rename(fromFd, from, toFd, cand)
		if err == nil {
			return cand, nil
		}
		if !isExist(err) {
			return "", err
		}
	}
	return "", err
}

func isExist(err error) bool {
	return errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY)
}

// rootFd returns a descriptor of its own for the directory a root holds.
func rootFd(r *os.Root) (int, error) {
	if r == nil {
		return -1, errors.New("nil root")
	}
	f, err := r.Open(".")
	if err != nil {
		return -1, err
	}
	defer f.Close()
	return unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
}

// openDestination opens dir beneath root, making it; "." is the root itself (a whole home).
func openDestination(root int, dir string) (int, error) {
	if dir == "." {
		return unix.FcntlInt(uintptr(root), unix.F_DUPFD_CLOEXEC, 0)
	}
	return ensureDir(root, dir)
}

func openDirAt(parent int, name string) (int, error) {
	return unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

// ensureDir opens rel (a relative, dotless path) beneath root, making each missing component,
// refusing a link at any of them.
func ensureDir(root int, rel string) (int, error) {
	rel = path.Clean(rel)
	if rel == "." || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return -1, fmt.Errorf("%q is not a relative path beneath the home", rel)
	}
	cur, err := unix.FcntlInt(uintptr(root), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, c := range strings.Split(rel, "/") {
		if err := unix.Mkdirat(cur, c, 0o755); err != nil && !errors.Is(err, syscall.EEXIST) {
			unix.Close(cur)
			return -1, err
		}
		next, err := openDirAt(cur, c)
		unix.Close(cur)
		if err != nil {
			return -1, err
		}
		cur = next
	}
	return cur, nil
}

// list lists the directory fd names, reading each child without following a link.
func list(fd int) ([]Entry, error) {
	dup, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "dir")
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, syscall.ENOENT) {
				continue
			}
			return nil, err
		}
		e := Entry{Name: n, Size: int64(st.Size)}
		switch uint32(st.Mode) & unix.S_IFMT {
		case unix.S_IFREG:
			e.Kind = KindFile
		case unix.S_IFDIR:
			e.Kind = KindDir
		case unix.S_IFLNK:
			e.Kind = KindLink
		default:
			e.Kind = KindOther
		}
		out = append(out, e)
	}
	return out, nil
}

func openFileAt(dir int, name string) (*os.File, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func modTime(dir int, name string) (t time.Time) {
	f, err := openFileAt(dir, name)
	if err != nil {
		return t
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil {
		return fi.ModTime()
	}
	return t
}

// sameBytes reports whether the regular files of that name in the two directories are identical.
func sameBytes(a, b int, name string) bool {
	fa, err := openFileAt(a, name)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := openFileAt(b, name)
	if err != nil {
		return false
	}
	defer fb.Close()
	ia, err := fa.Stat()
	if err != nil || !ia.Mode().IsRegular() {
		return false
	}
	ib, err := fb.Stat()
	if err != nil || !ib.Mode().IsRegular() || ia.Size() != ib.Size() {
		return false
	}
	bufA, bufB := make([]byte, 32<<10), make([]byte, 32<<10)
	for {
		na, ea := io.ReadFull(fa, bufA)
		nb, eb := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false
		}
		if ea != nil || eb != nil {
			return errors.Is(ea, io.EOF) || errors.Is(ea, io.ErrUnexpectedEOF)
		}
	}
}

// countLeaves counts the files and links an arrived entry brings: itself, or all beneath a directory.
func countLeaves(dir int, name string, k Kind) int {
	if k != KindDir {
		return 1
	}
	fd, err := openDirAt(dir, name)
	if err != nil {
		return 1
	}
	defer unix.Close(fd)
	es, err := list(fd)
	if err != nil {
		return 1
	}
	n := 0
	for _, e := range es {
		n += countLeaves(fd, e.Name, e.Kind)
	}
	return n
}
