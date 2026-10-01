package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The macOS nightly has been red on ONE cause since 2026-09-09, and these tests
// are the Linux-side reproduction of the DECISION behind it.
//
// The environment is unreproducible here — it needs a macOS Intel runner with a
// QEMU podman machine and no Linux builder. The decision is not: "build the
// image, or run the one the runtime already has" is a pure function of the
// launch's inputs, the identity oracle's answer and the runtime's reply, and all
// three are seams. So the class moves out of nightly-only and into the ordinary
// `go test -short` gate.
//
// Every test below drives the REAL AutoLoadImage. Pinning stockImageLoaded
// directly would be the shape AGENTS.md names — a test that stays green after
// its call site is deleted — and the call site is the whole fix: the defect was
// that AutoLoadImage's first act was `nix build`, with nothing asked of the
// runtime beforehand.

// stockOpts is c2Opts plus the three things the stock check reads: a repo root
// (so the launch is not degraded), an identity oracle, and an environment the
// test controls rather than the developer's shell.
func stockOpts(t *testing.T, f *fakeRuntime, out *bytes.Buffer, identity string) AutoLoadOptions {
	t.Helper()
	o := c2Opts("podman", storeManifest(t, "built-image"), f, out)
	o.RepoRoot = t.TempDir()
	o.EvalIdentity = func(string) (string, bool) {
		if identity == "" {
			return "", false
		}
		return identity, true
	}
	o.LookupEnv = func(string) (string, bool) { return "", false }
	// Never the real `nix-store --check-validity`: every recorded path in this
	// package's fixtures is a temp file, not a store path. Tests about validity
	// override it.
	o.StorePathValid = func(string) bool { return true }
	return o
}

// testIdentity is a well-formed identity: the algorithm tag plus 64 hex chars.
// Spelled out rather than computed so a change to ParseImageIdentity's shape
// fails here instead of silently agreeing with itself.
const testIdentity = "sha256:" +
	"816a3ee9bbc3b73bef97cdadb88702eadc4ee53bdcefd1c24e0d349b9304fb20"

// otherIdentity is a DIFFERENT tree's identity — the source-tree-moved case.
const otherIdentity = "sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000001"

// TestAStockImageInTheRuntimeSkipsTheBuild is the fix, stated as the behavior
// the macOS nightly needed: the image it was handed is this commit's, so nothing
// has to be built, so the absent Linux builder never matters.
//
// It fails if the call site is deleted: with the short-circuit gone,
// AutoLoadImage's first act is BuildStorePath, which this test makes fatal.
func TestAStockImageInTheRuntimeSkipsTheBuild(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	stockRef := StockImageRef("podman", testIdentity)
	f := newFakeRuntime(stockRef)
	o := stockOpts(t, f, &out, testIdentity)
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		t.Error("the image was built even though the runtime already holds this " +
			"source tree's stock image — on a darwin host with no Linux builder " +
			"that build is the whole of the macOS nightly's failure")
		return "", nil
	}
	o.BuildOffload = func(string, []any, string) (string, []string) {
		t.Error("the macOS build-offload ran for an image that is already loaded")
		return "", nil
	}

	res := AutoLoadImage(o)
	if !res.OK {
		t.Fatalf("AutoLoadImage refused a launch whose image is already present:\n%s", out.String())
	}
	if res.Ref != stockRef {
		t.Errorf("ran %q, want the stock ref %q", res.Ref, stockRef)
	}
	if res.StorePath != "" {
		t.Errorf("StorePath = %q; this host recorded no store path for the image, so "+
			"the launch has none to name, and recordCurrentImage keys off exactly that "+
			"emptiness", res.StorePath)
	}
	if f.loads != 0 || len(f.copiedDests) != 0 {
		t.Errorf("an already-present image was delivered again: %d loads, %d copies",
			f.loads, len(f.copiedDests))
	}
	// The launch stream has no quiet mode: a skipped build is a claim about WHICH
	// image is about to run, so it is disclosed with the evidence.
	if !strings.Contains(out.String(), stockRef) || !strings.Contains(out.String(), testIdentity) {
		t.Errorf("the skip was not disclosed with its ref and identity:\n%s", out.String())
	}
}

