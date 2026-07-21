package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestMiseBaseToolsExcludeBakedRuntimes enforces the "one runtime, one copy"
// invariant: a runtime baked into the OCI image (node, python) must NOT also be
// in miseBaseTools, or the default setup (no workspace mise_tools) would install
// a duplicate, non-nix copy — the source of the LD_LIBRARY_PATH/MCP-wrapper
// whack-a-mole and the host↔baked version skew. go is exempt (not baked; the
// flake's pkgs.go is only the host cross-compiler). A workspace may still pin its
// own node/python — this only governs the yolo defaults.
func TestMiseBaseToolsExcludeBakedRuntimes(t *testing.T) {
	// Read the baked runtimes from flake.nix (repo root is two dirs up from this
	// package: internal/entrypoint → internal → repo).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	flakePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "flake.nix")
	flake, err := os.ReadFile(flakePath)
	if err != nil {
		t.Skipf("cannot read flake.nix (%v) — skipping cross-check", err)
	}
	flakeStr := string(flake)

	// Map a baked-package marker in flake.nix → the mise tool name it duplicates.
	bakedRuntime := map[string]string{
		`nodejs_`: "node",   // imagePkgs.nodejs_<major>
		`python3`: "python", // imagePkgs.python3
	}
	baked := map[string]bool{}
	for marker, tool := range bakedRuntime {
		if regexp.MustCompile(regexp.QuoteMeta(marker)).MatchString(flakeStr) {
			baked[tool] = true
		}
	}
	if !baked["node"] || !baked["python"] {
		t.Fatalf("expected node+python baked in flake.nix; markers not found (node=%v python=%v)", baked["node"], baked["python"])
	}

	for _, bt := range miseBaseTools {
		if baked[bt.tool] {
			t.Errorf("miseBaseTools installs %q, but it is baked into the image — "+
				"remove it so the default setup doesn't get a duplicate non-nix %s "+
				"(see docs/research/tool-provisioning.md §2)", bt.tool, bt.tool)
		}
	}
}
