package loopholedecl

import (
	"strings"
	"testing"
)

const callBrokerFixture = `"call_broker": {
  "source":"local-tasks", "front":"http", "program":"tasks",
  "classifier":["{program:tasks-classifier}"], "executor":"{program:tasks-executor}",
  "credential_paths":["~/.config/tasks"], "env_from_host":["TASKS_TOKEN"],
  "scope":{"roots":"declared"},
  "resource":[{"type":"project"},{"type":"task","parent":"project"}],
  "cell":[{"name":"rerun_workflows","tables":["working_set","all"],"default":{"working_set":true}}],
  "lookup":[{"name":"task-project","cli":["task","get","{id}"]}],
  "read_over_post":["/graphql"],
  "joins_working_set":[{"cell":"rerun_workflows"},{"follows":{"source":"git","fact":"branch"}}]
}`

func callBrokerManifest(body string) (*Manifest, error) {
	return Decode([]byte(`{"name":"tasks","transport":"loopback-tls","lifecycle":"spawned",`+body+`}`), "/packs/fixture/loopholes/tasks")
}

func TestCallBrokerManifestGateAndScope(t *testing.T) {
	m, err := callBrokerManifest(callBrokerFixture)
	if err != nil {
		t.Fatalf("decode selected third-party call broker: %v", err)
	}
	cb := m.CallBroker
	if cb == nil || cb.Source != "local-tasks" || cb.Scope.Roots != "declared" || cb.Cells[0].Default["working_set"] != true {
		t.Fatalf("CallBroker = %+v", cb)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"duplicate declaration key", strings.Replace(callBrokerFixture, `"source":"local-tasks"`, `"source":"local-tasks","source":"other"`, 1), "duplicate key"},
		{"unknown block key", strings.Replace(callBrokerFixture, `"scope":{"roots":"declared"}`, `"scope":{"roots":"declared"},"daemon_command":["sh"]`, 1), "unknown key"},
		{"read write cell", strings.Replace(callBrokerFixture, `"name":"rerun_workflows"`, `"name":"read"`, 1), "must not be 'read'"},
		{"missing parent", strings.Replace(callBrokerFixture, `"parent":"project"`, `"parent":"missing"`, 1), "undeclared resource"},
		{"wrong-typed resource parent", strings.Replace(callBrokerFixture, `"parent":"project"`, `"parent":true`, 1), "must be a string"},
		{"wrong-typed cell max_for", strings.Replace(callBrokerFixture, `"tables":["working_set","all"],`, `"tables":["working_set","all"],"max_for":true,`, 1), "duration string such as 8h"},
		{"legacy host daemon cannot coexist", `"host_daemon":{"cmd":["yolo","internal","daemon","x"]},` + callBrokerFixture, "replaces 'host_daemon'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := callBrokerManifest(tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}
