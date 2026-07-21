package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// LoopholeInfo is the subset of a discovered loophole that
// validateLoopholeOverride consults. HasHostDaemon is true when the loophole
// declares a host daemon.
type LoopholeInfo struct {
	Name          string
	HasHostDaemon bool
}

// LoopholeResolver supplies the file-backed loophole set (including disabled
// ones) for validation. It is injected; a nil resolver means "no known
// loopholes" (discovery degraded to empty).
//
// Known returns the map of name->info and a boolean that is false when
// discovery failed. A false ok on a truly-empty machine and a false ok on a
// discovery error are indistinguishable to ValidateConfig — both yield the
// empty known set — so callers may simply return (nil, true) for "empty" or
// (nil, false) for "discovery errored"; both behave identically downstream.
type LoopholeResolver interface {
	Known() (map[string]LoopholeInfo, bool)
}

// ValidateConfig returns (errors, warnings) in a fixed append order (a frozen
// contract — the order must not drift). workspace is used for mount-path
// existence checks (config.mounts). resolver supplies known loopholes
// (nil => none).
func ValidateConfig(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver) (errors []string, warnings []string) {
	if workspace == "" {
		workspace = cwd()
	}
	errs := &[]string{}
	warns := &[]string{}

	reportUnknownKeys(config, knownTopLevelConfigKeys, "config", errs)

	validateRuntime(config, errs)
	validateRepoPath(config, errs)
	validateAgents(config, errs, warns)
	validatePackages(config, errs)
	validateMounts(config, workspace, errs, warns)
	validateWorkspaceReadonly(config, errs)
	validatePerSidePaths(config, errs)
	validateHostClaudeFiles(config, errs)
	validateLoopholes(config, resolver, errs, warns)
	validateJournal(config, errs)
	validateKVM(config, errs)
	validateEphemeralStorage(config, errs)
	validateNetwork(config, errs, warns)
	validateSecurity(config, errs)
	validateHostProcesses(config, errs)
	validateMiseTools(config, errs)
	validateLSPServers(config, errs)
	validateMCPPresets(config, errs)
	validateMCPServers(config, errs)
	validateDevices(config, errs, warns)
	validateGPU(config, errs, warns)
	validateResources(config, errs)
	validateIncludeIfFound(config, errs)
	validateAgentsMdExtra(config, errs)
	validateEnvSources(config, errs)
	validateCacheRelocations(config, workspace, errs, warns)

	errors = *errs
	warnings = *warns
	if errors == nil {
		errors = []string{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return errors, warnings
}

func add(list *[]string, s string) { *list = append(*list, s) }

// reportUnknownKeys iterates the mapping's keys in sorted order and appends
// "<path>.<key>: unknown key" for each not in allowed.
func reportUnknownKeys(m *jsonx.OrderedMap, allowed map[string]struct{}, path string, errs *[]string) {
	keys := append([]string(nil), m.Keys()...)
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := allowed[key]; !ok {
			add(errs, path+"."+key+": unknown key")
		}
	}
}

func validateRuntime(config *jsonx.OrderedMap, errs *[]string) {
	runtime, present := config.Get("runtime")
	if !present {
		return
	}
	if strEq(runtime, "docker") {
		add(errs, "config.runtime: 'docker' is no longer supported — "+
			"use 'podman' (Linux) or 'container' (macOS Apple Container)")
		return
	}
	if runtime != nil && !inStrList(paths.AllRuntimes, runtime) {
		add(errs, "config.runtime: expected 'podman', 'container', or 'macos-user'")
	}
}

func validateRepoPath(config *jsonx.OrderedMap, errs *[]string) {
	repoPath, present := config.Get("repo_path")
	if present && repoPath != nil {
		if _, ok := asStr(repoPath); !ok {
			add(errs, "config.repo_path: expected a string path")
		}
	}
}

func validateAgents(config *jsonx.OrderedMap, errs, warns *[]string) {
	agentsV, present := config.Get("agents")
	if !present || agentsV == nil {
		return
	}
	agentsList, ok := asList(agentsV)
	if !ok {
		add(errs, "config.agents: expected a list of agent names "+
			"(valid: "+joinSorted(validAgentSet)+")")
		return
	}
	for idx, name := range agentsList {
		s, ok := asStr(name)
		if !ok {
			add(errs, fmt.Sprintf("config.agents[%d]: expected a string", idx))
		} else if _, valid := validAgentSet[s]; !valid {
			add(errs, fmt.Sprintf("config.agents[%d]: unknown agent '%s'. Valid agents: %s",
				idx, s, joinSorted(validAgentSet)))
		}
	}
	if len(agentsList) == 0 {
		add(warns, "config.agents: empty list — no coding agents will be "+
			"installed in the jail")
	}
}

func validatePackages(config *jsonx.OrderedMap, errs *[]string) {
	packagesV, present := config.Get("packages")
	if !present || packagesV == nil {
		return
	}
	packages, ok := asList(packagesV)
	if !ok {
		add(errs, "config.packages: expected a list")
		return
	}
	for idx, pkgV := range packages {
		path := fmt.Sprintf("config.packages[%d]", idx)
		if s, ok := asStr(pkgV); ok {
			if !packageNameRe.MatchString(s) {
				add(errs, fmt.Sprintf("%s: invalid package name %s; "+
					"expected '<name>' or '<name>.<output>' "+
					"(letters, digits, '_' and '-' only; at most one dot)",
					path, pytext.Repr(s)))
			}
			continue
		}
		pkg, ok := asMap(pkgV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(pkg, knownPackageKeys, path, errs)
		nameV, _ := pkg.Get("name")
		if name, ok := asStr(nameV); !ok {
			add(errs, path+".name: expected a string")
		} else if strings.Contains(name, ".") {
			add(errs, path+".name: dotted output shorthand ('gtk4.dev') is "+
				"string-only; use the 'outputs' field on the object form")
		}
		outputsV, hasOutputs := pkg.Get("outputs")
		if hasOutputs {
			outputs, ok := asList(outputsV)
			allStr := ok
			if ok {
				for _, o := range outputs {
					if _, ok := asStr(o); !ok {
						allStr = false
						break
					}
				}
			}
			if !allStr {
				add(errs, path+`.outputs: expected a list of strings (e.g. ["out", "dev"])`)
			} else {
				for oIdx, o := range outputs {
					out, _ := asStr(o)
					if !packageOutputRe.MatchString(out) {
						add(errs, fmt.Sprintf("%s.outputs[%d]: invalid output name "+
							"%s (common values: out, dev, bin, lib, man, doc)",
							path, oIdx, pytext.Repr(out)))
					}
				}
			}
		}
		_, hasNixpkgs := pkg.Get("nixpkgs")
		hasVersionOverride := false
		for _, k := range []string{"version", "url", "hash"} {
			if _, ok := pkg.Get(k); ok {
				hasVersionOverride = true
				break
			}
		}
		if hasNixpkgs {
			nixpkgsV, _ := pkg.Get("nixpkgs")
			if _, ok := asStr(nixpkgsV); !ok {
				add(errs, path+".nixpkgs: expected a string")
			}
			if hasVersionOverride {
				add(errs, path+": use either nixpkgs pinning or version/url/hash overrides, not both")
			}
		} else if hasVersionOverride {
			for _, k := range []string{"version", "url", "hash"} {
				kv, _ := pkg.Get(k)
				if _, ok := asStr(kv); !ok {
					add(errs, path+"."+k+": expected a string")
				}
			}
		} else if !hasOutputs {
			add(errs, path+": object packages must use 'nixpkgs', "+
				"'version'+'url'+'hash', or 'outputs'")
		}
	}
}

func validateMounts(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	mountsV, present := config.Get("mounts")
	if !present || mountsV == nil {
		return
	}
	mounts, ok := asList(mountsV)
	if !ok {
		add(errs, "config.mounts: expected a list")
		return
	}
	for idx, mountV := range mounts {
		path := fmt.Sprintf("config.mounts[%d]", idx)
		mount, ok := asStr(mountV)
		if !ok {
			add(errs, path+": expected a string")
			continue
		}
		colonIdx := strings.LastIndex(mount, ":")
		hostPath := mount
		if colonIdx > 0 && colonIdx+1 < len(mount) && mount[colonIdx+1] == '/' {
			hostPath = mount[:colonIdx]
			containerPath := mount[colonIdx+1:]
			if !strings.HasPrefix(containerPath, "/") {
				add(errs, path+": container mount path must be absolute")
			}
		}
		if hostPath == "" {
			add(errs, path+": host mount path cannot be empty")
			continue
		}
		resolvedHost := expandAndResolve(hostPath)
		if !pathExists(resolvedHost) {
			add(warns, fmt.Sprintf("%s: host path does not exist and will be skipped: %s",
				path, resolvedHost))
		}
	}
}

func validateWorkspaceReadonly(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("workspace_readonly")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.workspace_readonly: expected a list of strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.workspace_readonly[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if strings.HasPrefix(entry, "/") {
			add(errs, path+": must be a relative path, not absolute")
		} else if containsDotDot(entry) {
			add(errs, path+": must not contain '..' components")
		}
	}
}

func validatePerSidePaths(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("per_side_paths")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.per_side_paths: expected a list of strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.per_side_paths[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if entry == "" || entry == "." {
			add(errs, path+": must name a workspace sub-path")
		} else if strings.HasPrefix(entry, "/") {
			add(errs, path+": must be a relative path, not absolute")
		} else if containsDotDot(entry) {
			add(errs, path+": must not contain '..' components")
		}
	}
}

func validateHostClaudeFiles(config *jsonx.OrderedMap, errs *[]string) {
	// host_claude_files and host_pi_files are validated as two identical blocks
	// in that fixed order.
	validateHostAgentFiles(config, "host_claude_files", errs)
	validateHostAgentFiles(config, "host_pi_files", errs)
}

// validateHostAgentFiles checks a `<agent>_files` key: absent → skip; present
// but not a list → "expected a list of strings"; each entry must be a string
// with no path separator ("must be a filename, not a path").
func validateHostAgentFiles(config *jsonx.OrderedMap, key string, errs *[]string) {
	v, present := config.Get(key)
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, fmt.Sprintf("config.%s: expected a list of strings", key))
		return
	}
	for idx, entryV := range list {
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, fmt.Sprintf("config.%s[%d]: expected a string", key, idx))
		} else if strings.Contains(entry, "/") || strings.Contains(entry, "\\") {
			add(errs, fmt.Sprintf("config.%s[%d]: must be a filename, not a path", key, idx))
		}
	}
}

