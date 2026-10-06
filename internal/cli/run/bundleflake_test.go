package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackagedFlakeImageExtrasUsesPathReference(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
for arg do
 if [ "$arg" = 'path:.#yoloImageExtras' ]; then echo /nix/store/fixture-extras; exit 0; fi
done
echo implicit-Git-reference >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := nixBuildImageExtrasProfile(root)
	if err != nil || got != "/nix/store/fixture-extras" {
		t.Fatalf("packaged extras=%q, %v", got, err)
	}
}
