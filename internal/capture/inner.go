// inner.go is the CAPTURE DRIVER — the process that actually runs a vendor installer and
// turns what it left behind into a tree the store can admit. It is `yolo internal
// capture-run`, and it is the reason install-capture needs an inner half at all.
//
// # Why this runs INSIDE
//
// The obvious design is a host-side before/after diff of the per-workspace bind dirs, and it
// cannot work: the boot writes into those same surfaces before any installer does (bootstrap
// npm packages, the config-gated LSP servers, the generated `yolo-bin` scripts). A host that
// snapshotted before launch would file yolo's own output as the vendor's, and one that
// snapshotted after launch would have to know when the boot finished. So the baseline is
// walked HERE, after boot and immediately before the installer runs, which is the only moment
// at which "everything present is not the installer's" is true.
//
// That also makes the driver backend-neutral in the strongest sense: it is a process with a
// HOME, a scratch directory and an argv. It knows nothing about podman, Apple Container or
// Seatbelt, which matters most for Apple Container — that backend has no per-directory binds
// at all (`appleContainerBaseMounts` puts the whole workspace state at /home/agent in ONE rw
// bind), so there is no bind whose contents could BE the delta. The baseline walk is what
// makes that backend work, and it costs the other two nothing.
//
// # Why the out path is an argument
//
// It is passed in, never read from the environment, for the reason `entrypoint.receiptsFile`
// spells out: YOLO_WORKSPACE is a HOST-side launcher input that does not exist inside a live
// container, and macos-user execs under `env -i`. HOME is the deliberate exception — a
// process's own home is exactly the thing that IS set in all three places, and "a process
// with a HOME" is the whole contract.
package capture

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Options configures one capture run.
type Options struct {
	// Home is the HOME whose surfaces are captured, and the HOME the installer runs
	// under. On the container backends this is /home/agent; on macos-user it is the
	// throwaway staging home, which is why Manifest.Home records it.
	Home string
	// Out is the ENTRY-SHAPED scratch directory the capture is built in: this call fills
	// TreeDir(Out) and writes ManifestPath(Out). It is what Store.Stage returns, so the
	// admit that follows is a rename inside the store.
	Out string
	// Command is the installer argv, run verbatim. A non-zero exit is a failed capture:
	// half an install is worse than none, and the store must never be handed one.
	Command []string
	// Env is the installer's environment. Nil inherits this process's, with HOME forced
	// to Home — the common case, since the driver is already running in the jail whose
	// home it is capturing.
	Env []string
	// Surfaces are the home-relative roots to walk. Nil is paths.HomeSurfaces(), which is
	// what every caller wants; the field exists so a test can prove the surface set is
	// what makes the delta what it is.
	Surfaces []paths.HomeSurface
	// Stdout and Stderr receive the installer's output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Result reports what one capture produced.
type Result struct {
	// Manifest is the delta manifest, already written to ManifestPath(Out).
	Manifest *Manifest
	// Tree is TreeDir(Out) — the argument Store.Admit takes.
	Tree string
	// Renamed and Copied count the TOP-LEVEL delta paths moved into the tree (a whole new
	// directory is one of them, not one per file inside it).
	//
	// Copied is the number that were not on the same MOUNT as the tree and had to be
	// duplicated instead. It is reported rather than swallowed because a copy is the
	// exact cost this subsystem exists to delete — 1.2 GB of it for claude, measured
	// 2026-09-03 — and a caller that sees it can move its scratch dir instead of paying
	// it on every capture forever.
	Renamed int
	Copied  int
}

// Run performs one capture: walk a baseline of the surfaces, run the installer, move
// everything it added or changed into TreeDir(Out), and write the manifest beside it.
//
// The delta is MOVED, not copied — the capture home is throwaway, and a copy of a
// gigabyte-scale install is the cost the store exists to avoid. See Result.Copied for the
// one case where it cannot be.
func Run(opts Options) (*Result, error) {
	d, err := newDriver(opts)
	if err != nil {
		return nil, err
	}
	baseline, err := d.walk()
	if err != nil {
		return nil, fmt.Errorf("capture baseline: %w", err)
	}
	if err := d.install(); err != nil {
		return nil, err
	}
	res := &Result{Tree: d.tree}
	if err := d.moveDelta(baseline, res); err != nil {
		return nil, fmt.Errorf("capture delta: %w", err)
	}
	entries, refs, err := describeTree(d.tree, d.home)
	if err != nil {
		return nil, fmt.Errorf("capture manifest: %w", err)
	}
	m := &Manifest{
		Schema:       ManifestSchema,
		Home:         d.home,
		Surfaces:     d.surfaceRels(),
		Entries:      entries,
		AbsoluteRefs: refs,
	}
	if m.Entries == nil {
		m.Entries = []ManifestEntry{}
	}
	if m.AbsoluteRefs == nil {
		m.AbsoluteRefs = []AbsoluteRef{}
	}
	if err := WriteManifest(d.opts.Out, m); err != nil {
		return nil, err
	}
	res.Manifest = m
	if res.Copied > 0 && d.opts.Stderr != nil {
		fmt.Fprintf(d.opts.Stderr, "yolo capture: %d of %d delta paths had to be COPIED into %s "+
			"rather than renamed — the scratch dir is on a different MOUNT from the capture "+
			"surfaces, so every capture pays for the bytes twice\n",
			res.Copied, res.Copied+res.Renamed, d.tree)
	}
	return res, nil
}

// driver is one capture's resolved inputs.
type driver struct {
	opts     Options
	home     string
	tree     string
	surfaces []paths.HomeSurface
}

func newDriver(opts Options) (*driver, error) {
	if opts.Home == "" {
		return nil, errors.New("capture: no HOME to capture (pass --home, or run with HOME set)")
	}
	if !filepath.IsAbs(opts.Home) {
		return nil, fmt.Errorf("capture: home %q must be an absolute path", opts.Home)
	}
	if fi, err := os.Stat(opts.Home); err != nil {
		return nil, fmt.Errorf("capture: home %s: %w", opts.Home, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("capture: home %s is not a directory", opts.Home)
	}
	if opts.Out == "" {
		return nil, errors.New("capture: no output directory (pass --out)")
	}
	if !filepath.IsAbs(opts.Out) {
		return nil, fmt.Errorf("capture: out %q must be an absolute path", opts.Out)
	}
	if len(opts.Command) == 0 {
		return nil, errors.New("capture: no installer command (pass it after `--`)")
	}
	d := &driver{
		opts:     opts,
		home:     filepath.Clean(opts.Home),
		tree:     TreeDir(opts.Out),
		surfaces: opts.Surfaces,
	}
	if d.surfaces == nil {
		d.surfaces = paths.HomeSurfaces()
	}
	// The scratch dir inside a capture surface would capture ITSELF, growing without
	// bound and filing yolo's own scratch as the vendor's install. Refused rather than
	// filtered: an out dir elsewhere is always available, and a filter would be a rule a
	// reader of the manifest could not see.
	for _, s := range d.surfaces {
		root := filepath.Join(d.home, filepath.FromSlash(s.HomeRel))
		if within(root, filepath.Clean(opts.Out)) {
			return nil, fmt.Errorf("capture: out dir %s is inside the capture surface %s — "+
				"it would capture itself", opts.Out, root)
		}
	}
	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		return nil, err
	}
	if ents, err := os.ReadDir(d.tree); err == nil && len(ents) > 0 {
		return nil, fmt.Errorf("capture: %s is not empty — a capture is assembled in a FRESH "+
			"scratch dir (Store.Stage), or two runs' output merge into one entry", d.tree)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(d.tree, 0o755); err != nil {
		return nil, err
	}
	return d, nil
}

// surfaceRels is the home-relative spelling of the walked surfaces, in walk order.
func (d *driver) surfaceRels() []string {
	out := make([]string, 0, len(d.surfaces))
	for _, s := range d.surfaces {
		out = append(out, s.HomeRel)
	}
	return out
}

// node is what the baseline remembers about one path: enough to say "the installer touched
// this" without reading a byte of it.
//
// Content is deliberately NOT hashed. The baseline is walked while a human waits, over a home
// the boot has already filled, and hashing it would make the capture pay for the bytes it is
// trying not to copy. The cost is one blind spot, stated rather than hidden: an installer that
// rewrites an existing file to the SAME size and the SAME mode within the filesystem's mtime
// granularity is invisible to the delta. Installers write new paths; that is what makes this
// trade sound rather than merely cheap.
type node struct {
	kind  string
	perm  fs.FileMode
	size  int64
	mtime int64
}

type snapshot map[string]node

// walk records the current state of every capture surface, keyed by home-relative
// slash-separated path (the surface root included, so a surface the installer CREATES is
// itself detectable).
func (d *driver) walk() (snapshot, error) {
	snap := snapshot{}
	for _, s := range d.surfaces {
		root := filepath.Join(d.home, filepath.FromSlash(s.HomeRel))
		if _, err := os.Lstat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(d.home, p)
			if rerr != nil {
				return rerr
			}
			info, ierr := de.Info()
			if ierr != nil {
				return ierr
			}
			snap[filepath.ToSlash(rel)] = node{
				kind:  kindOf(de.Type()),
				perm:  info.Mode().Perm(),
				size:  info.Size(),
				mtime: info.ModTime().UnixNano(),
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return snap, nil
}

// install runs the installer under the capture HOME.
func (d *driver) install() error {
	cmd := exec.Command(d.opts.Command[0], d.opts.Command[1:]...) //nolint:gosec // the argv IS the input
	cmd.Dir = d.home
	cmd.Stdout = d.opts.Stdout
	cmd.Stderr = d.opts.Stderr
	cmd.Env = d.opts.Env
	if cmd.Env == nil {
		cmd.Env = envWithHome(os.Environ(), d.home)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("capture: installer %s failed, so nothing was captured: %w",
			d.opts.Command[0], err)
	}
	return nil
}

// envWithHome replaces HOME in an environment slice, appending it if absent.
func envWithHome(env []string, home string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home)
}

// moveDelta moves everything the installer added or changed into the tree.
//
// TOP-DOWN, and a directory absent from the baseline is moved WHOLE rather than descended
// into: everything beneath a new directory is new by construction, so one rename moves a
// version directory of thousands of files. That is the difference between a capture that
// costs a syscall and one that costs a walk of 1.2 GB.
//
// A surface ROOT is never moved whole even when it is new. On the container backends those
// roots are bind MOUNTPOINTS and renaming one fails EBUSY; descending into it captures the
// same content and cannot fail that way.
func (d *driver) moveDelta(baseline snapshot, res *Result) error {
	for _, s := range d.surfaces {
		rel := filepath.ToSlash(s.HomeRel)
		if _, err := os.Lstat(filepath.Join(d.home, filepath.FromSlash(rel))); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := d.visit(rel, baseline, res, true); err != nil {
			return err
		}
	}
	return nil
}

// visit decides one path's fate: move it, descend into it, or leave it as the baseline's.
func (d *driver) visit(rel string, baseline snapshot, res *Result, isSurfaceRoot bool) error {
	src := filepath.Join(d.home, filepath.FromSlash(rel))
	info, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // the installer removed it between the walk and now
		}
		return err
	}
	now := node{
		kind:  kindOf(info.Mode().Type()),
		perm:  info.Mode().Perm(),
		size:  info.Size(),
		mtime: info.ModTime().UnixNano(),
	}
	was, had := baseline[rel]
	if now.kind == KindDir {
		if (!had || was.kind != KindDir) && !isSurfaceRoot {
			return d.move(rel, res)
		}
		ents, rerr := os.ReadDir(src)
		if rerr != nil {
			return rerr
		}
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			if verr := d.visit(path.Join(rel, n), baseline, res, false); verr != nil {
				return verr
			}
		}
		return nil
	}
	if had && was == now {
		return nil
	}
	return d.move(rel, res)
}