func validateJournal(config *jsonx.OrderedMap, errs *[]string) {
	journal, present := config.Get("journal")
	if !present || journal == nil {
		return
	}
	if isBool(journal) {
		return
	}
	s, ok := asStr(journal)
	if !ok || !inStrSlice(journalModes, s) {
		add(errs, fmt.Sprintf("config.journal: expected one of %s or a boolean (got %s)",
			pyListRepr(journalModes), pyReprValue(journal)))
	}
}

func validateKVM(config *jsonx.OrderedMap, errs *[]string) {
	kvm, present := config.Get("kvm")
	if !present || kvm == nil {
		return
	}
	if !isBool(kvm) {
		add(errs, "config.kvm: expected a boolean (got "+pyReprValue(kvm)+")")
	}
}

func validateEphemeralStorage(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("ephemeral_storage")
	if !present || v == nil {
		return
	}
	s, ok := asStr(v)
	if !ok || !inStrSlice(ephemeralStorageModes, s) {
		add(errs, fmt.Sprintf("config.ephemeral_storage: expected one of %s (got %s)",
			pyListRepr(ephemeralStorageModes), pyReprValue(v)))
	}
}

func validateNetwork(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("network")
	if !present || v == nil {
		return
	}
	network, ok := asMap(v)
	if !ok {
		add(errs, "config.network: expected an object")
		return
	}
	reportUnknownKeys(network, knownNetworkKeys, "config.network", errs)
	mode, _ := network.Get("mode")
	if mode != nil && !strEq(mode, "bridge") && !strEq(mode, "host") {
		add(errs, "config.network.mode: expected 'bridge' or 'host'")
	}
	ports, portsPresent := network.Get("ports")
	if portsPresent && ports != nil {
		if pl, ok := asList(ports); ok {
			for idx, port := range pl {
				validatePublishPort(port, fmt.Sprintf("config.network.ports[%d]", idx), errs)
			}
		} else {
			add(errs, "config.network.ports: expected a list")
		}
	}
	fhp, fhpPresent := network.Get("forward_host_ports")
	if fhpPresent && fhp != nil {
		if fl, ok := asList(fhp); ok {
			for idx, port := range fl {
				validateForwardHostPort(port, fmt.Sprintf("config.network.forward_host_ports[%d]", idx), errs)
			}
		} else {
			add(errs, "config.network.forward_host_ports: expected a list")
		}
	}
	if strEq(mode, "host") {
		if pv, ok := network.Get("ports"); ok && truthy(pv) {
			add(warns, "config.network.ports: ignored when network.mode is 'host'")
		}
		if fv, ok := network.Get("forward_host_ports"); ok && truthy(fv) {
			add(warns, "config.network.forward_host_ports: ignored when network.mode is 'host'")
		}
	}
}

