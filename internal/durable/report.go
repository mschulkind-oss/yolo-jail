package durable

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// WalkBudget bounds the size walk of the launch line: a fresh launch never waits longer than
// this for the figure, and a walk that stops says so (docs/design/durable-scratch-space.md
// §5.4, DS-D11).
const WalkBudget = 2 * time.Second

// adminFileCap bounds every git admin file this package reads. A `gitdir` or `HEAD` is one
// line; the cap keeps a jail-written file of any size from costing the host more than this.
const adminFileCap = 4 << 10

// Size is one measurement of the durable dir.
type Size struct {
	Bytes int64
	// Partial is set when the walk stopped at its budget: Bytes is then a lower bound.
	Partial bool
	// Unreadable counts directories the walk could not open and skipped.
	Unreadable int
}

// Measure is the lstat total of every regular file in workspace's durable dir, following no
// link, stopping once budget has elapsed (zero means no budget). It opens the dir through
// Open, which refuses a link at `.yolo` or `durable`, and descends only into entries getdents
// reports as directories, each opened as a root of its own that refuses a link swapped in
// meanwhile, so no path it stats leaves the tree. os.ErrNotExist means there is no durable
// dir.
func Measure(workspace string, budget time.Duration, now func() time.Time) (Size, error) {
	return MeasureRel(workspace, "", budget, now)
}

// MeasureRel is Measure of rel, a directory below the durable dir, reached one component at
// a time, so a link the jail put at any component is refused rather than followed — the
// per-worktree figure of the `yolo check` report.
func MeasureRel(workspace, rel string, budget time.Duration, now func() time.Time) (Size, error) {
	if now == nil {
		now = time.Now
	}
	r, err := Open(workspace)
	if err != nil {
		return Size{}, err
	}
	full := HostPath(workspace)
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			r.Close()
			return Size{}, &fs.PathError{Op: "open", Path: rel, Err: fs.ErrInvalid}
		}
		full = filepath.Join(full, part)
		sub, err := paths.OpenStateSubdirRoot(r, part, full)
		r.Close()
		if err != nil {
			return Size{}, err
		}
		r = sub
	}
	defer r.Close()
	var deadline time.Time
	if budget > 0 {
		deadline = now().Add(budget)
	}
	var s Size
	measureRoot(r, &s, deadline, now)
	return s, nil
}

// readDirBatch is how many entries measureRoot reads at a time, so one huge directory is read
// in slices with the deadline checked between them rather than listed whole first.
const readDirBatch = 256

func measureRoot(r *os.Root, s *Size, deadline time.Time, now func() time.Time) {
	f, err := r.Open(".")
	if err != nil {
		s.Unreadable++
		return
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(readDirBatch)
		for _, e := range entries {
			if s.Partial {
				return
			}
			if !deadline.IsZero() && now().After(deadline) {
				s.Partial = true
				return
			}
			switch t := e.Type(); {
			case t&fs.ModeSymlink != 0:
				// Counted as nothing: its target is not the durable dir's bytes.
			case t.IsDir():
				// Refuses a link swapped in since getdents said "directory", where r.OpenRoot
				// would follow one that stays inside r.
				sub, err := paths.OpenStateSubdirRoot(r, e.Name(), e.Name())
				if err != nil {
					s.Unreadable++
					continue
				}
				measureRoot(sub, s, deadline, now)
				sub.Close()
			case t.IsRegular():
				if fi, err := r.Lstat(e.Name()); err == nil && fi.Mode().IsRegular() {
					s.Bytes += fi.Size()
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.Unreadable++
			}
			return
		}
		if !deadline.IsZero() && now().After(deadline) {
			s.Partial = true
			return
		}
	}
}

// Printable is s with every control and bidi-format character removed, a newline, carriage
// return or tab becoming a space. Every string this package hands out that the JAIL chose (an
// entry name, a registration's path, its branch) passes through it before it is shown,
// because host yolo prints them on the human's terminal, where an ESC or OSC sequence in a
// directory name would otherwise be a terminal command.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r):
			return -1
		}
		return r
	}, s)
}

// Worktree is one git worktree registration of the workspace's repository, read from its
// admin directory `.git/worktrees/<ID>`.
type Worktree struct {
	// ID is the admin directory's name.
	ID string
	// Path is the worktree's directory at this frame's spelling (Aliases applied).
	Path string
	// Rel is Path relative to the durable dir, or "" when the worktree is not beneath it.
	Rel string
	// Branch is the checked-out branch's short name, or "" when HEAD is detached.
	Branch string
	// Head is the detached commit (the first eight hex digits), when Branch is "".
	Head string
	// LastActive is the newer of the admin directory's HEAD and index mtimes: the worktree's
	// last git activity.
	LastActive time.Time
	// Locked is true when the registration carries a `locked` file.
	Locked bool
}

