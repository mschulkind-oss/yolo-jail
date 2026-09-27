package packoverlay

// posturelist_test.go pins the rest of the POSTURE LIST's collection
// (docs/design/notch-scoped-config-contributions.md §4.1): order among a pack's plain
// config-lists, the malformed case, and the ownerless case — each at both values of the bit.
// Which posture the bit selects is autonomyinert_test.go's.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ORDER. A posture's lists stand at the autonomy contribution's position in `contributes`
// (pack-system.md#config-list-order: pack order, then declaration order), so an entry a pack
// declares before its autonomy contribution appends before the posture's, and one declared
// after appends after — at whichever posture the notch selects.
func TestPostureListsFoldAtTheirDeclarationPosition(t *testing.T) {
	matt := &packload.Pack{Name: "matt", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		listContribution("claude/settings", "/extra", `["before"]`),
		{Kind: packdecl.KindAutonomy,
			Autonomous: &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{
				{Surface: "claude/settings", Path: "/extra", Add: json.RawMessage(`["jail-posture"]`)}}},
			Guarded: &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{
				{Surface: "claude/settings", Path: "/extra", Add: json.RawMessage(`["host-posture"]`)}}}},
		listContribution("claude/settings", "/extra", `["after"]`),
	}}}
	later := listPack("later", listContribution("claude/settings", "/extra", `["later-pack"]`))
	packs := []*packload.Pack{ownerPack("claude"), matt, later}

	for autonomy, middle := range map[bool]string{true: "jail-posture", false: "host-posture"} {
		var order []string
		for _, l := range Collect(packs, autonomy, nil).ListsFor("claude", "settings") {
			order = append(order, l.Add[0].(string))
		}
		want := []string{"before", middle, "after", "later-pack"}
		if !reflect.DeepEqual(order, want) {
			t.Errorf("autonomy=%v: fold order = %v, want %v", autonomy, order, want)
		}
	}
}

// A MALFORMED posture list is a Problem at EVERY notch — including the one whose posture it
// is not — and is never placed. The gate sits after the decode for exactly this: an author
// whose host-only entry is broken hears it from a jail boot, not only from the host they have
// not applied yet. The problem leads with the kind as written, which is what apply.go's
// collectProblemKind reads.
func TestAMalformedPostureListIsAProblemAtBothNotches(t *testing.T) {
	packs := []*packload.Pack{
		ownerPack("claude"),
		postureListPack("bad", nil, []packdecl.PostureList{
			{Surface: "not-an-id", Path: "/x", Add: json.RawMessage(`["a"]`)},
			{Surface: "claude/settings", Path: "", Add: json.RawMessage(`["a"]`)},
			{Surface: "claude/settings", Path: "/x", Add: json.RawMessage(`"a"`)},
		}),
	}
	for _, autonomy := range []bool{true, false} {
		set := Collect(packs, autonomy, nil)
		if len(set.Problems) != 3 {
			t.Fatalf("autonomy=%v: problems = %v, want three — the unselected posture's "+
				"malformed lists too", autonomy, set.Problems)
		}
		for _, p := range set.Problems {
			if !strings.HasPrefix(p, "pack bad: autonomy guarded.lists") {
				t.Errorf("autonomy=%v: problem %q does not lead with the kind and posture the "+
					"author wrote", autonomy, p)
			}
		}
		if set.ListsFor("claude", "settings") != nil {
			t.Errorf("autonomy=%v: a malformed posture list was placed", autonomy)
		}
	}
}

// AN OWNERLESS posture list is inert and reported (R2) at a notch that SELECTS its posture,
// led by the kind the author wrote and naming the shipped owner — and at a notch that does not,
// it is not reported at all, because the reason it contributed nothing there is the notch.
func TestAnOwnerlessPostureListIsAnOrphanOnlyWhereItsPostureIsSelected(t *testing.T) {
	packs := []*packload.Pack{
		postureListPack("matt", nil, []packdecl.PostureList{
			{Surface: "pi/settings", Path: "/packages", Add: json.RawMessage(`["npm:automode"]`)}}),
	}
	host := Collect(packs, false, nil)
	if len(host.Orphans) != 1 {
		t.Fatalf("at the guarded posture: orphans = %+v, want the one posture list", host.Orphans)
	}
	o := host.Orphans[0]
	if o.KindName() != string(packdecl.KindAutonomy) || o.Pack != "matt" || o.Target != "pi/settings" {
		t.Errorf("orphan = %+v, want kind autonomy, pack matt, target pi/settings", o)
	}
	if !strings.Contains(o.Reason(), "`pi` pack is not selected") {
		t.Errorf("reason = %q, want the shipped owner named", o.Reason())
	}
	if jail := Collect(packs, true, nil); len(jail.Orphans) != 0 || len(jail.Problems) != 0 {
		t.Errorf("at the autonomous posture a guarded list is a clean skip, not an orphan: "+
			"orphans=%+v problems=%v", jail.Orphans, jail.Problems)
	}
}

// Onto a CORE surface: inert, and the sentence is about the posture list, not about the
// autonomy kind — whose own config patch does contribute only to its own pack's surfaces.
func TestAPostureListOntoACoreSurfaceSaysWhatItIs(t *testing.T) {
	set := Collect([]*packload.Pack{
		postureListPack("matt", nil, []packdecl.PostureList{
			{Surface: "mise/config", Path: "/tools", Add: json.RawMessage(`["x"]`)}}),
	}, false, nil)
	if len(set.Orphans) != 1 || !set.Orphans[0].CoreOwned {
		t.Fatalf("orphans = %+v, want one core-owned orphan", set.Orphans)
	}
	if r := set.Orphans[0].Reason(); !strings.Contains(r, "a posture list contributes to a surface a pack owns") {
		t.Errorf("reason = %q", r)
	}
}
