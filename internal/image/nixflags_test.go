package image

import (
	"slices"
	"testing"
)

// TestNixFlakeFlagsAcceptsFlakeConfig pins the flag itself. Dropping
// --accept-flake-config is silent at runtime — nix warns on stderr and builds
// from source anyway — so the only place it can be caught is here.
func TestNixFlakeFlagsAcceptsFlakeConfig(t *testing.T) {
	flags := NixFlakeFlags()
	if !slices.Contains(flags, "--accept-flake-config") {
		t.Fatalf("NixFlakeFlags() missing --accept-flake-config: %v", flags)
	}
	i := slices.Index(flags, "--extra-experimental-features")
	if i < 0 || i+1 >= len(flags) || flags[i+1] != "nix-command flakes" {
		t.Fatalf("NixFlakeFlags() missing experimental-features pair: %v", flags)
	}
}

// TestFlakeInvocationsCarryAcceptFlakeConfig is the table the roadmap item
// asks for: every argv builder that evaluates .#ociImage must carry both flags,
// and must carry them BEFORE the `build` subcommand (they are nix-level flags;
// nix rejects --accept-flake-config after the subcommand).
//
// check's dry-run argv is covered by the sibling test in internal/cli/check,
// which is where that builder lives.
func TestFlakeInvocationsCarryAcceptFlakeConfig(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"run path (buildImageStorePathArgs)", ociBuildArgv("/tmp/out-link", nil)},
		{"run path with builder offload", ociBuildArgv("/tmp/out-link", []string{"--builders", "ssh://b"})},
		{"check preflight (BuildOCIImage)", ociBuildArgv("/tmp/yolo-check-1", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFlakeArgv(t, tc.argv)
		})
	}
}

// assertFlakeArgv is the shared assertion; check's own test mirrors it for the
// dry-run builder.
func assertFlakeArgv(t *testing.T, argv []string) {
	t.Helper()
	if len(argv) == 0 || argv[0] != "nix" {
		t.Fatalf("argv does not invoke nix: %v", argv)
	}
	sub := slices.Index(argv, "build")
	if sub < 0 {
		t.Fatalf("argv has no `build` subcommand: %v", argv)
	}
	nixLevel := argv[1:sub]
	for _, want := range NixFlakeFlags() {
		if !slices.Contains(nixLevel, want) {
			t.Errorf("nix-level flags %v missing %q (full argv: %v)", nixLevel, want, argv)
		}
	}
}

// TestOCIBuildArgvShape keeps the extracted builder honest about the argv the
// two call sites used to spell inline, so the refactor that unified them is not
// free to change what nix is actually asked to do.
func TestOCIBuildArgvShape(t *testing.T) {
	got := ociBuildArgv("/tmp/link", []string{"--builders", "ssh://b"})
	want := []string{
		"nix",
		"--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"build", ".#ociImage", "--impure",
		"--out-link", "/tmp/link",
		"--print-build-logs",
		"--builders", "ssh://b",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ociBuildArgv:\n got %v\nwant %v", got, want)
	}
}
