package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

type operationScopeTestKey struct{}

func TestProductionFloorOrdinaryOperationContextKeepsColdRunnerPosture(t *testing.T) {
	for _, parentKind := range []string{"background", "cancellable"} {
		t.Run(parentKind, func(t *testing.T) {
			fx := patchedFloorFixture(t)
			floor, program, _ := selectedProductionFloor(t)
			parent := context.WithValue(context.Background(), operationScopeTestKey{}, "floor-operation")
			var cancel context.CancelFunc
			if parentKind == "cancellable" {
				parent, cancel = context.WithCancel(parent)
				t.Cleanup(cancel)
			}
			before := fx.child
			originalScope := advanceActScope
			scopeEntries := 0
			advanceActScope = func(act *run.ActInterrupt, fn func(context.Context)) os.Signal {
				scopeEntries++
				return originalScope(act, fn)
			}
			t.Cleanup(func() { advanceActScope = originalScope })
			prepared, err := floor.PreparePatched(parent, program, true)
			if err != nil {
				t.Fatalf("cold production Floor preparation: %v", err)
			}
			if prepared == nil {
				t.Fatal("cold production Floor preparation returned no token")
			}
			if got := fx.child - before; got != 0 {
				t.Errorf("ordinary cold Floor operation selected %d child builds; want the direct first-build runner", got)
			}
			if scopeEntries != 0 {
				t.Errorf("ordinary cold Floor operation entered %d standalone scopes, want none", scopeEntries)
			}
		})
	}
}

func TestProductionFloorServingAdvanceRetainsOperationContext(t *testing.T) {
	fx := newFloorPatchedFixture(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	floor, program, _ := selectedProductionFloor(t)
	deadline := time.Now().Add(2 * time.Minute).Round(0)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), operationScopeTestKey{}, "serving-operation"), deadline)
	defer cancel()

	before := fx.child
	var childCtx context.Context
	originalChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, build forkBuild, streams captureStreams,
		color bool) (int, bool) {
		childCtx = ctx
		return originalChild(ctx, bound, staging, build, streams, color)
	}
	t.Cleanup(func() { forkBuildChild = originalChild })
	prepared, err := floor.PreparePatched(parent, program, true)
	if err != nil {
		t.Fatalf("serving production Floor preparation: %v", err)
	}
	if prepared == nil {
		t.Fatal("serving production Floor preparation returned no token")
	}
	if got := fx.child - before; got != 1 {
		t.Errorf("serving Floor advance ran %d child builds; want one", got)
	}
	if childCtx == nil {
		t.Fatal("serving Floor advance did not reach the child runner")
	}
	if got := childCtx.Value(operationScopeTestKey{}); got != "serving-operation" {
		t.Errorf("child context value = %v, want operation value", got)
	}
	if got, ok := childCtx.Deadline(); !ok || !got.Equal(deadline) {
		t.Errorf("child deadline = %v, %v; want %v", got, ok, deadline)
	}
	if childCtx.Done() == nil {
		t.Error("serving child context is not cancellable")
	}
}

func TestProductionFloorOperationCancellationReachesActualAdvance(t *testing.T) {
	fx := newFloorPatchedFixture(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	floor, program, _ := selectedProductionFloor(t)
	act := &run.ActInterrupt{}
	initialGood := fx.record(t).Good
	if initialGood == nil {
		t.Fatal("fixture did not start with an admitted Good build")
	}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), operationScopeTestKey{}, "cancel-operation"))
	defer cancel()
	operation := withActInterrupt(parent, act)
	entered := make(chan context.Context, 1)
	originalChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ captureStreams, _ bool) (int, bool) {
		entered <- ctx
		<-ctx.Done()
		return 130, false
	}
	t.Cleanup(func() { forkBuildChild = originalChild })
	result := make(chan error, 1)
	go func() {
		_, err := floor.PreparePatched(operation, program, true)
		result <- err
	}()
	select {
	case childCtx := <-entered:
		if childCtx.Value(operationScopeTestKey{}) != "cancel-operation" {
			t.Errorf("actual serving child lost the operation value: %v", childCtx.Value(operationScopeTestKey{}))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production serving advance never entered its cancellable child runner")
	}
	cancel()
	forkBuildChild = originalChild
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Errorf("cancelled Floor preparation error = %v, want independent context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled production serving advance did not return")
	}
	if act.Interrupted() {
		t.Error("ordinary operation cancellation marked the act interrupted")
	}
	if got := fx.record(t).Good; got == nil || got.Commit != initialGood.Commit {
		t.Errorf("cancelled advance changed the serving Good: %+v, want initial commit %s", got, initialGood.Commit)
	}
	for _, outcome := range fx.record(t).Outcomes {
		if outcome.Commit == v13 && outcome.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("operation cancellation was recorded as a failed build: %+v", outcome)
		}
	}
	fx.later(time.Minute)
	if _, err := floor.PreparePatched(context.Background(), program, true); err != nil {
		t.Fatalf("retry after operation cancellation: %v", err)
	}
	if got := fx.record(t).Good; got == nil || got.Commit != v13 {
		t.Errorf("cancellation left the newer build outside the retry path: %+v, want %s", got, v13)
	}
}

