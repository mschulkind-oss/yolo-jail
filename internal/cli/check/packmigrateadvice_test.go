package check

// packmigrateadvice_test.go pins the Packs section's advice for a local pack on its legacy
// manifest: one line naming `yolo pack migrate <dir>`, never a failure or a warning, and none for a
// pack already on pack.toml unless it still writes a legacy provider spelling. Through
// sectionPacks, the call site.

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestCheckAdvisesMigratingALegacyLocalPack(t *testing.T) {
	for i, tc := range []struct {
		file, body string
		advised    bool
	}{
		{"pack.json", `{"name": "oldpack", "contributes": [{"kind": "env", "vars": {"OLDPACK": "1"}}]}`, true},
		{"pack.jsonc", `{"name": "oldpack", // note
 "contributes": [{"kind": "env", "vars": {"OLDPACK": "1"}}]}`, true},
		{"pack.toml", "name = \"oldpack\"\n", false},
		// A pack.toml still writing a legacy provider spelling (RC-D69).
		{"pack.toml", "name = \"oldpack\"\n\n[[provider]]\nname = \"zai\"\nplatform = \"zai\"\n", true},
	} {
		t.Run(tc.file+"_"+strconv.Itoa(i), func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			packsFixture(t, `{"packs": ["file://`+dir+`"]}`)
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
			out := buf.String()
			if r.failed != 0 {
				t.Fatalf("the pack failed the check:\n%s", out)
			}
			want := "`yolo pack migrate " + dir + "`"
			if got := strings.Count(out, want); tc.advised && got != 1 {
				t.Errorf("want one advice line naming %s, got %d:\n%s", want, got, out)
			}
			if !tc.advised && strings.Contains(out, "yolo pack migrate") {
				t.Errorf("a pack on pack.toml was advised to migrate:\n%s", out)
			}
		})
	}
}
