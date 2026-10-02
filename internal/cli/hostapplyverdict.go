package cli

// hostapplyverdict.go is docs/reference/report-tiers.md's VERDICT BLOCK: the sentence a host
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
//     footer (verified 2026-09-11). Both are now callers of printHostApplyVerdict.
//   - IT STATES THE OUTCOME, NEVER THE WORK. "6 config files would change" is an
//     observation; "an --assert would complete" is a result. The counts may say the former
//     only after the verdict has said the latter.
//   - EVERY TIER-3 CLASS IS REPRESENTED IN IT (the remedy contract). A loss contributes
//     a count, a blocker contributes its NAME — grouping may compress the lines above the
//     verdict, it may never leave the verdict silent about a class. ADOPTION is the class that
//     was missing (OQ-CO7 D3): it reproduces the file's bytes on the `assert` -> `own` switch,
//     so it reaches here as a destination that would not change and it used to contribute
//     nothing at all, under an unsuppressible line naming the copy the run had just taken. Two
//     surfaces disagreeing about one run is what this block exists to make impossible.
//
// WHAT IT DELIBERATELY DOES NOT COVER: the early refusals (an `agents` selector naming nobody,
// a doubly-owned surface, a name claimed twice, a declined loss confirmation). Each already
// ends in its own result sentence naming that nothing was written, which is exactly the verdict
// block's last table row — "today's refusal lines, unchanged" — so a second verdict there would
// be a second sentence about one outcome.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/updatehint"
)

// printHostApplyVerdict ends one apply with the verdict line, the counts and the footer.
//
// The home and the no-packs branch are READ FROM THE SURVEY rather than passed, because
// there is a second consumer now — the machine document — and two consumers each
// given their own copy of a fact is how the two come to disagree about it. The survey still
// cannot DERIVE either one (both a zero-packs run and a settled home reach it as an empty
// changed set, which is why they are recorded at the one place each is known), and that is
// the distinction: recorded, not inferred.
func printHostApplyVerdict(pr richtext.Printer, s *hostApplySurvey, write bool) {
	home := s.Home()
	pr.Printf("[bold]%s[/bold]", hostApplyVerdict(s, write))
	for _, line := range hostApplyCounts(s, write) {
		pr.Printf("  [dim]%s[/dim]", line)
	}
	if write {
		// NO FOOTER UNDER --assert. The reader typed the flag, the header already says
		// "applying into <home>", and the verdict says what happened; a line explaining what
		// the flag they just typed means ("Without --assert it is a dry run") was the one line
		// in the maintainer's report that told them nothing at all.
		return
	}
	// The footer names the DETAIL FLAG as well as the writing posture (the default view): the
	// default view counts what it does not itemize, so the reader who wants the destinations has
	// to be told the one word that produces them.
	pr.Printf("[dim]dry run — nothing was written into %s. `--assert` applies; `--verbose` "+
		"lists every destination.[/dim]", home)
}

