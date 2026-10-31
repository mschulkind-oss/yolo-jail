package configmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The commit's moves (config-migration.md, the commit steps). Each is a rename, a hard link or a
// symlink, never a write into an original, and each is idempotent: run again over a state a kill
// left at any point inside it, it finishes it or finds it finished.
const (
	// stepPlace moves a staged converted file From to its name To.
	stepPlace = "place"
	// stepLink makes the symlink To with text Text beside a linked old name.
	stepLink = "link"
	// stepUnlink removes the user's old symlink To, whose text is Text.
	stepUnlink = "unlink"
	// stepBackup moves the original From to To, its <name>.pre-migrate.
	stepBackup = "backup"
	// stepReplace keeps the original To as Backup (a hard link: the original's inode, untouched)
	// and renames the staged file From over To's name.
	stepReplace = "replace"
)

// commitStep is one move of the commit, as the journal records it.
type commitStep struct {
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	To     string `json:"to"`
	Backup string `json:"backup,omitempty"`
	Text   string `json:"text,omitempty"`
	// Hash is the sha256 of the bytes the step moves into place (place, replace) or keeps
	// (backup); OldHash the original's, for replace.
	Hash    string `json:"hash,omitempty"`
	OldHash string `json:"old_hash,omitempty"`
}

func (s commitStep) String() string {
	switch s.Kind {
	case stepPlace:
		return "placing " + s.To
	case stepLink:
		return "linking " + s.To
	case stepUnlink:
		return "removing the old link " + s.To
	case stepBackup:
		return "keeping " + s.From + " as " + s.To
	case stepReplace:
		return "respelling " + s.To + " (original kept as " + s.Backup + ")"
	}
	return s.Kind + " " + s.To
}

// migrationJournal is the commit's list of moves, written before the first and removed after the
// last.
type migrationJournal struct {
	Version   int          `json:"version"`
	Workspace string       `json:"workspace,omitempty"`
	Steps     []commitStep `json:"steps"`
	// UserConfigDir is the user config directory the migration converts, "" for a workspace
	// alone: where the pack locks live.
	UserConfigDir string `json:"user_config_dir,omitempty"`
	// Renames are the packs the conversion renames: their records move inside the commit, before
	// the journal is removed (respellPackRecords).
	Renames []PackRename `json:"renames,omitempty"`
	// Records are the approval record and boot baseline the plan respells once the files are in
	// place, with the text each held when it was planned.
	Records []journalRecord `json:"records,omitempty"`
	// UserTop is the converted user config's path and Canonical the hash of what the plan proved
	// the converted files say: the read-back a finished migration checks.
	UserTop   string `json:"user_top,omitempty"`
	Canonical string `json:"canonical,omitempty"`
}

// journalRecord is a Record as the journal keeps it.
type journalRecord struct {
	Kind   string      `json:"kind"`
	Path   string      `json:"path,omitempty"`
	State  RecordState `json:"state"`
	Why    string      `json:"why,omitempty"`
	Before string      `json:"before,omitempty"`
}

func journalRecords(rs []Record) []journalRecord {
	out := make([]journalRecord, len(rs))
	for i, r := range rs {
		out[i] = journalRecord{Kind: r.Kind, Path: r.Path, State: r.State, Why: r.Why, Before: r.before}
	}
	return out
}

func (j *migrationJournal) records() []Record {
	out := make([]Record, len(j.Records))
	for i, r := range j.Records {
		out[i] = Record{Kind: r.Kind, Path: r.Path, State: r.State, Why: r.Why, before: r.Before}
	}
	return out
}

// hashCanonical is the read-back's digest of a canonical set.
func hashCanonical(s canonicalSet) string {
	return hashOf([]byte(strings.Join([]string{s.user, s.project, s.composed, s.env, s.providers, s.grants}, "\x00")))
}

