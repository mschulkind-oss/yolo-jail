package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// currentimages_test.go pins the retention SET — which images survive a sweep
// and on what evidence. It replaces probes_test.go's
// TestProtectedImageTagsReadsTheLoadSentinel, which pinned the same property
// against the load sentinel's LRU-10 before OQ-LS3 moved the evidence
// (docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2).
//
// What PruneOldImages does with the set is probes_test.go's; what the LAUNCH
// writes into it is internal/cli/run/currentimage_test.go's.

// TestCurrentImageTagsKeysTagsTheWayAnImageRefIsKeyed pins the SHARED
// DERIVATION, which is the one thing a second implementation here would break
// silently: the tag a pointer protects has to be the tag the loaded image
// actually wears, and that tag is put there by image.JailImageRef. A private
// hash in this package would compare two values that merely look alike, and the
// symptom would be a retention set that protects nothing while reporting
// known=true — the worst available failure, because it looks like evidence.
func TestCurrentImageTagsKeysTagsTheWayAnImageRefIsKeyed(t *testing.T) {
	buildDir := t.TempDir()
	const storePath = "/nix/store/aaaa-current-image"
	mustPointAt(t, buildDir, "yolo-a-aaaaaaaa", storePath)

	tags, known := CurrentImageTags(buildDir)
	if !known {
		t.Fatal("one honourable pointer must report known=true")
	}
	// The half of the ref after the LAST colon, taken from the real ref builder
	// rather than restated: `podman images` rows are what PruneOldImages compares
	// against, and it compares tags because the runtimes spell the repository
	// differently.
	wantTag := tagOf(image.JailImageRef("podman", storePath))
	if _, ok := tags[wantTag]; !ok {
		t.Errorf("the pointer's image is not protected: tag %q missing from %v", wantTag, tags)
	}
	if wantTag != image.ImageStoreKey(storePath) {
		t.Errorf("JailImageRef's tag %q and ImageStoreKey %q disagree — the pointer set "+
			"can only match rows if these are one derivation", wantTag, image.ImageStoreKey(storePath))
	}
	// The degraded fallback's only handle (SkipBuild / opted-past build failure
	// has no store path to hash), so it is protected unconditionally.
	if _, ok := tags[tagOf(paths.JailImage)]; !ok {
		t.Errorf("the legacy tag is not protected: %v", tags)
	}
	// A path no workspace points at must NOT be protected, or retention degrades
	// into "never remove anything" and the whole pass stops reclaiming.
	if _, ok := tags[image.ImageStoreKey("/nix/store/bbbb-nobody-points-here")]; ok {
		t.Error("an unpointed store path's tag was protected")
	}
}

// TestCurrentImageTagsIsOnePointerPerWorkspace is the ruling itself: the unit is
// the CONFIGURATION, one pointer per workspace, and the union of them is the
// retention rule. Three workspaces, three tags — with no count anywhere that
// could evict the fourth-most-recent.
func TestCurrentImageTagsIsOnePointerPerWorkspace(t *testing.T) {
	buildDir := t.TempDir()
	paths3 := []string{"/nix/store/a-ws", "/nix/store/b-ws", "/nix/store/c-ws"}
	for i, p := range paths3 {
		mustPointAt(t, buildDir, "yolo-ws"+string(rune('a'+i))+"-0000000"+string(rune('0'+i)), p)
	}
	tags, known := CurrentImageTags(buildDir)
	if !known {
		t.Fatal("three pointers must report known=true")
	}
	for _, p := range paths3 {
		if _, ok := tags[image.ImageStoreKey(p)]; !ok {
			t.Errorf("workspace pointing at %s is not protected: %v", p, tags)
		}
	}
	// The legacy tag plus one per workspace, and nothing else: a count that crept
	// back in would show up here as a missing entry.
	if len(tags) != len(paths3)+1 {
		t.Errorf("tags = %v, want exactly one per workspace plus the legacy tag", tags)
	}
}

