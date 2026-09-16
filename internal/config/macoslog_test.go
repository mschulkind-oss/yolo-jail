package config

import (
	"strings"
	"testing"
)

// macoslog_test.go covers the `macos_log` dial's arrival in the schema.
//
// The key was READ, HONORED and DOCUMENTED long before it was ACCEPTED: macos-user's
// bootstrap installed the yolo-log helper from it in all three modes while
// knownTopLevelConfigKeys had no entry, so `config.macos_log: unknown key` refused every
// config that declared it — including the one yolo-log's own remedy text tells the user to
// write (docs/plans/setup-support-gaps.md F1). Hence a test for the mundane fact that the
// key validates at all: that is the fact that was false.

func TestMacosLogIsAcceptedAndEveryModeValidates(t *testing.T) {
	for _, mode := range MacosLogModes {
		errs, warns := ValidateConfig(decode(t, `{"macos_log": "`+mode+`"}`), t.TempDir(), nil)
		for _, e := range errs {
			if strings.Contains(e, "macos_log") {
				t.Errorf("macos_log %q should validate, got error: %s", mode, e)
			}
		}
		for _, w := range warns {
			if strings.Contains(w, "macos_log") {
				t.Errorf("macos_log %q should be quiet, got warning: %s", mode, w)
			}
		}
	}
}

// The enum is checked, and the message names the vocabulary — the same treatment
// ephemeral_storage gets. `journal`'s retirement is why this is worth stating: that key
// carried the identical off/user/full spelling and lost its enum check when it was
// retired, so "off/user/full is validated somewhere in this repo" is not a safe inference.
func TestMacosLogRejectsAnythingOutsideItsVocabulary(t *testing.T) {
	for _, body := range []string{`"verbose"`, `"user "`, `""`, `true`, `3`, `["user"]`} {
		errs, _ := ValidateConfig(decode(t, `{"macos_log": `+body+`}`), t.TempDir(), nil)
		var got []string
		for _, e := range errs {
			if strings.Contains(e, "macos_log") {
				got = append(got, e)
			}
		}
		if len(got) != 1 || !strings.Contains(got[0], "expected one of") {
			t.Errorf("macos_log %s: errors = %v, want one 'expected one of'", body, got)
			continue
		}
		for _, mode := range MacosLogModes {
			if !strings.Contains(got[0], mode) {
				t.Errorf("macos_log %s: %q never names the legal mode %q", body, got[0], mode)
			}
		}
	}
	// An explicit null is "not set", like every other optional key here.
	for _, body := range []string{`{"macos_log": null}`, `{}`} {
		errs, _ := ValidateConfig(decode(t, body), t.TempDir(), nil)
		for _, e := range errs {
			if strings.Contains(e, "macos_log") {
				t.Errorf("%s produced %s", body, e)
			}
		}
	}
}
