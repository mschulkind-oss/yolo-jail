package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// A timeout reports the production command's completion signal, persisted subscriptions, and the
// per-subscription shared-demand worker/request files. Passing runs emit none of this state.
func preparationSchedulerTimeoutState(commandDone <-chan int) string {
	commandState := "not signaled"
	if len(commandDone) != 0 {
		commandState = "result buffered"
	}
	state, stateErr := loadPreparationState()
	if stateErr != nil {
		return fmt.Sprintf("commandDone=%s schedulerStateError=%v", commandState, stateErr)
	}
	var demands []string
	for _, subscription := range state.Subscriptions {
		dir := sharedDemandDir(subscription.Key, subscription.Runtime, subscription.Platform)
		requests, requestErr := filepath.Glob(filepath.Join(dir, "request-*.json"))
		for i := range requests {
			requests[i] = filepath.Base(requests[i])
		}
		workerErr, workerErrRead := os.ReadFile(filepath.Join(dir, "worker-error.json"))
		demands = append(demands, fmt.Sprintf("%s/%s/%s active=%t generation=%d workerLocked=%t requests=%v requestError=%v workerError=%q workerErrorRead=%v",
			subscription.Key, subscription.Runtime, subscription.Platform, subscription.Active, subscription.Generation,
			pidlock.Held(filepath.Join(dir, "worker.lock")), requests, requestErr, string(workerErr), workerErrRead))
	}
	return fmt.Sprintf("commandDone=%s schedulerWorker=%+v subscriptions=%+v sharedDemand=%v", commandState, state.Worker, state.Subscriptions, demands)
}

func awaitPreparationCommandOwner(t *testing.T, root string, commandDone <-chan int, budget time.Duration) preparationWorker {
	t.Helper()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, state, err := probePreparationWorker(root)
		if err == nil && state == preparationAlive {
			return record
		}
		select {
		case <-timer.C:
			t.Fatalf("preparation command did not publish a live owner; %s", preparationSchedulerTimeoutState(commandDone))
			return preparationWorker{}
		case <-ticker.C:
		}
	}
}

// Production command, authenticated control, O timer dispatcher and independent
// child demand all participate. Deleting the command's lifecycle/dispatch wiring
// leaves this test without the initial child or the proven stop/drain response.
func TestPreparationSchedulerCommandAuthenticatedReplacementDrainsOriginalChild(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	_, _ = pf.launch(t, "podman", &run.ActInterrupt{})
	entered, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	pf.child = func(ctx context.Context, b forkBuild) int {
		if b.Fork.Key() == poolKeyA {
			once.Do(func() { close(entered) })
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
				return 130
			case <-release:
			}
		}
		return 0
	}
	commandDone := make(chan int, 1)
	go func() { commandDone <- runPreparationSchedulerCommand() }()
	readiness := testsupport.ReadinessBudget(t)
	owner := awaitPreparationCommandOwner(t, preparationRoot(), commandDone, readiness)
	commandJoined := false
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if commandJoined {
			return
		}
		budget := testsupport.ReadinessBudget(t)
		if budget <= 0 {
			budget = time.Second
		}
		deadline := time.Now().Add(budget)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		if err := controlPreparationWorker(ctx, preparationRoot(), owner.Identity, "stop"); err != nil {
			t.Errorf("stop scheduler during cleanup: %v; %s", err, preparationSchedulerTimeoutState(commandDone))
		}
		if !commandJoined {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case rc := <-commandDone:
				commandJoined = true
				if rc != 0 {
					t.Errorf("scheduler command cleanup rc=%d; %s", rc, preparationSchedulerTimeoutState(commandDone))
				}
			case <-timer.C:
				t.Errorf("scheduler command did not finish during bounded cleanup; %s", preparationSchedulerTimeoutState(commandDone))
			}
		}
	})
	select {
	case <-entered:
	case <-time.After(readiness):
		t.Fatalf("production command never dispatched initial work; %s", preparationSchedulerTimeoutState(commandDone))
	}
	if err := controlPreparationWorker(context.Background(), preparationRoot(), owner.Identity, "replace"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
		t.Fatal("replacement cancelled original child instead of draining")
	case <-time.After(100 * time.Millisecond):
	}
	if _, live, err := probePreparationWorker(preparationRoot()); err != nil || live != preparationAlive {
		t.Fatalf("draining owner lost authenticated lease: %s %v", live, err)
	}
	close(release)
	select {
	case rc := <-commandDone:
		commandJoined = true
		if rc != 0 {
			t.Fatalf("replacement drain rc=%d", rc)
		}
	case <-time.After(readiness):
		t.Fatalf("drained scheduler failed to release original lease; %s", preparationSchedulerTimeoutState(commandDone))
	}
	if _, live, err := probePreparationWorker(preparationRoot()); err != nil || live != preparationGone {
		t.Fatalf("original owner not proven gone: %s %v", live, err)
	}
	state, err := loadPreparationState()
	if err != nil || state.Worker.Identity != owner.Identity || state.Worker.StoppedAt.IsZero() {
		t.Fatalf("incarnation final state lost: %+v %v", state.Worker, err)
	}
}

func TestPreparationSchedulerUnknownSelectionRetainsButNeverAdmits(t *testing.T) {
	fx, _, _, good, _, _ := firstAdvance(t)
	f := fx.fork(t)
	identity, err := freshPreparationIdentity(f, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previous := sharedPreparationSpawn
	sharedPreparationSpawn = func(context.Context, preparationDemand) error { return nil }
	t.Cleanup(func() { sharedPreparationSpawn = previous })
	d := preparationDemand{Key: f.Key(), Runtime: "podman", Platform: patchedTestPlatform, Identity: identity}
	request, err := registerSharedDemand(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer request.release(false)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	// Malformed selection is unknown authority, not proof that an owner revoked it.
	writeFile(t, paths.UserConfigPath(), `{"packs":[`)
	_, err = authorizedPreparationDemands(ctx, request.dir, d.Identity)
	if err == nil || errors.Is(err, errPreparationAdmissionStale) {
		t.Fatalf("unknown selection classified as revocation: %v", err)
	}
	admitted := false
	if err := demandAdmissionGuard(ctx, request.dir, d, new(sync.Mutex))(func() error { admitted = true; return nil }); err == nil || admitted {
		t.Fatalf("unknown authority admitted: %v %v", admitted, err)
	}
	if fx.record(t).Good.Entry != good.delivery.Key {
		t.Fatal("unknown selection changed Good")
	}
}
