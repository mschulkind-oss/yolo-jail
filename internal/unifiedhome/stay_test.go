package unifiedhome

import (
	"fmt"
	"testing"
)

// TestMergeLeavesWhatStaysAndComposesItsPrefixes: a path Stay names (a generated file, a carve-out)
// is never merged out of a source, and a directory that is only a prefix of one is merged child by
// child, so pi's shared sessions move while its private install tree stays where it is.
func TestMergeLeavesWhatStaysAndComposesItsPrefixes(t *testing.T) {
	src, dst := tree(t), tree(t)
	put(t, src, ".pi/agent/sessions/s.jsonl", "s")
	put(t, src, ".pi/agent/npm/pkg/index.js", "elf")
	put(t, src, ".pi/agent/settings.json", "generated")
	put(t, src, ".pi/trust.json", "t")
	rep, err := Merge(open(t, dst), ".pi", Source{Label: "x", Root: open(t, src), Dirs: []string{".pi"},
		Stay: []string{"agent/npm", "agent/settings.json"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		".pi/agent/sessions/s.jsonl": "s",
		".pi/trust.json":             "t",
	}
	if got := snapshot(t, dst); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unified tree = %v, want %v", got, want)
	}
	left := map[string]string{
		".pi/agent/npm/pkg/index.js": "elf",
		".pi/agent/settings.json":    "generated",
	}
	if got := snapshot(t, src); fmt.Sprint(got) != fmt.Sprint(left) {
		t.Fatalf("source tree = %v, want what stays %v", got, left)
	}
	if rep.Moved != 2 || len(rep.Left) != 0 {
		t.Fatalf("report = %+v, want 2 moved and nothing counted as left (a path that stays is not an error)", rep)
	}
}

// TestMergeTakesAWholeHomeWhenTheDirectoryIsDot: the legacy home's remainder merges into a
// populated slice as a whole home, "." on both sides.
func TestMergeTakesAWholeHomeWhenTheDirectoryIsDot(t *testing.T) {
	src, dst := tree(t), tree(t)
	put(t, src, "local/bin/y", "y")
	put(t, src, "bash_history", "h")
	put(t, dst, "local/bin/x", "x")
	put(t, dst, "bash_history", "other")
	rep, err := Merge(open(t, dst), ".", Source{Label: "legacy", Root: open(t, src), Dirs: []string{"."}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot(t, dst)
	if got["local/bin/x"] != "x" || got["local/bin/y"] != "y" {
		t.Fatalf("the union of the two local trees is missing: %v", got)
	}
	if rep.Moved != 2 || rep.KeptBoth != 1 {
		t.Fatalf("report = %+v, want 2 moved (y and the clashing file), 1 kept twice", rep)
	}
	if got["bash_history"] == "" {
		t.Fatalf("a clash kept neither file: %v", got)
	}
}
