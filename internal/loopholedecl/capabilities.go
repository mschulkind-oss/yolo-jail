package loopholedecl

// capabilities.go is the `serves` half of capability supersession
// (docs/design/pack-capabilities.md §2).
//
// A CAPABILITY IS A NAMED JOB, not a name for the thing that does the job:
// `claude-oauth-refresh` is "serializing OAuth token refreshes so concurrent
// consumers do not burn a single-use refresh token", and the `claude-oauth-broker`
// loophole is one implementation of it.
//
// `serves` is A STATEMENT ABOUT ITSELF, so it is a bare string list and costs
// nothing. The other verb — `supersedes`, a claim that some OTHER component's job
// no longer needs doing — lives on the PACK manifest (internal/packdecl) and costs
// a mandatory `because`. That asymmetry is the design's own principle: a claim
// about yourself is cheap; a claim about code you did not write is not.
//
// STATIC VALIDATION ONLY, like everything else in this package. Whether a served
// capability is actually superseded needs the set of selected packs and the set of
// discovered loopholes, neither of which is a fact about this manifest — that is
// internal/loopholes' business (supersede.go).

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// CapabilityNameProblem reports why a capability name is unusable, or "" when it
// is fine.
//
// EXPORTED, and deliberately mirrored by packdecl.CapabilityNameProblem, because
// the two ends of a rendezvous have to agree about what a name IS: `serves` is
// written on a loophole manifest and `supersedes` on a pack manifest, and packdecl
// has zero internal imports by design, so it cannot call this one. The two
// implementations are pinned against each other by a table test in
// internal/packload (capabilityrule_test.go), which is the one package that
// imports both — duplication that drifts becomes a test failure rather than two
// spellings of one rule.
//
// Three rules, each with a consequence rather than a preference behind it:
//
//   - NON-EMPTY. An empty name matches nothing and reads as a declaration.
//   - NO CONTROL CHARACTERS. A capability name and its `because` are printed by
//     `yolo pack footprint` through richtext.Printer.Printf, which formats FIRST
//     and parses style tags over the result — so a newline in either can forge an
//     extra footprint line. Same argument, same gate placement, as sanitize.go.
//   - NO WHITESPACE. The name is a rendezvous key compared for EXACT equality;
//     "claude-oauth-refresh " and "claude-oauth-refresh" would look identical in
//     every message and match nothing.
func CapabilityNameProblem(name string) string {
	if name == "" {
		return "must be a non-empty string"
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Sprintf("contains a control character (U+%04X) — a capability name is"+
				" printed in the pack footprint, which formats the line before it parses"+
				" style tags, so a newline could forge a claim line", r)
		}
		if unicode.IsSpace(r) {
			return "contains whitespace — a capability name is a rendezvous key matched for" +
				" exact equality, so two spellings that look identical would match nothing"
		}
	}
	return ""
}

// parseServes decodes the optional `serves` list: the capabilities THIS loophole
// implements.
//
// ABSENT AND EMPTY MEAN THE SAME THING — "not participating" — and that is the
// rule the whole mechanism rests on (design §4): silence is never a default claim,
// so adding supersession cannot change the behaviour of any manifest, first- or
// third-party, that does not opt in. An empty declared list is therefore accepted
// rather than refused, unlike `platforms`, where "supports nothing" and "supports
// everything" genuinely needed different representations.
//
// A duplicate entry is refused: `serves` is a SET, and the `every served
// capability is superseded` rule counts entries, so a repeated name would be a
// declaration whose meaning depends on how many times it was written.
func parseServes(manifestPath string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'serves' must be a list of capability names", manifestPath)
	}
	out := []string{}
	seen := map[string]bool{}
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr {
			return nil, Errorf("%s: serves[%d] must be a string capability name", manifestPath, i)
		}
		if prob := CapabilityNameProblem(s); prob != "" {
			return nil, Errorf("%s: serves[%d] = %s %s", manifestPath, i, pytext.Repr(s), prob)
		}
		if seen[s] {
			return nil, Errorf("%s: serves[%d] = %s is declared twice — `serves` is a set,"+
				" and supersession retires a loophole only when EVERY capability it serves"+
				" is superseded, so a repeated name is a declaration whose meaning depends"+
				" on how often it was written", manifestPath, i, pytext.Repr(s))
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ServesCapability reports whether this manifest declares the named capability.
func (m *Manifest) ServesCapability(capability string) bool {
	for _, c := range m.Serves {
		if c == capability {
			return true
		}
	}
	return false
}

// JoinCapabilities renders a capability list for a message, in declaration order.
func JoinCapabilities(caps []string) string { return strings.Join(caps, ", ") }
