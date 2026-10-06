package cli

// hostapplyremedy.go is docs/reference/report-tiers.md's REMEDY CONTRACT: every tier-3 loss
// and blocker this run found, grouped by the fix rather than by the emitter, each group stating
// **what** is lost or blocked, **whose** it is, **where**, and **the remedy in a form that can
// be pasted**, once (the remedy contract).
//
// GROUPING IS BY REMEDY KEY — the config key, the local-pack path, the missing binary, or none
// — and never by text similarity. That is the whole mechanism: two facts that share a fix are
// one group with one remedy line, and two that do not are two groups however alike they read.
// The measured report had the inverse property, because the group boundary was a loop
// boundary: three surfaces dropping the same three MCP servers printed three `⚠` lines with
// three copies of one remedy, "three problems with three fixes, when they are one problem with
// one fix" (P1's third row).
//
// THE MCP REMEDY HAD THREE COPIES, not two, and the third is the reason a "unify the two" fix
// would have left the defect standing: the per-surface `⚠` (apply.go), the not-confirmed abort
// message (apply.go), and confirmHostLosses' own trailer. The three had drifted — the
// per-surface copy omitted *"reaching every agent"* — so mcpEntryRemedy is now the one string
// all three read, and it names the FILE the declaration goes in as well as the scope it covers
// (P2: copy-paste form, and the scope the remedy covers). That scope was itself wrong at this
// notch while host apply ran no derive for content: "one `mcp_servers` entry reaches every
// agent" was a jail's behavior, and here it left the entry dropped, so HC-D2
// (docs/design/host-computed-layer.md §7) named a `config-overlay` per surface instead. Since
// OQ-HC1 the host runs the jail's derives over the user's own tables, so the remedy names
// `mcp_servers`, `lsp_servers` and `providers` first again, with the per-surface overlay as the
// one-agent alternative (HC-D20); what keeps it one group is that the declaration goes in one
// file, the user config (mcpEntryRemedyKey).
//
// A GROUP WITH NO REMEDY SAYS SO (P2). A value replaced by the owning pack's MANAGED layer has
// no fix at this notch — that layer outranks every declaration — and a dropped comment has none
// possible. Both state the fact and stop, rather than wearing a `⚠` that implies a fix the reader
// will go looking for. A value replaced by a pack's CONFIG-OVERLAY does have one when the pack is
// the user's: edit the overlay (replacedValueGroups).
//
// EVERY GROUP IS REPRESENTED IN THE VERDICT (the verdict block). Grouping compresses the LINES,
// never the SET: each group carries the term the verdict block must contain for its class,
// which is what hostapplyremedy_test.go asserts against the verdict the survey independently
// produces. That is the half that keeps compression honest — a class that stops reaching the
// verdict is a class the reader can miss by reading only the last line, which is the reading P7
// promises is enough.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// remedyGroup is one tier-3 group: a set of losses or blockers that share a fix.
type remedyGroup struct {
	// Class is WHICH of the remedy contract's classes this group is — the one field that is not in
	// the rendered line, and the one a machine consumer branches on (machine consumers: "losses
	// and blockers with class, names and remedy key"). It is carried rather than recovered from
	// the headline's wording for the reason the tier is: the class is known where the group is
	// built, and prose is not a type.
	Class string
	// Key is the remedy contract's REMEDY KEY — the config key, the local-pack path, the missing
	// binary, or "" for a group whose fix does not exist. It is what the grouping is ON, and it is
	// carried rather than derived so a test can assert that two facts with one fix produced one
	// group.
	Key string
	// Headline is what is lost or blocked and whose it is, in the report vocabulary.
	Headline string
	// Items are the NAMES, per the remedy contract: grouping compresses the lines, never the set —
	// every dropped entry, adopted skill and missing binary appears in the default view, in its
	// group.
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
	// Miss is the miss line of a missing dependency (host-agent-environment.md): the whole
	// PATH yolo searched for it and the `host_path` fix, printed under the headline. "" for every
	// other class, and in a jail.
	Miss string
	// VerdictTerm is the word the verdict block requires for this group. It
	// travels WITH the group so the contract is checkable: the verdict is produced from the
	// survey independently, and a class that stops being counted there fails the assertion
	// rather than quietly disappearing from the one line a reader is promised is enough.
	VerdictTerm string
	// Warn is whether the group leads with a `⚠`. A loss with a remedy warns; a loss with
	// none states a fact (P2 — a `⚠` it cannot cash).
	Warn bool
}

