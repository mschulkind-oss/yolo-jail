package cli

// hostapplyremedy.go is docs/design/report-tiers.md §4.4's REMEDY CONTRACT: every tier-3 loss
// and blocker this run found, grouped by the fix rather than by the emitter, each group stating
// **what** is lost or blocked, **whose** it is, **where**, and **the remedy in a form that can
// be pasted**, once (§9 step 4).
//
// GROUPING IS BY REMEDY KEY — the config key, the local-pack path, the missing binary, or none
// — and never by text similarity. That is the whole mechanism: two facts that share a fix are
// one group with one remedy line, and two that do not are two groups however alike they read.
// The measured report had the inverse property, because the group boundary was a loop
// boundary: three surfaces dropping the same three MCP servers printed three `⚠` lines with
// three copies of one remedy, "three problems with three fixes, when they are one problem with
// one fix" (§3.3's third row).
//
// THE MCP REMEDY HAD THREE COPIES, not two, and the third is the reason a "unify the two" fix
// would have left the defect standing: the per-surface `⚠` (apply.go), the not-confirmed abort
// message (apply.go), and confirmHostLosses' own trailer. The three had drifted — the
// per-surface copy omitted *"reaching every agent"*, which is the two words that turn three
// fixes into one — so mcpEntryRemedy is now the one string all three read, and it names the
// FILE the declaration goes in as well as the scope it covers (P2: copy-paste form, and the
// scope the remedy covers).
//
// A GROUP WITH NO REMEDY SAYS SO (§3.5, §4.4). A replaced scalar has no fix at this notch — a
// config overlay folds BELOW the owner's managed layer, which still wins — and a dropped
// comment has none possible. Both state the fact and stop, rather than wearing a `⚠` that
// implies a fix the reader will go looking for.
//
// EVERY GROUP IS REPRESENTED IN THE VERDICT (§4.3). Grouping compresses the LINES, never the
// SET: each group carries the term the verdict block must contain for its class, which is what
// hostapplyremedy_test.go asserts against the verdict the survey independently produces. That
// is the half that keeps compression honest — a class that stops reaching the verdict is a
// class the reader can miss by reading only the last line, which is the reading P7 promises is
// enough.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// remedyGroup is one tier-3 group: a set of losses or blockers that share a fix.
type remedyGroup struct {
	// Class is WHICH of §4.4's classes this group is — the one field that is not in the
	// rendered line, and the one a machine consumer branches on (§4.8: "losses and blockers
	// with class, names and remedy key"). It is carried rather than recovered from the
	// headline's wording for the reason the tier is: the class is known where the group is
	// built, and prose is not a type.
	Class string
	// Key is §4.4's REMEDY KEY — the config key, the local-pack path, the missing binary, or
	// "" for a group whose fix does not exist. It is what the grouping is ON, and it is
	// carried rather than derived so a test can assert that two facts with one fix produced
	// one group.
	Key string
	// Headline is what is lost or blocked and whose it is, in §4.6's vocabulary.
	Headline string
	// Items are the NAMES. §4.4: grouping compresses the lines, never the set — every dropped
	// entry, adopted skill and missing binary appears in the default view, in its group.
	Items []string
	// Remedy is the pasteable fix, stated ONCE for every item in the group. Empty when none
	// exists, in which case NoRemedy says why.
	Remedy string
	// Alt is a second route to the same fix (a package manager behind a pack's own
	// installer). Never a second remedy for a second item — that would be a second group.
	Alt string
	// NoRemedy is why there is no fix, for the classes that have none. Mutually exclusive
	// with Remedy.
	NoRemedy string
	// Note is one trailing fact about the class, printed under the last group of its kind.
	Note string
	// VerdictTerm is the word §4.3 requires the verdict block to carry for this group. It
	// travels WITH the group so the contract is checkable: the verdict is produced from the
	// survey independently, and a class that stops being counted there fails the assertion
	// rather than quietly disappearing from the one line a reader is promised is enough.
	VerdictTerm string
	// Warn is whether the group leads with a `⚠`. A loss with a remedy warns; a loss with
	// none states a fact (§3.5 — a `⚠` it cannot cash).
	Warn bool
}

