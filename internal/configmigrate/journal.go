package configmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	for _, s := range j.Steps {
		if err := s.do(); err != nil {
			return nil, partWay(journal, s, err)
		}
	}
	if err := os.Remove(journal); err != nil {
		return nil, err
	}
	res := &ApplyResult{}
	j.result(res)
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
