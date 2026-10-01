package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// TestACarriedAgentWithNoRouteIsRefused pins the gate for an agent a profile's carrier carries
// (packload.ResolvedProfile.ViaFor, docs/design/wire-bridge-gateway.md WG-I44): oh-omp on plain
// `-p bedrock` is pointed at its via route, and a provider region the bridge composes no runtime
// host from leaves that route unserved, so the launch refuses, naming oh-omp and the region, with
// a carried agent's remedy: the profile names no via to drop and oh-omp has no Bedrock client to
// fall back on. `yolo check` runs this gate on every launch it predicts, so the launch must run it
// on a profile naming no via too, or the two disagree.
func TestACarriedAgentWithNoRouteIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["omp", "bedrock", "wire-bridge"]}`)
	cfg, err := jsonx.Decode([]byte(`{"providers": {"bedrock": {"region": "us-central1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"oh-omp": "bedrock"}, stagingCfg: cfg.(*jsonx.OrderedMap)}
	_, _, _, err = o.stagePacks("yolo-test-carried-noroute")
	if err == nil {
		t.Fatal("a carried agent the bridge serves no route for must refuse the launch")
	}
	for _, want := range []string{`profile "bedrock" (active for oh-omp)`,
		`it has no client of provider "bedrock"'s platform, so wire-bridge carries it`,
		`its region "us-central1" is not an AWS region`, "http://127.0.0.1:8216/agent/oh-omp",
		"select another profile for oh-omp (`-p oh-omp=<name>`)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `remove "via"`) {
		t.Errorf("the refusal offers to remove a via the profile does not name:\n%v", err)
	}
}

// TestACarriedAgentWhoseAdapterRouteIsTakenIsRefused pins WG-I45 at the launch
// (docs/design/wire-bridge-gateway.md): the bridge serves one adapter route, and claude on
// cerebras takes it, so copilot carried on plain `-p bedrock`, pointed at the same adapter
// address, would send its requests to cerebras with claude's key. Before the carrier copilot
// reached nothing there and the launch started; it now refuses, naming both agents and both
// providers.
func TestACarriedAgentWhoseAdapterRouteIsTakenIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["claude", "copilot", "cerebras"]}`)
	cfg, err := jsonx.Decode([]byte(`{"providers": {"bedrock": {"region": "us-east-1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"claude": "cerebras", "copilot": "bedrock"}, stagingCfg: cfg.(*jsonx.OrderedMap)}
	_, _, _, err = o.stagePacks("yolo-test-carried-taken")
	if err == nil {
		t.Fatal("copilot carried at an adapter route claude's cerebras holds must refuse the launch")
	}
	for _, want := range []string{`profile "bedrock" (active for copilot)`,
		`this launch gives it to claude on provider cerebras (profile "cerebras")`,
		"would reach provider cerebras", "another profile for copilot (`-p copilot=<name>`)"} {
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
// profile key may name any CLI a resolvable pack installs, so a user-scope entry for
// pi in a launch without pi must not bring the bridge in, nor claim it is "active for pi".
func TestAViaForAnAgentThisLaunchDoesNotCarryAddsNothing(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}}}`)
	cfg, err := jsonx.Decode([]byte(`{"profile": {"pi": "pz"}}`))
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
	  "profile": {"pi": "pz"}}`)
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

// TestMacosUserDoesNotGateAViaItClears is the via-route pre-flight at the notch that serves no
// bridge (notch convergence item 2, NC-D16): the same profile TestAViaWithNoRouteForItsAgentIsRefused
// refuses on a container launch is cleared on macos-user, where pi keeps its own client, so the
// pre-flight asks nothing of a route no daemon would serve. checkViaRoutes composes as
// composePackChannel does, served set included; deleting its ViaServedAt call fails this.
func TestMacosUserDoesNotGateAViaItClears(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pa": {"provider": "anth", "via": "wire-bridge"}}}`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"pi": "pa"},
		stagingCfg:  viaStagingCfg(t, "anth", `{"anthropic": {"base_url": "https://anth.example"}}`)}
	_, _, _, err := o.stagePacks("yolo-test-via-macos-control")
	if err == nil {
		t.Fatal("the container control no longer refuses, so this proves nothing")
	}
	o.runtime = "macos-user"
	_, loaded, _, err := o.stagePacks("yolo-test-via-macos")
	if err != nil {
		t.Fatalf("macos-user gated a via it clears: %v", err)
	}
	if !hasName(namesOf(loaded), "wire-bridge") {
		t.Fatalf("the via did not bring the bridge, so the pre-flight had a via to ask about: %v", namesOf(loaded))
	}
}

func namesOf(packs []*packload.Pack) []string {
	var names []string
	for _, p := range packs {
		names = append(names, p.Name)
	}
	return names
}
