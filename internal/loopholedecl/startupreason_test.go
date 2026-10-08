package loopholedecl_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestStartupReasonOptInDefaultsOffAndRequiresBoolean(t *testing.T) {
	m, err := decodeMap(t, "checked", launchCheckManifest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if m.HostDaemon.StartupReason {
		t.Fatal("legacy daemon unexpectedly opted into startup_reason")
	}
	m, err = decodeMap(t, "checked", launchCheckManifest(map[string]any{"startup_reason": true}))
	if err != nil || !m.HostDaemon.StartupReason {
		t.Fatalf("startup_reason true: decoded=%+v err=%v", m, err)
	}
	for _, value := range []any{"true", 1, ""} {
		_, err := decodeMap(t, "checked", launchCheckManifest(map[string]any{"startup_reason": value}))
		if err == nil || !strings.Contains(err.Error(), "startup_reason") {
			t.Errorf("startup_reason=%#v: err=%v, want a field-specific refusal", value, err)
		}
	}
	_, err = decodeMap(t, "checked", launchCheckManifest(map[string]any{"startup_reaso": true}))
	if err == nil || !strings.Contains(err.Error(), "startup_reaso") {
		t.Errorf("misspelled startup_reason was accepted: %v", err)
	}
	if _, _, err := loopholedecl.DecodeTolerant(manifestBytes(t,
		launchCheckManifest(map[string]any{"startup_reaso": true})), filepath.Join("/loopholes", "checked")); err != nil {
		t.Fatal(err)
	}
}
