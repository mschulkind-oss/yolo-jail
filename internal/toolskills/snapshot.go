package toolskills

// snapshot.go is the snapshot writer (docs/design/mise-tool-skills.md MS-D4, MS-D6, MS-D9; slice 3).
//
// It copies the skills Select kept into a content-addressed directory under
// <workspace>/.yolo/tool-skills/ and points `current` at it:
//
//	.yolo/tool-skills/
//	  current                    one line: the digest of the snapshot to read
//	  <digest>/index.json        {version, token, digest, skills: [{tool, version, name}]}
//	  <digest>/skills/<name>/…   the copied bytes: regular files and directories, never a link
//	  .lock                      the default lock file (the caller may name another)
//
// THE COPY IS CONFINED. Everything mise says is untrusted (a tool author chooses a skill's path
// and the bytes under it), so a skill's path must lie under mise's installs directory, and the
// walk under it is a chain of O_NOFOLLOW opens relative to a directory descriptor, with the
// workspace layer's confinedTree (internal/jailcontent/workspaceskills.go) as the precedent: files
// open O_NONBLOCK and are checked regular AFTER the open, so a FIFO or device swapped in cannot
// block or be read, and a link swapped in after the classification fails the open. The first two
// components under the installs directory (tool, version) may be links, because mise itself makes
// installs/<tool>/<alias> a link into the store; nothing below them may be followed. A link INSIDE
// a skill that resolves inside that skill is copied as the file or directory it names; one that
// leaves it, is absolute, loops or cycles makes that skill skipped and named.
//
// EVERY WRITE IS RELATIVE TO A DESCRIPTOR for .yolo/tool-skills, which is opened without
// following a link, so a repository that plants a link at .yolo or .yolo/tool-skills is refused
// and nothing lands outside.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// The workspace layer's caps (internal/jailcontent/workspaceskills.go), applied here to the whole
// snapshot (MS-D4).
const (
	DefaultMaxBytes   = 32 << 20
	DefaultMaxEntries = 4096
	maxLinkHops       = 40
	maxWalkDepth      = 64
	// SnapshotDirName is the directory under <workspace>/.yolo the snapshot lives in.
	SnapshotDirName = "tool-skills"
	indexVersion    = 1
)

// Options says where Publish writes and what it may copy.
type Options struct {
	// Workspace is the workspace root; the snapshot is <Workspace>/.yolo/tool-skills.
	Workspace string
	// InstallsDir is mise's installs directory ($MISE_DATA_DIR/installs). A skill whose path is
	// not under it is skipped.
	InstallsDir string
	// Token is the snapshot token written into the index (MS-D9).
	Token string
	// LockPath is the lock file serializing publishers; empty means <snapshot dir>/.lock. The
	// parent directory is created when absent.
	LockPath string
	// MaxBytes and MaxEntries cap the whole snapshot; zero means the defaults.
	MaxBytes   int64
	MaxEntries int
}

// SkippedSkill is a skill Publish did not deliver, with the reason (sanitized, fit to print).
type SkippedSkill struct {
	Skill  Skill
	Reason string
}

// Snapshot is what Publish published.
type Snapshot struct {
	Digest  string
	Skills  []Skill // delivered, in the order given
	Skipped []SkippedSkill
}

// Test hooks, nil in production: before a file's bytes are opened, and just before `current`
// is replaced.
var (
	testHookBeforeRead    func(path string)
	testHookBeforeCurrent func(digestDir string)
)

type indexSkill struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

type index struct {
	Version int          `json:"version"`
	Token   string       `json:"token"`
	Digest  string       `json:"digest"`
	Skills  []indexSkill `json:"skills"`
}

var errCap = errors.New("the snapshot's size cap is reached")

