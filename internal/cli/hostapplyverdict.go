package cli

// hostapplyverdict.go is docs/design/report-tiers.md §4.3's VERDICT BLOCK: the sentence a host
// apply ends with, the counts that evidence it, and the posture footer.
//
// IT IS THE THESIS (P7). The command used to hand over 277 true lines and leave the arithmetic
// to the reader — "8 in sync, 76 would change" and a `nothing written` line, neither of which
// says whether the thing worked. Nothing in that report distinguished a home that would apply
// cleanly from one where a declared dependency is missing and the rest of the apply is
// therefore pointless. The verdict states the RESULT; the counts are underneath it because a
// reader who stops after one line still has the answer.
//
// THREE PROPERTIES, each of which was a measured defect before it was a rule:
//
//   - IT PRINTS ON EVERY PATH, IN BOTH POSTURES. The roll-up it replaces sat inside
//     `if !write`, so an --assert run ended with no summary at all; and the zero-packs branch
//     returns before it, so an empty `packs` dry run ended with no count, no verdict and no
//     footer (§5, verified 2026-09-11). Both are now callers of printHostApplyVerdict.
//   - IT STATES THE OUTCOME, NEVER THE WORK. "6 config files would change" is an
//     observation; "an --assert would complete" is a result. The counts may say the former
//     only after the verdict has said the latter.
//   - EVERY TIER-3 CLASS IS REPRESENTED IN IT (§4.4). A loss contributes a count, a blocker
//     contributes its NAME — grouping may compress the lines above the verdict, it may never
//     leave the verdict silent about a class.
//
// WHAT IT DELIBERATELY DOES NOT COVER: the early refusals (an `agents` selector naming nobody,
// a doubly-owned surface, a name claimed twice, a declined loss confirmation). Each already
// ends in its own result sentence naming that nothing was written, which is exactly §4.3's
// last table row — "today's refusal lines, unchanged" — so a second verdict there would be a
// second sentence about one outcome.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// printHostApplyVerdict ends one apply with the verdict line, the counts and the footer.
//
// zeroPacks is the branch flag rather than a survey property on purpose: "no packs are
// configured" and "every configured pack happened to change nothing" are different results
// with different next actions, and the survey cannot tell them apart — both reach it as an
// empty changed set.
func printHostApplyVerdict(pr richtext.Printer, s *hostApplySurvey, home string,
	write, zeroPacks bool) {
	pr.Printf("[bold]%s[/bold]", hostApplyVerdict(s, write, zeroPacks))
	for _, line := range hostApplyCounts(s, write) {
		pr.Printf("  [dim]%s[/dim]", line)
	}
	if write {
		// The footer states the POSTURE, never this run's action: the verdict above has
		// already said what happened, and an --assert over a settled home writes nothing,
		// so a footer claiming a write would contradict the sentence it sits under.
		pr.Printf("[dim]assert — this posture writes into %s. Without --assert it is a dry "+
			"run.[/dim]", home)
		return
	}
	// The footer names the DETAIL FLAG as well as the writing posture (§4.2): the default
	// view counts what it does not itemize, so the reader who wants the destinations has to
	// be told the one word that produces them.
	pr.Printf("[dim]dry run — nothing was written into %s. `--assert` applies; `--verbose` "+
		"lists every destination.[/dim]", home)
}

