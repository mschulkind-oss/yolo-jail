package cli

// hostapplysurvey.go accumulates THE CHANGE PREDICATE across one whole host apply
// (docs/reference/host-apply-staleness.md §3.4, §10 step 1) AND the counts the report's
// verdict block is evidenced by (docs/design/report-tiers.md §4.3, §9 step 1).
//
// Each of the four written kinds computes the predicate for its own destinations — see
// entrypoint.HostRenderResult.WouldChange and hostskills.Result.WouldChange — and each already
// reports per-destination lines. What was missing is the ROLL-UP: "would an --assert change
// anything in this home, and what?" A dry run needs it to stop printing N identical
// `would render` lines over a home that is already correct, and §4.3's launch gate needs
// exactly the same answer to decide between exec'ing silently and stopping to ask.
//
// ONE collector, both consumers, and that is deliberate. The launch gate does not re-derive
// the answer from its own observe pass: it runs the SAME applyHost in observe posture with the
// output discarded and reads the survey it filled in. A second traversal of the four kinds
// would be a second thing to drift out of step with the apply it is supposed to describe.
//
// ONE WRITER for the counts, for the same reason (report-tiers.md §5, *One writer*): the
// survey is the only thing that knows them and the printer reads it, so no emitter computes
// its own total. The counts are what they are because the DESTINATION count answers a
// question nobody asked — 76 of them in the measured home, 70 being fourteen skills counted
// once per agent directory (§3.4). Everything below counts files, keys, entries, skills and
// binaries instead.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// reportTier is docs/design/report-tiers.md §4.1's REPORT TIER: the class of fact a line
// states, assigned WHERE THE FACT IS PRODUCED and read by the printer to decide whether a
// line prints, how many times, and under which flag.
//
// It is deliberately not a log level: a level is what an emitter picks in the moment ("this
// feels like a warning"), which is exactly how today's output came to be, while a tier is a
// property of the FACT — two emitters stating the same fact assign the same tier. It is not
// severity either; a kind refusal is a refusal and is a tier-1 notch fact, while an MCP entry
// loss is a warning and is tier 3.
//
// Tiers 1 (notch facts) and 4 (launch disclosures) have no per-destination representative and
// so have no constant here: a notch fact is true of the NOTCH rather than of a destination
// (§9 step 3 collapses those lines), and a disclosure belongs to a launch (§9 step 7).
type reportTier int

const (
	// tierRun is §4.1's tier 2: varies with the home, needs no action — a destination that
	// would render, one already in sync, a declared dependency that is present.
	tierRun reportTier = 2
	// tierLoss is §4.1's tier 3, which has TWO members and one treatment. A loss takes
	// something of the user's (a value replaced, a named entry dropped, a skill moved into
	// the local pack); a blocker stands between this home and a completed apply (a missing
	// declared dependency, a refusal, a pack that failed to render). They share a tier
	// because a tier is a RENDERING decision and both want the same rendering: always
	// itemized, the remedy stated once, and always represented in the verdict line.
	tierLoss reportTier = 3
)

// hostChange is one destination an --assert would alter.
type hostChange struct {
	// Kind is the written kind that owns the destination, as the user sees it in the report
	// (`config`, `skills`, `briefing`, `files`, `host_wrappers`).
	Kind string
	// Surface is the reported identity of the thing — "claude/settings", a skill name, a
	// pack's briefing.
	Surface string
	// Path is the absolute destination in the home.
	Path string
	// Tier is the class of fact this destination states (§4.1). tierLoss means THIS RUN
	// would take something of the user's here; everything else is tierRun.
	Tier reportTier
}

// skillFate is what an apply would do to one SKILL — by NAME, across every destination that
// skill reaches. The name is the unit because the destination is not: fourteen skills in five
// agent directories are seventy destinations and fourteen facts, and the seventy is most of
// why the measured count line was useless (§3.3, §3.4).
//
// Ordered by precedence, so a skill that is adopted in one directory and merely composed in
// another reports the adoption: the bigger claim on the user's content wins.
type skillFate int

