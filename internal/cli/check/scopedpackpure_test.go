package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestSectionPacksDirectScopedReadNeverStagesSelectedCarrier(t *testing.T) {
	user := testconfig.User(t, "packs=[{pack='local/corpus',skills={dir='raw',only=['chosen']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	if err := os.MkdirAll(filepath.Join(source, "raw/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "raw/chosen/SKILL.md"), []byte("chosen\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "pack.toml"), []byte("poison [\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	var out bytes.Buffer
	r := newReporter(&out, false)
	o := &Options{packTreeFactory: func() (string, error) { return tree, nil }}
	o.sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Fatalf("read-only scope failed: %s", out.String())
	}
	found := false
	for _, p := range o.selectedPacks {
		if p.Projection == nil {
			continue
		}
		found = true
		if p.Root != source || p.SourceRoot != "" {
			t.Fatalf("pure check materialized scoped carrier: root=%s source=%s", p.Root, p.SourceRoot)
		}
	}
	if !found {
		t.Fatalf("scoped selection not inspected: %s", out.String())
	}
}

func TestSectionPacksNeverFetchedScopedSelectionIsUnverified(t *testing.T) {
	user := testconfig.User(t, "packs=[{git='https://example.invalid/fixture/corpus',ref='main',skills={only=['chosen']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 || !strings.Contains(out.String(), "selection not yet verified; resolved at next launch") {
		t.Fatalf("unverified scope: failed=%d %s", r.failed, out.String())
	}
	for _, forbidden := range []string{"candidates:", "selected:", "license:", "omitted"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("unverified invented %q: %s", forbidden, out.String())
		}
	}
}
