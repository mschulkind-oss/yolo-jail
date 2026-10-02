package cli

// hostapplyfailures.go is the host apply's record of WHAT DID NOT GET WRITTEN, and whose it was:
// a pack whose render errored, and a destination refused because a symlink on its path leads
// into a directory that does not exist (entrypoint.FindBrokenLink).
//
// ONE RECORD, THREE READERS, which is why it is attributed at the point of failure rather than
// reconstructed from the report: the tier-3 group states each failure once with its fix
// (failureGroups), the verdict names the packs (hostApplyVerdict), and the launch gate decides
// from the same attribution whether the program it is about to exec is affected
// (hostApplyGateFailures). The report used to print a render failure as an interleaved stderr
// line and end with "see stderr"; the gate used to refuse `yolo host -- claude` because the pi
// pack had failed.

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostFailure is one thing this apply could not write.
type hostFailure struct {
	// Packs are the packs whose output this is: the pack whose render failed, or the pack(s)
	// declaring the refused destination. What the launch gate's relatedness test reads.
	Packs []string
	// What is the failure in the report's words — the render error, or the broken link's
	// reason.
	What string
	// Link is set for a broken-link refusal; nil for a render failure.
	Link *entrypoint.BrokenLink
}

// noteRenderFailure records a pack whose render errored, with the error. It is a tier-3
// BLOCKER: this pack's surfaces are absent from every count, so a verdict that did not name it
// would claim a completed apply out of a traversal that lost a pack.
func (s *hostApplySurvey) noteRenderFailure(pack, what string) {
	if s == nil {
		return
	}
	s.failedPacks = append(s.failedPacks, pack)
	s.failures = append(s.failures, hostFailure{Packs: []string{pack}, What: what})
}

// noteBrokenLink records a destination refused under the broken-link rule. Not counted in
// sync: the destination was not compared, and an --assert cannot leave it "as it is" in any
// sense a reader would accept, because the link is exactly what the reader has to fix.
func (s *hostApplySurvey) noteBrokenLink(packs []string, b entrypoint.BrokenLink) {
	if s == nil {
		return
	}
	for _, f := range s.failures {
		if f.Link != nil && f.Link.Link == b.Link {
			return // one link, one fact — a briefing and a config surface can share a path
		}
	}
	link := b
	s.failures = append(s.failures, hostFailure{Packs: packs, What: b.Reason(), Link: &link})
}

// The stages of a host apply whose failure the survey cannot pin on one pack's render, each by
// the word the verdict names it with. The stage's own line, above the verdict, says what failed
// and its fix; the verdict says that the stage did, so an --assert that exited 1 over one never
// ends "this home is up to date" (docs/reference/happy-path-principle.md, rule 5).
const (
	// stageDestinations: a pack's content has no destination in the selected set, so the pack
	// renders nothing (reportInferredDestinations).
	stageDestinations = "destinations"
	// stageOverlays: a config-overlay or config-list contribution is malformed (packoverlay.Collect).
	stageOverlays = "overlays"
	// stageSkills: the skills destinations were not composed, migrated or pruned (applyHostSkills).
	stageSkills = "skills"
	// stageBriefing: the briefing destinations were not composed, migrated or pruned
	// (applyHostBriefings).
	stageBriefing = "briefing"
	// stageRetire: a dropped pack's output was not retired (pruneDroppedPackOutput).
	stageRetire = "retire"
	// stageWrappers: the launch wrappers were not planned, written or cleared (applyHostWrappers).
	stageWrappers = "wrappers"
	// stageInputs: the host's derive inputs could not be composed (composeHostInputs). The apply
	// refuses there, before the verdict, in a line of its own; the launch gate reads it, and the
	// dry run's document calls it `refused` (hostApplyOutcome), never naming the stage in a
	// sentence.
	stageInputs = "inputs"
)

// noteStageFailure records that stage failed, for a stage whose failure lines lead with the
// stage's own name (`skills     refused — …`, `retire     refused — …`). It reaches the verdict,
// which names the stage (hostApplyOutcome), and it keeps the launch gate conservative: a failure
// it cannot attribute to a pack is one it cannot call unrelated (splitLaunchFailures). A stage that
// fails twice is named once.
func (s *hostApplySurvey) noteStageFailure(stage string) {
	s.noteStageFailureAs(stage)
}