// Publish copies skills (already selected, in delivery order) and publishes them. A skill it
// cannot copy is skipped and named; the others continue. It returns an error only when nothing
// could be published, in which case `current` is untouched (MS-D6: the previous snapshot stays).
// An empty skills slice publishes an empty snapshot.
func Publish(opts Options, skills []Skill) (Snapshot, error) {
	if opts.Workspace == "" || opts.InstallsDir == "" {
		return Snapshot{}, errors.New("no workspace or installs directory")
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultMaxEntries
	}
	wsFD, err := unix.Open(opts.Workspace, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("cannot open the workspace: %w", err)
	}
	defer unix.Close(wsFD)
	yoloFD, err := ensureDirAt(wsFD, ".yolo")
	if err != nil {
		return Snapshot{}, fmt.Errorf(".yolo: %w", err)
	}
	defer unix.Close(yoloFD)
	snapFD, err := ensureDirAt(yoloFD, SnapshotDirName)
	if err != nil {
		return Snapshot{}, fmt.Errorf(".yolo/%s: %w", SnapshotDirName, err)
	}
	defer unix.Close(snapFD)

	unlock, err := lockFile(snapFD, opts.LockPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("cannot take the snapshot lock: %w", err)
	}
	defer unlock()

	removeStale(snapFD)
	tmp := ".tmp-" + randHex(8)
	if err := unix.Mkdirat(snapFD, tmp, 0o755); err != nil {
		return Snapshot{}, fmt.Errorf("cannot make scratch: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			removeTree(snapFD, tmp)
		}
	}()
	tmpFD, err := openDirAt(snapFD, tmp, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer unix.Close(tmpFD)
	if err := unix.Mkdirat(tmpFD, "skills", 0o755); err != nil {
		return Snapshot{}, err
	}
	skillsFD, err := openDirAt(tmpFD, "skills", true)
	if err != nil {
		return Snapshot{}, err
	}
	defer unix.Close(skillsFD)

	c := &copier{opts: opts, installs: installsRoots(opts.InstallsDir)}
	var snap Snapshot
	total := sha256.New()
	seen := map[string]bool{}
	for _, s := range skills {
		skip := func(reason string) {
			snap.Skipped = append(snap.Skipped, SkippedSkill{Skill: sanitized(s), Reason: sanitizeTo(reason, 200)})
		}
		if !ValidName(s.Name) {
			skip("not a usable skill name")
			continue
		}
		if seen[s.Name] {
			skip("another skill of that name is already in this snapshot")
			continue
		}
		mark := c.mark()
		rec, err := c.copySkill(skillsFD, s)
		if err != nil {
			removeTree(skillsFD, s.Name)
			c.reset(mark)
			skip(err.Error())
			continue
		}
		seen[s.Name] = true
		fmt.Fprintf(total, "skill\x00%s\x00%s\x00%s\n", s.Tool, s.Version, s.Name)
		total.Write(rec)
		snap.Skills = append(snap.Skills, s)
	}
	snap.Digest = hex.EncodeToString(total.Sum(nil))

	idx := index{Version: indexVersion, Token: opts.Token, Digest: snap.Digest, Skills: []indexSkill{}}
	for _, s := range snap.Skills {
		idx.Skills = append(idx.Skills, indexSkill{Tool: s.Tool, Version: s.Version, Name: s.Name})
	}
	body, _ := json.MarshalIndent(idx, "", "  ")
	body = append(body, '\n')

	if _, err := statAt(snapFD, snap.Digest); err == nil {
		// The same content is already published, complete. Only the index (the token) may differ.
		dirFD, err := openDirAt(snapFD, snap.Digest, true)
		if err != nil {
			return Snapshot{}, err
		}
		err = writeFileAt(dirFD, "index.json", body)
		unix.Close(dirFD)
		if err != nil {
			return Snapshot{}, err
		}
	} else {
		if err := writeFileAt(tmpFD, "index.json", body); err != nil {
			return Snapshot{}, err
		}
		if err := unix.Renameat(snapFD, tmp, snapFD, snap.Digest); err != nil {
			return Snapshot{}, fmt.Errorf("cannot publish the snapshot directory: %w", err)
		}
		cleanup = false
	}
	if testHookBeforeCurrent != nil {
		testHookBeforeCurrent(filepath.Join(opts.Workspace, ".yolo", SnapshotDirName, snap.Digest))
	}
	prev := readCurrent(snapFD)
	if err := writeFileAt(snapFD, "current", []byte(snap.Digest+"\n")); err != nil {
		return Snapshot{}, fmt.Errorf("cannot replace current: %w", err)
	}
	_ = unix.Fsync(snapFD)
	collect(snapFD, map[string]bool{snap.Digest: true, prev: true})
	return snap, nil
}

func sanitized(s Skill) Skill {
	s.Name, s.Tool, s.Version = Sanitize(s.Name), Sanitize(s.Tool), Sanitize(s.Version)
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ensureDirAt makes name under parent when absent and opens it WITHOUT following a link: a link
// there (a repository planting one) is refused.
func ensureDirAt(parent int, name string) (int, error) {
	if err := unix.Mkdirat(parent, name, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, err
	}
	fd, err := openDirAt(parent, name, true)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return -1, errors.New("is not a plain directory (a link or a file is there); remove it and relaunch")
		}
		return -1, err
	}
	return fd, nil
}

