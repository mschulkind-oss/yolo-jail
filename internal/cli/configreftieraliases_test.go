package cli

// configreftieraliases_test.go: `yolo config-ref` names every conventional tier alias the
// derive helper warns about (docs/research/extension-model-defaults.md OQ-XM2 added
// `frontier`). It reads the list from luahook rather than retyping it, so adding a fifth alias
// there without documenting it fails here.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
)

func TestConfigRefNamesEveryConventionalTierAlias(t *testing.T) {
	ref := configRefText(t)
	start := strings.Index(ref, "      models         {alias: model id}")
	if start < 0 {
		t.Fatal("config-ref has no providers.models field; this test's anchor moved")
	}
	end := strings.Index(ref[start:], "      capabilities")
	if end < 0 {
		t.Fatal("config-ref's providers.models field has no following capabilities field")
	}
	field := ref[start : start+end]
	for _, alias := range luahook.ConventionalModelAliases {
		if !strings.Contains(field, "`"+alias+"`") {
			t.Errorf("config-ref's providers.models field does not name the conventional tier "+
				"alias %q:\n%s", alias, field)
		}
	}
}
