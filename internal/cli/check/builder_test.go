package check

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// storeDeliveryHost is the filesystem half of an eligible host: both nix paths the
// launch would bind-mount are present.
func storeDeliveryHost(p string) bool {
	return p == hostNixDaemonSocket || p == hostNixStore
}

// TestPreflightBuildRequestFollowsTheLaunch is §7 F6's regression. The preflight built
// image.ImageAttrDefault unconditionally, so on a host whose next launch realizes the
// LEAN image plus the store-delivered extras, the Image section proved neither — green
// on an artifact that host will not build.
//
// MUTATION: make preflightBuildRequest return the baked shape always (i.e. delete the
// storeDeliveryLaunch branch) and the "store delivery" case fails on the attr.
func TestPreflightBuildRequestFollowsTheLaunch(t *testing.T) {
	optIn := func(name string) string {
		if name == storeDeliveryOptInEnv {
			return "1"
		}
		return ""
	}
	none := func(string) string { return "" }
	noPaths := func(string) bool { return false }
	pkgs := []any{"zbar"}

	for _, tc := range []struct {
		name       string
		getenv     func(string) string
		exists     func(string) bool
		isMacOS    bool
		wantAttr   string
		wantExtras bool // .#yoloImageExtras proven beside the image
		wantPkgs   bool // the config's packages reach YOLO_EXTRA_PACKAGES
	}{
		{"default host bakes", none, storeDeliveryHost, false,
			image.ImageAttrDefault, false, true},
		{"store delivery", optIn, storeDeliveryHost, false,
			image.ImageAttrLean, true, false},
		// The three ways an opt-in launch falls back to baking, and the preflight has
		// to fall back with it or it proves the wrong pair on each.
		{"opt-in without a mounted store", optIn, noPaths, false,
			image.ImageAttrDefault, false, true},
		{"opt-in on macOS", optIn, storeDeliveryHost, true,
			image.ImageAttrDefault, false, true},
		{"opt-in spelled falsely", func(string) string { return "please" }, storeDeliveryHost, false,
			image.ImageAttrDefault, false, true},
	} {
		req := preflightBuildRequest(tc.getenv, tc.exists, tc.isMacOS, "/repo", pkgs)
		if req.Attr != tc.wantAttr {
			t.Errorf("%s: attr = %q, want %q", tc.name, req.Attr, tc.wantAttr)
		}
		if got := slices.Contains(req.AlsoBuild, image.ImageExtrasAttr); got != tc.wantExtras {
			t.Errorf("%s: builds %s = %v, want %v — a lean image without its extras "+
				"is half a launch", tc.name, image.ImageExtrasAttr, got, tc.wantExtras)
		}
		if got := len(req.ExtraPackages) > 0; got != tc.wantPkgs {
			t.Errorf("%s: passes `packages:` = %v, want %v", tc.name, got, tc.wantPkgs)
		}
		// The lean image reads YOLO_EXTRA_PACKAGES too (flake.nix), so an ambient one
		// must be scrubbed on that path and left alone on the baked one — where the
		// launch inherits it as well, and check has to prove the same image.
		if wantScrub := tc.wantAttr == image.ImageAttrLean; req.NoExtraPackagesEnv != wantScrub {
			t.Errorf("%s: NoExtraPackagesEnv = %v, want %v",
				tc.name, req.NoExtraPackagesEnv, wantScrub)
		}
	}
}

// TestStoreDeliveryOptInMatchesTheLauncherSpellings: the dial accepts what every other
// launcher opt-in accepts (run.envTruthy), because two dials that disagree about what
// "true" looks like turn "I set the variable and nothing happened" into a legitimate
// bug report — and here it would silently preflight the wrong image.
func TestStoreDeliveryOptInMatchesTheLauncherSpellings(t *testing.T) {
	for _, yes := range []string{"1", "true", "yes", "TRUE", " Yes "} {
		getenv := func(string) string { return yes }
		if !storeDeliveryLaunch(getenv, storeDeliveryHost, false) {
			t.Errorf("%q must count as an opt-in", yes)
		}
	}
	for _, no := range []string{"", "0", "false", "no", "off", "maybe"} {
		getenv := func(string) string { return no }
		if storeDeliveryLaunch(getenv, storeDeliveryHost, false) {
			t.Errorf("%q must not count as an opt-in", no)
		}
	}
}

