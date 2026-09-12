package packload_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// EVERY LAUNCH FLAG A SHIPPED PACK INJECTS IS NAMED IN ITS FOOTPRINT — whichever kind
// declares it.
//
// `yolo pack footprint` and `yolo pack lint` are the only surfaces that name a pack's launch
// flags at all: the launch banner shows review-worthy claims only, and neither `launch` nor
// `autonomy` is review-worthy. So this report is the whole of the disclosure, and the
// boundary today is disclosure rather than consent.
//
// THE REGRESSION THIS PINS, measured: copilot's `--yolo` — its permission bypass — was a
// plain `launch` contribution, whose claim renders its flags verbatim (`launch copilot
// --yolo --no-auto-update`). Moving it under `kind: "autonomy"` so the confinement notch
// governs it was right, and it silently took the flag OUT of every footprint, because the
// autonomy claim rendered only the posture PAIR ("autonomous posture only") and never what
// the posture injects. claude, codex and agy had never had theirs named at all. A
// security-motivated move must not cost a disclosure.
//
// Written over the flags packload actually COMPOSES at each notch (LaunchFlagsFor) rather
// than over the manifest, so it is a claim about what a jail really gets and fails for a flag
// declared in some third place a future kind invents.
func TestEveryInjectedLaunchFlagIsNamedInTheFootprint(t *testing.T) {
	for _, p := range loadAll(t) {
		var claimed []string
		for _, c := range packload.FootprintOf(p).Claims {
			if c.Kind == packdecl.KindLaunch || c.Kind == packdecl.KindAutonomy {
				claimed = append(claimed, c.Detail)
			}
		}
		disclosure := strings.Join(claimed, " | ")

		for _, notch := range []render.Kind{render.KindJail, render.KindGuest, render.KindHost} {
			autonomy := render.ProfileFor(notch).AgentAutonomy
			for bin, flags := range packload.LaunchFlagsFor([]*packload.Pack{p}, autonomy) {
				for _, flag := range flags {
					if strings.Contains(disclosure, flag) {
						continue
					}
					t.Errorf("pack %q injects %q after `%s` at the %v notch, and no launch or "+
						"autonomy claim in its footprint names it.\nfootprint details: %s\n"+
						"A flag yolo puts on an agent's command line is disclosed by this "+
						"report or by nothing.", p.Name, flag, bin, notch, disclosure)
				}
			}
		}
	}
}

// The copilot INSTANCE, spelled out, because the property test above passes for the whole
// shipped set and a reader chasing "where did `--yolo` go" needs the one line that answers it.
// It also pins WHICH posture is named: "injects" beside the wrong posture would say a host
// gets the bypass.
func TestCopilotFootprintNamesTheYoloFlagUnderItsAutonomousPosture(t *testing.T) {
	fp := packload.FootprintOf(packNamed(t, loadAll(t), "copilot"))
	var got string
	for _, c := range fp.Claims {
		if c.Kind == packdecl.KindAutonomy {
			got = c.Detail
		}
	}
	want := "autonomous posture only; autonomous injects `copilot --yolo`"
	if got != want {
		t.Errorf("copilot's autonomy claim detail = %q, want %q", got, want)
	}
}

// A posture entry carrying a bin and NO flags injects nothing, so the claim must not say it
// injects something. The shape is reachable — a posture entry replaces that binary's plain
// launch flags, so `{"bin":"x"}` under `guarded` is how a pack would SUBTRACT them — and
// "injects `x`" would be a false sentence about it.
func TestAFlaglessPostureEntryClaimsNoInjection(t *testing.T) {
	m, probs := packdecl.Decode([]byte(`{"name":"x","contributes":[` +
		`{"kind":"launch","bin":"x","flags":["--plain"]},` +
		`{"kind":"autonomy","autonomous":{"launch":[{"bin":"x","flags":["--go"]}]},` +
		`"guarded":{"launch":[{"bin":"x"}]}}]}`))
	if len(probs) != 0 {
		t.Fatalf("decoding the fixture: %v", probs)
	}
	for _, c := range packload.FootprintOf(&packload.Pack{Name: "x", Decl: m}).Claims {
		if c.Kind != packdecl.KindAutonomy {
			continue
		}
		if !strings.Contains(c.Detail, "autonomous injects `x --go`") {
			t.Errorf("the autonomous posture's flag is unnamed: %q", c.Detail)
		}
		if strings.Contains(c.Detail, "guarded injects") {
			t.Errorf("a flagless guarded entry injects nothing, but the claim says it does: %q",
				c.Detail)
		}
	}
}
