package wirebridged

// agentkey_test.go pins the bridge's half of the credential gate
// (docs/design/provider-credential-scope.md, OQ-CN6's constraint): the key of a provider
// the bridge serves no longer sits in the shared yolo-user-env.sh — the gate scopes it to
// the agent that selected the provider, in that agent's own env file — so the bridge must
// read THAT file, for the agent the route is served for, or a correctly configured bridged
// launch idles for want of a key it was handed.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// writeAgentKey writes one export into agent's env file under home, where the launch's
// directory bind puts it.
func writeAgentKey(t *testing.T, home, agent, line string) {
	t.Helper()
	p := entrypoint.AgentEnvFile(home, agent)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The route names the agent it serves, and resolveKey reads that agent's file first — and
// only that agent's: another agent's copy of a same-named variable is not this route's.
func TestTheBridgeReadsTheServedAgentsOwnKey(t *testing.T) {
	route, idle := resolveRoute(routeEnv(bridgedProviders,
		`{"cerebras-fast":{"provider":"cerebras"}}`,
		`{"claude":"cerebras-fast"}`))
	if idle != "" {
		t.Fatalf("a bridged route must serve, got idle: %s", idle)
	}
	if route.Agent != "claude" {
		t.Fatalf("route.Agent = %q, want the agent whose profile selected the provider", route.Agent)
	}

	home := t.TempDir()
	t.Setenv("CEREBRAS_API_KEY", "")
	writeAgentKey(t, home, "claude", "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'from-claude'}")
	writeAgentKey(t, home, "pi", "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'from-pi'}")
	got, source := resolveKey(route.KeyEnvName, home, route.Agent)
	if got != "from-claude" || source != entrypoint.AgentEnvFile(home, "claude") {
		t.Errorf("resolveKey = %q from %q, want claude's own file's value", got, source)
	}

	// Through the handler the daemon serves with: the key only claude's file holds is found.
	e := entrypoint.NewEnv(map[string]string{"HOME": home})
	if _, _, reason := adapterHandler(route, e); reason != "" {
		t.Errorf("the bridge refused a route whose key is in the served agent's file: %s", reason)
	}
	if _, _, reason := adapterHandler(route, entrypoint.NewEnv(map[string]string{"HOME": t.TempDir()})); reason == "" {
		t.Error("control: with no key anywhere the handler must refuse to serve unauthenticated")
	}
}

// The via route is served per agent too (rt.Agent), and reads that agent's file.
func TestAViaRouteReadsItsAgentsOwnKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZAI_API_KEY", "")
	writeAgentKey(t, home, "pi", "export ZAI_API_KEY=${ZAI_API_KEY:-'from-pi'}")
	rt := viaRoute{Agent: "pi", ProviderName: "zai", KeyEnvName: "ZAI_API_KEY"}
	up := newViaUpstream("https://api.z.ai/api/paas/v4")
	if h, line := viaUpstreamHandler(rt, up, home); isIdleVia(h) {
		t.Errorf("the via route for pi idled although pi's own file holds its key: %s", line)
	}
	if h, _ := viaUpstreamHandler(viaRoute{Agent: "codex", ProviderName: "zai", KeyEnvName: "ZAI_API_KEY"},
		up, home); !isIdleVia(h) {
		t.Error("a route served for codex must not borrow pi's key")
	}
}

func isIdleVia(h any) bool {
	_, idle := h.(idleViaHandler)
	return idle
}

// A BEDROCK ROUTE SIGNS WITH THE SERVED AGENT'S OWN AWS CREDENTIALS. The gate puts the AWS
// pair (bedrock claims it) only in the file of the agent that selected bedrock, so both SigV4
// lookups — the adapter route's and a via route's — must read that agent's file, or a
// bridged Bedrock launch idles for want of a pair it was handed. The control puts the pair in
// ANOTHER agent's file only, which the route must not borrow.
func TestABedrockRouteSignsWithTheServedAgentsOwnPair(t *testing.T) {
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_BEARER_TOKEN_BEDROCK"} {
		t.Setenv(k, "")
	}
	const upstream = "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"
	pair := "export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIA-claude'}\n" +
		"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'secret-claude'}"
	adapter := route{Agent: "claude", ProviderName: "bedrock", UpstreamBaseURL: upstream, SignRegion: "us-east-1"}
	via := viaRoute{Agent: "claude", ProviderName: "bedrock"}
	up := newViaUpstream(upstream)
	if up.SignRegion == "" {
		t.Fatalf("fixture: %s must read as a Bedrock host", upstream)
	}

	for _, tc := range []struct {
		name, holder string
		serves       bool
	}{{"the pair in the served agent's file", "claude", true},
		{"the pair only in another agent's file", "pi", false}} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeAgentKey(t, home, tc.holder, pair)
			_, _, reason := adapterHandler(adapter, entrypoint.NewEnv(map[string]string{"HOME": home}))
			if (reason == "") != tc.serves {
				t.Errorf("adapter route: serves = %v, want %v (reason %q)", reason == "", tc.serves, reason)
			}
			h, line := viaUpstreamHandler(via, up, home)
			if isIdleVia(h) == tc.serves {
				t.Errorf("via route: serves = %v, want %v (%s)", !isIdleVia(h), tc.serves, line)
			}
		})
	}
}
