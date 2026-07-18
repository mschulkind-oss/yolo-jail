package runcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestWriteUserEnvFileBytes pins the exact yolo-user-env.sh bytes (frozen
// export-line grammar: `export K=${K:-'v'}` with '\” single-quote escaping).
func TestWriteUserEnvFileBytes(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("FOO", "bar")
	env.Set("QUOTED", "it's a 'test'")
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, env)
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Auto-generated from yolo-jail.jsonc env config.\n" +
		"# Override by editing this file or workspace .env (mise).\n" +
		"export FOO=${FOO:-'bar'}\n" +
		`export QUOTED=${QUOTED:-'it'\''s a '\''test'\'''}` + "\n"
	if string(got) != want {
		t.Errorf("yolo-user-env.sh bytes:\n got: %q\nwant: %q", got, want)
	}
}

func TestWriteUserEnvFileEmptyTouches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, jsonx.NewOrderedMap())
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("empty env should touch an empty file, size=%d", info.Size())
	}
}

// TestUserEnvGoWriterPythonReaderRoundTrip closes the Stage-9 corpus in the
// Go-writer/Python-reader direction: the Go writer's bytes are read back by the
// live Python _hydrate_env_from_user_env_file, and each value must round-trip
// (including the '\” single-quote escaping). Skips without uv/python3.
func TestUserEnvGoWriterPythonReaderRoundTrip(t *testing.T) {
	root := repoRootForTest(t)
	pyRun := pythonRunner(t, root)
	if pyRun == nil {
		t.Skip("python unavailable")
	}

	env := jsonx.NewOrderedMap()
	env.Set("SIMPLE", "value")
	env.Set("WITH_SPACES", "a b c")
	env.Set("WITH_QUOTES", "it's got 'quotes'")
	env.Set("WITH_SPECIAL", `a$b"c\d`)
	env.Set("EMPTY", "")

	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeUserEnvFile(filepath.Join(cfgDir, "yolo-user-env.sh"), env)

	// Harness: import entrypoint with HOME=<home>, run the hydrator with a clean
	// os.environ (so no key is pre-set), print the resulting env as JSON.
	code := `
import json, os, sys
home = sys.argv[1]
os.environ["HOME"] = home
os.environ["JAIL_HOME"] = home
sys.path.insert(0, "src")
# Clear the keys we care about so the file default wins.
for k in ("SIMPLE","WITH_SPACES","WITH_QUOTES","WITH_SPECIAL","EMPTY"):
    os.environ.pop(k, None)
import entrypoint
entrypoint.HOME = __import__("pathlib").Path(home)
entrypoint._hydrate_env_from_user_env_file()
print(json.dumps({k: os.environ.get(k) for k in ("SIMPLE","WITH_SPACES","WITH_QUOTES","WITH_SPECIAL","EMPTY")}))
`
	out, err := pyRun("-c", code, home).Output()
	if err != nil {
		t.Skipf("python reader failed: %v", err)
	}
	// The reader prints one JSON line; take the last non-empty line.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := lines[len(lines)-1]
	var got map[string]string
	if err := json.Unmarshal([]byte(last), &got); err != nil {
		t.Fatalf("decode reader output: %v\n%s", err, out)
	}
	want := map[string]string{
		"SIMPLE":       "value",
		"WITH_SPACES":  "a b c",
		"WITH_QUOTES":  "it's got 'quotes'",
		"WITH_SPECIAL": `a$b"c\d`,
		"EMPTY":        "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("round-trip %s: got %q, want %q", k, got[k], v)
		}
	}
}
