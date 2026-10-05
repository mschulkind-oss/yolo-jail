package cli

// hostapplynotch.go says docs/reference/report-tiers.md's TIER-1 facts ONCE per run: which
// contribution kinds and which of your config keys DO NOT APPLY at the host notch, and which
// autonomy posture this notch renders. Both halves are render.FieldSet's census answers (the
// keys since docs/design/declaration-parity.md OQ-DP5's second half: InertKeys).
//
// A tier-1 fact is true of the NOTCH rather than of the home, so it is the same sentence on
// every machine — and the report printed it per CONTRIBUTION. Measured in this jail on
// 2026-09-10: nineteen refusal paragraphs naming seven kinds (`state` ×6, `hook` ×4,
// `loophole` ×4, `reads-host` ×2, `env`/`provider`/`profile` ×1) and five identical autonomy
// lines, ~40 words each, byte-identical in any home that selects the same packs (§2.1, P1).
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
// WHAT LEFT ALTOGETHER IS THE RATIONALE (P8, the report vocabulary): the ~40-word reasons stay
// in internal/render — they are still what the code decides by — and NO TERMINAL VIEW PRINTS
// THEM, at any verbosity. They live in the manual now, as list entries in config_ref.txt's
// host-notch section under a drift gate that reads both of render's maps (detail on demand).
// This line names the kinds and points there.
//
// THE WORD IS `does not apply`, NEVER `refused` (the report vocabulary). `refused` belongs
// to an apply that STOPPED — a doubly-owned surface, an `agents` selector naming nobody — and
// a kind that has no meaning off-container stopped nothing. The old line offered a remedy
// ("Launch a jail to run it") for a problem the reader does not have, which P2 calls a notch
// fact wearing a warning's word.
//
// AND `does not apply` IS NEVER SAID OF WHAT `yolo host --` DELIVERS. env, blocked-tool, adapter,
// a service's host half and a credential loophole's doorway reach an agent through the process
// `yolo host -- <program>` starts, and this command writes no file for any of them: the report
// vocabulary's AT LAUNCH ONLY (render.HostAtLaunch), a clause of its own on the same line. Until
// 2026-10-04 they were named as not applying at the host while `yolo host -- env` printed the
// pack env. Four of those kinds also have a shape the host delivers nowhere (a pointer at a socket
// only a jail binds, a jail-only service and an adapter whose address only that service answers,
// a loophole whose only client is a container), so the outcome is decided PER CONTRIBUTION
// (hostNotchOutcomeOf) and a kind can land in both clauses.
//
// THE LINE STATES WHAT AN ENABLED DECLARATION GETS AT `yolo host --`, NOT WHAT ONE LAUNCH DOES. It
// asks the launch's own admission check (launchservice.Admit) and doorway composition
// (run.HostDoorwayLoopholes), read as if every selected pack's loophole were switched on and
// without the launch's selection filter, since the apply runs no agent to select for. So a launch
// can still hold back what this line names at launch: a loophole the user switched off, a doorway
// or a service's host half its agent's selection does not ask for. A known case runs the other
// way: with YOLO_CLAUDE_CREDENTIAL_VIEW=1 in the invoking shell (CL-D27, off by default), `yolo
// host -- claude` starts the claude pack's loophole daemon (claude-oauth-broker) when none is
// running and keeps a credential view from it, and this line names that loophole as not applying,
// since the switch is read from the launch's environment.

