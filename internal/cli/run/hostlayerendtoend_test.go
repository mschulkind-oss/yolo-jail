package run

// hostlayerendtoend_test.go closes GAP 2 of docs/reference/config-target-resolution.md
// ("Where this does not reach"): the fifth host-layer disposition driven END TO END through
// one managed home, rather than each half pinned alone.
//
// The two halves already had unit tests — the launcher's label
// (TestHostLayerReportLabelsOnlyWhatYoloHasRendered) and the boot's reading of it
// (entrypoint.TestBootDoesNotComposeAHostLayerLabelledARender) — and each passes with the
// other broken, because each writes the other's half by hand: the first never boots, and the
// second hand-writes the YOLO_HOST_LAYERS value. Here nothing is hand-written between the
// three production steps:
//
//  1. THE HOST WRITE. entrypoint.RenderHostPack, over a packoverlay.Collect at the host's own
//     posture — the pair `yolo host apply --assert` runs — writes the host file and the
//     provenance mark. Two ways, since the `assert` retirement (OQ-CO14): under `own`, the
//     contract that writes today; and as the retired `assert` wrote it (every surface through
//     the rmw arm, which an owned host still runs for a surface declaring `rmw`), with the key
//     then left unset — OQ-CO14's face 2, a home yolo asserted into that now reads as `none`.
//  2. THE LAUNCHER. Each backend's own label: hostFileArgs + hostLayerEnv for the container
//     backends, buildMacosCtxTree + macosuser.BuildRunPlan for macos-user.
//  3. THE BOOT. entrypoint.ConfigurePackSurfaces over the same packs, handed exactly the wire
//     step 2 produced and the bytes it staged.
//
// The fixture is the motivating case of docs/design/notch-scoped-config-contributions.md: a
// personal pack whose GUARDED posture adds pi-automode to pi's `packages`, over a host file
// that already holds one package of the user's own. The host gets the entry; the jail must get
// neither the entry nor — because a managed home's host file is a baseline, not a layer — the
// rest of that file. The twin is the same home never written into, whose file composes as the
// user's, which is what makes the first cases about the label rather than about a missing file.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	e2eAutomode = "npm:@czottmann/pi-automode@1.17.0"
	e2eUsersOwn = "npm:the-users-own-extension"
)

// e2eHome is a host home selecting the shipped pi pack and the personal pack, whose
// ~/.pi/agent/settings.json already holds the user's own package. It returns the home and
// the packs the launch stages — one set, used by all three steps.
func e2eHome(t *testing.T) (string, []*packload.Pack) {
	t.Helper()
	return e2eHomeWith(t, `{"kind":"autonomy","guarded":{"lists":[
	    {"surface":"pi/settings","path":"/packages","add":["`+e2eAutomode+`"]}]}}`)
}

// e2eHomeWith is e2eHome with the personal pack's contributions spelled by the caller.
func e2eHomeWith(t *testing.T, contributes string) (string, []*packload.Pack) {
	t.Helper()
	home := packHome(t)
	personal := filepath.Join(t.TempDir(), "matt")
	writeHostFileAt(t, filepath.Join(personal, "pack.json"),
		`{"name":"matt","contributes":[`+contributes+`]}`, 0o644)
	writeUserPacks(t, home, `["pi", "file://`+personal+`"]`)
	writeHostFileAt(t, filepath.Join(home, ".pi", "agent", "settings.json"),
		`{"packages":["`+e2eUsersOwn+`"]}`, 0o644)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-hostlayer-e2e")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	return home, loaded
}

// e2eHostOwn is step 1 under `own`: what `yolo host apply --assert` runs at the host's own
// posture with `host_management: "own"` declared.
func e2eHostOwn(t *testing.T, home string, loaded []*packload.Pack) {
	t.Helper()
	e2eHostRender(t, home, loaded)
}

// e2eHostAsRetiredAssert is step 1 as the retired `assert` ran it, before OQ-CO14: every config
// surface through the rmw arm, which recorded. The arm is the one an owned host still runs for a
// surface its pack declares `rmw`, and nothing in the render branches on the contract but the
// mode census, so re-declaring each surface `rmw` and rendering under OwnershipOwn leaves the same
// file and the same provenance mark an `assert` apply left.
func e2eHostAsRetiredAssert(t *testing.T, home string, loaded []*packload.Pack) {
	t.Helper()
	asserted := make([]*packload.Pack, len(loaded))
	for i, p := range loaded {
		asserted[i] = rmwDeclaredPack(t, p)
	}
	e2eHostRender(t, home, asserted)
}

func e2eHostRender(t *testing.T, home string, loaded []*packload.Pack) {
	t.Helper()
	target := render.Host(home, nil, render.OwnershipOwn)
	set := packoverlay.Collect(loaded, target.Profile().AgentAutonomy, nil)
	for _, p := range loaded {
		if _, err := entrypoint.RenderHostPack(p, home, render.OwnershipOwn, false, set, nil); err != nil {
			t.Fatalf("RenderHostPack(%s): %v", p.Name, err)
		}
	}
}

