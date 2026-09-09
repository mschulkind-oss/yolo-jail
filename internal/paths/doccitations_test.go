package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docCitationRe matches a docs/ path this repo cites from Go: a comment naming
// the design or reference doc that explains WHY a thing is shaped as it is, or —
// worse to get wrong — a user-visible message telling somebody where to read.
var docCitationRe = regexp.MustCompile(`docs/[a-z]+/[A-Za-z0-9_.-]+\.md`)

// fixtureCitations are strings that LOOK like doc citations and are not: paths
// invented by a test to stand in for a real one. Each needs a reason, because an
// entry here is a hole in the check.
var fixtureCitations = map[string]string{
	"docs/design/whatever.md": "srcskew_test builds a synthetic commit touching a docs-only path; the name is deliberately meaningless",
	"docs/handoff/oauth.md":   "briefing/handoff fixtures quote a user's own handover note, which names a path in THEIR repo, not ours",
	"docs/handoff/broker.md":  "same fixture family",
	"docs/design/go-port-divergences.md": "internal/json5 and internal/jsonx name it to say " +
		"where the ledger WENT — it was archived in 2c229fbc and TestLedgeredDivergences is the " +
		"live record. A deliberate historical mention, not a live citation",
}

// TestEveryDocCitationFromGoResolves is the tripwire for a class that has bitten
// this repo twice in one day.
//
// A Go comment citing a design doc by path is this project's convention for
// recording why something is shaped as it is, and there are ~80 of them. Nothing
// about MOVING a doc forces anyone to look at them, so they rot silently — and on
// 2026-09-09 a corpus restructure turned eleven design docs into four references
// and left 10 dead citations behind in one commit.
//
// The expensive spelling is not a comment. `internal/loopholedecl`'s retirement
// message for the `unix-socket` transport names a doc path in text a MIGRATING
// PACK AUTHOR reads, so a moved doc turns yolo's own error message into a dead
// end at exactly the moment somebody needs it.
//
// This walks the tree rather than taking a list, so a citation added tomorrow is
// covered without anyone remembering to register it.
func TestEveryDocCitationFromGoResolves(t *testing.T) {
	root := repoRootForTest(t)
	seen := map[string][]string{} // citation -> where

	for _, dir := range []string{"internal", "cmd", "packs", "integration"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(p) != ".go" {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			rel, _ := filepath.Rel(root, p)
			// SKIP THIS FILE. It scans .go sources, and it is one — so without
			// this every fixtureCitations entry would be "cited" by the very map
			// that allowlists it, making the stale-entry check below vacuous.
			// Found by mutating that check and watching it stay green.
			if filepath.Base(rel) == "doccitations_test.go" {
				return nil
			}
			for _, m := range docCitationRe.FindAllString(string(b), -1) {
				seen[m] = append(seen[m], rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(seen) == 0 {
		t.Fatal("found no docs/ citations at all — the walk or the pattern is broken, " +
			"which would make this test vacuously green")
	}

	for cite, wheres := range seen {
		if _, ok := fixtureCitations[cite]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, cite)); err != nil {
			t.Errorf("%s is cited from %s but does not exist.\n"+
				"\tA moved doc leaves the citation behind: repoint it, or add it to "+
				"fixtureCitations WITH A REASON if it is a test's invented path.",
				cite, strings.Join(wheres, ", "))
		}
	}

	for cite := range fixtureCitations {
		if _, ok := seen[cite]; !ok {
			t.Errorf("fixtureCitations allowlists %q, which no Go file cites any more — "+
				"drop the entry so the allowlist cannot quietly cover a real citation", cite)
		}
	}
}

// repoRootForTest walks up for the directory holding go.mod. Tests run in their
// own package dir, and this test is the only one that needs the whole tree.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("no go.mod above the working directory; not a source checkout")
		}
		dir = parent
	}
}
