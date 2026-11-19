package mcpsignin

import "testing"

func TestResolveManagedMCPSignInUsesDeliveredIdentityAndCompositionOrder(t *testing.T) {
	native := func(name, key string) (string, error) { return "plugin:" + name + ":" + key, nil }
	agent := Agent{Name: "fixture", Formats: []string{"claude-plugin"}, Capturable: true, NativeName: native}
	server := Once{Name: "server", URL: "https://fixture.invalid/mcp", Transport: "http"}
	bundle := Bundle{Pack: "first", Agent: "fixture", Format: "claude-plugin", ManifestName: "team-original", Placement: "placed", Origin: "https://example.invalid/original", Revision: "0123456789abcdef0123456789abcdef01234567", CallbackRoute: true, Servers: []Server{{Key: "native-key", URL: server.URL, Transport: "http", ClientID: "public-client", CallbackPort: 3118}}}
	for _, tc := range []struct {
		name   string
		change func(*Once, *Bundle, *Agent)
		reason string
	}{
		{name: "delivered namespace"},
		{name: "own client equal", change: func(s *Once, _ *Bundle, _ *Agent) { s.ExpectedClientID = "public-client" }},
		{name: "own client mismatch", change: func(s *Once, _ *Bundle, _ *Agent) { s.ExpectedClientID = "owner-client" }, reason: "client mismatch"},
		{name: "not delivered", change: func(_ *Once, b *Bundle, _ *Agent) { b.Placement = "not delivered" }, reason: "not delivered"},
		{name: "unsupported shape", change: func(_ *Once, b *Bundle, _ *Agent) {
			b.Servers = append([]Server(nil), b.Servers...)
			b.Servers[0].Reason = "unsupported shape"
		}, reason: "unsupported shape"},
		{name: "wrong URL", change: func(s *Once, _ *Bundle, _ *Agent) { s.URL = "https://attacker.invalid/mcp" }, reason: "no sign-in point"},
		{name: "wrong transport", change: func(s *Once, _ *Bundle, _ *Agent) { s.Transport = "sse" }, reason: "no sign-in point"},
		{name: "unknown format", change: func(_ *Once, _ *Bundle, a *Agent) { a.Formats = nil }, reason: "no sign-in point"},
		{name: "no source", change: func(_ *Once, _ *Bundle, a *Agent) { a.Capturable = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b, a := server, bundle, agent
			if tc.change != nil {
				tc.change(&s, &b, &a)
			}
			decisions := Resolve([]Once{s}, []Bundle{b}, []Agent{a})
			if len(decisions) != 1 {
				t.Fatal(decisions)
			}
			d := decisions[0]
			if d.Reason != tc.reason || (d.Point == nil) != (tc.reason != "") || d.Capturable != a.Capturable {
				t.Fatalf("decision=%+v", d)
			}
			if d.Point != nil && (d.Point.NativeName != "plugin:team-original:native-key" || !d.Point.CallbackRoute || d.Point.CallbackPort != 3118) {
				t.Fatalf("delivered name or declared callback lost: %+v", d.Point)
			}
		})
	}
	second := bundle
	second.Pack = "second"
	second.ManifestName = "second-original"
	d := Resolve([]Once{server}, []Bundle{bundle, second}, []Agent{agent})[0]
	if d.Point == nil || d.Point.Pack != "first" || len(d.Alternatives) != 1 || d.Alternatives[0].Pack != "second" {
		t.Fatalf("composition tie-break lost: %+v", d)
	}
	if got := Resolve(nil, []Bundle{bundle}, []Agent{agent}); len(got) != 0 {
		t.Fatal("removed/non-once server acquired a point")
	}
}
