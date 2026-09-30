package run

// claudecredentialview_test.go pins the launch's half of the Claude credential view
// (docs/design/claude-login-without-interception.md, CL-D10 and CL-D11): the switch selects the
// view or the interception, per backend, for every reader at once — the argv, the jail-daemon
// payload, the jail's environment and the Apple Container allow list — and both launch arms
// register the workspace's view.

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

func viewEnv(val string) func(string) string {
	return func(k string) string {
		if k == claudeview.SwitchEnv {
			return val
		}
		return ""
	}
}

func terminatorIn(specs []string) bool {
	for _, s := range specs {
		if strings.Contains(s, "oauth-terminator") || s == broker.BrokerLoopholeName {
			return true
		}
	}
	return false
}

func jailDaemonNames(o *Options, rt string) []string {
	var names []string
	for _, s := range o.jailDaemonsFor(jsonx.NewOrderedMap(), rt, nil) {
		names = append(names, s.Name)
		names = append(names, s.Cmd...)
	}
	return names
}

func TestTheSwitchSelectsTheViewOrTheInterceptionAtLaunch(t *testing.T) {
	brokerFixtureDirs(t, true)

	// Podman, switch unset: the interception, exactly as before.
	o := goldenOptions("/ws", t.TempDir())
	argv := strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	if !strings.Contains(argv, "--add-host platform.claude.com:127.0.0.1") {
		t.Fatalf("podman with the switch unset lost the interception:\n%s", argv)
	}
	if strings.Contains(argv, claudeview.SwitchEnv) {
		t.Errorf("an interception launch told the jail it has a view:\n%s", argv)
	}
	if !terminatorIn(jailDaemonNames(o, "podman")) {
		t.Error("podman with the switch unset lost the terminator from the jail-daemon payload")
	}

	// Podman, switch on: the view, and nothing of the interception.
	o.Getenv = viewEnv("1")
	argv = strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	for _, unwanted := range []string{"--add-host", "platform.claude.com", "NODE_EXTRA_CA_CERTS"} {
		if strings.Contains(argv, unwanted) {
			t.Errorf("a view launch still carries %q:\n%s", unwanted, argv)
		}
	}
	if !strings.Contains(argv, "-e "+claudeview.SwitchEnv+"=1") {
		t.Errorf("a view launch did not hand the jail the resolved switch:\n%s", argv)
	}
	if terminatorIn(jailDaemonNames(o, "podman")) {
		t.Error("a view launch still starts the terminator in the jail")
	}

	// Every backend: off until measured (CL-D11), and `=1` turns it on.
	for _, rt := range []string{"podman", "macos-user", "container"} {
		o.Getenv = viewEnv("")
		if o.claudeCredentialView(rt, jsonx.NewOrderedMap()) {
			t.Errorf("%s defaults to the view before its measures passed (CL-D10, CL-D11)", rt)
		}
		o.Getenv = viewEnv("1")
		if !o.claudeCredentialView(rt, jsonx.NewOrderedMap()) {
			t.Errorf("YOLO_CLAUDE_CREDENTIAL_VIEW=1 did not turn the view on on %s", rt)
		}
	}
}

func TestTheViewNeedsTheBrokerLoophole(t *testing.T) {
	brokerFixtureDirs(t, false) // the loophole is off: a Bedrock user, say
	o := goldenOptions("/ws", t.TempDir())
	o.Getenv = viewEnv("1")
	if o.claudeCredentialView("podman", jsonx.NewOrderedMap()) {
		t.Fatal("the view was selected with the broker loophole off: nothing would write it, " +
			"and the jail would skip the shared file too")
	}
	if args := o.claudeCredentialViewEnvArgs("podman", jsonx.NewOrderedMap()); len(args) != 0 {
		t.Errorf("the jail was told it has a view with no broker to write it: %v", args)
	}
}

