package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
)

// TestHostFileMounts covers the yolo-declared host-file mounts (hostFileArgs)
// that replaced the retired host_claude_files/host_pi_files config keys. The set
// is driven entirely by each selected agent's AgentSpec.HostFiles (a fixed
// per-agent constant), NOT by config, and NO YOLO_HOST_*_FILES env pair is
// emitted — the entrypoint re-derives the identical list from the baked
// registry.
func TestHostFileMounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude")
	piDir := filepath.Join(home, ".pi", "agent")
	for _, d := range []string{claudeDir, piDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "settings.json"), []byte(`{"y":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A hook script referenced by settings.json — D3 dropped script
	// auto-discovery, so this must NOT be mounted even though it exists.
	if err := os.WriteFile(filepath.Join(claudeDir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	o := &Options{}
	newIn := func(names ...string) *assembleInput {
		return &assembleInput{
			wsState:      filepath.Join(home, ".yolo", "home"),
			mountTargets: map[string]struct{}{},
			agentSpecs:   agents.ResolveAgents(names),
		}
	}

	// No host-file agent selected → no args.
	if got := o.hostFileArgs(newIn("copilot")); got != nil {
		t.Errorf("copilot only: want nil args, got %v", got)
	}

	// pi selected → its settings.json mounts at /ctx/host-pi/, no env pair.
	piArgs := strings.Join(o.hostFileArgs(newIn("pi")), " ")
	if !strings.Contains(piArgs, "/ctx/host-pi/settings.json") {
		t.Errorf("pi: expected settings.json mount, got %q", piArgs)
	}
	if strings.Contains(piArgs, "YOLO_HOST_PI_FILES") || strings.Contains(piArgs, "YOLO_HOST_CLAUDE_FILES") {
		t.Errorf("pi: no YOLO_HOST_*_FILES env should be emitted, got %q", piArgs)
	}

	// claude selected → its settings.json mounts at /ctx/host-claude/, no env
	// pair, and the referenced hook.sh is NOT auto-mounted (D3).
	claudeArgs := strings.Join(o.hostFileArgs(newIn("claude")), " ")
	if !strings.Contains(claudeArgs, "/ctx/host-claude/settings.json") {
		t.Errorf("claude: expected settings.json mount, got %q", claudeArgs)
	}
	if strings.Contains(claudeArgs, "hook.sh") {
		t.Errorf("claude: hook.sh must not be auto-mounted (D3 dropped script discovery), got %q", claudeArgs)
	}
	if strings.Contains(claudeArgs, "YOLO_HOST_CLAUDE_FILES") {
		t.Errorf("claude: no YOLO_HOST_CLAUDE_FILES env should be emitted, got %q", claudeArgs)
	}
}

// TestHostFileMountsSkipsAbsent confirms a declared file that does not exist on
// the host is simply skipped (no mount arg, no crash).
func TestHostFileMountsSkipsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// ~/.pi/agent exists but has no settings.json.
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Options{}
	in := &assembleInput{
		wsState:      filepath.Join(home, ".yolo", "home"),
		mountTargets: map[string]struct{}{},
		agentSpecs:   agents.ResolveAgents([]string{"pi"}),
	}
	if got := o.hostFileArgs(in); got != nil {
		t.Errorf("absent settings.json: want nil args, got %v", got)
	}
}
