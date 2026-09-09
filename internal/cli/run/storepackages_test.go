package run

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// storepackages_test.go covers C4/C5's host half: who may take the store-delivery fast
// path, what happens to a launch that asks and cannot, and — the assertion the whole
// feature rests on — that an opt-in launch builds an image WITHOUT its packages
// (docs/reference/image-staging-vs-baking.md, "Store-delivered packages").

// storeOptions builds an Options with the deterministic seams these tests need.
func storeOptions(t *testing.T, env map[string]string) (*Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	o := &Options{
		Stdout:  &out,
		Stderr:  &errOut,
		Getenv:  func(k string) string { return env[k] },
		IsLinux: true,
		PathExists: func(p string) bool {
			return p == "/nix/store" || p == "/nix/var/nix/daemon-socket"
		},
	}
	return o, &out, &errOut
}

func cfgWithPackages(t *testing.T, body string) *jsonx.OrderedMap {
	t.Helper()
	decoded, err := jsonx.Decode([]byte(body))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("config is not an object: %T", decoded)
	}
	return m
}

// TestStorePackagesEligibility is §3.2's table, restated as the only three reasons the
// fast path can be refused. Each is a real constraint, not a policy: Apple Container
// cannot bind-mount the host store at all, a macOS podman VM shares no store, and without
// the socket + store there is nothing to resolve a store path against.
func TestStorePackagesEligibility(t *testing.T) {
	cases := []struct {
		name        string
		rt          string
		macOS       bool
		mounted     bool
		want        bool
		reasonWords string
	}{
		{"linux podman with the store", "podman", false, true, true, ""},
		{"apple container", "container", false, true, false, "podman"},
		{"macos podman", "podman", true, true, false, "Linux host"},
		{"no store mounted", "podman", false, false, false, "/nix/store"},
	}
	for _, tc := range cases {
		got, why := storePackagesEligible(tc.rt, tc.macOS, tc.mounted)
		if got != tc.want {
			t.Errorf("%s: eligible = %v, want %v (%s)", tc.name, got, tc.want, why)
		}
		if !tc.want && !strings.Contains(why, tc.reasonWords) {
			t.Errorf("%s: reason %q does not mention %q — the operator reads this line "+
				"to know why the dial did nothing", tc.name, why, tc.reasonWords)
		}
	}
}

// TestStorePackagesDefaultIsBaked: no opt-in, no change. The DEFAULT is the ruling, not a
// hedge (OQ-1), and it must cost nothing — no nix invocation, no plan, no output.
func TestStorePackagesDefaultIsBaked(t *testing.T) {
	o, out, errOut := storeOptions(t, nil)
	o.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		t.Fatal("a launch that did not opt in must not run nix at all")
		return "", nil, nil
	}
	plan, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["zbar"]}`),
		"podman", "/repo", true)
	if !ok || plan.Active {
		t.Fatalf("plan = %+v, ok = %v; want an inactive plan and a viable launch", plan, ok)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("a launch that did not opt in printed something:\n%s%s", out, errOut)
	}
}

// TestStorePackagesIneligibleFallsBackLoudly is the direction the fallback must go, and
// why. Baking yields a jail with all its tools, so refusing here would cost the user a
// working launch to enforce a preference about where bytes live. What is not acceptable is
// doing it silently: a fast path that quietly is not one gets its cost attributed to the
// wrong mechanism.
func TestStorePackagesIneligibleFallsBackLoudly(t *testing.T) {
	o, _, errOut := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	o.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		t.Fatal("an ineligible launch must not run nix")
		return "", nil, nil
	}
	plan, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["zbar"]}`),
		"container", "/repo", true)
	if !ok {
		t.Fatal("an ineligible opt-in must NOT refuse the launch: baking still gives " +
			"the user every tool they asked for")
	}
	if plan.Active {
		t.Fatal("the plan must be inactive, or the image is built without packages the " +
			"jail then has no way to get")
	}
	// On STDERR, like every other launch notice; TestStorePackagesNoticesGoToStderr
	// is the pin for the stream itself.
	if !strings.Contains(errOut.String(), StorePackagesOptInEnv) {
		t.Errorf("the fallback was SILENT; output was:\n%s", errOut)
	}
}

