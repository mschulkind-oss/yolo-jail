package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func TestMCPAuthDiagnosisWordingThroughMain(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	oldStatus, oldHeader := mcpAuthHostStatus, mcpAuthHostHeader
	t.Cleanup(func() { mcpAuthHostStatus, mcpAuthHostHeader = oldStatus, oldHeader })
	mcpAuthHostHeader = func(context.Context, string, func(string)) (string, error) {
		t.Fatal("status issued a header")
		return "", nil
	}
	point := &mcpsignin.Point{NativeName: "plugin:placed-original:fixture-key"}
	for _, input := range []mcpsignin.DiagnosisInput{
		{Once: true, Record: "live"}, {}, {Once: true},
		{Once: true, Point: point, Capturable: true},
		{Once: true, Source: "unsafe"}, {Once: true, Source: "entry awaiting adoption"},
		{Once: true, Outcome: "refused-client-mismatch"}, {Once: true, Record: "dead"},
		{Once: true, Record: "expired"}, {Once: true, Record: "live", Delivery: "delivered plain"},
		{Once: true, BrokerUnreachable: true}, {Once: true, Point: point, Mode: "credential view"},
		{Once: true, Outcome: "rotated-not-saved"}, {Once: true, Busy: true},
	} {
		input.Server, input.URL, input.Agent = "configured-alias", "https://mcp.fixture.invalid/mcp", "fixture-agent"
		diagnosis := mcpsignin.Diagnose(input)
		t.Run(diagnosis.Verdict, func(t *testing.T) {
			mcpAuthHostStatus = func(string) (map[string]any, error) {
				return map[string]any{"schema": "yolo.mcp-auth.status/1", "agents": []any{}, "once": []any{map[string]any{"name": input.Server, "diagnosis": diagnosis.Verdict, "next_step": diagnosis.NextStep}}}, nil
			}
			rc, out, err := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "status"}) })
			if rc != 0 || err != "" || !strings.Contains(out, "configured-alias: "+diagnosis.Verdict) || !strings.Contains(out, diagnosis.NextStep) {
				t.Fatalf("readable verdict became health gate: %d %s %s", rc, out, err)
			}
			if strings.Contains(out, "mcp-auth login") || strings.Contains(out, "authenticate configured-alias") {
				t.Fatal("recovery invented ceremony/native name")
			}
		})
	}
}

func TestMCPAuthStatusJailNeverFallsBackToHost(t *testing.T) {
	t.Setenv("YOLO_VERSION", "fixture")
	t.Setenv(mcpAuthEndpointEnv, "")
	previous := mcpAuthHostStatus
	t.Cleanup(func() { mcpAuthHostStatus = previous })
	mcpAuthHostStatus = func(string) (map[string]any, error) { t.Fatal("jail status read host state"); return nil, nil }
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "status", "--json"}) })
	if rc != 1 || out != "" || !strings.Contains(errs, "yolo mcp-auth status on the host") {
		t.Fatalf("missing endpoint did not refuse honestly: %d %s %s", rc, out, errs)
	}
}
