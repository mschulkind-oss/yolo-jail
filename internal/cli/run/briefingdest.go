package run

// briefingdest.go is the ONE enumeration of a jail's briefing destinations, plus the ONE
// encoding of the staging filename each of them gets.
//
// It exists because two halves of one launch have to agree about both, and they live in
// different functions: refreshJailBriefings composes and WRITES a staging file per
// destination, and assembleRunCmd bind-MOUNTS that file at the destination. A missing bind
// source for a FILE is not an error the way a missing directory is — podman happily mounts
// an empty one — so a disagreement here does not fail the launch, it produces a jail whose
// agent reads a blank briefing. That is exactly the shape briefing-audiences.md R2 names, and
// the two spellings were coupled only by a comment before this file.
//
// THE STAGING KEY IS THE DESTINATION, and that is the change §5 calls the jail half's whole
// structural cost. It used to be the PACK, which was sufficient only while every destination
// received the SAME composed body: a pack per file and a file per pack, with the mount loop
// picking the first pack at each path and the others' files written and never read. Once
// composition is per destination — which is what an audience means — the content of two
// staging files differs, so the key has to be the thing the content varies with.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// briefingDest is one briefing destination a jail delivers to, with everything the compose
// and mount halves need about it and nothing else.
type briefingDest struct {
	// Into is the home-relative path the composed briefing is mounted at. Never empty —
	// briefingDestinations drops a contribution that names an audience instead of a path.
	Into string
	// Agent is the IDENTITY its owning pack declared for it, or "" for a destination that
	// declared none. "" is not an error: it means no `agents` selector can name this
	// destination, which is the state every pack.json was in before the field existed (R4).
	Agent string
	// After is the declaring contribution's `after`, verbatim — `"host:<path>"` to prepend
	// the user's own file, or "".
	//
	// THERE WAS A MayAccessHost BESIDE IT, the declaring pack's origin gate, deciding
	// whether the `after` host file could be read at all. OQ-TP9 deleted it
	// (docs/design/trust-paths.md, 2026-09-04): a fetched pack's `after: "host:AGENTS.md"`
	// is honored like anyone else's, so the destination carries only what it declares.
	After string
}

// briefingDestinations enumerates a jail's briefing destinations, deduplicated by path with
// the FIRST declaration winning.
//
// FIRST WINS because the MOUNT made that the rule, not because it is the nicest policy.
// `briefing` is CombineConcat — several packs contributing prose at one path is the designed
// behavior, and the composition below merges all of them — but podman rejects a duplicate
// mount destination and kills the boot, so assembleRunCmd has always emitted one bind per
// path and dropped the rest. Pulling that dedup up here is what lets the write half agree
// with it by construction instead of by comment.
func briefingDestinations(packs []*packload.Pack) []briefingDest {
	var out []briefingDest
	seen := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			// `into` is CHECKED, not assumed, exactly as ComposeHostBriefings and
			// hostBriefingPaths check it at the host notch. Since briefing-audiences.md a
			// contribution may legally name an AUDIENCE instead of a destination, and the
			// mount half appends `":/home/agent/" + Into`: an empty `into` would bind a
			// single staged FILE over /home/agent itself, which is the jail's whole home.
			if c.Kind != packdecl.KindBriefing || c.Into == "" || seen[c.Into] {
				continue
			}
			seen[c.Into] = true
			out = append(out, briefingDest{Into: c.Into, Agent: c.Agent, After: c.After})
		}
	}
	return out
}

// briefingStagingName is the staging filename for ONE briefing destination.
//
// The encoding is RFC 6901's (JSON Pointer): `~` becomes `~0`, `/` becomes `~1`, in one pass.
// INJECTIVITY is the only property it must have, and it is not a nicety — two destinations
// sharing a staging file would deliver one agent's composed briefing to the other, silently.
// A hash prefix would be injective only probabilistically; a borrowed, provably-injective
// escape is one line and needs no argument about collision odds.
//
// THE OBVIOUS ESCAPE IS NOT INJECTIVE, and this is written down because it was tried and
// TestBriefingStagingNameIsInjective caught it: doubling `~` and mapping `/` to `~` sends both
// `a/~b` and `a~/b` to `a~~~b`, because `~~~` cannot say whether it is `~` + `~~` or `~~` +
// `~`. Two escape sequences that cannot prefix each other is what fixes it, which is exactly
// what RFC 6901 chose them for.
//
// Nothing decodes it. The name is written by refreshJailBriefings and read by assembleRunCmd,
// both through this function, and no third reader exists — so the escape is a one-way
// disambiguator rather than a serialization format. It stays readable anyway, which matters
// when the thing being debugged is "which file did this jail actually mount":
// `.claude/CLAUDE.md` becomes `briefing-.claude~1CLAUDE.md`.
func briefingStagingName(dest string) string {
	var b strings.Builder
	b.WriteString("briefing-")
	for _, r := range dest {
		switch r {
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
