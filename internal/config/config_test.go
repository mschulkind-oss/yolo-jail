package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Unit tests for filesystem-backed config loading, includes/cycles, and the
// interactive config-change control flow.

func decode(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decode %q: not a map (%T)", s, v)
	}
	return m
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- loading / includes (TestIncludeIfFound + TestWorkspaceLocalConfig) ----

func TestLoadJSONCFileNonexistentReturnsEmpty(t *testing.T) {
	m, err := LoadJSONCFile(filepath.Join(t.TempDir(), "nope.jsonc"), "test", false, discard)
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Errorf("expected empty, got %d keys", m.Len())
	}
}

func TestLoadJSONCFileNonObjectStrictRaises(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonc")
	write(t, p, "[1, 2, 3]")
	if _, err := LoadJSONCFile(p, "test", true, discard); err == nil {
		t.Errorf("expected ConfigError for non-object in strict mode")
	}
}

func TestLoadJSONCFileInvalidStrictRaises(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonc")
	write(t, p, "{broken json")
	if _, err := LoadJSONCFile(p, "test", true, discard); err == nil {
		t.Errorf("expected ConfigError for invalid JSON in strict mode")
	}
}

func TestIncludeChainAndCycle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.jsonc"), `{"packages": ["a"], "include_if_found": ["b.jsonc"]}`)
	write(t, filepath.Join(dir, "b.jsonc"), `{"packages": ["b"], "include_if_found": ["c.jsonc"]}`)
	write(t, filepath.Join(dir, "c.jsonc"), `{"packages": ["c"]}`)
	m, err := LoadJSONCWithIncludes(filepath.Join(dir, "a.jsonc"), "a", false, discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "a", "b", "c")
	if _, ok := m.Get("include_if_found"); ok {
		t.Errorf("include_if_found should be consumed")
	}

	// Cycle a->b->a must terminate with both present.
	write(t, filepath.Join(dir, "a.jsonc"), `{"packages": ["a"], "include_if_found": ["b.jsonc"]}`)
	write(t, filepath.Join(dir, "b.jsonc"), `{"packages": ["b"], "include_if_found": ["a.jsonc"]}`)
	m2, err := LoadJSONCWithIncludes(filepath.Join(dir, "a.jsonc"), "a", false, discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m2, "a", "b")
}

func TestIncludeAbsoluteRejectedStrict(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "base.jsonc"), `{"include_if_found": ["/etc/passwd"]}`)
	if _, err := LoadJSONCWithIncludes(filepath.Join(dir, "base.jsonc"), "base", true, discard, nil); err == nil {
		t.Errorf("expected ConfigError for absolute include in strict mode")
	}
}

func TestWorkspaceLocalWinsAndMerges(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, WorkspaceConfigName), `{"packages": ["just"], "network": {"mode": "bridge"}}`)
	write(t, filepath.Join(dir, WorkspaceLocalConfigName), `{"packages": ["htop"], "network": {"mode": "host"}}`)
	m, err := LoadWorkspaceConfig(dir, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "just", "htop")
	net, _ := m.Get("network")
	mode, _ := net.(*jsonx.OrderedMap).Get("mode")
	if mode != "host" {
		t.Errorf("network.mode = %v, want host", mode)
	}
}

func TestWorkspaceExplicitIncludeOfLocalNotMergedTwice(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, WorkspaceConfigName),
		`{"packages": ["just"], "include_if_found": ["yolo-jail.local.jsonc"]}`)
	write(t, filepath.Join(dir, WorkspaceLocalConfigName), `{"packages": ["htop"]}`)
	m, err := LoadWorkspaceConfig(dir, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "just", "htop")
}

// ---- CheckConfigChanges control flow (TestConfigSnapshot) ----

func TestCheckConfigChangesFirstRunSaves(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "project")
	config := decode(t, `{"packages": ["strace"]}`)
	ok, err := CheckConfigChanges(ws, config, false, nil)
	if err != nil || !ok {
		t.Fatalf("first run: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(ConfigSnapshotPath(ws)); err != nil {
		t.Errorf("snapshot not written: %v", err)
	}
}

type failPrompter struct {
	t      *testing.T
	called bool
}

func (p *failPrompter) Prompt(diffLines []string) bool {
	p.called = true
	p.t.Errorf("unexpected prompt with diff:\n%v", diffLines)
	return false
}

func TestCheckConfigChangesUnchangedPasses(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "project")
	config := decode(t, `{"packages": ["strace"]}`)
	_, _ = CheckConfigChanges(ws, config, false, nil)
	ok, err := CheckConfigChanges(ws, config, false, nil)
	if err != nil || !ok {
		t.Fatalf("unchanged: ok=%v err=%v", ok, err)
	}
}

func TestCheckConfigChangesNonTTYAutoAccepts(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "project")
	_, _ = CheckConfigChanges(ws, decode(t, `{"packages": ["strace"]}`), false, nil)
	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, false /*non-tty*/, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("non-tty auto-accept: ok=%v err=%v", ok, err)
	}
	// Snapshot must be updated to the new config.
	want, _ := SnapshotJSON(newCfg)
	got, _ := os.ReadFile(ConfigSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("snapshot not updated on auto-accept")
	}
}

func TestCheckConfigChangesTTYYesUpdates(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "project")
	_, _ = CheckConfigChanges(ws, decode(t, `{"packages": ["strace"]}`), false, nil)
	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, true, yesPrompter{})
	if err != nil || !ok {
		t.Fatalf("tty yes: ok=%v err=%v", ok, err)
	}
	want, _ := SnapshotJSON(newCfg)
	got, _ := os.ReadFile(ConfigSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("snapshot not updated on tty-yes")
	}
}

func TestCheckConfigChangesTTYNoRejectsAndKeepsSnapshot(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "project")
	orig := decode(t, `{"packages": ["strace"]}`)
	_, _ = CheckConfigChanges(ws, orig, false, nil)
	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, true, noPrompter{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("tty no: expected proceed=false")
	}
	// Snapshot NOT updated — still the original.
	want, _ := SnapshotJSON(orig)
	got, _ := os.ReadFile(ConfigSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("snapshot changed on tty-no rejection")
	}
}

// ---- helpers ----

func discard(string) {}

type yesPrompter struct{}

func (yesPrompter) Prompt([]string) bool { return true }

type noPrompter struct{}

func (noPrompter) Prompt([]string) bool { return false }

func assertPackages(t *testing.T, m *jsonx.OrderedMap, want ...string) {
	t.Helper()
	v, _ := m.Get("packages")
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("packages not a list: %T", v)
	}
	if len(list) != len(want) {
		t.Fatalf("packages = %v, want %v", list, want)
	}
	for i, w := range want {
		if list[i] != w {
			t.Errorf("packages[%d] = %v, want %s", i, list[i], w)
		}
	}
}
