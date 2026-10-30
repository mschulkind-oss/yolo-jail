package configmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"golang.org/x/sys/unix"
)

// ApplyOptions tunes Apply. The zero value is the real migration.
type ApplyOptions struct {
	// failAfter is a test seam: it runs after each write step, and an error aborts the migration
	// there and restores every file.
	failAfter func(step string) error
}

// ApplyResult is what Apply did.
type ApplyResult struct {
	// Written are the new files; Backups the originals kept as <name>.pre-migrate; Links the
	// symlinks made beside linked old names.
	Written, Backups, Links []string
	// Records are the records only yolo reads, each with what was done to it.
	Records []Record
	// Ignores are the plan's Ignores, each marked Done or carrying its hint.
	Ignores []Ignore
}

// Apply writes a plan that proved equal. It locks every file it replaces the way `yolo config
// set` does (the parent directory of each), refuses a file that
// changed since the plan and any collision before the first write, keeps each original as
// <name>.pre-migrate, reads the written files back through the launch's readers, and restores
// every file if any step or that check fails. Records are respelled only after the files are in
// place, and only where they still hold what the old files said.
func Apply(p *MigrationPlan, opts ApplyOptions) (*ApplyResult, error) {
	if p == nil || !p.Equal {
		return nil, errNotEqual
	}
	res := &ApplyResult{}
	if len(p.Targets) == 0 {
		return res, nil
	}
	unlock, err := lockTargets(p.Targets)
	if err != nil {
		return nil, err
	}
	defer unlock()
	for _, t := range p.Targets {
		now, err := os.ReadFile(t.Old)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w; nothing was written", t.Old, err)
		}
		if !bytes.Equal(now, t.File.Bytes) {
			return nil, fmt.Errorf("%s changed since the migration was planned, so nothing was written; run `yolo config migrate` again", t.Old)
		}
	}
	if err := p.preflight(); err != nil {
		return nil, err
	}

	var undo []func() error
	restore := func(cause error) error {
		var failed []error
		for i := len(undo) - 1; i >= 0; i-- {
			if err := undo[i](); err != nil {
				failed = append(failed, err)
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("%w; restoring the original files also failed: %v. Each original is kept as <name>.pre-migrate; `mv` it back to its name", cause, errors.Join(failed...))
		}
		return fmt.Errorf("%w; every original file was restored and nothing else changed", cause)
	}
	step := func(name string) error {
		if opts.failAfter != nil {
			return opts.failAfter(name)
		}
		return nil
	}
	for _, t := range p.Targets {
		info, err := os.Stat(t.Old)
		if err != nil {
			return nil, restore(err)
		}
		mode := info.Mode().Perm()
		if t.New != t.Old {
			if err := createExclusive(t.New, t.Bytes, mode); err != nil {
				return nil, restore(fmt.Errorf("write %s: %w", t.New, err))
			}
			newPath := t.New
			undo = append(undo, func() error { return os.Remove(newPath) })
			res.Written = append(res.Written, t.New)
			if err := step("write " + t.New); err != nil {
				return nil, restore(err)
			}
			if t.Link != nil {
				if err := os.Symlink(t.Link.Target, t.Link.Path); err != nil {
					return nil, restore(fmt.Errorf("link %s: %w", t.Link.Path, err))
				}
				linkPath := t.Link.Path
				undo = append(undo, func() error { return os.Remove(linkPath) })
				res.Links = append(res.Links, t.Link.Path)
				if err := step("link " + t.Link.Path); err != nil {
					return nil, restore(err)
				}
			}
			if err := renameNoReplace(t.Old, t.Backup); err != nil {
				return nil, restore(fmt.Errorf("keep %s as %s: %w", t.Old, t.Backup, err))
			}
			oldPath, backup := t.Old, t.Backup
			undo = append(undo, func() error { return renameNoReplace(backup, oldPath) })
			res.Backups = append(res.Backups, t.Backup)
			if err := step("backup " + t.Backup); err != nil {
				return nil, restore(err)
			}
			continue
		}
		if err := createExclusive(t.Backup, t.File.Bytes, mode); err != nil {
			return nil, restore(fmt.Errorf("keep %s as %s: %w", t.Old, t.Backup, err))
		}
		backup := t.Backup
		undo = append(undo, func() error { return os.Remove(backup) })
		res.Backups = append(res.Backups, t.Backup)
		if err := writeAtomic(t.Old, t.Bytes, mode); err != nil {
			return nil, restore(fmt.Errorf("write %s: %w", t.Old, err))
		}
		oldPath, before := t.Old, t.File.Bytes
		undo = append(undo, func() error { return writeAtomic(oldPath, before, mode) })
		res.Written = append(res.Written, t.Old)
		if err := step("respell " + t.Old); err != nil {
			return nil, restore(err)
		}
	}

	after, err := readCanonical(p.Graph, newUserTop(p.Graph), p.Graph.Request.Workspace, nil, nil, "", false, false, packload.Embedded())
	if err != nil {
		return nil, restore(fmt.Errorf("the written files do not read: %w", err))
	}
	if after.set != p.canonical {
		return nil, restore(fmt.Errorf("the written files do not say what the plan proved"))
	}
	res.Records = respellRecords(p.Records, p.Graph.Request.Workspace)
	res.Ignores = applyIgnores(p.Ignores, p.Graph.Request.Workspace)
	return res, nil
}

