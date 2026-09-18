package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// TestApplySealedNamesThePromoteVerb is docs/design/declaration-parity.md DP-B33 / DP-L14.
//
// `apply --sealed`'s refusal for an outstanding capture overlay offered two exits and
// spelled only one of them as a command: *"promote them into a pack or `yolo config reset
// <agent>/<surface>` to discard"*. `yolo config promote` has shipped since
// config-ownership-and-promotion.md §5 landed (cli.configRunW's `case "promote"`,
// internal/cli/configpromote.go), so the reader was handed a runnable command for the
// exit that DESTROYS their edits and English prose for the one that keeps them.
//
// ⚠ The catalog's §9 records that the sweep which found this claimed the verb did not
// exist, and was wrong. Both halves of that are why the assertion below is on the VERB
// being named runnably, not on the sentence's wording.
func TestApplySealedNamesThePromoteVerb(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	// host_management declared, so the ONLY outstanding input is the overlay below —
	// otherwise the refusal list carries a second entry and this test would pass on it.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"assert"}`)

	s, ok := surfaceManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("missing claude/settings")
	}
	writeFile(t, sealedWorkspaceStore().OverlayPath(s.Agent, s.Name), `{"myEdit":"present"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("an outstanding capture overlay should refuse (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "claude/settings") {
		t.Fatalf("the refusal did not come from the capture-overlay branch:\n%s", got)
	}

	// BOTH EXITS, both runnable, both scoped to the surface the refusal named — a remedy
	// the reader has to re-derive the arguments for is a remedy they will not take.
	promote := "`yolo config promote " + s.Agent + "/" + s.Name + "`"
	reset := "`yolo config reset " + s.Agent + "/" + s.Name + "`"
	if !strings.Contains(got, promote) {
		t.Errorf("the refusal does not name the promote command.\nwant substring: %s\ngot:\n%s",
			promote, got)
	}
	if !strings.Contains(got, reset) {
		t.Errorf("the refusal lost the discard command.\nwant substring: %s\ngot:\n%s",
			reset, got)
	}
	// The prose that stood in for the command. Its problem was not vagueness in the
	// abstract: it named no verb, so the only actionable half of the sentence was the
	// destructive one.
	if strings.Contains(got, "promote them into a pack") {
		t.Errorf("the refusal still describes promotion in prose beside a runnable reset:\n%s", got)
	}
}

// TestApplyAtGuestReusesTheSharedNotchSentence pins apply's half of OQ-DP3's "verbatim":
// the words this verb prints come from render.NotchUnbuilt, the same function `run.Run`'s
// launch gate calls, so the two cannot describe one notch differently.
//
// The literal-uniqueness half lives in internal/render
// (TestGuestNotchSentenceHasExactlyOneHome). This is the behavioural half — it fails if
// someone re-inlines the string here, even with the right words today.
func TestApplyAtGuestReusesTheSharedNotchSentence(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "guest"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("apply --at guest should fail closed (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}
	want := render.NotchUnbuilt("apply")
	if !strings.Contains(out.String(), want) {
		t.Errorf("apply --at guest does not print render.NotchUnbuilt's sentence.\n"+
			"want substring: %q\ngot:\n%s", want, out.String())
	}
}
