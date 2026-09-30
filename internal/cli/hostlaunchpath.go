package cli

import (
	"os"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostlaunchpath.go wires the LAUNCH PATH (internal/hostpath; docs/design/host-launch-environment.md
// §2.2, §3) into the host verbs: the PATH yolo's checks read at the host — the PATH this process was
// started with, then each `host_path` folder not already on it — and the pack names a miss line
// puts beside a program.
//
// Its callers are every host PATH check (HE-D5): the launch gate's dependency survey and `yolo host
// apply`'s pre-flight (probeHostDeps), the install the dependency gate runs and its re-probe
// (gateHostDeps, HE-D6), `yolo check-deps` (checkDepsMain) and the target lookup of `yolo host --`
// (resolveHostLaunchTarget). A program a selected pack delivers is the floor's to answer for, and
// never reaches the lookup.

// hostLaunchPath is the launch PATH of this process: the one resolver, handed this process's PATH.
// It is computed from the same two inputs — the PATH yolo was started with and the user config's
// `host_path` — every time it is asked, and neither changes in a process's life, so every check a
// process makes reads one value.
func hostLaunchPath() *hostpath.Launch { return hostpath.Resolve(os.Getenv("PATH")) }

// hostChildLaunch is the launch PATH the CHILD searches, which the exec's lookup reads too: the
// launch PATH itself, or — for a launch started with no PATH at all — the floor installer's system
// folders in its place, ahead of `host_path`'s (the floor design's HP-D12; the checks keep HE-D4's
// `host_path` alone).
func hostChildLaunch(lp *hostpath.Launch) *hostpath.Launch {
	return lp.WithStandIn(hostfloor.BaselinePath())
}

// depDeclarers is which selected packs declare one binary, by kind: the "required by" a miss line
// puts beside a program.
type depDeclarers struct{ requires, programs []string }

// note records one declaration. A pack is recorded once per kind.
func (d *depDeclarers) note(pack string, k packdecl.Kind) {
	list := &d.programs
	if k == packdecl.KindRequires {
		list = &d.requires
	}
	for _, p := range *list {
		if p == pack {
			return
		}
	}
	*list = append(*list, pack)
}

// miss is the miss line's subject for bin: the program and the packs that declare it.
func (d *depDeclarers) miss(bin string, launch bool) hostpath.Miss {
	m := hostpath.Miss{Bin: bin, Launch: launch}
	if d != nil {
		m.Requires = append([]string(nil), d.requires...)
		m.Programs = append([]string(nil), d.programs...)
		sort.Strings(m.Requires)
		sort.Strings(m.Programs)
	}
	return m
}

// declarersOf is every binary the packs' `program` and `requires` contributions declare, with the
// packs that declare it.
func declarersOf(packs []*packload.Pack) map[string]*depDeclarers {
	out := map[string]*depDeclarers{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if !isDepKind(c.Kind) || c.Bin == "" {
				continue
			}
			d := out[c.Bin]
			if d == nil {
				d = &depDeclarers{}
				out[c.Bin] = d
			}
			d.note(p.Name, c.Kind)
		}
	}
	return out
}
