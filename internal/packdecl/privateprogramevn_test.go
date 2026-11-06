package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

func TestPrivateProgramEnvContributionsStayOutOfOrdinaryProjections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items string
	}{
		{"private before ordinary", `
		  {"kind":"env","bin":"fixture-http-client","served_by":"fixture-http","vars":{"SSL_CERT_FILE":"private-ca","FIXTURE_API_TOKEN":"synthetic-value"}},
		  {"kind":"env","served_by":"ordinary-service","vars":{"SSL_CERT_FILE":"ordinary-ca","ORDINARY":"value"}}`},
		{"private after ordinary", `
		  {"kind":"env","served_by":"ordinary-service","vars":{"SSL_CERT_FILE":"ordinary-ca","ORDINARY":"value"}},
		  {"kind":"env","bin":"fixture-http-client","served_by":"fixture-http","vars":{"SSL_CERT_FILE":"private-ca","FIXTURE_API_TOKEN":"synthetic-value"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, problems := Decode([]byte(`{"name":"fixture","contributes":[` + tc.items + `]}`))
			if len(problems) != 0 {
				t.Fatalf("private program env declaration problems: %v", problems)
			}
			if got, want := m.EnvContributions(), map[string]string{"SSL_CERT_FILE": "ordinary-ca", "ORDINARY": "value"}; !reflect.DeepEqual(got, want) {
				t.Errorf("EnvContributions = %v, want ordinary declarations only %v", got, want)
			}
			if got, want := m.EnvServedBy(), map[string]string{"SSL_CERT_FILE": "ordinary-service", "ORDINARY": "ordinary-service"}; !reflect.DeepEqual(got, want) {
				t.Errorf("EnvServedBy = %v, want ordinary pointers only %v", got, want)
			}
			if got := m.GatedEnvContributions(); len(got) != 0 {
				t.Errorf("GatedEnvContributions = %+v, want no ordinary gated values", got)
			}
			private := m.PrivateProgramEnvContributions()
			if len(private) != 1 || private[0].Bin != "fixture-http-client" || private[0].ServedBy != "fixture-http" || private[0].Vars["SSL_CERT_FILE"] != "private-ca" {
				t.Errorf("PrivateProgramEnvContributions = %+v, want the selected client declaration", private)
			}
		})
	}
}

func TestPrivateProgramEnvRequiresOneServedProgramAndNoGates(t *testing.T) {
	for _, tc := range []struct {
		name, entry, want string
	}{
		{"served_by required", `{"kind":"env","bin":"fixture-http-client","vars":{"TOKEN":"synthetic-value"}}`, `served_by`},
		{"profile gate forbidden", `{"kind":"env","bin":"fixture-http-client","profile":"p","served_by":"fixture-http","vars":{"TOKEN":"synthetic-value"}}`, `gate`},
		{"platform gate forbidden", `{"kind":"env","bin":"fixture-http-client","platform":"aws-bedrock","served_by":"fixture-http","vars":{"TOKEN":"synthetic-value"}}`, `gate`},
		{"unknown substitution", `{"kind":"env","bin":"fixture-http-client","served_by":"fixture-http","vars":{"TOKEN":"{unknown}"}}`, `unknown substitution`},
		{"caller token must be whole value", `{"kind":"env","bin":"fixture-http-client","served_by":"fixture-http","vars":{"TOKEN":"Bearer {caller_token}"}}`, `whole value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(`{"name":"fixture","contributes":[` + tc.entry + `]}`))
			if len(problems) == 0 {
				t.Fatal("invalid private program env declaration was accepted")
			}
			if tc.want != "" {
				found := false
				for _, problem := range problems {
					if strings.Contains(problem, tc.want) {
						found = true
					}
				}
				if !found {
					t.Errorf("problems %v do not mention %q", problems, tc.want)
				}
			}
		})
	}
}
