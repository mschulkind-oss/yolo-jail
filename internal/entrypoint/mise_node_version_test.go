package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestMiseBaseToolsExcludeBakedRuntimes enforces the "one runtime, one copy"
// invariant: a runtime baked into the OCI image (node, python, AND go as of
// 2026-07-20) must NOT also be in miseBaseTools, or the default setup (no
// workspace mise_tools) would install a duplicate, non-nix copy — the source of
// the LD_LIBRARY_PATH/MCP-wrapper whack-a-mole and the host↔baked version skew.
// All three default runtimes are now baked, so miseBaseTools is empty. Note the
// flake's pkgs.go (nativeBuildInputs) is only the host cross-compiler; the jail
// go is the distinct imagePkgs.go entry. A workspace may still pin its own
// node/python/go — this only governs the yolo defaults.
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

	// Map a baked-package marker regex in flake.nix → the mise tool name it
	// duplicates. Each value is compiled as a regex directly (NOT QuoteMeta'd) so
	// the go marker can anchor with \b — `imagePkgs\.go\b` matches the exact
	// corePackages entry and CANNOT false-match imagePkgs.gh/gawk/git/glibc/… or
	// the host cross-compiler `pkgs.go` (no imagePkgs. prefix).
	bakedRuntime := map[string]string{
		`imagePkgs\.nodejs_`: "node",   // imagePkgs.nodejs_<major>
		`imagePkgs\.python3`: "python", // imagePkgs.python3
		`imagePkgs\.go\b`:    "go",     // imagePkgs.go (baked 2026-07-20)
	}
	baked := map[string]bool{}
	for marker, tool := range bakedRuntime {
		if regexp.MustCompile(marker).MatchString(flakeStr) {
			baked[tool] = true
		}
	}
	if !baked["node"] || !baked["python"] || !baked["go"] {
		t.Fatalf("expected node+python+go baked in flake.nix; markers not found (node=%v python=%v go=%v)", baked["node"], baked["python"], baked["go"])
	}

	for _, bt := range miseBaseTools {
		if baked[bt.tool] {
			t.Errorf("miseBaseTools installs %q, but it is baked into the image — "+
				"remove it so the default setup doesn't get a duplicate non-nix %s "+
				"(see docs/research/tool-provisioning.md §2)", bt.tool, bt.tool)
		}
	}
}
