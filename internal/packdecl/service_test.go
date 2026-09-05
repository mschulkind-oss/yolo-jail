package packdecl

import (
	"strings"
	"testing"
)

// service_test.go pins the `kind: "service"` vocabulary
// (docs/design/wire-bridge.md §2.1, WB-D16): the decode round-trip, the
// required fields, the anti-loophole refusal, and the name exclusivity that
// keeps two daemons off one endpoint file. The census pin itself lives in
// kinds_test.go (it must fail when the kind lands — that is the trap the plan
// names); everything shape-shaped is here.

// The full service half decodes and projects field-for-field — the §2.1
// service column exactly, and nothing from the loophole column.
func TestServiceDecodeRoundTrip(t *testing.T) {
	m, problems := Decode([]byte(`{
		"name": "svc",
		"contributes": [{
			"kind": "service",
			"name": "wire-bridge",
			"jail_daemon": {"cmd": ["yolo-jaild", "wire-bridge"], "restart": "on-failure"},
			"host_daemon": {"cmd": ["yolo", "internal", "daemon", "wire-bridge"]},
			"endpoint": "wire-bridge.endpoint",
			"platforms": ["linux/amd64"],
			"serves": ["anthropic-wire-bridge"],
			"settings": [{"key": "svc.verbose", "type": "bool", "default": false,
				"description": "log more"}]
		}]
	}`))
	if len(problems) != 0 {
		t.Fatalf("a complete service declaration must decode clean: %v", problems)
	}
	svcs := m.Services()
	if len(svcs) != 1 {
		t.Fatalf("Services() returned %d entries, want 1", len(svcs))
	}
	s := svcs[0]
	if s.Name != "wire-bridge" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.JailDaemon == nil || strings.Join(s.JailDaemon.Cmd, " ") != "yolo-jaild wire-bridge" ||
		s.JailDaemon.Restart != "on-failure" {
		t.Errorf("JailDaemon = %+v", s.JailDaemon)
	}
	// host_daemon is DECLARED AND CARRIED — validation accepts it and nothing
	// executes it in this build (the field's doc carries the why). This
	// assertion is what stops a future "nobody reads it, drop it" cleanup from
	// silently making the half unstateable.
	if s.HostDaemon == nil || len(s.HostDaemon.Cmd) != 4 {
		t.Errorf("HostDaemon must be carried: %+v", s.HostDaemon)
	}
	if s.Endpoint != "wire-bridge.endpoint" || len(s.Platforms) != 1 ||
		len(s.Serves) != 1 || len(s.Settings) != 1 || s.Settings[0].Key != "svc.verbose" {
		t.Errorf("service half did not round-trip: %+v", s)
	}
	// A restart of "always" and "no" are the supervisor's other two values.
	for _, restart := range []string{"always", "no"} {
		_, problems := Decode([]byte(`{"contributes":[{"kind":"service","name":"s",
			"jail_daemon":{"cmd":["x"],"restart":"` + restart + `"}}]}`))
		if len(problems) != 0 {
			t.Errorf("restart %q must be accepted (the supervisor's own enum): %v", restart, problems)
		}
	}
}

func TestServiceRefusals(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{"no name", `{"contributes":[{"kind":"service",
			"jail_daemon":{"cmd":["x"]}}]}`, `needs "name"`},
		{"no daemon", `{"contributes":[{"kind":"service","name":"s"}]}`,
			`needs "jail_daemon" or "host_daemon"`},
		{"unknown restart", `{"contributes":[{"kind":"service","name":"s",
			"jail_daemon":{"cmd":["x"],"restart":"sometimes"}}]}`, `unknown jail_daemon.restart`},
		{"empty jail cmd", `{"contributes":[{"kind":"service","name":"s",
			"jail_daemon":{"cmd":[]}}]}`, `needs a non-empty "cmd"`},
		{"empty host cmd", `{"contributes":[{"kind":"service","name":"s",
			"host_daemon":{"cmd":[]}}]}`, `needs a non-empty "cmd"`},
		{"endpoint with structure", `{"contributes":[{"kind":"service","name":"s",
			"jail_daemon":{"cmd":["x"]},"endpoint":"../escape.endpoint"}]}`,
			`must be a bare file name`},
		{"duplicate name in one pack", `{"contributes":[
			{"kind":"service","name":"s","jail_daemon":{"cmd":["a"]}},
			{"kind":"service","name":"s","jail_daemon":{"cmd":["b"]}}]}`,
			`is declared again`},
	}
	for _, tc := range cases {
		_, problems := Decode([]byte(tc.manifest))
		if len(problems) == 0 {
			t.Errorf("%s: Decode was silent — want a refusal containing %q", tc.name, tc.want)
			continue
		}
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, tc.want) {
			t.Errorf("%s: refusal %q does not contain %q", tc.name, joined, tc.want)
		}
	}
}

// THE ANTI-LOOPHOLE, as a test: `host` is the grant-shaped field — a read of
// the host home — and §2.1 says a service never carries one. The refusal names
// kind "loophole", which is where a boundary-crossing daemon belongs.
func TestServiceRefusesTheHostGrantField(t *testing.T) {
	_, problems := Decode([]byte(`{"contributes":[{"kind":"service","name":"s",
		"jail_daemon":{"cmd":["x"]},"host":".ssh/id_ed25519"}]}`))
	if len(problems) == 0 {
		t.Fatal("a service carrying a host grant must be refused")
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{`does not take "host"`, "loophole"} {
		if !strings.Contains(joined, want) {
			t.Errorf("refusal %q does not contain %q — the message must name the rule and the kind that does take the field", joined, want)
		}
	}
}
