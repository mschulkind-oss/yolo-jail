package mcpsignin

import (
	"strings"
	"testing"
)

func TestDiagnosisClosedVerdictsAndNativeRecovery(t *testing.T) {
	point := &Point{NativeName: "plugin:delivered-fixture:entry"}
	for _, tc := range []struct {
		verdict string
		input   DiagnosisInput
	}{
		{"ready", DiagnosisInput{Once: true, Record: "live"}},
		{"not one-sign-in here", DiagnosisInput{}},
		{"no sign-in point", DiagnosisInput{Once: true}},
		{"sign-in needed", DiagnosisInput{Once: true, Point: point, Capturable: true}},
		{"source unreadable", DiagnosisInput{Once: true, Source: "malformed"}},
		{"awaiting adoption", DiagnosisInput{Once: true, Source: "entry awaiting adoption"}},
		{"adoption refused", DiagnosisInput{Once: true, Outcome: "refused-client-mismatch"}},
		{"record dead", DiagnosisInput{Once: true, Record: "dead"}},
		{"record expired", DiagnosisInput{Once: true, Record: "expired"}},
		{"delivered plain", DiagnosisInput{Once: true, Record: "live", Delivery: "delivered plain", DeliveryReason: "route unsupported"}},
		{"broker unreachable", DiagnosisInput{Once: true, BrokerUnreachable: true}},
		{"not capturable here", DiagnosisInput{Once: true, Point: point, Mode: "credential view"}},
		{"sign-in lost", DiagnosisInput{Once: true, Outcome: "rotated-not-saved"}},
		{"busy", DiagnosisInput{Once: true, Busy: true}},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			tc.input.Server = "configured-alias"
			tc.input.URL = "https://mcp.fixture.invalid/mcp"
			tc.input.Agent = "fixture-agent"
			result := Diagnose(tc.input)
			if result.Verdict != tc.verdict || strings.Contains(result.NextStep, "mcp-auth login") {
				t.Fatalf("diagnosis=%+v", result)
			}
			if result.Verdict != "ready" && result.NextStep == "" {
				t.Fatal("refusal has no next step")
			}
			for _, callback := range []string{"owner", "held by other", "port busy", "IPv4 only"} {
				tc.input.Callback = callback
				if Diagnose(tc.input).Verdict != tc.verdict {
					t.Fatal("callback changed verdict")
				}
			}
		})
	}
	result := Diagnose(DiagnosisInput{Server: "configured-alias", Once: true, Point: point, Agent: "fixture-agent", Capturable: true})
	if !strings.Contains(result.NextStep, "authenticate plugin:delivered-fixture:entry") || strings.Contains(result.NextStep, "authenticate configured-alias") || !strings.Contains(result.NextStep, "paste the full callback URL") {
		t.Fatal("recovery invented a native name or omitted paste")
	}
	result = Diagnose(DiagnosisInput{Server: "configured-alias", Once: true, Record: "dead", HeaderSource: true})
	if !strings.Contains(result.NextStep, "restart the agent") {
		t.Fatal("dead header-only source cannot restore plain entry")
	}
}
