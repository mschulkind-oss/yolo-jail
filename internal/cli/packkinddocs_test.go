package cli

// packkinddocs_test.go is the DRIFT GATE between the closed contribution-kind set and the
// two hand-written docs that enumerate it: `yolo config-ref` (config_ref.txt) and
// `yolo pack --help` (packUsage).
//
// Why this exists: the kind set is machine-enumerable (packdecl.KnownKinds()) but both docs
// list it as prose, so every kind added since the lists were written silently went
// undocumented. When this test was added, `config-ref` was missing `autonomy` (12 of 13
// listed) and `pack --help` was missing BOTH `autonomy` and `config-overlay` (11 of 13) —
// and the same gap had already been reported and fixed once before, which is the signature
// of a missing test rather than a careless edit.
//
// The fix is structural: a 16th kind now fails `just test-fast` until it is documented in
// both places. That is cheaper than the alternative (a user discovering a kind exists by
// reading packs/*/pack.json, which is how `autonomy`'s schema had to be learned).
//
// AND THE GATE ITSELF IS PINNED (TestKindDocGateIsNotVacuous), because for a batch it was
// not. It matched any occurrence anywhere in the file, so 13 of the 15 kinds were satisfied
// by prose about something else — a drift gate that reported ok while the drift it exists to
// catch was present. A gate over hand-written prose needs its own control, or its greenness
// says nothing.

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestEveryKindIsDocumented asserts each kind in the closed set has its own LIST ENTRY in
// both user-facing kind listings.
//
// A LIST ENTRY, not an occurrence anywhere in the file, and that distinction is the whole
// value of the test. The first version asked `strings.Contains` with token boundaries, and
// MEASURED, it was vacuous for 13 of the 15 kinds: deleting the entire `loophole` entry from
// config_ref.txt's kind list left the test GREEN, because the word "loophole" also occurs in
// the unrelated `loopholes` CONFIG section forty pages earlier. Only `autonomy` and
// `config-overlay` were genuinely pinned — the two names that appear nowhere else. §3.3
// priced this test as the mechanical cost that ENFORCES a new kind being documented; an
// occurrence test enforces that the word exists somewhere, which prose about a different
// feature satisfies for free.
//
// It still does not validate the surrounding description — prose quality is not testable —
// but "the kind has a row in the list a reader scans" is, and it is the property the gate
// was always claiming.
func TestEveryKindIsDocumented(t *testing.T) {
	docs := map[string]string{
		"config_ref.txt (yolo config-ref)": configRefContent,
		"packUsage (yolo pack --help)":     packUsage,
	}
	for _, kind := range packdecl.KnownKinds() {
		name := string(kind)
		for label, content := range docs {
			if !hasKindListEntry(content, name) {
				t.Errorf("kind %q has no LIST ENTRY in %s — add a row to the kind list, at line "+
					"start, naming the kind and what it contributes.\n"+
					"(A mention elsewhere in the file does NOT count: that is what made this gate "+
					"vacuous for 13 of 15 kinds. Every kind in packdecl.KnownKinds() needs its own "+
					"row in both docs.)", name, label)
			}
		}
	}
}

// hasKindListEntry reports whether doc carries a LIST ENTRY for the kind: an INDENTED line
// whose first non-blank content is exactly the kind name, followed by the COLUMN GAP that
// separates a kind from its description.
//
// Three anchors, each closing a measured false pass:
//
//  1. LINE START (indented). Makes the answer "a reader can find this in the list" rather
//     than "the string occurs somewhere". This is the one whose absence made the gate vacuous
//     for 13 of 15 kinds — `loophole` was satisfied by the unrelated `loopholes` config
//     section forty pages earlier.
//  2. THE COLUMN GAP — two or more spaces, or one space before a `{` field shape. Both docs
//     lay the list out in fixed columns, so a real entry always has a gap; PROSE has single
//     spaces between words. Without this, config_ref's own continuation lines
//     ("state tree).", "launch error naming both.", "config key of its own.") passed as
//     entries for `state`, `launch` and `config`. The `{` allowance is not a loophole in the
//     rule: `config-overlay` is the longest name and overflows its column, so its entry
//     legitimately has one space before `{surface:…}`.
//  3. THE FULL NAME. `config` is a prefix of `config-overlay`, so a doc listing only the
//     latter must not read as documenting the former — the failure would be silent in the
//     direction that matters, since `config` is the more commonly used kind.
//
// A markdown table pipe is NOT accepted, deliberately. Both docs are plain-text CLI output;
// admitting `| config |` would widen the shape for a rendering neither of them uses.
func hasKindListEntry(doc, name string) bool {
	for _, line := range strings.Split(doc, "\n") {
		rest := strings.TrimLeft(line, " \t")
		if rest == line {
			// No leading whitespace: a section header or body prose, not a list row.
			continue
		}
		if !strings.HasPrefix(rest, name) {
			continue
		}
		if isKindColumnGap(rest[len(name):]) {
			return true
		}
	}
	return false
}

