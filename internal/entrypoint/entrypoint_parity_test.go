package entrypoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEntrypointTreeParity is the Stage 9 tree-diff/sha256 golden harness. It
// runs the Go pure content-generators into a fake HOME under the committed
// env matrix (tools/parity/entrypoint_matrix.json), then runs the LIVE Python
// oracle (tools/parity/entrypoint_oracle.py) over the SAME matrix, and byte-
// diffs the trees: the relpath set, per-file sha256 (with HOME normalized to
// the shared @HOME@ token), symlink targets, and exec modes.
//
// Skips (does not fail) when python3/uv is unavailable, so pure-Go `go test`
// still passes; CI always has the repo's Python.
func TestEntrypointTreeParity(t *testing.T) {
	repoRoot := findRepoRoot(t)
	matrix := loadMatrix(t, filepath.Join(repoRoot, "tools", "parity", "entrypoint_matrix.json"))

	pyOut, ok := runPythonOracle(t, repoRoot)
	if !ok {
		t.Skip("python oracle unavailable (uv/python3 not found or failed)")
	}
	var pyTrees map[string]tree
	if err := json.Unmarshal(pyOut, &pyTrees); err != nil {
		t.Fatalf("decode python oracle output: %v\n%s", err, truncate(pyOut))
	}

	for name, spec := range matrix.Scenarios {
		t.Run(name, func(t *testing.T) {
			goTree := runGoScenario(t, matrix.HomeToken, spec)
			pyTree, ok := pyTrees[name]
			if !ok {
				t.Fatalf("python oracle produced no tree for scenario %q", name)
			}
			diffTrees(t, pyTree, goTree)
		})
	}
}

// --- matrix loading ---

type matrixDoc struct {
	HomeToken string                  `json:"home_token"`
	Scenarios map[string]scenarioSpec `json:"scenarios"`
}

type scenarioSpec struct {
	Env                map[string]string `json:"env"`
	Files              map[string]string `json:"files"`
	HostClaudeSettings json.RawMessage   `json:"host_claude_settings"`
}

// rawMatrix decodes the top-level doc including the _comment string.
func loadMatrix(t *testing.T, path string) matrixDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	// The doc has a leading _comment; decode into a permissive shape.
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

// --- Go generation ---

type tree struct {
	Files    map[string]string `json:"files"`
	Symlinks map[string]string `json:"symlinks"`
	Modes    map[string]string `json:"modes"`
}

// runGoScenario drives the Go pure generators into a fresh temp HOME under the
// scenario's env + seed files, in the SAME boot order the oracle uses, then
// walks the tree with the same normalization.
func runGoScenario(t *testing.T, homeToken string, spec scenarioSpec) tree {
	t.Helper()
	home := t.TempDir()
	homeStr := home

	vars := map[string]string{"JAIL_HOME": home}
	for k, v := range spec.Env {
		vars[k] = strings.ReplaceAll(v, homeToken, homeStr)
	}
	// Seed pre-existing files.
	for rel, contents := range spec.Files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(contents, homeToken, homeStr)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	e := NewEnv(vars)

	mustGen := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Same boot order (minus orchestration) as entrypoint_oracle.py.
	mustGen(GenerateShims(e))
	mustGen(GenerateAgentLaunchers(e))
	mustGen(GeneratePackageManagerLaunchers(e))
	if _, err := GenerateCABundle(e); err != nil {
		t.Fatal(err)
	}
	mustGen(GenerateBashrc(e))
	mustGen(GenerateBootstrapScript(e))
	mustGen(GenerateVenvPrecreateScript(e))
	mustGen(GenerateMiseConfig(e))
	mustGen(GenerateMCPWrappers(e))
	for _, agent := range LoadAgents(e) {
		switch agent {
		case "claude":
			mustGen(ConfigureClaude(e))
		case "copilot":
			mustGen(ConfigureCopilot(e))
		case "gemini":
			mustGen(ConfigureGemini(e))
		case "opencode":
			mustGen(ConfigureOpencode(e))
		case "pi":
			mustGen(ConfigurePi(e))
		case "codex":
			mustGen(ConfigureCodex(e))
		}
	}
	mustGen(GenerateCglimitScript(e))
	mustGen(GenerateJournalctlScript(e))
	mustGen(GenerateYoloPsScript(e))
	mustGen(GenerateYoloWrapper(e))

	return walkTree(t, home, homeStr, homeToken)
}

func walkTree(t *testing.T, home, homeStr, homeToken string) tree {
	t.Helper()
	res := tree{Files: map[string]string{}, Symlinks: map[string]string{}, Modes: map[string]string{}}
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(home, path)
		if info.Mode()&os.ModeSymlink != 0 {
			target, rerr := os.Readlink(path)
			if rerr != nil {
				return rerr
			}
			res.Symlinks[rel] = strings.ReplaceAll(target, homeStr, homeToken)
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		data = bytes.ReplaceAll(data, []byte(homeStr), []byte(homeToken))
		sum := sha256.Sum256(data)
		res.Files[rel] = hex.EncodeToString(sum[:])
		if info.Mode().Perm()&0o111 != 0 {
			res.Modes[rel] = fmt.Sprintf("0o%o", info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk go tree: %v", err)
	}
	return res
}

// --- diff ---

func diffTrees(t *testing.T, py, go_ tree) {
	t.Helper()
	diffMap(t, "files", py.Files, go_.Files)
	diffMap(t, "symlinks", py.Symlinks, go_.Symlinks)
	diffMap(t, "modes", py.Modes, go_.Modes)
}

func diffMap(t *testing.T, label string, py, gm map[string]string) {
	t.Helper()
	keys := map[string]struct{}{}
	for k := range py {
		keys[k] = struct{}{}
	}
	for k := range gm {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		pv, pOk := py[k]
		gv, gOk := gm[k]
		switch {
		case pOk && !gOk:
			t.Errorf("%s: %q present in Python, MISSING in Go", label, k)
		case !pOk && gOk:
			t.Errorf("%s: %q present in Go, MISSING in Python", label, k)
		case pv != gv:
			t.Errorf("%s: %q mismatch\n  py: %s\n  go: %s", label, k, pv, gv)
		}
	}
}

// --- python oracle runner ---

func runPythonOracle(t *testing.T, repoRoot string) ([]byte, bool) {
	t.Helper()
	oracle := filepath.Join(repoRoot, "tools", "parity", "entrypoint_oracle.py")
	var cmd *exec.Cmd
	if _, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command("uv", "run", "python", oracle)
	} else if _, err := exec.LookPath("python3"); err == nil {
		cmd = exec.Command("python3", oracle)
	} else {
		return nil, false
	}
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Logf("python oracle failed: %v\nstderr:\n%s", err, stderr.String())
		return nil, false
	}
	return stdout.Bytes(), true
}

func truncate(b []byte) string {
	if len(b) > 2000 {
		return string(b[:2000]) + "..."
	}
	return string(b)
}

// findRepoRoot walks up from the test's dir until it finds go.mod.
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
