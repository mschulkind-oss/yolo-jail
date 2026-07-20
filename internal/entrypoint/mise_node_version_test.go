package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestMiseBaseNodeMatchesBakedImage guards against the skew that commit 230ca27
// left open: the mise base node default must be on the SAME major as the node
// baked into the OCI image (`imagePkgs.nodejs_<major>` in flake.nix). Otherwise
// a workspace that doesn't pin node gets a mise node on a different major than
// `/bin/node`, the exact split the baked-node bump meant to avoid.
func TestMiseBaseNodeMatchesBakedImage(t *testing.T) {
	// mise base node major.
	var baseNode string
	for _, bt := range miseBaseTools {
		if bt.tool == "node" {
			baseNode = bt.version
		}
	}
	if baseNode == "" {
		t.Fatal("no node entry in miseBaseTools")
	}

	// Baked node major from flake.nix (repo root is three dirs up from this
	// package: internal/entrypoint → internal → repo).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	flake, err := os.ReadFile(filepath.Join(repoRoot, "flake.nix"))
	if err != nil {
		t.Skipf("cannot read flake.nix (%v) — skipping cross-check", err)
	}
	m := regexp.MustCompile(`nodejs_(\d+)`).FindSubmatch(flake)
	if m == nil {
		t.Fatal("no imagePkgs.nodejs_<major> found in flake.nix")
	}
	bakedMajor := string(m[1])

	if baseNode != bakedMajor {
		t.Errorf("mise base node major %q != baked image node major %q — bump miseBaseTools node when the flake node bumps (see 230ca27)", baseNode, bakedMajor)
	}
}
