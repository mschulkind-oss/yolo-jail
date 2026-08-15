package loopholes

// supersede.go is the RUNTIME half of capability supersession
// (docs/design/pack-capabilities.md): the schema says what a manifest may DECLARE
// — `serves` on a loophole (internal/loopholedecl), `supersedes` on a pack
// (internal/packdecl) — and this file is where the two meet a real machine and a
// real set of selected packs.
//
// # The rule (§4)
//
//	Active()     = Enabled && !Superseded() && SupportedHere() && RequirementsMet()
//	Superseded() = serves is NON-EMPTY  AND  every served capability is superseded
//	               by some selected pack
//
//   - EVERY, not any. A loophole serving two jobs with one superseded still has a
//     job to do.
//   - NON-EMPTY `serves` is required, and this is the load-bearing half: SILENCE
//     MEANS "NOT PARTICIPATING", never a default claim. A manifest without the key
//     — every third-party one ever written, and two of the three bundled ones —
//     behaves byte-identically to before this file existed.
//
// # Why the claims arrive as DATA
//
// This package cannot import internal/packload: `loopholes` -> `config` ->
// `packload` is a cycle, measured in loopholedecl's package doc. So a pack's claims
// are pushed IN as plain values, exactly as its loophole MODULE DIRS already are
// (discover.go's PackModule) and for the same reason. internal/loopholes never
// learns what a pack is; it learns that some named thing claimed a capability, and
// carries the name and the reason so it can say who and why.
//
// EMPTY IS FAIL-SAFE at every branch, and here that is the SAFE direction rather
// than merely the cautious one: with no claims recorded, nothing is superseded and
// every loophole keeps running exactly as it does today. A missing record cannot
// silently turn something off.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// PackSupersession is one selected pack's claim that a capability's job no longer
// needs doing.
//
// A FLAT VALUE, not a *packdecl.Supersession plus a pack pointer, because the
// consumer needs exactly three strings and this package may not import the pack
// world (see the file doc). Pack is carried alongside the claim rather than derived
// later for the reason the whole design exists: an unexplained disappearance is the
// failure mode, so the record that turns something off must be able to name who did
// it and why without asking anybody else.
type PackSupersession struct {
	// Pack is the name of the pack that made the claim.
	Pack string
	// Capability is the named job it says no longer needs doing.
	Capability string
	// Because is the pack author's mandatory justification, printed wherever the
	// supersession takes effect. packdecl refuses an empty one, so this is non-empty
	// for any claim that came through a manifest; a hand-built zero value degrades to
	// "(no reason given)" in the rendering below rather than printing an empty quote.
	Because string
}

// Line renders one claim as the sentence a user reads: who, which job, and why.
// THE single rendering, so `loopholes list`, `loopholes status` and InactiveReason
// cannot drift into three shapes.
func (s PackSupersession) Line() string {
	because := s.Because
	if because == "" {
		because = "(no reason given)"
	}
	return fmt.Sprintf("superseded by pack %s — capability %s: %s",
		pytext.Repr(s.Pack), pytext.Repr(s.Capability), pytext.Repr(because))
}

// The process-wide record of the supersession claims THIS HOST's selected packs
// make, with the same two-tier shape (explicit record, then lazy resolver) that
// discover.go's packModules has — and for the same ordering reason: config
// validation runs before pack staging, so the authoritative record is empty at the
// moment some surfaces first ask.
//
// SEPARATE FROM packModules rather than folded into it, because the two are not the
// same set: a pack may supersede without shipping any loophole at all (the
// motivating case, a Bedrock auth pack, ships no loophole module and retires the
// bundled broker), and a pack may ship loopholes without superseding anything.
// Keying one off the other would make the common case unrepresentable.
var (
	packSupersessions    []PackSupersession
	packSupersessionsSet bool

	packSupersessionResolver  func() []PackSupersession
	packSupersessionsCached   []PackSupersession
	packSupersessionsResolved bool
)

