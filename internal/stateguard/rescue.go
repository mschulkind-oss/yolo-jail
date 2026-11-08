package stateguard

// rescue.go is the RESCUE's host side (docs/design/workspace-state-safety.md §3.5, SD-D13): the
// directory a rescue writes into, the extraction of what the running jail streams out, the record
// beside it, and the listing `yolo stores` prints. The copy itself, which needs the jail's runtime,
// is the keeper's (internal/cli/run/keeperrescue.go).
//
// EVERYTHING HERE IS WRITTEN UNDER A JAIL-WRITABLE DIRECTORY, from bytes the jail chose. `.yolo` is
// bind-mounted into the jail with the workspace, so the jail can put a link at `.yolo`, at
// `rescue`, or anywhere beneath, and it decides every name and link in the stream. So every write
// goes through an os.Root opened by the state-directory openers, which refuse a link at `.yolo` and
// at `rescue`, and the extraction never follows a link out of the rescue directory.

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// RescueDirName is the rescue directory's name under `.yolo`.
const RescueDirName = "rescue"

// RescueHomeName is the directory under one rescue that mirrors the home's layout, so one copy
// restores it.
const RescueHomeName = "home"

// rescueRecordName is the record a rescue leaves beside its copy, naming the home it came from.
const rescueRecordName = "rescue.json"

// RescueRecord is what a rescue records beside its copy. HomeRel is the home's path relative to
// `.yolo` (paths.HomeStateRel): a reader rebuilds the restore target from it and checks that it
// names one of the workspace's homes, since the jail can rewrite this file.
type RescueRecord struct {
	Created time.Time `json:"created"`
	HomeRel string    `json:"home_rel"`
	// Copied are the home's subpaths copied, relative to the home; Failed those that were not,
	// each with why.
	Copied []string          `json:"copied,omitempty"`
	Failed map[string]string `json:"failed,omitempty"`
}

// Rescue is one rescue directory being written: its path on the host and the root every write
// goes through.
type Rescue struct {
	Dir  string
	root *os.Root
}

// Close lets the rescue's root go.
func (r *Rescue) Close() error { return r.root.Close() }

// stampName is a rescue's directory name: the moment it was made, in UTC, sortable.
func stampName(now time.Time) string { return now.UTC().Format("20060102T150405Z") }

// NewRescue makes a fresh rescue directory, `<workspace>/.yolo/rescue/<stamp>/`, recreating
// `.yolo` through paths.EnsureWorkspaceStateDir when it is gone. It never reuses a directory:
// a name already taken gets a numbered suffix.
func NewRescue(workspace string, now time.Time) (*Rescue, error) {
	state, err := paths.EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return nil, err
	}
	sr, err := paths.OpenStateDirRoot(state)
	if err != nil {
		return nil, err
	}
	defer sr.Close()
	if err := sr.Mkdir(RescueDirName, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	rr, err := paths.OpenStateSubdirRoot(sr, RescueDirName, filepath.Join(state, RescueDirName))
	if err != nil {
		return nil, err
	}
	defer rr.Close()
	base := stampName(now)
	for i := 0; i < 100; i++ {
		name := base
		if i > 0 {
			name = base + "-" + strconv.Itoa(i)
		}
		if err := rr.Mkdir(name, 0o700); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return nil, err
		}
		root, err := paths.OpenStateSubdirRoot(rr, name, filepath.Join(state, RescueDirName, name))
		if err != nil {
			return nil, err
		}
		if err := root.Mkdir(RescueHomeName, 0o700); err != nil {
			root.Close()
			return nil, err
		}
		return &Rescue{Dir: filepath.Join(state, RescueDirName, name), root: root}, nil
	}
	return nil, fmt.Errorf("no free rescue directory name under %s", filepath.Join(state, RescueDirName))
}

// HomeDir is the directory under the rescue that mirrors the home.
func (r *Rescue) HomeDir() string { return filepath.Join(r.Dir, RescueHomeName) }

