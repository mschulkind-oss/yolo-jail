package cli

// backgroundadvance_test.go pins THE BACKGROUND ADVANCE itself (backgroundadvance.go;
// docs/design/pi-extension-store-builds.md §7.4, XB-D19): its keys resolved from the user config, a
// held lock skipped and never waited for, a build jail not known gone kept with its candidate
// pending, and its verb and signals.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// bgArgs are a background advance's flags for keys, on podman.
func bgArgs(keys ...string) backgroundArgs {
	return backgroundArgs{runtime: "podman", platform: patchedTestPlatform, keys: keys}
}

// outcomeOf reads key's outcome record, failing the test when there is none.
func outcomeOf(t *testing.T, key string) backgroundOutcome {
	t.Helper()
	o, err := readBackgroundOutcome(backgroundOutcomePath(key))
	if err != nil {
		t.Fatalf("%s has no outcome record: %v", key, err)
	}
	return o
}

// readyForABackgroundAdvance is the pool fixture with both keys built at a first launch, then a newer
// upstream and the clock past the check's interval: a check is due and finds a candidate.
func readyForABackgroundAdvance(t *testing.T) *poolFixture {
	t.Helper()
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	return pf
}

// THE SELECTION IS THE USER CONFIG'S (XB-D19, the selection decision): each key the argv names is
// resolved there, as `yolo capture` resolves it — never the launch's staged tree — and advanced in
// the background mode, with nothing handed; a key the config does not carry is recorded unresolved.
// Red if the background advance stops resolving its keys itself, or stops setting the mode.
func TestTheBackgroundAdvanceResolvesItsKeysFromTheUserSelection(t *testing.T) {
	pf := newPoolFixture(t)
	want := map[string]packload.Fork{}
	for _, f := range packload.PatchedTrees(selectConfiguredHostPacks().packs) {
		want[f.Key()] = f
	}
	var mu sync.Mutex
	seen := map[string]advanceOptions{}
	roots := map[string]string{}
	prev := treeAdvance
	treeAdvance = func(f packload.Fork, o advanceOptions) advanceResult {
		mu.Lock()
		defer mu.Unlock()
		seen[f.Key()], roots[f.Key()] = o, f.Root
		return advanceResult{}
	}
	t.Cleanup(func() { treeAdvance = prev })
	var out bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA, "nope/missing"), &out)
	o, ok := seen[poolKeyA]
	if !ok || len(seen) != 1 {
		t.Fatalf("the advance ran for %v, want %s alone:\n%s", seen, poolKeyA, out.String())
	}
	if !o.background || !o.launch || o.hand != nil || o.runtime != "podman" || o.platform != patchedTestPlatform ||
		o.ctx == nil || o.pool == nil {
		t.Errorf("the advance's options = %+v, want the background mode in a pool, handing nothing", o)
	}
	if roots[poolKeyA] != want[poolKeyA].Root || roots[poolKeyA] == "" {
		t.Errorf("the key resolved to %q, want the user config's %q", roots[poolKeyA], want[poolKeyA].Root)
	}
	if got := outcomeOf(t, poolKeyA); got.State != bgNothing || got.Pid != 0 {
		t.Errorf("the key's outcome = %+v, want nothing to do", got)
	}
	if got := outcomeOf(t, "nope/missing"); got.State != bgUnresolved {
		t.Errorf("the unknown key's outcome = %+v, want unresolved", got)
	}
	_ = pf
}

// A KEY ANOTHER BACKGROUND ADVANCE RUNS is skipped at once, its record left alone. Red if the key's
// background lock is waited for or ignored.
func TestTheBackgroundAdvanceSkipsAKeyWhoseLockIsHeld(t *testing.T) {
	newPoolFixture(t)
	lk, err := pidlock.Acquire(backgroundKeyLock(poolKeyA), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	var ran []string
	prev := treeAdvance
	treeAdvance = func(f packload.Fork, o advanceOptions) advanceResult {
		ran = append(ran, f.Key())
		return advanceResult{}
	}
	t.Cleanup(func() { treeAdvance = prev })
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &out); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the background advance waited for a held key lock")
	}
	if len(ran) != 0 || !strings.Contains(out.String(), "skipped — another background advance") {
		t.Errorf("a held key ran %v:\n%s", ran, out.String())
	}
	if _, err := os.Stat(backgroundOutcomePath(poolKeyA)); err == nil {
		t.Error("a skipped key's record was written")
	}
}

// A CHECK WHOSE RECORD LOCK ANOTHER PROCESS HOLDS (a foreground launch's check, say) is skipped at
// once: nothing is fetched or recorded, and the outcome is skipped. Red if the background check waits,
// or records the held lock as a failure.
func TestABackgroundCheckSkipsAHeldRecordLock(t *testing.T) {
	readyForABackgroundAdvance(t)
	store := patchedAdvanceStore(true)
	recPath := store.CheckRecordPath(poolKeyA)
	before, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = store.WithCheckRecord(poolKeyA, nil, func(*packsrc.CheckRecord, error, func() error) (bool, error) {
			close(holding)
			<-release
			return false, nil
		})
	}()
	<-holding
	var out bytes.Buffer
	done := make(chan struct{})
	start := time.Now()
	go func() { runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &out); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(release)
		<-done
		t.Fatalf("the background check waited for a held record lock:\n%s", out.String())
	}
	close(release)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the skip took %s", d)
	}
	if got := outcomeOf(t, poolKeyA); got.State != bgSkipped {
		t.Errorf("the outcome = %+v, want skipped:\n%s", got, out.String())
	}
	after, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the record changed:\nbefore %s\nafter  %s", before, after)
	}
}

// A BUILD WHOSE LOCK ANOTHER BUILD HOLDS — a foreground launch building the same candidate — is
// skipped, never waited for, and builds nothing. Red if the background build waits on the lock.
func TestABackgroundBuildSkipsAHeldBuildLock(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	// BOTH foreground builds wait inside their build jails, so neither holds the repository's mirror
	// lock for its replay when the background advance walks: the build lock is all that is held.
	entered, release := make(chan struct{}), make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(2)
	go func() { arrived.Wait(); close(entered) }()
	var builds sync.Map
	pf.child = func(_ context.Context, b forkBuild) int {
		builds.Store(b.Fork.Key(), true)
		arrived.Done()
		<-release
		return 0
	}
	launched := make(chan struct{})
	go func() { pf.launch(t, "podman", &run.ActInterrupt{}); close(launched) }()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the foreground build never started")
	}
	builds.Delete(poolKeyA)
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &out); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(release)
		<-done
		<-launched
		t.Fatalf("the background build waited for a held build lock:\n%s", out.String())
	}
	close(release)
	<-launched
	if _, built := builds.Load(poolKeyA); built {
		t.Errorf("the background advance built a key whose build lock was held:\n%s", out.String())
	}
	if got := outcomeOf(t, poolKeyA); got.State != bgSkipped || !strings.Contains(out.String(), "skipped — another build of this fork is running") {
		t.Errorf("the outcome = %+v, want skipped for the build lock:\n%s", got, out.String())
	}
}

// A BUILD JAIL NOT KNOWN GONE — the runtime could not say — keeps its staging, records no failed build
// and leaves the candidate pending, which the next launch says. Red if the background advance reports
// it as a failure, or loses the retained state.
func TestAnUnknownRunningBuildJailKeepsItsStagingAndTheCandidatePending(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	prevWait, prevProbe := forkBuildGoneWait, probeForkBuildContainer
	forkBuildGoneWait = 0
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, false }
	t.Cleanup(func() { forkBuildGoneWait, probeForkBuildContainer = prevWait, prevProbe })
	var staged string
	prevChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, d time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		// A build whose jail ran on podman and returned, and whose container the runtime cannot account for.
		staged = staging
		if err := writeForkBuildRuntime(staging, "podman"); err != nil {
			t.Error(err)
		}
		rc, bound := prevChild(ctx, d, staging, b, s, color)
		if err := writeForkBuildRunReturned(staging); err != nil {
			t.Error(err)
		}
		return rc, bound
	}
	t.Cleanup(func() { forkBuildChild = prevChild })
	before, err := patchedAdvanceStore(true).LoadCheckRecord(poolKeyA)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &out)
	got := outcomeOf(t, poolKeyA)
	if got.State != bgRetained || !strings.Contains(got.Retained, " pending — ") {
		t.Fatalf("the outcome = %+v, want retained:\n%s", got, out.String())
	}
	if _, err := os.Stat(staged); staged == "" || err != nil {
		t.Errorf("the retained staging %q is gone: %v", staged, err)
	}
	rec, err := patchedAdvanceStore(true).LoadCheckRecord(poolKeyA)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range rec.Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a retained build recorded a failed build:\n%s", out.String())
		}
	}
	if rec.Good == nil || before.Good == nil || rec.Good.Entry != before.Good.Entry {
		t.Errorf("the good build moved: %+v → %+v", before.Good, rec.Good)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	sp := stubSpawn(t)
	_, launch := pf.launch(t, "podman", &run.ActInterrupt{})
	if !strings.Contains(launch, "extension "+poolKeyA+": the background advance at ") || !strings.Contains(launch, " pending — ") {
		t.Errorf("the next launch does not say the candidate was left pending:\n%s", launch)
	}
	if sp.count() != 1 || !slices.Contains(sp.keys(0), poolKeyA) {
		t.Errorf("the pending candidate did not start another background advance: %v", sp.argvs)
	}
}

// INSIDE A JAIL the verb refuses: there is no host capture store to build into.
func TestTheBackgroundAdvanceRefusesInsideAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	if rc := runBackgroundAdvance(backgroundAdvanceArgv([]string{poolKeyA}, "podman", patchedTestPlatform)[2:]); rc != 2 {
		t.Errorf("in a jail the background advance returned %d, want 2", rc)
	}
}

// THE VERB IS DISPATCHED from `yolo internal`, and its argv round-trips through its flags. Red if the
// dispatch's case is deleted.
func TestTheVerbIsDispatched(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	rc := runInternal([]string{backgroundAdvanceVerb})
	os.Stderr = prev
	w.Close()
	said, _ := io.ReadAll(r)
	if rc != 2 || !strings.Contains(string(said), backgroundAdvanceVerb+": runs on the host only") {
		t.Errorf("`yolo internal %s` returned %d and said %q", backgroundAdvanceVerb, rc, said)
	}
	argv := backgroundAdvanceArgv([]string{poolKeyA, poolKeyB}, "podman", patchedTestPlatform)
	got, err := parseBackgroundArgs(argv[2:], io.Discard)
	if err != nil || got.runtime != "podman" || got.platform != patchedTestPlatform ||
		!slices.Equal(got.keys, []string{poolKeyA, poolKeyB}) {
		t.Errorf("the argv %q parsed to %+v (%v)", argv, got, err)
	}
	if _, err := parseBackgroundArgs([]string{"--runtime=podman"}, io.Discard); err == nil {
		t.Error("an argv with no key and no platform was accepted")
	}
	for _, rt := range []string{"container", "macos-user", "unknown"} {
		if _, err := parseBackgroundArgs(backgroundAdvanceArgv([]string{poolKeyA}, rt, patchedTestPlatform)[2:], io.Discard); err == nil {
			t.Errorf("background builds accepted unsupported runtime %q", rt)
		}
	}
}

// THE SIGNALS THAT END IT are SIGTERM and SIGHUP, which a logout or a shutdown sends, and SIGINT.
func TestBackgroundSignalsAreTermHupInt(t *testing.T) {
	want := []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}
	if !slices.Equal(backgroundSignals, want) {
		t.Errorf("backgroundSignals = %v, want %v", backgroundSignals, want)
	}
	if filepath.Dir(backgroundKeyLock(poolKeyA)) != filepath.Dir(backgroundOutcomePath(poolKeyA)) {
		t.Error("a key's lock and its outcome record are not kept together")
	}
}

