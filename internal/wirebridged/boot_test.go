package wirebridged

// boot_test.go pins the selection-lazy boot read (wire-bridge.md §3.4): the
// composed table decides serve-vs-idle, and every idle reason is the healthy
// no-op the design licenses the coarse when_bins inclusion with. The tables are
// hand-built YOLO_* values fed through the same loaders the real boot reads
// (entrypoint.NewEnv → LoadProviders/LoadProfiles/LoadUseProfiles), so a change
// in the wire shape of either side fails here rather than in a jail.

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSignalReadyWritesTheServiceName(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()

	signalReadyOnFD(int(write.Fd()), ServiceName)
	got, err := bufio.NewReader(read).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got != "ready "+ServiceName+"\n" {
		t.Errorf("readiness = %q, want %q", got, "ready "+ServiceName+"\n")
	}
}

func TestWaitForActiveRouteFailsRatherThanWaitingWhenBootRequiresReady(t *testing.T) {
	// A fresh boot that registered the endpoint promised that this daemon has a
	// route. If its initial channel disagrees, waiting for a future attach turns
	// that contradiction into an unbounded PID 1 stall.
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(write.Fd())))
	_, _, ok := waitForActiveRoute(context.Background(), entrypoint.NewEnv(map[string]string{}),
		entryChannelPollInterval)
	if ok {
		t.Fatal("waitForActiveRoute() found a route in an empty channel")
	}
}

// mustProviders decodes a composed-table JSON literal, failing the test on a
// typo — a table that does not decode would idle for the WRONG reason and the
// truth table would still pass.
func mustProviders(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decoding providers fixture: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("providers fixture is not an object: %T", v)
	}
	return m
}

const bridgedProviders = `{"cerebras":{
	"api_key_env_name":"CEREBRAS_API_KEY",
	"endpoints":{
		"anthropic":{"base_url":"http://127.0.0.1:8214","wire_api":"anthropic"},
		"openai":{"base_url":"https://api.cerebras.ai/v1","wire_api":"openai-chat-completions"}}}}`

func routeEnv(providers, profiles, useProfiles string) *entrypoint.Env {
	return entrypoint.NewEnv(map[string]string{
		"YOLO_PROVIDERS":    providers,
		"YOLO_PROFILES":     profiles,
		"YOLO_USE_PROFILES": useProfiles,
	})
}

// The bridged boot: claude's active profile resolves to a provider whose
// anthropic endpoint is the jail's own loopback, and the route carries exactly
// the three facts the daemon runs on — the URL's own host:port (WB-D2/D13: the
// port lives ONLY there), the openai upstream, and the credential variable's
// NAME (never a key).
func TestResolveRouteServesABridgedProvider(t *testing.T) {
	route, idle := resolveRoute(routeEnv(bridgedProviders,
		`{"cerebras-fast":{"provider":"cerebras"}}`,
		`{"claude":"cerebras-fast"}`))
	if idle != "" {
		t.Fatalf("a bridged route must serve, got idle: %s", idle)
	}
	if route.ListenAddr != "127.0.0.1:8214" {
		t.Errorf("ListenAddr = %q, want the URL's own host:port", route.ListenAddr)
	}
	if route.UpstreamBaseURL != "https://api.cerebras.ai/v1" {
		t.Errorf("UpstreamBaseURL = %q", route.UpstreamBaseURL)
	}
	if route.KeyEnvName != "CEREBRAS_API_KEY" {
		t.Errorf("KeyEnvName = %q, want the variable NAME (never a key)", route.KeyEnvName)
	}
	if route.ProviderName != "cerebras" {
		t.Errorf("ProviderName = %q", route.ProviderName)
	}
}

func TestResolveRouteServesClaudeCodexWithoutProviderTableEntry(t *testing.T) {
	route, idle := resolveRoute(routeEnv(`{}`, `{"codex":{"provider":"openai-codex"}}`, `{"claude":"codex"}`))
	if idle != "" {
		t.Fatalf("Claude codex profile must serve, got idle: %s", idle)
	}
	if route.ProviderName != "openai-codex" || route.ListenAddr != CodexResponsesListenAddr || route.UpstreamBaseURL != CodexResponsesBaseURL || !route.CodexAccessToken {
		t.Fatalf("Codex route = %+v", route)
	}
}

