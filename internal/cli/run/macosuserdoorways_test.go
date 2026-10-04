package run

// macosuserdoorways_test.go pins the macos-user launch's LAUNCH-OWNED DOORWAYS
// (macosuserdoorways.go; docs/design/host-notch-services.md HS-D15, the doorway rule): the Codex
// refresh adapter and the AWS credential adapter open outside the Seatbelt sandbox as this
// launch's own listeners, at the ports and behind the caller tokens the channel composed their
// clients with, and stop when the sandboxed command exits.
//
// DRIVEN THROUGH Run(), with the start observed through its seam and the sandbox through
// MacosUserRun's, so deleting the plan, the start, the stop or the dry-run line from the
// macos-user arm fails a test here. One test lets the start run for real: the test binary
// stands in for `yolo`, and TestMain dispatches `internal daemon openai-auth-adapter` to the
// doorway, so a real listener answers at the composed address.
//
// ⚠ NONE OF THIS HAS RUN ON A MAC. What only a Mac settles is that a doorway the launch opens
// outside Seatbelt is reachable from inside the sandbox on the Mac's loopback, and that a real
// Codex and a real AWS SDK in the sandbox refresh through it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// doorwaysSeen is what the stubbed doorway start observed.
type doorwaysSeen struct {
	plans   []*launchservice.Plan
	inputs  []map[string]string
	stopped int
}

// only is the one doorway named service that started, and its input; fatal when it is not
// exactly one.
func (d *doorwaysSeen) only(t *testing.T, service string) (*launchservice.Plan, map[string]string) {
	t.Helper()
	var plan *launchservice.Plan
	var in map[string]string
	for i, p := range d.plans {
		if p.Service == service {
			if plan != nil {
				t.Fatalf("the %q doorway started twice", service)
			}
			plan, in = p, d.inputs[i]
		}
	}
	if plan == nil {
		var names []string
		for _, p := range d.plans {
			names = append(names, p.Service)
		}
		t.Fatalf("no %q doorway started (started: %v)", service, names)
	}
	return plan, in
}

// observeDoorways stubs the doorway start for the test, recording each plan and input.
func observeDoorways(t *testing.T) *doorwaysSeen {
	t.Helper()
	seen := &doorwaysSeen{}
	orig := startMacosUserDoorway
	startMacosUserDoorway = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		seen.plans = append(seen.plans, p)
		seen.inputs = append(seen.inputs, env)
		return fakeLaunched{&seen.stopped}, "/log/launch-service-" + p.Service + ".log", nil
	}
	t.Cleanup(func() { startMacosUserDoorway = orig })
	return seen
}

