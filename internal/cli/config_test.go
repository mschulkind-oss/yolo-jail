package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
  if ctx.config.extensions then
    local kept = {}
    for _, ext in ipairs(ctx.config.extensions) do
      if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
    end
    ctx.config.extensions = kept
    ctx.stage.exclude("extensions/permission-gate.ts")
  end
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

// A3: `config render claude` composed claude/config (~/.claude.json) even though
// the JAIL NEVER COMPOSES THAT FILE — the boot path writes it via
// writeClaudeJSON's read-modify-write, which is why the manifest labels it
// "unrendered" and ls/diff/reset already skip it. Rendering it printed live agent
// state (machineID, the full mcpServers table, onboarding timestamps) as though it
// were a yolo-composed preview, breaking the §6 promise that what render prints is
// what the jail gets.
func TestConfigRenderSkipsUnrenderedSurfaces(t *testing.T) {
	var out, errw bytes.Buffer
	rc := configRender([]string{"claude"}, &out, &errw, false)
	if rc != 0 {
		t.Fatalf("rc = %d, stderr = %s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "claude/config") {
		t.Errorf("render must skip the unrendered claude/config surface:\n%s", got)
	}
	// The genuinely composed surface must still render.
	if !strings.Contains(got, "claude/settings") {
		t.Errorf("render dropped claude/settings:\n%s", got)
	}
}

// A7: `config render --explain` read the surface's OWN DESTINATION as the `host`
// layer. For a yolo-OWNED surface that destination is yolo's previous output, so
// every key it had written came back labelled `host` — mise's computed [tools]
// table reported as host-provided, and a claude `model` key that exists in no boot
// layer printed as though yolo composed it.
//
// Only two surfaces actually get host bytes at boot (surfaceHasHostLayer, bounded
// by AgentSpec.HostFiles' two entries). Everything else must compose with NO host
// layer, exactly as the jail does.
func TestConfigRenderExplainDoesNotAttributeOwnOutputToHost(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := configRender([]string{"mise", "--explain"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc != 0: %s", errw.String())
	}
	// mise/config has no host layer; its tools table is computed. Whatever the
	// local machine's rendered file contains, nothing may be attributed to `host`.
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "\thost") {
			t.Errorf("mise/config has no host layer, but render attributed a key to it: %q", line)
		}
	}
}

// `config drift` exit codes are the agent-facing interface: 4 no baseline, 0 in
// sync, 3 drifted. These pin them so an agent branching on $? never silently breaks.
func TestConfigDriftExitCodes(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)

	// No baseline yet → exit 4 (cannot determine).
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"drift"}, &out, &errw); rc != 4 {
		t.Fatalf("no baseline should exit 4, got %d\n%s%s", rc, out.String(), errw.String())
	}

	// Freeze a baseline matching the current config → in sync, exit 0.
	writeFile(t, filepath.Join(repo, ".yolo", "config-boot.json"), canonicalWS(t, repo))
	out.Reset()
	errw.Reset()
	if rc := configRunW([]string{"drift"}, &out, &errw); rc != 0 {
		t.Fatalf("matching baseline should exit 0, got %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "In sync") {
		t.Errorf("in-sync output unclear:\n%s", out.String())
	}

	// Edit the config after the baseline → drift, exit 3, diff printed.
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude","codex"]}`)
	out.Reset()
	errw.Reset()
	if rc := configRunW([]string{"drift"}, &out, &errw); rc != 3 {
		t.Fatalf("drift should exit 3, got %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "codex") || !strings.Contains(out.String(), "drifted") {
		t.Errorf("drift output should name the change:\n%s", out.String())
	}
}

// OQ-LP9 R7: `config drift` must NAME the scope it does not compare.
//
// The inherited user scope is launch-frozen — a generated, filtered render taken at launch —
// so a host-side user-config edit is invisible in-jail and undetectable as drift. Before
// OQ-LP9 the host's real config.jsonc was bind-mounted live, so the question never arose.
// Saying "In sync" with no qualifier would now let an agent conclude that nothing changed
// ANYWHERE, and go debug a stale `packs` list as a code problem.
//
// The exit codes are deliberately unchanged: the limit is not itself drift, and an agent
// branching on 0/3/4 must keep working. So this asserts the OUTPUT gained a note while
// TestConfigDriftExitCodes keeps pinning the interface.
func TestConfigDriftNamesTheUserScopeItCannotCompare(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	writeFile(t, filepath.Join(repo, ".yolo", "config-boot.json"), canonicalWS(t, repo))

	// On the HOST the user config is right there and live — no note, nothing to disclose.
	//
	// YOLO_VERSION must be cleared EXPLICITLY: this suite runs inside a jail, so the var is
	// genuinely set in the environment and a "host" case that did not unset it would be
	// testing the in-jail branch under a host label. (Caught by this test on its first run.)
	t.Setenv("YOLO_VERSION", "")
	var hostOut, hostErr bytes.Buffer
	if rc := configRunW([]string{"drift"}, &hostOut, &hostErr); rc != 0 {
		t.Fatalf("host: want exit 0, got %d\n%s%s", rc, hostOut.String(), hostErr.String())
	}
	if strings.Contains(hostOut.String(), "not visible in here") {
		t.Errorf("the in-jail-only note appeared on the HOST, where the user config is live:\n%s",
			hostOut.String())
	}

	// IN A JAIL the note must appear, on the in-sync path...
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	var out, errw bytes.Buffer
	rc := configRunW([]string{"drift"}, &out, &errw)
	if rc != 0 {
		t.Fatalf("in-jail in-sync: want exit 0 (the note must not change the interface), got %d\n%s%s",
			rc, out.String(), errw.String())
	}
	for _, want := range []string{"WORKSPACE config only", "next launch", "config dump"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("in-sync output is missing %q — an agent reading \"In sync\" would "+
				"conclude the user half was checked too:\n%s", want, out.String())
		}
	}

	// ...and on the DRIFTED path, because the limit is a property of the command, not of
	// the result.
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude","codex"]}`)
	out.Reset()
	errw.Reset()
	if rc := configRunW([]string{"drift"}, &out, &errw); rc != 3 {
		t.Fatalf("in-jail drifted: want exit 3, got %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "WORKSPACE config only") {
		t.Errorf("the drifted path lost the note:\n%s", out.String())
	}
}

// `config dump` prints canonical JSON (sorted keys) of the effective config and
// exits 0.
func TestConfigDumpCanonical(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"],"resources":{"pids_limit":4096}}`)
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"dump"}, &out, &errw); rc != 0 {
		t.Fatalf("dump should exit 0, got %d\n%s", rc, errw.String())
	}
	s := out.String()
	if !strings.Contains(s, `"pids_limit": 4096`) || !strings.Contains(s, `"packs"`) {
		t.Errorf("dump did not print the effective config:\n%s", s)
	}
	// Canonical: 2-space indent, so a nested key is indented and the output parses.
	if !strings.Contains(s, "\n  \"packs\"") {
		t.Errorf("dump is not 2-space-indented canonical JSON:\n%s", s)
	}
}

// canonicalWS returns the canonical snapshot JSON of a workspace's config, the form
// WriteWorkspaceBootBaseline writes — so a test can hand-place a matching baseline.
func canonicalWS(t *testing.T, repo string) string {
	t.Helper()
	wsCfg, err := config.LoadWorkspaceConfig(repo, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	j, err := config.SnapshotJSON(wsCfg)
	if err != nil {
		t.Fatal(err)
	}
	return j + "\n"
}
