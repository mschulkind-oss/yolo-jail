package jailcontent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RenderMechanicsIndexAndMap returns the applied-state files that may be refreshed after an
// effective live apply: the index, the host map, and the topic files, which carry the applied
// facts the briefing's core points at (docs/reference/jail-mechanics-skill.md#topic-files). It does not render
// recipes or any other built-in content.
func RenderMechanicsIndexAndMap(facts FactSheet) (map[string][]byte, error) {
	return renderMechanicsIndexAndMap(facts)
}

// mechanicsFileSnapshot is one file a refresh changes: what it held before, for the rollback,
// and what it is to hold, or that it is to go.
type mechanicsFileSnapshot struct {
	path    string
	data    []byte
	mode    os.FileMode
	exists  bool
	content []byte
	remove  bool
}

// RefreshMechanicsFiles updates jail-mechanics/SKILL.md, host-map.md and topics/ in the existing
// authenticated targets, and removes a topic file the facts no longer render (a context mount
// removed takes topics/mounts.md with it). It preserves directory inodes and restores the prior
// files if any output cannot be written.
func RefreshMechanicsFiles(existingTargetDirs []string, facts FactSheet) error {
	files, err := RenderMechanicsIndexAndMap(facts)
	if err != nil {
		return err
	}
	// The mechanics skill sits where the composition delivered it, under the user's
	// shipped_skill_prefix (shippedprefix.go).
	prefix := shippedSkillPrefix()
	files = PrefixBuiltinSkillFiles(files, prefix)
	mechanics := BuiltinSkillName(prefix, mechanicsSkill)
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	var snapshots []mechanicsFileSnapshot
	for _, dir := range existingTargetDirs {
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			if statErr == nil {
				statErr = fmt.Errorf("not a directory")
			}
			return fmt.Errorf("mechanics target %q: %w", dir, statErr)
		}
		for _, rel := range rels {
			s, err := snapshotMechanicsFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				return err
			}
			s.content = files[rel]
			snapshots = append(snapshots, s)
		}
		stale, err := staleMechanicsTopics(filepath.Join(dir, mechanics, "topics"), mechanics, files)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, stale...)
	}
	written := 0
	rollback := func() error {
		var first error
		for i := written - 1; i >= 0; i-- {
			s := snapshots[i]
			if s.exists {
				if err := replaceMechanicsFile(s.path, s.data, s.mode); err != nil && first == nil {
					first = err
				}
			} else if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) && first == nil {
				first = err
			}
		}
		return first
	}
	for _, s := range snapshots {
		var err error
		if s.remove {
			err = os.Remove(s.path)
		} else if err = os.MkdirAll(filepath.Dir(s.path), 0o755); err == nil {
			// MkdirAll: a target staged before the topics existed has no topics/ directory yet.
			err = replaceMechanicsFile(s.path, s.content, 0o644)
		}
		if err != nil {
			rbErr := rollback()
			if rbErr != nil {
				return fmt.Errorf("write mechanics file: %w (rollback failed: %v)", err, rbErr)
			}
			return fmt.Errorf("write mechanics file: %w", err)
		}
		written++
	}
	return nil
}

// snapshotMechanicsFile records what path holds now. Anything but a regular file is refused
// rather than replaced.
func snapshotMechanicsFile(path string) (mechanicsFileSnapshot, error) {
	s := mechanicsFileSnapshot{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() {
		return s, fmt.Errorf("mechanics target %q is not a regular file", path)
	}
	if s.data, err = os.ReadFile(path); err != nil {
		return s, err
	}
	s.mode, s.exists = info.Mode().Perm(), true
	return s, nil
}

// staleMechanicsTopics are the topic files in dir that files no longer holds, each to be removed.
func staleMechanicsTopics(dir, mechanics string, files map[string][]byte) ([]mechanicsFileSnapshot, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stale []mechanicsFileSnapshot
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if _, ok := files[mechanics+"/topics/"+e.Name()]; ok {
			continue
		}
		s, err := snapshotMechanicsFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		s.remove = true
		stale = append(stale, s)
	}
	return stale, nil
}

func replaceMechanicsFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".mechanics-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
