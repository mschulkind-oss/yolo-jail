package packmigrate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Apply carries out a planned migration. In a git checkout each tracked file moves with
// `git mv` (the manifest too, pack.json → pack.toml, before its new bytes are written), so
// history follows; an untracked file, or a pack outside any checkout, moves on the filesystem.
//
// It re-checks the plan first — the legacy manifest unchanged, every source present, every
// destination free — and refuses with nothing written if any of that drifted. After the last
// write it reloads the pack and compares what it delivers with what it delivered before; a
// difference undoes the migration and is reported.
func Apply(m *Migration) error {
	if m == nil {
		return fmt.Errorf("packmigrate: nothing to apply")
	}
	if err := recheck(m); err != nil {
		return err
	}
	git := inCheckout(m.Dir)
	var done []step
	undo := func(cause error) error {
		if rollbackErr := rollback(m, done); rollbackErr != nil {
			return fmt.Errorf("%w; undoing the migration also failed: %v — restore %s from version control or a backup", cause, rollbackErr, m.Dir)
		}
		return fmt.Errorf("%w; the migration was undone and %s is as it was", cause, m.Dir)
	}
	for _, move := range m.Moves {
		s, err := moveOne(m.Dir, move.From, move.To, git)
		if err != nil {
			return undo(fmt.Errorf("%s: moving %s to %s: %w", m.Dir, move.From, move.To, err))
		}
		done = append(done, s)
	}
	if m.Manifest != nil && m.inPlace {
		info, err := os.Stat(filepath.Join(m.Dir, m.Legacy))
		if err != nil {
			return undo(fmt.Errorf("%s: %w", m.Dir, err))
		}
		s := step{from: m.Legacy, to: m.Legacy, manifest: true, legacyBytes: m.legacyBytes}
		if err := os.WriteFile(filepath.Join(m.Dir, m.Legacy), m.Manifest, info.Mode().Perm()); err != nil {
			return undo(fmt.Errorf("%s: writing %s: %w", m.Dir, m.Legacy, err))
		}
		done = append(done, s)
	} else if m.Manifest != nil {
		manifestStep, err := writeManifest(m.Dir, m.Legacy, packdecl.ManifestTOMLName, m.Manifest, m.legacyBytes, git)
		if err != nil {
			return undo(fmt.Errorf("%s: writing %s: %w", m.Dir, packdecl.ManifestTOMLName, err))
		}
		done = append(done, manifestStep)
	}
	for _, l := range m.Loopholes {
		s, err := writeManifest(m.Dir, path.Join(l.Dir, l.Legacy), path.Join(l.Dir, loopholedecl.ManifestTOMLName), l.TOML, l.legacyBytes, git)
		if err != nil {
			return undo(fmt.Errorf("%s: writing %s: %w", m.Dir, path.Join(l.Dir, loopholedecl.ManifestTOMLName), err))
		}
		done = append(done, s)
	}

	pack, problems := packload.LoadDir(m.Dir, "")
	if len(problems) != 0 {
		return undo(fmt.Errorf("%s: the migrated pack does not load:\n  %s", m.Dir, strings.Join(problems, "\n  ")))
	}
	for _, l := range m.Loopholes {
		got, err := loopholedecl.LoadDir(filepath.Join(m.Dir, filepath.FromSlash(l.Dir)))
		if err != nil {
			return undo(fmt.Errorf("%s: the migrated loophole %s does not read: %v", m.Dir, l.Dir, err))
		}
		if err := LoopholeDifference(l.want, got); err != nil {
			return undo(fmt.Errorf("%s: %s: %w", m.Dir, l.Dir, err))
		}
	}
	// A pack that declared a provider both ways did not load before, so there is no delivery to
	// compare; a provider declaration delivers no file.
	if m.Manifest != nil && (m.delivery != nil || !m.inPlace) {
		delivery, err := Delivery(pack)
		if err != nil {
			return undo(fmt.Errorf("%s: reading what the migrated pack delivers: %w", m.Dir, err))
		}
		if diffs := DeliveryDifferences(m.delivery, delivery); len(diffs) != 0 {
			return undo(fmt.Errorf("%s: the migrated pack delivers different files:\n  %s", m.Dir, strings.Join(diffs, "\n  ")))
		}
	}
	removeEmptiedDirs(m.Dir, m.Moves)
	return nil
}