// isKindColumnGap reports whether what follows a kind name on a list line is the gap that
// separates the name column from the description column, rather than the single space
// between two words of a sentence.
func isKindColumnGap(after string) bool {
	switch {
	case after == "":
		// A bare name on its own line: the description is on the next line, which is a list
		// shape rather than prose (prose never ends a line mid-sentence on a kind name).
		return true
	case strings.HasPrefix(after, "  "), strings.HasPrefix(after, "\t"):
		return true
	case strings.HasPrefix(after, " {"):
		// The longest name (`config-overlay`) overflows its column and gets one space.
		return true
	}
	return false
}

// TestKindDocGateIsNotVacuous is the CONTROL on the gate above, and it is the test whose
// absence let the gate report ok for a batch while enforcing nothing.
//
// It reproduces the exact defect: `loophole` mentioned in body prose about the CONFIG
// `loopholes` block, with no list entry anywhere. The old predicate accepted that; this one
// must reject it. Without this control, strengthening the predicate could be silently undone
// by anyone who "simplified" it back to strings.Contains — which is how it was written the
// first time.
func TestKindDocGateIsNotVacuous(t *testing.T) {
	// Prose that names the kind, exactly as config_ref.txt's `loopholes` config section does.
	prose := "  loopholes (object): Host-side services the jail can reach.\n" +
		"    A loophole with a manifest.jsonc publishes an ENDPOINT FILE.\n"
	if hasKindListEntry(prose, "loophole") {
		t.Error("prose naming the kind was accepted as documentation. That is the measured " +
			"defect: the whole `loophole` list entry could be deleted from config_ref.txt and " +
			"the gate stayed GREEN, because the word survives in the unrelated `loopholes` " +
			"config section")
	}
	// And a real list row IS accepted, or the gate would be unsatisfiable rather than strict.
	if !hasKindListEntry("      loophole      {from} — a loophole MODULE dir\n", "loophole") {
		t.Error("a real list row was rejected — the gate would then be unsatisfiable, which is " +
			"a different way of enforcing nothing")
	}

	// The prefix case the old token check existed for, kept: a doc listing only
	// `config-overlay` must not be read as documenting `config`. The failure would be silent
	// in the direction that matters, since `config` is the more commonly used kind.
	overlayOnly := "      config-overlay {surface:\"agent/name\"} — keys on another pack's surface\n"
	if hasKindListEntry(overlayOnly, "config") {
		t.Error("`config-overlay`'s row was read as documenting `config` — a prefix match, which " +
			"is exactly the false pass the boundary check exists to prevent")
	}
	if !hasKindListEntry(overlayOnly, "config-overlay") {
		t.Error("`config-overlay`'s own row was rejected")
	}
}

// Every kind is documented in the SAME ORDER in both docs, so a reader moving between
// `yolo pack --help` and `yolo config-ref` reads one list rather than two shuffled ones.
//
// Not cosmetic: the two lists are the only enumeration a user sees, and the whole reason the
// gate exists is that a hand-maintained list drifts. Order drift is the cheapest early signal
// that one list was edited and the other was not.
func TestBothKindListsAreInTheSameOrder(t *testing.T) {
	refOrder := sortedByPosition(kindListPositions(t, configRefContent))
	usageOrder := sortedByPosition(kindListPositions(t, packUsage))
	if strings.Join(refOrder, ",") != strings.Join(usageOrder, ",") {
		t.Errorf("the two kind lists are in different orders — one was edited and the other was "+
			"not:\n  config-ref:  %s\n  pack --help: %s", strings.Join(refOrder, ", "),
			strings.Join(usageOrder, ", "))
	}
}

// kindListPositions maps each kind to the LINE INDEX of its list entry in doc.
//
// It reuses hasKindListEntry's own predicate rather than re-deriving the shape, because two
// matchers over one layout is how the order check and the presence check would come to
// disagree about what an entry is — and the first draft of this file did exactly that.
func kindListPositions(t *testing.T, doc string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for i, line := range strings.Split(doc, "\n") {
		for _, kind := range packdecl.KnownKinds() {
			name := string(kind)
			if _, taken := out[name]; taken {
				continue
			}
			if hasKindListEntry(line, name) {
				out[name] = i
			}
		}
	}
	return out
}

// sortedByPosition returns the kind names ordered by their line index.
func sortedByPosition(pos map[string]int) []string {
	names := make([]string, 0, len(pos))
	for n := range pos {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return pos[names[i]] < pos[names[j]] })
	return names
}
