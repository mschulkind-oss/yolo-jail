package loopholedecl_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// shippedModules is every loophole module the repository ships, as pack/name pairs.
func shippedModules(t *testing.T) map[string]string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "packs", "*", "loopholes", "*"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, dir := range dirs {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		pack := filepath.Base(filepath.Dir(filepath.Dir(dir)))
		out[pack+"__"+filepath.Base(dir)] = dir
	}
	if len(out) == 0 {
		t.Fatal("no shipped loophole modules found: the equivalence test is not reading the tree it exists for")
	}
	return out
}

// TestEveryShippedLoopholeIsTOMLAndDecodesToWhatItAlwaysDid is RC-D28's equivalence proof. The
// testdata files are each shipped manifest.jsonc as it stood at the conversion (comments removed,
// since a comment decodes to nothing); the module on disk is the loophole.toml that replaced it.
// Both go through the strict decoder and must come out the identical in-memory manifest, every
// field, including declaration order of settings and env keys.
func TestEveryShippedLoopholeIsTOMLAndDecodesToWhatItAlwaysDid(t *testing.T) {
	modules := shippedModules(t)
	legacy, err := filepath.Glob(filepath.Join("testdata", "legacy-shipped", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != len(modules) {
		t.Fatalf("%d legacy fixtures but %d shipped modules: a loophole was added or removed without its fixture", len(legacy), len(modules))
	}
	for _, fixture := range legacy {
		key := strings.TrimSuffix(filepath.Base(fixture), ".json")
		t.Run(key, func(t *testing.T) {
			dir, ok := modules[key]
			if !ok {
				t.Fatalf("no shipped module for fixture %s", key)
			}
			if _, err := os.Stat(filepath.Join(dir, loopholedecl.ManifestName)); err == nil {
				t.Fatalf("%s still holds a legacy %s beside its loophole.toml", dir, loopholedecl.ManifestName)
			}
			old, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			want, err := loopholedecl.Decode(old, dir)
			if err != nil {
				t.Fatalf("legacy fixture does not decode: %v", err)
			}
			got, err := loopholedecl.LoadDir(dir)
			if err != nil {
				t.Fatalf("loophole.toml does not decode strictly: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				wv, gv := reflect.ValueOf(*want), reflect.ValueOf(*got)
				for i := 0; i < wv.NumField(); i++ {
					if !reflect.DeepEqual(wv.Field(i).Interface(), gv.Field(i).Interface()) {
						t.Errorf("%s differs: legacy %+v, TOML %+v", wv.Type().Field(i).Name, wv.Field(i).Interface(), gv.Field(i).Interface())
					}
				}
			}
			tolerant, skipped, err := loopholedecl.LoadDirTolerant(dir)
			if err != nil || len(skipped) != 0 {
				t.Fatalf("tolerant read of loophole.toml: %v, skipped %v", err, skipped)
			}
			if !reflect.DeepEqual(want, tolerant) {
				t.Errorf("the tolerant read of loophole.toml differs from the legacy manifest")
			}
		})
	}
}