// SetPackSupersessions records the supersession claims of this process's selected
// packs, from the STAGED view. Called by the host-side command that resolved the
// pack set (the same place SetPackModules is called from).
//
// It SUPERSEDES the lazy resolver for the rest of the process, for the reason
// SetPackModules gives: staging is the authoritative view, and a launch must not
// validate against one set and mount another.
func SetPackSupersessions(claims []PackSupersession) {
	packSupersessions = append([]PackSupersession(nil), claims...)
	packSupersessionsSet = true
}

// SetPackSupersessionResolver installs the LAZY FALLBACK for the surfaces that
// reach discovery without having staged anything — `yolo loopholes list`/`status`,
// `yolo check`, and the config validator on the launch path (which runs BEFORE
// staging). Registered once per process by the package that can resolve packs.
func SetPackSupersessionResolver(fn func() []PackSupersession) {
	packSupersessionResolver = fn
	packSupersessionsCached, packSupersessionsResolved = nil, false
}

// PackSupersessions returns the recorded claims: the staged record when one has
// been set, else the lazy resolver's memoized answer, else nothing.
func PackSupersessions() []PackSupersession {
	if packSupersessionsSet {
		return append([]PackSupersession(nil), packSupersessions...)
	}
	if packSupersessionResolver != nil && !packSupersessionsResolved {
		packSupersessionsCached, packSupersessionsResolved = packSupersessionResolver(), true
	}
	return append([]PackSupersession(nil), packSupersessionsCached...)
}

// ResetPackSupersessions clears BOTH the staged record and the resolver's cache.
// For tests: the record is deliberately process-wide (it IS the convergence point),
// which makes isolation mandatory.
func ResetPackSupersessions() {
	packSupersessions, packSupersessionsSet = nil, false
	packSupersessionsCached, packSupersessionsResolved = nil, false
}

// Superseded reports whether every capability this loophole serves has been
// retired by a selected pack — the third gate on Active().
//
// It reads a FIELD rather than consulting anything, which is why it can sit ahead
// of the `requires` probes in both Active() and InactiveReason(): the decision was
// made once at discovery, where the claims and the records were both in hand.
func (l *Loophole) Superseded() bool { return len(l.SupersededBy) > 0 }

// SupersessionReason renders the one-line explanation for an inactive-by-
// supersession loophole, or ("", false) when it is not superseded.
//
// Every claim is named, not just the first. A loophole serving two capabilities is
// only superseded when BOTH were claimed, and the two claims may come from
// different packs with different reasons — reporting one of them would name a pack
// that, on its own, did not turn anything off.
func (l *Loophole) SupersessionReason() (string, bool) {
	if !l.Superseded() {
		return "", false
	}
	lines := make([]string, len(l.SupersededBy))
	for i, s := range l.SupersededBy {
		lines[i] = s.Line()
	}
	return strings.Join(lines, "; "), true
}

// applySupersessions stamps each record with the claims that retired it.
//
// FIRST CLAIM WINS per capability, which is §5's "any supersession wins" made
// concrete. There is deliberately no conflict resolution and no `needs`: at three
// bundled loopholes the conflict is unreachable, and inventing arbitration before
// the conflict exists is what §6 rules out. The mitigation is visibility — whichever
// claim won is the one printed, so the reader can see which pack to talk to.
//
// It MUTATES the records, which is right because supersession is a fact about the
// resolved set rather than about any one manifest: the same manifest is superseded
// in one workspace's pack selection and not in another's.
func applySupersessions(records []*Loophole, claims []PackSupersession) {
	if len(claims) == 0 {
		return
	}
	byCapability := map[string]PackSupersession{}
	for _, c := range claims {
		if _, seen := byCapability[c.Capability]; !seen {
			byCapability[c.Capability] = c
		}
	}
	for _, lp := range records {
		if lp == nil || len(lp.Serves) == 0 {
			continue
		}
		winners := make([]PackSupersession, 0, len(lp.Serves))
		everyOne := true
		for _, capability := range lp.Serves {
			claim, ok := byCapability[capability]
			if !ok {
				everyOne = false
				break
			}
			winners = append(winners, claim)
		}
		if everyOne {
			lp.SupersededBy = winners
		}
	}
}

