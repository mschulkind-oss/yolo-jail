package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func stubPreparationWorkerStart(t *testing.T) {
	t.Helper()
	previous := preparationSpawn
	preparationSpawn = func() error { return nil }
	t.Cleanup(func() { preparationSpawn = previous })
}

// This is the production registration call from runBuildSlot followed by the production timer loop.
// It fails if registration is removed or if the loop stops dispatching a timer tick.
func TestPreparationTimerAdvancesWithoutAnotherLaunch(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	builds := pf.countBuilds()
	buildFinished := make(chan struct{}, 32)
	child := pf.child
	pf.child = func(ctx context.Context, build forkBuild) int {
		result := child(ctx, build)
		buildFinished <- struct{}{}
		return result
	}
	_, _ = pf.launch(t, "podman", &run.ActInterrupt{})
	if builds.Load() != 0 {
		t.Fatalf("next-launch registration built trees before the timer tick: %d", builds.Load())
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		got, err := readPreparationSnapshot(key)
		if err != nil || got.Key != key || got.Generation == 0 {
			t.Fatalf("launch did not persist %s subscription: %+v err=%v", key, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	drain := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard, nil, drain) }()
	logState := func() {
		for _, key := range []string{poolKeyA, poolKeyB} {
			outcome, outcomeErr := readBackgroundOutcome(backgroundOutcomePath(key))
			snapshot, snapshotErr := readPreparationSnapshot(key)
			t.Logf("%s outcome=%+v outcome_error=%v snapshot=%+v snapshot_error=%v", key, outcome, outcomeErr, snapshot, snapshotErr)
		}
	}
	select {
	case ticks <- pf.now:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("scheduler did not receive its production timer tick")
	}
	// The fake child callback precedes the scheduler's persisted result snapshot. Drain the injected
	// production loop after both callbacks so assertions observe completed task results, not an
	// intermediate state.
	dispatchDeadline := time.NewTimer(5 * time.Second)
	defer dispatchDeadline.Stop()
	for range 2 {
		select {
		case <-buildFinished:
		case <-dispatchDeadline.C:
			cancel()
			<-done
			logState()
			t.Fatalf("timer did not start both registered tree builds (build count %d)", builds.Load())
		}
	}
	close(drain)
	// Bound a genuinely stuck scheduler by the shared test deadline; a completed drain is signaled
	// by the production loop and does not wait for this timer.
	finishDeadline := time.NewTimer(testsupport.ReadinessBudget(t))
	defer finishDeadline.Stop()
	select {
	case err := <-done:
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-finishDeadline.C:
		cancel()
		<-done
		logState()
		t.Fatal("scheduler did not finish and publish its registered tree tasks within the test readiness budget")
	}
	if builds.Load() < 2 {
		t.Fatalf("timer built %d registered trees, want both", builds.Load())
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		got, err := readBackgroundOutcome(backgroundOutcomePath(key))
		if err != nil || got.To == "" {
			t.Fatalf("%s: scheduler did not publish an admitted outcome: %+v err=%v", key, got, err)
		}
		snapshot, err := readPreparationSnapshot(key)
		if err != nil || snapshot.Phase != "admitted" || snapshot.ServingEntry != got.To {
			t.Fatalf("%s: scheduler result snapshot disagrees with outcome: %+v outcome=%+v err=%v", key, snapshot, got, err)
		}
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		if got := outcomeOf(t, key); got.To == "" {
			t.Errorf("%s: timer did not publish an admitted outcome: %+v", key, got)
		}
	}
	for _, f := range packload.PatchedTrees(selectConfiguredHostPacks().packs) {
		before, _ := readPreparationSnapshot(f.Key())
		noteBackgroundOutcome(f, richtext.Printer{W: io.Discard})
		if _, err := os.Stat(backgroundSaidPath(f.Key())); err != nil {
			t.Fatal(err)
		}
		after, _ := readPreparationSnapshot(f.Key())
		var status bytes.Buffer
		statusLine := f.Key() + " [podman/" + patchedTestPlatform + "]"
		if code := printPreparationStatus(&status, io.Discard, false, ""); code != 0 || after.Phase != "admitted" || after.ServingEntry != before.ServingEntry || !strings.Contains(status.String(), statusLine) || !strings.Contains(status.String(), "admitted entry: "+before.ServingEntry) {
			t.Fatalf("public all-record status lost fixture identity or actual admission: %+v status=%s", after, status.String())
		}
	}
}

func TestPreparationNoDueRegistrationCanBecomeDueOnLaterTick(t *testing.T) {
	pf := newPoolFixture(t)
	if got, out := pf.launch(t, "podman", &run.ActInterrupt{}); got[poolKeyA].Dir == "" || out == "" {
		t.Fatalf("initial tree build failed: %+v\n%s", got, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	builds := pf.countBuilds()
	buildFinished := make(chan struct{}, 2)
	child := pf.child
	pf.child = func(ctx context.Context, build forkBuild) int {
		result := child(ctx, build)
		buildFinished <- struct{}{}
		return result
	}
	_, _ = pf.launch(t, "podman", &run.ActInterrupt{})
	if builds.Load() != 0 {
		t.Fatalf("launch did work before due: %d builds", builds.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	drain := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard, nil, drain) }()
	// Three synchronous sends ensure the first no-due evaluation completed, without a sleep.
	ticks <- pf.now
	ticks <- pf.now
	ticks <- pf.now
	if builds.Load() != 0 {
		t.Fatalf("no-due timer tick built %d trees", builds.Load())
	}
	pf.commit(t, "", map[int]string{5: "five"})
	pf.setNow(pf.now.Add(2 * time.Hour))
	ticks <- pf.now
	deadline := time.NewTimer(testsupport.ReadinessBudget(t))
	defer deadline.Stop()
	for range 2 {
		select {
		case <-buildFinished:
		case <-deadline.C:
			cancel()
			<-done
			t.Fatalf("later due tick did not finish both real builds: builds=%d", builds.Load())
		}
	}
	// Both real fixture builds have returned. Drain the production loop so its persisted
	// admissions, not a counter or an intermediate callback, establish completion.
	close(drain)
	select {
	case err := <-done:
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-deadline.C:
		cancel()
		<-done
		t.Fatal("scheduler did not drain both completed builds")
	}
	if builds.Load() < 2 {
		t.Fatalf("later due tick built %d trees, want both", builds.Load())
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		if got, err := readBackgroundOutcome(backgroundOutcomePath(key)); err != nil || got.To == "" {
			t.Fatalf("later due tick did not admit %s: %+v %v", key, got, err)
		}
	}
}

func TestPreparationConcurrentRegistrationCoalesces(t *testing.T) {
	pf := newPoolFixture(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	req := run.TreeBuildRequest{Trees: trees, Build: true, Runtime: "podman", Platform: patchedTestPlatform}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := registerTreePreparation(append(trees, trees[0]), req, io.Discard); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	state, err := preparationSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 2 {
		t.Fatalf("concurrent duplicate registrations created %d records: %+v", len(state), state)
	}
	for _, record := range state {
		if !record.Active || record.Generation == 0 {
			t.Errorf("registration is not active and versioned: %+v", record)
		}
	}
}

func TestPreparationMalformedUserConfigBlocksDispatch(t *testing.T) {
	pf := newPoolFixture(t)
	path := filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc")
	if err := os.WriteFile(path, []byte(`{"packs":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := currentPreparationTrees(nil); err == nil || !strings.Contains(err.Error(), "selection is incomplete") {
		t.Fatalf("malformed user config did not disable scheduler dispatch: %v", err)
	}
}

func TestPreparationBinaryMismatchIsVisible(t *testing.T) {
	newPoolFixture(t)
	executable := filepath.Join(t.TempDir(), "yolo")
	if err := os.WriteFile(executable, []byte("current binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := withPreparationState(func(state *preparationState) error {
		state.Worker = preparationWorker{Executable: executable, BinaryDigest: "old-binary-digest"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := preparationCheckBinaryIdentity(); err == nil || !strings.Contains(err.Error(), "binary changed") {
		t.Fatalf("worker binary mismatch did not stop dispatch: %v", err)
	}
}

func TestPackPrepareEmptySelectionDoesNotStartWorker(t *testing.T) {
	pf := newPoolFixture(t)
	if err := os.WriteFile(filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"), []byte(`{"packs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	starts := 0
	previous := preparationSpawn
	preparationSpawn = func() error { starts++; return nil }
	t.Cleanup(func() { preparationSpawn = previous })
	var out strings.Builder
	if rc := packPrepare([]string{"start"}, &out, io.Discard, false); rc != 0 {
		t.Fatalf("empty start rc=%d", rc)
	}
	if rc := packPrepare([]string{"status"}, &out, io.Discard, false); rc != 0 {
		t.Fatalf("read-only status rc=%d", rc)
	}
	if starts != 0 {
		t.Fatalf("empty selection or status started the worker %d times", starts)
	}
}

func TestPreparationStateCorruptionIsNotSuccess(t *testing.T) {
	newPoolFixture(t)
	if err := os.MkdirAll(filepath.Dir(preparationStatePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preparationStatePath(), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPreparationState(); err == nil || !strings.Contains(err.Error(), "yolo pack prepare start") {
		t.Fatalf("corrupt scheduler state was not reported with a repair action: %v", err)
	}
}

func TestPreparationAdmissionGuardRejectsChangedGeneration(t *testing.T) {
	newPoolFixture(t)
	stubPreparationWorkerStart(t)
	if err := withPreparationState(func(state *preparationState) error {
		state.Subscriptions = append(state.Subscriptions, preparationRecord{Key: poolKeyA, Runtime: "podman", Platform: patchedTestPlatform, Generation: 4, Active: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := preparationUpdateRecord(poolKeyA, "podman", patchedTestPlatform, func(r *preparationRecord) { r.Generation++ }); err != nil {
		t.Fatal(err)
	}
	committed := false
	err := preparationAdmissionGuard(poolKeyA, "podman", patchedTestPlatform, 4)(func() error { committed = true; return nil })
	if !errors.Is(err, errPreparationAdmissionStale) || committed {
		t.Fatalf("stale generation admitted: committed=%t err=%v", committed, err)
	}
}

func TestPreparationAdmissionGuardRechecksCurrentPackSelection(t *testing.T) {
	pf := newPoolFixture(t)
	previousTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	t.Cleanup(func() { treeUpdateTiming = previousTiming })
	stubPreparationWorkerStart(t)
	selected := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(selected) == 0 {
		t.Fatal("fixture has no selected trees")
	}
	f := selected[0]
	req := run.TreeBuildRequest{Build: true, Runtime: "podman", Platform: patchedTestPlatform}
	if err := registerTreePreparation([]packload.Fork{f}, req, io.Discard); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readPreparationSnapshot(f.Key())
	if err != nil || snapshot.Generation == 0 {
		t.Fatalf("fixture did not register a generation: %+v err=%v", snapshot, err)
	}
	if err := os.WriteFile(filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"), []byte(`{"packs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	committed := false
	err = preparationAdmissionGuard(f.Key(), req.Runtime, req.Platform, snapshot.Generation)(func() error { committed = true; return nil })
	if !errors.Is(err, errPreparationAdmissionStale) || committed {
		t.Fatalf("deselected pack admitted a prepared build: committed=%t err=%v", committed, err)
	}
}

func TestPreparationEnvironmentUsesDeclaredProviderExclusionsAndBoundedAllowlist(t *testing.T) {
	env := legacyPreparationEnvironment([]string{
		"HOME=/home/user", "PATH=/usr/bin", "LANG=C.UTF-8", "SSH_AUTH_SOCK=/run/ssh.sock",
		"GIT_SSH_COMMAND=ssh -F /home/user/.ssh/config", "YOLO_REPO_ROOT=/src", "YOLO_RUNTIME=podman",
		"YOLO_ALLOW_PATCH_FAILURES=1", "YOLO_WORKSPACE=/workspace", "GIT_DIR=/repo/.git",
		"UNKNOWN_AGENT_TOKEN=secret", "SYNTHETIC_PROVIDER_KEY=secret", "HTTPS_PROXY=https://proxy",
	}, []string{"SYNTHETIC_PROVIDER_KEY"})
	got := map[string]string{}
	for _, item := range env {
		name, value, _ := strings.Cut(item, "=")
		got[name] = value
	}
	for _, name := range []string{"HOME", "PATH", "LANG", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND", "YOLO_RUNTIME", "HTTPS_PROXY"} {
		if got[name] == "" {
			t.Errorf("required worker environment %s was removed: %v", name, env)
		}
	}
	for _, name := range []string{"YOLO_REPO_ROOT", "YOLO_ALLOW_PATCH_FAILURES", "YOLO_WORKSPACE", "GIT_DIR", "UNKNOWN_AGENT_TOKEN", "SYNTHETIC_PROVIDER_KEY"} {
		if _, ok := got[name]; ok {
			t.Errorf("unbounded or credential variable %s crossed into worker environment", name)
		}
	}
	if !reflect.DeepEqual(env, legacyPreparationEnvironment([]string{
		"HTTPS_PROXY=https://proxy", "SYNTHETIC_PROVIDER_KEY=secret", "YOLO_RUNTIME=podman",
		"GIT_SSH_COMMAND=ssh -F /home/user/.ssh/config", "SSH_AUTH_SOCK=/run/ssh.sock", "LANG=C.UTF-8", "PATH=/usr/bin", "HOME=/home/user",
	}, []string{"SYNTHETIC_PROVIDER_KEY"})) {
		t.Error("bounded environment should be deterministic independent of inherited ordering")
	}
}

func TestPreparationRevocationCancelsPausedChild(t *testing.T) {
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("initial tree build failed: %+v\\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.setNow(pf.now.Add(2 * time.Hour))
	stubPreparationWorkerStart(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	pf.child = func(ctx context.Context, _ forkBuild) int {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(cancelled)
		return 1
	}
	_, _ = pf.launch(t, "podman", &run.ActInterrupt{})
	ctx, stop := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- pf.now
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		stop()
		<-done
		t.Fatal("scheduler did not start the paused build child")
	}
	pf.agentUpdates(t, `{"treepool":false}`)
	ticks <- pf.now
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		stop()
		<-done
		t.Fatal("selection removal did not cancel the owned child")
	}
	for {
		state, err := preparationSubscriptions()
		if err != nil {
			stop()
			<-done
			t.Fatal(err)
		}
		inactive := true
		for _, r := range state {
			if r.Active {
				inactive = false
			}
		}
		if inactive {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, key := range []string{poolKeyA, poolKeyB} {
		snapshot, err := readPreparationSnapshot(key)
		if err != nil || snapshot.Phase != "held" || !strings.Contains(snapshot.Reason, "agent_updates") {
			t.Errorf("revoked key %s was not retained held with its policy reason: %+v err=%v", key, snapshot, err)
		}
	}
}

// A key another advance holds is recorded busy without a success stamp, the free key behind it is
// still admitted from the same timer tick, and the next tick retries the released key. Each
// assertion is made when the scheduler reports that task's result recorded
// (preparationTaskSettled), never at a wall-clock deadline: B's admission is a real check,
// shared-demand handoff and capture whose duration follows machine load (a 5 s poll failed on a
// loaded landing gate with B still checking). The readiness budget bounds only a scheduler that
// never settles a task.
func TestPreparationBusyKeyRetriesWithoutSuccessStamp(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	_, _ = pf.launch(t, "podman", &run.ActInterrupt{})
	// The observer runs on the scheduler's goroutine before its next dispatch pass, so the snapshot it
	// reads is the record exactly as that task's result left it.
	type settlement struct {
		key      string
		snapshot preparationSnapshot
		err      error
	}
	settled := make(chan settlement, 16)
	previousSettled := preparationTaskSettled
	preparationTaskSettled = func(key string) {
		snapshot, err := readPreparationSnapshot(key)
		settled <- settlement{key, snapshot, err}
	}
	t.Cleanup(func() { preparationTaskSettled = previousSettled })
	lk, err := pidlock.Acquire(backgroundKeyLock(poolKeyA), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	ctx, stop := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	stopped := false
	defer func() {
		if !stopped {
			stop()
			<-done
		}
	}()
	budget := time.NewTimer(testsupport.ReadinessBudget(t))
	defer budget.Stop()
	// awaitSettled returns key's snapshot at its next recorded task result, failing the test when
	// the scheduler stops first or never settles key within the readiness budget. The scheduler runs
	// one task at a time in record order, so another key's settlement may arrive first; it is kept
	// for that key's own wait.
	early := map[string][]preparationSnapshot{}
	awaitSettled := func(key string) preparationSnapshot {
		t.Helper()
		for {
			if queued := early[key]; len(queued) > 0 {
				early[key] = queued[1:]
				return queued[0]
			}
			select {
			case got := <-settled:
				if got.err != nil {
					t.Fatalf("%s: settled result unreadable: %v", got.key, got.err)
				}
				early[got.key] = append(early[got.key], got.snapshot)
			case err := <-done:
				stopped = true
				t.Fatalf("scheduler stopped before settling %s: %v", key, err)
			case <-budget.C:
				a, _ := readPreparationSnapshot(poolKeyA)
				b, _ := readPreparationSnapshot(poolKeyB)
				t.Fatalf("scheduler never settled %s: A=%+v B=%+v", key, a, b)
			}
		}
	}
	ticks <- pf.now
	if snapshot := awaitSettled(poolKeyA); snapshot.Phase != "busy" {
		t.Fatalf("busy key did not defer: %+v", snapshot)
	}
	if outcome, err := readBackgroundOutcome(backgroundOutcomePath(poolKeyA)); err == nil && outcome.Pid == os.Getpid() && outcome.State == bgMoved {
		t.Fatal("busy skip manufactured a successful outcome")
	}
	if snapshot := awaitSettled(poolKeyB); snapshot.Phase != "admitted" || snapshot.ServingEntry == "" {
		t.Fatalf("free B starved while A remained locked: %+v", snapshot)
	}
	lk.Release()
	ticks <- pf.now
	awaitSettled(poolKeyA)
	if outcome, err := readBackgroundOutcome(backgroundOutcomePath(poolKeyA)); err != nil || outcome.To == "" {
		t.Fatalf("released busy key did not retry: outcome=%+v err=%v", outcome, err)
	}
	stopped = true
	stop()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPreparationStatusDoesNotMutateOrConsumeOutcomes(t *testing.T) {
	newPoolFixture(t)
	stubPreparationWorkerStart(t)
	platform := capture.Platform()
	if err := withPreparationState(func(s *preparationState) error {
		s.Subscriptions = append(s.Subscriptions, preparationRecord{Key: poolKeyA, Runtime: captureRuntime(), Platform: platform, Generation: 1, Active: true, Phase: "admitted", ServingEntry: "entry"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(preparationStatePath())
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if rc := printPreparationStatus(&out, io.Discard, false, poolKeyA); rc != 0 {
		t.Fatalf("status rc=%d", rc)
	}
	after, err := os.ReadFile(preparationStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read-only status changed scheduler state")
	}
	if !strings.Contains(out.String(), poolKeyA+" ["+captureRuntime()+"/"+platform+"]") ||
		!strings.Contains(out.String(), "admitted") || !strings.Contains(out.String(), "entry") {
		t.Fatalf("status omitted its exact platform-qualified persisted snapshot: %s", out.String())
	}
}
