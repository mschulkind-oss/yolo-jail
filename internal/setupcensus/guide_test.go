package setupcensus

// guide_test.go checks userguide/reference/settings-per-setup.md against the census (OQ-BP-1:
// "checked against the table or generated from it"; checked, for the reason
// docs/design/backend-parity.md's BP-D18 gives). The page is prose for a user, at a finer grain
// than the census and with footnotes the census does not hold, so it is not generated. What the
// check pins is the part the two must agree on: every key and kind has a row, every row naming
// one is claimed by the census, and each claimed cell's answer is the census's disposition.
//
// HOW A CELL IS READ. A guide cell answers in the page's own vocabulary — "works", "absent,
// warns", "absent, silent", "refuses", "n/a" — and often says more than one thing ("works on
// 1.1.0+; older: absent, warns"). guideClasses reads every answer a cell gives, and the census
// disposition must be one of them. That is deliberately lenient about the qualifications a user
// needs and strict about the disagreement the ruling cares about: a cell that says only "works"
// where the census says Warned, or only "absent, warns" where it says Honored, fails.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const guideRel = "userguide/reference/settings-per-setup.md"

// guideRow is one table row of the guide that has a column for each of the four setups.
type guideRow struct {
	line  int
	label string // the first cell, footnote references removed
	cells map[Setup]string
}

var footnoteRef = regexp.MustCompile(`\[\^[^\]]+\]`)

// setupColumn maps a header cell to its setup: the four headers, backticks and spaces removed.
func setupColumn(header string) (Setup, bool) {
	h := strings.NewReplacer("`", "", " ", "").Replace(header)
	for _, s := range Setups() {
		if h == s.String() {
			return s, true
		}
	}
	return 0, false
}

func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// guideRows reads every table of the guide whose header names all four setups.
func guideRows(t *testing.T) []guideRow {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", guideRel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", guideRel, err)
	}
	var rows []guideRow
	var cols map[int]Setup
	for i, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			cols = nil
			continue
		}
		cells := splitRow(line)
		if cols == nil {
			found := map[int]Setup{}
			for j, c := range cells {
				if s, ok := setupColumn(c); ok {
					found[j] = s
				}
			}
			if len(found) == len(Setups()) {
				cols = found
			} else {
				cols = map[int]Setup{} // a table this check does not read
			}
			continue
		}
		if len(cols) == 0 || strings.Trim(line, "|-: ") == "" {
			continue
		}
		r := guideRow{line: i + 1, label: strings.TrimSpace(footnoteRef.ReplaceAllString(cells[0], "")),
			cells: map[Setup]string{}}
		for j, s := range cols {
			if j < len(cells) {
				r.cells[s] = cells[j]
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatalf("%s has no table with a column for each of the four setups", guideRel)
	}
	return rows
}

// guideClasses reads the answers a guide cell gives, as the census dispositions they agree with.
// Honored and HonoredBy read the same, since the page states the mechanism in prose.
func guideClasses(cell string) map[Disposition]bool {
	c := strings.ToLower(strings.NewReplacer("*", "", "`", "").Replace(footnoteRef.ReplaceAllString(cell, "")))
	out := map[Disposition]bool{}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(c, w) {
				return true
			}
		}
		return false
	}
	if has("works", "expected to work") {
		out[Honored], out[HonoredBy] = true, true
	}
	if has("warns", "the launch says", "says so") {
		out[Warned] = true
	}
	if has("silent", "inert") {
		out[Dropped] = true
	}
	if has("refuse", "stops the launch", "breaks the launch") {
		out[Refused] = true
	}
	if has("n/a") {
		out[NotApplicable] = true
	}
	return out
}

// claim is one census entry (or aspect) and the guide label it names.
type claim struct {
	subject string
	entry   Entry
	label   string
}

// claims lists every guide label in the census, with the subject that names it.
func claims() []claim {
	var out []claim
	add := func(subject string, e Entry) {
		for _, l := range e.Guide {
			out = append(out, claim{subject, e, l})
		}
		for name, a := range e.Aspects {
			for _, l := range a.Guide {
				out = append(out, claim{subject + "." + name, a, l})
			}
		}
	}
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		add("config key "+k, e)
	}
	for _, k := range Kinds() {
		e, _ := Kind(k)
		add("pack kind "+string(k), e)
	}
	return out
}

// rowsFor returns the guide rows whose label starts with the given text.
func rowsFor(rows []guideRow, label string) []guideRow {
	var out []guideRow
	for _, r := range rows {
		if strings.HasPrefix(r.label, label) {
			out = append(out, r)
		}
	}
	return out
}

// TestEveryGuideLabelNamesOneRow: a label naming no row is a census entry checked against
// nothing, and one naming two is ambiguous about which it checks.
func TestEveryGuideLabelNamesOneRow(t *testing.T) {
	rows := guideRows(t)
	for _, c := range claims() {
		if got := rowsFor(rows, c.label); len(got) != 1 {
			var labels []string
			for _, r := range got {
				labels = append(labels, r.label)
			}
			t.Errorf("%s names guide row %q, which matches %d rows of %s %v: make the label the "+
				"start of exactly one row's first cell", c.subject, c.label, len(got), guideRel, labels)
		}
	}
}

