package cli

// applyhostmcp_test.go is the COMMAND-LEVEL half of "a pack can install Claude MCP servers
// on the host": what `apply --host` prints, and the confirmation it demands before it can
// destroy something.
//
// The render-level behavior (the ${workspace} prune, the round-trip guarantee, unresolved
// ${VAR}s) is pinned in internal/entrypoint/hostmcp_test.go. What can only be tested HERE is
// the one-way door: whether the command writes at all, and on whose say-so. Maintainer ruling
// (2026-08-02): warn-and-confirm, not warn-and-refuse — and fail-closed on a stdin nobody is
// holding, matching `pack install`'s host-access approval.
//
// Every test uses a t.TempDir() home. The real $HOME is never read or written.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpContributorPackJSON is a user's own pack contributing one MCP server to claude/config —
// the handoff's reproduction, as a pack manifest. The stdio form, so it COLLIDES with a
// pre-existing http entry of the same name (every key an add, so only entry-level detection
// sees it).
const mcpContributorPackJSON = `{"name":"matt-mcp","contributes":[
  {"kind":"config-overlay","surface":"claude/config",
   "config":{"managed":{"mcpServers":{"tavily":{"command":"npx",
     "args":["-y","tavily-mcp@latest"],"env":{"TAVILY_API_KEY":"${TAVILY_API_KEY}"}}}}}}]}`

// mcpURLPackJSON contributes the http form with a ${VAR} in the url — the entry that would
// otherwise be written literally and 401 with no warning.
const mcpURLPackJSON = `{"name":"matt-mcp","contributes":[
  {"kind":"config-overlay","surface":"claude/config",
   "config":{"managed":{"mcpServers":{"tavily":{"type":"http",
     "url":"https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"}}}}}]}`

// hostMCPFixture points a throwaway $HOME at a config selecting the SHIPPED claude pack plus
// a contributor pack, and returns the home. Selecting claude by bare name is what makes this
// the real end-to-end shape: the surface being overlaid is the one yolo ships.
func hostMCPFixture(t *testing.T, contributorJSON string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "matt-mcp")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(packDir, "pack.json"), contributorJSON)

	cfg := `{"packs":["claude",{"source":"file://` + packDir + `","name":"matt-mcp"}]}`
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE GOAL, end to end: `apply --host --assert` puts the pack's MCP server in the real
// ~/.claude.json, and the pruned projects.${workspace}.* keys are NAMED in the output.
func TestApplyHostInstallsMCPServerAndNamesPrunedKeys(t *testing.T) {
	home := hostMCPFixture(t, mcpURLPackJSON)

	var out, errw bytes.Buffer
	// stdin nil: a clean home loses nothing, so no confirmation is needed and fail-closed
	// stdin must not block it. That is itself the assertion — an rc of 1 here would mean the
	// gate fires on a run with nothing to lose.
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("apply --host --assert rc=%d (a clean home has nothing to confirm)\n%s\n%s",
			rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()

	if !strings.Contains(report, "claude/config") || !strings.Contains(report, "rendered") {
		t.Errorf("claude/config must report as rendered:\n%s", report)
	}
	for _, want := range []string{
		"projects.${workspace}.hasTrustDialogAccepted",
		"projects.${workspace}.enableAllProjectMcpServers",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the pruned key %q must be NAMED in the output, not silently dropped:\n%s",
				want, report)
		}
	}
	// The ${VAR} warning was REMOVED by ruling (2026-08-03): yolo resolves no variables at
	// either notch, so a literal reference is the expected content rather than a hazard, and
	// the old message's "put the value in the file directly" was advice to inline a live
	// credential. Asserted as ABSENT so the removal cannot silently regress.
	if strings.Contains(report, "written LITERALLY") {
		t.Errorf("the ${VAR} warning is gone by ruling; still present:\n%s", report)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("~/.claude.json was never created — the gap this fixes: %v", err)
	}
	if !strings.Contains(string(data), "mcp.tavily.com") {
		t.Errorf("the pack's MCP server did not land:\n%s", data)
	}
	if strings.Contains(string(data), "${workspace}") {
		t.Errorf("the placeholder reached the real home:\n%s", data)
	}
}

// A SECOND --assert is byte-identical. Idempotence at the command level, which is what
// "re-running apply does not churn my dotfiles" means.
func TestApplyHostSecondAssertIsByteIdentical(t *testing.T) {
	home := hostMCPFixture(t, mcpURLPackJSON)
	path := filepath.Join(home, ".claude.json")

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("second assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a second --assert changed the file:\n--- first\n%s\n--- second\n%s",
			first, second)
	}
}

