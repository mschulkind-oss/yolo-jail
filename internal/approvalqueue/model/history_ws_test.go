package model

import "testing"

func TestHistoryEntriesCarryWorkspaceAndJail(t *testing.T) {
	r := req("r-1", "pending")
	r.Workspace, r.Jail = "/home/example/code/app", "jail1"
	g := grant("g-1", "r-1")
	g.Workspace, g.Jail = "/home/example/code/app", "jail1"
	m := snap(New(t0), at(1), []RequestView{r, req("r-2", "pending")}, []GrantView{g})
	d := r
	d.State = "allowed"
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &d}}, at(5))
	m = snap(m, at(20), nil, nil)
	want := map[string][2]string{
		"r-1": {"/home/example/code/app", "jail1"}, // from the event
		"g-1": {"/home/example/code/app", "jail1"}, // from the vanished grant
		"r-2": {"", ""},
	}
	seen := 0
	for _, e := range m.History {
		if w, ok := want[e.Key]; ok {
			seen++
			if e.Workspace != w[0] || e.Jail != w[1] {
				t.Errorf("%s: workspace/jail = %q/%q", e.Key, e.Workspace, e.Jail)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("history entries seen: %d", seen)
	}
	// A vanished request keeps its workspace, and the page's view carries it.
	m2 := snap(New(t0), at(1), []RequestView{r}, nil)
	m2 = snap(m2, at(20), nil, nil)
	p := Publish(m2, at(21), Options{})
	if len(p.History.Entries) != 1 || p.History.Entries[0].Workspace != "/home/example/code/app" || p.History.Entries[0].Jail != "jail1" {
		t.Fatalf("%+v", p.History.Entries)
	}
	// A later observed outcome that names no workspace keeps the one already known.
	d2 := req("r-1", "denied")
	m2 = Reduce(m2, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(6), Request: &d2}}, at(22))
	if h := m2.History[len(m2.History)-1]; h.Status != StDenied || h.Workspace != "/home/example/code/app" {
		t.Fatalf("%+v", h)
	}
}