// hostApplyRemedyGroups is every tier-3 group this run found, in report order: BLOCKERS FIRST,
// then losses.
//
// Blockers lead because a blocker is what decides the outcome — the dependency rule's whole
// argument is that a missing dependency makes the rest of the apply pointless — and a reader
// who stops at the first `⚠` should have stopped at that one.
func hostApplyRemedyGroups(s *hostApplySurvey, home string, write bool) []remedyGroup {
	if s == nil {
		return nil
	}
	var out []remedyGroup
	out = append(out, unresolvedPackGroups(s.UnresolvedPacks())...)
	out = append(out, failureGroups(s, home, write)...)
	out = append(out, missingDepGroups(s)...)
	if g, ok := droppedEntryGroup(s, home, write); ok {
		out = append(out, g)
	}
	if g, ok := gatedEntryGroup(s, home, write); ok {
		out = append(out, g)
	}
	if g, ok := adoptedSkillGroup(s, home, write); ok {
		out = append(out, g)
	}
	out = append(out, replacedValueGroups(s, home, write)...)
	if g, ok := droppedCommentGroup(s, write); ok {
		out = append(out, g)
	}
	return out
}

// unresolvedPackGroups is the unresolvable-pack BLOCKER, grouped by remedy: one group for the
// git packs (this apply already tried to fetch them at its entry, so the remedy is to fix what
// the fetch error names and retry, `yolo pack install` being the retry), one for the configured
// packs with problems every launch refuses (the fix is in the pack, which `yolo pack lint`
// re-checks, or its `packs` entry), one for the conventional local pack whatever failed (the fix
// is in its directory: no `packs` list names it), and one for everything else (a local
// path or an address only the config can fix). The per-pack REASON is printed where the pack was
// resolved; the group states the fix once.
//
// "In the pack", never "in the pack's manifest": a problem LoadDir reports can be a FILE, such as
// a briefing/CLAUDE.md, which is fixed by renaming it.
//
// Shared by the dry run's report and the --assert refusal, so the lines a user reads when the
// apply refuses are the lines the dry run showed them.
func unresolvedPackGroups(list []unresolvedPack) []remedyGroup {
	var git, malformed, local, other, skewed []string
	for _, u := range list {
		switch {
		case len(u.Skipped) > 0:
			skewed = append(skewed, u.Name)
		case u.NeedsInstall:
			git = append(git, u.Name)
		case u.Implicit:
			// Whatever failed (a manifest problem, or a pack.json LoadDir could not read), the
			// local pack's fix is in its directory: no `packs` list names it.
			local = append(local, u.Name)
		case len(u.ManifestProblems) > 0:
			malformed = append(malformed, u.Name)
		default:
			other = append(other, u.Name)
		}
	}
	var out []remedyGroup
	if len(git) > 0 {
		out = append(out, remedyGroup{
			Class:    remedyClassUnresolvedPack,
			Key:      "yolo pack install",
			Headline: "configured packs not in the pack store, so nothing can be applied",
			Items:    git,
			Remedy: "yolo pack install   (retries the fetch this apply already attempted — " +
				"why it failed is named above for each pack; the next host launch retries it too)",
			VerdictTerm: git[0],
			Warn:        true,
		})
	}
	if len(malformed) > 0 {
		out = append(out, remedyGroup{
			Class:    remedyClassUnresolvedPack,
			Key:      "yolo pack lint",
			Headline: "configured packs with problems every launch refuses, so nothing can be applied",
			Items:    malformed,
			Remedy: "fix each problem named above in the pack (`yolo pack lint <its dir>` " +
				"re-checks it; every launch refuses it too), or remove it from `packs` in " +
				paths.UserConfigPath(),
			VerdictTerm: malformed[0],
			Warn:        true,
		})
	}
	if len(local) > 0 {
		dir := paths.LocalPackDir()
		out = append(out, remedyGroup{
			Class:    remedyClassUnresolvedPack,
			Key:      dir,
			Headline: "the local pack has problems every launch refuses, so nothing can be applied",
			Items:    local,
			Remedy: "fix each problem named above in " + dir + " (`yolo pack lint " + dir +
				"` re-checks it; every launch refuses it too) — it has no `packs` entry to " +
				"remove, since it is included because that directory exists",
			VerdictTerm: local[0],
			Warn:        true,
		})
	}
	if len(other) > 0 {
		out = append(out, remedyGroup{
			Class:    remedyClassUnresolvedPack,
			Key:      paths.UserConfigPath(),
			Headline: "configured packs that could not be read, so nothing can be applied",
			Items:    other,
			Remedy: "fix what is named above for each pack, or remove it from `packs` in " +
				paths.UserConfigPath(),
			VerdictTerm: other[0],
			Warn:        true,
		})
	}
	if len(skewed) > 0 {
		// docs/design/patched-forks.md PF-D70: the pack resolves and a launch runs it without
		// what this yolo cannot read, but a real home is never rendered around the hole, which
		// would retire what an earlier render of the skipped contribution wrote.
		out = append(out, remedyGroup{
			Class:    remedyClassUnresolvedPack,
			Key:      "update yolo",
			Headline: "packs holding contributions this yolo cannot read, so nothing can be applied",
			Items:    skewed,
			Remedy: "update yolo (`yolo update`), since a newer yolo may read what this one skips " +
				"in them; if a field is misspelled, `yolo pack lint <its dir>` names it. A launch " +
				"runs these packs without what it skips, and says so",
			VerdictTerm: skewed[0],
			Warn:        true,
		})
	}
	return out
}

