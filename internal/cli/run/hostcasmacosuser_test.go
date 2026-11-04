package run

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// hostcasmacosuser_test.go pins the macos-user binding of package-cache aliasing
// (hostcasalias.go's planMacosUserHostCAS; docs/design/macos-user-model-plan.md slice 8,
// MMP-D12): the mode is the input, the account mode production runs declines every store, and
// the run-as-you mode aliases through the sandbox HOME.

// macosHostCASOptions is a macos-user planner's Options on a host that claims to be this
// machine's Mac, with HOME at a fresh temp home holding a real pip cache.
func macosHostCASOptions(t *testing.T) (*Options, string, string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	pip := filepath.Join(home, "Library", "Caches", "pip")
	if err := os.MkdirAll(pip, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pip, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{
		Workspace:            floortest.ResolvedTemp(t),
		IsMacOS:              true,
		Getenv:               func(k string) string { return map[string]string{"HOME": home}[k] },
		HostCASProbe:         hostcas.DefaultProbe,
		macosHostCASPlatform: hostcas.DarwinJailPlatform(),
	}
	return o, home, pip
}

func dispositionNamed(t *testing.T, ds []hostcas.Disposition, name string) hostcas.Disposition {
	t.Helper()
	for _, d := range ds {
		if d.Store.Name == name {
			return d
		}
	}
	t.Fatalf("no disposition for %s", name)
	return hostcas.Disposition{}
}

// PRODUCTION'S MODE DECLINES: the account mode — the zero value, and every launch today —
// aliases nothing, even with a real cache present and every other fact a self-mode launch has.
func TestMacosUserAccountModeAliasesNoHostCache(t *testing.T) {
	o, _, _ := macosHostCASOptions(t)
	ds := o.planMacosUserHostCAS(macosuser.ModeAccount, nil)
	if len(ds) != len(hostcas.Stores) {
		t.Fatalf("%d dispositions, want one per store (%d)", len(ds), len(hostcas.Stores))
	}
	for _, d := range ds {
		if d.Aliased || d.Code != hostcas.CodeBackend {
			t.Errorf("%s in account mode: aliased=%v code=%s (%s)", d.Store.Name, d.Aliased, d.Code, d.Reason)
		}
	}
	if got := macosUserCacheAliases(ds); len(got) != 0 {
		t.Errorf("account mode handed the backend aliases %+v", got)
	}
}

// THE RUN-AS-YOU MODE aliases the host cache where the sandbox's own copy physically sits:
// ~/Library/Caches is a link into the machine root (MMP-D6), so the link goes there, and the
// backend is handed it and a resolved target.
func TestMacosUserSelfModeAliasesThroughTheSandboxHome(t *testing.T) {
	o, home, pip := macosHostCASOptions(t)
	ds := resolveMacosUserHostCAS(o.planMacosUserHostCAS(macosuser.ModeSelf, nil), o.macosUserHostCASFacts(macosuser.ModeSelf, nil))
	d := dispositionNamed(t, ds, "pip-cache")
	if !d.Aliased {
		t.Fatalf("pip-cache not aliased in self mode: %s (%s)", d.Code, d.Reason)
	}
	link := filepath.Join(macosuser.LaunchHomes(macosuser.ModeSelf, o.Workspace, home).Machine, "Library", "Caches", "pip")
	if d.Source != pip || d.Dest != link {
		t.Errorf("source %q dest %q, want %q and %q", d.Source, d.Dest, pip, link)
	}
	got := macosUserCacheAliases(ds)
	want := []macosuser.CacheAlias{{Tool: "pip", Link: link, Target: pip}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("aliases = %+v, want %+v", got, want)
	}
}

// The host's platform is a fact the gate reads, not one the binding assumes: a Linux host (the
// real platform of this package's CI) declines the darwin sandbox.
func TestMacosUserSelfModeDeclinesAnotherPlatform(t *testing.T) {
	o, _, _ := macosHostCASOptions(t)
	o.macosHostCASPlatform = "linux/" + goruntime.GOARCH
	if d := dispositionNamed(t, o.planMacosUserHostCAS(macosuser.ModeSelf, nil), "pip-cache"); d.Aliased || d.Code != hostcas.CodePlatform {
		t.Errorf("code %q aliased=%v, want %q", d.Code, d.Aliased, hostcas.CodePlatform)
	}
}

// THE CALL-SITE PIN for the relocation gate on this backend: a relocated ~/.cache segment
// reaches hostcas, so the alias does not put those bytes back under the home.
func TestMacosUserHostCASPassesTheRelocationsThrough(t *testing.T) {
	o, home, _ := macosHostCASOptions(t)
	if err := os.MkdirAll(filepath.Join(home, ".cache", "uv", "wheels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d := dispositionNamed(t, o.planMacosUserHostCAS(macosuser.ModeSelf, nil), "uv-cache"); !d.Aliased {
		t.Fatalf("baseline uv-cache not aliased: %s (%s)", d.Code, d.Reason)
	}
	ds := o.planMacosUserHostCAS(macosuser.ModeSelf, []macosuser.CacheRelocation{{Subdir: "uv", Target: "/Volumes/big/uv"}})
	if d := dispositionNamed(t, ds, "uv-cache"); d.Aliased || d.Code != hostcas.CodeRelocated {
		t.Errorf("relocated uv: code %q aliased=%v, want %q", d.Code, d.Aliased, hostcas.CodeRelocated)
	}
}

// A PLANTED LINK IS JUDGED BY WHERE IT LEADS: a cache directory replaced by a link to the home
// passes the source rule as spelled, and the profile would open its resolved target, the
// whole home, read-write. The resolution re-runs the rule and declines it. Fails if
// resolveMacosUserHostCAS stops re-checking the resolved target.
func TestAMacosUserAliasResolvingToTheHomeIsDeclined(t *testing.T) {
	o, home, pip := macosHostCASOptions(t)
	if err := os.WriteFile(filepath.Join(home, "notes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "token"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{home, cfg} {
		if err := os.RemoveAll(pip); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, pip); err != nil {
			t.Fatal(err)
		}
		ds := resolveMacosUserHostCAS(o.planMacosUserHostCAS(macosuser.ModeSelf, nil),
			o.macosUserHostCASFacts(macosuser.ModeSelf, nil))
		d := dispositionNamed(t, ds, "pip-cache")
		if d.Aliased || d.Code != hostcas.CodeUnsafeSource || !strings.Contains(d.Reason, "resolves to "+target) {
			t.Errorf("pip cache linked to %s: aliased=%v code=%s (%s)", target, d.Aliased, d.Code, d.Reason)
		}
		for _, a := range macosUserCacheAliases(ds) {
			if a.Tool == "pip" {
				t.Errorf("handed the backend %+v", a)
			}
		}
	}
}

// A source that vanishes between the decision and the resolution is downgraded, not handed to
// the backend unresolved: the profile names a target verbatim.
func TestAnUnresolvableMacosUserAliasIsDowngraded(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	ds := resolveMacosUserHostCAS([]hostcas.Disposition{{
		Store: hostcas.Stores[5], Aliased: true, Source: gone, Dest: "/ws/.yolo/home/Library/Caches/pip",
		Code: hostcas.CodeAliased,
	}}, hostcas.Facts{})
	if ds[0].Aliased || ds[0].Code != hostcas.CodeAbsent || ds[0].Reason == "" {
		t.Errorf("unresolvable source: %+v", ds[0])
	}
	if got := macosUserCacheAliases(ds); len(got) != 0 {
		t.Errorf("handed %+v", got)
	}
}

// The sandbox's mise data dir and the mise row's macOS destination are one path. In the
// run-as-you mode MISE_DATA_DIR is the MACHINE ROOT's (MMP-D6), outside HOME, so the row is
// machine tier and lands there — the plan the launch makes says so, not just the table.
func TestTheMiseRowFollowsTheSandboxMiseDataDir(t *testing.T) {
	homes := macosuser.LaunchHomes(macosuser.ModeSelf, "/Users/you/code/alpha", "/Users/you")
	s := hostcasStoreNamed(t, "mise-downloads")
	if !s.DarwinMachineTier() {
		t.Fatal("mise-downloads is not machine tier")
	}
	want := filepath.Join(macosuser.SandboxMiseData(homes.Machine), "downloads")
	if got := filepath.Join(homes.Machine, s.DarwinRel); got != want {
		t.Errorf("mise-downloads lands at %q, the sandbox's mise downloads are %q", got, want)
	}
	o := &Options{Workspace: "/Users/you/code/alpha", MacosUserHostHome: "/Users/you",
		Getenv: func(string) string { return "" }}
	for _, d := range o.planMacosUserHostCAS(macosuser.ModeSelf, nil) {
		if d.Store.Name == "mise-downloads" && d.Dest != want {
			t.Errorf("the launch's plan puts mise-downloads at %q, want %q", d.Dest, want)
		}
	}
}

func hostcasStoreNamed(t *testing.T, name string) hostcas.Store {
	t.Helper()
	for _, s := range hostcas.Stores {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no store %s", name)
	return hostcas.Store{}
}

// THE ARM'S CALL SITE: production's default mode hands the backend no alias; the run-as-you
// mode, on a host that is this machine's Mac, hands it the host's cache and discloses it.
// Run(), for macosctxtree_test.go's reason: a unit test of the planner passes with the arm's
// call deleted.
func TestTheMacosUserArmHandsCacheAliasesOnlyInSelfMode(t *testing.T) {
	ctxLaunchHome(t, "")
	seamHome := floortest.ResolvedTemp(t)
	pip := filepath.Join(seamHome, "Library", "Caches", "pip")
	if err := os.MkdirAll(pip, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pip, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	withHome := func(o *Options) {
		prev := o.Getenv
		o.Getenv = func(k string) string {
			if k == "HOME" {
				return seamHome
			}
			return prev(k)
		}
		o.HostCASProbe = hostcas.DefaultProbe
		o.macosHostCASPlatform = hostcas.DarwinJailPlatform()
	}

	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), withHome)
	if len(ctx.CacheAliases) != 0 {
		t.Errorf("the default (account) mode handed the backend %+v\n%s", ctx.CacheAliases, out)
	}
	if strings.Contains(out, "aliased (writable)") {
		t.Errorf("the default mode disclosed an alias:\n%s", out)
	}

	ws := floortest.ResolvedTemp(t)
	ctx, out = runMacosUserCapturingCtx(t, ws, func(o *Options) {
		withHome(o)
		selectMacosUserMode(o, macosuser.ModeSelf)
	})
	link := filepath.Join(macosuser.LaunchHomes(macosuser.ModeSelf, ws, seamHome).Machine, "Library", "Caches", "pip")
	found := false
	for _, a := range ctx.CacheAliases {
		if a.Tool == "pip" && a.Target == pip && a.Link == link {
			found = true
		}
	}
	if !found {
		t.Errorf("self mode handed the backend %+v, want pip %s -> %s\n%s", ctx.CacheAliases, link, pip, out)
	}
	if !strings.Contains(out, "aliased (writable)") || !strings.Contains(out, pip) {
		t.Errorf("self mode did not disclose the alias:\n%s", out)
	}
}
