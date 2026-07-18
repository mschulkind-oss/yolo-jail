package runmount

import (
	"encoding/json"
	"testing"
)

func TestResolveLSPInstalls(t *testing.T) {
	// Empty -> two empty strings, no bridge.
	if npm, go_ := ResolveLSPInstalls(nil); npm != "" || go_ != "" {
		t.Errorf("empty => (%q, %q), want empty", npm, go_)
	}
	// python + typescript -> npm list; go gets only the bridge.
	npm, go_ := ResolveLSPInstalls([]string{"python", "typescript"})
	if npm != "pyright\ntypescript-language-server\ntypescript" {
		t.Errorf("npm = %q", npm)
	}
	if go_ != "github.com/isaacphi/mcp-language-server@latest" {
		t.Errorf("go = %q", go_)
	}
	// go server -> gopls + bridge.
	_, go2 := ResolveLSPInstalls([]string{"go"})
	if go2 != "golang.org/x/tools/gopls@latest\ngithub.com/isaacphi/mcp-language-server@latest" {
		t.Errorf("go server = %q", go2)
	}
	// Custom-only (unknown) name: still non-empty -> bridge pulled, npm empty.
	npm3, go3 := ResolveLSPInstalls([]string{"customlsp"})
	if npm3 != "" || go3 != "github.com/isaacphi/mcp-language-server@latest" {
		t.Errorf("custom-only => (%q, %q)", npm3, go3)
	}
}

// TestResolveLSPParity cross-checks against live _resolve_lsp_installs.
func TestResolveLSPParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	// Each case is an ordered list of server names; Python takes a dict, whose
	// iteration order is insertion order, so we build it from an ordered list.
	cases := [][]string{
		{},
		{"python"},
		{"python", "typescript", "go"},
		{"go"},
		{"customlsp"},
		{"typescript", "python"},
	}
	inJSON, _ := json.Marshal(cases)
	script := `
import sys, json; sys.path.insert(0, 'src')
from cli.run_cmd import _resolve_lsp_installs
out = []
for names in json.loads(sys.argv[1]):
    d = {n: {"command": "x"} for n in names}
    out.append(_resolve_lsp_installs(d))
print(json.dumps(out))
`
	outBytes, err := py("-c", script, string(inJSON)).Output()
	if err != nil {
		t.Skipf("python run_cmd import failed: %v", err)
	}
	var want []map[string]string
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, names := range cases {
		npm, go_ := ResolveLSPInstalls(names)
		if npm != want[i]["npm"] || go_ != want[i]["go"] {
			t.Errorf("case %v:\n go=(%q,%q)\n py=(%q,%q)", names, npm, go_, want[i]["npm"], want[i]["go"])
		}
	}
}