// Initial recovery is not a completed build's settle: it must skip a busy record rather than wait
// before the background check can apply its own NoWait policy.
func TestABackgroundRecoverySkipsABusyRecord(t *testing.T) {
	newPoolFixture(t)
	sel := selectConfiguredHostPacks()
	f := packload.PatchedTrees(sel.packs)[0]
	a, early := newAdvance(f, advanceOptions{platform: patchedTestPlatform, launch: true})
	if early != nil {
		t.Fatal("could not initialize the fixture's advance")
	}
	// Give recovery a known admitted build without leaving a readable check record.
	var out bytes.Buffer
	r := advancePatchedFork(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &out, errw: &out})
	if r.delivery.Key == "" {
		t.Fatalf("fixture build failed: %+v\n%s", r, out.String())
	}
	if err := os.Remove(a.packs.CheckRecordPath(f.Key())); err != nil {
		t.Fatal(err)
	}
	holding, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(released)
		_ = a.packs.WithCheckRecord(f.Key(), nil, func(*packsrc.CheckRecord, error, func() error) (bool, error) {
			close(holding)
			<-release
			return false, nil
		})
	}()
	<-holding
	done := make(chan struct{})
	go func() {
		runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), &out)
		close(done)
	}()
	busy := false
	select {
	case <-done:
	case <-time.After(time.Second):
		busy = true
	}
	close(release)
	<-released
	<-done
	if busy {
		t.Fatal("background initialization waited for a busy recovery record")
	}
	if got := outcomeOf(t, f.Key()); got.State != bgSkipped {
		t.Errorf("outcome = %+v, want skipped", got)
	}
}

