package run

// macosuserviaroute_test.go pins the wire bridge's VIA and CARRIER routes on macos-user
// (docs/design/host-notch-services.md HS-D30, HS-D31; docs/design/wire-bridge-gateway.md
// WG-I46). That backend runs a pack service only as a launch-owned host half, so a profile whose
// `via` (or whose carrier, WG-I44) routes an agent through the bridge is composed against the host
// half the launch plans for it, at the port the launch picked for the bridge's `via_address`.
// Unit-level: the channel is composed for real and the start is observed through its seam, so no
// service and no agent runs.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// viaRoutePacks is claude, cerebras and the bridge (claude's adapter pairing plans the bridge's
// host half), beside pi, bedrock and aws-auth (pi's bedrock-bridge profile routes it through the
// bridge's via address).
func viaRoutePacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "cerebras"),
		officialPack(t, "wire-bridge"), officialPack(t, "pi"), officialPack(t, "bedrock"),
		officialPack(t, "aws-auth")}
}

// selectPerAgent sets the config's per-agent `profile` table and gives the shipped bedrock
// provider a region, so the region pre-flight is not what the test measures.
func selectPerAgent(table map[string]string) func(*Options, *jsonx.OrderedMap) {
	return func(o *Options, cfg *jsonx.OrderedMap) {
		sel := jsonx.NewOrderedMap()
		for agent, profile := range table {
			sel.Set(agent, profile)
		}
		cfg.Set("profile", sel)
		withBedrockRegion(cfg)
	}
}

// viaPort is the port of pi's composed via URL, the address its derive points it at.
func viaPort(t *testing.T, r packload.ResolvedProfile, agent string) string {
	t.Helper()
	raw := packload.ViaURLFor(r, agent)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("%s's via URL = %q, want an address", agent, raw)
	}
	return u.Host
}

// THE LIVE DEFECT (MEASURED at 5bdac0a8d by the parity probe): with claude's cerebras pairing
// planning the bridge's host half, pi on bedrock-bridge kept http://127.0.0.1:8216/agent/pi, the
// declared via address, which the plan neither reserved nor served. The plan now reserves the
// via address beside the adaptations (launchservice.NewPlan), so pi's via URL names the port
// this launch picked, which the host half listens on.
func TestMacosUserPointsAViaAtThePortThePlannedBridgeServes(t *testing.T) {
	packs := viaRoutePacks(t)
	o, cfg, _, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(),
		selectPerAgent(map[string]string{"claude": "cerebras", "pi": "bedrock-bridge"}))
	o.runtime = "macos-user"
	o.launchServices = nil
	channel, err := o.composePackChannel(cfg, packs, cerebrasKey())
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(o.launchServices) != 1 || o.launchServices[0].Service != "wire-bridge" {
		t.Fatalf("launch services = %+v, want the wire bridge once", o.launchServices)
	}
	picked := o.launchServices[0].Moved["127.0.0.1:8216"]
	if picked == "" || picked == "127.0.0.1:8216" {
		t.Fatalf("the plan did not reserve the bridge's via address 127.0.0.1:8216: %v", o.launchServices[0].Moved)
	}
	if got := viaPort(t, channel.resolvedProfiles["bedrock-bridge"], "pi"); got != picked {
		t.Errorf("pi's via URL names %s, want the port the plan picked for the via address, %s", got, picked)
	}
	if len(channel.unservedVias) != 0 {
		t.Errorf("the launch named %v as unserved vias, though it serves the bridge", channel.unservedVias)
	}
}

