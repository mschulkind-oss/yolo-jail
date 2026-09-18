package run

// packloopholes.go is the SPAWN BOUNDARY for a pack-shipped loophole: what the user is told
// before any host code runs, and what they are told when the backend means none will
// (docs/design/loophole-packaging.md §4.3 G4, §8 item 2).
//
// Two things live here, in that order:
//
//  1. The DISCLOSURE CLASSIFICATION — which claim kinds cross the boundary, whether a
//     kind's crossing is a host READ or host EXECUTION, and (since OQ-TP10) which claims
//     execute inside the JAIL instead of crossing at all. Data, not a hardcoded switch inside
//     the printer, because the printer's hardcoded set is exactly the defect §3.3 measured:
//     "notePackHostAccess switches on KindMount, KindReadsHost, KindEnv and drops every
//     other claim kind" — with no test to catch the next kind that gets dropped.
//  2. startLoopholesDisclosed — the ONE call site of startLoopholes, which prints before it
//     spawns. §4.3 G4: the spawn preceded the notice by an entire phase, and the spawn is
//     silent on success, so "a fetched pack's daemon could start on every launch for months
//     with the only host-side record being a lockfile the user has to go read."
//
// THERE IS ONE SPAWN BOUNDARY AGAIN, as of 2026-09-17, and the history is worth keeping
// because it is why the guard below exists. There were two: the SUBSET path — a backend that
// started the OpenAI credential service and nothing else — had its own wrapper in
// openaiauthbackend.go, because macos-user returned from Run above this one and reached the
// daemon directly. That arm now routes through this wrapper like every other backend, so the
// subset path and its wrapper are deleted (that file records the inversion). The guard that
// would catch a third path appearing is TestEverySpawnEntryDisclosesHostExecFirst, which asks
// the AST what encloses each spawn call rather than grepping for one spelling of one of them —
// and which refuses to run against a name the package no longer has, so it cannot quietly
// guard one fewer subject.
//
// The INERT REPORT the wrapper also calls is its own file (loopholeinert.go): it answers a
// different question ("will this even run here?") on a different axis pair, and it is the one
// half that has nothing to do with ordering.

