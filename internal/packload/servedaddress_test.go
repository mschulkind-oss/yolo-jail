package packload

// servedaddress_test.go pins the composers' half of SERVED ADDRESSES (served.go;
// docs/plans/notch-convergence.md NC-D41): a served set that moved a declared address carries the
// move into the provider table, the via base and every pack env pointer naming {listen}, and one
// that moved nothing composes every declared address as before.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// movedBridge is a container launch serving the wire bridge, with its three declared addresses
// moved the way a shared-namespace launch moves them.
func movedBridge() ServedDaemons {
	return ServedInJail([]string{"wire-bridge"}).WithRebind(map[string]string{
		"127.0.0.1:8214": "127.0.0.1:48214",
		"127.0.0.1:8215": "127.0.0.1:48215",
		"127.0.0.1:8216": "127.0.0.1:48216",
	})
}

func anthropicBase(t *testing.T, table *jsonx.OrderedMap, provider string) string {
	t.Helper()
	entry, _ := table.Get(provider)
	m, ok := entry.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("no %s entry in the composed table", provider)
	}
	eps, _ := m.Get("endpoints")
	em, _ := eps.(*jsonx.OrderedMap)
	if em == nil {
		return ""
	}
	a, _ := em.Get("anthropic")
	am, _ := a.(*jsonx.OrderedMap)
	if am == nil {
		return ""
	}
	v, _ := am.Get("base_url")
	s, _ := v.(string)
	return s
}