// Describe is the worktree's branch column: the branch, or "detached at <sha>", Printable.
func (w Worktree) Describe() string {
	switch {
	case w.Branch != "":
		return Printable(w.Branch)
	case w.Head != "":
		return "detached at " + Printable(w.Head)
	}
	return "unknown HEAD"
}

// Name is how a report names the worktree, Printable: its path below the durable dir's
// worktrees/, or below the durable dir, or its full path.
func (w Worktree) Name() string {
	if w.Rel == "" {
		return Printable(w.Path)
	}
	return Printable(strings.TrimPrefix(w.Rel, WorktreesDir+"/"))
}

// RelName is Rel, Printable: the `yolo check` row's first column.
func (w Worktree) RelName() string { return Printable(w.Rel) }

// ScanOptions says whose durable dir to scan, at which frame's spelling.
type ScanOptions struct {
	// Workspace is the workspace's directory at this frame's spelling (`/workspace` in a
	// container jail, the host path on the host and on macos-user).
	// The durable dir scanned is this workspace's, opened through Open.
	Workspace string
	// Aliases maps another frame's spelling of a directory to this frame's: a worktree made
	// in a container jail records `/workspace/…`, which the host reads as its workspace, and
	// one made on the host records the host path, which the jail reads as `/workspace`. The
	// workspace's own resolved spelling is always added: git records a worktree's path with
	// every link resolved, so a workspace reached through a symbolic link (darwin's /tmp and
	// /var/folders) would otherwise have no worktree beneath its durable dir.
	Aliases map[string]string
	// GoneRoots are the directories below which a registration OUTSIDE the durable dir is
	// checked for a missing directory, for the prunable count: an in-jail caller passes the
	// workspace and the per-launch set (prune.ScratchSlots' destinations), which is where the
	// /tmp incident class lives. A registration elsewhere is never judged: from a container
	// jail a worktree the host made beside the repository is invisible rather than gone, and
	// on the host a jail-written admin file would otherwise choose which host paths yolo
	// stats. Empty, as on the host, checks only beneath the durable dir. Each root is also
	// tried with its links resolved (git records `/private/tmp/…` for darwin's `/tmp`).
	GoneRoots []string
}

// maxRegistrations bounds how many `.git/worktrees` entries one scan reads, so a jail that
// made a million of them costs the host a bounded walk; Scan.Capped says it was reached.
const maxRegistrations = 1000

// Scan is what the durable dir holds.
type Scan struct {
	// Worktrees are the registrations of the workspace's repository beneath the durable dir,
	// the durable worktrees (§1.2), oldest activity first.
	Worktrees []Worktree
	// Others are the durable dir's top-level entries other than worktrees/, by name, Printable.
	Others []string
	// Prunable are the registrations whose directory no longer exists, Printable: the /tmp
	// incident class. One beneath the durable dir is always checked (beneath its root); one
	// outside it only below ScanOptions.GoneRoots. Never a locked one, which git itself does
	// not prune.
	Prunable []string
	// Capped is set when the repository has more registrations than one scan reads
	// (maxRegistrations); the figures then cover the first ones by name.
	Capped bool
	// NotGit is set when the workspace has no `.git` directory this package can read (not a
	// repository, a linked worktree or submodule, or a `.git` the jail replaced with a link).
	// WorktreeDirs then counts the directories beneath worktrees/ instead.
	NotGit       bool
	WorktreeDirs int
	// Err is the reason the durable dir itself could not be listed, if it could not.
	Err error
}

