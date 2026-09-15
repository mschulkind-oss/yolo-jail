package entrypoint

// mcp_test.go covers the WIRED chrome-devtools argv — the one an MCP client actually
// spawns. It is deliberately written against LoadMCPServers rather than against
// chromiumExecutablePath, because the defect it guards was never a wrong resolver: the
// resolver existed (chrome-devtools-mcp-wrapper has resolved chromium at run time all
// along) and the argv pinned "/usr/bin/chromium" beside it. A test that exercised the
// resolver alone would have stayed green through the entire outage — the CALLEE-pinned,
// CALL-SITE-unpinned shape AGENTS.md names. So every assertion below reads the emitted
// argv, and every one of them fails if the literal goes back into chromeDevtoolsArgs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// chromeDevtoolsExecutablePath pulls the value the wired chrome-devtools entry hands the
// MCP server as --executablePath, going through LoadMCPServers exactly as
// packsurfaces.go's liveTables does.
func chromeDevtoolsExecutablePath(t *testing.T, e *Env) string {
	t.Helper()
	v, ok := e.LoadMCPServers().Get("chrome-devtools")
	if !ok {
		t.Fatal("LoadMCPServers dropped the chrome-devtools preset")
	}
	cfg, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("chrome-devtools entry is %T, want an *jsonx.OrderedMap", v)
	}
	rawArgs, ok := cfg.Get("args")
	if !ok {
		t.Fatal("chrome-devtools entry has no args")
	}
	args, ok := rawArgs.([]any)
	if !ok {
		t.Fatalf("chrome-devtools args is %T, want []any", rawArgs)
	}
	var found []string
	for i, a := range args {
		if s, isStr := a.(string); !isStr || s != "--executablePath" {
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("--executablePath is the last argv element, with no value: %v", args)
		}
		val, isStr := args[i+1].(string)
		if !isStr {
			t.Fatalf("--executablePath value is %T, want a string: %v", args[i+1], args)
		}
		found = append(found, val)
	}
	// Exactly one, so a "fix" that appends a resolved flag beside the pinned one — leaving
	// whichever the MCP server happens to prefer in charge — fails here rather than
	// looking like a pass.
	if len(found) != 1 {
		t.Fatalf("chrome-devtools argv carries %d --executablePath flags, want 1: %v",
			len(found), args)
	}
	return found[0]
}

// presetEnv is an Env with the chrome-devtools preset enabled, pointed at a throwaway home.
func presetEnv(t *testing.T, extra map[string]string) *Env {
	t.Helper()
	vars := map[string]string{
		"JAIL_HOME":        t.TempDir(),
		"YOLO_MCP_PRESETS": `["chrome-devtools"]`,
	}
	for k, v := range extra {
		vars[k] = v
	}
	return NewEnv(vars)
}

// fakeImageBins points imageProbeBase at a temp dir standing in for the image's own bin
// dirs, and returns it. The real /bin and /usr/bin must not be read: this test runs on a
// HOST as well as in a jail, and whether the machine happens to have a chromium is not
// what is being measured. imageProbeBase is a var for exactly this
// (shippedinstallvia_test.go uses it the same way).
func fakeImageBins(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := imageProbeBase
	imageProbeBase = dir
	t.Cleanup(func() { imageProbeBase = orig })
	return dir
}

// TestChromeDevtoolsArgvResolvesChromiumFromWhatTheLaunchProvides is THE test for this
// behaviour: revert chromeDevtoolsArgs to the "/usr/bin/chromium" literal and it fails.
//
// Why it matters, in the words of the thing that broke: a YOLO_STORE_PACKAGES=1 launch
// builds .#ociImageLean, whose bin-path links come from
// `binPathLinksLean = mkBinPathLinks { withChromium = false; }` — so /usr/bin/chromium is
// not created — and gets chromium from the .#yoloImageExtras store profile the boot links
// into /run/yolo/packages/bin instead (flake.nix; storepackages.go). The argv has to name
// the copy this launch actually has.
func TestChromeDevtoolsArgvResolvesChromiumFromWhatTheLaunchProvides(t *testing.T) {
	bins := fakeImageBins(t)
	chromium := filepath.Join(bins, "chromium")
	if err := os.WriteFile(chromium, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed chromium: %v", err)
	}

	if got := chromeDevtoolsExecutablePath(t, presetEnv(t, nil)); got != chromium {
		t.Errorf("--executablePath = %q, want %q — the wired argv must name the chromium "+
			"THIS launch provides, not a path baked into the source. A lean "+
			"(YOLO_STORE_PACKAGES=1) image has no /usr/bin/chromium at all.", got, chromium)
	}
}

