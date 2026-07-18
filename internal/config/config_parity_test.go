package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestConfigParity is the Stage 13 differential gate: it drives a corpus of
// config operations through the LIVE src/cli/config.py (via
// tools/parity/config_oracle.py) and through internal/config, then byte-compares
// the merged JSON + snapshot bytes + ordered error/warning lists + derived-helper
// outputs. A single byte of drift on the snapshot form fires a spurious config-
// approval prompt on every Python<->Go switch, so this is the highest-priority
// parity gate.
//
// Skips (does not fail) when the Python oracle can't run, matching the existing
// parity tests; CI always has the repo's Python.
func TestConfigParity(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "tools", "parity", "config_oracle.py")
	corpus := filepath.Join(repoRoot, "tools", "parity", "corpus", "config_cases.json")

	py := pythonRunner(t, repoRoot)
	if py == nil {
		t.Skip("no python oracle available (uv/python3 not found)")
	}

	// The oracle runs with cwd=repoRoot (Dir set below). config.py's mount
	// existence check and the default workspace (Path.cwd()) are cwd-relative,
	// so align this process's cwd to repoRoot for the comparison.
	origWD, _ := os.Getwd()
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repoRoot: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	corpusBytes, err := os.ReadFile(corpus)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	decoded, err := jsonx.Decode(corpusBytes)
	if err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	cases, ok := decoded.([]any)
	if !ok {
		t.Fatalf("corpus is not a JSON array, got %T", decoded)
	}

	cmd := py(oracle)
	f, err := os.Open(corpus)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()
	cmd.Stdin = f
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python oracle failed to run (%v) — skipping cross-language parity", err)
	}
	expDecoded, err := jsonx.Decode(out)
	if err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	expected, ok := expDecoded.([]any)
	if !ok {
		t.Fatalf("oracle output is not an array, got %T", expDecoded)
	}
	if len(expected) != len(cases) {
		t.Fatalf("oracle returned %d results for %d cases", len(expected), len(cases))
	}

	for i, cAny := range cases {
		c := cAny.(*jsonx.OrderedMap)
		exp := expected[i].(*jsonx.OrderedMap)
		op := mustStr(c, "op")
		runCase(t, i, op, c, exp)
	}
}

func runCase(t *testing.T, i int, op string, c, exp *jsonx.OrderedMap) {
	t.Helper()
	switch op {
	case "merge":
		base := mustMap(c, "base")
		override := mustMap(c, "override")
		merged := MergeConfig(base, override)
		checkCompact(t, i, op, merged, exp)
		checkSnapshot(t, i, op, merged, exp)
	case "snapshot":
		config := mustMap(c, "config")
		checkSnapshot(t, i, op, config, exp)
	case "validate":
		config := mustMap(c, "config")
		ws := "/nonexistent-parity-workspace"
		if v, ok := c.Get("workspace"); ok {
			if s, ok := v.(string); ok {
				ws = s
			}
		}
		var resolver LoopholeResolver
		if kv, ok := c.Get("known_loopholes"); ok && kv != nil {
			resolver = buildResolver(kv)
		}
		errs, warns := ValidateConfig(config, ws, resolver)
		checkStrList(t, i, "errors", errs, exp)
		checkStrList(t, i, "warnings", warns, exp)
	case "normalize_blocked_tools":
		sec := optMap(c, "security")
		result := NormalizeBlockedTools(sec)
		checkCompactValue(t, i, op, result, exp)
	case "effective_packages":
		result := EffectivePackages(mustMap(c, "config"))
		checkCompactValue(t, i, op, result, exp)
	case "effective_mcp_server_names":
		servers, _ := c.Get("mcp_servers")
		presets, _ := c.Get("mcp_presets")
		result := EffectiveMCPServerNames(servers, presets)
		checkCompactValue(t, i, op, toAnySlice(result), exp)
	case "selected_agents":
		result := SelectedAgents(mustMap(c, "config"))
		checkCompactValue(t, i, op, strSliceToAny(result), exp)
	case "merge_mise_tools":
		result := MergeMiseTools(mustMap(c, "config"))
		checkCompactValue(t, i, op, result, exp)
	case "merge_mise_disabled_tools":
		val, _ := c.Get("value")
		result := MergeMiseDisabledTools(val)
		want := mustStr(exp, "result")
		if result != want {
			t.Errorf("case %d [%s] result mismatch:\n go: %q\n py: %q", i, op, result, want)
		}
	case "parse_dotenv":
		text := mustStr(c, "text")
		result := ParseDotenv(text)
		checkCompactValue(t, i, op, result, exp)
	default:
		t.Fatalf("case %d: unknown op %q", i, op)
	}
}

