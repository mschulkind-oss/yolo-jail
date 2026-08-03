package integration

import (
	"fmt"
	"strings"
	"testing"
)

// Agent library-model tests. They prove the selectable-agent surface: each
// agent installs on first use (lazy launcher), reports a version, and gets its
// config and briefing generated — all credential-free (no authenticated task
// runs).
//
// Every test drives a real container, so each calls requireJail(t) first, which
// also gates them out of `go test -short`. No test here calls t.Parallel():
// these are network-heavy (npm/go/native-installer fetches on first use) and can
// be per-arch flaky. Per the CI policy (ci.yml), a red cell in one arch means
// gating that agent out of that arch's matrix — not weakening the assertion here.

// TestAgentToolsAvailable confirms codex and copilot are both present inside a jail whose
// `packs` name them.
func TestAgentToolsAvailable(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "codex", "copilot")
	r := runYolo(t, dir, "codex --version && copilot --version")
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
	dir := writeProjectWithPacks(t, `{}`, "copilot")
	r := runYoloDirect(t, dir, "copilot", "--version")
	if r.rc != 0 {
		t.Fatalf("copilot --version failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
}

// packCase describes one shipped agent PACK: its pack name, the binary its `install`
// declaration provides, that binary's version flag, the config file its `surfaces` render,
// and an auto-approve marker expected inside that file.
type packCase struct {
	pack       string
	binary     string
	versionArg string
	configRel  string
	marker     string
}

// packMatrix has one row per shipped agent pack; the subtest name is the pack name.
//
// Markers are the auto-approve settings each pack's surfaces assert (claude acceptEdits,
// copilot yolo, opencode allow, pi defaultProjectTrust, codex danger-full-access). They
// come from the pack's `managed`/`defaults` layers — there is no per-agent config generator
// any more.
var packMatrix = []packCase{
	{"claude", "claude", "--version", ".claude/settings.json", "acceptEdits"},
	{"copilot", "copilot", "--version", ".copilot/config.json", "yolo"},
	{"opencode", "opencode", "--version", ".config/opencode/opencode.json", "allow"},
	{"pi", "pi", "--version", ".pi/agent/settings.json", "defaultProjectTrust"},
	{"codex", "codex", "--version", ".codex/config.toml", "danger-full-access"},
}

// TestPackInstallsVersionsAndConfigures runs, for each shipped agent pack, a single jail
// session that: exercises the lazy launcher's install path via `<bin> --version`; confirms
// the post-install/update stamp file exists (proving the update path ran); and greps the
// generated config for the pack's auto-approve marker (proving the surface rendered).
//
// It is the strongest end-to-end statement that the transition is real: every one of these
// launchers is generated from the pack's `install` declaration, and every config file from
// its `surfaces`, with no Go code naming any of these tools. The matrix entries are PACK
// names now, not agent names — the only registry left is the packs/ directory.
func TestPackInstallsVersionsAndConfigures(t *testing.T) {
	requireJail(t)
	for _, tc := range packMatrix {
		t.Run(tc.pack, func(t *testing.T) {
			requireJail(t)
			dir := writeProjectWithPacks(t, `{}`, tc.pack)
			stamp := "$HOME/.cache/yolo-agent-stamps/" + tc.binary + ".stamp"
			cmd := fmt.Sprintf(
				"%s %s && test -f %s && grep -q '%s' \"$HOME/%s\"",
				tc.binary, tc.versionArg, stamp, tc.marker, tc.configRel,
			)
			r := runYolo(t, dir, cmd)
			if r.rc != 0 {
				t.Fatalf("%s: install/version/config check failed: rc %d\nstdout: %s\nstderr: %s",
					tc.pack, r.rc, r.stdout, r.stderr)
			}
		})
	}
}

// TestPackSelectionPrunesUnselected confirms a codex-only jail installs codex but NOT
// copilot/claude: their lazy launchers under $HOME/.yolo-launchers are absent, and
// copilot's config dir is never generated.
//
// This is now the END-TO-END check on pack OPT-IN, and it is the assertion that would have
// caught the bug the unit tests missed. Embedded packs were briefly staged wholesale and
// filtered afterwards, so every pack rendered in-jail regardless of selection — a real jail
// halted its boot with eleven read-only-filesystem errors from packs nobody asked for. The
// mount is the filter, and this proves it from outside.
//
// The launchers moved out of ~/.yolo-shims (blockers only, first on PATH) into
// ~/.yolo-launchers (lazy installers, last on PATH). Asserting on the OLD path would have
// stayed green for the wrong reason — nothing is ever written there now.
func TestPackSelectionPrunesUnselected(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "codex")
	cmd := strings.Join([]string{
		"codex --version",
		"test -e $HOME/.yolo-launchers/codex",
		"! test -e $HOME/.yolo-launchers/copilot",
		"! test -e $HOME/.yolo-launchers/claude",
		"! test -e $HOME/.yolo-shims/codex",
		"! test -e $HOME/.copilot/config.json",
	}, " && ")
	r := runYolo(t, dir, cmd)
	if r.rc != 0 {
		t.Fatalf("selection pruning failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
}

// TestJailConfigsPresent confirms the persistent per-agent jail configs in the
// shared home are visible inside the jail (copilot config + codex config).
func TestJailConfigsPresent(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, "ls /home/agent/.copilot/config.json && ls /home/agent/.codex/config.toml")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
}