// noteStageFailureAs records that stage failed, and the words its failure lines lead with, which
// the verdict names it by: the reader finds the failure above the verdict by the word the verdict
// used. The destinations stage's lines lead with the kind a pack's content reached no destination
// for (`skills     refused — …`), and the overlays stage's with the contribution's kind
// (`config-list refused — …`); the verdict named them "the destinations stage" and "the overlays
// stage", words no line above it carried. With no names, the stage's own name is its word. The
// stage itself stays the document's token (failed_stages), which a consumer branches on.
func (s *hostApplySurvey) noteStageFailureAs(stage string, names ...string) {
	if s == nil {
		return
	}
	if !slices.Contains(s.failedStages, stage) {
		s.failedStages = append(s.failedStages, stage)
	}
	if len(names) == 0 {
		names = []string{stage}
	}
	for _, n := range names {
		if !slices.Contains(s.stageNames, n) {
			s.stageNames = append(s.stageNames, n)
		}
	}
}

// StageFailures names the stages that failed, in the order they ran: the document's tokens.
func (s *hostApplySurvey) StageFailures() []string {
	if s == nil {
		return nil
	}
	return s.failedStages
}

// StageFailureNames is the verdict's words for the stages that failed, in the order their lines
// printed, each once (noteStageFailureAs).
func (s *hostApplySurvey) StageFailureNames() []string {
	if s == nil {
		return nil
	}
	return s.stageNames
}

// inputsRefused is whether the host's derive inputs could not be composed (stageInputs): a
// refusal of the whole apply, which writes nothing, rather than one stage failing among others.
func (s *hostApplySurvey) inputsRefused() bool {
	return slices.Contains(s.StageFailures(), stageInputs)
}

// unattributedFailure is whether this run failed somewhere no pack's render can be named for: a
// stage, or the host agent floor (which the launch gate's apply never runs, so only a verb's
// survey can hold one).
func (s *hostApplySurvey) unattributedFailure() bool {
	return len(s.StageFailures()) > 0 || len(s.FloorFailures()) > 0 || len(s.FloorRefusals()) > 0
}

// noteLoaded records the pack set this apply resolved, for the launch gate's relatedness test.
func (s *hostApplySurvey) noteLoaded(packs []*packload.Pack) {
	if s != nil {
		s.loaded = packs
	}
}

// Failures is everything this apply could not write, in the order it was found.
func (s *hostApplySurvey) Failures() []hostFailure {
	if s == nil {
		return nil
	}
	return s.failures
}

