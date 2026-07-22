package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type matrixDoc struct {
	HomeToken string                  `json:"home_token"`
	Scenarios map[string]scenarioSpec `json:"scenarios"`
}

type scenarioSpec struct {
	Env                map[string]string `json:"env"`
	Files              map[string]string `json:"files"`
	HostClaudeSettings json.RawMessage   `json:"host_claude_settings"`
}

func loadMatrix(t *testing.T, path string) matrixDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	var full struct {
		HomeToken string                     `json:"home_token"`
		Scenarios map[string]json.RawMessage `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	doc := matrixDoc{HomeToken: full.HomeToken, Scenarios: map[string]scenarioSpec{}}
	for name, rawSpec := range full.Scenarios {
		var spec scenarioSpec
		if err := json.Unmarshal(rawSpec, &spec); err != nil {
			t.Fatalf("decode scenario %q: %v", name, err)
		}
		doc.Scenarios[name] = spec
	}
	if doc.HomeToken == "" {
		doc.HomeToken = "@HOME@"
	}
	return doc
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// TestGeneratedShellSyntax runs `bash -n` over every generated SHELL script
// (shims, agent/pkg launchers, .bashrc, bootstrap, venv-precreate, MCP
// wrappers, the yolo bash shim) across the committed env matrix. Generated bash
// must stay syntactically valid.
//
// Skips when bash is unavailable.
func TestGeneratedShellSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	matrix := loadMatrix(t, filepath.Join(findRepoRoot(t), "internal", "entrypoint", "testdata", "entrypoint_matrix.json"))

	for name, spec := range matrix.Scenarios {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			e := NewEnv(scenarioVars(dir, matrix.HomeToken, spec))
			seedFiles(t, dir, matrix.HomeToken, spec)
			generateAll(t, e)
			checkShellScripts(t, bash, dir)
		})
	}
}

func scenarioVars(home, token string, spec scenarioSpec) map[string]string {
	vars := map[string]string{"JAIL_HOME": home}
	// Isolate the prism §5 sidecars (<workspace>/.yolo/prism/) under the temp
	// home so the shell-syntax generation never writes into the real /workspace
	// (WorkspaceDir defaults to /workspace when YOLO_WORKSPACE is unset).
	vars["YOLO_WORKSPACE"] = filepath.Join(home, "workspace")
	for k, v := range spec.Env {
		vars[k] = replaceAll(v, token, home)
	}
	return vars
}

func seedFiles(t *testing.T, home, token string, spec scenarioSpec) {
	t.Helper()
	for rel, contents := range spec.Files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(replaceAll(contents, token, home)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func generateAll(t *testing.T, e *Env) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(GenerateShims(e))
	must(GenerateAgentLaunchers(e))
	must(GeneratePackageManagerLaunchers(e))
	if _, err := GenerateCABundle(e); err != nil {
		t.Fatal(err)
	}
	must(GenerateBashrc(e))
	must(GenerateBootstrapScript(e))
	must(GenerateVenvPrecreateScript(e))
	must(GenerateMiseConfig(e))
	must(GenerateMCPWrappers(e))
	for _, agent := range LoadAgents(e) {
		switch agent {
		case "claude":
			must(ConfigureClaudePrism(e))
		case "copilot":
			must(ConfigureCopilotPrism(e))
		case "gemini":
			must(ConfigureGeminiPrism(e))
		case "opencode":
			must(ConfigureOpencodePrism(e))
		case "pi":
			must(ConfigurePiPrism(e))
		case "codex":
			must(ConfigureCodexPrism(e))
		case "agy":
			must(ConfigureAgyPrism(e))
		}
	}
	must(GenerateCglimitScript(e))
	must(GenerateJournalctlScript(e))
	must(GenerateYoloWrapper(e))
}

func checkShellScripts(t *testing.T, bash, home string) {
	t.Helper()
	rels := []string{
		".bashrc",
		".yolo-bootstrap.sh",
		".yolo-venv-precreate.sh",
		".local/bin/chrome-devtools-mcp-wrapper",
		".local/bin/mcp-wrappers/node",
		".local/bin/mcp-wrappers/npx",
	}
	shimDir := filepath.Join(home, ".yolo-shims")
	if entries, err := os.ReadDir(shimDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			rels = append(rels, filepath.Join(".yolo-shims", ent.Name()))
		}
	}

	for _, rel := range rels {
		path := filepath.Join(home, rel)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out, err := exec.Command(bash, "-n", path).CombinedOutput()
		if err != nil {
			t.Errorf("bash -n failed for %s: %v\n%s", rel, err, out)
		}
	}
}

func replaceAll(s, old, replacement string) string {
	return strings.ReplaceAll(s, old, replacement)
}