// TestNoStockTagMeansTheBuildRuns is the other direction, and it is what stops
// the check from being a blanket "never build": the runtime holds a jail image
// under every OTHER name, and none of them is evidence.
func TestNoStockTagMeansTheBuildRuns(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	// :latest and a content ref are present; the stock tag is not.
	f := newFakeRuntime(JailImage("podman"), JailImageRef("podman", "/nix/store/zzz-old"))
	o := stockOpts(t, f, &out, testIdentity)
	built := false
	manifest := storeManifest(t, "fresh-image")
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		built = true
		return manifest, nil
	}

	res := AutoLoadImage(o)
	if !built {
		t.Fatalf("no build ran although nothing in the runtime carries this tree's "+
			"stock tag — a present :latest is NOT evidence about what it holds:\n%s",
			out.String())
	}
	if !res.OK || res.Ref != JailImageRef("podman", manifest) {
		t.Fatalf("res = %+v, want the content ref for %s", res, manifest)
	}
}

// TestAMovedSourceTreeStillBuilds: the stock tag of a DIFFERENT identity is
// present. This is the staleness case the whole check exists to not create — a
// launch whose flake moved must not run yesterday's image.
func TestAMovedSourceTreeStillBuilds(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime(StockImageRef("podman", otherIdentity))
	o := stockOpts(t, f, &out, testIdentity)
	built := false
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		built = true
		return storeManifest(t, "rebuilt"), nil
	}

	if res := AutoLoadImage(o); !res.OK || !built {
		t.Fatalf("built=%v res=%+v — a stock image of ANOTHER identity was accepted "+
			"for this tree:\n%s", built, res, out.String())
	}
}

// TestANonStockLaunchNeverSkipsTheBuild is the silent-staleness guard, and it is
// the reason the check is keyed on a TAG rather than on the identity label.
//
// `imageIdentity` is DELIBERATELY invariant across the full/minimal/lean trio and
// across every `packages:` list (flake.nix says so where it declares the label),
// so an image carrying a matching identity may still be a lean image or one with
// extra packages baked in. Each row below puts the stock ref in the runtime and
// requires the build to run anyway.
func TestANonStockLaunchNeverSkipsTheBuild(t *testing.T) {
	stockRef := StockImageRef("podman", testIdentity)
	for _, tc := range []struct {
		name  string
		apply func(*AutoLoadOptions)
	}{{
		name:  "lean attr",
		apply: func(o *AutoLoadOptions) { o.Attr = ImageAttrLean },
	}, {
		name:  "packages: entries",
		apply: func(o *AutoLoadOptions) { o.ExtraPackages = []any{"zbar"} },
	}, {
		// buildImageStorePathArgs builds with os.Environ() and only APPENDS
		// YOLO_EXTRA_PACKAGES when the config carried some, so an ambient one
		// reaches the flake's builtins.getEnv and bakes packages into an image
		// whose identity says nothing about them.
		name: "ambient YOLO_EXTRA_PACKAGES",
		apply: func(o *AutoLoadOptions) {
			o.LookupEnv = func(k string) (string, bool) {
				if k == "YOLO_EXTRA_PACKAGES" {
					return `["zbar"]`, true
				}
				return "", false
			}
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			withBuildDir(t)
			var out bytes.Buffer
			f := newFakeRuntime(stockRef)
			o := stockOpts(t, f, &out, testIdentity)
			tc.apply(&o)
			built := false
			o.BuildStorePath = func(string, []any, string) (string, []string) {
				built = true
				return storeManifest(t, "variant"), nil
			}
			res := AutoLoadImage(o)
			if !built {
				t.Fatalf("a %s launch ran the STOCK image: it carries neither this "+
					"launch's variant nor its packages, and nothing downstream could "+
					"tell\n%s", tc.name, out.String())
			}
			if res.Ref == stockRef {
				t.Errorf("ref = %q, the stock image", res.Ref)
			}
		})
	}
}

// TestAnUnanswerableIdentityFallsBackToTheBuild: a host with no nix, a broken
// checkout, an eval that times out. An oracle that cannot answer is a harness
// limitation, never evidence about an image — so the launch does what every
// launch did before this check existed.
func TestAnUnanswerableIdentityFallsBackToTheBuild(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime(StockImageRef("podman", testIdentity))
	o := stockOpts(t, f, &out, "") // identity "" => EvalIdentity answers ok=false
	built := false
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		built = true
		return storeManifest(t, "fallback"), nil
	}
	if res := AutoLoadImage(o); !res.OK || !built {
		t.Fatalf("built=%v res=%+v — an unanswerable oracle must degrade to the "+
			"build, not to a guess:\n%s", built, res, out.String())
	}
}

