package packload

// provideraliasing_test.go pins the ONE-WRITER property of ComposeProviders: the composed
// table shares no mutable object with the user's providers map or with a pack's
// declaration, so the adapter pass — which runs last, over the finished table — cannot
// write into anything a caller goes on reading.
//
// THE TEST IS THE REAL PATH ON PURPOSE. The defect this file exists for shipped behind a
// unit test that built a providers map BY HAND and called the consumer on it
// (TestLocalProviderForwardsFindsOnlyUserLoopbackURLs, internal/cli/run) — the callee was
// correct on every input the suite ever gave it, and nothing gave it one that had been
// through ComposeProviders. So the composition here is the shipped wire-bridge adapter
// declaration and the shipped claude pack's protocols, not a fixture restating them:
// a fixture would have gone on passing while the composer poisoned the config
// (docs/reference/wire-bridge.md#the-invariant-that-keeps-the-adapters-address-out-of-the-users-map).

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// bridgedPacks selects the shipped packs that make the injection fire: claude declares an
// anthropic-speaking program, wire-bridge declares openai→anthropic at 127.0.0.1:8214.
func bridgedPacks(t *testing.T) []*Pack {
	t.Helper()
	var out []*Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "claude", "wire-bridge":
			out = append(out, p)
		}
	}
	if len(out) != 2 {
		t.Fatalf("selected %d of the two shipped packs this measures", len(out))
	}
	return out
}

