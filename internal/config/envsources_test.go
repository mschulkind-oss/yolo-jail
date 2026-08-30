package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// writeEnvFile writes a dotenv file under dir and returns its path.
func writeEnvFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runEnvSources is the pair of calls hostEnvVars makes (internal/cli/host.go): the
// assignment map and the removal list, read from the SAME config. The one-pass invariant
// underneath them is exactly that the two answers cannot disagree, so the test drives
// them as the production caller does — through both exported functions.
func runEnvSources(t *testing.T, workspace string, entries []any) (assigned map[string]string, removed []string) {
	t.Helper()
	cfg := jsonx.NewOrderedMap()
	cfg.Set("env_sources", entries)
	merged := ResolveEnvSources(workspace, cfg, func(string) {})
	assigned = map[string]string{}
	for _, k := range merged.Keys() {
		v, _ := merged.Get(k)
		if s, ok := v.(string); ok {
			assigned[k] = s
		}
	}
	return assigned, EnvSourceRemovals(workspace, cfg, func(string) {})
}

// TestEnvSourcesRemovalAndAssignmentAgreeAcrossKinds pins the ordering contract ONE pass
// guarantees and two separate scans cannot: later entries win for BOTH answers, across
// entry KINDS. The second row is the bug the split-pass version shipped — a dotenv FILE
// listed after an inline null assigned the variable in the map while the inline-only
// removal scan still fired, so the unset silently beat the later assignment.
//
// Deleting the shared resolveEnvSources call (back to dict-only removal scanning) fails
// rows 1 and 2; deleting the removal support entirely fails rows 3 and 4.
func TestEnvSourcesRemovalAndAssignmentAgreeAcrossKinds(t *testing.T) {
	dir := t.TempDir()
	dotenv := writeEnvFile(t, dir, "vars.env", "X=yes\n")
	inline := func(kv map[string]any) *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		for k, v := range kv {
			m.Set(k, v)
		}
		return m
	}

	tests := []struct {
		name         string
		entries      []any
		wantAssigned map[string]string
		wantRemoved  bool
	}{
		{
			name:         "dotenv after inline null cancels the removal",
			entries:      []any{inline(map[string]any{"X": nil}), dotenv},
			wantAssigned: map[string]string{"X": "yes"},
			wantRemoved:  false,
		},
		{
			name:         "inline null after dotenv removes it",
			entries:      []any{dotenv, inline(map[string]any{"X": nil})},
			wantAssigned: map[string]string{},
			wantRemoved:  true,
		},
		{
			name:         "inline assignment after inline null cancels the removal",
			entries:      []any{inline(map[string]any{"X": nil}), inline(map[string]any{"X": "set"})},
			wantAssigned: map[string]string{"X": "set"},
			wantRemoved:  false,
		},
		{
			name:         "inline null after inline assignment removes it",
			entries:      []any{inline(map[string]any{"X": "set"}), inline(map[string]any{"X": nil})},
			wantAssigned: map[string]string{},
			wantRemoved:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assigned, removed := runEnvSources(t, dir, tt.entries)
			for k, want := range tt.wantAssigned {
				if got := assigned[k]; got != want {
					t.Errorf("%s = %q in assignment map, want %q", k, got, want)
				}
			}
			if len(assigned) != len(tt.wantAssigned) {
				t.Errorf("assignment map = %v, want exactly %v", assigned, tt.wantAssigned)
			}
			if got := slices.Contains(removed, "X"); got != tt.wantRemoved {
				t.Errorf("X in removals = %v (%v), want %v", got, removed, tt.wantRemoved)
			}
		})
	}
}

// TestValidateEnvSourcesAcceptsNullAsRemoval: null is the unset spelling, so accepting it
// in validation is what makes the feature configurable at all — before this, the exact
// config the removal feature requires was a `yolo check` error.
func TestValidateEnvSourcesAcceptsNullAsRemoval(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("AWS_PROFILE", nil)
	entry.Set("AWS_REGION", "us-east-1")
	entry.Set("AWS_TIMEOUT", float64(30)) // neither a string nor null
	cfg.Set("env_sources", []any{entry})

	var errs []string
	validateEnvSources(cfg, &errs)

	var badValue bool
	for _, e := range errs {
		if strings.Contains(e, "AWS_PROFILE") {
			t.Errorf("null must validate as the removal spelling, got: %s", e)
		}
		if strings.Contains(e, "AWS_TIMEOUT") {
			badValue = true
			if !strings.Contains(e, "null to unset") {
				t.Errorf("error for a non-string value should name the null alternative: %s", e)
			}
		}
	}
	if !badValue {
		t.Error("AWS_TIMEOUT=30 must still be rejected — only string-or-null is valid")
	}
}