func openDirAt(parent int, name string, nofollow bool) (int, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NONBLOCK | unix.O_CLOEXEC
	if nofollow {
		flags |= unix.O_NOFOLLOW
	}
	for {
		fd, err := unix.Openat(parent, name, flags, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return fd, err
	}
}

func statAt(parent int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return st, err
}

// lockFile takes the exclusive lock and returns its release.
func lockFile(snapFD int, path string) (func(), error) {
	var f *os.File
	if path == "" {
		fd, err := unix.Openat(snapFD, ".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return nil, err
		}
		f = os.NewFile(uintptr(fd), ".lock")
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		var err error
		if f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err != nil {
			return nil, err
		}
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// writeFileAt writes name under dir atomically: a scratch file, then a rename.
func writeFileAt(dir int, name string, data []byte) error {
	tmp := ".w-" + randHex(6)
	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = unix.Renameat(dir, tmp, dir, name)
	}
	if err != nil {
		_ = unix.Unlinkat(dir, tmp, 0)
	}
	return err
}

func readCurrent(snapFD int) string {
	fd, err := unix.Openat(snapFD, "current", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return ""
	}
	f := os.NewFile(uintptr(fd), "current")
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 256))
	return strings.TrimSpace(string(b))
}

func listNames(dirFD int) []string {
	dup, err := unix.Dup(dirFD)
	if err != nil {
		return nil
	}
	f := os.NewFile(uintptr(dup), "dir")
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	sort.Strings(names)
	return names
}

// removeTree removes name under parent and everything below it, never following a link.
func removeTree(parent int, name string) {
	if st, err := statAt(parent, name); err != nil {
		return
	} else if st.Mode&unix.S_IFMT == unix.S_IFDIR {
		if fd, err := openDirAt(parent, name, true); err == nil {
			for _, n := range listNames(fd) {
				removeTree(fd, n)
			}
			unix.Close(fd)
		}
		_ = unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
		return
	}
	_ = unix.Unlinkat(parent, name, 0)
}

// removeStale clears scratch a crashed publisher left; only the lock holder makes scratch.
func removeStale(snapFD int) {
	for _, n := range listNames(snapFD) {
		if strings.HasPrefix(n, ".tmp-") || strings.HasPrefix(n, ".w-") {
			removeTree(snapFD, n)
		}
	}
}

func isDigestName(n string) bool {
	if len(n) != 64 {
		return false
	}
	_, err := hex.DecodeString(n)
	return err == nil
}

// collect removes every digest directory but the ones in keep: the new snapshot and the one
// `current` named a moment ago, which a reader that read `current` earlier may still be in.
func collect(snapFD int, keep map[string]bool) {
	for _, n := range listNames(snapFD) {
		if isDigestName(n) && !keep[n] {
			removeTree(snapFD, n)
		}
	}
}

// --- the confined copy --------------------------------------------------------------

type copier struct {
	opts     Options
	installs []string
	bytes    int64
	entries  int
}

type mark struct {
	bytes   int64
	entries int
}

func (c *copier) mark() mark   { return mark{c.bytes, c.entries} }
func (c *copier) reset(m mark) { c.bytes, c.entries = m.bytes, m.entries }

func (c *copier) charge(entries int, n int64) error {
	if c.entries+entries > c.opts.MaxEntries || c.bytes+n > c.opts.MaxBytes {
		return errCap
	}
	c.entries += entries
	c.bytes += n
	return nil
}

// installsRoots are the spellings of the installs directory a skill path may use: as given and
// with links resolved.
func installsRoots(dir string) []string {
	out := []string{filepath.Clean(dir)}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != out[0] {
		out = append(out, real)
	}
	return out
}

