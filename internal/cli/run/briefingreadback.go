package run

// briefingreadback.go decides whether a pack's `after: "host:<path>"` file may be prepended to
// a jail briefing. The rule it enforces is one sentence: YOLO NEVER READS ITS OWN OUTPUT BACK
// AS INPUT.

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// launcherInJail is config.InJail behind a seam, so a test states which side of the jail
// boundary it is on instead of inheriting it from YOLO_VERSION — which is set when the suite
// runs inside a jail and unset in CI, so an implicit dependency passes in one and fails in
// the other. Tests override it and restore the PREVIOUS value with t.Cleanup.
var launcherInJail = config.InJail

// mayPrependHostBriefing reports whether the host briefing at src — the absolute path a
// destination's `after: "host:<path>"` resolves to under home — may be prepended to that
// destination's composed jail briefing. It is false for a file yolo wrote itself, which
// would otherwise arrive twice:
//
//   - ON THE HOST, a file `yolo host apply` composed (generated, the
//     entrypoint.GeneratedHostBriefings record). That record is the only thing that knows
//     which host files are yolo's, and outside a jail it alone decides.
//   - IN A JAIL, any src that is one of the selected pack set's briefing DESTINATIONS. There
//     `home` is the OUTER jail's home, and every briefing destination in it is a read-only
//     bind of the outer launch's own staging file (/proc/self/mountinfo shows
//     `.../agents/<jail>/briefing-.claude~1CLAUDE.md /home/agent/.claude/CLAUDE.md ro`). The
//     GeneratedHostBriefings record cannot see that — it lists host-apply output only — so a
//     nested launch prepended the outer jail's ENTIRE composed briefing to its own (measured
//     2026-09-22: every heading twice, separated by `---`). Every shipped agent pack declares
//     `after` equal to its own `into`, so this was every nested jail, not a corner.
//
// WHY THE INHERITANCE SPLIT DID NOT ALREADY COVER IT. internal/config/inherit.go (OQ-LP9)
// splits what an inner jail inherits from the outer one, but it splits CONFIG. A pack's
// `after: host:` read is not config: it is a launch-time read of the home directory, so it
// went straight past that split and read whatever the outer jail had mounted there.
//
// A src that is NOT a destination is still prepended in a jail: nothing yolo stages lives
// there, so whatever is there is the user's own (or an inherited mount of it).
//
// BOTH CHECKS ARE BY PATH AND BY FILE IDENTITY (os.SameFile), as the host composition's own
// guard is (entrypoint.sameFileAsYoloOutput). In a dotfiles layout one file has two names —
// ~/.foo/AGENTS.md a link to ~/AGENTS.md, with `after: "host:AGENTS.md"` — and `yolo host apply`
// writes its composition through the link, so the record lists ~/.foo/AGENTS.md while src is
// ~/AGENTS.md; by path alone the launch prepended yolo's own composition to its own.
func mayPrependHostBriefing(src, home string, dests []briefingDest, generated map[string]bool) bool {
	if generated[src] || sameFileAsAny(src, generatedPaths(generated)) {
		return false
	}
	if launcherInJail() && isBriefingDestination(src, home, dests) {
		return false
	}
	return true
}

// isBriefingDestination reports whether src is where one of dests is mounted under home, by
// path or by file identity.
func isBriefingDestination(src, home string, dests []briefingDest) bool {
	src = filepath.Clean(src)
	var candidates []string
	for _, d := range dests {
		p := filepath.Join(home, filepath.FromSlash(d.Into))
		if p == src {
			return true
		}
		candidates = append(candidates, p)
	}
	return sameFileAsAny(src, candidates)
}

// generatedPaths is the record's paths, for sameFileAsAny.
func generatedPaths(generated map[string]bool) []string {
	out := make([]string, 0, len(generated))
	for p, ok := range generated {
		if ok {
			out = append(out, p)
		}
	}
	return out
}

// sameFileAsAny reports whether src is, by file identity, one of candidates. os.Stat follows links
// and never opens the file, so a FIFO cannot stall it; a src or candidate it cannot stat (absent,
// or a link to nowhere) matches nothing, so the answer fails open to "the user's own", as
// GeneratedHostBriefings does.
func sameFileAsAny(src string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	info, err := os.Stat(src)
	if err != nil {
		return false
	}
	for _, c := range candidates {
		if other, err := os.Stat(c); err == nil && os.SameFile(info, other) {
			return true
		}
	}
	return false
}
