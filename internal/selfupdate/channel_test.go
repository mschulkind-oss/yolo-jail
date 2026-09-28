package selfupdate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func TestDetect(t *testing.T) {
	isGit := func(dir string) bool { return dir == "/src/yolo-jail" }
	noGit := func(string) bool { return false }
	bundleIn := func(want string) func(string) bool {
		return func(exeDir string) bool { return exeDir == want }
	}
	cases := []struct {
		name string
		in   DetectInput
		want Channel
	}{
		{
			name: "source install: stamped checkout that still exists",
			in: DetectInput{Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", SourceBranch: "main", GitCommit: "cfefa8bf",
				Baked: "0.10.0+510.gcfefa8bf", IsSourceCheckout: isGit},
			want: Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"},
		},
		{
			name: "source stamp whose checkout is gone says so",
			in: DetectInput{Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", GitCommit: "cfefa8bf",
				Baked: "0.10.0+510.gcfefa8bf", IsSourceCheckout: noGit, HasBundleBeside: func(string) bool { return true }},
			want: Channel{Kind: KindUnknown, Exe: "/home/u/.local/bin/yolo", MissingSourceDir: "/src/yolo-jail"},
		},
		{
			name: "homebrew, even though it ships a bundle beside the binary",
			in: DetectInput{Exe: "/opt/homebrew/Cellar/yolo-jail/0.10.0/bin/yolo", Baked: "0.10.0",
				HasBundleBeside: func(string) bool { return true }},
			want: Channel{Kind: KindHomebrew, Exe: "/opt/homebrew/Cellar/yolo-jail/0.10.0/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "pipx, found by its metadata file",
			in: DetectInput{Exe: "/home/u/.local/share/pipx/venvs/yolo-jail/bin/yolo", Baked: "0.10.0",
				FileExists: fileIn("/home/u/.local/share/pipx/venvs/yolo-jail/pipx_metadata.json")},
			want: Channel{Kind: KindPipx, Exe: "/home/u/.local/share/pipx/venvs/yolo-jail/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "pipx under a relocated PIPX_HOME",
			in: DetectInput{Exe: "/home/u/.pipx/venvs/yolo-jail/bin/yolo", Baked: "0.10.0",
				FileExists: fileIn("/home/u/.pipx/venvs/yolo-jail/pipx_metadata.json")},
			want: Channel{Kind: KindPipx, Exe: "/home/u/.pipx/venvs/yolo-jail/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "uv tool, found by its receipt",
			in: DetectInput{Exe: "/home/u/.local/share/uv/tools/yolo-jail/bin/yolo", Baked: "0.10.0",
				FileExists: fileIn("/home/u/.local/share/uv/tools/yolo-jail/uv-receipt.toml")},
			want: Channel{Kind: KindUV, Exe: "/home/u/.local/share/uv/tools/yolo-jail/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "pipx as the wheel really runs: the binary inside site-packages",
			in: DetectInput{Exe: "/home/u/.local/share/pipx/venvs/yolo-jail/lib/python3.13/site-packages/yolo_jail/bin/yolo", Baked: "0.10.0",
				FileExists: fileIn("/home/u/.local/share/pipx/venvs/yolo-jail/pipx_metadata.json")},
			want: Channel{Kind: KindPipx, Exe: "/home/u/.local/share/pipx/venvs/yolo-jail/lib/python3.13/site-packages/yolo_jail/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "uv as the wheel really runs",
			in: DetectInput{Exe: "/home/u/.local/share/uv/tools/yolo-jail/lib/python3.13/site-packages/yolo_jail/bin/yolo", Baked: "0.10.0",
				FileExists: fileIn("/home/u/.local/share/uv/tools/yolo-jail/uv-receipt.toml")},
			want: Channel{Kind: KindUV, Exe: "/home/u/.local/share/uv/tools/yolo-jail/lib/python3.13/site-packages/yolo_jail/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "a marker in some other tool's environment is not ours",
			in: DetectInput{Exe: "/home/u/.local/share/pipx/venvs/other/bin/yolo", Baked: "0.10.0",
				FileExists: func(string) bool { return true }},
			want: Channel{Kind: KindUnknown, Exe: "/home/u/.local/share/pipx/venvs/other/bin/yolo"},
		},
		{
			name: "go install from the module proxy",
			in:   DetectInput{Exe: "/home/u/go/bin/yolo", ModuleVersion: "v0.10.0"},
			want: Channel{Kind: KindGoInstall, Exe: "/home/u/go/bin/yolo", Version: "0.10.0"},
		},
		{
			name: "a local go build in a checkout is not go install, despite its pseudo-version",
			in:   DetectInput{Exe: "/src/yolo-jail/yolo", ModuleVersion: "v0.10.1-0.20260925235235-cfefa8bf599b+dirty", BuiltFromVCS: true},
			want: Channel{Kind: KindUnknown, Exe: "/src/yolo-jail/yolo"},
		},
		{
			name: "a test binary is unidentified",
			in:   DetectInput{Exe: "/tmp/go-build123/b001/cli.test", ModuleVersion: "(devel)"},
			want: Channel{Kind: KindUnknown, Exe: "/tmp/go-build123/b001/cli.test"},
		},
		{
			name: "release archive",
			in:   DetectInput{Exe: "/opt/yolo/yolo", Baked: "0.10.0", HasBundleBeside: bundleIn("/opt/yolo")},
			want: Channel{Kind: KindArchive, Exe: "/opt/yolo/yolo", Version: "0.10.0"},
		},
		{
			name: "a package-manager path with no version stamp is unidentified",
			in:   DetectInput{Exe: "/opt/homebrew/Cellar/yolo-jail/HEAD/bin/yolo"},
			want: Channel{Kind: KindUnknown, Exe: "/opt/homebrew/Cellar/yolo-jail/HEAD/bin/yolo"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(c.in); got != c.want {
				t.Errorf("Detect() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestIsSourceCheckout(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsSourceCheckout(dir) {
		t.Error("a git repository without this module must not be accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// comment\nmodule `"+Module+"`\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsSourceCheckout(dir) {
		t.Error("a yolo-jail git checkout must be accepted")
	}
}

func TestSourceIdentityIncludesCheckoutAndBranch(t *testing.T) {
	base := Channel{Kind: KindSource, Version: "cfefa8bf", SourceDir: "/src/one", Branch: "main"}
	for name, other := range map[string]Channel{
		"checkout": {Kind: KindSource, Version: "cfefa8bf", SourceDir: "/src/two", Branch: "main"},
		"branch":   {Kind: KindSource, Version: "cfefa8bf", SourceDir: "/src/one", Branch: "release"},
	} {
		t.Run(name, func(t *testing.T) {
			if base.Identity() == other.Identity() {
				t.Errorf("source identities collide: %q", base.Identity())
			}
		})
	}
}

func TestCurrentValidatesTheStampedCheckout(t *testing.T) {
	origDir, origBranch, origCommit := version.SourceDir, version.SourceBranch, version.GitCommit
	t.Cleanup(func() {
		version.SourceDir, version.SourceBranch, version.GitCommit = origDir, origBranch, origCommit
	})

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+Module+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	version.SourceDir, version.SourceBranch, version.GitCommit = dir, "main", "cfefa8bf"
	if got := Current(); got.Kind != KindSource || got.SourceDir != dir {
		t.Fatalf("Current() = %+v, want the stamped source checkout", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Current(); got.Kind != KindUnknown || got.MissingSourceDir != dir {
		t.Fatalf("Current() = %+v, want the replaced checkout reported as missing", got)
	}
}

func fileIn(want string) func(string) bool {
	return func(path string) bool { return path == want }
}

func TestEnabled(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	if !Enabled(env(nil)) {
		t.Error("a plain host process must be enabled")
	}
	for _, key := range []string{"YOLO_VERSION", "CI", DisableEnv} {
		if Enabled(env(map[string]string{key: "1"})) {
			t.Errorf("%s set must disable update checks", key)
		}
	}
}

func TestCanApply(t *testing.T) {
	for _, k := range []Kind{KindSource, KindHomebrew} {
		if !(Channel{Kind: k}).CanApply() {
			t.Errorf("%s should be updatable in place", k)
		}
	}
	for _, k := range []Kind{KindGoInstall, KindPipx, KindUV, KindArchive, KindUnknown} {
		if (Channel{Kind: k}).CanApply() {
			t.Errorf("%s cannot update the complete installation in place", k)
		}
	}
}
