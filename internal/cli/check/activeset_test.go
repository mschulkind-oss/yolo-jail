package check

// activeset_test.go pins `yolo check`'s prediction of the ACTIVE SET refusals
// (docs/design/active-provider-sets.md; the active set, a term that doc coins, is the ordered
// list of profiles one agent runs on): a later entry the agent cannot speak, and two entries on
// one provider, FAIL the check as the launch refuses them. Through sectionPacks, the call site,
// as protocols_test.go drives the primary's pairing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// setPackDir is a local pack whose set-capable agent speaks openai, beside two providers it can
// reach, one it cannot, and a profile over each.
func setPackDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "setpack",
  "contributes": [
    {"kind": "program", "bin": "setagent", "via": "npm", "package": "@example/setagent",
     "protocols": ["openai"], "provider_sets": true},
    {"kind": "provider", "name": "near", "endpoints": {"openai": {"base_url": "https://near.example.test/v1"}}},
    {"kind": "provider", "name": "also", "endpoints": {"openai": {"base_url": "https://also.example.test/v1"}}},
    {"kind": "provider", "name": "faraway", "endpoints": {"anthropic": {"base_url": "https://far.example.test"}}},
    {"kind": "profile", "name": "near", "provider": "near"},
    {"kind": "profile", "name": "near-too", "provider": "near"},
    {"kind": "profile", "name": "also", "provider": "also"},
    {"kind": "profile", "name": "faraway", "provider": "faraway"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setSelection(agent string, set ...string) *jsonx.OrderedMap {
	list := make([]any, len(set))
	for i, s := range set {
		list[i] = s
	}
	inner := jsonx.NewOrderedMap()
	inner.Set(agent, list)
	merged := jsonx.NewOrderedMap()
	merged.Set("profile", inner)
	return merged
}

func TestCheckPredictsTheSetRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  []string
		says string
	}{
		{"a later entry the agent cannot speak", []string{"near", "faraway"},
			`profile "faraway", entry 2 of setagent's profiles (near, faraway)`},
		{"two entries on one provider", []string{"near", "near-too"}, `both resolve to provider "near"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packsFixture(t, `{"packs": ["file://`+setPackDir(t)+`"]}`)
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, setSelection("setagent", tc.set...))
			if r.failed == 0 || !strings.Contains(buf.String(), tc.says) ||
				!strings.Contains(buf.String(), "REFUSED") {
				t.Errorf("the check must FAIL saying %q:\n%s", tc.says, buf.String())
			}
		})
	}
	// A set every entry of which pairs is clean.
	packsFixture(t, `{"packs": ["file://`+setPackDir(t)+`"]}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, setSelection("setagent", "near", "also"))
	if r.failed != 0 || strings.Contains(buf.String(), "REFUSED") {
		t.Errorf("a set whose every entry pairs must not fail the check:\n%s", buf.String())
	}
}
