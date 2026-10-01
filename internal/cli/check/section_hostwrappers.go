package check

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// sectionHostWrappers observes whether the generated host launch wrappers are actually what a
// bare invocation runs (docs/reference/host-agent-environment.md §5.5, OQ-4): the directory
// exists, holds a wrapper for every program the selected packs install (completeness), is on
// PATH, and WINS there for each wrapper's name (precedence) — and, riding on those, whether
// host_apply_on_launch's re-check is reachable at all.
//
// # Why this observation lives HERE and not in apply
//
// `apply` can only see the PATH of the shell that happened to invoke it, which is a fact
// about that shell rather than about the user's rc file — and it is wrong in BOTH
// directions. It nags when the line is in the rc but yolo was run from a shell started
// before the edit; and, worse, it stays silent when someone typed `export PATH=…` once by
// hand, so every NEW shell has no wrappers and nothing ever says so. That is the
// silent-skip class, arrived at by a check meant to prevent it.
//
// `check` is the command whose whole job is "what is the state of my environment", and it
// is typically run from a fresh shell — so here the same observation is both decidable
// and actionable. apply reports its own ACTIONS; check reports STATE.
//
// A [WARN], not a [FAIL]: the config surfaces still apply and `yolo host -- <agent>` still
// works, so this is configuration that is not in effect rather than a broken jail. It is
// the summary-COUNTED channel deliberately — an inert configuration nobody is told about
// is exactly what this row exists to prevent.
//
// # One cause, one row
//
// Each [WARN] here is one CAUSE: its headline says what is wrong, its note leads with the fix,
// and then says what the cause breaks — including host_apply_on_launch, when that cause is why
// the launch sync cannot fire (wrapperState.gateClause). No row points at another row for its
// fix. This section used to print the sync's failure as a second [WARN] beside the row naming
// its cause, "the rows below say what to fix", so one missing PATH entry was two warnings; the
// maintainer's ruling that ended it is HE-D2 (docs/reference/host-agent-environment.md).
//
// Silent unless there is something to say: not opted in means no row at all, which is
// what keeps the whole feature from being a nag for the users who never asked for it.
func (o *Options) sectionHostWrappers(r *reporter) {
	if o.inJail() {
		// The key is host-only and a jail has neither a user shell nor the host's PATH.
		// The inherit census already strips it from the config an in-jail check can see;
		// this is the second, local guard.
		return
	}
	if !config.HostWrappersEnabled() {
		return
	}

	st := o.observeWrappers(paths.WrapDir())
	dir, names := st.dir, st.names

	r.sectionHeader("Host launch wrappers")
	// true when the host_management row took over the generation rows below: under "none" the
	// apply that would generate a wrapper refuses, so "run `yolo host apply --assert`" is a
	// remedy that cannot work, and the missing wrappers are that row's cause, not their own.
	generationAbsorbed := hostManagementRow(r, st)
	hostApplyOnLaunchRow(r, st)

	missingDir := st.dirErr != nil && os.IsNotExist(st.dirErr)
	if st.dirErr != nil && !missingDir {
		r.warn("cannot read the wrapper directory "+dir,
			joinLines(st.dirErr.Error(), "Make it readable by you (`ls -ld "+shquote.Quote(dir)+"` shows its owner), "+
				recheck, st.gateClause(gateUnreadable)))
		return
	}
	if missingDir || len(names) == 0 {
		if generationAbsorbed {
			return
		}
		if st.binsKnown && len(st.bins) == 0 {
			// Known-empty, so say so rather than naming an apply that would generate nothing.
			r.warn("host_wrappers is on but no selected pack installs a program — there "+
				"is nothing to wrap",
				joinLines("Select a pack that installs an agent (`yolo pack --help`), or turn "+
					"host_wrappers off.", st.gateClause(gateNoWrapper)))
			return
		}
		if missingDir {
			r.warn("host_wrappers is on but no wrapper directory exists yet",
				joinLines("Run `yolo host apply --assert` to generate the wrappers for the "+
					"programs your selected packs install"+st.programsClause()+".",
					st.gateClause(gateNoWrapper)))
			return
		}
		detail := "Either no selected pack installs a program, or `yolo host apply --assert` " +
			"has not run since you enabled the key."
		if st.binsKnown {
			detail = "Run `yolo host apply --assert`: your selected packs install " +
				joinNames(st.bins) + ", and it has not generated their wrappers."
		}
		r.warn("host_wrappers is on but no wrappers are generated",
			joinLines(detail, st.gateClause(gateNoWrapper)))
		return
	}

	// COMPLETENESS: a program a selected pack installs with no wrapper. The directory
	// existing and being on PATH says nothing about THIS program, and a program with no
	// wrapper never passes the launch gate — so it is exactly the one launch that never
	// self-syncs, and the only thing that would regenerate its wrapper is a launch of some
	// OTHER, wrapped program. The plan is hostwrap.PlanFor over the same Bins an apply
	// uses, so this row and the apply's "would add" line cannot disagree.
	if len(st.missing) > 0 && !generationAbsorbed {
		remedy := "run `yolo host apply --assert`"
		if st.launchOn {
			remedy += " (or `yolo host -- " + st.missing[0] + "` once)"
		}
		r.warn(fmt.Sprintf("%d program(s) have no wrapper: %s — %s",
			len(st.missing), joinNames(st.missing), remedy),
			"Your selected packs install them, but "+dir+" has no wrapper for them, so a "+
				"bare `"+st.missing[0]+"` runs unwrapped: no composed environment, and it "+
				"never passes the launch gate that would bring this host up to date.")
	}

	if !st.onPath {
		// ONE row for the one cause. The fix leads, as the literal line; what the cause breaks
		// follows, once each: every bare command, and the launch sync when it is on. The
		// directory is spelled once, inside the line — the absolute-path escape hatch needs
		// no second spelling of it.
		r.warn("wrapper directory is not on PATH, so no wrapper runs",
			joinLines(hostwrap.PathLine(dir),
				"Add that line to your shell rc, below any line that puts ~/.local/bin on "+
					"PATH, then open a new shell.",
				"Until then a bare "+orNames(names)+" runs unwrapped, with no composed "+
					"environment, though each wrapper works by absolute path.",
				st.gateClause(gateOffPath)))
		return
	}

	// PRECEDENCE: on PATH is not winning. A wrap dir APPENDED to PATH is on it, and every
	// wrapper whose program is also installed earlier (claude's own installer writes
	// ~/.local/bin/claude) never runs — the "Prepend, not append" rule
	// (docs/reference/host-agent-environment.md), observed rather than restated.
	if len(st.shadowed) > 0 {
		var lines []string
		for _, sh := range st.shadowed {
			if sh.Winner == "" {
				lines = append(lines, "  "+sh.Bin+": nothing on PATH runs it — is "+
					filepath.Join(dir, sh.Bin)+" executable?")
				continue
			}
			lines = append(lines, "  "+sh.Bin+" runs "+sh.Winner+", ahead of the wrapper")
		}
		r.warn(fmt.Sprintf("%d wrapper(s) are shadowed by an earlier PATH entry: %s",
			len(st.shadowed), joinNames(shadowedNames(st.shadowed))),
			joinLines(hostwrap.PathLine(dir),
				"Put that line in your shell rc below the lines that add the directories "+
					"named here, then `hash -r` or open a new shell.",
				"The wrapper directory is on PATH but behind them, so a bare invocation runs "+
					"the binary unwrapped:\n"+strings.Join(lines, "\n"),
				st.gateClause(gateAllShadowed)))
		return
	}
	r.ok("wrapper directory is on PATH and wins for every wrapper (" + joinNames(names) + ")")
}

