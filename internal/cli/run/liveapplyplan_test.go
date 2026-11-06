package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The per-key, per-backend outcome table of docs/design/live-config-apply-plan.md (Task A,
// "Outcome table"), row by row. A key not named in a row is restart-only, and on macos-user
// every key reaches the next session, since each invocation there is a fresh sandbox.
func TestTheKeyOutcomeTable(t *testing.T) {
	for _, c := range []struct {
		rt, key string
		want    applyOutcome
	}{
		{"podman", "env_sources", outcomeNextSession},
		{"podman", "providers", outcomeNextSession},
		{"podman", "profiles", outcomeNextSession},
		{"podman", "profile", outcomeNextSession},
		{"podman", "model_facts", outcomeNextSession},
		{"podman", "adapters", outcomeNextSession},
		{"container", "providers", outcomeNextSession},
		{"podman", "agents_md_extra", outcomeNextSession},
		{"podman", "briefing_provenance", outcomeNextSession},
		{"container", "agents_md_extra", outcomeNextRestart},
		{"container", "briefing_provenance", outcomeNextRestart},
		{"podman", "network", outcomeNextRestart},
		{"podman", "packages", outcomeNextRestart},
		{"podman", "packs", outcomeNextRestart},
		{"container", "resources", outcomeNextRestart},
		{"macos-user", "network", outcomeNextSession},
		{"macos-user", "packages", outcomeNextSession},
		{"macos-user", "agents_md_extra", outcomeNextSession},
	} {
		if got, _ := keyOutcome(c.rt, c.key, false); got != c.want {
			t.Errorf("keyOutcome(%s, %s) = %v, want %v", c.rt, c.key, got, c.want)
		}
	}
	if _, why := keyOutcome("container", "agents_md_extra", false); why == "" {
		t.Error("Apple Container's restart-only briefing keys must say why")
	}
}

// mounts: applied now where the jail takes live mounts, at next restart with the reason it
// does not, at next restart on Apple Container whatever the tag says (the backend check comes
// before it), and next session on macos-user.
func TestTheMountsOutcomeRow(t *testing.T) {
	if got, _ := mountsOutcome("podman", true, ""); got != outcomeAppliedNow {
		t.Errorf("a podman jail that takes live mounts: %v, want applied now", got)
	}
	got, why := mountsOutcome("podman", false, "this jail started before live mounts existed")
	if got != outcomeNextRestart || why != "this jail started before live mounts existed" {
		t.Errorf("a podman jail that cannot: %v %q, want at next restart with the seam's reason", got, why)
	}
	got, why = mountsOutcome("container", true, "")
	if got != outcomeNextRestart || !strings.Contains(why, "Apple Container") {
		t.Errorf("Apple Container: %v %q, want at next restart naming Apple Container", got, why)
	}
	if got, _ := mountsOutcome("macos-user", false, "x"); got != outcomeNextSession {
		t.Errorf("macos-user: %v, want next session", got)
	}
}

// brokered and scope: applied now only when EVERY live broker file of the jail takes live
// scope; at next restart otherwise; Apple Container starts no broker; macos-user's next
// session runs its own gate and broker.
func TestTheScopeOutcomeRow(t *testing.T) {
	live := liveScopeFile{Path: "/a", LiveScope: true}
	old := liveScopeFile{Path: "/b"}
	if got, _ := scopeChangeOutcome("podman", []liveScopeFile{live}); got != outcomeAppliedNow {
		t.Errorf("one live-scope broker: %v, want applied now", got)
	}
	if got, why := scopeChangeOutcome("podman", []liveScopeFile{live, old}); got != outcomeNextRestart || why == "" {
		t.Errorf("one broker without live scope: %v %q, want at next restart with a reason", got, why)
	}
	if got, why := scopeChangeOutcome("podman", nil); got != outcomeNextRestart || why == "" {
		t.Errorf("no running broker: %v %q, want at next restart with a reason", got, why)
	}
	if got, why := scopeChangeOutcome("container", []liveScopeFile{live}); got != outcomeNextRestart ||
		why != "no broker runs in this jail" {
		t.Errorf("Apple Container: %v %q", got, why)
	}
	if got, _ := scopeChangeOutcome("macos-user", []liveScopeFile{live}); got != outcomeAppliedNow {
		t.Errorf("macos-user live sessions: %v, want applied now", got)
	}
	if got, _ := scopeChangeOutcome("macos-user", nil); got != outcomeNextSession {
		t.Errorf("macos-user with no live session: %v, want next session", got)
	}
}

func mapOf(t *testing.T, js string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.OrderedMap)
}

