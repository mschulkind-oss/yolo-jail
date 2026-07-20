package integration

import (
	"fmt"
	"strings"
	"testing"
)

// This file ports the agent library-model tests from the Python suite
// (tests/test_jail.py). They prove the selectable-agent surface: each agent
// installs on first use (lazy launcher), reports a version, and gets its config
// and briefing generated — all credential-free (no authenticated task runs).
//
// Every test drives a real container, so each calls requireJail(t) first, which
// also gates them out of `go test -short`. No test here calls t.Parallel():
// these are network-heavy (npm/go/native-installer fetches on first use) and can
// be per-arch flaky. Per the CI policy (ci.yml), a red cell in one arch means
// gating that agent out of that arch's matrix — not weakening the assertion here.

// TestAgentToolsAvailable confirms gemini and copilot are both present inside a
// jail that selects them.
func TestAgentToolsAvailable(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"agents": ["gemini", "copilot"]}`)
	r := runYolo(t, dir, "gemini --version && copilot --version")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
}

// TestAgentToolsAvailableDirect confirms copilot works when invoked directly
// (`yolo run -- copilot --version`), not via a login shell. This is the exact
// path that once failed with "copilot: command not found" because /mise/shims
// was absent from the non-login-shell PATH.
func TestAgentToolsAvailableDirect(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"agents": ["copilot"]}`)
	r := runYoloDirect(t, dir, "copilot", "--version")
	if r.rc != 0 {
		t.Fatalf("copilot --version failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
}

// agentCase describes one selectable agent: the config's agent name, the
// installed binary, its version flag, the in-jail config file it generates, and
// an auto-approve marker expected inside that file.
type agentCase struct {
	agent      string
	binary     string
	versionArg string
	configRel  string
	marker     string
}

// agentMatrix mirrors the Python _AGENT_MATRIX (test_jail.py): one row per
// selectable agent. The subtest name is the agent name (the old pytest id).
// Markers are the auto-approve settings each agent's config generator emits
// (claude acceptEdits, copilot yolo, gemini approvalMode, opencode allow,
// pi defaultProjectTrust, codex danger-full-access).
var agentMatrix = []agentCase{
	{"claude", "claude", "--version", ".claude/settings.json", "acceptEdits"},
	{"copilot", "copilot", "--version", ".copilot/config.json", "yolo"},
	{"gemini", "gemini", "--version", ".gemini/settings.json", "approvalMode"},
	{"opencode", "opencode", "--version", ".config/opencode/opencode.json", "allow"},
	{"pi", "pi", "--version", ".pi/agent/settings.json", "defaultProjectTrust"},
	{"codex", "codex", "--version", ".codex/config.toml", "danger-full-access"},
}

// TestAgentInstallsVersionsAndConfigures runs, for each selectable agent, a
// single jail session that: exercises the lazy launcher's install path via
// `<bin> --version`; confirms the post-install/update stamp file exists (proving
// the update path ran); and greps the generated config for the agent's
// auto-approve marker (proving the entrypoint generated it).
func TestAgentInstallsVersionsAndConfigures(t *testing.T) {
	requireJail(t)
	for _, tc := range agentMatrix {
		t.Run(tc.agent, func(t *testing.T) {
			requireJail(t)
			dir := writeProject(t, fmt.Sprintf(`{"agents": [%q]}`, tc.agent))
			stamp := "$HOME/.cache/yolo-agent-stamps/" + tc.binary + ".stamp"
			cmd := fmt.Sprintf(
				"%s %s && test -f %s && grep -q '%s' \"$HOME/%s\"",
				tc.binary, tc.versionArg, stamp, tc.marker, tc.configRel,
			)
			r := runYolo(t, dir, cmd)
			if r.rc != 0 {
				t.Fatalf("%s: install/version/config check failed: rc %d\nstdout: %s\nstderr: %s",
					tc.agent, r.rc, r.stdout, r.stderr)
			}
		})
	}
}

// TestAgentSelectionPrunesUnselected confirms a gemini-only jail installs gemini
// but NOT copilot/claude: their lazy-launcher shims under $HOME/.yolo-shims are
// absent, and copilot's config dir is never generated — the library model's
// isolation win.
func TestAgentSelectionPrunesUnselected(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"agents": ["gemini"]}`)
	cmd := strings.Join([]string{
		"gemini --version",
		"! test -e $HOME/.yolo-shims/copilot",
		"! test -e $HOME/.yolo-shims/claude",
		"! test -e $HOME/.copilot/config.json",
	}, " && ")
	r := runYolo(t, dir, cmd)
	if r.rc != 0 {
		t.Fatalf("selection pruning failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
}

// TestJailConfigsPresent confirms the persistent per-agent jail configs in the
// shared home are visible inside the jail (copilot config + gemini settings).
func TestJailConfigsPresent(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, "ls /home/agent/.copilot/config.json && ls /home/agent/.gemini/settings.json")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
}
