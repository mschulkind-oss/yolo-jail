package config

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// callBrokerRequested reads the exact request/default spelling accepted by validation.
func callBrokerRequested(cell loopholedecl.CallBrokerCell, requests *jsonx.OrderedMap, table string) (string, bool) {
	requested := "absent"
	if fallback, exists := cell.Default[table]; exists {
		if fallback {
			requested = "true"
		} else {
			requested = "false"
		}
	}
	if requests == nil {
		return requested, true
	}
	values, _ := asMap(getOr(requests, table, nil))
	if values == nil {
		return requested, true
	}
	raw, exists := values.Get(cell.Name)
	if !exists {
		return requested, true
	}
	switch value := raw.(type) {
	case bool:
		if value {
			return "true", true
		}
		return "false", true
	case string:
		return value, value == "ask"
	default:
		return "", false
	}
}

func callBrokerCeilingField(allow *jsonx.OrderedMap, field, table, cell string) bool {
	if allow == nil {
		return false
	}
	list, _ := getOr(allow, field, nil).([]any)
	for _, raw := range list {
		token, _ := raw.(string)
		if token == table || token == table+"."+cell {
			return true
		}
	}
	return false
}

func callBrokerCeiling(allow *jsonx.OrderedMap, table, cell string) bool {
	return callBrokerCeilingField(allow, "always", table, cell) || callBrokerCeilingField(allow, "on_request", table, cell)
}

// CallBrokerCurrentCeiling reads only the current host user's allowance. It
// never loads workspace requests or turns a pack default into host permission.
func CallBrokerCurrentCeiling(user *jsonx.OrderedMap, source, table, cell string) string {
	if user == nil {
		return "off"
	}
	allows, _ := asMap(getOr(user, "allow", nil))
	if allows == nil {
		return "off"
	}
	sourceAllow, _ := asMap(getOr(allows, source, nil))
	for _, field := range []string{"always", "on_request"} {
		if callBrokerCeilingField(sourceAllow, field, table, cell) ||
			table == "working_set" && callBrokerCeilingField(sourceAllow, field, "all", cell) {
			return field
		}
	}
	return "off"
}

// CallBrokerTablePolicy retains the approved input and its result for one declared
// table. Request is absent, true, false or ask; Ceiling is off, on_request or
// always. A snapshot is not a grant: the live decision must still check scope,
// working-set membership, read standing and store coverage.
type CallBrokerTablePolicy struct {
	Request  string `json:"request"`
	Ceiling  string `json:"ceiling"`
	Decision string `json:"decision"`
}

type CallBrokerCellPolicy struct {
	Tables map[string]CallBrokerTablePolicy `json:"tables"`
}

// EffectiveCallBrokerCells snapshots the validated request/default and ceiling pair.
// The caller must use the workspace read approved by the gate and the validated user
// scope from that launch; a host daemon must never call this with a fresh config read.
func EffectiveCallBrokerCells(source string, b *loopholedecl.CallBroker, user, workspace *jsonx.OrderedMap) (map[string]CallBrokerCellPolicy, error) {
	if b == nil || user == nil || workspace == nil {
		return nil, fmt.Errorf("call broker policy is incomplete; retry the launch after checking its config")
	}
	var errs, warns []string
	validateCallBrokerCells(source, b, user, workspace, &errs, &warns)
	if len(errs) != 0 {
		return nil, fmt.Errorf("call broker policy is invalid: %s; run `yolo check` and retry", errs[0])
	}
	requests, _ := asMap(getOr(workspace, "requests", nil))
	var sourceRequests *jsonx.OrderedMap
	if requests != nil {
		sourceRequests, _ = asMap(getOr(requests, source, nil))
	}
	allows, _ := asMap(getOr(user, "allow", nil))
	var sourceAllow *jsonx.OrderedMap
	if allows != nil {
		sourceAllow, _ = asMap(getOr(allows, source, nil))
	}
	out := make(map[string]CallBrokerCellPolicy, len(b.Cells))
	for _, cell := range b.Cells {
		policy := CallBrokerCellPolicy{Tables: make(map[string]CallBrokerTablePolicy, len(cell.Tables))}
		for _, table := range cell.Tables {
			request, _ := callBrokerRequested(cell, sourceRequests, table)
			ceiling := "off"
			for _, candidate := range []string{"on_request", "always"} {
				if callBrokerCeilingField(sourceAllow, candidate, table, cell.Name) ||
					(table == "working_set" && callBrokerCeilingField(sourceAllow, candidate, "all", cell.Name)) {
					ceiling = candidate
				}
			}
			decision := "ask"
			switch {
			case request == "false":
				decision = "refuse"
			case request == "ask":
				// An explicit ask wins even under an always ceiling.
			case request == "true" && ceiling != "off", request == "absent" && ceiling == "always":
				decision = "run"
			}
			policy.Tables[table] = CallBrokerTablePolicy{Request: request, Ceiling: ceiling, Decision: decision}
		}
		out[cell.Name] = policy
	}
	return out, nil
}