// wrapperState is everything this section observes, computed ONCE before any row prints —
// because the host_apply_on_launch row, which prints first, has to know whether any wrapper
// can reach the gate at all, and that is decided by the rows that follow it.
type wrapperState struct {
	dir    string
	names  []string // generated wrappers on disk, sorted
	dirErr error

	// bins is what a host apply would wrap (hostwrap.Bins over the selected packs), and
	// missing the subset with no wrapper on disk (hostwrap.PlanFor's Added). Both are
	// meaningful only when binsKnown — a section driven without sectionPacks having run
	// knows neither, and says nothing rather than guessing.
	bins      []string
	binsKnown bool
	missing   []string

	onPath   bool
	wins     []string // wrappers a bare invocation reaches
	shadowed []hostwrap.Shadow

	// launchOn is host_apply_on_launch, read once so every row agrees on it.
	launchOn bool
	// managementNone is host_management "none", under which the launch gate returns before it
	// looks at anything (hostapplygate.go), so host_apply_on_launch can never fire.
	managementNone bool
}

func (o *Options) observeWrappers(dir string) wrapperState {
	st := wrapperState{dir: dir, launchOn: config.HostApplyOnLaunchEnabled(),
		managementNone: config.HostManagementMode() == config.HostManagementNone}
	st.names, st.dirErr = wrapperNames(dir)
	if o.selectedPacksKnown {
		st.binsKnown = true
		st.bins = hostwrap.Bins(o.selectedPacks)
		if plan, err := hostwrap.PlanFor(dir, st.bins); err == nil {
			st.missing = plan.Added
		}
	}
	pathEnv := o.Getenv("PATH")
	st.onPath = hostwrap.OnPath(pathEnv, dir)
	if st.onPath && len(st.names) > 0 {
		st.wins, st.shadowed = hostwrap.Precedence(pathEnv, dir, st.names)
	}
	return st
}

