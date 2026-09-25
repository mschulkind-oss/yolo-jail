package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE TWO WORKSPACE SNAPSHOTS, config-assembled.json and config-boot.json, are written into
// <workspace>/.yolo on every fresh launch, and `.yolo` is writable from inside the jail (the
// workspace bind hides nothing under it). os.WriteFile follows a link the last jail left at
// either name, so the launch truncated the host file the link named and wrote config JSON,
// whose values the jail-writable workspace config chose, into it. Each is now written beneath
// a root on `.yolo` (paths.WriteWorkspaceStateFile), and a link at the name is replaced.
func TestWorkspaceSnapshotsNeverWriteThroughALink(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("packs", []any{"claude"})
	for _, tc := range []struct {
		name  string
		path  func(ws string) string
		write func(ws string) error
	}{
		{"config-assembled.json", WorkspaceAssembledConfigPath, func(ws string) error { return WriteAssembledConfig(ws, cfg) }},
		{"config-boot.json", WorkspaceConfigBootPath, func(ws string) error { return WriteWorkspaceBootBaseline(ws, cfg) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			host := filepath.Join(t.TempDir(), "bashrc")
			const body = "# the host user's own file\n"
			if err := os.WriteFile(host, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			link := tc.path(ws)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(host, link); err != nil {
				t.Fatal(err)
			}

			if err := tc.write(ws); err != nil {
				t.Fatalf("write: %v", err)
			}

			if got, _ := os.ReadFile(host); string(got) != body {
				t.Errorf("the snapshot was written through the link into the host file: %q", got)
			}
			if fi, err := os.Lstat(link); err != nil || !fi.Mode().IsRegular() {
				t.Errorf("%s is not a regular file after the write: %v", link, err)
			}
		})
	}
}
