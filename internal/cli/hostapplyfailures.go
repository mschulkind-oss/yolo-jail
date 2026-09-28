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
	"path/filepath"
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

// noteUnattributedFailure records that this run failed somewhere the survey cannot pin on a
// pack — an overlay problem, a refused skills or briefing stage, the wrappers. It is what keeps
// the launch gate conservative: a failure it cannot attribute is one it cannot call unrelated.
func (s *hostApplySurvey) noteUnattributedFailure() {
	if s != nil {
		s.unattributedFailure = true
	}
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
func failureGroups(s *hostApplySurvey, home string) []remedyGroup {
	var out []remedyGroup
	for _, f := range s.Failures() {
		if f.Link != nil {
			out = append(out, remedyGroup{
				Class: remedyClassBrokenLink,
				Key:   f.Link.Link,
				Headline: fmt.Sprintf("not written: %s is a symlink to %s, whose directory does "+
					"not exist", prettyHomePath(home, f.Link.Link), prettyHomePath(home, f.Link.Target)),
				Remedy: fmt.Sprintf("rm %s   (or recreate %s), then `yolo host apply --assert`",
					prettyHomePath(home, f.Link.Link),
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
