package packdecl

// modelcatalog_test.go pins the `model_catalog` field (Contribution.ModelCatalog;
// docs/design/model-lists-and-pickers.md MM-D19): an npm program's declaration, projected into its
// Install for `yolo check` to read, and refused wherever it could never name a file inside an
// installed package. Through Decode and the real validator.

import (
	"reflect"
	"testing"
)

func TestModelCatalogIsProjectedFromAnNpmProgram(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"@scope/pi@1",
	   "model_catalog":["node_modules/@scope/ai/data/*.json","models.json"]},
	  {"kind":"program","bin":"other","via":"npm","package":"q"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	installs := m.InstallContributions()
	if len(installs) != 2 {
		t.Fatalf("installs = %+v, want two", installs)
	}
	want := []string{"node_modules/@scope/ai/data/*.json", "models.json"}
	if !reflect.DeepEqual(installs[0].ModelCatalog, want) {
		t.Errorf("pi's ModelCatalog = %v, want %v", installs[0].ModelCatalog, want)
	}
	if installs[1].ModelCatalog != nil {
		t.Errorf("a program that declares none projects %v, want nil", installs[1].ModelCatalog)
	}
}

func TestModelCatalogIsRefusedWhereItNamesNoPackageFile(t *testing.T) {
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"a non-program kind", Contribution{Kind: KindSkills, From: "skills", Into: ".x/skills",
			ModelCatalog: []string{"a.json"}}, `does not take "model_catalog"`},
		{"an installer program", Contribution{Kind: KindProgram, Bin: "x", Via: "installer",
			URL: "https://example.test/install.sh", ModelCatalog: []string{"a.json"}}, `needs "via": "npm"`},
		{"an empty list", Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p",
			ModelCatalog: []string{}}, "an empty list"},
	}
	for _, entry := range []struct{ glob, want string }{
		{"", "is empty"},
		{".", "package's directory itself"},
		{"/abs/models.json", "is absolute"},
		{"a/../b.json", "not clean"},
		{"../outside.json", "leaves the installed package's directory"},
		{"a//b.json", "not clean"},
		{`a\b.json`, "backslash"},
		{"data/[.json", "not a valid pattern"},
	} {
		cases = append(cases, struct {
			name string
			c    Contribution
			want string
		}{"entry " + entry.glob, Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p",
			ModelCatalog: []string{entry.glob}}, entry.want})
	}
	cases = append(cases, struct {
		name string
		c    Contribution
		want string
	}{"a duplicate", Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p",
		ModelCatalog: []string{"a.json", "a.json"}}, "twice"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if probs := validateContribution("c[0]", tc.c); !containsSubstr(probs, tc.want) {
				t.Errorf("problems = %v, want one saying %q", probs, tc.want)
			}
		})
	}
	if probs := validateContribution("c[0]", Contribution{Kind: KindProgram, Bin: "x", Via: "npm",
		Package: "p", ModelCatalog: []string{"node_modules/@s/a/data/*.json"}}); len(probs) != 0 {
		t.Errorf("a clean relative glob on an npm program is accepted; got %v", probs)
	}
}
