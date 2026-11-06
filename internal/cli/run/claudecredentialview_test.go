package run

// claudecredentialview_test.go pins the launch's half of the Claude credential view
// (docs/design/claude-login-without-interception.md, CL-D10 and CL-D11): the switch selects the
// view or the interception, per backend, for every reader at once — the argv, the jail-daemon
// payload, the jail's environment and the Apple Container allow list — and both launch arms
// register the workspace's view.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudesecurity"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

func TestAccountSecurityAdapterCarrierFollowsOnlyTheResolvedMacosUserView(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.Getenv = viewEnv("1")
	for _, tc := range []struct {
		name string
		rt   string
		mode macosuser.Mode
		view string
		want string
	}{
		{"account view", "macos-user", macosuser.ModeAccount, "1", claudesecurity.AccountMode},
		{"self view", "macos-user", macosuser.ModeSelf, "1", ""},
		{"view off", "macos-user", macosuser.ModeAccount, "0", ""},
		{"container view", "podman", macosuser.ModeAccount, "1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectMacosUserMode(o, tc.mode)
			o.Getenv = viewEnv(tc.view)
			if got := o.claudeSecurityCompatMode(tc.rt, jsonx.NewOrderedMap()); got != tc.want {
				t.Fatalf("carrier mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBothMacosUserProductionLaunchArmsCarryTheAccountSecurityAdapter(t *testing.T) {
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "setClaudeSecurityCompatEnv(launchEnv, o.claudeSecurityCompatMode(rt, cfg))"); n != 2 {
		t.Fatalf("the keeper and keeperless view launch arms carry the resolved security adapter %d times, want both", n)
	}
	for _, fragment := range []string{
		"if o.claudeCredentialView(rt, cfg) {\n\t\t\t\tlaunchEnv.Set(claudeview.SwitchEnv, claudeview.ResolvedValue(true))\n\t\t\t\tsetClaudeSecurityCompatEnv",
	} {
		if !strings.Contains(string(body), fragment) {
			t.Errorf("security carrier is no longer adjacent to the production credential-view decision")
		}
	}
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

func TestRegistrationUsesTheClaudeHomeForEachMode(t *testing.T) {
	brokerFixtureDirs(t, true)
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = viewEnv("1")
	var got []claudeview.Location
	saved := registerView
	t.Cleanup(func() { registerView = saved })
	registerView = func(loc claudeview.Location, rt, cname string) (oauthbroker.RegisterResult, error) {
		got = append(got, loc)
		if err := loc.EnsureDir(); err != nil {
			return oauthbroker.RegisterResult{}, err
		}
		if err := loc.Write([]byte(`{"claudeAiOauth":{"accessToken":"fake-enrollment"}}`)); err != nil {
			return oauthbroker.RegisterResult{}, err
		}
		return oauthbroker.RegisterResult{Wrote: true}, nil
	}

	for _, tc := range []struct {
		name string
		rt   string
		mode macosuser.Mode
		want string
	}{
		{name: "podman account home", rt: "podman", mode: macosuser.ModeAccount, want: "claude"},
		{name: "podman self-configured options", rt: "podman", mode: macosuser.ModeSelf, want: "claude"},
		{name: "macos-user account home", rt: "macos-user", mode: macosuser.ModeAccount, want: ".claude"}, // HOME is the workspace home (MMP-D25)
		{name: "macos-user self home", rt: "macos-user", mode: macosuser.ModeSelf, want: ".claude"},
		{name: "Apple Container home", rt: "container", mode: macosuser.ModeSelf, want: ".claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectMacosUserMode(o, tc.mode)
			o.MacosUserHostHome = "/Users/you"
			o.registerClaudeCredentialView(tc.rt, "yolo-ws", jsonx.NewOrderedMap())
			loc := got[len(got)-1]
			if loc.Workspace != ws || loc.Subdir != tc.want {
				t.Fatalf("registered %+v, want workspace %q subdir %q", loc, ws, tc.want)
			}
			if tc.rt == "macos-user" {
				homes := macosuser.LaunchHomes(tc.mode, paths.LegacyHomeState(ws), o.MacosUserHostHome)
				if loc.Path() != filepath.Join(homes.Home, claudeview.ViewRel) {
					t.Fatalf("registered view %q, Claude HOME reads %q", loc.Path(), filepath.Join(homes.Home, claudeview.ViewRel))
				}
			}
			info, err := os.Lstat(loc.Path())
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("fake enrollment at %q is not a regular file: info=%v err=%v", loc.Path(), info, err)
			}
			gotBytes, err := loc.Read()
			if err != nil || !bytes.Contains(gotBytes, []byte("fake-enrollment")) {
				t.Fatalf("registered location does not read its fake enrollment: bytes=%q err=%v", gotBytes, err)
			}
			if tc.rt == "macos-user" && tc.mode == macosuser.ModeSelf {
				outside := filepath.Join(t.TempDir(), "outside.json")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(loc.Path()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, loc.Path()); err != nil {
					t.Fatal(err)
				}
				o.registerClaudeCredentialView(tc.rt, "yolo-ws", jsonx.NewOrderedMap())
				if outsideBytes, err := os.ReadFile(outside); err != nil || string(outsideBytes) != "outside" {
					t.Fatalf("registration followed a symlink at the self-mode Claude path: bytes=%q err=%v", outsideBytes, err)
				}
				if err := os.Remove(loc.Path()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}

	before := len(got)
	o.Getenv = viewEnv("")
	o.registerClaudeCredentialView("macos-user", "yolo-ws", jsonx.NewOrderedMap())
	if len(got) != before {
		t.Errorf("view-off registration changed count from %d to %d", before, len(got))
	}
	brokerFixtureDirs(t, false)
	o.Getenv = viewEnv("1")
	o.registerClaudeCredentialView("macos-user", "yolo-ws", jsonx.NewOrderedMap())
	if len(got) != before {
		t.Errorf("registration occurred without selected broker pack: count %d, want %d", len(got), before)
	}
}

func TestMacosUserViewNoticeReflectsTheInstalledCompatibilityPath(t *testing.T) {
	brokerFixtureDirs(t, true)
	for _, tc := range []struct {
		name string
		mode macosuser.Mode
		want string
		fn   func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error)
	}{
		{name: "self registration error", mode: macosuser.ModeSelf, want: "session TMPDIR", fn: func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error) {
			return oauthbroker.RegisterResult{}, os.ErrPermission
		}},
		{name: "self no canonical login", mode: macosuser.ModeSelf, want: "session TMPDIR", fn: func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error) {
			return oauthbroker.RegisterResult{}, nil
		}},
		{name: "account registration error", mode: macosuser.ModeAccount, want: "scoped security adapter fails Keychain writes promptly", fn: func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error) {
			return oauthbroker.RegisterResult{}, os.ErrPermission
		}},
		{name: "account no canonical login", mode: macosuser.ModeAccount, want: "scoped security adapter's fast Keychain-write refusal", fn: func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error) {
			return oauthbroker.RegisterResult{}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := goldenOptions(t.TempDir(), t.TempDir())
			selectMacosUserMode(o, tc.mode)
			o.Getenv = viewEnv("1")
			var stderr bytes.Buffer
			o.Stderr = &stderr
			saved := registerView
			t.Cleanup(func() { registerView = saved })
			registerView = tc.fn
			o.registerClaudeCredentialView("macos-user", "yolo-ws", jsonx.NewOrderedMap())
			message := stderr.String()
			if !strings.Contains(message, tc.want) {
				t.Errorf("notice omitted active mechanism %q:\n%s", tc.want, message)
			}
			if strings.Contains(message, "Keychain write hang") || strings.Contains(message, "does not yet set session TMPDIR") {
				t.Errorf("notice repeats a repaired failure:\n%s", message)
			}
		})
	}
}

func TestMacosUserKeeperRegistersModeSpecificLocationIntoTheFinalEnvironment(t *testing.T) {
	brokerFixtureDirs(t, true)
	for _, tc := range []struct {
		name   string
		mode   macosuser.Mode
		want   string
		notice string
	}{
		{name: "self", mode: macosuser.ModeSelf, want: ".claude", notice: "uses the session TMPDIR"},
		{name: "account", mode: macosuser.ModeAccount, want: ".claude", notice: "scoped security adapter fails Keychain writes promptly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `["claude"]`)
			ws := t.TempDir()
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			o.DryRun = false
			selectMacosUserMode(o, tc.mode)
			o.MacosUserHostHome = t.TempDir()
			o.Getenv = func(k string) string {
				switch k {
				case claudeview.SwitchEnv:
					return "1"
				case "YOLO_SETUP":
					return testSetupForRuntime("macos-user")
				default:
					return ""
				}
			}
			var got *jsonx.OrderedMap
			var registered *claudeview.Location
			o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
				_ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, env *jsonx.OrderedMap,
				_ []packload.BlockedTool, _ macosuser.JailDaemons) int {
				got = env
				return 0
			}
			saved := registerView
			t.Cleanup(func() { registerView = saved })
			registerView = func(loc claudeview.Location, rt, cname string) (oauthbroker.RegisterResult, error) {
				registered = &loc
				return oauthbroker.RegisterResult{}, errors.New("fixture enrollment")
			}
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
			}
			if registered == nil || registered.Workspace != ws || registered.Subdir != tc.want {
				t.Errorf("keeper registered %+v, want workspace %q subdir %q", registered, ws, tc.want)
			}
			if !strings.Contains(stderr.String(), tc.notice) || strings.Contains(stderr.String(), "YOLO_CLAUDE_CREDENTIAL_VIEW=0") {
				t.Errorf("keeper notice does not match %s repaired mechanism or repeats old workaround:\n%s", tc.name, stderr.String())
			}
			if got == nil {
				t.Fatal("macos-user launch did not hand its final environment to the runner")
			}
			if v := envAt(got, claudeview.SwitchEnv); v != "1" {
				t.Errorf("final %s = %q, want 1", claudeview.SwitchEnv, v)
			}
			if v := envAt(got, claudeview.SecureStorageEnv); v != "" {
				t.Errorf("view-on final environment points Claude at a shared refresh-token store: %q", v)
			}
			wantSecurity := ""
			if tc.mode == macosuser.ModeAccount {
				wantSecurity = claudesecurity.AccountMode
			}
			if v := envAt(got, claudesecurity.EnvVar); v != wantSecurity {
				t.Errorf("final %s = %q, want %q", claudesecurity.EnvVar, v, wantSecurity)
			}
		})
	}
}

