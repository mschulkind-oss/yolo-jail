package run

// macosuserforkslot_test.go pins THE FORK HALF OF THE BUILD SLOT ON macos-user
// (docs/reference/patched-forks.md PF-D97, docs/design/forked-programs-as-packs.md FP-D25), from
// Run's own entry point: a fresh macos-user launch hands its forks to the build act for the
// sandbox's darwin platform, never the tree half, hands the sandbox the act's keys, stages each
// delivered key into the sandbox's capture store, and refuses as a container launch does when a
// patched fork has no build or its series does not apply. A dry run builds nothing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosUserSlotRun is one macos-user launch through Run: its status, what it printed, and the
// launch env and host context it handed the backend (nil and zero when the backend never ran).
type macosUserSlotRun struct {
	rc      int
	out     string
	env     *jsonx.OrderedMap
	ctx     macosuser.HostContext
	ran     bool
	slot    []BuildSlotRequest
	pickArg map[string]string
}

func runMacosUserSlot(t *testing.T, mutate func(*Options)) macosUserSlotRun {
	t.Helper()
	var r macosUserSlotRun
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	// A Mac set up for this backend, so the arm's preflight admits the slot (macosUserPreflight).
	o.MacosUserLaunchProbes = setUpMacProbes
	store := t.TempDir()
	o.CapturesDir = func() string { return store }
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		ctx macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		r.env, r.ctx, r.ran = launchEnv, ctx, true
		return 0
	}
	o.MacosUserCaptures = func(dir string, bins []string, platform string, forks map[string]string) ([]macosuser.CaptureEntry, []string) {
		r.pickArg = forks
		var stage []macosuser.CaptureEntry
		for bin, key := range forks {
			stage = append(stage, macosuser.CaptureEntry{Bin: bin, Key: key, Source: dir + "/entries/" + key})
		}
		return stage, nil
	}
	o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
		r.slot = append(r.slot, req)
		return map[string]entrypoint.ForkDelivery{"tool": {Key: "abcdef0123"}}, nil
	}
	mutate(o)
	r.rc = Run(*o)
	r.out = stdout.String() + stderr.String()
	return r
}

// THE SLOT RUNS ON macos-user, for darwin: the patched fork goes to BuildSlot with the macos-user
// runtime and the sandbox's platform, no tree request, and its key reaches both the sandbox's fork
// decisions and its staged capture store. Red with the arm's slot call deleted (no BuildSlot), the
// plan's platform back to the container's, or the key left out of the wire or the staging.
func TestAMacosUserLaunchBuildsAPatchedForkForDarwinAndStagesIt(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(*Options) {})
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, r.ran, r.out)
	}
	if len(r.slot) != 1 || r.slot[0].Forks == nil {
		t.Fatalf("the macos-user launch called BuildSlot %d times (%+v), want once with its fork:\n%s", len(r.slot), r.slot, r.out)
	}
	req := r.slot[0]
	if req.Forks.Runtime != "macos-user" || req.Forks.Platform != macosUserJailPlatform() {
		t.Errorf("the fork request is for runtime %q platform %q, want macos-user and %s", req.Forks.Runtime,
			req.Forks.Platform, macosUserJailPlatform())
	}
	if req.Trees != nil {
		t.Errorf("the macos-user slot asked for a tree build: %+v", req.Trees)
	}
	if len(req.Forks.Pins) != 1 || req.Forks.Pins[0].Fork.Key() != "forkpack/tool" {
		t.Errorf("the fork request pins %+v, want forkpack/tool", req.Forks.Pins)
	}
	if d := handedOnMacosUser(t, r.env)["tool"]; d.Key != "abcdef0123" {
		t.Errorf("the sandbox is handed %+v for tool, want the act's key", d)
	}
	if r.pickArg["tool"] != "abcdef0123" {
		t.Errorf("the capture pick was asked for forks %v, want tool's delivered key", r.pickArg)
	}
	staged := false
	for _, e := range r.ctx.Captures {
		staged = staged || e.Key == "abcdef0123"
	}
	if !staged {
		t.Errorf("the sandbox's capture store stages %+v, want the fork's key", r.ctx.Captures)
	}
	if strings.Contains(r.out, "is not delivered on macos-user") || strings.Contains(r.out, "has no macOS build") {
		t.Errorf("a launch that delivered the fork says it does not:\n%s", r.out)
	}
}

