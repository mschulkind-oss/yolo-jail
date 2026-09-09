package run

// hostcasalias.go is the HOST half of L9 — the launch's decision about whether a
// recognised content-addressed cache is ALIASED from the host instead of pooled a
// second time inside the jail (docs/design/disk-levers-and-backfill.md §3 L9,
// ruled as OQ-BF10 on 2026-09-08).
//
// The DECISION and its justification live in internal/hostcas, which `yolo
// stores` reads too. This file is the binding: it turns the launch's own seams
// into hostcas.Facts, provisions the destination's backing directory, and
// discloses what it did. It contains no gate of its own, so the mount a launch
// emits and the row `yolo stores` prints cannot disagree.
//
// THE DECISION IS NOT RECORDED ANYWHERE, on purpose. It is a pure function of
// the host's filesystem and platform, so both readers re-derive it; a stamp under
// BuildDir() would be a second source of truth for a question the disk already
// answers, and it would go stale the first time a user deleted their pants cache.
// That is also why this file names no writer: there is nothing written.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// planHostCASAlias settles L9 for this launch: one hostcas.Disposition per
// recognised store, aliased or not, with the reason.
//
// It never refuses a launch and returns no error, because there is no failure
// here that a jail should not start through. Every negative outcome is the status
// quo — the jail keeps pooling its own copy — which is exactly the behaviour of
// every yolo that shipped before this.
// relocations are the user's `cache_relocations`, already loaded by the
// pipeline. They are passed IN rather than read here because an explicit config
// decision about where a cache lives outranks this optimisation, and the gate
// that enforces that lives in hostcas with the rest of them.
func (o *Options) planHostCASAlias(rt string, relocations []config.CacheRelocation) []hostcas.Disposition {
	relocated := make([]string, 0, len(relocations))
	for _, r := range relocations {
		relocated = append(relocated, r.Subdir)
	}
	return hostcas.Plan(hostcas.Facts{
		Runtime: rt,
		IsMacOS: o.IsMacOS,
		// The HOST's platform, and the JAIL's, spelled as two values so the
		// comparison in hostcas is over facts rather than over an OS check.
		HostPlatform: hostcas.HostPlatform(),
		JailPlatform: hostcas.JailPlatform(),
		// Through o.Getenv, never os.Getenv: a frozen argv has to stay a function
		// of its inputs. goldenOptions returns "" for every variable, which is what
		// keeps every golden argv in this package free of an alias mount without
		// any fixture having to know this feature exists.
		HostCacheRoot:     hostcas.CacheRoot(o.Getenv),
		JailCacheHost:     paths.GlobalCache(),
		RelocatedSegments: relocated,
		Probe:             o.HostCASProbe,
	})
}

// prepareHostCASAlias creates the DESTINATION's mountpoint for every aliased
// store, before the argv is assembled.
//
// THE MOUNTPOINT AND THE STRANDED COPY ARE THE SAME DIRECTORY, and that is worth
// reading twice: the destination /home/agent/.cache/<rel> sits inside the
// paths.GlobalCache() bind, so its host side is GlobalCache()/<rel> — which is
// exactly where the jail's own private copy already is. The alias mounts over
// the private copy IN PLACE, which is what makes the migration question
// ([Disposition.Stranded]) answerable at all: nothing is moved or deleted, the
// bytes are simply no longer the ones the jail reads.
//
// BOTH HALVES OF cache_relocations' ordering rule are MEASURED here, on podman
// 5.8.4, 2026-09-09, against a real `localhost/yolo-jail` image:
//
//   - a missing bind SOURCE kills the whole container with a bare
//     "statfs …: no such file or directory". That is why hostcas declines on
//     absence rather than creating anything.
//   - a missing DESTINATION does get auto-created by crun, two levels deep,
//     inside the read-write .cache bind — so this MkdirAll is not what makes the
//     mount work. What it changes is what the mountpoint IS: crun's is root-owned
//     mode 0755 with the STICKY BIT set (drwxr-xr-t), left behind in the user's
//     cache after the jail exits. One MkdirAll makes it an ordinary directory the
//     jail already owns, which is the same reason podmanBaseMounts pre-creates
//     its own nested mountpoints.
//
// It never touches the SOURCE. Creating a missing host store would alias an empty
// directory over whatever the jail has, which is the one outcome strictly worse
// than doing nothing — so hostcas declines on absence and this is only ever
// reached for stores that already exist.
//
// A failure is NOT fatal, unlike EnsureCacheRelocations': the alias is an
// optimisation whose failure mode is the status quo, and refusing a launch over
// it would trade a working jail for a preference about where bytes live. A
// disposition that could not be provisioned is DOWNGRADED rather than dropped, so
// no argv names a destination whose mountpoint is missing and the inventory can
// still explain what happened.
func prepareHostCASAlias(ds []hostcas.Disposition) []hostcas.Disposition {
	out := make([]hostcas.Disposition, 0, len(ds))
	for _, d := range ds {
		if !d.Aliased {
			out = append(out, d)
			continue
		}
		switch {
		case d.Stranded == "":
			d.Aliased = false
			d.Code = hostcas.CodeSameTree
			d.Reason = "the jail's own cache directory could not be resolved, so there is " +
				"no mountpoint to alias over"
		default:
			if err := os.MkdirAll(d.Stranded, 0o755); err != nil {
				d.Aliased = false
				d.Code = hostcas.CodeUnwritable
				d.Reason = "the mountpoint " + d.Stranded + " could not be created: " + err.Error()
			}
		}
		out = append(out, d)
	}
	return out
}

