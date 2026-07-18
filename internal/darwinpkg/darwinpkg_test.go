package darwinpkg

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildProfileArgv(t *testing.T) {
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"build", "--impure", "--no-link", "--print-out-paths", "--print-build-logs",
		".#packages.aarch64-darwin.yoloDarwinPackages",
	}
	if got := BuildProfileArgv(""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
	// Custom system flows through.
	if got := BuildProfileArgv("x86_64-darwin"); got[len(got)-1] != ".#packages.x86_64-darwin.yoloDarwinPackages" {
		t.Errorf("custom system attr = %q", got[len(got)-1])
	}
}

func TestUnavailableEvalArgv(t *testing.T) {
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"eval", "--impure", "--json", ".#darwinUnavailablePackages.aarch64-darwin",
	}
	if got := UnavailableEvalArgv(""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
}

func TestBuildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "YOLO_EXTRA_PACKAGES=stale", "HOME=/root"}
	// Empty packages -> var removed.
	got, err := BuildEnv(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range got {
		if kv == "YOLO_EXTRA_PACKAGES=stale" || len(kv) >= 20 && kv[:20] == "YOLO_EXTRA_PACKAGES=" {
			t.Errorf("empty packages should drop YOLO_EXTRA_PACKAGES, got %q", kv)
		}
	}
	// Non-empty -> compact JSON set.
	got, err = BuildEnv(base, []any{"ripgrep", "fd"})
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, kv := range got {
		if len(kv) >= 20 && kv[:20] == "YOLO_EXTRA_PACKAGES=" {
			found = kv[20:]
		}
	}
	if found != `["ripgrep", "fd"]` {
		t.Errorf("YOLO_EXTRA_PACKAGES = %q, want compact JSON", found)
	}
}

func TestProfilePaths(t *testing.T) {
	// Empty out -> empty.
	if pp, env := ProfilePaths("  ", nil); pp != nil || len(env) != 0 {
		t.Errorf("empty out => %v, %v", pp, env)
	}
	// bin always contributed; pkgconfig only when present.
	pp, env := ProfilePaths("/nix/store/abc-prof\n", func(string) bool { return false })
	if !reflect.DeepEqual(pp, []string{"/nix/store/abc-prof/bin"}) {
		t.Errorf("path prefix = %v", pp)
	}
	if len(env) != 0 {
		t.Errorf("no pkgconfig => empty env, got %v", env)
	}
	_, env = ProfilePaths("/nix/store/abc-prof", func(p string) bool { return true })
	if env["PKG_CONFIG_PATH"] != "/nix/store/abc-prof/lib/pkgconfig" {
		t.Errorf("PKG_CONFIG_PATH = %q", env["PKG_CONFIG_PATH"])
	}
}

func TestLockedNixpkgsRev(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "flake.lock")
	must(t, os.WriteFile(lock, []byte(`{"nodes":{"nixpkgs":{"locked":{"rev":"abc123def"}}}}`), 0o644))
	rev, err := LockedNixpkgsRev(lock)
	if err != nil || rev != "abc123def" {
		t.Errorf("rev = %q, %v", rev, err)
	}
	if _, err := LockedNixpkgsRev(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing lock should error")
	}
}

func TestParseSkippedNames(t *testing.T) {
	if got := ParseSkippedNames(`["foo","bar"]`); !reflect.DeepEqual(got, []string{"foo", "bar"}) {
		t.Errorf("= %v", got)
	}
	if got := ParseSkippedNames(`{}`); got != nil {
		t.Errorf("non-array => nil, got %v", got)
	}
	if got := ParseSkippedNames(`garbage`); got != nil {
		t.Errorf("bad json => nil, got %v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestParityVsLivePython cross-checks argv + env + profile_paths against the
// live darwin_packages.py. Skips without Python.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli import darwin_packages as d
env = d.build_env(["ripgrep", "fd"])
pp, extra = d.profile_paths("/nix/store/abc-prof")
out = {
  "profile_argv": d.build_profile_argv(),
  "unavail_argv": d.unavailable_eval_argv(),
  "pkg_json": env["YOLO_EXTRA_PACKAGES"],
  "path_prefix": pp,
}
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python darwin_packages import failed: %v", err)
	}
	var want struct {
		ProfileArgv []string `json:"profile_argv"`
		UnavailArgv []string `json:"unavail_argv"`
		PkgJSON     string   `json:"pkg_json"`
		PathPrefix  []string `json:"path_prefix"`
	}
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := BuildProfileArgv(""); !reflect.DeepEqual(got, want.ProfileArgv) {
		t.Errorf("profile argv:\n go: %v\n py: %v", got, want.ProfileArgv)
	}
	if got := UnavailableEvalArgv(""); !reflect.DeepEqual(got, want.UnavailArgv) {
		t.Errorf("unavail argv:\n go: %v\n py: %v", got, want.UnavailArgv)
	}
	goEnv, _ := BuildEnv([]string{}, []any{"ripgrep", "fd"})
	var goPkg string
	for _, kv := range goEnv {
		if len(kv) >= 20 && kv[:20] == "YOLO_EXTRA_PACKAGES=" {
			goPkg = kv[20:]
		}
	}
	if goPkg != want.PkgJSON {
		t.Errorf("pkg json go=%q py=%q", goPkg, want.PkgJSON)
	}
	if pp, _ := ProfilePaths("/nix/store/abc-prof", func(string) bool { return false }); !reflect.DeepEqual(pp, want.PathPrefix) {
		t.Errorf("path prefix go=%v py=%v", pp, want.PathPrefix)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