// preflightExec builds an Exec seam returning canned results for the two
// subprocess probes preflightBuilderNeeds drives: `nix build … --dry-run`
// (whose stderr classifies WillBuild) and `nix config show` (whose stdout feeds
// hasLinuxBuilder). Anything else degrades to "not ran".
func preflightExec(dryRunStderr string, dryRunRC int, configShow string) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		switch {
		case strings.Contains(key, "--dry-run"):
			return ExecResult{Stderr: dryRunStderr, RC: dryRunRC, Ran: true}
		case strings.Contains(key, "config show"):
			return ExecResult{Stdout: configShow, RC: 0, Ran: true}
		}
		return ExecResult{Ran: false}
	}
}

// buildStderr is a `nix build --dry-run` stderr that classifies as WillBuildYes
// with one offending derivation (a from-source package).
const buildStderr = "these 2 derivations will be built:\n" +
	"  /nix/store/aaa-yolo-jail-conf.json.drv\n" +
	"  /nix/store/bbb-foo.drv\n" +
	"these paths will be fetched:\n  /nix/store/ccc\n"

// TestBuildImageRealAsksWhichArtifactsToProve pins the CALL SITE the two tests above
// would otherwise leave free: preflightBuildRequest could be perfect and unused, and
// `yolo check` would go back to proving image.ImageAttrDefault on every host with the
// unit gate green. That shape — callee pinned, call site unpinned — is named in
// AGENTS.md as the thing this repo has shipped five times.
//
// Source-level for the reason internal/cli/captureinteractive_test.go is: the
// alternative is running a real multi-minute nix build to observe an argv. What it
// pins is the DECISION, which is the part a future edit drops.
func TestBuildImageRealAsksWhichArtifactsToProve(t *testing.T) {
	b, err := os.ReadFile("builder.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func buildImageReal(")
	if i < 0 {
		t.Fatal("buildImageReal is gone — this pin has lost its subject and is now vacuous")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j+1]
	}
	if !strings.Contains(body, "preflightBuildRequest(") {
		t.Error("buildImageReal no longer asks preflightBuildRequest which artifacts a " +
			"launch on this host implies, so the Image section is back to proving the " +
			"default image on a store-delivery host (setup-support-gaps.md §7 F6)")
	}
}

// TestPreflightBuilderNeeds_MacOSNoBuilder is the core rewire regression: on
// macOS, when a package must be built from source and the user has NO Linux
// builder configured, check must NOT tell the user to run `yolo builder …` or
// `nix run nixpkgs#darwin.linux-builder`. Instead it WARNs that a real `yolo`
// run offloads to a container builder, and returns false (skip the doomed local
// build — image.BuildOCIImage has no offload seam).
func TestPreflightBuilderNeeds_MacOSNoBuilder(t *testing.T) {
	var out bytes.Buffer
	o := &Options{IsMacOS: true, Exec: preflightExec(buildStderr, 0, "" /* no builders configured */)}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); viable {
		t.Error("WillBuildYes on macOS with no builder must be non-viable (return false)")
	}
	got := out.String()
	if r.failed != 0 {
		t.Errorf("must not FAIL (yolo builds fine via the offload); got %d fails:\n%s", r.failed, got)
	}
	if r.warned != 1 {
		t.Errorf("expected exactly one WARN, got %d:\n%s", r.warned, got)
	}
	if !strings.Contains(got, "container builder") {
		t.Errorf("WARN should name the container builder:\n%s", got)
	}
	for _, dead := range []string{"yolo builder", "darwin.linux-builder", "first boot", "first-boot"} {
		if strings.Contains(got, dead) {
			t.Errorf("dangling VM-builder reference %q in output:\n%s", dead, got)
		}
	}
}