func validatePortNumber(value any, path string, errs *[]string) {
	port, ok := pyInt(value)
	if !ok {
		add(errs, path+": expected an integer port number")
		return
	}
	if port < 1 || port > 65535 {
		add(errs, path+": port must be between 1 and 65535")
	}
}

func validatePublishPort(value any, path string, errs *[]string) {
	s, ok := asStr(value)
	if !ok {
		add(errs, path+": expected a string like '8000:8000'")
		return
	}
	base := s
	if strings.Contains(base, "/") {
		i := strings.LastIndex(base, "/")
		protocol := base[i+1:]
		base = base[:i]
		if protocol != "tcp" && protocol != "udp" {
			add(errs, path+": protocol must be tcp or udp")
		}
	}
	parts := strings.Split(base, ":")
	var hostPort, containerPort string
	if len(parts) == 2 {
		hostPort, containerPort = parts[0], parts[1]
	} else if len(parts) == 3 {
		hostPort, containerPort = parts[1], parts[2]
	} else {
		add(errs, path+": expected 'host:container' or 'ip:host:container'")
		return
	}
	validatePortNumber(hostPort, path+".host", errs)
	validatePortNumber(containerPort, path+".container", errs)
}

func validateForwardHostPort(value any, path string, errs *[]string) {
	if isBool(value) {
		// A bool counts as an integer port here.
		validatePortNumber(value, path, errs)
		return
	}
	if _, ok := jsonx.AsInt(value); ok {
		validatePortNumber(value, path, errs)
		return
	}
	s, ok := asStr(value)
	if !ok {
		add(errs, path+": expected an int or string like '8080:9090'")
		return
	}
	parts := strings.Split(s, ":")
	if len(parts) == 1 {
		validatePortNumber(parts[0], path, errs)
		return
	}
	if len(parts) == 2 {
		validatePortNumber(parts[0], path+".local", errs)
		validatePortNumber(parts[1], path+".host", errs)
		return
	}
	add(errs, path+": expected '<port>' or '<local>:<host>'")
}

