package packdecl

// platform.go is the SHAPE rule for a provider's `platform` (docs/design/
// providers-and-profiles-redesign.md OQ-BR2, ruled 2026-09-29): the one statement of what a
// platform value may look like, read by the manifest validator and by internal/config's check
// of a user's own `providers.<name>.platform`, so the two layers of one composed table cannot
// accept different spellings of one field.
//
// The VOCABULARY is open and deliberately not checked here: which platforms exist is the
// consuming derive's business, and an unknown value is inert (Contribution.Platform says why).
// What IS checked is that the value could ever equal one a derive compares against: a
// platform is one token, so an empty value or one carrying whitespace names no service a
// consumer could recognize, and accepting it would be a declaration that silently does
// nothing.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
)

// PlatformSwitch is one setting in an agent's own config that puts the agent on a provider
// platform by itself (Contribution.PlatformSwitches, PP-D1).
type PlatformSwitch struct {
	// Platform is the provider platform the switch turns the agent onto, "aws-bedrock" for
	// claude's CLAUDE_CODE_USE_BEDROCK. A selected provider declaring it is what the switch
	// needs to be served.
	Platform string `json:"platform"`
	// Surface is the config surface, "<agent>/<name>", that carries the switch: one of this
	// pack's own, of this program's agent, declaring `readsHost` — the switch that matters is
	// the one in the USER's own copy of the file, which only a host layer reads.
	Surface string `json:"surface"`
	// Pointer is the RFC 6901 JSON Pointer to the switch inside that file, "/env/CLAUDE_CODE_USE_BEDROCK".
	// The switch is ON when the value there is JSON true, the number 1, or a string reading 1,
	// true, yes or on in any case: the common environment-flag convention, Claude Code's among
	// them. Its last step names the key in the line the launch prints.
	Pointer string `json:"pointer"`
}

// PlatformSwitches is every platform switch the program installing bin declares, nil when it
// declares none (or the manifest installs no such program).
func (m *Manifest) PlatformSwitches(bin string) []PlatformSwitch {
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin {
			return c.PlatformSwitches
		}
	}
	return nil
}

// Key is the name the switch's pointer ends in, the key a user removes.
func (s PlatformSwitch) Key() string {
	steps, err := jsonptr.Parse(s.Pointer)
	if err != nil || len(steps) == 0 {
		return s.Pointer
	}
	return steps[len(steps)-1]
}

// platformSwitchProblems validates a program's platform_switches: each needs a platform of the
// platform shape, a surface of this program's agent spelled "<agent>/<name>", and a pointer that
// parses and names a key; on any kind but program the list is refused, a switch no consumer
// reads being the accepted-and-ignored declaration this schema refuses everywhere.
func platformSwitchProblems(label string, c Contribution) []string {
	if len(c.PlatformSwitches) == 0 {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"platform_switches\" — they are "+
			"settings in a PROGRAM's own config, so only \"program\" has one", label, c.Kind)}
	}
	var out []string
	for i, s := range c.PlatformSwitches {
		at := fmt.Sprintf("%s.platform_switches[%d]", label, i)
		if s.Platform == "" {
			out = append(out, at+": needs the \"platform\" the switch turns the agent onto")
		} else if prob := PlatformProblem(at+".platform", s.Platform); prob != "" {
			out = append(out, prob)
		}
		agent, name, ok := strings.Cut(s.Surface, "/")
		switch {
		case !ok || agent == "" || name == "" || strings.Contains(name, "/"):
			out = append(out, fmt.Sprintf("%s.surface: %q must be \"<agent>/<name>\", one of this "+
				"pack's own config surfaces", at, s.Surface))
		case agent != c.Bin:
			out = append(out, fmt.Sprintf("%s.surface: %q is agent %q's, and a switch is in this "+
				"program's (%q) own config", at, s.Surface, agent, c.Bin))
		}
		if steps, err := jsonptr.Parse(s.Pointer); err != nil || len(steps) == 0 {
			out = append(out, fmt.Sprintf("%s.pointer: %q must be an RFC 6901 pointer to a key "+
				"(\"/env/NAME\"), not the whole file", at, s.Pointer))
		}
	}
	return out
}

// PlatformProblem reports what is wrong with a platform value at path ("" when nothing is): it
// must be a non-empty token with no whitespace in it.
func PlatformProblem(path, v string) string {
	switch {
	case v == "":
		return path + ": an empty platform names no service — omit the key instead"
	case strings.IndexFunc(v, unicode.IsSpace) >= 0:
		return fmt.Sprintf("%s: %q carries whitespace, so it could never equal a platform a "+
			"derive recognizes (a platform is one token, such as \"aws-bedrock\")", path, v)
	}
	return ""
}