import (
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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

// pluginClaimTargetPrefix is the Target a WRAPPED PLUGIN's claim carries — "plugin:<name>",
// built in packload/footprint.go's Plugins loop. It is what distinguishes that claim from an
// ordinary `skills` one, which the two share a kind with.
//
// Spelled as a STRING here for packLoopholeKindName's reason one file over: the value is the
// contract between the producer and this classifier, and a prefix renamed on one side alone
// would leave this class matching nothing — silently, which is the failure this whole file
// exists to end. The guard is that the tests build the claim with the REAL producer
// (packload.FootprintOf over a loaded pack) rather than by hand, so a rename fails there.
//
// It cannot collide with an ordinary `skills` claim, whose target is a manifest path:
// footprint.go picks this shape because a manifest path may not contain a colon.
const pluginClaimTargetPrefix = "plugin:"

// disclosureClass says what a claim does — to the user's machine for the three host classes,
// and inside the jail for the fourth — which is what decides WHETHER it is disclosed at
// launch, WHEN, and in which block.
type disclosureClass int

const (
	// disclosureSkip: nothing crosses the boundary and nothing runs. A jail-internal EFFECT
	// (a skills tree, a config surface, launch flags, a home subtree yolo owns) is not
	// something the launch discloses — it is what the jail IS.
	//
	// THAT DEFINITION IS ABOUT CONTENT, and reading it as "jail-internal, therefore silent"
	// is what left a hole here until OQ-TP10: code an agent runs on its own lifecycle is
	// jail-internal too, and "what the jail is" was never an argument for saying nothing
	// about it. It has its own class below.
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
	// disclosureJailExec: the claim delivers CODE THAT RUNS INSIDE THE JAIL — today a wrapped
	// plugin's `hooks`, `mcpServers` or `lspServers`, which the agent starts on its own
	// lifecycle.
	//
	// THE FOURTH CLASS, and the axis is the whole point. The three above are axes of HOST
	// crossing — nothing, a read, an execution — and this one crosses nothing on the host at
	// all. trust-paths.md OQ-TP10 ruled (a), "plugin claims get their own disclosure class",
	// and deliberately left WHICH class open: "a fourth class, or a changed definition of the
	// first — a decision, not a lookup", with the warning that letting it default would reach
	// disclosureExec and "announce 'runs code on the host' about code that does not". A fourth
	// class is the decision, because the first three answer a question about the user's
	// MACHINE and this claim's answer to that question is honestly "nothing".
	//
	// Rendered by packJailCodeLines, NOT by disclosedClaims. report-tiers.md decided the
	// rendering before this was built — P5 (named, not itemized), P6 (count what the reader
	// cares about: hooks and servers) and P1 (a property of the pack set is stated once per
	// set) make it ONE COUNTED LINE PER PACK — so handing this class to the per-claim renderer
	// would print the line-per-hook shape the ruling costed out and rejected.
	//
	// ⚠ A LOOPHOLE'S `jail_daemon` IS NOT CARRIED YET, and the ruling says this class covers
	// it: trust-paths.md §3.2 measured that a loophole declaring only a `jail_daemon` produces
	// ZERO claims, so a supervised UID-0 process in the jail is named nowhere, and a class
	// that renders COUNTS can name a zero-claim crossing where one rendering claims could not.
	// What it needs first is the per-launch answer to "will it actually run" — a declared
	// daemon whose loophole is disabled, or whose backend is inert, starts nothing — and that
	// answer belongs to notePackLoopholesInert's producer (loopholeinert.go), not to a second
	// selection written here. Announcing a daemon that never starts is the overclaim the
	// `autonomy` and `profile` rows below refuse to make.
	//
	// ONE BACKEND HAS THAT ANSWER NOW, which is where a reader should start rather than
	// re-deriving it: the composed payload is hoisted above the backend dispatch
	// (packservices.go's jailDaemonsFor), and macos-user reads it to decline every entry by
	// name (jaildaemondecline.go). What is still missing for this class is the answer for a
	// backend that DOES run them, which is the per-launch "will it actually run" the paragraph
	// above is waiting on.
	disclosureJailExec
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
	packdecl.KindBlockedTool: disclosureSkip,
	// skills: a prose tree an agent reads, which is squarely what disclosureSkip is for —
	// AT THE KIND LEVEL, which is the only level this table speaks at.
	//
	// A WRAPPED PLUGIN'S CLAIM IS REPORTED UNDER THIS KIND TOO (packload/footprint.go, the
	// Plugins loop), because what a plugin declares lives in its own manifest rather than in
	// pack.json — so `hooks`, `mcpServers` and `lspServers`, which the agent runs on its
	// lifecycle, used to inherit this row and reach no banner at all. That was the known hole
	// TP9's deletion of the approval gate made load-bearing: the banner it KEPT as the
	// compensating disclosure was silent about exactly the contribution that runs code.
	//
	// It is closed PER CLAIM, not by moving this row: pluginCodeClaim below routes those
	// claims to disclosureJailExec (trust-paths.md OQ-TP10, ruled (a) on 2026-09-14). The row
	// stays disclosureSkip because the ruling rejected reclassifying it — option (c), which
	// "would announce every skill file and bury the hooks in the noise that made
	// disclosureSkip right here" — and because a plugin shipping only skills, commands or
	// output styles is prose, and belongs here with them.
	packdecl.KindSkills:        disclosureSkip,
	packdecl.KindFiles:         disclosureSkip,
	packdecl.KindConfig:        disclosureSkip,
	packdecl.KindConfigOverlay: disclosureSkip,
	packdecl.KindHook:          disclosureSkip,
	// autonomy declares LAUNCH FLAGS, and the tempting reading — "a permission bypass must
	// be disclosed" — picks the wrong instrument. This classifier prints per DECLARATION, so
	// a read row here would announce `--yolo` on every launch of a jail that selected the
	// copilot pack, including `yolo -- bash` and `yolo -- claude`, where no copilot process
	// exists. The flag is disclosed where it actually happens instead: the argv rewrite is
	// printed by run.injectLaunchFlagsDisclosed, with both command lines, and only when
	// something was really added (launchflagdisclosure.go). Same OQ-10 reasoning as
	// `profile` below — a disclosure that overclaims is the silent-skip failure wearing a
	// badge — reached from the other side.
	packdecl.KindAutonomy: disclosureSkip,
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

	// service is the anti-loophole (wire-bridge.md §2.1): it binds the JAIL's own
	// loopback, reads no host state, and crosses nothing — the same call as provider,
	// its constant companion in a bridged launch. THE ONE TRIPWIRE: the kind also
	// DECLARES a host_daemon half, and this build deliberately does not execute it
	// (packservices.go carries the why). The day that half gains a consumer, this row
	// MUST move to disclosureExec — a host daemon argv is exactly the pre-spawn block's
	// subject — and the exhaustiveness test here is what makes the revisit loud instead
	// of forgotten.
	packdecl.KindService: disclosureSkip,

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
//
// THE JAIL AXIS IS DECIDED FIRST, because it is not a point on the host one. A plugin claim's
// KIND is `skills` (disclosureSkip) and its RunsHostCode is false, so both host questions
// below answer "nothing crosses your machine" — correctly, and that is precisely how code
// that runs on the agent's lifecycle reached no banner for months.
func disclosureClassOfClaim(c packload.Claim) disclosureClass {
	if pluginCodeClaim(c) {
		return disclosureJailExec
	}
	class := disclosureClassOf(c.Kind)
	if class == disclosureExec && !c.RunsHostCode {
		return disclosureRead
	}
	return class
}

// pluginCodeClaim reports whether a claim is a wrapped plugin's AND declares a component that
// runs code.
//
// ReviewWorthy is the discriminator because on THIS claim it is the code question: the
// producer sets it from pluginpack.Plugin.RunsCode (footprint.go's Plugins loop), which is
// true exactly when the manifest declares `hooks`, `mcpServers` or `lspServers`. A plugin
// shipping only skills, commands or output styles is prose, keeps `skills`'s disclosureSkip,
// and stays off the launch — announcing it is option (c), which OQ-TP10 rejected for burying
// the hooks in the noise.
func pluginCodeClaim(c packload.Claim) bool {
	return c.Kind == packdecl.KindSkills &&
		strings.HasPrefix(c.Target, pluginClaimTargetPrefix) && c.ReviewWorthy
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
// ONE CLASS IS DELIBERATELY NOT RENDERED HERE. disclosureJailExec is a COUNTED line per pack
// (packJailCodeLines) rather than a line per claim, so asking this function for it would
// produce the per-hook shape OQ-TP10's rendering rejected. It answers for the two host blocks.
//
// THERE IS NOTHING LEFT TO SUBTRACT HERE, and this comment said otherwise until 2026-09-09.
// One kind used to need the gate applied at this point, via a predicate called
// claimWillHappen: a LOOPHOLE claim was deliberately NOT gated on the declaring pack's
// MayAccessHost in the footprint (footprint.go still says why — `pack footprint` answers what
// a pack WANTS before you trust it, and hiding a fetched pack's daemon argv from that report
// would hide the line the reader came for), so the launch, answering a different question,
// subtracted the refused ones itself.
//
// OQ-TP9 deleted MayAccessHost and the whole fetched-pack origin gate on 2026-09-04, and
// claimWillHappen went with it — it survives only in the name of the test that pins its
// absence (packhostdisclosure_test.go). Nothing is refused, so the launch disclosure and the
// footprint now answer the same question about the same set, and the footprint's asymmetry is
// vestigial rather than load-bearing.
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
			sentence := c.DisclosureSentence()
			// Footprints stay machine-independent and therefore retain manifest
			// tokens. A launch disclosure describes the argv about to execute on
			// this machine, so its name-derived state path must match the resolved
			// daemon and doctor argv rather than printing the literal token.
			if c.Kind == packdecl.KindLoophole && c.RunsHostCode {
				sentence = strings.ReplaceAll(sentence, loopholedecl.TokenState,
					loopholes.StateDirFor(c.Target))
			}
			lines = append(lines, disclosureLine{
				p.Name, sentence + "  [" + string(c.Kind) + "]"})
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
// It must be called before the spawn, and every caller is a WRAPPER around one, so the
// ordering is a property of a two-line function rather than of statement order in a 700-line
// pipeline: startLoopholesDisclosed below for the full set, startOpenAIAuthDisclosed for the
// subset a backend with no container starts.
//
// "the only thing that calls it" is what this said until 2026-09-16, and the sentence was true
// while being the defect: the subset path existed, spawned, and was not a caller — so the
// macos-user arm ran a pack's host daemon in silence. A single call site is only an invariant
// while a single path reaches a spawn.
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

// packJailCodeLines renders ONE LINE PER PACK saying how much pack-contributed code will run
// INSIDE the jail, counted by component kind.
//
// THE SHAPE IS NOT DECIDED HERE. report-tiers.md decided it before this was built, which is
// why OQ-TP10 could rule (a) without costing the launch a line per hook: P5 (the invariant is
// *named*, not itemized — "appearing once is appearing"), P6 ("counts count what the reader
// cares about", whose own examples include servers and skills) and P1 ("a property of the
// pack set is stated once per set") together make it one counted line per pack. The
// itemization is a command away — `yolo pack footprint` prints every plugin claim, naming the
// plugin and the components it declares — and the header says so, rather than each line
// repeating it (P1 again).
//
// THE FOOTPRINT DECIDES THE SET, the plugin tree only counts it. The loop takes the packs and
// names the claims classified disclosureJailExec and then counts the components of exactly
// those plugins, so this disclosure and `yolo pack footprint` cannot come to describe
// different sets — the property disclosedClaims states for the host half.
//
// WHAT THE NUMBER BESIDE A COMPONENT COUNTS is how many of the pack's plugins DECLARE it, not
// how many hook entries there are. yolo deliberately does not decode someone else's manifest
// schema (pluginpack.Manifest keeps every component as a RawMessage, so that their next
// release is not yolo's parse error), so the entry count is a fact this build does not have —
// and a count that looked like one would be worse than the honest one.
func packJailCodeLines(packs []*packload.Pack) []disclosureLine {
	var lines []disclosureLine
	for _, p := range packs {
		disclosed := map[string]bool{}
		for _, c := range packload.FootprintOf(p).Claims {
			if disclosureClassOfClaim(c) != disclosureJailExec {
				continue
			}
			disclosed[strings.TrimPrefix(c.Target, pluginClaimTargetPrefix)] = true
		}
		if len(disclosed) == 0 {
			continue
		}
		var order []string
		counts := map[string]int{}
		for _, pl := range p.Plugins() {
			if !disclosed[pl.Name()] {
				continue
			}
			for _, comp := range pl.Components() {
				if !comp.RunsCode {
					continue
				}
				if counts[comp.Name] == 0 {
					order = append(order, comp.Name)
				}
				counts[comp.Name]++
			}
		}
		lines = append(lines, disclosureLine{p.Name, jailCodeSummary(len(disclosed), order, counts)})
	}
	return lines
}

// jailCodeSummary is one pack's counted line. The component order is the manifest's own
// (pluginpack.Components returns a fixed order), so two launches of one pack print the same
// line and a reader can compare them.
func jailCodeSummary(plugins int, order []string, counts map[string]int) string {
	s := strconv.Itoa(plugins) + " wrapped plugin runs code in the jail"
	if plugins != 1 {
		s = strconv.Itoa(plugins) + " wrapped plugins run code in the jail"
	}
	for i, name := range order {
		sep := ", "
		if i == 0 {
			sep = " — "
		}
		s += sep + name + " (" + strconv.Itoa(counts[name]) + ")"
	}
	return s
}

// notePackJailCode prints, to stderr, the code each loaded pack delivers that runs INSIDE the
// jail (disclosureJailExec).
//
// NOT [bold yellow]: that register is notePackHostExec's, and it means "this is the last
// moment at which reading the line can change what runs on YOUR machine". Not [dim] either —
// that is notePackHostAccess's register for environment facts, and this is code. The register
// is the severity, so a third proposition gets the middle one.
//
// THE HEADER CARRIES THE POINTER, once, because the itemization is what the line leaves out
// and a reader who wants it should not have to know where claims live (P1, P2). It is the
// footprint report, and the spelling is deliberately left bare: `yolo pack footprint <name>`
// resolves an EMBEDDED pack, while a pack the user fetched or wrote is footprinted by its
// local path — a distinction that belongs in that command's help, not in a launch line.
//
// IT CANNOT BE GATED, and no flag may be added that could gate it (report-tiers.md P4, the
// launch stream's "a launch has no quiet mode"; AGENTS.md states the same invariant). The
// compression above IS the density control: one line per pack, not one per hook. A
// suppressible disclosure would delete exactly what OQ-TP9 kept when it deleted the approval
// gate — and this line is the half of that banner OQ-TP9's own sentence was false about.
//
// EVERY BACKEND PRINTS IT, and that became true the same day this was written. The caveat
// here said macos-user would not: that arm returned from Run above this wrapper and disclosed
// through a subset path scoped to what it SPAWNED on the host — the wrong set for this
// question, since every selected pack's plugins reach that backend's agent too. The arm now
// routes through this wrapper with the whole pack set, so the gap closed by removal rather
// than by the second call site the caveat proposed. Nothing here is backend-conditional, and
// nothing about it should become so: a plugin's hooks run inside whatever the jail is.
func (o *Options) notePackJailCode(packs []*packload.Pack) {
	lines := packJailCodeLines(packs)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[yellow]This launch delivers pack code that runs inside the jail " +
		"(`yolo pack footprint` names each one):[/yellow]")
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
// and since OQ-TP10 where the JAIL-execution disclosure hangs, for the same reason:
// everything a user must know before any of this launch's code runs (or before they conclude
// it did) belongs at one boundary.
func (o *Options) startLoopholesDisclosed(cname, rt string, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) []loopholeDaemon {
	o.notePackHostExec(packs)
	// The JAIL half of the same question — pack code that runs, on the other side of the
	// boundary — and it prints here because this wrapper is the last host-side moment before
	// the container takes the terminal. A hook fires on the agent's lifecycle, which is after
	// every line printed here, so a disclosure at this point still precedes what it names.
	o.notePackJailCode(packs)
	// The other half of the same honesty: on a backend whose jail cannot reach a host
	// service, say so rather than leaving an exec disclosure to imply that starting the
	// daemon was the whole job.
	//
	// ⚠ ONE PACK WAS EXEMPT FROM THIS REPORT UNTIL 2026-09-18, and the exemption is what made
	// Apple Container silent about the credential service. `rt == "container"` handed this the
	// set MINUS the openai-auth pack, on the reasoning that its host daemon is the one
	// `startLoopholes` really starts there, so reporting it inert would contradict the exec
	// line above. Measured, that reasoning does not hold and the silence it bought was the
	// whole defect:
	//
	//   - the two lines were never complements on this backend anyway. The exec disclosure is
	//     CLAIM-shaped — it names what each pack DECLARES it runs on your machine — so claude's
	//     broker is announced there and reported inert in the same AC launch already, and has
	//     been since both reports existed. Exempting one pack preserved nothing.
	//   - "its daemon starts" and "the jail cannot reach it" are both true here, and the second
	//     is the one the user needs: `backendInertReason` states exactly that (nothing crosses
	//     container→host on `container` 1.1.0), so the line is true of the one loophole whose
	//     daemon runs as much as of the ones whose daemons do not.
	//   - nothing else told them. The endpoint variable and the services-dir mount are emitted
	//     for this backend, and its host-loopback disposition is `unknown`, which the fatal
	//     reachability witness never escalates — so `codex` printed `OpenAI login is required.`
	//     with nothing naming Apple Container as the cause (setup-support-gaps.md G6).
	//
	// The reason is VERSION-GATED where it is written, not here: it names the release it was
	// measured on, and integration/applecontainer_test.go's TestAppleContainerReachesHostLoopback
	// is what expires it. No backend branch is left at this call site, which is the other half
	// of the fix — a report that treats every pack alike cannot acquire a second exemption.
	o.notePackLoopholesInert(rt, packs, cfg)
	return o.startLoopholes(cname, rt, cfg)
}
