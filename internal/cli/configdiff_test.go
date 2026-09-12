package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A6: config-ref promised "`yolo config reset` re-seeds" a `once` host_files
// entry. That cannot happen: reset discards CAPTURED edits by deleting the §5
// sidecars (capturedSurfaces gates on mode == "capture"), and a `once` surface
// writes no sidecar at all. The doc now says to delete the file instead, which is
// what actually re-seeds — `once` keys off the destination's absence.
//
// This test pins the CODE side of that claim so the doc cannot drift back: reset
// must not consider a non-capture surface.
func TestResetOnlyCoversCaptureSurfaces(t *testing.T) {
	for _, s := range capturedSurfaces("claude", "") {
		if surfaceMode(s) != "capture" {
			t.Errorf("reset considered non-capture surface %s/%s", s.Agent, s.Name)
		}
	}
	// claude/config is unrendered, so reset must never pick it up.
	for _, s := range capturedSurfaces("claude", "config") {
		t.Errorf("reset must not cover the unrendered claude/config: got %s/%s", s.Agent, s.Name)
	}
}

// Ruling 1 / B1: `reset` must survive adopt-on-first-migration.
//
// Deleting the two sidecars used to BE the discard: no baseline meant the next boot
// re-seeded from scratch. B1 changed that path to ADOPT the on-disk file, and "no
// baseline" is indistinguishable from "the user asked to discard" — so reset must
// also truncate the surface, or reset → no baseline → adopt resurrects the very
// edits the user discarded and reset is a silent no-op.
//
// This asserts the truncation half directly: an edited surface goes back to its
// pure render, so there is nothing left for the next boot to adopt.
func TestResetTruncatesSurfaceSoAdoptionFindsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s, ok := surfaceManifest().Lookup("copilot", "config")
	if !ok {
		t.Fatal("missing copilot/config")
	}
	path := expandHome(s.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An "edited" surface carrying a key yolo does not assert.
	if err := os.WriteFile(path, []byte(`{"yolo":true,"myEdit":"keep-me-not"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := truncateSurfaceToPureRender(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "myEdit") {
		t.Errorf("reset left the discarded edit in the file, so the next boot would adopt it back:\n%s", data)
	}
	if !strings.Contains(string(data), "yolo") {
		t.Errorf("reset must leave the PURE RENDER, not an empty file:\n%s", data)
	}
}

// An absent surface stays absent: reset discards edits, it does not create files
// the jail has not written yet.
func TestResetTruncationLeavesAbsentFileAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, _ := surfaceManifest().Lookup("copilot", "config")
	if err := truncateSurfaceToPureRender(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expandHome(s.Path)); !os.IsNotExist(err) {
		t.Errorf("reset created a file that did not exist (stat err = %v)", err)
	}
}

// E3: `config capture` must fold the CURRENT on-disk edits into the overlay
// immediately, so `diff` reflects edits made this session instead of showing a stale
// answer until the next boot.
//
// Nothing is lost without it — every composed surface lives under a host-backed bind,
// so an edit and its baseline both survive --rm and the next boot captures normally.
// What lags is VISIBILITY, and a user checking their own divergence has no way to tell
// the answer is stale.
func TestConfigCaptureFoldsCurrentEditsImmediately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	prev := prismSidecarDir
	prismSidecarDir = func() string { return filepath.Join(ws, ".yolo", "prism") }
	t.Cleanup(func() { prismSidecarDir = prev })

	s, ok := surfaceManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("missing claude/settings")
	}
	// Seed a baseline (what yolo "last rendered") and an edited surface.
	if err := os.MkdirAll(filepath.Join(ws, ".yolo", "prism"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := expandHome(s.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	baseline := `{"model":"base"}`
	if err := os.WriteFile(prismLastRenderPath("claude", "settings"), []byte(baseline), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"base","myEdit":"present"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := captureSurface(s)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 {
		t.Fatalf("captureSurface recorded %d keys, want at least 1", n)
	}
	data, err := os.ReadFile(prismOverlayPath("claude", "settings"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "myEdit") {
		t.Errorf("the edit was not captured into the overlay:\n%s", data)
	}
}

// With NO baseline the surface has never been rendered here, so there is nothing to
// diff against. That must be reported as "nothing to capture" rather than treated as
// an edit — capturing the whole file would freeze yolo's own output as a user edit.
func TestConfigCaptureWithNoBaselineIsANoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	prev := prismSidecarDir
	prismSidecarDir = func() string { return filepath.Join(ws, ".yolo", "prism") }
	t.Cleanup(func() { prismSidecarDir = prev })

	s, _ := surfaceManifest().Lookup("claude", "settings")
	path := expandHome(s.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := captureSurface(s)
	if err != nil {
		t.Fatal(err)
	}
	if n != -1 {
		t.Errorf("captureSurface = %d, want -1 (no baseline, nothing to capture)", n)
	}
}

// Phase-0 data-loss guard: host-side (surfaces resolve against a REAL home, not a
// jail's), `reset`/`capture` must REFUSE by default and only proceed with --force —
// so a stray host-side invocation cannot truncate a real dotfile or copy host config
// into a workspace. surfacesAreLocal() is false when YOLO_VERSION is unset, which is
// exactly the host-side condition.
func TestResetCaptureRefuseHostSideWithoutForce(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // force the host-side branch (surfacesAreLocal()==false)
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	for _, cmd := range []string{"reset", "capture"} {
		var out, errw bytes.Buffer
		// Without --force: refused, and the sidecar is left intact.
		rc := configRunW([]string{cmd, "claude", "--surface", "settings"}, &out, &errw)
		if rc == 0 {
			t.Errorf("%s host-side without --force should refuse, got rc=0", cmd)
		}
		if !strings.Contains(errw.String(), "refusing") {
			t.Errorf("%s refusal message unclear:\n%s", cmd, errw.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "claude-settings.overlay.json")); err != nil {
			t.Errorf("%s without --force removed the sidecar (err=%v) — it must not touch anything", cmd, err)
		}
	}

	// With --force, reset proceeds (removes the sidecars) — the escape hatch works.
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"reset", "claude", "--surface", "settings", "--force"}, &out, &errw); rc != 0 {
		t.Fatalf("reset --force host-side should proceed, got rc=%d: %s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-settings.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("reset --force did not remove the overlay sidecar (err=%v)", err)
	}
}

// diffFixture seeds a workspace-local sidecar pair for one surface and returns
// `config diff <agent>`'s plain-text output.
//
// It drives configDiff rather than readLastRenderKeys directly, on purpose: the bug
// these tests pin is a mismatch between the two sidecar readers the COMMAND pairs, so
// a test that called the reader alone would still pass with the pairing broken.
func diffFixture(t *testing.T, agent, name, lastRender, overlayJSON string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	prev := prismSidecarDir
	prismSidecarDir = func() string { return filepath.Join(ws, ".yolo", "prism") }
	t.Cleanup(func() { prismSidecarDir = prev })
	if err := os.MkdirAll(prismSidecarDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(capturedSurfaces(agent, name)) == 0 {
		t.Fatalf("%s/%s is not a capture surface, so the fixture proves nothing", agent, name)
	}
	if err := os.WriteFile(prismLastRenderPath(agent, name), []byte(lastRender), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prismOverlayPath(agent, name), []byte(overlayJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if rc := configDiff([]string{agent, "--surface", name}, &out, io.Discard, false); rc != 0 {
		t.Fatalf("config diff %s rc=%d\n%s", agent, rc, out.String())
	}
	return out.String()
}

// A capture that is byte-for-byte yolo's own last render is REDUNDANT, and diff must
// say so whatever format the surface is written in.
//
// It did not. readLastRenderKeys read every last_render sidecar as JSON, but the
// sidecar holds the last render's exact bytes, so a TOML surface's baseline failed to
// decode and came back empty — and an empty baseline reads as "(added in-jail)". Both
// TOML surfaces (mise/config, codex/config) reported a fully redundant capture as a
// new in-jail edit; measured on this repo's own jail before the fix.
//
// `config promote` consumes the same comparison to decide which captured keys are
// redundant, so the wrong answer here would become a wrong promotion there.
func TestDiffReadsTheLastRenderInTheSurfacesOwnCodec(t *testing.T) {
	// mise/config is core's own TOML surface — no pack needed to reach it.
	got := diffFixture(t, "mise", "config",
		"[tools]\nneovim = \"nightly\"\n",
		`{"tools":{"neovim":"nightly"}}`)
	if strings.Contains(got, "added in-jail") {
		t.Errorf("a TOML capture identical to the last render reported as a NEW edit:\n%s", got)
	}
	if !strings.Contains(got, "redundant capture") {
		t.Errorf("a TOML capture identical to the last render was not reported redundant:\n%s", got)
	}
}

// A TOML surface whose capture genuinely differs from the last render must still read
// as a real edit — the fix above must not make every TOML key look redundant.
func TestDiffStillReportsARealTOMLEdit(t *testing.T) {
	got := diffFixture(t, "mise", "config",
		"[tools]\nneovim = \"nightly\"\n",
		`{"tools":{"neovim":"stable"}}`)
	if strings.Contains(got, "redundant capture") {
		t.Errorf("a changed TOML value reported as redundant:\n%s", got)
	}
	if !strings.Contains(got, `(was {"neovim": "nightly"})`) {
		t.Errorf("a changed TOML value did not report what it was:\n%s", got)
	}
}

// The codec swap alone would have traded the TOML bug for a JSON one, so this pins the
// other half: the baseline is re-encoded through the JSON codec and re-decoded with
// jsonx, which is exactly the transform agentcfg.marshalOverlay applied to the overlay
// sidecar. Without it an integer arrives as a jsonx integer literal on the overlay side
// ("5") and as float64 on the codec side ("5.0"), and every integer-valued key in a
// JSON surface starts misreporting as an edit.
func TestDiffComparesIntegersAcrossTheTwoSidecarModels(t *testing.T) {
	got := diffFixture(t, "claude", "settings",
		`{"cleanupPeriodDays":5}`,
		`{"cleanupPeriodDays":5}`)
	if !strings.Contains(got, "redundant capture") {
		t.Errorf("an integer key identical to the last render did not report redundant:\n%s", got)
	}
}
