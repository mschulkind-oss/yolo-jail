package check

import (
	"os"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sectionHostWrappers observes whether the generated host launch-wrapper directory is
// actually on PATH (docs/design/host-agent-environment.md §5.5, OQ-4).
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

	dir := paths.WrapDir()
	names, dirErr := wrapperNames(dir)

	r.section("Host launch wrappers")
	hostApplyOnLaunchRow(r)

	if dirErr != nil && os.IsNotExist(dirErr) {
		r.warn("host_wrappers is on but no wrapper directory exists yet",
			"Run `yolo host apply --assert` to generate the wrappers for the programs "+
				"your selected packs install.")
		return
	}
	if dirErr != nil {
		r.warn("cannot read the wrapper directory "+dir, dirErr.Error())
		return
	}
	if len(names) == 0 {
		r.warn("host_wrappers is on but no wrappers are generated",
			"Either no selected pack installs a program, or `yolo host apply --assert` "+
				"has not run since you enabled the key.")
		return
	}

	if hostwrap.OnPath(o.Getenv("PATH"), dir) {
		r.ok("wrapper directory is on PATH (" + joinNames(names) + ")")
		return
	}
	r.warn("wrapper directory is NOT on PATH — the wrappers do nothing in this shell",
		"Generated "+joinNames(names)+" in "+dir+", but nothing on PATH reaches them, "+
			"so a bare `"+names[0]+"` runs unwrapped with no composed environment.\n"+
			"Add this line to your shell rc (it must PREPEND, ahead of ~/.local/bin):\n"+
			"  "+hostwrap.PathLine(dir)+"\n"+
			"`yolo host apply --shell-init` will append it for you. Either way "+
			dir+"/"+names[0]+" works right now as an absolute path.")
}

// hostApplyOnLaunchRow says whether a wrapped launch re-checks its own render before exec'ing
// (docs/design/host-apply-staleness.md §4.2, which asks for a line "when the feature is
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
// find. Neither state is a WARN — one is opted out of and the other is working.
func hostApplyOnLaunchRow(r *reporter) {
	if config.HostApplyOnLaunchEnabled() {
		r.ok("host_apply_on_launch is on — a wrapped launch re-checks the render first, and " +
			"stops to ask when it would change something")
		return
	}
	r.ok("host_apply_on_launch is off (the default) — a wrapped launch execs whatever the last " +
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
