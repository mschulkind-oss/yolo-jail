package run

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func shippedPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "packs", name))
	if err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, name)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	return pack
}

func onceLaunchInput(t *testing.T, effective, user string) LaunchInputContext {
	t.Helper()
	e, err := jsonx.Decode([]byte(effective))
	if err != nil {
		t.Fatal(err)
	}
	u, err := jsonx.Decode([]byte(user))
	if err != nil {
		t.Fatal(err)
	}
	em, um := e.(*jsonx.OrderedMap), u.(*jsonx.OrderedMap)
	return LaunchInputContext{Container: "jail-synthetic", Workspace: t.TempDir(), Runtime: "podman", MCPProven: true,
		EffectiveMCPServers: em, UserOnlyMCPServers: um, MCPScopes: packload.MCPServerScopes(em, um),
		SharedCredentialDir: filepath.Join(paths.GlobalHome(), ".claude-shared-credentials")}
}

// §16 item 4: a user-level `sign_in = "once"` server, with Claude bridged to the machine's
// shared credential file, hands the broker that server and that file, and nothing else.
func TestMCPAuthShareCarriesTheOneSignInServerAndClaudesMachineStore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the one-sign-in tier runs on Linux podman")
	}
	t.Setenv("HOME", t.TempDir())
	slack := `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`
	in := onceLaunchInput(t, slack, slack)
	data, err := mcpShareFromPacks(in, []*packload.Pack{shippedPack(t, "claude")})
	if err != nil {
		t.Fatal(err)
	}
	share, err := mcpauth.DecodeShare(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(share.Once) != 1 || share.Once[0].Name != "slack" || share.Once[0].URL != "https://mcp.slack.com/mcp" {
		t.Fatalf("once = %+v", share.Once)
	}
	if len(share.Sources) != 1 {
		t.Fatalf("sources = %+v", share.Sources)
	}
	src := share.Sources[0]
	if src.Pack != "claude" || src.Dir != in.SharedCredentialDir || src.Store != ".credentials.json" || src.LockDir != ".storage-write.lock" {
		t.Fatalf("source = %+v", src)
	}
	if len(share.Replicas) != 0 {
		t.Fatalf("Claude's proper-lockfile store became a per-workspace replica: %+v", share.Replicas)
	}
}

func TestMCPAuthShareHasNoOneSignInWithoutTheUsersSay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the one-sign-in tier runs on Linux podman")
	}
	t.Setenv("HOME", t.TempDir())
	claude := shippedPack(t, "claude")
	for _, tc := range []struct {
		name            string
		effective, user string
		bridge          bool
	}{
		{"no sign_in", `{"slack":{"url":"https://mcp.slack.com/mcp"}}`, `{"slack":{"url":"https://mcp.slack.com/mcp"}}`, true},
		{"per_agent", `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"per_agent"}}`, `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"per_agent"}}`, true},
		// A workspace-composed server is not the user's, whatever it says (MA-D11).
		{"workspace scope", `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`, `{}`, true},
		// The credential view gives each workspace its own Claude store (slice 2).
		{"credential view", `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`, `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := onceLaunchInput(t, tc.effective, tc.user)
			if !tc.bridge {
				in.SharedCredentialDir = ""
			}
			data, err := mcpShareFromPacks(in, []*packload.Pack{claude})
			if err != nil {
				t.Fatal(err)
			}
			share, err := mcpauth.DecodeShare(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(share.Once) != 0 && len(share.Sources) != 0 {
				t.Fatalf("share = once %+v, sources %+v; want no one-sign-in pair", share.Once, share.Sources)
			}
		})
	}
}

// On a backend with no broker, Claude's machine-wide store is still one file every jail reads,
// so the launch must not claim each workspace signs in on its own.
func TestMCPUnavailableLineForAMachineWideStore(t *testing.T) {
	lines := mcpUnavailableLines([]*packload.Pack{shippedPack(t, "claude")}, "Apple Container")
	if len(lines) != 1 || !strings.Contains(lines[0], "refreshed by claude itself on Apple Container") || strings.Contains(lines[0], "each workspace signs in") {
		t.Fatalf("lines = %q", lines)
	}
}

// The launch says what it shares: the one-sign-in servers, and (OQ-MA3) that Claude's MCP
// sign-ins live in one machine-wide file.
func TestMCPAuthNoticeNamesTheOneSignInServers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the one-sign-in tier runs on Linux podman")
	}
	t.Setenv("HOME", t.TempDir())
	slack := `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`
	in := onceLaunchInput(t, slack, slack)
	data, err := mcpShareFromPacks(in, []*packload.Pack{shippedPack(t, "claude")})
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr}
	o.noteMCPAuthShare(in, data)
	out := stderr.String()
	for _, want := range []string{"slack", "signs in once", "yolo audit --service mcp-auth", "every jail on this machine"} {
		if !strings.Contains(out, want) {
			t.Errorf("notice %q lacks %q", out, want)
		}
	}
}

// Run freezes the machine-wide Claude store this launch bridges into the keeper's plan, so the
// broker's share can name it as the one-sign-in source (ID-16). Deleting the assignment in
// freezeLaunchMCP fails here.
func TestRunFreezesClaudesBridgedStoreForTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the one-sign-in tier runs on Linux podman")
	}
	home := packHome(t)
	manifest := `{"name":"fixture-input","description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["true","{launch_input}","{socket}"],"publishes":"socket","launch_input":"fixture-input"}}`
	writeLocalLoopholePack(t, home, "fixture-input", manifest)
	writeUserConfigJSON(t, home, `{"packs":["claude"],"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
	workspace := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, workspace, "podman", &stdout, &stderr, nil)
	o.launchInputProducers = map[string]LaunchInputProducer{"fixture-input": {
		StateDir: func(LaunchInputContext) string { return t.TempDir() },
		Produce:  func(LaunchInputContext) ([]byte, error) { return []byte("{}"), nil },
	}}
	o.AcceptConfigChanges = true
	o.NeverAttach = true
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	var plan *keeperPlan
	previousSpawner := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = previousSpawner })
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
		var err error
		plan, err = readKeeperPlan(planPath)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("fixture stopped before container spawn")
	}
	if rc := Run(*o); rc == 0 || plan == nil {
		t.Fatalf("Run did not reach the keeper plan (rc=%d): %s %s", rc, stdout.String(), stderr.String())
	}
	want := filepath.Join(paths.GlobalHome(), ".claude-shared-credentials")
	if plan.LaunchInputMCP == nil || !plan.LaunchInputMCP.Proven || plan.LaunchInputMCP.SharedCredentialDir != want {
		t.Fatalf("plan's frozen MCP input = %+v, want the bridged store %s", plan.LaunchInputMCP, want)
	}
}
