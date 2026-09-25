package cli

// configlistcapture_test.go pins the CLI half of per-entry capture at config-list paths
// (docs/reference/pack-system.md#config-list-capture, OQ-AL1): the two layer-less captures
// (captureSurfaceAt, behind `yolo config capture` and capture-on-terminate) read and write the
// list-capture sidecar — the ONLY place they can learn which paths are list paths — `yolo
// config reset` discards it with the overlay, and `yolo config diff` reports its entries.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// A torn-down jail whose last boot rendered a config-list path, and whose agent then appended
// an entry: capture-on-terminate must record the entry PER ENTRY, never the whole array.
// Without the list-capture sidecar in its captureLocation this capture cannot know the path is
// a list path and freezes the whole array into the overlay — the defect OQ-AL1 ends.
func TestCaptureOnTerminateCapturesAListPathPerEntry(t *testing.T) {
	ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
		`{"plugins":["owner","pack-entry"]}`, `{"plugins":["owner","pack-entry","mine"]}`)
	listCapture := filepath.Join(ws, ".yolo", "prism", "claude-settings"+render.ListCaptureSuffix)
	if err := os.WriteFile(listCapture, []byte(`{"/plugins":{"add":[],"remove":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	captureOnTerminate(ws, "podman", func(m string) { warnings = append(warnings, m) })
	if len(warnings) != 0 {
		t.Fatalf("teardown warned: %v", warnings)
	}
	if got := readOverlay(t, ws, "claude-settings"); strings.Contains(got, "plugins") {
		t.Fatalf("capture-on-terminate froze the whole array into the overlay:\n%s", got)
	}
	data, err := os.ReadFile(listCapture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mine") || strings.Contains(string(data), "pack-entry") {
		t.Fatalf("list capture = %s — want the agent's entry recorded and the pack's not", data)
	}
}

// A surface with NO list path gets no list-capture sidecar from a capture.
func TestCaptureWritesNoListCaptureWithoutAListPath(t *testing.T) {
	ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
		`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)
	captureOnTerminate(ws, "podman", func(string) {})
	listCapture := filepath.Join(ws, ".yolo", "prism", "claude-settings"+render.ListCaptureSuffix)
	if _, err := os.Stat(listCapture); !os.IsNotExist(err) {
		t.Fatalf("a capture created a list-capture sidecar for a surface with no list (err=%v)", err)
	}
}

// `yolo config reset` discards the per-entry capture with the overlay — a reset that kept it
// would re-apply the discarded additions and removals on the next render — and says so.
func TestConfigResetDiscardsTheListCapture(t *testing.T) {
	home := withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{}`, `{"plugins":["a","mine"]}`)
	listCapture := tgt.listCapturePath("claude", "settings")
	if err := os.WriteFile(listCapture, []byte(`{"/plugins":{"add":["mine"],"remove":["b"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"plugins":["a","mine"]}`)

	var out, errw bytes.Buffer
	if rc := configReset(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset rc=%d, stderr=%s", rc, errw.String())
	}
	if _, err := os.Stat(listCapture); !os.IsNotExist(err) {
		t.Fatalf("the list capture survived reset (err=%v)", err)
	}
	if !strings.Contains(out.String(), "2 captured list entries") {
		t.Errorf("reset did not report the list entries it discarded:\n%s", out.String())
	}
}

// `yolo config diff` reports captured list entries — a `+` per addition, a `-` per removal,
// under the list path — even when the overlay itself is empty; and nothing about which packs
// contributed (that is `ls`'s, OQ-CR7).
func TestConfigDiffReportsCapturedListEntries(t *testing.T) {
	withScratchHome(t)
	tgt, dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{}`, `{"plugins":["a"]}`)
	if err := os.WriteFile(tgt.listCapturePath("claude", "settings"),
		[]byte(`{"/plugins":{"add":["mine"],"remove":["b"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := configDiff(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	for _, want := range []string{`/plugins  + "mine"`, `/plugins  - "b"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("diff output lacks %q:\n%s", want, out.String())
		}
	}
}

// `yolo config capture` (the in-jail, interactive twin of capture-on-terminate) resolves the
// list-capture sidecar off the target and records per entry too.
func TestConfigCaptureCapturesAListPathPerEntry(t *testing.T) {
	home := withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{}`, `{"plugins":["owner","pack-entry"]}`)
	if err := os.WriteFile(tgt.listCapturePath("claude", "settings"),
		[]byte(`{"/plugins":{"add":[],"remove":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"plugins":["owner","pack-entry","mine"]}`)

	var out, errw bytes.Buffer
	if rc := configCapture(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configCapture rc=%d, stderr=%s", rc, errw.String())
	}
	overlay, _ := os.ReadFile(tgt.overlayPath("claude", "settings"))
	if strings.Contains(string(overlay), "plugins") {
		t.Fatalf("`config capture` froze the whole array into the overlay:\n%s", overlay)
	}
	data, _ := os.ReadFile(tgt.listCapturePath("claude", "settings"))
	if !strings.Contains(string(data), "mine") {
		t.Fatalf("list capture = %s, want the agent's entry", data)
	}
}
