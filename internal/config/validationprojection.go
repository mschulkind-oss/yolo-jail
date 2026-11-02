package config

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

type readableValidationProjection struct {
	changed bool
	env     bool
	errors  []string
}

// projectReadableB2ForValidation gives the still-legacy common validator its B2 fields in the
// vocabulary it already understands. Readers keep the effective map in the readable vocabulary;
// this detached projection is only for the validation boundary.
func projectReadableB2ForValidation(config *jsonx.OrderedMap) (*jsonx.OrderedMap, readableValidationProjection) {
	projected := cloneValidationMap(config)
	projection := readableValidationProjection{}
	if projected == nil {
		return config, projection
	}
	installValue, _ := projected.Get("install")
	install, _ := asMap(installValue)
	if install != nil {
		if value, present := install.Get("nix"); present {
			projected.Set("packages", value)
			projection.changed = true
		}
		if value, present := install.Get("mise"); present {
			projected.Set("mise_tools", withoutOffEntries(value))
			projection.changed = true
		}
		install.Delete("nix")
		install.Delete("mise")
		if install.Len() > 0 {
			projected.Set("install", install)
		} else {
			projected.Delete("install")
		}
	}

	machineValue, _ := projected.Get("machine")
	machine, _ := asMap(machineValue)
	if machine != nil {
		if value, present := machine.Get("gpu"); present {
			if enabled, ok := value.(bool); ok {
				gpu := jsonx.NewOrderedMap()
				gpu.Set("enabled", enabled)
				value = gpu
			}
			projected.Set("gpu", value)
			projection.changed = true
		}
		if value, present := machine.Get("resources"); present {
			projected.Set("resources", value)
			projection.changed = true
		}
		for _, key := range []string{"ports", "forward"} {
			if value, present := machine.Get(key); present {
				projection.errors = append(projection.errors, validateReadablePortList(value, key)...)
				projection.changed = true
			}
		}
		machine.Delete("gpu")
		machine.Delete("resources")
		machine.Delete("ports")
		machine.Delete("forward")
		if machine.Len() > 0 {
			projected.Set("machine", machine)
		} else {
			projected.Delete("machine")
		}
	}

	if envValue, present := projected.Get("env"); present {
		if env, ok := asMap(envValue); ok && readableEnvValidationShape(env) {
			projected.Delete("env")
			projection.changed = true
			projection.env = true
		}
	}

	// A tool or server written "off" is no entry at all (RC-D52), which the legacy validator
	// spells as its absence.
	if lspValue, present := projected.Get("lsp_servers"); present {
		if _, ok := asMap(lspValue); ok {
			projected.Set("lsp_servers", withoutOffEntries(lspValue))
			projection.changed = true
		}
	}

	mcpValue, present := projected.Get("mcp_servers")
	if servers, ok := asMap(mcpValue); present && ok {
		for _, name := range servers.Keys() {
			value, _ := servers.Get(name)
			if value == "off" {
				servers.Set(name, nil)
				projection.changed = true
				continue
			}
			server, ok := asMap(value)
			if !ok {
				continue
			}
			commandValue, hasCommand := server.Get("command")
			command, ok := asList(commandValue)
			if !hasCommand || !ok {
				continue
			}
			valid := true
			for _, part := range command {
				if _, ok := asStr(part); !ok {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			binary := ""
			if len(command) > 0 {
				binary, _ = asStr(command[0])
			}
			server.Set("command", binary)
			projection.changed = true
		}
		projected.Set("mcp_servers", servers)
	}
	return projected, projection
}

func validateReadablePortList(value any, field string) []string {
	path := "config.machine." + field
	entries, ok := asList(value)
	if !ok {
		return []string{path + ": expected a list of tables"}
	}
	first, second := "host", "jail"
	if field == "forward" {
		first, second = "jail", "host"
	}
	var errors []string
	for i, entry := range entries {
		entryPath := fmt.Sprintf("%s[%d]", path, i)
		port, ok := asMap(entry)
		if !ok {
			errors = append(errors, entryPath+": expected a table")
			continue
		}
		for _, key := range port.Keys() {
			switch {
			case key == first || key == second:
			case field == "ports" && key == "address":
				// Any text podman takes before the host port, as the legacy entry's (RC-D53).
				text, ok := mapValueString(port, key)
				if !ok || strings.ContainsAny(text, ":/") {
					errors = append(errors, entryPath+".address: expected an address string like '127.0.0.1', with no ':' or '/'")
				}
			case field == "ports" && key == "protocol":
				if text, _ := mapValueString(port, key); text != "tcp" && text != "udp" {
					errors = append(errors, entryPath+".protocol: expected 'tcp' or 'udp'")
				}
			default:
				errors = append(errors, entryPath+"."+key+": unknown key")
			}
		}
		for _, key := range []string{first, second} {
			value, present := port.Get(key)
			if !present {
				errors = append(errors, entryPath+"."+key+": required")
				continue
			}
			number, ok := jsonx.AsInt(value)
			if !ok || number < 1 || number > 65535 {
				errors = append(errors, entryPath+"."+key+": expected an integer port number from 1 to 65535")
			}
		}
	}
	return errors
}

func readableEnvValidationShape(env *jsonx.OrderedMap) bool {
	for _, key := range env.Keys() {
		if key != "sources" && key != "files" && key != "set" && key != "unset" {
			return false
		}
	}
	if rawSources, present := env.Get("sources"); present {
		sources, ok := asList(rawSources)
		if !ok {
			return false
		}
		for _, entry := range sources {
			if _, ok := asStr(entry); ok {
				continue
			}
			table, ok := asMap(entry)
			if !ok {
				return false
			}
			if _, ok := envSourcesInline(table); !ok {
				return false
			}
		}
	}
	if rawFiles, present := env.Get("files"); present {
		files, ok := asList(rawFiles)
		if !ok {
			return false
		}
		for _, file := range files {
			if _, ok := asStr(file); !ok {
				return false
			}
		}
	}
	if rawSet, present := env.Get("set"); present {
		set, ok := asMap(rawSet)
		if !ok {
			return false
		}
		for _, key := range set.Keys() {
			value, _ := set.Get(key)
			if _, ok := asStr(value); !ok {
				return false
			}
		}
	}
	if rawUnset, present := env.Get("unset"); present {
		unset, ok := asList(rawUnset)
		if !ok {
			return false
		}
		for _, key := range unset {
			if _, ok := asStr(key); !ok {
				return false
			}
		}
	}
	return true
}

func validateReadableEnvironment(config *jsonx.OrderedMap, errors *[]string) {
	envValue, present := config.Get("env")
	env, ok := asMap(envValue)
	if !present || !ok {
		return
	}
	if setValue, present := env.Get("set"); present {
		set, _ := asMap(setValue)
		if set != nil {
			for _, name := range set.Keys() {
				if !envVarNameRe.MatchString(name) {
					add(errors, "config.env.set."+name+": invalid variable name (must match [A-Za-z_][A-Za-z0-9_]*)")
				}
			}
		}
	}
	if sourcesValue, present := env.Get("sources"); present {
		sources, _ := asList(sourcesValue)
		for i, entry := range sources {
			table, ok := asMap(entry)
			if !ok {
				continue
			}
			inline, _ := envSourcesInline(table)
			if inline == nil {
				continue
			}
			for _, name := range inline.Keys() {
				if !envVarNameRe.MatchString(name) {
					add(errors, fmt.Sprintf("config.env.sources[%d]: invalid variable name %s (must match [A-Za-z_][A-Za-z0-9_]*)", i, pyReprValue(name)))
				}
			}
		}
	}
	if unsetValue, present := env.Get("unset"); present {
		unset, _ := asList(unsetValue)
		for i, value := range unset {
			name, _ := asStr(value)
			if !envVarNameRe.MatchString(name) {
				add(errors, fmt.Sprintf("config.env.unset[%d]: invalid variable name %s (must match [A-Za-z_][A-Za-z0-9_]*)", i, pyReprValue(value)))
			}
		}
	}
}

func cloneValidationMap(config *jsonx.OrderedMap) *jsonx.OrderedMap {
	if config == nil {
		return nil
	}
	clone, _ := cloneValidationValue(config).(*jsonx.OrderedMap)
	return clone
}

func cloneValidationValue(value any) any {
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		clone := jsonx.NewOrderedMap()
		for _, key := range current.Keys() {
			item, _ := current.Get(key)
			clone.Set(key, cloneValidationValue(item))
		}
		return clone
	case []any:
		clone := make([]any, len(current))
		for i, item := range current {
			clone[i] = cloneValidationValue(item)
		}
		return clone
	default:
		return value
	}
}

func projectReadableValidationMessages(messages []string, projection readableValidationProjection) []string {
	for i, message := range messages {
		aliases := [][2]string{
			{"config.packages", "config.install.nix"},
			{"config.mise_tools", "config.install.mise"},
			{"config.gpu", "config.machine.gpu"},
			{"config.resources", "config.machine.resources"},
		}
		for _, alias := range aliases {
			message = replaceReadableDiagnosticPrefix(message, alias[0], alias[1])
		}
		messages[i] = message
	}
	return messages
}

func replaceReadableDiagnosticPrefix(message, before, after string) string {
	if !strings.HasPrefix(message, before) {
		return message
	}
	if len(message) > len(before) && !strings.ContainsRune(".[: ", rune(message[len(before)])) {
		return message
	}
	return after + message[len(before):]
}

// withoutOffEntries is value, when it is a table, without its entries written "off" (a removal,
// RC-D52); any other value as it is.
func withoutOffEntries(value any) any {
	table, ok := asMap(value)
	if !ok {
		return value
	}
	out := jsonx.NewOrderedMap()
	for _, key := range table.Keys() {
		entry, _ := table.Get(key)
		if entry == "off" {
			continue
		}
		out.Set(key, entry)
	}
	return out
}

func mapValueString(m *jsonx.OrderedMap, key string) (string, bool) {
	value, _ := m.Get(key)
	text, ok := value.(string)
	return text, ok
}