const (
	// skillComposed — yolo's own content lands in a destination. A run fact.
	skillComposed skillFate = iota
	// skillRetired — composed output is archived out of the home. Nothing of the user's
	// moves (their own skills live in the local pack), so this is not an adoption; it is
	// still content leaving a directory they look at.
	skillRetired
	// skillAdopted — a skill of the USER's moves or unions into their local pack and is
	// composed back. §4.4's loss class with a remedy, and the one the verdict names.
	skillAdopted
)

// hostApplySurvey is the accumulated predicate and counts for one apply.
//
// Every method is nil-safe, so a caller that does not want the roll-up passes nil rather than
// threading an unused value through five signatures.
type hostApplySurvey struct {
	// InSync counts the destinations an --assert would leave exactly as they are.
	//
	// "As they are" is the literal claim, and it deliberately includes a surface the render
	// REFUSES to touch: the report gives every refusal its own loud line, and folding them into
	// the changed set instead would make a launch gate stop on a condition applying cannot fix
	// — a prompt whose remedy does not exist (§4.4's cannot-determine class).
	InSync int
	// Changed lists the destinations the render would alter, in report order.
	Changed []hostChange

	// replacedKeys are the dotted managed keys that would overwrite a value of the user's,
	// and replacedFiles the destinations they sit in — §4.3's "values of yours replaced,
	// keys, with the file count". Two sets rather than one count because the operator's
	// first question is how many of THEIR values move, and the file count is what makes
	// that number locatable.
	replacedKeys  map[string]bool
	replacedFiles map[string]bool
	// droppedEntries are the NAMES of named-table entries that would be dropped or replaced
	// (an MCP server), deduplicated across agents; droppedFrom are the surfaces they would
	// go from. Nine raw losses in the measured home, three servers, three surfaces.
	droppedEntries map[string]bool
	droppedFrom    map[string]bool
	// skills is every skill this run touches, keyed by NAME (see skillFate).
	skills map[string]skillFate
	// commentSurfaces are the surfaces whose comments a canonical re-emit would drop — the
	// one §4.4 loss class with no remedy possible, and the one the verdict used to be silent
	// about while configResultTier was already counting it as a loss.
	commentSurfaces map[string]bool
	// adoptedSurfaces are the surfaces THIS RUN ADOPTED — composed wholesale out of what the
	// file already held, with the pre-existing file copied into the one-time archive
	// (entrypoint.HostRenderResult.Archived, OQ-CO7). Keyed by surface for the reason every
	// set above is: a surface adopts once per home, so the surface is the fact and the count
	// is one per file rather than one per write the adoption performed.
	adoptedSurfaces map[string]bool
	// deps is every declared dependency's FINDING keyed by BINARY — the probed state and,
	// for a missing one, the remedy the tier-3 group states once (§4.4 groups a blocker by
	// its remedy key, and for a dependency that key is the binary, across packs). depsNoBin
	// counts the contributions that named no binary at all: those cannot be deduplicated by
	// one, and they are not missing either — they were never probed (§4.9 point 6).
	deps      map[string]hostDepFinding
	depsNoBin int
	// installedDeps are the binaries THIS RUN installed, at the user's y, before anything
	// was rendered (applyhostdepgate.go). The verdict leads with them — "Installed `rg`;
	// applied: …" — because an apply that changed the host's toolchain did something the
	// counts below cannot express (§4.3).
	installedDeps []string
	firstApply    bool
	failedPacks   []string

	// home is the home THIS apply rendered into, and zeroPacks whether it took the
	// no-packs-configured branch. Both are RECORDED rather than re-derived, because both
	// are known exactly once — at the top of applyHostSurveyed, and in the branch itself —
	// and every consumer of the roll-up needs them: the verdict's footer names the home, and
	// "no packs are configured" is a different result from "every configured pack changed
	// nothing" with a different next action, which an empty Changed set cannot distinguish.
	home      string
	zeroPacks bool
	// notch is the tier-1 half of the report: the kinds this notch does nothing with and
	// whether any pack declares `autonomy`. Recorded for the machine document (§4.8), which
	// names the kinds and carries none of their prose — rationale is not data.
	notch notchFacts
}