// ScanDir reads what the durable dir holds: its top-level entries, and the workspace
// repository's worktree registrations from the admin files under `.git/worktrees`. It never
// runs git, never reads a file beneath the durable dir, and follows no link.
func ScanDir(o ScanOptions) Scan {
	var sc Scan
	dr, err := Open(o.Workspace)
	if err != nil {
		sc.Err = err
		return sc
	}
	defer dr.Close()
	if entries, err := readDirNames(dr, "."); err == nil {
		for _, name := range entries {
			if name != WorktreesDir {
				sc.Others = append(sc.Others, Printable(name))
			}
		}
	} else {
		sc.Err = err
		return sc
	}

	durableDir := HostPath(o.Workspace)
	aliases := make(map[string]string, len(o.Aliases)+1)
	for k, v := range o.Aliases {
		aliases[k] = v
	}
	// The workspace is not jail-writable ABOVE its own directory, so resolving its links is
	// safe in every frame; `.yolo` and below are never resolved.
	if real, err := filepath.EvalSymlinks(o.Workspace); err == nil && real != o.Workspace {
		aliases[real] = o.Workspace
	}
	var goneRoots []string
	for _, r := range o.GoneRoots {
		goneRoots = append(goneRoots, r)
		if real, err := filepath.EvalSymlinks(r); err == nil && real != r {
			goneRoots = append(goneRoots, real)
		}
	}

	regs, ok, capped := registrations(o.Workspace, aliases)
	sc.Capped = capped
	if !ok {
		sc.NotGit = true
		if names, err := readDirNames(dr, WorktreesDir); err == nil {
			for _, n := range names {
				if fi, err := dr.Lstat(WorktreesDir + "/" + n); err == nil && isDir(fi) {
					sc.WorktreeDirs++
				}
			}
		}
		return sc
	}
	for _, w := range regs {
		if rel, ok := beneath(durableDir, w.Path); ok {
			// Beneath the root on the durable dir, so this stat is safe in every frame: a
			// registration whose tree was deleted is not a durable worktree any more.
			if _, err := dr.Lstat(filepath.Join(rel, ".git")); errors.Is(err, fs.ErrNotExist) {
				if !w.Locked {
					sc.Prunable = append(sc.Prunable, Printable(w.Path))
				}
				continue
			}
			w.Rel = rel
			sc.Worktrees = append(sc.Worktrees, w)
			continue
		}
		if w.Locked || !beneathAny(goneRoots, w.Path) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(w.Path, ".git")); errors.Is(err, fs.ErrNotExist) {
			sc.Prunable = append(sc.Prunable, Printable(w.Path))
		}
	}
	sort.SliceStable(sc.Worktrees, func(i, j int) bool {
		return sc.Worktrees[i].LastActive.Before(sc.Worktrees[j].LastActive)
	})
	sort.Strings(sc.Prunable)
	return sc
}

// registrations reads the `.git/worktrees/<id>` entries of the workspace's repository, at
// most maxRegistrations of them (capped says more were there). ok is false when the
// workspace has no readable `.git` directory.
func registrations(workspace string, aliases map[string]string) (out []Worktree, ok, capped bool) {
	gitDir := filepath.Join(workspace, ".git")
	gr, err := paths.OpenStateDirRoot(gitDir)
	if err != nil {
		return nil, false, false
	}
	defer gr.Close()
	wr, err := paths.OpenStateSubdirRoot(gr, "worktrees", filepath.Join(gitDir, "worktrees"))
	if err != nil {
		// A repository with no linked worktree has no worktrees/ directory at all.
		return nil, errors.Is(err, fs.ErrNotExist), false
	}
	defer wr.Close()
	f, err := wr.Open(".")
	if err != nil {
		return nil, true, false
	}
	ids, _ := f.Readdirnames(maxRegistrations + 1)
	f.Close()
	if len(ids) > maxRegistrations {
		ids, capped = ids[:maxRegistrations], true
	}
	sort.Strings(ids)
	for _, id := range ids {
		admin := filepath.Join(gitDir, "worktrees", id)
		ar, err := paths.OpenStateSubdirRoot(wr, id, admin)
		if err != nil {
			continue
		}
		w, ok := readRegistration(ar, id, admin, aliases)
		ar.Close()
		if ok {
			out = append(out, w)
		}
	}
	return out, true, capped
}

func readRegistration(ar *os.Root, id, admin string, aliases map[string]string) (Worktree, bool) {
	gitdir, err := readAdminFile(ar, "gitdir")
	if err != nil || gitdir == "" {
		return Worktree{}, false
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(admin, gitdir)
	}
	gitdir = applyAliases(filepath.Clean(gitdir), aliases)
	w := Worktree{ID: id, Path: strings.TrimSuffix(gitdir, string(filepath.Separator)+".git")}
	if head, err := readAdminFile(ar, "HEAD"); err == nil {
		if ref, ok := strings.CutPrefix(head, "ref: "); ok {
			w.Branch = strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
		} else if len(head) >= 8 {
			w.Head = head[:8]
		}
	}
	for _, name := range []string{"HEAD", "index"} {
		if fi, err := ar.Lstat(name); err == nil && fi.ModTime().After(w.LastActive) {
			w.LastActive = fi.ModTime()
		}
	}
	if _, err := ar.Lstat("locked"); err == nil {
		w.Locked = true
	}
	return w, true
}

