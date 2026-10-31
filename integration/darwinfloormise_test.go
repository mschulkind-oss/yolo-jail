package integration

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestTheDarwinFloorsMiseIsRustlsWithoutACheckPhase pins the two settings of the mise the
// macOS floor builds instead of nixpkgs' (flake.nix, `noncontainerFloorSubstitutes`), by
// EVALUATING the floor profile each darwin system realizes, not by reading flake.nix's text.
//
// rustls is what lets mise reach HTTPS under the macos-user Seatbelt profile, which denies
// securityd (docs/design/macos-native-tls.md). No public cache holds that build, so every Mac
// and every macOS CI runner compiles it. The check phase is off because upstream's
// cargoTestFlags carry `--all-features`, so it recompiled mise with a different feature set
// and ran the nextest suite serially: with it on, the build ran past "Build the macOS floor"'s
// 60 minutes in macos-user.yml (scheduled runs 40332984099 through 41204422413) and past
// darwin-floor-cache.yml's 75, so the cache that should spare every later build was never
// filled.
//
// Eval only: nothing is built, and a Linux host evaluates darwin derivations fine.
func TestTheDarwinFloorsMiseIsRustlsWithoutACheckPhase(t *testing.T) {
	requireJail(t)
	requireNix(t)
	if bundleRoot == "" {
		t.Skip("module root unresolved")
	}
	const apply = `ps: map (p: { check = p.doCheck or null; noDefault = p.cargoBuildNoDefaultFeatures or false; features = p.cargoBuildFeatures or null; }) (builtins.filter (p: (p.pname or "") == "mise") ps)`
	for _, system := range []string{"aarch64-darwin", "x86_64-darwin"} {
		t.Run(system, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), nixEvalTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "nix",
				"--extra-experimental-features", "nix-command flakes",
				"eval", "--impure", "--json",
				matchedBundleFlakeRef("packages."+system+".yoloNoncontainerProfile.paths"),
				"--apply", apply)
			cmd.Dir = bundleRoot
			// The floor alone, as darwinpkg.BuildFloorProfileArgv realizes it.
			cmd.Env = append(cmd.Environ(), "YOLO_EXTRA_PACKAGES=")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			hang := armHangReport(cmd)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("nix eval of the %s floor's mise failed: %v\n%s%s", system, err, stderr.String(), hang.String())
			}
			var mises []struct {
				Check     *bool    `json:"check"`
				NoDefault bool     `json:"noDefault"`
				Features  []string `json:"features"`
			}
			if err := json.Unmarshal(out, &mises); err != nil {
				t.Fatalf("unparseable eval output %q: %v", out, err)
			}
			if len(mises) != 1 {
				t.Fatalf("the %s floor holds %d mise derivations, want exactly 1: %s", system, len(mises), out)
			}
			m := mises[0]
			if m.Check == nil || *m.Check {
				t.Errorf("the %s floor's mise runs its check phase: upstream's "+
					"`--all-features` check recompiles it and runs the suite serially, which put the "+
					"build past every macOS CI ceiling; set doCheck = false in "+
					"noncontainerFloorSubstitutes", system)
			}
			if !m.NoDefault || !slices.Contains(m.Features, "rustls") {
				t.Errorf("the %s floor's mise is not the rustls build (no default features: %v, "+
					"features: %v); its default native-tls fails every download under the "+
					"Seatbelt profile", system, m.NoDefault, m.Features)
			}
		})
	}
}
