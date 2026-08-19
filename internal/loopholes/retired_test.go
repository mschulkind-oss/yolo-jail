package loopholes

// Tests for the RETIREMENT of the hand-placed loopholes directory (OQ-LP10).
//
// Two properties, and they are equally load-bearing. It must not be DISCOVERED — that
// is the whole point, since it was the one channel that started a host daemon with no
// selection step. And it must not disappear SILENTLY — whatever sat there was running
// on the user's machine until the upgrade that removed the channel, so an unexplained
// absence would be the worst possible way to deliver this.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain pins the retired directory somewhere that does not exist, for the whole
// package.
//
// Without it every test in this package would depend on whether the developer running
// it happens to have ~/.local/share/yolo-jail/loopholes — the notice fires from
// Discover, which nearly every test here calls, and warning capture is asserted on. A
// suite whose result depends on whose home it ran in is not a suite.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "yolo-retired-absent")
	if err != nil {
		panic(err)
	}
	// Created and immediately removed, so the path is known-unique AND known-absent.
	_ = os.RemoveAll(dir)
	RetiredUserLoopholesDir = func() string { return dir }
	os.Exit(m.Run())
}

// withRetiredDir points the retired dir at root and re-arms the once-per-process
// notice, returning the restore func. t.Cleanup(withRetiredDir(dir)) is the shape.
func withRetiredDir(root string) func() {
	prev := RetiredUserLoopholesDir
	RetiredUserLoopholesDir = func() string { return root }
	resetRetiredNotice()
	return func() {
		RetiredUserLoopholesDir = prev
		resetRetiredNotice()
	}
}

// TestRetiredUserDirIsNotDiscovered is the retirement itself: a perfectly valid
// manifest in the old directory contributes NOTHING.
//
// The manifest is deliberately one that used to work — enabled, with a host_daemon —
// because "used to spawn a process on your machine with no config line anywhere" is
// exactly the thing being removed.
func TestRetiredUserDirIsNotDiscovered(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	retired := t.TempDir()
	mod := mkdir(t, filepath.Join(retired, "old-hand-placed"))
	writeManifest(t, mod, map[string]any{
		"name": "old-hand-placed", "description": "x",
		"host_daemon": map[string]any{"cmd": []any{"some-daemon", "--socket", "{socket}"}},
	})
	t.Cleanup(withRetiredDir(retired))

	for _, includeDisabled := range []bool{false, true} {
		got := names(Discover(DiscoverOptions{IncludeDisabled: includeDisabled}))
		if containsStr(got, "old-hand-placed") {
			t.Fatalf("Discover(IncludeDisabled=%v) = %v — the hand-placed loopholes directory "+
				"is retired, so nothing in it may reach discovery at any precedence",
				includeDisabled, got)
		}
	}
	// And `yolo check`'s independent walker agrees. It is a SEPARATE walk (it needs the
	// error channel Discover throws away), which is precisely how the two came to
	// disagree about sources before the convergence — so it is pinned separately.
	for _, e := range ValidateLoopholes() {
		if strings.Contains(e.Path, "old-hand-placed") {
			t.Errorf("ValidateLoopholes still walks the retired directory: %+v", e)
		}
	}
}

// TestRetiredUserDirIsReportedWithMigrationInstructions: a silent disappearance is not
// acceptable for something that was running a host daemon. The notice has to name the
// directory, every stranded module, and what to write instead.
func TestRetiredUserDirIsReportedWithMigrationInstructions(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	retired := t.TempDir()
	for _, name := range []string{"zeta", "alpha"} {
		mod := mkdir(t, filepath.Join(retired, name))
		writeManifest(t, mod, map[string]any{"name": name, "description": "x"})
	}
	t.Cleanup(withRetiredDir(retired))

	if got := RetiredUserLoopholes(); len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("RetiredUserLoopholes = %v, want [alpha zeta] (sorted)", got)
	}
	notice := RetiredUserLoopholeNotice()
	for _, want := range []string{retired, "alpha", "zeta", "RETIRED", "kind\": \"loophole"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}

	// And it reaches stderr from ordinary discovery, not only from a command someone
	// remembered to wire it into — a plain `yolo run` is where a user finds out.
	warnings := captureWarnings(t)
	resetRetiredNotice()
	Discover(DiscoverOptions{})
	if !containsStr2(*warnings, retired) {
		t.Errorf("Discover emitted no migration notice; warnings = %v", *warnings)
	}
	// ONCE per process. Discover runs several times on a launch (§5.1's census), and a
	// migration instruction repeated five times reads as a malfunction.
	before := len(*warnings)
	Discover(DiscoverOptions{})
	Discover(DiscoverOptions{})
	if len(*warnings) != before {
		t.Errorf("the notice repeated: %v", (*warnings)[before:])
	}
}

// TestRetiredUserDirIsSilentWhenThereIsNothingToMigrate: the overwhelming majority of
// machines never had this directory, and they must see nothing at all — an absent
// directory, an empty one, and a stray non-module child are all "nothing to migrate".
func TestRetiredUserDirIsSilentWhenThereIsNothingToMigrate(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	empty := t.TempDir()
	mkdir(t, filepath.Join(empty, "not-a-module")) // a dir with no manifest.jsonc
	if err := os.WriteFile(filepath.Join(empty, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{empty, filepath.Join(empty, "does-not-exist")} {
		t.Cleanup(withRetiredDir(dir))
		if got := RetiredUserLoopholes(); len(got) != 0 {
			t.Errorf("RetiredUserLoopholes(%s) = %v, want none", dir, got)
		}
		if got := RetiredUserLoopholeNotice(); got != "" {
			t.Errorf("notice for %s = %q, want empty", dir, got)
		}
		warnings := captureWarnings(t)
		resetRetiredNotice()
		Discover(DiscoverOptions{})
		if len(*warnings) != 0 {
			t.Errorf("warnings on a machine with nothing to migrate: %v", *warnings)
		}
	}
}

// TestRetiredUserDirReportsAModuleTheLoaderWouldReject: the listing must use the
// WEAKEST possible test for "is a loophole module", because its whole job is telling
// someone what they still have to move. Decoding here would hide exactly the module
// whose owner most needs to hear about it.
func TestRetiredUserDirReportsAModuleTheLoaderWouldReject(t *testing.T) {
	unsetJail(t)
	retired := t.TempDir()
	mod := mkdir(t, filepath.Join(retired, "broken"))
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte("{not: json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(withRetiredDir(retired))
	if got := RetiredUserLoopholes(); len(got) != 1 || got[0] != "broken" {
		t.Errorf("RetiredUserLoopholes = %v, want [broken] — a manifest the loader rejects is "+
			"still a directory its owner has to move", got)
	}
}

// containsStr2 reports whether any element of list CONTAINS sub. The warnings are
// whole paragraphs, so the plain equality helper next door does not fit.
func containsStr2(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
