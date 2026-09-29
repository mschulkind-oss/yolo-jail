package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

// claudeAuthHome points the broker's whole store at a temp HOME for one test: the canonical
// login, the view registrations, the shared credentials file and the relay source. It restores
// the store's package state when the test ends, because the verb configures it.
func claudeAuthHome(t *testing.T) (home, canonical, legacy string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	state := filepath.Join(home, "broker-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_BROKER_STATE_DIR", state)
	saved := []string{oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath,
		oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath}
	t.Cleanup(func() {
		oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath = saved[0], saved[1]
		oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath = saved[2], saved[3]
	})
	oauthbroker.ConfigureStore()
	legacy = oauthbroker.LegacyCredsPath()
	if !strings.HasPrefix(legacy, home) {
		t.Fatalf("the shared credentials file escaped the temp HOME: %s", legacy)
	}
	return home, oauthbroker.CanonicalPath, legacy
}

func writeCreds(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeAuthLogoutSignsTheMachineOut(t *testing.T) {
	home, canonical, legacy := claudeAuthHome(t)
	exp := time.Now().Add(7 * time.Hour).UnixMilli()
	login := `{"claudeAiOauth":{"accessToken":"AT_SECRET","refreshToken":"RT_SECRET","expiresAt":` +
		itoa64(exp) + `,"scopes":["user:inference"]}}`
	writeCreds(t, canonical, login)
	writeCreds(t, legacy, login)
	ws := filepath.Join(home, "code", "alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := claudeview.Location{Workspace: ws, Subdir: "claude"}
	if _, err := oauthbroker.RegisterView(loc, "podman", "yolo-alpha"); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if rc := claudeAuthMain([]string{"logout"}, false, &out, &errOut); rc != 0 {
		t.Fatalf("logout rc = %d\n%s%s", rc, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "MACHINE-WIDE") {
		t.Errorf("logout did not say its scope before acting:\n%s", errOut.String())
	}
	if _, err := os.Stat(canonical); !os.IsNotExist(err) {
		t.Errorf("the canonical login survived `yolo claude-auth logout`: %v", err)
	}
	for _, p := range []string{legacy, loc.Path()} {
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), "claudeAiOauth") {
			t.Errorf("%s still holds a login after logout:\n%s", p, data)
		}
	}

	// Idempotent, and it says so.
	out.Reset()
	if rc := claudeAuthMain([]string{"logout"}, false, &out, &errOut); rc != 0 {
		t.Fatalf("a second logout rc = %d", rc)
	}
	if !strings.Contains(out.String(), "already signed out") {
		t.Errorf("a second logout did not say the machine was already signed out:\n%s", out.String())
	}

	// Refused in a jail, where it would reach only a nested broker.
	if rc := claudeAuthMain([]string{"logout"}, true, &out, &errOut); rc != 2 {
		t.Errorf("logout inside a jail rc = %d, want the refusal 2", rc)
	}
}

func TestClaudeAuthStatusAndInspectNeverPrintAToken(t *testing.T) {
	_, canonical, legacy := claudeAuthHome(t)
	exp := time.Now().Add(7 * time.Hour).UnixMilli()
	login := `{"claudeAiOauth":{"accessToken":"AT_SECRET","refreshToken":"RT_SECRET","expiresAt":` +
		itoa64(exp) + `,"scopes":["user:inference"],"subscriptionType":"team"},"mcpOAuth":{"s":{"accessToken":"MCP_SECRET"}}}`
	writeCreds(t, canonical, login)
	writeCreds(t, legacy, login)

	for _, args := range [][]string{{"status"}, {"inspect", legacy}} {
		var out, errOut bytes.Buffer
		if rc := claudeAuthMain(args, false, &out, &errOut); rc != 0 {
			t.Fatalf("%v rc = %d: %s", args, rc, errOut.String())
		}
		text := out.String() + errOut.String()
		for _, secret := range []string{"AT_SECRET", "RT_SECRET", "MCP_SECRET"} {
			if strings.Contains(text, secret) {
				t.Errorf("`yolo claude-auth %s` printed a token:\n%s", strings.Join(args, " "), text)
			}
		}
		if !strings.Contains(text, "refresh token: PRESENT") || !strings.Contains(text, "expires:") {
			t.Errorf("`yolo claude-auth %s` did not describe the login:\n%s", strings.Join(args, " "), text)
		}
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
