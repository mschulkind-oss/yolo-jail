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
	// Mode is normalized: a staged pack file is content, so the exec bit can never
	// ride along even from a permissive source.
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", fi.Mode().Perm())
	}
}

// Rule 1: an executable is an ERROR without allow_exec, not a silent skip —
// dropping the one file the author cared about is worse than failing.
func TestStageRefusesExecutableWithoutOptIn(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{"hooks/run.sh": 0o755})

	_, err := Stage(Spec{Root: root, Dest: dest})
	if err == nil {
		t.Fatal("expected an error for an executable pack file")
	}
	for _, want := range []string{"hooks/run.sh", "allow_exec"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	// With the consumer's opt-in it stages AND arrives executable.
	//
	// This assertion is inverted from what it was. It used to require 0o644 "even with
	// allow_exec", on the reasoning that the exec bit is what rule 1 gates. But rule 1
	// gates whether an executable may be staged at all — enforced by the refusal above —
	// and a file that has passed that gate is one the CONSUMER explicitly asked for.
	// Stripping the bit anyway made allow_exec mean "may sit in the tree" instead of
	// "arrives usable", so no pack could ship a working script through any channel.
	res, err := Stage(Spec{Root: root, Dest: dest, AllowExec: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Staged) != 1 {
		t.Fatalf("staged = %v", res.Staged)
	}
	fi, _ := os.Stat(filepath.Join(dest, "hooks", "run.sh"))
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755: allow_exec grants the exec bit THROUGH to the "+
			"staged file, not merely admission to the tree", fi.Mode().Perm())
	}
}

// A non-executable file stays 0o644 even when the consumer set allow_exec: the flag is
// permission to carry a source's exec bit, not an instruction to add one. Only the 0o111
// bits come from the source, so a group-writable file in someone else's repo does not
// widen the staged copy either.
func TestStageDoesNotInventExecBit(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	writePack(t, root, map[string]os.FileMode{
		"skills/s/SKILL.md": 0o666, // world-writable source
	})
	if _, err := Stage(Spec{Root: root, Dest: dest, AllowExec: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "skills", "s", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644: allow_exec must not invent an exec bit, and the "+
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