func (c *copier) relUnderInstalls(path string) ([]string, bool) {
	p := filepath.Clean(path)
	for _, root := range c.installs {
		if p == root {
			return nil, false
		}
		if prefix := strings.TrimSuffix(root, "/") + "/"; strings.HasPrefix(p, prefix) {
			return strings.Split(p[len(prefix):], "/"), true
		}
	}
	return nil, false
}

// copySkill copies s into skillsFD/<name> and returns its digest record.
func (c *copier) copySkill(skillsFD int, s Skill) ([]byte, error) {
	if !filepath.IsAbs(s.Path) {
		return nil, errors.New("its path is not absolute")
	}
	comps, ok := c.relUnderInstalls(s.Path)
	if !ok {
		return nil, errors.New("its path is outside mise's installs directory")
	}
	rootFD, err := unix.Open(c.installs[len(c.installs)-1], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open mise's installs directory: %w", err)
	}
	cur := rootFD
	for i, comp := range comps {
		// tool and version may be links (mise's own aliases into the store); nothing below follows.
		fd, err := openDirAt(cur, comp, i >= 2)
		unix.Close(cur)
		if err != nil {
			return nil, openErr("its directory", err)
		}
		cur = fd
	}
	srcFD := cur
	defer unix.Close(srcFD)

	if err := c.charge(1, 0); err != nil {
		return nil, err
	}
	if err := unix.Mkdirat(skillsFD, s.Name, 0o755); err != nil {
		return nil, fmt.Errorf("cannot make its directory: %w", err)
	}
	dstFD, err := openDirAt(skillsFD, s.Name, true)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dstFD)
	var rec bytes.Buffer
	w := &walker{c: c, rootFD: srcFD, display: s.Path, rec: &rec}
	if err := w.dir(srcFD, dstFD, nil, "", 0); err != nil {
		return nil, err
	}
	return rec.Bytes(), nil
}

func openErr(what string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return fmt.Errorf("%s no longer exists", what)
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("%s is a link or not a directory", what)
	}
	return fmt.Errorf("%s cannot be read: %v", what, err)
}

type walker struct {
	c       *copier
	rootFD  int // the skill's directory: every link is resolved inside it
	display string
	rec     *bytes.Buffer
}

// dir copies the directory at srcFD, whose root-relative real position is srcRel, into dstFD.
func (w *walker) dir(srcFD, dstFD int, srcRel []string, dstRel string, depth int) error {
	if depth > maxWalkDepth {
		return errors.New("is nested too deeply")
	}
	for _, name := range listNames(srcFD) {
		st, err := statAt(srcFD, name)
		if err != nil {
			return openErr("an entry", err)
		}
		if err := w.entry(srcFD, dstFD, name, st, srcRel, join(dstRel, name), depth); err != nil {
			return err
		}
	}
	return nil
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}

func (w *walker) entry(srcFD, dstFD int, name string, st unix.Stat_t, srcRel []string, dstRel string, depth int) error {
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return w.file(srcFD, name, dstFD, name, dstRel, srcRel)
	case unix.S_IFDIR:
		return w.subdir(srcFD, name, dstFD, name, dstRel, append(append([]string{}, srcRel...), name), depth)
	case unix.S_IFLNK:
		return w.link(srcFD, name, dstFD, name, dstRel, srcRel, depth)
	}
	return fmt.Errorf("%s is not a regular file or directory", Sanitize(dstRel))
}

func (w *walker) subdir(srcParent int, srcName string, dstFD int, dstName, dstRel string, srcRel []string, depth int) error {
	if err := w.c.charge(1, 0); err != nil {
		return err
	}
	fd, err := openDirAt(srcParent, srcName, true)
	if err != nil {
		return openErr(Sanitize(dstRel), err)
	}
	defer unix.Close(fd)
	if err := unix.Mkdirat(dstFD, dstName, 0o755); err != nil {
		return err
	}
	nd, err := openDirAt(dstFD, dstName, true)
	if err != nil {
		return err
	}
	defer unix.Close(nd)
	fmt.Fprintf(w.rec, "d\x00%s\n", dstRel)
	return w.dir(fd, nd, srcRel, dstRel, depth+1)
}

