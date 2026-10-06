package entrypoint

// hostrevertvalues_test.go pins CO-D12 to CO-D14 (docs/design/config-ownership-and-promotion.md
// §13): `yolo host apply --revert` withdraws the VALUES yolo wrote, never a whole key holding the
// user's own beside them, and it forgets the records an owned apply would otherwise replay.
//
// The homes are written as the retired `assert` wrote them (asRetiredAssert: every surface through
// the rmw arm), because that is the home OQ-CO14 leaves under `none` — the one a revert under
// `none` is for — and then edited as a user edits after the upgrade, when no apply runs to
// relabel anything. Before CO-D12 the revert removed every top-level key the provenance record
// called yolo's, whatever it held by then.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// assertRender renders one surface of pack as the retired `assert` did, with use as the profile
// selection.
func assertRender(t *testing.T, home, pack, surface string, use map[string]string) {
	t.Helper()
	in := hostTestInputs(t, declaredRMW(t, testPacksForAgent(t, pack), pack, true), use, nil, nil)
	if r := hostRenderWith(t, home, render.OwnershipOwn, in, pack, surface); r.Action == "" ||
		r.Action[:min(len(r.Action), 7)] == "refused" {
		t.Fatalf("%s as assert rendered it: %q", surface, r.Action)
	}
}

// editJSON rewrites a JSON file through edit.
func editJSON(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	doc := decodeJSONFile(t, path)
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, string(raw))
}

// revertHome runs the writing revert over the shipped packs for agent.
func revertHome(t *testing.T, home string, agents ...string) HostRevert {
	t.Helper()
	var packs []*packload.Pack
	for _, a := range agents {
		packs = append(packs, testPacksForAgent(t, a)...)
	}
	rev, err := RevertHostRender(packs, home, false)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	return rev
}

// THE USER'S OWN VALUES INSIDE A TABLE yolo ADDED TO STAY. `assert` on claude's bedrock profile
// wrote CLAUDE_CODE_USE_BEDROCK into an `env` that already held the user's MYVAR, and its record
// labels the whole `env` yolo's (a layer that sets a nested key claims the top-level key); the
// guarded posture's managed `permissions.defaultMode` went in beside it. After the upgrade the user
// adds an `allow` list to `permissions`. The revert takes out the switch and `defaultMode` — the
// values the computed-leaf record and the managed layer say yolo wrote — and leaves MYVAR and the
// user's `allow`. Before CO-D12 it deleted `env` and `permissions` whole.
func TestARevertKeepsTheUsersOwnValuesInsideATableYoloAddedTo(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	writeTestFile(t, settings, `{"env": {"MYVAR": "mine"}}`)
	assertRender(t, home, "claude", "claude/settings", map[string]string{"claude": "bedrock"})
	doc := decodeJSONFile(t, settings)
	env, _ := doc["env"].(map[string]any)
	perms, _ := doc["permissions"].(map[string]any)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["MYVAR"] != "mine" || perms["defaultMode"] != "default" {
		t.Fatalf("fixture premise — the switch and the managed mode beside the user's MYVAR: %v", doc)
	}
	editJSON(t, settings, func(d map[string]any) {
		d["permissions"].(map[string]any)["allow"] = []any{"Bash(ls:*)"}
	})

	rev := revertHome(t, home, "claude")
	got := decodeJSONFile(t, settings)
	env, _ = got["env"].(map[string]any)
	if env["MYVAR"] != "mine" {
		t.Errorf("the revert took the user's own MYVAR with yolo's switch: %v", got)
	}
	if _, left := env["CLAUDE_CODE_USE_BEDROCK"]; left {
		t.Errorf("the revert left the Bedrock switch yolo wrote: %v", got)
	}
	perms, _ = got["permissions"].(map[string]any)
	if allow, _ := perms["allow"].([]any); len(allow) != 1 || allow[0] != "Bash(ls:*)" {
		t.Errorf("the revert took the `allow` the user added inside a table yolo manages: %v", got)
	}
	if _, left := perms["defaultMode"]; left {
		t.Errorf("the revert left the managed defaultMode yolo wrote: %v", got)
	}
	if !revertNames(rev, "/env/CLAUDE_CODE_USE_BEDROCK") || revertNames(rev, "env") {
		t.Errorf("the report must name the switch it withdraws, not the whole env: %+v", rev.Keys)
	}
}

