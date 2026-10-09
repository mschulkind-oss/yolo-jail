package hostfloor

// patched_test.go is the floor's PATCHED-FORK arm (patched.go; docs/design/patched-forks.md §9,
// PF-D14, PF-D19, PF-D23, PF-D25): a fork whose host copy is the store's build of its GOOD BUILD,
// and whose install runs the fork's ADVANCE first. The advance and the good-build read are the
// caller's (internal/cli's floorAdvance and floorPatchedState, driven end to end in
// cli/hostfloorpatched_test.go); here they are a scripted stand-in, over buildStore's entries, so
// every rule of the arm is pinned on every machine, a Mac included.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

const (
	patchedRecipeOne = "recipe-one"
	patchedRecipeTwo = "recipe-two"
)

// patchedProgram is forkProgram as a PATCHED fork: the upstream as its source, a series in the fork
// pack.
func patchedProgram() Program {
	p := forkProgram()
	p.Install.Source = "git+https://example.invalid/forkcli?ref=main"
	p.Install.Patches = "patches"
	return p
}

// patchedFake is the caller's half of a patched fork: the state Patched reads, and an advance that
// changes it as a scripted step.
type patchedFake struct {
	t        *testing.T
	bs       *buildStore
	state    PatchedState
	advances int
	// installed is the floor's copy each Advance was handed as serving (nil for none), in order.
	installed []*Record
	// advance is what the next Advance does to the state; nil changes nothing (nothing pending, or
	// a newer upstream that does not take the series).
	advance func(*patchedFake)
}

// good is a good build of commit at recipe, its store entry made relocatable or not.
func (pf *patchedFake) good(commit, recipe, label string, relocatable bool) *PatchedBuild {
	pf.t.Helper()
	return &PatchedBuild{Commit: commit, Recipe: recipe, Label: label,
		Entry: pf.bs.addFor(patchedProgram(), commit, relocatable)}
}

// patchedWorld is a Linux world holding patchedProgram, its Patched and Advance the fake's.
func patchedWorld(t *testing.T) (*world, *patchedFake) {
	t.Helper()
	w := newLinuxWorld(t)
	pf := &patchedFake{t: t, bs: newBuildStore(t),
		state: PatchedState{Recipe: patchedRecipeOne, Reason: "fork forkpack/forkcli has no build on this machine yet"}}
	w.floor.Patched = func(p Program) PatchedState {
		if p.Install.ForkedBy != "forkpack" || !p.Install.IsPatchedFork() {
			t.Errorf("Patched asked about %s (%+v)", p.Bin(), p.Install)
		}
		return pf.state
	}
	w.floor.Advance = func(_ context.Context, p Program, installed *Record) PatchedState {
		pf.advances++
		pf.installed = append(pf.installed, installed)
		if pf.advance != nil {
			pf.advance(pf)
		}
		return pf.state
	}
	// A patched fork has no pin: anything asking for one is the plain fork's arm reached by mistake.
	w.floor.ForkPin = func(p Program) (string, string) {
		t.Errorf("the floor asked a patched fork's pin")
		return "", ""
	}
	return w, pf
}

func TestFloorPatchedPreparationSelectsOnceWithoutFloorWrites(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	first := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	later := pf.good(forkCommitTwo, patchedRecipeOne, "v1.1.0 (22222222) + 2 patches", true)
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: first}
	advances := 0
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		advances++
		return PatchedState{Recipe: patchedRecipeOne, Good: first}
	}
	prepared, err := w.floor.PreparePatched(context.Background(), p, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if advances != 1 {
		t.Fatalf("prepare ran %d advances, want one", advances)
	}
	if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
		t.Fatalf("preparation wrote floor artifacts: %v", err)
	}
	pf.state.Good = later
	st, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	if err != nil || outcome != Installed || st.Record == nil || st.Record.Revision != forkCommitOne ||
		st.Record.Capture != first.Entry.Key || advances != 1 {
		t.Fatalf("EnsurePrepared did not consume the selected Good exactly once: status=%+v outcome=%q advances=%d err=%v",
			st, outcome, advances, err)
	}
}