// codexNativeLaunch is a macos-user launch of codex with no profile: the codex pack's refresh
// pointer is ungated, so every codex launch is pointed at the refresh doorway.
func codexNativeLaunch(t *testing.T) (*Options, *bytes.Buffer, *nativeLaunch) {
	t.Helper()
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["codex"]}`, shellWith(nil))
	o.Args = []string{"codex"}
	o.ProfileName = ""
	return o, stderr, seen
}

// THE ARM OPENS THE CODEX DOORWAY BEFORE THE COMMAND AND STOPS IT AFTER, and the command is
// pointed at it: CODEX_REFRESH_TOKEN_URL_OVERRIDE names the doorway's address, and the token the
// Codex launcher binds into the refresh marker is the doorway's. Deleting the
// startMacosUserDoorways call, or its deferred stop, from run.go fails this.
func TestTheMacosUserArmOpensTheCodexDoorwayAndStopsItAfterTheCommand(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	doors := observeDoorways(t)
	stoppedBeforeRun := -1
	run := o.MacosUserRun
	o.MacosUserRun = func(cfg *jsonx.OrderedMap, ws string, a, b []string, c, d string, h macosuser.HomeOverlay,
		ctx macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
		stoppedBeforeRun = doors.stopped
		return run(cfg, ws, a, b, c, d, h, ctx, dry, env, bt, jd)
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	plan, _ := doors.only(t, "openai-auth-broker")
	if stoppedBeforeRun != 0 || doors.stopped != 1 {
		t.Errorf("the doorway was stopped %d times before the command ran and %d after; want 0 and 1",
			stoppedBeforeRun, doors.stopped)
	}
	url, _ := seen.env.Get("CODEX_REFRESH_TOKEN_URL_OVERRIDE")
	if url != "http://"+plan.Addresses()[0]+"/oauth/token" {
		t.Errorf("codex's CODEX_REFRESH_TOKEN_URL_OVERRIDE = %v, want the doorway's http://%s/oauth/token",
			url, plan.Addresses()[0])
	}
	if tok, _ := seen.env.Get(openauthclient.CallerTokenEnv); tok != plan.Token {
		t.Errorf("the token the Codex launcher binds (%v) is not the doorway's", tok)
	}
	if plan.Pack != "openai-auth" || plan.TokenEnv != openauthclient.CallerTokenEnv {
		t.Errorf("the doorway's plan names pack %q and token %q", plan.Pack, plan.TokenEnv)
	}
}

// A DRY RUN SAYS WHAT IT WOULD OPEN, and opens nothing.
func TestMacosUserDryRunSaysWhichDoorwaysItWouldOpen(t *testing.T) {
	o, stderr, _ := codexNativeLaunch(t)
	o.DryRun = true
	doors := observeDoorways(t)
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d\n%s", rc, stderr.String())
	}
	if len(doors.plans) != 0 {
		t.Errorf("a dry run opened %d doorways", len(doors.plans))
	}
	want := `Would open the "openai-auth-broker" doorway (pack "openai-auth") on [127.0.0.1:`
	if !strings.Contains(stderr.String(), want) ||
		!strings.Contains(stderr.String(), "yolo internal daemon openai-auth-adapter --listen 127.0.0.1:") {
		t.Errorf("the dry run does not say %q with the argv:\n%s", want, stderr.String())
	}
}

// A DOORWAY THAT CANNOT START REFUSES THE LAUNCH before the command runs, naming it.
func TestTheMacosUserArmRefusesWhenADoorwayCannotStart(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	orig := startMacosUserDoorway
	startMacosUserDoorway = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		return nil, "", errors.New(`the "openai-auth-broker" service (pack "openai-auth") did not start: boom`)
	}
	t.Cleanup(func() { startMacosUserDoorway = orig })
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d, reached = %v: a doorway that cannot start must refuse before the command\n%s",
			rc, seen.reached, stderr.String())
	}
	if !strings.Contains(stderr.String(), `Refusing the macos-user launch: the "openai-auth-broker" service`) {
		t.Errorf("the refusal must name the doorway:\n%s", stderr.String())
	}
}

// A LATER DOORWAY THAT CANNOT START STOPS THE ONES ALREADY OPEN: the refusal returns before the
// arm defers any stop, so startMacosUserDoorways must stop what it started itself, or the first
// doorway keeps answering on the Mac's loopback until its lifeline closes. A bedrock launch
// opens two, the Codex refresh doorway and the AWS one; the first start succeeds and the second
// fails. Deleting the stop() in startMacosUserDoorways' error branch fails this.
func TestAMacosUserDoorwayThatCannotStartStopsTheOnesAlreadyOpen(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t,
		awsAuthUserConfig(`, "loopholes": {"aws-auth": {"enabled": true}}`), shellWith(nil))
	stopped, starts := 0, 0
	var first string
	orig := startMacosUserDoorway
	startMacosUserDoorway = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		starts++
		if starts == 1 {
			first = p.Service
			return fakeLaunched{&stopped}, "/log/launch-service-" + p.Service + ".log", nil
		}
		return nil, "", errors.New(`the "` + p.Service + `" service did not start: boom`)
	}
	t.Cleanup(func() { startMacosUserDoorway = orig })
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d, reached = %v: a doorway that cannot start must refuse before the command\n%s",
			rc, seen.reached, stderr.String())
	}
	if starts != 2 {
		t.Fatalf("the launch tried %d doorway starts, want 2 (the Codex and the AWS doorway): the "+
			"premise is gone\n%s", starts, stderr.String())
	}
	if stopped != 1 {
		t.Errorf("the %q doorway that started was stopped %d times when the next one failed, want 1",
			first, stopped)
	}
}

// A PACK YOLO DOES NOT SHIP CANNOT RUN HOST CODE THROUGH host_cmd (the launch-owned mechanism's
// admission rule): its doorway runs as the jail daemon it also is, in the guest, and the launch
// says why. Deleting admitDoorways from jailDaemonsFor fails this.
func TestMacosUserRunsARefusedDoorwayInTheGuest(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1999", "caller_token": true,
		"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	for _, p := range doors.plans {
		if p.Service == "acme-proxy" {
			t.Errorf("a local pack's host argv was run outside the sandbox: %v", p.Cmd)
		}
	}
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || specs[0].Name != "acme-proxy" || specs[0].Cmd[0] != "yolo-jaild" {
		t.Errorf("the refused doorway's jail daemon was not handed to the guest: %+v", specs)
	}
	if !strings.Contains(got.out, `Not opened outside the sandbox: the "acme-proxy" doorway's host argv`) ||
		!strings.Contains(got.out, "its pack is not one yolo ships") {
		t.Errorf("the launch does not say why the doorway runs in the guest:\n%s", got.out)
	}
	// And the host-execution disclosure does not name the argv it refused: that is code that runs
	// nowhere (packload's moduleClaims claims host_cmd only for a pack yolo ships).
	if strings.Contains(got.out, "RUNS yolo internal daemon acme-adapter") {
		t.Errorf("the launch discloses a refused host argv as running on your machine:\n%s", got.out)
	}
}