func recheck(m *Migration) error {
	var collisions []Collision
	if m.Manifest != nil {
		current, err := os.ReadFile(filepath.Join(m.Dir, m.Legacy))
		if err != nil || !bytes.Equal(current, m.legacyBytes) {
			return fmt.Errorf("%s: %s changed since the migration was planned; nothing was changed — run `yolo pack migrate %s` again", m.Dir, m.Legacy, m.Dir)
		}
		if _, err := os.Lstat(filepath.Join(m.Dir, packdecl.ManifestTOMLName)); err == nil && !m.inPlace {
			collisions = append(collisions, Collision{Path: packdecl.ManifestTOMLName, Reason: "already exists"})
		}
	}
	for _, l := range m.Loopholes {
		if !l.unchanged(m.Dir) {
			return fmt.Errorf("%s: %s/%s changed since the migration was planned; nothing was changed — run `yolo pack migrate %s` again", m.Dir, l.Dir, l.Legacy, m.Dir)
		}
		if _, err := os.Lstat(filepath.Join(m.Dir, filepath.FromSlash(l.Dir), loopholedecl.ManifestTOMLName)); err == nil {
			collisions = append(collisions, Collision{Path: path.Join(l.Dir, loopholedecl.ManifestTOMLName), Reason: "already exists"})
		}
	}
	for _, move := range m.Moves {
		if info, err := os.Lstat(filepath.Join(m.Dir, filepath.FromSlash(move.From))); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%s: %s changed since the migration was planned; nothing was changed — run `yolo pack migrate %s` again", m.Dir, move.From, m.Dir)
		}
	}
	collisions = append(collisions, destinationCollisions(m.Dir, m.Moves)...)
	if len(collisions) != 0 {
		return &CollisionError{Dir: m.Dir, Collisions: collisions}
	}
	return nil
}

// step is one completed mutation and how to reverse it.
type step struct {
	from, to string // pack-relative
	git      bool
	// manifest-only: the new file's bytes were written over the moved legacy file, whose own
	// bytes a rollback puts back
	manifest    bool
	legacyBytes []byte
}

func inCheckout(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func tracked(dir, rel string) bool {
	return exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", "--", rel).Run() == nil
}

func gitMove(dir, from, to string) error {
	out, err := exec.Command("git", "-C", dir, "mv", "--", from, to).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git mv: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func moveOne(dir, from, to string, git bool) (step, error) {
	target := filepath.Join(dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return step{}, err
	}
	if git && tracked(dir, from) {
		if err := gitMove(dir, from, to); err != nil {
			return step{}, err
		}
		return step{from: from, to: to, git: true}, nil
	}
	if err := os.Rename(filepath.Join(dir, filepath.FromSlash(from)), target); err != nil {
		return step{}, err
	}
	return step{from: from, to: to}, nil
}

// writeManifest replaces the legacy manifest at legacyRel with the new file's bytes at newRel,
// moving it with the version-control move where it is tracked so history follows. Both names
// are pack-relative.
func writeManifest(dir, legacyRel, newRel string, body, legacyBytes []byte, git bool) (step, error) {
	legacy := filepath.Join(dir, filepath.FromSlash(legacyRel))
	info, err := os.Stat(legacy)
	if err != nil {
		return step{}, err
	}
	target := filepath.Join(dir, filepath.FromSlash(newRel))
	s := step{from: legacyRel, to: newRel, manifest: true, legacyBytes: legacyBytes}
	if git && tracked(dir, legacyRel) {
		if err := gitMove(dir, legacyRel, newRel); err != nil {
			return step{}, err
		}
		s.git = true
	} else if err := os.Rename(legacy, target); err != nil {
		return step{}, err
	}
	if err := os.WriteFile(target, body, info.Mode().Perm()); err != nil {
		return s, err
	}
	return s, nil
}

// rollback reverses the completed steps, newest first.
func rollback(m *Migration, done []step) error {
	var failures []string
	for i := len(done) - 1; i >= 0; i-- {
		s := done[i]
		if s.manifest {
			if err := os.WriteFile(filepath.Join(m.Dir, filepath.FromSlash(s.to)), s.legacyBytes, 0o644); err != nil {
				failures = append(failures, err.Error())
				continue
			}
		}
		if s.from == s.to {
			continue
		}
		var err error
		if s.git {
			err = gitMove(m.Dir, s.to, s.from)
		} else {
			err = os.Rename(filepath.Join(m.Dir, filepath.FromSlash(s.to)), filepath.Join(m.Dir, filepath.FromSlash(s.from)))
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s → %s: %v", s.to, s.from, err))
		}
	}
	var moved []Move
	for _, s := range done {
		if !s.manifest {
			moved = append(moved, Move{From: s.to, To: s.from})
		}
	}
	removeEmptiedDirs(m.Dir, moved)
	if len(failures) != 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

// removeEmptiedDirs removes each move source's parent directories that the moves left empty,
// deepest first, never the pack root.
func removeEmptiedDirs(dir string, moves []Move) {
	seen := map[string]bool{}
	var parents []string
	for _, move := range moves {
		for parent := path.Dir(move.From); parent != "." && parent != "/" && parent != ""; parent = path.Dir(parent) {
			if !seen[parent] {
				seen[parent] = true
				parents = append(parents, parent)
			}
		}
	}
	sort.Slice(parents, func(i, j int) bool { return strings.Count(parents[i], "/") > strings.Count(parents[j], "/") })
	for _, parent := range parents {
		_ = os.Remove(filepath.Join(dir, filepath.FromSlash(parent))) // fails, harmlessly, unless empty
	}
}
