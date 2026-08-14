package packstage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A missing record is EMPTY, not an error: the first launch on a machine has nothing
// recorded, and that reads identically to a pruned state dir.
func TestLoadLoopholeOwnersMissingFileIsEmpty(t *testing.T) {
	rec, err := LoadLoopholeOwners(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing record must not be an error: %v", err)
	}
	if len(rec.Owners) != 0 {
		t.Errorf("missing record loaded %v, want empty", rec.Owners)
	}
	// Usable without a nil-map panic: the caller writes into it immediately.
	rec.Owners["acme-proxy"] = "acme"
}

// A CORRUPT record must not silently grant OR silently deny: it comes back empty (so
// nothing is retired) AND with the error (so the caller can say why).
func TestLoadLoopholeOwnersCorruptIsEmptyPlusError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owners.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadLoopholeOwners(path)
	if err == nil {
		t.Error("a corrupt record must be reported")
	}
	if len(rec.Owners) != 0 {
		t.Errorf("a corrupt record authorized %v; it must prove nothing", rec.Owners)
	}
}

func TestLoopholeOwnersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "owners.json")
	rec := &LoopholeOwners{Owners: map[string]string{"acme-proxy": "acme", "beta": "beta-pack"}}
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLoopholeOwners(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owners["acme-proxy"] != "acme" || got.Owners["beta"] != "beta-pack" {
		t.Errorf("round trip lost data: %v", got.Owners)
	}
	// The temp file must not survive as a second, stale record.
	if _, err := os.Lstat(path + ".tmp"); err == nil {
		t.Error("Save left its temp file behind")
	}
}

// Departed is keyed on the CONFIGURED pack set, and a pack still in the config keeps its
// loophole state — including when the pack failed to resolve this launch. That is the case
// that rules out comparing against the loaded set: an offline launch would otherwise
// archive a CA every time.
func TestDepartedOnlyReportsUnconfiguredPacks(t *testing.T) {
	rec := &LoopholeOwners{Owners: map[string]string{
		"acme-proxy": "acme",
		"zeta-tap":   "zeta",
		"beta-tap":   "beta",
	}}
	got := rec.Departed(map[string]bool{"acme": true})
	if len(got) != 2 {
		t.Fatalf("Departed = %v, want the two unconfigured packs' loopholes", got)
	}
	// Sorted by loophole name, so a report and a test read the same order every run.
	if got[0].Loophole != "beta-tap" || got[1].Loophole != "zeta-tap" {
		t.Errorf("Departed is not sorted by loophole name: %v", got)
	}
	if got[0].Pack != "beta" {
		t.Errorf("Departed lost the attribution: %v", got[0])
	}
	for _, d := range got {
		if d.Loophole == "acme-proxy" {
			t.Error("a still-configured pack's loophole was reported as departed")
		}
	}
}

// An EMPTY configured set means every recorded loophole is departed — which is correct, and
// is exactly why the launch-path caller must refuse an UNKNOWN set rather than passing an
// empty one (see TestStagePacksRefusesRetirementWithUnknownPackSet).
func TestDepartedWithNoConfiguredPacksReportsAll(t *testing.T) {
	rec := &LoopholeOwners{Owners: map[string]string{"a": "pa", "b": "pb"}}
	if got := rec.Departed(map[string]bool{}); len(got) != 2 {
		t.Errorf("Departed = %v, want both", got)
	}
}

