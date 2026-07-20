package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostPiFileArgs covers the host ~/.pi/agent file mount (run_cmd.py:2851):
// mounted only when pi is a selected agent, defaulting to settings.json, with
// the YOLO_HOST_PI_FILES env carrying the JSON list of mounted names. No script
// auto-discovery (unlike claude).
func TestHostPiFileArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	piDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "settings.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Options{}
	in := &assembleInput{wsState: filepath.Join(home, ".yolo", "home"), mountTargets: map[string]struct{}{}}

	// pi NOT selected → no args at all.
	if got := o.hostPiFileArgs(newConfig(), &assembleInput{
		wsState: in.wsState, mountTargets: in.mountTargets, agentsList: []string{"claude"},
	}); got != nil {
		t.Errorf("pi not selected: want nil args, got %v", got)
	}

	// pi selected, default host_pi_files → settings.json mounted + env.
	in.agentsList = []string{"pi"}
	args := o.hostPiFileArgs(newConfig(), in)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/ctx/host-pi/settings.json") {
		t.Errorf("expected settings.json mount, got: %v", args)
	}
	if !strings.Contains(joined, `YOLO_HOST_PI_FILES=["settings.json"]`) {
		t.Errorf("expected YOLO_HOST_PI_FILES env, got: %v", args)
	}

	// A configured filename that does not exist is skipped (no env when nothing
	// mounts).
	cfg := newConfig("host_pi_files", []any{"does-not-exist.json"})
	if got := o.hostPiFileArgs(cfg, in); got != nil {
		t.Errorf("nonexistent file: want nil args, got %v", got)
	}
}

// TestHostPiFileArgsMultiple confirms multiple existing files mount in order and
// the env lists exactly those mounted.
func TestHostPiFileArgsMultiple(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	piDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"settings.json", "memory.json"} {
		if err := os.WriteFile(filepath.Join(piDir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := &Options{}
	in := &assembleInput{
		wsState: filepath.Join(home, ".yolo", "home"), mountTargets: map[string]struct{}{},
		agentsList: []string{"pi"},
	}
	cfg := newConfig("host_pi_files", []any{"settings.json", "memory.json", "absent.json"})
	args := o.hostPiFileArgs(cfg, in)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `YOLO_HOST_PI_FILES=["settings.json", "memory.json"]`) {
		t.Errorf("env should list only the two existing files, got: %v", args)
	}
}
