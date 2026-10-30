package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsePhysicalTempDirResolvesASymlinkedTMPDIR(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", link)
	UsePhysicalTempDir()
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TMPDIR"); got != want {
		t.Fatalf("TMPDIR = %q, want the resolved %q", got, want)
	}
}

func TestUsePhysicalTempDirLeavesAnUnresolvableTMPDIR(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	t.Setenv("TMPDIR", missing)
	UsePhysicalTempDir()
	if got := os.Getenv("TMPDIR"); got != missing {
		t.Fatalf("TMPDIR = %q, want it left at %q", got, missing)
	}
}