// A DEFAULT THE USER CHANGED AFTER THE UPGRADE IS THEIRS. `assert` filled pi's `theme` from the
// pack's declared default and recorded it `defaults`; under `none` no apply runs to relabel an
// edited default as the user's, so the revert compares: a default still at its declared value goes,
// one the user changed stays and is named as kept. claude's statusLine, left as declared, is the
// control that goes.
func TestARevertKeepsADefaultTheUserChanged(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	pi := filepath.Join(home, ".pi", "agent", "settings.json")
	claude := filepath.Join(home, ".claude", "settings.json")
	assertRender(t, home, "pi", "pi/settings", nil)
	assertRender(t, home, "claude", "claude/settings", nil)
	if decodeJSONFile(t, pi)["theme"] != "light/dark" || decodeJSONFile(t, claude)["statusLine"] == nil {
		t.Fatalf("fixture premise — both defaults filled: %v / %v", decodeJSONFile(t, pi), decodeJSONFile(t, claude))
	}
	editJSON(t, pi, func(d map[string]any) { d["theme"] = "user-changed" })

	rev := revertHome(t, home, "pi", "claude")
	if got := decodeJSONFile(t, pi)["theme"]; got != "user-changed" {
		t.Errorf("the revert took a default the user changed: theme = %v", got)
	}
	if !keptKey(rev, "theme") {
		t.Errorf("the report does not name the changed default as kept: %+v", rev.Kept)
	}
	if _, left := decodeJSONFile(t, claude)["statusLine"]; left {
		t.Errorf("the control: a default still as declared must go: %v", decodeJSONFile(t, claude))
	}
}

// THE USER'S LATER MODEL PICK STAYS, and the selection record `assert` kept beside the provenance
// record is what says so. `assert` on pi's codex profile wrote defaultModel and recorded the value
// it selected; the user then picked their own model, and a later `assert` apply with the profile
// gone lifted the user's pick (the record keeps yolo's value, and the provenance labels the key
// `retired:computed`). The revert keeps a selection key whose value differs from what the record
// says yolo wrote, and removes the record — from the capture store and from where `assert` kept it
// (CO-D14).
func TestARevertKeepsTheUsersLaterModelPick(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	assertRender(t, home, "pi", "pi/settings", map[string]string{"pi": "codex"})
	editJSON(t, settings, func(d map[string]any) { d["defaultModel"] = "my-pick" })
	assertRender(t, home, "pi", "pi/settings", nil)
	legacy := moveSelectionRecordWhereAssertKeptIt(t, home, "pi", "settings")
	if got := decodeJSONFile(t, settings)["defaultModel"]; got != "my-pick" {
		t.Fatalf("fixture premise — the deselecting apply keeps the user's pick: %v", got)
	}

	rev := revertHome(t, home, "pi")
	if got := decodeJSONFile(t, settings)["defaultModel"]; got != "my-pick" {
		t.Errorf("the revert took the user's later model pick: defaultModel = %v", got)
	}
	if !keptKey(rev, "defaultModel") {
		t.Errorf("the report does not name the pick as kept: %+v", rev.Kept)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("the revert left the selection record `assert` kept at %s: %v", legacy, err)
	}
}