// noteHostCASAlias discloses this launch's L9 decision on stderr, in the same
// dim register and at the same point as notePackHostAccess — because it is the
// same class of fact. A WRITABLE BIND OF THE HOST USER'S OWN CACHE IS HOST ACCESS,
// and yolo's convention is that effective host access is visible at every launch
// rather than recorded once somewhere.
//
// Two things print, and nothing else:
//
//   - an ACTIVE alias, always. This is the disclosure, and it is not suppressible:
//     the trust step is the writable direction, so the launch says what it granted
//     and where.
//   - a DECLINED alias whose host store EXISTS. That is a happy path that
//     degraded, and §5.4's failure discipline is that a degradation is never
//     silent.
//
// An absent host store prints NOTHING. There was no happy path to degrade from —
// the machine does not run the tool — and §5.3's own degenerate rule ("an empty
// store or a class with 0 B reclaimable is silent") is the precedent. Restating
// the absence of a pants cache on every launch of every jail on every machine
// would be noise, not disclosure, and §5.1's warning about the slot printing over
// a running agent's TUI is what that noise costs. `yolo stores` is where the
// full per-store decision is legible on demand.
func (o *Options) noteHostCASAlias(ds []hostcas.Disposition) {
	var active, degraded []hostcas.Disposition
	for _, d := range ds {
		switch {
		case d.Aliased:
			active = append(active, d)
		case d.Code != hostcas.CodeAbsent && d.Code != hostcas.CodeBackend &&
			d.Code != hostcas.CodeMacOS && d.Code != hostcas.CodePlatform &&
			d.Code != hostcas.CodeNoCacheRoot:
			degraded = append(degraded, d)
		}
	}
	if len(active) == 0 && len(degraded) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	for _, d := range active {
		out.print("[dim]Host cache aliased (writable): " + d.Source + " → " + d.Dest +
			" — this jail shares the host's " + d.Store.Tool +
			" store instead of keeping a second copy.[/dim]")
	}
	for _, d := range degraded {
		out.printf("[yellow]Not aliasing the host %s store:[/yellow] %s. "+
			"[dim]This jail keeps its own copy at %s, as before.[/dim]",
			d.Store.Tool, d.Reason, d.Stranded)
	}
}

// hostCASAliasArgs is the -v pairs for this launch's aliased stores. Emitted by
// podmanBaseMounts, adjacent to the cache_relocations block it is the same shape
// as: a rw bind nested INSIDE the paths.GlobalCache() → /home/agent/.cache mount,
// so ~/.cache/<rel> in the jail is an ordinary writable directory backed by other
// storage. Table order, so the argv is deterministic.
func hostCASAliasArgs(ds []hostcas.Disposition) []string {
	var args []string
	for _, d := range hostcas.Aliased(ds) {
		args = append(args, "-v", d.Source+":"+d.Dest)
	}
	return args
}
