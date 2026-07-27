package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// This file replaces two source-GREPPING drift tests (TestBuiltinSurfaceRenderPaths and
// TestBuiltinSurfaceModesMatchRenderCallSites, both deleted).
//
// Those tests scanned the entrypoint's own .go files for renderSurfaceStateful /
// renderSurfaceComputed / renderSurfaceRMW call sites and compared the surfaces found
// against a hand-written list. They existed because of a premise stated in their own
// docstring: "a builtin surface's posture is not declared in the manifest — it is implied
// by which render helper boot.go calls." That premise is now false. Mode is declared on the
// surface and ONE loop dispatches on it (renderDeclaredSurface), so there is no second
// place to drift from, and there are no per-agent call sites left to grep for.
//
// What CAN still go wrong is different, so the tests are too: a mode could be declared and
// dispatched to the wrong mechanism. That is behavioral, so it is checked behaviorally —
// by rendering and looking at which artifacts appear.

// modeEnv is a home + workspace with no host mounts, for observing what a render writes.
func modeEnv(t *testing.T) *Env {
	t.Helper()
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	withCtxRoot(t, t.TempDir(), "none")
	return e
}

// TestStatefulModeWritesCaptureSidecars: the defining property of `stateful` is that it
// captures in-jail edits, which only works if it persists the two sidecars. A stateful
// surface silently rendered by the computed path would look right on the first boot and
// lose every user edit from then on — the failure the deleted tests were guarding, checked
// here by its observable consequence instead of by grepping for a function name.
func TestStatefulModeWritesCaptureSidecars(t *testing.T) {
	e := modeEnv(t)
	s := manifest.Surface{
		Agent: "example", Name: "stateful", Path: "~/.example/s.json", Codec: "json",
		Defaults: map[string]any{"a": 1},
	}
	if err := renderDeclaredSurface(e, s, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		prismLastRenderPath(e, "example", "stateful"),
		prismOverlayPath(e, "example", "stateful"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("stateful render did not write %s: %v", filepath.Base(p), err)
		}
	}
}

// TestComputedModeWritesNoSidecars is the other half: `computed` means "overwrite every
// boot, discard in-jail edits". Writing an overlay would start capturing them, silently
// converting an intentional overwrite into an edit-preserving surface.
func TestComputedModeWritesNoSidecars(t *testing.T) {
	e := modeEnv(t)
	s := manifest.Surface{
		Agent: "example", Name: "computed", Path: "~/.example/c.json", Codec: "json",
		Defaults: map[string]any{"a": 1}, Mode: manifest.ModeComputed,
	}
	if err := renderDeclaredSurface(e, s, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expandHomePath(e, s.Path)); err != nil {
		t.Fatalf("computed render wrote no surface file: %v", err)
	}
	if _, err := os.Stat(prismOverlayPath(e, "example", "computed")); err == nil {
		t.Error("computed render wrote a capture overlay — it must discard in-jail edits")
	}
}

// TestRMWModePreservesUnknownKeys: `rmw` exists for files the AGENT owns, so its defining
// property is that a key yolo has no opinion about survives. A surface routed to a composed
// path instead would drop it — which for a credentials file is data loss.
func TestRMWModePreservesUnknownKeys(t *testing.T) {
	e := modeEnv(t)
	path := filepath.Join(e.Home, ".example", "r.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"agentOwned":"keep me"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := manifest.Surface{
		Agent: "example", Name: "rmw", Path: "~/.example/r.json", Codec: "json",
		Managed: map[string]any{"yolo": true}, Mode: manifest.ModeRMW,
	}
	if err := renderDeclaredSurface(e, s, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "keep me") {
		t.Errorf("rmw dropped an agent-owned key: %s", data)
	}
	if !strings.Contains(string(data), `"yolo"`) {
		t.Errorf("rmw did not assert its managed key: %s", data)
	}
}

// TestUnrenderedModeWritesNothing: a surface declared `unrendered` is listed so
// `config ls` can describe it and so host_files cannot claim its path, but yolo must not
// write it.
func TestUnrenderedModeWritesNothing(t *testing.T) {
	e := modeEnv(t)
	s := manifest.Surface{
		Agent: "example", Name: "none", Path: "~/.example/u.json", Codec: "json",
		Defaults: map[string]any{"a": 1}, Mode: manifest.ModeUnrendered,
	}
	if err := renderDeclaredSurface(e, s, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expandHomePath(e, s.Path)); err == nil {
		t.Error("an unrendered surface must not be written")
	}
}

// TestEveryEmbeddedPackSurfaceDeclaresAKnownMode is the anti-drift guard that survives the
// transition: a pack surface whose mode is a typo would fall through to the default
// (stateful) and start capturing edits on a file meant to be overwritten. The decoder
// rejects an unknown mode, so this proves every shipped pack passes that decoder — which is
// the check that used to be "does a render helper call it".
func TestEveryEmbeddedPackSurfaceDeclaresAKnownMode(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	known := map[string]bool{
		manifest.ModeStateful: true, manifest.ModeComputed: true,
		manifest.ModeRMW: true, manifest.ModeUnrendered: true,
	}
	seen := 0
	for _, p := range packs {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			t.Errorf("pack %s: %v", p.Name, probs)
			continue
		}
		for _, s := range surfaces {
			seen++
			if !known[s.ResolvedMode()] {
				t.Errorf("%s/%s declares unknown mode %q", s.Agent, s.Name, s.ResolvedMode())
			}
		}
	}
	if seen == 0 {
		t.Error("no pack surfaces found — the embed list or the packs/ tree is broken")
	}
}