// missingDepGroups is the DRY RUN's rendering of every missing binary — one group each (the
// remedy contract's key for this class is the binary, "across packs"), plus the one note that
// is a property of the posture rather than of any dependency.
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
		last := &out[len(out)-1]
		if last.Note != "" {
			last.Note += "\n"
		}
		last.Note += "a dry run installs nothing — `--assert` offers to run the " +
			"command above, and a decline stops the run with nothing written."
	}
	return out
}

// printUnpublishedDepMisses prints the miss line (host-launch-environment.md §4.2, HE-D2) of every
// program this run found absent whose vendor publishes no build for this host, in the default view,
// just above the verdict that counts them. Not a tier-3 group: it is neither a loss nor a blocker,
// since nothing could install it. But the lookup that found nothing was the launch PATH's, and
// `yolo host -- <it>` runs whatever copy that PATH holds (OQ-HE11 (a)), so a copy the user put in a
// folder the PATH lacks is fixed by the line's `host_path` entry. A disclosure, so no verbosity
// hides it.
func printUnpublishedDepMisses(pr richtext.Printer, s *hostApplySurvey) {
	for _, bin := range s.UnpublishedDeps() {
		if line := s.MissLine(bin, false); line != "" {
			pr.Printf("  [yellow]–[/yellow] %s", richtext.Escape(line))
		}
	}
}

// depBlockerGroups renders missing dependencies as tier-3 groups — ONE group per binary, which
// is the remedy contract's remedy key for this class.
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
			Miss:        b.Miss,
			Note:        b.Ranked,
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
	surfaces := len(droppedNonGatedSurfaces(s))
	verb := "would be dropped"
	if write {
		verb = "were dropped"
	}
	return remedyGroup{
		Class: remedyClassEntryDropped,
		Key:   mcpEntryRemedyKey(home),
		Headline: fmt.Sprintf("%d of your %s %s from %d %s", len(names),
			plural(len(names), "entry", "entries"), verb, surfaces,
			plural(surfaces, "agent surface", "agent surfaces")),
		Items:       names,
		Remedy:      mcpEntryRemedy(home, s.DroppedTables()),
		VerdictTerm: "entr", // "entry"/"entries" — the verdict says one or the other
		Warn:        true,
	}, true
}