// A background application failure must survive a failed CheckRecord write as typed outcome data,
// stay attached to its real log, and remain current after a failed notice write has claimed it.
func TestBackgroundPatchFailureSurvivesRecordSaveFailureAndClaim(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newTreeFixture(t, `"f.txt"`)
	f := fx.tree(t)
	var firstOut bytes.Buffer
	first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &firstOut, errw: &firstOut})
	if first.delivery.Key == "" {
		t.Fatalf("clean compatible Good was not admitted: %+v\n%s", first, firstOut.String())
	}
	before := patchedRecordOf(t, f.Key())
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	lock := patchedRecordLockPath(f.Key())
	patchedGitWrapper(t, "for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then rm -f "+
		shellQuote(lock)+"; mkdir -p "+shellQuote(lock)+"; break; fi; done")
	if err := os.MkdirAll(filepath.Dir(backgroundAdvanceLog()), 0o700); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.OpenFile(backgroundAdvanceLog(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), logFile)
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	outcome := outcomeOf(t, f.Key())
	data, err := os.ReadFile(backgroundOutcomePath(f.Key()))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		State        string                `json:"state"`
		Log          string                `json:"log"`
		PatchFailure *packsrc.PatchFailure `json:"patch_failure"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.State != bgFailed || wire.PatchFailure == nil {
		t.Fatalf("actual background outcome lost its typed application failure: state=%q failure=%+v raw=%s",
			wire.State, wire.PatchFailure, data)
	}
	pf := wire.PatchFailure
	if pf.Owner != f.Key() || pf.Seq <= 0 || pf.Inputs != before.Read || pf.Series == "" || pf.Target.Tag != "v1.2.0" ||
		pf.Kind != "conflict" || pf.Member != "0001-ten.patch" || !slices.Contains(pf.Paths, "f.txt") || pf.Log != backgroundAdvanceLog() ||
		wire.Log != backgroundAdvanceLog() || outcome.Log != backgroundAdvanceLog() {
		t.Fatalf("background outcome failure is not bound to its original check and real log: %+v log=%q outcome=%+v",
			pf, wire.Log, outcome)
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	if record.PatchFailure != nil || !strings.Contains(outcome.Error, "check record could not be updated") {
		t.Fatalf("the fixture did not isolate RecordReplay persistence from typed operation evidence: recordFailure=%+v outcome=%+v",
			record.PatchFailure, outcome)
	}
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	inputs, _, _, _ := f.CheckWant(series).Inputs()
	if current := record.CurrentPatchFailure(inputs, series.Digest); current != nil {
		t.Fatalf("the fixture retained other current failure authority besides the background outcome: %+v", current)
	}
	logged, err := os.ReadFile(backgroundAdvanceLog())
	if err != nil || !strings.Contains(string(logged), "patch application failed at upstream v1.2.0") {
		t.Fatalf("the real background log did not retain the operation error: err=%v\n%s", err, logged)
	}
	// Reporting is not authority: even a failed notice writer has already claimed the outcome into .said.
	noteBackgroundOutcome(f, richtext.Printer{W: failedBackgroundNoticeWriter{}})
	saidData, err := os.ReadFile(backgroundSaidPath(f.Key()))
	if err != nil {
		t.Fatalf("claimed outcome did not survive its notice writer failure: %v", err)
	}
	var said struct {
		Log          string                `json:"log"`
		PatchFailure *packsrc.PatchFailure `json:"patch_failure"`
	}
	if err := json.Unmarshal(saidData, &said); err != nil || said.PatchFailure == nil || said.Log != backgroundAdvanceLog() {
		t.Fatalf(".said lost typed failure/log after report failure: %+v err=%v raw=%s", said, err, saidData)
	}
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	var recoveredErr bytes.Buffer
	noCheck, recoveredAdvance := servingTreeOf(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true, errw: &recoveredErr}, "", false)
	candidate := recoveredAdvance.backgroundPatchFailureFromSaid()
	if noCheck.patchFailure == nil || noCheck.delivery.PatchFailure == nil ||
		noCheck.patchFailure.Owner != pf.Owner || noCheck.patchFailure.Seq != pf.Seq ||
		!strings.Contains(recoveredErr.String(), "Log: "+backgroundAdvanceLog()) {
		t.Fatalf("no-check foreground recovery lost the claimed authority or real background log: result=%+v candidate=%+v rec=%+v said=%+v\n%s",
			noCheck, candidate, record, said, recoveredErr.String())
	}
	delivered, foreground := fx.deliver(t, true)
	if delivered.PatchFailure == nil || delivered.PatchFailure.Owner != pf.Owner || delivered.PatchFailure.Seq != pf.Seq ||
		delivered.PatchFailure.Target != pf.Target || delivered.PatchFailure.Member != pf.Member ||
		!strings.Contains(foreground, "ERROR: "+f.Key()+": patch application failed at upstream v1.2.0") ||
		!strings.Contains(foreground, "Log: "+backgroundAdvanceLog()) {
		t.Fatalf("foreground delivery/report lost claimed background authority or the ordered host error: got=%+v want=%+v error=%v log=%v\n%s",
			delivered.PatchFailure, pf, strings.Contains(foreground, "ERROR: "+f.Key()+": patch application failed at upstream v1.2.0"),
			strings.Contains(foreground, "Log: "+backgroundAdvanceLog()), foreground)
	}
	after, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil || after.Good == nil || before.Good == nil || after.Good.Entry != before.Good.Entry || len(fx.builds) != 1 {
		t.Fatalf("recovery moved Good or rebuilt: before=%+v after=%+v builds=%d err=%v", before.Good, after.Good, len(fx.builds), err)
	}

	for _, mode := range []string{"claim creation fails", "said publication fails"} {
		t.Run(mode, func(t *testing.T) {
			bg := makeDetachedPatchFailure(t, false, false)
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			switch mode {
			case "claim creation fails":
				previous := createBackgroundClaim
				createBackgroundClaim = func(string, string) (*os.File, error) {
					return nil, errors.New("injected claim creation failure")
				}
				t.Cleanup(func() { createBackgroundClaim = previous })
			case "said publication fails":
				previous := publishBackgroundClaim
				publishBackgroundClaim = func(source, destination string) error {
					if destination == backgroundSaidPath(bg.fork.Key()) {
						return errors.New("injected .said publication failure")
					}
					return os.Rename(source, destination)
				}
				t.Cleanup(func() { publishBackgroundClaim = previous })
			}
			noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
			if _, err := os.Stat(backgroundSaidPath(bg.fork.Key())); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the injected notice failure unexpectedly published .said: %v", err)
			}
			switch mode {
			case "claim creation fails":
				kept, err := readBackgroundOutcome(backgroundOutcomePath(bg.fork.Key()))
				if err != nil || kept.PatchFailure == nil || kept.Log != backgroundAdvanceLog() {
					t.Fatalf("the unclaimed .json lost typed evidence/log: %+v err=%v", kept, err)
				}
				withBackgroundKey(bg.fork.Key(), bg.fork.Label(), io.Discard, func(o *backgroundOutcome) {
					o.State = bgNothing
				})
				still, err := readBackgroundOutcome(backgroundOutcomePath(bg.fork.Key()))
				if err != nil || still.Generation != kept.Generation || still.PatchFailure == nil {
					t.Fatalf("a later background run overwrote the unclaimed typed failure: before=%+v after=%+v err=%v",
						kept, still, err)
				}
			case "said publication fails":
				claims, err := filepath.Glob(filepath.Join(filepath.Dir(backgroundOutcomePath(bg.fork.Key())), ".claimed-*"))
				if err != nil {
					t.Fatal(err)
				}
				var found bool
				for _, path := range claims {
					claim, readErr := readBackgroundOutcome(path)
					if readErr == nil && claim.Key == bg.fork.Key() && claim.PatchFailure != nil && claim.Log == backgroundAdvanceLog() {
						found = true
					}
				}
				if !found {
					t.Fatalf("the failed .said publication discarded its owned typed claim: %v", claims)
				}
			}
			var recoveredErr bytes.Buffer
			noCheck, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
				launch: true, errw: &recoveredErr}, "", false)
			if noCheck.patchFailure == nil || noCheck.delivery.PatchFailure == nil ||
				noCheck.patchFailure.Target != bg.failure.Target ||
				!strings.Contains(recoveredErr.String(), "Log: "+backgroundAdvanceLog()) {
				t.Fatalf("actual no-check recovery lost typed failure/log after %s: %+v\n%s", mode, noCheck, recoveredErr.String())
			}
			delivered, report := bg.fx.deliver(t, true)
			if delivered.PatchFailure == nil || delivered.PatchFailure.Target != bg.failure.Target ||
				!strings.Contains(report, "ERROR: "+bg.fork.Key()+": patch application failed at upstream v1.2.0") ||
				!strings.Contains(report, "Log: "+backgroundAdvanceLog()) {
				t.Fatalf("actual foreground delivery/report lost evidence after %s: %+v\n%s", mode, delivered.PatchFailure, report)
			}
		})
	}
}

type failedBackgroundNoticeWriter struct{}

func (failedBackgroundNoticeWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected background notice writer failure")
}

type detachedPatchFailureFixture struct {
	fx      *treeFixture
	fork    packload.Fork
	before  *packsrc.CheckRecord
	record  *packsrc.CheckRecord
	outcome backgroundOutcome
	failure *packsrc.PatchFailure
}

// makeDetachedPatchFailure drives the actual background producer through a typed conflict whose
// RecordReplay save is blocked after the walk. transient makes the immutable selected target clean
// to real Git, with one merge-tree result injected only for the detached attempt.
func makeDetachedPatchFailure(t *testing.T, transient, preexistingApply bool) detachedPatchFailureFixture {
	return makeDetachedPatchFailureWithRecordSave(t, transient, preexistingApply, true)
}

func makeDetachedPatchFailureWithRecordSave(t *testing.T, transient, preexistingApply, blockReplaySave bool) detachedPatchFailureFixture {
	t.Helper()
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newTreeFixture(t, `"f.txt"`)
	f := fx.tree(t)
	var firstOut bytes.Buffer
	first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &firstOut, errw: &firstOut})
	if first.delivery.Key == "" {
		t.Fatalf("clean compatible Good was not admitted: %+v\n%s", first, firstOut.String())
	}
	before := patchedRecordOf(t, f.Key())
	changes := map[int]string{14: "fourteen", 11: "eleven"}
	if transient {
		changes = map[int]string{14: "fourteen"}
	}
	fx.commit(t, "v1.2.0", changes)
	fx.now = fx.now.Add(2 * time.Hour)
	if preexistingApply {
		series, err := f.ReadSeries()
		if err != nil {
			t.Fatal(err)
		}
		checked := patchedAdvanceStore(true).CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Now: patchedNow})
		if checked.Err != nil || checked.Record == nil {
			t.Fatalf("preexisting apply check failed: %+v", checked)
		}
		inputs, _, _, _ := f.CheckWant(series).Inputs()
		snapshot, err := checked.Record.ReplaySnapshot(inputs, series.Digest)
		if err != nil {
			t.Fatal(err)
		}
		candidates := checked.Record.Candidates(inputs)
		if len(candidates) == 0 {
			t.Fatal("the clean target was not selected")
		}
		walk := patchedAdvanceStore(true).WalkSeries(mustRepo(f.Source), subdirOf(f.Source), series,
			[]packsrc.ListEntry{candidates[0]}, packsrc.WalkOptions{Snapshot: &snapshot, StopOnPatchFailure: true})
		recorded := patchedAdvanceStore(true).RecordReplay(snapshot, patchedYoloVersion(), walk, fx.now)
		if walk.PatchFailure() != nil || walk.Fit < 0 || !recorded.Recorded || recorded.Err != nil {
			t.Fatalf("could not create the old exact-target applies outcome: walk=%+v record=%+v", walk, recorded)
		}
	}
	lock := patchedRecordLockPath(f.Key())
	if transient {
		counter := filepath.Join(t.TempDir(), "merge-tree-count")
		block := "0"
		if blockReplaySave {
			block = "1"
		}
		patchedGitWrapper(t, "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; "+
			"if [ $is_merge -eq 1 ]; then n=$(cat "+shellQuote(counter)+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+
			shellQuote(counter)+"; if [ $n -eq 1 ]; then if [ "+block+" -eq 1 ]; then rm -f "+shellQuote(lock)+
			"; mkdir -p "+shellQuote(lock)+"; fi; printf 'injected-tree\\000f.txt\\000'; exit 1; fi; fi")
	} else if blockReplaySave {
		patchedGitWrapper(t, "for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then rm -f "+shellQuote(lock)+
			"; mkdir -p "+shellQuote(lock)+"; break; fi; done")
	}
	if err := os.MkdirAll(filepath.Dir(backgroundAdvanceLog()), 0o700); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.OpenFile(backgroundAdvanceLog(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var backgroundOut bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), io.MultiWriter(logFile, &backgroundOut))
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	o := outcomeOf(t, f.Key())
	if o.State != bgFailed || o.PatchFailure == nil || o.PatchFailure.Target.Tag != "v1.2.0" ||
		o.PatchFailure.Kind != "conflict" || o.PatchFailure.Log != backgroundAdvanceLog() {
		t.Fatalf("actual detached producer did not retain the typed conflict and log: %+v\n%s", o, backgroundOut.String())
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	if blockReplaySave {
		if record.PatchFailure != nil {
			t.Fatalf("the fixture failed to isolate the blocked CheckRecord replay save: %+v", record.PatchFailure)
		}
	} else if record.PatchFailure == nil {
		t.Fatal("the fixture did not persist the first background patch failure")
	}
	return detachedPatchFailureFixture{fx: fx, fork: f, before: before, record: record, outcome: o, failure: o.PatchFailure}
}

// A claimed result is evidence only when the core's fresh check still says the selected application
// failure is current. An unsuccessful fetch cannot prove removal; a successful removal can.
func TestBackgroundPatchFailureRecoveryUsesCurrentCheckAuthority(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	tests := []struct {
		name           string
		want           bool
		noTyped        bool
		state          string
		zeroGeneration bool
		unavailable    bool
		prepare        func(*packsrc.CheckRecord, *packsrc.PatchFailure)
	}{
		{name: "selected failure remains current", want: true},
		{name: "failed fetch cannot prove removal", want: true, prepare: func(r *packsrc.CheckRecord, _ *packsrc.PatchFailure) {
			r.Seq++
			r.Check.Seq = r.Seq
			r.Check.FetchErr = "injected offline fetch"
			r.Check.List = nil
		}},
		{name: "successful check removes target", prepare: func(r *packsrc.CheckRecord, pf *packsrc.PatchFailure) {
			r.Seq++
			r.Check.Seq = r.Seq
			kept := r.Check.List[:0]
			for _, selected := range r.Check.List {
				if selected.Commit != pf.Target.Commit {
					kept = append(kept, selected)
				}
			}
			r.Check.List = kept
		}},
		{name: "future sequence is stale", prepare: func(_ *packsrc.CheckRecord, pf *packsrc.PatchFailure) {
			pf.Seq++
		}},
		{name: "changed series is stale", prepare: func(_ *packsrc.CheckRecord, pf *packsrc.PatchFailure) {
			pf.Series = "another-series"
		}},
		{name: "unknown owner is not authority", prepare: func(_ *packsrc.CheckRecord, pf *packsrc.PatchFailure) {
			pf.Owner = "another-owner/tool"
		}},
		{name: "missing generation is unavailable, not authority", zeroGeneration: true, unavailable: true},
		{name: "opaque outcome text is not classification", want: false, noTyped: true},
		{name: "nonfailure outcome is not authority", want: false, state: bgMoved},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fx := newTreeFixture(t, `"f.txt"`)
			f := fx.tree(t)
			var firstOut bytes.Buffer
			first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
				out: &firstOut, errw: &firstOut})
			if first.delivery.Key == "" {
				t.Fatalf("clean Good was not admitted: %+v\n%s", first, firstOut.String())
			}
			fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
			fx.now = fx.now.Add(2 * time.Hour)
			var conflictOut bytes.Buffer
			conflict := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
				out: &conflictOut, errw: &conflictOut})
			if conflict.patchFailure == nil || conflict.patchFailure.Kind != "conflict" {
				t.Fatalf("fixture did not produce typed application failure: %+v\n%s", conflict, conflictOut.String())
			}
			failure := *conflict.patchFailure
			store := patchedAdvanceStore(true)
			if err := store.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
				if readErr != nil {
					return false, readErr
				}
				r.PatchFailure = nil // only the separately durable .said outcome carries this candidate.
				r.Outcomes = nil     // suppress the independent legacy-conflict authority for this test.
				if test.prepare != nil {
					test.prepare(r, &failure)
				}
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			state := test.state
			if state == "" {
				state = bgFailed
			}
			outcome := backgroundOutcome{Key: f.Key(), Label: f.Label(), State: state, At: fx.now.Unix(),
				Log: backgroundAdvanceLog(), Error: "opaque patch application failure text", Generation: 1}
			if test.zeroGeneration {
				outcome.Generation = 0
			}
			if !test.noTyped {
				outcome.PatchFailure = &failure
			}
			data, err := json.Marshal(outcome)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(backgroundSaidPath(f.Key())), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(backgroundSaidPath(f.Key()), append(data, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			a, early := newAdvance(f, advanceOptions{launch: true})
			if early != nil {
				t.Fatalf("could not read current check context: %+v", early)
			}
			got := a.deliveryPatchFailure(false)
			if test.unavailable {
				if got.State != "unavailable" || !strings.Contains(got.Diagnostic, "no allocated generation") {
					t.Fatalf("generationless detached evidence was not treated as unavailable: %+v", got)
				}
				return
			}
			if (got.State == "failure" && got.Failure != nil) != test.want {
				t.Fatalf("recovered typed evidence in the wrong current-check state: got=%+v wantFailure=%v", got, test.want)
			}
			if test.want && (got.Failure.Owner != f.Key() || got.Failure.Target != conflict.patchFailure.Target ||
				got.Failure.Seq != conflict.patchFailure.Seq) {
				t.Fatalf("current failure did not preserve original authority: got=%+v want=%+v", got.Failure, conflict.patchFailure)
			}
		})
	}

	t.Run("old same-clock applies is not repair proof; guarded clean replay is", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, true, true)
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		if bg.outcome.At != bg.fx.now.Unix() {
			t.Fatalf("fixture did not hold the same clock second: outcome=%d now=%d", bg.outcome.At, bg.fx.now.Unix())
		}
		var oldApply bool
		for _, outcome := range bg.record.Outcomes {
			if outcome.Kind == packsrc.OutcomeApplies && outcome.Commit == bg.failure.Target.Commit &&
				outcome.Series == bg.failure.Series && outcome.At == bg.fx.now.Unix() {
				oldApply = true
			}
		}
		if !oldApply {
			t.Fatalf("the negative case lacks a preexisting same-clock exact applies record: %+v", bg.record.Outcomes)
		}
		var preRepairErr bytes.Buffer
		preRepair, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &preRepairErr}, "", false)
		if preRepair.patchFailure == nil || preRepair.patchFailure.Target != bg.failure.Target {
			t.Fatalf("old matching applies evidence incorrectly retired the detached failure: %+v\n%s", preRepair, preRepairErr.String())
		}
		if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
			t.Fatal(err)
		}
		var replayOut, replayErr bytes.Buffer
		a, early := newAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
			out: &replayOut, errw: &replayErr})
		if early != nil {
			t.Fatalf("could not initialize the guarded foreground replay: %+v", early)
		}
		unrecordedSnapshot, err := a.replaySnapshot()
		if err != nil {
			t.Fatal(err)
		}
		unrecordedWalk := a.packs.WalkSeries(a.repo, a.subdir, a.series, []packsrc.ListEntry{bg.failure.Target},
			packsrc.WalkOptions{Snapshot: &unrecordedSnapshot, StopOnPatchFailure: true})
		if unrecordedWalk.PatchFailure() != nil || unrecordedWalk.Fit < 0 {
			t.Fatalf("the unrecorded exact target was not clean: %+v", unrecordedWalk)
		}
		if err := recordBackgroundPatchRepair(context.Background(), nil, a.packs, unrecordedSnapshot, a.yolo,
			unrecordedWalk, packsrc.RecordReplayResult{}, false); err != nil {
			t.Fatal(err)
		}
		unrecordedSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || unrecordedSaid.Resolution != nil {
			t.Fatalf("a clean but unrecorded walk wrote repair authority: %+v err=%v", unrecordedSaid, err)
		}
		previousWriter := writeBackgroundOutcomeFile
		writeBackgroundOutcomeFile = func(path string, outcome backgroundOutcome) error {
			if path == backgroundSaidPath(bg.fork.Key()) {
				return errors.New("injected resolution marker write failure")
			}
			return writeBackgroundOutcomeAtomic(path, outcome)
		}
		t.Cleanup(func() { writeBackgroundOutcomeFile = previousWriter })
		walk := a.walk([]packsrc.ListEntry{bg.failure.Target}, true)
		writeBackgroundOutcomeFile = previousWriter
		if walk.Err != nil || walk.PatchFailure() != nil || walk.Fit < 0 || a.patchFailure != nil {
			t.Fatalf("the unchanged selected target was not cleanly replayed through the foreground actor: %+v\n%s\n%s",
				walk, replayOut.String(), replayErr.String())
		}
		said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || said.PatchFailure == nil || said.Resolution != nil ||
			!strings.Contains(replayErr.String(), "matching detached failure evidence could not be resolved") {
			t.Fatalf("failed marker publication discarded evidence or went unreported: %+v err=%v\n%s", said, err, replayErr.String())
		}
		var failedWriteErr bytes.Buffer
		failedWrite, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &failedWriteErr}, "", false)
		if failedWrite.patchFailure == nil || failedWrite.patchFailure.Target != bg.failure.Target {
			t.Fatalf("a failed repair-marker write silently retired its original evidence: %+v", failedWrite)
		}
		walk = a.walk([]packsrc.ListEntry{bg.failure.Target}, true)
		if walk.Err != nil || walk.PatchFailure() != nil || walk.Fit < 0 || a.patchFailure != nil {
			t.Fatalf("the subsequent guarded clean replay did not complete: %+v", walk)
		}
		said, err = readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || said.PatchFailure == nil || said.Resolution == nil ||
			said.Resolution.Generation != said.Generation || said.Resolution.Owner != bg.failure.Owner ||
			said.Resolution.Inputs != bg.failure.Inputs || said.Resolution.Series != bg.failure.Series ||
			said.Resolution.Target != bg.failure.Target || said.Resolution.CheckSeq <= 0 ||
			said.Resolution.Yolo == "" || said.Resolution.Git == "" {
			t.Fatalf("the successful guarded replay did not bind durable exact repair proof: %+v err=%v", said, err)
		}
		var afterRepairErr bytes.Buffer
		afterRepair, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform,
			runtime: "podman", launch: true, errw: &afterRepairErr}, "", false)
		if afterRepair.patchFailure != nil || afterRepair.delivery.PatchFailure != nil {
			t.Fatalf("the repaired old .said resurrected after an actual no-check delivery: %+v\n%s",
				afterRepair, afterRepairErr.String())
		}
		repairedRecord, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		if err != nil {
			t.Fatal(err)
		}
		var exactApply bool
		for _, outcome := range repairedRecord.Outcomes {
			if outcome.Kind == packsrc.OutcomeApplies && outcome.Commit == bg.failure.Target.Commit &&
				outcome.Series == bg.failure.Series && outcome.Yolo == said.Resolution.Yolo &&
				outcome.Git == said.Resolution.Git && outcome.At == bg.fx.now.Unix() {
				exactApply = true
			}
		}
		if !exactApply {
			t.Fatalf("the repair proof was not backed by its same-clock exact RecordReplay outcome: %+v", repairedRecord.Outcomes)
		}
		newerFailure := *bg.failure
		newerFailure.Member = "newer-background-member"
		newerFailure.Detail = "later detached generation"
		newer := backgroundOutcome{Key: bg.fork.Key(), Label: bg.fork.Label(), State: bgFailed,
			At: bg.fx.now.Unix(), Generation: said.Generation + 1, Log: backgroundAdvanceLog(), PatchFailure: &newerFailure}
		if err := writeBackgroundOutcome(newer); err != nil {
			t.Fatal(err)
		}
		var newerErr bytes.Buffer
		newerDelivery, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &newerErr}, "", false)
		if newerDelivery.patchFailure == nil || newerDelivery.patchFailure.Member != "newer-background-member" {
			t.Fatalf("the old generation's repair marker retired a newer typed background generation: %+v\n%s",
				newerDelivery, newerErr.String())
		}
		record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		if err != nil || record.Good == nil || bg.before.Good == nil || record.Good.Entry != bg.before.Good.Entry ||
			len(bg.fx.builds) != 1 {
			t.Fatalf("clean repair changed Good or built/admitted another entry: before=%+v after=%+v builds=%d err=%v",
				bg.before.Good, record.Good, len(bg.fx.builds), err)
		}
	})

	t.Run("actual due and forced treeAdvance repair without synthetic snapshot authority", func(t *testing.T) {
		for _, mode := range []struct {
			name  string
			force bool
		}{
			{name: "due"},
			{name: "forced", force: true},
		} {
			t.Run(mode.name, func(t *testing.T) {
				bg := makeDetachedPatchFailure(t, true, true)
				t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
				noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
				if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
					t.Fatal(err)
				}
				if !mode.force {
					bg.fx.now = bg.fx.now.Add(packsrc.BranchRefreshInterval + time.Second)
				}
				var out bytes.Buffer
				result := treeAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
					launch: true, force: mode.force, out: &out, errw: &out})
				if result.patchFailure != nil || result.delivery.PatchFailure != nil ||
					strings.Contains(result.problem, "check authority changed") ||
					strings.Contains(out.String(), "replay snapshot is stale") {
					t.Fatalf("actual %s treeAdvance did not record the clean unchanged target against disk authority: %+v\n%s",
						mode.name, result, out.String())
				}
				said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
				if err != nil || said.PatchFailure == nil || said.Resolution == nil ||
					said.Resolution.Generation != said.Generation || said.Resolution.Target != bg.failure.Target {
					t.Fatalf("actual %s treeAdvance did not publish its observed generation's repair: %+v err=%v",
						mode.name, said, err)
				}
				var recoveredErr bytes.Buffer
				recovered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
					launch: true, errw: &recoveredErr}, "", false)
				if recovered.patchFailure != nil || recovered.delivery.PatchFailure != nil {
					t.Fatalf("actual no-check delivery resurrected the repaired .said after %s treeAdvance: %+v\n%s",
						mode.name, recovered, recoveredErr.String())
				}
				record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
				if err != nil || !hasAppliedOutcome(record, bg.failure.Target.Commit, bg.failure.Series,
					said.Resolution.Yolo, said.Resolution.Git) {
					t.Fatalf("actual %s treeAdvance lacks the exact guarded applies record: %+v err=%v", mode.name, record, err)
				}
			})
		}
	})

	t.Run("fresh newer typed authority beats an older said failure", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, true, false)
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		staleAdvance, early := newAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, launch: true})
		if early != nil {
			t.Fatalf("could not initialize the pre-concurrent foreground actor: %+v", early)
		}
		if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
			t.Fatal(err)
		}
		patchedGitWrapper(t, "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; "+
			"if [ $is_merge -eq 1 ]; then printf 'newer-tree\\000f.txt\\000'; exit 1; fi")
		store := patchedAdvanceStore(true)
		series, err := bg.fork.ReadSeries()
		if err != nil {
			t.Fatal(err)
		}
		want := bg.fork.CheckWant(series)
		inputs, _, _, err := want.Inputs()
		if err != nil {
			t.Fatal(err)
		}
		checked := store.CheckPatched(want, packsrc.CheckOptions{Force: true, Now: patchedNow})
		if checked.Err != nil || checked.Record == nil || checked.Record.Seq <= bg.failure.Seq {
			t.Fatalf("the forced concurrent check did not establish newer authority: %+v", checked)
		}
		snapshot, err := checked.Record.ReplaySnapshot(inputs, series.Digest)
		if err != nil {
			t.Fatal(err)
		}
		candidates := checked.Record.Candidates(inputs)
		if len(candidates) == 0 || candidates[0].Commit != bg.failure.Target.Commit {
			t.Fatalf("new check selected a different target: %+v want %+v", candidates, bg.failure.Target)
		}
		walk := store.WalkSeries(mustRepo(bg.fork.Source), subdirOf(bg.fork.Source), series,
			[]packsrc.ListEntry{candidates[0]}, packsrc.WalkOptions{Snapshot: &snapshot, StopOnPatchFailure: true})
		current := walk.PatchFailure()
		if current == nil {
			t.Fatalf("new guarded replay did not produce current typed authority: %+v", walk)
		}
		current.Owner, current.Inputs, current.Series, current.Seq = bg.fork.Key(), inputs, series.Digest, snapshot.Seq
		recorded := store.RecordReplay(snapshot, patchedYoloVersion(), walk, bg.fx.now)
		if recorded.Err != nil || !recorded.Recorded {
			t.Fatalf("new guarded replay was not recorded: %+v", recorded)
		}
		newer, err := store.LoadCheckRecord(bg.fork.Key())
		if err != nil || newer.CurrentPatchFailure(inputs, series.Digest) == nil {
			t.Fatalf("the newer current typed failure is absent: %+v err=%v", newer, err)
		}
		got := staleAdvance.deliveryPatchFailure(false)
		if got.Failure == nil || got.Failure.Target.Commit != bg.failure.Target.Commit ||
			newer.PatchFailure == nil || got.Failure.Seq != newer.PatchFailure.Seq {
			t.Fatalf("the fresh current typed failure was replaced by old .said evidence: got=%+v newer=%+v", got, newer.PatchFailure)
		}
	})
}

// A failure before the check (for example an unreadable series) still needs a next-launch report,
// with the actual cause rather than a silent "nothing" or a generic replay failure.
func publishTestBackgroundFailureGeneration(t *testing.T, f packload.Fork, failure *packsrc.PatchFailure, member string) backgroundOutcome {
	t.Helper()
	copy := *failure
	copy.Member = member
	copy.Detail = "detached generation " + member
	copy.Log = backgroundAdvanceLog()
	var out bytes.Buffer
	withBackgroundKey(f.Key(), f.Label(), &out, func(o *backgroundOutcome) {
		o.State = bgFailed
		o.PatchFailure = cloneBackgroundPatchFailure(&copy, o.Log)
		o.Error = "outcome " + member
	})
	o, err := readBackgroundOutcome(backgroundOutcomePath(f.Key()))
	if err != nil || o.PatchFailure == nil || o.PatchFailure.Member != member || o.State != bgFailed {
		t.Fatalf("production background outcome writer did not publish %s: %+v err=%v\n%s", member, o, err, out.String())
	}
	return o
}

func runBackgroundFinishContention(t *testing.T, bg detachedPatchFailureFixture,
	publish func(source, destination string) error) (backgroundOutcome, []string, string) {
	t.Helper()
	if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
		t.Fatal(err)
	}
	bg.fx.now = bg.fx.now.Add(packsrc.BranchRefreshInterval + time.Second)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	defer resume()
	var paused atomic.Bool
	previousHook := backgroundArtifactTestHook
	backgroundArtifactTestHook = func(phase, key string) {
		if phase == "background.finish" && key == bg.fork.Key() && paused.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}
	previousPublish := publishBackgroundEvidenceClaim
	if publish != nil {
		publishBackgroundEvidenceClaim = publish
	}
	defer func() {
		backgroundArtifactTestHook = previousHook
		publishBackgroundEvidenceClaim = previousPublish
	}()
	var output bytes.Buffer
	go func() {
		runBackgroundAdvanceUnder(context.Background(), bgArgs(bg.fork.Key()), &output)
		close(done)
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatalf("actual background producer ended before its finish transaction: %s", output.String())
	case <-time.After(5 * time.Second):
		resume()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("actual background producer did not reach finish: %s", output.String())
	}
	lock, err := pidlock.Acquire(backgroundArtifactLock(bg.fork.Key()), pidlock.NoWait, nil)
	if err != nil {
		resume()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("could not hold the artifact lock at the paused finish: %v", err)
	}
	resume()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		lock.Release()
		t.Fatal("background finish blocked instead of trying the artifact lock once")
	}
	lock.Release()
	outcome, err := readBackgroundOutcome(backgroundOutcomePath(bg.fork.Key()))
	if err != nil {
		t.Fatalf("background running artifact disappeared after a contended finish: %v", err)
	}
	claims, err := filepath.Glob(backgroundClaimPattern(bg.fork.Key()))
	if err != nil {
		t.Fatal(err)
	}
	return outcome, claims, output.String()
}

func TestBackgroundAdvanceRepairsPriorDetachedFailureAfterExactCleanRecording(t *testing.T) {
	bg := makeDetachedPatchFailure(t, true, false)
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	prior, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || prior.Generation != bg.outcome.Generation || prior.PatchFailure == nil || prior.Resolution != nil {
		t.Fatalf("fixture lacks an unresolved first-generation detached failure: %+v err=%v", prior, err)
	}
	if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
		t.Fatal(err)
	}
	bg.fx.now = bg.fx.now.Add(packsrc.BranchRefreshInterval + time.Second)
	var output bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(bg.fork.Key()), &output)
	completed := outcomeOf(t, bg.fork.Key())
	if completed.Generation != prior.Generation+1 || completed.State != bgMoved ||
		!strings.HasPrefix(completed.To, bg.failure.Target.Tag+" ") {
		t.Fatalf("actual later background advance did not record and build the exact clean target: %+v\n%s", completed, output.String())
	}
	said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || said.Generation != prior.Generation || said.PatchFailure == nil || said.Resolution == nil ||
		said.Resolution.Generation != prior.Generation || said.Resolution.Target != bg.failure.Target ||
		said.Resolution.CheckSeq == 0 {
		t.Fatalf("the successful guarded background replay did not resolve exactly the original failure generation: %+v err=%v", said, err)
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
	if err != nil || record.Good == nil || record.Good.Commit != bg.failure.Target.Commit || len(bg.fx.builds) != 2 {
		t.Fatalf("the later background generation did not build/admit its exact target: Good=%+v builds=%d err=%v", record.Good, len(bg.fx.builds), err)
	}
	var report bytes.Buffer
	delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		launch: true, errw: &report}, "", false)
	if delivered.patchFailure != nil || delivered.delivery.PatchFailure != nil {
		t.Fatalf("no-check foreground delivery resurrected the repaired first-generation failure: %+v\n%s", delivered, report.String())
	}
}

func TestBackgroundAdvanceRepairsPersistedFailureWithSeparateLogAnnotation(t *testing.T) {
	bg := makeDetachedPatchFailureWithRecordSave(t, true, false, false)
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	series, err := bg.fork.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	inputs, _, _, _ := bg.fork.CheckWant(series).Inputs()
	record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
	if err != nil {
		t.Fatal(err)
	}
	persisted := record.CurrentPatchFailure(inputs, series.Digest)
	if persisted == nil || persisted.Log != "" || bg.failure.Log != backgroundAdvanceLog() {
		t.Fatalf("fixture did not separate persisted failure identity from its real outcome log: record=%+v outcome=%+v",
			persisted, bg.failure)
	}
	outcomeFailure := *bg.failure
	outcomeFailure.Log = persisted.Log
	if !reflect.DeepEqual(&outcomeFailure, persisted) {
		t.Fatalf("fixture changed classified failure data beyond the log annotation: persisted=%+v outcome=%+v", persisted, bg.failure)
	}
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	prior, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || prior.PatchFailure == nil || prior.PatchFailure.Log != backgroundAdvanceLog() || prior.Resolution != nil {
		t.Fatalf("persisted first-generation outcome lost its real log or unresolved status: %+v err=%v", prior, err)
	}
	snapshot, err := record.ReplaySnapshot(inputs, series.Digest)
	if err != nil {
		t.Fatal(err)
	}
	token, tokenErr := observeBackgroundRepairToken(bg.fork.Key(), patchedAdvanceStore(true), snapshot,
		record.Candidates(inputs), context.Background(), true)
	foregroundToken, foregroundTokenErr := observeBackgroundRepairToken(bg.fork.Key(), patchedAdvanceStore(true), snapshot,
		record.Candidates(inputs), context.Background(), false)
	tokenFailure := packsrc.PatchFailure{}
	if token != nil {
		tokenFailure = token.Failure
		tokenFailure.Log = persisted.Log
	}
	foregroundTokenFailure := packsrc.PatchFailure{}
	if foregroundToken != nil {
		foregroundTokenFailure = foregroundToken.Failure
		foregroundTokenFailure.Log = persisted.Log
	}
	tokenMatchesPersistedFailure := tokenErr == nil && token != nil && token.Generation == prior.Generation &&
		token.Failure.Log == backgroundAdvanceLog() && reflect.DeepEqual(&tokenFailure, persisted) &&
		foregroundTokenErr == nil && foregroundToken != nil && foregroundToken.Generation == prior.Generation &&
		foregroundToken.Failure.Log == backgroundAdvanceLog() && reflect.DeepEqual(&foregroundTokenFailure, persisted)
	bg.fx.now = bg.fx.now.Add(packsrc.BranchRefreshInterval + time.Second)
	var output bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(bg.fork.Key()), &output)
	completed := outcomeOf(t, bg.fork.Key())
	if completed.Generation != prior.Generation+1 || completed.State != bgMoved ||
		!strings.HasPrefix(completed.To, bg.failure.Target.Tag+" ") {
		t.Fatalf("actual background replay did not cleanly record/build the persisted failure's exact target: %+v\n%s",
			completed, output.String())
	}
	said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || said.Generation != prior.Generation || said.PatchFailure == nil || said.PatchFailure.Log != backgroundAdvanceLog() ||
		said.Resolution == nil || said.Resolution.Generation != prior.Generation || said.Resolution.Target != bg.failure.Target {
		t.Fatalf("log-only representation difference prevented exact original-generation repair: %+v err=%v", said, err)
	}
	updated, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
	gitVersion, gitErr := patchedAdvanceStore(true).GitVersion()
	if err != nil || gitErr != nil || updated.PatchFailure != nil || updated.Good == nil || updated.Good.Commit != bg.failure.Target.Commit ||
		!hasAppliedOutcome(updated, bg.failure.Target.Commit, series.Digest, patchedYoloVersion(), gitVersion) {
		t.Fatalf("the authorized clean background replay did not persist an exact applied outcome and build without the old failure: %+v err=%v gitErr=%v",
			updated, err, gitErr)
	}
	var report bytes.Buffer
	delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		launch: true, errw: &report}, "", false)
	if delivered.patchFailure != nil || delivered.delivery.PatchFailure != nil {
		t.Fatalf("actual no-check foreground delivery resurrected the repaired persisted failure: %+v\n%s", delivered, report.String())
	}
	if !tokenMatchesPersistedFailure {
		t.Fatalf("pre-replay repair token was denied when the persisted record differed from the detached failure only by Log: background token=%+v err=%v foreground token=%+v err=%v persisted=%+v detached=%+v",
			token, tokenErr, foregroundToken, foregroundTokenErr, persisted, bg.failure)
	}
}

func TestBackgroundAdvancePersistedFailureForegroundForcedTreeAdvance(t *testing.T) {
	bg := makeDetachedPatchFailureWithRecordSave(t, true, false, false)
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	series, err := bg.fork.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	inputs, _, _, _ := bg.fork.CheckWant(series).Inputs()
	store := patchedAdvanceStore(true)
	before, err := store.LoadCheckRecord(bg.fork.Key())
	if err != nil || before.PatchFailure == nil {
		t.Fatalf("the first actual background failure was not persisted: record=%+v err=%v", before, err)
	}
	persisted := before.CurrentPatchFailure(inputs, series.Digest)
	if persisted == nil || persisted.Log != "" || bg.failure.Log != backgroundAdvanceLog() {
		t.Fatalf("fixture did not separate the persisted failure from its detached log annotation: record=%+v outcome=%+v",
			persisted, bg.failure)
	}
	var recordedConflict bool
	for _, outcome := range before.Outcomes {
		if outcome.Commit == bg.failure.Target.Commit && outcome.Series == series.Digest &&
			outcome.Kind == packsrc.OutcomeConflict && outcome.Member == bg.failure.Member &&
			reflect.DeepEqual(outcome.Paths, bg.failure.Paths) {
			recordedConflict = true
			break
		}
	}
	if !recordedConflict {
		t.Fatalf("first background failure lacks its successful exact RecordReplay conflict outcome: %+v", before.Outcomes)
	}
	outcomeFailure := *bg.failure
	outcomeFailure.Log = persisted.Log
	if !reflect.DeepEqual(&outcomeFailure, persisted) {
		t.Fatalf("fixture differs beyond PatchFailure.Log: persisted=%+v detached=%+v", persisted, bg.failure)
	}
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	prior, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || prior.Generation != bg.outcome.Generation || prior.PatchFailure == nil ||
		prior.PatchFailure.Log != backgroundAdvanceLog() || prior.Resolution != nil {
		t.Fatalf("the original typed failure was not published unresolved with its real log: %+v err=%v", prior, err)
	}

	var output bytes.Buffer
	result := treeAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		force: true, out: &output, errw: &output})
	if result.failed || !result.built || result.patchFailure != nil || result.delivery.PatchFailure != nil {
		t.Fatalf("the actual forced foreground treeAdvance did not cleanly replay and build the persisted failure's target: %+v\n%s",
			result, output.String())
	}
	said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || said.Generation != prior.Generation || said.PatchFailure == nil ||
		said.PatchFailure.Log != backgroundAdvanceLog() || said.Resolution == nil ||
		said.Resolution.Generation != prior.Generation || said.Resolution.Owner != bg.fork.Key() ||
		said.Resolution.Inputs != inputs || said.Resolution.Series != series.Digest ||
		said.Resolution.Target != bg.failure.Target || said.Resolution.FailureSeq != persisted.Seq ||
		said.Resolution.CheckSeq <= 0 {
		t.Fatalf("the actual forced foreground replay did not resolve only the original failure while retaining its real log: %+v err=%v",
			said, err)
	}
	after, err := store.LoadCheckRecord(bg.fork.Key())
	gitVersion, gitErr := store.GitVersion()
	if err != nil || gitErr != nil || after.PatchFailure != nil || after.Good == nil ||
		after.Good.Commit != bg.failure.Target.Commit || len(bg.fx.builds) != 2 ||
		!hasAppliedOutcome(after, bg.failure.Target.Commit, series.Digest, patchedYoloVersion(), gitVersion) {
		t.Fatalf("the actual foreground replay lacks its exact guarded applied record/Good build: record=%+v builds=%d err=%v gitErr=%v",
			after, len(bg.fx.builds), err, gitErr)
	}
	var report bytes.Buffer
	delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		launch: true, errw: &report}, "", false)
	if delivered.patchFailure != nil || delivered.delivery.PatchFailure != nil {
		t.Fatalf("no-check foreground delivery resurrected the repaired persisted failure: %+v\n%s", delivered, report.String())
	}
}

func TestBackgroundAdvanceRepairTokenRefusesOtherPersistedFailureIdentityChanges(t *testing.T) {
	cases := []struct {
		name   string
		change func(*packsrc.PatchFailure)
	}{
		{name: "owner", change: func(f *packsrc.PatchFailure) { f.Owner += "-other" }},
		{name: "inputs", change: func(f *packsrc.PatchFailure) { f.Inputs.Ref += "-other" }},
		{name: "series", change: func(f *packsrc.PatchFailure) { f.Series += "-other" }},
		{name: "target", change: func(f *packsrc.PatchFailure) { f.Target.Tag += "-other" }},
		{name: "kind", change: func(f *packsrc.PatchFailure) { f.Kind += "-other" }},
		{name: "member", change: func(f *packsrc.PatchFailure) { f.Member += "-other" }},
		{name: "paths nil versus nonempty", change: func(f *packsrc.PatchFailure) { f.Paths = nil }},
		{name: "detail", change: func(f *packsrc.PatchFailure) { f.Detail += "-other" }},
		{name: "sequence", change: func(f *packsrc.PatchFailure) { f.Seq++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bg := makeDetachedPatchFailureWithRecordSave(t, true, false, false)
			series, err := bg.fork.ReadSeries()
			if err != nil {
				t.Fatal(err)
			}
			inputs, _, _, _ := bg.fork.CheckWant(series).Inputs()
			store := patchedAdvanceStore(true)
			record, err := store.LoadCheckRecord(bg.fork.Key())
			if err != nil || record.PatchFailure == nil {
				t.Fatalf("initial persisted typed failure is missing: %+v err=%v", record, err)
			}
			if len(record.PatchFailure.Paths) == 0 {
				t.Fatalf("fixture needs nonempty failure paths for nil-versus-nonempty coverage: %+v", record.PatchFailure)
			}
			snapshot, err := record.ReplaySnapshot(inputs, series.Digest)
			if err != nil {
				t.Fatal(err)
			}
			mutated := *record.PatchFailure
			mutated.Paths = append([]string(nil), record.PatchFailure.Paths...)
			tc.change(&mutated)
			if err := store.WithCheckRecord(bg.fork.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
				if readErr != nil {
					return false, readErr
				}
				r.PatchFailure = &mutated
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			token, err := observeBackgroundRepairToken(bg.fork.Key(), store, snapshot, record.Candidates(inputs), context.Background(), true)
			if err != nil || token != nil {
				t.Fatalf("a persisted classified-failure identity change was accepted as exact repair: token=%+v err=%v mutated=%+v",
					token, err, mutated)
			}
		})
	}
}

func TestBackgroundAdvanceRepairCoordinationIsNoWaitAndConservative(t *testing.T) {
	for _, phase := range []string{"repair.observe", "repair.publish"} {
		t.Run(strings.TrimPrefix(phase, "repair."), func(t *testing.T) {
			bg := makeDetachedPatchFailure(t, true, false)
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
			if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
				t.Fatal(err)
			}
			bg.fx.now = bg.fx.now.Add(packsrc.BranchRefreshInterval + time.Second)
			files, said, output := runBackgroundRepairWithArtifactContention(t, bg, phase)
			var completed *backgroundOutcomeFile
			for i := range files {
				if files[i].Outcome.Generation == bg.outcome.Generation+1 && files[i].Outcome.State == bgMoved {
					completed = &files[i]
				}
			}
			if completed == nil || !strings.HasPrefix(completed.Outcome.To, bg.failure.Target.Tag+" ") ||
				completed.Path == backgroundOutcomePath(bg.fork.Key()) {
				t.Fatalf("the contended background generation did not retain its completed clean build as an immutable claim: files=%+v\n%s",
					files, output)
			}
			running, err := readBackgroundOutcome(backgroundOutcomePath(bg.fork.Key()))
			if err != nil || running.Generation != completed.Outcome.Generation || running.State != bgRunning {
				t.Fatalf("the busy .json was overwritten despite the background NoWait rule: %+v err=%v", running, err)
			}
			if said.Generation != bg.outcome.Generation || said.PatchFailure == nil || said.Resolution != nil {
				t.Fatalf("artifact contention falsely resolved or replaced the old typed failure: %+v", said)
			}
			record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
			actor, early := newAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true})
			if early != nil {
				t.Fatal(early)
			}
			gitVersion, gitErr := actor.packs.GitVersion()
			if err != nil || gitErr != nil || record.Good == nil || record.Good.Commit != bg.failure.Target.Commit ||
				!hasAppliedOutcome(record, bg.failure.Target.Commit, bg.failure.Series, patchedYoloVersion(), gitVersion) {
				t.Fatalf("guarded clean record/build did not complete while the artifact lock was held: Good=%+v err=%v", record.Good, err)
			}
			var report bytes.Buffer
			delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
				launch: true, errw: &report}, "", false)
			if delivered.patchFailure == nil || delivered.patchFailure.Target != bg.failure.Target ||
				!strings.Contains(report.String(), "patch application failed at upstream v1.2.0") {
				t.Fatalf("no-check foreground recovery did not conservatively retain old failure evidence: %+v\n%s", delivered, report.String())
			}
			if phase == "repair.observe" && !strings.Contains(output, "could not observe detached failure evidence") {
				t.Fatalf("failed nonblocking token observation had no concrete diagnostic: %s", output)
			}
			if phase == "repair.publish" && !strings.Contains(output, "matching detached failure evidence could not be resolved") {
				t.Fatalf("failed nonblocking marker transaction had no concrete diagnostic: %s", output)
			}
		})
	}
}

func runBackgroundRepairWithArtifactContention(t *testing.T, bg detachedPatchFailureFixture, phase string) ([]backgroundOutcomeFile, backgroundOutcome, string) {
	t.Helper()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	defer resume()
	var paused atomic.Bool
	previousHook := backgroundArtifactTestHook
	backgroundArtifactTestHook = func(gotPhase, key string) {
		if gotPhase == phase && key == bg.fork.Key() && paused.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}
	t.Cleanup(func() { backgroundArtifactTestHook = previousHook })
	var output bytes.Buffer
	go func() {
		runBackgroundAdvanceUnder(context.Background(), bgArgs(bg.fork.Key()), &output)
		close(done)
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatalf("actual background producer ended before %s: %s", phase, output.String())
	case <-time.After(5 * time.Second):
		resume()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("actual background producer did not reach %s: %s", phase, output.String())
	}
	lock, err := pidlock.Acquire(backgroundArtifactLock(bg.fork.Key()), pidlock.NoWait, nil)
	if err != nil {
		resume()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("could not contend the artifact lock during %s: %v", phase, err)
	}
	resume()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		lock.Release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("background producer waited behind the artifact lock during %s", phase)
	}
	lock.Release()
	files, err := backgroundOutcomeFiles(bg.fork.Key())
	if err != nil {
		t.Fatalf("reading the completed and retained background outcomes: %v", err)
	}
	said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil {
		t.Fatalf("contended producer lost its prior typed evidence: %v", err)
	}
	return files, said, output.String()
}

func TestBackgroundFinishContentionPreservesTypedFailureClaim(t *testing.T) {
	t.Run("closed append-only claim survives and later generations do not replace it", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, false, false)
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		var linkCalls atomic.Int32
		var tempPayload backgroundOutcome
		var publicationErr error
		running, claims, output := runBackgroundFinishContention(t, bg, func(source, destination string) error {
			linkCalls.Add(1)
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				publicationErr = errors.New("claim target was visible before atomic publication")
				return publicationErr
			}
			data, err := os.ReadFile(source)
			if err != nil {
				publicationErr = err
				return err
			}
			if err := json.Unmarshal(data, &tempPayload); err != nil || tempPayload.PatchFailure == nil || tempPayload.Log == "" {
				publicationErr = fmt.Errorf("fallback temporary payload was not complete and typed: %w", err)
				return publicationErr
			}
			return os.Link(source, destination)
		})
		if publicationErr != nil || linkCalls.Load() != 1 {
			t.Fatalf("append-only fallback did not publish one closed payload atomically: err=%v calls=%d", publicationErr, linkCalls.Load())
		}
		if running.State != bgRunning || running.Generation != bg.outcome.Generation+1 || running.PatchFailure != nil {
			t.Fatalf("contended finish overwrote .json despite losing its short-lock try: %+v", running)
		}
		if len(claims) != 1 {
			t.Fatalf("contended finish did not leave one recovery-readable claim: %v\n%s", claims, output)
		}
		claimBytes, err := os.ReadFile(claims[0])
		if err != nil {
			t.Fatal(err)
		}
		claim, err := readBackgroundOutcome(claims[0])
		if err != nil || claim.Key != bg.fork.Key() || claim.Generation != running.Generation || claim.State != bgFailed ||
			claim.PatchFailure == nil || claim.PatchFailure.Owner != bg.fork.Key() || claim.PatchFailure.Inputs != bg.failure.Inputs ||
			claim.PatchFailure.Series != bg.failure.Series || claim.PatchFailure.Target != bg.failure.Target ||
			claim.PatchFailure.Log != claim.Log || claim.Log == "" || tempPayload.Generation != claim.Generation {
			t.Fatalf("fallback claim does not retain the actual detached typed failure and log: %+v err=%v", claim, err)
		}
		if !strings.Contains(output, "retained generation "+fmt.Sprint(claim.Generation)+" as an immutable typed claim") ||
			!strings.Contains(output, claim.Log) {
			t.Fatalf("successful preservation was not reported with its log: %s", output)
		}
		record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		if err != nil || record.PatchFailure != nil {
			t.Fatalf("the failure unexpectedly gained CheckRecord authority: %+v err=%v", record, err)
		}
		var recoveredErr bytes.Buffer
		recovered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &recoveredErr}, "", false)
		if recovered.patchFailure == nil || recovered.patchFailure.Target != bg.failure.Target ||
			recovered.patchFailure.Log != claim.Log {
			t.Fatalf("foreground did not recover the failure from its immutable claim: %+v\n%s", recovered, recoveredErr.String())
		}

		withBackgroundKey(bg.fork.Key(), bg.fork.Label(), io.Discard, func(o *backgroundOutcome) { o.State = bgNothing })
		still, err := os.ReadFile(claims[0])
		if err != nil || !bytes.Equal(still, claimBytes) {
			t.Fatalf("a later locked generation mutated the append-only claim: err=%v", err)
		}
		newerFailure := *bg.failure
		newerFailure.Member = "later-locked-generation"
		newer := publishTestBackgroundFailureGeneration(t, bg.fork, &newerFailure, newerFailure.Member)
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		newSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		still, claimReadErr := os.ReadFile(claims[0])
		if err != nil || claimReadErr != nil || newSaid.Generation != newer.Generation || newSaid.PatchFailure == nil ||
			newSaid.PatchFailure.Member != newerFailure.Member || !bytes.Equal(still, claimBytes) {
			t.Fatalf("later locked publication displaced newer authority or mutated the fallback claim: said=%+v err=%v claimErr=%v",
				newSaid, err, claimReadErr)
		}
	})

	t.Run("failed no-replace link reports loss without claiming durability", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, false, false)
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		injected := errors.New("injected hard-link publication failure")
		running, claims, output := runBackgroundFinishContention(t, bg, func(string, string) error { return injected })
		if running.State != bgRunning || running.Generation != bg.outcome.Generation+1 || len(claims) != 0 ||
			!strings.Contains(output, injected.Error()) || !strings.Contains(output, "could not preserve generation") ||
			strings.Contains(output, "retained generation") || strings.Contains(output, "immutable typed claim") {
			t.Fatalf("failed evidence publication was hidden or reported as durable: running=%+v claims=%v output=%s", running, claims, output)
		}
		temps, err := filepath.Glob(filepath.Join(paths.BackgroundAdvanceDir(), ".outcome-pending-*"))
		if err != nil || len(temps) != 0 {
			t.Fatalf("failed no-replace publication left a partial temp visible: %v err=%v", temps, err)
		}
		said, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || said.Generation != bg.outcome.Generation || said.PatchFailure == nil {
			t.Fatalf("the previous published evidence was overwritten by failed fallback: %+v err=%v", said, err)
		}
	})
}

func TestBackgroundArtifactPublicationPreservesNewerGeneration(t *testing.T) {
	t.Run("delayed claim cannot replace newer said", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, true, false)
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		resume := func() { releaseOnce.Do(func() { close(release) }) }
		defer resume()
		var paused atomic.Bool
		previousHook := backgroundArtifactTestHook
		backgroundArtifactTestHook = func(phase, key string) {
			if phase == "notice.publish" && key == bg.fork.Key() && paused.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		t.Cleanup(func() { backgroundArtifactTestHook = previousHook })
		firstNotice := make(chan struct{})
		go func() {
			noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
			close(firstNotice)
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			resume()
			t.Fatal("generation 1 notice did not pause after claim and before publication")
		}
		if _, err := os.Stat(backgroundOutcomePath(bg.fork.Key())); !errors.Is(err, os.ErrNotExist) {
			resume()
			t.Fatalf("generation 1 was not moved to its owned claim before the pause: err=%v", err)
		}
		claimed, err := filepath.Glob(backgroundClaimPattern(bg.fork.Key()))
		if err != nil || len(claimed) != 1 {
			resume()
			t.Fatalf("generation 1 claim is not available to recovery: %v err=%v", claimed, err)
		}

		newer := publishTestBackgroundFailureGeneration(t, bg.fork, bg.failure, "newer-generation-two")
		if newer.Generation != bg.outcome.Generation+1 {
			resume()
			t.Fatalf("new producer did not allocate generation 2: old=%d new=%d", bg.outcome.Generation, newer.Generation)
		}
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		newSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || newSaid.Generation != newer.Generation || newSaid.PatchFailure == nil ||
			newSaid.PatchFailure.Member != "newer-generation-two" {
			resume()
			t.Fatalf("generation 2 did not publish through the actual notice path: %+v err=%v", newSaid, err)
		}
		resume()
		select {
		case <-firstNotice:
		case <-time.After(5 * time.Second):
			t.Fatal("delayed generation 1 notice did not finish")
		}
		finalSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || finalSaid.Generation != newer.Generation || finalSaid.PatchFailure == nil ||
			finalSaid.PatchFailure.Member != "newer-generation-two" || finalSaid.PatchFailure.Log != newer.PatchFailure.Log {
			t.Fatalf("delayed generation 1 claim overwrote the newer .said: %+v err=%v", finalSaid, err)
		}
		claim, err := readBackgroundOutcome(claimed[0])
		if err != nil || claim.Generation != bg.outcome.Generation || claim.PatchFailure == nil {
			t.Fatalf("the superseded generation 1 typed claim was not retained: %+v err=%v", claim, err)
		}
		var report bytes.Buffer
		delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &report}, "", false)
		if delivered.patchFailure == nil || delivered.patchFailure.Member != "newer-generation-two" ||
			delivered.patchFailure.Log != newer.PatchFailure.Log {
			t.Fatalf("no-check recovery did not select generation 2 with its absent CheckRecord failure authority: %+v\n%s",
				delivered, report.String())
		}
		record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		if err != nil || record.PatchFailure != nil {
			t.Fatalf("the newer detached failure was incorrectly treated as persisted CheckRecord authority: %+v err=%v", record, err)
		}
	})

	t.Run("delayed repair cannot resolve a replacement generation", func(t *testing.T) {
		bg := makeDetachedPatchFailure(t, true, false)
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
			t.Fatal(err)
		}
		actor, early := newAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
			out: io.Discard, errw: io.Discard})
		if early != nil {
			t.Fatalf("could not initialize actual foreground actor: %+v", early)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		resume := func() { releaseOnce.Do(func() { close(release) }) }
		defer resume()
		var paused atomic.Bool
		previousHook := backgroundArtifactTestHook
		backgroundArtifactTestHook = func(phase, key string) {
			if phase == "repair.publish" && key == bg.fork.Key() && paused.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		t.Cleanup(func() { backgroundArtifactTestHook = previousHook })
		type walkResult struct {
			walk packsrc.WalkResult
			out  bytes.Buffer
			err  bytes.Buffer
		}
		completed := make(chan walkResult, 1)
		go func() {
			var result walkResult
			actor.pr = richtext.Printer{W: &result.out}
			actor.epr = richtext.Printer{W: &result.err}
			result.walk = actor.walk([]packsrc.ListEntry{bg.failure.Target}, true)
			completed <- result
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			resume()
			t.Fatal("the actual replay did not pause after RecordReplay and before marker publication")
		}
		recorded, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		gitVersion, gitErr := actor.packs.GitVersion()
		if err != nil || gitErr != nil || !hasAppliedOutcome(recorded, bg.failure.Target.Commit, bg.failure.Series,
			patchedYoloVersion(), gitVersion) {
			resume()
			t.Fatalf("the delayed marker lacks an already successful guarded RecordReplay: %+v err=%v", recorded, err)
		}
		newer := publishTestBackgroundFailureGeneration(t, bg.fork, bg.failure, "replacement-generation-two")
		noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
		newSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || newSaid.Generation != newer.Generation || newSaid.Resolution != nil {
			resume()
			t.Fatalf("generation 2 was not an unresolved typed replacement before releasing the marker: %+v err=%v", newSaid, err)
		}
		resume()
		var result walkResult
		select {
		case result = <-completed:
		case <-time.After(5 * time.Second):
			t.Fatal("delayed repair marker did not finish")
		}
		if result.walk.Err != nil || result.walk.PatchFailure() != nil || result.walk.Fit < 0 {
			t.Fatalf("the actual foreground replay did not finish cleanly: %+v\n%s\n%s", result.walk, result.out.String(), result.err.String())
		}
		finalSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
		if err != nil || finalSaid.Generation != newer.Generation || finalSaid.Resolution != nil ||
			finalSaid.PatchFailure == nil || finalSaid.PatchFailure.Member != "replacement-generation-two" ||
			finalSaid.PatchFailure.Log != newer.PatchFailure.Log {
			t.Fatalf("the delayed gen-1 repair marker overwrote or resolved generation 2: %+v err=%v", finalSaid, err)
		}
		var report bytes.Buffer
		delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
			launch: true, errw: &report}, "", false)
		if delivered.patchFailure == nil || delivered.patchFailure.Member != "replacement-generation-two" ||
			delivered.patchFailure.Log != newer.PatchFailure.Log {
			t.Fatalf("recovery did not keep generation 2 despite the gen-1 replay's CheckRecord save: %+v\n%s", delivered, report.String())
		}
		record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
		recordedGit, recordedGitErr := actor.packs.GitVersion()
		if err != nil || recordedGitErr != nil || record.PatchFailure != nil || !hasAppliedOutcome(record, bg.failure.Target.Commit,
			bg.failure.Series, patchedYoloVersion(), recordedGit) {
			t.Fatalf("the test failed to retain absent failure authority beside the authorized replay: %+v err=%v", record, err)
		}
	})
}

func TestBackgroundRecoveryArtifactLockCancellationIsUnavailable(t *testing.T) {
	bg := makeDetachedPatchFailure(t, true, false)
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	lock, err := pidlock.Acquire(backgroundArtifactLock(bg.fork.Key()), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	result := treeAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		ctx: ctx, out: &output, errw: &output})
	if !result.failed || result.built || !strings.Contains(result.problem, "detached patch-failure evidence is unavailable") ||
		!strings.Contains(result.problem, "cancelled") {
		t.Fatalf("a cancelled recovery snapshot was treated as clean authority: %+v\n%s", result, output.String())
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(bg.fork.Key())
	if err != nil || record.PatchFailure != nil || bg.before == nil || record.Seq != bg.record.Seq ||
		record.Good == nil || bg.before.Good == nil || record.Good.Entry != bg.before.Good.Entry {
		t.Fatalf("unavailable detached authority changed core failure or Good state: before=%+v after=%+v err=%v", bg.record, record, err)
	}
}

func TestBackgroundOlderFallbackDoesNotDisplaceCurrentResolution(t *testing.T) {
	bg := makeDetachedPatchFailure(t, true, false)
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	if err := os.RemoveAll(patchedRecordLockPath(bg.fork.Key())); err != nil {
		t.Fatal(err)
	}
	actor, early := newAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: io.Discard, errw: io.Discard})
	if early != nil {
		t.Fatal(early)
	}
	walk := actor.walk([]packsrc.ListEntry{bg.failure.Target}, true)
	if walk.Err != nil || walk.PatchFailure() != nil || walk.Fit < 0 {
		t.Fatalf("generation 1's actual clean replay did not record: %+v", walk)
	}
	firstSaid, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || firstSaid.Resolution == nil || firstSaid.Resolution.Generation != firstSaid.Generation {
		t.Fatalf("generation 1 marker setup failed: %+v err=%v", firstSaid, err)
	}
	second := publishTestBackgroundFailureGeneration(t, bg.fork, bg.failure, "generation-two-before-repair")
	noteBackgroundOutcome(bg.fork, richtext.Printer{W: io.Discard})
	if second.Generation != firstSaid.Generation+1 {
		t.Fatalf("second failure did not advance the generation: gen1=%d gen2=%d", firstSaid.Generation, second.Generation)
	}
	bg.fx.now = bg.fx.now.Add(time.Second)
	var out bytes.Buffer
	result := treeAdvance(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		force: true, out: &out, errw: &out})
	if result.patchFailure != nil || result.delivery.PatchFailure != nil || strings.Contains(result.problem, "check authority changed") {
		t.Fatalf("the actual forced foreground replay did not repair generation 2: %+v\n%s", result, out.String())
	}
	resolved, err := readBackgroundOutcome(backgroundSaidPath(bg.fork.Key()))
	if err != nil || resolved.Generation != second.Generation || resolved.Resolution == nil ||
		resolved.Resolution.Generation != second.Generation {
		t.Fatalf("generation 2 was not repaired by the actual authorized recording: %+v err=%v", resolved, err)
	}
	resolvedBytes, err := os.ReadFile(backgroundSaidPath(bg.fork.Key()))
	if err != nil {
		t.Fatal(err)
	}
	if err := preserveBackgroundOutcomeClaim(bg.outcome); err != nil {
		t.Fatalf("could not create the older append-only fallback fixture: %v", err)
	}
	afterBytes, err := os.ReadFile(backgroundSaidPath(bg.fork.Key()))
	if err != nil || !bytes.Equal(afterBytes, resolvedBytes) {
		t.Fatalf("the older fallback replaced or mutated the newer resolution marker: err=%v", err)
	}
	var report bytes.Buffer
	delivered, _ := servingTreeOf(bg.fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		launch: true, errw: &report}, "", false)
	if delivered.patchFailure != nil || delivered.delivery.PatchFailure != nil {
		t.Fatalf("the older fallback displaced current newer-generation repair authority: %+v\n%s", delivered, report.String())
	}
	files, err := backgroundOutcomeFiles(bg.fork.Key())
	if err != nil {
		t.Fatal(err)
	}
	oldClaim := false
	for _, file := range files {
		if file.Outcome.Generation == bg.outcome.Generation && file.Outcome.PatchFailure != nil {
			oldClaim = true
		}
	}
	if !oldClaim {
		t.Fatal("the older fallback was not retained as a recovery-readable claim")
	}
}

func TestBackgroundResultKeepsAnEarlyFailureReason(t *testing.T) {
	o := backgroundOutcome{At: time.Now().Unix()}
	backgroundResult(&o, advanceResult{delivery: entrypoint.ForkDelivery{Reason: "the patch series is unreadable"}}, false)
	if o.State != bgFailed || o.Error != "the patch series is unreadable" || o.RetryAt <= o.At {
		t.Errorf("early failure outcome = %+v", o)
	}
}

// All keys may be checked together, but each NoWait pipeline is allowed to defer a sibling sharing
// its mirror. Independent repositories must still build in parallel under the existing pool bound.
func TestBackgroundAdvancesBuildIndependentKeysInParallel(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	other := t.TempDir()
	gitCmd(t, "", "clone", pf.repo, other)
	writeFile(t, filepath.Join(pf.packs, "treepool", "pack.json"), `{"name":"treepool","contributes":[`+
		`{"kind":"files","into":".tool/ext/ext-a","source":"git+file://`+pf.repo+`?ref=main"},`+
		`{"kind":"files","into":".tool/ext/ext-b","source":"git+file://`+other+`?ref=main"}]}`)
	pf.child = pf.bothRunning(t)
	var out bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA, poolKeyB), &out)
	if pf.peak != 2 {
		t.Errorf("background build peak = %d, want 2:\n%s", pf.peak, out.String())
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		if o := outcomeOf(t, key); o.State != bgMoved {
			t.Errorf("%s outcome = %+v:\n%s", key, o, out.String())
		}
	}
}

// Replay bookkeeping and an advance with no completed build are not the settle/CAS exception.
func TestBackgroundReplayAndFinishSkipABusyRecord(t *testing.T) {
	readyForABackgroundAdvance(t)
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	var out bytes.Buffer
	a, early := newAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		background: true, out: &out, errw: &out})
	if early != nil {
		t.Fatal("could not initialize the advance")
	}
	checked := a.packs.CheckPatched(f.CheckWant(a.series), packsrc.CheckOptions{Now: patchedNow})
	if checked.Err != nil {
		t.Fatal(checked.Err)
	}
	commit := checked.Record.Candidates(a.in)[0].Commit
	holding, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(released)
		_ = patchedAdvanceStore(true).WithCheckRecord(f.Key(), nil, func(*packsrc.CheckRecord, error, func() error) (bool, error) {
			close(holding)
			<-release
			return false, nil
		})
	}()
	<-holding
	done := make(chan struct{})
	var walk packsrc.WalkResult
	var result advanceResult
	go func() {
		walk = a.walk([]packsrc.ListEntry{{Commit: commit}}, true)
		result = a.finish(nil, forkBuild{}, 0, nil, "")
		close(done)
	}()
	busy := false
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		busy = true
	}
	close(release)
	<-released
	<-done
	if busy {
		t.Fatal("non-settle background record work waited for its lock")
	}
	if walk.Err != nil || walk.Fit < 0 || len(walk.Results) != 1 || !walk.Results[0].Clean ||
		result.skipped == "" || !strings.Contains(out.String(), "recording the replay:") ||
		!strings.Contains(out.String(), "the next background advance tries again") {
		t.Errorf("clean walk and busy record persistence were not kept distinct: walk=%+v finish=%+v\n%s",
			walk, result, out.String())
	}
}

// Selection itself must defer before any advance: a fetched configured pack can have its current
// commit in the mirror while a concurrent checkout holds the lock and has not published completion.
// Drive the production user-config/selection path, not a local-pack resolver or stubbed selection.
func TestBackgroundSelectionDefersAFetchedPackWhoseCheckoutLockIsHeld(t *testing.T) {
	pf := newPoolFixture(t)
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := gitPackRepoWith(t, map[string]string{"pack.json": mustRead(t, filepath.Join(pf.packs, "treepool", "pack.json"))})
	source := "git+file://" + repo + "?ref=main"
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"`+source+`","name":"treepool"}]}`)
	store := &packsrc.Store{Dir: paths.PacksDir(), Detached: true}
	addr, err := packsrc.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.Sync(addr)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Materialize(addr, commit)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve an incomplete checkout's bytes while its owner holds the mirror lock.
	if err := os.Remove(filepath.Join(res.Root, ".yolo-pack-complete")); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(res.Root, "partial-owner-bytes")
	writeFile(t, partial, "still owned by the checkout")
	locks, err := filepath.Glob(filepath.Join(store.Dir, "locks", "*.lock"))
	if err != nil || len(locks) != 1 {
		t.Fatalf("mirror lock = %v (%v)", locks, err)
	}
	lk, err := pidlock.Acquire(locks[0], pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	var advances atomic.Int32
	prev := treeAdvance
	treeAdvance = func(f packload.Fork, o advanceOptions) advanceResult { advances.Add(1); return advanceResult{} }
	t.Cleanup(func() { treeAdvance = prev })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuffer
	var wg sync.WaitGroup
	// Simulate subsequent launches too: no background resolver may queue behind the held lock.
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); runBackgroundAdvanceUnder(ctx, bgArgs(poolKeyA), &out) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	prompt := true
	select {
	case <-done:
	case <-time.After(time.Second):
		prompt = false
	}
	cancel()
	cancelledPromptly := true
	select {
	case <-done:
	case <-time.After(time.Second):
		cancelledPromptly = false
	}
	if !prompt || !cancelledPromptly {
		lk.Release() // let the broken ordinary resolver drain; never leave test goroutines queued
		<-done
		t.Fatalf("fetched-pack selection queued on its mirror: prompt=%v cancellation=%v\n%s", prompt, cancelledPromptly, out.String())
	}
	if advances.Load() != 0 || outcomeOf(t, poolKeyA).State != bgSkipped || pidlock.Held(backgroundKeyLock(poolKeyA)) {
		t.Fatalf("busy selection advanced or left a running key/backlog (%d):\n%s", advances.Load(), out.String())
	}
	if got := mustRead(t, partial); got != "still owned by the checkout" {
		t.Errorf("incomplete checkout changed: %q", got)
	}
	// A cancelled invocation must also finish with the mirror still locked and leave the checkout.
	runBackgroundAdvanceUnder(ctx, bgArgs(poolKeyA), &out)
	if got := outcomeOf(t, poolKeyA); got.State != bgInterrupted {
		t.Errorf("cancelled selection = %+v", got)
	}
	if advances.Load() != 0 {
		t.Error("cancelled selection entered an advance")
	}
	lk.Release()
	// Deferral does not strip fetched packs from selection permanently: once free, the same
	// resolver materializes the current commit and resolves its contributed key normally.
	runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &out)
	if advances.Load() != 1 || outcomeOf(t, poolKeyA).State != bgNothing {
		t.Errorf("free fetched selection did not resume (%d):\n%s", advances.Load(), out.String())
	}
}