// hostApplyRemedyGroups is every tier-3 group this run found, in report order: BLOCKERS FIRST,
// then losses.
//
// Blockers lead because a blocker is what decides the outcome — §4.9's whole argument is that a
// missing dependency makes the rest of the apply pointless — and a reader who stops at the
// first `⚠` should have stopped at that one.
func hostApplyRemedyGroups(s *hostApplySurvey, home string, write bool) []remedyGroup {
	if s == nil {
		return nil
	}
	var out []remedyGroup
	out = append(out, missingDepGroups(s)...)
	if g, ok := droppedEntryGroup(s, home, write); ok {
		out = append(out, g)
	}
	if g, ok := adoptedSkillGroup(s, home, write); ok {
		out = append(out, g)
	}
	if g, ok := replacedValueGroup(s, write); ok {
		out = append(out, g)
	}
	if g, ok := droppedCommentGroup(s, write); ok {
		out = append(out, g)
	}
	return out
}

// missingDepGroups is the DRY RUN's rendering of every missing binary — one group each (§4.4's
// key for this class is the binary, "across packs"), plus the one note that is a property of the
// posture rather than of any dependency.
//
// The note trails the LAST group because repeating it under every binary is noise and printing
// it under the first wedges it between two groups.
func missingDepGroups(s *hostApplySurvey) []remedyGroup {
	out := depBlockerGroups(hostDepBlockers(s))
	if len(out) > 0 {
		// The note is the DRY RUN's, and it is the only posture that can reach it: an --assert
		// with a missing dependency is refused by the gate before the first render, so a
		// writing run never prints these groups from here (applyhostdepgate.go prints them
		// itself, above its prompt). What the dry-run reader needs is the one fact the lines
		// above cannot carry — that the command they are looking at is one the NEXT posture
		// offers to run, and that saying no there stops the run rather than skipping a step.
		out[len(out)-1].Note = "a dry run installs nothing — `--assert` offers to run the " +
			"command above, and a decline stops the run with nothing written."
	}
	return out
}

// depBlockerGroups renders missing dependencies as tier-3 groups — ONE group per binary, which
// is §4.4's remedy key for this class.
//
// Shared by the dry run's report and the --assert gate's prompt, and that sharing is the point:
// the lines a user reads before answering `y` are the same lines the dry run showed them, so
// "run the dry run first" is advice about the same text rather than about a different rendering
// of it.
func depBlockerGroups(blockers []hostDepBlocker) []remedyGroup {
	var out []remedyGroup
	for _, b := range blockers {
		out = append(out, remedyGroup{
			Class: remedyClassDependency,
			Key:   b.Bin,
			Headline: fmt.Sprintf("`%s` is declared by your packs (%s) and MISSING on this host",
				b.Bin, b.Kind),
			Remedy:      b.Remedy,
			Alt:         b.Alt,
			NoRemedy:    b.NoRemedy,
			VerdictTerm: b.Bin,
			Warn:        true,
		})
	}
	return out
}

// droppedEntryGroup is the MCP class: one group, keyed on the config key that keeps them, for
// every named entry dropped from every agent surface.
func droppedEntryGroup(s *hostApplySurvey, home string, write bool) (remedyGroup, bool) {
	names := s.DroppedEntryNames()
	if len(names) == 0 {
		return remedyGroup{}, false
	}
	_, surfaces := s.DroppedEntries()
	verb := "would be dropped"
	if write {
		verb = "were dropped"
	}
	return remedyGroup{
		Class: remedyClassEntryDropped,
		Key:   mcpEntryRemedyKey,
		Headline: fmt.Sprintf("%d of your %s %s from %d %s", len(names),
			plural(len(names), "entry", "entries"), verb, surfaces,
			plural(surfaces, "agent surface", "agent surfaces")),
		Items:       names,
		Remedy:      mcpEntryRemedy(home),
		VerdictTerm: "entr", // "entry"/"entries" — the verdict says one or the other
		Warn:        true,
	}, true
}

