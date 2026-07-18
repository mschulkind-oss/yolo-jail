package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedShellSyntax runs `bash -n` over every generated SHELL script
// (shims, agent/pkg launchers, .bashrc, bootstrap, venv-precreate, MCP
// wrappers, the yolo bash shim) across the committed env matrix. Generated bash
// must stay syntactically valid (go-port plan Stage 9: "bash -n on all
// generated shell"). The Python helper scripts (yolo-cglimit, yolo-journalctl,
// yolo-ps, _yolo_bootstrap.py) are Python, not shell, and are excluded here.
//
// Skips when bash is unavailable.
func TestGeneratedShellSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	matrix := loadMatrix(t, filepath.Join(findRepoRoot(t), "tools", "parity", "entrypoint_matrix.json"))

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

// scenarioVars builds the env var map for a scenario rooted at home.
func scenarioVars(home, token string, spec scenarioSpec) map[string]string {
	vars := map[string]string{"JAIL_HOME": home}
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
			must(ConfigureClaude(e))
		case "copilot":
			must(ConfigureCopilot(e))
		case "gemini":
			must(ConfigureGemini(e))
		case "opencode":
			must(ConfigureOpencode(e))
		case "pi":
			must(ConfigurePi(e))
		case "codex":
			must(ConfigureCodex(e))
		}
	}
	must(GenerateCglimitScript(e))
	must(GenerateJournalctlScript(e))
	must(GenerateYoloPsScript(e))
	must(GenerateYoloWrapper(e))
}

// shellScripts are the generated files whose bodies are bash/sh (relative to
// HOME). Python-bodied helpers are excluded.
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
	// Shims + launchers (incl. the yolo bash shim) are dynamic per scenario —
	// walk the shim dir for present sh/bash scripts (shims are #!/bin/sh;
	// launchers + the yolo wrapper are #!/bin/bash). Skip the Python bootstrap.
	shimDir := filepath.Join(home, ".yolo-shims")
	if entries, err := os.ReadDir(shimDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() || ent.Name() == "_yolo_bootstrap.py" {
				continue
			}
			rels = append(rels, filepath.Join(".yolo-shims", ent.Name()))
		}
	}

	for _, rel := range rels {
		path := filepath.Join(home, rel)
		if _, err := os.Stat(path); err != nil {
			continue // not generated in this scenario
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
