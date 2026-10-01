package packload

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// carrier_test.go pins the carrier (carrier.go; docs/design/wire-bridge-gateway.md WG-I44) through
// the profile resolution every notch runs: on a profile naming no via over a provider the wire
// bridge fronts, the bridge carries exactly the agents with no client of the provider's platform.

// carrierLaunch is every shipped agent beside the bedrock provider and the wire bridge.
var carrierLaunch = []string{"claude", "codex", "opencode", "pi", "copilot", "omp", "agy", "bedrock",
	"aws-auth", "openai-auth", "wire-bridge"}

// TestTheCarrierCarriesOnlyTheAgentsWithNoClientOfThePlatform: on `-p bedrock` the bridge carries
// copilot and oh-omp, which have no Bedrock client, and not agy, which declares no protocol the
// bridge could point it at; claude, codex, opencode and pi keep their own clients. A profile naming
// its own via (`bedrock-bridge`) has no carrier, since every agent goes through the via there.
func TestTheCarrierCarriesOnlyTheAgentsWithNoClientOfThePlatform(t *testing.T) {
	packs := embeddedNamed(t, carrierLaunch...)
	_, resolved, _ := launchSelection(t, packs, nil, nil, nil)
	r := resolved["bedrock"]
	if r.Carrier != "wire-bridge" || r.CarrierBase != "http://127.0.0.1:8216" ||
		strings.Join(r.Carried, ",") != "copilot,oh-omp" {
		t.Fatalf("bedrock's carrier = %q at %q carrying %v, want wire-bridge at its via address carrying "+
			"copilot and oh-omp", r.Carrier, r.CarrierBase, r.Carried)
	}
	for _, agent := range []string{"copilot", "oh-omp"} {
		if got := ViaURLFor(r, agent); got != "http://127.0.0.1:8216/agent/"+agent {
			t.Errorf("%s on -p bedrock: via URL %q, want its route on the bridge", agent, got)
		}
		if got := ViaAPIKeyEnvNameFor(packs, r, agent); got != "YOLO_SERVICE_WIRE_BRIDGE_TOKEN" {
			t.Errorf("%s on -p bedrock sends %q to the bridge, want the bridge's caller token", agent, got)
		}
	}
	for _, agent := range []string{"claude", "codex", "opencode", "pi", "agy"} {
		if got := ViaURLFor(r, agent); got != "" {
			t.Errorf("%s on -p bedrock was routed at %q; it is not carried", agent, got)
		}
	}
	if b := resolved["bedrock-bridge"]; b.Carrier != "" || len(b.Carried) != 0 {
		t.Errorf("bedrock-bridge, which names its own via, has carrier %q for %v", b.Carrier, b.Carried)
	}
	// The wire: what the launcher writes into YOLO_PROFILES names the carrier and its agents.
	wire := ProfilesWireTable(resolved)
	v, _ := wire.Get("bedrock")
	entry := v.(*jsonx.OrderedMap)
	for key, want := range map[string]string{WireCarrierKey: "wire-bridge",
		WireCarrierBaseKey: "http://127.0.0.1:8216", WireCarriedKey: "copilot,oh-omp"} {
		if got, _ := entry.Get(key); got != want {
			t.Errorf("YOLO_PROFILES' bedrock %s = %v, want %q", key, got, want)
		}
	}
}

// TestNoCarrierWithoutAServiceThatFrontsThePlatform: with no wire bridge in the launch nothing
// fronts aws-bedrock, so nothing is carried and copilot on `-p bedrock` reaches nothing, as the
// profile line's warning says; and a notch that does not serve the bridge (the host's inert table)
// carries nobody either, without naming `bedrock` among the vias it cleared, since it names none.
func TestNoCarrierWithoutAServiceThatFrontsThePlatform(t *testing.T) {
	_, resolved, _ := launchSelection(t, embeddedNamed(t, "copilot", "omp", "bedrock", "aws-auth"), nil, nil, nil)
	if r := resolved["bedrock"]; r.Carrier != "" || len(r.Carried) != 0 {
		t.Errorf("with no bridge, bedrock's carrier = %q for %v, want none", r.Carrier, r.Carried)
	}
	packs := embeddedNamed(t, carrierLaunch...)
	_, served, _ := launchSelection(t, packs, nil, nil, nil)
	inert, cleared := ViaServedAt(served, packs, NothingServed())
	if r := inert["bedrock"]; r.Carrier != "" || len(r.Carried) != 0 || ViaURLFor(r, "copilot") != "" {
		t.Errorf("at a notch serving no daemon, bedrock still carries %v through %q", r.Carried, r.Carrier)
	}
	if slices.Contains(cleared, "bedrock") {
		t.Errorf("cleared vias %v name bedrock, which names no via", cleared)
	}
}
