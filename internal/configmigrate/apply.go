package configmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"golang.org/x/sys/unix"
)

// ApplyOptions tunes Apply. The zero value is the real migration.
type ApplyOptions struct {
	// StopAfter is a test seam for a kill: it runs after each commit step, numbered from 0, and
	// true stops the migration there exactly as a kill would, journal and all.
	StopAfter func(step int, name string) bool
}

// ErrInterrupted is Apply stopped part way by its test seam: the files are as a kill there would
// leave them, and Resume finishes the moves.
var ErrInterrupted = errors.New("the migration stopped part way")

// ApplyResult is what Apply did.
type ApplyResult struct {
	// Written are the new files; Backups the originals kept as <name>.pre-migrate.
	Written, Backups []string
	// Links are the user's symlinks renamed to their new names (RC-D41).
	Links []LinkMove
	// Records are the records only yolo reads, each with what was done to it.
	Records []Record
	// Ignores are the plan's Ignores, each marked Done or carrying its hint.
	Ignores []Ignore
}

// LinkMove is a user's symlink renamed by a conversion: From is the old name, To the new one.
type LinkMove struct{ From, To string }

// Apply carries out a plan that proved equal and that the launch's config gate accepted (RC-D44).
// It never rewrites an original's bytes and never restores one (RC-D45; config-migration.md, the commit
// steps):
//
//  1. Under the lock `yolo config set` takes, it refuses a file that changed since the plan and
//     any collision, then writes every converted file beside its destination under a hidden
//     staged name, and adds the git ignore lines. A kill here leaves the old layout loading as
//     before, and the next run removes the staged files.
//  2. It writes the journal: the list of moves still to make, with the bytes each must carry.
//  3. It makes the moves, every one a rename, a hard link or a symlink: the converted files no
//     top-level file reads yet, then each respelled TOML file and each top-level file, then the
//     originals nothing reads any more, each kept as <name>.pre-migrate. While the journal exists
//     the launch and `yolo check` refuse, naming `yolo config migrate`, which finishes the moves
//     (Resume): forward, never back.
//  4. It removes the journal, reads the written files back through the launch's readers, and
//     respells the records only yolo reads.
func Apply(p *MigrationPlan, opts ApplyOptions) (*ApplyResult, error) {
	if p == nil || !p.Equal {
		return nil, errNotEqual
	}
	if p.Refused != nil {
		return nil, p.Refused
	}
	res := &ApplyResult{}
	if len(p.Targets) == 0 {
		return res, nil
	}
	journal := journalPathFor(p.Graph.Request)
	if _, err := os.Lstat(journal); err == nil {
		return nil, fmt.Errorf("an earlier `yolo config migrate` stopped part way (%s), so nothing was changed; run `yolo config migrate` to finish it first", journal)
	}
	unlock, err := lockTargets(p.Targets)
	if err != nil {
		return nil, err
	}
	defer unlock()
	for _, t := range p.Targets {
		now, err := os.ReadFile(t.Old)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w; nothing was changed", t.Old, err)
		}
		if !bytes.Equal(now, t.File.Bytes) {
			return nil, fmt.Errorf("%s changed since the migration was planned, so nothing was changed; run `yolo config migrate` again", t.Old)
		}
	}
	if err := p.preflight(); err != nil {
		return nil, err
	}

	// Step 1: stage. Nothing a reader reads changes.
	for _, t := range p.Targets {
		info, err := os.Stat(t.Old)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w; nothing was changed", t.Old, err)
		}
		staged := stagedPath(t.New)
		_ = os.Remove(staged) // one a run killed before its journal left
		if err := createExclusive(staged, t.Bytes, info.Mode().Perm()); err != nil {
			removeStaged(p.Targets)
			return nil, fmt.Errorf("stage %s: %w; nothing was changed", t.New, err)
		}
	}
	res.Ignores = applyIgnores(p.Ignores, p.Graph.Request.Workspace)

	// Step 2: the journal.
	j := &migrationJournal{Version: 1, Workspace: p.Graph.Request.Workspace, Steps: commitSteps(p.Targets)}
	if err := j.write(journal); err != nil {
		removeStaged(p.Targets)
		return nil, fmt.Errorf("write the migration's journal %s: %w; nothing was changed", journal, err)
	}

	// Step 3: the moves.
	for i, s := range j.Steps {
		if err := s.do(); err != nil {
			return nil, partWay(journal, s, err)
		}
		if opts.StopAfter != nil && opts.StopAfter(i, s.String()) {
			return nil, &PartWayError{Journal: journal, Step: s.String(), Err: ErrInterrupted}
		}
	}

	// Step 4: done.
	if err := os.Remove(journal); err != nil {
		return nil, fmt.Errorf("converted, but the journal %s could not be removed (%w); delete it by hand", journal, err)
	}
	j.result(res)
	after, err := readCanonical(p.Graph, newUserTop(p.Graph), p.Graph.Request.Workspace, nil, nil, "", false, planOptions{}, packload.Embedded())
	if err != nil {
		return res, fmt.Errorf("converted: %s; originals kept as %s. But the written files do not read: %w. This is a yolo bug: please report it; to go back, move each original back to its name", strings.Join(res.Written, ", "), strings.Join(res.Backups, ", "), err)
	}
	if after.set != p.canonical {
		return res, fmt.Errorf("converted: %s; originals kept as %s. But the written files do not say what the plan proved. This is a yolo bug: please report it; to go back, move each original back to its name", strings.Join(res.Written, ", "), strings.Join(res.Backups, ", "))
	}
	res.Records = respellRecords(p.Records, p.Graph.Request.Workspace)
	return res, nil
}

