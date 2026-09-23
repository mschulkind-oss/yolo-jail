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
	hostManagementRow(r)
	hostApplyOnLaunchRow(r, st)

	missingDir := st.dirErr != nil && os.IsNotExist(st.dirErr)
	if st.dirErr != nil && !missingDir {
		r.warn("cannot read the wrapper directory "+dir, st.dirErr.Error())
		return
	}
	if missingDir || len(names) == 0 {
		if st.binsKnown && len(st.bins) == 0 {
			// Known-empty, so say so rather than naming an apply that would generate nothing.
			r.warn("host_wrappers is on but no selected pack installs a program — there "+
				"is nothing to wrap",
				"Select a pack that installs an agent (`yolo pack --help`), or turn host_wrappers off.")
			return
		}
		if missingDir {
			r.warn("host_wrappers is on but no wrapper directory exists yet",
				"Run `yolo host apply --assert` to generate the wrappers for the programs "+
					"your selected packs install"+st.programsClause()+".")
			return
		}
		detail := "Either no selected pack installs a program, or `yolo host apply --assert` " +
			"has not run since you enabled the key."
		if st.binsKnown {
			detail = "Your selected packs install " + joinNames(st.bins) + ", and " +
				"`yolo host apply --assert` has not generated their wrappers."
		}
		r.warn("host_wrappers is on but no wrappers are generated", detail)
		return
	}

	// COMPLETENESS: a program a selected pack installs with no wrapper. The directory
	// existing and being on PATH says nothing about THIS program, and a program with no
	// wrapper never passes the launch gate — so it is exactly the one launch that never
	// self-syncs, and the only thing that would regenerate its wrapper is a launch of some
	// OTHER, wrapped program. The plan is hostwrap.PlanFor over the same Bins an apply
	// uses, so this row and the apply's "would add" line cannot disagree.
	if len(st.missing) > 0 {
		remedy := "run `yolo host apply --assert`"
		if config.HostApplyOnLaunchEnabled() {
			remedy += " (or `yolo host -- " + st.missing[0] + "` once)"
		}
		r.warn(fmt.Sprintf("%d program(s) have no wrapper: %s — %s",
			len(st.missing), joinNames(st.missing), remedy),
			"Your selected packs install them, but "+dir+" has no wrapper for them, so a "+
				"bare `"+st.missing[0]+"` runs unwrapped: no composed environment, and it "+
				"never passes the launch gate that would bring this host up to date.")
	}

	if !st.onPath {
		r.warn("wrapper directory is NOT on PATH — the wrappers do nothing in this shell",
			"Generated "+joinNames(names)+" in "+dir+", but nothing on PATH reaches them, "+
				"so a bare `"+names[0]+"` runs unwrapped with no composed environment.\n"+
				"Add this line to your shell rc (it must PREPEND, ahead of ~/.local/bin):\n"+
				"  "+hostwrap.PathLine(dir)+"\n"+
				"`yolo host apply --shell-init` will append it for you. Either way "+
				dir+"/"+names[0]+" works right now as an absolute path.")
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
			"The wrapper directory is on PATH but behind them, so a bare invocation runs "+
				"the binary unwrapped:\n"+strings.Join(lines, "\n")+"\n"+
				"Prepend the directory instead, in your shell rc (then `hash -r` or open a "+
				"new shell):\n"+
				"  "+hostwrap.PathLine(dir))
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
}

func (o *Options) observeWrappers(dir string) wrapperState {
	st := wrapperState{dir: dir}
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

// gateUnreachable says why no launch can reach the host-apply gate, or "" when at least one
// wrapper wins on PATH. The gate runs inside `yolo host -- <bin>`, and only a generated
// wrapper execs that, so "no wrapper wins" and "the gate never fires" are one fact.
func (st wrapperState) gateUnreachable() string {
	switch {
	case st.dirErr != nil && os.IsNotExist(st.dirErr), st.dirErr == nil && len(st.names) == 0:
		return "no wrapper exists"
	case st.dirErr != nil:
		return "the wrapper directory cannot be read"
	case !st.onPath:
		return "the wrapper directory is not on PATH"
	case len(st.wins) == 0:
		return "every wrapper is shadowed by an earlier PATH entry"
	}
	return ""
}

func shadowedNames(sh []hostwrap.Shadow) []string {
	out := make([]string, 0, len(sh))
	for _, s := range sh {
		out = append(out, s.Bin)
	}
	return out
}

// hostManagementRow says who owns the config files yolo renders into this home — the
// declared ownership contract (docs/design/config-ownership-and-promotion.md §4).
//
// IT RIDES THE WRAPPERS SECTION, beside host_apply_on_launch, and the placement earns itself
// rather than merely being convenient: `none` REFUSES `yolo host apply`, and that same apply
// is what generates the wrappers. So a home with host_wrappers on and host_management "none"
// has a wrapper directory nothing will ever regenerate — a combination that is invisible
// anywhere else and is exactly this section's WARN criterion (configuration that is not in
// effect, rather than a broken jail).
//
// The coverage boundary it inherits is real and worth stating: with host_wrappers OFF the
// whole section is silent, so this row is not the place a user learns the key exists. That is
// what `yolo config-ref` is for; this is where the key's INTERACTION with the wrappers on
// their PATH is observable.
//
// [OK] for "assert", including when nobody wrote the key: an unset key IS "assert" by ruling
// (OQ-CO2), and saying so is how a reader learns that the silent default is a decision rather
// than an absence.
func hostManagementRow(r *reporter) {
	switch config.HostManagementMode() {
	case config.HostManagementNone:
		r.warn("host_management is \"none\" — `yolo host apply` refuses, so these wrappers "+
			"are never regenerated",
			"The key says your agents' config files are entirely yours, and yolo honors it "+
				"by writing nothing at all — including the wrapper directory this section is "+
				"about, which the same command generates.\n"+
				"Set host_management to \"assert\" in "+paths.UserConfigPath()+" to have "+
				"yolo own the keys your packs declare, or turn host_wrappers off.")
	case config.HostManagementOwn:
		r.ok("host_management is \"own\" — yolo composes these files whole and captures your " +
			"edits, so they are derived output: delete one and the next apply reproduces it")
	default:
		r.ok("host_management is \"assert\" — yolo owns the keys your packs declare and " +
			"rewrites only those; every other key in those files is yours and is left alone")
	}
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
// cannot run. That case is a [WARN] naming the reason (st.gateUnreachable); the rows below it
// carry the remedy.
func hostApplyOnLaunchRow(r *reporter, st wrapperState) {
	if config.HostApplyOnLaunchEnabled() {
		if why := st.gateUnreachable(); why != "" {
			r.warn("host_apply_on_launch is on, but the automatic sync cannot fire — "+why,
				"The re-check runs inside `yolo host -- <program>`, and only a generated "+
					"wrapper execs that, so a launch that does not go through a wrapper never "+
					"reaches it. Until one does, nothing re-checks this host's render; the "+
					"rows below say what to fix.")
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
