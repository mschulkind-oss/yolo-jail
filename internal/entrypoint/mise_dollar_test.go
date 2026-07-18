package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMiseInjectedVersionWithDollar is the audit §C regression: an injected mise
// version containing `$` must be written VERBATIM. Go's ReplaceAllString would
// expand `$1`/`$name` in the replacement and corrupt it; ReplaceAllLiteralString
// (matching Python's re.sub, which never expands `$`) preserves it.
func TestMiseInjectedVersionWithDollar(t *testing.T) {
	home := t.TempDir()
	miseDir := filepath.Join(home, ".config", "mise")
	if err := os.MkdirAll(miseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(miseDir, "config.toml")
	// Pre-existing config with a `node` line so the override (re-sub) path fires.
	if err := os.WriteFile(cfg, []byte("[tools]\nnode = \"20\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Inject node at a version containing `$1` and `$name` — the exact tokens Go
	// would misinterpret as capture-group refs.
	e := NewEnv(map[string]string{
		"HOME":            home,
		"YOLO_MISE_TOOLS": `{"node": "1.2.3-$1-${name}-$"}`,
	})
	if err := GenerateMiseConfig(e); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `node = "1.2.3-$1-${name}-$"`) {
		t.Errorf("dollar-version corrupted:\n%s", got)
	}
}
