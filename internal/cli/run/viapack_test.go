package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// viapack_test.go pins the `via` half of the selection closure (docs/design/
// wire-bridge-gateway.md OQ-WG6/WG7 (c)): a selected profile whose `via` names a service
// pack adds that pack like a need, so a pi-only jail gets the bridge its profile routes
// through.

func loadedNames(t *testing.T, o *Options, cname string) []string {
	t.Helper()
	_, loaded, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	var names []string
	for _, p := range loaded {
		names = append(names, p.Name)
	}
	return names
}

// TestAViaProfileBringsItsServicePack: pi alone selects no pack that needs the bridge,
// and a -p selecting a via profile for pi adds it, disclosing why.
func TestAViaProfileBringsItsServicePack(t *testing.T) {
	home := packHome(t)
	// zai is selected for its provider, so pi's derive has a zai row to point at the via URL.
	// Without it the via re-points nothing, and the via-route pre-flight (checkViaRoutes)
	// says so instead of the launch routing anything (WG-I15).
	writeUserConfig(t, home, `{"packs": ["pi", "zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}}}`)
	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf,
		UseProfiles: map[string]string{"pi": "pz"}}
	names := loadedNames(t, o, "yolo-test-via-joined")
	if !hasName(names, "wire-bridge") {
		t.Fatalf("a via profile did not bring its service pack: loaded = %v", names)
	}
	if got := errBuf.String(); !strings.Contains(got, "+ wire-bridge (via of profile pz, active for pi)") {
		t.Errorf("the launch must disclose why the pack joined:\n%s", got)
	}
}

// TestNoViaProfileLeavesThePackSetAlone is the control: the same config with the profile
// not selected (or no via on it) stages no bridge.
func TestNoViaProfileLeavesThePackSetAlone(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}, "plain": {"provider": "zai"}}}`)
	for name, use := range map[string]map[string]string{
		"unselected": nil,
		"no via":     {"pi": "plain"},
	} {
		o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(), UseProfiles: use}
		if names := loadedNames(t, o, "yolo-test-via-"+strings.ReplaceAll(name, " ", "-")); hasName(names, "wire-bridge") {
			t.Errorf("%s: the bridge joined without a selected via profile: %v", name, names)
		}
	}
}

// TestAViaNamingAPackThatServesNoViaRouteIsRefused: a via must name a service pack that
// declares a via_address; claude is embedded but serves none, so the launch refuses.
func TestAViaNamingAPackThatServesNoViaRouteIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pz": {"provider": "zai", "via": "claude"}}}`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"pi": "pz"}}
	_, _, _, err := o.stagePacks("yolo-test-via-noservice")
	if err == nil || !strings.Contains(err.Error(), "declares no service with a via_address") {
		t.Fatalf("err = %v, want a refusal naming the missing via_address", err)
	}
}

// viaStagingCfg is the merged config the via-route pre-flight composes providers from,
// declaring one user provider under name.
func viaStagingCfg(t *testing.T, name, endpointsJSON string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(`{"providers": {"` + name + `": {"endpoints": ` + endpointsJSON + `}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.OrderedMap)
}

// TestAViaWithNoRouteForItsAgentIsRefused pins WG-I13 at the launch: a via profile whose
// provider offers neither wire the via route passes through leaves pi's prefix unserved,
// so stagePacks refuses, naming the profile, the agent and the missing endpoint — rather
// than starting a jail whose pi gets a 404 at its first request.
func TestAViaWithNoRouteForItsAgentIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pa": {"provider": "anth", "via": "wire-bridge"}}}`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"pi": "pa"},
		stagingCfg:  viaStagingCfg(t, "anth", `{"anthropic": {"base_url": "https://anth.example"}}`)}
	_, _, _, err := o.stagePacks("yolo-test-via-noroute")
	if err == nil {
		t.Fatal("a via profile the bridge serves no route for must refuse the launch")
	}
	for _, want := range []string{`profile "pa" (active for pi)`,
		"declares no chat-completions or Responses endpoint", "http://127.0.0.1:8216/agent/pi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q:\n%v", want, err)
		}
	}
}

// TestAViaRouteWithoutTheAgentsWireWarns pins WG-I14 at the launch: the provider offers only
// Responses, pi prefers chat-completions, and which wire pi's client sends is not the
// launcher's to observe — so the launch warns, naming the missing endpoint, and proceeds.
func TestAViaRouteWithoutTheAgentsWireWarns(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pr": {"provider": "resp", "via": "wire-bridge"}}}`)
	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf,
		UseProfiles: map[string]string{"pi": "pr"},
		stagingCfg: viaStagingCfg(t, "resp",
			`{"openai": {"base_url": "https://resp.example/v1", "wire_api": "openai-responses"}}`)}
	if _, _, _, err := o.stagePacks("yolo-test-via-partial"); err != nil {
		t.Fatalf("a route without the agent's preferred wire must warn, not refuse: %v", err)
	}
	got := errBuf.String()
	for _, want := range []string{`profile "pr" (active for pi)`,
		"provider resp declares no chat-completions endpoint"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning must name %q:\n%s", want, got)
		}
	}
}

// TestAViaForAnAgentThisLaunchDoesNotCarryAddsNothing pins WG-I10 at the launch: a
// use_profiles key may name any CLI a resolvable pack installs, so a user-scope entry for
// pi in a launch without pi must not bring the bridge in, nor claim it is "active for pi".
func TestAViaForAnAgentThisLaunchDoesNotCarryAddsNothing(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}}}`)
	cfg, err := jsonx.Decode([]byte(`{"use_profiles": {"pi": "pz"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf,
		stagingCfg: cfg.(*jsonx.OrderedMap)}
	if names := loadedNames(t, o, "yolo-test-via-absent"); hasName(names, "wire-bridge") {
		t.Errorf("the bridge joined for an agent no selected pack installs: %v", names)
	}
	if strings.Contains(errBuf.String(), "active for pi") {
		t.Errorf("the launch claimed a via active for an absent agent:\n%s", errBuf.String())
	}
}

// TestTheLazyResolversApplyTheViaClosure pins WG-I11 for the read-only surfaces behind
// `yolo loopholes list` and config validation's loophole checks: the pack set they read is
// the launch's, so a pack an active via profile adds is in it.
func TestTheLazyResolversApplyTheViaClosure(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi", "zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}},
	  "use_profiles": {"pi": "pz"}}`)
	var names []string
	for _, p := range resolveConfiguredPacks() {
		names = append(names, p.Name)
	}
	if !hasName(names, "wire-bridge") {
		t.Errorf("the lazy pack set omits the pack pi's via profile adds: %v", names)
	}
}

// TestAViaThatRepointsNothingStartsWithANotice pins WG-I15 at the launch: pi implements the
// ChatGPT subscription natively and its derive never points openai-codex at the via URL, so
// a via over it changes nothing pi sends. The launch starts, and says the via has no effect,
// rather than refusing a launch that works.
func TestAViaThatRepointsNothingStartsWithANotice(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi", "openai-auth"],
	  "profiles": {"sub": {"provider": "openai-codex", "via": "wire-bridge"}}}`)
	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf,
		UseProfiles: map[string]string{"pi": "sub"}}
	if _, _, _, err := o.stagePacks("yolo-test-via-inert"); err != nil {
		t.Fatalf("a via that re-points nothing must not refuse the launch: %v", err)
	}
	got := errBuf.String()
	for _, want := range []string{`profile "sub" (active for pi)`, "has no effect on pi", "ChatGPT subscription"} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice must name %q:\n%s", want, got)
		}
	}
}
