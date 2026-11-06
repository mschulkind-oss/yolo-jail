package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `null` the user ADDED is not a deletion, and neither the capture nor promote may treat it
// as one (docs/reference/config-ownership.md#CO-D20).
//
// mergeDiff records a key the file gained as `"k": null` with the same token it uses for a
// deleted key. The only source that tells the two apart without lagging is the CURRENT
// surface file: a decoded file holds no tombstones, so a null in it is one the user wrote.
// last_render cannot be that source, because the teardown capture and `yolo config capture`
// rewrite only the overlay, and last_render then lags it by a session.
//
// So these tests drive the flow the reference documents — the session ends, the teardown
// capture folds the jail's edits from the host, then promote runs on the host — through the
// real captureOnTerminate and the real promote call site, with a last_render that predates
// the session. Before the fix promote wrote `env.FOO: null` into the local pack, deleting
// the key at every notch.
//
// Control: `env.BAR`, a live sibling, must still be promoted. The genuine-deletion control
// lives at the engine (agentcfg's TestCaptureKeepsARealDeletionOfARenderedNull), because the
// teardown capture runs with no config layers and so drops a real top-level deletion as
// converged — a separate, known defect.

// teardownThenPromote seeds a session's end state, runs the teardown capture, and returns the
// world ready for promote.
func teardownThenPromote(t *testing.T, lastRender, file string) *promoteWorld {
	t.Helper()
	w := newPromoteWorld(t, `["claude"]`)
	// The home a launch of the selected setup binds, whose store beside it the promote reads
	// (named-setups-plan.md SUP-D35).
	home := selectedHomeFor(w.ws)
	writeFile(t, filepath.Join(home, "claude", "settings.json"), file)
	writeFile(t, w.sidecar("claude", "settings", ".last_render"), lastRender)
	var warned []string
	captureOnTerminateIn(w.ws, home, "podman", func(s string) { warned = append(warned, s) })
	if _, err := os.Stat(w.sidecar("claude", "settings", ".overlay.json")); err != nil {
		t.Fatalf("the teardown capture wrote no overlay (warnings %v): %v", warned, err)
	}
	return w
}

func TestPromoteAfterTeardownNeverWritesAnAddedNullAsADeletion(t *testing.T) {
	w := teardownThenPromote(t,
		`{"model":"base","env":{"BAR":"0"}}`,
		`{"model":"base","statusLine":null,"env":{"BAR":"1","FOO":null}}`)

	if overlay := w.overlayFor("claude", "settings"); strings.Contains(overlay, "statusLine") ||
		strings.Contains(overlay, "FOO") {
		t.Errorf("the capture recorded an added null as a deletion tombstone:\n%s", overlay)
	}

	if out, errw, rc := w.run("claude", "--accept-promotion"); rc != 0 {
		t.Fatalf("rc=%d: %s%s", rc, out, errw)
	}
	decl := localPackManifest(t, w.home)
	contributes, _ := decl["contributes"].([]any)
	if len(contributes) != 1 {
		t.Fatalf("contributes = %v, want one config-overlay", contributes)
	}
	c, _ := contributes[0].(map[string]any)
	cfg, _ := c["config"].(map[string]any)
	managed, _ := cfg["managed"].(map[string]any)
	if v, present := managed["statusLine"]; present {
		t.Errorf("an added null was promoted as a deletion (%v): %v", v, managed)
	}
	env, _ := managed["env"].(map[string]any)
	if v, present := env["FOO"]; present {
		t.Errorf("a nested added null was promoted as a deletion (%v) — the local pack now "+
			"deletes env.FOO at every notch: %v", v, managed)
	}
	if env["BAR"] != "1" {
		t.Errorf("the nested live sibling was not promoted: %v", managed)
	}
}

// `config diff` after the same teardown must not call a key the file HOLDS a deletion, at
// the top level or nested.
func TestDiffAfterTeardownDoesNotReportAnAddedNullAsDeleted(t *testing.T) {
	teardownThenPromote(t,
		`{"model":"base","env":{"BAR":"0"}}`,
		`{"model":"base","statusLine":null,"env":{"BAR":"1","FOO":null}}`)
	var out, errw bytes.Buffer
	if rc := surfaceRunW([]string{"diff", "claude/settings"}, &out, &errw); rc != 0 {
		t.Fatalf("config diff rc=%d: %s%s", rc, out.String(), errw.String())
	}
	got := out.String()

	lines := map[string]string{}
	for _, line := range strings.Split(got, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			lines[f[0]] = line
		}
	}
	if l, ok := lines["statusLine"]; ok {
		t.Errorf("a null the file holds is reported as a captured edit: %q\n%s", l, got)
	}
	l := lines["env"]
	if !strings.Contains(l, `"BAR": "1"`) || strings.Contains(l, "FOO") {
		t.Errorf("env must report the BAR edit and nothing about the added FOO null (line %q):\n%s", l, got)
	}
}