// finish is the commit's last part, the same after Apply's moves and after Resume's: the renamed
// packs' records move while the journal still stands, so a kill there is finished by the next
// run; then the journal goes, and the written files are read back through the launch's readers
// and the approval records respelled.
func (j *migrationJournal) finish(journal string, res *ApplyResult) error {
	moved, err := respellPackRecords(j.UserConfigDir, j.Renames)
	res.PackRecords = moved
	if err != nil {
		return fmt.Errorf("the converted files are in place, but moving the renamed packs' records failed (%w); "+
			"fix what that names and run `yolo config migrate` to finish, as %s lists", err, journal)
	}
	if err := os.Remove(journal); err != nil {
		return fmt.Errorf("converted, but the journal %s could not be removed (%w); delete it by hand", journal, err)
	}
	syncDir(filepath.Dir(journal))
	j.result(res)
	if j.Canonical != "" {
		after, err := readCanonical(nil, j.UserTop, j.Workspace, nil, nil, "", false, planOptions{}, packload.Embedded())
		if err != nil {
			return fmt.Errorf("converted: %s; originals kept as %s. But the written files do not read: %w. This is a yolo bug: please report it; to go back, move each original back to its name", strings.Join(res.Written, ", "), strings.Join(res.Backups, ", "), err)
		}
		if hashCanonical(after.set) != j.Canonical {
			return fmt.Errorf("converted: %s; originals kept as %s. But the written files do not say what the plan proved. This is a yolo bug: please report it; to go back, move each original back to its name", strings.Join(res.Written, ", "), strings.Join(res.Backups, ", "))
		}
	}
	res.Records = respellRecords(j.records(), j.Workspace)
	return nil
}

// stepDirs are the directories a step changes an entry of.
func (s commitStep) dirs() []string {
	var out []string
	for _, p := range []string{s.From, s.To, s.Backup} {
		if p != "" {
			out = append(out, filepath.Dir(p))
		}
	}
	return out
}

// syncDir makes a directory's entries durable: a rename, link or removal in it is on disk once
// this returns, so a crash cannot undo a move the journal already counts as made.
func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	f.Close()
}

func (j *migrationJournal) write(path string) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return createExclusive(path, append(b, '\n'), 0o600)
}

