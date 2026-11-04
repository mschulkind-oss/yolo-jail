package configmigrate

import (
	"reflect"
	"strings"
	"testing"
)

// LC-D5: the crossing's lowering respells only the values a legacy file wrote. A TOML-written
// handle crosses verbatim in its place, a legacy path entry is respelled in its place with the
// pack source it needs, and nothing is left out. Fails if the lowering reads a TOML handle as a
// legacy relative path again (it was then reported and dropped).
func TestTheInheritedLoweringRespellsOnlyLegacyPackEntries(t *testing.T) {
	env(t)
	values := mustJSON(t, `{"packs": ["sample-packs/house-rules", "~/other-packs/lab-tools", "pi"],
	  "pack_sources": {"sample-packs": {"path": "~/sample-packs", "bare": true}}}`)
	lowered, omitted := LowerInheritedUserScope(values, func(path string) bool {
		return path == "packs[0]" || path == "packs[2]" || strings.HasPrefix(path, "pack_sources")
	})
	if len(omitted) > 0 {
		t.Fatalf("the lowering left out part of a scope that has a TOML spelling: %v", omitted)
	}
	got, _ := lowered.Get("packs")
	if want := []any{"sample-packs/house-rules", "other-packs/lab-tools", "pi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packs = %#v, want %#v", got, want)
	}
	sources, _ := mapAt(lowered, "pack_sources")
	if sources == nil {
		t.Fatal("no pack_sources")
	}
	for _, name := range []string{"sample-packs", "other-packs"} {
		if _, ok := sources.Get(name); !ok {
			t.Errorf("pack_sources lacks %s: %v", name, sources.Keys())
		}
	}
}

// LC-D6: a legacy loopholes block crosses as nothing. Its switches and settings go silently; an
// inline declaration, which would change a launch inside the jail, goes with one disclosure. It
// never declines, and no `allow` table is made from the block. Fails if the grant-declaring
// decline comes back, or if the block (or its `allow` reading) reaches the lowered scope.
func TestTheInheritedLoweringLeavesTheLoopholesBlockOut(t *testing.T) {
	env(t)
	values := mustJSON(t, `{"packs": ["journal", "host-processes", "aws-auth"],
	  "loopholes": {
	    "journal": {"enabled": true},
	    "host-processes": {"enabled": true, "settings": {"visible": ["postgres"], "fields": ["pid"]}},
	    "aws-auth": {"enabled": true, "settings": {"profile": "stand-in"}},
	    "lab-tool": {"command": ["/opt/lab/serve"], "description": "an inline loophole"}}}`)
	lowered, omitted := LowerInheritedUserScope(values, nil)
	for _, key := range []string{"loopholes", "allow", "requests"} {
		if _, ok := lowered.Get(key); ok {
			t.Errorf("the lowered scope holds %q: %v", key, lowered.Keys())
		}
	}
	if len(omitted) != 1 || omitted[0].Path != "config.loopholes.lab-tool" || !strings.Contains(omitted[0].Reason, "yolo pack init") {
		t.Fatalf("want one disclosure, for the inline declaration, got %+v", omitted)
	}
}
