package packdecl

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustDecodeLegacy(t *testing.T, data string) *Manifest {
	t.Helper()
	m, problems := Decode([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("fixture rejected: %v", problems)
	}
	return m
}

func TestEncodeTOMLWritesGroupedDefaultsUnwritten(t *testing.T) {
	m := mustDecodeLegacy(t, `{"name":"fixture","contributes":[
		{"kind":"program","bin":"rg","install_hints":{"brew":"ripgrep","apt":"ripgrep","dnf":"ripgrep","pacman":"ripgrep","nix":"ripgrep"}},
		{"kind":"program","bin":"fd","install_hints":{"apt":"fd-find","dnf":"fd-find"}},
		{"kind":"program","bin":"jq","install_hints":{"brew":"jq","apt":"jq","dnf":"jq","pacman":"jq","nix":"jq"}},
		{"kind":"program","bin":"tool","install_hints":{"brew":"tool-pkg","apt":"tool-pkg","dnf":"tool-pkg","pacman":"tool-pkg","nix":"tool","brew-cask":"tool-app"}},
		{"kind":"program","bin":"built","via":"pack","from":"src","run":"built","build":"make"},
		{"kind":"state","at":".fixture","scope":"workspace"},
		{"kind":"config","config":{"agent":"fixture","name":"settings","path":"~/.fixture/settings.json","codec":"json"}},
		{"kind":"program","bin":"plans","via":"npm","package":"plans","built_in_providers":{"names":["zai"],"plans":{"none":null}}}
	]}`)
	encoded, err := EncodeTOML(m)
	if err != nil {
		t.Fatalf("EncodeTOML: %v", err)
	}
	text := string(encoded)
	for _, want := range []string{
		"[[program]]\nbin = \"rg\"\ninstall = \"ripgrep\"\n",
		"[[program]]\nbin = \"fd\"\ninstall = { default = false, apt = \"fd-find\", dnf = \"fd-find\" }\n",
		"build = \"make\"\nfrom = \"src\"\ninstall = false\n",
		"[[program]]\nbin = \"jq\"\n\n",
		"install = { default = \"tool-pkg\", nix = \"tool\", brew-cask = \"tool-app\" }",
		"plans = { none = \"off\" }",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("encoded manifest lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"run = ", "scope = ", "codec = ", "install_hints", "kind = "} {
		if strings.Contains(text, unwanted) {
			t.Errorf("encoded manifest writes the default or legacy %q:\n%s", unwanted, text)
		}
	}
	decoded, problems := DecodeTOML(encoded)
	if len(problems) != 0 {
		t.Fatalf("strict DecodeTOML: %v\n%s", problems, text)
	}
	if got := decoded.Contributes[3].InstallHints; !reflect.DeepEqual(got, map[string]string{"brew": "tool-pkg", "apt": "tool-pkg", "dnf": "tool-pkg", "pacman": "tool-pkg", "nix": "tool", "brew-cask": "tool-app"}) {
		t.Errorf("folded install hints decode to %#v", got)
	}

	kept, err := EncodeTOMLWith(m, EncodeOptions{KeepInstallHints: true})
	if err != nil {
		t.Fatal(err)
	}
	// A typed manifest's hints are a Go map, so they come out in key order; a long table is
	// written one dotted key per manager.
	if !strings.Contains(string(kept), "bin = \"rg\"\ninstall.apt = \"ripgrep\"\ninstall.brew = \"ripgrep\"\ninstall.dnf = \"ripgrep\"\ninstall.nix = \"ripgrep\"\ninstall.pacman = \"ripgrep\"\n") {
		t.Errorf("KeepInstallHints did not keep the per-manager form:\n%s", kept)
	}
}

func TestEncodeTOMLRefusesWhatTOMLCannotSay(t *testing.T) {
	for name, fixture := range map[string]struct{ manifest, path string }{
		"null overlay key": {`{"contributes":[{"kind":"config-overlay","surface":"x/settings","config":{"managed":{"theme":null}}}]}`,
			"contributes[0].config.theme"},
		"unknown manager": {`{"contributes":[{"kind":"program","bin":"x","via":"npm","package":"x","install_hints":{"custom":"x-tool"}}]}`,
			"contributes[0].install_hints.custom"},
	} {
		t.Run(name, func(t *testing.T) {
			m := mustDecodeLegacy(t, fixture.manifest)
			_, err := EncodeTOML(m)
			var unrepresentable *UnrepresentableError
			if !errors.As(err, &unrepresentable) || unrepresentable.Path != fixture.path {
				t.Fatalf("EncodeTOML = %v, want an UnrepresentableError at %s", err, fixture.path)
			}
			if !strings.Contains(err.Error(), "keep this pack on its legacy manifest") {
				t.Errorf("refusal names no next step: %v", err)
			}
		})
	}
}

func TestEncodeTOMLKeepsGroupedProviderRowsAndReplace(t *testing.T) {
	grouped, problems := DecodeTOML([]byte(`name = "meta"

[[overlay]]
surface = "pi/settings"
set.packages = ["one"]

[providers.meta]
model = "spark"
every_model = { api = "openai-responses", accepts = ["text", "image"] }
models."spark" = { name = "Spark", context = 1048576 }
models."wonder" = { name = "Wonder", context = 1000000 }

[[env]]
vars = { META = "1" }
replace = true
`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	encoded, err := EncodeTOML(grouped)
	if err != nil {
		t.Fatalf("EncodeTOML: %v", err)
	}
	again, problems := DecodeTOML(encoded)
	if len(problems) != 0 {
		t.Fatalf("re-decode: %v\n%s", problems, encoded)
	}
	if !strings.Contains(string(encoded), "[providers.meta]\n") || strings.Count(string(encoded), "replace = true") != 1 {
		t.Errorf("grouped provider table or authored replace lost:\n%s", encoded)
	}
	a, b := grouped.Contributes, again.Contributes
	if len(a) != len(b) || !reflect.DeepEqual(a[1].ProviderModels, b[1].ProviderModels) || !reflect.DeepEqual(a[1].ProviderModelOrder, b[1].ProviderModelOrder) ||
		a[1].ProviderModel != b[1].ProviderModel || !reflect.DeepEqual(a[1].EveryProviderModel, b[1].EveryProviderModel) || a[0].Replace || !b[2].Replace {
		t.Errorf("grouped round trip lost provider rows or replace bits:\n%s", encoded)
	}
}

func TestEncodeTOMLRoundTripsMCPAuthKind(t *testing.T) {
	manifest := mustDecodeLegacy(t, `{"contributes":[{"kind":"mcp-auth","store":".fixture/auth.json","format":"json","entries_at":[],"companions":[],"empty_store":"delete","lock":{"protocol":"flock","path":".fixture/auth.lock"},"fields":{"expires_at":{"path":"expires_at","unit":"ms"}}}]}`)
	encoded, err := EncodeTOML(manifest)
	if err != nil {
		t.Fatalf("EncodeTOML: %v", err)
	}
	if !strings.Contains(string(encoded), "[[mcp_auth]]") || strings.Contains(string(encoded), "mcp_auth =") {
		t.Fatalf("mcp-auth was not encoded as its grouped table:\n%s", encoded)
	}
	decoded, problems := DecodeTOML(encoded)
	if len(problems) != 0 {
		t.Fatalf("re-decode: %v\n%s", problems, encoded)
	}
	want, got := *manifest.MCPAuth, *decoded.MCPAuth
	// Empty companion/entry arrays and absent arrays have the same declared meaning.
	want.EntriesAt, got.EntriesAt = nil, nil
	want.Companions, got.Companions = nil, nil
	if !reflect.DeepEqual(want, got) {
		t.Errorf("mcp-auth declaration changed in round trip: legacy=%#v grouped=%#v", want, got)
	}
}
