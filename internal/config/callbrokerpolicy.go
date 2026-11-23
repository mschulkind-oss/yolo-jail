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
// A `never` the user wrote for the cell, at the source or in any scope of the
// source, answers "off": the live broker has no scoped decision engine yet, so
// a scoped never is applied to the whole source (it may over-refuse, never run).
func CallBrokerCurrentCeiling(user *jsonx.OrderedMap, source, table, cell string) string {
	if callBrokerNeverRefuses(user, source, table, cell) {
		return "off"
	}
	ceiling := callBrokerLegacyCeiling(user, source, table, cell)
	if ceiling == "always" && callBrokerScopedOnRequest(user, source, table, cell) {
		// A narrower on_request under a broad always governs (design 3.2); applied source-wide.
		return "on_request"
	}
	return ceiling
}

// CallBrokerReadNeverRefuses reports whether the user's config names a read of source in `never`:
// `read`, `read.<cell>` (cell "" is a read whose pack declares no read cell), at the source or in any
// scope of it. The live broker has no scoped decision engine yet, so a scoped never refuses the read
// for the whole source (it may over-refuse, never run).
func CallBrokerReadNeverRefuses(user *jsonx.OrderedMap, source, cell string) bool {
	return callBrokerNeverRefuses(user, source, "read", cell)
}

// callBrokerScopedOnRequest reports a `[scopes.<alias>.allow]` of the source that holds the cell
// only on request, which lowers a broad always for the whole source until the scoped decision
// engine is wired.
func callBrokerScopedOnRequest(user *jsonx.OrderedMap, source, table, cell string) bool {
	if user == nil {
		return false
	}
	scopes, _ := asMap(getOr(user, "scopes", nil))
	if scopes == nil {
		return false
	}
	for _, alias := range scopes.Keys() {
		scope, _ := asMap(getOr(scopes, alias, nil))
		if scope == nil || getOr(scope, "source", nil) != any(source) {
			continue
		}
		if allow, _ := asMap(getOr(scope, "allow", nil)); allow != nil && ScopedBrokerCeilingValue(allow, table, cell) == "on_request" {
			return true
		}
	}
	return false
}

// callBrokerNeverRefuses reports a `never` in the user's `[allow.<source>]` or in
// any `[scopes.<alias>.allow]` of the source that governs this table and cell.
func callBrokerNeverRefuses(user *jsonx.OrderedMap, source, table, cell string) bool {
	if user == nil {
		return false
	}
	allows, _ := asMap(getOr(user, "allow", nil))
	if allows != nil {
		if own, _ := asMap(getOr(allows, source, nil)); own != nil && ScopedBrokerCeilingValue(own, table, cell) == "never" {
			return true
		}
	}
	scopes, _ := asMap(getOr(user, "scopes", nil))
	if scopes == nil {
		return false
	}
	for _, alias := range scopes.Keys() {
		scope, _ := asMap(getOr(scopes, alias, nil))
		if scope == nil || getOr(scope, "source", nil) != any(source) {
			continue
		}
		if allow, _ := asMap(getOr(scope, "allow", nil)); allow != nil && ScopedBrokerCeilingValue(allow, table, cell) == "never" {
			return true
		}
	}
	return false
}

// callBrokerScopedAskFalse reports a workspace `[scopes.<alias>.requests]` line
// of the source that asks `false` for this table and cell. Without the scoped
// decision engine it refuses the cell for the whole source.
func callBrokerScopedAskFalse(workspace *jsonx.OrderedMap, source, table, cell string) bool {
	return callBrokerScopedRequestIs(workspace, source, table, cell, false)
}

// callBrokerScopedAsk reports a workspace `[scopes.<alias>.requests]` line of the source that asks
// "ask" for this table and cell, which makes the whole source ask until the scoped engine is wired.
func callBrokerScopedAsk(workspace *jsonx.OrderedMap, source, table, cell string) bool {
	return callBrokerScopedRequestIs(workspace, source, table, cell, "ask")
}

func callBrokerScopedRequestIs(workspace *jsonx.OrderedMap, source, table, cell string, want any) bool {
	scopes, _ := asMap(getOr(workspace, "scopes", nil))
	if scopes == nil {
		return false
	}
	for _, alias := range scopes.Keys() {
		scope, _ := asMap(getOr(scopes, alias, nil))
		if scope == nil || getOr(scope, "source", nil) != any(source) {
			continue
		}
		requests, _ := asMap(getOr(scope, "requests", nil))
		if requests == nil {
			continue // a scope with no request lines (getOr on a nil map panics)
		}
		values, _ := asMap(getOr(requests, table, nil))
		if values == nil {
			continue
		}
		if raw, has := values.Get(cell); has && raw == want {
			return true
		}
	}
	return false
}

func callBrokerLegacyCeiling(user *jsonx.OrderedMap, source, table, cell string) string {
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
// scope from that launch. A snapshot reader may instead supply its validated live
// workspace snapshot and one fresh host-user bundle, never a fresh workspace read.
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
			if request != "false" && callBrokerScopedAsk(workspace, source, table, cell.Name) {
				// A narrower ask of "ask" governs a broader true or absent line (design 3.2), applied source-wide.
				request = "ask"
			}
			ceiling := "off"
			for _, candidate := range []string{"on_request", "always"} {
				if callBrokerCeilingField(sourceAllow, candidate, table, cell.Name) ||
					(table == "working_set" && callBrokerCeilingField(sourceAllow, candidate, "all", cell.Name)) {
					ceiling = candidate
				}
			}
			if ceiling == "always" && callBrokerScopedOnRequest(user, source, table, cell.Name) {
				ceiling = "on_request"
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
			if callBrokerNeverRefuses(user, source, table, cell.Name) || callBrokerScopedAskFalse(workspace, source, table, cell.Name) {
				// A never or a narrower false is a refusal that no broader grant overrides.
				ceiling, decision = "off", "refuse"
			}
			policy.Tables[table] = CallBrokerTablePolicy{Request: request, Ceiling: ceiling, Decision: decision}
		}
		out[cell.Name] = policy
	}
	return out, nil
}

// SourcesNamingNever are the `[allow.<source>]` tables of user whose `never` list is not empty, in
// the config's order. A broker started before `never` existed ignores such a list, so a caller that
// can see a running broker names the stop-and-relaunch step for each (BrokersIgnoringNever).
func SourcesNamingNever(user *jsonx.OrderedMap) []string {
	if user == nil {
		return nil
	}
	allows, _ := asMap(getOr(user, "allow", nil))
	if allows == nil {
		return nil
	}
	var out []string
	for _, source := range allows.Keys() {
		table, _ := asMap(getOr(allows, source, nil))
		if table == nil {
			continue
		}
		if never, _ := getOr(table, "never", nil).([]any); len(never) != 0 {
			out = append(out, source)
		}
	}
	return out
}
