package packsrc

// rename.go carries a pack's records across a rename of the pack: `yolo config migrate` respells a
// legacy path or URL entry in `packs` as its readable handle (OQ-RC15), which renames the pack
// (`pi-fork` becomes `yolo-packs/pi-fork`), and every record keyed by the old name would otherwise
// read as never made: a re-pin, a re-check and a rebuild at the next launch
// (docs/design/config-migration.md, the pack records). Each function here is idempotent and moves
// a record, never copies one beside the other and never drops one: run again over a state a kill
// left, it finishes or finds it finished.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RenamePackLock moves each renamed pack's packs.lock.json entry to its new name, under the
// lockfile's lock. sources gives a pack's new address where its readable handle spells it
// differently (a direct git table normalizes the URL); the commit and ref are kept. An entry
// already under the new name is left as it is, and the old one is then left too. It returns one
// line per moved entry.
func RenamePackLock(storeDir, lockPath string, renames, sources map[string]string) ([]string, error) {
	var moved []string
	err := WithLock(storeDir, lockPath, nil, func(l *Lock) (bool, error) {
		for _, old := range sortedNames(renames) {
			e, ok := l.Packs[old]
			if !ok {
				continue
			}
			to := renames[old]
			if _, taken := l.Packs[to]; taken {
				continue
			}
			e.Name = to
			if s := sources[old]; s != "" {
				e.Source = s
			}
			l.Packs[to] = e
			delete(l.Packs, old)
			moved = append(moved, filepath.Base(lockPath)+": "+old+" → "+to)
		}
		return len(moved) > 0, nil
	})
	return moved, err
}

// RenameForkLock moves each pin whose fork key rename respells (forks.lock.json), under the fork
// lock's lock. The pin's source is the fork's own declaration, which a pack rename does not
// change, so it is kept.
func RenameForkLock(storeDir, lockPath string, rename func(key string) (string, bool)) ([]string, error) {
	var moved []string
	err := WithForkLock(storeDir, lockPath, nil, func(l *ForkLock) (bool, error) {
		keys := make([]string, 0, len(l.Forks))
		for k := range l.Forks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			to, ok := rename(k)
			if !ok {
				continue
			}
			if _, taken := l.Forks[to]; taken {
				continue
			}
			e := l.Forks[k]
			e.Key = to
			l.Forks[to] = e
			delete(l.Forks, k)
			moved = append(moved, ForkLockName+": "+k+" → "+to)
		}
		return len(moved) > 0, nil
	})
	return moved, err
}

// RenameCheckRecords moves every check record whose owner rename respells to the new owner's
// file, owner fields respelled, under both owners' locks (taken in name order). A record already
// at the new owner is left, and so is the old one. The check-start ledger is not moved: its
// identity hashes the config bytes a migration changes anyway, so the new owner starts a new
// epoch, which only a fresh `--update` proof reads.
func (s *Store) RenameCheckRecords(rename func(owner string) (string, bool)) ([]string, error) {
	dir := filepath.Join(s.Dir, "checks")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "start-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var head struct {
			Owner string `json:"owner"`
		}
		if json.Unmarshal(data, &head) != nil || head.Owner == "" {
			continue
		}
		to, ok := rename(head.Owner)
		if !ok {
			continue
		}
		done, err := s.renameCheckRecord(head.Owner, to)
		if err != nil {
			return moved, err
		}
		if done {
			moved = append(moved, "check record: "+head.Owner+" → "+to)
		}
	}
	return moved, nil
}

func (s *Store) renameCheckRecord(from, to string) (bool, error) {
	owners := []string{from, to}
	sort.Strings(owners)
	for _, o := range owners {
		unlock, err := s.lockCheckRecord(o, nil)
		if err != nil {
			return false, err
		}
		defer unlock()
	}
	if _, err := os.Lstat(s.CheckRecordPath(to)); err == nil {
		return false, nil
	}
	r, err := s.LoadCheckRecord(from)
	if errors.Is(err, ErrNoCheckRecord) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	r.Owner = to
	if r.PatchFailure != nil && r.PatchFailure.Owner == from {
		r.PatchFailure.Owner = to
	}
	if err := s.saveCheckRecord(r); err != nil {
		return false, err
	}
	return true, os.Remove(s.CheckRecordPath(from))
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
