package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// withSidecarDir points the CLI's sidecar resolver at a temp dir. Without this
// seam an in-jail test run would read — and `reset` would DELETE — the real
// /workspace sidecars.
func withSidecarDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := prismSidecarDir
	prismSidecarDir = func() string { return dir }
	t.Cleanup(func() { prismSidecarDir = orig })
	return dir
}

// withLocalSurfaces pins surfacesAreLocal()==true for the duration of a test, so a
// reset/capture happy path runs the in-jail-owner branch regardless of the ambient
// environment. Without it these tests pass in a dev jail (YOLO_VERSION set,
// /workspace mounted) but the Phase-0 guard refuses on a bare CI runner, where
// neither holds — the classic passes-in-jail-fails-in-CI trap. Tests that model the
// host-side branch (e.g. TestResetCaptureRefuseHostSideWithoutForce) must NOT call
// this.
func withLocalSurfaces(t *testing.T) {
	t.Helper()
	orig := surfacesAreLocal
	surfacesAreLocal = func() bool { return true }
	t.Cleanup(func() { surfacesAreLocal = orig })
}

// withScratchHome points $HOME at a temp dir for tests that RESET a surface.
//
// ⚠ Not optional, and not hygiene. `reset` truncates the surface file to its pure render
// (ruling 1), and `expandHome` resolves that path against the ambient $HOME — so a reset test
// without this rewrites the DEVELOPER'S OWN ~/.claude/settings.json, in the jail this repo is
// developed inside. It survived unnoticed because claude/settings declares `readsHost`, so the
// jail-notch truncation re-reads the file as its own host layer and composes the same bytes
// back; a surface without that declaration would have been emptied. It also makes the reset
// path's own behaviour ambient: whether reset has a file to truncate — and therefore a
// baseline to re-seed — would depend on what the machine happens to have in its home.
func withScratchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeSidecar seeds an overlay (and optionally a last_render baseline).
func writeSidecar(t *testing.T, dir, agent, name, overlayJSON, lastRenderJSON string) {
	t.Helper()
	if overlayJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".overlay.json"), []byte(overlayJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if lastRenderJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".last_render"), []byte(lastRenderJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConfigLsListsSurfacesAndFlagsOverlay: the listing must show every surface's
// construction AND flag the ones carrying captured edits — the whole point, since
// an overlay outranks the host layer with no other user-facing view.
func TestConfigLsListsSurfacesAndFlagsOverlay(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark","model":null}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configLs([]string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{"SURFACE", "claude/settings", "~/.claude/settings.json", "capture"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "2 keys ⚠") {
		t.Errorf("overlay not flagged with its key count:\n%s", got)
	}
	if !strings.Contains(got, "yolo config reset") {
		t.Errorf("footer must point at the cure:\n%s", got)
	}
	// A pure-overwrite surface must never be reported as carrying an overlay.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "copilot/mcp") && strings.Contains(line, "⚠") {
			t.Errorf("a no-sidecar surface was flagged: %q", line)
		}
	}
}

// TestConfigLsMarksUnrenderedSurface: a surface declared as `unrendered` must be listed
// as such rather than implying the jail composes it.
//
// It used to pin claude/config, which was unrendered only because ~/.claude.json had a
// bespoke Go writer that must never wipe it. That writer is gone — the surface is now
// rmw + reconcile, actually rendered — so the test asserts the MECHANISM against a
// synthetic surface instead of a real one that no longer has the property. Keeping it
// pointed at claude/config would have meant re-marking a rendered file "not rendered" to
// satisfy a test.
func TestConfigLsMarksUnrenderedSurface(t *testing.T) {
	s := manifest.Surface{
		Agent: "example", Name: "config", Path: "~/.example/config.json",
		Codec: "json", Mode: manifest.ModeUnrendered,
	}
	if got := surfaceMode(s); got != surfaceModeUnrendered {
		t.Fatalf("surfaceMode = %q, want %q", got, surfaceModeUnrendered)
	}
	row := surfaceRow{Surface: "example/config", Path: s.Path, Codec: s.Codec,
		Mode: surfaceModeUnrendered, Overlay: -1, Reserved: true}
	var out bytes.Buffer
	writeSurfaceTable(&out, []surfaceRow{row}, false)
	if !strings.Contains(out.String(), "not rendered at boot") {
		t.Errorf("an unrendered surface must say so in the listing:\n%s", out.String())
	}
}

