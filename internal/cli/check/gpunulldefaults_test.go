package check

import (
	"fmt"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gpuNullSections builds, for a vendor (nil: unset), a `machine.gpu` section whose leaves are
// absent, one whose leaves are null and one holding config.GPUNullDefaults' values.
func gpuNullSections(vendor any) (absent, null, defaults *jsonx.OrderedMap) {
	absent, null, defaults = jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	for _, s := range []*jsonx.OrderedMap{absent, null, defaults} {
		s.Set("enabled", true)
	}
	for _, d := range config.GPUNullDefaults() {
		leaf := d.Path[len(d.Path)-1]
		if len(d.Path) != 3 || leaf == "enabled" {
			continue
		}
		if leaf == "vendor" && vendor != nil {
			for _, s := range []*jsonx.OrderedMap{absent, null, defaults} {
				s.Set("vendor", vendor)
			}
			continue
		}
		null.Set(leaf, nil)
		defaults.Set(leaf, d.Value)
	}
	return
}

// TestGPUConfigAnswersTheNullDefaults pins `yolo check`'s gpuConfig to config.GPUNullDefaults: an
// absent leaf, a null one and the listed default read as the same GPU, as the launch reads them.
func TestGPUConfigAnswersTheNullDefaults(t *testing.T) {
	read := func(section *jsonx.OrderedMap) string {
		merged := jsonx.NewOrderedMap()
		machine := jsonx.NewOrderedMap()
		machine.Set("gpu", section)
		merged.Set("machine", machine)
		enabled, vendor, _, mode := gpuConfig(merged)
		return fmt.Sprintf("enabled=%t vendor=%s cdi=%t", enabled, vendor, mode == "cdi")
	}
	for _, vendor := range []any{nil, "amd"} {
		absent, null, defaults := gpuNullSections(vendor)
		want := read(absent)
		if got := read(null); got != want {
			t.Errorf("vendor %v: null leaves read %s, absent ones %s", vendor, got, want)
		}
		if got := read(defaults); got != want {
			t.Errorf("vendor %v: the listed defaults read %s, absent leaves %s", vendor, got, want)
		}
	}
}