func validateSecurity(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("security")
	if !present || v == nil {
		return
	}
	security, ok := asMap(v)
	if !ok {
		add(errs, "config.security: expected an object")
		return
	}
	reportUnknownKeys(security, knownSecurityKeys, "config.security", errs)
	bt, present := security.Get("blocked_tools")
	if !present || bt == nil {
		return
	}
	list, ok := asList(bt)
	if !ok {
		add(errs, "config.security.blocked_tools: expected a list")
		return
	}
	for idx, toolV := range list {
		path := fmt.Sprintf("config.security.blocked_tools[%d]", idx)
		if _, ok := asStr(toolV); ok {
			continue
		}
		tool, ok := asMap(toolV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(tool, knownBlockedToolKeys, path, errs)
		if nameV, _ := tool.Get("name"); !isStr(nameV) {
			add(errs, path+".name: expected a string")
		}
		for _, key := range []string{"message", "suggestion"} {
			if kv, ok := tool.Get(key); ok && !isStr(kv) {
				add(errs, path+"."+key+": expected a string")
			}
		}
		if bfV, ok := tool.Get("block_flags"); ok {
			if !isStrList(bfV) {
				add(errs, path+".block_flags: expected a list of strings")
			}
		}
	}
}

func validateHostProcesses(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("host_processes")
	if !present || v == nil {
		return
	}
	hp, ok := asMap(v)
	if !ok {
		add(errs, "config.host_processes: expected an object")
		return
	}
	reportUnknownKeys(hp, knownHostProcessesKeys, "config.host_processes", errs)
	for _, listKey := range []string{"visible", "fields"} {
		if val, ok := hp.Get(listKey); ok {
			if !isStrList(val) {
				add(errs, "config.host_processes."+listKey+": expected a list of strings")
			}
		}
	}
}

func validateMiseTools(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mise_tools")
	if !present || v == nil {
		return
	}
	mt, ok := asMap(v)
	if !ok {
		add(errs, "config.mise_tools: expected an object")
		return
	}
	for _, key := range mt.Keys() {
		value, _ := mt.Get(key)
		// Keys of a decoded JSON object are always strings, so only the value
		// (version) type is checked here.
		if _, ok := asStr(value); !ok {
			add(errs, "config.mise_tools."+key+": expected a version string")
		}
	}
}

func validateLSPServers(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("lsp_servers")
	if !present || v == nil {
		return
	}
	lsp, ok := asMap(v)
	if !ok {
		add(errs, "config.lsp_servers: expected an object")
		return
	}
	for _, name := range lsp.Keys() {
		cfgV, _ := lsp.Get(name)
		path := "config.lsp_servers." + name
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object")
			continue
		}
		reportUnknownKeys(cfg, knownLSPServerKeys, path, errs)
		if cmd, _ := cfg.Get("command"); !isStr(cmd) {
			add(errs, path+".command: expected a string")
		}
		if argsV, ok := cfg.Get("args"); ok {
			validateStringList(argsV, path+".args", errs)
		}
		feV, _ := cfg.Get("fileExtensions")
		fe, ok := asMap(feV)
		if !ok {
			add(errs, path+".fileExtensions: expected an object")
		} else {
			for _, ext := range fe.Keys() {
				lang, _ := fe.Get(ext)
				if !isStr(lang) {
					add(errs, path+".fileExtensions: keys and values must be strings")
				}
			}
		}
	}
}

