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

// Pack and overlay staging both REPLACE their destination rather than merging, so
// content a pack stopped shipping actually disappears.
func TestStagedTreesReplaceRatherThanMerge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(src, sd string) [][]string
		dest  func(sd string) string
	}{
		{"packs", func(src, sd string) [][]string { return StagePackCommands(src, "proj", sd) },
			func(sd string) string { return StagedPackRoot("proj", sd) }},
		{"home overlay", func(src, sd string) [][]string { return StageHomeOverlayCommands(src, "proj", sd) },
			func(sd string) string { return StagedHomeOverlay("proj", sd) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, src := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "current"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			runStaged(t, tc.build(src, sd))

			// Plant a file from a "previous launch" that the source no longer has.
			stale := filepath.Join(tc.dest(sd), "removed-by-a-config-change")
			if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			runStaged(t, tc.build(src, sd))

			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Error("a file the source no longer ships survived re-staging — the " +
					"destination must be replaced, not merged into")
			}
			if _, err := os.Stat(filepath.Join(tc.dest(sd), "current")); err != nil {
				t.Errorf("the current content is missing after re-staging: %v", err)
			}
		})
	}
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