func newUserTop(g *Graph) string {
	if g.userTop == nil {
		return ""
	}
	if g.userTop.Role == RoleConvert {
		return twinPath(g.userTop.Logical)
	}
	return g.userTop.Logical
}

// respellRecords rewrites each record planned for respelling to what the launch computes from the
// new files, unless it changed since the plan. Respelling is not approval: a record that no
// longer holds what the old files said is left, and its workspace asks once.
func respellRecords(planned []Record, workspace string) []Record {
	out := make([]Record, 0, len(planned))
	for _, r := range planned {
		if r.State != RecordRespell {
			out = append(out, r)
			continue
		}
		read, current, write := config.ReadApprovalRecord, config.ApprovalRecordPart, config.WriteApprovalRecord
		if r.Kind == "boot baseline" {
			read, current, write = config.ReadBootBaseline, config.BootBaselinePart, config.WriteBootBaseline
		}
		text, exists, err := read(workspace)
		if err != nil || !exists || text != r.before {
			r.State, r.Why = RecordLeft, "it changed while the migration ran; the next launch asks once"
			out = append(out, r)
			continue
		}
		now, err := current(workspace)
		if err != nil {
			r.State, r.Why = RecordLeft, "the new config could not be read the way a launch reads it ("+err.Error()+"); the next launch asks once"
			out = append(out, r)
			continue
		}
		if now != text {
			if err := write(workspace, now); err != nil {
				r.State, r.Why = RecordLeft, "it could not be written ("+err.Error()+"); the next launch asks once"
			}
		}
		out = append(out, r)
	}
	return out
}

// lockTargets takes the lock `yolo config set` takes for each file it replaces: its parent
// directory (flock on the directory itself, never on an inode a rename replaces), in a fixed
// order. No lock file is made beside a config file: the directory lock is the one every writer
// takes, and a file left there would outlive the old name it guarded (RC-D34).
func lockTargets(targets []Target) (func(), error) {
	dirSet := map[string]bool{}
	for _, t := range targets {
		dirSet[filepath.Dir(t.Old)] = true
		if t.Link != nil {
			dirSet[filepath.Dir(t.Link.Path)] = true
		}
	}
	dirs := sortedKeys(dirSet)
	var release []func()
	unlock := func() {
		for i := len(release) - 1; i >= 0; i-- {
			release[i]()
		}
	}
	for _, dir := range dirs {
		f, err := os.Open(dir)
		if err != nil {
			unlock()
			return nil, fmt.Errorf("open %s: %w", dir, err)
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			f.Close()
			unlock()
			return nil, fmt.Errorf("lock %s: %w", dir, err)
		}
		release = append(release, func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() })
		held, err1 := f.Stat()
		named, err2 := os.Stat(dir)
		if err1 != nil || err2 != nil || !os.SameFile(held, named) {
			unlock()
			return nil, fmt.Errorf("%s changed while waiting for its lock; run `yolo config migrate` again", dir)
		}
	}
	return unlock, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// createExclusive writes a new file that must not exist, complete or not at all: the bytes go to
// a temporary file first, which is then hard-linked to its name (a link never replaces).
func createExclusive(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".migrate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpPath, path)
}

// writeAtomic replaces path with data through a temporary file in the same directory.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".migrate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// renameNoReplace moves a file to a name that must not exist: a hard link, then the old name
// removed.
func renameNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