func TestTheProviderTableComposesAServiceAdaptationAtItsServedAddress(t *testing.T) {
	packs := embeddedNamed(t, "claude", "cerebras", "wire-bridge")
	moved, _, err := ComposeProvidersAt(nil, packs, nil, movedBridge())
	if err != nil {
		t.Fatal(err)
	}
	if got := anthropicBase(t, moved, "cerebras"); got != "http://127.0.0.1:48214" {
		t.Errorf("cerebras's bridged anthropic endpoint = %q, want the served 127.0.0.1:48214", got)
	}
	declared, _, err := ComposeProvidersAt(nil, packs, nil, ServedInJail([]string{"wire-bridge"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := anthropicBase(t, declared, "cerebras"); got != "http://127.0.0.1:8214" {
		t.Errorf("with nothing moved the endpoint = %q, want the declared 127.0.0.1:8214", got)
	}
	// The user's own address for the conversion is not a declared address, so it never moves.
	override, _, err := ComposeProvidersAt(nil, packs,
		map[string]string{"openai->anthropic": "http://127.0.0.1:9214"}, movedBridge())
	if err != nil {
		t.Fatal(err)
	}
	if got := anthropicBase(t, override, "cerebras"); got != "http://127.0.0.1:9214" {
		t.Errorf("the user's override became %q; it must stay where the user put it", got)
	}
}

func TestAViaAnswersAtItsServedAddress(t *testing.T) {
	packs := embeddedNamed(t, "wire-bridge")
	in := map[string]ResolvedProfile{"pz": {Provider: "zai", Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216"}}
	out, cleared := ViaServedAt(in, packs, movedBridge())
	if got := out["pz"].ViaBase; got != "http://127.0.0.1:48216" || len(cleared) != 0 {
		t.Errorf("the served via base = %q (cleared %v), want http://127.0.0.1:48216", got, cleared)
	}
	if got := ViaURLFor(out["pz"], "pi"); !strings.HasPrefix(got, "http://127.0.0.1:48216/") {
		t.Errorf("pi's via URL = %q, want it under the served address", got)
	}
}

// A CARRIED AGENT ANSWERS AT THE BRIDGE'S SERVED ADDRESS TOO (carrier.go, wire-bridge-gateway.md
// WG-I44): on a shared network namespace the launcher moves the bridge's ports, and oh-omp, which
// the bridge carries on `-p bedrock`, must be pointed where the via listener binds, as a via
// profile's agent is, and copilot at the moved adapter address its provider entry composes.
func TestACarriedAgentAnswersAtTheBridgesServedAddress(t *testing.T) {
	packs := embeddedNamed(t, "claude", "copilot", "omp", "bedrock", "aws-auth", "wire-bridge")
	providers, _, err := ComposeProvidersAt(nil, packs, nil, movedBridge())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := ViaServedAt(resolved, packs, movedBridge())
	if got := ViaURLFor(out["bedrock"], "oh-omp"); got != "http://127.0.0.1:48216/agent/oh-omp" {
		t.Errorf("oh-omp carried on -p bedrock: via URL %q, want it under the served address", got)
	}
	if got := anthropicBase(t, providers, "bedrock"); got != "http://127.0.0.1:48214" {
		t.Errorf("bedrock's adapter address for copilot = %q, want the served 127.0.0.1:48214", got)
	}
}

// A POINTER NAMING {listen} IS COMPOSED FROM ITS DAEMON'S SERVED ADDRESS, and withheld, named,
// where that daemon declares none.
func TestAListenPointerComposesItsDaemonsServedAddress(t *testing.T) {
	// claude's pack ships the `bedrock` provider whose platform the pointer's gate keys on.
	packs := embeddedNamed(t, "codex", "openai-auth", "aws-auth", "bedrock", "claude")
	profiles := map[string]string{"codex": "bedrock"}
	providers, resolved, _ := launchSelection(t, packs, nil, nil, profiles)
	scope := func(served ServedDaemons) *CredentialScope {
		t.Helper()
		s, err := ScopeCredentials(ScopeInput{Packs: packs, Profiles: profiles, NoDerives: true, Served: &served,
			Providers: providers, Resolved: resolved})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	served := ServedInJail([]string{"openai-auth-broker", "aws-auth"}).WithListen(map[string]string{
		"openai-auth-broker": "127.0.0.1:41460", "aws-auth": "127.0.0.1:41461",
	})
	s := scope(served)
	if v, _ := s.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); v != "http://127.0.0.1:41460/oauth/token" {
		t.Errorf("codex's refresh pointer = %q, want the adapter's served address", v)
	}
	var uri string
	for _, e := range s.FoldFor("codex") {
		if e.Key == "AWS_CONTAINER_CREDENTIALS_FULL_URI" {
			uri = e.Value
		}
	}
	if uri != "http://127.0.0.1:41461/credentials" {
		t.Errorf("the bedrock pointer = %q, want the AWS adapter's served address", uri)
	}

	bare := scope(ServedInJail([]string{"openai-auth-broker", "aws-auth"}))
	if v, ok := bare.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); ok {
		t.Errorf("a pointer whose daemon has no address was delivered as %q", v)
	}
	lines := strings.Join(UnservedLines(bare, nil, nil), "\n")
	for _, want := range []string{"CODEX_REFRESH_TOKEN_URL_OVERRIDE", `"openai-auth-broker"`,
		// In the author's terms for both daemon kinds `served_by` can name: only a loophole
		// declares a listen address, so a pointer served by a pack service never composes.
		"a loophole declares one as jail_daemon.listen", "a pack service has none"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the withheld pointer is not named (%s):\n%s", want, lines)
		}
	}
}

func TestServedURLMovesOnlyADeclaredHostPort(t *testing.T) {
	s := movedBridge()
	for in, want := range map[string]string{
		"http://127.0.0.1:8214":        "http://127.0.0.1:48214",
		"http://127.0.0.1:8216/agent/": "http://127.0.0.1:48216/agent/",
		"http://127.0.0.1:9999":        "http://127.0.0.1:9999",
		"https://api.cerebras.ai/v1":   "https://api.cerebras.ai/v1",
		"":                             "",
	} {
		if got := s.ServedURL(in); got != want {
			t.Errorf("ServedURL(%q) = %q, want %q", in, got, want)
		}
	}
	if NothingServed().Listen("aws-auth") != "" {
		t.Error("a notch serving nothing gave a daemon a listen address")
	}
}
