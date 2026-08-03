package entrypoint

import (
	"encoding/json"
	"errors"
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

// The matrix must keep EXERCISING agent launchers, which stopped being automatic when
// the default agent set was deleted.
//
// checkShellScripts globs both generated dirs and skips files that do not exist, so a
// scenario generating no launcher still passes — silently testing less. Three scenarios
// (`default_claude`, `blocked_tools_matrix`, `mise_ca_seeded`) relied on an absent
// YOLO_AGENTS meaning "claude"; with no default that became "no agents", and the
// `bash -n` gate over generated agent launchers quietly lost them while staying green.
// The scenario literally named `default_claude` was exercising no claude at all, which
// is why it is now `no_agents`.
//
// So: at least one scenario must generate a launcher (the gate has something to check),
// and `no_agents` must generate NONE (a zero-agent boot stays a pinned, deliberate case
// rather than an accident). This asserts the matrix's coverage, not the generators.
func TestMatrixCoversAgentLaunchersAndTheZeroAgentCase(t *testing.T) {
	matrix := loadMatrix(t, filepath.Join(findRepoRoot(t), "internal", "entrypoint", "testdata", "entrypoint_matrix.json"))

	withLaunchers := 0
	for name, spec := range matrix.Scenarios {
		dir := t.TempDir()
		e := NewEnv(scenarioVars(dir, matrix.HomeToken, spec))
		seedFiles(t, dir, matrix.HomeToken, spec)
		if err := GenerateAgentLaunchers(e); err != nil {
			t.Fatalf("%s: GenerateAgentLaunchers: %v", name, err)
		}
		// Only agent launchers count: GeneratePackageManagerLaunchers is not run here,
		// so anything in the LAUNCHER dir came from the pack selection. (Reading the
		// launcher dir, not the shim dir — the two mechanisms were split so that the
		// blockers a scenario's blocked_tools generate can never be miscounted as
		// launchers, which is what this assertion is trying to measure.)
		got := 0
		if entries, err := os.ReadDir(filepath.Join(dir, ".yolo-launchers")); err == nil {
			got = len(entries)
		}
		if name == "no_agents" {
			if got != 0 {
				t.Errorf("scenario `no_agents` generated %d launcher(s), want 0 — a "+
					"boot with no packs must stay pinned", got)
			}
			continue
		}
		if got > 0 {
			withLaunchers++
		}
	}
	if withLaunchers == 0 {
		t.Error("no matrix scenario generates a tool launcher, so the `bash -n` gate " +
			"covers none — give a scenario a YOLO_PACK_ROOT with a pack declaring `install`")
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
	must(ConfigureMisePrism(e))
	must(GenerateMCPWrappers(e))
	// Every embedded pack, rather than a switch over a selected-agent list. That list
	// (YOLO_AGENTS) is gone: what a jail provisions comes from its packs.
	for _, name := range EmbeddedPackNames() {
		must(ConfigurePackByName(e, name))
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
	// Both generated script dirs: blockers in .yolo-shims, lazy installers in
	// .yolo-launchers. Globbing only the first would have quietly stopped `bash -n`-ing
	// every launcher the moment the two were split.
	for _, gen := range []string{".yolo-shims", ".yolo-launchers"} {
		entries, err := os.ReadDir(filepath.Join(home, gen))
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			rels = append(rels, filepath.Join(gen, ent.Name()))
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

// A12: a config-generator failure must be FATAL. genStep used to warn and discard
// the error, so a jail with a broken generator still started and handed the agent
// a missing or half-written config — the failure only showed up later as
// inexplicable agent behavior.
//
// Also pins the COLLECT-don't-stop-early behavior: every step runs, so one boot
// reports every broken generator rather than making the user restart once per bug.
func TestGenStepFailuresAreFatalAndCollected(t *testing.T) {
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}}

	if err := genFailuresError(e); err != nil {
		t.Fatalf("clean env must not error: %v", err)
	}

	genStep(e, "step_ok", func() error { return nil })
	if err := genFailuresError(e); err != nil {
		t.Fatalf("a succeeding step must not error: %v", err)
	}

	genStep(e, "step_one", func() error { return errors.New("boom one") })
	genStep(e, "step_two", func() error { return errors.New("boom two") })

	err := genFailuresError(e)
	if err == nil {
		t.Fatal("failed generators must abort the boot, not warn")
	}
	msg := err.Error()
	// Both failures must be named: reporting only the first would send the user
	// through one restart per bug.
	for _, want := range []string{"step_one", "boom one", "step_two", "boom two", "refusing to start"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}
