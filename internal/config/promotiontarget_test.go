package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromotionTargetReadsOnlyUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := PromotionTarget(); got != "local" {
		t.Fatalf("PromotionTarget() without config = %q, want local", got)
	}

	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"promotion_target":"pack:workspace"}`)
	write(t, userCfg, `{"promotion_target":"pack:matt"}`)
	if got := PromotionTarget(); got != "pack:matt" {
		t.Errorf("PromotionTarget() = %q, want pack:matt", got)
	}
}

func TestValidatePromotionTarget(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"promotion_target":"pack:workspace"}`)

	var errs []string
	validatePromotionTarget(decode(t, `{"promotion_target":"pack:workspace"}`), ws, &errs)
	if !strings.Contains(strings.Join(errs, "\n"), "user-scope only") {
		t.Fatalf("workspace promotion_target errors = %v, want user-scope-only refusal", errs)
	}

	for _, body := range []string{
		`{"promotion_target":"local"}`,
		`{"promotion_target":"pack:matt"}`,
		`{"promotion_target":null}`,
		`{}`,
	} {
		errs = nil
		validatePromotionTarget(decode(t, body), t.TempDir(), &errs)
		if len(errs) != 0 {
			t.Errorf("validatePromotionTarget(%s) errors = %v", body, errs)
		}
	}
	for _, body := range []string{
		`{"promotion_target":"host"}`,
		`{"promotion_target":"pack:"}`,
		`{"promotion_target":true}`,
	} {
		errs = nil
		validatePromotionTarget(decode(t, body), t.TempDir(), &errs)
		if len(errs) == 0 {
			t.Errorf("validatePromotionTarget(%s) accepted invalid target", body)
		}
	}
}