// PartWayError is a commit stopped part way: nothing was put back, the journal lists the moves
// left, and `yolo config migrate` finishes them.
type PartWayError struct {
	Journal string
	Step    string
	Err     error
}

func (e *PartWayError) Error() string {
	return fmt.Sprintf("the migration stopped part way, at %s: %v. Nothing was put back; your files are half "+
		"converted, and the launch refuses until the migration is finished. Fix what the error names, then "+
		"run `yolo config migrate` to finish the moves listed in %s", e.Step, e.Err, e.Journal)
}

func (e *PartWayError) Unwrap() error { return e.Err }

// partWay is the error for a move that failed: nothing is put back, and the next run finishes.
func partWay(journal string, s commitStep, err error) error {
	return &PartWayError{Journal: journal, Step: s.String(), Err: err}
}

func removeStaged(targets []Target) {
	for _, t := range targets {
		_ = os.Remove(stagedPath(t.New))
	}
}

// stagedPath is the hidden name a converted file waits under, beside its destination.
func stagedPath(dest string) string {
	return filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".migrate-staged")
}

// journalPathFor is where a migration of req keeps its journal: the user config directory when
// it converts the user config, else the workspace's .yolo.
func journalPathFor(req Request) string {
	if req.UserConfigDir != "" {
		return filepath.Join(req.UserConfigDir, config.MigrationJournalName)
	}
	return filepath.Join(req.Workspace, ".yolo", config.MigrationJournalName)
}

// isTop is a target a top-level selector reads by its name: the user config or a workspace
// file, whose old and new names beside each other are refused.
func isTop(t Target) bool { return t.File.Via == "" && !t.File.fragment }

// commitSteps orders the moves so that the old layout keeps loading as long as it can: converted
// files nothing reads yet first, then the files the readers start from, then the originals
// nothing reads any more.
func commitSteps(targets []Target) []commitStep {
	var first, middle, last []commitStep
	for _, t := range targets {
		staged := stagedPath(t.New)
		switch {
		case t.New == t.Old:
			middle = append(middle, commitStep{Kind: stepReplace, From: staged, To: t.Old, Backup: t.Backup,
				Hash: hashOf(t.Bytes), OldHash: hashOf(t.File.Bytes)})
		case isTop(t):
			middle = append(middle, commitStep{Kind: stepPlace, From: staged, To: t.New, Hash: hashOf(t.Bytes)})
			if t.Link != nil {
				middle = append(middle,
					commitStep{Kind: stepLink, To: t.Link.Path, Text: t.Link.Target},
					commitStep{Kind: stepUnlink, To: t.Link.Old, Text: t.Link.Text})
			}
			middle = append(middle, commitStep{Kind: stepBackup, From: t.Old, To: t.Backup, Hash: hashOf(t.File.Bytes)})
		default:
			first = append(first, commitStep{Kind: stepPlace, From: staged, To: t.New, Hash: hashOf(t.Bytes)})
			if t.Link != nil {
				first = append(first, commitStep{Kind: stepLink, To: t.Link.Path, Text: t.Link.Target})
				last = append(last, commitStep{Kind: stepUnlink, To: t.Link.Old, Text: t.Link.Text})
			}
			last = append(last, commitStep{Kind: stepBackup, From: t.Old, To: t.Backup, Hash: hashOf(t.File.Bytes)})
		}
	}
	return append(append(first, middle...), last...)
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

// renameNoReplace moves a file to a name that must not exist: a hard link, then the old name
// removed.
func renameNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
