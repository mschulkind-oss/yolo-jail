package configedit

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// A REMOVED KEY TAKES ITS OWN LINE (editing.md, the splice, rule 1). Each removal left a
// whitespace-only line where the key had been (the 2026-11-13 Mac lab report, §4): the
// comment-preserving blanking kept the spaces around `=`.
func TestPlanEditRemoveTOMLKeyTakesItsWholeLine(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		path         []configdoc.Step
		want         string
	}{
		{"top level", "setup = \"podman-self\"\nkvm = true\n", []configdoc.Step{{Key: "setup"}}, "kvm = true\n"},
		{"last top-level", "kvm = true\nsetup = \"podman-self\"\n", []configdoc.Step{{Key: "setup"}}, "kvm = true\n"},
		{"no final newline", "kvm = true\nsetup = \"podman-self\"", []configdoc.Step{{Key: "setup"}}, "kvm = true\n"},
		{"in a table", "[network]\nmode = \"bridge\"\nports = []\n", []configdoc.Step{{Key: "network"}, {Key: "mode"}}, "[network]\nports = []\n"},
		{"indented, CRLF", "[network]\r\n  mode = \"bridge\"\r\n  ports = []\r\n", []configdoc.Step{{Key: "network"}, {Key: "mode"}}, "[network]\r\n  ports = []\r\n"},
		{"multi-line list", "items = [\n  1,\n  2,\n]\nafter = 2\n", []configdoc.Step{{Key: "items"}}, "after = 2\n"},
		// The `machine` table only the header implied goes with it; the planner expected an
		// empty `machine` and refused its own splice.
		{"implied parent", "[machine.gpu]\nenabled = true\n", []configdoc.Step{{Key: "machine"}, {Key: "gpu"}}, ""},
		{"implied parent beside a sibling", "[machine.gpu]\nenabled = true\n[machine.resources]\ncpus = 2\n", []configdoc.Step{{Key: "machine"}, {Key: "gpu"}}, "[machine.resources]\ncpus = 2\n"},
		{"blank lines kept", "a = 1\n\nb = 2\n\nc = 3\n", []configdoc.Step{{Key: "b"}}, "a = 1\n\n\nc = 3\n"},
		// A comment on the line keeps the line: comments are never removed (rule 2).
		{"inline comment", "[network]\nmode = \"bridge\" # why\nports = []\n", []configdoc.Step{{Key: "network"}, {Key: "mode"}}, "[network]\n   # why\nports = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := PlanEdit([]byte(tc.source), configdoc.TOML, Mutation{Action: Remove, Path: tc.path})
			if err != nil {
				t.Fatal(err)
			}
			if string(p.Updated) != tc.want {
				t.Fatalf("want %q, got %q", tc.want, p.Updated)
			}
			assertHunksPreserveUnrequested(t, []byte(tc.source), p.Updated, p.Hunks)
		})
	}
}

// AN UNSET THAT EMPTIES ITS TABLE DROPS THE TABLE (the 2026-11-13 Mac lab report, §4: the
// `[env.secrets.set]` header stayed behind). Only when asked (PruneEmptiedTable, which `yolo
// config unset` sets), only the header table that held the removed key, and never a table
// holding a comment: comments are never removed, and a header-less comment would read as the
// previous table's.
func TestPlanEditRemovePrunesTheTableItEmptied(t *testing.T) {
	secret := []configdoc.Step{{Key: "env"}, {Key: "secrets"}, {Key: "set"}, {Key: "AWS_ACCESS_KEY_ID"}}
	for _, tc := range []struct {
		name, source, want string
		prune              bool
	}{
		{"last table", "packs = []\n\n[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\n", "packs = []\n", true},
		{"only table", "[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\n", "", true},
		{"between tables", "[a]\nx = 1\n\n[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\n\n[b]\ny = 2\n", "[a]\nx = 1\n\n[b]\ny = 2\n", true},
		{"CRLF", "[a]\r\nx = 1\r\n\r\n[env.secrets.set]\r\nAWS_ACCESS_KEY_ID = \"x\"\r\n", "[a]\r\nx = 1\r\n", true},
		{"sibling key stays", "[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\nB = \"y\"\n", "[env.secrets.set]\nB = \"y\"\n", true},
		{"comment keeps the table", "[env.secrets.set]\n# Bedrock keys\nAWS_ACCESS_KEY_ID = \"x\"\n", "[env.secrets.set]\n# Bedrock keys\n", true},
		{"header comment keeps the table", "[env.secrets.set] # Bedrock\nAWS_ACCESS_KEY_ID = \"x\"\n", "[env.secrets.set] # Bedrock\n", true},
		{"trailing comment keeps the table", "[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\n# more later\n\n[b]\ny = 2\n", "[env.secrets.set]\n# more later\n\n[b]\ny = 2\n", true},
		{"not asked", "[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"x\"\n", "[env.secrets.set]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := PlanEdit([]byte(tc.source), configdoc.TOML, Mutation{Action: Remove, Path: secret, PruneEmptiedTable: tc.prune})
			if err != nil {
				t.Fatal(err)
			}
			if string(p.Updated) != tc.want {
				t.Fatalf("want %q, got %q", tc.want, p.Updated)
			}
			assertCommentsPreserved(t, []byte(tc.source), p.Updated, configdoc.TOML)
			assertHunksPreserveUnrequested(t, []byte(tc.source), p.Updated, p.Hunks)
			doc, err := configdoc.Parse(p.Updated, configdoc.TOML)
			if err != nil {
				t.Fatal(err)
			}
			if !semanticEqual(doc.Values, p.Expected) {
				t.Fatalf("expected values %#v, parsed %#v", p.Expected, doc.Values)
			}
		})
	}
}
