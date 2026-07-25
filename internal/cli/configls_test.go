package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
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

// TestConfigLsMarksUnrenderedSurface: claude/config is declared in the manifest but
// never rendered at boot (writeClaudeJSON owns ~/.claude.json because it must never
// be wiped). The listing must say so rather than implying the jail composes it.
func TestConfigLsMarksUnrenderedSurface(t *testing.T) {
	withSidecarDir(t)
	var out, errw bytes.Buffer
	if rc := configLs([]string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "claude/config") {
			if !strings.Contains(line, "not rendered at boot") {
				t.Errorf("claude/config not marked unrendered: %q", line)
			}
			return
		}
	}
	t.Error("claude/config row missing from --all listing")
}

// TestConfigLsEveryBuiltinSurfaceHasAMode is the anti-drift guard: a new builtin
// surface with no entry in prismSurfaceMode would list with an empty MODE, silently
// implying it has no posture.
func TestConfigLsEveryBuiltinSurfaceHasAMode(t *testing.T) {
	for _, s := range agentcfg.BuiltinManifest().Surfaces() {
		key := s.Agent + "/" + s.Name
		if prismSurfaceMode[key] == "" {
			t.Errorf("surface %s has no prismSurfaceMode entry — it would list with an empty MODE", key)
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

// TestConfigResetRemovesBothSidecars is the load-bearing reset behavior: removing
// ONLY the overlay would leave the next boot diffing the still-edited file against
// a stale baseline and instantly re-capturing the edits just discarded. Deleting
// last_render too forces the first-migration re-seed.
func TestConfigResetRemovesBothSidecars(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)
	writeSidecar(t, dir, "pi", "settings", `{"other":true}`, `{"other":false}`)

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "discarded 1 captured key") {
		t.Errorf("reset did not report what it discarded:\n%s", out.String())
	}
	for _, gone := range []string{"claude-settings.overlay.json", "claude-settings.last_render"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived reset (err=%v) — the next boot would re-capture", gone, err)
		}
	}
	// A different surface must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "pi-settings.overlay.json")); err != nil {
		t.Errorf("reset of one surface removed another's sidecar: %v", err)
	}
}

// TestConfigResetIsIdempotent: running it twice must not error.
func TestConfigResetIsIdempotent(t *testing.T) {
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
