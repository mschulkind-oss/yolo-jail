package run

// packloopholes.go is the SPAWN BOUNDARY for a pack-shipped loophole: what the user is told
// before any host code runs, and what they are told when the backend means none will
// (docs/design/loophole-packaging.md §4.3 G4, §8 item 2).
//
// Two things live here, in that order:
//
//  1. The DISCLOSURE CLASSIFICATION — which claim kinds cross the boundary, and whether a
//     kind's crossing is a host READ or host EXECUTION. Data, not a hardcoded switch inside
//     the printer, because the printer's hardcoded set is exactly the defect §3.3 measured:
//     "notePackHostAccess switches on KindMount, KindReadsHost, KindEnv and drops every
//     other claim kind" — with no test to catch the next kind that gets dropped.
//  2. startLoopholesDisclosed — the ONE call site of startLoopholes, which prints before it
//     spawns. §4.3 G4: the spawn preceded the notice by an entire phase, and the spawn is
//     silent on success, so "a fetched pack's daemon could start on every launch for months
//     with the only host-side record being a lockfile the user has to go read."
//
// The INERT REPORT the wrapper also calls is its own file (loopholeinert.go): it answers a
// different question ("will this even run here?") on a different axis pair, and it is the one
// half that has nothing to do with ordering.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// packLoopholeKindName is the `contributes[]` kind that ships a loophole module directory
// (§3). Spelled as a STRING rather than read from packdecl because the kind itself is
// landing in a separate change: packdecl.Kind is a string type, so matching on the value
// works identically before and after that constant exists, and this file needs no edit when
// it does.
//
// The value is the contract. If the kind ever lands under a different spelling, the
// enumeration below silently finds nothing — which is why TestDisclosureClassifiesEveryKnownKind
// fails the moment a kind this file does not classify appears in packdecl's closed set.
const packLoopholeKindName = "loophole"

// disclosureClass says what a claim kind does to the user's machine, which is what decides
// both WHETHER it is disclosed at launch and WHEN.
type disclosureClass int

const (
	// disclosureSkip: nothing crosses the boundary. A jail-internal effect (a skills tree, a
	// config surface, launch flags, a home subtree yolo owns) is not something the launch
	// discloses — it is what the jail IS.
	disclosureSkip disclosureClass = iota
	// disclosureRead: the claim reads the user's host, or sets the jail's environment.
	// Printed with the launch banner. For a read, printing after the fact is merely
	// cosmetic (§4.3 G4) — the bytes were already visible to yolo when it decided to mount
	// them, and the user's approval was recorded before this launch existed.
	disclosureRead
	// disclosureExec: the claim runs code ON THE HOST. Printed BEFORE the spawn, because
	// after the spawn the line is not a disclosure, it is a notification that something
	// already happened (§4.3 G4).
	disclosureExec
)