// TestADegradedLaunchNeverConsultsTheIdentity: SkipBuild means there is no flake
// to evaluate (D2's degraded launch — repo-root resolution failed). Reaching for
// an oracle there would evaluate against whatever cwd the process happens to be
// in, which is the class the cwd removal deleted.
func TestADegradedLaunchNeverConsultsTheIdentity(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime(JailImage("podman"))
	o := stockOpts(t, f, &out, testIdentity)
	o.SkipBuild = true
	o.RepoRoot = ""
	o.EvalIdentity = func(string) (string, bool) {
		t.Error("a degraded launch evaluated a flake it has no root for")
		return "", false
	}
	if res := AutoLoadImage(o); !res.OK || res.Ref != JailImage("podman") {
		t.Fatalf("res = %+v, want the legacy ref the degraded branch owns:\n%s",
			res, out.String())
	}
}

// TestALoadedStockImageGetsItsStockTag is the OTHER call site, and without it
// the check above is dead code on every machine but a CI runner that tags by
// hand: something has to WRITE the tag, and only a load that knows it built the
// stock variant may.
func TestALoadedStockImageGetsItsStockTag(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime()
	o := stockOpts(t, f, &out, testIdentity)
	manifest := storeManifest(t, "first-load")
	o.BuildStorePath = func(string, []any, string) (string, []string) { return manifest, nil }

	res := AutoLoadImage(o)
	if !res.OK {
		t.Fatalf("load failed:\n%s", out.String())
	}
	stockRef := StockImageRef("podman", testIdentity)
	want := strings.Join(ImageTagCmd("podman", res.Ref, stockRef), " ")
	if !contains(f.cmds(), want) {
		t.Fatalf("the loaded stock image was never tagged %q.\nWithout it the next "+
			"launch rebuilds, and the pre-build check can never fire on a machine "+
			"that did not tag by hand.\nargv: %v", stockRef, f.cmds())
	}
	if f.present[stockRef] != f.present[res.Ref] {
		t.Errorf("the stock tag names %q but the launch ran %q — a tag must be a "+
			"SECOND NAME for the image, never a second image",
			f.present[stockRef], f.present[res.Ref])
	}
}

// TestANonStockLoadIsNeverTagged closes the loop with
// TestANonStockLaunchNeverSkipsTheBuild: the read side refuses a lean image, and
// the write side never lets one acquire the name in the first place. Either
// alone would leave the hazard representable.
func TestANonStockLoadIsNeverTagged(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime()
	o := stockOpts(t, f, &out, testIdentity)
	o.Attr = ImageAttrLean
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		return storeManifest(t, "lean-load"), nil
	}

	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("lean load failed:\n%s", out.String())
	}
	if ref := StockImageRef("podman", testIdentity); f.present[ref] != "" {
		t.Fatalf("a LEAN image was tagged %q. It carries neither fullPackages nor "+
			"the chromium half of the /lib farm, and a later stock launch would run "+
			"it believing it had both.", ref)
	}
}

// TestStockImageRefRejectsWhatIsNotAnIdentity. The values this has to refuse are
// not hypothetical: a pre-2026-09-12 image answers the identity question with a
// /nix/store path (OQ-IP3), and a `nix eval` on a broken checkout answers with
// prose. Both are strings, and both would compare equal to themselves.
func TestStockImageRefRejectsWhatIsNotAnIdentity(t *testing.T) {
	for _, bad := range []string{
		"",
		"ABSENT",
		"/nix/store/8r4ypm7z9qxmxvfhba7nxhkyhxm2qkzn-yolo-jail-image-identity",
		"sha256:",
		"sha256:816a3ee9",                   // too short
		"sha256:" + strings.Repeat("g", 64), // not hex
		"sha256:" + strings.Repeat("A", 64), // uppercase: not the spelling nix writes
		"sha1:" + strings.Repeat("a", 40),   // wrong algorithm
		strings.Repeat("a", 64),             // bare digest, no algorithm tag
	} {
		if ref := StockImageRef("podman", bad); ref != "" {
			t.Errorf("StockImageRef(%q) = %q; a value that is not an identity must "+
				"name no image", bad, ref)
		}
	}
	got := StockImageRef("podman", "  "+testIdentity+"\n")
	want := JailImageRepository("podman") + ":" + StockImageTagPrefix +
		strings.TrimPrefix(testIdentity, "sha256:")
	if got != want {
		t.Errorf("StockImageRef = %q, want %q (nix eval --raw still ends the value "+
			"with a newline)", got, want)
	}
}