// hostApplyVerdict is §4.3's verdict line: one sentence, in every posture, on every path,
// including the degenerate ones.
//
// The order of the cases IS the ruling. A blocker outranks "would complete" because a blocker
// is what decides the outcome, and a render failure outranks a blocker because it has already
// cost this run a pack's worth of surfaces — every count below is missing them, so the verdict
// must not claim a completed apply out of an incomplete traversal.
func hostApplyVerdict(s *hostApplySurvey, write, zeroPacks bool) string {
	switch {
	case zeroPacks:
		// §4.3's zero-packs row. The retire passes still run here (emptying `packs` is the
		// most complete drop there is), so the number they retired is the whole result.
		n := 0
		if s != nil {
			n = len(s.Changed)
		}
		if n == 0 {
			return "No packs configured — nothing to apply, and nothing left to retire."
		}
		if write {
			return fmt.Sprintf("No packs configured — nothing to apply; %d destination(s) retired.", n)
		}
		return fmt.Sprintf("No packs configured — nothing to apply; %d destination(s) "+
			"would be retired.", n)
	case len(s.FailedPacks()) > 0:
		failed := s.FailedPacks()
		if write {
			return fmt.Sprintf("Incomplete — %d pack(s) failed to render (%s); see stderr.",
				len(failed), strings.Join(failed, ", "))
		}
		return fmt.Sprintf("An --assert would be incomplete — %d pack(s) failed to render "+
			"(%s); see stderr.", len(failed), strings.Join(failed, ", "))
	case !write && len(s.MissingDeps()) > 0:
		// §4.9: in the DRY RUN a missing declared dependency is a tier-3 blocker that decides
		// the verdict and changes nothing else — exit 0, nothing written, nothing installed.
		// The names, not a count: the reader's next action is about those binaries.
		missing := s.MissingDeps()
		return fmt.Sprintf("An --assert would NOT complete: %d declared %s missing (%s).",
			len(missing), plural(len(missing), "dependency is", "dependencies are"),
			strings.Join(missing, ", "))
	case !s.Changes():
		if !write {
			return "Nothing to do — this home is up to date."
		}
		if p := installedPrefix(s); p != "" {
			return p + "nothing else to apply — this home is up to date."
		}
		return "Nothing to apply — this home is up to date."
	case write:
		if p := installedPrefix(s); p != "" {
			return p + "applied: " + hostApplyWork(s, true) + "."
		}
		return "Applied: " + hostApplyWork(s, true) + "."
	default:
		return "An --assert would complete."
	}
}

// installedPrefix is §4.3's "Installed `rg`; applied: …" — the clause an --assert leads with
// when the dependency gate installed something before the render (applyhostdepgate.go).
//
// It LEADS rather than trails because it is the half the counts cannot express: every other
// number in the verdict is about this home, and this one is about the machine's toolchain.
// Empty when nothing was installed, which is why each caller spells its sentence twice — the
// outcome clause is capitalized when it starts the sentence and lowercase when this one does.
//
// WHAT USED TO BE HERE was the inverse — a trailing "N declared dependencies still missing",
// carried because an --assert completed over an unready environment. It is GONE because that
// state is now unreachable: a writing run with a missing declared dependency is refused by the
// gate before the first render, so the verdict is never reached to state it.
func installedPrefix(s *hostApplySurvey) string {
	installed := s.InstalledDeps()
	if len(installed) == 0 {
		return ""
	}
	names := make([]string, 0, len(installed))
	for _, bin := range installed {
		names = append(names, "`"+bin+"`")
	}
	return fmt.Sprintf("Installed %s; ", strings.Join(names, ", "))
}

// workItem is one class of work this apply did or would do, in the units §4.3's count table
// specifies — files, skills, destinations; never the number of destinations a loop visited.
//
// TWO RENDERINGS of one count, because the verdict and the counts are different sentences.
// The verdict reads "Applied: 6 config files, 14 skills moved into your local pack" — one verb
// for the whole list — while the counts line states each class on its own and so each needs its
// own verb. Deriving one from the other by appending a word is what produced "14 skills would
// move into your local pack would change".
type workItem struct {
	// short is the verdict's form: the noun phrase, carrying a verb only when the class needs
	// one to be understood (a moved skill does; a changed file does not).
	short string
	// full is the counts line's form: the same count as a complete statement.
	full string
}

// hostApplyWorkItems is every non-zero class of work, in report order.
func hostApplyWorkItems(s *hostApplySurvey, wrote bool) []workItem {
	var out []workItem
	changed := "would change"
	if wrote {
		changed = "changed"
	}
	add := func(n int, one, many string) {
		if n > 0 {
			noun := fmt.Sprintf("%d %s", n, plural(n, one, many))
			out = append(out, workItem{short: noun, full: noun + " " + changed})
		}
	}
	add(s.ChangedOfKind("config"), "config file", "config files")
	if n := len(s.SkillNames(skillAdopted)); n > 0 {
		verb := "would move"
		if wrote {
			verb = "moved"
		}
		// The one class whose verb is load-bearing in BOTH sentences: "14 skills" alone
		// reads as fourteen skills being installed, which is the opposite of what an
		// adoption does to them.
		phrase := fmt.Sprintf("%d %s %s into your local pack", n, plural(n, "skill", "skills"), verb)
		out = append(out, workItem{short: phrase, full: phrase})
	}
	add(len(s.SkillNames(skillComposed)), "composed skill", "composed skills")
	add(len(s.SkillNames(skillRetired)), "retired skill", "retired skills")
	add(s.ChangedOfKind("briefing"), "briefing destination", "briefing destinations")
	add(s.ChangedOfKind("files"), "delivered file", "delivered files")
	add(s.ChangedOfKind("host_wrappers"), "wrapper directory", "wrapper directories")
	return out
}

