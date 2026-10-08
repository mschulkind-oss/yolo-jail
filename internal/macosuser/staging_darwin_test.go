//go:build darwin

package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// staging_darwin_test.go EXECUTES the staging commands the run plan emits, with the
// `sudo` prefix dropped and the destination redirected under a temp dir.
//
// WHY THIS IS WORTH DOING WITHOUT PRIVILEGE. The plan tests assert the argv is
// right; nothing asserted the argv WORKS. Everything about these commands except
// where they write is privilege-independent — the copy, the mode bits, and the
// atomic replace all behave identically against /tmp — so redirecting the
// destination buys real coverage of the one part of the macos-user launch that
// touches the filesystem. What is left needing root is the destination being
// /var/yolo-jail and the owner being root, which is two facts, not a mechanism.
//
// It also covers the J2 FRESH-INODE RULE, which is the trap here and is invisible to
// an argv assertion: macOS caches Mach-O code signatures per vnode, so overwriting a
// previously staged binary IN PLACE gets the next exec SIGKILLed. The commands go
// copy-to-temp then atomic mv precisely to avoid that, and re-running them has to
// leave a binary that still runs.

// runStaged executes plan commands with sudo dropped and the state dir redirected.
func runStaged(t *testing.T, cmds [][]string) {
	t.Helper()
	for _, cmd := range cmds {
		if len(cmd) == 0 {
			continue
		}
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("staging command failed: %v\n  %s\n%s", err, strings.Join(cmd, " "), out)
		}
	}
}

// The binary staging works, produces something executable, and — the part that
// matters — survives being run twice.
func TestStageBinaryCommandsExecuteAndReStage(t *testing.T) {
	sd := t.TempDir()

	// A real Mach-O to stage: this test binary itself. A shell script would not
	// exercise the vnode signature cache the fresh-inode rule exists for.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	runStaged(t, StageBinaryCommands(self, sd))
	staged := StagedYoloPath(sd)

	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("nothing was staged: %v", err)
	}
	// a+rX: the sandbox uid must be able to read and exec what root staged.
	if mode := info.Mode().Perm(); mode&0o055 != 0o055 {
		t.Errorf("staged binary mode = %o, want world read+exec (the sandbox uid "+
			"cannot run it otherwise)", mode)
	}
	firstIno := inodeOf(t, staged)

	// RE-STAGE. This is the J2 rule: a fresh inode every time, or the next exec is
	// SIGKILLed for an invalid signature.
	runStaged(t, StageBinaryCommands(self, sd))
	if inodeOf(t, staged) == firstIno {
		t.Error("re-staging reused the inode — macOS caches code signatures per vnode, " +
			"so the next exec of this path would be SIGKILLed")
	}
	if out, err := exec.Command(staged, "-test.run", "^$").CombinedOutput(); err != nil {
		t.Errorf("the re-staged binary does not execute: %v\n%s", err, out)
	}
}

// The home overlay still replaces its workspace destination rather than merging, so content
// a workspace stopped shipping disappears. Pack trees have a separate immutable per-launch
// contract in TestStagedPackTreesAreImmutableAndDistinct.
func TestStagedTreesReplaceRatherThanMerge(t *testing.T) {
	sd, src := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "current"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runStaged(t, StageHomeOverlayCommands(src, "proj", sd))
	stale := filepath.Join(StagedHomeOverlay("proj", sd), "removed-by-a-config-change")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runStaged(t, StageHomeOverlayCommands(src, "proj", sd))
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a file the source no longer ships survived home-overlay re-staging")
	}
	if _, err := os.Stat(filepath.Join(StagedHomeOverlay("proj", sd), "current")); err != nil {
		t.Errorf("the current home-overlay content is missing after re-staging: %v", err)
	}
}

