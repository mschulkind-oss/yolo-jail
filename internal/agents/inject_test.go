package agents

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInjectYoloFlags(t *testing.T) {
	// gemini: --yolo injected after the binary.
	if got := InjectYoloFlags([]string{"gemini"}); !reflect.DeepEqual(got, []string{"gemini", "--yolo"}) {
		t.Errorf("gemini = %v", got)
	}
	// gemini with -y already present: no --yolo (alias suppression).
	if got := InjectYoloFlags([]string{"gemini", "-y", "chat"}); !reflect.DeepEqual(got, []string{"gemini", "-y", "chat"}) {
		t.Errorf("gemini -y = %v (should not add --yolo)", got)
	}
	// copilot: two flags, order preserved.
	if got := InjectYoloFlags([]string{"copilot", "sub"}); !reflect.DeepEqual(got, []string{"copilot", "--yolo", "--no-auto-update", "sub"}) {
		t.Errorf("copilot = %v", got)
	}
	// copilot with --yolo already present: only --no-auto-update added.
	if got := InjectYoloFlags([]string{"copilot", "--yolo"}); !reflect.DeepEqual(got, []string{"copilot", "--no-auto-update", "--yolo"}) {
		t.Errorf("copilot dup = %v", got)
	}
	// Non-agent head: unchanged.
	if got := InjectYoloFlags([]string{"bash", "-c", "echo"}); !reflect.DeepEqual(got, []string{"bash", "-c", "echo"}) {
		t.Errorf("bash = %v", got)
	}
	// Empty: unchanged.
	if got := InjectYoloFlags(nil); got != nil {
		t.Errorf("nil = %v", got)
	}
	// Input not mutated.
	in := []string{"gemini", "chat"}
	_ = InjectYoloFlags(in)
	if !reflect.DeepEqual(in, []string{"gemini", "chat"}) {
		t.Errorf("input mutated: %v", in)
	}
}

// TestInjectYoloFlagsParity cross-checks against live _inject_agent_yolo_flags.
func TestInjectYoloFlagsParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	cases := [][]string{
		{"gemini"},
		{"gemini", "-y", "chat"},
		{"copilot", "sub"},
		{"copilot", "--yolo"},
		{"claude"},
		{"claude", "--dangerously-skip-permissions"},
		{"opencode", "run"},
		{"pi"},
		{"codex", "exec"},
		{"bash", "-c", "echo hi"},
		{},
	}
	inJSON, _ := json.Marshal(cases)
	script := `
import sys, json; sys.path.insert(0, 'src')
from cli.run_cmd import _inject_agent_yolo_flags
out = []
for c in json.loads(sys.argv[1]):
    cmd = list(c)
    _inject_agent_yolo_flags(cmd)
    out.append(cmd)
print(json.dumps(out))
`
	outBytes, err := py("-c", script, string(inJSON)).Output()
	if err != nil {
		t.Skipf("python run_cmd import failed: %v", err)
	}
	var want [][]string
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, c := range cases {
		got := InjectYoloFlags(c)
		// normalize nil vs empty for the {} case
		if len(got) == 0 && len(want[i]) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("case %v: go=%v py=%v", c, got, want[i])
		}
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
