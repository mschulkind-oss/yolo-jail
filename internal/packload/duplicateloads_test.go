package packload

// duplicateloads_test.go pins the DUPLICATE-LOAD LINT (duplicateloads.go;
// docs/design/patched-extensions.md PPX-D34): a list holding a patched extension's `~/<into>` and a
// `git:` or `npm:` entry of the same final name loads the extension twice, and the lint names the
// entry to drop; an entry of another name, a local path, another list or a posture the tree's
// entry never shares a notch with is no duplicate.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// piPackagesListOf is a config-list on pi's packages whose entries may be objects.
func piPackagesListOf(entries ...any) packdecl.Contribution {
	add, _ := json.Marshal(entries)
	return packdecl.Contribution{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages", Add: add}
}

func postureList(guarded bool, entries ...string) packdecl.Contribution {
	add, _ := json.Marshal(entries)
	posture := &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: add}}}
	if guarded {
		return packdecl.Contribution{Kind: packdecl.KindAutonomy, Guarded: posture}
	}
	return packdecl.Contribution{Kind: packdecl.KindAutonomy, Autonomous: posture}
}

// THE REQUEST'S CASE, and each remote spelling of the same package: the lint names the entry to drop.
func TestTheDuplicateLoadLintNamesTheRemoteEntryOfTheSameName(t *testing.T) {
	tree := "~/" + treeInto
	for _, remote := range []any{
		"git:github.com/nicobailon/pi-subagents",
		"git:github.com/nicobailon/pi-subagents@v1.2.0",
		"https://github.com/nicobailon/pi-subagents.git",
		"npm:pi-subagents",
		"npm:pi-subagents@0.9.1",
		"npm:@nicobailon/pi-subagents@0.9.1",
		map[string]any{"source": "git:github.com/nicobailon/pi-subagents", "extensions": []string{"x"}},
	} {
		matt := agentPack(t, "matt", patchedTreeContribution(), piPackagesListOf(remote, tree))
		got := LintDuplicateLoads(matt)
		src := remote
		if m, ok := remote.(map[string]any); ok {
			src = m["source"]
		}
		if len(got) != 1 || !strings.Contains(got[0], `"`+src.(string)+`"`) || !strings.Contains(got[0], "twice") ||
			!strings.Contains(got[0], "extension matt/pi-subagents") {
			t.Errorf("%v beside %s: lint = %q, want one warning naming it", remote, tree, got)
		}
	}
}

// THE SAME LIST is the same surface and path in any of the pack's list bodies whose notches meet:
// a config-list reaches every notch, so it meets either posture's list, and the two postures never
// meet each other.
func TestTheDuplicateLoadLintReadsEveryBodyOfTheSameList(t *testing.T) {
	tree := "~/" + treeInto
	remote := "git:github.com/nicobailon/pi-subagents"
	cases := []struct {
		name string
		with []packdecl.Contribution
		want int
	}{
		{"two config-lists", []packdecl.Contribution{piPackagesList(tree), piPackagesList(remote)}, 1},
		{"a config-list and the autonomous list", []packdecl.Contribution{piPackagesList(tree), postureList(false, remote)}, 1},
		{"the guarded list and a config-list", []packdecl.Contribution{postureList(true, tree), piPackagesList(remote)}, 1},
		{"one posture's list", []packdecl.Contribution{postureList(true, tree, remote)}, 1},
		{"the tree in two bodies, said once", []packdecl.Contribution{piPackagesList(tree), piPackagesList(tree, remote)}, 1},
		{"another path", []packdecl.Contribution{piPackagesList(tree), {Kind: packdecl.KindConfigList,
			Surface: "pi/settings", Path: "/other", Add: json.RawMessage(`["` + remote + `"]`)}}, 0},
		{"no tree entry", []packdecl.Contribution{piPackagesList(remote)}, 0},
	}
	for _, c := range cases {
		matt := agentPack(t, "matt", append([]packdecl.Contribution{patchedTreeContribution()}, c.with...)...)
		if got := LintDuplicateLoads(matt); len(got) != c.want {
			t.Errorf("%s: lint = %q, want %d warning(s)", c.name, got, c.want)
		}
	}
	// The two postures never share a notch, so neither loads the tree twice.
	add, _ := json.Marshal([]string{tree})
	other, _ := json.Marshal([]string{remote})
	both := packdecl.Contribution{Kind: packdecl.KindAutonomy,
		Autonomous: &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: add}}},
		Guarded:    &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: other}}}}
	if got := LintDuplicateLoads(agentPack(t, "matt", patchedTreeContribution(), both)); len(got) != 0 {
		t.Errorf("the autonomous list's tree and the guarded list's git: entry meet at no notch: lint = %q", got)
	}
}

// NOT A DUPLICATE: a remote entry of another final name, and a local path, which the agent loads
// from where it points.
func TestTheDuplicateLoadLintPassesOtherPackages(t *testing.T) {
	tree := "~/" + treeInto
	for _, other := range []string{
		"git:github.com/nicobailon/pi-subagents-extra",
		"npm:pi-subagents-lite",
		"npm:@pi-subagents/core",
		"../pi-subagents",
		"~/code/pi-subagents",
	} {
		if got := LintDuplicateLoads(agentPack(t, "matt", patchedTreeContribution(), piPackagesList(other, tree))); len(got) != 0 {
			t.Errorf("%s beside %s: lint = %q, want none", other, tree, got)
		}
	}
}

// THE UPSTREAM'S NAME counts as the extension's own: a tree landing under another name is still
// loaded twice by a remote entry of its upstream.
func TestTheDuplicateLoadLintKnowsTheUpstreamsName(t *testing.T) {
	c := patchedTreeContribution()
	c.Into = ".pi/agent/yolo-patched/subagents"
	matt := agentPack(t, "matt", c, piPackagesList("npm:pi-subagents", "~/"+c.Into))
	if got := LintDuplicateLoads(matt); len(got) != 1 || !strings.Contains(got[0], `"npm:pi-subagents"`) {
		t.Errorf("lint = %q, want the upstream's npm: entry named", got)
	}
	// A subdirectory source is named by its subdirectory.
	c.Source = "git+https://github.com/x/mono//packages/pi-footer?ref=main"
	matt = agentPack(t, "matt", c, piPackagesList("git:github.com/x/pi-footer", "~/"+c.Into))
	if got := LintDuplicateLoads(matt); len(got) != 1 {
		t.Errorf("a subdirectory source: lint = %q, want the entry of its subdirectory's name named", got)
	}
}