func TestStagedPackTreesAreImmutableAndDistinct(t *testing.T) {
	sd := t.TempDir()
	sourceA, sourceB, sourceC := t.TempDir(), t.TempDir(), t.TempDir()
	legacy := StagedPackRoot("proj", sd)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(legacy, "old-session")
	if err := os.WriteFile(legacyFile, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyInode := inodeOf(t, legacyFile)
	writeModule := func(root string, script []byte) {
		t.Helper()
		target := filepath.Join(root, "local", "loopholes", "hello", "bin", "hello")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, script, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "local", "data"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeModule(sourceA, []byte("#!/bin/sh\necho A\n"))
	writeModule(sourceB, []byte("#!/bin/sh\necho B\n"))
	rootA := StagedPackTreeRoot("proj", sourceA, sd)
	rootB := StagedPackTreeRoot("proj", sourceB, sd)
	rootC := StagedPackTreeRoot("proj", sourceC, sd)
	if rootA == rootB || rootA == rootC || rootB == rootC {
		t.Fatalf("distinct host tree identities produced reused roots: %s %s %s", rootA, rootB, rootC)
	}
	runStaged(t, StagePackCommands(sourceA, "proj", sd))
	stagedA := filepath.Join(rootA, "local", "loopholes", "hello", "bin", "hello")
	firstBytes, err := os.ReadFile(stagedA)
	if err != nil || string(firstBytes) != "#!/bin/sh\necho A\n" {
		t.Fatalf("A staged bytes = %q, err %v", firstBytes, err)
	}
	firstInode := inodeOf(t, stagedA)
	if mode := fileMode(t, stagedA); mode&0o111 != 0o111 {
		t.Errorf("A's executable mode = %o, want execute bits preserved", mode)
	}
	if mode := fileMode(t, filepath.Join(rootA, "local", "data")); mode&0o111 != 0 {
		t.Errorf("non-executable data gained execute bits: %o", mode)
	}

	runStaged(t, StagePackCommands(sourceB, "proj", sd))
	runStaged(t, StagePackCommands(sourceC, "proj", sd))
	if got, err := os.ReadFile(stagedA); err != nil || string(got) != string(firstBytes) || inodeOf(t, stagedA) != firstInode {
		t.Errorf("B/C changed A's stored script bytes or inode: bytes %q, err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(rootB, "local", "loopholes", "hello", "bin", "hello")); err != nil {
		t.Errorf("B's changed module is missing from B's tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootC, "local", "loopholes", "hello")); !os.IsNotExist(err) {
		t.Errorf("C retained the dropped module: %v", err)
	}
	if out, err := exec.Command(stagedA).CombinedOutput(); err != nil || string(out) != "A\n" {
		t.Errorf("A's restart target after B/C returned %q, err %v", out, err)
	}

	// A duplicate reservation fails before it can modify this launch or a legacy tree.
	var reserveFailed bool
	for _, cmd := range StagePackCommands(sourceA, "proj", sd) {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err == nil {
			continue
		}
		if isPackTreeReservationCommand(cmd, rootA) {
			reserveFailed = true
			break
		}
		t.Fatalf("duplicate A staging failed before exclusive reservation: %v (%s)\n%s", err,
			strings.Join(cmd, " "), out)
	}
	if !reserveFailed {
		t.Fatal("reusing A's destination did not fail its exclusive reservation")
	}
	if got, err := os.ReadFile(stagedA); err != nil || string(got) != string(firstBytes) || inodeOf(t, stagedA) != firstInode {
		t.Errorf("a failed duplicate reservation changed A: bytes %q, err %v", got, err)
	}
	if got, err := os.ReadFile(legacyFile); err != nil || string(got) != "legacy" || inodeOf(t, legacyFile) != legacyInode {
		t.Errorf("new stages changed the legacy workspace tree: bytes %q, err %v", got, err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// Empty source → no commands at all, so a launch with nothing to stage pays nothing
// and cannot half-create a destination.
func TestStagingIsSkippedWithNothingToStage(t *testing.T) {
	if cmds := StagePackCommands("", "proj", t.TempDir()); len(cmds) != 0 {
		t.Errorf("packs: %d commands for an empty source", len(cmds))
	}
	if cmds := StageHomeOverlayCommands("", "proj", t.TempDir()); len(cmds) != 0 {
		t.Errorf("overlay: %d commands for an empty source", len(cmds))
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	out, err := exec.Command("/usr/bin/stat", "-f", "%i", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return parseUint(t, strings.TrimSpace(string(out)))
}

func parseUint(t *testing.T, s string) uint64 {
	t.Helper()
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + uint64(r-'0')
	}
	return n
}
