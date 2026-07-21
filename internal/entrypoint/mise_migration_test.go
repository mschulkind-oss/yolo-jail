package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMiseConfig seeds a persistent global mise config.toml and returns its path.
func writeMiseConfig(t *testing.T, home, content string) string {
	t.Helper()
	miseDir := filepath.Join(home, ".config", "mise")
	if err := os.MkdirAll(miseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(miseDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// useWorkspaceMise points workspaceMisePath at a fixture (or an absent path) for
// the duration of a test, so the migration cleanup does not read the live
// /workspace/mise.toml of the dev environment.
func useWorkspaceMise(t *testing.T, content string) {
	t.Helper()
	prev := workspaceMisePath
	t.Cleanup(func() { workspaceMisePath = prev })
	if content == "" {
		// An absent workspace mise.toml: no tool is workspace-pinned.
		workspaceMisePath = filepath.Join(t.TempDir(), "no-mise.toml")
		return
	}
	p := filepath.Join(t.TempDir(), "mise.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceMisePath = p
}

// TestMiseMigrationStripsStaleBakedRuntimes is the config-migration-to-prism
// §4.1 fix: an existing jail's config.toml carrying the old yolo-written default
// runtime lines (node/python/go) must have them stripped once those runtimes are
// baked (miseBaseTools empty), so the stale mise copy stops shadowing /bin/<tool>.
// Unrelated tools (neovim) are untouched.
func TestMiseMigrationStripsStaleBakedRuntimes(t *testing.T) {
	useWorkspaceMise(t, "") // no workspace pin
	home := t.TempDir()
	cfg := writeMiseConfig(t, home,
		"[tools]\nnode = \"22\"\npython = \"3.13\"\ngo = \"latest\"\nneovim = \"nightly\"\n")

	e := NewEnv(map[string]string{"HOME": home}) // no YOLO_MISE_TOOLS, no workspace pin
	if err := GenerateMiseConfig(e); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, stale := range []string{"node =", "python =", "go ="} {
		if strings.Contains(s, stale) {
			t.Errorf("stale baked runtime %q not stripped:\n%s", stale, s)
		}
	}
	if !strings.Contains(s, "neovim = \"nightly\"") {
		t.Errorf("unrelated tool neovim must be preserved:\n%s", s)
	}
}

// TestMiseMigrationPreservesInjectedPin: a runtime pinned via YOLO_MISE_TOOLS is
// an intentional override and must be preserved (and re-applied at its pinned
// version), not stripped by the migration cleanup.
func TestMiseMigrationPreservesInjectedPin(t *testing.T) {
	useWorkspaceMise(t, "") // no workspace pin; the pin comes from YOLO_MISE_TOOLS
	home := t.TempDir()
	cfg := writeMiseConfig(t, home, "[tools]\nnode = \"22\"\npython = \"3.13\"\n")

	// Workspace pins node via injected YOLO_MISE_TOOLS; python is NOT pinned.
	e := NewEnv(map[string]string{
		"HOME":            home,
		"YOLO_MISE_TOOLS": `{"node": "20"}`,
	})
	if err := GenerateMiseConfig(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	if !strings.Contains(s, `node = "20"`) {
		t.Errorf("intentional injected node pin must be preserved at its version:\n%s", s)
	}
	if strings.Contains(s, "python =") {
		t.Errorf("unpinned python should be stripped:\n%s", s)
	}
}

// TestMiseMigrationPreservesWorkspacePin: a runtime pinned in the workspace
// mise.toml is an intentional override; the cleanup must not strip it, while an
// unpinned baked runtime in the same config is still stripped.
func TestMiseMigrationPreservesWorkspacePin(t *testing.T) {
	// Workspace pins node (bare key) and go (via a "go:" backend line that must
	// NOT be confused with a bare `go` pin); python is unpinned.
	useWorkspaceMise(t, "[tools]\nnode = \"24\"\ngo = \"1.26\"\n")
	home := t.TempDir()
	cfg := writeMiseConfig(t, home, "[tools]\nnode = \"22\"\npython = \"3.13\"\ngo = \"latest\"\n")

	e := NewEnv(map[string]string{"HOME": home}) // no injected pin
	if err := GenerateMiseConfig(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	if !strings.Contains(s, "node =") {
		t.Errorf("workspace-pinned node must be preserved:\n%s", s)
	}
	if !strings.Contains(s, "go =") {
		t.Errorf("workspace-pinned go must be preserved:\n%s", s)
	}
	if strings.Contains(s, "python =") {
		t.Errorf("unpinned python should be stripped even alongside pinned runtimes:\n%s", s)
	}
}

// TestWorkspacePinsToolMatching: the pin detector matches a bare or quoted key
// at line start but not a substring (nodejs) or a backend key (go:...).
func TestWorkspacePinsToolMatching(t *testing.T) {
	useWorkspaceMise(t, "[tools]\nnode = \"24\"\n\"python\" = \"3.13\"\nnodejs_extra = \"x\"\n\"go:honnef.co/go/tools\" = \"latest\"\n")
	cases := map[string]bool{
		"node":   true,  // bare key
		"python": true,  // quoted key
		"go":     false, // only "go:..." backend present, not a bare go pin
		"ruby":   false, // absent
	}
	for tool, want := range cases {
		if got := workspacePinsTool(tool); got != want {
			t.Errorf("workspacePinsTool(%q) = %v, want %v", tool, got, want)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
