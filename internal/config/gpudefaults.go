package config

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// gpudefaults.go is the one place `machine.gpu` (legacy: top-level `gpu`) says what each of its
// leaves means when it is absent or null. The launch's readers (internal/cli/run readableGPU and
// gpuArgs) answer these constants for an absent leaf and for a null one alike, since a reader takes
// only a value of the leaf's type; cli/run's TestGPUReadersAnswerTheNullDefaults fails if one stops.
//
// TOML has no null, and a legacy null under `gpu` reset whatever a lower layer wrote there to that
// default. So the null's TOML spelling is the default itself (RemovalDefault, LowerLegacyNulls),
// and `yolo config migrate` leaves it out where no other file writes the leaf. A whole-section
// null reset the section, which `machine.gpu = false` says.

const (
	// DefaultGPUVendor is `machine.gpu.vendor` when absent or null.
	DefaultGPUVendor = "nvidia"
	// DefaultGPUDevices is `machine.gpu.devices` when absent or null.
	DefaultGPUDevices = "all"
	// DefaultGPUCapabilities is `machine.gpu.capabilities` when absent or null; only the NVIDIA
	// passthrough reads it.
	DefaultGPUCapabilities = "compute,utility"
	// DefaultGPUMode is `machine.gpu.mode` when absent or null; only the AMD passthrough reads it.
	DefaultGPUMode = "devices"
	// DefaultGPUHSAOverride is `machine.gpu.hsa_override_gfx_version` when absent or null: no
	// override is set.
	DefaultGPUHSAOverride = ""
)

// GPUNullDefaults lists every `machine.gpu` key a legacy null may be written at, with the value
// that null means: the section itself, then each leaf.
func GPUNullDefaults() []WrittenDefault {
	gpu := func(leaf string) []string { return []string{"machine", "gpu", leaf} }
	return []WrittenDefault{
		{Path: []string{"machine", "gpu"}, Value: false},
		{Path: gpu("enabled"), Value: false},
		{Path: gpu("devices"), Value: DefaultGPUDevices},
		{Path: gpu("capabilities"), Value: DefaultGPUCapabilities},
		{Path: gpu("vendor"), Value: DefaultGPUVendor},
		{Path: gpu("mode"), Value: DefaultGPUMode},
		{Path: gpu("hsa_override_gfx_version"), Value: DefaultGPUHSAOverride},
		{Path: gpu("seccomp_unconfined"), Value: false},
		{Path: gpu("vaapi"), Value: false},
	}
}

// nullDefault is the value a legacy null at path (readable spelling) means, when GPUNullDefaults
// lists it.
func nullDefault(path []string) (any, bool) {
	for _, d := range GPUNullDefaults() {
		if len(d.Path) != len(path) {
			continue
		}
		same := true
		for i := range path {
			if d.Path[i] != path[i] {
				same = false
				break
			}
		}
		if same {
			return d.Value, true
		}
	}
	return nil, false
}

// EffectiveGPU is the GPU settings a launch reads from cfg (either spelling): every leaf of
// GPUNullDefaults resolved as the readers resolve it, a leaf of the wrong type, absent or null
// reading as its default. `machine.gpu = true` is the section enabled with every other default.
func EffectiveGPU(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	value, _ := MachineValue(cfg, "gpu", "", "gpu")
	section, _ := value.(*jsonx.OrderedMap)
	out := jsonx.NewOrderedMap()
	for _, d := range GPUNullDefaults()[1:] {
		leaf := d.Path[len(d.Path)-1]
		resolved := d.Value
		if section != nil {
			if v, ok := section.Get(leaf); ok {
				switch d.Value.(type) {
				case string:
					if s, isStr := v.(string); isStr {
						resolved = s
					}
				case bool:
					if b, isBool := v.(bool); isBool {
						resolved = b
					}
				}
			}
		} else if leaf == "enabled" {
			if b, isBool := value.(bool); isBool {
				resolved = b
			}
		}
		out.Set(leaf, resolved)
	}
	return out
}

// ApplyGPUNullDefaults spells cfg's `machine.gpu`, in place, as the table of every leaf the
// readers answer: an absent leaf filled with its default, `machine.gpu = true` or `false` as the
// table with `enabled` so, and no section as the table of defaults. It is what the readers make of
// each, spelled as one value, so a conversion that writes a null's default and one that leaves it
// out compare as the one fact they are (ApplyWrittenDefaults' rule).
func ApplyGPUNullDefaults(cfg *jsonx.OrderedMap) {
	if cfg == nil {
		return
	}
	machine, ok := asMap(getOr(cfg, "machine", nil))
	if !ok {
		machine = jsonx.NewOrderedMap()
		cfg.Set("machine", machine)
	}
	value, _ := machine.Get("gpu")
	section, ok := value.(*jsonx.OrderedMap)
	if !ok {
		enabled, isBool := value.(bool)
		if value != nil && !isBool {
			return
		}
		section = jsonx.NewOrderedMap()
		section.Set("enabled", enabled)
		machine.Set("gpu", section)
	}
	for _, d := range GPUNullDefaults()[1:] {
		leaf := d.Path[len(d.Path)-1]
		if _, ok := section.Get(leaf); !ok {
			section.Set(leaf, d.Value)
		}
	}
}