// THE TRIGGER (HS-D30): pi alone on bedrock-bridge pairs through no adapter, so the gate refuses
// nothing and the adapter trigger plans nothing; the via trigger plans the bridge's host half,
// because the what-if composition with the bridge served routes pi through it. Before it, the via
// was cleared and pi kept its own Bedrock client on a profile that asked for the bridge.
func TestMacosUserPlansTheBridgeForAViaProfile(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "bedrock"),
		officialPack(t, "aws-auth"), officialPack(t, "wire-bridge")}
	o, cfg, _, _ := attachFixture(t, currentJailEnv, packs, nil,
		selectPerAgent(map[string]string{"pi": "bedrock-bridge"}))
	o.runtime = "macos-user"
	o.launchServices = nil
	channel, err := o.composePackChannel(cfg, packs, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(o.launchServices) != 1 || o.launchServices[0].Service != "wire-bridge" {
		t.Fatalf("launch services = %+v, want the wire bridge planned for pi's via", o.launchServices)
	}
	plan := o.launchServices[0]
	if got := o.launchServiceAgents["wire-bridge"]; len(got) != 1 || got[0] != "pi" {
		t.Errorf("the bridge is planned for %v, want pi", got)
	}
	if got := viaPort(t, channel.resolvedProfiles["bedrock-bridge"], "pi"); got != plan.Moved["127.0.0.1:8216"] {
		t.Errorf("pi's via URL names %s, want the picked %s", got, plan.Moved["127.0.0.1:8216"])
	}
	if len(channel.unservedVias) != 0 {
		t.Errorf("the via was cleared (%v), though the launch plans its service", channel.unservedVias)
	}
}

// A CARRIER ROUTES TOO (WG-I44): copilot on plain `-p bedrock` has no Bedrock client of its own,
// and the bridge carries it once it is served. The via trigger plans the bridge, and copilot is
// pointed at the adapter address composed for the carrier, at the port this launch picked.
func TestMacosUserPlansTheBridgeForACarriedAgent(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "copilot"), officialPack(t, "bedrock"),
		officialPack(t, "aws-auth"), officialPack(t, "wire-bridge")}
	o, cfg, _, _ := attachFixture(t, currentJailEnv, packs, nil,
		selectPerAgent(map[string]string{"copilot": "bedrock"}))
	o.runtime = "macos-user"
	o.launchServices = nil
	channel, err := o.composePackChannel(cfg, packs, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(o.launchServices) != 1 || o.launchServices[0].Service != "wire-bridge" {
		t.Fatalf("launch services = %+v, want the wire bridge planned for copilot's carrier", o.launchServices)
	}
	picked := o.launchServices[0].Moved["127.0.0.1:8214"]
	env := map[string]string{}
	for _, v := range channel.scope.Agent("copilot").Shape {
		env[v.Key] = v.Value
	}
	if base := env["COPILOT_PROVIDER_BASE_URL"]; picked == "" || !strings.Contains(base, picked) {
		t.Errorf("copilot's COPILOT_PROVIDER_BASE_URL = %q, want the picked %s", base, picked)
	}
	if env["COPILOT_PROVIDER_API_KEY"] != o.launchServices[0].Token {
		t.Errorf("copilot's provider key is not the bridge's caller token")
	}
}

