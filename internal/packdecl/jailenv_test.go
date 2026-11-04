package packdecl

import (
	"strings"
	"testing"
)

func TestJailOnlyEnvDecodesInBothFormats(t *testing.T) {
	for name, decode := range map[string]func() (*Manifest, []string){
		"toml": func() (*Manifest, []string) {
			return DecodeTOML([]byte("name = \"test\"\n[[env]]\njail_only = true\nvars.TEST = \"1\"\n"))
		},
		"json": func() (*Manifest, []string) {
			return Decode([]byte(`{"name":"test","contributes":[{"kind":"env","jail_only":true,"vars":{"TEST":"1"}}]}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, problems := decode()
			if len(problems) != 0 {
				t.Fatal(problems)
			}
			cs := m.Contributions()
			if len(cs) != 1 || !cs[0].JailOnly || cs[0].Vars["TEST"] != "1" {
				t.Fatalf("jail_only env did not survive decoding: %+v", cs)
			}
		})
	}
}

func TestJailOnlyIsRefusedOnOtherContributionKinds(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"test","contributes":[{"kind":"state","at":".test","jail_only":true}]}`))
	if !strings.Contains(strings.Join(problems, "\n"), `does not take "jail_only"`) {
		t.Fatalf("jail_only on state must be refused with a remedy: %v", problems)
	}
}