func TestRegistrationWritesTheViewAndSaysWhenALogoutIsUndone(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = viewEnv("1")
	var got []claudeview.Location
	saved := registerView
	t.Cleanup(func() { registerView = saved })
	registerView = func(loc claudeview.Location, rt, cname string) (oauthbroker.RegisterResult, error) {
		got = append(got, loc)
		return oauthbroker.RegisterResult{WasLoggedOut: true, Wrote: true}, nil
	}

	o.registerClaudeCredentialView("podman", "yolo-ws", jsonx.NewOrderedMap())
	if len(got) != 1 || got[0].Workspace != "/ws" || got[0].Subdir != "claude" {
		t.Fatalf("registered %v, want /ws's podman overlay dir `claude`", got)
	}
	if !strings.Contains(stderr.String(), "signs it back in") {
		t.Errorf("the relaunch did not say it undid the jail's /logout (OQ-CL2):\n%s", stderr.String())
	}
	o.registerClaudeCredentialView("container", "yolo-ws", jsonx.NewOrderedMap())
	if got[1].Subdir != ".claude" {
		t.Errorf("Apple Container's view dir = %q, want the dotted `.claude` its whole-home bind reads", got[1].Subdir)
	}

	// Off: nothing registered.
	o.Getenv = viewEnv("")
	o.registerClaudeCredentialView("podman", "yolo-ws", jsonx.NewOrderedMap())
	if len(got) != 2 {
		t.Error("an interception launch registered a view")
	}
}

// TestBothLaunchArmsRegisterTheView is the call-site half: the registration above is only a
// feature if the container arm and the macos-user arm both reach it, right after the host
// services started (the singleton's ensure creates the state dir it writes into). The container
// arm's host services are its keeper's (keeper.go), so its registration is there.
func TestBothLaunchArmsRegisterTheView(t *testing.T) {
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)startLoopholesDisclosed\(cname, rt, cfg, [a-zA-Z.]+, jailDaemons\)\n.{0,400}?o\.registerClaudeCredentialView\(rt, cname, cfg\)`)
	if n := len(re.FindAllIndex(body, -1)); n != 1 {
		t.Errorf("run.go registers the credential view after %d of its 1 host-service start "+
			"(the macos-user arm)", n)
	}
	keeper, err := os.ReadFile("keeper.go")
	if err != nil {
		t.Fatal(err)
	}
	kre := regexp.MustCompile(`(?s)startPlannedLoopholes\(p\.Cname, p\.Runtime, cfg, p\.Payload\)\n.{0,40}?\n\to\.registerClaudeCredentialView\(p\.Runtime, p\.Cname, cfg\)`)
	if !kre.Match(keeper) {
		t.Error("the keeper no longer registers the credential view right after it starts the container " +
			"arm's host services")
	}
	if !strings.Contains(string(body), "launchEnv.Set(claudeview.SwitchEnv") {
		t.Error("the macos-user arm no longer hands its bootstrap the resolved switch")
	}
}

func TestAppleContainerStartsTheBrokerOnlyForAView(t *testing.T) {
	body, err := os.ReadFile("loopholesruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if !strings.Contains(src, "if o.claudeCredentialView(rt, cfg) {") ||
		!strings.Contains(src, "name == openAIAuthBrokerName || name == broker.BrokerLoopholeName") {
		t.Error("Apple Container's allow list no longer admits the claude broker for a view " +
			"launch: nothing would write the view on the backend where it is the default")
	}
	// And its endpoint stays unpublished to the jail, which dials nothing.
	if !hostScopedEndpointIsUnpublishable("container", broker.BrokerLoopholeName) {
		t.Error("the claude broker's endpoint became publishable to Apple Container")
	}
}

// TestTheViewIsNeverBoundAsASingleFile pins the one delivery that would break the view whatever
// the broker does: a bind mount of the view FILE. The broker replaces the view by rename, and a
// single-file bind pins the inode, so such a jail would read the first view forever
// (hive-mind#2296, "STALE, forever"). The view must arrive inside a bound directory.
func TestTheViewIsNeverBoundAsASingleFile(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		o := goldenOptions("/ws", t.TempDir())
		argv := o.assembleRunCmd(relocationInput(t, rt, "/ws/.yolo/home", nil))
		dirBound := false
		for i, a := range argv {
			if a != "-v" || i+1 >= len(argv) {
				continue
			}
			spec := argv[i+1]
			if strings.Contains(spec, claudeview.ViewFile) {
				t.Errorf("%s binds a credentials file by itself: %s", rt, spec)
			}
			parts := strings.Split(spec, ":")
			if len(parts) >= 2 && (parts[1] == "/home/agent/.claude" || parts[1] == "/home/agent") {
				dirBound = true
			}
		}
		if !dirBound {
			t.Errorf("%s binds neither ~/.claude nor the whole home as a directory:\n%v", rt, argv)
		}
	}
}