func TestFloorPatchedPreparationWithoutAdvanceHasNoFloorWrites(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	advances := 0
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		advances++
		return pf.state
	}
	if _, err := w.floor.PreparePatched(context.Background(), p, false); err != nil {
		t.Fatalf("cached-only preparation: %v", err)
	}
	if advances != 0 {
		t.Errorf("cached-only preparation ran %d advances", advances)
	}
	if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
		t.Errorf("cached-only preparation wrote floor artifacts: %v", err)
	}
}

func TestFloorPatchedPreparationRejectsChangedBindingBeforeWrites(t *testing.T) {
	for _, change := range []string{"program", "floor", "recipe"} {
		t.Run(change, func(t *testing.T) {
			w, pf := patchedWorld(t)
			p := patchedProgram()
			good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
			pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good}
			prepared, err := w.floor.PreparePatched(context.Background(), p, false)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			target := w.floor
			checkProgram := p
			switch change {
			case "program":
				checkProgram.Install.Build += " && changed"
			case "floor":
				other := *w.floor
				target = &other
			case "recipe":
				pf.state.Recipe = patchedRecipeTwo
			}
			if _, _, err := target.EnsurePrepared(context.Background(), checkProgram, prepared); err == nil {
				t.Fatalf("changed %s binding was accepted", change)
			}
			if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
				t.Errorf("rejected %s binding wrote floor artifacts: %v", change, err)
			}
		})
	}
}

func TestFloorPatchedPreparationRefusesUnresolvedAuthorityBesideGoodBuild(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good}
	w.floor.AllowPatchFailures = func() bool { return true }
	w.floor.ResolvePatched = func(context.Context, Program, *Record, bool) PatchedState {
		return PatchedState{Recipe: patchedRecipeOne, Good: good, AuthorityError: "detached evidence unavailable",
			OperationError: "independent operation error"}
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	var failure *packsrc.PatchFailure
	if err == nil || errors.As(err, &failure) || outcome != "" ||
		!strings.Contains(err.Error(), "detached evidence unavailable") || !strings.Contains(err.Error(), "independent operation error") {
		t.Fatalf("unresolved authority or operation error was waived by a compatible Good build: status=%+v outcome=%q err=%v",
			st, outcome, err)
	}
	if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
		t.Errorf("unresolved authority preparation wrote floor artifacts: %v", err)
	}
}

func TestFloorPatchFatalReturnedFailureDoesNotAdoptPreAdvanceGood(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	first := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: first}
	failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: patchedRecipeOne,
		Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch"}
	w.floor.AllowPatchFailures = func() bool { return true }
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		return PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure}
	}
	_, outcome, err := w.floor.Ensure(context.Background(), p)
	var got *packsrc.PatchFailure
	if !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || outcome != "" {
		t.Fatalf("pre-advance Good was substituted for the failure operation's missing selection: outcome=%q err=%v", outcome, err)
	}
	if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
		t.Errorf("unselected Good was installed after returned failure: %v", err)
	}
}

func TestFloorPatchFatalRaisedNodeFloorIsNotBypassCompatible(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	launcher, err := os.ReadFile(w.floor.Launcher(p.Bin()))
	if err != nil {
		t.Fatal(err)
	}
	raised := p
	raised.Install.NodeFloor = "99.1"
	failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: patchedRecipeOne,
		Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch"}
	pf.state.PatchFailure = failure
	w.floor.AllowPatchFailures = func() bool { return true }
	st, outcome, err := w.floor.Ensure(context.Background(), raised)
	var got *packsrc.PatchFailure
	if !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || outcome == Current || st.Record == nil {
		t.Fatalf("literal patch bypass accepted a copy below the selected node_floor: status=%+v outcome=%q err=%v",
			st, outcome, err)
	}
	after, readErr := os.ReadFile(w.floor.Launcher(p.Bin()))
	if readErr != nil || string(after) != string(launcher) {
		t.Errorf("Node-incompatible patch refusal changed the installed copy: err=%v", readErr)
	}
}