// A real replay I/O failure must keep its cause even while the admitted last-good tree is handed.
func TestBackgroundReplayFailureReportsItsCauseWhileLastGoodServes(t *testing.T) {
	fx := newTreeFixture(t, "")
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	var firstOut bytes.Buffer
	first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &firstOut, errw: &firstOut})
	if first.delivery.Key == "" {
		t.Fatalf("first build failed: %+v\n%s", first, firstOut.String())
	}
	candidate := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	failFile := filepath.Join(t.TempDir(), "fail")
	writeFile(t, failFile, candidate)
	patchedGitWrapper(t, failReadingFilesAt(failFile))
	var out bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), &out)
	o := outcomeOf(t, f.Key())
	want := "simulated: cannot read " + candidate
	if o.State != bgFailed || !strings.Contains(o.Error, want) || strings.Contains(o.Error, "\n") {
		t.Errorf("replay cause lost while last-good serves: %+v\n%s", o, out.String())
	}
	serving := servingTree(f, advanceOptions{platform: patchedTestPlatform, out: &out, errw: &out}, "")
	if serving.delivery.Key != first.delivery.Key || len(fx.builds) != 1 {
		t.Errorf("last-good changed: %+v", serving)
	}
	var next bytes.Buffer
	noteBackgroundOutcome(f, richtext.Printer{W: &next})
	if !strings.Contains(next.String(), want) {
		t.Errorf("next-launch report lacks concrete cause:\n%s", next.String())
	}
}