// TestChromeDevtoolsArgvSearchesTheStorePackageFarm is the store-delivered half, and it is
// the case the defect was actually about.
//
// The farm's own dir is a fixed /run tmpfs path that a unit test cannot create (it needs
// root on a CI runner), so the assertion is split in two, both halves through the call
// site: the resolution reaches the farm's POSITION — imageProbePath leads with it on an
// opted-in launch, which is what makes a store-delivered chromium win over anything the
// lean image might still have — and the emitted argv is the resolved path rather than the
// pin. TestImageProbePathCountsStoreDeliveredPackages (storepackages_test.go) is where the
// farm's presence on that path is pinned on its own.
func TestChromeDevtoolsArgvSearchesTheStorePackageFarm(t *testing.T) {
	bins := fakeImageBins(t)
	chromium := filepath.Join(bins, "chromium")
	if err := os.WriteFile(chromium, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed chromium: %v", err)
	}
	e := presetEnv(t, map[string]string{StoreProfilesEnv: "/nix/store/aaa-profile"})

	segs := strings.Split(imageProbePath(e), ":")
	if segs[0] != StorePackagesBin() {
		t.Fatalf("the path the argv resolves against is %v — a store-delivered chromium "+
			"lives in %s and must be found there first", segs, StorePackagesBin())
	}
	if got := chromeDevtoolsExecutablePath(t, e); got != chromium {
		t.Errorf("--executablePath = %q, want %q — an opted-in launch must still get a "+
			"resolved path, never the baked pin", got, chromium)
	}
}

// TestChromeDevtoolsArgvFallsBackToTheBakedChromiumPath pins the no-chromium-anywhere case
// to the value it has always had.
//
// The alternative failure modes are both worse than the old behaviour: an empty
// --executablePath, or the flag dropped so the MCP server goes looking for a browser to
// download. A jail with no chromium should fail the way it already failed.
func TestChromeDevtoolsArgvFallsBackToTheBakedChromiumPath(t *testing.T) {
	fakeImageBins(t) // seeded with nothing

	if got := chromeDevtoolsExecutablePath(t, presetEnv(t, nil)); got != bakedChromiumPath {
		t.Errorf("--executablePath = %q, want %q — with nothing to resolve, the entry must "+
			"stay byte-for-byte what it was before it learned to resolve", got, bakedChromiumPath)
	}
}

// TestChromeDevtoolsKeepsASoftwareRenderer pins the rendering half of the wired argv.
// Headless Chromium can run without a hardware GPU, but Page.captureScreenshot still
// needs SwiftShader (its software rasterizer). Passing --disable-gpu together with
// --disable-software-rasterizer starts a browser that can navigate and inspect the DOM
// while every screenshot fails with Page.captureScreenshot: Internal error.
func TestChromeDevtoolsKeepsASoftwareRenderer(t *testing.T) {
	v, ok := presetEnv(t, nil).LoadMCPServers().Get("chrome-devtools")
	if !ok {
		t.Fatal("LoadMCPServers dropped the chrome-devtools preset")
	}
	cfg := v.(*jsonx.OrderedMap)
	rawArgs, _ := cfg.Get("args")
	args := rawArgs.([]any)
	for _, arg := range args {
		if arg == "--chrome-arg=--disable-software-rasterizer" {
			t.Fatalf("wired chrome-devtools argv disables Chromium's only renderer: %v", args)
		}
	}
	if strings.Contains(chromeWrapper, "--disable-software-rasterizer") {
		t.Fatal("chrome-devtools wrapper disables Chromium's only renderer")
	}
}