// RETIREMENT ARCHIVES, NEVER DELETES — the load-bearing property. A pack-shipped
// intercepting loophole's state dir holds a CA PRIVATE KEY; the record authorizing removal
// is weak evidence, so being wrong must cost one `mv` back.
func TestRetireLoopholeStateArchivesRatherThanDeleting(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	logDir := filepath.Join(root, "logs")
	key := filepath.Join(stateRoot, "acme-proxy", "ca.key")
	if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "host-service-acme-proxy.log")
	if err := os.WriteFile(logPath, []byte("started\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stamp := ArchiveStamp(time.Unix(1700000000, 0).UTC())
	gen, moved, err := RetireLoopholeState(RetireRequest{
		Loophole: "acme-proxy", Pack: "acme", StateRoot: stateRoot, LogDir: logDir, Stamp: stamp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Errorf("moved = %v, want the state dir and the log", moved)
	}
	// The original is gone from the live tree...
	if _, err := os.Lstat(filepath.Join(stateRoot, "acme-proxy")); !os.IsNotExist(err) {
		t.Error("the live state dir survived retirement")
	}
	if _, err := os.Lstat(logPath); !os.IsNotExist(err) {
		t.Error("the live host-service log survived retirement")
	}
	// ...and every byte is recoverable from the archive.
	archived := filepath.Join(gen, "state", "ca.key")
	data, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("the CA key is not recoverable from the archive: %v", err)
	}
	if string(data) != "PRIVATE KEY" {
		t.Errorf("archived key = %q", data)
	}
	// The 0600 mode must survive: an archived key at umask-default 0644 is a new problem
	// created by the fix.
	if st, err := os.Lstat(archived); err == nil && st.Mode().Perm() != 0o600 {
		t.Errorf("archived key mode = %#o, want 0600", st.Mode().Perm())
	}
	if _, err := os.Lstat(filepath.Join(gen, "host-service-acme-proxy.log")); err != nil {
		t.Errorf("the log is not in the archive: %v", err)
	}
	// Attribution lives IN the archive, because the record that named the pack is about to
	// forget it.
	marker, err := os.ReadFile(filepath.Join(gen, ".pack"))
	if err != nil || !strings.Contains(string(marker), "acme") {
		t.Errorf(".pack marker = %q, %v; the archive must name its owning pack", marker, err)
	}
	// Under the STATE root, not the host-render archive: §4.5 measured that `yolo prune`'s
	// archive sweep walks a different tree, and a user who lost a CA looks here.
	if !strings.HasPrefix(gen, filepath.Join(stateRoot, RetiredLoopholeStateDir)) {
		t.Errorf("generation %q is not under the state root's retired dir", gen)
	}
}

// A loophole that never ran has nothing on disk: retirement is a silent no-op rather than an
// empty generation dir the user has to wonder about.
func TestRetireLoopholeStateNoStateIsANoOp(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	gen, moved, err := RetireLoopholeState(RetireRequest{
		Loophole: "never-ran", Pack: "acme", StateRoot: stateRoot, Stamp: "20260814-000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen != "" || moved != nil {
		t.Errorf("empty retirement produced gen=%q moved=%v", gen, moved)
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, RetiredLoopholeStateDir)); err == nil {
		t.Error("a no-op retirement created a retired dir")
	}
}

// THE STAMP MUST BE PRUNABLE. internal/prune deletes only a generation whose name it can
// parse as its own stamp format; a generation written in any other shape is reported as
// "none" and grows forever. This is the contract between the two files.
func TestRetiredStampIsPrunable(t *testing.T) {
	stamp := ArchiveStamp(time.Unix(1700000000, 0).UTC())
	if _, err := time.Parse("20060102-150405", stamp); err != nil {
		t.Errorf("stamp %q is not the format internal/prune parses: %v", stamp, err)
	}
}

// The retired dir is DOT-PREFIXED, which is what makes a collision with a real loophole
// unrepresentable: loophole discovery skips dot-children and a manifest's name must equal its
// directory basename, so nothing can be called ".retired".
func TestRetiredDirIsHiddenSoItIsNeverDiscovered(t *testing.T) {
	if !strings.HasPrefix(RetiredLoopholeStateDir, ".") {
		t.Errorf("RetiredLoopholeStateDir = %q must start with '.' or loophole discovery "+
			"would walk it as a loophole", RetiredLoopholeStateDir)
	}
}
