package config

import "testing"

func TestEffectiveCallBrokerCellsPreservesRequestCeilingAndDecision(t *testing.T) {
	b := callBrokerCellFixture(t)
	cases := []struct {
		name, user, workspace string
		working, all          CallBrokerTablePolicy
	}{
		{"default off", `{}`, `{}`, CallBrokerTablePolicy{"true", "off", "ask"}, CallBrokerTablePolicy{"absent", "off", "ask"}},
		{"default on request", `{"allow":{"local-tasks":{"on_request":["working_set.rerun_workflows"]}}}`, `{}`, CallBrokerTablePolicy{"true", "on_request", "run"}, CallBrokerTablePolicy{"absent", "off", "ask"}},
		{"explicit ask wins always", `{"allow":{"local-tasks":{"always":["all.rerun_workflows"]}}}`, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":"ask"}}}}`, CallBrokerTablePolicy{"ask", "always", "ask"}, CallBrokerTablePolicy{"absent", "always", "run"}},
		{"explicit false wins always", `{"allow":{"local-tasks":{"always":["all.rerun_workflows"]}}}`, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":false},"all":{"rerun_workflows":false}}}}`, CallBrokerTablePolicy{"false", "always", "refuse"}, CallBrokerTablePolicy{"false", "always", "refuse"}},
		{"request on request", `{"allow":{"local-tasks":{"on_request":["all.rerun_workflows"]}}}`, `{"requests":{"local-tasks":{"all":{"rerun_workflows":true}}}}`, CallBrokerTablePolicy{"true", "on_request", "run"}, CallBrokerTablePolicy{"true", "on_request", "run"}},
		{"true outside ceiling counts absent", `{}`, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":true}}}}`, CallBrokerTablePolicy{"true", "off", "ask"}, CallBrokerTablePolicy{"absent", "off", "ask"}},
		{"false off ceiling still refuses", `{}`, `{"requests":{"local-tasks":{"all":{"rerun_workflows":false}}}}`, CallBrokerTablePolicy{"true", "off", "ask"}, CallBrokerTablePolicy{"false", "off", "refuse"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EffectiveCallBrokerCells("local-tasks", b, testConfigMap(t, tc.user), testConfigMap(t, tc.workspace))
			if err != nil {
				t.Fatal(err)
			}
			cell := got["rerun_workflows"].Tables
			if len(cell) != 2 || cell["working_set"] != tc.working || cell["all"] != tc.all {
				t.Fatalf("effective=%+v", cell)
			}
		})
	}
	for _, request := range []string{
		`{"working_set":{"rerun_workflows":false},"all":{"rerun_workflows":"ask"}}`,
		`{"working_set":{"rerun_workflows":"ask"},"all":{"rerun_workflows":true}}`,
		`{"working_set":{"rerun_workflows":false}}`,
	} {
		_, err := EffectiveCallBrokerCells("local-tasks", b, testConfigMap(t, `{}`),
			testConfigMap(t, `{"requests":{"local-tasks":`+request+`}}`))
		if err == nil {
			t.Fatalf("inverted request %s must refuse before snapshot", request)
		}
	}
	_, err := EffectiveCallBrokerCells("local-tasks", b, testConfigMap(t, `{}`),
		testConfigMap(t, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":false},"all":{"rerun_workflows":true}}}}`))
	if err == nil {
		t.Fatal("inverted request must refuse before snapshot")
	}
	_, err = EffectiveCallBrokerCells("local-tasks", b, testConfigMap(t, `{}`),
		testConfigMap(t, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":123}}}}`))
	if err == nil {
		t.Fatal("invalid request must refuse before snapshot")
	}
	b.Cells[0].Tables = []string{"all"}
	got, err := EffectiveCallBrokerCells("local-tasks", b, testConfigMap(t, `{"allow":{"local-tasks":{"on_request":["all.rerun_workflows"]}}}`),
		testConfigMap(t, `{"requests":{"local-tasks":{"all":{"rerun_workflows":true}}}}`))
	if err != nil || len(got["rerun_workflows"].Tables) != 1 || got["rerun_workflows"].Tables["all"].Decision != "run" {
		t.Fatalf("undeclared table reached snapshot: %+v, %v", got, err)
	}
}
