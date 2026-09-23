package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// OFF unless the value is the boolean true. Absent is the case every existing config is in, so it
// is the one that must keep composing unlabelled briefings; a wrong shape is the validator's to
// report, never a reader's to guess as ON.
func TestBriefingProvenanceDefaultsOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		v    any
		want bool
	}{
		{name: "absent"},
		{name: "null", set: true, v: nil},
		{name: "false", set: true, v: false},
		{name: "a string is not true", set: true, v: "true"},
		{name: "true", set: true, v: true, want: true},
	} {
		cfg := jsonx.NewOrderedMap()
		if tc.set {
			cfg.Set(briefingProvenanceKey, tc.v)
		}
		if got := BriefingProvenance(cfg); got != tc.want {
			t.Errorf("%s: BriefingProvenance = %v, want %v", tc.name, got, tc.want)
		}
	}
	if BriefingProvenance(nil) {
		t.Error("a nil config must read as off")
	}
}

// Through ValidateConfig, so the test fails if the validator stops being registered — the
// direct-call test below cannot see that.
func TestValidateConfigRefusesANonBooleanBriefingProvenance(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set(briefingProvenanceKey, "yes")
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "briefing_provenance: expected a boolean") {
			return
		}
	}
	t.Errorf("ValidateConfig did not report the bad briefing_provenance; errors: %q", errs)
}

func TestBriefingProvenanceRefusesANonBoolean(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set(briefingProvenanceKey, "yes")
	var errs []string
	validateBriefingProvenance(cfg, &errs)
	if len(errs) != 1 || !strings.Contains(errs[0], "briefing_provenance: expected a boolean") {
		t.Errorf("want one shape error naming the key; got %q", errs)
	}
	for _, ok := range []any{true, false, nil} {
		cfg.Set(briefingProvenanceKey, ok)
		errs = nil
		validateBriefingProvenance(cfg, &errs)
		if len(errs) != 0 {
			t.Errorf("%v must validate cleanly; got %q", ok, errs)
		}
	}
}
