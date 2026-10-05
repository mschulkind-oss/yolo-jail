package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE DISPATCH HALF of content delivery: run.Run must COMPOSE the overlay and hand
// it to the macos-user backend on every launch.
//
// The tree builder had three tests and this had none, so the composition could have
// been deleted from the arm with a green suite — the agent would silently go back to
// starting with no AGENTS.md and no skills, which is the state the feature ended.
// Same family as TestPacksAreStagedBeforeBackendDispatch, and written for the same
// reason: the arm returns above runContainer, so anything the container path does
// implicitly has to be re-pinned here explicitly.
func TestMacosUserLaunchComposesAndPassesTheHomeOverlay(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)

	var gotOverlay macosuser.HomeOverlay
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
		overlay macosuser.HomeOverlay, _ macosuser.HostContext,
		_ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		gotOverlay = overlay
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	if gotOverlay.Tree == "" {
		t.Fatalf("the macos-user arm was handed no overlay — the agent would start with "+
			"no AGENTS.md and no skills\nstderr:\n%s", stderr.String())
	}
	// It must be a real composed tree, not just a path: the claude pack declares a
	// skills destination, so the built-in suite has to be in it.
	if _, err := os.Stat(filepath.Join(gotOverlay.Tree, ".claude", "skills")); err != nil {
		t.Errorf("the overlay carries no skills tree at .claude/skills: %v", err)
	}
	// Laid out by DESTINATION, never by staging name — the layout IS the manifest the
	// bootstrap copies, so a staging-side name here would land in the home verbatim.
	var leaked []string
	_ = filepath.Walk(gotOverlay.Tree, func(p string, _ os.FileInfo, _ error) error {
		if strings.HasPrefix(filepath.Base(p), "briefing-") ||
			strings.HasPrefix(filepath.Base(p), "skills-") {
			leaked = append(leaked, p)
		}
		return nil
	})
	if len(leaked) > 0 {
		t.Errorf("staging-side names reached the overlay: %v", leaked)
	}
}

// --dry-run composes too. The plan's job is to describe the launch, so a dry-run
// that skipped composition would print a plan with no content staging in it — a
// launch nobody runs.
func TestMacosUserDryRunStillComposesTheOverlay(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true

	var gotOverlay macosuser.HomeOverlay
	var gotDryRun bool
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
		overlay macosuser.HomeOverlay, _ macosuser.HostContext,
		dryRun bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		gotOverlay, gotDryRun = overlay, dryRun
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	if !gotDryRun {
		t.Fatal("dry-run flag did not reach the backend")
	}
	if gotOverlay.Tree == "" {
		t.Errorf("--dry-run composed no overlay, so the plan it prints omits the content "+
			"staging a real launch performs\nstderr:\n%s", stderr.String())
	}
}

// THE INHERITED USER SCOPE (OQ-LP9) on this arm: the generated ~/.config/yolo-jail/config.jsonc
// a container mounts as a single `:ro` file rides in the overlay as a destination of its own, so
// the bootstrap installs it and the Seatbelt profile write-protects it. Before, the sandbox had no
// user scope at all: an in-sandbox `yolo pack ls` listed no packs and `yolo check` judged no user
// config. Fails if the arm stops handing the overlay builder the rendered files.
func TestMacosUserOverlayCarriesTheInheritedUserScope(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := floortest.ResolvedTemp(t)

	for _, dryRun := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.DryRun = dryRun
		var got macosUserCall
		o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
			got = c
			return 0
		})
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run(dryRun=%v) = %d\nstderr:\n%s", dryRun, rc, stderr.String())
		}
		if !slices.Contains(got.overlay.Dests, inheritPreflightRel) {
			t.Fatalf("dryRun=%v: the overlay's destinations %v do not name %s, so the sandbox "+
				"gets no user scope", dryRun, got.overlay.Dests, inheritPreflightRel)
		}
		body, err := os.ReadFile(filepath.Join(got.overlay.Tree, filepath.FromSlash(inheritPreflightRel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(body), inheritHeaderFirstLine()) || !strings.Contains(string(body), `"claude"`) {
			t.Errorf("dryRun=%v: the delivered user scope lacks the generated header or the user's "+
				"packs:\n%s", dryRun, body)
		}
		// R2: this backend cannot nest, so the nested-launch file is never delivered.
		if slices.Contains(got.overlay.Dests, inheritNestedRel) {
			t.Errorf("dryRun=%v: the overlay delivers %s on a backend that cannot nest", dryRun, inheritNestedRel)
		}
		// WRITE-PROTECTED: the profile's content rules name the file where the layout puts it.
		ro := macosuser.ResolveHomeReadonly(macosuser.SandboxHome(), ws, got.overlay.WorkspaceDirs, got.overlay.Dests)
		want := filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail", "config.jsonc")
		if !slices.Contains(ro.Paths, want) {
			t.Errorf("dryRun=%v: the Seatbelt content rules %v do not protect %s", dryRun, ro.Paths, want)
		}
	}
}

// A user config with nothing to inherit delivers no file — and a launch removes the one a previous
// launch installed in the sidecar, which the overlay install would otherwise leave serving a stale
// user scope. Only that file: one an agent wrote at the path, without the generated header, stays.
// A dry run removes nothing.
func TestMacosUserEmptyUserScopeRemovesOnlyTheGeneratedFile(t *testing.T) {
	packHome(t) // no user config at all: nothing to inherit
	ws := floortest.ResolvedTemp(t)
	stale := filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail", "config.jsonc")
	plant := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stale, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	launch := func(dryRun bool) macosUserCall {
		t.Helper()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.DryRun = dryRun
		var got macosUserCall
		o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
			got = c
			return 0
		})
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run(dryRun=%v) = %d\nstderr:\n%s", dryRun, rc, stderr.String())
		}
		return got
	}
	generated := config.InheritHeader(config.InheritPreflight, "2026-10-01T00:00:00Z") + `{"packs": ["claude"]}` + "\n"

	plant(generated)
	if got := launch(true); slices.Contains(got.overlay.Dests, inheritPreflightRel) {
		t.Fatalf("a user config with nothing to inherit delivered %s", inheritPreflightRel)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("a --dry-run removed %s (%v); a plan render removes nothing", stale, err)
	}
	launch(false)
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("the generated %s a previous launch installed survived a launch that renders none", stale)
	}

	agents := `{"packs": ["mine"]}` + "\n"
	plant(agents)
	launch(false)
	if b, err := os.ReadFile(stale); err != nil || string(b) != agents {
		t.Errorf("a launch removed or changed the agent's own %s (err %v): %q", stale, err, b)
	}
}
