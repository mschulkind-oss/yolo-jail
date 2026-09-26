package packdecl

// envnames_test.go pins the provider's `api_key_env_name` as a string OR a list of
// variable names (docs/design/provider-credential-scope.md, OQ-CN1), on the manifest
// decoder a launch reads packs through.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAProviderMayListItsCredentialVariables(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"provider","name":"one","api_key_env_name":"ONE_KEY"},
	  {"kind":"provider","name":"routes","api_key_env_name":["ROUTE_BEARER","ROUTE_ID","ROUTE_SECRET"]}]}`))
	if len(problems) != 0 {
		t.Fatalf("a string and a list are both legal: %v", problems)
	}
	provs := m.Providers()
	if got := provs[0].APIKeyEnvName; len(got) != 1 || got.KeyPointer() != "ONE_KEY" {
		t.Errorf("a string decodes to the one variable it names, got %v", got)
	}
	if got := provs[1].APIKeyEnvName; strings.Join(got, ",") != "ROUTE_BEARER,ROUTE_ID,ROUTE_SECRET" {
		t.Errorf("a list decodes whole and in order, got %v", got)
	}
	if got := provs[1].APIKeyEnvName.KeyPointer(); got != "" {
		t.Errorf("several routes point an agent at none of them, got %q", got)
	}
}

func TestACredentialVariableListIsValidated(t *testing.T) {
	for name, body := range map[string]string{
		"an empty list":     `[]`,
		"a non-string item": `["OK", 3]`,
		"a bad name":        `["NOT-A-NAME"]`,
		"a duplicate":       `["DUP","DUP"]`,
		"a number":          `7`,
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p",` +
				`"api_key_env_name":` + body + `}]}`))
			if len(problems) == 0 {
				t.Errorf("api_key_env_name: %s must be refused", body)
			}
		})
	}
}

// One name round-trips as the string every existing reader knows; several as a list.
func TestEnvNamesEncodeTheirOwnShape(t *testing.T) {
	for _, tc := range []struct {
		in   EnvNames
		want string
	}{{EnvNames{"ONE"}, `"ONE"`}, {EnvNames{"A", "B"}, `["A","B"]`}} {
		b, err := json.Marshal(tc.in)
		if err != nil || string(b) != tc.want {
			t.Errorf("Marshal(%v) = %s (%v), want %s", tc.in, b, err, tc.want)
		}
	}
	if names, ok := EnvNamesFromValue([]any{"A", "B"}); !ok || len(names) != 2 {
		t.Errorf("a decoded config list lowers to its names, got %v %v", names, ok)
	}
	if _, ok := EnvNamesFromValue([]any{}); ok {
		t.Error("an empty config list must be refused")
	}
}

// A manifest's `"api_key_env_name": null` or `""` meant "no credential pointer" while the
// field was a plain string, and packs were loaded under that reading — so both still read as
// ABSENT rather than as a list holding one empty name, which manifest validation refuses and
// LoadJailPacks makes fatal at boot: a third-party pack that loaded before the field became a
// list must not stop a jail from starting.
func TestAnEmptyOrNullCredentialVariableReadsAsAbsent(t *testing.T) {
	for _, body := range []string{`null`, `""`} {
		t.Run(body, func(t *testing.T) {
			m, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p",` +
				`"api_key_env_name":` + body + `}]}`))
			if len(problems) != 0 {
				t.Fatalf("api_key_env_name: %s was accepted as no pointer and must stay so: %v", body, problems)
			}
			if got := m.Providers()[0].APIKeyEnvName; len(got) != 0 {
				t.Errorf("api_key_env_name: %s must decode to no names, got %#v", body, got)
			}
		})
	}
}
