package agentsmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoopholeFirst(t *testing.T) {
	cases := map[string]string{
		"audio. Second sentence here":    "audio",
		"single line no period":          "single line no period",
		"first line\nsecond line":        "first line",
		"trailing dots...":               "trailing dots",
		"":                               "",
		"PipeWire pass-through. More.\n": "PipeWire pass-through",
	}
	for in, want := range cases {
		if got := loopholeFirst(in); got != want {
			t.Errorf("loopholeFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeBriefing(t *testing.T) {
	if got := ComposeBriefing("body\n", ""); got != "body\n" {
		t.Errorf("no extra = %q", got)
	}
	if got := ComposeBriefing("body\n", "  extra  \n\n"); got != "body\n\n  extra\n" {
		t.Errorf("with extra = %q", got)
	}
}

func TestWriteBriefingBreaksHardlink(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	must(t, os.WriteFile(a, []byte("shared"), 0o644))
	must(t, os.Link(a, b)) // a and b now share an inode (nlink=2)

	// Writing b must break the link (fresh inode), leaving a untouched.
	must(t, WriteBriefing(b, "new b content"))
	if data, _ := os.ReadFile(a); string(data) != "shared" {
		t.Errorf("a clobbered through hardlink: %q", data)
	}
	if data, _ := os.ReadFile(b); string(data) != "new b content" {
		t.Errorf("b content = %q", data)
	}
	// Single-linked file: in-place write preserves the inode.
	ino1 := inodeOf(t, b)
	must(t, WriteBriefing(b, "again"))
	if inodeOf(t, b) != ino1 {
		t.Error("single-linked write should preserve inode")
	}
}

func TestWorkspaceIsYoloSourceTree(t *testing.T) {
	// A non-yolo dir.
	dir := t.TempDir()
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("empty dir is not a yolo source tree")
	}
	// The real repo root IS one.
	root := repoRoot(t)
	if !WorkspaceIsYoloSourceTree(root) {
		t.Error("repo root should be recognized as a yolo source tree")
	}
	// src present but foreign pyproject -> false.
	must(t, os.MkdirAll(filepath.Join(dir, "src", "cli"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "src", "cli", "__init__.py"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"other\"\n"), 0o644))
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("foreign project name should not match")
	}
}

// TestBriefingContentParity byte-diffs BriefingContent against live
// generate_agents_md across several config permutations. Skips without Python.
func TestBriefingContentParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}

	cases := []struct {
		name string
		in   BriefingInput
		// pyKwargs is the Python call reproducing the same inputs.
		pyKwargs string
	}{
		{
			name: "minimal",
			in:   BriefingInput{Workspace: "/home/matt/proj", NetMode: "bridge"},
			pyKwargs: `workspace=Path("/home/matt/proj"), blocked_tools=[], mount_descriptions=[],
			  net_mode="bridge", runtime="podman"`,
		},
		{
			name: "full",
			in: BriefingInput{
				Workspace: "/home/matt/proj",
				NetMode:   "bridge",
				BlockedTools: []BlockedTool{
					{Name: "grep", Message: "grep's recursive mode is blocked.", Suggestion: "rg <pattern>"},
					{Name: "find", Message: "", Suggestion: ""},
				},
				MountDescriptions: []string{"/host/data:/ctx/data", "/plainonly"},
				ForwardHostPorts:  []any{jsonx.IntValue(8000), "9000", "1234:5678"},
				Loopholes: []Loophole{
					{Name: "audio", Desc: "PipeWire pass-through. Enables mic."},
					{Name: "host-processes", Desc: ""},
				},
				Resources: map[string]any{"memory": "8g", "cpus": jsonx.IntValue(4)},
			},
			pyKwargs: `workspace=Path("/home/matt/proj"),
			  blocked_tools=[{"name":"grep","message":"grep's recursive mode is blocked.","suggestion":"rg <pattern>"},{"name":"find"}],
			  mount_descriptions=["/host/data:/ctx/data", "/plainonly"],
			  net_mode="bridge", runtime="podman",
			  forward_host_ports=[8000, "9000", "1234:5678"],
			  loopholes=[("audio","PipeWire pass-through. Enables mic."),("host-processes","")],
			  resources={"memory":"8g","cpus":4}`,
		},
		{
			name: "host_net_hides_ports",
			in: BriefingInput{
				Workspace:        "/w",
				NetMode:          "host",
				ForwardHostPorts: []any{jsonx.IntValue(8000)},
			},
			pyKwargs: `workspace=Path("/w"), blocked_tools=[], mount_descriptions=[],
			  net_mode="host", runtime="podman", forward_host_ports=[8000]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pyContent := pyGenerate(t, py, tc.pyKwargs)
			goContent := BriefingContent(tc.in)
			if goContent != pyContent {
				t.Errorf("briefing mismatch (%s):\n--- go ---\n%s\n--- py ---\n%s", tc.name, goContent, pyContent)
			}
		})
	}
}

// pyGenerate runs generate_agents_md under a temp HOME (so AGENTS_DIR + host
// briefings resolve there, no host briefing exists) with only the claude agent,
// then returns the staged CLAUDE.md (== pure jail content when no host briefing).
func pyGenerate(t *testing.T, py func(...string) *exec.Cmd, kwargs string) string {
	t.Helper()
	home := t.TempDir()
	script := `
import sys, os; sys.path.insert(0, 'src')
from pathlib import Path
from cli.agents_md import generate_agents_md
d = generate_agents_md("test-cname", ` + kwargs + `, agents=["claude"])
sys.stdout.write((Path(d) / "CLAUDE.md").read_text())
`
	cmd := py("-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python generate_agents_md failed: %v", err)
	}
	return string(out)
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	must(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no syscall.Stat_t")
	}
	return st.Ino
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