// userLocalProvider is the collision's precondition
// (wire-bridge.md#only-a-users-provider-url-becomes-an-implicit-forward), spelled as the shipped shape for a local
// inference server: a provider NO pack ships, offering openai and not anthropic. That is
// the entry ComposeProviders used to store by reference and then write the bridge's
// address into.
func userLocalProvider(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(`{
  "myprovider": {
    "api_key_env_name": "MYPROVIDER_API_KEY",
    "capabilities": ["web_search"],
    "endpoints": {"openai": {"base_url": "http://192.168.1.50:8000/v1"}},
    "models": {"default": "some-local-model"}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.OrderedMap)
}

// snapshot renders a map the way the config snapshot does, which is the comparison that
// notices a key the composer added at any depth.
func snapshot(t *testing.T, m *jsonx.OrderedMap) string {
	t.Helper()
	s, err := jsonx.DumpsSnapshot(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// THE REGRESSION. Composing must leave the caller's map byte-identical — and must still
// adapt, which is the half a copy could break by copying the wrong direction.
func TestComposeProvidersDoesNotWriteThroughToTheUsersMap(t *testing.T) {
	packs := bridgedPacks(t)
	user := userLocalProvider(t)
	before := snapshot(t, user)

	composed, err := ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}

	if after := snapshot(t, user); after != before {
		t.Errorf("ComposeProviders wrote through to the caller's providers map.\n"+
			"before: %s\nafter:  %s\n"+
			"The composed table may hold no object the caller owns: the adapter pass runs "+
			"over the finished table, and a shared map makes it an editor of the user's "+
			"config (docs/reference/wire-bridge.md#oq-pc1).", before, after)
	}

	// The adaptation still has to happen — only the write-through stops.
	entry, ok := composed.Get("myprovider")
	if !ok {
		t.Fatal("the user's provider left the composed table")
	}
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "http://127.0.0.1:8214" {
		t.Errorf("composed anthropic endpoint = %q, want the shipped bridge's address — "+
			"the copy must stop the write-through, not the adaptation", got)
	}
	if got := endpointURL(t, asOrdered(t, entry), "openai"); got != "http://192.168.1.50:8000/v1" {
		t.Errorf("composed openai endpoint = %q, want the user's own upstream", got)
	}
}

// THE SAME PROPERTY, CHECKED BY IDENTITY RATHER THAN BY OUTCOME. The test above notices a
// write that happens today; this one notices a SHARED OBJECT, which is what makes the next
// write possible. It is how "deep enough" is verified: a one-level copy leaves the
// `endpoints` map shared and fails here even when no pass writes to it yet.
func TestTheComposedTableSharesNoObjectWithTheUsersMap(t *testing.T) {
	user := userLocalProvider(t)
	composed, err := ComposeProviders(user, bridgedPacks(t))
	if err != nil {
		t.Fatal(err)
	}
	owned := map[any]string{}
	collectContainers(user, "providers", owned)
	for path, shared := range sharedContainers(composed, "composed", owned) {
		t.Errorf("the composed table reaches an object the caller owns: %s is the caller's %s",
			path, shared)
	}
}

// A PACK'S DECLARATION IS THE OTHER POSSIBLE VICTIM, and it is the less visible one: a
// manifest corrupted in memory would survive into every later read of Pack.Decl in the
// process. Measured by composing twice from the same packs with the first composition
// written into in between — shippedProviderEntry allocates every level, so the second
// composition must not have heard about it.
func TestComposingTwiceCannotCorruptAPacksDeclaration(t *testing.T) {
	packs := bridgedPacks(t)
	first, err := ComposeProviders(userLocalProvider(t), packs)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := first.Get("bedrock") // the claude pack's own shipped provider
	if !ok {
		t.Fatal("the shipped claude pack's bedrock provider left the composed table")
	}
	e := asOrdered(t, entry)
	e.Set("region", "tampered")
	addEndpoint(e, "anthropic", "http://127.0.0.1:1")

	second, err := ComposeProviders(userLocalProvider(t), packs)
	if err != nil {
		t.Fatal(err)
	}
	fresh := asOrdered(t, mustGet(t, second, "bedrock"))
	if got, _ := fresh.Get("region"); got == "tampered" {
		t.Error("a write into one composed table reached the pack's declaration: the second " +
			"composition carries the first one's edit")
	}
	if got := endpointURL(t, fresh, "anthropic"); got == "http://127.0.0.1:1" {
		t.Error("addEndpoint on one composed entry reached the pack's declared endpoints map")
	}
}

// mustGet reads a key that has to be there.
func mustGet(t *testing.T, m *jsonx.OrderedMap, key string) any {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("composed table has no %q", key)
	}
	return v
}

// collectContainers records every MUTABLE object reachable from v — the maps and slices a
// later write could land in — keyed by identity, valued by the path it was found at.
// Scalars are immutable and cannot be written through, so they are not tracked.
func collectContainers(v any, path string, out map[any]string) {
	walkContainers(v, path, func(id any, at string) {
		if _, seen := out[id]; !seen {
			out[id] = at
		}
	})
}

// sharedContainers walks v and returns every path whose object is one of owned's, mapped
// to the path it was owned at.
func sharedContainers(v any, path string, owned map[any]string) map[string]string {
	found := map[string]string{}
	walkContainers(v, path, func(id any, at string) {
		if who, ok := owned[id]; ok {
			found[at] = who
		}
	})
	return found
}

// walkContainers visits every mutable object reachable from v and reports its IDENTITY: a
// map's pointer, a slice's backing array. An empty slice is skipped — it has no element to
// share and Go gives every empty slice the same address.
func walkContainers(v any, path string, visit func(id any, at string)) {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		if t == nil {
			return
		}
		visit(any(t), path)
		for _, k := range t.Keys() {
			sub, _ := t.Get(k)
			walkContainers(sub, path+"."+k, visit)
		}
	case []any:
		if len(t) > 0 {
			visit(reflect.ValueOf(t).Pointer(), path)
		}
		for i, e := range t {
			walkContainers(e, path+"["+strconv.Itoa(i)+"]", visit)
		}
	case []string:
		if len(t) > 0 {
			visit(reflect.ValueOf(t).Pointer(), path)
		}
	case map[string]any:
		visit(reflect.ValueOf(t).Pointer(), path)
		for k, e := range t {
			walkContainers(e, path+"."+k, visit)
		}
	}
}