func TestFloorPatchFatalLiteralBypassCanInstallOnlyTheSelectedCompleteStoreBuild(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good}
	failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: patchedRecipeOne,
		Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch"}
	pf.state.PatchFailure = failure
	advances := 0
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		advances++
		return pf.state
	}
	w.floor.AllowPatchFailures = func() bool { return true }
	if _, err := os.Stat(w.floor.Dir); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly created floor artifacts before Ensure: %v", err)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Disposition != Provisioned || st.Record == nil ||
		st.Record.Capture != good.Entry.Key || advances != 0 {
		t.Fatalf("literal skip did not install the selected admitted build without another advance: status=%+v outcome=%q advances=%d err=%v",
			st, outcome, advances, err)
	}
	if pf.state.PatchFailure != failure || pf.state.Good == nil || pf.state.Good.Entry == nil ||
		pf.state.Good.Entry.Key != good.Entry.Key {
		t.Errorf("install mutated the failure or selected Good authority: state=%+v", pf.state)
	}
}

// A returned classified failure remains authoritative even when persistence did not retain it: the
// advance result is the operation's answer, not something a subsequent offline Status can replace.
func TestFloorPatchFatalReturnedAdvanceFailureSurvivesAbsentPersistence(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	before, err := os.ReadFile(w.floor.Launcher(p.Bin()))
	if err != nil {
		t.Fatal(err)
	}
	beforeRecord, err := os.ReadFile(w.floor.recordPath(p.Bin()))
	if err != nil {
		t.Fatal(err)
	}
	failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: "series-digest",
		Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch",
		Paths: []string{"conflict.txt"}}
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		return PatchedState{Recipe: pf.state.Recipe, Good: pf.state.Good, PatchFailure: failure}
	}
	_, outcome, err := w.floor.Ensure(context.Background(), p)
	var got *packsrc.PatchFailure
	if !errors.As(err, &got) || !reflect.DeepEqual(got, failure) {
		t.Fatalf("Ensure lost returned-only typed failure: outcome=%q err=%v", outcome, err)
	}
	if outcome != "" {
		t.Errorf("failure claimed successful outcome %q", outcome)
	}
	after, readErr := os.ReadFile(w.floor.Launcher(p.Bin()))
	afterRecord, recordErr := os.ReadFile(w.floor.recordPath(p.Bin()))
	if readErr != nil || recordErr != nil || string(after) != string(before) || string(afterRecord) != string(beforeRecord) {
		t.Errorf("returned-only failure changed the installed copy: launcher err=%v record err=%v", readErr, recordErr)
	}
}

func TestFloorPatchFatalCompatibilityBypassKeepsOnlyAnIntactCurrentSeriesCopy(t *testing.T) {
	for _, cell := range []string{"current", "lags-good", "store-entry-gone"} {
		t.Run(cell, func(t *testing.T) {
			w, pf := patchedWorld(t)
			pf.advance = firstGood
			p := patchedProgram()
			if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
				t.Fatalf("initial install: %v", err)
			}
			before, err := os.ReadFile(w.floor.Launcher(p.Bin()))
			if err != nil {
				t.Fatal(err)
			}
			if cell == "lags-good" {
				pf.state.Good = &PatchedBuild{Commit: forkCommitTwo, Recipe: patchedRecipeOne,
					Label: "v1.1.0 (22222222) + 2 patches"}
			}
			if cell == "store-entry-gone" {
				pf.state.Good.Entry = nil
			}
			failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: patchedRecipeOne,
				Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch"}
			w.floor.AllowPatchFailures = func() bool { return true }
			w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
				return PatchedState{Recipe: patchedRecipeOne, Good: pf.state.Good, PatchFailure: failure}
			}
			st, outcome, err := w.floor.Ensure(context.Background(), p)
			if err != nil || outcome != Current || st.Disposition != Provisioned {
				t.Fatalf("compatible literal bypass: status=%s outcome=%q err=%v", st.Disposition, outcome, err)
			}
			after, err := os.ReadFile(w.floor.Launcher(p.Bin()))
			if err != nil || string(after) != string(before) {
				t.Errorf("compatibility skip changed the existing floor copy: err=%v", err)
			}
		})
	}
}

