package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// terminateWorkspace builds a host-side workspace the way a torn-down jail leaves
// one: the prism sidecar dir with a last_render baseline, and the ws_state overlay
// holding the (edited) surface file at the podman location.
// It also pins HOME at an empty temp dir. Not hygiene for its own sake: a
// regression that resolved surfaces through expandHome would otherwise read the
// developer's (or the CI runner's) REAL dotfiles, which both hides the bug behind
// ambient state and makes the test capture whatever happens to be in that home.
func terminateWorkspace(t *testing.T, rel, baseline, current string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	prism := filepath.Join(ws, ".yolo", "prism")
	if err := os.MkdirAll(prism, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prism, "claude-settings.last_render"),
		[]byte(baseline), 0o644); err != nil {
		t.Fatal(err)
	}
	surface := filepath.Join(ws, ".yolo", "home", rel)
	if err := os.MkdirAll(filepath.Dir(surface), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func readOverlay(t *testing.T, ws, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, ".yolo", "prism", name+".overlay.json"))
	if err != nil {
		return ""
	}
	return string(data)
}

// TestCaptureOnTerminateFoldsThisSessionsEdits is E3 itself: after the jail is
// gone, an edit made inside it is in the overlay, so `yolo config diff` answers
// about the session that just ended.
func TestCaptureOnTerminateFoldsThisSessionsEdits(t *testing.T) {
	ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
		`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)

	var warnings []string
	captureOnTerminate(ws, "podman", func(m string) { warnings = append(warnings, m) })

	if got := readOverlay(t, ws, "claude-settings"); !strings.Contains(got, "myEdit") {
		t.Errorf("the in-jail edit was not captured at teardown:\n%s", got)
	}
	if len(warnings) != 0 {
		t.Errorf("clean teardown warned: %v", warnings)
	}
}

// TestCaptureOnTerminateNeverReadsTheRealHome is the privacy guard, and the reason
// this cannot reuse expandHome. Host-side, `~` is the invoking HUMAN's home; a
// capture that resolved there would copy their own dotfiles into the workspace
// overlay (BACKLOG G2), which is exactly what refuseHostSideWrite refuses the
// interactive command at the host to prevent.
func TestCaptureOnTerminateNeverReadsTheRealHome(t *testing.T) {
	// The workspace's own copy carries one edit...
	ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
		`{"model":"base"}`, `{"model":"base","jailEdit":"present"}`)
	// ...and a real dotfile at the surface's `~` path carries a different one, with
	// a key nothing else has. (After terminateWorkspace, which pins HOME itself.)
	home := t.TempDir()
	t.Setenv("HOME", home)
	realHome := filepath.Join(home, ".claude")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "settings.json"),
		[]byte(`{"model":"base","hostOnlySecret":"leaked"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	captureOnTerminate(ws, "podman", func(string) {})

	got := readOverlay(t, ws, "claude-settings")
	if strings.Contains(got, "hostOnlySecret") {
		t.Errorf("capture-on-terminate read the invoking user's REAL home:\n%s", got)
	}
	if !strings.Contains(got, "jailEdit") {
		t.Errorf("capture-on-terminate did not read the workspace's own copy:\n%s", got)
	}
}

// TestCaptureOnTerminateNeverBreaksTeardown is R7. A capture failure warns and the
// teardown proceeds — a jail that will not exit cleanly because an observability
// fold failed is a worse bug than the stale `diff` this fixes.
func TestCaptureOnTerminateNeverBreaksTeardown(t *testing.T) {
	ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
		`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)
	// Make the sidecar dir unwritable so the overlay write fails.
	prism := filepath.Join(ws, ".yolo", "prism")
	if err := os.Chmod(prism, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(prism, 0o755) })
	if f, err := os.OpenFile(filepath.Join(prism, "probe"), os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		// Running as root (the jail's own uid) ignores the mode bits, so the write
		// would succeed and the assertion would be vacuous. Fall back to making the
		// overlay path a DIRECTORY, which no uid can WriteFile over.
		_ = f.Close()
		_ = os.Remove(filepath.Join(prism, "probe"))
		_ = os.Chmod(prism, 0o755)
		if err := os.Mkdir(filepath.Join(prism, "claude-settings.overlay.json"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var warnings []string
	captureOnTerminate(ws, "podman", func(m string) { warnings = append(warnings, m) })

	if len(warnings) == 0 {
		t.Fatal("a failed capture must warn — silence makes the stale diff undiagnosable")
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "claude/settings") {
		t.Errorf("the warning does not name the surface: %v", warnings)
	}
}

// TestCaptureOnTerminateSurvivesAPanickingCapture: the recover is part of R7, not
// belt-and-braces. A nil-map or index panic deep in the compose engine must still
// leave the teardown running.
func TestCaptureOnTerminateSurvivesAPanickingCapture(t *testing.T) {
	ws := t.TempDir()
	// A sidecar dir that is a FILE, not a dir: Stat succeeds, IsDir is false.
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".yolo", "prism"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	captureOnTerminate(ws, "podman", func(string) {}) // must simply return
}

// TestCaptureOnTerminateIsANoOpWithoutABaseline: a workspace no jail has rendered
// into has no last_render, so there is nothing to tell an edit from yolo's own
// output. It must record nothing rather than freeze the whole file as an edit.
func TestCaptureOnTerminateIsANoOpWithoutABaseline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	prism := filepath.Join(ws, ".yolo", "prism")
	if err := os.MkdirAll(prism, 0o755); err != nil {
		t.Fatal(err)
	}
	surface := filepath.Join(ws, ".yolo", "home", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(surface), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface, []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	captureOnTerminate(ws, "podman", func(string) {})

	if got := readOverlay(t, ws, "claude-settings"); got != "" {
		t.Errorf("captured with no baseline — yolo's own output would freeze as an edit:\n%s", got)
	}
}

// TestJailHomeHostPath pins the two backend layouts and the decline. The rule is
// load-bearing: get it wrong and capture either reads nothing (silent no-op) or
// reads the wrong file.
func TestJailHomeHostPath(t *testing.T) {
	ws := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		p := filepath.Join(ws, ".yolo", "home", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// podman: the writable overlay is named with the leading dot stripped.
	mk("claude/settings.json")
	mk("config/mise/config.toml")
	mk("go/env")
	// Apple Container: the whole ws_state IS the home, so the dot survives.
	mk(".gemini/antigravity-cli/settings.json")

	cases := []struct {
		runtime, surface, wantRel string
		wantOK                    bool
	}{
		{"podman", "~/.claude/settings.json", "claude/settings.json", true},
		{"podman", "~/.config/mise/config.toml", "config/mise/config.toml", true},
		{"podman", "~/go/env", "go/env", true}, // no dot to strip
		{"container", "~/.gemini/antigravity-cli/settings.json",
			".gemini/antigravity-cli/settings.json", true},
		// Declines: wrong layout for the runtime, absent file, non-home path.
		{"container", "~/.claude/settings.json", "", false},
		{"podman", "~/.gemini/antigravity-cli/settings.json", "", false},
		{"podman", "~/.nothing/here.json", "", false},
		{"podman", "/etc/passwd", "", false},
	}
	for _, tc := range cases {
		got, ok := jailHomeHostPath(ws, tc.runtime, tc.surface)
		if ok != tc.wantOK {
			t.Errorf("jailHomeHostPath(%s, %q) ok = %v, want %v", tc.runtime, tc.surface, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		want := filepath.Join(ws, ".yolo", "home", filepath.FromSlash(tc.wantRel))
		if got != want {
			t.Errorf("jailHomeHostPath(%s, %q) = %q, want %q", tc.runtime, tc.surface, got, want)
		}
	}
}

// TestTerminateCaptureSurfacesCoversCaptureModeAndUserSidecars: the surface set is
// the manifest's capture-mode surfaces plus the host_files (`user`) sidecars, which
// live in config rather than the manifest and so must be discovered from disk.
func TestTerminateCaptureSurfacesCoversCaptureModeAndUserSidecars(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "user-_2egitconfig.overlay.json"),
		[]byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := terminateCaptureSurfaces(dir)

	byKey := map[string]manifest.Surface{}
	for _, s := range got {
		byKey[s.Agent+"/"+s.Name] = s
	}
	if _, ok := byKey["claude/settings"]; !ok {
		t.Errorf("claude/settings (mode: capture) missing from the surface set: %v", byKey)
	}
	u, ok := byKey["user/_2egitconfig"]
	if !ok {
		t.Fatalf("the host_files sidecar was not discovered: %v", byKey)
	}
	if u.Path != "~/.gitconfig" {
		t.Errorf("user sidecar path = %q, want ~/.gitconfig (the slug must be unescaped)", u.Path)
	}
	// A non-capture surface must not be in the set: capture is never implicit.
	for k := range byKey {
		if k == "claude/config" || k == "copilot/mcp" {
			t.Errorf("%s is not a capture surface but was included", k)
		}
	}
}
