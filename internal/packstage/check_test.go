package packstage

// check_test.go pins the two walk variants the one pack resolver (config.ResolvePack) added:
// Check, Stage's verdict without its copy, and FollowSymlinks, how every notch reads a local
// pack (docs/plans/notch-convergence.md item 5 and OQ-NC9).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CHECK IS STAGE'S VERDICT WITHOUT ITS COPY: the host footer reads an unfiltered pack's
// declaration in place through it, so it must refuse exactly what Stage refuses and write
// nothing, Dest included.
func TestCheckRefusesAnEscapingSymlinkAndCopiesNothing(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dest := filepath.Join(t.TempDir(), "never")
	writePack(t, root, map[string]os.FileMode{"skills/a/SKILL.md": 0o644, "drop.md": 0o644})
	res, err := Check(Spec{Root: root, Dest: dest, Exclude: []string{"drop.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Staged, ",") != "skills/a/SKILL.md" || strings.Join(res.Excluded, ",") != "drop.md" {
		t.Errorf("Check = staged %v excluded %v, want Stage's answer", res.Staged, res.Excluded)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("Check created its Dest (%v): it must copy nothing", err)
	}

	secret := filepath.Join(outside, "id")
	if err := os.WriteFile(secret, []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "skills", "a", "leak.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(Spec{Root: root}); err == nil || !strings.Contains(err.Error(), "outside the pack") {
		t.Errorf("Check over an escaping symlink = %v, want Stage's refusal", err)
	}
}

// FOLLOWSYMLINKS DELIVERS A DOTFILE MANAGER'S TREE AS CONTENT: a whole skill directory linked
// out of the pack is walked as if it were one, and a linked file stages its target's bytes. This
// is how every notch reads a local pack (OQ-NC9, ruled A).
func TestFollowSymlinksWalksALinkedDirectoryOutOfThePack(t *testing.T) {
	root, dest, dotfiles := t.TempDir(), t.TempDir(), t.TempDir()
	writePack(t, dotfiles, map[string]os.FileMode{
		"skills/reviewer/SKILL.md": 0o644, "skills/reviewer/run.sh": 0o700, "bin/pick.sh": 0o644,
	})
	for _, d := range []string{"skills", "files"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dotfiles, "skills", "reviewer"), filepath.Join(root, "skills", "reviewer")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "bin", "pick.sh"), filepath.Join(root, "files", "pick.sh")); err != nil {
		t.Fatal(err)
	}

	if _, err := Stage(Spec{Root: root, Dest: dest}); err == nil {
		t.Fatal("control: without FollowSymlinks the escaping links must refuse")
	}
	res, err := Stage(Spec{Root: root, Dest: dest, FollowSymlinks: true,
		Exclude: []string{"skills/reviewer/run.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(res.Staged, ","), "files/pick.sh,skills/reviewer/SKILL.md"; got != want {
		t.Errorf("staged %s, want %s", got, want)
	}
	if strings.Join(res.Excluded, ",") != "skills/reviewer/run.sh" {
		t.Errorf("a filter must apply inside a followed directory: excluded %v", res.Excluded)
	}
	fi, err := os.Lstat(filepath.Join(dest, "skills", "reviewer", "SKILL.md"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the followed skill must land as a real file: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "files", "pick.sh")); !strings.Contains(string(data), "bin/pick.sh") {
		t.Errorf("the linked file must stage its target's bytes, got %q", data)
	}
}

// A followed link is still refused when it is a LOOP (the walk would never finish) or DANGLING (it
// names nothing to deliver), so following cannot turn a broken tree into a silently short one.
func TestFollowSymlinksRefusesALoopAndADanglingLink(t *testing.T) {
	loop := t.TempDir()
	writePack(t, loop, map[string]os.FileMode{"skills/a/SKILL.md": 0o644})
	if err := os.Symlink(filepath.Join(loop, "skills"), filepath.Join(loop, "skills", "a", "again")); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(Spec{Root: loop, FollowSymlinks: true}); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Errorf("a link loop = %v, want a refusal naming the loop", err)
	}

	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(dangling, "gone"), filepath.Join(dangling, "ghost.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(Spec{Root: dangling, FollowSymlinks: true}); err == nil || !strings.Contains(err.Error(), "resolves to nothing") {
		t.Errorf("a dangling link = %v, want a refusal", err)
	}
}
