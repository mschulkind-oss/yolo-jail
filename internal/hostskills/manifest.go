package hostskills

// manifest.go is the provenance record for TIER B deliveries: which entries in a real
// skills dir yolo wrote, so a later apply can update or retire its own output without ever
// touching the user's.
//
// Tier A does not need this — a per-pack subtree with a marked manifest answers "is this
// mine?" from the path itself (tier.go). Tier B has no namespace, so yolo's entries sit
// directly beside hand-written ones and are indistinguishable by inspection. The
// alternative to a record is to never remove anything and never overwrite anything, which
// means a pack can add a skill but never fix or retire one.
//
// The record is deliberately WEAK evidence, and the code treats it that way. It can go
// stale in ordinary use: the user edits a file yolo wrote, the state dir is pruned, two
// machines share one config, an apply is interrupted between write and record. So it
// authorizes ARCHIVING (a reversible move, archive.go) rather than deletion, and a
// destination entry absent from the record is always treated as the user's.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Manifest records what yolo wrote into real skills dirs, keyed by destination so two packs
// writing into one dir stay distinguishable.
type Manifest struct {
	// Entries maps an absolute destination path to the pack that wrote it. Absolute
	// because one pack may deliver to several tools' skills dirs, and a bare skill name
	// would collide across them.
	Entries map[string]string `json:"entries"`
}

// LoadManifest reads the record at path. A missing file is an EMPTY manifest, not an error:
// the first apply on a machine has nothing recorded, and that is indistinguishable from a
// pruned state dir — both mean "yolo can prove nothing", which the callers already handle
// as "treat everything as the user's".
func LoadManifest(path string) (*Manifest, error) {
	m := &Manifest{Entries: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	if err := json.Unmarshal(data, m); err != nil {
		// A corrupt record must not silently grant OR silently deny. Report it; the caller
		// degrades to "prove nothing" (fail-closed: user content is never touched).
		return &Manifest{Entries: map[string]string{}}, err
	}
	if m.Entries == nil {
		m.Entries = map[string]string{}
	}
	return m, nil
}

// Save writes the record atomically-ish (temp file + rename), so an interrupted write
// leaves the previous record intact rather than a truncated one. A truncated record reads
// as "prove nothing", which is safe but loses the ability to retire yolo's own entries.
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// OwnedBy reports whether yolo recorded dest as written by pack.
func (m *Manifest) OwnedBy(dest, pack string) bool {
	owner, ok := m.Entries[dest]
	return ok && owner == pack
}

// Owner returns the pack that wrote dest, and false when nothing did (i.e. it is the
// user's, or predates the record).
func (m *Manifest) Owner(dest string) (string, bool) {
	owner, ok := m.Entries[dest]
	return owner, ok
}

// Record marks dest as written by pack.
func (m *Manifest) Record(dest, pack string) {
	if m.Entries == nil {
		m.Entries = map[string]string{}
	}
	m.Entries[dest] = pack
}

// Forget drops dest from the record, after its content has been archived or removed.
func (m *Manifest) Forget(dest string) { delete(m.Entries, dest) }

// EntriesUnder returns every recorded destination that lives directly under dir, sorted for
// deterministic output. This is how a COMPOSITION finds its stale entries: what the record says yolo
// put in this directory last time, minus what the composition ships now, is exactly the set to
// retire.
//
// Owner-AGNOSTIC, deliberately. Under composition a name changes composer between applies (that is
// what "later wins" means), so filtering by pack would leave an entry the previous apply's pack no
// longer ships and the current one never claimed sitting in the home forever — owned by a name
// nothing asks about. The owner is still recorded, for the dropped-pack question; it is just not the
// key to this one.
//
// DIRECTLY under, not anywhere beneath: a namespaced subtree is recorded at the subtree, and its
// leaves are not recorded at all, so a recursive reading would find nothing extra and a `..`-style
// containment test would wrongly claim a sibling destination's entries when one skills dir nests
// inside another (`.pi/agent/skills` under a hypothetical `.pi/skills`).
func (m *Manifest) EntriesUnder(dir string) []string {
	var out []string
	for dest := range m.Entries {
		if filepath.Dir(dest) != filepath.Clean(dir) {
			continue
		}
		out = append(out, dest)
	}
	sort.Strings(out)
	return out
}
