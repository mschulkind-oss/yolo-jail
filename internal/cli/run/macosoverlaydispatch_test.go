package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
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
	// Layout is by destination, never by staging name. A recipe named
	// skills-and-briefing.md is valid content; only staging-directory components and
	// briefing staging artifacts are forbidden in the delivered tree.
	stagingSideName := func(name string, isDir bool) bool {
		return isDir && (strings.HasPrefix(name, "skills-") || strings.HasPrefix(name, "briefings-")) ||
			!isDir && (strings.HasPrefix(name, "briefing-") || strings.HasPrefix(name, "briefings-"))
	}
	if stagingSideName("skills-and-briefing.md", false) {
		t.Fatal("ordinary recipe filename classified as a staging name")
	}
	if !stagingSideName("skills-claude", true) || !stagingSideName("briefings-claude", true) ||
		!stagingSideName("briefings-claude", false) || !stagingSideName("briefing-.claude~1CLAUDE.md", false) {
		t.Fatal("staging-side fixture names were not recognized")
	}
	var leaked []string
	_ = filepath.Walk(gotOverlay.Tree, func(p string, info os.FileInfo, _ error) error {
		if info != nil && stagingSideName(filepath.Base(p), info.IsDir()) {
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

// THE INHERITED USER SCOPE (OQ-LP9) on this arm: the generated TOML config a container
// mounts as a single `:ro` file rides in the overlay as a destination of its own, so the
// bootstrap installs it and the Seatbelt profile write-protects it. Before, the sandbox had no
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
		if _, _, ok := entrypoint.HomeOverlayDigest(got.overlay.Tree); !ok {
			t.Errorf("dryRun=%v: the inherited user scope did not participate in a valid content-keyed overlay digest", dryRun)
		}
		body, err := os.ReadFile(filepath.Join(got.overlay.Tree, filepath.FromSlash(inheritPreflightRel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(body), inheritTOMLHeaderFirstLine()) || !strings.Contains(string(body), `"claude"`) {
			t.Errorf("dryRun=%v: the delivered user scope lacks the generated header or the user's "+
				"packs:\n%s", dryRun, body)
		}
		// R2: this backend cannot nest, so the nested-launch file is never delivered.
		if slices.Contains(got.overlay.Dests, inheritNestedRel) {
			t.Errorf("dryRun=%v: the overlay delivers %s on a backend that cannot nest", dryRun, inheritNestedRel)
		}
		// WRITE-PROTECTED: the profile's content rules name the file where the layout puts it.
		ro := macosuser.ResolveHomeReadonly(got.overlay.Sidecar, got.overlay.Sidecar, got.overlay.WorkspaceDirs, got.overlay.Dests)
		want := filepath.Join(got.overlay.Sidecar, ".config", "yolo-jail", "config.toml")
		if !slices.Contains(ro.Paths, want) {
			t.Errorf("dryRun=%v: the Seatbelt content rules %v do not protect %s", dryRun, ro.Paths, want)
		}
	}
}

func TestMacosUserRunRefusesExistingDestinationsBeforeOverlayInstall(t *testing.T) {
	for _, tc := range []struct {
		name      string
		legacy    []byte
		toml      []byte
		link      bool
		wantPaths []string
	}{
		{name: "legacy owner", legacy: []byte("// operator comment\n{\"packs\": [\"copilot\"]}\n"), wantPaths: []string{"config.jsonc"}},
		{name: "toml owner", toml: []byte("# operator comment\npacks = [\"copilot\"]\n"), wantPaths: []string{"config.toml"}},
		{name: "mixed owners", legacy: []byte("{\"packs\": [\"copilot\"]}\n"), toml: []byte("packs = [\"codex\"]\n"), wantPaths: []string{"config.jsonc", "config.toml"}},
		{name: "linked legacy owner", link: true, wantPaths: []string{"config.jsonc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `["claude"]`)
			ws := floortest.ResolvedTemp(t)
			configDir := filepath.Join(runtimeHome(ws, "macos-user"), ".config", "yolo-jail")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(configDir, "config.jsonc")
			toml := filepath.Join(configDir, "config.toml")
			ownerFiles := map[string][]byte{}
			if len(tc.legacy) > 0 {
				ownerFiles[legacy] = tc.legacy
				if err := os.WriteFile(legacy, tc.legacy, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if len(tc.toml) > 0 {
				ownerFiles[toml] = tc.toml
				if err := os.WriteFile(toml, tc.toml, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var linkTarget string
			if tc.link {
				linkTarget = filepath.Join(t.TempDir(), "owner.jsonc")
				targetBytes := []byte("// link target\n{\"packs\": [\"codex\"]}\n")
				if err := os.WriteFile(linkTarget, targetBytes, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(linkTarget, legacy); err != nil {
					t.Fatal(err)
				}
				ownerFiles[linkTarget] = targetBytes
			}

			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			called := false
			o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int {
				called = true
				return 0
			})
			if rc := Run(*o); rc == 0 || called {
				t.Fatalf("existing owner destination was not refused before overlay install/dispatch: rc=%d called=%v", rc, called)
			}
			for _, name := range tc.wantPaths {
				if !strings.Contains(stderr.String(), filepath.Join(configDir, name)) {
					t.Errorf("refusal did not name %s:\n%s", name, stderr.String())
				}
			}
			if !strings.Contains(stderr.String(), "preserv") {
				t.Errorf("refusal did not give a safe preservation step:\n%s", stderr.String())
			}
			for path, want := range ownerFiles {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Errorf("refusal changed owner bytes at %s (err %v): %q", path, err, got)
				}
			}
			for path, wantMode := range map[string]os.FileMode{legacy: 0o640, toml: 0o600} {
				if _, exists := ownerFiles[path]; !exists {
					continue
				}
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != wantMode {
					t.Errorf("refusal changed mode at %s (info=%v err=%v), want %04o", path, info, err, wantMode)
				}
			}
			if tc.link {
				link, err := os.Readlink(legacy)
				if err != nil || link != linkTarget {
					t.Errorf("refusal changed link %s (got %q err %v), want %q", legacy, link, err, linkTarget)
				}
			}
		})
	}
}

func TestMacosUserEmptyUserScopeRemovesOnlyTheGeneratedFile(t *testing.T) {
	packHome(t) // no user config at all: nothing to inherit
	ws := floortest.ResolvedTemp(t)
	stale := map[string]string{
		inheritPreflightRel:       config.InheritTOMLHeader(config.InheritPreflight, "2026-10-01T00:00:00Z") + `packs = ["claude"]` + "\n",
		inheritPreflightLegacyRel: config.InheritHeader(config.InheritPreflight, "2026-10-01T00:00:00Z") + `{"packs": ["claude"]}` + "\n",
	}
	plant := func(rel, body string) string {
		t.Helper()
		path := filepath.Join(runtimeHome(ws, "macos-user"), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
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
	for rel, body := range stale {
		plant(rel, body)
	}
	for _, rel := range []string{inheritPreflightRel, inheritPreflightLegacyRel} {
		if got := launch(true); slices.Contains(got.overlay.Dests, rel) {
			t.Fatalf("a user config with nothing to inherit delivered %s", rel)
		}
		if _, err := os.Stat(filepath.Join(runtimeHome(ws, "macos-user"), filepath.FromSlash(rel))); err != nil {
			t.Fatalf("a --dry-run removed %s (%v); a plan render removes nothing", rel, err)
		}
	}
	launch(false)
	for rel := range stale {
		if _, err := os.Stat(filepath.Join(runtimeHome(ws, "macos-user"), filepath.FromSlash(rel))); err == nil {
			t.Errorf("the generated %s a previous launch installed survived a launch that renders none", rel)
		}
	}

	agents := `{"packs": ["mine"]}` + "\n"
	for _, rel := range []string{inheritPreflightRel, inheritPreflightLegacyRel} {
		path := plant(rel, agents)
		launch(false)
		if b, err := os.ReadFile(path); err != nil || string(b) != agents {
			t.Errorf("a launch removed or changed the agent's own %s (err %v): %q", rel, err, b)
		}
	}
}

// THE RENDER'S WARNINGS reach this arm too (inheritedScope): a key the inherit census does not
// classify is dropped from the sandbox's user scope and named on the launch's output, as the
// container path names it. Called on the arm's helper rather than through Run, because validation
// refuses an unknown top-level key before any launch gets this far; the arm's call of the helper is
// TestMacosUserOverlayCarriesTheInheritedUserScope's. Fails if macosUserInheritedScope renders the
// files without going through inheritedScope, which is where both warnings live.
func TestMacosUserArmWarnsAboutUnclassifiedInheritKeys(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const key = "zz_unclassified_key"
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"),
		[]byte(`{"packs": ["claude"], "`+key+`": 1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := floortest.ResolvedTemp(t)
	if err := os.MkdirAll(paths.LegacyHomeState(ws), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	core, err := o.macosUserInheritedScope("macos-user")
	if err != nil {
		t.Fatalf("rendering the macos-user inherited scope: %v", err)
	}

	if !strings.Contains(stderr.String(), "no inherit classification") || !strings.Contains(stderr.String(), key) {
		t.Errorf("the macos-user arm dropped %s from the sandbox's user scope without saying so:\n"+
			"stdout:\n%s\nstderr:\n%s", key, stdout.String(), stderr.String())
	}
	// The rest of the user scope is still delivered, without the dropped key.
	var body string
	for _, f := range core {
		if f.Rel == inheritPreflightRel {
			body = string(f.Body)
		}
	}
	if !strings.Contains(body, `"claude"`) || strings.Contains(body, key) ||
		!strings.HasPrefix(body, inheritTOMLHeaderFirstLine()) {
		t.Errorf("the delivered user scope should keep the packs, drop %s, and use the TOML header:\n%s", key, body)
	}
}
