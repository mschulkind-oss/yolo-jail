package hostprocesses

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestLoadConfigMissingFile: a missing file -> empty visible + DEFAULT fields.
func TestLoadConfigMissingFile(t *testing.T) {
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nope.jsonc"))
	if len(cfg.Visible) != 0 {
		t.Errorf("missing-file visible = %v, want empty", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("missing-file fields = %v, want DEFAULT", cfg.Fields)
	}
}
