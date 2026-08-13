package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadJailPacks used to swallow EVERY os.ReadDir error, so "the pack root is not there"
// and "the pack root is there but this process cannot read it" produced the same answer:
// no packs, no error, a boot that renders zero surfaces and reports success.
//
// That equivalence is the shape of B-0 (a macos-user launch was handed no pack root at
// all and rendered nothing, silently) and it is the shape any FUTURE staging mistake will
// take on a non-container backend, where the tree is a copied directory rather than a
// bind mount the runtime would have refused to start without. So the two cases are now
// split: ABSENT stays a legitimate empty render, anything else is A12-fatal.

// TestLoadJailPacksFailsLoudlyOnAnUnusablePackRoot: a YOLO_PACK_ROOT that is not a
// directory must be an error, not an empty pack set.
func TestLoadJailPacksFailsLoudlyOnAnUnusablePackRoot(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "packs")
	if err := os.WriteFile(notADir, []byte("this is not a pack tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": dir, "YOLO_PACK_ROOT": notADir})
	packs, err := LoadJailPacks(e)
	if err == nil {
		t.Fatalf("LoadJailPacks returned %d packs and no error for a pack root that "+
			"cannot be read; the boot would render nothing and report success", len(packs))
	}
	if packs != nil {
		t.Errorf("LoadJailPacks returned packs alongside the error: %v", packs)
	}
}

// TestLoadJailPacksTreatsAnAbsentRootAsEmpty: the other side of the split, which must NOT
// have changed. A launch whose staging produced an empty tree (no packs configured) has no
// _official subdir, and that is an ordinary empty render — making it fatal would refuse to
// start a jail whose only property is that the user configured no packs.
func TestLoadJailPacksTreatsAnAbsentRootAsEmpty(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "packs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": dir, "YOLO_PACK_ROOT": root})
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("an empty (but present) pack root must render nothing, not fail: %v", err)
	}
	if len(packs) != 0 {
		t.Errorf("empty pack root yielded %d packs", len(packs))
	}

	// And a root that does not exist at all, which is what an unset mount looks like.
	e2 := NewEnv(map[string]string{"JAIL_HOME": dir, "YOLO_PACK_ROOT": filepath.Join(dir, "nope")})
	if packs, err := LoadJailPacks(e2); err != nil || len(packs) != 0 {
		t.Errorf("absent pack root: got %d packs, err %v; want 0, nil", len(packs), err)
	}
}
