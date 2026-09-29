package run

// macosuserguestdaemons_test.go pins the CREDENTIAL half of the macos-user guest's jail daemons
// (macosuserguestdaemons.go; OQ-DP8/OQ-DP9): with the AWS credential adapter running in the
// Seatbelt guest, a `bedrock` launch is SERVED its pointer, at the port the adapter binds, and
// the adapter's SCOPED caller token reaches exactly two readers — the supervisor's own env file
// and the bedrock agent's environment — never the shared one. Driven through Run(), so deleting
// the guestJailDaemons argument, the served-set split or the settle fails it.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// ONLY WHAT THE GUEST RUNS IS GIVEN A PORT. On macos-user the payload still names the wire
// bridge's jail daemon (declined: its host half runs instead, with ports of its own from
// internal/launchservice), so settling served addresses over the whole payload would pick a port
// for the bridge's declared 8214 that nothing binds and record it in the channel. Deleting the
// JailDaemonsRunIn narrowing in jailDaemonsFor fails this.
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

func TestMacosUserServesTheBedrockPointerThroughTheGuestAdapter(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t,
		awsAuthUserConfig(`, "loopholes": {"aws-auth": {"enabled": true}}`), shellWith(nil))
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if !seen.reached {
		t.Fatalf("the launch never reached the macos-user handler:\n%s", stderr.String())
	}
	var listen string
	for _, s := range payloadOf(t, seen.jailDaemons) {
		if s.Name == "aws-auth" {
			if len(s.Cmd) != 4 || s.Cmd[0] != "yolo-jaild" || s.Cmd[1] != "aws-credential-adapter" {
				t.Errorf("the adapter's argv is not the declared one: %v", s.Cmd)
			}
			listen = s.Cmd[len(s.Cmd)-1]
		}
	}
	if listen == "" || listen == "127.0.0.1:1461" {
		t.Fatalf("the guest was not handed the AWS adapter at a picked port (listen %q); payload %+v\n%s",
			listen, payloadOf(t, seen.jailDaemons), stderr.String())
	}
	uri, _ := seen.env.Get(pointerVar)
	if uri != "http://"+listen+"/credentials" {
		t.Errorf("the bedrock agent's %s = %v, want the adapter's http://%s/credentials", pointerVar, uri, listen)
	}
	agentTok, _ := seen.env.Get("AWS_CONTAINER_AUTHORIZATION_TOKEN")
	daemonTok, _ := seen.jailDaemons.Env.Get("YOLO_SERVICE_AWS_AUTH_TOKEN")
	if s, _ := daemonTok.(string); !svcendpoint.IsToken(s) || agentTok != daemonTok {
		t.Errorf("the adapter's caller token (%v) is not the one the agent presents (%v)", daemonTok, agentTok)
	}
	if v, ok := seen.env.Get("YOLO_SERVICE_AWS_AUTH_TOKEN"); ok {
		t.Errorf("the SCOPED aws-auth token is exported by name in the agent's shared env: %v", v)
	}
	if strings.Contains(stderr.String(), pointerVar+" — points at") {
		t.Errorf("the launch still names the served pointer as withheld:\n%s", stderr.String())
	}
}
