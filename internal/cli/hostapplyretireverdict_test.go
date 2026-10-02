package cli

// hostapplyretireverdict_test.go pins that a dry run's VERDICT counts the dropped-pack retires
// still waiting (docs/reference/happy-path-principle.md, rule 5: never report OK over broken), and
// that the launch gate, which reads a different counter, does not.
//
// A pack that left `packs` leaves its skills and files in the home until an --assert is answered
// `y`. The dry run listed each one as `would archive (pack no longer configured)` and then ended
// "Nothing to do — this home is up to date.", or, with `packs` empty, "No packs configured —
// nothing to apply, and nothing left to retire.": the retire noted only the question an --assert
// would ask, and the verdict counted none of it.

import (
	"bytes"
	"strings"
	"testing"
)

// retireOnlyPackJSON is a pack whose whole output is a skill and a file, the two things the
// dropped-pack retire archives. No briefing, so once it is dropped nothing else in the home moves
// and the retire is the only work left.
const retireOnlyPackJSON = `{"name":"dropme","description":"d","contributes":[
  {"kind":"skills","from":"skills","into":".claude/skills"},
  {"kind":"files","from":"bin","into":".claude/bin"}]}`

// retireWaitingHome applies dropme with claude, drops it, and answers an --assert's retire
// question `n`: the rest of the home is applied, and dropme's two paths are still in it.
func retireWaitingHome(t *testing.T) string {
	t.Helper()
	home, _ := dropFixture(t, retireOnlyPackJSON)
	applyThenDrop(t, home)
	if rc, report := applyWith(t, true, strings.NewReader("n\n")); rc != 0 {
		t.Fatalf("the declining --assert rc=%d\n%s", rc, report)
	}
	return home
}

func TestADryRunVerdictCountsTheRetiresStillWaiting(t *testing.T) {
	t.Run("with packs", func(t *testing.T) {
		defaultReport(t)
		retireWaitingHome(t)

		_, report := applyWith(t, false, nil)
		if !strings.Contains(report, "would archive (pack no longer configured)") {
			t.Fatalf("fixture bug: no retire is waiting:\n%s", report)
		}
		if got, want := verdictLine(report), "An --assert would complete."; got != want {
			t.Errorf("the verdict is %q, want %q:\n%s", got, want, report)
		}
		if strings.Contains(report, "up to date") {
			t.Errorf("a dry run listing paths it would archive says the home is up to date:\n%s", report)
		}
		if want := "2 paths from dropped packs would be archived"; !strings.Contains(report, want) {
			t.Errorf("the counts do not say %q:\n%s", want, report)
		}
		doc := dryRunDoc(t)
		if doc.Outcome != outcomeWouldComplete || doc.Counts.DroppedPackPathsToRetire != 2 ||
			doc.Counts.DroppedPackKeysToRetire != 0 {
			t.Errorf("the document's outcome is %q with %d paths and %d keys to retire, want %q, 2 and 0",
				doc.Outcome, doc.Counts.DroppedPackPathsToRetire, doc.Counts.DroppedPackKeysToRetire,
				outcomeWouldComplete)
		}
	})
	t.Run("with no packs", func(t *testing.T) {
		defaultReport(t)
		home := retireWaitingHome(t)
		selectPacks(t, home, "")
		// Settle what the empty `packs` retires without asking (the composed briefing), so the
		// dropped pack's two paths are all that is left.
		if rc, report := applyWith(t, true, strings.NewReader("n\n")); rc != 0 {
			t.Fatalf("the declining --assert rc=%d\n%s", rc, report)
		}

		_, report := applyWith(t, false, nil)
		if !strings.Contains(report, "would archive (pack no longer configured)") {
			t.Fatalf("fixture bug: no retire is waiting:\n%s", report)
		}
		const want = "No packs configured — nothing to apply; 2 destination(s) would be retired."
		if got := verdictLine(report); got != want {
			t.Errorf("the verdict is %q, want %q:\n%s", got, want, report)
		}
		if strings.Contains(report, "nothing left to retire") {
			t.Errorf("a dry run listing paths it would archive says nothing is left to retire:\n%s", report)
		}
	})
}

// The verdict's sentence for an empty `packs` names the config keys too, and a destination
// count of zero is not said.
func TestTheNoPacksVerdictNamesTheKeysStillWaiting(t *testing.T) {
	s := &hostApplySurvey{}
	s.noteZeroPacks()
	s.noteDroppedRetire(0, 1)
	if got, want := hostApplyVerdict(s, false),
		"No packs configured — nothing to apply; 1 config key would be retired."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	s = &hostApplySurvey{}
	s.noteZeroPacks()
	s.noteDroppedRetire(3, 2)
	if got, want := hostApplyVerdict(s, false),
		"No packs configured — nothing to apply; 3 destination(s) and 2 config keys would be "+
			"retired."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	if got := hostApplyOutcome(s, false); got != outcomeNoPacks {
		t.Errorf("outcome = %q, want %q", got, outcomeNoPacks)
	}
}

