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
	"github.com/mschulkind-oss/yolo-jail/internal/render"
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

// hostNotchDocMarker is the header of config_ref.txt's host-notch list — the section
// docs/design/report-tiers.md §4.6 moved the kind-refusal RATIONALE into when P8 took it out of
// the report ("the report states facts, not rationale"). The tests below are the condition that
// move was made on.
const hostNotchDocMarker = "AT THE HOST NOTCH"

// TestEveryHostNotchInapplicableKindHasItsReasonDocumented is the DRIFT GATE on that move, and
// it is the only reason moving prose out of a mechanism and into a hand-written doc is safe
// here: retyped text drifts from the thing it describes, so §4.6 made the move conditional on
// this test existing.
//
// ⚠ IT READS BOTH MAPS IN internal/render, through notchInapplicable — the same predicate the
// report's tier-1 line is built from. §4.6's own wording names only the FieldSet's refusals,
// and stopping there would have covered five kinds and silently dropped the other six: `env`,
// `launch`, `hook`, `profile` and `provider` are HONORED by the host FieldSet and unbuilt
// (render.HostUnimplemented), and `service`/`blocked-tool` fall to the generic refusal with no
// entry in refusalReasons at all. A reader meeting any of them gets the one-line report and
// then this list; a gate over half the set would leave the other half undocumented and green.
//
// WHAT IT ASSERTS IS AN ENTRY, NOT THE TEXT. The strings stay in internal/render because they
// are what the code decides by, but the manual wraps and rephrases them for a reader, so
// comparing bytes would fail on the first line break. "The kind has a row in the list a reader
// scans" is the property, exactly as TestEveryKindIsDocumented's is.
func TestEveryHostNotchInapplicableKindHasItsReasonDocumented(t *testing.T) {
	section := hostNotchDocSection(t, configRefContent)
	fields := render.HostFields()
	documented := 0
	for _, kind := range packdecl.KnownKinds() {
		if !notchInapplicable(fields, kind) {
			continue
		}
		documented++
		if !hasKindListEntry(section, string(kind)) {
			t.Errorf("kind %q does not apply at the host notch and has NO ROW in config_ref's "+
				"%q list — `yolo host apply` names it in one line and points here for the "+
				"reason, so without the row the reason exists nowhere a user can read.\n"+
				"(The strings in internal/render are what the code decides by, not what a "+
				"user sees: no terminal view prints them at any verbosity.)",
				kind, hostNotchDocMarker)
		}
	}
	if documented < 2 {
		t.Fatalf("only %d kind(s) were checked — notchInapplicable is answering `false` for "+
			"nearly everything, so this gate is enforcing nothing", documented)
	}
}

// TestHostNotchDocGateIsNotVacuous is the CONTROL, and it exists for the reason its sibling
// control exists: the first version of the kind-doc gate reported ok while enforcing nothing
// for 13 of 15 kinds, because a match ANYWHERE in the file counted.
//
// The failure mode HERE is different and sharper: every kind already has a row in the MAIN kind
// list, so a gate pointed at the whole document would pass for all eleven without a word of the
// rationale ever being written. The section extraction is therefore the gate, and these are its
// assertions.
func TestHostNotchDocGateIsNotVacuous(t *testing.T) {
	section := hostNotchDocSection(t, configRefContent)

	// 1. THE SECTION IS NOT THE DOCUMENT. If it were, the main kind list would satisfy the
	//    gate for every kind and nothing would be enforced.
	if len(section) >= len(configRefContent)/2 {
		t.Errorf("the host-notch section is %d of %d bytes — that is not a section, and a gate "+
			"over it would be satisfied by the main kind list", len(section), len(configRefContent))
	}
	// 2. A KIND THAT DOES APPLY HAS NO ROW IN IT. `config` and `skills` are the host notch's
	//    whole point, and both have rows in the main list a few hundred lines above — so
	//    finding either one here means the extraction ran off the end of the section.
	for _, applies := range []string{"config", "skills", "briefing"} {
		if hasKindListEntry(section, applies) {
			t.Errorf("%q has a row in the host-notch list, but it APPLIES here — the section "+
				"extraction is reaching into the main kind list", applies)
		}
	}
	// 3. PROSE IS NOT A ROW, which is the original control's property, re-asserted against this
	//    section's own text rather than assumed to carry over.
	if hasKindListEntry("    the loophole's endpoint file is mounted into the jail\n", "loophole") {
		t.Error("prose naming a kind was accepted as a documented reason")
	}
}

// hostNotchDocSection returns config_ref's host-notch list: the marker's paragraph, then the
// indented rows under it, ending when the text returns to the marker's own indentation.
//
// INDENTATION RATHER THAN AN END-MARKER, because an end-marker is a thing to forget: the rows
// are indented six spaces and their continuations twenty, while the section that follows
// resumes at four. The one subtlety is that the marker's own paragraph WRAPS at the marker's
// indentation, so the scan cannot stop at the first line back at that level — it stops at the
// first one AFTER the rows have started. A list that grows a row keeps working; a list someone
// moves out of its block fails the control above rather than silently widening.
func hostNotchDocSection(t *testing.T, doc string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, hostNotchDocMarker) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("config_ref.txt has no %q section at all — the host notch's kind reasons "+
			"live nowhere a user can read them (docs/design/report-tiers.md §4.6)",
			hostNotchDocMarker)
	}
	indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " "))
	out := []string{lines[start]}
	inRows := false
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" {
			out = append(out, line)
			continue
		}
		deeper := len(line)-len(strings.TrimLeft(line, " ")) > indent
		if deeper {
			inRows = true
		} else if inRows {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