// droppedNonGatedSurfaces are the surfaces that lose an entry the declare-it remedy keeps: the
// ones its tables name, which leave out a gated loss (droppedTablesOf).
func droppedNonGatedSurfaces(s *hostApplySurvey) map[string]bool {
	out := map[string]bool{}
	for t := range s.droppedTables {
		out[t.Surface] = true
	}
	return out
}

// gatedEntryGroup is the class of a declared MCP server whose copy in an agent's file goes
// because that agent's requires_env gate left it out: one group, keyed on `env_sources`, the
// user config key that delivers the variable. The declare-it remedy keeps nothing for it.
func gatedEntryGroup(s *hostApplySurvey, home string, write bool) (remedyGroup, bool) {
	names, surfaces := s.GatedEntryNames()
	if len(names) == 0 {
		return remedyGroup{}, false
	}
	verb := "would be dropped"
	if write {
		verb = "were dropped"
	}
	return remedyGroup{
		Class: remedyClassEntryGated,
		Key:   "env_sources",
		Headline: fmt.Sprintf("%d declared %s %s from %d %s, %s required env unset for that "+
			"agent", len(names), plural(len(names), "entry", "entries"), verb, surfaces,
			plural(surfaces, "agent surface", "agent surfaces"),
			plural(len(names), "its", "their")),
		Items:       names,
		Remedy:      entrypoint.GatedEntryLossRemedy(userConfigPathIn(home)),
		VerdictTerm: "entr",
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

// replacedValueGroups is the replaced-value class, ONE GROUP PER WINNER, because the remedy is
// the winner's:
//
//   - A pack's CONFIG-OVERLAY wrote it. The overlay is a declaration in a pack, so keeping the
//     user's value is an edit to that pack: remove or change the key in its `config-overlay`. When
//     the pack is the user's own — the matt pack, the local pack — that is the whole fix, and the
//     old text ("a config-overlay folds BELOW the managed layer, which still wins — so there is no
//     way to keep your value") was false twice over: the overlay was the winner, and editing it is
//     the way. A SHIPPED pack's overlay is not the user's to edit, so that group says so and stops.
//   - The surface's OWN managed layer wrote it. That layer outranks everything but computed, so no
//     declaration keeps the user's value at this notch, and the group says so with no `⚠` (P2).
//   - A COMPUTED leaf wrote it: the pack's derive, over the user's own inputs (OQ-HC1). The input
//     is in the user config, so the remedy names it there. A leaf no input of theirs moves has no
//     remedy, and says so.
//   - The profile's SELECTION wrote it (selectedWinner): a computed key too, but written only when
//     that profile is first activated in the home, so a pick of the user's own after that stands
//     (HC-D17), which this group alone says rather than leave them to re-pick in vain. A leaf the
//     derive computes from the same profile is re-written on every apply, so the computed group
//     must not say it — it did, until 2026-10-05, for pi-subagents' subagents.defaultModel.
//
// Each item names its file, so the group is the one place the loss is stated (the per-surface
// `⚠ overwrote …` line that repeated it is gone).
func replacedValueGroups(s *hostApplySurvey, home string, write bool) []remedyGroup {
	keys, _ := s.ReplacedValues()
	if keys == 0 {
		return nil
	}
	verbFor := func(n int) string {
		if write {
			return plural(n, "was replaced", "were replaced")
		}
		return "would be replaced"
	}
	byWinner := map[string][]replacedValue{}
	var winners []string
	for _, r := range s.replaced {
		if _, seen := byWinner[r.Winner]; !seen {
			winners = append(winners, r.Winner)
		}
		byWinner[r.Winner] = append(byWinner[r.Winner], r)
	}
	sort.Strings(winners)
	var out []remedyGroup
	for _, w := range winners {
		vals := byWinner[w]
		files := map[string]bool{}
		items := make([]string, 0, len(vals))
		surfaces := map[string]bool{}
		for _, v := range vals {
			files[v.Path] = true
			surfaces[v.Surface] = true
			items = append(items, v.Key+" in "+prettyHomePath(home, v.Path))
		}
		sort.Strings(items)
		g := remedyGroup{Class: remedyClassValueReplaced, Items: items, VerdictTerm: "value"}
		if w == selectedWinner {
			out = append(out, selectedValueGroup(g, len(vals), verbFor(len(vals)), home))
			continue
		}
		if inputs, computed := strings.CutPrefix(w, computedWinner); computed {
			out = append(out, computedValueGroup(g, inputs, len(vals), verbFor(len(vals)), home))
			continue
		}
		if w == "" {
			g.Headline = fmt.Sprintf("%d %s of yours %s by keys the owning pack manages",
				len(vals), plural(len(vals), "value", "values"), verbFor(len(vals)))
			g.NoRemedy = "a pack's managed keys outrank every declaration at this notch"
			out = append(out, g)
			continue
		}
		g.Key = "pack:" + w
		g.Headline = fmt.Sprintf("%d %s of yours %s by %s's config-overlay", len(vals),
			plural(len(vals), "value", "values"), verbFor(len(vals)), w)
		if manifest, ok := editablePackManifest(s, w); ok {
			g.Remedy = fmt.Sprintf("to keep yours, remove %s from the `config-overlay` for %s in "+
				"%s, then apply again", plural(len(vals), "that key", "those keys"),
				joinWords(sortedKeysOf(surfaces), "and"), prettyHomePath(home, manifest))
			g.Warn = true
		} else {
			g.NoRemedy = fmt.Sprintf("%s is a shipped pack, so its overlay is not yours to edit; "+
				"drop it from `packs` to keep your value", w)
		}
		out = append(out, g)
	}
	return out
}

// computedValueGroup is replacedValueGroups' group for the values one set of computed inputs
// replaced: inputs is the label's "profile", "lsp_servers", "profile and lsp_servers", or ""
// for a leaf no input of the user's moves.
func computedValueGroup(g remedyGroup, inputs string, n int, verb, home string) remedyGroup {
	if inputs == "" {
		g.Headline = fmt.Sprintf("%d %s of yours %s by keys the owning pack computes",
			n, plural(n, "value", "values"), verb)
		g.NoRemedy = "the pack's derive computes them from nothing you configure, and a computed " +
			"key outranks your file at this notch"
		return g
	}
	names := strings.Split(strings.ReplaceAll(inputs, " and ", ", "), ", ")
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, "`"+name+"`")
	}
	g.Key = strings.Join(names, ",")
	g.Headline = fmt.Sprintf("%d %s of yours %s by what yolo computes from your %s",
		n, plural(n, "value", "values"), verb, inputs)
	g.Remedy = fmt.Sprintf("to keep yours, change or remove %s in %s, then apply again",
		joinWords(keys, "or"), prettyHomePath(home, userConfigPathIn(home)))
	g.Warn = true
	return g
}

