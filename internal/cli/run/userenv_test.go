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

// The file holds hydrated env_sources values — API keys in practice — so it is
// owner-only. It was 0644 until 2026-09-01, measured in a live jail as
// `-rw-r--r--` carrying two provider keys, while packs/zai's README tells the user
// to keep that key in a file that is "untracked, 0600": yolo's own copy was
// downgrading the mode the user chose.
func TestWriteUserEnvFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "yolo-user-env.sh")

	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "sk-secret")
	writeUserEnvFile(f, env)
	st, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("a file holding a plaintext credential must be 0600, got %04o", got)
	}

	// The EMPTY path carries the mode too: dropping env_sources truncates rather
	// than removing, and a truncation that widened the mode back would undo this on
	// the next launch.
	writeUserEnvFile(f, jsonx.NewOrderedMap())
	st, err = os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("the truncating path must keep 0600, got %04o", got)
	}
}

// os.WriteFile applies its mode only when CREATING, so a file an older yolo left
// at 0644 would keep it forever without an explicit chmod. Every launch is the
// migration, and this is the test that fails if the chmod is dropped.
func TestWriteUserEnvFileNarrowsAnExistingWideFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "yolo-user-env.sh")
	if err := os.WriteFile(f, []byte("# left by an older yolo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "sk-secret")
	writeUserEnvFile(f, env)
	st, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("an existing 0644 file must be narrowed in place, got %04o", got)
	}
}