// THE ARM STARTS IT AND STOPS IT (TestTheMacosUserArmStartsTheServiceAndStopsItAfterTheCommand's
// shape, for the via): `-p bedrock-bridge -- pi` and `-p bedrock -- copilot` each start the bridge
// before the sandboxed command, hand the command the picked port in its YOLO_PROFILES (pi's via
// base) or its provider base URL (copilot's carrier), and stop it after. Deleting the via trigger
// in composePackChannel fails both.
func TestTheMacosUserArmStartsTheBridgeForAViaOrCarrierAndStopsIt(t *testing.T) {
	for _, tc := range []struct {
		agent, profile, pack string
	}{
		{"pi", "bedrock-bridge", "pi"},
		{"copilot", "bedrock", "copilot"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["`+tc.pack+`", "bedrock", "wire-bridge"]`+
				bedrockRegionMember+`}`, shellWith(nil))
			o.Args = []string{tc.agent}
			o.ProfileName = tc.profile
			var started []*launchservice.Plan
			stopped, stoppedBeforeRun := 0, -1
			orig := startMacosUserService
			startMacosUserService = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
				started = append(started, p)
				return fakeLaunched{&stopped}, "/log/launch-service-wire-bridge.log", nil
			}
			t.Cleanup(func() { startMacosUserService = orig })
			run := o.MacosUserRun
			o.MacosUserRun = func(cfg *jsonx.OrderedMap, ws string, a, b []string, c, d string, h macosuser.HomeOverlay,
				ctx macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
				stoppedBeforeRun = stopped
				return run(cfg, ws, a, b, c, d, h, ctx, dry, env, bt, jd)
			}
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\n%s", rc, stderr.String())
			}
			if len(started) != 1 || started[0].Service != "wire-bridge" {
				t.Fatalf("started %v, want the wire bridge\n%s", started, stderr.String())
			}
			if stoppedBeforeRun != 0 || stopped != 1 {
				t.Errorf("stopped %d times before the command ran and %d after; want 0 and 1", stoppedBeforeRun, stopped)
			}
			// pi rides the via route, whose base the profile table carries (its derive writes pi's
			// config file from it at boot); copilot rides the adapter address composed for its
			// carrier, which its env derive hands it as its provider base URL.
			addr, key := started[0].Moved["127.0.0.1:8216"], "YOLO_PROFILES"
			if tc.agent == "copilot" {
				addr, key = started[0].Moved["127.0.0.1:8214"], "COPILOT_PROVIDER_BASE_URL"
			}
			if addr == "" {
				t.Fatalf("the plan moved no address the agent rides: %v", started[0].Moved)
			}
			got, _ := seen.env.Get(key)
			if s, _ := got.(string); !strings.Contains(s, addr) {
				t.Errorf("the command's %s does not name the picked %s:\n%v", key, addr, got)
			}
			if !strings.Contains(stderr.String(), `Started the "wire-bridge" service (pack "wire-bridge", pid 4242) on `+addr+" ") {
				t.Errorf("the start line must name the route %s rides, %s, and no other:\n%s", tc.agent, addr, stderr.String())
			}
			if strings.Contains(stderr.String(), `'s via — its service does not run here`) {
				t.Errorf("the launch still names the via as unserved:\n%s", stderr.String())
			}
		})
	}
}

