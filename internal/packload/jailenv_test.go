package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestJailOnlyEnvIsFilteredBeforeTheHostFold(t *testing.T) {
	p := &Pack{Name: "test", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"agent","via":"npm","package":"test-agent"},
	  {"kind":"provider","name":"provider"},
	  {"kind":"env","vars":{"SHARED":"ordinary","NORMAL":"yes"}},
	  {"kind":"env","jail_only":true,"vars":{"SHARED":"jail","JAIL":"yes"}},
	  {"kind":"env","profile":"p","jail_only":true,"vars":{"GATED":"yes"}},
	  {"kind":"env","platform":"test-platform","jail_only":true,"vars":{"PLATFORM":"yes"}}
	]}`)}
	providers, err := ComposeProviders(nil, []*Pack{p})
	if err != nil {
		t.Fatal(err)
	}
	for _, atHost := range []bool{false, true} {
		t.Run(map[bool]string{false: "jail", true: "host"}[atHost], func(t *testing.T) {
			scope, err := ScopeCredentials(ScopeInput{
				Packs: []*Pack{p}, AtHost: atHost,
				Profiles:  map[string]string{"agent": "p"},
				Resolved:  map[string]ResolvedProfile{"p": {Provider: "provider"}},
				Providers: providers,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Check the shared channel, the selected agent and the on-demand fold.
			for _, agent := range []string{"", "agent", "other"} {
				_, delivered := scope.EnvFor(agent).Value("JAIL")
				if delivered == atHost {
					t.Errorf("%q: composed JAIL delivery atHost=%v: %v", agent, atHost, delivered)
				}
				got := map[string]string{}
				for _, e := range scope.FoldFor(agent) {
					got[e.Key] = e.Value
				}
				want := "jail"
				if atHost {
					want = "ordinary"
				}
				if got["SHARED"] != want || got["NORMAL"] != "yes" {
					t.Errorf("%q: fold = %v, want SHARED=%s and NORMAL=yes", agent, got, want)
				}
				if (got["JAIL"] == "yes") == atHost {
					t.Errorf("%q: JAIL delivery atHost=%v: %v", agent, atHost, got)
				}
				if agent == "agent" && (got["GATED"] == "yes") == atHost {
					t.Errorf("gated delivery atHost=%v: %v", atHost, got)
				}
			}
		})
	}
	// The platform gate must obey jail_only as well as its provider selection.
	sel := GateSelection{
		Profiles:  map[string]string{"agent": "p"},
		Platforms: map[string]string{"agent": "test-platform"},
	}
	for _, atHost := range []bool{false, true} {
		found := false
		for _, e := range envFold([]*Pack{p}, sel, "agent", atHost) {
			found = found || e.Key == "PLATFORM"
		}
		if found == atHost {
			t.Errorf("platform-gated delivery atHost=%v: found=%v", atHost, found)
		}
	}
}

func TestPiPackCopiesThroughTheTerminalInJails(t *testing.T) {
	packs := Embedded()
	var pi *Pack
	for _, p := range packs {
		if p.Name == "pi" {
			pi = p
			break
		}
	}
	if pi == nil {
		t.Fatal("shipped pi pack not found")
	}
	if got := EnvVarsFor([]*Pack{pi}, GateSelection{}, "pi")["PI_CLIPBOARD_OSC52"]; got != "1" {
		t.Fatalf("jailed pi clipboard setting = %q, want 1", got)
	}
	for _, c := range pi.Decl.Contributions() {
		if c.Kind == packdecl.KindEnv && c.Vars["PI_CLIPBOARD_OSC52"] == "1" && !c.JailOnly {
			t.Fatal("pi clipboard setting must be jail-only")
		}
	}
}
