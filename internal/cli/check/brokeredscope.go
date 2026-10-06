package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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
// share, over the backend filter the launch applies (run's loopholeAllow): Apple Container
// starts no brokered loophole, and every other backend starts them all.
func brokeredScopeForCheck(workspace string, merged *jsonx.OrderedMap, rt string, read *config.WorkspaceRead) *config.ScopeCheck {
	set := loopholes.NewHostSet(subMap(merged, "loopholes"))
	lps := set.BrokeredToStart(func(string) bool { return rt != "container" })
	brokers := make([]config.ScopeBroker, 0, len(lps))
	for _, lp := range lps {
		brokers = append(brokers, config.ScopeBroker{Source: lp.Brokered.Source, Label: lp.Name,
			RemoteHost: lp.Brokered.RemoteHost})
	}
	return config.NewScopeCheck(workspace, brokers, read)
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
