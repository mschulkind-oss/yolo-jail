package entrypoint

// hostownedkeysandvalues_test.go is §11's success criterion AS RULED: the `assert` -> `own`
// switch is invariant in KEYS AND VALUES, not in bytes
// (docs/design/config-ownership-and-promotion.md §11, OQ-CO12).
//
// ⚠ IT IS NOT hostownedadoption_test.go's TEST WITH A WEAKER COMPARATOR, and reading it that
// way is how the relaxation becomes untestable. That file's fixture is already canonical —
// key-sorted JSON, no comments — so it is byte-identical across the switch and says nothing
// about which comparator is in force. This file's fixtures are deliberately NOT canonical:
// unsorted JSON keys, and a TOML file carrying the user's own comments. So each case asserts
// TWO things that only hold together under the ruled criterion:
//
//	the bytes DIFFER       — the fixture really exercises an axis the ruling made conformant
//	the VALUES are EQUAL   — and nothing of the user's was lost while they did
//
// Drop the first assertion and a canonicalized fixture would pass while measuring nothing;
// drop the second and there is no criterion left. The bytes-differ half is the one a reader
// is tempted to delete as redundant, so it carries its own message saying why it is not.
//
// WHAT "KEYS AND VALUES" MEANS HERE, and it is the codec's own answer rather than this file's:
// decode both sides with the SURFACE's codec and compare the decoded values structurally.
// Objects are equal when they hold the same key set and equal values under each; arrays when
// they hold equal elements in the same order; scalars by value; and a key present with a
// `null` value is a KEY, not an absence. Everything the codec does not decode is outside the
// criterion by construction — byte layout, indentation, insertion order, and, because a
// comment is neither a key nor a value, TOML comments and the generated header. Numbers cannot
// differ by Go type across a comparison, because both sides are decoded by the one codec.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point one at a real home.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// criterionPack owns ONE surface of the named codec at the named path, with the same layer
// shape adoptionBaselinePack uses — a `defaults` scalar and a `managed` OBJECT with a single
// leaf — so the fixture files can hold a sibling leaf under a declared parent.
func criterionPack(t *testing.T, codecName, path string) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": codecName,
		"path":     path,
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"permissions": map[string]any{"defaultMode": "default"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// criterionHome seeds a home with the agent's own file and applies ONCE under `assert`, so
// what comes back is a home already applying under `assert` — the state the criterion is
// about — plus the bytes that home holds.
func criterionHome(t *testing.T, codecName, rel, seed string) (home, path string, asserted []byte) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderHostPack(criterionPack(t, codecName, "~/"+rel), home,
		render.OwnershipAssert, false, nil); err != nil {
		t.Fatalf("first --assert apply: %v", err)
	}
	asserted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the assert baseline: %v", err)
	}
	return home, path, asserted
}

// decodeWith decodes bytes through the named surface codec — the one definition of "keys and
// values" this file has. A decode failure is fatal rather than a mismatch: a file yolo just
// wrote that its own codec cannot read is a worse defect than the one being measured.
func decodeWith(t *testing.T, codecName string, data []byte) any {
	t.Helper()
	c, ok := codec.LookupCodec(codecName)
	if !ok {
		t.Fatalf("no codec %q", codecName)
	}
	v, err := c.Decode(data)
	if err != nil {
		t.Fatalf("decode %s through its own codec: %v\n%s", codecName, err, data)
	}
	return v
}

// THE CRITERION. Each case is one surface codec and one deliberately non-canonical file.
func TestSwitchingToOwnPreservesKeysAndValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		codec string
		rel   string
		// axis names the ruled-conformant difference the fixture exists to exercise, and
		// is quoted in the failure when the bytes DON'T differ — so a reader who
		// canonicalized the fixture is told which property they deleted.
		axis string
		seed string
	}{
		{
			name:  "json key order",
			codec: "json",
			rel:   ".acme/settings.json",
			axis:  "JSON key order at every depth (own sorts; assert keeps the file's own)",
			seed: `{
  "zebra": "written last, sorted first",
  "apiKeyHelper": "/usr/local/bin/acme-key.sh",
  "nested": {
    "z": 1,
    "a": 2
  },
  "alpha": "written last-but-one",
  "permissions": {
    "ask": [
      "Bash(rm:*)"
    ]
  }
}
`,
		},
		{
			name:  "toml comments and the generated header",
			codec: "toml",
			rel:   ".acme/settings.toml",
			axis:  "TOML comments and the generated header (own destroys and prepends them)",
			seed: `# the user's own note about this file
apiKeyHelper = "/usr/local/bin/acme-key.sh"

# and one about the section
[permissions]
ask = ["Bash(rm:*)"]
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, path, asserted := criterionHome(t, tc.codec, tc.rel, tc.seed)

			results, err := RenderHostPack(criterionPack(t, tc.codec, "~/"+tc.rel), home,
				render.OwnershipOwn, false, nil)
			if err != nil {
				t.Fatalf("first `own` apply: %v", err)
			}
			if len(results) != 1 || results[0].Action != "rendered" {
				t.Fatalf("the owned render did not render: %+v", results)
			}
			owned, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read after the owned render: %v", err)
			}

			// THE CRITERION ITSELF.
			before, after := decodeWith(t, tc.codec, asserted), decodeWith(t, tc.codec, owned)
			if !reflect.DeepEqual(before, after) {
				t.Errorf("switching to `own` changed the surface's KEYS OR VALUES.\n"+
					"assert:\n%s\nown:\n%s\n\ndocs/design/config-ownership-and-promotion.md "+
					"§11 requires keys-and-values invariance across this switch (OQ-CO12). A "+
					"reformat is conformant; a key or a value that moved is not. If the diff "+
					"is a LEAF under a declared object, adoption has gone back to dropping "+
					"whole top-level subtrees (dropComputedTables' second bullet says why "+
					"that is wrong). If it is a whole top-level key the file held and no "+
					"layer declares, the first-migration adoption branch is not running — "+
					"check that the host capture store resolves (render.Target.SidecarDir) "+
					"rather than leaving last_render present.", asserted, owned)
			}

			// AND THE FIXTURE STILL EXERCISES THE AXIS. Without this the case could be
			// satisfied by a file that is byte-identical across the switch, which measures
			// the OLD criterion and says nothing about the ruled one.
			if string(asserted) == string(owned) {
				t.Errorf("this fixture is byte-identical across the switch, so it no longer "+
					"measures the relaxation — the axis it exists for is %s. Restore a "+
					"non-canonical fixture, or move the case to hostownedadoption_test.go, "+
					"which is where the byte-invariant case belongs.", tc.axis)
			}
		})
	}
}
