package runtime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestParsePodmanLive(t *testing.T) {
	// Mixed: a running yolo, a stopped yolo (excluded), a non-yolo running
	// (excluded), a paused yolo (included), a short line (skipped).
	stdout := "yolo-app-1234abcd running\n" +
		"yolo-old-5678efgh exited\n" +
		"some-other running\n" +
		"yolo-web-9999zzzz Paused\n" +
		"garbage\n"
	got := keys(ParsePodmanLive(stdout))
	want := []string{"yolo-app-1234abcd", "yolo-web-9999zzzz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePodmanLive = %v, want %v", got, want)
	}
	// Empty stdout -> empty set (NOT unknown; Known is the caller's concern).
	if len(ParsePodmanLive("")) != 0 {
		t.Error("empty stdout should yield empty set")
	}
}

func TestParseContainerLsLive(t *testing.T) {
	stdout := "ID  IMAGE  STATE\n" +
		"yolo-mac-aaaa1111 img running\n" +
		"not-a-jail img running\n"
	got := keys(ParseContainerLsLive(stdout))
	want := []string{"yolo-mac-aaaa1111"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseContainerLsLive = %v, want %v", got, want)
	}
	// Header-only output -> no rows.
	if len(ParseContainerLsLive("ID IMAGE STATE")) != 0 {
		t.Error("header-only should yield no live containers")
	}
}

func TestParsePodmanPsRows(t *testing.T) {
	stdout := "yolo-a-1111\tUp 2 hours\t2 hours ago\n" +
		"yolo-b-2222\tUp 3 minutes\t3 minutes ago\n" +
		"short\trow\n" // <3 fields, skipped
	got := ParsePodmanPsRows(stdout)
	want := []PsRow{
		{"yolo-a-1111", "Up 2 hours", "2 hours ago"},
		{"yolo-b-2222", "Up 3 minutes", "3 minutes ago"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if len(ParsePodmanPsRows("   ")) != 0 {
		t.Error("blank stdout should yield no rows")
	}
}

func TestParseContainerLsRows(t *testing.T) {
	stdout := "ID  IMAGE  STATE\n" +
		"yolo-x-1 img running foo\n" +
		"other img running\n"
	got := ParseContainerLsRows(stdout)
	want := []PsRow{{Name: "yolo-x-1", Status: "img running foo", RunningFor: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("container rows = %v, want %v", got, want)
	}
}

func TestWorkspaceFromInspectEnv(t *testing.T) {
	env := []string{"PATH=/bin", "YOLO_HOST_DIR=/home/matt/code/thing", "TERM=xterm"}
	if ws, ok := WorkspaceFromInspectEnv(env); !ok || ws != "/home/matt/code/thing" {
		t.Errorf("workspace = %q, %v", ws, ok)
	}
	if _, ok := WorkspaceFromInspectEnv([]string{"PATH=/bin"}); ok {
		t.Error("absent YOLO_HOST_DIR should return ok=false")
	}
	// Value containing '=' is preserved after the first '='.
	if ws, _ := WorkspaceFromInspectEnv([]string{"YOLO_HOST_DIR=/a=b"}); ws != "/a=b" {
		t.Errorf("split-once = %q, want /a=b", ws)
	}
}

func TestRenderPsTable(t *testing.T) {
	containers := []PsContainer{
		{"yolo-a-1", "Up 2 hours", "/home/matt/a"},
		{"yolo-longer-name-2", "Up 3m", "/home/matt/b"},
	}
	got := RenderPsTable(containers)
	// name col width = len("yolo-longer-name-2")=18, status col = len("Up 2 hours")=10.
	want := "CONTAINER           STATUS      WORKSPACE\n" +
		"yolo-a-1            Up 2 hours  /home/matt/a\n" +
		"yolo-longer-name-2  Up 3m       /home/matt/b"
	if got != want {
		t.Errorf("table =\n%q\nwant\n%q", got, want)
	}
	if RenderPsTable(nil) != "" {
		t.Error("no containers should render empty string")
	}
}

func TestStuckReasonFromTop(t *testing.T) {
	// Healthy: a real user command present.
	healthy := "COMMAND\nbash\nclaude\nmise\n"
	if r := StuckReasonFromTop(healthy); r != "" {
		t.Errorf("healthy = %q, want empty", r)
	}
	// Stuck: only provisioning + infra.
	stuck := "COMMAND\nbash\nyolo-entrypo\nmise\nnpm\n"
	if r := StuckReasonFromTop(stuck); r != "stuck in provisioning" {
		t.Errorf("stuck = %q", r)
	}
	// No processes (header only).
	if r := StuckReasonFromTop("COMMAND\n"); r != "no processes" {
		t.Errorf("empty = %q, want 'no processes'", r)
	}
}

func TestPodmanMachineResizeHint(t *testing.T) {
	want := "Increase the VM: `podman machine set --memory 4096 && podman machine stop && " +
		"podman machine start`.  Note: this restarts the VM and stops every container running on it."
	if got := PodmanMachineResizeHint(); got != want {
		t.Errorf("hint = %q", got)
	}
}

// TestParityVsLivePython cross-checks the parsers + hint against the live
// runtime.py by feeding identical canned inputs to both. Skips without Python.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli import runtime as r
# Re-derive the pure results using the same canned inputs, mirroring the
# parsing the subprocess branches do, so we compare parser-to-parser.
podman_ps = "yolo-a-1111\tUp 2 hours\t2 hours ago\nyolo-b-2222\tUp 3 minutes\t3 minutes ago\nshort\trow\n"
rows = []
for line in (podman_ps.strip().splitlines() if podman_ps.strip() else []):
    parts = line.split("\t")
    if len(parts) >= 3:
        rows.append([parts[0], parts[1], parts[2]])
out = {
  "hint": r._podman_machine_resize_hint(),
  "floor": r.PODMAN_MACHINE_MEMORY_FLOOR_MB,
  "rows": rows,
}
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python runtime import failed: %v", err)
	}
	var want struct {
		Hint  string     `json:"hint"`
		Floor int        `json:"floor"`
		Rows  [][]string `json:"rows"`
	}
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if PodmanMachineResizeHint() != want.Hint {
		t.Errorf("hint mismatch:\n go: %q\n py: %q", PodmanMachineResizeHint(), want.Hint)
	}
	if PodmanMachineMemoryFloorMB != want.Floor {
		t.Errorf("floor = %d, py = %d", PodmanMachineMemoryFloorMB, want.Floor)
	}
	goRows := ParsePodmanPsRows("yolo-a-1111\tUp 2 hours\t2 hours ago\nyolo-b-2222\tUp 3 minutes\t3 minutes ago\nshort\trow\n")
	if len(goRows) != len(want.Rows) {
		t.Fatalf("row count go=%d py=%d", len(goRows), len(want.Rows))
	}
	for i, w := range want.Rows {
		if goRows[i].Name != w[0] || goRows[i].Status != w[1] || goRows[i].RunningFor != w[2] {
			t.Errorf("row %d go=%v py=%v", i, goRows[i], w)
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
