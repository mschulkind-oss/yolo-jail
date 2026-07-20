package run

import (
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
