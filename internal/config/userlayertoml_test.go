package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A layer `yolo config migrate FILE` converted is a TOML file that names its packs by readable
// handle and carries its own pack_sources (RC-D46, amended 2026-11-01). The launch takes it over a
// TOML user config, over a legacy JSONC one and over none: the pack resolves to the directory the
// handle names, and the config checks pass.
func TestATOMLUserLayerLoadsOverEveryUserConfig(t *testing.T) {
	for _, user := range []string{"none", "toml", "jsonc"} {
		t.Run(user, func(t *testing.T) {
			home := layerHome(t, "")
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
			packDir := filepath.Join(home, "dots", "yolo-packs", "pi-fork")
			write(t, filepath.Join(packDir, "pack.toml"), "name = \"pi-fork\"\n")
			dir := filepath.Join(home, ".config", "yolo-jail")
			switch user {
			case "toml":
				write(t, filepath.Join(dir, paths.UserConfigTOMLName), "[install]\nnix = ['user']\n")
			case "jsonc":
				write(t, filepath.Join(dir, paths.UserConfigLegacyJSONCName), `{"packages": ["user"]}`+"\n")
			}
			layer := filepath.Join(home, "work.toml")
			write(t, layer, "packs = [{ pack = 'yolo-packs/pi-fork', bare = true }]\n\n[install]\nnix = ['layer']\n\n"+
				"[pack_sources.yolo-packs]\npath = '~/dots/yolo-packs'\nbare = true\n")
			if msg := ValidateUserLayer(layer); msg != "" {
				t.Fatalf("the front door refuses the TOML layer: %s", msg)
			}
			t.Setenv(UserLayerEnv, layer)

			entries, problems, err := LoadPackEntries()
			if err != nil || len(problems) != 0 {
				t.Fatalf("the layer's packs do not read: %v %v", problems, err)
			}
			var found bool
			for _, e := range entries {
				if e.Name == "yolo-packs/pi-fork" {
					found = true
					if got, _ := filepath.EvalSymlinks(e.LocalPath); got != mustEval(t, packDir) {
						t.Errorf("yolo-packs/pi-fork resolves to %q, want %q", e.LocalPath, packDir)
					}
				}
			}
			if !found {
				t.Fatalf("the layer's pack is not selected: %+v", entries)
			}
			ws := t.TempDir()
			cfg, src, err := LoadConfigWithSources(ws, true, func(string) {})
			if err != nil {
				t.Fatalf("a TOML --user-layer over a %s user config is refused: %v", user, err)
			}
			errs, _ := ValidateConfig(cfg, ws, nil)
			if errs = src.Annotate(errs); len(errs) != 0 {
				t.Fatalf("the launch refuses a TOML --user-layer over a %s user config: %v", user, errs)
			}
		})
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A command still naming a layer `yolo config migrate FILE` converted is told the new file, in the
// spelling it gave, and how to pass it; a layer that was never there keeps the plain refusal.
func TestAMissingUserLayerNamesItsConvertedTwin(t *testing.T) {
	layerHome(t, "")
	dir := t.TempDir()
	t.Chdir(dir)
	write(t, filepath.Join(dir, "work.toml"), "[install]\nnix = ['layer']\n")
	if err := os.WriteFile(filepath.Join(dir, "work.jsonc.pre-migrate"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := ValidateUserLayer("work.jsonc")
	for _, want := range []string{"work.jsonc is not there", "converted it to work.toml", "`--user-layer work.toml`"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
	if msg := ValidateUserLayer("other.jsonc"); !strings.Contains(msg, "cannot read other.jsonc") || strings.Contains(msg, "converted") {
		t.Errorf("a layer with no twin is not refused plainly: %s", msg)
	}
}