// readAdminFile reads one line of a git admin file beneath ar: a regular file only, never a
// link, and at most adminFileCap bytes of it.
func readAdminFile(ar *os.Root, name string) (string, error) {
	f, err := paths.OpenRegularFileBeneath(ar, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, adminFileCap))
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(line), nil
}

func applyAliases(p string, aliases map[string]string) string {
	// Longest prefix first, so a nested alias wins over its parent.
	keys := make([]string, 0, len(aliases))
	for k := range aliases {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, from := range keys {
		if from == "" {
			continue
		}
		if p == from {
			return aliases[from]
		}
		if rest, ok := strings.CutPrefix(p, from+string(filepath.Separator)); ok {
			return filepath.Join(aliases[from], rest)
		}
	}
	return p
}

// beneath reports p relative to dir when p is dir or below it.
func beneath(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func beneathAny(dirs []string, p string) bool {
	for _, d := range dirs {
		if d == p {
			return true
		}
		if _, ok := beneath(d, p); ok {
			return true
		}
	}
	return false
}

func readDirNames(r *os.Root, name string) ([]string, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	sort.Strings(names)
	return names, err
}

// isDir reports whether fi is a real directory (not a link to one).
func isDir(fi os.FileInfo) bool { return fi.Mode()&fs.ModeSymlink == 0 && fi.IsDir() }

// HumanBytes renders n in decimal units, one decimal above a megabyte: "1.2 GB", "412 MB".
func HumanBytes(n int64) string {
	const kb, mb, gb = 1000, 1000 * 1000, 1000 * 1000 * 1000
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	case n >= 10*mb:
		return fmt.Sprintf("%d MB", n/mb)
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%d kB", n/kb)
	}
	return fmt.Sprintf("%d B", n)
}

// HumanIdle renders an idle time in whole days, or hours under a day.
func HumanIdle(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "under an hour"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour")
	}
	return plural(int(d/(24*time.Hour)), "day")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// LaunchLines are the launch's report on the durable dir (§5.4): nothing when it is empty
// and no registration is prunable; otherwise one line of what it holds, and a second when
// registrations point at directories that are gone.
func LaunchLines(sc Scan, sz Size, sizeErr error, budget time.Duration, now time.Time) []string {
	var out []string
	var what []string
	switch {
	case sc.Err != nil:
		// Nothing listed: nothing to say about contents.
	case sc.NotGit && sc.WorktreeDirs > 0:
		what = append(what, count(sc.WorktreeDirs, "directory", "directories")+" under worktrees/")
	case len(sc.Worktrees) > 0:
		what = append(what, count(len(sc.Worktrees), "worktree", "worktrees"))
	}
	if sc.Err == nil && len(sc.Others) > 0 {
		one, many := "entry", "entries"
		if len(what) > 0 {
			one, many = "other entry", "other entries"
		}
		what = append(what, count(len(sc.Others), one, many))
	}
	if len(what) > 0 {
		parts := []string{strings.Join(what, " and ")}
		switch {
		case sizeErr != nil:
			parts = append(parts, "size unknown")
		case sz.Partial:
			parts = append(parts, fmt.Sprintf("≥ %s (size walk stopped at %s)", HumanBytes(sz.Bytes), budget))
		default:
			parts = append(parts, HumanBytes(sz.Bytes))
		}
		if len(sc.Worktrees) > 0 {
			oldest := sc.Worktrees[0]
			if !oldest.LastActive.IsZero() {
				parts = append(parts, fmt.Sprintf("oldest idle %s (%s)", HumanIdle(now.Sub(oldest.LastActive)), oldest.Name()))
			}
		}
		out = append(out, "Durable dir: "+strings.Join(parts, ", ")+
			" — `yolo check` lists them; yolo deletes nothing here.")
	}
	if n := len(sc.Prunable); n > 0 {
		shown := sc.Prunable
		more := ""
		if len(shown) > 3 {
			shown, more = shown[:3], ", …"
		}
		noun := "worktree registrations point"
		if n == 1 {
			noun = "worktree registration points"
		}
		out = append(out, fmt.Sprintf("Durable dir: %d %s at paths that no longer exist (%s%s) — "+
			"`git worktree prune -n -v` lists what a prune would drop.", n, noun, strings.Join(shown, ", "), more))
	}
	return out
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
