package run

import (
	"context"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// blockIncompleteSnapshotApply is an intermediate no-effect boundary, NOT consumer
// detection or the older frozen-ask precheck. No initial launcher publishes /3 yet.
// A recognized snapshot source cannot go through the separate legacy roots/grant
// writers while its complete atomic apply and daemon proof are still unimplemented.
func (o *Options) blockIncompleteSnapshotApply(run *applyRun, plan []applyChange) []applyChange {
	if !run.mode.running || run.mode.unknown {
		return plan
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyStepDeadline)
	defer cancel()
	blocked := map[string]string{}
	packs := map[string]string{}
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(run.cfg)).All() {
		if lp.CallBroker == nil {
			continue
		}
		source := lp.CallBroker.Source
		files, err := liveScopeFilesSeam(ctx, source, run.cname, o.Workspace)
		if err != nil {
			continue // Existing discovery-error dispositions remain the legacy plan's.
		}
		for _, file := range files {
			if file.File.PolicySnapshot != nil {
				blocked[source] = lp.Name
				packs[lp.Pack] = source
				break
			}
		}
	}
	if len(blocked) == 0 {
		return plan
	}
	reason := "snapshot apply is not active in this build; no authority or ancillary grant writes were performed for this source; stop this jail with `yolo stop` and relaunch after complete snapshot apply is installed"
	for i := range plan {
		c := &plan[i]
		source := c.scope.source
		if c.grant.lp != nil && c.grant.lp.CallBroker != nil {
			source = c.grant.lp.CallBroker.Source
		}
		_, affected := blocked[source]
		if !affected && c.key == "requests" {
			for candidate := range blocked {
				if snapshotRequestLabel(c.label, candidate) {
					affected = true
				}
			}
			for pack := range packs {
				if snapshotRequestLabel(c.label, pack) {
					affected = true
				}
			}
		}
		if affected {
			c.outcome, c.reason = outcomeSnapshotPending, reason
		}
	}
	// Even a no-op must not claim a consuming source was applied from legacy files.
	var sources []string
	for source := range blocked {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: blocked[source] + " complete policy", scope: scopeDelta{source: source}, outcome: outcomeSnapshotPending, reason: reason})
	}
	return plan
}

func snapshotRequestLabel(label, source string) bool {
	for _, path := range strings.Split(label, ", ") {
		if path == "requests."+source || path == "requests.\""+source+"\"" {
			return true
		}
	}
	return false
}
