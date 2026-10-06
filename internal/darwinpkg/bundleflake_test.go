package darwinpkg

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagedFlakeNativeBuildAndSkipEvalUsePathReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin", "darwin-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	guard := `found=0
for arg do
 case "$arg" in .\#*) exit 9;; path:.\#*) found=1;; esac
done
[ "$found" = 1 ] || exit 10
`
	standInNix(t, guard+`echo '["fixture-skipped"]'`, guard+`echo /nix/store/fixture-profile`)
	if got := skippedNames(root, os.Environ(), "aarch64-darwin"); len(got) != 1 || got[0] != "fixture-skipped" {
		t.Fatalf("packaged skip eval=%v", got)
	}
	for _, floor := range []bool{false, true} {
		var err error
		if floor {
			_, err = MaterializeFloorAt(root, nil, "aarch64-darwin", filepath.Join(t.TempDir(), "root"), io.Discard)
		} else {
			_, err = MaterializeAt(root, nil, "aarch64-darwin", "", io.Discard)
		}
		if err != nil {
			t.Fatalf("packaged build (floor=%v): %v", floor, err)
		}
	}
	// A checkout retains Git semantics even when prebuilt output happens to exist.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := MaterializeAt(root, nil, "aarch64-darwin", "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("source reference unexpectedly rewritten: %v", err)
	}
}
