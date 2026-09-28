package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestUpdateCheckValueFailsOpen(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		v    any
		want bool
	}{
		{"absent", false, nil, true},
		{"null", true, nil, true},
		{"true", true, true, true},
		{"false", true, false, false},
		// A malformed value is a `yolo check` error; the check itself keeps its default.
		{"malformed", true, "no", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := jsonx.NewOrderedMap()
			if c.set {
				cfg.Set(updateCheckKey, c.v)
			}
			if got := updateCheckValue(cfg); got != c.want {
				t.Errorf("updateCheckValue = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUpdateCheckProblem(t *testing.T) {
	if p := updateCheckProblem(false); p != "" {
		t.Errorf("false is fine, got %q", p)
	}
	if p := updateCheckProblem("off"); p == "" {
		t.Error("a string must be a problem")
	}
}

func TestValidateUpdateCheckScope(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"update_check":false}`)

	errs, _ := ValidateConfig(decode(t, `{"update_check":false}`), ws, nil)
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") {
		t.Fatalf("workspace update_check errors = %v, want user-scope-only refusal", errs)
	}

	for _, body := range []string{`{"update_check":true}`, `{"update_check":false}`, `{"update_check":null}`, `{}`} {
		errs, _ = ValidateConfig(decode(t, body), t.TempDir(), nil)
		if len(errs) != 0 {
			t.Errorf("validateUpdateCheck(%s) errors = %v", body, errs)
		}
	}
}

func TestValidateUpdateCheckType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	errs, _ := ValidateConfig(decode(t, `{"update_check":"no"}`), t.TempDir(), nil)
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "expected a boolean") {
		t.Fatalf("type errors = %v, want a boolean error", errs)
	}
}