// disclosureClasses is the explicit classification of every kind in packdecl's closed set.
//
// EXHAUSTIVE BY TEST, not by a switch's default: TestDisclosureClassifiesEveryKnownKind
// fails when a kind is added to packdecl and not classified here. That test is the point of
// the whole file — the hardcoded set it replaces was wrong for a year and nothing noticed,
// because "which kinds does the disclosure cover" was a fact only the printer knew.
//
// The `loophole` kind is deliberately absent, and its absence is HANDLED rather than
// tolerated: an unclassified kind defaults to disclosureExec (see disclosureClassOf), so
// even before that kind lands its claims would be announced before anything spawns. The
// test still fails until it is written down here, which is the right pair — correct at
// runtime, loud in review.
var disclosureClasses = map[packdecl.Kind]disclosureClass{
	// Host-crossing. These are exactly the kinds the retired Manifest.HostAccessClaims produced a claim
	// for — the set the user APPROVED at `yolo pack install` — plus env, which is ungated.
	// Matching the approval set is the whole of G4: the launch discloses what was approved,
	// so the two can be compared by a human rather than taken on trust.
	packdecl.KindReadsHost: disclosureRead,
	packdecl.KindMount:     disclosureRead,
	// program: only its `via: installer` instance crosses anything (a fetched script), and
	// that instance is the one the footprint marks ReviewWorthy — so the per-claim filter in
	// disclosedClaims keeps an npm install silent while the curl-to-shell prints.
	packdecl.KindProgram: disclosureRead,
	// briefing: only `after: "host:<path>"` reads the host home, again exactly the
	// ReviewWorthy instance.
	packdecl.KindBriefing: disclosureRead,
	// env is ungated (literal strings, no host reads) and shown anyway: it changes what the
	// agent inside the jail sees, which is the other thing a user checks a launch for.
	packdecl.KindEnv: disclosureRead,

	// NOT host-crossing.
	//
	// state is the one that looks like it should be here and is not. A machine-scope state
	// claim is ReviewWorthy — it leaks across workspaces — but it is a subtree of the JAIL's
	// home that yolo owns, not a path on the host the pack reads or writes. Disclosing it
	// here would put a line on every launch about a directory the jail created for itself,
	// and the review it needs is the one `yolo pack footprint` and `pack install` already
	// give. TestDisclosureCoversEveryReviewWorthyKind names it as the deliberate exclusion,
	// so this reasoning has to be restated (or refuted) by anyone who changes it.
	packdecl.KindState: disclosureSkip,
	// Everything below is jail-internal by construction.
	packdecl.KindRequires: disclosureSkip,
	// blocked-tool writes a refusing shim INSIDE the jail and reads nothing on the host.
	// It changes what the agent may do — which is why it is a pack contribution at all —
	// but the disclosure line answers "what does this launch reach on your machine", and
	// the answer here is nothing. A blocked tool that the agent then discovers is blocked
	// announces itself, by refusing, at the moment it matters.
	packdecl.KindBlockedTool:   disclosureSkip,
	packdecl.KindSkills:        disclosureSkip,
	packdecl.KindFiles:         disclosureSkip,
	packdecl.KindConfig:        disclosureSkip,
	packdecl.KindConfigOverlay: disclosureSkip,
	packdecl.KindLaunch:        disclosureSkip,
	packdecl.KindHook:          disclosureSkip,
	packdecl.KindAutonomy:      disclosureSkip,
	// profile is the same call as autonomy, with one more reason on top: the variant the
	// user can NAME here is the one they SELECTED, and that selection already prints by
	// name in the launch's profile line (noteUseProfiles, DECLARED/RECEIVED, never
	// "honored" — OQ-10). The footprint's claim is per DECLARATION, so classifying it read
	// would print every variant a pack ships whether or not it is active this launch — a
	// line claiming an env change that is not happening, which is the overclaim OQ-10
	// exists to stop, wearing a disclosure badge.
	packdecl.KindProfile: disclosureSkip,
	// provider is jail-internal too, and the tempting reading — "it names a credential, so
	// it must be disclosed" — is backwards: the ONLY credential-shaped thing it carries is
	// the NAME of a variable the user hydrates, which is a pointer, not a read. The facts
	// it does carry are the service's own URLs and model ids, composed into a config table
	// the jail's derives consume; nothing on the host is touched, so there is nothing to
	// announce at the spawn.
	packdecl.KindProvider: disclosureSkip,

	// loophole: the kind whose crossing can be HOST EXECUTION. Classified here so the
	// exhaustiveness test is satisfied, but the read/exec split for a loophole is decided
	// PER CLAIM, not per kind — see disclosureClassOfClaim. One contribution emits several
	// claims (the daemon argv, each intercept, each bind, each device, each socket), and
	// only some of them run code: a `transport: none` loophole declaring only `intercepts`
	// runs nothing on the host and still installs a CA trusted by every TLS client in the
	// jail. Reporting all of them as exec would cry wolf; reporting all as read would put
	// the daemon argv after the spawn.
	packdecl.KindLoophole: disclosureExec,
}

// disclosureClassOf classifies one KIND, defaulting an UNKNOWN kind to disclosureExec.
//
// FAIL-CLOSED IN THE ORDERING DIRECTION. The alternative defaults are both worse: skip
// drops the kind silently (the defect this file exists to fix), and read prints it after
// the spawn (the ordering defect §4.3 G4 measured). Announcing an unclassified kind before
// anything runs costs a line that may be unnecessary; the other two cost a line that comes
// too late or never.
//
// This is the KIND-level answer, which is what the exhaustiveness test checks. The answer a
// printer needs is the CLAIM-level one below.
func disclosureClassOf(k packdecl.Kind) disclosureClass {
	if c, ok := disclosureClasses[k]; ok {
		return c
	}
	return disclosureExec
}

// disclosureClassOfClaim classifies one CLAIM, which is the answer that decides when its line
// prints.
//
// It defers to the claim's own RunsHostCode wherever the kind admits execution at all, because
// that flag is the precise fact and the kind is only an approximation of it. `RunsHostCode` is
// per-instance and deliberately narrow — HOST execution, not "code runs somewhere" (a
// `program via installer` is curl-to-shell IN THE JAIL, a plugin hook runs in the agent's
// sandbox) — so it is exactly the predicate "must this precede the spawn?" needs.
//
// A claim of an exec-capable kind that does NOT run host code degrades to READ rather than
// disappearing. That is the case a kind-level answer gets wrong in both directions: an
// intercept's CA, a `:ro` bind, a passed-through device all cross the boundary and belong in
// the disclosure, but none of them is code about to execute, so putting them in the
// pre-spawn block would dilute the one block whose whole value is that it is short.
func disclosureClassOfClaim(c packload.Claim) disclosureClass {
	class := disclosureClassOf(c.Kind)
	if class == disclosureExec && !c.RunsHostCode {
		return disclosureRead
	}
	return class
}

// disclosureLine is one claim rendered for the launch disclosure.
type disclosureLine struct{ pack, claim string }