import (
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// notchFacts is what the selected pack set implies about THIS notch — the tier-1 half of one
// apply's report, collected before the first render so the lines it produces can say "below"
// about the surfaces and mean it.
type notchFacts struct {
	// Inapplicable is every declared kind this notch does nothing with, deduplicated and
	// sorted. Deduplicated because the kind is the unit: which pack declared it is a
	// property of the pack, and the tiers puts that behind --verbose (detail on demand).
	// A kind with an at-launch shape is here only for a contribution of the shape no host verb
	// delivers (hostNotchOutcomeOf).
	Inapplicable []packdecl.Kind
	// AtLaunch is every declared kind `yolo host -- <program>` delivers and this command writes
	// no file for (render.HostAtLaunch), for at least one contribution, deduplicated and sorted.
	// A kind may be here and in Inapplicable both, each for its own contributions.
	AtLaunch []packdecl.Kind
	// splitFrom names, for a kind in BOTH lists, the packs whose contributions landed in each
	// (atLaunch, inapplicable), sorted, which --verbose prints beside the kind: for that kind alone
	// the name does not say which contribution is which. Nil for a kind in one list.
	splitFrom map[packdecl.Kind][2][]string
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
	//
	// A PLACED POSTURE LIST COUNTS AS A FOLD (notch-scoped-config-contributions.md NS-D2): its
	// entries land in a config surface below exactly as a config patch's keys do, so a pack
	// whose guarded posture is lists alone — a personal pack adding a host-only package to a
	// surface another pack owns — must not read as "no selected pack's guarded posture patches
	// a config surface here". PLACED, not declared (NS-D12): a list whose surface has no owner
	// is an orphan reported "no effect" one line above, and lands in nothing below, so the
	// answer is the collector's (OverlaySet.PlacesPostureListFrom) rather than the manifest's.
	// The same holds for a POSTURE OVERLAY, a posture's config patch on another pack's surface
	// (NS-D24, OverlaySet.PlacesPostureConfigFrom); only a patch on the pack's OWN surface
	// counts by declaration (packload.Pack.PosturePatchesOwnSurface), since it always lands.
	AutonomyFolds bool
	// InertPackages is how many `packages:` entries this notch leaves inert, when the default
	// view folds that fact into the tier-1 line rather than printing describe's own line for it
	// (reportHostPackages); 0 otherwise.
	InertPackages int
	// InertConfig names the `host_files` entries this notch leaves inert (OQ-NC8): the ones with
	// a source, by destination (hostUserFiles.inertNames). The key itself is honored here, so
	// the config-key census cannot name them; this is the per-entry half.
	InertConfig []string
	// InertLoopholes names the user scope's enabled INLINE loopholes (run.HostInlineLoopholes):
	// a `loopholes.<name>` entry with a `command` and no manifest, a host daemon whose only client
	// is a jail. The `loopholes` key is honored here (a pack loophole's doorway, its settings), so
	// the config-key census cannot name them; this is the per-entry half, as InertConfig is
	// host_files'.
	InertLoopholes []string
	// InertKeys names every top-level key the user-scope config declares that the config-key
	// census (render's configkeys.go, OQ-DP5's second half) says this notch leaves undone —
	// not applicable here, or not built here yet — sorted. It is the census's answer, never a
	// list kept here: a key added to the schema is classified there or the build fails, and
	// once classified it is named here with no new call (inertConfigKeys).
	InertKeys []string
	// WithheldBriefings is every briefing file a selected pack ships that the host composer
	// leaves out of every destination, because it `describes` a kind no host verb delivers
	// (withheldBriefings, docs/design/boundary-broker.md BB-D69). A fact of the notch and the
	// pack, so it is stated once here, never per destination.
	WithheldBriefings []withheldBriefing
}

// inertConfigKeys is the config-key half of the survey: each top-level key cfg DECLARES whose
// census disposition at fields' target is left undone (render.KeyDisposition.LeftUndone),
// sorted. cfg is the user scope, the only config a host apply reads (NC-D30).
//
// A key present with a value that declares nothing — null, false, an empty string, list or
// object — is not named: the notch failing to do nothing is no gap, and `"kvm": false` or
// `"mounts": []` would otherwise read as something the host left undone.
func inertConfigKeys(cfg *jsonx.OrderedMap, fields render.FieldSet) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	for _, k := range cfg.Keys() {
		v, _ := cfg.Get(k)
		if !declaresSomething(v) {
			continue
		}
		if d, _ := fields.ConfigKey(k); d.LeftUndone() {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// declaresSomething reports whether a config value asks for anything (inertConfigKeys).
func declaresSomething(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case *jsonx.OrderedMap:
		return x != nil && x.Len() > 0
	}
	return true
}

// notchConfigNames is every config name the notch line states, in the order it states them:
// inert `packages:` from the effective config's own reporter, the census's keys, then the
// source-bearing `host_files` entries. A name two of them give (`packages`, which the census
// classifies and reportHostPackages counts) is stated once.
func (f notchFacts) notchConfigNames() []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if f.InertPackages > 0 {
		add("packages")
	}
	for _, k := range f.InertKeys {
		add(k)
	}
	for _, n := range f.InertConfig {
		add(n)
	}
	if len(f.InertLoopholes) > 0 {
		add(plural(len(f.InertLoopholes), "inline loophole", "inline loopholes") +
			" (" + strings.Join(f.InertLoopholes, ", ") + ")")
	}
	return names
}

// inertInlineLoopholes is the inline-loophole half of the survey: each enabled `loopholes.<name>`
// entry of cfg, the user scope, that is an inline loophole over the selected packs (a `command`,
// and no selected pack's loophole of that name), in the config's order. cfg is the user scope
// for NC-D30's reason, as inertConfigKeys's is.
func inertInlineLoopholes(cfg *jsonx.OrderedMap, packs []*packload.Pack) []string {
	return run.HostInlineLoopholes(cfg, packs)
}

// surveyNotchFacts walks every contribution the resolved pack set declares and collects the
// tier-1 facts, touching nothing and printing nothing.
//
// It runs over `loaded` AFTER packload.ResolveDestinations, for the same reason the render
// loop does: a zero-ceremony pack's contributions are whatever the resolution gave it, and a
// census taken before that would report a different set from the one the apply acts on.
//
// overlays is the apply's own packoverlay.Collect over the same packs at the same notch — the
// set the render folds — so a posture list counts only where that render will place it.
// doorways is run.HostDoorwayLoopholes over the same packs: the loopholes whose credential
// doorway `yolo host --` opens, the set the at-launch outcome of a loophole (and of an env
// pointer served by one) is read off. Computing it is loophole discovery, whose own warnings go
// to the process's stderr before this runs (hostdoorwaysets.go's header says why).
func surveyNotchFacts(loaded []*packload.Pack, fields render.FieldSet,
	overlays *packoverlay.OverlaySet, doorways map[string]bool) notchFacts {
	var f notchFacts
	// Per kind, per outcome, the packs whose contributions landed there.
	from := map[hostNotchOutcome]map[packdecl.Kind][]string{
		notchAtLaunch: {}, notchDoesNotApply: {},
	}
	// The posture this notch selects, read off render's ONE notch->preset table rather than
	// spelled `false` here: the survey must fold what the render folds, and a literal is how
	// the two come apart.
	hostAutonomy := render.ProfileFor(render.KindHost).AgentAutonomy
	for _, p := range loaded {
		// Three ways a posture folds into a surface below: a config patch on the pack's OWN
		// surface (always lands, in its managed layer), and a posture list or a posture
		// overlay the collector PLACED on another pack's (NS-D12, NS-D24). A declared cross-pack
		// patch is not enough: an ownerless one is reported "no effect" above and lands nowhere.
		if p.PosturePatchesOwnSurface(hostAutonomy) || overlays.PlacesPostureListFrom(p.Name) ||
			overlays.PlacesPostureConfigFrom(p.Name) {
			f.AutonomyFolds = true
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindAutonomy {
				f.Autonomy = true
			}
			outcome := hostNotchOutcomeOf(loaded, fields, p, c, doorways)
			byKind, ok := from[outcome]
			if !ok {
				continue
			}
			byKind[c.Kind] = append(byKind[c.Kind], p.Name)
		}
	}
	f.AtLaunch = sortedKinds(from[notchAtLaunch])
	f.Inapplicable = sortedKinds(from[notchDoesNotApply])
	f.WithheldBriefings = withheldBriefings(loaded)
	for k, launched := range from[notchAtLaunch] {
		if withheld, both := from[notchDoesNotApply][k]; both {
			if f.splitFrom == nil {
				f.splitFrom = map[packdecl.Kind][2][]string{}
			}
			f.splitFrom[k] = [2][]string{sortedUnique(launched), sortedUnique(withheld)}
		}
	}
	return f
}

// sortedUnique is names sorted, each once.
func sortedUnique(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return slices.Compact(out)
}

// sortedKinds is the keys of byKind, sorted.
func sortedKinds(byKind map[packdecl.Kind][]string) []packdecl.Kind {
	var out []packdecl.Kind
	for k := range byKind {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// hostNotchOutcome is what the host notch does with one contribution, as the apply's notch line
// reports it.
type hostNotchOutcome int

const (
	// notchApplies: this command renders it, or probes it (the dep kinds) — reported below the
	// notch line, never on it.
	notchApplies hostNotchOutcome = iota
	// notchAtLaunch: `yolo host -- <program>` delivers it, and this command writes no file for
	// it (render.HostAtLaunch, the report vocabulary's AT LAUNCH ONLY).
	notchAtLaunch
	// notchDoesNotApply: no host verb does anything with it.
	notchDoesNotApply
)

// hostNotchOutcomeOf decides the outcome at the host notch of c, one contribution of pack p. A
// kind render names as delivered at launch is delivered when deliveredByHostLaunch says this
// contribution's shape is, and does not apply otherwise; any other kind does not apply when
// notchInapplicable says so.
func hostNotchOutcomeOf(loaded []*packload.Pack, fields render.FieldSet, p *packload.Pack,
	c packdecl.Contribution, doorways map[string]bool) hostNotchOutcome {
	if _, ok := render.HostAtLaunch(c.Kind); ok {
		if deliveredByHostLaunch(loaded, p, c, doorways) {
			return notchAtLaunch
		}
		return notchDoesNotApply
	}
	if notchInapplicable(fields, c.Kind) {
		return notchDoesNotApply
	}
	return notchApplies
}

// deliveredByHostLaunch reports whether `yolo host -- <program>` delivers this contribution of an
// at-launch kind once it is enabled and asked for, by the launch's own checks rather than a list
// kept here (the header says where the two can still differ):
//
//   - a service when launchservice.Admit admits its host half (OQ-HS4: declared, in a pack yolo
//     ships, an argv naming `yolo`), the gate a launch asks before it runs one;
//   - a loophole when its module is a doorway the launch opens (run.HostDoorwayLoopholes, which
//     re-runs PlanHostDoorways' filter);
//   - an env contribution unless it is `served_by` a daemon the host serves neither as a doorway
//     nor as a pack service (packload's served-at-this-notch rule, NC-D16): a pointer at anything
//     else is withheld at `yolo host --` and named there, as audio's at a socket only a jail binds;
//   - an adapter of a pack that declares no service (a remote gateway, a proxy you run: yolo runs
//     nothing for its address), or one whose pack's service launchservice.Admit admits. That
//     service answers the adapter's address (packload.Adaptation.Service), and `yolo host --`
//     refuses a pairing through it when the gate admits no host half (planHostService);
//   - every blocked-tool contribution.
func deliveredByHostLaunch(loaded []*packload.Pack, p *packload.Pack, c packdecl.Contribution,
	doorways map[string]bool) bool {
	switch c.Kind {
	case packdecl.KindService:
		return hostAdmitsService(loaded, c.Name)
	case packdecl.KindLoophole:
		return doorways[path.Base(path.Clean(filepath.ToSlash(c.From)))]
	case packdecl.KindEnv:
		if c.ServedBy == "" {
			return true
		}
		return doorways[c.ServedBy] || hostAdmitsService(loaded, c.ServedBy)
	case packdecl.KindAdapter:
		if service := adapterServiceOf(p); service != "" {
			return hostAdmitsService(loaded, service)
		}
		return true
	}
	return true
}

// adapterServiceOf is the service that answers pack p's adapter addresses, "" when p declares
// none. It is read off packload.Adaptations rather than off p's manifest here, so the rule that
// names it is the composition's own; over p alone, so an adapter whose pair a later pack holds is
// still decided by the pack that declared it.
func adapterServiceOf(p *packload.Pack) string {
	if adaptations := packload.Adaptations([]*packload.Pack{p}); len(adaptations) > 0 {
		return adaptations[0].Service
	}
	return ""
}

// hostAdmitsService reports whether the launch's gate admits service's host half.
func hostAdmitsService(loaded []*packload.Pack, service string) bool {
	_, err := launchservice.Admit(loaded, service)
	return err == nil
}

// notchInapplicable is the predicate for "this notch's FieldSet does nothing with this kind",
// over both of render's maps (see the two-maps note at the top of this file): refused, or
// honored with no renderer behind it. It is the whole answer for a kind render does not name as
// delivered at launch; for one it does (service and loophole are refused by the FieldSet and
// delivered in their other shape), hostNotchOutcomeOf decides per contribution and asks this
// for nothing.
//
// The body is render.HostLeavesUndone, the one predicate the host briefing composer's
// `describes` gate also asks (through render.HostDelivers), so the notch line and the withheld
// briefing cannot disagree about which kinds the host leaves undone.
func notchInapplicable(fields render.FieldSet, k packdecl.Kind) bool {
	return render.HostLeavesUndone(fields, k)
}

// notchMayNotApply reports whether some contribution of kind k can land under "does not apply at
// the host": every kind notchInapplicable names (a service or loophole the FieldSet refuses
// included, for its undelivered shape), and an honored kind with a shape `yolo host --` does not
// deliver (render.HostWithheldAtLaunch: an env pointer at a daemon the host does not serve). It
// is the set config_ref.txt's DO NOT APPLY list must cover.
func notchMayNotApply(fields render.FieldSet, k packdecl.Kind) bool {
	if notchInapplicable(fields, k) {
		return true
	}
	_, withheld := render.HostWithheldAtLaunch(k)
	return withheld
}

// printNotchFacts prints the tier-1 half of the report: one line by default and at most four
// under --verbose, whatever the pack set's size.
//
// Both lines point somewhere rather than explaining themselves (P3/P8). The kinds line names
// `yolo config-ref`, which is where the report vocabulary moved the REASONS — as list entries
// under a drift gate of their own (TestEveryHostNotchInapplicableKindHasItsReasonDocumented,
// which reads BOTH of render's maps, so a refused kind and an honored-but-unbuilt one are
// covered alike, and TestEveryHostAtLaunchKindHasItsRowDocumented for the at-launch list); the
// autonomy line names what it did to the surfaces, because "did my jail-bypass keys reach my real
// home?" is the single most consequential question this command answers and the answer is one
// word.
//
// ONE LINE BY DEFAULT (report-tiers.md, tier 1: "one line per run, naming the kinds and the
// posture"). Every tier-1 fact is the same sentence on every run, so the default view names them
// on a single line — the kinds `yolo host --` delivers, the kinds that do not apply (the census,
// P5: appearing once is appearing), inert `packages:`, and the posture — and --verbose prints the
// full lines below. The maintainer's report was that three lines of this, above every apply, made
// the output "very confusing".
func printNotchFacts(pr richtext.Printer, f notchFacts) {
	if !reportVerbose() {
		var parts []string
		if len(f.AtLaunch) > 0 {
			parts = append(parts, "at launch only (`yolo host --`): "+kindNames(f.AtLaunch, nil, 0))
		}
		names := f.notchConfigNames()
		for _, k := range f.Inapplicable {
			names = append(names, string(k))
		}
		if len(names) > 0 {
			parts = append(parts, "does not apply at the host: "+strings.Join(names, ", "))
		}
		if fact := withheldBriefingFact(f.WithheldBriefings); fact != "" {
			parts = append(parts, richtext.Escape(fact))
		}
		if f.Autonomy {
			parts = append(parts, "guarded posture — permission prompts stay on")
		}
		if len(parts) > 0 {
			pr.Printf("  [dim]%s[/dim]", strings.Join(parts, " · "))
		}
		return
	}
	if n := len(f.AtLaunch); n > 0 {
		pr.Printf("  [dim]%d %s at launch only — `yolo host -- <program>` delivers %s to the "+
			"program it starts, and this command writes no file for %s: %s (`yolo config-ref` says "+
			"how)[/dim]", n, plural(n, "kind applies", "kinds apply"), plural(n, "it", "them"),
			plural(n, "it", "them"), kindNames(f.AtLaunch, f.splitFrom, 0))
	}
	if n := len(f.Inapplicable); n > 0 {
		pr.Printf("  [dim]%d %s %s at the host notch: %s (`yolo config-ref` says why)[/dim]",
			n, plural(n, "kind", "kinds"), plural(n, "does not apply", "do not apply"),
			kindNames(f.Inapplicable, f.splitFrom, 1))
	}
	// In --verbose `packages` can be named here AND on its own reporter's line, which prints in
	// full at this verbosity (reportHostPackages): this line states the notch fact, that one the
	// detail (how many entries, and a resolved profile's path when there is one).
	if names := f.notchConfigNames(); len(names) > 0 {
		why := "each takes effect in a jail"
		if len(f.InertConfig) > 0 {
			why += "; a source-bearing entry mirrors a host file that is already yours here"
		}
		if len(f.InertLoopholes) > 0 {
			why += "; an inline loophole's only client is a jail"
		}
		pr.Printf("  [dim]config that does not apply at the host notch: %s (%s)[/dim]",
			strings.Join(names, ", "), why)
	}
	// One line for every withheld briefing file, whatever their number: which file, and why the
	// host leaves it out of the destinations below (BB-D69).
	if fact := withheldBriefingFact(f.WithheldBriefings); fact != "" {
		pr.Printf("  [dim]%s[/dim]", richtext.Escape(fact))
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

// kindNames joins kinds for a notch line. With split non-nil, a kind in both of the line's
// lists is followed by the packs whose contributions landed in this one (split[k][side]: 0 the
// at-launch list, 1 the does-not-apply list), which is --verbose's detail on demand.
func kindNames(ks []packdecl.Kind, split map[packdecl.Kind][2][]string, side int) string {
	names := make([]string, len(ks))
	for i, k := range ks {
		names[i] = string(k)
		if packs, both := split[k]; both {
			names[i] += " (" + strings.Join(packs[side], ", ") + ")"
		}
	}
	return strings.Join(names, ", ")
}
