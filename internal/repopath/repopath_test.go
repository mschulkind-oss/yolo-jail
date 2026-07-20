package repopath

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetInsertsIntoCommentedTemplate(t *testing.T) {
	existing := "{\n" +
		"  // YOLO Jail user-level defaults.\n" +
		"  // \"runtime\": \"podman\",\n" +
		"}\n"
	got := Set(existing, false, "/home/matt/code/yolo-jail")
	if !strings.Contains(got, `"repo_path": "/home/matt/code/yolo-jail"`) {
		t.Errorf("repo_path not inserted:\n%s", got)
	}
	// The banner comment and the commented runtime line must survive.
	if !strings.Contains(got, "YOLO Jail user-level defaults.") ||
		!strings.Contains(got, `// "runtime": "podman",`) {
		t.Errorf("comments not preserved:\n%s", got)
	}
	// The insert goes after the opening brace, not before it.
	if strings.Index(got, "repo_path") < strings.IndexByte(got, '{') {
		t.Error("repo_path inserted before the opening brace")
	}
}

func TestSetReplacesActiveKey(t *testing.T) {
	existing := "{\n" +
		"  // keep me\n" +
		"  \"repo_path\": \"/old/path\",\n" +
		"  \"runtime\": \"podman\"\n" +
		"}\n"
	got := Set(existing, true, "/new/path")
	if !strings.Contains(got, `"repo_path": "/new/path"`) {
		t.Errorf("value not replaced:\n%s", got)
	}
	if strings.Contains(got, "/old/path") {
		t.Errorf("old value not removed:\n%s", got)
	}
	// Other keys and comments preserved; no duplicate repo_path.
	if strings.Count(got, "repo_path") != 1 {
		t.Errorf("expected exactly one repo_path:\n%s", got)
	}
	if !strings.Contains(got, "// keep me") || !strings.Contains(got, `"runtime": "podman"`) {
		t.Errorf("neighbors not preserved:\n%s", got)
	}
}

func TestSetIdempotent(t *testing.T) {
	existing := "{\n  \"repo_path\": \"/same\"\n}\n"
	once := Set(existing, true, "/same")
	twice := Set(once, true, "/same")
	if once != twice {
		t.Errorf("Set not idempotent:\n%q\nvs\n%q", once, twice)
	}
}

func TestSetEmptyProducesStarter(t *testing.T) {
	got := Set("", false, "/home/matt/code/yolo-jail")
	if !strings.Contains(got, `"repo_path": "/home/matt/code/yolo-jail"`) {
		t.Errorf("starter missing repo_path:\n%s", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "{") || !strings.HasSuffix(strings.TrimSpace(got), "}") {
		t.Errorf("starter is not a JSON object:\n%s", got)
	}
}

func TestSetEscapesPath(t *testing.T) {
	got := Set("", false, `/weird/pa"th`)
	if !strings.Contains(got, `"repo_path": "/weird/pa\"th"`) {
		t.Errorf("path not JSON-escaped:\n%s", got)
	}
}

func TestWriteFileCreatesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config", "yolo-jail", "config.jsonc")

	var buf bytes.Buffer
	if err := WriteFile(p, "/repo/yolo-jail", &buf); err != nil {
		t.Fatalf("WriteFile create: %v", err)
	}
	if !strings.Contains(buf.String(), "Created") {
		t.Errorf("expected 'Created' message, got %q", buf.String())
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got, ok := activeRepoPath(string(data)); !ok || got != "/repo/yolo-jail" {
		t.Errorf("repo_path = %q, %v; want /repo/yolo-jail", got, ok)
	}

	// Second call with the same value must not rewrite; must say "already set".
	buf.Reset()
	before, _ := os.ReadFile(p)
	if err := WriteFile(p, "/repo/yolo-jail", &buf); err != nil {
		t.Fatalf("WriteFile idempotent: %v", err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Error("idempotent WriteFile rewrote the file")
	}
	if !strings.Contains(buf.String(), "already set") {
		t.Errorf("expected 'already set', got %q", buf.String())
	}
}

func TestWriteFileUpdatesExistingPreservingComments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.jsonc")
	orig := "{\n  // my defaults\n  \"repo_path\": \"/old\",\n  \"runtime\": \"podman\"\n}\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteFile(p, "/new", &buf); err != nil {
		t.Fatalf("WriteFile update: %v", err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if got, _ := activeRepoPath(s); got != "/new" {
		t.Errorf("repo_path = %q, want /new", got)
	}
	if !strings.Contains(s, "// my defaults") || !strings.Contains(s, `"runtime": "podman"`) {
		t.Errorf("comments/keys not preserved:\n%s", s)
	}
	if !strings.Contains(buf.String(), "Updated") {
		t.Errorf("expected 'Updated' message, got %q", buf.String())
	}
}
