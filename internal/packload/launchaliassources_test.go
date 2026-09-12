package packload

import (
	"strings"
	"testing"
)

// TestAliasesSuppressAFlagTheAutonomyPostureInjects pins the cross-kind coupling that makes
// copilot's split declaration work, with a SYNTHETIC pack, so the property survives copilot.
//
// The two halves of one switch are declared under two different kinds, and only one of them
// can hold the alias:
//
//   - the FLAG lives in the `autonomy` contribution's autonomous posture, because it is a
//     permission bypass and the confinement notch has to be able to withhold it;
//   - the ALIAS lives in a plain `launch` contribution, because AutonomyLaunch is {bin,
//     flags} with nowhere to put an alias map.
//
// So the launch contribution carries a bin and an alias map and NO FLAGS AT ALL, and
// InjectLaunchFlags has to read the two sources independently — FlagAliases over every
// launch contribution, LaunchFlagsFor over the posture — for `-y` to suppress a `--yolo` the
// same contribution never declared.
//
// TestInjectLaunchFlags beside this one cannot see that: its flags and aliases sit in ONE
// contribution, so it passes just as well if the alias lookup is narrowed to the
// contribution the flag came from. TestCopilotFlagsInjectFromItsRealDeclaration does cover
// it, but only through the shipped copilot manifest — the mechanism would go unpinned the
// day that pack changes shape, which is precisely the day someone is editing this code.
func TestAliasesSuppressAFlagTheAutonomyPostureInjects(t *testing.T) {
	p := &Pack{Name: "p", Decl: declFrom(t, `{"contributes":[
		{"kind":"launch","bin":"tool","aliases":{"--yolo":["-y"]}},
		{"kind":"autonomy","autonomous":{"launch":[{"bin":"tool","flags":["--yolo"]}]}}
	]}`)}
	packs := []*Pack{p}

	// The flag arrives even though no `launch` contribution declares one.
	if got := InjectLaunchFlags(packs, []string{"tool", "sub"}); strings.Join(got, " ") != "tool --yolo sub" {
		t.Errorf("the autonomous posture's flag did not reach the argv: %v", got)
	}
	// And the alias declared on the FLAGLESS launch contribution suppresses it.
	if got := InjectLaunchFlags(packs, []string{"tool", "-y", "sub"}); strings.Contains(strings.Join(got, " "), "--yolo") {
		t.Errorf("`-y` must suppress the posture's `--yolo`, and the alias that says so is "+
			"declared on a launch contribution carrying NO flags: %v", got)
	}
	// The guarded notch withholds the flag; the alias table is unaffected by the notch.
	if got := LaunchFlagsFor(packs, false)["tool"]; len(got) != 0 {
		t.Errorf("the guarded notch must inject nothing: %v", got)
	}
	if got := FlagAliases(packs)["--yolo"]; len(got) != 1 || got[0] != "-y" {
		t.Errorf("the alias map is read off the launch contribution regardless of notch: %v", got)
	}
}
