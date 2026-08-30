package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestHostEnvWarnsOncePerMissingFile pins the one-pass call: hostEnvVars must produce
// ONE warning for a missing dotenv file, not two. The two-call form (ResolveEnvSources
// + EnvSourceRemovals) ran the shared pass twice, so every launch printed the same
// "file not found" line twice — observed live in a nested jail 2026-08-30. The entry
// here is ABSOLUTE so the host-notch backstop stays out of the way: it would add its own
// (correct, different) warning for an unanchored relative entry, which is a separate
// fact from the one this test counts.
func TestHostEnvWarnsOncePerMissingFile(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	cfg := jsonx.NewOrderedMap()
	cfg.Set("env_sources", []any{"/nonexistent/absolute.env"})

	var warnings []string
	hostEnvVars(cfg, t.TempDir(), "claude", "", func(msg string) { warnings = append(warnings, msg) })
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, "/nonexistent/absolute.env") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d warnings for one missing file, want exactly 1: %q", n, warnings)
	}
}
