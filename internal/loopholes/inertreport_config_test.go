package loopholes

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ApplyConfigEnabled is the rule the launch's platform-inert report lays over manifest-only
// records (G15, docs/plans/setup-support-gaps.md). What it must get right is the difference
// between "the config said false" and "the config said nothing": only the second may leave the
// author's default standing. The launch-level pin — that the report actually calls it — is
// run.TestLaunchReportsAUserEnabledPlatformInertLoophole.
func TestApplyConfigEnabledIsTheUsersSwitchWhereSetAndTheDefaultElsewhere(t *testing.T) {
	on := &Loophole{Name: "switched-on", Enabled: false}
	off := &Loophole{Name: "switched-off", Enabled: true}
	untouched := &Loophole{Name: "untouched", Enabled: true}
	cfg := jsonx.NewOrderedMap()
	onSpec := jsonx.NewOrderedMap()
	onSpec.Set("enabled", true)
	cfg.Set("switched-on", onSpec)
	offSpec := jsonx.NewOrderedMap()
	offSpec.Set("enabled", false)
	cfg.Set("switched-off", offSpec)
	cfg.Set("untouched", jsonx.NewOrderedMap()) // an entry with no `enabled` key

	ApplyConfigEnabled([]*Loophole{on, off, untouched, nil}, cfg)
	if !on.Enabled {
		t.Error("config enabled:true did not switch ON a default-off loophole")
	}
	if off.Enabled {
		t.Error("config enabled:false did not switch OFF a default-on loophole")
	}
	if !untouched.Enabled {
		t.Error("an entry that never set `enabled` overrode the author's default")
	}

	lp := &Loophole{Name: "x", Enabled: true}
	ApplyConfigEnabled([]*Loophole{lp}, nil)
	if !lp.Enabled {
		t.Error("a nil config changed a record")
	}
}
