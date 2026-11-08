package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestProgramCaptureEnvironmentKeepsTheVMShareFacts pins the sealed npm producer's environment
// allowlist against the 2026-10-15 Nightly macOS Integration run (38723632845): dropping
// YOLO_NIX_HOST_DAEMON made the producer's own launch refuse the store-path install prefix on
// every Mac whose podman machine shares /nix, failing every test that prepared an npm program.
func TestProgramCaptureEnvironmentKeepsTheVMShareFacts(t *testing.T) {
	got := map[string]bool{}
	for _, item := range programCaptureEnvironment([]string{
		"YOLO_NIX_HOST_DAEMON=1",
		"YOLO_NIX_HOST_STORE_LINUX=1",
		"YOLO_REPO_ROOT=/must-not-select-runtime-source",
		"NPM_CONFIG_REGISTRY=https://example.invalid",
	}) {
		got[item] = true
	}
	for _, want := range []string{"YOLO_NIX_HOST_DAEMON=1", "YOLO_NIX_HOST_STORE_LINUX=1"} {
		if !got[want] {
			t.Errorf("the sealed producer's environment dropped %s", want)
		}
	}
	if got["YOLO_REPO_ROOT=/must-not-select-runtime-source"] {
		t.Error("the sealed producer inherited the obsolete runtime source selector")
	}
	if got["NPM_CONFIG_REGISTRY=https://example.invalid"] {
		t.Error("the sealed producer's environment kept npm configuration")
	}
}

// TestInternalLogAppendAdmitsTheMacosUserSidecarLayout is the 2026-10-15 macos-user run
// (38733308150): as the sandbox account, whose ~/.config links into <ws>/.yolo/home/config, the
// provisioning log writer found "yolo's user config directory" inside the workspace and refused
// every startup.log.
func TestInternalLogAppendAdmitsTheMacosUserSidecarLayout(t *testing.T) {
	home := sandboxAccountHome(t)
	ws := filepath.Join(resolvedDir(t), "ws")
	sidecarConfig := filepath.Join(paths.WorkspaceStateDir(ws), "home", "config")
	if err := os.MkdirAll(filepath.Join(sidecarConfig, "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sidecarConfig, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if rc := runInternalLogAppend([]string{ws, "startup.log", "--reset"}, bytes.NewBufferString("boot\n")); rc != 0 {
		t.Fatalf("log-append as the sandbox account returned %d, want 0", rc)
	}
	got, err := os.ReadFile(paths.WorkspaceLogPath(ws, "startup.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "boot\n" {
		t.Fatalf("startup.log = %q", got)
	}
	// The account's own home stays refused.
	if rc := runInternalLogAppend([]string{home, "startup.log"}, bytes.NewBufferString("x")); rc != 1 {
		t.Fatalf("log-append into the sandbox account's home returned %d, want 1", rc)
	}
}
