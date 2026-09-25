package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// renderedBriefing is the briefing exactly as `yolo init` prints it, markup stripped — the
// PRODUCTION render (printBriefing over the embedded briefingContent), so a test reading it
// reads what a host agent reads rather than the source file.
func renderedBriefing(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	printBriefing(&buf, "/ws/yolo-jail.jsonc", false)
	return buf.String()
}

// briefingBullet returns the "•" bullet of the rendered briefing that contains marker,
// from its bullet line to the line before the next bullet or blank line, lowercased.
func briefingBullet(t *testing.T, text, marker string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("the briefing no longer mentions %q — this test has lost its subject and is "+
			"now vacuous; repoint it at wherever the briefing explains blocking", marker)
	}
	start := at
	for start > 0 && !strings.HasPrefix(strings.TrimSpace(lines[start]), "•") {
		start--
	}
	end := at + 1
	for end < len(lines) {
		trimmed := strings.TrimSpace(lines[end])
		if trimmed == "" || strings.HasPrefix(trimmed, "•") {
			break
		}
		end++
	}
	return strings.ToLower(strings.Join(lines[start:end], "\n"))
}

// TestBriefingAgreesWithTheBlockedToolDefault is TestConfigRefAgreesWithTheBlockedToolDefault
// for the OTHER surface that states the default: the briefing `yolo init` prints to a host
// agent. It said "Some tools are blocked (e.g., grep → rg, find → fd)" for three weeks after
// 2026-09-04, when blocking became opt-in through the guardrails pack and
// config.DefaultBlockedTools() went empty — so every agent that ran `yolo init` was told its
// jail refuses two tools that it runs.
//
// Both directions fail here, as in the config-ref test: an empty default may not be described
// as a block and must be stated as none; a non-empty one must be named. And the bullet's
// pointer to the opt-in is checked against the pack it names, so the sentence cannot describe
// a guardrails pack that blocks something else.
func TestBriefingAgreesWithTheBlockedToolDefault(t *testing.T) {
	bullet := briefingBullet(t, renderedBriefing(t), "YOLO_BYPASS_SHIMS")

	defaults := config.DefaultBlockedTools()
	if len(defaults) == 0 {
		for _, stale := range []string{"some tools are blocked", "(e.g., grep"} {
			if strings.Contains(bullet, stale) {
				t.Errorf("the briefing says %q, but the default blocked list is empty — "+
					"blocking is opt-in via the guardrails pack or security.blocked_tools:\n%s",
					stale, bullet)
			}
		}
		if !strings.Contains(bullet, "nothing is blocked by default") {
			t.Errorf("the default blocked list is empty and the briefing does not say so "+
				"plainly — an agent that remembers the old default will assume it holds:\n%s", bullet)
		}
	} else {
		if strings.Contains(bullet, "nothing is blocked by default") {
			t.Errorf("the briefing says nothing is blocked by default, but the default list is %v",
				defaults)
		}
		for _, tool := range defaults {
			if !strings.Contains(bullet, strings.ToLower(tool)) {
				t.Errorf("%q is blocked by default and the briefing's blocking bullet never "+
					"names it:\n%s", tool, bullet)
			}
		}
	}

	// The opt-in the bullet points at must be the pack it describes.
	if !strings.Contains(bullet, `"guardrails"`) {
		t.Fatalf("the blocking bullet no longer names the guardrails pack:\n%s", bullet)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "packs", "guardrails", "pack.json"))
	if err != nil {
		t.Fatalf("the briefing names a guardrails pack the tree does not ship: %v", err)
	}
	var manifest struct {
		Contributes []struct {
			Kind        string `json:"kind"`
			Bin         string `json:"bin"`
			Replacement string `json:"replacement"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode packs/guardrails/pack.json: %v", err)
	}
	blocks := 0
	for _, c := range manifest.Contributes {
		if c.Kind != "blocked-tool" {
			continue
		}
		blocks++
		for _, name := range []string{c.Bin, c.Replacement} {
			if !strings.Contains(bullet, strings.ToLower(name)) {
				t.Errorf("the guardrails pack blocks %s in favor of %s, and the briefing's "+
					"description of it never names %q:\n%s", c.Bin, c.Replacement, name, bullet)
			}
		}
	}
	if blocks == 0 {
		t.Errorf("packs/guardrails declares no blocked-tool contribution, so the briefing's " +
			"description of it describes nothing")
	}
}

// TestBriefingDoesNotClaimGhIsAuthenticated: the briefing told every host agent "GitHub CLI
// (gh) is pre-authenticated", and nothing grants a token — the host's gh credentials do not
// cross into a jail and ~/.config/gh does not exist there
// (docs/design/baked-editor-preference.md OQ-ED4's audit, which found it). An agent that
// believes it would plan work around a `gh` that fails at the first authenticated call.
func TestBriefingDoesNotClaimGhIsAuthenticated(t *testing.T) {
	text := strings.ToLower(renderedBriefing(t))
	if strings.Contains(text, "pre-authenticated") {
		t.Errorf("the briefing claims gh is pre-authenticated; no host credential for it " +
			"reaches the jail")
	}
	bullet := briefingBullet(t, renderedBriefing(t), "GitHub CLI")
	if !strings.Contains(bullet, "not signed in") {
		t.Errorf("the briefing mentions gh without saying it is not signed in — silence reads "+
			"as the old promise:\n%s", bullet)
	}
}
