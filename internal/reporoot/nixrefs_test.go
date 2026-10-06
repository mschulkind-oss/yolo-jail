package reporoot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFlakeArgvDistinguishesBundlesFromSource(t *testing.T) {
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "bin", platform), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0644); err != nil {
				t.Fatal(err)
			}
			argv := []string{"nix", "build", ".#ociImage", ".#imageCopier", "--out-link", "result", "other#attr"}
			want := []string{"nix", "build", "path:.#ociImage", "path:.#imageCopier", "--out-link", "result", "other#attr"}
			if got := FlakeArgv(root, argv); !reflect.DeepEqual(got, want) {
				t.Fatalf("bundle argv=%v want %v", got, want)
			}
			if argv[2] != ".#ociImage" {
				t.Fatal("mutated caller argv")
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if got := FlakeArgv(root, argv); !reflect.DeepEqual(got, argv) {
				t.Fatalf("source checkout changed: %v", got)
			}
		})
	}
}

func TestFlakeArgvDoesNotRewriteUnknownRoots(t *testing.T) {
	for _, root := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		argv := []string{"nix", "eval", ".#identity"}
		if got := FlakeArgv(root, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("root %q: %v", root, got)
		}
	}
}