// selectedValueGroup is replacedValueGroups' group for the values the profile's SELECTION
// replaced. The remedy is the computed group's for `profile`; what it adds is HC-D17's note, true
// of a selection key alone: the selection is edge-triggered, so this is the one apply that
// replaces the value, and a pick of the user's own after it stands.
func selectedValueGroup(g remedyGroup, n int, verb, home string) remedyGroup {
	g.Key = "profile"
	g.Headline = fmt.Sprintf("%d %s of yours %s by what your profile selects",
		n, plural(n, "value", "values"), verb)
	g.Remedy = fmt.Sprintf("to keep yours, change or remove `profile` in %s, then apply again",
		prettyHomePath(home, userConfigPathIn(home)))
	g.Warn = true
	g.Note = "a value your `profile` selects is written when that profile is first " +
		"activated in this home; a pick of your own after that (your agent's /model) " +
		"stands on every later apply"
	return g
}

// editablePackManifest is the pack.json of a configured pack the user can edit — any pack this
// run loaded that is not one yolo ships from its own embedded tree.
func editablePackManifest(s *hostApplySurvey, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	embedded := paths.EmbeddedPacksDir()
	for _, p := range s.loaded {
		if p.Name != name || p.Root == "" {
			continue
		}
		if rel, err := filepath.Rel(embedded, p.Root); err == nil && !strings.HasPrefix(rel, "..") {
			return "", false
		}
		return filepath.Join(p.Root, "pack.json"), true
	}
	return "", false
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
// without reading the loss: the launch gate's refusal is the model P2 names — two commands,
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
		if g.Miss != "" {
			pr.Printf("    %s", richtext.Escape(g.Miss))
		}
		switch {
		case g.Remedy != "":
			// VERBATIM (printVerbatim): the remedy and its alternative are printed to be pasted,
			// and through the printer a bracketed style word in a pack's command (`acme[red]`)
			// was read as markup and dropped, and an unclosed `[` ran on into the closing tag.
			printVerbatim(pr, "    [cyan]→ ", g.Remedy, "[/cyan]")
			if g.Alt != "" {
				printVerbatim(pr, "      [dim]", g.Alt, "[/dim]")
			}
		case g.NoRemedy != "":
			pr.Printf("    [dim]no remedy: %s[/dim]", g.NoRemedy)
		}
		if g.Note != "" {
			pr.Printf("    [dim]%s[/dim]", g.Note)
		}
	}
}

