package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestCheckReadsThePerWorkspaceFileAsALaunchDoes: `yolo check` composes the config a launch
// composes, the per-workspace file included (config/workspacefile.go;
// docs/design/boundary-broker.md BB-D54). So a brokered loophole switched on only there is one
// the check sees as starting: its Config Files section names the file and the switch, and
// --accept-config-changes records the repository scope the launch will ask about. Deleting the
// merge in Check() leaves the scope unrecorded, and deleting the row leaves the file unnamed.
func TestCheckReadsThePerWorkspaceFileAsALaunchDoes(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	mod := filepath.Join(t.TempDir(), "gb")
	must(t, os.MkdirAll(mod, 0o755))
	must(t, os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(`{"name": "gb", "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/true", "{socket}", "{repository_scope}"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`), 0o644))
	recordPackModule(t, mod, true)

	var path string
	out := runCheckOverConfigWith(t, `{}`, false, func(ws string) {
		must(t, os.MkdirAll(filepath.Join(ws, ".git"), 0o755))
		must(t, os.WriteFile(filepath.Join(ws, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = https://github.com/o/r\n"), 0o644))
		path, err = config.SetWorkspaceLoophole(ws, "gb", true)
		must(t, err)
	}, func(o *Options) {
		o.AcceptConfigChanges = true
		o.Getenv = func(string) string { return "" }
	})
	for _, want := range []string{"Parsed per-workspace file: " + path + " (gb on)",
		"Approved gb repository scope recorded: o/r"} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	}
}
