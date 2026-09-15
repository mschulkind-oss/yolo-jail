package prune

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestWorkspaceDedupeIncludesCodexStandalonePayloadOnly(t *testing.T) {
	want := filepath.Join("codex", "packages", "standalone")
	if !slices.Contains(dedupeSubtrees, want) {
		t.Fatalf("dedupeSubtrees = %v, missing %q", dedupeSubtrees, want)
	}
	if slices.Contains(dedupeSubtrees, "codex") {
		t.Fatalf("dedupeSubtrees = %v, includes mutable Codex state", dedupeSubtrees)
	}
}
