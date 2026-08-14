package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeResolver backs LoopholeResolver with a fixed known set (the file-backed
// loopholes a real Resolver would discover).
type fakeResolver map[string]LoopholeInfo

func (f fakeResolver) Known() (map[string]LoopholeInfo, bool) { return f, true }

// R5 of loophole-packaging.md found knownHostServiceKeys contradicting the rest
// of the loophole machinery: the loader reads `description` and `doctor_cmd`
// (discover.go), and validateInlineService itself prefix-checks `jail_endpoint`
// — yet all three were "unknown key" errors on an inline entry. The census is
// reconciled: every key the loader reads validates.
func TestInlineLoopholeKeysLoaderReadsAreKnown(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {
		"description": "a test service",
		"command": ["/bin/true"],
		"env": {"A": "b"},
		"doctor_cmd": ["/bin/true", "--ok"],
		"jail_endpoint": "`+paths.JailHostServicesDir+`/svc.endpoint"
	}}}`)
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — every key the loader reads must be a known inline key", errs)
	}
}

// jail_endpoint becoming a KNOWN key must not lose its prefix rule: the value
// still has to live under the jail host-services dir.
func TestInlineJailEndpointStillPrefixChecked(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {
		"command": ["/bin/true"],
		"jail_endpoint": "/tmp/evil.endpoint"
	}}}`)
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	var hit []string
	for _, e := range errs {
		if strings.Contains(e, "jail_endpoint") {
			hit = append(hit, e)
		}
	}
	if len(hit) != 1 || !strings.Contains(hit[0], "must start with "+paths.JailHostServicesDir+"/") {
		t.Errorf("jail_endpoint errors = %v, want exactly the prefix error (not an unknown-key error)", hit)
	}
}
