package cli

// hostplatformswitch_test.go pins PP-D1 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29) at `yolo host --`: the real ~/.claude/settings.json turning on
// CLAUDE_CODE_USE_BEDROCK while claude's selected provider is not Bedrock is named in one line
// with both fixes, and the launch runs. Through hostMain to the exec, so deleting the call in
// hostExec fails here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLaunchNamesAUsersOwnBedrockSwitch(t *testing.T) {
	withSwitch := func(value string) func(string) {
		return func(home string) {
			p := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(`{"env": {"CLAUDE_CODE_USE_BEDROCK": `+value+`}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	const line = "yolo host: claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which puts claude " +
		"on its own \"aws-bedrock\" client, but no \"aws-bedrock\" provider is selected for claude, so yolo " +
		"delivers it none of that platform's credentials: select one (-p bedrock), or remove " +
		"CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json (yolo leaves it alone)."

	rc, env, errs := hostGateRunIn(t, claudeAlone, nil, nil, "claude", withSwitch(`"1"`))
	if rc != 0 || env == nil {
		t.Fatalf("the line is a disclosure, never a refusal: rc=%d\n%s", rc, errs)
	}
	if strings.Count(errs, line) != 1 {
		t.Errorf("an unprofiled claude with the user's own Bedrock switch must be told once:\n%s", errs)
	}

	// Served: claude on bedrock, whose provider is Bedrock. And off: a switch reading false.
	for _, tc := range []struct {
		name, value string
		flags       []string
	}{
		{"claude on bedrock", `"1"`, []string{"-p", "bedrock"}},
		{"a switch that is off", `false`, nil},
	} {
		_, _, errs := hostGateRunIn(t, claudeAlone, nil, tc.flags, "claude", withSwitch(tc.value))
		if strings.Contains(errs, "sets CLAUDE_CODE_USE_BEDROCK") {
			t.Errorf("%s: nothing to name:\n%s", tc.name, errs)
		}
	}
}