// TestConfigLsListsRenderedClaudeConfig is the other half, and the reason the test above
// changed: ~/.claude.json IS rendered now (rmw, with its mcpServers table reconciled), so
// the listing must not describe it as reserved.
func TestConfigLsListsRenderedClaudeConfig(t *testing.T) {
	withSidecarDir(t)
	var out, errw bytes.Buffer
	if rc := configLs([]string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "claude/config") {
			if strings.Contains(line, "not rendered at boot") {
				t.Errorf("claude/config is rendered now; listing still calls it reserved: %q", line)
			}
			return
		}
	}
	t.Error("claude/config row missing from --all listing")
}

// TestConfigLsEveryBuiltinSurfaceHasAMode is the anti-drift guard: a new builtin
// surface with no resolvable mode would list with an empty MODE, silently
// implying it has no posture.
func TestConfigLsEveryBuiltinSurfaceHasAMode(t *testing.T) {
	for _, s := range surfaceManifest().Surfaces() {
		key := s.Agent + "/" + s.Name
		if surfaceMode(s) == "" {
			t.Errorf("surface %s has no resolvable mode — it would list with an empty MODE", key)
		}
	}
}

// TestConfigDiffShowsCapturedKeys: diff must distinguish a REAL edit from a
// redundant capture (same value yolo last wrote) and from a deletion, because the
// audit found most captured keys are noise.
func TestConfigDiffShowsCapturedKeys(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings",
		`{"theme":"dark","effortLevel":"xhigh","model":null,"added":1}`,
		`{"theme":"light","effortLevel":"xhigh"}`)

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		`theme  "dark" (was "light")`, // a real change, with the prior value
		"redundant capture",           // effortLevel matches last_render
		"model  deleted in-jail",      // a captured deletion
		"added  1 (added in-jail)",    // a key yolo never wrote
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
}

// TestConfigDiffEmptyOverlayIsQuiet: an empty overlay is the normal state and must
// not read as a problem.
func TestConfigDiffEmptyOverlayIsQuiet(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "pi", "settings", `{}`, `{"theme":"dark"}`)

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"pi"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "No captured in-jail edits") {
		t.Errorf("empty overlay not reported as clean:\n%s", out.String())
	}
}

// TestConfigResetDiscardsTheOverlayAndReSeedsTheBaseline is the load-bearing reset behavior
// at the JAIL notch: the capture overlay goes, and the baseline is left describing the bytes
// reset itself just wrote.
//
// Both halves are one statement about the NEXT render. A surviving overlay would re-apply the
// edit that was just discarded. A surviving STALE baseline — the pre-reset render — would have
// the next boot diff the truncated file against it and capture the discard as an edit. And no
// baseline at all, which is what this used to assert, has its own cost: a first migration over
// a non-empty file is an ADOPTION, so the next render treated reset's own output as the user's
// file and spent OQ-CO7's one-per-surface archive on a copy of it (§6.3.3, D1).
func TestConfigResetDiscardsTheOverlayAndReSeedsTheBaseline(t *testing.T) {
	withLocalSurfaces(t)
	home := withScratchHome(t)
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)
	writeSidecar(t, dir, "pi", "settings", `{"other":true}`, `{"other":false}`)
	// The surface the edit lives in. Without a file there is nothing to truncate and nothing
	// to re-seed, so the baseline half of this test would be vacuous.
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{"theme":"dark"}`)

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "discarded 1 captured key") {
		t.Errorf("reset did not report what it discarded:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-settings.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("the capture overlay survived reset (err=%v) — the discarded edit would be "+
			"re-applied by the next render", err)
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "claude-settings.last_render"))
	if err != nil {
		t.Fatalf("reset truncated %s and left no baseline for those bytes: %v", settings, err)
	}
	rendered, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(rendered) {
		t.Errorf("the baseline is not the file reset wrote:\n baseline: %q\n file:     %q\n\n"+
			"A baseline that disagrees with the file is the stale one this half exists to "+
			"prevent; one that is ABSENT makes the next render read yolo's own output as the "+
			"user's file (OQ-CO7 D1).", baseline, rendered)
	}
	// A different surface must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "pi-settings.overlay.json")); err != nil {
		t.Errorf("reset of one surface removed another's sidecar: %v", err)
	}
}

// TestConfigResetIsIdempotent: running it twice must not error. The second run finds an empty
// store — this surface has no file, so the first reset had no bytes to re-seed a baseline from
// — and says so rather than reporting a discard it did not make.
func TestConfigResetIsIdempotent(t *testing.T) {
	withLocalSurfaces(t)
	withScratchHome(t)
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"a":1}`, `{}`)
	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("first reset rc=%d", rc)
	}
	out.Reset()
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second reset rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "Nothing to reset") {
		t.Errorf("second reset should be a no-op, got:\n%s", out.String())
	}
}

