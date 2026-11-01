package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

func validateReadableTOML(values *jsonx.OrderedMap, scope SchemaScope, label string, index *configdoc.Index) error {
	var visit func(any, []string, []configdoc.Step) error
	visit = func(value any, path []string, locationPath []configdoc.Step) error {
		if len(path) > 0 {
			rule, ok := readableSchemaRule(path)
			if !ok {
				return readableTOMLError(label, index, path, locationPath, readableUnknownMessage(path))
			}
			if !readableTypeMatches(rule.Type, value, rule.Removal) && !readableLegacyShapeAccepted(path, value) {
				return readableTOMLError(label, index, path, locationPath, fmt.Sprintf("expected %s (got %s)", schemaTypeName(rule.Type), reprAny(value)))
			}
			userOnly := rule.Scope == SchemaScopeUserOnly || (len(path) == 1 && WorkspaceUserScopeOnlyKey(path[0]))
			if scope == SchemaScopeWorkspaceOnly && userOnly {
				return readableTOMLError(label, index, path, locationPath, "user-scope only; move this key to the user config")
			}
			if scope == SchemaScopeUserOnly && rule.Scope == SchemaScopeWorkspaceOnly {
				return readableTOMLError(label, index, path, locationPath, "workspace-only key; this file is user scope")
			}
			if rule.Type == SchemaTypeAny {
				return nil
			}
		}
		switch current := value.(type) {
		case *jsonx.OrderedMap:
			for _, key := range current.Keys() {
				child, _ := current.Get(key)
				if err := visit(child, appendReadablePath(path, key), appendReadableLocation(locationPath, configdoc.Step{Key: key})); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range current {
				if err := visit(child, appendReadablePath(path, "*"), appendReadableLocation(locationPath, configdoc.Step{Index: i, IsIndex: true})); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(values, nil, nil)
}

func appendReadableLocation(path []configdoc.Step, step configdoc.Step) []configdoc.Step {
	out := make([]configdoc.Step, len(path), len(path)+1)
	copy(out, path)
	return append(out, step)
}

func appendReadablePath(path []string, key string) []string {
	out := make([]string, len(path), len(path)+1)
	copy(out, path)
	return append(out, key)
}

// readableSchemaRules is ReadableSchemaKeys with each path split, built once: the schema is
// static, and a config read looks up every key it holds, so rebuilding it per key made reading a
// large config take seconds.
var readableSchemaRules = sync.OnceValue(func() []readableSchemaRuleEntry {
	keys := ReadableSchemaKeys()
	out := make([]readableSchemaRuleEntry, len(keys))
	for i, key := range keys {
		out[i] = readableSchemaRuleEntry{key: key, parts: strings.Split(key.Path, ".")}
	}
	return out
})

type readableSchemaRuleEntry struct {
	key   SchemaKey
	parts []string
}

func readableSchemaRule(path []string) (SchemaKey, bool) {
	var selected SchemaKey
	selectedWildcards := int(^uint(0) >> 1)
	for _, entry := range readableSchemaRules() {
		rule, parts := entry.key, entry.parts
		if len(parts) != len(path) {
			continue
		}
		wildcards := 0
		matches := true
		for i, part := range parts {
			if part == "*" {
				wildcards++
				continue
			}
			if part != path[i] {
				matches = false
				break
			}
		}
		if matches && wildcards < selectedWildcards {
			selected = rule
			selectedWildcards = wildcards
		}
	}
	return selected, selectedWildcards != int(^uint(0)>>1)
}

// readableLegacyShapeAccepted names the spellings the legacy JSONC reader accepts for a key that
// the readable schema types differently, so converting a legacy file never changes a value's
// type to satisfy the reader (RC-D31). An MCP server's command may be the program as one
// string ("npx"), as the legacy reader takes it, as well as an argv list; a pids limit may be a
// boolean, as the legacy one counts it.
func readableLegacyShapeAccepted(path []string, value any) bool {
	switch {
	case len(path) == 3 && path[0] == "mcp_servers" && path[2] == "command":
		_, ok := asStr(value)
		return ok
	case len(path) == 3 && path[0] == "machine" && path[1] == "resources" && path[2] == "pids_limit":
		// The legacy resources.pids_limit counts a boolean as an integer (true is 1), and
		// machine.resources.cpus is already typed to take one.
		_, ok := value.(bool)
		return ok
	}
	return false
}

func readableTypeMatches(typ SchemaType, value any, removal RemovalPolicy) bool {
	if value == "off" && removal == RemovalOffString {
		return true
	}
	switch typ {
	case SchemaTypeAny, SchemaTypeMixed:
		return true
	case SchemaTypeObject:
		_, ok := asMap(value)
		return ok
	case SchemaTypeString:
		_, ok := asStr(value)
		return ok
	case SchemaTypeBoolean:
		_, ok := value.(bool)
		return ok
	case SchemaTypeNumber:
		if jsonx.IsInt(value) {
			return true
		}
		switch value.(type) {
		case float64, float32:
			return true
		default:
			return false
		}
	case SchemaTypeList:
		_, ok := asList(value)
		return ok
	case SchemaTypeObjectOrList:
		_, object := asMap(value)
		_, list := asList(value)
		return object || list
	default:
		return false
	}
}

func schemaTypeName(typ SchemaType) string {
	switch typ {
	case SchemaTypeObject:
		return "a table"
	case SchemaTypeString:
		return "a string"
	case SchemaTypeBoolean:
		return "a boolean"
	case SchemaTypeNumber:
		return "a number"
	case SchemaTypeList:
		return "a list"
	case SchemaTypeObjectOrList:
		return "a table or list"
	default:
		return "a supported value"
	}
}

// singularRootKeys are the singular spellings of two plural top-level keys, which a reader is
// likely to try, mapped to the key that exists. singularRootKeyForms shows the table form.
var singularRootKeys = map[string]string{"mcp": "mcp_servers", "mount": "mounts"}

var singularRootKeyForms = map[string]string{
	"mcp_servers": "write [mcp_servers.<name>]",
	"mounts":      "write [[mounts]] for each mount",
}

func readableUnknownMessage(path []string) string {
	if len(path) == 1 && path[0] == ConvertLaterKey {
		return fmt.Sprintf("%q is refused in a TOML file: it has nothing to say there. It only stops the launch "+
			"offering to convert an old-format file, and this file is already converted. Remove this line", ConvertLaterKey)
	}
	if len(path) == 1 {
		if plural, ok := singularRootKeys[path[0]]; ok {
			return fmt.Sprintf("unknown TOML config key %q: the key is the plural %q (%s)",
				path[0], plural, singularRootKeyForms[plural])
		}
	}
	if descriptor, ok := readableRenameForPath(path); ok {
		if descriptor.TOMLRefusal != "" {
			return fmt.Sprintf("%q is not accepted in TOML. %s", readablePathDisplay(path, nil), descriptor.TOMLRefusal)
		}
		if len(descriptor.Replacements) > 0 {
			return fmt.Sprintf("%q is not accepted in TOML; use %s instead. The old spelling is not an alias", readablePathDisplay(path, nil), strings.Join(descriptor.Replacements, ", "))
		}
		return fmt.Sprintf("%q is retired and is not accepted in TOML; remove it. The old spelling is not an alias", readablePathDisplay(path, nil))
	}
	return fmt.Sprintf("unknown TOML config key %q", readablePathDisplay(path, nil))
}

func readableRenameForPath(path []string) (RenameDescriptor, bool) {
	var selected RenameDescriptor
	selectedWildcards := int(^uint(0) >> 1)
	for _, descriptor := range RenameDescriptors() {
		parts := strings.Split(descriptor.OldPath, ".")
		if len(parts) != len(path) {
			continue
		}
		wildcards, matches := 0, true
		for i, part := range parts {
			if part == "*" {
				wildcards++
			} else if part != path[i] {
				matches = false
				break
			}
		}
		if matches && wildcards < selectedWildcards {
			selected = descriptor
			selectedWildcards = wildcards
		}
	}
	return selected, selectedWildcards != int(^uint(0)>>1)
}

func readableTOMLError(label string, index *configdoc.Index, path []string, locationPath []configdoc.Step, message string) error {
	location := termsafe.Visible(label)
	if span, ok, err := index.Value(locationPath...); err == nil && ok {
		line, column := index.Position(span.Start)
		location = fmt.Sprintf("%s:%d:%d", location, line, column)
	}
	return configErr("%s: config.%s: %s", location, readablePathDisplay(path, locationPath), message)
}

func readablePathDisplay(path []string, locationPath []configdoc.Step) string {
	var out strings.Builder
	for i, part := range path {
		if part == "*" {
			if i < len(locationPath) && locationPath[i].IsIndex {
				out.WriteString(fmt.Sprintf("[%d]", locationPath[i].Index))
			} else {
				out.WriteString("[*]")
			}
			continue
		}
		if strings.ContainsAny(part, ".[]") {
			out.WriteString(fmt.Sprintf("[%q]", part))
			continue
		}
		if i > 0 {
			out.WriteByte('.')
		}
		out.WriteString(part)
	}
	return out.String()
}
