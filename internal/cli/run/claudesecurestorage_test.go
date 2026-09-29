package run

// claudesecurestorage_test.go pins CL-D22's bridge (docs/design/claude-login-without-interception.md)
// at every launch arm that delivers it: both container backends' argv, and the macos-user launch
// env driven through Run. Each call-site test fails if that arm's delivery is deleted, and each
// also asserts the directory the variable names is the one that arm mounts or lays WRITABLE,
// because Claude creates its lock files and its temp file there. The view and the bridge are
// asserted mutually exclusive through the view's own predicate.

import (
	"bytes"
	"path"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// claudeSharedDirRel is what the shipped claude pack declares; spelled here so a change to the
// manifest's `at` is a test failure to look at, not a silent re-point.
const claudeSharedDirRel = ".claude-shared-credentials"

// envArgValue returns the value of `-e KEY=…` in argv and whether it is there.
func envArgValue(argv []string, key string) (string, bool) {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], key+"=") {
			return strings.TrimPrefix(argv[i+1], key+"="), true
		}
	}
	return "", false
}

// bindTo returns the `-v` spec whose destination is dest, "" when none.
func bindTo(argv []string, dest string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		parts := strings.Split(argv[i+1], ":")
		if len(parts) >= 2 && parts[1] == dest {
			return argv[i+1]
		}
	}
	return ""
}

func TestTheClaudePackNamesTheBridgesDirectory(t *testing.T) {
	if got := claudeSharedCredentialDir(claudePackFixture(t)); got != claudeSharedDirRel {
		t.Fatalf("the claude pack's shared_credentials hook links Claude's credential into %q, "+
			"want %q", got, claudeSharedDirRel)
	}
	if got := claudeSharedCredentialDir(nil); got != "" {
		t.Errorf("no pack selected, yet the bridge points Claude at %q", got)
	}
}

// BOTH CONTAINER BACKENDS: the argv carries the variable, it names the directory the argv
// binds, and that bind is read-write. Driven through assembleRunCmd, so deleting its call to
// claudeSecureStorageEnvArgs fails both rows.
func TestBothContainerBackendsPointClaudesStoreAtTheWritableSharedDir(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			argv := o.assembleRunCmd(relocationInput(t, rt, "/ws/.yolo/home", nil))
			want := jailHome + "/" + claudeSharedDirRel
			got, ok := envArgValue(argv, claudeview.SecureStorageEnv)
			if !ok {
				t.Fatalf("%s's argv does not point Claude's credential store anywhere; Claude "+
					"would keep reading ~/.claude/.credentials.json through the symlink its "+
					"first refresh replaces:\n%v", rt, argv)
			}
			if got != want {
				t.Errorf("%s=%q, want %q", claudeview.SecureStorageEnv, got, want)
			}
			spec := bindTo(argv, got)
			if spec == "" {
				t.Fatalf("%s binds nothing at %s, the directory Claude is pointed at:\n%v", rt, got, argv)
			}
			if parts := strings.Split(spec, ":"); len(parts) > 2 && strings.Contains(parts[2], "ro") {
				t.Errorf("%s binds %s read-only (%s): Claude creates its refresh lock, its "+
					"write lock and a temp file there", rt, got, spec)
			}
		})
	}
}

// A VIEW LAUNCH carries the view's switch and not the bridge, and every other launch the
// bridge and not the switch: one predicate decides both, on every backend.
func TestTheBridgeAndTheViewAreMutuallyExclusive(t *testing.T) {
	brokerFixtureDirs(t, true)
	packs := claudePackFixture(t)
	cfg := jsonx.NewOrderedMap()
	o := goldenOptions("/ws", t.TempDir())
	for _, rt := range []string{"podman", "container", "macos-user"} {
		o.Getenv = viewEnv("1")
		if !o.claudeCredentialView(rt, cfg) {
			t.Fatalf("%s: the fixture did not select the view", rt)
		}
		if d := o.claudeSecureStorageDir(rt, cfg, packs, "/home/x"); d != "" {
			t.Errorf("%s: a view launch also points Claude's store at %s, so Claude would read "+
				"the machine's refresh token instead of its view", rt, d)
		}
		o.Getenv = viewEnv("")
		if d := o.claudeSecureStorageDir(rt, cfg, packs, "/home/x"); d != "/home/x/"+claudeSharedDirRel {
			t.Errorf("%s: an interception launch bridges to %q, want /home/x/%s", rt, d, claudeSharedDirRel)
		}
	}

	// And at the podman argv itself: the switch's `-e` and the bridge's never ride together.
	for _, tc := range []struct {
		val                  string
		wantView, wantBridge bool
	}{{"1", true, false}, {"", false, true}} {
		o.Getenv = viewEnv(tc.val)
		in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
		in.cfg = cfg
		argv := o.assembleRunCmd(in)
		_, view := envArgValue(argv, claudeview.SwitchEnv)
		_, bridge := envArgValue(argv, claudeview.SecureStorageEnv)
		if view != tc.wantView || bridge != tc.wantBridge {
			t.Errorf("switch %q: argv carries the view switch=%v, the bridge=%v; want %v, %v",
				tc.val, view, bridge, tc.wantView, tc.wantBridge)
		}
	}
}

