package brokerpolicy

import (
	"fmt"
	"slices"
)

type Kind string

const (
	Read  Kind = "read"
	Write Kind = "write"
)

type Value string

const (
	Absent    Value = "absent"
	False     Value = "false"
	Ask       Value = "ask"
	True      Value = "true"
	Never     Value = "never"
	OnRequest Value = "on_request"
	Always    Value = "always"
)

type Decision string

const (
	Refuse Decision = "refuse"
	Hold   Decision = "ask"
	Run    Decision = "run"
)

type Resource struct{ Type, ID string }

// Operation is a parser-validated subject. Policy matching never proves admission.
type Operation struct {
	Source, Cell, Description string
	Kind                      Kind
	Chain                     []Resource
}

type Cell struct {
	Name                  string
	Kind                  Kind
	Tables                []string
	Defaults              map[string]bool
	DefaultWithoutCeiling bool // Set only by an adapter for an existing native default.
}

// Rule is final within its authored scope. Ceiling rules are expanded per cell by
// the adapter using the shipped resolver; a read ask with Cell empty is the alias.
type Rule struct {
	Source, Scope, Cell, Table, Origin string
	Kind                               Kind
	Value                              Value
}

type Route struct{ Source, Scope, Context, Origin string }

type Membership struct {
	Workspace, Source, Type, ID string
	Live, Call                  bool
}

type Policy struct {
	source    string
	matcher   ScopeMatcher
	cells     map[string]Cell
	readGrant bool
	off       bool
	ceilings  []Rule
	asks      []Rule
	routes    []Route
}

func NewPolicy(source string, matcher ScopeMatcher, cells []Cell, readGrant, off bool, ceilings, asks []Rule, routes []Route) (Policy, error) {
	p := Policy{source: source, matcher: matcher, cells: map[string]Cell{}, readGrant: readGrant, off: off}
	if source == "" {
		return p, fmt.Errorf("policy needs a selected source")
	}
	for _, cell := range cells {
		if cell.Name == "" || cell.Name == "read" || cell.Kind != Read && cell.Kind != Write {
			return p, fmt.Errorf("invalid policy cell")
		}
		if _, exists := p.cells[cell.Name]; exists {
			return p, fmt.Errorf("duplicate policy cell %q", cell.Name)
		}
		if cell.Kind == Read && (len(cell.Tables) != 0 || len(cell.Defaults) != 0 || cell.DefaultWithoutCeiling) {
			return p, fmt.Errorf("read cells have no tables or defaults")
		}
		if cell.Kind == Write {
			if len(cell.Tables) == 0 {
				return p, fmt.Errorf("write cell has no declared table")
			}
			seenTables := map[string]bool{}
			for _, table := range cell.Tables {
				if table != "all" && table != "working_set" || seenTables[table] {
					return p, fmt.Errorf("invalid or duplicate write table")
				}
				seenTables[table] = true
			}
			for table := range cell.Defaults {
				if !seenTables[table] {
					return p, fmt.Errorf("default names an undeclared table")
				}
			}
		}
		cell.Tables = slices.Clone(cell.Tables)
		defaults := map[string]bool{}
		for table, value := range cell.Defaults {
			defaults[table] = value
		}
		cell.Defaults = defaults
		p.cells[cell.Name] = cell
	}
	for layer, rules := range [][]Rule{ceilings, asks} {
		seen := map[string]bool{}
		for _, rule := range rules {
			if rule.Source != source || rule.Scope != "" && matcher.ValidateScope(rule.Scope) != nil {
				return p, fmt.Errorf("policy rule has an invalid source or scope")
			}
			if rule.Kind == Read {
				if rule.Table != "" || !p.validReadCell(rule.Cell, layer == 1) {
					return p, fmt.Errorf("policy rule names an undeclared read cell")
				}
			} else if cell, ok := p.cells[rule.Cell]; rule.Kind != Write || !ok || cell.Kind != Write || !slices.Contains(cell.Tables, rule.Table) {
				return p, fmt.Errorf("policy rule names an undeclared write table or cell")
			}
			valid := rule.Value == Absent
			if layer == 0 {
				valid = valid || rule.Value == Never || rule.Value == OnRequest || rule.Value == Always
			} else {
				valid = valid || rule.Value == False || rule.Value == Ask || rule.Value == True
			}
			if !valid {
				return p, fmt.Errorf("policy rule has an invalid layer value")
			}
			identity := rule.Scope + "\x00" + string(rule.Kind) + "\x00" + rule.Table + "\x00" + rule.Cell
			if seen[identity] {
				return p, fmt.Errorf("duplicate policy rule")
			}
			seen[identity] = true
		}
	}
	seenRoutes := map[string]bool{}
	for _, route := range routes {
		if seenRoutes[route.Scope] {
			return p, fmt.Errorf("duplicate policy route")
		}
		seenRoutes[route.Scope] = true
		if route.Source != source || route.Context == "" || route.Scope != "" && matcher.ValidateScope(route.Scope) != nil {
			return p, fmt.Errorf("invalid policy route")
		}
	}
	p.ceilings, p.asks, p.routes = slices.Clone(ceilings), slices.Clone(asks), slices.Clone(routes)
	return p, nil
}

