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
//  1. THE HOST ASSERT. entrypoint.RenderHostPack at `assert`, over a packoverlay.Collect at the
//     host's own posture — the pair `yolo host apply --assert` runs — writes the host file and
//     the provenance mark.
//  2. THE LAUNCHER. Each backend's own label: hostFileArgs + hostLayerEnv for the container
//     backends, buildMacosCtxTree + macosuser.BuildRunPlan for macos-user.
//  3. THE BOOT. entrypoint.ConfigurePackSurfaces over the same packs, handed exactly the wire
//     step 2 produced and the bytes it staged.
//
// The fixture is the motivating case of docs/design/notch-scoped-config-contributions.md: a
// personal pack whose GUARDED posture adds pi-automode to pi's `packages`, over a host file
// that already holds one package of the user's own. The host gets the entry; the jail must get
// neither the entry nor — because a managed home's host file is a baseline, not a layer — the
// rest of that file. The twin is the same home never asserted into, whose file composes as the
// user's, which is what makes the first case about the label rather than about a missing file.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
	home := packHome(t)
	personal := filepath.Join(t.TempDir(), "matt")
	writeHostFileAt(t, filepath.Join(personal, "pack.json"), `{"name":"matt","contributes":[
	  {"kind":"autonomy","guarded":{"lists":[
	    {"surface":"pi/settings","path":"/packages","add":["`+e2eAutomode+`"]}]}}]}`, 0o644)
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

// e2eHostAssert is step 1: what `yolo host apply --assert` runs, at the host's own posture.
func e2eHostAssert(t *testing.T, home string, loaded []*packload.Pack) {
	t.Helper()
	target := render.Host(home, nil, render.OwnershipAssert)
	set := packoverlay.Collect(loaded, target.Profile().AgentAutonomy, nil)
	for _, p := range loaded {
		if _, err := entrypoint.RenderHostPack(p, home, render.OwnershipAssert, false, set); err != nil {
			t.Fatalf("RenderHostPack(%s): %v", p.Name, err)
		}
	}
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
	t.Setenv("YOLO_CTX_ROOT", ctxRoot)
	var errw bytes.Buffer
	e := &entrypoint.Env{Home: t.TempDir(), Workspace: t.TempDir(),
		Vars: map[string]string{packload.HostLayerEnvVar: wire}, Stderr: &errw, LogOnly: &errw}
	entrypoint.ConfigurePackSurfaces(e, loaded)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("the boot failed: %v\n%s", fails, errw.String())
	}
	return piPackages(t, e.Home), errw.String()
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
			[]string{"pi"}, []string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", "",
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

// THE MANAGED HOME. The host holds the user's entry and the guarded one; a jail on either
// backend holds neither, and its boot log says the host copy was a baseline. Delete the label
// from either launcher, or the label check from the boot's host-layer read, and the whole host
// file — automode included — composes into the jail as "the user's".
func TestAManagedHomesHostFileIsABaselineFromTheAssertToTheBoot(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			home, loaded := e2eHome(t)
			e2eHostAssert(t, home, loaded)
			if got := piPackages(t, home); !reflect.DeepEqual(got, []any{e2eUsersOwn, e2eAutomode}) {
				t.Fatalf("host packages after the assert = %#v, want the user's entry kept and the "+
					"guarded posture's appended", got)
			}

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
		})
	}
}

// THE TWIN: the same home, never asserted into. Nothing is labelled, so the user's own file
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
