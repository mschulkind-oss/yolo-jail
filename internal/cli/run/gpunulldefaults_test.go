package run

import (
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestGPUReadersAnswerTheNullDefaults pins the launch's GPU readers (readableGPU, gpuArgs) to
// config.GPUNullDefaults: for each vendor, a section whose leaves are absent, a section whose
// leaves are null, and one whose leaves hold the listed defaults launch with the same arguments.
// `yolo config migrate` writes a legacy null as that default and leaves out one nothing else
// writes, so a reader that answers anything else for an absent or null leaf makes the conversion
// change what a launch does.
func TestGPUReadersAnswerTheNullDefaults(t *testing.T) {
	o := &Options{
		PathExists:  func(string) bool { return true },
		Now:         func() time.Time { return time.Unix(0, 0) },
		IsTTYStdout: func() bool { return false },
		Stdout:      discardBuf(),
		Stderr:      discardBuf(),
	}
	launch := func(section *jsonx.OrderedMap) []string {
		cfg := newConfig("machine", newConfig("gpu", section))
		gpu := readableGPU(cfg)
		return append([]string{gpu.vendor, gpu.mode}, o.gpuArgs(cfg, "podman", true, gpu.vendor)...)
	}
	for _, vendor := range []any{nil, "amd"} {
		absent, null, defaults := jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
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
		want := launch(absent)
		if got := launch(null); !slices.Equal(got, want) {
			t.Errorf("vendor %v: null leaves launch %v, absent ones %v", vendor, got, want)
		}
		if got := launch(defaults); !slices.Equal(got, want) {
			t.Errorf("vendor %v: the listed defaults launch %v, absent leaves %v", vendor, got, want)
		}
		gotRead := jsonx.MustDumpsCompact(config.EffectiveGPU(newConfig("machine", newConfig("gpu", null))))
		if wantRead := jsonx.MustDumpsCompact(config.EffectiveGPU(newConfig("machine", newConfig("gpu", absent)))); gotRead != wantRead {
			t.Errorf("vendor %v: EffectiveGPU reads null leaves as %s, absent ones as %s", vendor, gotRead, wantRead)
		}
	}
}
