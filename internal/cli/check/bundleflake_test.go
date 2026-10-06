package check

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPackagedFlakeDryRunUsesPathReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	o := &Options{}
	fillDefaults(o)
	o.Exec = func(argv []string, dir string, _ []string, _ time.Duration) ExecResult {
		if dir != root || !slices.Contains(argv, "path:.#ociImage") || !slices.Contains(argv, "path:.#imageCopier") {
			t.Fatalf("packaged dry-run dir=%q argv=%v", dir, argv)
		}
		return ExecResult{Ran: true, RC: 0}
	}
	o.nixDryRunWillBuild(root, nil)
}
