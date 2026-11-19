package configmigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedPackMigrationRetainsLegacyFiltersAndNamesManualRewrite(t *testing.T) {
	for _, key := range []string{"only", "exclude", "name"} {
		t.Run(key, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			source := filepath.Join(dir, "packs", "corpus")
			write(t, filepath.Join(source, "skills", "chosen", "SKILL.md"), "fixture\n")
			value := `["skills/chosen/**"]`
			if key == "name" {
				value = `"old-alias"`
			}
			old := fmt.Sprintf("{\"packs\":[{\"source\":%q,%q:%s}]}\n", "file://"+filepath.ToSlash(source), key, value)
			path := filepath.Join(dir, "config.jsonc")
			write(t, path, old)
			graph, err := Discover(Request{UserConfigDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			p, err := Plan(graph)
			if err == nil || p == nil || p.Equal {
				t.Fatalf("expected retained unequal-plan refusal: %+v %v", p, err)
			}
			if len(p.NotConverted) != 1 {
				t.Fatalf("legacy entry lost its retention reason: %+v", p.NotConverted)
			}
			text := p.Transcript()
			for _, want := range []string{"skills.only", "skills.exclude", "manual", "not equivalent"} {
				if !strings.Contains(text, want) {
					t.Errorf("retention advice misses %q: %s", want, text)
				}
			}
			if _, err := Apply(p, ApplyOptions{}); err == nil {
				t.Fatal("applying a retained unequal plan succeeded")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != old {
				t.Fatalf("retained legacy bytes changed: %q %v", data, err)
			}
			if exists(filepath.Join(dir, "config.toml")) || exists(path+".pre-migrate") {
				t.Fatal("retained legacy file was replaced or backed up as converted")
			}
		})
	}
}
