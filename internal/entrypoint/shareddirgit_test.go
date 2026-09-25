package entrypoint

// shareddirgit_test.go pins pi's SECOND shared store, `.pi-shared-git`
// (docs/design/pi-git-extension-caching.md): the shipped manifest's shared_directory hook from
// `.pi/agent/git`, driven through the same boot entry as the npm store's. Each cell goes red if
// packs/pi stops declaring the hook.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// piGitStoreHook is the shipped pi pack's shared_directory hook for its git checkouts, found by
// the store it names rather than by position, so a reorder cannot swap it for the npm store's.
func piGitStoreHook(t *testing.T) (*packload.Pack, packdecl.Hook) {
	t.Helper()
	p, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Decl.HookContributions() {
		if h.Name == HookSharedDirectory && h.SharedDir == ".pi-shared-git" {
			if h.File != ".pi/agent/git" {
				t.Fatalf("the git store's hook shares %q, want .pi/agent/git (pi's git install root)", h.File)
			}
			return p, h
		}
	}
	t.Fatal("packs/pi no longer shares .pi/agent/git through .pi-shared-git")
	return nil, packdecl.Hook{}
}

// TestPiGitCheckoutsAreSharedOnAFreshHome: a fresh home's ~/.pi/agent/git becomes a relative
// link into the machine store, so the first clone any workspace makes lands where every other
// workspace fetches instead of cloning.
func TestPiGitCheckoutsAreSharedOnAFreshHome(t *testing.T) {
	p, hook := piGitStoreHook(t)
	e, link, shared := sharedDirEnv(t, hook)
	runHooks(t, e, p)
	assertLinkedToShared(t, link, shared)
}

// TestPiGitCheckoutsSeedAnEmptyStoreFromAWorkspace: the day-one case — a workspace that already
// cloned its extensions keeps them, copied into the empty machine store (history, working tree
// and all), and its path becomes the link.
func TestPiGitCheckoutsSeedAnEmptyStoreFromAWorkspace(t *testing.T) {
	p, hook := piGitStoreHook(t)
	e, link, shared := sharedDirEnv(t, hook)
	repo := filepath.Join(link, "github.com", "someone", "pi-ext")
	for _, rel := range []string{".git/refs/heads", "src"} {
		if err := os.MkdirAll(filepath.Join(repo, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "index.ts"), []byte("// from-the-workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil { // what EnsureGlobalStorage leaves
		t.Fatal(err)
	}

	runHooks(t, e, p)

	assertLinkedToShared(t, link, shared)
	for _, rel := range []string{".git/HEAD", "src/index.ts"} {
		if _, err := os.Stat(filepath.Join(shared, "github.com", "someone", "pi-ext", filepath.FromSlash(rel))); err != nil {
			t.Errorf("the workspace's checkout did not reach the machine store (%s): %v", rel, err)
		}
	}
}
