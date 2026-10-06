package check

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sectionHostFloor reports the HOST AGENT FLOOR (docs/design/host-tool-provisioning.md §7): one
// row per program the selected packs declare, with its disposition — provisioned (the copy
// `yolo host` runs: its version, when it was installed, where it is), missing (and what installs
// it), or no floor entry (why, and what `yolo host` runs instead) — and beside it every OTHER copy
// of that program on PATH or at a hint location, named as not run by `yolo host`. It also reports
// an install running now, an interrupted one left behind, and an entry no selected pack delivers.
//
// It INSTALLS NOTHING and reaches no network: every fact comes from the prefix itself
// (hostfloor.Floor.Status), so `yolo check` stays an observe verb.
//
// Grading: a provisioned entry passes; a missing one is ungraded — it is not broken, the next
// launch installs it (HP-D3) — and so is a program the floor cannot hold, which is a fact about
// this machine, not a fault. Only an interrupted install's leftover warns, because it is disk
// nothing but `yolo prune --apply` will reclaim.
func (o *Options) sectionHostFloor(r *reporter) {
	if o.inJail() || !o.selectedPacksKnown {
		// A jail provisions its agents through its own launchers and has no host prefix; a
		// section driven without sectionPacks knows no selection and says nothing rather than
		// guessing.
		return
	}
	var in []hostfloor.PackPrograms
	for _, p := range o.selectedPacks {
		installs, _ := p.HonoredInstalls()
		in = append(in, hostfloor.PackPrograms{Pack: p.Name, Installs: installs})
	}
	progs := hostfloor.Programs(in)
	floor := o.hostFloor(progs)
	records := floor.Records()
	leftovers := floor.Leftovers()
	if len(progs) == 0 && len(records) == 0 && len(leftovers) == 0 {
		return
	}

	r.sectionHeader("Host agent floor")
	r.dim("yolo's own copies of your selected packs' agents, which `yolo host -- <agent>` runs, in " +
		floor.Dir)
	// THE LAUNCH PATH, as a launch from this shell would read it (hostpath; HE-D7): the PATH this
	// check was started with, then host_path's folders, as the child of `yolo host` searches it.
	lp := hostpath.Resolve(o.Getenv("PATH")).Child()
	home := paths.Home()
	selected := map[string]bool{}
	for _, p := range progs {
		selected[p.Bin()] = true
		st := floor.Status(p)
		switch st.Disposition {
		case hostfloor.Provisioned:
			r.ok(fmt.Sprintf("%s — yolo's floor copy %s, installed %s (%s)", p.Bin(), st.Record.Version,
				st.Record.Installed.Local().Format(time.DateOnly), st.Launcher))
			if st.Pending != "" {
				r.dim(fmt.Sprintf("%s: the next `yolo host -- %s` reinstalls it — %s", p.Bin(), p.Bin(), st.Pending))
			}
		case hostfloor.Missing:
			if st.Newer {
				// A newer yolo's record, which no launch of this yolo installs over (Ensure
				// refuses): the reason is that refusal, and its step is `yolo update`.
				r.dim(fmt.Sprintf("%s — %s", p.Bin(), st.Reason))
				break
			}
			// The launch installs at every host-management mode; the apply only under "own".
			by := "the first `yolo host -- " + p.Bin() + "` installs it"
			if hostOwned() {
				by = "the first `yolo host -- " + p.Bin() + "`, or `yolo host apply --assert`, installs it"
			}
			r.dim(fmt.Sprintf("%s — not in the floor yet (%s): %s", p.Bin(), st.Reason, by))
		case hostfloor.NoEntry:
			// With no floor entry, the copy on the launch's PATH IS what runs (OQ-HE11 (a)), so it
			// is named as that rather than as a copy `yolo host` does not run. The PATH is the
			// launch PATH as read here — the one this check was started with, then host_path's
			// folders — through the exec's own lookup: a launcher with another PATH may find
			// another copy, or none.
			runs := "and this check's PATH, with host_path's folders, has none"
			if c, err := lp.LookPathSkipping(p.Bin(), floor.BinDir()); err == nil {
				runs = "here, " + c
			}
			r.dim(fmt.Sprintf("%s — no floor entry: %s. `yolo host -- %s` runs the one on the PATH it is "+
				"started with, then host_path's folders (%s)", p.Bin(), st.Reason, p.Bin(), runs))
			// A copy the floor installed before it stopped holding this program is a deselected entry,
			// which yolo host never runs (the launch's lookup skips the floor's bin/).
			if _, left := records[p.Bin()]; left {
				r.dim(fmt.Sprintf("%s: yolo's floor still holds a copy it no longer keeps, which `yolo host` "+
					"does not run — %s", p.Bin(), floor.StaleCopyStep(hostOwned(), p.Bin())))
			}
		}
		if lk := floor.Lock(p.Bin()); lk.Held {
			r.dim(fmt.Sprintf("%s: an install is running now (pid %d)", p.Bin(), lk.PID))
		}
		if st.Disposition == hostfloor.NoEntry {
			continue
		}
		skip := hostpath.ManagedDirs()
		for _, other := range floor.OtherCopies(p.Bin(), lp.Value(), home, skip) {
			r.dim(fmt.Sprintf("%s: also at %s — not run by `yolo host`", p.Bin(), other))
		}
	}
	var stale []string
	for _, bin := range sortedRecordBins(records) {
		if !selected[bin] {
			stale = append(stale, bin)
		}
	}
	if len(stale) > 0 {
		r.dim(fmt.Sprintf("in the floor but no selected pack delivers %s any more: %s",
			strings.Join(stale, ", "), floor.StaleCopyStep(hostOwned(), stale...)))
	}
	if len(leftovers) > 0 {
		var ps []string
		for _, l := range leftovers {
			ps = append(ps, l.Path)
		}
		r.warn(fmt.Sprintf("%d interrupted install(s) left in the floor", len(leftovers)),
			"`yolo prune --apply` removes them:\n"+strings.Join(ps, "\n"))
	}
	r.blank()
}

// hostFloor is the floor this section reads: Options.HostFloor when the CLI wired it (the
// launch's own construction), otherwise the prefix under this home with the user-scope
// `host_floor`, which is all a disposition reads.
func (o *Options) hostFloor(progs []hostfloor.Program) *hostfloor.Floor {
	if o.HostFloor != nil {
		return o.HostFloor(progs)
	}
	wire := config.HostFloorWire()
	return &hostfloor.Floor{
		Dir: paths.HostFloorDir(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NodeFloor: hostfloor.HighestNodeFloor(progs),
		Include:   func(pack string) bool { return entrypoint.PackPolicyAllows(wire, pack) },
		Outranked: hostfloor.Outranker(func(bin string) []string {
			return config.ProvisionerOrder(config.ProvisionerEnvHost, bin)
		}, o.lookup()),
	}
}

func sortedRecordBins(m map[string]*hostfloor.Record) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hostOwned reports whether the declared host-management mode is "own", the one mode whose
// `yolo host apply --assert` removes what the floor no longer keeps (hostfloor.StaleCopyStep).
func hostOwned() bool { return config.HostManagementMode() == config.HostManagementOwn }
