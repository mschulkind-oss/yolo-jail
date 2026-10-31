package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// RC-D43: each legacy pack spelling and the readable spelling it converts to, or why it has none.
func TestClassifyPackGivesEachLegacySpellingItsHandle(t *testing.T) {
	home := env(t)
	cases := []struct {
		entry      string
		handle     string // a string handle, or "git=<repo> ref=<ref>" for a table
		sourceKey  string
		wantReason string
	}{
		{entry: "claude", handle: "claude"},
		{entry: filepath.Join(home, ".config", "yolo-jail", "packs", "mine"), handle: "local/mine"},
		{entry: "~/.config/yolo-jail/packs/mine/", handle: "local/mine"},
		{entry: "file:///srv/packs/tools", handle: "tools", sourceKey: "dir:/srv/packs"},
		{entry: "~/src/packs/other", handle: "other", sourceKey: "dir:~/src/packs"},
		{entry: "git+https://github.com/acme/tools?ref=main", handle: "git=https://github.com/acme/tools ref=main"},
		{entry: "git+https://github.com/acme/packs//one?ref=v1", handle: "one", sourceKey: "git:https://github.com/acme/packs@v1"},
		{entry: "git+https://github.com/acme/packs//a/b?ref=v1", wantReason: "nested subdirectory"},
		{entry: "~/solo", wantReason: "directly under ~"},
		{entry: "relative/dir", wantReason: "absolute or start with ~/"},
	}
	for _, c := range cases {
		got := classifyPack(c.entry)
		if c.wantReason != "" {
			if !strings.Contains(got.reason, c.wantReason) {
				t.Errorf("%s: reason %q, want one naming %q", c.entry, got.reason, c.wantReason)
			}
			continue
		}
		if got.reason != "" {
			t.Errorf("%s: refused: %s", c.entry, got.reason)
			continue
		}
		handle, _ := got.handle.(string)
		if table, ok := got.handle.(*jsonx.OrderedMap); ok {
			repo, _ := table.Get("git")
			ref, _ := table.Get("ref")
			handle = "git=" + repo.(string) + " ref=" + ref.(string)
		}
		if handle != c.handle {
			t.Errorf("%s: handle %q, want %q", c.entry, handle, c.handle)
		}
		key := ""
		if got.source != nil {
			key = got.source.key
		}
		if key != c.sourceKey {
			t.Errorf("%s: source %q, want %q", c.entry, key, c.sourceKey)
		}
	}
}

// Two directories of packs with one base name get distinct source names, and a name yolo
// reserves is never one.
func TestSourceNamesAreDistinctAndNeverReserved(t *testing.T) {
	env(t)
	names := sourceNames([]any{"/a/packs/x", "/b/packs/y", "/c/local/z", "/a/packs/w"})
	if names["dir:/a/packs"] == names["dir:/b/packs"] {
		t.Errorf("two directories share the source name %q", names["dir:/a/packs"])
	}
	for key, n := range names {
		if n == "local" || n == "yolo" {
			t.Errorf("%s got the reserved source name %q", key, n)
		}
	}
}
