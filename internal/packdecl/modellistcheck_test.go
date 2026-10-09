package packdecl

import (
	"strings"
	"testing"
)

const modelListCheckProgram = `{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic"}`

func modelListCheckManifest(fact string) string {
	return `{"name":"checkpack","model_list_check":{"synthetic":` + fact + `},"contributes":[` + modelListCheckProgram + `]}`
}

func TestModelListCheckDecodesAsAnOptionalPackOwnedFact(t *testing.T) {
	m, problems := Decode([]byte(modelListCheckManifest(`{"rules":[{"platform":"aws-bedrock","via":false,"makers":["openai"],"native_empty_ok":true}]}`)))
	if len(problems) != 0 {
		t.Fatalf("valid declaration refused: %v", problems)
	}
	check, ok := m.ProgramModelListCheck("synthetic")
	if !ok || check == nil || len(check.Rules) != 1 || check.Rules[0].Via == nil || *check.Rules[0].Via ||
		len(check.Rules[0].Makers) != 1 || check.Rules[0].Makers[0] != "openai" || !check.Rules[0].NativeEmptyOK {
		t.Fatalf("ProgramModelListCheck() = (%+v, %t), want the complete program fact", check, ok)
	}
	if absent, ok := m.ProgramModelListCheck("missing"); ok || absent != nil {
		t.Errorf("missing program fact = (%+v, %t), want absent", absent, ok)
	}
}

func TestModelListCheckOmissionAndEmptyObject(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		optedIn        bool
	}{
		{"omitted", `{"contributes":[` + modelListCheckProgram + `]}`, false},
		{"empty map", `{"model_list_check":{},"contributes":[` + modelListCheckProgram + `]}`, false},
		{"empty object for owned bin", modelListCheckManifest(`{}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, problems := Decode([]byte(tc.manifest))
			if len(problems) != 0 {
				t.Fatal(problems)
			}
			check, ok := m.ProgramModelListCheck("synthetic")
			if ok != tc.optedIn || (check != nil) != tc.optedIn {
				t.Errorf("ProgramModelListCheck() = (%+v, %t), want opt-in %t", check, ok, tc.optedIn)
			}
			if installs := m.InstallContributions(); len(installs) != 1 || installs[0].Bin != "synthetic" {
				t.Fatalf("metadata changed the owning program: %+v", installs)
			}
		})
	}
}

func TestModelListCheckRejectsInvalidRules(t *testing.T) {
	for _, tc := range []struct{ name, fact, want string }{
		{"null fact", `null`, `needs an object`},
		{"empty platform", `{"rules":[{"platform":""}]}`, `needs a non-empty "platform"`},
		{"invalid maker", `{"rules":[{"platform":"p","makers":["OpenAI"]}]}`, `one lowercase token`},
		{"empty maker", `{"rules":[{"platform":"p","makers":[""]}]}`, `one lowercase token`},
		{"empty makers list", `{"rules":[{"platform":"p","makers":[]}]}`, `omit "makers"`},
		{"duplicate maker", `{"rules":[{"platform":"p","makers":["openai","openai"]}]}`, `lists "openai" twice`},
		{"overlapping selectors", `{"rules":[{"platform":"p","via":false},{"platform":"p"}]}`, `overlaps rules[0]`},
		{"duplicate selector", `{"rules":[{"platform":"p","via":true},{"platform":"p","via":true}]}`, `overlaps rules[0]`},
		{"native exception not native", `{"rules":[{"platform":"p","native_empty_ok":true}]}`, `requires "via": false`},
		{"native exception on via", `{"rules":[{"platform":"p","via":true,"native_empty_ok":true}]}`, `requires "via": false`},
		{"unknown nested key", `{"future":true}`, `unknown field`},
		{"unknown rule key", `{"rules":[{"platform":"p","future":true}]}`, `unknown field`},
		{"malformed via boolean", `{"rules":[{"platform":"p","via":"false"}]}`, `cannot unmarshal string`},
		{"malformed exception boolean", `{"rules":[{"platform":"p","native_empty_ok":"true"}]}`, `cannot unmarshal string`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(modelListCheckManifest(tc.fact)))
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

func TestModelListCheckRequiresLocalNonForkOwnership(t *testing.T) {
	for _, tc := range []struct{ name, manifest, want string }{
		{"missing owner", `{"model_list_check":{"synthetic":{}}}`, `must name a non-fork program`},
		{"empty bin", `{"model_list_check":{"":{}},"contributes":[` + modelListCheckProgram + `]}`, `must name a non-fork program`},
		{"provider not program", `{"model_list_check":{"p":{}},"contributes":[{"kind":"provider","name":"p"}]}`, `must name a non-fork program`},
		{"fork cannot restate base", `{"model_list_check":{"synthetic":{}},"contributes":[{"kind":"program","bin":"synthetic","via":"source","fork_of":"base","source":"git+https://example.test/fork?ref=main","build":"make","produces":[".local/bin/synthetic"]}]}`, `must name a non-fork program`},
		{"old unsafe program placement", `{"contributes":[{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic","model_list_check":{}}]}`, `unknown field`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(tc.manifest))
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}