// rmwDeclaredPack is a copy of p whose every config surface declares `rmw` (an `unrendered` one
// keeps its declaration, which `assert` honored by writing nothing).
func rmwDeclaredPack(t *testing.T, p *packload.Pack) *packload.Pack {
	t.Helper()
	if p.Decl == nil {
		return p
	}
	decl := *p.Decl
	decl.Contributes = append([]packdecl.Contribution(nil), p.Decl.Contributes...)
	for i, c := range decl.Contributes {
		if c.Kind != packdecl.KindConfig || len(c.Raw) == 0 {
			continue
		}
		var surfaces []map[string]any
		single := false
		if err := json.Unmarshal(c.Raw, &surfaces); err != nil {
			var one map[string]any
			if err := json.Unmarshal(c.Raw, &one); err != nil {
				t.Fatalf("pack %s: a config contribution is not a surface: %v", p.Name, err)
			}
			surfaces, single = []map[string]any{one}, true
		}
		for _, sf := range surfaces {
			if mode, _ := sf["mode"].(string); mode != "unrendered" {
				sf["mode"] = "rmw"
			}
		}
		var raw []byte
		var err error
		if single {
			raw, err = json.Marshal(surfaces[0])
		} else {
			raw, err = json.Marshal(surfaces)
		}
		if err != nil {
			t.Fatal(err)
		}
		decl.Contributes[i].Raw = raw
	}
	cp := *p
	cp.Decl = &decl
	return &cp
}

// piPackages reads `packages` out of a home's pi settings file.
func piPackages(t *testing.T, home string) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("read pi settings under %s: %v", home, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("pi settings is not JSON: %v\n%s", err, data)
	}
	got, _ := m["packages"].([]any)
	return got
}

// e2eBoot is step 3: a jail boot handed the wire and the staged bytes a launcher produced.
func e2eBoot(t *testing.T, loaded []*packload.Pack, ctxRoot, wire string) (packages []any, log string) {
	t.Helper()
	jailHome, log := e2eBootHome(t, loaded, ctxRoot, wire)
	return piPackages(t, jailHome), log
}

// e2eBootHome is e2eBoot returning the jail home it rendered into, for a caller that reads
// more of the file than `packages`.
func e2eBootHome(t *testing.T, loaded []*packload.Pack, ctxRoot, wire string) (home, log string) {
	t.Helper()
	t.Setenv("YOLO_CTX_ROOT", ctxRoot)
	var errw bytes.Buffer
	e := &entrypoint.Env{Home: t.TempDir(), Workspace: t.TempDir(),
		Vars: map[string]string{packload.HostLayerEnvVar: wire}, Stderr: &errw, LogOnly: &errw}
	entrypoint.ConfigurePackSurfaces(e, loaded)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("the boot failed: %v\n%s", fails, errw.String())
	}
	return e.Home, errw.String()
}

// launcher is step 2 for one backend: the wire it emits and the root its staged bytes sit at.
type launcher struct {
	name   string
	launch func(t *testing.T, loaded []*packload.Pack) (wire, ctxRoot string)
}

var e2eLaunchers = []launcher{
	{"podman", func(t *testing.T, loaded []*packload.Pack) (string, string) {
		o := &Options{Workspace: t.TempDir()}
		in := &assembleInput{
			wsState:      filepath.Join(t.TempDir(), "home"),
			mountTargets: map[string]struct{}{},
			packs:        loaded,
		}
		o.hostFileArgs(in)
		var wire string
		for _, a := range o.hostLayerEnv(in) {
			if v, ok := strings.CutPrefix(a, packload.HostLayerEnvVar+"="); ok {
				wire = v
			}
		}
		// The bind the argv asks for, made by hand: the container runtime is the one thing a
		// unit test cannot run. Same source, same destination — the grant's own two paths.
		ctxRoot := t.TempDir()
		for _, p := range loaded {
			granted, _ := p.HonoredHostFiles()
			for _, hf := range granted {
				dest := packload.CtxPath(p.StagedSlug(), hf)
				if !containsString(in.hostLayersDelivered, dest) {
					continue
				}
				body, err := os.ReadFile(filepath.Join(homeDir(), filepath.FromSlash(hf.From)))
				if err != nil {
					t.Fatal(err)
				}
				writeHostFileAt(t, filepath.Join(ctxRoot,
					strings.TrimPrefix(dest, packload.CtxRoot+"/")), string(body), 0o644)
			}
		}
		return wire, ctxRoot
	}},
	{"macos-user", func(t *testing.T, loaded []*packload.Pack) (string, string) {
		o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
		delivery, err := o.buildMacosCtxTree(t.TempDir(), loaded, jsonx.NewOrderedMap())
		if err != nil {
			t.Fatalf("buildMacosCtxTree: %v", err)
		}
		plan := macosuser.BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
			[]string{"pi"}, []string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", macosuser.HomeOverlay{},
			delivery.ctx, jsonx.NewOrderedMap(), nil, nil)
		var wire string
		for _, a := range plan.BootstrapArgv {
			if v, ok := strings.CutPrefix(a, packload.HostLayerEnvVar+"="); ok {
				wire = v
			}
		}
		// The bootstrap reads $YOLO_CTX_ROOT, the root-owned copy of exactly this tree.
		return wire, delivery.ctx.Tree
	}},
}

