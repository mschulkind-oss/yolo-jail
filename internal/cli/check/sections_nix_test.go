package check

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// TestNixDryRunArgvCarriesAcceptFlakeConfig pins the cache probe to the same
// nix-level flags the real build runs with. If they diverge, the probe answers
// a different question than the one check reports on: without
// --accept-flake-config nix ignores the flake's own substituter, so the dry run
// says "will build from source" for a path the real build substitutes — which
// on macOS turns a fine build into a bogus "you need a Linux builder" warning.
func TestNixDryRunArgvCarriesAcceptFlakeConfig(t *testing.T) {
	argv := nixDryRunArgv()
	sub := slices.Index(argv, "build")
	if len(argv) == 0 || argv[0] != "nix" || sub < 0 {
		t.Fatalf("unexpected argv shape: %v", argv)
	}
	// nix-level flags only: anything after the subcommand is a build flag and
	// nix would reject --accept-flake-config there.
	nixLevel := argv[1:sub]
	for _, want := range image.NixFlakeFlags() {
		if !slices.Contains(nixLevel, want) {
			t.Errorf("nix-level flags %v missing %q (full argv: %v)", nixLevel, want, argv)
		}
	}
	if !slices.Contains(argv, "--dry-run") {
		t.Errorf("dry-run probe lost --dry-run: %v", argv)
	}
	if !slices.Contains(argv, ".#ociImage") {
		t.Errorf("dry-run probe no longer evaluates .#ociImage: %v", argv)
	}
}