// A LAUNCH THE BACKEND IS ABOUT TO REFUSE BUILDS NOTHING (macosUserPreflight), as it captures
// nothing: on a Mac where `yolo macos-setup` never ran, the slot is skipped and no missing-build
// refusal is printed, so the backend's own refusal, which names the setup, is the launch's.
// Red with the slot called whatever the preflight says.
func TestAMacosUserLaunchTheBackendWillRefuseBuildsNoFork(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(o *Options) {
		o.MacosUserLaunchProbes = func() macosuser.LaunchProbes {
			p := setUpMacProbes()
			p.SandboxUserExists = func() bool { return false }
			return p
		}
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			t.Errorf("a launch the backend will refuse ran the fork-build slot: %+v", req)
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "its build failed"}}, nil
		}
	})
	if !r.ran {
		t.Fatalf("the launch stopped before the backend, whose refusal it should have been (rc %d):\n%s", r.rc, r.out)
	}
	if strings.Contains(r.out, "has no build on this machine") {
		t.Errorf("a launch the backend will refuse printed a missing-build refusal:\n%s", r.out)
	}
}

// A LAUNCH THAT CANNOT READ WHAT OTHER SESSIONS WERE HANDED PRUNES NO STAGED ENTRY: a reaper that
// cannot ask declines (HandedForkKeys' error), so the host context says keep every staged entry,
// and the plan's prune command names no keys to keep. With readable records it prunes as before.
func TestAMacosUserLaunchThatCannotReadHandedKeysPrunesNoStagedCapture(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(*Options) {})
	if !r.ran || r.ctx.CapturesKeepAll {
		t.Fatalf("with readable records the launch kept every staged entry (ran %v, keepAll %v):\n%s",
			r.ran, r.ctx.CapturesKeepAll, r.out)
	}
	if err := os.MkdirAll(paths.PackTreeRoot("yolo-other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.PackTreeRoot("yolo-other"), "x"+handedForksSuffix), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = runMacosUserSlot(t, func(*Options) {})
	if !r.ran || !r.ctx.CapturesKeepAll {
		t.Fatalf("an unreadable handed record did not keep every staged entry (ran %v, keepAll %v):\n%s",
			r.ran, r.ctx.CapturesKeepAll, r.out)
	}
	if len(r.ctx.Captures) == 0 {
		t.Errorf("the launch staged nothing for its fork: %+v", r.ctx)
	}
}

// A PATCHED FORK WITH NO BUILD REFUSES the macos-user launch, as a container's does (PF-D77), with
// the hatches; the missing-program hatch starts it without the program.
func TestAMacosUserLaunchRefusesAPatchedForkWithNoBuild(t *testing.T) {
	patchedLaunchHome(t)
	none := func(o *Options) {
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "its build failed"}}, nil
		}
	}
	r := runMacosUserSlot(t, none)
	if r.rc == 0 || r.ran {
		t.Fatalf("Run() = %d, backend ran %v: a patched fork with no build did not refuse:\n%s", r.rc, r.ran, r.out)
	}
	for _, w := range []string{"ERROR: 1 selected patched fork has no build on this machine", "yolo capture",
		// PF-D97: the container backends, which build it for linux, before starting without it, in
		// the hatch list's own one-column format (patchrecovery.Lines).
		"\nStart anyway:\n" +
			"  YOLO_SETUP=apple-container-self yolo    launch on Apple Container\n" +
			"  YOLO_SETUP=podman-self yolo             launch on podman\n" +
			"  YOLO_ALLOW_MISSING_PROGRAMS=1 yolo      start without it\n"} {
		if !strings.Contains(r.out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, r.out)
		}
	}
	r = runMacosUserSlot(t, func(o *Options) { none(o); withEnv(o, paths.AllowMissingProgramsEnv, "1") })
	if r.rc != 0 || !r.ran {
		t.Fatalf("with %s=1: Run() = %d, backend ran %v:\n%s", paths.AllowMissingProgramsEnv, r.rc, r.ran, r.out)
	}
	if d := handedOnMacosUser(t, r.env)["tool"]; d.Key != "" || !strings.Contains(d.Reason, "YOLO_SETUP=apple-container-self yolo") ||
		!strings.Contains(d.Reason, "YOLO_ALLOW_MISSING_PROGRAMS=1 yolo") {
		t.Errorf("the sandbox is handed %+v for tool, want the reason and the macOS ways out", d)
	}
}

