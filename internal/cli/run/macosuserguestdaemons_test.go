package run

// macosuserguestdaemons_test.go pins the CREDENTIAL half of the macos-user launch's served jail
// daemons (macosuserguestdaemons.go, macosuserdoorways.go; OQ-DP8/OQ-DP9, then HS-D15's doorway
// rule): with the AWS credential adapter opened OUTSIDE the Seatbelt guest as this launch's own
// doorway, a `bedrock` launch is SERVED its pointer, at the port the doorway binds, and the
// adapter's SCOPED caller token reaches exactly two readers — the doorway's input and the bedrock
// agent's environment — never the shared one. Driven through Run(), so deleting the doorway
// plan, the served-set split or the settle fails it.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// ONLY WHAT THE LAUNCH SERVES IS GIVEN A PORT. On macos-user the payload still names the wire
// bridge's jail daemon (declined: its host half runs instead, with ports of its own from
// internal/launchservice), so settling served addresses over the whole payload would pick a port
// for the bridge's declared 8214 that nothing binds and record it in the channel. Deleting the
// ServedJailDaemons narrowing in jailDaemonsFor fails this.
func TestMacosUserPicksNoPortForADaemonItsGuestDeclines(t *testing.T) {
	packs := bridgedPacks(t)
	o, cfg, _, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), selectCerebras)
	o.runtime = "macos-user"
	o.launchServices = nil
	o.served = servedAddressState{}
	o.jailDaemonsFor(cfg, "macos-user", packs)
	for from := range o.served.moved {
		if from == "127.0.0.1:8214" || from == "127.0.0.1:8215" {
			t.Errorf("a port was picked for the declined wire bridge's %s: %v", from, o.served.moved)
		}
	}
}

