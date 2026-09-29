package cli

// hostplatformswitch_test.go pins PP-D1 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29) at `yolo host --`: the real ~/.claude/settings.json turning on
// CLAUDE_CODE_USE_BEDROCK while claude's selected provider is not Bedrock is named in one line
// with both fixes, and the launch runs. Through hostMain to the exec, so deleting the call in
// hostExec fails here.

import (
	"bytes"
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

// A SWITCH yolo WROTE, end to end at the host (HC-D25, PP-D1): `yolo host apply` on bedrock writes
// CLAUDE_CODE_USE_BEDROCK into the real ~/.claude/settings.json (providers.md#pv-d8). A launch on
// another provider while the host selection is still Bedrock names the key as yolo's, and the
// apply that moves claude's host selection off Bedrock removes it, after which no line prints.
// Before, the key stayed forever and the line told the user to remove a key of theirs.
func TestHostApplyRemovesTheBedrockSwitchItWroteAndTheLineSaysWhoWroteIt(t *testing.T) {
	const providers = `"providers": {"bedrock": {"region": "us-west-2"},
	  "mine": {"endpoints": {"anthropic": {"base_url": "https://anthropic.example"}}}},
	  "profiles": {"mine": {"provider": "mine"}}`
	home := hostGateHome(t, `{"packs": ["claude"], "use_profiles": {"claude": "bedrock"}, `+providers+`}`, nil)
	settings := filepath.Join(home, ".claude", "settings.json")
	apply := func() string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
			t.Fatalf("yolo host apply: rc=%d\n%s\n%s", rc, out.String(), errw.String())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := apply(); !strings.Contains(got, `"CLAUDE_CODE_USE_BEDROCK"`) {
		t.Fatalf("host apply on bedrock must write the switch:\n%s", got)
	}

	// The host selection is still Bedrock; this one launch is not.
	const yolos = "yolo host: claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which " +
		"`yolo host apply` wrote there for claude's host selection, and it puts claude on its own " +
		"\"aws-bedrock\" client, but no \"aws-bedrock\" provider is selected for claude, so yolo " +
		"delivers it none of that platform's credentials: select one (-p bedrock), or run `yolo host " +
		"apply` with claude on a provider of another platform, which removes it."
	rc, reached, errs := hostExecRun(t, "claude", "-p", "mine")
	if rc != 0 || !reached {
		t.Fatalf("yolo host -p mine -- claude: rc=%d\n%s", rc, errs)
	}
	if strings.Count(errs, yolos) != 1 || strings.Contains(errs, "yolo leaves it alone") {
		t.Errorf("a switch yolo wrote must be named as yolo's, once:\n%s", errs)
	}

	// The host selection leaves Bedrock: the apply removes what it wrote, and nothing is named.
	userCfg(t, home, `{"packs": ["claude"], "use_profiles": {"claude": "mine"}, `+providers+`}`)
	if got := apply(); strings.Contains(got, `"CLAUDE_CODE_USE_BEDROCK"`) {
		t.Errorf("host apply with claude off Bedrock must remove the switch it wrote:\n%s", got)
	}
	if rc, reached, errs := hostExecRun(t, "claude"); rc != 0 || !reached ||
		strings.Contains(errs, "sets CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("with the switch gone, no line: rc=%d\n%s", rc, errs)
	}
}
