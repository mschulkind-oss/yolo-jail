package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// brokeredScopeForCheck is `yolo check --accept-config-changes`'s view of the approval
// record's scope part (docs/design/boundary-broker.md BB-D30): the repository scope of every
// brokered loophole a launch here would start, its remotes and the workspace's
// `brokered.<source>.repos` entry, read through the helper the launch's gate uses
// (config.NewScopeCheck) from the same one read of the workspace config. nil when none would
// start, so the check writes no scope part for a workspace whose launches never start a broker.
//
// "Would start" is loopholes.Set.BrokeredToStart, the predicate the launch's gate and spawn
// share, over the backend filter the launch applies (run.HostServiceAdmittedOn): Apple Container
// starts no brokered loophole, and every other backend starts them all.
//
// The launch's own collector (run.CollectScopeCheck), never a copy of it: two collectors are how
// `yolo check` and a launch come to disagree about what the gate asks. It also carries each
// selected grant-declaring loophole's grants (docs/reference/loophole-grants.md §4.2).
func brokeredScopeForCheck(workspace string, merged *jsonx.OrderedMap, rt string, read *config.WorkspaceRead) *config.ScopeCheck {
	return collectScopeCheck(run.ScopeInput{Workspace: workspace, Merged: merged, Runtime: rt, Read: read})
}

// collectScopeCheck is run.CollectScopeCheck, a variable so a test can prove the check goes
// through it.
var collectScopeCheck = run.CollectScopeCheck

// printRecordedGateChanges is what `yolo check --accept-config-changes` shows before it records:
// the repository scope's changes and the loophole grants', each only when it changed.
func printRecordedGateChanges(r *reporter, rep config.ChangeReport) {
	if rep.ScopeChanged {
		r.line("  Repository scope changed since the last approval; recording it as approved:")
		for _, line := range append(rep.ScopeBlock, rep.ScopeCounts...) {
			r.line("    " + line)
		}
	}
	if rep.GrantsChanged {
		r.line("  Loophole grants changed since the last approval; recording them as approved:")
		for _, line := range append(rep.GrantBlock, rep.GrantCounts...) {
			r.line("    " + termsafe.Visible(line))
		}
	}
}

// reportInertGrantRequests names each grant-declaring loophole this setup runs not at all whose
// grants this project requests (LG-D10): the gate shows no line for it, so `yolo check` says why,
// in the inert report's words. It also names each grant the approval record carries that no file
// of this project states, with the `yolo request` line that writes it. Nothing when the workspace config cannot be read for the gate, which
// the config's own section reports.
func reportInertGrantRequests(r *reporter, workspace string, merged *jsonx.OrderedMap, rt string) {
	read, err := config.ReadWorkspaceForGate(workspace)
	if err != nil {
		return
	}
	scope := collectScopeCheck(run.ScopeInput{Workspace: workspace, Merged: merged, Runtime: rt, Read: read})
	if scope == nil {
		return
	}
	// A grant the approval record carries from a retired switch file, which no file of this
	// project requests: it stands, and the note names the request that states it (LG-D11).
	for _, note := range scope.UnstatedRecordGrantNotes() {
		title, detail, _ := strings.Cut(note, "\n")
		r.warn(termsafe.Visible(title), termsafe.VisibleLines(strings.TrimSpace(detail)))
	}
	for _, ig := range scope.InertLoopholeGrants {
		if len(ig.Requested) == 0 {
			continue
		}
		r.dim(termsafe.Visible(ig.Pack + ": this project requests " + strings.Join(ig.Requested, ", ") +
			", which the config-change gate does not show: " + ig.Inert + ". It grants nothing here, " +
			"and nothing is missing to install."))
	}
}

func scopeSourcesOf(s *config.ScopeCheck) []config.ScopeSource {
	if s == nil {
		return nil
	}
	return s.Sources
}

// describeScope is the recorded line's list: every repository in the source's scope, its
// remotes' and its entry's, or what an empty one has none of.
func describeScope(s config.ScopeSource) string {
	repos := s.Repos()
	if len(repos) == 0 && s.Form == brokerscope.FormRemoteURL {
		return "none (no remote)"
	}
	if len(repos) == 0 {
		return "none (no remote on " + s.RemoteHost + " and no `" + config.BrokeredReposKey(s.Source) + "` entry)"
	}
	return strings.Join(repos, ", ")
}

// printRetiredBrokeredMoves is host `yolo check`'s own section for the retired
// `brokered.<source>.workspaces` form (docs/design/workspace-widening.md §3.6, WW-D21): every
// project's move, which the shared validator never prints, since a launch tees its messages
// into a log that workspace's jail reads. This output is copied into no workspace. Nothing in a
// jail, whose user scope is the host's.
func (o *Options) printRetiredBrokeredMoves(r *reporter) {
	if o.inJail() {
		return
	}
	moves := config.RetiredBrokeredMoves()
	if len(moves) == 0 {
		return
	}
	r.line("  Each project's move out of the retired `brokered.<source>.workspaces` key (printed here alone):")
	for _, m := range moves {
		r.dim(termsafe.Visible(m.Edit()))
	}
}
