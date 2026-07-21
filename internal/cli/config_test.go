package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny helper for the fixtures.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const piHostSettings = `{"theme":"dark","defaultModel":"claude-fable-5",` +
	`"extensions":["extensions/permission-gate.ts","extensions/git-helper.ts"]}`

const piGateTransform = `
yolo.transform("pi", function(ctx)
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept
  ctx.stage.exclude("extensions/permission-gate.ts")
end)
`

// withHomeAndCwd points HOME at a scratch home and chdirs to a scratch repo,
// restoring both after the test. Returns (homeDir, repoDir).
func withHomeAndCwd(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return home, repo
}

// TestConfigRenderPiWithTransform is the §6.5 acceptance test AT THE CLI LEVEL:
// a host settings file + a workspace config.lua transform, rendered by
// `yolo config render pi`, drops the permission-gate extension and enforces the
// managed key.
func TestConfigRenderPiWithTransform(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(home, ".pi/agent/settings.json"), piHostSettings)
	writeFile(t, filepath.Join(repo, "yolo-jail.config.lua"), piGateTransform)

	var out, errw bytes.Buffer
	rc := configRunW([]string{"render", "pi"}, &out, &errw)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "permission-gate") {
		t.Errorf("permission-gate should be dropped by the transform:\n%s", got)
	}
	if !strings.Contains(got, "git-helper") {
		t.Errorf("git-helper should survive:\n%s", got)
	}
	if !strings.Contains(got, `"defaultProjectTrust": "always"`) {
		t.Errorf("managed key should be enforced:\n%s", got)
	}
}

// TestConfigRenderExplain shows the winning layer per key, including the
// transform-dropped file exclusion.
func TestConfigRenderExplain(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(home, ".pi/agent/settings.json"), piHostSettings)
	writeFile(t, filepath.Join(repo, "yolo-jail.config.lua"), piGateTransform)

	var out, errw bytes.Buffer
	rc := configRunW([]string{"render", "pi", "--explain"}, &out, &errw)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		"defaultModel\thost",
		"defaultProjectTrust\tmanaged",
		"extensions\ttransform",
		"theme\thost",
		"extensions/permission-gate.ts", // excluded file listed
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--explain output missing %q:\n%s", want, got)
		}
	}
}

// TestConfigRenderExplainColor: with color forced, --explain wraps each layer
// in its distinct hue (managed=green, transform=yellow, host=blue) and the key
// in cyan — the syntax-highlight-provenance from cli-visual-polish. With color
// off the output is plain (the byte-stable path the other tests assert).
func TestConfigRenderExplainColor(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(home, ".pi/agent/settings.json"), piHostSettings)
	writeFile(t, filepath.Join(repo, "yolo-jail.config.lua"), piGateTransform)

	var out bytes.Buffer
	// Drive configRender with color=true (the front door gates this on a real
	// TTY; here we force it to assert the ANSI mapping).
	rc := configRender([]string{"pi", "--explain"}, &out, &bytes.Buffer{}, true)
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	got := out.String()
	// Green for managed, yellow for transform, blue for host, cyan for the key.
	for _, want := range []string{
		"\x1b[32mmanaged\x1b[0m",   // green
		"\x1b[33mtransform\x1b[0m", // yellow
		"\x1b[34mhost\x1b[0m",      // blue
		"\x1b[36m",                 // cyan (keys)
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--explain color output missing %q:\n%q", want, got)
		}
	}
}

// TestConfigRenderNoTransform: with no config.lua present, render is a plain
// merge+enforce and both extensions survive.
func TestConfigRenderNoTransform(t *testing.T) {
	home, _ := withHomeAndCwd(t)
	writeFile(t, filepath.Join(home, ".pi/agent/settings.json"), piHostSettings)

	var out, errw bytes.Buffer
	rc := configRunW([]string{"render", "pi"}, &out, &errw)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "permission-gate") || !strings.Contains(got, "git-helper") {
		t.Errorf("no transform: both extensions should survive:\n%s", got)
	}
}

// TestConfigHelpExitsZero: `config`, `config --help`, `config help` all print
// help to stdout with rc 0 (a self-documenting request, not an error).
func TestConfigHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}, {"-h"}, {"help"}} {
		var out, errw bytes.Buffer
		rc := configRunW(args, &out, &errw)
		if rc != 0 {
			t.Errorf("config %v: rc=%d, want 0", args, rc)
		}
		if !strings.Contains(out.String(), "render <agent>") {
			t.Errorf("config %v: help missing 'render <agent>':\n%s", args, out.String())
		}
	}
}

// TestConfigRenderMisuse: unknown subcommand and missing agent are machine-
// detectable errors (non-zero, message on stderr).
func TestConfigRenderMisuse(t *testing.T) {
	cases := []struct {
		args    []string
		wantRC  int
		wantErr string
	}{
		{[]string{"bogus"}, 2, "unknown subcommand"},
		{[]string{"render"}, 2, "needs an agent"},
		{[]string{"render", "nonesuch"}, 1, "no surfaces for agent"},
		{[]string{"render", "pi", "--bogus"}, 2, "unknown flag"},
	}
	for _, c := range cases {
		var out, errw bytes.Buffer
		rc := configRunW(c.args, &out, &errw)
		if rc != c.wantRC {
			t.Errorf("config %v: rc=%d, want %d", c.args, rc, c.wantRC)
		}
		if !strings.Contains(errw.String(), c.wantErr) {
			t.Errorf("config %v: stderr %q missing %q", c.args, errw.String(), c.wantErr)
		}
	}
}