func (w *walker) file(srcParent int, srcName string, dstParent int, dstName, dstRel string, _ []string) error {
	if testHookBeforeRead != nil {
		testHookBeforeRead(filepath.Join(w.display, filepath.FromSlash(dstRel)))
	}
	fd, err := unix.Openat(srcParent, srcName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("%s changed while it was read (a link is there now)", Sanitize(dstRel))
		}
		return openErr(Sanitize(dstRel), err)
	}
	f := os.NewFile(uintptr(fd), srcName)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s is not a regular file", Sanitize(dstRel))
	}
	if err := w.c.charge(1, 0); err != nil {
		return err
	}
	perm := uint32(0o644)
	if st.Mode&0o111 != 0 {
		perm = 0o755
	}
	ofd, err := unix.Openat(dstParent, dstName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, perm)
	if err != nil {
		return err
	}
	out := os.NewFile(uintptr(ofd), dstName)
	defer out.Close()
	// A mode set by openat is cut by the umask; the digest records the intent, so set it.
	_ = out.Chmod(os.FileMode(perm))
	h := sha256.New()
	room := w.c.opts.MaxBytes - w.c.bytes
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, room+1))
	if err != nil {
		return fmt.Errorf("%s cannot be read: %v", Sanitize(dstRel), err)
	}
	if n > room {
		return errCap
	}
	if err := w.c.charge(0, n); err != nil {
		return err
	}
	fmt.Fprintf(w.rec, "f\x00%s\x00%o\x00%s\x00%d\n", dstRel, perm, hex.EncodeToString(h.Sum(nil)), n)
	return nil
}

// link copies what a link names, when that stays inside the skill.
func (w *walker) link(srcFD int, name string, dstFD int, dstName, dstRel string, srcRel []string, depth int) error {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(srcFD, name, buf)
	if err != nil {
		return openErr(Sanitize(dstRel), err)
	}
	target := string(buf[:n])
	if strings.HasPrefix(target, "/") {
		return fmt.Errorf("%s is a link to an absolute path", Sanitize(dstRel))
	}
	real, err := w.resolve(append(append([]string{}, srcRel...), splitParts(target)...), dstRel)
	if err != nil {
		return err
	}
	if len(real) == 0 {
		return fmt.Errorf("%s is a link to the skill's own directory", Sanitize(dstRel))
	}
	parent, base := real[:len(real)-1], real[len(real)-1]
	pfd, err := w.openPath(parent)
	if err != nil {
		return err
	}
	defer unix.Close(pfd)
	st, err := statAt(pfd, base)
	if err != nil {
		return openErr(Sanitize(dstRel), err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return w.file(pfd, base, dstFD, dstName, dstRel, real)
	case unix.S_IFDIR:
		// A link to a directory that contains the link itself is a cycle.
		if isPrefix(real, srcRel) {
			return fmt.Errorf("%s is a link cycle", Sanitize(dstRel))
		}
		return w.subdir(pfd, base, dstFD, dstName, dstRel, real, depth)
	}
	return fmt.Errorf("%s is a link to something that is not a regular file or directory", Sanitize(dstRel))
}

func isPrefix(prefix, of []string) bool {
	if len(prefix) > len(of) {
		return false
	}
	for i := range prefix {
		if prefix[i] != of[i] {
			return false
		}
	}
	return true
}

func splitParts(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// resolve walks pending (root-relative, a link's target appended to its directory) through the
// skill, component by component: a link met on the way is expanded lexically, `..` above the root
// escapes, and more than maxLinkHops links is a loop. It returns the real root-relative parts.
func (w *walker) resolve(pending []string, shown string) ([]string, error) {
	var cur []string
	hops := 0
	for len(pending) > 0 {
		p := pending[0]
		pending = pending[1:]
		switch p {
		case ".":
			continue
		case "..":
			if len(cur) == 0 {
				return nil, fmt.Errorf("%s is a link that leaves the skill", Sanitize(shown))
			}
			cur = cur[:len(cur)-1]
			continue
		}
		fd, err := w.openPath(cur)
		if err != nil {
			return nil, err
		}
		st, err := statAt(fd, p)
		unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("%s is a link to something that is not there", Sanitize(shown))
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			cur = append(cur, p)
			continue
		}
		hops++
		if hops > maxLinkHops {
			return nil, fmt.Errorf("%s is a link loop", Sanitize(shown))
		}
		fd, err = w.openPath(cur)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, 4096)
		n, err := unix.Readlinkat(fd, p, buf)
		unix.Close(fd)
		if err != nil {
			return nil, openErr(Sanitize(shown), err)
		}
		t := string(buf[:n])
		if strings.HasPrefix(t, "/") {
			return nil, fmt.Errorf("%s is a link to an absolute path", Sanitize(shown))
		}
		pending = append(splitParts(t), pending...)
	}
	return cur, nil
}

