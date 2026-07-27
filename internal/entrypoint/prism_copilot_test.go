package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B1 end-to-end through the real BOOT WRITER (not just the engine): a live
// copilot config.json with an OAuth token, and no last_render sidecar — the exact
// data-loss scenario. ConfigureCopilotPrism must preserve the token.
func TestB1CopilotBootPreservesTokenOnFirstMigration(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"copilot_tokens":{"gh":"SECRET"},"logged_in_users":["ada"],"last_logged_in_user":"ada"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"copilot_tokens", "SECRET", "logged_in_users", "last_logged_in_user"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("first-migration boot lost %q — the OAuth wipe:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), `"yolo": true`) && !strings.Contains(string(got), `"yolo":true`) {
		t.Errorf("yolo default missing:\n%s", got)
	}
}
