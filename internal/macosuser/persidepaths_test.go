package macosuser

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE PER-SIDE SET ON A BACKEND WITH NO MOUNT NAMESPACE: uv is redirected off the host's .venv,
// and the launch names what the host and the sandbox share. Every case goes through buildPlan,
// the production caller, so deleting either call there fails it.

// perSidePlan builds the launch plan for a real workspace and returns it with the launch output.
func perSidePlan(t *testing.T, ws string, cfg *jsonx.OrderedMap, sandboxEnv *jsonx.OrderedMap) (RunPlan, string) {
	t.Helper()
	opts := newOpts(ws)
	if cfg != nil {
		opts.Config = cfg
	}
	opts.SandboxEnv = sandboxEnv
	var buf bytes.Buffer
	d := mockDeps(nil)
	d.Out = &buf
	return buildPlan(d, opts, nil), buf.String()
}

// perSideWorkspace is a resolved temp workspace holding the named files (a trailing "/" makes a
// directory).
func perSideWorkspace(t *testing.T, files ...string) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(ws, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// perSideLine is the disclosure line of a launch's output, or "".
func perSideLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "per-side paths are SHARED with the host") {
			return ln
		}
	}
	return ""
}

// The session env carries the redirect, relative, so uv resolves it per project root.
func TestBuildPlanRedirectsUvOffTheHostVenv(t *testing.T) {
	plan, _ := perSidePlan(t, perSideWorkspace(t), nil, nil)
	if v, _ := sandboxEnvFileValue(plan.EnvFileContent, "UV_PROJECT_ENVIRONMENT"); v != ".venv-macos-user" {
		t.Errorf("UV_PROJECT_ENVIRONMENT = %q, want .venv-macos-user; env file:\n%s", v, plan.EnvFileContent)
	}
	if strings.Contains(plan.EnvFileContent, "VIRTUAL_ENV=") {
		t.Errorf("VIRTUAL_ENV must not be set: it steers every tool, not uv alone\n%s", plan.EnvFileContent)
	}
}

// A value the user set wins — through sandbox_env, and through the composed channel that
// carries env_sources — and the disclosure reports the value that won.
func TestBuildPlanLetsTheUserChooseUvsEnvironment(t *testing.T) {
	ws := perSideWorkspace(t, "pyproject.toml")
	env := jsonx.NewOrderedMap()
	env.Set("UV_PROJECT_ENVIRONMENT", ".venv")
	plan, out := perSidePlan(t, ws, nil, env)
	if v, _ := sandboxEnvFileValue(plan.EnvFileContent, "UV_PROJECT_ENVIRONMENT"); v != ".venv" {
		t.Errorf("sandbox_env's UV_PROJECT_ENVIRONMENT lost: %q", v)
	}
	if line := perSideLine(out); !strings.Contains(line, "uv uses UV_PROJECT_ENVIRONMENT=.venv, which your own environment set") {
		t.Errorf("the disclosure does not report the user's uv value:\n%s", out)
	}

	opts := newOpts(ws)
	opts.PackEnv = jsonx.NewOrderedMap()
	opts.PackEnv.Set("UV_PROJECT_ENVIRONMENT", "envs/mine")
	if v, _ := sandboxEnvFileValue(buildPlan(mockDeps(nil), opts, nil).EnvFileContent,
		"UV_PROJECT_ENVIRONMENT"); v != "envs/mine" {
		t.Errorf("an env_sources value (PackEnv) lost to the default: %q", v)
	}
}

func TestPerSideDisclosure(t *testing.T) {
	userPaths := jsonx.NewOrderedMap()
	userPaths.Set("per_side_paths", []any{"packages/web/node_modules", "build"})
	for _, tc := range []struct {
		name    string
		files   []string
		cfg     *jsonx.OrderedMap
		named   []string // in the line
		unnamed []string // not in it
		uv      bool     // the uv clause is present
	}{
		{name: "an empty workspace hears nothing", files: nil},
		{name: "a Node project", files: []string{"package.json"},
			named: []string{"node_modules"}, unnamed: []string{".venv"}},
		{name: "a node_modules with no manifest", files: []string{"node_modules/"},
			named: []string{"node_modules"}},
		{name: "a Python project", files: []string{"pyproject.toml"},
			named: []string{".venv", "uv is redirected to .venv-macos-user (UV_PROJECT_ENVIRONMENT)",
				"`python -m venv`, poetry, pipenv and mise's `_.python.venv` still use the shared path"},
			unnamed: []string{"node_modules"}, uv: true},
		{name: "an existing .venv", files: []string{".venv/"}, named: []string{".venv"}, uv: true},
		{name: "a mise-declared venv", files: []string{"mise.toml"}, named: []string{"py/env"}, uv: true},
		{name: "user entries, always", cfg: userPaths,
			named: []string{"packages/web/node_modules", "build"}, unnamed: []string{".venv,"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := perSideWorkspace(t, tc.files...)
			if len(tc.files) == 1 && tc.files[0] == "mise.toml" {
				if err := os.WriteFile(filepath.Join(ws, "mise.toml"),
					[]byte("[env]\n_.python.venv = \"py/env\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, out := perSidePlan(t, ws, tc.cfg, nil)
			line := perSideLine(out)
			if len(tc.named) == 0 {
				if line != "" {
					t.Errorf("a workspace using no per-side path was warned:\n%s", line)
				}
				return
			}
			if line == "" {
				t.Fatalf("no per-side disclosure; launch output:\n%s", out)
			}
			for _, want := range tc.named {
				if !strings.Contains(line, want) {
					t.Errorf("the disclosure does not name %q:\n%s", want, line)
				}
			}
			for _, unwanted := range tc.unnamed {
				if strings.Contains(line, unwanted) {
					t.Errorf("the disclosure names %q, which this workspace does not use:\n%s", unwanted, line)
				}
			}
			if got := strings.Contains(line, "uv "); got != tc.uv {
				t.Errorf("uv clause present = %v, want %v:\n%s", got, tc.uv, line)
			}
			if !strings.Contains(line, "use a container runtime") {
				t.Errorf("the warning names no next step:\n%s", line)
			}
		})
	}
}
