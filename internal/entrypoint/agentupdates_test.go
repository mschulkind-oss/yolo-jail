package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentupdates_test.go covers the JAIL half of `agent_updates` (OQ-PD12): the precedence
// rule, and the fact that the GENERATOR consults it. The host half — the user-scope key
// and the three sites that put it on the wire — is internal/config's.

// TestAgentUpdatesPrecedence: a specific pack key beats "*", "*" beats absence, and
// absence is TRUE.
func TestAgentUpdatesPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, wire, pack string
		want             bool
	}{
		{"absent", "", "claude", true},
		{"global true", `true`, "claude", true},
		{"global false", `false`, "claude", false},
		{"star only", `{"*": false}`, "claude", false},
		{"specific beats star", `{"*": false, "claude": true}`, "claude", true},
		{"specific beats star, other way", `{"*": true, "claude": false}`, "claude", false},
		{"unlisted pack falls to star", `{"*": false, "claude": true}`, "codex", false},
		{"unlisted pack with no star is open", `{"claude": false}`, "codex", true},
		// Anything the host validator refuses. A launch that got here has already been
		// reported on, and freezing every agent over a malformed value is the wrong
		// direction to fail in.
		{"unparseable", `{oh no`, "claude", true},
		{"wrong shape", `["claude"]`, "claude", true},
		{"non-bool entry falls through to the star", `{"*": false, "claude": "yes"}`, "claude", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentUpdatesValue(tc.wire, tc.pack); got != tc.want {
				t.Errorf("agentUpdatesValue(%q, %q) = %v, want %v",
					tc.wire, tc.pack, got, tc.want)
			}
		})
	}
}

// TestGeneratedLaunchersCarryThePolicy is the CALL-SITE cell, and it is the one that
// matters: agentUpdatesValue could be perfect and unreferenced. It drives
// GenerateAgentLaunchers over two packs under a per-pack policy and reads the flag out of
// the scripts the generator actually wrote.
//
// The two-pack shape is the point. A cell with one pack passes against a generator that
// ignores the pack name and applies the global answer to everything — which is precisely
// the bug a per-pack key can have.
func TestGeneratedLaunchersCarryThePolicy(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	for name, bin := range map[string]string{"frozen": "frozentool", "fresh": "freshtool"} {
		dir := filepath.Join(packRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"` + name + `","contributes":[` +
			`{"kind":"program","bin":"` + bin + `","via":"npm","package":"` + bin + `"}]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	e := NewEnv(map[string]string{
		"JAIL_HOME":           home,
		"YOLO_PACK_ROOT":      packRoot,
		AgentUpdatesEnv:       `{"*": true, "frozen": false}`,
		"YOLO_MISE_TOOLS":     `{}`,
		"NPM_CONFIG_PREFIX":   filepath.Join(home, ".npm-global"),
		"YOLO_BLOCK_CONFIG":   `[]`,
		"YOLO_LSP_SERVERS":    `{}`,
		"YOLO_MCP_SERVERS":    `{}`,
		"YOLO_MCP_PRESETS":    `[]`,
		"YOLO_LSP_GO_INSTALL": "",
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	for bin, wantFlag := range map[string]string{
		"frozentool": "UPDATES_ENABLED=0",
		"freshtool":  "UPDATES_ENABLED=1",
	} {
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
		if err != nil {
			t.Fatalf("reading the %s launcher: %v", bin, err)
		}
		if !strings.Contains(string(body), "\n"+wantFlag+"\n") {
			t.Errorf("%s launcher should carry %s — the policy is per PACK, and a generator "+
				"that read one answer for all of them would pass a single-pack test",
				bin, wantFlag)
		}
	}
}
