package check

import (
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// sectionAutoGC observes the nix daemon's `min-free` setting — the automatic-GC
// safety net (storage-lifecycle §2). With §1 rooting in place, a non-zero
// min-free lets the daemon free UNROOTED store paths when a build runs low on
// space, bounding store growth without ever touching a running jail's rooted
// image closure. `min-free = 0` (the nix default) means that net is OFF.
//
// This is DETECT-AND-WARN only: min-free lives in host nix config and only a
// human can set it — yolo must not edit host nix config. The remedy names the
// one file the lines take effect in on this install and this OS's restart
// (nixSettingFile, nixDaemonRestart).
//
// DETERMINATE NIX NEEDS NO min-free: determinate-nixd runs its own disk-based
// garbage collection by default, independent of min-free, so a 0 there is not a
// missing net and gets an informational line rather than a warning.
//
// INSIDE A CONTAINER JAIL IT IS A HOST FACT. `nix config show` reports the
// settings of the process that runs it, so in a container jail it reads the jail's
// own nix config, not the host daemon's: MEASURED 2026-10-01, NIX_CONFIG="min-free
// = 123" in a jail's environment changed the min-free it reported, which a reading
// of the daemon could not see. This section used to say the opposite and graded the
// jail's own default as the host's GC being off. A macos-user jail is graded as the
// host is: its sandbox runs the host's own nix client against the host's /etc/nix.
//
// A WARN, never a FAIL: an unbounded store is a hygiene risk, not a broken jail,
// and on a huge disk it may be a deliberate choice. Skipped when nix is absent
// or the config can't be read (nothing to say).
func (o *Options) sectionAutoGC(r *reporter) {
	if _, hasNix := o.LookPath("nix"); !hasNix {
		return // no nix → the Nix section already failed; nothing to add here
	}
	if o.inJail() && !o.IsMacOS {
		r.sectionHeader("Nix auto-GC (store growth net)")
		r.hostFact("Nix auto-GC: the host daemon's min-free",
			"`nix config show` here reads this jail's own nix config, not the host daemon's. "+
				"Run `yolo check` on the host.")
		r.blank()
		return
	}
	res := o.Exec(nixCmdArgv("config", "show"), "", nil, 10*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return // couldn't read the daemon config — stay silent rather than guess
	}
	minFree, ok := nixdiag.MinFreeFromConfig(res.Stdout)
	if !ok {
		return // key absent/unparseable — don't invent a warning
	}
	r.sectionHeader("Nix auto-GC (store growth net)")
	switch {
	case minFree > 0:
		r.ok("nix min-free is set (" + humanBytes(minFree) + ") — the daemon auto-frees " +
			"unrooted store paths under space pressure")
	case o.nixDistribution() == storage.NixDeterminate:
		r.dim("Determinate Nix: determinate-nixd collects garbage on its own as free disk " +
			"runs low, so min-free = 0 does not leave the store unbounded")
	default:
		file, generated := o.nixSettingFile("min-free")
		where := "Add to " + file + ", e.g.\n" +
			"    min-free = 53687091200   # 50 GiB\n" +
			"    max-free = 214748364800  # 200 GiB\n"
		if generated {
			where = nixConfGeneratedNote + ": set them in that configuration and rebuild, e.g.\n" +
				"    nix.settings.min-free = 53687091200;   # 50 GiB\n" +
				"    nix.settings.max-free = 214748364800;  # 200 GiB\n"
		}
		r.warn("nix min-free = 0 — the daemon's automatic GC is OFF, so the store grows unbounded",
			"Set a min-free/max-free floor so the daemon reclaims UNROOTED store paths "+
				"automatically under space pressure. With the running image now GC-rooted "+
				"(storage §1) this is safe — a rooted closure is never a casualty. "+where+
				"Tune to your disk headroom; these are placeholders. Then:\n"+
				o.nixDaemonRestart())
	}
	r.blank()
}

// humanBytes renders a byte count as a short GiB/MiB/B string for the min-free
// PASS line (one decimal GiB above 1 GiB, whole MiB above 1 MiB, else bytes).
func humanBytes(n int64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%d MiB", n/mib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
