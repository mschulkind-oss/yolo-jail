package packload

import (
	"strings"
	"testing"
)

// viatrigger_test.go pins THE VIA TRIGGER's what-if (ViaRoutedServices; docs/design/host-notch-services.md
// HS-D30) and the file-carried reading `yolo host --` decides by (FileCarriedVia, HS-D31), over the
// shipped packs: a notch that serves no wire bridge asks whether serving it would route an agent
// through it, by its profile's `via` or by the profile's carrier (WG-I44).

// viaWhatIf is the what-if of a notch serving nothing yet, over carrierLaunch's packs.
func viaWhatIf(t *testing.T, active map[string]string) ViaWhatIf {
	t.Helper()
	return ViaWhatIf{Packs: embeddedNamed(t, carrierLaunch...), Served: NothingServed().AtHost(), Active: active}
}

// THE TRIGGER FIRES FOR A VIA AND FOR A CARRIER, AND FOR NOTHING ELSE: pi on bedrock-bridge (its
// profile's via) and copilot on plain bedrock (the carrier, which exists only once the bridge is
// served, since the `for_via` address composes only then) are routed through the bridge; claude on
// plain bedrock keeps its own Bedrock client, so nothing is; a notch that serves the bridge already
// asks nothing; and a service admission refuses is no candidate.
func TestViaRoutedServicesFiresForAViaAndACarrier(t *testing.T) {
	admit := func(string) bool { return true }
	for _, tc := range []struct {
		active map[string]string
		want   string
	}{
		{map[string]string{"pi": "bedrock-bridge"}, "pi"},
		{map[string]string{"copilot": "bedrock"}, "copilot"},
		{map[string]string{"copilot": "bedrock", "pi": "bedrock-bridge", "claude": "bedrock"}, "copilot,pi"},
		{map[string]string{"claude": "bedrock"}, ""},
	} {
		routed, err := ViaRoutedServices(viaWhatIf(t, tc.active), admit)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(routed) == 1 && routed[0].Service == "wire-bridge" && routed[0].Pack == "wire-bridge" {
			got = strings.Join(routed[0].Agents, ",")
		} else if len(routed) > 1 {
			t.Fatalf("%v: routed %d services, want at most the bridge", tc.active, len(routed))
		}
		if got != tc.want {
			t.Errorf("%v: the bridge routes [%s], want [%s]", tc.active, got, tc.want)
		}
	}
	in := viaWhatIf(t, map[string]string{"pi": "bedrock-bridge"})
	in.Served = ServedInJail([]string{"wire-bridge"})
	if routed, _ := ViaRoutedServices(in, admit); len(routed) != 0 {
		t.Errorf("a notch serving the bridge already was offered it again: %+v", routed)
	}
	if routed, _ := ViaRoutedServices(viaWhatIf(t, map[string]string{"pi": "bedrock-bridge"}),
		func(string) bool { return false }); len(routed) != 0 {
		t.Errorf("a service admission refuses was planned: %+v", routed)
	}
}

// THE WHAT-IF TABLES ARE THE SERVED ONES: the carrier copilot rides exists in them, and pi's via
// base is the declared address the plan then moves.
func TestViaRoutedServicesHandsBackTheServedTables(t *testing.T) {
	routed, err := ViaRoutedServices(viaWhatIf(t, map[string]string{"copilot": "bedrock", "pi": "bedrock-bridge"}),
		func(string) bool { return true })
	if err != nil || len(routed) != 1 {
		t.Fatalf("routed %+v, %v", routed, err)
	}
	if r := routed[0].Resolved["bedrock"]; r.Carrier != "wire-bridge" {
		t.Errorf("the what-if's bedrock has carrier %q, want the bridge", r.Carrier)
	}
	if got := ViaURLFor(routed[0].Resolved["bedrock-bridge"], "pi"); got != "http://127.0.0.1:8216/agent/pi" {
		t.Errorf("pi's what-if via URL = %q", got)
	}
}

// A FILE-CARRIED VIA IS NAMED BY ITS FILE (HS-D31): pi's derive writes its bedrock-bridge route into
// ~/.pi/agent/models.json, which a host launch does not render per launch; copilot rides the adapter
// address its environment carries, so no file carries its route.
func TestFileCarriedViaNamesTheConfigFileThatCarriesIt(t *testing.T) {
	active := map[string]string{"pi": "bedrock-bridge", "copilot": "bedrock-bridge"}
	in := viaWhatIf(t, active)
	routed, err := ViaRoutedServices(in, func(string) bool { return true })
	if err != nil || len(routed) != 1 {
		t.Fatalf("routed %+v, %v", routed, err)
	}
	pi, err := FileCarriedVia(in.Packs, routed[0], "pi", active)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range pi {
		found = found || f == "~/.pi/agent/models.json"
	}
	if !found {
		t.Errorf("pi's file-carried via names %v, want ~/.pi/agent/models.json among them", pi)
	}
	if copilot, err := FileCarriedVia(in.Packs, routed[0], "copilot", active); err != nil || len(copilot) != 0 {
		t.Errorf("copilot's via is file-carried by %v (%v), want none: its route is its environment's", copilot, err)
	}
}

// THE HOST'S OWN LINE FOR A VIA IT CLEARED replaces the notch's clause, and a profile with none
// keeps the notch's (UnservedLinesWith).
func TestUnservedLinesWithTakesTheLaunchsOwnViaLine(t *testing.T) {
	lines := UnservedLinesWith(nil, []string{"a", "b"}, map[string]string{"a": `profile "a"'s carrier — a file carries it`}, nil)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, `  profile "a"'s carrier — a file carries it`) || strings.Contains(text, `profile "a"'s via`) ||
		!strings.Contains(text, `profile "b"'s via — its service does not run here`) {
		t.Errorf("lines =\n%s", text)
	}
}

// THE TWO NOTCHES THAT RUN LAUNCH-OWNED SERVICES are the host's and macos-user's served sets; a
// container's is not (RunsLaunchOwnedServices, which `yolo check` predicts the trigger by).
func TestRunsLaunchOwnedServicesIsTheHostsAndMacosUsers(t *testing.T) {
	if !NothingServed().AtHost().RunsLaunchOwnedServices() || !ServedInJail(nil).MountsNothing().RunsLaunchOwnedServices() {
		t.Error("the host's or macos-user's served set does not run launch-owned services")
	}
	if ServedInJail([]string{"wire-bridge"}).RunsLaunchOwnedServices() {
		t.Error("a container's served set runs launch-owned services")
	}
}