// TestStorePackagesRefusesAPackageThatDidNotBuild mirrors macos-user's A2 ruling exactly,
// and the reason is stronger here: on an opt-in launch the image does not contain the
// package either, so launching anyway hands the agent a jail whose declared tool simply
// is not there.
func TestStorePackagesRefusesAPackageThatDidNotBuild(t *testing.T) {
	o, _, errOut := storeOptions(t, map[string]string{StorePackagesOptInEnv: "true"})
	o.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		return "/nix/store/aaa-profile", []string{"ripgrpe"}, nil
	}
	_, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["ripgrpe"]}`),
		"podman", "/repo", true)
	if ok {
		t.Fatal("a declared package with no build must be FATAL — a typo and a genuinely " +
			"unavailable package are indistinguishable, and both surface later as a " +
			"command that mysteriously does not exist")
	}
	if !strings.Contains(errOut.String(), "ripgrpe") {
		t.Errorf("the refusal must name the package, got:\n%s", errOut)
	}
}

// TestStorePackagesRefusesAFailedBuild: nix said no. There is nothing to fall back to
// at this point that would not silently be the other mechanism.
func TestStorePackagesRefusesAFailedBuild(t *testing.T) {
	o, _, errOut := storeOptions(t, map[string]string{StorePackagesOptInEnv: "yes"})
	o.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		return "", nil, errors.New("nix command not found on PATH")
	}
	if _, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["zbar"]}`),
		"podman", "/repo", true); ok {
		t.Fatal("a failed materialization must refuse the launch")
	}
	if !strings.Contains(errOut.String(), "nix command not found") {
		t.Errorf("the refusal must carry nix's own reason, got:\n%s", errOut)
	}
}

// TestStorePackagesActivePlanCarriesTheProfile is the happy path, and it pins the two
// things everything downstream reads: Active, and the ordered profile list.
func TestStorePackagesActivePlanCarriesTheProfile(t *testing.T) {
	o, _, _ := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	var sawRepo string
	var sawPkgs []any
	o.MaterializeStorePackages = func(repo string, pkgs []any) (string, []string, error) {
		sawRepo, sawPkgs = repo, pkgs
		return "/nix/store/aaa-profile", nil, nil
	}
	plan, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["zbar"]}`),
		"podman", "/repo", true)
	if !ok || !plan.Active {
		t.Fatalf("plan = %+v, ok = %v", plan, ok)
	}
	if !slices.Equal(plan.Profiles, []string{"/nix/store/aaa-profile"}) {
		t.Errorf("Profiles = %v", plan.Profiles)
	}
	if sawRepo != "/repo" {
		t.Errorf("materializer ran against repo %q, want /repo — an empty Dir makes nix "+
			"resolve whatever flake the user is standing in", sawRepo)
	}
	if len(sawPkgs) != 1 {
		t.Errorf("materializer saw %d packages, want the config's 1", len(sawPkgs))
	}

	// An opt-in launch with NO packages is still Active: Active is what tells the image
	// build to leave YOLO_EXTRA_PACKAGES unset, and under C5 there is content to move out
	// of the image even when the workspace declares nothing.
	empty, ok := o.planStorePackages(cfgWithPackages(t, `{}`), "podman", "/repo", true)
	if !ok || !empty.Active || len(empty.Profiles) != 0 {
		t.Errorf("empty-packages plan = %+v, ok = %v; want Active with no profiles", empty, ok)
	}
}

// TestOptInLaunchBuildsTheStockImage IS R2, and it is the call-site test for the one
// assignment the whole feature reduces to.
//
// "The baked path is retained" is per LAUNCH, never per package: a boot-written PATH dir
// cannot outrank the image (§3.1), so a package both baked and staged silently runs the
// BAKED copy. Delete `extra = nil` from autoLoadImage and every other test in this file
// still passes while the jail runs its packages out of the image — and the saving C4
// exists for, one image per machine instead of one per distinct `packages:` list,
// disappears with it.
func TestOptInLaunchBuildsTheStockImage(t *testing.T) {
	cfg := cfgWithPackages(t, `{"packages": ["zbar", "libsodium.dev"]}`)
	for _, tc := range []struct {
		name      string
		plan      storePackagesPlan
		wantExtra int
	}{
		{"baked (the default)", storePackagesPlan{}, 2},
		{"opted in", storePackagesPlan{Active: true, Profiles: []string{"/nix/store/aaa"}}, 0},
	} {
		var seen image.AutoLoadOptions
		o := &Options{
			Stdout:      os.Stdout,
			Stderr:      os.Stderr,
			Getenv:      func(string) string { return "" },
			IsTTYStdout: func() bool { return false },
			Getpid:      os.Getpid,
			autoLoad: func(opts image.AutoLoadOptions) image.LoadResult {
				seen = opts
				return image.LoadResult{OK: true}
			},
		}
		o.autoLoadImage(cfg, "podman", "/repo", tc.plan)
		if got := len(seen.ExtraPackages); got != tc.wantExtra {
			t.Errorf("%s: the image build got %d ExtraPackages, want %d. An opt-in "+
				"launch MUST build the stock image: a package that is both baked and "+
				"staged silently runs the baked copy (R2), and an image that still "+
				"reads YOLO_EXTRA_PACKAGES is still one image per distinct list (§1.5).",
				tc.name, got, tc.wantExtra)
		}
	}
}

// TestOptInLaunchBuildsTheLeanImage is C5's half of the same call site, and it fails if
// the attr selection is deleted.
//
// The saving IS the attr: an opt-in launch that still built `.#ociImage` would carry
// `fullPackages` and the chromium half of the /lib farm in its closure while ALSO linking
// a store profile that holds them — both copies present, the baked one silently winning
// (R2), and ~1.6–2 GB of image bought for nothing. An empty attr means the historical
// default, so every caller that never heard of the lean variant is unaffected.
func TestOptInLaunchBuildsTheLeanImage(t *testing.T) {
	cfg := cfgWithPackages(t, `{}`)
	for _, tc := range []struct {
		name string
		plan storePackagesPlan
		want string
	}{
		{"baked (the default)", storePackagesPlan{}, image.ImageAttrDefault},
		{"opted in", storePackagesPlan{Active: true, Profiles: []string{"/nix/store/aaa"}}, image.ImageAttrLean},
	} {
		var seen image.AutoLoadOptions
		o := &Options{
			Stdout:      os.Stdout,
			Stderr:      os.Stderr,
			Getenv:      func(string) string { return "" },
			IsTTYStdout: func() bool { return false },
			Getpid:      os.Getpid,
			autoLoad: func(opts image.AutoLoadOptions) image.LoadResult {
				seen = opts
				return image.LoadResult{OK: true}
			},
		}
		o.autoLoadImage(cfg, "podman", "/repo", tc.plan)
		if seen.Attr != tc.want {
			t.Errorf("%s: the image build got attr %q, want %q. The attr IS the saving: "+
				"an opt-in launch that still built the full image would carry "+
				"fullPackages in its closure AND link a store profile that holds them.",
				tc.name, seen.Attr, tc.want)
		}
	}
}