// disclosedClaims collects the claims of the given class across the loaded packs.
//
// It reads the FOOTPRINT, and since OQ-TP9 every claim in it happens: the origin gate that
// used to withhold a fetched pack's host reads is gone, so there is nothing left to
// subtract and this report and `yolo pack footprint` describe the same set.
//
// The per-claim filter is `ReviewWorthy || kind == env`, and both halves matter. ReviewWorthy
// is what distinguishes the instances of a kind that actually cross the boundary from the
// ones that do not — `program via npm` and a plain `briefing` are not host access, while
// `program via installer` and `briefing after host:` are, and that distinction lives on the
// claim rather than the kind. env is the exception because every env claim is shown (it is
// never gated and never review-worthy, and it is still what the agent sees).
//
// Classified per CLAIM (disclosureClassOfClaim), not per kind, so a loophole's several claims
// land in the right block each: the daemon argv before the spawn, its CA and binds with the
// rest of the environment.
//
// ONE KIND NEEDS THE GATE APPLIED HERE, and it is the reason claimWillHappen exists: a
// LOOPHOLE claim is deliberately NOT gated on MayAccessHost in the footprint (footprint.go
// says why — `pack footprint` answers what a pack WANTS before you trust it, and hiding a
// fetched pack's daemon argv from that report would hide the line the reader came for). The
// launch answers a different question, so it has to subtract the refused ones itself.
func disclosedClaims(packs []*packload.Pack, class disclosureClass) []disclosureLine {
	var lines []disclosureLine
	for _, p := range packs {
		for _, c := range packload.FootprintOf(p).Claims {
			if disclosureClassOfClaim(c) != class {
				continue
			}
			if !c.ReviewWorthy && c.Kind != packdecl.KindEnv {
				continue
			}
			// The SENTENCE, not the terse token line. packload.Claim.DisclosureSentence
			// is the §6 rendering (what is touched, which direction, whose machine); the
			// kind is kept as a trailing tag because §6 also requires that the
			// machine-comparable identity stay visible beside the prose — it is what a
			// reader matches a banner line to in `yolo pack footprint` and in
			// `yolo config-ref`'s per-kind reference.
			lines = append(lines, disclosureLine{
				p.Name, c.DisclosureSentence() + "  [" + string(c.Kind) + "]"})
		}
	}
	return lines
}

// packHostExecClaims returns the host-EXECUTION claim lines for the loaded packs.
//
// A PACKAGE VAR so the ORDERING test can drive it directly. It was introduced because the
// only kind producing an exec claim (`loophole`) was landing in a concurrent change, and the
// invariant could not otherwise be pinned until after the kind existed — one batch too late,
// which is how the ordering defect survived in the first place. It is KEPT now that the kind
// has landed, because the ordering test wants a claim without also needing a whole staged
// pack tree whose manifest declares a real daemon: the assertion is about WHEN the line
// prints, and building the argv to produce it would test the claim producer instead.
// Production never touches it.
var packHostExecClaims = func(packs []*packload.Pack) []disclosureLine {
	return disclosedClaims(packs, disclosureExec)
}

// notePackHostExec prints, to stderr, what each loaded pack RUNS ON THE HOST this launch.
//
// It must be called before the spawn — see startLoopholesDisclosed, which is the only thing
// that calls it, so the ordering is a property of one function rather than of statement
// order in a 700-line pipeline.
func (o *Options) notePackHostExec(packs []*packload.Pack) {
	lines := packHostExecClaims(packs)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	// Not [dim] like the read disclosure: this is code about to run on the user's machine,
	// and it is the last moment at which reading the line can change what they do.
	out.print("[bold yellow]This launch runs pack code on your machine:[/bold yellow]")
	for _, l := range lines {
		out.print("[yellow]  " + l.pack + ": " + l.claim + "[/yellow]")
	}
}

// startLoopholesDisclosed is the SPAWN BOUNDARY: disclose, report what will not run, then
// start the host services.
//
// THE ORDERING IS THE FIX (§4.3 G4). notePackHostAccess used to be an entire phase BELOW
// startLoopholes — the banner block, just before the container took the terminal — so a
// pack-shipped daemon was already running when its line printed, and on success the spawn
// itself says nothing. For a host READ, after is cosmetic; for a host EXECUTION, after is a
// notification that something already happened.
//
// Being a wrapper rather than two adjacent statements is deliberate: it makes
// startLoopholes reachable through exactly one path that has already disclosed, so the
// invariant cannot be broken by moving a line. TestStartLoopholesHasOneDisclosedCallSite
// pins that there is no second call site. It is also where the inert-on-backend report hangs,
// for the same reason: everything a user must know before host code runs (or before they
// conclude it did) belongs at one boundary.
func (o *Options) startLoopholesDisclosed(cname, rt string, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) []loopholeDaemon {
	o.notePackHostExec(packs)
	// The other half of the same honesty: on a backend that starts no host services at all,
	// say so rather than printing an exec disclosure for a daemon that will never run.
	o.notePackLoopholesInert(rt, packs, cfg)
	return o.startLoopholes(cname, rt, cfg)
}
