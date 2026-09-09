package stores

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// renderText writes the human inventory: a frame header, one section per store
// family, a table of rows, the per-row notes, the class nothing reclaims, and
// what this run did to the ledger.
//
// The section shape is `yolo prune`'s, deliberately: a user who has read one
// report should not have to learn a second layout to read this one. What differs
// is the columns, because the questions differ — prune answers "what would go",
// this answers "what is there, and does anything own it".
func renderText(rep Report, o Options) {
	p := printer{richtext.Printer{W: o.Out, Color: o.Color && o.IsTTYStdout()}}

	p.line("[bold]yolo stores[/bold]  (inventory — this command deletes, moves and mutates nothing)")
	p.line(fmt.Sprintf("Frame: [bold]%s[/bold] — %s", rep.Frame, rep.FrameNote))
	rtLine := "Runtime: " + orNone(rep.Runtime)
	p.line(fmt.Sprintf("%s   Measured: %s   Walk budget: %s per store",
		rtLine, rep.GeneratedAt.Format("2006-01-02 15:04:05 MST"), rep.Budget))
	if o.Age {
		p.line(fmt.Sprintf("[dim]--age: also counting files older than %g days — a per-file walk, so each row prints its own elapsed time.[/dim]", o.AgeDays))
	}

	// SectionAlias sits right after the cache, because it is about the cache: it
	// names the host trees whose bytes the cache section's rows are NOT counting
	// (docs/design/disk-levers-and-backfill.md OQ-BF10). Nothing sums across
	// sections, which is what keeps a host-owned 27 G store out of yolo's own
	// footprint.
	for _, section := range []string{SectionState, SectionCache, SectionAlias, SectionImages, SectionNix} {
		rows := rowsIn(rep, section)
		if len(rows) == 0 {
			continue
		}
		p.line("")
		p.line("[bold]" + section + "[/bold]" + sectionPath(rows))
		renderTable(p, rows, o.Age)
		for _, r := range rows {
			if r.Note != "" {
				p.line(fmt.Sprintf("  [dim]%s: %s[/dim]", r.Name, r.Note))
			}
		}
	}

	// The section the command is named for. §5.5's subject is "the inventory,
	// INCLUDING what nothing reclaims", and a class that is only visible by
	// reading a column of a long table is not surfaced — it is merely present.
	p.line("")
	p.line("[bold]What nothing reclaims[/bold]")
	var unowned []Store
	for _, s := range rep.Stores {
		// VERDICT, not the reclaimer field, is the filter. A store can have no
		// reclaimer function and still be owned — this command's own ledger is
		// bounded by its writer — while "not yolo's" bytes are somebody else's
		// business entirely. What belongs here is the middle case: real bytes, on
		// this machine, that only the user can decide about.
		if s.Verdict != VerdictHuman || (s.Bytes == 0 && s.Count == 0) {
			continue
		}
		unowned = append(unowned, s)
	}
	// Largest first, across every section: this list is the command's answer to
	// "what is nobody looking after", and that question is asked in bytes.
	sort.SliceStable(unowned, func(i, j int) bool { return unowned[i].Bytes > unowned[j].Bytes })
	for _, s := range unowned {
		p.line(fmt.Sprintf("  • %s %s   [dim]%s[/dim]",
			padRight(s.Name, 26), padLeft(sizeCell(s)+countSuffix(s), 22), whyNoReclaimer(s)))
	}
	if len(unowned) == 0 {
		p.line("  [dim]nothing — every non-empty store here has a reclaimer[/dim]")
	}

	p.line("")
	if rep.Recorded > 0 {
		p.line(fmt.Sprintf("[dim]Recorded %s sample(s) under %s — one dated line per store per run, last %d kept.[/dim]",
			fmtCount(rep.Recorded), rep.SamplesDir, MaxSamples))
	} else if o.NoRecord {
		p.line("[dim]--no-record: nothing was written. Growth needs two dated samples, so a store first seen " +
			"under --no-record still has no rate the next time you ask.[/dim]")
	}
	for _, e := range rep.RecordErrs {
		p.line("  [yellow]could not record[/yellow] " + e)
	}
}

// rowsIn returns one section's rows, in report order.
func rowsIn(rep Report, section string) []Store {
	var out []Store
	for _, s := range rep.Stores {
		if s.Section == section {
			out = append(out, s)
		}
	}
	return out
}

// sectionPath renders the root a section's rows share, when they share one.
func sectionPath(rows []Store) string {
	if len(rows) == 0 {
		return ""
	}
	root := rows[0].Path
	if len(rows) > 1 {
		root = commonDir(rows)
	}
	if root == "" {
		return ""
	}
	return "  [dim]" + root + "[/dim]"
}