// TestTheGuideAgreesWithTheCensus is the check the ruling asked for: on every row the census
// claims, each setup's guide cell gives the census's answer among its own.
func TestTheGuideAgreesWithTheCensus(t *testing.T) {
	rows := guideRows(t)
	for _, c := range claims() {
		got := rowsFor(rows, c.label)
		if len(got) != 1 {
			continue // TestEveryGuideLabelNamesOneRow reports it
		}
		r := got[0]
		for _, s := range Setups() {
			cell := c.entry.Cell(s)
			classes := guideClasses(r.cells[s])
			if len(classes) == 0 {
				t.Errorf("%s:%d (%s) on %s says %q, which gives none of the page's answers (works, "+
					"absent, warns, absent, silent, refuses, n/a): the census says %s — %s",
					guideRel, r.line, r.label, s, r.cells[s], cell.Disposition, cell.Reason)
				continue
			}
			if !classes[cell.Disposition] {
				t.Errorf("%s:%d (%s) on %s says %q, and the census (%s) says %s — %s.\nFix whichever "+
					"is wrong: the guide cell, or the census cell in internal/setupcensus.",
					guideRel, r.line, r.label, s, r.cells[s], c.subject, cell.Disposition, cell.Reason)
			}
		}
	}
}

// leadingName is a row's leading backticked name, cut at the first `.`, `:` or space, so
// `network.mode: "bridge"` names network and `programs: { autoprune: true }` names programs.
func leadingName(label string) string {
	if !strings.HasPrefix(label, "`") {
		return ""
	}
	end := strings.Index(label[1:], "`")
	if end < 0 {
		return ""
	}
	name := label[1 : end+1]
	if i := strings.IndexAny(name, ".: "); i >= 0 {
		name = name[:i]
	}
	return name
}

// TestEveryGuideRowNamingAKeyOrKindIsClaimed is the reverse direction: a row that starts with a
// live key or kind and that no census entry claims is a guide cell nothing checks.
func TestEveryGuideRowNamingAKeyOrKindIsClaimed(t *testing.T) {
	names := map[string]bool{}
	for _, k := range config.TopLevelConfigKeys() {
		names[k] = true
	}
	for _, k := range packdecl.KnownKinds() {
		names[string(k)] = true
	}
	labels := claims()
	for _, r := range guideRows(t) {
		name := leadingName(r.label)
		if !names[name] {
			continue
		}
		claimed := false
		for _, c := range labels {
			if strings.HasPrefix(r.label, c.label) {
				claimed = true
			}
		}
		if !claimed {
			t.Errorf("%s:%d (%s) names `%s` and no census entry claims it: add the row's label to "+
				"the Guide list of the entry or aspect it describes in internal/setupcensus",
				guideRel, r.line, r.label, name)
		}
	}
}

// TestEveryKeyAndKindHasAGuideRow: the page's summary says it covers every config key and pack
// kind, so a key with no claimed row is a key the guide leaves out. A KIND with none is covered
// by the pack section's sentence that every kind it does not list "is delivered on all four
// setups", so such a kind must work on all four, or the sentence is false.
func TestEveryKeyAndKindHasAGuideRow(t *testing.T) {
	claimed := map[string]bool{}
	for _, c := range claims() {
		claimed[strings.SplitN(c.subject, ".", 2)[0]] = true
	}
	var missing []string
	for _, k := range config.TopLevelConfigKeys() {
		if !claimed["config key "+k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("config keys with no row in %s: %v — add a row naming each to one of its "+
			"per-setup tables, and name the row in the key's census entry (Guide)", guideRel, missing)
	}
	for _, k := range packdecl.KnownKinds() {
		if claimed["pack kind "+string(k)] {
			continue
		}
		e, _ := Kind(k)
		for _, s := range Setups() {
			if c := e.Cell(s); !c.Disposition.Works() {
				t.Errorf("pack kind %s is %s on %s (%s), and %s lists it in no row, so its "+
					"sentence that every unlisted kind is delivered on all four setups is false: "+
					"add a row to the pack table and name it in the kind's census entry",
					k, c.Disposition, s, c.Reason, guideRel)
			}
		}
	}
}

// The reading of a cell is the check's whole judgement, so its cases are pinned.
func TestGuideClassesReadsThePagesVocabulary(t *testing.T) {
	for cell, want := range map[string][]Disposition{
		"works": {Honored, HonoredBy},
		"`absent, warns` — not built[^cachegap]":             {Warned},
		"absent, **silent** — no surface mentions it":        {Dropped},
		"**always RAM-backed, silent**[^aceph]":              {Dropped},
		"refuses — nix error at launch":                      {Refused},
		"`n/a` — the machine's real `/tmp`[^mueph]":          {NotApplicable},
		"works on 1.1.0+; older: `absent, warns`":            {Honored, HonoredBy, Warned},
		"**breaks the launch — measured**":                   {Refused},
		"not delivered, and the launch says where it is":     {Warned},
		"absent, warns — runs bridged; port keys still work": {Warned},
	} {
		got := guideClasses(cell)
		if len(got) != len(want) {
			t.Errorf("guideClasses(%q) = %v, want %v", cell, got, want)
			continue
		}
		for _, d := range want {
			if !got[d] {
				t.Errorf("guideClasses(%q) = %v, want %v", cell, got, want)
			}
		}
	}
}
