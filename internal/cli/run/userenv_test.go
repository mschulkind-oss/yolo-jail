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

// TestWriteUserEnvFileEmptyClearsStaleRender is the regression for creds that
// outlive their config. Dropping env_sources makes ResolveEnvSources return an
// empty map, which used to no-op on an ALREADY-EXISTING file (touchFile returns
// early when the path is there) — so the last populated render stayed mounted at
// ~/.config/yolo-user-env.sh and kept exporting AWS keys through both
// hydrateEnvFromUserEnvFile and .bashrc, across any number of jail rebuilds.
// Removing a credential from config must revoke it, so the empty case truncates.
func TestWriteUserEnvFileEmptyClearsStaleRender(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")

	// A previous launch rendered real credentials into the file.
	populated := jsonx.NewOrderedMap()
	populated.Set("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
	writeUserEnvFile(p, populated)

	// This launch has env_sources commented out.
	writeUserEnvFile(p, jsonx.NewOrderedMap())

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("file must still exist (the bind-mount source): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("stale render survived an empty env: %q", got)
	}
}
