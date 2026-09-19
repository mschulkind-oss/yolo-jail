package run

// providerforwardalias_test.go pins the CALL SITE that shipped the wire-bridge port
// collision: composedProviders runs, and localProviderForwards then reads the SAME
// `cfg.providers` pointer the composition was handed. For as long as the composer stored
// the user's entry by reference, that read saw the wire bridge's own 127.0.0.1:8214 and
// reported it as a host-loopback forward the user had asked for — so the launch started a
// socat for it and the in-jail forwarder bound 8214 four lines before the supervisor
// started the bridge (docs/design/wire-bridge-port-collision.md §2, §3).
//
// TestLocalProviderForwardsFindsOnlyUserLoopbackURLs, in this package, is the test that was
// supposed to catch this and could not: it builds a providers map by hand, so it measures
// the callee on inputs the launch never produces. This one runs the launch's own two calls,
// in the launch's own order, over the launch's own config map — delete either call and it
// stops measuring anything, which is the property that test lacks.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestComposingProvidersAddsNoImplicitHostForward(t *testing.T) {
	// composedProviders reads the user's `adapters` overrides off the USER FILE directly,
	// so the test needs a home of its own or the developer's config decides the result.
	t.Setenv("HOME", t.TempDir())

	// §2.3's precondition: a provider no shipped pack ships, offering openai and not
	// anthropic — the documented shape for a local inference server, and the exact case
	// localProviderForwards exists to serve.
	decoded, err := jsonx.Decode([]byte(`{
  "providers": {
    "myprovider": {
      "api_key_env_name": "MYPROVIDER_API_KEY",
      "endpoints": {"openai": {"base_url": "http://192.168.1.50:8000/v1"}}
    }
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := decoded.(*jsonx.OrderedMap)

	// The shipped packs that make the adapter fire: claude speaks anthropic, wire-bridge
	// declares openai→anthropic at 127.0.0.1:8214.
	var packs []*packload.Pack
	for _, p := range packload.Embedded() {
		switch p.Name {
		case "claude", "wire-bridge":
			packs = append(packs, p)
		}
	}
	if len(packs) != 2 {
		t.Fatalf("selected %d of the two shipped packs this measures", len(packs))
	}

	before := localProviderForwards(cfgMap(cfg, "providers"))
	if len(before) != 0 {
		t.Fatalf("the fixture already asks for a forward (%v); it names no loopback URL", before)
	}

	providers, err := composedProviders(cfg, packs)
	if err != nil {
		t.Fatal(err)
	}

	// THE MEASUREMENT: the launch's own second read, after the composition.
	if got := localProviderForwards(cfgMap(cfg, "providers")); len(got) != 0 {
		t.Errorf("composing the providers table manufactured host-loopback forwards %v.\n"+
			"Those ports are the wire bridge's, not the user's: the launch forwards them, "+
			"the in-jail socat binds them before the supervisor runs, and the bridge refuses "+
			"the launch with \"address already in use\" "+
			"(docs/design/wire-bridge-port-collision.md).", got)
	}

	// And the adaptation still reached the COMPOSED table, which is the half that must
	// keep working — the bridge's listen address comes from exactly this entry.
	entry, _ := providers.Get("myprovider")
	e, ok := entry.(*jsonx.OrderedMap)
	if !ok {
		t.Fatal("the user's provider left the composed table")
	}
	eps, _ := e.Get("endpoints")
	byProtocol, ok := eps.(*jsonx.OrderedMap)
	if !ok {
		t.Fatal("the composed entry has no endpoints map")
	}
	anthropic, ok := byProtocol.Get("anthropic")
	if !ok {
		t.Fatal("the composed entry gained no anthropic endpoint — the adapter must still adapt")
	}
	base, _ := anthropic.(*jsonx.OrderedMap).Get("base_url")
	if base != "http://127.0.0.1:8214" {
		t.Errorf("composed anthropic base_url = %v, want the shipped bridge's address", base)
	}
}
