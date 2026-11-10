package run

import (
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

type gpuConfigValues struct {
	options *jsonx.OrderedMap
	enabled bool
	vendor  string
	mode    string
}

func readableGPU(cfg *jsonx.OrderedMap) gpuConfigValues {
	value, _ := config.MachineValue(cfg, "gpu", "", "gpu")
	settings := gpuConfigValues{vendor: config.DefaultGPUVendor, mode: config.DefaultGPUMode}
	gpu, _ := value.(*jsonx.OrderedMap)
	settings.options = gpu
	if enabled, ok := value.(bool); ok {
		settings.enabled = enabled
	}
	if gpu != nil {
		settings.enabled = mapBoolOr(gpu, "enabled", false)
		settings.vendor = mapStrOr(gpu, "vendor", config.DefaultGPUVendor)
		settings.mode = mapStrOr(gpu, "mode", config.DefaultGPUMode)
	}
	return settings
}

func readablePublishPorts(cfg *jsonx.OrderedMap) []any {
	machine := cfgMap(cfg, "machine")
	var canonicalValue any
	var canonical bool
	if machine != nil {
		canonicalValue, canonical = machine.Get("ports")
	}
	network := cfgMap(cfg, "network")
	var legacyValue any
	var legacy bool
	if network != nil {
		legacyValue, legacy = network.Get("ports")
	}
	if !canonical {
		if legacy {
			return asAnyList(legacyValue)
		}
		return nil
	}
	entries, _ := canonicalValue.([]any)
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		port, ok := entry.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if spec, ok := config.PublishPortSpec(port); ok {
			out = append(out, spec)
		}
	}
	if legacy {
		out = append(out, asAnyList(legacyValue)...)
	}
	return out
}

func readableForwardPorts(cfg *jsonx.OrderedMap) []any {
	machine := cfgMap(cfg, "machine")
	var canonicalValue any
	var canonical bool
	if machine != nil {
		canonicalValue, canonical = machine.Get("forward")
	}
	network := cfgMap(cfg, "network")
	var legacyValue any
	var legacy bool
	if network != nil {
		legacyValue, legacy = network.Get("forward_host_ports")
	}
	if !canonical {
		if legacy {
			return asAnyList(legacyValue)
		}
		return nil
	}
	entries, _ := canonicalValue.([]any)
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		port, ok := entry.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		jail, jailOK := jsonx.AsInt(mapGet(port, "jail"))
		host, hostOK := jsonx.AsInt(mapGet(port, "host"))
		if jailOK && hostOK {
			out = append(out, strconv.FormatInt(jail, 10)+":"+strconv.FormatInt(host, 10))
		}
	}
	if legacy {
		out = append(out, asAnyList(legacyValue)...)
	}
	return out
}
