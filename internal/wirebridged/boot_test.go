package wirebridged

// boot_test.go pins the selection-lazy boot read (wire-bridge.md §3.4): the
// composed table decides serve-vs-idle, and every idle reason is the healthy
// no-op the design licenses the coarse when_bins inclusion with. The tables are
// hand-built YOLO_* values fed through the same loaders the real boot reads
// (entrypoint.NewEnv → LoadProviders/LoadProfiles/LoadUseProfiles), so a change
// in the wire shape of either side fails here rather than in a jail.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

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
