package unifiedhome

// compose.go is composition (docs/design/unified-workspace-home.md §5.2): what a launch does to
// its own slice. A pure plan layer (PlanCompose: two listings in, steps out) and a thin apply
// layer over no-link descriptors (Compose), which reuses the merge's primitives so a child that
// already exists in the unified home is merged, never replaced.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ComposeOp is what composition does with one name at one directory level.
type ComposeOp int

const (
	// ComposeAdopt moves the slice's real child into the unified home (merging when the name is
	// there) and puts a link in its place.
	ComposeAdopt ComposeOp = iota
	// ComposeLink links a unified child the slice does not have.
	ComposeLink
	// ComposeRecurse composes a directory that is a prefix of a generated or carved path: it
	// stays a real directory in the slice and its children are composed in turn.
	ComposeRecurse
	// ComposeLeave leaves the slice's entry where it is and counts it.
	ComposeLeave
)

// ComposeStep is one planned action at one directory level.
type ComposeStep struct {
	Name   string
	Op     ComposeOp
	Reason string // for ComposeLeave
}

// PlanCompose plans one directory level. slice and unified are the two listings; generated and
// carved are the paths, relative to this level and slash separated, that stay real in the slice:
// a name equal to one stays real whole, a name that is a proper prefix of one is composed
// recursively. A link in the slice is never adopted, followed or rewritten, and is not a step.
// Steps come back in name order; nothing is touched.
func PlanCompose(slice, unified []Entry, generated, carved []string) []ComposeStep {
	stays := append(append([]string(nil), generated...), carved...)
	sl := make(map[string]Entry, len(slice))
	ul := make(map[string]Entry, len(unified))
	names := map[string]bool{}
	for _, e := range slice {
		sl[e.Name] = e
		names[e.Name] = true
	}
	for _, e := range unified {
		ul[e.Name] = e
		names[e.Name] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	var steps []ComposeStep
	for _, n := range ordered {
		exact, prefix := matchStays(n, stays)
		s, inSlice := sl[n]
		u, inUnified := ul[n]
		switch {
		case exact:
			// Generated or carved: the slice's own, never shared.
		case inSlice && s.Kind == KindLink:
			// Already composed, or the user's own: never adopted, followed or rewritten.
		case prefix:
			switch {
			case inSlice && s.Kind != KindDir:
				steps = append(steps, ComposeStep{Name: n, Op: ComposeLeave, Reason: "a file where composition needs a directory"})
			case inUnified && u.Kind != KindDir:
				steps = append(steps, ComposeStep{Name: n, Op: ComposeLeave, Reason: "the unified home holds a file where composition needs a directory"})
			default:
				steps = append(steps, ComposeStep{Name: n, Op: ComposeRecurse})
			}
		case inSlice && s.Kind == KindOther:
			steps = append(steps, ComposeStep{Name: n, Op: ComposeLeave, Reason: leftReason(s)})
		case inSlice && s.Kind == KindFile && isProcessFile(n):
			steps = append(steps, ComposeStep{Name: n, Op: ComposeLeave, Reason: leftReason(s)})
		case inSlice:
			steps = append(steps, ComposeStep{Name: n, Op: ComposeAdopt})
		case inUnified:
			steps = append(steps, ComposeStep{Name: n, Op: ComposeLink})
		}
	}
	return steps
}

// matchStays reports whether name is one of the paths exactly, and whether it is a proper
// prefix (by whole segments) of one.
func matchStays(name string, paths []string) (exact, prefix bool) {
	for _, p := range paths {
		switch {
		case p == name:
			exact = true
		case strings.HasPrefix(p, name+"/"):
			prefix = true
		}
	}
	return exact, prefix
}

// below returns the paths that lie beneath name, relative to it.
func below(name string, paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, name+"/") {
			out = append(out, strings.TrimPrefix(p, name+"/"))
		}
	}
	return out
}

// ComposeSpec says what to compose.
type ComposeSpec struct {
	// Label names the launching setup in the ".from-" names a merge makes.
	Label string
	// HomePath is the ABSOLUTE path the jail sees the unified home at ("/workspace/.yolo/unified-home").
	// Every link target is built from it and the unified directory, so none can point elsewhere.
	HomePath string
	// Generated and Carved are paths relative to the composed directory that stay real in the
	// slice (see PlanCompose).
	Generated, Carved []string
}

// ComposeReport counts what one Compose did.
type ComposeReport struct {
	Report
	Linked int // links laid, adopted children and unified ones alike
	// Declined is set when the slice cannot hold links; DeclinedLine says so in one line and the
	// launch goes on. Nothing is lost: what was adopted before it stays in the unified home and a
	// later run finishes the links.
	Declined     bool
	DeclinedLine string
}

type composer struct {
	m       *merger
	spec    ComposeSpec
	udir    string
	symlink func(string, int, string) error
	rep     ComposeReport
	stop    bool
}