// THE ONE-WAY DOOR, fail-closed. A pre-existing hand-added server the pack would mangle, on a
// first apply, with NO stdin: the command must refuse to write. A scripted or CI run with
// nobody to answer must not destroy a server by default — pack.go's contract, applied here.
func TestApplyHostFirstApplyFailsClosedWithoutStdin(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	path := filepath.Join(home, ".claude.json")
	original := `{"numStartups":9,"mcpServers":{"tavily":{"type":"http",` +
		`"url":"https://mcp.tavily.com/mcp/?tavilyApiKey=SECRET"}}}`
	writeFile(t, path, original)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc == 0 {
		t.Fatalf("with no stdin to confirm, a destructive first apply must NOT proceed\n%s%s",
			out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{"tavily", "not confirmed", "nothing was written"} {
		if !strings.Contains(strings.ToLower(report), strings.ToLower(want)) {
			t.Errorf("the refusal must contain %q:\n%s", want, report)
		}
	}
	// And it really wrote nothing: the user's entry is intact, byte for byte.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("an unconfirmed apply modified the file:\n--- before\n%s\n--- after\n%s",
			original, after)
	}
}

// The same case with `y` on stdin PROCEEDS — warn-and-confirm, not warn-and-refuse. A refusal
// would leave the user no path forward short of hand-editing the file yolo is about to manage.
func TestApplyHostFirstApplyProceedsOnConfirm(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	path := filepath.Join(home, ".claude.json")
	writeFile(t, path, `{"numStartups":9,"mcpServers":{"tavily":{"type":"http",`+
		`"url":"https://mcp.tavily.com/mcp/?tavilyApiKey=SECRET"}}}`)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("a confirmed apply must proceed, rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	// The user was told WHAT would be lost, before the prompt.
	if !strings.Contains(report, "tavily") {
		t.Errorf("the warning must name the entry at risk:\n%s", report)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tavily-mcp@latest") {
		t.Errorf("after confirming, the pack's declaration must land:\n%s", data)
	}
	// The user's unrelated key survives regardless — rmw touches only declared keys.
	if !strings.Contains(string(data), "numStartups") {
		t.Errorf("an unrelated key was lost:\n%s", data)
	}
}

// `n` on stdin ABORTS and writes nothing — the explicit-decline path, distinct from
// no-stdin-at-all but with the same outcome.
func TestApplyHostFirstApplyAbortsOnDecline(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`
	writeFile(t, path, original)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, strings.NewReader("n\n")); rc == 0 {
		t.Fatalf("a declined apply must not proceed\n%s%s", out.String(), errw.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("a declined apply modified the file:\n%s", after)
	}
}

// OBSERVE NEVER PROMPTS. It writes nothing, so there is nothing to confirm — it reports what
// an --assert would damage and returns 0 even with no stdin. This is what gives the user the
// information before the prompt ever appears.
func TestApplyHostObserveReportsWithoutPrompting(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`
	writeFile(t, path, original)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("observe must never fail on a confirmation it does not need, rc=%d\n%s%s",
			rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	if strings.Contains(report, "[y/N]") {
		t.Errorf("observe must not prompt — it writes nothing:\n%s", report)
	}
	if !strings.Contains(report, "tavily") {
		t.Errorf("observe must REPORT what an --assert would damage:\n%s", report)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Errorf("observe wrote to the file:\n%s", after)
	}
}

// A SECOND apply into a home yolo already manages does NOT prompt again, even when it drops
// something. Once the user has opted in, wholesale regeneration is the documented policy —
// re-confirming forever would be the "trains people to hit y blind" failure the gate exists to
// avoid.
func TestApplyHostSecondApplyDoesNotPromptAgain(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	path := filepath.Join(home, ".claude.json")

	// First apply into a clean home: nothing to lose, so it proceeds with no stdin.
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// Now the user hand-adds a colliding http entry — the very thing the ruling says they
	// give up by managing mcpServers through yolo.
	writeFile(t, path, `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`)

	out.Reset()
	errw.Reset()
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("a home yolo already manages must not re-prompt, rc=%d\n%s%s",
			rc, out.String(), errw.String())
	}
	// Still REPORTED, though — the drop is never silent, it just is not gated any more.
	if report := out.String() + errw.String(); !strings.Contains(report, "tavily") {
		t.Errorf("the drop must still be reported even when it is not gated:\n%s", report)
	}
}
