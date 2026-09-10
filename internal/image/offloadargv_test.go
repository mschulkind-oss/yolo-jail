package image

import (
	"os"
	"strings"
	"testing"
)

// TestTheOffloadDoesNotForbidDarwinNativeBuilds is the regression for a macOS
// nightly that could not build its own image copier.
//
// The container-builder offload used to pass `--max-jobs 0` alongside
// `--builders`, forcing every derivation onto the Linux builder. That was
// harmless while the jail image was Linux derivations end to end. Layer-aware
// delivery introduced two that must be built NATIVELY on darwin — the
// nix2container tooling and the copier, which read the Mac's own /nix/store — and
// `--max-jobs 0` denies them:
//
//	Failed to find a machine for remote build!
//	required (system, features): (x86_64-darwin, [])
//	error: Cannot build … Reason: local builds are disabled (max-jobs = 0)
//
// Source-level, and deliberately so: the alternative is a Mac with a running
// Linux builder container, which is the one instrument this project does not have
// in CI on the Linux side. What it pins is the DECISION — that the offload routes
// by system rather than banning local work — which is the thing a future edit
// would undo while every Linux test stayed green.
func TestTheOffloadDoesNotForbidDarwinNativeBuilds(t *testing.T) {
	b, err := os.ReadFile("autoload.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	i := strings.Index(src, "func buildImageWithContainerBuilder(")
	if i < 0 {
		t.Fatal("buildImageWithContainerBuilder is gone — this pin has lost its subject " +
			"and is now vacuous; repoint it at whatever constructs the offload argv")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}

	// The flag, in any spelling nix accepts.
	for _, banned := range []string{`"--max-jobs", "0"`, `--max-jobs=0`, `"--max-jobs","0"`} {
		if strings.Contains(body, banned) {
			t.Errorf("the offload passes %s again — a darwin-native derivation (nix2container, "+
				"the copier) then cannot be built at all, and the macOS nightly fails with "+
				"\"local builds are disabled\". `--builders` alone already routes Linux "+
				"derivations to the container, because a darwin nix cannot build them locally.",
				banned)
		}
	}

	// Not vacuous: the offload must still HAVE a builders line, or this test would
	// pass just as well against a function that stopped offloading entirely.
	if !strings.Contains(body, `"--builders"`) {
		t.Error("the offload no longer passes --builders, so nothing routes Linux derivations " +
			"to the container and the assertion above is satisfied for the wrong reason")
	}
}
