package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func localPack(t *testing.T, manifest string) config.PackEntry {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestTOMLName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.PackEntry{Name: "mine", Source: "file://" + dir}
}

// RC-D69: a local pack.toml still writing a legacy provider spelling gets a note naming the
// verb that rewrites it; the keyed form, and a profile the verb cannot fold, get none.
func TestCheckNotesALegacyProviderSpellingNamingPackMigrate(t *testing.T) {
	legacy := localPack(t, "name = \"mine\"\n\n[[provider]]\nname = \"zai\"\nplatform = \"zai\"\n\n[[profile]]\nname = \"z\"\nprovider = \"zai\"\n")
	advice := packMigrateAdvice(legacy)
	for _, want := range []string{`[[provider]] name = "zai"`, `[[profile]] name = "z"`, "yolo pack migrate ", "[providers.<id>]"} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice %q lacks %q", advice, want)
		}
	}
	if advice := packMigrateAdvice(localPack(t, "name = \"mine\"\n\n[providers.zai]\nplatform = \"zai\"\n")); advice != "" {
		t.Errorf("the keyed form got advice %q", advice)
	}
	if advice := packMigrateAdvice(localPack(t, "name = \"mine\"\n\n[[profile]]\nname = \"z\"\nprovider = \"someone-elses\"\n")); advice != "" {
		t.Errorf("a profile over another pack's provider got advice %q", advice)
	}
}
