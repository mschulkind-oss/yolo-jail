package packload

// platformswitch_test.go pins PP-D1's rule (PlatformSwitchConflicts) over the embedded claude
// pack, whose program declares its settings file's CLAUDE_CODE_USE_BEDROCK: when the switch is
// on and the switch's platform is not claude's selection, one conflict; otherwise none. The
// launch call sites are pinned where they act (internal/cli/run, internal/cli).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
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
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	user := userProviders(t, `{"bedrock-eu":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	userProfiles := map[string]UserProfile{"eu": {Provider: "bedrock-eu"},
		"bedrock-bridge": {Provider: "bedrock", Via: "wire-bridge"}}
	conflicts := func(home string, profiles map[string]string) []PlatformSwitchConflict {
		providers, resolved, sel := launchSelection(t, packs, user, userProfiles, profiles)
		return PlatformSwitchConflicts(packs, sel, resolved, providers, home, "", render.HostLeafWrote(home))
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
	// A SWITCH yolo WROTE (HC-D25): the host's computed-leaf record names the pointer with the
	// value the file holds, so the conflict is yolo's and its line names `yolo host apply`; a
	// record naming another value, or none, leaves the switch the user's. The record is planted
	// where the host's rmw arm writes it, under the owned contract's target: the path is the
	// same whatever the contract (render.HostLeafWrote reads it unstated), so this is also where
	// a record the retired `assert` left on claude/settings sits.
	for _, tc := range []struct {
		file, record string
		yolos        bool
	}{
		{`"1"`, `{"/env/CLAUDE_CODE_USE_BEDROCK": "1"}`, true},
		{`"true"`, `{"/env/CLAUDE_CODE_USE_BEDROCK": "1"}`, false},
		{`"1"`, `{"/env/OTHER": "1"}`, false},
		{`"1"`, ``, false},
	} {
		h := t.TempDir()
		writeSettings(t, h, `{"env":{"CLAUDE_CODE_USE_BEDROCK":`+tc.file+`}}`)
		if tc.record != "" {
			rec := render.Host(h, nil, render.OwnershipOwn).LeafRecordPath("claude", "settings")
			if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rec, []byte(tc.record), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got := conflicts(h, map[string]string{"claude": "codex"})
		if len(got) != 1 || got[0].WrittenByYolo != tc.yolos {
			t.Errorf("file %s, record %q: conflicts %+v, want one with WrittenByYolo=%v", tc.file, tc.record, got, tc.yolos)
			continue
		}
		line := got[0].Line()
		if names := strings.Contains(line, "`yolo host apply` wrote there"); names != tc.yolos {
			t.Errorf("file %s, record %q: the line must name yolo's write exactly when it is yolo's:\n%s",
				tc.file, tc.record, line)
		}
		if leaves := strings.Contains(line, "yolo leaves it alone"); leaves == tc.yolos {
			t.Errorf("file %s, record %q: only the user's own switch is one yolo leaves alone:\n%s",
				tc.file, tc.record, line)
		}
	}

	// The host notch asks for its one agent only.
	providers, resolved, sel := launchSelection(t, packs, user, userProfiles, nil)
	if got := PlatformSwitchConflicts(packs, sel, resolved, providers, home, "codex", nil); len(got) != 0 {
		t.Errorf("a launch of codex names no conflict of claude's, got %+v", got)
	}
}

// A SWITCH yolo WROTE, WHERE NO HOST APPLY RENDERS. Under host_management "none" — the unset
// key since the `assert` retirement (OQ-CO14) — `yolo host apply` refuses, so a line telling the
// user to run it to remove a key the retired `assert` wrote repeated at every launch. Where no
// host apply renders the line names the two removals that run here: by hand, and `--revert`,
// which takes out the leaf the record names. Where one does (`own`), the line is as it was.
func TestAYoloWrittenSwitchLineNamesOnlyARemovalThatRunsHere(t *testing.T) {
	c := PlatformSwitchConflict{Agent: "claude", Platform: "aws-bedrock", File: "~/.claude/settings.json",
		Key: "CLAUDE_CODE_USE_BEDROCK", Profile: "bedrock", WrittenByYolo: true}
	const none = "claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which `yolo host apply` " +
		"wrote there for claude's host selection, and it puts claude on its own \"aws-bedrock\" client, " +
		"but no \"aws-bedrock\" provider is selected for claude, so yolo delivers it none of that " +
		"platform's credentials: select one (-p bedrock), or remove CLAUDE_CODE_USE_BEDROCK from " +
		"~/.claude/settings.json by hand, since no host apply renders here while host_management is " +
		"not \"own\" (`yolo host apply --revert` lists every key yolo wrote, this one included, and " +
		"takes them out with --assert)."
	if got := c.Line(); got != none {
		t.Errorf("no host apply renders here, so the line must not send the user to one:\n got %s\nwant %s",
			got, none)
	}
	if got := c.Line(); strings.Contains(got, "or run `yolo host apply` with") {
		t.Errorf("the line still names an apply that refuses here:\n%s", got)
	}

	c.HostApplyRenders = true
	const own = "claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which `yolo host apply` " +
		"wrote there for claude's host selection, and it puts claude on its own \"aws-bedrock\" client, " +
		"but no \"aws-bedrock\" provider is selected for claude, so yolo delivers it none of that " +
		"platform's credentials: select one (-p bedrock), or run `yolo host apply` with claude on a " +
		"provider of another platform, which removes it."
	if got := c.Line(); got != own {
		t.Errorf("where a host apply renders, the line is the one it was:\n got %s\nwant %s", got, own)
	}

	// The user's own switch reads the same whether or not a host apply renders: yolo leaves it alone.
	c.WrittenByYolo = false
	user := c.Line()
	c.HostApplyRenders = false
	if got := c.Line(); got != user || !strings.Contains(got, "(yolo leaves it alone)") {
		t.Errorf("the user's own switch must read the same under every host_management:\n%s\n%s", user, got)
	}
}