func commonDir(rows []Store) string {
	parts := strings.Split(rows[0].Path, "/")
	for _, r := range rows[1:] {
		other := strings.Split(r.Path, "/")
		n := min(len(parts), len(other))
		i := 0
		for i < n && parts[i] == other[i] {
			i++
		}
		parts = parts[:i]
	}
	return strings.Join(parts, "/")
}

// renderTable prints the aligned rows. Widths are computed from the content, so
// a long cache-subdir name never forces a wrapped column and a short inventory
// never pads to a width nothing needs.
func renderTable(p printer, rows []Store, age bool) {
	headers := []string{"STORE", "SIZE", "HOW", "GROWTH", "RECLAIMER", "TRIGGER", "CAN YOLO?"}
	cells := make([][]string, 0, len(rows))
	for _, s := range rows {
		row := []string{
			s.Name,
			sizeCell(s) + countSuffix(s),
			string(s.Sizing),
			growthCell(s),
			reclaimerCell(s),
			orNone(s.Reclaimer.Trigger),
			string(s.Verdict),
		}
		if age {
			row = append(row, deadCell(s), orDash(s.Elapsed))
		}
		cells = append(cells, row)
	}
	if age {
		// --age "prints its own elapsed time" (§5.5): the whole reason it is opt-in
		// is that it costs minutes, and a cost you cannot see is a cost you cannot
		// decide about next time.
		headers = append(headers, "OLDER THAN", "WALKED IN")
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = cellWidth(h)
	}
	for _, row := range cells {
		for i, c := range row {
			if w := cellWidth(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	p.line("  [dim]" + strings.TrimRight(pad(headers, widths), " ") + "[/dim]")
	for _, row := range cells {
		p.line("  " + strings.TrimRight(pad(row, widths), " "))
	}
}

func pad(row []string, widths []int) string {
	var b strings.Builder
	for i, c := range row {
		b.WriteString(c)
		if i < len(row)-1 {
			b.WriteString(strings.Repeat(" ", widths[i]-cellWidth(c)+2))
		}
	}
	return b.String()
}

// cellWidth is a cell's width in COLUMNS, not bytes. Every non-ASCII character
// this report prints — the em dash of an absent growth rate, the >= of a lower
// bound — is one column and several bytes, so a byte-counted width silently
// under-pads exactly the columns that carry the report's most important
// distinctions.
func cellWidth(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, w int) string {
	if n := w - cellWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := w - cellWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// sizeCell renders a store's size WITH its completeness, never without: a
// partial figure is a lower bound and is printed as one.
func sizeCell(s Store) string {
	switch s.Sizing {
	case SizingAbsent:
		return "absent"
	case SizingUnknown:
		return "unknown"
	case SizingPartial:
		return "≥ " + prune.FmtBytes(s.Bytes)
	default:
		return prune.FmtBytes(s.Bytes)
	}
}

func countSuffix(s Store) string {
	if s.Count == 0 || s.CountLabel == "" {
		return ""
	}
	label := s.CountLabel
	if s.Count == 1 {
		label = strings.TrimSuffix(label, "s")
	}
	return fmt.Sprintf(" (%s %s)", fmtCount(s.Count), label)
}

func growthCell(s Store) string {
	if s.Growth == nil {
		return "—"
	}
	sign := "+"
	n := s.Growth.BytesPerDay
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%s/d (%.0fd)", sign, prune.FmtBytes(n), s.Growth.Days)
}

func reclaimerCell(s Store) string {
	if s.Reclaimer.Func == "" {
		if s.Reclaimer.Detail != "" {
			return "none (" + s.Reclaimer.Detail + ")"
		}
		return "none"
	}
	if s.Reclaimer.Detail == "" {
		return s.Reclaimer.Func
	}
	return s.Reclaimer.Func + " (" + s.Reclaimer.Detail + ")"
}

func deadCell(s Store) string {
	if s.Dead == nil {
		return "—"
	}
	return fmt.Sprintf("%s in %s files", prune.FmtBytes(s.Dead.Bytes), fmtCount(s.Dead.Files))
}

// whyNoReclaimer is the one-line answer the "what nothing reclaims" list owes
// each row it names.
func whyNoReclaimer(s Store) string {
	switch {
	case s.Note != "":
		return s.Note
	case s.Section == SectionCache:
		return "no age purge covers this subdir"
	default:
		return "nothing in yolo sweeps this path"
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

// fmtCount groups an integer with thousands separators.
//
// It duplicates prune's unexported fmtComma, which is the honest cost of not
// touching internal/prune in this slice: the two are three lines each and a
// shared home for them is a later tidy-up, not a reason to edit a package
// another change is in flight against.
func fmtCount(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	joined := strings.Join(out, ",")
	if neg {
		return "-" + joined
	}
	return joined
}
