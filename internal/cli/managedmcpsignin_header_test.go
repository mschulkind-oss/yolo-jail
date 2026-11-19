package cli

import (
	"context"
	"testing"
)

func TestMCPAuthHeaderGrammarRefusesBeforeIssuance(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	previous := mcpAuthHostHeader
	t.Cleanup(func() { mcpAuthHostHeader = previous })
	calls := 0
	mcpAuthHostHeader = func(context.Context, string, func(string)) (string, error) { calls++; return "must-not-issue", nil }
	for _, args := range [][]string{{"header", "slack", "--format"}, {"header", "slack", "--format", "xml"}, {"header", "slack", "--format", "json", "extra"}, {"header", "slack", "--format", "json", "--format", "bare"}, {"header", "slack", "--unknown"}, {"header"}} {
		rc, out, _ := captureStd(t, func() int { return Main(append([]string{"yolo", "mcp-auth"}, args...)) })
		if rc != 2 || out != "" {
			t.Fatalf("malformed grammar admitted: %v", args)
		}
	}
	if calls != 0 {
		t.Fatal("grammar refusal issued a header")
	}
}

func TestMCPAuthJSONHeaderThroughMain(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	previous := mcpAuthHostHeader
	t.Cleanup(func() { mcpAuthHostHeader = previous })
	mcpAuthHostHeader = func(context.Context, string, func(string)) (string, error) { return "fixture-access", nil }
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "header", "slack", "--format", "json"}) })
	if rc != 0 || out != "{\"Authorization\":\"Bearer fixture-access\"}\n" || errs != "" {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}