// TestPreflightBuilderNeeds_MacOSOwnBuilder covers the §8 escape hatch: a user
// who configured their OWN Linux builder (nix-darwin linux-builder or
// /etc/nix/machines) keeps working — the build is viable (return true) with a
// PASS, no warning.
//
// The fixture's system is BuilderSystem(), not a literal: it must mean "a builder for
// the arch this host wants". Hardcoding aarch64-linux made this pass on an arm64
// runner and fail on an x86_64 one, which is the same arch assumption BACKLOG E8 was.
func TestPreflightBuilderNeeds_MacOSOwnBuilder(t *testing.T) {
	var out bytes.Buffer
	cfg := "builders = ssh-ng://mybox " + containerbuilder.BuilderSystem() + " /key 4\n"
	o := &Options{IsMacOS: true, Exec: preflightExec(buildStderr, 0, cfg)}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); !viable {
		t.Error("a user-configured Linux builder must make the build viable (return true)")
	}
	if r.failed != 0 || r.warned != 0 {
		t.Errorf("configured builder path should be a clean PASS; fails=%d warns=%d:\n%s", r.failed, r.warned, out.String())
	}
	if r.passed != 1 {
		t.Errorf("expected one PASS, got %d:\n%s", r.passed, out.String())
	}
}

// TestPreflightBuilderNeeds_MacOSWrongArchBuilder is the BACKLOG E8 regression at the
// check layer: a builder that serves a DIFFERENT Linux arch than this host wants cannot
// build this host's derivations, so it must not be reported as the escape hatch. Before
// the fix the probe matched a hardcoded aarch64-linux, so an x86_64 host with an
// arm64-only builder got a confident PASS and then a build nix refused to offload.
func TestPreflightBuilderNeeds_MacOSWrongArchBuilder(t *testing.T) {
	other := "aarch64-linux"
	if containerbuilder.BuilderSystem() == other {
		other = "x86_64-linux"
	}
	var out bytes.Buffer
	cfg := "builders = ssh-ng://mybox " + other + " /key 4\n"
	o := &Options{IsMacOS: true, Exec: preflightExec(buildStderr, 0, cfg)}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); viable {
		t.Errorf("a %s-only builder must not make an %s build viable",
			other, containerbuilder.BuilderSystem())
	}
	if r.passed != 0 {
		t.Errorf("wrong-arch builder must not PASS; got %d:\n%s", r.passed, out.String())
	}
	// It falls through to the container-builder WARN, which is the correct advice.
	if r.warned != 1 || !strings.Contains(out.String(), "container builder") {
		t.Errorf("expected the container-builder WARN; warns=%d:\n%s", r.warned, out.String())
	}
}

// TestPreflightBuilderNeeds_Linux confirms the Linux branch is unchanged: a
// from-source build is a native Linux build, always viable, no builder question.
func TestPreflightBuilderNeeds_Linux(t *testing.T) {
	var out bytes.Buffer
	o := &Options{IsMacOS: false, Exec: preflightExec(buildStderr, 0, "")}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); !viable {
		t.Error("Linux from-source build must be viable")
	}
	if r.failed != 0 || r.warned != 0 {
		t.Errorf("Linux path emits a dim note only; fails=%d warns=%d", r.failed, r.warned)
	}
}

// TestPreflightBuilderNeeds_FullyCached confirms the WillBuildNo happy path:
// everything substitutable → viable, no builder needed, on either platform.
func TestPreflightBuilderNeeds_FullyCached(t *testing.T) {
	substOnly := "these paths will be fetched (10 MiB download):\n  /nix/store/x\n"
	for _, mac := range []bool{true, false} {
		var out bytes.Buffer
		o := &Options{IsMacOS: mac, Exec: preflightExec(substOnly, 0, "")}
		r := newReporter(&out, false)
		if viable := o.preflightBuilderNeeds(r, t.TempDir(), nil); !viable {
			t.Errorf("fully-cached (macOS=%v) must be viable", mac)
		}
		if r.failed != 0 || r.warned != 0 {
			t.Errorf("fully-cached (macOS=%v) must be clean; fails=%d warns=%d", mac, r.failed, r.warned)
		}
	}
}
