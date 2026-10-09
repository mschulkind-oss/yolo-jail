package cli

// backgroundadvance_test.go pins THE BACKGROUND ADVANCE itself (backgroundadvance.go;
// docs/design/pi-extension-store-builds.md §7.4, XB-D19): its keys resolved from the user config, a
// held lock skipped and never waited for, a build jail not known gone kept with its candidate
// pending, and its verb and signals.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// A failure before the check (for example an unreadable series) still needs a next-launch report,
// with the actual cause rather than a silent "nothing" or a generic replay failure.
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
	if !errors.Is(walk.Err, packsrc.ErrLockHeld) || result.skipped == "" {
		t.Errorf("walk = %+v, finish = %+v, want both skipped:\n%s", walk, result, out.String())
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

// A walk settling the new upstream as a conflict reaches serving noFit, not serveOr; its computed
// why must survive the successful last-good delivery in the outcome and claimed next-launch report.
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
	if !strings.Contains(out.String(), "0001-ten.patch conflicts in f.txt") {
		t.Fatalf("the replay did not reach noFit's settled conflict:\n%s", out.String())
	}
	o := outcomeOf(t, f.Key())
	want := "upstream v1.1.0 (" + shortSHA(candidate) + ") does not take " + f.Label() + "'s patch series"
	if o.State != bgFailed || o.Error != want || o.RetryAt != fx.now.Add(packsrc.BranchRefreshInterval).Unix() {
		t.Errorf("noFit lost its computed cause or changed retry: %+v; want %q\n%s", o, want, out.String())
	}
	serving := servingTree(f, advanceOptions{platform: patchedTestPlatform, out: &out, errw: &out}, "")
	if serving.delivery.Key != first.delivery.Key || serving.delivery.Reason != "" || len(fx.builds) != 1 {
		t.Errorf("noFit changed last-good delivery: %+v\n%s", serving, out.String())
	}
	var next bytes.Buffer
	noteBackgroundOutcome(f, richtext.Printer{W: &next})
	if !strings.Contains(next.String(), want) {
		t.Errorf("claimed noFit report lacks computed cause:\n%s", next.String())
	}
	if said, err := readBackgroundOutcome(backgroundSaidPath(f.Key())); err != nil || said.Error != o.Error {
		t.Errorf("claimed noFit outcome = %+v (%v), want the original concrete cause", said, err)
	}
}
