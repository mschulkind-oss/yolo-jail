package packload

// platformswitch_test.go pins PP-D1's rule (PlatformSwitchConflicts) over the embedded claude
// pack, whose program declares its settings file's CLAUDE_CODE_USE_BEDROCK: when the switch is
// on and the switch's platform is not claude's selection, one conflict; otherwise none. The
// launch call sites are pinned where they act (internal/cli/run, internal/cli).

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, home, body string) {
	t.Helper()
	p := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAUsersOwnSwitchConflictsOnlyWhenTheSelectionDoesNotServeIt(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "openai-auth", "wire-bridge")
	user := userProviders(t, `{"bedrock-eu":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	userProfiles := map[string]UserProfile{"eu": {Provider: "bedrock-eu"},
		"bedrock-bridge": {Provider: "bedrock", Via: "wire-bridge"}}
	conflicts := func(home string, profiles map[string]string) []PlatformSwitchConflict {
		providers, resolved, sel := launchSelection(t, packs, user, userProfiles, profiles)
		return PlatformSwitchConflicts(packs, sel, resolved, providers, home, "")
	}

	home := t.TempDir()
	writeSettings(t, home, `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","OTHER":"x"}}`)
	for _, profiles := range []map[string]string{nil, {"claude": "codex"}} {
		got := conflicts(home, profiles)
		if len(got) != 1 || got[0].Agent != "claude" || got[0].Platform != "aws-bedrock" ||
			got[0].Key != "CLAUDE_CODE_USE_BEDROCK" || got[0].File != "~/.claude/settings.json" {
			t.Fatalf("selection %v with the switch on: conflicts = %+v, want claude's one", profiles, got)
		}
		// The offered fix is a declared profile over a Bedrock provider that routes through no
		// via service (bedrock-bridge would not serve claude's own client).
		if got[0].Profile != "bedrock" {
			t.Errorf("the fix must offer the native profile, got %q", got[0].Profile)
		}
	}
	// Served: the shipped profile, and a user's own Bedrock provider (the platform, not a name).
	for _, profile := range []string{"bedrock", "eu"} {
		if got := conflicts(home, map[string]string{"claude": profile}); len(got) != 0 {
			t.Errorf("claude on %s serves its switch, got %+v", profile, got)
		}
	}
	// Off, absent, or not JSON: nothing.
	for _, body := range []string{`{"env":{"CLAUDE_CODE_USE_BEDROCK":"0"}}`, `{"env":{}}`, `{}`, `not json`} {
		h := t.TempDir()
		writeSettings(t, h, body)
		if got := conflicts(h, nil); len(got) != 0 {
			t.Errorf("settings %s: no switch on, got %+v", body, got)
		}
	}
	if got := conflicts(t.TempDir(), nil); len(got) != 0 {
		t.Errorf("no settings file: nothing, got %+v", got)
	}
	// Every spelling of "on" counts.
	for _, v := range []string{`true`, `1`, `"TRUE"`, `"yes"`, `" on "`} {
		h := t.TempDir()
		writeSettings(t, h, `{"env":{"CLAUDE_CODE_USE_BEDROCK":`+v+`}}`)
		if got := conflicts(h, nil); len(got) != 1 {
			t.Errorf("CLAUDE_CODE_USE_BEDROCK=%s is on, got %+v", v, got)
		}
	}
	// The host notch asks for its one agent only.
	providers, resolved, sel := launchSelection(t, packs, user, userProfiles, nil)
	if got := PlatformSwitchConflicts(packs, sel, resolved, providers, home, "codex"); len(got) != 0 {
		t.Errorf("a launch of codex names no conflict of claude's, got %+v", got)
	}
}
