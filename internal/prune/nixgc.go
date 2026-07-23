package prune

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// Bounded, rooting-aware nix store GC (storage-lifecycle §3). This is the ONLY
// place in the codebase that ever invokes `nix store gc`, and it is opt-in
// (--nix-gc, default OFF) and host-only for good reason:
//
//   - `nix store gc` respects GC roots inherently, so its safety rests ENTIRELY
//     on §1 rooting being in place. This section is defense-in-depth: it CONFIRMS
//     every protected image closure has a durable §1 root before it trusts the
//     GC to spare running jails. Without that confirmation a GC could delete a
//     live jail's image out from under it — the exact incident §1 exists to
//     prevent (235 of 467 /bin symlinks left dangling).
//   - It NEVER runs a blanket `nix-collect-garbage -d`. It runs a BOUNDED
//     `nix store gc --max <N>`: "free unrooted store paths up to N bytes; never
//     touch a path a live jail's rooted image needs."
//   - It REFUSES in-jail. In a jail `nix store gc` delegates to the HOST daemon
//     and would collect the HOST /nix/store, but from in-jail we cannot enumerate
//     the host's live jails to confirm their images are rooted — so a GC there
//     would be blind. The section skips with an explanatory line (see prune.go).

const (
	// nixGCDefaultMaxBytes bounds an --apply store GC. A ceiling, not a target:
	// nix stops once it has freed this much, so a single prune never runs away.
	nixGCDefaultMaxBytes int64 = 50 << 30 // 50 GiB
	// nixGCDryRunTimeout / nixGCApplyTimeout: determining live/dead paths over a
	// large store is not instant, and a bounded apply deletes as it goes.
	nixGCDryRunTimeout = 5 * time.Minute
	nixGCApplyTimeout  = 30 * time.Minute
)

// storeGCCountRe matches the `nix store gc` summary path count on stdout, for
// both the dry-run ("N store paths would be deleted") and apply ("N store paths
// deleted") phrasings.
var storeGCCountRe = regexp.MustCompile(`(?m)^(\d+) store paths? (?:would be deleted|deleted)`)

// storeGCFreedRe matches an optional "…, X.X MiB freed" clause nix prints on an
// --apply run. Best-effort: dry-run reports no byte figure (nix only determines
// the dead set), so Bytes stays unknown there.
var storeGCFreedRe = regexp.MustCompile(`([0-9.]+)\s*(B|KiB|MiB|GiB|TiB|bytes)\s+freed`)

// UnrootedProtectedPaths returns the subset of protected image store paths that
// lack a durable §1 GC root — no BUILD_DIR/roots/<sha16> symlink, or one that no
// longer points at the path. An EMPTY result is the precondition for a safe
// bounded store GC: every closure a running/recent jail depends on is pinned, so
// the GC cannot reach it. Keyed by image.ImageStoreKey so it reads the roots dir
// directly without reverse-mapping a symlink. Sorted for a deterministic report.
func UnrootedProtectedPaths(rootsDir string, protected map[string]struct{}) []string {
	unrooted := []string{}
	for p := range protected {
		link := filepath.Join(rootsDir, image.ImageStoreKey(p))
		if target, err := os.Readlink(link); err != nil || target != p {
			unrooted = append(unrooted, p)
		}
	}
	sort.Strings(unrooted)
	return unrooted
}

// StoreGCOutcome is the parsed result of a `nix store gc` invocation.
type StoreGCOutcome struct {
	// Ran is false when the nix subprocess was absent, failed to start, timed
	// out, or exited non-zero (the RunFunc degrade) — the section then reports a
	// skip rather than a bogus zero.
	Ran bool
	// Paths is the store-path count nix reported (deleted on apply, or that WOULD
	// be deleted on dry-run).
	Paths int
	// HaveBytes / Bytes carry the freed-byte figure when nix reported one (apply
	// only; dry-run has none, so HaveBytes stays false).
	HaveBytes bool
	Bytes     int64
	// Summary is the raw matched summary line, shown when the count parse is
	// partial so the user still sees nix's own words.
	Summary string
}

// RunNixStoreGC invokes `nix store gc` through the injected exec seam: on dry-run
// (apply=false) `--dry-run` (read-only; shows the full reclaimable set as the
// ceiling), on apply `--max <maxBytes>` (bounded). The two nix flags are mutually
// exclusive, which is why the mode picks exactly one. Parses the path count (and,
// on apply, an optional freed-bytes clause) from stdout. A degraded/failed run
// yields Ran=false.
func RunNixStoreGC(run RunFunc, maxBytes int64, apply bool) StoreGCOutcome {
	argv := []string{"nix", "--extra-experimental-features", "nix-command flakes", "store", "gc"}
	timeout := nixGCDryRunTimeout
	if apply {
		argv = append(argv, "--max", strconv.FormatInt(maxBytes, 10))
		timeout = nixGCApplyTimeout
	} else {
		argv = append(argv, "--dry-run")
	}
	res := run(argv, timeout)
	if !res.Ran || res.RC != 0 {
		return StoreGCOutcome{Ran: false}
	}
	out := StoreGCOutcome{Ran: true}
	if m := storeGCCountRe.FindStringSubmatch(res.Stdout); m != nil {
		out.Summary = strings.TrimSpace(m[0])
		if n, err := strconv.Atoi(m[1]); err == nil {
			out.Paths = n
		}
	}
	if m := storeGCFreedRe.FindStringSubmatch(res.Stdout); m != nil {
		if b, ok := parseHumanBytes(m[1], m[2]); ok {
			out.HaveBytes = true
			out.Bytes = b
		}
	}
	return out
}

// parseHumanBytes converts a nix "X.X <unit>" freed figure to bytes. Best-effort:
// an unparseable number yields ok=false.
func parseHumanBytes(num, unit string) (int64, bool) {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	var mult float64
	switch unit {
	case "B", "bytes":
		mult = 1
	case "KiB":
		mult = 1 << 10
	case "MiB":
		mult = 1 << 20
	case "GiB":
		mult = 1 << 30
	case "TiB":
		mult = 1 << 40
	default:
		return 0, false
	}
	return int64(f * mult), true
}