// adoptedSkillGroup is the skills class: one group, keyed on the local pack every adopted skill
// moves into, because that path IS the fix — the move is what keeps them reaching every agent.
// What the remedy line offers is the opt-OUT, which is the only choice the user still has.
func adoptedSkillGroup(s *hostApplySurvey, home string, write bool) (remedyGroup, bool) {
	names := s.SkillNames(skillAdopted)
	if len(names) == 0 {
		return remedyGroup{}, false
	}
	localPack := localPackSkillsPath(home)
	verb := "would move"
	if write {
		verb = "moved"
	}
	g := remedyGroup{
		Class: remedyClassSkillAdopted,
		Key:   localPack,
		Headline: fmt.Sprintf("%d %s in your agent skill dirs %s yours, not yolo's, and %s "+
			"into your local pack", len(names), plural(len(names), "skill", "skills"),
			plural(len(names), "is", "are"), verb),
		Items:       names,
		Remedy:      "to keep one out of yolo's hands, remove it from the agent dir before applying",
		VerdictTerm: "skill",
		Warn:        true,
	}
	if localPack == "" {
		// No local pack could be resolved, so nothing composes them back: the move becomes an
		// archive, and the remedy above would be advice about a directory that is not there.
		g.Remedy = ""
		g.NoRemedy = "no local pack location could be resolved, so each one is ARCHIVED " +
			"instead — nothing is deleted, and nothing composes it back either"
	}
	return g, true
}

// replacedValueGroup is §4.4's no-remedy class, and the one §3.5 singles out: a managed key
// overwriting a value of the user's has no fix at this notch, so the group says which pack owns
// the keys and stops.
//
// No `⚠`. A config overlay folds BELOW the owner's managed layer, which still wins a conflict,
// so the user cannot re-declare their value — the remedy is
// config-ownership-and-promotion.md's to build, and until it ships a warning glyph here points
// at nothing.
func replacedValueGroup(s *hostApplySurvey, write bool) (remedyGroup, bool) {
	keys, files := s.ReplacedValues()
	if keys == 0 {
		return remedyGroup{}, false
	}
	verb := "would be replaced"
	if write {
		verb = "were replaced"
	}
	return remedyGroup{
		Class: remedyClassValueReplaced,
		Key:   "",
		Headline: fmt.Sprintf("%d of your values %s in %d %s", keys, verb, files,
			plural(files, "file", "files")),
		Items: s.ReplacedKeyNames(),
		NoRemedy: "these keys are managed by the packs that declare them, and a " +
			"config-overlay folds BELOW the managed layer, which still wins — so there is no " +
			"way to keep your value at this notch yet",
		VerdictTerm: "value",
	}, true
}

// droppedCommentGroup is the other no-remedy class: a canonical re-emit cannot keep a comment
// above a key whose value it changes. Nothing the user CONFIGURED moves, which is why it is not
// an overwrite — and a comment they wrote does not come back, which is why it is a loss at all.
func droppedCommentGroup(s *hostApplySurvey, write bool) (remedyGroup, bool) {
	n := s.DroppedComments()
	if n == 0 {
		return remedyGroup{}, false
	}
	verb := "would lose"
	if write {
		verb = "lost"
	}
	return remedyGroup{
		Class:    remedyClassCommentDropped,
		Key:      "",
		Headline: fmt.Sprintf("%d %s %s comments of yours", n, plural(n, "surface", "surfaces"), verb),
		Items:    s.CommentSurfaces(),
		NoRemedy: "a comment above a key this render CHANGES would be left lying about a " +
			"value that is gone, so it goes with it",
		VerdictTerm: "comment",
	}, true
}