// PI'S VIA ROW NAMES THE CALLER TOKEN BY VARIABLE (pi's derive writes `apiKey:
// "${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}"`, WG-I36), so the session that starts pi must carry that
// variable, holding the token the bridge's host half was started with: a container's shared
// channel exports it to every process, and on macos-user the session env is that channel. Before
// this, the arm handed the session only the tokens of the jail daemons and doorways it serves
// (ServedJailDaemons), never a launch-owned service's, so pi's via route (and any shell that
// starts pi, the hardware probe in integration/macosuserviaroute_test.go) sent no token and the
// bridge answered 401. Both a `-- pi` and a `-- bash` session count: the token is not scoped to an
// agent. Deleting the launch-service tokens from the arm's session env fails this.
func TestTheMacosUserSessionCarriesTheBridgesCallerTokenForAViaRoute(t *testing.T) {
	for _, cmd := range []string{"pi", "bash"} {
		t.Run(cmd, func(t *testing.T) {
			o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["pi", "bedrock", "wire-bridge"], `+
				`"profile": {"pi": "bedrock-bridge"}`+bedrockRegionMember+`}`, shellWith(nil))
			o.Args = []string{cmd}
			o.ProfileName = ""
			var started []*launchservice.Plan
			stopped := 0
			orig := startMacosUserService
			startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
				started = append(started, p)
				return fakeLaunched{&stopped}, "/log/launch-service-wire-bridge.log", nil
			}
			t.Cleanup(func() { startMacosUserService = orig })
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\n%s", rc, stderr.String())
			}
			if len(started) != 1 || started[0].Service != "wire-bridge" {
				t.Fatalf("started %v, want the wire bridge\n%s", started, stderr.String())
			}
			plan := started[0]
			if plan.TokenEnv != "YOLO_SERVICE_WIRE_BRIDGE_TOKEN" || plan.Token == "" {
				t.Fatalf("the bridge's plan has token %q in %q, want one in YOLO_SERVICE_WIRE_BRIDGE_TOKEN",
					plan.Token, plan.TokenEnv)
			}
			got, _ := seen.env.Get(plan.TokenEnv)
			if s, _ := got.(string); s != plan.Token {
				t.Errorf("the %s session's %s = %q, want the token the bridge's host half answers (%d chars), "+
					"or pi's via row sends none and is refused 401", cmd, plan.TokenEnv, s, len(plan.Token))
			}
		})
	}
}

// THE BRIDGE IS HANDED THE AWS DOORWAY'S POINTER FOR THE AGENT IT CARRIES (HS-D32): copilot on
// `-p bedrock`, aws-auth enabled, on macos-user. The launch opens the AWS doorway (the jail's
// condition, some agent's provider on Bedrock), and the bridge's input carries the pointer at it,
// its scoped caller token and the region the launch delivers copilot (here through env_sources, as
// the provider names none), which is what the bridge signs copilot's requests with and for, as a
// jail's bridge reads them from copilot's env file.
// Deleting the ServiceCredentialVars call in launchServiceInput fails this.
func TestTheMacosUserBridgeIsHandedTheAWSDoorwaysPointerForTheAgentItCarries(t *testing.T) {
	installFakeAWSCLI(t)
	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["copilot", "bedrock", "wire-bridge"], `+
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "yolo-unit", "unnarrowed": true}}}, `+
		`"env_sources": [{"AWS_REGION": "us-test-2"}]}`, shellWith(nil))
	o.Args = []string{"copilot"}
	o.ProfileName = "bedrock"
	doors := observeDoorways(t)
	var inputs []map[string]string
	stopped := 0
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		inputs = append(inputs, env)
		return fakeLaunched{&stopped}, "/log/launch-service-wire-bridge.log", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	door, _ := doors.only(t, "aws-auth")
	if len(inputs) != 1 {
		t.Fatalf("started %d services, want the wire bridge\n%s", len(inputs), stderr.String())
	}
	in := inputs[0]
	if got, want := in["AWS_CONTAINER_CREDENTIALS_FULL_URI"], "http://"+door.Addresses()[0]+"/credentials"; got != want {
		t.Errorf("the bridge's input carries the pointer %q, want the doorway's %q", got, want)
	}
	if in["AWS_CONTAINER_AUTHORIZATION_TOKEN"] != door.Token {
		t.Errorf("the bridge's input does not carry the doorway's caller token")
	}
	if in["AWS_REGION"] != "us-test-2" {
		t.Errorf("the bridge's input carries AWS_REGION=%q, want the one the launch delivers copilot", in["AWS_REGION"])
	}
}

// THE VIA GATE ASKS OF A VIA THE LAUNCH SERVES (WG-I13 on macos-user, HS-D30): pi on bedrock-bridge
// with a provider region the bridge composes no runtime host from leaves pi's via route unserved,
// so the macos-user launch refuses, naming the profile, the agent and the region, before the bridge
// starts, as a container launch refuses it (TestACarriedAgentWithNoRouteIsRefused's gate). stagePacks'
// via pre-flight runs before the channel plans the bridge and sees the via cleared, so the channel
// asks again; deleting its checkServedViaRoutes call fails this.
func TestMacosUserRefusesAViaTheBridgeServesNoRouteFor(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["pi", "bedrock", "wire-bridge"], `+
		`"providers": {"bedrock": {"region": "us-central1"}}}`, shellWith(nil))
	o.Args = []string{"pi"}
	o.ProfileName = "bedrock-bridge"
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		t.Errorf("a refused launch started %q", p.Service)
		return fakeLaunched{new(int)}, "", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d, reached = %v: a via the bridge serves no route for must refuse\n%s",
			rc, seen.reached, stderr.String())
	}
	for _, want := range []string{`profile "bedrock-bridge" (active for pi)`, `its region "us-central1" is not an AWS region`} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal must name %q:\n%s", want, stderr.String())
		}
	}
}

