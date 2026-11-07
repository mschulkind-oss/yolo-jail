package packdecl

// legacyspellings.go is the pack.toml provider and profile spellings RC-D69 retires
// (docs/design/readable-config.md RC-D69): a provider is declared `[providers.<id>]`, the shape a
// user's config has, so `[[provider]] name = "<id>"` and `[[profile]]` are legacy spellings, read
// for a few releases. `yolo check` notes each one naming `yolo pack migrate <path>`, which writes
// the keyed form, and a manifest declaring one provider both ways is refused naming the same
// command, since which of the two declarations is meant cannot be read from it.

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// PackMigrateStep is the next step every legacy-spelling refusal names, for the pack's own
// directory.
const PackMigrateStep = "`yolo pack migrate <path>` on this pack's directory,"

// legacyProviderSpellings lists the legacy spellings root writes (providers, then profiles, each
// in document order) and refuses an id declared both as `[providers.<id>]` and as
// `[[provider]] name = "<id>"`.
func legacyProviderSpellings(root map[string]any) (legacy, problems []string) {
	keyed := map[string]bool{}
	if table, ok := root["providers"].(map[string]any); ok {
		for id := range table {
			keyed[id] = true
		}
	}
	if items, ok := root["provider"].([]any); ok {
		var both []string
		for _, item := range items {
			fields, _ := item.(map[string]any)
			name, _ := fields["name"].(string)
			legacy = append(legacy, fmt.Sprintf("[[provider]] name = %q", name))
			if name != "" && keyed[name] {
				both = append(both, name)
			}
		}
		sort.Strings(both)
		for _, id := range both {
			problems = append(problems, fmt.Sprintf("provider %q is declared both as [providers.%s] and as "+
				"[[provider]] name = %q, so which one is meant cannot be read; run %s which writes it "+
				"once, as [providers.%s]", id, id, id, PackMigrateStep, id))
		}
	}
	if items, ok := root["profile"].([]any); ok {
		for _, item := range items {
			fields, _ := item.(map[string]any)
			name, _ := fields["name"].(string)
			legacy = append(legacy, fmt.Sprintf("[[profile]] name = %q", name))
		}
	}
	return legacy, problems
}

// LegacyProviderSpellings is the legacy provider and profile spellings a pack.toml's bytes write
// (RC-D69), for `yolo check`'s note; nil when there are none or the bytes do not parse, which the
// pack's load reports instead.
func LegacyProviderSpellings(data []byte) []string {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return nil
	}
	root, ok := jsonx.Plain(doc.Values).(map[string]any)
	if !ok {
		return nil
	}
	legacy, _ := legacyProviderSpellings(root)
	return legacy
}
