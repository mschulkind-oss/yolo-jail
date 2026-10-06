package integration

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestMacosUserBridgesPiThroughTheHostHalfViaRoute is HS-D30 (docs/design/host-notch-services.md;
// wire-bridge-gateway.md WG-I46) on the hardware. pi on the shipped bedrock-bridge routes through
// the wire bridge's VIA address, and on macos-user, whose guest declines the bridge's jail daemon,
// the launch plans the bridge's host half for that via, reserves a port for its via address, and
// renders pi's models.json with its via row at that port. The host half serves the via route from
// the launch's input tables, outside Seatbelt.
//
// WHAT ONLY THIS TEST CAN SEE (the reachability carve-out: no nested jail and no Linux unit test
// reaches it): that the via URL pi's own config names answers from inside the sandbox, on the Mac's
// loopback the host half shares; that it refuses a request without the caller token pi's row names
// (401); that with the token the route answers 503 naming runtime's URL composed from the provider's
// region, since this launch delivers pi no AWS credential (the jail's assertion in
// TestBedrockBridgeCarriesPiToRuntimeInItsRegion); and that the host half ran as the host user and
// is gone once the session ends. It runs no agent and calls no API: the probe reads the via URL out
// of pi's models.json and speaks chat-completions with curl, and no credential exists to reach AWS.
func TestMacosUserBridgesPiThroughTheHostHalfViaRoute(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{
		"packs": ["pi", "bedrock", "wire-bridge"],
		"profile": {"pi": "bedrock-bridge"},
		"providers": {"bedrock": {"region": "us-east-1"}}
	}`)
	ws := macosUserWorkspace(t, `{}`)
	uid := sandboxUID(t)
	me := strconv.Itoa(os.Getuid())

	watch := watchForDoorway(wireBridgeHostHalfArgv)
	t.Cleanup(func() { watch.stop() })
	curl := func(auth, out string) string {
		return `curl -sS -o ` + out + ` -w '%{http_code}' -H 'content-type: application/json' ` + auth +
			` -d '{"model":"m","messages":[]}' "$url/chat/completions"`
	}
	r := macosUserRunProbe(t, "via-route", ws, strings.Join([]string{
		`echo "=== VIA ==="`,
		`[ -r ~/.config/yolo-agent-env/pi.sh ] && . ~/.config/yolo-agent-env/pi.sh`,
		`url=$(sed -n 's|.*"baseUrl": *"\(http://[^"]*/agent/pi\)".*|\1|p' "$HOME/.pi/agent/models.json" | head -n 1)`,
		`echo "URL=$url"`,
		`echo "NOTOKEN=$(` + curl("", "/dev/null") + `)"`,
		`echo "TOKEN=$(` + curl(`-H "authorization: Bearer ${YOLO_SERVICE_WIRE_BRIDGE_TOKEN:-}"`, `"$HOME/via-route.json"`) + `)"`,
		`echo "BODY=$(tr -d '\n' < "$HOME/via-route.json")"`,
		`echo "=== END ==="`,
	}, "\n"))
	saw := watch.stop()
	out := section(r.stdout, "=== VIA ===", "=== END ===")
	diag := "\n--- probe:\n" + out + "\n--- launch stderr:\n" + r.stderr
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}

	if !strings.Contains(r.combined(), `Started the "wire-bridge" service (pack "wire-bridge"`) {
		t.Errorf("the launch did not say it started the bridge's host half for pi's via%s", diag)
	}
	if strings.Contains(r.combined(), `profile "bedrock-bridge"'s via — its service does not run here`) {
		t.Errorf("the launch still clears the via it now serves%s", diag)
	}
	if !strings.HasPrefix(got["URL"], "http://127.0.0.1:") || strings.HasPrefix(got["URL"], "http://127.0.0.1:8216/") {
		t.Fatalf("pi's models.json names no via row at a port picked for this launch%s", diag)
	}
	if got["NOTOKEN"] != "401" {
		t.Errorf("a request from inside the sandbox without the caller token got %q, want 401 "+
			"(000 means nothing answered at the address)%s", got["NOTOKEN"], diag)
	}
	if got["TOKEN"] != "503" ||
		!strings.Contains(got["BODY"], "goes to Bedrock (https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1)") {
		t.Errorf("pi's via route must answer 503 naming runtime's URL composed from us-east-1, got %q %s%s",
			got["TOKEN"], got["BODY"], diag)
	}
	requireDoorwayRanOutsideAndStopped(t, wireBridgeHostHalfArgv, saw, me, uid, diag)
}