// THE MIXED LAUNCH (HS-D24, HS-D30): claude on cerebras pairs through the bridge's adapter, and pi on
// bedrock-bridge rides its via, so ONE bridge serves both. The via trigger plans it first, for pi,
// so the adapter pairing plans nothing and launchServiceAgents names pi alone; the bridge still
// carries claude, whose ANTHROPIC_BASE_URL names the plan's moved 8214. So the start line names
// both routes the bridge serves (claude's moved 8214, pi's moved 8216) and nothing else, and the
// bridge's input carries claude's CEREBRAS_API_KEY, which it signs claude's requests with.
// Deleting serviceAgents from servicePointedAt, or from startMacosUserServices' input, fails this.
func TestTheMacosUserBridgeServingAViaAndAnAdapterPairingNamesBothAndCarriesBothCredentials(t *testing.T) {
	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras", "pi", "bedrock", "wire-bridge"], `+
		`"profile": {"claude": "cerebras", "pi": "bedrock-bridge"}`+bedrockRegionMember+
		inEnvSources(map[string]string{"CEREBRAS_API_KEY": "csk-test"})+`}`, shellWith(nil))
	o.Args = []string{"claude"}
	o.ProfileName = ""
	var started []*launchservice.Plan
	var inputs []map[string]string
	stopped := 0
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		started = append(started, p)
		inputs = append(inputs, env)
		return fakeLaunched{&stopped}, "/log/launch-service-wire-bridge.log", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if len(started) != 1 || started[0].Service != "wire-bridge" {
		t.Fatalf("started %v, want the wire bridge once\n%s", started, stderr.String())
	}
	plan := started[0]
	claude, pi, other := plan.Moved["127.0.0.1:8214"], plan.Moved["127.0.0.1:8216"], plan.Moved["127.0.0.1:8215"]
	if claude == "" || pi == "" || other == "" {
		t.Fatalf("the plan did not move the bridge's three addresses: %v", plan.Moved)
	}
	var line string
	for _, l := range strings.Split(stderr.String(), "\n") {
		if strings.Contains(l, `Started the "wire-bridge" service`) {
			line = l
		}
	}
	want := []string{claude, pi}
	if pi < claude {
		want = []string{pi, claude}
	}
	if !strings.Contains(line, " on "+strings.Join(want, ", ")+" for this launch") {
		t.Errorf("the start line must name claude's %s and pi's %s, and nothing else (not %s):\n%s",
			claude, pi, other, stderr.String())
	}
	if got := inputs[0]["CEREBRAS_API_KEY"]; got != "csk-test" {
		t.Errorf("the bridge's input carries CEREBRAS_API_KEY=%q, want claude's key, which it signs claude's requests with", got)
	}
}

// A VIA THAT RE-POINTS NOTHING STARTS NOTHING ON MACOS-USER (HS-D33): agy on
// bedrock-bridge has a via URL, but its config ignores it and names no address of the bridge, so
// the bridge would carry none of agy's requests. The launch starts no host process outside the
// sandbox for it and says the via has no effect on agy, where it used to start the bridge on three
// addresses nothing pointed at. Counting every agent ViaFor names (packload.ViaRoutedServices), or
// planning a service whose Agents is empty (planMacosUserViaServices), fails this.
func TestTheMacosUserArmStartsNoBridgeForAViaThatRePointsNothing(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["agy", "bedrock", "wire-bridge"]`+bedrockRegionMember+`}`,
		shellWith(nil))
	o.Args = []string{"agy"}
	o.ProfileName = "bedrock-bridge"
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		t.Errorf("the launch started %q for agy, whose config the via re-points nothing of", p.Service)
		return fakeLaunched{new(int)}, "", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("Run() = %d, reached = %v\n%s", rc, seen.reached, stderr.String())
	}
	if !strings.Contains(stderr.String(), `Warning: profile "bedrock-bridge" (active for agy): its via — agy's config does not `+
		`point it at the "wire-bridge" service, so the via has no effect on agy`) {
		t.Errorf("the launch must say the via has no effect on agy:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), `Started the "wire-bridge" service`) {
		t.Errorf("the launch started the bridge:\n%s", stderr.String())
	}
}
