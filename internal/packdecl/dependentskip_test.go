package packdecl

// dependentskip_test.go pins PF-D76 (docs/design/patched-forks.md): when a read skips a contribution
// this yolo cannot read, a readable sibling that cannot work without it is skipped too, with its own
// note, rather than failing a check that needs the skipped one present. The case is wire-bridge's
// shape: an adapter whose `adapts.from_platforms` is reached through the pack's own service, beside
// a service holding a field from a newer yolo. The host's use read and the jail's tolerant read keep
// the same contributions and say the same lines.

import (
	"reflect"
	"strings"
	"testing"
)

const dependentManifest = `{"name":"wb","contributes":[
  {"kind":"adapter","adapts":{"from":"openai","from_platforms":["aws-bedrock"],"to":"anthropic"},"address":"http://127.0.0.1:8214"},
  {"kind":"adapter","adapts":{"from":"openai-responses","to":"anthropic"},"address":"http://127.0.0.1:8215"},
  {"kind":"service","name":"wb","host_daemon":{"cmd":["wb"]},"via_address":"http://127.0.0.1:8216"FIELD}]}`

func TestASkippedServiceSkipsTheAdapterReachedThroughIt(t *testing.T) {
	skewed := []byte(strings.Replace(dependentManifest, "FIELD", `,"field_from_a_newer_yolo":1`, 1))
	m, problems, skipped := DecodeForUse(skewed)
	if len(problems) != 0 {
		t.Fatalf("the use read refuses the pack for a skipped service: %q", problems)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0], "contributes[2]: skipping the service contribution") ||
		!strings.HasPrefix(skipped[1], "contributes[0]: skipping the adapter contribution") ||
		!strings.Contains(skipped[1], "which this yolo skipped (contributes[2])") || !strings.Contains(skipped[1], "update yolo") {
		t.Errorf("the use read's notes: %q", skipped)
	}
	got := m.Contributions()
	if len(got) != 1 || got[0].Kind != KindAdapter || got[0].Adapts.From != "openai-responses" {
		t.Errorf("the use read keeps %+v, want the adapter that needs no service alone", got)
	}
	jail, jailProblems, jailSkipped := DecodeTolerant(skewed)
	if len(jailProblems) != 0 || !reflect.DeepEqual(jailSkipped, skipped) ||
		!reflect.DeepEqual(jail.Contributions(), got) {
		t.Errorf("the jail's read differs: problems %q, notes %q, kept %+v", jailProblems, jailSkipped, jail.Contributions())
	}
	// The control: with the service readable, the strict read keeps all three, with no problem.
	if m, problems := Decode([]byte(strings.Replace(dependentManifest, "FIELD", "", 1))); len(problems) != 0 ||
		len(m.Contributions()) != 3 {
		t.Errorf("the control: problems %q, kept %d", problems, len(m.Contributions()))
	}
}

// A SERVICE STILL KEPT keeps the adapter: one of two services skipped leaves the other to reach it.
func TestAnAdapterKeepsItsPlaceWhileAServiceRemains(t *testing.T) {
	two := strings.Replace(dependentManifest, "FIELD", `,"field_from_a_newer_yolo":1},`+
		`{"kind":"service","name":"wb2","host_daemon":{"cmd":["wb2"]},"via_address":"http://127.0.0.1:8217"`, 1)
	m, problems, skipped := DecodeForUse([]byte(two))
	if len(problems) != 0 || len(skipped) != 1 || len(m.Contributions()) != 3 {
		t.Errorf("problems %q, notes %q, kept %d; want the service alone skipped", problems, skipped, len(m.Contributions()))
	}
}
