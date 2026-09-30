package run

// macosuserservices_test.go pins the macos-user launch's LAUNCH-OWNED SERVICES
// (macosuserservices.go; docs/design/host-notch-services.md §4.7, OQ-NC1 ruled A). That backend's
// guest declines a pack service's jail daemon, so claude on a bridged profile there is composed
// against the wire bridge's
// host half, which the launch starts as its child and stops when the sandboxed command exits.
// Unit-level: the start is observed through its seam and the sandbox through MacosUserRun's, so
// no service and no agent runs.

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// fakeLaunched is a started service the test can see stopped.
type fakeLaunched struct{ stopped *int }

func (f fakeLaunched) Stop()    { *f.stopped++ }
func (f fakeLaunched) PID() int { return 4242 }

// The channel composes claude on cerebras against the bridge's host half on a port this launch
// picked, with the launch's caller token as claude's credential, where it used to refuse: macos-user
// now serves the service it plans (servedDaemons).
func TestMacosUserComposesABridgedPairingAgainstALaunchOwnedService(t *testing.T) {
	packs := bridgedPacks(t)
	o, cfg, _, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), selectCerebras)
	o.runtime = "macos-user"
	o.launchServices = nil
	channel, err := o.composePackChannel(cfg, packs, cerebrasKey())
	if err != nil {
		t.Fatalf("macos-user refused claude on cerebras: %v", err)
	}
	if len(o.launchServices) != 1 || o.launchServices[0].Service != "wire-bridge" {
		t.Fatalf("launch services = %+v, want the wire bridge", o.launchServices)
	}
	plan := o.launchServices[0]
	picked := plan.Moved["127.0.0.1:8214"]
	if picked == "" || picked == "127.0.0.1:8214" {
		t.Fatalf("the bridge's 8214 was not moved to a picked port: %v", plan.Moved)
	}
	env := map[string]string{}
	for _, v := range channel.scope.Agent("claude").Shape {
		env[v.Key] = v.Value
	}
	if env["ANTHROPIC_BASE_URL"] != "http://"+picked {
		t.Errorf("claude's ANTHROPIC_BASE_URL = %q, want the picked http://%s", env["ANTHROPIC_BASE_URL"], picked)
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != plan.Token {
		t.Errorf("claude's ANTHROPIC_AUTH_TOKEN is not the launch's caller token")
	}
	in := channel.launchServiceInput(o.launchServiceAgents["wire-bridge"])
	if in["CEREBRAS_API_KEY"] != "csk-test" || !strings.Contains(in["YOLO_PROVIDERS"], picked) {
		t.Errorf("the service's input lacks the provider key or the picked address: %v", in)
	}
}

