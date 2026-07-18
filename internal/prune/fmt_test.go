package prune

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFmtBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1023:       "1023 B",
		1024:       "1.0 KiB",
		1536:       "1.5 KiB",
		1500000000: "1.4 GiB",
		1 << 40:    "1.0 TiB",
		1 << 50:    "1024.0 TiB", // capped at TiB
	}
	for in, want := range cases {
		if got := FmtBytes(in); got != want {
			t.Errorf("FmtBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestFmtBytesParity cross-checks FmtBytes against live prune_cmd._fmt_bytes
// across a spread of magnitudes. Skips without Python.
func TestFmtBytesParity(t *testing.T) {
	py := pythonRunnerFmt(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	inputs := []int64{0, 1, 512, 1023, 1024, 1536, 999999, 1500000000, 1 << 40, (1 << 40) * 3, 1 << 50}
	inJSON, _ := json.Marshal(inputs)
	script := `
import sys, json; sys.path.insert(0, 'src')
from cli.prune_cmd import _fmt_bytes
xs = json.loads(sys.argv[1])
print(json.dumps([_fmt_bytes(x) for x in xs]))
`
	out, err := py("-c", script, string(inJSON)).Output()
	if err != nil {
		t.Skipf("python prune_cmd import failed: %v", err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, in := range inputs {
		if got := FmtBytes(in); got != want[i] {
			t.Errorf("FmtBytes(%d) go=%q py=%q", in, got, want[i])
		}
	}
}

func pythonRunnerFmt(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRootFmt(t)
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

func repoRootFmt(t *testing.T) string {
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