// TestConfigDiffResetRejectMissingAgent: both need an agent, and an agent with no
// capture surfaces is an error rather than a silent success.
func TestConfigDiffResetRejectMissingAgent(t *testing.T) {
	withSidecarDir(t)
	for _, fn := range []func([]string, *bytes.Buffer, *bytes.Buffer, bool) int{
		func(a []string, o, e *bytes.Buffer, c bool) int { return configDiff(a, o, e, c) },
		func(a []string, o, e *bytes.Buffer, c bool) int { return configReset(a, o, e, c) },
	} {
		var out, errw bytes.Buffer
		if rc := fn(nil, &out, &errw, false); rc != 2 {
			t.Errorf("no agent: rc=%d, want 2", rc)
		}
		out.Reset()
		errw.Reset()
		if rc := fn([]string{"nosuchagent"}, &out, &errw, false); rc != 1 {
			t.Errorf("unknown agent: rc=%d, want 1", rc)
		}
	}
}

// TestConfigResetUserSurfacesFromSidecars: a host_files capture surface is keyed by
// an opaque slug, so `reset user` must discover its surfaces from the sidecar files
// — which also lets it clean up after an entry the user has since removed.
func TestConfigResetUserSurfacesFromSidecars(t *testing.T) {
	withLocalSurfaces(t)
	withScratchHome(t)
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "user", ".config_2fmytool_2fx.json", `{"k":"v"}`, `{}`)

	var out, errw bytes.Buffer
	if rc := configReset([]string{"user"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset user rc=%d, stderr=%s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "user-.config_2fmytool_2fx.json.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("user overlay survived reset: %v", err)
	}
}

// TestUnslugHostFilePath: the diff/ls header must name the FILE, not the escaped
// slug. The slug is a reversible percent-escape, so decoding needs no config read
// and still works for an entry the user has since removed.
func TestUnslugHostFilePath(t *testing.T) {
	for _, path := range []string{
		".config/mytool/config.json",
		".npmrc",
		".config/dir with spaces/x.conf",
		"foo/bar_baz.json", // a literal '_' must survive the round trip
	} {
		slug := (config.HostFileEntry{Path: path}).Slug()
		if got := unslugHostFilePath(slug); got != path {
			t.Errorf("unslugHostFilePath(Slug(%q)) = %q, want round-trip", path, got)
		}
	}
}

// TestComposedFileExistsNeverClaimsAbsenceElsewhere is the regression for a bug the
// nested-jail run caught: presence was checked against the PROCESS home, so a
// host-side `config ls` reported every jail-rendered file as absent, and an in-jail
// run for a DIFFERENT workspace (a nested jail, and every integration test) did the
// same. Presence is only knowable when the surfaces are this jail's own.
func TestComposedFileExistsNeverClaimsAbsenceElsewhere(t *testing.T) {
	// Host-side (no YOLO_VERSION): must never claim absence.
	t.Setenv("YOLO_VERSION", "")
	if !composedFileExists("~/definitely-not-a-real-file-xyz") {
		t.Error("host-side presence check claimed a jail file is absent")
	}
	// In-jail but resolved to a foreign workspace: same.
	t.Setenv("YOLO_VERSION", "9.9.9")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if !composedFileExists("~/definitely-not-a-real-file-xyz") {
		t.Error("in-jail-but-foreign-workspace check claimed a file is absent")
	}
}

// TestWorkspaceRootWalksUp: the sidecar dir must resolve from a SUBDIRECTORY of the
// workspace (like git), and must not be hardcoded to /workspace — that shortcut
// silently read another workspace's sidecars, and `reset` would have deleted them.
func TestWorkspaceRootWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".yolo", "prism"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	// EvalSymlinks: t.TempDir may hand back a symlinked path (/tmp -> /private/tmp).
	want, _ := filepath.EvalSymlinks(root)
	got, _ := filepath.EvalSymlinks(workspaceRoot())
	if got != want {
		t.Errorf("workspaceRoot() from a subdir = %q, want the workspace root %q", got, want)
	}
}