// codexProviders is openai-codex as the resolver composes it once packs/openai-auth
// and packs/wire-bridge are both selected: the subscription's own Responses endpoint
// (packs/openai-auth declares it, credential-free), plus the anthropic address
// packload.adaptEndpoints writes in for the `openai-responses → anthropic` adaptation.
// The argument is that written address — the adapter's declaration by default, and
// whatever a user-scope `adapters.openai-responses->anthropic.address` moved it to
// otherwise, which is the only field such an override may set. It is also, verbatim,
// what packs/claude's env derive hands claude as ANTHROPIC_BASE_URL.
func codexProviders(anthropic string) string {
	return `{"openai-codex":{"capabilities":["web_search"],"endpoints":{
		"openai-responses":{"base_url":"https://chatgpt.com/backend-api/codex","wire_api":"openai-responses"},
		"anthropic":{"base_url":"` + anthropic + `"}}}}`
}

func codexRoute(t *testing.T, providers string) (route, string) {
	t.Helper()
	return resolveRoute(routeEnv(providers, `{"codex":{"provider":"openai-codex"}}`, `{"claude":"codex"}`))
}

// The defect this pins: the Codex branch used to return CodexResponsesListenAddr
// without reading the table, so a user who moved the adaptation's address moved
// claude's ANTHROPIC_BASE_URL and NOT the bind — claude dialing 9215 at a daemon
// listening on 8215, every request refused. The bind follows the composed entry, the
// way the cerebras route's always has.
func TestResolveRouteBindsTheCodexAddressTheComposedEntryNames(t *testing.T) {
	route, idle := codexRoute(t, codexProviders("http://127.0.0.1:9215"))
	if idle != "" {
		t.Fatalf("an overridden Codex adaptation must still serve, got idle: %s", idle)
	}
	if route.ListenAddr != "127.0.0.1:9215" {
		t.Errorf("ListenAddr = %q, want the composed entry's address — the one claude dials", route.ListenAddr)
	}
	if route.UpstreamBaseURL != CodexResponsesBaseURL || !route.CodexAccessToken {
		t.Errorf("the override must move the BIND only, got %+v", route)
	}
}

// The declaration-as-default half: an entry that names no anthropic endpoint — the
// launch whose packs never composed the adaptation — binds exactly what it bound
// before the route read the table at all.
func TestResolveRouteDefaultsTheCodexBindWhenTheEntryNamesNoAnthropicEndpoint(t *testing.T) {
	route, idle := codexRoute(t, `{"openai-codex":{"endpoints":{
		"openai-responses":{"base_url":"https://chatgpt.com/backend-api/codex","wire_api":"openai-responses"}}}}`)
	if idle != "" {
		t.Fatalf("an unadapted Codex entry must still serve, got idle: %s", idle)
	}
	if route.ListenAddr != CodexResponsesListenAddr {
		t.Errorf("ListenAddr = %q, want the declared default %q", route.ListenAddr, CodexResponsesListenAddr)
	}
}

// A non-loopback anthropic endpoint is somebody else's route on the Codex branch for
// the same reason it is on the general one, and the fallback that would bind
// CodexResponsesListenAddr at an agent dialing example.com is the defect above wearing
// a different address.
func TestResolveRouteSkipsACodexEntryRoutedAwayFromThisJail(t *testing.T) {
	_, idle := codexRoute(t, codexProviders("https://anthropic.example/v1"))
	if !strings.Contains(idle, "is not jail-local") {
		t.Fatalf("a Codex entry routed elsewhere must not bind here, got idle %q", idle)
	}
}

// The general route under the same override, unchanged — the regression guard for
// making the two symmetric. cerebras has always taken its bind off the composed
// entry, and making the Codex branch do so must not have moved it.
func TestResolveRouteStillBindsTheCerebrasAddressTheComposedEntryNames(t *testing.T) {
	route, idle := resolveRoute(routeEnv(`{"cerebras":{"api_key_env_name":"CEREBRAS_API_KEY","endpoints":{
		"anthropic":{"base_url":"http://127.0.0.1:9214","wire_api":"anthropic"},
		"openai":{"base_url":"https://api.cerebras.ai/v1","wire_api":"openai-chat-completions"}}}}`,
		`{"cerebras-fast":{"provider":"cerebras"}}`, `{"claude":"cerebras-fast"}`))
	if idle != "" {
		t.Fatalf("an overridden cerebras adaptation must still serve, got idle: %s", idle)
	}
	if route.ListenAddr != "127.0.0.1:9214" {
		t.Errorf("ListenAddr = %q, want the composed entry's address", route.ListenAddr)
	}
	if route.UpstreamBaseURL != "https://api.cerebras.ai/v1" || route.KeyEnvName != "CEREBRAS_API_KEY" {
		t.Errorf("the override must move the BIND only, got %+v", route)
	}
}

