package packstage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePack(t *testing.T, root string, files map[string]os.FileMode) {
	t.Helper()
	for rel, mode := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+rel), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageCopiesTreeAndForcesMode(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/rust-review/SKILL.md": 0o644,
		"AGENTS.md":                   0o600,
	})

	res, err := Stage(Spec{Root: root, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Staged) != 2 {
		t.Errorf("staged = %v, want 2 files", res.Staged)
	}
	got := filepath.Join(dest, "skills", "rust-review", "SKILL.md")
	if data, err := os.ReadFile(got); err != nil || !strings.Contains(string(data), "SKILL.md") {
		t.Errorf("staged file missing/wrong: %v %s", err, data)
	}
	// Mode is normalized to 0o644 for a NON-executable source: the read/write bits are
	// yolo's decision, not the source repo's. Executability is the one bit that rides
	// through (TestStageShipsExecutables).
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", fi.Mode().Perm())
	}
}

// A pack SHIPS ITS TOOLS. An executable stages, and arrives executable — which is the
// whole point: a skill that tells an agent to run references/check.sh must be able to
// ship references/check.sh runnable.
//
// This replaces TestStageRefusesExecutableWithoutOptIn and its allow_exec twin, and the
// deletion is the subject rather than a side effect. Those pinned a gate that refused any
// executable unless the CONSUMER set `allow_exec`. It read as a trust boundary and was not
// one — `bash file.sh` never needed the bit — so it stopped nothing an adversary would do
// while failing on the honest case it kept meeting. The hazard it was groping at is real
// and is now refused where it lives: a destination on the jail's PATH, at the manifest
// (packdecl.appendJailPathProblems). Mode bits were the wrong instrument for it.
func TestStageShipsExecutables(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/s/references/check.sh": 0o755,
	})

	res, err := Stage(Spec{Root: root, Dest: dest})
	if err != nil {
		t.Fatalf("staging an executable failed: %v", err)
	}
	if len(res.Staged) != 1 {
		t.Fatalf("staged = %v, want the one executable", res.Staged)
	}
	fi, err := os.Stat(filepath.Join(dest, "skills", "s", "references", "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755: the bit must ride THROUGH to the staged file, or "+
			"the pack ships a script nothing can run", fi.Mode().Perm())
	}
}

// A non-executable file stays 0o644. Only the 0o111 bits come from the source, so a
// group- or world-writable file in someone else's repo does not widen the staged copy:
// what carries through is executability, not the source's whole mode.
func TestStageDoesNotInventExecBit(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/s/SKILL.md": 0o666, // world-writable source
	})
	if _, err := Stage(Spec{Root: root, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "skills", "s", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644: staging must not invent an exec bit, and the "+
			"read/write bits stay yolo's decision", fi.Mode().Perm())
	}
}

// Rule 2: a pack comes from someone else's repo, so an escaping symlink must not
// stage a host secret into a mounted tree.
func TestStageRefusesEscapingSymlink(t *testing.T) {
	root, dest, outside := t.TempDir(), t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "skills", "innocuous.md")); err != nil {
		t.Fatal(err)
	}

	_, err := Stage(Spec{Root: root, Dest: dest})
	if err == nil {
		t.Fatal("expected an error for a symlink escaping the pack root")
	}
	if !strings.Contains(err.Error(), "outside the pack") {
		t.Errorf("error %q does not explain the escape", err)
	}
	// And nothing leaked.
	if data, _ := os.ReadFile(filepath.Join(dest, "skills", "innocuous.md")); strings.Contains(string(data), "PRIVATE KEY") {
		t.Error("the escaping symlink's target was staged")
	}
}

// An IN-pack symlink is fine and is resolved to a plain file, so the staged tree
// does not depend on the pack's internal link layout.
func TestStageResolvesInPackSymlink(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{"real.md": 0o644})
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	res, err := Stage(Spec{Root: root, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Staged) != 2 {
		t.Errorf("staged = %v, want both real.md and alias.md", res.Staged)
	}
	fi, err := os.Lstat(filepath.Join(dest, "alias.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("staged alias.md is still a symlink; it must be a plain file")
	}
}

// `only` must take a whole DIRECTORY, which is what a user means by
// "skills/rust-*" — the literal file-only reading would silently stage nothing.
func TestStageOnlyMatchesDirectoryPrefix(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/rust-review/SKILL.md":  0o644,
		"skills/rust-review/extra.md":  0o644,
		"skills/legacy-thing/SKILL.md": 0o644,
		"AGENTS.md":                    0o644,
	})

	res, err := Stage(Spec{Root: root, Dest: dest, Only: []string{"skills/rust-*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Staged) != 2 {
		t.Errorf("staged = %v, want both files under skills/rust-review", res.Staged)
	}
	// Excluded is REPORTED, not silently dropped, so an `only` typo is diagnosable.
	if len(res.Excluded) != 2 {
		t.Errorf("excluded = %v, want the two non-matching files reported", res.Excluded)
	}
}

func TestStageExcludeAppliesAfterOnly(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/a/SKILL.md": 0o644,
		"skills/b/SKILL.md": 0o644,
	})
	res, err := Stage(Spec{Root: root, Dest: dest,
		Only: []string{"skills/*"}, Exclude: []string{"skills/b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Staged) != 1 || !strings.HasPrefix(res.Staged[0], "skills/a") {
		t.Errorf("staged = %v, want only skills/a", res.Staged)
	}
}

// Rule 3: the staging DIR's inode must survive, because a running jail's bind mount
// captured it — recreating the dir silently detaches the mount.
func TestStageClearsContentsButKeepsDirInode(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{"new.md": 0o644})
	stale := filepath.Join(dest, "stale.md")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Stage(Spec{Root: root, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale file survived staging")
	}
	after, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("staging dir was recreated; a live bind mount would silently detach")
	}
}

// A pack's own .git must never be copied into the jail.
func TestStageSkipsGitMetadata(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"SKILL.md":    0o644,
		".git/config": 0o644,
	})
	res, err := Stage(Spec{Root: root, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Staged {
		if strings.HasPrefix(s, ".git") {
			t.Errorf("staged VCS metadata: %v", res.Staged)
		}
	}
}

func TestStageRejectsMissingRoot(t *testing.T) {
	_, err := Stage(Spec{Root: filepath.Join(t.TempDir(), "nope"), Dest: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error for a missing pack root")
	}
}
