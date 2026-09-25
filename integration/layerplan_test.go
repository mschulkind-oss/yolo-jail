package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// THE TWO GUARDS THE LAYER PLAN WAS OWED
// (docs/reference/image-staging-vs-baking.md#the-layer-plan).
//
// The plan's two load-bearing properties were stated and asserted by nothing that looked at a
// real image: the copied/skipped arithmetic has unit tests (internal/image), but a flake edit
// that broke either property passed every test. These build real `.#ociImage` manifests and
// read them.
//
//   - TestLayerPlanKeepsTheCuratedShellOverAPackagesCollision — PRECEDENCE. The top tier is one
//     `symlinkJoin` whose list order is the precedence (curated links over core over full over
//     `packages:`), so a workspace naming a package that ships `bin/bash` must not replace the
//     shell the boot itself runs through. Flipping that list order passes every other test.
//   - TestLayerPlanFlakeOnlyEditRedeliversOnlyTheTopLayer — the BYTE BUDGET. A `flake.nix`-only
//     edit (it moves `imageIdentity`, so the identity file in the top tier) must re-deliver the
//     top layer and nothing else, and that layer must stay small. A package added to the flake
//     in the wrong tier lands its closure in the top layer, which every later `flake.nix` edit
//     then re-copies in full.
//
// Both build from a TWO-FILE FLAKE — `flake.nix` and `flake.lock` copied from this tree into a
// temp dir and built as a `path:` flake — which is the shape the flake is written to build the
// image from ("two files and a binary", flake.nix's prebuilt short-circuit comment): the image
// derivation reads neither `goSrc` nor `bin/`. It evaluates to the same image store path as the
// checkout (checked 2026-09-25), and it is what lets the budget test make a `flake.nix`-only
// edit without touching the checkout.
//
// NOTHING IS LOADED INTO A RUNTIME. Each test reads the nix2container image manifest and the
// store paths it names: the manifest IS the layer plan's output (per-layer digests, sizes and
// the store paths each layer carries), so a runtime load would add minutes and no evidence.
//
// Linux only, like archivedelta_test.go: `.#ociImage` is a Linux image, and a darwin host
// builds it only through a Linux builder. The properties are the flake's, not the host's, so the
// Linux CI lanes are where they are asserted.

// flakeOnlyRedeliveryBudget is the most a `flake.nix`-only edit may re-deliver, in bytes.
//
// MEASURED 2026-09-25 on x86_64-linux at this tree: the edit moves exactly one layer, the top,
// at 27,408,896 bytes (26.1 MiB) — the name-only `/bin` links, the `/lib` farm, `/etc`, the
// identity file, `bin-path-links` and the mountpoint directories. The budget is 40 MiB, about
// 1.5x, and it was set by mutation rather than by round number: linking one package whose
// closure is not in the base tier (nix2container's skopeo, through a `bin-path-links` symlink)
// put about 33 MB of that closure in the top layer, which grew to 61,253,120 bytes — a size a
// 64 MiB budget would have let through. A deliberate growth of the top tier re-measures this constant; it is a
// tripwire for an accident, not a target.
const flakeOnlyRedeliveryBudget = 40 << 20

