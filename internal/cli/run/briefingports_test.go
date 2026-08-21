package run

// The launch path's own port→briefing projection. Asserted through
// briefingPortsFor, which is what refreshJailBriefings calls: a test that retyped
// the same two mapGet expressions would stay green with the wiring deleted, and
// the wiring is the whole feature — a briefing that omits network.ports is how an
// agent ends up unable to tell which direction is which.

import (
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

	publish, forward := briefingPortsFor("bridge", net)

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
	publish, forward := briefingPortsFor("bridge", networkSection("ports", []any{"8000:8000"}))
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
		publish, forward := briefingPortsFor(mode, net)
		if publish != nil || forward != nil {
			t.Errorf("netMode=%q gave (%v, %v), want both nil", mode, publish, forward)
		}
	}
}

func TestBriefingPortsForNoNetworkSection(t *testing.T) {
	publish, forward := briefingPortsFor("bridge", nil)
	if publish != nil || forward != nil {
		t.Errorf("no network section gave (%v, %v), want both nil", publish, forward)
	}
}
