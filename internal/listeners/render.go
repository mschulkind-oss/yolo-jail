package listeners

import (
	"strconv"
	"strings"
)

// maxCmdline is where the argv column is cut. A snapshot is meant to be pasted
// into a bug report, so it has to survive an 80-column paste; a jail agent's own
// argv can be hundreds of characters.
const maxCmdline = 60

// Render is the one-screen table to paste into a bug report.
//
// The first line is the VERDICT, not a count, because the count is exactly what a
// reader must not trust on its own: "0 sockets" and "could not read /proc" are the
// same table. The footer says how complete attribution was and lists every gap, so a
// socket with no owner can be read correctly — "nobody in this namespace holds it"
// versus "the walk could not look".
func Render(s Snapshot) string {
	var b strings.Builder
	switch s.Availability() {
	case Unavailable:
		b.WriteString("listening sockets: UNAVAILABLE — no socket table could be read\n")
		writeGaps(&b, s)
		return b.String()
	case Partial:
		b.WriteString("listening sockets: PARTIAL (" + strconv.Itoa(len(s.Sockets)) +
			" found, " + strconv.Itoa(s.TablesRead) + " of " +
			strconv.Itoa(s.TablesAttempted) + " tables read)\n")
	default:
		if len(s.Sockets) == 0 {
			b.WriteString("listening sockets: none (all " + strconv.Itoa(s.TablesRead) +
				" tables read cleanly)\n")
			writeAttribution(&b, s)
			writeGaps(&b, s)
			return b.String()
		}
		b.WriteString("listening sockets: " + strconv.Itoa(len(s.Sockets)) +
			" (all " + strconv.Itoa(s.TablesRead) + " tables read cleanly)\n")
	}

	// An owner-less row means two different things, and the cell says which: with
	// an exhaustive walk nobody in this namespace holds the socket ("none"); with
	// a partial one the owner may simply not have been looked at ("?"). One glyph
	// for both is how a reader concludes a port is free when it is not.
	unowned := "?"
	if s.AttributionComplete() {
		unowned = "none"
	}
	rows := [][]string{{"KIND", "LOCAL", "INODE", "PID", "PROCESS"}}
	for _, l := range s.Sockets {
		inode := strconv.FormatUint(l.Inode, 10)
		if len(l.Owners) == 0 {
			rows = append(rows, []string{string(l.Kind), l.Local(), inode, unowned, unowned})
			continue
		}
		for i, o := range l.Owners {
			// A second owner of ONE socket continues the row rather than
			// starting a new one: two rows with the same inode would read as
			// two sockets, which is the misreading this table exists to prevent.
			kind, local, ino := string(l.Kind), l.Local(), inode
			if i > 0 {
				kind, local, ino = "", "", ""
			}
			rows = append(rows, []string{kind, local, ino, strconv.Itoa(o.PID), processText(o)})
		}
	}
	writeTable(&b, rows)
	writeAttribution(&b, s)
	writeGaps(&b, s)
	return b.String()
}

// processText names the process: comm, plus the argv when it adds anything comm
// does not. comm is truncated to 15 bytes by the kernel, so for anything launched
// through a wrapper it is the argv that identifies the culprit.
func processText(o Owner) string {
	switch {
	case o.Comm == "" && o.Cmdline == "":
		return "?"
	case o.Cmdline == "" || o.Cmdline == o.Comm:
		return o.Comm
	case o.Comm == "":
		return truncate(o.Cmdline, maxCmdline)
	default:
		return o.Comm + " (" + truncate(o.Cmdline, maxCmdline) + ")"
	}
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func writeAttribution(b *strings.Builder, s Snapshot) {
	if !s.AttributionRan {
		b.WriteString("attribution: not run\n")
		return
	}
	state := "complete"
	if !s.AttributionComplete() {
		state = "PARTIAL"
	}
	b.WriteString("attribution: " + state + " — " + strconv.Itoa(s.PIDsScanned) + " of " +
		strconv.Itoa(s.PIDsFound) + " processes read, " + strconv.Itoa(s.FDLinksRead) +
		" descriptors examined")
	if s.Capped {
		b.WriteString(", BUDGET REACHED")
	}
	b.WriteString("\n")
}

func writeGaps(b *strings.Builder, s Snapshot) {
	if len(s.Gaps) == 0 {
		return
	}
	b.WriteString("gaps:\n")
	for _, g := range s.Gaps {
		b.WriteString("  " + g.Source + ": " + g.Reason + "\n")
	}
}

// writeTable pads columns to the widest cell, leaving the last column ragged.
func writeTable(b *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if i == len(row)-1 {
				b.WriteString(cell)
				continue
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-len(cell)+2))
		}
		b.WriteString("\n")
	}
}
