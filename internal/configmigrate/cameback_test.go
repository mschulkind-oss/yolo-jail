package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The owner's report of 2026-11-06: after a migration, a link manager re-created the
// yolo-jail.jsonc link the migration moved aside. `yolo config migrate` run again says what the
// launch says: the file came back, and the one removal, alone on its own line.
func TestConfigMigrateSaysAJSONCCameBackAsTheLaunchDoes(t *testing.T) {
	env(t)
	ws := realDir(t)
	write(t, filepath.Join(ws, "yolo-jail.toml"), "[install]\nnix = ['just']\n")
	write(t, filepath.Join(ws, "swarf", ".links", "yolo-jail.jsonc"), "{}\n")
	link := filepath.Join(ws, "yolo-jail.jsonc")
	for _, p := range []string{link + ".pre-migrate", link} {
		if err := os.Symlink("swarf/.links/yolo-jail.jsonc", p); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Discover(Request{Workspace: ws})
	if err == nil {
		t.Fatal("discovery accepted a TOML beside the JSONC it was converted from")
	}
	for _, want := range []string{link + " came back after you migrated to yolo-jail.toml", "\n    rm " + link + "\n"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("migrate's refusal does not say %q:\n%v", want, err)
		}
	}
}
