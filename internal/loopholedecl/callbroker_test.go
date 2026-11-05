package loopholedecl

import (
	"strings"
	"testing"
)

func TestCallBrokerTypedHTTPNativeLookupDeclaration(t *testing.T) {
	valid := strings.Replace(callBrokerFixture, `"source":"local-tasks"`, `"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`, 1)
	valid = strings.Replace(valid, `"cli":["task","get","{id}"]`, `"http_method":"GET","http_path":"/items/{id}","http_resource":"task","http_id_pointer":"/id","http_parent_pointer":"/project/id"`, 1)
	manifest, err := callBrokerManifest(valid)
	if err != nil || len(manifest.CallBroker.Lookups) != 1 || manifest.CallBroker.Lookups[0].HTTPParentPointer != "/project/id" {
		t.Fatalf("typed HTTP native mapping not decoded: manifest=%+v err=%v", manifest, err)
	}
	for _, bad := range []string{`"http_id_pointer":"/id"`, `"http_parent_pointer":"/project/id"`, `"http_method":"GET"`, `"http_resource":"task"`} {
		altered := strings.Replace(valid, bad, strings.SplitN(bad, ":", 2)[0]+":true", 1)
		if _, err := callBrokerManifest(altered); err == nil {
			t.Errorf("bad native mapping %q was accepted", bad)
		}
	}
	for _, route := range []string{"/items/{id}?page=2", "https://evil.example/{id}", "/items/{id}/{id}", "/items/../{id}"} {
		altered := strings.Replace(valid, `"http_path":"/items/{id}"`, `"http_path":"`+route+`"`, 1)
		if _, err := callBrokerManifest(altered); err == nil {
			t.Errorf("unsafe typed HTTP lookup route %q loaded", route)
		}
	}
	duplicate := strings.Replace(valid, `"lookup":[`, `"lookup":[{"name":"other-task-project","http_method":"GET","http_path":"/other/{id}","http_resource":"task","http_id_pointer":"/id","http_parent_pointer":"/project/id"},`, 1)
	if _, err := callBrokerManifest(duplicate); err == nil {
		t.Fatal("conflicting native mapping for the same resource loaded")
	}
	invalid := strings.Replace(valid, `"http_parent_pointer":"/project/id"`, `"http_parent_pointer":"/project/~9id"`, 1)
	if _, err := callBrokerManifest(invalid); err == nil {
		t.Fatal("invalid RFC 6901 pointer loaded")
	}
}

func TestCallBrokerHTTPDeclaration(t *testing.T) {
	valid := strings.Replace(callBrokerFixture,
		`"source":"local-tasks"`,
		`"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`,
		1)
	if _, err := callBrokerManifest(valid); err != nil {
		t.Fatalf("selected HTTP declaration must load: %v", err)
	}
	if _, err := callBrokerManifest(callBrokerFixture); err == nil {
		t.Fatal("HTTP front missing its binding loaded")
	}
	unknown := strings.Replace(valid, `"request_headers":["Content-Type"]`,
		`"request_headers":["Content-Type"],"unapproved_header":"X-Token"`, 1)
	if _, err := callBrokerManifest(unknown); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown nested HTTP key: %v; want refusal", err)
	}
}

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
	m, err := callBrokerManifest(strings.Replace(callBrokerFixture, `"source":"local-tasks"`, `"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`, 1))
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
			_, err := callBrokerManifest(strings.Replace(tc.body, `"source":"local-tasks"`, `"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`, 1))
			if tc.name == "duplicate declaration key" {
				_, err = callBrokerManifest(tc.body)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCallBrokerHTTPBindingRefusesMalformedDeclaration(t *testing.T) {
	valid := strings.Replace(callBrokerFixture, `"source":"local-tasks"`, `"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`, 1)
	for _, tc := range []struct{ name, from, to string }{
		{"duplicate JSON", `"origin":"https://fixture.example"`, `"origin":"https://fixture.example","origin":"https://other.example"`},
		{"wrong credential type", `"key":"TASKS_TOKEN"`, `"key":55`},
		{"credential source", `"source":"host_env"`, `"source":"runtime"`},
		{"unapproved variable", `"key":"TASKS_TOKEN"`, `"key":"OTHER_TOKEN"`},
		{"missing approval", `"env_from_host":["TASKS_TOKEN"]`, `"env_from_host":[]`},
		{"userinfo", `https://fixture.example`, `https://user@fixture.example`},
		{"path", `https://fixture.example`, `https://fixture.example/path`},
		{"query", `https://fixture.example`, `https://fixture.example?token=unsafe`},
		{"fragment", `https://fixture.example`, `https://fixture.example#unsafe`},
		{"wildcard", `https://fixture.example`, `https://*.example`},
		{"non HTTPS", `https://fixture.example`, `http://fixture.example`},
		{"IPv4 literal", `https://fixture.example`, `https://127.0.0.1`},
		{"IPv6 literal", `https://fixture.example`, `https://[::1]`},
		{"null allowlist", `"request_headers":["Content-Type"]`, `"request_headers":null`},
		{"Unicode C1 prefix", `"prefix":"Bearer "`, `"prefix":"\u0085"`},
		{"invalid port", `https://fixture.example`, `https://fixture.example:65536`},
		{"header host", `"header":"Authorization"`, `"header":"Host"`},
		{"header cookie", `"header":"Authorization"`, `"header":"Cookie"`},
		{"header proxy", `"header":"Authorization"`, `"header":"Proxy-Authorization"`},
		{"header hop", `"header":"Authorization"`, `"header":"Connection"`},
		{"header yolo", `"header":"Authorization"`, `"header":"Yolo-Verdict"`},
		{"allowlist authorization", `"Content-Type"`, `"Authorization"`},
		{"allowlist duplicate", `"Content-Type"`, `"Content-Type","content-type"`},
		{"allowlist hop", `"Content-Type"`, `"Transfer-Encoding"`},
		{"prefix long", `"prefix":"Bearer "`, `"prefix":"` + strings.Repeat("a", 129) + `"`},
		{"prefix control", `"prefix":"Bearer "`, `"prefix":"Bearer \n"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := callBrokerManifest(strings.Replace(valid, tc.from, tc.to, 1)); err == nil {
				t.Fatal("malformed binding loaded")
			}
		})
	}
	fenced := strings.Replace(valid, `"source":"host_env","key":"TASKS_TOKEN"`, `"source":"fenced_file","key":"/home/agent/credential"`, 1)
	fenced = strings.Replace(fenced, `"credential_paths":["~/.config/tasks"]`, `"credential_paths":["/home/agent/credential"]`, 1)
	if _, err := callBrokerManifest(fenced); err != nil {
		t.Fatalf("absolute declared file metadata must load: %v", err)
	}
	for _, path := range []string{`relative/credential`, `/home/agent/../credential`, `/home/agent/other`} {
		if _, err := callBrokerManifest(strings.Replace(fenced, `"key":"/home/agent/credential"`, `"key":"`+path+`"`, 1)); err == nil {
			t.Fatalf("unapproved/nonabsolute credential %q loaded", path)
		}
	}
}
