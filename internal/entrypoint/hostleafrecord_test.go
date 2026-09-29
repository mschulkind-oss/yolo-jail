package entrypoint

// hostleafrecord_test.go pins HC-D25 (docs/design/host-computed-layer.md, revising HC-D10 rule 4)
// through RenderHostPack, the one host entry, over the real claude pack: a leaf the settings derive
// asserted into the real ~/.claude/settings.json is cleared by the apply after the derive stops
// asserting it, when the file still holds yolo's value, and a value of the user's is never
// touched. The measured failure: `yolo host apply` on `bedrock` wrote CLAUDE_CODE_USE_BEDROCK,
// moving claude to `codex` and applying again left it, and every later launch then ran claude in
// Bedrock mode with no AWS credential while PP-D1's line blamed the user for yolo's write.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// applyClaudeSettings renders claude/settings into home under ownership with claude on profile
// ("" for none), and returns the file's `env` block.
func applyClaudeSettings(t *testing.T, home string, ownership render.HostOwnership, profile string) map[string]any {
	t.Helper()
	use := map[string]string{}
	if profile != "" {
		use["claude"] = profile
	}
	in := hostTestInputs(t, testPacksForAgent(t, "claude"), use, nil, nil)
	if r := hostRenderWith(t, home, ownership, in, "claude", "claude/settings"); r.Action == "" ||
		strings.HasPrefix(r.Action, "refused") {
		t.Fatalf("claude/settings on %q: %q", profile, r.Action)
	}
	env, _ := decodeJSONFile(t, filepath.Join(home, ".claude", "settings.json"))["env"].(map[string]any)
	return env
}

func TestHostApplyClearsTheBedrockSwitchItWroteWhenClaudeLeavesBedrock(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			writeTestFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env": {"MY_VAR": "x"}}`)
			if env := applyClaudeSettings(t, home, ownership, "bedrock"); env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
				t.Fatalf("claude on bedrock: the switch must land in the real file (providers.md#pv-d8): %v", env)
			}
			for _, profile := range []string{"codex", ""} {
				env := applyClaudeSettings(t, home, ownership, profile)
				if _, set := env["CLAUDE_CODE_USE_BEDROCK"]; set {
					t.Errorf("claude moved to %q: the apply left the Bedrock switch it wrote: %v", profile, env)
				}
				if env["MY_VAR"] != "x" {
					t.Errorf("claude moved to %q: a variable of yours changed: %v", profile, env)
				}
			}
			// Back on bedrock, the switch returns: the record forgot the clear, and the edge re-arms.
			if env := applyClaudeSettings(t, home, ownership, "bedrock"); env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
				t.Errorf("back on bedrock the switch must be written again: %v", env)
			}
		})
	}
}

// YOLO DELETES NOTHING IT DID NOT WRITE (PP-D1): a switch the user wrote before yolo ever asserted
// it, and one the user changed after yolo wrote it, both survive the apply that moves claude off
// Bedrock. The record claims a leaf only when yolo's write is what put its value there.
func TestHostApplyKeepsABedrockSwitchTheUserWrote(t *testing.T) {
	t.Run("written before yolo asserted it", func(t *testing.T) {
		t.Setenv("YOLO_CTX_ROOT", t.TempDir())
		home := t.TempDir()
		writeTestFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
		applyClaudeSettings(t, home, render.OwnershipAssert, "bedrock")
		if env := applyClaudeSettings(t, home, render.OwnershipAssert, "codex"); env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
			t.Errorf("the user's own switch must survive: %v", env)
		}
	})
	t.Run("changed after yolo wrote it", func(t *testing.T) {
		t.Setenv("YOLO_CTX_ROOT", t.TempDir())
		home := t.TempDir()
		settings := filepath.Join(home, ".claude", "settings.json")
		applyClaudeSettings(t, home, render.OwnershipAssert, "bedrock")
		doc := decodeJSONFile(t, settings)
		doc["env"].(map[string]any)["CLAUDE_CODE_USE_BEDROCK"] = "true"
		raw, _ := json.Marshal(doc)
		writeTestFile(t, settings, string(raw))
		if env := applyClaudeSettings(t, home, render.OwnershipAssert, "codex"); env["CLAUDE_CODE_USE_BEDROCK"] != "true" {
			t.Errorf("a value the user changed is theirs and must survive: %v", env)
		}
	})
}

