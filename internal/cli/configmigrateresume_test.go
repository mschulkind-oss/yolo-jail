package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
)

// RC-D45: a migration killed part way is finished by the next `yolo config migrate`, forward,
// and the closing report names what was converted and where the originals are.
func TestConfigMigrateFinishesAMigrationKilledPartWay(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), `{"packages": ["just"]}`+"\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), `{"packages": ["jq"]}`+"\n")
	req := configMigrateRequest(e.ws)
	graph, err := configmigrate.Discover(req)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := configmigrate.Plan(graph)
	if err != nil {
		t.Fatal(err)
	}
	stop := func(i int, _ string) bool { return i == 0 }
	if _, err := configmigrate.Apply(plan, configmigrate.ApplyOptions{StopAfter: stop}); err == nil {
		t.Fatal("the seam did not stop the migration")
	}
	if configmigrate.Interrupted(req) == "" {
		t.Fatal("no journal after a stop")
	}

	rc, out, errw := runConfigMigrateVerb(t, "--dry-run")
	if rc != 1 || !strings.Contains(errw, "stopped part way") || !strings.Contains(errw, "Nothing was changed") {
		t.Fatalf("--dry-run over an interrupted migration: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	rc, out, errw = runConfigMigrateVerb(t, "--yes")
	if rc != 0 || !strings.Contains(out, "Finished the migration that stopped part way. Converted: ") ||
		!strings.Contains(out, "originals kept as ") {
		t.Fatalf("finishing: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	for _, p := range []string{filepath.Join(e.userDir, "config.toml"), filepath.Join(e.userDir, "config.jsonc.pre-migrate"),
		filepath.Join(e.ws, "yolo-jail.toml"), filepath.Join(e.ws, "yolo-jail.jsonc.pre-migrate")} {
		if !present(p) {
			t.Errorf("%s is missing after finishing", p)
		}
	}
	if configmigrate.Interrupted(req) != "" {
		t.Error("the journal outlived the finished migration")
	}
}
