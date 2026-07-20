package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

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
