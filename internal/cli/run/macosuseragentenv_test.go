package run

// macosuseragentenv_test.go pins OQ-CN9 (docs/design/provider-credential-scope.md, ruled
// 2026-09-28): a macos-user launch writes the same per-agent env files the container vehicle
// writes, into the sidecar directory the bootstrap's home layout links the sandbox's ~/.config
// to, so an agent started from a bare `yolo`'s login shell sources its profile's values. Driven
// through Run to the backend's handler seam; deleting the arm's write fails it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestMacosUserBareLaunchWritesEveryProfiledAgentsEnvFile(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["claude", "pi", "zai"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-cn9", "GH_TOKEN": "gh-cn9"}]}`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.UseProfiles = map[string]string{"pi": "zai"} // a bare `yolo`: no command, a login zsh
	ran := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, argv []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		ran = true
		if filepath.Base(argv[0]) != "zsh" {
			t.Errorf("a bare launch should start a login zsh, got %v", argv)
		}
		return 0
	}
	if rc := Run(*o); rc != 0 || !ran {
		t.Fatalf("Run() = %d (handler ran %v)\nstdout:\n%s\nstderr:\n%s", rc, ran, stdout.String(), stderr.String())
	}

	// WHERE the sandbox reads it: its ~/.config is the layout's link into the sidecar, and the
	// launchers source ~/.config/yolo-agent-env/<bin>.sh (entrypoint.AgentEnvFile).
	sidecar := paths.WorkspaceHomeState(ws)
	sandboxHome := "/Users/_yolojail"
	var configTarget string
	for _, l := range entrypoint.DeriveDarwinHomeLayout(sandboxHome, sidecar, nil, nil).Links {
		if l.Path == filepath.Join(sandboxHome, ".config") {
			configTarget = l.Target
		}
	}
	if configTarget == "" {
		t.Fatal("the darwin home layout links no ~/.config; the files have nowhere to be read from")
	}
	rel, err := filepath.Rel(filepath.Join(sandboxHome, ".config"), entrypoint.AgentEnvFile(sandboxHome, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(configTarget, rel)
	st, err := os.Stat(piFile)
	if err != nil {
		t.Fatalf("the macos-user launch wrote no env file for pi at %s: %v", piFile, err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("pi's env file is %04o, want 0600 as on podman", st.Mode().Perm())
	}
	body, _ := os.ReadFile(piFile)
	if !strings.Contains(string(body), "export ZAI_API_KEY=${ZAI_API_KEY:-'tok-cn9'}\n") {
		t.Errorf("pi's file must carry its provider's key:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(piFile), "claude.sh")); err == nil {
		t.Error("claude selected nothing and must get no file")
	}
	if strings.Contains(string(body), "gh-cn9") {
		t.Errorf("an unclaimed value rides the session, not pi's file:\n%s", body)
	}
}
