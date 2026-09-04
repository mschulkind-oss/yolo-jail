package packsrc

// lock.go is the pack lockfile (C5).
//
// WHY A LOCKFILE AT ALL, given `ref` is already mandatory: a ref and a commit answer
// different questions. `?ref=main` records what you ASKED FOR; the lock records what
// you GOT. Without it, "the pack I reviewed" and "the pack running in my jail" are
// the same string and different content, and there is no way to notice — which is the
// specific failure mode that makes an unpinned dependency dangerous, only quieter.
//
// It also makes two operations possible that are otherwise guesswork: `rollback`
// needs a previous commit to go back TO, and reproducing a colleague's setup needs
// the commit, not the branch name.
//
// It lives BESIDE the user config (~/.config/yolo-jail/packs.lock.json) because packs
// are user-scope: a workspace cannot name one, so a repo-committed lock would describe
// something the repo cannot influence.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LockSchema is the on-disk format version. Bumped only for a breaking change; an
// unknown (higher) version is an error rather than a silent misread, because
// guessing at an unknown format is how a lockfile ends up describing the wrong pack.
const LockSchema = 1

// LockEntry is one pack's resolved state.
type LockEntry struct {
	// Name is the pack name, and the map key in Lock.Packs.
	Name string `json:"name"`
	// Source is the address as WRITTEN, so a diff shows when the request changed and
	// not just when the content did.
	Source string `json:"source"`
	// Commit is the full SHA the ref resolved to. Empty for a local pack — a
	// directory has no commit, and pretending otherwise would invent a pin.
	Commit string `json:"commit,omitempty"`
	// Ref is the ref as written, kept alongside Commit so the pair reads as
	// "asked for main, got <sha>".
	Ref string `json:"ref,omitempty"`

	// THERE IS NO APPROVAL RECORD HERE, and its absence is a ruling rather than an
	// omission. `ApprovedHostAccess` held the specific host-access claims a user said yes
	// to at `yolo pack install` ("mount X -> /ctx/Y", "reads-host Z", a loophole's daemon
	// argv), and `HostAccessApproved` was the launch gate's superset check over it. Both
	// were deleted on 2026-09-04 with the prompt that wrote them
	// (docs/design/trust-paths.md OQ-TP9): selecting a pack means writing `packs` in the
	// user config as the host user, which is strictly more authority than the gate
	// withheld, so it refused an actor who had already passed a stronger one
	// (gate-placement-principle.md Test 1). An `ApprovedAt` alongside it had gone the same
	// way earlier, for the narrower version of the same reason — it was written by every
	// install and read by nothing.
	//
	// DO NOT REINTRODUCE EITHER WITHOUT A DESIGN RULING. A persisted field in a TRUST file
	// is not read as documentation; it is read as a fact about the system, so a field
	// asserting an approval nothing enforces is worse than no field. Removing them costs no
	// compatibility: decoding does not reject unknown keys, so an existing lockfile keeps a
	// stray "approvedHostAccess"/"approvedAt" that nothing reads.
}

// Lock is the whole lockfile.
type Lock struct {
	Schema int `json:"schema"`
	// Packs is keyed by pack name. A map rather than a list so a rewrite is
	// order-insensitive and two people adding different packs do not conflict on
	// list position.
	Packs map[string]LockEntry `json:"packs"`
}

// LockPath returns the lockfile path beside a user config path.
func LockPath(userConfigPath string) string {
	return filepath.Join(filepath.Dir(userConfigPath), "packs.lock.json")
}

// LoadLock reads a lockfile. A MISSING file is not an error — it is the normal state
// before the first install, and returning an empty lock lets every caller treat
// "nothing locked yet" as ordinary.
func LoadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Lock{Schema: LockSchema, Packs: map[string]LockEntry{}}, nil
		}
		return nil, err
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %w (delete it to start over — it will be "+
			"regenerated on the next `yolo pack install`)", path, err)
	}
	if l.Schema > LockSchema {
		return nil, fmt.Errorf("%s: schema %d is newer than this yolo understands (%d) — "+
			"upgrade yolo rather than letting it misread the file", path, l.Schema, LockSchema)
	}
	if l.Packs == nil {
		l.Packs = map[string]LockEntry{}
	}
	return &l, nil
}

// Save writes the lockfile deterministically.
//
// Sorted keys and a trailing newline: this file is diffed by humans and may be
// committed to a dotfiles repo, so a rewrite that reorders keys would produce noise
// that hides the one line that actually changed.
func (l *Lock) Save(path string) error {
	if l.Packs == nil {
		l.Packs = map[string]LockEntry{}
	}
	l.Schema = LockSchema
	// json.Marshal sorts map keys already; MarshalIndent keeps it readable.
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Set records a resolution, replacing any previous entry for that name.
func (l *Lock) Set(e LockEntry) {
	if l.Packs == nil {
		l.Packs = map[string]LockEntry{}
	}
	l.Packs[e.Name] = e
}

// Get returns the locked entry for a name.
func (l *Lock) Get(name string) (LockEntry, bool) {
	e, ok := l.Packs[name]
	return e, ok
}

// Prune removes entries whose pack is no longer configured, and returns the removed
// names sorted.
//
// Reported rather than silent: a lock entry vanishing is worth seeing, because it
// means a pack left the config and its content is about to stop being delivered.
func (l *Lock) Prune(configured []string) []string {
	keep := map[string]bool{}
	for _, n := range configured {
		keep[n] = true
	}
	var removed []string
	for name := range l.Packs {
		if !keep[name] {
			removed = append(removed, name)
			delete(l.Packs, name)
		}
	}
	sort.Strings(removed)
	return removed
}

// Drift describes a locked pack whose CONFIGURED address no longer matches what was
// locked — the user edited the source or ref, so the lock is stale.
type Drift struct {
	Name         string
	LockedSource string
	WantedSource string
}

// DriftFrom reports the configured packs whose address differs from the lock.
//
// This is what makes a stale lock visible instead of silently ignored: launch resolves
// from the LOCK, so an edited `ref` in config would otherwise appear to do nothing
// until someone ran install — the single most confusing possible behavior.
func (l *Lock) DriftFrom(configured map[string]string) []Drift {
	var out []Drift
	names := make([]string, 0, len(configured))
	for n := range configured {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		locked, ok := l.Packs[name]
		if !ok {
			continue // not locked yet: install handles it, that is not drift
		}
		if locked.Source != configured[name] {
			out = append(out, Drift{
				Name: name, LockedSource: locked.Source, WantedSource: configured[name],
			})
		}
	}
	return out
}