// The chat-completions route reads the selected profile's
// supports_usage_in_streaming (the provider's declared default arrives in the
// resolved profile, with a user's value over it) and turns stream usage off
// only for the JSON spelling "false" — pi's rule for the same service fact, so
// a typo cannot silently switch the request shape.
func TestResolveRouteReadsTheStreamUsageServiceFact(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		wantOmit      bool
	}{
		{"undeclared keeps the default", `{"p":{"provider":"cerebras"}}`, false},
		{"true keeps the default", `{"p":{"provider":"cerebras","supports_usage_in_streaming":"true"}}`, false},
		{"false omits stream_options", `{"p":{"provider":"cerebras","supports_usage_in_streaming":"false"}}`, true},
		{"an unrecognized value keeps the default", `{"p":{"provider":"cerebras","supports_usage_in_streaming":"no"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route, idle := resolveRoute(routeEnv(bridgedProviders, tc.profile, `{"claude":"p"}`))
			if idle != "" {
				t.Fatalf("a bridged route must serve, got idle: %s", idle)
			}
			if route.OmitStreamUsage != tc.wantOmit {
				t.Errorf("OmitStreamUsage = %v, want %v", route.OmitStreamUsage, tc.wantOmit)
			}
		})
	}
}

func TestResolveRouteDoesNotServePiCodexProfile(t *testing.T) {
	_, idle := resolveRoute(routeEnv(`{}`, `{"codex":{"provider":"openai-codex"}}`, `{"pi":"codex"}`))
	if !strings.Contains(idle, "not in the composed table") {
		t.Fatalf("Pi's built-in provider must not start the Claude bridge, got idle %q", idle)
	}
}

// TestRunWakesForAnAttachedRoute pins the idle-to-serving lifecycle: it starts
// from Pi's built-in codex selection (which must leave this bridge idle), then
// requires the production runner to bind and publish after a live channel
// rewrite selects a local test route.
func TestRunWakesForAnAttachedRoute(t *testing.T) {
	home := t.TempDir()
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	oldEndpointFile := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = oldEndpointFile })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	writeChannel(t, home, `{}`, `{"codex":{"provider":"openai-codex"}}`, `{"pi":"codex"}`)
	initial := routeEnv(`{}`, `{"codex":{"provider":"openai-codex"}}`, `{"pi":"codex"}`)
	initial.Vars["JAIL_HOME"] = home
	initial.Home = home

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, initial, 5*time.Millisecond) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("wire bridge did not stop after cancellation")
		}
	})

	if _, err := os.Stat(endpointFile); !os.IsNotExist(err) {
		t.Fatalf("Pi codex selection published an endpoint: %v", err)
	}
	providers := `{"bridge":{"endpoints":{"anthropic":{"base_url":"http://` + addr + `"},"openai":{"base_url":"https://upstream.example/v1"}}}}`
	writeChannel(t, home, providers, `{"bridge":{"provider":"bridge"}}`, `{"claude":"bridge"}`)

	deadline := time.Now().Add(time.Second)
	for {
		if data, err := os.ReadFile(endpointFile); err == nil {
			conn, err := net.DialTimeout("tcp", strings.TrimSpace(string(data)), 100*time.Millisecond)
			if err != nil {
				t.Fatalf("published endpoint did not accept connections: %v", err)
			}
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bridge stayed idle after the attached channel selected a route")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWaitForActiveRouteSeesAttachedClaudeCodex(t *testing.T) {
	home := t.TempDir()
	writeChannel(t, home, `{}`, `{"codex":{"provider":"openai-codex"}}`, `{"pi":"codex"}`)
	initial := routeEnv(`{}`, `{"codex":{"provider":"openai-codex"}}`, `{"pi":"codex"}`)
	initial.Vars["JAIL_HOME"] = home
	initial.Home = home
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		route route
		ok    bool
	}
	resultCh := make(chan result, 1)
	go func() {
		route, _, ok := waitForActiveRoute(ctx, initial, 5*time.Millisecond)
		resultCh <- result{route, ok}
	}()
	writeChannel(t, home, `{}`, `{"codex":{"provider":"openai-codex"}}`, `{"claude":"codex"}`)
	select {
	case got := <-resultCh:
		if !got.ok || !got.route.CodexAccessToken || got.route.ListenAddr != CodexResponsesListenAddr {
			t.Fatalf("attached Claude codex route = %+v, ok=%v", got.route, got.ok)
		}
	case <-time.After(time.Second):
		t.Fatal("idle bridge did not see the attached Claude codex profile")
	}
}

func writeChannel(t *testing.T, home, providers, profiles, useProfiles string) {
	t.Helper()
	path := filepath.Join(home, ".config", "yolo-user-env.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := entrypoint.EntryChannelSectionHeader + "\n" +
		"export YOLO_PROVIDERS='" + providers + "'\n" +
		"export YOLO_PROFILES='" + profiles + "'\n" +
		"export YOLO_USE_PROFILES='" + useProfiles + "'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every idle reason the design names (§3.4), each a HEALTHY no-op. The reasons
// are asserted by substring so a regression says WHICH absent fact it hit.
func TestResolveRouteIdles(t *testing.T) {
	cases := []struct {
		name                         string
		providers, profiles, useProf string
		wantIdle                     string
	}{
		{"no profile active at all", bridgedProviders,
			`{"cerebras-fast":{"provider":"cerebras"}}`, `{}`,
			"no profile is active in YOLO_USE_PROFILES"},
		{"profile resolves to no provider", bridgedProviders,
			`{}`, `{"claude":"cerebras-fast"}`,
			"resolves to no provider"},
		{"provider missing from the table",
			`{"other":{"endpoints":{"anthropic":{"base_url":"http://127.0.0.1:8214"}}}}`,
			`{"cerebras-fast":{"provider":"cerebras"}}`, `{"claude":"cerebras-fast"}`,
			"not in the composed table"},
		{"no anthropic endpoint", `{"cerebras":{
			"endpoints":{"openai":{"base_url":"https://api.cerebras.ai/v1"}}}}`,
			`{"p":{"provider":"cerebras"}}`, `{"claude":"p"}`,
			"declares no anthropic endpoint"},
		{"anthropic endpoint not loopback", `{"zai":{
			"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
			"openai":{"base_url":"https://api.z.ai/api/paas/v4"}}}}`,
			`{"p":{"provider":"zai"}}`, `{"claude":"p"}`,
			"is not jail-local"},
		{"no openai endpoint", `{"anthropic-only":{
			"endpoints":{"anthropic":{"base_url":"http://127.0.0.1:8214"}}}}`,
			`{"p":{"provider":"anthropic-only"}}`, `{"claude":"p"}`,
			"declares no openai endpoint"},
		{"openai endpoint speaks the other wire", `{"responses-only":{
			"endpoints":{"anthropic":{"base_url":"http://127.0.0.1:8214"},
			"openai":{"base_url":"https://x.example/v1","wire_api":"openai-responses"}}}}`,
			`{"p":{"provider":"responses-only"}}`, `{"claude":"p"}`,
			"anthropic ↔ openai-chat-completions"},
		// The generalization (boot.go, "WHO IS SERVED"): the agent's NAME is not
		// the logic — an active profile for any CLI whose derive reads the
		// anthropic endpoint (copilot, per D-3) serves the same route.
		{"serves for a non-claude agent whose profile routes here", bridgedProviders,
			`{"cerebras-fast":{"provider":"cerebras"}}`, `{"copilot":"cerebras-fast"}`,
			""},
		{"a claude idle reason does not mask another agent's live route", bridgedProviders,
			`{"cerebras-fast":{"provider":"cerebras"}}`,
			`{"claude":"not-a-profile","copilot":"cerebras-fast"}`,
			""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, idle := resolveRoute(routeEnv(tc.providers, tc.profiles, tc.useProf))
			if tc.wantIdle == "" {
				if idle != "" {
					t.Fatalf("expected a route, got idle: %s", idle)
				}
				if route.ProviderName != "cerebras" {
					t.Errorf("ProviderName = %q, want the routed provider", route.ProviderName)
				}
				return
			}
			if idle == "" {
				t.Fatalf("expected an idle, got a route: %+v", route)
			}
			if !strings.Contains(idle, tc.wantIdle) {
				t.Errorf("idle reason %q does not name the absent fact (%q)", idle, tc.wantIdle)
			}
		})
	}
}

// WillServe's truth table, over the same inputs the launcher hands it
// (wire-bridge.md §5's WARNING): the serve cases are the bridged routes, and
// every idle case is one of resolveRoute's absent facts. Each row asserts BOTH
// exported answers — WillServe's bool and the boot resolution's idle string —
// because the two call sites (daemon boot, launcher emission) must never be
// able to disagree: if someone splits the decision in two, this table goes red
// on the row that diverged.
func TestWillServeTruthTable(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{
		"cerebras-fast": {Provider: "cerebras"},
	}
	serving := []struct {
		name        string
		providers   *jsonx.OrderedMap
		useProfiles map[string]string
	}{
		{"the shipped bridged route", mustProviders(t, bridgedProviders),
			map[string]string{"claude": "cerebras-fast"}},
		{"the profile active for another agent too", mustProviders(t, bridgedProviders),
			map[string]string{"claude": "cerebras-fast", "pi": "cerebras-fast"}},
	}
	for _, tc := range serving {
		t.Run("serves: "+tc.name, func(t *testing.T) {
			if !WillServe(tc.providers, tc.useProfiles, resolved) {
				t.Errorf("WillServe = false, want true for %s", tc.name)
			}
		})
	}
	idle := []struct {
		name        string
		providers   *jsonx.OrderedMap
		useProfiles map[string]string
	}{
		{"no profile active for claude", mustProviders(t, bridgedProviders), map[string]string{}},
		{"claude rides a provider with no anthropic endpoint",
			mustProviders(t, `{"cerebras":{"endpoints":{"openai":{
				"base_url":"https://api.cerebras.ai/v1","wire_api":"openai-chat-completions"}}}}`),
			map[string]string{"claude": "cerebras-fast"}},
		{"anthropic endpoint not jail-local",
			mustProviders(t, `{"zai":{"endpoints":{
				"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
				"openai":{"base_url":"https://api.z.ai/api/paas/v4"}}}}`),
			map[string]string{"claude": "cerebras-fast"}},
	}
	for _, tc := range idle {
		t.Run("idles: "+tc.name, func(t *testing.T) {
			if WillServe(tc.providers, tc.useProfiles, resolved) {
				t.Errorf("WillServe = true, want false for %s", tc.name)
			}
		})
	}
}

// TestWillServeAndTheBootResolutionAreOneDecision pins the property the
// launcher's emission depends on (§5's WARNING): for any tables, WillServe's
// bool and the daemon boot's idle answer agree. Both call sites go through
// routeFor by construction; this is the test that notices if that stops being
// true.
func TestWillServeAndTheBootResolutionAreOneDecision(t *testing.T) {
	tables := []struct{ providers, profiles, useProfiles string }{
		{bridgedProviders, `{"cerebras-fast":{"provider":"cerebras"}}`, `{"claude":"cerebras-fast"}`},
		{bridgedProviders, `{"cerebras-fast":{"provider":"cerebras"}}`, `{}`},
		{bridgedProviders, `{}`, `{"claude":"cerebras-fast"}`},
		{`{"other":{"endpoints":{"anthropic":{"base_url":"http://127.0.0.1:8214"}}}}`,
			`{"cerebras-fast":{"provider":"cerebras"}}`, `{"claude":"cerebras-fast"}`},
	}
	for i, tc := range tables {
		env := routeEnv(tc.providers, tc.profiles, tc.useProfiles)
		_, idle := resolveRoute(env)
		want := idle == ""
		if got := WillServe(mustProviders(t, tc.providers), useProfilesTable(env.LoadUseProfiles()),
			env.LoadProfiles()); got != want {
			t.Errorf("case %d: WillServe = %v, boot resolution says serve=%v (idle %q) — "+
				"the decision has split in two", i, got, want, idle)
		}
	}
}

func TestLoopbackListenAddr(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"http://127.0.0.1:8214", "127.0.0.1:8214", true},
		{"http://localhost:9000", "127.0.0.1:9000", true},
		{"http://127.0.0.1/", "127.0.0.1:80", true}, // no port: the scheme's IS the URL's port
		{"https://localhost:8443/x", "127.0.0.1:8443", true},
		{"https://api.anthropic.com/v1", "", false}, // somebody else's route
		{"http://10.0.0.5:8214", "", false},         // LAN-local is not loopback
		{"not a url at all", "", false},
	}
	for _, tc := range cases {
		got, ok := loopbackListenAddr(tc.url)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("loopbackListenAddr(%q) = %q,%v want %q,%v", tc.url, got, ok, tc.want, tc.ok)
		}
	}
}

// The key channel (§5): the 0600 yolo-user-env.sh the launcher writes, parsed
// in its frozen `export K=${K:-'v'}` format plus the hand-editable spellings;
// then the process-env fallback; then a miss, which idles the daemon.
func TestKeyFromUserEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yolo-user-env.sh")
	content := strings.Join([]string{
		"# Auto-generated from yolo-jail.env config.",
		"export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'csk-plain'}",
		"export ZAI_API_KEY=${ZAI_API_KEY:-'za'\\''pi'\\''key'}",
		"export HAND_EDITED='hand-value'",
		"export BARE_FORM=bare-value",
		"export EMPTY_VAR=${EMPTY_VAR:-''}",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, variable, want string
		ok                   bool
	}{
		{"the launcher's frozen format", "CEREBRAS_API_KEY", "csk-plain", true},
		{"escaped quote in the value", "ZAI_API_KEY", "za'pi'key", true},
		{"hand-edited single-quoted", "HAND_EDITED", "hand-value", true},
		{"hand-edited bare", "BARE_FORM", "bare-value", true},
		{"empty value is a miss, not a hit", "EMPTY_VAR", "", false},
		{"absent variable", "NOPE", "", false},
	}
	for _, tc := range cases {
		got, ok := keyFromUserEnvFile(path, tc.variable)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: keyFromUserEnvFile = %q,%v want %q,%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := keyFromUserEnvFile(filepath.Join(dir, "missing.sh"), "ANY"); ok {
		t.Errorf("a missing file must be a miss (the fallback decides next)")
	}
}

func TestResolveKeyFallsBackToProcessEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIREBRIDGE_TEST_KEY", "from-env")
	if got, _ := resolveKey("WIREBRIDGE_TEST_KEY", dir); got != "from-env" {
		t.Errorf("fallback = %q, want the process environment's value", got)
	}
	// The file wins over the environment when both are present.
	path := filepath.Join(dir, ".config", "yolo-user-env.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export WIREBRIDGE_TEST_KEY=${WIREBRIDGE_TEST_KEY:-'from-file'}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, source := resolveKey("WIREBRIDGE_TEST_KEY", dir)
	if got != "from-file" || !strings.HasSuffix(source, "yolo-user-env.sh") {
		t.Errorf("resolveKey = %q from %q, want the file's value from the file", got, source)
	}
	// No variable named: not a miss — a serve-without-credential provider.
	if got, source := resolveKey("", dir); got != "" || source != "" {
		t.Errorf("an empty keyEnvName is the no-credential case, got %q from %q", got, source)
	}
	// A named variable that is nowhere: the zero result that idles the daemon.
	if got, source := resolveKey("WIREBRIDGE_TEST_NOWHERE", dir); got != "" || source != "" {
		t.Errorf("a named-but-absent variable must be a miss, got %q from %q", got, source)
	}
}

// The endpoint file: written only after the bind, one address line, 0600, in a
// 0700 directory — and deliberately NOT the svcendpoint credential triple
// (endpoint.go carries the why).
func TestPublishEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc", "wire-bridge.endpoint")
	if err := publishEndpoint(path, "127.0.0.1:8214"); err != nil {
		t.Fatalf("publishEndpoint: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "127.0.0.1:8214\n" {
		t.Errorf("file = %q, want the bound address and nothing else", data)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	dirFi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirFi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dirFi.Mode().Perm())
	}
	// Republishing replaces the line whole — the stale-address case a torn
	// write would leave behind is what the rename discipline exists for.
	if err := publishEndpoint(path, "127.0.0.1:9999"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "127.0.0.1:9999\n" {
		t.Errorf("republish = %q, want only the new address", data)
	}
}
