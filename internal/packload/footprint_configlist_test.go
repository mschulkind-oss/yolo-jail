package packload

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// A config-list CLAIMS the array it appends to
// (docs/reference/pack-system.md#config-list-visibility): one claim per contribution,
// targeting `agent/name#<pointer>` so two paths on one surface are two lines, with the entry
// count and the short list in the Detail. Never review-worthy (it reads nothing and runs
// nothing), and two packs appending to one array never COLLIDE — CombineOverlay, several
// contributors being the feature.
func TestFootprintClaimsConfigList(t *testing.T) {
	kilo := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages",
			Add: json.RawMessage(`["git:github.com/mschulkind/kilo-pi-provider"]`)},
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/extensions",
			Add: json.RawMessage(`["a","b","c","d","e"]`)},
	}}
	cs := claimSet(FootprintOf(pk("kilo", kilo)))

	one, ok := cs["config-list pi/settings#/packages"]
	if !ok {
		t.Fatalf("no config-list claim on pi/settings#/packages: %v", cs)
	}
	if !strings.Contains(one.Detail, "appends 1 entry") ||
		!strings.Contains(one.Detail, `"git:github.com/mschulkind/kilo-pi-provider"`) {
		t.Errorf("the claim's Detail must count and show the entry, got %q", one.Detail)
	}
	if !strings.Contains(one.Detail, "owner's entries kept") {
		t.Errorf("the claim must state the precedence, got %q", one.Detail)
	}
	if one.ReviewWorthy || one.RunsHostCode {
		t.Errorf("a config-list claim crosses nothing and must not be flagged: %+v", one)
	}

	many, ok := cs["config-list pi/settings#/extensions"]
	if !ok {
		t.Fatalf("the second path on the same surface must be its own claim: %v", cs)
	}
	if !strings.Contains(many.Detail, "appends 5 entries") || !strings.Contains(many.Detail, "… 2 more") ||
		strings.Contains(many.Detail, `"d"`) {
		t.Errorf("a long list must be counted and truncated, got %q", many.Detail)
	}

	empty := claimSet(FootprintOf(pk("noop", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages", Add: json.RawMessage(`[]`)},
	}})))
	if c := empty["config-list pi/settings#/packages"]; !strings.Contains(c.Detail, "no-op") {
		t.Errorf("an empty add must say it is a no-op, got %q", c.Detail)
	}

	other := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages",
			Add: json.RawMessage(`["npm:other"]`)},
	}}
	if cols := Collisions([]*Pack{pk("kilo", kilo), pk("other", other)}); len(cols) != 0 {
		t.Errorf("two packs appending to one array must not collide: %+v", cols)
	}
}
