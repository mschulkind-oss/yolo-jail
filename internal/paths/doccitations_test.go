package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docCitationRe matches a docs/ path this repo cites from Go: a comment naming
// the design or reference doc that explains WHY a thing is shaped as it is, or —
// worse to get wrong — a user-visible message telling somebody where to read.
//
// The optional `#fragment` is captured too, because a citation that names the
// right file and a dead anchor lands the reader at the top of a long reference
// doc — the same dead end one hop later. A fragment is a GitHub heading slug or
// an explicit `id`, both of which are letters, digits, `_` and `-` only.
var docCitationRe = regexp.MustCompile(`(docs/[a-z]+/[A-Za-z0-9_.-]+\.md)(?:#([\p{L}\p{N}_-]+))?`)

// citingExts are the source kinds walked for citations. Markdown is deliberately
// absent: a doc's own links are vantage-check's to verify. Everything else that
// ships a comment or a message naming a doc is here, because a citation in
// flake.nix or a workflow rots exactly like one in Go.
var citingExts = map[string]bool{
	".go": true, ".lua": true, ".nix": true, ".yml": true, ".yaml": true,
	".sh": true, ".json": true, ".jsonc": true,
}

// citingDirs are walked recursively; citingRootFiles are single files at the
// repository root.
var (
	citingDirs      = []string{"internal", "cmd", "packs", "integration", "scripts", ".github"}
	citingRootFiles = []string{"flake.nix", "Justfile"}
)

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
	// THE SAME SHAPE AS THE ENTRY ABOVE, and both arrived the same way (2026-09-21): a
	// citation sweep found these two docs had been deleted rather than graduated, so the
	// comments citing them were rewritten to say WHERE THE TEXT IS — `git show <sha>^:<path>`
	// — which is the honest form for material that exists only in history.
	//
	// ⚠ THE PATH IS INSIDE A GIT COMMAND, which is why it reaches this check at all. That is
	// the one form docCitationRe cannot tell from a live citation, and narrowing the regex to
	// exclude it would also excuse a real dead citation that happened to sit near the word
	// `git`. An allowlist entry is the cheaper hole: it is visible, it carries its reason, and
	// it is wrong only if someone later RESURRECTS one of these paths.
	"docs/design/loophole-activation.md": "deleted, not graduated. internal/config and " +
		"internal/loopholedecl cite it as `git show 9190a4d1^:docs/design/loophole-activation.md` " +
		"§1.4 — the text is in history and the comment says so",
	"docs/design/pack-config-keys.md": "deleted, not graduated. internal/loopholedecl and " +
		"internal/loopholes cite it as `git show 2faee0cc^:docs/design/pack-config-keys.md` — " +
		"same shape, same reason",
	// Both deleted in 5eb1643f, and reachable only once this walk grew past .go files
	// (2026-09-23). flake.nix spells the git-show form; the workflow still names the path
	// bare, so ITS entry is a real dead citation held open until the comment is rewritten
	// to the same form, which leaves the entry correct.
	"docs/qa/macos-user-review-findings.md": "deleted in 5eb1643f. flake.nix cites it as " +
		"`git show 5eb1643f^:docs/qa/macos-user-review-findings.md` — same shape, same reason",
	"docs/design/jail-version-predictability.md": "deleted in 5eb1643f. " +
		".github/workflows/update-flake-lock.yml names it; the text is at " +
		"`git show 5eb1643f^:docs/design/jail-version-predictability.md`",
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
	seen := map[string][]string{}    // cited file -> where
	anchors := map[string][]string{} // cited file#fragment -> where

	record := func(rel, text string) {
		for _, m := range docCitationRe.FindAllStringSubmatch(text, -1) {
			seen[m[1]] = append(seen[m[1]], rel)
			if m[2] != "" {
				anchors[m[1]+"#"+m[2]] = append(anchors[m[1]+"#"+m[2]], rel)
			}
		}
	}

	for _, dir := range citingDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !citingExts[filepath.Ext(p)] {
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
			record(rel, string(b))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range citingRootFiles {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		record(name, string(b))
	}

	if len(seen) == 0 {
		t.Fatal("found no docs/ citations at all — the walk or the pattern is broken, " +
			"which would make this test vacuously green")
	}

	if len(anchors) == 0 {
		t.Fatal("found no docs/…#fragment citations at all — the fragment capture is " +
			"broken, which would make the anchor check vacuously green")
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

	targets := map[string]map[string]bool{} // doc -> its anchors, read once each
	for cite, wheres := range anchors {
		doc, frag, _ := strings.Cut(cite, "#")
		if _, ok := fixtureCitations[doc]; ok {
			continue
		}
		ids, ok := targets[doc]
		if !ok {
			b, err := os.ReadFile(filepath.Join(root, doc))
			if err != nil {
				continue // the missing FILE is already reported above
			}
			ids = markdownAnchors(string(b))
			targets[doc] = ids
		}
		if !ids[frag] {
			t.Errorf("%s is cited from %s, but %s has no anchor %q.\n"+
				"\tA reworded heading leaves the fragment behind: repoint it to the "+
				"heading's current slug, or give the target an explicit <a id>.",
				cite, strings.Join(wheres, ", "), doc, frag)
		}
	}

	for cite := range fixtureCitations {
		if _, ok := seen[cite]; !ok {
			t.Errorf("fixtureCitations allowlists %q, which no Go file cites any more — "+
				"drop the entry so the allowlist cannot quietly cover a real citation", cite)
		}
	}
}

var (
	explicitIDRe = regexp.MustCompile(`\bid="([^"]+)"`)
	headingRe    = regexp.MustCompile(`^#{1,6}[ \t]+(.+?)[ \t#]*$`)
	fenceRe      = regexp.MustCompile("^[ \t]*(```|~~~)")
	slugDropRe   = regexp.MustCompile(`[^\p{L}\p{N}_ -]`)
)

// markdownAnchors returns every fragment a GitHub render of doc resolves: each
// explicit id="…" and each ATX heading's slug, with GitHub's -1, -2 suffixes for
// repeats. The slug rule is GitHub's: lowercase, drop everything but letters,
// digits, `_`, `-` and space, then turn each space into `-`. Headings inside a
// fenced block are not headings.
func markdownAnchors(doc string) map[string]bool {
	out := map[string]bool{}
	for _, m := range explicitIDRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}
	counts := map[string]int{}
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slug := strings.ReplaceAll(slugDropRe.ReplaceAllString(strings.ToLower(m[1]), ""), " ", "-")
		if n := counts[slug]; n > 0 {
			out[slug+"-"+strconv.Itoa(n)] = true
		} else {
			out[slug] = true
		}
		counts[slug]++
	}
	return out
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