// TestImageExtrasRideBehindTheWorkspacePackages pins C5's profile ORDER, which is what
// makes the farm's first-wins rule reproduce the precedence a baked image already gives.
//
// `packages:` and `fullPackages` both land in a baked image's `contents`, and a workspace
// that declares its own version of a tool yolo also ships expects to get its own. Put the
// extras first and that silently inverts — for `fzf`, which is in fullPackages, among
// others.
func TestImageExtrasRideBehindTheWorkspacePackages(t *testing.T) {
	o, _, _ := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	o.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		return "/nix/store/user-profile", nil, nil
	}
	o.BuildImageExtras = func(string) (string, error) { return "/nix/store/extras-profile", nil }

	plan, ok := o.planStorePackages(cfgWithPackages(t, `{"packages": ["fzf"]}`),
		"podman", "/repo", true)
	if !ok {
		t.Fatal("planStorePackages refused")
	}
	plan, ok = o.addImageExtras(plan, "/repo")
	if !ok {
		t.Fatal("addImageExtras refused")
	}
	want := []string{"/nix/store/user-profile", "/nix/store/extras-profile"}
	if !slices.Equal(plan.Profiles, want) {
		t.Errorf("profiles = %v, want %v — the workspace's own `packages:` must LEAD, "+
			"because the jail's farm is first-wins and a baked image gives them the "+
			"same precedence", plan.Profiles, want)
	}

	// A launch that bakes must not pay for a build whose output it will not use.
	baked, _, _ := storeOptions(t, nil)
	baked.BuildImageExtras = func(string) (string, error) {
		t.Fatal("a launch that did not opt in must not build the extras profile")
		return "", nil
	}
	if got, ok := baked.addImageExtras(storePackagesPlan{}, "/repo"); !ok || len(got.Profiles) != 0 {
		t.Errorf("baked plan = %+v, ok = %v", got, ok)
	}
}

