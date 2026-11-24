package brokerpolicy

import (
	"reflect"
	"testing"
)

func fixturePolicy(t *testing.T, ceilings, asks []Rule, routes []Route) Policy {
	t.Helper()
	matcher, err := NewScopeMatcher("segment", "/", []string{"s3://", "ce://"})
	if err != nil {
		t.Fatal(err)
	}
	cells := []Cell{{Name: "list", Kind: Read}, {Name: "put", Kind: Write, Tables: []string{"all", "working_set"}}}
	p, err := NewPolicy("fixture", matcher, cells, true, false, ceilings, asks, routes)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fixtureRule(scope, cell, table string, kind Kind, value Value) Rule {
	return Rule{Source: "fixture", Scope: scope, Cell: cell, Table: table, Kind: kind, Value: value, Origin: scope + ":" + cell + ":" + string(value)}
}

func TestScopedBrokerPolicySixteenPairs(t *testing.T) {
	rows := []struct {
		ask  Value
		want []Decision
	}{
		{False, []Decision{Refuse, Refuse, Refuse, Refuse}},
		{Ask, []Decision{Refuse, Refuse, Hold, Hold}},
		{True, []Decision{Refuse, Refuse, Run, Run}},
		{Absent, []Decision{Refuse, Refuse, Hold, Run}},
	}
	for _, kind := range []Kind{Read, Write} {
		for _, row := range rows {
			for i, ceiling := range []Value{Absent, Never, OnRequest, Always} {
				t.Run(string(kind)+"/"+string(row.ask)+"/"+string(ceiling), func(t *testing.T) {
					cell, table := "list", ""
					if kind == Write {
						cell, table = "put", "all"
					}
					p := fixturePolicy(t, []Rule{fixtureRule("", cell, table, kind, ceiling)}, []Rule{fixtureRule("", cell, table, kind, row.ask)}, nil)
					op := Operation{Source: "fixture", Kind: kind, Cell: cell, Chain: []Resource{{Type: "object", ID: "s3://b/team/key"}, {Type: "location", ID: "s3://b"}}}
					got := p.Decide([]Operation{op}, "/workspace", nil)
					if got.Decision != row.want[i] || got.Operations[0].Table != table {
						t.Fatalf("decision=%+v want=%s table=%s", got, row.want[i], table)
					}
				})
			}
		}
	}
}

func TestScopedBrokerPolicyGoverningLayersAndRouteAreIndependent(t *testing.T) {
	ceilings := []Rule{fixtureRule("", "list", "", Read, Never), fixtureRule("s3://b/team/", "list", "", Read, Always)}
	asks := []Rule{fixtureRule("", "", "", Read, True), fixtureRule("s3://b", "list", "", Read, False)}
	routes := []Route{{Source: "fixture", Scope: "s3://b", Context: "host", Origin: "user route"}}
	p := fixturePolicy(t, ceilings, asks, routes)
	op := Operation{Source: "fixture", Kind: Read, Cell: "list", Chain: []Resource{{Type: "object", ID: "s3://b/team/key"}, {Type: "location", ID: "s3://b"}}}
	got := p.Decide([]Operation{op}, "", nil)
	if got.Decision != Refuse || got.Operations[0].Ceiling.Scope != "s3://b/team/" || got.Operations[0].Ask.Scope != "s3://b" {
		t.Fatalf("governing lines did not remain final per layer: %+v", got)
	}
	if route, ok := p.RouteFor(op); !ok || route.Context != "host" || route.Scope != "s3://b" {
		t.Fatalf("route was incorrectly taken from the rule scope: %+v %v", route, ok)
	}
	asks[1] = fixtureRule("s3://b", "list", "", Read, True)
	if got := p.Decide([]Operation{op}, "", nil); got.Decision != Refuse {
		t.Fatal("caller mutation changed frozen policy")
	}
}

func TestScopedBrokerPolicyChainPositionBeforeScopeLength(t *testing.T) {
	matcher, _ := NewScopeMatcher("exact", "", nil)
	cells := []Cell{{Name: "list", Kind: Read}}
	ceilings := []Rule{fixtureRule("very-long-parent-resource", "list", "", Read, Never), fixtureRule("leaf", "list", "", Read, Always)}
	asks := []Rule{fixtureRule("leaf", "", "", Read, False), fixtureRule("leaf", "list", "", Read, True)}
	p, err := NewPolicy("fixture", matcher, cells, false, false, ceilings, asks, nil)
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{Source: "fixture", Kind: Read, Cell: "list", Chain: []Resource{{ID: "leaf"}, {ID: "very-long-parent-resource"}}}
	got := p.Decide([]Operation{op}, "", nil)
	if got.Decision != Run || got.Operations[0].Ceiling.Scope != "leaf" || got.Operations[0].Ask.Cell != "list" {
		t.Fatalf("chain position or named read priority lost: %+v", got)
	}
}

func TestScopedBrokerPolicyStrictBundleAndSingleTable(t *testing.T) {
	p := fixturePolicy(t, []Rule{fixtureRule("", "list", "", Read, Never), fixtureRule("", "put", "all", Write, Always), fixtureRule("", "put", "working_set", Write, OnRequest)}, nil, nil)
	chain := []Resource{{Type: "object", ID: "s3://b/key"}, {Type: "location", ID: "s3://b"}}
	read := Operation{Source: "fixture", Kind: Read, Cell: "list", Chain: chain}
	write := Operation{Source: "fixture", Kind: Write, Cell: "put", Chain: chain}
	members := []Membership{{Workspace: "ws", Source: "fixture", Type: "location", ID: "s3://b", Live: true}}
	got := p.Decide([]Operation{write, read}, "ws", members)
	if got.Decision != Refuse || got.Operations[0].Decision != Hold || got.Operations[0].Table != "working_set" || got.Operations[1].Table != "" {
		t.Fatalf("bundle/table/read independence lost: %+v", got)
	}
	for _, mutation := range []Membership{
		{Workspace: "ws", Source: "fixture", Type: "location", ID: "s3://b", Live: true, Call: true},
		{Workspace: "other", Source: "fixture", Type: "location", ID: "s3://b", Live: true},
		{Workspace: "ws", Source: "other", Type: "location", ID: "s3://b", Live: true},
		{Workspace: "ws", Source: "fixture", Type: "location", ID: "s3://b"},
	} {
		if table := SelectWriteTable(write, "ws", []Membership{mutation}); table != "all" {
			t.Fatalf("non-working membership picked %s", table)
		}
	}
	if got := p.Decide([]Operation{write}, "ws", members); got.Decision != Hold || len(got.Operations) != 1 {
		t.Fatal("one ask must report one held bundle")
	}
}

func TestScopedBrokerCeilingNeverOnlyException(t *testing.T) {
	for _, tc := range []struct {
		always, ask, never []string
		shipped, want      Value
	}{
		{[]string{"all"}, []string{"all.put"}, nil, Always, Always},
		{[]string{"all.put"}, []string{"all.put"}, nil, Always, Always},
		{[]string{"all.put"}, nil, []string{"all"}, Absent, Always},
		{nil, []string{"all.put"}, []string{"all"}, Absent, OnRequest},
		{[]string{"all"}, nil, []string{"all.put"}, Always, Never},
		{nil, nil, []string{"all"}, Absent, Never},
	} {
		if got := CeilingValue("all.put", "all", tc.always, tc.ask, tc.never, tc.shipped); got != tc.want {
			t.Fatalf("case=%+v got=%s", tc, got)
		}
	}
	if got := CeilingValue("working_set.put", "working_set", nil, nil, []string{"all"}, Absent); got != Absent {
		t.Fatal("never all was incorrectly expanded to working_set")
	}
}

func TestScopedBrokerPolicyNoInventedDefaultOrSourceAuthority(t *testing.T) {
	matcher, _ := NewScopeMatcher("exact", "", nil)
	for _, native := range []bool{false, true} {
		cells := []Cell{{Name: "comment", Kind: Write, Tables: []string{"all"}, Defaults: map[string]bool{"all": true}, DefaultWithoutCeiling: native}}
		p, err := NewPolicy("fixture", matcher, cells, false, false, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		op := Operation{Source: "fixture", Kind: Write, Cell: "comment", Chain: []Resource{{ID: "root"}}}
		want := Refuse
		if native {
			want = Run
		}
		if got := p.Decide([]Operation{op}, "", nil); got.Decision != want {
			t.Fatalf("native=%v got=%+v", native, got)
		}
	}
	p, err := NewPolicy("fixture", matcher, nil, false, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide([]Operation{{Source: "fixture", Kind: Read, Chain: []Resource{{ID: "root"}}}}, "", nil); got.Decision != Refuse {
		t.Fatal("zero cells without a read grant acquired reads")
	}
	p = fixturePolicy(t, []Rule{fixtureRule("s3://", "list", "", Read, Always)}, nil, nil)
	p.off = true
	if got := p.Decide([]Operation{{Source: "fixture", Kind: Read, Cell: "list", Chain: []Resource{{ID: "s3://b/key"}}}}, "", nil); got.Decision != Refuse {
		t.Fatal("narrow allow overrode source off")
	}
	before := []Rule{fixtureRule("", "list", "", Read, Always)}
	p = fixturePolicy(t, before, nil, nil)
	before[0].Value = Never
	if !reflect.DeepEqual(p.ceilings[0].Value, Always) {
		t.Fatal("rule aliases caller memory")
	}
}
