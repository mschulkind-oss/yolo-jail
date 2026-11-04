package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// devBundleReapVerb is `yolo internal dev-bundle-reap [--dir <dev-bundles dir>]`, which
// scripts/matched-bundle.py runs with the launcher it has just published, so the backlog of
// development bundles is collected where it grows. It is the same rule `yolo prune` and the
// housekeeping slot apply (prune.PruneDevBundles) — one implementation, run by the newest
// binary — and it prints only when it removed something or declined.
const devBundleReapVerb = "dev-bundle-reap"

// devBundleReapDeps are the seams runDevBundleReap reads, so its tests never ask the real
// runtime or process table.
type devBundleReapDeps struct {
	storage func() string
	rt      func() string
	run     prune.RunFunc
	procs   func() ([]prune.ProcStart, bool)
	now     func() time.Time
	getenv  func(string) string
	// osCanAnswer is prune.DevBundleOSCanAnswer: false on an OS whose sweep can never remove a
	// bundle, where the verb asks nothing and names the by-hand removal instead of a retry.
	osCanAnswer func() bool
}

// devBundleReapOptOutEnv is the housekeeping slot's automatic-reap opt-out
// (run.autoReapOptOutEnv). The post-build sweep is automatic too, so it honors the same one.
const devBundleReapOptOutEnv = "YOLO_NO_AUTO_IMAGE_REAP"

func defaultDevBundleReapDeps() devBundleReapDeps {
	return devBundleReapDeps{
		storage:     paths.GlobalStorage,
		rt:          func() string { return selectedSetupBackend(nil) },
		run:         prune.ProbeExec,
		procs:       prune.DevBundleProcStarts,
		now:         time.Now,
		getenv:      os.Getenv,
		osCanAnswer: prune.DevBundleOSCanAnswer,
	}
}

func runDevBundleReap(args []string, stderr io.Writer, d devBundleReapDeps) int {
	dir := prune.DevBundlesDirUnder(d.storage())
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--dir" && args[1] != "":
		dir = args[1]
	default:
		fmt.Fprintln(stderr, "usage: yolo internal "+devBundleReapVerb+" [--dir <dev-bundles dir>]")
		return 2
	}
	if d.getenv(devBundleReapOptOutEnv) != "" {
		return 0
	}
	if !d.osCanAnswer() {
		// Checked before any evidence is gathered: no answer here would let a bundle go. Said
		// only once the backlog is past what a sweep would keep, so a fresh checkout is quiet.
		if n := prune.CountDevBundles(dir); n > prune.DevBundleKeep {
			fmt.Fprintf(stderr, "%d development bundles under %s are kept: %s. Remove ones you know "+
				"are unused by hand: %s\n", n, dir, prune.DevBundleOSWhy, prune.DevBundleByHand)
		}
		return 0
	}
	rt := d.rt()
	live := prune.LiveYoloContainers(rt, d.run)
	ev := prune.DevBundleEvidence(rt, live, d.run, d.procs)
	r := prune.PruneDevBundles(dir, prune.DevBundleKeep, ev, true, d.now(), nil)
	switch {
	case r.Declined != "":
		fmt.Fprintf(stderr, "Kept every old development bundle: %s. `yolo prune` reports them.\n", r.Declined)
	case r.Unknown != "":
		fmt.Fprintf(stderr, "Kept every old development bundle: %s. `yolo prune --apply` retries.\n", r.Unknown)
	}
	failed := 0
	for _, e := range r.Removed {
		if e.Err != nil {
			failed++
			fmt.Fprintf(stderr, "Could not remove development bundle %s: %v. `yolo prune --apply` retries.\n", e.Path, e.Err)
		}
	}
	if n := r.RemovedCount(); n > 0 {
		fmt.Fprintf(stderr, "Removed %d unused development bundle(s), %s (the newest %d are kept).\n",
			n, prune.FmtBytes(r.Bytes), prune.DevBundleKeep)
	}
	if failed > 0 {
		return 1
	}
	return 0
}
