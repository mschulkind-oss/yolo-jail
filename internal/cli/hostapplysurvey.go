package cli

// hostapplysurvey.go accumulates THE CHANGE PREDICATE across one whole host apply
// (docs/design/host-apply-staleness.md §3.4, §10 step 1).
//
// Each of the four written kinds computes the predicate for its own destinations — see
// entrypoint.HostRenderResult.WouldChange and hostskills.Result.WouldChange — and each already
// reports per-destination lines. What was missing is the ROLL-UP: "would an --assert change
// anything in this home, and what?" A dry run needs it to stop printing N identical
// `would render` lines over a home that is already correct, and §4.3's launch gate needs
// exactly the same answer to decide between exec'ing silently and stopping to ask.
//
// ONE collector, both consumers, and that is deliberate. The launch gate does not re-derive
// the answer from its own observe pass: it runs the SAME applyHost in observe posture with the
// output discarded and reads the survey it filled in. A second traversal of the four kinds
// would be a second thing to drift out of step with the apply it is supposed to describe.

import "fmt"

// hostChange is one destination an --assert would alter.
type hostChange struct {
	// Kind is the written kind that owns the destination, as the user sees it in the report
	// (`config`, `skills`, `briefing`, `files`, `host_wrappers`).
	Kind string
	// Surface is the reported identity of the thing — "claude/settings", a skill name, a
	// pack's briefing.
	Surface string
	// Path is the absolute destination in the home.
	Path string
}

// hostApplySurvey is the accumulated predicate for one apply.
//
// Every method is nil-safe, so a caller that does not want the roll-up passes nil rather than
// threading an unused value through five signatures.
type hostApplySurvey struct {
	// InSync counts the destinations an --assert would leave exactly as they are.
	//
	// "As they are" is the literal claim, and it deliberately includes a surface the render
	// REFUSES to touch: the report gives every refusal its own loud line, and folding them into
	// the changed set instead would make a launch gate stop on a condition applying cannot fix
	// — a prompt whose remedy does not exist (§4.4's cannot-determine class).
	InSync int
	// Changed lists the destinations the render would alter, in report order.
	Changed []hostChange
}

// note records one result's verdict. A result with no PATH is not a destination — an ownerless
// config patch, a pack-level refusal — and is counted in neither bucket.
func (s *hostApplySurvey) note(kind, surface, path string, wouldChange bool) {
	if s == nil || path == "" {
		return
	}
	if !wouldChange {
		s.InSync++
		return
	}
	s.Changed = append(s.Changed, hostChange{Kind: kind, Surface: surface, Path: path})
}

// Changes reports whether an --assert would alter anything at all. This is the whole question
// §4.3's table branches on.
func (s *hostApplySurvey) Changes() bool { return s != nil && len(s.Changed) > 0 }

// Summary is the one-line roll-up a dry run ends with.
func (s *hostApplySurvey) Summary() string {
	if s == nil {
		return "0 in sync, 0 would change"
	}
	return fmt.Sprintf("%d in sync, %d would change", s.InSync, len(s.Changed))
}
