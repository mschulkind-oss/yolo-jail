package packdecl

// supersedes.go is the `supersedes` half of capability supersession
// (docs/design/pack-capabilities.md §2): a pack's claim that some capability's job
// no longer needs doing, so whichever loophole serves it can stop.
//
// # Why it is a TOP-LEVEL key and not a 16th contribution kind
//
// The design's OQ-CAP says top-level, and the code agrees for a reason the design
// only gestures at. Every one of the 15 kinds is a CONTRIBUTION — something the
// pack DELIVERS at a target — and kinds.go types that literally: each has a
// `Footprint` carrying a `Combine` rule for "how two claims on the SAME target
// resolve", which is what `packload.Collisions` keys on. Supersession delivers
// nothing and owns no target, so it has no combine rule to state: two packs
// superseding one capability is not a collision, it is the mechanism working (§5 —
// "any supersession wins, there is deliberately no `needs`"). A kind whose combine
// rule has to be "never conflicts, and also there is no target" is a category
// error, and it would additionally have to be excluded by hand from the generic
// exclusive-target pass, the disclosure classifier, the host-render census and the
// per-kind docs test — four exclusions for something that is not a contribution.
//
// The shape it DOES match is `skills_tier`: a per-pack fact about how the pack
// relates to its environment, declared once, beside `name`. That is where it goes.
//
// # The asymmetry with `serves`, enforced
//
// `serves` (on a LOOPHOLE manifest, internal/loopholedecl) is a bare string list.
// `supersedes` requires an object with a MANDATORY `because`. Saying "this is my
// job" needs no justification; saying "your job does not need doing" is an
// assertion about code you did not write, and the person who later finds their
// loophole absent is owed the sentence. The `because` is not decoration: it is
// printed wherever the supersession takes effect (`yolo loopholes list`/`status`,
// InactiveReason) and in `yolo pack footprint`, so the justification travels with
// the consequence.

import (
	"fmt"
	"unicode"
)

// Supersession is one pack's claim that a capability's job no longer needs doing.
//
// It is a claim that DEMAND VANISHED, not that SUPPLY MOVED (design §2). The test
// is: after this pack is selected, does the job still need doing? "No, the demand
// is gone" is `supersedes`; "yes, and I do it now" is provision, which is
// deliberately not expressible. Superseding when you meant providing silently stops
// the job being done with nothing taking over, and nothing in the system can detect
// it — which is exactly why `because` is mandatory and printed.
type Supersession struct {
	// Capability is the named job, matched for EXACT equality against a loophole
	// manifest's `serves` entries. Never a loophole NAME: the capability is the
	// invariant and the loophole is the implementation, so naming the loophole
	// couples the pack to one implementation, breaks silently when it is renamed,
	// and says "turn that thing off" where the true statement is "that job does not
	// need doing" (design §3).
	Capability string `json:"capability"`
	// Because is the MANDATORY justification, printed wherever the supersession
	// takes effect. Required, and that is the whole asymmetry with `serves`.
	Because string `json:"because"`
}

// Supersessions returns the pack's supersession claims, in declaration order. THE
// accessor — no reader touches the field, matching how every other manifest fact is
// read through a method.
func (m *Manifest) Supersessions() []Supersession { return m.Supersedes }

// CapabilityNameProblem reports why a capability name is unusable, or "" when it is
// fine.
//
// A DELIBERATE MIRROR of loopholedecl.CapabilityNameProblem, which this package may
// not call: packdecl has zero internal imports by design (see the package doc), and
// both ends of a rendezvous have to agree about what a name IS. The duplication is
// pinned by a table test in internal/packload — the one package that imports both —
// so a divergence fails a test instead of producing a name one side accepts and the
// other cannot match.
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

// validateSupersedes reports every structural problem with the `supersedes` list.
//
// It runs on the TOLERANT path as well as the strict one, and that is safe for the
// reason validateSkillsTier's enum is NOT: every rule here is version-invariant. A
// missing `because`, an empty `capability`, a control character, a duplicate — all
// four are typos both ends of a version boundary agree about, so refusing them
// cannot become the `tier` incident a fourth time. There is deliberately no enum of
// known capability names to go stale (design §6: a yolo-owned registry would rebuild
// the agent registry the pack system exists to avoid).
//
// WHAT IS NOT CHECKED HERE, and could not be: whether any loophole actually SERVES
// the named capability. The namespace is closed by the loopholes present on a
// machine, which is not a fact about pack.json — internal/loopholes reports an
// unmatched claim where both halves are in hand (supersede.go).
func (m *Manifest) validateSupersedes() []string {
	var problems []string
	seen := map[string]int{}
	for i, s := range m.Supersedes {
		if prob := CapabilityNameProblem(s.Capability); prob != "" {
			problems = append(problems, fmt.Sprintf(
				"supersedes[%d].capability %q: %s", i, s.Capability, prob))
		}
		if s.Because == "" {
			problems = append(problems, fmt.Sprintf(
				"supersedes[%d]: \"because\" is required — `serves` is a statement about "+
					"yourself and costs nothing, while superseding is a claim that ANOTHER "+
					"component's job does not need doing. It is printed wherever the "+
					"supersession takes effect (`yolo loopholes list`, `yolo pack footprint`), "+
					"so whoever finds their loophole absent learns why. Say what makes the "+
					"job unnecessary, e.g. \"Bedrock overrides the OAuth path; no token is "+
					"ever refreshed\"", i))
		} else if prob := becauseProblem(s.Because); prob != "" {
			problems = append(problems, fmt.Sprintf("supersedes[%d].because: %s", i, prob))
		}
		if prev, dup := seen[s.Capability]; dup && s.Capability != "" {
			problems = append(problems, fmt.Sprintf(
				"supersedes[%d].capability %q is already claimed by supersedes[%d] — one pack "+
					"supersedes a capability once, and two entries would give one consequence "+
					"two different reasons", i, s.Capability, prev))
			continue
		}
		seen[s.Capability] = i
	}
	return problems
}

// becauseProblem refuses a `because` that cannot safely be printed.
//
// Same gate placement as loopholedecl's sanitize.go, and for the same measured
// reason: the string is rendered by `yolo pack footprint` through
// richtext.Printer.Printf, which formats FIRST and parses style tags over the
// result, so a newline here can forge an entire extra claim line and a raw ESC can
// erase the one above it. Refusing at load beats escaping at every renderer.
func becauseProblem(because string) string {
	for _, r := range because {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Sprintf("contains a control character (U+%04X) — this text is printed"+
				" in the pack footprint, which formats the line before it parses style tags,"+
				" so a newline could forge a claim line and an ESC could erase the one above"+
				" it; remove it", r)
		}
	}
	return ""
}
