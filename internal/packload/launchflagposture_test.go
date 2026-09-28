package packload

import (
	"strings"
	"testing"
)

// launchflagposture_test.go pins docs/plans/notch-convergence.md item 20 (row D9) at the
// injector: InjectLaunchFlags folds the posture its CALLER names. It used to fold the
// autonomous posture whatever the caller was, so a `guarded.launch` entry reached no notch.

// twoPostures declares a different flag for one binary in each posture, so the flag that
// lands says which posture was folded.
func twoPostures(t *testing.T) []*Pack {
	t.Helper()
	return []*Pack{{Name: "acme", Decl: declFrom(t,
		`{"contributes":[{"kind":"autonomy",`+
			`"autonomous":{"launch":[{"bin":"tool","flags":["--no-prompts"]}]},`+
			`"guarded":{"launch":[{"bin":"tool","flags":["--ask-first"]}]}}]}`)}}
}

func TestInjectLaunchFlagsFoldsTheGuardedPostureWhenAutonomyIsOff(t *testing.T) {
	packs := twoPostures(t)
	out, inj := InjectLaunchFlags(packs, false, []string{"tool", "sub"})
	if got := strings.Join(out, " "); got != "tool --ask-first sub" {
		t.Errorf("autonomy off: argv = %q, want the guarded posture's flag only (%q)", got,
			"tool --ask-first sub")
	}
	if inj == nil || inj.Pack != "acme" || strings.Join(inj.Flags, " ") != "--ask-first" {
		t.Errorf("autonomy off: the record must name the guarded flag it added: %+v", inj)
	}
}

func TestInjectLaunchFlagsFoldsTheAutonomousPostureWhenAutonomyIsOn(t *testing.T) {
	out, _ := InjectLaunchFlags(twoPostures(t), true, []string{"tool", "sub"})
	if got := strings.Join(out, " "); got != "tool --no-prompts sub" {
		t.Errorf("autonomy on: argv = %q, want the autonomous posture's flag only", got)
	}
}

// A pack that declares only an autonomous flag adds nothing when autonomy is off: the
// permission bypass is exactly what the guarded posture exists to withhold.
func TestInjectLaunchFlagsWithholdsAnAutonomousOnlyFlagWhenAutonomyIsOff(t *testing.T) {
	packs := []*Pack{{Name: "acme", Decl: declFrom(t,
		`{"contributes":[{"kind":"autonomy","autonomous":{"launch":[`+
			`{"bin":"tool","flags":["--yolo"]}]}}]}`)}}
	out, inj := InjectLaunchFlags(packs, false, []string{"tool"})
	if len(out) != 1 || inj != nil {
		t.Errorf("autonomy off must not inject an autonomous flag: argv = %v, record = %+v", out, inj)
	}
}

// The disclosure's words live beside the record, so both notches print one sentence.
func TestLaunchInjectionDisclosureLines(t *testing.T) {
	_, inj := InjectLaunchFlags(twoPostures(t), false, []string{"tool", "a b"})
	lines := inj.DisclosureLines()
	want := []string{
		"yolo CHANGED the command you asked for:",
		"  you asked for: tool 'a b'",
		"  yolo will run: tool --ask-first 'a b'",
		"  added by pack acme: --ask-first",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("DisclosureLines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	var none *LaunchInjection
	if got := none.DisclosureLines(); got != nil {
		t.Errorf("a nil record (nothing rewritten) must disclose nothing: %q", got)
	}
}
