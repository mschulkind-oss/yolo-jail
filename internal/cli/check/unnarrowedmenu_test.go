package check

// unnarrowedmenu_test.go pins MM-D5's `yolo check` line (docs/design/model-lists-and-pickers.md:
// "opencode cannot shape its menu without refusing, so with the switch off it gets no whitelist,
// and `yolo check` says its menu is then not narrowed"; MM-D29 the mechanism). Every case drives
// sectionPacks over the SHIPPED opencode pack, so deleting the call in packs.go, or the
// `exact_menu_refuses` declaration in packs/opencode/pack.json, turns it red.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// companyPack is a local pack whose only contribution is one `models` entry, as a company ships.
func companyPack(t *testing.T, models string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"name": "company", "contributes": [` + models + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// unnarrowedMenuCheck runs the Packs section over opencode, pi and zai beside the company pack,
// with the user's own profiles, and returns what it printed and how many it graded failed.
func unnarrowedMenuCheck(t *testing.T, models, agent string, set ...string) (string, int) {
	t.Helper()
	packsFixture(t, `{
	  "packs": ["opencode", "pi", "zai", "file://`+companyPack(t, models)+`"],
	  "profiles": {
	    "zai-open": {"provider": "zai", "enforce_models": false},
	    "codex-open": {"provider": "openai-codex", "enforce_models": false}
	  }
	}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, setSelection(agent, set...))
	// A section that stopped before the selection proves nothing about the line, for or against.
	if !strings.Contains(buf.String(), "[PASS] opencode: ships with yolo") {
		t.Fatalf("the Packs section did not load the shipped opencode pack:\n%s", buf.String())
	}
	return buf.String(), r.failed
}

const zaiOnly = `{"kind": "models", "provider": "zai", "only": ["glm-5.3"]}`

// THE LINE: a profile with the switch off, over a list an `only` narrowed, leaves opencode's menu
// unnarrowed, and the check says so, naming the agent, the provider, the profile and the pack
// whose declaration it read. A WARNING, never a failure: the launch starts.
func TestCheckSaysTheSwitchLeavesOpencodesNarrowedMenuUnnarrowed(t *testing.T) {
	out, failed := unnarrowedMenuCheck(t, zaiOnly, "opencode", "zai-open")
	for _, want := range []string{
		`yolo does not narrow opencode's menu for provider "zai" to its list, because profile "zai-open" turns enforce_models off`,
		"pack opencode says opencode's menu can show exactly a list only through a filter that also refuses",
		`drop "enforce_models": false from profile "zai-open"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the check should say %q:\n%s", want, out)
		}
	}
	if failed != 0 {
		t.Errorf("the line must WARN, never fail: the launch does not refuse it:\n%s", out)
	}
}

// ON THE SUBSCRIPTION the whole list is opencode's exact menu with no `only` (its derive writes the
// `openai` whitelist for it whenever the switch is on), so the switch off is said there too: the
// provider opencode's pack names in `exact_menu_refuses.providers`.
func TestCheckSaysTheSwitchLeavesOpencodesSubscriptionMenuUnnarrowed(t *testing.T) {
	out, _ := unnarrowedMenuCheck(t, zaiOnly, "opencode", "codex-open")
	if want := `opencode's menu for provider "openai-codex" to its list, because profile "codex-open"`; !strings.Contains(out, want) {
		t.Errorf("the check should say %q:\n%s", want, out)
	}
}

// NOTHING TO SAY: the switch on (the shipped `zai` profile says nothing, so it is on), a list no
// `only` narrowed (what an `add` shows is OQ-MM1's and refuses nothing either way), and an agent
// whose pack declares no refusing menu (pi narrows by registration, with or without the switch).
func TestCheckSaysNothingWhereTheSwitchDoesNotDecideTheMenu(t *testing.T) {
	for _, tc := range []struct{ name, models, agent, profile string }{
		{"the switch on", zaiOnly, "opencode", "zai"},
		{"a list no only narrowed", `{"kind": "models", "provider": "zai", "add": [{"id": "glm-6", "vendor": "zai"}]}`,
			"opencode", "zai-open"},
		{"an agent that narrows without refusing", zaiOnly, "pi", "zai-open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, failed := unnarrowedMenuCheck(t, tc.models, tc.agent, tc.profile)
			if failed != 0 {
				t.Fatalf("the fixture fails the section, so its silence proves nothing:\n%s", out)
			}
			if strings.Contains(out, "yolo does not narrow") {
				t.Errorf("the check named a menu the switch does not decide:\n%s", out)
			}
		})
	}
}

// AN ACTIVE SET: each entry's own profile governs its provider's row, so opencode on
// [zai, codex-open] is told about the subscription alone, the zai entry's switch being on.
func TestCheckNamesOnlyTheSetEntryWhoseSwitchIsOff(t *testing.T) {
	out, _ := unnarrowedMenuCheck(t, zaiOnly, "opencode", "zai", "codex-open")
	if !strings.Contains(out, `opencode's menu for provider "openai-codex" to its list, because profile "codex-open"`) {
		t.Errorf("the check should name the codex-open entry:\n%s", out)
	}
	if strings.Contains(out, `menu for provider "zai"`) {
		t.Errorf("the zai entry's switch is on, so nothing should be said of it:\n%s", out)
	}
}
