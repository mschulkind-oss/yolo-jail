package entrypoint

// hostcaptureoutranksoverlay_test.go pins the overwrite report to WHAT THE WRITE PRODUCES.
//
// Measured on the maintainer's host 2026-09-28, under `host_management: own`: the user's
// hand-added claude `hooks.Notification` had been captured, and the matt pack's config-overlay
// declared a different one. The fold puts a captured edit ABOVE every config-overlay
// (defaults<host<workspace<config-overlay<config-list<overlay<managed), so every apply wrote the
// user's value back — and every apply reported "⚠ overwrote your existing value for:
// hooks.Notification (config-overlay from matt)", the exact inverse, under a per-surface
// `rendered` while the counts said nothing changed. The report was computed from the overlay's
// declaration against the file, never from the file the write produces.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	usersNotification = `[{"matcher":"","hooks":[{"type":"command","command":"printf '\\a' > /dev/tty"}]}]`
	packsNotification = `[{"matcher":"idle_prompt","hooks":[{"type":"command","command":"printf bell"}]}]`
)

// notificationOverlayPack is a user pack whose config-overlay declares claude/settings'
// hooks.Notification — the matt pack's shape.
func notificationOverlayPack(t *testing.T) *packload.Pack {
	t.Helper()
	var hook any
	if err := json.Unmarshal([]byte(packsNotification), &hook); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"managed": map[string]any{
		"hooks": map[string]any{"Notification": hook}}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "matt", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "claude/settings", Raw: raw},
		},
	}}
}

func seedClaudeSettings(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"theme":"dark","hooks":{"Notification":` + usersNotification + `}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func notificationInFile(t *testing.T, path string) string {
	t.Helper()
	var got struct {
		Hooks struct {
			Notification json.RawMessage `json:"Notification"`
		} `json:"hooks"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	var v any
	_ = json.Unmarshal(got.Hooks.Notification, &v)
	b, _ := json.Marshal(v)
	return string(b)
}

func canonicalJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// renderClaudeWithOverlay renders claude/settings at an owned host with matt's overlay. rmw
// re-declares every claude surface `rmw` (asRetiredAssert's copy, used here for exactly that and
// not for an asserted home), so the surface runs the rmw arm instead of composing `stateful`.
func renderClaudeWithOverlay(t *testing.T, home string, rmw, observe bool) HostRenderResult {
	t.Helper()
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	if rmw {
		claude = asRetiredAssert(t, claude)
	}
	overlays := packoverlay.Collect([]*packload.Pack{claude, notificationOverlayPack(t)}, false, nil)
	results, err := RenderHostPack(claude, home, render.OwnershipOwn, observe, overlays, nil)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	return resultFor(t, results, "claude/settings")
}

func mentions(list []string, key string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, key+" ") || s == key {
			return true
		}
	}
	return false
}

// UNDER `own` THE CAPTURED EDIT WINS, so nothing is overwritten and the report says whose value
// is in the file: the user's, kept over matt's overlay. On every run, and the second run is
// `unchanged` in both the action and the predicate.
func TestUnderOwnACapturedEditKeptOverAnOverlayIsNotReportedAsOverwritten(t *testing.T) {
	home := t.TempDir()
	path := seedClaudeSettings(t, home)
	want := canonicalJSON(t, usersNotification)

	for run := 1; run <= 3; run++ {
		observe := run == 3
		r := renderClaudeWithOverlay(t, home, false, observe)
		if got := notificationInFile(t, path); got != want {
			t.Fatalf("run %d: fixture premise — the captured edit outranks the overlay, so the file "+
				"keeps the user's hook; got %s", run, got)
		}
		if mentions(r.Overwrites, "hooks.Notification") {
			t.Errorf("run %d: Overwrites=%v names hooks.Notification, but the write kept the "+
				"user's value there", run, r.Overwrites)
		}
		if !mentions(r.Kept, "hooks.Notification") {
			t.Errorf("run %d: Kept=%v; want hooks.Notification named as kept over matt's "+
				"config-overlay", run, r.Kept)
		}
		for _, k := range r.Kept {
			if strings.HasPrefix(k, "hooks.Notification") && !strings.Contains(k, "matt") {
				t.Errorf("run %d: the kept key does not name the overlay it outranked: %q", run, k)
			}
		}
		if run >= 2 && (r.WouldChange || r.Action != "unchanged") {
			t.Errorf("run %d: Action=%q WouldChange=%v over a settled file; the line and the "+
				"counts must agree that nothing changed", run, r.Action, r.WouldChange)
		}
	}
}

// THE CONTROL: through the rmw arm — a surface its pack declares `rmw`, which an owned host still
// runs (and every surface ran under the retired `assert`) — nothing is captured, so the overlay
// IS written over the user's value, the overwrite is real and reported, and nothing is kept.
func TestThroughRMWAnOverlayOverwriteIsStillReported(t *testing.T) {
	home := t.TempDir()
	path := seedClaudeSettings(t, home)
	r := renderClaudeWithOverlay(t, home, true, false)
	if got, want := notificationInFile(t, path), canonicalJSON(t, packsNotification); got != want {
		t.Fatalf("fixture premise — through rmw the overlay lands; got %s", got)
	}
	if !mentions(r.Overwrites, "hooks.Notification") {
		t.Errorf("Overwrites=%v; the overlay replaced the user's value and must say so", r.Overwrites)
	}
	if len(r.Kept) != 0 {
		t.Errorf("Kept=%v through rmw, where no captured edit exists", r.Kept)
	}
}
