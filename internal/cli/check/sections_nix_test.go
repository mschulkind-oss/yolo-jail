package check

import (
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// TestNixDryRunArgvCarriesAcceptFlakeConfig pins the cache probe to the same
// nix-level flags the real build runs with. If they diverge, the probe answers a
// different question than the one check reports on: without --accept-flake-config
// nix ignores the flake's own substituter, so the dry run says "will build from
// source" for a path the real build substitutes — which on macOS turns a fine
// build into a bogus "you need a Linux builder" warning.
//
// It drives nixDryRunWillBuild — the caller — rather than nixDryRunArgv, and
// reads the argv out of the Exec seam. Asserting on the argv builder alone would
// hold nothing: the defect b7f2ade fixed was that this probe spelled its own
// `[]string{"nix", "--extra-experimental-features", …}` inline, and a single edit
// puts it back there while every assertion against the builder stays green
// (measured 2026-08-17: reverting it left `go test -short ./...` fully green).
// The recorded argv is the artifact an inline builder cannot fake.
func TestNixDryRunArgvCarriesAcceptFlakeConfig(t *testing.T) {
	var got []string
	o := &Options{}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		got = argv
		return ExecResult{Ran: true, RC: 0}
	}
	o.nixDryRunWillBuild(t.TempDir(), nil)

	sub := slices.Index(got, "build")
	if len(got) == 0 || got[0] != "nix" || sub < 0 {
		t.Fatalf("unexpected argv shape: %v", got)
	}
	// nix-level flags only: anything after the subcommand is a build flag and
	// nix would reject --accept-flake-config there.
	nixLevel := got[1:sub]
	for _, want := range image.NixFlakeFlags() {
		if !slices.Contains(nixLevel, want) {
			t.Errorf("nix-level flags %v missing %q (full argv: %v)", nixLevel, want, got)
		}
	}
	if !slices.Contains(got, "--dry-run") {
		t.Errorf("dry-run probe lost --dry-run: %v", got)
	}
	if !slices.Contains(got, ".#ociImage") {
		t.Errorf("dry-run probe no longer evaluates .#ociImage: %v", got)
	}
}
