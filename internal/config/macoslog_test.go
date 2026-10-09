package config

import (
	"strings"
	"testing"
)

// macoslog_test.go pins the RETIREMENT of the top-level `macos_log` key (2026-10-09). It
// dialled an in-sandbox wrapper around /usr/bin/log that read nothing, because the macos-user
// sandbox account cannot read the unified log (packs/macos-log/README.md). The log is the
// `macos-log` loophole's now, and the key is a refusal naming it, as `journal` is.

// Every value the key ever took is refused on the host, once, with the three steps that
// replace it and the scope of the one that widens.
func TestRetiredMacosLogKeyIsRefusedAndNamesItsReplacement(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	for _, body := range []string{`"off"`, `"user"`, `"full"`, `true`, `null`} {
		errs, warns := ValidateConfig(decode(t, `{"macos_log": `+body+`}`), t.TempDir(), nil)
		hits := containing(errs, "config.macos_log")
		if len(hits) != 1 {
			t.Errorf("macos_log %s: errors = %v, want ONE refusal (and no generic unknown-key "+
				"error beside it)", body, errs)
			continue
		}
		for _, want := range []string{
			"REMOVED", `"packs": ["macos-log"]`, `"loopholes": {"macos-log": {"enabled": true}}`,
			`{"settings": {"full": true}}`, "USER-CONFIG-ONLY", "~/.config/yolo-jail/config.jsonc",
			"Delete the key", "yolo check",
		} {
			if !strings.Contains(hits[0], want) {
				t.Errorf("macos_log %s: refusal %q does not name %q", body, hits[0], want)
			}
		}
		if len(containing(warns, "macos_log")) != 0 {
			t.Errorf("macos_log %s: the refusal is also a warning: %v", body, warns)
		}
	}
	if errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil); len(containing(errs, "macos_log")) != 0 {
		t.Errorf("a config without the key is refused: %v", errs)
	}
}

// In a jail the config is the host-generated snapshot, so the refusal is a warning there.
func TestRetiredMacosLogKeyOnlyWarnsInsideAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.12.2")
	errs, warns := ValidateConfig(decode(t, `{"macos_log": "user"}`), t.TempDir(), nil)
	if len(containing(errs, "macos_log")) != 0 || len(containing(warns, "config.macos_log")) != 1 {
		t.Errorf("in-jail: errs %v warns %v, want one warning", errs, warns)
	}
}

// The key is listed as retired, so config-ref's coverage check does not demand it be documented.
func TestMacosLogIsARetiredKey(t *testing.T) {
	for _, k := range TopLevelConfigKeys() {
		if k == "macos_log" {
			t.Error("macos_log is still a live top-level key")
		}
	}
	found := false
	for _, k := range RetiredConfigKeys() {
		found = found || k == "macos_log"
	}
	if !found {
		t.Error("macos_log is not in RetiredConfigKeys")
	}
}
