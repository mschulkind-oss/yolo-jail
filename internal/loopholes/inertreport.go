package loopholes

// inertreport.go is THE INERT REPORT: "this loophole does nothing here, and here is
// why."
//
// §3.1 and §8 are the same situation on two axes, and the design is explicit that
// they share ONE message and ONE mechanism: platform (darwin vs linux) and backend
// (`container` and `macos-user` skip loopholes entirely) both answer that one
// sentence. Two mechanisms would give two half-messages for one user-visible
// situation, which is the B-0 shape — "a backend that looked provisioned and
// configured nothing" — that the run pipeline was restructured to end.
//
// So the mechanism is a VALUE, not a print: producers hand back notes, one caller
// renders them. This file supplies the PLATFORM producer, because the platform
// declaration is evaluated here; the BACKEND producer belongs with the code that
// knows which backend is running, and plugs in by constructing an InertNote with
// AxisBackend. Neither producer prints, so neither can print twice.

// Inert-report axes. An axis says WHICH fact made the loophole inert, so a reader
// looking at two lines can tell "wrong machine" from "wrong backend" without
// parsing the sentence.
const (
	// AxisPlatform: the manifest's `platforms` declaration excludes this
	// GOOS/GOARCH. Nothing can be installed to fix it.
	AxisPlatform = "platform"
	// AxisBackend: the container backend skips loopholes wholesale (§8 — Apple
	// Container returns before any external service starts; macos-user returns
	// before startLoopholes is reached at all). The loophole is fine; the backend
	// does not carry it.
	AxisBackend = "backend"
)

// InertNote is one loophole that will do nothing on this machine, with the reason
// and the axis it came from.
//
// Name is carried separately from the sentence so a caller can group, sort or
// filter by loophole without string surgery — §3.1 requires the report be BY NAME,
// and a name that only exists inside a rendered sentence is a name no other code
// can use.
type InertNote struct {
	Name string
	Axis string
	// Reason continues "loophole <name> is ": a clause, not a sentence, and never
	// capitalized. It carries its own trailing detail (the platforms the loophole
	// does support, the backend that skipped it).
	Reason string
}

// Line renders the note as the one line a user sees. THE single rendering, so the
// platform axis and the backend axis cannot drift into two shapes.
func (n InertNote) Line() string {
	return "loophole " + n.Name + " is " + n.Reason
}

// PlatformInertNotes evaluates the `platforms` declaration of every loophole
// against THIS machine and returns one note per loophole that is unsupported here.
//
// ONCE PER LOOPHOLE, by name: discovery merges four sources and a launch calls it
// from several places, so the dedup lives here rather than in each caller. A
// duplicated "unsupported on linux/amd64" line reads as two problems.
//
// A loophole DISABLED by the user is skipped: it is inert for a reason the user
// chose, already reported as "disabled", and telling them a switched-off loophole
// also has the wrong platform is noise. `requires` is deliberately NOT consulted —
// the whole point of the axis split is that an unmet requirement and an unsupported
// platform are different answers with different fixes, so a loophole that is both
// gets the categorical one.
func PlatformInertNotes(lps []*Loophole) []InertNote {
	var out []InertNote
	seen := map[string]bool{}
	for _, lp := range lps {
		if lp == nil || !lp.Enabled || seen[lp.Name] {
			continue
		}
		reason, ok := lp.UnsupportedHereReason()
		if !ok {
			continue
		}
		seen[lp.Name] = true
		out = append(out, InertNote{Name: lp.Name, Axis: AxisPlatform, Reason: reason})
	}
	return out
}
