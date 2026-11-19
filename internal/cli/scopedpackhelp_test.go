package cli

import (
	"strings"
	"testing"
)

func TestPackHelpExplainsScopedEditingAndInspectionBoundary(t *testing.T) {
	rc, out, stderr := dispatchScopedPackCapture(t, []string{"--help"})
	if rc != 0 {
		t.Fatalf("pack help: %d %s", rc, stderr)
	}
	for _, want := range []string{"--skills-dir", "--only", "--subdir", "--exclude", "--dry-run", "selection not yet verified", "lint and footprint", "user TOML"} {
		if !strings.Contains(out, want) {
			t.Errorf("actual pack help misses %q", want)
		}
	}
}

func TestConfigRefExplainsScopedModesAndLegacySeparation(t *testing.T) {
	for _, want := range []string{"skills.dir", "skills.only", "skills.exclude", "subdir", "markerless", "determined at publication", "whole-entry readiness", "not legacy file globs"} {
		if !strings.Contains(configRefText(t), want) {
			t.Errorf("config-ref misses %q", want)
		}
	}
}
