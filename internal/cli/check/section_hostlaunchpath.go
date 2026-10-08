package check

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sectionHostLaunchPath is `yolo check`'s HOST LAUNCH section (docs/reference/host-agent-environment.md,
// one resolver): the LAUNCH PATH `yolo host` searches — the PATH yolo was started with, then `host_path`'s
// folders not already on it — with where each part came from, each `host_path` folder that does not
// exist, and every dependency of the selected packs that no floor entry answers for, resolved
// through the same lookup a launch uses (hostpath, HE-D5). A miss is a [WARN] carrying the miss line.
//
// IT SAYS WHICH PATH IT READ (HE-D7): the PATH of the shell this check runs in, which is usually a
// terminal's. A launcher with a bare PATH (a Waybar button, cron, a hotkey launcher) searches its
// own, so a green section here is a statement about this shell, not a promise about every launcher
// — and the section says so in its first line.
//
// A program a selected pack delivers is the floor's (sectionHostFloor), and is not repeated here.
// Silent when there is nothing to say: no `host_path`, and no dependency the floor does not answer.
func (o *Options) sectionHostLaunchPath(r *reporter) {
	if o.inJail() || !o.selectedPacksKnown {
		// A jail's PATH is its boot's, and `host_path` is host-only; a section driven without
		// sectionPacks knows no selection.
		return
	}
	lp := hostpath.Resolve(o.Getenv("PATH"))
	deps := o.launchPathDeps()
	if len(lp.Declared()) == 0 && len(lp.Refused()) == 0 && len(deps) == 0 {
		return
	}

	r.sectionHeader("Host launch PATH")
	r.dim("where `yolo host` looks for the programs your packs need and for a command yolo keeps no " +
		"copy of: read here from the PATH this check was started with, then host_path's folders. A " +
		"launcher with another PATH (a Waybar button, cron, a hotkey launcher) searches its own")
	sep := string(os.PathListSeparator)
	if caller := lp.Caller(); len(caller) > 0 {
		r.dim("the PATH this check was started with: " + strings.Join(caller, sep))
	} else {
		r.dim("this check was started with no PATH, so a launch like it searches host_path's folders alone")
	}
	home := paths.Home()
	added := map[string]bool{}
	for _, d := range lp.Added() {
		added[filepath.Clean(d)] = true
	}
	for _, d := range lp.Declared() {
		shown := tildeUnder(home, d)
		switch st, err := os.Stat(d); {
		case err != nil || !st.IsDir():
			r.dim(fmt.Sprintf("host_path: %s does not exist; a lookup skips it", shown))
		case !added[filepath.Clean(d)]:
			r.dim(fmt.Sprintf("host_path: %s is already on the PATH this check was started with", shown))
		default:
			r.dim(fmt.Sprintf("host_path: %s, searched after the PATH this check was started with", shown))
		}
	}
	// An entry the reader leaves out is never searched, by this check or by a launch; validation
	// above reports it as an error, and this says what it costs here.
	for _, rf := range lp.Refused() {
		r.dim(fmt.Sprintf("host_path: %s is ignored, so no lookup searches it: %s", rf.Entry, rf.Why))
	}

	for _, d := range deps {
		path, err := lp.LookPath(d.miss.Bin)
		switch {
		case err == nil:
			r.ok(fmt.Sprintf("%s — %s (%s)", d.miss.Bin, path, lp.Source(filepath.Dir(path))))
		case d.unpublished != "":
			// Ungraded — nothing could install it — but `yolo host -- <it>` runs whatever copy the
			// launch PATH holds (OQ-HE11 (a)), so the miss line says where it looked (HE-D2).
			r.dim(fmt.Sprintf("%s — not on this PATH, and no build for this host: %s", d.miss.Bin, d.unpublished))
			r.note(lp.MissLine(d.miss))
		default:
			r.warn(d.miss.Bin+" — not on this PATH", lp.MissLine(d.miss))
		}
	}
	r.blank()
}

// launchPathDep is one binary the section resolves: the miss line's subject, and why the vendor
// publishes no build for this host ("" when it does).
type launchPathDep struct {
	miss        hostpath.Miss
	unpublished string
}

// launchPathDeps is every binary the selected packs' `requires` and `program` contributions
// declare, sorted, less the programs the floor answers for: those run from the floor whatever a PATH
// holds, or, with no floor entry on this machine, are refused rather than looked up on a PATH
// (host-notch-readiness.md HNR-D2), and sectionHostFloor reports them. A program that is not the
// floor's to hold (Floor.OutsideTheFloor, HNR-D4) is looked up on the PATH, so it stays.
func (o *Options) launchPathDeps() []launchPathDep {
	var in []hostfloor.PackPrograms
	for _, p := range o.selectedPacks {
		installs, _ := p.HonoredInstalls()
		in = append(in, hostfloor.PackPrograms{Pack: p.Name, Installs: installs})
	}
	progs := hostfloor.Programs(in)
	floorAnswers := map[string]bool{}
	if len(progs) > 0 {
		floor := o.hostFloor(progs)
		for _, p := range progs {
			if floor.Status(p).Disposition != hostfloor.NoEntry || !floor.OutsideTheFloor(p) {
				floorAnswers[p.Bin()] = true
			}
		}
	}
	byBin := map[string]*launchPathDep{}
	for _, p := range o.selectedPacks {
		addPackDeps(byBin, p, floorAnswers)
	}
	out := make([]launchPathDep, 0, len(byBin))
	for _, d := range byBin {
		sort.Strings(d.miss.Requires)
		sort.Strings(d.miss.Programs)
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].miss.Bin < out[j].miss.Bin })
	return out
}

// addPackDeps records one pack's declared dependencies into byBin.
func addPackDeps(byBin map[string]*launchPathDep, p *packload.Pack, floorAnswers map[string]bool) {
	if p == nil || p.Decl == nil {
		return
	}
	unpublished := map[string]string{}
	for _, d := range p.Decl.DepRequirements() {
		unpublished[d.Bin] = d.UnpublishedReason(runtime.GOOS, runtime.GOARCH)
	}
	for _, c := range p.Decl.Contributions() {
		// A fork's own contribution declares its base's bin and installs nothing itself
		// (packdecl.Contribution.IsFork): the base's program is the one declarer.
		if (c.Kind != packdecl.KindRequires && c.Kind != packdecl.KindProgram) || c.Bin == "" ||
			c.IsFork() || floorAnswers[c.Bin] {
			continue
		}
		d := byBin[c.Bin]
		if d == nil {
			d = &launchPathDep{miss: hostpath.Miss{Bin: c.Bin}}
			byBin[c.Bin] = d
		}
		if c.Kind == packdecl.KindRequires {
			d.miss.Requires = appendOnce(d.miss.Requires, p.Name)
		} else {
			d.miss.Programs = appendOnce(d.miss.Programs, p.Name)
		}
		if u := unpublished[c.Bin]; u != "" && d.unpublished == "" {
			d.unpublished = u
		}
	}
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// tildeUnder writes a path under home as ~/….
func tildeUnder(home, p string) string {
	if home != "" && home != "/" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
