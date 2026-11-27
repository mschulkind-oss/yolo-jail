// Package toolskills discovers the agent skills the user's own mise tools declare
// (docs/reference/mise-tool-skills.md, slice 2). It is the discovery half only: it runs
// `mise skills ls --json`, parses and filters the answer, and reports what it dropped. It copies
// nothing, writes nothing and prints nothing; the snapshot writer and the provisioning step
// (slice 3) call it and decide what the user reads.
//
// The calling sequence for slice 3:
//
//	res := toolskills.Runner{}.List(ctx, "/workspace") // never panics, never returns an error
//	switch res.Outcome {
//	case toolskills.OutcomeOK:
//		sel := toolskills.Select(res.Skills, allowlist) // allowlist = YOLO_MISE_SKILL_TOOLS, in order
//		// sel.Skills are the skills to copy, in delivery order; sel.Shadowed and sel.Invalid
//		// are named to the user; res.Warnings are mise's missing-skill lines.
//	default: // OutcomeUnsupported, OutcomeFailed, OutcomeTimedOut
//		// keep the previous snapshot; res.Reason is the text for the user's line.
//	}
//
// Everything mise says is untrusted: a tool author chooses skill names and paths. The parser
// takes only the documented shape (an array of objects with name, tool, version and absolute
// path), ignores unknown fields, and never relies on the order mise returns. Whether a path
// lies inside mise's installs directory is the copier's check (slice 3), not this package's.
package toolskills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Skill is one entry of `mise skills ls --json`. Path is the absolute directory holding the
// skill's SKILL.md. Tool is the backend identity as mise spells it, for example
// "packslip:github.com/jdx/hk".
type Skill struct {
	Name    string `json:"name"`
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// ParseList parses the stdout of `mise skills ls --json`. Unknown fields are ignored. It
// refuses anything that is not a JSON array of objects, and an entry whose name or tool is
// empty or whose path is not absolute, naming the first offender (sanitized).
func ParseList(stdout []byte) ([]Skill, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, errors.New("empty output, expected a JSON array")
	}
	if trimmed[0] != '[' {
		return nil, errors.New("output is not a JSON array")
	}
	var skills []Skill
	if err := json.Unmarshal(trimmed, &skills); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	for i, s := range skills {
		switch {
		case s.Name == "":
			return nil, fmt.Errorf("entry %d has no name", i)
		case s.Tool == "":
			return nil, fmt.Errorf("entry %d (%s) has no tool", i, Sanitize(s.Name))
		case !filepath.IsAbs(s.Path):
			return nil, fmt.Errorf("entry %d (%s) has a path that is not absolute", i, Sanitize(s.Name))
		}
	}
	return skills, nil
}

// MaxNameBytes is the longest skill name accepted.
const MaxNameBytes = 128

// ValidName reports whether name may be a skill's directory name: one path component, not
// starting with ".", no control characters, at most MaxNameBytes bytes, valid UTF-8.
func ValidName(name string) bool {
	if name == "" || len(name) > MaxNameBytes || !utf8.ValidString(name) {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Sanitize makes s safe to print: control characters and invalid bytes become "?", and the
// result is cut to MaxNameBytes bytes at a rune boundary, with "..." marking the cut.
func Sanitize(s string) string { return sanitizeTo(s, MaxNameBytes) }

func sanitizeTo(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			r = '?'
		}
		if b.Len()+utf8.RuneLen(r) > max {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Shadowed is a skill that lost a name conflict between two allowlisted tools. Winner is the
// tool, earlier in the allowlist, whose skill of that name is kept.
type Shadowed struct {
	Skill  Skill
	Winner string
}

// Invalid is a skill skipped because its name failed ValidName. Name is already sanitized.
type Invalid struct {
	Name string
	Tool string
}

// Selection is Select's answer.
type Selection struct {
	// Skills are the skills to deliver: ordered by allowlist position of their tool, then name.
	Skills []Skill
	// Shadowed lists the duplicates, in the same order, for disclosure.
	Shadowed []Shadowed
	// Invalid lists the skills skipped for an unusable name.
	Invalid []Invalid
}

// Select keeps the entries whose Tool exactly equals an allowlist entry, orders them by the
// allowlist position of the tool and then by skill name, drops invalid names, and resolves a
// name two tools both declare in favor of the tool first in the allowlist. mise's own order is
// never used. A tool named twice in the allowlist counts at its first position. A repeat of
// the same tool and name keeps the first in sorted order and is not reported.
func Select(all []Skill, allowlist []string) Selection {
	pos := make(map[string]int, len(allowlist))
	for i, t := range allowlist {
		if _, ok := pos[t]; !ok {
			pos[t] = i
		}
	}
	var kept []Skill
	var sel Selection
	for _, s := range all {
		if _, ok := pos[s.Tool]; !ok {
			continue
		}
		if !ValidName(s.Name) {
			sel.Invalid = append(sel.Invalid, Invalid{Name: Sanitize(s.Name), Tool: s.Tool})
			continue
		}
		kept = append(kept, s)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		pi, pj := pos[kept[i].Tool], pos[kept[j].Tool]
		if pi != pj {
			return pi < pj
		}
		if kept[i].Name != kept[j].Name {
			return kept[i].Name < kept[j].Name
		}
		return kept[i].Version < kept[j].Version
	})
	sort.SliceStable(sel.Invalid, func(i, j int) bool {
		a, b := sel.Invalid[i], sel.Invalid[j]
		if pos[a.Tool] != pos[b.Tool] {
			return pos[a.Tool] < pos[b.Tool]
		}
		return a.Name < b.Name
	})
	owner := map[string]string{}
	for _, s := range kept {
		if w, taken := owner[s.Name]; taken {
			if w != s.Tool {
				sel.Shadowed = append(sel.Shadowed, Shadowed{Skill: s, Winner: w})
			}
			continue
		}
		owner[s.Name] = s.Tool
		sel.Skills = append(sel.Skills, s)
	}
	return sel
}

// Warning is one missing-skill line mise printed on stderr, for example
//
//	mise WARN  packslip:github.com/jdx/hk@2.13.0 declares a skill hk-debug that is not installed: the install does not hold it; reinstall the tool to fetch it
//
// Line is the whole line, sanitized and cut to 200 bytes, for the user-facing "declared but
// not installed" message.
type Warning struct {
	Tool    string
	Version string
	Skill   string
	Line    string
}

var (
	missingRE = regexp.MustCompile(`^mise\s+WARN\s+(\S+)@(\S+)\s+declares a skill\s+(\S+)\s+that is not installed`)
	ansiRE    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

// ParseWarnings extracts mise's missing-skill lines from its stderr (sample:
// testdata/ls-missing.stderr, measured on mise 2026.10.17). Any other text is ignored, so a
// reworded or unrelated line costs only its disclosure. ANSI escapes are stripped first.
func ParseWarnings(stderr string) []Warning {
	var out []Warning
	for _, raw := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(ansiRE.ReplaceAllString(raw, ""))
		m := missingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, Warning{
			Tool: Sanitize(m[1]), Version: Sanitize(m[2]), Skill: Sanitize(m[3]),
			Line: sanitizeTo(line, 200),
		})
	}
	return out
}
