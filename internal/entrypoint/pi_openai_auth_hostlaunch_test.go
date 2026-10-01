package entrypoint

// pi_openai_auth_hostlaunch_test.go pins the message fix of docs/design/host-computed-layer.md
// §8.1 item 3: when pi's delivered OpenAI extension cannot reach yolo's shared login because
// no route is set and it runs OUTSIDE a jail — pi started directly or from an IDE on the host —
// the error tells the user to launch pi through `yolo host -- pi`. Before, it said only
// "openai-auth-client: YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT is not set", naming a jail's
// variable to someone who has no jail.
//
// The client the extension runs is the REAL one: the `yolo` on the harness's PATH re-executes
// this test binary as `yolo internal openai-auth-client`, which calls openauthclient.Run with
// the extension's own environment. So each case is the real client's routing and the real
// failure text, and the extension's rewrite is checked against what it actually meets. No case
// can reach a broker: none sets an endpoint, and the one host-socket case names a path that
// does not exist.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

const openAIClientHelperEnv = "YOLO_TEST_OPENAI_AUTH_CLIENT_HELPER"

// TestPiOpenAIAuthClientHelper is not a test: it is `yolo internal openai-auth-client` for the
// harness below, run when this binary is re-executed with the helper variable set.
func TestPiOpenAIAuthClientHelper(t *testing.T) {
	if os.Getenv(openAIClientHelperEnv) != "1" {
		t.Skip("helper process for the pi OpenAI extension's host-launch tests")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) < 2 || args[0] != "internal" || args[1] != "openai-auth-client" {
		fmt.Fprintf(os.Stderr, "helper: unexpected argv %q\n", args)
		os.Exit(2)
	}
	os.Exit(openauthclient.Run(args[2:], os.Getenv, os.Stdout, os.Stderr))
}

// hostLaunchCase runs the shipped extension's login and refresh under env and returns the two
// error messages, or fails if either call succeeded.
type hostLaunchCase struct {
	// jailHome creates ~/.yolo/bin in the fixture home, the jail witness that survives a
	// scrubbed environment.
	jailHome bool
	// noYolo leaves `yolo` off PATH entirely — a direct launch on a host without it there.
	noYolo bool
	env    []string
}

func runPiAuthFailure(t *testing.T, c hostLaunchCase) (login, refresh string) {
	t.Helper()
	f := newPiExtensionFixture(t)
	if c.jailHome {
		if err := os.MkdirAll(filepath.Join(f.home, ".yolo", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	if !c.noYolo {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\nexec \"$YOLO_TEST_SELF\" -test.run='^TestPiOpenAIAuthClientHelper$' -- \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "yolo"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		c.env = append(c.env, "YOLO_TEST_SELF="+self, openAIClientHelperEnv+"=1")
	}
	harness := `
import extension from "./extension.mjs";
let oauth;
await extension({ registerProvider(_name, config) { oauth = config.oauth; } });
const out = {};
for (const [name, call] of [["login", () => oauth.login({})],
		["refresh", () => oauth.refreshToken({}, new AbortController().signal)]]) {
	try { await call(); out[name] = "SUCCEEDED"; } catch (error) { out[name] = error.message; }
}
console.log(JSON.stringify(out));
`
	if err := os.WriteFile(filepath.Join(f.dir, "harness.mjs"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	node := requireNode(t, "the host-launched pi openai-auth extension")
	cmd := exec.Command(node, "harness.mjs")
	cmd.Dir = f.dir
	// A CLEAN environment, never os.Environ(): a developer's jail carries YOLO_VERSION and
	// perhaps a live broker endpoint, and either would decide the case being tested.
	cmd.Env = append([]string{"HOME=" + f.home, "PATH=" + bin}, c.env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the extension harness: %v\n%s", err, out)
	}
	var got struct{ Login, Refresh string }
	if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
		t.Fatalf("decoding the harness output %q: %v", out, err)
	}
	if got.Login == "SUCCEEDED" || got.Refresh == "SUCCEEDED" {
		t.Fatalf("a call with no route to the broker succeeded: %+v", got)
	}
	return got.Login, got.Refresh
}

func requireBoth(t *testing.T, login, refresh string, check func(msg string) string) {
	t.Helper()
	for name, msg := range map[string]string{"login": login, "refresh": refresh} {
		if problem := check(msg); problem != "" {
			t.Errorf("%s: %s\nmessage: %s", name, problem, msg)
		}
	}
}

// A DIRECT OR IDE LAUNCH ON THE HOST: no route, no jail. The message names the launch that
// works, whether or not yolo is on PATH.
func TestPiOpenAIAuthOutsideAJailSaysToLaunchThroughYoloHost(t *testing.T) {
	for name, c := range map[string]hostLaunchCase{
		"yolo on PATH":     {},
		"yolo not on PATH": {noYolo: true},
	} {
		t.Run(name, func(t *testing.T) {
			login, refresh := runPiAuthFailure(t, c)
			requireBoth(t, login, refresh, func(msg string) string {
				if !strings.Contains(msg, "`yolo host -- pi`") {
					return "does not say to launch pi through `yolo host -- pi`"
				}
				return ""
			})
		})
	}
}

// INSIDE A JAIL the advice would be wrong — there is no `yolo host` to launch through — so the
// client's own message stands, naming the jail's variable. Both witnesses count, the second
// for an environment scrubbed of YOLO_VERSION.
func TestPiOpenAIAuthInsideAJailKeepsTheClientsMessage(t *testing.T) {
	for name, c := range map[string]hostLaunchCase{
		"YOLO_VERSION set":      {env: []string{"YOLO_VERSION=0.10.0"}},
		"a jail home, scrubbed": {jailHome: true},
	} {
		t.Run(name, func(t *testing.T) {
			login, refresh := runPiAuthFailure(t, c)
			requireBoth(t, login, refresh, func(msg string) string {
				if strings.Contains(msg, "yolo host") {
					return "tells a jail to launch through `yolo host`"
				}
				if !strings.Contains(msg, openauthclient.EndpointEnv) {
					return "lost the client's own message naming " + openauthclient.EndpointEnv
				}
				return ""
			})
		})
	}
}

// THE EXTENSION NAMES THE CLIENT'S TWO ROUTE VARIABLES, spelled as the client spells them: it
// is JavaScript, so it cannot import the constants, and a renamed variable would otherwise
// leave it testing for a route that no launch sets any more.
func TestPiOpenAIAuthExtensionSpellsTheClientsRouteVariables(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(shippedPiPack(t).Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{openauthclient.EndpointEnv, openauthclient.HostSocketEnv} {
		if !strings.Contains(string(source), `"`+name+`"`) {
			t.Errorf("yolo-openai-auth.js does not spell %s, the variable the client reads", name)
		}
	}
}

// A `yolo host --` LAUNCH has a route, so a failure there is something else — here, a broker
// socket that is not there — and the message is the client's, with no launch advice.
func TestPiOpenAIAuthWithAHostRouteKeepsTheClientsMessage(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "absent.sock")
	login, refresh := runPiAuthFailure(t, hostLaunchCase{
		env: []string{openauthclient.HostSocketEnv + "=" + socket}})
	requireBoth(t, login, refresh, func(msg string) string {
		if strings.Contains(msg, "yolo host --") {
			return "gives launch advice to a launch that already went through `yolo host`"
		}
		if !strings.Contains(msg, "host OpenAI credential service") {
			return "does not carry the client's own failure"
		}
		return ""
	})
}
