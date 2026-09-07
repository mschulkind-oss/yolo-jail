package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The reader's whole contract: true only for a literal true. Everything else —
// absent, false, wrong type, empty config — is OFF, because the key is an
// opt-IN to output and an unreadable answer must not start writing files into
// every jail the user opens.
func TestPerfLoggingValue(t *testing.T) {
	cases := []struct {
		name string
		val  any
		set  bool
		want bool
	}{
		{"absent", nil, false, false},
		{"true", true, true, true},
		{"false", false, true, false},
		{"null", nil, true, false},
		{"string true is not a bool", "true", true, false},
		{"number", 1.0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := jsonx.NewOrderedMap()
			if tc.set {
				cfg.Set(perfLoggingKey, tc.val)
			}
			if got := perfLoggingValue(cfg); got != tc.want {
				t.Errorf("perfLoggingValue(%v) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// The shape check names the type rather than silently ignoring a value the user
// clearly meant — "perf_logging": "yes" must not read as off with no comment.
func TestPerfLoggingProblem(t *testing.T) {
	if prob := perfLoggingProblem(true); prob != "" {
		t.Errorf("a bool is valid, got %q", prob)
	}
	prob := perfLoggingProblem("yes")
	if prob == "" || !strings.Contains(prob, "boolean") {
		t.Errorf("a string must be refused by naming the type, got %q", prob)
	}
}

// The end-to-end user-scope read, through the real HOME lookup — the path
// PerfLoggingEnabled actually takes on a launch.
func TestPerfLoggingEnabledReadsTheUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(UserLayerEnv, "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if PerfLoggingEnabled() {
		t.Error("no config at all must read as off")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"),
		[]byte("{\n  // turn on the full timing logging\n  \"perf_logging\": true\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !PerfLoggingEnabled() {
		t.Error(`"perf_logging": true in the user config must enable timing`)
	}
}
