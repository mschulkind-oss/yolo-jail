package sigv4

import "testing"

func TestBedrockRuntimeRegionMatchesOnlyTheExactHost(t *testing.T) {
	for host, want := range map[string]string{
		"bedrock-runtime.us-east-1.amazonaws.com":      "us-east-1",
		"bedrock-runtime.eu-central-1.amazonaws.com":   "eu-central-1",
		"bedrock-runtime.us-gov-west-1.amazonaws.com":  "us-gov-west-1",
		"bedrock-runtime.ap-southeast-2.amazonaws.com": "ap-southeast-2",
	} {
		if got, ok := BedrockRuntimeRegion(host); !ok || got != want {
			t.Errorf("BedrockRuntimeRegion(%q) = %q, %v; want %q, true", host, got, ok, want)
		}
	}
	for _, host := range []string{
		"bedrock-runtime.us-east-1.amazonaws.com.evil.example", // suffix lookalike
		"evil-bedrock-runtime.us-east-1.amazonaws.com",         // prefix lookalike
		"bedrock-runtime.us-east-1.amazonaws.com:443",          // host:port, not a hostname
		"BEDROCK-RUNTIME.US-EAST-1.AMAZONAWS.COM",              // upper case
		"bedrock-runtime.amazonaws.com",                        // no region label
		"bedrock-runtime.us-east-1.x.amazonaws.com",            // extra label
		"bedrock-runtime-fips.us-east-1.amazonaws.com",         // FIPS: the marker re-key's job (OQ-WG1 b)
		"bedrock.us-east-1.amazonaws.com",                      // the control plane, not runtime
		"192.168.64.3", "[::1]", "::1", "",
		"api.anthropic.com", "openrouter.ai",
	} {
		if got, ok := BedrockRuntimeRegion(host); ok {
			t.Errorf("BedrockRuntimeRegion(%q) matched (region %q); it must never sign for that host", host, got)
		}
	}
}

func TestAgentCoreGatewayRegion(t *testing.T) {
	if got, ok := AgentCoreGatewayRegion("gw-abc123.gateway.bedrock-agentcore.us-west-2.amazonaws.com"); !ok || got != "us-west-2" {
		t.Errorf("gateway host did not match: %q %v", got, ok)
	}
	for _, host := range []string{
		"gateway.bedrock-agentcore.us-west-2.amazonaws.com",
		"gw.gateway.bedrock-agentcore.us-west-2.amazonaws.com.evil.example",
		"gw.gateway.bedrock-agentcore.us-west-2.amazonaws.com:443",
		"bedrock-runtime.us-west-2.amazonaws.com",
	} {
		if _, ok := AgentCoreGatewayRegion(host); ok {
			t.Errorf("AgentCoreGatewayRegion(%q) matched", host)
		}
	}
}