func validateMCPPresets(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mcp_presets")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.mcp_presets: expected an array of preset names")
		return
	}
	for idx, nameV := range list {
		name, ok := asStr(nameV)
		if !ok {
			add(errs, fmt.Sprintf("config.mcp_presets[%d]: expected a string", idx))
		} else if _, valid := validMCPPresets[name]; !valid {
			add(errs, fmt.Sprintf("config.mcp_presets[%d]: unknown preset '%s'. Valid presets: %s",
				idx, name, joinSorted(validMCPPresets)))
		}
	}
}

func validateMCPServers(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mcp_servers")
	if !present || v == nil {
		return
	}
	servers, ok := asMap(v)
	if !ok {
		add(errs, "config.mcp_servers: expected an object")
		return
	}
	for _, name := range servers.Keys() {
		cfgV, _ := servers.Get(name)
		path := "config.mcp_servers." + name
		if cfgV == nil {
			continue
		}
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object or null")
			continue
		}
		reportUnknownKeys(cfg, knownMCPServerKeys, path, errs)
		if cmd, _ := cfg.Get("command"); !isStr(cmd) {
			add(errs, path+".command: expected a string")
		}
		if argsV, ok := cfg.Get("args"); ok {
			validateStringList(argsV, path+".args", errs)
		}
		if envV, ok := cfg.Get("env"); ok {
			env, ok := asMap(envV)
			if !ok {
				add(errs, path+".env: expected an object")
			} else {
				for _, k := range env.Keys() {
					val, _ := env.Get(k)
					if !isStr(val) {
						add(errs, path+".env."+k+": expected string keys and values")
						break
					}
				}
			}
		}
		if reqV, ok := cfg.Get("requires_env"); ok {
			req, ok := asList(reqV)
			if !ok {
				add(errs, path+".requires_env: expected a list of env var names")
			} else {
				for rIdx, varV := range req {
					varName, ok := asStr(varV)
					if !ok || !envVarNameRe.MatchString(varName) {
						add(errs, fmt.Sprintf("%s.requires_env[%d]: invalid env var "+
							"name %s (must match [A-Za-z_][A-Za-z0-9_]*)",
							path, rIdx, pyReprValue(varV)))
					}
				}
			}
		}
	}
}