// openPath opens the directory parts under the skill's root, following no link.
func (w *walker) openPath(parts []string) (int, error) {
	cur, err := unix.Dup(w.rootFD)
	if err != nil {
		return -1, err
	}
	for _, p := range parts {
		fd, err := openDirAt(cur, p, true)
		unix.Close(cur)
		if err != nil {
			return -1, openErr(Sanitize(p), err)
		}
		cur = fd
	}
	return cur, nil
}

// --- the refresh and the provisioning step ------------------------------------------

// Report is what Refresh did. Ran is false when it did nothing at all (an empty allowlist).
type Report struct {
	Ran      bool
	List     Result
	Selected Selection
	// Snapshot is the published snapshot; nil when the previous one was kept.
	Snapshot *Snapshot
	// Err is why the previous snapshot was kept (a failed listing or a failed write).
	Err error
}

// Refresh lists the skills with r, selects the allowlisted ones and publishes them. With an
// empty allowlist it runs nothing and writes nothing. A failed listing or write keeps the
// previous snapshot (MS-D6); a listing that succeeds with no allowlisted skill publishes an
// empty snapshot, so a removed tool's skills drop.
func Refresh(ctx context.Context, opts Options, allowlist []string, r Runner) Report {
	if len(allowlist) == 0 {
		return Report{}
	}
	rep := Report{Ran: true}
	rep.List = r.List(ctx, opts.Workspace)
	if rep.List.Outcome != OutcomeOK {
		rep.Err = fmt.Errorf("%s", firstNonEmpty(rep.List.Reason, rep.List.Outcome.String()))
		return rep
	}
	rep.Selected = Select(rep.List.Skills, allowlist)
	snap, err := Publish(opts, rep.Selected.Skills)
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Snapshot = &snap
	return rep
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// RunStep is the provisioning step behind `yolo internal tool-skills <workspace> [lock-file]`.
// It reads YOLO_MISE_SKILL_TOOLS (a JSON array), YOLO_TOOL_SKILLS_TOKEN and MISE_DATA_DIR from
// getenv, refreshes, and ALWAYS returns 0: the session never fails or waits on it beyond the
// runner's bounds. An empty allowlist runs nothing and prints nothing. A failure prints one line.
// Disclosure of what changed is slice 5 and not here.
func RunStep(ctx context.Context, getenv func(string) string, args []string, r Runner, out io.Writer) int {
	raw := strings.TrimSpace(getenv("YOLO_MISE_SKILL_TOOLS"))
	if raw == "" {
		return 0
	}
	var allowlist []string
	if err := json.Unmarshal([]byte(raw), &allowlist); err != nil {
		fmt.Fprintln(out, "yolo: mise skills: the tool list this session was given is not readable; the last snapshot stays. Relaunch yolo to resend it.")
		return 0
	}
	if len(allowlist) == 0 {
		return 0
	}
	if len(args) < 1 || args[0] == "" {
		fmt.Fprintln(out, "yolo: mise skills: no workspace was named; the last snapshot stays.")
		return 0
	}
	token := getenv("YOLO_TOOL_SKILLS_TOKEN")
	if token == "" {
		fmt.Fprintln(out, "yolo: mise skills: this launch sent no snapshot token; the last snapshot stays. Relaunch yolo to resend it.")
		return 0
	}
	data := getenv("MISE_DATA_DIR")
	if data == "" {
		data = "/mise"
	}
	opts := Options{Workspace: args[0], InstallsDir: filepath.Join(data, "installs"), Token: token}
	if len(args) > 1 {
		opts.LockPath = args[1]
	}
	rep := Refresh(ctx, opts, allowlist, r)
	if rep.Err != nil {
		fmt.Fprintf(out, "yolo: mise skills: could not refresh (%s); the last snapshot stays. Run `mise skills ls` in the workspace to see why.\n",
			sanitizeTo(strings.ReplaceAll(rep.Err.Error(), "\n", " "), 200))
	}
	return 0
}
