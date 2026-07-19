package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// validateLoopholes runs the `host_services = config.get("loopholes")` block
// of _validate_config. Names matching a file-backed loophole are overrides
// (enabled/env/jail_env only); unknown override-shaped names warn; everything
// else is an inline service definition (command required).
func validateLoopholes(config *jsonx.OrderedMap, resolver LoopholeResolver, errs, warns *[]string) {
	v, present := config.Get("loopholes")
	if !present || v == nil {
		return
	}
	hostServices, ok := asMap(v)
	if !ok {
		add(errs, "config.loopholes: expected an object")
		return
	}

	// known_loopholes = _known_loopholes() if host_services else {}
	var known map[string]LoopholeInfo
	if hostServices.Len() > 0 && resolver != nil {
		if k, _ := resolver.Known(); k != nil {
			known = k
		}
	}

	for _, name := range hostServices.Keys() {
		specV, _ := hostServices.Get(name)
		path := "config.loopholes." + name
		// name is always a string key from a decoded JSON object; the
		// isinstance(name,str) half is always true, so only the regex matters.
		if !hostServiceName.MatchString(name) {
			add(errs, "config.loopholes: service name "+pytext.Repr(name)+
				" must match ^[a-zA-Z][a-zA-Z0-9_-]{0,63}$")
			continue
		}
		if name == paths.BuiltinCgroupLoopholeName {
			add(errs, path+": '"+paths.BuiltinCgroupLoopholeName+"' is reserved "+
				"for the built-in cgroup delegate service")
			continue
		}
		spec, ok := asMap(specV)
		if !ok {
			add(errs, path+": expected an object")
			continue
		}
		if info, isKnown := known[name]; isKnown {
			validateLoopholeOverride(name, spec, path, errs, &info)
			continue
		}
		// Override-shaped but no loophole discoverable from here:
		// spec (truthy) and "command" not in spec and set(spec) <= override keys
		if spec.Len() > 0 && !hasKey(spec, "command") && keysSubsetOf(spec, knownLoopholeOverrideKeys) {
			validateLoopholeOverride(name, spec, path, errs, nil)
			add(warns, path+": no loophole named "+pytext.Repr(name)+" is installed on "+
				"this machine — treating the entry as an override of "+
				"a host-side loophole. If the loophole was removed, "+
				"this entry is a no-op; an inline service would need "+
				"a 'command'.")
			continue
		}
		validateInlineService(spec, path, errs)
	}
}

// validateLoopholeOverride ports _validate_loophole_override. info is nil when
// the target is not resolvable on this machine (manifest-dependent checks skip).
func validateLoopholeOverride(name string, spec *jsonx.OrderedMap, path string, errs *[]string, info *LoopholeInfo) {
	if hasKey(spec, "command") {
		add(errs, path+".command: not overridable — "+pytext.Repr(name)+" is an existing "+
			"loophole whose command is fixed by its manifest; only "+
			"'enabled', 'env', and 'jail_env' may be overridden")
	}
	// _report_unknown_keys(spec, KNOWN_LOOPHOLE_OVERRIDE_KEYS | {"command"}, ...)
	allowed := set("enabled", "env", "jail_env", "command")
	reportUnknownKeys(spec, allowed, path, errs)
	if enabledV, ok := spec.Get("enabled"); ok && !isBool(enabledV) {
		add(errs, path+".enabled: expected a boolean (got "+pyReprValue(enabledV)+")")
	}
	if _, ok := spec.Get("env"); ok && info != nil && !info.HasHostDaemon {
		add(errs, path+".env: not applicable — "+pytext.Repr(name)+" has no host daemon, so "+
			"'env' would be silently ignored; use 'jail_env' to set "+
			"variables inside the jail")
	}
	for _, envKey := range []string{"env", "jail_env"} {
		envV, present := spec.Get(envKey)
		if !present || envV == nil {
			continue
		}
		env, ok := asMap(envV)
		if !ok {
			add(errs, path+"."+envKey+": expected an object")
			continue
		}
		for _, k := range env.Keys() {
			val, _ := env.Get(k)
			// k is always a string; only value type can fail.
			if !isStr(val) {
				add(errs, path+"."+envKey+": keys and values must be strings")
				break
			}
		}
	}
}

// validateInlineService runs the inline-service tail of the loopholes block.
func validateInlineService(spec *jsonx.OrderedMap, path string, errs *[]string) {
	reportUnknownKeys(spec, knownHostServiceKeys, path, errs)
	cmdV, present := spec.Get("command")
	if !present || cmdV == nil {
		add(errs, path+".command: required")
	} else if cmd, ok := asList(cmdV); !ok || len(cmd) == 0 {
		add(errs, path+".command: expected a non-empty list of strings")
	} else {
		for ci, ca := range cmd {
			if !isStr(ca) {
				add(errs, path+".command["+itoa(ci)+"]: expected a string, got "+typeName(ca))
			}
		}
	}
	envV, present := spec.Get("env")
	if present && envV != nil {
		env, ok := asMap(envV)
		if !ok {
			add(errs, path+".env: expected an object")
		} else {
			for _, k := range env.Keys() {
				val, _ := env.Get(k)
				if !isStr(val) {
					add(errs, path+".env: keys and values must be strings")
					break
				}
			}
		}
	}
	jsV, present := spec.Get("jail_socket")
	if present && jsV != nil {
		if js, ok := asStr(jsV); !ok {
			add(errs, path+".jail_socket: expected a string")
		} else if !hasPrefix(js, paths.JailHostServicesDir+"/") {
			add(errs, path+".jail_socket: must start with "+
				paths.JailHostServicesDir+"/ "+
				"(got "+pytext.Repr(js)+")")
		}
	}
}