// result fills res with what the journal's moves did.
func (j *migrationJournal) result(res *ApplyResult) {
	for _, s := range j.Steps {
		switch s.Kind {
		case stepPlace:
			res.Written = append(res.Written, s.To)
		case stepReplace:
			res.Written = append(res.Written, s.To)
			res.Backups = append(res.Backups, s.Backup)
		case stepBackup:
			res.Backups = append(res.Backups, s.To)
		case stepUnlink:
			for _, l := range j.Steps {
				if l.Kind == stepLink && filepath.Dir(l.To) == filepath.Dir(s.To) && twinName(filepath.Base(s.To)) == filepath.Base(l.To) {
					res.Links = append(res.Links, LinkMove{From: s.To, To: l.To})
				}
			}
		}
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hashFile is the sha256 of the file at path; "" when there is none.
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hashOf(b), nil
}

func lexists(path string) bool { _, err := os.Lstat(path); return err == nil }

// do makes the move, or finishes it, or finds it made.
func (s commitStep) do() error {
	switch s.Kind {
	case stepPlace:
		have, err := hashFile(s.To)
		if err != nil {
			return err
		}
		if have == s.Hash {
			if lexists(s.From) {
				return os.Remove(s.From)
			}
			return nil
		}
		if lexists(s.To) {
			return fmt.Errorf("%s exists and is not the converted file", s.To)
		}
		if staged, err := hashFile(s.From); err != nil || staged != s.Hash {
			return fmt.Errorf("the converted file staged as %s is missing or changed", s.From)
		}
		return renameNoReplace(s.From, s.To)
	case stepLink:
		if text, err := os.Readlink(s.To); err == nil {
			if text == s.Text {
				return nil
			}
			return fmt.Errorf("%s is a symlink to %s, not to %s", s.To, text, s.Text)
		}
		if lexists(s.To) {
			return fmt.Errorf("%s exists and is not the symlink to %s", s.To, s.Text)
		}
		return os.Symlink(s.Text, s.To)
	case stepUnlink:
		text, err := os.Readlink(s.To)
		if err != nil {
			if !lexists(s.To) {
				return nil
			}
			return fmt.Errorf("%s is no longer the symlink to %s", s.To, s.Text)
		}
		if text != s.Text {
			return fmt.Errorf("%s now points at %s, not %s", s.To, text, s.Text)
		}
		return os.Remove(s.To)
	case stepBackup:
		kept, err := hashFile(s.To)
		if err != nil {
			return err
		}
		if lexists(s.From) {
			have, err := hashFile(s.From)
			if err != nil {
				return err
			}
			if have != s.Hash {
				return fmt.Errorf("%s changed since the migration was planned", s.From)
			}
			if kept == s.Hash {
				return os.Remove(s.From)
			}
			if lexists(s.To) {
				return fmt.Errorf("%s exists and is not the original %s", s.To, s.From)
			}
			return renameNoReplace(s.From, s.To)
		}
		if kept != s.Hash {
			return fmt.Errorf("%s is gone and %s does not hold it", s.From, s.To)
		}
		return nil
	case stepReplace:
		have, err := hashFile(s.To)
		if err != nil {
			return err
		}
		kept, err := hashFile(s.Backup)
		if err != nil {
			return err
		}
		if have == s.Hash && kept == s.OldHash {
			if lexists(s.From) {
				return os.Remove(s.From)
			}
			return nil
		}
		if kept != s.OldHash {
			if lexists(s.Backup) {
				return fmt.Errorf("%s exists and is not the original %s", s.Backup, s.To)
			}
			if have != s.OldHash {
				return fmt.Errorf("%s changed since the migration was planned", s.To)
			}
			if err := os.Link(s.To, s.Backup); err != nil {
				return err
			}
		}
		if staged, err := hashFile(s.From); err != nil || staged != s.Hash {
			return fmt.Errorf("the converted file staged as %s is missing or changed", s.From)
		}
		return os.Rename(s.From, s.To)
	}
	return fmt.Errorf("unknown step %q", s.Kind)
}

// Resume finishes a migration a kill or a failed move stopped part way, from its journal: every
// move it lists is made or found made, forward, never back, and the journal is removed. A move
// whose files changed since is refused, naming them, and the journal stays.
func Resume(journal string) (*ApplyResult, error) {
	b, err := os.ReadFile(journal)
	if err != nil {
		return nil, err
	}
	var j migrationJournal
	if err := json.Unmarshal(b, &j); err != nil || j.Version != 1 {
		return nil, fmt.Errorf("%s is not a journal this yolo reads; move it aside and finish the moves by hand", journal)
	}
	// The locks Apply took: a `yolo config set` or a second migrate in flight is waited for.
	dirSet := map[string]bool{}
	for _, s := range j.Steps {
		for _, d := range s.dirs() {
			if _, err := os.Stat(d); err == nil {
				dirSet[d] = true
			}
		}
	}
	unlock, err := lockDirs(sortedKeys(dirSet))
	if err != nil {
		return nil, err
	}
	defer unlock()
	for _, s := range j.Steps {
		if err := s.do(); err != nil {
			return nil, partWay(journal, s, err)
		}
		for _, d := range s.dirs() {
			syncDir(d)
		}
	}
	res := &ApplyResult{}
	if err := j.finish(journal, res); err != nil {
		return res, err
	}
	return res, nil
}

// Interrupted is the journal of a migration of req that stopped part way, or "".
func Interrupted(req Request) string {
	candidates := []string{}
	if req.UserConfigDir != "" {
		candidates = append(candidates, journalPathFor(req))
	}
	if req.Workspace != "" {
		candidates = append(candidates, journalPathFor(Request{Workspace: req.Workspace}))
	}
	for _, p := range candidates {
		if lexists(p) {
			return p
		}
	}
	return ""
}
