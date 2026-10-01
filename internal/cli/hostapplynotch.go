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

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	// InertKeys names every top-level key the user-scope config declares that the config-key
	// census (render's configkeys.go, OQ-DP5's second half) says this notch leaves undone —
	// not applicable here, or not built here yet — sorted. It is the census's answer, never a
	// list kept here: a key added to the schema is classified there or the build fails, and
	// once classified it is named here with no new call (inertConfigKeys).
	InertKeys []string
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
	return names
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
func surveyNotchFacts(loaded []*packload.Pack, fields render.FieldSet,
	overlays *packoverlay.OverlaySet) notchFacts {
	var f notchFacts
	seen := map[packdecl.Kind]bool{}
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
// `yolo config-ref`, which is where the report vocabulary moved the REASONS — as list entries
// under a drift gate of their own (TestEveryHostNotchInapplicableKindHasItsReasonDocumented,
// which reads BOTH of render's maps, so a refused kind and an honored-but-unbuilt one are
// covered alike); the autonomy line names what it did to the surfaces, because "did my
// jail-bypass keys reach my real home?" is the single most consequential question this command
// answers and the answer is one word.
//
// ONE LINE BY DEFAULT (report-tiers.md, tier 1: "one line per run, naming the kinds and the
// posture"). Every tier-1 fact is the same sentence on every run, so the default view names them
// on a single line — the kinds that do not apply (the census, P5: appearing once is appearing),
// inert `packages:`, and the posture — and --verbose prints the two full lines below. The
// maintainer's report was that three lines of this, above every apply, made the output "very
// confusing".
func printNotchFacts(pr richtext.Printer, f notchFacts) {
	if !reportVerbose() {
		var parts []string
		names := f.notchConfigNames()
		for _, k := range f.Inapplicable {
			names = append(names, string(k))
		}
		if len(names) > 0 {
			parts = append(parts, "does not apply at the host: "+strings.Join(names, ", "))
		}
		if f.Autonomy {
			parts = append(parts, "guarded posture — permission prompts stay on")
		}
		if len(parts) > 0 {
			pr.Printf("  [dim]%s[/dim]", strings.Join(parts, " · "))
		}
		return
	}
	if len(f.Inapplicable) > 0 {
		names := make([]string, len(f.Inapplicable))
		for i, k := range f.Inapplicable {
			names[i] = string(k)
		}
		pr.Printf("  [dim]%d %s %s at the host notch: %s (`yolo config-ref` says why)[/dim]",
			len(names), plural(len(names), "kind", "kinds"),
			plural(len(names), "does not apply", "do not apply"), strings.Join(names, ", "))
	}
	// In --verbose `packages` can be named here AND on its own reporter's line, which prints in
	// full at this verbosity (reportHostPackages): this line states the notch fact, that one the
	// detail (how many entries, and a resolved profile's path when there is one).
	if names := f.notchConfigNames(); len(names) > 0 {
		why := "each takes effect in a jail"
		if len(f.InertConfig) > 0 {
			why += "; a source-bearing entry mirrors a host file that is already yours here"
		}
		pr.Printf("  [dim]config that does not apply at the host notch: %s (%s)[/dim]",
			strings.Join(names, ", "), why)
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
