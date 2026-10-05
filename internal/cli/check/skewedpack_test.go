package check

// skewedpack_test.go pins `yolo check` over a configured pack holding a contribution this yolo
// cannot read (docs/design/patched-forks.md PF-D60): a WARNING naming it, not a failure, because
// the launch runs the pack without it and says so; and a pass with nothing to say when the same
// pack is readable whole.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestCheckWarnsAboutAContributionThisYoloCannotRead(t *testing.T) {
	for _, newer := range []bool{true, false} {
		dir := t.TempDir()
		field := ""
		if newer {
			field = `, "field_from_a_newer_yolo": 1`
		}
		manifest := `{"name": "acme", "contributes": [
		  {"kind": "env", "vars": {"ACME": "1"}` + field + `},
		  {"kind": "env", "vars": {"OTHER": "1"}}]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		packsFixture(t, `{"packs": ["file://`+dir+`"]}`)

		var buf bytes.Buffer
		r := &reporter{w: &buf}
		(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r,
			jsonx.NewOrderedMap())
		report := buf.String()
		if r.failed != 0 {
			t.Errorf("newer=%v: a contribution the launch skips must not FAIL the check:\n%s", newer, report)
		}
		warned := strings.Contains(report, `unknown field "field_from_a_newer_yolo"`)
		if warned != newer || (newer && r.warned == 0) {
			t.Errorf("newer=%v: the check must warn exactly when a contribution is skipped "+
				"(warned=%v, graded=%d):\n%s", newer, warned, r.warned, report)
		}
		if newer {
			for _, want := range []string{": contributes[0]: skipping the env contribution", "yolo update", "yolo pack lint"} {
				if !strings.Contains(report, want) {
					t.Errorf("the warning must contain %q:\n%s", want, report)
				}
			}
		}
	}
}