func validateDevices(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("devices")
	if !present || v == nil {
		return
	}
	devices, ok := asList(v)
	if !ok {
		add(errs, "config.devices: expected a list")
		return
	}
	for idx, deviceV := range devices {
		path := fmt.Sprintf("config.devices[%d]", idx)
		if s, ok := asStr(deviceV); ok {
			if !pathExists(s) {
				add(warns, fmt.Sprintf("%s: device path does not exist and may be skipped: %s", path, s))
			}
			continue
		}
		device, ok := asMap(deviceV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(device, knownDeviceKeys, path, errs)
		_, hasUSB := device.Get("usb")
		_, hasCgroup := device.Get("cgroup_rule")
		if hasUSB == hasCgroup {
			add(errs, path+": expected exactly one of 'usb' or 'cgroup_rule'")
			continue
		}
		if hasUSB {
			usbV, _ := device.Get("usb")
			if usb, ok := asStr(usbV); !ok {
				add(errs, path+".usb: expected a string")
			} else if !usbIDRe.MatchString(usb) {
				add(errs, path+".usb: expected vendor:product hex format like '0bda:2838'")
			}
			if descV, ok := device.Get("description"); ok && !isStr(descV) {
				add(errs, path+".description: expected a string")
			}
		}
		if hasCgroup {
			cgV, _ := device.Get("cgroup_rule")
			if !isStr(cgV) {
				add(errs, path+".cgroup_rule: expected a string")
			}
		}
	}
}

func validateGPU(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("gpu")
	if !present || v == nil {
		return
	}
	gpu, ok := asMap(v)
	if !ok {
		add(errs, "config.gpu: expected an object")
		return
	}
	reportUnknownKeys(gpu, knownGPUKeys, "config.gpu", errs)
	if enabled, ok := gpu.Get("enabled"); ok && enabled != nil && !isBool(enabled) {
		add(errs, "config.gpu.enabled: expected a boolean")
	}
	vendorV, _ := gpu.Get("vendor")
	if vendorV != nil && !strEq(vendorV, "nvidia") && !strEq(vendorV, "amd") {
		add(errs, "config.gpu.vendor: expected 'nvidia' or 'amd'")
	}
	isAMD := strEq(vendorV, "amd")

	if dv, ok := gpu.Get("devices"); ok && dv != nil && !isStr(dv) {
		add(errs, "config.gpu.devices: expected a string ('all', '0', or '0,1')")
	}

	modeV, _ := gpu.Get("mode")
	if modeV != nil {
		if !isAMD {
			add(errs, "config.gpu.mode: only valid when vendor='amd'")
		} else if !strEq(modeV, "devices") && !strEq(modeV, "cdi") {
			add(errs, "config.gpu.mode: expected 'devices' or 'cdi'")
		}
	}

	capV, _ := gpu.Get("capabilities")
	if capV != nil {
		if isAMD {
			add(errs, "config.gpu.capabilities: not supported for vendor='amd' "+
				"(ROCm has no driver-capabilities concept)")
		} else if capsStr, ok := asStr(capV); !ok {
			add(errs, "config.gpu.capabilities: expected a string (e.g. 'compute,utility')")
		} else {
			validCaps := set("compute", "utility", "graphics", "video", "display", "compat32")
			for _, cap := range strings.Split(capsStr, ",") {
				cap = strings.TrimSpace(cap)
				if cap != "" {
					if _, ok := validCaps[cap]; !ok {
						add(errs, fmt.Sprintf("config.gpu.capabilities: unknown capability '%s'. Valid: %s",
							cap, joinSorted(validCaps)))
					}
				}
			}
		}
	}

	gfxV, _ := gpu.Get("hsa_override_gfx_version")
	if gfxV != nil {
		if !isAMD {
			add(errs, "config.gpu.hsa_override_gfx_version: only valid when vendor='amd'")
		} else if !isStr(gfxV) {
			add(errs, "config.gpu.hsa_override_gfx_version: expected a string (e.g. '11.0.0')")
		}
	}

	if seccompV, ok := gpu.Get("seccomp_unconfined"); ok && seccompV != nil && !isBool(seccompV) {
		add(errs, "config.gpu.seccomp_unconfined: expected a boolean")
	}

	vaapiV, hasVaapi := gpu.Get("vaapi")
	if hasVaapi && vaapiV != nil {
		if !isBool(vaapiV) {
			add(errs, "config.gpu.vaapi: expected a boolean")
		} else if truthy(vaapiV) && !isAMD {
			add(errs, "config.gpu.vaapi: currently requires vendor='amd' "+
				"(mesa radeonsi is the only wired-up VA-API driver)")
		} else if truthy(vaapiV) && !truthy(getOr(gpu, "enabled", nil)) {
			add(warns, "config.gpu.vaapi: inert without gpu.enabled=true "+
				"(no devices are passed through)")
		}
	}
}

func validateResources(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("resources")
	if !present || v == nil {
		return
	}
	resources, ok := asMap(v)
	if !ok {
		add(errs, "config.resources: expected an object")
		return
	}
	reportUnknownKeys(resources, knownResourcesKeys, "config.resources", errs)
	memoryV, _ := resources.Get("memory")
	if memoryV != nil {
		if memory, ok := asStr(memoryV); !ok {
			add(errs, "config.resources.memory: expected a string (e.g. '8g', '512m')")
		} else if !memoryRe.MatchString(memory) {
			add(errs, "config.resources.memory: invalid format. "+
				"Use a number with optional suffix: b, k, m, g (e.g. '8g', '512m')")
		}
	}
	cpusV, _ := resources.Get("cpus")
	if cpusV != nil {
		if isBool(cpusV) {
			// A bool counts as an int: true(1)>0 ok, false(0)<=0 -> error.
			n := int64(0)
			if cpusV.(bool) {
				n = 1
			}
			if n <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if n, ok := jsonx.AsInt(cpusV); ok {
			if n <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if f, ok := cpusV.(float64); ok {
			if f <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if s, ok := asStr(cpusV); ok {
			if val, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
				add(errs, "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')")
			} else if val <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else {
			add(errs, "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')")
		}
	}
	pidsV, _ := resources.Get("pids_limit")
	if pidsV != nil {
		// A bool counts as an int. Non-int, or <=0 -> error.
		if isBool(pidsV) {
			n := int64(0)
			if pidsV.(bool) {
				n = 1
			}
			if n <= 0 {
				add(errs, "config.resources.pids_limit: expected a positive integer")
			}
		} else if n, ok := jsonx.AsInt(pidsV); ok {
			if n <= 0 {
				add(errs, "config.resources.pids_limit: expected a positive integer")
			}
		} else {
			add(errs, "config.resources.pids_limit: expected a positive integer")
		}
	}
}

func validateIncludeIfFound(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("include_if_found")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.include_if_found: expected a list of relative path strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.include_if_found[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if entry == "" {
			add(errs, path+": empty string is not a valid path")
		} else if strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~") {
			add(errs, fmt.Sprintf("%s: must be a relative path (got %s); "+
				"absolute paths and '~' are not supported", path, pytext.Repr(entry)))
		}
	}
}

func validateAgentsMdExtra(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("agents_md_extra")
	if present && v != nil && !isStr(v) {
		add(errs, "config.agents_md_extra: expected a string of markdown")
	}
}

func validateEnvSources(config *jsonx.OrderedMap, errs *[]string) {
	if _, hasEnv := config.Get("env"); hasEnv {
		add(errs, "config.env: removed — rename to 'env_sources' (an ordered list where "+
			`strings are KEY=VALUE files and objects are inline {"KEY": "VALUE"} sets). `+
			"See `yolo config-ref`.")
	}
	v, present := config.Get("env_sources")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.env_sources: expected a list of strings (file paths) "+
			"or objects (inline env maps)")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.env_sources[%d]", idx)
		if s, ok := asStr(entryV); ok {
			if s == "" {
				add(errs, path+": empty string is not a valid path")
			}
			continue
		}
		if entry, ok := asMap(entryV); ok {
			for _, key := range entry.Keys() {
				value, _ := entry.Get(key)
				// "" is a valid JSON key, so an empty key is rejected here.
				if key == "" {
					add(errs, path+": inline map keys must be non-empty strings")
				} else if !envVarNameRe.MatchString(key) {
					add(errs, path+"."+key+": invalid variable name "+
						"(must match [A-Za-z_][A-Za-z0-9_]*)")
				}
				if !isStr(value) {
					add(errs, path+"."+key+": expected a string value")
				}
			}
			continue
		}
		add(errs, fmt.Sprintf("%s: expected a string (file path) or object (inline map), got %s",
			path, typeName(entryV)))
	}
}

// validateCacheRelocations enforces the two cache_relocations rules: the key is
// user-scope only, and every entry is shape-valid.
//
// The scope rule exists because a relocation is a read-write host mount and a
// workspace config is agent-editable (see LoadCacheRelocations for the full
// threat model). LoadCacheRelocations already ignores workspace scope entirely,
// so this check is defense-in-depth: without it, a workspace-scoped key is a
// silent no-op that looks like a broken feature.
//
// ValidateConfig only ever receives the MERGED map (cli/run/preflight.go,
// cli/check/check.go; merged in LoadConfig), and the merge carries no
// provenance — so the only way to tell where the key came from is to re-read
// the workspace config. That is one extra file read on a cold path, and much
// cheaper than threading provenance through every caller of the merge.
//
// warns is unused on purpose: a misconfigured relocation is always an error.
// Downgrading any of these to a warning would let the run proceed with the
// cache silently un-relocated, which is the exact failure mode the feature
// exists to prevent.
func validateCacheRelocations(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	v, present := config.Get(cacheRelocationsKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key
		// here proves the workspace config has none either — no re-read needed.
		return
	}
	// Warnings from the re-read are discarded: this same file was already
	// loaded (and any parse problem already reported) by whoever produced the
	// merged config we were handed.
	if wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {}); err == nil && wsCfg != nil {
		if _, atWorkspace := wsCfg.Get(cacheRelocationsKey); atWorkspace {
			add(errs, "config."+cacheRelocationsKey+": user-scope only — move it to "+
				"~/.config/yolo-jail/config.jsonc (a workspace config is "+
				"agent-editable, so it cannot grant read-write host mounts)")
		}
	}
	if v == nil {
		return
	}
	// The target-parent check is skipped inside a jail. Unlike the loader, this
	// runs against the MERGED config, which in a jail is the host-written
	// snapshot (LoadConfig prefers <workspace>/.yolo/config-snapshot.json) or
	// the host user config bind-mounted read-only — either way it carries the
	// host's cache_relocations, whose targets are host paths deliberately not
	// present in the jail's mount namespace. Stat'ing them here would turn a
	// perfectly valid host config into a fatal "parent directory of the target
	// does not exist" on every nested `yolo` run and every in-jail `yolo check`.
	// The shape, scope and duplicate rules still apply everywhere; only the
	// filesystem probe is host-only, and the host run has already done it for
	// real before writing the snapshot.
	_, problems := checkCacheRelocations(v, !inJail())
	for _, p := range problems {
		add(errs, p)
	}
}

// validateStringList checks that values is a list of strings.
func validateStringList(values any, path string, errs *[]string) {
	list, ok := asList(values)
	if !ok {
		add(errs, path+": expected a list")
		return
	}
	for idx, value := range list {
		if !isStr(value) {
			add(errs, fmt.Sprintf("%s[%d]: expected a string", path, idx))
		}
	}
}