func TestMacosUserKeeperRoundTripsTheResolvedModeInsteadOfReadingConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	selectMacosUserMode(o, macosuser.ModeSelf)
	o.macosUserKey = &macosUserKeying{}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("macos_user_mode", "account") // stale config must not override the resolved launch mode
	plan, err := o.macosUserKeeperPlanFor(cfg, "macos-user", "yolo-mode-roundtrip", stagedPacks{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, err := writeKeeperPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	read, err := readKeeperPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	k := newKeeper(read, KeeperSeams{}, nil, nil, nil, nil)
	if k.o.MacosUserMode != macosuser.ModeSelf {
		t.Errorf("keeper resolved mode = %s, want the launch's self mode despite stale config", k.o.MacosUserMode)
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
// arm's host services are its keeper's (keeper.go), so its registration is there, inside the one
// block the seal skips (FP-D15): a sealed fork build's keeper starts no service and registers no
// view, which TestASealedKeeperStartsNoServiceAndRegistersNoView pins by running one.
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
	kre := regexp.MustCompile(`(?s)\n\tif !p\.Sealed \{[^\n]*\n[^{}]{0,200}?startPlannedLoopholes\(p\.Cname, p\.Runtime, cfg, p\.Payload\)\n.{0,40}?\n\t\to\.registerClaudeCredentialView\(p\.Runtime, p\.Cname, cfg\)\n\t\}\n`)
	if n := len(kre.FindAllIndex(keeper, -1)); n != 1 {
		t.Errorf("the keeper registers the credential view right after it starts the container arm's host "+
			"services, both inside the seal's gate, %d times, want once", n)
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
	if !strings.Contains(src, "view := o.claudeCredentialView(rt, cfg)") ||
		!strings.Contains(src, "return HostServiceAdmittedOn(rt, name, view)") ||
		!HostServiceAdmittedOn("container", broker.BrokerLoopholeName, true) ||
		HostServiceAdmittedOn("container", broker.BrokerLoopholeName, false) {
		t.Error("Apple Container's allow list no longer admits the claude broker for a view " +
			"launch (and only for one): nothing would write the view on the backend where it is the default")
	}
	// And its endpoint stays unpublished to the jail, which dials nothing.
	if !promisedEndpointIsUnpublishable("container", broker.BrokerLoopholeName) {
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
