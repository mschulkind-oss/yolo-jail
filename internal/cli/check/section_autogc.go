package check

import (
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// sectionAutoGC observes the nix daemon's `min-free` setting — the automatic-GC
// safety net (storage-lifecycle §2). With §1 rooting in place, a non-zero
// min-free lets the daemon free UNROOTED store paths when a build runs low on
// space, bounding store growth without ever touching a running jail's rooted
// image closure. `min-free = 0` (the nix default) means that net is OFF.
//
// This is DETECT-AND-WARN only: min-free lives in /etc/nix/nix.conf (or a
// Determinate nix.custom.conf) and only a human can set it — yolo must not edit
// host nix config. In-jail `nix config show` reads the HOST daemon's effective
// config (NIX_REMOTE=daemon), so the observation is meaningful from here; the
// remedy is a host action either way.
//
// A WARN, never a FAIL: an unbounded store is a hygiene risk, not a broken jail,
// and on a huge disk it may be a deliberate choice. Skipped when nix is absent
// or the config can't be read (nothing to say).
func (o *Options) sectionAutoGC(r *reporter) {
	if _, hasNix := o.LookPath("nix"); !hasNix {
		return // no nix → the Nix section already failed; nothing to add here
	}
	res := o.Exec([]string{"nix", "--extra-experimental-features", "nix-command flakes",
		"config", "show"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return // couldn't read the daemon config — stay silent rather than guess
	}
	minFree, ok := nixdiag.MinFreeFromConfig(res.Stdout)
	if !ok {
		return // key absent/unparseable — don't invent a warning
	}
	r.section("Nix auto-GC (store growth net)")
	if minFree > 0 {
		r.ok("nix min-free is set (" + humanBytes(minFree) + ") — the daemon auto-frees " +
			"unrooted store paths under space pressure")
	} else {
		r.warn("nix min-free = 0 — the daemon's automatic GC is OFF, so the store grows unbounded",
			"Set a min-free/max-free floor in host nix config so the daemon reclaims "+
				"UNROOTED store paths automatically under space pressure. With the running "+
				"image now GC-rooted (storage §1) this is safe — a rooted closure is never "+
				"a casualty. Add to /etc/nix/nix.conf (or nix.custom.conf), e.g.\n"+
				"    min-free = 53687091200   # 50 GiB\n"+
				"    max-free = 214748364800  # 200 GiB\n"+
				"then restart the nix daemon. Tune to your disk headroom; these are placeholders.")
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
