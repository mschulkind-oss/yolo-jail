package packdecl

import (
	"strings"
	"testing"
)

// AN ADAPTER MAY FRONT A PLATFORM, and only beside a service (AdapterPair.FromPlatforms;
// docs/design/wire-bridge-gateway.md WG-I39): the platforms whose providers name no `from`
// address, which the pack's own daemon reaches itself. A pack with no daemon cannot, an empty
// list fronts nothing, and each entry has a platform's shape. The accessor carries the list,
// since packload.Adaptations reads it there.
func TestAnAdapterFrontsAPlatformOnlyBesideItsService(t *testing.T) {
	svc := `{"kind":"service","name":"br","jail_daemon":{"cmd":["yolo-jaild","br"]}}`
	ok := `{"name":"acme","contributes":[` + svc + `,
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic","from_platforms":["aws-bedrock"]},"address":"http://127.0.0.1:1"}]}`
	m, probs := Decode([]byte(ok))
	if len(probs) != 0 {
		t.Fatalf("a service's adapter fronting a platform must validate: %v", probs)
	}
	if a := m.Adapters(); len(a) != 1 || len(a[0].FromPlatforms) != 1 || a[0].FromPlatforms[0] != "aws-bedrock" {
		t.Errorf("Adapters() = %+v, want from_platforms carried", a)
	}
	for _, tc := range []struct{ raw, want string }{
		{`{"name":"acme","contributes":[
		  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic","from_platforms":["aws-bedrock"]},"address":"https://gw.example/v1"}]}`,
			"needs a `service` in the same pack"},
		{`{"name":"acme","contributes":[` + svc + `,
		  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic","from_platforms":[]},"address":"http://127.0.0.1:1"}]}`,
			"empty list"},
		{`{"name":"acme","contributes":[` + svc + `,
		  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic","from_platforms":["aws bedrock"]},"address":"http://127.0.0.1:1"}]}`,
			"carries whitespace"},
	} {
		_, probs := Decode([]byte(tc.raw))
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Errorf("want a problem containing %q, got %v", tc.want, probs)
		}
	}
}
