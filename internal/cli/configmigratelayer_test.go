package cli

// configmigratelayer_test.go drives `yolo config migrate FILE...` (RC-D46, amended 2026-11-01)
// through the dispatcher, so each fails if the verb's FILE branch is removed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layerOld is a synthetic --user-layer file: comments, a path pack, a package.
const layerOld = "{\n  // the forked agent\n  \"packs\": [\"~/dots/yolo-packs/pi-fork\"],\n  \"packages\": [\"layer\"]\n}\n"

func layerMigrateEnv(t *testing.T) (migrateEnv, string) {
	t.Helper()
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.home, "dots", "yolo-packs", "pi-fork", "pack.toml"), "name = \"pi-fork\"\n")
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), "{\n  \"packages\": [\"user\"]\n}\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")
	layer := filepath.Join(e.ws, "layers", "work.jsonc")
	writeMigrateFile(t, layer, layerOld)
	return e, layer
}

// The named file is converted and nothing else: the user config and the workspace's own files
// stay old, the original is kept byte for byte, and the report ends with the launch to run next.
func TestConfigMigrateConvertsOnlyTheNamedLayer(t *testing.T) {
	e, layer := layerMigrateEnv(t)
	rc, out, errw := runConfigMigrateVerb(t, "--yes", "layers/work.jsonc")
	if rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	newLayer := filepath.Join(e.ws, "layers", "work.toml")
	data, err := os.ReadFile(newLayer)
	if err != nil {
		t.Fatalf("the layer was not converted: %v\nstdout:\n%s", err, out)
	}
	for _, want := range []string{"# the forked agent", "yolo-packs/pi-fork", "[pack_sources.yolo-packs]"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("work.toml does not carry %q:\n%s", want, data)
		}
	}
	if kept, _ := os.ReadFile(layer + ".pre-migrate"); string(kept) != layerOld {
		t.Errorf("the original is not kept byte for byte: %q", kept)
	}
	if present(filepath.Join(e.userDir, "config.toml")) || present(filepath.Join(e.ws, "yolo-jail.toml")) {
		t.Fatal("a FILE migration converted the user config or the workspace's file")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if last != "Next: launch with `--user-layer layers/work.toml` in place of layers/work.jsonc, which a launch no longer finds." {
		t.Errorf("the report does not end with the next step: %q\n%s", last, out)
	}
	if !strings.Contains(out, "pi-fork  →  yolo-packs/pi-fork") {
		t.Errorf("the rename is not listed:\n%s", out)
	}
}

// A refused layer changes nothing and the refusal names the next step; --dry-run writes nothing.
func TestConfigMigrateRefusesALayerThatCannotConvert(t *testing.T) {
	e, layer := layerMigrateEnv(t)
	bad := filepath.Join(e.ws, "layers", "solo.jsonc")
	writeMigrateFile(t, bad, `{"packs": ["~/solo"]}`+"\n")
	rc, out, errw := runConfigMigrateVerb(t, "--yes", layer, bad)
	if rc != 1 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if !strings.Contains(out, "run `yolo config migrate` again") || !strings.Contains(errw, "Nothing was written for any of the files named") {
		t.Errorf("the refusal names no next step:\nstdout:\n%s\nstderr:\n%s", out, errw)
	}
	if present(filepath.Join(e.ws, "layers", "work.toml")) || present(layer+".pre-migrate") {
		t.Fatal("a refused run converted the other named file")
	}
	if rc, out, _ := runConfigMigrateVerb(t, "--dry-run", layer); rc != 0 || !strings.Contains(out, "Dry run; nothing was written.") || present(filepath.Join(e.ws, "layers", "work.toml")) {
		t.Fatalf("--dry-run FILE: rc=%d\n%s", rc, out)
	}
}

// In a jail a FILE outside the workspace is refused, naming the command to run on the host; one
// inside it is converted.
func TestConfigMigrateLayerInAJail(t *testing.T) {
	e, layer := layerMigrateEnv(t)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	t.Setenv("YOLO_WORKSPACE", e.ws)
	outside := filepath.Join(e.home, "work.jsonc")
	writeMigrateFile(t, outside, layerOld)
	rc, _, errw := runConfigMigrateVerb(t, "--yes", outside)
	if rc != 1 || !strings.Contains(errw, "Run `yolo config migrate "+outside+"` on the host") {
		t.Fatalf("rc=%d, stderr:\n%s", rc, errw)
	}
	if present(filepath.Join(e.home, "work.toml")) {
		t.Fatal("a jail converted a file outside its workspace")
	}
	rc, out, errw := runConfigMigrateVerb(t, "--yes", layer)
	if rc != 0 || !present(filepath.Join(e.ws, "layers", "work.toml")) {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
}

// FILE arguments are positional; an unknown flag is still refused, naming what the verb takes.
func TestConfigMigrateArguments(t *testing.T) {
	_, layer := layerMigrateEnv(t)
	rc, _, errw := runConfigMigrateVerb(t, "--user-layer", layer)
	if rc != 2 || !strings.Contains(errw, "--dry-run, --yes and file paths") {
		t.Fatalf("an unknown flag: rc=%d %s", rc, errw)
	}
	rc, out, _ := runConfigMigrateVerb(t, "--help")
	if rc != 0 || !strings.Contains(out, "[FILE...]") {
		t.Fatalf("--help: rc=%d\n%s", rc, out)
	}
	rc, out, errw = runConfigMigrateVerb(t, "--dry-run", "--", layer)
	if rc != 0 || !strings.Contains(out, "Found 1 file in the old format") {
		t.Fatalf("FILE after --: rc=%d\n%s\n%s", rc, out, errw)
	}
}
