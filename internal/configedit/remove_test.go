package configedit

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

func TestPlanEditRemoveTOMLKeyRetainsItsInlineComment(t *testing.T) {
	source := []byte("[settings]\r\nvalue = 1 # preserve me\r\nother = 2\r\n")
	p, err := PlanEdit(source, configdoc.TOML, Mutation{Action: Remove, Path: []configdoc.Step{{Key: "settings"}, {Key: "value"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Updated), " # preserve me\r\n") {
		t.Fatalf("comment lost or moved: %q", p.Updated)
	}
	if _, err := configdoc.Parse(p.Updated, configdoc.TOML); err != nil {
		t.Fatalf("result invalid: %v\n%s", err, p.Updated)
	}
}

// A member of a TOML inline table takes its separating comma with it, whichever member it is
// (secret-env-scoping-plan.md Task 5: `yolo config secret` removes one name from `set = { … }`).
func TestPlanEditRemoveTOMLInlineTableMemberTakesItsComma(t *testing.T) {
	for _, tc := range []struct{ name, key, want string }{
		{"first", "A", "[env]\nset = { B = \"2\", C = \"3\" }\n"},
		{"middle", "B", "[env]\nset = { A = \"1\", C = \"3\" }\n"},
		{"last", "C", "[env]\nset = { A = \"1\", B = \"2\" }\n"},
		{"only", "A", "[env]\nset = {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "[env]\nset = { A = \"1\", B = \"2\", C = \"3\" }\n"
			if tc.name == "only" {
				source = "[env]\nset = { A = \"1\" }\n"
			}
			p, err := PlanEdit([]byte(source), configdoc.TOML, Mutation{Action: Remove, Path: []configdoc.Step{{Key: "env"}, {Key: "set"}, {Key: tc.key}}})
			if err != nil {
				t.Fatal(err)
			}
			if string(p.Updated) != tc.want {
				t.Fatalf("want %q, got %q", tc.want, p.Updated)
			}
		})
	}
}

func TestPlanEditCreatesTOMLTableWithParsedNumericValue(t *testing.T) {
	plan, err := PlanEdit(nil, configdoc.TOML, Mutation{Action: Add, Path: []configdoc.Step{{Key: "runtime"}, {Key: "count"}}, Value: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plan.Updated), "[runtime]\ncount = 3\n"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if _, err := configdoc.Parse(plan.Updated, configdoc.TOML); err != nil {
		t.Fatalf("numeric table invalid: %v", err)
	}
}

func TestPlanEditCreatesTableUsingExistingCRLFStyle(t *testing.T) {
	source := []byte("name = 'workspace'\r\n")
	p, err := PlanEdit(source, configdoc.TOML, Mutation{Action: Add, Path: []configdoc.Step{{Key: "providers"}, {Key: "bedrock"}, {Key: "region"}}, Value: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := "name = 'workspace'\r\n\r\n[providers.bedrock]\r\nregion = \"us-east-1\"\r\n"
	if string(p.Updated) != want {
		t.Fatalf("want %q, got %q", want, p.Updated)
	}
	if _, err := configdoc.Parse(p.Updated, configdoc.TOML); err != nil {
		t.Fatalf("CRLF table invalid: %v", err)
	}
}