// hostApplyOutcome is the verdict as a STABLE TOKEN — the machine document's answer to
// "how did it go", and the thing the sentence below is rendered from.
//
// THE ORDER OF THE CASES IS THE RULING, and it lives here rather than in the sentence
// builder so that the two forms cannot disagree about the outcome they are reporting. A
// blocker outranks "would complete" because a blocker is what decides the outcome, and a
// render failure outranks a blocker because it has already cost this run a pack's worth of
// surfaces — every count is missing them, so no verdict may claim a completed apply out of
// an incomplete traversal.
//
// The tokens name the OUTCOME, never the posture: `nothing_to_do` is the same finding in a
// dry run and an --assert, and the sentences differ because the sentences are what the
// posture changes.
func hostApplyOutcome(s *hostApplySurvey, write bool) string {
	switch {
	case len(s.UnresolvedPacks()) > 0 || s.inputsRefused():
		// FIRST AMONG THE BLOCKERS: an --assert over an incomplete pack set writes nothing at all
		// (no half states), so no count below describes anything it would do. Nor over a config
		// whose providers or profiles the host refuses (stageInputs), which every surface reads:
		// the apply refuses there, before its verdict, so only the dry run's document says it.
		return outcomeRefused
	case len(s.Failures()) > 0 || len(s.StageFailures()) > 0 || len(s.FloorRefusals()) > 0 ||
		len(s.FloorFailures()) > 0:
		// A program yolo's floor will not install over a newer yolo's record is this outcome too,
		// in both postures, and one whose install an --assert tried and failed: an --assert writes
		// the rest and exits 1, as it does for a destination it cannot write. The verdict read the
		// floor stage not at all, and said "this home is up to date" under the line saying the
		// floor would not, or could not, install it. A failed stage (stageSkills, …) is this
		// outcome for the same reason: the verdict read none of them, and an --assert whose skills
		// stage was refused exited 1 under "Nothing to apply — this home is up to date."
		return outcomeIncomplete
	case s.ZeroPacks():
		// BELOW THE BLOCKERS, as nothing_to_do is: an empty `packs` still runs the retire, the
		// wrappers and the rest (applyHostSurveyed's zero-packs branch), and one of them failing
		// ended "No packs configured — nothing to apply, and nothing left to retire." It cannot
		// be refused: an empty `packs` names no pack that could fail to resolve.
		return outcomeNoPacks
	case !write && len(s.MissingDeps()) > 0:
		return outcomeBlocked
	case !s.Changes() && s.Adoptions() == 0 && !s.retireWaiting():
		// THE ADOPTION HALF IS NOT REDUNDANT WITH Changes(), which is the whole of OQ-CO7's
		// D3: an adoption composes the file out of what it already holds, so the canonical
		// one reproduces the bytes, reports WouldChange=false and leaves Changed empty —
		// while having copied the user's file into a slot there is one of, forever. A run
		// that walked through a one-way door is not a run with nothing to do, in either
		// posture's spelling of the sentence.
		//
		// NOR IS THE RETIRE HALF (rule 5): a dry run that lists a dropped pack's paths as
		// `would archive` has work for an --assert, which the launch gate's Changes() does not
		// count (hostApplySurvey.retirePaths). It ended "Nothing to do — this home is up to date."
		return outcomeNothingToDo
	case write:
		return outcomeApplied
	default:
		return outcomeWouldComplete
	}
}

// The outcome vocabulary. Stable tokens: a consumer branches on these, so they change only
// when the set of distinguishable outcomes does.
const (
	// outcomeNoPacks — no packs are configured. Different from nothing_to_do, and the
	// difference is the next action: one is "your config names nothing", the other "your
	// home already matches what it names".
	outcomeNoPacks = "no_packs"
	// outcomeRefused — a configured pack could not be resolved, or the config's providers or
	// profiles cannot be composed for the host (stageInputs), so an --assert refuses the whole
	// apply and writes nothing (dry run only: an --assert refuses before the verdict).
	outcomeRefused = "refused"
	// outcomeIncomplete — a pack failed to render, so the counts are missing its surfaces, or a
	// destination could not be written, or a stage failed (stageSkills, …), or yolo's floor will
	// not install a program over a newer yolo's record, or an --assert's floor install failed. An
	// --assert writes the rest and exits 1.
	outcomeIncomplete = "incomplete"
	// outcomeBlocked — a declared dependency is missing (dry run only: an --assert with one
	// is refused by the gate before it reaches a verdict at all).
	outcomeBlocked = "blocked"
	// outcomeNothingToDo — this home already matches what the packs declare.
	outcomeNothingToDo = "nothing_to_do"
	// outcomeApplied — an --assert wrote what it planned.
	outcomeApplied = "applied"
	// outcomeWouldComplete — a dry run that found work and no blocker.
	outcomeWouldComplete = "would_complete"
)

