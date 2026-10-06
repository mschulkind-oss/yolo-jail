package image

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func imageBundleFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPackagedFlakeBuildCallersUsePathReferences(t *testing.T) {
	for _, name := range []string{"run", "preflight", "install-prefix", "guest-prefix", "copier"} {
		t.Run(name, func(t *testing.T) {
			root := imageBundleFixture(t)
			freshNixChildren(t)
			standInNix(t, `found=0
for arg do
 case "$arg" in .\#*) echo implicit-Git-reference >&2; exit 9;; path:.\#*) found=1;; esac
done
[ "$found" = 1 ] || exit 10
exit 0`)
			var path string
			var tail []string
			switch name {
			case "run":
				path, tail = buildImageStorePath(ImageAttrDefault, root, nil, filepath.Join(t.TempDir(), "result"), io.Discard)
			case "install-prefix", "guest-prefix":
				t.Setenv("HOME", t.TempDir())
				if name == "install-prefix" {
					path, tail = BuildJailPrefix(root, io.Discard)
				} else {
					path, tail = BuildGuestPrefix(root, io.Discard)
				}
			case "copier":
				path, tail = runNixBuild(flakeBuildArgv(ImageCopierAttr, filepath.Join(t.TempDir(), "result"), nil), root, os.Environ(), "result", io.Discard)
			default:
				path, tail = BuildOCIImage(OCIBuildRequest{RepoRoot: root, AlsoBuild: []string{ImageExtrasAttr}})
			}
			if path == "" {
				t.Fatalf("packaged build failed: %v", tail)
			}
		})
	}
}

// This is the installed Cellar failure, driven through the production eval caller.
// No inputs, downloads, image build, container, or Homebrew installation is needed.
func TestPackagedFlakeInsideIgnoredGitDirectoryEvaluates(t *testing.T) {
	for _, bin := range []string{"nix", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	parent := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", parent}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("Cellar/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".gitignore")
	runGit("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	root := filepath.Join(parent, "Cellar", "yolo jail", "test", "share", "yolo-jail")
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	want := identityAlgoPrefix + strings.Repeat("a", 64)
	flake := "{ outputs = { self }: { imageIdentity = \"" + want + "\"; }; }\n"
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte(flake), 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "bundle alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, alias} {
		got, ok := EvalImageIdentity(dir)
		if !ok || got != want {
			t.Fatalf("ignored Cellar bundle %q identity = %q, %v; want %q", dir, got, ok, want)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, ok := EvalImageIdentity(root); ok {
		t.Fatalf("source checkout lost Git filtering: %q", got)
	}
}