// noteHome records the home this apply is rendering into. Called once, where it is resolved.
func (s *hostApplySurvey) noteHome(home string) {
	if s != nil {
		s.home = home
	}
}

// noteZeroPacks marks the no-packs-configured branch.
func (s *hostApplySurvey) noteZeroPacks() {
	if s != nil {
		s.zeroPacks = true
	}
}

// noteNotch records the tier-1 facts (hostapplynotch.go) so the machine document can state
// them without taking a second census.
func (s *hostApplySurvey) noteNotch(f notchFacts) {
	if s != nil {
		s.notch = f
	}
}

// Home is the home this apply rendered into, "" for a survey nobody filled.
func (s *hostApplySurvey) Home() string {
	if s == nil {
		return ""
	}
	return s.home
}

// ZeroPacks reports whether this run took the no-packs-configured branch.
func (s *hostApplySurvey) ZeroPacks() bool { return s != nil && s.zeroPacks }

// InapplicableKinds names the contribution kinds this notch does nothing with, sorted as the
// census collected them. The NAMES only: their reasons live in the manual (§4.6), and no
// terminal view or document prints them.
func (s *hostApplySurvey) InapplicableKinds() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.notch.Inapplicable))
	for _, k := range s.notch.Inapplicable {
		out = append(out, string(k))
	}
	return out
}

// AutonomyPosture is the posture this notch renders, or "" when no pack declares the kind.
// One value, because the posture is the NOTCH's — render.Host(...).Profile().AgentAutonomy
// resolves to guarded here and cannot differ between packs in one run, which is why
// printNotchFacts states it as a constant too.
func (s *hostApplySurvey) AutonomyPosture() string {
	if s == nil || !s.notch.Autonomy {
		return ""
	}
	return "guarded"
}

// note records one result's verdict at the given tier. A result with no PATH is not a
// destination — an ownerless config patch, a pack-level refusal — and is counted in neither
// bucket.
//
// The TIER is the caller's to decide and is passed rather than derived, which is the whole
// point of §4.1: the class of a fact is known where the fact is produced (the result struct
// carrying the losses, the typed skills Action) and is unrecoverable from the three strings
// that arrive here.
func (s *hostApplySurvey) note(tier reportTier, kind, surface, path string, wouldChange bool) {
	if s == nil || path == "" {
		return
	}
	if !wouldChange {
		s.InSync++
		return
	}
	s.Changed = append(s.Changed, hostChange{Kind: kind, Surface: surface, Path: path, Tier: tier})
}

// noteConfig records ONE config surface's result: its change predicate, its tier, and every
// loss the verdict counts. One call rather than five, because the losses and the predicate are
// facts about the same render and separating them at the call site is how one of them comes to
// be forgotten at a new one.
func (s *hostApplySurvey) noteConfig(r entrypoint.HostRenderResult) {
	if s == nil {
		return
	}
	s.note(configResultTier(r), "config", r.Surface, r.Path, r.WouldChange)
	if r.FirstApply {
		s.firstApply = true
	}
	for _, k := range r.Overwrites {
		s.mark(&s.replacedKeys, k)
		s.mark(&s.replacedFiles, r.Path)
	}
	for _, e := range r.EntryLosses {
		s.mark(&s.droppedEntries, entryLossName(e))
		s.mark(&s.droppedFrom, r.Surface)
	}
	if len(r.Formatting) > 0 {
		// The SURFACE is the unit §4.4 groups comment loss by, and the strings are not
		// recorded: a comment is the user's prose, and §4.4's first forbidden thing is
		// printing the user's own content back at them (a terminal transcript gets pasted
		// into bug reports). The count and the file are the whole fact.
		s.mark(&s.commentSurfaces, r.Surface)
	}
	if r.Archived != "" {
		// THE ADOPTION ITSELF, which is none of the three losses above and is the one fact
		// in this method that can be the ONLY thing a run did. OQ-CO7's zero-bytes
		// criterion says the `assert` -> `own` switch reproduces the file exactly, so the
		// destination reports WouldChange=false and files as in-sync — and with nothing
		// recorded here the run closed on "Nothing to apply — this home is up to date",
		// directly under the unsuppressible line naming the copy it had just taken
		// (OQ-CO7 D3, measured on the built binary 2026-09-12).
		s.mark(&s.adoptedSurfaces, r.Surface)
	}
}

