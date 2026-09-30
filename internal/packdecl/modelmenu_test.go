package packdecl

// modelmenu_test.go pins the `model_menu` field (Contribution.ModelMenu;
// docs/design/model-lists-and-pickers.md MM-D9, MM-D22): a program's declaration, projected into
// its Install for the generated launcher to bake, and refused wherever the launcher could never
// write a menu the program reads. Through Decode and the real validator.

import (
	"reflect"
	"testing"
)

const validModelMenu = `{"catalog":["debug","models","--bundled"],"list":".x/list.json",
  "into":".x/menu.json","flag":["-c","catalog={into}"],"entries":"models","id":"slug",
  "order":"priority","name":"display_name","clear":["upgrade"],"set":{"visibility":"list"}}`

func TestModelMenuIsProjectedFromAProgramOfAnyVia(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"program","bin":"codex","via":"installer","url":"https://example.test/i.sh",
	   "model_menu":` + validModelMenu + `},
	  {"kind":"program","bin":"tool","via":"npm","package":"tool","model_menu":` + validModelMenu + `},
	  {"kind":"program","bin":"other","via":"npm","package":"q"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	installs := m.InstallContributions()
	if len(installs) != 3 {
		t.Fatalf("installs = %+v, want three", installs)
	}
	want := &ModelMenu{Catalog: []string{"debug", "models", "--bundled"}, List: ".x/list.json",
		Into: ".x/menu.json", Flag: []string{"-c", "catalog={into}"}, Entries: "models", ID: "slug",
		Order: "priority", Name: "display_name", Clear: []string{"upgrade"},
		Set: map[string]string{"visibility": "list"}}
	for _, i := range []int{0, 1} {
		if !reflect.DeepEqual(installs[i].ModelMenu, want) {
			t.Errorf("%s's ModelMenu = %+v, want %+v", installs[i].Bin, installs[i].ModelMenu, want)
		}
	}
	if installs[2].ModelMenu != nil {
		t.Errorf("a program that declares none projects %+v, want nil", installs[2].ModelMenu)
	}
	// A copy, not an alias: a consumer editing its Install cannot reach the manifest.
	installs[0].ModelMenu.Catalog[0] = "edited"
	installs[0].ModelMenu.Set["visibility"] = "edited"
	again := m.InstallContributions()
	if again[0].ModelMenu.Catalog[0] != "debug" || again[0].ModelMenu.Set["visibility"] != "list" {
		t.Errorf("editing a projected ModelMenu reached the manifest: %+v", again[0].ModelMenu)
	}
}

func TestModelMenuIsRefusedWhereTheLauncherCouldWriteNoMenu(t *testing.T) {
	base := func() *ModelMenu {
		return &ModelMenu{Catalog: []string{"debug"}, List: ".x/list.json", Into: ".x/menu.json",
			Flag: []string{"-c", "k={into}"}, Entries: "models", ID: "slug"}
	}
	program := func(m *ModelMenu) Contribution {
		return Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p", ModelMenu: m}
	}
	with := func(edit func(*ModelMenu)) Contribution {
		m := base()
		edit(m)
		return program(m)
	}
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"a non-program kind", Contribution{Kind: KindSkills, From: "skills", Into: ".x/skills",
			ModelMenu: base()}, `does not take "model_menu"`},
		{"no catalog argv", with(func(m *ModelMenu) { m.Catalog = nil }), `no "catalog" argv`},
		{"an empty catalog word", with(func(m *ModelMenu) { m.Catalog = []string{""} }), "empty word"},
		{"an absolute list", with(func(m *ModelMenu) { m.List = "/etc/list.json" }), "not home-relative"},
		{"a tilde into", with(func(m *ModelMenu) { m.Into = "~/.x/menu.json" }), "not home-relative"},
		{"an escaping into", with(func(m *ModelMenu) { m.Into = "../menu.json" }), "leaves the home"},
		{"an unclean list", with(func(m *ModelMenu) { m.List = ".x//list.json" }), "not clean"},
		{"a missing into", with(func(m *ModelMenu) { m.Into = "" }), `"into" is empty`},
		{"one path for both", with(func(m *ModelMenu) { m.Into = m.List }), "one path"},
		{"a flag that never names the file", with(func(m *ModelMenu) { m.Flag = []string{"-c", "k=v"} }),
			`"{into}"`},
		{"no flag", with(func(m *ModelMenu) { m.Flag = nil }), `"{into}"`},
		{"no entries key", with(func(m *ModelMenu) { m.Entries = "" }), `no "entries" key`},
		{"no id key", with(func(m *ModelMenu) { m.ID = "" }), `no "id" key`},
		{"a key with two roles", with(func(m *ModelMenu) { m.Clear = []string{"slug"} }), "two roles"},
		{"a set key that is the order", with(func(m *ModelMenu) {
			m.Order = "priority"
			m.Set = map[string]string{"priority": "1"}
		}), "two roles"},
		{"an empty clear key", with(func(m *ModelMenu) { m.Clear = []string{""} }), `"clear" has an empty key`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if probs := validateContribution("c[0]", tc.c); !containsSubstr(probs, tc.want) {
				t.Errorf("problems = %v, want one saying %q", probs, tc.want)
			}
		})
	}
	if probs := validateContribution("c[0]", program(base())); len(probs) != 0 {
		t.Errorf("a whole declaration on a program is accepted; got %v", probs)
	}
}
