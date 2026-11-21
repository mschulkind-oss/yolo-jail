package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTraceStoreRootUsesGlobalLogsWithoutCreatingState(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	want := filepath.Join(LogsDir(), "traces")
	if got := TracesDir(); got != want {
		t.Fatalf("TracesDir = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("path helper created state: %v", err)
	}
}