// A typed replay refusal must retain its required git version and update remedy even when the
// good tree serves; do not turn its special remedy into serveOr's generic "tries again" advice.
func TestBackgroundOldGitReportsItsRemedyWhileLastGoodServes(t *testing.T) {
	fx := newTreeFixture(t, "")
	f := fx.tree(t)
	var out bytes.Buffer
	first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &out, errw: &out})
	if first.delivery.Key == "" {
		t.Fatalf("first build failed: %+v\n%s", first, out.String())
	}
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	patchedGitWrapper(t, `if [ "$1" = version ]; then echo "git version 2.39.5"; exit 0; fi`)
	runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), &out)
	o := outcomeOf(t, f.Key())
	if o.State != bgFailed || strings.Contains(o.Error, "\n") || strings.Contains(o.Error, "tries again") ||
		o.RetryAt != fx.now.Add(packsrc.BranchRefreshInterval).Unix() {
		t.Errorf("typed old-git outcome lost its special failure/remedy or changed retry: %+v\n%s", o, out.String())
	}
	serving := servingTree(f, advanceOptions{platform: patchedTestPlatform, out: &out, errw: &out}, "")
	if serving.delivery.Key != first.delivery.Key || serving.delivery.Reason != "" || len(fx.builds) != 1 {
		t.Errorf("old git changed last-good delivery: %+v\n%s", serving, out.String())
	}
	var next bytes.Buffer
	noteBackgroundOutcome(f, richtext.Printer{W: &next})
	for _, want := range []string{"needs git 2.40 or newer", "the host's git is 2.39.5", "update git on the host"} {
		if !strings.Contains(o.Error, want) || !strings.Contains(next.String(), want) {
			t.Errorf("old-git outcome or claimed report lacks %q: %+v\n%s", want, o, next.String())
		}
	}
	if strings.Contains(next.String(), "tries again") {
		t.Errorf("old-git report added a generic retry instead of its remedy:\n%s", next.String())
	}
	if said, err := readBackgroundOutcome(backgroundSaidPath(f.Key())); err != nil || said.Error != o.Error {
		t.Errorf("claimed old-git outcome = %+v (%v), want the original concrete cause", said, err)
	}
}