// THE RECORD IS WRITTEN WHERE A LAUNCH READS IT (render.Target.LeafRecordPath), naming the leaf by
// its pointer, and it is gone once nothing of yolo's is left to clear.
func TestTheHostLeafRecordNamesTheSwitchYoloWrote(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	rec := render.Host(home, nil, render.OwnershipAssert).LeafRecordPath("claude", "settings")
	applyClaudeSettings(t, home, render.OwnershipAssert, "bedrock")
	raw, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("the apply on bedrock must record the leaf it wrote: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || got["/env/CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Fatalf("the record must name /env/CLAUDE_CODE_USE_BEDROCK = \"1\": %s", raw)
	}
	applyClaudeSettings(t, home, render.OwnershipAssert, "")
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(rec)
		t.Errorf("with nothing of yolo's left, the record must be gone: %v\n%s", err, raw)
	}

	// A revert ends the relationship, and the record with it.
	applyClaudeSettings(t, home, render.OwnershipAssert, "bedrock")
	if _, err := RevertHostRender(testPacksForAgent(t, "claude"), home, false); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		t.Errorf("a revert must remove the computed-leaf record: %v", err)
	}
}

// THE LSP SWITCH, the leaf rule 4 used to name as the one that stays: with the last LSP server
// gone, the apply removes the ENABLE_LSP_TOOL it wrote, and the user's own variable stays.
func TestHostApplyClearsTheLSPSwitchWithTheLastServer(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	writeTestFile(t, settings, `{"env": {"MY_VAR": "x"}}`)
	packs := testPacksForAgent(t, "claude")
	for _, lsp := range []map[string]any{{"gopls": map[string]any{"command": "gopls"}}, nil} {
		in := hostTestInputs(t, packs, nil, nil, lsp)
		if r := hostRenderWith(t, home, render.OwnershipAssert, in, "claude", "claude/settings"); strings.HasPrefix(r.Action, "refused") {
			t.Fatalf("claude/settings: %q", r.Action)
		}
	}
	env, _ := decodeJSONFile(t, settings)["env"].(map[string]any)
	if _, set := env["ENABLE_LSP_TOOL"]; set || env["MY_VAR"] != "x" {
		t.Errorf("with no LSP server left, ENABLE_LSP_TOOL must go and MY_VAR stay: %v", env)
	}
}

// EVERY COMPUTED LEAF, not the switch alone, and every shape of one: the codex profile's picker
// keys in claude/settings are a scalar (enforceAvailableModels), an array (availableModels) and an
// object the derive does not declare in full (modelPicker), which "leave with the profile" in a
// jail (providers.md, the deselection note on claude). The render entry clears each the same way.
// (The CLI's host composition leaves claude's codex profile out, since it needs the wire bridge;
// this pins the render rule over the richest leaves claude's derive returns, not a CLI path.)
func TestHostApplyClearsTheCodexPickerWhenClaudeLeavesCodex(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			settings := filepath.Join(home, ".claude", "settings.json")
			writeTestFile(t, settings, `{"theme": "dark"}`)
			applyClaudeSettings(t, home, ownership, "codex")
			if doc := decodeJSONFile(t, settings); doc["enforceAvailableModels"] != true {
				t.Fatalf("claude on codex: the picker keys must land: %v", doc)
			}
			applyClaudeSettings(t, home, ownership, "")
			doc := decodeJSONFile(t, settings)
			for _, k := range []string{"availableModels", "enforceAvailableModels"} {
				if _, set := doc[k]; set {
					t.Errorf("claude off codex: the apply left %s: %v", k, doc)
				}
			}
			if picker, _ := doc["modelPicker"].(map[string]any); len(picker) != 0 {
				t.Errorf("claude off codex: the apply left the modelPicker entries: %v", picker)
			}
			if doc["theme"] != "dark" {
				t.Errorf("a key of yours changed: %v", doc)
			}
		})
	}
}