// A PATCH FAILURE WITH NO BUILD TO RUN on macos-user: the error block's list is the launch's only
// one (refuseMissingBuilds prints no second), so it names the container backends too (PF-D97).
func TestAMacosUserPatchFailureWithNoBuildNamesTheContainerBackends(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(o *Options) {
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "its series does not apply", PatchFailure: pfFailure()}}, nil
		}
	})
	if r.rc == 0 || r.ran {
		t.Fatalf("Run() = %d, backend ran %v: a patch failure with no build did not refuse:\n%s", r.rc, r.ran, r.out)
	}
	for _, w := range []string{"ERROR: fork forkpack/tool: patch series does not apply",
		"  YOLO_SETUP=apple-container-self yolo    launch on Apple Container\n" +
			"  YOLO_SETUP=podman-self yolo             launch on podman\n" +
			"  YOLO_ALLOW_MISSING_PROGRAMS=1 yolo      start without it\n"} {
		if !strings.Contains(r.out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, r.out)
		}
	}
}

// A PATCH FAILURE REFUSES the macos-user launch with its error block even when a good build serves
// (PF-D81), and YOLO_ALLOW_PATCH_FAILURES=1 runs that good build, handed and staged.
func TestAMacosUserLaunchRefusesAPatchFailureAndItsHatchRunsTheGoodBuild(t *testing.T) {
	patchedLaunchHome(t)
	failing := func(o *Options) {
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "abcdef0123", Runs: "v1.1.0 (0a1b2c3d) + 1 patch",
				PatchFailure: pfFailure()}}, nil
		}
	}
	r := runMacosUserSlot(t, failing)
	if r.rc == 0 || r.ran {
		t.Fatalf("Run() = %d, backend ran %v: a patch failure did not refuse:\n%s", r.rc, r.ran, r.out)
	}
	if !strings.Contains(r.out, "ERROR: fork forkpack/tool: patch series does not apply") ||
		!strings.Contains(r.out, "YOLO_ALLOW_PATCH_FAILURES=forkpack/tool yolo") {
		t.Errorf("the refusal lacks the error block or its hatch:\n%s", r.out)
	}
	r = runMacosUserSlot(t, func(o *Options) { failing(o); withEnv(o, "YOLO_ALLOW_PATCH_FAILURES", "1") })
	if r.rc != 0 || !r.ran {
		t.Fatalf("with YOLO_ALLOW_PATCH_FAILURES=1: Run() = %d, backend ran %v:\n%s", r.rc, r.ran, r.out)
	}
	if !strings.Contains(r.out, "Continuing with v1.1.0 (0a1b2c3d) + 1 patch (YOLO_ALLOW_PATCH_FAILURES)") {
		t.Errorf("the bypassed launch does not say what it runs:\n%s", r.out)
	}
	if d := handedOnMacosUser(t, r.env)["tool"]; d.Key != "abcdef0123" {
		t.Errorf("the sandbox is handed %+v, want the good build's key", d)
	}
	if strings.Contains(r.out, "YOLO_ALLOW_PATCH_FAILURES has no effect") {
		t.Errorf("the launch says the hatch it acted on has no effect:\n%s", r.out)
	}
}

// YOLO_USE_CACHED_GOOD ON macos-user (PF-D97): the selector reaches the darwin fork request, and
// the good build's key the act hands is staged, where it used to refuse the launch.
func TestAMacosUserLaunchServesTheCachedGoodFork(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(o *Options) {
		withEnv(o, CachedGoodEnv, "1")
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			if req.Forks == nil || req.Forks.CachedGoodOwner != "forkpack/tool" || req.Forks.Platform != macosUserJailPlatform() {
				t.Errorf("the slot was asked %+v, want the cached-good owner at darwin", req.Forks)
			}
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "cafe0123"}}, nil
		}
	})
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d, backend ran %v:\n%s", r.rc, r.ran, r.out)
	}
	if strings.Contains(r.out, "Refusing cached-good recovery") {
		t.Errorf("cached-good recovery refused on macos-user:\n%s", r.out)
	}
	if d := handedOnMacosUser(t, r.env)["tool"]; d.Key != "cafe0123" || r.pickArg["tool"] != "cafe0123" {
		t.Errorf("handed %+v, staged-pick %v; want the cached good key both ways", d, r.pickArg)
	}
}

// A DRY RUN BUILDS NOTHING: no BuildSlot, and its line says a launch without --dry-run builds it.
func TestAMacosUserDryRunBuildsNoFork(t *testing.T) {
	patchedLaunchHome(t)
	r := runMacosUserSlot(t, func(o *Options) { o.DryRun = true })
	if len(r.slot) != 0 {
		t.Fatalf("a dry run called BuildSlot: %+v", r.slot)
	}
	if !strings.Contains(r.out, "a dry run builds no fork") {
		t.Errorf("the dry run does not say it builds no fork:\n%s", r.out)
	}
	if strings.Contains(r.out, "has no macOS build") {
		t.Errorf("the dry run says a patched fork has no macOS build:\n%s", r.out)
	}
}