// failurePacks is the sorted, de-duplicated pack names across the failures — the verdict's
// names.
func failurePacks(failures []hostFailure) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range failures {
		for _, p := range f.Packs {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// failureGroups is the failure half of the remedy contract: one group per failure, BLOCKERS,
// each stated once with its fix. A broken link's fix is the link's; a render failure's is
// whatever the error names, which only the error can say.
func failureGroups(s *hostApplySurvey, home string, write bool) []remedyGroup {
	var out []remedyGroup
	notWritten := "cannot be written"
	if write {
		notWritten = "not written"
	}
	for _, f := range s.Failures() {
		if f.Link != nil {
			out = append(out, remedyGroup{
				Class: remedyClassBrokenLink,
				Key:   f.Link.Link,
				Headline: fmt.Sprintf("%s: %s is a symlink to %s, whose directory does not exist",
					notWritten, prettyHomePath(home, f.Link.Link), prettyHomePath(home, f.Link.Target)),
				Remedy: fmt.Sprintf("rm %s   (or recreate %s), then `yolo host apply --assert`",
					shellHomePath(home, f.Link.Link),
					prettyHomePath(home, filepath.Dir(f.Link.Target))),
				VerdictTerm: failureVerdictTerm(f),
				Warn:        true,
			})
			continue
		}
		out = append(out, remedyGroup{
			Class:       remedyClassRenderFailed,
			Key:         strings.Join(f.Packs, ","),
			Headline:    fmt.Sprintf("%s failed to render: %s", strings.Join(f.Packs, ", "), f.What),
			Remedy:      "fix what it names, then `yolo host apply --assert`",
			VerdictTerm: failureVerdictTerm(f),
			Warn:        true,
		})
	}
	return out
}

// failureVerdictTerm is the word the verdict must contain for a failure's group: its first
// pack, which the incomplete verdict names, or "config" for one no pack declares.
func failureVerdictTerm(f hostFailure) string {
	if len(f.Packs) > 0 && f.Packs[0] != "" {
		return f.Packs[0]
	}
	return "config"
}

// briefingDestinationPacks names the packs that DECLARE a briefing destination — an agent's own
// `briefing` contribution (one carrying `agent`) whose `into` is that path. Not every pack whose
// prose lands there: the matt pack's prose reaches pi's AGENTS.md through a destination the set
// inferred for it, and a broken link at pi's file is pi's.
func briefingDestinationPacks(loaded []*packload.Pack, home, path string) []string {
	var out []string
	for _, p := range loaded {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindBriefing && c.Agent != "" && c.Into != "" &&
				filepath.Join(home, filepath.FromSlash(c.Into)) == path {
				out = append(out, p.Name)
				break
			}
		}
	}
	return out
}

// THE LAUNCH GATE'S RELATEDNESS TEST ([OQ-HS17], host-apply-staleness.md). `yolo host -- claude`
// used to refuse because the pi pack had failed to render: a failure in a file claude never
// reads stopped claude. A failure now stops a launch only when it is in the launched program's
// own configuration; any other is reported, loudly and with its fix, and the program launches.

// launchRelatedPacks is the set of packs whose failure stops a launch of bin:
//
//   - the packs that INSTALL it (an honored `program` install whose `bin` is bin);
//   - every pack declaring a contribution or surface for one of THEIR agents (the `agent` their
//     contributions and surfaces carry) — the files the program reads;
//   - every pack whose `config-overlay` or `config-list` targets one of those agents' surfaces —
//     the packs that CONFIGURE the program;
//   - and the `needs` closure of all of them.
//
// A program no pack installs (`yolo host -- bash`) has no related pack: nothing yolo renders is
// its configuration.
func launchRelatedPacks(loaded []*packload.Pack, bin string) map[string]bool {
	related := map[string]bool{}
	agents := map[string]bool{}
	for _, p := range loaded {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.Bin == bin {
				related[p.Name] = true
			}
		}
	}
	for _, p := range loaded {
		if !related[p.Name] {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Agent != "" {
				agents[c.Agent] = true
			}
		}
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if s.Agent != "" {
				agents[s.Agent] = true
			}
		}
	}
	for _, p := range loaded {
		for _, c := range p.Decl.Contributions() {
			if c.Agent != "" && agents[c.Agent] {
				related[p.Name] = true
			}
			if (c.Kind == packdecl.KindConfigOverlay || c.Kind == packdecl.KindConfigList) &&
				c.Surface != "" && agents[strings.SplitN(c.Surface, "/", 2)[0]] {
				related[p.Name] = true
			}
		}
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if agents[s.Agent] {
				related[p.Name] = true
			}
		}
	}
	byName := map[string]*packload.Pack{}
	for _, p := range loaded {
		byName[p.Name] = p
	}
	for changed := true; changed; {
		changed = false
		for name := range related {
			p := byName[name]
			if p == nil {
				continue
			}
			for _, n := range p.Decl.Needs {
				if n.Pack != "" && !related[n.Pack] {
					related[n.Pack] = true
					changed = true
				}
			}
		}
	}
	return related
}

// splitLaunchFailures divides this apply's failures by whether they touch bin. blocking is true
// when any failure does — or when some stage failed where no pack can be named, which the gate
// cannot call unrelated.
func splitLaunchFailures(s *hostApplySurvey, bin string) (related, unrelated []hostFailure, blocking bool) {
	if s == nil {
		return nil, nil, false
	}
	rel := launchRelatedPacks(s.loaded, bin)
	for _, f := range s.Failures() {
		touches := len(f.Packs) == 0 // a failure no pack declares: not provably unrelated
		for _, p := range f.Packs {
			if rel[p] {
				touches = true
			}
		}
		if touches {
			related = append(related, f)
		} else {
			unrelated = append(unrelated, f)
		}
	}
	return related, unrelated, len(related) > 0 || s.unattributedFailure()
}

// reportLaunchFailures prints failures on the launch's stderr, each once with its fix — the same
// groups the apply's own report states them in, so a launch and `yolo host apply` say one thing.
func reportLaunchFailures(errw io.Writer, home string, failures []hostFailure) {
	s := &hostApplySurvey{failures: failures}
	for _, g := range failureGroups(s, home, true) {
		fmt.Fprintf(errw, "  ✗ %s\n", g.Headline)
		if g.Remedy != "" {
			fmt.Fprintf(errw, "    → %s\n", g.Remedy)
		}
	}
}

// reportUnrelatedLaunchFailures is the loud-and-launch half: what was not written, its fix, and
// that the program being launched does not read it.
func reportUnrelatedLaunchFailures(errw io.Writer, home, bin string, failures []hostFailure) {
	if len(failures) == 0 {
		return
	}
	fmt.Fprintf(errw, "yolo host: some of %s config was not written:\n",
		joinWords(possessives(failurePacks(failures)), "and"))
	reportLaunchFailures(errw, home, failures)
	fmt.Fprintf(errw, "  %s reads none of it — launching %s.\n", bin, bin)
}

func possessives(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + "'s"
	}
	if len(out) == 0 {
		return []string{"your"}
	}
	return out
}