// configResultTier is §4.1 applied to one config surface: tier 3 when this render would take
// something of the user's here, tier 2 otherwise.
//
// `Formatting` counts, and it is the one entry that is not obvious. Nothing the user
// CONFIGURED changes when a comment is dropped — which is why it is not an overwrite — but a
// comment they wrote does not come back, so the destination is one where something of theirs
// is lost, which is exactly what the tier decides.
//
// `Archived` counts for the same kind of reason one step further out, and it is the entry
// that is NOT derivable from the other three. A non-empty Archived means this render ADOPTED
// the file — composed the whole thing out of what it already held, for the first time
// (OQ-CO7) — and that is a one-way door whether or not the composition happened to reproduce
// the bytes. §11's criterion says the `assert` -> `own` switch usually DOES reproduce them, so
// without this the canonical adoption reports WouldChange=false, tier 2, and its line drops
// behind --verbose while the archive disclosure printed under it stays (OQ-RO3 forbids hiding
// that one). The result was an indented "archived your file as yolo found it: …" attaching
// itself to whatever unrelated line came before it, under a verdict reading "Nothing to apply
// — this home is up to date". The tier is what puts the surface's own line back above it.
//
// It changes no COUNT: hostApplySurvey.note reads the predicate first and files a
// !wouldChange destination as in-sync without consulting the tier at all. So the tier fixed
// the HEADING and left the VERDICT saying the opposite of the line under it — OQ-CO7's D3,
// which is `adoptedSurfaces` above and the `Adoptions()` half of hostApplyOutcome's
// nothing-to-do case. A tier decides how a destination's own line renders; only a count
// reaches the sentence the run ends on.
func configResultTier(r entrypoint.HostRenderResult) reportTier {
	if len(r.Overwrites) > 0 || len(r.EntryLosses) > 0 || len(r.Formatting) > 0 ||
		r.Archived != "" {
		return tierLoss
	}
	return tierRun
}

