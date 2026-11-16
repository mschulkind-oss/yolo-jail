package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func semanticModelViewPack(t *testing.T, contributes string) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"name":"company","description":"d","contributes":[`+contributes+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(dir, "company")
	if p == nil || len(problems) != 0 {
		t.Fatalf("company fixture: %v", problems)
	}
	return p
}

// The edit-validation path (`yolo config set|add|unset`, validateProvidersForExplicit) for a
// config that still selects through legacy profiles: the whole-table reader
// NormalizeLegacyProviderConsumers must not start refusing because a pack scoped an `only`.
func TestBuildReadableSemanticInputsIgnoresComposerModelViews(t *testing.T) {
	const env = `{"kind":"env","vars":{"REVIEW_FIXTURE":"1"}}`
	const only = `"only":["some/model"]`
	for _, tc := range []struct{ name, contributes string }{
		{"no models contribution (control)", env},
		{"scoped only", `{"kind":"models","provider":"openrouter",` + only + `,"agents":["claude"]}`},
		{"unscoped only (baseline behavior)", `{"kind":"models","provider":"openrouter",` + only + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packs := append(selectedEmbeddedPacks(t, "pi", "openrouter"), semanticModelViewPack(t, tc.contributes))
			user := editReadableMap(t, `{"profiles":{"mine":{"provider":"openrouter"}}}`)
			_, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
				Config: editReadableMap(t, `{}`), User: user, Packs: packs,
				Catalog: map[string]map[string]packdecl.ProviderModel{},
			})
			t.Logf("err=%v", err)
			if tc.name == "unscoped only (baseline behavior)" {
				if err == nil || !strings.Contains(err.Error(), "models_only") {
					t.Errorf("baseline models_only refusal lost: %v", err)
				}
			} else if err != nil {
				t.Errorf("edit validation refused: %v", err)
			}
		})
	}
}