// hostApplyWork is the verdict's form of the list above: what the apply did, in one clause.
func hostApplyWork(s *hostApplySurvey, wrote bool) string {
	items := hostApplyWorkItems(s, wrote)
	if len(items) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.short)
	}
	return strings.Join(parts, ", ")
}

// hostApplyCounts is §4.3's COUNTS: the evidence for the sentence above, in the units the
// reader cares about (P6). Two lines at most — what this apply moves, then what it costs —
// and a term appears only when its count is non-zero, because a row of zeroes is the
// arithmetic the verdict was supposed to replace.
//
// "In sync" is reported as the survey defines it and NOT as "compared and unchanged": the
// count deliberately includes destinations a render skipped or refused (see
// hostApplySurvey.InSync), so claiming they were compared would be a claim the render did not
// make (§3.4). Splitting the two is its own change, in the kinds' result structs.
func hostApplyCounts(s *hostApplySurvey, write bool) []string {
	if s == nil {
		return nil
	}
	var moved []string
	for _, it := range hostApplyWorkItems(s, write) {
		moved = append(moved, it.full)
	}
	if s.InSync > 0 {
		moved = append(moved, fmt.Sprintf("%d %s already in sync", s.InSync,
			plural(s.InSync, "destination", "destinations")))
	}
	var out []string
	if len(moved) > 0 {
		out = append(out, strings.Join(moved, " · "))
	}

	var cost []string
	if keys, files := s.ReplacedValues(); keys > 0 {
		verb := "would be replaced"
		if write {
			verb = "were replaced"
		}
		// "N of your values", plural even at one: "one of your values" is the grammatical
		// reading of the construction, and "1 of your value" is not a sentence.
		cost = append(cost, fmt.Sprintf("%d of your values %s in %d %s", keys, verb,
			files, plural(files, "file", "files")))
	}
	if entries, surfaces := s.DroppedEntries(); entries > 0 {
		verb := "would be dropped"
		if write {
			verb = "were dropped"
		}
		cost = append(cost, fmt.Sprintf("%d of your entries %s from %d %s", entries, verb,
			surfaces, plural(surfaces, "surface", "surfaces")))
	}
	if present, missing, notProbed := s.Deps(); present+missing+notProbed > 0 {
		dep := fmt.Sprintf("%d declared %s present", present,
			plural(present, "dependency", "dependencies"))
		if missing > 0 {
			dep += fmt.Sprintf(", %d missing (%s)", missing, strings.Join(s.MissingDeps(), ", "))
		}
		if notProbed > 0 {
			dep += fmt.Sprintf(", %d not probed", notProbed)
		}
		cost = append(cost, dep)
	}
	if n := s.DroppedComments(); n > 0 {
		// §4.4's other no-remedy class, and the one the verdict was silent about: a dropped
		// comment already counted as a tier-3 loss (configResultTier) with no term anywhere
		// in this block, so the one class whose loss cannot be undone was the one a reader
		// of the last line could miss entirely.
		verb := "would lose"
		if write {
			verb = "lost"
		}
		cost = append(cost, fmt.Sprintf("%d %s %s comments of yours", n,
			plural(n, "surface", "surfaces"), verb))
	}
	if s.FirstApply() {
		// The flag, not a count: it is what turns every loss above into a confirmation
		// prompt on --assert, so a reader seeing losses needs to know which side of the
		// one-way door this home is on.
		cost = append(cost, "first apply into this home")
	}
	if len(cost) > 0 {
		out = append(out, strings.Join(cost, " · "))
	}
	return out
}
