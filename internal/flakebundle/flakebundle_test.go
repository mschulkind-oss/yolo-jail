package flakebundle

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// stableIn returns the stable bundle path inside a fresh temp state dir, matching
// paths.FlakeBundleDir's shape (<state>/flake-bundle) without depending on it.
func stableIn(t *testing.T) string {
	t.Helper()
	// RESOLVED, because Current() resolves symlinks and callers compare its output
	// to paths derived from here. On macOS t.TempDir() hands back /var/folders/...,
	// which IS a symlink to /private/var/folders/..., so the unresolved form fails
	// every comparison there while passing on Linux.
	//
	// Reproducible on Linux, which is how this was verified rather than guessed:
	//   ln -s /tmp/real /tmp/link && TMPDIR=/tmp/link go test ./internal/flakebundle/
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "flake-bundle")
}

// stageBundle stages a minimally-real generation — the two flake files and the
// one binary whose absence is fatal — and activates it.
func stageBundle(t *testing.T, stable string, now time.Time, pid int) string {
	t.Helper()
	gen, err := StageDir(stable, now, pid)
	if err != nil {
		t.Fatalf("StageDir: %v", err)
	}
	bin := filepath.Join(gen, "bin", "linux-amd64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(gen, "flake.nix"), filepath.Join(bin, "yolo-entrypoint")} {
		if err := os.WriteFile(f, []byte(gen), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Activate(stable, gen); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return gen
}

func inode(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no inode on this platform")
	}
	return st.Ino
}

// TestActivateLeavesTheStablePathUsable is the baseline every other property
// rests on: after an install, the ONE path reporoot.Resolve consults still reads
// as a bundle. A generations scheme that got this wrong would refuse every launch
// on the machine.
func TestActivateLeavesTheStablePathUsable(t *testing.T) {
	stable := stableIn(t)
	gen := stageBundle(t, stable, time.Now(), 1)

	if _, err := os.Stat(filepath.Join(stable, "flake.nix")); err != nil {
		t.Fatalf("the stable path must read as a bundle after activation: %v", err)
	}
	if Current(stable) != gen {
		t.Errorf("Current = %q, want the activated generation %q", Current(stable), gen)
	}
	// RELATIVE, so a state dir that moves (a copied home, a differently-mounted
	// state dir) still resolves. An absolute target would silently dangle.
	target, err := os.Readlink(stable)
	if err != nil {
		t.Fatalf("the stable path must be a symlink: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("the symlink target must be relative, got %q", target)
	}
}

// TestASecondInstallDoesNotTouchTheFirstGeneration is the bug, stated as a test.
// A jail launched against generation 1 mounts that DIRECTORY; installing again
// must leave its inode and its contents exactly where they were, because a bind
// mount follows the inode and nothing can repair a running container's mount.
func TestASecondInstallDoesNotTouchTheFirstGeneration(t *testing.T) {
	stable := stableIn(t)
	first := stageBundle(t, stable, time.Now(), 1)
	firstBin := filepath.Join(first, "bin", "linux-amd64")
	before := inode(t, firstBin)

	second := stageBundle(t, stable, time.Now().Add(time.Second), 2)

	if second == first {
		t.Fatal("the second install reused the first generation's directory")
	}
	if _, err := os.Stat(filepath.Join(firstBin, "yolo-entrypoint")); err != nil {
		t.Fatalf("the first generation's yolo-entrypoint is gone — this is the bug "+
			"(`just install` deleting a running jail's pid1): %v", err)
	}
	if after := inode(t, firstBin); after != before {
		t.Errorf("the first generation's bin/ inode changed (%d -> %d); a running jail's "+
			"bind mount follows the inode, so this brings the failure back", before, after)
	}
	if Current(stable) != second {
		t.Errorf("the stable path must now point at the second generation")
	}
}

// TestActivateMigratesAPreGenerationsDirectoryByRename covers the upgrade window,
// which is the one moment this scheme could still brick a jail: the machine's
// stable path is a real DIRECTORY that running jails are mounting. It must be
// MOVED, never deleted — rename keeps the inode, so those mounts stay valid.
func TestActivateMigratesAPreGenerationsDirectoryByRename(t *testing.T) {
	stable := stableIn(t)
	legacyBin := filepath.Join(stable, "bin", "linux-amd64")
	if err := os.MkdirAll(legacyBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyBin, "yolo-entrypoint"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := inode(t, legacyBin)

	gen, err := StageDir(stable, time.Now(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	migrated, err := Activate(stable, gen)
	if err != nil {
		t.Fatalf("Activate over a pre-generations directory: %v", err)
	}
	if migrated == "" {
		t.Fatal("a real directory at the stable path must be reported as migrated — a " +
			"silent move leaves an unexplained legacy-* dir for a human to find")
	}
	if after := inode(t, filepath.Join(migrated, "bin", "linux-amd64")); after != before {
		t.Errorf("migration must be a rename (inode %d preserved), got %d — a copy-and-delete "+
			"would take the binaries out from under every jail running at upgrade time",
			before, after)
	}
	if got := Generations(stable); len(got) != 2 {
		t.Errorf("the migrated directory must become an ordinary generation the reaper can "+
			"collect; Generations = %v", got)
	}
}

// TestReapKeepsWhatIsStillRunning is the reaper's whole job. Four generations,
// each unreachable for a different reason, and only the genuinely dead one goes.
func TestReapKeepsWhatIsStillRunning(t *testing.T) {
	stable := stableIn(t)
	old := time.Now().Add(-48 * time.Hour)

	dead := stageBundle(t, stable, old, 1)
	inUse := stageBundle(t, stable, old.Add(time.Second), 2)
	fresh := stageBundle(t, stable, time.Now(), 3)
	current := stageBundle(t, stable, time.Now(), 4)
	// Only `fresh` and `current` are young; age the other two past the grace.
	for _, d := range []string{dead, inUse} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// `fresh` is inside the grace window but is NOT current — the case that
	// distinguishes "staged by an install that has not swapped yet" from garbage.
	if err := os.Chtimes(fresh, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{filepath.Join(inUse, "bin", "linux-amd64"): true}

	removed := Reap(stable, live, true, true, time.Now())

	if len(removed) != 1 || removed[0] != dead {
		t.Fatalf("Reap removed %v, want exactly [%s]", removed, dead)
	}
	for _, keep := range []string{inUse, fresh, current} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("Reap deleted a generation it must keep (%s): %v", keep, err)
		}
	}
}

// TestReapDeclinesWhenLivenessIsUnknown is the tri-state, and it is the property
// that keeps this from being the bug it fixes. "No jail is using this" and "I
// could not ask the runtime" produce the same empty set of sources; deleting on
// the second takes pid1 out from under a live jail.
func TestReapDeclinesWhenLivenessIsUnknown(t *testing.T) {
	stable := stableIn(t)
	old := time.Now().Add(-48 * time.Hour)
	gen := stageBundle(t, stable, old, 1)
	stageBundle(t, stable, time.Now(), 2) // so gen is not current
	if err := os.Chtimes(gen, old, old); err != nil {
		t.Fatal(err)
	}

	if removed := Reap(stable, nil, false /*liveKnown*/, true, time.Now()); len(removed) != 0 {
		t.Fatalf("Reap must decline entirely when the runtime cannot be enumerated, removed %v", removed)
	}
	if _, err := os.Stat(gen); err != nil {
		t.Errorf("the declined reap deleted something anyway: %v", err)
	}
}

// TestReapIgnoresWhatItDidNotStage is the scoping case, the same one OQ-BF3's
// tests exist for: a careless glob over the generations dir would take a human's
// own parked copy. Only names StageDir produces are candidates.
func TestReapIgnoresWhatItDidNotStage(t *testing.T) {
	stable := stableIn(t)
	stageBundle(t, stable, time.Now(), 1)
	strangers := []string{"keep-this", "20260909T120000Z-1-backup", "notes.txt"}
	for _, name := range strangers {
		p := filepath.Join(GenerationsDir(stable), name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	if removed := Reap(stable, map[string]bool{}, true, true, time.Now()); len(removed) != 0 {
		t.Fatalf("Reap took something it did not stage: %v", removed)
	}
	for _, name := range strangers {
		if _, err := os.Stat(filepath.Join(GenerationsDir(stable), name)); err != nil {
			t.Errorf("%s was deleted: %v", name, err)
		}
	}
}

// TestGenerationInUseDoesNotMatchOnAPathPrefix pins the separator. `…-1` is a
// string prefix of `…-12`, so a bare HasPrefix would report a dead generation as
// live — which is only ever safe by accident — and, read the other way, would let
// a rename of the comparison make a live one look dead.
func TestGenerationInUseDoesNotMatchOnAPathPrefix(t *testing.T) {
	gen := "/state/flake-bundles/20260909T120000Z-1"
	sources := map[string]bool{"/state/flake-bundles/20260909T120000Z-12/bin/linux-amd64": true}
	if generationInUse(gen, sources) {
		t.Error("a sibling whose name merely starts the same must not count as in use")
	}
	if !generationInUse(gen, map[string]bool{gen + "/bin/linux-amd64": true}) {
		t.Error("a source inside the generation must count as in use")
	}
}