// The arm starts the planned service before the sandboxed command, hands the command the address
// and token, says so on stderr, and stops the service once the command returns. Deleting the
// startMacosUserServices call in run.go fails this.
func TestTheMacosUserArmStartsTheServiceAndStopsItAfterTheCommand(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "csk-test"}]}`, shellWith(nil))
	o.ProfileName = "cerebras"
	var started []*launchservice.Plan
	stopped := 0
	stoppedBeforeRun := -1
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		started = append(started, p)
		if env["CEREBRAS_API_KEY"] != "csk-test" {
			t.Errorf("the service's input lacks the provider's key: %v", env)
		}
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
		t.Fatalf("started %v, want the wire bridge", started)
	}
	if stoppedBeforeRun != 0 || stopped != 1 {
		t.Errorf("stopped %d times before the command ran and %d after; want 0 and 1", stoppedBeforeRun, stopped)
	}
	base, _ := seen.env.Get("ANTHROPIC_BASE_URL")
	u, _ := url.Parse(base.(string))
	if u == nil || u.Host != started[0].Moved["127.0.0.1:8214"] {
		t.Errorf("the command's ANTHROPIC_BASE_URL = %v, want the picked %s", base, started[0].Moved["127.0.0.1:8214"])
	}
	if tok, _ := seen.env.Get("ANTHROPIC_AUTH_TOKEN"); tok != started[0].Token {
		t.Errorf("the command's ANTHROPIC_AUTH_TOKEN is not the service's token")
	}
	for _, want := range []string{`Started the "wire-bridge" service (pack "wire-bridge", pid 4242)`,
		"wire-bridge: yolo-jaild wire-bridge — a pack service runs its host half on this backend",
		"(its host half runs for this launch)"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch must say %q:\n%s", want, stderr.String())
		}
	}
	// ONLY THE ROUTE THE COMMAND WAS POINTED AT (HS-D24): the plan moved 8215 too, and the channel's
	// provider table names its picked port under openai-codex, a route claude on cerebras never takes.
	assertNamesOnlyTheRoute(t, stderr.String(), `Started the "wire-bridge" service`, started[0])
}

// assertNamesOnlyTheRoute checks that the line of out starting with lead names plan's picked
// 8214 (claude's route on cerebras) and not its picked 8215 (codex's, which it was not pointed at).
func assertNamesOnlyTheRoute(t *testing.T, out, lead string, plan *launchservice.Plan) {
	t.Helper()
	i := strings.Index(out, lead)
	if i < 0 {
		t.Fatalf("no line starts with %q:\n%s", lead, out)
	}
	line, _, _ := strings.Cut(out[i:], "\n")
	if used := plan.Moved["127.0.0.1:8214"]; used == "" || !strings.Contains(line, used) {
		t.Errorf("the line does not name %q, the route the command was pointed at:\n%s", used, line)
	}
	if unused := plan.Moved["127.0.0.1:8215"]; unused == "" || strings.Contains(line, unused) {
		t.Errorf("the line names %q, a route the command was not pointed at:\n%s", unused, line)
	}
}

// A DRY RUN NAMES THE SAME ROUTE THE START WOULD (HS-D24): the plan render starts nothing, and its
// "Would start" line names the one address the command was pointed at, not every address the plan
// moved (8214's and 8215's picked ports).
func TestTheMacosUserDryRunNamesOnlyTheRouteItsCommandIsPointedAt(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "csk-test"}]}`, shellWith(nil))
	o.ProfileName = "cerebras"
	o.DryRun = true
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		t.Errorf("a dry run started %q", p.Service)
		return nil, "", errors.New("no")
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	base, _ := seen.env.Get("ANTHROPIC_BASE_URL")
	s, _ := base.(string)
	u, err := url.Parse(s)
	if err != nil || u.Port() == "" || u.Port() == "8214" {
		t.Fatalf("the command's ANTHROPIC_BASE_URL = %v, want a picked port", base)
	}
	lead := `Would start the "wire-bridge" service (pack "wire-bridge") on [`
	i := strings.Index(stderr.String(), lead)
	if i < 0 {
		t.Fatalf("the dry run must say what it would start:\n%s", stderr.String())
	}
	named, _, _ := strings.Cut(stderr.String()[i+len(lead):], "]")
	if named != u.Host {
		t.Errorf("the dry run names [%s], want only [%s], the route the command was pointed at", named, u.Host)
	}
}

// NOTHING STARTS WHEN NO PROFILE NEEDS A SERVICE: claude with no profile on macos-user plans none.
func TestTheMacosUserArmStartsNoServiceWithoutABridgedProfile(t *testing.T) {
	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["claude"]}`, shellWith(nil))
	o.ProfileName = ""
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		t.Errorf("started %q with no bridged profile selected", p.Service)
		return nil, "", errors.New("no")
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if strings.Contains(stderr.String(), "Started the") {
		t.Errorf("a launch with no bridged profile said it started a service:\n%s", stderr.String())
	}
}

// A service that cannot start refuses the macos-user launch before the command runs.
func TestTheMacosUserArmRefusesWhenTheServiceCannotStart(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "csk-test"}]}`, shellWith(nil))
	o.ProfileName = "cerebras"
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		return nil, "", errors.New(`the "wire-bridge" service (pack "wire-bridge") did not start: boom`)
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d, reached = %v: a service that cannot start must refuse before the command\n%s",
			rc, seen.reached, stderr.String())
	}
	if !strings.Contains(stderr.String(), `the "wire-bridge" service (pack "wire-bridge") did not start`) {
		t.Errorf("the refusal must name the service:\n%s", stderr.String())
	}
}
