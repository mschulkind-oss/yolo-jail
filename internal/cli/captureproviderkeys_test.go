package cli

// captureproviderkeys_test.go pins PP-D15 (docs/design/providers-and-profiles-redesign.md, the
// maintainer's 2026-10-11 ruling: "we should in fact overwrite that. That's just the way
// precedence works… the profile should [win]"): a capture of in-jail edits never records one at
// a provider key. Through the two production callers of captureSurfaceAt — `yolo config
// capture` (configCapture) and the fold at a jail's teardown (captureOnTerminate) — so deleting
// the keys' hand-off, the notice or the log line fails here.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capturelog"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestTheCaptureVerbNeverRecordsAProviderKey(t *testing.T) {
	home := withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"model":"base","theme":"dark","env":{"CLAUDE_CODE_USE_BEDROCK":"1","MY_SETTING":"x"}}`)
	writeSidecar(t, dir, "claude", "settings", "", `{"model":"base"}`)

	var out, errw bytes.Buffer
	if rc := configCapture(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture rc=%d: %s%s", rc, out.String(), errw.String())
	}
	overlay, err := os.ReadFile(filepath.Join(dir, "claude-settings.overlay.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(overlay), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("the capture recorded a provider key: %s", overlay)
	}
	if !strings.Contains(string(overlay), `"theme"`) || !strings.Contains(string(overlay), "MY_SETTING") {
		t.Errorf("every other edit must still be captured: %s", overlay)
	}
	for _, want := range []string{"claude/settings: not captured: env.CLAUDE_CODE_USE_BEDROCK = \"1\"",
		"set it through the profile (yolo config-ref: profile/providers)",
		"lasts only until the next boot"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the capture must say it refused the key (%q):\n%s", want, out.String())
		}
	}
	log, _ := os.ReadFile(filepath.Join(dir, capturelog.FileName))
	if want := `claude/settings capture: x env.CLAUDE_CODE_USE_BEDROCK = "1": edited in the jail and not captured`; !strings.Contains(string(log), want) {
		t.Errorf("capture.log is missing %q:\n%s", want, log)
	}
}

func TestTheTeardownCaptureNeverRecordsAProviderKey(t *testing.T) {
	withScratchHome(t)
	ws := t.TempDir()
	prism := filepath.Join(ws, ".yolo", "prism")
	writeFile(t, filepath.Join(ws, ".yolo", "home", "claude", "settings.json"),
		`{"model":"base","env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/gw"}}`)
	writeFile(t, filepath.Join(prism, "claude-settings.last_render"), `{"model":"base"}`)
	var warned []string
	captureOnTerminateIn(ws, paths.LegacyHomeState(ws), "podman", func(s string) { warned = append(warned, s) })
	overlay, err := os.ReadFile(filepath.Join(prism, "claude-settings.overlay.json"))
	if err != nil {
		t.Fatalf("the teardown capture wrote no overlay (warnings %v): %v", warned, err)
	}
	if strings.Contains(string(overlay), "ANTHROPIC_BASE_URL") {
		t.Errorf("the teardown capture recorded a provider key: %s", overlay)
	}
	if got := strings.Join(warned, "\n"); !strings.Contains(got, "not captured: env.ANTHROPIC_BASE_URL") {
		t.Errorf("the teardown must say it refused the key:\n%s", got)
	}
}
