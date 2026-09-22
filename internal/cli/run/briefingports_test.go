package run

// The launch path's own port→briefing projection. Asserted through
// briefingPortsFor, which is what refreshJailBriefings calls: a test that retyped
// the same two mapGet expressions would stay green with the wiring deleted, and
// the wiring is the whole feature — a briefing that omits network.ports is how an
// agent ends up unable to tell which direction is which.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func networkSection(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestBriefingPortsForReturnsBothDirections(t *testing.T) {
	net := networkSection(
		"ports", []any{"8000:3000"},
		"forward_host_ports", []any{"5432:3306"},
	)

	publish, forward := briefingPortsFor("bridge", net, nil)

	// Returned SEPARATELY and not swapped: the publish list is network.ports.
	if len(publish) != 1 || publish[0] != "8000:3000" {
		t.Errorf("publish = %v, want [8000:3000] (network.ports)", publish)
	}
	if len(forward) != 1 || forward[0] != "5432:3306" {
		t.Errorf("forward = %v, want [5432:3306] (forward_host_ports)", forward)
	}
}

// TestBriefingPortsForPublishAloneStillReaches is the regression for the gap:
// a jail with only network.ports set must still have something to advertise.
func TestBriefingPortsForPublishAloneStillReaches(t *testing.T) {
	publish, forward := briefingPortsFor("bridge", networkSection("ports", []any{"8000:8000"}), nil)
	if len(publish) != 1 {
		t.Errorf("publish = %v, want the single network.ports entry", publish)
	}
	if forward != nil {
		t.Errorf("forward = %v, want nil", forward)
	}
}

// TestBriefingPortsForHostModeAdvertisesNeither: assembleRunCmd honors neither key
// outside bridge mode, so the briefing must not claim they are in effect.
func TestBriefingPortsForHostModeAdvertisesNeither(t *testing.T) {
	net := networkSection(
		"ports", []any{"8000:3000"},
		"forward_host_ports", []any{5432},
	)
	for _, mode := range []string{"host", ""} {
		publish, forward := briefingPortsFor(mode, net, nil)
		if publish != nil || forward != nil {
			t.Errorf("netMode=%q gave (%v, %v), want both nil", mode, publish, forward)
		}
	}
}

func TestBriefingPortsForNoNetworkSection(t *testing.T) {
	publish, forward := briefingPortsFor("bridge", nil, nil)
	if publish != nil || forward != nil {
		t.Errorf("no network section gave (%v, %v), want both nil", publish, forward)
	}
}

// TestBriefingPortsForMergesImplicitProviderForwards pins OQ-PC2's briefing half at the
// function refreshJailBriefings actually calls. A user provider on localhost opens a hole
// into the host, and an agent told only about `network.forward_host_ports` is told that hole
// is closed.
//
// It also covers the shape that has no `network` section at all, which is the common one for
// a config whose only forward is implicit.
func TestBriefingPortsForMergesImplicitProviderForwards(t *testing.T) {
	decoded, err := jsonx.Decode([]byte(`{"ollama": {"base_url": "http://localhost:11434/v1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	providers := decoded.(*jsonx.OrderedMap)

	_, forward := briefingPortsFor("bridge", networkSection("forward_host_ports", []any{5432}), providers)
	if want := []any{5432, 11434}; !reflect.DeepEqual(forward, want) {
		t.Errorf("forward = %#v, want %#v — the briefing must describe the MERGED list", forward, want)
	}

	// No network section, one implicit forward: the section must still appear.
	if _, only := briefingPortsFor("bridge", nil, providers); !reflect.DeepEqual(only, []any{11434}) {
		t.Errorf("a config with providers and no network section still forwards; got %#v", only)
	}

	// Host networking forwards nothing, however the providers are written.
	if _, none := briefingPortsFor("host", nil, providers); none != nil {
		t.Errorf("host networking forwards nothing; got %#v", none)
	}
}

// TestRenderedBriefingNamesAnImplicitProviderForward pins the CALL SITE, end to end through
// refreshJailBriefings — not briefingPortsFor in isolation.
//
// Without this, passing `nil` for providers at the one production call site would regress the
// feature with every unit test above still green. That is the exact shape AGENTS.md warns
// about: a test that pins the callee while the call site is unpinned is not a test.
func TestRenderedBriefingNamesAnImplicitProviderForward(t *testing.T) {
	providers := jsonx.NewOrderedMap()
	ollama := jsonx.NewOrderedMap()
	ollama.Set("base_url", "http://localhost:11434/v1")
	providers.Set("ollama", ollama)

	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := appliedOptions(t, t.TempDir(), home, false)
	got := appliedBriefing(t, o, "podman", appliedTestConfig("providers", providers))

	if !strings.Contains(got, "Forwarded Host Ports") {
		t.Fatalf("a localhost provider forwards a host port, so the briefing must carry the "+
			"section; got:\n%s", got)
	}
	if !strings.Contains(got, "11434") {
		t.Errorf("the briefing must name the implicitly forwarded port — an agent told only "+
			"about network.forward_host_ports believes this hole is closed; got:\n%s", got)
	}
}
