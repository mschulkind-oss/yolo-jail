package jailcontent

// briefingsize.go is the launch's size line (docs/design/briefing-diet.md BD-D9): when a
// destination's composed briefing is longer than the length its agent was measured to warn at
// (packdecl.Contribution.WarnsAboveChars), one line says so and names the next step. It never
// refuses and never trims; the caller prints whatever this returns, and a launch has no quiet
// mode to hide it.

import (
	"sort"
	"strconv"
	"strings"
)

// BriefingSize is one destination's composed briefing, measured against its declared limit.
type BriefingSize struct {
	// Into is the home-relative destination path, printed as `~/<Into>`.
	Into string
	// Agent is the identity the destination declared, or "" for none. It names who warns, and
	// it selects which pack prose reached the file, as ComposePackBriefings selected it.
	Agent string
	// Content is the destination's final composed file, exactly as written.
	Content string
	// Limit is the destination's warns_above_chars, or 0 when it declared none.
	Limit int
	// Packs is the pack prose the composer was handed, so the line can name the largest parts.
	Packs []PackBriefing
}

// BriefingChars is a briefing's length as its agent counts it: UTF-16 code units, which is
// what a JavaScript string's length is, and Claude Code is a Node program. For ASCII prose it is
// the byte count.
func BriefingChars(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// BriefingSizeWarning is the size line for one destination, or "" when the destination
// declared no limit or its file is not longer than the limit.
//
// The line names the file, its length, the limit and who warns at it, then the largest three
// parts of the file (each pack's prose that reached it, and the rest: yolo's own text, any
// agents_md_extra and any host file), then the next step: move a section of the largest pack's
// prose into a skill in that pack, where the agent reads it only on demand.
func BriefingSizeWarning(in BriefingSize) string {
	if in.Limit <= 0 {
		return ""
	}
	total := BriefingChars(in.Content)
	if total <= in.Limit {
		return ""
	}
	type part struct {
		name  string
		pack  string
		chars int
	}
	var parts []part
	index := map[string]int{}
	packChars := 0
	for _, p := range in.Packs {
		if !addressesAgent(p.Agents, in.Agent) {
			continue
		}
		n := BriefingChars(strings.TrimRight(p.Text, " \t\r\n"))
		if n == 0 {
			continue
		}
		packChars += n
		if i, ok := index[p.Name]; ok {
			parts[i].chars += n
			continue
		}
		index[p.Name] = len(parts)
		parts = append(parts, part{name: "pack " + p.Name, pack: p.Name, chars: n})
	}
	if rest := total - packChars; rest > 0 {
		parts = append(parts, part{name: "the rest (yolo's text and any host file)", chars: rest})
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].chars > parts[j].chars })

	who := "its agent"
	if in.Agent != "" {
		who = in.Agent
	}
	var b strings.Builder
	b.WriteString("~/" + in.Into + " is " + commaInt(total) + " characters, over the " +
		commaInt(in.Limit) + " at which " + who + " warns about a long file.")
	if len(parts) > 0 {
		b.WriteString(" Its largest parts: ")
		for i, p := range parts {
			if i == 3 {
				break
			}
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(p.name + " " + commaInt(p.chars))
		}
		b.WriteString(".")
	}
	largest := ""
	for _, p := range parts {
		if p.pack != "" {
			largest = p.pack
			break
		}
	}
	if largest != "" {
		b.WriteString(" Move a section of pack " + largest + "'s briefing into a skill in that pack " +
			"(skills/<name>/SKILL.md), which the agent reads only when it needs it.")
	} else {
		b.WriteString(" Move a section of the file into a skill (a pack's skills/<name>/SKILL.md), " +
			"which the agent reads only when it needs it.")
	}
	return b.String()
}

// commaInt writes n with a comma between each group of three digits: 40000 is "40,000".
func commaInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
