package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

// TestMiseSurfaceInstallsNoBakedRuntime enforces the "one runtime, one copy"
// invariant across the prism port: a runtime baked into the OCI image (node,
// python, AND go as of 2026-07-20) must NOT be a DEFAULT in the mise surface, or
// the default setup (no workspace mise_tools) would install a duplicate, non-nix
// copy — the source of the LD_LIBRARY_PATH/MCP-wrapper whack-a-mole and the
// host↔baked version skew. Under the prism this means the mise surface's
// Defaults layer must be EMPTY (a workspace may still pin its own node/python/go
// via YOLO_MISE_TOOLS — the computed layer — but yolo itself defaults none).
// Note the flake's pkgs.go (nativeBuildInputs) is only the host cross-compiler;
// the jail go is the distinct imagePkgs.go entry.
func TestMiseSurfaceInstallsNoBakedRuntime(t *testing.T) {
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
	// duplicates. The markers read the QUOTED NAME, because since 2026-09-12 the
	// core list is a list of nixpkgs attr NAMES (`coreFloorNames`) that
	// `corePackagesFromNixpkgs` maps over — the same names the non-container FLOOR
	// resolves against the native package set
	// (docs/design/macos-user-provisioning.md). The quotes are what keep `"go"`
	// exact: it cannot false-match gh/gawk/git/golangci-lint, nor the host
	// cross-compiler `pkgs.go`.
	bakedRuntime := map[string]string{
		`"nodejs_`:  "node",   // "nodejs_<major>"
		`"python3"`: "python", // "python3"
		`"go"`:      "go",     // "go" (baked 2026-07-20)
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

	// The mise surface must default NO baked runtime — its Defaults layer is
	// empty, so the default setup relies entirely on the baked /bin/<tool>.
	s, found := agentcfg.BuiltinManifest().Lookup("mise", "config")
	if !found {
		t.Fatal("builtin manifest missing mise/config")
	}
	for tool := range baked {
		if _, present := s.DefaultsMap()[tool]; present {
			t.Errorf("mise surface defaults %q, but it is baked into the image — "+
				"drop it so the default setup doesn't get a duplicate non-nix %s "+
				"(see docs/research/tool-provisioning.md §2)", tool, tool)
		}
	}
}