// TestRecordCurrentImageReplacesRatherThanAccumulates is "there is no undo
// buffer", as a property of the file rather than of a comment. A workspace that
// evolves forward stops protecting what it evolved away from, in the same write.
//
// DELETE THE REPLACEMENT (append instead of overwrite, or key the file by store
// path instead of by workspace) AND THIS FAILS: the old tag stays protected and
// the machine keeps a superseded copy per launch, for ever.
func TestRecordCurrentImageReplacesRatherThanAccumulates(t *testing.T) {
	buildDir := t.TempDir()
	ws := t.TempDir()
	const before = "/nix/store/1111-before"
	const after = "/nix/store/2222-after"
	for _, p := range []string{before, after} {
		if err := RecordCurrentImage(buildDir, "yolo-ws-deadbeef", ws, p); err != nil {
			t.Fatal(err)
		}
	}
	ptrs, known := ReadCurrentImagePointers(buildDir)
	if !known || len(ptrs) != 1 {
		t.Fatalf("pointers = %v (known=%v), want exactly one per workspace", ptrs, known)
	}
	if ptrs[0].StorePath != after {
		t.Errorf("pointer names %q, want the image the latest launch used (%q)",
			ptrs[0].StorePath, after)
	}
	tags, _ := CurrentImageTags(buildDir)
	if _, ok := tags[image.ImageStoreKey(after)]; !ok {
		t.Error("the current image is not protected")
	}
	if _, ok := tags[image.ImageStoreKey(before)]; ok {
		t.Error("the SUPERSEDED image is still protected — that is the undo buffer " +
			"OQ-LS3 removed, back again")
	}
}