// move relocates one delta path into the tree, creating the ancestor directories it needs.
func (d *driver) move(rel string, res *Result) error {
	if err := d.ensureParents(rel); err != nil {
		return err
	}
	src := filepath.Join(d.home, filepath.FromSlash(rel))
	dst := filepath.Join(d.tree, filepath.FromSlash(rel))
	err := os.Rename(src, dst)
	if err == nil {
		res.Renamed++
		return nil
	}
	if !isCrossDevice(err) {
		return err
	}
	// Not the same MOUNT. Measured 2026-09-04 in this jail: two bind mounts of ONE btrfs
	// (identical st_dev) still fail rename with EXDEV, because the kernel compares the
	// MOUNT, not the device — so "same filesystem" is not the predicate, and st_dev is not
	// a usable pre-check. Copying is the only way through; Result.Copied is how the caller
	// learns it happened.
	if cerr := copyTree(src, dst); cerr != nil {
		return cerr
	}
	if rerr := os.RemoveAll(src); rerr != nil {
		return rerr
	}
	res.Copied++
	return nil
}

// ensureParents creates rel's ancestor directories inside the tree, each with the mode of the
// directory it mirrors in the home, so the captured tree is the home's shape and not 0755
// everywhere.
func (d *driver) ensureParents(rel string) error {
	parts := strings.Split(path.Dir(rel), "/")
	cur := ""
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = path.Join(cur, p)
		dst := filepath.Join(d.tree, filepath.FromSlash(cur))
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		perm := fs.FileMode(0o755)
		if info, err := os.Lstat(filepath.Join(d.home, filepath.FromSlash(cur))); err == nil {
			perm = info.Mode().Perm()
		}
		if err := os.Mkdir(dst, perm); err != nil && !os.IsExist(err) {
			return err
		}
		// Mkdir's mode is masked by umask; the tree records the home's shape, not the
		// capture process's umask.
		if err := os.Chmod(dst, perm); err != nil {
			return err
		}
	}
	return nil
}

// copyTree duplicates src at dst, preserving directories, permission bits and SYMLINKS AS
// SYMLINKS (readlink + symlink, never followed — a captured install's links point at its own
// paths, and resolving them would inline whatever else is on the machine).
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, rerr := os.Readlink(src)
		if rerr != nil {
			return rerr
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
			return err
		}
		ents, rerr := os.ReadDir(src)
		if rerr != nil {
			return rerr
		}
		for _, e := range ents {
			if cerr := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); cerr != nil {
				return cerr
			}
		}
		return nil
	case !info.Mode().IsRegular():
		// A socket or fifo an installer left behind is not part of a package. Skipping
		// it is honest; recreating it would be a guess about what it was for.
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}

// isCrossDevice reports whether a rename failed because source and destination are on
// different MOUNTS (EXDEV) — which, on Linux, includes two bind mounts of one filesystem.
func isCrossDevice(err error) bool {
	var le *os.LinkError
	if errors.As(err, &le) {
		return errors.Is(le.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}

// kindOf maps a file mode's type bits to a manifest kind.
func kindOf(t fs.FileMode) string {
	switch {
	case t&fs.ModeSymlink != 0:
		return KindSymlink
	case t.IsDir():
		return KindDir
	default:
		return KindFile
	}
}