// layerPlanFlake writes a two-file flake — this tree's flake.nix with `edit` appended, and its
// flake.lock — into a fresh temp dir and returns the dir.
func layerPlanFlake(t *testing.T, edit string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"flake.nix", "flake.lock"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "flake.nix" {
			data = append(data, edit...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// layerPlanManifest builds `.#ociImage` from the flake in dir with YOLO_EXTRA_PACKAGES set to
// packagesJSON (a `packages:` list exactly as the launcher exports it; "" for none) and returns
// the image manifest's store path. The out-link lives in the test's temp dir, so the manifest
// and every layer it names are GC-rooted for the test's lifetime and no longer.
func layerPlanManifest(t *testing.T, dir, packagesJSON string) string {
	t.Helper()
	outLink := filepath.Join(t.TempDir(), "image")
	build := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"build", "path:"+dir+"#ociImage", "--impure", "--out-link", outLink)
	build.Dir = dir
	build.Env = append(envWithout("YOLO_EXTRA_PACKAGES"), "YOLO_EXTRA_PACKAGES="+packagesJSON)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("nix build .#ociImage (YOLO_EXTRA_PACKAGES=%q): %v\n%s", packagesJSON, err, out)
	}
	manifest, err := filepath.EvalSymlinks(outLink)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// layerPlanNixpkgsOut evaluates one package of the flake's OWN locked nixpkgs, for the image's
// system, to its store path — the value flake.nix interpolates as `${imagePkgs.<attr>}`
// (imagePkgs is that nixpkgs' legacyPackages for the image system, with no overlay).
func layerPlanNixpkgsOut(t *testing.T, dir, attr string) string {
	t.Helper()
	sys := strings.Replace(goruntime.GOARCH, "amd64", "x86_64", 1)
	sys = strings.Replace(sys, "arm64", "aarch64", 1) + "-linux"
	eval := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"eval", "--raw", "--inputs-from", "path:"+dir,
		"nixpkgs#legacyPackages."+sys+"."+attr+".outPath")
	eval.Dir = dir
	out, err := eval.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("nix eval nixpkgs#%s for %s: %v\n%s", attr, sys, err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// layerPlanImage is the part of a nix2container image manifest these tests read.
type layerPlanImage struct {
	Layers []struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
		Paths  []struct {
			Path    string `json:"path"`
			Options struct {
				Rewrite *struct {
					Regex string `json:"regex"`
					Repl  string `json:"repl"`
				} `json:"rewrite"`
			} `json:"options"`
		} `json:"paths"`
	} `json:"layers"`
}

// layerPlanRootProvider answers "which file is at /<rel> in this image" from the manifest: the
// store-path file that lands at /<rel>, or "" when none does.
//
// The model is the image's own: a store path carried with a rewrite of its prefix to "" is a
// `copyToRoot` entry and lands at the image root; every other path (`deps`, closure) lands at
// its /nix/store location and so never at /bin. Across layers the HIGHEST one wins (a union
// filesystem); within a layer the LAST entry wins (tar). So the answer is the last root entry,
// bottom to top, that carries <rel> — which is exactly what a precedence flip would move,
// whether it reorders the join or splits the top tier into more than one root entry.
func layerPlanRootProvider(t *testing.T, manifest, rel string) string {
	t.Helper()
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var img layerPlanImage
	if err := json.Unmarshal(data, &img); err != nil {
		t.Fatalf("decoding %s: %v", manifest, err)
	}
	provider := ""
	for _, l := range img.Layers {
		for _, p := range l.Paths {
			rw := p.Options.Rewrite
			if rw == nil || rw.Repl != "" || rw.Regex != "^"+p.Path {
				continue
			}
			candidate := filepath.Join(p.Path, filepath.FromSlash(rel))
			if _, err := os.Lstat(candidate); err == nil {
				provider = candidate
			}
		}
	}
	return provider
}

func requireLayerPlanHost(t *testing.T) {
	t.Helper()
	requireJail(t)
	if goruntime.GOOS != "linux" {
		t.Skip("the layer-plan guards build .#ociImage, a Linux image; they run on the Linux lanes")
	}
}

// PRECEDENCE: a `packages:` entry that ships `bin/bash` and `bin/sh` must not shadow the curated
// ones, which are the boot's own shell (the image's Cmd is /bin/bash).
//
// The colliding package is `bashNonInteractive`: in this nixpkgs `bash` IS the interactive build
// (the same store path the curated link names), so a `packages: ["bash"]` fixture collides with
// nothing and would pass however the precedence went. The test proves the collision is real
// before it reads the answer, so a nixpkgs bump that unifies the two turns it red rather than
// vacuous.
//
// Checked by mutation 2026-09-25: moving `extraPackages` to the FRONT of rootTree's `paths`
// makes /bin/bash resolve into bashNonInteractive, and this test fails on it.
func TestLayerPlanKeepsTheCuratedShellOverAPackagesCollision(t *testing.T) {
	requireLayerPlanHost(t)
	dir := layerPlanFlake(t, "")
	curated := layerPlanNixpkgsOut(t, dir, "bashInteractive") // flake.nix: mkBinPathLinks
	colliding := layerPlanNixpkgsOut(t, dir, "bashNonInteractive")
	if curated == colliding {
		t.Fatalf("bashInteractive and bashNonInteractive are one store path (%s) in this nixpkgs, "+
			"so this fixture no longer collides with the curated shell — pick another package "+
			"that ships bin/bash", curated)
	}
	manifest := layerPlanManifest(t, dir, `["bashNonInteractive"]`)

	for _, rel := range []string{"bin/bash", "bin/sh"} {
		want, err := filepath.EvalSymlinks(filepath.Join(curated, rel))
		if err != nil {
			t.Fatalf("the curated %s is not in the store: %v", rel, err)
		}
		shadow, err := filepath.EvalSymlinks(filepath.Join(colliding, rel))
		if err != nil {
			t.Fatalf("the colliding package ships no %s, so it collides with nothing: %v", rel, err)
		}
		if shadow == want {
			t.Fatalf("the colliding package's %s resolves to the curated one (%s): no collision", rel, want)
		}

		provider := layerPlanRootProvider(t, manifest, rel)
		if provider == "" {
			t.Fatalf("no root entry of the image carries /%s — the image has no shell to boot "+
				"(manifest %s)", rel, manifest)
		}
		got, err := filepath.EvalSymlinks(provider)
		if err != nil {
			t.Fatalf("/%s in the image (%s) does not resolve: %v", rel, provider, err)
		}
		if got != want {
			t.Errorf("/%s in an image whose `packages:` ships a colliding %s resolves to %s, want the "+
				"curated %s: the workspace's package replaced the shell the boot runs through. The "+
				"curated set must come FIRST in rootTree's `paths` (lndir keeps the first link for a "+
				"contested name) and the top tier must stay one root entry "+
				"(docs/reference/image-staging-vs-baking.md#the-layer-plan).", rel, rel, got, want)
		}
	}
}

// THE BYTE BUDGET: a `flake.nix`-only edit re-delivers exactly one layer, the top one, and it is
// under flakeOnlyRedeliveryBudget. "Re-delivers" is the production arithmetic
// (image.ReportFor over the digests the first image already put in the store), so the number
// here is the number a launch's copy report would print.
func TestLayerPlanFlakeOnlyEditRedeliversOnlyTheTopLayer(t *testing.T) {
	requireLayerPlanHost(t)
	before := layerPlanManifest(t, layerPlanFlake(t, ""), "")
	after := layerPlanManifest(t,
		layerPlanFlake(t, "\n# layer-plan probe: a flake.nix-only edit, which moves imageIdentity\n"), "")
	if before == after {
		t.Fatalf("a flake.nix edit did not move the image (%s): imageIdentity no longer hashes "+
			"flake.nix, so this test measured nothing", before)
	}

	a, err := image.ReadLayerInventory(before)
	if err != nil {
		t.Fatal(err)
	}
	b, err := image.ReadLayerInventory(after)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, l := range a {
		present[l.Digest] = struct{}{}
	}
	report := image.ReportFor(b, present)

	var moved []string
	for i, l := range b {
		if _, have := present[l.Digest]; !have {
			moved = append(moved, fmt.Sprintf("layer %d of %d (%d bytes)", i+1, len(b), l.Size))
		}
	}
	if report.CopiedLayers != 1 {
		t.Fatalf("a flake.nix-only edit re-delivers %d layers, want exactly 1 (the top tier): %s. "+
			"A lower tier now depends on something the edit moved, so every flake.nix edit "+
			"re-copies it.", report.CopiedLayers, strings.Join(moved, ", "))
	}
	if _, have := present[b[len(b)-1].Digest]; have {
		t.Fatalf("the one layer a flake.nix-only edit moved is not the top one (%s): the "+
			"identity file no longer sits in the top tier, or the tier order changed",
			strings.Join(moved, ", "))
	}
	if report.Copied > flakeOnlyRedeliveryBudget {
		t.Errorf("a flake.nix-only edit re-delivers %d bytes (%.1f MiB), over the %d MiB budget: "+
			"something whose content belongs in a lower tier now lands in the top one — a package "+
			"reached only through the root tree's closure (a curated link, a /lib-farm path) and "+
			"not listed in the base tier's `deps`. Every later flake.nix edit re-copies it in full "+
			"(docs/reference/image-staging-vs-baking.md#the-layer-plan).",
			report.Copied, float64(report.Copied)/(1<<20), flakeOnlyRedeliveryBudget>>20)
	}
}
