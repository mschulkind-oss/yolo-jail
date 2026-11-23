package check

// packlocal.go reports a `local` pack's clone (docs/design/local-pack-clones.md §8, slice 7): every §6.2
// condition the clone alone decides, as a finding with its next step. Read-only and network-free: it runs
// read-only git against the clone, probes no origin and writes no launch record. What a launch then decides
// (working tree or origin) is `yolo pack status`'s, from the record the launch wrote.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// reportLocalClones grades each `local` entry's clone: usable is a pass, an absent clone a pass that says
// origin's copy runs (one config shared by two machines, LPC-D8, is not a fault), and a clone the launch
// would not use a warning, as the launch prints it.
func reportLocalClones(r *reporter, entries []config.PackEntry) {
	store := &packsrc.Store{Dir: paths.PacksDir()}
	for _, e := range entries {
		if e.Local == "" {
			continue
		}
		addr, err := e.PackAddress()
		if err != nil || addr.Kind != packsrc.KindGit {
			continue // resolution reports a bad address, by name
		}
		shown := termsafe.Visible(packsrc.TildePath(e.Local, paths.Home()))
		st := store.InspectLocalClone(addr, e.Local)
		switch {
		case st.Absent:
			r.ok(e.Name + ": no local clone at " + shown + "; origin's copy runs")
			r.note("To use a clone here: git clone " + addr.Repo + " " + shown + "\nOr remove `local` from the entry (`yolo config set`), then rerun `yolo check`.")
		case st.NotUsed != "":
			r.warn(e.Name+": local clone "+shown+" not used: "+termsafe.VisibleLines(st.NotUsed),
				"Origin's copy runs until this is fixed. Fix it, then rerun `yolo check`.")
		default:
			r.ok(e.Name + ": local clone " + shown + " is usable (branch " + strings.TrimPrefix(addr.Ref, "refs/heads/") + ", remote " + st.Remote + ")")
		}
	}
}