func TestFloorPatchFatalOperationErrorSurvivesCompatibleBypass(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	failure := &packsrc.PatchFailure{Owner: "forkpack/forkcli", Series: patchedRecipeOne,
		Target: packsrc.ListEntry{Commit: "upstream-target"}, Kind: "conflict", Member: "0001-conflict.patch"}
	w.floor.AllowPatchFailures = func() bool { return true }
	w.floor.Advance = func(context.Context, Program, *Record) PatchedState {
		return PatchedState{Recipe: patchedRecipeOne, Good: pf.state.Good, PatchFailure: failure,
			OperationError: "the independent fetch check failed"}
	}
	_, outcome, err := w.floor.Ensure(context.Background(), p)
	var got *packsrc.PatchFailure
	if outcome != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || !strings.Contains(err.Error(), "independent fetch check failed") {
		t.Fatalf("the compatibility skip waived an independent operation error: outcome=%q err=%v", outcome, err)
	}
}

// firstGood is the advance a first install runs: it builds v1.0.0 and makes it the good build.
func firstGood(pf *patchedFake) {
	pf.state.Good = pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.state.Reason = ""
}

// THE FIRST `yolo host -- <bin>` OF A PATCHED FORK runs its advance, then installs the good build
// the advance made: the record names the good build's commit, its recipe (the series in it) and its
// label, its declaration is the repository with no ref, and the floor's copy is the build, started
// on the floor's own Node. Status, which a dry run and `yolo check` ask, never advances.
func TestAPatchedForkIsInstalledFromTheGoodBuildItsAdvanceLeaves(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()

	st := w.floor.Status(p)
	if st.Disposition != Missing || !strings.Contains(st.Reason, "the install checks fork pack forkpack's upstream, "+
		"replays its patch series and builds it") || pf.advances != 0 {
		t.Fatalf("before any install: %s (%s), %d advances", st.Disposition, st.Reason, pf.advances)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || pf.advances != 1 {
		t.Fatalf("Ensure: %s %v, %d advances\n%s", outcome, err, pf.advances, w.out.String())
	}
	rec := st.Record
	if rec.Via != "source" || rec.Declared != "git+https://example.invalid/forkcli" || rec.Revision != forkCommitOne ||
		rec.Recipe != patchedRecipeOne || rec.Version != "v1.0.0 (11111111) + 2 patches" ||
		rec.Capture != pf.state.Good.Entry.Key || rec.Node != w.version {
		t.Errorf("record = %+v", rec)
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "node:"+rec.Entry+" --version\n" {
		t.Fatalf("running the floor's forkcli with no environment: %q %v", got, err)
	}
	if out := w.out.String(); !strings.Contains(out, "materialized forkcli from fork build "+rec.Capture+
		" (v1.0.0 (11111111) + 2 patches)") {
		t.Errorf("the install does not say what it put in place:\n%s", out)
	}
}

// THE REFRESH ARM IS THE ADVANCE, UNDER UpdatesAllowed (PF-D14, PF-D19): a current entry advances on
// every Ensure, the advance running no git inside its own throttle, and a moved good build is
// installed — Updated, the copy it replaces kept for an agent still loading it. `agent_updates` off
// for the fork pack, or for its base, runs no advance on a current entry; the good build that moved
// meanwhile (a jail launch's advance) is still installed, its advance building nothing under the hold.
func TestTheRefreshArmAdvancesAPatchedForkUnderUpdatesAllowed(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	first := floorRecordOf(t, w, "forkcli")

	pf.advance = nil
	if _, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Current || pf.advances != 2 {
		t.Fatalf("a current entry: %s %v, %d advances, want the refresh arm's advance and nothing installed",
			outcome, err, pf.advances)
	}
	pf.advance = func(pf *patchedFake) {
		pf.state.Good = pf.good(forkCommitTwo, patchedRecipeOne, "v1.1.0 (22222222) + 2 patches", true)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Updated || st.Record.Revision != forkCommitTwo || pf.advances != 3 {
		t.Fatalf("a moved good build: %s %v %+v, %d advances\n%s", outcome, err, st.Record, pf.advances, w.out.String())
	}
	if _, err := os.Stat(first.Dir); err != nil {
		t.Errorf("the install the move replaced is gone at once (%v): an agent started a moment ago may load it", err)
	}

	for _, held := range []string{"forkpack", "basepack"} {
		pf.advance = func(pf *patchedFake) { t.Errorf("a current entry advanced while %s was held", held) }
		w.floor.UpdatesAllowed = func(pack string) bool { return pack != held }
		before := pf.advances
		if _, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Current || pf.advances != before {
			t.Errorf("held by %s: %s %v, %d advances, want no advance", held, outcome, err, pf.advances-before)
		}
	}
	// A JAIL LAUNCH MOVED THE GOOD BUILD while the fork pack is held: the entry is pending, so its
	// install runs the advance (which, held, builds nothing) and installs that build.
	pf.state.Good = pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.advance = nil
	before := pf.advances
	if st, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Updated ||
		st.Record.Revision != forkCommitOne || pf.advances != before+1 {
		t.Errorf("a moved good build under a hold: %s %v %+v, %d advances", outcome, err, st.Record, pf.advances-before)
	}
}

// THE ADVANCE IS HANDED THE FLOOR'S COPY THAT SERVES (PF-D55): nothing on a first install; the
// installed good build on a refresh; the installed copy a newer good build has moved past, which a
// failed install keeps; and nothing once the user's edit makes that copy a near-miss, which a failed
// install removes.
func TestThePatchedAdvanceIsHandedTheFloorsCopyThatServes(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.advance = nil
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.state.Good = &PatchedBuild{Commit: forkCommitTwo, Recipe: patchedRecipeOne, Label: "v1.1.0 (22222222) + 2 patches"}
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.state = PatchedState{Recipe: patchedRecipeTwo, Reason: "the series changed"}
	_, _, _ = w.floor.Ensure(context.Background(), p)
	if len(pf.installed) != 4 {
		t.Fatalf("%d advances, want 4", len(pf.installed))
	}
	if pf.installed[0] != nil {
		t.Errorf("a first install's advance was handed %+v, want nothing", pf.installed[0])
	}
	for i, why := range []string{"the installed good build", "the copy a newer good build moved past"} {
		if got := pf.installed[i+1]; got == nil || got.Revision != forkCommitOne || got.Recipe != patchedRecipeOne {
			t.Errorf("the advance over %s was handed %+v, want the floor's v1.0.0", why, got)
		}
	}
	if pf.installed[3] != nil {
		t.Errorf("the advance over the user's edit was handed %+v, a near-miss, want nothing", pf.installed[3])
	}
}

// A NEWER UPSTREAM WHOSE INSTALL FAILS KEEPS THE INSTALLED COPY (PF-D8): the copy is a build of the
// series as it stands, at an older upstream commit, so a failed reinstall of the moved good build —
// its entry gone from the store before the floor could copy it — leaves it serving.
func TestAMovedGoodBuildWhoseInstallFailsKeepsTheInstalledCopy(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.advance = func(pf *patchedFake) {
		pf.state.Good = &PatchedBuild{Commit: forkCommitTwo, Recipe: patchedRecipeOne, Label: "v1.1.0 (22222222) + 2 patches"}
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Kept || st.Record.Revision != forkCommitOne {
		t.Fatalf("a newer upstream whose install failed: %s %v %+v, want the installed copy kept\n%s",
			outcome, err, st.Record, w.out.String())
	}
	if _, err := os.Stat(w.floor.Launcher("forkcli")); err != nil {
		t.Errorf("the kept copy lost its launcher: %v", err)
	}
	if !strings.Contains(w.out.String(), "running the installed v1.0.0 (11111111) + 2 patches") {
		t.Errorf("the launch does not say what keeps running:\n%s", w.out.String())
	}
}

// THE USER'S OWN FAILED EDIT IS NOT HELD (PF-D23): an edited series or recipe that does not build
// leaves nothing serving, so the installed copy — of the old recipe, a near-miss — leaves bin/ and the
// install fails with the advance's reason, which names the revert.
func TestAUsersFailedEditRemovesThePatchedForksInstalledCopy(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.state = PatchedState{Recipe: patchedRecipeTwo, Reason: "fork forkpack/forkcli's patch series or build recipe " +
		"changed since its good build v1.0.0 (11111111) — reverting the edit brings that build back"}
	pf.advance = nil // the edited series did not build
	if st := w.floor.Status(p); st.Disposition != Provisioned || !strings.Contains(st.Pending, "changed since its good build") {
		t.Fatalf("an edit: %s pending %q, want the installed copy pending", st.Disposition, st.Pending)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err == nil || outcome == Kept || outcome == Current {
		t.Fatalf("a failed edit: %s %v, want the install to fail", outcome, err)
	}
	if !strings.Contains(err.Error(), "reverting the edit brings that build back") ||
		!strings.Contains(w.out.String(), "is not the build the pack now asks for, so the floor no longer runs it") {
		t.Errorf("err %v\n%s", err, w.out.String())
	}
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the old recipe's copy is still in bin/ (%v)", lerr)
	}
	if st.Disposition == Provisioned {
		t.Errorf("status after the failure = %s, want the old copy not reported as held", st.Disposition)
	}
}

// A MOVED GOOD BUILD THAT CANNOT LEAVE THE JAIL'S HOME HAS NO FLOOR ENTRY (§9), whatever the floor
// ran before it: the install's no-entry answer takes the installed copy out of bin/ too, and says
// which home the build was made for.
func TestAMovedGoodBuildThatCannotLeaveTheJailHasNoFloorEntry(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pf.advance = func(pf *patchedFake) {
		pf.state.Good = pf.good(forkCommitTwo, patchedRecipeOne, "v1.1.0 (22222222) + 2 patches", false)
	}
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry ||
		!strings.Contains(st.Reason, "built for the jail's home, /home/agent") {
		t.Fatalf("a moved good build that cannot move: %s (%s), %v", st.Disposition, st.Reason, err)
	}
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the floor still runs the copy the good build moved past (%v)", lerr)
	}
	if !strings.Contains(w.out.String(), "so the floor no longer runs the installed v1.0.0 (11111111) + 2 patches") {
		t.Errorf("the removal is not said:\n%s", w.out.String())
	}
}

// THE COPY IS CHECKED WHOLE AFTER IT ENDS (PF-D51): an entry another advance's move reaped while the
// floor copied it — its completion marker the reap's first removal — fails the install, never
// installed as a half-copied program.
func TestTheFloorsCopyOfAPatchedBuildIsCheckedWholeAfterItEnds(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	pf.advance = func(pf *patchedFake) {
		firstGood(pf)
		// The reap's first step, as capture's reapEntry takes it: the marker, then the tree.
		markers, _ := filepath.Glob(filepath.Join(pf.state.Good.Entry.Root, ".yolo-capture-*"))
		for _, m := range markers {
			must(t, os.Remove(m))
		}
	}
	_, _, err := w.floor.Ensure(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "left the capture store while it was copied") {
		t.Fatalf("Ensure over a reaped entry = %v, want the install refused\n%s", err, w.out.String())
	}
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("a copy of a reaped entry was installed (%v)", lerr)
	}
	if dirs, _ := os.ReadDir(w.floor.programsDir("forkcli")); len(dirs) != 0 {
		t.Errorf("the failed copy left %d install directories", len(dirs))
	}
}

// A MACHINE THAT CANNOT BUILD runs no advance — its build boots a jail — but still installs a good
// build its store holds (a jail launch's, say); with none, the program has no floor entry here, and
// the reason names the runtime that builds it.
func TestAPatchedForkOnAMachineThatCannotBuildInstallsOnlyWhatTheStoreHolds(t *testing.T) {
	w, pf := patchedWorld(t)
	w.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	pf.advance = func(*patchedFake) { t.Error("a machine that cannot build ran the advance") }
	p := patchedProgram()
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || !strings.Contains(st.Reason, "there is no build of forkcli from fork pack "+
		"forkpack's patch series on this machine, and no container runtime (podman) is on PATH — install one") {
		t.Fatalf("no build and no runtime: %s (%s), %v", st.Disposition, st.Reason, err)
	}
	firstGood(pf)
	if st, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Installed ||
		st.Record.Revision != forkCommitOne {
		t.Fatalf("a good build in the store: %s %v %+v", outcome, err, st.Record)
	}
}

// ON A MAC A PATCHED FORK HAS NO FLOOR ENTRY, as no fork has (FP-D16: the build is of the jail's
// platform), and the reason names the next step — a jail on a container backend — and nothing
// advances. A series that cannot be read is no floor entry on Linux too, with the series' own reason.
func TestAPatchedForkWithNoFloorEntryNamesWhereItRuns(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = func(*patchedFake) { t.Error("a patched fork with no floor entry ran the advance") }
	w.floor.GOOS = "darwin"
	p := patchedProgram()
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || !strings.Contains(st.Reason, "in a Linux capture jail, and this machine is darwin/") ||
		!strings.Contains(st.Reason, "run it in a jail instead (`yolo -- forkcli`, on a container backend") {
		t.Errorf("on a Mac: %s (%s), %v", st.Disposition, st.Reason, err)
	}
	w.floor.GOOS = "linux"
	pf.state = PatchedState{Reason: "patches/0001-a.patch is not a git format-patch member"}
	st, _, err = w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || !strings.Contains(st.Reason, "whose series cannot be read: patches/0001-a.patch") {
		t.Errorf("an unreadable series: %s (%s), %v", st.Disposition, st.Reason, err)
	}
}

// A COPY FROM ANOTHER REPOSITORY IS A NEAR-MISS (PF-D52): the patched recipe hashes the build and
// the series, never the repository, so the declaration is what tells a copy of another source apart.
// A switch of the fork's source whose install fails removes the installed copy rather than keeping
// it.
func TestAPatchedForksCopyFromAnotherRepositoryIsNotKept(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	moved := p
	moved.Install.Source = "git+https://example.invalid/otherfork?ref=main"
	pf.advance = func(pf *patchedFake) {
		// The same series and build at the same commit, from the other repository: not built yet.
		pf.state.Good = &PatchedBuild{Commit: forkCommitOne, Recipe: patchedRecipeOne, Label: "v1.0.0 (11111111) + 2 patches"}
	}
	st, outcome, err := w.floor.Ensure(context.Background(), moved)
	if err == nil || outcome == Kept || outcome == Current {
		t.Fatalf("another repository's install failed: %s %v, want the old repository's copy not kept\n%s",
			outcome, err, w.out.String())
	}
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the old repository's copy is still in bin/ (%v)", lerr)
	}
	if st.Disposition == Provisioned {
		t.Errorf("status after the failure = %s, want the old copy not reported as held", st.Disposition)
	}
}

// A RAISED node_floor LEAVES A PATCHED FORK'S COPY PENDING, as it leaves any Node program's: the copy
// is the good build, on a Node below what the pack now asks for, so the next install runs it on one
// that meets it.
func TestARaisedNodeFloorLeavesAPatchedForksCopyPending(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	raised := p
	raised.Install.NodeFloor = "99.1"
	if st := w.floor.Status(raised); st.Disposition != Provisioned ||
		!strings.Contains(st.Pending, "below the pack's node_floor 99.1") {
		t.Errorf("a raised node_floor: %s, pending %q, want the copy pending on its Node", st.Disposition, st.Pending)
	}
}

// A GOOD BUILD THAT CANNOT LEAVE THE JAIL'S HOME HAS NO FLOOR ENTRY BEFORE ANY INSTALL (§9): the
// status a dry run and `yolo check` read says so, rather than that an install would put it in place.
func TestAPatchedForkWhoseGoodBuildCannotLeaveTheJailHasNoFloorEntry(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.state.Good = pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", false)
	pf.state.Reason = ""
	st := w.floor.Status(patchedProgram())
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "built for the jail's home, /home/agent") {
		t.Errorf("a good build bound to the jail's home: %s (%s), want no floor entry", st.Disposition, st.Reason)
	}
	if pf.advances != 0 {
		t.Errorf("Status advanced %d times", pf.advances)
	}
}

