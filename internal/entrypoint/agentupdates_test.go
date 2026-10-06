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
		// The TIMING values (OQ-PD30) both let the pack move: "next-launch" moves only WHEN its
		// refresh runs. Read as absent, a pack entry of "next-launch" under a "*": false would
		// freeze the very pack the user asked to update in the background.
		{"global next-launch", `"next-launch"`, "claude", true},
		{"global launch", `"launch"`, "claude", true},
		{"next-launch entry beats a frozen star", `{"*": false, "pi": "next-launch"}`, "pi", true},
		{"launch entry beats a frozen star", `{"*": false, "pi": "launch"}`, "pi", true},
		{"next-launch star", `{"*": "next-launch"}`, "claude", true},
		{"an unknown string entry still falls through to the star", `{"*": false, "pi": "later"}`, "pi", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentUpdatesValue(tc.wire, tc.pack); got != tc.want {
				t.Errorf("agentUpdatesValue(%q, %q) = %v, want %v",
					tc.wire, tc.pack, got, tc.want)
			}
		})
	}
}

// TestRefreshTimingPrecedence: when a pack's pre-launch refresh runs, read by agentUpdatesValue's
// own precedence. A specific key beats "*" WHOLE — `true` there is "at launch", not "allowed, and
// take the star's timing" — and anything that is not "next-launch" is at launch, which is the
// default and the direction to fail in: a malformed value must never move work out of the user's
// sight.
func TestRefreshTimingPrecedence(t *testing.T) {
	const at, next = "launch", "next-launch"
	for _, tc := range []struct {
		name, wire, pack, want string
	}{
		{"absent", "", "pi", at},
		{"global true", `true`, "pi", at},
		{"global false", `false`, "pi", at},
		{"global launch", `"launch"`, "pi", at},
		{"global next-launch", `"next-launch"`, "pi", next},
		{"specific next-launch", `{"pi": "next-launch"}`, "pi", next},
		{"another pack's next-launch", `{"pi": "next-launch"}`, "claude", at},
		{"star next-launch", `{"*": "next-launch"}`, "claude", next},
		{"specific true beats a next-launch star", `{"*": "next-launch", "pi": true}`, "pi", at},
		{"specific launch beats a next-launch star", `{"*": "next-launch", "pi": "launch"}`, "pi", at},
		{"specific false beats a next-launch star", `{"*": "next-launch", "pi": false}`, "pi", at},
		{"an unknown entry falls through to the star", `{"*": "next-launch", "pi": "later"}`, "pi", next},
		{"an unknown global string", `"later"`, "pi", at},
		{"unparseable", `{oh no`, "pi", at},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refreshTimingValue(tc.wire, tc.pack); got != tc.want {
				t.Errorf("refreshTimingValue(%q, %q) = %q, want %q", tc.wire, tc.pack, got, tc.want)
			}
		})
	}
}

// TestHostPolicyReadsATimingAsOn: the host floor asks PackPolicyAllows whether a pack may move, and
// a timing value is a yes. The host notch runs no pre-launch refresh, so there "next-launch" means
// exactly what `true` does.
func TestHostPolicyReadsATimingAsOn(t *testing.T) {
	if !PackPolicyAllows(`{"*": false, "pi": "next-launch"}`, "pi") {
		t.Error(`PackPolicyAllows({"*": false, "pi": "next-launch"}, "pi") = false: the host floor ` +
			`would freeze the pack the user asked to update in the background`)
	}
}

// TestGeneratedLaunchersCarryTheRefreshTiming is the CALL-SITE cell for the timing: two packs,
// each declaring a refresh, under a policy that moves one pack's refresh to the background. It
// reads the baked REFRESH_TIMING out of the scripts GenerateAgentLaunchers actually wrote, so it
// goes red if the generator stops setting the timing, or sets one answer for every pack.
func TestGeneratedLaunchersCarryTheRefreshTiming(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	for name, bin := range map[string]string{"bg": "bgtool", "fg": "fgtool"} {
		dir := filepath.Join(packRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"` + name + `","contributes":[` +
			`{"kind":"program","bin":"` + bin + `","via":"npm","package":"` + bin + `",` +
			`"refresh":{"argv":["update"],"lock":".` + name + `-store/.yolo-update.lock"}}]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"YOLO_PACK_ROOT":    packRoot,
		AgentUpdatesEnv:     `{"*": true, "bg": "next-launch"}`,
		"YOLO_MISE_TOOLS":   `{}`,
		"NPM_CONFIG_PREFIX": filepath.Join(home, ".npm-global"),
		"YOLO_BLOCK_CONFIG": `[]`,
		"YOLO_LSP_SERVERS":  `{}`,
		"YOLO_MCP_SERVERS":  `{}`,
		"YOLO_MCP_PRESETS":  `[]`,
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	for bin, want := range map[string]string{"bgtool": "REFRESH_TIMING=next-launch", "fgtool": "REFRESH_TIMING=launch"} {
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
		if err != nil {
			t.Fatalf("reading the %s launcher: %v", bin, err)
		}
		if !strings.Contains(string(body), "\n"+want+"\n") {
			t.Errorf("%s launcher should bake %s — the timing is per PACK", bin, want)
		}
		if !strings.Contains(string(body), "\nUPDATES_ENABLED=1\n") {
			t.Errorf("%s launcher should still let the pack move: a timing is a yes", bin)
		}
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
		"JAIL_HOME":         home,
		"YOLO_PACK_ROOT":    packRoot,
		AgentUpdatesEnv:     `{"*": true, "frozen": false}`,
		"YOLO_MISE_TOOLS":   `{}`,
		"NPM_CONFIG_PREFIX": filepath.Join(home, ".npm-global"),
		"YOLO_BLOCK_CONFIG": `[]`,
		"YOLO_LSP_SERVERS":  `{}`,
		"YOLO_MCP_SERVERS":  `{}`,
		"YOLO_MCP_PRESETS":  `[]`,
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
