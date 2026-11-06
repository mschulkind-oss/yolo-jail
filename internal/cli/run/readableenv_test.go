package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestATOMLEnvTableReachesTheJailsEnvFile is W3-O's defect at the launch's own call site: a TOML
// project's [env] table was read by `yolo config why` and never hydrated, so the jail saw none of
// it. A podman launch through Run() writes the shared channel file the jail sources; the TOML
// env.set and env.files values must be in it. Fails if the launch hydrates env_sources alone.
func TestATOMLEnvTableReachesTheJailsEnvFile(t *testing.T) {
	ws := t.TempDir()
	argv, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) {
		o.AcceptConfigChanges = true
		if err := os.WriteFile(filepath.Join(o.Workspace, "probe.env"), []byte("READABLE_FILE_PROBE=from-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		body := "[env]\nfiles = ['probe.env']\nset = { READABLE_SET_PROBE = 'from-toml' }\n"
		if err := os.WriteFile(filepath.Join(o.Workspace, paths.WorkspaceConfigTOMLName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if argv == nil {
		t.Fatalf("the launch did not reach podman run:\n%s", printed)
	}
	raw, err := os.ReadFile(filepath.Join(runtimeHome(ws, "podman"), "yolo-user-env.sh"))
	if err != nil {
		t.Fatalf("the launch wrote no channel file: %v\n%s", err, printed)
	}
	file := string(raw)
	for _, want := range []string{"READABLE_SET_PROBE", "from-toml", "READABLE_FILE_PROBE", "from-file"} {
		if !strings.Contains(file, want) {
			t.Errorf("the jail's env file misses %q from the TOML [env] table:\n%s", want, file)
		}
	}
}
