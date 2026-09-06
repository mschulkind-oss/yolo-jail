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
// (docs/design/image-staging-vs-baking.md §4 C4/C5, R2).

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
	o, out, _ := storeOptions(t, map[string]string{StorePackagesOptInEnv: "1"})
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
	if !strings.Contains(out.String(), StorePackagesOptInEnv) {
		t.Errorf("the fallback was SILENT; output was:\n%s", out)
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