// THE MANAGED HOME, under `own`. The host holds the user's entry and the guarded one; a jail on
// either backend holds neither, and its boot log says the host copy was a baseline. Delete the
// label from either launcher, or the label check from the boot's host-layer read, and the whole
// host file — automode included — composes into the jail as "the user's".
//
// It was TestAManagedHomesHostFileIsABaselineFromTheAssertToTheBoot, rendering under `assert`;
// that home is now its twin below, and this is the contract that writes today. `own` composes the
// file whole, so its order is the composition's rather than rmw's append: the test reads the two
// entries as a set.
func TestAnOwnedHomesHostFileIsABaselineFromTheApplyToTheBoot(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			home, loaded := e2eHome(t)
			e2eHostOwn(t, home, loaded)
			if got := piPackages(t, home); len(got) != 2 || !containsAny(got, e2eUsersOwn) ||
				!containsAny(got, e2eAutomode) {
				t.Fatalf("host packages after the owned apply = %#v, want the user's entry kept "+
					"and the guarded posture's added", got)
			}
			e2eAssertBaseline(t, l, loaded)
		})
	}
}

// THE HOME `assert` WROTE, LEFT UNDER THE NEW DEFAULT (OQ-CO14 face 2). A home yolo asserted into
// before the retirement, whose key was never written: the unset default is `none` now, with no
// prompt and no notice, and the file stays exactly as `assert` last rendered it. The provenance
// mark that render left STAYS (read from the ruling), so a jail keeps treating the keys yolo wrote
// as yolo's — the launcher labels the file a render and the boot keeps it as a baseline — rather
// than composing them as the user's own layer: the laundering the `retired:` label exists to stop.
// Clear the mark, or key the label on the posture (which says `none` here), and the guarded entry
// `assert` wrote reaches the jail as the user's.
func TestAHomeAssertedIntoBeforeTheRetirementStaysABaselineToTheBoot(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			home, loaded := e2eHome(t)
			e2eHostAsRetiredAssert(t, home, loaded)
			if got := piPackages(t, home); !reflect.DeepEqual(got, []any{e2eUsersOwn, e2eAutomode}) {
				t.Fatalf("host packages after the assert = %#v, want the user's entry kept and the "+
					"guarded posture's appended", got)
			}
			if mode, declared := config.HostManagementDeclared(); mode != config.HostManagementNone || declared {
				t.Fatalf("the fixture's key is not unset-and-none: (%q, %v)", mode, declared)
			}
			e2eAssertBaseline(t, l, loaded)
			// And nothing rewrote the file on the way: the launch read it, it did not render it.
			if got := piPackages(t, home); !reflect.DeepEqual(got, []any{e2eUsersOwn, e2eAutomode}) {
				t.Errorf("the host file moved under `none`: %#v", got)
			}
		})
	}
}

// e2eAssertBaseline is steps 2 and 3 over a home yolo has written: the launcher labels the file,
// and the boot composes neither entry and says it kept the copy as a baseline.
func e2eAssertBaseline(t *testing.T, l launcher, loaded []*packload.Pack) {
	t.Helper()
	wire, ctxRoot := l.launch(t, loaded)
	if !strings.Contains(wire, `"rendered":[`) {
		t.Fatalf("the %s launcher did not label the managed home's host file:\n%s", l.name, wire)
	}
	pkgs, log := e2eBoot(t, loaded, ctxRoot, wire)
	for _, leaked := range []string{e2eAutomode, e2eUsersOwn} {
		if containsAny(pkgs, leaked) {
			t.Errorf("the jail's pi packages = %#v carry %q — the managed home's host file "+
				"was composed as the user's layer instead of kept as a baseline", pkgs, leaked)
		}
	}
	if !strings.Contains(log, "baseline and not a layer") {
		t.Errorf("the boot did not record that it kept the host copy as a baseline:\n%s", log)
	}
}

// THE TWIN: the same home, never written into. Nothing is labelled, so the user's own file
// composes — the onboarding path the host layer exists for (OQ-CR8) — and the guarded entry is
// absent because nothing ever wrote it and the jail's own posture does not select it.
func TestAnUnmanagedHomesHostFileComposesFromTheLaunchToTheBoot(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			_, loaded := e2eHome(t)
			wire, ctxRoot := l.launch(t, loaded)
			if strings.Contains(wire, `"rendered"`) {
				t.Fatalf("the %s launcher labelled a home yolo never rendered into:\n%s", l.name, wire)
			}
			pkgs, _ := e2eBoot(t, loaded, ctxRoot, wire)
			if !containsAny(pkgs, e2eUsersOwn) {
				t.Errorf("the jail's pi packages = %#v lack the user's own entry — an unmanaged "+
					"home's host file is the user's and must compose", pkgs)
			}
			if containsAny(pkgs, e2eAutomode) {
				t.Errorf("the jail's pi packages = %#v carry the guarded posture's entry", pkgs)
			}
		})
	}
}

func containsAny(list []any, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
