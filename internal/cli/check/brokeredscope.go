package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// brokeredScopeForCheck is `yolo check --accept-config-changes`'s view of the approval
// record's scope part (docs/design/boundary-broker.md BB-D30): the repository scope of every
// brokered loophole a launch here would start, read from the workspace's remotes exactly as
// the launch reads them. nil when none would start, so the check writes no scope part for a
// workspace whose launches never start a broker.
//
// "Would start" is loopholes.Set.BrokeredToStart, the predicate the launch's gate and spawn
// share, over the backend filter the launch applies (run's loopholeAllow): Apple Container
// starts no brokered loophole, and every other backend starts them all.
func brokeredScopeForCheck(workspace string, merged *jsonx.OrderedMap, rt string) *config.ScopeCheck {
	set := loopholes.NewHostSet(subMap(merged, "loopholes"))
	lps := set.BrokeredToStart(func(string) bool { return rt != "container" })
	if len(lps) == 0 {
		return nil
	}
	check := &config.ScopeCheck{}
	for _, lp := range lps {
		check.Sources = append(check.Sources, config.ScopeSource{
			Source: lp.Brokered.Source, Label: lp.Name,
			Read: brokerscope.ReadRemotes(workspace, lp.Brokered.RemoteHost)})
	}
	return check
}

func scopeSourcesOf(s *config.ScopeCheck) []config.ScopeSource {
	if s == nil {
		return nil
	}
	return s.Sources
}

func describeRepos(repos []string) string {
	if len(repos) == 0 {
		return "none (no remote on the forge)"
	}
	return strings.Join(repos, ", ")
}
