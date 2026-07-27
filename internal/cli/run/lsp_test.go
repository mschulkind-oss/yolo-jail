package run

import (
	"testing"
)

func TestResolveLSPInstalls(t *testing.T) {
	// Empty -> two empty strings.
	if npm, go_ := ResolveLSPInstalls(nil); npm != "" || go_ != "" {
		t.Errorf("empty => (%q, %q), want empty", npm, go_)
	}
	// python + typescript -> npm list only. The mcp-language-server bridge is GONE
	// with the gemini agent (A1): gemini was its only consumer, so a non-go LSP set
	// must now pull NOTHING from go.
	npm, go_ := ResolveLSPInstalls([]string{"python", "typescript"})
	if npm != "pyright\ntypescript-language-server\ntypescript" {
		t.Errorf("npm = %q", npm)
	}
	if go_ != "" {
		t.Errorf("go = %q, want empty (the gemini MCP bridge is removed)", go_)
	}
	// go server -> gopls, and only gopls.
	_, go2 := ResolveLSPInstalls([]string{"go"})
	if go2 != "golang.org/x/tools/gopls@latest" {
		t.Errorf("go server = %q", go2)
	}
	// Custom-only (unknown) name contributes nothing to either list.
	npm3, go3 := ResolveLSPInstalls([]string{"customlsp"})
	if npm3 != "" || go3 != "" {
		t.Errorf("custom-only => (%q, %q), want both empty", npm3, go3)
	}
}
