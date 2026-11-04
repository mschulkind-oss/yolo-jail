package packdecl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostKeychainProgram = `
[[program]]
bin = "fixture-tool"
install = { default = false, brew = "fixture-tool" }
url = "https://example.test/install"
via = "installer"
`

// TestTheCopilotPackDeclaresItsHostLoginItem: the service name OQ-KC5 reads is the copilot
// pack's declared value (keychain-from-a-jail.md §2.3), not a Go constant, and it decodes from
// the shipped manifest through the TOML reader.
func TestTheCopilotPackDeclaresItsHostLoginItem(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packs", "copilot", ManifestTOMLName))
	if err != nil {
		t.Fatal(err)
	}
	m, problems := DecodeTOML(raw)
	if len(problems) > 0 {
		t.Fatalf("copilot pack: %v", problems)
	}
	got := m.HostKeychainEnv("copilot")
	if len(got) != 1 || got[0].Var != "COPILOT_GITHUB_TOKEN" || len(got[0].Services) == 0 ||
		got[0].Services[0] != "copilot-cli" || got[0].Login != "copilot login" {
		t.Errorf("copilot's host_keychain_env = %+v", got)
	}
}

func TestHostKeychainEnvDecodesAndValidates(t *testing.T) {
	ok := `host_keychain_env = { fixture-tool = [{ var = "FIXTURE_TOKEN", services = ["fixture-new", "fixture-old"] }] }
` + hostKeychainProgram
	m, problems := DecodeTOML([]byte(ok))
	if len(problems) > 0 {
		t.Fatalf("valid declaration refused: %v", problems)
	}
	got := m.HostKeychainEnv("fixture-tool")
	if len(got) != 1 || strings.Join(got[0].Services, ",") != "fixture-new,fixture-old" {
		t.Fatalf("HostKeychainEnv = %+v", got)
	}
	got[0].Services[0] = "mutated"
	if m.HostKeychainEnv("fixture-tool")[0].Services[0] != "fixture-new" {
		t.Error("HostKeychainEnv hands out the manifest's own slice")
	}
	if m.HostKeychainEnv("other") != nil {
		t.Error("a bin with no declaration answered")
	}

	for name, tc := range map[string]struct{ decl, want string }{
		"bad var":       {`{ fixture-tool = [{ var = "BAD-NAME", services = ["s"] }] }`, ".var:"},
		"no services":   {`{ fixture-tool = [{ var = "T" }] }`, ".services: needs at least one"},
		"flag service":  {`{ fixture-tool = [{ var = "T", services = ["-a"] }] }`, "services[0]"},
		"blank service": {`{ fixture-tool = [{ var = "T", services = [" "] }] }`, "services[0]"},
		"twice":         {`{ fixture-tool = [{ var = "T", services = ["s"] }, { var = "T", services = ["s"] }] }`, "declared twice"},
		"unowned bin":   {`{ other-tool = [{ var = "T", services = ["s"] }] }`, "non-fork program declared by this pack"},
		"control login": {`{ fixture-tool = [{ var = "T", services = ["s"], login = "a\u0007" }] }`, ".login:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := DecodeTOML([]byte("host_keychain_env = " + tc.decl + "\n" + hostKeychainProgram))
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", problems, tc.want)
			}
		})
	}
}
