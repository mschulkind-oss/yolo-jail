package packdecl

// programneed_test.go pins OQ-PS11 (docs/design/provisioner-sets.md, ruled 2026-10-23): one
// `program` kind, a recipe-less entry being a NEED, and the old `requires` kind refused with
// the next step rather than migrated.

import (
	"strings"
	"testing"
)

func TestTheRequiresKindIsRefusedNamingTheNextStep(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"p","contributes":[{"kind":"requires","bin":"rg"}]}`))
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, `kind "requires" has been REMOVED`) ||
		!strings.Contains(joined, `write it as a "program" with no recipe: a [[program]] table with bin = "<bin>" and no via`) {
		t.Fatalf("the old kind must be refused with the rewrite named; got %v", problems)
	}
	if KnownKind("requires") {
		t.Error(`"requires" must not be a known kind: there is no transition`)
	}
}

func TestARecipeLessProgramIsANeed(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"p","contributes":[` +
		`{"kind":"program","bin":"rg","install_hints":{"brew":"ripgrep"}},` +
		`{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`))
	if len(problems) != 0 {
		t.Fatalf("a need and a recipe must both decode: %v", problems)
	}
	cs := m.Contributions()
	if !cs[0].IsNeed() || cs[0].Provides() || !cs[1].Provides() || cs[1].IsNeed() {
		t.Fatalf("shapes wrong: need=%+v recipe=%+v", cs[0], cs[1])
	}
	if ins := m.InstallContributions(); len(ins) != 1 || ins[0].Bin != "tool" {
		t.Errorf("a need installs nothing; InstallContributions = %+v", ins)
	}
	if req := m.RequiredBins(); len(req) != 1 || req[0].Bin != "rg" || !req[0].Need {
		t.Errorf("RequiredBins = %+v, want the one need", req)
	}
	deps := m.DepRequirements()
	if len(deps) != 2 || !deps[0].Need || deps[1].Need || deps[0].SelfInstall != "" {
		t.Errorf("DepRequirements = %+v, want the need (no self-install) then the recipe", deps)
	}
}

func TestANeedRefusesRecipeAndAgentFieldsByName(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"p","contributes":[` +
		`{"kind":"program","bin":"rg","package":"ripgrep","update":["up"]}]}`))
	joined := strings.Join(problems, "\n")
	for _, want := range []string{`a program with no "via" does not take "package"`,
		`a program with no "via" does not take "update"`, `it is a NEED`, `Add a "via"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, problems)
		}
	}
}

// PS-D20: within one pack, a need beside the pack's own recipe for the bin is refused, and so
// are two recipes whose platforms overlap; recipes whose platforms do not overlap are one
// program with a recipe per machine, which main accepted and this still does.
func TestOnePackDeclaresAProgramOncePerMachine(t *testing.T) {
	decode := func(entries string) string {
		_, problems := Decode([]byte(`{"name":"p","contributes":[` + entries + `]}`))
		return strings.Join(problems, "\n")
	}
	const npm = `{"kind":"program","bin":"tool","via":"npm","package":"tool"`
	const inst = `{"kind":"program","bin":"tool","via":"installer","url":"https://example.com/i.sh"`
	for _, tc := range []struct {
		name, entries, want string
	}{
		{"need then recipe", `{"kind":"program","bin":"tool"},` + npm + `}`, `declared with no recipe beside this pack's own recipe`},
		{"recipe then need", npm + `},{"kind":"program","bin":"tool"}`, `declared with no recipe beside this pack's own recipe`},
		{"two recipes, no platforms", npm + `},` + inst + `}`, `has a second recipe`},
		{"two recipes, one everywhere", npm + `,"platforms":["linux"]},` + inst + `}`, `has a second recipe`},
		{"two recipes, os vs os/arch", npm + `,"platforms":["linux"]},` + inst + `,"platforms":["linux/arm64"]}`, `has a second recipe`},
		{"two recipes, disjoint", inst + `,"platforms":["darwin"]},` + npm + `,"platforms":["linux"]}`, ``},
		{"two recipes, disjoint arches", inst + `,"platforms":["linux/amd64"]},` + npm + `,"platforms":["linux/arm64"]}`, ``},
		{"two needs", `{"kind":"program","bin":"tool"},{"kind":"program","bin":"tool"}`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decode(tc.entries)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("must decode clean: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q, got: %s", tc.want, got)
			}
		})
	}
}