// AN OWNED APPLY AFTER THE REVERT IS A FIRST APPLY AGAIN (CO-D13). `own` writes, the user goes back
// to `none` and reverts, then declares `own` again: the declared defaults come back. Before, the
// revert left the capture store's baseline and captures, so the next owned apply read the reverted
// file as edits against the old baseline and wrote `"statusLine": null` — every key the revert
// took out replayed as the user's deletion.
func TestAnOwnedApplyAfterARevertWritesTheDefaultsAgain(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	own := func() {
		t.Helper()
		in := hostTestInputs(t, testPacksForAgent(t, "claude"), nil, nil, nil)
		hostRenderWith(t, home, render.OwnershipOwn, in, "claude", "claude/settings")
	}
	own()
	if decodeJSONFile(t, settings)["statusLine"] == nil {
		t.Fatalf("fixture premise — own writes the statusLine default: %v", decodeJSONFile(t, settings))
	}
	rev := revertHome(t, home, "claude")
	if len(rev.Forgotten) == 0 {
		t.Errorf("the revert named no capture file to forget after an owned apply")
	}
	if _, left := decodeJSONFile(t, settings)["statusLine"]; left {
		t.Fatalf("fixture premise — the revert takes the default out: %v", decodeJSONFile(t, settings))
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).LastRenderPath("claude", "settings")); !os.IsNotExist(err) {
		t.Errorf("the revert left the capture store's baseline: %v", err)
	}
	own()
	got := decodeJSONFile(t, settings)
	if sl, _ := got["statusLine"].(map[string]any); sl == nil {
		t.Errorf("the owned apply after the revert did not write the default back: statusLine = %v\n%v",
			got["statusLine"], got)
	}
}

// THE FIRST OWNED APPLY OF A HOME `assert` WROTE INTO KEEPS THE USER'S PICK (CO-D14). `assert` on
// pi's codex profile wrote defaultModel and kept its selection record beside the provenance record;
// the user then picked their own model. The first owned apply, the profile still selected, finds
// no record in the capture store: read only there, the stateful arm took the pick for an unrecorded
// value and wrote the profile's model over it on every apply — "a pick of your own after that
// stands" false for exactly this home. Read from where `assert` kept it, the pick stands.
func TestTheFirstOwnedApplyKeepsAPickTheUserMadeUnderAssert(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	assertRender(t, home, "pi", "pi/settings", map[string]string{"pi": "codex"})
	editJSON(t, settings, func(d map[string]any) { d["defaultModel"] = "my-pick" })
	moveSelectionRecordWhereAssertKeptIt(t, home, "pi", "settings")
	for i := 0; i < 2; i++ {
		in := hostTestInputs(t, testPacksForAgent(t, "pi"), map[string]string{"pi": "codex"}, nil, nil)
		hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/settings")
		if got := decodeJSONFile(t, settings)["defaultModel"]; got != "my-pick" {
			t.Fatalf("owned apply %d over a home `assert` wrote into took the user's pick: defaultModel = %v", i+1, got)
		}
	}
}

// moveSelectionRecordWhereAssertKeptIt moves a surface's selection record from the owned capture
// store, where asRetiredAssert's render leaves it, to the provenance directory the retired
// `assert` kept it in, and drops the capture store `assert` never had. It returns the new path.
func moveSelectionRecordWhereAssertKeptIt(t *testing.T, home, agent, name string) string {
	t.Helper()
	owned := render.Host(home, nil, render.OwnershipOwn)
	from := owned.SelectionPath(agent, name)
	to := filepath.Join(owned.ProvenanceDir(), agent+"-"+name+".selection.json")
	if err := os.Rename(from, to); err != nil {
		t.Fatalf("fixture: no selection record to move: %v", err)
	}
	if err := os.RemoveAll(owned.SidecarDir()); err != nil {
		t.Fatal(err)
	}
	return to
}

func revertNames(rev HostRevert, key string) bool {
	for _, k := range rev.Keys {
		if k.Key == key {
			return true
		}
	}
	return false
}

func keptKey(rev HostRevert, key string) bool {
	for _, k := range rev.Kept {
		if k.Key == key && k.Why != "" {
			return true
		}
	}
	return false
}
