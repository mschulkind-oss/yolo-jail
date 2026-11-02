package packmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const loopholeFixture = `{
  // The probe loophole: a comment before the first key.
  "name": "probe",
  "description": "A probe.",
  "version": 1,
  /* Carried as written, false included. */
  "transport": "loopback-tls",
  "lifecycle": "spawned",
  "settings": {
    // The second key, declared first.
    "second": {"type": "int", "scope": "user", "default": 7, "description": "a description long enough that the whole declaration cannot stay on one line of the file"},
    "first": {"type": "string", "scope": "user", "default": "", "description": "another description long enough that it too becomes a table of its own in the file"}
  },
  "host_bind_mounts": [
    // The socket.
    {"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/run/pulse/native", "readonly": true},
    {"host": "{loophole_dir}/asound.conf", "container": "/etc/alsa/conf.d/50-probe.conf", "readonly": true}
  ],
  "host_daemon": {
    // Trailing comment on the daemon.
    "cmd": ["yolo", "internal", "daemon", "probe", "--socket", "{socket}", "--settings", "{settings}"],
    "publishes": "socket"
  }
}
`

func loopholePackFixture(t *testing.T, packFile, packBody string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "probe-pack")
	module := filepath.Join(dir, "loopholes", "probe")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(dir, packFile):            packBody,
		filepath.Join(module, "manifest.jsonc"): loopholeFixture,
		filepath.Join(module, "asound.conf"):    "x\n",
		filepath.Join(dir, "README.md"):         "x\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const loopholePackJSON = `{"name": "probe-pack", "description": "d", "contributes": [{"kind": "loophole", "from": "loopholes/probe"}]}`

const loopholePackTOML = `name = "probe-pack"
description = "d"

[[loophole]]
from = "loopholes/probe"
`

// TestMigrateConvertsAPacksLoopholeManifestsAlongsideItsPackManifest: a legacy pack.json pack's
// loophole module becomes loophole.toml in the same migration, every comment kept in order.
func TestMigrateConvertsAPacksLoopholeManifestsAlongsideItsPackManifest(t *testing.T) {
	dir := loopholePackFixture(t, "pack.json", loopholePackJSON)
	m, err := Plan(dir, Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(m.Loopholes) != 1 || m.Manifest == nil {
		t.Fatalf("plan = %+v, want the pack manifest and one loophole", m)
	}
	if err := Apply(m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertMigratedProbe(t, dir)
}

// TestMigrateConvertsLoopholesOfAPackAlreadyOnPackTOML: a pack already on pack.toml still has its
// loophole modules converted, and a second run has nothing left to do.
func TestMigrateConvertsLoopholesOfAPackAlreadyOnPackTOML(t *testing.T) {
	dir := loopholePackFixture(t, "pack.toml", loopholePackTOML)
	m, err := Plan(dir, Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if m.Manifest != nil || len(m.Loopholes) != 1 {
		t.Fatalf("plan = %+v, want loopholes only", m)
	}
	if err := Apply(m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertMigratedProbe(t, dir)
	if _, err := Plan(dir, Options{}); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
		t.Fatalf("second Plan = %v, want nothing to migrate", err)
	}
}

func assertMigratedProbe(t *testing.T, dir string) {
	t.Helper()
	module := filepath.Join(dir, "loopholes", "probe")
	if _, err := os.Stat(filepath.Join(module, "manifest.jsonc")); !os.IsNotExist(err) {
		t.Errorf("manifest.jsonc is still there: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(module, "loophole.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// Comments survive, as `#` comments, in their original order.
	order := []string{
		"# The probe loophole: a comment before the first key.",
		"# Carried as written, false included.",
		"# The second key, declared first.",
		"# The socket.",
		"# Trailing comment on the daemon.",
	}
	last := -1
	for _, c := range order {
		i := strings.Index(got, c)
		if i < 0 {
			t.Fatalf("comment %q lost:\n%s", c, got)
		}
		if i < last {
			t.Errorf("comment %q moved out of order:\n%s", c, got)
		}
		last = i
	}
	// Settings keep their declaration order, as sub-tables of settings.
	second, first := strings.Index(got, "[settings.second]"), strings.Index(got, "[settings.first]")
	if second < 0 || second > first {
		t.Errorf("settings lost their order:\n%s", got)
	}
	if strings.Contains(got, "\n[settings]\n") {
		t.Errorf("an empty [settings] header was written:\n%s", got)
	}
	if _, err := loopholedecl.LoadDir(module); err != nil {
		t.Errorf("the migrated loophole does not decode strictly: %v", err)
	}
}

// TestMigrateRefusesALoopholeThatWritesNull: TOML has no null, so the key is named rather than
// guessed at, and nothing is changed.
func TestMigrateRefusesALoopholeThatWritesNull(t *testing.T) {
	dir := loopholePackFixture(t, "pack.toml", loopholePackTOML)
	module := filepath.Join(dir, "loopholes", "probe", "manifest.jsonc")
	if err := os.WriteFile(module, []byte(`{"name": "probe", "transport": "none", "ca_cert": null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, dir)
	_, err := Plan(dir, Options{})
	if err == nil || !strings.Contains(err.Error(), "ca_cert") {
		t.Fatalf("Plan = %v, want a refusal naming ca_cert", err)
	}
	if !equalSnapshots(before, treeSnapshot(t, dir)) {
		t.Error("a refused migration changed the tree")
	}
}

// TestMigrateRefusesAModuleHoldingBothManifests: loophole.toml beside manifest.jsonc is refused
// naming the file to delete, as pack.toml beside pack.json is.
func TestMigrateRefusesAModuleHoldingBothManifests(t *testing.T) {
	dir := loopholePackFixture(t, "pack.toml", loopholePackTOML)
	toml := filepath.Join(dir, "loopholes", "probe", loopholedecl.ManifestTOMLName)
	if err := os.WriteFile(toml, []byte("name = \"probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Plan(dir, Options{})
	if err == nil || !strings.Contains(err.Error(), "delete manifest.jsonc") {
		t.Fatalf("Plan = %v, want a refusal naming manifest.jsonc to delete", err)
	}
}

// TestMigrationOfTheShippedLoopholesProvesEveryOneEqual: every shipped loophole's legacy manifest
// (the frozen fixtures, as the JSONC they were) converts through the migrator and proves equal,
// so the migrator, the encoder and the reader agree on every shape yolo itself ships.
func TestMigrationOfTheShippedLoopholesProvesEveryOneEqual(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "loopholedecl", "testdata", "legacy-shipped", "*.json"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no legacy fixtures found: %v", err)
	}
	for _, fixture := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixture), ".json")
		_, module, _ := strings.Cut(name, "__")
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "loopholes", module)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanLoophole(root, "loopholes/"+module)
			if err != nil || plan == nil {
				t.Fatalf("PlanLoophole = %v, %v", plan, err)
			}
		})
	}
}