// The plan's key half: one change per top-level workspace key that differs between the
// effective-config record and the config on disk, labeled down to the changed sub-keys, with
// the table's outcome.
func TestThePlanListsEachChangedKeyWithItsOutcome(t *testing.T) {
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true,
		boot:    mapOf(t, `{"network":{"ports":["1:1"],"mode":"bridge"},"profile":{"claude":"a"},"packages":["jq"]}`),
		current: mapOf(t, `{"network":{"ports":["2:2"],"mode":"bridge"},"profile":{"claude":"b"},"packages":["jq"]}`),
	})
	lines := renderApplyChanges(plan)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"network.ports: at next restart (yolo stop, then yolo)",
		"profile.claude: from your next yolo session",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the plan has no line %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "packages") {
		t.Errorf("an unchanged key is in the plan:\n%s", joined)
	}
}

// With no jail running every change waits for the next launch, which takes all of them.
func TestThePlanWithNoJailRunningIsTheNextLaunch(t *testing.T) {
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: false, bootOK: true,
		boot: mapOf(t, `{}`), current: mapOf(t, `{"profile":{"claude":"b"},"packages":["jq"]}`)})
	if len(plan) != 2 {
		t.Fatalf("want two changes, got %d", len(plan))
	}
	for _, c := range plan {
		if c.outcome != outcomeNextLaunch {
			t.Errorf("%s: %v, want the next launch", c.label, c.outcome)
		}
	}
}

// The mount half: with live mounts and a readable record, one change per element added or
// removed, applied now; a read-write add carries the launch's disclosure; a removal says an
// open file stays readable. Without live mounts, the key alone, at next restart, with why.
func TestThePlanSplitsMountsIntoElements(t *testing.T) {
	boot, cur := mapOf(t, `{"mounts":["/old"]}`), mapOf(t, `{"mounts":["/new"]}`)
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true, boot: boot, current: cur,
		mountsLive: true, mountRecordOK: true,
		mountAdds:    []configCtxMount{{source: "/new", dest: "/ctx/new"}, {source: "/rw", dest: "/ctx/rw", rw: true}},
		mountRemoves: []liveCtxMount{{Source: "/old", Dest: "/ctx/old"}},
	})
	joined := strings.Join(renderApplyChanges(plan), "\n")
	for _, want := range []string{
		"+ /ctx/new (read-only): applied now",
		"+ /ctx/rw (read-write): applied now",
		"- /ctx/old (read-only): applied now",
		"stays readable until it is closed",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the plan has no %q:\n%s", want, joined)
		}
	}
	for _, c := range plan {
		if c.key != "mounts" {
			t.Errorf("%s folds key %q, want mounts", c.label, c.key)
		}
	}

	plan = buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true, boot: boot, current: cur,
		mountsReason: "this jail started before live mounts existed"})
	joined = strings.Join(renderApplyChanges(plan), "\n")
	if !strings.Contains(joined, "mounts: at next restart (yolo stop, then yolo) — this jail started before live mounts existed") {
		t.Errorf("an old jail's mount change must wait for restart, saying why:\n%s", joined)
	}
}

func TestChangedBootScopeWithoutVerifiedLiveFilesWaitsForRestart(t *testing.T) {
	boot := mapOf(t, `{"brokered":{"github":{"repos":["o/old"]}}}`)
	current := mapOf(t, `{"brokered":{"github":{"repos":["o/new"]}}}`)
	for _, tc := range []struct {
		name string
		err  string
	}{
		{"absent scope file", ""},
		{"discovery error", "source lock wait timed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true,
				boot: boot, current: current, scopes: []scopeDelta{{label: "github", source: "github",
					desired: []string{"o/new"}, discoveryErr: tc.err}}})
			if len(plan) != 1 || plan[0].outcome != outcomeNextRestart {
				t.Fatalf("plan = %+v, want one restart-required brokered change", plan)
			}
			if tc.err != "" && !strings.Contains(plan[0].reason, tc.err) {
				t.Errorf("reason %q does not retain discovery error %q", plan[0].reason, tc.err)
			}
			if strings.Contains(plan[0].reason, "already matches") {
				t.Errorf("configured source was treated as proof of the live scope: %+v", plan[0])
			}
		})
	}
}

// The scope half: one change per brokered loophole whose live broker files hold a different
// scope than the one approved, naming each repository added and removed.
func TestThePlanNamesTheScopeChange(t *testing.T) {
	f := liveScopeFile{Path: "/s", LiveScope: true, File: brokerscope.File{Repos: []string{"o/r", "o/gone"}}}
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true,
		boot: mapOf(t, `{}`), current: mapOf(t, `{}`),
		scopes: []scopeDelta{{label: "gb", source: "gbsrc", desired: []string{"o/r", "o/lib"},
			files: []liveScopeFile{f}}},
	})
	if len(plan) != 1 {
		t.Fatalf("want one scope change, got %d: %v", len(plan), renderApplyChanges(plan))
	}
	line := renderApplyChanges(plan)[0]
	for _, want := range []string{"gb repository scope", "+o/lib", "-o/gone", "applied now"} {
		if !strings.Contains(line, want) {
			t.Errorf("the scope line has no %q: %s", want, line)
		}
	}
}
