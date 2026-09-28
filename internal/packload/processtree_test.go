package packload

// processtree_test.go pins the process pack tree (processtree.go): the directory a host verb
// stages its configured packs into for its own lifetime.

import (
	"os"
	"path/filepath"
	"testing"
)

// EVERY CALL IS A NEW DIRECTORY NAMED AS ASKED, inside ONE leased tree that ReleaseEmbedded
// deletes. The name is load-bearing (Pack.StagedSlug is the staged dir's base name), the lease is
// what lets a reaper tell a live tree from a dead one, and the release is the only thing that
// deletes it on the exits that already release the embedded packs.
func TestProcessPackDirIsLeasedAndGoesWithTheEmbeddedRelease(t *testing.T) {
	ReleaseEmbedded()
	t.Cleanup(ReleaseEmbedded)

	a, err := ProcessPackDir("mine")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ProcessPackDir("mine")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || filepath.Base(a) != "mine" || filepath.Base(b) != "mine" {
		t.Fatalf("ProcessPackDir = %s and %s, want two distinct dirs named mine", a, b)
	}
	root := ProcessTreeLocation()
	if root == "" || filepath.Dir(filepath.Dir(a)) != root {
		t.Fatalf("%s is not inside the process tree %q", a, root)
	}
	if state, unlock, err := ProbeLease(root); state != LeaseHeld {
		unlock()
		t.Fatalf("the process tree's lease is %v (%v), want held while the process runs", state, err)
	}

	ReleaseEmbedded()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("ReleaseEmbedded left the process tree %s behind (%v)", root, err)
	}
	if ProcessTreeLocation() != "" {
		t.Error("a released process tree is still reported")
	}
	// Released, not poisoned: the next caller gets a fresh tree.
	c, err := ProcessPackDir("mine")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(c)) == root {
		t.Errorf("a released tree was reused for %s", c)
	}
}

// A TREE DELETED UNDERNEATH THE PROCESS IS REPLACED, not handed out as a parent nothing leases —
// a test's TMPDIR cleanup or a manual rm must not make the next staging fail.
func TestProcessPackDirReplacesATreeDeletedUnderneathIt(t *testing.T) {
	ReleaseEmbedded()
	t.Cleanup(ReleaseEmbedded)
	if _, err := ProcessPackDir("x"); err != nil {
		t.Fatal(err)
	}
	gone := ProcessTreeLocation()
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	d, err := ProcessPackDir("x")
	if err != nil {
		t.Fatalf("staging after the tree vanished: %v", err)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		t.Fatalf("%s: %v", d, err)
	}
	if ProcessTreeLocation() == gone {
		t.Error("the vanished tree was not replaced")
	}
}
