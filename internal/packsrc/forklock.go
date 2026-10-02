package packsrc

// forklock.go is the FORK LOCK, forks.lock.json (docs/design/forked-programs-as-packs.md FP-D7,
// FP-D18): the revision each fork's source is pinned to, keyed "<fork pack>/<bin>".
//
// # Why a file of its own, beside packs.lock.json
//
// A launch rewrites packs.lock.json whenever a fetched pack moves, and a yolo that predates forks
// decodes it into a Lock with no field for them — so its next rewrite would drop every fork pin.
// Bumping that file's schema instead would make the same older yolo refuse every pack. A second
// file costs nothing either way: an older yolo never reads it.
//
// # Who writes it
//
// A LAUNCH PINS a fork the lock does not name yet, or names for a source the manifest no longer
// declares (PinForks, forkpin.go; FP-D18, which supersedes FP-D7's read-only launch under the
// maintainer's OQ-PF1), and so does `yolo pack install`, which is no longer required. Only `yolo
// pack update` MOVES a pin: a launch leaves a standing pin where it is, however far its branch has
// moved, because re-resolving it at launch would be the rebuild on a timer §9 forbids.
//
// It carries the same discipline as the pack lock (lock.go): a newer schema is refused, a rewrite is
// a temp file and a rename, and writers are serialized by a flock in the pack store (WithForkLock).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ForkLockSchema is the fork lock's on-disk format version; a higher one is refused rather than
// misread, as LockSchema is.
const ForkLockSchema = 1

// ForkLockName is the fork lock's file name, beside the user config.
const ForkLockName = "forks.lock.json"

// ForkLockEntry is one fork's pin.
type ForkLockEntry struct {
	// Key is "<fork pack>/<bin>", and the map key in ForkLock.Forks.
	Key string `json:"key"`
	// Source is the fork's `source` AS WRITTEN when it was pinned, so a changed declaration reads
	// as drift rather than as the old commit answering for a new address.
	Source string `json:"source"`
	// Ref is the ref the source names, kept beside Commit so the pair reads "asked for main, got
	// <sha>".
	Ref string `json:"ref,omitempty"`
	// Commit is the full SHA the ref resolved to: what a build checks out, what keys its store
	// entry, and what a launch names (OQ-FP6).
	Commit string `json:"commit"`
}

// ForkLock is the whole fork lock.
type ForkLock struct {
	Schema int `json:"schema"`
	// Forks is keyed by ForkLockEntry.Key.
	Forks map[string]ForkLockEntry `json:"forks"`
}

// ForkLockPath returns the fork lock's path beside a user config path.
func ForkLockPath(userConfigPath string) string {
	return filepath.Join(filepath.Dir(userConfigPath), ForkLockName)
}

// LoadForkLock reads a fork lock. A missing file is an empty lock, not an error: it is the state
// before the first fork is pinned.
func LoadForkLock(path string) (*ForkLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ForkLock{Schema: ForkLockSchema, Forks: map[string]ForkLockEntry{}}, nil
		}
		return nil, err
	}
	var l ForkLock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %w (delete it: the next launch pins every fork again, at what its "+
			"ref names then)", path, err)
	}
	if l.Schema > ForkLockSchema {
		return nil, newerSchemaError(path, l.Schema, ForkLockSchema)
	}
	if l.Forks == nil {
		l.Forks = map[string]ForkLockEntry{}
	}
	return &l, nil
}

// Save writes the fork lock deterministically, through a temp file and a rename, so a reader (a
// launch, `yolo pack status`) never sees it half-written.
func (l *ForkLock) Save(path string) error {
	if l.Forks == nil {
		l.Forks = map[string]ForkLockEntry{}
	}
	l.Schema = ForkLockSchema
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+ForkLockName+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Get returns the pin for a key.
func (l *ForkLock) Get(key string) (ForkLockEntry, bool) {
	e, ok := l.Forks[key]
	return e, ok
}

// Set records a pin, replacing any previous one for its key.
func (l *ForkLock) Set(e ForkLockEntry) {
	if l.Forks == nil {
		l.Forks = map[string]ForkLockEntry{}
	}
	l.Forks[e.Key] = e
}

// Prune removes the pins whose key is not in keep and returns the removed keys, sorted — reported
// by the caller, because a pin vanishing means a fork left the selection.
func (l *ForkLock) Prune(keep []string) []string {
	want := map[string]bool{}
	for _, k := range keep {
		want[k] = true
	}
	var removed []string
	for k := range l.Forks {
		if !want[k] {
			removed = append(removed, k)
			delete(l.Forks, k)
		}
	}
	sort.Strings(removed)
	return removed
}

// WithForkLock is the fork lock's load-modify-save under an exclusive flock in the pack store
// (storeDir/locks), WithLock's discipline for the second file: fn reports whether it changed
// anything, and nothing is written when it did not.
func WithForkLock(storeDir, lockPath string, waiting func(string), fn func(*ForkLock) (bool, error)) error {
	unlock, err := flockPath(filepath.Join(storeDir, "locks", "forklock-"+mirrorSlug(lockPath)+".lock"),
		"the fork lock "+lockPath, waiting)
	if err != nil {
		return err
	}
	defer unlock()
	l, err := LoadForkLock(lockPath)
	if err != nil {
		return err
	}
	changed, err := fn(l)
	if err != nil || !changed {
		return err
	}
	return l.Save(lockPath)
}
