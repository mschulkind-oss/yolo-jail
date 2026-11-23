package config

import (
	"strings"
	"testing"
)

// Design 3.2: a narrower on_request under a broad always governs. Until the scoped engine is wired
// it lowers the whole source, in the launch snapshot and in the live ceiling alike.
func TestScopedOnRequestLowersAnAlwaysCallBrokerCell(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n[scopes.reports]\nsource=\"aws\"\nresource=\"s3://b/reports/\"\n[scopes.reports.allow]\non_request=[\"all\",\"working_set\"]\n"
	project := "[call_roots]\naws=[\"s3://b\"]\n"
	policy, current := awsCellPolicy(t, user, project)
	if policy.Ceiling != "on_request" || policy.Decision != "ask" || current != "on_request" {
		t.Fatalf("scoped on_request under always ignored: policy=%+v current=%q", policy, current)
	}
	control, current := awsCellPolicy(t, "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n", project)
	if control.Ceiling != "always" || control.Decision != "run" || current != "always" {
		t.Fatalf("control must still run: %+v %q", control, current)
	}
}

// Design 3.2: a narrower ask of "ask" governs a broad true; whole-source until U3.
func TestScopedAskAskGovernsABroadTrue(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n"
	project := awsRoots + "[scopes.reports]\nsource=\"aws\"\nresource=\"s3://b/reports/\"\n[scopes.reports.requests.all]\nput_objects=\"ask\"\n[scopes.reports.requests.working_set]\nput_objects=\"ask\"\n"
	policy, _ := awsCellPolicy(t, user, project)
	if policy.Decision != "ask" || policy.Request != "ask" {
		t.Fatalf("scoped ask \"ask\" ignored under a broad true: %+v", policy)
	}
}

// Design 3.7: never names a read. The aws pack declares read cells, so `read.<cell>` is accepted.
func TestNeverNamingAReadIsSeenByTheLiveReadCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		allow, cell string
		want        bool
	}{
		"source read":         {"never=[\"read\"]", "get_cost_and_usage", true},
		"source read cell":    {"never=[\"read.get_cost_and_usage\"]", "get_cost_and_usage", true},
		"other read cell":     {"never=[\"read.get_cost_and_usage\"]", "get_objects", false},
		"cell-less read":      {"never=[\"read\"]", "", true},
		"no never":            {"on_request=[\"read\"]", "get_cost_and_usage", false},
		"scoped read":         {"scope-never=[\"read\"]", "get_cost_and_usage", true},
		"scoped read cell":    {"scope-never=[\"read.get_cost_and_usage\"]", "get_cost_and_usage", true},
		"scoped other cell":   {"scope-never=[\"read.get_cost_and_usage\"]", "get_objects", false},
		"scoped never a call": {"scope-never=[\"all\",\"working_set\"]", "get_objects", false},
	} {
		t.Run(name, func(t *testing.T) {
			user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n"
			if v, ok := strings.CutPrefix(tc.allow, "scope-"); ok {
				user += "[scopes.reports]\nsource=\"aws\"\nresource=\"s3://b/reports/\"\n[scopes.reports.allow]\n" + v + "\n"
			} else {
				user = strings.Replace(user, "always=[\"read\",\"all\"]\n", "always=[\"read\",\"all\"]\n"+tc.allow+"\n", 1)
			}
			input, _ := scopedPolicyFixture(t, user, "[call_roots]\naws=[\"s3://b\"]\n")
			if got := CallBrokerReadNeverRefuses(input.User, "aws", tc.cell); got != tc.want {
				t.Fatalf("CallBrokerReadNeverRefuses(%q) = %v, want %v", tc.cell, got, tc.want)
			}
		})
	}
}

// A scoped read that asks or refuses cannot be enforced yet, so the config is refused with its next step
// rather than accepted and ignored (happy-path principle).
func TestScopedReadAskIsRefusedUntilEnforced(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n"
	for name, line := range map[string]string{
		"ask":        "[scopes.team.requests]\nread=\"ask\"\n",
		"false":      "[scopes.team.requests]\nread=false\n",
		"cell ask":   "[scopes.team.requests.read]\nget_objects=\"ask\"\n",
		"true is ok": "",
	} {
		t.Run(name, func(t *testing.T) {
			project := awsRoots + "[scopes.team]\nsource=\"aws\"\nresource=\"s3://b/team/\"\n" + line
			input, _ := scopedPolicyFixture(t, user, project)
			plan, err := ResolveReadableAllowance(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range plan.Sources {
				if s.Source == "aws" && s.CallBroker != nil {
					_, err := EffectiveCallBrokerCells("aws", s.CallBroker, input.User, input.Workspace)
					if line == "" {
						if err != nil {
							t.Fatalf("control refused: %v", err)
						}
						return
					}
					if err == nil || !strings.Contains(err.Error(), "not enforced yet") || !strings.Contains(err.Error(), "yolo config unset") {
						t.Fatalf("scoped read line accepted or refusal names no next step: %v", err)
					}
					return
				}
			}
			t.Fatal("aws not selected")
		})
	}
}