// TestCurrentImageTagsDeclinesWithoutEvidence pins the tri-state, in each of the
// three shapes a real machine produces it in. known=false is what makes
// PruneOldImages decline, so every one of these is a state in which nothing is
// reaped and the command says why (OQ-LS2).
func TestCurrentImageTagsDeclinesWithoutEvidence(t *testing.T) {
	t.Run("no pointer directory at all — the day OQ-LS3 ships", func(t *testing.T) {
		tags, known := CurrentImageTags(t.TempDir())
		if known {
			t.Error("an absent pointer dir must report known=false")
		}
		// It still returns the legacy tag, which is exactly why the boolean has to
		// carry the signal: a caller reading only the map gets a non-empty one.
		if len(tags) == 0 {
			t.Error("expected the legacy tag even with no evidence")
		}
	})

	t.Run("an empty pointer directory", func(t *testing.T) {
		buildDir := t.TempDir()
		if err := os.MkdirAll(CurrentImagesDir(buildDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, known := CurrentImageTags(buildDir); known {
			t.Error("an empty pointer dir must report known=false — every pointer file " +
				"having been deleted is not the same as no workspace wanting anything")
		}
	})

	t.Run("only pointers whose workspace is gone", func(t *testing.T) {
		buildDir := t.TempDir()
		gone := filepath.Join(t.TempDir(), "deleted-workspace")
		if err := RecordCurrentImage(buildDir, "yolo-gone-deadbeef", gone, "/nix/store/x-img"); err != nil {
			t.Fatal(err)
		}
		tags, known := CurrentImageTags(buildDir)
		if known {
			t.Error("a pointer whose workspace no longer exists is not evidence: a deleted " +
				"workspace has no configuration, so there is nothing to keep for it")
		}
		if _, ok := tags[image.ImageStoreKey("/nix/store/x-img")]; ok {
			t.Error("a dead workspace's image was protected")
		}
		// The FILE is deliberately left in place: a workspace can be temporarily
		// absent (a detached volume), the file is ~100 bytes, and an image removed
		// under a returning workspace costs one re-stream from a closure OQ-LS1's
		// GC-root policy still holds for a week.
		if _, err := os.Stat(filepath.Join(CurrentImagesDir(buildDir), "yolo-gone-deadbeef")); err != nil {
			t.Errorf("the pointer file was reaped: %v — reading it is not licence to delete it", err)
		}
	})
}

// TestReadCurrentImagePointersIgnoresHalfWrittenFiles: os.WriteFile truncates
// before it writes, so a full disk — precisely the condition that makes someone
// reclaim — can leave a pointer with one line or none. Such a file must read as
// "no pointer for this workspace" rather than as a pointer at a path nobody
// asked for, and it must not take the whole directory's readability with it.
func TestReadCurrentImagePointersIgnoresHalfWrittenFiles(t *testing.T) {
	buildDir := t.TempDir()
	mustPointAt(t, buildDir, "yolo-good-aaaaaaaa", "/nix/store/good-img")
	dir := CurrentImagesDir(buildDir)
	if err := os.WriteFile(filepath.Join(dir, "yolo-torn-bbbbbbbb"),
		[]byte("/nix/store/torn-img\n"), 0o644); err != nil { // store path, no workspace
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo-empty-cccccccc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	ptrs, known := ReadCurrentImagePointers(buildDir)
	if !known {
		t.Fatal("one torn file must not make the directory unreadable — that would " +
			"decline the whole pass on one workspace's bad luck")
	}
	if len(ptrs) != 1 || ptrs[0].Container != "yolo-good-aaaaaaaa" {
		t.Fatalf("pointers = %v, want only the complete one", ptrs)
	}
	tags, _ := CurrentImageTags(buildDir)
	if _, ok := tags[image.ImageStoreKey("/nix/store/torn-img")]; ok {
		t.Error("a half-written pointer protected an image")
	}
}

// TestRecordCurrentImageWritesNothingIncomplete: a pointer missing any of its
// three parts cannot be read back, so writing one would only occupy the name a
// real pointer needs. The degraded launch is the live case — AutoLoadImage's
// SkipBuild / build-failure fallback has no store path at all.
func TestRecordCurrentImageWritesNothingIncomplete(t *testing.T) {
	buildDir := t.TempDir()
	for _, tc := range []struct{ name, container, workspace, storePath string }{
		{"no store path (a degraded launch)", "yolo-a-aaaaaaaa", t.TempDir(), ""},
		{"no container name", "", t.TempDir(), "/nix/store/x"},
		{"no workspace", "yolo-a-aaaaaaaa", "", "/nix/store/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := RecordCurrentImage(buildDir, tc.container, tc.workspace, tc.storePath); err != nil {
				t.Fatalf("must not be an error the caller has to handle: %v", err)
			}
			if ptrs, _ := ReadCurrentImagePointers(buildDir); len(ptrs) != 0 {
				t.Errorf("wrote %v", ptrs)
			}
		})
	}
}

// TestCurrentImagePointersLiveUnderTheBuildDir pins WHERE, because the launch
// path and the reaper are different processes agreeing on a path by
// construction: both call the functions here. A divergence would be silent in
// one direction only — the reaper finds no pointers, declines for ever, and disk
// stops being reclaimed — so the location is worth one assertion.
func TestCurrentImagePointersLiveUnderTheBuildDir(t *testing.T) {
	dir := CurrentImagesDir("/tmp/build")
	if !strings.HasPrefix(dir, "/tmp/build"+string(os.PathSeparator)) {
		t.Errorf("CurrentImagesDir(%q) = %q, want it under the build dir it was given",
			"/tmp/build", dir)
	}
	// A SIBLING of the load sentinel and the GC roots, not inside either: they are
	// read on the same pass but written by different rules, and the sentinel's
	// last-writer-wins append is exactly what must not be able to lose a
	// workspace's pointer.
	if strings.Contains(dir, "roots") || strings.Contains(dir, "last-load") {
		t.Errorf("CurrentImagesDir = %q, want a directory of its own", dir)
	}
}