// printRemedyGroups renders the groups, once per run, above the verdict block.
//
// The remedy is INDENTED UNDER its group and arrowed, so the eye can find the pasteable half
// without reading the loss: the launch gate's refusal is the model §3.5 names — two commands,
// copy-paste, the key and its file — and this is the same shape for a report.
func printRemedyGroups(pr richtext.Printer, groups []remedyGroup) {
	for _, g := range groups {
		head := g.Headline
		if len(g.Items) > 0 {
			head += ": " + strings.Join(g.Items, ", ")
		}
		if g.Warn {
			pr.Printf("  [bold yellow]⚠ %s[/bold yellow]", head)
		} else {
			pr.Printf("  [yellow]%s[/yellow]", head)
		}
		switch {
		case g.Remedy != "":
			pr.Printf("    [cyan]→ %s[/cyan]", g.Remedy)
			if g.Alt != "" {
				pr.Printf("      [dim]%s[/dim]", g.Alt)
			}
		case g.NoRemedy != "":
			pr.Printf("    [dim]no remedy: %s[/dim]", g.NoRemedy)
		}
		if g.Note != "" {
			pr.Printf("    [dim]%s[/dim]", g.Note)
		}
	}
}

// The §4.4 classes. A BLOCKER stands between this home and a completed apply; a LOSS takes
// something of the user's. They share tier 3 because they want the same rendering, and they
// are told apart here because a consumer acting on the document needs to know which it is.
const (
	// remedyClassDependency — a declared dependency is missing on this host. A blocker.
	remedyClassDependency = "missing_dependency"
	// remedyClassEntryDropped — a named table entry of the user's (an MCP server) goes.
	remedyClassEntryDropped = "entry_dropped"
	// remedyClassSkillAdopted — a skill of the user's moves into their local pack.
	remedyClassSkillAdopted = "skill_adopted"
	// remedyClassValueReplaced — a managed key replaces a value of the user's. No remedy.
	remedyClassValueReplaced = "value_replaced"
	// remedyClassCommentDropped — a comment above a changed key does not come back. No remedy.
	remedyClassCommentDropped = "comment_dropped"
)

// mcpEntryRemedyKey is the config key that keeps a hand-added MCP server through a wholesale
// table regeneration. It is the GROUP KEY as well as the text, which is the point of §4.4's
// "group by remedy key": the key is what makes three agents' worth of losses one fix.
const mcpEntryRemedyKey = "mcp_servers"

// mcpEntryRemedy is THE remedy for a dropped named entry, in one place. Three copies of it used
// to sit in apply.go and they had drifted (see the file header); every caller now reads this.
//
// It names the FILE and the SCOPE, which is what P2 asks of a remedy and what the per-surface
// copy did not have: "declare the entry under `mcp_servers`" left the reader to find out where
// that goes and whether they would have to repeat it per agent. The answer to the second is the
// whole reason this is one group — one declaration reaches every agent — and it is the half the
// drifted copy had dropped.
func mcpEntryRemedy(home string) string {
	return fmt.Sprintf("declare them under `%s` in %s — one entry there reaches every agent",
		mcpEntryRemedyKey, userConfigPathIn(home))
}

// userConfigPathIn is the user config file inside the home THIS APPLY is rendering into.
//
// Derived from the rendered home rather than returned by paths.UserConfigPath() directly, for
// the reason localPackSkillsPath states: that helper reads $HOME, so a caller rendering into a
// home it was handed (every test, and a `--at host` run from inside a jail) would print a path
// in a different home than the one the report is about. Falls back to the conventional layout
// when the two cannot be related.
func userConfigPathIn(home string) string {
	rel, err := filepath.Rel(paths.Home(), paths.UserConfigPath())
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	}
	return filepath.Join(home, rel)
}
