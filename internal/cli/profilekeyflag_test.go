package cli

// profilekeyflag_test.go pins PP-D10's equation (docs/design/providers-and-profiles-redesign.md):
// the config `profile` key mirrors -p/--profile form for form. Each row is one form written
// both ways; the key lowers through config.ProfileSelectionOf and the argv through the run
// path's own parse (parseRunArgs → applyProfileValue → Options.ProfileFlags), and the two must
// set the same two fields and fold to the same table. So a change to either spelling's reading
// that the other does not share fails here.
//
// The list forms are rows too (docs/design/active-provider-sets.md OQ-AP1 to OQ-AP3): a JSON
// array in the key is the comma list on the command line, per agent and bare alike.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestTheProfileKeyAndItsFlagSelectTheSame(t *testing.T) {
	// pi and opencode declare provider_sets, so a bare list reaches each whole and the others as
	// its first entry (OQ-AP3), whichever spelling carried it.
	recv := config.ProfileReceivers{Bins: []string{"claude", "pi", "codex", "opencode"},
		SetCapable: map[string]bool{"pi": true, "opencode": true}}
	for _, tc := range []struct {
		name string
		key  string // the `profile` value
		argv string // the -p spelling of the same selection
	}{
		{"one profile for every agent", `"bedrock"`, "run -p bedrock -- claude"},
		{"per agent, one value", `{"pi": "codex", "claude": "bedrock"}`, "run -p pi=codex,claude=bedrock -- claude"},
		{"per agent, repeated flags", `{"pi": "codex", "claude": "bedrock"}`, "run -p pi=codex -p claude=bedrock -- claude"},
		{"every agent not named", `{"*": "bedrock", "pi": "codex"}`, "run -p bedrock -p pi=codex -- claude"},
		{"every agent not named, the pair typed first", `{"*": "bedrock", "pi": "codex"}`, "run -p pi=codex -p bedrock -- claude"},
		{"the long flag", `"zai"`, "run --profile zai -- claude"},
		{"no command after --", `{"*": "zai", "codex": "bedrock"}`, "-p zai -p codex=bedrock"},
		{"a list for one agent", `{"pi": ["zai", "openrouter"]}`, "run -p pi=zai,openrouter -- pi"},
		{"a list for opencode", `{"opencode": ["zai", "openrouter"]}`, "run -p opencode=zai,openrouter -- opencode"},
		{"a list for one agent beside a name for another", `{"pi": ["zai", "openrouter"], "claude": "codex"}`,
			"run -p pi=zai,openrouter,claude=codex -- pi"},
		{"a list for every agent", `["zai", "openrouter"]`, "run -p zai,openrouter -- pi"},
		{"a list for every agent not named", `{"*": ["zai", "openrouter"], "codex": "bedrock"}`,
			"run -p zai,openrouter -p codex=bedrock -- pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := jsonx.Decode([]byte(tc.key))
			if err != nil {
				t.Fatal(err)
			}
			fromKey, ok := config.ProfileSelectionOf(v)
			if !ok {
				t.Fatalf("the key %s did not lower", tc.key)
			}
			var opts run.Options
			if parsed := parseRunArgs(strings.Fields(tc.argv), &opts); parsed.misuse != nil {
				t.Fatalf("%q did not parse: %v", tc.argv, parsed.misuse)
			}
			fromFlag := opts.ProfileFlags()
			if !reflect.DeepEqual(fromKey.Default, fromFlag.Default) || !reflect.DeepEqual(fromKey.Named, fromFlag.Named) {
				t.Errorf("key %s = {Default: %q, Named: %v}, but %q = {Default: %q, Named: %v}",
					tc.key, fromKey.Default, fromKey.Named, tc.argv, fromFlag.Default, fromFlag.Named)
			}
			keyTable, _ := jsonx.DumpsCompact(config.ProfileTableFor(recv, fromKey))
			flagTable, _ := jsonx.DumpsCompact(config.ProfileTableFor(recv, fromFlag))
			if keyTable != flagTable {
				t.Errorf("key %s folds to %s, but %q folds to %s", tc.key, keyTable, tc.argv, flagTable)
			}
		})
	}
}