// TestImageExtrasFailureRefusesTheLaunch: the lean image was going to be built WITHOUT
// these packages, so there is nothing to fall back to that is not silently the other
// mechanism.
func TestImageExtrasFailureRefusesTheLaunch(t *testing.T) {
	o, _, errOut := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	o.BuildImageExtras = func(string) (string, error) {
		return "", errors.New("attribute 'yoloImageExtras' missing")
	}
	if _, ok := o.addImageExtras(storePackagesPlan{Active: true}, "/repo"); ok {
		t.Fatal("a failed extras build must refuse the launch")
	}
	if !strings.Contains(errOut.String(), "yoloImageExtras") {
		t.Errorf("the refusal must carry nix's own reason, got:\n%s", errOut)
	}
}

// TestStoreProfilesReachTheContainerArgv is the argv-side call site. Deleting the
// `in.storePackages.env()` append leaves the farm's generator with nothing to read, so the
// jail boots with neither the baked packages nor the staged ones.
func TestStoreProfilesReachTheContainerArgv(t *testing.T) {
	pair := func(in *assembleInput) []string { return in.storePackages.env() }

	if got := pair(&assembleInput{}); got != nil {
		t.Errorf("a baked launch emitted %v; it must emit nothing, or the jail claims "+
			"store delivery is live", got)
	}
	if got := pair(&assembleInput{storePackages: storePackagesPlan{Active: true}}); got != nil {
		t.Errorf("an active plan with NO profiles emitted %v; an empty variable would "+
			"make the jail build a farm out of nothing", got)
	}
	got := pair(&assembleInput{storePackages: storePackagesPlan{
		Active:   true,
		Profiles: []string{"/nix/store/aaa", "/nix/store/bbb"},
	}})
	want := []string{"-e", entrypoint.StoreProfilesEnv + "=/nix/store/aaa:/nix/store/bbb"}
	if !slices.Equal(got, want) {
		t.Errorf("store-profiles env pair = %v, want %v — the ORDER is precedence, and "+
			"the jail's farm is first-wins", got, want)
	}
}

// TestStoreProfileRootIsKeyedByContent is the multi-jail case gcroot.go's fixed leaf does
// NOT cover, and the reason C4 could not use darwinpkg.Materialize unmodified.
//
// A non-container notch has at most one profile current per home, so one link that moves
// is right. A machine running jails has several live at once — one per workspace — so a
// fixed leaf means launching jail B unroots the closure jail A is still executing from,
// and the next `nix store gc` deletes the agent's toolset mid-session. That is the exact
// N1 defect the root exists to close, resurrected by concurrency.
func TestStoreProfileRootIsKeyedByContent(t *testing.T) {
	a := storeProfileRootLink([]any{"zbar"}, false)
	b := storeProfileRootLink([]any{"libsodium"}, false)
	if a == "" || b == "" {
		t.Fatal("a host launch must root its profile: an unrooted closure the agent then " +
			"executes from is exactly what a GC root exists to prevent")
	}
	if a == b {
		t.Errorf("two different `packages:` lists share the GC root %q — one jail's "+
			"launch would then unroot the closure another jail is running from", a)
	}
	if again := storeProfileRootLink([]any{"zbar"}, false); again != a {
		t.Errorf("the root is not stable for one list: %q then %q", a, again)
	}
	if !strings.Contains(a, "package-roots") {
		t.Errorf("root %q is not under paths.PackageRootsDir — parking it under "+
			"build/roots would let PruneOrphanImageRoots sweep it away", a)
	}
	// In-jail, rooting is a lie: the gcroots dir is unmounted and the host daemon prunes
	// a root pointing into a jail home as stale. Same ruling imageload.go's rootImageFn
	// already makes for images.
	if got := storeProfileRootLink([]any{"zbar"}, true); got != "" {
		t.Errorf("in-jail root = %q, want \"\" (the unrooted build)", got)
	}
}

