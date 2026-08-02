package run

// packconfigexclusive_test.go is the LAUNCH half of Option 1
// (docs/design/pack-config-collaboration.md): two `config` declarations of one surface
// identity fail the launch, before the container exists, naming both packs.
//
// Why it belongs at this call site rather than only in packload: this is the one collision in
// the cluster that NO runtime error would ever announce. A duplicate `files` destination at
// least ends in podman's "duplicate mount destination" (naming the wrong thing); two `config`
// declarations resolve in Go — manifest.Merge, last-writer-wins, whole — and the jail comes up
// looking fine, having flipped one pack's surface `mode` and silently dropped its capture
// sidecars. Ruling R1: "very harmful … this is a general mechanism."

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configSurfacePackDir writes a local pack declaring one config surface and returns its root.
// mode is omitted from the manifest when empty, so the surface inherits stateful — exactly as
// a real pack.json does.
func configSurfacePackDir(t *testing.T, name, agent, surface, mode, managed string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	modeField := ""
	if mode != "" {
		modeField = `"mode":"` + mode + `",`
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"config","config":[{"agent":"` + agent + `","name":"` + surface + `",` +
		`"codec":"json","path":"~/.` + agent + `/` + surface + `.json",` + modeField +
		`"managed":` + managed + `}]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestStagePacksRefusesConfigSurfaceCollision: the pre-flight at its real call site. The
// second pack declares mode:"rmw" over a stateful surface — the concrete R1 damage — and the
// launch must refuse rather than come up with capture silently disabled.
func TestStagePacksRefusesConfigSurfaceCollision(t *testing.T) {
	home := packHome(t)
	owner := configSurfacePackDir(t, "owner", "acme", "settings", "", `{"preferences":{"x":1}}`)
	second := configSurfacePackDir(t, "grabby", "acme", "settings", "rmw", `{"fileSuggestion":"x"}`)
	writeUserPacks(t, home, `["file://`+owner+`", "file://`+second+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-config-collide")
	if err == nil {
		t.Fatal("two packs declaring one config surface identity must fail the launch — " +
			"manifest.Merge would otherwise silently resolve it last-writer-wins, flipping the " +
			"owner's surface mode with nothing reported (R1)")
	}
	msg := err.Error()
	for _, want := range []string{
		"owner", "grabby", // both packs
		"acme/settings",  // the identity
		"config-overlay", // the correct expression, which now exists
		"\"surface\":",   // shown as a copyable shape
		"mode",           // what silently changed
		"exactly ONE owner",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("launch refusal missing %q — a user hitting this should be able to convert "+
				"without reading a design doc; got:\n%s", want, msg)
		}
	}
}

// The DESIGNED shape must still launch: the contributor declares `config-overlay`, the owner
// keeps its `config`. This is the pack the fzf example became, and refusing it would mean
// Option 1 broke what Option 2 shipped to make correct.
func TestStagePacksAllowsOverlayAlongsideOwner(t *testing.T) {
	home := packHome(t)
	owner := configSurfacePackDir(t, "owner", "acme", "settings", "", `{"preferences":{"x":1}}`)

	contributor := filepath.Join(t.TempDir(), "contrib")
	if err := os.MkdirAll(contributor, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"contrib","contributes":[` +
		`{"kind":"config-overlay","surface":"acme/settings",` +
		`"config":{"managed":{"fileSuggestion":"x"}}}]}`
	if err := os.WriteFile(filepath.Join(contributor, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+owner+`", "file://`+contributor+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-overlay-ok")
	if err != nil {
		t.Fatalf("config + config-overlay on one identity is the supported shape and must "+
			"launch: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded %d packs, want 2", len(loaded))
	}
}

// THE SHIPPED PACKS must still launch together. If this fails, the check refuses every real
// jail — the finding to report rather than the check to weaken.
func TestStagePacksShippedSetStillLoads(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home,
		`["claude", "copilot", "opencode", "pi", "codex", "agy"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-shipped-six")
	if err != nil {
		t.Fatalf("all six shipped packs together must still launch (the config-exclusivity "+
			"pre-flight must not fire on them): %v", err)
	}
	if len(loaded) != 6 {
		t.Errorf("loaded %d packs, want all 6 shipped", len(loaded))
	}
}