// WriteRecord writes the rescue's record beside its copy.
func (r *Rescue) WriteRecord(rec RescueRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	f, err := r.root.OpenFile(rescueRecordName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Extract writes a tar stream into the rescue, under `home/<target>`. Each member's leading
// component must be strip (the name the stream was made from, `tar -C <parent> <strip>`), which is
// replaced by target, the home-relative path the copy restores to; a member that is not under strip
// is skipped. Directories, regular files, symbolic links and hard links are written, as themselves
// and never followed out of the rescue; device nodes and fifos are skipped. Permission bits are
// kept, without setuid, setgid or sticky. It returns the bytes written.
func (r *Rescue) Extract(stream io.Reader, strip, target string) (int64, error) {
	target = path.Clean(filepath.ToSlash(target))
	if target != "." && !fs.ValidPath(target) {
		return 0, fmt.Errorf("rescue target %q is not a relative path", target)
	}
	dest := func(name string) (string, bool) {
		name = strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(name, "./")), "/")
		rest, ok := strings.CutPrefix(name, strip)
		if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
			return "", false
		}
		out := path.Join(RescueHomeName, target, strings.TrimPrefix(rest, "/"))
		if !fs.ValidPath(out) {
			return "", false
		}
		return out, true
	}
	if t, ok := dest(strip); ok && path.Dir(t) != "." {
		if err := r.root.MkdirAll(path.Dir(t), 0o700); err != nil {
			return 0, err
		}
	}
	var written int64
	type dirTime struct {
		name string
		mode fs.FileMode
		mt   time.Time
	}
	var dirs []dirTime
	tr := tar.NewReader(stream)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return written, err
		}
		name, ok := dest(h.Name)
		if !ok {
			continue
		}
		mode := fs.FileMode(h.Mode) & fs.ModePerm
		switch h.Typeflag {
		case tar.TypeDir:
			if err := r.root.MkdirAll(name, 0o700); err != nil {
				return written, err
			}
			dirs = append(dirs, dirTime{name, mode, h.ModTime})
		case tar.TypeReg:
			if err := r.root.MkdirAll(path.Dir(name), 0o700); err != nil {
				return written, err
			}
			// Remove first, so a link the stream put at this name earlier is replaced rather than
			// written through.
			_ = r.root.Remove(name)
			f, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return written, err
			}
			n, cerr := io.Copy(f, tr)
			written += n
			if err := f.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return written, cerr
			}
			_ = r.root.Chmod(name, mode)
			_ = r.root.Chtimes(name, h.ModTime, h.ModTime)
		case tar.TypeSymlink:
			if err := r.root.MkdirAll(path.Dir(name), 0o700); err != nil {
				return written, err
			}
			_ = r.root.Remove(name)
			// The link is written as the jail made it, and never resolved here: os.Root refuses
			// any later write through it that would leave the rescue.
			if err := r.root.Symlink(h.Linkname, name); err != nil {
				return written, err
			}
		case tar.TypeLink:
			old, ok := dest(h.Linkname)
			if !ok {
				continue
			}
			if err := r.root.MkdirAll(path.Dir(name), 0o700); err != nil {
				return written, err
			}
			_ = r.root.Remove(name)
			if err := r.root.Link(old, name); err != nil {
				return written, err
			}
		}
	}
	// Directory modes and times last, deepest first, so a read-only directory does not refuse
	// the files written into it.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].name) > len(dirs[j].name) })
	for _, d := range dirs {
		_ = r.root.Chmod(d.name, d.mode|0o700)
		_ = r.root.Chtimes(d.name, d.mt, d.mt)
	}
	return written, nil
}

// RestoreCommand is the command that puts a rescue's copy back into home, to be run with no jail
// running in the workspace: a copy of the rescue's home over it, which works whether the home is
// gone or a later launch has made a fresh one. A move cannot merge into a home that exists.
func RestoreCommand(rescueDir, home string) string {
	return shquote.Join([]string{"cp", "-a", filepath.Join(rescueDir, RescueHomeName) + "/.", home})
}

// Listed is one rescue `yolo stores` lists.
type Listed struct {
	// Dir is the rescue's directory on the host.
	Dir string
	// Bytes is its size, and Err why it could not be measured.
	Bytes int64
	Err   error
	// Restore is the command that restores it, "" when its record does not name one of the
	// workspace's homes.
	Restore string
	Created time.Time
}

// List lists workspace's rescues, oldest first. It reads only through roots that refuse a link at
// `.yolo`, at `rescue` and at each rescue, so a jail cannot have it list or size another host
// directory. A workspace with none lists nothing.
func List(workspace string) []Listed {
	state := paths.WorkspaceStateDir(workspace)
	sr, err := paths.OpenStateDirRoot(state)
	if err != nil {
		return nil
	}
	defer sr.Close()
	rr, err := paths.OpenStateSubdirRoot(sr, RescueDirName, filepath.Join(state, RescueDirName))
	if err != nil {
		return nil
	}
	defer rr.Close()
	entries, err := fs.ReadDir(rr.FS(), ".")
	if err != nil {
		return nil
	}
	var out []Listed
	for _, e := range entries {
		dir := filepath.Join(state, RescueDirName, e.Name())
		one, err := paths.OpenStateSubdirRoot(rr, e.Name(), dir)
		if err != nil {
			continue
		}
		l := Listed{Dir: dir}
		l.Bytes, l.Err = treeBytes(one.FS())
		if b, err := fs.ReadFile(one.FS(), rescueRecordName); err == nil {
			var rec RescueRecord
			if json.Unmarshal(b, &rec) == nil {
				l.Created = rec.Created
				home := filepath.Join(state, filepath.FromSlash(rec.HomeRel))
				if rec.HomeRel != "" && paths.IsWorkspaceHomeState(workspace, home) {
					l.Restore = RestoreCommand(dir, home)
				}
			}
		}
		one.Close()
		out = append(out, l)
	}
	return out
}

// treeBytes sums the apparent size of every regular file in fsys, following no link.
func treeBytes(fsys fs.FS) (int64, error) {
	var total int64
	err := fs.WalkDir(fsys, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}
