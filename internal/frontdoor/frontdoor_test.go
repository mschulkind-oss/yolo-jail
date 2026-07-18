package frontdoor

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRewriteArgv(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		// `yolo -- echo foo` -> `yolo run -- echo foo`.
		{[]string{"--", "echo", "foo"}, []string{"run", "--", "echo", "foo"}},
		// A subcommand before `--` is left alone.
		{[]string{"run", "--", "echo"}, []string{"run", "--", "echo"}},
		{[]string{"broker", "restart"}, []string{"broker", "restart"}},
		// Flags before `--` don't count as subcommands -> insert run.
		{[]string{"-v", "--", "ls"}, []string{"-v", "run", "--", "ls"}},
		// No `--` -> unchanged.
		{[]string{"check"}, []string{"check"}},
		{[]string{"ps"}, []string{"ps"}},
		{nil, nil},
	}
	for _, tc := range cases {
		got := RewriteArgv(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RewriteArgv(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubcommand(t *testing.T) {
	cases := map[string]string{
		"run --":      "run", // via a helper below (split)
		"check":       "check",
		"broker stop": "broker",
		"-v run":      "run",
		"--version":   "",
		"":            "",
		"bogus -- x":  "",
	}
	for in, want := range cases {
		args := strings.Fields(in)
		if got := Subcommand(args); got != want {
			t.Errorf("Subcommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostPlatformNaming(t *testing.T) {
	// The banner must use x86_64/aarch64 (platform.machine()), never Go's
	// amd64/arm64.
	p := hostPlatform()
	if strings.Contains(p, "amd64") || strings.Contains(p, "arm64") {
		t.Errorf("hostPlatform() = %q, must not contain Go arch names", p)
	}
}

func TestStartupBanner(t *testing.T) {
	// Same version -> plain; no res parts.
	got := StartupBanner("0.6.0", "podman", "yolo-x-abc", nil, "")
	if !strings.HasPrefix(got, "yolo-jail 0.6.0 | ") || !strings.HasSuffix(got, " | podman | yolo-x-abc") {
		t.Errorf("banner = %q", got)
	}
	// Differing jail version -> attached form.
	got = StartupBanner("0.6.0", "podman", "c", nil, "0.5.0")
	if !strings.Contains(got, "attached to jail built at 0.5.0") {
		t.Errorf("attached banner = %q", got)
	}
	// Res parts add a second line.
	got = StartupBanner("0.6.0", "podman", "c", []string{"pids=32768"}, "")
	if !strings.Contains(got, "\nResource limits: pids=32768") {
		t.Errorf("res banner = %q", got)
	}
}

// TestSubcommandsMatchesPython cross-asserts the Go Subcommands set against the
// Python _SUBCOMMANDS (the seam #1 argv-rewrite oracle). Skips without Python.
func TestSubcommandsMatchesPython(t *testing.T) {
	root := repoRoot(t)
	py := pythonRunner(t, root)
	if py == nil {
		t.Skip("python unavailable")
	}
	out, err := py("-c", "import sys; sys.path.insert(0,'src'); from cli import _SUBCOMMANDS; print('\\n'.join(sorted(_SUBCOMMANDS)))").Output()
	if err != nil {
		t.Skipf("could not read Python _SUBCOMMANDS: %v", err)
	}
	pySet := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			pySet[line] = struct{}{}
		}
	}
	for k := range pySet {
		if _, ok := Subcommands[k]; !ok {
			t.Errorf("Python _SUBCOMMANDS has %q; Go Subcommands is missing it", k)
		}
	}
	for k := range Subcommands {
		if _, ok := pySet[k]; !ok {
			t.Errorf("Go Subcommands has %q; Python _SUBCOMMANDS is missing it", k)
		}
	}
}

func pythonRunner(t *testing.T, root string) func(args ...string) *exec.Cmd {
	t.Helper()
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