func TestProductionFloorStandaloneServingAdvanceUsesActualActScope(t *testing.T) {
	fx := newFloorPatchedFixture(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	floor, program, _ := selectedProductionFloor(t)
	parent := context.WithValue(context.Background(), operationScopeTestKey{}, "scope-operation")
	originalScope := advanceActScope
	scopeEntries := 0
	advanceActScope = func(act *run.ActInterrupt, fn func(context.Context)) os.Signal {
		scopeEntries++
		return originalScope(act, fn)
	}
	t.Cleanup(func() { advanceActScope = originalScope })

	if _, err := floor.PreparePatched(parent, program, true); err != nil {
		t.Fatalf("serving Floor preparation: %v", err)
	}
	if scopeEntries != 1 {
		t.Errorf("ordinary serving Floor advance entered ActInterrupt.Scope %d times, want once", scopeEntries)
	}
}

func TestPatchedAdvanceExternallyOwnedContextRetainsPosture(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), operationScopeTestKey{}, "external-lane"))
	defer cancel()
	act := &run.ActInterrupt{}
	originalScope := advanceActScope
	scopeEntries := 0
	advanceActScope = func(act *run.ActInterrupt, fn func(context.Context)) os.Signal {
		scopeEntries++
		return originalScope(act, fn)
	}
	t.Cleanup(func() { advanceActScope = originalScope })
	entered := make(chan context.Context, 1)
	originalChild := forkBuildChild
	childCalls := 0
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ captureStreams, _ bool) (int, bool) {
		childCalls++
		entered <- ctx
		<-ctx.Done()
		return 130, false
	}
	t.Cleanup(func() { forkBuildChild = originalChild })
	fork := fx.fork(t)
	result := make(chan advanceResult, 1)
	go func() {
		result <- advancePatchedFork(fork, advanceOptions{
			platform: fx.platform, launch: true, ctx: parent, act: act,
			out: io.Discard, errw: io.Discard,
		})
	}()
	select {
	case childCtx := <-entered:
		if childCtx.Value(operationScopeTestKey{}) != "external-lane" || childCtx.Done() == nil {
			t.Errorf("explicit external context did not reach its cancellable cold child: %v", childCtx)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit external context did not select its cold child runner")
	}
	cancel()
	var res advanceResult
	select {
	case res = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("external-context cancellation did not return the owned-lane advance")
	}
	if res.delivery.Key != "" || res.built {
		t.Errorf("cancelled cold external-lane advance published a build: %+v", res)
	}
	if childCalls != 1 {
		t.Errorf("explicit external context selected %d cold child builds, want one", childCalls)
	}
	if scopeEntries != 0 {
		t.Errorf("externally owned context opened %d standalone scopes, want none", scopeEntries)
	}
	if act.Interrupted() {
		t.Error("ordinary external context work marked its act interrupted")
	}
	if got := fx.record(t).Good; got != nil {
		t.Errorf("cancelled cold build unexpectedly admitted Good: %+v", got)
	}
	for _, outcome := range fx.record(t).Outcomes {
		if outcome.Commit == v11 && outcome.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("owned-context cancellation was recorded as a failed build: %+v", outcome)
		}
	}
}