// programsClause renders " (claude, pi)" for the programs a host apply would wrap, or "" when
// the pack set is unknown or installs nothing.
func (st wrapperState) programsClause() string {
	if !st.binsKnown || len(st.bins) == 0 {
		return ""
	}
	return " (" + joinNames(st.bins) + ")"
}

// The five reasons no launch can reach the host-apply gate, or reaches it to no effect. Each is
// the CAUSE of exactly one row in this section, which is the row that says the sync cannot fire
// (gateClause).
const (
	// gateNone comes first: under host_management "none" the gate is a no-op, so no PATH or
	// wrapper fix makes the key fire, and the host_management row is the one that says so.
	gateNone        = `host_management is "none"`
	gateNoWrapper   = "no wrapper exists"
	gateUnreadable  = "the wrapper directory cannot be read"
	gateOffPath     = "the wrapper directory is not on PATH"
	gateAllShadowed = "every wrapper is shadowed by an earlier PATH entry"
)

// gateUnreachable says why no launch can reach the host-apply gate, or "" when at least one
// wrapper wins on PATH. The gate runs inside `yolo host -- <bin>`, and only a generated
// wrapper execs that, so "no wrapper wins" and "the gate never fires" are one fact. Under
// host_management "none" the gate does nothing even when reached, and that is the reason
// whatever the wrappers' state, because fixing them would not make it fire.
func (st wrapperState) gateUnreachable() string {
	switch {
	case st.managementNone:
		return gateNone
	case st.dirErr != nil && os.IsNotExist(st.dirErr), st.dirErr == nil && len(st.names) == 0:
		return gateNoWrapper
	case st.dirErr != nil:
		return gateUnreadable
	case !st.onPath:
		return gateOffPath
	case len(st.wins) == 0:
		return gateAllShadowed
	}
	return ""
}

// gateClause is the sentence a cause row adds when THAT cause is why host_apply_on_launch cannot
// fire: "" unless the key is on and gateUnreachable is reason. Asking for the row's own reason,
// rather than "is the gate unreachable at all", is what keeps the sentence on the one row whose
// fix also fixes the sync — a row about a different cause never carries it.
//
// It says the key IS ON in as many words, because the row it replaced was the only place a
// reader learned that: an enabled key makes a wrapped launch stop and ask, and someone debugging
// that has to be able to find it here even while no launch can reach it.
func (st wrapperState) gateClause(reason string) string {
	if !st.launchOn || st.gateUnreachable() != reason {
		return ""
	}
	if reason == gateNone {
		return `host_apply_on_launch is on but does nothing under "none": there is no render ` +
			"for a launch to re-check."
	}
	return "host_apply_on_launch is on but cannot fire, so no launch re-checks this host's render."
}

func shadowedNames(sh []hostwrap.Shadow) []string {
	out := make([]string, 0, len(sh))
	for _, s := range sh {
		out = append(out, s.Bin)
	}
	return out
}

