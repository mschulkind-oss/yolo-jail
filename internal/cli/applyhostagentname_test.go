package cli

// applyhostagentname_test.go is the `yolo host apply` half of the agent-name exclusivity
// pre-flight (briefing-audiences.md OQ-BA6/BA7).
//
// It matters most at THIS notch, which is why it is refused here as well as at launch: the
// render routes an addressed contribution to "where <name> reads", and with two owners the
// prose lands at whichever destination the resolution loop saw first — in a REAL home, with
// nothing said. The pass has unit tests in internal/packload; these drive applyHost, so
// deleting the call site in apply.go turns them red.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoOwnerFixture selects two packs that both claim the agent name `claude` — one by
// installing the binary, the other by declaring where claude reads. Neither shape is
// individually invalid, which is the point: only the SET is.
func twoOwnerFixture(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	official := filepath.Join(t.TempDir(), "claude-official")
	writeFile(t, filepath.Join(official, "pack.json"),
		`{"name":"claude-official","description":"o","contributes":[`+
			`{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"}]}`)
	fork := filepath.Join(t.TempDir(), "claude-fork")
	writeFile(t, filepath.Join(fork, "pack.json"),
		`{"name":"claude-fork","description":"f","contributes":[`+
			`{"kind":"briefing","into":".claude/CLAUDE.md","agent":"claude"}]}`)
	writeFile(t, filepath.Join(fork, "AGENTS.md"), "Fork prose.\n")

	selectPacks(t, home,
		`{"source":"file://`+official+`","name":"claude-official"},`+
			`{"source":"file://`+fork+`","name":"claude-fork"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE CALL-SITE TEST: the apply is REFUSED, it says why, and it writes nothing.
func TestApplyHostRefusesTwoPacksClaimingOneAgentName(t *testing.T) {
	home := twoOwnerFixture(t)
	before := treeHashes(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("host apply --assert accepted two packs claiming `claude`; it routes an "+
			"addressed contribution to \"where claude reads\", so with two owners the prose "+
			"lands wherever the resolution loop looked first:\n%s", report)
	}
	for _, want := range []string{
		"agent",
		"claude",
		"exactly ONE owning pack",
		"Nothing was written",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q; got:\n%s", want, report)
		}
	}
	// NOTHING WRITTEN is the load-bearing half — a refusal that had already rendered half the
	// home would be worse than no refusal, because the user would have to undo it by hand.
	if after := treeHashes(t, home); len(after) != len(before) {
		t.Errorf("the refused apply changed the home: %d files before, %d after", len(before), len(after))
	} else {
		for rel, sum := range before {
			if after[rel] != sum {
				t.Errorf("the refused apply rewrote %s", rel)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); err == nil {
		t.Error("the refused apply generated a briefing anyway")
	}
}

// The apply must still succeed for the shipped shape — one pack owning one name through both
// `program` and its briefing's `agent` — or the refusal above would block every stock config.
func TestApplyHostAcceptsOnePackOwningItsOwnName(t *testing.T) {
	home := t.TempDir()
	solo := filepath.Join(t.TempDir(), "solocli")
	writeFile(t, filepath.Join(solo, "pack.json"),
		`{"name":"solocli","description":"s","contributes":[`+
			`{"kind":"program","bin":"solocli","via":"npm","package":"solocli"},`+
			`{"kind":"launch","bin":"solocli","flags":["--yes"]},`+
			`{"kind":"briefing","into":".solo/AGENTS.md","agent":"solocli"}]}`)
	writeFile(t, filepath.Join(solo, "AGENTS.md"), "Solo prose.\n")
	selectPacks(t, home, `{"source":"file://`+solo+`","name":"solocli"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("one pack owning one name through three kinds must apply; rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".solo", "AGENTS.md"))
	if err != nil {
		t.Fatalf("the briefing was not delivered: %v\n%s", err, report)
	}
	if !strings.Contains(string(got), "Solo prose.") {
		t.Errorf("delivered briefing is missing the pack's prose:\n%s", got)
	}
}
