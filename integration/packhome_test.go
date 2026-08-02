package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPackHomeSharesHostStores is the regression test for the bug that took out
// both Linux integration jobs (and the macOS nightly) for many commits: ~15 tests
// failing, none of them reproducible on a developer machine.
//
// packHome redirects HOME to a temp dir so a test can supply its own user config.
// Everything that hangs off HOME follows it, so packHome re-links the HOME-rooted
// STORES back to the real home's copies. Two properties of that re-link are load-
// bearing, and CI broke on both:
//
//   - The link target must EXIST. A symlink to a not-yet-created directory is
//     DANGLING, and os.MkdirAll — which storage.EnsureGlobalStorage calls on
//     $HOME/.local/share/yolo-jail at the top of every single run — reports a
//     dangling symlink as "mkdir <path>: file exists" and returns an error rather
//     than following it. On a developer machine the real store already exists so
//     the link resolves and nothing is wrong; on a FRESH CI runner nothing has
//     created ~/.local/share/yolo-jail yet, so every packHome test that ran before
//     the first real-HOME test died in setup with that baffling message.
//
//   - The set of re-linked stores must be COMPLETE. Rootless podman keeps its
//     graphroot at $HOME/.local/share/containers/storage, so a redirected HOME also
//     redirects the IMAGE STORE: the child re-loaded the whole jail image into a
//     throwaway store inside the t.TempDir(), and the image's overlay diffs contain
//     read-only nix-store trees that t.TempDir()'s own RemoveAll cleanup cannot
//     unlink — "TempDir RemoveAll cleanup: ... permission denied", which fails the
//     test AFTER its assertions have already passed. This is invisible in yolo's own
//     jail, where podman runs as root with a graphroot at /var/lib/containers/storage
//     that no HOME redirect can move.
//
// It runs under -short (no container): this is a harness invariant, like
// TestChildRepoRootEnv, so `just test-fast` and the check-go CI job enforce it.
func TestPackHomeSharesHostStores(t *testing.T) {
	// A "host home" that has never run yolo — the fresh-CI-runner condition, and
	// the one a developer machine can never be in.
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)

	packHome(t, `{"packs": ["claude"]}`)

	tmpHome := os.Getenv("HOME")
	if tmpHome == realHome {
		t.Fatalf("packHome did not redirect HOME (still %s)", realHome)
	}

	// The config packHome exists to deliver must be in the redirected home.
	if _, err := os.Stat(filepath.Join(tmpHome, ".config", "yolo-jail", "config.jsonc")); err != nil {
		t.Errorf("user config missing from the redirected home: %v", err)
	}

	for _, store := range packHomeSharedStores {
		link := filepath.Join(tmpHome, filepath.FromSlash(store))

		if fi, err := os.Lstat(link); err != nil {
			t.Errorf("%s: not created in the redirected home: %v", store, err)
			continue
		} else if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s: want a symlink to the host store, got mode %v", store, fi.Mode())
			continue
		}

		// os.Stat FOLLOWS the link, so this is exactly the check MkdirAll makes.
		// A dangling link fails here and reproduces the CI "file exists" failure.
		if fi, err := os.Stat(link); err != nil {
			t.Errorf("%s: link is DANGLING, so MkdirAll on it fails with "+
				"\"file exists\" — the target must be created first: %v", store, err)
			continue
		} else if !fi.IsDir() {
			t.Errorf("%s: resolves to a non-directory (%v)", store, fi.Mode())
			continue
		}

		// And it must resolve to the HOST home's store, not somewhere inside the
		// throwaway home — otherwise the redirect it exists to undo is still in
		// effect.
		got, err := filepath.EvalSymlinks(link)
		if err != nil {
			t.Errorf("%s: resolving link: %v", store, err)
			continue
		}
		want, err := filepath.EvalSymlinks(filepath.Join(realHome, filepath.FromSlash(store)))
		if err != nil {
			t.Errorf("%s: resolving host store: %v", store, err)
			continue
		}
		if got != want {
			t.Errorf("%s: resolves to %s, want the host store %s", store, got, want)
		}
	}
}
