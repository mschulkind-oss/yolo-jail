package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestAppleContainerMaterializesSingleFiles is the regression for the AC
// single-file-mount workaround (run_cmd.py:2063 + 2881): on runtime=container,
// yolo-user-env.sh and each agent's briefing must be COPIED into ws_state (so
// the ws_state:/home/agent parent mount exposes them) rather than bind-mounted
// as single files (which Apple Container silently drops). A prior version only
// handled the rt!="container" mount branch, so on AC every env_sources var and
// every briefing silently vanished.
func TestAppleContainerMaterializesSingleFiles(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-stage the source files the assembler materializes.
	userEnv := filepath.Join(wsState, "yolo-user-env.sh")
	if err := os.WriteFile(userEnv, []byte("export FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(ws, "agents")
	specs := agents.ResolveAgents([]string{"claude"})
	if err := os.MkdirAll(agentsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	stagedBriefing := filepath.Join(agentsPath, specs[0].Briefing.Staging)
	if err := os.MkdirAll(filepath.Dir(stagedBriefing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedBriefing, []byte("# briefing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("agents", []any{"claude"}, "security", sec)

	in := &assembleInput{
		cfg:          cfg,
		rt:           "container",
		cname:        "yolo-ws-abcd1234",
		repoRoot:     "/repo",
		agentsList:   []string{"claude"},
		agentSpecs:   specs,
		agentsPath:   agentsPath,
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}

	got := o.assembleRunCmd(in)
	joined := strings.Join(got, " ")

	// 1. yolo-user-env.sh copied under ws_state at the expected rel path.
	materializedEnv := filepath.Join(wsState, ".config", "yolo-user-env.sh")
	if b, err := os.ReadFile(materializedEnv); err != nil || string(b) != "export FOO=bar\n" {
		t.Errorf("yolo-user-env.sh not materialized into ws_state: err=%v content=%q", err, string(b))
	}
	// 2. briefing copied under ws_state at spec.Briefing.Mount.
	materializedBrief := filepath.Join(wsState, specs[0].Briefing.Mount)
	if b, err := os.ReadFile(materializedBrief); err != nil || string(b) != "# briefing\n" {
		t.Errorf("briefing not materialized into ws_state: err=%v content=%q", err, string(b))
	}
	// 3. NO single-file -v mount for either (that's the AC bug being avoided).
	if strings.Contains(joined, "yolo-user-env.sh:/home/agent") {
		t.Errorf("AC path must NOT single-file-mount yolo-user-env.sh: %v", got)
	}
	if strings.Contains(joined, ":/home/agent/"+specs[0].Briefing.Mount+":ro") {
		t.Errorf("AC path must NOT single-file-mount the briefing: %v", got)
	}
}
