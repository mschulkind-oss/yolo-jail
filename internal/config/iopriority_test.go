package config

import (
	"strings"
	"testing"
)

// TestResourcesIoIsAKnownKeyInBothForms drives the whole validator, so deleting `io` from
// knownResourcesKeys fails it as surely as a broken reader does: the key was an unknown-key
// error until it existed (docs/design/io-priority.md §4, "State that exists").
func TestResourcesIoIsAKnownKeyInBothForms(t *testing.T) {
	for _, body := range []string{
		`"low"`, `"idle"`, `"normal"`, `null`, `{}`,
		`{"priority": "low"}`, `{"priority": "idle"}`, `{"priority": null}`,
	} {
		errs, _ := ValidateConfig(decode(t, `{"resources": {"memory": "8g", "io": `+body+`}}`), t.TempDir(), nil)
		if len(errs) > 0 {
			t.Errorf("resources.io = %s refused: %v", body, errs)
		}
	}
}

// TestResourcesIoRefusalsReachTheValidator: every refusal internal/ioprio's Parse reports
// is a validation error at the full config path — so a launch refuses it and `yolo check`
// reports it as a [FAIL], instead of a value the launcher then reads as "normal".
func TestResourcesIoRefusalsReachTheValidator(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`""`, `config.resources.io: expected "idle", "low" or "normal"`},
		{`"Low"`, `config.resources.io: expected "idle", "low" or "normal" (got "Low")`},
		{`2`, `config.resources.io: expected a string`},
		{`{"priority": "background"}`, `config.resources.io.priority: expected`},
		{`{"weight": 500}`, `config.resources.io.weight: unknown key`},
	} {
		errs, _ := ValidateConfig(decode(t, `{"resources": {"io": `+tc.body+`}}`), t.TempDir(), nil)
		if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("resources.io = %s: errs %v, want one containing %q", tc.body, errs, tc.want)
		}
	}
}