// TestNightlyWorkflowTagsTheStockImage is a TWO-LANGUAGE SPELLING PIN, the shape
// internal/prune already uses for the image owner label.
//
// The workflow builds the image on a Linux runner, ships it to a Mac and tags it
// with the identity the image itself carries. That tag is the only thing that
// lets the darwin launch skip a build it cannot perform, and it is written in
// shell — so a rename of StockImageTagPrefix here would leave the nightly red
// with a green unit gate, which is exactly the class this repo has shipped five
// times.
func TestNightlyWorkflowTagsTheStockImage(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "nightly-macos.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	// The tag the workflow writes, with the shell's own parameter expansion for
	// the algorithm prefix: `stock-${IDENT#sha256:}`.
	want := JailImageRepository("podman") + ":" + StockImageTagPrefix + "${IDENT#sha256:}"
	if !strings.Contains(string(body), want) {
		t.Fatalf("%s does not tag the loaded image %q.\nWithout that tag every "+
			"launch on the macOS runner rebuilds the image from source, which needs "+
			"a Linux builder that job has never been able to start.", path, want)
	}
}

// THE REGRESSION THIS FILE EXISTS TO PREVENT A SECOND TIME. The stock-skip line
// is the only disclosure on the WARM path — the path every ordinary launch takes
// — and it was first written to Out. On the run path Out was then the jail command's
// own stdout, so `yolo -- bash -c "env | grep ..."` came back with a provenance
// sentence glued to the front of the command's output. Two integration tests that
// compare stdout EXACTLY went red on main within hours
// (TestProvidersRenderInTheAgentsOwnVocabulary and
// TestHostComposedBriefingIsNotDeliveredTwice), and NO -short gate could have
// caught it: both are integration tests, and `just check-ci` does not run them.
//
// Routing the line back to Out fails this test; deleting the Report wiring in
// run.autoLoadImage fails TestTheRunPathSendsImageDisclosuresToStderr. The run path
// now sends Out to stderr too (TestAColdImageLoadWritesNothingOnTheCommandsStdout),
// and this stays a pin of its own: a disclosure is Report's whatever a caller makes
// of Out.
func TestTheStockSkipDisclosureStaysOffTheCommandsStdout(t *testing.T) {
	withBuildDir(t)
	var out, report bytes.Buffer
	stockRef := StockImageRef("podman", testIdentity)
	o := stockOpts(t, newFakeRuntime(stockRef), &out, testIdentity)
	o.Report = &report

	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("AutoLoadImage refused a launch whose image is already present:\n%s", report.String())
	}

	if got := out.String(); strings.Contains(got, "Image build skipped") {
		t.Errorf("the disclosure reached Out rather than Report, so it goes wherever a caller "+
			"sends the load's general output — once the jail command's stdout, where it "+
			"corrupted the output of whatever the user asked the jail to run.\nOut:\n%s", got)
	}
	if got := report.String(); !strings.Contains(got, stockRef) || !strings.Contains(got, testIdentity) {
		t.Errorf("the disclosure did not reach Report with its ref and identity, so the "+
			"launch ran an image it never named. OQ-RO3: a disclosure may be compressed, "+
			"never suppressed.\nReport:\n%s", got)
	}
}

// The fallback is what keeps this change small: every caller that never sets
// Report — including the sibling tests in this file — behaves exactly as before.
func TestReportFallsBackToOutWhenUnset(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	stockRef := StockImageRef("podman", testIdentity)
	o := stockOpts(t, newFakeRuntime(stockRef), &out, testIdentity)
	o.Report = nil

	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("AutoLoadImage refused a launch whose image is already present:\n%s", out.String())
	}
	if got := out.String(); !strings.Contains(got, "Image build skipped") {
		t.Errorf("with Report unset the disclosure must still reach Out; got:\n%s", got)
	}
}
