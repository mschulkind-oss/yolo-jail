package entrypoint

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvPlatformDefaults: an Env built the normal way (container) reports the
// Linux container values, so existing generators are unchanged. This guards
// against the J2 platform-threading accidentally flipping the Linux default.
func TestEnvPlatformDefaults(t *testing.T) {
	e := NewEnv(map[string]string{"HOME": "/home/agent"})
	if got := e.WorkspaceDir(); got != "/workspace" {
		t.Errorf("WorkspaceDir() = %q, want /workspace", got)
	}
	if got := e.ShimBinPath(); got != "/bin" {
		t.Errorf("ShimBinPath() = %q, want /bin", got)
	}
	if !e.GNUStat {
		t.Error("GNUStat should default true (Linux container)")
	}
}

// TestEnvWorkspaceOverride: a non-container workspace (the macos-user case)
// flows through to a generator that reads WorkspaceDir — proving the literal is
// no longer hardcoded. Uses the .claude.json projects key as the witness. The
// workspace is a real temp dir (not a bare /Users path) because the prism now
// materializes §5 sidecars under <workspace>/.yolo/prism/ during the render.
func TestEnvWorkspaceOverride(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "Users", "dev", "proj")
	e := NewEnv(map[string]string{"HOME": home})
	e.Workspace = ws

	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, e.ClaudeJSONPath()))
	if !strings.Contains(got, ws) {
		t.Errorf(".claude.json should key projects on the overridden workspace:\n%s", got)
	}
	if strings.Contains(got, `"/workspace"`) {
		t.Errorf(".claude.json still contains hardcoded /workspace:\n%s", got)
	}
}

// TestEnvShimBinOverride: a macOS shim exec's /usr/bin/<tool>, not /bin/<tool>.
func TestEnvShimBinOverride(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"HOME":              home,
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","block_flags":["-r"],"message":"no","suggestion":"rg"}]`,
	})
	e.ShimBinDir = "/usr/bin"

	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, e.ShimDir()+"/grep"))
	if !strings.Contains(got, "/usr/bin/grep") {
		t.Errorf("shim should exec /usr/bin/grep on macOS:\n%s", got)
	}
	if strings.Contains(got, "/bin/grep") && !strings.Contains(got, "/usr/bin/grep") {
		t.Errorf("shim still hardcodes /bin/grep:\n%s", got)
	}
}
