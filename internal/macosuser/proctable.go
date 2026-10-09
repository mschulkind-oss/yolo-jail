package macosuser

import (
	"strconv"
	"strings"
)

// procSample is one process as a native process-table reader sees it: its pid, its parent's,
// and its resident size in bytes when the reader was allowed to ask (sized false otherwise).
type procSample struct {
	pid, ppid int
	rssBytes  int64
	sized     bool
}

// renderProcTable writes samples in the shape parsePSTable reads — `pid ppid rss` with rss in
// KiB, rounded up so a resident process never reads as 0 — and "-" for a process whose size
// the reader could not ask for, which is how ps renders one too. Rendering rather than
// returning rows keeps parsePSTable the ONE judge of a table on every reader.
func renderProcTable(samples []procSample) string {
	var b strings.Builder
	for _, p := range samples {
		b.WriteString(strconv.Itoa(p.pid))
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(p.ppid))
		b.WriteByte(' ')
		if p.sized && p.rssBytes >= 0 {
			b.WriteString(strconv.FormatInt((p.rssBytes+1023)/1024, 10))
		} else {
			b.WriteByte('-')
		}
		b.WriteByte('\n')
	}
	return b.String()
}
