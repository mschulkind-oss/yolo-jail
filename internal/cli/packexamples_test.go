package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// examplePacksDir holds the example packs the repository ships for users to copy. They are
// not embedded (packs/ is the shipped set), so nothing else reads them: no launch, no
// `yolo check`, no TestShippedPacksLintClean. A schema change that refuses their shape
// therefore leaves every one of them green everywhere but in the hands of the person who
// copies it.
var examplePacksDir = filepath.Join("..", "..", "docs", "examples")

// EVERY example pack lints clean, through `yolo pack lint` itself, and draws none of its
// advice. The sibling of TestShippedPacksLintClean for the packs that are not embedded.
//
// It exists because the fzf example was stranded exactly this way: its prose was a root
// AGENTS.md named by a briefing `from`, and when that basename became a refused SOURCE
// (docs/reference/pack-system.md#briefing-p1) the example stopped loading — `yolo pack lint`
// refused it, and so would every launch selecting a copy — while nothing in the suite read it.
//
// THE ADVICE COUNTS TOO. Lint exits 0 beside an info line telling the author the shape is
// wrong for what they meant (a content `into` an agent pack already declares, a prose file
// nothing ships). An example is the shape users copy, so one that draws lint's own advice
// teaches the shape lint advises against.
//
// Every DIRECTORY under docs/examples is a pack: an example that is not one belongs elsewhere,
// and a directory that is not a pack fails lint ("does nothing"), so a misplaced one is caught
// rather than skipped.
func TestExamplePacksLintClean(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entries, err := os.ReadDir(examplePacksDir)
	if err != nil {
		t.Fatalf("read the example packs: %v", err)
	}
	var linted []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		linted = append(linted, e.Name())
		dir := filepath.Join(examplePacksDir, e.Name())
		var out, errw bytes.Buffer
		rc := packMain([]string{"lint", dir}, &out, &errw, false)
		if rc != 0 {
			t.Errorf("example pack %s does not lint clean (rc=%d), so a user who copies it gets "+
				"a pack that will not load:\n%s%s", e.Name(), rc, out.String(), errw.String())
			continue
		}
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "ℹ") {
				t.Errorf("example pack %s draws lint's advice, so it teaches a shape lint "+
					"advises against:\n%s\n(full output:\n%s)", e.Name(), line, out.String())
			}
		}
	}
	// The control: a gate that found nothing to lint passes for free. The fzf example is named
	// because it is the one this test was written for, so moving or renaming it is a decision
	// this line makes someone take on purpose.
	if !slices.Contains(linted, "claude-fzf-pack") {
		t.Fatalf("no claude-fzf-pack under %s (found %v): the gate is not reading the examples "+
			"it exists for", examplePacksDir, linted)
	}
}
