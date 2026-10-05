package integration

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// TestMacosUserUvKeepsTheHostVenv: macos-user has no mount namespace, so `.venv` is not
// shadowed per side as it is on a container, and a sandbox `uv sync` used to meet the HOST's
// venv — interpreter links into a home the sandbox cannot read — and rebuild it in place. The
// launch now sets UV_PROJECT_ENVIRONMENT to `.venv-macos-user` (macosuser's buildPlan), and
// names the per-side paths the two sides still share.
//
// The host venv is a stand-in with the one property that matters: its interpreter link points
// into the invoking user's home. A sentinel inside it must survive the sandbox's sync, and the
// sandbox's own venv must appear beside it, with uv's `*` .gitignore. The workspace is also a
// Node project, so the disclosure must name node_modules, which nothing redirects.
//
// Network: uv may download a managed Python if the sandbox PATH holds none it accepts.
func TestMacosUserUvKeepsTheHostVenv(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{"mise_tools": {"uv": "latest"}}`)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("provisioning log — %s:\n%s", provision.StartupLog(ws), readProvisionLog(ws))
		}
	})
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	hostPython := filepath.Join(u.HomeDir, ".yolo-it-host-python", "bin", "python3")
	for path, content := range map[string]string{
		"pyproject.toml": "[project]\nname = \"yolo-it-uv\"\nversion = \"0.1.0\"\n" +
			"requires-python = \">=3.8\"\ndependencies = []\n",
		"package.json":            "{}\n",
		".venv/yolo-it-sentinel":  "the host's venv\n",
		".venv/pyvenv.cfg":        "home = " + filepath.Dir(hostPython) + "\n",
		".venv/bin/.yolo-it-keep": "",
	} {
		p := filepath.Join(ws, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(hostPython, filepath.Join(ws, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}

	probe := strings.Join([]string{
		`log=/tmp/yolo-it-uv-$$.log`,
		`echo "=== UV ==="`,
		`echo "env|${UV_PROJECT_ENVIRONMENT:-unset}"`,
		`if uv sync >"$log" 2>&1; then echo "sync|OK"; else echo "sync|FAILED"; fi`,
		`if [ -e .venv/yolo-it-sentinel ]; then echo "sentinel|KEPT"; else echo "sentinel|GONE"; fi`,
		`if [ -f .venv-macos-user/pyvenv.cfg ]; then echo "own-venv|MADE"; else echo "own-venv|ABSENT"; fi`,
		`echo "=== END UV ==="`,
		`echo "--- uv sync output ---"; cat "$log"; rm -f "$log"`,
	}, "\n")
	r := runMacosUser(t, ws, probe)
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END UV ===") {
		t.Fatalf("the launch did not run its probe (rc %d)\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "UV")
	for key, want := range map[string]string{
		"env": ".venv-macos-user", "sync": "OK", "sentinel": "KEPT", "own-venv": "MADE",
	} {
		if got[key] != want {
			t.Errorf("%s|%s, want %s\n%s", key, got[key], want, r.stdout)
		}
	}
	// And from the host's side, after the session: its venv untouched, the sandbox's ignored.
	if _, err := os.Stat(filepath.Join(ws, ".venv", "yolo-it-sentinel")); err != nil {
		t.Errorf("the host's venv lost its sentinel: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, ".venv-macos-user", ".gitignore")); err != nil ||
		strings.TrimSpace(string(b)) != "*" {
		t.Errorf("the sandbox's venv carries no `*` .gitignore (%q, %v), so git would show it", b, err)
	}
	out := r.combined()
	for _, want := range []string{"per-side paths are SHARED with the host on macos-user",
		"node_modules", "uv is redirected to .venv-macos-user"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch's per-side disclosure lacks %q:\n%s", want, out)
		}
	}
}