// Compose composes the directory sliceDir of the slice home against unifiedDir of the unified
// home. It writes only the slice and adds to the unified home: every move is a rename that
// refuses to replace (a name that exists is merged), and the one thing it removes from the
// slice is what it moved. Every descriptor is opened O_NOFOLLOW; a link already in the slice is
// never adopted, followed or rewritten. A slice directory that is absent or a link is left.
// A slice that cannot hold links is a Declined result, not an error; an error is a failed move.
func Compose(slice *os.Root, sliceDir string, unified *os.Root, unifiedDir string, s ComposeSpec, o Options) (ComposeReport, error) {
	c := &composer{m: &merger{rename: o.Rename, label: s.Label}, spec: s, udir: unifiedDir, symlink: o.Symlink}
	if c.m.rename == nil {
		c.m.rename = defaultRename
	}
	if c.symlink == nil {
		c.symlink = func(target string, dir int, name string) error { return unix.Symlinkat(target, dir, name) }
	}
	if !path.IsAbs(s.HomePath) || path.Clean(s.HomePath) != s.HomePath {
		return ComposeReport{}, fmt.Errorf("the unified home's path %q is not a clean absolute path", s.HomePath)
	}
	sroot, err := rootFd(slice)
	if err != nil {
		return ComposeReport{}, err
	}
	defer unix.Close(sroot)
	uroot, err := rootFd(unified)
	if err != nil {
		return ComposeReport{}, err
	}
	defer unix.Close(uroot)
	sfd, err := openDirAt(sroot, sliceDir)
	switch {
	case err == nil:
	case errors.Is(err, syscall.ENOENT):
		return ComposeReport{}, nil
	case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
		c.m.leave(sliceDir, "a link or not a directory")
		return c.finish(), nil
	default:
		return ComposeReport{}, err
	}
	defer unix.Close(sfd)
	ufd, err := ensureDir(uroot, unifiedDir)
	if err != nil {
		return ComposeReport{}, fmt.Errorf("open the unified directory %s: %w", unifiedDir, err)
	}
	defer unix.Close(ufd)
	c.level(sfd, ufd, "", s.Generated, s.Carved)
	return c.finish(), errors.Join(c.m.errs...)
}

func (c *composer) finish() ComposeReport {
	c.rep.Report = c.m.rep
	return c.rep
}

func (c *composer) level(sfd, ufd int, rel string, generated, carved []string) {
	sl, err := list(sfd)
	if err != nil {
		c.m.fail(rel, err)
		return
	}
	ul, err := list(ufd)
	if err != nil {
		c.m.fail(rel, err)
		return
	}
	for _, st := range PlanCompose(sl, ul, generated, carved) {
		if c.stop {
			return
		}
		p := path.Join(rel, st.Name)
		switch st.Op {
		case ComposeLeave:
			c.m.leave(p, st.Reason)
		case ComposeLink:
			c.link(sfd, rel, st.Name, p)
		case ComposeAdopt:
			c.m.levelOnly(sfd, ufd, rel, map[string]bool{st.Name: true})
			var stat unix.Stat_t
			switch err := unix.Fstatat(sfd, st.Name, &stat, unix.AT_SYMLINK_NOFOLLOW); {
			case errors.Is(err, syscall.ENOENT):
				c.link(sfd, rel, st.Name, p)
			case err == nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
				unix.Unlinkat(sfd, st.Name, unix.AT_REMOVEDIR) == nil:
				// The merge moved every child out and left the empty directory: rmdir removes
				// only an empty one, so no byte is lost, and a link takes its place.
				c.link(sfd, rel, st.Name, p)
			case err == nil:
				// A rename failed or the merge left something beneath it: the child stays a real
				// directory or file in the slice, and the next launch tries again.
				c.m.leave(p, "still holds what the merge did not move")
			default:
				c.m.fail(p, err)
			}
		case ComposeRecurse:
			c.recurse(sfd, ufd, rel, st.Name, p, below(st.Name, generated), below(st.Name, carved))
		}
	}
}

func (c *composer) recurse(sfd, ufd int, rel, name, p string, generated, carved []string) {
	for _, d := range []int{sfd, ufd} {
		if err := unix.Mkdirat(d, name, 0o755); err != nil && !errors.Is(err, syscall.EEXIST) {
			c.m.fail(p, err)
			return
		}
	}
	ss, serr := openDirAt(sfd, name)
	us, uerr := openDirAt(ufd, name)
	defer func() {
		if serr == nil {
			unix.Close(ss)
		}
		if uerr == nil {
			unix.Close(us)
		}
	}()
	if serr != nil || uerr != nil {
		c.m.fail(p, errors.Join(serr, uerr))
		return
	}
	c.level(ss, us, p, generated, carved)
}

func (c *composer) link(sfd int, rel, name, p string) {
	target := path.Join(c.spec.HomePath, c.udir, rel, name)
	err := c.symlink(target, sfd, name)
	switch {
	case err == nil:
		c.rep.Linked++
	case errors.Is(err, syscall.EEXIST):
		// Something took the name since the listing; it is the user's or another launch's.
	case cannotHoldLinks(err):
		c.rep.Declined = true
		c.rep.DeclinedLine = fmt.Sprintf("This setup's home cannot hold links (%v), so its sessions are not shared with the "+
			"workspace's other setups; the launch continues, and a later launch with a writable home shares them.", err)
		c.stop = true
	default:
		c.m.fail(p, err)
	}
}

// cannotHoldLinks reports an error that says the filesystem or the mount refuses to hold a link
// (read-only, a permission the launch cannot have, no symlink support), as against a failure of one move.
func cannotHoldLinks(err error) bool {
	for _, e := range []error{syscall.EROFS, syscall.EPERM, syscall.EACCES, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOSYS} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