func checkSnapshot(t *testing.T, i int, op string, v any, exp *jsonx.OrderedMap) {
	t.Helper()
	got, err := jsonx.DumpsSnapshot(v)
	if err != nil {
		t.Errorf("case %d [%s] snapshot encode: %v", i, op, err)
		return
	}
	want := mustStr(exp, "snapshot")
	if got != want {
		t.Errorf("case %d [%s] snapshot mismatch:\n go: %q\n py: %q", i, op, got, want)
	}
}

func checkCompact(t *testing.T, i int, op string, v any, exp *jsonx.OrderedMap) {
	t.Helper()
	got, err := jsonx.DumpsCompact(v)
	if err != nil {
		t.Errorf("case %d [%s] compact encode: %v", i, op, err)
		return
	}
	want := mustStr(exp, "compact")
	if got != want {
		t.Errorf("case %d [%s] compact mismatch:\n go: %q\n py: %q", i, op, got, want)
	}
}

func checkCompactValue(t *testing.T, i int, op string, v any, exp *jsonx.OrderedMap) {
	t.Helper()
	checkCompact(t, i, op, v, exp)
}

// checkStrList compares a Go []string against the oracle's list under key.
func checkStrList(t *testing.T, i int, key string, got []string, exp *jsonx.OrderedMap) {
	t.Helper()
	rawV, _ := exp.Get(key)
	rawList, _ := rawV.([]any)
	if len(got) != len(rawList) {
		t.Errorf("case %d %s length mismatch: go=%d py=%d\n go: %#v\n py: %#v",
			i, key, len(got), len(rawList), got, rawList)
		return
	}
	for j := range got {
		want, _ := rawList[j].(string)
		if got[j] != want {
			t.Errorf("case %d %s[%d] mismatch:\n go: %q\n py: %q", i, key, j, got[j], want)
		}
	}
}

// buildResolver constructs a LoopholeResolver from a {name: {has_host_daemon}}
// map decoded from the corpus, mirroring the oracle's _fake_known.
func buildResolver(kv any) LoopholeResolver {
	m, ok := kv.(*jsonx.OrderedMap)
	if !ok {
		return staticResolver{known: map[string]LoopholeInfo{}}
	}
	known := map[string]LoopholeInfo{}
	for _, name := range m.Keys() {
		infoV, _ := m.Get(name)
		hasDaemon := false
		if im, ok := infoV.(*jsonx.OrderedMap); ok {
			if hv, ok := im.Get("has_host_daemon"); ok {
				b, _ := hv.(bool)
				hasDaemon = b
			}
		}
		known[name] = LoopholeInfo{Name: name, HasHostDaemon: hasDaemon}
	}
	return staticResolver{known: known}
}

type staticResolver struct{ known map[string]LoopholeInfo }

func (r staticResolver) Known() (map[string]LoopholeInfo, bool) { return r.known, true }

// ---- small decode helpers ----

func mustStr(m *jsonx.OrderedMap, key string) string {
	v, _ := m.Get(key)
	s, _ := v.(string)
	return s
}

// mustMap returns the OrderedMap at key, or an empty one when the value is
// absent/null (an empty config dict — the {} corpus case decodes to this).
func mustMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	v, ok := m.Get(key)
	if !ok || v == nil {
		return jsonx.NewOrderedMap()
	}
	if sm, ok := v.(*jsonx.OrderedMap); ok {
		return sm
	}
	return jsonx.NewOrderedMap()
}

func optMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	v, ok := m.Get(key)
	if !ok || v == nil {
		return nil
	}
	sm, _ := v.(*jsonx.OrderedMap)
	return sm
}

func toAnySlice(in []any) []any {
	if in == nil {
		return []any{}
	}
	return in
}

func strSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// pythonRunner / findRepoRoot mirror the helpers in internal/jsonx/parity_test.go
// (each test package needs its own copy — they are unexported).
func pythonRunner(t *testing.T, repoRoot string) func(script string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(script string) *exec.Cmd {
			c := exec.Command("uv", "run", "python", script)
			c.Dir = repoRoot
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(script string) *exec.Cmd {
			c := exec.Command("python3", script)
			c.Dir = repoRoot
			return c
		}
	}
	return nil
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
