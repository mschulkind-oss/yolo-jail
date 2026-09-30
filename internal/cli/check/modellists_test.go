package check

// modellists_test.go drives sectionPacks, not modelListNotes, so deleting the call in
// packs.go turns it red (the callee-pinned shape AGENTS.md names).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A `models` contribution whose `only` names an id nothing added, and whose `add` repeats an
// id the provider already lists, is named by the check, as §7.2 says: neither refuses a launch
// (the duplicate keeps its first writer, the unknown id is dropped), so this is where anyone
// hears about them. A contribution that composes clean says nothing.
func TestSectionPacksNamesWhatAModelsContributionCouldNotDo(t *testing.T) {
	dir := t.TempDir()
	manifest := `{
  "name": "policy",
  "contributes": [
    {"kind": "provider", "name": "gw",
     "endpoints": {"openai": {"base_url": "https://gw.example.test/v1"}},
     "models": {"alpha-1": "alpha-1"}},
    {"kind": "models", "provider": "gw", "add": [{"id": "alpha-1", "vendor": "gw"}]},
    {"kind": "models", "provider": "gw", "only": ["alpha-1", "ghost-9"]}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["file://`+dir+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	out := buf.String()
	for _, want := range []string{`adds "alpha-1", which the list already holds`, `names "ghost-9"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the check should say %q:\n%s", want, out)
		}
	}
	if r.failed != 0 {
		t.Errorf("a models note must WARN, never fail: the launch does not refuse it:\n%s", out)
	}
}
