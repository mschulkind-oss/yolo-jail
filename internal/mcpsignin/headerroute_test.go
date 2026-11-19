package mcpsignin

import "testing"

func TestHeaderRouteUsesFrozenSourceFactsAndCapabilities(t *testing.T) {
	facts := &Launch{Version: LaunchVersion, Decisions: []Decision{{Server: "configured-alias", Agent: "fixture-agent", Capturable: true, Point: &Point{NativeName: "resolved-native"}}}}
	for _, tc := range []struct {
		name, form, agent, server string
		source, reachable, want   bool
		reason                    string
	}{
		{"capturable source", "json", "fixture-agent", "configured-alias", true, true, true, ""},
		{"nonsource missing frozen eligibility", "bare", "nonsource-agent", "other-server", false, true, false, "not this launch's user-level once server"},
		{"eligible nonsource independent of point", "bare", "fixture-agent", "configured-alias", false, true, true, ""},
		{"unsupported", "", "fixture-agent", "configured-alias", true, true, false, "header route unsupported"},
		{"unreachable", "json", "fixture-agent", "configured-alias", true, false, false, "broker unreachable"},
		{"wrong agent", "json", "other-agent", "configured-alias", true, true, false, "native plain bootstrap"},
		{"wrong server", "json", "fixture-agent", "other-server", true, true, false, "native plain bootstrap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approved, reason := HeaderRoute(facts, tc.server, tc.agent, tc.form, tc.source, tc.reachable)
			if approved != tc.want || reason != tc.reason {
				t.Fatalf("route=%v reason=%q", approved, reason)
			}
		})
	}
	for _, mutation := range []func(*Launch){
		func(l *Launch) { l.Version++ },
		func(l *Launch) { l.Decisions[0].Capturable = false },
		func(l *Launch) { l.Decisions[0].Point = nil },
		func(l *Launch) { l.Decisions[0].Point.NativeName = "" },
	} {
		copy := &Launch{Version: facts.Version, Decisions: []Decision{{Server: "configured-alias", Agent: "fixture-agent", Capturable: true, Point: &Point{NativeName: "resolved-native"}}}}
		mutation(copy)
		if approved, _ := HeaderRoute(copy, "configured-alias", "fixture-agent", "json", true, true); approved {
			t.Fatal("unusable source facts approved header route")
		}
	}
	facts.Decisions[0].Capturable = false
	facts.Decisions[0].Point = nil
	facts.Decisions[0].UsableRecord = true
	if approved, _ := HeaderRoute(facts, "configured-alias", "fixture-agent", "json", true, true); !approved {
		t.Fatal("usable canonical record required a native bundle")
	}
}
