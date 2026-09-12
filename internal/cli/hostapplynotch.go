package cli

// hostapplynotch.go says docs/design/report-tiers.md §4.1's TIER-1 facts ONCE per run: which
// contribution kinds DO NOT APPLY at the host notch, and which autonomy posture this notch
// renders.
//
// A tier-1 fact is true of the NOTCH rather than of the home, so it is the same sentence on
// every machine — and the report printed it per CONTRIBUTION. Measured in this jail on
// 2026-09-10: nineteen refusal paragraphs naming seven kinds (`state` ×6, `hook` ×4,
// `loophole` ×4, `reads-host` ×2, `env`/`provider`/`profile` ×1) and five identical autonomy
// lines, ~40 words each, byte-identical in any home that selects the same packs (§2.1, §3.2).
// P1 says a property of the notch is stated once per run; this file is that once.
//
// THE CENSUS INVARIANT IS UNCHANGED, and P5 is why: it is *named*, not *itemized* — "a kind
// the host notch refuses appears in the report, and appearing once is appearing". The line
// below names every such kind, so TestApplyHostAccountsForEveryDeclaredKind still holds for
// the same reason it held before (nothing a pack declares is silently absent); what it no
// longer holds for is nineteen chances to notice.
//
// TWO MAPS, ONE ANSWER. The reasons the old lines carried came from two different places —
// render.refusalReasons for a kind the host FieldSet does not honor (state, mount, reads-host,
// loophole), render.hostUnimplemented for one it honors with no renderer behind it (env,
// launch, hook, profile, provider) — and the report told them apart by printing the same word
// with a different string. The reader's question is the same in both cases and so is the
// answer, so notchInapplicable folds them: a kind is either something this notch does with
// your home or it is not. Collapsing only one of the two maps would have collapsed eleven of
// the nineteen lines and left the rest.
//
// WHAT LEFT ALTOGETHER IS THE RATIONALE (P8, §4.6): the ~40-word reasons stay in
// internal/render — they are still what the code decides by — and NO TERMINAL VIEW PRINTS
// THEM, at any verbosity. They live in the manual now, as list entries in config_ref.txt's
// host-notch section under a drift gate that reads both of render's maps (§9 step 6). This
// line names the kinds and points there.
//
// THE WORD IS `does not apply`, NEVER `refused` (§4.6's closed vocabulary). `refused` belongs
// to an apply that STOPPED — a doubly-owned surface, an `agents` selector naming nobody — and
// a kind that has no meaning off-container stopped nothing. The old line offered a remedy
// ("Launch a jail to run it") for a problem the reader does not have, which §3.5 calls a notch
// fact wearing a warning's word.

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// notchFacts is what the selected pack set implies about THIS notch — the tier-1 half of one
// apply's report, collected before the first render so the lines it produces can say "below"
// about the surfaces and mean it.
type notchFacts struct {
	// Inapplicable is every declared kind this notch does nothing with, deduplicated and
	// sorted. Deduplicated because the kind is the unit: which pack declared it is a
	// property of the pack, and §4.1 puts that behind --verbose (§9 step 6).
	Inapplicable []packdecl.Kind
	// Autonomy is whether any pack declares the kind at all. The POSTURE is the notch's and
	// cannot differ between packs in one run — render.Host(...).Profile().AgentAutonomy
	// resolves to guarded here — so the only per-run question is whether to say it.
	Autonomy bool
	// AutonomyFolds is whether the SELECTED posture actually patches a config surface, which
	// is a different question from whether the kind is declared and became a different ANSWER
	// the day copilot shipped an autonomy contribution whose autonomous posture is a launch
	// flag and whose guarded posture is absent (a flag has no persistence, so not selecting it
	// IS the tightening). The line below promises the fold "in the config surfaces below", and
	// for a pack set like that one there is nothing below to point at.
	AutonomyFolds bool
}

// surveyNotchFacts walks every contribution the resolved pack set declares and collects the
// tier-1 facts, touching nothing and printing nothing.
//
// It runs over `loaded` AFTER packload.ResolveDestinations, for the same reason the render
// loop does: a zero-ceremony pack's contributions are whatever the resolution gave it, and a
// census taken before that would report a different set from the one the apply acts on.
func surveyNotchFacts(loaded []*packload.Pack, fields render.FieldSet) notchFacts {
	var f notchFacts
	seen := map[packdecl.Kind]bool{}
	// The posture this notch selects, read off render's ONE notch->preset table rather than
	// spelled `false` here: the survey must fold what the render folds, and a literal is how
	// the two come apart.
	hostAutonomy := render.ProfileFor(render.KindHost).AgentAutonomy
	for _, p := range loaded {
		if posture := p.Decl.PostureFor(hostAutonomy); posture != nil && len(posture.Config) > 0 {
			f.AutonomyFolds = true
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindAutonomy {
				f.Autonomy = true
			}
			if seen[c.Kind] || !notchInapplicable(fields, c.Kind) {
				continue
			}
			seen[c.Kind] = true
			f.Inapplicable = append(f.Inapplicable, c.Kind)
		}
	}
	sort.Slice(f.Inapplicable, func(i, j int) bool { return f.Inapplicable[i] < f.Inapplicable[j] })
	return f
}

// notchInapplicable is the ONE predicate for "this notch does nothing with this kind", over
// both of render's maps (see the two-maps note at the top of this file). The apply's render
// loop asks the same question with the same call, so the line and the loop cannot disagree
// about which kinds produced no surface.
func notchInapplicable(fields render.FieldSet, k packdecl.Kind) bool {
	if !fields.Honors(k) {
		return true
	}
	_, unbuilt := render.HostUnimplemented(k)
	return unbuilt
}

// printNotchFacts prints the tier-1 half of the report: at most two lines, whatever the pack
// set's size.
//
// Both lines point somewhere rather than explaining themselves (P3/P8). The kinds line names
// `yolo config-ref`, which is where §4.6 moved the REASONS — as list entries under a drift gate
// of their own (TestEveryHostNotchInapplicableKindHasItsReasonDocumented, which reads BOTH of
// render's maps, so a refused kind and an honored-but-unbuilt one are covered alike); the
// autonomy line names what it did to the surfaces, because
// "did my jail-bypass keys reach my real home?" is the single most consequential question this
// command answers and the answer is one word.
func printNotchFacts(pr richtext.Printer, f notchFacts) {
	if len(f.Inapplicable) > 0 {
		names := make([]string, len(f.Inapplicable))
		for i, k := range f.Inapplicable {
			names[i] = string(k)
		}
		pr.Printf("  [dim]%d %s %s at the host notch: %s (`yolo config-ref` says why)[/dim]",
			len(names), plural(len(names), "kind", "kinds"),
			plural(len(names), "does not apply", "do not apply"), strings.Join(names, ", "))
	}
	if f.Autonomy {
		where := "folded into the config surfaces below"
		if !f.AutonomyFolds {
			// SAY SO rather than dropping the line. The posture is still the answer to "did my
			// jail-bypass keys reach my real home?" — for a pack whose autonomy is a launch
			// flag alone the answer is no, and there was nothing to fold — but pointing at
			// surfaces that carry no patch would be a promise the report below does not keep.
			where = "no selected pack's guarded posture patches a config surface here"
		}
		pr.Printf("  [cyan]autonomy[/cyan]   guarded posture — permission prompts stay ON; %s", where)
	}
}