// TestEnvTruthyMatchesTheOtherOptInDials keeps two dials from disagreeing about what the
// operator typed. shouldMountHostNix has always accepted 1/true/yes; a store-delivery dial
// that accepted a different set would make "I set the variable and nothing happened" a
// legitimate bug report.
func TestEnvTruthyMatchesTheOtherOptInDials(t *testing.T) {
	for _, yes := range []string{"1", "true", "TRUE", "yes", " yes "} {
		if !envTruthy(yes) {
			t.Errorf("envTruthy(%q) = false", yes)
		}
		if !shouldMountHostNix("podman", true, true, true, yes) {
			t.Errorf("shouldMountHostNix disagrees about %q", yes)
		}
	}
	for _, no := range []string{"", "0", "false", "no", "maybe"} {
		if envTruthy(no) {
			t.Errorf("envTruthy(%q) = true", no)
		}
	}
}

// TestStorePackagesNoticesGoToStderr pins the stream of every progress line C4/C5
// prints. The rule is warnIfNoPacks': stdout belongs to the jailed command, so a
// launch notice there is swallowed by the user's redirect or corrupts a piped payload.
//
// This file shipped with the split that causes the bug: its REFUSALS were already on
// stderr while its two PROGRESS lines were on stdout. That is the identical defect C8's
// build notice shipped, and it broke CI twice in two days through two separate call
// sites (6580186c, then CI run 34079261711 — both times taking out
// TestHostComposedBriefingIsNotDeliveredTwice and TestProvidersRenderInTheAgentsOwn-
// Vocabulary). Here it is LATENT rather than constant only because the path is opt-in:
// the first CI job to set YOLO_STORE_PACKAGES=1 would reproduce that outage exactly.
//
// Each block pins one print site — delete the print and its stderr half fails, move it
// back to o.Stdout and its stdout half fails.
func TestStorePackagesNoticesGoToStderr(t *testing.T) {
	// (1) The ineligible-fallback notice: opted in, but this backend cannot.
	ineligible, stdout, stderr := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	if _, ok := ineligible.planStorePackages(
		cfgWithPackages(t, `{"packages": ["zbar"]}`), "container", "/repo", true); !ok {
		t.Fatal("an ineligible opt-in must not refuse the launch")
	}
	assertNoticeOnStderr(t, "the ineligible-fallback notice", stdout, stderr, StorePackagesOptInEnv)

	// (2) The `packages:` realization notice, on the active path.
	active, stdout, stderr := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	active.MaterializeStorePackages = func(string, []any) (string, []string, error) {
		return "/nix/store/aaa-profile", nil, nil
	}
	if _, ok := active.planStorePackages(
		cfgWithPackages(t, `{"packages": ["zbar"]}`), "podman", "/repo", true); !ok {
		t.Fatal("planStorePackages refused an eligible opt-in")
	}
	assertNoticeOnStderr(t, "the `packages:` realization notice", stdout, stderr,
		"Realizing `packages:`")

	// (3) The image-extras notice (C5).
	extras, stdout, stderr := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
	extras.BuildImageExtras = func(string) (string, error) { return "/nix/store/extras", nil }
	if _, ok := extras.addImageExtras(storePackagesPlan{Active: true}, "/repo"); !ok {
		t.Fatal("addImageExtras refused")
	}
	assertNoticeOnStderr(t, "the image-extras notice", stdout, stderr, "bulk extras")
}

// assertNoticeOnStderr: the notice reached stderr, and stdout — the jailed command's
// own stream — stayed empty.
func assertNoticeOnStderr(t *testing.T, what string, stdout, stderr *bytes.Buffer, want string) {
	t.Helper()
	if stdout.Len() != 0 {
		t.Errorf("%s reached STDOUT, which belongs to the jailed command:\n%s", what, stdout)
	}
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("%s is missing from stderr (wanted %q):\n%s", what, want, stderr)
	}
}

// TestImageExtrasNixProgressGoesToStderr is the expression pin for the other writer in
// the same decision — realBuildImageExtras streams nix's progress to the writer
// buildImageExtras hands it, and driving that closure runs a real `nix build`. Same
// shape and same reason as TestPrefixNixProgressGoesToStderr.
func TestImageExtrasNixProgressGoesToStderr(t *testing.T) {
	body, err := os.ReadFile("storepackages.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "realBuildImageExtras(repoRoot, inJail, o.Stderr)") {
		t.Error("buildImageExtras no longer hands realBuildImageExtras o.Stderr; nix's " +
			"progress summaries would land on the jailed command's stdout")
	}
}