// hostManagementRow says who owns the config files yolo renders into this home — the
// declared ownership contract (docs/design/config-ownership-and-promotion.md §4) — and reports
// whether it took over the section's generation rows.
//
// IT RIDES THE WRAPPERS SECTION, beside host_apply_on_launch, and the placement earns itself
// rather than merely being convenient: `none` REFUSES `yolo host apply`, and that same apply
// is what generates the wrappers. So a home with host_wrappers on and host_management "none"
// has a wrapper directory nothing will ever regenerate — a combination that is invisible
// anywhere else and is exactly this section's WARN criterion (configuration that is not in
// effect, rather than a broken jail).
//
// UNDER "none" IT IS ALSO THE CAUSE OF ANY WRAPPER THAT IS MISSING, and so it absorbs those
// rows and returns true: "run `yolo host apply --assert`" (and "`yolo host -- <bin>` once",
// whose launch gate is a no-op under `none`) would be a remedy that refuses, printed beside the
// row saying so. One cause, one row — the section doc comment says why.
//
// The coverage boundary it inherits is real and worth stating: with host_wrappers OFF the
// whole section is silent, so this row is not the place a user learns the key exists. That is
// what `yolo config-ref` is for; this is where the key's INTERACTION with the wrappers on
// their PATH is observable.
//
// [OK] for "assert", including when nobody wrote the key: an unset key IS "assert" by ruling
// (OQ-CO2), and saying so is how a reader learns that the silent default is a decision rather
// than an absence.
func hostManagementRow(r *reporter, st wrapperState) (absorbedGeneration bool) {
	switch config.HostManagementMode() {
	case config.HostManagementNone:
		why := "The key says your agents' config files are entirely yours, and yolo honors it " +
			"by writing nothing at all — the wrapper directory included, which the same " +
			"command generates."
		readable := st.dirErr == nil || os.IsNotExist(st.dirErr)
		if readable && (len(st.names) == 0 || len(st.missing) > 0) {
			unwrapped := st.missing
			if len(st.names) == 0 {
				unwrapped = nil
				if st.binsKnown {
					unwrapped = st.bins
				}
			}
			forClause := ""
			if len(unwrapped) > 0 {
				forClause = " for " + joinNames(unwrapped)
			}
			r.warn("host_management is \"none\", so `yolo host apply` refuses and no wrapper "+
				"is generated"+forClause,
				joinLines("Set host_management to \"assert\" in "+paths.UserConfigPath()+
					" and run `yolo host apply --assert`, or turn host_wrappers off.",
					why, st.gateClause(gateNone)))
			return true
		}
		r.warn("host_management is \"none\" — `yolo host apply` refuses, so these wrappers "+
			"are never regenerated",
			joinLines("Set host_management to \"assert\" in "+paths.UserConfigPath()+" to have "+
				"yolo own the keys your packs declare, or turn host_wrappers off.", why,
				st.gateClause(gateNone)))
	case config.HostManagementOwn:
		r.ok("host_management is \"own\" — yolo composes these files whole and captures your " +
			"edits, so they are derived output: delete one and the next apply reproduces it")
	default:
		r.ok("host_management is \"assert\" — yolo owns the keys your packs declare and " +
			"rewrites only those; every other key in those files is yours and is left alone")
	}
	return false
}

// hostApplyOnLaunchRow says whether a wrapped launch re-checks its own render before exec'ing
// (docs/reference/host-apply-staleness.md §4.2, which asks for a line "when the feature is
// available and off, naming where to learn to turn it on").
//
// AVAILABLE-AND-OFF IS THE CASE THAT NEEDS SAYING, and the reason is the whole point of the
// feature: `yolo host apply` writes into the real home once and, with this key off, nothing
// ever looks again — so the state the row describes is silent by construction. A user who has
// wrappers on their PATH has already asked for their launches to go through yolo; telling them
// that those launches do not notice a stale render is the one fact they cannot observe.
//
// It rides the wrappers SECTION rather than one of its own, because it shares that section's
// coverage boundary exactly (§7.1): the mechanism is only reached through a generated wrapper,
// so a jail, a direct binary invocation and an IDE launch are all outside it — and this
// section is already where that boundary is reported.
//
// [OK] when on, not silence: an enabled key means a launch can stop and ask, which is a
// behaviour change to `claude` that a reader debugging a paused terminal has to be able to
// find. Off is never a WARN — it is opted out of.
//
// ⚠ "ON" IS NOT "WORKING", and the row used to say it was. The re-check runs only inside
// `yolo host -- <bin>`, which only a wrapper execs, so with no wrapper generated, the directory
// off PATH, or every wrapper shadowed by a real binary ahead of it, no launch ever reaches the
// gate — and a PASS promising automatic synchronization was reassurance about a mechanism that
// cannot run. Under host_management "none" the gate is a no-op even when reached, which is the
// same reassurance about the same nothing. In either case THIS row prints nothing: the row naming the cause says the key is on
// and cannot fire (wrapperState.gateClause), beside the fix that makes it fire. It used to be a
// [WARN] of its own pointing at "the rows below", which counted one cause twice (HE-D2).
func hostApplyOnLaunchRow(r *reporter, st wrapperState) {
	if st.launchOn {
		if st.gateUnreachable() != "" {
			return
		}
		r.ok("host_apply_on_launch is on — a wrapped launch re-checks the render first, and " +
			"synchronizes host configuration automatically (reached through " +
			joinNames(st.wins) + ")")
		return
	}
	r.ok("host_apply_on_launch is off — a wrapped launch execs whatever the last " +
		"`yolo host apply --assert` left, however stale.\n" +
		"  Turn it on in " + paths.UserConfigPath() + " to have a launch notice; see " +
		"`yolo config-ref` for what it does and does not grant.")
}

// wrapperNames lists the generated wrappers in dir, sorted.
func wrapperNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// orNames renders "claude", "claude or pi", "agy, claude or pi": the programs a bare
// invocation of ANY one of them is about, in a sentence about one invocation.
func orNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// joinLines joins a note's lines, dropping the empty ones — a row's optional sentence
// (gateClause) is "" when it does not apply, and must not leave a blank line in the note.
func joinLines(lines ...string) string {
	var kept []string
	for _, l := range lines {
		if l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}
