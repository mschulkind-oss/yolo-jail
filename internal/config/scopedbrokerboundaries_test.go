package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestScopedMetadataNeverEntersInheritedUserScopes(t *testing.T) {
	values := testConfigMap(t, `{"contexts":{"host":{"source":"fixture","profile":"synthetic-host-selector"}},"scopes":{"host":{"source":"fixture","resource":"s3://b","context":"host","allow":{"never":["all"]}}}}`)
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		filtered, unclassified := FilterInherit(values, scope)
		if len(unclassified) != 0 {
			t.Fatalf("%s left roots unclassified: %v", scope, unclassified)
		}
		for _, key := range []string{"contexts", "scopes"} {
			if _, has := filtered.Get(key); has {
				t.Fatalf("%s inherited host metadata %s", scope, key)
			}
		}
		body, err := configdoc.Encode(filtered, configdoc.TOML)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "synthetic-host-selector") {
			t.Fatal("host selector crossed the jail user-scope boundary")
		}
	}
}

func TestScopedVocabularyLegacyValidationHandlesAbsentPolicyTables(t *testing.T) {
	for _, fixture := range []string{
		`{"packs":["aws"]}`,
		`{"packs":["git"],"allow":{"git":{"always":["fetch"]}}}`,
		`{"packs":["github"],"allow":{"github":{"sets":[{"name":"review","mode":"standing"}]}}}`,
	} {
		t.Run(fixture, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv(UserLayerEnv, "")
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), fixture)
			workspace := t.TempDir()
			merged, err := LoadConfig(workspace, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			errors, _ := ValidateConfig(merged, workspace, nil)
			if len(errors) != 0 {
				t.Fatalf("unchanged legacy policy failed validation: %v", errors)
			}
		})
	}
}
