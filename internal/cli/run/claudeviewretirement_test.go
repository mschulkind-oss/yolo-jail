package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

// Drive the production launch lifecycle with only private synthetic credentials.
// Off must stop the broker maintaining a view before the interception layout replaces it.
func TestClaudeViewOnToOffRetiresProductionRegistration(t *testing.T) {
	brokerFixtureDirs(t, true)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	state := filepath.Join(home, "broker")
	t.Setenv("YOLO_BROKER_STATE_DIR", state)
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	oldLock, oldCanonical, oldRegistry, oldRelay := oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath, oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath
	t.Cleanup(func() {
		oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath, oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath = oldLock, oldCanonical, oldRegistry, oldRelay
	})
	oauthbroker.ConfigureStore()
	if err := os.WriteFile(oauthbroker.CanonicalPath, []byte(`{"claudeAiOauth":{"accessToken":"PRIVATE_FAKE_AT","refreshToken":"PRIVATE_FAKE_RT","expiresAt":4102444800000,"scopes":["user:inference"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(home, "workspace")
	o := goldenOptions(ws, home)
	o.Getenv = viewEnv("1")
	o.registerClaudeCredentialView("podman", "yolo-synthetic", jsonx.NewOrderedMap())
	loc := claudeview.Location{Workspace: ws, Subdir: "claude"}
	data, err := os.ReadFile(loc.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "PRIVATE_FAKE_AT") || strings.Contains(string(data), "PRIVATE_FAKE_RT") {
		t.Fatalf("view was not access-token-only: %s", data)
	}
	entries, err := os.ReadDir(oauthbroker.ViewRegistryDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("on registration: entries=%v err=%v", entries, err)
	}
	// The Off transition runs through the actual keeper production caller, not a
	// substitute retirement seam. Removing that caller must make this test fail.
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		p.Workspace = ws
	}, func(o *Options) {
		o.Getenv = viewEnv("0")
	})
	if !f.relayWithin(30 * time.Second) {
		t.Fatalf("keeper refused Off launch: %s", f.errOut.String())
	}
	_ = f.lifeW.Close()
	if rc := f.wait(); rc != 0 {
		t.Fatalf("keeper ended %d", rc)
	}
	entries, err = os.ReadDir(oauthbroker.ViewRegistryDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("view-off production launch left %d stale registrations active", len(entries))
	}
}

func TestClaudeViewOffRetiresResolvedRuntimeLocationNonfatally(t *testing.T) {
	for _, tc := range []struct {
		name string
		rt   string
		mode macosuser.Mode
		want string
	}{
		{name: "podman", rt: "podman", want: "claude"},
		{name: "container", rt: "container", want: ".claude"},
		{name: "macos-user account", rt: "macos-user", mode: macosuser.ModeAccount, want: ".claude"}, // MMP-D25
		{name: "macos-user self", rt: "macos-user", mode: macosuser.ModeSelf, want: ".claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := goldenOptions(filepath.Join(t.TempDir(), "workspace"), t.TempDir())
			o.Getenv = viewEnv("0")
			o.MacosUserMode = tc.mode
			o.MacosUserHostHome = "/Users/synthetic"
			var stderr strings.Builder
			o.Stderr = &stderr
			var got claudeview.Location
			old := retireView
			retireView = func(loc claudeview.Location) (bool, error) {
				got = loc
				return false, os.ErrPermission
			}
			t.Cleanup(func() { retireView = old })
			o.registerClaudeCredentialView(tc.rt, "yolo-synthetic", jsonx.NewOrderedMap())
			if got.Workspace != o.Workspace || got.Subdir != tc.want {
				t.Fatalf("retired location = %+v, want workspace %q subdir %q", got, o.Workspace, tc.want)
			}
			message := stderr.String()
			for _, want := range []string{"Warning", "yolo claude-auth status", "relaunch", "permission denied"} {
				if !strings.Contains(message, want) {
					t.Errorf("nonfatal retirement warning omits %q: %s", want, message)
				}
			}
		})
	}
}