func TestAdvanceOperationContextMergeDisposesCancellationForwarding(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	parent := context.WithValue(context.Background(), operationScopeTestKey{}, "store-operation")
	sharedContext := context.WithValue(context.Background(), operationScopeTestKey{}, "shared-store")
	shared := patchedForkStore()
	shared.Git = "fixture-git"
	shared.Timeout = 17 * time.Second
	shared.Detached = true
	shared.Env = []string{"YOLO_OPERATION_STORE=kept"}
	shared.Getenv = func(string) string { return "kept" }
	shared.Ctx = sharedContext
	shared.NoWait = true
	originalFactory := patchedAdvanceStore
	patchedAdvanceStore = func(bool) *packsrc.Store { return shared }
	t.Cleanup(func() { patchedAdvanceStore = originalFactory })

	a, early := newAdvance(fx.fork(t), advanceOptions{launch: true, operationCtx: parent})
	if early != nil {
		t.Fatalf("construct operation-scoped advance: %v", early)
	}
	if a.packs == shared {
		t.Fatal("advance reused the factory's shared Store instead of making an instance-local copy")
	}
	if a.packs.Ctx != parent {
		t.Errorf("instance Store context = %v, want operation parent", a.packs.Ctx)
	}
	if got := a.packs.Ctx.Value(operationScopeTestKey{}); got != "store-operation" {
		t.Errorf("instance Store lost operation value: %v", got)
	}
	if a.packs.Git != shared.Git || a.packs.Timeout != shared.Timeout || a.packs.Detached != shared.Detached ||
		a.packs.NoWait != shared.NoWait || len(a.packs.Env) != 1 || a.packs.Env[0] != shared.Env[0] || a.packs.Getenv == nil {
		t.Errorf("instance Store lost configured budgets or launch policy: %+v", a.packs)
	}
	if shared.Ctx != sharedContext || shared.Ctx.Value(operationScopeTestKey{}) != "shared-store" {
		t.Errorf("operation setup mutated shared Store context: %v", shared.Ctx)
	}
	assertAdvanceOperationContextMerge(t)
}

func assertAdvanceOperationContextMerge(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Minute).Round(0)
	parent, cancelParent := context.WithDeadline(context.WithValue(context.Background(), operationScopeTestKey{}, "parent"), deadline)
	defer cancelParent()
	scope, cancelScope := context.WithCancel(context.Background())
	merged, stopScope, cancelMerged := mergeAdvanceOperationContext(parent, scope)
	if got := merged.Value(operationScopeTestKey{}); got != "parent" {
		t.Errorf("merged context value = %v, want parent value", got)
	}
	if got, ok := merged.Deadline(); !ok || !got.Equal(deadline) {
		t.Errorf("merged deadline = %v, %v; want %v", got, ok, deadline)
	}
	if merged.Done() == nil {
		t.Fatal("merged context is not cancellable")
	}
	if !stopScope() {
		t.Fatal("scope cancellation forwarding was already running before cleanup")
	}
	cancelScope()
	select {
	case <-merged.Done():
		t.Fatal("a disposed scope-forwarding callback still cancelled the merged context")
	default:
	}
	cancelMerged()
	select {
	case <-merged.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("explicit merged-context cleanup did not cancel the context")
	}

	alreadyCancelled, cancelAlready := context.WithCancel(context.Background())
	cancelAlready()
	preCancelled, stopPreCancelled, cancelPreCancelled := mergeAdvanceOperationContext(context.Background(), alreadyCancelled)
	defer stopPreCancelled()
	defer cancelPreCancelled()
	if preCancelled.Err() != context.Canceled {
		t.Errorf("already-cancelled scope context error = %v, want immediate cancellation", preCancelled.Err())
	}

	parentForCancel, cancelParentForCancel := context.WithCancel(context.Background())
	scopeForParentCancel, cancelScopeForParentCancel := context.WithCancel(context.Background())
	fromParent, stopParentForward, cancelFromParent := mergeAdvanceOperationContext(parentForCancel, scopeForParentCancel)
	defer stopParentForward()
	defer cancelFromParent()
	cancelParentForCancel()
	select {
	case <-fromParent.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("operation-parent cancellation did not cancel merged context")
	}
	cancelScopeForParentCancel()

	parentForScopeCancel, cancelParentForScopeCancel := context.WithCancel(context.Background())
	defer cancelParentForScopeCancel()
	scopeForCancel, cancelScopeForCancel := context.WithCancel(context.Background())
	fromScope, stopScopeForward, cancelFromScope := mergeAdvanceOperationContext(parentForScopeCancel, scopeForCancel)
	defer stopScopeForward()
	defer cancelFromScope()
	cancelScopeForCancel()
	select {
	case <-fromScope.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("scope cancellation did not cancel merged context")
	}
}