// entryLossName reduces one HostRenderResult.EntryLosses string to the NAME of the entry it is
// about, which is the unit the count has to be keyed on.
//
// The strings are built by entrypoint's tableLosses as "<table>.<entry> (<what happened>)",
// and the TABLE is the half that differs between agents for one server: claude spells the
// table `mcpServers`, codex `mcp_servers`, opencode `mcp`. Counting the raw strings therefore
// reports three servers the user added by hand as nine losses — §3.3's worst repetition axis,
// and the only reason this reduction exists.
//
// The format is pinned by going through the real multi-agent render rather than by a literal
// (hostapplysurvey_test.go), so a change to tableLosses' wording breaks that test instead of
// silently inflating this count back to nine.
func entryLossName(loss string) string {
	name := loss
	if i := strings.Index(name, " ("); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// noteSkill records what would become of one skill BY NAME. The bigger fate wins, so a skill
// adopted at one destination and composed at four more is one adoption, not five facts.
func (s *hostApplySurvey) noteSkill(name string, fate skillFate) {
	if s == nil || name == "" {
		return
	}
	if s.skills == nil {
		s.skills = map[string]skillFate{}
	}
	if cur, seen := s.skills[name]; !seen || fate > cur {
		s.skills[name] = fate
	}
}

// noteDep records one declared dependency's finding, keyed by binary so two packs declaring
// `rg` are one dependency on one host with one remedy. A contribution naming no binary is
// counted separately: it has no key to deduplicate by, and it is NOT missing — nothing probed
// it.
//
// The WORSE state wins, and at equal states the finding that carries a remedy does: two packs
// can declare one binary with different install hints, and a group that states "no remedy"
// while a selected pack declares one would send the reader to fix a manifest that is fine.
func (s *hostApplySurvey) noteDep(bin string, f hostDepFinding) {
	if s == nil {
		return
	}
	if bin == "" {
		s.depsNoBin++
		return
	}
	if s.deps == nil {
		s.deps = map[string]hostDepFinding{}
	}
	cur, seen := s.deps[bin]
	s.deps[bin] = mergeDepFinding(cur, f, seen)
}

// mergeDepFinding is THE rule for two declarations of one binary, and it is a function rather
// than four lines inside noteDep because the gate's offer now turns on the answer: whichever
// finding survives here is the one the prompt, the group and the verdict all read.
//
// The WORSE state wins — two packs disagreeing about `rg` is one missing dependency on one
// host. At equal states two tie-breaks, in order:
//
//   - an INSTALLABLE finding wins (§4.9 / OQ-RO7). One pack declaring `rg` as a `program` and
//     another as a `requires` means yolo does have an install to offer, and dropping to the
//     `requires` would refuse the run over a remedy it was holding.
//   - otherwise a finding carrying a REMEDY wins: a group stating "no remedy" while a selected
//     pack declares one sends the reader to fix a manifest that is fine.
func mergeDepFinding(cur, f hostDepFinding, seen bool) hostDepFinding {
	switch {
	case !seen, f.State > cur.State:
		return f
	case f.State != cur.State:
		return cur
	case f.installable() && !cur.installable():
		return f
	case cur.Remedy == "" && f.Remedy != "":
		return f
	}
	return cur
}

// noteInstalled records a binary the dependency gate installed this run, and updates the
// probe's answer to match: a dep the run just installed is PRESENT, so every count, group and
// line downstream states what is true after the install rather than what was true before it.
func (s *hostApplySurvey) noteInstalled(bin string) {
	if s == nil || bin == "" {
		return
	}
	s.installedDeps = append(s.installedDeps, bin)
	if f, ok := s.deps[bin]; ok {
		f.State, f.Remedy, f.Alt, f.NoRemedy = depPresent, "", "", ""
		s.deps[bin] = f
	}
}

// InstalledDeps names the binaries this run installed, in install order.
func (s *hostApplySurvey) InstalledDeps() []string {
	if s == nil {
		return nil
	}
	return s.installedDeps
}

// noteRenderFailure records a pack whose render errored. It is a §4.1 blocker: its surfaces
// are absent from every count above, so a verdict that did not name it would be claiming a
// completed apply out of counts that silently lost a pack.
func (s *hostApplySurvey) noteRenderFailure(pack string) {
	if s == nil {
		return
	}
	s.failedPacks = append(s.failedPacks, pack)
}

func (s *hostApplySurvey) mark(set *map[string]bool, key string) {
	if *set == nil {
		*set = map[string]bool{}
	}
	(*set)[key] = true
}

// Changes reports whether an --assert would alter anything at all. This is the whole question
// §4.3's table branches on.
func (s *hostApplySurvey) Changes() bool { return s != nil && len(s.Changed) > 0 }

// Summary is the DESTINATION roll-up, which is the launch gate's question and not the
// operator's: it counts the destinations a traversal visited (docs/reference/host-apply-staleness.md
// §3.4). The reader's counts are the ones below it — see hostapplyverdict.go.
func (s *hostApplySurvey) Summary() string {
	if s == nil {
		return "0 in sync, 0 would change"
	}
	return fmt.Sprintf("%d in sync, %d would change", s.InSync, len(s.Changed))
}

// ChangedOfKind counts the changed destinations one written kind owns.
func (s *hostApplySurvey) ChangedOfKind(kind string) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, c := range s.Changed {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// ReplacedValues is §4.3's "values of yours replaced": how many keys, in how many files.
func (s *hostApplySurvey) ReplacedValues() (keys, files int) {
	if s == nil {
		return 0, 0
	}
	return len(s.replacedKeys), len(s.replacedFiles)
}

// DroppedEntries is §4.3's "MCP entries dropped", said as N entries from M surfaces.
func (s *hostApplySurvey) DroppedEntries() (entries, surfaces int) {
	if s == nil {
		return 0, 0
	}
	return len(s.droppedEntries), len(s.droppedFrom)
}

// SkillNames lists the skills with this fate, sorted — NAMES, because §4.4 forbids a default
// view that drops a name from a loss group, and a sorted list is the only form a report can
// print twice and get the same answer.
func (s *hostApplySurvey) SkillNames(fate skillFate) []string {
	if s == nil {
		return nil
	}
	var out []string
	for name, f := range s.skills {
		if f == fate {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Deps is §4.3's dependency count, three ways. "Not probed" is its own number and never folds
// into missing: §4.9 point 6 rules that yolo may not call an environment unready on evidence
// it does not have.
func (s *hostApplySurvey) Deps() (present, missing, notProbed int) {
	if s == nil {
		return 0, 0, 0
	}
	for _, f := range s.deps {
		switch f.State {
		case depPresent:
			present++
		case depMissing:
			missing++
		default:
			notProbed++
		}
	}
	return present, missing, notProbed + s.depsNoBin
}

// MissingDeps names the binaries that are declared and absent, sorted. The verdict line names
// them rather than counting them (§4.3): a blocker contributes its NAME, because the reader's
// next action is about that binary.
func (s *hostApplySurvey) MissingDeps() []string {
	if s == nil {
		return nil
	}
	var out []string
	for bin, f := range s.deps {
		if f.State == depMissing {
			out = append(out, bin)
		}
	}
	sort.Strings(out)
	return out
}

// MissingDepFinding returns the finding for one missing binary — the remedy the tier-3 group
// states once. Looked up by name rather than returned with MissingDeps so the sorted name list
// stays the one authority on WHICH binaries are missing.
func (s *hostApplySurvey) MissingDepFinding(bin string) hostDepFinding {
	if s == nil {
		return hostDepFinding{}
	}
	return s.deps[bin]
}

// ReplacedKeyNames lists the managed keys that would overwrite a value of the user's, sorted.
// The NAMES, because §4.4 forbids a default view that drops a name from a loss group — and the
// user's own VALUES are never printed, at any verbosity, which is that section's other
// forbidden thing.
func (s *hostApplySurvey) ReplacedKeyNames() []string { return sortedSet(s, s.replacedKeys) }

// DroppedEntryNames lists the named-table entries that would be dropped, sorted and
// deduplicated across agents (see entryLossName for why the raw strings cannot be the unit).
func (s *hostApplySurvey) DroppedEntryNames() []string { return sortedSet(s, s.droppedEntries) }

// DroppedComments is §4.4's comment class: how many surfaces would lose a comment. A loss with
// no remedy possible, and the one the verdict had no term for.
func (s *hostApplySurvey) DroppedComments() int {
	if s == nil {
		return 0
	}
	return len(s.commentSurfaces)
}

// CommentSurfaces names those surfaces, sorted.
func (s *hostApplySurvey) CommentSurfaces() []string { return sortedSet(s, s.commentSurfaces) }

// Adoptions is how many surfaces this run ADOPTED: §4.4's one-way door, and the one tier-3
// class the verdict block had no term for at all (OQ-CO7 D3). It is the count the verdict
// needs to stop reporting an adopting run as a run that did nothing.
//
// IT IS NOT A SECOND SPELLING OF Changes(), and the two are kept apart deliberately
// (report-tiers.md §6: the survey grows fields, it does not change what Changes() means). The
// launch gate reads Changes() to decide whether a home needs an apply; an adoption is
// something a completed apply DID, so folding it in would make a settled home look pending to
// the gate forever.
//
// ASSERT-ONLY, which is why the dry run's machine document carries no field for it: a render
// that writes nothing archives nothing (see HostRenderResult.Archived's own docstring), so a
// number here in a dry run could only ever be zero, and a field that is a constant is not data.
func (s *hostApplySurvey) Adoptions() int {
	if s == nil {
		return 0
	}
	return len(s.adoptedSurfaces)
}

// sortedSet is the one nil-safe reader for the survey's name sets: a sorted list is the only
// form a report can print twice and get the same answer.
func sortedSet(s *hostApplySurvey, set map[string]bool) []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FirstApply reports whether any surface this run rendered has no provenance record — yolo has
// never asserted it in this home. It is what turns the losses above into a prompt on --assert.
func (s *hostApplySurvey) FirstApply() bool { return s != nil && s.firstApply }

// FailedPacks names the packs whose render errored this run.
func (s *hostApplySurvey) FailedPacks() []string {
	if s == nil {
		return nil
	}
	return s.failedPacks
}