// The remedy contract's classes. A BLOCKER stands between this home and a completed apply; a
// LOSS takes something of the user's. They share tier 3 because they want the same rendering,
// and they are told apart here because a consumer acting on the document needs to know which it
// is.
const (
	// remedyClassUnresolvedPack — a configured pack could not be resolved, so an --assert
	// refuses the whole apply. A blocker.
	remedyClassUnresolvedPack = "unresolved_pack"
	// remedyClassRenderFailed — a pack's render errored, so its surfaces are missing from the
	// run. A blocker.
	remedyClassRenderFailed = "render_failed"
	// remedyClassBrokenLink — a destination is a symlink into a directory that does not
	// exist, so it was not written. A blocker.
	remedyClassBrokenLink = "broken_link"
	// remedyClassDependency — a declared dependency is missing on this host. A blocker.
	remedyClassDependency = "missing_dependency"
	// remedyClassEntryDropped — a named table entry of the user's (an MCP server) goes.
	remedyClassEntryDropped = "entry_dropped"
	// remedyClassEntryGated — a declared MCP server's copy goes because an agent's requires_env
	// gate left it out; the fix delivers the variable.
	remedyClassEntryGated = "entry_gated"
	// remedyClassSkillAdopted — a skill of the user's moves into their local pack.
	remedyClassSkillAdopted = "skill_adopted"
	// remedyClassValueReplaced — a managed key or a pack's config-overlay replaces a value of the
	// user's. A remedy only when the overlay's pack is the user's to edit.
	remedyClassValueReplaced = "value_replaced"
	// remedyClassCommentDropped — a comment above a changed key does not come back. No remedy.
	remedyClassCommentDropped = "comment_dropped"
)

// mcpEntryRemedyKey is the file whose declaration keeps a hand-added table entry through a host
// apply: the user config, in the home this apply renders into. It is the GROUP KEY as well as the
// file the remedy names first, which is the point of the remedy contract's "group by remedy key":
// one file to edit is what makes three agents' worth of losses one group.
//
// It was the local pack's manifest from HC-D2 until OQ-HC1 (docs/reference/host-agent-environment.md):
// while host apply ran no derive for content, a `config-overlay` there was the one declaration
// that reached a host table. The host now runs the jail's derives over the user's own
// `mcp_servers`, `lsp_servers` and `providers`, so the user config is that file again (HC-D20).
func mcpEntryRemedyKey(home string) string { return userConfigPathIn(home) }

