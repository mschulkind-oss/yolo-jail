package callbrokerconformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCheckClassificationsRejectsNondeterminism(t *testing.T) {
	calls := 0
	classify := func(context.Context, Call) (json.RawMessage, error) {
		calls++
		if calls == 1 {
			return json.RawMessage(`{"argv":["read","a"]}`), nil
		}
		return json.RawMessage(`{"argv":["read","b"]}`), nil
	}
	err := CheckClassifications(context.Background(), classify, []ClassificationCase{
		{Name: "R1", Call: Call{Argv: []string{"read", "a"}}, Want: json.RawMessage(`{"argv":["read","a"]}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "R1") || calls != 2 {
		t.Fatalf("drift accepted: err=%v calls=%d", err, calls)
	}
}

func TestCheckClassifications(t *testing.T) {
	good := json.RawMessage(`{"argv":["a","b"],"n":1}`)
	for _, tc := range []struct {
		name  string
		cases []ClassificationCase
		fn    Classify
		bad   bool
	}{
		{"canonical JSON", []ClassificationCase{{Name: "row", Want: good}}, func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{ "n":1.0, "argv":["a","b"] }`), nil
		}, false},
		{"exact exponent normalization", []ClassificationCase{{Name: "row", Want: json.RawMessage(`[1000,0,-0.0100]`)}}, func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`[1e3,-0.0,-1e-2]`), nil }, false},
		{"huge exponent", []ClassificationCase{{Name: "row", Want: json.RawMessage(`1e999999999999999999999`)}}, func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`10e999999999999999999998`), nil
		}, false},
		{"equally wrong", []ClassificationCase{{Name: "row", Want: good}}, func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{"argv":["a","c"],"n":1}`), nil
		}, true},
		{"distinct large JSON numbers", []ClassificationCase{{Name: "row", Want: json.RawMessage(`{"n":9007199254740992}`)}}, func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{"n":9007199254740993}`), nil
		}, true},
		{"array order", []ClassificationCase{{Name: "row", Want: good}}, func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{"argv":["b","a"],"n":1}`), nil
		}, true},
		{"negative", []ClassificationCase{{Name: "row", ErrorContains: "invalid"}}, func(context.Context, Call) (json.RawMessage, error) { return nil, errors.New("invalid schema") }, false},
		{"wrong error", []ClassificationCase{{Name: "row", ErrorContains: "invalid"}}, func(context.Context, Call) (json.RawMessage, error) { return nil, errors.New("other") }, true},
		{"success with error", []ClassificationCase{{Name: "row", Want: good}}, func(context.Context, Call) (json.RawMessage, error) { return good, errors.New("invalid") }, true},
		{"negative succeeds", []ClassificationCase{{Name: "row", ErrorContains: "invalid"}}, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
		{"malformed answer", []ClassificationCase{{Name: "row", Want: good}}, func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`{`), nil }, true},
		{"empty", nil, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
		{"nil classifier", []ClassificationCase{{Name: "row", Want: good}}, nil, true},
		{"empty name", []ClassificationCase{{Want: good}}, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
		{"malformed expectation", []ClassificationCase{{Name: "row", Want: json.RawMessage(`{`)}}, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
		{"missing expectation", []ClassificationCase{{Name: "row"}}, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
		{"ambiguous", []ClassificationCase{{Name: "row", Want: good, ErrorContains: "invalid"}}, func(context.Context, Call) (json.RawMessage, error) { return good, nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckClassifications(context.Background(), tc.fn, tc.cases); (err != nil) != tc.bad {
				t.Fatalf("err=%v bad=%v", err, tc.bad)
			}
		})
	}
}

func TestCheckClassificationsNegativeRunsTwice(t *testing.T) {
	calls := 0
	err := CheckClassifications(context.Background(), func(context.Context, Call) (json.RawMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("invalid")
		}
		return json.RawMessage(`{}`), nil
	}, []ClassificationCase{{Name: "negative", ErrorContains: "invalid"}})
	if err == nil || calls != 2 || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCheckSteps(t *testing.T) {
	for _, tc := range []struct {
		name        string
		observation Observation
		err         error
		step        Step
		bad         bool
	}{
		{name: "success", observation: Observation{Verdict: "runs", Output: "safe", Counts: Counts{Writes: 1}}, step: Step{WantVerdict: "runs", WantCounts: Counts{Writes: 1}, OutputContains: []string{"safe"}, OutputExcludes: []string{"secret"}}},
		{name: "verdict", observation: Observation{Verdict: "asks"}, step: Step{WantVerdict: "runs"}, bad: true},
		{name: "writes", observation: Observation{Counts: Counts{Writes: 1}}, bad: true},
		{name: "pending", observation: Observation{Counts: Counts{Pending: 1}}, bad: true},
		{name: "uses", observation: Observation{Counts: Counts{GrantUses: 1}}, bad: true},
		{name: "error with success", err: errors.New("bad"), bad: true},
		{name: "expected error", err: errors.New("bad schema"), step: Step{ErrorContains: "bad"}},
		{name: "missing error", step: Step{ErrorContains: "bad"}, bad: true},
		{name: "wrong error", err: errors.New("other"), step: Step{ErrorContains: "bad"}, bad: true},
		{name: "leak", observation: Observation{Output: "secret"}, step: Step{OutputExcludes: []string{"secret"}}, bad: true},
		{name: "missing output", step: Step{OutputContains: []string{"safe"}}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := tc.step
			s.Name = "step"
			if s.WantVerdict == "" {
				s.WantVerdict = "refused"
			}
			observed := tc.observation
			if observed.Verdict == "" {
				observed.Verdict = "refused"
			}
			s.Run = func(context.Context) (Observation, error) { calls++; return observed, tc.err }
			err := CheckSteps(context.Background(), []Step{s})
			if (err != nil) != tc.bad || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if err != nil && !strings.Contains(err.Error(), "step") {
				t.Fatal(err)
			}
		})
	}
	for _, steps := range [][]Step{nil, {{Name: "nil"}}, {{Run: func(context.Context) (Observation, error) { return Observation{}, nil }}}} {
		if CheckSteps(context.Background(), steps) == nil {
			t.Fatal("invalid script accepted")
		}
	}
	calls := 0
	err := CheckSteps(context.Background(), []Step{{Name: "first", WantVerdict: "runs", Run: func(context.Context) (Observation, error) { calls++; return Observation{}, nil }}, {Name: "second", Run: func(context.Context) (Observation, error) { calls++; return Observation{}, nil }}})
	if err == nil || calls != 1 {
		t.Fatalf("did not stop: %v calls=%d", err, calls)
	}
}

func TestCheckStepsRejectsMissingOrAmbiguousExpectation(t *testing.T) {
	for _, s := range []Step{
		{Name: "missing verdict"},
		{Name: "runs with error", WantVerdict: "runs", ErrorContains: "bad"},
		{Name: "approved with error", WantVerdict: "approved", ErrorContains: "bad"},
	} {
		t.Run(s.Name, func(t *testing.T) {
			calls := 0
			s.Run = func(context.Context) (Observation, error) {
				calls++
				return Observation{Verdict: s.WantVerdict}, errors.New("bad")
			}
			err := CheckSteps(context.Background(), []Step{s})
			if err == nil || calls != 0 || !strings.Contains(err.Error(), s.Name) {
				t.Fatalf("invalid expectation ran: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCheckClassificationsRejectsErrorNondeterminism(t *testing.T) {
	calls := 0
	err := CheckClassifications(context.Background(), func(context.Context, Call) (json.RawMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("invalid one")
		}
		return nil, errors.New("invalid two")
	}, []ClassificationCase{{Name: "negative drift", ErrorContains: "invalid"}})
	if err == nil || !strings.Contains(err.Error(), "negative drift") || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCheckStepsRunsInOrder(t *testing.T) {
	var order []string
	steps := []Step{}
	for _, name := range []string{"first", "second"} {
		steps = append(steps, Step{Name: name, WantVerdict: "runs", Run: func(context.Context) (Observation, error) {
			order = append(order, name)
			return Observation{Verdict: "runs"}, nil
		}})
	}
	if err := CheckSteps(context.Background(), steps); err != nil || strings.Join(order, ",") != "first,second" {
		t.Fatalf("err=%v order=%v", err, order)
	}
}
