package run

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestActInterruptScopeRoutesSignalAndDisposesItsArm(t *testing.T) {
	outer := armInterruptScope(func(os.Signal) {})
	t.Cleanup(func() { outer.disarm() })
	launchArms.mu.Lock()
	baseline := len(launchArms.arms)
	signals := launchArms.sigs
	launchArms.mu.Unlock()
	if signals == nil {
		t.Fatal("outer arm did not install the in-process signal router")
	}

	act := &ActInterrupt{}
	entered := make(chan context.Context, 1)
	returned := make(chan os.Signal, 1)
	go func() {
		returned <- act.Scope(func(ctx context.Context) {
			entered <- ctx
			<-ctx.Done()
		})
	}()

	var scopeContext context.Context
	select {
	case scopeContext = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ActInterrupt.Scope did not enter its callback")
	}
	if scopeContext.Done() == nil {
		t.Fatal("scope callback received a context without cancellation")
	}
	launchArms.mu.Lock()
	active := len(launchArms.arms)
	launchArms.mu.Unlock()
	if active != baseline+1 {
		t.Fatalf("active launch-arm depth = %d, want baseline %d plus the scoped arm", active, baseline)
	}

	select {
	case signals <- syscall.SIGINT:
	case <-time.After(2 * time.Second):
		t.Fatal("could not feed SIGINT to the in-process launch signal router")
	}
	select {
	case <-scopeContext.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("routed SIGINT did not cancel the scope callback")
	}
	select {
	case sig := <-returned:
		if sig != syscall.SIGINT {
			t.Errorf("scope returned signal %v, want SIGINT", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ActInterrupt.Scope did not return after its callback ended")
	}
	if !act.Interrupted() {
		t.Error("ActInterrupt did not retain the routed SIGINT")
	}
	launchArms.mu.Lock()
	remaining := len(launchArms.arms)
	launchArms.mu.Unlock()
	if remaining != baseline {
		t.Errorf("launch-arm depth after scope = %d, want baseline %d", remaining, baseline)
	}
}