// hostApplyVerdict is the verdict line: one sentence, in every posture, on every path,
// including the degenerate ones. One case per outcome above, in the same order, so a reader
// comparing the two sees the same list twice.
func hostApplyVerdict(s *hostApplySurvey, write bool) string {
	switch hostApplyOutcome(s, write) {
	case outcomeNoPacks:
		// the verdict block's zero-packs row. The retire passes still run here (emptying `packs` is
		// the most complete drop there is), so the number they retired is the whole result: the
		// destinations the briefing and skills retires move, and, in a dry run, the paths and keys
		// the dropped-pack retire would ask about, which it lists above as `would archive`.
		n := 0
		if s != nil {
			n = len(s.Changed)
		}
		paths, keys := s.DroppedRetires()
		n += paths
		// And, in an --assert answered `n`, what that retire kept, which the line above the
		// verdict says is still in the home: "nothing left to retire" contradicted it.
		kept := declinedRetireClause(s)
		if n == 0 && keys == 0 {
			if kept != "" {
				return "No packs configured — nothing to apply; " + kept + "."
			}
			return "No packs configured — nothing to apply, and nothing left to retire."
		}
		var what []string
		if n > 0 {
			what = append(what, fmt.Sprintf("%d destination(s)", n))
		}
		if keys > 0 {
			what = append(what, fmt.Sprintf("%d config %s", keys, plural(keys, "key", "keys")))
		}
		if write {
			if kept != "" {
				return fmt.Sprintf("No packs configured — nothing to apply; %s retired, and %s.",
					joinWords(what, "and"), kept)
			}
			return fmt.Sprintf("No packs configured — nothing to apply; %s retired.",
				joinWords(what, "and"))
		}
		return fmt.Sprintf("No packs configured — nothing to apply; %s would be retired.",
			joinWords(what, "and"))
	case outcomeRefused:
		names := unresolvedNames(s.UnresolvedPacks())
		if len(names) == 0 {
			// stageInputs: the refusal's own line, above, says what in the config and its fix.
			if write {
				return "Refused — your config's providers or profiles are refused (above); nothing " +
					"was written."
			}
			return "An --assert would REFUSE: your config's providers or profiles are refused " +
				"(above), so nothing would be written."
		}
		if write {
			return fmt.Sprintf("Refused — %d configured %s could not be resolved (%s); nothing "+
				"was written.", len(names), plural(len(names), "pack", "packs"),
				strings.Join(names, ", "))
		}
		return fmt.Sprintf("An --assert would REFUSE: %d configured %s could not be resolved "+
			"(%s), and an incomplete pack set is never applied — nothing would be written.",
			len(names), plural(len(names), "pack", "packs"), strings.Join(names, ", "))
	case outcomeIncomplete:
		// The PACKS, and the rest of the run in the same sentence: the failures themselves are
		// stated once, with their fixes, in the group above, and what the reader still needs
		// from the last line is whose config is missing and whether anything else happened. A
		// program the floor could not install is named beside them, its error being in the floor
		// stage's line, and one it will not install as a blocker is (the verdict block), with its
		// step: one sentence, ending the run (P7).
		var what []string
		if failures := s.Failures(); len(failures) > 0 {
			who := "some of " + joinWords(possessives(failurePacks(failures)), "and") + " config"
			if write {
				what = append(what, who+" was not written")
			} else {
				what = append(what, who+" cannot be written")
			}
		}
		if stages := s.StageFailureNames(); len(stages) > 0 {
			what = append(what, stageFailureClause(stages, write))
		}
		if bins := s.FloorFailures(); len(bins) > 0 {
			what = append(what, "yolo's floor could not install "+joinWords(bins, "and"))
		}
		if bins := s.FloorRefusals(); len(bins) > 0 {
			what = append(what, floorRefusalClause(bins))
		}
		blockers := strings.Join(what, ", and ") + " (above)"
		if write {
			rest := "nothing else needed changing"
			if work := hostApplyWork(s, true); work != "nothing" {
				rest = "applied the rest: " + work
			}
			// A retire answered `n` is something that needed changing and was kept.
			if kept := declinedRetireClause(s); kept != "" {
				if rest == "nothing else needed changing" {
					rest = kept
				} else {
					rest += "; " + kept
				}
			}
			return fmt.Sprintf("Incomplete — %s; %s.", blockers, rest)
		}
		return fmt.Sprintf("An --assert would be incomplete — %s.", blockers)
	case outcomeBlocked:
		// the dependency rule: in the DRY RUN a missing declared dependency is a tier-3 blocker that
		// decides the verdict and changes nothing else — exit 0, nothing written, nothing installed.
		// The names, not a count: the reader's next action is about those binaries.
		missing := s.MissingDeps()
		return fmt.Sprintf("An --assert would NOT complete: %d declared %s missing (%s).",
			len(missing), plural(len(missing), "dependency is", "dependencies are"),
			strings.Join(missing, ", "))
	case outcomeNothingToDo:
		if !write {
			return "Nothing to do — this home is up to date."
		}
		// An --assert answered `n` at the retire has nothing left to do, and is not up to date:
		// what it kept is named instead (declinedRetireClause).
		state := "this home is up to date"
		if kept := declinedRetireClause(s); kept != "" {
			state = kept
		}
		if p := installedPrefix(s); p != "" {
			return p + "nothing else to apply — " + state + "."
		}
		return "Nothing to apply — " + state + "."
	case outcomeApplied:
		work := hostApplyWork(s, true)
		if kept := declinedRetireClause(s); kept != "" {
			work += "; " + kept
		}
		if p := installedPrefix(s); p != "" {
			return p + "applied: " + work + "."
		}
		return "Applied: " + work + "."
	default:
		return "An --assert would complete."
	}
}

