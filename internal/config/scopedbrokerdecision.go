package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// scopedOperationPolicies converts already-validated selected metadata to the
// immutable decision leaf. It does not feed the shipped gate or live daemon.
func scopedOperationPolicies(plan *ReadableAllowancePlan, input ReadableAllowanceInput) error {
	plan.OperationPolicies = map[string]brokerpolicy.Policy{}
	for _, source := range plan.Sources {
		if source.CallBroker == nil {
			continue
		}
		matcher, err := brokerpolicy.NewScopeMatcher(source.CallBroker.Scope.Nesting, source.CallBroker.Scope.Separator, source.CallBroker.Scope.Anchors)
		if err != nil {
			return err
		}
		var cells []brokerpolicy.Cell
		var readNames []string
		for _, cell := range source.CallBroker.Cells {
			kind := brokerpolicy.Write
			if cell.IsRead() {
				kind = brokerpolicy.Read
				readNames = append(readNames, cell.Name)
			}
			cells = append(cells, brokerpolicy.Cell{Name: cell.Name, Kind: kind, Tables: cell.Tables, Defaults: cell.Default})
		}
		if len(readNames) == 0 && source.ReadAvailable {
			readNames = []string{""}
		}
		var ceilings, asks []brokerpolicy.Rule
		origin := func(path string, origins map[string][]string) string {
			return path + " (" + strings.Join(origins[path], ", ") + ")"
		}
		addCeilings := func(scope, path string, allow *jsonx.OrderedMap) {
			if allow == nil {
				allow = jsonx.NewOrderedMap()
			}
			add := func(kind brokerpolicy.Kind, table, cell string) {
				word := ScopedBrokerCeilingValue(allow, table, cell)
				if word == "absent" {
					return
				}
				decisionTable := table
				if kind == brokerpolicy.Read {
					decisionTable = ""
				}
				ceilings = append(ceilings, brokerpolicy.Rule{Source: source.Source, Scope: scope, Kind: kind, Cell: cell, Table: decisionTable, Value: brokerpolicy.Value(word), Origin: origin(path+"."+word, input.UserOrigins)})
			}
			for _, name := range readNames {
				add(brokerpolicy.Read, "read", name)
			}
			for _, cell := range cells {
				if cell.Kind == brokerpolicy.Write {
					for _, table := range cell.Tables {
						add(brokerpolicy.Write, table, cell.Name)
					}
				}
			}
		}
		addReadAsks := func(scope, path string, reads map[string]string) {
			keys := []string{""}
			for _, name := range readNames {
				if name != "" {
					keys = append(keys, name)
				}
			}
			for _, name := range keys {
				word, has := reads[name]
				if !has {
					continue
				}
				line := path
				if name != "" {
					line += "." + name
				}
				asks = append(asks, brokerpolicy.Rule{Source: source.Source, Scope: scope, Kind: brokerpolicy.Read, Cell: name, Value: brokerpolicy.Value(word), Origin: origin(line, input.WorkspaceOrigins)})
			}
		}
		allows, _ := readableTable(input.User, "allow")
		allow, _ := asMap(readableValue(allows, source.Source))
		addCeilings("", "allow."+source.Source, allow)
		addReadAsks("", "requests."+source.Source+".read", source.ReadRequests)
		requests, _ := readableTable(input.Workspace, "requests")
		request, _ := asMap(readableValue(requests, source.Source))
		for _, cell := range cells {
			if cell.Kind != brokerpolicy.Write || request == nil {
				continue
			}
			for _, table := range cell.Tables {
				values, _ := asMap(readableValue(request, table))
				if values == nil {
					continue
				}
				if raw, has := values.Get(cell.Name); has {
					word, _ := requestWord(raw)
					path := "requests." + source.Source + "." + table + "." + cell.Name
					asks = append(asks, brokerpolicy.Rule{Source: source.Source, Kind: brokerpolicy.Write, Cell: cell.Name, Table: table, Value: brokerpolicy.Value(word), Origin: origin(path, input.WorkspaceOrigins)})
				}
			}
		}
		var routes []brokerpolicy.Route
		for _, scope := range plan.Scopes {
			if !scope.Selected || scope.Source != source.Source {
				continue
			}
			path := "scopes." + scope.Alias
			if scope.Layer == "ceiling" {
				addCeilings(scope.Resource, path+".allow", scopeAllowValue(scope))
				if scope.Context != "" {
					routes = append(routes, brokerpolicy.Route{Source: source.Source, Scope: scope.Resource, Context: scope.Context, Origin: origin(path+".context", input.UserOrigins)})
				}
				continue
			}
			addReadAsks(scope.Resource, path+".requests.read", scope.Reads)
			for _, cell := range cells {
				if cell.Kind != brokerpolicy.Write {
					continue
				}
				for _, table := range cell.Tables {
					if word, has := scope.Tables[table][cell.Name]; has {
						line := path + ".requests." + table + "." + cell.Name
						asks = append(asks, brokerpolicy.Rule{Source: source.Source, Scope: scope.Resource, Kind: brokerpolicy.Write, Cell: cell.Name, Table: table, Value: brokerpolicy.Value(word), Origin: origin(line, input.WorkspaceOrigins)})
					}
				}
			}
		}
		off := allow != nil && readableValue(allow, "off") == true
		policy, err := brokerpolicy.NewPolicy(source.Source, matcher, cells, source.ReadAvailable, off, ceilings, asks, routes)
		if err != nil {
			return err
		}
		plan.OperationPolicies[source.Source] = policy
	}
	return nil
}
