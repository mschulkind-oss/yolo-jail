package packload

import (
	"strings"
	"testing"
)

// The two rows of docs/design/pack-naming.md §3.4.1's second table that need no skill read: a
// namespace word two entries share, and a landing two packs share. Each names both entries and
// the step.
func TestNameCollisionsRefuseASharedWordAndASharedLanding(t *testing.T) {
	alice := &Pack{Name: "git:alice/tools", Namespace: "tools", NamespaceOwner: "repo:alice/tools", Landing: "tools"}
	acmeTools := &Pack{Name: "acme/tools", Namespace: "acme", NamespaceOwner: "source:acme", Landing: "acme-tools"}
	acmeLint := &Pack{Name: "acme/lint", Namespace: "acme", NamespaceOwner: "source:acme", Landing: "acme-lint"}
	claude := &Pack{Name: "claude", Official: true}

	if cols := NameCollisions([]*Pack{claude, alice, acmeTools, acmeLint}); len(cols) != 0 {
		t.Errorf("two packs of one source, or a shipped pack, were refused: %v", cols)
	}

	// Drop bob's namespace: both repositories default to `tools`.
	bob := &Pack{Name: "git:bob/tools", Namespace: "tools", NamespaceOwner: "repo:bob/tools", Landing: "tools-2"}
	cols := NameCollisions([]*Pack{alice, bob})
	if len(cols) != 1 || !strings.Contains(cols[0], "git:alice/tools") || !strings.Contains(cols[0], "git:bob/tools") ||
		!strings.Contains(cols[0], "namespace = ") {
		t.Errorf("a shared namespace word = %v", cols)
	}
	// `local/acme` beside the source `acme`: the source's step is the rename verb.
	localAcme := &Pack{Name: "local/acme", Namespace: "acme", NamespaceOwner: "local:acme", Landing: "acme"}
	cols = NameCollisions([]*Pack{acmeTools, localAcme})
	if len(cols) != 1 || !strings.Contains(cols[0], "renaming the [pack_sources] key acme") {
		t.Errorf("a local pack named like a source = %v", cols)
	}

	// Add `{ pack = "local/tools", bare = true }`: its landing `tools` is alice's.
	localTools := &Pack{Name: "local/tools", Landing: "tools"}
	cols = NameCollisions([]*Pack{alice, localTools})
	if len(cols) != 1 || !strings.Contains(cols[0], `"tools"`) || !strings.Contains(cols[0], "local/tools") ||
		!strings.Contains(cols[0], "drop `bare`") {
		t.Errorf("a shared landing = %v", cols)
	}
}
