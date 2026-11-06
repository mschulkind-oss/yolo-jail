package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// The owner's report of 2026-11-06: a JSONC link that came back after `yolo config migrate`.
// `yolo check` FAILs with the launch's sentence and gives its one removal, alone on its own line,
// with the re-check on a line of its own after it.
func TestCheckSaysAJSONCCameBackAfterMigration(t *testing.T) {
	var out bytes.Buffer
	o := baseOptions(t, &out)
	ws, err := filepath.EvalSymlinks(o.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	o.Workspace = ws
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.UserLayerEnv, "")
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), []byte("[install]\nnix = ['just']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "swarf", ".links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "swarf", ".links", "yolo-jail.jsonc"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "yolo-jail.jsonc")
	for _, p := range []string{link + ".pre-migrate", link} {
		if err := os.Symlink("swarf/.links/yolo-jail.jsonc", p); err != nil {
			t.Fatal(err)
		}
	}

	if rc := Check(o); rc == 0 {
		t.Errorf("check passed a TOML beside the JSONC it was converted from:\n%s", out.String())
	}
	got := out.String()
	if want := "[FAIL] " + link + " came back after you migrated to yolo-jail.toml"; !strings.Contains(got, want) {
		t.Errorf("check does not FAIL with the launch's sentence (want %q):\n%s", want, got)
	}
	if want := "\n" + noteContPrefix + "    rm " + link + "\n"; !strings.Contains(got, want) {
		t.Errorf("check's note does not give the removal on its own line (want %q):\n%s", want, got)
	}
	if want := "\n" + noteContPrefix + recheck + "\n"; !strings.Contains(got, want) {
		t.Errorf("check's re-check is not on its own line (want %q):\n%s", want, got)
	}
	if strings.Contains(got, "come back; then") {
		t.Errorf("the re-check is glued to the prose:\n%s", got)
	}
}
