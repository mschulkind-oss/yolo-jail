package config

import "sort"

// SchemaConstants returns a drift-comparable snapshot of the config-schema
// constants (config.py:75-148) for the cross-language drift dump
// (cmd/yolo-parity ↔ tools/parity/py_drift_dump.py). Every value is a SORTED
// []string so the comparison is order-insensitive for the Python `set`/`dict`
// literals (whose iteration order is arbitrary) and deterministic for both
// sides. The audit (2026-07-18 §7) found zero config constants were drift-
// covered, which let host_pi_files silently drop out of the known-keys set;
// dumping them here closes that gap.
//
// Regex patterns are dumped by their SOURCE string; the mise defaults are
// dumped as "key=value" pairs (sorted) so both the keys and values are pinned.
func SchemaConstants() map[string][]string {
	return map[string][]string{
		"DEFAULT_HOST_CLAUDE_FILES":    sortedCopy(DefaultHostClaudeFiles),
		"DEFAULT_HOST_PI_FILES":        sortedCopy(DefaultHostPiFiles),
		"KNOWN_TOP_LEVEL_CONFIG_KEYS":  sortedSet(knownTopLevelConfigKeys),
		"JOURNAL_MODES":                sortedCopy(journalModes),
		"EPHEMERAL_STORAGE_MODES":      sortedCopy(ephemeralStorageModes),
		"KNOWN_NETWORK_KEYS":           sortedSet(knownNetworkKeys),
		"KNOWN_SECURITY_KEYS":          sortedSet(knownSecurityKeys),
		"KNOWN_BLOCKED_TOOL_KEYS":      sortedSet(knownBlockedToolKeys),
		"KNOWN_HOST_PROCESSES_KEYS":    sortedSet(knownHostProcessesKeys),
		"KNOWN_PACKAGE_KEYS":           sortedSet(knownPackageKeys),
		"KNOWN_LSP_SERVER_KEYS":        sortedSet(knownLSPServerKeys),
		"KNOWN_MCP_SERVER_KEYS":        sortedSet(knownMCPServerKeys),
		"KNOWN_DEVICE_KEYS":            sortedSet(knownDeviceKeys),
		"KNOWN_GPU_KEYS":               sortedSet(knownGPUKeys),
		"KNOWN_RESOURCES_KEYS":         sortedSet(knownResourcesKeys),
		"KNOWN_HOST_SERVICE_KEYS":      sortedSet(knownHostServiceKeys),
		"KNOWN_LOOPHOLE_OVERRIDE_KEYS": sortedSet(knownLoopholeOverrideKeys),
		"VAAPI_PACKAGES":               sortedCopy(vaapiPackages),
		"VALID_MCP_PRESETS":            sortedSet(validMCPPresets),
		"DEFAULT_MISE_DISABLED_TOOLS":  sortedCopy(defaultMiseDisabledTools),
		"DEFAULT_MISE_TOOLS":           miseToolPairs(),
		"PACKAGE_NAME_RE":              {packageNameRe.String()},
		"PACKAGE_OUTPUT_RE":            {packageOutputRe.String()},
		"HOST_SERVICE_NAME_RE":         {hostServiceName.String()},
		"USB_ID_RE":                    {usbIDRe.String()},
		"MEMORY_RE":                    {memoryRe.String()},
	}
}

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func miseToolPairs() []string {
	out := make([]string, 0, len(defaultMiseToolsVals))
	for k, v := range defaultMiseToolsVals {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