// THE LAUNCH GATE KEEPS ITS OWN COUNTER. It decides whether to apply from Changes(), the
// destinations a render would alter, and a waiting retire is not one: it is a question the gate
// may not ask (PendingDecisions), and it is never asked while nothing else would change. So the
// launch stays as it was, silent, with nothing rendered and nothing read from stdin, over a home
// whose dry run now says an --assert has work to do. Folding the retires into Changed would send
// this launch into the gate's decision branch, and this test fails.
func TestTheLaunchGateDoesNotCountARetireStillWaiting(t *testing.T) {
	home := retireWaitingHome(t)
	selectPacksWith(t, home, `"claude"`, `,"host_apply_on_launch":true`)
	t.Setenv("YOLO_VERSION", "")
	setGateTTY(t, false)

	survey := &hostApplySurvey{}
	var sink bytes.Buffer
	applyHostSurveyed(&sink, &sink, false, false, nil, survey)
	if survey.Changes() {
		t.Errorf("Changes(), the launch gate's counter, counts this home as changed: the waiting "+
			"retire was folded into it, or something else would change (a fixture bug):\n%s",
			sink.String())
	}
	if paths, _ := survey.DroppedRetires(); paths != 2 {
		t.Fatalf("the survey counts %d paths waiting to be retired, want 2:\n%s", paths, sink.String())
	}
	if got := hostApplyOutcome(survey, false); got != outcomeWouldComplete {
		t.Errorf("the dry run's outcome is %q, want %q", got, outcomeWouldComplete)
	}

	if lock := tryHostApplyLock(home); lock != nil {
		lock.Close() // the hook takes the lock; take it once so the hash ignores it
	}
	before := hashTree(t, home)
	stdin := &yesTrap{}
	var errw bytes.Buffer
	if !hostApplyGate(&errw, stdin, "claude") {
		t.Fatalf("a home whose only work is a waiting retire refused the launch:\n%s", errw.String())
	}
	if errw.Len() != 0 {
		t.Errorf("the gate printed over a home whose only work is a waiting retire:\n%s", errw.String())
	}
	if stdin.read {
		t.Errorf("the gate read stdin — the retire question, through a buffer:\n%s", errw.String())
	}
	if hashTree(t, home) != before {
		t.Errorf("the gate rendered or retired something:\n%s", errw.String())
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "only an --assert answered y retires it")
	mustExist(t, file, "only an --assert answered y retires it")
}

// AN --assert ANSWERED `n` keeps the dropped pack's paths, and its verdict says so. It ended
// "Nothing to apply — this home is up to date.", or with `packs` empty "No packs configured —
// nothing to apply, and nothing left to retire.", on the line after "not retired — 2 path(s) …
// are still in your home": the dry run over the same home now says an --assert has work, and the
// --assert called that home done.
func TestAnAssertThatDeclinesTheRetireSaysWhatStays(t *testing.T) {
	const stays = "2 paths from dropped packs are still in your home, not retired (above)."
	t.Run("with packs", func(t *testing.T) {
		defaultReport(t)
		retireWaitingHome(t)

		rc, report := applyWith(t, true, strings.NewReader("n\n"))
		if rc != 0 {
			t.Fatalf("a declined retire rc=%d, want 0 (declining is an answer):\n%s", rc, report)
		}
		if !strings.Contains(report, "not retired — 2 path(s)") {
			t.Fatalf("fixture bug: the --assert did not decline a retire:\n%s", report)
		}
		if got, want := verdictLine(report), "Nothing to apply — "+stays; got != want {
			t.Errorf("the verdict is\n%q\nwant\n%q\n%s", got, want, report)
		}
		if strings.Contains(report, "up to date") {
			t.Errorf("an --assert that kept a dropped pack's paths says the home is up to date:\n%s", report)
		}
	})
	t.Run("with no packs", func(t *testing.T) {
		defaultReport(t)
		home := retireWaitingHome(t)
		selectPacks(t, home, "")

		// The first --assert with `packs` empty also retires the composed briefing, unasked.
		_, report := applyWith(t, true, strings.NewReader("n\n"))
		if got, want := verdictLine(report), "No packs configured — nothing to apply; 1 destination(s) "+
			"retired, and "+stays; got != want {
			t.Errorf("the first verdict is\n%q\nwant\n%q\n%s", got, want, report)
		}
		_, report = applyWith(t, true, strings.NewReader("n\n"))
		if got, want := verdictLine(report), "No packs configured — nothing to apply; "+stays; got != want {
			t.Errorf("the second verdict is\n%q\nwant\n%q\n%s", got, want, report)
		}
		if strings.Contains(report, "nothing left to retire") {
			t.Errorf("an --assert that kept a dropped pack's paths says nothing is left to retire:\n%s", report)
		}
	})
}

// The kept retire joins an --assert's applied sentence, and counts its config keys in their own
// words.
func TestTheAppliedVerdictNamesADeclinedRetire(t *testing.T) {
	s := &hostApplySurvey{}
	s.Changed = append(s.Changed, hostChange{Kind: "briefing"})
	s.noteDeclinedRetire(1, 2)
	if got, want := hostApplyVerdict(s, true), "Applied: 1 briefing destination; 1 path and 2 config "+
		"keys from dropped packs are still in your home, not retired (above)."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	s = &hostApplySurvey{}
	s.noteDeclinedRetire(0, 1)
	if got, want := hostApplyVerdict(s, true), "Nothing to apply — 1 config key from dropped packs "+
		"is still in your home, not retired (above)."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	// Beside a failed stage, in place of "nothing else needed changing".
	s = &hostApplySurvey{}
	s.noteStageFailure(stageWrappers)
	s.noteDeclinedRetire(2, 0)
	if got, want := hostApplyVerdict(s, true), "Incomplete — the wrappers stage failed (above); 2 "+
		"paths from dropped packs are still in your home, not retired (above)."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	s.Changed = append(s.Changed, hostChange{Kind: "config"})
	if got, want := hostApplyVerdict(s, true), "Incomplete — the wrappers stage failed (above); "+
		"applied the rest: 1 config file; 2 paths from dropped packs are still in your home, not "+
		"retired (above)."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	// A dry run records no answer, and an --assert that confirmed records none either.
	if got := hostApplyVerdict(&hostApplySurvey{}, true); got != "Nothing to apply — this home is up to date." {
		t.Errorf("an --assert with nothing kept says %q", got)
	}
}
