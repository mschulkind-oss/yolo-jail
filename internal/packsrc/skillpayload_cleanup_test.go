package packsrc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveSealedSkillTreeRefusesLinkedAncestorWithoutChangingOutsideModes(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "payload")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("outside"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0755) })
	if err := os.Symlink(outside, filepath.Join(base, "ancestor")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSealedSkillTree(base, filepath.Join(base, "ancestor/payload")); err == nil {
		t.Fatal("linked ancestor accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("outside permissions changed: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(target, "marker"))
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside bytes changed: %q %v", data, err)
	}
}
func TestRemoveSealedSkillTreeRefusesLinkedAncestorEvenWithinItsBase(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "protected")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("keep"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0755) })
	if err := os.Symlink("protected", filepath.Join(base, "ancestor")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSealedSkillTree(base, filepath.Join(base, "ancestor/marker")); err == nil {
		t.Fatal("linked ancestor allowed ordinary removal before refusal")
	}
	data, err := os.ReadFile(filepath.Join(target, "marker"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("guard deleted target: %q %v", data, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("guard changed outside-root modes: %v %v", info, err)
	}
}

func TestRemoveSealedSkillTreeRejectsItsBaseBeforePermissionRestoration(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0755) })
	if err := RemoveSealedSkillTree(base, base); err == nil {
		t.Fatal("owned base removal accepted")
	}
	info, err := os.Stat(base)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("base permissions changed before rejection: %v %v", info, err)
	}
}