// declinedRetireClause is the verdict's clause for a dropped-pack retire an --assert was answered
// `n` about (hostApplySurvey.DeclinedRetires): "2 paths from dropped packs are still in your home,
// not retired (above)", the line above it carrying the steps that retire them. Empty when none was
// declined, which a dry run never records.
func declinedRetireClause(s *hostApplySurvey) string {
	paths, keys := s.DeclinedRetires()
	var what []string
	if paths > 0 {
		what = append(what, fmt.Sprintf("%d %s", paths, plural(paths, "path", "paths")))
	}
	if keys > 0 {
		what = append(what, fmt.Sprintf("%d config %s", keys, plural(keys, "key", "keys")))
	}
	if len(what) == 0 {
		return ""
	}
	return fmt.Sprintf("%s from dropped packs %s still in your home, not retired (above)",
		joinWords(what, "and"), plural(paths+keys, "is", "are"))
}

// stageFailureClause is the verdict's clause for the stages that failed: each by the word its own
// lines above the verdict lead with (StageFailureNames), and the fix being those lines'.
func stageFailureClause(stages []string, write bool) string {
	noun, verb := "stage", "would fail"
	if len(stages) > 1 {
		noun = "stages"
	}
	if write {
		verb = "failed"
	}
	return "the " + joinWords(stages, "and") + " " + noun + " " + verb
}

// floorRefusalClause is the verdict's clause for the programs yolo's floor will not install over a
// record a newer yolo wrote, in both postures: the floor stage's own words, "will not install"
// (P8: one term per outcome, and `refused` is the apply's, for a run that wrote nothing), and the
// step that clears it, the same two steps the floor stage's line names (hostfloor.ErrNewerRecord).
func floorRefusalClause(bins []string) string {
	record, that := "a record a newer yolo wrote", "that record"
	if len(bins) > 1 {
		record, that = "records a newer yolo wrote", "those records"
	}
	return fmt.Sprintf("yolo's floor will not install %s over %s until you %s or remove %s",
		joinWords(bins, "and"), record, updatehint.Step(), that)
}

// installedPrefix is the verdict line's "Installed `rg`; applied: …" — the clause an --assert
// leads with when the dependency gate installed something before the render
// (applyhostdepgate.go).
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

