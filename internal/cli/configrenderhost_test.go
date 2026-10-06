package cli

// configrenderhost_test.go pins docs/plans/notch-convergence.md item 23 (row D5): `yolo config
// render --at host` previews exactly what `yolo host apply --assert` writes, byte for byte.
// MEASURED 2026-09-27 before the fix: the preview showed claude's autonomous
// `additionalDirectories ["/"]` while host apply wrote the guarded `[]`.
//
// Every test here goes through configRunW, the front door, so routing the host notch back
// through the jail preview's loop (deleting configRender's branch) fails them. Every config
// declares `host_management: "own"`, the one contract that renders since the `assert`
// retirement (OQ-CO14): under the unset key (`none`) there is no host write to preview.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jailPreviewTargetForTest is a JAIL target for a scratch workspace, for the tests that
// exercise the jail preview's composition. They used to pass hostTargetForTest(), which was
// the jail preview only because the host notch had none of its own; since item 23 a host
// target previews the host apply instead.
func jailPreviewTargetForTest(t *testing.T) configTarget {
	t.Helper()
	t.Setenv("YOLO_RUNTIME", "podman") // no platform probe for the non-local jail's layout
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return jailConfigTarget(ws, "the test")
}

// hostRenderHome is a scratch HOME selecting packs under `host_management: "own"`, with the
// declared binaries stubbed so host apply's dependency pre-flight has nothing to install.
func hostRenderHome(t *testing.T, packs string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USE_PROFILES", "")
	t.Chdir(t.TempDir())
	selectPacksWith(t, home, packs, `,"host_management":"own"`)
	return home
}

// previewBody is the file body of a one-surface `config render` output: everything after the
// `# agent/name → path` header line.
func previewBody(t *testing.T, out string) string {
	t.Helper()
	header, body, ok := strings.Cut(out, "\n")
	if !ok || !strings.HasPrefix(header, "# ") {
		t.Fatalf("render output has no header line:\n%s", out)
	}
	return body
}

// Both host WRITERS, under `own`: a surface declaring no mode (claude/settings) composes
// through the stateful writer, and one declaring `rmw` (claude/config) through the rmw writer
// `assert` used to run for every surface. The preview must be each one's own bytes. The two
// cells were `assert` and `own` over claude/settings until the `assert` retirement (OQ-CO14).
func TestConfigRenderAtHostIsWhatHostApplyWrites(t *testing.T) {
	for _, c := range []struct{ writer, surface, rel string }{
		{"stateful", "claude/settings", filepath.Join(".claude", "settings.json")},
		{"rmw", "claude/config", ".claude.json"},
	} {
		t.Run(c.writer, func(t *testing.T) { configRenderAtHostIsWhatHostApplyWrites(t, c.surface, c.rel) })
	}
}

func configRenderAtHostIsWhatHostApplyWrites(t *testing.T, surface, rel string) {
	home := hostRenderHome(t, `"claude"`)
	// A user key the write keeps, so the comparison covers a pre-existing file too.
	path := filepath.Join(home, rel)
	writeFile(t, path, `{"theme": "the users own"}`)

	rc, out, errs := runConfigVerb(t, "render", surface, "--at", "host")
	if rc != 0 {
		t.Fatalf("config render --at host rc=%d\n%s%s", rc, out, errs)
	}
	preview := previewBody(t, out)
	if !strings.Contains(out, "# "+surface+" → "+path) {
		t.Errorf("the header must name the real-home destination %s:\n%s", path, out)
	}
	// The measured defect: the autonomous posture's `/` must not be in a host preview.
	if strings.Contains(preview, `"/"`) {
		t.Errorf("the host preview shows the jail's autonomous additionalDirectories:\n%s", preview)
	}

	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != preview {
		t.Errorf("the preview and the write disagree.\npreview:\n%s\nwritten:\n%s", preview, written)
	}
	if !strings.Contains(preview, "the users own") {
		t.Errorf("the preview dropped the user's own key, which the write keeps:\n%s", preview)
	}
}

// --explain at the host prints the per-key record host apply keeps, and the record it prints
// is the one the write then stores.
func TestConfigRenderAtHostExplainIsTheRecordHostApplyKeeps(t *testing.T) {
	hostRenderHome(t, `"claude"`)
	rc, out, errs := runConfigVerb(t, "render", "claude/settings", "--at", "host", "--explain")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out, errs)
	}
	if !strings.Contains(out, "as `yolo host apply --assert` records it") {
		t.Errorf("the explain header must say whose record it is:\n%s", out)
	}
	if !hasLine(out, "permissions", "managed") {
		t.Errorf("the guarded posture's permissions key must be attributed to managed:\n%s", out)
	}
}

// A surface the host render refuses has no content, and the preview says host apply's own
// sentence rather than printing a file nothing writes.
func TestConfigRenderAtHostNamesAnAgentNoConfiguredPackRenders(t *testing.T) {
	hostRenderHome(t, `"claude"`)
	rc, out, errs := runConfigVerb(t, "render", "codex", "--at", "host")
	if rc == 0 || out != "" {
		t.Fatalf("an agent no configured pack renders at the host must refuse: rc=%d\n%s", rc, out)
	}
	if !strings.Contains(errs, `no surfaces for agent "codex" at the host notch (known: claude`) {
		t.Errorf("the refusal must name the agents the host render knows:\n%s", errs)
	}
}

// The jail preview is untouched: with the same packs, the jail target still shows the
// autonomous posture, which is what the jail writes.
func TestConfigRenderAtJailStillPreviewsTheJailsPosture(t *testing.T) {
	hostRenderHome(t, `"claude"`)
	var out, errw bytes.Buffer
	if rc := configRender(jailPreviewTargetForTest(t), []string{"claude/settings"}, &out, &errw,
		false); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), `"/"`) {
		t.Errorf("the jail preview must still show the autonomous additionalDirectories:\n%s", out.String())
	}
}

// THE PREVIEW NEVER FETCHES, and says so where that makes it differ from host apply. A git
// pack nobody fetched yet is the one input the two resolve differently: `yolo host apply`
// fetches it first (TestHostApplyFetchesANeverInstalledGitPack), the preview reads the store
// as it stands. So the preview must neither fetch nor predict the apply's refusal of an
// incomplete set — which it used to, for a pack the apply would simply fetch and render.
func TestConfigRenderAtHostSaysHostApplyFetchesAPackTheStoreLacks(t *testing.T) {
	neverInstalledGitPackHome(t, `,"host_management":"own"`)
	rc, out, errs := runConfigVerb(t, "render", "claude/settings", "--at", "host")
	if rc != 0 {
		t.Fatalf("the resolvable packs still render: rc=%d\n%s%s", rc, out, errs)
	}
	if n := mirrorCount(t); n != 0 {
		t.Errorf("a read-only preview fetched %d repositories", n)
	}
	if !strings.Contains(errs, "not fetched yet: gp (") ||
		!strings.Contains(errs, "`yolo host apply` fetches a git pack first") {
		t.Errorf("the preview must say host apply fetches the pack it could not show:\n%s", errs)
	}
	if strings.Contains(errs, "refuses an incomplete pack set") {
		t.Errorf("host apply fetches this pack rather than refusing, so the preview must not "+
			"predict a refusal:\n%s", errs)
	}
}
