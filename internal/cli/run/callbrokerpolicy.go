package run

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func cloneCellPolicy(policy map[string]config.CallBrokerCellPolicy) map[string]config.CallBrokerCellPolicy {
	if policy == nil {
		return nil
	}
	cells := make(map[string]config.CallBrokerCellPolicy, len(policy))
	for cell, setting := range policy {
		tables := make(map[string]config.CallBrokerTablePolicy, len(setting.Tables))
		for table, value := range setting.Tables {
			tables[table] = value
		}
		cells[cell] = config.CallBrokerCellPolicy{Tables: tables}
	}
	return cells
}

func cloneCallBrokerCells(in map[string]map[string]config.CallBrokerCellPolicy) map[string]map[string]config.CallBrokerCellPolicy {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]config.CallBrokerCellPolicy, len(in))
	for name, policy := range in {
		out[name] = cloneCellPolicy(policy)
	}
	return out
}

// freezeCallBrokerCells captures the gate's workspace read and validated user ceiling.
// Only selected declarations contribute; no producer or keeper later rereads config.
func freezeCallBrokerCells(merged, workspace, user *jsonx.OrderedMap) (map[string]map[string]config.CallBrokerCellPolicy, error) {
	cells := map[string]map[string]config.CallBrokerCellPolicy{}
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All() {
		if lp.Source != loopholes.SourcePack || !lp.Enabled || lp.CallBroker == nil {
			continue
		}
		policy, err := config.EffectiveCallBrokerCells(lp.CallBroker.Source, lp.CallBroker, user, workspace)
		if err != nil {
			return nil, fmt.Errorf("selected call broker %s: %w", lp.Name, err)
		}
		cells[lp.Name] = policy
	}
	return cells, nil
}