// A REFUSED DOORWAY WHOSE JAIL DAEMON THE GUEST DECLINES TOO IS SAID TO RUN NOWHERE, not in the
// sandbox: this one's program is a Linux executable the pack ships in its module directory, which
// the guest declines by name, so the refusal line must follow the daemon to where it actually
// goes. Before this, one launch printed that daemon's Declined: line and a line saying it ran in
// the sandbox instead, while the guest was handed nothing. Deleting the decline lookup from
// noteRefusedDoorways fails this.
func TestMacosUserSaysARefusedDoorwayTheGuestDeclinesRunsNowhere(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["{jail_loophole_dir}/my-agent", "--listen", "{listen}"],
		"listen": "127.0.0.1:1999", "caller_token": true,
		"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`)
	writeLocalModuleFile(t, home, "acme-proxy", "my-agent", linuxProgram)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if len(doors.plans) != 0 {
		t.Errorf("a local pack's host argv was run outside the sandbox: %v", doors.plans[0].Cmd)
	}
	if specs := payloadOf(t, got.jailDaemons); len(specs) != 0 {
		t.Fatalf("the guest was handed %+v, want nothing: its program is a Linux executable", specs)
	}
	var refusal string
	for _, line := range strings.Split(got.out, "\n") {
		if strings.Contains(line, `Not opened outside the sandbox: the "acme-proxy" doorway's host argv`) {
			refusal = line
		}
	}
	if refusal == "" {
		t.Fatalf("the launch does not say it refused the doorway's host argv:\n%s", got.out)
	}
	if strings.Contains(refusal, "runs in the sandbox instead") {
		t.Errorf("the refusal says the jail daemon runs in the sandbox, which declined it:\n%s", got.out)
	}
	guestProgram := filepath.Join(macosuser.StagedPackRoot(runtime.FromWorkspace(ws), ""), "local",
		"loopholes", "acme-proxy", "my-agent")
	if !strings.Contains(refusal, "declined in the sandbox too") ||
		!strings.Contains(got.out, "acme-proxy: "+guestProgram) || !strings.Contains(got.out, "a Linux executable") {
		t.Errorf("the refusal does not point at the jail daemon's own Declined: line:\n%s", got.out)
	}
	// A daemon the user selected runs nowhere, so the line names the next step: a container
	// backend runs it (docs/reference/happy-path-principle.md).
	if !strings.Contains(refusal, "nothing serves it this launch; a container runtime runs it "+
		"(`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container` for one launch)") {
		t.Errorf("the refusal says the doorway runs nowhere and names no next step:\n%s", refusal)
	}
}