// THE BRIDGE DOES NOT NEED THE BROKER. A Bedrock user who turned the loophole off still shares
// one Claude login through the machine-scope directory, so the bridge still applies.
func TestTheBridgeHoldsWithTheBrokerLoopholeOff(t *testing.T) {
	brokerFixtureDirs(t, false)
	o := goldenOptions("/ws", t.TempDir())
	o.Getenv = viewEnv("1")
	if d := o.claudeSecureStorageDir("podman", jsonx.NewOrderedMap(), claudePackFixture(t), jailHome); d == "" {
		t.Error("with the broker loophole off the view cannot apply, and the bridge must")
	}
}

// MACOS-USER, driven through Run: the launch env the backend is handed (which becomes the
// sandbox session's env file) points Claude's store at the ACCOUNT HOME's machine-scope
// directory, which the bootstrap lays as a real directory and the Seatbelt profile lets the
// sandbox write. Deleting run.go's launchEnv.Set fails the first assertion.
func TestTheMacosUserSessionPointsClaudesStoreAtTheWritableSharedDir(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		home := packHome(t)
		writeUserPacks(t, home, `["claude"]`)
		ws := t.TempDir()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.DryRun = dryRun
		var got *jsonx.OrderedMap
		var overlay macosuser.HomeOverlay
		var packs []*packload.Pack
		o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
			ov macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, env *jsonx.OrderedMap,
			_ []packload.BlockedTool, _ macosuser.JailDaemons) int {
			got, overlay = env, ov
			return 0
		}
		if rc := Run(*o); rc != 0 {
			t.Fatalf("dry run %v: Run() = %d\nstderr:\n%s", dryRun, rc, stderr.String())
		}
		sbHome := macosuser.SandboxHome()
		want := path.Join(sbHome, claudeSharedDirRel)
		if v := envAt(got, claudeview.SecureStorageEnv); v != want {
			t.Fatalf("dry run %v: the macos-user session's %s = %q, want %q: the sandbox's Claude "+
				"would keep the symlink its first refresh replaces", dryRun, claudeview.SecureStorageEnv, v, want)
		}
		if dryRun {
			continue
		}
		packs = claudePackFixture(t)
		// The bootstrap lays it as a REAL directory in the account home, not a link into the
		// workspace sidecar.
		layout := entrypoint.DeriveDarwinHomeLayout(sbHome, "/ws/.yolo/home",
			packload.WritableDirs(packs), packload.SharedDirs(packs))
		laid := false
		for _, d := range layout.Dirs {
			if d == want {
				laid = true
			}
		}
		for _, l := range layout.Links {
			if l.Path == want {
				t.Errorf("the account home's %s is a link into the sidecar (%s)", want, l.Target)
			}
		}
		if !laid {
			t.Errorf("the macos-user bootstrap does not create %s: %v", want, layout.Dirs)
		}
		// And the Seatbelt profile lets the sandbox write it: the home is in the write grant,
		// and no write-protected delivery covers the directory.
		ro := macosuser.ResolveHomeReadonly(sbHome, ws, overlay.WorkspaceDirs, overlay.Dests)
		for _, p := range ro.Paths {
			if p == want || strings.HasPrefix(want, strings.TrimSuffix(p, "/")+"/") {
				t.Errorf("the Seatbelt profile write-protects %s, which covers %s", p, want)
			}
		}
		profile := macosuser.SeatbeltProfile(ws, sbHome, nil, ro)
		if !strings.Contains(profile, `(subpath "`+sbHome+`")`) {
			t.Errorf("the Seatbelt write grant does not cover the sandbox home %s", sbHome)
		}
	}
}