func (p Policy) validReadCell(name string, alias bool) bool {
	reads := 0
	for _, cell := range p.cells {
		if cell.Kind == Read {
			reads++
		}
	}
	if name == "" {
		return reads == 0 && p.readGrant || alias && reads != 0
	}
	cell, ok := p.cells[name]
	return ok && cell.Kind == Read
}

// CeilingValue adds only the covering-never exception. The no-never result is
// supplied by the shipped adapter, including its own broad/table precedence.
func CeilingValue(own, broad string, always, onRequest, never []string, shipped Value) Value {
	if slices.Contains(never, own) {
		return Never
	}
	if !slices.Contains(never, broad) {
		return shipped
	}
	if slices.Contains(always, own) {
		return Always
	}
	if slices.Contains(onRequest, own) {
		return OnRequest
	}
	return Never
}

func Combine(ask, ceiling Value) Decision {
	if ask != Absent && ask != False && ask != Ask && ask != True {
		return Refuse
	}
	if ask == False || ceiling != Always && ceiling != OnRequest {
		return Refuse
	}
	if ask == Ask || ceiling == OnRequest && ask != True {
		return Hold
	}
	return Run
}

// SelectWriteTable consumes already-filtered live membership. Call/timed entries
// never select a working-set table; reads have no table at all.
func SelectWriteTable(op Operation, workspace string, members []Membership) string {
	if op.Kind != Write {
		return ""
	}
	for _, member := range members {
		if !member.Live || member.Call || member.Source != op.Source || member.Workspace != workspace {
			continue
		}
		for _, resource := range op.Chain {
			if resource.Type == member.Type && resource.ID == member.ID {
				return "working_set"
			}
		}
	}
	return "all"
}

func chainIDs(op Operation) []string {
	ids := make([]string, len(op.Chain))
	for i, resource := range op.Chain {
		ids[i] = resource.ID
	}
	return ids
}

func (p Policy) governing(rules []Rule, op Operation, table string) Rule {
	var scopes []string
	for _, rule := range rules {
		if rule.Scope != "" && !slices.Contains(scopes, rule.Scope) {
			scopes = append(scopes, rule.Scope)
		}
	}
	ordered := p.matcher.ApplyingScopes(scopes, chainIDs(op))
	ordered = append(ordered, ScopeMatch{Scope: ""})
	for _, match := range ordered {
		var alias *Rule
		for _, rule := range rules {
			if rule.Scope != match.Scope || rule.Kind != op.Kind || rule.Table != table || rule.Value == Absent {
				continue
			}
			if rule.Cell == op.Cell {
				return rule
			}
			if op.Kind == Read && rule.Cell == "" {
				copy := rule
				alias = &copy
			}
		}
		if alias != nil {
			return *alias
		}
	}
	return Rule{Value: Absent}
}

// RouteFor uses the same order as rules, but considers only route-bearing scopes.
// It returns identity metadata, never environment values or execution authority.
func (p Policy) RouteFor(op Operation) (Route, bool) {
	var scopes []string
	for _, route := range p.routes {
		if route.Scope != "" {
			scopes = append(scopes, route.Scope)
		}
	}
	matches := append(p.matcher.ApplyingScopes(scopes, chainIDs(op)), ScopeMatch{Scope: ""})
	for _, match := range matches {
		for _, route := range p.routes {
			if route.Scope == match.Scope && route.Source == op.Source {
				return route, true
			}
		}
	}
	return Route{}, false
}

type OperationDecision struct {
	Operation Operation
	Table     string
	Ask       Rule
	Ceiling   Rule
	Decision  Decision
}

type BundleDecision struct {
	Decision   Decision
	Operations []OperationDecision
}

// Decide reports a bundle; it neither files a request nor spends/executes anything.
func (p Policy) Decide(operations []Operation, workspace string, members []Membership) BundleDecision {
	out := BundleDecision{Decision: Run}
	if len(operations) == 0 {
		out.Decision = Refuse
	}
	for _, op := range operations {
		op.Chain = slices.Clone(op.Chain)
		table := SelectWriteTable(op, workspace, members)
		ask, ceiling := p.governing(p.asks, op, table), p.governing(p.ceilings, op, table)
		cell, declared := p.cells[op.Cell]
		valid := op.Source == p.source && len(op.Chain) != 0 && (op.Kind == Read && p.validReadCell(op.Cell, false) || op.Kind == Write && declared && cell.Kind == Write && slices.Contains(cell.Tables, table))
		if ask.Value == Absent && op.Kind == Write {
			if fallback, exists := cell.Defaults[table]; exists {
				ask = Rule{Source: p.source, Cell: op.Cell, Kind: Write, Table: table, Origin: "declared default", Value: False}
				if fallback {
					ask.Value = True
				}
			}
		}
		decision := Combine(ask.Value, ceiling.Value)
		if cell.DefaultWithoutCeiling && ask.Origin == "declared default" && ask.Value == True && ceiling.Value == Absent {
			decision = Run
		}
		if !valid || p.off {
			decision = Refuse
		}
		out.Operations = append(out.Operations, OperationDecision{Operation: op, Table: table, Ask: ask, Ceiling: ceiling, Decision: decision})
		if decision == Refuse || decision == Hold && out.Decision == Run {
			out.Decision = decision
		}
	}
	return out
}