// A PACK THAT DECLARES THE ADAPTER WITHOUT A HOST ARGV STILL RUNS IT IN THE GUEST: the doorway
// rule moves only a doorway whose manifest declares `host_cmd`. This is the premise of the Mac
// integration test that keeps the guest's supervisor measured (integration/'s
// TestMacosUserJailDaemonRunsConfinedInTheGuest, whose local pack is this manifest).
func TestALocalAdapterWithoutAHostArgvRunsInTheGuest(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "openai-auth-broker", `{"name": "openai-auth-broker",
		"description": "the adapter as a guest jail daemon", "version": 1, "default_enabled": true,
		"transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "openai-auth-broker",
		  "--socket", "{socket}", "--state-file", "{state}/credentials.json"],
		  "publishes": "socket", "scope": "host"},
		"jail_daemon": {"cmd": ["yolo-jaild", "openai-auth-adapter", "--listen", "{listen}"],
		  "listen": "127.0.0.1:1460", "restart": "on-failure", "caller_token": true},
		"state_files": [".mount-sentinel"]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if len(doors.plans) != 0 {
		t.Errorf("a doorway opened for an adapter that declares no host argv: %v", doors.plans[0].Cmd)
	}
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || specs[0].Name != "openai-auth-broker" || specs[0].Cmd[1] != "openai-auth-adapter" {
		t.Errorf("the guest was not handed the adapter: %+v\n%s", specs, got.out)
	}
}

// THE REAL DOORWAY, end to end on this machine's loopback: the start is not stubbed, so the
// test binary runs `internal daemon openai-auth-adapter --listen <picked>` as the launch's
// child, and while the command runs, the address codex was pointed at answers. A refresh
// without the launch's caller token is refused 401 before any broker is asked; one carrying the
// token bound into its marker, as the Codex launcher writes it, gets past the check. After the
// launch returns, nothing answers there.
func TestTheRealCodexDoorwayAnswersOnlyTheLaunchsCallerToken(t *testing.T) {
	o, stderr, _ := codexNativeLaunch(t)
	var url, token string
	var without, with int
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		u, _ := env.Get("CODEX_REFRESH_TOKEN_URL_OVERRIDE")
		tk, _ := env.Get(openauthclient.CallerTokenEnv)
		url, _ = u.(string)
		token, _ = tk.(string)
		without = postRefresh(t, url, "yolo-broker:1")
		with = postRefresh(t, url, openauthclient.BindCallerToken("yolo-broker:1", token))
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), `Opened the "openai-auth-broker" doorway (pack "openai-auth"`) {
		t.Fatalf("the launch did not open the doorway:\n%s", stderr.String())
	}
	if without != http.StatusUnauthorized {
		t.Errorf("a refresh without the caller token got %d from %s, want 401", without, url)
	}
	if with == http.StatusUnauthorized || with == 0 {
		t.Errorf("a refresh carrying the launch's caller token got %d from %s, want past the check", with, url)
	}
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Post(url, "application/json", strings.NewReader("{}")); err == nil {
		resp.Body.Close()
		t.Errorf("the doorway still answers at %s after the launch returned", url)
	}
}

// THE PORT THE LAUNCH PICKED FOR A DOORWAY IS STILL THE DOORWAY'S WHEN ANOTHER LISTENER ASKS FOR IT
// FIRST. The pick used to bind port 0 and let the port go at once, and this launch binds other
// listeners before it opens its doorways: once the claude broker's front was handed the Codex
// doorway's port, the doorway's own bind failed with "address already in use", and the launch was
// refused (docs/plans/test-suite-speed.md). Here, just before the real start, another listener asks
// for the exact port the doorway was planned on, which makes that race deterministic. It must be
// refused, and the doorway must still open on the address codex was pointed at. Driven through
// Run's macos-user arm, so the pick (servedaddresses.go), the plan (planMacosUserDoorways) and the
// start are all production's.
func TestAMacosUserDoorwaysPickedPortIsStillItsOwnWhenAnotherListenerAsksFirst(t *testing.T) {
	o, stderr, _ := codexNativeLaunch(t)
	orig := startMacosUserDoorway
	var taken []string
	startMacosUserDoorway = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		for _, addr := range p.Addresses() {
			if other, err := net.Listen("tcp", addr); err == nil {
				t.Cleanup(func() { _ = other.Close() })
				taken = append(taken, addr)
			}
		}
		return orig(p, env)
	}
	t.Cleanup(func() { startMacosUserDoorway = orig })
	var url string
	answered := 0
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		u, _ := env.Get("CODEX_REFRESH_TOKEN_URL_OVERRIDE")
		url, _ = u.(string)
		answered = postRefresh(t, url, "yolo-broker:1")
		return 0
	}
	rc := Run(*o)
	if len(taken) > 0 {
		t.Errorf("another listener bound %v, the port this launch picked for its doorway, before the "+
			"doorway opened: the pick let the port go", taken)
	}
	if rc != 0 {
		t.Fatalf("Run() = %d: the doorway lost the port codex was pointed at\n%s", rc, stderr.String())
	}
	// 401 is the doorway's own answer to a refresh without the caller token: the listener at the
	// address codex was pointed at is the doorway.
	if answered != http.StatusUnauthorized {
		t.Errorf("a refresh at %s got %d, want the doorway's 401", url, answered)
	}
}

// postRefresh POSTs Codex's refresh request shape with refreshToken to url, returning the status
// (0 when nothing answered).
func postRefresh(t *testing.T, url, refreshToken string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken})
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Logf("POST %s: %v", url, err)
		return 0
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	t.Logf("POST %s → %d %s", url, resp.StatusCode, answer)
	return resp.StatusCode
}