// mcpEntryRemedy is THE remedy for a dropped named entry, in one place. Three copies of it used
// to sit in apply.go and they had drifted (see the file header); every caller now reads this.
//
// It names the FILE, the DECLARATION and the SCOPE, which is what P2 asks of a remedy. The
// declaration is the user config's own table — `mcp_servers` for an MCP server, `lsp_servers`
// for an LSP server, `providers` for a provider — because since OQ-HC1 host apply runs each
// agent's derive over those tables, so one entry reaches every agent's host file as it reaches a
// jail's (HC-D20). That list is manifest.EntryKindHomes, the one spelling the jail boot's drop
// notice shares. The per-surface `config-overlay` stays named as the alternative for an entry
// meant for one agent's file alone; it is what HC-D2 named while those tables reached no host
// file.
//
// The overlay example and its key list are built from `tables`, the surfaces and table keys that
// lost an entry in THIS run, read off the loss lines, so an LSP server dropped from Copilot's
// `lspServers` is not handed codex's `mcp_servers`. The name says MCP for the class that
// motivated it; the remedy is any table's.
//
// It ends without a full stop: two callers embed it mid-sentence.
func mcpEntryRemedy(home string, tables []droppedTable) string {
	example := droppedTable{Surface: "<agent>/<surface>", Table: "<table>"}
	if len(tables) > 0 {
		example = tables[0]
	}
	var surfaces, keys []string
	seenSurface, seenKey := map[string]bool{}, map[string]bool{}
	for _, t := range tables {
		if !seenSurface[t.Surface] {
			seenSurface[t.Surface] = true
			surfaces = append(surfaces, t.Surface)
		}
		if !seenKey[t.Table] {
			seenKey[t.Table] = true
			keys = append(keys, "`"+t.Table+"`")
		}
	}
	scope := " — one per surface, under the table key its loss line names"
	if len(surfaces) > 0 {
		scope = fmt.Sprintf(" — one for each of %s, under the table key its loss line names (%s)",
			joinWords(surfaces, "and"), joinWords(keys, "or"))
	}
	return fmt.Sprintf("declare it in %s — %s — which reaches every agent's files "+
		"here as it reaches a jail's; or, for one agent's file alone, add a `config-overlay` to "+
		"the `contributes` list in %s, for example "+
		`{"kind": "config-overlay", "surface": %q, "config": {"managed": `+
		`{%q: {"<name>": {…}}}}}`+
		"%s", userConfigPathIn(home), manifest.EntryKindHomes(), localPackManifestPathIn(home),
		example.Surface, example.Table, scope)
}

// joinWords lists items the way a sentence does: "a", "a and b", "a, b and c".
func joinWords(items []string, conj string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + conj + " " + items[len(items)-1]
}

// droppedTable is one table that lost an entry in a run: the surface, and the key its file
// keeps the table under, as the loss line spells it.
type droppedTable struct{ Surface, Table string }

// droppedTablesOf is every table one surface's loss lines name, in the order they name them.
//
// A gated loss (entrypoint.IsGatedEntryLoss) names no table: declaring its entry keeps nothing.
func droppedTablesOf(surface string, losses []string) []droppedTable {
	var out []droppedTable
	seen := map[string]bool{}
	for _, l := range losses {
		if entrypoint.IsGatedEntryLoss(l) {
			continue
		}
		table := entryLossTable(l)
		if table == "" || seen[table] {
			continue
		}
		seen[table] = true
		out = append(out, droppedTable{Surface: surface, Table: table})
	}
	return out
}

// sortDroppedTables orders tables by surface and then key, so a remedy built from them prints
// the same sentence every run.
func sortDroppedTables(tables []droppedTable) {
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Surface != tables[j].Surface {
			return tables[i].Surface < tables[j].Surface
		}
		return tables[i].Table < tables[j].Table
	})
}

// localPackManifestPathIn is the conventional local pack's pack.json inside the home THIS
// APPLY is rendering into, for userConfigPathIn's reason: paths.LocalPackDir reads $HOME, and a
// report about a home it was handed must name a path in that home.
func localPackManifestPathIn(home string) string {
	rel, err := filepath.Rel(paths.Home(), paths.LocalPackDir())
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.Join(home, ".config", "yolo-jail", "local", "pack.json")
	}
	return filepath.Join(home, rel, "pack.json")
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
