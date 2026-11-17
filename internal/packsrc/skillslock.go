package packsrc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SkillsLockName is the shared repository/ref record, separate from pack and fork pins.
const SkillsLockName = "skills.lock.json"
const skillsLockSchema = 1

// SkillRecord contains no selection history: every caller validates its own rows.
type SkillRecord struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	Kind   string `json:"kind"`
}
type skillsLock struct {
	Schema int                    `json:"schema"`
	Pairs  map[string]SkillRecord `json:"pairs"`
}

// SkillsLockPath returns the sourced-skills record beside the user configuration.
func SkillsLockPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), SkillsLockName)
}
func skillPair(repo, ref string) string { return repo + "\n" + ref }
func loadSkillsLock(path string) (*skillsLock, error) {
	l := &skillsLock{Schema: skillsLockSchema, Pairs: map[string]SkillRecord{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("%s: %w — restore the record or run yolo pack install to resolve the selected sources", path, err)
	}
	if l.Schema != skillsLockSchema {
		return nil, newerSchemaError(path, l.Schema, skillsLockSchema)
	}
	if l.Pairs == nil {
		l.Pairs = map[string]SkillRecord{}
	}
	for key, r := range l.Pairs {
		if key != skillPair(r.Repo, r.Ref) || !isFullSHA(r.Commit) {
			return nil, fmt.Errorf("%s: invalid sourced skill record %q — restore it or run yolo pack install", path, key)
		}
	}
	return l, nil
}

// saveSkillRecord is a leaf transaction. The caller holds ONE mirror lock; no
// subprocess, validation, callback or other lock acquisition happens below here.
func (s *Store) saveSkillRecord(path string, r SkillRecord, waiting func(string)) error {
	unlock, err := s.flock(filepath.Join(s.Dir, "locks", "skills-record-"+mirrorSlug(path)+".lock"), "the skills record "+path, waiting)
	if err != nil {
		return err
	}
	defer unlock()
	l, err := loadSkillsLock(path)
	if err != nil {
		return err
	}
	if err := s.parentCtx().Err(); err != nil {
		return err
	}
	l.Pairs[skillPair(r.Repo, r.Ref)] = r
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".skills-record-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0644); err != nil {
		return err
	}
	if err := s.parentCtx().Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
