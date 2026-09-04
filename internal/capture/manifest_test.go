package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The manifest round-trips through disk unchanged, and it lands at the documented name in the
// ENTRY root — the path slice 4's materialize and slice 5's GC will both address it by.
func TestManifestRoundTripsAtTheDocumentedPath(t *testing.T) {
	root := t.TempDir()
	m := &Manifest{
		Schema:   ManifestSchema,
		Home:     "/var/folders/yolo-capture-staging",
		Surfaces: []string{".npm-global", ".local", "go"},
		Entries: []ManifestEntry{
			{Path: ".local/bin", Kind: KindDir, Mode: "0755"},
			{Path: ".local/bin/claude", Kind: KindSymlink, Target: "/var/folders/yolo-capture-staging/.local/share/claude/versions/1/claude"},
			{Path: ".local/share/claude/versions/1/claude", Kind: KindFile, Mode: "0755", Size: 12},
		},
		AbsoluteRefs: []AbsoluteRef{{
			Path:  ".local/bin/claude",
			Kind:  RefSymlinkTarget,
			Value: "/var/folders/yolo-capture-staging/.local/share/claude/versions/1/claude",
		}},
	}
	must(t, WriteManifest(root, m))

	if got, want := ManifestPath(root), filepath.Join(root, "capture-manifest.json"); got != want {
		t.Errorf("ManifestPath = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(TreeDir(root), ManifestName)); !os.IsNotExist(err) {
		t.Errorf("the manifest must live BESIDE tree/, not inside it: %v", err)
	}
	back, err := ReadManifest(root)
	must(t, err)
	if len(back.Entries) != 3 || back.Entries[1].Target != m.Entries[1].Target ||
		back.Home != m.Home || len(back.AbsoluteRefs) != 1 {
		t.Errorf("round trip lost data: %+v", back)
	}
}

// A manifest written by a NEWER yolo is a hard error, not a best-effort parse — the same
// discipline packsrc's lockfile takes, for the same reason: an unknown field may change what
// the known ones mean, and materialize acts on this file.
func TestReadManifestRefusesAHigherSchema(t *testing.T) {
	root := t.TempDir()
	raw, err := json.Marshal(map[string]any{"schema": ManifestSchema + 1, "home": "/home/agent"})
	must(t, err)
	must(t, os.WriteFile(ManifestPath(root), raw, 0o644))

	_, rerr := ReadManifest(root)
	if rerr == nil || !strings.Contains(rerr.Error(), "upgrade yolo") {
		t.Fatalf("want a refusal naming the upgrade, got %v", rerr)
	}
}

// underPrefix is component-aware in both directions: a sibling directory whose name merely
// starts with the prefix is not a reference into it, and a relative target is never one at all
// (it is already relocatable, and rewriting it would break the very thing it got right).
func TestUnderPrefixIsComponentAwareAndIgnoresRelativeTargets(t *testing.T) {
	cases := []struct {
		prefix, ref string
		want        bool
	}{
		{"/home/agent", "/home/agent/.local/bin/x", true},
		{"/home/agent", "/home/agent", true},
		{"/home/agent", "/home/agentx/.local/bin/x", false},
		{"/home/agent", "../share/x", false},
		{"/home/agent", "x", false},
		{"", "/home/agent/x", false},
	}
	for _, tc := range cases {
		if got := underPrefix(tc.prefix, tc.ref); got != tc.want {
			t.Errorf("underPrefix(%q, %q) = %v, want %v", tc.prefix, tc.ref, got, tc.want)
		}
	}
}