// A genuine application conflict is fatal to the background update, even though the admitted Good
// remains unchanged. No older fit or base is built after the conflict.
func TestBackgroundNoFitReportsItsCauseWhileLastGoodServes(t *testing.T) {
	fx := newTreeFixture(t, "")
	f := fx.tree(t)
	var out bytes.Buffer
	first := treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &out, errw: &out})
	if first.delivery.Key == "" {
		t.Fatalf("first build failed: %+v\n%s", first, out.String())
	}
	candidate := fx.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	runBackgroundAdvanceUnder(context.Background(), bgArgs(f.Key()), &out)
	if !strings.Contains(out.String(), "ERROR: "+f.Key()+": patch application failed at upstream v1.1.0") ||
		!strings.Contains(out.String(), "Operation stopped; no older fit or base will be built.") {
		t.Fatalf("the genuine conflict was not fatal before any older fit or base:\n%s", out.String())
	}
	o := outcomeOf(t, f.Key())
	if o.State != bgFailed || o.PatchFailure == nil || o.PatchFailure.Target.Commit != candidate ||
		o.PatchFailure.Kind != "conflict" || o.PatchFailure.Member != "0001-ten.patch" {
		t.Errorf("background outcome did not preserve the genuine typed conflict: %+v\n%s", o, out.String())
	}
	serving := servingTree(f, advanceOptions{platform: patchedTestPlatform, out: &out, errw: &out}, "")
	if serving.delivery.Key != first.delivery.Key || serving.delivery.PatchFailure == nil || len(fx.builds) != 1 {
		t.Errorf("the fatal conflict changed Good or escaped current typed authority: %+v builds=%d\n%s",
			serving, len(fx.builds), out.String())
	}
	after := patchedRecordOf(t, f.Key())
	if after.Good == nil || after.Good.Entry != first.delivery.Key {
		t.Errorf("the genuine conflict changed the admitted Good: first=%s record=%+v", first.delivery.Key, after.Good)
	}
	var next bytes.Buffer
	noteBackgroundOutcome(f, richtext.Printer{W: &next})
	if !strings.Contains(next.String(), "could not update it: patch application failed at v1.1.0") ||
		!strings.Contains(next.String(), "0001-ten.patch") {
		t.Errorf("claimed typed failure report lost its actual cause:\n%s", next.String())
	}
}