func installFakeAWSCLI(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	fakeAWS := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'aws-cli/2.99.0 fake'; exit 0; fi\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWS), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestMacosUserServesTheBedrockPointerThroughALaunchOwnedDoorway(t *testing.T) {
	installFakeAWSCLI(t)
	o, stderr, seen := overrideNativeLaunch(t,
		awsAuthUserConfig(`, "loopholes": {"aws-auth": {"enabled": true,
		"settings": {"profile": "yolo-unit", "unnarrowed": true}}}`), shellWith(nil))
	doors := observeDoorways(t)
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if !seen.reached {
		t.Fatalf("the launch never reached the macos-user handler:\n%s", stderr.String())
	}
	for _, s := range payloadOf(t, seen.jailDaemons) {
		if s.Name == "aws-auth" {
			t.Errorf("the guest was handed the AWS adapter's jail daemon too: %v", s.Cmd)
		}
	}
	plan, in := doors.only(t, "aws-auth")
	listen := plan.Cmd[len(plan.Cmd)-1]
	if strings.Join(plan.Cmd[:len(plan.Cmd)-1], " ") != "yolo internal daemon aws-credential-adapter --listen" {
		t.Errorf("the doorway's argv is not the manifest's host_cmd: %v", plan.Cmd)
	}
	if !strings.HasPrefix(listen, "127.0.0.1:") || listen == "127.0.0.1:1461" || plan.Addresses()[0] != listen {
		t.Fatalf("the doorway is not at a picked port (argv %v, addresses %v)", plan.Cmd, plan.Addresses())
	}
	uri, _ := seen.env.Get(pointerVar)
	if uri != "http://"+listen+"/credentials" {
		t.Errorf("the bedrock agent's %s = %v, want the doorway's http://%s/credentials", pointerVar, uri, listen)
	}
	agentTok, _ := seen.env.Get("AWS_CONTAINER_AUTHORIZATION_TOKEN")
	if !svcendpoint.IsToken(plan.Token) || agentTok != plan.Token ||
		plan.TokenEnv != "YOLO_SERVICE_AWS_AUTH_TOKEN" {
		t.Errorf("the doorway's caller token (%s=%q) is not the one the agent presents (%v)",
			plan.TokenEnv, plan.Token, agentTok)
	}
	if _, ok := in["YOLO_SERVICE_AWS_AUTH_TOKEN"]; ok {
		t.Errorf("the token rode the doorway's input map rather than launchservice.Start's own write")
	}
	if v, ok := seen.env.Get("YOLO_SERVICE_AWS_AUTH_TOKEN"); ok {
		t.Errorf("the SCOPED aws-auth token is exported by name in the agent's shared env: %v", v)
	}
	if strings.Contains(stderr.String(), pointerVar+" — points at") {
		t.Errorf("the launch still names the served pointer as withheld:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "aws-auth: yolo-jaild aws-credential-adapter --listen "+listen+
		" — its doorway opens outside the sandbox") {
		t.Errorf("the launch does not decline the AWS adapter's jail daemon with the doorway's reason:\n%s",
			stderr.String())
	}
	// The doorway is host code of a pack yolo ships, so the launch's host-execution disclosure
	// names it: the staged pack keeps its official mark, which packload's claim reads.
	if !strings.Contains(stderr.String(), "and yolo internal daemon aws-credential-adapter --listen '{listen}' on your machine") {
		t.Errorf("the launch does not disclose the AWS doorway's host argv as running on your machine:\n%s",
			stderr.String())
	}
}

// THE AWS DOORWAY IS HANDED THE ENDPOINT IT FORWARDS TO. It asks the host aws-auth service
// through the front the launch published for this session, named by YOLO_SERVICE_AWS_AUTH_ENDPOINT
// (awscredadapter.EndpointEnv), so its input must carry the path the launch's own host service
// published. Without it the doorway still binds and answers every request ServiceUnreachable,
// while the launch prints that it opened. Here the aws-auth host service really starts: the test
// binary serves `internal daemon aws-auth`, a stand-in `aws` on PATH answers its `--version`
// spawn check, and the profile is configured un-narrowed so the daemon does not refuse. Deleting
// the endpoint files from doorwayInput fails this.
func TestMacosUserHandsTheAWSDoorwayTheEndpointItForwardsTo(t *testing.T) {
	bin := t.TempDir()
	fakeAWS := "#!/bin/sh\ncase \"$1\" in --version) echo 'aws-cli/2.99.0 fake'; exit 0 ;; esac\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWS), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	o, stderr, seen := overrideNativeLaunch(t, awsAuthUserConfig(`, "loopholes": {"aws-auth": {"enabled": true,
		"settings": {"profile": "yolo-unit", "unnarrowed": true}}}`), shellWith(nil))
	// The host service is a host-wide singleton, and this package's later launches would adopt
	// it: stopped here, before the temp HOME holding its state goes.
	t.Cleanup(func() {
		broker.BrokerKill(broker.SingletonDeps(awscredadapter.LoopholeName, nil), syscall.SIGTERM, 2*time.Second)
		_ = os.Remove(paths.HostSingletonLock(awscredadapter.LoopholeName))
	})
	doors := observeDoorways(t)
	published := false
	run := o.MacosUserRun
	o.MacosUserRun = func(cfg *jsonx.OrderedMap, ws string, a, b []string, c, d string, h macosuser.HomeOverlay,
		ctx macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
		if v, ok := env.Get(awscredadapter.EndpointEnv); ok {
			if p, _ := v.(string); p != "" {
				_, err := os.Stat(p)
				published = err == nil
			}
		}
		return run(cfg, ws, a, b, c, d, h, ctx, dry, env, bt, jd)
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	endpoint, _ := seen.env.Get(awscredadapter.EndpointEnv)
	if !published {
		t.Fatalf("the aws-auth host service published no endpoint while the command ran (%s=%v), so "+
			"this test has lost its premise:\n%s", awscredadapter.EndpointEnv, endpoint, stderr.String())
	}
	_, in := doors.only(t, "aws-auth")
	if got := in[awscredadapter.EndpointEnv]; got == "" || got != endpoint {
		t.Errorf("the AWS doorway's input carries %s=%q, want the endpoint the launch published (%v)",
			awscredadapter.EndpointEnv, got, endpoint)
	}
}

// WITHOUT `bedrock` NO AWS DOORWAY OPENS: the adapter starts only when some agent's profile
// selects the profile it serves (OQ-CN7 (b)), so a launch on no profile opens only the OpenAI
// refresh doorway.
func TestMacosUserOpensNoAWSDoorwayWithoutBedrock(t *testing.T) {
	installFakeAWSCLI(t)
	o, stderr, _ := overrideNativeLaunch(t,
		awsAuthUserConfig(`, "loopholes": {"aws-auth": {"enabled": true,
		"settings": {"profile": "yolo-unit", "unnarrowed": true}}}`), shellWith(nil))
	t.Cleanup(func() {
		broker.BrokerKill(broker.SingletonDeps(awscredadapter.LoopholeName, nil), syscall.SIGTERM, 2*time.Second)
		_ = os.Remove(paths.HostSingletonLock(awscredadapter.LoopholeName))
	})
	o.ProfileName = ""
	doors := observeDoorways(t)
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	for _, p := range doors.plans {
		if p.Service == "aws-auth" {
			t.Errorf("the AWS doorway opened with no agent on bedrock:\n%s", stderr.String())
		}
	}
}
