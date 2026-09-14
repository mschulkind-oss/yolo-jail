package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// resolvedTempDir is t.TempDir() with every symlink in the path resolved.
//
// THE RULE IT GIVES A HOME TO, which three fixtures in this package each found
// separately: on macOS `t.TempDir()` returns a path under `/var/folders/…`, and `/var`
// is a symlink to `/private/var`. Anything downstream that resolves symlinks — or, worse,
// REFUSES them — sees a different path than the fixture handed out, and passes on Linux
// either way (AGENTS.md, "the darwin PATH-RESOLUTION class").
//
// ⚠ RESOLVE WHERE THE PATH IS MINTED, not at each comparison. That is the whole reason
// this is a fixture and not a helper called at the assertion: a comparison site that
// remembers to resolve fixes one test, and the next comparison added forgets. It is also
// why fixing `isolateHome` on 2026-09-14 did NOT fix the builder-offload test in the same
// run — that test isolates a home AND mints a second directory for `nix --store`, and
// only the first had the rule. nix refuses the second outright:
//
//	error: the path "/var" is a symlink; this is not allowed for the Nix store
//	and its parent directories
//
// (Measured on the macOS nightly, run 34862784409 shard 6 — a 642-second timeout whose
// cause was one line at the bottom of it.)
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test temp dir: %v", err)
	}
	return dir
}

// THE DARWIN CLASS, REPRODUCED ON LINUX — AGENTS.md gives the one-line recipe and this is
// it as a test, so the fixture above is covered on every push rather than once a night on
// hardware none of us is looking at.
//
// It deliberately does NOT call requireJail: there is no container in it, so it runs under
// `-short` too, which is where the class would otherwise be invisible.
func TestResolvedTempDirIsFreeOfSymlinksWhereTempDirIsNot(t *testing.T) {
	// A symlinked TMPDIR is exactly the shape macOS ships by default.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here (%v); the class is unreproducible on this filesystem", err)
	}
	t.Setenv("TMPDIR", link)

	t.Run("t.TempDir is NOT resolved", func(t *testing.T) {
		raw := t.TempDir()
		got, err := filepath.EvalSymlinks(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got == raw {
			t.Skipf("t.TempDir() came back already resolved (%s), so this platform does not "+
				"exhibit the class and the assertion below would prove nothing", raw)
		}
	})

	t.Run("resolvedTempDir is", func(t *testing.T) {
		got := resolvedTempDir(t)
		again, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatal(err)
		}
		if again != got {
			t.Errorf("resolvedTempDir returned %s, which still resolves to %s.\n"+
				"A fixture that hands out an unresolved path is the darwin PATH-RESOLUTION "+
				"class: every consumer that resolves — or refuses — sees a different path "+
				"than the one it was given, and Linux never notices.", got, again)
		}
	})
}