// workItem is one class of work this apply did or would do, in the units the verdict block's
// count table specifies — files, skills, destinations; never the number of destinations a loop
// visited.
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
	if n := s.Adoptions(); n > 0 {
		// PAST TENSE IN BOTH POSTURES, and that is not an oversight in the one class that
		// skips `add`'s changed/would-change pair. This count comes from
		// HostRenderResult.Archived, which names a copy that was MADE — assert-only by that
		// field's own ruling, because reporting a path a dry run did not write is the one lie
		// a net cannot afford. The other classes need both spellings because they describe a
		// prediction in one posture and an act in the other; this one never describes a
		// prediction, so a second spelling would be an unreachable branch asserting the
		// opposite of the field's contract.
		//
		// SURFACES, not files, and not "config files" either: the adopted destination is
		// already counted by the line above whenever the render also moved it, and repeating
		// the noun there would read as a second file. The unit is the one the per-surface
		// line and the archive disclosure under it both use.
		phrase := fmt.Sprintf("%d %s adopted (archived first)", n,
			plural(n, "surface", "surfaces"))
		out = append(out, workItem{short: phrase, full: phrase})
	}
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
	// THE DROPPED-PACK RETIRE, in a dry run only (hostApplySurvey.retirePaths): what the lines above
	// list as `would archive` and `would remove key`, in their words. One spelling, the dry run's,
	// since only a dry run records it.
	paths, keys := s.DroppedRetires()
	if paths > 0 {
		phrase := fmt.Sprintf("%d %s from dropped packs would be archived", paths,
			plural(paths, "path", "paths"))
		out = append(out, workItem{short: phrase, full: phrase})
	}
	if keys > 0 {
		phrase := fmt.Sprintf("%d config %s from dropped packs would be removed", keys,
			plural(keys, "key", "keys"))
		out = append(out, workItem{short: phrase, full: phrase})
	}
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

// hostApplyCounts is the verdict block's COUNTS: the evidence for the sentence above, in the
// units the reader cares about (P6). Two lines at most — what this apply moves, then what it
// costs — and a term appears only when its count is non-zero, because a row of zeroes is the
// arithmetic the verdict was supposed to replace.
//
// "In sync" is reported as the survey defines it and NOT as "compared and unchanged": the
// count deliberately includes destinations a render skipped or refused (see
// hostApplySurvey.InSync), so claiming they were compared would be a claim the render did not
// make (P6). Splitting the two is its own change, in the kinds' result structs.
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
	floorRefused, floorFailed := s.FloorBlockedDeps()
	if present, missing, notProbed, unpublished := s.Deps(); present+missing+notProbed+unpublished+
		len(floorRefused)+len(floorFailed) > 0 {
		dep := fmt.Sprintf("%d declared %s present", present,
			plural(present, "dependency", "dependencies"))
		if missing > 0 {
			dep += fmt.Sprintf(", %d missing (%s)", missing, strings.Join(s.MissingDeps(), ", "))
		}
		if notProbed > 0 {
			dep += fmt.Sprintf(", %d not probed", notProbed)
		}
		if unpublished > 0 {
			dep += fmt.Sprintf(", %d with no build for this host (%s)", unpublished,
				strings.Join(s.UnpublishedDeps(), ", "))
		}
		// With the problems, by name, as the verdict names them: never present (Deps).
		if len(floorRefused) > 0 {
			dep += fmt.Sprintf(", %d yolo's floor will not install (%s)", len(floorRefused),
				strings.Join(floorRefused, ", "))
		}
		if len(floorFailed) > 0 {
			dep += fmt.Sprintf(", %d yolo's floor could not install (%s)", len(floorFailed),
				strings.Join(floorFailed, ", "))
		}
		cost = append(cost, dep)
	}
	if n := s.DroppedComments(); n > 0 {
		// the remedy contract's other no-remedy class, and the one the verdict was silent about: a
		// dropped comment already counted as a tier-3 loss (configResultTier) with no term anywhere
		// in this block, so the one class whose loss cannot be undone was the one a reader of the
		// last line could miss entirely.
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
		// one-way door they are on.
		//
		// ⚠ It says SURFACE and not "home" because that is what it measures — one surface
		// with no provenance record sets it, and a home yolo has applied into for months
		// sets it again the day a pack claims a surface it did not before. It read "first
		// apply into this home" until 2026-09-18, which put it in the same line as "77
		// destinations already in sync" and made the verdict argue with itself.
		cost = append(cost, "first apply of a surface into this home")
	}
	if len(cost) > 0 {
		out = append(out, strings.Join(cost, " · "))
	}
	return out
}
