package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const hostOnceSlackConfig = `{"packs": ["claude"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp", "sign_in": "once"}}}`

// recordHostOnce stands in for the session's poll: it records each session started, and whether
// its stop (the last pass) ran.
func recordHostOnce(t *testing.T) (started *[]*run.OnceSession, stopped *int) {
	t.Helper()
	started, stopped = new([]*run.OnceSession), new(int)
	prev := hostOnceStart
	t.Cleanup(func() { hostOnceStart = prev })
	hostOnceStart = func(s *run.OnceSession) func() {
		*started = append(*started, s)
		return func() { *stopped++ }
	}
	return started, stopped
}

func setHostOnceGOOS(t *testing.T, goos string) {
	t.Helper()
	// A jail's own Claude bridge variable would point the program elsewhere than ~/.claude.
	t.Setenv(claudeview.SecureStorageEnv, "")
	prev := hostOnceGOOS
	t.Cleanup(func() { hostOnceGOOS = prev })
	hostOnceGOOS = goos
}

// ID-21 (OQ-MA10, ruled on by default): `yolo host -- claude` on Linux keeps a sign_in = "once"
// server for the session's length on the real home's ~/.claude store, so it stays resident
// rather than exec'ing the program, and runs the last pass once the program has exited. Deleting
// the host launch's call to the session, or the resident branch, fails here.
func TestHostLaunchOnLinuxKeepsTheOneSignInForTheSession(t *testing.T) {
	setHostOnceGOOS(t, "linux")
	started, stopped := recordHostOnce(t)
	rc, env, errs := hostGateRunIn(t, hostOnceSlackConfig, nil, nil, "claude", nil)
	if rc != 0 {
		t.Fatalf("rc = %d:\n%s", rc, errs)
	}
	if env != nil {
		t.Fatalf("the launch exec'd the program, so nothing kept the sign-in while it ran:\n%s", errs)
	}
	if len(*started) != 1 || *stopped != 1 {
		t.Fatalf("sessions started = %d, stopped = %d; want one, ended after the program\n%s", len(*started), *stopped, errs)
	}
	want := filepath.Join(paths.Home(), ".claude", ".credentials.json")
	if got := (*started)[0].Stores(); len(got) != 1 || got[0] != want {
		t.Fatalf("the session's store = %q, want the real home's %s", got, want)
	}
	if (*started)[0].Names() != "slack" {
		t.Fatalf("the session keeps %q, want slack", (*started)[0].Names())
	}
	for _, want := range []string{"MCP one sign-in: slack signs in once", "~/.claude/.credentials.json", "yolo audit --service mcp-auth"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host launch does not say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "is not kept") {
		t.Errorf("the Linux launch still says the tier is not kept:\n%s", errs)
	}
}

// On a Mac, host Claude keeps its sign-ins in the Keychain, so the tier is not kept, the launch
// says so naming the Keychain and the jail launch that keeps it, and the program is exec'd as
// before. Deleting the darwin line fails here.
func TestHostLaunchOnAMacSaysTheOneSignInIsNotKeptHere(t *testing.T) {
	setHostOnceGOOS(t, "darwin")
	started, _ := recordHostOnce(t)
	rc, env, errs := hostGateRunIn(t, hostOnceSlackConfig, nil, nil, "claude", nil)
	if rc != 0 {
		t.Fatalf("rc = %d:\n%s", rc, errs)
	}
	if env == nil || len(*started) != 0 {
		t.Fatalf("a Mac launch started a session or did not exec:\n%s", errs)
	}
	for _, want := range []string{"MCP one sign-in is not kept at yolo host on macOS: slack", "Keychain", "`yolo -- claude`"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host launch does not say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "OQ-MA10") {
		t.Errorf("the line still calls the ruling pending:\n%s", errs)
	}
}

// Without a one-sign-in server, or for a program whose pack keeps no MCP sign-ins, nothing is
// said, nothing is started, and the program is exec'd.
func TestHostLaunchIsSilentWithoutAOneSignInServer(t *testing.T) {
	setHostOnceGOOS(t, "linux")
	for name, cfg := range map[string]string{
		"no once server": `{"packs": ["claude"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp"}}}`,
		"pi":             `{"packs": ["claude", "pi"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp", "sign_in": "once"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			started, _ := recordHostOnce(t)
			agent := "claude"
			if name == "pi" {
				agent = "pi"
			}
			_, env, errs := hostGateRunIn(t, cfg, nil, nil, agent, nil)
			if strings.Contains(errs, "MCP one sign-in") {
				t.Errorf("the line was printed:\n%s", errs)
			}
			if env == nil || len(*started) != 0 {
				t.Errorf("a session started or the program was not exec'd:\n%s", errs)
			}
		})
	}
}

// Under the Claude credential view the program keeps its sign-ins in the managed view directory,
// not ~/.claude, so the tier is not kept there, as in a jail's credential-view mode (ID-16).
// Deleting the view check fails here.
func TestHostOnceSignInIsNotKeptUnderTheCredentialView(t *testing.T) {
	setHostOnceGOOS(t, "linux")
	cfg, err := jsonx.Decode([]byte(hostOnceSlackConfig))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "packs", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	claude, problems := packload.LoadDirForUse(root, "claude")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	launch := &hostComposition{agent: "claude", cfg: cfg.(*jsonx.OrderedMap), packs: []*packload.Pack{claude}, workspace: t.TempDir()}
	var errs strings.Builder
	environ := []string{claudeview.SecureStorageEnv + "=/state/host-agents/claude"}
	if s := hostOnceSignIn(launch, environ, nil, &errs); s != nil {
		t.Fatal("a session was started under the credential view")
	}
	if !strings.Contains(errs.String(), "not kept for this launch") || !strings.Contains(errs.String(), claudeview.SwitchEnv) {
		t.Errorf("the launch does not say why the tier is not kept:\n%s", errs.String())
	}
	// Without the view's variable, the same composition keeps it.
	errs.Reset()
	if s := hostOnceSignIn(launch, nil, nil, &errs); s == nil {
		t.Fatalf("no session without the view:\n%s", errs.String())
	}
}