// TWO LAUNCHES TOGETHER INSTALL A PATCHED FORK ONCE: an install that waited for the floor's lock
// reads the status again under it, finds what the holder installed, and installs nothing itself.
func TestAPatchedInstallThatWaitedForTheLockFindsTheHoldersInstall(t *testing.T) {
	w, pf := patchedWorld(t)
	pf.advance = firstGood
	p := patchedProgram()
	if err := w.floor.ensureDir("bin", "programs", "records", "locks"); err != nil {
		t.Fatal(err)
	}
	held, err := acquire(w.floor.lockPath(p.Bin()), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		outcome Outcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, outcome, err := w.floor.Ensure(context.Background(), p)
		done <- result{outcome, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(w.out.String(), "waiting for pid") {
		if time.Now().After(deadline) {
			held.release()
			t.Fatalf("the install never waited for the lock:\n%s", w.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// THE HOLDER installs the good build, as the launch that held the lock would.
	if _, err := w.floor.install(context.Background(), p); err != nil {
		held.release()
		t.Fatal(err)
	}
	held.release()
	r := <-done
	if r.err != nil || r.outcome != Current {
		t.Fatalf("the waiter: %s %v, want the holder's install found current\n%s", r.outcome, r.err, w.out.String())
	}
	b, _ := os.ReadFile(w.floor.receiptsPath())
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("receipts.jsonl has %d lines, want the holder's one install:\n%s", n, b)
	}
}

// A GOOD BUILD GONE FROM THE STORE STOPS WITH THE ACT THAT BUILDS IT AGAIN: a moved good build the
// floor cannot copy keeps the installed copy, and the line says what puts the build back — `yolo
// capture <bin>` on a machine that builds, a container runtime on one that cannot.
func TestAGoneGoodBuildsLineNamesWhatBuildsItAgain(t *testing.T) {
	for _, tc := range []struct{ name, unavailable, want string }{
		{"a machine that builds", "", "`yolo capture forkcli` builds it, and the next `yolo host -- forkcli` installs it"},
		{"a machine that cannot", "no container runtime (podman) is on PATH", "no container runtime (podman) is on " +
			"PATH — install one (`yolo check` names how on this machine) and the next `yolo host` launch builds it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, pf := patchedWorld(t)
			pf.advance = firstGood
			p := patchedProgram()
			if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			pf.advance = nil
			pf.state.Good = &PatchedBuild{Commit: forkCommitTwo, Recipe: patchedRecipeOne, Label: "v1.1.0 (22222222) + 2 patches"}
			if tc.unavailable != "" {
				w.floor.CaptureUnavailable = func() string { return tc.unavailable }
			}
			first := len(w.out.String())
			if _, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Kept {
				t.Fatalf("%s %v, want the installed copy kept\n%s", outcome, err, w.out.String())
			}
			out := w.out.String()[first:]
			if !strings.Contains(out, "good build v1.1.0 (22222222) + 2 patches is gone from the capture store") ||
				!strings.Contains(out, tc.want) {
				t.Errorf("the kept line does not name what builds it again (%q):\n%s", tc.want, out)
			}
			if strings.Contains(out, "installing forkcli into yolo's floor (") {
				t.Errorf("an install of a build that is not in the store was started:\n%s", out)
			}
		})
	}
}

// floorRecordOf is w's floor record of bin.
func floorRecordOf(t *testing.T, w *world, bin string) *Record {
	t.Helper()
	rec, ok := w.floor.Records()[bin]
	if !ok {
		t.Fatalf("the floor holds no record of %s", bin)
	}
	return rec
}
