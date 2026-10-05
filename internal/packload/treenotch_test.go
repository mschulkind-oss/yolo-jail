package packload

// treenotch_test.go pins PPX-D34 and PPX-D35 (docs/design/patched-extensions.md): a list entry under
// `~/<into>/` loads the tree, for the owning agent pack, where it reaches, and the lint, and a tree is
// delivered only at a notch its owner's entry reaches.

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// PPX-D35: `~/<into>`, the same with a trailing slash, and a path inside it name the owner, reach
// where their list reaches, and silence the lint; a sibling, an escape through `..`, and a path
// that is not home-relative do none of it.
func TestAListEntryUnderTheTreeLoadsIt(t *testing.T) {
	pi := embeddedPack(t, "pi")
	for _, tc := range []struct {
		entry string
		loads bool
	}{
		{"~/" + treeInto, true},
		{"~/" + treeInto + "/", true},
		{"~/" + treeInto + "/packages/session-name", true},
		{"~/" + treeInto + "//packages/./footer/", true},
		{"~/" + treeInto + "-other", false},
		{"~/" + treeInto + "/../pi-other", false},
		{"/home/agent/" + treeInto, false},
		{"git:github.com/me/pi-subagents", false},
	} {
		matt := agentPack(t, "matt", patchedTreeContribution(), piPackagesList(tc.entry))
		f := PatchedTrees([]*Pack{pi, matt})[0]
		if got := f.Owner == "pi" && f.ListedInJail && f.ListedAtHost; got != tc.loads {
			t.Errorf("entry %q: owner %q (jail %v, host %v), want loading it %v", tc.entry, f.Owner,
				f.ListedInJail, f.ListedAtHost, tc.loads)
		}
		if linted := len(LintPatchedTrees(matt)) != 0; linted == tc.loads {
			t.Errorf("entry %q: linted %v, want %v", tc.entry, linted, !tc.loads)
		}
	}
}

// A posture list under the tree reaches its posture's notch alone, as `~/<into>` itself does.
func TestAGuardedEntryUnderTheTreeReachesTheHostAlone(t *testing.T) {
	pi := embeddedPack(t, "pi")
	add, _ := json.Marshal([]string{"~/" + treeInto + "/packages/a"})
	guarded := packdecl.Contribution{Kind: packdecl.KindAutonomy, Guarded: &packdecl.AutonomyPosture{
		Lists: []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: add}}}}
	f := PatchedTrees([]*Pack{pi, agentPack(t, "matt", patchedTreeContribution(), guarded)})[0]
	if f.Owner != "pi" || f.ListedInJail || !f.ListedAtHost {
		t.Errorf("a guarded entry under the tree: owner %q, jail %v, host %v; want pi at the host alone",
			f.Owner, f.ListedInJail, f.ListedAtHost)
	}
}

// PPX-D34: a tree is delivered where its owner's entry reaches; one with no owner says nothing about
// where it loads, and is delivered everywhere; a fork of a program is not this rule's.
func TestATreeIsDeliveredWhereItsOwnersEntryReaches(t *testing.T) {
	for _, tc := range []struct {
		name           string
		f              Fork
		inJail, atHost bool
	}{
		{"a config-list", Fork{Into: treeInto, Owner: "pi", ListedInJail: true, ListedAtHost: true}, true, true},
		{"a guarded list", Fork{Into: treeInto, Owner: "pi", ListedAtHost: true}, false, true},
		{"an autonomous list", Fork{Into: treeInto, Owner: "pi", ListedInJail: true}, true, false},
		{"no owner", Fork{Into: treeInto}, true, true},
		{"a fork of a program", Fork{Bin: "pi", Base: "pi"}, true, true},
	} {
		if got := tc.f.DeliveredInJail(); got != tc.inJail {
			t.Errorf("%s: DeliveredInJail = %v, want %v", tc.name, got, tc.inJail)
		}
		if got := tc.f.DeliveredAtHost(); got != tc.atHost {
			t.Errorf("%s: DeliveredAtHost = %v, want %v", tc.name, got, tc.atHost)
		}
	}
}