// unmatchedSupersessions reports every claim that matched no served capability —
// the typo case design §5 exists for.
//
// A TYPO SUPERSEDES NOTHING, SILENTLY, and the author believes it worked:
// `"capability": "claude-oauth-refersh"` matches no `serves`, so the broker keeps
// running and nothing anywhere says why. The message is most of the value here, so
// it names the unmatched string, suggests the nearest served one, and lists what IS
// served — "superseded nothing" would be useless.
//
// REPORTED, NOT REFUSED, and this is a deliberate departure from §5's wording ("a
// supersession matching no served capability is REFUSED AT LOAD"). §5's premise is
// that "the namespace is closed by the loopholes present, so this is decidable" —
// true of the SET, but the set is a fact about one machine at one moment, and a
// refusal keyed on it is refusable by circumstance:
//
//   - the claim is decodable long before the loopholes are. `pack.json` is validated
//     by `yolo pack lint` and by the in-jail entrypoint, neither of which has the
//     bundled+pack+user+config loophole set in hand (and the entrypoint cannot get
//     it: this package's whole cycle argument);
//   - it is the `tier` incident's shape a fourth time. A pack superseding a
//     capability served only by a NEWER bundled manifest would brick every jail on a
//     pre-`just load` image — a manifest yolo SHIPS, with no route to recovery;
//   - the failure direction of a warning is SAFE. An unmatched claim leaves the
//     loophole running, which is the status quo. A refusal would take down `yolo
//     loopholes list` — the very command a user runs to find out what happened — over
//     a pack's typo.
//
// So the structural half of §5 IS refused at load, in packdecl, where it is
// version-invariant (an empty `capability`, a missing `because`, a duplicate), and
// the match half is reported here, loudly, with the fix in the sentence.
func unmatchedSupersessions(records []*Loophole, claims []PackSupersession) []string {
	if len(claims) == 0 {
		return nil
	}
	servedSet := map[string]bool{}
	for _, lp := range records {
		if lp == nil {
			continue
		}
		for _, capability := range lp.Serves {
			servedSet[capability] = true
		}
	}
	served := make([]string, 0, len(servedSet))
	for capability := range servedSet {
		served = append(served, capability)
	}
	sort.Strings(served)

	var out []string
	reported := map[string]bool{}
	for _, c := range claims {
		if servedSet[c.Capability] || reported[c.Capability] {
			continue
		}
		reported[c.Capability] = true
		msg := fmt.Sprintf("pack %s supersedes capability %s, which NO loophole on this machine serves",
			pytext.Repr(c.Pack), pytext.Repr(c.Capability))
		if suggestion := nearestCapability(c.Capability, served); suggestion != "" {
			msg += fmt.Sprintf(" — did you mean %s?", pytext.Repr(suggestion))
		}
		if len(served) == 0 {
			msg += ". No loophole here declares `serves` at all, so nothing can be superseded"
		} else {
			msg += fmt.Sprintf(". Served here: [%s]", strings.Join(served, ", "))
		}
		msg += ". Nothing was superseded, so every loophole keeps running"
		out = append(out, msg)
	}
	return out
}

// nearestCapability suggests the served capability closest to a misspelled claim,
// or "" when nothing is close enough to be a fix rather than a guess.
//
// The threshold scales with the name's length (a third of it, capped at 4) because
// a fixed edit distance is wrong at both ends: 3 edits out of `x` is a different
// string, and 3 edits out of `claude-oauth-refresh` is obviously a typo. Ties break
// on the lexicographically first candidate so the message is deterministic.
func nearestCapability(want string, served []string) string {
	budget := len(want) / 3
	if budget > 4 {
		budget = 4
	}
	if budget < 1 {
		return ""
	}
	best, bestDist := "", budget+1
	for _, candidate := range served {
		if d := editDistance(want, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

// editDistance is plain Levenshtein over runes, two rows at a time. Small inputs
// (capability names), so the straightforward version is the right one.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
